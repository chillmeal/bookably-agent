# BOOKABLY AGENT

## System Architecture

Version 1.0 | March 2026 | Doc 04 of series  
Depends on: [01 - Vision](./01-vision-mvp-scope.md), [02 - Use Cases](./02-use-cases.md), [03 - Requirements](./03-requirements.md)

## 1. Architecture overview

Bookably Agent is a Go service that connects Telegram, an LLM, ACP, and the Bookably API.  
The service has no database of its own. It uses Redis for ephemeral session state and delegates all business data to Bookably.

Core architectural principle: every layer has one job.

- Bot layer handles Telegram protocol.
- Interpreter layer handles language.
- Domain layer handles semantic contracts.
- ACP layer handles execution safety.

Layers communicate only through narrow, typed interfaces.

### 1.1 System context

Components interacting with the agent (`EXTERNAL` are outside this repository boundary):

| Component | Role / Notes |
| --- | --- |
| Telegram Bot API (EXTERNAL) | Message delivery: receives provider updates, sends responses and streaming drafts |
| LLM API - Anthropic / OpenAI (EXTERNAL) | Intent classification and structured parameter extraction from natural language |
| ACP Runtime (EXTERNAL - chillmeal/TON-ACP) | Execution control plane: policy gating, lifecycle management, audit trail for all write operations |
| Bookably API (EXTERNAL - private repo) | Source of truth: providers, services, schedules, slots, bookings |
| Redis (INFRASTRUCTURE) | Ephemeral session state: pending plans, message IDs, dialog history, provider preferences |
| Bookably Agent (THIS SERVICE) | Conversational execution layer: `intent -> preview -> confirm -> execute` |

## 2. Repository structure

Repository is a single Go module. All packages under `internal/` are unexported.  
`cmd/` entry point wires dependencies via constructor injection.

```text
bookably-agent/
├── cmd/agent/
│   └── main.go              // wire: config -> redis -> bot -> start
│
├── internal/
│   ├── bot/                 // Telegram update handling + streaming
│   │   ├── handler.go       // routes Updates to intent pipeline
│   │   ├── streaming.go     // sendMessageDraft wrapper
│   │   ├── sessions.go      // thin wrapper over SessionStore
│   │   └── keyboard.go      // inline keyboard builders
│   │
│   ├── interpreter/         // LLM-based intent classification
│   │   ├── interpreter.go   // orchestrates LLM call -> parse -> validate
│   │   ├── types.go         // ActionPlan, Intent, ActionParams, Clarification
│   │   ├── prompts.go       // loads prompt file, renders with context
│   │   └── parser.go        // JSON -> ActionPlan, defensive, no panics
│   │
│   ├── domain/              // pure domain types + provider interface
│   │   ├── provider.go      // Provider interface (key seam)
│   │   └── types.go         // Booking, Slot, Preview, Conflict, RiskLevel
│   │
│   ├── bookably/            // Bookably API adapter
│   │   ├── adapter.go       // implements domain.Provider
│   │   ├── client.go        // HTTP client with auth + timeout
│   │   ├── endpoints.go     // all URL constants and request builders
│   │   └── errors.go        // HTTP status -> typed domain errors
│   │
│   ├── acp/                 // ACP runtime client
│   │   ├── client.go        // POST /runs, GET /runs/{id}
│   │   ├── runner.go        // submit run, poll to completion
│   │   ├── builder.go       // ActionPlan -> ACP run payload per intent
│   │   └── types.go         // ACPRun, ACPStep, ACPRunResult, ACPStatus
│   │
│   ├── session/             // Redis-backed session store
│   │   ├── store.go         // SessionStore interface + Redis implementation
│   │   └── types.go         // Session, PendingPlan
│   │
│   └── llm/                 // LLM provider abstraction
│       ├── client.go        // LLMClient interface
│       ├── anthropic.go     // Anthropic implementation
│       └── openai.go        // OpenAI implementation (alternative)
│
├── prompts/
│   └── intent_classifier.md // system prompt, versioned
│
├── config/
│   └── config.go            // env vars, validated at startup
│
├── .env.example
├── Dockerfile
├── go.mod                   // module bookably-agent
└── README.md
```

## 3. Package descriptions

### `internal/bot` - entry layer

Responsibility: receive Telegram updates, route intent pipeline, manage streaming responses, handle `callbackQuery` for confirm/cancel.

- Owns:
  - `handler.go`: update routing logic
  - `streaming.go`: `sendMessageDraft` state machine
  - `keyboard.go`: inline keyboard builders with prefixed `callback_data`
