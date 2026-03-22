package bookably

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

type errorEnvelope struct {
	Error struct {
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Details interface{} `json:"details"`
	} `json:"error"`
}

func mapHTTPError(statusCode int, body []byte) error {
	message := http.StatusText(statusCode)
	if parsed := parseErrorEnvelope(body); parsed != "" {
		message = parsed
	}

	switch {
	case statusCode == http.StatusUnauthorized:
		return wrapDomainError(domain.ErrUnauthorized, message)
	case statusCode == http.StatusForbidden:
		return wrapDomainError(domain.ErrForbidden, message)
	case statusCode == http.StatusNotFound:
		return wrapDomainError(domain.ErrNotFound, message)
	case statusCode == http.StatusConflict:
		return wrapDomainError(domain.ErrConflict, message)
	case statusCode == http.StatusUnprocessableEntity:
		return wrapDomainError(domain.ErrValidation, message)
	case statusCode == http.StatusTooManyRequests:
		return wrapDomainError(domain.ErrRateLimit, message)
	case statusCode >= http.StatusInternalServerError:
		return wrapDomainError(domain.ErrUpstream, fmt.Sprintf("status %d: %s", statusCode, message))
	default:
		return fmt.Errorf("bookably: unexpected status %d: %s", statusCode, message)
	}
}

func parseErrorEnvelope(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return strings.TrimSpace(string(body))
	}

	parts := make([]string, 0, 3)
	if env.Error.Code != "" {
		parts = append(parts, env.Error.Code)
	}
	if env.Error.Message != "" {
		parts = append(parts, env.Error.Message)
	}
	if env.Error.Details != nil {
		parts = append(parts, fmt.Sprintf("details=%v", env.Error.Details))
	}
	return strings.TrimSpace(strings.Join(parts, ": "))
}

func wrapDomainError(kind error, msg string) error {
	if strings.TrimSpace(msg) == "" {
		return kind
	}
	return errors.Join(kind, fmt.Errorf("bookably: %s", msg))
}
