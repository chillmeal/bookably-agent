package bookably

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/observability"
	"github.com/redis/go-redis/v9"
)

const (
	defaultBookablyTimeout = 5 * time.Second
	defaultTokenTTL        = time.Hour
	refreshLeadTime        = 60 * time.Second
	refreshLockTTL         = 10 * time.Second
	maxRateLimitWait       = 30 * time.Second
)

type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (t Token) TTL() time.Duration {
	if t.ExpiresAt.IsZero() {
		return defaultTokenTTL
	}
	remaining := time.Until(t.ExpiresAt.Add(-refreshLeadTime))
	if remaining <= 0 {
		return time.Second
	}
	return remaining
}

type TokenStore interface {
	GetToken(ctx context.Context, specialistID string) (*Token, error)
	SaveToken(ctx context.Context, specialistID string, token *Token) error
}

type refreshLocker interface {
	AcquireRefreshLock(ctx context.Context, specialistID string, ttl time.Duration) (bool, error)
	ReleaseRefreshLock(ctx context.Context, specialistID string) error
}

type RedisTokenStore struct {
	client     redis.Cmdable
	defaultTTL time.Duration
}

func NewRedisTokenStore(client redis.Cmdable, defaultTTL time.Duration) (*RedisTokenStore, error) {
	if client == nil {
		return nil, errors.New("bookably token store: redis client is nil")
	}
	if defaultTTL <= 0 {
		defaultTTL = defaultTokenTTL
	}
	return &RedisTokenStore{client: client, defaultTTL: defaultTTL}, nil
}

func (s *RedisTokenStore) GetToken(ctx context.Context, specialistID string) (*Token, error) {
	if strings.TrimSpace(specialistID) == "" {
		return nil, errors.Join(domain.ErrUnauthorized, errors.New("bookably token store: specialist id is required"))
	}

	payload, err := s.client.Get(ctx, tokenKey(specialistID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.Join(domain.ErrUnauthorized, fmt.Errorf("bookably token store: token not found for specialist %s", specialistID))
		}
		return nil, fmt.Errorf("bookably token store: get token: %w", err)
	}

	var token Token
	if err := json.Unmarshal(payload, &token); err != nil {
		return nil, fmt.Errorf("bookably token store: unmarshal token: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.Join(domain.ErrUnauthorized, errors.New("bookably token store: access token is empty"))
	}
	return &token, nil
}

func (s *RedisTokenStore) SaveToken(ctx context.Context, specialistID string, token *Token) error {
	if strings.TrimSpace(specialistID) == "" {
		return errors.New("bookably token store: specialist id is required")
	}
	if token == nil {
		return errors.New("bookably token store: token is nil")
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return errors.New("bookably token store: access token is required")
	}

	payload, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("bookably token store: marshal token: %w", err)
	}

	ttl := token.TTL()
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	if err := s.client.Set(ctx, tokenKey(specialistID), payload, ttl).Err(); err != nil {
		return fmt.Errorf("bookably token store: save token: %w", err)
	}
	return nil
}

func (s *RedisTokenStore) AcquireRefreshLock(ctx context.Context, specialistID string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = refreshLockTTL
	}
	ok, err := s.client.SetNX(ctx, tokenLockKey(specialistID), "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("bookably token store: acquire lock: %w", err)
	}
	return ok, nil
}

func (s *RedisTokenStore) ReleaseRefreshLock(ctx context.Context, specialistID string) error {
	if err := s.client.Del(ctx, tokenLockKey(specialistID)).Err(); err != nil {
		return fmt.Errorf("bookably token store: release lock: %w", err)
	}
	return nil
}

func tokenKey(specialistID string) string {
	return tokenKeyPrefix + specialistID
}

func tokenLockKey(specialistID string) string {
	return tokenKeyPrefix + specialistID + ":lock"
}

type Client struct {
	baseURL      string
	specialistID string
	tokenStore   TokenStore
	httpClient   *http.Client
	timeout      time.Duration
	logger       *observability.Logger
}

func NewClient(baseURL, specialistID string, tokenStore TokenStore, httpClient *http.Client, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("bookably client: base url is required")
	}
	if strings.TrimSpace(specialistID) == "" {
		return nil, errors.New("bookably client: specialist id is required")
	}
	if tokenStore == nil {
		return nil, errors.New("bookably client: token store is nil")
	}
	if timeout <= 0 {
		timeout = defaultBookablyTimeout
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		specialistID: specialistID,
		tokenStore:   tokenStore,
		httpClient:   httpClient,
		timeout:      timeout,
		logger:       observability.NewLogger(nil),
	}, nil
}

func (c *Client) SetLogger(logger *observability.Logger) {
	if c == nil || logger == nil {
		return
	}
	c.logger = logger
}

