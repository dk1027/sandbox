import os
import time
import logging
from confluent_kafka import Producer
from confluent_kafka.serialization import StringSerializer, SerializationContext, MessageField
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroSerializer
from prometheus_client import start_http_server, Counter

# OpenTelemetry tracing
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_PUBLISHED = Counter(
    'messages_published_total',
    'Total messages published by the Kafka producer',
    ['succeed', 'queued', 'reason'],
)

KAFKA_BROKERS = os.getenv('KAFKA_BROKERS', 'my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092')
TOPIC = os.getenv('TOPIC', 'test-topic')
METRICS_PORT = int(os.getenv('METRICS_PORT', '8000'))
SCHEMA_REGISTRY_URL = os.getenv('SCHEMA_REGISTRY_URL', 'http://confluent-sr.kafka.svc.cluster.local:8081')
OTEL_SERVICE_NAME = os.getenv('OTEL_SERVICE_NAME', 'kafka-producer')
OTEL_ENDPOINT = os.getenv('OTEL_EXPORTER_OTLP_ENDPOINT', 'http://tempo.tracing.svc.cluster.local:4317')

# Initialize OpenTelemetry tracing
resource = Resource.create({"service.name": OTEL_SERVICE_NAME})
provider = TracerProvider(resource=resource)
processor = BatchSpanProcessor(OTLPSpanExporter(endpoint=OTEL_ENDPOINT, insecure=True))
provider.add_span_processor(processor)
trace.set_tracer_provider(provider)
tracer = trace.get_tracer(__name__)

schema_str = """
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

def delivery_report(err, msg):
    if err is not None:
        logger.error(f"Message delivery failed: {err}")
        MESSAGES_PUBLISHED.labels(succeed='false', queued='true', reason=type(err).__name__).inc()
    else:
        logger.info(f"Message delivered to {msg.topic()} [{msg.partition()}]")
        MESSAGES_PUBLISHED.labels(succeed='true', queued='true', reason='').inc()

def main():
    logger.info(f"Starting producer. Connecting to {KAFKA_BROKERS}, topic: {TOPIC}")
    start_http_server(METRICS_PORT)
    logger.info(f"Started metrics server on port {METRICS_PORT}")

    # Wait for Schema Registry to be available (simple retry logic can be added here)
    time.sleep(15)

    schema_registry_conf = {'url': SCHEMA_REGISTRY_URL}
    schema_registry_client = SchemaRegistryClient(schema_registry_conf)
    avro_serializer = AvroSerializer(schema_registry_client, schema_str)
    string_serializer = StringSerializer('utf_8')

    producer_conf = {'bootstrap.servers': KAFKA_BROKERS}
    producer = Producer(producer_conf)

    counter = 0
    while True:
        user = {"name": f"User_{counter}", "age": 20 + (counter % 50)}
        with tracer.start_as_current_span(f"produce-message-{counter}") as span:
            span.set_attribute("messaging.destination", TOPIC)
            span.set_attribute("messaging.message_id", str(counter))
            try:
                producer.produce(topic=TOPIC,
                                 key=string_serializer(str(counter)),
                                 value=avro_serializer(user, SerializationContext(TOPIC, MessageField.VALUE)),
                                 on_delivery=delivery_report)
                logger.info(f"Produced message {counter}")
            except Exception as e:
                logger.error(f"Exception producing message: {e}")
                MESSAGES_PUBLISHED.labels(succeed='false', queued='false', reason=type(e).__name__).inc()
                span.record_exception(e)
                span.set_status(trace.Status(trace.StatusCode.ERROR, str(e)))
            finally:
                try:
                    producer.poll(0)
                except Exception as e:
                    logger.error(f"Exception polling producer: {e}")

        counter += 1
        time.sleep(10)

if __name__ == '__main__':
    main()
