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

const documentedMilestone = `{"url":"https://api.github.com/repos/octocat/Hello-World/milestones/1",` +
	`"html_url":"https://github.com/octocat/Hello-World/milestones/v1.0",` +
	`"labels_url":"https://api.github.com/repos/octocat/Hello-World/milestones/1/labels","id":1002604,` +
	`"node_id":"MDk6TWlsZXN0b25lMTAwMjYwNA==","number":1,"state":"open","title":"v1.0",` +
	`"description":"Tracking milestone for version 1.0","creator":` + documentedAccount + `,"open_issues":4,` +
	`"closed_issues":8,"created_at":"2011-04-10T20:09:31Z","updated_at":"2014-03-03T18:58:10Z",` +
	`"closed_at":"2013-02-12T13:22:01Z","due_on":"2012-10-09T23:39:01Z"}`

// github.milestones.list reports every member GitHub documents for a milestone; a milestone without the new
// members keeps its earlier output exactly. The result is encoded with sorted keys.
func TestMilestonesListReportsEveryDocumentedMember(t *testing.T) {
	const want = `{"has_more":false,"milestones":[{"closed_at":"2013-02-12T13:22:01Z","closed_issues":8,` +
		`"created_at":"2011-04-10T20:09:31Z","creator":{"avatar_url":"https://github.com/images/error/octocat_happy.gif",` +
		`"gravatar_id":"","id":1,"links":{"followers_url":"https://api.github.com/users/octocat/followers",` +
		`"url":"https://api.github.com/users/octocat"},"login":"octocat","node_id":"MDQ6VXNlcjE=","site_admin":false,` +
		`"type":"User","url":"https://github.com/octocat"},"description":"Tracking milestone for version 1.0",` +
		`"due_on":"2012-10-09T23:39:01Z","id":1002604,"links":{"labels_url":` +
		`"https://api.github.com/repos/octocat/Hello-World/milestones/1/labels","url":` +
		`"https://api.github.com/repos/octocat/Hello-World/milestones/1"},"node_id":"MDk6TWlsZXN0b25lMTAwMjYwNA==",` +
		`"number":1,"open_issues":4,"state":"open","title":"v1.0","updated_at":"2014-03-03T18:58:10Z",` +
		`"url":"https://github.com/octocat/Hello-World/milestones/v1.0"}],"repository":"octo-org/example"}`
	const oldWant = `{"has_more":false,"milestones":[{"closed_issues":5,"description":"d","due_on":"2026-03-01T00:00:00Z",` +
		`"number":1,"open_issues":2,"state":"open","title":"v1"}],"repository":"octo-org/example"}`
	const oldBody = `[{"number":1,"title":"v1","state":"open","description":"d","due_on":"2026-03-01T00:00:00Z",` +
		`"open_issues":2,"closed_issues":5,"closed_at":null,"creator":null}]`
	red := &redact.Redactor{}
	for _, c := range []struct{ body, want string }{{`[` + documentedMilestone + `]`, want}, {oldBody, oldWant}} {
		base := serveRoute(t, "/api/v3/repos/octo-org/example/milestones", c.body)
		core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
		got, err := invoke(t, core, milestonesList.ID, "repo", `{}`, false)
		if err != nil || string(got) != c.want {
			t.Errorf("milestones.list = %s, %v, want %s", got, err, c.want)
		}
	}
}

// sort and direction reach GitHub only when given and bind the cursor: a cursor of another filter is refused.
func TestMilestonesListSortAndDirectionBindTheRequestAndTheCursor(t *testing.T) {
	f, _, base := serveMilestones(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	if _, err := invoke(t, core, milestonesList.ID, "repo", `{"state":"all"}`, false); err != nil {
		t.Fatal(err)
	}
	last := func() string { r := f.recorded(); return r[len(r)-1].query }
	if query := last(); strings.Contains(query, "sort") || strings.Contains(query, "direction") {
		t.Errorf("query without the filters = %q", query)
	}

	first, err := invoke(t, core, milestonesList.ID, "repo",
		`{"state":"all","sort":"completeness","direction":"desc","limit":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if query := last(); !strings.Contains(query, "sort=completeness") || !strings.Contains(query, "direction=desc") {
		t.Errorf("query with the filters = %q", query)
	}
	var page MilestoneList
	if json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page = %s", first)
	}
	for _, other := range []string{
		`{"state":"all","sort":"completeness","direction":"asc"`,
		`{"state":"all","sort":"due_on","direction":"desc"`,
		`{"state":"all","sort":"completeness"`,
		`{"state":"all"`,
	} {
		if _, err := invoke(t, core, milestonesList.ID, "repo", other+`,"cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
			t.Errorf("cursor with %s = %v, want an invalid request", other, err)
		}
	}
	next, err := invoke(t, core, milestonesList.ID, "repo",
		`{"state":"all","sort":"completeness","direction":"desc","cursor":"`+page.NextCursor+`"}`, false)
	if err != nil || !strings.Contains(string(next), `"title":"v2"`) {
		t.Errorf("continued page = %s, %v", next, err)
	}

	reads := 0
	counting := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)
	for _, bad := range []string{`{"sort":"title"}`, `{"direction":"up"}`} {
		if _, err := invoke(t, counting, milestonesList.ID, "repo", bad, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", bad, err)
		}
	}
	if reads != 0 {
		t.Error("an invalid filter reached the credential")
	}
}

// Title, state, description, and due date of a milestone keep their earlier handling, as decoded on ebf8628:
// no cleaning and no cut.
func TestMilestonesListKeepsTheEarlierHandlingOfTheCompactMembers(t *testing.T) {
	body, _ := json.Marshal([]map[string]any{{"number": 1, "title": longText1500, "state": longText1500,
		"description": longText1500, "due_on": longText1500, "open_issues": 1, "closed_issues": 2}})
	base := serveRoute(t, "/api/v3/repos/octo-org/example/milestones", string(body))
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	got, err := invoke(t, core, milestonesList.ID, "repo", `{}`, false)
	var list MilestoneList
	if err != nil || json.Unmarshal(got, &list) != nil || len(list.Milestones) != 1 {
		t.Fatalf("milestones.list = %s, %v", got, err)
	}
	m := list.Milestones[0]
	if m.Title != longText1500 || m.State != longText1500 || m.Description != longText1500 || m.DueOn != longText1500 {
		t.Error("milestones.list changed a compact member")
	}
}
