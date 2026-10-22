package forge

import (
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// pullFixture is the answer of one product for a pull request of acme/widget; head names the repository of the
// head branch. Forgejo adds flow, Gitea content_version.
func pullFixture(p *product, number int, base, head string) string {
	n := strconv.Itoa(number)
	fields := `"id":` + strconv.Itoa(2000+number) + `,"number":` + n + `,"title":"Fix crash","body":"Body text",` +
		`"state":"open","draft":false,"is_locked":false,"merged":false,"mergeable":true,"allow_maintainer_edit":true,` +
		`"merge_base":"aaa111","additions":3,"deletions":1,"changed_files":2,"comments":1,"review_comments":0,` +
		`"pin_order":0,"user":{"id":3,"login":"alice"},"assignees":[{"id":4,"login":"bob"}],` +
		`"requested_reviewers":[{"id":5,"login":"carol"}],"requested_reviewers_teams":[{"id":6,"name":"core"}],` +
		`"labels":[{"id":9,"name":"bug"}],"milestone":{"id":2,"title":"v1"},"html_url":"https://forge.example.test/p",` +
		`"diff_url":"https://forge.example.test/p.diff","patch_url":"https://forge.example.test/p.patch",` +
		`"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z",` +
		`"base":{"label":"main","ref":"main","sha":"b1","repo_id":5,"repo":{"id":5,"name":"widget","full_name":"` + base +
		`","owner":{"id":1,"login":"acme"},"private":true,"description":"secret words"}},` +
		`"head":{"label":"fork:feature","ref":"feature","sha":"c2","repo_id":8,"repo":{"id":8,"name":"widget","full_name":"` +
		head + `","owner":{"id":2,"login":"fork"},"private":true,"description":"secret words"}}`
	if p == forgejo {
		fields += `,"flow":1`
	} else {
		fields += `,"content_version":4`
	}
	return `{` + fields + `}`
}

func readerBody(text string) io.ReadCloser { return io.NopCloser(strings.NewReader(text)) }

func widgetPull(p *product, number int) string {
	return pullFixture(p, number, "acme/widget", "fork/widget")
}

// pullServer answers pull request 7 of acme/widget and pull request 9 whose base lies elsewhere.
func pullServer(p *product, other func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if c.method == http.MethodGet {
			switch c.path {
			case "/api/v1/repos/acme/widget/pulls/7":
				return jsonResponse(200, widgetPull(p, 7))
			case "/api/v1/repos/acme/widget/pulls/9":
				return jsonResponse(200, pullFixture(p, 9, "other/place", "fork/widget"))
			}
		}
		if other != nil {
			return other(c)
		}
		return notFoundResponse()
	}
}

