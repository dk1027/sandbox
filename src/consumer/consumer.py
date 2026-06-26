"""Kafka consumer with Prometheus metrics and OpenTelemetry tracing."""

from __future__ import annotations

import logging
import os
import time

from confluent_kafka import Consumer, KafkaError
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroDeserializer
from confluent_kafka.serialization import MessageField, SerializationContext
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import BatchSpanProcessor, TracerProvider
from prometheus_client import Counter, start_http_server

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_CONSUMED: Counter = Counter(
    "messages_consumed_total", "Total messages consumed from Kafka"
)

KAFKA_BROKERS: str = os.getenv(
    "KAFKA_BROKERS", "my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092"
)
TOPIC: str = os.getenv("TOPIC", "test-topic")
GROUP_ID: str = os.getenv("GROUP_ID", "test-group")
METRICS_PORT: int = int(os.getenv("METRICS_PORT", "8000"))
SCHEMA_REGISTRY_URL: str = os.getenv(
    "SCHEMA_REGISTRY_URL", "http://confluent-sr.kafka.svc.cluster.local:8081"
)
OTEL_SERVICE_NAME: str = os.getenv("OTEL_SERVICE_NAME", "kafka-consumer")
OTEL_ENDPOINT: str = os.getenv(
    "OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo.tracing.svc.cluster.local:4317"
)


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


def main() -> None:
    """Run the consumer loop forever."""

    configure_tracing(OTEL_SERVICE_NAME, OTEL_ENDPOINT)
    tracer = trace.get_tracer(__name__)

    logger.info("Starting consumer. Connecting to %s, topic: %s", KAFKA_BROKERS, TOPIC)
    start_http_server(METRICS_PORT)
    logger.info("Started metrics server on port %s", METRICS_PORT)

    # Wait for Schema Registry to be available.
    time.sleep(15)

    schema_registry_client = build_schema_registry_client(SCHEMA_REGISTRY_URL)
    avro_deserializer = AvroDeserializer(schema_registry_client)

    consumer_conf: dict[str, str] = {
        "bootstrap.servers": KAFKA_BROKERS,
        "group.id": GROUP_ID,
        "auto.offset.reset": "earliest",
    }
    consumer = Consumer(consumer_conf)
    consumer.subscribe([TOPIC])

    try:
        while True:
            msg = consumer.poll(1.0)
            if msg is None:
                continue

            if msg.error():
                error = msg.error()
                if error.code() == KafkaError._PARTITION_EOF:
                    continue
                logger.error("Consumer error: %s", error)
                continue

            with tracer.start_as_current_span(
                f"consume-message-p{msg.partition()}-o{msg.offset()}"
            ) as span:
                span.set_attribute("messaging.destination", msg.topic())
                span.set_attribute("messaging.kafka.partition", msg.partition())
                span.set_attribute("messaging.kafka.offset", msg.offset())
                try:
                    user = avro_deserializer(
                        msg.value(), SerializationContext(msg.topic(), MessageField.VALUE)
                    )
                    logger.info("Consumed message: %s", user)
                    MESSAGES_CONSUMED.inc()
                except Exception as exc:
                    logger.exception("Deserialization error")
                    span.record_exception(exc)
                    span.set_status(trace.Status(trace.StatusCode.ERROR, str(exc)))
    finally:
        consumer.close()


if __name__ == "__main__":
    main()
