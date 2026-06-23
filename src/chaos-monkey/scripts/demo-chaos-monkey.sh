#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

info() {
  printf '\n[demo] %s\n' "$*"
}

wait_for_prefix() {
  local namespace="$1"
  local prefix="$2"
  local timeout_seconds="${3:-120}"
  local start_ts
  start_ts=$(date +%s)
  while true; do
    if kubectl get nodechaostasks.chaos.dk1027.io -n "$namespace" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -q "^${prefix}"; then
      return 0
    fi
    if (( $(date +%s) - start_ts > timeout_seconds )); then
      echo "Timed out waiting for NodeChaosTask prefix $namespace/$prefix" >&2
      return 1
    fi
    sleep 3
  done
}

info "Applying Grafana dashboard"
kubectl apply -f "$REPO_ROOT/deploy/manifests/monitoring/chaos-monkey-dashboard.yaml"

info "Applying producer + broker chaos experiments"
kubectl apply -f "$REPO_ROOT/deploy/manifests/chaos/demo-chaos-experiments.yaml"

info "Waiting for the controller to create node-local tasks"
wait_for_prefix apps producer-packet-loss-
wait_for_prefix kafka broker-network-blackhole-

info "Current node-local chaos tasks"
kubectl get nodechaostasks.chaos.dk1027.io -A -o wide

info "Current chaos monkey metrics pods"
kubectl get pods -n apps -l app=chaos-monkey-daemon -o wide

info "Experiment is running for 5 minutes. Open Grafana and watch the dashboard: Chaos Monkey Network Experiment"
info "Prometheus/Grafana are already installed by the repo's monitoring stack."
info "The dashboard panel should show injected vs recovered faults, producer success/failure, and consumer throughput recovery."
