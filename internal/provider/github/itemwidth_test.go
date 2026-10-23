package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const wideAccount = `{"__typename":"User","login":"octocat","id":"U_1","databaseId":1,"name":"The Octocat",` +
	`"email":"octocat@example.com","isSiteAdmin":false,"url":"https://github.com/octocat",` +
	`"avatarUrl":"https://avatars.example.invalid/1","resourcePath":"/octocat"}`

func wideField(id, name string) string { return fmt.Sprintf(`"field":{"id":%q,"name":%q}`, id, name) }

func wideCommon(id string) string {
	return `"id":"` + id + `","databaseId":11,"createdAt":"2026-09-02T10:00:00Z","updatedAt":"2026-09-03T10:00:00Z",` +
		`"creator":` + wideAccount + `,`
}

// wideValues holds one value of every type a project field can hold on an item.
func wideValues() []string {
	return []string{
		`{"__typename":"ProjectV2ItemFieldTextValue",` + wideCommon("PVTFV_text") + `"text":"short\u0007 note",` + wideField("F_note", "Note") + `}`,
		`{"__typename":"ProjectV2ItemFieldNumberValue",` + wideCommon("PVTFV_num") + `"number":3.50,` + wideField("F_est", "Estimate") + `}`,
		`{"__typename":"ProjectV2ItemFieldDateValue",` + wideCommon("PVTFV_date") + `"date":"2026-10-01",` + wideField("F_due", "Due") + `}`,
		`{"__typename":"ProjectV2ItemFieldSingleSelectValue",` + wideCommon("PVTFV_sel") + `"name":"P1","optionId":"O_p1",` +
			`"color":"RED","description":"Urgent",` + wideField("F_prio", "Priority") + `}`,
		`{"__typename":"ProjectV2ItemFieldIterationValue",` + wideCommon("PVTFV_it") + `"title":"Sprint 4","iterationId":"I_s4",` +
			`"startDate":"2026-09-07","duration":14,` + wideField("F_sprint", "Sprint") + `}`,
		`{"__typename":"ProjectV2ItemFieldMultiSelectValue",` + wideCommon("PVTFV_multi") + `"value":"UI, API",` +
			`"options":[{"id":"M_ui","name":"UI","color":"BLUE","description":"Interface"},{"id":"M_api","name":"API","color":"GRAY","description":""}],` +
			wideField("F_tags", "Tags") + `}`,
		`{"__typename":"ProjectV2ItemFieldLabelValue","labels":{"totalCount":12,"nodes":[` +
			strings.Repeat(`{"id":"L_1","name":"bug","color":"d73a4a","isDefault":true,"createdAt":"2026-01-01T00:00:00Z"},`, 9) +
			`{"id":"L_2","name":"help","color":"008672","isDefault":false}]},` + wideField("F_labels", "Labels") + `}`,
		`{"__typename":"ProjectV2ItemFieldMilestoneValue","milestone":{"id":"MI_1","number":4,"title":"QA release",` +
			`"state":"OPEN","closed":false,"progressPercentage":33.3,"creator":` + wideAccount + `},` + wideField("F_ms", "Milestone") + `}`,
		`{"__typename":"ProjectV2ItemFieldPullRequestValue","pullRequests":{"totalCount":1,"nodes":[{"id":"PR_1","number":9,` +
			`"title":"Fix","state":"OPEN","url":"https://github.com/octo-org/example/pull/9","repository":{"nameWithOwner":"octo-org/example"}}]},` +
			wideField("F_pr", "Linked pull requests") + `}`,
		`{"__typename":"ProjectV2ItemFieldRepositoryValue","repository":{"id":"R_1","databaseId":5,"fullDatabaseId":"5",` +
			`"name":"secret","nameWithOwner":"other-org/secret","description":"private words","url":"https://github.com/other-org/secret"},` +
			wideField("F_repo", "Repository") + `}`,
		`{"__typename":"ProjectV2ItemFieldReviewerValue","reviewers":{"totalCount":2,"nodes":[` + wideAccount +
			`,{"__typename":"Team","id":"T_1","databaseId":3,"name":"Core","slug":"core","combinedSlug":"octo-org/core",` +
			`"privacy":"CLOSED","notificationSetting":"NOTIFICATIONS_ENABLED","url":"https://github.com/orgs/octo-org/teams/core"}]},` +
			wideField("F_rev", "Reviewers") + `}`,
		`{"__typename":"ProjectV2ItemFieldUserValue","users":{"totalCount":1,"nodes":[` + wideAccount + `]},` + wideField("F_users", "Owners") + `}`,
		`{"__typename":"ProjectV2ItemIssueFieldValue","issueFieldValue":{"__typename":"IssueFieldNumberValue","id":"IFV_1",` +
			`"field":{"__typename":"IssueFieldNumber","name":"Score","dataType":"NUMBER","id":"IF_1"},"value":7.25},` +
			wideField("F_issue", "Score") + `}`,
		`{}`,
	}
}

