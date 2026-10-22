package infomaniakdav

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const workPath = calHome + "work/"

func icsOf(vevents ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(vevents, "") + "END:VCALENDAR\r\n"
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func eventEntry(href, etag, ics string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:getetag>"` + etag +
		`"</d:getetag><c:calendar-data><![CDATA[` + ics + `]]></c:calendar-data></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

// eventsFake answers discovery, one REPORT of the work calendar, and GETs of its events.
func eventsFake(t *testing.T, report string, get func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == "REPORT" && r.URL.Path == workPath:
			return xmlResponse(207, report), nil
		case r.Method == "GET" && get != nil:
			return get(r)
		case r.Method == "PROPFIND":
			return davFake(t)(r)
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		return xmlResponse(404, bodyCanary), nil
	}
}

const (
	rangeArgs = `"start":"2026-03-01T00:00:00+01:00","end":"2026-04-01T00:00:00Z"`
	allArgs   = `{"calendar":"work",` + rangeArgs + `}`
)

func listEventsOf(t *testing.T, targets []string, args string) (any, error) {
	t.Helper()
	return invokeEventsList(context.Background(), resolved(targets...), resolver(nil), nil, json.RawMessage(args))
}

func TestEventsListReportsStructuredEventsInRange(t *testing.T) {
	report := multistatus(
		eventEntry(workPath+"b.ics", "e2", icsOf(vevent("UID:u-b", "SUMMARY:Later\\, with comma",
			"DTSTART;TZID=Europe/Zurich:20260310T090000", "DTEND;TZID=Europe/Zurich:20260310T100000",
			"LOCATION:Room 1", "STATUS:CONFIRMED", "DESCRIPTION:not in list"))),
		eventEntry(workPath+"a.ics", "e1", icsOf(vevent("UID:u-a", "SUMMARY:Daily", "DTSTART;VALUE=DATE:20260302",
			"DTEND;VALUE=DATE:20260303", "RRULE:FREQ=DAILY;COUNT=1000",
			"ORGANIZER;CN=Boss:mailto:boss@example.org"))))
	calls := serve(t, eventsFake(t, report, nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, allArgs)
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	page := out.(*EventsPage)
	if page.Count != 2 || page.Truncated || page.Events[0].ID != "a.ics" || page.Events[1].ID != "b.ics" {
		t.Fatalf("page = %+v", page)
	}
	a, b := page.Events[0], page.Events[1]
	if !a.AllDay || a.Start != "2026-03-02" || a.End != "2026-03-03" || a.RRule != "FREQ=DAILY;COUNT=1000" ||
		a.ETag != "e1" || a.UID != "u-a" || a.Organizer != nil {
		t.Errorf("a = %+v", a)
	}
	if b.Summary != "Later, with comma" || b.Start != "2026-03-10T09:00:00" || b.Timezone != "Europe/Zurich" ||
		b.Status != "CONFIRMED" || b.Description != "" {
		t.Errorf("b = %+v", b)
	}
	last := (*calls)[len(*calls)-1]
	if last.method != "REPORT" || last.depth != "1" || !strings.HasSuffix(last.path, workPath) ||
		!strings.Contains(last.body, `start="20260228T230000Z" end="20260401T000000Z"`) {
		t.Errorf("report = %+v", last)
	}
}

func TestEventsListLimitTruncatesAfterSorting(t *testing.T) {
	var entries []string
	for _, n := range []string{"3", "1", "2"} {
		entries = append(entries, eventEntry(workPath+n+".ics", n, icsOf(vevent("UID:"+n, "DTSTART:2026030"+n+"T100000Z"))))
	}
	serve(t, eventsFake(t, multistatus(entries...), nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, `{"calendar":"work",`+rangeArgs+`,"limit":2}`)
	page := out.(*EventsPage)
	if err != nil || page.Count != 2 || !page.Truncated || page.Events[0].ID != "1.ics" || page.Events[1].ID != "2.ics" ||
		page.Events[0].Start != "2026-03-01T10:00:00Z" {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}

func TestEventsListRefusesBeforeAnyIO(t *testing.T) {
	calls := serve(t, eventsFake(t, multistatus(), nil))
	for name, args := range map[string]string{
		"calendar not allowed": `{"calendar":"other",` + rangeArgs + `}`,
		"calendar traversal":   `{"calendar":"../work",` + rangeArgs + `}`,
		"missing range":        `{"calendar":"work"}`,
		"bad start":            `{"calendar":"work","start":"yesterday","end":"2026-04-01T00:00:00Z"}`,
		"end before start":     `{"calendar":"work","start":"2026-04-01T00:00:00Z","end":"2026-03-01T00:00:00Z"}`,
		"range too long":       `{"calendar":"work","start":"2026-01-01T00:00:00Z","end":"2027-06-01T00:00:00Z"}`,
		"limit too large":      `{"calendar":"work",` + rangeArgs + `,"limit":201}`,
		"free url":             `{"calendar":"work",` + rangeArgs + `,"url":"https://evil.example"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := listEventsOf(t, []string{"calendar/work"}, args)
			if err == nil || strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "evil") {
				t.Errorf("err = %v", err)
			}
		})
	}
	if _, err := invokeEventsList(context.Background(), resolved("calendar/work"), nil, nil,
		json.RawMessage(`{"calendar":"other",`+rangeArgs+`}`)); classOf(err) != provider.ClassPermission {
		t.Errorf("allow-list err = %v, want permission before any secret access", err)
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestEventsListRejectsHrefOutsideCalendar(t *testing.T) {
	for name, href := range map[string]string{
		"other calendar": calHome + "other/x.ics",
		"nested":         workPath + "sub/x.ics",
		"foreign host":   "https://evil.example" + workPath + "x.ics",
		"the calendar":   workPath,
		"dot dot":        workPath + "../other/x.ics",
		"percent id":     workPath + "a%25b.ics",
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, eventsFake(t, multistatus(eventEntry(href, "e", icsOf(vevent("UID:x", "DTSTART:20260301T100000Z")))), nil))
			_, err := listEventsOf(t, []string{"calendar/work"}, allArgs)
			if classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("err = %v, want an invalid response", err)
			}
		})
	}
}