- Calls: `internal/interpreter`, `internal/session`, `internal/acp`, Telegram Bot API HTTP

### `internal/interpreter` - intelligence layer

Responsibility: receive user message + dialog history, call LLM, parse structured JSON into `ActionPlan`.  
Knows nothing about Bookably or Telegram.

- Owns: `ActionPlan`, `Intent`, `ActionParams`, `Clarification`; prompt path; JSON parser
- Calls: `internal/llm` only (`LLMClient` interface)
- Explicitly does NOT call: `domain.Provider`, ACP

### `internal/domain` - semantic boundary

Responsibility: define `Provider` interface and shared domain types.  
Contains no logic; pure interfaces/value types.

- Owns: `Provider`, `Booking`, `Slot`, `Preview`, `Conflict`, `RiskLevel`, typed errors
- Calls: nothing (leaf package)

### `internal/bookably` - domain adapter

Responsibility: only package aware of Bookably HTTP API. Implements `domain.Provider`.  
Translates `ActionParams` to HTTP requests. Maps Bookably responses/errors to domain types.

- Owns: Bookably HTTP client, endpoint constants, error mapping, `BuildPreview` implementations
- Calls: Bookably HTTP API
- Explicitly does NOT import: `internal/acp`, `internal/bot`

### `internal/acp` - execution layer

Responsibility: submit ACP runs for confirmed write operations and poll completion.  
Build ACP payload from `ActionPlan`. Handle ACP-specific error types/retry logic.

- Owns: `ACPRun`, `ACPStep`, `ACPRunResult`, `ACPStatus`, run builder per intent, poll loop/timeout
- Calls: ACP runtime HTTP API
- Explicitly does NOT import: `internal/bookably`, `internal/interpreter`

### `internal/session` - state layer

Responsibility: Redis-backed session store. Manage per-chat state: pending plan, preview `message_id`, dialog history, timezone, expiry.

- Owns: `SessionStore`, `Session`, `PendingPlan`, Redis key schema, TTL rules
- Calls: Redis via `go-redis`
- Explicitly does NOT import: other internal packages

### `internal/llm` - LLM abstraction

Responsibility: single interface `LLMClient` with Anthropic/OpenAI implementations.

- Owns: `LLMClient`, `AnthropicClient`, `OpenAIClient`, `Message`
- Calls: LLM HTTP APIs
- Explicitly does NOT import: other internal packages

## 4. Core interfaces

These are the architectural seams. Dependency flow must pass through them.

### 4.1 `domain.Provider`

Defined in `internal/domain/provider.go`.  
Implemented by `internal/bookably/adapter.go`.  
Called by `internal/bot` (preview) and indirectly by `internal/acp` (writes via HTTP steps).

```go
// Provider is the interface between the agent and any booking backend.
// All methods are context-aware and return typed domain errors.
type Provider interface {
    // --- Read operations (called during preview, no side effects) ---

    // GetBookings returns bookings matching the filter.
    GetBookings(ctx context.Context, providerID string, f BookingFilter) ([]Booking, error)

    // FindSlots returns available slots for a service within constraints.
    // Returns at most maxResults slots ordered by start time.
    FindSlots(ctx context.Context, providerID string, req SlotSearchRequest) ([]Slot, error)

    // GetProviderInfo returns timezone and service list for a provider.
    GetProviderInfo(ctx context.Context, providerID string) (*ProviderInfo, error)

    // --- Preview builders (read-only, compute impact without writing) ---

    // PreviewAvailabilityChange computes the impact of a working-hours mutation:
    // slots added/removed, conflicting bookings.
    PreviewAvailabilityChange(ctx context.Context, providerID string, p ActionParams) (*Preview, error)

    // PreviewBookingCreate returns proposed slots and client resolution.
    PreviewBookingCreate(ctx context.Context, providerID string, p ActionParams) (*Preview, error)

    // PreviewBookingCancel returns the booking to be cancelled.
    PreviewBookingCancel(ctx context.Context, providerID string, p ActionParams) (*Preview, error)
}
```

### 4.2 `session.SessionStore`

Defined in `internal/session/store.go`.  
Implemented by Redis-backed store.  
Called only by `internal/bot`.

