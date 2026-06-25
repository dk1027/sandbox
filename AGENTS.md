# Agent Instructions

You are running inside the `hermes` container. The repo is mounted at:

```text
/home/hermes/github.com/dk1027/sandbox
```

Use that directory as the working tree when editing code or running tests.

## Image Builds

Build images with `buildctl`, using:

```bash
BUILDKIT_HOST=tcp://buildkitd:1234
```

Before building, check BuildKit connectivity:

```bash
buildctl debug workers
```

To build and push an image to the Ryzen registry, run this from the directory
containing the Dockerfile:

```bash
buildctl build \
  --frontend dockerfile.v0 \
  --local context=. \
  --local dockerfile=. \
  --output type=image,name=ryzen.local:5001/<image-name>:<tag>,push=true
```

## Registry TLS

The registry is `ryzen.local:5001` and uses HTTPS with a local CA trusted by
the `buildkitd` container. If pushing fails with an `x509` unknown-authority
error, report the failure rather than working around TLS verification.
