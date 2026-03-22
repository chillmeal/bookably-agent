# BOOKABLY AGENT

## Product Vision & MVP Scope

Version 1.0 | March 2026  
Confidential - Internal working document

## 1. Vision

### One-liner

A Telegram-native AI agent that lets service providers manage availability and bookings in plain language, with ACP-powered safe execution.

### 1.1 Problem

Service professionals (massage therapists, personal trainers, coaches, beauty specialists) already live in Telegram. Their clients write to them there. Their reminders come from there. But managing availability and bookings still requires opening a separate app, navigating a calendar UI, and performing repetitive CRUD operations.

The cost is not the 30 seconds per click. The cost is the context switch: stopping a session, unlocking a phone, finding the app, and filling the form. At scale, this is tens of minutes of admin per day, time taken directly from client-facing work.

### 1.2 Solution

Bookably Agent turns natural-language messages into safe, previewable booking operations without leaving Telegram.

The agent is not a wrapper over a form. It is a conversational execution layer with three distinct responsibilities:

- Interpret: understand intent from natural language, resolve ambiguity with a single targeted question, and extract structured parameters.
- Preview: build a human-readable preview of exactly what will change (slots affected, conflicts detected, risk assessed) before touching any data.
- Execute: execute only after explicit confirmation, through ACP, which enforces policy, writes an audit trail, and provides idempotent retry.

### 1.3 Why now

- Telegram Bot API 9.3+ exposes `sendMessageDraft`, enabling native streaming responses where UX is first-class, not a workaround.
- ACP provides the execution control plane (capability registry, policy gates, lifecycle management, audit trail), turning the agent into a trustworthy system, not just a chat wrapper.
- Bookably already has the source-of-truth API. The agent needs zero duplication of business logic and calls existing endpoints.

### 1.4 What this is not

- Not an autonomous agent. Every write operation requires explicit confirmation from the provider.
- Not a client-facing chatbot. The agent serves the service provider only.
- Not a replacement for the Mini App. Complex workflows, calendar visualization, and multi-step corrections remain in the Mini App via deep link.
- Not a payment system. TON payment integration is out of scope for this release.

## 2. Positioning

| Not this | This |
| --- | --- |
| "An AI chatbot for Bookably" | "A Telegram-native booking operations agent with ACP-powered safe execution" |

The key distinction: Bookably Agent is not a chat interface bolted onto CRUD operations. It is a conversational execution layer where:

- LLM handles understanding and planning.
- ACP handles governance and safety.
- Bookably API remains the single source of truth.

### 2.1 Relationship to ACP

Bookably Agent is the reference implementation and live showcase for ACP. This is architecturally significant:

- ACP gains a real consumer with real domain actions and real risk surfaces, proving the control plane is practical.
- Bookably Agent gains durable execution, policy enforcement, audit trail, and crash recovery without implementing these concerns itself.
- The combined submission demonstrates both platform and product, stronger than two independent entries.

### 2.2 Relationship to Mini App

The agent and the Mini App are complementary, not competing:

- Agent optimizes for speed and context: provider types a sentence and action proceeds.
- Mini App optimizes for precision and visual review: complex operations, calendar views, manual corrections.

Handoff rule: if an operation requires more than two clarification exchanges, or if visual output is the primary value (calendar preview, conflict visualization), the agent surfaces a deep link into the Mini App.

## 3. Responsibility boundaries

| Component | Owns | Does not own | Calls |
| --- | --- | --- | --- |
| Bookably (Mini App) | Providers, Services, Schedules, Slots, Bookings, Clients, Payment rules | Conversation state, AI planning | - |
| Bookably Agent | Dialogue context, Intent interpretation, ActionPlan, Preview, Confirm gate | Business logic, Domain state, Execution safety | Bookably API (read for preview), ACP (write execution) |
| ACP | Capability registry, Policy enforcement, Execution lifecycle, Audit trail | Domain knowledge, UX | Bookably API (write, via HTTP steps) |
| Mini App (UI) | Complex scheduling UI, Calendar views, Manual override | Agent conversation | Bookably API directly |

