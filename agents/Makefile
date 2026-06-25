hermes:
	docker build -f Dockerfile.hermes -t hermes \
		--build-arg UID=$$(id -u) \
		--build-arg GID=$$(id -g) \
		.
	docker run -it --rm \
		--name hermes \
		--network host \
		-e KUBECONFIG=/home/hermes/.kube/config \
		--mount type=bind,source="$$HOME/.kube",target=/home/hermes/.kube \
		--mount type=bind,source="$$HOME/.hermes",target=/opt/data \
		--mount type=bind,source="$$HOME/github.com/dk1027/sandbox",target=/home/hermes/github.com/dk1027/sandbox \
		-w /home/hermes/github.com/dk1027/sandbox \
		hermes

# https://github.com/moby/buildkit/blob/master/docs/rootless.md#containerized-deployment
buildkitd:
	docker run \
	--name buildkitd \
	-d \
	--security-opt seccomp=unconfined \
	--security-opt apparmor=unconfined \
	--security-opt systempaths=unconfined \
	moby/buildkit:rootless
