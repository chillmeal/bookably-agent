package acp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

// Availability write builder remains a provisional path and is currently not used at runtime:
// bot executor blocks availability execution until commit-operation migration is implemented.

func BuildCancelBookingRun(baseURL, accessToken, bookingID, idempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(bookingID) == "" {
		return nil, errors.New("acp builder: booking id is required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}

	urlValue := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/api/v1/specialist/bookings/%s/cancel", strings.TrimSpace(bookingID))
	run := &ACPRun{
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps: []ACPStep{{
			Type:       "http",
			Capability: "booking.cancel",
			Config: ACPStepConfig{
				Method: "POST",
				URL:    urlValue,
				Headers: map[string]string{
					"Authorization":   bearer(accessToken),
					"Idempotency-Key": strings.TrimSpace(idempotencyKey),
				},
			},
		}},
	}
	return run, nil
}

func BuildCreateBookingRun(baseURL, accessToken, serviceID, slotID, idempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(serviceID) == "" || strings.TrimSpace(slotID) == "" {
		return nil, errors.New("acp builder: service id and slot id are required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}

	urlValue := strings.TrimRight(baseURL, "/") + "/api/v1/public/bookings"
	run := &ACPRun{
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps: []ACPStep{{
			Type:       "http",
			Capability: "booking.create",
			Config: ACPStepConfig{
				Method: "POST",
				URL:    urlValue,
				Headers: map[string]string{
					"Authorization":   bearer(accessToken),
					"Content-Type":    "application/json",
					"Idempotency-Key": strings.TrimSpace(idempotencyKey),
				},
				Body: map[string]string{
					"serviceId": strings.TrimSpace(serviceID),
					"slotId":    strings.TrimSpace(slotID),
				},
			},
		}},
	}
	return run, nil
}

func BuildAvailabilityRun(baseURL, accessToken string, slots []domain.Slot, baseIdempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(baseIdempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}

	trimmedBaseURL := strings.TrimRight(baseURL, "/")
	steps := make([]ACPStep, 0, len(slots)+1)

	for idx, slot := range slots {
		if strings.TrimSpace(slot.ID) == "" {
			return nil, fmt.Errorf("acp builder: slot index %d has empty id", idx)
		}

		key := fmt.Sprintf("%s:del:%d", strings.TrimSpace(baseIdempotencyKey), idx)
		steps = append(steps, ACPStep{
			Type:       "http",
			Capability: "availability.delete_slot",
			Config: ACPStepConfig{
				Method: "DELETE",
				URL:    fmt.Sprintf("%s/api/v1/specialist/slots/%s", trimmedBaseURL, strings.TrimSpace(slot.ID)),
				Headers: map[string]string{
					"Authorization":   bearer(accessToken),
					"Idempotency-Key": key,
				},
			},
		})
	}

	commitKey := fmt.Sprintf("%s:commit", strings.TrimSpace(baseIdempotencyKey))
	steps = append(steps, ACPStep{
		Type:       "http",
		Capability: "availability.commit",
		Config: ACPStepConfig{
			Method: "POST",
			URL:    trimmedBaseURL + "/api/v1/specialist/schedule/commit",
			Headers: map[string]string{
				"Authorization":   bearer(accessToken),
				"Idempotency-Key": commitKey,
			},
		},
	})

	return &ACPRun{
		IdempotencyKey: strings.TrimSpace(baseIdempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps:          steps,
	}, nil
}

func buildMetadata(meta RunMetadata) map[string]string {
	return map[string]string{
		"chat_id":       strings.TrimSpace(meta.ChatID),
		"specialist_id": strings.TrimSpace(meta.SpecialistID),
		"intent":        strings.TrimSpace(meta.Intent),
		"risk_level":    strings.TrimSpace(meta.RiskLevel),
		"raw_message":   strings.TrimSpace(meta.RawMessage),
	}
}

func bearer(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	return "Bearer " + trimmed
}
