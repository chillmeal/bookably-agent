package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLoggerLogErrorRedactsSecrets(t *testing.T) {
	var out bytes.Buffer
	logger := NewLogger(&out)

	rawAccessToken := "access-token-123"
	rawRefreshToken := "refresh-token-456"

	logger.LogError(Entry{
		TraceID:    "trace-1",
		ChatID:     101,
		Intent:     "cancel_booking",
		Component:  "bookably/client",
		DurationMS: 42,
		ErrorType:  "ErrUnauthorized",
		Error:      errors.New(`authorization: Bearer ` + rawAccessToken + ` refreshToken=` + rawRefreshToken),
		Fields: map[string]any{
			"access_token": rawAccessToken,
			"refreshToken": rawRefreshToken,
			"details":      `token=` + rawAccessToken,
		},
	})

	line := strings.TrimSpace(out.String())
	if line == "" {
		t.Fatal("expected log output")
	}
	if strings.Contains(line, rawAccessToken) {
		t.Fatalf("access token leaked in log line: %s", line)
	}
	if strings.Contains(line, rawRefreshToken) {
		t.Fatalf("refresh token leaked in log line: %s", line)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["trace_id"] != "trace-1" {
		t.Fatalf("trace_id mismatch: %#v", payload["trace_id"])
	}
	if payload["error_type"] != "ErrUnauthorized" {
		t.Fatalf("error_type mismatch: %#v", payload["error_type"])
	}
}

func TestSanitizeStringPatterns(t *testing.T) {
	input := `{"accessToken":"abc","refreshToken":"def","authorization":"Bearer xyz","api_key":"k1","secret":"s1"}`
	sanitized := SanitizeString(input)

	for _, secret := range []string{"abc", "def", "xyz", "k1", "s1"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("secret %q leaked in sanitized string: %s", secret, sanitized)
		}
	}
}

