package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/acp"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
)

type fakeRunSubmitter struct {
	lastRun acp.ACPRun
	fn      func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error)
}

func (f *fakeRunSubmitter) SubmitAndWait(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
	f.lastRun = run
	if f.fn != nil {
		return f.fn(ctx, run)
	}
	return &acp.ACPRunResult{RunID: "run-1", Status: acp.ACPStatusCompleted}, nil
}

func (f *fakeRunSubmitter) wasCalled() bool {
	return len(f.lastRun.Steps) > 0 || strings.TrimSpace(f.lastRun.IdempotencyKey) != ""
}

func TestRuntimeACPExecutorCancelRunShape(t *testing.T) {
	runner := &fakeRunSubmitter{}

	executor, err := NewRuntimeACPExecutor("https://bookably.test", "svc-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}

	s := &session.Session{ChatID: 10, ProviderID: "spec-1", TelegramUserID: 123456789}
	pending := &session.PendingPlan{
		ID:             "plan-1",
		IdempotencyKey: "idem-1",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentCancelBooking,
			Params: interpreter.ActionParams{
				BookingID: "booking-123",
			},
		},
	}

	result, err := executor.ExecuteConfirmed(context.Background(), s, pending)
	if err != nil {
		t.Fatalf("ExecuteConfirmed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(runner.lastRun.Steps) != 1 {
		t.Fatalf("expected 1 ACP step, got %d", len(runner.lastRun.Steps))
	}
	step := runner.lastRun.Steps[0]
	if step.Config.Method != "POST" {
		t.Fatalf("expected POST cancel method, got %q", step.Config.Method)
	}
	if step.Config.URL == "" || step.Config.URL != "https://bookably.test/api/v1/specialist/bookings/booking-123/cancel" {
		t.Fatalf("unexpected cancel URL: %q", step.Config.URL)
	}
	if runner.lastRun.Metadata["intent"] != string(interpreter.IntentCancelBooking) {
		t.Fatalf("unexpected intent metadata: %#v", runner.lastRun.Metadata)
	}
	if runner.lastRun.Metadata["risk_level"] != string(domain.RiskHigh) {
		t.Fatalf("unexpected risk level metadata: %#v", runner.lastRun.Metadata)
	}
	if runner.lastRun.Steps[0].Config.Headers["X-Bot-Service-Key"] != "svc-key" {
		t.Fatalf("expected X-Bot-Service-Key header, got %q", runner.lastRun.Steps[0].Config.Headers["X-Bot-Service-Key"])
	}
	if runner.lastRun.Steps[0].Config.Headers["X-Telegram-User-Id"] != "123456789" {
		t.Fatalf("expected X-Telegram-User-Id header, got %q", runner.lastRun.Steps[0].Config.Headers["X-Telegram-User-Id"])
	}
}

func TestRuntimeACPExecutorCreateBookingIsContractBlocked(t *testing.T) {
	runner := &fakeRunSubmitter{}

	executor, err := NewRuntimeACPExecutor("https://bookably.test", "svc-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}

	s := &session.Session{ChatID: 11, ProviderID: "spec-1", TelegramUserID: 123456789}
	pending := &session.PendingPlan{
		ID:             "plan-1",
		IdempotencyKey: "idem-1",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentCreateBooking,
			Params: interpreter.ActionParams{
				ServiceID: "svc-1",
				SlotID:    "slot-1",
			},
		},
	}

	_, err = executor.ExecuteConfirmed(context.Background(), s, pending)
	if err == nil {
		t.Fatal("expected contract blocked error for create_booking")
	}
	if !errors.Is(err, ErrExecutionContractBlocked) {
		t.Fatalf("expected ErrExecutionContractBlocked, got %v", err)
	}
}

func TestRuntimeACPExecutorAvailabilityDirectPrimary(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/specialist/schedule/commit" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Bot-Service-Key"); got != "svc-key" {
			t.Fatalf("X-Bot-Service-Key mismatch: %q", got)
		}
		if got := r.Header.Get("X-Telegram-User-Id"); got != "987654321" {
			t.Fatalf("X-Telegram-User-Id mismatch: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := &fakeRunSubmitter{}

	executor, err := NewRuntimeACPExecutor(server.URL, "svc-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}

	s := &session.Session{ChatID: 12, ProviderID: "spec-1", TelegramUserID: 987654321, Timezone: "Europe/Moscow"}
	start := time.Date(2026, 3, 24, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	pending := &session.PendingPlan{
		ID:             "plan-1",
		IdempotencyKey: "idem-1",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentSetWorkingHours,
		},
		Availability: &session.PendingAvailability{
			Create: []session.PendingAvailabilityCreate{
				{StartAt: start.Format(time.RFC3339), EndAt: end.Format(time.RFC3339)},
			},
			DeleteSlotIDs: []string{"slot-1"},
		},
	}

	_, err = executor.ExecuteConfirmed(context.Background(), s, pending)
	if err != nil {
		t.Fatalf("ExecuteConfirmed: %v", err)
	}
	if runner.wasCalled() {
		t.Fatal("expected ACP runner to be skipped for availability direct-primary path")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected one direct schedule commit call, got %d", calls)
	}
}

func TestRuntimeACPExecutorMapsTransientAndPolicy(t *testing.T) {
	cases := []struct {
		name      string
		runnerErr error
		expectErr error
	}{
		{
			name:      "policy",
			runnerErr: errors.Join(acp.ErrACPPolicyViolation, errors.New("policy")),
			expectErr: ErrExecutionPolicyViolation,
		},
		{
			name:      "transient",
			runnerErr: errors.Join(acp.ErrACPTransient, errors.New("transient")),
			expectErr: ErrExecutionTransient,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunSubmitter{
				fn: func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
					return nil, tc.runnerErr
				},
			}

			executor, err := NewRuntimeACPExecutor("https://bookably.test", "svc-key", runner)
			if err != nil {
				t.Fatalf("NewRuntimeACPExecutor: %v", err)
			}

			s := &session.Session{ChatID: 13, ProviderID: "spec-1", TelegramUserID: 123456789}
			pending := &session.PendingPlan{
				ID:             "plan-1",
				IdempotencyKey: "idem-1",
				Plan: interpreter.ActionPlan{
					Intent: interpreter.IntentCancelBooking,
					Params: interpreter.ActionParams{
						BookingID: "b-1",
					},
				},
			}

			_, err = executor.ExecuteConfirmed(context.Background(), s, pending)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("expected %v, got %v", tc.expectErr, err)
			}
		})
	}
}

