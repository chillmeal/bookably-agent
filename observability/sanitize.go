package observability

import (
	"encoding/json"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var (
	quotedSecretValuePattern = regexp.MustCompile(`(?i)("(?:accesstoken|refreshtoken|authorization|api[_-]?key|secret|token)"\s*:\s*")([^"]+)(")`)
	bearerPattern            = regexp.MustCompile(`(?i)(bearer\s+)([^\s",;]+)`)
	keyValuePattern          = regexp.MustCompile(`(?i)\b(access[_-]?token|refresh[_-]?token|authorization|api[_-]?key|secret|token)\b\s*[:=]\s*([^\s",;]+)`)
)

func SanitizeString(value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return clean
	}

	clean = quotedSecretValuePattern.ReplaceAllString(clean, `${1}`+redacted+`${3}`)
	clean = bearerPattern.ReplaceAllString(clean, `${1}`+redacted)
	clean = keyValuePattern.ReplaceAllString(clean, `${1}=`+redacted)
	return clean
}

func sanitizeField(key string, value any) any {
	if isSensitiveKey(key) {
		return redacted
	}

	switch typed := value.(type) {
	case string:
		return SanitizeString(typed)
	case error:
		return SanitizeString(typed.Error())
	case []byte:
		return SanitizeString(string(typed))
	case json.RawMessage:
		return SanitizeString(string(typed))
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizeField(nestedKey, nestedValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeField("", item))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, SanitizeString(item))
		}
		return out
	default:
		return typed
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	sensitiveFragments := []string{
		"token",
		"authorization",
		"api_key",
		"apikey",
		"secret",
		"refresh",
	}
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
