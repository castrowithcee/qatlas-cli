package github

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The issue list width: the members of GitHub's Issue the issue list answers beyond the compact view. The
// repository of an issue is left out, since the call names it; the rendered variants bodyHTML, bodyText, and
// titleHTML, the hovercard, and the deprecated projectCards and timeline repeat other members or are
// obsolete; projectV2(number:) is a lookup and no member. The projects and their item count are read only on
// request (project_details), since they need project read access of the token. The pending suggestions are a
// list without paging that is cut here at maxPendingSuggestions. The comments, sub-issues, dependencies, timeline,
// edit history, reactions, and project items are counted only, since GitHub serves them as lists of their
// own; the other connections are read with their first page.
//
// The query bounds one page: at the largest limit of 100 the issue connection holds 100 nodes, and each
// issue asks for at most issueLabelsFirst labels, issueAssigneesFirst assignees, and issueConnectionFirst
// entries in each of nine further connections (ten with project_details), 120 nodes per issue (130) and 12100
// (13100) nodes with the issues themselves. The pending suggestions have no first value; counted generously
// as 100 per issue they add 10000 nodes. All of it is far below the 500000 nodes GitHub allows. A body arrives complete, since GraphQL cannot cut it, and is cut here.

const (
	issueListBodyLimit   = 1000
	issueLabelsFirst     = 20
	issueAssigneesFirst  = 10
	issueConnectionFirst = 10
)

// issueConnection selects the first page of a connection with its total.
func issueConnection(name string, first int, nodes string) string {
	return name + "(first:" + strconv.Itoa(first) + "){totalCount pageInfo{hasNextPage} nodes{" + nodes + "}}"
}

// issueListSelection selects one issue of the list. The projects and the project item count need project read
// access of the token, so they are asked for only when projects is set.
func issueListSelection(projects bool) string {
	selection := issueListBaseSelection
	if projects {
		selection += ` ` + issueConnection("projectsV2", issueConnectionFirst, `id number title url closed`) +
			` projectItems{totalCount}`
	}
	return selection
}

// issueListBaseSelection is the selection of one issue without the project members.
var issueListBaseSelection = `number title state updatedAt ` +
	issueConnection("assignees", issueAssigneesFirst, gqlUserSelection) + ` ` +
	issueConnection("labels", issueLabelsFirst, gqlLabelSelection) + ` ` +
	`issueType{` + gqlIssueTypeSelection + `} ` +
	`id databaseId fullDatabaseId url author{` + gqlAccountSelection + `} authorAssociation body ` +
	`bodyResourcePath bodyUrl closed closedAt createdAt createdViaEmail includesCreatedEdit lastEditedAt ` +
	`publishedAt editor{` + gqlAccountSelection + `} locked activeLockReason stateReason isPinned ` +
	`isReadByViewer milestone{` + gqlMilestoneSelection + `} parent{` + gqlReferenceSelection + `} ` +
	`duplicateOf{` + gqlReferenceSelection + `} pinnedIssueComment{id databaseId fullDatabaseId pinnedAt ` +
	`pinnedBy{` + gqlAccountSelection + `} issueComment{id}} subIssuesSummary{completed percentCompleted total} ` +
	`issueDependenciesSummary{blockedBy blocking totalBlockedBy totalBlocking} ` +
	`reactionGroups{` + gqlReactionGroupSelection + `} trackedIssuesCount resourcePath ` +
	issueConnection("assignedActors", issueConnectionFirst, `... on Actor{`+gqlAccountSelection+`}`) + ` ` +
	issueConnection("closedByPullRequestsReferences", issueConnectionFirst, gqlReferenceSelection) + ` ` +
	issueConnection("linkedBranches", issueConnectionFirst, `id ref{name target{oid}}`) + ` ` +
	issueConnection("participants", issueConnectionFirst, gqlUserSelection) + ` ` +
	issueConnection("relatesTo", issueConnectionFirst, gqlReferenceSelection) + ` ` +
	issueConnection("suggestedActors", issueConnectionFirst, `... on Actor{`+gqlAccountSelection+`}`) + ` ` +
	issueConnection("trackedInIssues", issueConnectionFirst, gqlReferenceSelection) + ` ` +
	issueConnection("trackedIssues", issueConnectionFirst, gqlReferenceSelection) + ` ` +
	issueConnection("issueFieldValues", issueConnectionFirst, issueFieldValueSelection) + ` ` +
	`comments{totalCount} subIssues{totalCount} blockedBy{totalCount} blocking{totalCount} ` +
	`timelineItems{totalCount} userContentEdits{totalCount} reactions{totalCount} ` +
	pendingSuggestionsSelection + ` ` +
	`viewerCanAssign viewerCanClose viewerCanDelete viewerCanLabel viewerCanReact viewerCanReopen ` +
	`viewerCanSetFields viewerCanSetMilestone viewerCanSubscribe viewerCanType viewerCanUpdate ` +
	`viewerCanUpdateMetadata viewerCannotUpdateReasons viewerDidAuthor viewerSubscription ` +
	`viewerThreadSubscriptionFormAction viewerThreadSubscriptionStatus`

// pendingSuggestionsSelection selects the pending suggestions of an issue, a list without paging. The nested
// objects carry only their identifiers; accounts use the shared selection.
const pendingSuggestionsSelection = `pendingSuggestions{__typename ` +
	`... on PendingAssigneeSuggestion{id actor{` + gqlAccountSelection + `} assignee{... on Actor{` + gqlAccountSelection + `}} createdAt rationale updatedAt} ` +
	`... on PendingCloseSuggestion{id actor{` + gqlAccountSelection + `} createdAt duplicateOf{__typename ... on Issue{id number repository{nameWithOwner}} ` +
	`... on PullRequest{id number repository{nameWithOwner}}} rationale stateReason updatedAt} ` +
	`... on PendingFieldSuggestion{id actor{` + gqlAccountSelection + `} createdAt issueField{... on Node{id} ... on IssueFieldCommon{name dataType}} ` +
	`rationale suggestedValue updatedAt} ` +
	`... on PendingLabelSuggestion{id actor{` + gqlAccountSelection + `} createdAt label{id name} rationale updatedAt} ` +
	`... on PendingTypeSuggestion{id actor{` + gqlAccountSelection + `} createdAt issueType{id name} rationale updatedAt}}`

// maxPendingSuggestions caps the pending suggestions of one issue; GitHub serves the list without paging.
const maxPendingSuggestions = 10

type pendingSuggestionRaw struct {
	Typename       *string          `json:"__typename"`
	ID             *string          `json:"id"`
	Actor          *gqlAccountRaw   `json:"actor"`
	Assignee       *gqlAccountRaw   `json:"assignee"`
	CreatedAt      *string          `json:"createdAt"`
	UpdatedAt      *string          `json:"updatedAt"`
	Rationale      *string          `json:"rationale"`
	StateReason    *string          `json:"stateReason"`
	SuggestedValue *string          `json:"suggestedValue"`
	DuplicateOf    *gqlReferenceRaw `json:"duplicateOf"`
	IssueField     *struct {
		ID       *string `json:"id"`
		Name     *string `json:"name"`
		DataType *string `json:"dataType"`
	} `json:"issueField"`
	Label *struct {
		ID   *string `json:"id"`
		Name *string `json:"name"`
	} `json:"label"`
	IssueType *struct {
		ID   *string `json:"id"`
		Name *string `json:"name"`
	} `json:"issueType"`
}

// IssueSuggestionRef names an object of a suggestion by identifier.
type IssueSuggestionRef struct {
	ID       *string `json:"id,omitempty"`
	Name     *string `json:"name,omitempty"`
	DataType *string `json:"data_type,omitempty"`
}

// IssuePendingSuggestion is a pending suggestion for an issue. Type is the GraphQL type of the suggestion;
// only the members of that type are set.
type IssuePendingSuggestion struct {
	Type           *string             `json:"type,omitempty"`
	ID             *string             `json:"id,omitempty"`
	Actor          *GraphQLAccount     `json:"actor,omitempty"`
	Assignee       *GraphQLAccount     `json:"assignee,omitempty"`
	CreatedAt      *string             `json:"created_at,omitempty"`
	UpdatedAt      *string             `json:"updated_at,omitempty"`
	Rationale      *string             `json:"rationale,omitempty"`
	StateReason    *string             `json:"state_reason,omitempty"`
	DuplicateOf    *GraphQLReference   `json:"duplicate_of,omitempty"`
	IssueField     *IssueSuggestionRef `json:"issue_field,omitempty"`
	SuggestedValue *string             `json:"suggested_value,omitempty"`
	Label          *IssueSuggestionRef `json:"label,omitempty"`
	IssueType      *IssueSuggestionRef `json:"issue_type,omitempty"`
}