func wideContent(kind, body string) string {
	common := `"__typename":"` + kind + `","id":"C_1","databaseId":42,"fullDatabaseId":"9007199254740993","number":9,` +
		`"title":"Fix\u0007 crash","url":"https://github.com/octo-org/example/pull/9","repository":{"nameWithOwner":"octo-org/example"},` +
		`"assignees":{"totalCount":1,"nodes":[{"login":"octocat"}]},"labels":{"totalCount":1,"nodes":[{"name":"bug"}]},` +
		`"body":` + strconv.Quote(body) + `,"createdAt":"2026-09-01T09:00:00Z","updatedAt":"2026-09-02T09:00:00Z",` +
		`"publishedAt":"2026-09-01T09:00:00Z","lastEditedAt":"2026-09-02T08:00:00Z","author":` + wideAccount + `,` +
		`"editor":` + wideAccount + `,"authorAssociation":"MEMBER","closed":false,"locked":false,"createdViaEmail":false,` +
		`"includesCreatedEdit":true,"isReadByViewer":true,"resourcePath":"/octo-org/example/pull/9",` +
		`"viewerCanClose":true,"viewerCanUpdate":false,"viewerCannotUpdateReasons":["LOCKED"],"viewerDidAuthor":true,` +
		`"viewerSubscription":"SUBSCRIBED",`
	if kind == "Issue" {
		return `{` + common + `"issueState":"OPEN","stateReason":null,"isPinned":false,"trackedIssuesCount":0,` +
			`"bodyResourcePath":"/octo-org/example/issues/9#issue-1","bodyUrl":"https://github.com/octo-org/example/issues/9#issue-1",` +
			`"viewerCanSetFields":true,"viewerThreadSubscriptionStatus":"NONE"}`
	}
	return `{` + common + `"pullRequestState":"MERGED","additions":10,"deletions":2,"changedFiles":3,"baseRefName":"main",` +
		`"baseRefOid":"abc","headRefName":"fix","headRefOid":"def","canBeRebased":true,"isCrossRepository":false,` +
		`"isDraft":false,"merged":true,"mergedAt":"2026-09-03T09:00:00Z","mergedBy":` + wideAccount + `,` +
		`"mergeable":"UNKNOWN","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","totalCommentsCount":4,` +
		`"permalink":"https://github.com/octo-org/example/pull/9/commits/def","checksUrl":"https://github.com/octo-org/example/pull/9/checks",` +
		`"viewerCanMergeAsAdmin":false,"viewerCanUpdateBranch":true}`
}

func wideNode(kind, content string) string {
	values := wideValues()
	return `{"id":"PVTI_wide","type":"` + kind + `","createdAt":"2026-09-01T10:00:00Z","updatedAt":"2026-09-04T10:00:00Z",` +
		`"isArchived":false,"fullDatabaseId":"9007199254740993","creator":` + wideAccount + `,` +
		`"fieldValues":{"totalCount":45,"nodes":[` + strings.Join(values, ",") + `]},"content":` + content + `}`
}

func wideCore(t *testing.T, f *fakeGitHub) *application.Core {
	t.Helper()
	base := serve(t, f)
	red := &redact.Redactor{}
	return application.New(registry(t), coreConfig(base), resolver(red, nil), red)
}

// walkPath reads a member of decoded JSON by a dotted path with list positions.
func walkPath(t *testing.T, value any, path string) any {
	t.Helper()
	for _, step := range strings.Split(path, ".") {
		switch v := value.(type) {
		case map[string]any:
			value = v[step]
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i >= len(v) {
				t.Fatalf("%s: no position %s", path, step)
			}
			value = v[i]
		default:
			t.Fatalf("%s: cannot step into %v at %s", path, value, step)
		}
	}
	return value
}

