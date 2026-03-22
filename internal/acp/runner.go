package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

var (
	ErrACPPolicyViolation = errors.New("acp: policy violation")
	ErrACPTimeout         = errors.New("acp: timeout")
	ErrACPTransient       = errors.New("acp: transient failure")
)

type Runner struct {
	client       *Client
	pollInterval time.Duration
	timeout      time.Duration
}

func NewRunner(client *Client, pollInterval, timeout time.Duration) (*Runner, error) {
	if client == nil {
		return nil, errors.New("acp runner: client is nil")
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Runner{client: client, pollInterval: pollInterval, timeout: timeout}, nil
}

func (r *Runner) SubmitAndWait(ctx context.Context, run ACPRun) (*ACPRunResult, error) {
	runID, err := r.client.SubmitRun(ctx, run)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	for {
		result, getErr := r.client.GetRun(waitCtx, runID)
		if getErr != nil {
			var failedErr *RunFailedError
			if errors.As(getErr, &failedErr) {
				return failedErr.Result, classifyRunFailure(failedErr.Result)
			}
			if errors.Is(getErr, context.DeadlineExceeded) || errors.Is(getErr, context.Canceled) {
				return nil, errors.Join(ErrACPTimeout, getErr)
			}
			return nil, getErr
		}

		switch result.Status {
		case ACPStatusCompleted:
			return result, nil
		case ACPStatusFailed:
			return result, classifyRunFailure(result)
		}

		select {
		case <-waitCtx.Done():
			return nil, errors.Join(ErrACPTimeout, waitCtx.Err())
		case <-time.After(r.pollInterval):
		}
	}
}

func classifyRunFailure(result *ACPRunResult) error {
	if result == nil {
		return errors.Join(domain.ErrUpstream, errors.New("acp: failed with nil result"))
	}

	message := "run failed"
	code := ""
	errType := ""
	if result.Error != nil {
		if strings.TrimSpace(result.Error.Message) != "" {
			message = strings.TrimSpace(result.Error.Message)
		}
		code = strings.ToUpper(strings.TrimSpace(result.Error.Code))
		errType = strings.ToUpper(strings.TrimSpace(result.Error.Type))
	}
	probe := code + " " + errType

	switch {
	case strings.Contains(probe, "POLICY"):
		return errors.Join(ErrACPPolicyViolation, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "TIMEOUT"):
		return errors.Join(ErrACPTimeout, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "CONFLICT"):
		return errors.Join(domain.ErrConflict, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "VALIDATION"):
		return errors.Join(domain.ErrValidation, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "NOT_FOUND"):
		return errors.Join(domain.ErrNotFound, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "UNAUTHORIZED"):
		return errors.Join(domain.ErrUnauthorized, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "FORBIDDEN"):
		return errors.Join(domain.ErrForbidden, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "RATE_LIMIT"):
		return errors.Join(domain.ErrRateLimit, fmt.Errorf("acp: %s", message))
	case strings.Contains(probe, "TRANSIENT") || strings.Contains(probe, "UPSTREAM") || strings.Contains(probe, "SERVER"):
		return errors.Join(ErrACPTransient, domain.ErrUpstream, fmt.Errorf("acp: %s", message))
	default:
		return errors.Join(domain.ErrUpstream, fmt.Errorf("acp: %s", message))
	}
}