func TestEventsListEmptyRangeAndSizeCaps(t *testing.T) {
	serve(t, eventsFake(t, multistatus(), nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, allArgs)
	if page := out.(*EventsPage); err != nil || page.Count != 0 || page.Events == nil {
		t.Errorf("empty = %+v, %v", out, err)
	}
	big := icsOf(vevent("UID:x", "DTSTART:20260301T100000Z", "DESCRIPTION:"+strings.Repeat("a", maxEventBytes)))
	serve(t, eventsFake(t, multistatus(eventEntry(workPath+"x.ics", "e", big)), nil))
	if _, err := listEventsOf(t, []string{"calendar/work"}, allArgs); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("big event err = %v", err)
	}
	many := make([]string, dav.MaxEntries+1)
	for i := range many {
		many[i] = `<d:response><d:href>` + workPath + `x.ics</d:href></d:response>`
	}
	serve(t, eventsFake(t, multistatus(many...), nil))
	if _, err := listEventsOf(t, []string{"calendar/work"}, allArgs); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("too many err = %v", err)
	}
}

func TestEventsGetReadsStructuredEventWithETag(t *testing.T) {
	ics := icsOf(vevent("UID:u1", "SUMMARY:Planning", "DTSTART:20260310T090000Z", "DTEND:20260310T100000Z",
		"DESCRIPTION:line one\\nline two", "RRULE:FREQ=WEEKLY;BYDAY=TU",
		"ORGANIZER;CN=Boss:mailto:boss@example.org",
		"ATTENDEE;CN=Ann;PARTSTAT=ACCEPTED:mailto:ann@example.org"),
		vevent("UID:u1", "RECURRENCE-ID:20260317T090000Z", "SUMMARY:Moved", "DTSTART:20260317T110000Z"))
	calls := serve(t, eventsFake(t, "", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != workPath+"u1.ics" {
			t.Errorf("GET path = %s", r.URL.Path)
		}
		resp := xmlResponse(200, ics)
		resp.Header.Set("ETag", `"abc123"`)
		return resp, nil
	}))
	out, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
		json.RawMessage(`{"calendar":"work","id":"u1.ics"}`))
	if err != nil {
		t.Fatalf("get = %v", err)
	}
	event := out.(*EventResult).Event
	if event.ID != "u1.ics" || event.UID != "u1" || event.Summary != "Planning" || event.ETag != "abc123" ||
		event.Description != "line one\nline two" || event.RRule != "FREQ=WEEKLY;BYDAY=TU" ||
		event.Start != "2026-03-10T09:00:00Z" || event.End != "2026-03-10T10:00:00Z" || event.Timezone != "UTC" ||
		event.Organizer == nil || event.Organizer.Address != "boss@example.org" || event.Organizer.Name != "Boss" ||
		len(event.Attendees) != 1 || event.Attendees[0].Status != "ACCEPTED" {
		t.Errorf("event = %+v", event)
	}
	if last := (*calls)[len(*calls)-1]; last.method != "GET" || last.auth != basic() {
		t.Errorf("call = %+v", last)
	}
}

