package config

import (
	"strings"
	"testing"
	"time"
)

var allEnvKeys = []string{
	"TG_BOT_TOKEN",
	"TG_WEBHOOK_URL",
	"TG_WEBHOOK_SECRET",
	"REDIS_URL",
	"ACP_BASE_URL",
	"ACP_API_KEY",
	"BOOKABLY_API_URL",
	"BOOKABLY_BOT_SERVICE_KEY",
	"LLM_PROVIDER",
	"LLM_API_KEY",
	"LLM_MODEL",
	"MINI_APP_URL",
	"PORT",
	"LOG_LEVEL",
	"LLM_TIMEOUT",
	"SESSION_TTL",
	"PLAN_TTL",
	"WORKER_TIMEOUT",
	"ACP_POLL_INTERVAL",
	"ACP_POLL_TIMEOUT",
	"BOOKABLY_HTTP_TIMEOUT",
}

func setBaseEnv(t *testing.T) {
	t.Helper()

	for _, key := range allEnvKeys {
		t.Setenv(key, "")
	}

	t.Setenv("TG_BOT_TOKEN", "token")
	t.Setenv("TG_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("TG_WEBHOOK_SECRET", "secret")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("ACP_BASE_URL", "http://localhost:8181")
	t.Setenv("ACP_API_KEY", "acp-key")
	t.Setenv("BOOKABLY_API_URL", "http://localhost:3000")
	t.Setenv("BOOKABLY_BOT_SERVICE_KEY", "bot-service-key")
	t.Setenv("LLM_PROVIDER", "openrouter")
	t.Setenv("LLM_API_KEY", "llm-key")
	t.Setenv("LLM_MODEL", "openai/gpt-5.4-nano")
	t.Setenv("MINI_APP_URL", "https://t.me/bookably_bot/app")
	t.Setenv("PORT", "8080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LLM_TIMEOUT", "15m")
	t.Setenv("SESSION_TTL", "24h")
	t.Setenv("PLAN_TTL", "15m")
	t.Setenv("WORKER_TIMEOUT", "90s")
	t.Setenv("ACP_POLL_INTERVAL", "2s")
	t.Setenv("ACP_POLL_TIMEOUT", "30s")
	t.Setenv("BOOKABLY_HTTP_TIMEOUT", "5s")
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name      string
		missing   string
		errorText string
	}{
		{
			name:      "missing telegram bot token",
			missing:   "TG_BOT_TOKEN",
			errorText: "TG_BOT_TOKEN",
		},
		{
			name:      "missing webhook url",
			missing:   "TG_WEBHOOK_URL",
			errorText: "TG_WEBHOOK_URL",
		},
		{
			name:      "missing webhook secret",
			missing:   "TG_WEBHOOK_SECRET",
			errorText: "TG_WEBHOOK_SECRET",
		},
		{
			name:      "missing redis url",
			missing:   "REDIS_URL",
			errorText: "REDIS_URL",
		},
		{
			name:      "missing llm api key",
			missing:   "LLM_API_KEY",
			errorText: "LLM_API_KEY",
		},
		{
			name:      "missing bookably bot service key",
			missing:   "BOOKABLY_BOT_SERVICE_KEY",
			errorText: "BOOKABLY_BOT_SERVICE_KEY",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(tc.missing, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for missing %s", tc.missing)
			}
			if !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errorText)
			}
		})
	}
}

