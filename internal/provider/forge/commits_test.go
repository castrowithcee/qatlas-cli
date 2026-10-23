package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const commitUser = `{"id":5,"login":"alice","full_name":"Alice","email":"alice@example.test","created":"2030-01-01T00:00:00Z"}`

func commitFixture(sha string) string {
	return `{"sha":"` + sha + `","url":"https://forge.example.test/api/v1/repos/acme/widget/git/commits/` + sha + `",` +
		`"html_url":"https://forge.example.test/acme/widget/commit/` + sha + `","created":"2030-01-02T03:04:05Z",` +
		`"author":` + commitUser + `,"committer":` + commitUser + `,"parents":[{"sha":"aaa1111","url":"u","created":"c"}],` +
		`"commit":{"message":"fix: thing","url":"u","tree":{"sha":"t1","url":"u","created":"c"},` +
		`"author":{"name":"Alice","email":"alice@example.test","date":"2030-01-02T03:04:05Z"},` +
		`"committer":{"name":"Alice","email":"alice@example.test","date":"2030-01-02T03:04:05Z"},` +
		`"verification":{"verified":true,"reason":"ok","payload":"RAW-PAYLOAD","signature":"RAW-SIG",` +
		`"signer":{"name":"Alice","email":"alice@example.test","username":"alice"}}},` +
		`"stats":{"additions":3,"deletions":1,"total":4},"files":[{"filename":"a.go","status":"modified"}]}`
}

func statusFixture(id int, state string) string {
	return `{"id":` + itoa(id) + `,"context":"ci/build","status":"` + state + `","description":"d",` +
		`"target_url":"https://ci.example.test/run/1","url":"u","creator":` + commitUser +
		`,"created_at":"2030-01-02T03:04:05Z","updated_at":"2030-01-02T03:04:06Z"}`
}

func commitServer(p *product, extra func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		const base = "/api/v1/repos/acme/widget"
		if extra != nil {
			if r := extra(c); r != nil {
				return r
			}
		}
		switch {
		case c.path == base+"/commits":
			return jsonResponse(200, `[`+commitFixture("c0ffee1")+`]`, "X-HasMore", "true")
		case strings.HasPrefix(c.path, base+"/git/commits/") && !strings.Contains(c.path, "."):
			return jsonResponse(200, commitFixture(strings.TrimPrefix(c.path, base+"/git/commits/")))
		case strings.HasPrefix(c.path, base+"/compare/"):
			return jsonResponse(200, `{"total_commits":1,"commits":[`+commitFixture("c0ffee1")+`],`+
				`"files":[{"filename":"a.go","status":"added"}]}`)
		case c.path == base+"/statuses/c0ffee1" && c.method == http.MethodGet,
			strings.HasSuffix(c.path, "/statuses") && strings.HasPrefix(c.path, base+"/commits/"):
			return jsonResponse(200, `[`+statusFixture(1, "success")+`]`, "X-Total-Count", "1")
		case c.path == base+"/statuses/c0ffee1" && c.method == http.MethodPost:
			return jsonResponse(201, statusFixture(2, "pending"))
		case strings.HasSuffix(c.path, "/status") && strings.HasPrefix(c.path, base+"/commits/"):
			return jsonResponse(200, `{"sha":"c0ffee1","state":"success","total_count":1,"commit_url":"u","url":"u",`+
				`"statuses":[`+statusFixture(1, "success")+`],"repository":{"id":1,"name":"widget","full_name":"acme/widget"}}`)
		}
		return notFoundResponse()
	}
}

func TestCommitsListFiltersPagingAndProductMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		args := `{"sha":"main","path":"./src//lib/","not":"dev","stat":false,"verification":false,"files":false,"limit":10`
		if p == gitea {
			args += `,"since":"2030-01-01T00:00:00Z","until":"2030-02-01T00:00:00Z"`
		}
		result, err := e.invoke(t, commitsList, "repo", args+`}`)
		if err != nil || result["has_more"] != true || result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		q, _ := url.ParseQuery((*e.calls)[0].query)
		if q.Get("sha") != "main" || q.Get("path") != "src/lib" || q.Get("not") != "dev" || q.Get("stat") != "false" ||
			q.Get("verification") != "false" || q.Get("files") != "false" || q.Get("page") != "1" || q.Get("limit") != "10" ||
			(q.Get("since") != "") != (p == gitea) {
			t.Errorf("%s query = %v", p.id, q)
		}
		commit := result["commits"].([]any)[0].(map[string]any)
		verification := commit["commit"].(map[string]any)["verification"].(map[string]any)
		if commit["sha"] != "c0ffee1" || commit["stats"] == nil || commit["files"] == nil || verification["payload"] != nil ||
			verification["verified"] != true || commit["author"].(map[string]any)["login"] != "alice" {
			t.Errorf("%s commit = %v", p.id, commit)
		}
		// The next batch continues with page 2; a cursor does not follow other filters.
		cursor := `,"cursor":"` + result["next_cursor"].(string) + `"}`
		if _, err := e.invoke(t, commitsList, "repo", args+cursor); err != nil {
			t.Errorf("%s: %v", p.id, err)
		} else if q, _ := url.ParseQuery((*e.calls)[1].query); q.Get("page") != "2" {
			t.Errorf("%s: second query = %v", p.id, q)
		}
		if _, err := e.invoke(t, commitsList, "repo", strings.Replace(args, `"main"`, `"dev"`, 1)+cursor); !isInvalidRequest(err) {
			t.Errorf("%s: a cursor with other filters was accepted: %v", p.id, err)
		}
	}
	e := newEnvironment(t, forgejo, commitServer(forgejo, nil))
	for _, bad := range []string{`{"since":"2030-01-01T00:00:00Z"}`, `{"until":"2030-01-01T00:00:00Z"}`} {
		if _, err := e.invoke(t, commitsList, "repo", bad); !isInvalidRequest(err) {
			t.Errorf("forgejo accepted %s: %v", bad, err)
		}
	}
	if len(*e.calls) != 0 || *e.reads != 0 {
		t.Errorf("calls %v, reads %d", methods(e), *e.reads)
	}
}

func TestCommitsListNextPageFromHasMoreFalse(t *testing.T) {
	e := newEnvironment(t, gitea, func(c call) *http.Response {
		return jsonResponse(200, `[`+commitFixture("c0ffee1")+`]`, "X-HasMore", "false", "X-Total-Count", "99")
	})
	result, err := e.invoke(t, commitsList, "repo", `{}`)
	if err != nil || result["has_more"] != false || result["next_cursor"] != nil {
		t.Fatalf("%v, %v", result, err)
	}
}

func TestCommitsGetAndDiff(t *testing.T) {
	long := strings.Repeat("é", maxDiffBytes)
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/git/commits/c0ffee1.patch") {
				return &http.Response{StatusCode: 200, Body: readerBody(long), Header: http.Header{}}
			}
			if strings.HasSuffix(c.path, "/git/commits/c0ffee1.diff") {
				return &http.Response{StatusCode: 200, Body: readerBody("diff --git a b"), Header: http.Header{}}
			}
			return nil
		}))
		got, err := e.invoke(t, commitsGet, "repo", `{"sha":"C0FFEE1","stat":false}`)
		if err != nil || got["sha"] != "C0FFEE1" || (*e.calls)[0].query != "stat=false" {
			t.Fatalf("%s: %v, %v, %v", p.id, got, err, *e.calls)
		}
		diff, err := e.invoke(t, commitDiffsGet, "repo", `{"sha":"c0ffee1"}`)
		if err != nil || diff["content"] != "diff --git a b" || diff["truncated"] != false || diff["type"] != "diff" {
			t.Fatalf("%s: %v, %v", p.id, diff, err)
		}
		patch, err := e.invoke(t, commitDiffsGet, "repo", `{"sha":"c0ffee1","type":"patch"}`)
		content, _ := patch["content"].(string)
		if err != nil || patch["truncated"] != true || len(content) > maxDiffBytes || len(content) < maxDiffBytes-3 ||
			strings.Contains(content, "�") {
			t.Errorf("%s: truncated %v, %d bytes, %v", p.id, patch["truncated"], len(content), err)
		}
	}
}

func TestCommitArgumentsAreRefusedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		tool *tool
		args string
	}{
		{commitsGet, `{"sha":"zzzzzzz"}`}, {commitsGet, `{"sha":"abc"}`}, {commitsGet, `{"sha":"main"}`},
		{commitDiffsGet, `{"sha":"c0ffee1","type":"zip"}`}, {commitDiffsGet, `{"sha":"../etc"}`},
		{commitsList, `{"sha":"a..b"}`}, {commitsList, `{"sha":"x y"}`}, {commitsList, `{"not":"a..b"}`},
		{commitsList, `{"path":"../secret"}`}, {commitsList, `{"path":"/etc/passwd"}`}, {commitsList, `{"path":"a/../../b"}`},
		{commitsList, `{"path":"."}`}, {commitsList, `{"path":"a\\b"}`},
		{compareGet, `{"basehead":"main"}`}, {compareGet, `{"basehead":"a..b...c"}`}, {compareGet, `{"basehead":"main...fork:feature"}`},
		{compareGet, `{"basehead":"...head"}`}, {compareGet, `{"basehead":"main...x y"}`},
		{commitStatusesList, `{}`}, {commitStatusesList, `{"ref":"main","sha":"c0ffee1"}`},
		{commitStatusesList, `{"sha":"nothex!"}`}, {commitStatusesList, `{"ref":"a..b"}`},
		{commitStatusesList, `{"ref":"main","state":"skipped"}`}, {commitStatusesList, `{"ref":"main","sort":"x"}`},
		{commitStatusesGetCombined, `{"ref":"a..b"}`}, {commitStatusesGetCombined, `{}`},
		{commitStatusesCreate, `{"sha":"main","state":"success"}`}, {commitStatusesCreate, `{"sha":"c0ffee1","state":"bogus"}`},
		{commitStatusesCreate, `{"sha":"c0ffee1","state":"success","target_url":"javascript:alert(1)"}`},
		{commitStatusesCreate, `{"sha":"c0ffee1","state":"success","target_url":"ftp://x.example.test/a"}`},
		{commitStatusesCreate, `{"sha":"c0ffee1","state":"success","target_url":"https://x.example.test/` + strings.Repeat("a", 2100) + `"}`},
		{commitStatusesCreate, `{"sha":"c0ffee1","state":"success","description":"` + strings.Repeat("a", 1025) + `"}`},
		{commitStatusesCreate, `{"sha":"c0ffee1","state":"success","context":"` + strings.Repeat("a", 256) + `"}`},
		{commitStatusesCreate, `{"repository":"other/place","sha":"c0ffee1","state":"success"}`},
		{commitsList, `{"repository":"other/place"}`}, {compareGet, `{"repository":"other/place","basehead":"a...b"}`},
	}
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		for _, c := range cases {
			if _, err := e.invoke(t, c.tool, "repo", c.args); !isInvalidRequest(err) {
				t.Errorf("%s %s %s: %v", p.id, c.tool.action, c.args, err)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: calls %v, token reads %d", p.id, methods(e), *e.reads)
		}
	}
	// Gitea has the skipped state, Forgejo does not.
	e := newEnvironment(t, forgejo, commitServer(forgejo, nil))
	if _, err := e.invoke(t, commitStatusesCreate, "repo", `{"sha":"c0ffee1","state":"skipped"}`); !isInvalidRequest(err) {
		t.Errorf("forgejo accepted skipped: %v", err)
	}
	e = newEnvironment(t, gitea, commitServer(gitea, nil))
	if _, err := e.invoke(t, commitStatusesCreate, "repo", `{"sha":"c0ffee1","state":"skipped"}`); err != nil {
		t.Errorf("gitea refused skipped: %v", err)
	}
}

func TestCompareGet(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		result, err := e.invoke(t, compareGet, "repo", `{"basehead":"main...release/1.0"}`)
		if err != nil || result["total_commits"] == nil || len(result["commits"].([]any)) != 1 ||
			(result["files"] != nil) != (p == forgejo) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := (*e.calls)[0].path; got != "/api/v1/repos/acme/widget/compare/main...release/1.0" {
			t.Errorf("%s path = %q", p.id, got)
		}
	}
}

func TestCompareCutsCommitsAndGiteaOutput(t *testing.T) {
	many := strings.Repeat(commitFixture("c0ffee1")+",", 120) + commitFixture("c0ffee2")
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		return jsonResponse(200, `{"total_commits":121,"commits":[`+many+`]}`)
	})
	result, err := e.invoke(t, compareGet, "repo", `{"basehead":"a...b"}`)
	if err != nil || result["truncated"] != true || len(result["commits"].([]any)) != maxListItems {
		t.Fatalf("%v, %v", result, err)
	}
	if _, err := e.invoke(t, compareGet, "repo", `{"basehead":"a...b","output":"diff"}`); !isInvalidRequest(err) {
		t.Errorf("forgejo accepted output: %v", err)
	}
	g := newEnvironment(t, gitea, func(c call) *http.Response {
		return &http.Response{StatusCode: 200, Body: readerBody("diff text"), Header: http.Header{}}
	})
	text, err := g.invoke(t, compareGet, "repo", `{"basehead":"a...b","output":"patch"}`)
	if err != nil || text["content"] != "diff text" || text["type"] != "patch" || (*g.calls)[0].query != "output=patch" {
		t.Fatalf("%v, %v, %v", text, err, *g.calls)
	}
}