func (c *Client) buildURL(path string, q url.Values) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("bookably client: request path is required")
	}

	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("bookably client: parse url: %w", err)
	}
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) NewRequest(ctx context.Context, method, path string, q url.Values, body []byte) (*http.Request, error) {
	fullURL, err := c.buildURL(path, q)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, fmt.Errorf("bookably client: build request: %w", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) GetJSON(ctx context.Context, path string, q url.Values, out interface{}) error {
	req, err := c.NewRequest(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bookably client: read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return mapHTTPError(resp.StatusCode, payload)
	}

	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("bookably client: decode response: %w", err)
	}
	return nil
}

func (c *Client) PostJSON(ctx context.Context, path string, body interface{}, idempotencyKey string, out interface{}) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("bookably client: encode body: %w", err)
	}

	req, err := c.NewRequest(ctx, http.MethodPost, path, nil, encoded)
	if err != nil {
		return err
	}
	if strings.TrimSpace(idempotencyKey) != "" {
		req.Header.Set("Idempotency-Key", strings.TrimSpace(idempotencyKey))
	}

	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bookably client: read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return mapHTTPError(resp.StatusCode, payload)
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("bookably client: decode response: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("bookably client: nil request")
	}
	started := time.Now()
	if err := ensureReusableRequest(req); err != nil {
		c.logError(started, "ErrValidation", err, req, map[string]any{"stage": "ensure_reusable_request"})
		return nil, err
	}

	token, err := c.tokenStore.GetToken(ctx, c.specialistID)
	if err != nil {
		c.logError(started, "ErrUnauthorized", err, req, map[string]any{"stage": "get_token"})
		return nil, err
	}

	resp, err := c.sendWithToken(ctx, req, token.AccessToken)
	if err != nil {
		c.logError(started, "ErrUpstream", err, req, map[string]any{"stage": "send_with_token"})
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()

		refreshed, refreshErr := c.refreshTokenWithLock(ctx, token)
		if refreshErr != nil {
			c.logError(started, "ErrUnauthorized", refreshErr, req, map[string]any{"stage": "refresh_token"})
			return nil, errors.Join(domain.ErrUnauthorized, refreshErr)
		}
		second, secondErr := c.sendWithToken(ctx, req, refreshed.AccessToken)
		if secondErr != nil {
			c.logError(started, "ErrUpstream", secondErr, req, map[string]any{"stage": "send_with_refreshed_token"})
			return nil, secondErr
		}
		if second.StatusCode == http.StatusUnauthorized {
			_ = second.Body.Close()
			c.logError(started, "ErrUnauthorized", errors.New("unauthorized after refresh retry"), req, map[string]any{"stage": "post_refresh_unauthorized"})
			return nil, errors.Join(domain.ErrUnauthorized, errors.New("bookably client: unauthorized after refresh retry"))
		}
		return second, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		wait, waitErr := retryAfterDuration(resp.Header.Get("Retry-After"))
		_ = resp.Body.Close()
		if waitErr != nil {
			c.logError(started, "ErrRateLimit", waitErr, req, map[string]any{"stage": "parse_retry_after"})
			return nil, errors.Join(domain.ErrRateLimit, waitErr)
		}
		if wait > maxRateLimitWait {
			c.logError(started, "ErrRateLimit", fmt.Errorf("retry-after %s exceeds %s", wait, maxRateLimitWait), req, map[string]any{"stage": "retry_after_too_large"})
			return nil, errors.Join(domain.ErrRateLimit, fmt.Errorf("retry-after %s exceeds %s", wait, maxRateLimitWait))
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		second, secondErr := c.sendWithToken(ctx, req, token.AccessToken)
		if secondErr != nil {
			c.logError(started, "ErrUpstream", secondErr, req, map[string]any{"stage": "send_after_rate_limit_wait"})
			return nil, secondErr
		}
		if second.StatusCode == http.StatusTooManyRequests {
			_ = second.Body.Close()
			c.logError(started, "ErrRateLimit", errors.New("rate limit persisted after retry"), req, map[string]any{"stage": "rate_limit_persisted"})
			return nil, errors.Join(domain.ErrRateLimit, errors.New("bookably client: rate limit persisted after retry"))
		}
		return second, nil
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		payload, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		mapped := mapHTTPError(resp.StatusCode, payload)
		c.logError(started, "ErrUpstream", mapped, req, map[string]any{"stage": "upstream_5xx", "status_code": resp.StatusCode})
		return nil, mapped
	}

	return resp, nil
}

func (c *Client) sendWithToken(ctx context.Context, req *http.Request, accessToken string) (*http.Response, error) {
	clone, err := cloneRequest(req, ctx)
	if err != nil {
		return nil, err
	}
	clone.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))

	attemptCtx, cancel := context.WithTimeout(clone.Context(), c.timeout)
	defer cancel()
	clone = clone.WithContext(attemptCtx)

	resp, err := c.httpClient.Do(clone)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("bookably client: http do: %w", err)
	}
	return resp, nil
}

