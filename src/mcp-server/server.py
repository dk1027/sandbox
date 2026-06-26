"""In-cluster MCP server for Prometheus, kubectl, Kafka, and Loki operations."""

from __future__ import annotations

import json
import os
import subprocess
import urllib.parse
import urllib.request
from collections.abc import Sequence
from typing import Any

from mcp.server.fastmcp import FastMCP

app = FastMCP("sre-observability-mcp", host="0.0.0.0", port=8080)

PROMETHEUS_URL: str = os.getenv(
    "PROMETHEUS_URL", "http://prometheus-operated.monitoring.svc.cluster.local:9090"
)
LOKI_URL: str = os.getenv("LOKI_URL", "http://loki-gateway.logging.svc.cluster.local:3100")
KUBECTL_BIN: str = os.getenv("KUBECTL_BIN", "kubectl")


def prometheus_query(url: str) -> str:
    """Query the Prometheus HTTP API and return raw JSON."""

    req = urllib.request.Request(f"{PROMETHEUS_URL}{url}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read().decode()


def loki_query(url: str) -> str:
    """Query the Loki HTTP API and return raw JSON."""

    req = urllib.request.Request(f"{LOKI_URL}{url}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read().decode()


def run_kubectl(args: Sequence[str]) -> str:
    """Run kubectl and return stdout or a concise error string."""

    cmd = [KUBECTL_BIN, *args]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        return f"Error (exit {result.returncode}): {result.stderr.strip()}"
    return result.stdout


def decode_json(output: str) -> Any:
    """Parse JSON output from kubectl or return a readable error string."""

    try:
        return json.loads(output)
    except json.JSONDecodeError as exc:
        return {"error": f"Invalid JSON output: {exc.msg}", "raw": output}


@app.tool()
async def prometheus_query_range(query: str, start: str, end: str, step: str = "15s") -> str:
    """Execute a range query against Prometheus."""

    url = (
        f"/api/v1/query_range?query={urllib.parse.quote(query)}"
        f"&start={start}&end={end}&step={step}"
    )
    return prometheus_query(url)


@app.tool()
async def prometheus_instant_query(query: str) -> str:
    """Execute an instant query against Prometheus."""

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
    """Get Kubernetes resources using kubectl."""

    args = ["get", resource, "-o", "json"]
    if namespace:
        args.extend(["-n", namespace])
    if labels:
        args.extend(["-l", labels])
    return run_kubectl(args)


@app.tool()
async def kubectl_describe(resource: str, name: str, namespace: str = "") -> str:
    """Describe a Kubernetes resource."""

    args = ["describe", resource, name]
    if namespace:
        args.extend(["-n", namespace])
    return run_kubectl(args)


@app.tool()
async def kubectl_logs(
    pod: str, namespace: str = "", container: str = "", tail: int = 100
) -> str:
    """Get logs from a pod."""

    args = ["logs", pod, "--tail", str(tail)]
    if namespace:
        args.extend(["-n", namespace])
    if container:
        args.extend(["-c", container])
    return run_kubectl(args)


@app.tool()
async def kubectl_events(namespace: str = "", recent: int = 50) -> str:
    """Get recent Kubernetes events."""

    args = ["get", "events", "--sort-by=.metadata.creationTimestamp", "-o", "json"]
    if namespace:
        args.extend(["-n", namespace])
    result = run_kubectl(args)
    parsed = decode_json(result)
    if isinstance(parsed, dict) and "error" in parsed:
        return json.dumps(parsed, indent=2)

    items = parsed.get("items", [])
    items.sort(key=lambda item: item.get("metadata", {}).get("creationTimestamp", ""))
    return json.dumps(items[-recent:], indent=2)


@app.tool()
async def kubectl_top_pods(namespace: str = "") -> str:
    """Get resource usage for pods."""

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
    """Query logs via LogQL range query."""

    url = f"/loki/api/v1/query_range?query={urllib.parse.quote(query)}&start={start}&end={end}"
    return loki_query(url)


@app.tool()
async def loki_query_instant(query: str) -> str:
    """Query logs via LogQL instant query."""

    url = f"/loki/api/v1/query?query={urllib.parse.quote(query)}"
    return loki_query(url)


@app.tool()
async def kafka_topics() -> str:
    """List Kafka topics using kubectl to exec into a Kafka broker."""

    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    namespace = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec",
        "-n",
        namespace,
        pod,
        "-c",
        "kafka",
        "--",
        "/opt/kafka/bin/kafka-topics.sh",
        "--bootstrap-server",
        "localhost:9092",
        "--list",
    ]
    return run_kubectl(args)


@app.tool()
async def kafka_consumer_groups() -> str:
    """Describe Kafka consumer groups."""

    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    namespace = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec",
        "-n",
        namespace,
        pod,
        "-c",
        "kafka",
        "--",
        "/opt/kafka/bin/kafka-consumer-groups.sh",
        "--bootstrap-server",
        "localhost:9092",
        "--describe",
        "--all-groups",
    ]
    return run_kubectl(args)


@app.tool()
async def kafka_topic_describe(topic: str) -> str:
    """Describe a specific Kafka topic."""

    pod = os.getenv("KAFKA_BROKER_POD", "my-cluster-kafka-pool-a-0")
    namespace = os.getenv("KAFKA_NAMESPACE", "kafka")
    args = [
        "exec",
        "-n",
        namespace,
        pod,
        "-c",
        "kafka",
        "--",
        "/opt/kafka/bin/kafka-topics.sh",
        "--bootstrap-server",
        "localhost:9092",
        "--describe",
        "--topic",
        topic,
    ]
    return run_kubectl(args)


if __name__ == "__main__":
    app.run(transport="sse")
