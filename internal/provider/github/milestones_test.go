package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeMilestoneEntry is one milestone of the fake repository.
type fakeMilestoneEntry struct {
	number                   int
	title, state, dueOn      string
	openIssues, closedIssues int
}

// fakeMilestones answers the milestone list route of the bound repository through the failure hook of
// fakeGitHub, the way fakeLabels answers the label routes.
type fakeMilestones struct {
	mu         sync.Mutex
	milestones []fakeMilestoneEntry
}

func serveMilestones(t *testing.T) (*fakeGitHub, *fakeMilestones, string) {
	t.Helper()
	m := &fakeMilestones{milestones: []fakeMilestoneEntry{
		{number: 1, title: "v1", state: "open", dueOn: "2026-03-01T00:00:00Z", openIssues: 2, closedIssues: 5},
		{number: 2, title: "v2", state: "closed", openIssues: 0, closedIssues: 8},
	}}
	f := &fakeGitHub{}
	f.failure = m.route
	return f, m, serve(t, f)
}

func milestoneJSON(e fakeMilestoneEntry) string {
	dueOn := "null"
	if e.dueOn != "" {
		dueOn = strconv.Quote(e.dueOn)
	}
	return fmt.Sprintf(`{"number":%d,"title":%q,"state":%q,"due_on":%s,"open_issues":%d,"closed_issues":%d}`,
		e.number, e.title, e.state, dueOn, e.openIssues, e.closedIssues)
}

func (m *fakeMilestones) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || rest != "milestones" || req.Method != http.MethodGet {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	state := req.URL.Query().Get("state")
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	page, _ := strconv.Atoi(req.URL.Query().Get("page"))
	matching := make([]fakeMilestoneEntry, 0, len(m.milestones))
	for _, entry := range m.milestones {
		if state == "" || state == "all" || entry.state == state {
			matching = append(matching, entry)
		}
	}
	start := min((page-1)*perPage, len(matching))
	end := min(start+perPage, len(matching))
	entries := make([]string, 0, end-start)
	for _, entry := range matching[start:end] {
		entries = append(entries, milestoneJSON(entry))
	}
	if end < len(matching) {
		w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
	return true
}

// github.milestones.list satisfies its output contract through the application core, filters by state, and
// is paged the same way labels are, by the Link response header.
func TestMilestonesListSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f, _, base := serveMilestones(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	open, err := invoke(t, core, milestonesList.ID, "repo", `{"state":"open"}`, false)
	if err != nil || !strings.Contains(string(open), `"title":"v1"`) || strings.Contains(string(open), `"title":"v2"`) {
		t.Fatalf("list open = %s, %v", open, err)
	}
	var openPage MilestoneList
	if json.Unmarshal(open, &openPage) != nil || len(openPage.Milestones) != 1 || openPage.HasMore ||
		openPage.Milestones[0].Number != 1 || openPage.Milestones[0].DueOn == "" ||
		openPage.Milestones[0].OpenIssues != 2 || openPage.Milestones[0].ClosedIssues != 5 {
		t.Fatalf("open page = %+v", openPage)
	}

	all, err := invoke(t, core, milestonesList.ID, "repo", `{"state":"all"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var allPage MilestoneList
	if json.Unmarshal(all, &allPage) != nil || len(allPage.Milestones) != 2 {
		t.Fatalf("all page = %s", all)
	}

	// Without a state, only open milestones are listed, the way GitHub itself defaults.
	defaulted, err := invoke(t, core, milestonesList.ID, "repo", `{}`, false)
	if err != nil || !strings.Contains(string(defaulted), `"title":"v1"`) || strings.Contains(string(defaulted), `"title":"v2"`) {
		t.Fatalf("default list = %s, %v", defaulted, err)
	}
	if len(f.recorded()) == 0 {
		t.Fatal("no request recorded")
	}
}

// state is refused before a credential is resolved, and a project-scoped connection never reaches GitHub.
func TestMilestonesListValidatesArgumentsAndTargetBeforeIO(t *testing.T) {
	_, _, base := serveMilestones(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)

	if _, err := invoke(t, core, milestonesList.ID, "repo", `{"state":"merged"}`, false); !isInvalidRequest(err) {
		t.Errorf("invalid state = %v, want an invalid request", err)
	}
	if reads != 0 {
		t.Error("an invalid state reached the credential")
	}
	reads = 0
	if _, err := invoke(t, core, milestonesList.ID, "planning", `{}`, false); !isInvalidRequest(err) {
		t.Errorf("milestones.list on a project-scoped connection = %v, want an invalid request", err)
	}
	if reads != 0 {
		t.Error("a project-scoped connection reached the credential")
	}
}
