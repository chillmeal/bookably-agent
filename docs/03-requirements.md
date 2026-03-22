# BOOKABLY AGENT

## Functional & Non-Functional Requirements

Version 1.0 | March 2026 | Doc 03 of series  
Depends on: [01 - Vision & MVP Scope](./01-vision-mvp-scope.md), [02 - Use Cases](./02-use-cases.md)

## Priority Notation

| Priority | Meaning |
| --- | --- |
| MUST | Mandatory for MVP; no exceptions |
| SHOULD | Strong recommendation; deviations require explicit rationale |
| MAY | Optional; future-friendly capability |

## 1. Intent processing requirements

Requirements governing how the agent parses, classifies, and validates incoming natural-language messages.

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-IP-01 | MUST | The interpreter MUST classify every incoming text message into one of 8 intent values: `set_working_hours`, `add_break`, `close_range`, `list_bookings`, `create_booking`, `cancel_booking`, `find_next_slot`, `unknown`. | Closed intent set prevents unbounded agent behavior |
| FR-IP-02 | MUST | The interpreter MUST return a confidence score in `[0.0, 1.0]` for every classification. Messages with confidence `< 0.50` MUST be classified as `unknown` regardless of the top candidate intent. | Prevents misfire on ambiguous input |
| FR-IP-03 | MUST | Unknown intent MUST produce a short onboarding hint listing 3 example commands. It MUST NOT attempt to answer general questions or perform any action. | Agent scope is strictly booking operations |
| FR-IP-04 | MUST | The interpreter MUST extract all structured parameters defined for the classified intent (see section 2). If a required parameter is absent, a `Clarification` record MUST be produced instead of a partial `ActionPlan`. | Prevents execution with incomplete data |
| FR-IP-05 | MUST | The interpreter MUST resolve relative date/time expressions (`tomorrow`, `next week`, `on Monday`, `in the evening`) using the provider's configured IANA timezone. UTC is never assumed. | Providers operate in local time |
| FR-IP-06 | MUST | The interpreter MUST produce at most one `Clarification` per response cycle. It MUST identify the single most blocking ambiguity and ask only that question. | Multiple questions in one response degrade UX |
| FR-IP-07 | MUST | After two consecutive `Clarification` cycles for the same intent, the agent MUST surface a deep link to the Mini App and abandon the intent. | Prevents infinite clarification loops |
| FR-IP-08 | SHOULD | The interpreter SHOULD normalize colloquial time references consistently: `вечером = 18:00`, `утром = 09:00`, `обед` / `после обеда = 13:00`, `ночью = 22:00`. These defaults MUST be documented and overridable via provider preferences. | Reduces clarification round-trips |
| FR-IP-09 | SHOULD | The interpreter SHOULD detect locale automatically from the message language (`ru` / `en`) and select the corresponding system prompt variant. | Providers may write in Russian or English |
| FR-IP-10 | MAY | The interpreter MAY accept voice messages by transcribing them before intent classification. Transcription is out of scope for MVP but the interface must not preclude it. | Future capability; interface must be clean |

## 2. Parameter contracts per intent

For each intent, this section specifies required/optional parameters and validation rules.  
These contracts are the source of truth for `ActionParams` in the type system.

### 2.1 `set_working_hours`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-01 | MUST | `date_range.from` and `date_range.to` are both required. If either is absent, `Clarification` MUST be produced. | Cannot set hours without knowing when |
| FR-P-02 | MUST | `working_hours.from` and `working_hours.to` are required. Both must be valid `HH:MM` strings. `to > from` is enforced; violation produces an error message, not a clarification. | Inverted range is a provider error, not ambiguity |
| FR-P-03 | SHOULD | `weekdays` array defaults to all working days in the range if not specified. Provider may specify a subset (for example `["mon","wed","fri"]`). | Natural language rarely lists weekdays explicitly |
| FR-P-04 | MAY | `breaks` array is optional. Each break must satisfy: `break.from >= working_hours.from` and `break.to <= working_hours.to`. Violations produce an error. | Break outside working hours is a logical error |

