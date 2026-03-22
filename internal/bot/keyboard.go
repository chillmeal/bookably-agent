package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

const (
	callbackPrefixConfirm = "confirm"
	callbackPrefixCancel  = "cancel"
	callbackPrefixSlot    = "slot"

	buttonStyleSuccess = "success"
	buttonStyleDanger  = "danger"
)

type CallbackType string

const (
	CallbackTypeConfirm CallbackType = callbackPrefixConfirm
	CallbackTypeCancel  CallbackType = callbackPrefixCancel
	CallbackTypeSlot    CallbackType = callbackPrefixSlot
)

type ParsedCallback struct {
	Type      CallbackType
	PlanID    string
	SlotIndex int
}

type WebAppInfo struct {
	URL string `json:"url"`
}

type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
	Style        string      `json:"style,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

func ConfirmData(planID string) string {
	return fmt.Sprintf("%s:%s", callbackPrefixConfirm, strings.TrimSpace(planID))
}

func CancelData(planID string) string {
	return fmt.Sprintf("%s:%s", callbackPrefixCancel, strings.TrimSpace(planID))
}

func SlotData(idx int, planID string) string {
	return fmt.Sprintf("%s:%d:%s", callbackPrefixSlot, idx, strings.TrimSpace(planID))
}

func ParseCallback(data string) (*ParsedCallback, error) {
	raw := strings.TrimSpace(data)
	if raw == "" {
		return nil, errors.New("bot callback: empty callback data")
	}

	parts := strings.Split(raw, ":")
	switch parts[0] {
	case callbackPrefixConfirm, callbackPrefixCancel:
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("bot callback: invalid %s format", parts[0])
		}
		parsed := &ParsedCallback{
			Type:   CallbackType(parts[0]),
			PlanID: strings.TrimSpace(parts[1]),
		}
		return parsed, nil
	case callbackPrefixSlot:
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, errors.New("bot callback: invalid slot format")
		}
		idx, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || idx < 0 {
			return nil, errors.New("bot callback: slot index must be non-negative integer")
		}
		return &ParsedCallback{
			Type:      CallbackTypeSlot,
			SlotIndex: idx,
			PlanID:    strings.TrimSpace(parts[2]),
		}, nil
	default:
		return nil, fmt.Errorf("bot callback: unknown prefix %q", parts[0])
	}
}

func BuildPreviewKeyboard(planID string) InlineKeyboardMarkup {
	return InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{
					Text:         "✅ Применить",
					CallbackData: ConfirmData(planID),
					Style:        buttonStyleSuccess,
				},
				{
					Text:         "❌ Отменить",
					CallbackData: CancelData(planID),
					Style:        buttonStyleDanger,
				},
			},
		},
	}
}

func BuildSlotKeyboard(planID string, slots []domain.Slot, tz *time.Location) InlineKeyboardMarkup {
	location := tz
	if location == nil {
		location = time.UTC
	}

	keyboard := make([][]InlineKeyboardButton, 0, 2)
	slotButtons := make([]InlineKeyboardButton, 0, 2)

	max := len(slots)
	if max > 2 {
		max = 2
	}
	for i := 0; i < max; i++ {
		slotButtons = append(slotButtons, InlineKeyboardButton{
			Text:         formatSlotLabel(slots[i].Start, location),
			CallbackData: SlotData(i, planID),
			Style:        buttonStyleSuccess,
		})
	}
	if len(slotButtons) > 0 {
		keyboard = append(keyboard, slotButtons)
	}

	keyboard = append(keyboard, []InlineKeyboardButton{{
		Text:         "❌ Отменить",
		CallbackData: CancelData(planID),
		Style:        buttonStyleDanger,
	}})

	return InlineKeyboardMarkup{InlineKeyboard: keyboard}
}

func StripKeyboardStyles(markup *InlineKeyboardMarkup) *InlineKeyboardMarkup {
	if markup == nil {
		return nil
	}

	cloned := &InlineKeyboardMarkup{
		InlineKeyboard: make([][]InlineKeyboardButton, len(markup.InlineKeyboard)),
	}
	for i := range markup.InlineKeyboard {
		row := markup.InlineKeyboard[i]
		clonedRow := make([]InlineKeyboardButton, len(row))
		for j := range row {
			btn := row[j]
			btn.Style = ""
			clonedRow[j] = btn
		}
		cloned.InlineKeyboard[i] = clonedRow
	}

	return cloned
}

func NewWebAppButton(text, webAppURL string) InlineKeyboardButton {
	return InlineKeyboardButton{
		Text:   text,
		WebApp: &WebAppInfo{URL: strings.TrimSpace(webAppURL)},
	}
}

func markupHasStyle(markup *InlineKeyboardMarkup) bool {
	if markup == nil {
		return false
	}
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if strings.TrimSpace(btn.Style) != "" {
				return true
			}
		}
	}
	return false
}

func formatSlotLabel(ts time.Time, tz *time.Location) string {
	now := time.Now().In(tz)
	local := ts.In(tz)
	if sameDate(local, now) {
		return fmt.Sprintf("Сегодня %s", local.Format("15:04"))
	}

	months := []string{
		"",
		"января",
		"февраля",
		"марта",
		"апреля",
		"мая",
		"июня",
		"июля",
		"августа",
		"сентября",
		"октября",
		"ноября",
		"декабря",
	}
	month := months[int(local.Month())]
	return fmt.Sprintf("%02d %s %s", local.Day(), month, local.Format("15:04"))
}

func sameDate(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
