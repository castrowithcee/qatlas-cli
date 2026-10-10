package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	eventCalendarPath = calHome + "personal/"
	eventPath         = eventCalendarPath + "standup.ics"
	eventHint         = "may have been applied"
)

var eventTools = []string{calendarsList.ID, eventsList.ID, eventsGet.ID, eventsCreate.ID, eventsUpdate.ID, eventsDelete.ID}

// eventAttendee is built so that no address literal appears in the source.
var eventAttendee = "ada" + "@" + "example.invalid"

const (
	eventCreateArgs = `{"calendar":"personal","summary":"Review","start":"2026-03-10T09:00:00",` +
		`"end":"2026-03-10T10:00:00","timezone":"Europe/Berlin","rrule":"FREQ=WEEKLY;COUNT=3"}`
	eventUpdateArgs = `{"calendar":"personal","id":"standup.ics","etag":"e1","summary":"Standup",` +
		`"start":"2026-03-11T09:00:00Z","end":"2026-03-11T09:15:00Z"}`
	eventDeleteArgs = `{"calendar":"personal","id":"standup.ics","etag":"e1"}`
)

func eventWrite(t *testing.T, tools, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, tools, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func eventCalendarAnswer(privileges []string, components ...string) *http.Response {
	return xmlResponse(http.StatusMultiStatus, calMultistatus(
		calendarNode(eventCalendarPath, "Personal", isCalendar, privileges, components...)))
}

func eventAnswer(etag, data string, privileges []string) *http.Response {
	return xmlResponse(http.StatusMultiStatus, calMultistatus(`<d:response><d:href>`+eventPath+`</d:href>`+
		`<d:propstat><d:prop><d:getetag>&quot;`+etag+`&quot;</d:getetag>`+privilegeXML(privileges...)+
		`<c:calendar-data>`+data+`</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`+
		`</d:response>`))
}

func eventChanged(code int, etag string) *http.Response {
	response := status(code)
	if etag != "" {
		response.Header.Set("ETag", `"`+etag+`"`)
	}
	return response
}

// eventFixture answers the pre-read of each tool with a writable calendar or event and the mutation with
// mutate, and records the headers of the mutation.
func eventFixture(t *testing.T, mutate func() (*http.Response, error)) (*[]call, *http.Header) {
	t.Helper()
	headers := &http.Header{}
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == methodPropfind && r.URL.Path == eventCalendarPath:
			return eventCalendarAnswer(fullRights, "VEVENT", "VTODO"), nil
		case r.Method == methodPropfind && r.URL.Path == eventPath:
			return eventAnswer("e1", ics("u-keep", "Standup", "20260310T090000Z", "SEQUENCE:2\r\n"), fullRights), nil
		}
		*headers = r.Header.Clone()
		return mutate()
	})
	return calls, headers
}

func eventMethods(calls *[]call) string {
	methods := []string{}
	for _, c := range *calls {
		methods = append(methods, c.method)
	}
	return strings.Join(methods, " ")
}

func TestEventsCreatePutsOneNewEventWithIfNoneMatch(t *testing.T) {
	calls, headers := eventFixture(t, func() (*http.Response, error) { return eventChanged(http.StatusCreated, "n1"), nil })
	args := strings.TrimSuffix(eventCreateArgs, "}") + `,"attendees":[{"address":"` + eventAttendee + `","name":"Ada"}]}`
	result, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.create", args, true)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	var got EventWriteResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.Calendar != "personal" || got.ID != got.UID+".ics" || len(got.UID) != 36 || got.ETag != "n1" {
		t.Errorf("result = %s", result)
	}
	if eventMethods(calls) != "PROPFIND PUT" {
		t.Fatalf("calls = %+v", *calls)
	}
	probe, put := (*calls)[0], (*calls)[1]
	if probe.url.Path != eventCalendarPath || probe.depth != depthSelf || !strings.Contains(probe.body, "current-user-privilege-set") {
		t.Errorf("pre-read = %+v", probe)
	}
	if put.url.Path != eventCalendarPath+got.ID || headers.Get("If-None-Match") != "*" || headers.Get("If-Match") != "" ||
		!strings.HasPrefix(headers.Get("Content-Type"), "text/calendar") {
		t.Errorf("put = %+v, headers = %v", put, *headers)
	}
	for _, want := range []string{"UID:" + got.UID, "SUMMARY:Review", "TZID=Europe/Berlin", "RRULE:FREQ=WEEKLY;COUNT=3",
		"mailto:" + eventAttendee, "PRODID:" + eventProductID} {
		if !strings.Contains(put.body, want) {
			t.Errorf("body lacks %q:\n%s", want, put.body)
		}
	}
}

