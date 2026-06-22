# Local Kubernetes Kafka Environment

Welcome! This project provisions a complete local 5-node Kubernetes (`kind`) cluster containing a 3-broker Strimzi Kafka cluster, Confluent Schema Registry, a full Prometheus/Grafana observability stack, and custom Python test applications.

Everything is managed via Infrastructure as Code principles using a single entrypoint `Makefile`.

---

## Directory Layout

* **`Makefile`**: The central orchestrator for building images, configuring the cluster, and deploying resources.
* **`k8s/`**: 
  * `kind-config.yaml`: Configuration for the 5-node (1 control-plane, 4 workers) Kind cluster.
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
To spin up the entire environment from scratch, simply run:
```bash
make all
```

**What this does under the hood:**
1. Provisions the Kind cluster (`make cluster`).
2. Applies strict 1 CPU and 6GB memory resource limits per node using Docker constraints (`make apply-limits`).
3. Builds the Python test applications and loads their images directly into the local Kind registry (`make build`).
4. Installs the Strimzi Operator via Helm (`make setup-strimzi`).
5. Installs the KRaft-based Kafka cluster and Schema Registry (`make setup-kafka`).
6. Installs the `kube-prometheus-stack` to enable cluster-wide metric scraping (`make setup-monitoring`).
7. Deploys the Python apps using the local Helm chart (`make deploy-apps`).

---

## Development Workflow

### Making a Code Change and Redeploying
If you modify the Python application code in `src/producer/producer.py` or `src/consumer/consumer.py`, you can quickly rebuild the Docker images and trigger a rolling update of the deployments by running:

```bash
make redeploy-apps
```

This target builds the latest images, loads them into the kind nodes, triggers the helm upgrade, and gracefully restarts the application pods so they pull the updated images.

Similarly, if you modify the Go application code in `src/chaos_monkey/main.go`, you can rebuild and roll out updates using:

```bash
make redeploy-chaos-monkey
```

Or run the build and deploy steps individually:

```bash
make build-chaos-monkey
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
