package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultHTTPTimeout = 10 * time.Second

// RunFailedError is returned when ACP reports failed run state.
type RunFailedError struct {
	Result *ACPRunResult
}

func (e *RunFailedError) Error() string {
	if e == nil || e.Result == nil {
		return "acp: run failed"
	}
	msg := ""
	if e.Result.Error != nil {
		msg = strings.TrimSpace(e.Result.Error.Message)
	}
	if msg == "" {
		msg = "run failed"
	}
	return fmt.Sprintf("acp: run %s failed: %s", e.Result.RunID, msg)
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("acp client: base URL is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("acp client: api key is required")
	}
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: httpClient}, nil
}

func (c *Client) SubmitRun(ctx context.Context, run ACPRun) (string, error) {
	if len(run.Steps) == 0 {
		return "", errors.New("acp client: run must contain at least one step")
	}

	payload, err := json.Marshal(run)
	if err != nil {
		return "", fmt.Errorf("acp client: marshal run: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/runs", payload)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("acp client: submit run request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("acp client: read submit response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("acp client: submit run status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	runID, err := parseSubmitRunID(body)
	if err != nil {
		return "", err
	}
	if runID == "" {
		return "", errors.New("acp client: submit response missing run id")
	}
	return runID, nil
}

func (c *Client) GetRun(ctx context.Context, runID string) (*ACPRunResult, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("acp client: run id is required")
	}

	path := "/runs/" + url.PathEscape(runID)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("acp client: get run request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("acp client: read get run response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("acp client: get run status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	result, err := parseRunResult(body)
	if err != nil {
		return nil, err
	}
	if result.RunID == "" {
		result.RunID = runID
	}

	if result.Status == ACPStatusFailed {
		return result, &RunFailedError{Result: result}
	}

	return result, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	urlValue := c.baseURL + path
	var reader io.Reader
	if len(body) > 0 {
		reader = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, urlValue, reader)
	if err != nil {
		return nil, fmt.Errorf("acp client: build request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func parseSubmitRunID(payload []byte) (string, error) {
	var flat struct {
		RunID string `json:"run_id"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(payload, &flat); err == nil {
		if strings.TrimSpace(flat.RunID) != "" {
			return strings.TrimSpace(flat.RunID), nil
		}
		if strings.TrimSpace(flat.ID) != "" {
			return strings.TrimSpace(flat.ID), nil
		}
	}

	var nested struct {
		Run struct {
			RunID string `json:"run_id"`
			ID    string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal(payload, &nested); err == nil {
		if strings.TrimSpace(nested.Run.RunID) != "" {
			return strings.TrimSpace(nested.Run.RunID), nil
		}
		if strings.TrimSpace(nested.Run.ID) != "" {
			return strings.TrimSpace(nested.Run.ID), nil
		}
	}

	return "", fmt.Errorf("acp client: cannot parse submit run id from payload: %s", strings.TrimSpace(string(payload)))
}

func parseRunResult(payload []byte) (*ACPRunResult, error) {
	result := &ACPRunResult{}
	if err := json.Unmarshal(payload, result); err == nil && result.Status != "" {
		return result, nil
	}

	var nested struct {
		Run ACPRunResult `json:"run"`
	}
	if err := json.Unmarshal(payload, &nested); err == nil && nested.Run.Status != "" {
		return &nested.Run, nil
	}

	return nil, fmt.Errorf("acp client: cannot parse run result payload: %s", strings.TrimSpace(string(payload)))
}
