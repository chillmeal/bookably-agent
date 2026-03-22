package llm

import (
	"context"
	"net/http"
	"time"
)

const defaultLLMTimeout = 15 * time.Second

type Message struct {
	Role    string
	Content string
}

type Completion struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

type LLMClient interface {
	Complete(ctx context.Context, messages []Message) (*Completion, error)
}

type ClientOptions struct {
	Model      string
	Timeout    time.Duration
	BaseURL    string
	HTTPClient *http.Client
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultLLMTimeout
	}
	return timeout
}
