package github

import (
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The item width: the members of GitHub's ProjectV2Item, its content (Issue, PullRequest, DraftIssue), and
// its field values that the project item tools answer beyond the compact item. The content reports its
// scalar, enum, and id members and its accounts; its connections and nested objects are read with the tool
// for that object. Rendered variants repeat other members, the deprecated databaseId of the item is left
// out, and the comments of the content are read with the comment tools. The lists of a field value are read
// with their first page of itemValueListFirst entries, with their total and a truncated marker.

const (
	// itemListBodyLimit cuts the body of the content in the list; the detail read cuts at projectionBodyLimit.
	itemListBodyLimit = 1000
	// itemValueListFirst is the first page of a list inside a field value. Every item of a page of 100 carries
	// up to 30 values, so the lists stay small to keep the query far below GitHub's node limit.
	itemValueListFirst = "5"
	itemCreatorFields  = `creator{` + gqlAccountSelection + `}`
)

// itemContentCommon are the members Issue and PullRequest share.
const itemContentCommon = `id databaseId fullDatabaseId body createdAt updatedAt publishedAt lastEditedAt ` +
	`author{` + gqlAccountSelection + `} editor{` + gqlAccountSelection + `} authorAssociation closed closedAt ` +
	`locked activeLockReason createdViaEmail includesCreatedEdit isReadByViewer resourcePath ` +
	`viewerCanClose viewerCanLabel viewerCanReact viewerCanReopen viewerCanSubscribe viewerCanUpdate ` +
	`viewerCanAssign viewerCannotUpdateReasons viewerDidAuthor viewerSubscription`

const itemIssueMembers = `stateReason isPinned trackedIssuesCount bodyResourcePath bodyUrl viewerCanDelete ` +
	`viewerCanSetFields viewerCanSetMilestone viewerCanType viewerCanUpdateMetadata ` +
	`viewerThreadSubscriptionFormAction viewerThreadSubscriptionStatus`

const itemPullRequestMembers = `additions deletions changedFiles baseRefName baseRefOid headRefName headRefOid ` +
	`canBeRebased isCrossRepository isDraft isInMergeQueue isMergeQueueEnabled maintainerCanModify ` +
	`mergeStateStatus mergeable merged mergedAt mergedBy{` + gqlAccountSelection + `} reviewDecision ` +
	`totalCommentsCount permalink checksResourcePath checksUrl revertResourcePath revertUrl ` +
	`viewerCanApplySuggestion viewerCanDeleteHeadRef viewerCanDisableAutoMerge viewerCanEditFiles ` +
	`viewerCanEnableAutoMerge viewerCanMergeAsAdmin viewerCanUpdateBranch`

const itemReviewerSelection = `__typename ` +
	`... on User{login avatarUrl url resourcePath id databaseId name ` + gateUserEmail + `email` + gateEnd +
	` isSiteAdmin} ` +
	`... on Bot{login avatarUrl url resourcePath id databaseId} ` +
	`... on Mannequin{login avatarUrl url resourcePath id databaseId name email} ` +
	gateTeam + `... on Team{id databaseId name slug combinedSlug description privacy notificationSetting ` +
	`createdAt updatedAt url resourcePath}` + gateEnd + ` ` +
	gateEnterpriseTeam + `... on EnterpriseTeam{id fullDatabaseId name slug combinedSlug description privacy ` +
	`notificationSetting createdAt updatedAt}` + gateEnd

// itemIssueFieldSelection selects an issue field value. The union ProjectV2IssueFieldValues takes no field of
// its own, so the members its types share are asked for through their interfaces.
const itemIssueFieldSelection = `__typename ... on Node{id} ` +
	`... on IssueFieldValueCommon{field{__typename ... on IssueFieldCommon{name dataType} ... on Node{id}}} ` +
	`... on IssueFieldTextValue{value} ... on IssueFieldDateValue{value} ... on IssueFieldNumberValue{value} ` +
	`... on IssueFieldSingleSelectValue{value name optionId color description} ` +
	`... on IssueFieldMultiSelectValue{value options{id databaseId fullDatabaseId name color description priority}}`

// itemFieldValueSelection selects the field values of an item: the members every value type has, then those
// of each type. The field of a value is a member of the project field union. The users of a value are Users.
const itemFieldValueSelection = `__typename ` +
	`... on ProjectV2ItemFieldValueCommon{id databaseId createdAt updatedAt ` + itemCreatorFields + `} ` +
	`... on ProjectV2ItemFieldSingleSelectValue{name optionId color description ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldTextValue{text ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldNumberValue{number ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldDateValue{date ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldIterationValue{title iterationId startDate duration ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldMultiSelectValue{value options{id name color description} ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldLabelValue{labels(first:` + itemValueListFirst + `){totalCount nodes{` + gqlLabelSelection + `}} ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldMilestoneValue{milestone{` + gqlMilestoneSelection + `} ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldPullRequestValue{pullRequests(first:` + itemValueListFirst + `){totalCount nodes{` + gqlReferenceSelection + `}} ` +
	fieldRef + `} ` +
	`... on ProjectV2ItemFieldRepositoryValue{repository{` + gqlNestedRepositorySelection + `} ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldReviewerValue{reviewers(first:` + itemValueListFirst + `){totalCount nodes{` + itemReviewerSelection + `}} ` +
	fieldRef + `} ` +
	`... on ProjectV2ItemFieldUserValue{users(first:` + itemValueListFirst + `){totalCount nodes{` + gqlUserSelection + `}} ` + fieldRef + `} ` +
	`... on ProjectV2ItemIssueFieldValue{issueFieldValue{` + itemIssueFieldSelection + `} ` + fieldRef + `}`

// ---- Raw nodes

type itemOptionRaw struct {
	ID          *string `json:"id"`
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
}

type itemReviewerRaw struct {
	gqlAccountRaw
	Slug                *string    `json:"slug"`
	CombinedSlug        *string    `json:"combinedSlug"`
	Description         *string    `json:"description"`
	Privacy             *string    `json:"privacy"`
	NotificationSetting *string    `json:"notificationSetting"`
	CreatedAt           *string    `json:"createdAt"`
	UpdatedAt           *string    `json:"updatedAt"`
	FullDatabaseID      *gqlBigInt `json:"fullDatabaseId"`
}

type itemIssueFieldRaw struct {
	Typename *string `json:"__typename"`
	ID       *string `json:"id"`
	Field    *struct {
		ID       *string `json:"id"`
		Name     *string `json:"name"`
		DataType *string `json:"dataType"`
	} `json:"field"`
	Value       json.RawMessage `json:"value"`
	Name        *string         `json:"name"`
	OptionID    *string         `json:"optionId"`
	Color       *string         `json:"color"`
	Description *string         `json:"description"`
	Options     []*struct {
		ID             *string    `json:"id"`
		DatabaseID     *int64     `json:"databaseId"`
		FullDatabaseID *gqlBigInt `json:"fullDatabaseId"`
		Name           *string    `json:"name"`
		Color          *string    `json:"color"`
		Description    *string    `json:"description"`
		Priority       *int64     `json:"priority"`
	} `json:"options"`
}

// itemContentJSON is the content of an item as itemFragment asks for it. The flat members of the compact
// view and the width members share one node, since an Issue, a PullRequest, and a DraftIssue are different
// types and each answers only its own.
type itemContentJSON struct {
	Typename         string `json:"__typename"`
	Number           int    `json:"number"`
	Title            string `json:"title"`
	IssueState       string `json:"issueState"`
	PullRequestState string `json:"pullRequestState"`
	URL              string `json:"url"`
	Repository       *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Assignees countedLogins `json:"assignees"`
	Labels    countedLabels `json:"labels"`
	Body      *string       `json:"body"`

	ID                  *string        `json:"id"`
	DatabaseID          *int64         `json:"databaseId"`
	FullDatabaseID      *gqlBigInt     `json:"fullDatabaseId"`
	CreatedAt           *string        `json:"createdAt"`
	UpdatedAt           *string        `json:"updatedAt"`
	PublishedAt         *string        `json:"publishedAt"`
	LastEditedAt        *string        `json:"lastEditedAt"`
	Author              *gqlAccountRaw `json:"author"`
	Editor              *gqlAccountRaw `json:"editor"`
	Creator             *gqlAccountRaw `json:"creator"`
	MergedBy            *gqlAccountRaw `json:"mergedBy"`
	AuthorAssociation   *string        `json:"authorAssociation"`
	Closed              *bool          `json:"closed"`
	ClosedAt            *string        `json:"closedAt"`
	Locked              *bool          `json:"locked"`
	ActiveLockReason    *string        `json:"activeLockReason"`
	CreatedViaEmail     *bool          `json:"createdViaEmail"`
	IncludesCreatedEdit *bool          `json:"includesCreatedEdit"`
	IsReadByViewer      *bool          `json:"isReadByViewer"`
	ResourcePath        *string        `json:"resourcePath"`

	StateReason        *string `json:"stateReason"`
	IsPinned           *bool   `json:"isPinned"`
	TrackedIssuesCount *int64  `json:"trackedIssuesCount"`
	BodyResourcePath   *string `json:"bodyResourcePath"`
	BodyURL            *string `json:"bodyUrl"`

	Additions           *int64  `json:"additions"`
	Deletions           *int64  `json:"deletions"`
	ChangedFiles        *int64  `json:"changedFiles"`
	BaseRefName         *string `json:"baseRefName"`
	BaseRefOid          *string `json:"baseRefOid"`
	HeadRefName         *string `json:"headRefName"`
	HeadRefOid          *string `json:"headRefOid"`
	CanBeRebased        *bool   `json:"canBeRebased"`
	IsCrossRepository   *bool   `json:"isCrossRepository"`
	IsDraft             *bool   `json:"isDraft"`
	IsInMergeQueue      *bool   `json:"isInMergeQueue"`
	IsMergeQueueEnabled *bool   `json:"isMergeQueueEnabled"`
	MaintainerCanModify *bool   `json:"maintainerCanModify"`
	MergeStateStatus    *string `json:"mergeStateStatus"`
	Mergeable           *string `json:"mergeable"`
	Merged              *bool   `json:"merged"`
	MergedAt            *string `json:"mergedAt"`
	ReviewDecision      *string `json:"reviewDecision"`
	TotalCommentsCount  *int64  `json:"totalCommentsCount"`
	Permalink           *string `json:"permalink"`
	ChecksResourcePath  *string `json:"checksResourcePath"`
	ChecksURL           *string `json:"checksUrl"`
	RevertResourcePath  *string `json:"revertResourcePath"`
	RevertURL           *string `json:"revertUrl"`

	ViewerCanApplySuggestion           *bool     `json:"viewerCanApplySuggestion"`
	ViewerCanAssign                    *bool     `json:"viewerCanAssign"`
	ViewerCanClose                     *bool     `json:"viewerCanClose"`
	ViewerCanDelete                    *bool     `json:"viewerCanDelete"`
	ViewerCanDeleteHeadRef             *bool     `json:"viewerCanDeleteHeadRef"`
	ViewerCanDisableAutoMerge          *bool     `json:"viewerCanDisableAutoMerge"`
	ViewerCanEditFiles                 *bool     `json:"viewerCanEditFiles"`
	ViewerCanEnableAutoMerge           *bool     `json:"viewerCanEnableAutoMerge"`
	ViewerCanLabel                     *bool     `json:"viewerCanLabel"`
	ViewerCanMergeAsAdmin              *bool     `json:"viewerCanMergeAsAdmin"`
	ViewerCanReact                     *bool     `json:"viewerCanReact"`
	ViewerCanReopen                    *bool     `json:"viewerCanReopen"`
	ViewerCanSetFields                 *bool     `json:"viewerCanSetFields"`
	ViewerCanSetMilestone              *bool     `json:"viewerCanSetMilestone"`
	ViewerCanSubscribe                 *bool     `json:"viewerCanSubscribe"`
	ViewerCanType                      *bool     `json:"viewerCanType"`
	ViewerCanUpdate                    *bool     `json:"viewerCanUpdate"`
	ViewerCanUpdateBranch              *bool     `json:"viewerCanUpdateBranch"`
	ViewerCanUpdateMetadata            *bool     `json:"viewerCanUpdateMetadata"`
	ViewerCannotUpdateReasons          []*string `json:"viewerCannotUpdateReasons"`
	ViewerDidAuthor                    *bool     `json:"viewerDidAuthor"`
	ViewerSubscription                 *string   `json:"viewerSubscription"`
	ViewerThreadSubscriptionFormAction *string   `json:"viewerThreadSubscriptionFormAction"`
	ViewerThreadSubscriptionStatus     *string   `json:"viewerThreadSubscriptionStatus"`
}

// ---- Output

// ItemOption is one selected option of a multi-select value.
type ItemOption struct {
	ID          *string `json:"id,omitempty"`
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ItemReviewer is a requested reviewer: an account, a team, or an enterprise team. Type says which.
type ItemReviewer struct {
	GraphQLAccount
	FullDatabaseID      *string `json:"full_database_id,omitempty"`
	Slug                *string `json:"slug,omitempty"`
	CombinedSlug        *string `json:"combined_slug,omitempty"`
	Description         *string `json:"description,omitempty"`
	Privacy             *string `json:"privacy,omitempty"`
	NotificationSetting *string `json:"notification_setting,omitempty"`
	CreatedAt           *string `json:"created_at,omitempty"`
	UpdatedAt           *string `json:"updated_at,omitempty"`
}

// ItemIssueFieldOption is one selected option of a multi-select issue field value.
type ItemIssueFieldOption struct {
	ID             *string `json:"id,omitempty"`
	DatabaseID     *int64  `json:"database_id,omitempty"`
	FullDatabaseID *string `json:"full_database_id,omitempty"`
	Name           *string `json:"name,omitempty"`
	Color          *string `json:"color,omitempty"`
	Description    *string `json:"description,omitempty"`
	Priority       *int64  `json:"priority,omitempty"`
}

// ItemIssueFieldValue is the value of an organization issue field on the content of an item.
type ItemIssueFieldValue struct {
	Type             *string                `json:"type,omitempty"`
	ID               *string                `json:"id,omitempty"`
	FieldID          *string                `json:"field_id,omitempty"`
	FieldName        *string                `json:"field_name,omitempty"`
	FieldDataType    *string                `json:"field_data_type,omitempty"`
	Value            any                    `json:"value,omitempty"`
	Name             *string                `json:"name,omitempty"`
	OptionID         *string                `json:"option_id,omitempty"`
	Color            *string                `json:"color,omitempty"`
	Description      *string                `json:"description,omitempty"`
	Options          []ItemIssueFieldOption `json:"options,omitempty"`
	OptionsTruncated bool                   `json:"options_truncated,omitempty"`
}

// ItemFieldValue is one value a project field holds on an item. Type is the GraphQL type of the value; the
// members it has are those of that type.
type ItemFieldValue struct {
	Type                *string                  `json:"type,omitempty"`
	ID                  *string                  `json:"id,omitempty"`
	DatabaseID          *int64                   `json:"database_id,omitempty"`
	FieldID             *string                  `json:"field_id,omitempty"`
	FieldName           *string                  `json:"field_name,omitempty"`
	CreatedAt           *string                  `json:"created_at,omitempty"`
	UpdatedAt           *string                  `json:"updated_at,omitempty"`
	Creator             *GraphQLAccount          `json:"creator,omitempty"`
	Text                *string                  `json:"text,omitempty"`
	Number              *json.Number             `json:"number,omitempty"`
	Date                *string                  `json:"date,omitempty"`
	OptionID            *string                  `json:"option_id,omitempty"`
	Name                *string                  `json:"name,omitempty"`
	Color               *string                  `json:"color,omitempty"`
	Description         *string                  `json:"description,omitempty"`
	IterationID         *string                  `json:"iteration_id,omitempty"`
	Title               *string                  `json:"title,omitempty"`
	StartDate           *string                  `json:"start_date,omitempty"`
	Duration            *int64                   `json:"duration,omitempty"`
	Options             []ItemOption             `json:"options,omitempty"`
	OptionsTotalCount   *int64                   `json:"options_total_count,omitempty"`
	OptionsTruncated    bool                     `json:"options_truncated,omitempty"`
	Value               *string                  `json:"value,omitempty"`
	Labels              []GraphQLLabel           `json:"labels,omitempty"`
	LabelsTotalCount    *int64                   `json:"labels_total_count,omitempty"`
	LabelsTruncated     bool                     `json:"labels_truncated,omitempty"`
	Milestone           *GraphQLMilestone        `json:"milestone,omitempty"`
	PullRequests        []GraphQLReference       `json:"pull_requests,omitempty"`
	PullRequestsTotal   *int64                   `json:"pull_requests_total_count,omitempty"`
	PullRequestsCut     bool                     `json:"pull_requests_truncated,omitempty"`
	Repository          *GraphQLNestedRepository `json:"repository,omitempty"`
	Reviewers           []ItemReviewer           `json:"reviewers,omitempty"`
	ReviewersTotalCount *int64                   `json:"reviewers_total_count,omitempty"`
	ReviewersTruncated  bool                     `json:"reviewers_truncated,omitempty"`
	Users               []GraphQLAccount         `json:"users,omitempty"`
	UsersTotalCount     *int64                   `json:"users_total_count,omitempty"`
	UsersTruncated      bool                     `json:"users_truncated,omitempty"`
	IssueFieldValue     *ItemIssueFieldValue     `json:"issue_field_value,omitempty"`
}

// ItemContent is the issue, pull request, or draft issue behind an item, with its scalar, enum, and id
// members and its accounts. The members of the other content types are absent.
type ItemContent struct {
	ID                             *string         `json:"id,omitempty"`
	DatabaseID                     *int64          `json:"database_id,omitempty"`
	FullDatabaseID                 *string         `json:"full_database_id,omitempty"`
	Number                         *int64          `json:"number,omitempty"`
	Title                          *string         `json:"title,omitempty"`
	Body                           *string         `json:"body,omitempty"`
	BodyTruncated                  bool            `json:"body_truncated,omitempty"`
	State                          *string         `json:"state,omitempty"`
	StateReason                    *string         `json:"state_reason,omitempty"`
	Closed                         *bool           `json:"closed,omitempty"`
	ClosedAt                       *string         `json:"closed_at,omitempty"`
	CreatedAt                      *string         `json:"created_at,omitempty"`
	UpdatedAt                      *string         `json:"updated_at,omitempty"`
	PublishedAt                    *string         `json:"published_at,omitempty"`
	LastEditedAt                   *string         `json:"last_edited_at,omitempty"`
	AuthorDetail                   *GraphQLAccount `json:"author_detail,omitempty"`
	Editor                         *GraphQLAccount `json:"editor,omitempty"`
	Creator                        *GraphQLAccount `json:"creator,omitempty"`
	AuthorAssociation              *string         `json:"author_association,omitempty"`
	Locked                         *bool           `json:"locked,omitempty"`
	ActiveLockReason               *string         `json:"active_lock_reason,omitempty"`
	CreatedViaEmail                *bool           `json:"created_via_email,omitempty"`
	IncludesCreatedEdit            *bool           `json:"includes_created_edit,omitempty"`
	IsReadByViewer                 *bool           `json:"is_read_by_viewer,omitempty"`
	IsPinned                       *bool           `json:"is_pinned,omitempty"`
	TrackedIssuesCount             *int64          `json:"tracked_issues_count,omitempty"`
	URL                            *string         `json:"url,omitempty"`
	Links                          Links           `json:"links,omitempty"`
	Additions                      *int64          `json:"additions,omitempty"`
	Deletions                      *int64          `json:"deletions,omitempty"`
	ChangedFiles                   *int64          `json:"changed_files,omitempty"`
	BaseRefName                    *string         `json:"base_ref_name,omitempty"`
	BaseRefOid                     *string         `json:"base_ref_oid,omitempty"`
	HeadRefName                    *string         `json:"head_ref_name,omitempty"`
	HeadRefOid                     *string         `json:"head_ref_oid,omitempty"`
	CanBeRebased                   *bool           `json:"can_be_rebased,omitempty"`
	IsCrossRepository              *bool           `json:"is_cross_repository,omitempty"`
	IsDraft                        *bool           `json:"is_draft,omitempty"`
	IsInMergeQueue                 *bool           `json:"is_in_merge_queue,omitempty"`
	IsMergeQueueEnabled            *bool           `json:"is_merge_queue_enabled,omitempty"`
	MaintainerCanModify            *bool           `json:"maintainer_can_modify,omitempty"`
	MergeStateStatus               *string         `json:"merge_state_status,omitempty"`
	Mergeable                      *string         `json:"mergeable,omitempty"`
	Merged                         *bool           `json:"merged,omitempty"`
	MergedAt                       *string         `json:"merged_at,omitempty"`
	MergedBy                       *GraphQLAccount `json:"merged_by,omitempty"`
	ReviewDecision                 *string         `json:"review_decision,omitempty"`
	TotalCommentsCount             *int64          `json:"total_comments_count,omitempty"`
	ViewerCanApplySuggestion       *bool           `json:"viewer_can_apply_suggestion,omitempty"`
	ViewerCanAssign                *bool           `json:"viewer_can_assign,omitempty"`
	ViewerCanClose                 *bool           `json:"viewer_can_close,omitempty"`
	ViewerCanDelete                *bool           `json:"viewer_can_delete,omitempty"`
	ViewerCanDeleteHeadRef         *bool           `json:"viewer_can_delete_head_ref,omitempty"`
	ViewerCanDisableAutoMerge      *bool           `json:"viewer_can_disable_auto_merge,omitempty"`
	ViewerCanEditFiles             *bool           `json:"viewer_can_edit_files,omitempty"`
	ViewerCanEnableAutoMerge       *bool           `json:"viewer_can_enable_auto_merge,omitempty"`
	ViewerCanLabel                 *bool           `json:"viewer_can_label,omitempty"`
	ViewerCanMergeAsAdmin          *bool           `json:"viewer_can_merge_as_admin,omitempty"`
	ViewerCanReact                 *bool           `json:"viewer_can_react,omitempty"`
	ViewerCanReopen                *bool           `json:"viewer_can_reopen,omitempty"`
	ViewerCanSetFields             *bool           `json:"viewer_can_set_fields,omitempty"`
	ViewerCanSetMilestone          *bool           `json:"viewer_can_set_milestone,omitempty"`
	ViewerCanSubscribe             *bool           `json:"viewer_can_subscribe,omitempty"`
	ViewerCanType                  *bool           `json:"viewer_can_type,omitempty"`
	ViewerCanUpdate                *bool           `json:"viewer_can_update,omitempty"`
	ViewerCanUpdateBranch          *bool           `json:"viewer_can_update_branch,omitempty"`
	ViewerCanUpdateMetadata        *bool           `json:"viewer_can_update_metadata,omitempty"`
	ViewerCannotUpdateReasons      []string        `json:"viewer_cannot_update_reasons,omitempty"`
	ViewerDidAuthor                *bool           `json:"viewer_did_author,omitempty"`
	ViewerSubscription             *string         `json:"viewer_subscription,omitempty"`
	ViewerThreadSubscriptionAction *string         `json:"viewer_thread_subscription_form_action,omitempty"`
	ViewerThreadSubscription       *string         `json:"viewer_thread_subscription_status,omitempty"`
}

// ---- Mapping

func (r *itemOptionRaw) project() *ItemOption {
	if r == nil {
		return nil
	}
	return &ItemOption{ID: gqlName(r.ID), Name: gqlName(r.Name), Color: gqlName(r.Color),
		Description: gqlText(r.Description)}
}

func (r *itemReviewerRaw) project() *ItemReviewer {
	if r == nil {
		return nil
	}
	return &ItemReviewer{GraphQLAccount: *r.gqlAccountRaw.project(), FullDatabaseID: r.FullDatabaseID.text(),
		Slug: gqlName(r.Slug), CombinedSlug: gqlName(r.CombinedSlug), Description: gqlText(r.Description),
		Privacy: gqlName(r.Privacy), NotificationSetting: gqlName(r.NotificationSetting),
		CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt)}
}

func (r *itemIssueFieldRaw) project() *ItemIssueFieldValue {
	if r == nil {
		return nil
	}
	v := &ItemIssueFieldValue{Type: gqlName(r.Typename), ID: gqlName(r.ID), Name: gqlName(r.Name),
		OptionID: gqlName(r.OptionID), Color: gqlName(r.Color), Description: gqlText(r.Description)}
	if r.Field != nil {
		v.FieldID, v.FieldName, v.FieldDataType = gqlName(r.Field.ID), gqlName(r.Field.Name), gqlName(r.Field.DataType)
	}
	var text string
	var number json.Number
	if json.Unmarshal(r.Value, &text) == nil && text != "" {
		v.Value = *longText(&text)
	} else if json.Unmarshal(r.Value, &number) == nil && number != "" {
		v.Value = number
	}
	options, cut := capList(r.Options, func(o *struct {
		ID             *string    `json:"id"`
		DatabaseID     *int64     `json:"databaseId"`
		FullDatabaseID *gqlBigInt `json:"fullDatabaseId"`
		Name           *string    `json:"name"`
		Color          *string    `json:"color"`
		Description    *string    `json:"description"`
		Priority       *int64     `json:"priority"`
	}) *ItemIssueFieldOption {
		if o == nil {
			return nil
		}
		return &ItemIssueFieldOption{ID: gqlName(o.ID), DatabaseID: o.DatabaseID, FullDatabaseID: o.FullDatabaseID.text(),
			Name: gqlName(o.Name), Color: gqlName(o.Color), Description: gqlText(o.Description), Priority: o.Priority}
	})
	v.Options, v.OptionsTruncated = options, cut
	return v
}

// widthValue maps the members of one field value beyond the compact view. A value without a type is no
// value GitHub sent and is left out.
func (v *fieldValueJSON) widthValue(allowed allowlist) (ItemFieldValue, bool) {
	if v.Typename == nil || *v.Typename == "" {
		return ItemFieldValue{}, false
	}
	out := ItemFieldValue{Type: gqlName(v.Typename), ID: gqlName(v.ID), DatabaseID: v.DatabaseID,
		FieldID: gqlName(&v.Field.ID), FieldName: gqlName(v.Field.Name), CreatedAt: gqlName(v.CreatedAt),
		UpdatedAt: gqlName(v.UpdatedAt), Creator: v.Creator.project(), Text: gqlText(v.Text), Number: v.Number,
		Date: gqlName(v.Date), OptionID: gqlName(v.OptionID), Name: gqlName(v.Name), Color: gqlName(v.Color),
		Description: gqlText(v.Description), IterationID: gqlName(v.IterationID), Title: gqlName(v.Title),
		StartDate: gqlName(v.StartDate), Duration: v.Duration, Value: gqlName(v.Value),
		Milestone: v.Milestone.project(), Repository: v.Repository.project(allowed),
		IssueFieldValue: v.IssueFieldValue.project()}
	if v.Options != nil {
		out.Options, out.OptionsTruncated = capList(v.Options, (*itemOptionRaw).project)
		if out.OptionsTruncated {
			total := int64(len(v.Options))
			out.OptionsTotalCount = &total
		}
	}
	out.Labels, out.LabelsTotalCount, out.LabelsTruncated = pageItems(v.Labels, (*gqlLabelRaw).project)
	out.PullRequests, out.PullRequestsTotal, out.PullRequestsCut = pageItems(v.PullRequests, (*gqlReferenceRaw).project)
	out.Reviewers, out.ReviewersTotalCount, out.ReviewersTruncated = pageItems(v.Reviewers, (*itemReviewerRaw).project)
	out.Users, out.UsersTotalCount, out.UsersTruncated = pageItems(v.Users, (*gqlAccountRaw).project)
	return out, true
}

// width maps the content. Its state is the one of the issue or of the pull request, whichever GitHub sent.
func (r *itemContentJSON) width(bodyLimit int) *ItemContent {
	if r == nil {
		return nil
	}
	c := &ItemContent{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, FullDatabaseID: r.FullDatabaseID.text(),
		Title: gqlName(&r.Title), State: gqlName(statePointer(r.IssueState, r.PullRequestState)),
		StateReason: gqlName(r.StateReason), Closed: r.Closed, ClosedAt: gqlName(r.ClosedAt),
		CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt), PublishedAt: gqlName(r.PublishedAt),
		LastEditedAt: gqlName(r.LastEditedAt), AuthorDetail: r.Author.project(), Editor: r.Editor.project(),
		Creator: r.Creator.project(), AuthorAssociation: gqlName(r.AuthorAssociation), Locked: r.Locked,
		ActiveLockReason: gqlName(r.ActiveLockReason), CreatedViaEmail: r.CreatedViaEmail,
		IncludesCreatedEdit: r.IncludesCreatedEdit, IsReadByViewer: r.IsReadByViewer, IsPinned: r.IsPinned,
		TrackedIssuesCount: r.TrackedIssuesCount, URL: address(&r.URL),
		Links: gqlLinks([]string{"resource_path", "body_resource_path", "body_url", "permalink",
			"checks_resource_path", "checks_url", "revert_resource_path", "revert_url"}, r.ResourcePath,
			r.BodyResourcePath, r.BodyURL, r.Permalink, r.ChecksResourcePath, r.ChecksURL, r.RevertResourcePath,
			r.RevertURL),
		Additions: r.Additions, Deletions: r.Deletions, ChangedFiles: r.ChangedFiles,
		BaseRefName: gqlName(r.BaseRefName), BaseRefOid: gqlName(r.BaseRefOid), HeadRefName: gqlName(r.HeadRefName),
		HeadRefOid: gqlName(r.HeadRefOid), CanBeRebased: r.CanBeRebased, IsCrossRepository: r.IsCrossRepository,
		IsDraft: r.IsDraft, IsInMergeQueue: r.IsInMergeQueue, IsMergeQueueEnabled: r.IsMergeQueueEnabled,
		MaintainerCanModify: r.MaintainerCanModify, MergeStateStatus: gqlName(r.MergeStateStatus),
		Mergeable: gqlName(r.Mergeable), Merged: r.Merged, MergedAt: gqlName(r.MergedAt), MergedBy: r.MergedBy.project(),
		ReviewDecision: gqlName(r.ReviewDecision), TotalCommentsCount: r.TotalCommentsCount,
		ViewerCanApplySuggestion: r.ViewerCanApplySuggestion, ViewerCanAssign: r.ViewerCanAssign,
		ViewerCanClose: r.ViewerCanClose, ViewerCanDelete: r.ViewerCanDelete,
		ViewerCanDeleteHeadRef: r.ViewerCanDeleteHeadRef, ViewerCanDisableAutoMerge: r.ViewerCanDisableAutoMerge,
		ViewerCanEditFiles: r.ViewerCanEditFiles, ViewerCanEnableAutoMerge: r.ViewerCanEnableAutoMerge,
		ViewerCanLabel: r.ViewerCanLabel, ViewerCanMergeAsAdmin: r.ViewerCanMergeAsAdmin,
		ViewerCanReact: r.ViewerCanReact, ViewerCanReopen: r.ViewerCanReopen, ViewerCanSetFields: r.ViewerCanSetFields,
		ViewerCanSetMilestone: r.ViewerCanSetMilestone, ViewerCanSubscribe: r.ViewerCanSubscribe,
		ViewerCanType: r.ViewerCanType, ViewerCanUpdate: r.ViewerCanUpdate, ViewerCanUpdateBranch: r.ViewerCanUpdateBranch,
		ViewerCanUpdateMetadata: r.ViewerCanUpdateMetadata, ViewerCannotUpdateReasons: gqlNames(r.ViewerCannotUpdateReasons),
		ViewerDidAuthor: r.ViewerDidAuthor, ViewerSubscription: gqlName(r.ViewerSubscription),
		ViewerThreadSubscriptionAction: gqlName(r.ViewerThreadSubscriptionFormAction),
		ViewerThreadSubscription:       gqlName(r.ViewerThreadSubscriptionStatus)}
	if r.Number > 0 {
		number := int64(r.Number)
		c.Number = &number
	}
	c.Body, c.BodyTruncated = cleanText(filled(r.Body), bodyLimit, true)
	return c
}

