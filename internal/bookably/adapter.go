package bookably

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/redis/go-redis/v9"
)

const (
	defaultBookingsLimit = 50
	defaultSlotsWindow   = 7 * 24 * time.Hour
	defaultPrefsCacheTTL = 5 * time.Minute
	defaultSlotDuration  = time.Hour
	dayEndDuration       = 24*time.Hour - time.Nanosecond
	maxCancelSearchRange = 31 * 24 * time.Hour
)

var errNoBreakWindows = errors.New("bookably adapter: no breaks provided")

type Adapter struct {
	client   *Client
	cache    redis.Cmdable
	prefsTTL time.Duration
}

func NewAdapter(client *Client, cache redis.Cmdable, prefsTTL time.Duration) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("bookably adapter: client is nil")
	}
	if prefsTTL <= 0 {
		prefsTTL = defaultPrefsCacheTTL
	}
	return &Adapter{client: client, cache: cache, prefsTTL: prefsTTL}, nil
}

type apiBooking struct {
	ID            string `json:"id"`
	PublicID      string `json:"publicId"`
	Status        string `json:"status"`
	CanceledAt    string `json:"canceledAt"`
	ClientComment string `json:"clientComment"`
	Client        struct {
		ID               string `json:"id"`
		TelegramUserID   string `json:"telegramUserId"`
		FirstName        string `json:"firstName"`
		LastName         string `json:"lastName"`
		TelegramUsername string `json:"telegramUsername"`
	} `json:"client"`
	Service struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"service"`
	Slot struct {
		ID      string `json:"id"`
		StartAt string `json:"startAt"`
		EndAt   string `json:"endAt"`
	} `json:"slot"`
}

type bookingsResponse struct {
	Bookings   []apiBooking `json:"bookings"`
	Cursor     string       `json:"cursor"`
	NextCursor string       `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}

type apiSlot struct {
	ID      string `json:"id"`
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
}

type slotsResponse struct {
	Slots []apiSlot `json:"slots"`
}

type servicesResponse struct {
	Services []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Name        string `json:"name"`
		DurationMin int    `json:"durationMin"`
		IsActive    bool   `json:"isActive"`
	} `json:"services"`
}

type meResponse struct {
	Actor struct {
		SpecialistID string `json:"specialistId"`
		Role         string `json:"role"`
	} `json:"actor"`
}

type specialistProfileResponse struct {
	Specialist *struct {
		ID       string `json:"id"`
		Timezone string `json:"timezone"`
	} `json:"specialist"`
}

type prefsCachePayload struct {
	Services []domain.Service `json:"services"`
	SavedAt  time.Time        `json:"saved_at"`
}

func (a *Adapter) GetBookings(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
	q := url.Values{}
	if f.From != nil {
		q.Set("from", f.From.UTC().Format(time.RFC3339))
	}
	if f.To != nil {
		q.Set("to", f.To.UTC().Format(time.RFC3339))
	}
	if f.Limit <= 0 {
		f.Limit = defaultBookingsLimit
	}
	q.Set("limit", fmt.Sprintf("%d", f.Limit))

	status := strings.ToLower(strings.TrimSpace(f.Status))
	switch status {
	case "", "all":
		// no filter
	case "upcoming":
		q.Set("status", "CONFIRMED")
	case "past":
		q.Set("status", "CANCELLED")
	default:
		q.Set("status", f.Status)
	}

	direction := strings.ToLower(strings.TrimSpace(f.Direction))
	if direction == "" {
		direction = "future"
	}
	q.Set("direction", direction)

	cursor := strings.TrimSpace(f.Cursor)
	if cursor != "" {
		q.Set("cursor", cursor)
	}

	all := make([]domain.Booking, 0)
	for {
		if cursor != "" {
			q.Set("cursor", cursor)
		} else {
			q.Del("cursor")
		}

		var out bookingsResponse
		if err := a.client.GetJSON(ctx, endpointSpecialistBookings, q, &out); err != nil {
			return nil, err
		}

		mapped, err := mapBookings(out.Bookings)
		if err != nil {
			return nil, err
		}
		all = append(all, mapped...)

		nextCursor := strings.TrimSpace(out.NextCursor)
		if nextCursor == "" && out.HasMore {
			nextCursor = strings.TrimSpace(out.Cursor)
		}
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].At.Before(all[j].At)
	})
	return all, nil
}

