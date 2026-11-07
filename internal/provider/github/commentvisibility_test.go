package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// fakeVisibility answers the REST reads of an issue comment, a review, and a line comment, and the two
// visibility mutations, through the failure hook of fakeGitHub. Objects 555, 31, and 41 belong to the bound
// repository; 888, 32, and 42 answer from the bound repository's route but name another repository.
type fakeVisibility struct {
	mu sync.Mutex
	f  *fakeGitHub
	// mutation answers the GraphQL document; nil answers a visible object after unhide and a hidden one
	// after hide.
	mutation func(w http.ResponseWriter, hide bool)
}

func serveVisibility(t *testing.T) (*fakeVisibility, string) {
	t.Helper()
	v := &fakeVisibility{f: &fakeGitHub{}}
	v.f.failure = v.route
	return v, serve(t, v.f)
}

func (v *fakeVisibility) route(w http.ResponseWriter, req *http.Request) bool {
	w.Header().Set("Content-Type", "application/json")
	if req.URL.Path == "/api/graphql" {
		requests := v.f.recorded()
		document := requests[len(requests)-1].document
		if !strings.HasPrefix(document, "mutation") {
			number := requests[len(requests)-1].variables["number"]
			switch {
			case strings.Contains(document, "pullRequest(number:$number){id}") && number == float64(7):
				fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"id":"PR_7"}}}}`)
			case strings.Contains(document, "pullRequest(number:$number){id}"):
				fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}}}`)
			case strings.Contains(document, "issue(number:$number){id}"):
				fmt.Fprint(w, `{"data":{"repository":{"issue":{"id":"I_42"}}}}`)
			default:
				return false
			}
			return true
		}
		hide := strings.Contains(document, "result:minimizeComment")
		if v.mutation != nil {
			v.mutation(w, hide)
			return true
		}
		if hide {
			fmt.Fprint(w, `{"data":{"result":{"minimizedComment":{"isMinimized":true,"minimizedReason":"off-topic"}}}}`)
		} else {
			fmt.Fprint(w, `{"data":{"result":{"unminimizedComment":{"isMinimized":false,"minimizedReason":null}}}}`)
		}
		return true
	}
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || req.Method != http.MethodGet {
		return false
	}
	const other = "https://api.github.com/repos/other-org/secret/"
	switch rest {
	case "issues/comments/555":
		fmt.Fprint(w, `{"id":555,"node_id":"IC_555","body":"canary","issue_url":"https://api.github.com/repos/octo-org/example/issues/42"}`)
	case "issues/comments/777":
		fmt.Fprint(w, `{"id":777,"node_id":"IC_777","issue_url":"https://api.github.com/repos/octo-org/example/issues/7"}`)
	case "issues/comments/778":
		fmt.Fprint(w, `{"id":778,"node_id":"IC_778","issue_url":"https://api.github.com/repos/octo-org/example/issues/42"}`)
	case "issues/comments/889":
		fmt.Fprint(w, `{"id":889,"node_id":"IC_889","issue_url":"`+other+`issues/7"}`)
	case "issues/comments/888":
		fmt.Fprint(w, `{"id":888,"node_id":"IC_888","issue_url":"`+other+`issues/42"}`)
	case "pulls/7/reviews/31":
		fmt.Fprint(w, `{"id":31,"node_id":"PRR_31","body":"canary","pull_request_url":"https://api.github.com/repos/Octo-Org/Example/pulls/7"}`)
	case "pulls/7/reviews/32":
		fmt.Fprint(w, `{"id":32,"node_id":"PRR_32","pull_request_url":"`+other+`pulls/7"}`)
	case "pulls/8/reviews/31":
		fmt.Fprint(w, `{"id":31,"node_id":"PRR_31","pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7"}`)
	case "pulls/comments/41":
		fmt.Fprint(w, `{"id":41,"node_id":"PRRC_41","body":"canary","pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7"}`)
	case "pulls/comments/42":
		fmt.Fprint(w, `{"id":42,"node_id":"PRRC_42","pull_request_url":"`+other+`pulls/7"}`)
	case "pulls/comments/43":
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"canary"}`)
	default:
		if !strings.HasPrefix(rest, "issues/comments/") && !strings.HasPrefix(rest, "pulls/comments/") &&
			!strings.Contains(rest, "/reviews/") {
			return false
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
	return true
}

func visibilityConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget, Effects: all}
	cfg.Connections["ex"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all,
		Exclude: []string{"repos/octo-org/example"}}
	return cfg
}

func (v *fakeVisibility) mutations() int {
	_, mutations, _ := split(v.f.recorded())
	return len(mutations)
}

// Each kind is read below the bound repository first, hides with the node identifier of that answer and the
// chosen reason, and shows again; the answer holds the state GitHub reports and none of its text.
func TestCommentVisibilityChangesEveryKindWithTheNodeOfItsOwnRead(t *testing.T) {
	v, base := serveVisibility(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), visibilityConfig(base), resolver(red, nil), red)
	for _, tt := range []struct{ kind, args, node string }{
		{"issue-comment", `{"kind":"issue-comment","id":555`, "IC_555"},
		{"pull-request-comment", `{"kind":"pull-request-comment","pull_number":7,"id":777`, "IC_777"},
		{"review", `{"kind":"review","pull_number":7,"id":31`, "PRR_31"},
		{"review-comment", `{"kind":"review-comment","id":41`, "PRRC_41"},
	} {
		before := len(v.f.recorded())
		hidden, err := invoke(t, core, commentsHide.ID, "repo", tt.args+`,"reason":"OFF_TOPIC"}`, true)
		if err != nil || !strings.Contains(string(hidden), `"kind":"`+tt.kind+`"`) ||
			!strings.Contains(string(hidden), `"hidden":true`) || !strings.Contains(string(hidden), `"reason":"OFF_TOPIC"`) ||
			strings.Contains(string(hidden), "canary") {
			t.Fatalf("hide %s = %s, %v", tt.kind, hidden, err)
		}
		_, mutations, rest := split(v.f.recorded()[before:])
		if len(mutations) != 1 || rest[http.MethodGet] < 1 || mutations[0].variables["id"] != tt.node ||
			mutations[0].variables["classifier"] != "OFF_TOPIC" {
			t.Errorf("hide %s sent %+v and %v", tt.kind, mutations, rest)
		}
		shown, err := invoke(t, core, commentsUnhide.ID, "repo", tt.args+`}`, true)
		if err != nil || !strings.Contains(string(shown), `"hidden":false`) || strings.Contains(string(shown), `"reason"`) {
			t.Fatalf("unhide %s = %s, %v", tt.kind, shown, err)
		}
	}
	unconfirmed := &application.ConfirmationRequiredError{}
	before := len(v.f.recorded())
	if _, err := invoke(t, core, commentsHide.ID, "repo", `{"kind":"issue-comment","id":555,"reason":"SPAM"}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("hide without confirmation = %v", err)
	}
	if len(v.f.recorded()) != before {
		t.Error("hide without confirmation reached GitHub")
	}
}