### 2.2 `add_break`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-05 | MUST | Break time slot (`from`, `to`) is required. `date_range` is required. Both missing -> `Clarification`. | Cannot add break without knowing when |
| FR-P-06 | MUST | Break must fall within existing working hours for the date range. If working hours are not set for any day in the range, those days MUST be excluded from the preview with a warning. | Breaks outside working hours are meaningless |
| FR-P-07 | SHOULD | `weekdays` defaults to all days in range if not specified. | Matches natural language "add lunch every day" |

### 2.3 `close_range`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-08 | MUST | At least one of: specific date, `date_range`, or weekday is required. Completely open-ended request (`закрой что-нибудь`) -> `unknown` intent. | Cannot close without a target |
| FR-P-09 | MUST | `time_range` is optional. If absent, the entire working day is closed. If present, only that time window is closed. | `Закрой вечер` vs `Закрой среду` |
| FR-P-10 | MUST | Vague time references (`вечер`, `утро`, `день`) for `close_range` MUST produce a `Clarification` asking for exact time, not assume defaults. | `close_range` is higher-risk than read; assume nothing |

### 2.4 `list_bookings`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-11 | MUST | `date` or `date_range` is required. If absent, default to `today`. | No date -> show today; sensible default |
| FR-P-12 | MUST | `date_range` spanning more than 7 days MUST trigger deep-link escalation instead of a text list. | Long lists in chat are unusable |
| FR-P-13 | SHOULD | Status filter defaults to `upcoming`. Provider may request `all` or `past` explicitly. | Default to actionable information |

### 2.5 `create_booking`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-14 | MUST | `client_name` or client reference is required. `Clarification` if absent. | Cannot book without knowing who |
| FR-P-15 | MUST | `service_id` or `service_name` is required. If `service_name` matches multiple services, `Clarification` with a choice list (max 3 items) is produced. | Prevents wrong service selection |
| FR-P-16 | MUST | `preferred_at` or `not_before` is required. `As soon as possible` is valid and maps to `not_before = now()`. | Cannot book without time context |
| FR-P-17 | MUST | `duration_min` must match the selected service's configured duration. Agent MUST NOT override service duration based on free text. | Service duration is a business rule, not user input |
| FR-P-18 | MUST | Agent MUST search for available slots using the Bookably API and present at most 2 options. It MUST NOT invent slots. | Slot availability is Bookably's source of truth |
| FR-P-19 | SHOULD | If requested time has no slot, agent SHOULD offer the nearest available slot after requested time, not fail silently. | Reduces friction for provider |

### 2.6 `cancel_booking`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-20 | MUST | Booking reference (`client name + approximate time`, or booking ID) is required. `Clarification` if absent. | Cannot cancel without identifying the booking |
| FR-P-21 | MUST | If multiple bookings match the reference, agent MUST present a disambiguation list (max 3 items) and require provider to select one. It MUST NOT cancel all matches automatically. | Ambiguous cancel is too destructive to auto-resolve |
| FR-P-22 | MUST | Preview for `cancel_booking` MUST include an explicit irreversibility warning. | Highest-risk write operation |

### 2.7 `find_next_slot`

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-P-23 | MUST | `service_id` or `service_name` is required. `Clarification` if absent. | Slot duration depends on service |
| FR-P-24 | MUST | Agent MUST return at most 2 slots. Each slot MUST include date, start time, and duration. | More than 2 choices creates decision paralysis |
| FR-P-25 | SHOULD | `not_before` defaults to `now()` if absent. `after_time` constrains the start of the search window within a day. | Natural default for "find me a slot" |
| FR-P-26 | SHOULD | Result MUST include an inline button that transitions directly to `create_booking` with pre-filled parameters. | Reduces friction from find to book |

## 3. Preview requirements

