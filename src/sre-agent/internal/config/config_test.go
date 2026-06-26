package config

import "testing"

func TestLoadFromEnvParsesRequiredSettings(t *testing.T) {
    env := map[string]string{
        "APP_NAME":             "payments",
        "APP_NAMESPACE":        "apps",
        "LLM_BASE_URL":         "http://host.docker.internal:8080/v1",
        "LLM_API_KEY":          "EMPTY",
        "LLM_MODEL":            "gpt-4o-mini",
        "LISTEN_ADDR":          ":8090",
        "REMEDIATION_MODE":     "recommend",
        "CONFIDENCE_THRESHOLD": "0.75",
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
}

func TestLoadFromEnvMapRejectsMissingRequiredValues(t *testing.T) {
    if _, err := LoadFromEnvMap(map[string]string{}); err == nil {
        t.Fatal("LoadFromEnvMap() error = nil, want error")
    }
}
