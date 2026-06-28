package config

import "testing"

func TestLoadFromEnvParsesRequiredSettings(t *testing.T) {
	env := map[string]string{
		"APP_NAME":                    "payments",
		"APP_NAMESPACE":               "apps",
		"LLM_BASE_URL":                "http://host.docker.internal:8080/v1",
		"LLM_API_KEY":                 "EMPTY",
		"LLM_MODEL":                   "gpt-4o-mini",
		"LISTEN_ADDR":                 ":8090",
		"REMEDIATION_MODE":            "recommend",
		"CONFIDENCE_THRESHOLD":        "0.75",
		"PROMETHEUS_URL":              "http://prometheus.monitoring.svc.cluster.local:9090",
		"LOKI_URL":                    "http://loki-gateway.monitoring.svc.cluster.local:3100",
		"OTEL_SERVICE_NAME":           "sre-agent-payments",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector.monitoring.svc.cluster.local:4318",
		"ALERT_ROUTING_RECEIVER":      "sre-agent-payments",
		"ALERT_ROUTING_WEBHOOK_PATH":  "/alerts",
		"ALERT_ROUTING_MATCH_LABELS":  `["namespace","app"]`,
		"ALERT_ROUTING_GROUP_BY":      `["namespace","app","alertname"]`,
	}

	cfg, err := LoadFromEnvMap(env)
	if err != nil {
		t.Fatalf("LoadFromEnvMap() error = %v", err)
	}

	if cfg.AppName != "payments" {
		t.Fatalf("AppName = %q, want %q", cfg.AppName, "payments")
	}
	if cfg.AppNamespace != "apps" {
		t.Fatalf("AppNamespace = %q, want %q", cfg.AppNamespace, "apps")
	}
	if cfg.LLMBaseURL != "http://host.docker.internal:8080/v1" {
		t.Fatalf("LLMBaseURL = %q", cfg.LLMBaseURL)
	}
	if cfg.ListenAddr != ":8090" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.RemediationMode != "recommend" {
		t.Fatalf("RemediationMode = %q", cfg.RemediationMode)
	}
	if cfg.ConfidenceThreshold != 0.75 {
		t.Fatalf("ConfidenceThreshold = %v, want 0.75", cfg.ConfidenceThreshold)
	}
	if cfg.PrometheusURL == "" || cfg.LokiURL == "" {
		t.Fatalf("expected observability URLs to be parsed: %+v", cfg)
	}
	if cfg.OTELServiceName != "sre-agent-payments" {
		t.Fatalf("OTELServiceName = %q", cfg.OTELServiceName)
	}
	if cfg.OTELExporterOTLPEndpoint != "http://otel-collector.monitoring.svc.cluster.local:4318" {
		t.Fatalf("OTELExporterOTLPEndpoint = %q", cfg.OTELExporterOTLPEndpoint)
	}
	if len(cfg.AlertRoutingMatchLabels) != 2 || cfg.AlertRoutingMatchLabels[0] != "namespace" || cfg.AlertRoutingMatchLabels[1] != "app" {
		t.Fatalf("AlertRoutingMatchLabels = %#v", cfg.AlertRoutingMatchLabels)
	}
	if len(cfg.AlertRoutingGroupBy) != 3 || cfg.AlertRoutingGroupBy[2] != "alertname" {
		t.Fatalf("AlertRoutingGroupBy = %#v", cfg.AlertRoutingGroupBy)
	}
}

func TestLoadFromEnvMapRejectsMissingRequiredValues(t *testing.T) {
	if _, err := LoadFromEnvMap(map[string]string{}); err == nil {
		t.Fatal("LoadFromEnvMap() error = nil, want error")
	}
}
