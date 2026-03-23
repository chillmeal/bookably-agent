package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
)

type inMemorySessionStore struct {
	mu       sync.Mutex
	sessions map[int64]*session.Session
}

func newInMemorySessionStore() *inMemorySessionStore {
	return &inMemorySessionStore{
		sessions: make(map[int64]*session.Session),
	}
}

func (s *inMemorySessionStore) Get(ctx context.Context, chatID int64) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.sessions[chatID]
	if !ok {
		return &session.Session{
			ChatID:        chatID,
			DialogHistory: make([]session.Message, 0, 10),
		}, nil
	}
	return cloneSession(existing), nil
}

func (s *inMemorySessionStore) Save(ctx context.Context, in *session.Session) error {
	if in == nil {
		return errors.New("nil session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[in.ChatID] = cloneSession(in)
	return nil
}

func (s *inMemorySessionStore) Delete(ctx context.Context, chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, chatID)
	return nil
}

func cloneSession(in *session.Session) *session.Session {
	if in == nil {
		return nil
	}
	payload, _ := json.Marshal(in)
	var out session.Session
	_ = json.Unmarshal(payload, &out)
	if out.DialogHistory == nil {
		out.DialogHistory = make([]session.Message, 0, 10)
	}
	return &out
}

type mockInterpreter struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error)
}

func (m *mockInterpreter) Interpret(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
	m.mu.Lock()
	m.calls++
	fn := m.fn
	m.mu.Unlock()

	if fn == nil {
		return nil, errors.New("mock interpreter: fn is nil")
	}
	return fn(ctx, userMessage, convo)
}

func (m *mockInterpreter) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type mockProvider struct {
	mu sync.Mutex

	getBookingsCalls          int
	findSlotsCalls            int
	previewAvailabilityCalls  int
	previewBookingCreateCalls int
	previewBookingCancelCalls int
	getProviderInfoCalls      int
	getBookingsFn             func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error)
	findSlotsFn               func(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error)
	previewAvailabilityFn     func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewBookingCreateFn    func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewBookingCancelFn    func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	getProviderInfoFn         func(ctx context.Context, providerID string) (*domain.ProviderInfo, error)
}

func (m *mockProvider) GetBookings(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
	m.mu.Lock()
	m.getBookingsCalls++
	fn := m.getBookingsFn
	m.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, providerID, f)
}

func (m *mockProvider) FindSlots(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error) {
	m.mu.Lock()
	m.findSlotsCalls++
	fn := m.findSlotsFn
	m.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, providerID, req)
}

func (m *mockProvider) GetProviderInfo(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
	m.mu.Lock()
	m.getProviderInfoCalls++
	fn := m.getProviderInfoFn
	m.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, providerID)
}

func (m *mockProvider) PreviewAvailabilityChange(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewAvailabilityCalls++
	fn := m.previewAvailabilityFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

func (m *mockProvider) PreviewBookingCreate(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewBookingCreateCalls++
	fn := m.previewBookingCreateFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

func (m *mockProvider) PreviewBookingCancel(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewBookingCancelCalls++
	fn := m.previewBookingCancelFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

func (m *mockProvider) GetBookingsCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getBookingsCalls
}

func (m *mockProvider) FindSlotsCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.findSlotsCalls
}

type mockExecutor struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error)
}

func (m *mockExecutor) ExecuteConfirmed(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error) {
	m.mu.Lock()
	m.calls++
	fn := m.fn
	m.mu.Unlock()
	if fn == nil {
		return &ExecutionResult{Message: "ok"}, nil
	}
	return fn(ctx, s, pending)
}

func (m *mockExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type mockTokenStore struct {
	mu      sync.Mutex
	tokens  map[string]AuthToken
	saves   int
	saveErr error
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{tokens: make(map[string]AuthToken)}
}

func (m *mockTokenStore) SaveToken(ctx context.Context, specialistID string, token AuthToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saves++
	m.tokens[specialistID] = token
	return nil
}

func (m *mockTokenStore) SaveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saves
}

