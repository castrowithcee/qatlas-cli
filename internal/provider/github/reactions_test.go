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
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeReactionEntry is one reaction the fake repository holds.
type fakeReactionEntry struct {
	id      int64
	login   string
	content string
}

// fakeReactions answers the reaction routes of the bound repository through the failure hook of fakeGitHub,
// the way fakeComments answers the comment maintenance routes: by a target key of its kind and id, so the
// three kinds share one fixture the way they share one tool.
type fakeReactions struct {
	mu        sync.Mutex
	f         *fakeGitHub
	targets   map[string]bool
	reactions map[string][]fakeReactionEntry
	nextID    int64
}

func targetKey(kind string, id int64) string {
	return kind + ":" + strconv.FormatInt(id, 10)
}

// serveReactions installs a repository whose issue 42, its comment 555, pull request 7 (a plain issue for
// the reaction routes, the way it already is for github.comments.*), its conversation comment 777, and one
// pull request line comment 9001 all accept reactions; the account behind the token is octocat, the same
// login the base fake router's /user route already answers.
func serveReactions(t *testing.T) (*fakeGitHub, *fakeReactions, string) {
	t.Helper()
	r := &fakeReactions{
		targets:   map[string]bool{targetKey("issue", 42): true, targetKey("issue", 7): true, targetKey("issue_comment", 555): true, targetKey("issue_comment", 777): true, targetKey("review_comment", 9001): true},
		reactions: map[string][]fakeReactionEntry{},
		nextID:    1,
	}
	f := &fakeGitHub{}
	r.f = f
	f.failure = r.route
	return f, r, serve(t, f)
}

func reactionJSONOf(e fakeReactionEntry) string {
	return fmt.Sprintf(`{"id":%d,"content":%q,"user":{"login":%q}}`, e.id, e.content, e.login)
}

