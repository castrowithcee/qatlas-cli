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

// repoIndex and repoByIndex let the sub-issue and issue dependency fakes compute a stable fake database id
// from a repository and an issue number, and read the pair back from an id, without a separate registration
// step. octo-org/blocked lies outside the targets of the connection these tests use, so a call that names it
// as a sub-issue or a dependency repository must be refused before any request reaches it; the withheld tests
// use it only as a foreign repository GitHub's answer names inside a batch it already served for the bound
// repository, never as the repository a request addresses.
var (
	repoIndex   = map[string]int64{"octo-org/example": 1, "octo-org/other": 2, "octo-org/blocked": 3}
	repoByIndex = map[int64]string{1: "octo-org/example", 2: "octo-org/other", 3: "octo-org/blocked"}
)

func fakeIssueID(repo string, number int) int64 { return repoIndex[repo]*1000000 + int64(number) }

func fakeIssueOf(id int64) (repo string, number int, ok bool) {
	repo, ok = repoByIndex[id/1000000]
	return repo, int(id % 1000000), ok
}

func issueKey(repo string, number int) string { return repo + "#" + strconv.Itoa(number) }

func splitIssueKey(key string) (string, int) {
	repo, numeric, _ := strings.Cut(key, "#")
	number, _ := strconv.Atoi(numeric)
	return repo, number
}

func issueRefJSON(repo string, number int, title string) string {
	return fmt.Sprintf(`{"id":%d,"number":%d,"title":%q,"state":"open",`+
		`"html_url":"https://github.com/%s/issues/%d","repository":{"full_name":%q}}`,
		fakeIssueID(repo, number), number, title, repo, number, repo)
}

func repoAndRest(path string) (repo, rest string, ok bool) {
	tail, ok := strings.CutPrefix(path, "/api/v3/repos/")
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(tail, "/", 3)
	if len(parts) < 3 {
		return "", "", false
	}
	repo = parts[0] + "/" + parts[1]
	if _, known := repoIndex[repo]; !known {
		return "", "", false
	}
	return repo, parts[2], true
}

// issueNumberPath matches "issues/NUMBER" exactly, the route that resolves one issue's database id.
func issueNumberPath(rest string) (int, bool) {
	tail, ok := strings.CutPrefix(rest, "issues/")
	if !ok || tail == "" || strings.Contains(tail, "/") {
		return 0, false
	}
	number, err := strconv.Atoi(tail)
	return number, err == nil && number > 0
}

// issueSubPath matches "issues/NUMBER/TAIL", the sub-issue and issue dependency routes below one issue.
func issueSubPath(rest string) (number int, tail string, ok bool) {
	cut, ok := strings.CutPrefix(rest, "issues/")
	if !ok {
		return 0, "", false
	}
	digits, tail, found := strings.Cut(cut, "/")
	if !found {
		return 0, "", false
	}
	number, err := strconv.Atoi(digits)
	return number, tail, err == nil && number > 0
}

func bodyInt64(body map[string]any, key string) (int64, bool) {
	value, ok := body[key].(float64)
	return int64(value), ok
}

