package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	calHome        = "/remote.php/dav/calendars/" + aliceUser + "/"
	foreignCalName = "foreign-calendar-canary-5d2c"
)

func calMultistatus(entries ...string) string {
	return `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" ` +
		`xmlns:cs="http://calendarserver.org/ns/">` + strings.Join(entries, "") + `</d:multistatus>`
}

func privilegeXML(names ...string) string {
	var b strings.Builder
	b.WriteString(`<d:current-user-privilege-set>`)
	for _, name := range names {
		b.WriteString(`<d:privilege><d:` + name + `/></d:privilege>`)
	}
	b.WriteString(`</d:current-user-privilege-set>`)
	return b.String()
}

// calendarNode is one child of the calendar home. kind is the extra resource type element.
func calendarNode(href, name, kind string, privileges []string, components ...string) string {
	set := ""
	if len(components) > 0 {
		set = `<c:supported-calendar-component-set>`
		for _, component := range components {
			set += `<c:comp name="` + component + `"/>`
		}
		set += `</c:supported-calendar-component-set>`
	}
	priv := ""
	if privileges != nil {
		priv = privilegeXML(privileges...)
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:displayname>` + name +
		`</d:displayname><d:resourcetype><d:collection/>` + kind + `</d:resourcetype>` + priv + set +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

const isCalendar = `<c:calendar/>`

var fullRights = []string{"read", "write", "write-properties", "write-content", "bind", "unbind"}

func eventNode(href, etag, ics string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:getetag>&quot;` + etag +
		`&quot;</d:getetag><c:calendar-data>` + ics + `</c:calendar-data></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func ics(uid, summary, start, extra string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nSUMMARY:" + summary + "\r\nDTSTART:" + start + "\r\nDTEND:" + start + "\r\n" + extra +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
}

func calInvoke(t *testing.T, targets []string, operation, args string) (json.RawMessage, error) {
	t.Helper()
	response, err := accountClient(t, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args),
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func homeListing() string {
	return calMultistatus(
		calendarNode(calHome, "Home", "", nil),
		calendarNode(calHome+"personal/", "Personal", isCalendar, fullRights, "VEVENT", "VTODO"),
		calendarNode(calHome+"team_shared_by_bob/", "Team", isCalendar, []string{"read"}, "VEVENT"),
		calendarNode(calHome+"inbox/", "Inbox", `<c:schedule-inbox/>`, []string{"read", "write"}),
		calendarNode(calHome+"outbox/", "Outbox", isCalendar, fullRights, "VEVENT"),
		calendarNode(calHome+"trashbin/", "Trash", isCalendar, fullRights, "VEVENT"),
		calendarNode(calHome+"subscribed/", "Feed", `<cs:subscribed/>`, []string{"read"}),
		calendarNode("https://evil.example.invalid"+calHome+"hostile/", foreignCalName, isCalendar, fullRights, "VEVENT"),
		calendarNode(calHome+"personal/deeper/", foreignCalName, isCalendar, fullRights, "VEVENT"),
		calendarNode("/remote.php/dav/calendars/bobby/theirs/", foreignCalName, isCalendar, fullRights, "VEVENT"),
		calendarNode("/remote.php/dav/files/alice/", foreignCalName, isCalendar, fullRights, "VEVENT"),
	)
}

func TestCalendarsListReportsOnlyBoundCalendarsOfTheHome(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, homeListing()), nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendars.list", `{}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var listed CalendarsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Count != 2 || listed.Calendars[0].ID != "personal" || listed.Calendars[1].ID != "team_shared_by_bob" ||
		listed.Calendars[0].ReadOnly || !listed.Calendars[1].ReadOnly ||
		strings.Join(listed.Calendars[0].Components, ",") != "VEVENT,VTODO" {
		t.Errorf("result = %s", result)
	}
	if strings.Contains(string(result), foreignCalName) {
		t.Errorf("a foreign node was reported: %s", result)
	}
	request := (*calls)[0]
	if len(*calls) != 1 || request.method != "PROPFIND" || request.depth != depthChildren ||
		request.url.Path != calHome || !strings.Contains(request.body, "current-user-privilege-set") ||
		request.auth == "" {
		t.Errorf("calls = %+v", *calls)
	}

	// A single calendar target narrows the listing to that calendar.
	result, err = calInvoke(t, []string{"calendar/team_shared_by_bob"}, "nextcloud.calendars.list", `{}`)
	if err != nil || !strings.Contains(string(result), `"count":1`) || strings.Contains(string(result), "personal") {
		t.Errorf("narrowed = %s, %v", result, err)
	}
}

func TestReservedCollectionsAreNeverCalendars(t *testing.T) {
	for _, name := range []string{"inbox", "outbox", "trashbin"} {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return xmlResponse(http.StatusMultiStatus, homeListing()), nil
		})
		// Bound by name, the collection is neither listed nor addressable.
		result, err := calInvoke(t, []string{"calendar/" + name}, "nextcloud.calendars.list", `{}`)
		if err != nil || !strings.Contains(string(result), `"count":0`) {
			t.Errorf("list %s = %s, %v", name, result, err)
		}
		before := len(*calls)
		for op, args := range map[string]string{
			"nextcloud.events.list": `{"calendar":"` + name + `","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}`,
			"nextcloud.events.get":  `{"calendar":"` + name + `","id":"a.ics"}`,
		} {
			if _, err := calInvoke(t, []string{"calendar/" + name}, op, args); classOf(err) != provider.ClassPermission {
				t.Errorf("%s %s = %v, want a permission refusal", op, name, err)
			}
			if _, err := calInvoke(t, []string{"calendar"}, op, args); classOf(err) != provider.ClassPermission {
				t.Errorf("%s %s with calendar = %v, want a permission refusal", op, name, err)
			}
		}
		if len(*calls) != before {
			t.Errorf("%s: requests after the refusals = %d", name, len(*calls)-before)
		}
	}
}

