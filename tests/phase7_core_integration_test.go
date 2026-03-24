//go:build integration

package tests

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
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/chillmeal/bookably-agent/internal/acp"
	"github.com/chillmeal/bookably-agent/internal/bot"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
	"github.com/redis/go-redis/v9"
)

type scriptedInterpreter struct {
	mu    sync.Mutex
	queue []scriptedInterpretResponse
	calls int
}

type scriptedInterpretResponse struct {
	plan *interpreter.ActionPlan
	err  error
}

func (s *scriptedInterpreter) Interpret(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.queue) == 0 {
		return nil, errors.New("scripted interpreter: no response configured")
	}
	out := s.queue[0]
	s.queue = s.queue[1:]
	return out.plan, out.err
}

type providerMock struct {
	mu sync.Mutex

	getBookingsCalls          int
	previewBookingCancelCalls int
	findSlotsCalls            int
	previewBookingCreateCalls int
	previewAvailabilityCalls  int

	getBookingsFn         func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error)
	findSlotsFn           func(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error)
	previewAvailabilityFn func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewCreateFn       func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
	previewCancelFn       func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error)
}

func (m *providerMock) GetBookings(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
	m.mu.Lock()
	m.getBookingsCalls++
	fn := m.getBookingsFn
	m.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, providerID, f)
}

func (m *providerMock) FindSlots(ctx context.Context, providerID string, req domain.SlotSearchRequest) ([]domain.Slot, error) {
	m.mu.Lock()
	m.findSlotsCalls++
	fn := m.findSlotsFn
	m.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, providerID, req)
}

func (m *providerMock) GetProviderInfo(ctx context.Context, providerID string) (*domain.ProviderInfo, error) {
	return &domain.ProviderInfo{
		ProviderID: providerID,
		Timezone:   "UTC",
	}, nil
}

func (m *providerMock) PreviewAvailabilityChange(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewAvailabilityCalls++
	fn := m.previewAvailabilityFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

func (m *providerMock) PreviewBookingCreate(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewBookingCreateCalls++
	fn := m.previewCreateFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

func (m *providerMock) PreviewBookingCancel(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
	m.mu.Lock()
	m.previewBookingCancelCalls++
	fn := m.previewCancelFn
	m.mu.Unlock()
	if fn == nil {
		return &domain.Preview{}, nil
	}
	return fn(ctx, providerID, p)
}

type runSubmitterMock struct {
	mu    sync.Mutex
	calls int
	runs  []acp.ACPRun
	fn    func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error)
}

func (m *runSubmitterMock) SubmitAndWait(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
	m.mu.Lock()
	m.calls++
	m.runs = append(m.runs, run)
	fn := m.fn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, run)
	}
	return &acp.ACPRunResult{RunID: "run-1", Status: acp.ACPStatusCompleted}, nil
}

type telegramMessageCall struct {
	ChatID   int64
	Text     string
	Keyboard *bot.InlineKeyboardMarkup
}

type telegramGatewayMock struct {
	mu sync.Mutex

	nextMessageID int64

	sendChatActionCalls int
	answerCalls         int
	editCalls           int

	draftCalls    []telegramMessageCall
	finalizeCalls []telegramMessageCall
	sendTextCalls []telegramMessageCall
}

func newTelegramGatewayMock() *telegramGatewayMock {
	return &telegramGatewayMock{nextMessageID: 100}
}

func (m *telegramGatewayMock) SendChatAction(ctx context.Context, chatID int64, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendChatActionCalls++
	return nil
}

func (m *telegramGatewayMock) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answerCalls++
	return nil
}

func (m *telegramGatewayMock) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int64, markup *bot.InlineKeyboardMarkup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editCalls++
	return nil
}

func (m *telegramGatewayMock) SendText(ctx context.Context, chatID int64, text string, keyboard *bot.InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendTextCalls = append(m.sendTextCalls, telegramMessageCall{
		ChatID:   chatID,
		Text:     text,
		Keyboard: cloneKeyboard(keyboard),
	})
	m.nextMessageID++
	return m.nextMessageID, nil
}

func (m *telegramGatewayMock) Draft(ctx context.Context, chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draftCalls = append(m.draftCalls, telegramMessageCall{
		ChatID: chatID,
		Text:   text,
	})
	return nil
}

