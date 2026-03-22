# BOOKABLY AGENT

## API Contract - Bookably Adapter

Version 1.0 | March 2026 | Doc 05 of series  
Source: `openapi.yaml` (generated 2026-03-08)  
Depends on: [04 - Architecture](./04-architecture.md)

## Scope of this document

This document defines exactly which Bookably API endpoints the agent adapter calls, with what parameters, and how responses and errors are mapped to domain types.  
It is the implementation contract for `internal/bookably/adapter.go`.

## Contract precedence note

Doc 05 contains findings from real OpenAPI analysis that supersede earlier assumptions in prior docs where they conflict (for example service-key auth assumptions).  
This document is authoritative for adapter implementation details. Prior docs are intentionally not edited in this iteration.

## 1. Critical discoveries from real API analysis

Reading the actual OpenAPI spec revealed several facts that differ from assumptions in Doc 04 (Architecture). These must be understood before implementing the adapter.

### Discovery 1 - Slot-centric model

Bookably does not have a "set working hours" endpoint. Availability is slot-centric: specialist creates individual time slot objects. There is no bulk working-hours API.

Implication:

- `set_working_hours`, `add_break`, and `close_range` map to batch slot create/delete operations, not a single API call.
- Each intent may produce `N` ACP HTTP steps (one per slot operation), or one ACP step calling a batch endpoint if one exists.

### Discovery 2 - Native idempotency in Bookably

All write endpoints (`POST cancel`, `POST create booking`, `DELETE slot`, etc.) require an `Idempotency-Key` header.

Implication:

- This aligns with ACP idempotency model.
- ACP `idempotency_key` is passed directly as Bookably `Idempotency-Key`.

### Discovery 3 - Auth is TMA JWT, not a service key

There is no service-level API key. Authentication is Bearer JWT derived from Telegram Mini App auth (`POST /api/v1/auth/tma`).  
Agent holds specialist personal JWT obtained when specialist first links the bot.

Implication:

- Agent must store and refresh specialist JWT per-session/use.
- Token is loaded from Redis at request time.
- `BOOKABLY_API_KEY` assumption in older architecture notes is incorrect for adapter auth; use per-provider token storage (`BOOKABLY_TOKEN_{specialist_id}` semantics in Redis).

### Discovery 4 - Two-phase schedule commit

`POST /api/v1/specialist/schedule/commit` exists and requires `Idempotency-Key`. Slot creates appear staged; commit likely publishes draft schedule.

Implication:

- Adapter must call commit after batch slot operations for `set_working_hours`, `add_break`, `close_range`.

### Discovery 5 - Slot create/update bodies not in OpenAPI

`POST /api/v1/specialist/slots` and `PATCH /api/v1/specialist/slots/{slotId}` return `200`, but request-body schema is not documented.

Implication:

- These endpoints require clarification from Bookably source code or manual testing before adapter can be fully implemented.

### Discovery 6 - Agent acts as specialist, not as client

For booking operations (list, cancel), agent must use `/specialist/` endpoints (admin tag), not `/public/` endpoints. Specialist JWT grants access to `/api/v1/specialist/bookings` and `/api/v1/specialist/bookings/{id}/cancel`.

## 2. Authentication model

### 2.1 Token acquisition flow

Specialist links bot once. On first message, agent calls `POST /api/v1/auth/tma` with Telegram `initData` to obtain JWT. Token is stored in Redis and refreshed proactively.

#### `POST /api/v1/auth/tma`

Authenticate via Telegram Mini App `initData`  
Auth: none | Tag: auth

Request body (not documented in OpenAPI, inferred from TMA convention):

```json
{ "initData": "<Telegram initData string>" }
```

Response `200`:

```json
{
  "accessToken": "<JWT>",
  "refreshToken": "<token>"
}
```

#### `POST /api/v1/auth/refresh`

Refresh access token  
Auth: none | Tag: auth

Request body (inferred):

```json
{ "refreshToken": "<token>" }
```

Response `200`:

```json
{ "accessToken": "<new JWT>" }
```

### 2.2 Token storage in Redis

Token storage is per-specialist, not per-session. Multiple chat sessions for same specialist share token.

