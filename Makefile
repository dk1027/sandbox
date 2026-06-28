CLUSTER_NAME ?= dev-cluster
KUBE_CONTEXT ?= kind-$(CLUSTER_NAME)
REGISTRY ?= ryzen.local:5001
IMAGE_TAG ?= dev
IMAGE_PLATFORM ?= linux/amd64
LOKI_CHART_VERSION ?= 7.0.0
PROMTAIL_CHART_VERSION ?= 6.17.1
PYTHON_SOURCES := src/producer/producer.py src/consumer/consumer.py src/mcp-server/server.py

KUBECTL ?= kubectl --context $(KUBE_CONTEXT)
HELM ?= helm --kube-context $(KUBE_CONTEXT)
DOCKER ?= docker
KIND ?= kind
SRE_AGENT_NAMESPACE ?= apps
SRE_AGENT_APP_NAME ?= sre-agent
SRE_AGENT_APP_NAMESPACE ?= $(SRE_AGENT_NAMESPACE)
SRE_AGENT_RELEASE ?= sre-agent$(if $(filter-out sre-agent,$(SRE_AGENT_APP_NAME)),-$(SRE_AGENT_APP_NAME),)

.PHONY: all \
	bootstrap-cluster recreate-cluster cluster apply-limits teardown \
	build push buildpush \
	build-apps push-apps build-chaos-monkey push-chaos-monkey build-mcp-server push-mcp-server build-sre-agent push-sre-agent \
	setup-strimzi setup-kafka setup-monitoring setup-logging setup-alerting setup-tracing \
	deploy-apps deploy-chaos-monkey deploy-chaos-monkey-dashboard deploy-mcp-server deploy-sre-agent deploy-dashboards \
	redeploy-apps redeploy-chaos-monkey \
	grafana-port-forward grafana-password kafka-ui-port-forward mcp-port-forward \
	check check-python

# Install the application stack into an already bootstrapped Kind cluster.
# Cluster creation, node limits, the TLS registry, and ingress-nginx are owned by
# infra/kind/bootstrap-kind-cluster.sh.
all: setup-strimzi setup-kafka setup-monitoring setup-logging setup-alerting setup-tracing deploy-apps deploy-chaos-monkey deploy-chaos-monkey-dashboard deploy-mcp-server deploy-dashboards

bootstrap-cluster:
	infra/kind/bootstrap-kind-cluster.sh

recreate-cluster:
	infra/kind/bootstrap-kind-cluster.sh --recreate

# Backward-compatible alias. Prefer `make bootstrap-cluster` or
# `make recreate-cluster` so destructive cluster recreation is explicit.
cluster: bootstrap-cluster

# Node resource limits are handled by the bootstrap script. Keep this target as a
# compatibility shim for old docs/scripts that may still call it.
apply-limits:
	@echo "Node resource limits are handled by infra/kind/bootstrap-kind-cluster.sh"

build:
	$(MAKE) -C src build

push:
	$(MAKE) -C src push

buildpush:
	$(MAKE) -C src buildpush

build-apps:
	$(MAKE) -C src build-apps

push-apps:
	$(MAKE) -C src push-apps

build-chaos-monkey:
	$(MAKE) -C src build-chaos-monkey

push-chaos-monkey:
	$(MAKE) -C src push-chaos-monkey

build-mcp-server:
	$(MAKE) -C src build-mcp-server

push-mcp-server:
	$(MAKE) -C src push-mcp-server

build-sre-agent:
	$(MAKE) -C src build-sre-agent

push-sre-agent:
	$(MAKE) -C src push-sre-agent

setup-strimzi:
	$(HELM) repo add strimzi https://strimzi.io/charts/
	$(HELM) repo update
	$(HELM) upgrade --install strimzi-cluster-operator strimzi/strimzi-kafka-operator --namespace kafka --create-namespace

setup-kafka:
	$(KUBECTL) wait --for=condition=established --timeout=60s crd/kafkas.kafka.strimzi.io
	$(KUBECTL) apply -f deploy/manifests/kafka/kafka-metrics-configmap.yaml -n kafka
	$(KUBECTL) apply -f deploy/manifests/kafka/kafka-cluster.yaml -n kafka
	$(KUBECTL) apply -f deploy/manifests/kafka/schema-registry.yaml -n kafka
	$(KUBECTL) apply -f deploy/manifests/kafka/kafka-ui.yaml -n kafka
	# Apply ServiceMonitors for Kafka components
	$(KUBECTL) apply -f deploy/manifests/kafka/kafka-servicemonitor.yaml -n kafka
	$(KUBECTL) apply -f deploy/manifests/kafka/kafka-exporter-servicemonitor.yaml -n kafka

setup-monitoring:
	$(HELM) repo add prometheus-community https://prometheus-community.github.io/helm-charts
	$(HELM) repo update
	$(HELM) upgrade --install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace \
		-f deploy/values/prometheus-values.yaml

