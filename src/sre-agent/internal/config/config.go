package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppName                  string
	AppNamespace             string
	ListenAddr               string
	LLMBaseURL               string
	LLMAPIKey                string
	LLMModel                 string
	RemediationMode          string
	ConfidenceThreshold      float64
	PrometheusURL            string
	LokiURL                  string
	OTELServiceName          string
	OTELExporterOTLPEndpoint string
	AlertRoutingReceiver     string
	AlertRoutingWebhookPath  string
	AlertRoutingMatchLabels  []string
	AlertRoutingGroupBy      []string
}

func LoadFromEnv() (Config, error) {
	return LoadFromEnvMap(map[string]string{
		"APP_NAME":                    os.Getenv("APP_NAME"),
		"APP_NAMESPACE":               os.Getenv("APP_NAMESPACE"),
		"LISTEN_ADDR":                 os.Getenv("LISTEN_ADDR"),
		"LLM_BASE_URL":                os.Getenv("LLM_BASE_URL"),
		"LLM_API_KEY":                 os.Getenv("LLM_API_KEY"),
		"LLM_MODEL":                   os.Getenv("LLM_MODEL"),
		"REMEDIATION_MODE":            os.Getenv("REMEDIATION_MODE"),
		"CONFIDENCE_THRESHOLD":        os.Getenv("CONFIDENCE_THRESHOLD"),
		"PROMETHEUS_URL":              os.Getenv("PROMETHEUS_URL"),
		"LOKI_URL":                    os.Getenv("LOKI_URL"),
		"OTEL_SERVICE_NAME":           os.Getenv("OTEL_SERVICE_NAME"),
		"OTEL_EXPORTER_OTLP_ENDPOINT": os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		"ALERT_ROUTING_RECEIVER":      os.Getenv("ALERT_ROUTING_RECEIVER"),
		"ALERT_ROUTING_WEBHOOK_PATH":  os.Getenv("ALERT_ROUTING_WEBHOOK_PATH"),
		"ALERT_ROUTING_MATCH_LABELS":  os.Getenv("ALERT_ROUTING_MATCH_LABELS"),
		"ALERT_ROUTING_GROUP_BY":      os.Getenv("ALERT_ROUTING_GROUP_BY"),
	})
}

func LoadFromEnvMap(env map[string]string) (Config, error) {
	cfg := Config{
		ListenAddr:               defaultString(env, "LISTEN_ADDR", ":8080"),
		RemediationMode:          defaultString(env, "REMEDIATION_MODE", "recommend"),
		ConfidenceThreshold:      0.7,
		LLMAPIKey:                env["LLM_"+"API_KEY"],
		PrometheusURL:            strings.TrimSpace(env["PROMETHEUS_URL"]),
		LokiURL:                  strings.TrimSpace(env["LOKI_URL"]),
		OTELServiceName:          strings.TrimSpace(env["OTEL_SERVICE_NAME"]),
		OTELExporterOTLPEndpoint: strings.TrimSpace(env["OTEL_EXPORTER_OTLP_ENDPOINT"]),
		AlertRoutingReceiver:     strings.TrimSpace(env["ALERT_ROUTING_RECEIVER"]),
		AlertRoutingWebhookPath:  strings.TrimSpace(env["ALERT_ROUTING_WEBHOOK_PATH"]),
	}

	if raw := env["CONFIDENCE_THRESHOLD"]; raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse CONFIDENCE_THRESHOLD: %w", err)
		}
		cfg.ConfidenceThreshold = parsed
	}

	var err error
	if cfg.AlertRoutingMatchLabels, err = parseStringList(env["ALERT_ROUTING_MATCH_LABELS"]); err != nil {
		return Config{}, fmt.Errorf("parse ALERT_ROUTING_MATCH_LABELS: %w", err)
	}
	if cfg.AlertRoutingGroupBy, err = parseStringList(env["ALERT_ROUTING_GROUP_BY"]); err != nil {
		return Config{}, fmt.Errorf("parse ALERT_ROUTING_GROUP_BY: %w", err)
	}

	cfg.AppName = env["APP_NAME"]
	cfg.AppNamespace = env["APP_NAMESPACE"]
	cfg.LLMBaseURL = env["LLM_BASE_URL"]
	cfg.LLMModel = env["LLM_MODEL"]

	if cfg.AppName == "" {
		return Config{}, fmt.Errorf("APP_NAME is required")
	}
	if cfg.AppNamespace == "" {
		return Config{}, fmt.Errorf("APP_NAMESPACE is required")
	}
	if cfg.LLMBaseURL == "" {
		return Config{}, fmt.Errorf("LLM_BASE_URL is required")
	}
	if cfg.LLMModel == "" {
		return Config{}, fmt.Errorf("LLM_MODEL is required")
	}

	return cfg, nil
}

func defaultString(env map[string]string, key, fallback string) string {
	if v := env[key]; v != "" {
		return v
	}
	return fallback
}

func parseStringList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		return values, nil
	}
	parts := strings.Split(raw, ",")
	values = make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no values found")
	}
	return values, nil
}