```text
ba:token:{specialistId}          // { accessToken, refreshToken, expiresAt }
ba:token:{specialistId}:lock     // distributed lock for token refresh
```

TTL rule:

- Access-token key TTL is `accessToken TTL - 60s` (refresh before expiry).

### 2.3 Request auth header

```http
Authorization: Bearer <accessToken>
Content-Type: application/json
Idempotency-Key: <sha256_key>   // on all write operations
```

#### `GET /api/v1/me`

Get current actor (resolve `specialistId` from token)  
Auth: bearer | Tag: public

Called once after token acquisition to resolve/cache `specialistId`.

Response `200`:

```json
{
  "actor": {
    "telegramUserId": "123456789",
    "role": "SPECIALIST",
    "specialistId": "spec_uuid",
    "clientId": null,
    "firstName": "Мария",
    "lastName": "Иванова"
  }
}
```

## 3. Intent-to-endpoint mapping

Complete mapping from agent intents to Bookably API calls. Intents with multiple calls are multi-step.

| Intent | Method | Endpoint | Notes |
| --- | --- | --- | --- |
| `list_bookings` | GET | `/api/v1/specialist/bookings` | Read-only. No ACP run |
| `find_next_slot` | GET | `/api/v1/public/slots` | Read-only. No ACP run |
| `find_next_slot` | GET | `/api/v1/public/services` | Step 1: resolve `serviceId` from name |
| `create_booking` | GET | `/api/v1/public/slots` | Preview: find available slots |
| `create_booking` | POST | `/api/v1/public/bookings` | Execute: via ACP HTTP step |
| `cancel_booking` | GET | `/api/v1/specialist/bookings` | Preview: find booking by client name |
| `cancel_booking` | POST | `/api/v1/specialist/bookings/{id}/cancel` | Execute: via ACP HTTP step |
| `set_working_hours` | GET | `/api/v1/specialist/slots` | Preview: read existing slots in range |
| `set_working_hours` | POST | `/api/v1/specialist/slots` (xN) | Execute: create new slots (batch) |
| `set_working_hours` | DELETE | `/api/v1/specialist/slots/{id}` (xM) | Execute: delete old slots |
| `set_working_hours` | POST | `/api/v1/specialist/schedule/commit` | Execute: commit staged changes |
| `add_break` | GET | `/api/v1/specialist/slots` | Preview: find slots in break window |
| `add_break` | DELETE | `/api/v1/specialist/slots/{id}` (xN) | Execute: delete slots in break window |
| `add_break` | POST | `/api/v1/specialist/schedule/commit` | Execute: commit staged changes |
| `close_range` | GET | `/api/v1/specialist/slots` | Preview: find slots in range |
| `close_range` | DELETE | `/api/v1/specialist/slots/{id}` (xN) | Execute: delete all slots in range |
| `close_range` | POST | `/api/v1/specialist/schedule/commit` | Execute: commit staged changes |
| `set/add/close` (all) | GET | `/api/v1/specialist/bookings` | Preview: detect conflicting bookings |

### ACP multi-step run for availability intents

`set_working_hours`, `add_break`, and `close_range` require `N` slot operations plus commit.  
ACP run for these intents must contain multiple HTTP steps, executed sequentially.

If any step fails:

- run fails,
- agent surfaces error,
- partial changes should not leak (commit is last step and only reached after successful slot ops).

## 4. Endpoint reference

Full request/response schemas for all endpoints used by agent. Listed fields are what adapter uses; additional API fields are ignored.

### 4.1 List specialist bookings

`GET /api/v1/specialist/bookings`  
List all bookings for authenticated specialist  
Auth: bearer | Tag: admin

Query parameters used by agent:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `from` | date-time | no | ISO 8601 UTC. Range start. Agent converts local time using specialist timezone |
| `to` | date-time | no | ISO 8601 UTC. Range end |
| `limit` | integer | no | Agent uses `limit=50`. Range: `1-100` |
| `status` | string | no | `CONFIRMED` or `CANCELLED`. Agent omits for `all`, sets `CONFIRMED` for `upcoming` |
| `direction` | string | no | `future` for upcoming, `past` for history. Agent defaults to `future` |
| `cursor` | string | no | Pagination cursor. Agent follows cursor until range is fully fetched |