func (m *telegramGatewayMock) Finalize(ctx context.Context, chatID int64, text string, keyboard *bot.InlineKeyboardMarkup) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeCalls = append(m.finalizeCalls, telegramMessageCall{
		ChatID:   chatID,
		Text:     text,
		Keyboard: cloneKeyboard(keyboard),
	})
	m.nextMessageID++
	return m.nextMessageID, nil
}

func (m *telegramGatewayMock) SetWebhook(ctx context.Context, webhookURL, secret string, allowedUpdates []string) error {
	return nil
}

func cloneKeyboard(in *bot.InlineKeyboardMarkup) *bot.InlineKeyboardMarkup {
	if in == nil {
		return nil
	}
	payload, _ := json.Marshal(in)
	var out bot.InlineKeyboardMarkup
	_ = json.Unmarshal(payload, &out)
	return &out
}

func newSessionStore(t *testing.T) (session.SessionStore, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := session.NewRedisStore(client, 24*time.Hour)
	return store, func() {
		_ = client.Close()
		mr.Close()
	}
}

func seedSession(t *testing.T, store session.SessionStore, s *session.Session) {
	t.Helper()
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func buildMessageUpdate(t *testing.T, chatID int64, text string) []byte {
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
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal message update: %v", err)
	}
	return body
}

func buildCallbackUpdate(t *testing.T, chatID int64, callbackID, data string, messageID int64) []byte {
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
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal callback update: %v", err)
	}
	return body
}

func sendWebhook(t *testing.T, handler *bot.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !handler.WaitForIdle(5 * time.Second) {
		t.Fatal("timeout waiting handler background worker")
	}
	return rec
}

