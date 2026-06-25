# Observability Status and Follow-up Plan (2026-06-25)

## Current Status

Observability work since `ea0c793` is partially implemented and partially running.

What is already in place:
- Prometheus + Grafana are deployed and running in `monitoring`
- ServiceMonitors exist for producer/consumer apps and chaos-monkey components
- Alerting rules are installed and healthy in Prometheus
- Grafana dashboards exist for:
  - Kafka cluster health
  - Pipeline throughput
  - Cluster resources
  - Chaos monkey
- An MCP server is deployed in-cluster and reachable over `/sse`
- Grafana is configured with Loki and Tempo datasources in values

What is still missing or incomplete:
- Kafka broker metrics are not actually being scraped yet
- Kafka UI metrics are not active in Prometheus
- Schema Registry metrics are not active in Prometheus
- Loki is not deployed/running in the cluster
- Promtail or another log collector is not deployed/running
- Tempo is not deployed/running in the cluster
- Tracing instrumentation is not added to producer/consumer apps
- The repo does not yet show a full end-to-end verified observability stack

Verification findings:
- `monitoring` namespace exists and core Prometheus/Grafana components are running
- `logging` namespace does not exist
- `tracing` namespace does not exist
- Prometheus has the new alert rule groups loaded and healthy
- Prometheus currently shows active targets for producer/consumer and chaos-monkey, but not the Kafka broker/UI/schema-registry targets
- `mcp-server` pod is running and `/sse` returns HTTP 200
- `make build` fails in this environment because Docker is unavailable (`Cannot connect to the Docker daemon`)
- The MCP server image was verified successfully with BuildKit

## Plan

- [ ] Enable Kafka broker JMX/exporter metrics in the Kafka CR / Strimzi config
- [ ] Verify Kafka broker metrics are exposed on the expected Service/port
- [ ] Make Prometheus scrape Kafka broker metrics successfully
- [ ] Verify Kafka broker metrics appear in Prometheus targets and queries
- [ ] Verify or fix Kafka UI metrics exposure and scraping
- [ ] Verify or fix Schema Registry metrics exposure and scraping
- [ ] Deploy Loki in a `logging` namespace
- [ ] Deploy a log collector (Promtail or Fluent Bit) as a DaemonSet
- [ ] Verify logs are flowing into Loki
- [ ] Confirm Grafana can query Loki from the configured datasource
- [ ] Deploy Tempo in a `tracing` namespace
- [ ] Verify Tempo is reachable from Grafana
- [ ] Add OpenTelemetry tracing to producer and consumer apps
- [ ] Emit spans across producer -> Kafka -> consumer path
- [ ] Verify traces are visible in Tempo/Grafana
- [ ] Review and tighten Prometheus alert rules as needed
- [ ] Validate all dashboards render and use real available metrics
- [ ] Verify the MCP server supports the intended Prometheus/kubectl/Kafka/Loki operations end to end
- [ ] Run the full repo build/deploy verification path and record what passes/fails
- [ ] Close any gaps between the implementation and the original observability requirements in `notes/20260623-observability.md`
