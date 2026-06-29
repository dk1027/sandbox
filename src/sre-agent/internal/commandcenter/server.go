package commandcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sre-agent/internal/agent"
	"sre-agent/internal/alert"
	"sre-agent/internal/config"
	"sre-agent/internal/llm"
)

const (
	defaultConversationLimit = 25
	maxUserMessageBytes      = 4096
	conversationPathPrefix   = "/api/v1/conversations/"
)

var allowedSeverities = map[string]struct{}{
	"critical": {},
	"high":     {},
	"warning":  {},
	"medium":   {},
	"low":      {},
}

type AgentProcessor interface {
	HandleWebhook(context.Context, []byte) (agent.Result, error)
}

type Handler struct {
	cfg     config.Config
	preview AgentProcessor
	execute AgentProcessor
	store   *Store
}

type Store struct {
	mu            sync.RWMutex
	seq           atomic.Uint64
	conversations map[string]*Conversation
	orderedIDs    []string
}

type ConversationRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
	App            string `json:"app,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	Message        string `json:"message,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Confirm        bool   `json:"confirm,omitempty"`
}

type ConversationResponse struct {
	Conversation        Conversation `json:"conversation"`
	Result              agent.Result `json:"result"`
	ConfirmationNeeded  bool         `json:"confirmation_needed"`
	ConfirmationMessage string       `json:"confirmation_message,omitempty"`
}

type AgentSummary struct {
	ID                string    `json:"id"`
	App               string    `json:"app"`
	Namespace         string    `json:"namespace"`
	Health            string    `json:"health"`
	Ready             bool      `json:"ready"`
	RemediationMode   string    `json:"remediation_mode"`
	WebhookPath       string    `json:"webhook_path"`
	ConversationCount int       `json:"conversation_count"`
	LastActivityAt    time.Time `json:"last_activity_at,omitempty"`
}

type ConversationSummary struct {
	ID                  string    `json:"id"`
	App                 string    `json:"app"`
	Namespace           string    `json:"namespace"`
	Status              string    `json:"status"`
	PendingConfirmation bool      `json:"pending_confirmation"`
	MessageCount        int       `json:"message_count"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	LastOutcome         string    `json:"last_outcome,omitempty"`
	LastActionStatus    string    `json:"last_action_status,omitempty"`
}

type Conversation struct {
	mu                  sync.RWMutex
	ID                  string                `json:"id"`
	App                 string                `json:"app"`
	Namespace           string                `json:"namespace"`
	Status              string                `json:"status"`
	PendingConfirmation bool                  `json:"pending_confirmation"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	Messages            []ConversationMessage `json:"messages"`
	LastResult          *agent.Result         `json:"last_result,omitempty"`
	PendingRequest      *ConversationRequest  `json:"-"`
}

type ConversationMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func New(cfg config.Config, preview, execute AgentProcessor) *Handler {
	return &Handler{cfg: cfg, preview: preview, execute: execute, store: NewStore()}
}

func NewStore() *Store {
	return &Store{conversations: make(map[string]*Conversation)}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/agents":
		h.handleAgents(w, r)
	case r.URL.Path == "/api/v1/conversations":
		h.handleConversations(w, r)
	case strings.HasPrefix(r.URL.Path, conversationPathPrefix):
		h.handleConversationByID(w, r)
	default:
		h.writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	count, lastActivity := h.store.conversationStats()
	h.writeJSON(w, http.StatusOK, map[string]any{
		"agents": []AgentSummary{{
			ID:                h.agentID(),
			App:               h.cfg.AppName,
			Namespace:         h.cfg.AppNamespace,
			Health:            "healthy",
			Ready:             h.preview != nil,
			RemediationMode:   strings.ToLower(strings.TrimSpace(h.cfg.RemediationMode)),
			WebhookPath:       h.webhookPath(),
			ConversationCount: count,
			LastActivityAt:    lastActivity,
		}},
	})
}

func (h *Handler) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		summaries := h.store.listSummaries(defaultConversationLimit)
		h.writeJSON(w, http.StatusOK, map[string]any{"conversations": summaries})
	case http.MethodPost:
		h.handleConversationMutation(w, r, "")
	default:
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleConversationByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, conversationPathPrefix)
	if id == "" {
		h.writeError(w, http.StatusNotFound, "not found")
		return
	}
	if idx := strings.IndexByte(id, '/'); idx >= 0 {
		id = id[:idx]
	}
	if id == "" {
		h.writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method == http.MethodGet {
		conversation, ok := h.store.get(id)
		if !ok {
			h.writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		h.writeJSON(w, http.StatusOK, conversation)
		return
	}
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleConversationMutation(w, r, id)
}

func (h *Handler) handleConversationMutation(w http.ResponseWriter, r *http.Request, pathID string) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUserMessageBytes)
	defer r.Body.Close()

	var req ConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if pathID != "" {
		req.ConversationID = pathID
	}

	conversation, outcome, err := h.processConversation(r.Context(), req)
	if err != nil {
		if status, ok := statusFromError(err); ok {
			h.writeError(w, status, err.Error())
			return
		}
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, ConversationResponse{
		Conversation:        conversation,
		Result:              outcome.Result,
		ConfirmationNeeded:  outcome.ConfirmationNeeded,
		ConfirmationMessage: outcome.ConfirmationMessage,
	})
}

