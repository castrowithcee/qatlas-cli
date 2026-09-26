package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
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
	mergeCommitSHA                  string
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
// Every other repository is unknown to it. notMergeable makes the next merge attempt answer 405 instead of
// comparing the head commit, consumed by the one request that hits it.
type fakePulls struct {
	f             *fakeGitHub
	pulls         []fakePull
	files         map[int][]fakeFile
	commits       map[int][]fakeCommit
	diffs         map[int]string
	runs          map[string][]fakeCheckRun
	runsTotal     map[string]int
	statuses      map[string][]fakeStatus
	statusesTotal map[string]int
	seq           int
	notMergeable  bool
}

func servePulls(t *testing.T) (*fakeGitHub, *fakePulls, string) {
	t.Helper()
	p := &fakePulls{}
	f := &fakeGitHub{failure: p.route}
	p.f = f
	return f, p, serve(t, f)
}

func (p *fakePulls) route(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/graphql" {
		w.Header().Set("Content-Type", "application/json")
		p.draftMutation(w)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok || !(strings.HasPrefix(rest, "pulls") || strings.HasPrefix(rest, "commits/")) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && rest == "pulls":
		p.listPulls(w, r)
	case r.Method == http.MethodPost && rest == "pulls":
		p.createPull(w)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/") && strings.HasSuffix(rest, "/files"):
		p.listFiles(w, r, rest)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "pulls/") && strings.HasSuffix(rest, "/commits"):
		p.listCommits(w, r, rest)
	case r.Method == http.MethodPut && strings.HasSuffix(rest, "/merge"):
		p.mergePull(w, rest)
	case r.Method == http.MethodPut && strings.HasSuffix(rest, "/update-branch"):
		p.updateBranch(w, rest)
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "pulls/") && !strings.Contains(strings.TrimPrefix(rest, "pulls/"), "/"):
		p.patchPull(w, rest)
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

// lastBody is the decoded REST body of the most recent request, which fakeGitHub already recorded before the
// failure hook ran.
func (p *fakePulls) lastBody() map[string]any {
	requests := p.f.recorded()
	if len(requests) == 0 {
		return nil
	}
	return requests[len(requests)-1].body
}

func (p *fakePulls) lastVariables() map[string]any {
	requests := p.f.recorded()
	if len(requests) == 0 {
		return nil
	}
	return requests[len(requests)-1].variables
}

func (p *fakePulls) lastDocument() string {
	requests := p.f.recorded()
	if len(requests) == 0 {
		return ""
	}
	return requests[len(requests)-1].document
}

func (p *fakePulls) index(number int) int {
	for i, pull := range p.pulls {
		if pull.number == number {
			return i
		}
	}
	return -1
}

func (p *fakePulls) createPull(w http.ResponseWriter) {
	body := p.lastBody()
	p.seq++
	number := 200 + p.seq
	title, _ := body["title"].(string)
	head, _ := body["head"].(string)
	base, _ := body["base"].(string)
	pullBody, _ := body["body"].(string)
	draft, _ := body["draft"].(bool)
	pull := fakePull{number: number, title: title, state: "open", draft: draft, headBranch: head,
		headSHA: fmt.Sprintf("createdsha%d", number), baseBranch: base, body: pullBody}
	p.pulls = append(p.pulls, pull)
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, pullJSONOf(pull))
}

func (p *fakePulls) patchPull(w http.ResponseWriter, rest string) {
	number, ok := pullNumber(rest)
	idx := p.index(number)
	if !ok || idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := p.lastBody()
	pull := &p.pulls[idx]
	if v, ok := body["title"].(string); ok {
		pull.title = v
	}
	if v, ok := body["body"].(string); ok {
		pull.body = v
	}
	if v, ok := body["base"].(string); ok {
		pull.baseBranch = v
	}
	if v, ok := body["state"].(string); ok {
		pull.state = v
	}
	fmt.Fprint(w, pullJSONOf(*pull))
}

