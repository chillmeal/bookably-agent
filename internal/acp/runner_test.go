package acp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestRunnerSubmitAndWaitCompleted(t *testing.T) {
	var polls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/runs":
			_, _ = io.WriteString(w, `{"run_id":"run-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run-1":
			call := atomic.AddInt32(&polls, 1)
			if call <= 3 {
				_, _ = io.WriteString(w, `{"run_id":"run-1","status":"running"}`)
				return
			}
			_, _ = io.WriteString(w, `{"run_id":"run-1","status":"completed","output":{"ok":true}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(client, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.SubmitAndWait(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "test", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err != nil {
		t.Fatalf("SubmitAndWait: %v", err)
	}
	if result == nil || result.Status != ACPStatusCompleted {
		t.Fatalf("expected completed result, got %+v", result)
	}
}

func TestRunnerSubmitAndWaitTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/runs":
			_, _ = io.WriteString(w, `{"run_id":"run-timeout"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run-timeout":
			_, _ = io.WriteString(w, `{"run_id":"run-timeout","status":"running"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(client, 20*time.Millisecond, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	_, err = runner.SubmitAndWait(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "test", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrACPTimeout) {
		t.Fatalf("expected ErrACPTimeout, got %v", err)
	}
}

func TestRunnerSubmitAndWaitPolicyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/runs":
			_, _ = io.WriteString(w, `{"run_id":"run-fail"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run-fail":
			_, _ = io.WriteString(w, `{"run_id":"run-fail","status":"failed","error":{"code":"POLICY_DENIED","message":"policy blocked"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(client, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	_, err = runner.SubmitAndWait(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "test", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !errors.Is(err, ErrACPPolicyViolation) {
		t.Fatalf("expected ErrACPPolicyViolation, got %v", err)
	}
}

func TestRunnerSubmitAndWaitTransientFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/runs":
			_, _ = io.WriteString(w, `{"run_id":"run-transient"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run-transient":
			_, _ = io.WriteString(w, `{"run_id":"run-transient","status":"failed","error":{"code":"SERVER_ERROR","message":"upstream unavailable"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(client, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	_, err = runner.SubmitAndWait(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "test", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !errors.Is(err, ErrACPTransient) {
		t.Fatalf("expected ErrACPTransient, got %v", err)
	}
}

func TestRunnerSubmitAndWaitDomainConflictFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/runs":
			_, _ = io.WriteString(w, `{"run_id":"run-conflict"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run-conflict":
			_, _ = io.WriteString(w, `{"run_id":"run-conflict","status":"failed","error":{"code":"BOOKING_CONFLICT","message":"slot already taken"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(client, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	_, err = runner.SubmitAndWait(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "test", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected domain.ErrConflict, got %v", err)
	}
}