type conversationOutcome struct {
	Result              agent.Result
	ConfirmationNeeded  bool
	ConfirmationMessage string
}

func (h *Handler) processConversation(ctx context.Context, req ConversationRequest) (Conversation, conversationOutcome, error) {
	req.App = strings.TrimSpace(req.App)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Message = strings.TrimSpace(req.Message)
	req.Severity = normalizeSeverity(req.Severity)

	if req.App == "" {
		req.App = h.cfg.AppName
	}
	if req.Namespace == "" {
		req.Namespace = h.cfg.AppNamespace
	}
	if req.App == "" || req.Namespace == "" {
		return Conversation{}, conversationOutcome{}, newBadRequestError("app and namespace are required")
	}

	var conversation *Conversation
	if req.ConversationID != "" {
		var ok bool
		conversation, ok = h.store.getMutable(req.ConversationID)
		if !ok {
			return Conversation{}, conversationOutcome{}, newNotFoundError("conversation not found")
		}
		if req.Message == "" && conversation.PendingRequest != nil {
			req.Message = conversation.PendingRequest.Message
			req.App = conversation.PendingRequest.App
			req.Namespace = conversation.PendingRequest.Namespace
			req.Severity = normalizeSeverity(conversation.PendingRequest.Severity)
		}
	}
	if req.Message == "" {
		return Conversation{}, conversationOutcome{}, newBadRequestError("message is required")
	}
	if conversation == nil {
		conversation = h.store.create(req.App, req.Namespace)
	}

	conversation.addMessage("user", req.Message)
	conversation.setMetadata(req.App, req.Namespace, "processing", false, nil)

	processor := h.preview
	if req.Confirm && h.execute != nil {
		processor = h.execute
	}
	if processor == nil {
		return Conversation{}, conversationOutcome{}, fmt.Errorf("agent processor unavailable")
	}

	payload, err := buildWebhookPayload(conversation, req)
	if err != nil {
		conversation.setMetadata(req.App, req.Namespace, "error", false, nil)
		return conversation.snapshot(), conversationOutcome{}, err
	}
	result, err := processor.HandleWebhook(ctx, payload)
	if err != nil {
		conversation.setMetadata(req.App, req.Namespace, "error", false, nil)
		return conversation.snapshot(), conversationOutcome{}, err
	}

	conversation.addMessage("assistant", summarizeResult(result))
	conversation.setLastResult(result)

	outcome := conversationOutcome{Result: result}
	if req.Confirm {
		conversation.setMetadata(req.App, req.Namespace, statusForResult(result), false, nil)
		return conversation.snapshot(), outcome, nil
	}

	if len(result.AppliedActions) > 0 && result.ActionStatus == "recommended" {
		conversation.setMetadata(req.App, req.Namespace, "awaiting_confirmation", true, &req)
		outcome.ConfirmationNeeded = true
		outcome.ConfirmationMessage = "agent recommended a safe action; re-submit the same conversation with confirm=true to execute it"
		return conversation.snapshot(), outcome, nil
	}

	conversation.setMetadata(req.App, req.Namespace, statusForResult(result), false, nil)
	return conversation.snapshot(), outcome, nil
}

func buildWebhookPayload(conversation *Conversation, req ConversationRequest) ([]byte, error) {
	history := conversation.transcript(4)
	summary := req.Message
	if history != "" {
		summary = summary + "\n\nConversation history:\n" + history
	}
	summary = trimTo(summary, 3500)
	description := trimTo(req.Message, 512)
	payload := alert.Webhook{
		Status: "firing",
		Alerts: []alert.WebhookItem{{
			Status: "firing",
			Labels: map[string]string{
				"alertname": "CommandCenterRequest",
				"app":       req.App,
				"namespace": req.Namespace,
				"severity":  req.Severity,
			},
			Annotations: map[string]string{
				"summary":     summary,
				"description": description,
			},
			StartsAt: time.Now().UTC().Format(time.RFC3339),
		}},
	}
	return json.Marshal(payload)
}