func (a *Adapter) FindSlots(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error) {
	if strings.TrimSpace(req.ServiceID) == "" {
		return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: service_id is required"))
	}
	if req.From.IsZero() {
		req.From = time.Now().UTC()
	}
	if req.To.IsZero() || !req.To.After(req.From) {
		req.To = req.From.Add(defaultSlotsWindow)
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 2
	}

	q := url.Values{}
	q.Set("serviceId", req.ServiceID)
	q.Set("from", req.From.UTC().Format(time.RFC3339))
	q.Set("to", req.To.UTC().Format(time.RFC3339))

	var out slotsResponse
	if err := a.client.GetJSON(ctx, endpointPublicSlots, q, &out); err != nil {
		return nil, err
	}

	slots := make([]domain.Slot, 0, len(out.Slots))
	for _, s := range out.Slots {
		start, err := parseAPITime(s.StartAt)
		if err != nil {
			return nil, err
		}
		end, err := parseAPITime(s.EndAt)
		if err != nil {
			return nil, err
		}
		slots = append(slots, domain.Slot{ID: s.ID, Start: start, End: end, ServiceID: req.ServiceID})
	}

	sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })
	if len(slots) == 0 {
		return nil, errors.Join(domain.ErrNotFound, errors.New("bookably adapter: no slots available"))
	}
	if len(slots) > req.MaxResults {
		slots = slots[:req.MaxResults]
	}
	return slots, nil
}

func (a *Adapter) GetProviderInfo(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
	resolvedProviderID := a.resolveProviderID(providerID)

	var me meResponse
	if err := a.client.GetJSON(ctx, endpointMe, nil, &me); err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(me.Actor.Role), "SPECIALIST") {
		return nil, errors.Join(domain.ErrForbidden, errors.New("bookably adapter: actor is not specialist"))
	}
	if strings.TrimSpace(me.Actor.SpecialistID) != "" {
		resolvedProviderID = me.Actor.SpecialistID
	}
	if strings.TrimSpace(resolvedProviderID) == "" {
		return nil, errors.Join(domain.ErrForbidden, errors.New("bookably adapter: specialist actor has no specialist id"))
	}

	services, err := a.listServices(ctx, resolvedProviderID)
	if err != nil {
		return nil, err
	}

	timezone, err := a.fetchSpecialistTimezone(ctx, resolvedProviderID)
	if err != nil {
		return nil, err
	}
	return &domain.ProviderInfo{ProviderID: resolvedProviderID, Timezone: timezone, Services: services}, nil
}

