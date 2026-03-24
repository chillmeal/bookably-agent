package session

import (
	"time"

	"github.com/chillmeal/bookably-agent/internal/interpreter"
)

// Message keeps short dialog history for context-aware interpretation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PendingPlan struct {
	ID                string                    `json:"id"`
	Plan              interpreter.ActionPlan    `json:"plan"`
	PreviewMsgID      int64                     `json:"preview_msg_id"`
	CreatedAt         time.Time                 `json:"created_at"`
	IdempotencyKey    string                    `json:"idempotency_key"`
	SlotCandidates    []PendingSlotCandidate    `json:"slot_candidates,omitempty"`
	BookingCandidates []PendingBookingCandidate `json:"booking_candidates,omitempty"`
	Availability      *PendingAvailability      `json:"availability,omitempty"`
}

type Session struct {
	ChatID                int64        `json:"chat_id"`
	TelegramUserID        int64        `json:"telegram_user_id,omitempty"`
	LastProcessedUpdateID int64        `json:"last_processed_update_id,omitempty"`
	ProviderID            string       `json:"provider_id"`
	Timezone              string       `json:"timezone"`
	ClarificationCount    int          `json:"clarification_count"`
	PendingPlan           *PendingPlan `json:"pending_plan,omitempty"`
	DialogHistory         []Message    `json:"dialog_history"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

type PendingSlotCandidate struct {
	ID        string `json:"id"`
	ServiceID string `json:"service_id,omitempty"`
	StartAt   string `json:"start_at"`
	EndAt     string `json:"end_at"`
}

type PendingBookingCandidate struct {
	ID          string `json:"id"`
	ClientName  string `json:"client_name,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	At          string `json:"at"`
}

type PendingAvailability struct {
	Create        []PendingAvailabilityCreate `json:"create,omitempty"`
	DeleteSlotIDs []string                    `json:"delete_slot_ids,omitempty"`
	Availability  []PendingAvailabilityDay    `json:"availability,omitempty"`
}

type PendingAvailabilityCreate struct {
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
}

type PendingAvailabilityDay struct {
	Date   string                     `json:"date"`
	Ranges []PendingAvailabilityRange `json:"ranges,omitempty"`
}

type PendingAvailabilityRange struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}