func (s *Store) create(app, namespace string) *Conversation {
	now := time.Now().UTC()
	id := "conv-" + strconv.FormatUint(s.seq.Add(1), 36)
	conv := &Conversation{
		ID:        id,
		App:       app,
		Namespace: namespace,
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []ConversationMessage{},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversations[id] = conv
	s.orderedIDs = append([]string{id}, s.orderedIDs...)
	return conv
}

func (s *Store) get(id string) (Conversation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conv, ok := s.conversations[id]
	if !ok {
		return Conversation{}, false
	}
	return conv.snapshot(), true
}

func (s *Store) getMutable(id string) (*Conversation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conv, ok := s.conversations[id]
	if !ok {
		return nil, false
	}
	return conv, true
}

func (s *Store) listSummaries(limit int) []ConversationSummary {
	if limit <= 0 {
		limit = defaultConversationLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ConversationSummary, 0, min(limit, len(s.orderedIDs)))
	for _, id := range s.orderedIDs {
		conv := s.conversations[id]
		if conv == nil {
			continue
		}
		out = append(out, conv.summary())
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) conversationStats() (int, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := len(s.conversations)
	var latest time.Time
	for _, id := range s.orderedIDs {
		conv := s.conversations[id]
		if conv == nil {
			continue
		}
		current := conv.snapshot()
		if current.UpdatedAt.After(latest) {
			latest = current.UpdatedAt
		}
	}
	return count, latest
}

func (c *Conversation) addMessage(role, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Messages = append(c.Messages, ConversationMessage{
		ID:        fmt.Sprintf("%s-%d", c.ID, len(c.Messages)+1),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	})
	c.UpdatedAt = time.Now().UTC()
}

func (c *Conversation) setMetadata(app, namespace, status string, pending bool, pendingRequest *ConversationRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.App = app
	c.Namespace = namespace
	if status != "" {
		c.Status = status
	}
	c.PendingConfirmation = pending
	if pendingRequest != nil {
		req := *pendingRequest
		c.PendingRequest = &req
	} else {
		c.PendingRequest = nil
	}
	c.UpdatedAt = time.Now().UTC()
}

func (c *Conversation) setLastResult(result agent.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := result
	clone.AppliedActions = append([]llm.Action(nil), result.AppliedActions...)
	c.LastResult = &clone
	c.UpdatedAt = time.Now().UTC()
}

func (c *Conversation) transcript(limit int) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if limit <= 0 || len(c.Messages) == 0 {
		return ""
	}
	start := len(c.Messages) - limit
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for _, msg := range c.Messages[start:] {
		fmt.Fprintf(&b, "%s: %s\n", msg.Role, trimTo(msg.Content, 600))
	}
	return strings.TrimSpace(b.String())
}

func (c *Conversation) summary() ConversationSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	outcome := ""
	actionStatus := ""
	if c.LastResult != nil {
		outcome = c.LastResult.Outcome
		actionStatus = c.LastResult.ActionStatus
	}
	return ConversationSummary{
		ID:                  c.ID,
		App:                 c.App,
		Namespace:           c.Namespace,
		Status:              c.Status,
		PendingConfirmation: c.PendingConfirmation,
		MessageCount:        len(c.Messages),
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
		LastOutcome:         outcome,
		LastActionStatus:    actionStatus,
	}
}

func (c *Conversation) snapshot() Conversation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clone := Conversation{
		ID:                  c.ID,
		App:                 c.App,
		Namespace:           c.Namespace,
		Status:              c.Status,
		PendingConfirmation: c.PendingConfirmation,
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
		Messages:            append([]ConversationMessage(nil), c.Messages...),
	}
	if c.LastResult != nil {
		result := *c.LastResult
		result.AppliedActions = append([]llm.Action(nil), c.LastResult.AppliedActions...)
		clone.LastResult = &result
	}
	return clone
}

func statusForResult(result agent.Result) string {
	switch {
	case result.ActionStatus == "remediated":
		return "completed"
	case result.ActionStatus == "escalated":
		return "escalated"
	case result.Outcome == "ignored":
		return "ignored"
	case result.Outcome == "error":
		return "error"
	default:
		return "completed"
	}
}

func summarizeResult(result agent.Result) string {
	bits := []string{fmt.Sprintf("outcome=%s", result.Outcome)}
	if result.Classification != "" {
		bits = append(bits, fmt.Sprintf("classification=%s", result.Classification))
	}
	if result.ActionStatus != "" {
		bits = append(bits, fmt.Sprintf("action_status=%s", result.ActionStatus))
	}
	if result.GuardrailReason != "" {
		bits = append(bits, fmt.Sprintf("guardrail=%s", result.GuardrailReason))
	}
	if result.Decision.Recommended != "" {
		bits = append(bits, fmt.Sprintf("recommended=%s", result.Decision.Recommended))
	}
	return strings.Join(bits, " ")
}

func copyResult(result agent.Result) *agent.Result {
	clone := result
	clone.AppliedActions = append([]llm.Action(nil), result.AppliedActions...)
	return &clone
}

func normalizeSeverity(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "medium"
	}
	if _, ok := allowedSeverities[raw]; !ok {
		return "medium"
	}
	return raw
}

func trimTo(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *Handler) agentID() string {
	return h.cfg.AppName + "." + h.cfg.AppNamespace
}

func (h *Handler) webhookPath() string {
	path := strings.TrimSpace(h.cfg.AlertRoutingWebhookPath)
	if path == "" {
		return "/alerts"
	}
	return path
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, fmt.Sprintf("encode response: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]any{"error": message})
}

type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string { return e.msg }

func newBadRequestError(msg string) error { return apiError{status: http.StatusBadRequest, msg: msg} }
func newNotFoundError(msg string) error   { return apiError{status: http.StatusNotFound, msg: msg} }

func statusFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if api, ok := err.(apiError); ok {
		return api.status, true
	}
	return 0, false
}
