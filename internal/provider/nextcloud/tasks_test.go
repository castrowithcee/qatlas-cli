package nextcloud

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func todo(uid, summary, extra string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VTODO\r\nUID:" + uid +
		"\r\nSUMMARY:" + summary + "\r\n" + extra + "END:VTODO\r\nEND:VCALENDAR\r\n"
}

func taskCalendarProbe(components ...string) string {
	return calMultistatus(calendarNode(calHome+"tasks/", "Tasks", isCalendar, fullRights, components...))
}

// taskServer answers the support probe with the given components and every REPORT with report.
func taskServer(t *testing.T, report string, components ...string) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "REPORT" {
			return xmlResponse(http.StatusMultiStatus, report), nil
		}
		return xmlResponse(http.StatusMultiStatus, taskCalendarProbe(components...)), nil
	})
}

func TestTasksListSendsOneFixedReportPerFilter(t *testing.T) {
	const head = `<?xml version="1.0" encoding="UTF-8"?><c:calendar-query xmlns:d="DAV:" ` +
		`xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><c:calendar-data/></d:prop>` +
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VTODO">`
	const tail = `</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
	for name, tc := range map[string]struct{ args, filter string }{
		"none": {`{"calendar":"tasks"}`, ``},
		"open": {`{"calendar":"tasks","state":"open"}`,
			`<c:prop-filter name="COMPLETED"><c:is-not-defined/></c:prop-filter>`},
		"completed": {`{"calendar":"tasks","state":"completed"}`, `<c:prop-filter name="COMPLETED"/>`},
		"due range": {`{"calendar":"tasks","due_from":"2026-03-01T00:00:00+01:00","due_to":"2026-04-01T00:00:00Z"}`,
			`<c:prop-filter name="DUE"><c:time-range start="20260228T230000Z" end="20260401T000000Z"/></c:prop-filter>`},
		"due from only": {`{"calendar":"tasks","due_from":"2026-03-01T00:00:00Z"}`,
			`<c:prop-filter name="DUE"><c:time-range start="20260301T000000Z"/></c:prop-filter>`},
		"combined": {`{"calendar":"tasks","state":"open","due_to":"2026-04-01T00:00:00Z"}`,
			`<c:prop-filter name="DUE"><c:time-range end="20260401T000000Z"/></c:prop-filter><c:prop-filter name="COMPLETED"><c:is-not-defined/></c:prop-filter>`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := taskServer(t, calMultistatus(), "VTODO")
			if _, err := calInvoke(t, []string{"calendar"}, "nextcloud.tasks.list", tc.args); err != nil {
				t.Fatalf("invoke = %v", err)
			}
			if len(*calls) != 2 || (*calls)[0].method != "PROPFIND" || (*calls)[0].depth != depthSelf ||
				(*calls)[0].url.Path != calHome+"tasks/" {
				t.Fatalf("calls = %+v", *calls)
			}
			report := (*calls)[1]
			if report.method != "REPORT" || report.depth != depthChildren || report.url.Path != calHome+"tasks/" ||
				report.body != head+tc.filter+tail {
				t.Errorf("report = %+v", report)
			}
		})
	}
}

func TestTasksListReportsFieldsDropsForeignNodesAndFiltersState(t *testing.T) {
	base := calHome + "tasks/"
	report := calMultistatus(
		eventNode(base+"late.ics", "e2", todo("late", "Late", "DUE:20260320T100000Z\r\nSTATUS:NEEDS-ACTION\r\n")),
		eventNode(base+"early.ics", "e1", todo("early", "Early",
			"DUE;VALUE=DATE:20260302\r\nSTATUS:IN-PROCESS\r\nPRIORITY:3\r\nPERCENT-COMPLETE:40\r\n"+
				"CATEGORIES:home,errand\r\nRELATED-TO:parent-1\r\nDESCRIPTION:"+strings.Repeat("d", 600)+"\r\n")),
		eventNode(base+"nodue.ics", "e3", todo("nodue", "No due", "")),
		eventNode(base+"done.ics", "e4", todo("done", "Done", "STATUS:COMPLETED\r\nCOMPLETED:20260301T080000Z\r\n")),
		eventNode(base+"early.ics", "dup", todo("dup", "Duplicate", "")),
		eventNode("https://evil.example.invalid"+base+"host.ics", "x", todo("h", "Foreign host", "")),
		eventNode(base+"sub/deep.ics", "x", todo("d", "Too deep", "")),
		eventNode(calHome+"work/other.ics", "x", todo("o", "Other calendar", "")),
		eventNode(base+"event.ics", "x", ics("ev", "Only an event", "20260303T090000Z", "")),
		eventNode(base+"broken.ics", "x", "not an iCalendar object"),
	)
	taskServer(t, report, "VTODO")
	result, err := calInvoke(t, []string{"calendar/tasks"}, "nextcloud.tasks.list", `{"calendar":"tasks","state":"open"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page TasksPage
	if err := json.Unmarshal(result, &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 3 || page.Truncated || page.Tasks[0].ID != "early.ics" || page.Tasks[1].ID != "late.ics" ||
		page.Tasks[2].ID != "nodue.ics" {
		t.Fatalf("page = %s", result)
	}
	early := page.Tasks[0]
	if early.UID != "early" || early.Title != "Early" || early.Status != "IN-PROCESS" || early.Priority != 3 ||
		early.Percent != 40 || early.Due != "2026-03-02" || early.ParentUID != "parent-1" || early.ETag != "e1" ||
		strings.Join(early.Categories, ",") != "home,errand" || len(early.Description) != taskListDescription {
		t.Errorf("early = %+v", early)
	}
	for _, leak := range []string{"Foreign host", "Too deep", "Other calendar", "Only an event", "Duplicate", "Done"} {
		if strings.Contains(string(result), leak) {
			t.Errorf("%q was reported: %s", leak, result)
		}
	}

	// The completed filter applies again to what the server returned.
	result, err = calInvoke(t, []string{"calendar/tasks"}, "nextcloud.tasks.list", `{"calendar":"tasks","state":"completed"}`)
	if err != nil || !strings.Contains(string(result), `"count":1`) || !strings.Contains(string(result), `"completed":"2026-03-01T08:00:00Z"`) {
		t.Errorf("completed = %s, %v", result, err)
	}
}

func TestTasksListCapsCountAndReportsTruncation(t *testing.T) {
	base := calHome + "tasks/"
	var nodes []string
	for _, id := range []string{"c", "a", "b"} {
		nodes = append(nodes, eventNode(base+id+".ics", id, todo(id, id, "")))
	}
	taskServer(t, calMultistatus(nodes...), "VTODO")
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","limit":2}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page TasksPage
	if err := json.Unmarshal(result, &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || !page.Truncated || page.Tasks[0].ID != "a.ics" || page.Tasks[1].ID != "b.ics" {
		t.Errorf("page = %s", result)
	}
}

func TestTasksGetReadsOneTaskAtAFixedPathWithCappedStrings(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			response := xmlResponse(http.StatusOK, todo("u1", strings.Repeat("t", 400),
				"DESCRIPTION:"+strings.Repeat("d", 6000)+"\r\nDUE:20260310T090000Z\r\n"+
					"RELATED-TO;RELTYPE=CHILD:kid\r\nRELATED-TO;RELTYPE=PARENT:mom\r\n"))
			response.Header = http.Header{"Etag": []string{`"abc123"`}}
			return response, nil
		}
		return xmlResponse(http.StatusMultiStatus, taskCalendarProbe("VEVENT", "VTODO")), nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.tasks.get", `{"calendar":"tasks","id":"my task.ics"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got TaskResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Task.ID != "my task.ics" || got.Task.ETag != "abc123" || got.Task.Due != "2026-03-10T09:00:00Z" ||
		got.Task.ParentUID != "mom" || len(got.Task.Title) != 256 || len(got.Task.Description) != 4096 {
		t.Errorf("result = %s", result)
	}
	request := (*calls)[1]
	if len(*calls) != 2 || request.method != http.MethodGet || request.body != "" ||
		request.url.EscapedPath() != calHome+"tasks/my%20task.ics" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestTasksRefuseACalendarWithoutTaskSupport(t *testing.T) {
	for op, args := range map[string]string{
		"nextcloud.tasks.list": `{"calendar":"tasks"}`,
		"nextcloud.tasks.get":  `{"calendar":"tasks","id":"a.ics"}`,
	} {
		calls := taskServer(t, calMultistatus(), "VEVENT")
		_, err := calInvoke(t, []string{"calendar"}, op, args)
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "does not support tasks") {
			t.Errorf("%s = %v", op, err)
		}
		if len(*calls) != 1 || (*calls)[0].method != "PROPFIND" {
			t.Errorf("%s calls = %+v", op, *calls)
		}
	}
}

func TestTasksAreRefusedBeforeAnyRequestWhenUnboundOrInvalid(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, nil
	})
	for name, tc := range map[string]struct {
		targets []string
		op, arg string
		class   provider.Class
	}{
		"list, no calendar target": {[]string{"folder/Reports"}, "nextcloud.tasks.list", `{"calendar":"tasks"}`, provider.ClassPermission},
		"list, other calendar":     {[]string{"calendar/work"}, "nextcloud.tasks.list", `{"calendar":"other-secret"}`, provider.ClassPermission},
		"get, other calendar":      {[]string{"calendar/work"}, "nextcloud.tasks.get", `{"calendar":"other-secret","id":"a.ics"}`, provider.ClassPermission},
		"reserved calendar":        {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"inbox"}`, provider.ClassPermission},
		"traversal calendar":       {[]string{"calendar"}, "nextcloud.tasks.get", `{"calendar":"../bobby","id":"a.ics"}`, provider.ClassPermission},
		"traversal id":             {[]string{"calendar"}, "nextcloud.tasks.get", `{"calendar":"tasks","id":"../x.ics"}`, ""},
		"separator id":             {[]string{"calendar"}, "nextcloud.tasks.get", `{"calendar":"tasks","id":"a/b.ics"}`, ""},
		"unknown state":            {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","state":"all"}`, ""},
		"bad due":                  {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","due_from":"yesterday at noon"}`, ""},
		"backwards due":            {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","due_from":"2026-02-01T00:00:00Z","due_to":"2026-01-01T00:00:00Z"}`, ""},
		"limit":                    {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","limit":500}`, ""},
		"free filter":              {[]string{"calendar"}, "nextcloud.tasks.list", `{"calendar":"tasks","filter":"x"}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := calInvoke(t, tc.targets, tc.op, tc.arg)
			if err == nil || tc.class != "" && classOf(err) != tc.class {
				t.Fatalf("err = %v", err)
			}
			for _, leak := range []string{"other-secret", "work", "Reports", "folder"} {
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

func TestTaskRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	const location = "https://evil.example.invalid/redirect-canary-9f1e"
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		serve(t, func(*http.Request) (*http.Response, error) {
			response := xmlResponse(status, "")
			response.Header.Set("Location", location)
			return response, nil
		})
		for op, args := range map[string]string{
			"nextcloud.tasks.list": `{"calendar":"tasks"}`,
			"nextcloud.tasks.get":  `{"calendar":"tasks","id":"a.ics"}`,
		} {
			_, err := calInvoke(t, []string{"calendar"}, op, args)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "redirect-canary") || strings.Contains(err.Error(), "evil") {
				t.Errorf("%s %d = %v", op, status, err)
			}
		}
	}
}

func TestTaskRedirectOnTheDataRequestIsAClearFailure(t *testing.T) {
	for _, method := range []string{"REPORT", http.MethodGet} {
		serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method == method {
				response := xmlResponse(http.StatusFound, "")
				response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-9f1e")
				return response, nil
			}
			return xmlResponse(http.StatusMultiStatus, taskCalendarProbe("VTODO")), nil
		})
		op, args := "nextcloud.tasks.list", `{"calendar":"tasks"}`
		if method == http.MethodGet {
			op, args = "nextcloud.tasks.get", `{"calendar":"tasks","id":"a.ics"}`
		}
		_, err := calInvoke(t, []string{"calendar"}, op, args)
		if err == nil || !strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), "canary") {
			t.Errorf("%s = %v", op, err)
		}
	}
}

func TestTasksGetOfAnEventIsAClearRefusal(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return xmlResponse(http.StatusOK, ics("ev", "Meeting", "20260303T090000Z", "")), nil
		}
		return xmlResponse(http.StatusMultiStatus, taskCalendarProbe("VEVENT", "VTODO")), nil
	})
	_, err := calInvoke(t, []string{"calendar"}, "nextcloud.tasks.get", `{"calendar":"tasks","id":"ev.ics"}`)
	if err == nil || !strings.Contains(err.Error(), "does not name a task") || strings.Contains(err.Error(), "valid iCalendar") {
		t.Errorf("err = %v", err)
	}
}