func (p *fakePulls) mergePull(w http.ResponseWriter, rest string) {
	tail := strings.TrimSuffix(strings.TrimPrefix(rest, "pulls/"), "/merge")
	number, err := strconv.Atoi(tail)
	idx := p.index(number)
	if err != nil || idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	pull := &p.pulls[idx]
	body := p.lastBody()
	sha, _ := body["sha"].(string)
	switch {
	case p.notMergeable:
		p.notMergeable = false
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(w, `{"message":"Pull Request is not mergeable"}`)
	case sha != pull.headSHA:
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"message":"Head branch was modified. Review and try the merge again."}`)
	default:
		pull.merged, pull.state, pull.mergeCommitSHA = true, "closed", fmt.Sprintf("mergecommit%d", number)
		fmt.Fprintf(w, `{"sha":%q,"merged":true,"message":"Pull Request successfully merged"}`, pull.mergeCommitSHA)
	}
}

func (p *fakePulls) updateBranch(w http.ResponseWriter, rest string) {
	tail := strings.TrimSuffix(strings.TrimPrefix(rest, "pulls/"), "/update-branch")
	number, err := strconv.Atoi(tail)
	idx := p.index(number)
	if err != nil || idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := p.lastBody()
	sha, _ := body["expected_head_sha"].(string)
	if sha != p.pulls[idx].headSHA {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"expected_head_sha is not the current head"}`)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"message":"Updating pull request branch.","url":"https://api.github.com/repos/octo-org/example/pulls/%d"}`,
		number)
}

// pullNodeID and pullNodeNumber mirror each other: the fake's node identifier of a pull request, and the
// number it stands for.
func pullNodeID(number int) string { return fmt.Sprintf("PR_kw%d", number) }

func pullNodeNumber(id string) (int, bool) {
	digits, ok := strings.CutPrefix(id, "PR_kw")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

// draftMutation answers markPullRequestReadyForReview and convertPullRequestToDraft, the only GraphQL calls a
// pull request tool sends.
func (p *fakePulls) draftMutation(w http.ResponseWriter) {
	id, _ := p.lastVariables()["id"].(string)
	number, ok := pullNodeNumber(id)
	idx := p.index(number)
	if !ok || idx < 0 {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","message":"no pull request"}]}`)
		return
	}
	pull := &p.pulls[idx]
	alias := "ready"
	if strings.Contains(p.lastDocument(), "convertPullRequestToDraft") {
		pull.draft, alias = true, "draft"
	} else {
		pull.draft = false
	}
	fmt.Fprintf(w, `{"data":{%q:{"pullRequest":{"id":%q,"isDraft":%v}}}}`, alias, id, pull.draft)
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
	mergeCommitSHA := pull.mergeCommitSHA
	if mergeCommitSHA == "" {
		mergeCommitSHA = "mergecommitsha"
	}
	return fmt.Sprintf(`{"number":%d,"node_id":%q,"title":%q,"body":%q,"state":%q,"draft":%v,`+
		`"user":{"login":"octocat"},"head":{"ref":%q,"sha":%q},"base":{"ref":%q,"sha":"basesha"},`+
		`"labels":[%s],"requested_reviewers":[{"login":"hubot"}],"merged":%v,`+
		`"mergeable":true,"mergeable_state":"clean","merge_commit_sha":%q,"commits":3,`+
		`"changed_files":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",`+
		`"closed_at":%s,"merged_at":%s,"html_url":"https://github.com/octo-org/example/pull/%d"}`,
		pull.number, pullNodeID(pull.number), pull.title, pull.body, pull.state, pull.draft, pull.headBranch,
		pull.headSHA, pull.baseBranch, labelsJSON(pull.labels), pull.merged, mergeCommitSHA, closedAt, mergedAt,
		pull.number)
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

