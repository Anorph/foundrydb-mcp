package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an HTTP client for the FoundryDB REST API.
// It is stateless and safe for concurrent use.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// New creates a new API client with Basic Auth credentials.
func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// do performs an HTTP request with Basic Auth and returns the decoded JSON body.
func (c *Client) do(ctx context.Context, method, path string, body interface{}) (map[string]interface{}, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if len(respBody) == 0 {
		return nil, resp.StatusCode, nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Response may not be JSON (e.g. 204 No Content style responses)
		return map[string]interface{}{"raw": string(respBody)}, resp.StatusCode, nil
	}
	return result, resp.StatusCode, nil
}

// doList performs a GET and expects a JSON array at the given key, or returns the raw array.
func (c *Client) doList(ctx context.Context, path, arrayKey string) ([]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if arrayKey != "" {
		if arr, ok := result[arrayKey].([]interface{}); ok {
			return arr, nil
		}
	}
	// Fall back: if the response is itself a list wrapped in an object, return empty
	return nil, nil
}

// --- Services ---

func (c *Client) ListServices(ctx context.Context) ([]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, "/managed-services/", nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if services, ok := result["services"].([]interface{}); ok {
		return services, nil
	}
	return nil, nil
}

func (c *Client) GetService(ctx context.Context, id string) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, "/managed-services/"+id, nil)
	return result, err
}

// GetServiceByName finds a service by its name by listing all services.
func (c *Client) GetServiceByName(ctx context.Context, name string) (map[string]interface{}, error) {
	services, err := c.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range services {
		svc, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if svc["name"] == name {
			return svc, nil
		}
	}
	return nil, fmt.Errorf("service with name %q not found", name)
}

func (c *Client) CreateService(ctx context.Context, req CreateServiceRequest) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodPost, "/managed-services/", req)
	return result, err
}

func (c *Client) DeleteService(ctx context.Context, id string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/managed-services/"+id, nil)
	return err
}

// --- Users ---

func (c *Client) ListUsers(ctx context.Context, serviceID string) ([]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/database-users", nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if users, ok := result["users"].([]interface{}); ok {
		return users, nil
	}
	return nil, nil
}

func (c *Client) RevealPassword(ctx context.Context, serviceID, username string) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodPost,
		"/managed-services/"+serviceID+"/database-users/"+username+"/reveal-password",
		map[string]interface{}{},
	)
	return result, err
}

// --- Backups ---

func (c *Client) ListBackups(ctx context.Context, serviceID string) ([]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/backups", nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if backups, ok := result["backups"].([]interface{}); ok {
		return backups, nil
	}
	return nil, nil
}

func (c *Client) TriggerBackup(ctx context.Context, serviceID string, req CreateBackupRequest) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/backups", req)
	return result, err
}

// --- Metrics ---

func (c *Client) GetMetrics(ctx context.Context, serviceID string) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/metrics/current", nil)
	return result, err
}

// --- Logs (two-phase async) ---

// RequestLogs enqueues a log fetch task and returns the task ID.
func (c *Client) RequestLogs(ctx context.Context, serviceID string, lines int) (string, error) {
	result, _, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/managed-services/%s/logs?lines=%d", serviceID, lines),
		map[string]interface{}{},
	)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("empty response from log request")
	}
	taskID, ok := result["task_id"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: %v", result)
	}
	return taskID, nil
}

// GetLogsResult polls for the result of a log fetch task.
func (c *Client) GetLogsResult(ctx context.Context, serviceID, taskID string) (map[string]interface{}, error) {
	result, _, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/managed-services/%s/logs?task_id=%s", serviceID, taskID),
		nil,
	)
	return result, err
}