type telegramMessageCall struct {
	ChatID   int64
	Text     string
	Keyboard *InlineKeyboardMarkup
}

type editMarkupCall struct {
	ChatID    int64
	MessageID int64
	Markup    *InlineKeyboardMarkup
}

type setWebhookCall struct {
	WebhookURL     string
	Secret         string
	AllowedUpdates []string
}

type mockTelegramGateway struct {
	mu sync.Mutex

	sendChatActionCalls []struct {
		ChatID int64
		Action string
	}
	answerCallbackCalls []struct {
		CallbackID string
		Text       string
	}
	editMarkupCalls []editMarkupCall
	sendTextCalls   []telegramMessageCall
	draftCalls      []telegramMessageCall
	finalizeCalls   []telegramMessageCall
	setWebhookCalls []setWebhookCall

	nextMessageID int64
}

func newMockTelegramGateway() *mockTelegramGateway {
	return &mockTelegramGateway{nextMessageID: 100}
}

func (m *mockTelegramGateway) SendChatAction(ctx context.Context, chatID int64, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendChatActionCalls = append(m.sendChatActionCalls, struct {
		ChatID int64
		Action string
	}{ChatID: chatID, Action: action})
	return nil
}

func (m *mockTelegramGateway) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answerCallbackCalls = append(m.answerCallbackCalls, struct {
		CallbackID string
		Text       string
	}{CallbackID: callbackID, Text: text})
	return nil
}

func (m *mockTelegramGateway) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int64, markup *InlineKeyboardMarkup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editMarkupCalls = append(m.editMarkupCalls, editMarkupCall{
		ChatID:    chatID,
		MessageID: messageID,
		Markup:    cloneMarkup(markup),
	})
	return nil
}

func (m *mockTelegramGateway) SendText(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendTextCalls = append(m.sendTextCalls, telegramMessageCall{
		ChatID:   chatID,
		Text:     text,
		Keyboard: cloneMarkup(keyboard),
	})
	m.nextMessageID++
	return m.nextMessageID, nil
}

func (m *mockTelegramGateway) Draft(ctx context.Context, chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draftCalls = append(m.draftCalls, telegramMessageCall{
		ChatID: chatID,
		Text:   text,
	})
	return nil
}

func (m *mockTelegramGateway) Finalize(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeCalls = append(m.finalizeCalls, telegramMessageCall{
		ChatID:   chatID,
		Text:     text,
		Keyboard: cloneMarkup(keyboard),
	})
	m.nextMessageID++
	return m.nextMessageID, nil
}

func (m *mockTelegramGateway) SetWebhook(ctx context.Context, webhookURL, secret string, allowedUpdates []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setWebhookCalls = append(m.setWebhookCalls, setWebhookCall{
		WebhookURL:     webhookURL,
		Secret:         secret,
		AllowedUpdates: append([]string(nil), allowedUpdates...),
	})
	return nil
}

func cloneMarkup(markup *InlineKeyboardMarkup) *InlineKeyboardMarkup {
	if markup == nil {
		return nil
	}
	payload, _ := json.Marshal(markup)
	var out InlineKeyboardMarkup
	_ = json.Unmarshal(payload, &out)
	return &out
}

func newTestHandler(t *testing.T, store *inMemorySessionStore, interp *mockInterpreter, provider *mockProvider, executor *mockExecutor, tg *mockTelegramGateway) *Handler {
	return newTestHandlerWithTokenStore(t, store, interp, provider, executor, nil, tg)
}

