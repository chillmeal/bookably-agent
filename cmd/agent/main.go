package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/chillmeal/bookably-agent/config"
	"github.com/chillmeal/bookably-agent/internal/acp"
	"github.com/chillmeal/bookably-agent/internal/bookably"
	"github.com/chillmeal/bookably-agent/internal/bot"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/llm"
	"github.com/chillmeal/bookably-agent/internal/session"
	"github.com/chillmeal/bookably-agent/observability"
	"github.com/joho/godotenv"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func run() error {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	redisClient, err := session.NewRedisClientFromURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() {
		_ = redisClient.Close()
	}()

	sessionStore := session.NewRedisStore(redisClient, cfg.SessionTTL)
	appLogger := observability.NewLogger(os.Stdout)

	bookablyClient, err := bookably.NewClient(cfg.BookablyAPIURL, cfg.BookablyBotServiceKey, nil, cfg.BookablyHTTPTimeout)
	if err != nil {
		return err
	}
	provider, err := bookably.NewAdapter(bookablyClient, redisClient, 0)
	if err != nil {
		return err
	}

	llmClient, err := buildLLMClient(cfg)
	if err != nil {
		return err
	}

	promptPath, err := resolvePromptPath()
	if err != nil {
		return err
	}
	interp, err := interpreter.New(llmClient, promptPath, cfg.LLMTimeout)
	if err != nil {
		return err
	}

	acpClient, err := acp.NewClient(cfg.ACPBaseURL, cfg.ACPAPIKey, nil, cfg.ACPPollTimeout)
	if err != nil {
		return err
	}
	runner, err := acp.NewRunner(acpClient, cfg.ACPPollInterval, cfg.ACPPollTimeout)
	if err != nil {
		return err
	}
	executor, err := bot.NewRuntimeACPExecutor(cfg.BookablyAPIURL, cfg.BookablyBotServiceKey, runner)
	if err != nil {
		return err
	}
	executor.SetLogger(appLogger)

	telegram, err := bot.NewAPIGateway(cfg.TelegramBotToken, nil, "")
	if err != nil {
		return err
	}

	handler, err := bot.NewHandler(bot.HandlerConfig{
		WebhookSecret: cfg.TelegramWebhookSecret,
		WebhookURL:    cfg.TelegramWebhookURL,
		MiniAppURL:    cfg.MiniAppURL,
		PlanTTL:       cfg.PlanTTL,
		WorkerTimeout: cfg.WorkerTimeout,
	}, sessionStore, interp, provider, executor, telegram)
	if err != nil {
		return err
	}
	handler.SetLogger(appLogger)
	logACPAvailability(cfg.ACPBaseURL, cfg.ACPAPIKey, appLogger)

	registerCtx, cancelRegister := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRegister()
	if err := handler.RegisterWebhook(registerCtx); err != nil {
		return fmt.Errorf("register webhook: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		return server.Shutdown(shutdownCtx)
	case serveErr := <-errCh:
		return serveErr
	}
}

func logACPAvailability(baseURL, apiKey string, logger *observability.Logger) {
	if logger == nil {
		return
	}
	started := time.Now()
	probeURL := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		logger.LogError(observability.Entry{
			TraceID:    "startup-acp-check",
			Component:  "cmd/agent",
			DurationMS: time.Since(started).Milliseconds(),
			ErrorType:  "ErrValidation",
			Error:      err,
			Fields: map[string]any{
				"event":   "acp.unavailable",
				"stage":   "startup_check",
				"acp_url": probeURL,
			},
		})
		return
	}
	req.Header.Set("Authorization", strings.TrimSpace(apiKey))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.LogError(observability.Entry{
			TraceID:    "startup-acp-check",
			Component:  "cmd/agent",
			DurationMS: time.Since(started).Milliseconds(),
			ErrorType:  "ErrUpstream",
			Error:      err,
			Fields: map[string]any{
				"event":   "acp.unavailable",
				"stage":   "startup_check",
				"acp_url": probeURL,
			},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusInternalServerError {
		logger.LogError(observability.Entry{
			TraceID:    "startup-acp-check",
			Component:  "cmd/agent",
			DurationMS: time.Since(started).Milliseconds(),
			ErrorType:  "ErrUpstream",
			Error:      fmt.Errorf("acp health status %d", resp.StatusCode),
			Fields: map[string]any{
				"event":   "acp.unavailable",
				"stage":   "startup_check",
				"acp_url": probeURL,
				"status":  resp.StatusCode,
			},
		})
		return
	}

	logger.LogInfo(observability.Entry{
		TraceID:    "startup-acp-check",
		Component:  "cmd/agent",
		DurationMS: time.Since(started).Milliseconds(),
		Fields: map[string]any{
			"event":   "acp.available",
			"stage":   "startup_check",
			"acp_url": probeURL,
			"status":  resp.StatusCode,
		},
	})
}

func buildLLMClient(cfg *config.Config) (llm.LLMClient, error) {
	switch cfg.LLMProvider {
	case "openrouter":
		return llm.NewOpenRouterClientFromConfig(cfg)
	case "stub":
		return llm.NewStubClient(), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.LLMProvider)
	}
}

func resolvePromptPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve prompt path: getwd: %w", err)
	}
	path := filepath.Join(cwd, "prompts", "intent_classifier.md")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("resolve prompt path %s: %w", path, err)
	}
	return path, nil
}
