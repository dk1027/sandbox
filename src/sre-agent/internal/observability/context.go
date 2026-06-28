package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sre-agent/internal/alert"
	"sre-agent/internal/config"
)

type PromptEvidence struct {
	Prometheus  []string
	Loki        []string
	Kubernetes  []string
	Diagnostics []string
}

type ContextCollector interface {
	Collect(ctx context.Context, signal alert.Signal) PromptEvidence
}

type Collector struct {
	cfg         config.Config
	httpClient  *http.Client
	kubeClient  *http.Client
	kubeAPIBase string
	kubeToken   string
}

func NewContextCollector(cfg config.Config, httpClient *http.Client) *Collector {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	collector := &Collector{cfg: cfg, httpClient: httpClient}
	if kubeClient, baseURL, token, ok := kubeHTTPClient(); ok {
		collector.kubeClient = kubeClient
		collector.kubeAPIBase = baseURL
		collector.kubeToken = token
	}
	return collector
}

func (c *Collector) Collect(ctx context.Context, signal alert.Signal) PromptEvidence {
	var evidence PromptEvidence
	if c == nil {
		evidence.Diagnostics = append(evidence.Diagnostics, "observability collector unavailable")
		return evidence
	}
	if lines := c.collectPrometheus(ctx, signal); len(lines) > 0 {
		evidence.Prometheus = append(evidence.Prometheus, lines...)
	}
	if lines := c.collectLoki(ctx, signal); len(lines) > 0 {
		evidence.Loki = append(evidence.Loki, lines...)
	}
	if lines := c.collectKubernetes(ctx, signal); len(lines) > 0 {
		evidence.Kubernetes = append(evidence.Kubernetes, lines...)
	}
	if len(evidence.Prometheus) == 0 {
		evidence.Diagnostics = append(evidence.Diagnostics, "prometheus evidence unavailable")
	}
	if len(evidence.Loki) == 0 {
		evidence.Diagnostics = append(evidence.Diagnostics, "loki evidence unavailable")
	}
	if len(evidence.Kubernetes) == 0 {
		evidence.Diagnostics = append(evidence.Diagnostics, "kubernetes evidence unavailable")
	}
	return evidence
}

func (c *Collector) collectPrometheus(ctx context.Context, signal alert.Signal) []string {
	baseURL := strings.TrimRight(c.cfg.PrometheusURL, "/")
	if baseURL == "" {
		return nil
	}
	queries := []struct {
		label string
		expr  string
	}{
		{label: "request_rate", expr: fmt.Sprintf(`sum(rate(http_requests_total{namespace=%q,app=%q}[5m]))`, signal.Namespace, signal.AppName)},
		{label: "p95_latency", expr: fmt.Sprintf(`histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{namespace=%q,app=%q}[5m])) by (le))`, signal.Namespace, signal.AppName)},
	}
	var lines []string
	for _, q := range queries {
		values, err := prometheusQuery(ctx, c.httpClient, baseURL, q.expr)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: query failed (%v)", q.label, err))
			continue
		}
		if len(values) == 0 {
			lines = append(lines, fmt.Sprintf("%s: no samples", q.label))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", q.label, strings.Join(values, "; ")))
	}
	return limitLines(lines, 3)
}

func (c *Collector) collectLoki(ctx context.Context, signal alert.Signal) []string {
	baseURL := strings.TrimRight(c.cfg.LokiURL, "/")
	if baseURL == "" {
		return nil
	}
	queries := []string{
		fmt.Sprintf(`{namespace=%q,app=%q} |= "error"`, signal.Namespace, signal.AppName),
		fmt.Sprintf(`{namespace=%q,app=%q}`, signal.Namespace, signal.AppName),
	}
	for _, q := range queries {
		lines, err := lokiQueryRange(ctx, c.httpClient, baseURL, q)
		if err != nil {
			continue
		}
		if len(lines) > 0 {
			return limitLines(lines, 3)
		}
	}
	return nil
}

func (c *Collector) collectKubernetes(ctx context.Context, signal alert.Signal) []string {
	if c.kubeClient == nil || c.kubeAPIBase == "" {
		return nil
	}
	namespace := strings.TrimSpace(signal.Namespace)
	if namespace == "" {
		namespace = strings.TrimSpace(c.cfg.AppNamespace)
	}
	if namespace == "" {
		return nil
	}
	app := strings.TrimSpace(signal.AppName)
	if app == "" {
		app = strings.TrimSpace(c.cfg.AppName)
	}
	var lines []string
	workloadNames := map[string]struct{}{}
	for _, kind := range []string{"deployments", "statefulsets", "daemonsets"} {
		items, err := kubeListWorkloads(ctx, c.kubeClient, c.kubeAPIBase, c.kubeToken, namespace, kind, app)
		if err != nil {
			continue
		}
		for _, item := range items {
			workloadNames[item.Name] = struct{}{}
			lines = append(lines, item.Summary(kind))
		}
	}
	if events, err := kubeListEvents(ctx, c.kubeClient, c.kubeAPIBase, c.kubeToken, namespace); err == nil {
		for _, event := range events {
			if event.matches(app, workloadNames) {
				lines = append(lines, event.Summary())
			}
			if len(lines) >= 6 {
				break
			}
		}
	}
	return limitLines(lines, 6)
}

func limitLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	return append([]string(nil), lines[:max]...)
}

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}