func (a *Adapter) PreviewAvailabilityChange(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	from, to, err := parseDateRangeUTC(p.DateRange)
	if err != nil {
		return nil, err
	}

	slots, err := a.listSpecialistSlots(ctx, from, to)
	if err != nil {
		return nil, err
	}

	bookings, err := a.GetBookings(ctx, providerID, domain.BookingFilter{
		From:   &from,
		To:     &to,
		Status: "upcoming",
		Limit:  defaultBookingsLimit,
	})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	removedWindows := make([]timeWindow, 0)
	addedSlots := 0
	removedSlots := 0
	createSlots := make([]domain.Slot, 0)
	deleteSlotIDs := make([]string, 0)

	switch {
	case p.WorkingHours != nil:
		targetSlots, targetErr := buildWorkingHoursSlots(from, to, p, inferSlotDuration(slots))
		if targetErr != nil {
			return nil, targetErr
		}

		existingMap := make(map[string]domain.Slot, len(slots))
		for _, s := range slots {
			existingMap[slotKey(s.Start, s.End)] = s
		}
		targetMap := make(map[string]domain.Slot, len(targetSlots))
		for _, s := range targetSlots {
			targetMap[slotKey(s.Start, s.End)] = s
		}

		for key, s := range existingMap {
			if _, ok := targetMap[key]; !ok {
				removedSlots++
				removedWindows = append(removedWindows, timeWindow{From: s.Start, To: s.End})
				if strings.TrimSpace(s.ID) != "" {
					deleteSlotIDs = append(deleteSlotIDs, strings.TrimSpace(s.ID))
				}
			}
		}
		for key, s := range targetMap {
			if _, ok := existingMap[key]; !ok {
				addedSlots++
				createSlots = append(createSlots, domain.Slot{Start: s.Start, End: s.End})
			}
		}

	case p.BreakSlot != nil || len(p.Breaks) > 0:
		windows, buildErr := buildBreakWindows(from, to, p)
		if buildErr != nil {
			return nil, buildErr
		}
		for _, s := range slots {
			if overlapsAny(s.Start, s.End, windows) {
				removedSlots++
				removedWindows = append(removedWindows, timeWindow{From: s.Start, To: s.End})
				if strings.TrimSpace(s.ID) != "" {
					deleteSlotIDs = append(deleteSlotIDs, strings.TrimSpace(s.ID))
				}
			}
		}

	default:
		windows, buildErr := buildCloseWindows(from, to, p.TimeRange)
		if buildErr != nil {
			return nil, buildErr
		}
		for _, s := range slots {
			if overlapsAny(s.Start, s.End, windows) {
				removedSlots++
				removedWindows = append(removedWindows, timeWindow{From: s.Start, To: s.End})
				if strings.TrimSpace(s.ID) != "" {
					deleteSlotIDs = append(deleteSlotIDs, strings.TrimSpace(s.ID))
				}
			}
		}
	}

	conflicts := findConflicts(bookings, removedWindows)
	risk := determineRisk(addedSlots, removedSlots, len(conflicts))

	sort.Slice(createSlots, func(i, j int) bool { return createSlots[i].Start.Before(createSlots[j].Start) })
	sort.Strings(deleteSlotIDs)
	var availabilityExec *domain.AvailabilityExecutionPayload
	if len(createSlots) > 0 || len(deleteSlotIDs) > 0 {
		availabilityExec = &domain.AvailabilityExecutionPayload{
			CreateSlots:   createSlots,
			DeleteSlotIDs: deleteSlotIDs,
		}
	}

	return &domain.Preview{
		Summary: fmt.Sprintf("Preview availability impact: +%d slots, -%d slots", addedSlots, removedSlots),
		AvailabilityChange: domain.AvailabilityChange{
			AddedSlots:   addedSlots,
			RemovedSlots: removedSlots,
		},
		AvailabilityExec: availabilityExec,
		Conflicts:        conflicts,
		RiskLevel:        risk,
	}, nil
}

func (a *Adapter) PreviewBookingCreate(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	serviceID := strings.TrimSpace(p.ServiceID)
	serviceName := strings.TrimSpace(p.ServiceName)

	if serviceID == "" {
		if serviceName == "" {
			return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: service is required"))
		}
		resolved, err := a.resolveServiceByName(ctx, a.resolveProviderID(providerID), serviceName)
		if err != nil {
			return nil, err
		}
		serviceID = resolved.ID
		serviceName = resolved.Title
	}

	from, err := parseOptionalDateTimeUTC(p.NotBefore, p.PreferredAt, p.PreferredDate)
	if err != nil {
		return nil, err
	}
	to := from.Add(defaultSlotsWindow)

	slots, err := a.FindSlots(ctx, providerID, domain.SlotSearchRequest{
		ServiceID:  serviceID,
		From:       from,
		To:         to,
		MaxResults: 2,
	})
	if err != nil {
		return nil, err
	}

	return &domain.Preview{
		Summary:       fmt.Sprintf("Proposed booking slots for %s", serviceName),
		ProposedSlots: slots,
		RiskLevel:     domain.RiskMedium,
	}, nil
}