```go
type SessionStore interface {
    // Get returns the session for a chat, or a new empty session if absent.
    Get(ctx context.Context, chatID int64) (*Session, error)

    // Save persists the session with configured TTL (24h).
    // Resets TTL on every call.
    Save(ctx context.Context, s *Session) error

    // Delete removes session. Called on /reset command.
    Delete(ctx context.Context, chatID int64) error
}

type Session struct {
    ChatID        int64
    ProviderID    string
    Timezone      string       // IANA tz string, e.g. "Europe/Berlin"
    PendingPlan   *PendingPlan // nil if no plan awaiting confirmation
    DialogHistory []Message    // last 10 messages, oldest first
    UpdatedAt     time.Time
}

type PendingPlan struct {
    ID             string    // short UUID, used in callback_data
    Plan           ActionPlan
    PreviewMsgID   int64     // message_id of the preview message
    CreatedAt      time.Time // used for 15-min expiry check
    IdempotencyKey string    // SHA-256(chatID+planID+intent)
}
```

### 4.3 `llm.LLMClient`

Defined in `internal/llm/client.go`.  
Implemented by `AnthropicClient` and `OpenAIClient`.  
Called only by `internal/interpreter`.

```go
type Message struct {
    Role    string // "system" | "user" | "assistant"
    Content string
}

type Completion struct {
    Content      string
    InputTokens  int
    OutputTokens int
}

type LLMClient interface {
    // Complete sends messages to the LLM and returns completion.
    // Implementors must respect ctx for timeout/cancellation.
    // Returns error on network failure, timeout, or rate limit.
    Complete(ctx context.Context, messages []Message) (*Completion, error)
}
```

## 5. Data flows

Three canonical flows cover all use cases.

### 5.1 Read flow (`list_bookings`, `find_next_slot`)

No ACP run. No confirmation. Direct Bookably read -> format -> respond.

| # | Component | Action | Notes |
| --- | --- | --- | --- |
| 1 | `bot/handler` | Receive update (message). Send `sendChatAction(typing)`. Load session from Redis. | `< 50 ms` target |
| 2 | `bot/handler` | Call `sendMessageDraft` with placeholder to start streaming immediately. | `Ищу...` or similar |
| 3 | `interpreter` | Build LLM messages: system prompt + dialog history + new message. Call `LLMClient.Complete()`. | 15s timeout |
| 4 | `interpreter` | Parse JSON response -> `ActionPlan`. Validate parameters. If clarification needed, return immediately. | Defensive parse |
| 5 | `bot/handler` | If clarification: update draft with question, save session, return. | One question only |
| 6 | `bookably/adapter` | Call `Provider.GetBookings()` or `Provider.FindSlots()` with resolved params. | Read-only API call |
| 7 | `bot/handler` | Format result as MarkdownV2. Complete draft to final message via `sendMessageDraft`. | No keyboard |
| 8 | `bot/handler` | Append exchange to dialog history. Save session. | TTL reset |

### 5.2 Write flow - happy path (`set_working_hours`, `create_booking`, `cancel_booking`, etc.)

Two-phase flow: preview (read-only) then execution (ACP-gated).

| # | Component | Action | Notes |
| --- | --- | --- | --- |
| 1 | `bot/handler` | Receive update. `sendChatAction(typing)`. Load session. Check for stale pending plan. | Replace stale plan with warning |
| 2 | `bot/streaming` | Start `sendMessageDraft` with streaming placeholder. | `Анализирую...` |
| 3 | `interpreter` | Build LLM call with full dialog context. Call `LLMClient.Complete()`. Parse -> `ActionPlan`. | 15s timeout |
| 4 | `bot/handler` | If clarification: update draft, save session (no pending plan), return. |  |
| 5 | `bookably/adapter` | Call `Provider.Preview*()` for classified intent. Return `Preview` with impact/conflicts/risk. | Read-only only |
| 6 | `bot/handler` | Build preview text from `Preview`. Compute `RiskLevel`. Build keyboard: `[confirm:{planID}] [cancel:{planID}]`. | `planID` is short UUID |
| 7 | `bot/streaming` | Complete `sendMessageDraft` with full preview + keyboard. Record `message_id`. |  |
| 8 | `session` | Save `PendingPlan`: `planID`, `ActionPlan`, preview `message_id`, `idempotency_key`, `created_at`. | TTL reset |
| 9 | - | User reviews preview and taps confirm |  |
| 10 | `bot/handler` | Receive `callbackQuery` (`confirm:{planID}`). Answer callback. Load session. | Must answer `< 10s` |
| 11 | `session` | Verify `planID` matches `Session.PendingPlan.ID`. Verify not expired (`< 15 min`). | Reject stale confirm |
| 12 | `bot/handler` | Remove preview keyboard. Send `Выполняю...` status update. | `editMessageReplyMarkup` |
| 13 | `acp/builder` | Build ACP payload from `ActionPlan`: HTTP step, metadata, idempotency key. | One HTTP step per intent |
| 14 | `acp/runner` | `POST /runs`; poll `GET /runs/{id}` every 2s up to 30s. | Bounded poll |
| 15 | `acp/runner` | ACP returns `completed` or `failed`; classify error type. | Policy / transient / domain |
| 16 | `bot/handler` | On success: edit to final confirmation. Clear `PendingPlan`. |  |
| 17 | `bot/handler` | On failure: edit to error + retry/deep-link action. Keep `PendingPlan` for retry. | Reuse same idempotency key |

