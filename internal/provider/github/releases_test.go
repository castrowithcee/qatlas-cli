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

// fakeReleaseAsset is one asset attached to a fake release, metadata only.
type fakeReleaseAsset struct {
	id                               int64
	name, label, contentType, digest string
	size, downloads                  int64
	createdAt, updatedAt             string
}

// fakeRelease is one release of the fake repository.
type fakeRelease struct {
	id                      int64
	tag, target, name, body string
	draft, prerelease       bool
	author                  string
	createdAt, publishedAt  string
	makeLatest              string
	assets                  []fakeReleaseAsset
}

// fakeReleases answers the release routes of the bound repository through the failure hook of fakeGitHub.
type fakeReleases struct {
	f        *fakeGitHub
	releases []fakeRelease
	seq      int64
}

func serveReleases(t *testing.T) (*fakeGitHub, *fakeReleases, string) {
	t.Helper()
	r := &fakeReleases{}
	f := &fakeGitHub{failure: r.route}
	r.f = f
	return f, r, serve(t, f)
}

func (r *fakeReleases) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || !strings.HasPrefix(rest, "releases") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && rest == "releases":
		r.list(w, req)
	case req.Method == http.MethodPost && rest == "releases":
		r.create(w)
	case req.Method == http.MethodGet && rest == "releases/latest":
		r.getLatest(w)
	case req.Method == http.MethodGet && strings.HasPrefix(rest, "releases/tags/"):
		r.getByTag(w, strings.TrimPrefix(rest, "releases/tags/"))
	case req.Method == http.MethodGet && strings.HasSuffix(rest, "/assets"):
		r.listAssets(w, req, rest)
	case req.Method == http.MethodPatch && strings.HasPrefix(rest, "releases/"):
		r.update(w, rest)
	case req.Method == http.MethodDelete && strings.HasPrefix(rest, "releases/"):
		r.delete(w, rest)
	case req.Method == http.MethodGet && strings.HasPrefix(rest, "releases/"):
		r.get(w, rest)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

func (r *fakeReleases) lastBody() map[string]any {
	requests := r.f.recorded()
	if len(requests) == 0 {
		return nil
	}
	return requests[len(requests)-1].body
}

func (r *fakeReleases) index(id int64) int {
	for i, release := range r.releases {
		if release.id == id {
			return i
		}
	}
	return -1
}

func assetJSONOf(a fakeReleaseAsset) string {
	digest := "null"
	if a.digest != "" {
		encoded, _ := json.Marshal(a.digest)
		digest = string(encoded)
	}
	return fmt.Sprintf(`{"id":%d,"name":%q,"label":%q,"size":%d,"content_type":%q,"digest":%s,`+
		`"download_count":%d,"created_at":%q,"updated_at":%q}`, a.id, a.name, a.label, a.size, a.contentType,
		digest, a.downloads, a.createdAt, a.updatedAt)
}

