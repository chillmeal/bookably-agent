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
}

func TestFormatBookingListPreviewSorted(t *testing.T) {
	now := time.Now().UTC()
	bookings := []domain.Booking{
		{ClientName: "Б", ServiceName: "Сервис", At: now.Add(2 * time.Hour)},
		{ClientName: "А", ServiceName: "Сервис", At: now.Add(time.Hour)},
	}

	out := FormatBookingListPreview(bookings, time.UTC)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %q", out)
	}
	if !strings.Contains(lines[0], "А") {
		t.Fatalf("expected sorted output, first line=%q", lines[0])
	}
}
