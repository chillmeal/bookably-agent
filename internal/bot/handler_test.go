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
	return &inMemorySessionStore{sessions: make(map[int64]*session.Session)}
}

func (s *inMemorySessionStore) Get(ctx context.Context, chatID int64) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.sessions[chatID]
	if !ok {
		return &session.Session{ChatID: chatID, DialogHistory: make([]session.Message, 0, 10)}, nil
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
	payload, _ := json.Marshal(in)
	var out session.Session
	_ = json.Unmarshal(payload, &out)
	if out.DialogHistory == nil {
		out.DialogHistory = make([]session.Message, 0, 10)
	}
	return &out
}

type mockInterpreter struct {
	mu       sync.Mutex
	calls    int
	fn       func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error)
	streamFn func(ctx context.Context, userMessage string, convo interpreter.ConversationContext, onProgress func(interpreter.Progress)) (*interpreter.ActionPlan, error)
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

func (m *mockInterpreter) InterpretWithProgress(ctx context.Context, userMessage string, convo interpreter.ConversationContext, onProgress func(interpreter.Progress)) (*interpreter.ActionPlan, error) {
	m.mu.Lock()
	m.calls++
	fn := m.streamFn
	fallback := m.fn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, userMessage, convo, onProgress)
	}
	if fallback == nil {
		return nil, errors.New("mock interpreter: fn is nil")
	}
	return fallback(ctx, userMessage, convo)
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

	getBookingsFn          func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error)
	findSlotsFn            func(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error)
	previewAvailabilityFn  func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewBookingCreateFn func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewBookingCancelFn func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	getProviderInfoFn      func(ctx context.Context, providerID string) (*domain.ProviderInfo, error)
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
		return &domain.ProviderInfo{ProviderID: "spec-1", Timezone: "UTC"}, nil
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
	nextMessageID   int64
	editMarkupErr   error
	draftErr        error
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
	if m.editMarkupErr != nil {
		return m.editMarkupErr
	}
	return nil
}

func (m *mockTelegramGateway) SendText(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendTextCalls = append(m.sendTextCalls, telegramMessageCall{ChatID: chatID, Text: text, Keyboard: cloneMarkup(keyboard)})
	m.nextMessageID++
	return m.nextMessageID, nil
}

func (m *mockTelegramGateway) Draft(ctx context.Context, chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draftCalls = append(m.draftCalls, telegramMessageCall{ChatID: chatID, Text: text})
	if m.draftErr != nil {
		return m.draftErr
	}
	return nil
}

