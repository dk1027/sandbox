# Local Kubernetes Kafka Environment

This repository deploys a Kafka test stack into a shared Kind development cluster: a 3-broker Strimzi Kafka cluster, Confluent Schema Registry, Kafka UI, Prometheus/Grafana observability, Python producer/consumer test apps, and the Go-based Chaos Monkey controller/daemon.

Cluster bootstrap is owned by `infra/kind/bootstrap-kind-cluster.sh`. The root `Makefile` installs and updates Kubernetes workloads after the cluster exists.

---

## Directory Layout

* **`infra/`**: Shared infrastructure for the local test environment.
  * `kind/`: Kind cluster bootstrap, local TLS registry, ingress-nginx, and legacy Kind examples.
  * `docs/`: Infrastructure/network topology documentation.
* **`src/`**: Application source code.
  * `producer/`: Python Kafka producer app, requirements, and Dockerfile.
  * `consumer/`: Python Kafka consumer app, requirements, and Dockerfile.
  * `chaos-monkey/`: Go Chaos Monkey source, Dockerfile, scripts, and project-specific docs/designs.
* **`deploy/`**: Kubernetes deployment artifacts.
  * `charts/`: Local Helm charts for repo-owned apps.
  * `manifests/`: Raw Kubernetes manifests, CRs, demos, and dashboards.
  * `values/`: Values files for third-party Helm charts.
* **`notes/`**: Planning notes and TODO checklists.
* **`generated/`**: Ignored local artifacts such as generated certs and registry data.
* **`Makefile`**: Workload orchestration for image build/push and Kubernetes/Helm deploys.

See `deploy/README.md` for the chart/manifest/value split.

The retroactive design doc for the SRE Agent lives at `src/sre-agent/design.md`.

---

## Initializing the Environment

### Prerequisites

Make sure you have the following CLI tools installed:

- `docker`
- `kind`
- `kubectl`
- `helm`

### Spin Up

First bootstrap the shared Kind development cluster from the repository root:

```bash
infra/kind/bootstrap-kind-cluster.sh --recreate
```

Then install the Kafka/application stack:

```bash
make all
```

What this does under the hood:

0. Builds and pushes all repo images (`make buildpush`).
1. Installs the Strimzi Operator via Helm (`make setup-strimzi`).
2. Installs the KRaft-based Kafka cluster, Schema Registry, and Kafka UI (`make setup-kafka`).
3. Installs the `kube-prometheus-stack` to enable cluster-wide metric scraping (`make setup-monitoring`).
4. Deploys the Python producer/consumer apps using `deploy/charts/kafka-apps` (`make deploy-apps`).
5. Deploys the Chaos Monkey controller/daemonset and dashboard (`make deploy-chaos-monkey`, `make deploy-chaos-monkey-dashboard`).

`make all` assumes the cluster already exists. Cluster creation, node resource limits, the TLS registry, and ingress-nginx are handled by the bootstrap script.

If you recreate the cluster, refresh any copied kubeconfig with:

```bash
ssh ltse@ryzen.local 'cd /opt/data/src/github.com/dk1027/sandbox && make kubeconfig' > ~/.kube/kind-ryzen/dev-cluster.yaml
```

That target emits a kubeconfig with the API server already rewritten to `https://ryzen.local:6443`, so the file can be used directly from the MacBook.

---

## Development Workflow

Images default to the TLS registry at `ryzen.local:5001` and tag `dev`. Override with `REGISTRY`, `IMAGE_TAG`, `IMAGE_PLATFORM`, and `BUILD_BACKEND` as needed.

`BUILD_BACKEND` defaults to `auto`:
- `auto` uses the remote buildx/BuildKit backend when `BUILDKIT_HOST` is set, which matches the Hermes container and `agents/docker-compose.yml`
- `docker` forces the local Docker daemon path via `docker buildx build --load`
- `buildkit` forces the remote BuildKit path via `docker buildx build --push` or OCI export for build-only runs

Examples:

```bash
make buildpush IMAGE_TAG=$(git rev-parse --short HEAD)
BUILD_BACKEND=buildkit make buildpush IMAGE_TAG=$(git rev-parse --short HEAD)
```

If you modify the Python application code in `src/producer/producer.py` or `src/consumer/consumer.py`, rebuild, push, deploy, and restart the deployments with:

```bash
make redeploy-apps
```

If you modify the Go application code in `src/chaos-monkey/`, rebuild and roll out updates with:

```bash
make redeploy-chaos-monkey
```

Or run the build and deploy steps individually:

```bash
make build-chaos-monkey
make push-chaos-monkey
make deploy-chaos-monkey
```

---

## Quick Start: Chaos Monkey

To run a ready-made chaos demo:

```bash
src/chaos-monkey/scripts/demo-chaos-monkey.sh
```

This applies:

- the Grafana dashboard ConfigMap from `deploy/manifests/monitoring/chaos-monkey-dashboard.yaml`
- producer and Kafka broker chaos experiments from `deploy/manifests/chaos/demo-chaos-experiments.yaml`

For parameter details and custom examples, see `src/chaos-monkey/docs/chaos-monkey-user-guide.md`.

---

## Accessing Services & Observability

### Accessing Grafana

1. Deploy the Chaos Monkey dashboard into the monitoring namespace:

```bash
make deploy-chaos-monkey-dashboard
```

2. Retrieve your auto-generated admin password:

```bash
make grafana-password
```

3. Run the demo script to apply the dashboard and start the example packet-loss / blackhole experiments:

```bash
src/chaos-monkey/scripts/demo-chaos-monkey.sh
```

4. If the Kind cluster was bootstrapped with ingress-nginx, open Grafana at:

```text
https://grafana.ryzen.local
```

Alternatively, port-forward the Grafana service to your localhost:

```bash
make grafana-port-forward
```

5. Log in with username `admin` and the password retrieved above.

For the full operational runbook covering dashboards, logs, and traces, see `deploy/README.md#operational-runbook`.

### Accessing Kafka UI

To explore Kafka topics, view messages, and manage Schema Registry through a web interface:

1. If the Kind cluster was bootstrapped with ingress-nginx, open Kafka UI at:

```text
https://kafka-ui.ryzen.local
```

Alternatively, port-forward the Kafka UI service:

```bash
make kafka-ui-port-forward
```

2. If using port-forward, open http://localhost:8080 in your browser.

### Viewing App Logs

```bash
kubectl logs -f -l app=producer -n apps
kubectl logs -f -l app=consumer -n apps
kubectl logs -f -l app=chaos-monkey -n apps
```

---

## Teardown

To destroy the cluster and delete all resources entirely, run:

```bash
make teardown
```
