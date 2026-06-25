# Agent Instructions

You are running inside the `hermes` container. The repo is mounted at:

```text
/opt/data/src/github.com/dk1027/sandbox
```

Use that directory as the working tree when editing code or running tests.

## Image build and push

The `hermes` container has no access to the host's docker daemon. 

To build images, use the builtkid backend: `tcp://buildkitd:1234` and push images to the Ryzen registry: `ryzen.local:5001`

`kubectl` and `helm` are available to deploy workloads to `dev-cluster`, which is a Kind cluster running on `ryzen.local`

When working on build system / Makefiles, make sure you do not break the developer unsandboxed workflow that uses the local docker daemon.

## Registry TLS

The registry is `ryzen.local:5001` and uses HTTPS with a local CA trusted by
the `buildkitd` container. If pushing fails with an `x509` unknown-authority
error, report the failure rather than working around TLS verification.
