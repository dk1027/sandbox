package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Agent struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
	Health         string `json:"health"`
	Status         string `json:"status"`
	Mode           string `json:"mode"`
	OpenAlerts     int    `json:"open_alerts"`
	LastUpdated    string `json:"last_updated"`
	RecentDecision string `json:"recent_decision"`
}

type Interaction struct {
	ID        int    `json:"id"`
	Timestamp string `json:"timestamp"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Response  string `json:"response"`
}

type Snapshot struct {
	AppName       string        `json:"app_name"`
	AppNamespace  string        `json:"app_namespace"`
	LastRefreshed string        `json:"last_refreshed"`
	Agents        []Agent       `json:"agents"`
	SelectedAgent Agent         `json:"selected_agent"`
	Interactions  []Interaction `json:"interactions"`
	CommandHint   string        `json:"command_hint"`
}

type commandRequest struct {
	AgentID string `json:"agent_id"`
	Command string `json:"command"`
}

type commandResponse struct {
	Interaction Interaction `json:"interaction"`
	State       Snapshot    `json:"state"`
}

type server struct {
	mu           sync.RWMutex
	appName      string
	appNamespace string
	agents       map[string]*Agent
	order        []string
	selectedID   string
	interactions []Interaction
	nextID       int
	template     *template.Template
}

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "command_center_requests_total",
			Help: "Total HTTP requests handled by the command center UI.",
		},
		[]string{"route", "method", "status"},
	)
	commandTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "command_center_commands_total",
			Help: "Total commands sent through the command center UI.",
		},
		[]string{"agent_id", "status"},
	)
	stateSnapshotTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "command_center_state_snapshots_total",
			Help: "Total state snapshot requests served by the command center UI.",
		},
	)
)

func init() {
	prometheus.MustRegister(requestsTotal, commandTotal, stateSnapshotTotal)
}

func main() {
	listenAddr := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = ":8090"
	}
	appName := strings.TrimSpace(os.Getenv("APP_NAME"))
	if appName == "" {
		appName = "command-center"
	}
	appNamespace := strings.TrimSpace(os.Getenv("APP_NAMESPACE"))
	if appNamespace == "" {
		appNamespace = "sre"
	}

	mux := newServer(appName, appNamespace)
	mux.Handle("/metrics", promhttp.Handler())

	fmt.Printf("command-center listening on %s\n", listenAddr)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "command-center server error:", err)
		os.Exit(1)
	}
}

func newServer(appName, appNamespace string) *http.ServeMux {
	s := &server{
		appName:      defaultString(appName, "command-center"),
		appNamespace: defaultString(appNamespace, "sre"),
		agents:       map[string]*Agent{},
		order:        []string{},
		nextID:       1,
	}
	s.seed()
	s.template = template.Must(template.New("page").Parse(commandCenterPage))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/agents/", s.handleAgentSelection)
	mux.HandleFunc("/api/command", s.handleCommand)
	mux.HandleFunc("/api/agents", s.handleAgents)
	return mux
}

func (s *server) seed() {
	now := time.Now().UTC()
	seedAgents := []Agent{
		{
			ID:             s.appName,
			Name:           strings.ToUpper(s.appName[:1]) + s.appName[1:],
			Namespace:      s.appNamespace,
			Health:         "healthy",
			Status:         "ready",
			Mode:           "recommend",
			OpenAlerts:     1,
			LastUpdated:    now.Add(-7 * time.Minute).Format(time.RFC3339),
			RecentDecision: "Monitoring the current alert stream and surfacing bounded recommendations.",
		},
		{
			ID:             "producer",
			Name:           "producer-sre-agent",
			Namespace:      s.appNamespace,
			Health:         "healthy",
			Status:         "watching",
			Mode:           "recommend",
			OpenAlerts:     0,
			LastUpdated:    now.Add(-4 * time.Minute).Format(time.RFC3339),
			RecentDecision: "Confirmed Kafka producer routing and metrics are reporting normally.",
		},
		{
			ID:             "consumer",
			Name:           "consumer-sre-agent",
			Namespace:      s.appNamespace,
			Health:         "degraded",
			Status:         "needs-review",
			Mode:           "recommend",
			OpenAlerts:     2,
			LastUpdated:    now.Add(-2 * time.Minute).Format(time.RFC3339),
			RecentDecision: "A retry storm is under observation and a safe restart is being drafted.",
		},
	}
	for i := range seedAgents {
		agent := seedAgents[i]
		s.agents[agent.ID] = &agent
		s.order = append(s.order, agent.ID)
	}
	if _, ok := s.agents[s.appName]; ok {
		s.selectedID = s.appName
	} else if len(s.order) > 0 {
		s.selectedID = s.order[0]
	}
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.WithLabelValues("/healthz", http.MethodGet, "200").Inc()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeHTTPError(w, r, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	requestsTotal.WithLabelValues("/", r.Method, "200").Inc()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.template.Execute(w, map[string]string{
		"AppName":      s.appName,
		"AppNamespace": s.appNamespace,
	}); err != nil {
		writeHTTPError(w, r, http.StatusInternalServerError, err.Error())
	}
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stateSnapshotTotal.Inc()
	writeJSON(w, http.StatusOK, s.snapshot())
	requestsTotal.WithLabelValues("/api/state", r.Method, "200").Inc()
}

func (s *server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	requestsTotal.WithLabelValues("/api/agents", r.Method, "200").Inc()
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.snapshot().Agents})
}

func (s *server) handleAgentSelection(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) != 2 || parts[1] != "select" {
		writeHTTPError(w, r, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.selectAgent(parts[0]); err != nil {
		writeHTTPError(w, r, http.StatusNotFound, err.Error())
		return
	}
	requestsTotal.WithLabelValues("/api/agents/{id}/select", r.Method, "200").Inc()
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()
	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	interaction, err := s.submitCommand(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUnknownAgent) {
			status = http.StatusNotFound
		}
		writeHTTPError(w, r, status, err.Error())
		return
	}
	requestsTotal.WithLabelValues("/api/command", r.Method, "201").Inc()
	writeJSON(w, http.StatusCreated, commandResponse{Interaction: interaction, State: s.snapshot()})
}

var errUnknownAgent = errors.New("unknown agent")

func (s *server) selectAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[id]; !ok {
		return fmt.Errorf("%w %q", errUnknownAgent, id)
	}
	s.selectedID = id
	return nil
}

func (s *server) submitCommand(req commandRequest) (Interaction, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return Interaction{}, fmt.Errorf("command is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = s.selectedID
	}
	agent, ok := s.agents[agentID]
	if !ok {
		return Interaction{}, fmt.Errorf("%w %q", errUnknownAgent, agentID)
	}

	status, response := summarizeCommand(agent, command)
	agent.Status = statusForCommand(status)
	agent.Health = healthForCommand(agent.Health, command)
	agent.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	agent.RecentDecision = response
	if strings.Contains(strings.ToLower(command), "restart") || strings.Contains(strings.ToLower(command), "rollout") {
		agent.OpenAlerts = max(agent.OpenAlerts-1, 0)
	}

	interaction := Interaction{
		ID:        s.nextID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		AgentID:   agent.ID,
		AgentName: agent.Name,
		Command:   command,
		Status:    status,
		Response:  response,
	}
	s.nextID++
	s.interactions = append([]Interaction{interaction}, s.interactions...)
	if len(s.interactions) > 8 {
		s.interactions = append([]Interaction(nil), s.interactions[:8]...)
	}
	commandTotal.WithLabelValues(agent.ID, status).Inc()
	return interaction, nil
}

func (s *server) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]Agent, 0, len(s.order))
	for _, id := range s.order {
		if agent, ok := s.agents[id]; ok {
			agents = append(agents, *agent)
		}
	}
	selected := Agent{}
	if agent, ok := s.agents[s.selectedID]; ok {
		selected = *agent
	}
	interactions := make([]Interaction, len(s.interactions))
	copy(interactions, s.interactions)
	return Snapshot{
		AppName:       s.appName,
		AppNamespace:  s.appNamespace,
		LastRefreshed: time.Now().UTC().Format(time.RFC3339),
		Agents:        agents,
		SelectedAgent: selected,
		Interactions:  interactions,
		CommandHint:   "Try a bounded command like 'rollout restart deployment/api' or a diagnostic note like 'summarize current status'.",
	}
}

func summarizeCommand(agent *Agent, command string) (string, string) {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "restart") || strings.Contains(lower, "rollout"):
		return "preview", fmt.Sprintf("Previewed a bounded rollout plan for %s: %s. Confirm before execution.", agent.Name, command)
	case strings.Contains(lower, "status") || strings.Contains(lower, "summary"):
		return "responded", fmt.Sprintf("%s is %s with %d open alert(s); recent decision: %s", agent.Name, agent.Health, agent.OpenAlerts, agent.RecentDecision)
	default:
		return "acknowledged", fmt.Sprintf("%s acknowledged command: %s", agent.Name, command)
	}
}

func statusForCommand(status string) string {
	switch status {
	case "preview":
		return "awaiting-confirmation"
	case "responded":
		return "responded"
	default:
		return "acknowledged"
	}
}

func healthForCommand(currentHealth, command string) string {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "degrade") || strings.Contains(lower, "error") {
		return "degraded"
	}
	if currentHealth == "unavailable" {
		return currentHealth
	}
	return currentHealth
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		writeHTTPError(w, nil, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

func writeHTTPError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r != nil {
		route := routeLabel(r.URL.Path)
		requestsTotal.WithLabelValues(route, r.Method, fmt.Sprintf("%d", status)).Inc()
	}
	writeJSON(w, status, map[string]any{"error": message})
}

func routeLabel(path string) string {
	switch {
	case path == "/":
		return "/"
	case path == "/healthz":
		return "/healthz"
	case path == "/api/state":
		return "/api/state"
	case path == "/api/command":
		return "/api/command"
	case strings.HasPrefix(path, "/api/agents/"):
		return "/api/agents/{id}/select"
	case path == "/api/agents":
		return "/api/agents"
	default:
		return path
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

const commandCenterPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SRE Command Center</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #07111f;
      --panel: #0c1727;
      --panel-2: #101d31;
      --border: #1f2d46;
      --text: #e5eefc;
      --muted: #91a4c7;
      --accent: #66e3a6;
      --accent-2: #7aa7ff;
      --warn: #f6c177;
      --error: #ff7b72;
      --shadow: 0 20px 60px rgba(0, 0, 0, 0.35);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      background: radial-gradient(circle at top, #14223a 0%, var(--bg) 46%);
      color: var(--text);
    }

    a { color: var(--accent-2); text-decoration: none; }
    .shell {
      max-width: 1500px;
      margin: 0 auto;
      padding: 24px;
    }

    .topbar, .panel, .agent-card, .feed-item, .composer, .status-banner {
      border: 1px solid var(--border);
      background: rgba(12, 23, 39, 0.96);
      box-shadow: var(--shadow);
      border-radius: 18px;
    }

    .topbar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      padding: 20px 24px;
      margin-bottom: 20px;
    }

    .eyebrow {
      color: var(--accent);
      text-transform: uppercase;
      letter-spacing: 0.18em;
      font-size: 0.75rem;
      font-weight: 700;
      margin-bottom: 6px;
    }

    h1 { margin: 0; font-size: 2rem; }
    .subtitle { margin-top: 8px; color: var(--muted); max-width: 70ch; }

    .header-meta {
      text-align: right;
      color: var(--muted);
      min-width: 220px;
    }

    .grid {
      display: grid;
      grid-template-columns: 300px minmax(0, 1.1fr) 360px;
      gap: 20px;
      align-items: start;
    }

    .panel { padding: 18px; }
    .panel h2, .panel h3, .composer h3 { margin: 0 0 12px; }
    .panel h2 { font-size: 1.1rem; }
    .panel h3, .composer h3 { font-size: 0.98rem; color: var(--muted); text-transform: uppercase; letter-spacing: 0.08em; }

    .status-banner {
      padding: 14px 16px;
      margin-bottom: 16px;
      color: var(--muted);
    }
    .status-banner.loading { border-color: #405272; }
    .status-banner.error { border-color: #73363a; color: #ffd3d0; }
    .status-banner.success { border-color: #2f6b49; color: #d7ffe9; }

    .stack { display: grid; gap: 12px; }
    .agent-card {
      width: 100%;
      text-align: left;
      padding: 14px;
      cursor: pointer;
      color: inherit;
      background: rgba(16, 29, 49, 0.85);
    }
    .agent-card.active { outline: 2px solid rgba(102, 227, 166, 0.45); }
    .agent-card:hover { transform: translateY(-1px); }
    .agent-name { font-size: 1rem; font-weight: 700; }
    .agent-desc, .muted { color: var(--muted); }
    .badges { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
    .badge {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 5px 10px;
      border-radius: 999px;
      background: rgba(122, 167, 255, 0.12);
      color: var(--text);
      font-size: 0.8rem;
      border: 1px solid rgba(122, 167, 255, 0.18);
    }
    .badge.health-healthy { background: rgba(102, 227, 166, 0.12); border-color: rgba(102, 227, 166, 0.18); }
    .badge.health-degraded { background: rgba(246, 193, 119, 0.12); border-color: rgba(246, 193, 119, 0.18); }
    .badge.health-unavailable { background: rgba(255, 123, 114, 0.12); border-color: rgba(255, 123, 114, 0.2); }

    .selected-title { display: flex; justify-content: space-between; gap: 12px; align-items: start; }
    .selected-title h2 { margin-bottom: 6px; }
    .detail-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px;
      margin: 16px 0;
    }
    .stat {
      background: rgba(16, 29, 49, 0.8);
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 12px;
    }
    .stat-label { color: var(--muted); font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.08em; }
    .stat-value { margin-top: 6px; font-size: 1rem; font-weight: 700; }

    .composer { margin-top: 16px; padding: 16px; }
    .form-row { display: grid; gap: 10px; }
    .preview-card {
      margin-top: 2px;
      padding: 12px 14px;
      border: 1px solid var(--border);
      border-radius: 14px;
      background: rgba(9, 17, 29, 0.85);
    }
    .preview-title {
      color: var(--muted);
      font-size: 0.8rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      margin-bottom: 8px;
    }
    .preview-text { color: var(--text); line-height: 1.45; }
    .confirm-row {
      display: flex;
      gap: 10px;
      align-items: flex-start;
      color: var(--muted);
      font-size: 0.95rem;
    }
    .confirm-row input { margin-top: 4px; }
    textarea {
      width: 100%;
      min-height: 116px;
      resize: vertical;
      border-radius: 14px;
      border: 1px solid var(--border);
      background: #09111d;
      color: var(--text);
      padding: 12px 14px;
      font: inherit;
    }
    .button-row { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
    button {
      border: 1px solid transparent;
      background: linear-gradient(135deg, var(--accent-2), #8a5cff);
      color: white;
      padding: 11px 14px;
      border-radius: 12px;
      font: inherit;
      font-weight: 700;
      cursor: pointer;
    }
    button.secondary {
      background: transparent;
      border-color: var(--border);
      color: var(--text);
    }
    button:disabled { opacity: 0.55; cursor: not-allowed; }

    .feed-item {
      padding: 14px;
      margin-bottom: 12px;
      background: rgba(16, 29, 49, 0.8);
    }
    .feed-top { display: flex; justify-content: space-between; gap: 12px; }
    .feed-title { font-weight: 700; }
    .feed-command { margin: 10px 0; padding: 10px 12px; border-left: 3px solid var(--accent-2); background: rgba(122, 167, 255, 0.07); border-radius: 0 10px 10px 0; }
    .feed-response { color: #d7e6ff; }

    .empty, .loading-placeholder { color: var(--muted); padding: 12px 0; }
    .footer-note { margin-top: 14px; color: var(--muted); font-size: 0.9rem; }

    @media (max-width: 1200px) {
      .grid { grid-template-columns: 1fr; }
      .header-meta { text-align: left; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header class="topbar">
      <div>
        <div class="eyebrow">SRE Command Center</div>
        <h1>Command center for {{.AppName}}</h1>
        <div class="subtitle">Monitor agents, inspect their current posture, and send bounded commands with an explicit review step before anything state-changing happens.</div>
      </div>
      <div class="header-meta">
        <div><strong>Namespace</strong><br>{{.AppNamespace}}</div>
        <div style="margin-top: 12px;"><strong>Target</strong><br>sre.ryzen.local</div>
      </div>
    </header>

    <div id="banner" class="status-banner loading">Loading command center…</div>

    <main class="grid">
      <section class="panel">
        <h2>Agents</h2>
        <div id="agentList" class="stack loading-placeholder">Fetching agent inventory…</div>
      </section>

      <section class="panel">
        <div class="selected-title">
          <div>
            <h2 id="selectedName">Select an agent</h2>
            <div id="selectedSummary" class="muted">Choose a card to inspect its health and recent decision.</div>
          </div>
          <div id="selectedHealth"></div>
        </div>

        <div class="detail-grid">
          <div class="stat"><div class="stat-label">Namespace</div><div id="selectedNamespace" class="stat-value">—</div></div>
          <div class="stat"><div class="stat-label">Mode</div><div id="selectedMode" class="stat-value">—</div></div>
          <div class="stat"><div class="stat-label">Open alerts</div><div id="selectedAlerts" class="stat-value">—</div></div>
          <div class="stat"><div class="stat-label">Last updated</div><div id="selectedUpdated" class="stat-value">—</div></div>
        </div>

        <div class="panel" style="padding: 0; border: 0; box-shadow: none; background: transparent;">
          <h3>Recent decision</h3>
          <div id="selectedDecision" class="feed-item"><div class="empty">No agent selected yet.</div></div>
        </div>

        <div class="composer">
          <h3>Send command</h3>
          <form id="commandForm" class="form-row">
            <textarea id="commandInput" placeholder="Example: rollout restart deployment/api or summarize current health"></textarea>
            <div class="preview-card">
              <div class="preview-title">Expected effect</div>
              <div id="previewText" class="preview-text">Start typing a command to see the expected effect.</div>
            </div>
            <label class="confirm-row" for="confirmCheckbox">
              <input id="confirmCheckbox" type="checkbox">
              <span>I have reviewed the expected effect and want to send this command.</span>
            </label>
            <div class="button-row">
              <button id="sendButton" type="submit" disabled>Send command</button>
              <button id="refreshButton" type="button" class="secondary">Refresh state</button>
              <span id="composerHint" class="muted">Commands stay scoped to the selected agent.</span>
            </div>
          </form>
        </div>
      </section>

      <section class="panel">
        <h2>Interaction log</h2>
        <div id="feed" class="stack loading-placeholder">Waiting for activity…</div>
      </section>
    </main>

    <div class="footer-note">This interface is designed to be extended into a richer incident workflow without changing the URL or the basic interaction model.</div>
  </div>

  <script>
    const els = {
      banner: document.getElementById('banner'),
      agentList: document.getElementById('agentList'),
      selectedName: document.getElementById('selectedName'),
      selectedSummary: document.getElementById('selectedSummary'),
      selectedHealth: document.getElementById('selectedHealth'),
      selectedNamespace: document.getElementById('selectedNamespace'),
      selectedMode: document.getElementById('selectedMode'),
      selectedAlerts: document.getElementById('selectedAlerts'),
      selectedUpdated: document.getElementById('selectedUpdated'),
      selectedDecision: document.getElementById('selectedDecision'),
      feed: document.getElementById('feed'),
      previewText: document.getElementById('previewText'),
      confirmCheckbox: document.getElementById('confirmCheckbox'),
      commandForm: document.getElementById('commandForm'),
      commandInput: document.getElementById('commandInput'),
      sendButton: document.getElementById('sendButton'),
      refreshButton: document.getElementById('refreshButton'),
      composerHint: document.getElementById('composerHint')
    };

    let state = null;

    function setBanner(message, kind) {
      els.banner.className = 'status-banner ' + kind;
      els.banner.textContent = message;
    }

    function formatTimestamp(value) {
      if (!value) return '—';
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return value;
      return date.toLocaleString();
    }

    function healthClass(health) {
      return 'health-' + String(health || 'unknown').toLowerCase();
    }

    function renderAgents() {
      if (!state || !state.agents || !state.agents.length) {
        els.agentList.innerHTML = '<div class="empty">No agents are available.</div>';
        return;
      }
      els.agentList.innerHTML = state.agents.map(function(agent) {
        const active = state.selected_agent && agent.id === state.selected_agent.id ? 'active' : '';
        return [
          '<button class="agent-card ' + active + '" data-agent-id="' + agent.id + '">',
          '<div class="agent-name">' + escapeHtml(agent.name) + '</div>',
          '<div class="agent-desc">' + escapeHtml(agent.namespace) + ' · ' + escapeHtml(agent.status) + '</div>',
          '<div class="badges">',
          '<span class="badge ' + healthClass(agent.health) + '">' + escapeHtml(agent.health) + '</span>',
          '<span class="badge">Alerts ' + escapeHtml(String(agent.open_alerts)) + '</span>',
          '<span class="badge">Mode ' + escapeHtml(agent.mode) + '</span>',
          '</div>',
          '</button>'
        ].join('');
      }).join('');
      els.agentList.querySelectorAll('[data-agent-id]').forEach(function(button) {
        button.addEventListener('click', function() {
          selectAgent(button.getAttribute('data-agent-id'));
        });
      });
    }

    function renderSelected() {
      const agent = state && state.selected_agent;
      if (!agent || !agent.id) {
        els.selectedName.textContent = 'Select an agent';
        els.selectedSummary.textContent = 'Choose a card to inspect its health and recent decision.';
        els.selectedHealth.innerHTML = '';
        els.selectedNamespace.textContent = '—';
        els.selectedMode.textContent = '—';
        els.selectedAlerts.textContent = '—';
        els.selectedUpdated.textContent = '—';
        els.selectedDecision.innerHTML = '<div class="empty">No agent selected yet.</div>';
        return;
      }
      els.selectedName.textContent = agent.name;
      els.selectedSummary.textContent = agent.recent_decision || 'No recent decision available.';
      els.selectedHealth.innerHTML = '<span class="badge ' + healthClass(agent.health) + '">' + escapeHtml(agent.health) + '</span>';
      els.selectedNamespace.textContent = agent.namespace || '—';
      els.selectedMode.textContent = agent.mode || '—';
      els.selectedAlerts.textContent = String(agent.open_alerts);
      els.selectedUpdated.textContent = formatTimestamp(agent.last_updated);
      els.selectedDecision.innerHTML = [
        '<div class="feed-top">',
        '<div class="feed-title">' + escapeHtml(agent.status || 'unknown') + '</div>',
        '<div class="muted">' + escapeHtml(formatTimestamp(agent.last_updated)) + '</div>',
        '</div>',
        '<div class="feed-response" style="margin-top: 10px;">' + escapeHtml(agent.recent_decision || 'No decision recorded.') + '</div>'
      ].join('');
      els.composerHint.textContent = 'Sending to ' + agent.name + ' in ' + agent.namespace + '.';
    }

    function renderFeed() {
      const items = state && state.interactions ? state.interactions : [];
      if (!items.length) {
        els.feed.innerHTML = '<div class="empty">No commands have been sent yet.</div>';
        return;
      }
      els.feed.innerHTML = items.map(function(item) {
        return [
          '<article class="feed-item">',
          '<div class="feed-top">',
          '<div class="feed-title">' + escapeHtml(item.agent_name) + '</div>',
          '<div class="muted">' + escapeHtml(formatTimestamp(item.timestamp)) + '</div>',
          '</div>',
          '<div class="badges" style="margin-top: 8px;">',
          '<span class="badge">' + escapeHtml(item.status) + '</span>',
          '<span class="badge">' + escapeHtml(item.agent_id) + '</span>',
          '</div>',
          '<div class="feed-command">' + escapeHtml(item.command) + '</div>',
          '<div class="feed-response">' + escapeHtml(item.response) + '</div>',
          '</article>'
        ].join('');
      }).join('');
    }

    function deriveCommandPreview(command) {
      const agent = state && state.selected_agent;
      const agentName = agent && agent.name ? agent.name : 'the selected agent';
      const trimmed = String(command || '').trim();
      if (!trimmed) {
        return state && state.command_hint ? state.command_hint : 'Start typing a command to see the expected effect.';
      }

      const lower = trimmed.toLowerCase();
      if (lower.includes('restart') || lower.includes('rollout')) {
        return 'This will preview a bounded remediation plan for ' + agentName + ' before any state-changing request is sent.';
      }
      if (lower.includes('status') || lower.includes('summary')) {
        return 'This is read-only. It will ask ' + agentName + ' for its current health and recent decision.';
      }
      return 'This will send an advisory command to ' + agentName + ' and record the result in the interaction log.';
    }

    function updateSendState() {
      const command = els.commandInput.value.trim();
      const agentSelected = !!(state && state.selected_agent && state.selected_agent.id);
      const confirmed = els.confirmCheckbox.checked;
      els.sendButton.disabled = !(agentSelected && command && confirmed);
      els.sendButton.textContent = confirmed ? 'Confirm and send' : 'Send command';
    }

    function updateComposerPreview() {
      if (els.previewText) {
        els.previewText.textContent = deriveCommandPreview(els.commandInput.value);
      }
      updateSendState();
    }

    function renderState() {
      renderAgents();
      renderSelected();
      renderFeed();
      updateComposerPreview();
    }

    async function loadState() {
      setBanner('Loading command center…', 'loading');
      els.sendButton.disabled = true;
      try {
        const response = await fetch('/api/state', { cache: 'no-store' });
        if (!response.ok) {
          throw new Error('state request failed with ' + response.status);
        }
        state = await response.json();
        renderState();
        setBanner('Connected to ' + state.agents.length + ' agent(s).', 'success');
      } catch (error) {
        setBanner('Unable to load command center: ' + error.message, 'error');
      } finally {
        updateComposerPreview();
      }
    }

    async function selectAgent(agentId) {
      if (!agentId) return;
      try {
        const response = await fetch('/api/agents/' + encodeURIComponent(agentId) + '/select', {
          method: 'POST'
        });
        if (!response.ok) {
          const body = await response.json().catch(function() { return {}; });
          throw new Error(body.error || 'failed to select agent');
        }
        state = await response.json();
        renderState();
        els.confirmCheckbox.checked = false;
        updateComposerPreview();
        setBanner('Selected ' + state.selected_agent.name + '.', 'success');
      } catch (error) {
        setBanner('Selection failed: ' + error.message, 'error');
      }
    }

    async function sendCommand(event) {
      event.preventDefault();
      if (!state || !state.selected_agent || !state.selected_agent.id) {
        setBanner('Choose an agent before sending a command.', 'error');
        return;
      }
      const command = els.commandInput.value.trim();
      if (!command) {
        setBanner('Enter a command or message first.', 'error');
        return;
      }
      if (!els.confirmCheckbox.checked) {
        setBanner('Confirm the expected effect before sending this command.', 'error');
        return;
      }
      els.sendButton.disabled = true;
      try {
        const response = await fetch('/api/command', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({agent_id: state.selected_agent.id, command: command})
        });
        const payload = await response.json();
        if (!response.ok) {
          throw new Error(payload.error || 'command failed');
        }
        state = payload.state;
        els.commandInput.value = '';
        els.confirmCheckbox.checked = false;
        renderState();
        const interaction = payload.interaction;
        setBanner('Command ' + interaction.status + ' for ' + interaction.agent_name + '.', 'success');
      } catch (error) {
        setBanner('Command failed: ' + error.message, 'error');
      } finally {
        els.sendButton.disabled = false;
      }
    }

    function escapeHtml(value) {
      return String(value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    }

    els.commandForm.addEventListener('submit', sendCommand);
    els.refreshButton.addEventListener('click', loadState);
    els.commandInput.addEventListener('input', updateComposerPreview);
    els.confirmCheckbox.addEventListener('change', updateSendState);
    loadState();
  </script>
</body>
</html>`
