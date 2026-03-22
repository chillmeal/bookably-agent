package interpreter

import "testing"

func TestParseActionPlan_ValidJSON(t *testing.T) {
	raw := `{"intent":"create_booking","confidence":0.92,"requires_confirmation":false,"params":{"client_name":"Алина"}}`

	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatalf("ParseActionPlan: %v", err)
	}

	if plan.Intent != IntentCreateBooking {
		t.Fatalf("intent mismatch: got %q", plan.Intent)
	}
	if !plan.RequiresConfirm {
		t.Fatal("expected requires_confirmation to be normalized to true")
	}
	if plan.Params.ClientName != "Алина" {
		t.Fatalf("client_name mismatch: got %q", plan.Params.ClientName)
	}
}

func TestParseActionPlan_FencedJSON(t *testing.T) {
	raw := "```json\n{\"intent\":\"list_bookings\",\"confidence\":0.80,\"params\":{}}\n```"

	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatalf("ParseActionPlan: %v", err)
	}

	if plan.Intent != IntentListBookings {
		t.Fatalf("intent mismatch: got %q", plan.Intent)
	}
}

func TestParseActionPlan_ProseReturnsUnknown(t *testing.T) {
	plan, err := ParseActionPlan("sorry, I cannot parse this")
	if err != nil {
		t.Fatalf("ParseActionPlan: %v", err)
	}
	if plan.Intent != IntentUnknown {
		t.Fatalf("expected unknown intent, got %q", plan.Intent)
	}
}

func TestParseActionPlan_PartialJSONReturnsUnknown(t *testing.T) {
	plan, err := ParseActionPlan(`{"intent":"cancel_booking","confidence":0.9`)
	if err != nil {
		t.Fatalf("ParseActionPlan: %v", err)
	}
	if plan.Intent != IntentUnknown {
		t.Fatalf("expected unknown intent, got %q", plan.Intent)
	}
}

func TestParseActionPlan_LowConfidenceForcesUnknown(t *testing.T) {
	raw := `{"intent":"set_working_hours","confidence":0.49,"requires_confirmation":true,"params":{"client_name":"x"},"clarifications":[{"field":"date_range","question":"?"}]}`

	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatalf("ParseActionPlan: %v", err)
	}
	if plan.Intent != IntentUnknown {
		t.Fatalf("expected unknown intent, got %q", plan.Intent)
	}
	if plan.RequiresConfirm {
		t.Fatal("unknown intent must not require confirmation")
	}
	if len(plan.Clarifications) != 0 {
		t.Fatalf("expected clarifications to be dropped, got %d", len(plan.Clarifications))
	}
}