func suggestionRef(id, name, dataType *string) *IssueSuggestionRef {
	if id == nil && name == nil && dataType == nil {
		return nil
	}
	return &IssueSuggestionRef{ID: gqlName(id), Name: gqlName(name), DataType: gqlName(dataType)}
}

func (r *pendingSuggestionRaw) project() *IssuePendingSuggestion {
	if r == nil {
		return nil
	}
	s := &IssuePendingSuggestion{Type: gqlName(r.Typename), ID: gqlName(r.ID), Actor: r.Actor.project(),
		Assignee: r.Assignee.project(), CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt),
		Rationale: gqlText(r.Rationale), StateReason: gqlName(r.StateReason), DuplicateOf: r.DuplicateOf.project(),
		SuggestedValue: gqlText(r.SuggestedValue)}
	if r.IssueField != nil {
		s.IssueField = suggestionRef(r.IssueField.ID, r.IssueField.Name, r.IssueField.DataType)
	}
	if r.Label != nil {
		s.Label = suggestionRef(r.Label.ID, r.Label.Name, nil)
	}
	if r.IssueType != nil {
		s.IssueType = suggestionRef(r.IssueType.ID, r.IssueType.Name, nil)
	}
	return s
}

// issueFieldValueSelection selects an issue field value of any type. The value members differ in type and
// nullability between the five value types, so each has its own response name.
const issueFieldValueSelection = `... on IssueFieldValueCommon{field{... on Node{id} ... on IssueFieldCommon{name dataType}}} ` +
	`... on IssueFieldTextValue{value} ... on IssueFieldDateValue{dateValue:value} ` +
	`... on IssueFieldNumberValue{numberValue:value} ... on IssueFieldSingleSelectValue{optionValue:value} ` +
	`... on IssueFieldMultiSelectValue{multiValue:value}`

// gqlConnectionRaw is the first page of a connection with its total.
type gqlConnectionRaw[T any] struct {
	TotalCount *int64 `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pageInfo"`
	Nodes []*T `json:"nodes"`
}

// connectionPage maps the first page of a connection, at most limit entries, with the total and whether the
// connection holds more entries than shown. project returns nil for an entry that says nothing.
func connectionPage[T, V any](c *gqlConnectionRaw[T], limit int, project func(*T) *V) ([]V, *int64, bool) {
	if c == nil {
		return nil, nil, false
	}
	var items []V
	shown := 0
	for _, node := range c.Nodes {
		if node == nil {
			continue
		}
		if shown == limit {
			return items, c.TotalCount, true
		}
		shown++
		if v := project(node); v != nil {
			items = append(items, *v)
		}
	}
	return items, c.TotalCount, connectionTruncated(c.TotalCount, c.PageInfo.HasNextPage, shown)
}

// projectActor maps an entry of an actor union and leaves out one that is no actor.
func projectActor(r *gqlAccountRaw) *GraphQLAccount {
	if r.Login == nil && r.ID == nil {
		return nil
	}
	return r.project()
}

type issueLinkedBranchRaw struct {
	ID  *string `json:"id"`
	Ref *struct {
		Name   *string `json:"name"`
		Target *struct {
			OID *string `json:"oid"`
		} `json:"target"`
	} `json:"ref"`
}

// IssueLinkedBranch is a branch linked to an issue.
type IssueLinkedBranch struct {
	ID        *string `json:"id,omitempty"`
	RefName   *string `json:"ref_name,omitempty"`
	TargetOID *string `json:"target_oid,omitempty"`
}

func (r *issueLinkedBranchRaw) project() *IssueLinkedBranch {
	b := &IssueLinkedBranch{ID: gqlName(r.ID)}
	if r.Ref != nil {
		b.RefName = gqlName(r.Ref.Name)
		if r.Ref.Target != nil {
			b.TargetOID = gqlName(r.Ref.Target.OID)
		}
	}
	return b
}

type issueProjectRaw struct {
	ID     *string `json:"id"`
	Number *int64  `json:"number"`
	Title  *string `json:"title"`
	URL    *string `json:"url"`
	Closed *bool   `json:"closed"`
}

