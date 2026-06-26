package llm

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestClientGenerateDecisionPostsChatCompletion(t *testing.T) {
    var sawPath string
    var sawAuth string
    var sawModel string

    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        sawPath = r.URL.Path
        sawAuth = r.Header.Get("Authorization")
        var req ChatRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            t.Fatalf("decode request: %v", err)
        }
        sawModel = req.Model
        if len(req.Messages) == 0 {
            t.Fatal("expected messages in request")
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"summary\\\":\\\"restart\\\",\\\"diagnosis\\\":\\\"pod crashloop\\\",\\\"confidence\\\":0.91,\\\"severity\\\":\\\"critical\\\",\\\"recommended\\\":\\\"restart deployment\\\",\\\"actions\\\":[{\\\"type\\\":\\\"restart\\\",\\\"target\\\":\\\"deployment/payments\\\"}],\\\"escalate\\\":false}\"}}]}"))
    }))
    defer server.Close()

    client := NewClient(server.URL, "EMPTY", server.Client())
    decision, err := client.ChatCompletion(context.Background(), ChatRequest{
        Model:    "gpt-4o-mini",
        Messages: []ChatMessage{{Role: "user", Content: "hello"}},
    })
    if err != nil {
        t.Fatalf("ChatCompletion() error = %v", err)
    }
    if sawPath != "/chat/completions" {
        t.Fatalf("path = %q, want /chat/completions", sawPath)
    }
    if sawAuth != "Bearer EMPTY" {
        t.Fatalf("auth = %q, want Bearer EMPTY", sawAuth)
    }
    if sawModel != "gpt-4o-mini" {
        t.Fatalf("model = %q", sawModel)
    }
    if decision.Summary != "restart" || decision.Escalate {
        t.Fatalf("decision = %+v", decision)
    }
}