func (a *Adapter) PreviewBookingCancel(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	filter, exactTimeRef, hasExactTime, err := buildCancelFilter(p, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(p.BookingID) != "" {
		bookings, err := a.GetBookings(ctx, providerID, filter)
		if err != nil {
			return nil, err
		}
		for _, b := range bookings {
			if b.ID == p.BookingID {
				return &domain.Preview{Summary: "Booking selected for cancellation", BookingResult: &b, RiskLevel: domain.RiskHigh}, nil
			}
		}
		return nil, errors.Join(domain.ErrNotFound, fmt.Errorf("bookably adapter: booking %s not found", p.BookingID))
	}

	bookings, err := a.GetBookings(ctx, providerID, filter)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	nameNeedle := strings.ToLower(strings.TrimSpace(p.ClientName))
	matches := make([]domain.Booking, 0)
	for _, b := range bookings {
		if nameNeedle != "" && !strings.Contains(strings.ToLower(strings.TrimSpace(b.ClientName)), nameNeedle) {
			continue
		}
		if hasExactTime && !cancelTimeMatches(b.At, exactTimeRef, true) {
			continue
		}
		matches = append(matches, b)
	}

	if !hasExactTime && strings.TrimSpace(p.ApproximateTime) != "" {
		approx, hasClock, parseErr := parseCancelApproximateTime(strings.TrimSpace(p.ApproximateTime))
		if parseErr != nil {
			return nil, parseErr
		}
		filtered := make([]domain.Booking, 0, len(matches))
		for _, b := range matches {
			if cancelTimeMatches(b.At, approx, hasClock) {
				filtered = append(filtered, b)
			}
		}
		matches = filtered
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].At.Before(matches[j].At)
	})

	switch len(matches) {
	case 0:
		return nil, errors.Join(domain.ErrNotFound, fmt.Errorf("bookably adapter: no booking found for client %q", p.ClientName))
	case 1:
		result := matches[0]
		return &domain.Preview{
			Summary:       "Booking selected for cancellation",
			BookingResult: &result,
			RiskLevel:     domain.RiskHigh,
		}, nil
	default:
		if len(matches) > 3 {
			matches = matches[:3]
		}
		return &domain.Preview{
			Summary:           "Multiple bookings matched for cancellation",
			BookingCandidates: matches,
			RiskLevel:         domain.RiskHigh,
		}, nil
	}
}

func (a *Adapter) resolveProviderID(providerID string) string {
	return strings.TrimSpace(providerID)
}

func (a *Adapter) listServices(ctx context.Context, providerID string) ([]domain.Service, error) {
	if cached, ok := a.readServicesCache(ctx, providerID); ok {
		return cached, nil
	}

	q := url.Values{}
	if strings.TrimSpace(providerID) != "" {
		q.Set("specialistId", providerID)
	}

	var out servicesResponse
	if err := a.client.GetJSON(ctx, endpointPublicServices, q, &out); err != nil {
		return nil, err
	}

	services := make([]domain.Service, 0, len(out.Services))
	for _, s := range out.Services {
		if !s.IsActive {
			continue
		}
		services = append(services, domain.Service{
			ID:          s.ID,
			Title:       s.Title,
			DurationMin: s.DurationMin,
			IsActive:    s.IsActive,
		})
	}

	sort.Slice(services, func(i, j int) bool {
		return strings.ToLower(services[i].Title) < strings.ToLower(services[j].Title)
	})

	a.writeServicesCache(ctx, providerID, services)
	return services, nil
}

