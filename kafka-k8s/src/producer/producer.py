import os
import time
import logging
from confluent_kafka import Producer
from confluent_kafka.serialization import StringSerializer, SerializationContext, MessageField
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroSerializer
from prometheus_client import start_http_server, Counter

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_PUBLISHED = Counter('messages_published_total', 'Total messages published to Kafka')

KAFKA_BROKERS = os.getenv('KAFKA_BROKERS', 'my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092')
TOPIC = os.getenv('TOPIC', 'test-topic')
METRICS_PORT = int(os.getenv('METRICS_PORT', '8000'))
SCHEMA_REGISTRY_URL = os.getenv('SCHEMA_REGISTRY_URL', 'http://schema-registry.kafka.svc.cluster.local:8081')

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
    else:
        logger.info(f"Message delivered to {msg.topic()} [{msg.partition()}]")
        MESSAGES_PUBLISHED.inc()

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
        try:
            user = {"name": f"User_{counter}", "age": 20 + (counter % 50)}
            producer.produce(topic=TOPIC,
                             key=string_serializer(str(counter)),
                             value=avro_serializer(user, SerializationContext(TOPIC, MessageField.VALUE)),
                             on_delivery=delivery_report)
            producer.poll(0)
            logger.info(f"Produced message {counter}")
        except Exception as e:
            logger.error(f"Exception producing message: {e}")
        
        counter += 1
        time.sleep(2)

if __name__ == '__main__':
    main()