// An object that is already in the requested state is reported with the state GitHub answers, as success.
func TestCommentVisibilityIsIdempotent(t *testing.T) {
	v, base := serveVisibility(t)
	v.mutation = func(w http.ResponseWriter, hide bool) {
		if hide {
			fmt.Fprint(w, `{"data":{"result":{"minimizedComment":{"isMinimized":true,"minimizedReason":"spam"}}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"result":{"unminimizedComment":{"isMinimized":false,"minimizedReason":null}}}}`)
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), visibilityConfig(base), resolver(red, nil), red)
	for i := 0; i < 2; i++ {
		got, err := invoke(t, core, commentsHide.ID, "repo", `{"kind":"issue-comment","id":555,"reason":"OFF_TOPIC"}`, true)
		if err != nil || !strings.Contains(string(got), `"hidden":true`) || !strings.Contains(string(got), `"reason":"SPAM"`) {
			t.Errorf("repeated hide = %s, %v", got, err)
		}
		got, err = invoke(t, core, commentsUnhide.ID, "repo", `{"kind":"issue-comment","id":555}`, true)
		if err != nil || !strings.Contains(string(got), `"hidden":false`) {
			t.Errorf("repeated unhide = %s, %v", got, err)
		}
	}
}

// An object that does not name the bound repository, a pull request conversation comment, and an unknown or
// mismatching object are refused before any mutation is sent.
func TestCommentVisibilityRefusesForeignObjectsWithoutAMutation(t *testing.T) {
	v, base := serveVisibility(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), visibilityConfig(base), resolver(red, nil), red)
	for _, tt := range []struct {
		name, args string
		class      provider.Class
	}{
		{"issue comment of another repository", `{"kind":"issue-comment","id":888`, provider.ClassNotFound},
		{"conversation comment of another pull request", `{"kind":"pull-request-comment","pull_number":8,"id":777`, provider.ClassNotFound},
		{"conversation comment of another repository", `{"kind":"pull-request-comment","pull_number":7,"id":889`, provider.ClassNotFound},
		{"unknown conversation comment", `{"kind":"pull-request-comment","pull_number":7,"id":999999`, provider.ClassNotFound},
		{"review of another repository", `{"kind":"review","pull_number":7,"id":32`, provider.ClassNotFound},
		{"review of another pull request", `{"kind":"review","pull_number":8,"id":31`, provider.ClassNotFound},
		{"line comment of another repository", `{"kind":"review-comment","id":42`, provider.ClassNotFound},
		{"unknown issue comment", `{"kind":"issue-comment","id":999999`, provider.ClassNotFound},
		{"unknown review", `{"kind":"review","pull_number":7,"id":999`, provider.ClassNotFound},
		{"unknown line comment", `{"kind":"review-comment","id":999999`, provider.ClassNotFound},
		{"forbidden read", `{"kind":"review-comment","id":43`, provider.ClassPermission},
	} {
		for _, tool := range []struct{ id, extra string }{{commentsHide.ID, `,"reason":"SPAM"}`}, {commentsUnhide.ID, `}`}} {
			_, err := invoke(t, core, tool.id, "repo", tt.args+tool.extra, true)
			if classOf(err) != tt.class || strings.Contains(fmt.Sprint(err), "canary") {
				t.Errorf("%s via %s = %v, want %s", tt.name, tool.id, err, tt.class)
			}
		}
	}
	// An issue given as a pull request is refused as an invalid request.
	if _, err := invoke(t, core, commentsHide.ID, "repo", `{"kind":"pull-request-comment","pull_number":42,"id":778,"reason":"SPAM"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "is an issue") {
		t.Errorf("issue comment as pull request comment = %v, want an invalid request naming it", err)
	}
	if _, err := invoke(t, core, commentsHide.ID, "repo", `{"kind":"issue-comment","id":777,"reason":"SPAM"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "is a pull request") || !strings.Contains(err.Error(), "pull-request-comment") {
		t.Errorf("pull request conversation comment = %v, want an invalid request naming it", err)
	}
	if n := v.mutations(); n != 0 {
		t.Errorf("%d mutations were sent for refused objects", n)
	}
}

