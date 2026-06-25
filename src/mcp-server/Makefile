REGISTRY ?= ryzen.local:5001
IMAGE_TAG ?= dev
IMAGE_NAME = mcp-server
IMAGE = $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
DOCKER ?= docker

.PHONY: build push

build:
	$(DOCKER) build --platform linux/amd64 -t $(IMAGE) .

push:
	$(DOCKER) push $(IMAGE)