Preview is the primary trust mechanism of the agent. These requirements govern its content, accuracy, and scope.

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-PV-01 | MUST | Every write intent (`set_working_hours`, `add_break`, `close_range`, `create_booking`, `cancel_booking`) MUST produce a preview before any data is mutated. There is no exception. | Core safety guarantee |
| FR-PV-02 | MUST | Preview MUST be built using read-only API calls to Bookably. No Bookably write endpoint may be called during preview construction. | Preview must be side-effect free |
| FR-PV-03 | MUST | Preview for availability-mutating intents (`set_working_hours`, `add_break`, `close_range`) MUST include: (a) human-readable summary of what was understood, (b) count of slots added, (c) count of slots removed, (d) list of conflicting bookings with client name and time. | Provider must see full impact before confirming |
| FR-PV-04 | MUST | Preview for `create_booking` MUST include client name, service name, proposed time, and duration. If multiple slot options exist, all offered options (max 2) MUST appear as separate inline buttons. | Choice must be visible in preview, not after confirm |
| FR-PV-05 | MUST | Preview for `cancel_booking` MUST include client name, service name, exact date/time, and an irreversibility warning. | Cancellation is destructive; provider must see exactly what is being canceled |
| FR-PV-06 | MUST | Every preview MUST include exactly two inline buttons: a confirmation action and a cancel action. No other buttons on the preview message. | Single confirm gate; no ambiguity about action |
| FR-PV-07 | MUST | Preview confirmation must be delivered via `callbackQuery` on the exact `message_id` of the preview. A confirm on a stale `message_id` (older than 15 minutes) MUST be rejected with an expiry notice. | Prevents executing a plan that is no longer current |
| FR-PV-08 | MUST | `RiskLevel` MUST be computed and included in the audit trail. Rules: High if `cancel_booking` OR if availability change affects a booking; Medium if more than 10 slots affected; Low otherwise. | Risk metadata is required for ACP audit |
| FR-PV-09 | SHOULD | If preview detects more than 20 slots affected, agent SHOULD append a recommendation to review in the Mini App calendar before confirming. | Large-scale changes benefit from visual review |

## 4. Execution & ACP integration requirements

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-EX-01 | MUST | All write operations MUST be executed through ACP runs. Direct Bookably API write calls from the agent are not permitted after confirmation. | ACP is the execution control plane; bypassing it removes auditability |
| FR-EX-02 | MUST | Every ACP run MUST include an `idempotency_key` computed as `SHA-256(chat_id + plan_id + intent)`. This key MUST be stored in session before run submission. | Prevents duplicate execution on retry |
| FR-EX-03 | MUST | ACP run MUST carry metadata: `{ chat_id, provider_id, intent, risk_level, raw_user_message }`. This metadata is written to ACP audit trail. | Audit trail must be attributable to provider and message |
| FR-EX-04 | MUST | Agent MUST poll ACP run result using `GET /runs/{id}` with 2-second interval and 30-second total timeout. On timeout, agent MUST surface an error with retry button. | Bounded wait prevents hanging sessions |
| FR-EX-05 | MUST | On ACP run failure, agent MUST distinguish: (a) policy violation -> show policy reason, no retry; (b) transient error (`5xx`) -> offer retry with same `idempotency_key`; (c) domain error from Bookably -> show error message + deep link. | Each failure mode requires different UX response |
| FR-EX-06 | MUST | ACP run status transitions visible to agent: `received -> interpreted -> awaiting_confirmation -> executing -> completed | failed`. Agent must not assume completion until ACP explicitly returns `completed`. | ACP lifecycle is authoritative |
| FR-EX-07 | SHOULD | For read-only intents (`list_bookings`, `find_next_slot`), agent SHOULD call Bookably API directly without creating ACP run. ACP runs are only for write operations. | Unnecessary ACP runs add latency and audit noise |
| FR-EX-08 | SHOULD | ACP run payload MUST be structured as a single HTTP step targeting the relevant Bookably API endpoint. Step config MUST include method, URL, headers (auth token), and body. | ACP executes Bookably writes via configured HTTP steps |

