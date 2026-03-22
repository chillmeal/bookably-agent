package interpreter

import (
	"encoding/json"
	"strings"
)

// Intent is a normalized action category produced by the classifier.
type Intent string

const (
	IntentSetWorkingHours Intent = "set_working_hours"
	IntentAddBreak        Intent = "add_break"
	IntentCloseRange      Intent = "close_range"
	IntentListBookings    Intent = "list_bookings"
	IntentCreateBooking   Intent = "create_booking"
	IntentCancelBooking   Intent = "cancel_booking"
	IntentFindNextSlot    Intent = "find_next_slot"
	IntentUnknown         Intent = "unknown"
)

func normalizeIntent(raw string) Intent {
	switch Intent(strings.ToLower(strings.TrimSpace(raw))) {
	case IntentSetWorkingHours,
		IntentAddBreak,
		IntentCloseRange,
		IntentListBookings,
		IntentCreateBooking,
		IntentCancelBooking,
		IntentFindNextSlot,
		IntentUnknown:
		return Intent(strings.ToLower(strings.TrimSpace(raw)))
	default:
		return IntentUnknown
	}
}

// UnmarshalJSON normalizes unknown/invalid values to IntentUnknown.
func (i *Intent) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*i = normalizeIntent(raw)
	return nil
}

// RequiresConfirm indicates whether intent must pass through explicit confirm gate.
func (i Intent) RequiresConfirm() bool {
	switch i {
	case IntentSetWorkingHours, IntentAddBreak, IntentCloseRange, IntentCreateBooking, IntentCancelBooking:
		return true
	default:
		return false
	}
}

type Clarification struct {
	Field    string `json:"field"`
	Question string `json:"question"`
}

type DateRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type TimeRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// ActionParams is a normalized payload emitted by the interpreter.
// It intentionally mirrors the system prompt schema from Doc 06.
type ActionParams struct {
	DateRange       *DateRange  `json:"date_range,omitempty"`
	Weekdays        []string    `json:"weekdays,omitempty"`
	WorkingHours    *TimeRange  `json:"working_hours,omitempty"`
	Breaks          []TimeRange `json:"breaks,omitempty"`
	BreakSlot       *TimeRange  `json:"break_slot,omitempty"`
	TimeRange       *TimeRange  `json:"time_range,omitempty"`
	ClientName      string      `json:"client_name,omitempty"`
	ClientReference string      `json:"client_reference,omitempty"`
	ServiceID       string      `json:"service_id,omitempty"`
	ServiceName     string      `json:"service_name,omitempty"`
	SlotID          string      `json:"slot_id,omitempty"`
	BookingID       string      `json:"booking_id,omitempty"`
	NotBefore       string      `json:"not_before,omitempty"`
	PreferredAt     string      `json:"preferred_at,omitempty"`
	PreferredDate   string      `json:"preferred_date,omitempty"`
	ApproximateTime string      `json:"approximate_time,omitempty"`
	Status          string      `json:"status,omitempty"`
	MaxResults      int         `json:"max_results,omitempty"`
}

type ActionPlan struct {
	Intent          Intent          `json:"intent"`
	Confidence      float64         `json:"confidence"`
	RequiresConfirm bool            `json:"requires_confirmation"`
	Params          ActionParams    `json:"params"`
	Clarifications  []Clarification `json:"clarifications,omitempty"`
	RawUserMessage  string          `json:"raw_user_message,omitempty"`
	Timezone        string          `json:"timezone,omitempty"`
}

func (p ActionPlan) NeedsClarification() bool {
	return len(p.Clarifications) > 0
}
