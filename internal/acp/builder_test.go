package acp

import (
	"fmt"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestBuildCancelBookingRunShape(t *testing.T) {
	meta := RunMetadata{
		ChatID:       "123",
		SpecialistID: "spec-1",
		Intent:       "cancel_booking",
		RiskLevel:    "high",
		RawMessage:   "Отмени запись",
	}

	run, err := BuildCancelBookingRun("https://api.bookably.app", "token-1", "book-42", "idem-1", meta)
	if err != nil {
		t.Fatalf("BuildCancelBookingRun: %v", err)
	}

	if run.IdempotencyKey != "idem-1" {
		t.Fatalf("idempotency key mismatch: %q", run.IdempotencyKey)
	}
	if len(run.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(run.Steps))
	}

	step := run.Steps[0]
	if step.Capability != "booking.cancel" {
		t.Fatalf("capability mismatch: %q", step.Capability)
	}
	if step.Config.Method != "POST" {
		t.Fatalf("method mismatch: %q", step.Config.Method)
	}
	if step.Config.URL != "https://api.bookably.app/api/v1/specialist/bookings/book-42/cancel" {
		t.Fatalf("url mismatch: %q", step.Config.URL)
	}
	if step.Config.Headers["Authorization"] != "Bearer token-1" {
		t.Fatalf("authorization header mismatch: %q", step.Config.Headers["Authorization"])
	}
	if step.Config.Headers["Idempotency-Key"] != "idem-1" {
		t.Fatalf("idempotency header mismatch: %q", step.Config.Headers["Idempotency-Key"])
	}

	if run.Metadata["chat_id"] != "123" || run.Metadata["specialist_id"] != "spec-1" || run.Metadata["intent"] != "cancel_booking" || run.Metadata["risk_level"] != "high" || run.Metadata["raw_message"] != "Отмени запись" {
		t.Fatalf("unexpected metadata: %#v", run.Metadata)
	}
}

func TestBuildCreateBookingRunShape(t *testing.T) {
	meta := RunMetadata{
		ChatID:       "321",
		SpecialistID: "spec-2",
		Intent:       "create_booking",
		RiskLevel:    "medium",
		RawMessage:   "Запиши Алину",
	}

	run, err := BuildCreateBookingRun("https://api.bookably.app/", "token-2", "svc-1", "slot-7", "idem-2", meta)
	if err != nil {
		t.Fatalf("BuildCreateBookingRun: %v", err)
	}

	if len(run.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(run.Steps))
	}
	step := run.Steps[0]
	if step.Capability != "booking.create" {
		t.Fatalf("capability mismatch: %q", step.Capability)
	}
	if step.Config.Method != "POST" {
		t.Fatalf("method mismatch: %q", step.Config.Method)
	}
	if step.Config.URL != "https://api.bookably.app/api/v1/public/bookings" {
		t.Fatalf("url mismatch: %q", step.Config.URL)
	}
	if step.Config.Headers["Authorization"] != "Bearer token-2" {
		t.Fatalf("authorization header mismatch: %q", step.Config.Headers["Authorization"])
	}
	if step.Config.Headers["Idempotency-Key"] != "idem-2" {
		t.Fatalf("idempotency header mismatch: %q", step.Config.Headers["Idempotency-Key"])
	}
	body, ok := step.Config.Body.(map[string]string)
	if !ok {
		t.Fatalf("expected body map[string]string, got %T", step.Config.Body)
	}
	if body["serviceId"] != "svc-1" || body["slotId"] != "slot-7" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestBuildAvailabilityRunWithThreeSlots(t *testing.T) {
	now := time.Now().UTC()
	slots := []domain.Slot{
		{ID: "s1", Start: now, End: now.Add(time.Hour)},
		{ID: "s2", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		{ID: "s3", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour)},
	}
	meta := RunMetadata{
		ChatID:       "777",
		SpecialistID: "spec-3",
		Intent:       "close_range",
		RiskLevel:    "medium",
		RawMessage:   "Закрой диапазон",
	}

	run, err := BuildAvailabilityRun("https://api.bookably.app", "token-3", slots, "idem-base", meta)
	if err != nil {
		t.Fatalf("BuildAvailabilityRun: %v", err)
	}

	if len(run.Steps) != 4 {
		t.Fatalf("expected 4 steps (3 deletes + commit), got %d", len(run.Steps))
	}

	for idx := 0; idx < 3; idx++ {
		step := run.Steps[idx]
		if step.Capability != "availability.delete_slot" {
			t.Fatalf("step %d capability mismatch: %q", idx, step.Capability)
		}
		if step.Config.Method != "DELETE" {
			t.Fatalf("step %d method mismatch: %q", idx, step.Config.Method)
		}
		wantKey := fmt.Sprintf("idem-base:del:%d", idx)
		if step.Config.Headers["Idempotency-Key"] != wantKey {
			t.Fatalf("step %d idempotency key mismatch: got %q want %q", idx, step.Config.Headers["Idempotency-Key"], wantKey)
		}
	}

	commit := run.Steps[3]
	if commit.Capability != "availability.commit" {
		t.Fatalf("commit capability mismatch: %q", commit.Capability)
	}
	if commit.Config.Method != "POST" {
		t.Fatalf("commit method mismatch: %q", commit.Config.Method)
	}
	if commit.Config.Headers["Idempotency-Key"] != "idem-base:commit" {
		t.Fatalf("commit idempotency key mismatch: %q", commit.Config.Headers["Idempotency-Key"])
	}
}
