package observability

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	webhookRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sre_agent_webhook_requests_total",
			Help: "Total webhook requests handled by the SRE agent, partitioned by outcome and decision state.",
		},
		[]string{"outcome", "classification", "action_status"},
	)
	webhookDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "sre_agent_webhook_request_duration_seconds",
			Help:    "Time spent processing webhook requests end to end.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		},
	)
	llmRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sre_agent_llm_requests_total",
			Help: "Total LLM requests issued by the SRE agent, partitioned by result.",
		},
		[]string{"result"},
	)
	llmDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "sre_agent_llm_request_duration_seconds",
			Help:    "Time spent waiting for LLM decisions.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		},
	)
	remediationActions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sre_agent_remediation_actions_total",
			Help: "Total remediation actions attempted by the SRE agent, partitioned by result and action type.",
		},
		[]string{"result", "action_type"},
	)
	guardrailBlocks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sre_agent_guardrail_blocks_total",
			Help: "Total guardrail blocks encountered by the SRE agent, partitioned by reason.",
		},
		[]string{"reason"},
	)
)

func init() {
	prometheus.MustRegister(webhookRequests)
	prometheus.MustRegister(webhookDuration)
	prometheus.MustRegister(llmRequests)
	prometheus.MustRegister(llmDuration)
	prometheus.MustRegister(remediationActions)
	prometheus.MustRegister(guardrailBlocks)
}

func Handler() http.Handler {
	return promhttp.Handler()
}

func ObserveWebhook(duration time.Duration, outcome, classification, actionStatus string) {
	webhookDuration.Observe(duration.Seconds())
	webhookRequests.WithLabelValues(normalizeLabel(outcome), normalizeLabel(classification), normalizeLabel(actionStatus)).Inc()
}

func ObserveLLM(result string, duration time.Duration) {
	llmDuration.Observe(duration.Seconds())
	llmRequests.WithLabelValues(normalizeLabel(result)).Inc()
}

func ObserveRemediation(result, actionType string) {
	remediationActions.WithLabelValues(normalizeLabel(result), normalizeLabel(actionType)).Inc()
}

func ObserveGuardrailBlock(reason string) {
	guardrailBlocks.WithLabelValues(normalizeLabel(reason)).Inc()
}

func normalizeLabel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "unknown"
	}
	return v
}
