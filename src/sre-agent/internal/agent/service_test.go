package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"sre-agent/internal/alert"
	"sre-agent/internal/config"
	"sre-agent/internal/llm"
)

type recordingRemediator struct {
	called   bool
	signal   alert.Signal
	decision llm.Decision
}

func (r *recordingRemediator) Execute(ctx context.Context, signal alert.Signal, decision llm.Decision) error {
	r.called = true
	r.signal = signal
	r.decision = decision
	return nil
}

func TestServiceHandleWebhookProcessesMatchingAlert(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"restart workload\",\"diagnosis\":\"High error rate\",\"confidence\":0.88,\"severity\":\"critical\",\"recommended\":\"roll deployment\",\"actions\":[{\"type\":\"restart\",\"target\":\"deployment/payments\"}],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	svc := NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
		ListenAddr:          ":8090",
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

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
	if got.Decision.Summary != "restart workload" {
		t.Fatalf("Decision.Summary = %q", got.Decision.Summary)
	}
	if len(got.AppliedActions) != 1 || got.AppliedActions[0].Type != "restart" {
		t.Fatalf("AppliedActions = %+v, want one restart action", got.AppliedActions)
	}
}

func TestServiceHandleWebhookIgnoresForeignAlert(t *testing.T) {
	llmCalled := false
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ignored\",\"diagnosis\":\"ignored\",\"confidence\":0.1,\"severity\":\"low\",\"recommended\":\"none\",\"actions\":[],\"escalate\":true}"}}]}`))
	}))
	defer llmSrv.Close()

	svc := NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleForeignAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.Outcome != "ignored" {
		t.Fatalf("Outcome = %q, want ignored", got.Outcome)
	}
	if llmCalled {
		t.Fatal("expected LLM not to be called for foreign alert")
	}
}

func TestServiceHandleWebhookExecutesSafeActionsInActMode(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"restart workload\",\"diagnosis\":\"pod crashloop\",\"confidence\":0.95,\"severity\":\"critical\",\"recommended\":\"restart deployment\",\"actions\":[{\"type\":\"restart\",\"target\":\"deployment/payments\"}],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	remediator := &recordingRemediator{}
	svc := NewServiceWithRemediator(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "act",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()), remediator)

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleMatchingAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.ActionStatus != "remediated" {
		t.Fatalf("ActionStatus = %q, want remediated", got.ActionStatus)
	}
	if !remediator.called {
		t.Fatal("expected remediator to be called")
	}
	if len(remediator.decision.Actions) != 1 || remediator.decision.Actions[0].Type != "restart" {
		t.Fatalf("remediator decision actions = %+v", remediator.decision.Actions)
	}
}

func TestServiceHandleWebhookEscalatesLowConfidenceAndSkipsRemediation(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"restart workload\",\"diagnosis\":\"uncertain\",\"confidence\":0.1,\"severity\":\"critical\",\"recommended\":\"page human\",\"actions\":[{\"type\":\"restart\",\"target\":\"deployment/payments\"}],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	remediator := &recordingRemediator{}
	svc := NewServiceWithRemediator(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "act",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()), remediator)

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleMatchingAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.ActionStatus != "escalated" {
		t.Fatalf("ActionStatus = %q, want escalated", got.ActionStatus)
	}
	if remediator.called {
		t.Fatal("expected remediator not to be called for low confidence")
	}
}

func TestServiceHandleWebhookFiltersUnsafeActions(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"restart workload\",\"diagnosis\":\"pod crashloop\",\"confidence\":0.95,\"severity\":\"critical\",\"recommended\":\"restart deployment\",\"actions\":[{\"type\":\"restart\",\"target\":\"deployment/payments\"},{\"type\":\"delete\",\"target\":\"namespace/apps\"}],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	svc := NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleMatchingAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.ActionStatus != "escalated" {
		t.Fatalf("ActionStatus = %q, want escalated", got.ActionStatus)
	}
	if len(got.AppliedActions) != 1 || got.AppliedActions[0].Type != "restart" {
		t.Fatalf("AppliedActions = %+v, want only the safe restart action", got.AppliedActions)
	}
	if got.GuardrailReason == "" {
		t.Fatal("expected guardrail reason to be populated")
	}
}

func TestServiceHandleWebhookIgnoresResolvedAlert(t *testing.T) {
	llmCalled := false
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"resolved\",\"diagnosis\":\"resolved\",\"confidence\":0.9,\"severity\":\"low\",\"recommended\":\"none\",\"actions\":[],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	svc := NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMAPIKey:           "EMPTY",
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

	got, err := svc.HandleWebhook(context.Background(), []byte(sampleResolvedAlertWebhook))
	if err != nil {
		t.Fatalf("HandleWebhook() error = %v", err)
	}
	if got.Outcome != "ignored" {
		t.Fatalf("Outcome = %q, want ignored", got.Outcome)
	}
	if llmCalled {
		t.Fatal("expected LLM not to be called for resolved alert")
	}
}

const sampleMatchingAlertWebhook = `{
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighErrorRate",
        "severity": "critical",
        "app": "payments",
        "namespace": "apps"
      },
      "annotations": {
        "summary": "payments error rate is high"
      },
      "startsAt": "2026-06-26T08:00:00Z"
    }
  ]
}`

const sampleForeignAlertWebhook = `{
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighErrorRate",
        "severity": "critical",
        "app": "billing",
        "namespace": "other"
      },
      "annotations": {
        "summary": "billing error rate is high"
      },
      "startsAt": "2026-06-26T08:00:00Z"
    }
  ]
}`

const sampleResolvedAlertWebhook = `{
  "status": "resolved",
  "alerts": [
    {
      "status": "resolved",
      "labels": {
        "alertname": "HighErrorRate",
        "severity": "critical",
        "app": "payments",
        "namespace": "apps"
      },
      "annotations": {
        "summary": "payments error rate recovered"
      },
      "startsAt": "2026-06-26T08:00:00Z"
    }
  ]
}`
