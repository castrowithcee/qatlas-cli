package nextcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	calTrashObjects     = calHome + "trashbin/objects/"
	calTrashRestorePath = calHome + "trashbin/restore/"
)

var trashTools = []string{calendarTrashList.ID, calendarTrashRestore.ID, calendarTrashDelete.ID}

func calendarTrash(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, trashTools, targets, operation, args, confirm)
}

func trashNode(href, uri, contentType, deletedAt string) string {
	props := `<d:resourcetype/>`
	if contentType != "" {
		props += `<d:getcontenttype>` + contentType + `</d:getcontenttype>`
	}
	if uri != "" {
		props += `<nc:calendar-uri xmlns:nc="http://nextcloud.com/ns">` + uri + `</nc:calendar-uri>`
	}
	if deletedAt != "" {
		props += `<nc:deleted-at xmlns:nc="http://nextcloud.com/ns">` + deletedAt + `</nc:deleted-at>`
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` + props +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

const (
	typeEvent = "text/calendar; charset=utf-8; component=vevent"
	typeTodo  = "text/calendar; charset=utf-8; component=vtodo"
)

func trashListAnswer(extra ...string) *http.Response {
	entries := append([]string{
		trashNode(calTrashObjects, "", "", ""),
		trashNode(calTrashObjects+"b.ics-d2", "projects", typeEvent, "2026-01-02T03:04:05+00:00"),
		trashNode(calTrashObjects+"a.ics-d1", "projects", typeTodo, "1767312000"),
		trashNode(calTrashObjects+"w.ics-d3", "work", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"n.ics-d4", "projects", "", "bad"),
	}, extra...)
	return xmlResponse(http.StatusMultiStatus, calMultistatus(entries...))
}

func retentionAnswer(value string) *http.Response {
	props := ``
	if value != "" {
		props = `<nc:trash-bin-retention-duration xmlns:nc="http://nextcloud.com/ns">` + value +
			`</nc:trash-bin-retention-duration>`
	}
	return xmlResponse(http.StatusMultiStatus, calMultistatus(`<d:response><d:href>`+calHome+`trashbin/</d:href>`+
		`<d:propstat><d:prop>`+props+`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
}

func trashListFixture(t *testing.T, extra ...string) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == calHome+"trashbin/" {
			return retentionAnswer("2592000"), nil
		}
		return trashListAnswer(extra...), nil
	})
}

func decodeTrashList(t *testing.T, raw json.RawMessage) CalendarTrashResult {
	t.Helper()
	var got CalendarTrashResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result = %s, %v", raw, err)
	}
	return got
}

func TestCalendarTrashListFiltersToBoundCalendars(t *testing.T) {
	foreign := []string{
		trashNode("/remote.php/dav/calendars/bob/trashbin/objects/x.ics", "projects", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"deep/x.ics", "projects", typeEvent, "1767312000"),
		trashNode("https://evil.example.invalid"+calTrashObjects+"h.ics", "projects", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"i.ics-d5", "inbox", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"t.ics-d6", "trashbin", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"m.ics-d7", "", typeEvent, "1767312000"),
		trashNode(calTrashObjects+"p.ics-d8", "../bob", typeEvent, "1767312000"),
	}
	for name, tc := range map[string]struct {
		targets  []string
		wantIDs  string
		wantCals string
	}{
		"single": {[]string{"calendar/projects"}, "a.ics-d1 b.ics-d2 n.ics-d4", "projects projects projects"},
		"all":    {[]string{"calendar"}, "a.ics-d1 b.ics-d2 n.ics-d4 w.ics-d3", "projects projects projects work"},
	} {
		calls := trashListFixture(t, foreign...)
		raw, err := calendarTrash(t, tc.targets, "nextcloud.calendartrash.list", `{}`, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := decodeTrashList(t, raw)
		ids, cals := []string{}, []string{}
		for _, e := range got.Entries {
			ids, cals = append(ids, e.ID), append(cals, e.Calendar)
		}
		if strings.Join(ids, " ") != tc.wantIDs || strings.Join(cals, " ") != tc.wantCals || got.Count != len(ids) ||
			got.Truncated || got.RetentionSeconds != 2592000 {
			t.Errorf("%s: result = %s", name, raw)
		}
		if got.Entries[0].Kind != "VTODO" || got.Entries[0].DeletedAt != "2026-01-02T00:00:00Z" ||
			got.Entries[1].Kind != "VEVENT" || got.Entries[1].DeletedAt != "2026-01-02T03:04:05Z" ||
			got.Entries[2].Kind != "" || got.Entries[2].DeletedAt != "" {
			t.Errorf("%s: entries = %+v", name, got.Entries)
		}
		for _, leak := range []string{"bob", "evil", "inbox", "x.ics", "h.ics", "i.ics", "m.ics", "p.ics", "t.ics"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%s: the result names %q", name, leak)
			}
		}
		if len(*calls) != 2 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != "1" ||
			(*calls)[0].url.Path != calTrashObjects || (*calls)[1].depth != "0" || (*calls)[1].url.Path != calHome+"trashbin/" {
			t.Errorf("%s: calls = %+v", name, *calls)
		}
	}
}

