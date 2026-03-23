package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
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

type PendingPlanOptions struct {
	SlotCandidates []domain.Slot
	Availability   *domain.AvailabilityExecutionPayload
}

func SetPendingPlan(s *session.Session, plan interpreter.ActionPlan, msgID int64, idempKey string, opts *PendingPlanOptions) *session.PendingPlan {
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

	if opts != nil {
		if len(opts.SlotCandidates) > 0 {
			pending.SlotCandidates = toPendingSlotCandidates(opts.SlotCandidates)
		}
		if opts.Availability != nil {
			pending.Availability = toPendingAvailability(opts.Availability)
		}
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
	pending := SetPendingPlan(s, newPlan, msgID, idempKey, nil)
	return replaced, pending
}

func toPendingSlotCandidates(slots []domain.Slot) []session.PendingSlotCandidate {
	out := make([]session.PendingSlotCandidate, 0, len(slots))
	for _, slot := range slots {
		if strings.TrimSpace(slot.ID) == "" || slot.Start.IsZero() || slot.End.IsZero() {
			continue
		}
		out = append(out, session.PendingSlotCandidate{
			ID:        strings.TrimSpace(slot.ID),
			ServiceID: strings.TrimSpace(slot.ServiceID),
			StartAt:   slot.Start.UTC().Format(time.RFC3339),
			EndAt:     slot.End.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func toPendingAvailability(exec *domain.AvailabilityExecutionPayload) *session.PendingAvailability {
	if exec == nil {
		return nil
	}
	out := &session.PendingAvailability{
		Create:        make([]session.PendingAvailabilityCreate, 0, len(exec.CreateSlots)),
		DeleteSlotIDs: make([]string, 0, len(exec.DeleteSlotIDs)),
	}

	for _, slot := range exec.CreateSlots {
		if slot.Start.IsZero() || slot.End.IsZero() {
			continue
		}
		out.Create = append(out.Create, session.PendingAvailabilityCreate{
			StartAt: slot.Start.UTC().Format(time.RFC3339),
			EndAt:   slot.End.UTC().Format(time.RFC3339),
		})
	}

	for _, slotID := range exec.DeleteSlotIDs {
		slotID = strings.TrimSpace(slotID)
		if slotID == "" {
			continue
		}
		out.DeleteSlotIDs = append(out.DeleteSlotIDs, slotID)
	}

	if len(out.Create) == 0 && len(out.DeleteSlotIDs) == 0 {
		return nil
	}
	return out
}

func generatePlanID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}