func (m *mockTelegramGateway) Finalize(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeCalls = append(m.finalizeCalls, telegramMessageCall{ChatID: chatID, Text: text, Keyboard: cloneMarkup(keyboard)})
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

type failingTelegramGateway struct {
	*mockTelegramGateway
	sendTextErr error
}

func (m *failingTelegramGateway) SendText(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	if m.sendTextErr != nil {
		return 0, m.sendTextErr
	}
	return m.mockTelegramGateway.SendText(ctx, chatID, text, keyboard)
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
	t.Helper()

	if store == nil {
		store = newInMemorySessionStore()
	}
	if interp == nil {
		interp = &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
			return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
		}}
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

	handler, err := NewHandler(HandlerConfig{
		WebhookSecret: "secret",
		WebhookURL:    "https://example.test/webhook",
		MiniAppURL:    "https://mini.app/open",
		PlanTTL:       15 * time.Minute,
	}, store, interp, provider, executor, tg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func seedSession(t *testing.T, store *inMemorySessionStore, s *session.Session) {
	t.Helper()
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func makeMessageBody(t *testing.T, chatID int64, fromID int64, text string) []byte {
	t.Helper()
	payload := map[string]any{
		"message": map[string]any{
			"message_id": 1,
			"date":       1,
			"chat":       map[string]any{"id": chatID},
			"from":       map[string]any{"id": fromID},
			"text":       text,
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal message body: %v", err)
	}
	return out
}

func makeMessageBodyWithUpdateID(t *testing.T, updateID int64, chatID int64, fromID int64, text string) []byte {
	t.Helper()
	payload := map[string]any{
		"update_id": updateID,
		"message": map[string]any{
			"message_id": 1,
			"date":       1,
			"chat":       map[string]any{"id": chatID},
			"from":       map[string]any{"id": fromID},
			"text":       text,
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal message body: %v", err)
	}
	return out
}

func makeCallbackBody(t *testing.T, chatID int64, fromID int64, callbackID, data string, messageID int64) []byte {
	t.Helper()
	payload := map[string]any{
		"callback_query": map[string]any{
			"id": callbackID,
			"from": map[string]any{
				"id": fromID,
			},
			"message": map[string]any{
				"message_id": messageID,
				"chat":       map[string]any{"id": chatID},
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
	if !h.WaitForIdle(2 * time.Second) {
		t.Fatal("timeout waiting handler background worker")
	}
	return rec
}

func sendWebhookNoWait(t *testing.T, h *Handler, secret string, body []byte) *httptest.ResponseRecorder {
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
}

func TestHandlerServeHTTPRejectsWrongSecret(t *testing.T) {
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.1}, nil
	}}
	h := newTestHandler(t, nil, interp, nil, nil, nil)

	rec := sendWebhook(t, h, "wrong-secret", makeMessageBody(t, 10, 100, "Привет"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if interp.CallCount() != 0 {
		t.Fatalf("interpreter should not be called, got %d", interp.CallCount())
	}
}

func TestHandlerMessageHydratesProviderAndProcessesWithoutLoginPrompt(t *testing.T) {
	store := newInMemorySessionStore()
	provider := &mockProvider{
		getProviderInfoFn: func(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
			return &domain.ProviderInfo{ProviderID: "spec-1", Timezone: "Europe/Moscow"}, nil
		},
	}
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
	}}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 11, 111, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.sendTextCalls) != 0 {
		t.Fatalf("did not expect login prompt text, got %#v", tg.sendTextCalls)
	}
	if interp.CallCount() != 1 {
		t.Fatalf("expected interpreter call after hydration, got %d", interp.CallCount())
	}
	saved, err := store.Get(context.Background(), 11)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.TelegramUserID != 111 {
		t.Fatalf("expected telegram_user_id=111, got %d", saved.TelegramUserID)
	}
	if saved.ProviderID != "spec-1" {
		t.Fatalf("expected provider id spec-1, got %q", saved.ProviderID)
	}
}

func TestHandlerMessageNonSpecialistShowsForbidden(t *testing.T) {
	store := newInMemorySessionStore()
	provider := &mockProvider{
		getProviderInfoFn: func(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
			return nil, domain.ErrForbidden
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 12, 222, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.sendTextCalls) == 0 || !strings.Contains(strings.ToLower(tg.sendTextCalls[0].Text), "нет доступа") {
		t.Fatalf("expected forbidden message, got %#v", tg.sendTextCalls)
	}
}

func TestHandlerSerializesProcessingPerChat(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 13, TelegramUserID: 333, ProviderID: "spec-1", Timezone: "UTC"})

	var inFlight int32
	var maxInFlight int32
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
	}}
	h := newTestHandler(t, store, interp, nil, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sendWebhook(t, h, "secret", makeMessageBody(t, 13, 333, "Тест"))
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&maxInFlight) != 1 {
		t.Fatalf("expected max concurrent calls 1, got %d", maxInFlight)
	}
}

func TestHandlerWriteIntentBuildsPreviewAndPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 14, TelegramUserID: 444, ProviderID: "spec-1", Timezone: "UTC"})

	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{
			Intent:     interpreter.IntentSetWorkingHours,
			Confidence: 0.95,
			Params: interpreter.ActionParams{
				DateRange:    &interpreter.DateRange{From: "2026-03-24", To: "2026-03-27"},
				WorkingHours: &interpreter.TimeRange{From: "12:00", To: "20:00"},
			},
		}, nil
	}}
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

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 14, 444, "На следующей неделе работаю с 12 до 20"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	saved, err := store.Get(context.Background(), 14)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("expected pending plan")
	}
}

