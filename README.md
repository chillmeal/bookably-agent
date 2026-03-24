# Bookably Agent

**Telegram-native AI agent for service providers — manage bookings and availability in plain language, with ACP-governed execution.**

Service professionals already live in Telegram. Bookably Agent brings scheduling operations directly into the chat: the provider writes a sentence, the agent understands the intent, shows a preview of what will change, and executes only after explicit confirmation — through [TON-ACP](https://github.com/chillmeal/TON-ACP), a governed execution runtime that records every action on-chain.

---

## How it works

```
Provider writes in chat
        |
        v
  Intent classifier (LLM)
  +---------------------------------------------+
  | "Next week 12–20, except Friday, add lunch" |
  |                  v                          |
  |           ActionPlan (JSON)                 |
  |  intent: set_working_hours                  |
  |  date_range, working_hours, breaks          |
  +---------------------------------------------+
        |
        v
  Preview (read-only Bookably API calls)
  +--------------------------------------+
  | +28 new slots                        |
  | -4 removed slots                     |
  | ! 1 conflict: Alina Thu 14:00        |
  +--------------------------------------+
        |
   [ ✅ Apply ]  [ ❌ Cancel ]
        |
        v (on confirm)
  ACP Run -> Bookably API write -> on-chain anchor
```

No data is mutated until the provider explicitly confirms. The agent never calls write endpoints directly — all confirmed operations go through [TON-ACP](https://github.com/chillmeal/TON-ACP), which enforces policy, tracks the full execution lifecycle, and anchors completed runs to TON.

---

## Supported operations

| Intent | Example | Confirmation | Risk |
|--------|---------|:---:|:---:|
| `set_working_hours` | "Next week 12–20, except Friday" | ✅ | Medium |
| `add_break` | "Add lunch 15–16 on weekdays" | ✅ | Low |
| `close_range` | "Close Wednesday morning" | ✅ | Medium |
| `cancel_booking` | "Cancel Ivan's booking Thursday" | ✅ | High |
| `create_booking` | "Book Alina for massage 60 min after 18:00" | ✅ | Medium |
| `list_bookings` | "Show my bookings tomorrow" | — | Read only |
| `find_next_slot` | "Find next 90-min window for manicure" | — | Read only |

All write operations go through preview -> explicit confirm -> ACP execution. The confirmation step can never be skipped.

---

## Architecture

The project is a single Go service with a strict layered structure and a DAG import graph — no layer knows about layers above it.

```
cmd/agent/
  └── main.go               Wire-up and startup

internal/
  ├── domain/               Pure types + Provider interface (imports nothing)
  ├── interpreter/          LLM -> ActionPlan (no HTTP, no Bookably, no ACP)
  ├── llm/                  LLM abstraction: OpenRouter, stub
  ├── bookably/             Bookably HTTP adapter (implements domain.Provider)
  ├── acp/                  ACP run builder, client, polling runner
  ├── session/              Redis-backed session store (per-chat state)
  ├── bot/                  Telegram bot layer (routing, streaming, keyboards)
  └── actorctx/             Context propagation for Telegram user identity

observability/
  ├── logger.go             Structured JSON logs (trace_id, chat_id, intent, duration_ms)
  └── sanitize.go           Automatic token/secret redaction from all log output

prompts/
  └── intent_classifier.md  System prompt — versioned, loaded at runtime
```

### Key design decisions

**`domain.Provider` is the only seam between the agent and any booking backend.** The interface has six methods covering reads and preview builders. The entire agent layer (session, streaming, confirmation flow, ACP integration) works unchanged when you swap the adapter.

**Authentication is Telegram-native.** No JWT, no login flow, no Mini App round-trip. The bot uses a shared service key (`X-Bot-Service-Key`) + the user's `X-Telegram-User-Id` propagated through context. Telegram guarantees `from.id` cannot be spoofed. The backend maps it to a specialist and enforces data isolation.

**Streaming via `sendMessageDraft`.** Responses use Bot API 9.3+ `sendMessageDraft` for native streaming as the LLM generates output. No polling `editMessageText` workaround.

**Per-chat sequential processing.** A `sync.Mutex` per `chat_id` prevents concurrent message processing that would corrupt session state. Update deduplication via Redis prevents Telegram retries from re-executing completed operations.

**ACP idempotency.** Every confirmed operation gets an idempotency key (`SHA-256(chat_id:plan_id:intent)`) stored before the ACP run is submitted. Retries on network failure cannot produce duplicate executions.

---

## Session model

Each chat session is stored in Redis (`ba:session:{chat_id}`, 24h TTL) and contains:

| Field | Description |
|-------|-------------|
| `provider_id` | Resolved specialist ID from Bookably |
| `timezone` | Specialist's IANA timezone (used for all date resolution) |
| `pending_plan` | ActionPlan awaiting confirmation (expires in 15 min) |
| `dialog_history` | Last 10 turns passed to LLM as context |
| `clarification_count` | Tracks clarification loops (escalates to Mini App after 2) |
| `last_processed_update_id` | Deduplication guard |

---

## Using this as a template

Bookably Agent is designed to be forked. The coupling to Bookably is contained entirely in `internal/bookably/`. Everything else is backend-agnostic.

To adapt it to your own platform:

1. Implement `domain.Provider` in a new adapter package
2. Update `internal/acp/builder.go` with your endpoint URLs per intent
3. Edit `prompts/intent_classifier.md` with your domain vocabulary and service names
4. Wire your adapter in `cmd/agent/main.go`

The session model, confirmation flow, streaming UX, ACP integration, per-chat locking, and observability layer require no changes.

---

## Built on TON-ACP

[TON-ACP](https://github.com/chillmeal/TON-ACP) is a governed execution runtime for AI agents on TON. Bookably Agent is a production consumer of ACP — every confirmed write operation goes through it.

What ACP provides in this integration:

| Capability | How it's used |
|-----------|--------------|
| **Policy gates** | Validates operation is allowed before execution starts |
| **Execution lifecycle** | `received -> executing -> completed / failed` tracked per run |
| **Idempotent retry** | Same idempotency key always produces the same result |
| **Audit trail** | Every run records `chat_id`, `intent`, `risk_level`, `raw_message` |
| **On-chain anchor** | Completed runs anchored to TON as verifiable proof |

Multi-step ACP runs are used for availability operations (`set_working_hours`, `add_break`, `close_range`): N slot operations followed by a schedule commit — all in a single run. If any step fails, the commit is never reached.

---

## Running locally

### Prerequisites

- Go 1.22+
- Redis
- Running [TON-ACP](https://github.com/chillmeal/TON-ACP) instance
- Telegram bot token from [@BotFather](https://t.me/BotFather)
- [OpenRouter](https://openrouter.ai) API key (or use `LLM_PROVIDER=stub` for testing)

### Setup

```bash
git clone https://github.com/chillmeal/bookably-agent
cd bookably-agent

# Start Redis
docker-compose up -d redis

# Configure
cp .env.example .env
# Edit .env — minimum required fields are marked below

go run ./cmd/agent
```

### Environment variables

| Variable | Required | Default | Description |
|----------|:--------:|---------|-------------|
| `TG_BOT_TOKEN` | ✓ | — | Telegram Bot API token |
| `TG_WEBHOOK_URL` | ✓ | — | Public HTTPS URL for webhook |
| `TG_WEBHOOK_SECRET` | ✓ | — | Random string for webhook validation |
| `REDIS_URL` | ✓ | — | Redis connection string |
| `ACP_BASE_URL` | ✓ | — | TON-ACP instance URL |
| `ACP_API_KEY` | ✓ | — | Shared key between agent and ACP |
| `BOOKABLY_API_URL` | ✓ | — | Bookably backend base URL |
| `BOOKABLY_BOT_SERVICE_KEY` | ✓ | — | Shared service key for agent→Bookably calls |
| `LLM_PROVIDER` | ✓ | — | `openrouter` or `stub` |
| `LLM_API_KEY` | ✓* | — | LLM provider API key (*not required for stub) |
| `LLM_MODEL` | — | `openai/gpt-5.4-mini` | Model override |
| `MINI_APP_URL` | ✓ | — | Telegram Mini App URL for deep links |
| `PORT` | — | `8080` | HTTP listen port |
| `LLM_TIMEOUT` | — | `15s` | LLM call timeout |
| `PLAN_TTL` | — | `15m` | Pending plan expiry |
| `SESSION_TTL` | — | `24h` | Redis session TTL |
| `WORKER_TIMEOUT` | — | `90s` | Per-update processing budget |
| `ACP_POLL_INTERVAL` | — | `2s` | ACP run poll interval |
| `ACP_POLL_TIMEOUT` | — | `30s` | ACP max wait |

### Webhook (Telegram requires HTTPS)

```bash
# Expose local port
ngrok http 8080

# Register with Telegram
curl -X POST https://api.telegram.org/bot{TOKEN}/setWebhook \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://your-ngrok.ngrok.io/webhook",
    "secret_token": "your-webhook-secret",
    "allowed_updates": ["message", "callback_query"]
  }'
```

---

## Testing

```bash
# Unit tests (no external dependencies)
go test ./...

# With race detector
go test -race ./internal/session/... ./internal/bot/...

# Integration tests (requires Redis + ACP + Bookably staging)
go test -tags=integration ./...

# Stub mode — test the full bot flow without LLM or Bookably
LLM_PROVIDER=stub go run ./cmd/agent
```

---

## Tech stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22 |
| Telegram | Bot API 9.3+ (`sendMessageDraft`, webhook) |
| LLM | OpenRouter (OpenAI-compatible) |
| Session state | Redis |
| Execution runtime | [TON-ACP](https://github.com/chillmeal/TON-ACP) |
| Containerisation | Docker (scratch image, ~15 MB) |

---

## License

MIT
