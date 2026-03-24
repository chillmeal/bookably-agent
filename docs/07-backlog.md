# BOOKABLY AGENT

## Implementation Backlog

Version 1.0 | March 2026 | Doc 07 of series  
Live document - update status and check off criteria as work progresses.

## Canonical tracker note

This file is the canonical live status tracker for implementation progress.  
`ENGINEERING_PLAN.md` is maintained in parallel as an execution log and mirrored ticket-state ledger.

## Latest update (2026-03-24)

- Iteration 17 stabilization pass completed (`DONE`) as runtime hardening over existing phases.
- ACP deploy contour switched to always-on in agent compose (`agent + redis + acp + postgres`), so `ACP_BASE_URL=http://acp:8080` is stable by default.
- OpenRouter SSE parser fixed for token spacing and multi-line `data:` events.
- Handler streaming moved to hybrid mode (SSE progress + heartbeat draft updates every ~2s).
- Large availability impact (`>20`) changed from hard deep-link block to recommendation-only (confirm remains available in chat).
- `editMessageReplyMarkup ... message is not modified` treated as idempotent success (no noisy flow break).
- User-facing formatter unified to structured sections: `Понял` / `Что сделаю` / `Действие` / `Что дальше`.
- Runtime limitations unchanged: `P2-05` remains `IN PROGRESS` (live suite pending controlled budget), `create_booking` confirm execution remains contract-blocked.

## Status legend

| Status | Meaning |
| --- | --- |
| TODO | Not started |
| IN PROGRESS | Active |
| DONE | All criteria met |
| BLOCKED | External dependency |

Doc refs:

- `01` = Vision
- `02` = UseCases
- `03` = Requirements
- `04` = Architecture
- `05` = APIContract
- `06` = ImplGuide

---

## Phase 0 - Foundation & Domain Types

Scaffold everything. Zero logic: structure, config, interfaces, and types.

### P0-01 - Initialise repository

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `cmd/agent`, `go.mod`
- Refs: Doc 04 section 2, Doc 06 section 2

Acceptance criteria:

- [x] `go.mod` created with module name `github.com/chillmeal/bookably-agent`
- [x] All dependencies from Doc 06 section 2 present with correct versions
- [x] Directory tree matches Doc 04 section 2 exactly (all folders created, `.gitkeep` where empty)
- [x] `docker-compose.yml` starts Redis on port 6379 successfully
- [x] `go build ./...` succeeds with zero errors
- [x] `go vet ./...` produces no warnings
- [x] `.env.example` committed, `.env` in `.gitignore`
- [x] `README.md` includes project description, setup steps, env vars table, and run instructions

### P0-02 - Config package

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `config/config.go`
- Refs: Doc 03 section 10, Doc 06 section 3

Acceptance criteria:

- [x] All env vars from Doc 06 section 3 present in `Config` struct with correct types
- [x] `config.Load()` returns clear error when required variable is missing
- [x] Missing `TG_BOT_TOKEN` -> error names missing variable
- [x] Missing `LLM_API_KEY` -> error names missing variable
- [x] All duration fields parse correctly (`15m`, `2s`, `24h`)
- [x] Unit test `TestLoadConfig_MissingRequired` covers 5 different missing vars
- [x] Unit test `TestLoadConfig_ValidEnv` parses complete `.env` and asserts all fields

### P0-03 - Domain types and interfaces

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/domain/`
- Refs: Doc 04 section 4, Doc 03 section 7

Acceptance criteria:

- [x] `domain/types.go`: `Booking`, `Slot`, `Preview`, `Conflict`, `AvailabilityChange`, `RiskLevel`, `BookingStatus` defined
- [x] `domain/types.go`: typed errors defined: `ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrUpstream`, `ErrUnauthorized`, `ErrForbidden`, `ErrRateLimit`
- [x] `domain/provider.go`: `Provider` interface defined with all methods from Doc 04 section 4.1
- [x] `domain` imports no other internal package (leaf package rule from Doc 04 section 8)
- [x] `go vet` for domain package produces zero warnings
- [x] Unit test: domain types JSON-serializable with correct field names

Note: `domain` is a leaf. If importing from `internal/` here, stop and redesign.

---

## Phase 1 - Infrastructure

Session store and LLM client. Independently testable, no business logic.

### P1-01 - Redis session store

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/session/`
- Refs: Doc 03 section 5, Doc 04 section 6, Doc 06 section 4

Acceptance criteria:

- [x] `SessionStore` interface defined with `Get`, `Save`, `Delete`
- [x] `Session` struct matches Doc 04 section 6.1 JSON example exactly
- [x] `PendingPlan` struct includes `ID`, `Plan (ActionPlan)`, `PreviewMsgID`, `CreatedAt`, `IdempotencyKey`
- [x] `RedisStore.Get()` returns empty `Session` (not error) for unknown `chatID`
- [x] `RedisStore.Save()` sets TTL from `config.SessionTTL` on every call
- [x] `RedisStore.Save()` resets TTL even if key already exists
- [x] `Session.PendingPlan` is `nil` when no plan pending
- [x] `DialogHistory` capped at 10 entries (oldest dropped on overflow)
- [x] Unit test with `miniredis`: `Get -> Save -> Get` round-trip preserves all fields
- [x] Unit test: `Save()` with TTL=1s -> sleep 2s -> `Get()` returns empty session
- [x] Unit test: `PendingPlan` survives process restart (write, new client, read)
- [x] Redis key format `ba:session:{chatID}` verified after `Save()`

Note: use `miniredis` for unit tests; no real Redis dependency in unit tests.

