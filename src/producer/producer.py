"""Kafka producer with Prometheus metrics and OpenTelemetry tracing."""

from __future__ import annotations

import functools
import logging
import os
import time
from typing import Any

from confluent_kafka import Producer
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroSerializer
from confluent_kafka.serialization import MessageField, SerializationContext, StringSerializer
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode
from prometheus_client import Counter, Gauge, Histogram, start_http_server

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_PUBLISHED: Counter = Counter(
    "messages_published_total",
    "Total messages published by the Kafka producer",
    ["succeed", "queued", "reason"],
)
MESSAGE_PUBLISH_DURATION: Histogram = Histogram(
    "message_publish_duration_seconds",
    "Time spent enqueueing and confirming Kafka message delivery",
    ["result"],
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5),
)
PRODUCER_READY: Gauge = Gauge(
    "producer_ready",
    "Whether the Kafka producer finished its startup checks",
)
PRODUCER_LAST_SUCCESS_TIMESTAMP: Gauge = Gauge(
    "producer_last_success_timestamp_seconds",
    "Unix timestamp of the last successful Kafka publish",
)
PRODUCER_LAST_ERROR_TIMESTAMP: Gauge = Gauge(
    "producer_last_error_timestamp_seconds",
    "Unix timestamp of the last Kafka publish error",
)

KAFKA_BROKERS: str = os.getenv(
    "KAFKA_BROKERS", "my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092"
)
TOPIC: str = os.getenv("TOPIC", "test-topic")
METRICS_PORT: int = int(os.getenv("METRICS_PORT", "8000"))
SCHEMA_REGISTRY_URL: str = os.getenv(
    "SCHEMA_REGISTRY_URL", "http://confluent-sr.kafka.svc.cluster.local:8081"
)
OTEL_SERVICE_NAME: str = os.getenv("OTEL_SERVICE_NAME", "kafka-producer")
OTEL_ENDPOINT: str = os.getenv(
    "OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo.tracing.svc.cluster.local:4317"
)

SCHEMA_STR: str = """
{
  "namespace": "example.avro",
  "type": "record",
  "name": "User",
  "fields": [
    {"name": "name", "type": "string"},
    {"name": "age", "type": "int"}
  ]
}
"""


def configure_tracing(service_name: str, endpoint: str) -> None:
    """Configure an OTLP tracer provider for the process."""

    resource = Resource.create({"service.name": service_name})
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint, insecure=True))
    )
    trace.set_tracer_provider(provider)


def build_schema_registry_client(url: str) -> SchemaRegistryClient:
    """Create a Schema Registry client."""

    return SchemaRegistryClient({"url": url})


def delivery_report(started_at: float, err: Exception | None, msg: Any) -> None:
    """Prometheus callback that records delivery success or failure."""

    elapsed = time.perf_counter() - started_at

    if err is not None:
        logger.error("Message delivery failed: %s", err)
        MESSAGE_PUBLISH_DURATION.labels(result="failure").observe(elapsed)
        PRODUCER_LAST_ERROR_TIMESTAMP.set(time.time())
        MESSAGES_PUBLISHED.labels(
            succeed="false", queued="true", reason=type(err).__name__
        ).inc()
        return

    logger.info("Message delivered to %s [%s]", msg.topic(), msg.partition())
    MESSAGE_PUBLISH_DURATION.labels(result="success").observe(elapsed)
    PRODUCER_LAST_SUCCESS_TIMESTAMP.set(time.time())
    MESSAGES_PUBLISHED.labels(succeed="true", queued="true", reason="").inc()


def main() -> None:
    """Run the producer loop forever."""

    configure_tracing(OTEL_SERVICE_NAME, OTEL_ENDPOINT)
    tracer = trace.get_tracer(__name__)

    logger.info("Starting producer. Connecting to %s, topic: %s", KAFKA_BROKERS, TOPIC)
    start_http_server(METRICS_PORT)
    logger.info("Started metrics server on port %s", METRICS_PORT)

    # Wait for Schema Registry to be available.
    time.sleep(15)

    schema_registry_client = build_schema_registry_client(SCHEMA_REGISTRY_URL)
    avro_serializer = AvroSerializer(schema_registry_client, SCHEMA_STR)
    string_serializer = StringSerializer("utf_8")

    producer_conf: dict[str, str] = {"bootstrap.servers": KAFKA_BROKERS}
    producer = Producer(producer_conf)
    PRODUCER_READY.set(1)

    counter = 0
    while True:
        user = {"name": f"User_{counter}", "age": 20 + (counter % 50)}
        with tracer.start_as_current_span(f"produce-message-{counter}") as span:
            span.set_attribute("messaging.destination", TOPIC)
            span.set_attribute("messaging.message_id", str(counter))
            publish_started_at = time.perf_counter()
            try:
                on_delivery = functools.partial(delivery_report, publish_started_at)
                producer.produce(
                    topic=TOPIC,
                    key=string_serializer(str(counter)),
                    value=avro_serializer(
                        user,
                        SerializationContext(TOPIC, MessageField.VALUE),
                    ),
                    on_delivery=on_delivery,
                )
                logger.info("Produced message %s", counter)
            except Exception as exc:
                logger.exception("Exception producing message")
                MESSAGE_PUBLISH_DURATION.labels(result="error").observe(
                    time.perf_counter() - publish_started_at
                )
                PRODUCER_LAST_ERROR_TIMESTAMP.set(time.time())
                MESSAGES_PUBLISHED.labels(
                    succeed="false", queued="false", reason=type(exc).__name__
                ).inc()
                span.record_exception(exc)
                span.set_status(Status(StatusCode.ERROR, str(exc)))
            finally:
                try:
                    producer.poll(0)
                except Exception:
                    logger.exception("Exception polling producer")

        counter += 1
        time.sleep(10)


if __name__ == "__main__":
    main()