// route answers issues/NUMBER/reactions, issues/comments/ID/reactions, and pulls/comments/ID/reactions,
// with or without a trailing /REACTION_ID, the way the three reaction target kinds address them.
func (r *fakeReactions) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	kind, tail, ok := r.parse(rest)
	if !ok {
		return false
	}
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) < 1 || parts[0] != "reactions" {
		return false
	}
	idText := strings.TrimPrefix(tail, "reactions")
	idText = strings.TrimPrefix(idText, "/")
	number, ok := r.targetID(kind, rest)
	if !ok {
		return false
	}
	key := targetKey(kind, number)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.targets[key] {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && idText == "":
		content := req.URL.Query().Get("content")
		perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(req.URL.Query().Get("page"))
		matching := make([]fakeReactionEntry, 0)
		for _, entry := range r.reactions[key] {
			if entry.content == content {
				matching = append(matching, entry)
			}
		}
		start := min((page-1)*perPage, len(matching))
		end := min(start+perPage, len(matching))
		entries := make([]string, 0, end-start)
		for _, entry := range matching[start:end] {
			entries = append(entries, reactionJSONOf(entry))
		}
		if end < len(matching) {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
		}
		fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
	case req.Method == http.MethodPost && idText == "":
		var body struct {
			Content string `json:"content"`
		}
		if requests := r.f.recorded(); len(requests) > 0 {
			content, _ := requests[len(requests)-1].body["content"].(string)
			body.Content = content
		}
		for _, entry := range r.reactions[key] {
			if entry.login == "octocat" && entry.content == body.Content {
				fmt.Fprint(w, reactionJSONOf(entry))
				return true
			}
		}
		entry := fakeReactionEntry{id: r.nextID, login: "octocat", content: body.Content}
		r.nextID++
		r.reactions[key] = append(r.reactions[key], entry)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, reactionJSONOf(entry))
	case req.Method == http.MethodDelete && idText != "":
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		entries := r.reactions[key]
		for i, entry := range entries {
			if entry.id == id {
				r.reactions[key] = append(entries[:i], entries[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return true
			}
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	default:
		return false
	}
	return true
}

// parse tells apart the three reaction route shapes below the bound repository and returns the kind and the
// tail after the target's identifier: "reactions" or "reactions/REACTION_ID".
func (r *fakeReactions) parse(rest string) (kind, tail string, ok bool) {
	switch {
	case strings.HasPrefix(rest, "issues/comments/"):
		tail, ok = cutSecondSegment(strings.TrimPrefix(rest, "issues/comments/"))
		return "issue_comment", tail, ok
	case strings.HasPrefix(rest, "pulls/comments/"):
		tail, ok = cutSecondSegment(strings.TrimPrefix(rest, "pulls/comments/"))
		return "review_comment", tail, ok
	case strings.HasPrefix(rest, "issues/"):
		tail, ok = cutSecondSegment(strings.TrimPrefix(rest, "issues/"))
		return "issue", tail, ok
	}
	return "", "", false
}

func cutSecondSegment(rest string) (string, bool) {
	_, tail, ok := strings.Cut(rest, "/")
	return tail, ok
}

// targetID reads the numeric identifier of the target of one reaction path, the first path segment after
// the kind's prefix.
func (r *fakeReactions) targetID(kind, rest string) (int64, bool) {
	var digits string
	switch kind {
	case "issue_comment":
		digits, _, _ = strings.Cut(strings.TrimPrefix(rest, "issues/comments/"), "/")
	case "review_comment":
		digits, _, _ = strings.Cut(strings.TrimPrefix(rest, "pulls/comments/"), "/")
	case "issue":
		digits, _, _ = strings.Cut(strings.TrimPrefix(rest, "issues/"), "/")
	default:
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

// reactionsConfig binds a connection with every permission and no tools list, the way commentMaintenanceConfig
// binds its own repository and project connections.
func reactionsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["planning-full"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: all}
	return cfg
}

// Every reaction tool satisfies its output contract through the application core once confirmed, one example
// per target kind, github.reactions.add is idempotent on a reaction this account already left, and
// github.reactions.remove never deletes another account's reaction.
func TestReactionsToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, r, base := serveReactions(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), reactionsConfig(base), resolver(red, nil), red)

	// issue: a plain issue number.
	added, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"issue","id":42,"content":"+1"}`, true)
	if err != nil || !strings.Contains(string(added), `"added":true`) || !strings.Contains(string(added), `"content":"+1"`) {
		t.Fatalf("add to an issue = %s, %v", added, err)
	}
	before := len(f.recorded())
	again, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"issue","id":42,"content":"+1"}`, true)
	if err != nil || !strings.Contains(string(again), `"added":false`) {
		t.Fatalf("repeated add to the same issue = %s, %v", again, err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPost] != 1 {
		t.Errorf("a repeated add sent %v, want exactly one POST", rest)
	}

	// issue: a pull request number is not refused, since a pull request conversation is an issue for
	// reactions.
	prAdded, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"issue","id":7,"content":"heart"}`, true)
	if err != nil || !strings.Contains(string(prAdded), `"added":true`) {
		t.Fatalf("add to a pull request number = %s, %v", prAdded, err)
	}

	// issue_comment: an issue comment.
	commentAdded, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"issue_comment","id":555,"content":"laugh"}`, true)
	if err != nil || !strings.Contains(string(commentAdded), `"added":true`) {
		t.Fatalf("add to a comment = %s, %v", commentAdded, err)
	}

	// issue_comment: a pull request conversation comment is accepted too, unlike github.comments.update.
	prCommentAdded, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"issue_comment","id":777,"content":"rocket"}`, true)
	if err != nil || !strings.Contains(string(prCommentAdded), `"added":true`) {
		t.Fatalf("add to a pull request conversation comment = %s, %v", prCommentAdded, err)
	}

	// review_comment: a pull request line comment.
	lineAdded, err := invoke(t, core, reactionsAdd.ID, "repo", `{"kind":"review_comment","id":9001,"content":"eyes"}`, true)
	if err != nil || !strings.Contains(string(lineAdded), `"added":true`) {
		t.Fatalf("add to a line comment = %s, %v", lineAdded, err)
	}

	// A foreign reaction of the same content on the same issue is never removed: seed it before removing the
	// account's own.
	r.mu.Lock()
	key := targetKey("issue", 42)
	r.reactions[key] = append(r.reactions[key], fakeReactionEntry{id: 999, login: "someone-else", content: "+1"})
	r.mu.Unlock()

	removed, err := invoke(t, core, reactionsRemove.ID, "repo", `{"kind":"issue","id":42,"content":"+1"}`, true)
	if err != nil || !strings.Contains(string(removed), `"removed":true`) {
		t.Fatalf("remove the account's own reaction = %s, %v", removed, err)
	}
	r.mu.Lock()
	remaining := append([]fakeReactionEntry(nil), r.reactions[key]...)
	r.mu.Unlock()
	if len(remaining) != 1 || remaining[0].login != "someone-else" {
		t.Errorf("reactions of issue 42 after remove = %+v, want only the foreign one left", remaining)
	}

	// Removing again finds no reaction of this account left and changes nothing.
	before = len(f.recorded())
	removedAgain, err := invoke(t, core, reactionsRemove.ID, "repo", `{"kind":"issue","id":42,"content":"+1"}`, true)
	if err != nil || !strings.Contains(string(removedAgain), `"removed":false`) {
		t.Fatalf("remove without a reaction left = %s, %v", removedAgain, err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodDelete] != 0 {
		t.Errorf("removing without a reaction left sent a DELETE: %v", rest)
	}
	r.mu.Lock()
	remaining = append([]fakeReactionEntry(nil), r.reactions[key]...)
	r.mu.Unlock()
	if len(remaining) != 1 || remaining[0].login != "someone-else" {
		t.Errorf("the foreign reaction was touched: %+v", remaining)
	}
}

// Every check of these tools runs before a credential is resolved: kind, id, and content.
func TestReactionsArgumentsAreValidatedBeforeIO(t *testing.T) {
	_, _, base := serveReactions(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), reactionsConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, id, arguments string
	}{
		{"missing kind", reactionsAdd.ID, `{"id":42,"content":"+1"}`},
		{"invalid kind", reactionsAdd.ID, `{"kind":"pull_request","id":42,"content":"+1"}`},
		{"missing id", reactionsAdd.ID, `{"kind":"issue","content":"+1"}`},
		{"zero id", reactionsAdd.ID, `{"kind":"issue","id":0,"content":"+1"}`},
		{"issue id too large", reactionsAdd.ID, `{"kind":"issue","id":9007199254740991,"content":"+1"}`},
		{"missing content", reactionsAdd.ID, `{"kind":"issue","id":42}`},
		{"invalid content", reactionsAdd.ID, `{"kind":"issue","id":42,"content":"tada"}`},
		{"missing kind", reactionsRemove.ID, `{"id":42,"content":"+1"}`},
		{"invalid kind", reactionsRemove.ID, `{"kind":"pull_request","id":42,"content":"+1"}`},
		{"zero id", reactionsRemove.ID, `{"kind":"issue_comment","id":0,"content":"+1"}`},
		{"invalid content", reactionsRemove.ID, `{"kind":"review_comment","id":9001,"content":"tada"}`},
	} {
		reads = 0
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.id, tt.arguments, err)
		}
		if reads != 0 {
			t.Errorf("%s: %s reached the credential", tt.name, tt.id)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a reaction
// tool: the target is checked before a secret is read.
func TestReactionsRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	_, _, base := serveReactions(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), reactionsConfig(base), resolver(red, &reads), red)

	for _, id := range []string{reactionsAdd.ID, reactionsRemove.ID} {
		reads = 0
		if _, err := invoke(t, core, id, "planning-full", `{"kind":"issue","id":42,"content":"+1"}`, true); !isInvalidRequest(err) {
			t.Errorf("%s on a project-scoped connection = %v, want an invalid request", id, err)
		}
		if reads != 0 {
			t.Errorf("%s on a project-scoped connection reached the credential", id)
		}
	}
}

// Both reaction tools require --confirm the way every other change of this provider does, and a refused
// call never reaches GitHub.
func TestReactionsRequireConfirmation(t *testing.T) {
	f, _, base := serveReactions(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), reactionsConfig(base), resolver(red, nil), red)

	for _, id := range []string{reactionsAdd.ID, reactionsRemove.ID} {
		before := len(f.recorded())
		unconfirmed := &application.ConfirmationRequiredError{}
		if _, err := invoke(t, core, id, "repo", `{"kind":"issue","id":42,"content":"+1"}`, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", id, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", id)
		}
	}
}