func TestEventsGetBoundsAttendees(t *testing.T) {
	lines := []string{"UID:u", "DTSTART:20260310T090000Z"}
	for i := 0; i < dav.MaxAttendees+5; i++ {
		lines = append(lines, "ATTENDEE:mailto:a@example.org")
	}
	serve(t, eventsFake(t, "", func(*http.Request) (*http.Response, error) {
		return xmlResponse(200, icsOf(vevent(lines...))), nil
	}))
	out, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
		json.RawMessage(`{"calendar":"work","id":"u.ics"}`))
	if event := out.(*EventResult).Event; err != nil || len(event.Attendees) != dav.MaxAttendees || !event.AttendeesTruncated {
		t.Errorf("event = %+v, err = %v", event, err)
	}
}

func TestEventsGetRefusesBeforeAnyIO(t *testing.T) {
	calls := serve(t, eventsFake(t, "", nil))
	for name, args := range map[string]string{
		"calendar not allowed": `{"calendar":"other","id":"x.ics"}`,
		"id with slash":        `{"calendar":"work","id":"a/b.ics"}`,
		"id dot dot":           `{"calendar":"work","id":".."}`,
		"id encoded":           `{"calendar":"work","id":"a%2Fb"}`,
		"id empty":             `{"calendar":"work","id":""}`,
		"id too long":          `{"calendar":"work","id":"` + strings.Repeat("a", 129) + `"}`,
		"id control":           `{"calendar":"work","id":"a\u0001b"}`,
		"free path":            `{"calendar":"work","id":"x.ics","path":"/etc"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := invokeEventsGet(context.Background(), resolved("calendar/work"), nil, nil,
				json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestEventsErrorsCarryNoProviderText(t *testing.T) {
	for status, class := range map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission,
		404: provider.ClassNotFound, 500: provider.ClassProviderError} {
		serve(t, eventsFake(t, "", func(*http.Request) (*http.Response, error) { return xmlResponse(status, bodyCanary), nil }))
		_, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
			json.RawMessage(`{"calendar":"work","id":"x.ics"}`))
		if classOf(err) != class || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	serve(t, eventsFake(t, "", func(*http.Request) (*http.Response, error) {
		return xmlResponse(200, "not an ical "+bodyCanary), nil
	}))
	_, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
		json.RawMessage(`{"calendar":"work","id":"x.ics"}`))
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("invalid ics err = %v", err)
	}
}

func occurrence(uid, recurrence, start, summary string) string {
	return vevent("UID:"+uid, "RECURRENCE-ID:"+recurrence, "DTSTART:"+start, "SUMMARY:"+summary)
}

func lastReport(calls *[]call) call { return (*calls)[len(*calls)-1] }

func TestEventsListWithoutNewArgumentsKeepsBody(t *testing.T) {
	calls := serve(t, eventsFake(t, multistatus(), nil))
	if _, err := listEventsOf(t, []string{"calendar/work"}, allArgs); err != nil {
		t.Fatal(err)
	}
	if got := lastReport(calls).body; got != dav.CalendarQueryBody("20260228T230000Z", "20260401T000000Z") {
		t.Errorf("body = %s", got)
	}
}

func TestEventsListSendsFiltersEscaped(t *testing.T) {
	calls := serve(t, eventsFake(t, multistatus(), nil))
	args := `{"calendar":"work",` + rangeArgs + `,"property_filters":[` +
		`{"property":"SUMMARY","text":"R&D <x>","negate":true,"collation":"i;octet"},` +
		`{"property":"ATTENDEE","parameter":{"name":"PARTSTAT","text":"ACCEPTED"}},` +
		`{"property":"LOCATION","defined":false}],` +
		`"alarm_range":{"start":"2026-03-01T00:00:00Z","end":"2026-03-08T00:00:00Z"}}`
	if _, err := listEventsOf(t, []string{"calendar/work"}, args); err != nil {
		t.Fatal(err)
	}
	body := lastReport(calls).body
	for _, want := range []string{
		`<c:text-match collation="i;octet" negate-condition="yes">R&amp;D &lt;x&gt;</c:text-match>`,
		`<c:prop-filter name="ATTENDEE"><c:param-filter name="PARTSTAT"><c:text-match>ACCEPTED</c:text-match></c:param-filter></c:prop-filter>`,
		`<c:prop-filter name="LOCATION"><c:is-not-defined/></c:prop-filter>`,
		`<c:comp-filter name="VALARM"><c:time-range start="20260301T000000Z" end="20260308T000000Z"/></c:comp-filter>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
}

func TestEventsListFilterRefusalsBeforeAnyIO(t *testing.T) {
	calls := serve(t, eventsFake(t, multistatus(), nil))
	long := strings.Repeat("a", 257)
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, `{"property":"SUMMARY","text":"a"}`)
	}
	for name, filters := range map[string]string{
		"unknown property":  `"property_filters":[{"property":"X-FOO","text":"a"}]`,
		"unknown parameter": `"property_filters":[{"property":"ATTENDEE","parameter":{"name":"X","text":"a"}}]`,
		"unknown collation": `"property_filters":[{"property":"SUMMARY","text":"a","collation":"i;unicode-casemap"}]`,
		"text too long":     `"property_filters":[{"property":"SUMMARY","text":"` + long + `"}]`,
		"nine entries":      `"property_filters":[` + strings.Join(nine, ",") + `]`,
		"two conditions":    `"property_filters":[{"property":"CREATED","text":"a","defined":false}]`,
		"range on summary":  `"property_filters":[{"property":"SUMMARY","range":{"start":"2026-03-01T00:00:00Z","end":"2026-03-02T00:00:00Z"}}]`,
		"match type":        `"property_filters":[{"property":"SUMMARY","text":"a","match_type":"equals"}]`,
		"alarm too long":    `"alarm_range":{"start":"2026-01-01T00:00:00Z","end":"2027-06-01T00:00:00Z"}`,
		"alarm end first":   `"alarm_range":{"start":"2026-03-02T00:00:00Z","end":"2026-03-01T00:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			// A nil secret resolver proves the check precedes any secret access.
			_, err := invokeEventsList(context.Background(), resolved("calendar/work"), nil, nil,
				json.RawMessage(`{"calendar":"work",`+rangeArgs+`,`+filters+`}`))
			if err == nil {
				t.Error("want refusal")
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestEventsListExpandListsOccurrencesDeduplicatedAndLimited(t *testing.T) {
	series := icsOf(occurrence("s@example.com", "20260305T100000Z", "20260305T100000Z", "Daily"),
		occurrence("s@example.com", "20260303T100000Z", "20260303T100000Z", "Daily"),
		occurrence("s@example.com", "20260304T100000Z", "20260304T120000Z", "Moved"),
		occurrence("s@example.com", "20260303T100000Z", "20260303T100000Z", "Daily"))
	single := icsOf(vevent("UID:o@example.com", "DTSTART:20260301T080000Z", "SUMMARY:Once"))
	report := multistatus(eventEntry(workPath+"series.ics", "e1", series),
		eventEntry(workPath+"series.ics", "e1", series), eventEntry(workPath+"once.ics", "e2", single))
	calls := serve(t, eventsFake(t, report, nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, `{"calendar":"work",`+rangeArgs+`,"expand":true,"limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	page := out.(*EventsPage)
	if page.Count != 3 || !page.Truncated {
		t.Fatalf("page = %+v", page)
	}
	got := []string{page.Events[0].ID + "|" + page.Events[0].RecurrenceID, page.Events[1].ID + "|" + page.Events[1].RecurrenceID,
		page.Events[2].ID + "|" + page.Events[2].RecurrenceID}
	want := []string{"once.ics|", "series.ics|2026-03-03T10:00:00Z", "series.ics|2026-03-04T10:00:00Z"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("events = %v, want %v", got, want)
			break
		}
	}
	if page.Events[2].Start != "2026-03-04T12:00:00Z" || page.Events[2].ETag != "e1" {
		t.Errorf("moved occurrence = %+v", page.Events[2])
	}
	if !strings.Contains(lastReport(calls).body, `<c:calendar-data><c:expand start="20260228T230000Z" end="20260401T000000Z"/>`) {
		t.Errorf("body = %s", lastReport(calls).body)
	}
	raw, _ := json.Marshal(page.Events[1])
	if !strings.Contains(string(raw), `"recurrence_id":"2026-03-03T10:00:00Z"`) {
		t.Errorf("json = %s", raw)
	}
}

