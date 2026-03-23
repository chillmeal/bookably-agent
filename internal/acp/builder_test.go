package acp

import (
	"testing"
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
	create := []CommitCreateItem{
		{Date: "2026-03-24", StartTime: "12:00", EndTime: "13:00"},
		{Date: "2026-03-24", StartTime: "13:00", EndTime: "14:00"},
	}
	deleteIDs := []string{"s1", "s2", "s3"}
	meta := RunMetadata{
		ChatID:       "777",
		SpecialistID: "spec-3",
		Intent:       "close_range",
		RiskLevel:    "medium",
		RawMessage:   "Закрой диапазон",
	}

	run, err := BuildAvailabilityRun("https://api.bookably.app", "token-3", create, deleteIDs, "idem-base", meta)
	if err != nil {
		t.Fatalf("BuildAvailabilityRun: %v", err)
	}

	if len(run.Steps) != 1 {
		t.Fatalf("expected 1 commit step, got %d", len(run.Steps))
	}
	commit := run.Steps[0]
	if commit.Capability != "availability.commit" {
		t.Fatalf("commit capability mismatch: %q", commit.Capability)
	}
	if commit.Config.Method != "POST" {
		t.Fatalf("commit method mismatch: %q", commit.Config.Method)
	}
	if commit.Config.Headers["Idempotency-Key"] != "idem-base:commit" {
		t.Fatalf("commit idempotency key mismatch: %q", commit.Config.Headers["Idempotency-Key"])
	}

	body, ok := commit.Config.Body.(CommitScheduleBody)
	if !ok {
		t.Fatalf("expected CommitScheduleBody, got %T", commit.Config.Body)
	}
	if len(body.Create) != 2 {
		t.Fatalf("expected 2 create items, got %d", len(body.Create))
	}
	if len(body.Delete) != 3 {
		t.Fatalf("expected 3 delete items, got %d", len(body.Delete))
	}
	if body.Delete[0].SlotID != "s1" || body.Delete[2].SlotID != "s3" {
		t.Fatalf("unexpected delete body: %#v", body.Delete)
	}
}
