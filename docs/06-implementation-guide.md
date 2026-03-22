# BOOKABLY AGENT

## Implementation Guide

Version 1.0 | March 2026 | Doc 06 of series  
Depends on: all previous docs (`01-05`)

## Purpose

This is the document to open when coding starts. It contains:

- exact system prompt for interpreter
- `go.mod` dependency set
- build order and rationale
- full test-case set for intent classifier

## Source-of-truth note

This guide is normative for implementation sequencing and prompt/dependency contracts.  
If snippets here differ from temporary bootstrap files, treat this document as source of truth for subsequent coding tickets.

## 1. `go.mod` - dependencies

Exact dependency list. Do not add dependencies not listed here without updating this document first.

```go
module github.com/{your-username}/bookably-agent

go 1.22

require (
    // Telegram Bot API — raw HTTP client (no framework, full control)
    github.com/go-telegram-bot-api/telegram-bot-api/v5  v5.5.1

    // Redis client — session store
    github.com/redis/go-redis/v9                        v9.5.1

    // LLM providers
    github.com/anthropics/anthropic-sdk-go              v0.2.0-alpha.4
    // github.com/sashabaranov/go-openai               v1.24.0  // uncomment if using OpenAI

    // HTTP server for webhook
    github.com/gin-gonic/gin                            v1.9.1

    // Structured logging
    go.uber.org/zap                                     v1.27.0

    // Environment config
    github.com/joho/godotenv                            v1.5.1
    github.com/kelseyhightower/envconfig                v1.4.0

    // Testing
    github.com/stretchr/testify                         v1.9.0
    github.com/stretchr/mock                            v0.0.1
)

// INTENTIONALLY ABSENT:
// - No ORM (no database owned by agent)
// - No gRPC
// - No message queue
// - No heavy framework (no Echo, no Fiber)
```

### On `telegram-bot-api` vs raw HTTP

`go-telegram-bot-api v5` does not yet support `sendMessageDraft` (Bot API 9.3+).  
`internal/bot/streaming.go` must call `sendMessageDraft` directly via raw HTTP POST:

`https://api.telegram.org/bot{token}/sendMessageDraft`

All other Bot API calls can use the library.

## 2. Configuration - `config/config.go`

Full config struct with validation. Service must not start if any required field is missing.

```go
package config

import (
    "fmt"
    "github.com/kelseyhightower/envconfig"
)

type Config struct {
    // Telegram
    TelegramBotToken      string `envconfig:"TG_BOT_TOKEN" required:"true"`
    TelegramWebhookURL    string `envconfig:"TG_WEBHOOK_URL" required:"true"`
    TelegramWebhookSecret string `envconfig:"TG_WEBHOOK_SECRET" required:"true"`

    // Redis
    RedisURL string `envconfig:"REDIS_URL" required:"true"`

    // ACP
    ACPBaseURL string `envconfig:"ACP_BASE_URL" required:"true"`
    ACPAPIKey  string `envconfig:"ACP_API_KEY" required:"true"`

    // Bookably
    BookablyAPIURL string `envconfig:"BOOKABLY_API_URL" required:"true"`

    // LLM
    LLMProvider string `envconfig:"LLM_PROVIDER" required:"true"` // anthropic | openai
    LLMAPIKey   string `envconfig:"LLM_API_KEY" required:"true"`
    LLMModel    string `envconfig:"LLM_MODEL" default:""` // empty = use provider default

    // Mini App
    MiniAppURL string `envconfig:"MINI_APP_URL" required:"true"`

    // Server
    Port     int    `envconfig:"PORT" default:"8080"`
    LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
}

func Load() (*Config, error) {
    var c Config
    if err := envconfig.Process("", &c); err != nil {
        return nil, fmt.Errorf("config: %w", err)
    }
    if c.LLMProvider != "anthropic" && c.LLMProvider != "openai" {
        return nil, fmt.Errorf("config: LLM_PROVIDER must be 'anthropic' or 'openai', got %q", c.LLMProvider)
    }
    return &c, nil
}
```

## 3. System prompt - `prompts/intent_classifier.md`

Exact prompt file content. It is loaded at startup and passed as system message to LLM on every interpreter call.  
Do not inline this text in Go source.