func buildHandler(t *testing.T, store session.SessionStore, interp bot.InterpreterService, provider domain.Provider, executor bot.ACPExecutor, tg bot.TelegramGateway) *bot.Handler {
	t.Helper()
	h, err := bot.NewHandler(bot.HandlerConfig{
		WebhookSecret: "secret",
		WebhookURL:    "https://example.test/webhook",
		MiniAppURL:    "https://mini.app/open",
		PlanTTL:       15 * time.Minute,
	}, store, interp, provider, executor, tg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func newRuntimeExecutor(t *testing.T, runner *runSubmitterMock) *bot.RuntimeACPExecutor {
	t.Helper()
	executor, err := bot.NewRuntimeACPExecutor("https://bookably.test", "bot-service-key", runner)
	if err != nil {
		t.Fatalf("NewRuntimeACPExecutor: %v", err)
	}
	return executor
}

func TestP701ReadFlowListBookingsIntegration(t *testing.T) {
	store, cleanup := newSessionStore(t)
	defer cleanup()

	seedSession(t, store, &session.Session{
		ChatID:        1001,
		ProviderID:    "spec-1",
		Timezone:      "Europe/Moscow",
		DialogHistory: []session.Message{},
	})

	interp := &scriptedInterpreter{
		queue: []scriptedInterpretResponse{
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentListBookings,
					Confidence: 0.92,
					Params: interpreter.ActionParams{
						DateRange: &interpreter.DateRange{From: "2026-03-22", To: "2026-03-22"},
					},
				},
			},
		},
	}
	provider := &providerMock{
		getBookingsFn: func(ctx context.Context, providerID string, f domain.BookingFilter) ([]domain.Booking, error) {
			return []domain.Booking{
				{ID: "b1", ClientName: "Алина Смирнова", ServiceName: "Массаж 60 мин", At: time.Date(2026, 3, 22, 8, 0, 0, 0, time.UTC)},
				{ID: "b2", ClientName: "Марина", ServiceName: "Маникюр 90 мин", At: time.Date(2026, 3, 22, 10, 30, 0, 0, time.UTC)},
				{ID: "b3", ClientName: "Иван Петров", ServiceName: "Стрижка 30 мин", At: time.Date(2026, 3, 22, 13, 0, 0, 0, time.UTC)},
			}, nil
		},
	}
	runner := &runSubmitterMock{}
	tg := newTelegramGatewayMock()
	h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

	rec := sendWebhook(t, h, buildMessageUpdate(t, 1001, "Покажи мои записи на завтра"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(tg.draftCalls) != 1 {
		t.Fatalf("expected 1 draft call, got %d", len(tg.draftCalls))
	}
	if len(tg.finalizeCalls) != 1 {
		t.Fatalf("expected 1 finalize call, got %d", len(tg.finalizeCalls))
	}
	out := tg.finalizeCalls[0].Text
	if !strings.Contains(out, "Алина Смирнова") || !strings.Contains(out, "Марина") || !strings.Contains(out, "Иван Петров") {
		t.Fatalf("final output missing expected names: %q", out)
	}
	if !strings.Contains(out, "11:00") || !strings.Contains(out, "13:30") || !strings.Contains(out, "16:00") {
		t.Fatalf("final output missing expected local times: %q", out)
	}
	if runner.calls != 0 {
		t.Fatalf("expected no ACP run calls, got %d", runner.calls)
	}

	saved, err := store.Get(context.Background(), 1001)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if len(saved.DialogHistory) == 0 {
		t.Fatal("expected dialog history to be updated")
	}
}

func TestP702WriteCancelHappyPathIntegration(t *testing.T) {
	store, cleanup := newSessionStore(t)
	defer cleanup()

	seedSession(t, store, &session.Session{
		ChatID:        1002,
		ProviderID:    "spec-1",
		Timezone:      "Europe/Moscow",
		DialogHistory: []session.Message{},
	})

	interp := &scriptedInterpreter{
		queue: []scriptedInterpretResponse{
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentCancelBooking,
					Confidence: 0.95,
					Params: interpreter.ActionParams{
						ClientName: "Иван",
					},
					RawUserMessage: "Отмени запись Ивана в четверг",
				},
			},
		},
	}
	provider := &providerMock{
		previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
			return &domain.Preview{
				Summary: "Booking selected for cancellation",
				BookingResult: &domain.Booking{
					ID:          "booking-42",
					ClientName:  "Иван Петров",
					ServiceName: "Стрижка 30 мин",
					At:          time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
				},
				RiskLevel: domain.RiskHigh,
			}, nil
		},
	}
	runner := &runSubmitterMock{}
	tg := newTelegramGatewayMock()
	h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

	rec := sendWebhook(t, h, buildMessageUpdate(t, 1002, "Отмени запись Ивана в четверг"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	saved, err := store.Get(context.Background(), 1002)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("expected pending plan after preview")
	}
	if saved.PendingPlan.Plan.Params.BookingID != "booking-42" {
		t.Fatalf("expected booking_id to be persisted, got %q", saved.PendingPlan.Plan.Params.BookingID)
	}

	callback := buildCallbackUpdate(t, 1002, "cb-1", bot.ConfirmData(saved.PendingPlan.ID), saved.PendingPlan.PreviewMsgID)
	rec = sendWebhook(t, h, callback)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm callback expected 200, got %d", rec.Code)
	}
	if runner.calls != 1 {
		t.Fatalf("expected 1 ACP run call, got %d", runner.calls)
	}

	run := runner.runs[0]
	if len(run.Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(run.Steps))
	}
	step := run.Steps[0]
	if step.Config.Method != "POST" {
		t.Fatalf("expected POST method, got %q", step.Config.Method)
	}
	if !strings.Contains(step.Config.URL, "/api/v1/specialist/bookings/booking-42/cancel") {
		t.Fatalf("unexpected cancel URL: %q", step.Config.URL)
	}
	if run.Metadata["intent"] != "cancel_booking" || run.Metadata["risk_level"] != "high" {
		t.Fatalf("unexpected run metadata: %#v", run.Metadata)
	}

	saved, err = store.Get(context.Background(), 1002)
	if err != nil {
		t.Fatalf("store.Get after confirm: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("expected pending plan to be cleared")
	}
}