### 5.3 Clarification sub-loop

If `len(Clarifications) > 0`, flow enters clarification sub-state.

| # | Component | Action | Notes |
| --- | --- | --- | --- |
| 1 | `interpreter` | Return `ActionPlan` with `Clarifications[0]`. Plan is NOT saved as `PendingPlan`. | No pending plan during clarification |
| 2 | `bot/handler` | Update draft with clarification question. No keyboard. Append exchange to dialog history. |  |
| 3 | - | User answers clarification question |  |
| 4 | `interpreter` | New message + updated history -> LLM call -> new `ActionPlan`. Clarifications may now be empty. | Context resolves ambiguity |
| 5 | `bot/handler` | If still clarifications: check `clarification_count`. If `>= 2`, surface deep link and stop. | CC-06 escape rule |
| 6 | `bot/handler` | If no clarifications: continue normal preview/read flow. |  |

## 6. Redis key schema

All agent-owned Redis keys use `ba:` prefix (`bookably-agent`).  
All keys use JSON values unless noted. TTL resets on each write.

### 6.1 Session key

| Field | Value |
| --- | --- |
| Key pattern | `ba:session:{chat_id}` |
| Type | Redis String (JSON-encoded `Session`) |
| TTL | 24 hours, reset on every `Save()` |
| Size estimate | ~2 KB per session (10-message history + pending plan) |

Session JSON structure:

```json
{
  "chat_id": 123456789,
  "provider_id": "prov_abc123",
  "timezone": "Europe/Berlin",
  "clarification_count": 0,
  "pending_plan": {
    "id": "f3a9b2",
    "plan": {},
    "preview_msg_id": 987654321,
    "created_at": "2026-03-21T14:30:00Z",
    "idempotency_key": "sha256:a1b2c3d4..."
  },
  "dialog_history": [
    { "role": "user", "content": "Покажи записи на завтра" },
    { "role": "assistant", "content": "Записи на 22 марта: ..." }
  ],
  "updated_at": "2026-03-21T14:32:00Z"
}
```

### 6.2 Provider preferences cache (optional)

| Field | Value |
| --- | --- |
| Key pattern | `ba:prefs:{provider_id}` |
| Type | Redis String (JSON) |
| TTL | 5 minutes |
| Contents | Timezone, services list (IDs + durations) |

### 6.3 Idempotency check (optional, belt-and-suspenders)

| Field | Value |
| --- | --- |
| Key pattern | `ba:idem:{idempotency_key}` |
| Type | Redis String (ACP `run_id`) |
| TTL | 1 hour |
| Purpose | If confirm fires twice, second call reuses existing `run_id` instead of submitting a new run |

## 7. ACP run payloads

ACP runs are submitted to `POST /runs`.  
Run contains a single HTTP step calling relevant Bookably write endpoint.  
Agent never calls Bookably write endpoints directly after confirm.

Idempotency key scheme:

`idempotency_key = hex(SHA-256(chat_id + ":" + plan_id + ":" + intent))`

Key is generated before run submission and stored in `PendingPlan`. Retry reuses same key.

### 7.1 `set_working_hours`

```json
{
  "idempotency_key": "a1b2c3...",
  "metadata": {
    "chat_id": "123456789",
    "provider_id": "prov_abc",
    "intent": "set_working_hours",
    "risk_level": "medium",
    "raw_message": "На следующей неделе работаю с 12 до 20"
  },
  "steps": [{
    "type": "http",
    "capability": "availability.set_working_hours",
    "config": {
      "method": "POST",
      "url": "https://api.bookably.app/v1/providers/{provider_id}/availability",
      "headers": { "Authorization": "Bearer {provider_token}" },
      "body": {
        "date_range": { "from": "2026-03-23", "to": "2026-03-26" },
        "weekdays": ["mon", "tue", "wed", "thu"],
        "working_hours": { "from": "12:00", "to": "20:00" },
        "breaks": [{ "from": "15:00", "to": "16:00" }]
      }
    }
  }]
}
```

