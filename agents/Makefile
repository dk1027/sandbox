# hermes:
# 	docker build -f Dockerfile.hermes -t hermes \
# 		--build-arg UID=$$(id -u) \
# 		--build-arg GID=$$(id -g) \
# 		.
# 	docker run -d \
# 		--name hermes \
# 		--add-host=host.docker.internal:host-gateway \
# 		-v "$$HOME/.hermes:/home/hermes/.hermes" \
# 		-v "$$HOME/github.com:/home/hermes/github.com" \
# 		hermes

hermes:
	docker run -it --rm \
		-v ~/.hermes:/opt/data \
		nousresearch/hermes-agent bash