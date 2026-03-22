package interpreter

import (
	"fmt"
	"os"
	"strings"
	"time"
)

var promptNowFunc = time.Now

// loadSystemPrompt reads the prompt file and injects runtime context.
func loadSystemPrompt(promptPath string, tz *time.Location) (string, error) {
	if strings.TrimSpace(promptPath) == "" {
		return "", fmt.Errorf("prompt: path is required")
	}
	if tz == nil {
		return "", fmt.Errorf("prompt: timezone is nil")
	}

	raw, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("prompt: read %s: %w", promptPath, err)
	}

	now := promptNowFunc().In(tz)
	replacer := strings.NewReplacer(
		"{{TODAY_DATE}}", now.Format("2006-01-02"),
		"{{TODAY_WEEKDAY}}", now.Weekday().String(),
		"{{TIMEZONE}}", tz.String(),
		"{{CURRENT_TIME}}", now.Format("15:04"),
	)

	return replacer.Replace(string(raw)), nil
}
