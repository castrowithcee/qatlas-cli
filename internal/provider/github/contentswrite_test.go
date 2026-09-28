package github

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeContentsWrite answers the Git refs route and the general Contents write routes (every path outside
// .github/workflows/, which fakeMaintain answers instead) of the bound repository, through the failure hook
// of fakeGitHub, and keeps their state.
type fakeContentsWrite struct {
	mu      sync.Mutex
	f       *fakeGitHub
	files   map[string]string // path -> content
	refs    map[string]string // branch name (without refs/heads/) -> commit sha
	commits map[string]string // a ref or a SHA given as "from" -> the commit sha it resolves to
	// defaultBranch, when set, answers the repository route with this default branch name.
	defaultBranch string
	// forceConflict, when set, answers every content write with a 409 regardless of the given sha, the way a
	// branch protection rule unrelated to the blob SHA would.
	forceConflict bool
}

func serveContentsWrite(t *testing.T) (*fakeGitHub, *fakeContentsWrite, string) {
	t.Helper()
	w := &fakeContentsWrite{files: map[string]string{}, refs: map[string]string{}, commits: map[string]string{}}
	f := &fakeGitHub{}
	w.f = f
	f.failure = w.route
	return f, w, serve(t, f)
}

func (w *fakeContentsWrite) route(rw http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/octo-org/example" {
		w.mu.Lock()
		branch := w.defaultBranch
		w.mu.Unlock()
		if branch == "" {
			return false
		}
		rw.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(rw, `{"full_name":"octo-org/example","default_branch":%q}`, branch)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var body map[string]any
	if requests := w.f.recorded(); r.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	rw.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "commits/"):
		ref := strings.TrimPrefix(rest, "commits/")
		sha, ok := w.commits[ref]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return true
		}
		fmt.Fprintf(rw, `{"sha":%q}`, sha)
	case r.Method == http.MethodPost && rest == "git/refs":
		ref, _ := body["ref"].(string)
		sha, _ := body["sha"].(string)
		name := strings.TrimPrefix(ref, "refs/heads/")
		if _, exists := w.refs[name]; exists {
			rw.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(rw, `{"message":"Reference already exists"}`)
			return true
		}
		w.refs[name] = sha
		rw.WriteHeader(http.StatusCreated)
		fmt.Fprintf(rw, `{"ref":%q,"object":{"sha":%q,"type":"commit"}}`, ref, sha)
	case strings.HasPrefix(rest, "contents/") && !strings.HasPrefix(rest, "contents/.github/workflows/"):
		w.contentRoute(rw, r, strings.TrimPrefix(rest, "contents/"), body)
	default:
		return false
	}
	return true
}