func statePointer(issue, pullRequest string) *string {
	if state := issue + pullRequest; state != "" {
		return &state
	}
	return nil
}

// ---- Schema and fields

// specSchema builds closed-object properties from "name:kind" pairs: s string, i integer, n number,
// b boolean, l list of strings.
func specSchema(spec string) string {
	kinds := map[string]string{"s": `{"type":"string"}`, "i": `{"type":"integer"}`, "n": `{"type":"number"}`,
		"b": `{"type":"boolean"}`, "l": `{"type":"array","items":{"type":"string"}}`}
	var parts []string
	for _, pair := range strings.Fields(spec) {
		name, kind, _ := strings.Cut(pair, ":")
		parts = append(parts, `"`+name+`":`+kinds[kind])
	}
	return strings.Join(parts, ",")
}

var itemLinkKeys = []string{"resource_path", "body_resource_path", "body_url", "permalink", "checks_resource_path",
	"checks_url", "revert_resource_path", "revert_url"}

var itemContentSchema = objectSchema(specSchema(`id:s database_id:i full_database_id:s number:i title:s body:s `+
	`body_truncated:b state:s state_reason:s closed:b closed_at:s created_at:s updated_at:s published_at:s `+
	`last_edited_at:s author_association:s locked:b active_lock_reason:s created_via_email:b `+
	`includes_created_edit:b is_read_by_viewer:b is_pinned:b tracked_issues_count:i url:s additions:i deletions:i `+
	`changed_files:i base_ref_name:s base_ref_oid:s head_ref_name:s head_ref_oid:s can_be_rebased:b `+
	`is_cross_repository:b is_draft:b is_in_merge_queue:b is_merge_queue_enabled:b maintainer_can_modify:b `+
	`merge_state_status:s mergeable:s merged:b merged_at:s review_decision:s total_comments_count:i `+
	`viewer_can_apply_suggestion:b viewer_can_assign:b viewer_can_close:b viewer_can_delete:b `+
	`viewer_can_delete_head_ref:b viewer_can_disable_auto_merge:b viewer_can_edit_files:b `+
	`viewer_can_enable_auto_merge:b viewer_can_label:b viewer_can_merge_as_admin:b viewer_can_react:b `+
	`viewer_can_reopen:b viewer_can_set_fields:b viewer_can_set_milestone:b viewer_can_subscribe:b `+
	`viewer_can_type:b viewer_can_update:b viewer_can_update_branch:b viewer_can_update_metadata:b `+
	`viewer_cannot_update_reasons:l viewer_did_author:b viewer_subscription:s `+
	`viewer_thread_subscription_form_action:s viewer_thread_subscription_status:s`) + `,` +
	`"author_detail":` + gqlAccountSchema + `,"editor":` + gqlAccountSchema + `,"creator":` + gqlAccountSchema + `,` +
	`"merged_by":` + gqlAccountSchema + `,"links":` + linksSchema(itemLinkKeys))

