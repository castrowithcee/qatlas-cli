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
	// commitTrees and commitParents hold every commit github.files.push created: its tree SHA and its single
	// parent, so a later git/refs/heads PATCH can tell a fast-forward from a stale one.
	commitTrees   map[string]string
	commitParents map[string]string
	// raceBranch and raceSHA, when set, move refs[raceBranch] to raceSHA the moment its head is read through
	// git/refs/heads/, simulating another push landing in the window github.files.push leaves open between
	// reading a branch and moving it.
	raceBranch, raceSHA string
	// defaultBranch, when set, answers the repository route with this default branch name.
	defaultBranch string
	// forceConflict, when set, answers every content write with a 409 regardless of the given sha, the way a
	// branch protection rule unrelated to the blob SHA would.
	forceConflict bool
	// failGitDataStatus, when set, answers every git/blobs, git/trees, or git/commits POST with this status
	// instead of creating the object, to test how github.files.push reports a failure after some objects
	// were already created, for both a permission refusal and an ambiguous server error.
	failGitDataStatus int
}

func serveContentsWrite(t *testing.T) (*fakeGitHub, *fakeContentsWrite, string) {
	t.Helper()
	w := &fakeContentsWrite{files: map[string]string{}, refs: map[string]string{}, commits: map[string]string{},
		commitTrees: map[string]string{}, commitParents: map[string]string{}}
	f := &fakeGitHub{}
	w.f = f
	f.failure = w.route
	return f, w, serve(t, f)
}

// treeShaOf and commitShaOf compute deterministic, fake object SHAs from a git/trees or a git/commits
// request, the way blobOf computes one from a blob's content.
func treeShaOf(baseTree string, entries []any) string {
	parts := []string{baseTree}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path, _ := entry["path"].(string)
		sha, _ := entry["sha"].(string)
		parts = append(parts, path+":"+sha)
	}
	return blobOf("tree:" + strings.Join(parts, "|"))
}