func (a *Adapter) resolveServiceByName(ctx context.Context, providerID, name string) (*domain.Service, error) {
	services, err := a.listServices(ctx, providerID)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: service_name is empty"))
	}

	matches := make([]domain.Service, 0, 3)
	for _, svc := range services {
		title := strings.ToLower(strings.TrimSpace(svc.Title))
		if strings.Contains(title, needle) {
			matches = append(matches, svc)
		}
	}

	switch len(matches) {
	case 0:
		return nil, errors.Join(domain.ErrNotFound, fmt.Errorf("bookably adapter: service %q not found", name))
	case 1:
		return &matches[0], nil
	default:
		return nil, errors.Join(domain.ErrConflict, fmt.Errorf("bookably adapter: service %q is ambiguous (%d matches)", name, len(matches)))
	}
}

func (a *Adapter) readServicesCache(ctx context.Context, providerID string) ([]domain.Service, bool) {
	if a.cache == nil {
		return nil, false
	}
	payload, err := a.cache.Get(ctx, prefsKeyPrefix+providerID).Bytes()
	if err != nil {
		return nil, false
	}
	var entry prefsCachePayload
	if err := json.Unmarshal(payload, &entry); err != nil {
		return nil, false
	}
	return entry.Services, true
}

func (a *Adapter) writeServicesCache(ctx context.Context, providerID string, services []domain.Service) {
	if a.cache == nil {
		return
	}
	entry := prefsCachePayload{Services: services, SavedAt: time.Now().UTC()}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = a.cache.Set(ctx, prefsKeyPrefix+providerID, payload, a.prefsTTL).Err()
}

func (a *Adapter) listSpecialistSlots(ctx context.Context, from, to time.Time) ([]domain.Slot, error) {
	query := url.Values{}
	query.Set("from", from.UTC().Format(time.RFC3339))
	query.Set("to", to.UTC().Format(time.RFC3339))

	var out slotsResponse
	if err := a.client.GetJSON(ctx, endpointSpecialistSlots, query, &out); err != nil {
		return nil, err
	}

	slots := make([]domain.Slot, 0, len(out.Slots))
	for _, s := range out.Slots {
		start, parseErr := parseAPITime(s.StartAt)
		if parseErr != nil {
			return nil, parseErr
		}
		end, parseErr := parseAPITime(s.EndAt)
		if parseErr != nil {
			return nil, parseErr
		}
		if start.Before(to) && end.After(from) {
			slots = append(slots, domain.Slot{ID: s.ID, Start: start, End: end})
		}
	}
	return slots, nil
}

func (a *Adapter) fetchSpecialistTimezone(ctx context.Context, specialistID string) (string, error) {
	q := url.Values{}
	q.Set("specialistId", strings.TrimSpace(specialistID))

	var out specialistProfileResponse
	if err := a.client.GetJSON(ctx, endpointPublicSpecialistProfile, q, &out); err != nil {
		return "", err
	}
	if out.Specialist == nil {
		return "", errors.Join(domain.ErrNotFound, fmt.Errorf("bookably adapter: specialist profile %q not found", specialistID))
	}
	timezone := strings.TrimSpace(out.Specialist.Timezone)
	if timezone == "" {
		return "", errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: specialist profile %q has empty timezone", specialistID))
	}
	return timezone, nil
}

func mapBookings(in []apiBooking) ([]domain.Booking, error) {
	out := make([]domain.Booking, 0, len(in))
	for _, item := range in {
		start, err := parseAPITime(item.Slot.StartAt)
		if err != nil {
			return nil, err
		}
		end, err := parseAPITime(item.Slot.EndAt)
		if err != nil {
			return nil, err
		}

		status := domain.BookingStatusUpcoming
		if strings.EqualFold(item.Status, "CANCELLED") {
			status = domain.BookingStatusCancelled
		}

		out = append(out, domain.Booking{
			ID:          item.ID,
			PublicID:    item.PublicID,
			ClientID:    item.Client.ID,
			ClientName:  displayClientName(item.Client.FirstName, item.Client.LastName, item.Client.TelegramUsername, item.Client.TelegramUserID),
			ServiceID:   item.Service.ID,
			ServiceName: fallbackString(item.Service.Title, "услуга"),
			At:          start,
			DurationMin: int(end.Sub(start).Minutes()),
			Status:      status,
			Notes:       item.ClientComment,
		})
	}
	return out, nil
}