func newTestHandlerWithTokenStore(t *testing.T, store *inMemorySessionStore, interp *mockInterpreter, provider *mockProvider, executor *mockExecutor, tokenStore *mockTokenStore, tg *mockTelegramGateway) *Handler {
	t.Helper()

	if store == nil {
		store = newInMemorySessionStore()
	}
	if interp == nil {
		interp = &mockInterpreter{
			fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
				return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
			},
		}
	}
	if provider == nil {
		provider = &mockProvider{}
	}
	if executor == nil {
		executor = &mockExecutor{}
	}
	if tg == nil {
		tg = newMockTelegramGateway()
	}
	if tokenStore == nil {
		tokenStore = newMockTokenStore()
	}

	handler, err := NewHandler(
		HandlerConfig{
			WebhookSecret:     "secret",
			WebhookURL:        "https://example.test/webhook",
			MiniAppURL:        "https://mini.app/open",
			DefaultProviderID: "prov-1",
			PlanTTL:           15 * time.Minute,
		},
		store,
		interp,
		provider,
		executor,
		tokenStore,
		tg,
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func seedAuthorizedSession(t *testing.T, store *inMemorySessionStore, chatID int64) {
	t.Helper()
	if err := store.Save(context.Background(), &session.Session{
		ChatID:        chatID,
		ProviderID:    "prov-1",
		Timezone:      "UTC",
		DialogHistory: make([]session.Message, 0, 10),
	}); err != nil {
		t.Fatalf("seed authorized session: %v", err)
	}
}

func makeMessageBody(t *testing.T, chatID int64, text string) []byte {
	t.Helper()
	payload := map[string]any{
		"message": map[string]any{
			"message_id": 1,
			"date":       1,
			"chat": map[string]any{
				"id": chatID,
			},
			"from": map[string]any{
				"id": chatID,
			},
			"text": text,
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal message body: %v", err)
	}
	return out
}

func makeWebAppDataBody(t *testing.T, chatID int64, payload string) []byte {
	t.Helper()
	raw := map[string]any{
		"message": map[string]any{
			"message_id": 1,
			"date":       1,
			"chat": map[string]any{
				"id": chatID,
			},
			"from": map[string]any{
				"id": chatID,
			},
			"web_app_data": map[string]any{
				"data":        payload,
				"button_text": "Войти в Bookably",
			},
		},
	}
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal web_app_data body: %v", err)
	}
	return out
}

func makeCallbackBody(t *testing.T, chatID int64, callbackID, data string, messageID int64) []byte {
	t.Helper()
	payload := map[string]any{
		"callback_query": map[string]any{
			"id": callbackID,
			"from": map[string]any{
				"id": chatID,
			},
			"message": map[string]any{
				"message_id": messageID,
				"chat": map[string]any{
					"id": chatID,
				},
			},
			"data": data,
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal callback body: %v", err)
	}
	return out
}

func sendWebhook(t *testing.T, h *Handler, secret string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set(telegramSecretHeader, secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerRegisterWebhook(t *testing.T) {
	tg := newMockTelegramGateway()
	h := newTestHandler(t, nil, nil, nil, nil, tg)

	if err := h.RegisterWebhook(context.Background()); err != nil {
		t.Fatalf("RegisterWebhook: %v", err)
	}

	if len(tg.setWebhookCalls) != 1 {
		t.Fatalf("expected 1 setWebhook call, got %d", len(tg.setWebhookCalls))
	}
	call := tg.setWebhookCalls[0]
	if call.WebhookURL != "https://example.test/webhook" {
		t.Fatalf("unexpected webhook url: %q", call.WebhookURL)
	}
	if call.Secret != "secret" {
		t.Fatalf("unexpected webhook secret: %q", call.Secret)
	}
	if len(call.AllowedUpdates) != 2 || call.AllowedUpdates[0] != "message" || call.AllowedUpdates[1] != "callback_query" {
		t.Fatalf("unexpected allowed updates: %#v", call.AllowedUpdates)
	}
}

func TestHandlerServeHTTPRejectsWrongSecret(t *testing.T) {
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
		},
	}
	h := newTestHandler(t, nil, interp, nil, nil, nil)

	rec := sendWebhook(t, h, "wrong-secret", makeMessageBody(t, 10, "Привет"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if interp.CallCount() != 0 {
		t.Fatalf("interpreter should not be called, got %d", interp.CallCount())
	}
}

func TestHandlerServeHTTPRoutesMessageOnce(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 11)
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
		},
	}
	h := newTestHandler(t, store, interp, nil, nil, nil)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 11, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if interp.CallCount() != 1 {
		t.Fatalf("expected 1 interpreter call, got %d", interp.CallCount())
	}
}

func TestHandlerSerializesProcessingPerChat(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 12)
	var inFlight int32
	var maxInFlight int32

	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
		},
	}
	h := newTestHandler(t, store, interp, nil, nil, nil)

	const requests = 2
	var wg sync.WaitGroup
	wg.Add(requests)
	errCh := make(chan error, requests)

	for i := 0; i < requests; i++ {
		go func() {
			defer wg.Done()
			rec := sendWebhook(t, h, "secret", makeMessageBody(t, 12, "Привет"))
			if rec.Code != http.StatusOK {
				errCh <- errors.New("non-200 response")
				return
			}
			errCh <- nil
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}
	if atomic.LoadInt32(&maxInFlight) != 1 {
		t.Fatalf("expected max in-flight interpreter calls to be 1, got %d", maxInFlight)
	}
}

