package domain

import (
	"errors"
	"time"
)

// Typed domain errors used for routing and user-facing responses.
var (
	ErrNotFound     = errors.New("domain: not found")
	ErrConflict     = errors.New("domain: conflict")
	ErrValidation   = errors.New("domain: validation")
	ErrUpstream     = errors.New("domain: upstream")
	ErrUnauthorized = errors.New("domain: unauthorized")
	ErrForbidden    = errors.New("domain: forbidden")
	ErrRateLimit    = errors.New("domain: rate limit")
)

type BookingStatus string

const (
	BookingStatusUpcoming  BookingStatus = "upcoming"
	BookingStatusCancelled BookingStatus = "cancelled"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type Booking struct {
	ID          string        `json:"id"`
	PublicID    string        `json:"public_id,omitempty"`
	ClientID    string        `json:"client_id,omitempty"`
	ClientName  string        `json:"client_name"`
	ServiceID   string        `json:"service_id,omitempty"`
	ServiceName string        `json:"service_name"`
	At          time.Time     `json:"at"`
	DurationMin int           `json:"duration_min"`
	Status      BookingStatus `json:"status"`
	Notes       string        `json:"notes,omitempty"`
}

type Slot struct {
	ID        string    `json:"id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	ServiceID string    `json:"service_id,omitempty"`
}

type Conflict struct {
	BookingID   string    `json:"booking_id,omitempty"`
	ClientName  string    `json:"client_name"`
	ServiceName string    `json:"service_name,omitempty"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason,omitempty"`
}

type AvailabilityChange struct {
	AddedSlots   int `json:"added_slots"`
	RemovedSlots int `json:"removed_slots"`
}

type AvailabilityExecutionPayload struct {
	CreateSlots   []Slot   `json:"create_slots,omitempty"`
	DeleteSlotIDs []string `json:"delete_slot_ids,omitempty"`
}

type Preview struct {
	Summary            string                        `json:"summary"`
	AvailabilityChange AvailabilityChange            `json:"availability_change"`
	AvailabilityExec   *AvailabilityExecutionPayload `json:"availability_exec,omitempty"`
	Conflicts          []Conflict                    `json:"conflicts,omitempty"`
	ProposedSlots      []Slot                        `json:"proposed_slots,omitempty"`
	BookingResult      *Booking                      `json:"booking_result,omitempty"`
	RiskLevel          RiskLevel                     `json:"risk_level"`
}

type BookingFilter struct {
	From      *time.Time `json:"from,omitempty"`
	To        *time.Time `json:"to,omitempty"`
	Status    string     `json:"status,omitempty"`
	Direction string     `json:"direction,omitempty"`
	Cursor    string     `json:"cursor,omitempty"`
	Limit     int        `json:"limit,omitempty"`
}

type SlotSearchRequest struct {
	ServiceID  string    `json:"service_id"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	MaxResults int       `json:"max_results"`
}

type ProviderInfo struct {
	ProviderID string    `json:"provider_id"`
	Timezone   string    `json:"timezone"`
	Services   []Service `json:"services,omitempty"`
}

type Service struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DurationMin int    `json:"duration_min"`
	IsActive    bool   `json:"is_active"`
}

type DateRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type TimeRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

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
}
