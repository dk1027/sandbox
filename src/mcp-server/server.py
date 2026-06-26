"""In-cluster MCP server for Prometheus, kubectl, Kafka, and Loki operations."""

from __future__ import annotations

import functools
import json
import logging
import os
import subprocess
import time
import urllib.parse
import urllib.request
from collections.abc import Awaitable, Callable, Sequence
from typing import Any, TypeVar

from mcp.server.fastmcp import FastMCP
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode
from prometheus_client import Counter, Gauge, Histogram, start_http_server

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastMCP("sre-observability-mcp", host="0.0.0.0", port=8080)

PROMETHEUS_URL: str = os.getenv(
    "PROMETHEUS_URL", "http://prometheus-operated.monitoring.svc.cluster.local:9090"
)
LOKI_URL: str = os.getenv("LOKI_URL", "http://loki-gateway.logging.svc.cluster.local:3100")
KUBECTL_BIN: str = os.getenv("KUBECTL_BIN", "kubectl")
METRICS_PORT: int = int(os.getenv("METRICS_PORT", "8081"))
OTEL_SERVICE_NAME: str = os.getenv("OTEL_SERVICE_NAME", "sre-observability-mcp")
OTEL_ENDPOINT: str = os.getenv(
    "OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo.tracing.svc.cluster.local:4317"
)

MCP_TOOL_REQUESTS: Counter = Counter(
    "mcp_tool_requests_total",
    "Total MCP tool invocations",
    ["tool", "status"],
)
MCP_TOOL_ERRORS: Counter = Counter(
    "mcp_tool_errors_total",
    "Total MCP tool failures",
    ["tool", "error_type"],
)
MCP_TOOL_DURATION: Histogram = Histogram(
    "mcp_tool_duration_seconds",
    "Time spent handling MCP tool calls",
    ["tool"],
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10),
)
MCP_SERVER_READY: Gauge = Gauge(
    "mcp_server_ready",
    "Whether the MCP server completed startup",
)
MCP_LAST_SUCCESS_TIMESTAMP: Gauge = Gauge(
    "mcp_tool_last_success_timestamp_seconds",
    "Unix timestamp of the last successful MCP tool call",
)
MCP_LAST_ERROR_TIMESTAMP: Gauge = Gauge(
    "mcp_tool_last_error_timestamp_seconds",
    "Unix timestamp of the last failed MCP tool call",
)


def configure_tracing(service_name: str, endpoint: str) -> None:
    """Configure an OTLP tracer provider for the process."""

    resource = Resource.create({"service.name": service_name})
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint, insecure=True))
    )
    trace.set_tracer_provider(provider)


configure_tracing(OTEL_SERVICE_NAME, OTEL_ENDPOINT)
TRACER = trace.get_tracer(__name__)


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


ToolFunc = TypeVar("ToolFunc", bound=Callable[..., Awaitable[str]])


def instrument_tool(tool_name: str) -> Callable[[ToolFunc], ToolFunc]:
    """Wrap an MCP tool with metrics and trace spans."""

    def decorator(func: ToolFunc) -> ToolFunc:
        @functools.wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> str:
            started_at = time.perf_counter()
            with TRACER.start_as_current_span(f"mcp.tool.{tool_name}") as span:
                span.set_attribute("mcp.tool.name", tool_name)
                try:
                    result = await func(*args, **kwargs)
                    elapsed = time.perf_counter() - started_at
                    MCP_TOOL_REQUESTS.labels(tool=tool_name, status="success").inc()
                    MCP_TOOL_DURATION.labels(tool=tool_name).observe(elapsed)
                    MCP_LAST_SUCCESS_TIMESTAMP.set(time.time())
                    span.set_status(Status(StatusCode.OK))
                    return result
                except Exception as exc:
                    elapsed = time.perf_counter() - started_at
                    MCP_TOOL_REQUESTS.labels(tool=tool_name, status="error").inc()
                    MCP_TOOL_DURATION.labels(tool=tool_name).observe(elapsed)
                    MCP_TOOL_ERRORS.labels(tool=tool_name, error_type=type(exc).__name__).inc()
                    MCP_LAST_ERROR_TIMESTAMP.set(time.time())
                    span.record_exception(exc)
                    span.set_status(Status(StatusCode.ERROR, str(exc)))
                    logger.exception("MCP tool %s failed", tool_name)
                    raise

        return wrapper  # type: ignore[return-value]

    return decorator


