package agent

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"sre-agent/internal/config"
	"sre-agent/internal/llm"
)

func TestServiceHandleWebhookWithLocalHeuristicSidecar(t *testing.T) {
	t.Parallel()

	serverURL, cleanup := startHeuristicSidecar(t)
	defer cleanup()

	svc := NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          serverURL + "/v1",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(serverURL+"/v1", "", &http.Client{Timeout: 10 * time.Second}))

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleMatchingAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.Outcome != "processed" {
		t.Fatalf("Outcome = %q, want processed", got.Outcome)
	}
	if got.ActionStatus != "recommended" {
		t.Fatalf("ActionStatus = %q, want recommended", got.ActionStatus)
	}
	if got.Decision.Escalate {
		t.Fatalf("Decision.Escalate = true, want false: %+v", got.Decision)
	}
	if len(got.Decision.Actions) != 1 {
		t.Fatalf("Decision.Actions = %+v, want one action", got.Decision.Actions)
	}
	if got.Decision.Actions[0].Type != "rollout_restart" {
		t.Fatalf("Decision.Actions[0].Type = %q, want rollout_restart", got.Decision.Actions[0].Type)
	}
	if got.Decision.Actions[0].Target != "deployment/payments" {
		t.Fatalf("Decision.Actions[0].Target = %q, want deployment/payments", got.Decision.Actions[0].Target)
	}
}

func startHeuristicSidecar(t *testing.T) (string, func()) {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	script := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../../deploy/charts/sre-agent/files/openai_server.py"))
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("stat script %q: %v", script, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	cmd := exec.Command("python3", script, "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "UPSTREAM_BASE_URL=", "UPSTREAM_API_KEY=")
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL, cleanup
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup()
	t.Fatalf("sidecar never became ready; logs:\n%s", logs.String())
	return "", func() {}
}

