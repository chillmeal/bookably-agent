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
)

func TestOpenAICompleteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"created":1710000000,
			"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"{\"intent\":\"create_booking\"}"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
		}`)
	}))
	defer server.Close()

	client, err := NewOpenAIClient("test-key", ClientOptions{
		Model:      "gpt-4o",
		Timeout:    time.Second,
		BaseURL:    server.URL + "/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient() unexpected error: %v", err)
	}

	got, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if got.Content != `{"intent":"create_booking"}` {
		t.Fatalf("content mismatch: %q", got.Content)
	}
	if got.InputTokens != 11 || got.OutputTokens != 7 {
		t.Fatalf("token mismatch: %+v", got)
	}
}

func TestOpenAICompleteRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer server.Close()

	client, err := NewOpenAIClient("test-key", ClientOptions{
		Model:      "gpt-4o",
		Timeout:    time.Second,
		BaseURL:    server.URL + "/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient() unexpected error: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "rate"}})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompleteUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom","type":"server_error"}}`)
	}))
	defer server.Close()

	client, err := NewOpenAIClient("test-key", ClientOptions{
		Model:      "gpt-4o",
		Timeout:    time.Second,
		BaseURL:    server.URL + "/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient() unexpected error: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "fail"}})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !strings.Contains(err.Error(), "upstream failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompleteContextCanceledOrDeadline(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	client, err := NewOpenAIClient("test-key", ClientOptions{
		Model:      "gpt-4o",
		Timeout:    20 * time.Millisecond,
		BaseURL:    "http://openai.test/v1",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient() unexpected error: %v", err)
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
