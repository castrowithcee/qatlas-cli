package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The pull request width: the members of GitHub's REST pull request beyond the compact view the list and get
// tools answered before. The compact members keep their own treatment in the tools; everything here passes
// through the shared projection helpers. The _links member is left out, since its addresses repeat the *_url
// members that links holds, and the rendered body variants add no content.

// pullEntry decodes one REST pull request twice from the same object: the compact view and the width. A
// member the width cannot read is left out rather than failing a read the compact view already decoded.
type pullEntry struct {
	pull pullJSON
	wide pullSimpleRaw
}

func (e *pullEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.pull); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// PullWidth holds the members of GitHub's pull request that every pull request read adds to its compact view.
type PullWidth struct {
	ID                                 *int64             `json:"id,omitempty"`
	NodeID                             *string            `json:"node_id,omitempty"`
	Locked                             *bool              `json:"locked,omitempty"`
	ActiveLockReason                   *string            `json:"active_lock_reason,omitempty"`
	AuthorDetail                       *RESTAccount       `json:"author_detail,omitempty"`
	AuthorAssociation                  *string            `json:"author_association,omitempty"`
	Milestone                          *string            `json:"milestone,omitempty"`
	MilestoneDetail                    *RESTMilestone     `json:"milestone_detail,omitempty"`
	Assignee                           *RESTAccount       `json:"assignee,omitempty"`
	Assignees                          []string           `json:"assignees,omitempty"`
	AssigneesTruncated                 bool               `json:"assignees_truncated,omitempty"`
	AssigneesDetails                   []RESTAccount      `json:"assignees_details,omitempty"`
	AssigneesDetailsTruncated          bool               `json:"assignees_details_truncated,omitempty"`
	LabelsDetails                      []RESTLabel        `json:"labels_details,omitempty"`
	LabelsDetailsTruncated             bool               `json:"labels_details_truncated,omitempty"`
	RequestedReviewersDetails          []RESTAccount      `json:"requested_reviewers_details,omitempty"`
	RequestedReviewersDetailsTruncated bool               `json:"requested_reviewers_details_truncated,omitempty"`
	RequestedTeams                     []RESTTeamSimple   `json:"requested_teams,omitempty"`
	RequestedTeamsTruncated            bool               `json:"requested_teams_truncated,omitempty"`
	HeadDetail                         *RESTPullRef       `json:"head_detail,omitempty"`
	BaseDetail                         *RESTPullRef       `json:"base_detail,omitempty"`
	AutoMerge                          *RESTPullAutoMerge `json:"auto_merge,omitempty"`
	Stack                              *RESTPullStack     `json:"stack,omitempty"`
	Links                              Links              `json:"links,omitempty"`
}

// PullDetail holds the members only the single pull request read answers.
type PullDetail struct {
	Rebaseable          *bool        `json:"rebaseable,omitempty"`
	MergedBy            *RESTAccount `json:"merged_by,omitempty"`
	Comments            *int64       `json:"comments,omitempty"`
	ReviewComments      *int64       `json:"review_comments,omitempty"`
	MaintainerCanModify *bool        `json:"maintainer_can_modify,omitempty"`
	Additions           *int64       `json:"additions,omitempty"`
	Deletions           *int64       `json:"deletions,omitempty"`
}

// PullRequestDetail is the single pull request read: the compact view, the width, and the detail members.
type PullRequestDetail struct {
	PullRequest
	PullWidth
	PullDetail
}

// width projects the width of one pull request. The repositories of head and base are shown in full only when
// the targets admit them; a fork outside the targets keeps its identifiers.
func (r *pullSimpleRaw) width(allowed allowlist) PullWidth {
	w := PullWidth{ID: r.ID, NodeID: shortText(r.NodeID), Locked: r.Locked,
		ActiveLockReason: shortText(r.ActiveLockReason), AuthorDetail: r.User.project(),
		AuthorAssociation: shortText(r.AuthorAssociation), MilestoneDetail: r.Milestone.detail(),
		Assignee: r.Assignee.project(), HeadDetail: r.Head.project(allowed), BaseDetail: r.Base.project(allowed),
		AutoMerge: r.autoMerge(), Stack: r.stack(), Links: r.links}
	if r.Milestone != nil {
		w.Milestone = shortText(r.Milestone.Title)
	}
	assignees, cut := capped(r.Assignees)
	for i := range assignees {
		if login := shortText(assignees[i].Login); login != nil {
			w.Assignees = append(w.Assignees, *login)
		}
		w.AssigneesDetails = append(w.AssigneesDetails, *assignees[i].project())
	}
	w.AssigneesTruncated, w.AssigneesDetailsTruncated = cut, cut
	labels, cut := capped(r.Labels)
	for i := range labels {
		w.LabelsDetails = append(w.LabelsDetails, labels[i].detail())
	}
	w.LabelsDetailsTruncated = cut
	w.RequestedReviewersDetails, w.RequestedReviewersDetailsTruncated = accounts(r.RequestedReviewers)
	teams, cut := capped(r.RequestedTeams)
	for i := range teams {
		w.RequestedTeams = append(w.RequestedTeams, *teams[i].core.simple())
	}
	w.RequestedTeamsTruncated = cut
	return w
}