func (c *Client) refreshTokenWithLock(ctx context.Context, stale *Token) (*Token, error) {
	if stale == nil || strings.TrimSpace(stale.RefreshToken) == "" {
		return nil, errors.New("bookably client: refresh token is empty")
	}

	if locker, ok := c.tokenStore.(refreshLocker); ok {
		locked, err := locker.AcquireRefreshLock(ctx, c.specialistID, refreshLockTTL)
		if err != nil {
			return nil, err
		}
		if locked {
			defer func() { _ = locker.ReleaseRefreshLock(context.Background(), c.specialistID) }()
			fresh, err := c.refreshToken(ctx, stale.RefreshToken)
			if err != nil {
				return nil, err
			}
			if err := c.tokenStore.SaveToken(ctx, c.specialistID, fresh); err != nil {
				return nil, err
			}
			return fresh, nil
		}

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			current, getErr := c.tokenStore.GetToken(ctx, c.specialistID)
			if getErr == nil && current != nil && strings.TrimSpace(current.AccessToken) != "" && current.AccessToken != stale.AccessToken {
				return current, nil
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	fresh, err := c.refreshToken(ctx, stale.RefreshToken)
	if err != nil {
		return nil, err
	}
	if err := c.tokenStore.SaveToken(ctx, c.specialistID, fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (c *Client) refreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	body, err := json.Marshal(map[string]string{"refreshToken": refreshToken})
	if err != nil {
		return nil, fmt.Errorf("bookably client: marshal refresh body: %w", err)
	}

	req, err := c.NewRequest(ctx, http.MethodPost, endpointAuthRefresh, nil, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.sendNoAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bookably client: read refresh response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, mapHTTPError(resp.StatusCode, payload)
	}

	var out struct {
		AccessToken  string    `json:"accessToken"`
		RefreshToken string    `json:"refreshToken"`
		ExpiresAt    time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("bookably client: decode refresh response: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, errors.New("bookably client: refresh response missing accessToken")
	}
	if strings.TrimSpace(out.RefreshToken) == "" {
		out.RefreshToken = refreshToken
	}
	return &Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresAt: out.ExpiresAt}, nil
}

func (c *Client) AcquireTokenWithInitData(ctx context.Context, initData string) (*Token, error) {
	if strings.TrimSpace(initData) == "" {
		return nil, errors.New("bookably client: initData is required")
	}

	body, err := json.Marshal(map[string]string{"initData": initData})
	if err != nil {
		return nil, fmt.Errorf("bookably client: marshal tma body: %w", err)
	}

	req, err := c.NewRequest(ctx, http.MethodPost, endpointAuthTMA, nil, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.sendNoAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bookably client: read tma response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, mapHTTPError(resp.StatusCode, payload)
	}

	var out struct {
		AccessToken  string    `json:"accessToken"`
		RefreshToken string    `json:"refreshToken"`
		ExpiresAt    time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("bookably client: decode tma response: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, errors.New("bookably client: tma response missing accessToken")
	}

	token := &Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresAt: out.ExpiresAt}
	if err := c.tokenStore.SaveToken(ctx, c.specialistID, token); err != nil {
		return nil, err
	}
	return token, nil
}

func (c *Client) sendNoAuth(ctx context.Context, req *http.Request) (*http.Response, error) {
	clone, err := cloneRequest(req, ctx)
	if err != nil {
		return nil, err
	}

	attemptCtx, cancel := context.WithTimeout(clone.Context(), c.timeout)
	defer cancel()
	clone = clone.WithContext(attemptCtx)

	resp, err := c.httpClient.Do(clone)
	if err != nil {
		return nil, fmt.Errorf("bookably client: http do no auth: %w", err)
	}
	return resp, nil
}

func ensureReusableRequest(req *http.Request) error {
	if req.GetBody != nil || req.Body == nil {
		return nil
	}

	payload, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("bookably client: read request body: %w", err)
	}
	_ = req.Body.Close()

	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	req.ContentLength = int64(len(payload))
	return nil
}

func cloneRequest(req *http.Request, ctx context.Context) (*http.Request, error) {
	clone := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("bookably client: clone body: %w", err)
		}
		clone.Body = body
	}
	return clone, nil
}

func retryAfterDuration(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Second, nil
	}

	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid Retry-After %q", raw)
	}
	if seconds <= 0 {
		return time.Second, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

func (c *Client) logError(started time.Time, errType string, err error, req *http.Request, fields map[string]any) {
	if c == nil || c.logger == nil {
		return
	}
	payload := map[string]any{}
	for k, v := range fields {
		payload[k] = v
	}
	if req != nil && req.URL != nil {
		payload["path"] = req.URL.Path
		payload["method"] = req.Method
	}
	payload["specialist_id"] = c.specialistID

	c.logger.LogError(observability.Entry{
		TraceID:    tokenSafeTraceID(),
		ChatID:     0,
		Intent:     "",
		Component:  "bookably/client",
		DurationMS: time.Since(started).Milliseconds(),
		ErrorType:  errType,
		Error:      err,
		Fields:     payload,
	})
}

func tokenSafeTraceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}
