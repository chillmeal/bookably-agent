package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config defines all runtime configuration for the service.
type Config struct {
	// Telegram
	TelegramBotToken      string `envconfig:"TG_BOT_TOKEN" required:"true"`
	TelegramWebhookURL    string `envconfig:"TG_WEBHOOK_URL" required:"true"`
	TelegramWebhookSecret string `envconfig:"TG_WEBHOOK_SECRET" required:"true"`

	// Redis
	RedisURL string `envconfig:"REDIS_URL" required:"true"`

	// ACP
	ACPBaseURL string `envconfig:"ACP_BASE_URL" required:"true"`
	ACPAPIKey  string `envconfig:"ACP_API_KEY" required:"true"`

	// Bookably
	BookablyAPIURL       string `envconfig:"BOOKABLY_API_URL" required:"true"`
	BookablySpecialistID string `envconfig:"BOOKABLY_SPECIALIST_ID" required:"true"`

	// LLM
	LLMProvider string `envconfig:"LLM_PROVIDER" required:"true"` // anthropic | openai | stub
	LLMAPIKey   string `envconfig:"LLM_API_KEY" default:""`
	LLMModel    string `envconfig:"LLM_MODEL" default:""` // empty = provider default

	// Mini App
	MiniAppURL string `envconfig:"MINI_APP_URL" required:"true"`

	// Server
	Port     int    `envconfig:"PORT" default:"8080"`
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`

	// Timeouts / TTLs
	LLMTimeout          time.Duration `envconfig:"LLM_TIMEOUT" default:"15s"`
	SessionTTL          time.Duration `envconfig:"SESSION_TTL" default:"24h"`
	PlanTTL             time.Duration `envconfig:"PLAN_TTL" default:"15m"`
	ACPPollInterval     time.Duration `envconfig:"ACP_POLL_INTERVAL" default:"2s"`
	ACPPollTimeout      time.Duration `envconfig:"ACP_POLL_TIMEOUT" default:"30s"`
	BookablyHTTPTimeout time.Duration `envconfig:"BOOKABLY_HTTP_TIMEOUT" default:"5s"`
}

// Load parses environment variables into Config and validates cross-field constraints.
func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	required := map[string]string{
		"TG_BOT_TOKEN":           c.TelegramBotToken,
		"TG_WEBHOOK_URL":         c.TelegramWebhookURL,
		"TG_WEBHOOK_SECRET":      c.TelegramWebhookSecret,
		"REDIS_URL":              c.RedisURL,
		"ACP_BASE_URL":           c.ACPBaseURL,
		"ACP_API_KEY":            c.ACPAPIKey,
		"BOOKABLY_API_URL":       c.BookablyAPIURL,
		"BOOKABLY_SPECIALIST_ID": c.BookablySpecialistID,
		"LLM_PROVIDER":           c.LLMProvider,
		"MINI_APP_URL":           c.MiniAppURL,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("config: missing required variable %s", name)
		}
	}

	switch c.LLMProvider {
	case "anthropic", "openai":
		if strings.TrimSpace(c.LLMAPIKey) == "" {
			return nil, fmt.Errorf("config: missing required variable LLM_API_KEY for provider %s", c.LLMProvider)
		}
	case "stub":
		// LLM_API_KEY intentionally optional for stub mode.
	default:
		return nil, fmt.Errorf("config: LLM_PROVIDER must be 'anthropic', 'openai', or 'stub', got %q", c.LLMProvider)
	}

	return &c, nil
}