func TestLoadConfig_ValidEnv(t *testing.T) {
	setBaseEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.TelegramBotToken != "token" {
		t.Fatalf("TelegramBotToken mismatch: %q", cfg.TelegramBotToken)
	}
	if cfg.TelegramWebhookURL != "https://example.com/webhook" {
		t.Fatalf("TelegramWebhookURL mismatch: %q", cfg.TelegramWebhookURL)
	}
	if cfg.TelegramWebhookSecret != "secret" {
		t.Fatalf("TelegramWebhookSecret mismatch: %q", cfg.TelegramWebhookSecret)
	}
	if cfg.RedisURL != "redis://localhost:6379/0" {
		t.Fatalf("RedisURL mismatch: %q", cfg.RedisURL)
	}
	if cfg.ACPBaseURL != "http://localhost:8181" {
		t.Fatalf("ACPBaseURL mismatch: %q", cfg.ACPBaseURL)
	}
	if cfg.ACPAPIKey != "acp-key" {
		t.Fatalf("ACPAPIKey mismatch: %q", cfg.ACPAPIKey)
	}
	if cfg.BookablyAPIURL != "http://localhost:3000" {
		t.Fatalf("BookablyAPIURL mismatch: %q", cfg.BookablyAPIURL)
	}
	if cfg.BookablyBotServiceKey != "bot-service-key" {
		t.Fatalf("BookablyBotServiceKey mismatch: %q", cfg.BookablyBotServiceKey)
	}
	if cfg.LLMProvider != "openrouter" {
		t.Fatalf("LLMProvider mismatch: %q", cfg.LLMProvider)
	}
	if cfg.LLMAPIKey != "llm-key" {
		t.Fatalf("LLMAPIKey mismatch: %q", cfg.LLMAPIKey)
	}
	if cfg.LLMModel != "openai/gpt-5.4-nano" {
		t.Fatalf("LLMModel mismatch: %q", cfg.LLMModel)
	}
	if cfg.MiniAppURL != "https://t.me/bookably_bot/app" {
		t.Fatalf("MiniAppURL mismatch: %q", cfg.MiniAppURL)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port mismatch: %d", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel mismatch: %q", cfg.LogLevel)
	}

	// Duration parse assertions required by AC.
	if cfg.LLMTimeout != 15*time.Minute {
		t.Fatalf("LLMTimeout mismatch: got %s", cfg.LLMTimeout)
	}
	if cfg.ACPPollInterval != 2*time.Second {
		t.Fatalf("ACPPollInterval mismatch: got %s", cfg.ACPPollInterval)
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("SessionTTL mismatch: got %s", cfg.SessionTTL)
	}
	if cfg.PlanTTL != 15*time.Minute {
		t.Fatalf("PlanTTL mismatch: got %s", cfg.PlanTTL)
	}
	if cfg.WorkerTimeout != 90*time.Second {
		t.Fatalf("WorkerTimeout mismatch: got %s", cfg.WorkerTimeout)
	}
	if cfg.ACPPollTimeout != 30*time.Second {
		t.Fatalf("ACPPollTimeout mismatch: got %s", cfg.ACPPollTimeout)
	}
	if cfg.BookablyHTTPTimeout != 5*time.Second {
		t.Fatalf("BookablyHTTPTimeout mismatch: got %s", cfg.BookablyHTTPTimeout)
	}
}

func TestLoadConfig_InvalidProvider(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("LLM_PROVIDER", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LLM_PROVIDER")
	}
	if !strings.Contains(err.Error(), "LLM_PROVIDER") {
		t.Fatalf("error %q does not reference LLM_PROVIDER", err.Error())
	}
}

func TestLoadConfig_StubProviderWithoutAPIKey(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("LLM_PROVIDER", "stub")
	t.Setenv("LLM_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error for stub provider: %v", err)
	}
	if cfg.LLMProvider != "stub" {
		t.Fatalf("LLMProvider mismatch: %q", cfg.LLMProvider)
	}
	if cfg.LLMAPIKey != "" {
		t.Fatalf("LLMAPIKey should be empty for stub provider, got %q", cfg.LLMAPIKey)
	}
}

func TestLoadConfig_OpenRouterProviderRequiresAPIKey(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("LLM_PROVIDER", "openrouter")
	t.Setenv("LLM_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for openrouter provider without LLM_API_KEY")
	}
	if !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("error %q does not mention LLM_API_KEY", err.Error())
	}
}

func TestLoadConfig_OpenRouterDefaultsModel(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("LLM_PROVIDER", "openrouter")
	t.Setenv("LLM_API_KEY", "or-key")
	t.Setenv("LLM_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.LLMModel != "openai/gpt-5.4-nano" {
		t.Fatalf("expected LLM_MODEL default openai/gpt-5.4-nano, got %q", cfg.LLMModel)
	}
}

func TestLoadConfig_OpenRouterRejectsNonStrictModel(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("LLM_PROVIDER", "openrouter")
	t.Setenv("LLM_API_KEY", "or-key")
	t.Setenv("LLM_MODEL", "openai/gpt-4o")

	_, err := Load()
	if err == nil {
		t.Fatal("expected strict model policy error for openrouter")
	}
	if !strings.Contains(err.Error(), "LLM_MODEL=openai/gpt-5.4-nano") {
		t.Fatalf("unexpected error: %v", err)
	}
}
