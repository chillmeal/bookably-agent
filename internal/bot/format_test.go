package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/chillmeal/bookably-agent/internal/domain"
)

func TestEscapeV2(t *testing.T) {
	got := escapeV2("Алина.")
	if got != "Алина\\." {
		t.Fatalf("escapeV2 mismatch: got %q", got)
	}
}

func TestFormatAvailabilityPreviewContainsConflictNames(t *testing.T) {
	preview := domain.Preview{
		Summary: "Сводка",
		AvailabilityChange: domain.AvailabilityChange{
			AddedSlots:   2,
			RemovedSlots: 1,
		},
		Conflicts: []domain.Conflict{
			{ClientName: "Алина", ServiceName: "Массаж", At: time.Now().UTC()},
			{ClientName: "Иван", ServiceName: "Стрижка", At: time.Now().UTC()},
		},
	}

	out := FormatAvailabilityPreview(preview)
	if !strings.Contains(out, "Алина") || !strings.Contains(out, "Иван") {
		t.Fatalf("expected both conflict names in output, got %q", out)
	}
	assertStructuredSections(t, out)
}

func TestFormatBookingListPreviewSorted(t *testing.T) {
	now := time.Now().UTC()
	bookings := []domain.Booking{
		{ClientName: "Б", ServiceName: "Сервис", At: now.Add(2 * time.Hour)},
		{ClientName: "А", ServiceName: "Сервис", At: now.Add(time.Hour)},
	}

	out := FormatBookingListPreview(bookings, time.UTC)
	idxA := strings.Index(out, "А")
	idxB := strings.Index(out, "Б")
	if idxA == -1 || idxB == -1 {
		t.Fatalf("expected both clients in output, got %q", out)
	}
	if idxA > idxB {
		t.Fatalf("expected sorted output, got %q", out)
	}
	assertStructuredSections(t, out)
}

func TestFormatErrorStructured(t *testing.T) {
	out := FormatError("upstream")
	assertStructuredSections(t, out)
	if !strings.Contains(strings.ToLower(out), "внешний сервис") {
		t.Fatalf("expected upstream wording, got %q", out)
	}
}

func TestFormatClarificationStructured(t *testing.T) {
	out := FormatClarification("Уточни дату")
	assertStructuredSections(t, out)
	if !strings.Contains(out, "Уточни дату") {
		t.Fatalf("expected question in clarification text, got %q", out)
	}
}

func assertStructuredSections(t *testing.T, out string) {
	t.Helper()
	required := []string{"*Понял:*", "*Что сделаю:*", "*Действие:*", "*Что дальше:*"}
	for _, section := range required {
		if !strings.Contains(out, section) {
			t.Fatalf("expected section %q in output: %q", section, out)
		}
	}
}