Response (`200`):

```json
{
  "bookings": [
    {
      "id": "uuid",
      "publicId": "BK-1234",
      "status": "CONFIRMED",
      "client": {
        "id": "uuid",
        "telegramUserId": "123456789",
        "firstName": "Алина",
        "lastName": "Смирнова",
        "telegramUsername": "alina_s"
      },
      "service": {
        "id": "uuid",
        "title": "Массаж 60 мин"
      },
      "slot": {
        "id": "uuid",
        "startAt": "2026-03-22T11:00:00Z",
        "endAt": "2026-03-22T12:00:00Z"
      },
      "canceledAt": null,
      "clientComment": null
    }
  ]
}
```

Domain mapping:

- each booking item -> `domain.Booking`
- client display name = `firstName + " " + lastName`, fallback to `telegramUsername`

### 4.2 Cancel booking (specialist)

`POST /api/v1/specialist/bookings/{bookingId}/cancel`  
Cancel booking as specialist  
Auth: bearer | `Idempotency-Key` required | Tag: admin

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `bookingId` | uuid (path) | yes | Booking UUID to cancel |
| `Idempotency-Key` | string (header) | yes | ACP `idempotency_key` passed verbatim |

Request body: none.  
Response `200`: updated booking with `status=CANCELLED`.

ACP HTTP step config for `cancel_booking`:

```json
{
  "type": "http",
  "capability": "booking.cancel",
  "config": {
    "method": "POST",
    "url": "{BOOKABLY_API_URL}/api/v1/specialist/bookings/{bookingId}/cancel",
    "headers": {
      "Authorization": "Bearer {accessToken}",
      "Idempotency-Key": "{idempotencyKey}"
    }
  }
}
```

### 4.3 List available slots (booking preview and `find_next_slot`)

`GET /api/v1/public/slots`  
List available slots for service in a time window  
Auth: bearer | Tag: public

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `serviceId` | uuid (query) | yes | Service UUID. Agent resolves from name via `/services` |
| `from` | date-time (query) | yes | Search window start. For `find_next_slot`: `now()` or `not_before` |
| `to` | date-time (query) | yes | Search window end. Agent searches up to `+7 days` from `from` |
| `rescheduleBookingId` | uuid (query) | no | Reschedule only; not used in MVP |

Response (`200`):

```json
{
  "slots": [
    {
      "id": "uuid",
      "startAt": "2026-03-22T18:30:00Z",
      "endAt": "2026-03-22T19:30:00Z"
    }
  ]
}
```

Agent behavior:

- pick first 2 slots after requested time
- convert to specialist local time for display

### 4.4 Create booking

`POST /api/v1/public/bookings`  
Create booking for a client  
Auth: bearer | `Idempotency-Key` required | Tag: public

Request body:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `serviceId` | uuid | yes | Service UUID from services list |
| `slotId` | uuid | yes | Slot UUID from available slots response |
| `comment` | string | no | Max 1000 chars |
| `visitAddress` | string | no | Max 255 chars; for `ONSITE` if different from service default |

Important:

- agent books on behalf of specialist JWT actor.
- confirm with Bookably whether specialist-initiated flow is truly `/public/bookings` or dedicated `/specialist/bookings` endpoint.

Response (`201`):

```json
{
  "booking": {
    "id": "uuid",
    "status": "CONFIRMED",
    "startAt": "2026-03-22T18:30:00Z",
    "service": {
      "id": "uuid",
      "title": "Массаж 60 мин",
      "durationMin": 60
    }
  }
}
```

ACP HTTP step config for `create_booking`:

```json
{
  "type": "http",
  "capability": "booking.create",
  "config": {
    "method": "POST",
    "url": "{BOOKABLY_API_URL}/api/v1/public/bookings",
    "headers": {
      "Authorization": "Bearer {accessToken}",
      "Content-Type": "application/json",
      "Idempotency-Key": "{idempotencyKey}"
    },
    "body": {
      "serviceId": "{serviceId}",
      "slotId": "{slotId}"
    }
  }
}
```

