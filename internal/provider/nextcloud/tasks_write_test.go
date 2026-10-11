package nextcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	taskCalendarPath = calHome + "tasks/"
	taskPath         = taskCalendarPath + "errand.ics"
)

var taskTools = []string{calendarsList.ID, tasksList.ID, tasksGet.ID, tasksCreate.ID, tasksUpdate.ID, tasksDelete.ID}

const (
	taskCreateArgs = `{"calendar":"tasks","title":"Send invoice","due":"2026-03-12","priority":3,"categories":["work"]}`
	taskUpdateArgs = `{"calendar":"tasks","id":"errand.ics","etag":"e1","title":"Send invoice","status":"COMPLETED"}`
	taskDeleteArgs = `{"calendar":"tasks","id":"errand.ics","etag":"e1"}`
)

// taskStore is what the fake server holds: the stored object behind the task path and the REPORT answer.
type taskStore struct {
	data, etag string
	report     string
	rights     []string
}

func taskReportAnswer(entries ...string) string {
	var body strings.Builder
	for i, data := range entries {
		href := taskCalendarPath + "t" + strconv.Itoa(i) + ".ics"
		body.WriteString(`<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:getetag>&quot;x&quot;</d:getetag>` +
			`<c:calendar-data>` + data + `</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
	}
	return calMultistatus(body.String())
}

// taskFixture answers the pre-checks and records the headers of the mutation, which mutate answers.
func taskFixture(t *testing.T, store taskStore, mutate func() (*http.Response, error)) (*[]call, *http.Header) {
	t.Helper()
	if store.data == "" {
		store.data = todo("u-keep", "Old", "SEQUENCE:2\r\nRELATED-TO:mom\r\n")
	}
	if store.etag == "" {
		store.etag = "e1"
	}
	if store.rights == nil {
		store.rights = fullRights
	}
	headers := &http.Header{}
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == methodPropfind:
			return xmlResponse(http.StatusMultiStatus, calMultistatus(
				calendarNode(taskCalendarPath, "Tasks", isCalendar, store.rights, "VEVENT", "VTODO"))), nil
		case r.Method == methodReport:
			return xmlResponse(http.StatusMultiStatus, store.report), nil
		case r.Method == http.MethodGet:
			response := xmlResponse(http.StatusOK, store.data)
			response.Header = http.Header{"Etag": []string{`"` + store.etag + `"`}}
			return response, nil
		}
		*headers = r.Header.Clone()
		return mutate()
	})
	return calls, headers
}

func taskDone(code int) func() (*http.Response, error) {
	return func() (*http.Response, error) { return eventChanged(code, "n1"), nil }
}

func TestTasksCreatePutsOneNewTaskWithIfNoneMatch(t *testing.T) {
	calls, headers := taskFixture(t, taskStore{}, taskDone(http.StatusCreated))
	result, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.create", taskCreateArgs, true)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	var got TaskWriteResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.ID != got.UID+".ics" || len(got.UID) != 36 || got.ETag != "n1" {
		t.Errorf("result = %s", result)
	}
	if eventMethods(calls) != "PROPFIND PUT" || (*calls)[0].url.Path != taskCalendarPath || (*calls)[0].depth != depthSelf {
		t.Fatalf("calls = %+v", *calls)
	}
	put := (*calls)[1]
	if put.url.Path != taskCalendarPath+got.ID || headers.Get("If-None-Match") != "*" || headers.Get("If-Match") != "" {
		t.Errorf("put = %+v, headers = %v", put, *headers)
	}
	for _, want := range []string{"BEGIN:VTODO", "UID:" + got.UID, "SUMMARY:Send invoice", "STATUS:NEEDS-ACTION",
		"DUE;VALUE=DATE:20260312", "PRIORITY:3", "CATEGORIES:work"} {
		if !strings.Contains(put.body, want) {
			t.Errorf("body lacks %q:\n%s", want, put.body)
		}
	}
}

func TestTasksCreateWithParentQueriesTheUIDOnceAndNeedsExactlyOneMatch(t *testing.T) {
	parent := todo("mom", "Parent", "")
	for name, tc := range map[string]struct {
		report string
		ok     bool
	}{
		"found":          {taskReportAnswer(parent), true},
		"none":           {taskReportAnswer(), false},
		"two":            {taskReportAnswer(parent, parent), false},
		"foreign answer": {taskReportAnswer(todo("other", "Other", "")), false},
	} {
		calls, headers := taskFixture(t, taskStore{report: tc.report}, taskDone(http.StatusCreated))
		args := strings.TrimSuffix(taskCreateArgs, "}") + `,"parent_uid":"mom"}`
		_, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.create", args, true)
		if tc.ok {
			report := (*calls)[0]
			if err != nil || eventMethods(calls) != "REPORT PUT" || report.url.Path != taskCalendarPath ||
				!strings.Contains(report.body, `<c:prop-filter name="UID"><c:text-match collation="i;octet">mom</c:text-match>`) ||
				!strings.Contains((*calls)[1].body, "RELATED-TO:mom") || headers.Get("If-None-Match") != "*" {
				t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "parent_uid") || eventMethods(calls) != "REPORT" {
			t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
		}
	}
}

func TestTasksUpdateCompletesBoundToTheETagAndKeepsUIDAndParent(t *testing.T) {
	calls, headers := taskFixture(t, taskStore{}, taskDone(http.StatusNoContent))
	result, err := eventWrite(t, taskTools, []string{"calendar/tasks"}, "nextcloud.tasks.update", taskUpdateArgs, true)
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	var got TaskWriteResult
	if json.Unmarshal(result, &got) != nil ||
		got != (TaskWriteResult{Calendar: "tasks", ID: "errand.ics", UID: "u-keep", ETag: "n1", Updated: true}) {
		t.Errorf("result = %s", result)
	}
	if eventMethods(calls) != "GET PUT" || (*calls)[0].url.Path != taskPath {
		t.Fatalf("calls = %+v", *calls)
	}
	put := (*calls)[1]
	if put.url.Path != taskPath || headers.Get("If-Match") != `"e1"` || headers.Get("If-None-Match") != "" {
		t.Errorf("put = %+v, headers = %v", put, *headers)
	}
	for _, want := range []string{"UID:u-keep", "STATUS:COMPLETED", "PERCENT-COMPLETE:100", "RELATED-TO:mom"} {
		if !strings.Contains(put.body, want) {
			t.Errorf("body lacks %q:\n%s", want, put.body)
		}
	}
	if !regexp.MustCompile(`(?m)^COMPLETED:\d{8}T\d{6}Z\r$`).MatchString(put.body) ||
		!regexp.MustCompile(`(?m)^SEQUENCE:3\r$`).MatchString(put.body) {
		t.Errorf("body:\n%s", put.body)
	}
}

func TestTasksDeleteSendsOneETagBoundDelete(t *testing.T) {
	calls, headers := taskFixture(t, taskStore{}, taskDone(http.StatusNoContent))
	result, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.delete", taskDeleteArgs, true)
	var got TaskWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil ||
		got != (TaskWriteResult{Calendar: "tasks", ID: "errand.ics", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(calls) != "GET DELETE" || (*calls)[1].url.Path != taskPath || headers.Get("If-Match") != `"e1"` ||
		headers.Get("If-None-Match") != "" {
		t.Errorf("calls = %+v, headers = %v", *calls, *headers)
	}
}

func TestTaskUpdateRefusesUnmodeledContentWithoutAMutation(t *testing.T) {
	for name, data := range map[string]string{
		"rrule":  todo("u1", "S", "RRULE:FREQ=DAILY\r\n"),
		"alarm":  todo("u1", "S", "BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT5M\r\nDESCRIPTION:x\r\nEND:VALARM\r\n"),
		"x prop": todo("u1", "S", "X-VENDOR:1\r\n"),
		"attach": todo("u1", "S", "ATTACH:https://example.invalid/a\r\n"),
	} {
		calls, _ := taskFixture(t, taskStore{data: data}, func() (*http.Response, error) {
			t.Errorf("%s: a mutation was sent", name)
			return status(http.StatusNoContent), nil
		})
		_, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.update", taskUpdateArgs, true)
		if err == nil || !strings.Contains(err.Error(), "does not write") || eventMethods(calls) != "GET" {
			t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
		}
	}
}

func TestTaskWritesRefuseAnEventAndAChangedETagWithoutAMutation(t *testing.T) {
	event := ics("u1", "S", "20260310T090000Z", "")
	for _, tc := range []struct {
		name, op, args, want string
		store                taskStore
	}{
		{"event update", "update", taskUpdateArgs, "does not name a task", taskStore{data: event}},
		{"event delete", "delete", taskDeleteArgs, "does not name a task", taskStore{data: event}},
		{"etag update", "update", taskUpdateArgs, "precondition failed", taskStore{etag: "e9"}},
		{"etag delete", "delete", taskDeleteArgs, "precondition failed", taskStore{etag: "e9"}},
	} {
		calls, _ := taskFixture(t, tc.store, func() (*http.Response, error) {
			t.Errorf("%s: a mutation was sent", tc.name)
			return status(http.StatusNoContent), nil
		})
		_, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks."+tc.op, tc.args, true)
		if err == nil || !strings.Contains(err.Error(), tc.want) || eventMethods(calls) != "GET" {
			t.Errorf("%s: err = %v, calls = %+v", tc.name, err, *calls)
		}
	}
}

func TestTaskCreateRefusesAReadOnlyOrTasklessCalendar(t *testing.T) {
	calls, _ := taskFixture(t, taskStore{rights: []string{"read"}}, func() (*http.Response, error) {
		t.Error("a mutation was sent")
		return status(http.StatusNoContent), nil
	})
	_, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.create", taskCreateArgs, true)
	if classOf(err) != provider.ClassPermission || eventMethods(calls) != "PROPFIND" {
		t.Errorf("read-only: err = %v, calls = %+v", err, *calls)
	}
	calls = serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			calendarNode(taskCalendarPath, "Tasks", isCalendar, fullRights, "VEVENT"))), nil
	})
	if _, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks.create", taskCreateArgs, true); err == nil ||
		eventMethods(calls) != "PROPFIND" {
		t.Errorf("no tasks: err = %v, calls = %+v", err, *calls)
	}
}

func TestTaskWriteForbiddenIsReportedClearly(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		args := map[string]string{"update": taskUpdateArgs, "delete": taskDeleteArgs}[op]
		calls, _ := taskFixture(t, taskStore{rights: []string{"read"}}, func() (*http.Response, error) {
			return status(http.StatusForbidden), nil
		})
		_, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks."+op, args, true)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), eventHint) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

var taskWrites = map[string]string{
	"nextcloud.tasks.create": taskCreateArgs, "nextcloud.tasks.update": taskUpdateArgs, "nextcloud.tasks.delete": taskDeleteArgs,
}

func TestTaskWritePreconditionFailureIsClear(t *testing.T) {
	for op, want := range map[string]string{
		"nextcloud.tasks.create": "already exists", "nextcloud.tasks.update": "precondition failed",
		"nextcloud.tasks.delete": "precondition failed",
	} {
		calls, _ := taskFixture(t, taskStore{}, func() (*http.Response, error) { return status(http.StatusPreconditionFailed), nil })
		_, err := eventWrite(t, taskTools, []string{"calendar"}, op, taskWrites[op], true)
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), want) ||
			strings.Contains(err.Error(), eventHint) || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestTaskWriteUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":  func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":    func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted": func() (*http.Response, error) { return status(http.StatusAccepted), nil },
	}
	for _, code := range []int{500, 503} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for op, args := range taskWrites {
		for name, answer := range answers {
			calls, _ := taskFixture(t, taskStore{}, answer)
			_, err := eventWrite(t, taskTools, []string{"calendar"}, op, args, true)
			if err == nil || !strings.Contains(err.Error(), eventHint) || !strings.Contains(err.Error(), "tasks.list") ||
				strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestTaskWriteRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func() *http.Response {
		response := status(http.StatusTemporaryRedirect)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	for op, args := range taskWrites {
		calls, _ := taskFixture(t, taskStore{}, func() (*http.Response, error) { return redirect(), nil })
		_, err := eventWrite(t, taskTools, []string{"calendar"}, op, args, true)
		if err == nil || !strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), "canary") ||
			strings.Contains(err.Error(), eventHint) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
		calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(), nil })
		_, err = eventWrite(t, taskTools, []string{"calendar"}, op, args, true)
		if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
			t.Errorf("%s pre-check: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestTaskWritesRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct {
		targets []string
		op, arg string
	}{
		"no calendar target": {[]string{"folder/Reports"}, "create", taskCreateArgs},
		"other calendar":     {[]string{"calendar/work"}, "create", strings.Replace(taskCreateArgs, "tasks", "other-secret", 1)},
		"other on update":    {[]string{"calendar/work"}, "update", strings.Replace(taskUpdateArgs, "tasks", "other-secret", 1)},
		"other on delete":    {[]string{"calendar/work"}, "delete", strings.Replace(taskDeleteArgs, "tasks", "other-secret", 1)},
		"reserved":           {[]string{"calendar"}, "create", strings.Replace(taskCreateArgs, "tasks", "inbox", 1)},
		"traversal":          {[]string{"calendar"}, "delete", strings.Replace(taskDeleteArgs, "tasks", "../bobby", 1)},
	} {
		_, err := eventWrite(t, taskTools, tc.targets, "nextcloud.tasks."+tc.op, tc.arg, true)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "other-secret") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	with := func(base, extra string) string { return strings.TrimSuffix(base, "}") + "," + extra + "}" }
	for name, tc := range map[string]struct{ op, args string }{
		"bad id":          {"update", strings.Replace(taskUpdateArgs, "errand.ics", "a/b.ics", 1)},
		"wildcard etag":   {"delete", strings.Replace(taskDeleteArgs, `"e1"`, `"*"`, 1)},
		"create with id":  {"create", with(taskCreateArgs, `"id":"x.ics"`)},
		"create with tag": {"create", with(taskCreateArgs, `"etag":"e1"`)},
		"update parent":   {"update", with(taskUpdateArgs, `"parent_uid":"mom"`)},
		"free uid":        {"create", with(taskCreateArgs, `"uid":"x"`)},
		"free path":       {"create", with(taskCreateArgs, `"path":"x.ics"`)},
		"rrule":           {"create", with(taskCreateArgs, `"rrule":"FREQ=DAILY"`)},
		"bad status":      {"create", with(taskCreateArgs, `"status":"DONE"`)},
		"bad due":         {"update", with(taskUpdateArgs, `"due":"tomorrow"`)},
		"bad percent":     {"update", with(taskUpdateArgs, `"percent":101`)},
		"bad priority":    {"create", with(taskCreateArgs, `"priority":10`)},
		"fields on del":   {"delete", with(taskDeleteArgs, `"title":"x"`)},
	} {
		if _, err := eventWrite(t, taskTools, []string{"calendar"}, "nextcloud.tasks."+tc.op, tc.args, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for op, args := range taskWrites {
		if _, err := eventWrite(t, taskTools, []string{"calendar"}, op, args, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
		if _, err := eventWrite(t, []string{calendarsList.ID}, []string{"calendar"}, op, args, true); err == nil {
			t.Errorf("%s without a tools list entry: accepted", op)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestTaskWriteToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id          string
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{tasksCreate.ID, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{tasksUpdate.ID, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{tasksDelete.ID, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupCalendar || r.Effect != tc.effect || r.Idempotency != tc.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != calendarSensitivity ||
			d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.tasks.") && id != tasksList.ID && id != tasksGet.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