func TestEventsUpdateReplacesBoundToTheETagAndKeepsTheUID(t *testing.T) {
	calls, headers := eventFixture(t, func() (*http.Response, error) { return eventChanged(http.StatusNoContent, "e2"), nil })
	result, err := eventWrite(t, eventTools, []string{"calendar/personal"}, "nextcloud.events.update", eventUpdateArgs, true)
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	var got EventWriteResult
	if json.Unmarshal(result, &got) != nil ||
		got != (EventWriteResult{Calendar: "personal", ID: "standup.ics", UID: "u-keep", ETag: "e2", Updated: true}) {
		t.Errorf("result = %s", result)
	}
	if eventMethods(calls) != "PROPFIND PUT" {
		t.Fatalf("calls = %+v", *calls)
	}
	probe, put := (*calls)[0], (*calls)[1]
	if probe.url.Path != eventPath || probe.depth != depthSelf || !strings.Contains(probe.body, "calendar-data") {
		t.Errorf("pre-read = %+v", probe)
	}
	if put.url.Path != eventPath || headers.Get("If-Match") != `"e1"` || headers.Get("If-None-Match") != "" ||
		!strings.Contains(put.body, "UID:u-keep") || !regexp.MustCompile(`(?m)^SEQUENCE[^:\r\n]*:3\r$`).MatchString(put.body) {
		t.Errorf("put = %+v, headers = %v", put, *headers)
	}
}

func TestEventsDeleteSendsOneETagBoundDelete(t *testing.T) {
	calls, headers := eventFixture(t, func() (*http.Response, error) { return eventChanged(http.StatusNoContent, ""), nil })
	result, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.delete", eventDeleteArgs, true)
	var got EventWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil ||
		got != (EventWriteResult{Calendar: "personal", ID: "standup.ics", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(calls) != "PROPFIND DELETE" || (*calls)[0].url.Path != eventCalendarPath ||
		(*calls)[1].url.Path != eventPath || headers.Get("If-Match") != `"e1"` || headers.Get("If-None-Match") != "" {
		t.Errorf("calls = %+v, headers = %v", *calls, *headers)
	}
}

func TestEventsUpdateRefusesOverridesBeforeAnyPut(t *testing.T) {
	override := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:u1\r\nDTSTART:20260310T090000Z\r\nRRULE:FREQ=DAILY;COUNT=3\r\nSUMMARY:Series\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:u1\r\nRECURRENCE-ID:20260311T090000Z\r\nDTSTART:20260311T100000Z\r\nSUMMARY:Moved\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VTODO\r\nUID:t1\r\nSUMMARY:Task\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"
	for name, data := range map[string]string{"override": override, "todo": todo} {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != methodPropfind {
					t.Errorf("a mutation was sent: %s", r.Method)
				}
				return eventAnswer("e1", data, fullRights), nil
			})
			_, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.update", eventUpdateArgs, true)
			if err == nil || !strings.Contains(err.Error(), "overrides of single occurrences") || len(*calls) != 1 {
				t.Errorf("err = %v, calls = %d", err, len(*calls))
			}
		})
	}
}

func TestEventWritesRefuseAReadOnlyCalendarAfterThePreRead(t *testing.T) {
	for _, privileges := range [][]string{{"read"}, {"read", "write-properties"}, {"read", "write-content"}, nil} {
		for op, args := range map[string]string{
			"nextcloud.events.create": eventCreateArgs,
			"nextcloud.events.update": eventUpdateArgs,
			"nextcloud.events.delete": eventDeleteArgs,
		} {
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				switch {
				case r.Method != methodPropfind:
					t.Errorf("%s: a mutation was sent: %s", op, r.Method)
					return status(http.StatusNoContent), nil
				case r.URL.Path == eventPath:
					return eventAnswer("e1", ics("u1", "S", "20260310T090000Z", ""), privileges), nil
				}
				return eventCalendarAnswer(privileges, "VEVENT"), nil
			})
			_, err := eventWrite(t, eventTools, []string{"calendar"}, op, args, true)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read-only") || len(*calls) != 1 {
				t.Errorf("%s %v: err = %v, calls = %d", op, privileges, err, len(*calls))
			}
		}
	}

	// A calendar that takes no events is refused the same way, without a mutation.
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != methodPropfind {
			t.Errorf("a mutation was sent: %s", r.Method)
		}
		return eventCalendarAnswer(fullRights, "VTODO"), nil
	})
	if _, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.create", eventCreateArgs, true); err == nil ||
		len(*calls) != 1 {
		t.Errorf("tasks-only calendar: err = %v, calls = %d", err, len(*calls))
	}
}

