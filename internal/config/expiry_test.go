package config

import (
	"errors"
	"testing"
	"time"
)

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if at, err := ParseExpiry("", now); at != nil || err != nil {
		t.Fatalf("empty = %v, %v", at, err)
	}
	at, err := ParseExpiry("2026-06-01", now)
	if err != nil || !at.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("date = %v, %v", at, err)
	}
	at, err = ParseExpiry("2026-06-01T10:00:00Z", now)
	if err != nil || !at.Equal(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("rfc3339 = %v, %v", at, err)
	}
	if _, err := ParseExpiry("tomorrow", now); !errors.Is(err, ErrExpiryFormat) {
		t.Fatalf("invalid = %v", err)
	}
	if _, err := ParseExpiry("2026-05-01T12:00:00Z", now); !errors.Is(err, ErrExpiryPast) {
		t.Fatalf("equal now = %v", err)
	}
	if _, err := ParseExpiry("2020-01-01", now); !errors.Is(err, ErrExpiryPast) {
		t.Fatalf("past = %v", err)
	}
}
