package llm

import (
	"context"
	"errors"
	"testing"
)

func TestStubClientComplete(t *testing.T) {
	client := NewStubClient()
	got, err := client.Complete(context.Background(), nil)
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if got.Content != stubUnknownActionPlan {
		t.Fatalf("unexpected stub content: %q", got.Content)
	}
}

func TestStubClientCompleteCanceledContext(t *testing.T) {
	client := NewStubClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Complete(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
