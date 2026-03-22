# BOOKABLY AGENT

## Use Cases & User Stories

Version 1.0 | March 2026 | Doc 02 of series  
Depends on: [01 - Vision & MVP Scope](./01-vision-mvp-scope.md)

## Purpose Of This Document

This document specifies all MVP use cases for Bookably Agent.

For each of the 7 supported intents, it defines:

- Happy path: ideal conversation flow with no ambiguity and no conflicts.
- Alternative scenarios: clarification loops, conflict handling, cancellation, deep-link escalation, and system errors.
- Acceptance criteria: testable conditions that define "done" for each scenario.

Conversation examples are illustrative. They are not UI copy specifications. Final wording can change in implementation; what is fixed here is semantic content and message structure.

## Notation

- `USER` messages are provider inputs.
- `AGENT` messages are bot outputs (streamed via `sendMessageDraft`).
- Inline keyboard buttons are shown as `[ Button text ]`.

---

## UC-01 - `set_working_hours`

Provider updates working hours for a specific date range and weekdays, optionally with breaks.

| Field | Value |
| --- | --- |
| Trigger | Provider sends free text describing new availability for a time range |
| Actors | Service provider |
| Preconditions | Provider has active workspace in Bookably; agent session is authenticated |
| Postconditions (success) | Working hours updated in Bookably; ACP audit event written; provider sees confirmation |
| Risk level | Medium (may delete existing available slots) |

### Happy path

Scenario: multi-condition availability with break, no conflicts.

**USER**  
`На следующей неделе работаю с 12 до 20, кроме пятницы. Добавь обед 15–16.`

**AGENT**
```text
Понял так:
  Период: пн–чт, 24–27 марта
  Рабочие часы: 12:00–20:00
  Перерыв: 15:00–16:00 ежедневно

Что изменится:
  + 28 новых слотов (по 7 окон в день × 4 дня)
  – удалит 4 ранее открытых слота 24 марта до 12:00

Конфликтов нет.

[ ✅ Применить ] [ ❌ Отменить ]
```

**USER**  
`✅ Применить`

**AGENT**  
`Готово. Рабочие часы на 24–27 марта обновлены.`

### Alternative scenarios

#### Clarification

Scenario: ambiguous date range (`next week`) in ambiguous timezone context.

**USER**  
`Со следующей недели работаю по-другому.`

**AGENT**  
`С какого числа и по какое, и какие часы ставить?`

**USER**  
`С 24 по 28, с 10 до 19.`

**AGENT**
```text
Понял так:
  Период: пн–пт, 24–28 марта
  Рабочие часы: 10:00–19:00
  Перерывов нет

Что изменится:
  + 45 новых слотов
  – нет удалений

[ ✅ Применить ] [ ❌ Отменить ]
```

#### Conflict

Scenario: existing booking falls inside the range being closed.

**USER**  
`Закрой среду 26 марта полностью.`

**AGENT**
```text
Понял так: закрыть всё расписание на среду, 26 марта.

⚠️  Конфликт:
  • Алина Смирнова — массаж 60 мин в 14:00
    Запись будет в закрытом слоте.

Что делать с этой записью?

[ Закрыть и уведомить клиента ] [ Отменить операцию ]
```

**USER**  
`[ Закрыть и уведомить клиента ]`

**AGENT**
```text
Среда 26 марта закрыта.
Запись Алины Смирновой в 14:00 отмечена как конфликтная —
уведомление отправлено через Bookably.
```

#### Cancel

Scenario: provider reviews preview and cancels.

**AGENT**  
`[ preview, as above ]`

**USER**  
`[ ❌ Отменить ]`

**AGENT**  
`Понял, ничего не изменено.`

#### Error

Scenario: Bookably API unavailable during preview build.

**AGENT**
```text
Не удалось получить текущее расписание из Bookably.
Попробуй ещё раз или открой календарь в приложении.
[ Открыть в приложении → ]
```

### Acceptance criteria - UC-01

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | Preview is built without writes to Bookably (read-only) | Mock Bookably write endpoint; assert no calls before confirm |
| 2 | Preview includes exact count of slots to add and remove | Unit test with schedule fixture |
| 3 | Conflicting bookings are listed with client name and time | Test case with 2 conflicts |
| 4 | Confirm starts ACP run; Bookably write occurs through ACP step | Integration test; inspect audit log |
| 5 | Cancel performs no writes | Assert Bookably write count = 0 |
| 6 | On incomplete data, exactly one clarification question is asked (not a list) | Prompt test set with 5 ambiguities |
| 7 | API error response includes Mini App deep link | Mock `500` from Bookably |

