package main

import (
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "time"

    "sre-agent/internal/agent"
    "sre-agent/internal/config"
    "sre-agent/internal/llm"
)

func main() {
    cfg, err := config.LoadFromEnv()
    if err != nil {
        log.Fatalf("load config: %v", err)
    }

    service := agent.NewService(cfg, llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey, &http.Client{Timeout: 30 * time.Second}))
    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })
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