### P1-02 - LLM client: interface + Anthropic/OpenAI implementations

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/llm/`
- Refs: Doc 03 section 8, Doc 06 section 4.3

Acceptance criteria:

- [x] `LLMClient` interface: `Complete(ctx, []Message) (*Completion, error)`
- [x] `Message` struct: `Role` (`system/user/assistant`), `Content string`
- [x] `Completion` struct: `Content string`, `InputTokens int`, `OutputTokens int`
- [x] `AnthropicClient` reads API key and model from config
- [x] `AnthropicClient` sets request timeout from `config.LLMTimeout` (default 15s)
- [x] `AnthropicClient` returns error on HTTP `429` (rate limit)
- [x] `AnthropicClient` returns error on context deadline exceeded
- [x] Unit test: mock HTTP server returns valid Anthropic response -> parsed `Completion`
- [x] Unit test: mock HTTP server returns `500` -> error returned, no panic
- [x] Unit test: context canceled mid-request -> error returned
- [x] `llm` package imports no other internal package

Note: OpenAI implementation is included in this iteration in addition to Anthropic.

---

## Phase 2 - Interpreter

NL -> `ActionPlan` intelligence layer.

### P2-01 - ActionPlan types and JSON contracts

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/interpreter/types.go`
- Refs: Doc 04 section 3, Doc 06 section 4

Acceptance criteria:

- [x] `Intent` type and all 8 intent constants defined (Doc 04 section 3)
- [x] `Intent.RequiresConfirm()` returns correct bool for all 8 intents
- [x] `ActionParams` covers all fields from Doc 06 section 4 prompt parameter reference
- [x] `Clarification` struct: `Field string`, `Question string`
- [x] `ActionPlan` struct includes `Intent`, `Confidence float64`, `RequiresConfirm bool`, `Params ActionParams`, `Clarifications []Clarification`, `RawUserMessage string`, `Timezone string`
- [x] `ActionPlan.NeedsClarification()` returns true iff `len(Clarifications) > 0`
- [x] Unit test: marshal->unmarshal round-trip preserves all fields for each intent
- [x] Unit test: unknown intent string unmarshals to `IntentUnknown`

### P2-02 - Prompt loader and template renderer

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/interpreter/prompts.go`
- Refs: Doc 06 section 4

Acceptance criteria:

- [x] Prompt loaded from `prompts/intent_classifier.md` at runtime (not embedded/hardcoded)
- [x] Missing prompt file returns error with explicit file path
- [x] Runtime placeholders injected using provider timezone: `{{TODAY_DATE}}`, `{{TODAY_WEEKDAY}}`, `{{TIMEZONE}}`, `{{CURRENT_TIME}}`
- [x] Unit test: successful render validates placeholder substitution
- [x] Unit test: missing file returns path-aware error
- [x] Unit test: nil timezone returns explicit validation error

### P2-03 - JSON parser: defensive ActionPlan parsing

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/interpreter/parser.go`
- Refs: Doc 03 FR-LM-03

Acceptance criteria:

- [x] `ParseActionPlan(string) (*ActionPlan, error)` defined
- [x] Valid JSON -> correct `ActionPlan` with all fields
- [x] JSON wrapped in markdown fences (```json ... ```) -> parsed correctly
- [x] Prose text response -> returns `IntentUnknown`, no error
- [x] Partial/truncated JSON -> returns `IntentUnknown`, no panic
- [x] `confidence < 0.50` overrides parsed intent to `IntentUnknown`
- [x] Unit test coverage: valid JSON, fenced JSON, prose, partial JSON, low confidence

Note: do not propagate JSON parse failure as hard error; return `unknown`.

### P2-04 - Interpreter orchestrator

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/interpreter/interpreter.go`
- Refs: Doc 03 section 1, Doc 04 section 3

Acceptance criteria:

- [x] `Interpret(ctx, userMessage string, convo ConversationContext) (*ActionPlan, error)` defined
- [x] System prompt loaded and rendered from file with conversation timezone
- [x] Dialog history (capped to 10 turns) + new user message appended to LLM message slice
- [x] Calls `LLMClient.Complete()` and parses via `ParseActionPlan()`
- [x] LLM timeout enforced via context deadline
- [x] Parser fallback (`prose/invalid`) returns `IntentUnknown` without hard error
- [x] `interpreter` imports only `internal/llm` from internal packages (no `bookably`/`acp`/`session`)
- [x] Unit test: mocked LLM success path returns expected `ActionPlan`
- [x] Unit test: mocked timeout/cancel returns error within configured timeout

### P2-05 - Classifier test suite (35 cases)

- Status: `IN PROGRESS`
- Status transitions: `TODO → IN PROGRESS` (2026-03-22)
- Component: `internal/interpreter/`
- Refs: Doc 06 section 5

Acceptance criteria:

- [x] Integration harness with 35 table-driven cases added (`//go:build integration`)
- [x] Harness is gated by env (`LLM_PROVIDER`, `LLM_API_KEY`) and skips with explicit reason when missing
- [x] Harness fixes context to `now=2026-03-22`, `tz=Europe/Berlin` via prompt clock injection
- [ ] All 35 return correct expected intent
- [ ] All 35 meet minimum confidence floor
- [ ] Cases `30-35` (`unknown/edge`) return `IntentUnknown`
- [ ] Cases `4, 8, 10, 11, 21, 22, 28` produce exactly 1 clarification
- [ ] No case produces more than 1 clarification
- [x] Test is tagged `//go:build integration` and skipped in unit runs

Run:

`go test -tags integration -run TestClassifier ./internal/interpreter/`

Latest live run note (2026-03-23):

- `LLM_PROVIDER=amvera`, `LLM_MODEL=gpt-5` executed against real endpoint.
- Status remains `IN PROGRESS`: provider returned `402` (`Run out of tokens` / billing blocked), so 35-case acceptance gate could not be completed.
- Strict policy remains in force: `amvera` is `gpt-5` only, no model fallback.

---

## Phase 3 - Bookably Adapter

HTTP client + `domain.Provider` implementation.

### P3-01 - Resolve Bookably open questions

