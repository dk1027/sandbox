package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"sre-agent/internal/alert"
	"sre-agent/internal/config"
	"sre-agent/internal/llm"
	"sre-agent/internal/observability"
)

type Service struct {
	cfg        config.Config
	llm        LLMClient
	remediator Remediator
	collector  observability.ContextCollector
}

type LLMClient interface {
	ChatCompletion(ctx context.Context, req llm.ChatRequest) (llm.Decision, error)
}

type Remediator interface {
	Execute(ctx context.Context, signal alert.Signal, decision llm.Decision) error
}

type Result struct {
	Outcome         string       `json:"outcome"`
	Classification  string       `json:"classification,omitempty"`
	ActionStatus    string       `json:"action_status,omitempty"`
	SkippedReason   string       `json:"skipped_reason,omitempty"`
	GuardrailReason string       `json:"guardrail_reason,omitempty"`
	Alert           alert.Signal `json:"alert,omitempty"`
	Decision        llm.Decision `json:"decision,omitempty"`
	AppliedActions  []llm.Action `json:"applied_actions,omitempty"`
}

func NewService(cfg config.Config, client LLMClient) *Service {
	return &Service{cfg: cfg, llm: client, collector: observability.NewContextCollector(cfg, &http.Client{Timeout: 5 * time.Second})}
}

func NewServiceWithRemediator(cfg config.Config, client LLMClient, remediator Remediator) *Service {
	return &Service{cfg: cfg, llm: client, remediator: remediator, collector: observability.NewContextCollector(cfg, &http.Client{Timeout: 5 * time.Second})}
}

func (s *Service) HandleWebhook(ctx context.Context, payload []byte) (result Result, err error) {
	start := time.Now()
	defer func() {
		if err != nil {
			if result.Outcome == "" {
				result.Outcome = "error"
			}
			if result.Classification == "" {
				result.Classification = "error"
			}
			if result.ActionStatus == "" {
				result.ActionStatus = "error"
			}
		}
		observability.ObserveWebhook(time.Since(start), result.Outcome, result.Classification, result.ActionStatus)
	}()

	signals, err := alert.ParseWebhook(payload)
	if err != nil {
		return Result{}, err
	}
	for _, signal := range signals {
		if !signal.Matches(s.cfg.AppName, s.cfg.AppNamespace) {
			continue
		}
		classification, reason := classifySignal(signal)
		if classification != "actionable" {
			result = Result{
				Outcome:        "ignored",
				Classification: classification,
				SkippedReason:  reason,
				Alert:          signal,
			}
			return result, nil
		}

		evidence := observability.PromptEvidence{}
		if s.collector != nil {
			evidence = s.collector.Collect(ctx, signal)
		}

		decision, err := s.llm.ChatCompletion(ctx, llm.ChatRequest{
			Model: s.cfg.LLMModel,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: "You are an SRE agent. Return one JSON object with summary, diagnosis, confidence, severity, recommended, actions, escalate, and escalation_note. Keep the response deterministic, conservative, and safe. Only emit restart or rollout_restart actions for automated remediation."},
				{Role: "user", Content: buildPrompt(s.cfg, signal, evidence)},
			},
			Temperature: 0,
			Stream:      false,
		})
		if err != nil {
			return Result{}, err
		}

		normalized := normalizeDecision(s.cfg, signal, decision)
		result = Result{
			Outcome:        "processed",
			Classification: classification,
			Alert:          signal,
			Decision:       normalized,
			AppliedActions: append([]llm.Action(nil), normalized.Actions...),
		}

		if normalized.Escalate {
			result.ActionStatus = "escalated"
			result.GuardrailReason = normalized.EscalationNote
			if result.GuardrailReason == "" {
				result.GuardrailReason = "policy requires human review"
			}
			observability.ObserveGuardrailBlock(result.GuardrailReason)
			return result, nil
		}

		switch strings.ToLower(strings.TrimSpace(s.cfg.RemediationMode)) {
		case "act":
			if len(normalized.Actions) == 0 {
				result.ActionStatus = "diagnosed"
				result.GuardrailReason = "no safe remediation actions were returned"
				observability.ObserveGuardrailBlock(result.GuardrailReason)
				return result, nil
			}
			if s.remediator == nil {
				result.ActionStatus = "recommended"
				result.GuardrailReason = "remediator unavailable"
				observability.ObserveGuardrailBlock(result.GuardrailReason)
				return result, nil
			}
			if err := s.remediator.Execute(ctx, signal, normalized); err != nil {
				return Result{}, err
			}
			result.ActionStatus = "remediated"
			return result, nil
		default:
			result.ActionStatus = "recommended"
			if len(normalized.Actions) > 0 {
				observability.ObserveRemediation("recommended", normalized.Actions[0].Type)
			}
			return result, nil
		}
	}
	result = Result{Outcome: "ignored", SkippedReason: "no in-scope alerts"}
	return result, nil
}

