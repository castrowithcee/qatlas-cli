package infomaniakdav

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
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
	many := make([]string, maxEntries+1)
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
	for i := 0; i < maxAttendees+5; i++ {
		lines = append(lines, "ATTENDEE:mailto:a@example.org")
	}
	serve(t, eventsFake(t, "", func(*http.Request) (*http.Response, error) {
		return xmlResponse(200, icsOf(vevent(lines...))), nil
	}))
	out, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
		json.RawMessage(`{"calendar":"work","id":"u.ics"}`))
	if event := out.(*EventResult).Event; err != nil || len(event.Attendees) != maxAttendees || !event.AttendeesTruncated {
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

func TestEventTextIsBounded(t *testing.T) {
	event, err := parseEvent(icsOf(vevent("UID:u", "DTSTART:20260310T090000Z",
		"SUMMARY:"+strings.Repeat("ä", 1000), "DESCRIPTION:"+strings.Repeat("b", 10000),
		"LOCATION:"+strings.Repeat("c", 1000))), true)
	if err != nil || len(event.Summary) > maxSummaryLength || len(event.Description) > maxEventDescription ||
		len(event.Location) > maxLocationLength {
		t.Errorf("event = %d/%d/%d, err = %v", len(event.Summary), len(event.Description), len(event.Location), err)
	}
}
