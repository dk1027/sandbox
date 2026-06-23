# Local Kubernetes Kafka Environment

Welcome! This project deploys a Kafka test stack into the shared Kind development cluster: a 3-broker Strimzi Kafka cluster, Confluent Schema Registry, a full Prometheus/Grafana observability stack, and custom test applications.

Cluster bootstrap is owned by `../infra/kind/bootstrap-kind-cluster.sh`. The `Makefile` in this directory installs and updates the Kafka/application workloads after the cluster exists.

---

## Directory Layout

* **`Makefile`**: Workload orchestration for image build/push and Kubernetes/Helm deploys.
* **`k8s/`**: 
  * `kind-config.yaml`: Legacy Kind config for ad-hoc local clusters. The primary bootstrap path is `../infra/kind/bootstrap-kind-cluster.sh`.
  * `kafka-cluster.yaml`: Strimzi CRD manifests using the modern KRaft architecture and `KafkaNodePool`s.
  * `schema-registry.yaml`: A standard deployment and service for Confluent Schema Registry.
* **`src/`**: 
  * `producer/`: Python source, `requirements.txt`, and `Dockerfile` for the Kafka producer application. Produces Avro payloads using `confluent-kafka` and exposes `messages_published_total` with `succeed`, `queued`, and `reason` labels.
  * `consumer/`: Python source, `requirements.txt`, and `Dockerfile` for the Kafka consumer application. Exposes `messages_consumed_total` metrics.
  * `chaos_monkey/`: Go source, `go.mod`, and `Dockerfile` for the Chaos Monkey placeholder application.
* **`helm/`**: 
  * `kafka-apps/`: A custom Helm chart that deploys the producer and consumer applications. It wires up `ServiceMonitor` resources so that the Prometheus stack scrapes them automatically.
  * `chaos-monkey/`: A custom Helm chart that deploys the Go-based Chaos Monkey placeholder application.

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

Then install the Kafka/application stack from this directory:
```bash
make all
```

**What this does under the hood:**
1. Installs the Strimzi Operator via Helm (`make setup-strimzi`).
2. Installs the KRaft-based Kafka cluster, Schema Registry, and Kafka UI (`make setup-kafka`).
3. Installs the `kube-prometheus-stack` to enable cluster-wide metric scraping (`make setup-monitoring`).
4. Deploys the Python producer/consumer apps using the local Helm chart (`make deploy-apps`).
5. Deploys the Chaos Monkey controller/daemonset and dashboard (`make deploy-chaos-monkey`, `make deploy-chaos-monkey-dashboard`).

`make all` assumes the cluster already exists and that the referenced application images are available in the registry. Cluster creation, node resource limits, the TLS registry, and ingress-nginx are handled by the bootstrap script.

---

## Development Workflow

### Making a Code Change and Redeploying
Images default to the TLS registry at `ryzen.local:5001` and tag `dev`. Override with `REGISTRY` and `IMAGE_TAG` as needed:

```bash
make build-and-push IMAGE_TAG=$(git rev-parse --short HEAD)
```

If you modify the Python application code in `src/producer/producer.py` or `src/consumer/consumer.py`, you can rebuild, push, deploy, and restart the deployments by running:

```bash
make redeploy-apps
```

This target builds images for `linux/amd64`, pushes them to `$(REGISTRY)`, runs the Helm upgrade, and restarts the application pods so they pull the updated tag.

Similarly, if you modify the Go application code in `src/chaos_monkey/main.go`, you can rebuild and roll out updates using:

```bash
make redeploy-chaos-monkey
```

Or run the build and deploy steps individually:

```bash
make build-chaos-monkey-image
make push-chaos-monkey-image
make deploy-chaos-monkey
```

---

## Teardown

To destroy the cluster and delete all resources entirely, run:

```bash
make teardown
```

---

## Quick Start: Chaos Monkey

To run a ready-made chaos demo:

```bash
./scripts/demo-chaos-monkey.sh
```

This applies:
- the Grafana dashboard ConfigMap
- a producer packet-loss experiment
- a Kafka broker blackhole experiment

For parameter details and custom examples, see `docs/chaos-monkey-user-guide.md`.

## Accessing Services & Observability

### Accessing Grafana
To access the Grafana dashboards and view the custom metrics, you can use the built-in Make targets:

For a full walk-through of the chaos experiment fields and examples, see:

- `docs/chaos-monkey-user-guide.md`

1. Deploy the chaos-monkey dashboard into the monitoring namespace:
```bash
make deploy-chaos-monkey-dashboard
```

2. Retrieve your auto-generated admin password:
```bash
make grafana-password
```

3. Run the demo script to apply the dashboard and start the example packet-loss / blackhole experiments:
```bash
./scripts/demo-chaos-monkey.sh
```

4. Port-forward the Grafana service to your localhost:
```bash
make grafana-port-forward
```

5. Open [http://localhost:3000](http://localhost:3000) in your browser and log in with username **`admin`** and the password retrieved above.

If you expose Grafana through ingress later, that Ingress configuration should live with the monitoring Helm values rather than in the Kind bootstrap script.

### Accessing Kafka UI
To explore Kafka topics, view messages, and manage Schema Registry through a web interface:

1. Port-forward the Kafka UI service:
```bash
make kafka-ui-port-forward
```

2. Open [http://localhost:8080](http://localhost:8080) in your browser.

### Viewing App Logs
If you want to debug the test applications directly via standard output:
```bash
# View Producer logs
kubectl logs -f -l app=producer -n apps

# View Consumer logs
kubectl logs -f -l app=consumer -n apps

# View Chaos Monkey logs
kubectl logs -f -l app=chaos-monkey -n apps
```
