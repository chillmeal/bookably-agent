package bookably

import (
	"os"
	"strings"
	"testing"
)

func TestEndpointConstantsContract(t *testing.T) {
	if endpointSpecialistSlots != "/api/v1/specialist/slots" {
		t.Fatalf("unexpected specialist slots endpoint: %q", endpointSpecialistSlots)
	}
	if endpointSpecialistCommit != "/api/v1/specialist/schedule/commit" {
		t.Fatalf("unexpected specialist schedule commit endpoint: %q", endpointSpecialistCommit)
	}
	if endpointPublicBookings != "/api/v1/public/bookings" {
		t.Fatalf("unexpected public bookings endpoint: %q", endpointPublicBookings)
	}
	if endpointSpecialistBookCancel != "/api/v1/specialist/bookings/%s/cancel" {
		t.Fatalf("unexpected specialist cancel endpoint: %q", endpointSpecialistBookCancel)
	}
}

func TestEndpointsContractNoteIsResolved(t *testing.T) {
	raw, err := os.ReadFile("endpoints.go")
	if err != nil {
		t.Fatalf("read endpoints.go: %v", err)
	}
	text := strings.ToLower(string(raw))
	if strings.Contains(text, "unresolved") {
		t.Fatalf("endpoints contract note still contains unresolved markers")
	}
	if !strings.Contains(text, "p3-01 resolution") {
		t.Fatalf("expected p3-01 resolution note in endpoints contract block")
	}
	if !strings.Contains(text, "slot_not_available") {
		t.Fatalf("expected explicit 409 conflict code note in endpoints contract block")
	}
}

