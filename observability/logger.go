package observability

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	TraceID    string
	ChatID     int64
	Intent     string
	Component  string
	DurationMS int64
	ErrorType  string
	Error      error
	Fields     map[string]any
}

type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

func NewLogger(out io.Writer) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{out: out}
}

func (l *Logger) LogError(entry Entry) {
	if l == nil || l.out == nil {
		return
	}

	payload := map[string]any{
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"level":       "error",
		"trace_id":    strings.TrimSpace(entry.TraceID),
		"chat_id":     entry.ChatID,
		"intent":      strings.TrimSpace(entry.Intent),
		"component":   strings.TrimSpace(entry.Component),
		"duration_ms": entry.DurationMS,
		"error_type":  strings.TrimSpace(entry.ErrorType),
	}

	if entry.Error != nil {
		payload["error"] = SanitizeString(entry.Error.Error())
	} else {
		payload["error"] = ""
	}

	for k, v := range entry.Fields {
		payload[k] = sanitizeField(k, v)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(append(encoded, '\n'))
}

