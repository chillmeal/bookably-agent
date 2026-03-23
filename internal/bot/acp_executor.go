package bot

import (
	"context"
	"errors"
	"fmt"
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

type AccessTokenProvider interface {
	GetAccessToken(ctx context.Context, specialistID string) (string, error)
}

type RuntimeACPExecutor struct {
	bookablyBaseURL string
	runner          ACPRunSubmitter
	tokenProvider   AccessTokenProvider
	logger          *observability.Logger
}

func NewRuntimeACPExecutor(bookablyBaseURL string, runner ACPRunSubmitter, tokenProvider AccessTokenProvider) (*RuntimeACPExecutor, error) {
	if strings.TrimSpace(bookablyBaseURL) == "" {
		return nil, errors.New("bot acp executor: bookably base url is required")
	}
	if runner == nil {
		return nil, errors.New("bot acp executor: runner is nil")
	}
	if tokenProvider == nil {
		return nil, errors.New("bot acp executor: token provider is nil")
	}
	return &RuntimeACPExecutor{
		bookablyBaseURL: strings.TrimRight(strings.TrimSpace(bookablyBaseURL), "/"),
		runner:          runner,
		tokenProvider:   tokenProvider,
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

	started := time.Now()
	accessToken, err := e.tokenProvider.GetAccessToken(ctx, s.ProviderID)
	if err != nil {
		e.logError(s, pending, started, "ErrUnauthorized", err, map[string]any{"stage": "token_provider"})
		return nil, err
	}

	meta := acp.RunMetadata{
		ChatID:       fmt.Sprintf("%d", s.ChatID),
		SpecialistID: strings.TrimSpace(s.ProviderID),
		Intent:       string(pending.Plan.Intent),
		RiskLevel:    riskFromIntent(pending.Plan.Intent),
		RawMessage:   strings.TrimSpace(pending.Plan.RawUserMessage),
	}

	run, err := e.buildRun(accessToken, pending, meta, s.Timezone)
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

func (e *RuntimeACPExecutor) buildRun(accessToken string, pending *session.PendingPlan, meta acp.RunMetadata, timezone string) (*acp.ACPRun, error) {
	switch pending.Plan.Intent {
	case interpreter.IntentCancelBooking:
		bookingID := strings.TrimSpace(pending.Plan.Params.BookingID)
		if bookingID == "" {
			return nil, errors.Join(domain.ErrValidation, errors.New("bot acp executor: booking_id is required for cancel"))
		}
		return acp.BuildCancelBookingRun(e.bookablyBaseURL, accessToken, bookingID, pending.IdempotencyKey, meta)
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
		return acp.BuildAvailabilityRun(e.bookablyBaseURL, accessToken, create, deleteIDs, pending.IdempotencyKey, meta)
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
		return "Запись отменена\\."
	case interpreter.IntentCreateBooking:
		return "Запись создана\\."
	default:
		return "Готово\\. Изменения применены\\."
	}
}
