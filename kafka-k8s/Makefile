CLUSTER_NAME ?= dev-cluster
KUBE_CONTEXT ?= kind-$(CLUSTER_NAME)
REGISTRY ?= ryzen.local:5001
IMAGE_TAG ?= dev
IMAGE_PLATFORM ?= linux/amd64

KUBECTL ?= kubectl --context $(KUBE_CONTEXT)
HELM ?= helm --kube-context $(KUBE_CONTEXT)
DOCKER ?= docker
KIND ?= kind

.PHONY: all \
	bootstrap-cluster recreate-cluster cluster apply-limits teardown \
	build build-app-images push push-app-images build-and-push \
	build-chaos-monkey build-chaos-monkey-image push-chaos-monkey-image \
	setup-strimzi setup-kafka setup-monitoring deploy-apps deploy-chaos-monkey deploy-chaos-monkey-dashboard \
	redeploy-apps redeploy-chaos-monkey \
	grafana-port-forward grafana-password kafka-ui-port-forward

# Install the application stack into an already bootstrapped Kind cluster.
# Cluster creation, node limits, the TLS registry, and ingress-nginx are owned by
# ../infra/kind/bootstrap-kind-cluster.sh.
all: setup-strimzi setup-kafka setup-monitoring deploy-apps deploy-chaos-monkey deploy-chaos-monkey-dashboard

bootstrap-cluster:
	../infra/kind/bootstrap-kind-cluster.sh

recreate-cluster:
	../infra/kind/bootstrap-kind-cluster.sh --recreate

# Backward-compatible alias. Prefer `make bootstrap-cluster` or
# `make recreate-cluster` so destructive cluster recreation is explicit.
cluster: bootstrap-cluster

# Node resource limits are applied by the bootstrap script. Keep this target as a
# compatibility shim for old docs/scripts that may still call it.
apply-limits:
	@echo "Node resource limits are handled by ../infra/kind/bootstrap-kind-cluster.sh"

build: build-app-images

build-app-images:
	$(DOCKER) build --platform $(IMAGE_PLATFORM) -t $(REGISTRY)/kafka-producer:$(IMAGE_TAG) src/producer
	$(DOCKER) build --platform $(IMAGE_PLATFORM) -t $(REGISTRY)/kafka-consumer:$(IMAGE_TAG) src/consumer

push: push-app-images push-chaos-monkey-image

push-app-images:
	$(DOCKER) push $(REGISTRY)/kafka-producer:$(IMAGE_TAG)
	$(DOCKER) push $(REGISTRY)/kafka-consumer:$(IMAGE_TAG)

build-and-push: build-app-images push-app-images build-chaos-monkey-image push-chaos-monkey-image

build-chaos-monkey: build-chaos-monkey-image

build-chaos-monkey-image:
	$(DOCKER) build --platform $(IMAGE_PLATFORM) -t $(REGISTRY)/chaos-monkey:$(IMAGE_TAG) src/chaos_monkey

push-chaos-monkey-image:
	$(DOCKER) push $(REGISTRY)/chaos-monkey:$(IMAGE_TAG)

setup-strimzi:
	$(HELM) repo add strimzi https://strimzi.io/charts/
	$(HELM) repo update
	$(HELM) upgrade --install strimzi-cluster-operator strimzi/strimzi-kafka-operator --namespace kafka --create-namespace

setup-kafka:
	$(KUBECTL) wait --for=condition=established --timeout=60s crd/kafkas.kafka.strimzi.io
	$(KUBECTL) apply -f k8s/kafka-cluster.yaml -n kafka
	$(KUBECTL) apply -f k8s/schema-registry.yaml -n kafka
	$(KUBECTL) apply -f k8s/kafka-ui.yaml -n kafka

setup-monitoring:
	$(HELM) repo add prometheus-community https://prometheus-community.github.io/helm-charts
	$(HELM) repo update
	$(HELM) upgrade --install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace \
		-f k8s/prometheus-values.yaml

deploy-apps:
	$(HELM) upgrade --install kafka-apps ./helm/kafka-apps --namespace apps --create-namespace \
		--set producer.image.repository=$(REGISTRY)/kafka-producer \
		--set producer.image.tag=$(IMAGE_TAG) \
		--set consumer.image.repository=$(REGISTRY)/kafka-consumer \
		--set consumer.image.tag=$(IMAGE_TAG)

deploy-chaos-monkey:
	$(HELM) upgrade --install chaos-monkey ./helm/chaos-monkey --namespace apps --create-namespace \
		--set image.repository=$(REGISTRY)/chaos-monkey \
		--set image.tag=$(IMAGE_TAG)

deploy-chaos-monkey-dashboard:
	$(KUBECTL) apply -f k8s/monitoring/chaos-monkey-dashboard.yaml

teardown:
	$(KIND) delete cluster --name $(CLUSTER_NAME)

redeploy-apps: build-app-images push-app-images deploy-apps
	$(KUBECTL) rollout restart deployment -n apps kafka-apps-producer kafka-apps-consumer

redeploy-chaos-monkey: build-chaos-monkey-image push-chaos-monkey-image deploy-chaos-monkey
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