- Status: `DONE`
- Status transitions: `BLOCKED → IN PROGRESS → DONE` (2026-03-22)
- Component: `-`
- Refs: Doc 05 section 7

Acceptance criteria:

- [x] Q1 answered: `POST /specialist/slots` body schema confirmed (`startAt`, `endAt`)
- [x] Q2 answered: `schedule/commit` requirement confirmed as operational commit endpoint with mutation groups
- [x] Q3 answered: specialist-initiated create-booking for arbitrary named client **not exposed** as dedicated backend contract (gap fixed via runtime fail-safe block)
- [x] Q4 answered: `GET /specialist/slots` range query params confirmed (`from`, `to`)
- [x] Q5 answered: execution-time booking conflict code identified (`409 SLOT_NOT_AVAILABLE`; plus related slot conflict codes)
- [x] Answers documented in comment block in `internal/bookably/endpoints.go`

Note: backend gap remains for specialist-initiated create-booking execution on arbitrary named client; agent keeps preview/find flows, but confirm execution is fail-safe blocked with Mini App fallback until backend contract appears.

### P3-02 - Bookably HTTP client with auth

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/client.go`
- Refs: Doc 05 section 2, section 5

Acceptance criteria:

- [x] `Client` includes `specialistID`, `tokenStore`, `httpClient`, `baseURL`, `timeout`
- [x] `do()` loads token from Redis, sets `Authorization`, performs request
- [x] On `401`: refresh once and retry original request
- [x] On second `401`: returns `ErrUnauthorized`
- [x] On `429`: read `Retry-After`, wait/retry if wait `< 30s`, else `ErrRateLimit`
- [x] On `5xx`: return `ErrUpstream` with status in message
- [x] All requests use 5s timeout from config (default, configurable)
- [x] Unit test: `401 -> refresh -> retry success`
- [x] Unit test: `429 Retry-After: 1` -> retried after 1s
- [x] Unit test: `500` -> `ErrUpstream`
- [x] `bookably/client` imports no internal package except `domain`

### P3-03 - Token store: acquire and refresh

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/client.go`
- Refs: Doc 05 section 2.2

Acceptance criteria:

- [x] `TokenStore` interface: `GetToken`, `SaveToken`
- [x] `RedisTokenStore` stores token at `ba:token:{specialistID}`
- [x] Stored JSON: `{ accessToken, refreshToken, expiresAt }`
- [x] `GetToken` returns token or `ErrUnauthorized` if absent
- [x] `POST /api/v1/auth/tma` implemented (`AcquireTokenWithInitData`)
- [x] `POST /api/v1/auth/refresh` called on `401` retry path
- [x] Refresh uses distributed lock (`ba:token:{id}:lock`) to prevent stampede
- [x] Unit test: `GetToken` after `SaveToken` returns same token
- [x] Unit test: concurrent refresh attempts -> only one HTTP refresh call

### P3-04 - Error mapping

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/errors.go`
- Refs: Doc 05 section 5.2

Acceptance criteria:

- [x] `mapHTTPError(statusCode int, body []byte) error` defined
- [x] `404 -> ErrNotFound` with booking/slot context
- [x] `409 -> ErrConflict`
- [x] `422 -> ErrValidation` with `error.message` from response
- [x] `401 -> ErrUnauthorized`
- [x] `403 -> ErrForbidden`
- [x] `5xx -> ErrUpstream` with status code
- [x] All domain error types implement `error`
- [x] `errors.Is(err, domain.ErrNotFound)` works
- [x] Unit tests cover all mapped status codes

### P3-05 - GetBookings (`list_bookings`)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 05 section 4.1, Doc 03 FR-P-11/12/13

Acceptance criteria:

- [x] Calls `GET /api/v1/specialist/bookings` with `from`, `to`, `status`, `limit`, `direction`
- [x] Converts local specialist range to UTC query timestamps
- [x] Follows cursor pagination until all range results are fetched
- [x] Maps response to `[]domain.Booking` per Doc 05 section 6.1
- [x] Client display name fallback: `firstName + lastName`, then `telegramUsername`
- [x] Status mapping: `CONFIRMED -> BookingStatusUpcoming`, `CANCELLED -> BookingStatusCancelled`
- [x] Slot times stored as UTC `time.Time`
- [x] Unit test: API returns 2 bookings -> 2 mapped domain bookings
- [x] Unit test: pagination follows page 2 when `nextCursor` exists

### P3-06 - FindSlots (`find_next_slot` + `create_booking` preview)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 05 section 4.3

Acceptance criteria:

- [x] Calls `GET /api/v1/public/slots` with `serviceId`, `from`, `to`
- [x] `from = not_before || now()`, `to = from + 7 days`
- [x] Returns max 2 slots ordered by `startAt` ascending
- [x] Slot fields set: `ID`, `Start`, `End` (UTC), `ServiceID` from query
- [x] Returns `ErrNotFound` when no slots in range
- [x] Unit test: mock 5 slots -> first 2 returned
- [x] Unit test: mock 0 slots -> `ErrNotFound`

### P3-07 - GetServices (service name resolution)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 05 section 4.5

Acceptance criteria:

- [x] Calls `GET /api/v1/public/services` with `specialistId`
- [x] Filters `isActive=true` only
- [x] Cache in Redis key `ba:prefs:{specialistId}` with 5-minute TTL
- [x] `ResolveServiceByName(name)` uses case-insensitive substring match on `title`
- [x] Returns `ErrNotFound` if no active match
- [x] Returns `ErrConflict` if more than one match (triggers clarification)
- [x] Unit test: `масс` matches `Массаж 60 мин`
- [x] Unit test: `маникюр` single match -> returned
- [x] Unit test: `процедура` two matches -> `ErrConflict`
- [x] Unit test: `xyz` no matches -> `ErrNotFound`

### P3-08 - PreviewAvailabilityChange (`set_working_hours`, `add_break`, `close_range`)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 03 FR-PV-03, Doc 05 section 4.6

Acceptance criteria:

- [x] Calls `GET /specialist/slots` for existing slots in affected range
- [x] Calls `GET /specialist/bookings` for bookings in same range
- [x] Computes deleted slots (`close_range` / `add_break`) or replaced slots
- [x] Computes created slots (`set_working_hours`)
- [x] Detects conflicting bookings where `booking.slot.startAt` is in deleted window
- [x] Returns `Preview` with `AvailabilityChange` + `Conflicts`
- [x] `RiskLevel`: High if conflicts, Medium if `>10` slots affected, Low otherwise
- [x] No write calls during preview (validated by mock assertions)
- [x] Unit test: fixture with 3 slots + 1 conflict booking -> preview has 1 conflict
- [x] Unit test: empty range -> `added=0`, `removed=0`, `conflicts=0`

### P3-09 - PreviewBookingCreate

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 03 FR-PV-04

Acceptance criteria:

- [x] Calls `FindSlots` for availability
- [x] Calls `ResolveServiceByName` for service details
- [x] Returns `Preview` with `ProposedSlots` (max 2)
- [x] Slot display values are specialist local time (converted from UTC)
- [x] Returns `ErrNotFound` if no slots
- [x] Unit test: 2 available slots -> `Preview.ProposedSlots` length is 2

### P3-10 - PreviewBookingCancel

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bookably/adapter.go`
- Refs: Doc 03 FR-PV-05

