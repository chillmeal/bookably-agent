package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/config"
	openai "github.com/sashabaranov/go-openai"
)

const defaultOpenAIModel = openai.GPT4o

type OpenAIClient struct {
	client  *openai.Client
	model   string
	timeout time.Duration
}

func NewOpenAIClientFromConfig(cfg *config.Config) (*OpenAIClient, error) {
	if cfg == nil {
		return nil, errors.New("openai: config is nil")
	}
	return NewOpenAIClient(cfg.LLMAPIKey, ClientOptions{
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
}

func NewOpenAIClient(apiKey string, opts ClientOptions) (*OpenAIClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai: api key is required")
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultOpenAIModel
	}

	cfg := openai.DefaultConfig(apiKey)
	if strings.TrimSpace(opts.BaseURL) != "" {
		cfg.BaseURL = strings.TrimSpace(opts.BaseURL)
	}
	if opts.HTTPClient != nil {
		cfg.HTTPClient = opts.HTTPClient
	}

	return &OpenAIClient{
		client:  openai.NewClientWithConfig(cfg),
		model:   model,
		timeout: normalizeTimeout(opts.Timeout),
	}, nil
}

func (c *OpenAIClient) Complete(ctx context.Context, messages []Message) (*Completion, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("openai: client is nil")
	}
	if len(messages) == 0 {
		return nil, errors.New("openai: at least one message is required")
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	requestMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleAssistant, openai.ChatMessageRoleUser:
		default:
			role = openai.ChatMessageRoleUser
		}

		requestMessages = append(requestMessages, openai.ChatCompletionMessage{
			Role:    role,
			Content: content,
		})
	}
	if len(requestMessages) == 0 {
		return nil, errors.New("openai: conversation is empty")
	}

	resp, err := c.client.CreateChatCompletion(callCtx, openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: requestMessages,
	})
	if err != nil {
		var apiErr *openai.APIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.HTTPStatusCode == http.StatusTooManyRequests:
				return nil, fmt.Errorf("openai: rate limited: %w", err)
			case apiErr.HTTPStatusCode >= http.StatusInternalServerError:
				return nil, fmt.Errorf("openai: upstream failure: %w", err)
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("openai: timeout: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("openai: canceled: %w", err)
		}
		return nil, fmt.Errorf("openai: complete: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("openai: empty completion choices")
	}

	return &Completion{
		Content:      strings.TrimSpace(resp.Choices[0].Message.Content),
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}, nil
}