func TestP703SessionRecoveryAfterRestartIntegration(t *testing.T) {
	store, cleanup := newSessionStore(t)
	defer cleanup()

	seedSession(t, store, &session.Session{
		ChatID:        1003,
		ProviderID:    "spec-1",
		Timezone:      "Europe/Moscow",
		DialogHistory: []session.Message{},
	})

	interp := &scriptedInterpreter{
		queue: []scriptedInterpretResponse{
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentCancelBooking,
					Confidence: 0.95,
					Params: interpreter.ActionParams{
						ClientName: "Иван",
					},
				},
			},
		},
	}
	provider := &providerMock{
		previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
			return &domain.Preview{
				BookingResult: &domain.Booking{
					ID:          "booking-restart-1",
					ClientName:  "Иван Петров",
					ServiceName: "Стрижка",
					At:          time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
				},
				RiskLevel: domain.RiskHigh,
			}, nil
		},
	}
	runner := &runSubmitterMock{}

	tg1 := newTelegramGatewayMock()
	h1 := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg1)
	rec := sendWebhook(t, h1, buildMessageUpdate(t, 1003, "Отмени запись Ивана"))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview request expected 200, got %d", rec.Code)
	}

	beforeRestart, err := store.Get(context.Background(), 1003)
	if err != nil {
		t.Fatalf("store.Get before restart: %v", err)
	}
	if beforeRestart.PendingPlan == nil {
		t.Fatal("expected pending plan before restart")
	}
	expectedIDKey := beforeRestart.PendingPlan.IdempotencyKey
	planID := beforeRestart.PendingPlan.ID
	msgID := beforeRestart.PendingPlan.PreviewMsgID

	tg2 := newTelegramGatewayMock()
	h2 := buildHandler(t, store, &scriptedInterpreter{}, provider, newRuntimeExecutor(t, runner), tg2)
	rec = sendWebhook(t, h2, buildCallbackUpdate(t, 1003, "cb-restart", bot.ConfirmData(planID), msgID))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm after restart expected 200, got %d", rec.Code)
	}
	if runner.calls != 1 {
		t.Fatalf("expected one ACP run after restart, got %d", runner.calls)
	}
	if runner.runs[0].IdempotencyKey != expectedIDKey {
		t.Fatalf("idempotency key changed after restart: got %q want %q", runner.runs[0].IdempotencyKey, expectedIDKey)
	}
}

func TestP704ClarificationLoopEscalatesDeepLinkIntegration(t *testing.T) {
	store, cleanup := newSessionStore(t)
	defer cleanup()

	seedSession(t, store, &session.Session{
		ChatID:        1004,
		ProviderID:    "spec-1",
		Timezone:      "Europe/Moscow",
		DialogHistory: []session.Message{},
	})

	interp := &scriptedInterpreter{
		queue: []scriptedInterpretResponse{
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentCreateBooking,
					Confidence: 0.7,
					Clarifications: []interpreter.Clarification{
						{Field: "service_name", Question: "Какую услугу записать?"},
					},
				},
			},
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentCreateBooking,
					Confidence: 0.7,
					Clarifications: []interpreter.Clarification{
						{Field: "time", Question: "На какое время записать?"},
					},
				},
			},
		},
	}
	provider := &providerMock{}
	runner := &runSubmitterMock{}
	tg := newTelegramGatewayMock()
	h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

	rec := sendWebhook(t, h, buildMessageUpdate(t, 1004, "Запиши Марину"))
	if rec.Code != http.StatusOK {
		t.Fatalf("first message expected 200, got %d", rec.Code)
	}
	rec = sendWebhook(t, h, buildMessageUpdate(t, 1004, "Маникюр"))
	if rec.Code != http.StatusOK {
		t.Fatalf("second message expected 200, got %d", rec.Code)
	}

	if provider.previewAvailabilityCalls != 0 || provider.previewBookingCancelCalls != 0 || provider.previewBookingCreateCalls != 0 {
		t.Fatal("provider previews must not be called in clarification escalation flow")
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be called, got %d", runner.calls)
	}
	if len(tg.sendTextCalls) == 0 {
		t.Fatal("expected deep-link escalation message")
	}
	last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
	if last.Keyboard == nil || len(last.Keyboard.InlineKeyboard) == 0 || last.Keyboard.InlineKeyboard[0][0].WebApp == nil {
		t.Fatal("expected web_app deep-link button on escalation")
	}
	link := last.Keyboard.InlineKeyboard[0][0].WebApp.URL
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse deep link: %v", err)
	}
	if u.Query().Get("action") != "clarification_limit" {
		t.Fatalf("unexpected deep-link action: %q", u.Query().Get("action"))
	}
}

