# SRE Agent Design Doc (Retroactive)

Date: 2026-06-28
Status: retrospective design for the implementation currently in this repository

## 1. Purpose

The SRE Agent is a per-application alert triage and optional remediation service for this repository's Kubernetes demo stack.

It exists to:

- receive Alertmanager webhooks for a specific app + namespace pair
- ignore alerts that are out of scope or clearly non-actionable
- gather observability context from Prometheus, Loki, and Kubernetes
- ask a local OpenAI-compatible LLM for a conservative diagnosis
- either recommend an action or apply a bounded restart-style remediation
- expose metrics so the whole workflow can be observed in Grafana

The current implementation is intentionally narrow: it is not a general autonomous incident responder, and it is not allowed to perform arbitrary cluster changes.

## 2. Design goals

1. Per-app isolation
   - Each deployment handles one application identity only.
   - Alert routing is keyed on `app` and `namespace` so producer, consumer, and default SRE-agent instances stay separated.

2. Safe automation
   - Default mode is recommendation-only.
   - Act mode can only perform restart-style remediation on deployments, statefulsets, and daemonsets.
   - Unsafe actions are removed before execution.

3. Evidence-based decisions
   - The agent should not rely on the LLM alone.
   - It enriches prompts with query results from Prometheus, Loki, and Kubernetes when those backends are available.

4. Observable behavior
   - Webhook, LLM, remediation, and guardrail activity all emit Prometheus metrics.
   - The chart also ships a Grafana dashboard and ServiceMonitor so the service can be monitored in-cluster.

5. Operable in a local dev cluster
   - The repo is built around a local Kubernetes environment with Kafka, Prometheus, Grafana, Loki, Tempo, and an in-cluster MCP server.
   - The SRE Agent must run without cluster-admin privileges.

## 3. Scope

In scope:

- Alertmanager webhook intake
- alert filtering by app and namespace
- LLM-based classification and recommendation
- restart-style remediation for workloads
- Prometheus metrics and Grafana dashboarding
- Kubernetes API access through a service account
- optional LLM sidecar for local dev convenience

Out of scope:

- arbitrary mutation of cluster resources
- multi-tenant routing across unrelated applications inside one deployment
- full incident management workflow
- auto-scaling, traffic shifting, or database recovery actions
- human approval UI

## 4. System overview

High-level flow:

1. Alertmanager sends a webhook to the SRE Agent service.
2. The service parses the alert payload into one or more signals.
3. It keeps only signals that match its configured app and namespace.
4. For actionable signals, it collects evidence from:
   - Prometheus
   - Loki
   - Kubernetes
5. It sends a constrained prompt to the configured LLM backend.
6. The decision is normalized and filtered by safety rules.
7. Depending on remediation mode:
   - recommend: return a recommendation only
   - act: apply restart-style remediation if safe and available
8. The service records metrics for the webhook, LLM call, remediation, and guardrail decision.

Logical topology:

```text
Alertmanager
   |
   |  webhook (/alerts)
   v
SRE Agent Service  -----> Prometheus
   |                    ---> Loki
   |                    ---> Kubernetes API
   |                    ---> LLM backend / sidecar
   v
JSON response + metrics
```

## 5. Deployment model

The Helm chart deploys one isolated SRE Agent instance per application.

Current wired releases:

- default `sre-agent`
- `sre-agent-producer`
- `sre-agent-consumer`

Each release gets its own:

- Deployment
- Service
- ServiceMonitor
- alert routing receiver name
- app/namespace matching rules

This means the producer and consumer agents do not compete for the same webhook stream. Each only sees alerts whose labels match its own configured scope.

## 6. Request lifecycle

### 6.1 Webhook intake

The webhook endpoint accepts Alertmanager payloads and parses the `alerts` array into internal signals.

Important fields used by the parser:

- `app`
- `namespace`
- `alertname`
- `severity`
- `summary` or `description`
- `startsAt`

A signal matches if both of these are true:

- app is empty or equals the configured app
- namespace is empty or equals the configured namespace

Signals outside the agent's scope are ignored.

### 6.2 Classification gate

Before calling the LLM, the service performs a simple gate:

- alerts not in `firing` state are treated as non-actionable
- known watcher alerts such as `Watchdog` are ignored
- alerts missing a summary are treated as ambiguous

Only actionable alerts proceed to evidence collection and LLM evaluation.

### 6.3 Evidence collection

If configured, the service collects context from three surfaces:

- Prometheus: request rate and p95 latency queries for the app/namespace pair
- Loki: recent error or general log search for the app/namespace pair
- Kubernetes: matching workloads and recent events in the target namespace

The collector is best-effort:

- each backend can fail independently
- missing backends are recorded in diagnostics rather than failing the request outright
- the prompt includes whatever evidence is available

### 6.4 LLM decision step

The service sends a two-message request to the LLM:

- a system instruction that constrains the model to deterministic, conservative JSON output
- a user prompt containing the alert, evidence, and routing context

The expected decision object contains:

- summary
- diagnosis
- confidence
- severity
- recommended
- actions
- escalate
- escalation_note

The model temperature is set to 0.

### 6.5 Normalization and guardrails

After the response is decoded, the service normalizes it before any action is taken:

- deduplicates actions
- trims whitespace
- sorts actions for deterministic output
- removes unsafe actions
- blocks unsupported action types
- preserves only restart or rollout_restart for automated remediation

If the model asks to escalate, the request is returned as escalated and no remediation is applied.

### 6.6 Remediation