setup-logging:
	$(HELM) repo add grafana https://grafana.github.io/helm-charts
	$(HELM) repo update
	$(HELM) upgrade --install loki grafana/loki --version $(LOKI_CHART_VERSION) --namespace logging --create-namespace \
		-f deploy/values/loki-values.yaml
	$(HELM) upgrade --install promtail grafana/promtail --version $(PROMTAIL_CHART_VERSION) --namespace logging --create-namespace \
		-f deploy/values/promtail-values.yaml

setup-alerting:
	$(KUBECTL) apply -f deploy/manifests/monitoring/alerting-rules.yaml

setup-tracing:
	$(HELM) repo add grafana https://grafana.github.io/helm-charts
	$(HELM) repo update
	$(HELM) upgrade --install tempo grafana/tempo --namespace tracing --create-namespace \
		-f deploy/values/tempo-values.yaml

deploy-apps:
	$(HELM) upgrade --install kafka-apps deploy/charts/kafka-apps --namespace apps --create-namespace \
		--set producer.image.repository=$(REGISTRY)/kafka-producer \
		--set producer.image.tag=$(IMAGE_TAG) \
		--set consumer.image.repository=$(REGISTRY)/kafka-consumer \
		--set consumer.image.tag=$(IMAGE_TAG)

deploy-chaos-monkey:
	$(HELM) upgrade --install chaos-monkey deploy/charts/chaos-monkey --namespace apps --create-namespace \
		--set image.repository=$(REGISTRY)/chaos-monkey \
		--set image.tag=$(IMAGE_TAG)

deploy-chaos-monkey-dashboard:
	$(KUBECTL) apply -f deploy/manifests/monitoring/chaos-monkey-dashboard.yaml

deploy-mcp-server:
	$(KUBECTL) apply -f deploy/manifests/monitoring/mcp-server.yaml
	$(KUBECTL) apply -f deploy/manifests/monitoring/mcp-server-servicemonitor.yaml

deploy-sre-agent:
	$(HELM) upgrade --install $(SRE_AGENT_RELEASE) deploy/charts/sre-agent --namespace $(SRE_AGENT_NAMESPACE) --create-namespace \
		--set app.name=$(SRE_AGENT_APP_NAME) \
		--set app.namespace=$(SRE_AGENT_APP_NAMESPACE)

deploy-dashboards:
	$(KUBECTL) apply -f deploy/manifests/monitoring/kafka-dashboard.yaml
	$(KUBECTL) apply -f deploy/manifests/monitoring/pipeline-dashboard.yaml
	$(KUBECTL) apply -f deploy/manifests/monitoring/cluster-resources-dashboard.yaml
	$(KUBECTL) apply -f deploy/manifests/monitoring/sre-agent-dashboard.yaml

check: check-python

check-python:
	uv run --group dev mypy $(PYTHON_SOURCES)
	uv run --group dev ruff check $(PYTHON_SOURCES)

teardown:
	$(KIND) delete cluster --name $(CLUSTER_NAME)

redeploy-apps: build-apps push-apps deploy-apps
	$(KUBECTL) rollout restart deployment -n apps kafka-apps-producer kafka-apps-consumer

redeploy-chaos-monkey: build-chaos-monkey push-chaos-monkey deploy-chaos-monkey
	$(KUBECTL) rollout restart deployment -n apps chaos-monkey-controller
	$(KUBECTL) rollout restart daemonset -n apps chaos-monkey-daemon

grafana-port-forward:
	@echo "Forwarding Grafana to http://localhost:3000..."
	@POD_NAME=$$($(KUBECTL) get pods --namespace monitoring -l "app.kubernetes.io/name=grafana" -o jsonpath="{.items[0].metadata.name}"); \
	$(KUBECTL) --namespace monitoring port-forward $$POD_NAME 3000

grafana-password:
	@echo "Grafana Admin Password:"
	@$(KUBECTL) get secret --namespace monitoring -l app.kubernetes.io/component=admin-secret -o jsonpath="{.items[0].data.admin-password}" | base64 --decode ; echo

kafka-ui-port-forward:
	@echo "Forwarding Kafka UI to http://localhost:8080..."
	@POD_NAME=$$($(KUBECTL) get pods --namespace kafka -l "app=kafka-ui" -o jsonpath="{.items[0].metadata.name}"); \
	$(KUBECTL) --namespace kafka port-forward $$POD_NAME 8080:8080

mcp-port-forward:
	@echo "Forwarding MCP server to http://localhost:8080..."
	@POD_NAME=$$($(KUBECTL) get pods --namespace monitoring -l "app=mcp-server" -o jsonpath="{.items[0].metadata.name}"); \
	$(KUBECTL) --namespace monitoring port-forward $$POD_NAME 8080
