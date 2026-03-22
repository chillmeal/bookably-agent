package acp

import "encoding/json"

type ACPStatus string

const (
	ACPStatusPending   ACPStatus = "pending"
	ACPStatusRunning   ACPStatus = "running"
	ACPStatusCompleted ACPStatus = "completed"
	ACPStatusFailed    ACPStatus = "failed"
)

type ACPRun struct {
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Steps          []ACPStep         `json:"steps"`
}

type ACPStep struct {
	Type       string        `json:"type"`
	Capability string        `json:"capability"`
	Config     ACPStepConfig `json:"config"`
}

type ACPStepConfig struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    interface{}       `json:"body,omitempty"`
}

type ACPError struct {
	Code    string          `json:"code,omitempty"`
	Type    string          `json:"type,omitempty"`
	Message string          `json:"message,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

type ACPRunResult struct {
	RunID  string          `json:"run_id,omitempty"`
	Status ACPStatus       `json:"status"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  *ACPError       `json:"error,omitempty"`
}

type RunMetadata struct {
	ChatID       string
	SpecialistID string
	Intent       string
	RiskLevel    string
	RawMessage   string
}
