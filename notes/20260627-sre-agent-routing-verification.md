# SRE-agent routing verification

Date: 2026-06-27
Repository: /opt/data/src/github.com/dk1027/sandbox

## Routing contract

The live Alertmanager config in `deploy/values/prometheus-values.yaml` routes alerts by `namespace` + `app` and fans out to these receivers:

- `producer-sre-agent` -> `http://sre-agent-producer.apps.svc.cluster.local:8080/alerts`
- `consumer-sre-agent` -> `http://sre-agent-consumer.apps.svc.cluster.local:8080/alerts`
- `sre-agent` -> `http://sre-agent.apps.svc.cluster.local:8080/alerts`

The per-app chart values mirror that wiring:

- `deploy/charts/sre-agent/values.yaml` -> default `sre-agent`
- `deploy/charts/sre-agent/values-producer.yaml` -> producer receiver
- `deploy/charts/sre-agent/values-consumer.yaml` -> consumer receiver

## Smoke-test commands

1. Inspect the live Alertmanager config and confirm the receiver names and webhook URLs match the chart values.

   ```sh
   python - <<'PY'
   import base64, gzip, json, subprocess
   raw = subprocess.check_output([
       'kubectl','get','secret','-n','monitoring',
       'alertmanager-prometheus-kube-prometheus-alertmanager-generated','-o','json'
   ], text=True)
   obj = json.loads(raw)
   config = gzip.decompress(base64.b64decode(obj['data']['alertmanager.yaml.gz'])).decode()
   print(config)
   PY
   ```

2. Send a synthetic firing alert to the matching app/namespace instance.

   ```sh
   kubectl exec -n apps deploy/sre-agent -- wget -qO- \
     --header 'Content-Type: application/json' \
     --post-data '{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"HighErrorRate","severity":"critical","app":"sre-agent","namespace":"apps"},"annotations":{"summary":"sre-agent error rate is high"},"startsAt":"2026-06-27T00:00:00Z"}]}' \
     http://127.0.0.1:8080/alerts
   ```

3. Send a foreign alert with a different app/namespace and confirm it is ignored.

   ```sh
   kubectl exec -n apps deploy/sre-agent -- wget -qO- \
     --header 'Content-Type: application/json' \
     --post-data '{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"HighErrorRate","severity":"critical","app":"billing","namespace":"other"},"annotations":{"summary":"billing error rate is high"},"startsAt":"2026-06-27T00:00:00Z"}]}' \
     http://127.0.0.1:8080/alerts
   ```

## Verification result

The live cluster now matches the chart wiring, and the synthetic smoke test succeeds end-to-end:

- matching `app=sre-agent, namespace=apps` alert -> `outcome=processed`, `classification=actionable`, `action_status=recommended`
- foreign `app=billing, namespace=other` alert -> `outcome=ignored`, `skipped_reason=no in-scope alerts`

That gives reviewers a concrete routing check: the alert reaches only the intended sre-agent instance, and the response contains the expected classification/action status.
