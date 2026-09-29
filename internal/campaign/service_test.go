package campaign

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestNormalizeRecipients(t *testing.T) {
	valid, skipped := NormalizeRecipients([]string{
		" Bob@Example.com ", "bob@example.com", "Alice <alice@example.org>",
		"not-an-email", "", "x@localhost",
	})

	if want := []string{"bob@example.com", "alice@example.org"}; !slices.Equal(valid, want) {
		t.Errorf("valid = %v, want %v", valid, want)
	}
	if want := []string{"not-an-email", "x@localhost"}; !slices.Equal(skipped, want) {
		t.Errorf("skipped = %v, want %v", skipped, want)
	}
}

func TestParseSchedule(t *testing.T) {
	got, err := ParseSchedule("2026-10-01", "09:30", "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if got, err := ParseSchedule("", "", ""); got != nil || err != nil {
		t.Errorf("empty: got %v, %v", got, err)
	}

	var vErr *ValidationError
	for _, tc := range [][3]string{
		{"2026-10-01", "", "UTC"},
		{"2026-10-01", "25:00", "UTC"},
		{"2026-10-01", "09:30", "Mars/Olympus"},
	} {
		if _, err := ParseSchedule(tc[0], tc[1], tc[2]); !errors.As(err, &vErr) {
			t.Errorf("%v: got %v, want ValidationError", tc, err)
		}
	}
}
