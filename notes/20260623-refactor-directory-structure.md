# 2026-06-23 Directory Structure Refactor TODO

Goal: flatten the old `kafka-k8s/` project directory into clearer top-level areas while keeping `infra/` as the shared Kind cluster substrate, moving application code to top-level `src/`, and grouping Kubernetes deployment artifacts under top-level `deploy/`.

Decisions:

- Keep `infra/` for cluster/host infrastructure.
- Move application source code to top-level `src/`.
- Use top-level `deploy/` for Helm charts, raw manifests, and third-party Helm values.
- Do not create top-level `scripts/`, `docs/`, or `designs/` directories.
- Merge chaos-monkey docs and designs under `src/chaos-monkey/docs/`.
- Move chaos-monkey scripts under `src/chaos-monkey/scripts/`.
- Keep existing `notes/` content untouched, except this TODO file.
- Preserve raw Kubernetes manifests for now; do not force everything into Helm.
- Preserve legacy Kind config under `infra/kind/examples/` rather than deleting it.

TODO:

- [x] Move `kafka-k8s/Makefile` to top-level `Makefile`.
- [x] Move producer source from `kafka-k8s/src/producer/` to `src/producer/`.
- [x] Move consumer source from `kafka-k8s/src/consumer/` to `src/consumer/`.
- [x] Move Chaos Monkey source from `kafka-k8s/src/chaos_monkey/` to `src/chaos-monkey/`.
- [x] Move Chaos Monkey script from `kafka-k8s/scripts/demo-chaos-monkey.sh` to `src/chaos-monkey/scripts/demo-chaos-monkey.sh`.
- [x] Move Chaos Monkey docs/designs from `kafka-k8s/docs/` and `kafka-k8s/designs/` to `src/chaos-monkey/docs/`.
- [x] Move local Helm charts from `kafka-k8s/helm/` to `deploy/charts/`.
- [x] Move raw Kafka manifests from `kafka-k8s/k8s/` to `deploy/manifests/kafka/`.
- [x] Move chaos experiment manifests to `deploy/manifests/chaos/`.
- [x] Move monitoring dashboard manifest to `deploy/manifests/monitoring/`.
- [x] Move Prometheus third-party chart values to `deploy/values/prometheus-values.yaml`.
- [x] Move legacy Kind config to `infra/kind/examples/kind-config.yaml`.
- [x] Move legacy Kafka/Kubernetes setup notes to `infra/docs/20260619-kafka-infra-setup-notes.md`.
- [x] Move legacy Chaos Monkey implementation notes to `src/chaos-monkey/docs/20260621-chaos-monkey-notes.md`.
- [x] Replace `kafka-k8s/README.md` with a top-level `README.md` updated for the new layout.
- [x] Add `deploy/README.md` explaining charts vs manifests vs values.
- [x] Update `Makefile` paths for source builds, Helm charts, manifests, values, and bootstrap.
- [x] Update `src/chaos-monkey/scripts/demo-chaos-monkey.sh` paths for its new location.
- [x] Update docs references in `README.md`, `infra/kind/README.md`, and Chaos Monkey docs.
- [x] Remove the empty `kafka-k8s/` directory after tracked files have moved.
- [x] Run `git status --short` to review moved/changed files.
- [x] Run `go test ./...` in `src/chaos-monkey`.
- [x] Run Helm template checks for `deploy/charts/kafka-apps` and `deploy/charts/chaos-monkey`.
- [x] Run YAML/client validation checks for moved manifests where possible.
- [x] Run Makefile dry-run checks for representative targets.
- [x] Mark all completed items in this TODO file.
