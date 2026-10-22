package dav

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Bounds of the structured event filters.
const (
	MaxPropertyFilters = 8
	MaxFilterText      = 256
)

// The fixed value lists of the event filters. Only these values ever reach a REPORT body.
var (
	EventFilterProperties = []string{"SUMMARY", "DESCRIPTION", "LOCATION", "STATUS", "CLASS", "TRANSP", "CATEGORIES",
		"COMMENT", "CONTACT", "RESOURCES", "UID", "URL", "ATTENDEE", "ORGANIZER", "RELATED-TO", "DTSTAMP", "CREATED",
		"LAST-MODIFIED"}
	EventFilterParameters = []string{"PARTSTAT", "ROLE", "CUTYPE", "RSVP", "CN", "DELEGATED-TO", "DELEGATED-FROM",
		"SENT-BY", "MEMBER", "RELTYPE", "LANGUAGE"}
	EventFilterCollations = []string{"i;ascii-casemap", "i;octet"}
	eventTimeProperties   = []string{"DTSTAMP", "CREATED", "LAST-MODIFIED"}
)

// TimeRange is a range of two RFC 3339 times.
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ParamFilter narrows a property by one of its parameters: a text condition or defined=false.
type ParamFilter struct {
	Name      string  `json:"name"`
	Text      *string `json:"text"`
	Negate    bool    `json:"negate"`
	Collation string  `json:"collation"`
	Defined   *bool   `json:"defined"`
}

// PropertyFilter is one prop-filter of a calendar-query. Without a parameter it carries exactly one condition.
type PropertyFilter struct {
	Property  string       `json:"property"`
	Text      *string      `json:"text"`
	Negate    bool         `json:"negate"`
	Collation string       `json:"collation"`
	Defined   *bool        `json:"defined"`
	Range     *TimeRange   `json:"range"`
	Parameter *ParamFilter `json:"parameter"`
}

// EventFilter is the optional narrowing of an event list inside the fixed VCALENDAR/VEVENT comp-filter.
type EventFilter struct {
	Properties []PropertyFilter
	Alarm      *TimeRange
	Expand     bool
}

// Zero reports whether the filter changes nothing about the plain query.
func (f EventFilter) Zero() bool { return len(f.Properties) == 0 && f.Alarm == nil && !f.Expand }

// UsesCollation reports whether any condition names a collation.
func (f EventFilter) UsesCollation() bool {
	for _, p := range f.Properties {
		if p.Collation != "" || (p.Parameter != nil && p.Parameter.Collation != "") {
			return true
		}
	}
	return false
}

// EventQueryBody validates the filter and builds the calendar-query for a range given in the form ParseRange
// returns. Without a filter the body is the one CalendarQueryBody returns. Every text is XML-escaped and every
// name comes from a fixed list.
func EventQueryBody(op, from, to string, f EventFilter) (string, error) {
	if f.Zero() {
		return CalendarQueryBody(from, to), nil
	}
	if len(f.Properties) > MaxPropertyFilters {
		return "", provider.Fail(op, fmt.Sprintf("property_filters takes at most %d entries", MaxPropertyFilters))
	}
	var filters bytes.Buffer
	for i := range f.Properties {
		if err := writePropFilter(op, &filters, &f.Properties[i]); err != nil {
			return "", err
		}
	}
	if f.Alarm != nil {
		start, end, err := parseSubRange(op, "alarm_range", f.Alarm)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&filters, `<c:comp-filter name="VALARM"><c:time-range start="%s" end="%s"/></c:comp-filter>`, start, end)
	}
	data := `<c:calendar-data/>`
	if f.Expand {
		data = fmt.Sprintf(`<c:calendar-data><c:expand start="%s" end="%s"/></c:calendar-data>`, from, to)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><d:getetag/>` + data + `</d:prop>` +
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">` +
		fmt.Sprintf(`<c:time-range start="%s" end="%s"/>`, from, to) + filters.String() +
		`</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`, nil
}

func parseSubRange(op, label string, r *TimeRange) (string, string, error) {
	start, end, err := ParseRange(op, r.Start, r.End)
	if err != nil {
		var failure *provider.Error
		if ok := asProviderError(err, &failure); ok {
			return "", "", provider.Fail(op, label+": "+failure.Message)
		}
		return "", "", err
	}
	return start, end, nil
}

func asProviderError(err error, target **provider.Error) bool {
	e, ok := err.(*provider.Error)
	if ok {
		*target = e
	}
	return ok
}

