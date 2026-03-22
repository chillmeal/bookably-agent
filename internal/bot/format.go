package bot

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

var markdownV2Escapes = map[rune]struct{}{
	'_': {},
	'*': {},
	'[': {},
	']': {},
	'(': {},
	')': {},
	'~': {},
	'`': {},
	'>': {},
	'#': {},
	'+': {},
	'-': {},
	'=': {},
	'|': {},
	'{': {},
	'}': {},
	'.': {},
	'!': {},
}

func escapeV2(in string) string {
	var builder strings.Builder
	for _, r := range in {
		if _, ok := markdownV2Escapes[r]; ok {
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func FormatAvailabilityPreview(preview domain.Preview) string {
	var b strings.Builder
	b.WriteString("*Понял так:*\n")
	if strings.TrimSpace(preview.Summary) != "" {
		b.WriteString("  ")
		b.WriteString(escapeV2(preview.Summary))
		b.WriteString("\n")
	}

	b.WriteString("\n*Что изменится:*\n")
	b.WriteString(fmt.Sprintf("  \\+ %d новых слотов\n", preview.AvailabilityChange.AddedSlots))
	b.WriteString(fmt.Sprintf("  \\- %d существующих слотов удалятся\n", preview.AvailabilityChange.RemovedSlots))

	if len(preview.Conflicts) > 0 {
		b.WriteString("\n⚠️ *Конфликты:*\n")
		for _, c := range preview.Conflicts {
			line := fmt.Sprintf("  • %s — %s в %s",
				escapeV2(c.ClientName),
				escapeV2(fallbackValue(c.ServiceName, "услуга")),
				escapeV2(c.At.Local().Format("02.01 15:04")),
			)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func FormatBookingListPreview(bookings []domain.Booking, tz *time.Location) string {
	if len(bookings) == 0 {
		return "На выбранный период записей нет\\."
	}

	sorted := make([]domain.Booking, len(bookings))
	copy(sorted, bookings)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	loc := tz
	if loc == nil {
		loc = time.UTC
	}

	var b strings.Builder
	for _, booking := range sorted {
		at := booking.At.In(loc).Format("15:04")
		line := fmt.Sprintf("%s — %s · %s", at, escapeV2(booking.ClientName), escapeV2(booking.ServiceName))
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func FormatCancelPreview(preview domain.Preview) string {
	if preview.BookingResult == nil {
		return "Не удалось определить запись для отмены\\."
	}

	bk := preview.BookingResult
	return strings.TrimSpace(fmt.Sprintf(
		"*Нашёл запись:*\n  %s · %s · %s\n\n⚠️ *Это действие необратимо\\.*",
		escapeV2(bk.ClientName),
		escapeV2(bk.ServiceName),
		escapeV2(bk.At.Local().Format("02.01 15:04")),
	))
}

func FormatCreatePreview(preview domain.Preview, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(preview.ProposedSlots) == 0 {
		return "Свободных слотов не найдено\\."
	}

	max := len(preview.ProposedSlots)
	if max > 2 {
		max = 2
	}

	var b strings.Builder
	b.WriteString("*Нашёл свободное время:*\n")
	for i := 0; i < max; i++ {
		slot := preview.ProposedSlots[i]
		b.WriteString("  ")
		b.WriteString(escapeV2(slot.Start.In(loc).Format("15:04")))
		if i == 0 {
			b.WriteString(" — основной вариант")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func FormatFindSlotResult(slots []domain.Slot, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(slots) == 0 {
		return "Свободных окон не найдено\\."
	}

	max := len(slots)
	if max > 2 {
		max = 2
	}

	var b strings.Builder
	b.WriteString("*Ближайшие свободные окна:*\n")
	for i := 0; i < max; i++ {
		slot := slots[i]
		b.WriteString(fmt.Sprintf("%d\\. %s\n", i+1, escapeV2(slot.Start.In(loc).Format("02.01 15:04"))))
	}
	return strings.TrimSpace(b.String())
}

func FormatClarification(q string) string {
	if strings.TrimSpace(q) == "" {
		return "Уточни, пожалуйста, запрос\\."
	}
	return escapeV2(strings.TrimSpace(q))
}

func FormatError(errType string) string {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "not_found":
		return "Ничего не найдено\\. Уточни параметры или открой приложение\\."
	case "conflict":
		return "Обнаружен конфликт данных\\. Проверь детали и попробуй снова\\."
	case "timeout":
		return "Операция заняла слишком много времени\\. Повтори ещё раз или открой приложение\\."
	case "upstream":
		return "Сервис временно недоступен\\. Попробуй ещё раз или открой приложение\\."
	default:
		return "Не удалось обработать запрос\\. Повтори попытку или открой приложение\\."
	}
}

func FormatUnknownIntent() string {
	return strings.TrimSpace(
		"Привет\\! Я помогаю управлять расписанием и записями\\.\n" +
			"Попробуй:\n" +
			"• «Покажи записи на завтра»\n" +
			"• «Закрой пятницу»\n" +
			"• «Запиши Алину на массаж»",
	)
}

func fallbackValue(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