func TestHandlerMessageWriteIntentBuildsPreviewAndPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 13)
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{
				Intent:     interpreter.IntentSetWorkingHours,
				Confidence: 0.95,
				Params: interpreter.ActionParams{
					DateRange: &interpreter.DateRange{From: "2026-03-24", To: "2026-03-27"},
					WorkingHours: &interpreter.TimeRange{
						From: "12:00",
						To:   "20:00",
					},
				},
			}, nil
		},
	}
	provider := &mockProvider{
		previewAvailabilityFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
			return &domain.Preview{
				Summary: "Период 24-27 марта",
				AvailabilityChange: domain.AvailabilityChange{
					AddedSlots:   8,
					RemovedSlots: 2,
				},
				RiskLevel: domain.RiskMedium,
			}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 13, "На следующей неделе работаю с 12 до 20"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if len(tg.sendChatActionCalls) != 1 || tg.sendChatActionCalls[0].Action != "typing" {
		t.Fatalf("expected typing chat action, got %#v", tg.sendChatActionCalls)
	}
	if len(tg.draftCalls) == 0 {
		t.Fatal("expected draft call")
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(tg.finalizeCalls))
	}
	if tg.finalizeCalls[0].Keyboard == nil {
		t.Fatal("expected preview keyboard on finalize")
	}

	saved, err := store.Get(context.Background(), 13)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("expected pending plan to be saved")
	}
	if saved.PendingPlan.PreviewMsgID == 0 {
		t.Fatal("expected preview message id to be set")
	}
	if len(saved.PendingPlan.IdempotencyKey) != 64 {
		t.Fatalf("expected 64-char idempotency hash, got %q", saved.PendingPlan.IdempotencyKey)
	}
	if saved.PendingPlan.Plan.Intent != interpreter.IntentSetWorkingHours {
		t.Fatalf("unexpected pending intent: %s", saved.PendingPlan.Plan.Intent)
	}
}

func TestHandlerMessageUnknownIntentOnboardingNoKeyboard(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 14)
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, nil, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 14, "Как дела"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(tg.finalizeCalls))
	}
	if tg.finalizeCalls[0].Keyboard != nil {
		t.Fatal("unknown intent must not include keyboard")
	}
	if !strings.Contains(tg.finalizeCalls[0].Text, "управлять расписанием") {
		t.Fatalf("unexpected onboarding text: %q", tg.finalizeCalls[0].Text)
	}

	saved, err := store.Get(context.Background(), 14)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must remain nil for unknown intent")
	}
}

