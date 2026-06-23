# Deployment Artifacts

This directory contains Kubernetes deployment artifacts for the local Kafka test stack.

## Layout

- `charts/`: Local Helm charts owned by this repository.
  - `kafka-apps/`: Producer and consumer application deployments, services, and ServiceMonitors.
  - `chaos-monkey/`: Chaos Monkey CRDs, controller deployment, daemonset, and RBAC.
- `manifests/`: Raw Kubernetes YAML for resources that do not currently need a local Helm chart.
  - `kafka/`: Strimzi Kafka CRs, Schema Registry, and Kafka UI.
  - `chaos/`: Demo and example ChaosExperiment manifests.
  - `monitoring/`: Dashboard ConfigMaps and related monitoring manifests.
- `values/`: Values files for third-party Helm charts.
  - `prometheus-values.yaml`: Values for `prometheus-community/kube-prometheus-stack`.

## Convention

Use Helm charts when a repo-owned app has a release lifecycle, image settings, or environment-specific values. Use raw manifests for simple static resources, demos/examples, CR instances, and third-party chart values. Do not move a resource into Helm just because it is Kubernetes YAML.
