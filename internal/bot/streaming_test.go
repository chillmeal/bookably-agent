package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamerDraftAndFinalizeHappyPath(t *testing.T) {
	var draftCalls int32
	var finalizeCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/sendMessageDraft":
			atomic.AddInt32(&draftCalls, 1)
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "/bottoken/sendMessage":
			atomic.AddInt32(&finalizeCalls, 1)
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":777}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	streamer, err := NewStreamer("token", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewStreamer: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := streamer.Draft(context.Background(), 1, "Тест"); err != nil {
			t.Fatalf("Draft #%d: %v", i+1, err)
		}
	}
	msgID, err := streamer.Finalize(context.Background(), 1, "Финал", &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "ok", CallbackData: "confirm:1", Style: buttonStyleSuccess}},
		},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if msgID != 777 {
		t.Fatalf("message id mismatch: %d", msgID)
	}
	if atomic.LoadInt32(&draftCalls) != 3 {
		t.Fatalf("expected 3 draft calls, got %d", draftCalls)
	}
	if atomic.LoadInt32(&finalizeCalls) != 1 {
		t.Fatalf("expected 1 finalize call, got %d", finalizeCalls)
	}
}

func TestStreamerDraftConcurrencySafe(t *testing.T) {
	var inFlight int32
	var maxInFlight int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessageDraft" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()

	streamer, err := NewStreamer("token", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewStreamer: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			errCh <- streamer.Draft(context.Background(), 1, "x")
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Draft returned error: %v", err)
		}
	}
	if atomic.LoadInt32(&maxInFlight) != 1 {
		t.Fatalf("expected max in-flight draft requests to be 1, got %d", maxInFlight)
	}
}

func TestStreamerDraftContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent when context is canceled")
	}))
	defer server.Close()

	streamer, err := NewStreamer("token", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewStreamer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := streamer.Draft(ctx, 1, "x"); err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestStreamerFinalizeStyleFallbackRetry(t *testing.T) {
	var calls int32
	var sawStyleOnFirst bool
	var sawStyleOnSecond bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/sendMessage":
			call := atomic.AddInt32(&calls, 1)
			var req finalizeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			hasStyle := false
			if req.ReplyMarkup != nil {
				for _, row := range req.ReplyMarkup.InlineKeyboard {
					for _, btn := range row {
						if strings.TrimSpace(btn.Style) != "" {
							hasStyle = true
						}
					}
				}
			}

			if call == 1 {
				sawStyleOnFirst = hasStyle
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: style is not supported"}`)
				return
			}
			sawStyleOnSecond = hasStyle
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":101}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	streamer, err := NewStreamer("token", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewStreamer: %v", err)
	}

	keyboard := BuildPreviewKeyboard("p1")
	msgID, err := streamer.Finalize(context.Background(), 1, "text", &keyboard)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if msgID != 101 {
		t.Fatalf("message id mismatch: %d", msgID)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 sendMessage calls, got %d", calls)
	}
	if !sawStyleOnFirst {
		t.Fatal("expected style on first call")
	}
	if sawStyleOnSecond {
		t.Fatal("expected styles to be stripped on retry")
	}
}