## 5. Session & state requirements

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-SS-01 | MUST | Session state MUST be persisted in Redis keyed by `session:{chat_id}`. Required fields: `pending_plan` (serialized `ActionPlan`), `preview_message_id` (`int64`), `plan_created_at` (Unix timestamp), `dialog_history` (last 10 messages), `provider_id`, `timezone`. | Redis ensures session survives bot restarts |
| FR-SS-02 | MUST | Sessions MUST have TTL of 24 hours. Each new user message resets TTL. | Prevents unbounded Redis growth |
| FR-SS-03 | MUST | A `pending_plan` older than 15 minutes MUST be treated as expired. Confirm callback on expired plan MUST be rejected with: `Запрос устарел, повтори его заново.` | Stale plans may reference changed data |
| FR-SS-04 | MUST | Only one `pending_plan` may exist per session at a time. If new message arrives while plan is pending confirmation, new message MUST replace pending plan after warning provider: `Предыдущий запрос отменён.` | Concurrent plans create ambiguous confirm state |
| FR-SS-05 | MUST | Message processing per `chat_id` MUST be sequential. Concurrent messages for same `chat_id` MUST be queued, not processed in parallel. | Parallel processing would corrupt session state |
| FR-SS-06 | SHOULD | Dialog history (last 10 messages) MUST be included in every LLM interpreter call for pronoun/reference context (`отмени её запись`). | Context-aware interpretation reduces clarification round-trips |
| FR-SS-07 | SHOULD | Provider preferences (timezone, colloquial time defaults) MUST be stored in session on first load and refreshed every 24 hours from Bookably API. | Timezone is required for date resolution |

## 6. Telegram interface requirements

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-TG-01 | MUST | Agent responses MUST use `sendMessageDraft` (Bot API 9.3+) to stream partial content while interpreter runs. Final message with inline keyboard MUST be sent only when complete response is ready. | Native streaming UX without `editMessageText` polling |
| FR-TG-02 | MUST | `sendChatAction` with `action=typing` MUST be sent immediately on receiving user message, before processing starts. | Immediate feedback that bot is alive |
| FR-TG-03 | MUST | Preview messages MUST use Telegram MarkdownV2 formatting. Conflict warnings use bold. Slot counts use bold. Irreversibility warnings use bold + warning emoji. | Visual hierarchy improves preview readability |
| FR-TG-04 | MUST | Inline keyboard buttons MUST use `callback_data` with structured prefix: `confirm:{plan_id}`, `cancel:{plan_id}`, `slot:{slot_index}:{plan_id}`. `plan_id` MUST be a short UUID stored in session. | Prefixed callback data enables reliable routing |
| FR-TG-05 | MUST | After confirm or cancel callback, inline keyboard MUST be removed from preview (`editMessageReplyMarkup` with empty keyboard). This prevents double confirmation. | Removing buttons prevents accidental re-submit |
| FR-TG-06 | MUST | Bot MUST answer every `callbackQuery` within 10 seconds (Telegram timeout). For long-running operations, answer callback immediately with loading indicator, then edit message when done. | Unanswered callbacks show spinner indefinitely |
| FR-TG-07 | SHOULD | Deep links to Mini App MUST be presented as `InlineKeyboardButton` with `type=web_app` pointing to Mini App URL with context parameter. Plain text URLs MUST NOT be used. | `web_app` buttons open Mini App inline, not in browser |
| FR-TG-08 | SHOULD | Error messages MUST be concise (max 2 sentences) and always include actionable next step (retry button or Mini App link). | Dead-end errors frustrate providers |

## 7. Domain adapter (Bookably) requirements

Requirements for internal Bookably adapter: translates `ActionParams` into Bookably API calls and maps responses to domain types.

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-DA-01 | MUST | Adapter MUST implement `domain.Provider` interface. No component except adapter may know Bookably URLs, auth headers, or response shapes. | Isolation enables future multi-provider support |
| FR-DA-02 | MUST | All Bookably API calls from adapter MUST use per-provider auth token. Token is loaded from session at request time; never hardcoded or cached globally. | Each provider has own auth context |
| FR-DA-03 | MUST | Adapter MUST map Bookably HTTP status to typed domain errors: `404 -> ErrNotFound`, `409 -> ErrConflict`, `422 -> ErrValidation`, `5xx -> ErrUpstream`. Types are used for FR-EX-05 routing. | Typed errors enable correct failure handling |
| FR-DA-04 | MUST | Preview calls (read operations) MUST use HTTP `GET` with no side effects. Adapter MUST NOT call any Bookably write endpoint during preview construction. | Enforces FR-PV-02 at adapter layer |
| FR-DA-05 | MUST | Adapter MUST implement `BuildPreview(ctx, ActionPlan) *Preview` for each write intent. Method calls Bookably read endpoints and computes impact without writing. | Preview builder is adapter core responsibility |
| FR-DA-06 | SHOULD | Adapter MUST set 5-second HTTP timeout on all Bookably API calls. Timeouts return `ErrUpstream`. | Prevents hangs on slow Bookably responses |
| FR-DA-07 | MAY | Adapter MAY cache provider preferences (timezone, services list) in-memory with 5-minute TTL to reduce API calls during session. | Services list is read on every `create_booking` flow |