Acceptance criteria:

- [x] Calls `GetBookings` filtered by client-name substring
- [x] Returns `ErrNotFound` when no match
- [x] Returns `ErrConflict` when multiple matches (disambiguation needed)
- [x] Returns `Preview` with `BookingResult` for single match
- [x] `RiskLevel` always High for cancel
- [x] Unit test: 1 match -> `Preview.BookingResult` populated
- [x] Unit test: 2 matches -> `ErrConflict`
- [x] Unit test: 0 matches -> `ErrNotFound`

### P3 hardening note (post-DONE quality pass)

- Status: `DONE` (quality hardening, no ticket state change)
- Date: `2026-03-22`
- Scope:
  - Fixed break-window validation path so invalid breaks are no longer silently ignored during working-hours slot build.
  - Removed silent `/me` error suppression in `GetProviderInfo`; upstream failure now propagates explicitly.
- Added regression tests:
  - `TestBuildWorkingHoursSlots_InvalidBreakReturnsValidation`
  - `TestGetProviderInfo_ReturnsMeError`
- Follow-up (tech debt, non-blocking for P4 start):
  - Date-range calculations are currently UTC-centric in availability preview internals.
  - `429` handling for `Retry-After > 30s` should be rechecked for exact Doc 05/Doc 07 wording alignment.

---

## Phase 4 - ACP Client

Submit runs, poll completion, build payloads per intent.

### P4-01 - ACP types and HTTP client

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/acp/`
- Refs: Doc 04 section 7, Doc 04 section 4

Acceptance criteria:

- [x] `ACPRun`, `ACPStep`, `ACPRunResult`, `ACPStatus` types defined
- [x] `ACPStatus` constants include `Pending`, `Running`, `Completed`, `Failed`
- [x] HTTP client for `POST /runs` and `GET /runs/{id}`
- [x] All ACP requests include `ACP_API_KEY` in `Authorization`
- [x] Unit test: mock ACP -> run submitted -> `run_id` returned
- [x] Unit test: mock ACP failed status -> error returned

### P4-02 - ACP runner: submit and poll

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/acp/runner.go`
- Refs: Doc 03 FR-EX-04/05/06

Acceptance criteria:

- [x] `SubmitAndWait(ctx, ACPRun) (*ACPRunResult, error)` defined
- [x] Polls `GET /runs/{id}` every `config.ACPPollInterval` (default 2s)
- [x] Stops when status is `completed` or `failed`
- [x] Times out after `config.ACPPollTimeout` (default 30s)
- [x] On completed -> returns `ACPRunResult` output
- [x] On failed -> classifies policy/transient/domain errors
- [x] Unit test: submitted -> polled 3x -> completed -> result returned
- [x] Unit test: always running -> timeout at 30s
- [x] Unit test: failed with policy code -> `ErrACPPolicyViolation`

### P4-03 - ACP run builder (per intent)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/acp/builder.go`
- Refs: Doc 04 section 7.1-7.3, Doc 06 section 6.6

Acceptance criteria:

- [x] `BuildCancelBookingRun()` -> single HTTP step `POST /specialist/bookings/{id}/cancel`
- [x] `BuildCreateBookingRun()` -> single HTTP step `POST /public/bookings`
- [x] `BuildAvailabilityRun(create, delete)` -> single HTTP step `POST /specialist/schedule/commit` with operation-group payload in body
- [x] Each step has correct capability string (`booking.cancel`, `booking.create`, etc.)
- [x] Each step passes `Idempotency-Key` from `PendingPlan.IdempotencyKey`
- [x] Each run metadata includes `chat_id`, `specialist_id`, `intent`, `risk_level`, `raw_message`
- [x] Commit key format: `{baseKey}:commit`
- [x] Unit test: `BuildCancelBookingRun` validates step URL/method/headers
- [x] Unit test: availability run validates commit body shape (`create[]`, `delete[]`) and commit idempotency key

Notes:

- Availability execution is now strict-real via `/specialist/schedule/commit` operation-group payload.
- `cancel_booking` step uses Doc 05 precedence (`POST .../cancel`) and is canonicalized in this ticket.
- `create_booking` execution remains fail-safe blocked until backend publishes specialist-initiated create-booking contract for named clients.

---

## Phase 5 - Bot Layer

Telegram protocol, streaming, keyboards, session helpers.

### P5-01 - Streaming (`sendMessageDraft` wrapper)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/streaming.go`
- Refs: Doc 06 section 6.1, Doc 03 FR-TG-01