func TestReadOnlyFollowsTheConservativePrivilegeRule(t *testing.T) {
	for name, tc := range map[string]struct {
		privileges []string
		readOnly   bool
	}{
		"all":                    {[]string{"all"}, false},
		"write":                  {[]string{"read", "write"}, false},
		"bind and write-content": {[]string{"read", "bind", "write-content"}, false},
		"write-content only":     {[]string{"read", "write-content"}, true},
		"bind only":              {[]string{"read", "bind"}, true},
		"write-properties only":  {[]string{"read", "write-properties"}, true},
		"read only":              {[]string{"read", "read-acl"}, true},
		"not reported":           {nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return xmlResponse(http.StatusMultiStatus, calMultistatus(
					calendarNode(calHome, "Home", "", nil),
					calendarNode(calHome+"one/", "One", isCalendar, tc.privileges, "VEVENT"))), nil
			})
			result, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendars.list", `{}`)
			var listed CalendarsResult
			if err != nil || json.Unmarshal(result, &listed) != nil || len(listed.Calendars) != 1 ||
				listed.Calendars[0].ReadOnly != tc.readOnly {
				t.Errorf("result = %s, %v", result, err)
			}
		})
	}
}

func TestUnboundCalendarIsRefusedBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, nil
	})
	rangeArgs := `"start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"`
	for name, tc := range map[string]struct {
		targets []string
		op, arg string
	}{
		"list, no calendar target": {[]string{"folder/Reports"}, "nextcloud.calendars.list", `{}`},
		"events, no target":        {[]string{"folder/Reports"}, "nextcloud.events.list", `{"calendar":"personal",` + rangeArgs + `}`},
		"get, no target":           {[]string{"account"}, "nextcloud.events.get", `{"calendar":"personal","id":"a.ics"}`},
		"events, other calendar":   {[]string{"calendar/work"}, "nextcloud.events.list", `{"calendar":"other-secret",` + rangeArgs + `}`},
		"get, other calendar":      {[]string{"calendar/work"}, "nextcloud.events.get", `{"calendar":"other-secret","id":"a.ics"}`},
		"case differs":             {[]string{"calendar/work"}, "nextcloud.events.get", `{"calendar":"Work","id":"a.ics"}`},
		"traversal calendar":       {[]string{"calendar"}, "nextcloud.events.get", `{"calendar":"../bobby","id":"a.ics"}`},
		"separator calendar":       {[]string{"calendar"}, "nextcloud.events.list", `{"calendar":"a/b",` + rangeArgs + `}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := calInvoke(t, tc.targets, tc.op, tc.arg)
			if classOf(err) != provider.ClassPermission {
				t.Fatalf("err = %v, want a permission refusal", err)
			}
			for _, leak := range []string{"other-secret", "work", "Reports", "folder", "personal"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the refusal names %q: %v", leak, err)
				}
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestEventIDAndRangeAreValidatedBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, nil
	})
	for _, id := range []string{"../x.ics", "a/b.ics", "%2e%2e", "..", ".", "a\\b.ics", "a\u0001.ics", strings.Repeat("a", 256)} {
		body, _ := json.Marshal(map[string]string{"calendar": "personal", "id": id})
		if _, err := calInvoke(t, []string{"calendar"}, "nextcloud.events.get", string(body)); err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
	for name, args := range map[string]string{
		"too long":    `{"calendar":"personal","start":"2026-01-01T00:00:00Z","end":"2027-03-01T00:00:00Z"}`,
		"backwards":   `{"calendar":"personal","start":"2026-02-01T00:00:00Z","end":"2026-01-01T00:00:00Z"}`,
		"not a time":  `{"calendar":"personal","start":"yesterday at noon","end":"2026-01-01T00:00:00Z"}`,
		"limit":       `{"calendar":"personal","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z","limit":500}`,
		"free filter": `{"calendar":"personal","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z","filter":"x"}`,
	} {
		if _, err := calInvoke(t, []string{"calendar"}, "nextcloud.events.list", args); err == nil {
			t.Errorf("%s: the arguments were accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestEventsListSendsOneFixedReportAndDropsForeignNodes(t *testing.T) {
	base := calHome + "personal/"
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			eventNode(base+"late.ics", "e2", ics("late", "Late", "20260320T100000Z", "")),
			eventNode(base+"early.ics", "e1", ics("early", "Early", "20260302T090000Z", "RRULE:FREQ=WEEKLY\r\n")),
			eventNode(base+"early.ics", "dup", ics("dup", "Duplicate", "20260301T090000Z", "")),
			eventNode("https://evil.example.invalid"+base+"host.ics", "x", ics("h", "Foreign host", "20260303T090000Z", "")),
			eventNode(base+"sub/deep.ics", "x", ics("d", "Too deep", "20260303T090000Z", "")),
			eventNode(calHome+"work/other.ics", "x", ics("o", "Other calendar", "20260303T090000Z", "")),
			eventNode(base+"broken.ics", "x", "not an iCalendar object"),
		)), nil
	})
	result, err := calInvoke(t, []string{"calendar/personal"}, "nextcloud.events.list",
		`{"calendar":"personal","start":"2026-03-01T00:00:00+01:00","end":"2026-04-01T00:00:00Z","limit":1}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page EventsPage
	if err := json.Unmarshal(result, &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || !page.Truncated || page.Events[0].ID != "early.ics" || page.Events[0].RRule != "FREQ=WEEKLY" ||
		page.Events[0].Summary != "Early" || page.Events[0].ETag != "e1" {
		t.Errorf("page = %s", result)
	}
	request := (*calls)[0]
	if len(*calls) != 1 || request.method != "REPORT" || request.depth != depthChildren || request.url.Path != base ||
		!strings.Contains(request.body, `start="20260228T230000Z" end="20260401T000000Z"`) ||
		strings.Contains(request.body, "expand") {
		t.Errorf("calls = %+v", *calls)
	}
	for _, leak := range []string{"Foreign host", "Too deep", "Other calendar"} {
		if strings.Contains(string(result), leak) {
			t.Errorf("a foreign node was reported: %s", result)
		}
	}
}

func TestEventsGetReadsOneEventAtAFixedPath(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		response := xmlResponse(http.StatusOK, ics("u1", "Planning", "20260310T090000Z",
			"RRULE:FREQ=DAILY;COUNT=3\r\nDESCRIPTION:Agenda\r\nORGANIZER;CN=Dora:mailto:dora@example.invalid\r\n"))
		response.Header = http.Header{"Etag": []string{`"abc123"`}}
		return response, nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.events.get",
		`{"calendar":"team_shared_by_bob","id":"my event.ics"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got EventResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Event.ID != "my event.ics" || got.Event.RRule != "FREQ=DAILY;COUNT=3" || got.Event.Description != "Agenda" ||
		got.Event.ETag != "abc123" || got.Event.Organizer == nil || got.Event.Organizer.Address != "dora@example.invalid" {
		t.Errorf("result = %s", result)
	}
	request := (*calls)[0]
	if len(*calls) != 1 || request.method != http.MethodGet || request.body != "" ||
		request.url.EscapedPath() != calHome+"team_shared_by_bob/my%20event.ics" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestCalendarRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	const location = "https://evil.example.invalid/redirect-canary-9f1e"
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		serve(t, func(*http.Request) (*http.Response, error) {
			response := xmlResponse(status, "")
			response.Header.Set("Location", location)
			return response, nil
		})
		for op, args := range map[string]string{
			"nextcloud.calendars.list": `{}`,
			"nextcloud.events.list":    `{"calendar":"personal","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}`,
			"nextcloud.events.get":     `{"calendar":"personal","id":"a.ics"}`,
		} {
			_, err := calInvoke(t, []string{"calendar"}, op, args)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "redirect-canary") || strings.Contains(err.Error(), "evil") {
				t.Errorf("%s %d = %v", op, status, err)
			}
		}
	}
}

