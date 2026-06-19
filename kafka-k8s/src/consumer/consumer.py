import os
import time
import logging
from confluent_kafka import Consumer, KafkaError, KafkaException
from confluent_kafka.serialization import SerializationContext, MessageField
from confluent_kafka.schema_registry import SchemaRegistryClient
from confluent_kafka.schema_registry.avro import AvroDeserializer
from prometheus_client import start_http_server, Counter

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

MESSAGES_CONSUMED = Counter('messages_consumed_total', 'Total messages consumed from Kafka')

KAFKA_BROKERS = os.getenv('KAFKA_BROKERS', 'my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092')
TOPIC = os.getenv('TOPIC', 'test-topic')
GROUP_ID = os.getenv('GROUP_ID', 'test-group')
METRICS_PORT = int(os.getenv('METRICS_PORT', '8000'))
SCHEMA_REGISTRY_URL = os.getenv('SCHEMA_REGISTRY_URL', 'http://schema-registry.kafka.svc.cluster.local:8081')

def main():
    logger.info(f"Starting consumer. Connecting to {KAFKA_BROKERS}, topic: {TOPIC}")
    start_http_server(METRICS_PORT)
    logger.info(f"Started metrics server on port {METRICS_PORT}")
    
    time.sleep(15) # Wait for Schema Registry to be available

    schema_registry_conf = {'url': SCHEMA_REGISTRY_URL}
    schema_registry_client = SchemaRegistryClient(schema_registry_conf)
    avro_deserializer = AvroDeserializer(schema_registry_client)
    
    consumer_conf = {
        'bootstrap.servers': KAFKA_BROKERS,
        'group.id': GROUP_ID,
        'auto.offset.reset': 'earliest'
    }
    consumer = Consumer(consumer_conf)
    consumer.subscribe([TOPIC])
    
    try:
        while True:
            msg = consumer.poll(1.0)
            if msg is None:
                continue
            if msg.error():
                if msg.error().code() == KafkaError._PARTITION_EOF:
                    continue
                else:
                    logger.error(f"Consumer error: {msg.error()}")
                    continue
            
            try:
                user = avro_deserializer(msg.value(), SerializationContext(msg.topic(), MessageField.VALUE))
                logger.info(f"Consumed message: {user}")
                MESSAGES_CONSUMED.inc()
            except Exception as e:
                logger.error(f"Deserialization error: {e}")
    finally:
        consumer.close()

if __name__ == '__main__':
    main()