---

## UC-02 - `add_break`

Provider adds a recurring break to existing working hours for a date range.

### Happy path

Scenario: simple break added to current week.

**USER**  
`Добавь обед 13–14 на всю эту неделю.`

**AGENT**
```text
Понял так:
  Перерыв 13:00–14:00, пн–пт, 17–21 марта

Что изменится:
  – закроет 5 слотов по 1 ч (по одному в день)
  Записей в это время нет.

[ ✅ Добавить ] [ ❌ Отменить ]
```

### Alternative scenarios

#### Conflict

Scenario: existing booking at break time.

**USER**  
`Добавь обед 14–15 начиная с завтра на 2 недели.`

**AGENT**
```text
⚠️  Конфликт: запись Марины в четверг 20 марта в 14:15.
Перерыв накроет этот слот.

Добавить перерыв и пометить запись Марины как конфликтную?

[ Да, добавить ] [ Отменить ]
```

#### Ambiguous or invalid request

Scenario: break outside current working hours.

**USER**  
`Добавь перерыв с 9 до 10.`

**AGENT**
```text
Твои рабочие часы начинаются в 12:00.
9:00–10:00 находится вне расписания — добавлять перерыв туда не имеет смысла.
Уточни время или измени рабочие часы.
```

### Acceptance criteria - UC-02

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | Break outside working hours is rejected with explanation | Test: break start < working-hours start |
| 2 | Break overlapping booking shows client name in preview | Test with booking fixture |
| 3 | Confirm creates ACP run with `type=http` and `capability=availability.add_break` | Inspect ACP run payload |
| 4 | One break per day is idempotent | Send same operation twice with same key |

---

## UC-03 - `close_range`

Provider closes a specific time range or full day, making slots unavailable.

### Happy path

Scenario: close a half-day with no conflicts.

**USER**  
`Закрой утро в пятницу 28 марта — до 12.`

**AGENT**
```text
Понял так: закрыть 28 марта 00:00–12:00.

Что изменится:
  – удалит 4 утренних слота
  Записей нет.

[ ✅ Закрыть ] [ ❌ Отменить ]
```

### Alternative scenarios

#### Conflict

Scenario: active bookings inside range.

**USER**  
`Закрой всё послезавтра.`

**AGENT**
```text
⚠️  2 записи попадают в закрываемый день (29 марта):
  • Иван Петров — стрижка в 11:00
  • Ольга — маникюр в 15:30

Закрыть день и пометить записи как конфликтные?

[ Закрыть и пометить ] [ Отменить ]
```

#### Clarification

Scenario: vague time reference.

**USER**  
`Закрой вечер в среду.`

**AGENT**  
`С какого времени считать вечер? Например, с 18:00?`

**USER**  
`Да, с 18.`

**AGENT**
```text
Понял так: закрыть среду, 26 марта, 18:00–конец дня.

Что изменится: удалит 2 слота. Записей нет.

[ ✅ Закрыть ] [ ❌ Отменить ]
```

### Acceptance criteria - UC-03

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | "Вечер", "утро", "день" require explicit clarification; no guessing | Prompt tests with vague time references |
| 2 | "Послезавтра" resolves by provider timezone | Test: today = Saturday, "послезавтра" = Monday |
| 3 | All conflicting bookings are listed before confirm | Fixture with 3 bookings |
| 4 | Closing full day does not delete bookings; it marks conflicts only | Assert Bookably booking count unchanged |

---

## UC-04 - `list_bookings`

Read-only intent: provider requests upcoming bookings for a date or range. No confirmation required.

### Happy path

Scenario: bookings for tomorrow.

**USER**  
`Покажи мои записи на завтра.`

**AGENT**
```text
Записи на воскресенье, 22 марта:

  11:00 — Алина Смирнова · Массаж 60 мин
  13:30 — Марина · Маникюр 90 мин
  16:00 — Иван Петров · Стрижка 30 мин

Итого: 3 записи · 3.5 ч.
```

Scenario: empty day.

**USER**  
`Что у меня в понедельник?`

**AGENT**  
`На понедельник, 24 марта, записей нет.`

### Alternative scenarios

#### Ambiguous period

**USER**  
`Покажи записи на майские.`

**AGENT**  
`Уточни: с 1 по 8 мая или другой диапазон?`

#### Deep link escalation

**USER**  
`Покажи всё на следующий месяц.`

**AGENT**
```text
На месяц лучше смотреть в календаре — там удобнее.
[ Открыть апрель в приложении → ]
```