// withoutFields drops the free-form fields object, whose members are project field names, from a copy.
func withoutFields(out map[string]any) map[string]any {
	copied := map[string]any{}
	for key, value := range out {
		copied[key] = value
	}
	delete(copied, "fields")
	delete(copied, "project")
	if items, ok := out["items"].([]any); ok {
		list := []any{}
		for _, item := range items {
			entry := map[string]any{}
			for key, value := range item.(map[string]any) {
				entry[key] = value
			}
			delete(entry, "fields")
			list = append(list, entry)
		}
		copied["items"] = list
	}
	return copied
}

func TestItemListReportsEveryMemberOfTheItem(t *testing.T) {
	long := strings.Repeat("b", itemListBodyLimit+50)
	f := &fakeGitHub{items: []fakeItem{{id: "PVTI_wide", kind: "PULL_REQUEST", node: wideNode("PULL_REQUEST", wideContent("PullRequest", long))}}}
	core := wideCore(t, f)
	raw, err := invoke(t, core, itemsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(itemsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", withoutFields(out), schema)
	for path, want := range map[string]any{
		"items.0.created_at": "2026-09-01T10:00:00Z", "items.0.updated_at": "2026-09-04T10:00:00Z",
		"items.0.is_archived": false, "items.0.full_database_id": "9007199254740993",
		"items.0.creator.email": "octocat@example.com", "items.0.creator.type": "User",
		"items.0.url": "https://github.com/octo-org/example/pull/9", "items.0.state": "merged",
		"items.0.content.state": "MERGED", "items.0.content.full_database_id": "9007199254740993",
		"items.0.content.title": "Fix  crash", "items.0.content.body_truncated": true,
		"items.0.content.author_detail.login": "octocat", "items.0.content.merged_by.login": "octocat",
		"items.0.content.base_ref_name": "main", "items.0.content.review_decision": "APPROVED",
		"items.0.content.merged": true, "items.0.content.is_draft": false, "items.0.content.additions": float64(10),
		"items.0.content.mergeable": "UNKNOWN", "items.0.content.viewer_cannot_update_reasons.0": "LOCKED",
		"items.0.content.links.checks_url":                     "https://github.com/octo-org/example/pull/9/checks",
		"items.0.content.links.resource_path":                  "/octo-org/example/pull/9",
		"items.0.field_values_total_count":                     float64(45),
		"items.0.field_values_truncated":                       true,
		"items.0.field_values.0.type":                          "ProjectV2ItemFieldTextValue",
		"items.0.field_values.0.text":                          "short  note",
		"items.0.field_values.0.field_name":                    "Note",
		"items.0.field_values.0.creator.login":                 "octocat",
		"items.0.field_values.0.database_id":                   float64(11),
		"items.0.field_values.3.option_id":                     "O_p1",
		"items.0.field_values.3.color":                         "RED",
		"items.0.field_values.4.iteration_id":                  "I_s4",
		"items.0.field_values.4.duration":                      float64(14),
		"items.0.field_values.5.options.1.id":                  "M_api",
		"items.0.field_values.5.value":                         "UI, API",
		"items.0.field_values.6.labels_total_count":            float64(12),
		"items.0.field_values.6.labels_truncated":              true,
		"items.0.field_values.7.milestone.title":               "QA release",
		"items.0.field_values.7.milestone.creator.login":       "octocat",
		"items.0.field_values.8.pull_requests.0.number":        float64(9),
		"items.0.field_values.9.repository.name_with_owner":    "other-org/secret",
		"items.0.field_values.10.reviewers.1.slug":             "core",
		"items.0.field_values.10.reviewers.1.type":             "Team",
		"items.0.field_values.10.reviewers.0.login":            "octocat",
		"items.0.field_values.11.users.0.login":                "octocat",
		"items.0.field_values.12.issue_field_value.field_name": "Score",
		"items.0.field_values.12.issue_field_value.value":      float64(7.25),
	} {
		if got := walkPath(t, out, path); got != want {
			t.Errorf("%s = %v (%T), want %v", path, got, got, want)
		}
	}
	if body := walkPath(t, out, "items.0.content.body").(string); len([]rune(body)) != itemListBodyLimit {
		t.Errorf("list body has %d characters, want %d", len([]rune(body)), itemListBodyLimit)
	}
	// The number keeps the digits GitHub sent.
	if !bytes.Contains(raw, []byte(`"number":3.50`)) {
		t.Errorf("the number value lost its digits: %s", raw)
	}
	// A repository outside the targets keeps its identifiers only.
	repository := walkPath(t, out, "items.0.field_values.9.repository").(map[string]any)
	if _, ok := repository["description"]; ok || repository["id"] != "R_1" || repository["name"] != nil {
		t.Errorf("repository outside the targets = %v, want identifiers only", repository)
	}
	// The flat members stay as they were, and a node without a type is no value.
	if walkPath(t, out, "items.0.number") != float64(9) || walkPath(t, out, "items.0.labels.0") != "bug" ||
		len(walkPath(t, out, "items.0.field_values").([]any)) != 13 {
		t.Errorf("flat members or value count changed: %s", raw)
	}
	if bytes.Contains(raw, []byte("\u0007")) {
		t.Error("a control character reached the output")
	}
}

func TestItemGetReportsTheFullBodyAndTheContent(t *testing.T) {
	body := strings.Repeat("c", projectionBodyLimit+10)
	f := &fakeGitHub{items: []fakeItem{{id: "PVTI_wide", kind: "ISSUE", node: wideNode("ISSUE", wideContent("Issue", body))}}}
	core := wideCore(t, f)
	raw, err := invoke(t, core, itemsGet.ID, "planning", `{"item_id":"PVTI_wide"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(itemsGet.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", withoutFields(out), schema)
	if len(out["body"].(string)) != len(body) {
		t.Errorf("flat body has %d characters, want the full %d", len(out["body"].(string)), len(body))
	}
	if got := len([]rune(walkPath(t, out, "content.body").(string))); got != projectionBodyLimit ||
		walkPath(t, out, "content.body_truncated") != true {
		t.Errorf("content body has %d characters, want %d and a marker", got, projectionBodyLimit)
	}
	for path, want := range map[string]any{"content.is_pinned": false, "content.tracked_issues_count": float64(0),
		"content.viewer_can_set_fields": true, "content.viewer_thread_subscription_status": "NONE",
		"content.links.body_url": "https://github.com/octo-org/example/issues/9#issue-1",
		"content.number":         float64(9), "url": "https://github.com/octo-org/example/pull/9"} {
		if got := walkPath(t, out, path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
	if _, ok := out["content"].(map[string]any)["state_reason"]; ok {
		t.Error("a null state_reason reached the output")
	}
}

func TestItemListWithoutTheWidthIsUnchanged(t *testing.T) {
	f := &fakeGitHub{items: []fakeItem{{id: "PVTI_a", kind: "DRAFT_ISSUE", title: "Draft", status: "Todo"}}}
	core := wideCore(t, f)
	raw, err := invoke(t, core, itemsList.ID, "planning", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Items) != 1 {
		t.Fatalf("%s: %v", raw, err)
	}
	entry := out.Items[0]
	want := `{"content":{"id":"DI_PVTI_a","title":"Draft"},"fields":{"Due":"2026-10-01","Estimate":3,` +
		`"Note":"short note","Priority":"P1","Sprint":"Sprint 4","Tags":["UI","API"]},"id":"PVTI_a","status":"Todo",` +
		`"title":"Draft","type":"draft_issue"}`
	got, _ := json.Marshal(entry)
	if string(got) != want {
		t.Errorf("entry = %s\nwant %s", got, want)
	}
}

func TestItemFiltersBecomeQuotedTerms(t *testing.T) {
	tests := []struct {
		name    string
		filters ItemFilters
		want    string
	}{
		{"state and reason", ItemFilters{State: "closed", Reason: "not_planned"}, `is:closed reason:"not planned"`},
		{"lists", ItemFilters{Milestone: []string{"QA release", "v2"}, Reviewers: []string{"octocat", "@me"},
			IssueType: []string{"Bug"}}, `milestone:"QA release","v2" reviewers:octocat,@me type:"Bug"`},
		{"parent", ItemFilters{ParentIssue: "octo-org/example#4"}, `parent-issue:octo-org/example#4`},
		{"updated range", ItemFilters{UpdatedFrom: "2026-01-01", UpdatedTo: "@today-7"}, `updated:2026-01-01..@today-7d`},
		{"updated open end", ItemFilters{UpdatedFrom: "@today"}, `updated:@today..*`},
		{"has and no", ItemFilters{Has: []string{"Target date"}, No: []string{"Priority"}}, `has:Target-date no:Priority`},
		{"field values", ItemFilters{FieldFilters: []FieldFilter{{Field: "Priority", Values: []string{"P1", "P2"}, Negate: true}}},
			`-Priority:"P1","P2"`},
		{"field ranges", ItemFilters{FieldFilters: []FieldFilter{
			{Field: "Estimate", From: "1", To: "3.5"}, {Field: "Sprint", From: "@current", To: "@current+3"},
			{Field: "Due", From: "@today", To: "@today+7"}, {Field: "Start", From: "2026-01-01"},
			{Field: "Sprint", To: "Iteration 5"}}},
			`Estimate:1..3.5 Sprint:@current..@current+3 Due:@today..@today+7d Start:2026-01-01..* Sprint:*.."Iteration 5"`},
		{"text", ItemFilters{Text: "crash on start"}, `"crash on start"`},
	}
	for _, tt := range tests {
		options := ItemListOptions{ItemFilters: tt.filters, StatusNot: []string{}}
		if err := options.ItemFilters.check(); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := options.query(); got != tt.want {
			t.Errorf("%s: query = %q, want %q", tt.name, got, tt.want)
		}
	}
	// The terms follow the existing ones.
	options := ItemListOptions{Type: "issue", Labels: []string{"bug"}, ItemFilters: ItemFilters{State: "open"}, StatusNot: []string{"Done"}}
	if got := options.query(); got != `-status:"Done" is:issue label:"bug" is:open` {
		t.Errorf("query = %q", got)
	}
}

func TestItemFiltersRefuseValuesBeforeAnyCredential(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)
	long := strings.Repeat("x", 257)
	for name, arguments := range map[string]string{
		"state":              `{"state":"all"}`,
		"reason":             `{"reason":"done"}`,
		"quote in milestone": `{"milestone":["a\"b"]}`,
		"newline":            `{"issue_type":["a\nb"]}`,
		"too many":           `{"milestone":["1","2","3","4","5","6","7","8","9","10","11"]}`,
		"reviewer":           `{"reviewers":["a b"]}`,
		"parent":             `{"parent_issue":"octo-org/example"}`,
		"date":               `{"updated_from":"2026-13-40"}`,
		"offset":             `{"updated_to":"@today+400"}`,
		"has":                `{"has":["a:b"]}`,
		"no negated":         `{"no":["-Priority"]}`,
		"field quote":        `{"field_filters":[{"field":"a\"b","values":["x"]}]}`,
		"values and range":   `{"field_filters":[{"field":"a","values":["x"],"from":"1"}]}`,
		"neither":            `{"field_filters":[{"field":"a"}]}`,
		"bad keyword":        `{"field_filters":[{"field":"a","from":"@later"}]}`,
		"me offset":          `{"field_filters":[{"field":"a","from":"@me+1"}]}`,
		"offset range":       `{"field_filters":[{"field":"a","from":"@current+999"}]}`,
		"long value":         `{"field_filters":[{"field":"a","values":["` + long + `"]}]}`,
		"long text":          `{"text":"` + long + `"}`,
		"text quote":         `{"text":"a\"b"}`,
		"too many filters":   `{"field_filters":[` + strings.Repeat(`{"field":"a","values":["x"]},`, 10) + `{"field":"a","values":["x"]}]}`,
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, itemsList.ID, "planning", arguments, false); !isInvalidRequest(err) || reads != 0 ||
			len(f.recorded()) != before {
			t.Errorf("%s: %v (secret reads %d, requests %d), want invalid-request before the secret and GitHub", name, err, reads,
				len(f.recorded())-before)
		}
	}
}