func TestHandlerWriteIntentLargeImpactWarnsButKeepsConfirm(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 1401, TelegramUserID: 4441, ProviderID: "spec-1", Timezone: "UTC"})

	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{
			Intent:     interpreter.IntentSetWorkingHours,
			Confidence: 0.95,
			Params: interpreter.ActionParams{
				DateRange:    &interpreter.DateRange{From: "2026-03-24", To: "2026-03-27"},
				WorkingHours: &interpreter.TimeRange{From: "10:00", To: "19:00"},
			},
		}, nil
	}}
	provider := &mockProvider{
		previewAvailabilityFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
			return &domain.Preview{
				Summary: "Пн–Пт, 10:00–19:00",
				AvailabilityChange: domain.AvailabilityChange{
					AddedSlots:   22,
					RemovedSlots: 4,
				},
				RiskLevel: domain.RiskMedium,
			}, nil
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 1401, 4441, "Сделай график с 10 до 19"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.sendTextCalls) != 0 {
		t.Fatalf("expected no forced deep link text, got %#v", tg.sendTextCalls)
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected one preview finalize, got %d", len(tg.finalizeCalls))
	}
	call := tg.finalizeCalls[0]
	if call.Keyboard == nil || len(call.Keyboard.InlineKeyboard) == 0 {
		t.Fatalf("expected keyboard in preview: %#v", call.Keyboard)
	}
	if !strings.Contains(call.Text, "Рекомендация") {
		t.Fatalf("expected recommendation warning in preview text, got %q", call.Text)
	}
}

func TestHandlerReadOnlyListBookingsCallsProviderWithoutACP(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 15, TelegramUserID: 555, ProviderID: "spec-1", Timezone: "UTC"})

	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		return &interpreter.ActionPlan{
			Intent:     interpreter.IntentListBookings,
			Confidence: 0.9,
			Params: interpreter.ActionParams{
				DateRange: &interpreter.DateRange{From: "2026-03-22", To: "2026-03-22"},
			},
		}, nil
	}}
	provider := &mockProvider{
		getBookingsFn: func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
			return []domain.Booking{{ClientName: "Алина", ServiceName: "Массаж", At: time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)}}, nil
		},
	}
	executor := &mockExecutor{}
	h := newTestHandler(t, store, interp, provider, executor, newMockTelegramGateway())

	rec := sendWebhook(t, h, "secret", makeMessageBody(t, 15, 555, "Покажи записи"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if provider.GetBookingsCallCount() != 1 {
		t.Fatalf("expected GetBookings called once, got %d", provider.GetBookingsCallCount())
	}
	if executor.CallCount() != 0 {
		t.Fatalf("expected no ACP calls, got %d", executor.CallCount())
	}
}

func TestHandlerCallbackConfirmSuccessClearsPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{
		ChatID:         16,
		TelegramUserID: 666,
		ProviderID:     "spec-1",
		Timezone:       "UTC",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-1",
			PreviewMsgID: 55,
			CreatedAt:    time.Now().UTC(),
			Plan: interpreter.ActionPlan{
				Intent: interpreter.IntentCancelBooking,
				Params: interpreter.ActionParams{BookingID: "b-1"},
			},
		},
	})

	executor := &mockExecutor{fn: func(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error) {
		return &ExecutionResult{Message: "ok"}, nil
	}}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, nil, executor, tg)

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 16, 666, "cb-1", ConfirmData("plan-1"), 55))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	saved, err := store.Get(context.Background(), 16)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared")
	}
}

func TestHandlerCallbackCancelClearsPendingPlan(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{
		ChatID:         17,
		TelegramUserID: 777,
		ProviderID:     "spec-1",
		Timezone:       "UTC",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-2",
			PreviewMsgID: 77,
			CreatedAt:    time.Now().UTC(),
			Plan:         interpreter.ActionPlan{Intent: interpreter.IntentSetWorkingHours},
		},
	})

	executor := &mockExecutor{}
	h := newTestHandler(t, store, nil, nil, executor, newMockTelegramGateway())
	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 17, 777, "cb-2", CancelData("plan-2"), 77))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if executor.CallCount() != 0 {
		t.Fatalf("expected no executor call, got %d", executor.CallCount())
	}
	saved, err := store.Get(context.Background(), 17)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared")
	}
}

