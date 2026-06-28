# SRE-agent Architecture and Rollout Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Build a Go-based SRE-agent with one deployment per app that ingests app-scoped alerts, gathers observability context, asks a local OpenAI-compatible LLM for a conservative decision, and either recommends or executes only safe namespace-scoped remediations.

**Architecture:** Each app gets its own Kubernetes namespace, Deployment, ServiceAccount, Role/RoleBinding, ConfigMap, and optional Service so the agent only sees one app’s resources. Alertmanager delivers alerts to `/alerts`; the agent filters them by app and namespace, enriches them with Prometheus/Loki/Kubernetes context, sends a deterministic chat-completions request to the configured LLM endpoint, validates the response against policy, and then either escalates to a human or performs a bounded restart-style remediation. For local workstation/dev use, `LLM_BASE_URL` may point at `http://host.docker.internal:8080/v1`; for in-cluster/Kind runs, prefer a pod-local sidecar or cluster Service because `host.docker.internal` is not a reliable pod target.

**Tech Stack:** Go, `net/http`, Kubernetes RBAC, Prometheus HTTP API, Loki HTTP API, OpenAI-compatible `/v1/chat/completions`, Helm chart values for deployment wiring.

---

## 1. Target runtime model

### Per-app isolation boundary
- One SRE-agent instance per app.
- Namespace is the primary tenancy boundary.
- Labels/selectors must include the app identity, e.g. `app=<app-name>`.
- Read access can extend to cluster observability systems; write access must remain namespace-scoped.

### Pod layout
- `sre-agent` container: HTTP webhook server and decision engine.
- Optional `llm-server` sidecar: exposes the OpenAI-compatible API inside the pod at `127.0.0.1:<port>/v1` for in-cluster runs.
- `ServiceAccount` + `Role`/`RoleBinding`: allow only the read/write verbs needed for the allowlisted remediation actions.
- `Service`: only if external systems need a stable in-cluster target for the webhook.

### LLM connectivity
- Production-ready MVP should not depend on the host gateway from inside Kubernetes.
- Support two modes through config:
  - local/dev: `http://host.docker.internal:8080/v1`
  - in-cluster: sidecar or Service URL reachable from the pod
- The LLM contract stays OpenAI-compatible so the agent code does not care which backend serves it.

---

## 2. Alert intake and scoping

### Intake contract
- Accept Alertmanager webhook payloads on `/alerts`.
- Parse alert labels and annotations into a normalized signal.
- Required identity fields: `app`, `namespace`, `alertname`, `severity`, `summary`.

### Scope filter
- Drop alerts not matching the agent’s `APP_NAME` and `APP_NAMESPACE`.
- Ignore resolved or otherwise non-actionable alerts.
- Ignore watcher-style noise such as `Watchdog` / `DeadMansSwitch`.
- Never fan out across apps or namespaces.

### Decision gating
- If the alert is ambiguous, missing evidence, or below confidence threshold, escalate rather than act.
- If the LLM returns any unsafe action, strip it and force escalation.
- Only accept restart-style remediations in MVP.

---

## 3. Control loop

1. Receive alert webhook.
2. Normalize and verify app scope.
3. Enrich the incident with recent Prometheus metrics, Loki logs, and Kubernetes status/events.
4. Build a concise prompt with incident facts and action policy.
5. Call the LLM with deterministic settings (`temperature: 0`, `stream: false`).
6. Decode strict JSON from the model response.
7. Enforce policy and confidence thresholds.
8. Execute a safe remediation or return an escalation result.
9. Emit logs and metrics for the decision path.

### LLM response shape
The assistant response should be strict JSON with at least:
- `summary`
- `diagnosis`
- `confidence`
- `severity`
- `recommended`
- `actions`
- `escalate`
- `escalation_note` (optional)

Actions should be normalized to a small allowlist such as:
- `restart`
- `rollout_restart`

Anything else is advisory only and must not be executed.

---

## 4. Configuration model

### Required env vars
- `APP_NAME`
- `APP_NAMESPACE`
- `LISTEN_ADDR`
- `LLM_BASE_URL`
- `LLM_API_KEY`
- `LLM_MODEL`
- `REMEDIATION_MODE` (`recommend` default, `act` gated)
- `CONFIDENCE_THRESHOLD`

### Suggested chart values
- `app.name`
- `app.namespace`
- `llm.baseUrl`
- `llm.model`
- `llm.apiKey`
- `llm.sidecar.enabled`
- `runtime.remediationMode`
- `runtime.confidenceThreshold`
- `alertRouting.receiver`
- `alertRouting.matchLabels`

