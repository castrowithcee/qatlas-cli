package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const watchFixture = `{"subscribed":true,"ignored":false,"reason":"manual","created_at":"2026-01-01T00:00:00Z",` +
	`"url":"https://forge.example.test/api/v1/repos/acme/widget/subscription",` +
	`"repository_url":"https://forge.example.test/api/v1/repos/acme/widget"}`

func starRepo(full string) string { return lifecycleRepo(full) }

// starServer answers the star and watch routes; status is the answer of every change and of the checks.
func starServer(status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		path := strings.TrimPrefix(c.path, "/api/v1")
		list := `[` + starRepo("acme/widget") + `,` + starRepo("other/tool") + `,null,` + starRepo("acme/gadget") + `]`
		switch {
		case c.method != http.MethodGet && strings.HasSuffix(path, "/subscription") && *status == 200:
			if c.method == http.MethodPut {
				return jsonResponse(200, watchFixture)
			}
			return jsonResponse(204, "")
		case c.method != http.MethodGet:
			return jsonResponse(*status, "")
		case path == "/user/starred" || path == "/users/acme/starred" || path == "/users/bob/starred" ||
			path == "/user/subscriptions" || path == "/users/acme/subscriptions":
			return jsonResponse(200, list, "X-Total-Count", "9")
		case strings.HasPrefix(path, "/user/starred/"):
			return jsonResponse(*status, "")
		case strings.HasSuffix(path, "/stargazers") || strings.HasSuffix(path, "/subscribers"):
			return jsonResponse(200, `[`+userFixture+`,null]`, "Link", `<https://forge.example.test/next>; rel="next"`)
		case strings.HasSuffix(path, "/subscription"):
			if *status == 404 {
				return notFoundResponse()
			}
			if *status != 200 {
				return jsonResponse(*status, "")
			}
			return jsonResponse(200, watchFixture)
		}
		return notFoundResponse()
	}
}

func TestStarAndWatchListsKeepOnlyRowsInsideTheTargets(t *testing.T) {
	for _, p := range products {
		status := 200
		for _, c := range []struct {
			tl         *tool
			connection string
			args       string
			want       []string
			path       string
		}{
			{starsList, "open", `{}`, []string{"acme/widget", "other/tool", "acme/gadget"}, "/api/v1/user/starred"},
			{starsList, "repo", `{}`, []string{"acme/widget"}, "/api/v1/user/starred"},
			{starsList, "org", `{"limit":5}`, []string{"acme/widget", "acme/gadget"}, "/api/v1/user/starred"},
			{starsList, "org", `{"username":"acme"}`, []string{"acme/widget", "acme/gadget"}, "/api/v1/users/acme/starred"},
			{starsList, "repo", `{"username":"acme"}`, []string{"acme/widget"}, "/api/v1/users/acme/starred"},
			{starsList, "open", `{"username":"bob"}`, []string{"acme/widget", "other/tool", "acme/gadget"}, "/api/v1/users/bob/starred"},
			{subscriptionsList, "repo", `{}`, []string{"acme/widget"}, "/api/v1/user/subscriptions"},
			{subscriptionsList, "org", `{"username":"acme"}`, []string{"acme/widget", "acme/gadget"}, "/api/v1/users/acme/subscriptions"},
		} {
			e := newEnvironment(t, p, starServer(&status))
			result, err := e.invoke(t, c.tl, c.connection, c.args)
			if err != nil {
				t.Fatalf("%s %s %s: %v", p.id, c.tl.group, c.args, err)
			}
			var got []string
			for _, row := range rowsOf(t, result, "repositories") {
				got = append(got, row["full_name"].(string))
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s %s on %s = %v, want %v", p.id, c.tl.group, c.args, c.connection, got, c.want)
			}
			// X-Total-Count says more follows, whatever the filter left of the batch.
			if result["has_more"] != true || result["next_cursor"] == nil {
				t.Errorf("%s %s: has_more = %v", p.id, c.tl.group, result)
			}
			if got := methods(e); len(got) != 1 || got[0] != "GET "+c.path {
				t.Errorf("%s %s calls = %v", p.id, c.tl.group, got)
			}
		}
	}
}

func TestStarAndWatchListsRefuseAUserOutsideTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		status := 200
		for _, c := range []struct {
			tl         *tool
			connection string
			args       string
		}{
			{starsList, "org", `{"username":"bob"}`}, {starsList, "repo", `{"username":"bob"}`},
			{starsList, "owner", `{"username":"bob"}`}, {subscriptionsList, "org", `{"username":"bob"}`},
			{starsList, "open", `{"username":"bad name"}`}, {starsList, "open", `{"limit":51}`},
		} {
			e := newEnvironment(t, p, starServer(&status))
			_, err := e.invoke(t, c.tl, c.connection, c.args)
			refused(t, e, c.tl.group+" "+c.args, err)
			if err != nil && strings.Contains(err.Error(), "bob") {
				t.Errorf("%s: the refusal names the user: %v", p.id, err)
			}
		}
	}
}

