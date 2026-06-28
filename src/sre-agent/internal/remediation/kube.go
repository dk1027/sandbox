package remediation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sre-agent/internal/alert"
	"sre-agent/internal/llm"
	"sre-agent/internal/observability"
)

const restartAnnotationKey = "sre-agent.nousresearch.com/restarted-at"

type Client struct {
	baseURL    string
	namespace  string
	token      string
	httpClient *http.Client
}

func New(baseURL, namespace, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		namespace:  strings.TrimSpace(namespace),
		token:      strings.TrimSpace(token),
		httpClient: httpClient,
	}
}

func NewFromEnvironment(namespace string) (*Client, error) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	if host == "" || port == "" {
		return nil, fmt.Errorf("kubernetes service host and port are required")
	}

	tokenPath := filepath.Join("/var/run/secrets/kubernetes.io/serviceaccount", "token")
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}

	caPath := filepath.Join("/var/run/secrets/kubernetes.io/serviceaccount", "ca.crt")
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read service account ca: %w", err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caBytes); !ok {
		return nil, fmt.Errorf("parse service account ca certificate")
	}

	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	return New("https://"+host+":"+port, namespace, string(tokenBytes), client), nil
}

func (c *Client) Execute(ctx context.Context, signal alert.Signal, decision llm.Decision) error {
	namespace := strings.TrimSpace(signal.Namespace)
	if namespace == "" {
		namespace = c.namespace
	}
	for _, action := range decision.Actions {
		if err := c.executeAction(ctx, namespace, action); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) executeAction(ctx context.Context, namespace string, action llm.Action) error {
	switch action.Type {
	case "restart", "rollout_restart":
		err := c.restartWorkload(ctx, namespace, action.Target)
		if err != nil {
			observability.ObserveRemediation("error", action.Type)
			return err
		}
		observability.ObserveRemediation("success", action.Type)
		return nil
	default:
		observability.ObserveRemediation("unsupported", action.Type)
		return fmt.Errorf("unsupported remediation action %q", action.Type)
	}
}

func (c *Client) restartWorkload(ctx context.Context, namespace, target string) error {
	resourceKind, resourceName, err := parseTarget(target)
	if err != nil {
		return err
	}

	path, err := appsResourcePath(namespace, resourceKind, resourceName)
	if err != nil {
		return err
	}

	patchBody, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						restartAnnotationKey: time.Now().UTC().Format(time.RFC3339Nano),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal restart patch: %w", err)
	}

	return c.doJSON(ctx, http.MethodPatch, path, "application/merge-patch+json", patchBody)
}

func parseTarget(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("remediation target is required")
	}

	kind := "deployment"
	name := target
	if strings.Contains(target, "/") {
		parts := strings.SplitN(target, "/", 2)
		kind = parts[0]
		name = parts[1]
	}

	kind = normalizeKind(kind)
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return "", "", fmt.Errorf("invalid remediation target %q", target)
	}
	return kind, name, nil
}

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment", "deployments":
		return "deployment"
	case "statefulset", "statefulsets":
		return "statefulset"
	case "daemonset", "daemonsets":
		return "daemonset"
	default:
		return ""
	}
}

func appsResourcePath(namespace, kind, name string) (string, error) {
	if namespace == "" {
		return "", fmt.Errorf("namespace is required for remediation")
	}
	switch kind {
	case "deployment":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name), nil
	case "statefulset":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", namespace, name), nil
	case "daemonset":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/daemonsets/%s", namespace, name), nil
	default:
		return "", fmt.Errorf("unsupported workload kind %q", kind)
	}
}

func (c *Client) doJSON(ctx context.Context, method, path, contentType string, body []byte) error {
	if c.baseURL == "" {
		return fmt.Errorf("kubernetes api base url is required")
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build kubernetes request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call kubernetes api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kubernetes api request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}