func TestHandlerReadOnlyListBookingsCallsProviderWithoutACP(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 15)
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{
				Intent:     interpreter.IntentListBookings,
				Confidence: 0.91,
				Params: interpreter.ActionParams{
					DateRange: &interpreter.DateRange{From: "2026-03-22", To: "2026-03-22"},
				},
			}, nil
		},
	}
	provider := &mockProvider{
		getBookingsFn: func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
			return []domain.Booking{
				{
					ClientName:  "Алина",
					ServiceName: "Массаж",
					At:          time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
				},
			}, nil
		},
	}
	executor := &mockExecutor{}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, provider, executor, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 15, "Покажи записи на сегодня"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if provider.GetBookingsCallCount() != 1 {
		t.Fatalf("expected GetBookings to be called once, got %d", provider.GetBookingsCallCount())
	}
	if executor.CallCount() != 0 {
		t.Fatalf("expected no ACP execution for read-only intent, got %d", executor.CallCount())
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(tg.finalizeCalls))
	}
	if tg.finalizeCalls[0].Keyboard != nil {
		t.Fatal("list_bookings response should not include keyboard")
	}
}

func TestHandlerReadOnlyListBookingsLongRangeEscalatesDeepLink(t *testing.T) {
	store := newInMemorySessionStore()
	seedAuthorizedSession(t, store, 16)
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{
				Intent:     interpreter.IntentListBookings,
				Confidence: 0.91,
				Params: interpreter.ActionParams{
					DateRange: &interpreter.DateRange{From: "2026-03-01", To: "2026-03-12"},
				},
			}, nil
		},
	}
	provider := &mockProvider{}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 16, "Покажи записи за месяц"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if provider.GetBookingsCallCount() != 0 {
		t.Fatalf("provider.GetBookings must not be called on deep-link escalation, got %d", provider.GetBookingsCallCount())
	}
	if len(tg.sendTextCalls) != 1 {
		t.Fatalf("expected one SendText call, got %d", len(tg.sendTextCalls))
	}
	if tg.sendTextCalls[0].Keyboard == nil ||
		len(tg.sendTextCalls[0].Keyboard.InlineKeyboard) == 0 ||
		tg.sendTextCalls[0].Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatal("expected deep-link web_app keyboard")
	}
}

func TestHandlerMessageUnauthenticatedShowsLoginButton(t *testing.T) {
	store := newInMemorySessionStore()
	interp := &mockInterpreter{
		fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, nil, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 66, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if interp.CallCount() != 0 {
		t.Fatalf("interpreter must not be called for unauthenticated message, got %d", interp.CallCount())
	}
	if len(tg.sendTextCalls) != 1 {
		t.Fatalf("expected one login message, got %d", len(tg.sendTextCalls))
	}
	call := tg.sendTextCalls[0]
	if call.Keyboard == nil || len(call.Keyboard.InlineKeyboard) == 0 || call.Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("expected web_app login keyboard, got %#v", call.Keyboard)
	}
	if !strings.Contains(call.Keyboard.InlineKeyboard[0][0].WebApp.URL, "mode=bot_auth") {
		t.Fatalf("expected bot_auth mode in login URL, got %q", call.Keyboard.InlineKeyboard[0][0].WebApp.URL)
	}
}