// Create opens a pull request with one request; update sends REST fields, a draft change, or both, re-reading
// the pull request only when a draft change leaves the REST answer stale; close and reopen are idempotent on
// a pull request already in the state asked for, and reopening a merged pull request is refused.
func TestPullRequestCreateUpdateCloseAndReopen(t *testing.T) {
	f, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 50, title: "Old title", state: "open", headBranch: "feature",
		headSHA: "sha050", baseBranch: "main"})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	before := len(f.recorded())
	created, err := invoke(t, core, "github.pullrequests.create", "repo",
		`{"title":"Add retry logic","head":"feature/retry","base":"main","draft":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var createdPull struct {
		State string `json:"state"`
		Draft bool   `json:"draft"`
	}
	if err := json.Unmarshal(created, &createdPull); err != nil || createdPull.State != "open" || !createdPull.Draft {
		t.Fatalf("created pull = %s, %v", created, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodPost {
		t.Errorf("create requests = %+v, want exactly one POST", requests)
	}

	before = len(f.recorded())
	updated, err := invoke(t, core, "github.pullrequests.update", "repo",
		`{"number":50,"title":"New title","base":"develop"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var updatedPull struct {
		Title      string `json:"title"`
		BaseBranch string `json:"base_branch"`
	}
	if err := json.Unmarshal(updated, &updatedPull); err != nil || updatedPull.Title != "New title" ||
		updatedPull.BaseBranch != "develop" {
		t.Fatalf("updated pull = %s, %v", updated, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 || requests[0].method != http.MethodGet ||
		requests[1].method != http.MethodPatch {
		t.Errorf("update requests = %+v, want a read then a PATCH", requests)
	}

	// A draft change alone sends only the mutation, then re-reads the pull request because GitHub's REST
	// answer would otherwise still show the earlier draft state.
	before = len(f.recorded())
	toDraft, err := invoke(t, core, "github.pullrequests.update", "repo", `{"number":50,"draft":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var draftPull struct {
		Draft bool `json:"draft"`
	}
	if err := json.Unmarshal(toDraft, &draftPull); err != nil || !draftPull.Draft {
		t.Fatalf("draft pull = %s, %v", toDraft, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 3 || requests[0].method != http.MethodGet ||
		requests[1].path != "/api/graphql" || requests[2].method != http.MethodGet {
		t.Errorf("draft-only requests = %+v, want a read, a mutation, then a read", requests)
	}

	// A REST field and a draft change together send both, then re-read once.
	before = len(f.recorded())
	ready, err := invoke(t, core, "github.pullrequests.update", "repo",
		`{"number":50,"title":"Ready again","draft":false}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var readyPull struct {
		Title string `json:"title"`
		Draft bool   `json:"draft"`
	}
	if err := json.Unmarshal(ready, &readyPull); err != nil || readyPull.Draft || readyPull.Title != "Ready again" {
		t.Fatalf("ready pull = %s, %v", ready, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 4 || requests[0].method != http.MethodGet ||
		requests[1].method != http.MethodPatch || requests[2].path != "/api/graphql" ||
		requests[3].method != http.MethodGet {
		t.Errorf("combined update requests = %+v, want a read, a PATCH, a mutation, then a read", requests)
	}

	before = len(f.recorded())
	closed, err := invoke(t, core, "github.pullrequests.close", "repo", `{"number":50}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var closedPull struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(closed, &closedPull); err != nil || closedPull.State != "closed" {
		t.Fatalf("closed pull = %s, %v", closed, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 || requests[1].method != http.MethodPatch {
		t.Errorf("close requests = %+v, want a read then a PATCH", requests)
	}

	// A repeated close on an already-closed pull request is idempotent and sends only the read.
	before = len(f.recorded())
	if _, err := invoke(t, core, "github.pullrequests.close", "repo", `{"number":50}`, true); err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a repeated close sent %d requests, want only the read", len(requests))
	}

	before = len(f.recorded())
	reopened, err := invoke(t, core, "github.pullrequests.reopen", "repo", `{"number":50}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var reopenedPull struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(reopened, &reopenedPull); err != nil || reopenedPull.State != "open" {
		t.Fatalf("reopened pull = %s, %v", reopened, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 || requests[1].method != http.MethodPatch {
		t.Errorf("reopen requests = %+v, want a read then a PATCH", requests)
	}

	// A repeated reopen on an already-open pull request is idempotent and sends only the read.
	before = len(f.recorded())
	if _, err := invoke(t, core, "github.pullrequests.reopen", "repo", `{"number":50}`, true); err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a repeated reopen sent %d requests, want only the read", len(requests))
	}

	// A merged pull request cannot be reopened; the refusal names it before any request is sent.
	idx := p.index(50)
	p.pulls[idx].state, p.pulls[idx].merged = "closed", true
	before = len(f.recorded())
	if _, err := invoke(t, core, "github.pullrequests.reopen", "repo", `{"number":50}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "pull request #50 is already merged and cannot be reopened") {
		t.Errorf("reopen of a merged pull request = %v, want an invalid request naming it", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a refused reopen sent %d requests, want only the read", len(requests))
	}
}

// A head branch update is sent only while the given commit is still the pull request's head; a stale one is
// refused clearly without applying anything.
func TestPullRequestBranchUpdateQueuesWhileTheHeadMatches(t *testing.T) {
	f, p, base := servePulls(t)
	head := strings.Repeat("a", 40)
	p.pulls = append(p.pulls, fakePull{number: 60, title: "x", state: "open", headSHA: head})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequestbranches.update", "repo",
		fmt.Sprintf(`{"number":60,"expected_head_sha":%q}`, head), true)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Number   int  `json:"number"`
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(result, &got); err != nil || !got.Accepted || got.Number != 60 {
		t.Fatalf("branch update = %s, %v", result, err)
	}
	if requests := f.recorded(); len(requests) != 2 || requests[0].method != http.MethodGet ||
		requests[1].method != http.MethodPut {
		t.Errorf("requests = %+v, want a read then a PUT", requests)
	}

	before := len(f.recorded())
	stale := strings.Repeat("b", 40)
	_, err = invoke(t, core, "github.pullrequestbranches.update", "repo",
		fmt.Sprintf(`{"number":60,"expected_head_sha":%q}`, stale), true)
	if classOf(err) != provider.ClassProviderError ||
		!strings.Contains(err.Error(), "expected_head_sha may no longer be its current head commit") {
		t.Errorf("a stale expected_head_sha = %v, want a clear provider refusal", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 || requests[1].method != http.MethodPut {
		t.Errorf("stale update requests = %+v, want a read then the refused PUT", requests)
	}
}

// A merge is sent only while sha is still the pull request's head; already merged with exactly that head is
// success without another request, already merged with another is refused naming both, a changed head answers
// 409 and becomes an invalid request naming both, an unmergeable pull request answers 405 with the reason, and
// merge is never offered by a connection whose tools list does not name it.
func TestPullRequestMergeIsIdempotentAndReportsConflictsClearly(t *testing.T) {
	f, p, base := servePulls(t)
	head := strings.Repeat("c", 40)
	p.pulls = append(p.pulls, fakePull{number: 70, title: "x", state: "open", headSHA: head})
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["merger"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionUpdate},
		Tools:       []string{pullsGet.ID, pullsMerge.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)

	result, err := invoke(t, core, "github.pullrequests.merge", "merger",
		fmt.Sprintf(`{"number":70,"sha":%q,"method":"squash"}`, head), true)
	if err != nil {
		t.Fatal(err)
	}
	var merged struct {
		Number int    `json:"number"`
		Merged bool   `json:"merged"`
		SHA    string `json:"sha"`
	}
	if err := json.Unmarshal(result, &merged); err != nil || !merged.Merged || merged.SHA == "" {
		t.Fatalf("merge = %s, %v", result, err)
	}
	if requests := f.recorded(); requests[len(requests)-1].method != http.MethodPut ||
		requests[len(requests)-1].body["merge_method"] != "squash" {
		t.Errorf("merge request = %+v, want merge_method squash", requests[len(requests)-1])
	}

	// Already merged with exactly this head commit is idempotent success without another request.
	before := len(f.recorded())
	again, err := invoke(t, core, "github.pullrequests.merge", "merger",
		fmt.Sprintf(`{"number":70,"sha":%q}`, head), true)
	if err != nil {
		t.Fatal(err)
	}
	var repeated struct {
		Merged bool   `json:"merged"`
		SHA    string `json:"sha"`
	}
	if err := json.Unmarshal(again, &repeated); err != nil || !repeated.Merged || repeated.SHA != merged.SHA {
		t.Fatalf("repeated merge = %s, %v, want the same merge commit", again, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodGet {
		t.Errorf("a repeated merge with the same head sent %d requests, want only the read", len(requests))
	}

	// Already merged with a different head commit is refused as an invalid request naming both.
	before = len(f.recorded())
	other := strings.Repeat("d", 40)
	_, err = invoke(t, core, "github.pullrequests.merge", "merger", fmt.Sprintf(`{"number":70,"sha":%q}`, other), true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), head) || !strings.Contains(err.Error(), other) {
		t.Errorf("merge with the wrong head = %v, want an invalid request naming both commits", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a mismatched already-merged check sent %d requests, want only the read", len(requests))
	}

	// A fresh, unmerged pull request whose head changed since it was read answers 409, which becomes an
	// invalid request naming the expected and the now-current head.
	stale, current := strings.Repeat("e", 40), strings.Repeat("f", 40)
	p.pulls = append(p.pulls, fakePull{number: 71, title: "y", state: "open", headSHA: current})
	_, err = invoke(t, core, "github.pullrequests.merge", "merger", fmt.Sprintf(`{"number":71,"sha":%q}`, stale), true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), stale) || !strings.Contains(err.Error(), current) {
		t.Errorf("a changed head = %v, want an invalid request naming the expected and the current head", err)
	}

	// A pull request GitHub cannot merge answers 405 with a clear provider message naming likely reasons; it
	// is never retried automatically.
	before = len(f.recorded())
	p.notMergeable = true
	_, err = invoke(t, core, "github.pullrequests.merge", "merger", fmt.Sprintf(`{"number":71,"sha":%q}`, current), true)
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "not mergeable") {
		t.Errorf("a not-mergeable pull request = %v, want a clear provider refusal", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 {
		t.Errorf("a refused merge sent %d requests, want a read and exactly one attempt, never a retry", len(requests))
	}

	// A connection whose tools list does not name the merge neither discovers nor runs it.
	before = len(f.recorded())
	_, err = invoke(t, core, "github.pullrequests.merge", "repo", fmt.Sprintf(`{"number":70,"sha":%q}`, head), true)
	if _, ok := err.(*capability.UnsupportedError); !ok {
		t.Errorf("merge on a connection without the tool listed = %T %v, want unsupported", err, err)
	}
	if len(f.recorded()) != before {
		t.Error("an unsupported merge reached GitHub")
	}
}

// Every pull request change tool satisfies its output contract through the application core and is audited
// as one successful change.
func TestPullRequestChangesSatisfyTheirContractThroughTheApplicationCoreWithAudit(t *testing.T) {
	_, p, base := servePulls(t)
	sha := strings.Repeat("1", 40)
	p.pulls = append(p.pulls,
		fakePull{number: 80, title: "x", state: "open", headSHA: sha},
		fakePull{number: 81, title: "y", state: "closed"},
		fakePull{number: 82, title: "z", state: "open", headSHA: strings.Repeat("2", 40)},
	)
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["merger"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionUpdate},
		Tools:       []string{pullsGet.ID, pullsMerge.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	var audit strings.Builder
	core.SetAudit(&audit)

	for _, request := range []application.InvokeRequest{
		{Operation: "github.pullrequests.create", Connection: "repo",
			Arguments: json.RawMessage(`{"title":"New feature","head":"feature/new","base":"main"}`)},
		{Operation: "github.pullrequests.update", Connection: "repo",
			Arguments: json.RawMessage(`{"number":80,"title":"Updated title"}`)},
		{Operation: "github.pullrequests.close", Connection: "repo", Arguments: json.RawMessage(`{"number":80}`)},
		{Operation: "github.pullrequests.reopen", Connection: "repo", Arguments: json.RawMessage(`{"number":81}`)},
		{Operation: "github.pullrequestbranches.update", Connection: "repo",
			Arguments: json.RawMessage(fmt.Sprintf(`{"number":82,"expected_head_sha":%q}`, strings.Repeat("2", 40)))},
		{Operation: "github.pullrequests.merge", Connection: "merger",
			Arguments: json.RawMessage(fmt.Sprintf(`{"number":82,"sha":%q}`, strings.Repeat("2", 40)))},
	} {
		request.Confirmed = true
		response, err := core.Invoke(context.Background(), request)
		if err != nil {
			t.Errorf("%s %s = %v", request.Operation, request.Arguments, err)
			continue
		}
		if strings.Contains(string(response.Result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.Operation, response.Result)
		}
	}
	if strings.Count(audit.String(), `"result":"success"`) != 6 {
		t.Errorf("audit = %s, want one success event per confirmed change", audit.String())
	}
}