func TestItemCursorBindsTheNewFilters(t *testing.T) {
	f := &fakeGitHub{items: roster(), ignoreQuery: true}
	base := serve(t, f)
	c := client(t, base, projectTarget)
	page, err := c.ListItems(t.Context(), ItemListOptions{Limit: 2, ItemFilters: ItemFilters{State: "open"}})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	for name, options := range map[string]ItemListOptions{
		"no filter":    {Cursor: page.NextCursor},
		"other filter": {Cursor: page.NextCursor, ItemFilters: ItemFilters{State: "closed"}},
		"extra filter": {Cursor: page.NextCursor, ItemFilters: ItemFilters{State: "open", Text: "x"}},
	} {
		if _, err := c.ListItems(t.Context(), options); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want a refused cursor", name, err)
		}
	}
	if _, err := c.ListItems(t.Context(), ItemListOptions{Cursor: page.NextCursor, ItemFilters: ItemFilters{State: "open"}}); err != nil {
		t.Errorf("the same filter: %v", err)
	}
	// Without any new filter the fingerprint is the one cursors had before the filters existed.
	options := ItemListOptions{Status: []string{"Todo"}, Type: "issue"}
	bound := c.target
	old := fingerprint("projects.items", bound.String(), folded(options.Status), folded(options.StatusNot), options.Type,
		"", "", folded(options.Labels))
	if !bytes.Equal(options.binding(bound), old) {
		t.Error("the fingerprint without the new filters changed")
	}
}

