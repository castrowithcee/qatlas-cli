package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// issueFixture is the answer of one product for an issue or a pull request of acme/widget. Gitea adds the
// members only it documents.
func issueFixture(p *product, number int, state string, pullRequest bool, repository string) string {
	n := strconv.Itoa(number)
	fields := `"id":` + strconv.Itoa(1000+number) + `,"number":` + n + `,"title":"Crash on start","body":"It crashes",` +
		`"state":"` + state + `","is_locked":false,"comments":2,"updated_at":"2030-01-02T03:04:05Z",` +
		`"repository":{"id":5,"name":"widget","owner":"acme","full_name":"` + repository + `"},` +
		`"user":{"id":3,"login":"alice"},"assignees":[{"id":4,"login":"bob"}],"assignee":{"id":4,"login":"bob"},` +
		`"labels":[{"id":9,"name":"bug","color":"ff0000"}],"milestone":{"id":2,"title":"v1","state":"open"},` +
		`"assets":[{"id":1,"name":"log.txt","size":10}]`
	if p == gitea {
		fields += `,"content_version":7,"time_estimate":3600,"projects":[{"id":11,"title":"Board","state":"open"}]`
	}
	if pullRequest {
		fields += `,"pull_request":{"merged":false,"draft":false}`
	}
	return `{` + fields + `}`
}

func widgetIssue(p *product, number int, state string) string {
	return issueFixture(p, number, state, false, "acme/widget")
}

// issueServer answers the evidence read and the change of issue 7 and records the rest as not found.
func issueServer(p *product, state string, change func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.path == "/api/v1/repos/acme/widget/issues/7" && c.method == http.MethodGet:
			return jsonResponse(200, widgetIssue(p, 7, state))
		case c.path == "/api/v1/repos/acme/widget/issues/8" && c.method == http.MethodGet:
			return jsonResponse(200, issueFixture(p, 8, "open", true, "acme/widget"))
		case c.path == "/api/v1/repos/acme/widget/issues/9" && c.method == http.MethodGet:
			return jsonResponse(200, issueFixture(p, 9, "open", false, "other/place"))
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func bodyOf(t *testing.T, c call) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(c.body), &body); err != nil {
		t.Fatalf("body %q: %v", c.body, err)
	}
	return body
}