func TestCalendarTrashListCapsAndReportsTruncation(t *testing.T) {
	nodes := []string{}
	for i := 0; i < maxCalendarTrash+5; i++ {
		nodes = append(nodes, trashNode(calTrashObjects+"e"+strconv.Itoa(1000+i)+".ics", "projects", typeEvent, ""))
	}
	trashListFixture(t, nodes...)
	raw, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.list", `{}`, false)
	if got := decodeTrashList(t, raw); err != nil || got.Count != maxCalendarTrash || !got.Truncated ||
		len(got.Entries) != maxCalendarTrash {
		t.Errorf("count = %d, truncated = %v, err = %v", got.Count, got.Truncated, err)
	}
}

func TestCalendarTrashListWithoutRetentionOmitsIt(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == calHome+"trashbin/" {
			return retentionAnswer(""), nil
		}
		return trashListAnswer(), nil
	})
	raw, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.list", `{}`, false)
	if err != nil || strings.Contains(string(raw), "retention") {
		t.Errorf("result = %s, %v", raw, err)
	}
}

func TestCalendarTrashListRefusesWithoutCalendarTargetAndReportsMissingTrash(t *testing.T) {
	refuse(t)
	if _, err := calendarTrash(t, []string{"folder/Reports"}, "nextcloud.calendartrash.list", `{}`, false); err == nil {
		t.Error("no calendar target: accepted")
	}
	serve(t, func(*http.Request) (*http.Response, error) { return status(http.StatusNotFound), nil })
	_, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.list", `{}`, false)
	if err == nil || !strings.Contains(err.Error(), "not available") || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("missing trash: err = %v", err)
	}
}

// entryFixture answers the pre-check for the entry with calendar uri and records the headers of the
// mutation.
func entryFixture(t *testing.T, uri string, mutate func() (*http.Response, error)) (*[]call, *http.Header) {
	t.Helper()
	headers := &http.Header{}
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind {
			if uri == "" {
				return status(http.StatusNotFound), nil
			}
			return xmlResponse(http.StatusMultiStatus, calMultistatus(
				trashNode(calTrashObjects+"a.ics-d1", uri, typeEvent, "1767312000"))), nil
		}
		*headers = r.Header.Clone()
		return mutate()
	})
	return calls, headers
}

func TestCalendarTrashRestoreSendsOneMoveToAFixedDestination(t *testing.T) {
	calls, headers := entryFixture(t, "projects", noContent)
	raw, err := calendarTrash(t, []string{"calendar/projects"}, "nextcloud.calendartrash.restore", `{"id":"a.ics-d1"}`, true)
	var got map[string]any
	if err != nil || json.Unmarshal(raw, &got) != nil || got["restored"] != true || got["calendar"] != "projects" ||
		got["id"] != "a.ics-d1" {
		t.Fatalf("restore = %s, %v", raw, err)
	}
	if eventMethods(calls) != "PROPFIND MOVE" || (*calls)[0].depth != "0" || (*calls)[0].url.Path != calTrashObjects+"a.ics-d1" ||
		(*calls)[1].url.Path != calTrashObjects+"a.ics-d1" ||
		headers.Get("Destination") != (*calls)[1].url.Scheme+"://"+(*calls)[1].url.Host+calTrashRestorePath+"a.ics-d1" {
		t.Errorf("calls = %+v, destination = %q", *calls, headers.Get("Destination"))
	}
}

func TestCalendarTrashDeleteSendsOneDeleteAfterThePreCheck(t *testing.T) {
	calls, _ := entryFixture(t, "projects", noContent)
	raw, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.delete", `{"id":"a.ics-d1"}`, true)
	var got map[string]any
	if err != nil || json.Unmarshal(raw, &got) != nil || got["deleted"] != true || got["calendar"] != "projects" {
		t.Fatalf("delete = %s, %v", raw, err)
	}
	if eventMethods(calls) != "PROPFIND DELETE" || (*calls)[1].url.Path != calTrashObjects+"a.ics-d1" || (*calls)[1].body != "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestCalendarTrashChangeTreatsForeignAndMissingEntriesAlike(t *testing.T) {
	for _, op := range []string{"restore", "delete"} {
		for name, tc := range map[string]struct {
			targets []string
			uri     string
		}{
			"other calendar": {[]string{"calendar/projects"}, "work-secret"},
			"reserved":       {[]string{"calendar"}, "inbox"},
			"no uri":         {[]string{"calendar"}, "<x/>"},
			"missing":        {[]string{"calendar"}, ""},
		} {
			uri := tc.uri
			if uri == "<x/>" {
				uri = ""
			}
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != methodPropfind {
					t.Errorf("%s %s: a mutation was sent", op, name)
				}
				if tc.uri == "" {
					return status(http.StatusNotFound), nil
				}
				return xmlResponse(http.StatusMultiStatus, calMultistatus(
					trashNode(calTrashObjects+"a.ics-d1", uri, typeEvent, ""))), nil
			})
			_, err := calendarTrash(t, tc.targets, "nextcloud.calendartrash."+op, `{"id":"a.ics-d1"}`, true)
			if err == nil || !strings.Contains(err.Error(), messageTrashObjectNone) || len(*calls) != 1 ||
				strings.Contains(err.Error(), "work-secret") || strings.Contains(err.Error(), "inbox") {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestCalendarTrashChangeRefusesAnUnusableAnswerBeforeAnyMutation(t *testing.T) {
	for name, body := range map[string]string{
		"foreign href": calMultistatus(trashNode(calTrashObjects+"other.ics", "projects", typeEvent, "")),
		"two nodes": calMultistatus(trashNode(calTrashObjects+"a.ics-d1", "projects", typeEvent, ""),
			trashNode(calTrashObjects+"b.ics", "projects", typeEvent, "")),
		"not xml": "<not-xml",
	} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != methodPropfind {
				t.Errorf("%s: a mutation was sent", name)
			}
			return xmlResponse(http.StatusMultiStatus, body), nil
		})
		if _, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.delete", `{"id":"a.ics-d1"}`, true); err == nil ||
			len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
}

