#!/usr/bin/env bash
set -euo pipefail

# Create a Kind cluster configured to pull images from a TLS-enabled local
# registry that is reachable from the LAN as ${REGISTRY_HOST}:${REGISTRY_PORT}.
#
# Defaults are tailored for this test host:
#   - Mac/dev machine pushes: ryzen.local:5001/<image>:<tag>
#   - Kind workloads use the same image names.
#   - Kind node containerd gets registry trust via a host-mounted generated dir,
#     not via one-off docker cp into node containers.
#
# This script is intentionally destructive only when --recreate is passed.

CLUSTER_NAME="${CLUSTER_NAME:-kafka-cluster}"
REGISTRY_NAME="${REGISTRY_NAME:-kind-registry}"
REGISTRY_HOST="${REGISTRY_HOST:-ryzen.local}"
REGISTRY_PORT="${REGISTRY_PORT:-5001}"
GRAFANA_HOST_PORT="${GRAFANA_HOST_PORT:-3000}"
GRAFANA_NODE_PORT="${GRAFANA_NODE_PORT:-30000}"
NODE_COUNT="${NODE_COUNT:-4}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GENERATED_DIR="${GENERATED_DIR:-${REPO_ROOT}/generated/kind-registry}"
CERT_DIR="${CERT_DIR:-${GENERATED_DIR}/certs}"
REGISTRY_DATA_DIR="${REGISTRY_DATA_DIR:-${GENERATED_DIR}/registry-data}"
CONTAINERD_CERTS_DIR="${CONTAINERD_CERTS_DIR:-${GENERATED_DIR}/containerd-certs}"
RECREATE=false

usage() {
  cat <<EOF
Usage: $(basename "$0") [--recreate]

Creates a TLS-enabled Docker registry and a Kind cluster configured to pull from it.

Environment overrides:
  CLUSTER_NAME            default: kafka-cluster
  REGISTRY_NAME           default: kind-registry
  REGISTRY_HOST           default: ryzen.local
  REGISTRY_PORT           default: 5001
  GRAFANA_HOST_PORT       default: 3000
  GRAFANA_NODE_PORT       default: 30000
  NODE_COUNT              default: 4 worker nodes
  GENERATED_DIR           default: generated/kind-registry
  CERT_DIR                default: generated/kind-registry/certs
  REGISTRY_DATA_DIR       default: generated/kind-registry/registry-data
  CONTAINERD_CERTS_DIR    default: generated/kind-registry/containerd-certs

Options:
  --recreate      Delete any existing Kind cluster named CLUSTER_NAME first.
  -h, --help      Show this help.

After setup, configure Docker on the Mac to trust:
  ${CERT_DIR}/ca.crt
and push images as:
  ${REGISTRY_HOST}:${REGISTRY_PORT}/image-name:tag
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

mkdir -p "${CERT_DIR}" "${REGISTRY_DATA_DIR}" "${CONTAINERD_CERTS_DIR}"

CA_KEY="${CERT_DIR}/ca.key"
CA_CERT="${CERT_DIR}/ca.crt"
SERVER_KEY="${CERT_DIR}/registry.key"
SERVER_CERT="${CERT_DIR}/registry.crt"
SERVER_CSR="${CERT_DIR}/registry.csr"
SERVER_EXT="${CERT_DIR}/registry.ext"

if [[ ! -f "${CA_KEY}" || ! -f "${CA_CERT}" ]]; then
  echo "Generating local registry CA in ${CERT_DIR}."
  openssl genrsa -out "${CA_KEY}" 4096
  openssl req -x509 -new -nodes -key "${CA_KEY}" -sha256 -days 3650 \
    -subj "/CN=${REGISTRY_HOST} local registry CA" \
    -out "${CA_CERT}"
fi

cat >"${SERVER_EXT}" <<EOF
subjectAltName = DNS:${REGISTRY_HOST},DNS:${REGISTRY_NAME},DNS:localhost,IP:127.0.0.1
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF

# Regenerate the server certificate each run so REGISTRY_HOST/REGISTRY_NAME changes
# are reflected without needing manual cleanup.
echo "Generating registry server certificate for ${REGISTRY_HOST} and ${REGISTRY_NAME}."
openssl genrsa -out "${SERVER_KEY}" 4096
openssl req -new -key "${SERVER_KEY}" \
  -subj "/CN=${REGISTRY_HOST}" \
  -out "${SERVER_CSR}"
openssl x509 -req -in "${SERVER_CSR}" \
  -CA "${CA_CERT}" -CAkey "${CA_KEY}" -CAcreateserial \
  -out "${SERVER_CERT}" -days 825 -sha256 -extfile "${SERVER_EXT}"
rm -f "${SERVER_CSR}"

CONTAINERD_REGISTRY_DIR="${CONTAINERD_CERTS_DIR}/${REGISTRY_HOST}:${REGISTRY_PORT}"
mkdir -p "${CONTAINERD_REGISTRY_DIR}"
cp "${CA_CERT}" "${CONTAINERD_REGISTRY_DIR}/ca.crt"
cat >"${CONTAINERD_REGISTRY_DIR}/hosts.toml" <<EOF
server = "https://${REGISTRY_HOST}:${REGISTRY_PORT}"

[host."https://${REGISTRY_NAME}:5000"]
  capabilities = ["pull", "resolve"]
  ca = "/etc/containerd/certs.d/${REGISTRY_HOST}:${REGISTRY_PORT}/ca.crt"
EOF

if docker ps -a --format '{{.Names}}' | grep -qx "${REGISTRY_NAME}"; then
  echo "Replacing registry container ${REGISTRY_NAME} so it uses the current TLS certificate and host mounts."
  docker rm -f "${REGISTRY_NAME}" >/dev/null
fi

echo "Starting TLS registry ${REGISTRY_NAME} on 0.0.0.0:${REGISTRY_PORT}."
docker run -d --restart=always \
  -p "0.0.0.0:${REGISTRY_PORT}:5000" \
  --name "${REGISTRY_NAME}" \
  -v "${CERT_DIR}:/certs:ro" \
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
  extraPortMappings:
  - containerPort: ${GRAFANA_NODE_PORT}
    hostPort: ${GRAFANA_HOST_PORT}
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

cat <<EOF

Kind cluster and TLS registry are ready.

Registry host:          ${REGISTRY_HOST}:${REGISTRY_PORT}
Grafana URL:            http://${REGISTRY_HOST}:${GRAFANA_HOST_PORT}
Cluster name:           ${CLUSTER_NAME}
Generated artifacts:    ${GENERATED_DIR}
CA cert for Mac trust:  ${CA_CERT}
Containerd cert config: ${CONTAINERD_REGISTRY_DIR}
Registry image data:    ${REGISTRY_DATA_DIR}

Next steps:
  1. Trust ${CA_CERT} on the MacBook / Docker Desktop.
  2. Build and push images from the MacBook, for example:
       docker build --platform linux/amd64 -t ${REGISTRY_HOST}:${REGISTRY_PORT}/chaos-monkey:dev src/chaos_monkey
       docker push ${REGISTRY_HOST}:${REGISTRY_PORT}/chaos-monkey:dev
  3. Run make setup-monitoring to install Grafana.
  4. Open Grafana from the MacBook at http://${REGISTRY_HOST}:${GRAFANA_HOST_PORT}.
  5. Deploy apps with Helm using image.repository=${REGISTRY_HOST}:${REGISTRY_PORT}/chaos-monkey.
EOF
