package bookably

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/redis/go-redis/v9"
)

func newTestAdapter(t *testing.T, serverURL string, cache redis.Cmdable) *Adapter {
	t.Helper()

	store := newMemoryTokenStore(map[string]*Token{
		"spec-1": {AccessToken: "token", RefreshToken: "refresh"},
	})
	client, err := NewClient(serverURL, "spec-1", store, http.DefaultClient, 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	adapter, err := NewAdapter(client, "spec-1", cache, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return adapter
}

func TestGetBookingsPaginationAndMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpointSpecialistBookings {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		cursor := r.URL.Query().Get("cursor")
		if cursor == "" {
			_, _ = io.WriteString(w, `{
				"bookings":[{
					"id":"b1","publicId":"BK-1","status":"CONFIRMED",
					"client":{"id":"c1","firstName":"Алина","lastName":"Смирнова"},
					"service":{"id":"s1","title":"Массаж 60 мин"},
					"slot":{"id":"sl1","startAt":"2026-03-22T11:00:00Z","endAt":"2026-03-22T12:00:00Z"}
				}],
				"nextCursor":"page2"
			}`)
			return
		}

		_, _ = io.WriteString(w, `{
			"bookings":[{
				"id":"b2","publicId":"BK-2","status":"CANCELLED",
				"client":{"id":"c2","telegramUsername":"ivanov"},
				"service":{"id":"s2","title":"Стрижка"},
				"slot":{"id":"sl2","startAt":"2026-03-22T13:00:00Z","endAt":"2026-03-22T13:30:00Z"}
			}],
			"nextCursor":""
		}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	from := time.Date(2026, 3, 22, 0, 0, 0, 0, time.FixedZone("UTC+3", 3*3600))
	to := from.Add(24 * time.Hour)

	bookings, err := adapter.GetBookings(context.Background(), "spec-1", domain.BookingFilter{
		From:      &from,
		To:        &to,
		Status:    "upcoming",
		Direction: "future",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("GetBookings: %v", err)
	}
	if len(bookings) != 2 {
		t.Fatalf("expected 2 bookings, got %d", len(bookings))
	}
	if bookings[0].ClientName != "Алина Смирнова" {
		t.Fatalf("unexpected client display name: %q", bookings[0].ClientName)
	}
	if bookings[1].ClientName != "ivanov" {
		t.Fatalf("expected username fallback, got %q", bookings[1].ClientName)
	}
	if bookings[1].Status != domain.BookingStatusCancelled {
		t.Fatalf("status mapping mismatch: %q", bookings[1].Status)
	}
}

func TestFindSlotsAndNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpointPublicSlots {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("serviceId") == "missing" {
			_, _ = io.WriteString(w, `{"slots":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"slots":[
			{"id":"1","startAt":"2026-03-22T10:00:00Z","endAt":"2026-03-22T11:00:00Z"},
			{"id":"2","startAt":"2026-03-22T11:00:00Z","endAt":"2026-03-22T12:00:00Z"},
			{"id":"3","startAt":"2026-03-22T12:00:00Z","endAt":"2026-03-22T13:00:00Z"},
			{"id":"4","startAt":"2026-03-22T13:00:00Z","endAt":"2026-03-22T14:00:00Z"},
			{"id":"5","startAt":"2026-03-22T14:00:00Z","endAt":"2026-03-22T15:00:00Z"}
		]}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	from := time.Date(2026, 3, 22, 9, 0, 0, 0, time.UTC)

	slots, err := adapter.FindSlots(context.Background(), "spec-1", domain.SlotSearchRequest{
		ServiceID:  "svc",
		From:       from,
		To:         from.Add(24 * time.Hour),
		MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("FindSlots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(slots))
	}

	_, err = adapter.FindSlots(context.Background(), "spec-1", domain.SlotSearchRequest{ServiceID: "missing", From: from, To: from.Add(time.Hour), MaxResults: 2})
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestResolveServiceByNameAndCache(t *testing.T) {
	var serviceCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpointPublicServices {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&serviceCalls, 1)
		_, _ = io.WriteString(w, `{"services":[
			{"id":"s1","title":"Массаж 60 мин","durationMin":60,"isActive":true},
			{"id":"s2","title":"Маникюр","durationMin":90,"isActive":true},
			{"id":"s3","title":"Процедура А","durationMin":60,"isActive":true},
			{"id":"s4","title":"Процедура Б","durationMin":60,"isActive":true},
			{"id":"sx","title":"Старый","durationMin":30,"isActive":false}
		]}`)
	}))
	defer server.Close()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	adapter := newTestAdapter(t, server.URL, rdb)

	svc, err := adapter.resolveServiceByName(context.Background(), "spec-1", "масс")
	if err != nil {
		t.Fatalf("resolve service масс: %v", err)
	}
	if svc.ID != "s1" {
		t.Fatalf("expected s1, got %s", svc.ID)
	}

	manicure, err := adapter.resolveServiceByName(context.Background(), "spec-1", "маникюр")
	if err != nil {
		t.Fatalf("resolve service маникюр: %v", err)
	}
	if manicure.ID != "s2" {
		t.Fatalf("expected s2, got %s", manicure.ID)
	}

	_, err = adapter.resolveServiceByName(context.Background(), "spec-1", "процедура")
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for ambiguous service, got %v", err)
	}

	_, err = adapter.resolveServiceByName(context.Background(), "spec-1", "xyz")
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown service, got %v", err)
	}

	if !mr.Exists(prefsKeyPrefix + "spec-1") {
		t.Fatalf("expected cache key %s", prefsKeyPrefix+"spec-1")
	}
	if atomic.LoadInt32(&serviceCalls) != 1 {
		t.Fatalf("expected one services API call (cache hit after), got %d", serviceCalls)
	}
}

func TestPreviewAvailabilityChange_NoWriteCallsAndConflict(t *testing.T) {
	var writeCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			atomic.AddInt32(&writeCalls, 1)
		}

		switch r.URL.Path {
		case endpointSpecialistSlots:
			_, _ = io.WriteString(w, `{"slots":[
				{"id":"sl1","startAt":"2026-03-24T10:00:00Z","endAt":"2026-03-24T11:00:00Z"},
				{"id":"sl2","startAt":"2026-03-24T11:00:00Z","endAt":"2026-03-24T12:00:00Z"},
				{"id":"sl3","startAt":"2026-03-24T12:00:00Z","endAt":"2026-03-24T13:00:00Z"}
			]}`)
		case endpointSpecialistBookings:
			_, _ = io.WriteString(w, `{"bookings":[
				{"id":"b1","publicId":"BK-1","status":"CONFIRMED","client":{"firstName":"Алина","lastName":"Смирнова"},"service":{"title":"Массаж"},"slot":{"id":"x","startAt":"2026-03-24T11:00:00Z","endAt":"2026-03-24T12:00:00Z"}}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	preview, err := adapter.PreviewAvailabilityChange(context.Background(), "spec-1", domain.ActionParams{
		DateRange: &domain.DateRange{From: "2026-03-24", To: "2026-03-24"},
		TimeRange: &domain.TimeRange{From: "11:00", To: "12:00"},
	})
	if err != nil {
		t.Fatalf("PreviewAvailabilityChange: %v", err)
	}
	if preview.AvailabilityChange.RemovedSlots != 1 {
		t.Fatalf("expected 1 removed slot, got %d", preview.AvailabilityChange.RemovedSlots)
	}
	if len(preview.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(preview.Conflicts))
	}
	if preview.RiskLevel != domain.RiskHigh {
		t.Fatalf("expected high risk, got %s", preview.RiskLevel)
	}
	if atomic.LoadInt32(&writeCalls) != 0 {
		t.Fatalf("expected no write calls in preview, got %d", writeCalls)
	}
}

func TestPreviewBookingCreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case endpointPublicServices:
			_, _ = io.WriteString(w, `{"services":[{"id":"s1","title":"Массаж 60 мин","durationMin":60,"isActive":true}]}`)
		case endpointPublicSlots:
			_, _ = io.WriteString(w, `{"slots":[
				{"id":"1","startAt":"2026-03-22T18:30:00Z","endAt":"2026-03-22T19:30:00Z"},
				{"id":"2","startAt":"2026-03-22T19:30:00Z","endAt":"2026-03-22T20:30:00Z"}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	preview, err := adapter.PreviewBookingCreate(context.Background(), "spec-1", domain.ActionParams{
		ServiceName: "Массаж",
		NotBefore:   "2026-03-22T18:00:00",
	})
	if err != nil {
		t.Fatalf("PreviewBookingCreate: %v", err)
	}
	if len(preview.ProposedSlots) != 2 {
		t.Fatalf("expected 2 proposed slots, got %d", len(preview.ProposedSlots))
	}
}

func TestPreviewBookingCancelSingleMultipleAndNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpointSpecialistBookings {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"bookings":[
			{"id":"b1","status":"CONFIRMED","client":{"firstName":"Иван","lastName":"Петров"},"service":{"title":"Стрижка"},"slot":{"startAt":"2026-03-27T11:00:00Z","endAt":"2026-03-27T11:30:00Z"}},
			{"id":"b2","status":"CONFIRMED","client":{"firstName":"Марина"},"service":{"title":"Маникюр"},"slot":{"startAt":"2026-03-28T14:00:00Z","endAt":"2026-03-28T15:00:00Z"}},
			{"id":"b3","status":"CONFIRMED","client":{"firstName":"Марина"},"service":{"title":"Педикюр"},"slot":{"startAt":"2026-04-01T11:30:00Z","endAt":"2026-04-01T12:30:00Z"}}
		]}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)

	one, err := adapter.PreviewBookingCancel(context.Background(), "spec-1", domain.ActionParams{ClientName: "Иван"})
	if err != nil {
		t.Fatalf("PreviewBookingCancel single: %v", err)
	}
	if one.BookingResult == nil || one.BookingResult.ID != "b1" {
		t.Fatalf("expected b1 result, got %+v", one.BookingResult)
	}

	_, err = adapter.PreviewBookingCancel(context.Background(), "spec-1", domain.ActionParams{ClientName: "Марина"})
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for multiple matches, got %v", err)
	}

	_, err = adapter.PreviewBookingCancel(context.Background(), "spec-1", domain.ActionParams{ClientName: "Кирилл"})
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown client, got %v", err)
	}
}