### Versioning rule

Every change to this file must be committed with message like:

`prompt: v1.1 - improve date resolution`

Prompt is source code. Treat it like Go files.

```text
# Bookably Booking Agent — Intent Classifier
# Version: 1.0

You are an AI assistant for a service provider using the Bookably platform.
Your ONLY job is to classify the provider's message into a structured JSON action plan.
You operate in Russian by default. Respond to English input in English.

## Your capabilities (supported intents)

| Intent              | Description                                      |
|---------------------|--------------------------------------------------|
| set_working_hours   | Update working hours for a date range            |
| add_break           | Add a recurring break within existing hours      |
| close_range         | Close a specific time period (remove slots)      |
| list_bookings       | Show upcoming or past bookings                   |
| create_booking      | Book a client for a service at a time            |
| cancel_booking      | Cancel an existing booking                       |
| find_next_slot      | Find next available slot for a service           |
| unknown             | Anything outside the above capabilities          |

## Output format

You MUST respond with ONLY valid JSON. No prose. No markdown fences. No preamble.
The JSON must match this exact schema:

{
  "intent": "<one of the 8 values above>",
  "confidence": <float 0.0–1.0>,
  "requires_confirmation": <boolean>,
  "clarifications": [],
  "params": {
    // intent-specific parameters — see schema below
  }
}

## Confirmation rules

Set requires_confirmation: true for these intents:
  set_working_hours, add_break, close_range, create_booking, cancel_booking
Set requires_confirmation: false for: list_bookings, find_next_slot, unknown

## Confidence rules

- confidence >= 0.85: clear intent, output full params
- confidence 0.50–0.84: intent likely, but key params missing — output clarification
- confidence < 0.50: set intent to 'unknown'

## Clarification rules

- If a required parameter is absent or ambiguous, add ONE item to clarifications[]
- Ask for the SINGLE most blocking missing parameter
- Never ask more than one clarification question at a time
- Write the question in the provider's language

Clarification object schema:
{
  "field": "<parameter name>",
  "question": "<short, friendly question in Russian>"
}

## Date and time resolution

The provider's timezone is provided in the context below. Always resolve dates
relative to today's date and the provider's timezone.

Colloquial time references — always resolve to exact times:
  утром / с утра     → 09:00
  в обед / после обеда → 13:00
  вечером / с вечера → 18:00
  ночью              → 22:00
  до обеда           → until 12:00
  после обеда        → from 13:00

EXCEPTION: for close_range intent, do NOT assume time defaults.
If the time is vague (вечером, утром), ask for exact time instead.

Relative date references:
  сегодня            → today
  завтра             → today + 1 day
  послезавтра        → today + 2 days
  на следующей неделе → Monday to Friday of next week
  на майские         → clarify exact range

## Params schema by intent

### set_working_hours
{
  "date_range": { "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" },  // REQUIRED
  "weekdays": ["mon","tue","wed","thu","fri"],  // optional, defaults to all days in range
  "working_hours": { "from": "HH:MM", "to": "HH:MM" },  // REQUIRED
  "breaks": [{ "from": "HH:MM", "to": "HH:MM" }]  // optional
}

### add_break
{
  "date_range": { "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" },  // REQUIRED
  "weekdays": ["mon","tue"],  // optional
  "break_slot": { "from": "HH:MM", "to": "HH:MM" }  // REQUIRED
}

### close_range
{
  "date_range": { "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" },  // REQUIRED
  "time_range": { "from": "HH:MM", "to": "HH:MM" }  // optional, omit = close full day
}

### list_bookings
{
  "date_range": { "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" },  // REQUIRED
  "status": "upcoming"  // upcoming | past | all, default: upcoming
}

### create_booking
{
  "client_name": "Алина",  // REQUIRED
  "service_name": "Массаж 60 мин",  // REQUIRED
  "not_before": "YYYY-MM-DDTHH:MM:00",  // REQUIRED — earliest acceptable time
  "preferred_date": "YYYY-MM-DD"  // optional — preferred date
}

### cancel_booking
{
  "client_name": "Иван",  // REQUIRED
  "approximate_time": "YYYY-MM-DDTHH:MM:00"  // optional but strongly preferred
}

### find_next_slot
{
  "service_name": "Маникюр",  // REQUIRED
  "not_before": "YYYY-MM-DDTHH:MM:00",  // optional, defaults to now
  "max_results": 2  // always 2
}

## Context (injected per request)

Today's date: {{TODAY_DATE}}
Today's weekday: {{TODAY_WEEKDAY}}
Provider timezone: {{TIMEZONE}}
Current time: {{CURRENT_TIME}}

## Important rules

1. Never invent data. If client_name is not in the message, ask for it.
2. Never call APIs or take actions. Your output is JSON only.
3. If confidence < 0.50, set intent to 'unknown' and leave params empty.
4. All dates in params must be ISO 8601 format (YYYY-MM-DD or YYYY-MM-DDTHH:MM:00).
5. Times in working_hours and break_slot must be HH:MM (24h format).
6. Weekday names must be lowercase English 3-letter codes: mon, tue, wed, thu, fri, sat, sun.
7. Do not output any text outside the JSON object.
```

