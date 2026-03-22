package acp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSubmitRunReturnsRunID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/runs" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "acp-key" {
			t.Fatalf("authorization header mismatch: %q", got)
		}
		_, _ = io.WriteString(w, `{"run_id":"run-123"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	runID, err := client.SubmitRun(context.Background(), ACPRun{Steps: []ACPStep{{Type: "http", Capability: "x", Config: ACPStepConfig{Method: "GET", URL: "http://example"}}}})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if runID != "run-123" {
		t.Fatalf("run id mismatch: %q", runID)
	}
}

func TestClientGetRunFailedStatusReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/runs/run-failed" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"run_id":"run-failed","status":"failed","error":{"code":"POLICY_DENIED","message":"blocked"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "acp-key", server.Client(), time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	result, err := client.GetRun(context.Background(), "run-failed")
	if err == nil {
		t.Fatal("expected failed run error")
	}
	if result == nil || result.Status != ACPStatusFailed {
		t.Fatalf("expected failed result, got %+v", result)
	}
}