func TestGetBookingsQueryContainsUTCAndPaginationCursor(t *testing.T) {
	var seenFirst url.Values
	var seenSecond url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpointSpecialistBookings {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			seenFirst = r.URL.Query()
			_, _ = io.WriteString(w, `{"bookings":[],"nextCursor":"c2"}`)
			return
		}
		seenSecond = r.URL.Query()
		_, _ = io.WriteString(w, `{"bookings":[]}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	from := time.Date(2026, 3, 22, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*3600))
	to := from.Add(4 * time.Hour)

	_, err := adapter.GetBookings(context.Background(), "spec-1", domain.BookingFilter{From: &from, To: &to, Status: "upcoming", Limit: 50})
	if err != nil {
		t.Fatalf("GetBookings: %v", err)
	}

	if seenFirst.Get("from") != "2026-03-22T09:00:00Z" {
		t.Fatalf("expected UTC from query, got %q", seenFirst.Get("from"))
	}
	if seenSecond.Get("cursor") != "c2" {
		t.Fatalf("expected cursor c2 on second call, got %q", seenSecond.Get("cursor"))
	}
	if !strings.EqualFold(seenFirst.Get("status"), "CONFIRMED") {
		t.Fatalf("expected status CONFIRMED, got %q", seenFirst.Get("status"))
	}
}

func TestPreviewAvailabilityChangeEmptyRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case endpointSpecialistSlots:
			_, _ = io.WriteString(w, `{"slots":[]}`)
		case endpointSpecialistBookings:
			_, _ = io.WriteString(w, `{"bookings":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	preview, err := adapter.PreviewAvailabilityChange(context.Background(), "spec-1", domain.ActionParams{
		DateRange: &domain.DateRange{From: "2026-03-24", To: "2026-03-24"},
		TimeRange: &domain.TimeRange{From: "10:00", To: "11:00"},
	})
	if err != nil {
		t.Fatalf("PreviewAvailabilityChange: %v", err)
	}
	if preview.AvailabilityChange.AddedSlots != 0 || preview.AvailabilityChange.RemovedSlots != 0 || len(preview.Conflicts) != 0 {
		t.Fatalf("expected zero impact preview, got %+v", preview)
	}
}

func TestBuildWorkingHoursSlots_InvalidBreakReturnsValidation(t *testing.T) {
	from := time.Date(2026, 3, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 24, 23, 59, 59, 0, time.UTC)

	_, err := buildWorkingHoursSlots(from, to, domain.ActionParams{
		WorkingHours: &domain.TimeRange{From: "10:00", To: "18:00"},
		Breaks:       []domain.TimeRange{{From: "08:00", To: "09:00"}},
	}, time.Hour)
	if err == nil {
		t.Fatal("expected validation error for break outside working hours")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestGetProviderInfo_ReturnsMeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case endpointMe:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"code":"SERVER_ERROR","message":"me unavailable"}}`)
		case endpointPublicServices:
			t.Fatal("services endpoint should not be called when /me fails")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil)
	_, err := adapter.GetProviderInfo(context.Background(), "spec-1")
	if err == nil {
		t.Fatal("expected error from /me")
	}
	if !errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("expected ErrUpstream from /me failure, got %v", err)
	}
}
