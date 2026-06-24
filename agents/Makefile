hermes:
	docker build -f Dockerfile.hermes -t hermes \
		--build-arg UID=$$(id -u) \
		--build-arg GID=$$(id -g) \
		.
	docker run -it --rm \
		--name hermes \
		--add-host=host.docker.internal:host-gateway \
		-v "$$HOME/.hermes:/opt/data" \
		-v "$$HOME/github.com/dk1027/sandbox:/home/hermes/github.com/dk1027/sandbox" \
		-w /home/hermes/github.com/dk1027/sandbox \
		hermes
