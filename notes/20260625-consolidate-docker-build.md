# Consolidating Docker/Image Builds (2026-06-25)

## Current Status

I investigated the build failure seen with `make build` in the Hermes container.

What I found:
- The repo’s top-level `make build` delegates to `src/Makefile`, which currently uses `docker build` for all images.
- In the Hermes container, `docker` exists but there is no reachable Docker daemon (`Cannot connect to the Docker daemon at unix:///var/run/docker.sock`).
- `docker buildx` is not available in the Hermes container (`docker: 'buildx' is not a docker command`).
- `buildctl` against `tcp://buildkitd:1234` does work in Hermes, and the MCP server image was verified that way.
- On the host-side environment, Docker is expected to work locally, so the same repo should keep supporting a normal Docker-based build path there.

Research result on `docker buildx`:
- `buildx` is a Docker CLI plugin, not a standalone replacement for BuildKit.
- It supports multiple drivers, including `docker`, `docker-container`, `kubernetes`, and `remote`.
- The `remote` driver can point buildx at a manually managed BuildKit daemon.
- That means buildx can help unify workflows, but only if the environment actually has the buildx plugin installed and the builder is configured appropriately.
- In the current Hermes container, buildx is not installed, so this is not a simple drop-in config flip.

Bottom line:
- The current build problem is not just a bad Make target; it is an environment/backend mismatch.
- A future fix should preserve the host Docker build path while adding a Hermes-friendly BuildKit path.
- Using `docker buildx` alone is not enough in the current Hermes container unless the plugin is added and the builder is configured to a compatible driver.

## Plan

Check off each TODO as you finish them

- [ ] Decide on the build abstraction boundary for the repo (Makefile variables, helper script, or both).
- [ ] Add a backend switch so one logical build target can select either Docker or BuildKit.
- [ ] Keep the host default working with local Docker.
- [ ] Add a Hermes default that uses `BUILDKIT_HOST=tcp://buildkitd:1234` and `buildctl`.
- [ ] If using buildx, verify the plugin is available in both environments and document the required builder setup.
- [ ] Prefer a simple CLI wrapper over duplicating image build logic across multiple Makefiles.
- [ ] Preserve existing image names, tags, and platform handling so deployments do not need to change.
- [ ] Verify the host build path with local Docker.
- [ ] Verify the Hermes build path with BuildKit.
- [ ] Confirm both paths produce the same pushed image format/tag behavior.
- [ ] Update the repo docs with the supported build modes and the exact environment variables to use.
- [ ] Re-run `make build` after the change in both environments and record the results.
- [ ] Wait for user to review code changes before completing the task

## Suggested Direction

The lowest-risk approach is to keep a single repository-level build entrypoint, but make the implementation conditional:
- Docker path for the host
- BuildKit path for Hermes

That gives the same `make` target to users, while avoiding a fragile dependency on `docker buildx` being installed everywhere.