func TestPullRequestGetShowsProductMembersAndIdentifiersOfRepositories(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, pullServer(p, nil))
		result, err := e.invoke(t, pullRequestsGet, "repo", `{"number":7}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if result["title"] != "Fix crash" || result["number"] != float64(7) || result["requested_reviewers"] == nil ||
			result["requested_reviewers_teams"] == nil || result["mergeable"] != true || result["merge_base"] != "aaa111" {
			t.Errorf("%s result = %v", p.id, result)
		}
		if _, ok := result["flow"]; ok != (p == forgejo) {
			t.Errorf("%s: flow present = %v", p.id, ok)
		}
		if _, ok := result["content_version"]; ok != (p == gitea) {
			t.Errorf("%s: content_version present = %v", p.id, ok)
		}
		head := result["head"].(map[string]any)["repo"].(map[string]any)
		if head["full_name"] != "fork/widget" || head["private"] != nil || head["description"] != nil {
			t.Errorf("%s: head repository = %v, want identifiers only", p.id, head)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/pulls/7"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestPullRequestToolsRefuseTargetsBeforeTheTokenAndForeignBasesAfterTheEvidenceRead(t *testing.T) {
	cases := map[*tool]string{
		pullRequestsList:          `{"repository":"other/place"}`,
		pullRequestsGet:           `{"repository":"other/place","number":7}`,
		pullRequestsGetByBranches: `{"repository":"other/place","base":"main","head":"feature"}`,
		pullRequestsListPinned:    `{"repository":"other/place"}`,
		pullRequestsCheckMerged:   `{"repository":"other/place","number":7}`,
		commitPullRequestsGet:     `{"repository":"other/place","sha":"0123abc"}`,
		pullRequestFilesList:      `{"repository":"other/place","number":7}`,
		pullRequestCommitsList:    `{"repository":"other/place","number":7}`,
		pullRequestDiffsGet:       `{"repository":"other/place","number":7}`,
	}
	for _, p := range products {
		for tl, arguments := range cases {
			e := newEnvironment(t, p, pullServer(p, nil))
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s: err %v, calls %d, reads %d", p.id, tl.action, err, len(*e.calls), *e.reads)
			}
		}
		// The base repository of the answer is the evidence: pull request 9 lies in another repository.
		for tl, arguments := range map[*tool]string{pullRequestsGet: `{"number":9}`, pullRequestsCheckMerged: `{"number":9}`,
			pullRequestFilesList: `{"number":9}`, pullRequestCommitsList: `{"number":9}`, pullRequestDiffsGet: `{"number":9}`} {
			e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
				t.Errorf("%s %s: request %s after the evidence read", p.id, tl.action, c.path)
				return notFoundResponse()
			}))
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || len(*e.calls) != 1 {
				t.Errorf("%s %s: err %v, calls %d", p.id, tl.action, err, len(*e.calls))
			}
		}
	}
}

func TestPullRequestListFiltersPerProductAndDropsForeignRows(t *testing.T) {
	list := `[` + widgetPull(gitea, 7) + `,` + pullFixture(gitea, 9, "other/place", "fork/widget") + `]`
	for _, p := range products {
		e := newEnvironment(t, p, forgejoOrGitea(p, "17.0.0", func(c call) *http.Response {
			return jsonResponse(200, list, "Link", `<https://forge.example.test/next>; rel="next"`)
		}))
		branch := `"base_branch":"main"`
		if p == forgejo {
			branch = `"base":"main","head":"feature"`
		}
		result, err := e.invoke(t, pullRequestsList, "repo", `{"state":"closed","sort":"oldest","milestone":3,`+
			`"labels":[1,2],"poster":"alice",`+branch+`,"limit":10}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		rows := result["pull_requests"].([]any)
		if len(rows) != 1 || result["has_more"] != true || result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Errorf("%s result = %v", p.id, result)
		}
		var got url.Values
		for _, c := range *e.calls {
			if c.path == "/api/v1/repos/acme/widget/pulls" {
				got, _ = url.ParseQuery(c.query)
			}
		}
		want := url.Values{"state": {"closed"}, "sort": {"oldest"}, "milestone": {"3"}, "labels": {"1", "2"},
			"poster": {"alice"}, "page": {"1"}, "limit": {"10"}}
		if p == forgejo {
			want["base"], want["head"] = []string{"main"}, []string{"feature"}
		} else {
			want["base_branch"] = []string{"main"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s query = %v, want %v", p.id, got, want)
		}
	}
}

// forgejoOrGitea answers the version of the product's instance.
func forgejoOrGitea(p *product, v string, api func(call) *http.Response) func(call) *http.Response {
	if p == forgejo {
		return forgejoAt(v, api)
	}
	return giteaAt(v, api)
}

func TestPullRequestListRefusesTheOtherProductsFilterBeforeTheToken(t *testing.T) {
	for p, arguments := range map[*product]string{gitea: `{"base":"main"}`, forgejo: `{"base_branch":"main"}`} {
		e := newEnvironment(t, p, forgejoOrGitea(p, "17.0.0", nil))
		_, err := e.invoke(t, pullRequestsList, "repo", arguments)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: err %v, calls %d, reads %d", p.id, err, len(*e.calls), *e.reads)
		}
	}
	e := newEnvironment(t, gitea, nil)
	if _, err := e.invoke(t, pullRequestsList, "repo", `{"head":"x"}`); !isInvalidRequest(err) {
		t.Errorf("head on Gitea: %v", err)
	}
}

func TestForgejoBranchFiltersNeedForgejo17(t *testing.T) {
	e := newEnvironment(t, forgejo, forgejoAt("16.0.6", func(c call) *http.Response {
		t.Errorf("request %s on an instance without the filter", c.path)
		return notFoundResponse()
	}))
	if _, err := e.invoke(t, pullRequestsList, "repo", `{"base":"main"}`); err == nil || !strings.Contains(err.Error(), "17.0.0") {
		t.Fatalf("err = %v, want the minimum version", err)
	}
	// The list without the filter needs no newer version.
	e = newEnvironment(t, forgejo, forgejoAt("15.0.10", func(c call) *http.Response { return jsonResponse(200, `[]`) }))
	if _, err := e.invoke(t, pullRequestsList, "repo", `{}`); err != nil {
		t.Fatalf("plain list on Forgejo 15: %v", err)
	}
}

func TestPullRequestListContinuesWithItsCursor(t *testing.T) {
	e := newEnvironment(t, gitea, func(c call) *http.Response {
		return jsonResponse(200, `[`+widgetPull(gitea, 7)+`]`, "Link", `<x>; rel="next"`)
	})
	first, err := e.invoke(t, pullRequestsList, "repo", `{"limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	cursor := first["next_cursor"].(string)
	if _, err := e.invoke(t, pullRequestsList, "repo", `{"state":"all","cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters was accepted: %v", err)
	}
	if _, err := e.invoke(t, pullRequestsList, "repo", `{"cursor":"`+cursor+`"}`); err != nil {
		t.Fatal(err)
	}
	last := (*e.calls)[len(*e.calls)-1]
	if q, _ := url.ParseQuery(last.query); q.Get("page") != "2" || q.Get("limit") != "5" {
		t.Errorf("continuation query = %s", last.query)
	}
}

func TestPullRequestGetByBranchesEscapesAndValidatesBranches(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
			if c.path == "/api/v1/repos/acme/widget/pulls/main/fork:feature/x" {
				return jsonResponse(200, widgetPull(p, 7))
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, pullRequestsGetByBranches, "repo", `{"base":"main","head":"fork:feature/x"}`)
		if err != nil || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		for _, bad := range []string{`".."`, `"a..b"`, `"a\\b"`, `"a\nb"`, `""`, `"` + strings.Repeat("a", 256) + `"`} {
			e := newEnvironment(t, p, pullServer(p, nil))
			if _, err := e.invoke(t, pullRequestsGetByBranches, "repo", `{"base":"main","head":`+bad+`}`); err == nil ||
				len(*e.calls) != 0 {
				t.Errorf("%s: head %s accepted (calls %d)", p.id, bad, len(*e.calls))
			}
		}
	}
}

func TestPullRequestCheckMergedReadsTheStatus(t *testing.T) {
	for _, p := range products {
		for status, want := range map[int]bool{204: true, 404: false} {
			e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
				if c.path == "/api/v1/repos/acme/widget/pulls/7/merge" {
					return &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}
				}
				return notFoundResponse()
			}))
			result, err := e.invoke(t, pullRequestsCheckMerged, "repo", `{"number":7}`)
			if err != nil || result["merged"] != want || result["number"] != float64(7) {
				t.Errorf("%s status %d: %v, %v", p.id, status, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/pulls/7",
				"GET /api/v1/repos/acme/widget/pulls/7/merge"}) {
				t.Errorf("%s calls = %v", p.id, got)
			}
		}
		// A pull request that does not exist is not "not merged".
		e := newEnvironment(t, p, pullServer(p, nil))
		if _, err := e.invoke(t, pullRequestsCheckMerged, "repo", `{"number":8}`); err == nil {
			t.Errorf("%s: a missing pull request was reported", p.id)
		}
	}
}

func TestCommitPullRequestGetValidatesTheCommitID(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
			if c.path == "/api/v1/repos/acme/widget/commits/0123abcDEF/pull" {
				return jsonResponse(200, widgetPull(p, 7))
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, commitPullRequestsGet, "repo", `{"sha":"0123abcDEF"}`)
		if err != nil || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		for _, bad := range []string{"abc", "../x", "0123abc/pulls", "xyz1234", strings.Repeat("a", 65)} {
			e := newEnvironment(t, p, pullServer(p, nil))
			if _, err := e.invoke(t, commitPullRequestsGet, "repo", `{"sha":"`+bad+`"}`); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s: sha %q accepted", p.id, bad)
			}
		}
	}
}

func TestPullRequestFilesAndCommitsPageBelowTheEvidenceRead(t *testing.T) {
	files := `[{"filename":"a.go","previous_filename":"b.go","status":"renamed","additions":1,"deletions":2,` +
		`"changes":3,"html_url":"h","contents_url":"c","raw_url":"r","patch":"must not appear"}]`
	commits := `[{"sha":"c2","created":"2030-01-01T00:00:00Z","html_url":"h","url":"u","author":{"id":3,"login":"alice"},` +
		`"commit":{"message":"fix","author":{"name":"Alice","email":"a` + "@" + `x.test","date":"d"},` +
		`"verification":{"verified":true,"reason":"valid","payload":"RAW","signature":"SIG","signer":{"username":"alice"}}},` +
		`"parents":[{"sha":"p1"}],"stats":{"additions":1,"deletions":0,"total":1},"files":[{"filename":"a.go","status":"added"}]}]`
	for _, p := range products {
		e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/repos/acme/widget/pulls/7/files":
				return jsonResponse(200, files, "X-Total-Count", "1")
			case "/api/v1/repos/acme/widget/pulls/7/commits":
				return jsonResponse(200, commits, "Link", `<x>; rel="next"`)
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, pullRequestFilesList, "repo", `{"number":7,"skip_to":"a.go","whitespace":"ignore-all"}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		rows := result["files"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["previous_filename"] != "b.go" || rows[0].(map[string]any)["patch"] != nil ||
			result["has_more"] != false {
			t.Errorf("%s files = %v", p.id, result)
		}
		if q, _ := url.ParseQuery((*e.calls)[1].query); q.Get("skip-to") != "a.go" || q.Get("whitespace") != "ignore-all" {
			t.Errorf("%s files query = %s", p.id, (*e.calls)[1].query)
		}
		result, err = e.invoke(t, pullRequestCommitsList, "repo", `{"number":7,"verification":false,"files":true}`)
		if err != nil || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		raw := result["commits"].([]any)[0].(map[string]any)
		verification := raw["commit"].(map[string]any)["verification"].(map[string]any)
		if verification["payload"] != nil || verification["signature"] != nil || verification["verified"] != true ||
			raw["parents"] == nil || raw["stats"] == nil || raw["files"] == nil {
			t.Errorf("%s commit = %v", p.id, raw)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if q, _ := url.ParseQuery(last.query); q.Get("verification") != "false" || q.Get("files") != "true" {
			t.Errorf("%s commits query = %s", p.id, last.query)
		}
		if bad, err := e.invoke(t, pullRequestFilesList, "repo", `{"number":7,"whitespace":"all"}`); err == nil {
			t.Errorf("%s: whitespace accepted: %v", p.id, bad)
		}
	}
}

func TestPullRequestDiffIsCutAtTheByteLimit(t *testing.T) {
	long := strings.Repeat("é", maxDiffBytes) // two bytes each: the cut falls inside a character
	for _, p := range products {
		for _, kind := range []string{"diff", "patch"} {
			body := long
			e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
				if c.path == "/api/v1/repos/acme/widget/pulls/7."+kind {
					return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}
				}
				return notFoundResponse()
			}))
			// A short body is returned whole and not truncated.
			result, err := e.invoke(t, pullRequestDiffsGet, "repo", `{"number":7,"type":"`+kind+`","binary":true}`)
			if err != nil || result["content"] != "" || result["truncated"] != false || result["type"] != kind {
				t.Fatalf("%s %s: %v, %v", p.id, kind, result, err)
			}
			if q, _ := url.ParseQuery((*e.calls)[1].query); q.Get("binary") != "true" {
				t.Errorf("%s: query = %s", p.id, (*e.calls)[1].query)
			}
			e = newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
				if c.path == "/api/v1/repos/acme/widget/pulls/7."+kind {
					return &http.Response{StatusCode: 200, Body: readerBody(body), Header: http.Header{}}
				}
				return notFoundResponse()
			}))
			result, err = e.invoke(t, pullRequestDiffsGet, "repo", `{"number":7,"type":"`+kind+`"}`)
			content, _ := result["content"].(string)
			if err != nil || result["truncated"] != true || len(content) > maxDiffBytes || len(content) < maxDiffBytes-3 ||
				strings.Contains(content, "�") {
				t.Errorf("%s %s: truncated %v, %d bytes, err %v", p.id, kind, result["truncated"], len(content), err)
			}
		}
		e := newEnvironment(t, p, pullServer(p, nil))
		if _, err := e.invoke(t, pullRequestDiffsGet, "repo", `{"number":7,"type":"zip"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s: type zip: %v", p.id, err)
		}
	}
}