func displayClientName(firstName, lastName, username, telegramUserID string) string {
	fullName := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if fullName != "" {
		return fullName
	}
	if strings.TrimSpace(username) != "" {
		return username
	}
	if strings.TrimSpace(telegramUserID) != "" {
		return telegramUserID
	}
	return "Клиент"
}

func fallbackString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func parseAPITime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: parse time %q: %w", raw, err))
	}
	return t.UTC(), nil
}

type timeWindow struct {
	From time.Time
	To   time.Time
}

func overlapsAny(start, end time.Time, windows []timeWindow) bool {
	for _, w := range windows {
		if start.Before(w.To) && end.After(w.From) {
			return true
		}
	}
	return false
}

func slotKey(start, end time.Time) string {
	return start.UTC().Format(time.RFC3339Nano) + "|" + end.UTC().Format(time.RFC3339Nano)
}

func inferSlotDuration(slots []domain.Slot) time.Duration {
	best := defaultSlotDuration
	for _, s := range slots {
		d := s.End.Sub(s.Start)
		if d > 0 && d < best {
			best = d
		}
	}
	return best
}

func parseDateRangeUTC(r *domain.DateRange) (time.Time, time.Time, error) {
	if r == nil || strings.TrimSpace(r.From) == "" || strings.TrimSpace(r.To) == "" {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, errors.New("bookably adapter: date_range.from and date_range.to are required"))
	}
	from, err := time.ParseInLocation("2006-01-02", r.From, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid date_range.from: %w", err))
	}
	to, err := time.ParseInLocation("2006-01-02", r.To, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid date_range.to: %w", err))
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, errors.New("bookably adapter: date range is inverted"))
	}
	return from, to.Add(dayEndDuration), nil
}

func parseClock(clock string) (int, int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(clock))
	if err != nil {
		return 0, 0, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid HH:MM %q", clock))
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func dateAt(t time.Time, h, m int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, time.UTC)
}

func buildCloseWindows(from, to time.Time, tr *domain.TimeRange) ([]timeWindow, error) {
	windows := make([]timeWindow, 0)
	for day := dateAt(from, 0, 0); !day.After(to); day = day.AddDate(0, 0, 1) {
		if tr == nil || (strings.TrimSpace(tr.From) == "" && strings.TrimSpace(tr.To) == "") {
			windows = append(windows, timeWindow{From: day, To: day.Add(24 * time.Hour)})
			continue
		}
		fromH, fromM, err := parseClock(tr.From)
		if err != nil {
			return nil, err
		}
		toH, toM, err := parseClock(tr.To)
		if err != nil {
			return nil, err
		}
		start := dateAt(day, fromH, fromM)
		end := dateAt(day, toH, toM)
		if !end.After(start) {
			return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: close time_range.to must be after from"))
		}
		windows = append(windows, timeWindow{From: start, To: end})
	}
	return windows, nil
}