When `REMEDIATION_MODE=act`, the service can apply bounded restart-style remediation.

Supported targets:

- deployment
- statefulset
- daemonset

Supported actions:

- restart
- rollout_restart

Implementation detail:

- the service patches the workload pod template annotations with a timestamped restart marker
- this forces a rollout without needing cluster-admin privileges
- the Kubernetes client uses the pod's service account token and namespace-scoped RBAC

If the mode is `recommend`, the service returns the decision but does not patch workloads.

## 7. Configuration model

The service is configured through environment variables injected by the Helm chart.

Key configuration groups:

### Identity

- `APP_NAME`
- `APP_NAMESPACE`

### LLM

- `LLM_BASE_URL`
- `LLM_API_KEY`
- `LLM_MODEL`

### Runtime policy

- `REMEDIATION_MODE` (`recommend` or `act`)
- `CONFIDENCE_THRESHOLD`

### Observability sources

- `PROMETHEUS_URL`
- `LOKI_URL`
- `OTEL_SERVICE_NAME`
- `OTEL_EXPORTER_OTLP_ENDPOINT`

### Alert routing metadata

- `ALERT_ROUTING_RECEIVER`
- `ALERT_ROUTING_WEBHOOK_PATH`
- `ALERT_ROUTING_MATCH_LABELS`
- `ALERT_ROUTING_GROUP_BY`

The Helm chart provides per-release values so producer and consumer instances can point at different Alertmanager receivers while reusing the same codebase.

## 8. Observability design

The service emits first-class metrics for the key path components:

- webhook outcome and decision state
- webhook latency
- LLM request count and latency
- remediation attempts by type and result
- guardrail blocks by reason

This is what makes the system operable rather than merely functional.

Dashboard intent:

- Webhook Outcomes: traffic and classification health
- LLM P95 Latency: model responsiveness
- Remediation Actions: recommendation vs remediation split
- Guardrail Blocks: policy rejections and missing-safe-action cases
- Webhook Errors: hard failures that should stay near zero

The repo also provisions a ServiceMonitor, so Prometheus can scrape the agent directly.

## 9. Security and trust boundaries

The design intentionally keeps trust narrow.

### Kubernetes permissions

The service account only needs namespaced access to:

- get/list/watch selected resources
- patch/update workloads when act mode is enabled

The RBAC is scoped to the application namespace and workload kinds that can be safely restarted.

### LLM output is never trusted blindly

Even if the LLM suggests an action, the service still:

- normalizes the response
- filters unsupported actions
- blocks unsafe targets
- can escalate instead of acting

### Network boundaries

The agent talks to:

- Alertmanager over in-cluster HTTP
- Prometheus, Loki, and Kubernetes over in-cluster service endpoints
- a local LLM backend, sometimes via a sidecar for developer convenience

## 10. LLM sidecar pattern

The deployment can include an `llm-server` sidecar.

Why it exists:

- simplifies local development
- proxies requests to a host OpenAI-compatible backend
- avoids hardcoding the main app to one specific provider implementation

Tradeoff:

- it adds one more moving part to the pod
- the main service now depends on the sidecar being healthy when the sidecar is enabled

The design accepts this because the repo is optimized for a local cluster workflow, not for minimal production footprint.

## 11. Failure modes

Expected failure modes and handling:

1. Alert parsing fails
   - request returns error
   - webhook metric records error outcome

2. Alert does not match app/namespace
   - signal is ignored
   - no LLM call is made

3. Observability backends are unavailable
   - prompt evidence is sparse
   - diagnostics explain the missing sources
   - the request can still be processed conservatively

4. LLM backend unavailable or malformed response
   - request fails
   - metrics show LLM error

5. No safe actions are produced
   - request is diagnosed but not remediated
   - guardrail block is recorded

6. Unsupported remediation requested
   - action is rejected before execution
   - guardrail metric increments

7. Kubernetes patch fails
   - remediation returns an error
   - remediation metric records failure

## 12. Tradeoffs

### Why one agent per app?

Pros:

- simpler routing
- smaller blast radius
- easier to reason about who owns which alerts
- cleaner dashboards and receiver mapping

Cons:

- more releases to manage
- some duplication in config and resources

The repo chooses isolation over consolidation.

### Why use an LLM at all?

Pros:

- useful for structured diagnosis from messy alert + log context
- can summarize evidence across systems

Cons:

- nondeterminism risk
- latency and external dependency
- needs guardrails and careful prompt shaping

The implementation mitigates this with temperature 0, strong prompting, and action filtering.

### Why bounded remediation only?

Pros:

- safer automation boundary
- understandable rollback path
- fits a demo cluster and early production trust model

Cons:

- cannot auto-fix everything
- some incidents still need a human or a more advanced workflow

This is a deliberate product choice.

## 13. What this design is not

This is not:

- a full incident commander
- a generic multi-tenant automation platform
- a remote shell for the cluster
- a replacement for human review

It is a scoped, observable, conservative SRE assistant.

## 14. Open follow-ups

The current implementation leaves room for future hardening:

- richer remediation catalog with explicit approvals
- stronger prompt-evidence ranking and summarization
- better per-app dashboards and alert routing smoke tests
- more exhaustive end-to-end verification of the live LLM backend path
- additional runbook guidance for common failure signatures

## 15. Summary

The SRE Agent in this repository is a deliberately bounded alert triage and restart-remediation system. Its architecture is built around per-app isolation, conservative LLM use, and first-class observability. The design favors safety, determinism, and operational clarity over breadth of automation.
