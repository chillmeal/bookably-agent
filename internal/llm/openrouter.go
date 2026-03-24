package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/config"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultOpenRouterModel   = "openai/gpt-5.4-mini"
)

type OpenRouterClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
}

func NewOpenRouterClientFromConfig(cfg *config.Config) (*OpenRouterClient, error) {
	if cfg == nil {
		return nil, errors.New("openrouter: config is nil")
	}
	return NewOpenRouterClient(cfg.LLMAPIKey, ClientOptions{
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
}

func NewOpenRouterClient(apiKey string, opts ClientOptions) (*OpenRouterClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openrouter: api key is required")
	}

	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = defaultOpenRouterBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultOpenRouterModel
	}
	if model != defaultOpenRouterModel {
		return nil, fmt.Errorf("openrouter: strict model policy requires %q, got %q", defaultOpenRouterModel, model)
	}

	timeout := normalizeTimeout(opts.Timeout)
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newDefaultLLMHTTPClient(timeout)
	}

	return &OpenRouterClient{
		httpClient: httpClient,
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(apiKey),
		model:      model,
		timeout:    timeout,
	}, nil
}

func (c *OpenRouterClient) Complete(ctx context.Context, messages []Message) (*Completion, error) {
	return c.CompleteStream(ctx, messages, nil)
}

func (c *OpenRouterClient) CompleteStream(ctx context.Context, messages []Message, onProgress func(StreamProgress)) (*Completion, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("openrouter: client is nil")
	}
	if len(messages) == 0 {
		return nil, errors.New("openrouter: at least one message is required")
	}

	payloadMessages := make([]openRouterMessage, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system", "assistant", "user":
		default:
			role = "user"
		}
		payloadMessages = append(payloadMessages, openRouterMessage{
			Role:    role,
			Content: content,
		})
	}
	if len(payloadMessages) == 0 {
		return nil, errors.New("openrouter: conversation is empty")
	}

	body, err := json.Marshal(openRouterRequest{
		Model:    c.model,
		Messages: payloadMessages,
		Stream:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openrouter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("openrouter: timeout: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("openrouter: canceled: %w", err)
		}
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, mapOpenRouterNonOK(resp.StatusCode, raw)
	}

	return parseOpenRouterSSE(resp.Body, onProgress)
}

func parseOpenRouterSSE(body io.Reader, onProgress func(StreamProgress)) (*Completion, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		builder      strings.Builder
		usageIn      int
		usageOut     int
		chunkCount   int
		streamBytes  int64
		streamStart  = time.Now()
		lastProgress = streamStart
		seenDone     bool
	)

	flushEvent := func(eventData []string) error {
		if len(eventData) == 0 {
			return nil
		}
		payload := strings.Join(eventData, "\n")
		if strings.TrimSpace(payload) == "" {
			return nil
		}
		if strings.TrimSpace(payload) == "[DONE]" {
			seenDone = true
			return nil
		}

		var chunk openRouterStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("openrouter: decode stream chunk: %w", err)
		}
		if chunk.Error != nil {
			msg := strings.TrimSpace(chunk.Error.Message)
			if msg == "" {
				msg = "stream error"
			}
			return fmt.Errorf("openrouter: stream error: %s", msg)
		}
		if chunk.Usage != nil {
			usageIn = firstNonZeroInt(usageIn, chunk.Usage.PromptTokens)
			usageOut = firstNonZeroInt(usageOut, chunk.Usage.CompletionTokens)
		}

		for _, choice := range chunk.Choices {
			piece := choice.Delta.Content
			if piece == "" {
				piece = choice.Message.Content
			}
			if piece == "" {
				continue
			}
			builder.WriteString(piece)
			chunkCount++
			streamBytes += int64(len(piece))
			lastProgress = time.Now()
			if onProgress != nil {
				onProgress(StreamProgress{
					ChunkCount: chunkCount,
					Bytes:      streamBytes,
					StartedAt:  streamStart,
					UpdatedAt:  lastProgress,
				})
			}
		}
		return nil
	}

	eventData := make([]string, 0, 4)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flushEvent(eventData); err != nil {
				return nil, err
			}
			eventData = eventData[:0]
			if seenDone {
				break
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimPrefix(line, "data:")
		if strings.HasPrefix(payload, " ") {
			payload = payload[1:]
		}
		eventData = append(eventData, payload)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("openrouter: stream read: %w", err)
	}
	if !seenDone {
		if err := flushEvent(eventData); err != nil {
			return nil, err
		}
	}
	if !seenDone {
		return nil, errors.New("openrouter: stream ended without [DONE]")
	}

	content := strings.TrimSpace(builder.String())
	if content == "" {
		return nil, errors.New("openrouter: empty streamed content")
	}

	return &Completion{
		Content:      content,
		InputTokens:  usageIn,
		OutputTokens: usageOut,
	}, nil
}

func mapOpenRouterNonOK(status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if strings.TrimSpace(envelope.Error.Message) != "" {
			msg = strings.TrimSpace(envelope.Error.Message)
		} else if strings.TrimSpace(envelope.Message) != "" {
			msg = strings.TrimSpace(envelope.Message)
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}

	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("openrouter: rate limited: %s", msg)
	case status >= http.StatusInternalServerError:
		return fmt.Errorf("openrouter: upstream failure: status=%d message=%s", status, msg)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("openrouter: unauthorized: %s", msg)
	case status == http.StatusForbidden:
		return fmt.Errorf("openrouter: forbidden: %s", msg)
	default:
		return fmt.Errorf("openrouter: request failed with status %d: %s", status, msg)
	}
}

type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}
