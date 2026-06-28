# Deployment Artifacts

This directory contains Kubernetes deployment artifacts for the local Kafka test stack.

## Layout

- `charts/`: Local Helm charts owned by this repository.
  - `kafka-apps/`: Producer and consumer application deployments, services, and ServiceMonitors.
  - `chaos-monkey/`: Chaos Monkey CRDs, controller deployment, daemonset, and RBAC.
  - `sre-agent/`: Per-application SRE agent with bounded restart-style remediation. The chart includes per-app receiver wiring (`values.yaml`, `values-producer.yaml`, `values-consumer.yaml`) and operator verification guidance in its README.
- `manifests/`: Raw Kubernetes YAML for resources that do not currently need a local Helm chart.
  - `kafka/`: Strimzi Kafka CRs, Schema Registry, and Kafka UI.
  - `chaos/`: Demo and example ChaosExperiment manifests.
  - `monitoring/`: Dashboard ConfigMaps, SRE-agent dashboards, and related monitoring manifests.
- `values/`: Values files for third-party Helm charts.
  - `prometheus-values.yaml`: Values for `prometheus-community/kube-prometheus-stack`.

For the concrete Alertmanager routing and synthetic alert smoke-test commands, see `notes/20260627-sre-agent-routing-verification.md`.

## Operational runbook

Use this guide when an incident needs the observability stack end to end. Start with dashboards to understand the shape of the problem, move to logs for the exact failure, and finish with traces when you need request-level latency or dependency context. The three surfaces answer different questions:

- Dashboard: what changed, how broad it is, and which service looks unhealthy
- Logs: the exact error, request, or crash signature
- Traces: where a request spent time and which hop failed

### 1. Dashboards: identify the failing surface

Open Grafana at `https://grafana.ryzen.local` if ingress-nginx is available, or use `make grafana-port-forward` and browse to the forwarded local URL.

Use the SRE Agent dashboard when you need to confirm alert handling, LLM latency, or remediation behavior. In Grafana, open Dashboards and search for `SRE Agent Observability`. The dashboard is provisioned from `deploy/manifests/monitoring/sre-agent-dashboard.yaml` and is tagged with `grafana_dashboard: "1"`, so it should appear automatically once Grafana reloads dashboards from the `monitoring` namespace.

Panels to inspect first:

- Webhook Outcomes: confirms whether the agent is processing, ignoring, or erroring on incoming alerts
- LLM P95 Latency: shows whether the model backend is responding within a reasonable bound
- Remediation Actions: shows the mix of recommended vs. remediated actions when `remediationMode=act` is enabled
- Guardrail Blocks: indicates escalations, missing safe actions, or other reasons the agent refused to act
- Webhook Errors: should remain at zero; any non-zero value points to payload parsing, dependency, or availability failures

A healthy dashboard usually has recent webhook traffic and either low counts or zero counts on the error and guardrail panels. An empty dashboard is often expected if no matching alerts have been sent yet.

If the dashboard is unavailable or empty:

1. Verify Grafana is reachable. If ingress is not configured, use the port-forward target instead of the public hostname.
2. Verify the dashboard ConfigMap exists in `monitoring` and still carries the `grafana_dashboard: "1"` label.
3. Verify Prometheus is scraping the SRE-agent service: the chart's ServiceMonitor should target the `/metrics` endpoint on the `http` port at a 15s interval.
4. Verify the SRE-agent pods are running and that matching alerts have actually been sent. The dashboard panels remain empty until webhook traffic exists.
5. If Grafana is running but the dashboard still does not appear, restart Grafana so it reloads provisioned dashboard files from ConfigMaps.

### 2. Logs: narrow to the failing component

For Kubernetes workloads, start with pod logs and narrow to the smallest scope that still shows the failure:

```bash
kubectl get pods -n <namespace>
kubectl logs -n <namespace> <pod-name>
kubectl logs -n <namespace> <pod-name> -c <container-name>
kubectl logs -n <namespace> <pod-name> -c <container-name> --previous
```

If the workload is controlled by a Deployment, StatefulSet, or DaemonSet, you can target the owning resource directly:

```bash
kubectl logs -n <namespace> deploy/<deployment-name>
kubectl logs -n <namespace> statefulset/<statefulset-name>
kubectl logs -n <namespace> daemonset/<daemonset-name>
```

Filter by request ID, trace ID, or correlation ID whenever you have one:

```bash
kubectl logs -n <namespace> <pod-name> | grep '<request-id>'
kubectl logs -n <namespace> <pod-name> | grep '<trace-id>'
```

If a centralized log platform exists in the environment, use it first for cross-service searches and longer retention, then fall back to pod logs for the exact line-level detail. When only raw text is available, search around the incident timestamp and look for the first error, not just the last one.

Common log signatures and what they usually mean:

- `timeout` / `deadline exceeded`: dependency slowness, network issue, or a request budget that is too tight
- `connection refused` / `no route to host`: service discovery, pod readiness, or network path issue
- `401` / `403`: auth, token, or permission issue
- `500` / unhandled exception: application bug or bad input
- `OOMKilled` / process restart: memory pressure or runaway workload
- `rate limit` / `throttled`: dependency protection or client burst behavior

### 3. Traces: follow the request path

The cluster uses Grafana Tempo for distributed traces. Grafana is preconfigured with a Tempo datasource at `http://tempo.tracing.svc.cluster.local:3200`, and the app pods export OTLP traces to `http://tempo.tracing.svc.cluster.local:4317`.

To find a trace:

1. Open Grafana and go to Explore.
2. Select the Tempo datasource.
3. Use the most specific identifier you have, in this order:
   - Trace ID: paste the trace ID directly into the trace lookup field.
   - Request ID: search for the request ID carried in logs or span attributes, then narrow to the matching trace.
   - Service: filter by `service.name` to narrow the view to the app that emitted the span. For this repo, the service name defaults to the chart fullname unless `observability.otelServiceName` is set.
4. Once you have the candidate trace, open it in the trace view and jump to the failing request timestamp from the alert or log line.

What to inspect in the trace view:

- Root span duration: this is the end-to-end latency for the request path
- Waterfall timing: look for large gaps, fan-out, or repeated spans
- Span attributes: check HTTP status, exception/error fields, Kafka topic/offset data, and any request or correlation IDs
- Error spans and red bars: these identify the exact operation that failed
- Service graph / dependency edges: use these when the latency crosses service boundaries

Common patterns and what they usually mean:

- One long root span with short children: the request spent most of its time waiting in the app or blocked on external I/O
- A single long child span after a downstream call: the downstream dependency is slow
- Repeated spans for the same operation: retries or a retry storm
- Gaps between spans: queueing, lock contention, backpressure, or client timeouts
- Fast-failing red spans: validation, auth, or configuration errors that fail before any real work starts
- No matching trace for a request: tracing may be missing, sampled away, or emitted under the wrong service name; confirm the request reached the app and check the paired logs

### 4. Suggested incident order

1. Check the dashboard to decide whether the problem is broad or isolated.
2. Use logs to find the first concrete error and the exact request or crash signature.
3. Pivot to traces when you need latency breakdowns, hop-by-hop timing, or dependency boundaries.
4. If the evidence still conflicts, trust the live pod/log/trace data over an empty or stale dashboard.

## Convention

Use Helm charts when a repo-owned app has a release lifecycle, image settings, or environment-specific values. Use raw manifests for simple static resources, demos/examples, CR instances, and third-party chart values. Do not move a resource into Helm just because it is Kubernetes YAML.