// Every check runs before a credential is resolved or a request is sent, including the target and exclusion.
func TestCommentVisibilityIsCheckedBeforeSecretAccess(t *testing.T) {
	v, base := serveVisibility(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), visibilityConfig(base), resolver(red, &reads), red)
	for _, tt := range []struct{ name, id, connection, args string }{
		{"invalid reason", commentsHide.ID, "repo", `{"kind":"issue-comment","id":555,"reason":"BORING"}`},
		{"lowercase reason", commentsHide.ID, "repo", `{"kind":"issue-comment","id":555,"reason":"spam"}`},
		{"missing reason", commentsHide.ID, "repo", `{"kind":"issue-comment","id":555}`},
		{"reason on unhide", commentsUnhide.ID, "repo", `{"kind":"issue-comment","id":555,"reason":"SPAM"}`},
		{"unknown kind", commentsUnhide.ID, "repo", `{"kind":"issue","id":555}`},
		{"zero id", commentsUnhide.ID, "repo", `{"kind":"issue-comment","id":0}`},
		{"review without pull_number", commentsUnhide.ID, "repo", `{"kind":"review","id":31}`},
		{"conversation comment without pull_number", commentsUnhide.ID, "repo", `{"kind":"pull-request-comment","id":777}`},
		{"pull_number on a line comment", commentsUnhide.ID, "repo", `{"kind":"review-comment","id":41,"pull_number":7}`},
		{"pull_number on a comment", commentsUnhide.ID, "repo", `{"kind":"issue-comment","id":555,"pull_number":7}`},
		{"foreign repository", commentsUnhide.ID, "repo", `{"kind":"issue-comment","id":555,"repository":"octo-org/other"}`},
		{"excluded repository", commentsUnhide.ID, "ex", `{"kind":"issue-comment","id":555,"repository":"octo-org/example"}`},
		{"excluded repository hide", commentsHide.ID, "ex",
			`{"kind":"issue-comment","id":555,"reason":"SPAM","repository":"octo-org/example"}`},
	} {
		if _, err := invoke(t, core, tt.id, tt.connection, tt.args, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
	}
	if reads != 0 || len(v.f.recorded()) != 0 {
		t.Errorf("refused requests read %d credentials and sent %d requests", reads, len(v.f.recorded()))
	}
}

