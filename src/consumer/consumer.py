"""Kafka consumer with Prometheus metrics and OpenTelemetry tracing."""

from __future__ import annotations

import logging
import os
import time

from confluent_kafka import Consumer, KafkaError, TopicPartition
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroDeserializer
from confluent_kafka.serialization import MessageField, SerializationContext
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode
from prometheus_client import Counter, Gauge, Histogram, start_http_server

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_CONSUMED: Counter = Counter(
    "messages_consumed_total", "Total messages consumed from Kafka"
)
MESSAGE_CONSUME_DURATION: Histogram = Histogram(
    "message_consume_duration_seconds",
    "Time spent decoding and handling Kafka messages",
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5),
)
CONSUMER_ERRORS: Counter = Counter(
    "consumer_errors_total",
    "Total Kafka consumer processing errors",
    ["stage", "reason"],
)
CONSUMER_PARTITION_LAG: Gauge = Gauge(
    "consumer_partition_lag_messages",
    "Estimated consumer lag in messages for the current partition",
    ["topic", "partition", "group_id"],
)
CONSUMER_READY: Gauge = Gauge(
    "consumer_ready",
    "Whether the Kafka consumer finished its startup checks",
)
CONSUMER_LAST_SUCCESS_TIMESTAMP: Gauge = Gauge(
    "consumer_last_success_timestamp_seconds",
    "Unix timestamp of the last successful Kafka consume",
)
CONSUMER_LAST_ERROR_TIMESTAMP: Gauge = Gauge(
    "consumer_last_error_timestamp_seconds",
    "Unix timestamp of the last Kafka consumer error",
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
    CONSUMER_READY.set(1)

    def update_partition_lag(message_partition: int, message_offset: int) -> None:
        """Record the lag for the active partition."""

        partition = TopicPartition(TOPIC, message_partition)
        _low_offset, high_offset = consumer.get_watermark_offsets(
            partition, timeout=5.0
        )
        lag = max(high_offset - message_offset - 1, 0)
        CONSUMER_PARTITION_LAG.labels(
            topic=TOPIC, partition=str(message_partition), group_id=GROUP_ID
        ).set(lag)

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
                CONSUMER_ERRORS.labels(
                    stage="poll", reason=str(error.code())
                ).inc()
                CONSUMER_LAST_ERROR_TIMESTAMP.set(time.time())
                continue

            message_started_at = time.perf_counter()
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
                    update_partition_lag(msg.partition(), msg.offset())
                    MESSAGE_CONSUME_DURATION.observe(time.perf_counter() - message_started_at)
                    CONSUMER_LAST_SUCCESS_TIMESTAMP.set(time.time())
                except Exception as exc:
                    logger.exception("Deserialization error")
                    CONSUMER_ERRORS.labels(stage="deserialize", reason=type(exc).__name__).inc()
                    CONSUMER_LAST_ERROR_TIMESTAMP.set(time.time())
                    span.record_exception(exc)
                    span.set_status(Status(StatusCode.ERROR, str(exc)))
    finally:
        consumer.close()


if __name__ == "__main__":
    main()
