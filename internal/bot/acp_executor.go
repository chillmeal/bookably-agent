package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/acp"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/internal/interpreter"
	"github.com/chillmeal/bookably-agent/internal/session"
	"github.com/chillmeal/bookably-agent/observability"
)

type ACPRunSubmitter interface {
	SubmitAndWait(ctx context.Context, run acp.ACPRun) (*acp.ACPRunResult, error)
}

type RuntimeACPExecutor struct {
	bookablyBaseURL string
	botServiceKey   string
	runner          ACPRunSubmitter
	httpClient      *http.Client
	logger          *observability.Logger
}

func NewRuntimeACPExecutor(bookablyBaseURL, botServiceKey string, runner ACPRunSubmitter) (*RuntimeACPExecutor, error) {
	if strings.TrimSpace(bookablyBaseURL) == "" {
		return nil, errors.New("bot acp executor: bookably base url is required")
	}
	if strings.TrimSpace(botServiceKey) == "" {
		return nil, errors.New("bot acp executor: bot service key is required")
	}
	if runner == nil {
		return nil, errors.New("bot acp executor: runner is nil")
	}
	return &RuntimeACPExecutor{
		bookablyBaseURL: strings.TrimRight(strings.TrimSpace(bookablyBaseURL), "/"),
		botServiceKey:   strings.TrimSpace(botServiceKey),
		runner:          runner,
		httpClient:      &http.Client{Timeout: 8 * time.Second},
		logger:          observability.NewLogger(nil),
	}, nil
}

func (e *RuntimeACPExecutor) SetLogger(logger *observability.Logger) {
	if e == nil || logger == nil {
		return
	}
	e.logger = logger
}

func (e *RuntimeACPExecutor) ExecuteConfirmed(ctx context.Context, s *session.Session, pending *session.PendingPlan) (*ExecutionResult, error) {
	if e == nil {
		return nil, errors.New("bot acp executor: nil executor")
	}
	if s == nil {
		return nil, errors.New("bot acp executor: nil session")
	}
	if pending == nil {
		return nil, errors.New("bot acp executor: nil pending plan")
	}
	if strings.TrimSpace(s.ProviderID) == "" {
		return nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: provider_id is required"))
	}
	if s.TelegramUserID <= 0 {
		return nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: telegram_user_id is required"))
	}

	started := time.Now()
	meta := acp.RunMetadata{
		ChatID:       fmt.Sprintf("%d", s.ChatID),
		SpecialistID: strings.TrimSpace(s.ProviderID),
		Intent:       string(pending.Plan.Intent),
		RiskLevel:    riskFromIntent(pending.Plan.Intent),
		RawMessage:   strings.TrimSpace(pending.Plan.RawUserMessage),
	}

	if shouldExecuteDirectFirst(pending.Plan.Intent) {
		if err := e.executeDirectBookably(ctx, s, pending); err != nil {
			e.logError(s, pending, started, classifyExecutionErrorType(err), err, map[string]any{
				"stage": "direct_primary",
			})
			switch {
			case errors.Is(err, domain.ErrUpstream), errors.Is(err, domain.ErrRateLimit):
				return nil, errors.Join(ErrExecutionTransient, err)
			default:
				return nil, err
			}
		}
		msg := successMessageForIntent(pending.Plan.Intent)
		if strings.TrimSpace(msg) == "" {
			msg = "Готово. Изменения применены."
		}
		return &ExecutionResult{Message: msg}, nil
	}

	run, err := e.buildRun(s.TelegramUserID, pending, meta, s.Timezone)
	if err != nil {
		errType := "ErrValidation"
		if errors.Is(err, ErrExecutionContractBlocked) {
			errType = "ErrExecutionContractBlocked"
		}
		e.logError(s, pending, started, errType, err, map[string]any{"stage": "build_run"})
		return nil, err
	}

	result, runErr := e.runner.SubmitAndWait(ctx, *run)
	if runErr != nil {
		if shouldFallbackDirect(runErr) && canDirectExecute(pending.Plan.Intent) {
			if directErr := e.executeDirectBookably(ctx, s, pending); directErr == nil {
				msg := successMessageForIntent(pending.Plan.Intent)
				if strings.TrimSpace(msg) == "" {
					msg = "Готово. Изменения применены."
				}
				return &ExecutionResult{Message: msg}, nil
			} else {
				e.logError(s, pending, started, classifyExecutionErrorType(directErr), directErr, map[string]any{
					"stage": "direct_fallback",
				})
			}
		}
		e.logError(s, pending, started, classifyExecutionErrorType(runErr), runErr, map[string]any{"stage": "submit_and_wait"})
		switch {
		case errors.Is(runErr, acp.ErrACPPolicyViolation):
			return nil, errors.Join(ErrExecutionPolicyViolation, runErr)
		case errors.Is(runErr, acp.ErrACPTimeout),
			errors.Is(runErr, acp.ErrACPTransient),
			errors.Is(runErr, domain.ErrUpstream),
			errors.Is(runErr, domain.ErrRateLimit):
			return nil, errors.Join(ErrExecutionTransient, runErr)
		default:
			return nil, runErr
		}
	}

	msg := successMessageForIntent(pending.Plan.Intent)
	if result != nil && strings.TrimSpace(msg) == "" {
		msg = "Готово\\. Изменения применены\\."
	}
	return &ExecutionResult{Message: msg}, nil
}

