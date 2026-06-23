#!/usr/bin/env bash
set -euo pipefail

# Bootstrap the Linux test host's Kind cluster.
#
# This creates the cluster-level infrastructure only:
#   - TLS local registry for MacBook image pushes and Kind image pulls
#   - Kind cluster with mounted containerd registry trust
#   - Docker resource limits on Kind node containers
#   - ingress-nginx reachable from the LAN on HTTPS port 443 only
#
# Application-specific ingress objects belong with the application chart or
# manifest that owns the service, not in this bootstrap script.

CLUSTER_NAME="${CLUSTER_NAME:-dev-cluster}"
REGISTRY_NAME="${REGISTRY_NAME:-kind-registry}"
REGISTRY_HOST="${REGISTRY_HOST:-ryzen.local}"
REGISTRY_PORT="${REGISTRY_PORT:-5001}"
INGRESS_HTTPS_PORT="${INGRESS_HTTPS_PORT:-443}"
INGRESS_DOMAIN="${INGRESS_DOMAIN:-ryzen.local}"
NODE_COUNT="${NODE_COUNT:-4}"
NODE_CPUS="${NODE_CPUS:-1}"
NODE_MEMORY="${NODE_MEMORY:-6g}"
NODE_MEMORY_SWAP="${NODE_MEMORY_SWAP:-12g}"
INGRESS_NGINX_MANIFEST="${INGRESS_NGINX_MANIFEST:-https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GENERATED_DIR="${GENERATED_DIR:-${REPO_ROOT}/generated/kind-cluster}"
REGISTRY_CERT_DIR="${REGISTRY_CERT_DIR:-${GENERATED_DIR}/registry/certs}"
REGISTRY_DATA_DIR="${REGISTRY_DATA_DIR:-${GENERATED_DIR}/registry/data}"
CONTAINERD_CERTS_DIR="${CONTAINERD_CERTS_DIR:-${GENERATED_DIR}/containerd-certs}"
INGRESS_CERT_DIR="${INGRESS_CERT_DIR:-${GENERATED_DIR}/ingress/certs}"
RECREATE=false

usage() {
  cat <<EOF
Usage: $(basename "$0") [--recreate]

Bootstraps the Kind test cluster and shared cluster infrastructure.

Environment overrides:
  CLUSTER_NAME             default: dev-cluster
  REGISTRY_NAME            default: kind-registry
  REGISTRY_HOST            default: ryzen.local
  REGISTRY_PORT            default: 5001
  INGRESS_DOMAIN           default: ryzen.local
  INGRESS_HTTPS_PORT       default: 443
  NODE_COUNT               default: 4 worker nodes
  NODE_CPUS                default: 1
  NODE_MEMORY              default: 6g
  NODE_MEMORY_SWAP         default: 12g
  GENERATED_DIR            default: generated/kind-cluster
  REGISTRY_CERT_DIR        default: generated/kind-cluster/registry/certs
  REGISTRY_DATA_DIR        default: generated/kind-cluster/registry/data
  CONTAINERD_CERTS_DIR     default: generated/kind-cluster/containerd-certs
  INGRESS_CERT_DIR         default: generated/kind-cluster/ingress/certs
  INGRESS_NGINX_MANIFEST   default: ingress-nginx Kind provider manifest URL

Options:
  --recreate      Delete any existing Kind cluster named CLUSTER_NAME first.
  -h, --help      Show this help.

Generated certs and registry data are local artifacts under GENERATED_DIR.
Do not commit them.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --recreate)
      RECREATE=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_cmd docker
require_cmd kind
require_cmd kubectl
require_cmd openssl
require_cmd seq

if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
  if [[ "${RECREATE}" == true ]]; then
    echo "Deleting existing Kind cluster ${CLUSTER_NAME}."
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    echo "Kind cluster ${CLUSTER_NAME} already exists. Re-run with --recreate to replace it." >&2
    exit 1
  fi
fi

mkdir -p \
  "${REGISTRY_CERT_DIR}" \
  "${REGISTRY_DATA_DIR}" \
  "${CONTAINERD_CERTS_DIR}" \
  "${INGRESS_CERT_DIR}"

REGISTRY_CA_KEY="${REGISTRY_CERT_DIR}/ca.key"
REGISTRY_CA_CERT="${REGISTRY_CERT_DIR}/ca.crt"
REGISTRY_SERVER_KEY="${REGISTRY_CERT_DIR}/registry.key"
REGISTRY_SERVER_CERT="${REGISTRY_CERT_DIR}/registry.crt"
REGISTRY_SERVER_CSR="${REGISTRY_CERT_DIR}/registry.csr"
REGISTRY_SERVER_EXT="${REGISTRY_CERT_DIR}/registry.ext"