func TestRuntimeACPExecutorRequiresTelegramUserID(t *testing.T) {
	runner := &fakeRunSubmitter{}
	executor, err := NewRuntimeACPExecutor("https://bookably.test", "svc-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}

	s := &session.Session{ChatID: 14, ProviderID: "spec-1"}
	pending := &session.PendingPlan{
		ID:             "plan-1",
		IdempotencyKey: "idem-1",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentCancelBooking,
			Params: interpreter.ActionParams{
				BookingID: "b-1",
			},
		},
	}

	_, err = executor.ExecuteConfirmed(context.Background(), s, pending)
	if err == nil {
		t.Fatal("expected error when telegram_user_id is missing")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestRuntimeACPExecutorFallbackToDirectOnACPContractMismatch(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/specialist/bookings/booking-123/cancel" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Bot-Service-Key"); got != "svc-key" {
			t.Fatalf("X-Bot-Service-Key mismatch: %q", got)
		}
		if got := r.Header.Get("X-Telegram-User-Id"); got != "123456789" {
			t.Fatalf("X-Telegram-User-Id mismatch: %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "idem-1" {
			t.Fatalf("Idempotency-Key mismatch: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := &fakeRunSubmitter{
		fn: func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
			return nil, errors.New("acp client: submit run status 400: {\"error\":{\"code\":\"policy_violation\",\"message\":\"invalid input: agent_id (must not be empty)\"}}")
		},
	}
	executor, err := NewRuntimeACPExecutor(server.URL, "svc-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}

	s := &session.Session{ChatID: 21, ProviderID: "spec-1", TelegramUserID: 123456789}
	pending := &session.PendingPlan{
		ID:             "plan-1",
		IdempotencyKey: "idem-1",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentCancelBooking,
			Params: interpreter.ActionParams{BookingID: "booking-123"},
		},
	}

	result, err := executor.ExecuteConfirmed(context.Background(), s, pending)
	if err != nil {
		t.Fatalf("ExecuteConfirmed: %v", err)
	}
	if result == nil || result.Message == "" {
		t.Fatalf("expected success result, got %+v", result)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected one direct fallback request, got %d", calls)
	}
}