func writePropFilter(op string, out *bytes.Buffer, p *PropertyFilter) error {
	if !slices.Contains(EventFilterProperties, p.Property) {
		return provider.Fail(op, "property_filters names a property that is not offered")
	}
	conditions := 0
	if p.Text != nil {
		conditions++
	}
	if p.Defined != nil {
		conditions++
	}
	if p.Range != nil {
		conditions++
	}
	if conditions > 1 {
		return provider.Fail(op, "a property filter takes exactly one of text, defined, and range")
	}
	if conditions == 0 && p.Parameter == nil {
		return provider.Fail(op, "a property filter needs one of text, defined, and range")
	}
	if p.Defined != nil && *p.Defined {
		return provider.Fail(op, "defined only takes false")
	}
	if p.Defined != nil && p.Parameter != nil {
		return provider.Fail(op, "a property that must be undefined cannot carry a parameter filter")
	}
	if p.Text == nil && (p.Negate || p.Collation != "") {
		return provider.Fail(op, "negate and collation need a text")
	}
	if p.Range != nil && !slices.Contains(eventTimeProperties, p.Property) {
		return provider.Fail(op, "range is only offered for DTSTAMP, CREATED, and LAST-MODIFIED")
	}
	var inner bytes.Buffer
	switch {
	case p.Text != nil:
		match, err := textMatch(op, *p.Text, p.Negate, p.Collation)
		if err != nil {
			return err
		}
		inner.WriteString(match)
	case p.Defined != nil:
		inner.WriteString(`<c:is-not-defined/>`)
	case p.Range != nil:
		start, end, err := parseSubRange(op, "range", p.Range)
		if err != nil {
			return err
		}
		fmt.Fprintf(&inner, `<c:time-range start="%s" end="%s"/>`, start, end)
	}
	if p.Parameter != nil {
		param, err := paramFilter(op, p.Parameter)
		if err != nil {
			return err
		}
		inner.WriteString(param)
	}
	fmt.Fprintf(out, `<c:prop-filter name="%s">%s</c:prop-filter>`, p.Property, inner.String())
	return nil
}

func paramFilter(op string, p *ParamFilter) (string, error) {
	if !slices.Contains(EventFilterParameters, p.Name) {
		return "", provider.Fail(op, "parameter names a parameter that is not offered")
	}
	switch {
	case p.Text != nil && p.Defined != nil:
		return "", provider.Fail(op, "a parameter filter takes exactly one of text and defined")
	case p.Defined != nil:
		if *p.Defined || p.Negate || p.Collation != "" {
			return "", provider.Fail(op, "defined only takes false, without negate or collation")
		}
		return `<c:param-filter name="` + p.Name + `"><c:is-not-defined/></c:param-filter>`, nil
	case p.Text != nil:
		match, err := textMatch(op, *p.Text, p.Negate, p.Collation)
		if err != nil {
			return "", err
		}
		return `<c:param-filter name="` + p.Name + `">` + match + `</c:param-filter>`, nil
	}
	return "", provider.Fail(op, "a parameter filter needs a text or defined")
}

// textMatch builds a text-match. The collation attribute is only written when the caller chose one, so the
// server default applies otherwise.
func textMatch(op, text string, negate bool, collation string) (string, error) {
	if text == "" || len(text) > MaxFilterText {
		return "", provider.Fail(op, fmt.Sprintf("a filter text takes 1 to %d bytes", MaxFilterText))
	}
	if !utf8.ValidString(text) || strings.IndexFunc(text, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", provider.Fail(op, "a filter text must be valid text without control characters")
	}
	attrs := ""
	if collation != "" {
		if !slices.Contains(EventFilterCollations, collation) {
			return "", provider.Fail(op, "collation is not offered")
		}
		attrs += ` collation="` + collation + `"`
	}
	if negate {
		attrs += ` negate-condition="yes"`
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(text))
	return `<c:text-match` + attrs + `>` + escaped.String() + `</c:text-match>`, nil
}

// EventFilterSchema returns the JSON schema members of the filter arguments for a tool input schema.
func EventFilterSchema() string {
	enum := func(values []string) string {
		raw, _ := json.Marshal(values)
		return `{"type":"string","enum":` + string(raw) + `}`
	}
	timeText := `{"type":"string","minLength":20,"maxLength":40}`
	rng := `{"type":"object","properties":{"start":` + timeText + `,"end":` + timeText +
		`},"required":["start","end"],"additionalProperties":false}`
	text := fmt.Sprintf(`{"type":"string","minLength":1,"maxLength":%d}`, MaxFilterText)
	condition := `"text":` + text + `,"negate":{"type":"boolean"},"collation":` + enum(EventFilterCollations) +
		`,"defined":{"type":"boolean","enum":[false]}`
	parameter := `{"type":"object","properties":{"name":` + enum(EventFilterParameters) + `,` + condition +
		`},"required":["name"],"additionalProperties":false}`
	entry := `{"type":"object","properties":{"property":` + enum(EventFilterProperties) + `,` + condition +
		`,"range":` + rng + `,"parameter":` + parameter + `},"required":["property"],"additionalProperties":false}`
	return `"property_filters":{"type":"array","maxItems":` + fmt.Sprint(MaxPropertyFilters) + `,"items":` + entry + `},` +
		`"alarm_range":` + rng + `,"expand":{"type":"boolean"}`
}
