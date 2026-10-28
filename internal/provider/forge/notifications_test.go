package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const (
	threadInside = `{"id":11,"unread":true,"pinned":false,"updated_at":"2026-01-01T00:00:00Z","url":"https://forge.test/api/v1/notifications/threads/11",` +
		`"subject":{"title":"Fix it","type":"Issue","state":"open","url":"u","html_url":"h","latest_comment_url":"lu","latest_comment_html_url":"lh"},` +
		`"repository":{"id":1,"name":"widget","full_name":"acme/widget"}}`
	threadOutside = `{"id":12,"unread":true,"pinned":true,"updated_at":"2026-01-02T00:00:00Z",` +
		`"subject":{"title":"Secret","type":"Pull"},"repository":{"id":2,"name":"hidden","full_name":"other/hidden"}}`
)

func notificationServer(status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method == http.MethodPut || c.method == http.MethodPatch:
			return jsonResponse(*status, `[`+threadInside+`]`)
		case c.path == "/api/v1/notifications/new":
			return jsonResponse(200, `{"new":3}`)
		case c.path == "/api/v1/notifications/threads/11":
			return jsonResponse(200, threadInside)
		case c.path == "/api/v1/notifications/threads/12":
			return jsonResponse(200, threadOutside)
		case c.path == "/api/v1/notifications":
			return jsonResponse(200, `[`+threadInside+`,null,`+threadOutside+`]`, "X-Total-Count", "9")
		case c.path == "/api/v1/repos/acme/widget/notifications":
			return jsonResponse(200, `[`+threadInside+`]`)
		case c.path == "/api/v1/repos/acme/widget":
			return jsonResponse(200, `{"id":1,"name":"widget","full_name":"acme/widget"}`)
		case c.path == "/api/v1/repos/old/name":
			return jsonResponse(200, `{"id":2,"name":"hidden","full_name":"other/hidden"}`)
		}
		return notFoundResponse()
	}
}

func TestNotificationListDropsRowsOutsideTheTargets(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, notificationsList, "open", `{"limit":3}`)
		rows, _ := result["notifications"].([]any)
		if err != nil || len(rows) != 2 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s open: %v, %v", p.id, result, err)
		}
		first, _ := rows[0].(map[string]any)
		subject, _ := first["subject"].(map[string]any)
		if first["pinned"] != false || first["unread"] != true || subject["title"] != "Fix it" ||
			subject["latest_comment_html_url"] != "lh" {
			t.Errorf("%s row = %v", p.id, first)
		}
		result, err = e.invoke(t, notificationsList, "repo", `{"all":true,"status-types":["read","unread"],"subject-type":["issue","pull"],`+
			`"since":"2026-01-01T00:00:00Z","before":"2026-02-01T00:00:00Z"}`)
		rows, _ = result["notifications"].([]any)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s repo: %v, %v", p.id, result, err)
		}
		c := (*e.calls)[len(*e.calls)-1]
		want := url.Values{"page": {"1"}, "limit": {"30"}, "all": {"true"}, "status-types": {"read", "unread"},
			"subject-type": {"issue", "pull"}, "since": {"2026-01-01T00:00:00Z"}, "before": {"2026-02-01T00:00:00Z"}}
		if c.method != "GET" || c.path != "/api/v1/notifications" || !reflect.DeepEqual(queryOf(t, c), want) {
			t.Errorf("%s call = %+v", p.id, c)
		}
		if _, err := e.invoke(t, notificationsList, "open", `{"since":"yesterday"}`); !isInvalidRequest(err) {
			t.Errorf("%s: bad time: %v", p.id, err)
		}
	}
}

func TestNotificationSubjectTypeCommitIsGiteaOnly(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		_, err := e.invoke(t, notificationsList, "open", `{"subject-type":["commit"]}`)
		if p == forgejo && (!isInvalidRequest(err) || *e.reads != 0 || len(*e.calls) != 0) {
			t.Errorf("forgejo: %v, reads %d, calls %d", err, *e.reads, len(*e.calls))
		}
		if p == gitea && err != nil {
			t.Errorf("gitea: %v", err)
		}
	}
}

func TestRepositoryNotificationsListChecksTheRepository(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, repositoryNotificationsList, "repo", `{}`)
		rows, _ := result["notifications"].([]any)
		if err != nil || len(rows) != 1 || !reflect.DeepEqual(methods(e), []string{"GET /api/v1/repos/acme/widget/notifications"}) {
			t.Fatalf("%s: %v, %v, %v", p.id, result, err, methods(e))
		}
		before := len(*e.calls)
		_, err = e.invoke(t, repositoryNotificationsList, "repo", `{"repository":"secret-owner/hidden"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *e.reads != 1 || len(*e.calls) != before {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestNotificationGetProvesTheThreadAgainstTheTargets(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, notificationsGet, "open", `{"id":12}`)
		if err != nil || result["id"] != float64(12) {
			t.Fatalf("%s open: %v, %v", p.id, result, err)
		}
		if result, err = e.invoke(t, notificationsGet, "repo", `{"id":11}`); err != nil || result["unread"] != true {
			t.Fatalf("%s inside: %v, %v", p.id, result, err)
		}
		_, err = e.invoke(t, notificationsGet, "repo", `{"id":12}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "hidden") {
			t.Errorf("%s outside: %v", p.id, err)
		}
	}
}