func (w *fakeContentsWrite) contentRoute(rw http.ResponseWriter, r *http.Request, path string, body map[string]any) {
	if w.forceConflict && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		rw.WriteHeader(http.StatusConflict)
		fmt.Fprintf(rw, `{"message":"branch protection blocks %s"}`, path)
		return
	}
	switch r.Method {
	case http.MethodGet:
		content, exists := w.files[path]
		if !exists {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return
		}
		fmt.Fprintf(rw, `{"type":"file","path":%q,"sha":%q,"size":%d,"encoding":"base64","content":%q}`,
			path, blobOf(content), len(content), base64.StdEncoding.EncodeToString([]byte(content)))
	case http.MethodPut:
		sha, _ := body["sha"].(string)
		current, exists := w.files[path]
		switch {
		case !exists && sha != "", exists && sha == "":
			rw.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(rw, `{"message":"Invalid request. \"sha\" wasn't supplied."}`)
			return
		case exists && sha != blobOf(current):
			rw.WriteHeader(http.StatusConflict)
			fmt.Fprintf(rw, `{"message":"%s does not match %s"}`, path, sha)
			return
		}
		encoded, _ := body["content"].(string)
		data, _ := base64.StdEncoding.DecodeString(encoded)
		w.files[path] = string(data)
		status := http.StatusOK
		if !exists {
			status = http.StatusCreated
		}
		rw.WriteHeader(status)
		fmt.Fprintf(rw, `{"content":{"path":%q,"sha":%q,"size":%d},`+
			`"commit":{"sha":%q,"html_url":"https://github.com/octo-org/example/commit/%s"}}`,
			path, blobOf(string(data)), len(data), commitSHA, commitSHA)
	case http.MethodDelete:
		sha, _ := body["sha"].(string)
		current, exists := w.files[path]
		if !exists {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return
		}
		if sha != blobOf(current) {
			rw.WriteHeader(http.StatusConflict)
			fmt.Fprintf(rw, `{"message":"%s does not match %s"}`, path, sha)
			return
		}
		delete(w.files, path)
		fmt.Fprintf(rw, `{"commit":{"sha":%q,"html_url":"https://github.com/octo-org/example/commit/%s"}}`,
			commitSHA, commitSHA)
	default:
		rw.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// contentsWriteConfig extends coreChangeConfig with the permissions and the tools list a delete needs: a
// "repo" connection that is not listed for it, and "repo-listed", one that is.
func contentsWriteConfig(base string) *config.Config {
	cfg := coreChangeConfig(base)
	repo := cfg.Connections["repo"]
	repo.Permissions = config.Permissions()
	cfg.Connections["repo"] = repo
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: config.Permissions(), Tools: []string{contentsDelete.ID}}
	cfg.Connections["project-only"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: config.Permissions()}
	return cfg
}

// Every change tool of this file satisfies its output contract through the application core once confirmed.
func TestContentsWriteToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, w, base := serveContentsWrite(t)
	w.commits["main"] = "cafed00dcafed00dcafed00dcafed00dcafed00d"
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)

	// branches.create resolves "from" through the commits route and creates the ref from its resolved SHA.
	created, err := invoke(t, core, branchesCreate.ID, "repo", `{"name":"feature/login","from":"main"}`, true)
	if err != nil || !strings.Contains(string(created), `"ref":"refs/heads/feature/login"`) ||
		!strings.Contains(string(created), `"sha":"cafed00dcafed00dcafed00dcafed00dcafed00d"`) ||
		!strings.Contains(string(created), `"from":"main"`) {
		t.Fatalf("%s = %s, %v", branchesCreate.ID, created, err)
	}
	requests := f.recorded()
	if len(requests) != 2 || requests[0].method != http.MethodGet || requests[0].path != actionsPrefix+"commits/main" ||
		requests[1].method != http.MethodPost || requests[1].path != actionsPrefix+"git/refs" {
		t.Fatalf("requests = %+v, want a commit resolution then one ref creation", requests)
	}

	// A repeated create of the same branch is refused with a clear message, without a second attempt.
	before := len(f.recorded())
	if _, err := invoke(t, core, branchesCreate.ID, "repo", `{"name":"feature/login","from":"main"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "already holds a branch named feature/login") {
		t.Errorf("a repeated branch create = %v, want an invalid request naming it", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 2 {
		t.Errorf("requests = %+v, want one resolution and one refused create, no retry", requests)
	}

	// Without from, the branch starts at the repository's default branch, read first.
	w.defaultBranch = "main"
	before = len(f.recorded())
	fromDefault, err := invoke(t, core, branchesCreate.ID, "repo", `{"name":"feature/logout"}`, true)
	if err != nil || !strings.Contains(string(fromDefault), `"from":"main"`) ||
		!strings.Contains(string(fromDefault), `"ref":"refs/heads/feature/logout"`) {
		t.Fatalf("%s without from = %s, %v", branchesCreate.ID, fromDefault, err)
	}
	requests = f.recorded()[before:]
	if len(requests) != 3 || requests[0].path != "/api/v3/repos/octo-org/example" ||
		requests[1].path != actionsPrefix+"commits/main" || requests[2].path != actionsPrefix+"git/refs" {
		t.Fatalf("requests = %+v, want the default branch read, a commit resolution, then one ref creation", requests)
	}

	// contents.put creates a new file, then updates it while its current sha is given.
	put, err := invoke(t, core, contentsPut.ID, "repo",
		`{"path":"docs/notes.md","content":"first\n","message":"docs: add notes"}`, true)
	if err != nil || !strings.Contains(string(put), `"path":"docs/notes.md"`) ||
		!strings.Contains(string(put), `"commit_sha":"`+commitSHA+`"`) || strings.Contains(string(put), "first") {
		t.Fatalf("%s create = %s, %v", contentsPut.ID, put, err)
	}
	currentSHA := blobOf("first\n")
	updated, err := invoke(t, core, contentsPut.ID, "repo",
		`{"path":"docs/notes.md","content":"second\n","message":"docs: update notes","sha":"`+currentSHA+`"}`, true)
	if err != nil || !strings.Contains(string(updated), `"sha":"`+blobOf("second\n")+`"`) {
		t.Fatalf("%s update = %s, %v", contentsPut.ID, updated, err)
	}

	// contents.delete removes the file with its current sha, and is offered only where a connection lists it.
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, contentsDelete.ID, "repo",
		`{"path":"docs/notes.md","sha":"`+blobOf("second\n")+`","message":"docs: remove notes"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("contents.delete without a tools list = %v, want unsupported-capability", err)
	}
	deleted, err := invoke(t, core, contentsDelete.ID, "repo-listed",
		`{"path":"docs/notes.md","sha":"`+blobOf("second\n")+`","message":"docs: remove notes"}`, true)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) ||
		!strings.Contains(string(deleted), `"commit_sha":"`+commitSHA+`"`) {
		t.Fatalf("%s = %s, %v", contentsDelete.ID, deleted, err)
	}
}