## 8. LLM abstraction requirements

Agent must not be tightly coupled to any single LLM provider. These requirements define abstraction boundary.

| ID | Priority | Requirement | Rationale |
| --- | --- | --- | --- |
| FR-LM-01 | MUST | Interpreter MUST depend on `LLMClient` interface with a single method: `Complete(ctx, messages []Message) (*Completion, error)`. All LLM calls go through this interface. | Enables provider swap without interpreter changes |
| FR-LM-02 | MUST | System prompt MUST be loaded from file (`prompts/intent_classifier.md`), not hardcoded in Go source. Prompt file MUST be versioned in repository. | Prompt iteration must not require recompilation |
| FR-LM-03 | MUST | LLM response MUST be parsed as structured JSON matching `ActionPlan`. If parsing fails, response MUST be treated as `unknown` intent, not propagated as error. | LLM output is unreliable; parse defensively |
| FR-LM-04 | MUST | LLM call MUST include last 10 messages of dialog history as context (see FR-SS-06). History is formatted as alternating `user/assistant` messages. | Context window enables pronoun resolution |
| FR-LM-05 | SHOULD | System prompt MUST explicitly instruct LLM to return only valid JSON matching `ActionPlan` schema, with no prose wrapping. Schema MUST be included in prompt. | Reduces parse failures on structured output |
| FR-LM-06 | SHOULD | LLM call timeout MUST be 15 seconds. On timeout, agent returns user-facing error (CC-04 from UC doc) and does not produce partial plan. | Partial plans are more dangerous than no plan |

## 9. Non-functional requirements

| ID | Category | Metric | Target | Measurement |
| --- | --- | --- | --- | --- |
| NFR-01 | Performance | Time to first `sendMessageDraft` after user message | `< 800 ms` (p95) | Load test: 20 concurrent sessions |
| NFR-02 | Performance | Total time from message to complete preview visible | `< 4 s` (p95) for LLM + preview build | End-to-end trace in staging |
| NFR-03 | Performance | ACP run submission to completed (Bookably write) | `< 8 s` (p95) | ACP run event timeline |
| NFR-04 | Reliability | Bot uptime | `99.5%` monthly | External uptime monitor |
| NFR-05 | Reliability | Session recovery on restart | `100%` of pending sessions restored from Redis | Chaos test: kill process, confirm pending plan |
| NFR-06 | Reliability | Duplicate execution on retry | `0` duplicates | Idempotency test: submit same `run_id` twice |
| NFR-07 | Security | Provider auth token storage | Never logged, never stored in plaintext outside Redis TLS | Code audit + Redis config review |
| NFR-08 | Security | Telegram webhook origin validation | All requests validated with `X-Telegram-Bot-Api-Secret-Token` header | Penetration test: forged update |
| NFR-09 | Security | LLM prompt injection | Agent MUST not execute arbitrary instructions injected via user message | Red-team test: injection attempts in NL input |
| NFR-10 | Observability | Structured logs | Every intent classification, preview build, ACP run, and error logged with `trace_id`, `chat_id`, `intent`, `duration_ms` | Log query in production |
| NFR-11 | Observability | ACP audit trail completeness | Every write execution has corresponding ACP audit event with `run_id`, `intent`, `provider_id`, `risk_level` | Query ACP audit log post-demo |
| NFR-12 | Observability | Error-rate alerting | Alert if error rate on any intent > 5% over 5-minute window | Prometheus + Alertmanager rule |
| NFR-13 | Maintainability | LLM provider swap | Changing provider requires changes only in `internal/llm/`, zero changes elsewhere | Architecture test: mock LLM behind interface |
| NFR-14 | Maintainability | Domain adapter swap | Adding new booking platform requires only new `domain.Provider` implementation | Interface compliance test |
| NFR-15 | Deployment | Configuration | All secrets and URLs via environment variables; no config in source code | CI grep for hardcoded URLs/tokens |
| NFR-16 | Deployment | Containerization | Agent ships as single Docker image with no runtime dependencies beyond Redis and external HTTP | Docker build in CI |
| NFR-17 | Compliance | Bookably API usage | Agent MUST only call Bookably API endpoints documented in adapter contract (section 7). Undocumented endpoints are forbidden. | Adapter code review |