func TestHandlerWebAppDataAuthSuccessSavesTokenAndSession(t *testing.T) {
	store := newInMemorySessionStore()
	tokenStore := newMockTokenStore()
	provider := &mockProvider{
		getProviderInfoFn: func(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
			return &domain.ProviderInfo{ProviderID: "prov-1", Timezone: "Europe/Moscow"}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandlerWithTokenStore(t, store, nil, provider, nil, tokenStore, tg)

	payload := `{"token":"access-1","refreshToken":"refresh-1","specialistId":"prov-1"}`
	rec := sendWebhook(t, h, "secret", makeWebAppDataBody(t, 67, payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if tokenStore.SaveCount() != 1 {
		t.Fatalf("expected one token save, got %d", tokenStore.SaveCount())
	}

	saved, err := store.Get(context.Background(), 67)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.ProviderID != "prov-1" {
		t.Fatalf("expected provider id prov-1, got %q", saved.ProviderID)
	}
	if saved.Timezone != "Europe/Moscow" {
		t.Fatalf("expected timezone Europe/Moscow, got %q", saved.Timezone)
	}
	if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "Вошёл") {
		t.Fatalf("expected auth success message, got %#v", tg.sendTextCalls)
	}
}

func TestHandlerWebAppDataSpecialistMismatchShowsLogin(t *testing.T) {
	store := newInMemorySessionStore()
	tokenStore := newMockTokenStore()
	tg := newMockTelegramGateway()
	h := newTestHandlerWithTokenStore(t, store, nil, nil, nil, tokenStore, tg)

	payload := `{"token":"access-1","refreshToken":"refresh-1","specialistId":"other-spec"}`
	rec := sendWebhook(t, h, "secret", makeWebAppDataBody(t, 68, payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if tokenStore.SaveCount() != 0 {
		t.Fatalf("expected no token save on specialist mismatch, got %d", tokenStore.SaveCount())
	}
	if len(tg.sendTextCalls) < 2 {
		t.Fatalf("expected mismatch warning + login prompt, got %d calls", len(tg.sendTextCalls))
	}
	last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
	if last.Keyboard == nil || len(last.Keyboard.InlineKeyboard) == 0 || last.Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("expected login web_app keyboard, got %#v", last.Keyboard)
	}
}

func TestHandlerWebAppDataInvalidPayloadShowsLogin(t *testing.T) {
	store := newInMemorySessionStore()
	tokenStore := newMockTokenStore()
	tg := newMockTelegramGateway()
	h := newTestHandlerWithTokenStore(t, store, nil, nil, nil, tokenStore, tg)

	rec := sendWebhook(t, h, "secret", makeWebAppDataBody(t, 69, "{bad json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if tokenStore.SaveCount() != 0 {
		t.Fatalf("expected no token save on invalid payload, got %d", tokenStore.SaveCount())
	}
	if len(tg.sendTextCalls) < 2 {
		t.Fatalf("expected error + login prompt, got %d calls", len(tg.sendTextCalls))
	}
	last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
	if last.Keyboard == nil || len(last.Keyboard.InlineKeyboard) == 0 || last.Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("expected login web_app keyboard, got %#v", last.Keyboard)
	}
}

func TestHandlerCallbackConfirmSuccessClearsPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	now := time.Now().UTC()
	seed := &session.Session{
		ChatID:     17,
		Timezone:   "UTC",
		ProviderID: "prov-1",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-1",
			PreviewMsgID: 55,
			CreatedAt:    now,
			Plan: interpreter.ActionPlan{
				Intent:     interpreter.IntentCancelBooking,
				Confidence: 0.9,
			},
		},
	}
	if err := store.Save(context.Background(), seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	executor := &mockExecutor{
		fn: func(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error) {
			return &ExecutionResult{Message: "Готово. Отмена применена."}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
	}}, nil, executor, tg)
	h.clock = func() time.Time { return now.Add(1 * time.Minute) }

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 17, "cb-1", ConfirmData("plan-1"), 55))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if executor.CallCount() != 1 {
		t.Fatalf("expected one executor call, got %d", executor.CallCount())
	}
	if len(tg.answerCallbackCalls) != 1 {
		t.Fatalf("expected one answerCallback call, got %d", len(tg.answerCallbackCalls))
	}
	if len(tg.editMarkupCalls) == 0 {
		t.Fatal("expected keyboard removal call")
	}

	saved, err := store.Get(context.Background(), 17)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared after successful confirm")
	}
}

func TestHandlerCallbackCancelDoesNotCallACPAndClearsPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	seed := &session.Session{
		ChatID:     18,
		Timezone:   "UTC",
		ProviderID: "prov-1",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-2",
			PreviewMsgID: 77,
			CreatedAt:    time.Now().UTC(),
			Plan: interpreter.ActionPlan{
				Intent:     interpreter.IntentSetWorkingHours,
				Confidence: 0.95,
			},
		},
	}
	if err := store.Save(context.Background(), seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	executor := &mockExecutor{}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, nil, executor, tg)

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 18, "cb-2", CancelData("plan-2"), 77))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if executor.CallCount() != 0 {
		t.Fatalf("expected no ACP call on cancel, got %d", executor.CallCount())
	}
	if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "ничего не изменено") {
		t.Fatalf("expected cancel confirmation text, got %#v", tg.sendTextCalls)
	}

	saved, err := store.Get(context.Background(), 18)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared after cancel")
	}
}

