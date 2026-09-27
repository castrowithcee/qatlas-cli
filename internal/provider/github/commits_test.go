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
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeCommit2 is one commit of the fake repository's commit history. It is distinct from fakeCommit, which
// the pull request tests use for the narrower shape GitHub's pull request commits route answers.
type fakeCommit2 struct {
	sha, authorLogin, authorName, committerLogin, committerName, authorDate, committerDate, message string
	parents                                                                                         []string
	stats                                                                                           *[3]int // additions, deletions, total
	files                                                                                           []fakeFile
}

// fakeCommits answers the commits routes of the bound repository through the failure hook of fakeGitHub.
type fakeCommits struct {
	f       *fakeGitHub
	commits []fakeCommit2
}

func serveCommits(t *testing.T) (*fakeGitHub, *fakeCommits, string) {
	t.Helper()
	c := &fakeCommits{}
	f := &fakeGitHub{failure: c.route}
	c.f = f
	return f, c, serve(t, f)
}

func (c *fakeCommits) route(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok || !strings.HasPrefix(rest, "commits") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && rest == "commits":
		c.list(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "commits/"):
		c.get(w, strings.TrimPrefix(rest, "commits/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

func commit2JSON(commit fakeCommit2, full bool) string {
	author, committer := "null", "null"
	if commit.authorLogin != "" {
		author = fmt.Sprintf(`{"login":%q}`, commit.authorLogin)
	}
	if commit.committerLogin != "" {
		committer = fmt.Sprintf(`{"login":%q}`, commit.committerLogin)
	}
	parents := make([]string, 0, len(commit.parents))
	for _, sha := range commit.parents {
		parents = append(parents, fmt.Sprintf(`{"sha":%q}`, sha))
	}
	base := fmt.Sprintf(`"sha":%q,"commit":{"message":%q,"author":{"name":%q,"date":%q},`+
		`"committer":{"name":%q,"date":%q}},"author":%s,"committer":%s,"parents":[%s]`,
		commit.sha, commit.message, commit.authorName, commit.authorDate, commit.committerName,
		commit.committerDate, author, committer, strings.Join(parents, ","))
	if !full {
		return "{" + base + "}"
	}
	stats := `null`
	if commit.stats != nil {
		stats = fmt.Sprintf(`{"additions":%d,"deletions":%d,"total":%d}`, commit.stats[0], commit.stats[1], commit.stats[2])
	}
	files := make([]string, 0, len(commit.files))
	for _, file := range commit.files {
		files = append(files, fileJSONOf(file))
	}
	return "{" + base + fmt.Sprintf(`,"stats":%s,"files":[%s]`, stats, strings.Join(files, ",")) + "}"
}

func (c *fakeCommits) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	matches := make([]fakeCommit2, 0, len(c.commits))
	for _, commit := range c.commits {
		if sha := query.Get("sha"); sha != "" && sha != commit.sha {
			continue
		}
		if author := query.Get("author"); author != "" && author != commit.authorLogin {
			continue
		}
		matches = append(matches, commit)
	}
	perPage, _ := strconv.Atoi(query.Get("per_page"))
	pageNumber, _ := strconv.Atoi(query.Get("page"))
	start := min((pageNumber-1)*perPage, len(matches))
	end := min(start+perPage, len(matches))
	entries := make([]string, 0, end-start)
	for _, commit := range matches[start:end] {
		entries = append(entries, commit2JSON(commit, false))
	}
	if end < len(matches) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (c *fakeCommits) get(w http.ResponseWriter, ref string) {
	for _, commit := range c.commits {
		if commit.sha == ref {
			fmt.Fprint(w, commit2JSON(commit, true))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"message":"Not Found"}`)
}

// Commits are filtered by ref, path, author, since, and until (sent as GitHub's sha, path, author, since,
// and until query parameters), and paged by GitHub's Link header since the plain array route carries no
// total count; a cursor stays bound to its filters. The compact list carries no patch.
func TestCommitsAreListedFilteredAndPagedByLinkHeader(t *testing.T) {
	f, c, base := serveCommits(t)
	for i := 1; i <= 35; i++ {
		c.commits = append(c.commits, fakeCommit2{sha: fmt.Sprintf("sha%02d", i), authorLogin: "octocat",
			authorName: "The Octocat", authorDate: "2026-01-01T00:00:00Z", committerName: "The Octocat",
			committerDate: "2026-01-01T00:00:00Z", message: "Fix bug " + strconv.Itoa(i) + "\n\nDetails.",
			parents: []string{"parent" + strconv.Itoa(i)}})
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	type page struct {
		Commits []struct {
			SHA     string `json:"sha"`
			Message string `json:"message"`
			Author  string `json:"author"`
			Parents int    `json:"parents"`
		} `json:"commits"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	first, err := invoke(t, core, commitsList.ID, "repo", `{"author":"octocat","limit":30}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var firstPage page
	if json.Unmarshal(first, &firstPage) != nil || len(firstPage.Commits) != 30 || !firstPage.HasMore {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	if firstPage.Commits[0].Message != "Fix bug 1" || firstPage.Commits[0].Parents != 1 {
		t.Errorf("first commit = %+v, want the message cut to its first line and one parent", firstPage.Commits[0])
	}
	second, err := invoke(t, core, commitsList.ID, "repo", `{"author":"octocat","cursor":"`+firstPage.NextCursor+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var secondPage page
	if json.Unmarshal(second, &secondPage) != nil || len(secondPage.Commits) != 5 || secondPage.HasMore {
		t.Fatalf("second batch = %s, %v", second, err)
	}
	for _, request := range f.recorded() {
		if !strings.HasSuffix(request.path, "/commits") {
			continue
		}
		query, _ := url.ParseQuery(request.query)
		if query.Get("author") != "octocat" {
			t.Errorf("request = %+v, want the author filter as GitHub's parameter", request)
		}
	}

	// A cursor of other filters is refused.
	before := len(f.recorded())
	if _, err := invoke(t, core, commitsList.ID, "repo",
		`{"author":"hubot","cursor":"`+firstPage.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 0 {
		t.Errorf("requests = %d, want refusals before provider I/O", len(requests))
	}
	if strings.Contains(string(first), `"patch"`) {
		t.Errorf("the compact list carries a patch: %s", first)
	}
}

// One commit answers its stats and its changed files, each file's patch cut to maxPatchBytes with the cut
// visible, and the file count cut to maxCommitFiles with the cut visible as well.
func TestCommitGetReadsStatsAndBoundsFilesAndPatches(t *testing.T) {
	_, c, base := serveCommits(t)
	bigPatch := strings.Repeat("+x", 3000) // 6000 bytes, more than maxPatchBytes
	files := []fakeFile{{filename: "a.go", status: "modified", additions: 10, deletions: 2, changes: 12, patch: bigPatch},
		{filename: "b.go", status: "renamed", previous: "old.go", additions: 0, deletions: 0, changes: 0}}
	c.commits = append(c.commits, fakeCommit2{sha: "deadbeef", authorLogin: "octocat", authorName: "The Octocat",
		authorDate: "2026-01-01T00:00:00Z", committerName: "The Octocat", committerDate: "2026-01-01T00:00:00Z",
		message: "Fix the crash\n\nFull body of the commit message.", parents: []string{"parent1", "parent2"},
		stats: &[3]int{10, 2, 12}, files: files})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, commitsGet.ID, "repo", `{"ref":"deadbeef"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var detail CommitDetail
	if json.Unmarshal(result, &detail) != nil {
		t.Fatal(err)
	}
	if detail.SHA != "deadbeef" || detail.Message != "Fix the crash\n\nFull body of the commit message." ||
		detail.MessageTruncated || detail.Author != "octocat" || len(detail.Parents) != 2 ||
		detail.Additions != 10 || detail.Deletions != 2 || detail.Total != 12 {
		t.Fatalf("commit detail = %+v", detail)
	}
	if len(detail.Files) != 2 || detail.Files[0].Path != "a.go" || len(detail.Files[0].Patch) != maxPatchBytes ||
		!detail.Files[0].PatchTruncated {
		t.Fatalf("files[0] = %+v, want a patch cut to %d bytes", detail.Files[0], maxPatchBytes)
	}
	if detail.Files[1].PreviousFilename != "old.go" || detail.Files[1].PatchTruncated {
		t.Fatalf("files[1] = %+v, want the rename's previous filename and no patch cut", detail.Files[1])
	}
	if detail.FilesTruncated {
		t.Errorf("files_truncated = true for a commit under the bound")
	}

	// A commit with at least maxCommitFiles files is reported as cut, since GitHub's own route already caps
	// there without ever saying whether more exist.
	many := make([]fakeFile, 0, maxCommitFiles+5)
	for i := 0; i < maxCommitFiles+5; i++ {
		many = append(many, fakeFile{filename: fmt.Sprintf("f%d.go", i), status: "modified", additions: 1, changes: 1})
	}
	c.commits = append(c.commits, fakeCommit2{sha: "manyfiles", authorName: "A", authorDate: "2026-01-01T00:00:00Z",
		committerName: "A", committerDate: "2026-01-01T00:00:00Z", message: "many", stats: &[3]int{0, 0, 0},
		files: many})
	result, err = invoke(t, core, commitsGet.ID, "repo", `{"ref":"manyfiles"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var big CommitDetail
	if json.Unmarshal(result, &big) != nil || !big.FilesTruncated || len(big.Files) != maxCommitFiles {
		t.Fatalf("commit with many files = %+v, want %d files with files_truncated", big, maxCommitFiles)
	}

	// A ref GitHub does not hold is not-found, naming the ref.
	if _, err := invoke(t, core, commitsGet.ID, "repo", `{"ref":"missing"}`, false); classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), "commit missing in repository octo-org/example") {
		t.Errorf("get on a missing ref = %v, want not-found naming the commit", err)
	}
}

// A long message is cut to its first line and to maxCommitSummaryLength characters in the compact list; a
// message longer than maxCommitMessageLength characters is cut, with the cut visible, in the get.
func TestCommitMessagesAreBoundedWithTheCutVisible(t *testing.T) {
	_, c, base := serveCommits(t)
	longFirstLine := strings.Repeat("x", maxCommitSummaryLength+50)
	c.commits = append(c.commits, fakeCommit2{sha: "longsha", authorName: "A", authorDate: "2026-01-01T00:00:00Z",
		committerName: "A", committerDate: "2026-01-01T00:00:00Z", message: longFirstLine + "\nmore body",
		stats: &[3]int{0, 0, 0}})
	longMessage := strings.Repeat("y", maxCommitMessageLength+50)
	c.commits = append(c.commits, fakeCommit2{sha: "longmsg", authorName: "A", authorDate: "2026-01-01T00:00:00Z",
		committerName: "A", committerDate: "2026-01-01T00:00:00Z", message: longMessage, stats: &[3]int{0, 0, 0}})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, commitsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Commits []CommitEntry `json:"commits"`
	}
	if json.Unmarshal(result, &listed) != nil || len(listed.Commits) != 2 ||
		len([]rune(listed.Commits[0].Message)) != maxCommitSummaryLength {
		t.Fatalf("list = %+v, want the first line cut to %d characters", listed, maxCommitSummaryLength)
	}

	result, err = invoke(t, core, commitsGet.ID, "repo", `{"ref":"longmsg"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var detail CommitDetail
	if json.Unmarshal(result, &detail) != nil || !detail.MessageTruncated ||
		len([]rune(detail.Message)) != maxCommitMessageLength {
		t.Fatalf("get on a long message = %+v, want it cut to %d characters", detail, maxCommitMessageLength)
	}
}

// Ref, path, author, since, and until are checked before a credential is resolved, so an unusable value
// never reaches GitHub, and since must not lie after until.
func TestCommitArgumentsAreValidatedBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, operation, arguments string
	}{
		{"bad ref in list", commitsList.ID, `{"ref":"bad..ref"}`},
		{"bad path", commitsList.ID, `{"path":"../secret"}`},
		{"bad author", commitsList.ID, `{"author":"bad author"}`},
		{"bad since", commitsList.ID, `{"since":"not-a-date"}`},
		{"since after until", commitsList.ID, `{"since":"2026-02-01","until":"2026-01-01"}`},
		{"missing ref in get", commitsGet.ID, `{}`},
		{"bad ref in get", commitsGet.ID, `{"ref":"bad..ref"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.operation, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.operation)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a commit tool.
func TestCommitsRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ operation, arguments string }{
		{commitsList.ID, `{}`},
		{commitsGet.ID, `{"ref":"main"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.operation, "planning", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s on a project-scoped connection = %v, want an invalid request", tt.operation, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on a project-scoped connection reached the credential or GitHub", tt.operation)
		}
	}
}

func TestSinceUntilBoundsNormalizesDatesAndValidatesOrder(t *testing.T) {
	since, until, err := sinceUntilBounds("2026-01-01", "2026-01-02")
	if err != nil || since != "2026-01-01T00:00:00Z" || until != "2026-01-02T23:59:59Z" {
		t.Fatalf("sinceUntilBounds(dates) = %q, %q, %v", since, until, err)
	}
	since, until, err = sinceUntilBounds("2026-01-01T10:00:00Z", "")
	if err != nil || since != "2026-01-01T10:00:00Z" || until != "" {
		t.Fatalf("sinceUntilBounds(time, empty) = %q, %q, %v", since, until, err)
	}
	if _, _, err := sinceUntilBounds("2026-01-02", "2026-01-01"); !isInvalidRequest(err) {
		t.Errorf("sinceUntilBounds(after until) = %v, want an invalid request", err)
	}
	if _, _, err := sinceUntilBounds("garbage", ""); !isInvalidRequest(err) {
		t.Errorf("sinceUntilBounds(garbage) = %v, want an invalid request", err)
	}
}
