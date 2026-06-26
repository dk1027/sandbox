# Consolidating Docker/Image Builds (2026-06-25)

## Goal

Use Docker buildx with separate backends for the `hermes` sandboxed container (remote BuildKit) and for local unsandboxed development (local Docker daemon), while keeping the Makefile targets clean and maintainable.

Support two ways of building docker images:
- When building from a sandboxed container, use remote buildkit. 
- When building from the host with full privileges, use the docker daemon
- Push to a remote repository in either case 
- Don't keep a parallel set of make targets. I would rather my Makefile to use the same dockerfile and same docker commands to build images, rather than littering a lot of conditional statements, or having a lot of parallel make target for remote and local docker builds.

## Subgoals
- Verify by actually building and pushing an image and observe that pods are updated
- Make sure the build with docker daemon path also works. Pause and provide a command for the user to test on the host
- Wait for user to review changes before completing the task

## Additional info
- Only work on the `sandbox` repo
- See `AGENTS.md` for more info



