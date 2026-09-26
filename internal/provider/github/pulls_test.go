package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakePull is one pull request of the fake repository.
type fakePull struct {
	number                          int
	title, state                    string
	draft, merged                   bool
	headBranch, headSHA, baseBranch string
	labels                          []string
	body                            string
}

type fakeFile struct {
	filename, status, previous    string
	additions, deletions, changes int
	patch                         string
}

type fakeCommit struct {
	sha, authorLogin, authorName, date, message string
}

type fakeCheckRun struct {
	name, status, conclusion, startedAt, completedAt string
}

type fakeStatus struct {
	context, state, updatedAt string
}

// fakePulls answers the pull request routes of the bound repository through the failure hook of fakeGitHub.
// Every other repository is unknown to it.
type fakePulls struct {
	pulls         []fakePull
	files         map[int][]fakeFile
	commits       map[int][]fakeCommit
	diffs         map[int]string
	runs          map[string][]fakeCheckRun
	runsTotal     map[string]int
	statuses      map[string][]fakeStatus
	statusesTotal map[string]int
}

func servePulls(t *testing.T) (*fakeGitHub, *fakePulls, string) {
	t.Helper()
	p := &fakePulls{}
	f := &fakeGitHub{failure: p.route}
	return f, p, serve(t, f)
}

