IMAGE_NAME = sre-agent
include ../image-build.mk

.PHONY: build push buildpush build-command-center push-command-center

build-command-center:
	$(MAKE) -C command-center build

push-command-center:
	$(MAKE) -C command-center push
