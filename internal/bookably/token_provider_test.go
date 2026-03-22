package bookably

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubTokenStore struct {
	token *Token
	err   error
}

func (s *stubTokenStore) GetToken(ctx context.Context, specialistID string) (*Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.token, nil
}

func (s *stubTokenStore) SaveToken(ctx context.Context, specialistID string, token *Token) error {
	return nil
}

func TestAccessTokenProviderGetAccessToken(t *testing.T) {
	store := &stubTokenStore{
		token: &Token{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}

	provider, err := NewAccessTokenProvider(store)
	if err != nil {
		t.Fatalf("NewAccessTokenProvider: %v", err)
	}

	token, err := provider.GetAccessToken(context.Background(), "spec-1")
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if token != "access-1" {
		t.Fatalf("unexpected token: %q", token)
	}
}

func TestAccessTokenProviderPropagatesError(t *testing.T) {
	store := &stubTokenStore{
		err: errors.New("boom"),
	}
	provider, err := NewAccessTokenProvider(store)
	if err != nil {
		t.Fatalf("NewAccessTokenProvider: %v", err)
	}

	_, err = provider.GetAccessToken(context.Background(), "spec-1")
	if err == nil {
		t.Fatal("expected error")
	}
}