func TestEventsListWithoutExpandKeepsMainComponent(t *testing.T) {
	series := icsOf(vevent("UID:s", "DTSTART:20260303T100000Z", "SUMMARY:Main", "RRULE:FREQ=DAILY"),
		occurrence("s", "20260304T100000Z", "20260304T120000Z", "Moved"))
	serve(t, eventsFake(t, multistatus(eventEntry(workPath+"series.ics", "e1", series)), nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, allArgs)
	page := out.(*EventsPage)
	if err != nil || page.Count != 1 || page.Events[0].Summary != "Main" || page.Events[0].RecurrenceID != "" {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}

func TestEventsListCollationRefusalIsRequestError(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "REPORT" {
			return xmlResponse(403, bodyCanary), nil
		}
		return eventsFake(t, "", nil)(r)
	})
	_, err := listEventsOf(t, []string{"calendar/work"}, `{"calendar":"work",`+rangeArgs+
		`,"property_filters":[{"property":"SUMMARY","text":"a","collation":"i;octet"}]}`)
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "canary") {
		t.Errorf("err = %v", err)
	}
	_, err = listEventsOf(t, []string{"calendar/work"}, allArgs)
	if classOf(err) != provider.ClassPermission {
		t.Errorf("without collation err = %v, want permission", err)
	}
}

func TestEventsGetRefusesFilterArguments(t *testing.T) {
	calls := serve(t, eventsFake(t, "", nil))
	_, err := invokeEventsGet(context.Background(), resolved("calendar/work"), nil, nil,
		json.RawMessage(`{"calendar":"work","id":"a.ics","expand":true}`))
	if err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %v", err, *calls)
	}
}
