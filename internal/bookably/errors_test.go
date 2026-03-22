package bookably

import (
	"errors"
	"testing"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestMapHTTPError_StatusMappings(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       error
	}{
		{name: "401 unauthorized", statusCode: 401, want: domain.ErrUnauthorized},
		{name: "403 forbidden", statusCode: 403, want: domain.ErrForbidden},
		{name: "404 not found", statusCode: 404, want: domain.ErrNotFound},
		{name: "409 conflict", statusCode: 409, want: domain.ErrConflict},
		{name: "422 validation", statusCode: 422, want: domain.ErrValidation},
		{name: "429 rate limit", statusCode: 429, want: domain.ErrRateLimit},
		{name: "500 upstream", statusCode: 500, want: domain.ErrUpstream},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := mapHTTPError(tc.statusCode, []byte(`{"error":{"code":"X","message":"m"}}`))
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("errors.Is mismatch: got %v want %v", err, tc.want)
			}
		})
	}
}

func TestMapHTTPError_ValidationContainsMessage(t *testing.T) {
	err := mapHTTPError(422, []byte(`{"error":{"code":"VALIDATION_ERROR","message":"bad payload","details":{"field":"from"}}}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if got := err.Error(); got == "" || got == "bookably: " {
		t.Fatalf("expected detailed error message, got %q", got)
	}
}