Acceptance criteria:

- [x] `Streamer` has `Draft(ctx, chatID, text)` and `Finalize(ctx, chatID, text, keyboard)` methods
- [x] `Draft()` calls Bot API `POST /sendMessageDraft`
- [x] `Finalize()` sends final `sendMessage` with `MarkdownV2`
- [x] `Finalize()` returns `message_id` for session storage
- [x] `Draft()` is concurrency-safe (internal mutex)
- [x] Unit test: mock Bot API -> 3 drafts + finalize -> `message_id` returned
- [x] Unit test: context canceled mid-stream -> `Draft()` returns error, no panic

Note: `sendMessageDraft`/style-keyboard are implemented via raw HTTP; finalize includes one fallback retry without `style` on style-related `400`.

### P5-02 - Keyboard builders

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/keyboard.go`
- Refs: Doc 03 FR-TG-04/05, Doc 06 section 6.3

Acceptance criteria:

- [x] `ConfirmData(planID)` / `CancelData(planID)` generate correct prefixed callback data
- [x] `SlotData(idx, planID)` generates `slot:{idx}:{planID}`
- [x] `ParseCallback(data)` parses all three formats
- [x] `BuildPreviewKeyboard(planID)` -> `[[Confirm][Cancel]]`
- [x] `BuildSlotKeyboard(planID, slots, tz)` -> slot buttons + Cancel
- [x] Slot button labels use local time (`Сегодня 18:30` or `22 марта 18:30`)
- [x] Confirm button style is success (green) via `InlineKeyboardButton.style`
- [x] Cancel button style is danger (red)
- [x] Unit test: parse callback round-trip for all formats
- [x] Unit test: invalid callback data -> parse error

### P5-03 - Preview text formatters

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/format.go`
- Refs: Doc 03 FR-TG-03, Doc 06 section 6.5

Acceptance criteria:

- [x] `escapeV2(string)` escapes all MarkdownV2 specials
- [x] `FormatAvailabilityPreview(preview)` returns complete preview text
- [x] `FormatBookingListPreview(bookings, tz)` returns sorted booking list
- [x] `FormatCancelPreview(preview)` includes irreversibility warning
- [x] `FormatCreatePreview(preview, tz)` renders proposed slots in local time
- [x] `FormatFindSlotResult(slots, tz)` renders max 2 slots in local time
- [x] `FormatClarification(q)` returns single clarification question
- [x] `FormatError(errType)` returns user-facing error (max 2 sentences)
- [x] `FormatUnknownIntent()` returns onboarding hint with 3 examples
- [x] Unit test: availability preview with 2 conflicts includes both names
- [x] Unit test: `escapeV2("Алина.") == "Алина\\."`

### P5-04 - Session helpers

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/sessions.go`
- Refs: Doc 03 section 5

Acceptance criteria:

- [x] `LoadOrCreate(ctx, store, chatID)` returns session; missing key is not error
- [x] `SetPendingPlan(session, plan, msgID, idempKey)` updates in place
- [x] `ClearPendingPlan(session)` sets `PendingPlan=nil`
- [x] `IsPlanExpired(plan, now, ttl)` returns bool
- [x] `AppendHistory(session, role, content)` trims history to 10
- [x] `ReplacePendingPlan(session, newPlan, msgID, idempKey)` returns replacement signal and replaces plan
- [x] Unit test: `14m` old plan not expired, `16m` old plan expired
- [x] Unit test: append 11 history entries -> only latest 10 remain

---

## Phase 6 - Handler: Full Update Routing

Main orchestrator. Depends on all previous phases.

### P6-01 - Webhook setup and update routing

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/handler.go`
- Refs: Doc 03 FR-TG-02/06/08

Acceptance criteria:

- [x] Webhook registered at startup with `TG_WEBHOOK_SECRET` validation
- [x] Every request validates `X-Telegram-Bot-Api-Secret-Token`
- [x] Invalid secret -> HTTP `403`, no processing
- [x] Update routing: `Message -> handleMessage()`, `CallbackQuery -> handleCallback()`
- [x] `sendChatAction(typing)` sent immediately for each incoming message
- [x] Concurrent messages per `chat_id` handled sequentially (chat locks)
- [x] Unit test: wrong secret -> `403`
- [x] Unit test: valid update -> handler invoked exactly once

### P6-02 - Message handler: interpret and preview

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/handler.go`
- Refs: Doc 04 section 5.1/5.2, UC-01..UC-07

Acceptance criteria:

- [x] Loads session and checks stale pending plan
- [x] New message during pending plan -> `Предыдущий запрос отменён` and replace plan
- [x] Calls `Streamer.Draft()` immediately after `sendChatAction`
- [x] Calls `interpreter.Interpret()` with session context
- [x] If `NeedsClarification`: updates draft with question, saves session, returns
- [x] Increments `clarification_count`; if `>=2`, surfaces deep link (CC-06)
- [x] If no clarification: calls adapter `Preview*()` by intent
- [x] Preview errors (`ErrNotFound`, etc.) mapped to user-facing text
- [x] Preview success -> `Streamer.Finalize()` with preview + keyboard
- [x] Saves `PendingPlan` with idempotency key, `msg_id`, `created_at`
- [x] Appends exchange to dialog history
- [x] Integration test: `set_working_hours` message -> preview with confirm keyboard
- [x] Integration test: unknown intent -> onboarding hint, no keyboard

### P6-03 - Callback handler: confirm flow

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/handler.go`
- Refs: Doc 04 section 5.2 steps 10-17, CC-01

Acceptance criteria:

