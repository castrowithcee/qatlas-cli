package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeRepositories answers the repository create, fork, delete, and collaborators routes, through the
// failure hook of fakeGitHub, and keeps the repositories it created or forked. A change reads its body from
// the request fakeGitHub already recorded, since fakeGitHub's own ServeHTTP has already drained it by the
// time the failure hook runs.
type fakeRepositories struct {
	mu            sync.Mutex
	f             *fakeGitHub
	created       map[string]bool // "owner/name" -> exists, for the 422 "already exists" case
	deleted       []string
	collaborators []map[string]any
}

func serveRepositories(t *testing.T) (*fakeGitHub, *fakeRepositories, string) {
	t.Helper()
	r := &fakeRepositories{created: map[string]bool{"octo-org/example": true}}
	f := &fakeGitHub{}
	r.f = f
	f.failure = r.route
	return f, r, serve(t, f)
}

func (r *fakeRepositories) route(w http.ResponseWriter, req *http.Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	var body map[string]any
	if requests := r.f.recorded(); req.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodPost && req.URL.Path == "/api/v3/user/repos":
		name, _ := body["name"].(string)
		full := "octocat/" + name
		if r.created[full] {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"message":"name already exists on this account"}]}`)
			return true
		}
		r.created[full] = true
		private, _ := body["private"].(bool)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"full_name":%q,"private":%t,"default_branch":"main",`+
			`"html_url":"https://github.com/%s","owner":{"login":"octocat","type":"User"}}`, full, private, full)
	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v3/orgs/") && strings.HasSuffix(req.URL.Path, "/repos"):
		org := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v3/orgs/"), "/repos")
		name, _ := body["name"].(string)
		full := org + "/" + name
		if r.created[full] {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"message":"name already exists on this account"}]}`)
			return true
		}
		r.created[full] = true
		private, _ := body["private"].(bool)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"full_name":%q,"private":%t,"default_branch":"main",`+
			`"html_url":"https://github.com/%s","owner":{"login":%q,"type":"Organization"}}`, full, private, full, org)
	case req.Method == http.MethodPost && req.URL.Path == "/api/v3/repos/octo-org/example/forks":
		owner := "octocat"
		if org, ok := body["organization"].(string); ok && org != "" {
			owner = org
		}
		name := "example"
		if given, ok := body["name"].(string); ok && given != "" {
			name = given
		}
		full := owner + "/" + name
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"full_name":%q,"private":true,"html_url":"https://github.com/%s"}`, full, full)
	case req.Method == http.MethodDelete && req.URL.Path == "/api/v3/repos/octo-org/example":
		r.deleted = append(r.deleted, "octo-org/example")
		w.WriteHeader(http.StatusNoContent)
	case req.Method == http.MethodGet && req.URL.Path == "/api/v3/repos/octo-org/example/collaborators":
		if len(r.collaborators) == 0 {
			r.collaborators = []map[string]any{
				{"login": "octocat", "type": "User", "role_name": "admin", "email": "octocat@example.invalid",
					"permissions": map[string]any{"pull": true, "triage": true, "push": true, "maintain": true, "admin": true}},
				{"login": "hubot", "type": "User", "role_name": "read", "email": "hubot@example.invalid",
					"permissions": map[string]any{"pull": true, "triage": false, "push": false, "maintain": false, "admin": false}},
			}
		}
		entries := make([]string, 0, len(r.collaborators))
		for _, c := range r.collaborators {
			data, _ := json.Marshal(c)
			entries = append(entries, string(data))
		}
		fmt.Fprint(w, "["+strings.Join(entries, ",")+"]")
	default:
		return false
	}
	return true
}

// repositoriesConfig binds every shape of connection the repository lifecycle tools distinguish: a
// repository connection, one scoped to an organization owner target, one with no targets at all (the
// token's own account), one scoped only to a repository pattern (no owner target, so an omitted owner or
// organization is refused before IO), and one that lists github.repositories.delete.
func repositoriesConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{repositoriesDelete.ID}}
	cfg.Connections["org"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all}
	cfg.Connections["repo-org"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{repoTarget, "orgs/octo-org"}, Permissions: all}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	cfg.Connections["pattern"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/*"}, Permissions: all}
	return cfg
}

// Every repository lifecycle and collaborators tool satisfies its output contract through the application
// core once confirmed.
func TestRepositoriesToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, fr, base := serveRepositories(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), repositoriesConfig(base), resolver(red, nil), red)

	// Without owner, create lands under the token's own account.
	created, err := invoke(t, core, repositoriesCreate.ID, "open", `{"name":"new-repo"}`, true)
	if err != nil || !strings.Contains(string(created), `"repository":"octocat/new-repo"`) ||
		!strings.Contains(string(created), `"owner":"users/octocat"`) || !strings.Contains(string(created), `"private":true`) {
		t.Fatalf("create under the token account = %s, %v", created, err)
	}

	// With owner, create lands under the named organization, which the "org" connection allows as its one
	// owner target.
	createdOrg, err := invoke(t, core, repositoriesCreate.ID, "org",
		`{"owner":"orgs/octo-org","name":"new-repo","private":false}`, true)
	if err != nil || !strings.Contains(string(createdOrg), `"repository":"octo-org/new-repo"`) ||
		!strings.Contains(string(createdOrg), `"owner":"orgs/octo-org"`) || !strings.Contains(string(createdOrg), `"private":false`) {
		t.Fatalf("create under an organization = %s, %v", createdOrg, err)
	}

	// A repeated create of the same name is refused with a clear message, without a second attempt.
	before := len(f.recorded())
	if _, err := invoke(t, core, repositoriesCreate.ID, "org", `{"owner":"orgs/octo-org","name":"new-repo"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "already holds a repository named new-repo") {
		t.Errorf("a repeated create = %v, want an invalid request naming it", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one refused create, no retry", requests)
	}

	// Fork into the token's own account on a connection without targets, then into an explicit organization
	// on a connection that allows both the source repository and the organization.
	forked, err := invoke(t, core, repositoriesFork.ID, "open", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(forked), `"fork":"octocat/example"`) ||
		!strings.Contains(string(forked), `"accepted":true`) || !strings.Contains(string(forked), `"repository":"octo-org/example"`) {
		t.Fatalf("fork into the token account = %s, %v", forked, err)
	}
	forkedOrg, err := invoke(t, core, repositoriesFork.ID, "repo-org", `{"organization":"orgs/octo-org","name":"renamed"}`, true)
	if err != nil || !strings.Contains(string(forkedOrg), `"fork":"octo-org/renamed"`) {
		t.Fatalf("fork into an organization = %s, %v", forkedOrg, err)
	}

	// Collaborators are listed with login, type, role, and permissions, never an email address.
	collaborators, err := invoke(t, core, collaboratorsList.ID, "repo", `{}`, false)
	if err != nil || !strings.Contains(string(collaborators), `"login":"octocat"`) ||
		!strings.Contains(string(collaborators), `"role_name":"admin"`) ||
		!strings.Contains(string(collaborators), `"maintain":true`) ||
		strings.Contains(string(collaborators), "email") || strings.Contains(string(collaborators), "example.invalid") {
		t.Fatalf("collaborators = %s, %v", collaborators, err)
	}

	// delete is offered only where a connection lists it, and confirm_name must repeat the repository exactly.
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, repositoriesDelete.ID, "repo", `{"confirm_name":"octo-org/example"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("delete without a tools list = %v, want unsupported-capability", err)
	}
	before = len(f.recorded())
	if _, err := invoke(t, core, repositoriesDelete.ID, "repo-listed", `{"confirm_name":"octo-org/other"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "octo-org/example") {
		t.Errorf("a mismatched confirm_name = %v, want an invalid request naming the repository", err)
	}
	if len(f.recorded()) != before {
		t.Error("a mismatched confirm_name reached GitHub")
	}
	deleted, err := invoke(t, core, repositoriesDelete.ID, "repo-listed", `{"confirm_name":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) {
		t.Fatalf("delete = %s, %v", deleted, err)
	}
	if len(fr.deleted) != 1 {
		t.Errorf("deleted = %v, want one delete sent to GitHub", fr.deleted)
	}
}

// A connection scoped only to a repository pattern names no owner target, so create and fork refuse to fall
// back to the token's own account: the account behind the token may belong to a different customer than the
// one the pattern names.
func TestRepositoriesCreateAndForkRefuseAccountWideDestinationBeforeIO(t *testing.T) {
	f, _, base := serveRepositories(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), repositoriesConfig(base), resolver(red, &reads), red)

	before := len(f.recorded())
	if _, err := invoke(t, core, repositoriesCreate.ID, "pattern", `{"name":"new-repo"}`, true); !isInvalidRequest(err) {
		t.Errorf("create without owner on a repository-scoped connection = %v, want an invalid request", err)
	}
	if _, err := invoke(t, core, repositoriesFork.ID, "pattern", `{"repository":"octo-org/example"}`, true); !isInvalidRequest(err) {
		t.Errorf("fork without organization on a repository-scoped connection = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("a refused create or fork reached the credential or GitHub")
	}

	// An organization outside the targets is refused the same way, whatever connection is used.
	if _, err := invoke(t, core, repositoriesCreate.ID, "org", `{"owner":"orgs/other-org","name":"x"}`, true); !isInvalidRequest(err) {
		t.Errorf("an organization outside the targets = %v, want an invalid request", err)
	}
	// A user owner is refused for create: GitHub creates a repository only under the token's own account or
	// an organization, never under an arbitrary other user.
	if _, err := invoke(t, core, repositoriesCreate.ID, "open", `{"owner":"users/octocat","name":"x"}`, true); !isInvalidRequest(err) {
		t.Errorf("a user owner = %v, want an invalid request", err)
	}
}

// Every check of these tools runs before a credential is resolved.
func TestRepositoriesArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveRepositories(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), repositoriesConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		id, connection, arguments string
	}{
		{repositoriesCreate.ID, "open", `{"name":"../x"}`},
		{repositoriesCreate.ID, "open", `{"name":"x","description":"bad\x00text"}`},
		{repositoriesFork.ID, "repo", `{"name":"../x"}`},
		{repositoriesDelete.ID, "repo-listed", `{}`},
		{collaboratorsList.ID, "repo", `{"affiliation":"invalid"}`},
		{collaboratorsList.ID, "repo", `{"permission":"invalid"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.id, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s %s reached the credential or GitHub", tt.id, tt.arguments)
		}
	}
}
