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

type StreamProgress struct {
	ChunkCount int
	Bytes      int64
	StartedAt  time.Time
	UpdatedAt  time.Time
}

type StreamingClient interface {
	CompleteStream(ctx context.Context, messages []Message, onProgress func(StreamProgress)) (*Completion, error)
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

func newDefaultLLMHTTPClient(timeout time.Duration) *http.Client {
	t := normalizeTimeout(timeout)

	baseTransport := http.DefaultTransport
	cloned, ok := baseTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}

	transport := cloned.Clone()
	tlsHandshakeTimeout := t
	if tlsHandshakeTimeout < 20*time.Second {
		tlsHandshakeTimeout = 20 * time.Second
	}
	if tlsHandshakeTimeout > 45*time.Second {
		tlsHandshakeTimeout = 45 * time.Second
	}

	transport.TLSHandshakeTimeout = tlsHandshakeTimeout
	transport.ResponseHeaderTimeout = t
	return &http.Client{Transport: transport}
}