// A refused mutation names no provider text, and an unclear outcome is sent once and reported as uncertain.
func TestCommentVisibilityMutationFailures(t *testing.T) {
	for name, tt := range map[string]struct {
		answer    func(http.ResponseWriter, bool)
		class     provider.Class
		uncertain bool
	}{
		"forbidden": {func(w http.ResponseWriter, _ bool) {
			fmt.Fprint(w, `{"errors":[{"type":"FORBIDDEN","message":"canary","path":["result"]}],"data":null}`)
		}, provider.ClassPermission, false},
		"not found": {func(w http.ResponseWriter, _ bool) {
			fmt.Fprint(w, `{"errors":[{"type":"NOT_FOUND","message":"canary","path":["result"]}],"data":null}`)
		}, provider.ClassNotFound, false},
		"http forbidden": {func(w http.ResponseWriter, _ bool) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"canary"}`)
		}, provider.ClassPermission, false},
		"gateway timeout": {func(w http.ResponseWriter, _ bool) { w.WriteHeader(http.StatusGatewayTimeout) },
			provider.ClassTimeout, true},
		"server error": {func(w http.ResponseWriter, _ bool) { w.WriteHeader(http.StatusBadGateway) },
			provider.ClassProviderError, true},
		"unreadable answer": {func(w http.ResponseWriter, _ bool) { fmt.Fprint(w, `{"data":`) },
			provider.ClassInvalidResponse, true},
		"answer without a state": {func(w http.ResponseWriter, _ bool) { fmt.Fprint(w, `{"data":{"result":null}}`) },
			provider.ClassInvalidResponse, true},
		"dropped connection": {func(w http.ResponseWriter, _ bool) {
			if connection, _, err := w.(http.Hijacker).Hijack(); err == nil {
				connection.Close()
			}
		}, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			v, base := serveVisibility(t)
			v.mutation = tt.answer
			red := &redact.Redactor{}
			core := application.New(registry(t), visibilityConfig(base), resolver(red, nil), red)
			for _, call := range []struct{ id, args string }{
				{commentsHide.ID, `{"kind":"review-comment","id":41,"reason":"SPAM"}`},
				{commentsUnhide.ID, `{"kind":"review-comment","id":41}`},
			} {
				before := v.mutations()
				_, err := invoke(t, core, call.id, "repo", call.args, true)
				if err == nil || strings.Contains(err.Error(), "canary") ||
					strings.Contains(err.Error(), uncertain) != tt.uncertain ||
					(tt.class != "" && classOf(err) != tt.class) {
					t.Errorf("%s = %v, want class %q, uncertain %v", call.id, err, tt.class, tt.uncertain)
				}
				if n := v.mutations() - before; n != 1 {
					t.Errorf("%s sent %d mutations, want exactly one", call.id, n)
				}
			}
		})
	}
}
