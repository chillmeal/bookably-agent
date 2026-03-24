package bot

import (
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestParseCallbackRoundTrip(t *testing.T) {
	planID := "abc123"

	cases := []string{
		ConfirmData(planID),
		CancelData(planID),
		SlotData(1, planID),
		BookingData(2, planID),
	}

	for _, raw := range cases {
		parsed, err := ParseCallback(raw)
		if err != nil {
			t.Fatalf("ParseCallback(%q): %v", raw, err)
		}
		if parsed.PlanID != planID {
			t.Fatalf("plan id mismatch: got %q want %q", parsed.PlanID, planID)
		}
	}
}

func TestParseCallbackInvalid(t *testing.T) {
	invalid := []string{
		"",
		"unknown:x",
		"confirm:",
		"cancel",
		"slot:abc:plan",
		"slot:1:",
	}

	for _, raw := range invalid {
		if _, err := ParseCallback(raw); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestBuildSlotKeyboardAndStripStyles(t *testing.T) {
	now := time.Now().UTC()
	keyboard := BuildSlotKeyboard("plan1", []domain.Slot{
		{ID: "s1", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		{ID: "s2", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour)},
	}, time.UTC)

	if len(keyboard.InlineKeyboard) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(keyboard.InlineKeyboard))
	}
	if len(keyboard.InlineKeyboard[0]) != 2 {
		t.Fatalf("expected 2 slot buttons, got %d", len(keyboard.InlineKeyboard[0]))
	}

	noStyle := StripKeyboardStyles(&keyboard)
	if noStyle == nil {
		t.Fatal("expected stripped keyboard")
	}
	for _, row := range noStyle.InlineKeyboard {
		for _, btn := range row {
			if btn.Style != "" {
				t.Fatalf("expected style to be removed, got %q", btn.Style)
			}
		}
	}
}

func TestBuildBookingCandidatesKeyboard(t *testing.T) {
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	keyboard := BuildBookingCandidatesKeyboard("plan2", []domain.Booking{
		{ID: "b1", ClientName: "Алина", ServiceName: "Массаж", At: now},
		{ID: "b2", ClientName: "Иван", ServiceName: "Стрижка", At: now.Add(time.Hour)},
	}, time.UTC)

	if len(keyboard.InlineKeyboard) != 3 {
		t.Fatalf("expected 3 rows (2 candidates + cancel), got %d", len(keyboard.InlineKeyboard))
	}
	first := keyboard.InlineKeyboard[0][0]
	if first.CallbackData != BookingData(0, "plan2") {
		t.Fatalf("unexpected booking callback data: %q", first.CallbackData)
	}
	last := keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1][0]
	if last.CallbackData != CancelData("plan2") {
		t.Fatalf("unexpected cancel callback data: %q", last.CallbackData)
	}
}
