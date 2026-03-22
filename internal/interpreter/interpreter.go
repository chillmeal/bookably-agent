package interpreter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/llm"
)

const (
	defaultInterpretTimeout = 15 * time.Second
	maxHistoryTurns         = 10
)

type Turn struct {
	Role    string
	Content string
}

type ConversationContext struct {
	Timezone string
	History  []Turn
}

type Interpreter struct {
	llmClient  llm.LLMClient
	promptPath string
	timeout    time.Duration
}

func New(llmClient llm.LLMClient, promptPath string, timeout time.Duration) (*Interpreter, error) {
	if llmClient == nil {
		return nil, fmt.Errorf("interpreter: llm client is nil")
	}
	if strings.TrimSpace(promptPath) == "" {
		return nil, fmt.Errorf("interpreter: prompt path is required")
	}
	if timeout <= 0 {
		timeout = defaultInterpretTimeout
	}

	return &Interpreter{
		llmClient:  llmClient,
		promptPath: promptPath,
		timeout:    timeout,
	}, nil
}

func (i *Interpreter) Interpret(ctx context.Context, userMessage string, convo ConversationContext) (*ActionPlan, error) {
	if i == nil {
		return nil, fmt.Errorf("interpreter: interpreter is nil")
	}
	trimmedUserMessage := strings.TrimSpace(userMessage)
	if trimmedUserMessage == "" {
		return nil, fmt.Errorf("interpreter: user message is empty")
	}
	if isPromptInjection(trimmedUserMessage) {
		zone := strings.TrimSpace(convo.Timezone)
		if zone == "" {
			zone = "UTC"
		}
		return &ActionPlan{
			Intent:          IntentUnknown,
			Confidence:      0,
			RequiresConfirm: false,
			RawUserMessage:  trimmedUserMessage,
			Timezone:        zone,
		}, nil
	}

	tz, err := loadTimezone(convo.Timezone)
	if err != nil {
		return nil, err
	}

	systemPrompt, err := loadSystemPrompt(i.promptPath, tz)
	if err != nil {
		return nil, err
	}

	messages := buildMessages(systemPrompt, convo.History, trimmedUserMessage)
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	completion, err := i.llmClient.Complete(callCtx, messages)
	if err != nil {
		return nil, fmt.Errorf("interpreter: llm complete: %w", err)
	}

	plan, err := ParseActionPlan(completion.Content)
	if err != nil {
		return nil, fmt.Errorf("interpreter: parse action plan: %w", err)
	}

	plan.RawUserMessage = trimmedUserMessage
	plan.Timezone = tz.String()
	plan.RequiresConfirm = plan.Intent.RequiresConfirm()
	return plan, nil
}

func loadTimezone(raw string) (*time.Location, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("interpreter: timezone is required")
	}
	loc, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("interpreter: invalid timezone %q: %w", value, err)
	}
	return loc, nil
}

func buildMessages(systemPrompt string, history []Turn, userMessage string) []llm.Message {
	historySlice := history
	if len(historySlice) > maxHistoryTurns {
		historySlice = historySlice[len(historySlice)-maxHistoryTurns:]
	}

	messages := make([]llm.Message, 0, 2+len(historySlice))
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	for _, turn := range historySlice {
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}
		role := normalizeRole(turn.Role)
		messages = append(messages, llm.Message{Role: role, Content: content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: userMessage})
	return messages
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "assistant"
	case "system":
		return "system"
	default:
		return "user"
	}
}

func isPromptInjection(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}

	markers := []string{
		"ignore previous instructions",
		"ignore all previous instructions",
		"forget previous instructions",
		"you are now system",
		"system prompt",
		"role: system",
		`"role":"system"`,
		"developer message",
		"override safety rules",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return looksLikeRawCommandBlob(normalized)
}

func looksLikeRawCommandBlob(message string) bool {
	if !strings.Contains(message, "{") || !strings.Contains(message, "}") {
		return false
	}
	score := 0
	for _, key := range []string{
		`"intent"`,
		`"confidence"`,
		`"requires_confirmation"`,
		`"params"`,
	} {
		if strings.Contains(message, key) {
			score++
		}
	}
	return score >= 2
}
