package alert

import (
    "encoding/json"
    "fmt"
    "strings"
    "time"
)

type Webhook struct {
    Status string        `json:"status"`
    Alerts []WebhookItem `json:"alerts"`
}

type WebhookItem struct {
    Status      string            `json:"status"`
    Labels      map[string]string  `json:"labels"`
    Annotations map[string]string  `json:"annotations"`
    StartsAt    string            `json:"startsAt"`
}

type Signal struct {
    AppName     string            `json:"app_name"`
    Namespace   string            `json:"namespace"`
    AlertName   string            `json:"alert_name"`
    Severity    string            `json:"severity"`
    Status      string            `json:"status,omitempty"`
    Summary     string            `json:"summary"`
    Fingerprint string            `json:"fingerprint,omitempty"`
    Labels      map[string]string `json:"labels"`
    Annotations map[string]string `json:"annotations"`
    StartsAt    time.Time         `json:"starts_at"`
}

func ParseWebhook(payload []byte) ([]Signal, error) {
    var webhook Webhook
    if err := json.Unmarshal(payload, &webhook); err != nil {
        return nil, fmt.Errorf("decode alert webhook: %w", err)
    }
    signals := make([]Signal, 0, len(webhook.Alerts))
    for _, item := range webhook.Alerts {
        sig := Signal{
            AppName:     firstNonEmpty(item.Labels["app"], item.Labels["app_name"]),
            Namespace:   firstNonEmpty(item.Labels["namespace"], item.Labels["kubernetes_namespace"]),
            AlertName:   firstNonEmpty(item.Labels["alertname"], item.Labels["alert_name"]),
            Severity:    item.Labels["severity"],
            Status:      item.Status,
            Summary:     firstNonEmpty(item.Annotations["summary"], item.Annotations["description"], item.Labels["summary"]),
            Fingerprint: item.Labels["fingerprint"],
            Labels:      item.Labels,
            Annotations: item.Annotations,
        }
        if item.StartsAt != "" {
            ts, err := time.Parse(time.RFC3339, item.StartsAt)
            if err != nil {
                return nil, fmt.Errorf("parse startsAt: %w", err)
            }
            sig.StartsAt = ts
        }
        signals = append(signals, sig)
    }
    return signals, nil
}

func (s Signal) Matches(app, namespace string) bool {
    if app != "" && !strings.EqualFold(s.AppName, app) {
        return false
    }
    if namespace != "" && !strings.EqualFold(s.Namespace, namespace) {
        return false
    }
    return true
}

func firstNonEmpty(values ...string) string {
    for _, v := range values {
        if v != "" {
            return v
        }
    }
    return ""
}
