package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeGists answers the gist routes. Gist aa11 belongs to octocat, bb22 to hubot; the token belongs to octocat.
type fakeGists struct{ f *fakeGitHub }

func gistBody(id, owner string) string {
	ownerJSON := "null"
	if owner != "" {
		ownerJSON = fmt.Sprintf(`{"login":%q}`, owner)
	}
	return fmt.Sprintf(`{"id":%q,"description":%q,"public":false,"created_at":"2026-01-01T00:00:00Z",`+
		`"updated_at":"2026-01-02T00:00:00Z","html_url":"https://gist.github.com/%s/%s","owner":%s,"files":{`+
		`"a.txt":{"filename":"a.txt","type":"text/plain","language":"Text","size":30000,"content":%q},`+
		`"b.bin":{"filename":"b.bin","type":"application/octet-stream","size":4,"encoding":"base64","content":"AAEC"}}}`,
		id, "desc "+strings.Repeat("d", gistDescriptionLimit+10), owner, id, ownerJSON, strings.Repeat("x", 30000))
}

func (g *fakeGists) route(w http.ResponseWriter, r *http.Request) bool {
	path, ok := strings.CutPrefix(r.URL.Path, notificationPrefix)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case path == "/user":
		fmt.Fprint(w, `{"login":"octocat","type":"User"}`)
	case r.Method == http.MethodGet && (path == "/gists" || path == "/users/octocat/gists"):
		w.Header().Set("Link", `<https://api.github.com/gists?page=2>; rel="next"`)
		fmt.Fprintf(w, "[%s]", gistBody("aa11", "octocat"))
	case r.Method == http.MethodPost && path == "/gists":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, gistBody("cc33", "octocat"))
	case strings.HasPrefix(path, "/gists/"):
		id := strings.TrimPrefix(path, "/gists/")
		switch {
		case id == "aa11" && r.Method == http.MethodGet, id == "aa11" && r.Method == http.MethodPatch:
			fmt.Fprint(w, gistBody("aa11", "octocat"))
		case id == "bb22" && r.Method == http.MethodGet:
			fmt.Fprint(w, gistBody("bb22", "hubot"))
		case id == "aa11" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		return false
	}
	return true
}

func gistRig(t *testing.T) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = (&fakeGists{f: f}).route
	cfg := coreConfig(serve(t, f))
	all := config.Permissions()
	add := func(name string, targets []string, tools ...string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: all, Tools: tools}
	}
	add("open", nil)
	all5 := []string{gistsList.ID, gistsGet.ID, gistsCreate.ID, gistsUpdate.ID, gistsDelete.ID}
	add("open-listed", nil, all5...)
	add("me", []string{"users/octocat"}, all5...)
	add("other", []string{"users/hubot"}, all5...)
	add("org", []string{"orgs/octo-org"})
	add("repo", []string{repoTarget})
	add("mixed", []string{"users/octocat", repoTarget})
	red := &redact.Redactor{}
	reads := 0
	return f, application.New(registry(t), cfg, resolver(red, &reads), red), &reads
}

func TestGistsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, core, _ := gistRig(t)

	list, err := invoke(t, core, gistsList.ID, "open", `{"since":"2026-01-01","limit":5}`, false)
	if err != nil || !strings.Contains(string(list), `"id":"aa11"`) || !strings.Contains(string(list), `"has_more":true`) ||
		!strings.Contains(string(list), `"description_truncated":true`) || strings.Contains(string(list), "xxxx") ||
		strings.Contains(string(list), `"content"`) {
		t.Fatalf("list = %s, %v", list, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.path != notificationPrefix+"/gists" ||
		!strings.Contains(last.query, "since=2026-01-01T00%3A00%3A00Z") || !strings.Contains(last.query, "per_page=5") {
		t.Errorf("list request = %+v", last)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(list, &page)
	if _, err := invoke(t, core, gistsList.ID, "open", `{"since":"2026-01-01","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("next batch = %v", err)
	}
	if _, err := invoke(t, core, gistsList.ID, "open", `{"username":"octocat","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("foreign cursor = %v, want an invalid request", err)
	}
	if _, err := invoke(t, core, gistsList.ID, "open", `{"username":"octocat"}`, false); err != nil ||
		f.recorded()[len(f.recorded())-1].path != notificationPrefix+"/users/octocat/gists" {
		t.Errorf("user list = %v", err)
	}

	got, err := invoke(t, core, gistsGet.ID, "open", `{"gist_id":"aa11"}`, false)
	if err != nil || !strings.Contains(string(got), `"content_truncated":true`) || !strings.Contains(string(got), `"binary":true`) ||
		strings.Contains(string(got), strings.Repeat("x", gistFileLimit+1)) || strings.Contains(string(got), "AAEC") {
		t.Fatalf("get = %s, %v", got, err)
	}

	before := len(f.recorded())
	created, err := invoke(t, core, gistsCreate.ID, "open", `{"description":"n","files":[{"filename":"n.md","content":"hi"}]}`, true)
	requests := sent(f, before)
	if err != nil || !strings.Contains(string(created), `"id":"cc33"`) || len(requests) != 1 ||
		requests[0].method != http.MethodPost || requests[0].body["public"] != false {
		t.Errorf("create = %s, %v, %+v", created, err, requests)
	}

	before = len(f.recorded())
	updated, err := invoke(t, core, gistsUpdate.ID, "open",
		`{"gist_id":"aa11","description":"","files":[{"filename":"a.txt","new_filename":"c.txt"}],"remove_files":["old.txt"]}`, true)
	requests = sent(f, before)
	files, _ := requests[0].body["files"].(map[string]any)
	if err != nil || len(requests) != 1 || requests[0].method != http.MethodPatch || files["old.txt"] != nil ||
		files["a.txt"] == nil || requests[0].body["description"] != "" {
		t.Errorf("update = %s, %v, %+v", updated, err, requests)
	}

	before = len(f.recorded())
	deleted, err := invoke(t, core, gistsDelete.ID, "open-listed", `{"gist_id":"aa11"}`, true)
	requests = sent(f, before)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) || len(requests) != 1 ||
		requests[0].method != http.MethodDelete {
		t.Errorf("delete = %s, %v, %+v", deleted, err, requests)
	}
}

func TestGistChangesNeedConfirmationAndDeleteIsListedOnly(t *testing.T) {
	f, core, _ := gistRig(t)
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, call := range []struct{ tool, connection, arguments string }{
		{gistsCreate.ID, "open", `{"files":[{"filename":"n.md","content":"hi"}]}`},
		{gistsUpdate.ID, "open", `{"gist_id":"aa11","description":"x"}`},
		{gistsDelete.ID, "open-listed", `{"gist_id":"aa11"}`},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, call.tool, call.connection, call.arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", call.tool, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", call.tool)
		}
	}
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, gistsDelete.ID, "open", `{"gist_id":"aa11"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("delete without a tools list = %v, want unsupported-capability", err)
	}
}

func TestGistTargetsBindTheToolsToUsers(t *testing.T) {
	f, core, reads := gistRig(t)
	// Targets without a user, or with a repository, refuse before any secret access.
	for _, connection := range []string{"org", "repo", "mixed"} {
		for _, call := range []struct{ tool, arguments string }{
			{gistsList.ID, `{}`}, {gistsGet.ID, `{"gist_id":"aa11"}`},
			{gistsCreate.ID, `{"files":[{"filename":"n.md","content":"hi"}]}`},
			{gistsUpdate.ID, `{"gist_id":"aa11","description":"x"}`},
		} {
			before := *reads
			if _, err := invoke(t, core, call.tool, connection, call.arguments, true); !isInvalidRequest(err) {
				t.Errorf("%s on %s = %v, want an invalid request", call.tool, connection, err)
			}
			if *reads != before {
				t.Errorf("%s on %s read a secret", call.tool, connection)
			}
		}
	}
	// A user outside the targets is refused before any secret access.
	before := *reads
	if _, err := invoke(t, core, gistsList.ID, "me", `{"username":"hubot"}`, false); !isInvalidRequest(err) || *reads != before {
		t.Errorf("list of a foreign user = %v", err)
	}
	// A foreign gist is not found and nothing changes.
	for _, tool := range []struct{ id, arguments string }{
		{gistsGet.ID, `{"gist_id":"bb22"}`}, {gistsUpdate.ID, `{"gist_id":"bb22","description":"x"}`},
		{gistsDelete.ID, `{"gist_id":"bb22"}`},
	} {
		before := len(f.recorded())
		_, err := invoke(t, core, tool.id, "me", tool.arguments, true)
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassNotFound {
			t.Errorf("%s of a foreign gist = %v, want not-found", tool.id, err)
		}
		for _, request := range sent(f, before) {
			if request.method != http.MethodGet {
				t.Errorf("%s changed a foreign gist: %+v", tool.id, request)
			}
		}
	}
	// The account behind the token must be a user target for the own list and for a create.
	before = len(f.recorded())
	if _, err := invoke(t, core, gistsCreate.ID, "other", `{"files":[{"filename":"n.md","content":"hi"}]}`, true); !isInvalidRequest(err) {
		t.Errorf("create for a foreign account = %v", err)
	}
	if _, err := invoke(t, core, gistsList.ID, "other", `{}`, false); !isInvalidRequest(err) {
		t.Errorf("own list for a foreign account = %v", err)
	}
	for _, request := range sent(f, before) {
		if request.method != http.MethodGet {
			t.Errorf("a refused call sent %+v", request)
		}
	}
	// The own gists, and a gist of the user, pass.
	if _, err := invoke(t, core, gistsList.ID, "me", `{}`, false); err != nil {
		t.Errorf("own list = %v", err)
	}
	if _, err := invoke(t, core, gistsGet.ID, "me", `{"gist_id":"aa11"}`, false); err != nil {
		t.Errorf("own gist = %v", err)
	}
	if _, err := invoke(t, core, gistsDelete.ID, "me", `{"gist_id":"aa11"}`, true); err != nil {
		t.Errorf("own delete = %v", err)
	}
}