func prometheusQuery(ctx context.Context, client *http.Client, baseURL, expr string) ([]string, error) {
	endpoint := baseURL + "/api/v1/query?query=" + url.QueryEscape(expr)
	var resp promResponse
	if err := getJSON(ctx, client, endpoint, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		if resp.Error != "" {
			return nil, fmt.Errorf("prometheus error: %s", resp.Error)
		}
		return nil, fmt.Errorf("prometheus request failed")
	}
	lines := make([]string, 0, len(resp.Data.Result))
	for _, result := range resp.Data.Result {
		value := ""
		if len(result.Value) >= 2 {
			value = fmt.Sprint(result.Value[1])
		}
		metricName := result.Metric["__name__"]
		if metricName == "" {
			metricName = "sample"
		}
		lines = append(lines, fmt.Sprintf("%s=%s", metricName, value))
	}
	return lines, nil
}

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}

func lokiQueryRange(ctx context.Context, client *http.Client, baseURL, query string) ([]string, error) {
	start := time.Now().Add(-15 * time.Minute).UnixNano()
	end := time.Now().UnixNano()
	endpoint := fmt.Sprintf("%s/loki/api/v1/query_range?query=%s&start=%d&end=%d&limit=5&direction=BACKWARD", baseURL, url.QueryEscape(query), start, end)
	var resp lokiResponse
	if err := getJSON(ctx, client, endpoint, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		if resp.Error != "" {
			return nil, fmt.Errorf("loki error: %s", resp.Error)
		}
		return nil, fmt.Errorf("loki request failed")
	}
	var lines []string
	for _, result := range resp.Data.Result {
		for _, value := range result.Values {
			if len(value) < 2 {
				continue
			}
			stream := formatLabelSet(result.Stream)
			lines = append(lines, fmt.Sprintf("%s %s", stream, value[1]))
		}
	}
	return lines, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type kubeWorkload struct {
	Name   string
	Kind   string
	Ready  string
	Avail  string
	Update string
}

func (w kubeWorkload) Summary(kind string) string {
	parts := []string{fmt.Sprintf("%s/%s", kind[:len(kind)-1], w.Name)}
	if w.Ready != "" {
		parts = append(parts, "ready="+w.Ready)
	}
	if w.Avail != "" {
		parts = append(parts, "available="+w.Avail)
	}
	if w.Update != "" {
		parts = append(parts, "updated="+w.Update)
	}
	return strings.Join(parts, " ")
}

func kubeListWorkloads(ctx context.Context, client *http.Client, baseURL, token, namespace, kind, app string) ([]kubeWorkload, error) {
	type listResponse struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Replicas          int `json:"replicas"`
				ReadyReplicas     int `json:"readyReplicas"`
				AvailableReplicas int `json:"availableReplicas"`
				UpdatedReplicas   int `json:"updatedReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	var resp listResponse
	endpoint := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/%s?labelSelector=%s", baseURL, url.PathEscape(namespace), kind, url.QueryEscape("app="+app))
	if err := kubeGetJSON(ctx, client, token, endpoint, &resp); err != nil {
		return nil, err
	}
	items := make([]kubeWorkload, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, kubeWorkload{
			Name:   item.Metadata.Name,
			Ready:  formatRatio(item.Status.ReadyReplicas, item.Status.Replicas),
			Avail:  formatRatio(item.Status.AvailableReplicas, item.Status.Replicas),
			Update: formatCount(item.Status.UpdatedReplicas),
		})
	}
	return items, nil
}

type kubeEvent struct {
	Message        string `json:"message"`
	Reason         string `json:"reason"`
	Type           string `json:"type"`
	InvolvedObject struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"involvedObject"`
}

func kubeListEvents(ctx context.Context, client *http.Client, baseURL, token, namespace string) ([]kubeEvent, error) {
	type eventList struct {
		Items []kubeEvent `json:"items"`
	}
	var resp eventList
	endpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/events?limit=10", baseURL, url.PathEscape(namespace))
	if err := kubeGetJSON(ctx, client, token, endpoint, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func kubeGetJSON(ctx context.Context, client *http.Client, token, endpoint string, out any) error {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kube http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e kubeEvent) matches(app string, workloads map[string]struct{}) bool {
	if app != "" && strings.Contains(strings.ToLower(e.Message), strings.ToLower(app)) {
		return true
	}
	if _, ok := workloads[e.InvolvedObject.Name]; ok {
		return true
	}
	return false
}

func (e kubeEvent) Summary() string {
	bits := []string{"event"}
	if e.Type != "" {
		bits = append(bits, strings.ToLower(e.Type))
	}
	if e.Reason != "" {
		bits = append(bits, e.Reason)
	}
	if e.Message != "" {
		bits = append(bits, e.Message)
	}
	return strings.Join(bits, ": ")
}

func formatRatio(value, total int) string {
	if total <= 0 {
		return ""
	}
	return strconv.Itoa(value) + "/" + strconv.Itoa(total)
}

func formatCount(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func formatLabelSet(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return "{}"
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func kubeHTTPClient() (*http.Client, string, string, bool) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	if host == "" || port == "" {
		return nil, "", "", false
	}
	tokenPath := filepath.Join("/var/run/secrets/kubernetes.io/serviceaccount", "token")
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, "", "", false
	}
	caPath := filepath.Join("/var/run/secrets/kubernetes.io/serviceaccount", "ca.crt")
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, "", "", false
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caBytes); !ok {
		return nil, "", "", false
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: transport}, "https://" + host + ":" + port, string(tokenBytes), true
}
