package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesPrometheusMetrics(t *testing.T) {
	ObserveWebhook(0, "processed", "actionable", "recommended")
	ObserveLLM("success", 0)
	ObserveRemediation("success", "restart")
	ObserveGuardrailBlock("example")

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sre_agent_webhook_requests_total") {
		t.Fatalf("metrics output did not contain webhook counter: %s", body)
	}
}
