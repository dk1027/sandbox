REGISTRY ?= ryzen.local:5001
IMAGE_TAG ?= dev
IMAGE_PLATFORM ?= linux/amd64
# BUILD_BACKEND is intentionally a single switch shared by every image Makefile.
# When it is left on 'auto', we infer the right backend from BUILDKIT_HOST.
# Hermes sets BUILDKIT_HOST for the sandboxed/containerized path so buildx can
# reach the remote BuildKit daemon, while host runs typically leave it unset and
# fall back to the local Docker daemon.
BUILD_BACKEND ?= auto
DOCKER ?= docker
DOCKERFILE ?= Dockerfile
IMAGE_CONTEXT ?= .

IMAGE_REF := $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
BUILD_BACKEND_RESOLVED := $(if $(filter auto,$(BUILD_BACKEND)),$(if $(BUILDKIT_HOST),buildkit,docker),$(BUILD_BACKEND))
# Keep the shared build flags in one place so the backend branches only differ
# in how they export the image: local daemon uses --load + docker push, while
# BuildKit publishes directly with --push or writes an OCI tarball for build.
BUILDX_COMMON_FLAGS := --platform "$(IMAGE_PLATFORM)" -t "$(IMAGE_REF)" -f "$(DOCKERFILE)" "$(IMAGE_CONTEXT)"


.PHONY: build push buildpush

build:
	@set -eu; \
	case "$(BUILD_BACKEND_RESOLVED)" in \
	  docker) \
	    $(DOCKER) buildx build --load $(BUILDX_COMMON_FLAGS); \
	    ;; \
	  buildkit) \
	    tmpdir="$$(mktemp -d)"; \
	    trap 'rm -rf "$$tmpdir"' EXIT; \
	    $(DOCKER) buildx build $(BUILDX_COMMON_FLAGS) --output "type=oci,dest=$$tmpdir/$(IMAGE_NAME).oci.tar"; \
	    ;; \
	  *) \
	    echo "invalid BUILD_BACKEND: $(BUILD_BACKEND)" >&2; \
	    exit 2; \
	    ;; \
	esac

push:
	@set -eu; \
	case "$(BUILD_BACKEND_RESOLVED)" in \
	  docker) \
	    $(DOCKER) push "$(IMAGE_REF)"; \
	    ;; \
	  buildkit) \
	    $(DOCKER) buildx build $(BUILDX_COMMON_FLAGS) --push; \
	    ;; \
	  *) \
	    echo "invalid BUILD_BACKEND: $(BUILD_BACKEND)" >&2; \
	    exit 2; \
	    ;; \
	esac

buildpush:
	@set -eu; \
	case "$(BUILD_BACKEND_RESOLVED)" in \
	  docker) \
	    $(DOCKER) buildx build --load $(BUILDX_COMMON_FLAGS) && $(DOCKER) push "$(IMAGE_REF)"; \
	    ;; \
	  buildkit) \
	    $(DOCKER) buildx build $(BUILDX_COMMON_FLAGS) --push; \
	    ;; \
	  *) \
	    echo "invalid BUILD_BACKEND: $(BUILD_BACKEND)" >&2; \
	    exit 2; \
	    ;; \
	esac