@app.tool()
@instrument_tool("prometheus_query_range")
async def prometheus_query_range(query: str, start: str, end: str, step: str = "15s") -> str:
    """Execute a range query against Prometheus."""

    url = (
        f"/api/v1/query_range?query={urllib.parse.quote(query)}"
        f"&start={start}&end={end}&step={step}"
    )
    return prometheus_query(url)


@app.tool()
@instrument_tool("prometheus_instant_query")
async def prometheus_instant_query(query: str) -> str:
    """Execute an instant query against Prometheus."""

    url = f"/api/v1/query?query={urllib.parse.quote(query)}"
    return prometheus_query(url)


@app.tool()
@instrument_tool("prometheus_targets")
async def prometheus_targets() -> str:
    """List all Prometheus scrape targets and their health status."""

    return prometheus_query("/api/v1/targets")


@app.tool()
@instrument_tool("prometheus_alerts")
async def prometheus_alerts() -> str:
    """List current Prometheus alerts and their state."""

    return prometheus_query("/api/v1/alerts")


@app.tool()
@instrument_tool("kubectl_get")
async def kubectl_get(resource: str, namespace: str = "", labels: str = "") -> str:
    """Get Kubernetes resources using kubectl."""

    args = ["get", resource, "-o", "json"]
    if namespace:
        args.extend(["-n", namespace])
    if labels:
        args.extend(["-l", labels])
    return run_kubectl(args)


@app.tool()
@instrument_tool("kubectl_describe")
async def kubectl_describe(resource: str, name: str, namespace: str = "") -> str:
    """Describe a Kubernetes resource."""

    args = ["describe", resource, name]
    if namespace:
        args.extend(["-n", namespace])
    return run_kubectl(args)


@app.tool()
@instrument_tool("kubectl_logs")
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
@instrument_tool("kubectl_events")
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
@instrument_tool("kubectl_top_pods")
async def kubectl_top_pods(namespace: str = "") -> str:
    """Get resource usage for pods."""

    args = ["top", "pods"]
    if namespace:
        args.extend(["-n", namespace])
    return run_kubectl(args)


@app.tool()
@instrument_tool("kubectl_top_nodes")
async def kubectl_top_nodes() -> str:
    """Get resource usage for nodes."""

    return run_kubectl(["top", "nodes"])


@app.tool()
@instrument_tool("loki_query_range")
async def loki_query_range(query: str, start: str, end: str) -> str:
    """Query logs via LogQL range query."""

    url = f"/loki/api/v1/query_range?query={urllib.parse.quote(query)}&start={start}&end={end}"
    return loki_query(url)


@app.tool()
@instrument_tool("loki_query_instant")
async def loki_query_instant(query: str) -> str:
    """Query logs via LogQL instant query."""

    url = f"/loki/api/v1/query?query={urllib.parse.quote(query)}"
    return loki_query(url)


@app.tool()
@instrument_tool("kafka_topics")
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
@instrument_tool("kafka_consumer_groups")
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
@instrument_tool("kafka_topic_describe")
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


def main() -> None:
    """Start the MCP server and metrics exporter."""

    start_http_server(METRICS_PORT)
    MCP_SERVER_READY.set(1)
    logger.info("Started MCP metrics server on port %s", METRICS_PORT)
    app.run(transport="sse")


if __name__ == "__main__":
    main()