func buildBreakWindows(from, to time.Time, p domain.ActionParams) ([]timeWindow, error) {
	breaks := make([]domain.TimeRange, 0)
	if p.BreakSlot != nil {
		breaks = append(breaks, *p.BreakSlot)
	}
	breaks = append(breaks, p.Breaks...)
	if len(breaks) == 0 {
		return nil, errNoBreakWindows
	}

	weekdaySet := normalizeWeekdaySet(p.Weekdays)
	windows := make([]timeWindow, 0)
	for day := dateAt(from, 0, 0); !day.After(to); day = day.AddDate(0, 0, 1) {
		if len(weekdaySet) > 0 {
			if _, ok := weekdaySet[strings.ToLower(day.Weekday().String()[:3])]; !ok {
				continue
			}
		}
		for _, br := range breaks {
			fromH, fromM, err := parseClock(br.From)
			if err != nil {
				return nil, err
			}
			toH, toM, err := parseClock(br.To)
			if err != nil {
				return nil, err
			}
			start := dateAt(day, fromH, fromM)
			end := dateAt(day, toH, toM)
			if !end.After(start) {
				return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: break.to must be after break.from"))
			}
			windows = append(windows, timeWindow{From: start, To: end})
		}
	}
	return windows, nil
}

func buildWorkingHoursSlots(from, to time.Time, p domain.ActionParams, slotDuration time.Duration) ([]domain.Slot, error) {
	if p.WorkingHours == nil {
		return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: working_hours are required"))
	}
	startH, startM, err := parseClock(p.WorkingHours.From)
	if err != nil {
		return nil, err
	}
	endH, endM, err := parseClock(p.WorkingHours.To)
	if err != nil {
		return nil, err
	}

	weekdaySet := normalizeWeekdaySet(p.Weekdays)
	breakWindows, err := buildBreakWindows(from, to, domain.ActionParams{Breaks: p.Breaks, BreakSlot: p.BreakSlot, Weekdays: p.Weekdays})
	if err != nil && !errors.Is(err, errNoBreakWindows) {
		return nil, err
	}

	slots := make([]domain.Slot, 0)
	for day := dateAt(from, 0, 0); !day.After(to); day = day.AddDate(0, 0, 1) {
		if len(weekdaySet) > 0 {
			if _, ok := weekdaySet[strings.ToLower(day.Weekday().String()[:3])]; !ok {
				continue
			}
		}

		workStart := dateAt(day, startH, startM)
		workEnd := dateAt(day, endH, endM)
		if !workEnd.After(workStart) {
			return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: working_hours.to must be after from"))
		}

		for _, bw := range breakWindows {
			if sameDayUTC(day, bw.From) && (bw.From.Before(workStart) || bw.To.After(workEnd)) {
				return nil, errors.Join(domain.ErrValidation, errors.New("bookably adapter: break must be within working hours"))
			}
		}

		for cursor := workStart; cursor.Add(slotDuration).Before(workEnd) || cursor.Add(slotDuration).Equal(workEnd); cursor = cursor.Add(slotDuration) {
			next := cursor.Add(slotDuration)
			if overlapsAny(cursor, next, breakWindows) {
				continue
			}
			slots = append(slots, domain.Slot{Start: cursor.UTC(), End: next.UTC()})
		}
	}
	return slots, nil
}

func normalizeWeekdaySet(in []string) map[string]struct{} {
	set := make(map[string]struct{}, len(in))
	for _, item := range in {
		v := strings.ToLower(strings.TrimSpace(item))
		if v == "" {
			continue
		}
		if len(v) > 3 {
			v = v[:3]
		}
		set[v] = struct{}{}
	}
	return set
}

func sameDayUTC(day time.Time, value time.Time) bool {
	return day.Year() == value.Year() && day.Month() == value.Month() && day.Day() == value.Day()
}

func findConflicts(bookings []domain.Booking, removed []timeWindow) []domain.Conflict {
	if len(bookings) == 0 || len(removed) == 0 {
		return nil
	}
	conflicts := make([]domain.Conflict, 0)
	for _, b := range bookings {
		for _, w := range removed {
			if !b.At.Before(w.From) && b.At.Before(w.To) {
				conflicts = append(conflicts, domain.Conflict{
					BookingID:   b.ID,
					ClientName:  b.ClientName,
					ServiceName: b.ServiceName,
					At:          b.At,
					Reason:      "booking falls into removed slot",
				})
				break
			}
		}
	}
	return conflicts
}