### 3.1 How the prompt is loaded in Go

`internal/interpreter/prompts.go`:

```go
package interpreter

import (
    "fmt"
    "os"
    "strings"
    "time"
)

// loadSystemPrompt reads the prompt file and injects runtime context.
func loadSystemPrompt(promptPath string, tz *time.Location) (string, error) {
    raw, err := os.ReadFile(promptPath)
    if err != nil {
        return "", fmt.Errorf("prompt: read %s: %w", promptPath, err)
    }

    now := time.Now().In(tz)
    replacer := strings.NewReplacer(
        "{{TODAY_DATE}}", now.Format("2006-01-02"),
        "{{TODAY_WEEKDAY}}", now.Weekday().String(),
        "{{TIMEZONE}}", tz.String(),
        "{{CURRENT_TIME}}", now.Format("15:04"),
    )
    return replacer.Replace(string(raw)), nil
}
```

## 4. Build order

Implementation order is designed so each layer can be tested independently before the next is built.  
Never write bot layer before interpreter is tested.

Golden rule:

- build bottom-up
- add tests at every layer
- next phase starts only after current phase tests pass

### Phase 1 - Foundation (no external calls)

Build and test components with zero external dependencies first.

1. `internal/domain/` - types and `Provider` interface. Zero imports. Add compile-time interface compliance test (`bookably.Adapter` implements `domain.Provider`).
2. `internal/interpreter/types.go` - `ActionPlan`, `Intent`, `ActionParams`, `Clarification`. Only standard library imports. Add unit tests for `Intent.RequiresConfirm()` and `ActionPlan.NeedsClarification()`.
3. `internal/session/types.go` - `Session`, `PendingPlan`. Standard library (`time`) only.
4. `config/config.go` - `Load()` with `envconfig`. Test missing required var errors.
5. `prompts/intent_classifier.md` - system prompt from section 3.

### Phase 2 - LLM layer (single external dependency)

Build LLM abstraction and test with real API before Bookably integration.

6. `internal/llm/client.go` - `LLMClient` interface.
7. `internal/llm/anthropic.go` - Anthropic implementation. Test real API call and valid JSON completion.
8. `internal/interpreter/prompts.go` - `loadSystemPrompt()` context injection.
9. `internal/interpreter/parser.go` - JSON -> `ActionPlan` parser. Test with full test-case set from section 5 (most important test file).
10. `internal/interpreter/interpreter.go` - orchestration: build messages -> call LLM -> parse -> validate. Mock `LLMClient` in tests.

### Phase 3 - Session layer (Redis)

Build session persistence before Telegram/Bookably integration.

11. `internal/session/store.go` - `SessionStore` + Redis implementation. Test with real Redis or containerized local dependency.
12. Test plan expiry (`15 min`): `CreatedAt = now - 16min` -> `IsExpired() == true`.
13. Test session replacement: save `PendingPlan A`, then `PendingPlan B`, verify `A` is gone.

### Phase 4 - Bookably adapter (HTTP, read-only first)

Build adapter incrementally. Read operations first for preview; writes later through ACP.