if [[ ! -f "${REGISTRY_CA_KEY}" || ! -f "${REGISTRY_CA_CERT}" ]]; then
  echo "Generating local registry CA in ${REGISTRY_CERT_DIR}."
  openssl genrsa -out "${REGISTRY_CA_KEY}" 4096
  openssl req -x509 -new -nodes -key "${REGISTRY_CA_KEY}" -sha256 -days 3650 \
    -subj "/CN=${REGISTRY_HOST} local registry CA" \
    -out "${REGISTRY_CA_CERT}"
fi

cat >"${REGISTRY_SERVER_EXT}" <<EOF
subjectAltName = DNS:${REGISTRY_HOST},DNS:${REGISTRY_NAME},DNS:localhost,IP:127.0.0.1
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF

echo "Generating registry server certificate for ${REGISTRY_HOST} and ${REGISTRY_NAME}."
openssl genrsa -out "${REGISTRY_SERVER_KEY}" 4096
openssl req -new -key "${REGISTRY_SERVER_KEY}" \
  -subj "/CN=${REGISTRY_HOST}" \
  -out "${REGISTRY_SERVER_CSR}"
openssl x509 -req -in "${REGISTRY_SERVER_CSR}" \
  -CA "${REGISTRY_CA_CERT}" -CAkey "${REGISTRY_CA_KEY}" -CAcreateserial \
  -out "${REGISTRY_SERVER_CERT}" -days 825 -sha256 -extfile "${REGISTRY_SERVER_EXT}"
rm -f "${REGISTRY_SERVER_CSR}"

CONTAINERD_REGISTRY_DIR="${CONTAINERD_CERTS_DIR}/${REGISTRY_HOST}:${REGISTRY_PORT}"
mkdir -p "${CONTAINERD_REGISTRY_DIR}"
cp "${REGISTRY_CA_CERT}" "${CONTAINERD_REGISTRY_DIR}/ca.crt"
cat >"${CONTAINERD_REGISTRY_DIR}/hosts.toml" <<EOF
server = "https://${REGISTRY_HOST}:${REGISTRY_PORT}"

[host."https://${REGISTRY_NAME}:5000"]
  capabilities = ["pull", "resolve"]
  ca = "/etc/containerd/certs.d/${REGISTRY_HOST}:${REGISTRY_PORT}/ca.crt"
EOF

INGRESS_CA_KEY="${INGRESS_CERT_DIR}/ca.key"
INGRESS_CA_CERT="${INGRESS_CERT_DIR}/ca.crt"
INGRESS_TLS_KEY="${INGRESS_CERT_DIR}/wildcard.${INGRESS_DOMAIN}.key"
INGRESS_TLS_CERT="${INGRESS_CERT_DIR}/wildcard.${INGRESS_DOMAIN}.crt"
INGRESS_TLS_CSR="${INGRESS_CERT_DIR}/wildcard.${INGRESS_DOMAIN}.csr"
INGRESS_TLS_EXT="${INGRESS_CERT_DIR}/wildcard.${INGRESS_DOMAIN}.ext"

if [[ ! -f "${INGRESS_CA_KEY}" || ! -f "${INGRESS_CA_CERT}" ]]; then
  echo "Generating local ingress CA in ${INGRESS_CERT_DIR}."
  openssl genrsa -out "${INGRESS_CA_KEY}" 4096
  openssl req -x509 -new -nodes -key "${INGRESS_CA_KEY}" -sha256 -days 3650 \
    -subj "/CN=${INGRESS_DOMAIN} local ingress CA" \
    -out "${INGRESS_CA_CERT}"
fi

cat >"${INGRESS_TLS_EXT}" <<EOF
subjectAltName = DNS:*.${INGRESS_DOMAIN},DNS:${INGRESS_DOMAIN}
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF

echo "Generating ingress wildcard certificate for *.${INGRESS_DOMAIN}."
openssl genrsa -out "${INGRESS_TLS_KEY}" 4096
openssl req -new -key "${INGRESS_TLS_KEY}" \
  -subj "/CN=*.${INGRESS_DOMAIN}" \
  -out "${INGRESS_TLS_CSR}"
openssl x509 -req -in "${INGRESS_TLS_CSR}" \
  -CA "${INGRESS_CA_CERT}" -CAkey "${INGRESS_CA_KEY}" -CAcreateserial \
  -out "${INGRESS_TLS_CERT}" -days 825 -sha256 -extfile "${INGRESS_TLS_EXT}"
rm -f "${INGRESS_TLS_CSR}"