func TestStarAndWatchListsContinueWithTheirCursor(t *testing.T) {
	for _, p := range products {
		status := 200
		e := newEnvironment(t, p, starServer(&status))
		first, err := e.invoke(t, starsList, "open", `{"limit":4}`)
		if err != nil {
			t.Fatal(err)
		}
		cursor, _ := first["next_cursor"].(string)
		if _, err := e.invoke(t, starsList, "open", `{"cursor":"`+cursor+`"}`); err != nil {
			t.Fatalf("%s: continue: %v", p.id, err)
		}
		if (*e.calls)[1].query != "limit=4&page=2" {
			t.Errorf("%s query = %q", p.id, (*e.calls)[1].query)
		}
		// A cursor of the star list does not continue the watch list.
		if _, err := e.invoke(t, subscriptionsList, "open", `{"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: foreign cursor: %v", p.id, err)
		}
	}
}

func TestStarCheckReadsOneRepository(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, starServer(&status))
		result, err := e.invoke(t, starsCheck, "repo", `{}`)
		if err != nil || result["starred"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		status = 404
		if result, err = e.invoke(t, starsCheck, "repo", `{"repository":"acme/widget"}`); err != nil || result["starred"] != false {
			t.Errorf("%s: not starred: %v, %v", p.id, result, err)
		}
		status = 403
		if _, err = e.invoke(t, starsCheck, "repo", `{}`); classOfError(err) != provider.ClassNotFound {
			t.Errorf("%s: 403 = %v", p.id, err)
		}
		before := len(*e.calls)
		if _, err = e.invoke(t, starsCheck, "repo", `{"repository":"other/tool"}`); !isInvalidRequest(err) || len(*e.calls) != before {
			t.Errorf("%s: foreign repository: %v", p.id, err)
		}
		if want := "GET /api/v1/user/starred/acme/widget"; methods(e)[0] != want {
			t.Errorf("%s calls = %v", p.id, methods(e))
		}
	}
}

func TestStarAndWatchChangesNeedConfirmationAndTheTargetsBeforeAnyIO(t *testing.T) {
	changes := []struct {
		tl     *tool
		method string
		path   string
		key    string
	}{
		{starsAdd, "PUT", "/api/v1/user/starred/acme/widget", "starred"},
		{starsRemove, "DELETE", "/api/v1/user/starred/acme/widget", "unstarred"},
		{repositorySubscriptionsSet, "PUT", "/api/v1/repos/acme/widget/subscription", "subscribed"},
		{repositorySubscriptionsDelete, "DELETE", "/api/v1/repos/acme/widget/subscription", "unsubscribed"},
	}
	for _, p := range products {
		for _, c := range changes {
			status := 200
			if c.tl == starsAdd || c.tl == starsRemove {
				status = 204
			}
			e := newEnvironment(t, p, starServer(&status))
			if _, err := e.invokeAs(t, c.tl, "repo", `{}`, false); err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Fatalf("%s %s: unconfirmed: %v", p.id, c.tl.group, err)
			}
			_, err := e.invoke(t, c.tl, "repo", `{"repository":"other/tool"}`)
			refused(t, e, c.tl.group+" foreign repository", err)
			if err != nil && strings.Contains(err.Error(), "other/tool") {
				t.Errorf("%s: the refusal names the repository: %v", p.id, err)
			}
			result, err := e.invoke(t, c.tl, "repo", `{}`)
			if err != nil || result[c.key] != true || result["repository"] != "acme/widget" {
				t.Fatalf("%s %s: %v, %v", p.id, c.tl.action, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{c.method + " " + c.path}) {
				t.Errorf("%s %s calls = %v", p.id, c.tl.action, got)
			}
			if body := (*e.calls)[0].body; body != "" {
				t.Errorf("%s %s sends a body %q", p.id, c.tl.action, body)
			}
		}
	}
}

func TestStarAndWatchChangesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{starsAdd, starsRemove, repositorySubscriptionsSet, repositorySubscriptionsDelete} {
			status := 502
			e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(status, bodyCanary) })
			_, err := e.invoke(t, tl, "repo", `{}`)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %s: %v after %d calls", p.id, tl.group+"."+tl.action, err, len(*e.calls))
			}
			// A set answered with an unreadable body is unclear as well.
			if tl == repositorySubscriptionsSet {
				e = newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, `{"subscribed":`) })
				_, err = e.invoke(t, tl, "repo", `{}`)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
					t.Errorf("%s: unreadable set: %v after %d calls", p.id, err, len(*e.calls))
				}
			}
		}
	}
}

func TestWatchStateIsReadWithEveryMember(t *testing.T) {
	for _, p := range products {
		status := 200
		e := newEnvironment(t, p, starServer(&status))
		result, err := e.invoke(t, repositorySubscriptionsGet, "repo", `{}`)
		if err != nil || result["subscribed"] != true || result["ignored"] != false || result["reason"] != "manual" ||
			result["created_at"] == nil || result["url"] == nil || result["repository_url"] == nil ||
			result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		status = 404
		result, err = e.invoke(t, repositorySubscriptionsGet, "repo", `{}`)
		if err != nil || result["subscribed"] != false {
			t.Errorf("%s: not watching: %v, %v", p.id, result, err)
		}
		status = 403
		if _, err = e.invoke(t, repositorySubscriptionsGet, "repo", `{}`); classOfError(err) != provider.ClassNotFound {
			t.Errorf("%s: 403 = %v", p.id, err)
		}
	}
}

func TestRepositoryPeopleLists(t *testing.T) {
	for _, p := range products {
		status := 200
		for _, tl := range []*tool{repositoryStargazersList, repositorySubscribersList} {
			e := newEnvironment(t, p, starServer(&status))
			result, err := e.invoke(t, tl, "repo", `{"limit":2}`)
			users := rowsOf(t, result, "users")
			if err != nil || len(users) != 1 || users[0]["login"] != "octo" || result["has_more"] != true ||
				result["next_cursor"] == nil || result["repository"] != "acme/widget" {
				t.Fatalf("%s %s: %v, %v", p.id, tl.group, result, err)
			}
			suffix := strings.TrimSuffix(strings.TrimPrefix(tl.group, "repository-"), "s") + "s"
			if got := methods(e); len(got) != 1 || !strings.HasSuffix(got[0], "/repos/acme/widget/"+suffix) ||
				(*e.calls)[0].query != "limit=2&page=1" {
				t.Errorf("%s %s calls = %v %q", p.id, tl.group, got, (*e.calls)[0].query)
			}
			e = newEnvironment(t, p, starServer(&status))
			_, err = e.invoke(t, tl, "repo", `{"repository":"other/tool"}`)
			refused(t, e, tl.group+" foreign repository", err)
		}
	}
}

func TestStarAndWatchRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{starsList, starsCheck, starsAdd, starsRemove, repositoryStargazersList,
			repositorySubscribersList, repositorySubscriptionsGet, repositorySubscriptionsSet,
			repositorySubscriptionsDelete, subscriptionsList} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is missing", p.toolID(tl))
			}
			change := tl == starsAdd || tl == starsRemove || tl == repositorySubscriptionsSet ||
				tl == repositorySubscriptionsDelete
			if d.RequiresToolAllowList || d.Risk.Idempotency == "" || d.Risk.DataSensitivity == "" || !d.Risk.OpenWorld ||
				(change != (d.Risk.Confirmation == "required")) || (change != (d.Risk.Effect == "update")) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			people := tl == repositoryStargazersList || tl == repositorySubscribersList
			if people != strings.HasSuffix(d.Risk.DataSensitivity, "-personal-data") {
				t.Errorf("%s sensitivity = %s", d.ID, d.Risk.DataSensitivity)
			}
		}
	}
}

func TestExcludedRepositoriesAreAbsentFromStarLists(t *testing.T) {
	for _, p := range products {
		status := 200
		e := newEnvironment(t, p, starServer(&status))
		for _, c := range []struct {
			connection, args string
			want             string
		}{
			{"exclude", `{}`, "acme/widget"},
			{"owner-exclude", `{}`, "acme/widget"},
			{"exclude", `{"username":"bob"}`, "acme/widget"},
			{"owner-exclude", `{"username":"acme"}`, "acme/widget"},
		} {
			result, err := e.invoke(t, starsList, c.connection, c.args)
			if err != nil {
				t.Fatalf("%s %s %s: %v", p.id, c.connection, c.args, err)
			}
			var got []string
			for _, row := range rowsOf(t, result, "repositories") {
				got = append(got, row["full_name"].(string))
			}
			if strings.Join(got, ",") != c.want {
				t.Errorf("%s %s %s = %v, want %s", p.id, c.connection, c.args, got, c.want)
			}
		}
		for _, args := range []string{`{"username":"other"}`, `{"username":"Other"}`} {
			*e.calls = (*e.calls)[:0]
			if _, err := e.invoke(t, starsList, "exclude", args); !isInvalidRequest(err) || len(*e.calls) != 0 ||
				strings.Contains(strings.ToLower(err.Error()), "exclu") {
				t.Errorf("%s excluded owner %s: %v, %v", p.id, args, err, e.paths())
			}
		}
		for _, tl := range []*tool{starsCheck, starsAdd, starsRemove} {
			*e.calls = (*e.calls)[:0]
			if _, err := e.invoke(t, tl, "exclude", `{"repository":"acme/gadget"}`); !isInvalidRequest(err) ||
				len(*e.calls) != 0 {
				t.Errorf("%s %s excluded repository: %v", p.id, tl.action, err)
			}
			if _, err := e.invoke(t, tl, "owner-exclude", `{"repository":"Acme/Gadget"}`); !isInvalidRequest(err) ||
				len(*e.calls) != 0 {
				t.Errorf("%s %s excluded repository by case: %v", p.id, tl.action, err)
			}
		}
	}
}
