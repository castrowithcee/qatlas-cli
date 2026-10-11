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
	manageCalendarPath = calHome + "projects/"
	principalBase      = "/remote.php/dav/principals/users/"
)

var manageTools = []string{calendarsList.ID, calendarsCreate.ID, calendarsUpdate.ID, calendarsDelete.ID}

func manage(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, manageTools, targets, operation, args, confirm)
}

// ownerAnswer is the pre-check answer of the calendar projects, owned by owner; an empty owner reports none.
func ownerAnswer(owner string) *http.Response {
	props := `<d:resourcetype><d:collection/><c:calendar/></d:resourcetype>`
	if owner != "" {
		props += `<oc:owner-principal xmlns:oc="http://owncloud.org/ns"><d:href>` + principalBase + owner +
			`/</d:href></oc:owner-principal>`
	}
	return xmlResponse(http.StatusMultiStatus, calMultistatus(`<d:response><d:href>`+manageCalendarPath+`</d:href>`+
		`<d:propstat><d:prop>`+props+`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
}

func calendarPatchAnswer(name, color string) *http.Response {
	ok := func(element string) string {
		return `<d:propstat><d:prop>` + element + `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`
	}
	denied := func(element string) string {
		return `<d:propstat><d:prop>` + element + `</d:prop><d:status>HTTP/1.1 403 Forbidden</d:status></d:propstat>`
	}
	pick := func(state, element string) string {
		if state == "ok" {
			return ok(element)
		}
		if state == "denied" {
			return denied(element)
		}
		return ""
	}
	return xmlResponse(http.StatusMultiStatus, calMultistatus(`<d:response><d:href>`+manageCalendarPath+`</d:href>`+
		pick(name, `<d:displayname/>`)+pick(color, `<x:calendar-color xmlns:x="http://apple.com/ns/ical/"/>`)+
		`</d:response>`))
}

// manageFixture answers the pre-check with owner and the mutation with mutate.
func manageFixture(t *testing.T, owner string, mutate func() (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind {
			return ownerAnswer(owner), nil
		}
		return mutate()
	})
}

func noContent() (*http.Response, error) { return status(http.StatusNoContent), nil }

func TestCalendarsCreateSendsOneGeneratedMkcalendar(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(http.StatusCreated), nil })
	args := `{"name":"Team <A> & \"B\"","color":"#3366cc","components":["VTODO","VEVENT"]}`
	result, err := manage(t, []string{"calendar"}, "nextcloud.calendars.create", args, true)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	var got CalendarWriteResult
	if err := json.Unmarshal(result, &got); err != nil || !got.Created ||
		!regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(got.ID) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(*calls) != 1 || (*calls)[0].method != methodMkcalendar || (*calls)[0].url.Path != calHome+got.ID+"/" {
		t.Fatalf("calls = %+v", *calls)
	}
	const golden = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:mkcalendar xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:d="DAV:" xmlns:a="http://apple.com/ns/ical/">` +
		`<d:set><d:prop><d:displayname>Team &lt;A&gt; &amp; &#34;B&#34;</d:displayname>` +
		`<a:calendar-color>#3366CC</a:calendar-color><c:supported-calendar-component-set>` +
		`<c:comp name="VEVENT"/><c:comp name="VTODO"/></c:supported-calendar-component-set></d:prop></d:set></c:mkcalendar>`
	if (*calls)[0].body != golden {
		t.Errorf("body = %s", (*calls)[0].body)
	}
}

func TestCalendarsCreateDefaultsToBothComponentsWithoutColor(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(http.StatusCreated), nil })
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.create", `{"name":"Plain"}`, true); err != nil {
		t.Fatal(err)
	}
	body := (*calls)[0].body
	if strings.Contains(body, "calendar-color") || !strings.Contains(body, `<c:comp name="VEVENT"/><c:comp name="VTODO"/>`) {
		t.Errorf("body = %s", body)
	}
}