func TestCompareFlagsNeedANewerVersion(t *testing.T) {
	cases := []struct {
		p       *product
		handler func(call) *http.Response
		allowed bool
		needs   string
	}{
		{forgejo, forgejoAt("15.0.10", commitServer(forgejo, nil)), false, "Forgejo 17.0.0"},
		{forgejo, forgejoAt("17.0.0", commitServer(forgejo, nil)), true, ""},
		{gitea, giteaAt("1.27.3", commitServer(gitea, nil)), false, "Gitea 28.2.0"},
		{gitea, giteaAt("28.2.0", commitServer(gitea, nil)), true, ""},
	}
	for _, tc := range cases {
		e := newEnvironment(t, tc.p, tc.handler)
		_, err := e.invoke(t, compareGet, "repo", `{"basehead":"a...b","files":false}`)
		if (err == nil) != tc.allowed || (err != nil && !strings.Contains(err.Error(), tc.needs)) {
			t.Errorf("%s %s: %v", tc.p.id, tc.needs, err)
		}
		if !tc.allowed {
			for _, c := range *e.calls {
				if strings.Contains(c.path, "/compare/") {
					t.Errorf("%s sent the comparison", tc.p.id)
				}
			}
		}
		// Without the switches the comparison needs no newer version.
		e = newEnvironment(t, tc.p, tc.handler)
		if _, err := e.invoke(t, compareGet, "repo", `{"basehead":"a...b"}`); err != nil {
			t.Errorf("%s: %v", tc.p.id, err)
		}
	}
}

func TestCommitStatusReads(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		byRef, err := e.invoke(t, commitStatusesList, "repo", `{"ref":"release/1.0","state":"success","sort":"oldest"}`)
		if err != nil || len(byRef["statuses"].([]any)) != 1 || byRef["has_more"] != false {
			t.Fatalf("%s: %v, %v", p.id, byRef, err)
		}
		bySha, err := e.invoke(t, commitStatusesList, "repo", `{"sha":"c0ffee1","limit":5}`)
		if err != nil || len(bySha["statuses"].([]any)) != 1 {
			t.Fatalf("%s: %v, %v", p.id, bySha, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{
			"GET /api/v1/repos/acme/widget/commits/release/1.0/statuses", "GET /api/v1/repos/acme/widget/statuses/c0ffee1"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		status := byRef["statuses"].([]any)[0].(map[string]any)
		if status["context"] != "ci/build" || status["target_url"] != "https://ci.example.test/run/1" ||
			status["creator"].(map[string]any)["login"] != "alice" {
			t.Errorf("%s status = %v", p.id, status)
		}
		combined, err := e.invoke(t, commitStatusesGetCombined, "repo", `{"ref":"main"}`)
		if err != nil || combined["state"] != "success" || combined["total_count"] != float64(1) ||
			combined["repository"].(map[string]any)["full_name"] != "acme/widget" || len(combined["statuses"].([]any)) != 1 {
			t.Fatalf("%s: %v, %v", p.id, combined, err)
		}
	}
}

func TestCommitStatusCreateSendsOneRequestAndNeverRetries(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		args := `{"sha":"c0ffee1","state":"pending","context":"ci/build","description":"running",` +
			`"target_url":"https://ci.example.test/run/1"}`
		if _, err := e.invokeAs(t, commitStatusesCreate, "repo", args, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		result, err := e.invoke(t, commitStatusesCreate, "repo", args)
		if err != nil || result["id"] != float64(2) || len(*e.calls) != 1 {
			t.Fatalf("%s: %v, %v, %v", p.id, result, err, methods(e))
		}
		c := (*e.calls)[0]
		if c.method != "POST" || c.path != "/api/v1/repos/acme/widget/statuses/c0ffee1" ||
			!reflect.DeepEqual(bodyOf(t, c), map[string]any{"state": "pending", "context": "ci/build",
				"description": "running", "target_url": "https://ci.example.test/run/1"}) {
			t.Errorf("%s call = %+v", p.id, c)
		}
		failing := newEnvironment(t, p, func(call) *http.Response { return jsonResponse(503, `{"message":"`+bodyCanary+`"}`) })
		_, err = failing.invoke(t, commitStatusesCreate, "repo", args)
		if err == nil || len(*failing.calls) != 1 || !strings.Contains(err.Error(), "may have been applied") {
			t.Errorf("%s: %v, %d calls", p.id, err, len(*failing.calls))
		}
	}
}

func TestCommitToolsStayInsideTheTargets(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commitServer(p, nil))
		if _, err := e.invoke(t, commitsGet, "repo", `{}`); !isInvalidRequest(err) {
			t.Errorf("%s: %v", p.id, err)
		}
		if _, err := e.invoke(t, commitsGet, "two", `{"sha":"c0ffee1"}`); !isInvalidRequest(err) {
			t.Errorf("%s: two repositories need an argument: %v", p.id, err)
		}
		if _, err := e.invoke(t, commitsGet, "two", `{"sha":"c0ffee1","repository":"acme/gadget"}`); isInvalidRequest(err) {
			t.Errorf("%s: %v", p.id, err)
		}
		if _, err := e.invoke(t, commitsGet, "two", `{"sha":"c0ffee1","repository":"acme/other"}`); !isInvalidRequest(err) {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}
