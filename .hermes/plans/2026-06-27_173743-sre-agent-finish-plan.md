# SRE-agent Finishing Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Finish the SRE-agent so it is not just buildable, but actually aligned with the repository’s SRE objective: it must consume real observability context, use a reachable in-cluster LLM backend, process only in-scope alerts, and expose enough metrics/dashboards to prove safe operation.

**Architecture:** Keep the existing conservative control loop and namespace-scoped remediation model. The minimum missing work is to (1) wire live Prometheus/Loki/Kubernetes context into the decision prompt, (2) remove the demo-only LLM path by making the pod use a real OpenAI-compatible backend reachable inside the cluster, and (3) verify the end-to-end alert routing, metrics, and dashboard experience with smoke tests and deployment documentation. Do not broaden the remediation catalog until the existing restart-only path is fully observable and testable.

**Tech Stack:** Go, Helm, Kubernetes RBAC, Prometheus HTTP API, Loki HTTP API, OpenAI-compatible `/v1/chat/completions`, Grafana dashboard JSON, Alertmanager routing.

---

## Assumptions and scope

- The current webhook filtering, conservative `recommend`/`act` split, and namespace-scoped restart remediation are the correct product direction and should be preserved.
- The first shippable version should still default to recommendation mode unless a deployment explicitly opts into act mode.
- The agent should not depend on `host.docker.internal` from inside Kubernetes; if a pod-local sidecar is not acceptable, the LLM backend must be moved behind an in-cluster Service.
- We are not adding new remediation types in this finishing pass. The MVP remains restart / rollout-restart only.
- The repo already contains enough observability primitives; the remaining work is to connect them to live data and prove the dashboard works.

---

## Priority 0: make the runtime actually see real observability context

### Task 1: Load observability settings into config and make them available to the agent

**Objective:** Ensure the agent can read the observability endpoints already present in the chart values and pass them through the control loop.

**Files:**
- Modify: `src/sre-agent/internal/config/config.go`
- Modify: `src/sre-agent/internal/config/config_test.go`
- Modify: `deploy/charts/sre-agent/templates/configmap.yaml`
- Modify: `deploy/charts/sre-agent/values.yaml`
- Modify: `deploy/charts/sre-agent/README.md`

**Implementation notes:**
- Add typed fields for `PrometheusURL`, `LokiURL`, and the OTEL settings to `config.Config`.
- Parse them in `LoadFromEnvMap` with explicit required/default behavior.
- Keep the fail-closed behavior for missing required runtime inputs.
- Surface the new config in the chart ConfigMap so the pod receives the same values the chart already advertises.

**Validation:**
- Add/extend unit tests for config parsing and required-field behavior.
- Re-run `go test ./...` in `src/sre-agent`.

**Dependency:** None.

---

### Task 2: Enrich alert handling with live Prometheus/Loki/Kubernetes evidence

**Objective:** Replace the current “labels and annotations only” prompt with one that includes recent metrics, logs, and workload status.

**Files:**
- Modify: `src/sre-agent/internal/agent/service.go`
- Modify: `src/sre-agent/internal/llm/client.go`
- Add or modify: `src/sre-agent/internal/observability/*`
- Add tests: `src/sre-agent/internal/agent/service_test.go`
- Add tests: `src/sre-agent/internal/observability/metrics_test.go` if new metrics are used

**Implementation notes:**
- Introduce small interfaces for context collection so the agent can be tested without a live cluster.
- Collect a bounded slice of evidence only: recent error-rate or latency data from Prometheus, recent matching log lines from Loki, and current workload status / events from Kubernetes.
- Keep prompts concise and deterministic; the LLM should receive a structured summary, not a dump of raw logs.
- If any evidence source is unavailable or times out, continue with the remaining evidence and record the missing source as a guardrail/diagnostic reason instead of failing open.

**Validation:**
- Unit-test prompt construction and the fallback behavior when a source is unavailable.
- Add a test proving the agent still escalates when confidence is low even if context is present.

**Dependency:** Task 1.

---

### Task 3: Keep metrics honest and dashboardable

**Objective:** Ensure the metrics emitted by the agent line up with the actual decision flow and are visible in Grafana.

**Files:**
- Modify: `src/sre-agent/internal/observability/metrics.go`
- Add tests: `src/sre-agent/internal/observability/metrics_test.go`
- Modify: `deploy/manifests/monitoring/sre-agent-dashboard.yaml`
- Modify: `deploy/charts/sre-agent/templates/servicemonitor.yaml`
- Modify: `deploy/charts/sre-agent/README.md`

**Implementation notes:**
- Keep the current counters/histograms, but add any missing dimensions needed to distinguish ignored / escalated / remediated paths.
- Add dashboard panels that make the finish criteria visible: webhook outcome rate, LLM error rate, confidence/guardrail blocks, and remediation success/failure.
- Keep the dashboard simple enough that a reviewer can confirm the agent is alive and behaving conservatively from one screen.

**Validation:**
- `kubectl`/Prometheus smoke check that the ServiceMonitor selects the deployment.
- Confirm the dashboard JSON still loads via the repo’s Grafana dashboard ConfigMap convention.

**Dependency:** Tasks 1–2.

---

## Priority 1: remove the demo-only LLM path and make in-cluster connectivity real

