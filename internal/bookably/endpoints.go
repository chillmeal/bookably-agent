package bookably

// API endpoints used by the Bookably adapter.
const (
	endpointAuthTMA              = "/api/v1/auth/tma"
	endpointAuthRefresh          = "/api/v1/auth/refresh"
	endpointMe                   = "/api/v1/me"
	endpointSpecialistBookings   = "/api/v1/specialist/bookings"
	endpointPublicSlots          = "/api/v1/public/slots"
	endpointPublicServices       = "/api/v1/public/services"
	endpointPublicSpecialistProfile = "/api/v1/public/specialist/profile"
	endpointSpecialistSlots      = "/api/v1/specialist/slots"
	endpointSpecialistCommit     = "/api/v1/specialist/schedule/commit"
	endpointPublicBookings       = "/api/v1/public/bookings"
	endpointSpecialistBookCancel = "/api/v1/specialist/bookings/%s/cancel"
)

const (
	tokenKeyPrefix = "ba:token:"
	prefsKeyPrefix = "ba:prefs:"
)

/*
P3-01 Resolution (Doc 05 section 7), source of truth:
- c:\project\booking-backend\src
- c:\project\booking-backend\openapi\openapi.yaml
- c:\project\booking-backend\docs
- c:\project\booking-backend\tests

Resolved findings:
1) POST /api/v1/specialist/slots request body uses slot timestamps (`startAt`, `endAt`);
   `serviceId` is not required by this endpoint body contract.
2) POST /api/v1/specialist/schedule/commit is a real commit endpoint with operation groups
   (`create`, `delete`, `availability`, etc.); not a no-op/publish call.
3) Specialist-initiated create booking for an arbitrary named client is not exposed as a
   dedicated backend endpoint contract. Current public booking write path is client-context.
4) GET /api/v1/specialist/slots expects `from`/`to` range query parameters.
5) Execution-time booking conflict is returned as HTTP 409 with `SLOT_NOT_AVAILABLE`
   (plus related slot-conflict codes for specialist slot mutations).

Runtime policy from these findings:
- create_booking execution in agent stays fail-safe blocked until backend introduces an explicit
  specialist-initiated create-booking contract for named clients.
*/