func TestNotificationCheckNewRunsOnlyWithoutTargets(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, notificationsCheckNew, "open", `{}`)
		if err != nil || result["new"] != float64(3) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		before, reads := len(*e.calls), *e.reads
		if _, err := e.invoke(t, notificationsCheckNew, "repo", `{}`); !isInvalidRequest(err) ||
			len(*e.calls) != before || *e.reads != reads {
			t.Errorf("%s with targets: %v", p.id, err)
		}
	}
}

func TestNotificationMarkThreadChecksTheThreadBeforeTheChange(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		if _, err := e.invokeAs(t, notificationsMarkThread, "notify-open", `{"id":11}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed: %v", p.id, err)
		}
		result, err := e.invoke(t, notificationsMarkThread, "notify-open", `{"id":12,"to-status":"unread"}`)
		if err != nil || result["status"] != "unread" || result["id"] != float64(12) ||
			!reflect.DeepEqual(methods(e), []string{"PATCH /api/v1/notifications/threads/12"}) ||
			(*e.calls)[0].query != "to-status=unread" {
			t.Fatalf("%s open: %v, %v, %v", p.id, result, err, methods(e))
		}
		*e.calls = (*e.calls)[:0]
		if result, err = e.invoke(t, notificationsMarkThread, "notify-repo", `{"id":11}`); err != nil || result["status"] != "read" ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/notifications/threads/11", "PATCH /api/v1/notifications/threads/11"}) {
			t.Fatalf("%s inside: %v, %v, %v", p.id, result, err, methods(e))
		}
		*e.calls = (*e.calls)[:0]
		_, err = e.invoke(t, notificationsMarkThread, "notify-repo", `{"id":12}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "hidden") ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/notifications/threads/12"}) {
			t.Errorf("%s outside: %v, %v", p.id, err, methods(e))
		}
		if _, err := e.invoke(t, notificationsMarkThread, "notify-open", `{"id":11,"to-status":"done"}`); err == nil {
			t.Errorf("%s: bad status accepted", p.id)
		}
	}
}

func TestNotificationMarkAllIsListedOnlyAndRunsOnlyWithoutTargets(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{notificationsMarkAllRead, repositoryNotificationsMarkRead} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "update" {
				t.Fatalf("%s descriptor = %+v", d.ID, d)
			}
			metadata, _ := reg.ProviderMetadata(p.id)
			for _, profile := range metadata.Profiles {
				for _, id := range profile.Tools {
					if id == d.ID {
						t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
					}
				}
			}
		}
		for _, tl := range []*tool{notificationsList, notificationsCheckNew, notificationsGet, notificationsMarkThread,
			repositoryNotificationsList} {
			if d, _, ok := reg.Lookup(p.toolID(tl)); !ok || d.RequiresToolAllowList {
				t.Errorf("%s listed-only = %+v", p.toolID(tl), d)
			}
		}
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		if _, err := e.invoke(t, notificationsMarkAllRead, "open", `{}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, notificationsMarkAllRead, "notify-open", `{}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed: %v", p.id, err)
		}
		result, err := e.invoke(t, notificationsMarkAllRead, "notify-open",
			`{"status-types":["unread","pinned"],"to-status":"read","last_read_at":"2026-01-01T00:00:00Z","all":true}`)
		c := (*e.calls)[len(*e.calls)-1]
		if err != nil || result["status"] != "read" || c.method != "PUT" || c.path != "/api/v1/notifications" ||
			!reflect.DeepEqual(queryOf(t, c), url.Values{"status-types": {"unread", "pinned"}, "to-status": {"read"},
				"last_read_at": {"2026-01-01T00:00:00Z"}, "all": {"true"}}) {
			t.Fatalf("%s: %v, %v, %+v", p.id, result, err, c)
		}
		before, reads := len(*e.calls), *e.reads
		if _, err := e.invoke(t, notificationsMarkAllRead, "notify-repo", `{}`); !isInvalidRequest(err) ||
			len(*e.calls) != before || *e.reads != reads {
			t.Errorf("%s with targets: %v, calls %d, reads %d", p.id, err, len(*e.calls), *e.reads)
		}
	}
}

