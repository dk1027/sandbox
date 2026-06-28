package remediation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sre-agent/internal/alert"
	"sre-agent/internal/llm"
)

func TestClientExecuteRestartsDeployment(t *testing.T) {
	var (
		method string
		path   string
		head   string
		body   string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		head = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"kind":"Deployment"}`))
	}))
	defer server.Close()

	client := New(server.URL, "apps", "token-123", server.Client())
	err := client.Execute(context.Background(), alert.Signal{Namespace: "apps"}, llm.Decision{
		Actions: []llm.Action{{Type: "restart", Target: "deployment/payments"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if method != http.MethodPatch {
		t.Fatalf("method = %q, want %q", method, http.MethodPatch)
	}
	if path != "/apis/apps/v1/namespaces/apps/deployments/payments" {
		t.Fatalf("path = %q", path)
	}
	if head != "Bearer token-123" {
		t.Fatalf("authorization = %q", head)
	}
	if !strings.Contains(body, restartAnnotationKey) {
		t.Fatalf("patch body = %q, want restart annotation", body)
	}
}

func TestClientExecuteRejectsUnsupportedAction(t *testing.T) {
	client := New("https://example.invalid", "apps", "token-123", &http.Client{})
	err := client.Execute(context.Background(), alert.Signal{Namespace: "apps"}, llm.Decision{
		Actions: []llm.Action{{Type: "notify", Target: "deployment/payments"}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported action error")
	}
	if !strings.Contains(err.Error(), "unsupported remediation action") {
		t.Fatalf("Execute() error = %v, want unsupported remediation action", err)
	}
}
