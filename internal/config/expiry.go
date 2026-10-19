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

// ParseExpiryTime reads the format of an expiry, a date or an RFC 3339 time, and says nothing about whether
// it lies ahead. It is the one parser of that format.
func ParseExpiryTime(text string) (time.Time, error) {
	at, err := time.ParseInLocation("2006-01-02", text, time.Local)
	if err != nil {
		if at, err = time.Parse(time.RFC3339, text); err != nil {
			return time.Time{}, ErrExpiryFormat
		}
	}
	return at, nil
}

// ParseExpiry reads an expiry: empty for none, else a date, meaning its start in local time, or an RFC 3339
// time. It must lie after now, which the caller supplies.
func ParseExpiry(text string, now time.Time) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	at, err := ParseExpiryTime(text)
	if err != nil {
		return nil, err
	}
	if !at.After(now) {
		return nil, ErrExpiryPast
	}
	return &at, nil
}

// Until returns the instant a connection stays active until, and whether it has one that reads.
func (c Connection) Until() (time.Time, bool) {
	if c.ActiveUntil == "" {
		return time.Time{}, false
	}
	at, err := ParseExpiryTime(c.ActiveUntil)
	return at, err == nil
}

// Off reports whether the connection offers nothing at now: switched off, or past its active_until. A
// limit that does not read counts as past, so a malformed value never keeps a connection on.
func (c Connection) Off(now time.Time) bool {
	if c.Disabled {
		return true
	}
	if c.ActiveUntil == "" {
		return false
	}
	at, ok := c.Until()
	return !ok || !now.Before(at)
}

// Expired reports whether an active connection is past its active_until at now.
func (c Connection) Expired(now time.Time) bool { return !c.Disabled && c.Off(now) }

// FormatActiveUntil writes an instant the way the configuration file keeps it.
func FormatActiveUntil(at time.Time) string {
	return at.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// ActiveUntilCanonical is the limit as RFC 3339 in UTC, or the text as read when it does not read, so a
// malformed value still differs from every valid one. Empty means no limit.
func (c Connection) ActiveUntilCanonical() string {
	if at, ok := c.Until(); ok {
		return FormatActiveUntil(at)
	}
	return c.ActiveUntil
}