// itemNodeEstimate counts the nodes GitHub charges a query for: every object field once per parent node, a
// connection once per requested entry, and a plain list as listEntries entries. Scalars cost nothing.
func itemNodeEstimate(document string, listFields map[string]bool, listEntries int) int {
	firstPattern := regexp.MustCompile(`^first:(\d+)$`)
	var parse func(text string, multiplier int) (int, string)
	parse = func(text string, multiplier int) (int, string) {
		total := 0
		for len(text) > 0 {
			text = strings.TrimLeft(text, " ")
			if text == "" || text[0] == '}' {
				return total, text
			}
			end := strings.IndexAny(text, " {}(")
			if end < 0 {
				return total, ""
			}
			name := text[:end]
			if name == "..." {
				// ... on Type{ : the fragment adds its members to the same node.
				next := strings.TrimLeft(text[end:], " ")
				next = strings.TrimPrefix(next, "on ")
				brace := strings.Index(next, "{")
				inner, rest := parse(next[brace+1:], multiplier)
				total += inner
				text = strings.TrimPrefix(rest, "}")
				continue
			}
			text = text[end:]
			entries, paged := 1, false
			if strings.HasPrefix(text, "(") {
				close := strings.Index(text, ")")
				if m := firstPattern.FindStringSubmatch(strings.ReplaceAll(text[1:close], " ", "")); m != nil {
					entries, _ = strconv.Atoi(m[1])
					paged = true
				}
				text = text[close+1:]
			}
			if listFields[name] {
				entries = listEntries
			}
			text = strings.TrimLeft(text, " ")
			if !strings.HasPrefix(text, "{") {
				continue
			}
			// A connection counts through its nodes, which are one object per requested entry.
			connection := paged || name == "edges"
			inner, rest := parse(text[1:], multiplier*entries)
			if name == "nodes" || name == "node" {
				total += multiplier
			} else if !connection {
				total += multiplier * entries
			}
			total += inner
			text = strings.TrimPrefix(rest, "}")
		}
		return total, ""
	}
	// The fragment body replaces the spread in the items query.
	spread := regexp.MustCompile(`fragment itemFields on ProjectV2Item\{(.*)\}$`).FindStringSubmatch(document)
	_ = spread
	count, _ := parse(strings.SplitN(document, " fragment ", 2)[0][strings.Index(document, "{"):], 1)
	return count
}

