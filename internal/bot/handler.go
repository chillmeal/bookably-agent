package bot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
	"github.com/chillmeal/bookably-agent/observability"
)

const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

var (
	ErrExecutionPolicyViolation = errors.New("bot handler: execution policy violation")
	ErrExecutionTransient       = errors.New("bot handler: execution transient")
	ErrExecutionContractBlocked = errors.New("bot handler: execution contract blocked")
)

type HandlerConfig struct {
	WebhookSecret string
	WebhookURL    string
	MiniAppURL    string
	PlanTTL       time.Duration
}

type TelegramGateway interface {
	SendChatAction(ctx context.Context, chatID int64, action string) error
	AnswerCallbackQuery(ctx context.Context, callbackID, text string) error
	EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int64, markup *InlineKeyboardMarkup) error
	SendText(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error)
	Draft(ctx context.Context, chatID int64, text string) error
	Finalize(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error)
	SetWebhook(ctx context.Context, webhookURL, secret string, allowedUpdates []string) error
}

type InterpreterService interface {
	Interpret(ctx context.Context, userMessage string, convo interpreter.ConversationContext) (*interpreter.ActionPlan, error)
}

type ExecutionResult struct {
	Message string
}

type ACPExecutor interface {
	ExecuteConfirmed(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error)
}

type Handler struct {
	store       session.SessionStore
	interpreter InterpreterService
	provider    domain.Provider
	executor    ACPExecutor
	telegram    TelegramGateway
	logger      *observability.Logger

	webhookSecret string
	webhookURL    string
	miniAppURL    string
	planTTL       time.Duration

	clock func() time.Time

	locksMu   sync.Mutex
	chatLocks map[int64]*sync.Mutex
}

type webhookUpdate struct {
	Message       *telegramMessageUpdate       `json:"message,omitempty"`
	CallbackQuery *telegramCallbackQueryUpdate `json:"callback_query,omitempty"`
}

type telegramMessageUpdate struct {
	MessageID int64        `json:"message_id"`
	Chat      telegramChat `json:"chat"`
	From      telegramFrom `json:"from"`
	Text      string       `json:"text"`
	Date      int64        `json:"date"`
}

type telegramCallbackQueryUpdate struct {
	ID      string               `json:"id"`
	From    telegramFrom         `json:"from"`
	Message *telegramCallbackMsg `json:"message,omitempty"`
	Data    string               `json:"data"`
}

