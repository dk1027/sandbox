NETWORK_MODE ?= bridge

hermes:
	docker build -f Dockerfile.hermes -t hermes \
		--build-arg UID=$$(id -u) \
		--build-arg GID=$$(id -g) \
		.
ifeq ($(NETWORK_MODE),host)
	docker run -it --rm \
		--name hermes \
		--network host \
		-e KUBECONFIG=/home/hermes/.kube/config \
		-v "$$HOME/.kube:/home/hermes/.kube" \
		-v "$$HOME/.hermes:/opt/data" \
		-v "$$HOME/github.com/dk1027/sandbox:/home/hermes/github.com/dk1027/sandbox" \
		-w /home/hermes/github.com/dk1027/sandbox \
		hermes
else
	docker run -it --rm \
		--name hermes \
		--add-host=host.docker.internal:host-gateway \
		-e KUBECONFIG=/home/hermes/.kube/config \
		-v "$$HOME/.kube:/home/hermes/.kube" \
		-v "$$HOME/.hermes:/opt/data" \
		-v "$$HOME/github.com/dk1027/sandbox:/home/hermes/github.com/dk1027/sandbox" \
		-w /home/hermes/github.com/dk1027/sandbox \
		hermes
endif
