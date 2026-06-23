# Chaos Monkey User Guide

This guide explains how to run your own chaos experiments with the eBPF-based Chaos Monkey in this repo.

## What the Chaos Monkey currently does

The Chaos Monkey supports node-local network chaos for selected pods.

Current practical experiment types:
- PacketLoss: drop a percentage of packets for the selected pods
- NetworkBlackhole: a 100% packet-loss experiment, useful to simulate a full network outage

Important note:
- `latencyMs` and `corruptPercentage` are part of the CRD schema, but the current eBPF implementation only applies packet-loss behavior.
- For now, keep `latencyMs: 0` and `corruptPercentage: 0` when you create your own experiment.

## Resource model

The experiment is declared in two layers:

- ChaosExperiment: user-facing intent
- NodeChaosTask: internal per-node task created by the controller

You normally create only the `ChaosExperiment`. The controller fans it out to matching nodes automatically.

## Available parameters

### metadata
- `metadata.name`: the experiment name
- `metadata.namespace`: the namespace where the experiment lives

### spec.targetSelectors
Controls which pods are targeted.

- `spec.targetSelectors.namespaces`
  - List of namespaces to search
  - If omitted, the experiment namespace is used
- `spec.targetSelectors.podSelector`
  - Standard Kubernetes label selector
  - Example: `matchLabels: { app: producer }`

### spec.action
Selects the type of chaos.

Supported practical values in this repo:
- `PacketLoss`
- `NetworkBlackhole`

### spec.duration
- A duration string such as `30s`, `5m`, `15m`
- The controller converts this into an absolute end time for the node tasks

### spec.parameters
Defines the network shaping settings.

- `latencyMs`
  - Currently keep this at `0`
- `dropPercentage`
  - Integer from `0` to `100`
  - `10` means drop roughly 10% of packets
  - `100` means a complete outage
- `corruptPercentage`
  - Currently keep this at `0`

## Example: disrupt the producer app

This example targets the producer app in the `apps` namespace and drops 10% of its packets for 5 minutes.

```yaml
apiVersion: chaos.dk1027.io/v1alpha1
kind: ChaosExperiment
metadata:
  name: producer-packet-loss
  namespace: apps
spec:
  targetSelectors:
    namespaces:
      - apps
    podSelector:
      matchLabels:
        app: producer
  action: PacketLoss
  duration: 5m
  parameters:
    latencyMs: 0
    dropPercentage: 10
    corruptPercentage: 0
```

Apply it with:

```bash
kubectl apply -f producer-packet-loss.yaml
```

## Example: blackhole a Kafka broker

This example targets the Strimzi Kafka broker pods and simulates a full network outage.

```yaml
apiVersion: chaos.dk1027.io/v1alpha1
kind: ChaosExperiment
metadata:
  name: broker-network-blackhole
  namespace: kafka
spec:
  targetSelectors:
    namespaces:
      - kafka
    podSelector:
      matchLabels:
        strimzi.io/name: my-cluster-kafka
  action: NetworkBlackhole
  duration: 5m
  parameters:
    latencyMs: 0
    dropPercentage: 100
    corruptPercentage: 0
```

Apply it with:

```bash
kubectl apply -f broker-network-blackhole.yaml
```

## Recommended workflow

1. Deploy the Chaos Monkey controller and daemon.
2. Make sure Prometheus and Grafana are running.
3. Apply your `ChaosExperiment` manifest.
4. Watch the controller create `NodeChaosTask` objects.
5. Observe the effect in Grafana:
   - producer publish failures should rise during chaos
   - consumer throughput should dip and then recover
   - Chaos Monkey metrics should show injected and recovered faults

## Demo entrypoint

If you want a ready-made example, run:

```bash
src/chaos-monkey/scripts/demo-chaos-monkey.sh
```

That script applies:
- the dashboard ConfigMap
- the example producer/broker experiments

and then waits for the node-local tasks to appear.