type telegramCallbackMsg struct {
	MessageID int64        `json:"message_id"`
	Chat      telegramChat `json:"chat"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramFrom struct {
	ID int64 `json:"id"`
}

func NewHandler(cfg HandlerConfig, store session.SessionStore, interp InterpreterService, provider domain.Provider, executor ACPExecutor, tg TelegramGateway) (*Handler, error) {
	if store == nil {
		return nil, errors.New("bot handler: session store is nil")
	}
	if interp == nil {
		return nil, errors.New("bot handler: interpreter is nil")
	}
	if provider == nil {
		return nil, errors.New("bot handler: provider is nil")
	}
	if executor == nil {
		return nil, errors.New("bot handler: executor is nil")
	}
	if tg == nil {
		return nil, errors.New("bot handler: telegram gateway is nil")
	}
	if strings.TrimSpace(cfg.WebhookSecret) == "" {
		return nil, errors.New("bot handler: webhook secret is required")
	}
	if strings.TrimSpace(cfg.MiniAppURL) == "" {
		return nil, errors.New("bot handler: mini app URL is required")
	}
	if cfg.PlanTTL <= 0 {
		cfg.PlanTTL = 15 * time.Minute
	}

	return &Handler{
		store:         store,
		interpreter:   interp,
		provider:      provider,
		executor:      executor,
		telegram:      tg,
		logger:        observability.NewLogger(nil),
		webhookSecret: strings.TrimSpace(cfg.WebhookSecret),
		webhookURL:    strings.TrimSpace(cfg.WebhookURL),
		miniAppURL:    strings.TrimSpace(cfg.MiniAppURL),
		planTTL:       cfg.PlanTTL,
		clock:         func() time.Time { return time.Now().UTC() },
		chatLocks:     make(map[int64]*sync.Mutex),
	}, nil
}

func (h *Handler) SetLogger(logger *observability.Logger) {
	if h == nil || logger == nil {
		return
	}
	h.logger = logger
}

func (h *Handler) RegisterWebhook(ctx context.Context) error {
	if h == nil {
		return errors.New("bot handler: nil handler")
	}
	if strings.TrimSpace(h.webhookURL) == "" {
		return errors.New("bot handler: webhook URL is required")
	}
	return h.telegram.SetWebhook(ctx, h.webhookURL, h.webhookSecret, []string{"message", "callback_query"})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		http.Error(w, "handler is nil", http.StatusInternalServerError)
		return
	}
	started := time.Now()

	if r.Header.Get(telegramSecretHeader) != h.webhookSecret {
		h.logError(0, "", "bot/handler", started, "ErrForbidden", errors.New("invalid telegram webhook secret"), nil)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	defer r.Body.Close()
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		h.logError(0, "", "bot/handler", started, "ErrValidation", err, map[string]any{"stage": "read_body"})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var update webhookUpdate
	if err := json.Unmarshal(payload, &update); err != nil {
		h.logError(0, "", "bot/handler", started, "ErrValidation", err, map[string]any{"stage": "decode_update"})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var chatID int64
	switch {
	case update.Message != nil:
		chatID = update.Message.Chat.ID
	case update.CallbackQuery != nil && update.CallbackQuery.Message != nil:
		chatID = update.CallbackQuery.Message.Chat.ID
	case update.CallbackQuery != nil:
		chatID = update.CallbackQuery.From.ID
	default:
		w.WriteHeader(http.StatusOK)
		return
	}
	if chatID == 0 {
		h.logError(0, "", "bot/handler", started, "ErrValidation", errors.New("empty chat_id"), nil)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	processErr := h.withChatLock(chatID, func() error {
		switch {
		case update.Message != nil:
			return h.handleMessage(r.Context(), update.Message)
		case update.CallbackQuery != nil:
			return h.handleCallback(r.Context(), update.CallbackQuery)
		default:
			return nil
		}
	})

	if processErr != nil {
		h.logError(chatID, "", "bot/handler", started, errorKind(processErr), processErr, map[string]any{"stage": "process_update"})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) withChatLock(chatID int64, fn func() error) error {
	lock := h.getChatLock(chatID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (h *Handler) getChatLock(chatID int64) *sync.Mutex {
	h.locksMu.Lock()
	defer h.locksMu.Unlock()

	lock, ok := h.chatLocks[chatID]
	if !ok {
		lock = &sync.Mutex{}
		h.chatLocks[chatID] = lock
	}
	return lock
}

func (h *Handler) handleMessage(ctx context.Context, msg *telegramMessageUpdate) error {
	if msg == nil {
		return nil
	}

	chatID := msg.Chat.ID
	if err := h.telegram.SendChatAction(ctx, chatID, "typing"); err != nil {
		return err
	}

	s, err := LoadOrCreate(ctx, h.store, chatID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.Timezone) == "" {
		s.Timezone = "UTC"
	}

	if s.PendingPlan != nil {
		if _, warnErr := h.telegram.SendText(ctx, chatID, "Предыдущий запрос отменён\\.", nil); warnErr != nil {
			return warnErr
		}
		ClearPendingPlan(s)
	}

	_ = h.telegram.Draft(ctx, chatID, "🤔 Анализирую\\.\\.\\.")

	convo := interpreter.ConversationContext{
		Timezone: s.Timezone,
		History:  convertHistory(s.DialogHistory),
	}
	plan, err := h.interpreter.Interpret(ctx, strings.TrimSpace(msg.Text), convo)
	if err != nil {
		h.logError(chatID, "", "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "interpret"})
		_, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil)
		if sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	AppendHistory(s, "user", msg.Text)

	if plan.Intent == interpreter.IntentUnknown || plan.Confidence < 0.5 {
		if _, err := h.telegram.Finalize(ctx, chatID, FormatUnknownIntent(), nil); err != nil {
			return err
		}
		AppendHistory(s, "assistant", "unknown-intent")
		return h.store.Save(ctx, s)
	}

	if plan.NeedsClarification() {
		s.ClarificationCount++
		if s.ClarificationCount >= 2 {
			return h.sendDeepLinkEscalation(ctx, s, chatID, "clarification_limit", string(plan.Intent))
		}
		if _, err := h.telegram.Finalize(ctx, chatID, FormatClarification(plan.Clarifications[0].Question), nil); err != nil {
			return err
		}
		AppendHistory(s, "assistant", plan.Clarifications[0].Question)
		return h.store.Save(ctx, s)
	}

	s.ClarificationCount = 0
	switch plan.Intent {
	case interpreter.IntentListBookings:
		return h.handleListBookings(ctx, s, plan)
	case interpreter.IntentFindNextSlot:
		return h.handleFindNextSlot(ctx, s, plan)
	default:
		return h.handleWritePreview(ctx, s, plan)
	}
}

func (h *Handler) handleListBookings(ctx context.Context, s *session.Session, plan *interpreter.ActionPlan) error {
	chatID := s.ChatID
	if isLongRange(plan.Params.DateRange, s.Timezone) {
		return h.sendDeepLinkEscalation(ctx, s, chatID, "bookings", "range_gt_7d")
	}

	filter, err := buildBookingFilter(plan.Params, s.Timezone, h.clock())
	if err != nil {
		h.logError(chatID, string(plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "build_booking_filter"})
		if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	bookings, err := h.provider.GetBookings(ctx, s.ProviderID, filter)
	if err != nil {
		h.logError(chatID, string(plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "provider_get_bookings"})
		if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	location, _ := time.LoadLocation(s.Timezone)
	if location == nil {
		location = time.UTC
	}
	text := FormatBookingListPreview(bookings, location)
	if _, err := h.telegram.Finalize(ctx, chatID, text, nil); err != nil {
		return err
	}

	ClearPendingPlan(s)
	AppendHistory(s, "assistant", text)
	return h.store.Save(ctx, s)
}

func (h *Handler) handleFindNextSlot(ctx context.Context, s *session.Session, plan *interpreter.ActionPlan) error {
	chatID := s.ChatID
	req, err := buildSlotSearchRequest(plan.Params, h.clock())
	if err != nil {
		h.logError(chatID, string(plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "build_slot_search"})
		if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	slots, err := h.provider.FindSlots(ctx, s.ProviderID, req)
	if err != nil {
		h.logError(chatID, string(plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "provider_find_slots"})
		if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	location, _ := time.LoadLocation(s.Timezone)
	if location == nil {
		location = time.UTC
	}

	pending := SetPendingPlan(s, *plan, 0, "")
	keyboard := BuildSlotKeyboard(pending.ID, slots, location)
	text := FormatFindSlotResult(slots, location)
	msgID, err := h.telegram.Finalize(ctx, chatID, text, &keyboard)
	if err != nil {
		return err
	}

	pending.PreviewMsgID = msgID
	AppendHistory(s, "assistant", text)
	return h.store.Save(ctx, s)
}

func (h *Handler) handleWritePreview(ctx context.Context, s *session.Session, plan *interpreter.ActionPlan) error {
	chatID := s.ChatID
	params := mapActionParams(plan.Params)

	var (
		preview *domain.Preview
		err     error
	)
	switch plan.Intent {
	case interpreter.IntentSetWorkingHours, interpreter.IntentAddBreak, interpreter.IntentCloseRange:
		preview, err = h.provider.PreviewAvailabilityChange(ctx, s.ProviderID, params)
	case interpreter.IntentCreateBooking:
		preview, err = h.provider.PreviewBookingCreate(ctx, s.ProviderID, params)
	case interpreter.IntentCancelBooking:
		preview, err = h.provider.PreviewBookingCancel(ctx, s.ProviderID, params)
	default:
		err = errors.New("bot handler: unsupported write intent")
	}
	if err != nil {
		h.logError(chatID, string(plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "build_preview"})
		if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	if plan.Intent != interpreter.IntentCreateBooking &&
		preview.AvailabilityChange.AddedSlots+preview.AvailabilityChange.RemovedSlots > 20 {
		return h.sendDeepLinkEscalation(ctx, s, chatID, "availability", "impact_gt_20")
	}

	pending := SetPendingPlan(s, *plan, 0, "")
	pending.IdempotencyKey = buildIdempotencyKey(chatID, pending.ID, plan.Intent)

	location, _ := time.LoadLocation(s.Timezone)
	if location == nil {
		location = time.UTC
	}

	if plan.Intent == interpreter.IntentCreateBooking {
		if len(preview.ProposedSlots) == 0 {
			if _, sendErr := h.telegram.SendText(ctx, chatID, FormatError("not_found"), nil); sendErr != nil {
				return sendErr
			}
			return h.store.Save(ctx, s)
		}
		if strings.TrimSpace(pending.Plan.Params.ServiceID) == "" {
			pending.Plan.Params.ServiceID = strings.TrimSpace(preview.ProposedSlots[0].ServiceID)
		}

		keyboard := BuildSlotKeyboard(pending.ID, preview.ProposedSlots, location)
		text := FormatCreatePreview(*preview, location)
		msgID, finalizeErr := h.telegram.Finalize(ctx, chatID, text, &keyboard)
		if finalizeErr != nil {
			return finalizeErr
		}
		pending.PreviewMsgID = msgID
		AppendHistory(s, "assistant", text)
		return h.store.Save(ctx, s)
	}

	if plan.Intent == interpreter.IntentCancelBooking && preview.BookingResult != nil &&
		strings.TrimSpace(pending.Plan.Params.BookingID) == "" {
		pending.Plan.Params.BookingID = strings.TrimSpace(preview.BookingResult.ID)
	}

	keyboard := BuildPreviewKeyboard(pending.ID)
	text := formatPreviewByIntent(*preview, plan.Intent, location)
	msgID, err := h.telegram.Finalize(ctx, chatID, text, &keyboard)
	if err != nil {
		return err
	}
	pending.PreviewMsgID = msgID
	AppendHistory(s, "assistant", text)
	return h.store.Save(ctx, s)
}

func (h *Handler) handleCallback(ctx context.Context, cb *telegramCallbackQueryUpdate) error {
	if cb == nil {
		return nil
	}

	chatID := cb.From.ID
	if cb.Message != nil && cb.Message.Chat.ID != 0 {
		chatID = cb.Message.Chat.ID
	}
	if chatID == 0 {
		return errors.New("bot handler: callback chat_id is empty")
	}

	_ = h.telegram.AnswerCallbackQuery(ctx, cb.ID, "")

	parsed, err := ParseCallback(cb.Data)
	if err != nil {
		_, sendErr := h.telegram.SendText(ctx, chatID, "Запрос устарел, повтори заново\\.", nil)
		if sendErr != nil {
			return sendErr
		}
		return nil
	}

	s, err := LoadOrCreate(ctx, h.store, chatID)
	if err != nil {
		return err
	}
	if s.PendingPlan == nil || s.PendingPlan.ID != parsed.PlanID {
		_, sendErr := h.telegram.SendText(ctx, chatID, "Запрос устарел, повтори заново\\.", nil)
		if sendErr != nil {
			return sendErr
		}
		return nil
	}
	if IsPlanExpired(s.PendingPlan, h.clock(), h.planTTL) {
		ClearPendingPlan(s)
		if err := h.store.Save(ctx, s); err != nil {
			return err
		}
		_, sendErr := h.telegram.SendText(ctx, chatID, "Запрос устарел, повтори заново\\.", nil)
		return sendErr
	}

	switch parsed.Type {
	case CallbackTypeCancel:
		if err := h.telegram.EditMessageReplyMarkup(ctx, chatID, s.PendingPlan.PreviewMsgID, &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}); err != nil {
			return err
		}
		if _, err := h.telegram.SendText(ctx, chatID, "Понял, ничего не изменено\\.", nil); err != nil {
			return err
		}
		ClearPendingPlan(s)
		return h.store.Save(ctx, s)
	case CallbackTypeSlot:
		return h.handleSlotSelection(ctx, s, parsed.SlotIndex)
	case CallbackTypeConfirm:
		return h.handleConfirm(ctx, s)
	default:
		return nil
	}
}

func (h *Handler) handleSlotSelection(ctx context.Context, s *session.Session, idx int) error {
	req, err := buildSlotSearchRequest(s.PendingPlan.Plan.Params, h.clock())
	if err != nil {
		h.logError(s.ChatID, string(s.PendingPlan.Plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "slot_select_build_search"})
		if _, sendErr := h.telegram.SendText(ctx, s.ChatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return nil
	}

	slots, err := h.provider.FindSlots(ctx, s.ProviderID, req)
	if err != nil {
		h.logError(s.ChatID, string(s.PendingPlan.Plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "slot_select_find_slots"})
		if _, sendErr := h.telegram.SendText(ctx, s.ChatID, FormatError(errorKind(err)), nil); sendErr != nil {
			return sendErr
		}
		return nil
	}
	if idx < 0 || idx >= len(slots) {
		if _, sendErr := h.telegram.SendText(ctx, s.ChatID, "Выбранный слот недоступен\\.", nil); sendErr != nil {
			return sendErr
		}
		return nil
	}

	selected := slots[idx]
	if err := h.telegram.EditMessageReplyMarkup(ctx, s.ChatID, s.PendingPlan.PreviewMsgID, &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}); err != nil {
		return err
	}

	pending := s.PendingPlan
	pending.Plan.Intent = interpreter.IntentCreateBooking
	pending.Plan.RequiresConfirm = true
	pending.Plan.Params.SlotID = strings.TrimSpace(selected.ID)
	pending.Plan.Params.ServiceID = strings.TrimSpace(selected.ServiceID)
	pending.Plan.Params.NotBefore = selected.Start.UTC().Format(time.RFC3339)
	pending.Plan.Params.PreferredAt = selected.Start.UTC().Format(time.RFC3339)
	pending.IdempotencyKey = buildIdempotencyKey(s.ChatID, pending.ID, pending.Plan.Intent)

	location, _ := time.LoadLocation(s.Timezone)
	if location == nil {
		location = time.UTC
	}
	text := FormatCreatePreview(domain.Preview{
		ProposedSlots: []domain.Slot{selected},
	}, location) + "\n\nПодтвердить создание записи\\?"
	keyboard := BuildPreviewKeyboard(pending.ID)
	msgID, err := h.telegram.Finalize(ctx, s.ChatID, text, &keyboard)
	if err != nil {
		return err
	}
	pending.PreviewMsgID = msgID
	AppendHistory(s, "assistant", text)
	return h.store.Save(ctx, s)
}

func (h *Handler) handleConfirm(ctx context.Context, s *session.Session) error {
	if err := h.telegram.EditMessageReplyMarkup(ctx, s.ChatID, s.PendingPlan.PreviewMsgID, &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}); err != nil {
		return err
	}
	if _, err := h.telegram.SendText(ctx, s.ChatID, "Выполняю\\.\\.\\.", nil); err != nil {
		return err
	}

	result, err := h.executor.ExecuteConfirmed(ctx, s, s.PendingPlan)
	if err != nil {
		h.logError(s.ChatID, string(s.PendingPlan.Plan.Intent), "bot/handler", time.Now(), errorKind(err), err, map[string]any{"stage": "execute_confirmed"})
		if errors.Is(err, ErrExecutionPolicyViolation) {
			_, sendErr := h.telegram.SendText(ctx, s.ChatID, "Не удалось применить изменения\\. Причина: policy violation\\.", nil)
			return sendErr
		}
		if errors.Is(err, ErrExecutionContractBlocked) {
			link := h.BuildDeepLink("execution_blocked", string(s.PendingPlan.Plan.Intent))
			keyboard := InlineKeyboardMarkup{
				InlineKeyboard: [][]InlineKeyboardButton{
					{NewWebAppButton("Открыть в приложении →", link)},
				},
			}
			if _, sendErr := h.telegram.SendText(ctx, s.ChatID, "Эта операция временно недоступна в текущем контракте API\\. Открой приложение для выполнения\\.", &keyboard); sendErr != nil {
				return sendErr
			}
			ClearPendingPlan(s)
			return h.store.Save(ctx, s)
		}
		if errors.Is(err, ErrExecutionTransient) {
			retryKeyboard := buildRetryKeyboard(s.PendingPlan.ID)
			_, sendErr := h.telegram.SendText(ctx, s.ChatID, FormatError("upstream"), &retryKeyboard)
			if sendErr != nil {
				return sendErr
			}
			return h.store.Save(ctx, s)
		}
		retryKeyboard := buildRetryKeyboard(s.PendingPlan.ID)
		_, sendErr := h.telegram.SendText(ctx, s.ChatID, FormatError(errorKind(err)), &retryKeyboard)
		if sendErr != nil {
			return sendErr
		}
		return h.store.Save(ctx, s)
	}

	message := "Готово\\. Изменения применены\\."
	if result != nil && strings.TrimSpace(result.Message) != "" {
		message = escapeV2(strings.TrimSpace(result.Message))
	}
	if _, err := h.telegram.SendText(ctx, s.ChatID, message, nil); err != nil {
		return err
	}

	ClearPendingPlan(s)
	return h.store.Save(ctx, s)
}

func (h *Handler) BuildDeepLink(action, contextValue string) string {
	base, err := url.Parse(h.miniAppURL)
	if err != nil {
		return h.miniAppURL
	}
	query := base.Query()
	query.Set("action", strings.TrimSpace(action))
	query.Set("context", strings.TrimSpace(contextValue))
	base.RawQuery = query.Encode()
	return base.String()
}

func (h *Handler) sendDeepLinkEscalation(ctx context.Context, s *session.Session, chatID int64, action, contextValue string) error {
	link := h.BuildDeepLink(action, contextValue)
	keyboard := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{NewWebAppButton("Открыть в приложении →", link)},
		},
	}
	text := "Для этого шага удобнее перейти в приложение\\."
	if _, err := h.telegram.SendText(ctx, chatID, text, &keyboard); err != nil {
		return err
	}
	AppendHistory(s, "assistant", text)
	return h.store.Save(ctx, s)
}

func formatPreviewByIntent(preview domain.Preview, intent interpreter.Intent, tz *time.Location) string {
	switch intent {
	case interpreter.IntentCreateBooking:
		return FormatCreatePreview(preview, tz)
	case interpreter.IntentCancelBooking:
		return FormatCancelPreview(preview)
	default:
		return FormatAvailabilityPreview(preview)
	}
}

func buildRetryKeyboard(planID string) InlineKeyboardMarkup {
	return InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{
					Text:         "🔁 Повторить",
					CallbackData: ConfirmData(planID),
					Style:        buttonStyleSuccess,
				},
				{
					Text:         "❌ Отменить",
					CallbackData: CancelData(planID),
					Style:        buttonStyleDanger,
				},
			},
		},
	}
}

func buildIdempotencyKey(chatID int64, planID string, intent interpreter.Intent) string {
	raw := fmt.Sprintf("%d:%s:%s", chatID, strings.TrimSpace(planID), string(intent))
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func convertHistory(history []session.Message) []interpreter.Turn {
	if len(history) == 0 {
		return nil
	}
	turns := make([]interpreter.Turn, 0, len(history))
	for _, msg := range history {
		turns = append(turns, interpreter.Turn{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return turns
}

func mapActionParams(p interpreter.ActionParams) domain.ActionParams {
	out := domain.ActionParams{
		Weekdays:        append([]string(nil), p.Weekdays...),
		ClientName:      p.ClientName,
		ClientReference: p.ClientReference,
		ServiceID:       p.ServiceID,
		ServiceName:     p.ServiceName,
		SlotID:          p.SlotID,
		BookingID:       p.BookingID,
		NotBefore:       p.NotBefore,
		PreferredAt:     p.PreferredAt,
		PreferredDate:   p.PreferredDate,
		ApproximateTime: p.ApproximateTime,
		Status:          p.Status,
	}
	if p.DateRange != nil {
		out.DateRange = &domain.DateRange{From: p.DateRange.From, To: p.DateRange.To}
	}
	if p.WorkingHours != nil {
		out.WorkingHours = &domain.TimeRange{From: p.WorkingHours.From, To: p.WorkingHours.To}
	}
	if p.BreakSlot != nil {
		out.BreakSlot = &domain.TimeRange{From: p.BreakSlot.From, To: p.BreakSlot.To}
	}
	if p.TimeRange != nil {
		out.TimeRange = &domain.TimeRange{From: p.TimeRange.From, To: p.TimeRange.To}
	}
	if len(p.Breaks) > 0 {
		out.Breaks = make([]domain.TimeRange, 0, len(p.Breaks))
		for _, br := range p.Breaks {
			out.Breaks = append(out.Breaks, domain.TimeRange{From: br.From, To: br.To})
		}
	}
	return out
}

func buildBookingFilter(params interpreter.ActionParams, tzName string, now time.Time) (domain.BookingFilter, error) {
	loc := time.UTC
	if strings.TrimSpace(tzName) != "" {
		if l, err := time.LoadLocation(tzName); err == nil {
			loc = l
		}
	}

	from := now.In(loc)
	to := from
	if params.DateRange != nil && strings.TrimSpace(params.DateRange.From) != "" {
		parsedFrom, err := time.ParseInLocation("2006-01-02", params.DateRange.From, loc)
		if err != nil {
			return domain.BookingFilter{}, errors.Join(domain.ErrValidation, err)
		}
		from = parsedFrom
		if strings.TrimSpace(params.DateRange.To) != "" {
			parsedTo, err := time.ParseInLocation("2006-01-02", params.DateRange.To, loc)
			if err != nil {
				return domain.BookingFilter{}, errors.Join(domain.ErrValidation, err)
			}
			to = parsedTo
		} else {
			to = parsedFrom
		}
	}

	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc).UTC()
	end := time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, loc).UTC()
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = "upcoming"
	}
	direction := "future"
	if strings.EqualFold(status, "past") {
		direction = "past"
	}
	return domain.BookingFilter{
		From:      &start,
		To:        &end,
		Status:    status,
		Direction: direction,
		Limit:     50,
	}, nil
}

func buildSlotSearchRequest(params interpreter.ActionParams, now time.Time) (domain.SlotSearchRequest, error) {
	serviceID := strings.TrimSpace(params.ServiceID)
	if serviceID == "" {
		return domain.SlotSearchRequest{}, errors.Join(domain.ErrValidation, errors.New("service_id is required"))
	}

	from := now.UTC()
	if strings.TrimSpace(params.NotBefore) != "" {
		parsed, err := parseFlexibleDateTime(params.NotBefore)
		if err != nil {
			return domain.SlotSearchRequest{}, errors.Join(domain.ErrValidation, err)
		}
		from = parsed.UTC()
	}

	return domain.SlotSearchRequest{
		ServiceID:  serviceID,
		From:       from,
		To:         from.Add(7 * 24 * time.Hour),
		MaxResults: 2,
	}, nil
}

func parseFlexibleDateTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, errors.New("datetime is empty")
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:00",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid datetime %q", raw)
}

func isLongRange(r *interpreter.DateRange, tzName string) bool {
	if r == nil || strings.TrimSpace(r.From) == "" || strings.TrimSpace(r.To) == "" {
		return false
	}
	loc := time.UTC
	if strings.TrimSpace(tzName) != "" {
		if loaded, err := time.LoadLocation(tzName); err == nil {
			loc = loaded
		}
	}
	from, err := time.ParseInLocation("2006-01-02", r.From, loc)
	if err != nil {
		return false
	}
	to, err := time.ParseInLocation("2006-01-02", r.To, loc)
	if err != nil {
		return false
	}
	if to.Before(from) {
		return false
	}
	days := int(to.Sub(from).Hours()/24) + 1
	return days > 7
}

func errorKind(err error) string {
	switch {
	case err == nil:
		return "upstream"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrValidation):
		return "validation"
	case errors.Is(err, domain.ErrUpstream), errors.Is(err, ErrExecutionTransient):
		return "upstream"
	default:
		return "upstream"
	}
}

func (h *Handler) logError(chatID int64, intent, component string, started time.Time, errType string, err error, fields map[string]any) {
	if h == nil || h.logger == nil {
		return
	}
	duration := int64(0)
	if !started.IsZero() {
		duration = time.Since(started).Milliseconds()
	}
	h.logger.LogError(observability.Entry{
		TraceID:    newTraceID(),
		ChatID:     chatID,
		Intent:     strings.TrimSpace(intent),
		Component:  component,
		DurationMS: duration,
		ErrorType:  errType,
		Error:      err,
		Fields:     fields,
	})
}

func newTraceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
