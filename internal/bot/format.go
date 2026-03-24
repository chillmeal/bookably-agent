package bot

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

const (
	boldOpenMark  = "§B§"
	boldCloseMark = "§/B§"
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
	lead := strings.TrimSpace(preview.Summary)
	if lead == "" {
		lead = "Проверил изменения по расписанию"
	}
	b.WriteString(boldLead(lead))
	b.WriteString("• Добавится: ")
	b.WriteString(boldText(fmt.Sprintf("%d", preview.AvailabilityChange.AddedSlots)))
	b.WriteString("\n")
	b.WriteString("• Удалится: ")
	b.WriteString(boldText(fmt.Sprintf("%d", preview.AvailabilityChange.RemovedSlots)))

	if len(preview.Conflicts) > 0 {
		b.WriteString("\n\n⚠️ Есть пересечения с записями:\n")
		for _, c := range preview.Conflicts {
			b.WriteString("• ")
			b.WriteString(boldText(fallbackValue(c.ClientName, "Клиент")))
			b.WriteString(" — ")
			b.WriteString(escapeV2(fallbackValue(c.ServiceName, "услуга")))
			b.WriteString(", ")
			b.WriteString(boldText(humanDateTime(c.At, time.UTC)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n\nЕсли всё верно, жми ✅ Применить. Если нужно изменить параметры, жми ❌ Отменить.")
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatBookingListPreview(bookings []domain.Booking, tz *time.Location) string {
	if len(bookings) == 0 {
		return renderBody(boldLead("На выбранный период записей нет") + "Попробуй уточнить дату или диапазон.")
	}

	loc := tz
	if loc == nil {
		loc = time.UTC
	}

	sorted := make([]domain.Booking, len(bookings))
	copy(sorted, bookings)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	var b strings.Builder
	b.WriteString(boldLead("Вот что нашёл"))
	for _, booking := range sorted {
		b.WriteString("• ")
		b.WriteString(boldText(humanDateTime(booking.At, loc)))
		b.WriteString(" — ")
		b.WriteString(boldText(fallbackValue(booking.ClientName, "Клиент")))
		b.WriteString(" · ")
		b.WriteString(escapeV2(fallbackValue(booking.ServiceName, "Услуга")))
		b.WriteString("\n")
	}
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatCancelPreview(preview domain.Preview, tz *time.Location) string {
	if preview.BookingResult == nil {
		return renderBody(boldLead("Не получилось однозначно определить запись для отмены") + "Уточни клиента или время.")
	}
	loc := tz
	if loc == nil {
		loc = time.UTC
	}

	bk := preview.BookingResult
	var b strings.Builder
	b.WriteString(boldLead("Нашёл запись для отмены"))
	b.WriteString("• Клиент: ")
	b.WriteString(boldText(fallbackValue(bk.ClientName, "Клиент")))
	b.WriteString("\n")
	b.WriteString("• Услуга: ")
	b.WriteString(boldText(fallbackValue(bk.ServiceName, "Услуга")))
	b.WriteString("\n")
	b.WriteString("• Время: ")
	b.WriteString(boldText(humanDateTime(bk.At, loc)))
	b.WriteString("\n\n⚠️ Отмена необратима.")
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatCancelCandidates(candidates []domain.Booking, tz *time.Location) string {
	if len(candidates) == 0 {
		return renderBody(boldLead("По указанным данным записи не нашёл") + "Попробуй уточнить клиента или время.")
	}
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	max := len(candidates)
	if max > 3 {
		max = 3
	}

	var b strings.Builder
	b.WriteString(boldLead("Нашёл несколько подходящих записей"))
	b.WriteString("Выбери нужную кнопкой ниже:\n")
	for i := 0; i < max; i++ {
		item := candidates[i]
		b.WriteString("• ")
		b.WriteString(boldText(fallbackValue(item.ClientName, "Клиент")))
		b.WriteString(" — ")
		b.WriteString(escapeV2(fallbackValue(item.ServiceName, "Услуга")))
		b.WriteString(", ")
		b.WriteString(boldText(humanDateTime(item.At, loc)))
		b.WriteString("\n")
	}
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatCreatePreview(preview domain.Preview, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(preview.ProposedSlots) == 0 {
		return renderBody(boldLead("Свободных окон в текущем диапазоне нет") + "Попробуй другую дату или более широкий диапазон.")
	}

	max := len(preview.ProposedSlots)
	if max > 2 {
		max = 2
	}
	var b strings.Builder
	b.WriteString(boldLead("Нашёл ближайшие свободные окна"))
	for i := 0; i < max; i++ {
		slot := preview.ProposedSlots[i]
		b.WriteString("• ")
		b.WriteString(boldText(humanDateTime(slot.Start, loc)))
		if i == 0 {
			b.WriteString(" (основной вариант)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nВыбери вариант кнопкой ниже, затем подтверди создание записи.")
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatFindSlotResult(slots []domain.Slot, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(slots) == 0 {
		return renderBody(boldLead("Пока не вижу свободных окон по этим условиям") + "Попробуй расширить диапазон времени.")
	}

	max := len(slots)
	if max > 2 {
		max = 2
	}
	var b strings.Builder
	b.WriteString(boldLead("Ближайшие варианты"))
	for i := 0; i < max; i++ {
		slot := slots[i]
		b.WriteString(fmt.Sprintf("• %d) %s\n", i+1, boldText(humanDateTime(slot.Start, loc))))
	}
	b.WriteString("\nВыбери подходящий вариант кнопкой ниже.")
	return renderBody(strings.TrimSpace(b.String()))
}

func FormatClarification(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		q = "Уточни, пожалуйста, детали запроса."
	}
	return renderBody(boldLead("Нужна одна деталь, чтобы продолжить") + "• " + q)
}

func FormatError(errType string) string {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "not_found":
		return renderBody(boldLead("Не нашёл подходящих данных по этому запросу") + "Уточни клиента, услугу или дату.")
	case "conflict":
		return renderBody(boldLead("Нашёл несколько совпадений, и без выбора можно ошибиться") + "Уточни клиента или время записи.")
	case "timeout":
		return renderBody(boldLead("Сервис ответил слишком медленно, поэтому остановил попытку без изменений") + "Примеры: «Покажи записи на завтра», «Закрой пятницу», «Отмени запись Ивана в четверг».")
	case "forbidden":
		return renderBody(boldLead("Недостаточно прав для этой операции") + "Проверь, что аккаунт активирован как специалист.")
	case "upstream":
		return renderBody(boldLead("Внешний сервис сейчас отвечает нестабильно") + "Подожди 10–20 секунд и повтори команду.")
	case "validation":
		return renderBody(boldLead("Не хватает обязательных деталей для выполнения команды") + "Уточни дату, время или услугу.")
	default:
		return renderBody(boldLead("Не удалось обработать запрос") + "Повтори попытку или уточни формулировку.")
	}
}

func FormatUnknownIntent() string {
	return renderBody(boldLead("Пока не понял команду") + "Попробуй так:\n• Покажи записи на завтра\n• Закрой пятницу\n• Запиши Алину на массаж")
}

func fallbackValue(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func renderBody(body string) string {
	escaped := escapeV2(body)
	escaped = strings.ReplaceAll(escaped, boldOpenMark, "*")
	escaped = strings.ReplaceAll(escaped, boldCloseMark, "*")
	return escaped
}

func boldText(value string) string {
	return boldOpenMark + strings.TrimSpace(value) + boldCloseMark
}

func boldLead(value string) string {
	return boldText(value) + "\n\n"
}

func humanDateTime(ts time.Time, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	local := ts.In(loc)
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
	return fmt.Sprintf("%d %s, %s", local.Day(), month, local.Format("15:04"))
}
