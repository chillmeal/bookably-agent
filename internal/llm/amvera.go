package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/config"
)

const (
	defaultAmveraBaseURL = "https://kong-proxy.yc.amvera.ru/api/v1"
	defaultAmveraModel   = "gpt-5"
)

type AmveraClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
}

func NewAmveraClientFromConfig(cfg *config.Config) (*AmveraClient, error) {
	if cfg == nil {
		return nil, errors.New("amvera: config is nil")
	}
	return NewAmveraClient(cfg.LLMAPIKey, ClientOptions{
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
}

func NewAmveraClient(apiKey string, opts ClientOptions) (*AmveraClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("amvera: api key is required")
	}

	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = defaultAmveraBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultAmveraModel
	}
	if model != defaultAmveraModel {
		return nil, fmt.Errorf("amvera: strict model policy requires %q, got %q", defaultAmveraModel, model)
	}

	timeout := normalizeTimeout(opts.Timeout)

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newDefaultLLMHTTPClient(timeout)
	}

	return &AmveraClient{
		httpClient: httpClient,
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(apiKey),
		model:      model,
		timeout:    timeout,
	}, nil
}

func (c *AmveraClient) Complete(ctx context.Context, messages []Message) (*Completion, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("amvera: client is nil")
	}
	if len(messages) == 0 {
		return nil, errors.New("amvera: at least one message is required")
	}

	payloadMessages := make([]amveraMessage, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system", "user", "assistant":
		default:
			role = "user"
		}
		payloadMessages = append(payloadMessages, amveraMessage{
			Role: role,
			Text: content,
		})
	}
	if len(payloadMessages) == 0 {
		return nil, errors.New("amvera: conversation is empty")
	}

	body, err := json.Marshal(amveraRequest{
		Model:    c.model,
		Messages: payloadMessages,
	})
	if err != nil {
		return nil, fmt.Errorf("amvera: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	doRequest := func() (int, []byte, error) {
		req, reqErr := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/models/gpt", bytes.NewReader(body))
		if reqErr != nil {
			return 0, nil, fmt.Errorf("amvera: build request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Token", "Bearer "+c.apiKey)

		resp, reqErr := c.httpClient.Do(req)
		if reqErr != nil {
			return 0, nil, reqErr
		}
		defer resp.Body.Close()

		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return 0, nil, fmt.Errorf("amvera: read response: %w", readErr)
		}
		return resp.StatusCode, raw, nil
	}

	var (
		status int
		raw    []byte
		reqErr error
	)
	for attempt := 0; attempt < 2; attempt++ {
		status, raw, reqErr = doRequest()
		if reqErr == nil {
			break
		}
		if attempt == 0 && isRetryableTransportError(reqErr) {
			if waitErr := waitRetry(callCtx, 450*time.Millisecond); waitErr != nil {
				return nil, fmt.Errorf("amvera: timeout: %w", waitErr)
			}
			continue
		}
		if errors.Is(reqErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("amvera: timeout: %w", reqErr)
		}
		if errors.Is(reqErr, context.Canceled) {
			return nil, fmt.Errorf("amvera: canceled: %w", reqErr)
		}
		return nil, fmt.Errorf("amvera: request failed: %w", reqErr)
	}
	if reqErr != nil {
		return nil, fmt.Errorf("amvera: request failed: %w", reqErr)
	}

	if status != http.StatusOK {
		return nil, c.mapNonOK(status, raw)
	}

	var parsed amveraResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("amvera: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("amvera: empty choices in response")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Text)
	if content == "" {
		content = strings.TrimSpace(parsed.Choices[0].Message.Content)
	}
	if content == "" {
		return nil, errors.New("amvera: empty message content")
	}

	return &Completion{
		Content:      content,
		InputTokens:  firstNonZeroInt(extractInt(parsed.Usage.PromptTokens), extractInt(parsed.Usage.InputTextTokens)),
		OutputTokens: firstNonZeroInt(extractInt(parsed.Usage.CompletionTokens), extractInt(parsed.Usage.CompletionTokensAlt)),
	}, nil
}

func isRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "tls handshake timeout") ||
		strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "i/o timeout")
}

func waitRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *AmveraClient) mapNonOK(status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))

	var envelope struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		switch {
		case strings.TrimSpace(envelope.Message) != "":
			msg = strings.TrimSpace(envelope.Message)
		case strings.TrimSpace(envelope.Error.Message) != "":
			msg = strings.TrimSpace(envelope.Error.Message)
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}

	switch {
	case status == http.StatusPaymentRequired:
		return fmt.Errorf("amvera: billing blocked: %s", msg)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("amvera: rate limited: %s", msg)
	case status >= http.StatusInternalServerError:
		return fmt.Errorf("amvera: upstream failure: status=%d message=%s", status, msg)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("amvera: unauthorized: %s", msg)
	case status == http.StatusForbidden:
		return fmt.Errorf("amvera: forbidden: %s", msg)
	default:
		return fmt.Errorf("amvera: request failed with status %d: %s", status, msg)
	}
}

func extractInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return i
	default:
		return 0
	}
}

func firstNonZeroInt(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

type amveraRequest struct {
	Model    string          `json:"model"`
	Messages []amveraMessage `json:"messages"`
}

type amveraMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type amveraResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Text    string `json:"text"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        any `json:"prompt_tokens"`
		InputTextTokens     any `json:"inputTextTokens"`
		CompletionTokens    any `json:"completion_tokens"`
		CompletionTokensAlt any `json:"completionTokens"`
	} `json:"usage"`
}
