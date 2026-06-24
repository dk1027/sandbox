# Observability Plan for SRE Agent (2026-06-24)

## Goal

Give an autonomous SRE agent a 360-degree view of the cluster so it can detect, diagnose, and respond to incidents without human intervention. The agent needs metrics, logs, and direct cluster access via MCP.

---

## Current State (what we have)

- **Prometheus + Grafana** via `kube-prometheus-stack` in `monitoring` namespace
- **ServiceMonitors** for producer and consumer apps (15s scrape interval)
- **Chaos Monkey dashboard** (Grafana ConfigMap) — 4 panels
- **Producer metrics**: `messages_published_total` counter (succeed, queued, reason labels)
- **Consumer metrics**: `messages_consumed_total` counter
- **Chaos Monkey daemon metrics**: `chaos_monkey_active_targets`, `injected_faults_total`, `recovered_faults_total`
- **Grafana exposed** at `grafana.ryzen.local`

## What is missing

| Area | Gap |
|---|---|
| Kafka broker metrics | Strimzi JMX exporter not scraping broker-level metrics (partitions, under-replicated, consumer lag) |
| Schema Registry metrics | No ServiceMonitor or metrics endpoint |
| Consumer lag | Not tracked — critical for detecting pipeline stalls |
| Logs | No centralized log aggregation (Loki/Fluent Bit) |
| Alerting | No Alertmanager rules defined |
| Tracing | No distributed tracing (Jaeger/Tempo) |
| MCP access | No MCP server for the SRE agent to query Prometheus, kubectl, or Kafka |
| Cluster-level dashboards | No node/resource utilization or pod health dashboard |

---

## Plan

### Phase 1: Kafka broker metrics (highest ROI)

Strimzi ships a JMX exporter sidecar. Enable it and expose broker metrics to Prometheus.

1. **Enable JMX metrics in the Kafka CR** — add `metricsConfig` to `deploy/manifests/kafka/kafka-cluster.yaml` with JMX exporter enabled.
2. **Add a ServiceMonitor for Kafka brokers** — create `deploy/manifests/kafka/kafka-servicemonitor.yaml` that selects the JMX exporter service Strimzi creates.
3. **Add a ServiceMonitor for Kafka UI** — scrape its `/metrics` endpoint if available.
4. **Add a ServiceMonitor for Schema Registry** — Confluent Schema Registry exposes metrics on port 8081; add annotations or a ServiceMonitor.

Key metrics to capture:
- `kafka_server_replicamanager_underreplicatedpartitions`
- `kafka_server_brokertopicmetrics_messagesinpersec`
- `kafka_server_brokertopicmetrics_bytesinpersec` / `bytesoutpersec`
- `kafka_consumer_group_members_in_group` (consumer group health)
- Consumer group lag (from `kafka_consumer_group`)

### Phase 2: Centralized logging with Loki

Add Grafana Loki + Promtail (or Fluent Bit) so the SRE agent can query logs.

1. **Deploy Loki stack** via `grafana/loki-stack` Helm chart in a `logging` namespace.
2. **Configure Promtail as a DaemonSet** to collect container logs from all namespaces.
3. **Add Loki as a Grafana datasource** — update `prometheus-values.yaml` or create a separate ConfigMap.
4. **Label pods** with `app` and `component` labels so log queries are structured.

This gives the agent `logql` access to correlate metric anomalies with log events.

### Phase 3: Alerting rules

Define Prometheus alerting rules so the agent (and humans) get notified before things break.

1. **Create `deploy/manifests/monitoring/alerting-rules.yaml`** — a `PrometheusRule` CRD with:
   - `KafkaUnderReplicatedPartitions` — fires when under-replicated partitions > 0 for 2m
   - `KafkaConsumerLagHigh` — fires when consumer lag exceeds threshold
   - `KafkaBrokerDown` — fires when a broker stops reporting metrics
   - `ProducerPublishFailures` — fires when publish failure rate > 10% over 5m
   - `ConsumerThroughputZero` — fires when consumer throughput drops to 0 for 5m
   - `ChaosExperimentActive` — informational alert when chaos is running
   - `PodCrashLooping`, `NodeNotReady` — standard K8s alerts
