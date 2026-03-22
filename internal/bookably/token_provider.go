package bookably

import (
	"context"
	"errors"
	"strings"
)

// AccessTokenProvider exposes read-only access token retrieval
// for runtime execution bridges that should not depend on Bookably client internals.
type AccessTokenProvider struct {
	store TokenStore
}

func NewAccessTokenProvider(store TokenStore) (*AccessTokenProvider, error) {
	if store == nil {
		return nil, errors.New("bookably token provider: token store is nil")
	}
	return &AccessTokenProvider{store: store}, nil
}

func (p *AccessTokenProvider) GetAccessToken(ctx context.Context, specialistID string) (string, error) {
	if p == nil {
		return "", errors.New("bookably token provider: nil provider")
	}
	token, err := p.store.GetToken(ctx, strings.TrimSpace(specialistID))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(token.AccessToken), nil
}