func TestP705TransientFailureAndRetryIntegration(t *testing.T) {
	store, cleanup := newSessionStore(t)
	defer cleanup()

	seedSession(t, store, &session.Session{
		ChatID:        1005,
		ProviderID:    "spec-1",
		Timezone:      "Europe/Moscow",
		DialogHistory: []session.Message{},
	})

	interp := &scriptedInterpreter{
		queue: []scriptedInterpretResponse{
			{
				plan: &interpreter.ActionPlan{
					Intent:     interpreter.IntentCancelBooking,
					Confidence: 0.9,
					Params:     interpreter.ActionParams{ClientName: "Иван"},
				},
			},
		},
	}
	provider := &providerMock{
		previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
			return &domain.Preview{
				BookingResult: &domain.Booking{
					ID:          "booking-99",
					ClientName:  "Иван",
					ServiceName: "Стрижка",
					At:          time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
				},
				RiskLevel: domain.RiskHigh,
			}, nil
		},
	}
	runner := &runSubmitterMock{}
	var runCalls int
	runner.fn = func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
		runCalls++
		if runCalls == 1 {
			return nil, errors.Join(acp.ErrACPTransient, errors.New("temporary upstream error"))
		}
		return &acp.ACPRunResult{RunID: "run-ok", Status: acp.ACPStatusCompleted}, nil
	}
	tg := newTelegramGatewayMock()
	h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

	rec := sendWebhook(t, h, buildMessageUpdate(t, 1005, "Отмени запись Ивана"))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview request expected 200, got %d", rec.Code)
	}
	saved, err := store.Get(context.Background(), 1005)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("expected pending plan")
	}
	planID := saved.PendingPlan.ID
	previewMsgID := saved.PendingPlan.PreviewMsgID

	rec = sendWebhook(t, h, buildCallbackUpdate(t, 1005, "cb-1", bot.ConfirmData(planID), previewMsgID))
	if rec.Code != http.StatusOK {
		t.Fatalf("first confirm expected 200, got %d", rec.Code)
	}
	if runner.calls != 1 {
		t.Fatalf("expected 1 run on first confirm, got %d", runner.calls)
	}
	saved, err = store.Get(context.Background(), 1005)
	if err != nil {
		t.Fatalf("store.Get after first confirm: %v", err)
	}
	if saved.PendingPlan == nil {
		t.Fatal("pending plan must remain for retry")
	}
	if len(tg.sendTextCalls) == 0 || tg.sendTextCalls[len(tg.sendTextCalls)-1].Keyboard == nil {
		t.Fatal("expected retry message with keyboard on transient failure")
	}

	rec = sendWebhook(t, h, buildCallbackUpdate(t, 1005, "cb-2", bot.ConfirmData(planID), previewMsgID))
	if rec.Code != http.StatusOK {
		t.Fatalf("second confirm expected 200, got %d", rec.Code)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 run calls after retry, got %d", runner.calls)
	}
	if runner.runs[0].IdempotencyKey != runner.runs[1].IdempotencyKey {
		t.Fatalf("idempotency key changed between retries: %q vs %q", runner.runs[0].IdempotencyKey, runner.runs[1].IdempotencyKey)
	}
	saved, err = store.Get(context.Background(), 1005)
	if err != nil {
		t.Fatalf("store.Get after retry: %v", err)
	}
	if saved.PendingPlan != nil {
		t.Fatal("pending plan must be cleared after successful retry")
	}
}