### Acceptance criteria - UC-04

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | `list_bookings` never triggers confirm and never creates ACP run | Assert no ACP call |
| 2 | Empty day returns explicit message, not empty list | Test with no-bookings fixture |
| 3 | Range > 7 days triggers Mini App deep link | Test with 30-day range |
| 4 | Bookings are sorted by start time ascending | Assert order |
| 5 | Time formatting uses provider timezone | Test: provider UTC+3, storage UTC |

---

## UC-05 - `create_booking`

Provider books a client for a service at requested time. Agent finds slot, asks confirm, executes via ACP.

### Happy path

Scenario: client name, service, and time constraints provided.

**USER**  
`Запиши Алину на массаж 60 минут завтра после 18.`

**AGENT**
```text
Нашёл свободное время:
  18:30 — Массаж 60 мин — Алина Смирнова
  (следующее окно: 19:30)

Создать запись на 18:30?

[ ✅ Создать в 18:30 ] [ 19:30 ] [ ❌ Отменить ]
```

**USER**  
`[ ✅ Создать в 18:30 ]`

**AGENT**  
`Запись создана: Алина Смирнова · Массаж 60 мин · завтра 18:30.`

### Alternative scenarios

#### Clarification - service ambiguity

**USER**  
`Запиши Марину на процедуру в пятницу.`

**AGENT**
```text
Какую процедуру записать Марине?
  1. Маникюр (60 мин)
  2. Педикюр (90 мин)
  3. Комбо маникюр+педикюр (120 мин)

[ Маникюр ] [ Педикюр ] [ Комбо ]
```

#### Clarification - no slots in requested window

**USER**  
`Запиши Ивана на стрижку сегодня в 17.`

**AGENT**
```text
В 17:00 сегодня свободных окон нет.
Ближайшее: завтра 10:00.
Записать Ивана на завтра 10:00?

[ ✅ Да, завтра 10:00 ] [ ❌ Нет ]
```

#### Ambiguous client identity

**USER**  
`Запиши Катю на маникюр в среду.`

**AGENT**
```text
Клиента с именем «Катя» нет в базе.
Создать нового клиента или это существующий клиент с другим именем?

[ Создать нового клиента ] [ Уточнить имя ]
```

#### Error - execution-time slot race

**AGENT**
```text
Не удалось создать запись — слот 18:30 уже занят.
Следующее доступное время: 19:30.
Записать Алину на 19:30?

[ ✅ Да ] [ ❌ Нет ]
```

### Acceptance criteria - UC-05

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | Agent proposes at most 2 slot options in preview | Assert slot count <= 2 |
| 2 | If no slot in requested window, nearest alternative is proposed | Test: all window slots occupied |
| 3 | Race condition after preview leads to re-proposal, not terminal error | Mock slot taken at execution |
| 4 | Unknown client is not auto-created; explicit choice required | Test: unknown client name |
| 5 | ACP run uses `idempotency_key = hash(chat_id + plan_id)` | Inspect run payload |

---

## UC-06 - `cancel_booking`

Provider cancels existing booking. Highest-risk intent; explicit confirmation always required.

### Happy path

Scenario: unambiguous booking reference.

**USER**  
`Отмени запись Ивана в четверг.`

**AGENT**
```text
Нашёл запись:
  Иван Петров · Стрижка 30 мин · чт 27 марта 11:00

⚠️  Это действие необратимо.
Отменить эту запись?

[ ✅ Да, отменить ] [ ❌ Нет ]
```

**USER**  
`[ ✅ Да, отменить ]`

**AGENT**  
`Запись отменена. Слот 27 марта 11:00 освобождён.`

### Alternative scenarios

#### Clarification - multiple matches

**USER**  
`Отмени запись Марины.`

**AGENT**
```text
Нашёл 2 записи Марины:
  1. Маникюр · пт 28 марта 14:00
  2. Педикюр · вт 1 апреля 11:30

Какую отменить?

[ 28 марта 14:00 ] [ 1 апреля 11:30 ] [ ❌ Ни одну ]
```

#### No match

**USER**  
`Отмени запись Алёши на следующей неделе.`

**AGENT**  
`Записей клиента «Алёша» на следующей неделе нет.`

#### Cancel cancellation

**AGENT**  
`[ preview, as above ]`

**USER**  
`[ ❌ Нет ]`

**AGENT**  
`Понял, запись осталась без изменений.`

### Acceptance criteria - UC-06

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | Preview contains irreversible action warning | Assert warning text present |
| 2 | If multiple matches exist, show list and do not guess | Test: 2 bookings same client name |
| 3 | Cancellation never executes without explicit confirm | Assert no ACP call before confirm |
| 4 | After cancellation, slot is freed in schedule | Read slot state after cancel |
| 5 | Unknown client or empty search result returns explicit message | Test: unknown client |

