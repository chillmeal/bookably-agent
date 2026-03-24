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
	understood := strings.TrimSpace(preview.Summary)
	if understood == "" {
		understood = "Запрос на изменение расписания по интервалам."
	}
	willDo := "Подготовил изменения расписания и проверил риски до применения."
	var action strings.Builder
	action.WriteString(fmt.Sprintf("+ %d новых слотов\n", preview.AvailabilityChange.AddedSlots))
	action.WriteString(fmt.Sprintf("- %d существующих слотов удалятся", preview.AvailabilityChange.RemovedSlots))
	if len(preview.Conflicts) > 0 {
		action.WriteString("\n⚠️ Конфликты:")
		for _, c := range preview.Conflicts {
			line := fmt.Sprintf("\n• %s — %s в %s",
				fallbackValue(c.ClientName, "клиент"),
				fallbackValue(c.ServiceName, "услуга"),
				c.At.Local().Format("02.01 15:04"),
			)
			action.WriteString(line)
		}
	}
	next := "Проверь превью и нажми «✅ Применить» или «❌ Отменить»."
	return buildStructuredResponse(understood, willDo, action.String(), next)
}

func FormatBookingListPreview(bookings []domain.Booking, tz *time.Location) string {
	if len(bookings) == 0 {
		return buildStructuredResponse(
			"Запрос на список записей.",
			"Проверил календарь на выбранный период.",
			"Записей в этом диапазоне нет.",
			"Можешь запросить другой период, например: «покажи записи на завтра».",
		)
	}

	sorted := make([]domain.Booking, len(bookings))
	copy(sorted, bookings)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	loc := tz
	if loc == nil {
		loc = time.UTC
	}

	var action strings.Builder
	for _, booking := range sorted {
		at := booking.At.In(loc).Format("15:04")
		line := fmt.Sprintf("%s — %s · %s", at, fallbackValue(booking.ClientName, "Клиент"), fallbackValue(booking.ServiceName, "Услуга"))
		action.WriteString(line)
		action.WriteString("\n")
	}

	return buildStructuredResponse(
		"Запрос на список записей.",
		"Отсортировал записи по времени.",
		strings.TrimSpace(action.String()),
		"Если нужно, задай точный день или диапазон.",
	)
}

func FormatCancelPreview(preview domain.Preview) string {
	if preview.BookingResult == nil {
		return buildStructuredResponse(
			"Запрос на отмену записи.",
			"Проверил совпадения по параметрам.",
			"Не удалось однозначно определить запись для отмены.",
			"Уточни клиента и время записи.",
		)
	}

	bk := preview.BookingResult
	return buildStructuredResponse(
		"Запрос на отмену записи.",
		"Нашёл запись для отмены и проверил параметры.",
		fmt.Sprintf("%s · %s · %s\n⚠️ Это действие необратимо.", fallbackValue(bk.ClientName, "Клиент"), fallbackValue(bk.ServiceName, "Услуга"), bk.At.Local().Format("02.01 15:04")),
		"Подтверди отмену кнопкой «✅ Применить» или отмени действие.",
	)
}

func FormatCreatePreview(preview domain.Preview, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(preview.ProposedSlots) == 0 {
		return buildStructuredResponse(
			"Запрос на создание записи.",
			"Проверил доступные интервалы у специалиста.",
			"Свободных интервалов в текущем окне не найдено.",
			"Попробуй расширить диапазон времени или дату.",
		)
	}

	max := len(preview.ProposedSlots)
	if max > 2 {
		max = 2
	}

	var action strings.Builder
	for i := 0; i < max; i++ {
		slot := preview.ProposedSlots[i]
		action.WriteString(slot.Start.In(loc).Format("15:04"))
		if i == 0 {
			action.WriteString(" — основной вариант")
		}
		action.WriteString("\n")
	}
	return buildStructuredResponse(
		"Запрос на создание записи.",
		"Нашёл ближайшие доступные интервалы.",
		strings.TrimSpace(action.String()),
		"Выбери вариант кнопкой ниже, затем подтверди создание.",
	)
}