var itemOptionSchema = objectSchema(specSchema(`id:s name:s color:s description:s`))

var itemReviewerSchema = objectSchema(gqlAccountProperties + `,` + specSchema(`full_database_id:s slug:s `+
	`combined_slug:s description:s privacy:s notification_setting:s created_at:s updated_at:s`))

var itemIssueFieldValueSchema = objectSchema(specSchema(`type:s id:s field_id:s field_name:s field_data_type:s name:s `+
	`option_id:s color:s description:s options_truncated:b`) + `,"value":{},"options":` +
	`{"type":"array","items":` + objectSchema(specSchema(`id:s database_id:i full_database_id:s name:s color:s `+
	`description:s priority:i`)) + `}`)

func itemListSchema(name, itemSchema string) string {
	return `"` + name + `":{"type":"array","items":` + itemSchema + `},"` + name + `_total_count":{"type":"integer"},"` +
		name + `_truncated":{"type":"boolean"}`
}

var itemFieldValueSchema = objectSchema(specSchema(`type:s id:s database_id:i field_id:s field_name:s created_at:s `+
	`updated_at:s text:s date:s option_id:s name:s color:s description:s iteration_id:s title:s start_date:s `+
	`duration:i value:s`) + `,"creator":` + gqlAccountSchema + `,"number":{"type":"number"},` +
	itemListSchema("options", itemOptionSchema) + `,` + itemListSchema("labels", gqlLabelSchema) + `,` +
	`"milestone":` + gqlMilestoneSchema + `,` + itemListSchema("pull_requests", gqlReferenceSchema) + `,` +
	`"repository":` + nestedRepositorySchema + `,` + itemListSchema("reviewers", itemReviewerSchema) + `,` +
	itemListSchema("users", gqlAccountSchema) + `,"issue_field_value":` + itemIssueFieldValueSchema)

