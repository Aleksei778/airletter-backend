package campaign

import (
	"errors"
	"slices"
	"testing"
	"time"

	"airletter/internal/config"
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

func TestUnsubscribeToken(t *testing.T) {
	key := []byte("k1")
	tok := UnsubscribeToken(key, 42)
	if id, err := ParseUnsubscribeToken(key, tok); err != nil || id != 42 {
		t.Fatalf("parse = %d, %v", id, err)
	}
	if _, err := ParseUnsubscribeToken([]byte("k2"), tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("other key: %v", err)
	}
	forged := UnsubscribeToken(key, 43)[:11] + tok[11:]
	for _, bad := range []string{"", "abc", forged, tok + "A"} {
		if _, err := ParseUnsubscribeToken(key, bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSendInterval(t *testing.T) {
	cfg := &config.Config{SendIntervalSeconds: 3, MaxSendIntervalSeconds: 3600}
	tests := []struct {
		requested int
		want      int
		ok        bool
	}{
		{requested: 0, want: 3, ok: true},
		{requested: 3, want: 3, ok: true},
		{requested: 90, want: 90, ok: true},
		{requested: 3600, want: 3600, ok: true},
		{requested: 2, ok: false},
		{requested: -5, ok: false},
		{requested: 3601, ok: false},
	}
	for _, tt := range tests {
		got, err := SendInterval(tt.requested, cfg)
		var vErr *ValidationError
		if tt.ok && (err != nil || got != tt.want) {
			t.Errorf("SendInterval(%d) = %d, %v; want %d", tt.requested, got, err, tt.want)
		}
		if !tt.ok && !errors.As(err, &vErr) {
			t.Errorf("SendInterval(%d) error = %v; want ValidationError", tt.requested, err)
		}
	}
}
