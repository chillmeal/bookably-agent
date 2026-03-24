package llm

import (
	"context"
	"errors"
	"strings"
)

const stubUnknownActionPlan = `{"intent":"unknown","confidence":1.0,"requires_confirmation":false,"clarifications":[],"params":{}}`

type StubClient struct {
	response string
}

func NewStubClient() *StubClient {
	return &StubClient{response: stubUnknownActionPlan}
}

func (c *StubClient) Complete(ctx context.Context, _ []Message) (*Completion, error) {
	if c == nil {
		return nil, errors.New("stub: client is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Completion{
		Content:      strings.TrimSpace(c.response),
		InputTokens:  0,
		OutputTokens: 0,
	}, nil
}
