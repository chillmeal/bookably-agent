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
	understood := strings.TrimSpace(preview.Summary)
	if understood == "" {
		understood = "Ок, меняем расписание по интервалам."
	}
	willDo := "Я ничего не применяю сразу, сначала показываю изменения и жду подтверждение."
	var action strings.Builder
	action.WriteString(fmt.Sprintf("• Добавится: %s\n", boldText(fmt.Sprintf("%d", preview.AvailabilityChange.AddedSlots))))
	action.WriteString(fmt.Sprintf("• Удалится: %s", boldText(fmt.Sprintf("%d", preview.AvailabilityChange.RemovedSlots))))
	if len(preview.Conflicts) > 0 {
		action.WriteString("\n\n⚠️ Конфликты:")
		for _, c := range preview.Conflicts {
			line := fmt.Sprintf("\n• %s — %s в %s",
				fallbackValue(c.ClientName, "клиент"),
				fallbackValue(c.ServiceName, "услуга"),
				c.At.Local().Format("02.01 15:04"),
			)
			action.WriteString(line)
		}
	}
	next := "Если всё верно, жми ✅ Применить. Если нужно скорректировать, жми ❌ Отменить."
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
		"Понял, нужен список записей.",
		"Собрал и отсортировал записи по времени.",
		strings.TrimSpace(action.String()),
		"Можешь уточнить период: например «на пятницу» или «на эту неделю».",
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
		"Ок, готовлю отмену записи.",
		"Нашёл нужную запись и проверил детали.",
		fmt.Sprintf("• Клиент: %s\n• Услуга: %s\n• Время: %s\n⚠️ Это действие необратимо.",
			boldText(fallbackValue(bk.ClientName, "Клиент")),
			boldText(fallbackValue(bk.ServiceName, "Услуга")),
			boldText(bk.At.Local().Format("02.01 15:04")),
		),
		"Подтверди отмену кнопкой ✅ Применить или отмени действие кнопкой ❌ Отменить.",
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
		"Понял, создаём запись.",
		"Нашёл ближайшие свободные интервалы.",
		strings.TrimSpace(action.String()),
		"Выбери вариант кнопкой ниже, потом подтверди создание.",
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
		"Понял, ищем ближайшее окно.",
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
		"Нужна одна короткая деталь.",
		"Сразу продолжу после твоего ответа.",
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
			"Похоже, сервис ответил слишком медленно.",
			"Остановил попытку без изменений.",
			"Команда пока не выполнена.",
			"Примеры: «Покажи записи на завтра», «Закрой пятницу», «Отмени запись Ивана в четверг».",
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
			"Внешний сервис сейчас отвечает нестабильно.",
			"Чтобы не сделать дубликаты, остановил выполнение.",
			"Изменения не применены.",
			"Подожди 10–20 секунд и повтори команду.",
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
		"Похоже, команда вне моего сценария.",
		"Я помогаю только с расписанием и записями.",
		"Примеры:\n• Покажи записи на завтра\n• Закрой пятницу\n• Запиши Алину на массаж",
		"Напиши команду в похожем формате, и сразу продолжим.",
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
		b.WriteString(renderBody(body))
	}
	return strings.TrimSpace(b.String())
}

func boldText(value string) string {
	return boldOpenMark + strings.TrimSpace(value) + boldCloseMark
}

func renderBody(body string) string {
	escaped := escapeV2(body)
	escaped = strings.ReplaceAll(escaped, boldOpenMark, "*")
	escaped = strings.ReplaceAll(escaped, boldCloseMark, "*")
	return escaped
}
