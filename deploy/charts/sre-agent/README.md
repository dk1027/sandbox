# SRE Agent Chart

This chart deploys one isolated SRE-agent instance per application.

## Remediation modes

The agent supports two modes:

- `recommend` (default): the service analyzes alerts and returns a bounded recommendation payload, but does not modify cluster resources.
- `act`: the service will execute only restart-style remediations in-cluster by patching the target workload pod template annotations. Supported automated actions are `restart` and `rollout_restart` against `deployment`, `statefulset`, or `daemonset` targets.

Any other action type remains recommendation-only and should be surfaced to a human reviewer. The chart RBAC grants the agent namespaced patch/update access for the workload kinds above so these bounded rollouts can happen without cluster-admin privileges.

## Observability

The chart exposes a `/metrics` endpoint on the main service so Prometheus can scrape the agent itself. A matching ServiceMonitor template is included in the chart, and the repository also ships a Grafana dashboard ConfigMap under `deploy/manifests/monitoring/sre-agent-dashboard.yaml`. The optional LLM sidecar proxies to the host OpenAI-compatible backend when `llm.sidecar.upstreamBaseUrl` is set. For the default MacBook dev service, the chart also injects a host alias for `Longs-MacBook-Pro.local` so the pod can resolve the mDNS name inside Kubernetes, and the upstream proxy timeout is lengthened so slow model loads can still complete.

## Dashboard runbook

The full dashboard/logs/traces workflow now lives in `../../README.md#operational-runbook` so the incident guidance stays in one place. Use that guide when you need to inspect the SRE Agent dashboard, correlate it with logs, or pivot to Tempo traces.

This chart still provisions the SRE Agent dashboard from `deploy/manifests/monitoring/sre-agent-dashboard.yaml`, and Grafana should surface it automatically once the `monitoring` namespace dashboards are reloaded.

## Alert routing

The chart values are wired to the Alertmanager receiver names used in `deploy/values/prometheus-values.yaml`:

- default `values.yaml` -> receiver `sre-agent`
- `values-producer.yaml` -> receiver `producer-sre-agent`
- `values-consumer.yaml` -> receiver `consumer-sre-agent`

Each receiver points to the matching in-namespace Service on `/alerts` with `send_resolved: true`, so a reviewer can confirm the routing path by sending a synthetic alert with `app` and `namespace` labels that match only one release.

## Operator verification

1. Install or upgrade the chart for the target app/namespace.
2. Confirm the Alertmanager config contains the expected receiver name and webhook URL.
3. Send a synthetic firing alert with `app=<release app>` and `namespace=<release namespace>` labels.
4. Confirm the intended agent responds with the expected classification/action status and that foreign labels are ignored.

## Values

See `values.yaml` for the application name, namespace, alert routing, LLM endpoint, and remediation settings.