func (r *pullSimpleRaw) detail() PullDetail {
	return PullDetail{Rebaseable: r.Rebaseable, MergedBy: r.MergedBy.project(), Comments: r.Comments,
		ReviewComments: r.ReviewComments, MaintainerCanModify: r.MaintainerCanModify, Additions: r.Additions,
		Deletions: r.Deletions}
}

// Schemas of the pull request width.

var pullWidthProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"locked":{"type":"boolean"},` +
	`"active_lock_reason":{"type":"string"},` + detailProperty("author", restAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"milestone":{"type":"string"},` +
	detailProperty("milestone", objectSchema(milestoneProperties), false) + `,"assignee":` + restAccountSchema + `,` +
	`"assignees":{"type":"array","maxItems":100,"items":{"type":"string"}},"assignees_truncated":{"type":"boolean"},` +
	detailProperty("assignees", restAccountSchema, true) + `,"assignees_details_truncated":{"type":"boolean"},` +
	detailProperty("labels", objectSchema(labelProperties), true) + `,"labels_details_truncated":{"type":"boolean"},` +
	detailProperty("requested_reviewers", restAccountSchema, true) +
	`,"requested_reviewers_details_truncated":{"type":"boolean"},` +
	`"requested_teams":{"type":"array","maxItems":100,"items":` + teamSimpleSchema + `},` +
	`"requested_teams_truncated":{"type":"boolean"},` +
	detailProperty("head", restPullRefSchema, false) + `,` + detailProperty("base", restPullRefSchema, false) + `,` +
	`"auto_merge":` + restPullAutoMergeSchema + `,"stack":` + restPullStackSchema + `,` +
	`"links":` + linksSchema(restPullSimpleLinkKeys)

var pullDetailProperties = `"rebaseable":{"type":"boolean"},"merged_by":` + restAccountSchema + `,` +
	`"comments":{"type":"integer"},"review_comments":{"type":"integer"},"maintainer_can_modify":{"type":"boolean"},` +
	`"additions":{"type":"integer"},"deletions":{"type":"integer"}`

var pullWidthFields = []capability.Field{
	{Name: "id", Description: "Numeric REST id of the pull request"},
	{Name: "node_id", Description: "GraphQL node id of the pull request"},
	{Name: "locked", Description: "True when the conversation is locked"},
	{Name: "active_lock_reason", Description: "Why the conversation is locked, such as too heated"},
	detailField("author", "Author account: "+restAccountMembers+",", false),
	{Name: "author_association", Description: "How the author relates to the repository, such as OWNER or MEMBER"},
	{Name: "milestone", Description: "Title of the milestone, untrusted data"},
	detailField("milestone", "Milestone with number, title, description, state, due_on, open_issues, "+
		"closed_issues, id, node_id, creator, created_at, updated_at, closed_at, url, and links,", false),
	{Name: "assignee", Description: "First assignee as an account: " + restAccountMembers},
	{Name: "assignees", Description: "Logins of the assignees, at most 100"},
	{Name: "assignees_truncated", Description: "True when the pull request holds more assignees than shown"},
	detailField("assignees", "Assignee accounts, at most 100,", true),
	{Name: "assignees_details_truncated", Description: "True when the pull request holds more assignees than shown"},
	detailField("labels", "Labels, at most 100, with name, color, description, default, id, node_id, "+
		"archived_at, archived_by, and links,", true),
	{Name: "labels_details_truncated", Description: "True when the pull request carries more labels than shown"},
	detailField("requested_reviewers", "Accounts whose review is requested, at most 100,", true),
	{Name: "requested_reviewers_details_truncated", Description: "True when more reviewers were requested than shown"},
	{Name: "requested_teams", Description: "Teams whose review is requested, at most 100: id, node_id, name, " +
		"slug, description, permission, privacy, notification_setting, type, ldap_dn, organization_id, " +
		"enterprise_id, url, and links; untrusted data"},
	{Name: "requested_teams_truncated", Description: "True when more teams were requested than shown"},
	detailField("head", "Head with label, ref, sha, user, and repo as a repository; a repository outside the "+
		"connection's targets keeps only id, node_id, and full_name,", false),
	detailField("base", "Base with label, ref, sha, user, and repo as a repository, shown like head,", false),
	{Name: "auto_merge", Description: "enabled_by (an account), merge_method, commit_title, and commit_message " +
		"(cut at 4000 characters, commit_message_truncated says so) of the auto-merge setting; untrusted data"},
	{Name: "stack", Description: "base (ref and sha), size, position, id, and number of the stack the pull " +
		"request belongs to"},
	{Name: "links", Description: "REST url, diff_url, patch_url, issue_url, commits_url, review_comments_url, " +
		"review_comment_url, comments_url, and statuses_url by their GitHub names, untrusted data, never fetched"},
}

var pullDetailFields = []capability.Field{
	{Name: "rebaseable", Description: "Whether GitHub can rebase it; absent while GitHub is still computing it"},
	{Name: "merged_by", Description: "Account that merged the pull request: " + restAccountMembers},
	{Name: "comments", Description: "Number of comments on the conversation"},
	{Name: "review_comments", Description: "Number of review comments on the diff"},
	{Name: "maintainer_can_modify", Description: "True when maintainers of the base repository may push to the head branch"},
	{Name: "additions", Description: "Number of added lines"},
	{Name: "deletions", Description: "Number of deleted lines"},
}
