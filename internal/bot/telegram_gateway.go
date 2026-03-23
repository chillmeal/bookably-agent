package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type APIGateway struct {
	streamer *Streamer
}

func NewAPIGateway(token string, httpClient *http.Client, baseURL string) (*APIGateway, error) {
	streamer, err := NewStreamer(token, httpClient, baseURL)
	if err != nil {
		return nil, err
	}
	return &APIGateway{streamer: streamer}, nil
}

func (g *APIGateway) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if g == nil || g.streamer == nil {
		return errors.New("bot gateway: nil gateway")
	}
	payload := map[string]any{
		"chat_id": chatID,
		"action":  strings.TrimSpace(action),
	}
	return g.call(ctx, "sendChatAction", payload)
}

func (g *APIGateway) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	if g == nil || g.streamer == nil {
		return errors.New("bot gateway: nil gateway")
	}
	payload := map[string]any{
		"callback_query_id": strings.TrimSpace(callbackID),
		"text":              strings.TrimSpace(text),
	}
	return g.call(ctx, "answerCallbackQuery", payload)
}

func (g *APIGateway) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int64, markup *InlineKeyboardMarkup) error {
	if g == nil || g.streamer == nil {
		return errors.New("bot gateway: nil gateway")
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	return g.call(ctx, "editMessageReplyMarkup", payload)
}

func (g *APIGateway) SendText(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	if g == nil || g.streamer == nil {
		return 0, errors.New("bot gateway: nil gateway")
	}
	return g.streamer.Finalize(ctx, chatID, text, keyboard)
}

func (g *APIGateway) Draft(ctx context.Context, chatID int64, text string) error {
	if g == nil || g.streamer == nil {
		return errors.New("bot gateway: nil gateway")
	}
	return g.streamer.Draft(ctx, chatID, text)
}

func (g *APIGateway) Finalize(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	if g == nil || g.streamer == nil {
		return 0, errors.New("bot gateway: nil gateway")
	}
	return g.streamer.Finalize(ctx, chatID, text, keyboard)
}

func (g *APIGateway) SetWebhook(ctx context.Context, webhookURL, secret string, allowedUpdates []string) error {
	if g == nil || g.streamer == nil {
		return errors.New("bot gateway: nil gateway")
	}
	payload := map[string]any{
		"url":             strings.TrimSpace(webhookURL),
		"secret_token":    strings.TrimSpace(secret),
		"allowed_updates": allowedUpdates,
	}
	return g.call(ctx, "setWebhook", payload)
}

func (g *APIGateway) call(ctx context.Context, method string, payload interface{}) error {
	status, body, err := g.streamer.postJSON(ctx, method, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return buildTelegramError(method, status, body)
	}
	envelope, err := parseTelegramEnvelope(body)
	if err != nil {
		return fmt.Errorf("bot gateway: %s parse response: %w", method, err)
	}
	if !envelope.OK {
		return fmt.Errorf("bot gateway: %s rejected: %s", method, strings.TrimSpace(envelope.Description))
	}
	return nil
}
