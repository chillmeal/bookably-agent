package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	sessionKeyPrefix = "ba:session:"
	maxDialogHistory = 10
)

// SessionStore persists per-chat state in Redis.
type SessionStore interface {
	Get(ctx context.Context, chatID int64) (*Session, error)
	Save(ctx context.Context, s *Session) error
	Delete(ctx context.Context, chatID int64) error
}

type RedisStore struct {
	client redis.Cmdable
	ttl    time.Duration
}

func NewRedisStore(client redis.Cmdable, ttl time.Duration) *RedisStore {
	return &RedisStore{client: client, ttl: ttl}
}

func (s *RedisStore) Get(ctx context.Context, chatID int64) (*Session, error) {
	key := sessionKey(chatID)
	payload, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return &Session{ChatID: chatID, DialogHistory: make([]Message, 0, maxDialogHistory)}, nil
		}
		return nil, fmt.Errorf("session get %s: %w", key, err)
	}

	var out Session
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("session unmarshal %s: %w", key, err)
	}

	if out.ChatID == 0 {
		out.ChatID = chatID
	}
	if out.DialogHistory == nil {
		out.DialogHistory = make([]Message, 0, maxDialogHistory)
	}
	if len(out.DialogHistory) > maxDialogHistory {
		out.DialogHistory = out.DialogHistory[len(out.DialogHistory)-maxDialogHistory:]
	}

	return &out, nil
}

func (s *RedisStore) Save(ctx context.Context, in *Session) error {
	if in == nil {
		return errors.New("session save: nil session")
	}
	if in.ChatID == 0 {
		return errors.New("session save: chat_id must be non-zero")
	}
	if s.ttl <= 0 {
		return fmt.Errorf("session save: ttl must be > 0, got %s", s.ttl)
	}

	cp := *in
	if cp.DialogHistory == nil {
		cp.DialogHistory = make([]Message, 0, maxDialogHistory)
	}
	if len(cp.DialogHistory) > maxDialogHistory {
		cp.DialogHistory = cp.DialogHistory[len(cp.DialogHistory)-maxDialogHistory:]
	}
	cp.UpdatedAt = time.Now().UTC()

	payload, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("session save marshal chat_id=%d: %w", in.ChatID, err)
	}

	key := sessionKey(in.ChatID)
	if err := s.client.Set(ctx, key, payload, s.ttl).Err(); err != nil {
		return fmt.Errorf("session save %s: %w", key, err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, chatID int64) error {
	key := sessionKey(chatID)
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("session delete %s: %w", key, err)
	}
	return nil
}

func sessionKey(chatID int64) string {
	return fmt.Sprintf("%s%d", sessionKeyPrefix, chatID)
}