## 4. MVP scope

Scope principle: MVP proves the conversational execution pattern. It does not replace full Mini App feature set. Every item not listed in section 4.1 is explicitly out of scope.

### 4.1 In scope - intent registry

| Intent | Natural language example | Confirm required | Risk level |
| --- | --- | --- | --- |
| `set_working_hours` | "Next week I work 12-20, except Friday" | Required | Medium |
| `add_break` | "Add lunch 15-16 on weekdays" | Required | Low |
| `close_range` | "Close Wednesday morning" | Required | Medium |
| `list_bookings` | "Show my bookings tomorrow" | None | None |
| `create_booking` | "Book Alina for massage 60 min tomorrow after 18" | Required | Medium |
| `cancel_booking` | "Cancel Ivan's booking on Thursday" | Required | High |
| `find_next_slot` | "Find next available window for manicure 90 min" | None | None |

### 4.2 Out of scope for MVP

The following are explicitly deferred. They are not planned for a later version; they are excluded to ensure MVP ships:

- Client-facing AI support or automated client communication.
- Marketing assistant or outbound messaging.
- Autonomous schedule changes without confirmation.
- Full feature parity with the Mini App.
- Multi-step workflows exceeding two clarification exchanges.
- TON payment integration.
- Multi-provider/workspace switching.
- Recurring availability patterns.

### 4.3 Explicit UX constraints

These are design decisions, not implementation details. They must be preserved across all development stages.

#### Preview before any write

The agent never mutates data on first request. Every write intent produces a preview that states:

- what was understood,
- which entities were resolved,
- what will change,
- what is at risk,
- what conflicts exist.

Execution follows only explicit confirmation.

#### One clarification at a time

If request is ambiguous, the agent asks exactly one question and waits. It does not output a list of clarifying questions. It asks only the single most blocking ambiguity.

#### Deep link escape hatch

If operation exceeds two clarification exchanges, or if visual output is primary value, the agent surfaces a Mini App deep link. It does not attempt complex calendar resolution in chat.

#### Streaming responses via `sendMessageDraft`

Agent responses use Telegram Bot API 9.3+ `sendMessageDraft` to stream partial content while generating. Final message (with inline confirmation keyboard) is sent only when complete response is ready. This provides native streaming UX without `editMessageText` polling.

## 5. Success criteria

### 5.1 Functional

- All 7 intents parse correctly from natural-language input with >= 85% confidence on a representative test set.
- Preview is generated without side effects (read-only) for all write intents.
- Confirmation gate fires for every write intent; no write executes without explicit user confirm.
- ACP audit trail is written for every executed run.
- Session survives bot restart (Redis-backed state).
- Deep link to Mini App is produced when escape hatch rule fires.

### 5.2 Demo

- Demo 1: provider types a multi-part availability instruction, agent produces a preview identifying affected slots and one conflict, provider confirms, change is applied.
- Demo 2: provider requests booking for a client by name and approximate time, agent finds next matching slot, proposes it, and creates booking on confirm.

### 5.3 Non-goals (not measured)

- Latency benchmarks: correctness over speed for MVP.
- Concurrent user load: single provider, single workspace.
- Localization: Russian language only for MVP.

## 6. Open questions

The following questions are unresolved and must be answered before or during implementation.

| Question | Default / assumption | Decision needed by |
| --- | --- | --- |
| Which LLM provider for the interpreter? | Abstracted and swappable. First implementation: Anthropic Claude Sonnet. | Architecture phase |
| What are the exact Bookably API endpoints for each intent? | TBD: agent adapter maps to real endpoints. | Before coding adapters |
| How does provider auth flow into the agent (token/session mapping)? | `telegram_user_id` -> provider token via env/config. | Security design |
| What is the Redis key schema for sessions? | `session:{chat_id}` with TTL 24h. | Before session implementation |
| What is the retry/timeout policy for ACP run polling? | Poll every 2s, max 30s, then surface error. | Before ACP integration |