func determineRisk(added, removed, conflicts int) domain.RiskLevel {
	if conflicts > 0 {
		return domain.RiskHigh
	}
	if added+removed > 10 {
		return domain.RiskMedium
	}
	return domain.RiskLow
}

func parseOptionalDateTimeUTC(candidates ...string) (time.Time, error) {
	for _, raw := range candidates {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid datetime %q", value))
	}
	return time.Now().UTC(), nil
}

func buildCancelFilter(p domain.ActionParams, now time.Time) (domain.BookingFilter, time.Time, bool, error) {
	filter := domain.BookingFilter{
		Status:    "upcoming",
		Direction: "future",
		Limit:     defaultBookingsLimit,
	}

	if strings.TrimSpace(p.ApproximateTime) != "" {
		approx, _, err := parseCancelApproximateTime(strings.TrimSpace(p.ApproximateTime))
		if err != nil {
			return domain.BookingFilter{}, time.Time{}, false, err
		}
		from, to := dayBoundsUTC(approx)
		filter.From = &from
		filter.To = &to
		return clampCancelFilterRange(filter), approx, true, nil
	}

	if p.DateRange != nil && strings.TrimSpace(p.DateRange.From) != "" {
		from, to, err := parseCancelDateRangeUTC(p.DateRange)
		if err != nil {
			return domain.BookingFilter{}, time.Time{}, false, err
		}
		filter.From = &from
		filter.To = &to
		return clampCancelFilterRange(filter), time.Time{}, false, nil
	}

	from := now.UTC()
	to := from.Add(maxCancelSearchRange)
	filter.From = &from
	filter.To = &to
	return filter, time.Time{}, false, nil
}

func parseCancelApproximateTime(raw string) (time.Time, bool, error) {
	value := strings.TrimSpace(raw)
	layoutsWithClock := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	}
	for _, layout := range layoutsWithClock {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true, nil
		}
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), false, nil
	}
	return time.Time{}, false, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid approximate_time %q", raw))
}

func parseCancelDateRangeUTC(r *domain.DateRange) (time.Time, time.Time, error) {
	from, err := time.Parse("2006-01-02", strings.TrimSpace(r.From))
	if err != nil {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid cancel date_range.from: %w", err))
	}
	toRaw := strings.TrimSpace(r.To)
	if toRaw == "" {
		dayFrom, dayTo := dayBoundsUTC(from.UTC())
		return dayFrom, dayTo, nil
	}
	to, err := time.Parse("2006-01-02", toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, fmt.Errorf("bookably adapter: invalid cancel date_range.to: %w", err))
	}
	fromUTC := from.UTC()
	toUTC := to.UTC().Add(dayEndDuration)
	if toUTC.Before(fromUTC) {
		return time.Time{}, time.Time{}, errors.Join(domain.ErrValidation, errors.New("bookably adapter: cancel date range is inverted"))
	}
	return fromUTC, toUTC, nil
}

func dayBoundsUTC(t time.Time) (time.Time, time.Time) {
	day := time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return day, day.Add(dayEndDuration)
}

func clampCancelFilterRange(filter domain.BookingFilter) domain.BookingFilter {
	if filter.From == nil || filter.To == nil {
		return filter
	}
	from := filter.From.UTC()
	to := filter.To.UTC()
	maxTo := from.Add(maxCancelSearchRange)
	if to.After(maxTo) {
		to = maxTo
	}
	filter.From = &from
	filter.To = &to
	return filter
}

func cancelTimeMatches(bookingAt time.Time, reference time.Time, hasClock bool) bool {
	b := bookingAt.UTC()
	r := reference.UTC()
	if hasClock {
		diff := b.Sub(r)
		if diff < 0 {
			diff = -diff
		}
		return diff <= 6*time.Hour
	}
	return b.Year() == r.Year() && b.Month() == r.Month() && b.Day() == r.Day()
}
