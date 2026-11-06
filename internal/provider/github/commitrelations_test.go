package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const (
	relSHA     = "6dcb09b5b57875f334f61aebed695e2e4193db5e"
	relRepo    = "https://api.github.com/repos/octo-org/example"
	relBlobURL = "https://github.com/octo-org/example/blob/6dcb09b5/file1.txt"
)

// relationConfig adds connections that differ only in their targets: one that also allows the owner of a
// fork, one whose organization target covers the fork owner octo-org, and two that exclude.
func relationConfig(base string) *config.Config {
	cfg := discoveryConfig(base)
	all := config.Effects()
	cfg.Connections["forkowner"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{repoTarget, "users/forkuser"}, Effects: all}
	cfg.Connections["forkexcluded"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Exclude: []string{"users/forkuser"}, Effects: all}
	cfg.Connections["repoexcluded"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/*"}, Exclude: []string{repoTarget}, Effects: all}
	return cfg
}

// guardedCore starts a core whose fake counts credential reads and records every request.
func guardedCore(t *testing.T) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	return f, application.New(registry(t), relationConfig(serve(t, f)), resolver(red, &reads), red), &reads
}

// outputSchemaOf is the output schema of one registered tool, with the target member the registry adds.
func outputSchemaOf(t *testing.T, id string) map[string]any {
	t.Helper()
	for _, d := range registry(t).Provider(Provider) {
		if d.ID == id {
			return decodeAny(t, d.OutputSchema).(map[string]any)
		}
	}
	t.Fatalf("no tool %s", id)
	return nil
}

// coversSchema checks every member of the answer against the output schema. A link_relations map names its
// members by GitHub, which the schema describes as a map rather than as properties, so it is checked apart.
func coversSchema(t *testing.T, id string, got json.RawMessage) {
	t.Helper()
	value := decodeAny(t, got)
	var strip func(any)
	strip = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "link_relations")
			for _, member := range v {
				strip(member)
			}
		case []any:
			for _, item := range v {
				strip(item)
			}
		}
	}
	strip(value)
	checkSchemaCovers(t, "$", value, outputSchemaOf(t, id))
}

// The comparison documented by GitHub's REST reference for "Compare two commits", with the commits and the
// diff entry shortened to the members the projection reads.
const (
	compareFile = `{"sha":"bbcd538c8e72b8c175046e27cc8f907076331401","filename":"file1.txt","status":"added",` +
		`"additions":103,"deletions":21,"changes":124,"blob_url":"` + relBlobURL + `",` +
		`"raw_url":"https://github.com/octo-org/example/raw/6dcb09b5/file1.txt",` +
		`"contents_url":"` + relRepo + `/contents/file1.txt?ref=6dcb09b5","patch":"@@ -132,7 +132,7 @@"}`
	documentedCompare = `{"url":"` + relRepo + `/compare/master...topic",` +
		`"html_url":"https://github.com/octo-org/example/compare/master...topic",` +
		`"permalink_url":"https://github.com/octo-org/example/compare/octo-org:bbcd538...octo-org:0328041",` +
		`"diff_url":"https://github.com/octo-org/example/compare/master...topic.diff",` +
		`"patch_url":"https://github.com/octo-org/example/compare/master...topic.patch",` +
		`"base_commit":` + documentedCommitList + `,"merge_base_commit":` + documentedCommitList + `,` +
		`"status":"behind","ahead_by":1,"behind_by":2,"total_commits":1,"commits":[` + documentedCommitList + `],` +
		`"files":[` + compareFile + `]}`
)

func TestCompareReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedCompare, false, relationConfig)
	got, err := invoke(t, core, compareGet.ID, "repo", `{"base":"master","head":"topic"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, compareGet.ID, got)
	request := lastRequest(t, f)
	query, _ := url.ParseQuery(request.query)
	if request.method != http.MethodGet || request.path != "/api/v3/repos/octo-org/example/compare/master...topic" ||
		query.Get("per_page") != "30" || query.Get("page") != "1" {
		t.Errorf("request = %+v, want the fixed compare route with GitHub's paging", request)
	}
	object := members(t, got)
	wantMembers(t, "comparison", object, map[string]string{
		"url": `"https://github.com/octo-org/example/compare/master...topic"`,
		"links": `{"url":"` + relRepo + `/compare/master...topic",` +
			`"permalink_url":"https://github.com/octo-org/example/compare/octo-org:bbcd538...octo-org:0328041",` +
			`"diff_url":"https://github.com/octo-org/example/compare/master...topic.diff",` +
			`"patch_url":"https://github.com/octo-org/example/compare/master...topic.patch"}`,
		"status": `"behind"`, "ahead_by": `1`, "behind_by": `2`, "total_commits": `1`, "files_truncated": `false`,
		"has_more": `false`, "repository": `"octo-org/example"`,
	})
	for _, name := range []string{"base_commit", "merge_base_commit"} {
		wantMembers(t, name, members(t, got, name), map[string]string{"sha": `"6dcb09b5"`, "message": `"Fix all the bugs"`})
	}
	commit := members(t, got, "commits", "#0")
	wantMembers(t, "commit", commit, commitWidthWant())
	wantMembers(t, "commit", commit, map[string]string{"sha": `"6dcb09b5"`, "parents": `1`, "message": `"Fix all the bugs"`})
	for _, absent := range []string{"stats", "files"} {
		if _, ok := commit[absent]; ok {
			t.Errorf("a compared commit carries %s, which the files member holds", absent)
		}
	}
	wantMembers(t, "file", members(t, got, "files", "#0"), map[string]string{
		"path": `"file1.txt"`, "status": `"added"`, "additions": `103`, "deletions": `21`, "changes": `124`,
		"sha": `"bbcd538c8e72b8c175046e27cc8f907076331401"`, "patch": `"@@ -132,7 +132,7 @@"`,
		"links": `{"blob_url":"` + relBlobURL + `","raw_url":"https://github.com/octo-org/example/raw/6dcb09b5/file1.txt",` +
			`"contents_url":"` + relRepo + `/contents/file1.txt?ref=6dcb09b5"}`,
	})
}

// Commits and files are paged by GitHub's page and per_page: a cursor moves both, it stays bound to base and
// head, and a comparison with more commits than the batch holds continues even without a Link header.
func TestComparePagesCommitsAndFilesTogether(t *testing.T) {
	f, core := cannedCore(t, strings.Replace(documentedCompare, `"total_commits":1`, `"total_commits":5`, 1), false,
		relationConfig)
	first, err := invoke(t, core, compareGet.ID, "repo", `{"base":"master","head":"topic","limit":2}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first batch = %s, want more from total_commits", first)
	}
	query, _ := url.ParseQuery(lastRequest(t, f).query)
	if query.Get("per_page") != "2" {
		t.Errorf("per_page = %q, want the limit", query.Get("per_page"))
	}
	if _, err := invoke(t, core, compareGet.ID, "repo",
		`{"base":"master","head":"topic","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery(lastRequest(t, f).query)
	if query.Get("page") != "2" || query.Get("per_page") != "2" {
		t.Errorf("continuation query = %v, want page 2 at the batch size of the first batch", query)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, compareGet.ID, "repo",
		`{"base":"master","head":"other","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of another comparison = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused cursor reached GitHub")
	}

	// The Link header alone continues it as well, for a comparison whose files go on.
	_, core = cannedCore(t, documentedCompare, true, relationConfig)
	got, err := invoke(t, core, compareGet.ID, "repo", `{"base":"master","head":"topic"}`, false)
	if err != nil || json.Unmarshal(got, &page) != nil || !page.HasMore {
		t.Errorf("answer with a next link = %s, %v, want has_more", got, err)
	}
}

// A patch is cut to maxPatchBytes with the cut visible, a file without a patch (binary) has none, and a batch
// that reaches the files limit of GitHub's route is reported as cut.
func TestCompareBoundsPatchesAndFiles(t *testing.T) {
	big := strings.Repeat("+x", 3000)
	files := []string{
		`{"filename":"a.go","status":"modified","additions":1,"deletions":0,"changes":1,"patch":"` + big + `"}`,
		`{"filename":"logo.png","status":"added","additions":0,"deletions":0,"changes":0}`,
	}
	_, core := cannedCore(t, `{"status":"ahead","ahead_by":1,"behind_by":0,"total_commits":1,"commits":[],"files":[`+
		strings.Join(files, ",")+`]}`, false, relationConfig)
	got, err := invoke(t, core, compareGet.ID, "repo", `{"base":"a","head":"b"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var result Comparison
	if json.Unmarshal(got, &result) != nil || len(result.Files) != 2 || len(result.Files[0].Patch) != maxPatchBytes ||
		!result.Files[0].PatchTruncated || result.Files[1].Patch != "" || result.Files[1].PatchTruncated ||
		result.FilesTruncated {
		t.Fatalf("comparison = %+v, want a cut patch, none for the binary file, and no cut file list", result)
	}

	many := make([]string, maxCommitFiles+5)
	for i := range many {
		many[i] = fmt.Sprintf(`{"filename":"f%d.go","status":"modified","additions":1,"deletions":0,"changes":1}`, i)
	}
	_, core = cannedCore(t, `{"status":"ahead","ahead_by":1,"behind_by":0,"total_commits":1,"commits":[],"files":[`+
		strings.Join(many, ",")+`]}`, false, relationConfig)
	got, err = invoke(t, core, compareGet.ID, "repo", `{"base":"a","head":"b"}`, false)
	var cut Comparison
	if err != nil || json.Unmarshal(got, &cut) != nil || !cut.FilesTruncated || len(cut.Files) != maxCommitFiles {
		t.Fatalf("comparison with many files = %s, %v, want %d files with files_truncated", got, err, maxCommitFiles)
	}
}

// base and head are checked like the ref tools, a fork's ref only for an owner the targets allow, and every
// refusal ends before a credential is read and before GitHub is contacted, without quoting the owner.
func TestCompareRefusesUnusableRefsAndForeignForksBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	for _, tt := range []struct{ name, connection, arguments string }{
		{"bad base", "repo", `{"base":"bad..ref","head":"main"}`},
		{"bad head", "repo", `{"base":"main","head":"bad..ref"}`},
		{"empty head", "repo", `{"base":"main","head":""}`},
		{"missing head", "repo", `{"base":"main"}`},
		{"bad fork ref", "forkowner", `{"base":"main","head":"forkuser:bad..ref"}`},
		{"bad fork owner", "forkowner", `{"base":"main","head":"fork user:topic"}`},
		{"two colons", "forkowner", `{"base":"main","head":"forkuser:a:b"}`},
		{"fork outside the targets", "repo", `{"base":"main","head":"forkuser:topic"}`},
		{"fork as base outside the targets", "repo", `{"base":"forkuser:topic","head":"main"}`},
		{"other owner than the allowed one", "forkowner", `{"base":"main","head":"stranger:topic"}`},
		{"excluded fork owner", "forkexcluded", `{"base":"main","head":"forkuser:topic","repository":"octo-org/example"}`},
		{"foreign repository", "repo", `{"base":"main","head":"topic","repository":"other/example"}`},
		{"excluded repository", "repoexcluded", `{"base":"main","head":"topic","repository":"octo-org/example"}`},
		{"foreign cursor", "repo", `{"base":"main","head":"topic","cursor":"AAAA"}`},
	} {
		*reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, compareGet.ID, tt.connection, tt.arguments, false)
		if !isInvalidRequest(err) {
			t.Errorf("%s: %v, want an invalid request", tt.name, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
		if err != nil && strings.Contains(err.Error(), "forkuser") || err != nil && strings.Contains(err.Error(), "stranger") {
			t.Errorf("%s: the refusal quotes the owner: %v", tt.name, err)
		}
	}
}

// A fork owner the targets allow (an owner target next to the repository, the repository's own owner, or no
// targets at all) is sent as OWNER:ref in the one fixed route.
func TestCompareAcceptsAForkOfAnAllowedOwner(t *testing.T) {
	for _, tt := range []struct{ connection, head string }{
		{"forkowner", "forkuser:topic"}, {"repo", "octo-org:topic"}, {"open", "anyone:topic"},
	} {
		f, core := cannedCore(t, documentedCompare, false, relationConfig)
		arguments := `{"base":"main","head":"` + tt.head + `","repository":"octo-org/example"}`
		if _, err := invoke(t, core, compareGet.ID, tt.connection, arguments, false); err != nil {
			t.Errorf("%s with head %s = %v", tt.connection, tt.head, err)
			continue
		}
		if got := lastRequest(t, f).path; got != "/api/v3/repos/octo-org/example/compare/main..."+tt.head {
			t.Errorf("%s path = %q, want base...OWNER:ref", tt.connection, got)
		}
	}
}

// A slash of a branch name never becomes a path segment, and a ref GitHub does not hold is not found, naming
// the comparison.
func TestCompareEscapesRefsAndReportsMissingOnes(t *testing.T) {
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
	_, err := invoke(t, core, compareGet.ID, "repo", `{"base":"main","head":"feature/x"}`, false)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "comparison main...feature/x") {
		t.Errorf("compare with a missing ref = %v, want not-found naming the comparison", err)
	}
	if got := lastRequest(t, f).path; got != "/api/v3/repos/octo-org/example/compare/main...feature/x" {
		t.Errorf("path = %q", got)
	}
}

// ---- github.branches.get

func TestBranchGetReportsEveryDocumentedMember(t *testing.T) {
	body := `{"name":"main","commit":{` + documentedCommitHead + `,"stats":{"additions":104,"deletions":4,"total":108}},` +
		`"_links":{"html":"https://github.com/octo-org/example/tree/main","self":"` + relRepo + `/branches/main"},` +
		`"protected":true,"protection":` + documentedProtection + `,"protection_url":"` + refBase +
		`/branches/main/protection","pattern":"release/*","required_approving_review_count":2}`
	f, core := cannedCore(t, body, false, relationConfig)
	got, err := invoke(t, core, branchesGet.ID, "repo", `{"branch":"main"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, branchesGet.ID, got)
	if path := lastRequest(t, f).path; path != "/api/v3/repos/octo-org/example/branches/main" {
		t.Errorf("path = %q", path)
	}
	wantMembers(t, "branch", members(t, got), map[string]string{
		"name": `"main"`, "protected": `true`, "sha": `"6dcb09b5"`, "pattern": `"release/*"`,
		"required_approving_review_count": `2`,
		"link_relations": `{"html":"https://github.com/octo-org/example/tree/main","self":"` + relRepo +
			`/branches/main"}`,
		"links": `{"protection_url":"` + refBase + `/branches/main/protection"}`,
	})
	wantMembers(t, "commit", members(t, got, "commit"), commitWidthWant())
	wantMembers(t, "commit", members(t, got, "commit"), map[string]string{"stats": `{"additions":104,"deletions":4,"total":108}`})
	wantMembers(t, "protection", members(t, got, "protection"), map[string]string{"name": `"main"`, "enabled": `true`})
}

func TestBranchGetEscapesTheNameAndReportsAMissingOne(t *testing.T) {
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Branch not found"}`)
		return true
	}}
	red := &redact.Redactor{}
	reads := 0
	core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, &reads), red)
	_, err := invoke(t, core, branchesGet.ID, "repo", `{"branch":"feature/x"}`, false)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "branch feature/x") {
		t.Errorf("get on a missing branch = %v, want not-found naming the branch", err)
	}
	if got := lastRequest(t, f).path; got != "/api/v3/repos/octo-org/example/branches/feature/x" {
		t.Errorf("path = %q", got)
	}
	for _, arguments := range []string{`{}`, `{"branch":"bad..name"}`, `{"branch":""}`} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, branchesGet.ID, "repo", arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
}

// ---- github.commit-branches.list

func TestCommitBranchesAreListedWithTheirProtection(t *testing.T) {
	body := `[{"name":"main","commit":{"sha":"c5b97d5a","url":"` + refBase + `/commits/c5b97d5a"},"protected":true},` +
		`{"name":"topic","commit":{"sha":"c5b97d5a","url":"` + refBase + `/commits/c5b97d5a"},"protected":false}]`
	f, core := cannedCore(t, body, false, relationConfig)
	got, err := invoke(t, core, commitBranchesList.ID, "repo", `{"commit_sha":"`+relSHA+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, commitBranchesList.ID, got)
	if path := lastRequest(t, f).path; path != "/api/v3/repos/octo-org/example/commits/"+relSHA+"/branches-where-head" {
		t.Errorf("path = %q", path)
	}
	wantMembers(t, "branch", members(t, got, "branches", "#0"), map[string]string{
		"name": `"main"`, "protected": `true`, "sha": `"c5b97d5a"`,
		"commit": `{"sha":"c5b97d5a","links":{"url":"` + refBase + `/commits/c5b97d5a"}}`,
	})

	// More branches than the cap are cut with the cut visible; none at all is an empty list.
	entries := make([]string, projectionListLimit+3)
	for i := range entries {
		entries[i] = fmt.Sprintf(`{"name":"b%d","commit":{"sha":"c5b97d5a"},"protected":false}`, i)
	}
	_, core = cannedCore(t, "["+strings.Join(entries, ",")+"]", false, relationConfig)
	got, err = invoke(t, core, commitBranchesList.ID, "repo", `{"commit_sha":"`+relSHA+`"}`, false)
	var cut CommitBranchList
	if err != nil || json.Unmarshal(got, &cut) != nil || len(cut.Branches) != projectionListLimit || !cut.BranchesTruncated {
		t.Errorf("many branches = %s, %v, want %d with the cut visible", got, err, projectionListLimit)
	}
	_, core = cannedCore(t, `[]`, false, relationConfig)
	got, err = invoke(t, core, commitBranchesList.ID, "repo", `{"commit_sha":"`+relSHA+`"}`, false)
	if err != nil || strings.Contains(string(got), `"name"`) {
		t.Errorf("no branches = %s, %v, want an empty list", got, err)
	}
}

func TestCommitBranchesReportAMissingCommit(t *testing.T) {
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
	_, err := invoke(t, core, commitBranchesList.ID, "repo", `{"commit_sha":"`+relSHA+`"}`, false)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "the branches headed by commit "+relSHA) {
		t.Errorf("missing commit = %v, want not-found naming the commit", err)
	}
}

// ---- github.commit-pulls.list

func TestCommitPullsAreListedAndForeignOnesShowIdentifiersOnly(t *testing.T) {
	foreign := strings.ReplaceAll(strings.Replace(documentedPull, `"number":42`, `"number":43`, 1),
		"octo-org/example", "other/example")
	f, core := cannedCore(t, `[`+documentedPull+`,`+foreign+`]`, true, relationConfig)
	got, err := invoke(t, core, commitPullsList.ID, "repo", `{"commit_sha":"`+relSHA+`","limit":10}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, commitPullsList.ID, got)
	request := lastRequest(t, f)
	query, _ := url.ParseQuery(request.query)
	if request.path != "/api/v3/repos/octo-org/example/commits/"+relSHA+"/pulls" || query.Get("per_page") != "10" ||
		query.Get("page") != "1" {
		t.Errorf("request = %+v", request)
	}
	own := members(t, got, "pull_requests", "#0")
	wantMembers(t, "pull", own, map[string]string{
		"number": `42`, "title": `"Amazing new feature"`, "state": `"open"`, "node_id": `"MDExOlB1bGxSZXF1ZXN0MQ=="`,
		"head": `{"label":"octocat:new-topic","ref":"new-topic","sha":"6dcb09b5","user":` + accountWant +
			`,"repo":` + pullHeadForeign + `}`,
	})
	outside := members(t, got, "pull_requests", "#1")
	if len(outside) != 4 {
		t.Errorf("a pull request of a foreign repository = %v, want its identifiers and its base repository's", outside)
	}
	wantMembers(t, "foreign pull", outside, map[string]string{
		"number": `43`, "id": `1`, "base": `{"repo":{"id":99,"node_id":"MDEwOlJlcG9zaXRvcnk5OQ==","full_name":"other/example"}}`,
	})
	if strings.Count(string(got), "Amazing new feature") != 1 {
		t.Errorf("the foreign pull request shows its title: %s", got)
	}
	var page struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if json.Unmarshal(got, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %s, want a continuation", got)
	}
	if _, err := invoke(t, core, commitPullsList.ID, "repo",
		`{"commit_sha":"`+relSHA+`","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Fatal(err)
	}
	if query, _ = url.ParseQuery(lastRequest(t, f).query); query.Get("page") != "2" || query.Get("per_page") != "10" {
		t.Errorf("continuation query = %v", query)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, commitPullsList.ID, "repo",
		`{"commit_sha":"`+strings.Repeat("a", 40)+`","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of another commit = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused cursor reached GitHub")
	}
}

// ---- github.commit-comments.list and github.commit-comments.get

const documentedCommitComment = `{"html_url":"https://github.com/octo-org/example/commit/6dcb09b5#commitcomment-1",` +
	`"url":"` + relRepo + `/comments/1","id":1,"node_id":"MDEzOkNvbW1pdENvbW1lbnQx","body":"Great stuff",` +
	`"path":"file1.txt","position":4,"line":14,"commit_id":"6dcb09b5b57875f334f61aebed695e2e4193db5e",` +
	`"user":` + documentedAccount + `,"created_at":"2011-04-14T16:00:49Z","updated_at":"2011-04-14T16:00:49Z",` +
	`"author_association":"COLLABORATOR","reactions":{"url":"` + relRepo + `/comments/1/reactions","total_count":5,` +
	`"+1":3,"-1":1,"laugh":0,"confused":0,"heart":1,"hooray":0,"eyes":1,"rocket":0}}`

func commitCommentWant() map[string]string {
	return map[string]string{
		"id": `1`, "node_id": `"MDEzOkNvbW1pdENvbW1lbnQx"`, "body": `"Great stuff"`, "path": `"file1.txt"`,
		"position": `4`, "line": `14`, "commit_id": `"` + relSHA + `"`, "user": accountWant,
		"created_at": `"2011-04-14T16:00:49Z"`, "updated_at": `"2011-04-14T16:00:49Z"`,
		"author_association": `"COLLABORATOR"`,
		"reactions": `{"total_count":5,"plus_one":3,"minus_one":1,"laugh":0,"confused":0,"heart":1,"hooray":0,` +
			`"eyes":1,"rocket":0,"links":{"url":"` + relRepo + `/comments/1/reactions"}}`,
		"url":   `"https://github.com/octo-org/example/commit/6dcb09b5#commitcomment-1"`,
		"links": `{"url":"` + relRepo + `/comments/1"}`,
	}
}

func TestCommitCommentsAreListedForTheRepositoryAndForOneCommit(t *testing.T) {
	long := strings.Repeat("c", projectionBodyLimit+10)
	long1 := strings.Replace(documentedCommitComment, `"id":1,`, `"id":2,`, 1)
	long1 = strings.Replace(long1, "Great stuff", long, 1)
	f, core := cannedCore(t, `[`+documentedCommitComment+`,`+long1+`]`, true, relationConfig)
	got, err := invoke(t, core, commitCommentsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, commitCommentsList.ID, got)
	request := lastRequest(t, f)
	if query, _ := url.ParseQuery(request.query); request.path != "/api/v3/repos/octo-org/example/comments" ||
		query.Get("per_page") != "30" || query.Get("page") != "1" {
		t.Errorf("repository request = %+v", request)
	}
	wantMembers(t, "comment", members(t, got, "comments", "#0"), commitCommentWant())
	second := members(t, got, "comments", "#1")
	wantMembers(t, "long comment", second, map[string]string{
		"body": `"` + long[:projectionBodyLimit] + `"`, "body_truncated": `true`,
	})
	var page struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if json.Unmarshal(got, &page) != nil || !page.HasMore {
		t.Fatalf("page = %s, want a continuation", got)
	}
	if _, err := invoke(t, core, commitCommentsList.ID, "repo", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Fatal(err)
	}
	if query, _ := url.ParseQuery(lastRequest(t, f).query); query.Get("page") != "2" {
		t.Errorf("continuation query = %v", query)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, commitCommentsList.ID, "repo",
		`{"commit_sha":"`+relSHA+`","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a repository cursor on a commit list = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused cursor reached GitHub")
	}

	if _, err := invoke(t, core, commitCommentsList.ID, "repo", `{"commit_sha":"`+relSHA+`"}`, false); err != nil {
		t.Fatal(err)
	}
	if path := lastRequest(t, f).path; path != "/api/v3/repos/octo-org/example/commits/"+relSHA+"/comments" {
		t.Errorf("commit request path = %q", path)
	}
}

func TestCommitCommentGetReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedCommitComment, false, relationConfig)
	got, err := invoke(t, core, commitCommentsGet.ID, "repo", `{"comment_id":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, commitCommentsGet.ID, got)
	if path := lastRequest(t, f).path; path != "/api/v3/repos/octo-org/example/comments/1" {
		t.Errorf("path = %q", path)
	}
	wantMembers(t, "comment", members(t, got), commitCommentWant())

	// GitHub answering another comment than the requested one is no answer.
	if _, err := invoke(t, core, commitCommentsGet.ID, "repo", `{"comment_id":2}`, false); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("another comment = %v, want an invalid response", err)
	}
}

func TestCommitCommentGetReportsAMissingComment(t *testing.T) {
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
	_, err := invoke(t, core, commitCommentsGet.ID, "repo", `{"comment_id":7}`, false)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "commit comment 7") {
		t.Errorf("missing comment = %v, want not-found naming the comment", err)
	}
}

// ---- shared refusals

// Every relation tool settles its target and its arguments before a credential is read: a repository outside
// the targets, an excluded one, and a value no route accepts never reach GitHub.
func TestCommitRelationToolsRefuseBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	arguments := map[string]string{
		branchesGet.ID:        `"branch":"main"`,
		commitBranchesList.ID: `"commit_sha":"` + relSHA + `"`,
		commitPullsList.ID:    `"commit_sha":"` + relSHA + `"`,
		commitCommentsList.ID: `"commit_sha":"` + relSHA + `"`,
		commitCommentsGet.ID:  `"comment_id":1`,
	}
	for id, own := range arguments {
		for _, tt := range []struct{ name, connection, arguments string }{
			{"foreign repository", "repo", `{` + own + `,"repository":"other/example"}`},
			{"excluded repository", "repoexcluded", `{` + own + `,"repository":"octo-org/example"}`},
			{"project target", "planning", `{` + own + `}`},
		} {
			*reads = 0
			before := len(f.recorded())
			if _, err := invoke(t, core, id, tt.connection, tt.arguments, false); !isInvalidRequest(err) {
				t.Errorf("%s, %s = %v, want an invalid request", id, tt.name, err)
			}
			if *reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s, %s reached the credential or GitHub", id, tt.name)
			}
		}
	}
	for _, tt := range []struct{ id, arguments string }{
		{commitBranchesList.ID, `{"commit_sha":"main"}`},
		{commitBranchesList.ID, `{"commit_sha":"../x"}`},
		{commitBranchesList.ID, `{}`},
		{commitPullsList.ID, `{"commit_sha":"ABCD1234"}`},
		{commitPullsList.ID, `{}`},
		{commitCommentsList.ID, `{"commit_sha":"zzzz"}`},
		{commitCommentsGet.ID, `{"comment_id":0}`},
		{commitCommentsGet.ID, `{"comment_id":-3}`},
		{commitCommentsGet.ID, `{}`},
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.id, tt.arguments, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s %s reached the credential or GitHub", tt.id, tt.arguments)
		}
	}
}

func TestCommitRelationSubjectsNameOnlyUsableValues(t *testing.T) {
	for path, want := range map[string]string{
		"compare/main...topic":                       "comparison main...topic",
		"compare/main...forkuser:topic":              "comparison main...forkuser:topic",
		"compare/main...feature%2Fx":                 "comparison main...feature/x",
		"compare/main...bad..ref":                    "",
		"branches/main":                              "branch main",
		"branches/feature%2Fx":                       "branch feature/x",
		"branches/main/protection":                   "",
		"comments":                                   "the commit comments of the repository",
		"comments/12":                                "commit comment 12",
		"comments/x":                                 "",
		"commits/" + relSHA + "/pulls":               "the pull requests of commit " + relSHA,
		"commits/" + relSHA + "/comments":            "the comments of commit " + relSHA,
		"commits/" + relSHA + "/branches-where-head": "the branches headed by commit " + relSHA,
		"commits/" + relSHA + "/unknown":             "",
		"commits/main/pulls":                         "",
		"commits/" + relSHA:                          "",
	} {
		if got := commitRelationSubject(path); got != want {
			t.Errorf("commitRelationSubject(%q) = %q, want %q", path, got, want)
		}
	}
}
