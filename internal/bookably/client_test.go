package bookably

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/redis/go-redis/v9"
)

type memoryTokenStore struct {
	mu     sync.Mutex
	tokens map[string]*Token
}

func newMemoryTokenStore(initial map[string]*Token) *memoryTokenStore {
	if initial == nil {
		initial = map[string]*Token{}
	}
	return &memoryTokenStore{tokens: initial}
}

func (s *memoryTokenStore) GetToken(ctx context.Context, specialistID string) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[specialistID]
	if !ok {
		return nil, domain.ErrUnauthorized
	}
	cp := *tok
	return &cp, nil
}

func (s *memoryTokenStore) SaveToken(ctx context.Context, specialistID string, token *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *token
	s.tokens[specialistID] = &cp
	return nil
}

func TestClientDo401RefreshOnce(t *testing.T) {
	const specialistID = "spec-1"
	var refreshCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resource":
			auth := r.Header.Get("Authorization")
			if auth == "Bearer fresh-token" {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{}`)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"UNAUTHORIZED","message":"expired"}}`)
		case endpointAuthRefresh:
			atomic.AddInt32(&refreshCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"accessToken":"fresh-token","refreshToken":"refresh-1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store := newMemoryTokenStore(map[string]*Token{
		specialistID: {AccessToken: "stale-token", RefreshToken: "refresh-1"},
	})

	client, err := NewClient(server.URL, specialistID, store, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodGet, "/resource", nil, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&refreshCalls) != 1 {
		t.Fatalf("expected one refresh call, got %d", refreshCalls)
	}
}

func TestClientDo429RetriesAfterHeader(t *testing.T) {
	const specialistID = "spec-2"
	var resourceCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		call := atomic.AddInt32(&resourceCalls, 1)
		if call == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"later"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	store := newMemoryTokenStore(map[string]*Token{
		specialistID: {AccessToken: "token", RefreshToken: "refresh"},
	})

	client, err := NewClient(server.URL, specialistID, store, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodGet, "/resource", nil, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	started := time.Now()
	resp, err := client.do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Fatalf("expected retry delay close to 1s, got %s", elapsed)
	}
}

func TestClientDo500ReturnsErrUpstream(t *testing.T) {
	const specialistID = "spec-3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"SERVER_ERROR","message":"boom"}}`)
	}))
	defer server.Close()

	store := newMemoryTokenStore(map[string]*Token{
		specialistID: {AccessToken: "token", RefreshToken: "refresh"},
	})

	client, err := NewClient(server.URL, specialistID, store, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodGet, "/resource", nil, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	_, err = client.do(context.Background(), req)
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestClientDoSecond401ReturnsErrUnauthorized(t *testing.T) {
	const specialistID = "spec-unauth"
	var refreshCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resource":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"UNAUTHORIZED","message":"nope"}}`)
		case endpointAuthRefresh:
			atomic.AddInt32(&refreshCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"accessToken":"still-bad","refreshToken":"r1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store := newMemoryTokenStore(map[string]*Token{
		specialistID: {AccessToken: "bad", RefreshToken: "r1"},
	})

	client, err := NewClient(server.URL, specialistID, store, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodGet, "/resource", nil, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	_, err = client.do(context.Background(), req)
	if err == nil {
		t.Fatal("expected unauthorized error")
	}
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if atomic.LoadInt32(&refreshCalls) != 1 {
		t.Fatalf("expected one refresh attempt, got %d", refreshCalls)
	}
}

func TestRedisTokenStoreRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	store, err := NewRedisTokenStore(rdb, time.Hour)
	if err != nil {
		t.Fatalf("NewRedisTokenStore: %v", err)
	}

	in := &Token{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour).UTC()}
	if err := store.SaveToken(context.Background(), "spec", in); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	out, err := store.GetToken(context.Background(), "spec")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if out.AccessToken != in.AccessToken || out.RefreshToken != in.RefreshToken {
		t.Fatalf("token mismatch: got %+v want %+v", out, in)
	}
}

func TestRedisTokenStoreConcurrentRefreshOnlyOneHTTPCall(t *testing.T) {
	const specialistID = "spec-4"
	var refreshCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resource":
			if r.Header.Get("Authorization") == "Bearer fresh" {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{}`)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"UNAUTHORIZED","message":"expired"}}`)
		case endpointAuthRefresh:
			atomic.AddInt32(&refreshCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"accessToken":"fresh","refreshToken":"r1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	store, err := NewRedisTokenStore(rdb, time.Hour)
	if err != nil {
		t.Fatalf("NewRedisTokenStore: %v", err)
	}

	if err := store.SaveToken(context.Background(), specialistID, &Token{AccessToken: "stale", RefreshToken: "r1"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	client, err := NewClient(server.URL, specialistID, store, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	callDo := func() error {
		req, reqErr := client.NewRequest(context.Background(), http.MethodGet, "/resource", nil, nil)
		if reqErr != nil {
			return reqErr
		}
		resp, doErr := client.do(context.Background(), req)
		if doErr != nil {
			return doErr
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("status is not 200")
		}
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- callDo()
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}

	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("expected exactly one refresh HTTP call, got %d", got)
	}
}
