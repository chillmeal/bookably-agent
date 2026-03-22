package interpreter

import (
	"encoding/json"
	"testing"
)

func TestIntentRequiresConfirmTruthTable(t *testing.T) {
	tests := []struct {
		intent Intent
		want   bool
	}{
		{IntentSetWorkingHours, true},
		{IntentAddBreak, true},
		{IntentCloseRange, true},
		{IntentCreateBooking, true},
		{IntentCancelBooking, true},
		{IntentListBookings, false},
		{IntentFindNextSlot, false},
		{IntentUnknown, false},
	}

	for _, tc := range tests {
		got := tc.intent.RequiresConfirm()
		if got != tc.want {
			t.Fatalf("intent %q: RequiresConfirm()=%v want %v", tc.intent, got, tc.want)
		}
	}
}

func TestActionPlanNeedsClarification(t *testing.T) {
	plan := ActionPlan{}
	if plan.NeedsClarification() {
		t.Fatal("expected false with empty clarifications")
	}

	plan.Clarifications = []Clarification{{Field: "service_name", Question: "Уточни услугу"}}
	if !plan.NeedsClarification() {
		t.Fatal("expected true when clarifications exist")
	}
}

func TestActionPlanRoundTripForAllIntents(t *testing.T) {
	intents := []Intent{
		IntentSetWorkingHours,
		IntentAddBreak,
		IntentCloseRange,
		IntentListBookings,
		IntentCreateBooking,
		IntentCancelBooking,
		IntentFindNextSlot,
		IntentUnknown,
	}

	for _, intent := range intents {
		t.Run(string(intent), func(t *testing.T) {
			in := ActionPlan{
				Intent:          intent,
				Confidence:      0.83,
				RequiresConfirm: intent.RequiresConfirm(),
				Params: ActionParams{
					DateRange:    &DateRange{From: "2026-03-22", To: "2026-03-28"},
					Weekdays:     []string{"mon", "wed"},
					WorkingHours: &TimeRange{From: "10:00", To: "18:00"},
					BreakSlot:    &TimeRange{From: "13:00", To: "14:00"},
					ClientName:   "Иван",
					ServiceName:  "Массаж 60 мин",
					NotBefore:    "2026-03-22T18:00:00",
					Status:       "upcoming",
					MaxResults:   2,
				},
				Clarifications: []Clarification{{Field: "client_name", Question: "Уточни имя клиента"}},
				RawUserMessage: "fixture",
				Timezone:       "Europe/Berlin",
			}

			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var out ActionPlan
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if out.Intent != in.Intent {
				t.Fatalf("intent mismatch: got %q want %q", out.Intent, in.Intent)
			}
			if out.Confidence != in.Confidence {
				t.Fatalf("confidence mismatch: got %f want %f", out.Confidence, in.Confidence)
			}
			if out.RequiresConfirm != in.RequiresConfirm {
				t.Fatalf("requires_confirmation mismatch: got %v want %v", out.RequiresConfirm, in.RequiresConfirm)
			}
			if out.Params.ServiceName != in.Params.ServiceName || out.Params.ClientName != in.Params.ClientName {
				t.Fatalf("params mismatch: got %+v want %+v", out.Params, in.Params)
			}
			if len(out.Clarifications) != 1 || out.Clarifications[0].Field != "client_name" {
				t.Fatalf("clarifications mismatch: got %+v", out.Clarifications)
			}
			if out.RawUserMessage != in.RawUserMessage || out.Timezone != in.Timezone {
				t.Fatalf("metadata mismatch: got raw=%q tz=%q", out.RawUserMessage, out.Timezone)
			}
		})
	}
}

func TestIntentUnknownOnUnmarshal(t *testing.T) {
	payload := `{"intent":"some_new_intent","confidence":0.9,"requires_confirmation":false,"params":{}}`

	var plan ActionPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if plan.Intent != IntentUnknown {
		t.Fatalf("expected %q, got %q", IntentUnknown, plan.Intent)
	}
}