- [x] Answers every callback immediately (`answerCallbackQuery`) within 10s
- [x] Parses callback forms: `confirm:{planID}`, `cancel:{planID}`, `slot:{idx}:{planID}`
- [x] Loads session and finds `PendingPlan` by `planID`
- [x] Plan mismatch -> `Запрос устарел, повтори заново`, no action
- [x] Expired plan (`> config.PlanTTL`) -> `Запрос устарел, повтори заново`
- [x] On confirm: remove preview keyboard (`editMessageReplyMarkup`)
- [x] On confirm: send `Выполняю...` status update
- [x] On confirm: build ACP run and submit via runner
- [x] On ACP completed: edit to success confirmation
- [x] On transient ACP failure: show error + retry button
- [x] On policy ACP failure: show policy reason, no retry
- [x] On cancel: remove keyboard, send `Понял, ничего не изменено`, clear plan
- [x] On slot select: update pending plan with selected slot and re-preview `create_booking`
- [x] Integration test: confirm callback -> ACP run submitted -> success shown
- [x] Integration test: cancel callback -> no ACP call, plan cleared

### P6-04 - Read-only intent handlers

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/handler.go`
- Refs: UC-04, UC-07

Acceptance criteria:

- [x] `list_bookings`: call `GetBookings`, format result, finalize message (no keyboard)
- [x] `list_bookings` range `> 7 days` -> deep link instead (CC-06)
- [x] `find_next_slot`: call `FindSlots`, format with slot-selection keyboard
- [x] Slot button tap transitions to `create_booking` with pre-filled params (UC-07 AC-3)
- [x] No ACP run for either read-only intent
- [x] Unit test: `list_bookings` -> `GetBookings` called, ACP not called
- [x] Unit test: `find_next_slot` -> `FindSlots` called, slot keyboard present

### P6-05 - Deep link builder

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `internal/bot/handler.go`
- Refs: Doc 02 CC-06, Doc 03 FR-TG-07

Acceptance criteria:

- [x] `BuildDeepLink(action, context)` builds URL from `config.MiniAppURL` with encoded `action` and `context` query params
- [x] `InlineKeyboardButton` uses `web_app` type with built URL
- [x] Triggered by: `clarification_count >= 2`, list range `> 7 days`, affected slots `> 20`
- [x] Unit test: generated URL includes action/context params

Notes:

- Deep-link runtime is canonicalized to FR-TG-07 (`web_app` button + `MINI_APP_URL` query context). Legacy `tg://resolve` remains documentation history only.

---

## Phase 7 - Testing & Hardening

Integration tests, error paths, chaos scenarios.

### P7-01 - Integration test: read flow (`list_bookings`)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 02 UC-04, Doc 04 section 5.1

Acceptance criteria:

- [x] Mock LLM returns `list_bookings` `ActionPlan` for tomorrow
- [x] Mock Bookably returns 3 bookings
- [x] Mock Telegram receives exactly 1 draft + 1 final message
- [x] Final message contains all 3 booking names/times in local timezone
- [x] No ACP call
- [x] Session saved with updated dialog history

### P7-02 - Integration test: write flow happy path (`cancel_booking`)

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 02 UC-06, Doc 04 section 5.2

Acceptance criteria:

- [x] Mock LLM returns `cancel_booking` plan (`Иван в четверг`)
- [x] Mock Bookably returns 1 matching booking
- [x] Preview sent with confirm/cancel keyboard
- [x] Confirm callback received
- [x] Mock ACP receives correct payload (**POST** step with booking ID, Doc 05 precedence)
- [x] Mock ACP returns completed
- [x] Success message sent and keyboard removed
- [x] `Session.PendingPlan` cleared
- [x] ACP metadata includes `intent=cancel_booking`, `risk_level=high`

### P7-03 - Integration test: session recovery after restart

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 02 CC-01

Acceptance criteria:

- [x] Pending `cancel_booking` plan stored in Redis
- [x] Handler restart simulated (new instance, same Redis)
- [x] Confirm callback arrives
- [x] Handler restores session and finds pending plan
- [x] ACP run submitted with same idempotency key
- [x] Success message sent

### P7-04 - Integration test: clarification loop and deep link

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 02 CC-06

Acceptance criteria:

- [x] Mock LLM returns one clarification on first message
- [x] Clarification question sent; `clarification_count=1`
- [x] Second user reply still yields clarification
- [x] `clarification_count=2` -> deep link instead of third clarification
- [x] No preview and no ACP run at any point

### P7-05 - Integration test: ACP transient failure and retry

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 03 FR-EX-05

Acceptance criteria:

- [x] Confirm triggers ACP run submission
- [x] Mock ACP returns transient failure code
- [x] Error message includes retry button
- [x] Retry callback tapped
- [x] Same idempotency key reused
- [x] Second ACP call returns completed
- [x] Success message shown

### P7-06 - Error path tests

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 04 section 10.1

Acceptance criteria:

- [x] LLM timeout -> user sees retry-context error
- [x] Bookably `404` during preview -> `Запись не найдена`
- [x] Bookably `409` during preview -> conflict shown in preview
- [x] Bookably `500` during ACP execution -> `ErrUpstream` + retry button
- [x] ACP policy violation -> policy reason, no retry
- [x] Expired plan confirm -> `Запрос устарел`, no ACP call
- [x] Double-confirm -> second confirm rejected (plan already cleared)

### P7-07 - Security tests

- Status: `DONE`
- Status transitions: `TODO → IN PROGRESS → DONE` (2026-03-22)
- Component: `tests/`
- Refs: Doc 03 NFR-07/08/09

Acceptance criteria:

- [x] Forged webhook update (wrong secret) -> `403`, no processing
- [x] Access token never appears in logs
- [x] Refresh token never appears in logs
- [x] Prompt injection (`ignore previous instructions`) -> classified `unknown`
- [x] Prompt injection with JSON blob -> parsed defensively, `unknown`
- [x] Redis TLS enforced for `rediss://` URLs

---

## Phase 8 - Demo Preparation

Seed data, deploy, and rehearse demo script.

### P8-01 - Seed data setup

- Status: `TODO`
- Component: `ops/`
- Refs: Doc 01 section 5.2

