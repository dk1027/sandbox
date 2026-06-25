FROM python:3.12-slim

RUN pip install --no-cache-dir mcp requests uvicorn

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY server.py .

ENV PROMETHEUS_URL=http://prometheus-operated.monitoring.svc.cluster.local:9090
ENV LOKI_URL=http://loki-gateway.logging.svc.cluster.local:3100
ENV KAFKA_BROKER_POD=my-cluster-kafka-pool-a-0
ENV KAFKA_NAMESPACE=kafka

RUN apt-get update && apt-get install -y wget &&     wget -qO /usr/local/bin/kubectl https://dl.k8s.io/release/v1.31.0/bin/linux/amd64/kubectl &&     chmod +x /usr/local/bin/kubectl &&     apt-get purge -y wget && rm -rf /var/lib/apt/lists/*

EXPOSE 8080
CMD ["python", "server.py"]
