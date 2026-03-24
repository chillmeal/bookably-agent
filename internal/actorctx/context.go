package actorctx

import (
	"context"
	"errors"
)

type contextKey string

const telegramUserIDKey contextKey = "telegram_user_id"

// WithTelegramUserID stores Telegram user ID in context for downstream Bookably calls.
func WithTelegramUserID(ctx context.Context, telegramUserID int64) context.Context {
	return context.WithValue(ctx, telegramUserIDKey, telegramUserID)
}

// TelegramUserIDFromContext extracts Telegram user ID from context.
func TelegramUserIDFromContext(ctx context.Context) (int64, error) {
	raw := ctx.Value(telegramUserIDKey)
	if raw == nil {
		return 0, errors.New("actor context: telegram user id is missing")
	}
	value, ok := raw.(int64)
	if !ok || value <= 0 {
		return 0, errors.New("actor context: telegram user id is invalid")
	}
	return value, nil
}