func methods(e *environment) []string {
	var out []string
	for _, c := range *e.calls {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

func TestIssueGetShowsProductMembersOnly(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", nil))
		result, err := e.invoke(t, issuesGet, "repo", `{"number":7}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if result["title"] != "Crash on start" || result["number"] != float64(7) || result["updated_at"] == nil ||
			result["assignees"] == nil || result["labels"] == nil || result["assets"] == nil {
			t.Errorf("%s result = %v", p.id, result)
		}
		for _, name := range []string{"content_version", "time_estimate", "projects"} {
			if _, ok := result[name]; ok != (p == gitea) {
				t.Errorf("%s: member %s present = %v", p.id, name, ok)
			}
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		for _, c := range *e.calls {
			if strings.Contains(c.path, "/version") {
				t.Errorf("%s: an issue tool asked for the version", p.id)
			}
		}
	}
}

func TestIssueToolsRefuseThePullRequestIndexBeforeAnyChange(t *testing.T) {
	cases := map[*tool]string{
		issuesGet:         `{"number":8}`,
		issuesUpdate:      `{"number":8,"title":"x"}`,
		issuesClose:       `{"number":8}`,
		issuesReopen:      `{"number":8}`,
		issuesDelete:      `{"number":8}`,
		issueDeadlinesSet: `{"number":8,"due_date":""}`,
	}
	for _, p := range products {
		for tl, arguments := range cases {
			e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
				t.Errorf("%s %s: a change request %s %s", p.id, tl.action, c.method, c.path)
				return notFoundResponse()
			}))
			connection := "repo"
			if tl == issuesDelete {
				connection = "delete"
			}
			if _, err := e.invoke(t, tl, connection, arguments); !isInvalidRequest(err) ||
				!strings.Contains(err.Error(), "pull-request tools") {
				t.Errorf("%s %s: error = %v", p.id, tl.action, err)
			}
			if len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodGet {
				t.Errorf("%s %s: calls = %v", p.id, tl.action, methods(e))
			}
		}
	}
}

func TestIssueOutsideTheTargetsIsRefusedWithoutNamingIt(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", nil))
		// The argument lies outside the targets: refused before the secret and any request.
		_, err := e.invoke(t, issuesGet, "repo", `{"repository":"secret-owner/hidden","number":7}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: %v, reads %d, calls %d", p.id, err, *e.reads, len(*e.calls))
		}
		// The canonical repository of the answer lies outside the targets.
		before := len(*e.calls)
		_, err = e.invoke(t, issuesGet, "repo", `{"number":9}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "other/place") || len(*e.calls) != before+1 {
			t.Errorf("%s: %v, calls %v", p.id, err, methods(e))
		}
		if _, err := e.invoke(t, issuesGet, "open", `{"number":7}`); !isInvalidRequest(err) {
			t.Errorf("%s: a repository is required without a single target: %v", p.id, err)
		}
	}
}

func TestIssueMembersOfTheOtherProductAreRefusedBeforeTheSecret(t *testing.T) {
	foreign := map[string][]struct {
		tool      *tool
		arguments string
	}{
		forgejo.id: {
			{issuesUpdate, `{"number":7,"content_version":3}`},
			{issuesUpdate, `{"number":7,"projects":[1]}`},
			{issuesCreate, `{"title":"x","projects":[1]}`},
			{issuesSearch, `{"created_by":"alice"}`},
			{issuesList, `{"time_estimate":5}`},
		},
		gitea.id: {
			{issuesUpdate, `{"number":7,"updated_at":"2030-01-02T03:04:05Z"}`},
			{issuesList, `{"sort":"latest"}`},
			{issuesSearch, `{"sort":"latest"}`},
			{issuesSearch, `{"priority_repo_id":4}`},
		},
	}
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", nil))
		for _, entry := range foreign[p.id] {
			if _, err := e.invokeAs(t, entry.tool, "repo", entry.arguments, true); !isInvalidRequest(err) {
				t.Errorf("%s %s %s: %v", p.id, entry.tool.action, entry.arguments, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
	// The tool itself, called without the schema, refuses the same members.
	for _, p := range products {
		for _, entry := range foreign[p.id] {
			if entry.tool == issuesList {
				continue
			}
			if _, err := entry.tool.prepare(p, allowlist{}, json.RawMessage(entry.arguments)); !isInvalidRequest(err) {
				t.Errorf("%s %s prepare: %v", p.id, entry.arguments, err)
			}
		}
	}
}

func TestIssueCreateSendsOneRequestWithValidatedMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(201, widgetIssue(p, 12, "open"))
		})
		arguments := `{"title":"Crash","body":"Details","assignees":["bob","carol"],"labels":[3,4],"milestone":2,` +
			`"ref":"refs/heads/main","closed":false`
		want := map[string]any{"title": "Crash", "body": "Details", "assignees": []any{"bob", "carol"},
			"labels": []any{float64(3), float64(4)}, "milestone": float64(2), "ref": "refs/heads/main", "closed": false}
		if p == gitea {
			arguments += `,"projects":[11]`
			want["projects"] = []any{float64(11)}
		}
		// Without confirmation nothing is sent.
		if _, err := e.invokeAs(t, issuesCreate, "repo", arguments+`}`, false); err == nil ||
			!errors.As(err, new(*application.ConfirmationRequiredError)) || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v, calls %d", p.id, err, len(*e.calls))
		}
		result, err := e.invoke(t, issuesCreate, "repo", arguments+`}`)
		if err != nil || result["number"] != float64(12) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		sent := (*e.calls)[0]
		if len(*e.calls) != 1 || sent.method != http.MethodPost || sent.path != "/api/v1/repos/acme/widget/issues" ||
			sent.contentType != "application/json" || sent.query != "" || !reflect.DeepEqual(bodyOf(t, sent), want) {
			t.Errorf("%s: call %+v", p.id, sent)
		}
		for _, bad := range []string{`{"title":""}`, `{"body":"x"}`, `{"title":"x","assignees":["a b"]}`,
			`{"title":"x","labels":[0]}`, `{"title":"x","milestone":-1}`, `{"title":"x","state":"closed"}`,
			`{"title":"x","due_date":"2030-01-01T00:00:00Z"}`, `{"title":"x","assignee":"bob"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, issuesCreate, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestIssueUpdateReadsThenPatchesOnlyTheSetMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			if c.method == http.MethodPatch && c.path == "/api/v1/repos/acme/widget/issues/7" {
				return jsonResponse(201, widgetIssue(p, 7, "open"))
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, issuesUpdate, "repo", `{"number":7,"title":"New"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed update: %v", p.id, err)
		}
		arguments, want := `{"number":7,"title":"New","assignees":[]`, map[string]any{"title": "New", "assignees": []any{}}
		if p == gitea {
			arguments += `,"content_version":7,"projects":[]`
			want["content_version"], want["projects"] = float64(7), []any{}
		} else {
			arguments += `,"updated_at":"2030-01-02T03:04:05Z"`
			want["updated_at"] = "2030-01-02T03:04:05Z"
		}
		if _, err := e.invoke(t, issuesUpdate, "repo", arguments+`}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"PATCH /api/v1/repos/acme/widget/issues/7"}) {
			t.Fatalf("%s calls = %v", p.id, got)
		}
		if body := bodyOf(t, (*e.calls)[1]); !reflect.DeepEqual(body, want) {
			t.Errorf("%s body = %v, want %v", p.id, body, want)
		}
		before := len(*e.calls)
		for _, bad := range []string{`{"number":7}`, `{"number":7,"state":"closed"}`, `{"number":7,"labels":[1]}`,
			`{"number":7,"closed":true}`, `{"number":7,"due_date":"2030-01-01T00:00:00Z"}`, `{"title":"x"}`} {
			if _, err := e.invoke(t, issuesUpdate, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestIssueCloseAndReopenAreIdempotent(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{issuesClose, issuesReopen} {
			target, current := "closed", "open"
			if tl == issuesReopen {
				target, current = "open", "closed"
			}
			e := newEnvironment(t, p, issueServer(p, current, func(c call) *http.Response {
				if c.method == http.MethodPatch && c.path == "/api/v1/repos/acme/widget/issues/7" {
					return jsonResponse(201, widgetIssue(p, 7, target))
				}
				return notFoundResponse()
			}))
			result, err := e.invoke(t, tl, "repo", `{"number":7}`)
			if err != nil || result["changed"] != true || result["state"] != target {
				t.Fatalf("%s %s: %v, %v", p.id, tl.action, result, err)
			}
			if got := methods(e); len(got) != 2 || got[1] != "PATCH /api/v1/repos/acme/widget/issues/7" ||
				(*e.calls)[1].body != `{"state":"`+target+`"}` {
				t.Errorf("%s %s calls = %v %+v", p.id, tl.action, got, *e.calls)
			}
			// An issue in the requested state is a success without a change.
			same := newEnvironment(t, p, issueServer(p, target, func(c call) *http.Response {
				t.Errorf("%s %s: change request %s", p.id, tl.action, c.method)
				return notFoundResponse()
			}))
			result, err = same.invoke(t, tl, "repo", `{"number":7}`)
			if err != nil || result["changed"] != false || len(*same.calls) != 1 {
				t.Errorf("%s %s: %v, %v, calls %v", p.id, tl.action, result, err, methods(same))
			}
		}
	}
}

func TestIssueDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(issuesDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, other := range reg.Provider(p.id) {
			if other.ID != d.ID && other.RequiresToolAllowList && !strings.Contains(other.ID, ".delete") &&
				other.ID != p.toolID(pullRequestsMerge) && other.ID != p.toolID(pullRequestBranchesUpdate) &&
				other.ID != p.toolID(issueTimesReset) && other.ID != p.id+".branches.update-ref" &&
				other.ID != p.toolID(reviewsApprove) && other.ID != p.toolID(actionsSecretsSet) &&
				other.ID != p.toolID(orgsRename) && other.ID != p.toolID(adminUsersRename) && other.ID != p.toolID(notificationsMarkAllRead) &&
				other.ID != p.toolID(repositoryNotificationsMarkRead) &&
				!slices.Contains(lifecycleListedOnly(p), other.ID) {
				t.Errorf("%s is listed-only too", other.ID)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("%s: profile %s holds issues.delete", p.id, profile.ID)
				}
			}
		}
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		// A connection that does not name the tool never offers it.
		if _, err := e.invoke(t, issuesDelete, "repo", `{"number":7}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, issuesDelete, "delete", `{"number":7}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, issuesDelete, "delete", `{"number":7}`)
		if err != nil || result["deleted"] != true || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"DELETE /api/v1/repos/acme/widget/issues/7"}) || (*e.calls)[1].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestIssueDeadlineSetsAndRemoves(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			if c.method == http.MethodPost && c.path == "/api/v1/repos/acme/widget/issues/7/deadline" {
				return jsonResponse(201, `{"due_date":"2030-01-31T00:00:00Z"}`)
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, issueDeadlinesSet, "repo", `{"number":7,"due_date":"2030-01-31T00:00:00Z"}`)
		if err != nil || result["due_date"] != "2030-01-31T00:00:00Z" || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if _, err := e.invoke(t, issueDeadlinesSet, "repo", `{"number":7,"due_date":""}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		calls := *e.calls
		if len(calls) != 4 || calls[1].body != `{"due_date":"2030-01-31T00:00:00Z"}` || calls[3].body != `{"due_date":null}` ||
			calls[1].method != http.MethodPost {
			t.Errorf("%s calls = %v", p.id, methods(e))
		}
		before := len(*e.calls)
		for _, bad := range []string{`{"number":7}`, `{"number":7,"due_date":"tomorrow"}`} {
			if _, err := e.invoke(t, issueDeadlinesSet, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestIssueSearchDropsRowsOutsideTheTargets(t *testing.T) {
	for _, p := range products {
		rows := "[" + widgetIssue(p, 1, "open") + "," + issueFixture(p, 2, "open", false, "other/place") + "," +
			issueFixture(p, 3, "open", true, "acme/widget") + "," + issueFixture(p, 4, "open", false, "Acme/Widget") +
			`,{"id":5,"number":5,"title":"no repository"}]`
		e := newEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(200, rows, "Link", `<https://forge.example.test/api/v1/repos/issues/search?page=2>; rel="next"`)
		})
		arguments := `{"q":"crash","state":"all","labels":["bug","p1"],"owner":"acme","assigned":true,"limit":10}`
		result, err := e.invoke(t, issuesSearch, "repo", arguments)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		issues, _ := result["issues"].([]any)
		if len(issues) != 2 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Errorf("%s result = %v", p.id, result)
		}
		sent := (*e.calls)[0]
		query, _ := url.ParseQuery(sent.query)
		if sent.method != http.MethodGet || sent.path != "/api/v1/repos/issues/search" || query.Get("type") != "issues" ||
			query.Get("q") != "crash" || query.Get("labels") != "bug,p1" || query.Get("state") != "all" ||
			query.Get("owner") != "acme" || query.Get("assigned") != "true" || query.Get("limit") != "10" ||
			query.Get("page") != "1" {
			t.Errorf("%s query = %v", p.id, query)
		}
		// The cursor continues with the same filters and page 2.
		next := strings.TrimSuffix(arguments, "}") + `,"cursor":"` + result["next_cursor"].(string) + `"}`
		if _, err := e.invoke(t, issuesSearch, "repo", next); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if again, _ := url.ParseQuery((*e.calls)[1].query); again.Get("page") != "2" {
			t.Errorf("%s: second query = %v", p.id, again)
		}
		changed := strings.Replace(next, `"crash"`, `"other"`, 1)
		if _, err := e.invoke(t, issuesSearch, "repo", changed); !isInvalidRequest(err) {
			t.Errorf("%s: a cursor of other filters was accepted: %v", p.id, err)
		}
		for _, bad := range []string{`{"owner":"secret-owner"}`, `{"team":"devs"}`, `{"since":"yesterday"}`,
			`{"labels":["a,b"]}`, `{"state":"merged"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, issuesSearch, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
	e := newEnvironment(t, forgejo, func(call) *http.Response { return jsonResponse(200, "[]") })
	if _, err := e.invoke(t, issuesSearch, "open", `{"priority_repo_id":4,"sort":"recentupdate"}`); err != nil {
		t.Fatal(err)
	}
	if query, _ := url.ParseQuery((*e.calls)[0].query); query.Get("priority_repo_id") != "4" || query.Get("sort") != "recentupdate" {
		t.Errorf("query = %v", query)
	}
	g := newEnvironment(t, gitea, func(call) *http.Response { return jsonResponse(200, "[]") })
	if _, err := g.invoke(t, issuesSearch, "open", `{"created_by":"alice"}`); err != nil {
		t.Fatal(err)
	}
	if query, _ := url.ParseQuery((*g.calls)[0].query); query.Get("created_by") != "alice" {
		t.Errorf("query = %v", query)
	}
}

func TestIssueListAsksForIssuesOfOneRepository(t *testing.T) {
	for _, p := range products {
		rows := "[" + widgetIssue(p, 1, "open") + "," + issueFixture(p, 3, "open", true, "acme/widget") + "," +
			issueFixture(p, 4, "open", false, "other/place") + "]"
		e := newEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(200, rows, "X-Total-Count", "90")
		})
		arguments := `{"state":"closed","milestones":["v1"],"created_by":"alice","assigned_by":"bob",` +
			`"mentioned_by":"carol","since":"2030-01-01T00:00:00Z","before":"2031-01-01T00:00:00+01:00"`
		if p == forgejo {
			arguments += `,"sort":"mostcomment"`
		}
		result, err := e.invoke(t, issuesList, "repo", arguments+`}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		issues, _ := result["issues"].([]any)
		if len(issues) != 1 || result["has_more"] != true || result["repository"] != "acme/widget" {
			t.Errorf("%s result = %v", p.id, result)
		}
		sent := (*e.calls)[0]
		query, _ := url.ParseQuery(sent.query)
		wantSort := ""
		if p == forgejo {
			wantSort = "mostcomment"
		}
		if sent.method != http.MethodGet || sent.path != "/api/v1/repos/acme/widget/issues" || query.Get("type") != "issues" ||
			query.Get("state") != "closed" || query.Get("milestones") != "v1" || query.Get("created_by") != "alice" ||
			query.Get("assigned_by") != "bob" || query.Get("mentioned_by") != "carol" || query.Get("sort") != wantSort ||
			query.Get("limit") != "30" {
			t.Errorf("%s query = %v", p.id, query)
		}
	}
}

