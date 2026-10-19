package config

import (
	"errors"
	"time"
)

// Expiry errors let each caller word the refusal in its own surface.
var (
	ErrExpiryFormat = errors.New("expiry is neither a date as YYYY-MM-DD nor an RFC 3339 time")
	ErrExpiryPast   = errors.New("expiry does not lie ahead of now")
)

// ParseExpiry reads an expiry: empty for none, else a date, meaning its start in local time, or an RFC 3339
// time. It must lie after now, which the caller supplies.
func ParseExpiry(text string, now time.Time) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation("2006-01-02", text, time.Local)
	if err != nil {
		if at, err = time.Parse(time.RFC3339, text); err != nil {
			return nil, ErrExpiryFormat
		}
	}
	if !at.After(now) {
		return nil, ErrExpiryPast
	}
	return &at, nil
}
