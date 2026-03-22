package interpreter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSystemPrompt_ReplacesRuntimePlaceholders(t *testing.T) {
	t.Cleanup(func() {
		promptNowFunc = time.Now
	})
	promptNowFunc = func() time.Time {
		return time.Date(2026, 3, 22, 14, 5, 0, 0, time.UTC)
	}

	tz, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	content := strings.Join([]string{
		"Date={{TODAY_DATE}}",
		"Weekday={{TODAY_WEEKDAY}}",
		"Timezone={{TIMEZONE}}",
		"Time={{CURRENT_TIME}}",
	}, "\n")
	if err := osWriteFile(promptPath, []byte(content)); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	rendered, err := loadSystemPrompt(promptPath, tz)
	if err != nil {
		t.Fatalf("loadSystemPrompt: %v", err)
	}

	if !strings.Contains(rendered, "Date=2026-03-22") {
		t.Fatalf("expected rendered date, got: %q", rendered)
	}
	if !strings.Contains(rendered, "Weekday=Sunday") {
		t.Fatalf("expected rendered weekday, got: %q", rendered)
	}
	if !strings.Contains(rendered, "Timezone=Europe/Berlin") {
		t.Fatalf("expected rendered timezone, got: %q", rendered)
	}
	if !strings.Contains(rendered, "Time=15:05") {
		t.Fatalf("expected rendered time, got: %q", rendered)
	}
}

func TestLoadSystemPrompt_MissingFile(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	missingPath := filepath.Join(t.TempDir(), "missing.md")
	_, err = loadSystemPrompt(missingPath, tz)
	if err == nil {
		t.Fatal("expected error for missing prompt file")
	}
	if !strings.Contains(err.Error(), missingPath) {
		t.Fatalf("expected error to include path %q, got %q", missingPath, err.Error())
	}
}

func TestLoadSystemPrompt_NilTimezone(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := osWriteFile(promptPath, []byte("x")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	_, err := loadSystemPrompt(promptPath, nil)
	if err == nil {
		t.Fatal("expected error for nil timezone")
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