2. **Wire rules into the prometheus-values.yaml** so kube-prometheus-stack picks them up.

### Phase 4: MCP server for the SRE agent

Deploy an MCP server inside the cluster that the SRE agent can call to:

1. **Prometheus MCP tool** — query PromQL, list targets, fetch alert status.
   - Use the Prometheus HTTP API (`/api/v1/query`, `/api/v1/query_range`, `/api/v1/targets`, `/api/v1/alerts`).
   - Expose via port-forward or an Ingress route (`prometheus.ryzen.local`).
2. **kubectl MCP tool** — `get pods`, `describe pod`, `get events`, `logs`, `top nodes/pods`.
   - Run as a sidecar or standalone pod with a ServiceAccount that has cluster-wide `get`/`list`/`watch` permissions.
3. **Kafka MCP tool** — list topics, describe consumer groups, check lag, list schemas.
   - Use `kafka-console-consumer`, `kafka-consumer-groups.sh`, or the Kafka Admin API.
   - Can run as a pod in the `kafka` namespace with network access to the brokers.
4. **Loki MCP tool** — query logs via LogQL.

Options for MCP deployment:
- **In-cluster MCP server pod** — a single container exposing an MCP endpoint over HTTP/SSE, with tools for each data source. The SRE agent connects via port-forward.
- **Host-side MCP client** — run MCP on the Linux host with kubectl access, forwarding requests into the cluster.

Recommendation: in-cluster pod with a lightweight HTTP MCP server. Gives the agent a single endpoint to talk to.

### Phase 5: Dashboards

Create or update Grafana dashboards for the SRE agent's reference:

1. **Kafka cluster health dashboard** — broker metrics, partition counts, under-replicated partitions, consumer group lag.
2. **Pipeline throughput dashboard** — producer publish rate, consumer consume rate, end-to-end lag.
3. **Cluster resource dashboard** — CPU/memory per node, pod resource usage, PVC utilization.
4. **Chaos experiment dashboard** — existing one, but expand to include Kafka broker impact correlation.

Import dashboards as ConfigMaps with the `grafana_dashboard: "1"` label (same pattern as chaos-monkey-dashboard).

### Phase 6: Tracing (optional, lower priority)

Add Jaeger or Grafana Tempo for distributed tracing across producer -> Kafka -> consumer.

1. Deploy Tempo via the kube-prometheus-stack or standalone.
2. Instrument producer and consumer with OpenTelemetry SDK (Python).
3. Add Tempo as a Grafana datasource.

This is lower priority for a local test cluster but valuable for demonstrating end-to-end latency analysis.

---

## Execution order

```
Phase 1 (Kafka metrics)  -->  Phase 2 (Loki logging)  -->  Phase 3 (Alerting)
                                                                        |
Phase 5 (Dashboards)  <--  Phase 4 (MCP server)  <--  +
```

Phase 1 is the highest-impact quick win — Strimzi already ships the JMX exporter, we just need to enable it and add a ServiceMonitor. Phase 4 (MCP) is the enabler for the SRE agent itself and can be built in parallel with Phases 2-3.

---

## File changes summary

| File | Action |
|---|---|
| `deploy/manifests/kafka/kafka-cluster.yaml` | Add `metricsConfig` with JMX exporter |
| `deploy/manifests/kafka/kafka-servicemonitor.yaml` | New — ServiceMonitor for Kafka JMX |
| `deploy/manifests/kafka/schema-registry-servicemonitor.yaml` | New — ServiceMonitor for Schema Registry |
| `deploy/values/prometheus-values.yaml` | Add `prometheusRule` config, Loki datasource |
| `deploy/manifests/monitoring/alerting-rules.yaml` | New — PrometheusRule CRD |
| `deploy/manifests/monitoring/loki-stack-values.yaml` | New — Loki Helm values |
| `deploy/charts/chaos-monkey/templates/` | Add ServiceMonitor for chaos-monkey controller |
| `Makefile` | Add `setup-logging`, `setup-alerting`, `setup-mcp` targets |
| `src/producer/producer.py` | Add consumer lag / latency metrics |
| `src/consumer/consumer.py` | Add consumer lag / latency metrics |
