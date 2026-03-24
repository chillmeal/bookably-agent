//go:build integration

package interpreter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/llm"
)

type classifierCase struct {
	id                  int
	input               string
	expectedIntent      Intent
	minConfidence       float64
	expectClarification bool
}

func TestClassifier(t *testing.T) {
	provider := strings.TrimSpace(os.Getenv("LLM_PROVIDER"))
	apiKey := strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	if provider == "" || apiKey == "" {
		t.Skip("integration test requires LLM_PROVIDER and LLM_API_KEY")
	}

	promptPath, err := resolvePromptPath()
	if err != nil {
		t.Skipf("integration test skipped: %v", err)
	}

	interpTimeout := loadIntegrationTimeout()
	client, err := newIntegrationLLMClient(provider, apiKey, strings.TrimSpace(os.Getenv("LLM_MODEL")), interpTimeout)
	if err != nil {
		t.Fatalf("build llm client: %v", err)
	}

	interp, err := New(client, promptPath, interpTimeout)
	if err != nil {
		t.Fatalf("new interpreter: %v", err)
	}

	tzName := "Europe/Berlin"
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}

	originalNow := promptNowFunc
	promptNowFunc = func() time.Time {
		return time.Date(2026, 3, 22, 10, 0, 0, 0, tz)
	}
	t.Cleanup(func() {
		promptNowFunc = originalNow
	})

	cases := []classifierCase{
		{id: 1, input: "На следующей неделе работаю с 12 до 20, кроме пятницы", expectedIntent: IntentSetWorkingHours, minConfidence: 0.50},
		{id: 2, input: "Со вторника по четверг ставь мне с 10 до 18", expectedIntent: IntentSetWorkingHours, minConfidence: 0.50},
		{id: 3, input: "С 24 по 28 марта работаю с 9 утра до 7 вечера", expectedIntent: IntentSetWorkingHours, minConfidence: 0.50},
		{id: 4, input: "Следующая неделя - рабочая, часы как обычно", expectedIntent: IntentSetWorkingHours, minConfidence: 0.50, expectClarification: true},
		{id: 5, input: "Работаю завтра с 12", expectedIntent: IntentSetWorkingHours, minConfidence: 0.50},
		{id: 6, input: "Добавь обед 13-14 на всю эту неделю", expectedIntent: IntentAddBreak, minConfidence: 0.50},
		{id: 7, input: "Каждый день в 15 делай перерыв на час", expectedIntent: IntentAddBreak, minConfidence: 0.50},
		{id: 8, input: "Добавь перерыв утром", expectedIntent: IntentAddBreak, minConfidence: 0.50, expectClarification: true},
		{id: 9, input: "Закрой пятницу 28 марта", expectedIntent: IntentCloseRange, minConfidence: 0.50},
		{id: 10, input: "Закрой утро в среду", expectedIntent: IntentCloseRange, minConfidence: 0.50, expectClarification: true},
		{id: 11, input: "Закрой вечер в среду", expectedIntent: IntentCloseRange, minConfidence: 0.50, expectClarification: true},
		{id: 12, input: "Не работаю в мае с 1 по 8", expectedIntent: IntentCloseRange, minConfidence: 0.50},
		{id: 13, input: "Покажи мои записи на завтра", expectedIntent: IntentListBookings, minConfidence: 0.50},
		{id: 14, input: "Что у меня сегодня?", expectedIntent: IntentListBookings, minConfidence: 0.50},
		{id: 15, input: "Покажи записи на эту неделю", expectedIntent: IntentListBookings, minConfidence: 0.50},
		{id: 16, input: "Кто записан на понедельник?", expectedIntent: IntentListBookings, minConfidence: 0.50},
		{id: 17, input: "Запиши Алину на массаж 60 мин завтра после 18", expectedIntent: IntentCreateBooking, minConfidence: 0.50},
		{id: 18, input: "Создай запись: Марина, маникюр, в пятницу", expectedIntent: IntentCreateBooking, minConfidence: 0.50},
		{id: 19, input: "Запиши Ивана на стрижку как можно скорее", expectedIntent: IntentCreateBooking, minConfidence: 0.50},
		{id: 20, input: "Запиши Катю на массаж в среду", expectedIntent: IntentCreateBooking, minConfidence: 0.50},
		{id: 21, input: "Запиши Марину на процедуру в пятницу", expectedIntent: IntentCreateBooking, minConfidence: 0.50, expectClarification: true},
		{id: 22, input: "Запиши кого-нибудь на вечер", expectedIntent: IntentCreateBooking, minConfidence: 0.50, expectClarification: true},
		{id: 23, input: "Отмени запись Ивана в четверг", expectedIntent: IntentCancelBooking, minConfidence: 0.50},
		{id: 24, input: "Отмени Марину", expectedIntent: IntentCancelBooking, minConfidence: 0.50},
		{id: 25, input: "Удали бронь Алёши", expectedIntent: IntentCancelBooking, minConfidence: 0.50},
		{id: 26, input: "Найди ближайшее окно для маникюра 90 минут после 16", expectedIntent: IntentFindNextSlot, minConfidence: 0.50},
		{id: 27, input: "Есть место на массаж завтра?", expectedIntent: IntentFindNextSlot, minConfidence: 0.50},
		{id: 28, input: "Когда ближайшее окно?", expectedIntent: IntentFindNextSlot, minConfidence: 0.50, expectClarification: true},
		{id: 29, input: "Когда следующий свободный час для стрижки?", expectedIntent: IntentFindNextSlot, minConfidence: 0.50},
		{id: 30, input: "Привет!", expectedIntent: IntentUnknown, minConfidence: 0.0},
		{id: 31, input: "Как дела?", expectedIntent: IntentUnknown, minConfidence: 0.0},
		{id: 32, input: "Сколько стоит массаж?", expectedIntent: IntentUnknown, minConfidence: 0.0},
		{id: 33, input: "Позвони клиенту", expectedIntent: IntentUnknown, minConfidence: 0.0},
		{id: 34, input: "12345", expectedIntent: IntentUnknown, minConfidence: 0.0},
		{id: 35, input: "Запиши всех клиентов которые есть", expectedIntent: IntentUnknown, minConfidence: 0.0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("case_%02d", tc.id), func(t *testing.T) {
			// Keep per-case context longer than interpreter timeout so the harness
			// does not cut requests prematurely under variable provider latency.
			ctx, cancel := context.WithTimeout(context.Background(), interpTimeout+10*time.Second)
			defer cancel()

			plan, err := interp.Interpret(ctx, tc.input, ConversationContext{
				Timezone: tzName,
				History:  nil,
			})
			if err != nil {
				t.Fatalf("interpret: %v", err)
			}

			if plan.Intent != tc.expectedIntent {
				t.Fatalf("expected intent %q, got %q (confidence=%.2f)", tc.expectedIntent, plan.Intent, plan.Confidence)
			}
			if plan.Confidence < tc.minConfidence {
				t.Fatalf("expected confidence >= %.2f, got %.2f", tc.minConfidence, plan.Confidence)
			}
			if len(plan.Clarifications) > 1 {
				t.Fatalf("expected at most one clarification, got %d", len(plan.Clarifications))
			}
			if tc.expectClarification && len(plan.Clarifications) != 1 {
				t.Fatalf("expected exactly one clarification, got %d", len(plan.Clarifications))
			}
		})
	}
}

func loadIntegrationTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("LLM_INTEGRATION_TIMEOUT_SEC"))
	if raw == "" {
		return 40 * time.Second
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec <= 0 {
		return 40 * time.Second
	}
	return time.Duration(sec) * time.Second
}

func newIntegrationLLMClient(provider, apiKey, model string, timeout time.Duration) (llm.LLMClient, error) {
	opts := llm.ClientOptions{Model: model, Timeout: timeout}

	switch provider {
	case "openrouter":
		return llm.NewOpenRouterClient(apiKey, opts)
	default:
		return nil, fmt.Errorf("unsupported LLM_PROVIDER %q", provider)
	}
}

func resolvePromptPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	candidates := []string{
		filepath.Join(wd, "..", "..", "prompts", "intent_classifier.md"),
		filepath.Join(wd, "prompts", "intent_classifier.md"),
		filepath.Join("prompts", "intent_classifier.md"),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("prompt file not found in expected locations")
}
