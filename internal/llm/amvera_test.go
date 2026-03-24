package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"strings"
	"testing"
	"time"
)

func TestAmveraCompleteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/models/gpt" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Auth-Token"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("invalid json body: %v", err)
		}
		if payload["model"] != "gpt-5" {
			t.Fatalf("unexpected model: %v", payload["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"cmpl_1",
			"choices":[{"message":{"role":"assistant","text":"{\"intent\":\"list_bookings\"}"}}],
			"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}
		}`)
	}))
	defer server.Close()

	client, err := NewAmveraClient("test-key", ClientOptions{
		Timeout:    time.Second,
		BaseURL:    server.URL + "/api/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAmveraClient() unexpected error: %v", err)
	}

	got, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if got.Content != `{"intent":"list_bookings"}` {
		t.Fatalf("content mismatch: %q", got.Content)
	}
	if got.InputTokens != 12 || got.OutputTokens != 5 {
		t.Fatalf("token mismatch: %+v", got)
	}
}

func TestAmveraCompleteErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"message":"Unauthorized"}`,
			want:       "unauthorized",
		},
		{
			name:       "forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"message":"Access to this model is forbidden"}`,
			want:       "forbidden",
		},
		{
			name:       "rate-limit",
			statusCode: http.StatusTooManyRequests,
			body:       `{"message":"Too many requests"}`,
			want:       "rate limited",
		},
		{
			name:       "billing-blocked",
			statusCode: http.StatusPaymentRequired,
			body:       `{"message":"Run out of tokens"}`,
			want:       "billing blocked",
		},
		{
			name:       "upstream",
			statusCode: http.StatusInternalServerError,
			body:       `{"message":"boom"}`,
			want:       "upstream failure",
		},
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

			client, err := NewAmveraClient("test-key", ClientOptions{
				Model:      "gpt-5",
				Timeout:    time.Second,
				BaseURL:    server.URL + "/api/v1",
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("NewAmveraClient() unexpected error: %v", err)
			}

			_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAmveraCompleteMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[`) // invalid json
	}))
	defer server.Close()

	client, err := NewAmveraClient("test-key", ClientOptions{
		Timeout:    time.Second,
		BaseURL:    server.URL + "/api/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAmveraClient() unexpected error: %v", err)
	}

	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAmveraCompleteContextCanceledOrDeadline(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	client, err := NewAmveraClient("test-key", ClientOptions{
		Model:      "gpt-5",
		Timeout:    20 * time.Millisecond,
		BaseURL:    "http://amvera.test/api/v1",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewAmveraClient() unexpected error: %v", err)
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

func TestAmveraDefaultModelIsGPT5(t *testing.T) {
	client, err := NewAmveraClient("test-key", ClientOptions{})
	if err != nil {
		t.Fatalf("NewAmveraClient() unexpected error: %v", err)
	}
	if client.model != "gpt-5" {
		t.Fatalf("expected default model gpt-5, got %q", client.model)
	}
}

func TestAmveraStrictModelPolicy(t *testing.T) {
	_, err := NewAmveraClient("test-key", ClientOptions{Model: "gpt-4"})
	if err == nil {
		t.Fatal("expected strict model policy error")
	}
	if !strings.Contains(err.Error(), "strict model policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAmveraRetriesOnTLSHandshakeTimeout(t *testing.T) {
	var attempts int32
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			n := atomic.AddInt32(&attempts, 1)
			if n == 1 {
				return nil, &net.OpError{Op: "dial", Err: errors.New("TLS handshake timeout")}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"choices":[{"message":{"role":"assistant","text":"{\"intent\":\"unknown\"}"}}],
					"usage":{"prompt_tokens":1,"completion_tokens":1}
				}`)),
			}, nil
		}),
	}

	client, err := NewAmveraClient("test-key", ClientOptions{
		Model:      "gpt-5",
		Timeout:    time.Second,
		BaseURL:    "http://amvera.test/api/v1",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewAmveraClient() unexpected error: %v", err)
	}

	out, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if out == nil || strings.TrimSpace(out.Content) == "" {
		t.Fatalf("expected non-empty completion, got: %+v", out)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}
