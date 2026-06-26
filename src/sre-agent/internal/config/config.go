package config

import (
    "fmt"
    "os"
    "strconv"
)

type Config struct {
    AppName             string
    AppNamespace        string
    ListenAddr          string
    LLMBaseURL          string
    LLMAPIKey           string
    LLMModel            string
    RemediationMode     string
    ConfidenceThreshold float64
}

func LoadFromEnv() (Config, error) {
    return LoadFromEnvMap(map[string]string{
        "APP_NAME":             os.Getenv("APP_NAME"),
        "APP_NAMESPACE":        os.Getenv("APP_NAMESPACE"),
        "LISTEN_ADDR":          os.Getenv("LISTEN_ADDR"),
        "LLM_BASE_URL":         os.Getenv("LLM_BASE_URL"),
        "LLM_API_KEY":          os.Getenv("LLM_API_KEY"),
        "LLM_MODEL":            os.Getenv("LLM_MODEL"),
        "REMEDIATION_MODE":     os.Getenv("REMEDIATION_MODE"),
        "CONFIDENCE_THRESHOLD": os.Getenv("CONFIDENCE_THRESHOLD"),
    })
}

func LoadFromEnvMap(env map[string]string) (Config, error) {
    cfg := Config{
        ListenAddr:          defaultString(env, "LISTEN_ADDR", ":8080"),
        RemediationMode:     defaultString(env, "REMEDIATION_MODE", "recommend"),
        ConfidenceThreshold: 0.7,
        LLMAPIKey:           env["LLM_API_KEY"],
    }

    if raw := env["CONFIDENCE_THRESHOLD"]; raw != "" {
        parsed, err := strconv.ParseFloat(raw, 64)
        if err != nil {
            return Config{}, fmt.Errorf("parse CONFIDENCE_THRESHOLD: %w", err)
        }
        cfg.ConfidenceThreshold = parsed
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