func buildPrompt(cfg config.Config, signal alert.Signal, evidence observability.PromptEvidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "App: %s\nNamespace: %s\nMode: %s\nConfidence threshold: %.2f\n", cfg.AppName, cfg.AppNamespace, cfg.RemediationMode, cfg.ConfidenceThreshold)
	if cfg.PrometheusURL != "" || cfg.LokiURL != "" || cfg.OTELServiceName != "" || cfg.OTELExporterOTLPEndpoint != "" {
		fmt.Fprintln(&b, "Observability settings:")
		if cfg.PrometheusURL != "" {
			fmt.Fprintf(&b, "- Prometheus URL: %s\n", cfg.PrometheusURL)
		}
		if cfg.LokiURL != "" {
			fmt.Fprintf(&b, "- Loki URL: %s\n", cfg.LokiURL)
		}
		if cfg.OTELServiceName != "" {
			fmt.Fprintf(&b, "- OTEL service name: %s\n", cfg.OTELServiceName)
		}
		if cfg.OTELExporterOTLPEndpoint != "" {
			fmt.Fprintf(&b, "- OTLP endpoint: %s\n", cfg.OTELExporterOTLPEndpoint)
		}
	}
	fmt.Fprintf(&b, "Alert: %s\nSeverity: %s\nStatus: %s\nSummary: %s\n", signal.AlertName, signal.Severity, signal.Status, signal.Summary)
	if len(signal.Labels) > 0 {
		labels, _ := json.Marshal(signal.Labels)
		fmt.Fprintf(&b, "Labels: %s\n", labels)
	}
	if len(signal.Annotations) > 0 {
		annotations, _ := json.Marshal(signal.Annotations)
		fmt.Fprintf(&b, "Annotations: %s\n", annotations)
	}
	appendSection := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", title)
		for _, line := range lines {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	appendSection("Prometheus evidence", evidence.Prometheus)
	appendSection("Loki evidence", evidence.Loki)
	appendSection("Kubernetes evidence", evidence.Kubernetes)
	appendSection("Collection diagnostics", evidence.Diagnostics)
	fmt.Fprintln(&b, "Only recommend safe, namespace-scoped, idempotent restart or rollout_restart actions. If the alert is ambiguous, non-actionable, below confidence threshold, or the evidence is sparse, set escalate=true and explain why.")
	fmt.Fprintln(&b, "Return JSON with summary, diagnosis, confidence, severity, recommended, actions, escalate, and escalation_note.")
	return b.String()
}

type classificationResult struct {
	label  string
	reason string
}

func classifySignal(signal alert.Signal) (string, string) {
	if status := strings.ToLower(strings.TrimSpace(signal.Status)); status != "" && status != "firing" {
		return "non-actionable", fmt.Sprintf("alert status is %q", signal.Status)
	}
	if isIgnoredAlertName(signal.AlertName) {
		return "non-actionable", fmt.Sprintf("alert %q is a watcher signal", signal.AlertName)
	}
	if strings.TrimSpace(signal.Summary) == "" {
		return "ambiguous", "alert is missing a summary"
	}
	return "actionable", ""
}

func isIgnoredAlertName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "watchdog", "deadmansswitch":
		return true
	default:
		return false
	}
}

func normalizeDecision(cfg config.Config, signal alert.Signal, decision llm.Decision) llm.Decision {
	decision.Summary = firstNonEmpty(strings.TrimSpace(decision.Summary), strings.TrimSpace(signal.Summary), strings.TrimSpace(signal.AlertName))
	decision.Diagnosis = strings.TrimSpace(decision.Diagnosis)
	decision.Recommended = strings.TrimSpace(decision.Recommended)
	decision.Severity = strings.ToLower(strings.TrimSpace(firstNonEmpty(decision.Severity, signal.Severity)))
	decision.EscalationNote = strings.TrimSpace(decision.EscalationNote)

	filtered := make([]llm.Action, 0, len(decision.Actions))
	seen := make(map[string]struct{}, len(decision.Actions))
	unsafeActionFound := false
	for _, action := range decision.Actions {
		action.Type = strings.ToLower(strings.TrimSpace(action.Type))
		action.Target = strings.TrimSpace(action.Target)
		if action.Type == "" || action.Target == "" {
			continue
		}
		key := action.Type + "\x00" + action.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if !isSafeAction(action) {
			unsafeActionFound = true
			observability.ObserveGuardrailBlock("unsafe_action_removed")
			continue
		}
		filtered = append(filtered, action)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Type == filtered[j].Type {
			return filtered[i].Target < filtered[j].Target
		}
		return filtered[i].Type < filtered[j].Type
	})
	decision.Actions = filtered

	if unsafeActionFound {
		decision.Escalate = true
		if decision.EscalationNote == "" {
			decision.EscalationNote = "unsafe actions were removed by policy"
		}
	}
	if decision.Confidence < cfg.ConfidenceThreshold {
		decision.Escalate = true
		if decision.EscalationNote == "" {
			decision.EscalationNote = fmt.Sprintf("confidence %.2f below threshold %.2f", decision.Confidence, cfg.ConfidenceThreshold)
		}
		observability.ObserveGuardrailBlock("confidence_below_threshold")
	}
	if decision.Summary == "" {
		decision.Summary = "SRE decision pending"
	}
	if decision.Diagnosis == "" {
		decision.Diagnosis = "model returned no diagnosis"
	}
	if decision.Recommended == "" && len(decision.Actions) > 0 {
		decision.Recommended = describeAction(decision.Actions[0])
	}
	if decision.Recommended == "" {
		decision.Recommended = "review alert and capture more evidence"
	}
	return decision
}

func isSafeAction(action llm.Action) bool {
	switch action.Type {
	case "restart", "rollout_restart":
		return true
	default:
		return false
	}
}

func describeAction(action llm.Action) string {
	switch action.Type {
	case "restart":
		return fmt.Sprintf("restart %s", action.Target)
	case "rollout_restart":
		return fmt.Sprintf("rollout restart %s", action.Target)
	case "scale":
		return fmt.Sprintf("scale %s", action.Target)
	case "notify":
		return fmt.Sprintf("notify %s", action.Target)
	case "runbook":
		return fmt.Sprintf("follow runbook for %s", action.Target)
	default:
		return fmt.Sprintf("%s %s", action.Type, action.Target)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
