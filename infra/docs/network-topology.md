# Network topology for the Kind test environment

This document describes how network traffic is expected to flow between the MacBook development machine, the Linux test host (`ryzen.local`), Docker, the Kind cluster, the local registry, and ingress-nginx.

## Machines and roles

- MacBook: development machine. Source code is edited here and Docker images are built here.
- Linux test host: reachable as `ryzen.local` on the LAN. This host runs Docker, the TLS registry container, and the Kind Kubernetes cluster.
- Kind cluster: Kubernetes cluster running inside Docker containers on the Linux test host.
- Local registry: Docker registry container named `kind-registry`, exposed on `ryzen.local:5001` with TLS.
- ingress-nginx: Kubernetes ingress controller running inside the Kind cluster, exposed from the Linux host on port `443` only.

## DNS model

The base host name is:

```text
ryzen.local
```

Application ingress uses subdomains such as:

```text
grafana.ryzen.local
kafka-ui.ryzen.local
```

The MacBook must resolve those subdomains to the Linux test host. `ryzen.local` may resolve through mDNS, but wildcard subdomains such as `grafana.ryzen.local` usually do not resolve automatically through mDNS.

Use one of these approaches:

1. Add explicit `/etc/hosts` entries on the MacBook.
2. Configure LAN DNS to resolve the needed subdomains to the Linux test host.
3. Configure LAN DNS with a wildcard for `*.ryzen.local` if your DNS stack supports it.

Example `/etc/hosts` entries:

```text
<ryzen-lan-ip> grafana.ryzen.local
<ryzen-lan-ip> kafka-ui.ryzen.local
```

## Image push path

When the MacBook builds and pushes an image:

```text
MacBook Docker
  -> https://ryzen.local:5001/v2/...
  -> Linux host port 5001
  -> Docker registry container kind-registry:5000
  -> generated/kind-cluster/registry/data on the Linux host
```

The registry uses a generated local CA and server certificate from:

```text
generated/kind-cluster/registry/certs/
```

The MacBook Docker client must trust:

```text
generated/kind-cluster/registry/certs/ca.crt
```

Images should be tagged with the same registry host name that the cluster will use, for example:

```text
ryzen.local:5001/chaos-monkey:<tag>
```

## Image pull path inside Kind

When a pod starts with an image such as:

```text
ryzen.local:5001/chaos-monkey:<tag>
```

traffic flows like this:

```text
Kind node containerd
  -> registry host entry for ryzen.local:5001
  -> https://kind-registry:5000 over the Docker kind network
  -> Docker registry container
  -> generated/kind-cluster/registry/data on the Linux host
```

Kind nodes get their registry trust config through a host mount:

```text
generated/kind-cluster/containerd-certs/ -> /etc/containerd/certs.d:ro
```

This avoids one-time `docker cp` state inside node containers and survives container restarts.

## Browser ingress path

Application browser traffic should use HTTPS only:

```text
MacBook browser
  -> https://grafana.ryzen.local
  -> Linux host port 443
  -> Kind control-plane container port 443
  -> ingress-nginx controller
  -> Kubernetes Service selected by the app's Ingress rule
  -> application pod
```

The Kind cluster exposes only host port `443` for HTTP application ingress. Port `80` is intentionally not exposed by bootstrap.

The bootstrap script generates an ingress CA and wildcard certificate for:

```text
*.ryzen.local
ryzen.local
```

The ingress-nginx controller uses that wildcard certificate as its default HTTPS certificate. The MacBook browser or OS trust store must trust:

```text
generated/kind-cluster/ingress/certs/ca.crt
```

## Application ingress ownership

The cluster bootstrap only installs the ingress controller and default HTTPS certificate. It does not define application routes.

Application routes belong with the application owner:

- Grafana ingress belongs in the kube-prometheus-stack values file.
- Kafka UI ingress belongs in its Kubernetes manifest or a nearby Kafka UI ingress manifest.
- Helm-packaged applications should define ingress templates in their chart.

This keeps the cluster bootstrap generic and lets each app declare its own host name, service, path, and app-specific settings.

## Cluster bootstrap responsibilities

`infra/kind/bootstrap-kind-cluster.sh` is responsible for shared infrastructure:

1. Create or recreate the Kind cluster.
2. Start the TLS registry container.
3. Generate local registry TLS material.
4. Generate local ingress TLS material.
5. Mount containerd registry trust into Kind nodes.
6. Apply Docker CPU/memory limits to Kind node containers.
7. Install ingress-nginx.
8. Publish the local-registry discovery ConfigMap.

It is not responsible for:

- installing Kafka, Grafana, or application workloads
- defining application Ingress objects
- deploying Helm charts for apps
- pushing application images

## External ports

Expected externally reachable ports on `ryzen.local`:

| Port | Protocol | Purpose |
| --- | --- | --- |
| 443 | HTTPS | ingress-nginx for app browser traffic |
| 5001 | HTTPS | Docker registry push/pull |

Port `80` is not exposed by the Kind bootstrap.