func TestCalendarRequestsFollowTheInstallationPrefix(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus()), nil
	})
	resolved := resolvedConnection("partner", "partner-reader", carolUserEnv, carolTokenEnv, partnerInstance, "")
	resolved.Targets = []string{"calendar"}
	red := &redact.Redactor{}
	if _, err := invokeCalendarsList(context.Background(), resolved, resolver(red), red, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].url.String(); got != partnerInstance+"/remote.php/dav/calendars/"+carolUser+"/" {
		t.Errorf("URL = %s", got)
	}
}

func TestConnectionTestReadsEachBoundCalendarAtDepthZero(t *testing.T) {
	respond := func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/dav/files/") {
			return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(r.URL.EscapedPath(), "Reports", "1", "1"))), nil
		}
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			calendarNode(r.URL.EscapedPath(), "Any", isCalendar, nil))), nil
	}
	red := &redact.Redactor{}

	calls := serve(t, respond)
	class, err := TestConnection(context.Background(), listed("folder/Reports", "calendar/work", "calendar/home"), resolver(red), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection = %q, %v", class, err)
	}
	var seen []string
	for _, c := range *calls {
		if c.method != "PROPFIND" || c.depth != depthSelf {
			t.Errorf("call = %+v", c)
		}
		seen = append(seen, c.url.Path)
	}
	want := []string{"/remote.php/dav/files/alice/Reports", calHome + "work/", calHome + "home/"}
	if strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Errorf("paths = %v, want %v", seen, want)
	}

	calls = serve(t, respond)
	class, err = TestConnection(context.Background(), listed("calendar"), resolver(red), red)
	if err != nil || class != provider.ClassOK || len(*calls) != 2 || (*calls)[1].url.Path != calHome ||
		(*calls)[1].depth != depthSelf {
		t.Errorf("calendar home: %q, %v, %+v", class, err, *calls)
	}

	// A folder-only connection makes no calendar request.
	calls = serve(t, respond)
	if class, err = TestConnection(context.Background(), listed("folder/Reports"), resolver(red), red); err != nil ||
		class != provider.ClassOK || len(*calls) != 1 {
		t.Errorf("folder only: %q, %v, %+v", class, err, *calls)
	}
}