Acceptance criteria:

- [ ] Demo specialist account created in Bookably
- [ ] At least 3 active services created: `Массаж 60 мин`, `Маникюр 90 мин`, `Стрижка 30 мин`
- [ ] Existing slots created for next week (`Mon-Thu`, `10:00-19:00`)
- [ ] At least 2 existing bookings created: `Алина Смирнова (Wed 14:00)`, `Иван Петров (Thu 11:00)`
- [ ] Agent bot linked to demo specialist Telegram account
- [ ] Bot responds to `/start` with welcome message

### P8-02 - Deploy and smoke test

- Status: `TODO`
- Component: `ops/`
- Refs: Doc 03 NFR-04/16

Acceptance criteria:

- [ ] Docker image builds: `docker build -t bookably-agent .`
- [ ] Image deployed to target environment
- [ ] Webhook registered/verified (`getWebhookInfo` has correct URL)
- [ ] Health check endpoint returns `200`
- [ ] Bot replies to `Привет` with onboarding hint within 3 seconds
- [ ] Logs stream to stdout as JSON
- [ ] Redis connectivity verified from container
- [ ] ACP connectivity verified (`POST /runs` returns `201`)

### P8-03 - Demo 1 rehearsal: schedule management

- Status: `TODO`
- Component: `-`
- Refs: Doc 01 section 12, Doc 02 UC-01

Acceptance criteria:

- [ ] Script: `На следующей неделе работаю с 12 до 20, кроме пятницы. Добавь обед 15–16.`
- [ ] Agent shows correct preview within 4 seconds
- [ ] Preview shows `Mon-Thu`, working hours `12-20`, break `15-16`
- [ ] Preview shows conflict with `Алина` (`Wed 14:00` in break window)
- [ ] Confirm -> ACP run executes -> success message within 8 seconds
- [ ] Slot updates visible in Bookably (Mini App or API verification)
- [ ] Full demo duration under 60 seconds end-to-end

### P8-04 - Demo 2 rehearsal: booking creation

- Status: `TODO`
- Component: `-`
- Refs: Doc 01 section 12, Doc 02 UC-05

Acceptance criteria:

- [ ] Script: `Запиши Алину на ближайшее окно после 18:00 на массаж 60 минут`
- [ ] Agent finds next available slot after `18:00`
- [ ] Preview shows slot time in local timezone and correct service
- [ ] Confirm -> booking created in Bookably
- [ ] Success message shows client, service, and exact time
- [ ] Booking visible in Mini App calendar
- [ ] Full demo duration under 60 seconds end-to-end

---

## Backlog summary

| Phase | Tasks | TODO | IN PROG | DONE | Description |
| --- | --- | --- | --- | --- | --- |
| 0 | 3 | 0 | 0 | 3 | Foundation, config, domain types |
| 1 | 2 | 0 | 0 | 2 | Session store, LLM client |
| 2 | 5 | 0 | 1 | 4 | Interpreter, parser, classifier test suite |
| 3 | 10 | 0 | 0 | 10 | Bookably adapter |
| 4 | 3 | 0 | 0 | 3 | ACP client, runner, run builder |
| 5 | 4 | 0 | 0 | 4 | Streaming, keyboards, formatters, session helpers |
| 6 | 5 | 0 | 0 | 5 | Handler: full update routing |
| 7 | 7 | 0 | 0 | 7 | Integration tests, error paths, security |
| 8 | 4 | 4 | 0 | 0 | Demo prep, deploy, rehearsal |
| TOTAL | 43 | 4 | 1 | 38 | Runtime backend gap tracked separately (specialist create-booking execution contract) |

## Iteration 10 note (No-key baseline, 2026-03-22)

- Local no-key finalization completed: unit tests, integration(mock) tests, `go vet`, and `go build` are part of this baseline gate.
- `P2-05` remains `IN PROGRESS` until live LLM execution is run with real provider credentials.
- `P8-01..P8-04` remain `TODO` because they require real environment credentials, deployment target, and end-to-end API availability.
- Workflow/plan documents are intentionally local-only per current policy (`CODEX_WORKFLOW` and `ENGINEERING_PLAN` paths are gitignored).

## Iteration 11 note (Mini App auth + runtime readiness, 2026-03-23)

- README cleanup completed: documentation section removed (including stale `docs/CODEX_WORKFLOW.md` reference).
- Auth entrypoint is now canonical via Mini App `web_app_data`:
  - bot does not call `/auth/tma` directly;
  - bot accepts payload `{ token, refreshToken, specialistId }`;
  - token is saved to Redis token store, provider/session are hydrated.
- Single-provider guard enabled via `BOOKABLY_SPECIALIST_ID`:
  - `specialistId` mismatch is rejected;
  - unauthenticated messages receive login `web_app` button (`?mode=bot_auth`).
- Timezone source fixed:
  - removed `/me.timezone` assumption;
  - timezone is resolved from `/api/v1/public/specialist/profile?specialistId=...`;
  - empty timezone now returns explicit validation error (no silent UTC fallback).
- Runtime entrypoint added: `cmd/agent/main.go` with full wiring (`config`, Redis/session/token store, LLM/interpreter, Bookably adapter, ACP runner/executor, Telegram gateway, `/webhook` and `/health`).
- Slot-selection race fixed:
  - callback uses `PendingPlan` slot snapshot;
  - no re-fetch from `FindSlots` on slot button tap.
- Strict-real write status:
  - availability intents (`set_working_hours`, `add_break`, `close_range`) execute through ACP using `/specialist/schedule/commit` payload from pending preview snapshot;
  - `create_booking` confirm remains contract-blocked with deep-link fallback.

## Iteration 12 note (No-key readiness + Mini App bridge + VPS wiring prep, 2026-03-23)

