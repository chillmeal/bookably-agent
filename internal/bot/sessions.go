package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
)

const historyLimit = 10

func LoadOrCreate(ctx context.Context, store session.SessionStore, chatID int64) (*session.Session, error) {
	if store == nil {
		return nil, errors.New("bot session helper: store is nil")
	}
	s, err := store.Get(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return &session.Session{
			ChatID:        chatID,
			DialogHistory: make([]session.Message, 0, historyLimit),
		}, nil
	}
	if s.ChatID == 0 {
		s.ChatID = chatID
	}
	if s.DialogHistory == nil {
		s.DialogHistory = make([]session.Message, 0, historyLimit)
	}
	if len(s.DialogHistory) > historyLimit {
		s.DialogHistory = s.DialogHistory[len(s.DialogHistory)-historyLimit:]
	}
	return s, nil
}

func SetPendingPlan(s *session.Session, plan interpreter.ActionPlan, msgID int64, idempKey string) *session.PendingPlan {
	if s == nil {
		return nil
	}

	pending := &session.PendingPlan{
		ID:             generatePlanID(),
		Plan:           plan,
		PreviewMsgID:   msgID,
		CreatedAt:      time.Now().UTC(),
		IdempotencyKey: strings.TrimSpace(idempKey),
	}
	s.PendingPlan = pending
	return pending
}

func ClearPendingPlan(s *session.Session) {
	if s == nil {
		return
	}
	s.PendingPlan = nil
}

func IsPlanExpired(plan *session.PendingPlan, now time.Time, ttl time.Duration) bool {
	if plan == nil {
		return false
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if plan.CreatedAt.IsZero() {
		return true
	}
	return !now.Before(plan.CreatedAt.Add(ttl))
}

func AppendHistory(s *session.Session, role, content string) {
	if s == nil {
		return
	}

	msg := session.Message{
		Role:    strings.TrimSpace(role),
		Content: strings.TrimSpace(content),
	}
	if msg.Content == "" {
		return
	}
	if msg.Role == "" {
		msg.Role = "user"
	}

	s.DialogHistory = append(s.DialogHistory, msg)
	if len(s.DialogHistory) > historyLimit {
		s.DialogHistory = s.DialogHistory[len(s.DialogHistory)-historyLimit:]
	}
}

func ReplacePendingPlan(s *session.Session, newPlan interpreter.ActionPlan, msgID int64, idempKey string) (bool, *session.PendingPlan) {
	if s == nil {
		return false, nil
	}
	replaced := s.PendingPlan != nil
	pending := SetPendingPlan(s, newPlan, msgID, idempKey)
	return replaced, pending
}

func generatePlanID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}