// The list asks for the most nodes at the largest page: 100 items, each with 30 field values, and the lists of
// each value. The estimate stays far below GitHub's node limit of 500000, and no connection of an entry asks for
// more than the existing caps.
func TestItemListQueryStaysBelowTheNodeLimit(t *testing.T) {
	const nodeLimit = 500000
	for _, forbidden := range []string{"bodyHTML", "bodyText", "titleHTML", "nameHTML", "descriptionHTML", "hovercard",
		"databaseId @", "comments(", "fieldValueByName", "project{"} {
		if strings.Contains(itemsQuery, forbidden) {
			t.Errorf("the query asks for %q", forbidden)
		}
	}
	allowedPages := map[string]bool{"30": true, "20": true, "10": true, "5": true}
	for _, page := range regexp.MustCompile(`first:(\d+)\)`).FindAllStringSubmatch(itemsQuery, -1) {
		if !allowedPages[page[1]] {
			t.Errorf("a connection asks for %s entries", page[1])
		}
	}
	// Per item: 30 field values, each with lists of 5 and, counted generously, 25 entries per plain list.
	items := fragmentNodes(t, itemFragmentBody())
	nodes := 1 + maxLimit*(1+items)
	if nodes > nodeLimit*6/10 {
		t.Errorf("a page of %d items asks for %d nodes, want at most %d", maxLimit, nodes, nodeLimit*6/10)
	}
	t.Logf("nodes per item %d, page of %d items %d", items, maxLimit, nodes)
}

// itemFragmentBody is the selection of an item without its fragment wrapper.
func itemFragmentBody() string {
	return strings.TrimSuffix(strings.SplitN(itemFragment, "ProjectV2Item{", 2)[1], "}")
}

func fragmentNodes(t *testing.T, body string) int {
	t.Helper()
	return itemNodeEstimate("query{x{"+body+"}}", map[string]bool{"options": true}, 25) - 1
}
