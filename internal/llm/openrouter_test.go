package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenRouterStreamSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keepalive\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"intent\\\":\\\"\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"list_bookings\\\"}\"}}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		BaseURL:    server.URL + "/api/v1",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}

	var events int32
	out, err := client.CompleteStream(context.Background(), []Message{{Role: "user", Content: "привет"}}, func(progress StreamProgress) {
		if progress.ChunkCount > 0 {
			atomic.AddInt32(&events, 1)
		}
	})
	if err != nil {
		t.Fatalf("CompleteStream() unexpected error: %v", err)
	}
	if strings.TrimSpace(out.Content) != `{"intent":"list_bookings"}` {
		t.Fatalf("content mismatch: %q", out.Content)
	}
	if out.InputTokens != 11 || out.OutputTokens != 7 {
		t.Fatalf("token mismatch: %+v", out)
	}
	if atomic.LoadInt32(&events) == 0 {
		t.Fatal("expected progress events")
	}
}

func TestOpenRouterStreamErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":{"message":"bad key"}}`, want: "unauthorized"},
		{name: "forbidden", statusCode: http.StatusForbidden, body: `{"error":{"message":"forbidden"}}`, want: "forbidden"},
		{name: "rate", statusCode: http.StatusTooManyRequests, body: `{"error":{"message":"too many"}}`, want: "rate limited"},
		{name: "upstream", statusCode: http.StatusBadGateway, body: `{"error":{"message":"gateway"}}`, want: "upstream failure"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			client, err := NewOpenRouterClient("test-key", ClientOptions{
				BaseURL:    server.URL + "/api/v1",
				Timeout:    2 * time.Second,
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
			}

			_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestOpenRouterStreamMalformedChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {bad json\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		BaseURL:    server.URL + "/api/v1",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}
	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected decode stream error")
	}
	if !strings.Contains(err.Error(), "decode stream chunk") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenRouterStreamChunkErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		BaseURL:    server.URL + "/api/v1",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}
	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected stream error")
	}
	if !strings.Contains(err.Error(), "stream error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenRouterStreamContextCanceledOrDeadline(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		Timeout:    20 * time.Millisecond,
		BaseURL:    "http://openrouter.test/api/v1",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
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

func TestOpenRouterDefaultModelAndCustomModel(t *testing.T) {
	client, err := NewOpenRouterClient("test-key", ClientOptions{})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}
	if client.model != defaultOpenRouterModel {
		t.Fatalf("expected default model %q, got %q", defaultOpenRouterModel, client.model)
	}

	client, err = NewOpenRouterClient("test-key", ClientOptions{Model: "openai/gpt-5.4-nano"})
	if err != nil {
		t.Fatalf("expected custom model to be accepted, got error: %v", err)
	}
	if client.model != "openai/gpt-5.4-nano" {
		t.Fatalf("expected custom model, got %q", client.model)
	}
}

func TestOpenRouterStreamPreservesTokenSpaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Привет\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"мир\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		BaseURL:    server.URL + "/api/v1",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}

	out, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if out.Content != "Привет мир" {
		t.Fatalf("expected preserved spaces, got %q", out.Content)
	}
}

func TestOpenRouterStreamSupportsMultilineDataEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\n")
		_, _ = io.WriteString(w, "data: \"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", ClientOptions{
		BaseURL:    server.URL + "/api/v1",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenRouterClient() unexpected error: %v", err)
	}

	out, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("expected content ok, got %q", out.Content)
	}
	if out.InputTokens != 3 || out.OutputTokens != 2 {
		t.Fatalf("unexpected usage mapping: %+v", out)
	}
}
