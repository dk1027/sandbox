package commandcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sre-agent/internal/agent"
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

func TestHandlerExposesConfiguredAgent(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"diagnosis\":\"ok\",\"confidence\":0.9,\"severity\":\"medium\",\"recommended\":\"review\",\"actions\":[],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	preview := agent.NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

	handler := New(config.Config{AppName: "payments", AppNamespace: "apps", RemediationMode: "recommend"}, preview, preview)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload struct {
		Agents []AgentSummary `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Agents) != 1 {
		t.Fatalf("agents = %+v, want one agent", payload.Agents)
	}
	if payload.Agents[0].App != "payments" || payload.Agents[0].Health != "healthy" {
		t.Fatalf("agent summary = %+v", payload.Agents[0])
	}
}

func TestHandlerRequiresConfirmationBeforeExecuting(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"restart workload\",\"diagnosis\":\"pod crashloop\",\"confidence\":0.95,\"severity\":\"critical\",\"recommended\":\"restart deployment\",\"actions\":[{\"type\":\"restart\",\"target\":\"deployment/payments\"}],\"escalate\":false}"}}]}`))
	}))
	defer llmSrv.Close()

	preview := agent.NewService(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "recommend",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()))

	remediator := &recordingRemediator{}
	execute := agent.NewServiceWithRemediator(config.Config{
		AppName:             "payments",
		AppNamespace:        "apps",
		LLMBaseURL:          llmSrv.URL,
		LLMModel:            "gpt-4o-mini",
		RemediationMode:     "act",
		ConfidenceThreshold: 0.7,
	}, llm.NewClient(llmSrv.URL, "", llmSrv.Client()), remediator)

	handler := New(config.Config{AppName: "payments", AppNamespace: "apps", RemediationMode: "act"}, preview, execute)

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", mustJSON(t, ConversationRequest{Message: "restart the payments deployment"}))
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", firstRec.Code)
	}

	var created ConversationResponse
	if err := json.Unmarshal(firstRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if !created.ConfirmationNeeded {
		t.Fatalf("confirmation needed = false, want true")
	}
	if created.Conversation.ID == "" {
		t.Fatal("expected conversation id")
	}
	if created.Conversation.PendingConfirmation != true {
		t.Fatalf("conversation pending = %v, want true", created.Conversation.PendingConfirmation)
	}
	if remediator.called {
		t.Fatal("remediator should not be called before confirmation")
	}

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+created.Conversation.ID, mustJSON(t, ConversationRequest{Confirm: true}))
	confirmRec := httptest.NewRecorder()
	handler.ServeHTTP(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200", confirmRec.Code)
	}
	var confirmed ConversationResponse
	if err := json.Unmarshal(confirmRec.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if confirmed.Result.ActionStatus != "remediated" {
		t.Fatalf("action status = %q, want remediated", confirmed.Result.ActionStatus)
	}
	if !remediator.called {
		t.Fatal("expected remediator to be called after confirmation")
	}
	if confirmed.Conversation.PendingConfirmation {
		t.Fatal("conversation should no longer be pending after confirmation")
	}
}

func TestHandlerRejectsEmptyMessage(t *testing.T) {
	preview := agent.NewService(config.Config{AppName: "payments", AppNamespace: "apps", LLMBaseURL: "http://example.invalid", LLMModel: "gpt-4o-mini", RemediationMode: "recommend", ConfidenceThreshold: 0.7}, llm.NewClient("http://example.invalid", "", http.DefaultClient))
	handler := New(config.Config{AppName: "payments", AppNamespace: "apps"}, preview, preview)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", mustJSON(t, ConversationRequest{}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func mustJSON(t *testing.T, value any) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return strings.NewReader(string(data))
}