### Task 4: Replace the heuristic LLM sidecar with a real OpenAI-compatible runtime path

**Objective:** Remove the fake/demo-only behavior so the deployed pod talks to a real LLM endpoint that is reachable inside the cluster.

**Files:**
- Modify or replace: `deploy/charts/sre-agent/files/openai_server.py`
- Modify: `deploy/charts/sre-agent/templates/deployment.yaml`
- Modify: `deploy/charts/sre-agent/templates/llm-server-configmap.yaml`
- Modify: `deploy/charts/sre-agent/templates/configmap.yaml`
- Modify: `deploy/charts/sre-agent/values.yaml`
- Modify: `src/sre-agent/internal/llm/client.go`
- Modify: `src/sre-agent/internal/llm/client_test.go`

**Implementation notes:**
- Prefer a cluster-reachable backend: either a genuine sidecar wrapper around an OpenAI-compatible local model server or an in-cluster Service.
- Stop relying on `host.docker.internal` as the production pod path; keep it only as a local developer override if needed.
- Add explicit timeout, context cancellation, and fail-closed behavior to the client so a broken model backend never causes unsafe action.
- Keep the model contract stable: strict JSON decision payloads with deterministic settings.

**Validation:**
- Unit-test client failure modes and timeout behavior.
- Deploy the chart into the dev cluster and verify `/healthz` and one synthetic `/alerts` request complete with the real backend path.

**Dependency:** Tasks 1–3.

---

### Task 5: Prove end-to-end alert routing works for the matching app only

**Objective:** Make the Alertmanager routing path concrete so alerts land only in the intended app/namespace instance.

**Files:**
- Modify: `deploy/values/prometheus-values.yaml`
- Modify: `deploy/manifests/monitoring/alerting-rules.yaml`
- Modify: `deploy/README.md`
- Modify: `README.md` if needed for operator-facing instructions

**Implementation notes:**
- Ensure routing labels carry both `app` and `namespace` through to the correct receiver.
- Make the route and receiver names match the chart values so the app-specific release is not guesswork.
- Document the minimum operational steps for verifying one synthetic alert reaches exactly one agent instance.

**Validation:**
- Use a synthetic alert payload to confirm a foreign app is ignored and the matching app is processed.
- Confirm the resulting result payload and logs include the expected classification and action status.

**Dependency:** Tasks 1–4.

---

## Priority 2: lock in safe automation and ship readiness

### Task 6: Add smoke tests for the safe-action contract

**Objective:** Verify the non-negotiable safety behavior before calling the work complete.

**Files:**
- Add or modify: `src/sre-agent/internal/agent/service_test.go`
- Add or modify: `src/sre-agent/internal/remediation/kube_test.go`
- Add or modify: `src/sre-agent/cmd/sre-agent/main_test.go`
- Potentially add a repo-level smoke test script or make target if one does not already exist

**Implementation notes:**
- Cover at least these cases: ignored foreign alert, ignored resolved/watchdog alert, low-confidence escalation, unsafe action removal, and bounded restart-style remediation in `act` mode.
- Make sure a failed LLM or unavailable remediator does not silently turn into an unsafe action.
- Keep tests hermetic where possible; use fake clients and fake remediators rather than live cluster dependencies.

**Validation:**
- `go test ./...`
- `make check`
- `make build`

**Dependency:** Tasks 1–5.

---

### Task 7: Finish operator docs and release criteria

**Objective:** Turn the implementation into something another engineer can deploy and verify without tribal knowledge.

**Files:**
- Modify: `deploy/charts/sre-agent/README.md`
- Modify: `deploy/README.md`
- Modify: `README.md`
- Add if useful: a short runbook under `notes/` for deployment verification and rollback

**Implementation notes:**
- Document the required values for app name, namespace, LLM endpoint, remediation mode, and confidence threshold.
- Include the minimal verification checklist: chart install, rollout status, `/healthz`, `/metrics`, synthetic alert, and dashboard check.
- Note the rollback stance: disable `act` mode first if anything looks unsafe.

**Validation:**
- A reviewer can follow the docs to deploy the agent, send a synthetic alert, and confirm the Grafana dashboard changes without reading source code.

**Dependency:** Tasks 1–6.

---

## Final acceptance criteria for the finished agent

- The agent reads real observability settings and uses them in its decision path.
- The deployed pod reaches a real OpenAI-compatible LLM backend from inside Kubernetes.
- Alertmanager routing sends only matching app/namespace alerts to the corresponding agent instance.
- The agent remains conservative: foreign, resolved, ambiguous, or low-confidence alerts are ignored or escalated, not auto-remediated.
- Restart-style remediation is namespaced, bounded, and covered by tests.
- `/metrics`, the ServiceMonitor, and the Grafana dashboard show the agent’s live decision health.
- `make check` and `make build` stay green.

---

## Recommended execution order

1. Task 1: config + env wiring
2. Task 2: observability enrichment
3. Task 3: metrics and dashboard alignment
4. Task 4: real LLM backend path
5. Task 5: alert routing verification
6. Task 6: smoke tests for safety guarantees
7. Task 7: docs and release criteria

---

## Engineering note

The current repository already has a strong skeleton. The finishing work should stay focused on closing the remaining product gap, not rewriting the architecture. The right bar is: conservative decisioning, real in-cluster reachability, and a dashboard that proves it is safe to operate.
