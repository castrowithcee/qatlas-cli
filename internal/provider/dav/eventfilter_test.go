package dav

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

const (
	testFrom = "20260301T000000Z"
	testTo   = "20260401T000000Z"
)

func TestEventQueryBodyWithoutFilterIsUnchanged(t *testing.T) {
	body, err := EventQueryBody("op", testFrom, testTo, EventFilter{})
	if err != nil || body != CalendarQueryBody(testFrom, testTo) {
		t.Errorf("body = %q, err = %v", body, err)
	}
}

func TestEventQueryBodyShapes(t *testing.T) {
	const head = `<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">` +
		`<c:time-range start="20260301T000000Z" end="20260401T000000Z"/>`
	const tail = `</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
	for name, tc := range map[string]struct {
		filter EventFilter
		want   string
	}{
		"text": {EventFilter{Properties: []PropertyFilter{{Property: "SUMMARY", Text: ptr("a <b> & \"c\"")}}},
			`<c:prop-filter name="SUMMARY"><c:text-match>a &lt;b&gt; &amp; &#34;c&#34;</c:text-match></c:prop-filter>`},
		"negate and collation": {EventFilter{Properties: []PropertyFilter{{Property: "LOCATION", Text: ptr("x"),
			Negate: true, Collation: "i;octet"}}},
			`<c:prop-filter name="LOCATION"><c:text-match collation="i;octet" negate-condition="yes">x</c:text-match></c:prop-filter>`},
		"not defined": {EventFilter{Properties: []PropertyFilter{{Property: "STATUS", Defined: ptr(false)}}},
			`<c:prop-filter name="STATUS"><c:is-not-defined/></c:prop-filter>`},
		"time range": {EventFilter{Properties: []PropertyFilter{{Property: "CREATED",
			Range: &TimeRange{Start: "2026-01-01T00:00:00+01:00", End: "2026-02-01T00:00:00Z"}}}},
			`<c:prop-filter name="CREATED"><c:time-range start="20251231T230000Z" end="20260201T000000Z"/></c:prop-filter>`},
		"parameter text": {EventFilter{Properties: []PropertyFilter{{Property: "ATTENDEE", Text: ptr("example.com"),
			Parameter: &ParamFilter{Name: "PARTSTAT", Text: ptr("NEEDS-ACTION"), Negate: true}}}},
			`<c:prop-filter name="ATTENDEE"><c:text-match>example.com</c:text-match><c:param-filter name="PARTSTAT">` +
				`<c:text-match negate-condition="yes">NEEDS-ACTION</c:text-match></c:param-filter></c:prop-filter>`},
		"parameter only": {EventFilter{Properties: []PropertyFilter{{Property: "ORGANIZER",
			Parameter: &ParamFilter{Name: "CN", Defined: ptr(false)}}}},
			`<c:prop-filter name="ORGANIZER"><c:param-filter name="CN"><c:is-not-defined/></c:param-filter></c:prop-filter>`},
		"alarm": {EventFilter{Alarm: &TimeRange{Start: "2026-03-01T00:00:00Z", End: "2026-03-02T00:00:00Z"}},
			`<c:comp-filter name="VALARM"><c:time-range start="20260301T000000Z" end="20260302T000000Z"/></c:comp-filter>`},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := EventQueryBody("op", testFrom, testTo, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, head+tc.want+tail) || !strings.Contains(body, `<c:calendar-data/>`) {
				t.Errorf("body = %s", body)
			}
		})
	}
}

