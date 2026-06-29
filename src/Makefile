.PHONY: build push buildpush build-apps push-apps build-chaos-monkey push-chaos-monkey build-mcp-server push-mcp-server build-sre-agent push-sre-agent build-command-center push-command-center

build: build-apps build-chaos-monkey build-mcp-server build-sre-agent build-command-center

build-apps:
	$(MAKE) -C producer build
	$(MAKE) -C consumer build

build-chaos-monkey:
	$(MAKE) -C chaos-monkey build

build-mcp-server:
	$(MAKE) -C mcp-server build

build-sre-agent:
	$(MAKE) -C sre-agent build

build-command-center:
	$(MAKE) -C sre-agent/command-center build

push: push-apps push-chaos-monkey push-mcp-server push-sre-agent push-command-center

push-apps:
	$(MAKE) -C producer push
	$(MAKE) -C consumer push

push-chaos-monkey:
	$(MAKE) -C chaos-monkey push

push-mcp-server:
	$(MAKE) -C mcp-server push

push-sre-agent:
	$(MAKE) -C sre-agent push

push-command-center:
	$(MAKE) -C sre-agent/command-center push

buildpush: build push
