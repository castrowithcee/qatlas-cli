package github

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const widthIssueRef = `{"id":"I_9","number":9,"title":"Other","state":"OPEN","url":"https://github.com/octo-org/example/issues/9",` +
	`"repository":{"nameWithOwner":"octo-org/example"}}`

const widthIssueRefView = `{"id":"I_9","number":9,"title":"Other","state":"OPEN",` +
	`"url":"https://github.com/octo-org/example/issues/9","repository":"octo-org/example"}`

const widthMilestone = `{"id":"M_1","number":3,"title":"v1","description":"First","state":"OPEN","closed":false,` +
	`"closedAt":"2026-02-01T00:00:00Z","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z",` +
	`"dueOn":"2026-03-01T00:00:00Z","openIssueCount":2,"closedIssueCount":5,"progressPercentage":71.4285,` +
	`"creator":` + widthUser + `,"viewerCanClose":true,"viewerCanReopen":false,` +
	`"url":"https://github.com/octo-org/example/milestone/3","resourcePath":"/octo-org/example/milestone/3"}`

const widthMilestoneView = `{"id":"M_1","number":3,"title":"v1","description":"First","state":"OPEN","closed":false,` +
	`"closed_at":"2026-02-01T00:00:00Z","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
	`"due_on":"2026-03-01T00:00:00Z","open_issue_count":2,"closed_issue_count":5,"progress_percentage":71.4285,` +
	`"creator":` + widthUserView + `,"viewer_can_close":true,"viewer_can_reopen":false,` +
	`"url":"https://github.com/octo-org/example/milestone/3","links":{"resource_path":"/octo-org/example/milestone/3"}}`

const widthIssueType = `{"id":"IT_1","name":"Bug","description":"Something broke","color":"RED","isEnabled":true,"isPrivate":false}`

const widthIssueTypeView = `{"id":"IT_1","name":"Bug","description":"Something broke","color":"RED","is_enabled":true,"is_private":false}`

// widthConnection is the first page of a connection with the given nodes.
func widthConnection(total int, more bool, nodes ...string) string {
	return fmt.Sprintf(`{"totalCount":%d,"pageInfo":{"hasNextPage":%t},"nodes":[%s]}`, total, more, strings.Join(nodes, ","))
}

const widthSuggestionTimes = `"createdAt":"2026-01-06T00:00:00Z","updatedAt":"2026-01-07T00:00:00Z"`
const widthSuggestionTimesView = `"created_at":"2026-01-06T00:00:00Z","updated_at":"2026-01-07T00:00:00Z"`

// widthSuggestions holds one pending suggestion of each type.
var widthSuggestions = `[{"__typename":"PendingAssigneeSuggestion","id":"PS_1","actor":` + widthBot + `,"assignee":` + widthUser +
	`,"rationale":"owner",` + widthSuggestionTimes + `},` +
	`{"__typename":"PendingCloseSuggestion","id":"PS_2","actor":` + widthBot + `,"duplicateOf":{"__typename":"Issue","id":"I_9",` +
	`"number":9,"repository":{"nameWithOwner":"octo-org/example"}},"rationale":"dup","stateReason":"DUPLICATE",` + widthSuggestionTimes + `},` +
	`{"__typename":"PendingFieldSuggestion","id":"PS_3","actor":` + widthBot + `,"issueField":{"id":"IFT_1","name":"Notes","dataType":"TEXT"},` +
	`"rationale":"notes","suggestedValue":"x",` + widthSuggestionTimes + `},` +
	`{"__typename":"PendingLabelSuggestion","id":"PS_4","actor":` + widthBot + `,"label":{"id":"LA_1","name":"help"},` +
	`"rationale":"label",` + widthSuggestionTimes + `},` +
	`{"__typename":"PendingTypeSuggestion","id":"PS_5","actor":` + widthBot + `,"issueType":{"id":"IT_1","name":"Bug"},` +
	`"rationale":"type",` + widthSuggestionTimes + `}]`

var widthSuggestionsView = `[{"type":"PendingAssigneeSuggestion","id":"PS_1","actor":` + widthBotView + `,"assignee":` + widthUserView +
	`,"rationale":"owner",` + widthSuggestionTimesView + `},` +
	`{"type":"PendingCloseSuggestion","id":"PS_2","actor":` + widthBotView + `,"duplicate_of":{"id":"I_9","number":9,` +
	`"repository":"octo-org/example"},"rationale":"dup","state_reason":"DUPLICATE",` + widthSuggestionTimesView + `},` +
	`{"type":"PendingFieldSuggestion","id":"PS_3","actor":` + widthBotView + `,"issue_field":{"id":"IFT_1","name":"Notes","data_type":"TEXT"},` +
	`"rationale":"notes","suggested_value":"x",` + widthSuggestionTimesView + `},` +
	`{"type":"PendingLabelSuggestion","id":"PS_4","actor":` + widthBotView + `,"label":{"id":"LA_1","name":"help"},` +
	`"rationale":"label",` + widthSuggestionTimesView + `},` +
	`{"type":"PendingTypeSuggestion","id":"PS_5","actor":` + widthBotView + `,"issue_type":{"id":"IT_1","name":"Bug"},` +
	`"rationale":"type",` + widthSuggestionTimesView + `}]`

// widthIssue is an Issue as the GraphQL schema describes it, with every member set.
var widthIssue = `{"number":42,"title":"Crash","state":"CLOSED","updatedAt":"2026-01-05T00:00:00Z",` +
	`"assignees":` + widthConnection(11, true, widthUser) + `,"labels":` + widthConnection(1, false, widthLabel) + `,` +
	`"issueType":` + widthIssueType + `,"id":"I_42","databaseId":4242,"fullDatabaseId":"9007199254740993",` +
	`"url":"https://github.com/octo-org/example/issues/42","author":` + widthUser + `,"authorAssociation":"MEMBER",` +
	`"body":"The body","bodyResourcePath":"/octo-org/example/issues/42#issue-4242",` +
	`"bodyUrl":"https://github.com/octo-org/example/issues/42#issue-4242","closed":true,` +
	`"closedAt":"2026-01-04T00:00:00Z","createdAt":"2026-01-01T00:00:00Z","createdViaEmail":true,` +
	`"includesCreatedEdit":false,"lastEditedAt":"2026-01-02T00:00:00Z","publishedAt":"2026-01-01T00:00:01Z",` +
	`"editor":` + widthBot + `,"locked":true,"activeLockReason":"RESOLVED","stateReason":"COMPLETED",` +
	`"isPinned":true,"isReadByViewer":false,"milestone":` + widthMilestone + `,"parent":` + widthIssueRef + `,` +
	`"duplicateOf":` + widthIssueRef + `,"pinnedIssueComment":{"id":"PIC_1","databaseId":5,"fullDatabaseId":"9007199254740995",` +
	`"pinnedAt":"2026-01-03T00:00:00Z","pinnedBy":` + widthUser + `,"issueComment":{"id":"IC_1"}},` +
	`"subIssuesSummary":{"completed":1,"percentCompleted":50,"total":2},` +
	`"issueDependenciesSummary":{"blockedBy":1,"blocking":2,"totalBlockedBy":3,"totalBlocking":4},` +
	`"reactionGroups":` + widthReactionGroups + `,"trackedIssuesCount":2,"resourcePath":"/octo-org/example/issues/42",` +
	`"assignedActors":` + widthConnection(2, false, widthBot, widthUser) + `,` +
	`"closedByPullRequestsReferences":` + widthConnection(1, false, widthIssueRef) + `,` +
	`"linkedBranches":` + widthConnection(1, false, `{"id":"LB_1","ref":{"name":"fix-42","target":{"oid":"abc123"}}}`) + `,` +
	`"participants":` + widthConnection(1, false, widthUser) + `,` +
	`"projectsV2":` + widthConnection(1, false, `{"id":"PVT_1","number":7,"title":"Board","url":"https://github.com/orgs/octo-org/projects/7","closed":false}`) + `,` +
	`"relatesTo":` + widthConnection(1, false, widthIssueRef) + `,` +
	`"suggestedActors":` + widthConnection(1, false, widthUser) + `,` +
	`"trackedInIssues":` + widthConnection(1, false, widthIssueRef) + `,` +
	`"trackedIssues":` + widthConnection(1, false, widthIssueRef) + `,` +
	`"pendingSuggestions":` + widthSuggestions + `,"comments":{"totalCount":4},"subIssues":{"totalCount":2},"blockedBy":{"totalCount":1},"blocking":{"totalCount":2},` +
	`"timelineItems":{"totalCount":9},"userContentEdits":{"totalCount":3},"reactions":{"totalCount":3},` +
	`"projectItems":{"totalCount":1},"viewerCanAssign":true,"viewerCanClose":true,"viewerCanDelete":false,` +
	`"viewerCanLabel":true,"viewerCanReact":true,"viewerCanReopen":false,"viewerCanSetFields":true,` +
	`"viewerCanSetMilestone":true,"viewerCanSubscribe":true,"viewerCanType":false,"viewerCanUpdate":true,` +
	`"viewerCanUpdateMetadata":true,"viewerCannotUpdateReasons":["LOCKED"],"viewerDidAuthor":false,` +
	`"viewerSubscription":"SUBSCRIBED","viewerThreadSubscriptionFormAction":"UNSUBSCRIBE",` +
	`"viewerThreadSubscriptionStatus":"ENABLED"}`

var widthIssueView = `{"number":42,"title":"Crash","state":"closed","updated_at":"2026-01-05T00:00:00Z",` +
	`"assignees":["octocat"],"labels":["help"],"issue_type":"Bug","author":"octocat","milestone":"v1",` +
	`"issue_type_detail":` + widthIssueTypeView + `,"id":"I_42","database_id":4242,"full_database_id":"9007199254740993",` +
	`"url":"https://github.com/octo-org/example/issues/42","author_detail":` + widthUserView + `,` +
	`"author_association":"MEMBER","body":"The body","closed":true,"closed_at":"2026-01-04T00:00:00Z",` +
	`"created_at":"2026-01-01T00:00:00Z","created_via_email":true,"includes_created_edit":false,` +
	`"last_edited_at":"2026-01-02T00:00:00Z","published_at":"2026-01-01T00:00:01Z","editor":` + widthBotView + `,` +
	`"locked":true,"active_lock_reason":"RESOLVED","state_reason":"COMPLETED","is_pinned":true,` +
	`"is_read_by_viewer":false,"milestone_detail":` + widthMilestoneView + `,"parent":` + widthIssueRefView + `,` +
	`"duplicate_of":` + widthIssueRefView + `,"pinned_issue_comment":{"id":"PIC_1","database_id":5,` +
	`"full_database_id":"9007199254740995","pinned_at":"2026-01-03T00:00:00Z","pinned_by":` + widthUserView + `,` +
	`"issue_comment_id":"IC_1"},"sub_issues_summary":{"completed":1,"percent_completed":50,"total":2},` +
	`"issue_dependencies_summary":{"blocked_by":1,"blocking":2,"total_blocked_by":3,"total_blocking":4},` +
	`"reaction_groups":` + widthReactionGroupsView + `,"tracked_issues_count":2,` +
	`"labels_details":[` + widthLabelView + `],"labels_total_count":1,` +
	`"assignees_details":[` + widthUserView + `],"assignees_total_count":11,"assignees_truncated":true,` +
	`"links":{"resource_path":"/octo-org/example/issues/42","body_resource_path":"/octo-org/example/issues/42#issue-4242",` +
	`"body_url":"https://github.com/octo-org/example/issues/42#issue-4242"},` +
	`"assigned_actors":[` + widthBotView + `,` + widthUserView + `],"assigned_actors_total_count":2,` +
	`"closed_by_pull_requests_references":[` + widthIssueRefView + `],"closed_by_pull_requests_references_total_count":1,` +
	`"linked_branches":[{"id":"LB_1","ref_name":"fix-42","target_oid":"abc123"}],"linked_branches_total_count":1,` +
	`"participants":[` + widthUserView + `],"participants_total_count":1,` +
	`"projects_v2":[{"id":"PVT_1","number":7,"title":"Board","url":"https://github.com/orgs/octo-org/projects/7","closed":false}],` +
	`"projects_v2_total_count":1,"relates_to":[` + widthIssueRefView + `],"relates_to_total_count":1,` +
	`"suggested_actors":[` + widthUserView + `],"suggested_actors_total_count":1,` +
	`"tracked_in_issues":[` + widthIssueRefView + `],"tracked_in_issues_total_count":1,` +
	`"tracked_issues":[` + widthIssueRefView + `],"tracked_issues_total_count":1,` +
	`"pending_suggestions":` + widthSuggestionsView + `,"comments_total_count":4,"sub_issues_total_count":2,"blocked_by_total_count":1,"blocking_total_count":2,` +
	`"timeline_items_total_count":9,"user_content_edits_total_count":3,"reactions_total_count":3,` +
	`"project_items_total_count":1,"viewer_can_assign":true,"viewer_can_close":true,"viewer_can_delete":false,` +
	`"viewer_can_label":true,"viewer_can_react":true,"viewer_can_reopen":false,"viewer_can_set_fields":true,` +
	`"viewer_can_set_milestone":true,"viewer_can_subscribe":true,"viewer_can_type":false,"viewer_can_update":true,` +
	`"viewer_can_update_metadata":true,"viewer_cannot_update_reasons":["LOCKED"],"viewer_did_author":false,` +
	`"viewer_subscription":"SUBSCRIBED","viewer_thread_subscription_form_action":"UNSUBSCRIBE",` +
	`"viewer_thread_subscription_status":"ENABLED"}`

func issuePage(nodes ...string) string {
	return `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[` +
		strings.Join(nodes, ",") + `]}}}}`
}

// The list carries every member of GitHub's Issue except the repository, the renderings, the lookups, and
// the lists GitHub serves on their own, and the query asks for exactly the members the output carries.
func TestIssueListCarriesTheFullIssue(t *testing.T) {
	f, core := cannedCore(t, issuePage(widthIssue), false, coreConfig)
	got, err := invoke(t, core, issuesList.ID, "repo", `{"project_details":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(got), `{"issues":[`+widthIssueView+`],"has_more":false,"repository":"octo-org/example"}`)
	requests := f.recorded()
	if len(requests) != 1 || !strings.Contains(requests[0].document, fullDocument(issueListSelection(true))) {
		t.Fatalf("requests = %d, want one query with the issue list selection", len(requests))
	}

	var value map[string]any
	if err := json.Unmarshal([]byte(widthIssueView), &value); err != nil {
		t.Fatal(err)
	}
	entry, _ := json.Marshal(value)
	assertSelectionMatchesOutput(t, issueListSelection(true), entry, entrySchema(t, issuesList.OutputSchema, "issues"),
		[]string{
			"assigned_actors_total_count", "assignees_details", "assignees_total_count", "assignees_truncated",
			"author_detail", "blocked_by_total_count", "blocking_total_count",
			"closed_by_pull_requests_references_total_count", "comments_total_count", "count", "field_id",
			"field_name", "issue_comment_id",
			"issue_type_detail", "labels_details", "labels_total_count", "linked_branches_total_count", "links",
			"milestone_detail", "participants_total_count", "project_items_total_count", "projects_v2_total_count",
			"reactions_total_count", "ref_name", "relates_to_total_count", "sub_issues_total_count",
			"suggested_actors_total_count", "target_oid", "timeline_items_total_count",
			"tracked_in_issues_total_count", "tracked_issues_total_count", "type", "user_content_edits_total_count"},
		[]string{
			"comments", "dateValue", "field", "first", "hasNextPage", "issueComment", "multiValue", "nameWithOwner",
			"nodes", "numberValue", "oid", "optionValue", "pageInfo", "projectItems", "reactions", "reactors",
			"ref", "subIssues", "target", "timelineItems", "totalCount", "userContentEdits"})
	for _, banned := range []string{"bodyHTML", "bodyText", "titleHTML", "hovercard", "projectCards", "timeline{",
		"projectV2(", "repository{" + "id"} {
		if strings.Contains(issueListSelection(true), banned) {
			t.Errorf("selection asks for %s", banned)
		}
	}
}

