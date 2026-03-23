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
	"syscall"
	"time"

	"github.com/chillmeal/bookably-agent/config"
	"github.com/chillmeal/bookably-agent/internal/acp"
	"github.com/chillmeal/bookably-agent/internal/bookably"
	"github.com/chillmeal/bookably-agent/internal/bot"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/llm"
	"github.com/chillmeal/bookably-agent/internal/session"
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

	tokenStore, err := bookably.NewRedisTokenStore(redisClient, time.Hour)
	if err != nil {
		return err
	}
	bookablyClient, err := bookably.NewClient(cfg.BookablyAPIURL, cfg.BookablySpecialistID, tokenStore, nil, cfg.BookablyHTTPTimeout)
	if err != nil {
		return err
	}
	provider, err := bookably.NewAdapter(bookablyClient, cfg.BookablySpecialistID, redisClient, 0)
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
	tokenProvider, err := bookably.NewAccessTokenProvider(tokenStore)
	if err != nil {
		return err
	}
	executor, err := bot.NewRuntimeACPExecutor(cfg.BookablyAPIURL, runner, tokenProvider)
	if err != nil {
		return err
	}

	telegram, err := bot.NewAPIGateway(cfg.TelegramBotToken, nil, "")
	if err != nil {
		return err
	}

	handler, err := bot.NewHandler(bot.HandlerConfig{
		WebhookSecret:     cfg.TelegramWebhookSecret,
		WebhookURL:        cfg.TelegramWebhookURL,
		MiniAppURL:        cfg.MiniAppURL,
		DefaultProviderID: cfg.BookablySpecialistID,
		PlanTTL:           cfg.PlanTTL,
	}, sessionStore, interp, provider, executor, &botTokenStoreBridge{store: tokenStore}, telegram)
	if err != nil {
		return err
	}

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

func buildLLMClient(cfg *config.Config) (llm.LLMClient, error) {
	switch cfg.LLMProvider {
	case "anthropic":
		return llm.NewAnthropicClientFromConfig(cfg)
	case "openai":
		return llm.NewOpenAIClientFromConfig(cfg)
	case "amvera":
		return llm.NewAmveraClientFromConfig(cfg)
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

type botTokenStoreBridge struct {
	store bookably.TokenStore
}

func (b *botTokenStoreBridge) SaveToken(ctx context.Context, specialistID string, token bot.AuthToken) error {
	if b == nil || b.store == nil {
		return errors.New("bot token bridge: store is nil")
	}
	return b.store.SaveToken(ctx, specialistID, &bookably.Token{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
	})
}