func TestHandlerCallbackConfirmContractBlockedShowsDeepLinkAndClearsPending(t *testing.T) {
	store := newInMemorySessionStore()
	seed := &session.Session{
		ChatID:     19,
		Timezone:   "UTC",
		ProviderID: "prov-1",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-3",
			PreviewMsgID: 88,
			CreatedAt:    time.Now().UTC(),
			Plan: interpreter.ActionPlan{
				Intent:     interpreter.IntentCreateBooking,
				Confidence: 0.95,
			},
		},
	}
	if err := store.Save(context.Background(), seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	executor := &mockExecutor{
		fn: func(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error) {
			return nil, errors.Join(ErrExecutionContractBlocked, errors.New("blocked by backend gap"))
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, nil, executor, tg)

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 19, "cb-3", ConfirmData("plan-3"), 88))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.sendTextCalls) == 0 {
		t.Fatal("expected contract-blocked user message")
	}
	last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
	if last.Keyboard == nil || len(last.Keyboard.InlineKeyboard) == 0 || last.Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("expected deep link web_app keyboard on contract blocked, got %#v", last.Keyboard)
	}
	if !strings.Contains(last.Text, "временно недоступна") {
		t.Fatalf("unexpected contract-blocked text: %q", last.Text)
	}

	saved, err := store.Get(context.Background(), 19)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared after contract-blocked confirm")
	}
}

func TestHandlerCallbackSlotSelectionUsesPendingSnapshotWithoutProviderRefetch(t *testing.T) {
	store := newInMemorySessionStore()
	now := time.Now().UTC().Truncate(time.Second)
	seed := &session.Session{
		ChatID:     70,
		Timezone:   "UTC",
		ProviderID: "prov-1",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-slot",
			PreviewMsgID: 99,
			CreatedAt:    now,
			Plan: interpreter.ActionPlan{
				Intent:     interpreter.IntentCreateBooking,
				Confidence: 0.9,
				Params: interpreter.ActionParams{
					ServiceID: "svc-1",
				},
			},
			SlotCandidates: []session.PendingSlotCandidate{
				{
					ID:        "slot-1",
					ServiceID: "svc-1",
					StartAt:   now.Add(30 * time.Minute).Format(time.RFC3339),
					EndAt:     now.Add(90 * time.Minute).Format(time.RFC3339),
				},
			},
		},
	}
	if err := store.Save(context.Background(), seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	provider := &mockProvider{
		findSlotsFn: func(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error) {
			return nil, errors.New("must not call provider.FindSlots on slot callback")
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, provider, nil, tg)
	h.clock = func() time.Time { return now.Add(1 * time.Minute) }

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 70, "cb-slot", SlotData(0, "plan-slot"), 99))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if provider.FindSlotsCallCount() != 0 {
		t.Fatalf("expected provider.FindSlots call count 0, got %d", provider.FindSlotsCallCount())
	}
	if len(tg.finalizeCalls) == 0 {
		t.Fatal("expected finalize call for create confirm preview")
	}

	saved, err := store.Get(context.Background(), 70)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("expected pending plan to persist")
	}
	if saved.PendingPlan.Plan.Params.SlotID != "slot-1" {
		t.Fatalf("expected selected slot id slot-1, got %q", saved.PendingPlan.Plan.Params.SlotID)
	}
}

func TestHandlerBuildDeepLink(t *testing.T) {
	h := newTestHandler(t, nil, nil, nil, nil, nil)
	link := h.BuildDeepLink("bookings", "range > 7 days")

	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	if parsed.Query().Get("action") != "bookings" {
		t.Fatalf("unexpected action query value: %q", parsed.Query().Get("action"))
	}
	if parsed.Query().Get("context") != "range > 7 days" {
		t.Fatalf("unexpected context query value: %q", parsed.Query().Get("context"))
	}
}