- No-key runtime baseline expanded:
  - `LLM_PROVIDER=stub` added as a first-class runtime mode;
  - `LLM_API_KEY` is required only for real providers (`anthropic`, `openai`, `amvera`);
  - `cmd/agent` LLM factory now supports deterministic no-network stub client.
- Deploy artifacts added for VPS path-routing mode:
  - `deploy/docker-compose.agent.yml`;
  - `deploy/.env.agent.example`;
  - `deploy/nginx/app.bookably.ru.agent-snippet.conf` (`/bot/webhook`, `/bot/health`).
- Mini App bridge (`booking-backend/apps/web`) implemented for `mode=bot_auth`:
  - after `bootstrapSession()`, app auto-sends `sendData({ token, refreshToken, specialistId })` to bot in TMA context;
  - one-shot guard via `sessionStorage`;
  - non-TMA `mode=bot_auth` flow is blocked with explicit user hint to open from Telegram.
- Runtime limitation remains explicit:
  - availability write intents are strict-real;
  - `create_booking` execution remains contract-blocked until specialist-initiated create-booking backend contract is published.
- Status integrity preserved:
  - `P2-05` remains `IN PROGRESS` until live LLM run with real key;
  - `P8-01..P8-04` remain `TODO` until environment credentials/deploy smoke are completed.

## Iteration 13 note (Amvera live integration, 2026-03-23)

- Added real `amvera` provider integration in `bookably-agent` (`internal/llm/amvera.go`) with wire contract:
  - `POST https://kong-proxy.yc.amvera.ru/api/v1/models/gpt`
  - header `X-Auth-Token: Bearer <LLM_API_KEY>`
  - response mapping from `choices[0].message.text`.
- Strict model policy is now runtime-enforced:
  - `amvera` accepts only `LLM_MODEL=gpt-5`;
  - config defaults `LLM_MODEL` to `gpt-5` for `amvera` and rejects other models.
- Live classifier attempt executed with `amvera/gpt-5`, but acceptance gate is still blocked by provider billing (`402` / run out of tokens), so:
  - `P2-05` stays `IN PROGRESS`;
  - no fallback provider/model was introduced.

## Iteration 14 note (Auth loop fix + one-time login UX, 2026-03-24)

- `bookably-agent` auth/session flow hardened without LLM spend:
  - unauthenticated message path now persists session state and enforces one-time login prompt cooldown (`LastAuthPromptAt`, 10 minutes);
  - if `session.ProviderID` is empty, handler attempts token-based auto-restore for `BOOKABLY_SPECIALIST_ID` before showing login button;
  - `SpecialistTokenStore` bot seam now supports token presence checks via `HasToken(...)`.
- Auth observability expanded (redacted structured events):
  - `auth.prompt_sent`, `auth.auto_restored`, `auth.web_app_data_received`, `auth.web_app_data_invalid`, `auth.specialist_mismatch`, `auth.blocked_no_payload`.
- `booking-backend/apps/web` `mode=bot_auth` UX no longer silent:
  - explicit status screen for `sent` (`Вход выполнен, можно вернуться в чат`);
  - explicit status screen for `blocked_not_tma` (open from Telegram);
  - explicit status screen for `blocked_no_payload` (active specialist profile required), with return CTA.
- Validation performed with no external LLM calls:
  - `go test ./internal/bot/... -v`
  - `go test ./internal/session/... -v`
  - `go test ./tests/... -tags=integration -v`
  - `go test ./...`, `go vet ./...`, `go build ./...`
  - `npm --prefix apps/web test -- --run tests/bot-auth-bridge.test.ts`
  - `npm --prefix apps/web run build`

## Iteration 15 note (Service-key bot auth cutover, 2026-03-24)

- Backend auth surface switched to dual mode on existing `/api/v1/*` routes:
  - JWT bearer remains supported;
  - bot server-to-server headers are now supported as alternative:
    - `X-Bot-Service-Key`
    - `X-Telegram-User-Id`.
- Agent runtime auth hard-cut to service-key mode:
  - removed runtime login prompt flow and `web_app_data` handshake from bot handler;
  - removed JWT token-store/refresh runtime dependencies from Bookably client and ACP executor;
  - every Bookably/ACP write/read call now carries bot auth headers derived from session `telegram_user_id`.
- Session contract updated:
  - `telegram_user_id` persisted as first-class field in `ba:session:{chat_id}`;
  - provider hydration is now driven by Telegram `from.id` actor context.
- Mini App cleanup completed:
  - removed `mode=bot_auth` bridge runtime and related test/module wiring.
- Runtime policy unchanged:
  - `create_booking` execution remains contract-blocked (deep-link fallback);
  - availability writes remain strict-real via commit payload.

## Iteration 18 note (Cancel/write recovery + human UX, 2026-03-24)

- `cancel_booking` preview fixed to safe search windows (`<=31 days`) and no longer queries unsupported wide ranges.
- Cancel resolution moved to date-first behavior:
  - supports date/day window and approximate time filtering;
  - returns `BookingCandidates` (up to 3) instead of immediate conflict error when multiple matches exist.
- Bot callback contract extended:
  - new callback type `booking:{idx}:{planID}`;
  - pending plan now stores booking candidate snapshot for deterministic selection before confirm.
- Handler flow updated for multi-candidate cancel:
  - first step shows candidate list with inline buttons;
  - after selection, pending plan is pinned with `booking_id` and final `confirm/cancel` keyboard is shown.
- Formatter baseline switched to natural, intent-specific responses:
  - removed rigid universal section frame;
  - human date formatting (`12 апреля, 14:30`) and focused bold key entities retained.
- Regression status for this iteration:
  - `go test ./internal/bookably/... -v` passed;
  - `go test ./internal/bot/... -v` passed;
  - `go test ./...`, `go vet ./...`, `go build ./...` passed.

Update `status` fields and checkboxes as tasks are completed.  
Blocked count is now zero in backlog status; backend create-booking contract gap is tracked as a runtime limitation note.
