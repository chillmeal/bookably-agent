package bookably

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/actorctx"
	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestClientGetJSONSendsBotAuthHeaders(t *testing.T) {
	var seenServiceKey string
	var seenTelegramID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenServiceKey = r.Header.Get(headerBotServiceKey)
		seenTelegramID = r.Header.Get(headerTelegramUser)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-1", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := actorctx.WithTelegramUserID(context.Background(), 987654321)
	var out map[string]any
	if err := client.GetJSON(ctx, "/resource", nil, &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if seenServiceKey != "svc-key-1" {
		t.Fatalf("expected service key header, got %q", seenServiceKey)
	}
	if seenTelegramID != "987654321" {
		t.Fatalf("expected telegram user id header, got %q", seenTelegramID)
	}
}

func TestClientGetJSONRequiresActorContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-2", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = client.GetJSON(context.Background(), "/resource", nil, &map[string]any{})
	if err == nil {
		t.Fatal("expected error when actor context is missing")
	}
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestClientDo429RetriesAfterHeader(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"later"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-3", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	started := time.Now()
	ctx := actorctx.WithTelegramUserID(context.Background(), 42)
	var out map[string]any
	if err := client.GetJSON(ctx, "/resource", nil, &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Fatalf("expected retry delay close to 1s, got %s", elapsed)
	}
	if calls != 2 {
		t.Fatalf("expected two calls after retry, got %d", calls)
	}
}

func TestClientDo429TooLargeRetryAfterReturnsErrRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"wait"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-4", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := actorctx.WithTelegramUserID(context.Background(), 42)
	err = client.GetJSON(ctx, "/resource", nil, &map[string]any{})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if !errors.Is(err, domain.ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
}

func TestClientGetJSONMapsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"SERVER_ERROR","message":"boom"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-5", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := actorctx.WithTelegramUserID(context.Background(), 77)
	err = client.GetJSON(ctx, "/resource", nil, &map[string]any{})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestClientPostJSONIncludesIdempotencyKey(t *testing.T) {
	var seenKey string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"done":true}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "svc-key-6", server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := actorctx.WithTelegramUserID(context.Background(), 111)
	err = client.PostJSON(ctx, "/resource", map[string]any{"a": 1}, "idem-1", &map[string]any{})
	if err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if seenKey != "idem-1" {
		t.Fatalf("expected idempotency key idem-1, got %q", seenKey)
	}
	if body["a"] != float64(1) {
		t.Fatalf("unexpected request body: %#v", body)
	}
}
