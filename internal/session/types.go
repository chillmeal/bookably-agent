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
	ID             string                 `json:"id"`
	Plan           interpreter.ActionPlan `json:"plan"`
	PreviewMsgID   int64                  `json:"preview_msg_id"`
	CreatedAt      time.Time              `json:"created_at"`
	IdempotencyKey string                 `json:"idempotency_key"`
}

type Session struct {
	ChatID             int64        `json:"chat_id"`
	ProviderID         string       `json:"provider_id"`
	Timezone           string       `json:"timezone"`
	ClarificationCount int          `json:"clarification_count"`
	PendingPlan        *PendingPlan `json:"pending_plan,omitempty"`
	DialogHistory      []Message    `json:"dialog_history"`
	UpdatedAt          time.Time    `json:"updated_at"`
}
