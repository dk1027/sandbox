package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerServesCommandCenterLayout(t *testing.T) {
	t.Parallel()

	handler := newServer("sre-agent", "apps")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{"SRE Command Center", "Select an agent", "Expected effect", "I have reviewed the expected effect"} {
		if !strings.Contains(text, want) {
			t.Fatalf("page body missing %q", want)
		}
	}
}

func TestServerBootstrapsStateAndAcceptsCommands(t *testing.T) {
	t.Parallel()

	handler := newServer("sre-agent", "apps")

	snapshot := decodeState(t, handler)
	if len(snapshot.Agents) < 1 {
		t.Fatalf("expected at least one agent, got %d", len(snapshot.Agents))
	}
	if snapshot.CommandHint == "" {
		t.Fatal("expected command hint to be populated")
	}
	if snapshot.SelectedAgent.ID == "" {
		t.Fatal("expected selected agent id to be set")
	}

	selectResp := httptest.NewRecorder()
	selectReq := httptest.NewRequest(http.MethodPost, "/api/agents/consumer/select", nil)
	handler.ServeHTTP(selectResp, selectReq)
	if selectResp.Code != http.StatusOK {
		t.Fatalf("select status = %d, want %d", selectResp.Code, http.StatusOK)
	}

	snapshot = decodeState(t, handler)
	if snapshot.SelectedAgent.ID != "consumer" {
		t.Fatalf("selected agent = %q, want consumer", snapshot.SelectedAgent.ID)
	}

	payload := map[string]string{"command": "rollout restart deployment/consumer-api"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	commandResp := httptest.NewRecorder()
	commandReq := httptest.NewRequest(http.MethodPost, "/api/command", bytes.NewReader(body))
	commandReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(commandResp, commandReq)

	if commandResp.Code != http.StatusCreated {
		t.Fatalf("command status = %d, want %d", commandResp.Code, http.StatusCreated)
	}
	commandBody, _ := io.ReadAll(commandResp.Body)
	if !strings.Contains(string(commandBody), "rollout restart") {
		t.Fatalf("command response missing rollout restart text: %s", commandBody)
	}

	snapshot = decodeState(t, handler)
	if len(snapshot.Interactions) == 0 {
		t.Fatal("expected command interaction to be recorded")
	}
	last := snapshot.Interactions[0]
	if last.AgentID != "consumer" {
		t.Fatalf("interaction agent = %q, want consumer", last.AgentID)
	}
	if last.Status == "" || last.Response == "" {
		t.Fatalf("interaction missing status or response: %+v", last)
	}
}

func TestServerRejectsUnknownAgentSelection(t *testing.T) {
	t.Parallel()

	handler := newServer("sre-agent", "apps")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/missing/select", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

type stateSnapshot struct {
	Agents        []agentSummary     `json:"agents"`
	SelectedAgent agentSummary       `json:"selected_agent"`
	Interactions  []interactionEntry `json:"interactions"`
	CommandHint   string             `json:"command_hint"`
}

type agentSummary struct {
	ID string `json:"id"`
}

type interactionEntry struct {
	AgentID  string `json:"agent_id"`
	Status   string `json:"status"`
	Response string `json:"response"`
}

func decodeState(t *testing.T, handler http.Handler) stateSnapshot {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want %d", rec.Code, http.StatusOK)
	}
	var snapshot stateSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return snapshot
}