func (p *fakePulls) route(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok || !(strings.HasPrefix(rest, "pulls") || strings.HasPrefix(rest, "commits/")) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && rest == "pulls":
		p.listPulls(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/") && strings.HasSuffix(rest, "/files"):
		p.listFiles(w, r, rest)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/") && strings.HasSuffix(rest, "/commits"):
		p.listCommits(w, r, rest)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/") && r.Header.Get("Accept") == "application/vnd.github.diff":
		p.getDiff(w, rest)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/"):
		p.getPull(w, rest)
	case r.Method == http.MethodGet && strings.HasSuffix(rest, "/check-runs"):
		p.getCheckRuns(w, rest)
	case r.Method == http.MethodGet && strings.HasSuffix(rest, "/status"):
		p.getStatus(w, rest)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

func pullNumber(rest string) (int, bool) {
	tail := strings.TrimPrefix(rest, "pulls/")
	digits, _, _ := strings.Cut(tail, "/")
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

func (p *fakePulls) pullExists(number int) bool {
	for _, pull := range p.pulls {
		if pull.number == number {
			return true
		}
	}
	return false
}

func labelsJSON(labels []string) string {
	parts := make([]string, len(labels))
	for i, label := range labels {
		parts[i] = fmt.Sprintf(`{"name":%q}`, label)
	}
	return strings.Join(parts, ",")
}

func pullJSONOf(pull fakePull) string {
	mergedAt, closedAt := "null", "null"
	if pull.merged {
		mergedAt = `"2026-01-05T00:00:00Z"`
	}
	if pull.state == "closed" {
		closedAt = `"2026-01-04T00:00:00Z"`
	}
	return fmt.Sprintf(`{"number":%d,"title":%q,"body":%q,"state":%q,"draft":%v,`+
		`"user":{"login":"octocat"},"head":{"ref":%q,"sha":%q},"base":{"ref":%q,"sha":"basesha"},`+
		`"labels":[%s],"requested_reviewers":[{"login":"hubot"}],"merged":%v,`+
		`"mergeable":true,"mergeable_state":"clean","merge_commit_sha":"mergecommitsha","commits":3,`+
		`"changed_files":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",`+
		`"closed_at":%s,"merged_at":%s,"html_url":"https://github.com/octo-org/example/pull/%d"}`,
		pull.number, pull.title, pull.body, pull.state, pull.draft, pull.headBranch, pull.headSHA, pull.baseBranch,
		labelsJSON(pull.labels), pull.merged, closedAt, mergedAt, pull.number)
}

func (p *fakePulls) listPulls(w http.ResponseWriter, r *http.Request) {
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(p.pulls))
	end := min(start+perPage, len(p.pulls))
	entries := make([]string, 0, end-start)
	for _, pull := range p.pulls[start:end] {
		entries = append(entries, pullJSONOf(pull))
	}
	if end < len(p.pulls) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (p *fakePulls) getPull(w http.ResponseWriter, rest string) {
	number, ok := pullNumber(rest)
	if ok {
		for _, pull := range p.pulls {
			if pull.number == number {
				fmt.Fprint(w, pullJSONOf(pull))
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func fileJSONOf(file fakeFile) string {
	previous := "null"
	if file.previous != "" {
		encoded, _ := json.Marshal(file.previous)
		previous = string(encoded)
	}
	patch := "null"
	if file.patch != "" {
		encoded, _ := json.Marshal(file.patch)
		patch = string(encoded)
	}
	return fmt.Sprintf(`{"filename":%q,"status":%q,"additions":%d,"deletions":%d,"changes":%d,`+
		`"previous_filename":%s,"patch":%s}`, file.filename, file.status, file.additions, file.deletions,
		file.changes, previous, patch)
}

func (p *fakePulls) listFiles(w http.ResponseWriter, r *http.Request, rest string) {
	number, ok := pullNumber(rest)
	if !ok || !p.pullExists(number) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	files := p.files[number]
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(files))
	end := min(start+perPage, len(files))
	entries := make([]string, 0, end-start)
	for _, file := range files[start:end] {
		entries = append(entries, fileJSONOf(file))
	}
	if end < len(files) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func commitJSONOf(commit fakeCommit) string {
	author := "null"
	if commit.authorLogin != "" {
		author = fmt.Sprintf(`{"login":%q}`, commit.authorLogin)
	}
	return fmt.Sprintf(`{"sha":%q,"commit":{"author":{"name":%q,"date":%q},"message":%q},"author":%s,`+
		`"html_url":"https://github.com/octo-org/example/commit/%s"}`, commit.sha, commit.authorName, commit.date,
		commit.message, author, commit.sha)
}

func (p *fakePulls) listCommits(w http.ResponseWriter, r *http.Request, rest string) {
	number, ok := pullNumber(rest)
	if !ok || !p.pullExists(number) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	commits := p.commits[number]
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(commits))
	end := min(start+perPage, len(commits))
	entries := make([]string, 0, end-start)
	for _, commit := range commits[start:end] {
		entries = append(entries, commitJSONOf(commit))
	}
	if end < len(commits) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (p *fakePulls) getDiff(w http.ResponseWriter, rest string) {
	number, ok := pullNumber(rest)
	diff, found := p.diffs[number]
	if !ok || !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8; format=diff")
	fmt.Fprint(w, diff)
}

func checkRunJSONOf(run fakeCheckRun) string {
	return fmt.Sprintf(`{"name":%q,"status":%q,"conclusion":%q,"started_at":%q,"completed_at":%q,`+
		`"details_url":"https://ci.example/%s"}`, run.name, run.status, run.conclusion, run.startedAt,
		run.completedAt, run.name)
}

func (p *fakePulls) getCheckRuns(w http.ResponseWriter, rest string) {
	sha := strings.TrimSuffix(strings.TrimPrefix(rest, "commits/"), "/check-runs")
	if sha == "facade0000" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	runs := p.runs[sha]
	total := p.runsTotal[sha]
	if total == 0 {
		total = len(runs)
	}
	entries := make([]string, 0, len(runs))
	for _, run := range runs {
		entries = append(entries, checkRunJSONOf(run))
	}
	fmt.Fprintf(w, `{"total_count":%d,"check_runs":[%s]}`, total, strings.Join(entries, ","))
}

func statusJSONOf(status fakeStatus) string {
	return fmt.Sprintf(`{"context":%q,"state":%q,"target_url":"https://ci.example/%s","updated_at":%q}`,
		status.context, status.state, status.context, status.updatedAt)
}

func (p *fakePulls) getStatus(w http.ResponseWriter, rest string) {
	sha := strings.TrimSuffix(strings.TrimPrefix(rest, "commits/"), "/status")
	if sha == "facade0000" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	statuses := p.statuses[sha]
	total := p.statusesTotal[sha]
	if total == 0 {
		total = len(statuses)
	}
	entries := make([]string, 0, len(statuses))
	for _, status := range statuses {
		entries = append(entries, statusJSONOf(status))
	}
	fmt.Fprintf(w, `{"state":"pending","total_count":%d,"statuses":[%s]}`, total, strings.Join(entries, ","))
}

func TestHasNextPageParsesTheLinkHeader(t *testing.T) {
	tests := []struct {
		name, link string
		want       bool
	}{
		{"absent", "", false},
		{"next only", `<https://api.github.com/x?page=2>; rel="next"`, true},
		{"next among several", `<https://api.github.com/x?page=1>; rel="prev", ` +
			`<https://api.github.com/x?page=3>; rel="next", <https://api.github.com/x?page=5>; rel="last"`, true},
		{"last only", `<https://api.github.com/x?page=5>; rel="last"`, false},
	}
	for _, tt := range tests {
		header := http.Header{}
		if tt.link != "" {
			header.Set("Link", tt.link)
		}
		if got := hasNextPage(header); got != tt.want {
			t.Errorf("%s: hasNextPage(%q) = %v, want %v", tt.name, tt.link, got, tt.want)
		}
	}
}

func TestPullRequestsProfileOffersExactlySixReadsAndIsNotRecommended(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var profile config.ToolProfile
	found := false
	for _, candidate := range metadata.Profiles {
		if candidate.ID == "pull-requests" {
			profile, found = candidate, true
		}
	}
	if !found || profile.Recommended {
		t.Fatalf("profile = %+v, found=%v, want an existing, not-recommended profile", profile, found)
	}
	want := []string{pullsList.ID, pullsGet.ID, pullFilesList.ID, pullCommitsList.ID, pullDiffsGet.ID, pullChecksList.ID}
	if strings.Join(profile.Tools, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", profile.Tools, want)
	}
	if got := metadata.ProfilePermissions(profile); len(got) != 1 || got[0] != config.PermissionRead {
		t.Errorf("permissions = %v, want read only", got)
	}
}

// The list is filtered by state, base, and head, and pages by GitHub's Link header since the plain array
// route carries no total count; a cursor stays bound to its filters.
func TestPullRequestsAreListedFilteredAndPagedByLinkHeader(t *testing.T) {
	f, p, base := servePulls(t)
	for i := 1; i <= 35; i++ {
		p.pulls = append(p.pulls, fakePull{number: i, title: fmt.Sprintf("Change %d", i), state: "open",
			headBranch: "feature", headSHA: fmt.Sprintf("sha%02d", i), baseBranch: "main"})
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	type page struct {
		PullRequests []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"pull_requests"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	first, err := invoke(t, core, "github.pullrequests.list", "repo", `{"base":"main","limit":30}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var firstPage page
	if err := json.Unmarshal(first, &firstPage); err != nil || len(firstPage.PullRequests) != 30 || !firstPage.HasMore {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	second, err := invoke(t, core, "github.pullrequests.list", "repo",
		`{"base":"main","cursor":"`+firstPage.NextCursor+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var secondPage page
	if err := json.Unmarshal(second, &secondPage); err != nil || len(secondPage.PullRequests) != 5 || secondPage.HasMore {
		t.Fatalf("second batch = %s, %v", second, err)
	}
	if firstPage.PullRequests[0].Number != 1 || secondPage.PullRequests[4].Number != 35 {
		t.Errorf("numbers = %+v / %+v, want every pull request once in order", firstPage, secondPage)
	}
	for _, request := range f.recorded() {
		if !strings.HasSuffix(request.path, "/pulls") {
			continue
		}
		query, _ := url.ParseQuery(request.query)
		if query.Get("state") != "open" || query.Get("base") != "main" {
			t.Errorf("request = %+v, want the structured filters as GitHub parameters", request)
		}
	}

	before := len(f.recorded())
	for name, cursor := range alteredCursors(t, firstPage.NextCursor) {
		if _, err := invoke(t, core, "github.pullrequests.list", "repo",
			`{"base":"main","cursor":"`+cursor+`"}`, false); !isInvalidRequest(err) ||
			!strings.HasSuffix(err.Error(), "; start the list again without cursor") {
			t.Errorf("%s: err = %v, want an invalid request with the next step", name, err)
		}
	}
	if _, err := invoke(t, core, "github.pullrequests.list", "repo",
		`{"head":"octocat:feature","cursor":"`+firstPage.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 0 {
		t.Errorf("requests = %d, want refusals before provider I/O", len(requests))
	}
}

func TestGetPullRequestReadsFullDetail(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 100, title: "Add feature", state: "open", draft: true,
		headBranch: "feature", headSHA: "deadbeef00", baseBranch: "main", labels: []string{"bug"}, body: bodyCanary})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequests.get", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Number             int      `json:"number"`
		Draft              bool     `json:"draft"`
		Body               string   `json:"body"`
		Mergeable          *bool    `json:"mergeable"`
		MergeableState     string   `json:"mergeable_state"`
		RequestedReviewers []string `json:"requested_reviewers"`
		Commits            int      `json:"commits"`
		ChangedFiles       int      `json:"changed_files"`
		Repository         string   `json:"repository"`
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Number != 100 || !got.Draft || got.Body != bodyCanary || got.Mergeable == nil || !*got.Mergeable ||
		got.MergeableState != "clean" || len(got.RequestedReviewers) != 1 || got.RequestedReviewers[0] != "hubot" ||
		got.Commits != 3 || got.ChangedFiles != 2 || got.Repository != "octo-org/example" {
		t.Errorf("pull request = %s", result)
	}
}

func TestPullRequestFilesTruncatePatchAndReportRenames(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 100, title: "x", state: "open"})
	bigPatch := strings.Repeat("+x", 3000) // 6000 bytes, more than maxPatchBytes
	p.files = map[int][]fakeFile{100: {
		{filename: "a.go", status: "modified", additions: 1, deletions: 0, changes: 1, patch: "@@ -1 +1 @@\n+x\n"},
		{filename: "b.go", status: "modified", additions: 500, deletions: 0, changes: 500, patch: bigPatch},
		{filename: "new.go", previous: "old.go", status: "renamed"},
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequestfiles.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Files []struct {
			Path           string `json:"path"`
			Status         string `json:"status"`
			PreviousPath   string `json:"previous_path"`
			Patch          string `json:"patch"`
			PatchTruncated bool   `json:"patch_truncated"`
		} `json:"files"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(result, &listed); err != nil || len(listed.Files) != 3 || listed.HasMore {
		t.Fatalf("files = %s, %v", result, err)
	}
	if listed.Files[0].Patch != "@@ -1 +1 @@\n+x\n" || listed.Files[0].PatchTruncated {
		t.Errorf("file 0 = %+v, want the short patch kept whole", listed.Files[0])
	}
	if len(listed.Files[1].Patch) != maxPatchBytes || !listed.Files[1].PatchTruncated {
		t.Errorf("file 1 = %d bytes truncated=%v, want %d bytes truncated", len(listed.Files[1].Patch),
			listed.Files[1].PatchTruncated, maxPatchBytes)
	}
	if listed.Files[2].PreviousPath != "old.go" || listed.Files[2].Status != "renamed" {
		t.Errorf("file 2 = %+v, want the rename reported", listed.Files[2])
	}
}

func TestPullRequestCommitsPreferTheGitHubAuthor(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 100, title: "x", state: "open"})
	p.commits = map[int][]fakeCommit{100: {
		{sha: "sha1", authorLogin: "octocat", authorName: "Octo Cat", date: "2026-01-01T00:00:00Z", message: bodyCanary},
		{sha: "sha2", authorName: "Unlinked Author", date: "2026-01-02T00:00:00Z", message: "fixup"},
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequestcommits.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Commits []struct {
			SHA     string `json:"sha"`
			Author  string `json:"author"`
			Message string `json:"message"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(result, &listed); err != nil || len(listed.Commits) != 2 {
		t.Fatalf("commits = %s, %v", result, err)
	}
	if listed.Commits[0].Author != "octocat" || listed.Commits[0].Message != bodyCanary {
		t.Errorf("commit 0 = %+v, want the GitHub login preferred", listed.Commits[0])
	}
	if listed.Commits[1].Author != "Unlinked Author" {
		t.Errorf("commit 1 = %+v, want the commit author name without a linked account", listed.Commits[1])
	}
}

func TestPullRequestDiffIsBoundedFromItsStart(t *testing.T) {
	f, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 100, title: "x", state: "open"},
		fakePull{number: 101, title: "y", state: "open"})
	small := "diff --git a/a b/a\n+line\n"
	p.diffs = map[int]string{100: small, 101: strings.Repeat("+line\n", 20000)} // more than maxDiffBytes
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequestdiffs.get", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Diff      string `json:"diff"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(result, &got); err != nil || got.Diff != small || got.Truncated {
		t.Fatalf("small diff = %s, %v", result, err)
	}

	result, err = invoke(t, core, "github.pullrequestdiffs.get", "repo", `{"number":101}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var big struct {
		Diff      string `json:"diff"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(result, &big); err != nil || len(big.Diff) != maxDiffBytes || !big.Truncated {
		t.Fatalf("large diff = %d bytes truncated=%v, %v", len(big.Diff), big.Truncated, err)
	}
	for _, request := range f.recorded() {
		if strings.HasSuffix(request.path, "/pulls/100") && request.accept != "application/vnd.github.diff" {
			t.Errorf("request = %+v, want the diff media type", request)
		}
	}
}

func TestPullRequestChecksCombineRunsAndStatusesWithOverall(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls,
		fakePull{number: 100, title: "pending", state: "open", headSHA: "aaa100"},
		fakePull{number: 101, title: "success", state: "open", headSHA: "aaa101"},
		fakePull{number: 102, title: "none", state: "open", headSHA: "aaa102"},
		fakePull{number: 103, title: "failure", state: "open", headSHA: "aaa103"},
	)
	p.runs = map[string][]fakeCheckRun{
		"aaa100": {{name: "build", status: "in_progress"}},
		"aaa101": {{name: "build", status: "completed", conclusion: "success"}},
		"aaa103": {{name: "build", status: "completed", conclusion: "failure"}},
	}
	p.runsTotal = map[string]int{"aaa100": 3}
	p.statuses = map[string][]fakeStatus{
		"aaa100": {{context: "ci/legacy", state: "success", updatedAt: "2026-01-01T00:00:00Z"}},
		"aaa101": {{context: "ci/legacy", state: "success", updatedAt: "2026-01-01T00:00:00Z"}},
		"aaa103": {{context: "ci/legacy", state: "pending"}},
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, tt := range []struct {
		number    int
		overall   string
		truncated bool
		checks    int
	}{
		{100, "pending", true, 2},
		{101, "success", false, 2},
		{102, "none", false, 0},
		{103, "failure", false, 2},
	} {
		result, err := invoke(t, core, "github.pullrequestchecks.list", "repo",
			fmt.Sprintf(`{"number":%d}`, tt.number), false)
		if err != nil {
			t.Fatalf("%d: %v", tt.number, err)
		}
		var got struct {
			HeadSHA   string           `json:"head_sha"`
			Checks    []map[string]any `json:"checks"`
			Overall   string           `json:"overall"`
			Truncated bool             `json:"truncated"`
		}
		if err := json.Unmarshal(result, &got); err != nil {
			t.Fatal(err)
		}
		if got.Overall != tt.overall || got.Truncated != tt.truncated || len(got.Checks) != tt.checks {
			t.Errorf("pull %d checks = %s, want overall=%s truncated=%v checks=%d", tt.number, result,
				tt.overall, tt.truncated, tt.checks)
		}
	}
}

// Not found names the pull request or, once it was read, the commit its checks were asked for.
func TestPullRequestNotFoundNamesTheSubject(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 106, title: "x", state: "open", headSHA: "facade0000"})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ operation, arguments, want string }{
		{"github.pullrequests.get", `{"number":9}`, "pull request #9 in repository octo-org/example"},
		{"github.pullrequestfiles.list", `{"number":9}`, "pull request #9 in repository octo-org/example"},
		{"github.pullrequestcommits.list", `{"number":9}`, "pull request #9 in repository octo-org/example"},
		{"github.pullrequestdiffs.get", `{"number":9}`, "pull request #9 in repository octo-org/example"},
		{"github.pullrequestchecks.list", `{"number":9}`, "pull request #9 in repository octo-org/example"},
		{"github.pullrequestchecks.list", `{"number":106}`, "commit facade0000 in repository octo-org/example"},
	} {
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, false); classOf(err) != provider.ClassNotFound ||
			!strings.Contains(err.Error(), "GitHub does not hold "+tt.want) {
			t.Errorf("%s %s = %v, want not-found naming %s", tt.operation, tt.arguments, err, tt.want)
		}
	}
}

// Every pull request tool satisfies its output contract through the application core, and the token never
// reaches an answer.
func TestPullRequestsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	_, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 100, title: "x", state: "open", headSHA: "aaa100"})
	p.files = map[int][]fakeFile{100: {{filename: "a.go", status: "modified", additions: 1, changes: 1, patch: "x"}}}
	p.commits = map[int][]fakeCommit{100: {{sha: "c1", authorName: "Octo", date: "2026-01-01T00:00:00Z", message: "msg"}}}
	p.diffs = map[int]string{100: "diff --git a/a b/a\n"}
	p.runs = map[string][]fakeCheckRun{"aaa100": {{name: "build", status: "completed", conclusion: "success"}}}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, request := range []struct{ operation, arguments string }{
		{"github.pullrequests.list", `{"limit":5}`},
		{"github.pullrequests.get", `{"number":100}`},
		{"github.pullrequestfiles.list", `{"number":100}`},
		{"github.pullrequestcommits.list", `{"number":100}`},
		{"github.pullrequestdiffs.get", `{"number":100}`},
		{"github.pullrequestchecks.list", `{"number":100}`},
	} {
		result, err := invoke(t, core, request.operation, "repo", request.arguments, false)
		if err != nil {
			t.Errorf("%s %s = %v", request.operation, request.arguments, err)
			continue
		}
		if strings.Contains(string(result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.operation, result)
		}
	}
}
