# Refactory network routing plan - 2026-06-23

## TODOs

- [x] Rename `infra/kind/setup-kind-registry.sh` to `infra/kind/bootstrap-kind-cluster.sh`.
- [x] Make bootstrap create the Kind cluster and keep cluster-level setup in one script.
- [x] Make bootstrap apply Docker CPU/memory limits to Kind node containers.
- [x] Make bootstrap create and run the TLS local registry at `ryzen.local:5001`.
- [x] Keep generated certs, keys, registry data, and containerd trust files under top-level `generated/` with gitignore protection.
- [x] Make bootstrap install ingress-nginx into the Kind cluster.
- [x] Expose ingress on host port `443` only; do not expose port `80` or app-specific NodePorts from bootstrap.
- [x] Generate ingress TLS material for `*.ryzen.local` / `ryzen.local` and configure ingress-nginx to use it as the default HTTPS certificate.
- [x] Keep Grafana, Kafka UI, and other application Ingress resources out of bootstrap; they belong in their Helm chart or owning Kubernetes manifest.
- [x] Revert the earlier Grafana NodePort configuration in `kafka-k8s/k8s/prometheus-values.yaml`.
- [x] Document the network topology in `infra/docs/network-topology.md`, including image push, image pull, ingress routing, DNS, TLS trust, and exposed ports.
- [x] Update `infra/kind/README.md` to describe the new bootstrap responsibilities and app ingress ownership.
- [x] Run syntax/YAML checks for changed scripts and values files.
- [x] Launch a judge subagent to review the work and use its feedback before marking implementation complete.

## Judge review

Judge subagent completed review and returned PASS with no blocking fixes. The checklist above was checked off after that review.

## Notes

The cluster bootstrap should stay generic. It prepares shared infrastructure: Kind, registry, node limits, and ingress-nginx. App-specific ingress is intentionally not part of the bootstrap because hostnames, paths, service names, and app-specific external URL settings belong with the app deployment.