14. `internal/bookably/client.go` - HTTP client with auth, 5s timeout, 401 retry.
15. `internal/bookably/errors.go` - HTTP status -> typed domain error mapping.
16. `internal/bookably/adapter.go` - implement `GetBookings()`, `FindSlots()`, `GetProviderInfo()`. Test with staging/dev Bookably.
17. `internal/bookably/adapter.go` - implement `PreviewAvailabilityChange()`, `PreviewBookingCreate()`, `PreviewBookingCancel()` as read-only builders.
18. BLOCKER: before implementing slot-mutation previews, resolve Doc 05 section 7 open questions (slot body schema, commit requirement, slot range filters).

### Phase 5 - ACP layer

Build ACP integration after adapter; payload builder depends on resolved endpoint contracts.

19. `internal/acp/types.go` - `ACPRun`, `ACPStep`, `ACPRunResult`, `ACPStatus`.
20. `internal/acp/client.go` - `POST /runs`, `GET /runs/{id}`. Test with real ACP runtime.
21. `internal/acp/builder.go` - `ActionPlan -> ACPRun` payload builders per intent. Unit-test builder JSON shape against Doc 05 section 4.
22. `internal/acp/runner.go` - submit + poll (`2s` interval, `30s` timeout). Test with mock ACP returning `executing` x3 then `completed`.

### Phase 6 - Bot layer (Telegram integration)

Build last. Lower layers must be tested first.

23. `internal/bot/streaming.go` - `sendMessageDraft` via raw HTTP. Test with real bot token.
24. `internal/bot/keyboard.go` - inline keyboard builders and prefixed `callback_data`.
25. `internal/bot/sessions.go` - thin wrapper over `SessionStore`, `chat_id` routing.
26. `internal/bot/handler.go` - update routing for message and callback flows; test with mocked dependencies.

### Phase 7 - Wire-up & end-to-end

27. `cmd/agent/main.go` - dependency injection wiring from config.
28. `Dockerfile` - production image with static binary.
29. E2E test with two demo messages from Doc 01 section 12.
30. Demo dry-run against Bookably staging.

## 5. Intent classifier test cases

These 40 test inputs cover all 7 intents plus edge cases.  
Interpreter must pass all before Phase 3 can begin.

Minimum acceptable: `85%` pass rate.  
Target: `100%`.

Run as table-driven test in `internal/interpreter/interpreter_test.go`.

Per row:

- call `Interpret()` with input and "today" context
- assert expected intent
- assert confidence `>= 0.5`

### 5.1 `set_working_hours`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `На следующей неделе работаю с 12 до 20, кроме пятницы` | `set_working_hours` | weekdays, hours | Standard happy path |
| `Со вторника по четверг ставь мне с 10 до 18` | `set_working_hours` | date_range, hours | Named weekdays |
| `С 24 по 28 марта работаю с 9 утра до 7 вечера` | `set_working_hours` | date_range, hours | Colloquial times |
| `Следующая неделя — рабочая, часы как обычно` | `set_working_hours` |  | Clarification: what hours? |
| `Работаю завтра с 12` | `set_working_hours` |  | Clarification: until what time? |

### 5.2 `add_break`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Добавь обед 13–14 на всю эту неделю` | `add_break` | break_slot, date_range | Standard |
| `Каждый день в 15 делай перерыв на час` | `add_break` | break_slot | Duration -> infer end time |
| `По будням ставь мне обеденный перерыв с двух до трёх` | `add_break` | break_slot, weekdays | Colloquial from/to |
| `Убери обед в среду` | `close_range` | date_range, time_range | Ambiguous -> maps to `close_range`, not `add_break` |

### 5.3 `close_range`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Закрой пятницу 28 марта` | `close_range` | date_range | Full day |
| `Закрой утро в среду` | `close_range` | clarification | Vague time; must ask exact time |
| `Закрой всё послезавтра` | `close_range` | date_range | Relative date |
| `Закрой среду вечером с 18` | `close_range` | date_range, time_range | Partial explicit time |
| `Не работаю в мае с 1 по 8` | `close_range` | date_range | Multi-day range |

