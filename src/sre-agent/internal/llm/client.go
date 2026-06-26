package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

type ChatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ChatRequest struct {
    Model       string        `json:"model"`
    Messages    []ChatMessage `json:"messages"`
    Temperature float64       `json:"temperature,omitempty"`
    Stream      bool          `json:"stream"`
}

type Action struct {
    Type   string `json:"type"`
    Target string `json:"target"`
}

type Decision struct {
    Summary        string   `json:"summary"`
    Diagnosis      string   `json:"diagnosis"`
    Confidence     float64  `json:"confidence"`
    Severity       string   `json:"severity"`
    Recommended    string   `json:"recommended"`
    Actions        []Action `json:"actions"`
    Escalate       bool     `json:"escalate"`
    EscalationNote string   `json:"escalation_note,omitempty"`
}

type Client struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
    if httpClient == nil {
        httpClient = &http.Client{Timeout: 30 * time.Second}
    }
    return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: httpClient}
}

func (c *Client) ChatCompletion(ctx context.Context, req ChatRequest) (Decision, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return Decision{}, fmt.Errorf("marshal request: %w", err)
    }

    endpoint := c.baseURL + "/chat/completions"
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
    if err != nil {
        return Decision{}, fmt.Errorf("build request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    if c.apiKey != "" {
        httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
    }

    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return Decision{}, fmt.Errorf("call llm: %w", err)
    }
    defer resp.Body.Close()

    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return Decision{}, fmt.Errorf("read llm response: %w", err)
    }
    if resp.StatusCode < 200 || resp.StatusCode > 299 {
        return Decision{}, fmt.Errorf("llm request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
    }

    var envelope struct {
        Choices []struct {
            Message ChatMessage `json:"message"`
        } `json:"choices"`
    }
    if err := json.Unmarshal(data, &envelope); err != nil {
        return Decision{}, fmt.Errorf("decode llm envelope: %w", err)
    }
    if len(envelope.Choices) == 0 {
        return Decision{}, fmt.Errorf("llm response missing choices")
    }

    var decision Decision
    if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &decision); err != nil {
        return Decision{}, fmt.Errorf("decode decision: %w", err)
    }
    return decision, nil
}