func releaseJSONOf(release fakeRelease) string {
	name := "null"
	if release.name != "" {
		encoded, _ := json.Marshal(release.name)
		name = string(encoded)
	}
	published := "null"
	if release.publishedAt != "" {
		encoded, _ := json.Marshal(release.publishedAt)
		published = string(encoded)
	}
	assets := make([]string, len(release.assets))
	for i, asset := range release.assets {
		assets[i] = assetJSONOf(asset)
	}
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"target_commitish":%q,"name":%s,"body":%q,"draft":%v,`+
		`"prerelease":%v,"author":{"login":%q},"assets":[%s],"created_at":%q,"published_at":%s,`+
		`"html_url":"https://github.com/octo-org/example/releases/tag/%s"}`,
		release.id, release.tag, release.target, name, release.body, release.draft, release.prerelease,
		release.author, strings.Join(assets, ","), release.createdAt, published, release.tag)
}

func (r *fakeReleases) list(w http.ResponseWriter, req *http.Request) {
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(req.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(r.releases))
	end := min(start+perPage, len(r.releases))
	entries := make([]string, 0, end-start)
	for _, release := range r.releases[start:end] {
		entries = append(entries, releaseJSONOf(release))
	}
	if end < len(r.releases) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
}

func (r *fakeReleases) get(w http.ResponseWriter, rest string) {
	id, err := strconv.ParseInt(strings.TrimPrefix(rest, "releases/"), 10, 64)
	if err == nil {
		if idx := r.index(id); idx >= 0 {
			fmt.Fprint(w, releaseJSONOf(r.releases[idx]))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// getByTag mirrors GitHub: a lookup by tag never finds a draft, because GitHub does not index a draft's tag.
func (r *fakeReleases) getByTag(w http.ResponseWriter, tag string) {
	tag, _ = url.PathUnescape(tag)
	for _, release := range r.releases {
		if release.tag == tag && !release.draft {
			fmt.Fprint(w, releaseJSONOf(release))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (r *fakeReleases) getLatest(w http.ResponseWriter) {
	for i := len(r.releases) - 1; i >= 0; i-- {
		if !r.releases[i].draft && !r.releases[i].prerelease {
			fmt.Fprint(w, releaseJSONOf(r.releases[i]))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (r *fakeReleases) listAssets(w http.ResponseWriter, req *http.Request, rest string) {
	digits := strings.TrimSuffix(strings.TrimPrefix(rest, "releases/"), "/assets")
	id, err := strconv.ParseInt(digits, 10, 64)
	idx := -1
	if err == nil {
		idx = r.index(id)
	}
	if idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	assets := r.releases[idx].assets
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(req.URL.Query().Get("page"))
	start := min((pageNumber-1)*perPage, len(assets))
	end := min(start+perPage, len(assets))
	entries := make([]string, 0, end-start)
	for _, asset := range assets[start:end] {
		entries = append(entries, assetJSONOf(asset))
	}
	if end < len(assets) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
}

func (r *fakeReleases) create(w http.ResponseWriter) {
	body := r.lastBody()
	tag, _ := body["tag_name"].(string)
	for _, release := range r.releases {
		if release.tag == tag {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists",`+
				`"field":"tag_name"}]}`)
			return
		}
	}
	r.seq++
	release := fakeRelease{id: 900 + r.seq, tag: tag, author: "octocat", createdAt: "2026-01-01T00:00:00Z"}
	if v, ok := body["target_commitish"].(string); ok {
		release.target = v
	}
	if v, ok := body["name"].(string); ok {
		release.name = v
	}
	if v, ok := body["body"].(string); ok {
		release.body = v
	}
	if v, ok := body["draft"].(bool); ok {
		release.draft = v
	}
	if v, ok := body["prerelease"].(bool); ok {
		release.prerelease = v
	}
	if v, ok := body["make_latest"].(string); ok {
		release.makeLatest = v
	}
	if !release.draft {
		release.publishedAt = "2026-01-01T00:00:00Z"
	}
	r.releases = append(r.releases, release)
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, releaseJSONOf(release))
}

func (r *fakeReleases) update(w http.ResponseWriter, rest string) {
	id, err := strconv.ParseInt(strings.TrimPrefix(rest, "releases/"), 10, 64)
	idx := -1
	if err == nil {
		idx = r.index(id)
	}
	if idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := r.lastBody()
	release := &r.releases[idx]
	if v, ok := body["name"].(string); ok {
		release.name = v
	}
	if v, ok := body["body"].(string); ok {
		release.body = v
	}
	if v, ok := body["tag_name"].(string); ok {
		release.tag = v
	}
	if v, ok := body["target_commitish"].(string); ok {
		release.target = v
	}
	if v, ok := body["draft"].(bool); ok {
		wasDraft := release.draft
		release.draft = v
		if wasDraft && !v {
			release.publishedAt = "2026-01-05T00:00:00Z"
		}
	}
	if v, ok := body["prerelease"].(bool); ok {
		release.prerelease = v
	}
	if v, ok := body["make_latest"].(string); ok {
		release.makeLatest = v
	}
	fmt.Fprint(w, releaseJSONOf(*release))
}

func (r *fakeReleases) delete(w http.ResponseWriter, rest string) {
	id, err := strconv.ParseInt(strings.TrimPrefix(rest, "releases/"), 10, 64)
	idx := -1
	if err == nil {
		idx = r.index(id)
	}
	if idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	r.releases = append(r.releases[:idx], r.releases[idx+1:]...)
	w.WriteHeader(http.StatusNoContent)
}