### 5.4 `list_bookings`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Покажи мои записи на завтра` | `list_bookings` | date_range=tomorrow | Standard |
| `Что у меня сегодня?` | `list_bookings` | date_range=today | Colloquial |
| `Покажи записи на эту неделю` | `list_bookings` | date_range=this week | Week range |
| `Кто записан на понедельник?` | `list_bookings` | date_range=monday | Implicit listing |
| `Покажи всё на следующий месяц` | `list_bookings` | date_range=30d | Deep-link trigger (`> 7d`) |

### 5.5 `create_booking`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Запиши Алину на массаж 60 мин завтра после 18` | `create_booking` | client, service, not_before | Happy path |
| `Создай запись: Марина, маникюр, в пятницу` | `create_booking` | client, service, date | Day without time -> `not_before=00:00` Friday |
| `Запиши Ивана на стрижку как можно скорее` | `create_booking` | client, service | `not_before=now()` |
| `Запиши Катю на процедуру` | `create_booking` | clarification | `service_name` ambiguous; ask |
| `Запиши кого-нибудь на вечер` | `create_booking` | clarification | `client_name` missing; ask |

### 5.6 `cancel_booking`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Отмени запись Ивана в четверг` | `cancel_booking` | client_name, time | Happy path |
| `Отмени Марину` | `cancel_booking` | client_name | No time; disambiguation expected |
| `Отмени запись на завтра 14:00` | `cancel_booking` | time, no client | Clarification: who? |
| `Удали бронь Алёши` | `cancel_booking` | client_name | Cancel synonym |

### 5.7 `find_next_slot`

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Найди ближайшее окно для маникюра 90 минут после 16` | `find_next_slot` | service, not_before | Happy path |
| `Когда следующий свободный час?` | `find_next_slot` | clarification | Service missing; ask |
| `Есть место на массаж завтра?` | `find_next_slot` | service, date | Implicit find |
| `Когда ближайшее окно?` | `find_next_slot` | clarification | No service; ask |

### 5.8 `unknown` / edge cases

| Input (Russian) | Expected intent | Key params | Edge / note |
| --- | --- | --- | --- |
| `Привет!` | `unknown` |  | Greeting -> onboarding hint |
| `Как дела?` | `unknown` |  | Small talk |
| `Сколько стоит массаж?` | `unknown` |  | Price query out of scope |
| `Позвони клиенту` | `unknown` |  | Action out of scope |
| `Напиши Марине что завтра отмена` | `unknown` |  | Messaging out of scope |
| `12345` | `unknown` |  | Random input, low confidence |
| `Запиши всех клиентов которые есть` | `unknown` |  | Nonsensical, low confidence |

## 6. `sendMessageDraft` implementation

`go-telegram-bot-api` does not support `sendMessageDraft` yet. Use raw HTTP implementation in `internal/bot/streaming.go`.

API reference:

- Bot API 9.3+ (Dec 2025)
- available to all bots since Bot API 9.5 (Mar 2026)
- streams partial message text while content is generated

```go
package bot

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

type draftRequest struct {
    ChatID    int64  `json:"chat_id"`
    Text      string `json:"text"`
    ParseMode string `json:"parse_mode,omitempty"`
}