func TestConnectionTestClassifiesCalendarFailuresWithoutTargets(t *testing.T) {
	red := &redact.Redactor{}
	files := func(r *http.Request) *http.Response {
		return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(r.URL.EscapedPath(), "Reports", "1", "1")))
	}
	for name, tc := range map[string]struct {
		targets []string
		answer  func() *http.Response
		class   provider.Class
		message string
	}{
		"missing calendar": {[]string{"calendar/secret-cal"}, func() *http.Response { return xmlResponse(http.StatusNotFound, bodyCanary) },
			"", "does not hold a calendar"},
		"missing home": {[]string{"calendar"}, func() *http.Response { return xmlResponse(http.StatusNotFound, bodyCanary) },
			"", "no calendar home"},
		"not a calendar": {[]string{"calendar/secret-cal"}, func() *http.Response {
			return xmlResponse(http.StatusMultiStatus, calMultistatus(calendarNode(calHome+"secret-cal/", "x", "", nil)))
		}, provider.ClassInvalidResponse, ""},
		"forbidden": {[]string{"calendar"}, func() *http.Response { return xmlResponse(http.StatusForbidden, bodyCanary) },
			provider.ClassPermission, ""},
		"redirect": {[]string{"calendar"}, func() *http.Response {
			response := xmlResponse(http.StatusFound, "")
			response.Header.Set("Location", "https://evil.example.invalid/x")
			return response
		}, provider.ClassProviderError, ""},
		"reserved": {[]string{"calendar/inbox"}, nil, "", "not a calendar"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/dav/files/") {
					return files(r), nil
				}
				return tc.answer(), nil
			})
			class, err := TestConnection(context.Background(), listed(tc.targets...), resolver(red), red)
			if class != tc.class {
				t.Errorf("class = %q, want %q (err %v)", class, tc.class, err)
			}
			if tc.message != "" && (err == nil || !strings.Contains(err.Error(), tc.message)) {
				t.Errorf("err = %v, want %q", err, tc.message)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-cal") || strings.Contains(err.Error(), bodyCanary)) {
				t.Errorf("the error names a target or provider text: %v", err)
			}
			if name == "reserved" {
				for _, c := range *calls {
					if strings.Contains(c.url.Path, "inbox") {
						t.Errorf("a reserved collection was requested: %s", c.url.Path)
					}
				}
			}
		})
	}
}

func TestCalendarToolsAreGroupedAndProfiled(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var tools []string
	for _, p := range metadata.Profiles {
		if p.ID == "calendar" {
			tools = p.Tools
		}
	}
	if strings.Join(tools, " ") != "nextcloud.calendars.list nextcloud.events.list nextcloud.events.get" {
		t.Errorf("profile tools = %v", tools)
	}
	for _, d := range []string{calendarsList.ID, eventsList.ID, eventsGet.ID} {
		descriptor, _, ok := reg.Lookup(d)
		if !ok || descriptor.Group != groupCalendar || descriptor.Risk.DataSensitivity != calendarSensitivity {
			t.Errorf("%s = %+v, %v", d, descriptor, ok)
		}
	}
}