// An answer without the new members keeps its earlier output.
func TestIssueListWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	const old = `{"number":1,"title":"A","state":"OPEN","updatedAt":"2026-01-01T00:00:00Z",` +
		`"assignees":{"nodes":[{"login":"octocat"}]},"labels":{"nodes":[{"name":"bug"}]},"issueType":null}`
	_, core := cannedCore(t, issuePage(old), false, coreConfig)
	got, err := invoke(t, core, issuesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(got), `{"issues":[{"number":1,"title":"A","state":"open","assignees":["octocat"],`+
		`"labels":["bug"],"updated_at":"2026-01-01T00:00:00Z","assignees_details":[{"login":"octocat"}],`+
		`"labels_details":[{"name":"bug"}]}],"has_more":false,"repository":"octo-org/example"}`)
}

// A connection past its first page is counted and marked; a body is cut and cleaned of control characters.
func TestIssueListCapsConnectionsAndCutsTheBody(t *testing.T) {
	long := "a\u0007b\n" + strings.Repeat("x", issueListBodyLimit)
	quoted, _ := json.Marshal(long)
	node := `{"number":1,"title":"A","state":"OPEN","updatedAt":"2026-01-01T00:00:00Z","body":` + string(quoted) + `,` +
		`"participants":` + widthConnection(11, true, widthUser) + `,"relatesTo":` + widthConnection(3, false, widthIssueRef) + `}`
	_, core := cannedCore(t, issuePage(node), false, coreConfig)
	got, err := invoke(t, core, issuesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Issues []struct {
			Body                   string `json:"body"`
			BodyTruncated          bool   `json:"body_truncated"`
			Participants           []any  `json:"participants"`
			ParticipantsTotalCount int    `json:"participants_total_count"`
			ParticipantsTruncated  bool   `json:"participants_truncated"`
			RelatesToTruncated     bool   `json:"relates_to_truncated"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(got, &result); err != nil || len(result.Issues) != 1 {
		t.Fatalf("result = %s, %v", got, err)
	}
	issue := result.Issues[0]
	if !issue.BodyTruncated || len([]rune(issue.Body)) != issueListBodyLimit || strings.ContainsRune(issue.Body, '\a') ||
		!strings.HasPrefix(issue.Body, "a b\nxx") {
		t.Errorf("body = %q (truncated %t), want a cleaned body of %d characters", issue.Body[:10], issue.BodyTruncated, issueListBodyLimit)
	}
	if len(issue.Participants) != 1 || issue.ParticipantsTotalCount != 11 || !issue.ParticipantsTruncated ||
		!issue.RelatesToTruncated {
		t.Errorf("issue = %+v, want a counted and marked participants list and relates_to cut by its total", issue)
	}
}

// A page of the largest limit stays below the node limit of GitHub: every connection of an issue has a first
// value, and their product with the page size is far below 500000.
func TestIssueListQueryStaysBelowTheNodeLimit(t *testing.T) {
	perIssue := 0
	for _, match := range regexp.MustCompile(`\(first:(\d+)\)`).FindAllStringSubmatch(issuesQuery(true), -1) {
		first, _ := strconv.Atoi(match[1])
		if first > 20 {
			t.Errorf("a connection asks for %d entries", first)
		}
		perIssue += first
	}
	// The pending suggestions have no first value; count them as 100 per issue.
	if nodes := maxLimit + maxLimit*(perIssue+100); nodes != 22100 || nodes >= 500000 {
		t.Errorf("a full page asks for %d nodes, want 22100 and below 500000", nodes)
	}
	if issueConnectionFirst > 10 || issueAssigneesFirst > 10 || issueLabelsFirst != 20 {
		t.Error("the connections are not capped at 10 entries, the labels at 20 as before")
	}
}

// Every filter reaches filterBy, the order reaches orderBy, and the order defaults to the creation order.
func TestIssueListFiltersAndOrderReachGitHub(t *testing.T) {
	f, core := cannedCore(t, issuePage(), false, coreConfig)
	if _, err := invoke(t, core, issuesList.ID, "repo", `{}`, false); err != nil {
		t.Fatal(err)
	}
	variables := lastRequest(t, f).variables
	order, _ := variables["orderBy"].(map[string]any)
	filter, _ := variables["filter"].(map[string]any)
	if order["field"] != "CREATED_AT" || order["direction"] != "DESC" || len(filter) != 1 {
		t.Errorf("default request = %v, want CREATED_AT DESC and only the state", variables)
	}

	const all = `{"created_by":"hubot","mentioned":"octocat","milestone":"v1 beta","milestone_number":"*",` +
		`"since":"2026-01-01T00:00:00Z","type":"Bug","viewer_subscribed":true,"order_by":"COMMENTS","direction":"ASC",` +
		`"issue_field_values":[{"field_id":"IFT_1","text_value":"x"},{"field_name":"Estimate","number_value":2.5},` +
		`{"field_name":"Due","date_value":"2026-04-01"},{"field_name":"P","single_select_option_id":"IFSO_1"},` +
		`{"field_name":"P","single_select_option_value":"High"},` +
		`{"field_name":"A","multi_select_option_ids":["IFSO_1","IFSO_2"]},` +
		`{"field_name":"A","multi_select_option_values":["api","ui"]}]}`
	if _, err := invoke(t, core, issuesList.ID, "repo", all, false); err != nil {
		t.Fatal(err)
	}
	variables = lastRequest(t, f).variables
	order, _ = variables["orderBy"].(map[string]any)
	filter, _ = variables["filter"].(map[string]any)
	want := map[string]any{"states": []any{"OPEN"}, "createdBy": "hubot", "mentioned": "octocat", "milestone": "v1 beta",
		"milestoneNumber": "*", "since": "2026-01-01T00:00:00Z", "type": "Bug", "viewerSubscribed": true,
		"issueFieldValues": []any{
			map[string]any{"fieldId": "IFT_1", "textValue": "x"},
			map[string]any{"fieldName": "Estimate", "numberValue": 2.5},
			map[string]any{"fieldName": "Due", "dateValue": "2026-04-01"},
			map[string]any{"fieldName": "P", "singleSelectOptionId": "IFSO_1"},
			map[string]any{"fieldName": "P", "singleSelectOptionValue": "High"},
			map[string]any{"fieldName": "A", "multiSelectOptionIds": []any{"IFSO_1", "IFSO_2"}},
			map[string]any{"fieldName": "A", "multiSelectOptionValues": []any{"api", "ui"}}}}
	if order["field"] != "COMMENTS" || order["direction"] != "ASC" {
		t.Errorf("order = %v, want COMMENTS ASC", order)
	}
	if fmt.Sprint(filter) != fmt.Sprint(want) {
		t.Errorf("filter = %v\nwant %v", filter, want)
	}

	if _, err := invoke(t, core, issuesList.ID, "repo", `{"milestone":"none","milestone_number":"none"}`, false); err != nil {
		t.Fatal(err)
	}
	filter, _ = lastRequest(t, f).variables["filter"].(map[string]any)
	if value, ok := filter["milestone"]; !ok || value != nil {
		t.Errorf("milestone none = %v, %t, want an explicit null", value, ok)
	}
	if value, ok := filter["milestoneNumber"]; !ok || value != nil {
		t.Errorf("milestone_number none = %v, %t, want an explicit null", value, ok)
	}
}

// A value the bounds refuse never reaches the credential or GitHub.
func TestIssueListRefusesInvalidFiltersBeforeTheCredential(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)
	tooMany := strings.Repeat(`{"field_name":"A","text_value":"x"},`, 10) + `{"field_name":"A","text_value":"x"}`
	for name, arguments := range map[string]string{
		"created_by":        `{"created_by":"bad login"}`,
		"mentioned":         `{"mentioned":"-x"}`,
		"milestone":         `{"milestone":"` + strings.Repeat("m", 257) + `"}`,
		"milestone control": `{"milestone":"a\u0007b"}`,
		"milestone_number":  `{"milestone_number":"abc"}`,
		"since":             `{"since":"yesterday"}`,
		"since date":        `{"since":"2026-01-01"}`,
		"type":              `{"type":"` + strings.Repeat("t", 101) + `"}`,
		"order_by":          `{"order_by":"created_at"}`,
		"direction":         `{"direction":"UP"}`,
		"too many entries":  `{"issue_field_values":[` + tooMany + `]}`,
		"no field":          `{"issue_field_values":[{"text_value":"x"}]}`,
		"both fields":       `{"issue_field_values":[{"field_id":"IFT_1","field_name":"A","text_value":"x"}]}`,
		"no value":          `{"issue_field_values":[{"field_name":"A"}]}`,
		"two values":        `{"issue_field_values":[{"field_name":"A","text_value":"x","number_value":1}]}`,
		"field id":          `{"issue_field_values":[{"field_id":"a b","text_value":"x"}]}`,
		"option id":         `{"issue_field_values":[{"field_name":"A","single_select_option_id":"a b"}]}`,
		"option ids":        `{"issue_field_values":[{"field_name":"A","multi_select_option_ids":["a b"]}]}`,
		"date":              `{"issue_field_values":[{"field_name":"A","date_value":"01.04.2026"}]}`,
		"long text":         `{"issue_field_values":[{"field_name":"A","text_value":"` + strings.Repeat("x", 257) + `"}]}`,
		"many options":      `{"issue_field_values":[{"field_name":"A","multi_select_option_values":["a","b","c","d","e","f","g","h","i","j","k"]}]}`,
	} {
		if _, err := invoke(t, core, issuesList.ID, "repo", arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", name, err)
		}
	}
	if reads != 0 || len(f.recorded()) != 0 {
		t.Error("an invalid filter reached the credential or GitHub")
	}
}

// The cursor binds every filter and the order; a cursor without them stays valid for a request without them.
func TestIssueListCursorBindsFiltersAndOrder(t *testing.T) {
	bound := target{kind: kindRepository, owner: "octo-org", repo: "example"}
	plain := IssueListOptions{State: "open", Assignee: "OctoCat", Labels: []string{"Bug"}, OrderBy: "CREATED_AT", Direction: "DESC"}
	if got, want := plain.binding(bound), fingerprint("issues", bound.String(), "open", folded([]string{"Bug"}), "octocat"); string(got) != string(want) {
		t.Error("the binding without the new filters changed, so earlier cursors would be refused")
	}
	seen := map[string]string{string(plain.binding(bound)): "plain"}
	variants := map[string]func(*IssueListOptions){
		"created_by":       func(o *IssueListOptions) { o.CreatedBy = "hubot" },
		"mentioned":        func(o *IssueListOptions) { o.Mentioned = "hubot" },
		"milestone":        func(o *IssueListOptions) { o.Milestone = "v1" },
		"milestone_number": func(o *IssueListOptions) { o.MilestoneNumber = "3" },
		"since":            func(o *IssueListOptions) { o.Since = "2026-01-01T00:00:00Z" },
		"type":             func(o *IssueListOptions) { o.Type = "Bug" },
		"viewer":           func(o *IssueListOptions) { o.ViewerSubscribed = true },
		"fields":           func(o *IssueListOptions) { o.IssueFieldValues = []IssueFieldFilter{{FieldName: "A", TextValue: "x"}} },
		"order_by":         func(o *IssueListOptions) { o.OrderBy = "UPDATED_AT" },
		"direction":        func(o *IssueListOptions) { o.Direction = "ASC" },
	}
	for name, change := range variants {
		o := plain
		change(&o)
		key := string(o.binding(bound))
		if other, ok := seen[key]; ok {
			t.Errorf("%s binds like %s", name, other)
		}
		seen[key] = name
	}

	const page = `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[]}}}}`
	_, core := cannedCore(t, page, false, coreConfig)
	first, err := invoke(t, core, issuesList.ID, "repo", `{"order_by":"UPDATED_AT","direction":"ASC","type":"Bug"}`, false)
	var result struct {
		NextCursor string `json:"next_cursor"`
	}
	if err != nil || json.Unmarshal(first, &result) != nil || result.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	same := `{"order_by":"UPDATED_AT","direction":"ASC","type":"Bug","cursor":"` + result.NextCursor + `"}`
	if _, err := invoke(t, core, issuesList.ID, "repo", same, false); err != nil {
		t.Errorf("the same filters = %v, want the next batch", err)
	}
	for name, arguments := range map[string]string{
		"other order":  `{"order_by":"CREATED_AT","direction":"ASC","type":"Bug","cursor":"` + result.NextCursor + `"}`,
		"other type":   `{"order_by":"UPDATED_AT","direction":"ASC","type":"Task","cursor":"` + result.NextCursor + `"}`,
		"no filters":   `{"cursor":"` + result.NextCursor + `"}`,
		"other filter": `{"order_by":"UPDATED_AT","direction":"ASC","type":"Bug","mentioned":"hubot","cursor":"` + result.NextCursor + `"}`,
	} {
		if _, err := invoke(t, core, issuesList.ID, "repo", arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", name, err)
		}
	}
}

// Without project_details the query and the output leave the projects out; with it both are read.
func TestIssueListReadsProjectsOnlyOnRequest(t *testing.T) {
	// The answer to a query without the project members holds none, as GitHub's does.
	plain := strings.NewReplacer(`"projectsV2":`+widthConnection(1, false, `{"id":"PVT_1","number":7,"title":"Board","url":"https://github.com/orgs/octo-org/projects/7","closed":false}`), `"x":null`,
		`"projectItems":{"totalCount":1},`, ``).Replace(widthIssue)
	for _, tc := range []struct {
		arguments string
		projects  bool
	}{{`{}`, false}, {`{"project_details":false}`, false}, {`{"project_details":true}`, true}} {
		page := plain
		if tc.projects {
			page = widthIssue
		}
		f, core := cannedCore(t, issuePage(page), false, coreConfig)
		got, err := invoke(t, core, issuesList.ID, "repo", tc.arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		document := lastRequest(t, f).document
		if has := strings.Contains(document, "projectsV2") || strings.Contains(document, "projectItems"); has != tc.projects {
			t.Errorf("%s: query asks for projects = %t, want %t", tc.arguments, has, tc.projects)
		}
		if has := strings.Contains(string(got), "projects_v2") || strings.Contains(string(got), "project_items_total_count"); has != tc.projects {
			t.Errorf("%s: output carries projects = %t, want %t", tc.arguments, has, tc.projects)
		}
		if document != fullDocument(issuesQuery(tc.projects)) {
			t.Errorf("%s: the query is not the selection of project_details %t", tc.arguments, tc.projects)
		}
	}
	reads := 0
	red := &redact.Redactor{}
	bad := application.New(registry(t), coreConfig(serve(t, &fakeGitHub{})), resolver(red, &reads), red)
	if _, err := invoke(t, bad, issuesList.ID, "repo", `{"project_details":"yes"}`, false); err == nil || reads != 0 {
		t.Errorf("a project_details of another type = %v after %d credential reads, want a refusal before any", err, reads)
	}
	// project_details changes the members of an issue, not which issues a page holds, so a cursor stays valid.
	bound := target{kind: kindRepository, owner: "octo-org", repo: "example"}
	a := IssueListOptions{State: "open", OrderBy: "CREATED_AT", Direction: "DESC"}
	b := a
	b.ProjectDetails = true
	if string(a.binding(bound)) != string(b.binding(bound)) {
		t.Error("project_details changes the cursor binding")
	}
}

// Pending suggestions are cut at ten per issue and marked.
func TestIssueListCapsPendingSuggestions(t *testing.T) {
	one := `{"__typename":"PendingTypeSuggestion","id":"PS_1","issueType":{"id":"IT_1","name":"Bug"}}`
	list := strings.TrimSuffix(strings.Repeat(one+",", maxPendingSuggestions+1), ",")
	node := `{"number":1,"title":"A","state":"OPEN","updatedAt":"2026-01-01T00:00:00Z","pendingSuggestions":[` + list + `,null]}`
	_, core := cannedCore(t, issuePage(node), false, coreConfig)
	got, err := invoke(t, core, issuesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Issues []struct {
			PendingSuggestions          []map[string]any `json:"pending_suggestions"`
			PendingSuggestionsTruncated bool             `json:"pending_suggestions_truncated"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(got, &result); err != nil || len(result.Issues) != 1 ||
		len(result.Issues[0].PendingSuggestions) != maxPendingSuggestions || !result.Issues[0].PendingSuggestionsTruncated {
		t.Errorf("result = %s, %v, want %d suggestions marked as truncated", got, err, maxPendingSuggestions)
	}
}
