# Kind cluster bootstrap

This directory contains the bootstrap script for the Linux test machine.

Target workflow:

- MacBook/dev machine builds images.
- MacBook pushes images to the TLS registry at `ryzen.local:5001`.
- This Linux host runs a Docker registry with TLS and persistent host-mounted storage.
- The Kind cluster on this Linux host pulls those same image names.
- ingress-nginx runs inside Kind and exposes application HTTP traffic on HTTPS port `443` only.
- Application-specific Ingress objects live with the app Helm chart or Kubernetes manifest that owns the service.

## Setup

From the repository root on the Linux test machine:

```bash
infra/kind/bootstrap-kind-cluster.sh --recreate
```

`--recreate` is required when a Kind cluster with the same name already exists. It deletes the existing Kind cluster and all cluster state before creating the new one.

Defaults:

- Cluster name: `dev-cluster`
- Registry host: `ryzen.local`
- Registry port: `5001`
- Registry container: `kind-registry`
- Kubernetes API endpoint: `https://ryzen.local:6443`
- Kubernetes API host bind: `0.0.0.0:6443`
- Ingress domain: `ryzen.local`
- Ingress host port: `443`
- Worker nodes: `4`
- Node resource limits: `1` CPU, `6g` memory, `12g` memory+swap per Kind node container
- Generated artifacts: `generated/kind-cluster/`
- Registry CA/cert output: `generated/kind-cluster/registry/certs/`
- Ingress CA/cert output: `generated/kind-cluster/ingress/certs/`
- Registry image data: `generated/kind-cluster/registry/data/`
- Kind node containerd registry config: `generated/kind-cluster/containerd-certs/`

Environment overrides:

```bash
CLUSTER_NAME=dev-cluster \
REGISTRY_HOST=ryzen.local \
REGISTRY_PORT=5001 \
API_SERVER_ADDRESS=0.0.0.0 \
API_SERVER_PORT=6443 \
API_SERVER_CERT_SANS=ryzen.local,192.168.1.73 \
INGRESS_DOMAIN=ryzen.local \
INGRESS_HTTPS_PORT=443 \
NODE_COUNT=4 \
NODE_CPUS=1 \
NODE_MEMORY=6g \
NODE_MEMORY_SWAP=12g \
infra/kind/bootstrap-kind-cluster.sh --recreate
```

## Generated files and git safety

All generated certificates, private keys, registry data, and Kind containerd trust config are written under the repository-level `generated/` directory.

`generated/.gitignore` ignores everything under that directory except the `.gitignore` file itself. The repository `.gitignore` also ignores `/generated/*` and the old `/infra/kind/certs/` location.

Do not commit files from `generated/kind-cluster/`.

## What the bootstrap script does

1. Generates a local CA and TLS certificate for the registry.
2. Writes containerd registry trust files for Kind nodes.
3. Generates a separate local CA and wildcard certificate for ingress hosts such as `grafana.ryzen.local`.
4. Starts a TLS-enabled `registry:3` container published as `0.0.0.0:5001` with host-mounted certs and registry data.
5. Creates a Kind cluster with:
   - mounted containerd registry trust config
   - Kubernetes API exposed as `0.0.0.0:6443` on the Linux host
   - API server certificate SANs for `ryzen.local` and `192.168.1.73`
   - an `ingress-ready=true` label on the control-plane node for ingress-nginx scheduling
   - HTTPS-only ingress host port mapping: host `443` -> Kind control-plane `443`
6. Applies Docker resource limits to each Kind node container.
7. Connects the registry container to the Docker `kind` network.
8. Publishes the standard `local-registry-hosting` ConfigMap in `kube-public`.
9. Installs ingress-nginx using the Kind provider manifest.
10. Configures ingress-nginx with the generated wildcard TLS certificate as its default HTTPS certificate.

## Persistence model

The registry container mounts host paths instead of receiving one-time copied files:

- `generated/kind-cluster/registry/certs/` -> `/certs:ro`
- `generated/kind-cluster/registry/data/` -> `/var/lib/registry`

The Kind node containers also mount the generated containerd certs directory:

- `generated/kind-cluster/containerd-certs/` -> `/etc/containerd/certs.d:ro`

That means the registry TLS material, registry image data, and node containerd registry trust config survive container restarts. Recreating the Kind cluster still recreates node containers, but the same host-mounted generated files are reused.

## TLS trust on the MacBook

Registry pushes from Docker need the registry CA:

```text
generated/kind-cluster/registry/certs/ca.crt
```

A typical Docker Desktop client-side trust path is:

```bash
mkdir -p ~/.docker/certs.d/ryzen.local:5001
cp ca.crt ~/.docker/certs.d/ryzen.local:5001/ca.crt
```

Browser access to ingress hosts needs the ingress CA trusted in macOS/browser trust settings:

```text
generated/kind-cluster/ingress/certs/ca.crt
```

## Application ingress ownership

This bootstrap creates the ingress controller and wildcard/default TLS certificate. It does not create Grafana, Kafka UI, Prometheus, or app-specific Ingress resources.

Those resources should live with the owner of each app:

- Grafana: kube-prometheus-stack values in `deploy/values/prometheus-values.yaml`
- Kafka UI: the Kafka UI manifest under `deploy/manifests/kafka/`
- Custom apps: their Helm chart templates under `deploy/charts/`

Each app should define its own host, for example:

- `https://grafana.ryzen.local`
- `https://kafka-ui.ryzen.local`

Because the bootstrap exposes only port `443`, app Ingress resources should be configured for HTTPS hosts. They can use the ingress-nginx default wildcard certificate installed by bootstrap, or define their own namespace-local TLS secret if an app needs a distinct certificate.

## Verification

After bootstrap:

```bash
kubectl get pods -n ingress-nginx
kubectl get svc -n ingress-nginx
kubectl get configmap local-registry-hosting -n kube-public -o yaml
```

To verify the registry after trusting the registry CA locally:

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

## MacBook kubectl access

After recreating the cluster, copy the kubeconfig to the MacBook and rewrite the
server URL to the LAN endpoint:

```bash
mkdir -p ~/.kube/kind-ryzen
ssh ltse@ryzen.local 'kubectl config view --minify --raw' > ~/.kube/kind-ryzen/dev-cluster.yaml
kubectl --kubeconfig ~/.kube/kind-ryzen/dev-cluster.yaml \
  config set-cluster kind-dev-cluster --server=https://ryzen.local:6443
KUBECONFIG=~/.kube/kind-ryzen/dev-cluster.yaml kubectl get nodes
```