func FormatFindSlotResult(slots []domain.Slot, tz *time.Location) string {
	loc := tz
	if loc == nil {
		loc = time.UTC
	}
	if len(slots) == 0 {
		return buildStructuredResponse(
			"Запрос на поиск ближайшего окна.",
			"Проверил доступность по выбранной услуге.",
			"Свободных интервалов не найдено.",
			"Попробуй другую дату или более широкий диапазон.",
		)
	}

	max := len(slots)
	if max > 2 {
		max = 2
	}

	var action strings.Builder
	for i := 0; i < max; i++ {
		slot := slots[i]
		action.WriteString(fmt.Sprintf("%d. %s\n", i+1, slot.Start.In(loc).Format("02.01 15:04")))
	}
	return buildStructuredResponse(
		"Запрос на поиск ближайшего окна.",
		"Подобрал ближайшие доступные интервалы.",
		strings.TrimSpace(action.String()),
		"Выбери подходящий вариант кнопкой ниже.",
	)
}

func FormatClarification(q string) string {
	if strings.TrimSpace(q) == "" {
		q = "Уточни, пожалуйста, запрос."
	}
	return buildStructuredResponse(
		"Нужна одна уточняющая деталь.",
		"После ответа сразу продолжу выполнение запроса.",
		strings.TrimSpace(q),
		"Ответь одним коротким сообщением.",
	)
}

func FormatError(errType string) string {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "not_found":
		return buildStructuredResponse(
			"Не удалось найти данные по запросу.",
			"Проверил доступные записи и интервалы.",
			"Совпадений не найдено.",
			"Уточни клиента, услугу или дату и повтори запрос.",
		)
	case "conflict":
		return buildStructuredResponse(
			"Найдён конфликт данных.",
			"Остановил выполнение до ручного подтверждения.",
			"Изменения могут затронуть уже занятые интервалы.",
			"Проверь детали в превью и повтори действие.",
		)
	case "timeout":
		return buildStructuredResponse(
			"Операция заняла слишком много времени.",
			"Текущая попытка остановлена без изменений.",
			"Команда не выполнена.",
			"Повтори запрос. Примеры: «Покажи записи на завтра», «Закрой пятницу», «Отмени запись Ивана в четверг».",
		)
	case "forbidden":
		return buildStructuredResponse(
			"Недостаточно прав для выполнения операции.",
			"Проверил доступы текущего аккаунта.",
			"Операция разрешена только активному специалисту.",
			"Проверь роль аккаунта и попробуй снова.",
		)
	case "upstream":
		return buildStructuredResponse(
			"Внешний сервис временно недоступен.",
			"Остановил выполнение, чтобы избежать дублирования.",
			"Изменения не применены.",
			"Подожди 10–20 секунд и повтори запрос.",
		)
	case "validation":
		return buildStructuredResponse(
			"В запросе не хватает обязательных данных.",
			"Проверил параметры перед выполнением.",
			"Команда отклонена до уточнения.",
			"Уточни дату, время или услугу и повтори запрос.",
		)
	default:
		return buildStructuredResponse(
			"Не удалось обработать запрос.",
			"Система не смогла безопасно продолжить выполнение.",
			"Изменения не применены.",
			"Повтори попытку или уточни формулировку команды.",
		)
	}
}

func FormatUnknownIntent() string {
	return buildStructuredResponse(
		"Команда вне поддерживаемых сценариев.",
		"Могу помочь с расписанием и записями.",
		"Примеры:\n• Покажи записи на завтра\n• Закрой пятницу\n• Запиши Алину на массаж",
		"Отправь одну из команд в похожем формате.",
	)
}

func fallbackValue(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func buildStructuredResponse(understood, willDo, action, next string) string {
	sections := []struct {
		title string
		body  string
	}{
		{title: "Понял", body: understood},
		{title: "Что сделаю", body: willDo},
		{title: "Действие", body: action},
		{title: "Что дальше", body: next},
	}

	var b strings.Builder
	for _, section := range sections {
		body := strings.TrimSpace(section.body)
		if body == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("*")
		b.WriteString(section.title)
		b.WriteString(":*\n")
		b.WriteString(escapeV2(body))
	}
	return strings.TrimSpace(b.String())
}