---

## UC-07 - `find_next_slot`

Read-only intent: provider asks for next available slot matching constraints. No confirmation required.

### Happy path

Scenario: service, duration, and time constraint.

**USER**  
`Найди ближайшее окно для маникюра 90 минут после 16.`

**AGENT**
```text
Ближайшие свободные окна (90 мин, после 16:00):

  1. Сегодня 17:30 — чт 21 марта
  2. Завтра 16:00 — пт 22 марта

[ Записать клиента на 17:30 ] [ Записать на пятницу ]
```

If provider taps a slot button, flow transitions to UC-05 (`create_booking`) with pre-filled parameters.

Scenario: no time constraint, duration only.

**USER**  
`Когда следующее окно на 2 часа?`

**AGENT**
```text
Ближайшее окно 120 мин:
  Завтра 12:00 · пт 22 марта
```

### Alternative scenarios

#### Ambiguous request

**USER**  
`Найди свободное окно.`

**AGENT**  
`Для какой услуги и на сколько минут?`

#### Clarification - no nearby slots

**USER**  
`Найди окно для массажа 90 минут на этой неделе.`

**AGENT**
```text
На этой неделе нет свободных окон 90 мин для массажа.
Ближайшее: пн 24 марта 12:00.
```

### Acceptance criteria - UC-07

| # | Criterion | Verification |
| --- | --- | --- |
| 1 | `find_next_slot` does not create ACP run | Assert no ACP call |
| 2 | Shows at most 2 slot options | Assert results <= 2 |
| 3 | "Записать" button starts UC-05 with pre-filled params | E2E test: tap button -> `create_booking` flow |
| 4 | If no slots in period, suggest nearest outside period | Test: no slots this week |
| 5 | Duration is mandatory; agent asks if missing | Test: no duration in prompt |

---

## Cross-Cutting Scenarios

These scenarios apply across multiple use cases and are not tied to a single intent.

### CC-01 - Session recovery after bot restart

If bot restarts while session has pending `ActionPlan` waiting for confirmation:

1. Provider sends request and gets preview.
2. Bot restarts (deploy, crash, scale event).
3. Provider taps `[ ✅ Применить ]`.

Expected behavior:

- Session is restored from Redis.
- Agent recognizes `callbackQuery`, loads pending plan, and continues ACP execution as if no restart occurred.

Implementation note:

- Redis session key must contain `chat_id`, serialized pending `ActionPlan`, preview `message_id`, and plan-expiry timestamp.
- Plan expiry is 15 minutes.
- Stale confirm must be rejected with message asking provider to repeat request.

### CC-02 - Concurrent requests

Scenario: provider sends two messages quickly before first is fully processed.

Expected behavior:

- Messages are handled sequentially per `chat_id`.
- Second message waits until first intent cycle completes.
- Agent never interleaves two `ActionPlan` instances in one session.

### CC-03 - Unknown intent

**USER**  
`Привет, как дела?`

**AGENT**
```text
Привет! Я помогаю управлять расписанием и записями.
Попробуй написать что-то вроде:
  • «Покажи записи на завтра»
  • «Закрой пятницу»
  • «Запиши Алину на массаж»
```

Rules:

- Agent does not answer arbitrary questions.
- Confidence `< 0.5` -> `unknown` intent -> onboarding hint.

### CC-04 - LLM timeout or failure

**AGENT**
```text
Не смог обработать запрос — попробуй ещё раз.
Если проблема повторяется, открой приложение.
[ Открыть приложение → ]
```

### CC-05 - ACP run failure after confirm

Scenario: provider confirmed, ACP run started, but execution failed (Bookably error, policy violation, timeout).

**AGENT**
```text
Не удалось применить изменения.
Причина: [описание ошибки из ACP]

Попробовать ещё раз?
[ Повторить ] [ Открыть в приложении → ]
```

Retry uses the same `idempotency_key`; ACP guarantees non-duplication.

### CC-06 - Deep-link escalation rule

Agent surfaces Mini App deep link when any condition is met:

- More than 2 clarification exchanges for one intent.
- Intent is `list_bookings` with range > 7 days.
- Provider explicitly asks for calendar or visual view.
- Write operation affects more than 20 slots simultaneously (high-impact visual review recommended).

Deep-link format:

```text
tg://resolve?domain=bookably_bot&appname=bookably&startapp={action}_{context}
```