func shouldFallbackDirect(err error) bool {
	if err == nil {
		return false
	}
	raw := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(raw, "agent_id") && strings.Contains(raw, "must not be empty") {
		return true
	}
	if strings.Contains(raw, "policy_contract_addr") && strings.Contains(raw, "must not be empty") {
		return true
	}
	return false
}

func canDirectExecute(intent interpreter.Intent) bool {
	switch intent {
	case interpreter.IntentCancelBooking, interpreter.IntentSetWorkingHours, interpreter.IntentAddBreak, interpreter.IntentCloseRange:
		return true
	default:
		return false
	}
}

func shouldExecuteDirectFirst(intent interpreter.Intent) bool {
	switch intent {
	case interpreter.IntentSetWorkingHours, interpreter.IntentAddBreak, interpreter.IntentCloseRange:
		return true
	default:
		return false
	}
}

func (e *RuntimeACPExecutor) executeDirectBookably(ctx context.Context, s *session.Session, pending *session.PendingPlan) error {
	switch pending.Plan.Intent {
	case interpreter.IntentCancelBooking:
		bookingID := strings.TrimSpace(pending.Plan.Params.BookingID)
		if bookingID == "" {
			return errors.Join(domain.ErrValidation, errors.New("bot acp executor: booking_id is required for direct cancel"))
		}
		endpoint := fmt.Sprintf("%s/api/v1/specialist/bookings/%s/cancel", e.bookablyBaseURL, bookingID)
		return e.doDirectRequest(ctx, http.MethodPost, endpoint, s.TelegramUserID, pending.IdempotencyKey, nil)
	case interpreter.IntentSetWorkingHours, interpreter.IntentAddBreak, interpreter.IntentCloseRange:
		create, deleteIDs, err := buildAvailabilityCommitPayload(pending, s.Timezone)
		if err != nil {
			return err
		}
		body := acp.CommitScheduleBody{
			Create: create,
			Delete: make([]acp.CommitDeleteItem, 0, len(deleteIDs)),
		}
		for _, id := range deleteIDs {
			body.Delete = append(body.Delete, acp.CommitDeleteItem{SlotID: id})
		}
		endpoint := fmt.Sprintf("%s/api/v1/specialist/schedule/commit", e.bookablyBaseURL)
		return e.doDirectRequest(ctx, http.MethodPost, endpoint, s.TelegramUserID, pending.IdempotencyKey+":commit", body)
	default:
		return errors.Join(ErrExecutionContractBlocked, fmt.Errorf("bot acp executor: direct execution is unsupported for intent %q", pending.Plan.Intent))
	}
}

func (e *RuntimeACPExecutor) doDirectRequest(ctx context.Context, method, url string, telegramUserID int64, idempotencyKey string, body any) error {
	if e.httpClient == nil {
		e.httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: marshal direct payload: %w", err))
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: build direct request: %w", err))
	}
	req.Header.Set("X-Bot-Service-Key", e.botServiceKey)
	req.Header.Set("X-Telegram-User-Id", strconv.FormatInt(telegramUserID, 10))
	req.Header.Set("Idempotency-Key", strings.TrimSpace(idempotencyKey))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return errors.Join(domain.ErrUpstream, fmt.Errorf("bot acp executor: direct request failed: %w", err))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return mapDirectHTTPError(resp.StatusCode, respBody)
}

func mapDirectHTTPError(status int, body []byte) error {
	type envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	var out envelope
	_ = json.Unmarshal(body, &out)
	msg := strings.TrimSpace(out.Error.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = fmt.Sprintf("http status %d", status)
	}
	switch status {
	case http.StatusUnauthorized:
		return errors.Join(domain.ErrUnauthorized, errors.New(msg))
	case http.StatusForbidden:
		return errors.Join(domain.ErrForbidden, errors.New(msg))
	case http.StatusNotFound:
		return errors.Join(domain.ErrNotFound, errors.New(msg))
	case http.StatusConflict:
		return errors.Join(domain.ErrConflict, errors.New(msg))
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return errors.Join(domain.ErrValidation, errors.New(msg))
	case http.StatusTooManyRequests:
		return errors.Join(domain.ErrRateLimit, errors.New(msg))
	default:
		if status >= 500 {
			return errors.Join(domain.ErrUpstream, errors.New(msg))
		}
		return errors.Join(domain.ErrValidation, errors.New(msg))
	}
}

