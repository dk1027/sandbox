CLUSTER_NAME ?= kafka-cluster

.PHONY: all cluster apply-limits build setup-strimzi setup-kafka setup-monitoring deploy-apps teardown redeploy-apps build-chaos-monkey deploy-chaos-monkey redeploy-chaos-monkey deploy-chaos-monkey-dashboard

all: cluster apply-limits build setup-strimzi setup-kafka setup-monitoring deploy-apps

cluster:
	kind create cluster --config k8s/kind-config.yaml --name $(CLUSTER_NAME) || true

apply-limits:
	@echo "Applying CPU and Memory limits to kind nodes via docker update..."
	@for node in $$(kind get nodes --name $(CLUSTER_NAME)); do \
		docker update --cpus 1 -m 6g --memory-swap 12g $$node; \
	done

build:
	docker build -t kafka-producer:latest src/producer
	docker build -t kafka-consumer:latest src/consumer
	kind load docker-image kafka-producer:latest --name $(CLUSTER_NAME)
	kind load docker-image kafka-consumer:latest --name $(CLUSTER_NAME)

build-chaos-monkey:
	docker build -t chaos-monkey:latest src/chaos_monkey
	kind load docker-image chaos-monkey:latest --name $(CLUSTER_NAME)

setup-strimzi:
	helm repo add strimzi https://strimzi.io/charts/
	helm repo update
	helm upgrade --install strimzi-cluster-operator strimzi/strimzi-kafka-operator --namespace kafka --create-namespace

setup-kafka:
	kubectl wait --for=condition=established --timeout=60s crd/kafkas.kafka.strimzi.io
	kubectl apply -f k8s/kafka-cluster.yaml -n kafka
	kubectl apply -f k8s/schema-registry.yaml -n kafka
	kubectl apply -f k8s/kafka-ui.yaml -n kafka

setup-monitoring:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update
	helm upgrade --install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace \
		-f k8s/prometheus-values.yaml

deploy-apps:
	helm upgrade --install kafka-apps ./helm/kafka-apps --namespace apps --create-namespace

deploy-chaos-monkey:
	helm upgrade --install chaos-monkey ./helm/chaos-monkey --namespace apps --create-namespace

deploy-chaos-monkey-dashboard:
	kubectl apply -f k8s/monitoring/chaos-monkey-dashboard.yaml

teardown:
	kind delete cluster --name $(CLUSTER_NAME)

redeploy-apps: build deploy-apps
	kubectl rollout restart deployment -n apps kafka-apps-producer kafka-apps-consumer

redeploy-chaos-monkey: build-chaos-monkey deploy-chaos-monkey
	kubectl rollout restart deployment -n apps chaos-monkey-controller
	kubectl rollout restart daemonset -n apps chaos-monkey-daemon

grafana-port-forward:
	@echo "Forwarding Grafana to http://localhost:3000..."
	@export POD_NAME=$$(kubectl get pods --namespace monitoring -l "app.kubernetes.io/name=grafana" -o jsonpath="{.items[0].metadata.name}"); \
	kubectl --namespace monitoring port-forward $$POD_NAME 3000

grafana-password:
	@echo "Grafana Admin Password:"
	@kubectl get secret --namespace monitoring -l app.kubernetes.io/component=admin-secret -o jsonpath="{.items[0].data.admin-password}" | base64 --decode ; echo

kafka-ui-port-forward:
	@echo "Forwarding Kafka UI to http://localhost:8080..."
	@export POD_NAME=$$(kubectl get pods --namespace kafka -l "app=kafka-ui" -o jsonpath="{.items[0].metadata.name}"); \
	kubectl --namespace kafka port-forward $$POD_NAME 8080:8080