func TestRepositoryNotificationsMarkReadProvesTheRepository(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, repositoryNotificationsMarkRead, "notify-repo", `{"to-status":"read"}`)
		if err != nil || result["repository"] != "acme/widget" || !reflect.DeepEqual(methods(e),
			[]string{"GET /api/v1/repos/acme/widget", "PUT /api/v1/repos/acme/widget/notifications"}) {
			t.Fatalf("%s: %v, %v, %v", p.id, result, err, methods(e))
		}
		*e.calls = (*e.calls)[:0]
		reads := *e.reads
		if _, err := e.invoke(t, repositoryNotificationsMarkRead, "notify-repo", `{"repository":"secret-owner/hidden"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 || *e.reads != reads || strings.Contains(err.Error(), "secret-owner") {
			t.Errorf("%s outside: %v", p.id, err)
		}
		*e.calls = (*e.calls)[:0]
		result, err = e.invoke(t, repositoryNotificationsMarkRead, "notify-open", `{"repository":"acme/widget"}`)
		if err != nil || !reflect.DeepEqual(methods(e), []string{"PUT /api/v1/repos/acme/widget/notifications"}) {
			t.Errorf("%s open: %v, %v, %v", p.id, result, err, methods(e))
		}
	}
}

func TestNotificationChangesAreNeverRepeatedAfterAnUnclearOutcome(t *testing.T) {
	for _, p := range products {
		status := 502
		e := newEnvironment(t, p, notificationServer(&status))
		_, err := e.invoke(t, notificationsMarkAllRead, "notify-open", `{}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
			t.Errorf("%s mark all: %v, calls %d", p.id, err, len(*e.calls))
		}
		*e.calls = (*e.calls)[:0]
		_, err = e.invoke(t, notificationsMarkThread, "notify-open", `{"id":11}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
			t.Errorf("%s mark thread: %v, calls %d", p.id, err, len(*e.calls))
		}
	}
}

func TestExcludedThreadsAreDroppedAndProvenWithoutTargets(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		result, err := e.invoke(t, notificationsList, "exclude", `{}`)
		rows, _ := result["notifications"].([]any)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s list: %v, %v", p.id, result, err)
		}
		if _, err := e.invoke(t, notificationsGet, "exclude", `{"id":11}`); err != nil {
			t.Errorf("%s reachable thread: %v", p.id, err)
		}
		*e.calls = (*e.calls)[:0]
		_, err = e.invoke(t, notificationsGet, "exclude", `{"id":12}`)
		if classOfError(err) != "not-found" || strings.Contains(err.Error(), "hidden") ||
			strings.Contains(err.Error(), "other") || len(*e.calls) != 1 {
			t.Errorf("%s excluded thread: %v, %v", p.id, err, e.paths())
		}
		*e.calls = (*e.calls)[:0]
		if _, err := e.invoke(t, notificationsMarkThread, "exclude", `{"id":12}`); classOfError(err) != "not-found" ||
			len(*e.calls) != 1 {
			t.Errorf("%s mark excluded thread: %v, %v", p.id, err, e.paths())
		}
	}
}

func TestExcludedRepositoryNotificationsAreRefusedBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		for _, connection := range []string{"exclude", "owner-exclude"} {
			reads := *e.reads
			_, err := e.invoke(t, repositoryNotificationsList, connection, `{"repository":"other/hidden"}`)
			if connection == "owner-exclude" {
				_, err = e.invoke(t, repositoryNotificationsList, connection, `{"repository":"acme/gadget"}`)
			}
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
				strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "gadget") ||
				strings.Contains(strings.ToLower(err.Error()), "exclu") {
				t.Errorf("%s %s: %v, calls %v", p.id, connection, err, e.paths())
			}
		}
		// A reachable repository can still be marked, after one proof read of its canonical name.
		result, err := e.invoke(t, repositoryNotificationsMarkRead, "notify-exclude", `{"repository":"acme/widget"}`)
		if err != nil || result["repository"] != "acme/widget" ||
			strings.Join(methods(e), ",") != "GET /api/v1/repos/acme/widget,PUT /api/v1/repos/acme/widget/notifications" {
			t.Errorf("%s mark reachable: %v, %v, %v", p.id, result, err, methods(e))
		}
		// A repository that was renamed into an excluded one is not reached under its old name.
		*e.calls = (*e.calls)[:0]
		_, err = e.invoke(t, repositoryNotificationsMarkRead, "notify-exclude", `{"repository":"old/name"}`)
		if classOfError(err) != "not-found" || len(*e.calls) != 1 {
			t.Errorf("%s renamed: %v, %v", p.id, err, e.paths())
		}
	}
}

func TestNotificationBulkToolsUnderExclusions(t *testing.T) {
	for _, p := range products {
		status := 205
		e := newEnvironment(t, p, notificationServer(&status))
		before, reads := len(*e.calls), *e.reads
		if _, err := e.invoke(t, notificationsMarkAllRead, "notify-exclude", `{}`); !isInvalidRequest(err) ||
			len(*e.calls) != before || *e.reads != reads {
			t.Errorf("%s mark-all with exclusions: %v", p.id, err)
		}
		// The count is one number over the account; exclusions alone allow it, targets do not.
		result, err := e.invoke(t, notificationsCheckNew, "exclude", `{}`)
		if err != nil || result["new"] != float64(3) {
			t.Errorf("%s check-new with exclusions: %v, %v", p.id, result, err)
		}
		if _, err := e.invoke(t, notificationsCheckNew, "owner-exclude", `{}`); !isInvalidRequest(err) {
			t.Errorf("%s check-new with targets and exclusions: %v", p.id, err)
		}
	}
}
