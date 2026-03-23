package acp

import (
	"errors"
	"fmt"
	"strings"
)

type CommitCreateItem struct {
	Date      string `json:"date"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type CommitDeleteItem struct {
	SlotID string `json:"slotId"`
}

type CommitScheduleBody struct {
	Create []CommitCreateItem `json:"create,omitempty"`
	Delete []CommitDeleteItem `json:"delete,omitempty"`
}

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

func BuildAvailabilityRun(baseURL, accessToken string, create []CommitCreateItem, deleteSlotIDs []string, baseIdempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(baseIdempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}
	if len(create) == 0 && len(deleteSlotIDs) == 0 {
		return nil, errors.New("acp builder: at least one availability operation is required")
	}

	trimmedBaseURL := strings.TrimRight(baseURL, "/")
	body := CommitScheduleBody{
		Create: make([]CommitCreateItem, 0, len(create)),
		Delete: make([]CommitDeleteItem, 0, len(deleteSlotIDs)),
	}
	for idx, item := range create {
		if strings.TrimSpace(item.Date) == "" {
			return nil, fmt.Errorf("acp builder: create item %d has empty date", idx)
		}
		if strings.TrimSpace(item.StartTime) == "" || strings.TrimSpace(item.EndTime) == "" {
			return nil, fmt.Errorf("acp builder: create item %d has empty time bounds", idx)
		}
		body.Create = append(body.Create, CommitCreateItem{
			Date:      strings.TrimSpace(item.Date),
			StartTime: strings.TrimSpace(item.StartTime),
			EndTime:   strings.TrimSpace(item.EndTime),
		})
	}
	for idx, slotID := range deleteSlotIDs {
		slotID = strings.TrimSpace(slotID)
		if slotID == "" {
			return nil, fmt.Errorf("acp builder: delete slot id %d is empty", idx)
		}
		body.Delete = append(body.Delete, CommitDeleteItem{SlotID: slotID})
	}

	return &ACPRun{
		IdempotencyKey: strings.TrimSpace(baseIdempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps: []ACPStep{{
			Type:       "http",
			Capability: "availability.commit",
			Config: ACPStepConfig{
				Method: "POST",
				URL:    trimmedBaseURL + "/api/v1/specialist/schedule/commit",
				Headers: map[string]string{
					"Authorization":   bearer(accessToken),
					"Content-Type":    "application/json",
					"Idempotency-Key": fmt.Sprintf("%s:commit", strings.TrimSpace(baseIdempotencyKey)),
				},
				Body: body,
			},
		}},
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