### 4.5 List services (intent parameter resolution)

`GET /api/v1/public/services`  
List active services for specialist  
Auth: bearer | Tag: public

Used to resolve `service_name -> serviceId` and fetch `durationMin` for slot search. Called during preview and cached in Redis for 5 minutes.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `specialistId` | string (query) | no | Agent passes authenticated specialist ID |
| `withAvailability` | string (query) | no | Not used by agent; availability fetched via `/slots` |

Response (`200`) - used fields:

```json
{
  "services": [
    {
      "id": "uuid",
      "title": "Массаж 60 мин",
      "name": "massage_60",
      "durationMin": 60,
      "isActive": true,
      "price": 2500,
      "currency": "RUB",
      "format": "ONSITE"
    }
  ]
}
```

Service-name resolution:

- case-insensitive substring match on `title`
- if multiple matches: `Clarification`

### 4.6 Slot management - availability intents

These endpoints implement `set_working_hours`, `add_break`, `close_range`.  
Request bodies are not fully documented in OpenAPI; below is inferred and must be verified against Bookably source.

#### `GET /api/v1/specialist/slots`

Get all slots for specialist  
Auth: bearer | Tag: specialist

Used in preview phase to count existing slots in affected range and find conflicting bookings.

Expected response (schema not in OpenAPI; verify):

```json
{
  "slots": [
    {
      "id": "uuid",
      "startAt": "2026-03-24T12:00:00Z",
      "endAt": "2026-03-24T13:00:00Z",
      "booking": null
    }
  ]
}
```

#### `POST /api/v1/specialist/slots`

Create new availability slot  
Auth: bearer | `Idempotency-Key` required | Tag: specialist

Schema gap:

- request body not documented in OpenAPI
- expected shape (to verify): `{ startAt: date-time, endAt: date-time, serviceId?: uuid }`

#### `DELETE /api/v1/specialist/slots/{slotId}`

Delete slot (remove availability)  
Auth: bearer | `Idempotency-Key` required | Tag: specialist

No request body. Path param `slotId`. Response `200` on success.

ACP HTTP step config for each slot deletion (one step per slot):

```json
{
  "type": "http",
  "capability": "availability.delete_slot",
  "config": {
    "method": "DELETE",
    "url": "{BOOKABLY_API_URL}/api/v1/specialist/slots/{slotId}",
    "headers": {
      "Authorization": "Bearer {accessToken}",
      "Idempotency-Key": "{idempotencyKey}:{slotId}"
    }
  }
}
```

#### `POST /api/v1/specialist/schedule/commit`

Commit staged schedule changes  
Auth: bearer | `Idempotency-Key` required | Tag: specialist

Called as final step of any availability-mutating ACP run. No request body documented. Response `200`.

ACP HTTP step config for schedule commit (always last step):

```json
{
  "type": "http",
  "capability": "availability.commit",
  "config": {
    "method": "POST",
    "url": "{BOOKABLY_API_URL}/api/v1/specialist/schedule/commit",
    "headers": {
      "Authorization": "Bearer {accessToken}",
      "Idempotency-Key": "{idempotencyKey}:commit"
    }
  }
}
```

## 5. Error response format & mapping

### 5.1 Error envelope

All Bookably non-2xx errors use consistent JSON envelope:

```json
{
  "error": {
    "code": "BOOKING_NOT_FOUND",
    "message": "Booking not found",
    "details": null
  }
}
```

### 5.2 HTTP status -> domain error mapping

| HTTP | Code example | Domain error | Agent response | Retry? |
| --- | --- | --- | --- | --- |
| `401` | `UNAUTHORIZED` | `ErrUnauthorized` | Refresh token, retry once. If still `401`: re-auth required | Yes (once) |
| `403` | `FORBIDDEN` | `ErrForbidden` | `Нет доступа к этой операции` + Mini App link | No |
| `404` | `NOT_FOUND` | `ErrNotFound` | `Запись не найдена` (preview phase) | No |
| `409` | `CONFLICT` | `ErrConflict` | Show conflict in preview; execute blocked | No |
| `422` | `VALIDATION_ERROR` | `ErrValidation` | Message from `error.message` + `error.details` | No |
| `429` | `RATE_LIMIT_EXCEEDED` | `ErrRateLimit` | Wait `Retry-After`, then retry | Yes (after wait) |
| `5xx` | `SERVER_ERROR` | `ErrUpstream` | `Сервис временно недоступен` + retry + Mini App link | Yes (same key) |