func TestIssueTimelineShowsOtherObjectsByIdentifiersOnly(t *testing.T) {
	const events = `[{"id":1,"type":"comment","body":"hello","user":{"id":3,"login":"alice"},` +
		`"ref_issue":{"id":50,"number":4,"title":"foreign title","body":"foreign body",` +
		`"repository":{"id":9,"name":"vault","owner":"secret","full_name":"secret/vault","private":true}},` +
		`"ref_comment":{"id":77,"body":"foreign comment","html_url":"https://forge.example.test/secret/vault"},` +
		`"dependent_issue":{"id":51,"number":5,"title":"foreign"},"assignee_team":{"id":2,"name":"devs",` +
		`"organization":{"id":1,"name":"acme","email":"hidden"}},"tracked_time":{"id":4,"time":60,"user_name":"bob",` +
		`"issue":{"id":50,"number":4,"title":"foreign title"}},"label":{"id":9,"name":"bug"}}]`
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, events) })
		result, err := e.invoke(t, issueTimelineList, "repo", `{"number":7,"since":"2030-01-01T00:00:00Z","limit":5}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		sent := (*e.calls)[0]
		query, _ := url.ParseQuery(sent.query)
		if sent.method != http.MethodGet || sent.path != "/api/v1/repos/acme/widget/issues/7/timeline" ||
			query.Get("since") != "2030-01-01T00:00:00Z" || query.Get("limit") != "5" || len(*e.calls) != 1 {
			t.Errorf("%s call = %+v", p.id, sent)
		}
		encoded, _ := json.Marshal(result)
		for _, leaked := range []string{"foreign", "private", "hidden", "forge.example.test/secret"} {
			if strings.Contains(string(encoded), leaked) {
				t.Errorf("%s: the timeline shows %q: %s", p.id, leaked, encoded)
			}
		}
		for _, kept := range []string{`"number":4`, `"full_name":"secret/vault"`, `"id":77`, `"login":"alice"`} {
			if !strings.Contains(string(encoded), kept) {
				t.Errorf("%s: the timeline lacks %s: %s", p.id, kept, encoded)
			}
		}
	}
}

func TestIssueTimelineLeavesOutReferencesFromOutsideTheTargets(t *testing.T) {
	ref := func(id int, kind, repository string) string {
		source := `"ref_issue":{"id":50,"number":4`
		if repository != "" {
			source += `,"repository":{"full_name":"` + repository + `"}`
		}
		return `{"id":` + strconv.Itoa(id) + `,"type":"` + kind + `",` + source + `}}`
	}
	events := "[" + ref(1, "issue_ref", "other/place") + "," + ref(2, "pull_ref", "acme/widget") + "," +
		ref(3, "comment_ref", "Acme/Widget") + "," + ref(4, "issue_ref", "") + "," + ref(5, "pull_ref", "other/place") + "," +
		ref(6, "comment_ref", "other/place") + `,{"id":7,"type":"commit_ref","ref_commit_sha":"abc"},` +
		`{"id":8,"type":"dependent_issue","dependent_issue":{"id":51,"number":5,"repository":{"full_name":"other/place"}}},` +
		`{"id":9,"type":"comment","body":"hi"}]`
	ids := func(result map[string]any) []float64 {
		var got []float64
		list, _ := result["events"].([]any)
		for _, event := range list {
			row, _ := event.(map[string]any)
			id, _ := row["id"].(float64)
			got = append(got, id)
		}
		return got
	}
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, events, "X-Total-Count", "90") })
		result, err := e.invoke(t, issueTimelineList, "repo", `{"number":7,"limit":9}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := ids(result); !reflect.DeepEqual(got, []float64{2, 3, 7, 8, 9}) || result["has_more"] != true {
			t.Errorf("%s: scoped timeline = %v %v", p.id, got, result)
		}
		open := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, events) })
		result, err = open.invoke(t, issueTimelineList, "open", `{"repository":"acme/widget","number":7}`)
		if got := ids(result); err != nil || len(got) != 9 {
			t.Errorf("%s: open timeline = %v %v", p.id, got, err)
		}
	}
}

