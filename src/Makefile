.PHONY: build push buildpush build-apps push-apps build-chaos-monkey push-chaos-monkey

build: build-apps build-chaos-monkey

build-apps:
	$(MAKE) -C producer build
	$(MAKE) -C consumer build

push: push-apps push-chaos-monkey

push-apps:
	$(MAKE) -C producer push
	$(MAKE) -C consumer push

build-chaos-monkey:
	$(MAKE) -C chaos-monkey build

push-chaos-monkey:
	$(MAKE) -C chaos-monkey push

buildpush: build push
