package domain

import "context"

// Provider is the interface between the agent and any booking backend.
// All methods are context-aware and return typed domain errors.
type Provider interface {
	// Read operations (called during preview, no side effects).
	GetBookings(ctx context.Context, providerID string, f BookingFilter) ([]Booking, error)
	FindSlots(ctx context.Context, providerID string, req SlotSearchRequest) ([]Slot, error)
	GetProviderInfo(ctx context.Context, providerID string) (*ProviderInfo, error)

	// Preview builders (read-only, compute impact without writing).
	PreviewAvailabilityChange(ctx context.Context, providerID string, p ActionParams) (*Preview, error)
	PreviewBookingCreate(ctx context.Context, providerID string, p ActionParams) (*Preview, error)
	PreviewBookingCancel(ctx context.Context, providerID string, p ActionParams) (*Preview, error)
}