func removeID(ids []int64, id int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// reorderID places id in without just after after (when hasAfter) or just before before (when hasBefore), or
// last when neither sibling is found.
func reorderID(without []int64, id, after, before int64, hasAfter, hasBefore bool) []int64 {
	place := func(mark int64, offset int) []int64 {
		for i, v := range without {
			if v == mark {
				out := append([]int64{}, without[:i+offset]...)
				out = append(out, id)
				return append(out, without[i+offset:]...)
			}
		}
		return nil
	}
	if hasAfter {
		if out := place(after, 1); out != nil {
			return out
		}
	}
	if hasBefore {
		if out := place(before, 0); out != nil {
			return out
		}
	}
	return append(append([]int64{}, without...), id)
}

// fakeIssueGraph answers the sub-issue and issue dependency REST routes of every repository these tests use,
// through the failure hook of fakeGitHub. children and blockedBy are keyed by issueKey and hold the ordered
// fake database ids of the sub-issues or the blocking issues.
type fakeIssueGraph struct {
	mu        sync.Mutex
	f         *fakeGitHub
	titles    map[string]string
	children  map[string][]int64
	blockedBy map[string][]int64
}

func serveIssueGraph(t *testing.T) (*fakeGitHub, *fakeIssueGraph, string) {
	t.Helper()
	g := &fakeIssueGraph{titles: map[string]string{}, children: map[string][]int64{}, blockedBy: map[string][]int64{}}
	f := &fakeGitHub{}
	g.f = f
	f.failure = g.route
	return f, g, serve(t, f)
}

func (g *fakeIssueGraph) title(repo string, number int) string {
	if title, ok := g.titles[issueKey(repo, number)]; ok {
		return title
	}
	return fmt.Sprintf("Issue %d", number)
}

func (g *fakeIssueGraph) parentJSON(number, total int) string {
	return fmt.Sprintf(`{"number":%d,"sub_issues_summary":{"total":%d,"completed":0,"percent_completed":0}}`,
		number, total)
}

func (g *fakeIssueGraph) route(w http.ResponseWriter, req *http.Request) bool {
	repo, rest, ok := repoAndRest(req.URL.Path)
	if !ok {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var body map[string]any
	if req.Method != http.MethodGet {
		if requests := g.f.recorded(); len(requests) > 0 {
			body = requests[len(requests)-1].body
		}
	}
	w.Header().Set("Content-Type", "application/json")

	if req.Method == http.MethodGet {
		if number, ok := issueNumberPath(rest); ok {
			fmt.Fprint(w, issueRefJSON(repo, number, g.title(repo, number)))
			return true
		}
	}
	number, tail, ok := issueSubPath(rest)
	if !ok {
		return false
	}
	key := issueKey(repo, number)
	switch {
	case req.Method == http.MethodGet && tail == "sub_issues":
		items := make([]string, 0, len(g.children[key]))
		for _, id := range g.children[key] {
			childRepo, childNumber, _ := fakeIssueOf(id)
			items = append(items, issueRefJSON(childRepo, childNumber, g.title(childRepo, childNumber)))
		}
		fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	case req.Method == http.MethodPost && tail == "sub_issues":
		id, _ := bodyInt64(body, "sub_issue_id")
		g.children[key] = append(g.children[key], id)
		fmt.Fprint(w, g.parentJSON(number, len(g.children[key])))
	case req.Method == http.MethodDelete && tail == "sub_issue":
		id, _ := bodyInt64(body, "sub_issue_id")
		g.children[key] = removeID(g.children[key], id)
		fmt.Fprint(w, g.parentJSON(number, len(g.children[key])))
	case req.Method == http.MethodPatch && tail == "sub_issues/priority":
		id, _ := bodyInt64(body, "sub_issue_id")
		after, hasAfter := bodyInt64(body, "after_id")
		before, hasBefore := bodyInt64(body, "before_id")
		g.children[key] = reorderID(removeID(g.children[key], id), id, after, before, hasAfter, hasBefore)
		fmt.Fprint(w, g.parentJSON(number, len(g.children[key])))
	case req.Method == http.MethodGet && tail == "dependencies/blocked_by":
		items := make([]string, 0, len(g.blockedBy[key]))
		for _, id := range g.blockedBy[key] {
			blockerRepo, blockerNumber, _ := fakeIssueOf(id)
			items = append(items, issueRefJSON(blockerRepo, blockerNumber, g.title(blockerRepo, blockerNumber)))
		}
		fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	case req.Method == http.MethodGet && tail == "dependencies/blocking":
		items := []string{}
		for otherKey, blockers := range g.blockedBy {
			if containsID(blockers, fakeIssueID(repo, number)) {
				otherRepo, otherNumber := splitIssueKey(otherKey)
				items = append(items, issueRefJSON(otherRepo, otherNumber, g.title(otherRepo, otherNumber)))
			}
		}
		fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	case req.Method == http.MethodPost && tail == "dependencies/blocked_by":
		id, _ := bodyInt64(body, "issue_id")
		g.blockedBy[key] = append(g.blockedBy[key], id)
		fmt.Fprint(w, issueRefJSON(repo, number, g.title(repo, number)))
	case req.Method == http.MethodDelete && strings.HasPrefix(tail, "dependencies/blocked_by/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(tail, "dependencies/blocked_by/"), 10, 64)
		g.blockedBy[key] = removeID(g.blockedBy[key], id)
		fmt.Fprint(w, issueRefJSON(repo, number, g.title(repo, number)))
	default:
		return false
	}
	return true
}

// issueGraphConfig binds a connection whose targets allow both octo-org/example and octo-org/other, so a
// sub-issue or a dependency tool can be proven to accept a sub-issue or a blocking issue in either, and to
// refuse one in a third repository the targets never name, before any credential is resolved.
func issueGraphConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/example", "repos/octo-org/other"}, Permissions: all}
	return cfg
}

// Every sub-issue tool satisfies its output contract through the application core once confirmed: listing,
// adding, removing, and reprioritizing, in the bound repository and, for add, in another repository the
// connection's targets allow as well.
func TestSubIssuesToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, g, base := serveIssueGraph(t)
	g.children[issueKey("octo-org/example", 42)] = []int64{fakeIssueID("octo-org/example", 43),
		fakeIssueID("octo-org/example", 44)}
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, subIssuesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil || !strings.Contains(string(listed), `"number":43`) || !strings.Contains(string(listed), `"number":44`) {
		t.Fatalf("list = %s, %v", listed, err)
	}

	added, err := invoke(t, core, subIssuesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":42,"sub_issue_number":45}`, true)
	if err != nil || !strings.Contains(string(added), `"number":45`) ||
		!strings.Contains(string(added), `"total":3`) {
		t.Fatalf("add = %s, %v", added, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.method != http.MethodPost ||
		!strings.HasSuffix(last.path, "/issues/42/sub_issues") ||
		last.body["sub_issue_id"] != float64(fakeIssueID("octo-org/example", 45)) {
		t.Errorf("add request = %+v", last)
	}

	// A sub-issue in another repository the connection's targets allow is added the same way, and its
	// repository is reported because it differs from the parent's.
	addedOther, err := invoke(t, core, subIssuesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":42,"sub_issue_number":9,"sub_issue_repository":"octo-org/other"}`, true)
	if err != nil || !strings.Contains(string(addedOther), `"repository":"octo-org/other"`) {
		t.Fatalf("cross-repository add = %s, %v", addedOther, err)
	}

	removed, err := invoke(t, core, subIssuesRemove.ID, "repo",
		`{"repository":"octo-org/example","number":42,"sub_issue_number":43}`, true)
	if err != nil || !strings.Contains(string(removed), `"removed":true`) {
		t.Fatalf("remove = %s, %v", removed, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.method != http.MethodDelete ||
		!strings.HasSuffix(last.path, "/issues/42/sub_issue") {
		t.Errorf("remove request = %+v", last)
	}

	reprioritized, err := invoke(t, core, subIssuesReprioritize.ID, "repo",
		`{"repository":"octo-org/example","number":42,"sub_issue_number":45,"after_number":44}`, true)
	if err != nil || !strings.Contains(string(reprioritized), `"number":45`) {
		t.Fatalf("reprioritize = %s, %v", reprioritized, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.method != http.MethodPatch ||
		!strings.HasSuffix(last.path, "/issues/42/sub_issues/priority") ||
		last.body["after_id"] != float64(fakeIssueID("octo-org/example", 44)) {
		t.Errorf("reprioritize request = %+v", last)
	}
	final, err := invoke(t, core, subIssuesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if at44, at45 := strings.Index(string(final), `"number":44`), strings.Index(string(final), `"number":45`); at44 < 0 ||
		at45 < 0 || at44 > at45 {
		t.Errorf("sub-issues after reprioritize = %s, want 44 before 45", final)
	}
}

// A sub-issue in a repository the connection's targets do not name is refused before any credential is
// resolved, whatever its number, since the fake never serves octo-org/blocked.
func TestSubIssuesRefuseARepositoryOutsideTheTargetsBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	before := len(f.recorded())
	_, err := invoke(t, core, subIssuesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":42,"sub_issue_number":1,"sub_issue_repository":"octo-org/blocked"}`,
		true)
	if !isInvalidRequest(err) {
		t.Errorf("add outside the targets = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("add outside the targets reached the credential or GitHub")
	}
}

// Every check of the sub-issue tools runs before a credential is resolved.
func TestSubIssuesArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, id, arguments string
	}{
		{"missing number", subIssuesList.ID, `{"repository":"octo-org/example"}`},
		{"missing sub_issue_number", subIssuesAdd.ID, `{"repository":"octo-org/example","number":42}`},
		{"malformed sub_issue_repository", subIssuesAdd.ID,
			`{"repository":"octo-org/example","number":42,"sub_issue_number":1,"sub_issue_repository":"not-a-repo"}`},
		{"neither after nor before", subIssuesReprioritize.ID,
			`{"repository":"octo-org/example","number":42,"sub_issue_number":43}`},
		{"both after and before", subIssuesReprioritize.ID,
			`{"repository":"octo-org/example","number":42,"sub_issue_number":43,"after_number":44,"before_number":45}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.id, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.id)
		}
	}
}

// A change of a sub-issue relationship needs its own confirmation and is refused without one, before it
// reaches GitHub.
func TestSubIssuesChangesNeedConfirmation(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	unconfirmed := &application.ConfirmationRequiredError{}
	for _, tt := range []struct{ id, arguments string }{
		{subIssuesAdd.ID, `{"repository":"octo-org/example","number":42,"sub_issue_number":1}`},
		{subIssuesRemove.ID, `{"repository":"octo-org/example","number":42,"sub_issue_number":1}`},
		{subIssuesReprioritize.ID, `{"repository":"octo-org/example","number":42,"sub_issue_number":1,"after_number":2}`},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", tt.id, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", tt.id)
		}
	}
}

// A sub-issue whose repository lies outside the connection's targets is withheld from sub_issues and only
// counted in withheld, while a sub-issue in the bound repository or in another repository the targets allow
// is still listed; a connection without targets withholds nothing.
func TestSubIssuesListWithholdsSubIssuesOutsideTheTargets(t *testing.T) {
	_, g, base := serveIssueGraph(t)
	g.children[issueKey("octo-org/example", 42)] = []int64{
		fakeIssueID("octo-org/example", 43),
		fakeIssueID("octo-org/other", 9),
		fakeIssueID("octo-org/blocked", 99),
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, subIssuesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(listed), `"number":43`) ||
		!strings.Contains(string(listed), `"repository":"octo-org/other"`) {
		t.Fatalf("list = %s, want the bound and the allowed cross-repository sub-issue", listed)
	}
	if strings.Contains(string(listed), `"octo-org/blocked"`) {
		t.Fatalf("list = %s, leaked a sub-issue outside the targets", listed)
	}
	if !strings.Contains(string(listed), `"withheld":1`) {
		t.Fatalf("list = %s, want withheld:1", listed)
	}

	unrestricted := coreConfig(base)
	unrestricted.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader"}
	coreUnrestricted := application.New(registry(t), unrestricted, resolver(red, nil), red)
	all, err := invoke(t, coreUnrestricted, subIssuesList.ID, "repo",
		`{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(all), `"octo-org/blocked"`) || strings.Contains(string(all), `"withheld"`) {
		t.Fatalf("unrestricted list = %s, want the withheld sub-issue included and no withheld field", all)
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a sub-issue
// tool.
func TestSubIssuesRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	before := len(f.recorded())
	if _, err := invoke(t, core, subIssuesList.ID, "planning", `{"number":42}`, false); !isInvalidRequest(err) {
		t.Errorf("list on a project-scoped connection = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("list on a project-scoped connection reached the credential or GitHub")
	}
}
