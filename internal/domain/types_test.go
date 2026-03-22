package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBookingJSONRoundTripAndFieldNames(t *testing.T) {
	in := Booking{
		ID:          "b_1",
		PublicID:    "BK-100",
		ClientID:    "c_1",
		ClientName:  "Ivan Petrov",
		ServiceID:   "s_1",
		ServiceName: "Massage 60",
		At:          time.Date(2026, 3, 22, 10, 30, 0, 0, time.UTC),
		DurationMin: 60,
		Status:      BookingStatusUpcoming,
		Notes:       "note",
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal booking: %v", err)
	}

	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal booking to map: %v", err)
	}
	for _, k := range []string{
		"id",
		"public_id",
		"client_id",
		"client_name",
		"service_id",
		"service_name",
		"at",
		"duration_min",
		"status",
		"notes",
	} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("expected json key %q in booking payload", k)
		}
	}

	var out Booking
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal booking: %v", err)
	}
	if out.ID != in.ID || out.ClientName != in.ClientName || out.Status != in.Status {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
}

func TestPreviewJSONRoundTripAndFieldNames(t *testing.T) {
	in := Preview{
		Summary: "preview",
		AvailabilityChange: AvailabilityChange{
			AddedSlots:   2,
			RemovedSlots: 1,
		},
		Conflicts: []Conflict{
			{
				BookingID:   "b_1",
				ClientName:  "Maria",
				ServiceName: "Manicure",
				At:          time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC),
				Reason:      "slot overlaps",
			},
		},
		ProposedSlots: []Slot{
			{
				ID:        "slot_1",
				Start:     time.Date(2026, 3, 22, 18, 30, 0, 0, time.UTC),
				End:       time.Date(2026, 3, 22, 19, 30, 0, 0, time.UTC),
				ServiceID: "s_1",
			},
		},
		BookingResult: &Booking{
			ID:          "b_2",
			ClientName:  "Aline",
			ServiceName: "Massage",
			At:          time.Date(2026, 3, 23, 14, 0, 0, 0, time.UTC),
			DurationMin: 60,
			Status:      BookingStatusUpcoming,
		},
		RiskLevel: RiskHigh,
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}

	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal preview to map: %v", err)
	}
	for _, k := range []string{
		"summary",
		"availability_change",
		"conflicts",
		"proposed_slots",
		"booking_result",
		"risk_level",
	} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("expected json key %q in preview payload", k)
		}
	}

	var out Preview
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	if out.Summary != in.Summary || out.RiskLevel != in.RiskLevel {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
}

func TestDomainTypedErrorsSupportErrorsIs(t *testing.T) {
	tests := []error{
		ErrNotFound,
		ErrConflict,
		ErrValidation,
		ErrUpstream,
		ErrUnauthorized,
		ErrForbidden,
		ErrRateLimit,
	}

	for _, sentinel := range tests {
		wrapped := fmt.Errorf("wrapped: %w", sentinel)
		if !errors.Is(wrapped, sentinel) {
			t.Fatalf("errors.Is must match sentinel %v", sentinel)
		}
	}
}