### Secret vs ConfigMap split
- Put non-sensitive defaults in ConfigMaps.
- Put LLM keys or other credentials in Secrets only if the target backend needs them.
- Avoid embedding policy decisions in image code; keep them in values/config.

---

## 5. Observability requirements

### Logging
Emit structured logs for each webhook with:
- outcome (`ignored`, `processed`)
- classification (`actionable`, `non-actionable`, `ambiguous`)
- app / namespace / alert name
- action status (`recommended`, `escalated`, `diagnosed`, `remediated`)
- skipped / guardrail reason

### Metrics
MVP should expose a `/metrics` endpoint and dashboardable counters/histograms for:
- alerts received / processed / ignored
- alerts escalated / remediated
- LLM request count, failures, and latency
- remediation attempts and failures
- guardrail blocks by reason

### Dashboard
Provide a Grafana dashboard for each app namespace that shows:
- alert volume and classification
- LLM latency / error rate
- remediation outcomes
- escalation rate

The project is not production-ready until the agent is both running and observable in Grafana.

---

## 6. Remediation policy

### Allowed actions
- Restart a deployment, statefulset, or daemonset in the same namespace.
- Use idempotent patch/update operations only.

### Disallowed actions for MVP
- Deleting workloads or namespaces.
- Cross-namespace writes.
- Scaling down without explicit policy support.
- Any cluster-admin style operation.

### Safety rules
- Confidence below threshold must force escalation.
- Any unsupported or unsafe action must be removed from the response.
- If no safe actions remain, return a recommendation or escalation instead of acting.

---

## 7. Current code mapping

The current repository already has the basic shape of this design:
- `src/sre-agent/cmd/sre-agent/main.go` wires the HTTP server and alert webhook handler.
- `src/sre-agent/internal/config/config.go` loads `APP_NAME`, `APP_NAMESPACE`, `LLM_BASE_URL`, `LLM_MODEL`, `REMEDIATION_MODE`, and `CONFIDENCE_THRESHOLD`.
- `src/sre-agent/internal/alert/alert.go` normalizes Alertmanager webhook payloads and applies app/namespace matching.
- `src/sre-agent/internal/llm/client.go` speaks OpenAI-compatible `/chat/completions` and decodes strict JSON decisions.
- `src/sre-agent/internal/agent/service.go` classifies alerts, applies guardrails, and gates remediation.
- `src/sre-agent/internal/remediation/kube.go` performs namespace-scoped restart patches for allowed workload kinds.
- `deploy/charts/sre-agent/values.yaml` and `deploy/charts/sre-agent/templates/deployment.yaml` define the one-deployment-per-app chart wiring.

---

## 8. Rollout plan for the MVP

### Phase 1: Wire the per-app runtime
- Keep the app namespace, service account, and chart values per app.
- Ensure alert routing preserves `app` and `namespace` labels.
- Default to `recommend` mode.

### Phase 2: Make LLM connectivity reliable
- Prefer the in-pod sidecar or in-cluster Service.
- Keep `host.docker.internal:8080/v1` only as a dev override.
- Add timeouts and fail-closed behavior around the LLM client.

### Phase 3: Add observability
- Expose `/metrics`.
- Add dashboard panels for alert, LLM, and remediation health.
- Verify the dashboard shows per-app status after a synthetic alert.

### Phase 4: Enable bounded automation
- Gate `act` mode behind app-specific values.
- Allow only restart-style remediations.
- Verify foreign alerts and low-confidence alerts are escalated, not acted on.

---

## 9. Acceptance criteria

- One deployment per app, namespace-isolated and independently configurable.
- Alertmanager webhooks are processed only for the matching app and namespace.
- The agent can talk to the configured LLM endpoint and decode structured JSON decisions.
- Only allowlisted restart-style actions can be executed in `act` mode.
- Resolved, foreign, or ambiguous alerts are ignored or escalated appropriately.
- Logs, metrics, and a Grafana dashboard exist for the agent.
- `make check` and `make build` remain green after implementation.

---

## 10. Implementation notes for follow-up workers

- Keep the service conservative; prefer escalation over action.
- Treat the LLM as advisory and policy-bounded, never authoritative.
- Keep all writes namespace-scoped and idempotent.
- When adding new integrations, prefer read-only data collection first, then add an explicit allowlist for any write path.
- If a future worker needs deeper observability enrichment, add it behind a small interface so it can be mocked in tests.
