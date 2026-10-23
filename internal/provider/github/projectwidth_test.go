package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// metaFake answers every GraphQL document with what answer returns for it; the other routes stay unreached.
func metaFake(answer func(document string, variables map[string]any) string) *fakeGitHub {
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		requests := f.recorded()
		last := requests[len(requests)-1]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, answer(last.document, last.variables))
		return true
	}
	return f
}

func metaCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	base := serve(t, f)
	red := &redact.Redactor{}
	return application.New(registry(t), ownersConfig(base), resolver(red, reads), red)
}

// metaOut decodes the answer of a tool so a test reads its members by path.
func metaOut(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func checkMetaPaths(t *testing.T, out map[string]any, want map[string]any) {
	t.Helper()
	for path, value := range want {
		if got := walkPath(t, out, path); got != value {
			t.Errorf("%s = %v (%T), want %v", path, got, got, value)
		}
	}
}

func wideProject(readme string) string {
	repositories := `{"nameWithOwner":"octo-org/example","url":"https://github.com/octo-org/example"},` +
		`{"nameWithOwner":"other-org/secret","url":"https://github.com/other-org/secret"}` +
		strings.Repeat(`,{"nameWithOwner":"octo-org/more","url":"https://github.com/octo-org/more"}`, 9)
	return `{"number":3,"title":"Roadmap","url":"https://github.com/orgs/octo-org/projects/3","closed":true,` +
		`"id":"PVT_3","fullDatabaseId":"9007199254740993","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-02-01T00:00:00Z","closedAt":"2026-03-01T00:00:00Z","creator":` + wideAccount + `,` +
		`"public":true,"readme":` + fmt.Sprintf("%q", readme) + `,"shortDescription":"Short\u0007 text",` +
		`"template":false,"viewerCanClose":true,"viewerCanReopen":false,"viewerCanUpdate":true,` +
		`"resourcePath":"/orgs/octo-org/projects/3","repositories":{"totalCount":12,"nodes":[` + repositories + `]},` +
		`"items":{"totalCount":40},"fields":{"totalCount":9},"views":{"totalCount":3},"workflows":{"totalCount":4},` +
		`"statusUpdates":{"totalCount":2},"teams":{"totalCount":1}}`
}

func projectPage(nodes ...string) string {
	edges := make([]string, len(nodes))
	for i, node := range nodes {
		edges[i] = fmt.Sprintf(`{"cursor":"c%d","node":%s}`, i, node)
	}
	return `{"data":{"owner":{"projectsV2":{"pageInfo":{"hasNextPage":false,"endCursor":"c0"},"edges":[` +
		strings.Join(edges, ",") + `]}}}}`
}

func TestProjectListReportsEveryMemberOfTheProject(t *testing.T) {
	long := strings.Repeat("r", projectionBodyLimit+20)
	f := metaFake(func(string, map[string]any) string { return projectPage(wideProject(long)) })
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, projectsList.ID, "org", `{"team_counts":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	var schema map[string]any
	if err := json.Unmarshal(projectsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	withoutOwner := map[string]any{"projects": out["projects"], "has_more": out["has_more"]}
	checkSchemaCovers(t, "$", withoutOwner, schema)
	checkMetaPaths(t, out, map[string]any{
		"projects.0.project": "orgs/octo-org/projects/3", "projects.0.closed": true,
		"projects.0.id": "PVT_3", "projects.0.full_database_id": "9007199254740993",
		"projects.0.created_at": "2026-01-01T00:00:00Z", "projects.0.updated_at": "2026-02-01T00:00:00Z",
		"projects.0.closed_at": "2026-03-01T00:00:00Z", "projects.0.creator.login": "octocat",
		"projects.0.creator.email": "octocat@example.com", "projects.0.public": true,
		"projects.0.short_description": "Short  text", "projects.0.readme_truncated": true,
		"projects.0.template": false, "projects.0.viewer_can_close": true, "projects.0.viewer_can_reopen": false,
		"projects.0.viewer_can_update": true, "projects.0.links.resource_path": "/orgs/octo-org/projects/3",
		"projects.0.repositories_total_count": float64(12), "projects.0.repositories_truncated": true,
		"projects.0.repositories.0.name_with_owner": "octo-org/example",
		"projects.0.repositories.0.url":             "https://github.com/octo-org/example",
		"projects.0.repositories.1.name_with_owner": "other-org/secret",
		"projects.0.items_total_count":              float64(40), "projects.0.fields_total_count": float64(9),
		"projects.0.views_total_count": float64(3), "projects.0.workflows_total_count": float64(4),
		"projects.0.status_updates_total_count": float64(2), "projects.0.teams_total_count": float64(1),
	})
	if got := len([]rune(walkPath(t, out, "projects.0.readme").(string))); got != projectionBodyLimit {
		t.Errorf("readme has %d characters, want %d", got, projectionBodyLimit)
	}
	if got := len(walkPath(t, out, "projects.0.repositories").([]any)); got != repositoryListCap {
		t.Errorf("repositories = %d, want %d", got, repositoryListCap)
	}
	// A repository outside the targets of the connection keeps its name only.
	if _, ok := walkPath(t, out, "projects.0.repositories.1").(map[string]any)["url"]; ok {
		t.Error("a repository outside the targets shows its address")
	}
	if strings.Contains(string(raw), "\u0007") {
		t.Error("a control character reached the output")
	}

	entry, _ := json.Marshal(out["projects"].([]any)[0])
	assertSelectionMatchesOutput(t, "number title url closed "+projectWidthSelection+projectTeamsSelection, entry,
		entrySchema(t, projectsList.OutputSchema, "projects"),
		[]string{"project", "type", "links", "readme_truncated", "repositories_truncated", "repositories_total_count",
			"items_total_count", "fields_total_count", "views_total_count", "workflows_total_count",
			"status_updates_total_count", "teams_total_count"},
		[]string{"resourcePath", "first", "totalCount", "nodes", "items", "fields", "views", "workflows",
			"statusUpdates", "teams"})
	for _, banned := range []string{"HTML", "owner{", "view(", "field(", "workflow("} {
		if strings.Contains(projectWidthSelection, banned) {
			t.Errorf("the selection asks for %s", banned)
		}
	}
}

func TestProjectListWithoutTheWidthIsUnchanged(t *testing.T) {
	f := metaFake(func(string, map[string]any) string {
		return projectPage(`{"number":1,"title":"Plan 1","url":"https://github.com/orgs/octo-org/projects/1","closed":false}`)
	})
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, projectsList.ID, "org", `{}`, false)
	if want := `{"has_more":false,"owner":"orgs/octo-org","projects":[{"closed":false,"number":1,` +
		`"project":"orgs/octo-org/projects/1","title":"Plan 1","url":"https://github.com/orgs/octo-org/projects/1"}]}`; err != nil ||
		string(raw) != want {
		t.Errorf("result = %s, %v; want %s", raw, err, want)
	}
}

func TestProjectListFiltersTravelAsVariables(t *testing.T) {
	f := metaFake(func(string, map[string]any) string { return projectPage() })
	core := metaCore(t, f, nil)
	for _, tt := range []struct {
		name, arguments string
		want            map[string]string
	}{
		{"no argument", `{}`, map[string]string{"orderBy": "map[direction:ASC field:NUMBER]", "query": "<nil>",
			"minPermissionLevel": "<nil>"}},
		{"every filter", `{"query":"road \"map\"","order_by":"TITLE","direction":"DESC","min_permission_level":"ADMIN"}`,
			map[string]string{"orderBy": "map[direction:DESC field:TITLE]", "query": `road "map"`,
				"minPermissionLevel": "ADMIN"}},
		{"a direction alone", `{"direction":"DESC"}`, map[string]string{"orderBy": "map[direction:DESC field:NUMBER]"}},
		{"an order alone", `{"order_by":"UPDATED_AT"}`, map[string]string{"orderBy": "map[direction:ASC field:UPDATED_AT]"}},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, projectsList.ID, "org", tt.arguments, false); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		request := f.recorded()[before]
		for name, want := range tt.want {
			if got := fmt.Sprint(request.variables[name]); got != want {
				t.Errorf("%s: variable %s = %s, want %s", tt.name, name, got, want)
			}
		}
		for _, text := range []string{"road", "TITLE", "ADMIN"} {
			if strings.Contains(request.document, text) {
				t.Errorf("%s: the document holds the value %q", tt.name, text)
			}
		}
	}
}

func TestProjectListRefusalsTouchNeitherTheSecretNorGitHub(t *testing.T) {
	f := metaFake(func(string, map[string]any) string { return projectPage() })
	reads := 0
	core := metaCore(t, f, &reads)
	invalid := &provider.InvalidRequestError{}
	for name, arguments := range map[string]string{
		"a long query":         `{"query":"` + strings.Repeat("q", maxProjectQuery+1) + `"}`,
		"a control character":  `{"query":"a\u0007b"}`,
		"a line break":         `{"query":"a\nb"}`,
		"an unknown order":     `{"order_by":"STARS"}`,
		"a lower case order":   `{"order_by":"title"}`,
		"an unknown direction": `{"direction":"UP"}`,
		"an unknown level":     `{"min_permission_level":"OWNER"}`,
	} {
		_, err := invoke(t, core, projectsList.ID, "org", arguments, false)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if got, want := fmt.Sprintf("%T", err), fmt.Sprintf("%T", invalid); got != want {
			t.Errorf("%s: err = %s, want %s", name, got, want)
		}
	}
	if reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded()))
	}
	if _, err := invoke(t, core, projectsList.ID, "org", `{"query":"`+strings.Repeat("q", maxProjectQuery)+`"}`,
		false); err != nil {
		t.Errorf("a query of the largest length = %v", err)
	}
}

// A cursor made without the new arguments stays valid, and a cursor made with them belongs to them.
func TestProjectListCursorsAreBoundToTheFilters(t *testing.T) {
	f := metaFake(func(document string, variables map[string]any) string {
		return `{"data":{"owner":{"projectsV2":{"pageInfo":{"hasNextPage":true,"endCursor":"gh-1"},"edges":[` +
			`{"cursor":"gh-1","node":{"number":1,"title":"Plan","url":"https://github.com/orgs/octo-org/projects/1","closed":false}},` +
			`{"cursor":"gh-2","node":{"number":2,"title":"Plan","url":"https://github.com/orgs/octo-org/projects/2","closed":false}}]}}}}`
	})
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, projectsList.ID, "org", `{"limit":1}`, false)
	cursor := nextCursorOf(raw)
	if err != nil || cursor == "" {
		t.Fatalf("first batch = %s, %v", raw, err)
	}
	// The binding of an unfiltered list is the one the list had before the filters existed.
	owner := target{kind: kindOwner, scope: "orgs", owner: "octo-org"}
	var options projectListOptions
	options.Limit = 1
	binding, _, err := options.normalize(owner)
	if err != nil || string(binding) != string(fingerprint("projects", strings.ToLower(owner.String()))) {
		t.Errorf("binding of an unfiltered list changed: %v", err)
	}
	for name, arguments := range map[string]string{
		"the same list":       `{"limit":1,"cursor":"` + cursor + `"}`,
		"the default spelled": `{"limit":1,"order_by":"NUMBER","direction":"ASC","cursor":"` + cursor + `"}`,
	} {
		if _, err := invoke(t, core, projectsList.ID, "org", arguments, false); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, arguments := range map[string]string{
		"another query":     `{"query":"x","cursor":"` + cursor + `"}`,
		"another order":     `{"order_by":"TITLE","cursor":"` + cursor + `"}`,
		"another direction": `{"direction":"DESC","cursor":"` + cursor + `"}`,
		"another level":     `{"min_permission_level":"WRITE","cursor":"` + cursor + `"}`,
	} {
		if _, err := invoke(t, core, projectsList.ID, "org", arguments, false); err == nil {
			t.Errorf("%s: the cursor was accepted", name)
		}
	}
	// A cursor of a filtered list continues only that list.
	raw, err = invoke(t, core, projectsList.ID, "org", `{"limit":1,"query":"plan","order_by":"TITLE"}`, false)
	filtered := nextCursorOf(raw)
	if err != nil || filtered == "" {
		t.Fatalf("filtered batch = %s, %v", raw, err)
	}
	if _, err := invoke(t, core, projectsList.ID, "org", `{"limit":1,"query":"plan","order_by":"TITLE","cursor":"`+
		filtered+`"}`, false); err != nil {
		t.Errorf("the filtered continuation = %v", err)
	}
	if _, err := invoke(t, core, projectsList.ID, "org", `{"limit":1,"cursor":"`+filtered+`"}`, false); err == nil {
		t.Error("a filtered cursor continued the unfiltered list")
	}
}

// ---- Node limit

var topConnection = regexp.MustCompile(`\(first:\$first,[^)]*\)`)

// The lists ask for the most nodes at the largest page: 100 entries, each with the connections of its width.
// GitHub counts every requested entry of a connection; a connection read for its count alone adds none, which
// the estimate counts as one node each. The estimates stay far below GitHub's node limit of 500000.
func TestMetadataQueriesStayBelowTheNodeLimit(t *testing.T) {
	const nodeLimit = 500000
	projects := topConnection.ReplaceAllString(ownerListHead+projectsDeclarations+"){owner:organization"+
		projectsSelection(true), "(first:100)")
	teams := topConnection.ReplaceAllString(teamsQuery("organization"), "(first:100)")
	views := `query($owner:String!,$number:Int!){owner:organization(login:$owner){projectV2(number:$number){id ` +
		viewsSelection + `}}}`
	fields := `query($owner:String!,$number:Int!){owner:organization(login:$owner){projectV2(number:$number){id ` +
		planningFieldSelection + `}}}`
	for _, tt := range []struct {
		name, document string
	}{{"projects", projects}, {"teams", teams}, {"views", views}, {"fields", fields}} {
		nodes := itemNodeEstimate(tt.document, map[string]bool{"options": true, "multiSelectOptions": true,
			"iterations": true, "completedIterations": true}, 100)
		if nodes > nodeLimit*6/10 {
			t.Errorf("the %s query asks for %d nodes, want at most %d", tt.name, nodes, nodeLimit*6/10)
		}
		t.Logf("%s: %d nodes at the largest page", tt.name, nodes)
		for _, page := range regexp.MustCompile(`first:(\d+)\)`).FindAllStringSubmatch(tt.document, -1) {
			switch page[1] {
			case "100", "20", "10":
			default:
				t.Errorf("the %s query asks for a page of %s", tt.name, page[1])
			}
		}
	}
	for _, document := range []string{projectWidthSelection + projectTeamsSelection, teamNodeSelection} {
		for _, page := range regexp.MustCompile(`first:(\d+)\)`).FindAllStringSubmatch(document, -1) {
			if page[1] != "10" {
				t.Errorf("a connection of an entry asks for %s entries, want at most 10", page[1])
			}
		}
		for _, banned := range []string{"HTML", "Html", "hovercard", "organization{", "owner{"} {
			if strings.Contains(strings.ReplaceAll(document, "organization{"+gqlOrganizationSelection, ""), banned) {
				t.Errorf("a selection asks for %s", banned)
			}
		}
	}
}

// ---- Fields, status updates, views, workflows, and teams of a project

func projectAnswer(members string) string {
	return `{"data":{"owner":{"projectV2":{"id":"PVT_7",` + members + `}}}}`
}

func TestProjectFieldsReportIdentifiersAndTimes(t *testing.T) {
	useToday(t, "2026-09-24")
	nodes := `{"id":"F_note","databaseId":5,"name":"Note","dataType":"TEXT","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-01-02T00:00:00Z","isIssueField":false},` +
		`{"id":"F_prio","databaseId":6,"name":"Priority","dataType":"SINGLE_SELECT","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-01-02T00:00:00Z","isIssueField":true,"issueField":{"id":"IF_1","name":"Priority",` +
		`"description":"Org\u0007 field","dataType":"SINGLE_SELECT","visibility":"ALL","createdAt":"2025-12-01T00:00:00Z",` +
		`"fullDatabaseId":"9007199254740993"},"options":[{"id":"O_p1","name":"P1","color":"RED","description":"Urgent"}]},` +
		`{"id":"F_sprint","databaseId":7,"name":"Sprint","dataType":"ITERATION","isIssueField":false,` +
		`"configuration":{"duration":14,"startDay":1,"iterations":[{"id":"I_s5","title":"Sprint 5",` +
		`"startDate":"2026-09-21","duration":14}],"completedIterations":[{"id":"I_s4","title":"Sprint 4",` +
		`"startDate":"2026-09-07","duration":14}]}}`
	f := metaFake(func(string, map[string]any) string { return projectAnswer(`"fields":{"nodes":[` + nodes + `]}`) })
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, fieldsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	var schema map[string]any
	if err := json.Unmarshal(fieldsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", map[string]any{"fields": out["fields"]}, schema)
	checkMetaPaths(t, out, map[string]any{
		"fields.0.id": "F_note", "fields.0.database_id": float64(5), "fields.0.created_at": "2026-01-01T00:00:00Z",
		"fields.0.updated_at": "2026-01-02T00:00:00Z", "fields.0.is_issue_field": false,
		"fields.1.is_issue_field": true, "fields.1.issue_field.id": "IF_1", "fields.1.issue_field.data_type": "SINGLE_SELECT",
		"fields.1.issue_field.description": "Org  field", "fields.1.issue_field.full_database_id": "9007199254740993",
		"fields.1.options.0.id": "O_p1", "fields.1.options.0.name": "P1",
		"fields.2.iteration.iterations.0.id": "I_s4", "fields.2.iteration.iterations.0.state": "completed",
		"fields.2.iteration.iterations.1.id": "I_s5", "fields.2.iteration.iterations.1.state": "current",
		"fields.2.iteration.completed_iterations.0.id":         "I_s4",
		"fields.2.iteration.completed_iterations.0.title":      "Sprint 4",
		"fields.2.iteration.completed_iterations.0.start_date": "2026-09-07",
		"fields.2.iteration.completed_iterations.0.duration":   float64(14),
	})
	if strings.Contains(string(raw), "\u0007") {
		t.Error("a control character reached the output")
	}
	selection := fieldNodeSelection
	entry, _ := json.Marshal(out["fields"].([]any)[1])
	assertSelectionMatchesOutput(t, selection, entry, entrySchema(t, fieldsList.OutputSchema, "fields"),
		[]string{"type", "built_in", "iteration", "completed_iterations"},
		[]string{"dataType", "multiSelectOptions", "configuration", "startDay", "completedIterations",
			"iterations", "duration", "startDate", "title"})
	for _, banned := range []string{"nameHTML", "descriptionHTML", "titleHTML", "project{", "field(", "orderBy"} {
		if strings.Contains(selection, banned) {
			t.Errorf("the selection asks for %s", banned)
		}
	}
}

func TestProjectStatusUpdatesReportTheAuthorAsAccount(t *testing.T) {
	node := `{"id":"SU_1","fullDatabaseId":"9007199254740993","status":"ON_TRACK","startDate":"2026-09-01",` +
		`"targetDate":"2026-12-18","body":"Fine","createdAt":"2026-09-20T00:00:00Z","updatedAt":"2026-09-21T00:00:00Z",` +
		`"creator":` + wideAccount + `}`
	f := metaFake(func(string, map[string]any) string {
		return projectAnswer(`"statusUpdates":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[` + node + `]}`)
	})
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, statusList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	var schema map[string]any
	if err := json.Unmarshal(statusList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", map[string]any{"status_updates": out["status_updates"], "has_more": false}, schema)
	checkMetaPaths(t, out, map[string]any{
		"status_updates.0.author": "octocat", "status_updates.0.author_detail.login": "octocat",
		"status_updates.0.author_detail.email": "octocat@example.com", "status_updates.0.author_detail.type": "User",
		"status_updates.0.full_database_id": "9007199254740993", "status_updates.0.status": "on_track",
	})
	entry, _ := json.Marshal(out["status_updates"].([]any)[0])
	assertSelectionMatchesOutput(t, statusUpdateSelection, entry, entrySchema(t, statusList.OutputSchema, "status_updates"),
		[]string{"author", "author_detail", "links", "type"}, []string{"creator", "resourcePath"})
	if strings.Contains(statusUpdateSelection, "HTML") {
		t.Error("the selection asks for a rendered variant")
	}
}

func TestProjectViewsReportFieldObjectsBesideTheNames(t *testing.T) {
	field := func(id, name string) string { return fmt.Sprintf(`{"id":%q,"name":%q}`, id, name) }
	node := `{"id":"V_1","fullDatabaseId":"9007199254740993","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-01-02T00:00:00Z","number":1,"name":"Board","layout":"BOARD_LAYOUT","filter":"status:Todo",` +
		`"configuration":{"visibleFields":{"nodes":[` + field("F_title", "Title") + `,` + field("F_status", "Status") + `]}},` +
		`"fields":{"nodes":[` + field("F_title", "Title") + `,` + field("F_status", "Status") + `]},` +
		`"groupByFields":{"nodes":[` + field("F_prio", "Priority") + `]},` +
		`"verticalGroupByFields":{"nodes":[` + field("F_status", "Status") + `]},` +
		`"sortByFields":{"nodes":[{"direction":"DESC","field":` + field("F_prio", "Priority") + `}]}}`
	f := metaFake(func(string, map[string]any) string { return projectAnswer(`"views":{"nodes":[` + node + `]}`) })
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, viewsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	var schema map[string]any
	if err := json.Unmarshal(viewsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", map[string]any{"views": out["views"]}, schema)
	checkMetaPaths(t, out, map[string]any{
		"views.0.id": "V_1", "views.0.full_database_id": "9007199254740993", "views.0.created_at": "2026-01-01T00:00:00Z",
		"views.0.updated_at": "2026-01-02T00:00:00Z", "views.0.fields.1": "Status", "views.0.fields_details.1.id": "F_status",
		"views.0.fields_details.1.name": "Status", "views.0.configuration.visible_fields.0.id": "F_title",
		"views.0.group_by.0": "Priority", "views.0.group_by_fields.0.id": "F_prio",
		"views.0.column_by.0": "Status", "views.0.vertical_group_by_fields.0.id": "F_status",
		"views.0.sort_by.0.field": "Priority", "views.0.sort_by.0.direction": "desc",
		"views.0.sort_by_fields.0.direction": "DESC", "views.0.sort_by_fields.0.field.id": "F_prio",
		"views.0.sort_by_fields.0.field.name": "Priority",
	})
	entry, _ := json.Marshal(out["views"].([]any)[0])
	assertSelectionMatchesOutput(t, viewSelection, entry, entrySchema(t, viewsList.OutputSchema, "views"),
		[]string{"fields_details", "group_by", "group_by_fields", "column_by", "vertical_group_by_fields", "sort_by",
			"sort_by_fields", "visible_fields", "name", "id"},
		[]string{"visibleFields", "groupByFields", "verticalGroupByFields", "sortByFields", "nodes", "first",
			"direction", "field", "layout"})
	for _, deprecated := range []string{"groupBy(", "verticalGroupBy(", "sortBy(", "databaseId"} {
		if strings.Contains(viewSelection, deprecated) {
			t.Errorf("the selection asks for the deprecated %s", deprecated)
		}
	}
}

func TestProjectWorkflowsReportIdentifiersAndTimes(t *testing.T) {
	node := `{"id":"W_1","fullDatabaseId":"9007199254740993","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-01-02T00:00:00Z","number":1,"name":"Item closed","enabled":true}`
	f := metaFake(func(string, map[string]any) string { return projectAnswer(`"workflows":{"nodes":[` + node + `]}`) })
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, projectWorkflowsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	checkMetaPaths(t, out, map[string]any{"workflows.0.id": "W_1", "workflows.0.full_database_id": "9007199254740993",
		"workflows.0.created_at": "2026-01-01T00:00:00Z", "workflows.0.updated_at": "2026-01-02T00:00:00Z",
		"workflows.0.enabled": true})
	entry, _ := json.Marshal(out["workflows"].([]any)[0])
	assertSelectionMatchesOutput(t, strings.TrimSuffix(strings.SplitN(workflowsSelection, "nodes{", 2)[1], "}}"), entry,
		entrySchema(t, projectWorkflowsList.OutputSchema, "workflows"), nil, nil)
}

func teamFixture(slug string, extra string) string {
	return `{"id":"T_1","databaseId":3,"name":"Core Team","slug":` + fmt.Sprintf("%q", slug) + `,"combinedSlug":"octo-org/` + slug +
		`","description":"Builds\u0007 things","privacy":"VISIBLE","notificationSetting":"NOTIFICATIONS_ENABLED",` +
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","reviewRequestDelegationEnabled":true,` +
		`"reviewRequestDelegationAlgorithm":"ROUND_ROBIN","reviewRequestDelegationMemberCount":2,` +
		`"reviewRequestDelegationNotifyTeam":false,"viewerCanAdminister":true,` +
		`"url":"https://github.com/orgs/octo-org/teams/` + slug + `","resourcePath":"/orgs/octo-org/teams/` + slug + `",` +
		`"avatarUrl":"https://avatars.example.invalid/t/3","membersUrl":"https://github.com/orgs/octo-org/teams/` + slug +
		`/members","membersResourcePath":"/orgs/octo-org/teams/` + slug + `/members",` +
		`"repositoriesUrl":"https://github.com/orgs/octo-org/teams/` + slug + `/repositories",` +
		`"repositoriesResourcePath":"/orgs/octo-org/teams/` + slug + `/repositories",` +
		`"teamsUrl":"https://github.com/orgs/octo-org/teams/` + slug + `/teams",` +
		`"teamsResourcePath":"/orgs/octo-org/teams/` + slug + `/teams",` +
		`"editTeamUrl":"https://github.com/orgs/octo-org/teams/` + slug + `/edit",` +
		`"editTeamResourcePath":"/orgs/octo-org/teams/` + slug + `/edit",` +
		`"newTeamUrl":"https://github.com/orgs/octo-org/new-team?parent=` + slug + `",` +
		`"newTeamResourcePath":"/orgs/octo-org/new-team"` + extra + `}`
}

func TestProjectTeamsReportEveryMemberOfTheTeam(t *testing.T) {
	status := `{"id":"US_1","createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z","emoji":":palm_tree:",` +
		`"message":"On\u0007 leave","indicatesLimitedAvailability":true,"expiresAt":"2026-10-01T00:00:00Z",` +
		`"user":` + wideAccount + `,"organization":{"__typename":"Organization","id":"O_1","databaseId":2,"login":"octo-org",` +
		`"name":"Octo Org","url":"https://github.com/octo-org","resourcePath":"/octo-org"}}`
	ancestors := strings.Repeat(teamFixture("parent", "")+",", 10) + teamFixture("root", "")
	extra := `,"parentTeam":` + teamFixture("parent", "") + `,"ancestors":{"totalCount":12,"nodes":[` + ancestors + `]},` +
		`"memberStatuses":{"totalCount":1,"nodes":[` + status + `]},` +
		`"projectsV2":{"totalCount":1,"nodes":[{"id":"PVT_7","number":7,"title":"Plan","url":"https://github.com/orgs/octo-org/projects/7"}]},` +
		`"members":{"totalCount":8},"childTeams":{"totalCount":2},"repositories":{"totalCount":5},"invitations":{"totalCount":1}`
	f := metaFake(func(string, map[string]any) string {
		return projectAnswer(`"teams":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[` + teamFixture("core", extra) + `]}`)
	})
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, teamsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := metaOut(t, raw)
	var schema map[string]any
	if err := json.Unmarshal(teamsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", map[string]any{"teams": out["teams"], "has_more": false}, schema)
	checkMetaPaths(t, out, map[string]any{
		"teams.0.team": "core", "teams.0.name": "Core Team", "teams.0.slug": "core", "teams.0.id": "T_1",
		"teams.0.database_id": float64(3), "teams.0.combined_slug": "octo-org/core",
		"teams.0.description": "Builds  things", "teams.0.privacy": "VISIBLE",
		"teams.0.notification_setting": "NOTIFICATIONS_ENABLED", "teams.0.created_at": "2026-01-01T00:00:00Z",
		"teams.0.review_request_delegation_enabled": true, "teams.0.review_request_delegation_algorithm": "ROUND_ROBIN",
		"teams.0.review_request_delegation_member_count": float64(2), "teams.0.review_request_delegation_notify_team": false,
		"teams.0.viewer_can_administer": true, "teams.0.url": "https://github.com/orgs/octo-org/teams/core",
		"teams.0.links.avatar_url":                                 "https://avatars.example.invalid/t/3",
		"teams.0.links.members_url":                                "https://github.com/orgs/octo-org/teams/core/members",
		"teams.0.links.repositories_url":                           "https://github.com/orgs/octo-org/teams/core/repositories",
		"teams.0.links.teams_url":                                  "https://github.com/orgs/octo-org/teams/core/teams",
		"teams.0.links.edit_team_url":                              "https://github.com/orgs/octo-org/teams/core/edit",
		"teams.0.links.new_team_url":                               "https://github.com/orgs/octo-org/new-team?parent=core",
		"teams.0.links.resource_path":                              "/orgs/octo-org/teams/core",
		"teams.0.links.edit_team_resource_path":                    "/orgs/octo-org/teams/core/edit",
		"teams.0.links.members_resource_path":                      "/orgs/octo-org/teams/core/members",
		"teams.0.links.new_team_resource_path":                     "/orgs/octo-org/new-team",
		"teams.0.links.repositories_resource_path":                 "/orgs/octo-org/teams/core/repositories",
		"teams.0.links.teams_resource_path":                        "/orgs/octo-org/teams/core/teams",
		"teams.0.parent_team.slug":                                 "parent",
		"teams.0.parent_team.links.avatar_url":                     "https://avatars.example.invalid/t/3",
		"teams.0.ancestors_total_count":                            float64(12),
		"teams.0.ancestors_truncated":                              true,
		"teams.0.member_statuses.0.emoji":                          ":palm_tree:",
		"teams.0.member_statuses.0.message":                        "On  leave",
		"teams.0.member_statuses.0.user.login":                     "octocat",
		"teams.0.member_statuses.0.user.email":                     "octocat@example.com",
		"teams.0.member_statuses.0.organization.login":             "octo-org",
		"teams.0.member_statuses.0.indicates_limited_availability": true,
		"teams.0.member_statuses.0.expires_at":                     "2026-10-01T00:00:00Z",
		"teams.0.member_statuses_total_count":                      float64(1),
		"teams.0.projects_v2.0.number":                             float64(7),
		"teams.0.projects_v2.0.url":                                "https://github.com/orgs/octo-org/projects/7",
		"teams.0.members_total_count":                              float64(8), "teams.0.child_teams_total_count": float64(2),
		"teams.0.repositories_total_count": float64(5), "teams.0.invitations_total_count": float64(1),
	})
	if got := len(walkPath(t, out, "teams.0.ancestors").([]any)); got != repositoryListCap {
		t.Errorf("ancestors = %d, want %d", got, repositoryListCap)
	}
	if strings.Contains(string(raw), "\u0007") {
		t.Error("a control character reached the output")
	}
	// A nested team carries no connection and no organization.
	for _, member := range []string{"ancestors", "member_statuses", "parent_team", "members_total_count"} {
		if _, ok := walkPath(t, out, "teams.0.parent_team").(map[string]any)[member]; ok {
			t.Errorf("the parent team carries %s", member)
		}
	}
	entry, _ := json.Marshal(out["teams"].([]any)[0])
	assertSelectionMatchesOutput(t, teamNodeSelection, entry, entrySchema(t, teamsList.OutputSchema, "teams"),
		[]string{"team", "links", "type", "ancestors_truncated", "ancestors_total_count", "member_statuses_truncated",
			"member_statuses_total_count", "projects_v2_truncated", "projects_v2_total_count", "members_total_count",
			"child_teams_total_count", "repositories_total_count", "invitations_total_count", "name_with_owner"},
		[]string{"resourcePath", "avatarUrl", "membersUrl", "membersResourcePath", "repositoriesUrl",
			"repositoriesResourcePath", "teamsUrl", "teamsResourcePath", "editTeamUrl", "editTeamResourcePath",
			"newTeamUrl", "newTeamResourcePath", "first", "totalCount", "nodes", "members", "childTeams",
			"repositories", "invitations", "parentTeam"})
	for _, banned := range []string{"HTML", "viewerCanSubscribe", "viewerSubscription", "organization{ ", "Html"} {
		if strings.Contains(teamNodeSelection, banned) {
			t.Errorf("the selection asks for %s", banned)
		}
	}
}

func TestProjectTeamsWithoutTheWidthAreUnchanged(t *testing.T) {
	f := metaFake(func(string, map[string]any) string {
		return projectAnswer(`"teams":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[{"slug":"design","name":"Design"}]}`)
	})
	core := metaCore(t, f, nil)
	raw, err := invoke(t, core, teamsList.ID, "planning", `{}`, false)
	if want := `{"has_more":false,"project":"orgs/octo-org/projects/7","teams":[{"name":"Design","slug":"design","team":"design"}]}`; err != nil ||
		string(raw) != want {
		t.Errorf("result = %s, %v; want %s", raw, err, want)
	}
}

// A member GitHub refuses the token drops out of its entry and is named there; every other error fails the
// call as before.
func TestRefusedMembersDropOutOfTheirEntry(t *testing.T) {
	forbidden := func(path string) string {
		return `,"errors":[{"type":"FORBIDDEN","path":` + path + `,"message":"Resource not accessible"}]`
	}
	projectNode := `{"number":3,"title":"Roadmap","url":"https://github.com/orgs/octo-org/projects/3","closed":false,` +
		`"id":"PVT_3","teams":null,"items":{"totalCount":4}}`
	teamNode := `{"slug":"core","name":"Core","id":"T_1","invitations":null,"memberStatuses":{"totalCount":0,"nodes":[]}}`
	cases := []struct {
		name, tool, connection, response, want string
		failure                                bool
	}{
		{"project teams", projectsList.ID, "org", `{"data":{"owner":{"projectsV2":{"pageInfo":{"hasNextPage":false,` +
			`"endCursor":"c"},"edges":[{"cursor":"c","node":` + projectNode + `}]}}}` +
			forbidden(`["owner","projectsV2","edges",0,"node","teams"]`) + `}`, `["teams_total_count"]`, false},
		{"another project error", projectsList.ID, "org", `{"data":{"owner":null},"errors":[{"type":"INTERNAL",` +
			`"path":["owner"],"message":"boom"}]}`, "", true},
		{"a refused compact member", projectsList.ID, "org", `{"data":{"owner":{"projectsV2":{"pageInfo":` +
			`{"hasNextPage":false,"endCursor":"c"},"edges":[{"cursor":"c","node":null}]}}}` +
			forbidden(`["owner","projectsV2","edges",0,"node","title"]`) + `}`, "", true},
		{"team invitations", teamsList.ID, "planning", projectAnswerWithErrors(`"teams":{"pageInfo":{"hasNextPage":false,`+
			`"endCursor":"x"},"nodes":[`+teamNode+`]}`, forbidden(`["owner","projectV2","teams","nodes",0,"invitations"]`)),
			`["invitations_total_count"]`, false},
		{"another team error", teamsList.ID, "planning", projectAnswerWithErrors(`"teams":{"pageInfo":{"hasNextPage":false,`+
			`"endCursor":"x"},"nodes":[`+teamNode+`]}`, `,"errors":[{"type":"INTERNAL","path":["owner"],"message":"boom"}]`),
			"", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := metaFake(func(string, map[string]any) string { return tt.response })
			core := metaCore(t, f, nil)
			arguments := `{"team_counts":true}`
			if tt.tool == teamsList.ID {
				arguments = `{}`
			}
			raw, err := invoke(t, core, tt.tool, tt.connection, arguments, false)
			if tt.failure {
				if err == nil {
					t.Fatalf("result = %s, want an error", raw)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			out := metaOut(t, raw)
			list := "projects"
			if tt.tool == teamsList.ID {
				list = "teams"
			}
			entry := walkPath(t, out, list+".0").(map[string]any)
			got, _ := json.Marshal(entry["unreadable"])
			if string(got) != tt.want {
				t.Errorf("unreadable = %s, want %s: %s", got, tt.want, raw)
			}
			for _, member := range []string{"teams_total_count", "invitations_total_count"} {
				if _, ok := entry[member]; ok {
					t.Errorf("the refused member %s is present", member)
				}
			}
		})
	}
}

func projectAnswerWithErrors(members, errors string) string {
	return strings.TrimSuffix(projectAnswer(members), "}") + errors + "}"
}

// The team count is asked for only on request, is refused for a wrong type before a secret is read, and does
// not bind a cursor.
func TestProjectListTeamCountsAreOptIn(t *testing.T) {
	f := metaFake(func(string, map[string]any) string {
		return `{"data":{"owner":{"projectsV2":{"pageInfo":{"hasNextPage":true,"endCursor":"gh-1"},"edges":[` +
			`{"cursor":"gh-1","node":{"number":1,"title":"Plan","url":"https://github.com/orgs/octo-org/projects/1","closed":false}},` +
			`{"cursor":"gh-2","node":{"number":2,"title":"Plan","url":"https://github.com/orgs/octo-org/projects/2","closed":false}}]}}}}`
	})
	reads := 0
	core := metaCore(t, f, &reads)
	raw, err := invoke(t, core, projectsList.ID, "org", `{"limit":1}`, false)
	cursor := nextCursorOf(raw)
	if err != nil || cursor == "" {
		t.Fatalf("first batch = %s, %v", raw, err)
	}
	if document := f.recorded()[0].document; strings.Contains(document, "teams") {
		t.Errorf("the default query asks for teams: %s", document)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, projectsList.ID, "org", `{"limit":1,"team_counts":true,"cursor":"`+cursor+`"}`,
		false); err != nil {
		t.Errorf("the cursor with team_counts = %v", err)
	}
	if document := f.recorded()[before].document; !strings.Contains(document, "teams{totalCount}") {
		t.Errorf("team_counts does not ask for teams: %s", document)
	}
	reads, before = 0, len(f.recorded())
	_, err = invoke(t, core, projectsList.ID, "org", `{"team_counts":"yes"}`, false)
	if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", &provider.InvalidRequestError{}) || reads != 0 ||
		len(f.recorded()) != before {
		t.Errorf("err = %T %v, reads = %d; want invalid-request before any read", err, err, reads)
	}
}