## 10. Implementation constraints

Hard constraints that are non-negotiable for this implementation. These are fixed decisions, not prioritized requirements.

### 10.1 Language & runtime

- Implementation language: Go 1.22+
- Telegram bot library: `go-telegram-bot-api v5+` or equivalent, or raw HTTP calls to Bot API
- Redis client: `go-redis v9+`
- No ORM; Bookably API is accessed via HTTP, not a database
- No shared Go packages with the Bookably Mini App codebase

### 10.2 Repository isolation

- Agent repository MUST be standalone and buildable without dependency on Bookably Mini App repository
- Agent connects to Bookably only via HTTP API; no shared database, no shared Go modules
- `domain.Provider` is the only coupling point, implemented by Bookably adapter

### 10.3 ACP dependency

- Agent depends on ACP via HTTP API only (`POST /runs`, `GET /runs/{id}`)
- ACP Go package may be imported if it exports typed client; otherwise raw HTTP is acceptable
- ACP must be running and reachable for write operations; agent fails hard if ACP is unreachable on startup

### 10.4 Telegram API version

- Minimum Bot API version: 9.3 (`sendMessageDraft` support)
- `sendMessageDraft` is used for streaming responses; fallback to `sendMessage` is NOT acceptable
- Inline keyboard style: success (green) for confirm buttons, danger (red) for cancel/destructive buttons; requires Bot API 9.4+

### 10.5 Out-of-scope technical decisions

- Multi-region deployment: single region for MVP
- Horizontal scaling: single instance with Redis for state
- gRPC: internal interfaces are Go interfaces; external interfaces are HTTP
- Database (Postgres, etc.): Redis only for agent-owned state
- TON payment integration: explicitly excluded (Vision doc, section 1.4)

## Appendix A - Requirements traceability matrix

Maps each requirement group to covered use cases and responsible architectural component.

| Requirement group | Use cases covered | Owner component | Key risk if violated |
| --- | --- | --- | --- |
| FR-IP (Intent processing) | All UCs, CC-03 | `internal/interpreter` | Misclassification -> wrong action executed |
| FR-P (Parameters) | UC-01 to UC-07 | `internal/interpreter` | Missing parameter -> partial plan executed |
| FR-PV (Preview) | UC-01, UC-02, UC-03, UC-05, UC-06 | `internal/bot` + domain adapter | Data mutation without provider awareness |
| FR-EX (Execution/ACP) | UC-01, UC-02, UC-03, UC-05, UC-06 | `internal/acp` | Duplicate execution, no audit trail |
| FR-SS (Session/State) | CC-01, CC-02, CC-04 | `internal/bot/sessions` | Lost pending plan, double confirm |
| FR-TG (Telegram interface) | All UCs | `internal/bot` | Poor UX, broken confirm flow |
| FR-DA (Domain adapter) | All UCs | `internal/bookably` | Wrong data sent to Bookably |
| FR-LM (LLM abstraction) | All UCs | `internal/interpreter` | Vendor lock-in, brittle parsing |
| NFR-07, NFR-08, NFR-09 (Security) | All UCs | Cross-cutting | Auth bypass, token exposure |
| NFR-10, NFR-11, NFR-12 (Observability) | All UCs | Cross-cutting | Invisible failures in production |
