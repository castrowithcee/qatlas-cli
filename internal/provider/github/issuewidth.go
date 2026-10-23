package github

import (
	"bytes"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The issue width: the members of GitHub's REST issue beyond the compact view each issue tool answered
// before. Everything here is new next to those views and passes through the shared projection helpers; the
// members a tool answered before keep their own treatment in the tool. The pull_request member is never
// read, since the issue tools refuse pull requests, and the rendered body variants body_html and body_text
// are left out.

var issueLinkKeys = []string{"url", "repository_url", "labels_url", "comments_url", "events_url", "timeline_url",
	"parent_issue_url"}

type issueFieldOptionRaw struct {
	ID    *int64  `json:"id"`
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

type issueFieldValueRaw struct {
	IssueFieldID       *int64                `json:"issue_field_id"`
	IssueFieldName     *string               `json:"issue_field_name"`
	NodeID             *string               `json:"node_id"`
	DataType           *string               `json:"data_type"`
	Value              json.RawMessage       `json:"value"`
	SingleSelectOption *issueFieldOptionRaw  `json:"single_select_option"`
	MultiSelectOptions []issueFieldOptionRaw `json:"multi_select_options"`
}

// issueWidthRaw is the part of one REST issue object the issue width reads; it is decoded beside the compact
// view of each tool, from the same object.
type issueWidthRaw struct {
	ID                       *int64                        `json:"id"`
	NodeID                   *string                       `json:"node_id"`
	StateReason              *string                       `json:"state_reason"`
	Body                     *string                       `json:"body"`
	User                     *accountRaw                   `json:"user"`
	Assignee                 *accountRaw                   `json:"assignee"`
	Assignees                []*accountRaw                 `json:"assignees"`
	Labels                   []labelRaw                    `json:"labels"`
	Milestone                *milestoneRaw                 `json:"milestone"`
	Type                     *issueTypeJSON                `json:"type"`
	Locked                   *bool                         `json:"locked"`
	ActiveLockReason         *string                       `json:"active_lock_reason"`
	Comments                 *int64                        `json:"comments"`
	Draft                    *bool                         `json:"draft"`
	ClosedBy                 *accountRaw                   `json:"closed_by"`
	CreatedAt                *string                       `json:"created_at"`
	UpdatedAt                *string                       `json:"updated_at"`
	ClosedAt                 *string                       `json:"closed_at"`
	AuthorAssociation        *string                       `json:"author_association"`
	PerformedViaGitHubApp    *appRaw                       `json:"performed_via_github_app"`
	Reactions                *reactionsRaw                 `json:"reactions"`
	SubIssuesSummary         *subIssuesSummaryRaw          `json:"sub_issues_summary"`
	IssueDependenciesSummary *RESTIssueDependenciesSummary `json:"issue_dependencies_summary"`
	IssueFieldValues         []issueFieldValueRaw          `json:"issue_field_values"`
	PinnedComment            *issueCommentRaw              `json:"pinned_comment"`
	Repository               *repositoryRaw                `json:"repository"`
	Score                    *json.Number                  `json:"score"`
	TextMatches              []textMatchRaw                `json:"text_matches"`
	links                    Links
}

func (r *issueWidthRaw) UnmarshalJSON(data []byte) error {
	type plain issueWidthRaw
	err := json.Unmarshal(data, (*plain)(r))
	r.links, _ = readLinks(data, issueLinkKeys)
	return err
}

// readIssueWidth decodes the width of one issue object. A member the width cannot read is left out rather
// than failing a read the compact view already decoded, so the width never turns an answer into an error.
func readIssueWidth(data []byte) issueWidthRaw {
	var width issueWidthRaw
	_ = json.Unmarshal(data, &width)
	return width
}

// RESTIssueDependenciesSummary is the dependency count GitHub reports on an issue.
type RESTIssueDependenciesSummary struct {
	BlockedBy      *int64 `json:"blocked_by,omitempty"`
	Blocking       *int64 `json:"blocking,omitempty"`
	TotalBlockedBy *int64 `json:"total_blocked_by,omitempty"`
	TotalBlocking  *int64 `json:"total_blocking,omitempty"`
}

// RESTIssueFieldOption is one option of a select issue field value.
type RESTIssueFieldOption struct {
	ID    *int64  `json:"id,omitempty"`
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

// RESTIssueFieldValue is one issue field value of an issue. Value is free-form; beyond the bounds of a
// custom property value it is left out and ValueOmitted says so.
type RESTIssueFieldValue struct {
	IssueFieldID                *int64                 `json:"issue_field_id,omitempty"`
	IssueFieldName              *string                `json:"issue_field_name,omitempty"`
	NodeID                      *string                `json:"node_id,omitempty"`
	DataType                    *string                `json:"data_type,omitempty"`
	Value                       any                    `json:"value,omitempty"`
	ValueOmitted                bool                   `json:"value_omitted,omitempty"`
	SingleSelectOption          *RESTIssueFieldOption  `json:"single_select_option,omitempty"`
	MultiSelectOptions          []RESTIssueFieldOption `json:"multi_select_options,omitempty"`
	MultiSelectOptionsTruncated bool                   `json:"multi_select_options_truncated,omitempty"`
}

// IssueWidth holds the members of GitHub's issue every issue read adds to its own compact view.
type IssueWidth struct {
	ID                        *int64                        `json:"id,omitempty"`
	NodeID                    *string                       `json:"node_id,omitempty"`
	Assignee                  *RESTAccount                  `json:"assignee,omitempty"`
	AssigneesDetails          []RESTAccount                 `json:"assignees_details,omitempty"`
	AssigneesDetailsTruncated bool                          `json:"assignees_details_truncated,omitempty"`
	LabelsDetails             []RESTLabel                   `json:"labels_details,omitempty"`
	LabelsDetailsTruncated    bool                          `json:"labels_details_truncated,omitempty"`
	MilestoneDetail           *RESTMilestone                `json:"milestone_detail,omitempty"`
	IssueTypeDetail           *RESTIssueType                `json:"issue_type_detail,omitempty"`
	Locked                    *bool                         `json:"locked,omitempty"`
	ActiveLockReason          *string                       `json:"active_lock_reason,omitempty"`
	Comments                  *int64                        `json:"comments,omitempty"`
	Draft                     *bool                         `json:"draft,omitempty"`
	ClosedBy                  *RESTAccount                  `json:"closed_by,omitempty"`
	AuthorAssociation         *string                       `json:"author_association,omitempty"`
	PerformedViaGitHubApp     *RESTApp                      `json:"performed_via_github_app,omitempty"`
	Reactions                 *RESTReactions                `json:"reactions,omitempty"`
	SubIssuesSummary          *SubIssuesSummary             `json:"sub_issues_summary,omitempty"`
	IssueDependenciesSummary  *RESTIssueDependenciesSummary `json:"issue_dependencies_summary,omitempty"`
	IssueFieldValues          []RESTIssueFieldValue         `json:"issue_field_values,omitempty"`
	IssueFieldValuesTruncated bool                          `json:"issue_field_values_truncated,omitempty"`
	PinnedComment             *RESTIssueComment             `json:"pinned_comment,omitempty"`
	Links                     Links                         `json:"links,omitempty"`
}

// IssueCore holds the members of GitHub's issue that the compact entries of the issue search, the sub-issue
// list, and the dependency list lack and the detail read already answers on its own.
type IssueCore struct {
	StateReason   *string      `json:"state_reason,omitempty"`
	Body          *string      `json:"body,omitempty"`
	BodyTruncated bool         `json:"body_truncated,omitempty"`
	User          *RESTAccount `json:"user,omitempty"`
	IssueType     *string      `json:"issue_type,omitempty"`
	CreatedAt     *string      `json:"created_at,omitempty"`
	ClosedAt      *string      `json:"closed_at,omitempty"`
}

func (r *issueWidthRaw) width() IssueWidth {
	w := IssueWidth{ID: r.ID, NodeID: shortText(r.NodeID), Assignee: r.Assignee.project(),
		MilestoneDetail: r.Milestone.detail(), IssueTypeDetail: r.Type.detail(), Locked: r.Locked,
		ActiveLockReason: shortText(r.ActiveLockReason), Comments: r.Comments, Draft: r.Draft,
		ClosedBy: r.ClosedBy.project(), AuthorAssociation: shortText(r.AuthorAssociation),
		PerformedViaGitHubApp: r.PerformedViaGitHubApp.project(), Reactions: r.Reactions.project(),
		SubIssuesSummary: r.SubIssuesSummary.project(), PinnedComment: r.PinnedComment.project(), Links: r.links}
	assignees, cut := capped(r.Assignees)
	for _, assignee := range assignees {
		if account := assignee.project(); account != nil {
			w.AssigneesDetails = append(w.AssigneesDetails, *account)
		}
	}
	w.AssigneesDetailsTruncated = cut
	labels, cut := capped(r.Labels)
	for i := range labels {
		w.LabelsDetails = append(w.LabelsDetails, labels[i].detail())
	}
	w.LabelsDetailsTruncated = cut
	if s := r.IssueDependenciesSummary; s != nil && (s.BlockedBy != nil || s.Blocking != nil ||
		s.TotalBlockedBy != nil || s.TotalBlocking != nil) {
		w.IssueDependenciesSummary = s
	}
	values, cut := capped(r.IssueFieldValues)
	for _, value := range values {
		w.IssueFieldValues = append(w.IssueFieldValues, value.project())
	}
	w.IssueFieldValuesTruncated = cut
	return w
}

func (r *issueWidthRaw) core() IssueCore {
	body, cut := cleanText(filled(r.Body), projectionBodyLimit, true)
	core := IssueCore{StateReason: shortText(r.StateReason), Body: body, BodyTruncated: cut, User: r.User.project(),
		CreatedAt: shortText(r.CreatedAt), ClosedAt: shortText(r.ClosedAt)}
	if r.Type != nil {
		core.IssueType = shortText(r.Type.Name)
	}
	return core
}

func (o *issueFieldOptionRaw) project() *RESTIssueFieldOption {
	if o == nil {
		return nil
	}
	return &RESTIssueFieldOption{ID: o.ID, Name: shortText(o.Name), Color: shortText(o.Color)}
}

func (r issueFieldValueRaw) project() RESTIssueFieldValue {
	value := RESTIssueFieldValue{IssueFieldID: r.IssueFieldID, IssueFieldName: shortText(r.IssueFieldName),
		NodeID: shortText(r.NodeID), DataType: shortText(r.DataType),
		SingleSelectOption: r.SingleSelectOption.project()}
	value.Value, value.ValueOmitted = boundedFieldValue(r.Value)
	options, cut := capped(r.MultiSelectOptions)
	for i := range options {
		value.MultiSelectOptions = append(value.MultiSelectOptions, *options[i].project())
	}
	value.MultiSelectOptionsTruncated = cut
	return value
}

// boundedFieldValue decodes a free-form issue field value within the bounds of a custom property value.
// A null or empty value stays out; a value beyond the bounds is left out and reported, never cut.
func boundedFieldValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, true
	}
	nodes := 0
	value, ok := boundedValue(value, 1, &nodes)
	if !ok {
		return nil, true
	}
	if text, isText := value.(string); isText && text == "" {
		return nil, false
	}
	return value, false
}

// withoutLink returns links without key, or nil when nothing remains.
func withoutLink(links Links, key string) Links {
	if _, ok := links[key]; !ok {
		return links
	}
	out := Links{}
	for k, v := range links {
		if k != key {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Schemas of the issue width.

const issueDependenciesSummarySchema = `{"type":"object","properties":{"blocked_by":{"type":"integer"},` +
	`"blocking":{"type":"integer"},"total_blocked_by":{"type":"integer"},"total_blocking":{"type":"integer"}},` +
	`"additionalProperties":false}`

const issueFieldOptionSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"color":{"type":"string"}},"additionalProperties":false}`

const issueFieldValueOutputSchema = `{"type":"object","properties":{"issue_field_id":{"type":"integer"},` +
	`"issue_field_name":{"type":"string"},"node_id":{"type":"string"},"data_type":{"type":"string"},` +
	`"value":{"anyOf":[{"type":"string"},{"type":"number"},{"type":"boolean"},{"type":"object"},{"type":"array"}]},` +
	`"value_omitted":{"type":"boolean"},"single_select_option":` + issueFieldOptionSchema + `,` +
	`"multi_select_options":{"type":"array","maxItems":100,"items":` + issueFieldOptionSchema + `},` +
	`"multi_select_options_truncated":{"type":"boolean"}},"additionalProperties":false}`

// issueDraftProperty is the draft member of the issue width; the shared search item schema declares it
// already, so the search adds the width without it.
const issueDraftProperty = `"draft":{"type":"boolean"}`

// issueWidthLeadProperties are the schema members of IssueWidth except draft and links.
var issueWidthLeadProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"assignee":` + restAccountSchema +
	`,` + detailProperty("assignees", restAccountSchema, true) + `,"assignees_details_truncated":{"type":"boolean"},` +
	detailProperty("labels", objectSchema(labelProperties), true) + `,"labels_details_truncated":{"type":"boolean"},` +
	detailProperty("milestone", objectSchema(milestoneProperties), false) + `,` +
	detailProperty("issue_type", objectSchema(issueTypeProperties), false) + `,` +
	`"locked":{"type":"boolean"},"active_lock_reason":{"type":"string"},"comments":{"type":"integer"},` +
	`"closed_by":` + restAccountSchema + `,"author_association":{"type":"string"},` +
	`"performed_via_github_app":` + restAppSchema + `,"reactions":` + restReactionsSchema + `,` +
	`"sub_issues_summary":` + subIssuesSummaryOutput + `,"issue_dependencies_summary":` +
	issueDependenciesSummarySchema + `,"issue_field_values":{"type":"array","maxItems":100,"items":` +
	issueFieldValueOutputSchema + `},"issue_field_values_truncated":{"type":"boolean"},` +
	`"pinned_comment":` + restIssueCommentSchema

// issueWidthProperties are the schema members of IssueWidth except draft.
var issueWidthProperties = issueWidthLeadProperties + `,"links":` + linksSchema(issueLinkKeys)

// issueCoreProperties are the schema members of IssueCore.
var issueCoreProperties = `"state_reason":{"type":"string"},"body":{"type":"string"},` +
	`"body_truncated":{"type":"boolean"},"user":` + restAccountSchema + `,"issue_type":{"type":"string"},` +
	`"created_at":{"type":"string"},"closed_at":{"type":"string"}`

// repositoryDetailField describes the repository of an issue whose repository the call does not fix.
var repositoryDetailField = capability.Field{Name: "repository_detail", Description: "Repository of the " +
	"issue with the members of GitHub's repository, untrusted data, addresses never fetched; a repository " +
	"outside the connection's targets keeps only id, node_id, and full_name"}

var issueDraftField = capability.Field{Name: "draft", Description: "True for a draft issue"}

// issueWidthFields describe IssueWidth except draft.
var issueWidthFields = []capability.Field{
	{Name: "id", Description: "Numeric REST id of the issue, as the sub-issue and dependency routes name it"},
	{Name: "node_id", Description: "GraphQL node id of the issue"},
	{Name: "assignee", Description: "First assignee as an account: " + restAccountMembers},
	detailField("assignees", "Assignee accounts, at most 100,", true),
	{Name: "assignees_details_truncated", Description: "True when the issue holds more assignees than shown"},
	detailField("labels", "Labels, at most 100, with name, color, description, default, id, node_id, "+
		"archived_at, archived_by, and links,", true),
	{Name: "labels_details_truncated", Description: "True when the issue carries more labels than shown"},
	detailField("milestone", "Milestone with number, title, description, state, due_on, open_issues, "+
		"closed_issues, id, node_id, creator, created_at, updated_at, closed_at, url, and links,", false),
	detailField("issue_type", "Issue type with id, node_id, name, description, color, is_enabled, created_at, "+
		"and updated_at,", false),
	{Name: "locked", Description: "True when the conversation is locked"},
	{Name: "active_lock_reason", Description: "Why the conversation is locked, such as too heated"},
	{Name: "comments", Description: "Number of comments"},
	{Name: "closed_by", Description: "Account that closed the issue: " + restAccountMembers},
	{Name: "author_association", Description: "How the author relates to the repository, such as OWNER or MEMBER"},
	{Name: "performed_via_github_app", Description: "App the issue was opened through: " + restAppMembers},
	{Name: "reactions", Description: "Reaction counts: " + restReactionsMembers},
	{Name: "sub_issues_summary", Description: "total, completed, and percent_completed sub-issues of the issue"},
	{Name: "issue_dependencies_summary", Description: "blocked_by, blocking, total_blocked_by, and " +
		"total_blocking dependency counts of the issue"},
	{Name: "issue_field_values", Description: "Issue field values, at most 100: issue_field_id, " +
		"issue_field_name, node_id, data_type, value (left out beyond the bounds of a custom property value, " +
		"value_omitted says so), single_select_option, and multi_select_options (id, name, color); untrusted data"},
	{Name: "issue_field_values_truncated", Description: "True when the issue holds more field values than shown"},
	{Name: "pinned_comment", Description: "Pinned comment of the issue, with the members of an issue comment: " +
		"id, node_id, body (cut at 4000 characters, body_truncated says so), user, created_at, updated_at, " +
		"author_association, performed_via_github_app, reactions, pin, minimized, url, and links; untrusted data"},
	{Name: "links", Description: "REST url, repository_url, labels_url, comments_url, events_url, " +
		"timeline_url, and parent_issue_url by their GitHub names, untrusted data, never fetched"},
}

// issueCoreFields describe IssueCore.
var issueCoreFields = []capability.Field{
	{Name: "state_reason", Description: "Why the issue was closed or reopened, such as completed or not_planned"},
	{Name: "body", Description: "Issue body, cut at 4000 characters (body_truncated says so), untrusted data"},
	{Name: "body_truncated", Description: "True when the body was cut"},
	{Name: "user", Description: "Author as an account: " + restAccountMembers},
	{Name: "issue_type", Description: "Name of the issue type, as github.issue-types.list reports it; absent " +
		"when none is set"},
	{Name: "created_at", Description: "When the issue was created"},
	{Name: "closed_at", Description: "When the issue was closed; absent while it is open"},
}
