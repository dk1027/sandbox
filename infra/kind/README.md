# Kind cluster with TLS local registry

This directory contains setup automation for the Linux test machine.

Target workflow:

- MacBook/dev machine builds images.
- MacBook pushes images to `ryzen.local:5001`.
- This Linux host runs a Docker registry with TLS.
- The Kind cluster on this Linux host pulls those same image names.
- Helm deploys charts into the Kind cluster using `image.repository=ryzen.local:5001/...`.

## Setup

From the repository root on the Linux test machine:

```bash
infra/kind/setup-kind-registry.sh --recreate
```

`--recreate` is required when a Kind cluster with the same name already exists. It deletes the existing Kind cluster and all cluster state before creating the new one.

Defaults:

- Cluster name: `kafka-cluster`
- Registry host: `ryzen.local`
- Registry port: `5001`
- Registry container: `kind-registry`
- Grafana host URL: `http://ryzen.local:3000`
- Grafana Kind NodePort: `30000`
- Worker nodes: `4`
- Generated artifacts: `generated/kind-registry/`
- CA/cert output: `generated/kind-registry/certs/`
- Registry image data: `generated/kind-registry/registry-data/`
- Kind node containerd registry config: `generated/kind-registry/containerd-certs/`

Environment overrides:

```bash
CLUSTER_NAME=kafka-cluster \
REGISTRY_HOST=ryzen.local \
REGISTRY_PORT=5001 \
GRAFANA_HOST_PORT=3000 \
GRAFANA_NODE_PORT=30000 \
NODE_COUNT=4 \
infra/kind/setup-kind-registry.sh --recreate
```

## Generated files and git safety

All generated certificates, private keys, registry data, and Kind containerd trust config are written under the repository-level `generated/` directory.

`generated/.gitignore` ignores everything under that directory except the `.gitignore` file itself. This keeps sensitive local CA/private-key material out of normal source control operations.

Do not commit files from `generated/kind-registry/`.

## Persistence model

The registry container does not receive a one-time copy of certificates. It mounts host paths instead:

- `generated/kind-registry/certs/` -> `/certs:ro`
- `generated/kind-registry/registry-data/` -> `/var/lib/registry`

The Kind node containers also do not receive a one-time `docker cp` of trust files. The generated containerd certs directory is mounted into every Kind node with Kind `extraMounts`:

- `generated/kind-registry/containerd-certs/` -> `/etc/containerd/certs.d:ro`

That means the registry TLS material, registry image data, and node containerd registry trust config survive container restarts. Recreating the Kind cluster still recreates node containers, but the same host-mounted generated files are reused.

## Grafana access from the MacBook

The setup script maps the Kind control-plane container port `30000` to this host's port `3000` on `0.0.0.0`. The Prometheus Helm values configure Grafana as a NodePort service on `30000`.

After running `make setup-monitoring`, Grafana should be reachable from the MacBook at:

```text
http://ryzen.local:3000
```

This avoids a long-running `kubectl port-forward` process for Grafana. The existing `make grafana-password` target still prints the admin password.

## TLS trust on the MacBook

The script generates a local CA at:

```text
generated/kind-registry/certs/ca.crt
```

Copy that CA certificate to the MacBook and trust it for Docker/Desktop. A typical Docker Desktop client-side trust path is:

```bash
mkdir -p ~/.docker/certs.d/ryzen.local:5001
cp ca.crt ~/.docker/certs.d/ryzen.local:5001/ca.crt
```

You may also need to add the CA to macOS Keychain or restart Docker Desktop, depending on the Docker Desktop version and settings. After trust is configured, the MacBook should be able to push to:

```text
ryzen.local:5001
```

Example MacBook image build/push:

```bash
docker build --platform linux/amd64 \
  -t ryzen.local:5001/chaos-monkey:dev \
  kafka-k8s/src/chaos_monkey

docker push ryzen.local:5001/chaos-monkey:dev
```

Use `--platform linux/amd64` from Apple Silicon Macs because the Kind nodes on the Linux test host are amd64.

## Helm deploy example

Run Helm on this Linux test machine:

```bash
cd kafka-k8s

helm upgrade --install chaos-monkey ./helm/chaos-monkey \
  --namespace apps \
  --create-namespace \
  --set image.repository=ryzen.local:5001/chaos-monkey \
  --set image.tag=dev \
  --set image.pullPolicy=Always

kubectl rollout status deployment/chaos-monkey-controller -n apps
kubectl rollout status daemonset/chaos-monkey-daemon -n apps
```

For regular development, prefer unique tags such as the git SHA instead of `dev`/`latest`.

## What the setup script does

1. Generates a local CA and registry server certificate under `generated/kind-registry/certs/`.
2. Writes containerd registry trust files under `generated/kind-registry/containerd-certs/`.
3. Starts a TLS-enabled `registry:3` container published as `0.0.0.0:5001` with host-mounted certs and registry data.
4. Creates a Kind cluster with containerd registry config-dir support and host-mounted registry trust config.
5. Maps `ryzen.local:3000` on the host to Grafana's Kind NodePort `30000`.
6. Connects the registry container to the Docker `kind` network.
7. Publishes the standard `local-registry-hosting` ConfigMap in `kube-public`.

## Verification

After trusting the CA locally on the Linux test host if needed, push a small test image:

```bash
docker pull busybox:latest
docker tag busybox:latest ryzen.local:5001/busybox:test
docker push ryzen.local:5001/busybox:test

kubectl run registry-test \
  --image=ryzen.local:5001/busybox:test \
  --restart=Never \
  --command -- sleep 30

kubectl wait --for=condition=Ready pod/registry-test --timeout=60s
kubectl delete pod registry-test
```

If `docker push` fails with a certificate error, the local Docker daemon does not trust `generated/kind-registry/certs/ca.crt` yet.