// IssueProject is a project an issue belongs to.
type IssueProject struct {
	ID     *string `json:"id,omitempty"`
	Number *int64  `json:"number,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
	Closed *bool   `json:"closed,omitempty"`
}

func (r *issueProjectRaw) project() *IssueProject {
	return &IssueProject{ID: gqlName(r.ID), Number: r.Number, Title: gqlName(r.Title), URL: address(r.URL),
		Closed: r.Closed}
}

type issueFieldValueNodeRaw struct {
	Field *struct {
		ID       *string `json:"id"`
		Name     *string `json:"name"`
		DataType *string `json:"dataType"`
	} `json:"field"`
	Value       *string      `json:"value"`
	DateValue   *string      `json:"dateValue"`
	NumberValue *json.Number `json:"numberValue"`
	OptionValue *string      `json:"optionValue"`
	MultiValue  *string      `json:"multiValue"`
}

// GraphQLIssueFieldValue is the value of one issue field on an issue. A number stays a number with the
// digits GitHub sent; the other types are text, a multi-select value being the option names joined by commas.
type GraphQLIssueFieldValue struct {
	FieldID   *string `json:"field_id,omitempty"`
	FieldName *string `json:"field_name,omitempty"`
	DataType  *string `json:"data_type,omitempty"`
	Value     any     `json:"value,omitempty"`
}

func (r *issueFieldValueNodeRaw) project() *GraphQLIssueFieldValue {
	v := &GraphQLIssueFieldValue{}
	if r.Field != nil {
		v.FieldID, v.FieldName, v.DataType = gqlName(r.Field.ID), gqlName(r.Field.Name), gqlName(r.Field.DataType)
	}
	if r.NumberValue != nil {
		v.Value = *r.NumberValue
	}
	for _, text := range []*string{r.Value, r.DateValue, r.OptionValue, r.MultiValue} {
		if t := gqlText(text); t != nil {
			v.Value = *t
		}
	}
	if v.FieldID == nil && v.FieldName == nil && v.DataType == nil && v.Value == nil {
		return nil
	}
	return v
}

type issuePinnedCommentRaw struct {
	ID             *string        `json:"id"`
	DatabaseID     *int64         `json:"databaseId"`
	FullDatabaseID *gqlBigInt     `json:"fullDatabaseId"`
	PinnedAt       *string        `json:"pinnedAt"`
	PinnedBy       *gqlAccountRaw `json:"pinnedBy"`
	IssueComment   *struct {
		ID *string `json:"id"`
	} `json:"issueComment"`
}

// IssuePinnedComment is the comment pinned to an issue; the comment itself is read with
// github.comments.list.
type IssuePinnedComment struct {
	ID             *string         `json:"id,omitempty"`
	DatabaseID     *int64          `json:"database_id,omitempty"`
	FullDatabaseID *string         `json:"full_database_id,omitempty"`
	PinnedAt       *string         `json:"pinned_at,omitempty"`
	PinnedBy       *GraphQLAccount `json:"pinned_by,omitempty"`
	IssueCommentID *string         `json:"issue_comment_id,omitempty"`
}

func (r *issuePinnedCommentRaw) project() *IssuePinnedComment {
	if r == nil {
		return nil
	}
	p := &IssuePinnedComment{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, FullDatabaseID: r.FullDatabaseID.text(),
		PinnedAt: gqlName(r.PinnedAt), PinnedBy: r.PinnedBy.project()}
	if r.IssueComment != nil {
		p.IssueCommentID = gqlName(r.IssueComment.ID)
	}
	return p
}

type issueSubIssuesSummaryRaw struct {
	Completed        *int64 `json:"completed"`
	PercentCompleted *int64 `json:"percentCompleted"`
	Total            *int64 `json:"total"`
}

type issueDependenciesSummaryRaw struct {
	BlockedBy      *int64 `json:"blockedBy"`
	Blocking       *int64 `json:"blocking"`
	TotalBlockedBy *int64 `json:"totalBlockedBy"`
	TotalBlocking  *int64 `json:"totalBlocking"`
}

// issueNodeRaw is one Issue as issueListSelection asks for it.
type issueNodeRaw struct {
	Number                             int                                       `json:"number"`
	Title                              string                                    `json:"title"`
	State                              string                                    `json:"state"`
	UpdatedAt                          string                                    `json:"updatedAt"`
	Assignees                          *gqlConnectionRaw[gqlAccountRaw]          `json:"assignees"`
	Labels                             *gqlConnectionRaw[gqlLabelRaw]            `json:"labels"`
	IssueType                          *gqlIssueTypeRaw                          `json:"issueType"`
	ID                                 *string                                   `json:"id"`
	DatabaseID                         *int64                                    `json:"databaseId"`
	FullDatabaseID                     *gqlBigInt                                `json:"fullDatabaseId"`
	URL                                *string                                   `json:"url"`
	Author                             *gqlAccountRaw                            `json:"author"`
	AuthorAssociation                  *string                                   `json:"authorAssociation"`
	Body                               *string                                   `json:"body"`
	BodyResourcePath                   *string                                   `json:"bodyResourcePath"`
	BodyURL                            *string                                   `json:"bodyUrl"`
	Closed                             *bool                                     `json:"closed"`
	ClosedAt                           *string                                   `json:"closedAt"`
	CreatedAt                          *string                                   `json:"createdAt"`
	CreatedViaEmail                    *bool                                     `json:"createdViaEmail"`
	IncludesCreatedEdit                *bool                                     `json:"includesCreatedEdit"`
	LastEditedAt                       *string                                   `json:"lastEditedAt"`
	PublishedAt                        *string                                   `json:"publishedAt"`
	Editor                             *gqlAccountRaw                            `json:"editor"`
	Locked                             *bool                                     `json:"locked"`
	ActiveLockReason                   *string                                   `json:"activeLockReason"`
	StateReason                        *string                                   `json:"stateReason"`
	IsPinned                           *bool                                     `json:"isPinned"`
	IsReadByViewer                     *bool                                     `json:"isReadByViewer"`
	Milestone                          *gqlMilestoneRaw                          `json:"milestone"`
	Parent                             *gqlReferenceRaw                          `json:"parent"`
	DuplicateOf                        *gqlReferenceRaw                          `json:"duplicateOf"`
	PinnedIssueComment                 *issuePinnedCommentRaw                    `json:"pinnedIssueComment"`
	SubIssuesSummary                   *issueSubIssuesSummaryRaw                 `json:"subIssuesSummary"`
	IssueDependenciesSummary           *issueDependenciesSummaryRaw              `json:"issueDependenciesSummary"`
	ReactionGroups                     []*gqlReactionGroupRaw                    `json:"reactionGroups"`
	TrackedIssuesCount                 *int64                                    `json:"trackedIssuesCount"`
	ResourcePath                       *string                                   `json:"resourcePath"`
	AssignedActors                     *gqlConnectionRaw[gqlAccountRaw]          `json:"assignedActors"`
	ClosedByPullRequestsReferences     *gqlConnectionRaw[gqlReferenceRaw]        `json:"closedByPullRequestsReferences"`
	LinkedBranches                     *gqlConnectionRaw[issueLinkedBranchRaw]   `json:"linkedBranches"`
	Participants                       *gqlConnectionRaw[gqlAccountRaw]          `json:"participants"`
	ProjectsV2                         *gqlConnectionRaw[issueProjectRaw]        `json:"projectsV2"`
	RelatesTo                          *gqlConnectionRaw[gqlReferenceRaw]        `json:"relatesTo"`
	SuggestedActors                    *gqlConnectionRaw[gqlAccountRaw]          `json:"suggestedActors"`
	TrackedInIssues                    *gqlConnectionRaw[gqlReferenceRaw]        `json:"trackedInIssues"`
	TrackedIssues                      *gqlConnectionRaw[gqlReferenceRaw]        `json:"trackedIssues"`
	IssueFieldValues                   *gqlConnectionRaw[issueFieldValueNodeRaw] `json:"issueFieldValues"`
	PendingSuggestions                 []*pendingSuggestionRaw                   `json:"pendingSuggestions"`
	Comments                           *countRaw                                 `json:"comments"`
	SubIssues                          *countRaw                                 `json:"subIssues"`
	BlockedBy                          *countRaw                                 `json:"blockedBy"`
	Blocking                           *countRaw                                 `json:"blocking"`
	TimelineItems                      *countRaw                                 `json:"timelineItems"`
	UserContentEdits                   *countRaw                                 `json:"userContentEdits"`
	Reactions                          *countRaw                                 `json:"reactions"`
	ProjectItems                       *countRaw                                 `json:"projectItems"`
	ViewerCanAssign                    *bool                                     `json:"viewerCanAssign"`
	ViewerCanClose                     *bool                                     `json:"viewerCanClose"`
	ViewerCanDelete                    *bool                                     `json:"viewerCanDelete"`
	ViewerCanLabel                     *bool                                     `json:"viewerCanLabel"`
	ViewerCanReact                     *bool                                     `json:"viewerCanReact"`
	ViewerCanReopen                    *bool                                     `json:"viewerCanReopen"`
	ViewerCanSetFields                 *bool                                     `json:"viewerCanSetFields"`
	ViewerCanSetMilestone              *bool                                     `json:"viewerCanSetMilestone"`
	ViewerCanSubscribe                 *bool                                     `json:"viewerCanSubscribe"`
	ViewerCanType                      *bool                                     `json:"viewerCanType"`
	ViewerCanUpdate                    *bool                                     `json:"viewerCanUpdate"`
	ViewerCanUpdateMetadata            *bool                                     `json:"viewerCanUpdateMetadata"`
	ViewerCannotUpdateReasons          []*string                                 `json:"viewerCannotUpdateReasons"`
	ViewerDidAuthor                    *bool                                     `json:"viewerDidAuthor"`
	ViewerSubscription                 *string                                   `json:"viewerSubscription"`
	ViewerThreadSubscriptionFormAction *string                                   `json:"viewerThreadSubscriptionFormAction"`
	ViewerThreadSubscriptionStatus     *string                                   `json:"viewerThreadSubscriptionStatus"`
}

// IssueListWidth holds the members of GitHub's issue the list adds to its compact view.
type IssueListWidth struct {
	ID                                       *string                       `json:"id,omitempty"`
	DatabaseID                               *int64                        `json:"database_id,omitempty"`
	FullDatabaseID                           *string                       `json:"full_database_id,omitempty"`
	URL                                      *string                       `json:"url,omitempty"`
	AuthorDetail                             *GraphQLAccount               `json:"author_detail,omitempty"`
	AuthorAssociation                        *string                       `json:"author_association,omitempty"`
	Body                                     *string                       `json:"body,omitempty"`
	BodyTruncated                            bool                          `json:"body_truncated,omitempty"`
	Closed                                   *bool                         `json:"closed,omitempty"`
	ClosedAt                                 *string                       `json:"closed_at,omitempty"`
	CreatedAt                                *string                       `json:"created_at,omitempty"`
	CreatedViaEmail                          *bool                         `json:"created_via_email,omitempty"`
	IncludesCreatedEdit                      *bool                         `json:"includes_created_edit,omitempty"`
	LastEditedAt                             *string                       `json:"last_edited_at,omitempty"`
	PublishedAt                              *string                       `json:"published_at,omitempty"`
	Editor                                   *GraphQLAccount               `json:"editor,omitempty"`
	Locked                                   *bool                         `json:"locked,omitempty"`
	ActiveLockReason                         *string                       `json:"active_lock_reason,omitempty"`
	StateReason                              *string                       `json:"state_reason,omitempty"`
	IsPinned                                 *bool                         `json:"is_pinned,omitempty"`
	IsReadByViewer                           *bool                         `json:"is_read_by_viewer,omitempty"`
	MilestoneDetail                          *GraphQLMilestone             `json:"milestone_detail,omitempty"`
	IssueTypeDetail                          *GraphQLIssueType             `json:"issue_type_detail,omitempty"`
	Parent                                   *GraphQLReference             `json:"parent,omitempty"`
	DuplicateOf                              *GraphQLReference             `json:"duplicate_of,omitempty"`
	PinnedIssueComment                       *IssuePinnedComment           `json:"pinned_issue_comment,omitempty"`
	SubIssuesSummary                         *SubIssuesSummary             `json:"sub_issues_summary,omitempty"`
	IssueDependenciesSummary                 *RESTIssueDependenciesSummary `json:"issue_dependencies_summary,omitempty"`
	ReactionGroups                           []GraphQLReactionGroup        `json:"reaction_groups,omitempty"`
	TrackedIssuesCount                       *int64                        `json:"tracked_issues_count,omitempty"`
	LabelsDetails                            []GraphQLLabel                `json:"labels_details,omitempty"`
	LabelsTotalCount                         *int64                        `json:"labels_total_count,omitempty"`
	LabelsTruncated                          bool                          `json:"labels_truncated,omitempty"`
	AssigneesDetails                         []GraphQLAccount              `json:"assignees_details,omitempty"`
	AssigneesTotalCount                      *int64                        `json:"assignees_total_count,omitempty"`
	AssigneesTruncated                       bool                          `json:"assignees_truncated,omitempty"`
	Links                                    Links                         `json:"links,omitempty"`
	AssignedActors                           []GraphQLAccount              `json:"assigned_actors,omitempty"`
	AssignedActorsTotalCount                 *int64                        `json:"assigned_actors_total_count,omitempty"`
	AssignedActorsTruncated                  bool                          `json:"assigned_actors_truncated,omitempty"`
	ClosedByPullRequestsReferences           []GraphQLReference            `json:"closed_by_pull_requests_references,omitempty"`
	ClosedByPullRequestsReferencesTotalCount *int64                        `json:"closed_by_pull_requests_references_total_count,omitempty"`
	ClosedByPullRequestsReferencesTruncated  bool                          `json:"closed_by_pull_requests_references_truncated,omitempty"`
	LinkedBranches                           []IssueLinkedBranch           `json:"linked_branches,omitempty"`
	LinkedBranchesTotalCount                 *int64                        `json:"linked_branches_total_count,omitempty"`
	LinkedBranchesTruncated                  bool                          `json:"linked_branches_truncated,omitempty"`
	Participants                             []GraphQLAccount              `json:"participants,omitempty"`
	ParticipantsTotalCount                   *int64                        `json:"participants_total_count,omitempty"`
	ParticipantsTruncated                    bool                          `json:"participants_truncated,omitempty"`
	ProjectsV2                               []IssueProject                `json:"projects_v2,omitempty"`
	ProjectsV2TotalCount                     *int64                        `json:"projects_v2_total_count,omitempty"`
	ProjectsV2Truncated                      bool                          `json:"projects_v2_truncated,omitempty"`
	RelatesTo                                []GraphQLReference            `json:"relates_to,omitempty"`
	RelatesToTotalCount                      *int64                        `json:"relates_to_total_count,omitempty"`
	RelatesToTruncated                       bool                          `json:"relates_to_truncated,omitempty"`
	SuggestedActors                          []GraphQLAccount              `json:"suggested_actors,omitempty"`
	SuggestedActorsTotalCount                *int64                        `json:"suggested_actors_total_count,omitempty"`
	SuggestedActorsTruncated                 bool                          `json:"suggested_actors_truncated,omitempty"`
	TrackedInIssues                          []GraphQLReference            `json:"tracked_in_issues,omitempty"`
	TrackedInIssuesTotalCount                *int64                        `json:"tracked_in_issues_total_count,omitempty"`
	TrackedInIssuesTruncated                 bool                          `json:"tracked_in_issues_truncated,omitempty"`
	TrackedIssues                            []GraphQLReference            `json:"tracked_issues,omitempty"`
	TrackedIssuesTotalCount                  *int64                        `json:"tracked_issues_total_count,omitempty"`
	TrackedIssuesTruncated                   bool                          `json:"tracked_issues_truncated,omitempty"`
	IssueFieldValues                         []GraphQLIssueFieldValue      `json:"issue_field_values,omitempty"`
	IssueFieldValuesTotalCount               *int64                        `json:"issue_field_values_total_count,omitempty"`
	IssueFieldValuesTruncated                bool                          `json:"issue_field_values_truncated,omitempty"`
	PendingSuggestions                       []IssuePendingSuggestion      `json:"pending_suggestions,omitempty"`
	PendingSuggestionsTruncated              bool                          `json:"pending_suggestions_truncated,omitempty"`
	CommentsTotalCount                       *int64                        `json:"comments_total_count,omitempty"`
	SubIssuesTotalCount                      *int64                        `json:"sub_issues_total_count,omitempty"`
	BlockedByTotalCount                      *int64                        `json:"blocked_by_total_count,omitempty"`
	BlockingTotalCount                       *int64                        `json:"blocking_total_count,omitempty"`
	TimelineItemsTotalCount                  *int64                        `json:"timeline_items_total_count,omitempty"`
	UserContentEditsTotalCount               *int64                        `json:"user_content_edits_total_count,omitempty"`
	ReactionsTotalCount                      *int64                        `json:"reactions_total_count,omitempty"`
	ProjectItemsTotalCount                   *int64                        `json:"project_items_total_count,omitempty"`
	ViewerCanAssign                          *bool                         `json:"viewer_can_assign,omitempty"`
	ViewerCanClose                           *bool                         `json:"viewer_can_close,omitempty"`
	ViewerCanDelete                          *bool                         `json:"viewer_can_delete,omitempty"`
	ViewerCanLabel                           *bool                         `json:"viewer_can_label,omitempty"`
	ViewerCanReact                           *bool                         `json:"viewer_can_react,omitempty"`
	ViewerCanReopen                          *bool                         `json:"viewer_can_reopen,omitempty"`
	ViewerCanSetFields                       *bool                         `json:"viewer_can_set_fields,omitempty"`
	ViewerCanSetMilestone                    *bool                         `json:"viewer_can_set_milestone,omitempty"`
	ViewerCanSubscribe                       *bool                         `json:"viewer_can_subscribe,omitempty"`
	ViewerCanType                            *bool                         `json:"viewer_can_type,omitempty"`
	ViewerCanUpdate                          *bool                         `json:"viewer_can_update,omitempty"`
	ViewerCanUpdateMetadata                  *bool                         `json:"viewer_can_update_metadata,omitempty"`
	ViewerCannotUpdateReasons                []string                      `json:"viewer_cannot_update_reasons,omitempty"`
	ViewerDidAuthor                          *bool                         `json:"viewer_did_author,omitempty"`
	ViewerSubscription                       *string                       `json:"viewer_subscription,omitempty"`
	ViewerThreadSubscriptionFormAction       *string                       `json:"viewer_thread_subscription_form_action,omitempty"`
	ViewerThreadSubscriptionStatus           *string                       `json:"viewer_thread_subscription_status,omitempty"`
}

var issueListLinkKeys = []string{"resource_path", "body_resource_path", "body_url"}

// width maps the members beyond the compact issue. A reaction group nobody gave says nothing and stays out.
func (n *issueNodeRaw) width() IssueListWidth {
	body, cut := cleanText(filled(n.Body), issueListBodyLimit, true)
	w := IssueListWidth{ID: gqlName(n.ID), DatabaseID: n.DatabaseID, FullDatabaseID: n.FullDatabaseID.text(),
		URL: address(n.URL), AuthorDetail: n.Author.project(), AuthorAssociation: gqlName(n.AuthorAssociation),
		Body: body, BodyTruncated: cut, Closed: n.Closed, ClosedAt: gqlName(n.ClosedAt),
		CreatedAt: gqlName(n.CreatedAt), CreatedViaEmail: n.CreatedViaEmail,
		IncludesCreatedEdit: n.IncludesCreatedEdit, LastEditedAt: gqlName(n.LastEditedAt),
		PublishedAt: gqlName(n.PublishedAt), Editor: n.Editor.project(), Locked: n.Locked,
		ActiveLockReason: gqlName(n.ActiveLockReason), StateReason: gqlName(n.StateReason), IsPinned: n.IsPinned,
		IsReadByViewer: n.IsReadByViewer, MilestoneDetail: n.Milestone.project(),
		IssueTypeDetail: n.IssueType.project(), Parent: n.Parent.project(), DuplicateOf: n.DuplicateOf.project(),
		PinnedIssueComment: n.PinnedIssueComment.project(), ReactionGroups: projectGivenReactionGroups(n.ReactionGroups),
		TrackedIssuesCount: n.TrackedIssuesCount,
		Links:              gqlLinks(issueListLinkKeys, n.ResourcePath, n.BodyResourcePath, n.BodyURL),
		CommentsTotalCount: n.Comments.total(), SubIssuesTotalCount: n.SubIssues.total(),
		BlockedByTotalCount: n.BlockedBy.total(), BlockingTotalCount: n.Blocking.total(),
		TimelineItemsTotalCount: n.TimelineItems.total(), UserContentEditsTotalCount: n.UserContentEdits.total(),
		ReactionsTotalCount: n.Reactions.total(), ProjectItemsTotalCount: n.ProjectItems.total(),
		ViewerCanAssign: n.ViewerCanAssign, ViewerCanClose: n.ViewerCanClose, ViewerCanDelete: n.ViewerCanDelete,
		ViewerCanLabel: n.ViewerCanLabel, ViewerCanReact: n.ViewerCanReact, ViewerCanReopen: n.ViewerCanReopen,
		ViewerCanSetFields: n.ViewerCanSetFields, ViewerCanSetMilestone: n.ViewerCanSetMilestone,
		ViewerCanSubscribe: n.ViewerCanSubscribe, ViewerCanType: n.ViewerCanType, ViewerCanUpdate: n.ViewerCanUpdate,
		ViewerCanUpdateMetadata: n.ViewerCanUpdateMetadata, ViewerCannotUpdateReasons: gqlNames(n.ViewerCannotUpdateReasons),
		ViewerDidAuthor: n.ViewerDidAuthor, ViewerSubscription: gqlName(n.ViewerSubscription),
		ViewerThreadSubscriptionFormAction: gqlName(n.ViewerThreadSubscriptionFormAction),
		ViewerThreadSubscriptionStatus:     gqlName(n.ViewerThreadSubscriptionStatus)}
	if s := n.SubIssuesSummary; s != nil && s.Completed != nil && s.PercentCompleted != nil && s.Total != nil {
		w.SubIssuesSummary = &SubIssuesSummary{Total: int(*s.Total), Completed: int(*s.Completed),
			PercentCompleted: int(*s.PercentCompleted)}
	}
	if d := n.IssueDependenciesSummary; d != nil && (d.BlockedBy != nil || d.Blocking != nil ||
		d.TotalBlockedBy != nil || d.TotalBlocking != nil) {
		w.IssueDependenciesSummary = &RESTIssueDependenciesSummary{BlockedBy: d.BlockedBy, Blocking: d.Blocking,
			TotalBlockedBy: d.TotalBlockedBy, TotalBlocking: d.TotalBlocking}
	}
	w.LabelsDetails, w.LabelsTotalCount, w.LabelsTruncated =
		connectionPage(n.Labels, issueLabelsFirst, (*gqlLabelRaw).project)
	w.AssigneesDetails, w.AssigneesTotalCount, w.AssigneesTruncated =
		connectionPage(n.Assignees, issueAssigneesFirst, projectActor)
	w.AssignedActors, w.AssignedActorsTotalCount, w.AssignedActorsTruncated =
		connectionPage(n.AssignedActors, issueConnectionFirst, projectActor)
	w.ClosedByPullRequestsReferences, w.ClosedByPullRequestsReferencesTotalCount,
		w.ClosedByPullRequestsReferencesTruncated =
		connectionPage(n.ClosedByPullRequestsReferences, issueConnectionFirst, (*gqlReferenceRaw).project)
	w.LinkedBranches, w.LinkedBranchesTotalCount, w.LinkedBranchesTruncated =
		connectionPage(n.LinkedBranches, issueConnectionFirst, (*issueLinkedBranchRaw).project)
	w.Participants, w.ParticipantsTotalCount, w.ParticipantsTruncated =
		connectionPage(n.Participants, issueConnectionFirst, projectActor)
	w.ProjectsV2, w.ProjectsV2TotalCount, w.ProjectsV2Truncated =
		connectionPage(n.ProjectsV2, issueConnectionFirst, (*issueProjectRaw).project)
	w.RelatesTo, w.RelatesToTotalCount, w.RelatesToTruncated =
		connectionPage(n.RelatesTo, issueConnectionFirst, (*gqlReferenceRaw).project)
	w.SuggestedActors, w.SuggestedActorsTotalCount, w.SuggestedActorsTruncated =
		connectionPage(n.SuggestedActors, issueConnectionFirst, projectActor)
	w.TrackedInIssues, w.TrackedInIssuesTotalCount, w.TrackedInIssuesTruncated =
		connectionPage(n.TrackedInIssues, issueConnectionFirst, (*gqlReferenceRaw).project)
	w.TrackedIssues, w.TrackedIssuesTotalCount, w.TrackedIssuesTruncated =
		connectionPage(n.TrackedIssues, issueConnectionFirst, (*gqlReferenceRaw).project)
	w.IssueFieldValues, w.IssueFieldValuesTotalCount, w.IssueFieldValuesTruncated =
		connectionPage(n.IssueFieldValues, issueConnectionFirst, (*issueFieldValueNodeRaw).project)
	shown := 0
	for _, suggestion := range n.PendingSuggestions {
		if suggestion == nil {
			continue
		}
		if shown == maxPendingSuggestions {
			w.PendingSuggestionsTruncated = true
			break
		}
		shown++
		if v := suggestion.project(); v != nil {
			w.PendingSuggestions = append(w.PendingSuggestions, *v)
		}
	}
	return w
}

// ---- Filters and order of the list

// maxIssueFieldFilters bounds the issue field filters of one request; maxIssueFieldFilterValues bounds each
// list of option identifiers or names, and maxIssueFilterText bounds a name or text.
const (
	maxIssueFieldFilters      = 10
	maxIssueFieldFilterValues = 10
	maxIssueFilterText        = 256
)

var (
	nodeIDExpression          = regexp.MustCompile(nodeIDPattern)
	milestoneNumberExpression = regexp.MustCompile(`^([0-9]{1,10}|\*|none)$`)
	issueOrderFields          = []string{"CREATED_AT", "UPDATED_AT", "COMMENTS"}
	issueOrderDirections      = []string{"ASC", "DESC"}
)

// IssueFieldFilter is one issue field value filter: the field by identifier or name and exactly one value.
type IssueFieldFilter struct {
	FieldID                 string   `json:"field_id,omitempty"`
	FieldName               string   `json:"field_name,omitempty"`
	DateValue               string   `json:"date_value,omitempty"`
	NumberValue             *float64 `json:"number_value,omitempty"`
	SingleSelectOptionID    string   `json:"single_select_option_id,omitempty"`
	SingleSelectOptionValue string   `json:"single_select_option_value,omitempty"`
	MultiSelectOptionIDs    []string `json:"multi_select_option_ids,omitempty"`
	MultiSelectOptionValues []string `json:"multi_select_option_values,omitempty"`
	TextValue               string   `json:"text_value,omitempty"`
}

// plainText reports whether value is a non-empty text of at most limit characters without control characters.
func plainText(value string, limit int) bool {
	if value == "" || utf8.RuneCountInString(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func plainTexts(name string, values []string) error {
	if len(values) > maxIssueFieldFilterValues {
		return invalidRequest(fmt.Sprintf("%s accepts at most %d values", name, maxIssueFieldFilterValues))
	}
	for _, value := range values {
		if !plainText(value, maxIssueFilterText) {
			return invalidRequest(fmt.Sprintf("%s must hold texts of 1 to %d characters without control characters",
				name, maxIssueFilterText))
		}
	}
	return nil
}

func nodeIDs(name string, values []string) error {
	if len(values) > maxIssueFieldFilterValues {
		return invalidRequest(fmt.Sprintf("%s accepts at most %d values", name, maxIssueFieldFilterValues))
	}
	for _, value := range values {
		if !nodeIDExpression.MatchString(value) {
			return invalidRequest(name + " must hold GraphQL node ids")
		}
	}
	return nil
}

// check applies the bounds of one issue field filter.
func (f IssueFieldFilter) check() error {
	if (f.FieldID == "") == (f.FieldName == "") {
		return invalidRequest("each issue_field_values entry needs exactly one of field_id or field_name")
	}
	if f.FieldID != "" && !nodeIDExpression.MatchString(f.FieldID) {
		return invalidRequest("issue_field_values.field_id must be a GraphQL node id")
	}
	if f.FieldName != "" && !plainText(f.FieldName, maxIssueFilterText) {
		return invalidRequest("issue_field_values.field_name must hold 1 to 256 characters without control characters")
	}
	values := 0
	if f.DateValue != "" {
		if _, err := time.Parse(time.DateOnly, f.DateValue); err != nil {
			return invalidRequest("issue_field_values.date_value must be a date as YYYY-MM-DD")
		}
		values++
	}
	if f.NumberValue != nil {
		values++
	}
	if f.SingleSelectOptionID != "" {
		if !nodeIDExpression.MatchString(f.SingleSelectOptionID) {
			return invalidRequest("issue_field_values.single_select_option_id must be a GraphQL node id")
		}
		values++
	}
	if f.SingleSelectOptionValue != "" {
		if !plainText(f.SingleSelectOptionValue, maxIssueFilterText) {
			return invalidRequest("issue_field_values.single_select_option_value must hold 1 to 256 characters " +
				"without control characters")
		}
		values++
	}
	if len(f.MultiSelectOptionIDs) > 0 {
		if err := nodeIDs("issue_field_values.multi_select_option_ids", f.MultiSelectOptionIDs); err != nil {
			return err
		}
		values++
	}
	if len(f.MultiSelectOptionValues) > 0 {
		if err := plainTexts("issue_field_values.multi_select_option_values", f.MultiSelectOptionValues); err != nil {
			return err
		}
		values++
	}
	if f.TextValue != "" {
		if !plainText(f.TextValue, maxIssueFilterText) {
			return invalidRequest("issue_field_values.text_value must hold 1 to 256 characters without control characters")
		}
		values++
	}
	if values != 1 {
		return invalidRequest("each issue_field_values entry needs exactly one of date_value, number_value, " +
			"single_select_option_id, single_select_option_value, multi_select_option_ids, " +
			"multi_select_option_values, or text_value")
	}
	return nil
}

// input is the GraphQL IssueFieldValueFilter of the filter.
func (f IssueFieldFilter) input() map[string]any {
	input := map[string]any{}
	set := func(key, value string) {
		if value != "" {
			input[key] = value
		}
	}
	set("fieldId", f.FieldID)
	set("fieldName", f.FieldName)
	set("dateValue", f.DateValue)
	set("singleSelectOptionId", f.SingleSelectOptionID)
	set("singleSelectOptionValue", f.SingleSelectOptionValue)
	set("textValue", f.TextValue)
	if f.NumberValue != nil {
		input["numberValue"] = *f.NumberValue
	}
	if len(f.MultiSelectOptionIDs) > 0 {
		input["multiSelectOptionIds"] = f.MultiSelectOptionIDs
	}
	if len(f.MultiSelectOptionValues) > 0 {
		input["multiSelectOptionValues"] = f.MultiSelectOptionValues
	}
	return input
}

// checkIssueFilters applies the bounds of the filters and the order beyond state, labels, and assignee, and
// sets the default order.
func (o *IssueListOptions) checkIssueFilters() error {
	for name, login := range map[string]string{"created_by": o.CreatedBy, "mentioned": o.Mentioned} {
		if login != "" && !validLogin(login) {
			return invalidRequest(name + " must be a GitHub login")
		}
	}
	if o.Milestone != "" && !plainText(o.Milestone, maxIssueFilterText) {
		return invalidRequest("milestone must hold 1 to 256 characters without control characters, or be * or none")
	}
	if o.MilestoneNumber != "" && !milestoneNumberExpression.MatchString(o.MilestoneNumber) {
		return invalidRequest("milestone_number must be a milestone number, *, or none")
	}
	if o.Since != "" {
		if len(o.Since) > 64 {
			return invalidRequest("since must be a date and time as RFC 3339")
		}
		if _, err := time.Parse(time.RFC3339, o.Since); err != nil {
			return invalidRequest("since must be a date and time as RFC 3339")
		}
	}
	if o.Type != "" && !plainText(o.Type, maxTypeLength) {
		return invalidRequest(fmt.Sprintf("type must hold 1 to %d characters without control characters, or be * or none",
			maxTypeLength))
	}
	if len(o.IssueFieldValues) > maxIssueFieldFilters {
		return invalidRequest(fmt.Sprintf("issue_field_values accepts at most %d entries", maxIssueFieldFilters))
	}
	for _, filter := range o.IssueFieldValues {
		if err := filter.check(); err != nil {
			return err
		}
	}
	switch {
	case o.OrderBy == "":
		o.OrderBy = "CREATED_AT"
	case !containsExact(issueOrderFields, o.OrderBy):
		return invalidRequest("order_by must be CREATED_AT, UPDATED_AT, or COMMENTS")
	}
	switch {
	case o.Direction == "":
		o.Direction = "DESC"
	case !containsExact(issueOrderDirections, o.Direction):
		return invalidRequest("direction must be ASC or DESC")
	}
	return nil
}

func containsExact(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// bindingParts returns the filters and the order beyond state, labels, and assignee that bind a cursor, or
// nil when none is set and the order is the default, so a cursor issued before they existed stays valid.
func (o *IssueListOptions) bindingParts() []any {
	defaultOrder := o.OrderBy == "CREATED_AT" && o.Direction == "DESC"
	if o.CreatedBy == "" && o.Mentioned == "" && o.Milestone == "" && o.MilestoneNumber == "" && o.Since == "" &&
		o.Type == "" && len(o.IssueFieldValues) == 0 && !o.ViewerSubscribed && defaultOrder {
		return nil
	}
	return []any{strings.ToLower(o.CreatedBy), strings.ToLower(o.Mentioned), o.Milestone, o.MilestoneNumber,
		o.Since, o.Type, o.IssueFieldValues, o.ViewerSubscribed, o.OrderBy, o.Direction}
}

// applyIssueFilters adds the filters beyond state, labels, and assignee to the GraphQL filter. A milestone
// of none is an explicit null, which GitHub reads as no milestone.
func (o *IssueListOptions) applyIssueFilters(filter map[string]any) {
	if o.CreatedBy != "" {
		filter["createdBy"] = o.CreatedBy
	}
	if o.Mentioned != "" {
		filter["mentioned"] = o.Mentioned
	}
	if o.Milestone == "none" {
		filter["milestone"] = nil
	} else if o.Milestone != "" {
		filter["milestone"] = o.Milestone
	}
	if o.MilestoneNumber == "none" {
		filter["milestoneNumber"] = nil
	} else if o.MilestoneNumber != "" {
		filter["milestoneNumber"] = o.MilestoneNumber
	}
	if o.Since != "" {
		filter["since"] = o.Since
	}
	if o.Type != "" {
		filter["type"] = o.Type
	}
	if len(o.IssueFieldValues) > 0 {
		values := make([]map[string]any, len(o.IssueFieldValues))
		for i, f := range o.IssueFieldValues {
			values[i] = f.input()
		}
		filter["issueFieldValues"] = values
	}
	if o.ViewerSubscribed {
		filter["viewerSubscribed"] = true
	}
}

// ---- Schema and descriptions

const issueFilterTextSchema = `{"type":"string","minLength":1,"maxLength":256,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`

const issueFieldFilterSchema = `{"type":"object","properties":{"field_id":` + fieldIDSchema + `,` +
	`"field_name":` + issueFilterTextSchema + `,"date_value":` + dateOnlySchema + `,"number_value":{"type":"number"},` +
	`"single_select_option_id":` + fieldIDSchema + `,"single_select_option_value":` + issueFilterTextSchema + `,` +
	`"multi_select_option_ids":{"type":"array","minItems":1,"maxItems":10,"items":` + fieldIDSchema + `},` +
	`"multi_select_option_values":{"type":"array","minItems":1,"maxItems":10,"items":` + issueFilterTextSchema + `},` +
	`"text_value":` + issueFilterTextSchema + `},"additionalProperties":false}`

// issueListInputProperties are the schema members of the list's filters and order beyond state, labels, and
// assignee.
const issueListInputProperties = `"created_by":{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"},` +
	`"mentioned":{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"},` +
	`"milestone":` + issueFilterTextSchema + `,"milestone_number":{"type":"string","maxLength":10,"pattern":"` +
	`^([0-9]{1,10}|\\*|none)$"},"since":{"type":"string","maxLength":64},` +
	`"type":{"type":"string","minLength":1,"maxLength":100,"pattern":"^[^\\x00-\\x1f\\x7f]+$"},` +
	`"issue_field_values":{"type":"array","minItems":1,"maxItems":10,"items":` + issueFieldFilterSchema + `},` +
	`"viewer_subscribed":{"type":"boolean"},"project_details":{"type":"boolean"},` +
	`"order_by":{"type":"string","enum":["CREATED_AT","UPDATED_AT","COMMENTS"]},` +
	`"direction":{"type":"string","enum":["ASC","DESC"]}`

var issueListInputArguments = []capability.Argument{
	{Name: "created_by", Description: "Return only issues created by this login"},
	{Name: "mentioned", Description: "Return only issues that mention this login"},
	{Name: "milestone", Description: "Return only issues of the milestone with this name, * for any milestone, " +
		"or none for no milestone"},
	{Name: "milestone_number", Description: "Return only issues of the milestone with this number, * for any " +
		"milestone, or none for no milestone"},
	{Name: "since", Description: "Return only issues updated at or after this time, as RFC 3339"},
	{Name: "type", Description: "Return only issues of the issue type with this name, * for any type, or none " +
		"for no type"},
	{Name: "issue_field_values", Description: "Return only issues for which every entry matches, at most 10: " +
		"the field by field_id (a node id as github.issue-fields.list reports it) or field_name, and exactly one " +
		"value from date_value, number_value, single_select_option_id, single_select_option_value, " +
		"multi_select_option_ids, multi_select_option_values, or text_value; lists hold at most 10 entries, " +
		"texts at most 256 characters"},
	{Name: "project_details", Description: "true to read the projects of each issue and its project item count, " +
		"which needs project read access of the token (classic: read:project; fine-grained: Projects read); " +
		"false (default) leaves projects_v2 and project_items_total_count out"},
	{Name: "viewer_subscribed", Description: "true returns only issues the authenticated user subscribed to"},
	{Name: "order_by", Description: "CREATED_AT, UPDATED_AT, or COMMENTS; CREATED_AT when omitted"},
	{Name: "direction", Description: "ASC or DESC; DESC when omitted"},
}

// issueListWidthProperties are the schema members of an issue of the list beyond the compact view.
var issueListWidthProperties = `"author":{"type":"string"},"milestone":{"type":"string"},` +
	`"id":{"type":"string"},"database_id":{"type":"integer"},"full_database_id":{"type":"string"},` +
	`"url":{"type":"string"},` + detailProperty("author", gqlAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"body":{"type":"string"},"body_truncated":{"type":"boolean"},` +
	`"closed":{"type":"boolean"},"closed_at":{"type":"string"},"created_at":{"type":"string"},` +
	`"created_via_email":{"type":"boolean"},"includes_created_edit":{"type":"boolean"},` +
	`"last_edited_at":{"type":"string"},"published_at":{"type":"string"},"editor":` + gqlAccountSchema + `,` +
	`"locked":{"type":"boolean"},"active_lock_reason":{"type":"string"},"state_reason":{"type":"string"},` +
	`"is_pinned":{"type":"boolean"},"is_read_by_viewer":{"type":"boolean"},` +
	detailProperty("milestone", gqlMilestoneSchema, false) + `,` +
	detailProperty("issue_type", gqlIssueTypeSchema, false) + `,` +
	`"parent":` + gqlReferenceSchema + `,"duplicate_of":` + gqlReferenceSchema + `,` +
	`"pinned_issue_comment":` + issuePinnedCommentSchema + `,"sub_issues_summary":` + subIssuesSummaryOutput + `,` +
	`"issue_dependencies_summary":` + issueDependenciesSummarySchema + `,` +
	`"reaction_groups":{"type":"array","items":` + gqlReactionGroupSchema + `},` +
	`"tracked_issues_count":{"type":"integer"},` +
	issueConnectionProperties("labels", true, gqlLabelSchema) + `,` +
	issueConnectionProperties("assignees", true, gqlAccountSchema) + `,` +
	`"links":` + linksSchema(issueListLinkKeys) + `,` +
	issueConnectionProperties("assigned_actors", false, gqlAccountSchema) + `,` +
	issueConnectionProperties("closed_by_pull_requests_references", false, gqlReferenceSchema) + `,` +
	issueConnectionProperties("linked_branches", false, issueLinkedBranchSchema) + `,` +
	issueConnectionProperties("participants", false, gqlAccountSchema) + `,` +
	issueConnectionProperties("projects_v2", false, issueProjectSchema) + `,` +
	issueConnectionProperties("relates_to", false, gqlReferenceSchema) + `,` +
	issueConnectionProperties("suggested_actors", false, gqlAccountSchema) + `,` +
	issueConnectionProperties("tracked_in_issues", false, gqlReferenceSchema) + `,` +
	issueConnectionProperties("tracked_issues", false, gqlReferenceSchema) + `,` +
	issueConnectionProperties("issue_field_values", false, issueFieldValueSchemaOutput) + `,` +
	`"pending_suggestions":{"type":"array","items":` + issuePendingSuggestionSchema + `},` +
	`"pending_suggestions_truncated":{"type":"boolean"},` +
	`"comments_total_count":{"type":"integer"},"sub_issues_total_count":{"type":"integer"},` +
	`"blocked_by_total_count":{"type":"integer"},"blocking_total_count":{"type":"integer"},` +
	`"timeline_items_total_count":{"type":"integer"},"user_content_edits_total_count":{"type":"integer"},` +
	`"reactions_total_count":{"type":"integer"},"project_items_total_count":{"type":"integer"},` +
	`"viewer_can_assign":{"type":"boolean"},"viewer_can_close":{"type":"boolean"},` +
	`"viewer_can_delete":{"type":"boolean"},"viewer_can_label":{"type":"boolean"},` +
	`"viewer_can_react":{"type":"boolean"},"viewer_can_reopen":{"type":"boolean"},` +
	`"viewer_can_set_fields":{"type":"boolean"},"viewer_can_set_milestone":{"type":"boolean"},` +
	`"viewer_can_subscribe":{"type":"boolean"},"viewer_can_type":{"type":"boolean"},` +
	`"viewer_can_update":{"type":"boolean"},"viewer_can_update_metadata":{"type":"boolean"},` +
	`"viewer_cannot_update_reasons":{"type":"array","items":{"type":"string"}},` +
	`"viewer_did_author":{"type":"boolean"},"viewer_subscription":{"type":"string"},` +
	`"viewer_thread_subscription_form_action":{"type":"string"},` +
	`"viewer_thread_subscription_status":{"type":"string"}`

var issuePinnedCommentSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
	`"full_database_id":{"type":"string"},"pinned_at":{"type":"string"},"pinned_by":` + gqlAccountSchema + `,` +
	`"issue_comment_id":{"type":"string"}`)

var issueSuggestionRefSchema = objectSchema(`"id":{"type":"string"},"name":{"type":"string"},"data_type":{"type":"string"}`)

var issuePendingSuggestionSchema = objectSchema(`"type":{"type":"string"},"id":{"type":"string"},` +
	`"actor":` + gqlAccountSchema + `,"assignee":` + gqlAccountSchema + `,"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"rationale":{"type":"string"},"state_reason":{"type":"string"},` +
	`"duplicate_of":` + gqlReferenceSchema + `,"issue_field":` + issueSuggestionRefSchema + `,` +
	`"suggested_value":{"type":"string"},"label":` + issueSuggestionRefSchema + `,` +
	`"issue_type":` + issueSuggestionRefSchema)

var issueLinkedBranchSchema = objectSchema(`"id":{"type":"string"},"ref_name":{"type":"string"},` +
	`"target_oid":{"type":"string"}`)

var issueProjectSchema = objectSchema(`"id":{"type":"string"},"number":{"type":"integer"},` +
	`"title":{"type":"string"},"url":{"type":"string"},"closed":{"type":"boolean"}`)

var issueFieldValueSchemaOutput = objectSchema(`"field_id":{"type":"string"},"field_name":{"type":"string"},` +
	`"data_type":{"type":"string"},"value":{"anyOf":[{"type":"string"},{"type":"number"}]}`)

// issueConnectionProperties are the schema members of a connection read with its first page: the entries
// (named name_details for a list behind a flat field, as detailName says), the total, and the marker.
func issueConnectionProperties(name string, flat bool, entry string) string {
	list := name
	if flat {
		list = detailName(name, true)
	}
	return `"` + list + `":{"type":"array","items":` + entry + `},"` + name + `_total_count":{"type":"integer"},"` +
		name + `_truncated":{"type":"boolean"}`
}

func issueConnectionFields(name, what string, first int) []capability.Field {
	list := name
	for _, flat := range []string{"labels", "assignees"} {
		if name == flat {
			list = detailName(name, true)
		}
	}
	return []capability.Field{
		{Name: list, Description: what + ", the first " + strconv.Itoa(first) + "; untrusted data"},
		{Name: name + "_total_count", Description: "Number of all entries of " + name},
		{Name: name + "_truncated", Description: "True when " + name + " holds more entries than shown"},
	}
}

// issueListWidthFields describe the members of an issue of the list beyond the compact view.
var issueListWidthFields = slices.Concat(
	[]capability.Field{
		{Name: "author", Description: "Login of the author, untrusted data"},
		{Name: "milestone", Description: "Title of the milestone, untrusted data; absent when none is set"},
		{Name: "id", Description: "GraphQL node id of the issue"},
		{Name: "database_id", Description: "Numeric identifier of the issue"},
		{Name: "full_database_id", Description: "Numeric identifier of the issue as digits, for values beyond 53 bits"},
		{Name: "url", Description: "Web address of the issue, untrusted data, never fetched"},
		detailField("author", "Account", false),
		{Name: "author_association", Description: "Relation of the author to the repository such as OWNER, MEMBER, or NONE"},
		{Name: "body", Description: "Body of the issue, cut at 1000 characters (body_truncated says so), untrusted data; " +
			"github.issues.get reads it in full"},
		{Name: "body_truncated", Description: "True when the body was cut"},
		{Name: "closed", Description: "True when the issue is closed"},
		{Name: "closed_at", Description: "Time the issue was closed"},
		{Name: "created_at", Description: "Creation time"},
		{Name: "created_via_email", Description: "True when the issue was created by an email"},
		{Name: "includes_created_edit", Description: "True when the edit history includes an edit with the creation data"},
		{Name: "last_edited_at", Description: "Time of the last edit"},
		{Name: "published_at", Description: "Time the issue was published"},
		{Name: "editor", Description: "Account that last edited the issue: " + gqlAccountMembers},
		{Name: "locked", Description: "True when the conversation is locked"},
		{Name: "active_lock_reason", Description: "Why the conversation is locked such as RESOLVED or TOO_HEATED"},
		{Name: "state_reason", Description: "Why the issue has its state such as COMPLETED, NOT_PLANNED, or DUPLICATE"},
		{Name: "is_pinned", Description: "True when the issue is pinned to the repository issues list"},
		{Name: "is_read_by_viewer", Description: "True when the authenticated user read the issue"},
		detailField("milestone", "Milestone: "+gqlMilestoneMembers+";", false),
		detailField("issue_type", "Issue type: "+gqlIssueTypeMembers+";", false),
		{Name: "parent", Description: "Parent issue: " + gqlReferenceMembers},
		{Name: "duplicate_of", Description: "Issue this issue was marked a duplicate of: " + gqlReferenceMembers},
		{Name: "pinned_issue_comment", Description: "Comment pinned to the issue: id, database_id, " +
			"full_database_id, pinned_at, pinned_by (an account), and issue_comment_id; github.comments.list reads the comment"},
		{Name: "sub_issues_summary", Description: "total, completed, and percent_completed sub-issues of the issue"},
		{Name: "issue_dependencies_summary", Description: "blocked_by, blocking, total_blocked_by, and " +
			"total_blocking dependency counts of the issue"},
		{Name: "reaction_groups", Description: "Reactions by kind, only kinds somebody gave: " + gqlReactionGroupMembers},
		{Name: "tracked_issues_count", Description: "Number of issues tracked in the issue's task list"},
	},
	[]capability.Field{
		{Name: detailName("labels", true), Description: "Labels, the first 20: " + gqlLabelMembers},
		{Name: "labels_total_count", Description: "Number of all labels of the issue"},
		{Name: "labels_truncated", Description: "True when the issue carries more labels than shown"},
		{Name: detailName("assignees", true), Description: "Assignee accounts, the first 10: " + gqlAccountMembers},
		{Name: "assignees_total_count", Description: "Number of all assignees of the issue"},
		{Name: "assignees_truncated", Description: "True when the issue has more assignees than shown"},
		{Name: "links", Description: "resource_path, body_resource_path, and body_url of the issue, untrusted data, " +
			"never fetched"},
	},
	issueConnectionFields("assigned_actors", "Accounts assigned to the issue, including bots", 10),
	issueConnectionFields("closed_by_pull_requests_references", "Open pull requests that close the issue", 10),
	issueConnectionFields("linked_branches", "Branches linked to the issue: id, ref_name, and target_oid", 10),
	issueConnectionFields("participants", "Users taking part in the conversation", 10),
	issueConnectionFields("projects_v2", "Projects the issue belongs to: id, number, title, url, and closed", 10),
	issueConnectionFields("relates_to", "Issues related to the issue", 10),
	issueConnectionFields("suggested_actors", "Accounts suggested for assignment", 10),
	issueConnectionFields("tracked_in_issues", "Issues that track the issue", 10),
	issueConnectionFields("tracked_issues", "Issues tracked inside the issue", 10),
	issueConnectionFields("issue_field_values", "Issue field values: field_id, field_name, data_type, and value "+
		"(a number, or text; a multi-select value lists the option names joined by commas)", 10),
	[]capability.Field{
		{Name: "pending_suggestions", Description: "Pending suggestions for the issue, the first 10, untrusted data: " +
			"type (PendingAssigneeSuggestion, PendingCloseSuggestion, PendingFieldSuggestion, PendingLabelSuggestion, " +
			"or PendingTypeSuggestion), id, actor and, for an assignee suggestion, assignee (accounts), created_at, " +
			"updated_at, rationale, and the members of the type: state_reason and duplicate_of (id, number, repository) " +
			"for a close suggestion, issue_field (id, name, data_type) and suggested_value for a field suggestion, " +
			"label (id, name), or issue_type (id, name)"},
		{Name: "pending_suggestions_truncated", Description: "True when the issue holds more pending suggestions than shown"},
		{Name: "comments_total_count", Description: "Number of comments; github.comments.list reads them"},
		{Name: "sub_issues_total_count", Description: "Number of sub-issues; github.sub-issues.list reads them"},
		{Name: "blocked_by_total_count", Description: "Number of issues blocking this issue; " +
			"github.issue-dependencies.list reads them"},
		{Name: "blocking_total_count", Description: "Number of issues this issue blocks; " +
			"github.issue-dependencies.list reads them"},
		{Name: "timeline_items_total_count", Description: "Number of timeline items of the issue"},
		{Name: "user_content_edits_total_count", Description: "Number of edits in the edit history of the issue"},
		{Name: "reactions_total_count", Description: "Number of all reactions on the issue"},
		{Name: "project_items_total_count", Description: "Number of project items of the issue"},
		{Name: "viewer_can_assign", Description: "True when the authenticated user can change the assignees"},
		{Name: "viewer_can_close", Description: "True when the authenticated user can close the issue"},
		{Name: "viewer_can_delete", Description: "True when the authenticated user can delete the issue"},
		{Name: "viewer_can_label", Description: "True when the authenticated user can change the labels"},
		{Name: "viewer_can_react", Description: "True when the authenticated user can react to the issue"},
		{Name: "viewer_can_reopen", Description: "True when the authenticated user can reopen the issue"},
		{Name: "viewer_can_set_fields", Description: "True when the authenticated user can set issue fields"},
		{Name: "viewer_can_set_milestone", Description: "True when the authenticated user can change the milestone"},
		{Name: "viewer_can_subscribe", Description: "True when the authenticated user can change the subscription"},
		{Name: "viewer_can_type", Description: "True when the authenticated user can set the issue type"},
		{Name: "viewer_can_update", Description: "True when the authenticated user can update the issue"},
		{Name: "viewer_can_update_metadata", Description: "True when the authenticated user has triage access or higher"},
		{Name: "viewer_cannot_update_reasons", Description: "Reasons the authenticated user cannot update the issue"},
		{Name: "viewer_did_author", Description: "True when the authenticated user wrote the issue"},
		{Name: "viewer_subscription", Description: "Subscription of the authenticated user: SUBSCRIBED, UNSUBSCRIBED, or IGNORED"},
		{Name: "viewer_thread_subscription_form_action", Description: "Subscription action offered to the authenticated user"},
		{Name: "viewer_thread_subscription_status", Description: "Thread subscription status of the authenticated user"},
	},
)