func TestP706ErrorPathIntegration(t *testing.T) {
	t.Run("llm timeout returns retry-context error", func(t *testing.T) {
		store, cleanup := newSessionStore(t)
		defer cleanup()

		seedSession(t, store, &session.Session{
			ChatID:        1101,
			ProviderID:    "spec-1",
			Timezone:      "Europe/Moscow",
			DialogHistory: []session.Message{},
		})

		interp := &scriptedInterpreter{
			queue: []scriptedInterpretResponse{
				{err: context.DeadlineExceeded},
			},
		}
		provider := &providerMock{}
		runner := &runSubmitterMock{}
		tg := newTelegramGatewayMock()
		h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

		rec := sendWebhook(t, h, buildMessageUpdate(t, 1101, "test"))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "слишком много времени") {
			t.Fatalf("expected timeout user message, got %#v", tg.sendTextCalls)
		}
	})

	t.Run("bookably 404 and 409 preview mapping", func(t *testing.T) {
		store, cleanup := newSessionStore(t)
		defer cleanup()
		seedSession(t, store, &session.Session{
			ChatID:        1102,
			ProviderID:    "spec-1",
			Timezone:      "Europe/Moscow",
			DialogHistory: []session.Message{},
		})

		interp := &scriptedInterpreter{
			queue: []scriptedInterpretResponse{
				{
					plan: &interpreter.ActionPlan{
						Intent:     interpreter.IntentCancelBooking,
						Confidence: 0.9,
						Params:     interpreter.ActionParams{ClientName: "X"},
					},
				},
				{
					plan: &interpreter.ActionPlan{
						Intent:     interpreter.IntentCancelBooking,
						Confidence: 0.9,
						Params:     interpreter.ActionParams{ClientName: "Y"},
					},
				},
			},
		}
		provider := &providerMock{
			previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
				if p.ClientName == "X" {
					return nil, errors.Join(domain.ErrNotFound, errors.New("missing booking"))
				}
				return nil, errors.Join(domain.ErrConflict, errors.New("conflict"))
			},
		}
		runner := &runSubmitterMock{}
		tg := newTelegramGatewayMock()
		h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

		rec := sendWebhook(t, h, buildMessageUpdate(t, 1102, "first"))
		if rec.Code != http.StatusOK {
			t.Fatalf("first preview expected 200, got %d", rec.Code)
		}
		if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "Ничего не найдено") {
			t.Fatalf("expected not_found user text, got %#v", tg.sendTextCalls)
		}

		rec = sendWebhook(t, h, buildMessageUpdate(t, 1102, "second"))
		if rec.Code != http.StatusOK {
			t.Fatalf("second preview expected 200, got %d", rec.Code)
		}
		if len(tg.sendTextCalls) == 0 || !strings.Contains(strings.ToLower(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text), "конфликт") {
			t.Fatalf("expected conflict user text, got %q", tg.sendTextCalls[len(tg.sendTextCalls)-1].Text)
		}
	})

	t.Run("acp policy and upstream paths", func(t *testing.T) {
		makeScenario := func(runnerFn func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error)) (*bot.Handler, session.SessionStore, *telegramGatewayMock, *runSubmitterMock) {
			store, cleanup := newSessionStore(t)
			t.Cleanup(cleanup)

			seedSession(t, store, &session.Session{
				ChatID:        1103,
				ProviderID:    "spec-1",
				Timezone:      "Europe/Moscow",
				DialogHistory: []session.Message{},
			})

			interp := &scriptedInterpreter{
				queue: []scriptedInterpretResponse{
					{
						plan: &interpreter.ActionPlan{
							Intent:     interpreter.IntentCancelBooking,
							Confidence: 0.9,
							Params:     interpreter.ActionParams{ClientName: "Иван"},
						},
					},
				},
			}
			provider := &providerMock{
				previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
					return &domain.Preview{
						BookingResult: &domain.Booking{
							ID:          "booking-err-1",
							ClientName:  "Иван",
							ServiceName: "Стрижка",
							At:          time.Now().UTC(),
						},
						RiskLevel: domain.RiskHigh,
					}, nil
				},
			}

			runner := &runSubmitterMock{fn: runnerFn}
			tg := newTelegramGatewayMock()
			h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

			rec := sendWebhook(t, h, buildMessageUpdate(t, 1103, "Отмени запись"))
			if rec.Code != http.StatusOK {
				t.Fatalf("preview expected 200, got %d", rec.Code)
			}
			return h, store, tg, runner
		}

		t.Run("policy no retry button", func(t *testing.T) {
			h, store, tg, _ := makeScenario(func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
				return nil, errors.Join(acp.ErrACPPolicyViolation, errors.New("policy deny"))
			})
			saved, err := store.Get(context.Background(), 1103)
			if err != nil {
				t.Fatalf("store.Get: %v", err)
			}
			rec := sendWebhook(t, h, buildCallbackUpdate(t, 1103, "cb-policy", bot.ConfirmData(saved.PendingPlan.ID), saved.PendingPlan.PreviewMsgID))
			if rec.Code != http.StatusOK {
				t.Fatalf("policy confirm expected 200, got %d", rec.Code)
			}
			last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
			if !strings.Contains(last.Text, "policy violation") {
				t.Fatalf("expected policy reason text, got %q", last.Text)
			}
			if last.Keyboard != nil {
				t.Fatalf("policy error must not include retry keyboard: %#v", last.Keyboard)
			}
		})

		t.Run("upstream retry button", func(t *testing.T) {
			h, store, tg, _ := makeScenario(func(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error) {
				return nil, errors.Join(domain.ErrUpstream, errors.New("bookably 500"))
			})
			saved, err := store.Get(context.Background(), 1103)
			if err != nil {
				t.Fatalf("store.Get: %v", err)
			}
			rec := sendWebhook(t, h, buildCallbackUpdate(t, 1103, "cb-upstream", bot.ConfirmData(saved.PendingPlan.ID), saved.PendingPlan.PreviewMsgID))
			if rec.Code != http.StatusOK {
				t.Fatalf("upstream confirm expected 200, got %d", rec.Code)
			}
			last := tg.sendTextCalls[len(tg.sendTextCalls)-1]
			if last.Keyboard == nil {
				t.Fatal("upstream error must include retry keyboard")
			}
		})
	})

	t.Run("expired plan confirm rejected", func(t *testing.T) {
		store, cleanup := newSessionStore(t)
		defer cleanup()

		seedSession(t, store, &session.Session{
			ChatID:     1104,
			ProviderID: "spec-1",
			Timezone:   "UTC",
			PendingPlan: &session.PendingPlan{
				ID:           "plan-expired",
				PreviewMsgID: 123,
				CreatedAt:    time.Now().UTC().Add(-20 * time.Minute),
				Plan: interpreter.ActionPlan{
					Intent: interpreter.IntentCancelBooking,
					Params: interpreter.ActionParams{BookingID: "booking-old"},
				},
			},
			DialogHistory: []session.Message{},
		})

		interp := &scriptedInterpreter{}
		provider := &providerMock{}
		runner := &runSubmitterMock{}
		tg := newTelegramGatewayMock()
		h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

		rec := sendWebhook(t, h, buildCallbackUpdate(t, 1104, "cb-expired", bot.ConfirmData("plan-expired"), 123))
		if rec.Code != http.StatusOK {
			t.Fatalf("expired confirm expected 200, got %d", rec.Code)
		}
		if runner.calls != 0 {
			t.Fatalf("runner must not be called for expired plan, got %d", runner.calls)
		}
		if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "Запрос устарел") {
			t.Fatalf("expected stale-plan text, got %#v", tg.sendTextCalls)
		}
	})

	t.Run("double confirm rejected", func(t *testing.T) {
		store, cleanup := newSessionStore(t)
		defer cleanup()

		seedSession(t, store, &session.Session{
			ChatID:        1105,
			ProviderID:    "spec-1",
			Timezone:      "UTC",
			DialogHistory: []session.Message{},
		})

		interp := &scriptedInterpreter{
			queue: []scriptedInterpretResponse{
				{
					plan: &interpreter.ActionPlan{
						Intent:     interpreter.IntentCancelBooking,
						Confidence: 0.9,
						Params:     interpreter.ActionParams{ClientName: "Иван"},
					},
				},
			},
		}
		provider := &providerMock{
			previewCancelFn: func(ctx context.Context, providerID string, p domain.ActionParams) (*domain.Preview, error) {
				return &domain.Preview{
					BookingResult: &domain.Booking{
						ID:          "booking-double",
						ClientName:  "Иван",
						ServiceName: "Стрижка",
						At:          time.Now().UTC(),
					},
					RiskLevel: domain.RiskHigh,
				}, nil
			},
		}
		runner := &runSubmitterMock{}
		tg := newTelegramGatewayMock()
		h := buildHandler(t, store, interp, provider, newRuntimeExecutor(t, runner), tg)

		rec := sendWebhook(t, h, buildMessageUpdate(t, 1105, "Отмени запись"))
		if rec.Code != http.StatusOK {
			t.Fatalf("preview expected 200, got %d", rec.Code)
		}
		saved, err := store.Get(context.Background(), 1105)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		planID := saved.PendingPlan.ID
		msgID := saved.PendingPlan.PreviewMsgID

		rec = sendWebhook(t, h, buildCallbackUpdate(t, 1105, "cb-first", bot.ConfirmData(planID), msgID))
		if rec.Code != http.StatusOK {
			t.Fatalf("first confirm expected 200, got %d", rec.Code)
		}
		rec = sendWebhook(t, h, buildCallbackUpdate(t, 1105, "cb-second", bot.ConfirmData(planID), msgID))
		if rec.Code != http.StatusOK {
			t.Fatalf("second confirm expected 200, got %d", rec.Code)
		}
		if runner.calls != 1 {
			t.Fatalf("runner must be called exactly once on double-confirm scenario, got %d", runner.calls)
		}
		if len(tg.sendTextCalls) == 0 || !strings.Contains(tg.sendTextCalls[len(tg.sendTextCalls)-1].Text, "Запрос устарел") {
			t.Fatalf("expected stale second-confirm text, got %#v", tg.sendTextCalls)
		}
	})
}
