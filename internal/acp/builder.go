package acp

import (
	"errors"
	"fmt"
	"strconv"
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

type CommitAvailabilityRange struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type CommitAvailabilityItem struct {
	Date   string                    `json:"date"`
	Ranges []CommitAvailabilityRange `json:"ranges"`
}

type CommitScheduleBody struct {
	Create       []CommitCreateItem       `json:"create,omitempty"`
	Delete       []CommitDeleteItem       `json:"delete,omitempty"`
	Availability []CommitAvailabilityItem `json:"availability,omitempty"`
}

func BuildCancelBookingRun(baseURL, botServiceKey string, telegramUserID int64, bookingID, idempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(bookingID) == "" {
		return nil, errors.New("acp builder: booking id is required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}
	headers, err := buildBotAuthHeaders(botServiceKey, telegramUserID)
	if err != nil {
		return nil, err
	}
	headers["Idempotency-Key"] = strings.TrimSpace(idempotencyKey)

	urlValue := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/api/v1/specialist/bookings/%s/cancel", strings.TrimSpace(bookingID))
	run := &ACPRun{
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps: []ACPStep{{
			Type:       "http",
			Capability: "booking.cancel",
			Config: ACPStepConfig{
				Method:  "POST",
				URL:     urlValue,
				Headers: headers,
			},
		}},
	}
	return run, nil
}

func BuildCreateBookingRun(baseURL, botServiceKey string, telegramUserID int64, serviceID, slotID, idempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(serviceID) == "" || strings.TrimSpace(slotID) == "" {
		return nil, errors.New("acp builder: service id and slot id are required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}
	headers, err := buildBotAuthHeaders(botServiceKey, telegramUserID)
	if err != nil {
		return nil, err
	}
	headers["Content-Type"] = "application/json"
	headers["Idempotency-Key"] = strings.TrimSpace(idempotencyKey)

	urlValue := strings.TrimRight(baseURL, "/") + "/api/v1/public/bookings"
	run := &ACPRun{
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Metadata:       buildMetadata(meta),
		Steps: []ACPStep{{
			Type:       "http",
			Capability: "booking.create",
			Config: ACPStepConfig{
				Method:  "POST",
				URL:     urlValue,
				Headers: headers,
				Body: map[string]string{
					"serviceId": strings.TrimSpace(serviceID),
					"slotId":    strings.TrimSpace(slotID),
				},
			},
		}},
	}
	return run, nil
}

func BuildAvailabilityRun(baseURL, botServiceKey string, telegramUserID int64, create []CommitCreateItem, deleteSlotIDs []string, baseIdempotencyKey string, meta RunMetadata) (*ACPRun, error) {
	if strings.TrimSpace(baseIdempotencyKey) == "" {
		return nil, errors.New("acp builder: idempotency key is required")
	}
	if len(create) == 0 && len(deleteSlotIDs) == 0 {
		return nil, errors.New("acp builder: at least one availability operation is required")
	}
	headers, err := buildBotAuthHeaders(botServiceKey, telegramUserID)
	if err != nil {
		return nil, err
	}
	headers["Content-Type"] = "application/json"
	headers["Idempotency-Key"] = fmt.Sprintf("%s:commit", strings.TrimSpace(baseIdempotencyKey))

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
				Method:  "POST",
				URL:     trimmedBaseURL + "/api/v1/specialist/schedule/commit",
				Headers: headers,
				Body:    body,
			},
		}},
	}, nil
}

func buildBotAuthHeaders(botServiceKey string, telegramUserID int64) (map[string]string, error) {
	key := strings.TrimSpace(botServiceKey)
	if key == "" {
		return nil, errors.New("acp builder: bot service key is required")
	}
	if telegramUserID <= 0 {
		return nil, errors.New("acp builder: telegram user id is required")
	}
	return map[string]string{
		"X-Bot-Service-Key":  key,
		"X-Telegram-User-Id": strconv.FormatInt(telegramUserID, 10),
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