func TestCalendarTrashChangeRefusesInvalidIDsUnconfirmedAndUnlistedBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for _, op := range []string{"restore", "delete"} {
		for _, id := range []string{"", ".", "..", "a/b", "../x", `a\b`, "a%2fb", "a\nb", strings.Repeat("x", 256)} {
			args, _ := json.Marshal(map[string]string{"id": id})
			if _, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, string(args), true); err == nil {
				t.Errorf("%s id %q: accepted", op, id)
			}
		}
		if _, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, `{"id":"a.ics"}`, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
		if _, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, `{"id":"a.ics","x":1}`, true); err == nil {
			t.Errorf("%s with extra field: accepted", op)
		}
		if _, err := calendarTrash(t, []string{"folder/Reports"}, "nextcloud.calendartrash."+op, `{"id":"a.ics"}`, true); err == nil {
			t.Errorf("%s without calendar target: accepted", op)
		}
	}
	// Without a tools list naming it the delete is not offered.
	if _, err := eventWrite(t, nil, []string{"calendar"}, "nextcloud.calendartrash.delete", `{"id":"a.ics"}`, true); err == nil {
		t.Error("delete without a tools list: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestCalendarTrashUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
	}
	for _, code := range []int{500, 502, 503, 504} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for _, op := range []string{"restore", "delete"} {
		for name, answer := range answers {
			calls, _ := entryFixture(t, "projects", answer)
			_, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, `{"id":"a.ics-d1"}`, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				!strings.Contains(err.Error(), "calendartrash.list") || strings.Contains(err.Error(), bodyCanary) ||
				len(*calls) != 2 {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestCalendarTrashRestoreConflictIsClearAndPlain(t *testing.T) {
	for _, code := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		calls, _ := entryFixture(t, "projects", func() (*http.Response, error) { return status(code), nil })
		_, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash.restore", `{"id":"a.ics-d1"}`, true)
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || strings.Contains(err.Error(), bodyCanary) ||
			strings.Contains(err.Error(), "may have been applied") || len(*calls) != 2 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func TestCalendarTrashRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func(code int) *http.Response {
		response := status(code)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	for _, op := range []string{"list", "restore", "delete"} {
		for _, code := range []int{301, 302, 307, 308} {
			// On the read, which then sends no mutation.
			calls := serve(t, func(*http.Request) (*http.Response, error) { return redirect(code), nil })
			args := `{"id":"a.ics-d1"}`
			if op == "list" {
				args = `{}`
			}
			_, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, args, op != "list")
			if err == nil || !strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), "canary") || len(*calls) != 1 {
				t.Errorf("%s %d read: err = %v, calls = %d", op, code, err, len(*calls))
			}
			if op == "list" {
				continue
			}
			calls, _ = entryFixture(t, "projects", func() (*http.Response, error) { return redirect(code), nil })
			_, err = calendarTrash(t, []string{"calendar"}, "nextcloud.calendartrash."+op, `{"id":"a.ics-d1"}`, true)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "may have been applied") ||
				len(*calls) != 2 {
				t.Errorf("%s %d mutation: err = %v, calls = %d", op, code, err, len(*calls))
			}
		}
	}
}

func TestCalendarTrashToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id        string
		effect    capability.Effect
		confirm   capability.Confirmation
		allowList bool
	}{
		{calendarTrashList.ID, capability.EffectRead, capability.ConfirmationNone, false},
		{calendarTrashRestore.ID, capability.EffectUpdate, capability.ConfirmationRequired, false},
		{calendarTrashDelete.ID, capability.EffectDelete, capability.ConfirmationRequired, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupCalendar || r.Effect != tc.effect || r.Confirmation != tc.confirm || !r.OpenWorld ||
			r.DataSensitivity != calendarSensitivity || r.Idempotency == "" || d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.calendartrash.") {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