func TestReleasesProfileOffersFiveToolsWithoutDeleteAndIsNotRecommended(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var profile config.ToolProfile
	found := false
	for _, candidate := range metadata.Profiles {
		if candidate.ID == "releases" {
			profile, found = candidate, true
		}
	}
	if !found || profile.Recommended {
		t.Fatalf("profile = %+v, found=%v, want an existing, not-recommended profile", profile, found)
	}
	want := []string{releasesList.ID, releasesGet.ID, releaseAssetsList.ID, releasesCreate.ID, releasesUpdate.ID}
	if strings.Join(profile.Tools, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", profile.Tools, want)
	}
	for _, id := range profile.Tools {
		if id == releasesDelete.ID {
			t.Error("the not-recommended profile selects delete, which every profile must leave out")
		}
	}
}

// The list is paged by GitHub's Link header, since the plain array route carries no total count.
func TestReleasesAreListedAndPagedByLinkHeader(t *testing.T) {
	_, r, base := serveReleases(t)
	for i := 1; i <= 35; i++ {
		r.releases = append(r.releases, fakeRelease{id: int64(i), tag: fmt.Sprintf("v1.%d.0", i), name: "x",
			author: "octocat", createdAt: "2026-01-01T00:00:00Z", publishedAt: "2026-01-01T00:00:00Z"})
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	type page struct {
		Releases []struct {
			ID  int64  `json:"id"`
			Tag string `json:"tag"`
		} `json:"releases"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	first, err := invoke(t, core, "github.releases.list", "repo", `{"limit":30}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var firstPage page
	if err := json.Unmarshal(first, &firstPage); err != nil || len(firstPage.Releases) != 30 || !firstPage.HasMore {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	second, err := invoke(t, core, "github.releases.list", "repo", `{"cursor":"`+firstPage.NextCursor+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var secondPage page
	if err := json.Unmarshal(second, &secondPage); err != nil || len(secondPage.Releases) != 5 || secondPage.HasMore {
		t.Fatalf("second batch = %s, %v", second, err)
	}
	if firstPage.Releases[0].ID != 1 || secondPage.Releases[4].ID != 35 {
		t.Errorf("ids = %+v / %+v, want every release once in order", firstPage, secondPage)
	}
}

// Exactly one of id, tag, or latest must be given, and a lookup by tag never finds a draft.
func TestGetReleaseSelectsExactlyOneSelectorAndDraftsAreNotFoundByTag(t *testing.T) {
	_, r, base := serveReleases(t)
	r.releases = append(r.releases,
		fakeRelease{id: 1, tag: "v1.0.0", name: "First", body: bodyCanary, author: "octocat",
			target: "main", createdAt: "2026-01-01T00:00:00Z", publishedAt: "2026-01-01T00:00:00Z",
			assets: []fakeReleaseAsset{{id: 11, name: "a.tar.gz", size: 100, downloads: 3}}},
		fakeRelease{id: 2, tag: "v2.0.0-draft", name: "Draft", draft: true, author: "octocat",
			createdAt: "2026-01-02T00:00:00Z"},
	)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.releases.get", "repo", `{"id":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID              int64  `json:"id"`
		Tag             string `json:"tag"`
		Body            string `json:"body"`
		TargetCommitish string `json:"target_commitish"`
		AssetsCount     int    `json:"assets_count"`
	}
	if err := json.Unmarshal(result, &got); err != nil || got.ID != 1 || got.Tag != "v1.0.0" || got.Body != bodyCanary ||
		got.TargetCommitish != "main" || got.AssetsCount != 1 {
		t.Fatalf("get by id = %s, %v", result, err)
	}

	if result, err := invoke(t, core, "github.releases.get", "repo", `{"tag":"v1.0.0"}`, false); err != nil {
		t.Fatalf("get by tag = %v", err)
	} else if !strings.Contains(string(result), `"id":1`) {
		t.Errorf("get by tag = %s, want release 1", result)
	}

	if result, err := invoke(t, core, "github.releases.get", "repo", `{"latest":true}`, false); err != nil {
		t.Fatalf("get latest = %v", err)
	} else if !strings.Contains(string(result), `"id":1`) {
		t.Errorf("get latest = %s, want release 1", result)
	}

	// A lookup by the draft's tag finds nothing, because GitHub does not index a draft's tag.
	if _, err := invoke(t, core, "github.releases.get", "repo", `{"tag":"v2.0.0-draft"}`, false); classOf(err) != provider.ClassNotFound {
		t.Errorf("get draft by tag = %v, want not found", err)
	}

	for _, tt := range []struct{ name, arguments string }{
		{"none given", `{}`},
		{"id and tag", `{"id":1,"tag":"v1.0.0"}`},
		{"id and latest", `{"id":1,"latest":true}`},
		{"tag and latest", `{"tag":"v1.0.0","latest":true}`},
	} {
		if _, err := invoke(t, core, "github.releases.get", "repo", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", tt.name, err)
		}
	}
}

func TestReleaseAssetsAreListed(t *testing.T) {
	_, r, base := serveReleases(t)
	r.releases = append(r.releases, fakeRelease{id: 5, tag: "v1.0.0", assets: []fakeReleaseAsset{
		{id: 1, name: "a.tar.gz", label: "Archive", size: 1024, contentType: "application/gzip",
			digest: "sha256:abc", downloads: 7, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z"},
		{id: 2, name: "b.zip", size: 2048},
	}})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, "github.releaseassets.list", "repo", `{"id":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Assets []struct {
			ID            int64  `json:"id"`
			Name          string `json:"name"`
			Label         string `json:"label"`
			SizeBytes     int64  `json:"size_bytes"`
			ContentType   string `json:"content_type"`
			Digest        string `json:"digest"`
			DownloadCount int64  `json:"download_count"`
		} `json:"assets"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Assets) != 2 || got.HasMore {
		t.Fatalf("assets = %s, %v", result, err)
	}
	if got.Assets[0].Name != "a.tar.gz" || got.Assets[0].Label != "Archive" || got.Assets[0].SizeBytes != 1024 ||
		got.Assets[0].ContentType != "application/gzip" || got.Assets[0].Digest != "sha256:abc" ||
		got.Assets[0].DownloadCount != 7 {
		t.Errorf("asset[0] = %+v", got.Assets[0])
	}
}

// Create sends one request; a repeated tag is refused with a clear message and no second attempt. Update
// changes only the given fields and publishes a draft with draft: false.
func TestReleaseCreateRefusesADuplicateTagAndUpdatePublishesADraft(t *testing.T) {
	f, _, base := serveReleases(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	before := len(f.recorded())
	created, err := invoke(t, core, "github.releases.create", "repo",
		`{"tag":"v1.0.0","target":"main","name":"v1.0.0","draft":true,"generate_notes":true,"make_latest":"false"}`,
		true)
	if err != nil {
		t.Fatal(err)
	}
	var createdRelease struct {
		ID    int64  `json:"id"`
		Draft bool   `json:"draft"`
		Tag   string `json:"tag"`
	}
	if err := json.Unmarshal(created, &createdRelease); err != nil || !createdRelease.Draft || createdRelease.Tag != "v1.0.0" {
		t.Fatalf("created release = %s, %v", created, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodPost ||
		requests[0].body["generate_release_notes"] != true || requests[0].body["make_latest"] != "false" {
		t.Errorf("create requests = %+v, want exactly one POST with generate_release_notes and make_latest", requests)
	}

	// A second create for the same tag is refused with a clear message; no second attempt is made.
	before = len(f.recorded())
	_, err = invoke(t, core, "github.releases.create", "repo", `{"tag":"v1.0.0"}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "already holds a release for tag v1.0.0") {
		t.Errorf("duplicate create = %v, want an invalid request naming the tag", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a refused duplicate create sent %d requests, want exactly one attempt", len(requests))
	}

	id := strconv.FormatInt(createdRelease.ID, 10)
	before = len(f.recorded())
	published, err := invoke(t, core, "github.releases.update", "repo", `{"id":`+id+`,"draft":false}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var publishedRelease struct {
		Draft bool `json:"draft"`
	}
	if err := json.Unmarshal(published, &publishedRelease); err != nil || publishedRelease.Draft {
		t.Fatalf("published release = %s, %v", published, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodPatch ||
		len(requests[0].body) != 1 {
		t.Errorf("update requests = %+v, want exactly one PATCH with only draft", requests)
	}

	before = len(f.recorded())
	if _, err := invoke(t, core, "github.releases.update", "repo", `{"id":`+id+`}`, true); !isInvalidRequest(err) {
		t.Errorf("update without a field to change = %v, want an invalid request", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 0 {
		t.Errorf("a refused update sent %d requests, want none", len(requests))
	}
}

// Delete is offered only where a connection's tools list names it, and a release already deleted answers
// not found, like the other delete tools.
func TestReleaseDeleteIsListedOnlyAndTreatsAnAlreadyDeletedReleaseAsNotFound(t *testing.T) {
	f, r, base := serveReleases(t)
	r.releases = append(r.releases, fakeRelease{id: 42, tag: "v1.0.0"})
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["deleter"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionDelete},
		Tools:       []string{releasesGet.ID, releasesDelete.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)

	// A connection whose tools list does not name it neither discovers nor runs it.
	before := len(f.recorded())
	if _, err := invoke(t, core, "github.releases.delete", "repo", `{"id":42}`, true); err == nil {
		t.Error("delete on a connection without the tool listed succeeded, want unsupported")
	} else if _, ok := err.(*capability.UnsupportedError); !ok {
		t.Errorf("delete on a connection without the tool listed = %T %v, want unsupported", err, err)
	}
	if len(f.recorded()) != before {
		t.Error("an unsupported delete reached GitHub")
	}

	deleted, err := invoke(t, core, "github.releases.delete", "deleter", `{"id":42}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID      int64 `json:"id"`
		Deleted bool  `json:"deleted"`
	}
	if err := json.Unmarshal(deleted, &got); err != nil || !got.Deleted || got.ID != 42 {
		t.Fatalf("deleted = %s, %v", deleted, err)
	}

	// A release already deleted answers not found, like the other delete tools.
	_, err = invoke(t, core, "github.releases.delete", "deleter", `{"id":42}`, true)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "GitHub does not hold release 42") {
		t.Errorf("a repeated delete = %v, want not found naming the release", err)
	}
}

func TestReleaseNotFoundNamesTheSubject(t *testing.T) {
	_, r, base := serveReleases(t)
	r.releases = append(r.releases, fakeRelease{id: 1, tag: "v1.0.0"})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ operation, arguments, want string }{
		{"github.releases.get", `{"id":9}`, "release 9 in repository octo-org/example"},
		{"github.releases.get", `{"tag":"v9.0.0"}`, "release with tag v9.0.0 in repository octo-org/example"},
		{"github.releaseassets.list", `{"id":9}`, "release asset 9 in repository octo-org/example"},
	} {
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, false); classOf(err) != provider.ClassNotFound ||
			!strings.Contains(err.Error(), "GitHub does not hold "+tt.want) {
			t.Errorf("%s %s = %v, want not-found naming %s", tt.operation, tt.arguments, err, tt.want)
		}
	}
}

// Every release tool satisfies its output contract through the application core, and the token never reaches
// an answer; every change is audited as one successful change.
func TestReleasesSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	_, r, base := serveReleases(t)
	r.releases = append(r.releases, fakeRelease{id: 100, tag: "v1.0.0", name: "x", body: bodyCanary,
		author: "octocat", createdAt: "2026-01-01T00:00:00Z", publishedAt: "2026-01-01T00:00:00Z",
		assets: []fakeReleaseAsset{{id: 1, name: "a.zip", size: 10}}})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	for _, request := range []struct{ operation, arguments string }{
		{"github.releases.list", `{"limit":5}`},
		{"github.releases.get", `{"id":100}`},
		{"github.releaseassets.list", `{"id":100}`},
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

	for _, request := range []struct{ operation, arguments string }{
		{"github.releases.create", `{"tag":"v2.0.0"}`},
		{"github.releases.update", `{"id":100,"body":"` + bodyCanary + `"}`},
	} {
		result, err := invoke(t, core, request.operation, "repo", request.arguments, true)
		if err != nil {
			t.Errorf("%s %s = %v", request.operation, request.arguments, err)
			continue
		}
		if strings.Contains(string(result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.operation, result)
		}
	}
}

// Every release change is audited as one successful change through the application core.
func TestReleaseChangesSatisfyTheirContractThroughTheApplicationCoreWithAudit(t *testing.T) {
	_, r, base := serveReleases(t)
	r.releases = append(r.releases, fakeRelease{id: 200, tag: "v1.0.0"})
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["deleter"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionDelete},
		Tools:       []string{releasesGet.ID, releasesDelete.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	var audit strings.Builder
	core.SetAudit(&audit)

	for _, request := range []application.InvokeRequest{
		{Operation: "github.releases.create", Connection: "repo", Arguments: json.RawMessage(`{"tag":"v3.0.0"}`)},
		{Operation: "github.releases.update", Connection: "repo",
			Arguments: json.RawMessage(`{"id":200,"name":"First"}`)},
		{Operation: "github.releases.delete", Connection: "deleter", Arguments: json.RawMessage(`{"id":200}`)},
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
	if strings.Count(audit.String(), `"result":"success"`) != 3 {
		t.Errorf("audit = %s, want one success event per confirmed change", audit.String())
	}
}