// SendDraft streams partial message content to the user.
// Call repeatedly as new content becomes available.
// The text replaces the previous draft each time.
// Telegram displays it as a live typing effect.
func (b *Bot) SendDraft(ctx context.Context, chatID int64, text string) error {
    body := draftRequest{
        ChatID:    chatID,
        Text:      text,
        ParseMode: "MarkdownV2",
    }
    payload, err := json.Marshal(body)
    if err != nil {
        return fmt.Errorf("sendMessageDraft: marshal: %w", err)
    }

    url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessageDraft", b.token)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
    if err != nil {
        return fmt.Errorf("sendMessageDraft: build request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := b.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("sendMessageDraft: http: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("sendMessageDraft: status %d", resp.StatusCode)
    }
    return nil
}

// StreamInterpret calls SendDraft with progress updates while the LLM runs.
// Returns the final ActionPlan once interpretation is complete.
func (b *Bot) StreamInterpret(ctx context.Context, chatID int64,
    interpret func(ctx context.Context) (*ActionPlan, error),
) (*ActionPlan, error) {

    // Send initial draft immediately
    _ = b.SendDraft(ctx, chatID, "🤔 Анализирую\\.\\.\\.")

    // Run interpretation (LLM call — up to 15s)
    plan, err := interpret(ctx)

    // Draft is replaced by the final message via sendMessage
    // No need to clear the draft — sendMessage supersedes it
    return plan, err
}
```

## 7. Preview message format

Exact MarkdownV2 templates for final preview message after interpretation.

### 7.1 `set_working_hours` / `add_break` / `close_range` preview

Template (MarkdownV2; escape special chars with backslash).  
Chars that MUST be escaped: `_ * [ ] ( ) ~ \` > # + - = | { } . !`

```text
*Понял так:*
  Период: пн–чт, 24–27 марта
  Рабочие часы: 12:00–20:00
  Перерыв: 15:00–16:00 ежедневно

*Что изменится:*
  \+ 28 новых слотов
  \- 4 существующих слота удалятся

⚠️ *Конфликты:*
  • Алина Смирнова — Массаж в ср 26 марта 14:00
```

Inline keyboard (Go):

```go
// tgbotapi.NewInlineKeyboardMarkup(
//   tgbotapi.NewInlineKeyboardRow(
//     tgbotapi.NewInlineKeyboardButtonData("✅ Применить", "confirm:"+planID),
//     tgbotapi.NewInlineKeyboardButtonData("❌ Отменить", "cancel:"+planID),
//   ),
// )
```

### 7.2 `create_booking` preview

```text
*Нашёл свободное время:*
  18:30 — Массаж 60 мин — Алина Смирнова
  19:30 — (следующий вариант)
```

Inline keyboard with slot choices:

- row 1: `[✅ 18:30] [19:30]`
- row 2: `[❌ Отменить]`
- `callback_data`: `slot:0:{planID}`, `slot:1:{planID}`, `cancel:{planID}`

### 7.3 `cancel_booking` preview

```text
*Нашёл запись:*
  Иван Петров · Стрижка 30 мин · чт 27 марта 11:00

⚠️ *Это действие необратимо\.*
```

Inline keyboard:

- `[✅ Да, отменить] [❌ Нет]`
- `callback_data`: `confirm:{planID}`, `cancel:{planID}`

## 8. Dockerfile

```dockerfile
# Stage 1: build
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o agent ./cmd/agent

# Stage 2: run
FROM scratch
COPY --from=builder /app/agent /agent
COPY --from=builder /app/prompts /prompts
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8080
ENTRYPOINT ["/agent"]
```

## 9. Local development setup

### 9.1 `docker-compose.yml` for dependencies

```yaml
version: "3.9"
services:
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    command: redis-server --requirepass localpass

  # ACP runtime (from chillmeal/TON-ACP)
  acp:
    image: ghcr.io/chillmeal/ton-acp:latest  # or build from source
    ports:
      - "8181:8080"
    env_file: .env.acp

  # Bookably API — run separately from your private repo
  # Point BOOKABLY_API_URL to http://localhost:3000 or staging URL
```

### 9.2 `.env.example`

```dotenv
TG_BOT_TOKEN=123456:ABC-DEF...
TG_WEBHOOK_URL=https://your-ngrok-url.ngrok.io/webhook
TG_WEBHOOK_SECRET=random-secret-string

REDIS_URL=redis://:localpass@localhost:6379/0

ACP_BASE_URL=http://localhost:8181
ACP_API_KEY=local-acp-key

BOOKABLY_API_URL=http://localhost:3000

LLM_PROVIDER=anthropic
LLM_API_KEY=sk-ant-...

MINI_APP_URL=https://t.me/your_bot/app

PORT=8080
LOG_LEVEL=debug
```

### 9.3 Webhook tunnel for local development

```bash
# Use ngrok to expose local port for Telegram webhook
ngrok http 8080

# Then register webhook:
curl -X POST https://api.telegram.org/bot{TOKEN}/setWebhook \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://your-ngrok.ngrok.io/webhook",
    "secret_token": "random-secret-string",
    "allowed_updates": ["message", "callback_query"]
  }'
```