func TestEventQueryBodyAndsEntriesInOrderAndExpands(t *testing.T) {
	body, err := EventQueryBody("op", testFrom, testTo, EventFilter{Expand: true, Properties: []PropertyFilter{
		{Property: "SUMMARY", Text: ptr("a")}, {Property: "UID", Text: ptr("b")}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `<c:calendar-data><c:expand start="20260301T000000Z" end="20260401T000000Z"/></c:calendar-data>`) ||
		strings.Index(body, `name="SUMMARY"`) > strings.Index(body, `name="UID"`) {
		t.Errorf("body = %s", body)
	}
}

func TestEventQueryBodyRefusals(t *testing.T) {
	long := strings.Repeat("a", MaxFilterText+1)
	many := make([]PropertyFilter, MaxPropertyFilters+1)
	for i := range many {
		many[i] = PropertyFilter{Property: "SUMMARY", Text: ptr("a")}
	}
	day := &TimeRange{Start: "2026-03-01T00:00:00Z", End: "2026-03-02T00:00:00Z"}
	for name, f := range map[string]EventFilter{
		"unknown property":     {Properties: []PropertyFilter{{Property: "X-FOO", Text: ptr("a")}}},
		"lowercase property":   {Properties: []PropertyFilter{{Property: "summary", Text: ptr("a")}}},
		"unknown parameter":    {Properties: []PropertyFilter{{Property: "ATTENDEE", Text: ptr("a"), Parameter: &ParamFilter{Name: "X", Text: ptr("a")}}}},
		"unknown collation":    {Properties: []PropertyFilter{{Property: "SUMMARY", Text: ptr("a"), Collation: "i;unicode-casemap"}}},
		"unknown param coll":   {Properties: []PropertyFilter{{Property: "ATTENDEE", Parameter: &ParamFilter{Name: "CN", Text: ptr("a"), Collation: "x"}}}},
		"text too long":        {Properties: []PropertyFilter{{Property: "SUMMARY", Text: &long}}},
		"empty text":           {Properties: []PropertyFilter{{Property: "SUMMARY", Text: ptr("")}}},
		"control character":    {Properties: []PropertyFilter{{Property: "SUMMARY", Text: ptr("a\x00b")}}},
		"too many entries":     {Properties: many},
		"two conditions":       {Properties: []PropertyFilter{{Property: "CREATED", Text: ptr("a"), Range: day}}},
		"text and defined":     {Properties: []PropertyFilter{{Property: "SUMMARY", Text: ptr("a"), Defined: ptr(false)}}},
		"defined true":         {Properties: []PropertyFilter{{Property: "SUMMARY", Defined: ptr(true)}}},
		"no condition":         {Properties: []PropertyFilter{{Property: "SUMMARY"}}},
		"negate without text":  {Properties: []PropertyFilter{{Property: "SUMMARY", Defined: ptr(false), Negate: true}}},
		"range on summary":     {Properties: []PropertyFilter{{Property: "SUMMARY", Range: day}}},
		"range end before":     {Properties: []PropertyFilter{{Property: "CREATED", Range: &TimeRange{Start: day.End, End: day.Start}}}},
		"undefined and param":  {Properties: []PropertyFilter{{Property: "ATTENDEE", Defined: ptr(false), Parameter: &ParamFilter{Name: "CN", Defined: ptr(false)}}}},
		"param two conditions": {Properties: []PropertyFilter{{Property: "ATTENDEE", Parameter: &ParamFilter{Name: "CN", Text: ptr("a"), Defined: ptr(false)}}}},
		"param no condition":   {Properties: []PropertyFilter{{Property: "ATTENDEE", Parameter: &ParamFilter{Name: "CN"}}}},
		"alarm too long":       {Alarm: &TimeRange{Start: "2026-01-01T00:00:00Z", End: "2027-06-01T00:00:00Z"}},
		"alarm end before":     {Alarm: &TimeRange{Start: day.End, End: day.Start}},
		"alarm bad time":       {Alarm: &TimeRange{Start: "soon", End: day.End}},
	} {
		t.Run(name, func(t *testing.T) {
			if body, err := EventQueryBody("op", testFrom, testTo, f); err == nil {
				t.Errorf("body = %s, want refusal", body)
			}
		})
	}
}

func TestParseOccurrencesReadsEveryVEvent(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:u1@example.com\r\nRECURRENCE-ID:20260302T100000Z\r\nDTSTART:20260302T100000Z\r\nSUMMARY:Daily\r\nRRULE:FREQ=DAILY\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:u1@example.com\r\nRECURRENCE-ID:20260303T100000Z\r\nDTSTART:20260303T110000Z\r\nSUMMARY:Moved\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	events, err := ParseOccurrences(data, true)
	if err != nil || len(events) != 2 || events[0].RecurrenceID != "2026-03-02T10:00:00Z" ||
		events[1].RecurrenceID != "2026-03-03T10:00:00Z" || events[1].Start != "2026-03-03T11:00:00Z" ||
		events[1].Description != "" {
		t.Errorf("events = %+v, err = %v", events, err)
	}
	if _, err := ParseOccurrences("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nEND:VCALENDAR\r\n", true); err == nil {
		t.Error("no VEVENT must be an error")
	}
}