func TestHandlerCallbackCancelIgnoresMessageNotModifiedError(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{
		ChatID:         1701,
		TelegramUserID: 7771,
		ProviderID:     "spec-1",
		Timezone:       "UTC",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-2",
			PreviewMsgID: 77,
			CreatedAt:    time.Now().UTC(),
			Plan:         interpreter.ActionPlan{Intent: interpreter.IntentSetWorkingHours},
		},
	})

	tg := newMockTelegramGateway()
	tg.editMarkupErr = errors.New("bot gateway: editMessageReplyMarkup rejected: Bad Request: message is not modified")
	h := newTestHandler(t, store, nil, nil, &mockExecutor{}, tg)

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 1701, 7771, "cb-2", CancelData("plan-2"), 77))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	saved, err := store.Get(context.Background(), 1701)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared")
	}
}

func TestHandlerCallbackSlotSelectionUsesPendingSnapshot(t *testing.T) {
	store := newInMemorySessionStore()
	now := time.Now().UTC().Truncate(time.Second)
	seedSession(t, store, &session.Session{
		ChatID:         18,
		TelegramUserID: 888,
		ProviderID:     "spec-1",
		Timezone:       "UTC",
		PendingPlan: &session.PendingPlan{
			ID:           "plan-slot",
			PreviewMsgID: 99,
			CreatedAt:    now,
			Plan: interpreter.ActionPlan{
				Intent: interpreter.IntentCreateBooking,
				Params: interpreter.ActionParams{ServiceID: "svc-1"},
			},
			SlotCandidates: []session.PendingSlotCandidate{{
				ID:        "slot-1",
				ServiceID: "svc-1",
				StartAt:   now.Add(30 * time.Minute).Format(time.RFC3339),
				EndAt:     now.Add(90 * time.Minute).Format(time.RFC3339),
			}},
		},
	})

	provider := &mockProvider{
		findSlotsFn: func(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error) {
			return nil, errors.New("must not call provider.FindSlots during slot selection")
		},
	}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, nil, provider, nil, tg)

	rec := sendWebhook(t, h, "secret", makeCallbackBody(t, 18, 888, "cb-slot", SlotData(0, "plan-slot"), 99))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if provider.FindSlotsCallCount() != 0 {
		t.Fatalf("expected zero provider.FindSlots calls, got %d", provider.FindSlotsCallCount())
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
		t.Fatalf("unexpected action: %q", parsed.Query().Get("action"))
	}
	if parsed.Query().Get("context") != "range > 7 days" {
		t.Fatalf("unexpected context: %q", parsed.Query().Get("context"))
	}
}

func TestHandlerServeHTTPFastAckOnSlowInterpreter(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 19, TelegramUserID: 999, ProviderID: "spec-1", Timezone: "UTC"})
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		time.Sleep(5 * time.Second)
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
	}}
	h := newTestHandler(t, store, interp, nil, nil, newMockTelegramGateway())

	start := time.Now()
	rec := sendWebhookNoWait(t, h, "secret", makeMessageBodyWithUpdateID(t, 9001, 19, 999, "Привет"))
	ackMS := time.Since(start).Milliseconds()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ackMS > 300 {
		t.Fatalf("expected webhook ack <=300ms, got %dms", ackMS)
	}
	if !h.WaitForIdle(7 * time.Second) {
		t.Fatal("expected background processing to complete")
	}
}

func TestHandlerDeduplicatesRetryByUpdateID(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 20, TelegramUserID: 1000, ProviderID: "spec-1", Timezone: "UTC"})
	var calls int32
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		atomic.AddInt32(&calls, 1)
		return nil, context.DeadlineExceeded
	}}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, nil, nil, tg)

	body := makeMessageBodyWithUpdateID(t, 9002, 20, 1000, "Покажи записи")
	for i := 0; i < 3; i++ {
		rec := sendWebhook(t, h, "secret", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected interpreter called once for duplicate updates, got %d", got)
	}
	if len(tg.sendTextCalls) != 1 {
		t.Fatalf("expected single timeout message, got %d", len(tg.sendTextCalls))
	}
}

