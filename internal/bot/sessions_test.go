package bot

import (
	"context"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
)

type stubStore struct {
	s *session.Session
}

func (s *stubStore) Get(ctx context.Context, chatID int64) (*session.Session, error) {
	if s.s == nil {
		return &session.Session{
			ChatID:        chatID,
			DialogHistory: []session.Message{},
		}, nil
	}
	return s.s, nil
}

func (s *stubStore) Save(ctx context.Context, ss *session.Session) error {
	s.s = ss
	return nil
}

func (s *stubStore) Delete(ctx context.Context, chatID int64) error {
	s.s = nil
	return nil
}

func TestLoadOrCreate(t *testing.T) {
	store := &stubStore{}
	got, err := LoadOrCreate(context.Background(), store, 42)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if got.ChatID != 42 {
		t.Fatalf("chat id mismatch: %d", got.ChatID)
	}
}

func TestIsPlanExpiredBoundary(t *testing.T) {
	now := time.Now().UTC()
	plan := &session.PendingPlan{CreatedAt: now.Add(-14 * time.Minute)}
	if IsPlanExpired(plan, now, 15*time.Minute) {
		t.Fatal("plan should not be expired at 14 minutes")
	}

	plan.CreatedAt = now.Add(-16 * time.Minute)
	if !IsPlanExpired(plan, now, 15*time.Minute) {
		t.Fatal("plan should be expired at 16 minutes")
	}
}

func TestAppendHistoryTrimsToTen(t *testing.T) {
	s := &session.Session{}
	for i := 0; i < 11; i++ {
		AppendHistory(s, "user", "msg")
	}
	if len(s.DialogHistory) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(s.DialogHistory))
	}
}

func TestReplacePendingPlan(t *testing.T) {
	s := &session.Session{}
	first := interpreter.ActionPlan{Intent: interpreter.IntentListBookings}
	second := interpreter.ActionPlan{Intent: interpreter.IntentCancelBooking}

	replaced, p1 := ReplacePendingPlan(s, first, 1, "k1")
	if replaced {
		t.Fatal("first replacement should report replaced=false")
	}
	if p1 == nil || s.PendingPlan == nil {
		t.Fatal("pending plan must be set")
	}
	oldID := p1.ID

	replaced, p2 := ReplacePendingPlan(s, second, 2, "k2")
	if !replaced {
		t.Fatal("second replacement should report replaced=true")
	}
	if p2 == nil || p2.ID == oldID {
		t.Fatal("second plan id should differ from first")
	}
	if s.PendingPlan.Plan.Intent != interpreter.IntentCancelBooking {
		t.Fatalf("unexpected intent after replacement: %s", s.PendingPlan.Plan.Intent)
	}
}
