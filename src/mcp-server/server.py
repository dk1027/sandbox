"""
In-cluster MCP server that exposes Prometheus, kubectl, Kafka, and Loki
tools to the SRE agent via the Model Context Protocol (SSE transport).

Uses the official `mcp` Python SDK (modelcontextprotocol.io).
"""

import json
import os
import subprocess
import urllib.parse
import urllib.request

from mcp.server.fastmcp import FastMCP

app = FastMCP("sre-observability-mcp", host="0.0.0.0", port=8080)

# --- Configuration from environment ---
PROMETHEUS_URL = os.getenv("PROMETHEUS_URL", "http://prometheus-operated.monitoring.svc.cluster.local:9090")
LOKI_URL = os.getenv("LOKI_URL", "http://loki-gateway.logging.svc.cluster.local:3100")
KUBECTL_BIN = os.getenv("KUBECTL_BIN", "kubectl")


def prometheus_query(url: str) -> str:
    """Query the Prometheus HTTP API and return JSON."""
    req = urllib.request.Request(f"{PROMETHEUS_URL}{url}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read().decode()


def loki_query(url: str) -> str:
    """Query the Loki HTTP API and return JSON."""
    req = urllib.request.Request(f"{LOKI_URL}{url}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read().decode()


def run_kubectl(args: list) -> str:
    """Run kubectl and return stdout."""
    cmd = [KUBECTL_BIN] + args
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        return f"Error (exit {result.returncode}): {result.stderr.strip()}"
    return result.stdout


# --- Tool definitions ---

@app.tool()
async def prometheus_query_range(query: str, start: str, end: str, step: str = "15s") -> str:
    """Execute a PromQL range query against Prometheus.

    Args:
        query: The PromQL expression
        start: Start time (RFC3339 or Unix timestamp)
        end: End time (RFC3339 or Unix timestamp)
        step: Query resolution step width (default: 15s)
    """
    url = f"/api/v1/query_range?query={urllib.parse.quote(query)}&start={start}&end={end}&step={step}"
    return prometheus_query(url)


@app.tool()
async def prometheus_instant_query(query: str) -> str:
    """Execute an instant PromQL query against Prometheus.

    Args:
        query: The PromQL expression
    """
    url = f"/api/v1/query?query={urllib.parse.quote(query)}"
    return prometheus_query(url)


@app.tool()
async def prometheus_targets() -> str:
    """List all Prometheus scrape targets and their health status."""
    return prometheus_query("/api/v1/targets")


@app.tool()
async def prometheus_alerts() -> str:
    """List current Prometheus alerts and their state."""
    return prometheus_query("/api/v1/alerts")


@app.tool()
async def kubectl_get(resource: str, namespace: str = "", labels: str = "") -> str:
    """Get Kubernetes resources using kubectl.

    Args:
        resource: Resource type (e.g. pods, nodes, deployments, events)
        namespace: Namespace (empty for all namespaces)
        labels: Label selector (e.g. app=producer)
    """
    args = ["get", resource, "-o", "json"]
    if namespace:
        args.extend(["-n", namespace])
    if labels:
        args.extend(["-l", labels])
    return run_kubectl(args)


@app.tool()
async def kubectl_describe(resource: str, name: str, namespace: str = "") -> str:
    """Describe a Kubernetes resource.

    Args:
        resource: Resource type (e.g. pod, node, deployment)
        name: Resource name
        namespace: Namespace
    """
    args = ["describe", resource, name]
    if namespace:
        args.extend(["-n", namespace])
    return run_kubectl(args)


@app.tool()
async def kubectl_logs(pod: str, namespace: str = "", container: str = "", tail: int = 100) -> str:
    """Get logs from a pod.

    Args:
        pod: Pod name
        namespace: Namespace
        container: Container name (for multi-container pods)
        tail: Number of lines from the end (default: 100)
    """
    args = ["logs", pod, "--tail", str(tail)]
    if namespace:
        args.extend(["-n", namespace])
    if container:
        args.extend(["-c", container])
    return run_kubectl(args)


@app.tool()
async def kubectl_events(namespace: str = "", recent: int = 50) -> str:
    """Get recent Kubernetes events.

    Args:
        namespace: Namespace (empty for all)
        recent: Number of recent events (default: 50)
    """
    args = ["get", "events", "--sort-by=.metadata.creationTimestamp", "-o", "json"]
    if namespace:
        args.extend(["-n", namespace])
    result = run_kubectl(args)
    data = json.loads(result)
    items = data.get("items", [])
    items.sort(key=lambda x: x.get("metadata", {}).get("creationTimestamp", ""))
    items = items[-recent:]
    return json.dumps(items, indent=2)


@app.tool()
async def kubectl_top_pods(namespace: str = "") -> str:
    """Get resource usage for pods.

    Args:
        namespace: Namespace (empty for all)
    """
    args = ["top", "pods"]
    if namespace:
        args.extend(["-n", namespace])
    return run_kubectl(args)


@app.tool()
async def kubectl_top_nodes() -> str:
    """Get resource usage for nodes."""
    return run_kubectl(["top", "nodes"])


@app.tool()
async def loki_query_range(query: str, start: str, end: str) -> str:
    """Query logs via LogQL range query.

    Args:
        query: LogQL expression (e.g. {app="producer"} |= "error")
        start: Start time (RFC3339)
        end: End time (RFC3339)
    """
    url = f"/loki/api/v1/query_range?query={urllib.parse.quote(query)}&start={start}&end={end}"
    return loki_query(url)


@app.tool()
async def loki_query_instant(query: str) -> str:
    """Query logs via LogQL instant query.

    Args:
        query: LogQL expression
    """
    url = f"/loki/api/v1/query?query={urllib.parse.quote(query)}"
    return loki_query(url)


@app.tool()
async def kafka_topics() -> str:
    """List Kafka topics using kubectl to exec into a Kafka broker."""
    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    ns = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec", "-n", ns, pod, "-c", "kafka", "--",
        "/opt/kafka/bin/kafka-topics.sh",
        "--bootstrap-server", "localhost:9092",
        "--list",
    ]
    return run_kubectl(args)


@app.tool()
async def kafka_consumer_groups() -> str:
    """Describe Kafka consumer groups."""
    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    ns = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec", "-n", ns, pod, "-c", "kafka", "--",
        "/opt/kafka/bin/kafka-consumer-groups.sh",
        "--bootstrap-server", "localhost:9092",
        "--describe",
        "--all-groups",
    ]
    return run_kubectl(args)


@app.tool()
async def kafka_topic_describe(topic: str) -> str:
    """Describe a specific Kafka topic.

    Args:
        topic: Topic name
    """
    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    ns = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec", "-n", ns, pod, "-c", "kafka", "--",
        "/opt/kafka/bin/kafka-topics.sh",
        "--bootstrap-server", "localhost:9092",
        "--describe", "--topic", topic,
    ]
    return run_kubectl(args)


if __name__ == "__main__":
    app.run(transport="sse")