func TestHandlerDeduplicatesUpdateEvenWhenHandlingFails(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 201, TelegramUserID: 1002, ProviderID: "spec-1", Timezone: "UTC"})

	var calls int32
	interp := &mockInterpreter{fn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
		atomic.AddInt32(&calls, 1)
		return nil, context.DeadlineExceeded
	}}

	baseTG := newMockTelegramGateway()
	tg := &failingTelegramGateway{
		mockTelegramGateway: baseTG,
		sendTextErr:         errors.New("telegram send failure"),
	}
	h, err := NewHandler(HandlerConfig{
		WebhookSecret: "secret",
		WebhookURL:    "https://example.test/webhook",
		MiniAppURL:    "https://mini.app/open",
		PlanTTL:       15 * time.Minute,
	}, store, interp, &mockProvider{}, &mockExecutor{}, tg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := makeMessageBodyWithUpdateID(t, 9004, 201, 1002, "Покажи записи")
	for i := 0; i < 3; i++ {
		rec := sendWebhook(t, h, "secret", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected interpreter called once for duplicate failed updates, got %d", got)
	}
}

func TestHandlerProgressiveDraftOnLongInterpret(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 21, TelegramUserID: 1001, ProviderID: "spec-1", Timezone: "UTC"})
	interp := &mockInterpreter{streamFn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext, onProgress func(interpreter.Progress)) (*interpreter.ActionPlan, error) {
		started := time.Now()
		for i := 0; i < 4; i++ {
			onProgress(interpreter.Progress{
				ChunkCount: i + 1,
				Bytes:      int64((i + 1) * 16),
				StartedAt:  started,
				UpdatedAt:  time.Now(),
			})
			time.Sleep(1900 * time.Millisecond)
		}
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
	}}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, nil, nil, tg)

	rec := sendWebhookNoWait(t, h, "secret", makeMessageBodyWithUpdateID(t, 9003, 21, 1001, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !h.WaitForIdle(10 * time.Second) {
		t.Fatal("timeout waiting for background processing")
	}
	if len(tg.draftCalls) < 3 {
		t.Fatalf("expected multiple draft calls for long interpret, got %d", len(tg.draftCalls))
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected single finalize call, got %d", len(tg.finalizeCalls))
	}
}

func TestHandlerHeartbeatDraftOnSlowInterpretWithoutChunks(t *testing.T) {
	store := newInMemorySessionStore()
	seedSession(t, store, &session.Session{ChatID: 2102, TelegramUserID: 1003, ProviderID: "spec-1", Timezone: "UTC"})
	interp := &mockInterpreter{streamFn: func(ctx context.Context, userMessage string, convo interpreter.ConversationContext, onProgress func(interpreter.Progress)) (*interpreter.ActionPlan, error) {
		time.Sleep(5 * time.Second)
		return &interpreter.ActionPlan{Intent: interpreter.IntentUnknown, Confidence: 0.2}, nil
	}}
	tg := newMockTelegramGateway()
	h := newTestHandler(t, store, interp, nil, nil, tg)

	rec := sendWebhookNoWait(t, h, "secret", makeMessageBodyWithUpdateID(t, 9005, 2102, 1003, "Привет"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !h.WaitForIdle(8 * time.Second) {
		t.Fatal("timeout waiting for background processing")
	}
	if len(tg.draftCalls) < 2 {
		t.Fatalf("expected heartbeat drafts, got %d", len(tg.draftCalls))
	}
}

func TestFormatErrorTimeoutContainsExamples(t *testing.T) {
	got := FormatError("timeout")
	if !strings.Contains(got, "Примеры") {
		t.Fatalf("expected timeout fallback with examples, got: %q", got)
	}
	if !strings.Contains(got, "Покажи записи на завтра") {
		t.Fatalf("expected example commands in timeout fallback, got: %q", got)
	}
}