if docker ps -a --format '{{.Names}}' | grep -qx "${REGISTRY_NAME}"; then
  echo "Replacing registry container ${REGISTRY_NAME} so it uses the current TLS certificate and host mounts."
  docker rm -f "${REGISTRY_NAME}" >/dev/null
fi

echo "Starting TLS registry ${REGISTRY_NAME} on 0.0.0.0:${REGISTRY_PORT}."
docker run -d --restart=always \
  -p "0.0.0.0:${REGISTRY_PORT}:5000" \
  --name "${REGISTRY_NAME}" \
  -v "${REGISTRY_CERT_DIR}:/certs:ro" \
  -v "${REGISTRY_DATA_DIR}:/var/lib/registry" \
  -e REGISTRY_HTTP_ADDR=0.0.0.0:5000 \
  -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
  -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
  registry:3 >/dev/null

KIND_CONFIG="$(mktemp)"
trap 'rm -f "${KIND_CONFIG}"' EXIT

append_node() {
  local role="$1"
  cat >>"${KIND_CONFIG}" <<EOF
- role: ${role}
EOF
  if [[ "${role}" == "control-plane" ]]; then
    cat >>"${KIND_CONFIG}" <<EOF
  labels:
    ingress-ready: "true"
  extraPortMappings:
  - containerPort: 443
    hostPort: ${INGRESS_HTTPS_PORT}
    listenAddress: "0.0.0.0"
    protocol: TCP
EOF
  fi
  cat >>"${KIND_CONFIG}" <<EOF
  extraMounts:
  - hostPath: ${CONTAINERD_CERTS_DIR}
    containerPath: /etc/containerd/certs.d
    readOnly: true
EOF
}

cat >"${KIND_CONFIG}" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: ${CLUSTER_NAME}
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
nodes:
EOF

append_node control-plane
for _ in $(seq 1 "${NODE_COUNT}"); do
  append_node worker
done

echo "Creating Kind cluster ${CLUSTER_NAME}."
kind create cluster --config "${KIND_CONFIG}" --name "${CLUSTER_NAME}"

echo "Applying Docker resource limits to Kind node containers."
for node in $(kind get nodes --name "${CLUSTER_NAME}"); do
  docker update --cpus "${NODE_CPUS}" -m "${NODE_MEMORY}" --memory-swap "${NODE_MEMORY_SWAP}" "${node}" >/dev/null
done

if [[ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${REGISTRY_NAME}")" == "null" ]]; then
  echo "Connecting ${REGISTRY_NAME} to Docker network kind."
  docker network connect kind "${REGISTRY_NAME}"
fi

kubectl --context "kind-${CLUSTER_NAME}" apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "${REGISTRY_HOST}:${REGISTRY_PORT}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

echo "Installing ingress-nginx."
kubectl --context "kind-${CLUSTER_NAME}" apply -f "${INGRESS_NGINX_MANIFEST}"

kubectl --context "kind-${CLUSTER_NAME}" -n ingress-nginx create secret tls ingress-wildcard-tls \
  --cert "${INGRESS_TLS_CERT}" \
  --key "${INGRESS_TLS_KEY}" \
  --dry-run=client -o yaml | kubectl --context "kind-${CLUSTER_NAME}" apply -f -

kubectl --context "kind-${CLUSTER_NAME}" -n ingress-nginx patch deployment ingress-nginx-controller \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--default-ssl-certificate=ingress-nginx/ingress-wildcard-tls"}]'

kubectl --context "kind-${CLUSTER_NAME}" -n ingress-nginx wait \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=180s

cat <<EOF

Kind cluster bootstrap is complete.

Cluster name:                 ${CLUSTER_NAME}
Registry endpoint:            ${REGISTRY_HOST}:${REGISTRY_PORT}
Ingress HTTPS endpoint:        https://*.${INGRESS_DOMAIN} via ${REGISTRY_HOST}:${INGRESS_HTTPS_PORT}
Generated artifacts:           ${GENERATED_DIR}
Registry CA for Mac Docker:    ${REGISTRY_CA_CERT}
Ingress CA for Mac browsers:   ${INGRESS_CA_CERT}
Containerd registry config:    ${CONTAINERD_REGISTRY_DIR}
Registry image data:           ${REGISTRY_DATA_DIR}

Next steps:
  1. Trust ${REGISTRY_CA_CERT} in Docker on the MacBook.
  2. Trust ${INGRESS_CA_CERT} in the MacBook browser/system keychain for HTTPS ingress.
  3. Add DNS or /etc/hosts entries for app subdomains such as grafana.${INGRESS_DOMAIN} and kafka-ui.${INGRESS_DOMAIN}.
  4. Define each app's Ingress in its Helm chart or Kubernetes manifest.
EOF
