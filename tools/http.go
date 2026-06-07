package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
)

// itoa formats an int as a decimal string for building query parameters.
func itoa(n int) string { return strconv.Itoa(n) }

// This file holds the direct-HTTP helpers used by tools that wrap API
// endpoints not yet covered by the SDK. All helpers authenticate with the
// same credentials as the SDK client and hit the same audited routes.

// apiGet performs an authenticated GET request to the FoundryDB API.
func apiGet(ctx context.Context, cfg foundrydb.Config, path string) (map[string]interface{}, error) {
	return apiRequest(ctx, cfg, http.MethodGet, path, nil)
}

// apiPost performs an authenticated POST request to the FoundryDB API.
func apiPost(ctx context.Context, cfg foundrydb.Config, path string, body interface{}) (map[string]interface{}, error) {
	return apiRequest(ctx, cfg, http.MethodPost, path, body)
}

// apiPatch performs an authenticated PATCH request to the FoundryDB API.
func apiPatch(ctx context.Context, cfg foundrydb.Config, path string, body interface{}) (map[string]interface{}, error) {
	return apiRequest(ctx, cfg, http.MethodPatch, path, body)
}

// apiPut performs an authenticated PUT request to the FoundryDB API.
func apiPut(ctx context.Context, cfg foundrydb.Config, path string, body interface{}) (map[string]interface{}, error) {
	return apiRequest(ctx, cfg, http.MethodPut, path, body)
}

// apiDelete performs an authenticated DELETE request to the FoundryDB API.
func apiDelete(ctx context.Context, cfg foundrydb.Config, path string) (map[string]interface{}, error) {
	return apiRequest(ctx, cfg, http.MethodDelete, path, nil)
}

// splitAndTrim splits a comma-separated string into a slice with each element
// trimmed of surrounding whitespace, dropping empties. Used for list-valued
// tool parameters passed as a single comma-separated string.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func apiRequest(ctx context.Context, cfg foundrydb.Config, method, path string, body interface{}) (map[string]interface{}, error) {
	apiURL := strings.TrimRight(cfg.APIURL, "/")
	if apiURL == "" {
		apiURL = "https://api.foundrydb.com"
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	} else {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if cfg.OrgID != "" {
		req.Header.Set("X-Active-Org-ID", cfg.OrgID)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respData))
	}

	if len(respData) == 0 {
		return nil, nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respData, &result); err != nil {
		return map[string]interface{}{"raw": string(respData)}, nil
	}
	return result, nil
}