func commitShaOf(message, tree, parent string) string {
	return blobOf("commit:" + message + ":" + tree + ":" + parent)
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
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "git/refs/heads/"):
		branch := strings.TrimPrefix(rest, "git/refs/heads/")
		sha, exists := w.refs[branch]
		if !exists {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return true
		}
		fmt.Fprintf(rw, `{"ref":"refs/heads/%s","object":{"sha":%q,"type":"commit"}}`, branch, sha)
		if branch == w.raceBranch {
			w.refs[branch] = w.raceSHA
		}
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "git/refs/heads/"):
		branch := strings.TrimPrefix(rest, "git/refs/heads/")
		sha, _ := body["sha"].(string)
		current, exists := w.refs[branch]
		if !exists {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return true
		}
		if forced, ok := body["force"]; !ok || forced != false {
			rw.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(rw, `{"message":"force must be false"}`)
			return true
		}
		if w.commitParents[sha] != current {
			rw.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(rw, `{"message":"Update is not a fast forward"}`)
			return true
		}
		w.refs[branch] = sha
		fmt.Fprintf(rw, `{"ref":"refs/heads/%s","object":{"sha":%q}}`, branch, sha)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "git/commits/"):
		sha := strings.TrimPrefix(rest, "git/commits/")
		tree, exists := w.commitTrees[sha]
		if !exists {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"message":"Not Found"}`)
			return true
		}
		fmt.Fprintf(rw, `{"sha":%q,"tree":{"sha":%q}}`, sha, tree)
	case r.Method == http.MethodPost && rest == "git/blobs":
		if w.failGitDataStatus != 0 {
			rw.WriteHeader(w.failGitDataStatus)
			fmt.Fprint(rw, `{"message":"blob creation refused"}`)
			return true
		}
		content, _ := body["content"].(string)
		sha := blobOf(content)
		rw.WriteHeader(http.StatusCreated)
		fmt.Fprintf(rw, `{"sha":%q,"url":"https://api.github.com/repos/octo-org/example/git/blobs/%s"}`, sha, sha)
	case r.Method == http.MethodPost && rest == "git/trees":
		if w.failGitDataStatus != 0 {
			rw.WriteHeader(w.failGitDataStatus)
			fmt.Fprint(rw, `{"message":"tree creation refused"}`)
			return true
		}
		baseTree, _ := body["base_tree"].(string)
		entries, _ := body["tree"].([]any)
		sha := treeShaOf(baseTree, entries)
		rw.WriteHeader(http.StatusCreated)
		fmt.Fprintf(rw, `{"sha":%q}`, sha)
	case r.Method == http.MethodPost && rest == "git/commits":
		if w.failGitDataStatus != 0 {
			rw.WriteHeader(w.failGitDataStatus)
			fmt.Fprint(rw, `{"message":"commit creation refused"}`)
			return true
		}
		message, _ := body["message"].(string)
		tree, _ := body["tree"].(string)
		parents, _ := body["parents"].([]any)
		parent := ""
		if len(parents) > 0 {
			parent, _ = parents[0].(string)
		}
		sha := commitShaOf(message, tree, parent)
		w.commitTrees[sha] = tree
		w.commitParents[sha] = parent
		rw.WriteHeader(http.StatusCreated)
		fmt.Fprintf(rw, `{"sha":%q,"html_url":"https://github.com/octo-org/example/commit/%s"}`, sha, sha)
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

// contentsWriteConfig extends coreChangeConfig with the permissions and the tools list a delete and a push
// need: a "repo" connection that is not listed for either, and "repo-listed", one that is listed for both.
func contentsWriteConfig(base string) *config.Config {
	cfg := coreChangeConfig(base)
	repo := cfg.Connections["repo"]
	repo.Permissions = config.Permissions()
	cfg.Connections["repo"] = repo
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: config.Permissions(), Tools: []string{contentsDelete.ID, filesPush.ID}}
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

// github.files.push reads the branch's head and tree, creates one blob per file and one new tree, creates
// one commit with the read head as its only parent, and moves the branch to it with a fast-forward-only
// ref update sent with force:false and no other field; it is offered only where a connection's tools list
// names it.
func TestFilesPushWritesOneCommitThroughGitDataAPI(t *testing.T) {
	f, w, base := serveContentsWrite(t)
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	w.refs["main"] = head
	w.commitTrees[head] = tree
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)

	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, filesPush.ID, "repo",
		`{"branch":"main","message":"m","files":[{"path":"a.md","content":"A\n"}]}`, true); !errors.As(err, &unsupported) {
		t.Errorf("files.push without a tools list = %v, want unsupported-capability", err)
	}

	pushed, err := invoke(t, core, filesPush.ID, "repo-listed", `{"branch":"main","message":"docs: two files",`+
		`"files":[{"path":"docs/a.md","content":"# A\n"},{"path":"docs/b.md","content":"# B\n"}]}`, true)
	if err != nil {
		t.Fatalf("files.push = %v", err)
	}
	newTree := treeShaOf(tree, []any{
		map[string]any{"path": "docs/a.md", "mode": "100644", "type": "blob", "sha": blobOf("# A\n")},
		map[string]any{"path": "docs/b.md", "mode": "100644", "type": "blob", "sha": blobOf("# B\n")},
	})
	wantCommit := commitShaOf("docs: two files", newTree, head)
	if !strings.Contains(string(pushed), `"branch":"main"`) ||
		!strings.Contains(string(pushed), `"commit_sha":"`+wantCommit+`"`) ||
		!strings.Contains(string(pushed), `"parent_sha":"`+head+`"`) ||
		!strings.Contains(string(pushed), `"paths":["docs/a.md","docs/b.md"]`) ||
		strings.Contains(string(pushed), "# A") || strings.Contains(string(pushed), "# B") {
		t.Fatalf("files.push = %s, %v", pushed, err)
	}
	if w.refs["main"] != wantCommit {
		t.Errorf("branch main = %s, want it moved to %s", w.refs["main"], wantCommit)
	}

	requests := f.recorded()
	if len(requests) != 7 {
		t.Fatalf("requests = %+v, want a head read, a tree read, two blobs, one tree, one commit, and one ref update", requests)
	}
	wantMethodsAndPaths := []struct{ method, path string }{
		{http.MethodGet, actionsPrefix + "git/refs/heads/main"},
		{http.MethodGet, actionsPrefix + "git/commits/" + head},
		{http.MethodPost, actionsPrefix + "git/blobs"},
		{http.MethodPost, actionsPrefix + "git/blobs"},
		{http.MethodPost, actionsPrefix + "git/trees"},
		{http.MethodPost, actionsPrefix + "git/commits"},
		{http.MethodPatch, actionsPrefix + "git/refs/heads/main"},
	}
	for i, want := range wantMethodsAndPaths {
		if requests[i].method != want.method || requests[i].path != want.path {
			t.Errorf("request %d = %s %s, want %s %s", i, requests[i].method, requests[i].path, want.method, want.path)
		}
	}
	if requests[6].body["force"] != false || requests[6].body["sha"] != wantCommit || len(requests[6].body) != 2 {
		t.Errorf("ref update body = %+v, want only sha and force:false", requests[6].body)
	}
}

// A branch that moved between github.files.push's read of its head and its ref update is refused with
// nothing written: the ref update is sent with force:false exactly once, GitHub's non-fast-forward 422 is
// turned into an invalid request naming the branch's current head, read again on a best-effort basis.
func TestFilesPushRefusesAStaleHeadWithoutForce(t *testing.T) {
	f, w, base := serveContentsWrite(t)
	head, tree, raced := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	w.refs["main"] = head
	w.commitTrees[head] = tree
	w.raceBranch, w.raceSHA = "main", raced
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)

	before := len(f.recorded())
	_, err := invoke(t, core, filesPush.ID, "repo-listed",
		`{"branch":"main","message":"m","files":[{"path":"a.md","content":"A\n"}]}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "not a fast-forward") ||
		!strings.Contains(err.Error(), "branch main is now at "+raced) {
		t.Errorf("push on a moved branch = %v, want an invalid request naming the current head %s", err, raced)
	}
	requests := f.recorded()[before:]
	patches := 0
	for _, request := range requests {
		if request.method == http.MethodPatch {
			patches++
			if request.body["force"] != false {
				t.Errorf("ref update body = %+v, want force:false", request.body)
			}
		}
	}
	if patches != 1 {
		t.Errorf("ref update PATCHes = %d, want exactly one, no retry", patches)
	}
	if w.refs["main"] != raced {
		t.Errorf("branch main = %s, want it left at the concurrent push's head %s", w.refs["main"], raced)
	}
}