func TestCalendarsCreateNeedsTheGeneralTargetAndValidInput(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	_, err := manage(t, []string{"calendar/projects"}, "nextcloud.calendars.create", `{"name":"X"}`, true)
	if classOf(err) != provider.ClassPermission {
		t.Errorf("single binding: err = %v", err)
	}
	_, err = manage(t, []string{"folder/Reports"}, "nextcloud.calendars.create", `{"name":"X"}`, true)
	if classOf(err) != provider.ClassPermission {
		t.Errorf("no calendar target: err = %v", err)
	}
	for name, args := range map[string]string{
		"blank name":    `{"name":"   "}`,
		"control":       "{\"name\":\"a\\u0007b\"}",
		"newline":       `{"name":"a\nb"}`,
		"long name":     `{"name":"` + strings.Repeat("n", 129) + `"}`,
		"bad color":     `{"name":"X","color":"red"}`,
		"short color":   `{"name":"X","color":"#fff"}`,
		"bad component": `{"name":"X","components":["VJOURNAL"]}`,
		"no components": `{"name":"X","components":[]}`,
		"uri argument":  `{"name":"X","calendar":"mine"}`,
		"free field":    `{"name":"X","url":"https://evil.example.invalid"}`,
	} {
		if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.create", args, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.create", `{"name":"X"}`, false); err == nil {
		t.Error("unconfirmed create: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestCalendarsCreateFailuresAreClear(t *testing.T) {
	for code, want := range map[int]string{405: "already exists", 409: "cannot create", 403: "may not"} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return status(code), nil })
		_, err := manage(t, []string{"calendar"}, "nextcloud.calendars.create", `{"name":"X"}`, true)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), bodyCanary) ||
			strings.Contains(err.Error(), eventHint) || len(*calls) != 1 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func TestCalendarsUpdateSendsOneProppatchAfterTheOwnerCheck(t *testing.T) {
	calls := manageFixture(t, aliceUser, func() (*http.Response, error) { return calendarPatchAnswer("ok", "ok"), nil })
	result, err := manage(t, []string{"calendar/projects"}, "nextcloud.calendars.update",
		`{"calendar":"projects","name":"Projects <2026>","color":"#abcdef80"}`, true)
	var got CalendarWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || got != (CalendarWriteResult{ID: "projects", Updated: true}) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if eventMethods(calls) != "PROPFIND PROPPATCH" || (*calls)[0].url.Path != manageCalendarPath ||
		(*calls)[0].depth != depthSelf || (*calls)[1].url.Path != manageCalendarPath {
		t.Fatalf("calls = %+v", *calls)
	}
	const golden = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propertyupdate xmlns:d="DAV:" xmlns:a="http://apple.com/ns/ical/"><d:set><d:prop>` +
		`<d:displayname>Projects &lt;2026&gt;</d:displayname><a:calendar-color>#ABCDEF80</a:calendar-color>` +
		`</d:prop></d:set></d:propertyupdate>`
	if (*calls)[1].body != golden {
		t.Errorf("body = %s", (*calls)[1].body)
	}
}

func TestCalendarsUpdateNeedsOneOfNameAndColor(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("no request expected") })
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.update", `{"calendar":"projects"}`, true); err == nil {
		t.Error("update without a change: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d", len(*calls))
	}
	manageFixture(t, aliceUser, func() (*http.Response, error) { return calendarPatchAnswer("none", "ok"), nil })
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.update",
		`{"calendar":"projects","color":"#112233"}`, true); err != nil {
		t.Errorf("colour only: %v", err)
	}
}

func TestCalendarsUpdateReportsPartialAndRefusedProperties(t *testing.T) {
	both := `{"calendar":"projects","name":"N","color":"#112233"}`
	for name, tc := range map[string]struct{ name, color, want string }{
		"colour refused": {"ok", "denied", "changed the name but refused the change of color"},
		"name refused":   {"denied", "ok", "changed the color but refused the change of name"},
		"all refused":    {"denied", "denied", "refused the change of name and color, nothing was changed"},
		"missing":        {"ok", "none", "refused the change of color"},
	} {
		manageFixture(t, aliceUser, func() (*http.Response, error) { return calendarPatchAnswer(tc.name, tc.color), nil })
		_, err := manage(t, []string{"calendar"}, "nextcloud.calendars.update", both, true)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	manageFixture(t, aliceUser, func() (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, "<not-xml"), nil
	})
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.update", both, true); err == nil ||
		!strings.Contains(err.Error(), eventHint) {
		t.Errorf("unreadable answer: err = %v", err)
	}
}

func TestCalendarMutationsRefuseForeignAndUnconfirmedOwnerBeforeAnyChange(t *testing.T) {
	for op, args := range map[string]string{
		"nextcloud.calendars.update": `{"calendar":"projects","name":"N"}`,
		"nextcloud.calendars.delete": `{"calendar":"projects"}`,
	} {
		for _, owner := range []string{"bob", "alice2", ""} {
			calls := manageFixture(t, owner, func() (*http.Response, error) {
				t.Errorf("%s: a mutation was sent", op)
				return status(http.StatusNoContent), nil
			})
			_, err := manage(t, []string{"calendar"}, op, args, true)
			if classOf(err) != provider.ClassPermission || len(*calls) != 1 || strings.Contains(err.Error(), "bob") {
				t.Errorf("%s owner %q: err = %v, calls = %d", op, owner, err, len(*calls))
			}
		}
	}
}

func TestCalendarMutationsRefuseUnboundBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for _, op := range []string{"update", "delete"} {
		extra := ""
		if op == "update" {
			extra = `,"name":"N"`
		}
		for name, tc := range map[string]struct {
			targets  []string
			calendar string
		}{
			"other calendar": {[]string{"calendar/work"}, "other-secret"},
			"no target":      {[]string{"folder/Reports"}, "projects"},
			"reserved":       {[]string{"calendar"}, "trashbin"},
			"reserved bound": {[]string{"calendar/inbox"}, "inbox"},
			"traversal":      {[]string{"calendar"}, "../bobby"},
		} {
			_, err := manage(t, tc.targets, "nextcloud.calendars."+op, `{"calendar":"`+tc.calendar+`"`+extra+`}`, true)
			if classOf(err) != provider.ClassPermission {
				t.Errorf("%s %s: err = %v", op, name, err)
				continue
			}
			for _, leak := range []string{"other-secret", "work", "bobby", "trashbin", "inbox", "Reports"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s %s: the refusal names %q", op, name, leak)
				}
			}
		}
		if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars."+op, `{"calendar":"projects"`+extra+`}`, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
	}
	if _, err := manage(t, []string{"calendar"}, "nextcloud.calendars.delete", `{"calendar":"projects","name":"x"}`, true); err == nil {
		t.Error("delete with extra field: accepted")
	}
	// Without a tools list naming it the delete is not offered.
	if _, err := eventWrite(t, nil, []string{"calendar"}, "nextcloud.calendars.delete", `{"calendar":"projects"}`, true); err == nil {
		t.Error("delete without a tools list: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestCalendarsDeleteSendsOneDeleteAfterTheOwnerCheck(t *testing.T) {
	calls := manageFixture(t, aliceUser, noContent)
	result, err := manage(t, []string{"calendar/projects"}, "nextcloud.calendars.delete", `{"calendar":"projects"}`, true)
	var got CalendarWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || got != (CalendarWriteResult{ID: "projects", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(calls) != "PROPFIND DELETE" || (*calls)[1].url.Path != manageCalendarPath || (*calls)[1].body != "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestCalendarManagementUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted":   func() (*http.Response, error) { return status(http.StatusAccepted), nil },
	}
	for _, code := range []int{500, 502, 503, 504} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for op, args := range map[string]string{
		"nextcloud.calendars.create": `{"name":"X"}`,
		"nextcloud.calendars.update": `{"calendar":"projects","name":"N"}`,
		"nextcloud.calendars.delete": `{"calendar":"projects"}`,
	} {
		for name, answer := range answers {
			calls := manageFixture(t, aliceUser, answer)
			_, err := manage(t, []string{"calendar"}, op, args, true)
			want := 2
			if strings.HasSuffix(op, "create") {
				want = 1
			}
			if err == nil || !strings.Contains(err.Error(), eventHint) || !strings.Contains(err.Error(), "before repeating") ||
				strings.Contains(err.Error(), bodyCanary) || len(*calls) != want {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestCalendarManagementRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func(code int) *http.Response {
		response := status(code)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	for op, args := range map[string]string{
		"nextcloud.calendars.create": `{"name":"X"}`,
		"nextcloud.calendars.update": `{"calendar":"projects","name":"N"}`,
		"nextcloud.calendars.delete": `{"calendar":"projects"}`,
	} {
		for _, code := range []int{301, 302, 307, 308} {
			calls := manageFixture(t, aliceUser, func() (*http.Response, error) { return redirect(code), nil })
			_, err := manage(t, []string{"calendar"}, op, args, true)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), eventHint) {
				t.Errorf("%s %d: err = %v, calls = %d", op, code, err, len(*calls))
			}
			if strings.HasSuffix(op, "create") {
				continue
			}
			// On the pre-check, which then sends no mutation.
			calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(code), nil })
			_, err = manage(t, []string{"calendar"}, op, args, true)
			if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
				t.Errorf("%s %d pre-check: err = %v, calls = %d", op, code, err, len(*calls))
			}
		}
	}
}

func TestCalendarManagementToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id          string
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{calendarsCreate.ID, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{calendarsUpdate.ID, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{calendarsDelete.ID, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupCalendar || r.Effect != tc.effect || r.Idempotency != tc.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != calendarSensitivity ||
			d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	d, _, _ := reg.Lookup(calendarsDelete.ID)
	if !strings.Contains(d.Description, "calendar trash bin") {
		t.Errorf("delete description = %q", d.Description)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.calendars.") && id != calendarsList.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
