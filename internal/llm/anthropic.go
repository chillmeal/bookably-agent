package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/chillmeal/bookably-agent/config"
)

const (
	defaultAnthropicModel     = anthropic.ModelClaude3_5SonnetLatest
	defaultAnthropicMaxTokens = int64(1024)
)

type AnthropicClient struct {
	client    *anthropic.Client
	model     anthropic.Model
	timeout   time.Duration
	maxTokens int64
}

func NewAnthropicClientFromConfig(cfg *config.Config) (*AnthropicClient, error) {
	if cfg == nil {
		return nil, errors.New("anthropic: config is nil")
	}
	return NewAnthropicClient(cfg.LLMAPIKey, ClientOptions{
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
}

func NewAnthropicClient(apiKey string, opts ClientOptions) (*AnthropicClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("anthropic: api key is required")
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = string(defaultAnthropicModel)
	}

	requestOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	}
	if strings.TrimSpace(opts.BaseURL) != "" {
		requestOpts = append(requestOpts, option.WithBaseURL(strings.TrimSpace(opts.BaseURL)))
	}
	if opts.HTTPClient != nil {
		requestOpts = append(requestOpts, option.WithHTTPClient(opts.HTTPClient))
	}

	return &AnthropicClient{
		client:    anthropic.NewClient(requestOpts...),
		model:     anthropic.Model(model),
		timeout:   normalizeTimeout(opts.Timeout),
		maxTokens: defaultAnthropicMaxTokens,
	}, nil
}

func (c *AnthropicClient) Complete(ctx context.Context, messages []Message) (*Completion, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("anthropic: client is nil")
	}

	params, err := c.buildParams(messages)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Messages.New(callCtx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == http.StatusTooManyRequests:
				return nil, fmt.Errorf("anthropic: rate limited: %w", err)
			case apiErr.StatusCode >= http.StatusInternalServerError:
				return nil, fmt.Errorf("anthropic: upstream failure: %w", err)
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("anthropic: timeout: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("anthropic: canceled: %w", err)
		}
		return nil, fmt.Errorf("anthropic: complete: %w", err)
	}

	return &Completion{
		Content:      extractAnthropicText(resp.Content),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}, nil
}

func (c *AnthropicClient) buildParams(messages []Message) (anthropic.MessageNewParams, error) {
	if len(messages) == 0 {
		return anthropic.MessageNewParams{}, errors.New("anthropic: at least one message is required")
	}

	conversation := make([]anthropic.MessageParam, 0, len(messages))
	system := make([]anthropic.TextBlockParam, 0)

	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "system":
			system = append(system, anthropic.NewTextBlock(content))
		case "assistant":
			conversation = append(conversation, anthropic.NewAssistantMessage(anthropic.NewTextBlock(content)))
		default:
			conversation = append(conversation, anthropic.NewUserMessage(anthropic.NewTextBlock(content)))
		}
	}

	if len(conversation) == 0 {
		return anthropic.MessageNewParams{}, errors.New("anthropic: conversation is empty")
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.F(c.model),
		MaxTokens: anthropic.F(c.maxTokens),
		Messages:  anthropic.F(conversation),
	}
	if len(system) > 0 {
		params.System = anthropic.F(system)
	}

	return params, nil
}

func extractAnthropicText(blocks []anthropic.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
