package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultTelegramBaseURL = "https://api.telegram.org"
	defaultHTTPTimeout     = 10 * time.Second
)

type Streamer struct {
	token      string
	baseURL    string
	httpClient *http.Client
	draftMu    sync.Mutex
	// progressMessageIDs stores fallback progress message id by chat.
	// Used when sendMessageDraft is unavailable/rate-limited for this bot.
	progressMessageIDs map[int64]int64
}

type draftRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type finalizeRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type telegramEnvelope struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type telegramMessage struct {
	MessageID int64 `json:"message_id"`
}

func NewStreamer(token string, httpClient *http.Client, baseURL string) (*Streamer, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("bot streamer: token is required")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultTelegramBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Streamer{
		token:      strings.TrimSpace(token),
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
		progressMessageIDs: make(map[int64]int64),
	}, nil
}

func (s *Streamer) Draft(ctx context.Context, chatID int64, text string) error {
	if s == nil {
		return errors.New("bot streamer: nil streamer")
	}

	request := draftRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "MarkdownV2",
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()

	status, body, err := s.postJSON(ctx, "sendMessageDraft", request)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		if fallbackErr := s.upsertProgressMessage(ctx, chatID, text); fallbackErr == nil {
			return nil
		}
		return buildTelegramError("sendMessageDraft", status, body)
	}

	envelope, err := parseTelegramEnvelope(body)
	if err != nil {
		return fmt.Errorf("bot streamer: sendMessageDraft parse response: %w", err)
	}
	if !envelope.OK {
		if fallbackErr := s.upsertProgressMessage(ctx, chatID, text); fallbackErr == nil {
			return nil
		}
		return fmt.Errorf("bot streamer: sendMessageDraft rejected: %s", strings.TrimSpace(envelope.Description))
	}

	return nil
}

func (s *Streamer) Finalize(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	if s == nil {
		return 0, errors.New("bot streamer: nil streamer")
	}

	s.draftMu.Lock()
	progressMsgID := s.progressMessageIDs[chatID]
	s.draftMu.Unlock()

	if progressMsgID != 0 {
		msgID, status, body, err := s.editMessageText(ctx, chatID, progressMsgID, text, keyboard)
		if err == nil {
			s.clearProgressMessage(chatID)
			return msgID, nil
		}
		if status == http.StatusBadRequest && markupHasStyle(keyboard) && isStyleRelatedError(body) {
			msgID, _, _, retryErr := s.editMessageText(ctx, chatID, progressMsgID, text, StripKeyboardStyles(keyboard))
			if retryErr == nil {
				s.clearProgressMessage(chatID)
				return msgID, nil
			}
		}
	}

	request := finalizeRequest{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	}

	messageID, status, body, err := s.sendMessage(ctx, request)
	if err == nil {
		return messageID, nil
	}

	// Bot API style support may differ; retry once without style fields.
	if status == http.StatusBadRequest && markupHasStyle(keyboard) && isStyleRelatedError(body) {
		request.ReplyMarkup = StripKeyboardStyles(keyboard)
		messageID, _, _, retryErr := s.sendMessage(ctx, request)
		if retryErr == nil {
			return messageID, nil
		}
		return 0, retryErr
	}

	return 0, err
}

type editMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

func (s *Streamer) sendMessage(ctx context.Context, request finalizeRequest) (int64, int, []byte, error) {
	status, body, err := s.postJSON(ctx, "sendMessage", request)
	if err != nil {
		return 0, status, body, err
	}
	if status != http.StatusOK {
		return 0, status, body, buildTelegramError("sendMessage", status, body)
	}

	envelope, err := parseTelegramEnvelope(body)
	if err != nil {
		return 0, status, body, fmt.Errorf("bot streamer: sendMessage parse response: %w", err)
	}
	if !envelope.OK {
		return 0, status, body, fmt.Errorf("bot streamer: sendMessage rejected: %s", strings.TrimSpace(envelope.Description))
	}

	var message telegramMessage
	if err := json.Unmarshal(envelope.Result, &message); err != nil {
		return 0, status, body, fmt.Errorf("bot streamer: sendMessage parse result: %w", err)
	}
	if message.MessageID == 0 {
		return 0, status, body, errors.New("bot streamer: sendMessage response missing message_id")
	}

	return message.MessageID, status, body, nil
}

func (s *Streamer) editMessageText(ctx context.Context, chatID, messageID int64, text string, keyboard *InlineKeyboardMarkup) (int64, int, []byte, error) {
	request := editMessageRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	}
	status, body, err := s.postJSON(ctx, "editMessageText", request)
	if err != nil {
		return 0, status, body, err
	}
	if status != http.StatusOK {
		return 0, status, body, buildTelegramError("editMessageText", status, body)
	}

	envelope, err := parseTelegramEnvelope(body)
	if err != nil {
		return 0, status, body, fmt.Errorf("bot streamer: editMessageText parse response: %w", err)
	}
	if !envelope.OK {
		return 0, status, body, fmt.Errorf("bot streamer: editMessageText rejected: %s", strings.TrimSpace(envelope.Description))
	}
	if len(envelope.Result) == 0 {
		return messageID, status, body, nil
	}
	var message telegramMessage
	if err := json.Unmarshal(envelope.Result, &message); err != nil {
		return messageID, status, body, nil
	}
	if message.MessageID == 0 {
		return messageID, status, body, nil
	}
	return message.MessageID, status, body, nil
}

func (s *Streamer) upsertProgressMessage(ctx context.Context, chatID int64, text string) error {
	progressID := s.progressMessageIDs[chatID]
	if progressID == 0 {
		request := finalizeRequest{
			ChatID:    chatID,
			Text:      text,
			ParseMode: "MarkdownV2",
		}
		msgID, _, _, err := s.sendMessage(ctx, request)
		if err != nil {
			return err
		}
		s.progressMessageIDs[chatID] = msgID
		return nil
	}
	_, _, _, err := s.editMessageText(ctx, chatID, progressID, text, nil)
	if err != nil {
		// If progress message was removed, try create a fresh one.
		if strings.Contains(strings.ToLower(err.Error()), "message to edit not found") {
			delete(s.progressMessageIDs, chatID)
			return s.upsertProgressMessage(ctx, chatID, text)
		}
		return err
	}
	return nil
}

func (s *Streamer) clearProgressMessage(chatID int64) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	delete(s.progressMessageIDs, chatID)
}

func (s *Streamer) postJSON(ctx context.Context, method string, payload interface{}) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("bot streamer: marshal %s payload: %w", method, err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", s.baseURL, s.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("bot streamer: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("bot streamer: %s http: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("bot streamer: read %s response: %w", method, err)
	}

	return resp.StatusCode, respBody, nil
}

func parseTelegramEnvelope(payload []byte) (*telegramEnvelope, error) {
	var out telegramEnvelope
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func buildTelegramError(method string, status int, payload []byte) error {
	envelope := &telegramEnvelope{}
	_ = json.Unmarshal(payload, envelope)
	if strings.TrimSpace(envelope.Description) != "" {
		return fmt.Errorf("bot streamer: %s status %d: %s", method, status, strings.TrimSpace(envelope.Description))
	}
	return fmt.Errorf("bot streamer: %s status %d: %s", method, status, strings.TrimSpace(string(payload)))
}

func isStyleRelatedError(payload []byte) bool {
	lower := strings.ToLower(string(payload))
	return strings.Contains(lower, "style")
}