// expected_head_sha, when given, is checked against the branch's head as soon as it is read, before any
// blob is created, so a caller that already knows the branch moved is refused without creating git objects.
func TestFilesPushChecksExpectedHeadSHABeforeAnyBlob(t *testing.T) {
	f, w, base := serveContentsWrite(t)
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	w.refs["main"] = head
	w.commitTrees[head] = tree
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)

	stale := strings.Repeat("d", 40)
	before := len(f.recorded())
	_, err := invoke(t, core, filesPush.ID, "repo-listed", `{"branch":"main","message":"m","expected_head_sha":"`+
		stale+`","files":[{"path":"a.md","content":"A\n"}]}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), head) || !strings.Contains(err.Error(), stale) {
		t.Errorf("push with a stale expected_head_sha = %v, want an invalid request naming both SHAs", err)
	}
	requests := f.recorded()[before:]
	if len(requests) != 1 || requests[0].path != actionsPrefix+"git/refs/heads/main" {
		t.Errorf("requests = %+v, want only the head read, no blob created", requests)
	}
}

// A refusal while creating a blob, a tree, or a commit leaves the branch exactly as it was: a permission
// refusal is replaced outright, and any other refusal is told apart from an ambiguous server error, which
// keeps its own uncertain wording since the ref update was never reached either way.
func TestFilesPushLeavesTheBranchUnchangedOnAnObjectFailure(t *testing.T) {
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tt := range []struct {
		name   string
		status int
		check  func(t *testing.T, err error)
	}{
		{"permission", http.StatusForbidden, func(t *testing.T, err error) {
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read and write") {
				t.Errorf("blob creation refused by permission = %v, want the contents change permission message", err)
			}
		}},
		{"server error", http.StatusServiceUnavailable, func(t *testing.T, err error) {
			if isInvalidRequest(err) || !strings.Contains(err.Error(), "branch main is unchanged") ||
				!strings.Contains(err.Error(), "loose Git object") {
				t.Errorf("blob creation refused by a server error = %v, want branch main named unchanged", err)
			}
		}},
	} {
		_, w, base := serveContentsWrite(t)
		w.refs["main"] = head
		w.commitTrees[head] = tree
		w.failGitDataStatus = tt.status
		red := &redact.Redactor{}
		core := application.New(registry(t), contentsWriteConfig(base), resolver(red, nil), red)
		_, err := invoke(t, core, filesPush.ID, "repo-listed",
			`{"branch":"main","message":"m","files":[{"path":"a.md","content":"A\n"}]}`, true)
		if err == nil {
			t.Fatalf("%s: files.push succeeded, want a refusal", tt.name)
		}
		tt.check(t, err)
		if w.refs["main"] != head {
			t.Errorf("%s: branch main = %s, want it left at %s", tt.name, w.refs["main"], head)
		}
	}
}

// branch, message, files, and every file's path and content are checked before a credential is resolved, a
// duplicate or a workflow path is refused the same way, and the count and the combined size of files stay
// within their bounds.
func TestFilesPushArgumentsAreValidatedBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), contentsWriteConfig(serve(t, f)), resolver(red, &reads), red)

	oversizedFile := `{"path":"a.md","content":"` + strings.Repeat("a", maxContentsPutBytes+1) + `"}`
	manyFiles := make([]string, 0, maxFilesPushCount+1)
	for i := 0; i <= maxFilesPushCount; i++ {
		manyFiles = append(manyFiles, fmt.Sprintf(`{"path":"f%d.md","content":"x"}`, i))
	}
	bigFile := `{"path":"big%d.md","content":"` + strings.Repeat("a", maxContentsPutBytes) + `"}`
	tooMuchContent := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		tooMuchContent = append(tooMuchContent, fmt.Sprintf(bigFile, i))
	}

	for _, tt := range []struct{ name, arguments string }{
		{"missing branch", `{"message":"m","files":[{"path":"a.md","content":"x"}]}`},
		{"bad branch", `{"branch":"bad..ref","message":"m","files":[{"path":"a.md","content":"x"}]}`},
		{"blank message", `{"branch":"main","message":"  ","files":[{"path":"a.md","content":"x"}]}`},
		{"no files", `{"branch":"main","message":"m","files":[]}`},
		{"too many files", `{"branch":"main","message":"m","files":[` + strings.Join(manyFiles, ",") + `]}`},
		{"oversized file", `{"branch":"main","message":"m","files":[` + oversizedFile + `]}`},
		{"combined content too large", `{"branch":"main","message":"m","files":[` + strings.Join(tooMuchContent, ",") + `]}`},
		{"duplicate path", `{"branch":"main","message":"m","files":[{"path":"a.md","content":"x"},` +
			`{"path":"a.md","content":"y"}]}`},
		{"workflow path", `{"branch":"main","message":"m","files":[{"path":".github/workflows/ci.yml","content":"x"}]}`},
		{"workflow path, case-insensitive", `{"branch":"main","message":"m","files":[` +
			`{"path":".GitHub/Workflows/ci.yml","content":"x"}]}`},
		{"bad path", `{"branch":"main","message":"m","files":[{"path":"../a.md","content":"x"}]}`},
		{"bad expected_head_sha", `{"branch":"main","message":"m","expected_head_sha":"nothex",` +
			`"files":[{"path":"a.md","content":"x"}]}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, filesPush.ID, "repo-listed", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: files.push(%s) = %v, want an invalid request", tt.name, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: files.push reached the credential or GitHub", tt.name)
		}
	}

	// A mutation without confirmation is refused before a credential is resolved.
	reads = 0
	if _, err := invoke(t, core, filesPush.ID, "repo-listed",
		`{"branch":"main","message":"m","files":[{"path":"a.md","content":"x"}]}`, false); err == nil {
		t.Error("files.push without --confirm succeeded, want confirmation-required")
	}
	if reads != 0 {
		t.Error("files.push without --confirm reached the credential")
	}
}