// A change is sent once and never repeated. An open outcome says so; a refusal that proves nothing happened
// does not.
func TestAChangeWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	failures := map[string]func(c call) (*http.Response, error){
		"500":     func(call) (*http.Response, error) { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`), nil },
		"502":     func(call) (*http.Response, error) { return jsonResponse(502, ""), nil },
		"503":     func(call) (*http.Response, error) { return jsonResponse(503, ""), nil },
		"timeout": func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":   func(call) (*http.Response, error) { return nil, errors.New("read: connection reset by peer") },
		"garbage": func(call) (*http.Response, error) { return jsonResponse(201, `{"number":`), nil },
	}
	for _, p := range products {
		for name, failure := range failures {
			e := newEnvironment(t, p, issueServer(p, "open", nil))
			inner := transport
			transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPatch {
					return inner.RoundTrip(r)
				}
				*e.calls = append(*e.calls, call{method: r.Method, path: r.URL.Path})
				return failure(call{})
			})
			_, err := e.invoke(t, issuesClose, "repo", `{"number":7}`)
			patches := 0
			for _, c := range *e.calls {
				if c.method == http.MethodPatch {
					patches++
				}
			}
			var failureError *provider.Error
			if !errors.As(err, &failureError) || !strings.HasSuffix(failureError.Message, uncertain) || patches != 1 {
				t.Errorf("%s %s: %v, patches %d", p.id, name, err, patches)
			}
		}
		// A refusal proves that nothing was applied.
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			return jsonResponse(http.StatusUnprocessableEntity, `{"message":"`+bodyCanary+`"}`)
		}))
		_, err := e.invoke(t, issuesClose, "repo", `{"number":7}`)
		var refused *provider.Error
		if !errors.As(err, &refused) || strings.Contains(refused.Message, uncertain) || len(*e.calls) != 2 {
			t.Errorf("%s: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestChangeAllowsOnlyTheMutatingMethods(t *testing.T) {
	c := &Client{product: gitea, origin: baseURL, auth: "token x", http: &http.Client{Timeout: time.Second}}
	for _, method := range []string{http.MethodGet, http.MethodHead, "SUDO", ""} {
		if err := c.change(context.Background(), "test", method, "/repos/a/b/issues", nil, nil); err == nil {
			t.Errorf("change accepted %q", method)
		}
	}
	// PUT is a mutating method and reaches the transport with its body.
	var calls []call
	serve(t, &calls, func(call) *http.Response { return jsonResponse(200, `[]`) })
	c = &Client{product: gitea, origin: baseURL, auth: "token " + tokenValue, http: provider.NoRedirectClient(time.Second, transport),
		limiter: limiters.For(tokenValue)}
	var out []object
	if err := c.change(context.Background(), "test", http.MethodPut, "/repos/a/b/issues/1/labels",
		map[string]any{"labels": []int64{1}}, &out); err != nil || len(calls) != 1 || calls[0].method != http.MethodPut ||
		calls[0].body != `{"labels":[1]}` || calls[0].contentType != "application/json" {
		t.Errorf("PUT = %v, %+v", err, calls)
	}
}
