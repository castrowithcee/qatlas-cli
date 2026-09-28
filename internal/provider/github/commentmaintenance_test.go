package github

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeIssueComment is one issue comment the fake repository holds, addressed by its REST identifier; issue
// 42 is a plain issue and issue 7 is a pull request, both already answered by the base fake router's issue
// route, so a comment on 7 exercises the pull request refusal without any extra fixture.
type fakeIssueComment struct {
	id     int64
	number int
	body   string
}

// fakeComments answers the issue comment maintenance routes of the bound repository through the failure hook
// of fakeGitHub, the way fakeLabels answers the label routes.
type fakeComments struct {
	mu       sync.Mutex
	f        *fakeGitHub
	comments map[int64]fakeIssueComment
}

func serveCommentMaintenance(t *testing.T) (*fakeGitHub, *fakeComments, string) {
	t.Helper()
	c := &fakeComments{comments: map[int64]fakeIssueComment{
		555: {id: 555, number: 42, body: "Original comment"},
		777: {id: 777, number: 7, body: "A pull request conversation comment"},
	}}
	f := &fakeGitHub{}
	c.f = f
	f.failure = c.route
	return f, c, serve(t, f)
}

func issueCommentJSON(e fakeIssueComment) string {
	return fmt.Sprintf(`{"id":%d,"node_id":"IC_%d","user":{"login":"octocat"},"body":%q,`+
		`"created_at":"2026-01-03T00:00:00Z","updated_at":"2026-01-03T00:00:00Z",`+
		`"html_url":"https://github.com/octo-org/example/issues/%d#issuecomment-%d",`+
		`"issue_url":"https://api.github.com/repos/octo-org/example/issues/%d"}`,
		e.id, e.id, e.body, e.number, e.id, e.number)
}

func (c *fakeComments) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	idText, ok := strings.CutPrefix(rest, "issues/comments/")
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if requests := c.f.recorded(); req.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	entry, exists := c.comments[id]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	switch req.Method {
	case http.MethodGet:
		fmt.Fprint(w, issueCommentJSON(entry))
	case http.MethodPatch:
		if newBody, ok := body["body"].(string); ok {
			entry.body = newBody
			c.comments[id] = entry
		}
		fmt.Fprint(w, issueCommentJSON(entry))
	case http.MethodDelete:
		delete(c.comments, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

// commentMaintenanceConfig binds a connection with every permission and no tools list, and one that lists
// github.comments.delete, the way labelsConfig binds its own guarded tool.
func commentMaintenanceConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{commentsDelete.ID}}
	cfg.Connections["planning-full"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: all}
	return cfg
}

// Every comment maintenance tool satisfies its output contract through the application core once confirmed,
// github.comments.delete, listed only, is sent exactly once, and both refuse a comment that reads back to a
// pull request or that GitHub does not hold in the bound repository, before anything changes.
func TestCommentMaintenanceToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, _, base := serveCommentMaintenance(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), commentMaintenanceConfig(base), resolver(red, nil), red)

	updated, err := invoke(t, core, commentsUpdate.ID, "repo", `{"comment_id":555,"body":"Fixed in the latest build."}`, true)
	if err != nil || !strings.Contains(string(updated), `"body":"Fixed in the latest build."`) {
		t.Fatalf("update = %s, %v", updated, err)
	}

	// delete is offered only where a connection lists it.
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, commentsDelete.ID, "repo", `{"comment_id":555}`, true); !errors.As(err, &unsupported) {
		t.Errorf("delete without a tools list = %v, want unsupported-capability", err)
	}
	before := len(f.recorded())
	unconfirmed := &application.ConfirmationRequiredError{}
	if _, err := invoke(t, core, commentsDelete.ID, "repo-listed", `{"comment_id":555}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("delete without --confirm = %v, want confirmation-required", err)
	}
	if len(f.recorded()) != before {
		t.Error("delete without --confirm reached GitHub")
	}
	deleted, err := invoke(t, core, commentsDelete.ID, "repo-listed", `{"comment_id":555}`, true)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) || !strings.Contains(string(deleted), `"comment_id":555`) {
		t.Fatalf("delete = %s, %v", deleted, err)
	}
	if _, err := invoke(t, core, commentsUpdate.ID, "repo", `{"comment_id":555,"body":"too late"}`, true); classOf(err) != provider.ClassNotFound {
		t.Errorf("update of a deleted comment = %v, want not-found", err)
	}

	// Comment 777 reads back to issue 7, a pull request; neither tool changes it.
	before = len(f.recorded())
	if _, err := invoke(t, core, commentsUpdate.ID, "repo", `{"comment_id":777,"body":"edited"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "is a pull request") {
		t.Errorf("update of a pull request comment = %v, want an invalid request naming it", err)
	}
	if _, err := invoke(t, core, commentsDelete.ID, "repo-listed", `{"comment_id":777}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "is a pull request") {
		t.Errorf("delete of a pull request comment = %v, want an invalid request naming it", err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPatch] != 0 || rest[http.MethodDelete] != 0 {
		t.Errorf("a pull request comment was changed: %v", rest)
	}

	// A comment_id GitHub does not hold in this repository, such as one of another repository, answers
	// not-found before anything changes.
	before = len(f.recorded())
	if _, err := invoke(t, core, commentsUpdate.ID, "repo", `{"comment_id":999999,"body":"x"}`, true); classOf(err) != provider.ClassNotFound {
		t.Errorf("update of an unknown comment_id = %v, want not-found", err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPatch] != 0 {
		t.Errorf("an unknown comment was changed: %v", rest)
	}
}

// Every check of these tools runs before a credential is resolved: comment_id must be positive, and
// comments.update needs a non-empty body.
func TestCommentMaintenanceArgumentsAreValidatedBeforeIO(t *testing.T) {
	_, _, base := serveCommentMaintenance(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), commentMaintenanceConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, id, connection, arguments string
	}{
		{"missing comment_id", commentsUpdate.ID, "repo", `{"body":"x"}`},
		{"zero comment_id", commentsUpdate.ID, "repo", `{"comment_id":0,"body":"x"}`},
		{"missing body", commentsUpdate.ID, "repo", `{"comment_id":555}`},
		{"empty body", commentsUpdate.ID, "repo", `{"comment_id":555,"body":""}`},
		{"missing comment_id", commentsDelete.ID, "repo-listed", `{}`},
		{"zero comment_id", commentsDelete.ID, "repo-listed", `{"comment_id":0}`},
	} {
		reads = 0
		before := 0
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.id, tt.arguments, err)
		}
		if reads != before {
			t.Errorf("%s: %s reached the credential", tt.name, tt.id)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a comment
// maintenance tool: the target is checked before a secret is read.
func TestCommentMaintenanceRefusesANonRepositoryTargetBeforeIO(t *testing.T) {
	_, _, base := serveCommentMaintenance(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), commentMaintenanceConfig(base), resolver(red, &reads), red)

	if _, err := invoke(t, core, commentsUpdate.ID, "planning-full", `{"comment_id":555,"body":"x"}`, true); !isInvalidRequest(err) {
		t.Errorf("update on a project-scoped connection = %v, want an invalid request", err)
	}
	if reads != 0 {
		t.Error("update on a project-scoped connection reached the credential")
	}
}
