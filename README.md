# Bookably Agent

Telegram-native AI booking operations agent for service providers. The agent interprets natural-language requests, builds safe previews, and executes write operations through ACP.

## Project Layout

- `cmd/agent` - application entrypoint and dependency wiring.
- `internal/*` - core application layers (bot, interpreter, domain, adapter, session, acp, llm).
- `config` - environment-based runtime configuration.
- `prompts` - versioned LLM system prompts.
- `docs` - canonical product, architecture, and implementation documentation.

## Quick Start

### 1) Prepare environment

1. Copy `.env.example` to `.env`.
2. Fill required secrets (`TG_BOT_TOKEN`, `ACP_API_KEY`, `LLM_API_KEY`, etc.).

### 2) Start local dependencies

Run Redis only (required for current phase):

```bash
docker compose up -d redis
```

Optional ACP stack (Postgres + ACP in mock TON mode, local source at `../ACP-release-work`):

```bash
docker compose --profile acp up -d postgres acp
```

### 3) Validate repository baseline

```bash
go test ./...
go vet ./...
go build ./...
```

## Environment Variables

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `TG_BOT_TOKEN` | Yes | - | Telegram bot token |
| `TG_WEBHOOK_URL` | Yes | - | Public webhook URL |
| `TG_WEBHOOK_SECRET` | Yes | - | Telegram webhook secret token |
| `REDIS_URL` | Yes | - | Redis connection URL |
| `ACP_BASE_URL` | Yes | - | ACP API base URL |
| `ACP_API_KEY` | Yes | - | ACP API key |
| `BOOKABLY_API_URL` | Yes | - | Bookably API base URL |
| `LLM_PROVIDER` | Yes | - | `anthropic` or `openai` |
| `LLM_API_KEY` | Yes | - | LLM provider API key |
| `LLM_MODEL` | No | provider default | Optional model override |
| `MINI_APP_URL` | Yes | - | Telegram Mini App URL |
| `PORT` | No | `8080` | HTTP server port |
| `LOG_LEVEL` | No | `info` | Logger level |
| `LLM_TIMEOUT` | No | `15s` | LLM request timeout |
| `SESSION_TTL` | No | `24h` | Session TTL in Redis |
| `PLAN_TTL` | No | `15m` | Pending plan expiry |
| `ACP_POLL_INTERVAL` | No | `2s` | ACP polling interval |
| `ACP_POLL_TIMEOUT` | No | `30s` | ACP poll timeout |
| `BOOKABLY_HTTP_TIMEOUT` | No | `5s` | Bookably HTTP timeout |

## Documentation

Read docs before coding:

- `docs/CODEX_WORKFLOW.md`
- `docs/01-vision-mvp-scope.md`
- `docs/02-use-cases.md`
- `docs/03-requirements.md`
- `docs/04-architecture.md`
- `docs/05-api-contract.md`
- `docs/06-implementation-guide.md`
- `docs/07-backlog.md`
- `docs/ENGINEERING_PLAN.md`
