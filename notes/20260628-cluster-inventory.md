# Expected cluster inventory after `make recreate-cluster` + `make all`

Date: 2026-06-28
Repository: /opt/data/src/github.com/dk1027/sandbox

## Authoritative sources

The source of truth is the repo's cluster bootstrap + deployment wiring:

- `Makefile`
  - `recreate-cluster` -> `infra/kind/bootstrap-kind-cluster.sh --recreate`
  - `all` -> `buildpush setup-strimzi setup-monitoring setup-kafka setup-logging setup-alerting setup-tracing deploy-apps deploy-chaos-monkey deploy-chaos-monkey-dashboard deploy-mcp-server deploy-dashboards`
- `infra/kind/bootstrap-kind-cluster.sh`
  - defines the cluster-level platform pieces: TLS registry, Kind cluster, Docker node limits, ingress-nginx, and the `local-registry-hosting` ConfigMap
- `README.md`
  - documents the intended stack and the `make all` flow
- `deploy/README.md`
  - documents the observability stack and the dashboard/logs/traces runbook
- `deploy/manifests/**` and `deploy/charts/**`
  - define the actual workloads, services, probes, dashboards, and ServiceMonitors

## Expected running set

### Cluster/bootstrap layer

These are created by `infra/kind/bootstrap-kind-cluster.sh` before `make all` runs:

- Docker registry container: `kind-registry` on `ryzen.local:5001`
- Kind cluster: `dev-cluster`
- ingress-nginx in namespace `ingress-nginx`
  - controller Deployment/Pod(s)
  - controller Service
  - wildcard/default TLS secret for HTTPS ingress
- `kube-public/local-registry-hosting` ConfigMap

### Monitoring namespace

Created by `make setup-monitoring` and `make deploy-dashboards` / `make deploy-mcp-server` / `make setup-alerting`:

- `prometheus-community/kube-prometheus-stack` release (`prometheus`) in `monitoring`
  - Prometheus
  - Alertmanager
  - Grafana
  - Prometheus Operator / CRD support
  - kube-state-metrics / node-exporter / related stack components
- Grafana ingress on `https://grafana.ryzen.local`
- `deploy/manifests/monitoring/alerting-rules.yaml`
  - Kafka alerts
  - chaos alerts
  - Kubernetes health alerts
- `mcp-server` Deployment + Service in `monitoring`
- Grafana dashboard ConfigMaps:
  - `kafka-cluster-dashboard`
  - `pipeline-dashboard`
  - `cluster-resources-dashboard`
  - `chaos-monkey-dashboard`

### Logging namespace

Created by `make setup-logging`:

- Loki release `loki` in `logging`
- Promtail release `promtail` in `logging`

### Tracing namespace

Created by `make setup-tracing`:

- Tempo release `tempo` in `tracing`

### Kafka namespace

Created by `make setup-strimzi` and `make setup-kafka`:

- Strimzi cluster operator release `strimzi-cluster-operator` in `kafka`
- Kafka CR / node pool / topic resources:
  - `KafkaNodePool pool-a`
  - `Kafka my-cluster`
  - `KafkaTopic test-topic`
- Strimzi-managed runtime from the Kafka CR:
  - 3 combined controller/broker pods from `pool-a`
  - Kafka bootstrap service and broker-related services
  - entity operator / exporter components created by Strimzi as part of the cluster
- Schema Registry Deployment/Service:
  - `confluent-sr`
- Kafka UI Deployment/Service/Ingress:
  - `kafka-ui`
  - host `kafka-ui.ryzen.local`
- Kafka ServiceMonitor resources:
  - broker/service monitor
  - exporter/service monitor

### Apps namespace

Created by `make deploy-apps` and `make deploy-chaos-monkey`:

- Kafka apps Helm release `kafka-apps` in `apps`
  - producer Deployment + Service + ServiceMonitor
  - consumer Deployment + Service + ServiceMonitor
- Chaos Monkey Helm release `chaos-monkey` in `apps`
  - controller Deployment + Service + ServiceMonitor
  - daemonSet + headless Service + ServiceMonitor

## What is not part of the default `make all` success state

- `deploy/charts/sre-agent` is present in the repo and has its own deploy target, but it is not invoked by the default `make all` target.
- If the SRE-agent stack is needed, it must be deployed separately with `make deploy-sre-agent`.

## Health checks / success criteria

A successful run is defined by the following evidence:

- Bootstrap succeeds and the ingress controller comes up ready.
  - `infra/kind/bootstrap-kind-cluster.sh` waits for the ingress-nginx controller pod to become Ready.
  - The registry and `local-registry-hosting` ConfigMap are present.
- Core namespaces exist and their workload pods are Ready:
  - `ingress-nginx`
  - `monitoring`
  - `logging`
  - `tracing`
  - `kafka`
  - `apps`
- Workload-level probes pass for repo-owned apps:
  - producer: `/metrics` readiness/liveness
  - consumer: `/metrics` readiness/liveness
  - mcp-server: `/metrics` readiness/liveness
  - chaos-monkey daemon/controller: `/metrics` ServiceMonitors and running pods
- Prometheus is configured to scrape the repo-owned metrics surfaces:
  - producer/consumer ServiceMonitors on port `metrics`
  - chaos-monkey controller/daemon ServiceMonitors on `/metrics`
  - mcp-server ServiceMonitor on `/metrics`
  - Kafka ServiceMonitors for broker/exporter metrics
- Grafana can load the provisioned dashboards from ConfigMaps labeled `grafana_dashboard: "1"`.
- Kafka UI responds through the ingress host or port-forward.
- Kafka health is visually trackable via the Kafka dashboard, especially:
  - under-replicated partitions = 0
  - offline partitions = 0
  - active controller count = 1
  - consumer lag stays bounded
- Kafka alert rules are present in `monitoring` and can fire if the cluster degrades.

## Practical verification commands referenced by the repo

- `kubectl get pods -n ingress-nginx`
- `kubectl get pods -A`
- `kubectl get svc -n kafka`
- `kubectl get configmap local-registry-hosting -n kube-public -o yaml`
- `make grafana-port-forward`
- `make grafana-password`
- `make kafka-ui-port-forward`
- `kubectl logs -f -l app=producer -n apps`
- `kubectl logs -f -l app=consumer -n apps`
- `kubectl logs -f -l app=chaos-monkey -n apps`

## Bottom line

After `make recreate-cluster` + `make all`, the expected healthy environment is a freshly recreated Kind cluster with ingress-nginx and local registry bootstrap complete, plus the Kafka, monitoring, logging, tracing, app, chaos, dashboard, and MCP workloads above running and scrapeable.
