agent-up:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) docker compose up --build -d

hermes: agent-up
	docker compose exec hermes bash

agent-shell:
	docker compose exec hermes bash

buildkitd:
	docker compose up -d buildkitd

agent-down:
	docker compose down
