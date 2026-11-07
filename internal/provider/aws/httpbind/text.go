package httpbind

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrValue reports a value that does not fit its Shape. It names the member, never the value.
var ErrValue = errors.New("httpbind: invalid value")

// ErrResponse reports an answer that does not fit its Shape.
var ErrResponse = errors.New("httpbind: the answer does not fit its description")

// TimeFormat resolves the timestamp format of a shape: its own, else the default of the place.
func (s *Shape) TimeFormat(def Format) Format {
	if s.Format != FormatDefault {
		return s.Format
	}
	return def
}

// Text renders a scalar argument in its wire text: integers in decimal, floats without exponent, booleans as
// true/false, timestamps by their format (def applies where the shape names none), a blob as the base64 it
// already is, strings unchanged. args are JSON values: json.Number, Go numbers, strings, booleans. The second
// result is false for a value that does not fit the kind.
func Text(s *Shape, v any, def Format) (string, bool) {
	switch s.Kind {
	case KindString:
		text, ok := v.(string)
		return text, ok && utf8.ValidString(text)
	case KindBoolean:
		b, ok := v.(bool)
		return strconv.FormatBool(b), ok
	case KindInteger:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n), true
		case int64:
			return strconv.FormatInt(n, 10), true
		case float64:
			if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
				return "", false
			}
			return strconv.FormatInt(int64(n), 10), true
		case json.Number:
			i, err := strconv.ParseInt(n.String(), 10, 64)
			return strconv.FormatInt(i, 10), err == nil
		}
	case KindFloat:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n), true
		case int64:
			return strconv.FormatInt(n, 10), true
		case float64:
			return formatFloat(n)
		case json.Number:
			f, err := strconv.ParseFloat(n.String(), 64)
			if err != nil {
				return "", false
			}
			return formatFloat(f)
		}
	case KindTimestamp:
		text, ok := v.(string)
		if !ok {
			return "", false
		}
		t, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return "", false
		}
		return FormatTimestamp(t, s.TimeFormat(def))
	case KindBlob:
		text, ok := v.(string)
		if !ok {
			return "", false
		}
		_, err := base64.StdEncoding.DecodeString(text)
		return text, err == nil
	}
	return "", false
}

// formatFloat refuses NaN and infinities, whose form depends on the service.
func formatFloat(f float64) (string, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", false
	}
	return strconv.FormatFloat(f, 'f', -1, 64), true
}

// FormatTimestamp renders t in one of the three formats. FormatDefault is not a format: callers resolve it
// first. A time before 1970 has no epoch-seconds form here and a time with a fraction has no http-date form,
// because both would need a rounding the service never specified.
func FormatTimestamp(t time.Time, f Format) (string, bool) {
	t = t.UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return "", false
	}
	switch f {
	case FormatEpochSeconds:
		if t.Before(time.Unix(0, 0)) {
			return "", false
		}
		text := strconv.FormatInt(t.Unix(), 10)
		if ns := t.Nanosecond(); ns != 0 {
			text += "." + strings.TrimRight(leftPad9(ns), "0")
		}
		return text, true
	case FormatDateTime:
		return t.Format("2006-01-02T15:04:05.999999999Z"), true
	case FormatHTTPDate:
		if t.Nanosecond() != 0 {
			return "", false
		}
		return t.Format(http.TimeFormat), true
	}
	return "", false
}

func leftPad9(n int) string {
	s := strconv.Itoa(n)
	return strings.Repeat("0", 9-len(s)) + s
}

// ParseText reads wire text as the value of a scalar shape: strings capped at MaxLen (reported as cut),
// integers as int64, floats as float64, booleans, timestamps as RFC 3339 UTC text, and blobs as the base64
// they are. Text that does not parse as its kind is ErrResponse, never a zero value.
func ParseText(s *Shape, text string, def Format) (v any, cut bool, err error) {
	limit := orInt(s.MaxLen, DefaultMaxString)
	if !utf8.ValidString(text) {
		return nil, false, ErrResponse
	}
	switch s.Kind {
	case KindString:
		if len(text) > limit {
			text = text[:limit]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
			return text, true, nil
		}
		return text, false, nil
	case KindBoolean:
		switch text {
		case "true":
			return true, false, nil
		case "false":
			return false, false, nil
		}
	case KindInteger:
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, false, nil
		}
	case KindFloat:
		if f, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, false, nil
		}
	case KindTimestamp:
		if t, ok := parseTimestamp(text, s.TimeFormat(def)); ok && len(text) <= limit {
			return t.UTC().Format(time.RFC3339Nano), false, nil
		}
	case KindBlob:
		if _, err := base64.StdEncoding.DecodeString(text); err == nil && len(text) <= limit {
			return text, false, nil
		}
	}
	return nil, false, ErrResponse
}

func parseTimestamp(text string, f Format) (time.Time, bool) {
	var t time.Time
	var err error
	switch f {
	case FormatDateTime:
		t, err = time.Parse(time.RFC3339Nano, text)
	case FormatHTTPDate:
		t, err = http.ParseTime(text)
	case FormatEpochSeconds:
		return parseEpoch(text)
	default:
		return time.Time{}, false
	}
	return t, err == nil && t.Year() >= 1 && t.Year() <= 9999
}

// parseEpoch reads seconds since the epoch with an optional fraction of at most nine digits, or the
// exponent form some JSON writers produce.
func parseEpoch(text string) (time.Time, bool) {
	var t time.Time
	if strings.ContainsAny(text, "eE") {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || f < 0 || f > 253402300799 {
			return t, false
		}
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(frac*1e9)), true
	}
	whole, fraction, _ := strings.Cut(text, ".")
	sec, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || sec < 0 || sec > 253402300799 || whole != strconv.FormatInt(sec, 10) || len(fraction) > 9 {
		return t, false
	}
	var ns int64
	if fraction != "" {
		if ns, err = strconv.ParseInt(fraction, 10, 64); err != nil || fraction[0] == '-' || fraction[0] == '+' {
			return t, false
		}
		for i := len(fraction); i < 9; i++ {
			ns *= 10
		}
	}
	return time.Unix(sec, ns), true
}

func orInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// OrInt returns value, or fallback when value is not positive. Codecs use it for the caps of a Shape.
func OrInt(value, fallback int) int { return orInt(value, fallback) }
