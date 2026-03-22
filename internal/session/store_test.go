package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T, ttl time.Duration) (*RedisStore, *miniredis.Miniredis, *redis.Client) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	return NewRedisStore(client, ttl), mr, client
}

func TestRedisStoreGetMissingReturnsEmptySession(t *testing.T) {
	store, _, _ := newTestStore(t, 24*time.Hour)

	s, err := store.Get(context.Background(), 101)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if s.ChatID != 101 {
		t.Fatalf("chat id mismatch: got %d", s.ChatID)
	}
	if s.PendingPlan != nil {
		t.Fatal("expected nil pending plan for new session")
	}
	if len(s.DialogHistory) != 0 {
		t.Fatalf("expected empty dialog history, got %d items", len(s.DialogHistory))
	}
}

func TestRedisStoreRoundTripPreservesFields(t *testing.T) {
	store, _, _ := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	in := &Session{
		ChatID:             777,
		ProviderID:         "prov_1",
		Timezone:           "Europe/Berlin",
		ClarificationCount: 1,
		PendingPlan: &PendingPlan{
			ID: "plan_a",
			Plan: interpreter.ActionPlan{
				Intent:          interpreter.IntentCreateBooking,
				Confidence:      0.91,
				RequiresConfirm: true,
				Params: interpreter.ActionParams{
					ClientName:  "Алина",
					ServiceName: "Массаж 60 мин",
				},
				RawUserMessage: "Запиши Алину на массаж",
				Timezone:       "Europe/Berlin",
			},
			PreviewMsgID:   12345,
			CreatedAt:      time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
			IdempotencyKey: "idem-1",
		},
		DialogHistory: []Message{
			{Role: "user", Content: "Привет"},
			{Role: "assistant", Content: "Привет!"},
		},
	}

	if err := store.Save(ctx, in); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	out, err := store.Get(ctx, 777)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	if out.ChatID != in.ChatID || out.ProviderID != in.ProviderID || out.Timezone != in.Timezone {
		t.Fatalf("session mismatch: got %+v want %+v", out, in)
	}
	if out.PendingPlan == nil {
		t.Fatal("expected pending plan")
	}
	if out.PendingPlan.ID != "plan_a" || out.PendingPlan.IdempotencyKey != "idem-1" {
		t.Fatalf("pending plan mismatch: %+v", out.PendingPlan)
	}
	if out.PendingPlan.Plan.Intent != interpreter.IntentCreateBooking {
		t.Fatalf("intent mismatch: got %s", out.PendingPlan.Plan.Intent)
	}
	if len(out.DialogHistory) != 2 {
		t.Fatalf("dialog history length mismatch: got %d", len(out.DialogHistory))
	}
}

func TestRedisStoreSaveCapsDialogHistoryToTen(t *testing.T) {
	store, _, _ := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	history := make([]Message, 0, 11)
	for i := 1; i <= 11; i++ {
		history = append(history, Message{Role: "user", Content: fmt.Sprintf("m%d", i)})
	}

	in := &Session{ChatID: 42, DialogHistory: history}
	if err := store.Save(ctx, in); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	out, err := store.Get(ctx, 42)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	if len(out.DialogHistory) != 10 {
		t.Fatalf("expected 10 history entries, got %d", len(out.DialogHistory))
	}
	if out.DialogHistory[0].Content != "m2" {
		t.Fatalf("expected oldest retained entry m2, got %q", out.DialogHistory[0].Content)
	}
	if out.DialogHistory[9].Content != "m11" {
		t.Fatalf("expected newest entry m11, got %q", out.DialogHistory[9].Content)
	}
}

func TestRedisStoreSaveResetsTTLAndExpires(t *testing.T) {
	store, mr, _ := newTestStore(t, 3*time.Second)
	ctx := context.Background()
	s := &Session{ChatID: 99}

	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}
	key := sessionKey(99)
	if !mr.Exists(key) {
		t.Fatalf("expected key %s to exist", key)
	}

	mr.FastForward(2 * time.Second)
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() second unexpected error: %v", err)
	}
	if ttl := mr.TTL(key); ttl < 2*time.Second {
		t.Fatalf("expected TTL reset close to 3s, got %s", ttl)
	}

	mr.FastForward(4 * time.Second)
	got, err := store.Get(ctx, 99)
	if err != nil {
		t.Fatalf("Get() after expiry unexpected error: %v", err)
	}
	if got.PendingPlan != nil || len(got.DialogHistory) != 0 || got.ProviderID != "" {
		t.Fatalf("expected empty session after expiration, got %+v", got)
	}
}

func TestRedisStorePlanReplacement(t *testing.T) {
	store, _, _ := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	s := &Session{
		ChatID: 123,
		PendingPlan: &PendingPlan{
			ID: "plan-A",
			Plan: interpreter.ActionPlan{
				Intent: interpreter.IntentSetWorkingHours,
			},
			PreviewMsgID:   111,
			CreatedAt:      time.Now().UTC(),
			IdempotencyKey: "idem-A",
		},
	}
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save(A) unexpected error: %v", err)
	}

	s.PendingPlan = &PendingPlan{
		ID: "plan-B",
		Plan: interpreter.ActionPlan{
			Intent: interpreter.IntentCancelBooking,
		},
		PreviewMsgID:   222,
		CreatedAt:      time.Now().UTC(),
		IdempotencyKey: "idem-B",
	}
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save(B) unexpected error: %v", err)
	}

	out, err := store.Get(ctx, 123)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if out.PendingPlan == nil || out.PendingPlan.ID != "plan-B" || out.PendingPlan.IdempotencyKey != "idem-B" {
		t.Fatalf("expected replaced plan-B, got %+v", out.PendingPlan)
	}
}

func TestRedisStorePendingPlanSurvivesProcessRestart(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()

	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	storeA := NewRedisStore(clientA, 24*time.Hour)

	s := &Session{
		ChatID:        555,
		ProviderID:    "prov-x",
		DialogHistory: []Message{{Role: "user", Content: "m1"}},
		PendingPlan:   &PendingPlan{ID: "persist", IdempotencyKey: "idem-persist", CreatedAt: time.Now().UTC()},
	}
	if err := storeA.Save(ctx, s); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}
	_ = clientA.Close()

	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientB.Close()
	storeB := NewRedisStore(clientB, 24*time.Hour)

	out, err := storeB.Get(ctx, 555)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if out.PendingPlan == nil || out.PendingPlan.ID != "persist" {
		t.Fatalf("expected persisted pending plan, got %+v", out.PendingPlan)
	}
	if out.ProviderID != "prov-x" {
		t.Fatalf("provider mismatch after restart: %q", out.ProviderID)
	}
}
