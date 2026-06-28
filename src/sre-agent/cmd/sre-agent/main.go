package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"sre-agent/internal/agent"
	"sre-agent/internal/config"
	"sre-agent/internal/llm"
	"sre-agent/internal/remediation"
	"sre-agent/internal/observability"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	serviceClient := llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey, &http.Client{Timeout: 30 * time.Second})
	service := agent.NewService(cfg, serviceClient)
	if strings.EqualFold(strings.TrimSpace(cfg.RemediationMode), "act") {
		remediator, err := remediation.NewFromEnvironment(cfg.AppNamespace)
		if err != nil {
			log.Fatalf("build remediator: %v", err)
		}
		service = agent.NewServiceWithRemediator(cfg, serviceClient, remediator)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", observability.Handler())
	mux.HandleFunc("/alerts", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.HandleWebhook(r.Context(), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("alert webhook outcome=%s classification=%s app=%s namespace=%s alert=%s action_status=%s skipped_reason=%s guardrail_reason=%s",
			result.Outcome,
			result.Classification,
			result.Alert.AppName,
			result.Alert.Namespace,
			result.Alert.AlertName,
			result.ActionStatus,
			result.SkippedReason,
			result.GuardrailReason,
		)
		w.Header().Set("Content-Type", "application/json")
		if result.Outcome == "ignored" {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	addr := cfg.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("sre-agent starting app=%s namespace=%s listen=%s llm=%s", cfg.AppName, cfg.AppNamespace, addr, cfg.LLMBaseURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "sre-agent server error:", err)
		os.Exit(1)
	}
}