### 7.2 `create_booking`

```json
{
  "idempotency_key": "b2c3d4...",
  "metadata": { "intent": "create_booking", "risk_level": "medium" },
  "steps": [{
    "type": "http",
    "capability": "booking.create",
    "config": {
      "method": "POST",
      "url": "https://api.bookably.app/v1/providers/{provider_id}/bookings",
      "headers": { "Authorization": "Bearer {provider_token}" },
      "body": {
        "client_id": "client_xyz",
        "service_id": "svc_massage",
        "starts_at": "2026-03-22T18:30:00+03:00",
        "duration_min": 60
      }
    }
  }]
}
```

### 7.3 `cancel_booking`

```json
{
  "idempotency_key": "c3d4e5...",
  "metadata": { "intent": "cancel_booking", "risk_level": "high" },
  "steps": [{
    "type": "http",
    "capability": "booking.cancel",
    "config": {
      "method": "DELETE",
      "url": "https://api.bookably.app/v1/providers/{provider_id}/bookings/{booking_id}",
      "headers": { "Authorization": "Bearer {provider_token}" }
    }
  }]
}
```

`add_break` and `close_range` follow `set_working_hours` pattern with different capabilities (`availability.add_break`, `availability.close_range`) and body fields.

## 8. Package dependency rules

Import direction is strictly controlled and enforced in CI.  
Graph is a DAG; cycles are forbidden.

```text
// Allowed import directions (A -> B means A may import B)

cmd/agent -> internal/bot, internal/session, internal/bookably, internal/acp, internal/llm, config

internal/bot -> internal/interpreter, internal/session, internal/acp, internal/domain

internal/interpreter -> internal/llm, internal/domain

internal/bookably -> internal/domain

internal/acp -> internal/domain

internal/session -> (no internal imports)

internal/llm -> (no internal imports)

internal/domain -> (no internal imports)  // leaf package

// FORBIDDEN:
// internal/domain -> anything else
// internal/bookably -> internal/acp
// internal/acp -> internal/bookably
// internal/interpreter -> internal/bookably
// internal/interpreter -> internal/acp
```

Why `interpreter` never imports `bookably`:

Interpreter produces `ActionPlan` from language only. It must not know Bookably endpoint shape, auth model, or transport details.  
Bot orchestrates handoff from `ActionPlan` to preview (`bookably`) and to execution (`acp`), enabling interpreter unit tests without HTTP mocking.

## 9. Configuration

All config is loaded from environment variables at startup. Missing required variables cause fatal startup error.

| Variable | Required |
| --- | --- |
| `TG_BOT_TOKEN` | Yes |
| `TG_WEBHOOK_SECRET` | Yes |
| `REDIS_URL` | Yes |
| `ACP_BASE_URL` | Yes |
| `ACP_API_KEY` | Yes |
| `BOOKABLY_API_URL` | Yes |
| `BOOKABLY_API_KEY` | Yes |
| `LLM_PROVIDER` | Yes |
| `LLM_API_KEY` | Yes |
| `LLM_MODEL` | No |
| `MINI_APP_URL` | Yes |
| `PORT` | No |
| `LOG_LEVEL` | No |

## 10. Error handling strategy

### 10.1 Error taxonomy

| Error type | Origin | Recovery strategy |
| --- | --- | --- |
| `ErrClarification` | interpreter | Ask one clarification question and continue intent cycle |
| `ErrUnknownIntent` | interpreter | Return onboarding hint with examples |
| `ErrLLMTimeout` | llm | Return retry + Mini App fallback |
| `ErrNotFound` | bookably | Show not-found message, no retry by default |
| `ErrConflict` | bookably | Show conflict details, suggest alternative/deep link |
| `ErrValidation` | bookably | Show actionable correction message |
| `ErrUpstream (5xx)` | bookably / ACP | Show transient error, offer retry |
| `ErrPlanExpired` | session | Reject callback, ask user to repeat request |
| `ErrACPPolicyViolation` | acp | Show policy reason, no retry |
| `ErrACPTimeout` | acp | Offer retry with same idempotency key |

### 10.2 Logging contract

Every handled error MUST be logged as structured JSON with at least:

```json
{
  "level": "error",
  "trace_id": "uuid",
  "chat_id": 123456789,
  "provider_id": "prov_abc",
  "intent": "cancel_booking",
  "error": "bookably: 409 conflict on booking_id=xyz",
  "error_type": "ErrConflict",
  "duration_ms": 234,
  "component": "bookably/adapter"
}
```