// itemWidthProperties are the properties the item gains beside the compact view.
var itemWidthProperties = `"url":{"type":"string"},` + specSchema(`created_at:s updated_at:s is_archived:b full_database_id:s`) +
	`,"creator":` + gqlAccountSchema + `,"content":` + itemContentSchema + `,"field_values":{"type":"array","items":` +
	itemFieldValueSchema + `},"field_values_total_count":{"type":"integer"},"field_values_truncated":{"type":"boolean"}`

// itemWidthFields describes the members of an item beside the compact view.
var itemWidthFields = []capability.Field{
	{Name: "url", Description: "Web address of the issue or pull request, untrusted data, never fetched"},
	{Name: "created_at", Description: "Time the item was added to the project"},
	{Name: "updated_at", Description: "Time of the last change of the item"},
	{Name: "creator", Description: "Account that added the item: " + gqlAccountMembers},
	{Name: "is_archived", Description: "True when the item is archived"},
	{Name: "full_database_id", Description: "Numeric identifier of the item as digits"},
	{Name: "content", Description: "Issue, pull request, or draft issue behind the item with its scalar, enum, and " +
		"id members and its accounts (author_detail, editor, merged_by, creator of a draft), links, and the " +
		"viewer_* rights; a list cuts body at 1000 characters, the detail at 4000, and body_truncated says so; " +
		"titles, bodies, and addresses are untrusted data; absent for a redacted item"},
	{Name: "field_values", Description: "Values of the project fields on the item, each with type, id, " +
		"database_id, field_id, field_name, created_at, updated_at, creator, and the members of its type " +
		"(text, number, date, option_id, name, color, description, iteration_id, title, start_date, duration, " +
		"options, labels, milestone, pull_requests, repository, reviewers, users, issue_field_value); lists " +
		"hold the first 5 with <name>_total_count and <name>_truncated; texts are untrusted data"},
	{Name: "field_values_total_count", Description: "Number of all field values of the item; field_values holds the first 30"},
	{Name: "field_values_truncated", Description: "True when the item holds more field values than shown"},
}
