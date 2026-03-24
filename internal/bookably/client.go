package bookably

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chillmeal/bookably-agent/internal/actorctx"
	"github.com/chillmeal/bookably-agent/internal/domain"
	"github.com/chillmeal/bookably-agent/observability"
)

const (
	defaultBookablyTimeout     = 5 * time.Second
	maxRateLimitRetryAfterWait = 30 * time.Second

	headerBotServiceKey = "X-Bot-Service-Key"
	headerTelegramUser  = "X-Telegram-User-Id"
)

type Client struct {
	baseURL       string
	botServiceKey string
	httpClient    *http.Client
	timeout       time.Duration
	logger        *observability.Logger
}

func NewClient(baseURL, botServiceKey string, httpClient *http.Client, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("bookably client: base url is required")
	}
	if strings.TrimSpace(botServiceKey) == "" {
		return nil, errors.New("bookably client: bot service key is required")
	}
	if timeout <= 0 {
		timeout = defaultBookablyTimeout
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		botServiceKey: strings.TrimSpace(botServiceKey),
		httpClient:    httpClient,
		timeout:       timeout,
		logger:        observability.NewLogger(nil),
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
	if err := ensureReusableRequest(req); err != nil {
		return nil, err
	}

	telegramUserID, err := actorctx.TelegramUserIDFromContext(ctx)
	if err != nil {
		return nil, errors.Join(domain.ErrUnauthorized, err)
	}

	send := func() (*http.Response, error) {
		clone, cloneErr := cloneRequest(req, ctx)
		if cloneErr != nil {
			return nil, cloneErr
		}
		clone.Header.Set(headerBotServiceKey, c.botServiceKey)
		clone.Header.Set(headerTelegramUser, strconv.FormatInt(telegramUserID, 10))

		attemptCtx, cancel := context.WithTimeout(clone.Context(), c.timeout)
		defer cancel()
		clone = clone.WithContext(attemptCtx)
		resp, doErr := c.httpClient.Do(clone)
		if doErr != nil {
			return nil, fmt.Errorf("bookably client: http do: %w", doErr)
		}
		return resp, nil
	}

	resp, err := send()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}

	wait, waitErr := retryAfterDuration(resp.Header.Get("Retry-After"))
	_ = resp.Body.Close()
	if waitErr != nil {
		return nil, errors.Join(domain.ErrRateLimit, waitErr)
	}
	if wait > maxRateLimitRetryAfterWait {
		return nil, errors.Join(domain.ErrRateLimit, fmt.Errorf("retry-after %s exceeds %s", wait, maxRateLimitRetryAfterWait))
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wait):
	}

	return send()
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