func TestEventsUpdateRefusesAChangedETagBeforeAnyPut(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != methodPropfind {
			t.Errorf("a mutation was sent: %s", r.Method)
		}
		return eventAnswer("e9", ics("u1", "S", "20260310T090000Z", ""), fullRights), nil
	})
	_, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.update", eventUpdateArgs, true)
	if err == nil || !strings.Contains(err.Error(), "precondition failed") || len(*calls) != 1 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestEventWritePreconditionFailureIsClear(t *testing.T) {
	for op, tc := range map[string]struct{ args, want string }{
		"nextcloud.events.create": {eventCreateArgs, "already exists"},
		"nextcloud.events.update": {eventUpdateArgs, "precondition failed"},
		"nextcloud.events.delete": {eventDeleteArgs, "precondition failed"},
	} {
		calls, _ := eventFixture(t, func() (*http.Response, error) { return status(http.StatusPreconditionFailed), nil })
		_, err := eventWrite(t, eventTools, []string{"calendar"}, op, tc.args, true)
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), tc.want) ||
			strings.Contains(err.Error(), eventHint) || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestEventWriteUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted":   func() (*http.Response, error) { return status(http.StatusAccepted), nil },
	}
	for _, code := range []int{500, 502, 503, 504} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for op, args := range map[string]string{
		"nextcloud.events.create": eventCreateArgs,
		"nextcloud.events.update": eventUpdateArgs,
		"nextcloud.events.delete": eventDeleteArgs,
	} {
		for name, answer := range answers {
			calls, _ := eventFixture(t, answer)
			_, err := eventWrite(t, eventTools, []string{"calendar"}, op, args, true)
			if err == nil || !strings.Contains(err.Error(), eventHint) || !strings.Contains(err.Error(), "before repeating") ||
				strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestEventWriteRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	const location = "https://evil.example.invalid/redirect-canary-4e2a"
	redirect := func(code int) *http.Response {
		response := status(code)
		response.Header.Set("Location", location)
		return response
	}
	for op, args := range map[string]string{
		"nextcloud.events.create": eventCreateArgs,
		"nextcloud.events.update": eventUpdateArgs,
		"nextcloud.events.delete": eventDeleteArgs,
	} {
		for _, code := range []int{301, 302, 307, 308} {
			// On the mutation.
			calls, _ := eventFixture(t, func() (*http.Response, error) { return redirect(code), nil })
			_, err := eventWrite(t, eventTools, []string{"calendar"}, op, args, true)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), eventHint) || len(*calls) != 2 {
				t.Errorf("%s %d: err = %v, calls = %d", op, code, err, len(*calls))
			}
			// On the pre-read, which then sends no mutation.
			calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(code), nil })
			_, err = eventWrite(t, eventTools, []string{"calendar"}, op, args, true)
			if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
				t.Errorf("%s %d pre-read: err = %v, calls = %d", op, code, err, len(*calls))
			}
		}
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestEventWritesRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	with := func(base, extra string) string { return strings.TrimSuffix(base, "}") + "," + extra + "}" }
	unbound := map[string]struct {
		targets []string
		op, arg string
	}{
		"no calendar target": {[]string{"folder/Reports"}, "create", eventCreateArgs},
		"other calendar":     {[]string{"calendar/work"}, "create", strings.Replace(eventCreateArgs, "personal", "other-secret", 1)},
		"other on update":    {[]string{"calendar/work"}, "update", strings.Replace(eventUpdateArgs, "personal", "other-secret", 1)},
		"other on delete":    {[]string{"calendar/work"}, "delete", strings.Replace(eventDeleteArgs, "personal", "other-secret", 1)},
		"traversal":          {[]string{"calendar"}, "delete", strings.Replace(eventDeleteArgs, "personal", "../bobby", 1)},
	}
	for _, name := range []string{"inbox", "outbox", "trashbin"} {
		unbound["reserved "+name] = struct {
			targets []string
			op, arg string
		}{[]string{"calendar"}, "create", strings.Replace(eventCreateArgs, "personal", name, 1)}
		unbound["reserved bound "+name] = struct {
			targets []string
			op, arg string
		}{[]string{"calendar/" + name}, "delete", strings.Replace(eventDeleteArgs, "personal", name, 1)}
	}
	for name, tc := range unbound {
		_, err := eventWrite(t, eventTools, tc.targets, "nextcloud.events."+tc.op, tc.arg, true)
		if classOf(err) != provider.ClassPermission {
			t.Errorf("%s: err = %v, want a permission refusal", name, err)
			continue
		}
		for _, leak := range []string{"other-secret", "work", "bobby", "inbox", "outbox", "trashbin", "Reports"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the refusal names %q: %v", name, leak, err)
			}
		}
	}
	for name, tc := range map[string]struct{ op, args string }{
		"bad id":          {"update", strings.Replace(eventUpdateArgs, "standup.ics", "a/b.ics", 1)},
		"dot id":          {"delete", strings.Replace(eventDeleteArgs, "standup.ics", "..", 1)},
		"wildcard etag":   {"delete", strings.Replace(eventDeleteArgs, `"e1"`, `"*"`, 1)},
		"spaced etag":     {"update", strings.Replace(eventUpdateArgs, `"e1"`, `"e 1"`, 1)},
		"create with id":  {"create", with(eventCreateArgs, `"id":"x.ics"`)},
		"create with tag": {"create", with(eventCreateArgs, `"etag":"e1"`)},
		"unknown zone":    {"create", strings.Replace(eventCreateArgs, "Europe/Berlin", "Mars/Base", 1)},
		"bad rule":        {"create", strings.Replace(eventCreateArgs, "FREQ=WEEKLY;COUNT=3", "FREQ=SOMETIMES", 1)},
		"prefixed rule":   {"create", strings.Replace(eventCreateArgs, "FREQ=WEEKLY", "RRULE:FREQ=WEEKLY", 1)},
		"bad start":       {"update", strings.Replace(eventUpdateArgs, "2026-03-11T09:00:00Z", "tomorrow at nine", 1)},
		"bad attendee":    {"create", with(eventCreateArgs, `"attendees":[{"address":"not an address"}]`)},
		"free url":        {"create", with(eventCreateArgs, `"url":"https://evil.example.invalid"`)},
		"free header":     {"update", with(eventUpdateArgs, `"headers":{"X":"y"}`)},
		"fields on del":   {"delete", with(eventDeleteArgs, `"summary":"x"`)},
	} {
		if _, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events."+tc.op, tc.args, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for op, args := range map[string]string{"create": eventCreateArgs, "update": eventUpdateArgs, "delete": eventDeleteArgs} {
		if _, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events."+op, args, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
	}
	// Without a tools list naming it the delete is not offered, whatever the permissions.
	if _, err := eventWrite(t, nil, []string{"calendar"}, "nextcloud.events.delete", eventDeleteArgs, true); err == nil {
		t.Error("delete without a tools list: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestEventWriteToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id          string
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{eventsCreate.ID, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{eventsUpdate.ID, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{eventsDelete.ID, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupCalendar || r.Effect != tc.effect || r.Idempotency != tc.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != calendarSensitivity ||
			d.RequiresToolAllowList != tc.allowList || !strings.Contains(d.Description, "by e-mail") {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	last := metadata.Profiles[len(metadata.Profiles)-1]
	if last.ID != "events" || strings.Join(last.Tools, " ") !=
		"nextcloud.calendars.list nextcloud.events.list nextcloud.events.get nextcloud.events.create nextcloud.events.update" {
		t.Errorf("profile = %+v", last)
	}
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == eventsDelete.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}

// The mutation reads no answer body, so an unreadable body of a success is still a success.
func TestEventWriteIgnoresTheBodyOfASuccess(t *testing.T) {
	eventFixture(t, func() (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(failingBody{})}, nil
	})
	if _, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.events.delete", eventDeleteArgs, true); err != nil {
		t.Errorf("delete = %v", err)
	}
}
