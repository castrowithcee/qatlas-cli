package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeBranch is one branch of the fake repository.
type fakeBranch struct {
	name string
	sha  string
	// protected keeps the tag name distinct from a bool zero value in table-driven tests below.
	protected bool
}

// fakeTagRef is one tag of the fake repository, lightweight when annotatedSHA is empty, otherwise annotated:
// the ref then points at the tag object annotatedSHA, which the Git tags API dereferences to targetSHA.
type fakeTagRef struct {
	name         string
	commitSHA    string
	annotatedSHA string
	targetSHA    string
	targetType   string
	tagger       string
	taggerDate   string
	message      string
}

// fakeRefs answers the branches, tags, and Git refs and tags routes of the bound repository through the
// failure hook of fakeGitHub.
type fakeRefs struct {
	f        *fakeGitHub
	branches []fakeBranch
	tags     []fakeTagRef
}

func serveRefs(t *testing.T) (*fakeGitHub, *fakeRefs, string) {
	t.Helper()
	r := &fakeRefs{}
	f := &fakeGitHub{failure: r.route}
	r.f = f
	return f, r, serve(t, f)
}

func (r *fakeRefs) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || !(rest == "branches" || rest == "tags" || strings.HasPrefix(rest, "git/refs/tags/") ||
		strings.HasPrefix(rest, "git/tags/")) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && rest == "branches":
		r.listBranches(w, req)
	case req.Method == http.MethodGet && rest == "tags":
		r.listTags(w, req)
	case req.Method == http.MethodGet && strings.HasPrefix(rest, "git/refs/tags/"):
		r.tagRef(w, strings.TrimPrefix(rest, "git/refs/tags/"))
	case req.Method == http.MethodGet && strings.HasPrefix(rest, "git/tags/"):
		r.tagObject(w, strings.TrimPrefix(rest, "git/tags/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

func (r *fakeRefs) listBranches(w http.ResponseWriter, req *http.Request) {
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(req.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(r.branches))
	end := min(start+perPage, len(r.branches))
	entries := make([]string, 0, end-start)
	for _, branch := range r.branches[start:end] {
		entries = append(entries, fmt.Sprintf(`{"name":%q,"protected":%t,"commit":{"sha":%q}}`,
			branch.name, branch.protected, branch.sha))
	}
	if end < len(r.branches) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (r *fakeRefs) listTags(w http.ResponseWriter, req *http.Request) {
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(req.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(r.tags))
	end := min(start+perPage, len(r.tags))
	entries := make([]string, 0, end-start)
	for _, tag := range r.tags[start:end] {
		sha := tag.commitSHA
		if sha == "" {
			sha = tag.annotatedSHA
		}
		entries = append(entries, fmt.Sprintf(`{"name":%q,"commit":{"sha":%q}}`, tag.name, sha))
	}
	if end < len(r.tags) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (r *fakeRefs) find(name string) (fakeTagRef, bool) {
	for _, tag := range r.tags {
		if tag.name == name {
			return tag, true
		}
	}
	return fakeTagRef{}, false
}

func (r *fakeRefs) tagRef(w http.ResponseWriter, tag string) {
	found, ok := r.find(tag)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return
	}
	if found.annotatedSHA != "" {
		fmt.Fprintf(w, `{"ref":"refs/tags/%s","object":{"sha":%q,"type":"tag"}}`, found.name, found.annotatedSHA)
		return
	}
	fmt.Fprintf(w, `{"ref":"refs/tags/%s","object":{"sha":%q,"type":"commit"}}`, found.name, found.commitSHA)
}

func (r *fakeRefs) tagObject(w http.ResponseWriter, sha string) {
	for _, tag := range r.tags {
		if tag.annotatedSHA == sha {
			fmt.Fprintf(w, `{"tag":%q,"sha":%q,"message":%q,"tagger":{"name":%q,"date":%q},`+
				`"object":{"sha":%q,"type":%q}}`, tag.name, tag.annotatedSHA, tag.message, tag.tagger,
				tag.taggerDate, tag.targetSHA, tag.targetType)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"message":"Not Found"}`)
}

// Branches are listed with their protection status and their latest commit SHA, paged by GitHub's Link
// header since the plain array route carries no total count; a cursor stays bound to the list.
func TestBranchesAreListedWithProtectionAndPagedByLinkHeader(t *testing.T) {
	f, r, base := serveRefs(t)
	for i := 1; i <= 35; i++ {
		r.branches = append(r.branches, fakeBranch{name: fmt.Sprintf("branch%02d", i), sha: fmt.Sprintf("sha%02d", i),
			protected: i%2 == 0})
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	type page struct {
		Branches   []Branch `json:"branches"`
		HasMore    bool     `json:"has_more"`
		NextCursor string   `json:"next_cursor"`
	}
	first, err := invoke(t, core, branchesList.ID, "repo", `{"limit":30}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var firstPage page
	if json.Unmarshal(first, &firstPage) != nil || len(firstPage.Branches) != 30 || !firstPage.HasMore {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	if firstPage.Branches[0].Name != "branch01" || firstPage.Branches[0].SHA != "sha01" || firstPage.Branches[0].Protected {
		t.Errorf("first branch = %+v", firstPage.Branches[0])
	}
	if !firstPage.Branches[1].Protected {
		t.Errorf("second branch = %+v, want protected", firstPage.Branches[1])
	}
	second, err := invoke(t, core, branchesList.ID, "repo", `{"cursor":"`+firstPage.NextCursor+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var secondPage page
	if json.Unmarshal(second, &secondPage) != nil || len(secondPage.Branches) != 5 || secondPage.HasMore {
		t.Fatalf("second batch = %s, %v", second, err)
	}
	before := len(f.recorded())
	for name, cursor := range alteredCursors(t, firstPage.NextCursor) {
		if _, err := invoke(t, core, branchesList.ID, "repo", `{"cursor":"`+cursor+`"}`, false); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
	}
	if requests := f.recorded()[before:]; len(requests) != 0 {
		t.Errorf("requests = %d, want refusals before provider I/O", len(requests))
	}
}

// Tags are listed with the commit SHA each points at, paged the same way.
func TestTagsAreListedAndPagedByLinkHeader(t *testing.T) {
	f, r, base := serveRefs(t)
	for i := 1; i <= 5; i++ {
		r.tags = append(r.tags, fakeTagRef{name: fmt.Sprintf("v0.%d.0", i), commitSHA: fmt.Sprintf("sha%02d", i)})
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, tagsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tags []TagEntry `json:"tags"`
	}
	if json.Unmarshal(result, &listed) != nil || len(listed.Tags) != 5 || listed.Tags[0].Name != "v0.1.0" ||
		listed.Tags[0].SHA != "sha01" {
		t.Fatalf("tags = %+v", listed)
	}
	if len(f.recorded()) == 0 {
		t.Fatal("no request recorded")
	}
}

// A lightweight tag resolves to its commit directly; an annotated tag is dereferenced through the Git tags
// API, with its tagger, its bounded message, and its target object.
func TestTagsGetResolvesLightweightAndAnnotatedTags(t *testing.T) {
	_, r, base := serveRefs(t)
	r.tags = append(r.tags,
		fakeTagRef{name: "v1.0.0", commitSHA: "deadbeef01"},
		fakeTagRef{name: "v2.0.0", annotatedSHA: "cafebabe02", targetSHA: "deadbeef02", targetType: "commit",
			tagger: "Ada Lovelace", taggerDate: "2026-01-01T00:00:00Z", message: "Release 2.0.0"})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, tagsGet.ID, "repo", `{"tag":"v1.0.0"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var light TagDetail
	if json.Unmarshal(result, &light) != nil || light.Type != "lightweight" || light.SHA != "deadbeef01" ||
		light.Tagger != "" || light.Message != "" {
		t.Fatalf("lightweight tag = %+v", light)
	}

	result, err = invoke(t, core, tagsGet.ID, "repo", `{"tag":"v2.0.0"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var annotated TagDetail
	if json.Unmarshal(result, &annotated) != nil || annotated.Type != "annotated" || annotated.SHA != "cafebabe02" ||
		annotated.TargetSHA != "deadbeef02" || annotated.TargetType != "commit" || annotated.Tagger != "Ada Lovelace" ||
		annotated.Message != "Release 2.0.0" {
		t.Fatalf("annotated tag = %+v", annotated)
	}

	// A long annotated tag message is cut, with the cut visible.
	r.tags = append(r.tags, fakeTagRef{name: "v3.0.0", annotatedSHA: "cafebabe03", targetSHA: "deadbeef03",
		targetType: "commit", message: strings.Repeat("z", maxTagMessageLength+50)})
	result, err = invoke(t, core, tagsGet.ID, "repo", `{"tag":"v3.0.0"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var big TagDetail
	if json.Unmarshal(result, &big) != nil || !big.MessageTruncated || len([]rune(big.Message)) != maxTagMessageLength {
		t.Fatalf("long annotated tag = %+v, want the message cut to %d characters", big, maxTagMessageLength)
	}

	// A tag GitHub does not hold is not-found, naming the tag.
	if _, err := invoke(t, core, tagsGet.ID, "repo", `{"tag":"missing"}`, false); classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), "tag missing in repository octo-org/example") {
		t.Errorf("get on a missing tag = %v, want not-found naming the tag", err)
	}
}

// tag and cursor arguments are checked before a credential is resolved, so an unusable value never reaches
// GitHub.
func TestRefsArgumentsAreValidatedBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, operation, arguments string }{
		{"missing tag", tagsGet.ID, `{}`},
		{"bad tag", tagsGet.ID, `{"tag":"bad..tag"}`},
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

// A connection whose targets name a project, not a repository, never resolves a credential for a branch or
// tag tool.
func TestRefsRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ operation, arguments string }{
		{branchesList.ID, `{}`},
		{tagsList.ID, `{}`},
		{tagsGet.ID, `{"tag":"v1.0.0"}`},
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

func TestValidObjectSHAAcceptsSevenToFortyHexCharacters(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"abcdef1", true},
		{strings.Repeat("a", 40), true},
		{"abcdef", false}, // 6 characters, one short of the minimum
		{strings.Repeat("a", 41), false},
		{"abcdefg", false}, // not hex
		{"", false},
	} {
		if got := validObjectSHA(tt.value); got != tt.want {
			t.Errorf("validObjectSHA(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}
