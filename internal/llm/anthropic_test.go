package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAnthropicCompleteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-3-5-haiku-latest",
			"content":[{"type":"text","text":"{\"intent\":\"list_bookings\"}"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{"input_tokens":12,"output_tokens":34}
		}`)
	}))
	defer server.Close()

	client, err := NewAnthropicClient("test-key", ClientOptions{
		Model:      string(anthropic.ModelClaude3_5HaikuLatest),
		Timeout:    time.Second,
		BaseURL:    server.URL + "/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAnthropicClient() unexpected error: %v", err)
	}

	got, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if got.Content != `{"intent":"list_bookings"}` {
		t.Fatalf("content mismatch: %q", got.Content)
	}
	if got.InputTokens != 12 || got.OutputTokens != 34 {
		t.Fatalf("token mismatch: %+v", got)
	}
}

func TestAnthropicCompleteRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	defer server.Close()

	client, err := NewAnthropicClient("test-key", ClientOptions{
		Model:      string(anthropic.ModelClaude3_5HaikuLatest),
		Timeout:    time.Second,
		BaseURL:    server.URL + "/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAnthropicClient() unexpected error: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "rate"}})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnthropicCompleteUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
	}))
	defer server.Close()

	client, err := NewAnthropicClient("test-key", ClientOptions{
		Model:      string(anthropic.ModelClaude3_5HaikuLatest),
		Timeout:    time.Second,
		BaseURL:    server.URL + "/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAnthropicClient() unexpected error: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "fail"}})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !strings.Contains(err.Error(), "upstream failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnthropicCompleteContextCanceledOrDeadline(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	client, err := NewAnthropicClient("test-key", ClientOptions{
		Model:      string(anthropic.ModelClaude3_5HaikuLatest),
		Timeout:    20 * time.Millisecond,
		BaseURL:    "http://anthropic.test/",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewAnthropicClient() unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Complete(ctx, []Message{{Role: "user", Content: "cancel"}})
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "timeout"}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got: %v", err)
	}
}
