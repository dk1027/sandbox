REGISTRY ?= ryzen.local:5001
IMAGE_TAG ?= dev
IMAGE_PLATFORM ?= linux/amd64
DOCKER ?= docker

IMAGE_NAME = kafka-producer

.PHONY: build push buildpush

build:
	$(DOCKER) build --platform $(IMAGE_PLATFORM) -t $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) .

push:
	$(DOCKER) push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

buildpush: build push