func (e *RuntimeACPExecutor) logError(s *session.Session, pending *session.PendingPlan, started time.Time, errType string, err error, fields map[string]any) {
	if e == nil || e.logger == nil {
		return
	}

	chatID := int64(0)
	intent := ""
	if s != nil {
		chatID = s.ChatID
	}
	if pending != nil {
		intent = string(pending.Plan.Intent)
	}

	e.logger.LogError(observability.Entry{
		TraceID:    newTraceID(),
		ChatID:     chatID,
		Intent:     intent,
		Component:  "bot/acp_executor",
		DurationMS: time.Since(started).Milliseconds(),
		ErrorType:  errType,
		Error:      err,
		Fields:     fields,
	})
}

func classifyExecutionErrorType(err error) string {
	switch {
	case err == nil:
		return "ErrUnknown"
	case errors.Is(err, acp.ErrACPPolicyViolation):
		return "ErrACPPolicyViolation"
	case errors.Is(err, acp.ErrACPTimeout):
		return "ErrACPTimeout"
	case errors.Is(err, acp.ErrACPTransient), errors.Is(err, domain.ErrUpstream):
		return "ErrUpstream"
	case errors.Is(err, domain.ErrRateLimit):
		return "ErrRateLimit"
	default:
		return "ErrExecution"
	}
}

func (e *RuntimeACPExecutor) buildRun(telegramUserID int64, pending *session.PendingPlan, meta acp.RunMetadata, timezone string) (*acp.ACPRun, error) {
	switch pending.Plan.Intent {
	case interpreter.IntentCancelBooking:
		bookingID := strings.TrimSpace(pending.Plan.Params.BookingID)
		if bookingID == "" {
			return nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: booking_id is required for cancel"))
		}
		return acp.BuildCancelBookingRun(e.bookablyBaseURL, e.botServiceKey, telegramUserID, bookingID, pending.IdempotencyKey, meta)
	case interpreter.IntentCreateBooking:
		return nil, errors.Join(
			ErrExecutionContractBlocked,
			errors.New("bot acp executor: create_booking execution is blocked until backend exposes specialist-initiated create-booking contract for named clients"),
		)
	case interpreter.IntentSetWorkingHours, interpreter.IntentAddBreak, interpreter.IntentCloseRange:
		create, deleteIDs, err := buildAvailabilityCommitPayload(pending, timezone)
		if err != nil {
			return nil, err
		}
		return acp.BuildAvailabilityRun(e.bookablyBaseURL, e.botServiceKey, telegramUserID, create, deleteIDs, pending.IdempotencyKey, meta)
	default:
		return nil, errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: unsupported intent %q", pending.Plan.Intent))
	}
}

func buildAvailabilityCommitPayload(pending *session.PendingPlan, tzName string) ([]acp.CommitCreateItem, []string, error) {
	if pending == nil || pending.Availability == nil {
		return nil, nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: pending availability payload is missing"))
	}
	if strings.TrimSpace(tzName) == "" {
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(strings.TrimSpace(tzName))
	if err != nil {
		return nil, nil, errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: invalid timezone %q", tzName))
	}

	create := make([]acp.CommitCreateItem, 0, len(pending.Availability.Create))
	for idx, item := range pending.Availability.Create {
		start, startErr := time.Parse(time.RFC3339, strings.TrimSpace(item.StartAt))
		if startErr != nil {
			return nil, nil, errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: invalid availability create start at index %d", idx))
		}
		end, endErr := time.Parse(time.RFC3339, strings.TrimSpace(item.EndAt))
		if endErr != nil {
			return nil, nil, errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: invalid availability create end at index %d", idx))
		}
		if !end.After(start) {
			return nil, nil, errors.Join(domain.ErrValidation, fmt.Errorf("bot acp executor: availability create item %d has non-positive duration", idx))
		}

		localStart := start.In(loc)
		localEnd := end.In(loc)
		create = append(create, acp.CommitCreateItem{
			Date:      localStart.Format("2006-01-02"),
			StartTime: localStart.Format("15:04"),
			EndTime:   localEnd.Format("15:04"),
		})
	}

	deleteIDs := make([]string, 0, len(pending.Availability.DeleteSlotIDs))
	for _, raw := range pending.Availability.DeleteSlotIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		deleteIDs = append(deleteIDs, id)
	}
	if len(create) == 0 && len(deleteIDs) == 0 {
		return nil, nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: availability payload contains no operations"))
	}
	return create, deleteIDs, nil
}

func riskFromIntent(intent interpreter.Intent) string {
	switch intent {
	case interpreter.IntentCancelBooking:
		return string(domain.RiskHigh)
	case interpreter.IntentCreateBooking, interpreter.IntentSetWorkingHours, interpreter.IntentCloseRange:
		return string(domain.RiskMedium)
	default:
		return string(domain.RiskLow)
	}
}

func successMessageForIntent(intent interpreter.Intent) string {
	switch intent {
	case interpreter.IntentCancelBooking:
		return "Запись отменена."
	case interpreter.IntentCreateBooking:
		return "Запись создана."
	default:
		return "Готово. Изменения применены."
	}
}