// A missing or a stale sha is refused as an invalid request naming the file's current SHA once it can be
// read; a conflict whose current SHA matches the one given had another cause and keeps its original class
// and message unchanged.
func TestContentsPutRefusesAMissingOrStaleSHAWithTheCurrentOne(t *testing.T) {
	_, w, base := serveContentsWrite(t)
	w.files["exists.md"] = "current content\n"
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)

	currentSHA := blobOf("current content\n")

	// An existing file without sha is refused as an invalid request, naming the current sha.
	_, err := invoke(t, core, contentsPut.ID, "repo",
		`{"path":"exists.md","content":"new\n","message":"m"}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), currentSHA) {
		t.Errorf("put without sha on an existing file = %v, want an invalid request naming sha %s", err, currentSHA)
	}

	// A stale sha is refused as an invalid request, naming the file's current sha.
	_, err = invoke(t, core, contentsPut.ID, "repo",
		`{"path":"exists.md","content":"new\n","message":"m","sha":"`+strings.Repeat("f", 40)+`"}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "current SHA is "+currentSHA) {
		t.Errorf("put with a stale sha = %v, want an invalid request naming the current sha %s", err, currentSHA)
	}

	// A stale sha on a delete is refused the same way.
	_, err = invoke(t, core, contentsDelete.ID, "repo-listed",
		`{"path":"exists.md","sha":"`+strings.Repeat("f", 40)+`","message":"m"}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "current SHA is "+currentSHA) {
		t.Errorf("delete with a stale sha = %v, want an invalid request naming the current sha %s", err, currentSHA)
	}
}

// A conflict whose current sha equals the sha given was not a sha problem, such as a branch protection rule,
// so the original refusal is kept unchanged instead of being misread as a stale sha.
func TestContentsWriteKeepsTheOriginalRefusalWhenTheGivenSHAIsCurrent(t *testing.T) {
	_, w, base := serveContentsWrite(t)
	w.files["exists.md"] = "current content\n"
	w.forceConflict = true
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)
	currentSHA := blobOf("current content\n")

	_, err := invoke(t, core, contentsPut.ID, "repo",
		`{"path":"exists.md","content":"new\n","message":"m","sha":"`+currentSHA+`"}`, true)
	if classOf(err) != provider.ClassProviderError || isInvalidRequest(err) ||
		strings.Contains(err.Error(), "no longer has the given blob SHA") ||
		!strings.Contains(err.Error(), "current state of the resource") {
		t.Errorf("put with the current sha under an unrelated conflict = %v, want the original conflict unchanged", err)
	}

	_, err = invoke(t, core, contentsDelete.ID, "repo-listed",
		`{"path":"exists.md","sha":"`+currentSHA+`","message":"m"}`, true)
	if classOf(err) != provider.ClassProviderError || isInvalidRequest(err) ||
		strings.Contains(err.Error(), "no longer has the given blob SHA") ||
		!strings.Contains(err.Error(), "current state of the resource") {
		t.Errorf("delete with the current sha under an unrelated conflict = %v, want the original conflict unchanged", err)
	}
}

// A path below .github/workflows/ is refused before a credential is resolved, case included, naming the
// workflow file tools instead; contents.delete has none, since it offers no delete tool of its own.
func TestContentsPutAndDeleteRefuseWorkflowPathsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ operation, path, connection string }{
		{contentsPut.ID, ".github/workflows/ci.yml", "repo"},
		{contentsPut.ID, ".GitHub/Workflows/ci.yml", "repo"},
		{contentsDelete.ID, ".github/workflows/ci.yml", "repo-listed"},
	} {
		reads = 0
		before := len(f.recorded())
		args := `{"path":"` + tt.path + `","content":"x","message":"m","sha":"` + strings.Repeat("a", 40) + `"}`
		if tt.operation == contentsDelete.ID {
			args = `{"path":"` + tt.path + `","sha":"` + strings.Repeat("a", 40) + `","message":"m"}`
		}
		if _, err := invoke(t, core, tt.operation, tt.connection, args, true); !isInvalidRequest(err) ||
			!strings.Contains(err.Error(), ".github/workflows/") {
			t.Errorf("%s on %s = %v, want an invalid request naming .github/workflows/", tt.operation, tt.path, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on %s reached the credential or GitHub", tt.operation, tt.path)
		}
	}
}

// name, from, path, sha, message, and branch are checked before a credential is resolved, so an unusable
// value never reaches GitHub, and a mutation without confirmation is refused the same way.
func TestContentsWriteArgumentsAreValidatedBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, operation, arguments string }{
		{"missing name", branchesCreate.ID, `{"from":"main"}`},
		{"bad from", branchesCreate.ID, `{"name":"x","from":"bad..ref"}`},
		{"missing content", contentsPut.ID, `{"path":"a.md","message":"m"}`},
		{"blank message", contentsPut.ID, `{"path":"a.md","content":"x","message":"  "}`},
		{"bad sha", contentsPut.ID, `{"path":"a.md","content":"x","message":"m","sha":"nothex"}`},
		{"missing sha", contentsDelete.ID, `{"path":"a.md","message":"m"}`},
		{"bad path", contentsDelete.ID, `{"path":"../a.md","sha":"` + strings.Repeat("a", 40) + `","message":"m"}`},
	} {
		reads = 0
		before := len(f.recorded())
		connection := "repo"
		if tt.operation == contentsDelete.ID {
			connection = "repo-listed"
		}
		if _, err := invoke(t, core, tt.operation, connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.operation, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.operation)
		}
	}

	// A mutation without confirmation is refused before a credential is resolved.
	for _, operation := range []string{branchesCreate.ID, contentsPut.ID} {
		reads = 0
		if _, err := invoke(t, core, operation, "repo", `{"name":"x","from":"main","path":"a.md","content":"x","message":"m"}`, false); err == nil {
			t.Errorf("%s without --confirm succeeded, want confirmation-required", operation)
		}
		if reads != 0 {
			t.Errorf("%s without --confirm reached the credential", operation)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a branch or
// content write tool.
func TestContentsWriteRefusesANonRepositoryTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ operation, arguments string }{
		{branchesCreate.ID, `{"name":"x","from":"main"}`},
		{contentsPut.ID, `{"path":"a.md","content":"x","message":"m"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.operation, "project-only", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s on a project-scoped connection = %v, want an invalid request", tt.operation, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on a project-scoped connection reached the credential or GitHub", tt.operation)
		}
	}
}