### 5.3 Rate limiting

Bookably returns `429` with `Retry-After` header (seconds). Adapter must:

- read `Retry-After` on `429`
- wait specified duration before retry (respect context deadline; total <= 30s)
- if `Retry-After > 30s`, surface `ErrUpstream` with Mini App deep link
- never classify rate limit as ACP policy violation (it is transient)

### 5.4 Token expiry handling

JWT has finite TTL. Adapter handles `401` with single refresh attempt:

```go
func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
    token, err := c.tokenStore.GetToken(ctx, c.specialistID)
    if err != nil {
        return nil, err
    }

    req.Header.Set("Authorization", "Bearer "+token.AccessToken)
    resp, err := c.http.Do(req)
    if err != nil {
        return nil, err
    }

    if resp.StatusCode == 401 {
        // Attempt refresh once
        newToken, refreshErr := c.refreshToken(ctx, token.RefreshToken)
        if refreshErr != nil {
            return nil, ErrUnauthorized
        }
        c.tokenStore.SaveToken(ctx, c.specialistID, newToken)

        // Retry original request with new token
        req.Header.Set("Authorization", "Bearer "+newToken.AccessToken)
        return c.http.Do(req)
    }
    return resp, nil
}
```

## 6. Domain type mapping

How Bookably response fields map to domain types in `internal/domain/types.go`.

### 6.1 Booking

| `domain.Booking` field | Bookably API field | Notes |
| --- | --- | --- |
| `ID` | `bookings[].id` | UUID string |
| `PublicID` | `bookings[].publicId` | Human-readable (`BK-1234`), shown in messages |
| `ClientName` | `client.firstName + lastName` | Fallback: `client.telegramUsername`, then `telegramUserId` |
| `ServiceName` | `service.title` | Nullable; fallback `услуга` if null |
| `ServiceID` | `service.id` | Nullable |
| `At` | `slot.startAt` | Parse as UTC `time.Time`; convert to specialist TZ for display |
| `DurationMin` | `slot.endAt - slot.startAt` | Compute diff in minutes |
| `Status` | `status` | `CONFIRMED -> BookingStatusUpcoming`, `CANCELLED -> BookingStatusCancelled` |
| `Notes` | `clientComment` | Nullable; passthrough |

### 6.2 Slot

| `domain.Slot` field | Bookably API field | Notes |
| --- | --- | --- |
| `ID` | `slots[].id` | UUID string; required for booking creation |
| `Start` | `slots[].startAt` | Parse as UTC `time.Time` |
| `End` | `slots[].endAt` | Parse as UTC `time.Time` |
| `ServiceID` | query param | Not in slot response; taken from `serviceId` query used to fetch slots |

## 7. Open questions requiring Bookably clarification

These items must be resolved before adapter can be fully implemented.

| # | Question | Why it blocks |
| --- | --- | --- |
| 1 | Exact request body schema for `POST /api/v1/specialist/slots`? Are `startAt/endAt` required? Is `serviceId` in body or slot is service-agnostic? | Cannot implement `set_working_hours` / `add_break` safely |
| 2 | Is `POST /api/v1/specialist/schedule/commit` required after slot creates/deletes, or are slot changes immediately live? | Determines whether commit step is mandatory in ACP run |
| 3 | Does `POST /api/v1/public/bookings` create booking for token holder only, or require `clientId` to specify client? | Specialist-initiated booking flow correctness depends on this |
| 4 | Does `GET /api/v1/specialist/slots` accept `from/to` query params for range filtering? | Without range filter, adapter must fetch all slots and filter in memory |
| 5 | Which error code is returned when slot becomes unavailable at execution time? | Required for correct error classification (`ErrConflict` vs `ErrValidation`) |
