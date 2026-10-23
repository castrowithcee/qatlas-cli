package github

import (
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The discussion width: the members of GitHub's Discussion, DiscussionComment, DiscussionCategory, and
// DiscussionPoll the discussion read tools answer beyond the compact view. The repository, the discussion of
// a comment, and the category and poll backlinks are left out, since the call names them; bodyHTML, bodyText,
// and emojiHTML repeat other members; the comments, the reactions, and the edit history are counted only,
// since GitHub serves them as lists of their own. The labels and the poll options are read with their first
// page, the options at most discussionPollOptionsGet or discussionPollOptionsList.

const (
	discussionLabelsFirst      = 20
	discussionPollOptionsGet   = 20
	discussionPollOptionsList  = 10
	discussionAnswerSelection  = `id databaseId url author{` + gqlAccountSelection + `} createdAt isAnswer`
	discussionCategorySelected = `id name slug description emoji isAnswerable createdAt updatedAt`
)

// discussionWidthSelection selects the members beyond the compact discussion; options is the size of the
// first page of poll options.
func discussionWidthSelection(options int) string {
	return `databaseId authorAssociation editor{` + gqlAccountSelection + `} lastEditedAt publishedAt ` +
		`createdViaEmail includesCreatedEdit activeLockReason closedAt stateReason ` +
		`answer{` + discussionAnswerSelection + `} answerChosenBy{` + gqlAccountSelection + `} ` +
		`poll{id question totalVoteCount viewerCanVote viewerHasVoted options(first:` + strconv.Itoa(options) + `){` +
		`totalCount pageInfo{hasNextPage} nodes{id option totalVoteCount viewerHasVoted}}} ` +
		`reactionGroups{` + gqlReactionGroupSelection + `} reactions{totalCount} resourcePath ` +
		`userContentEdits{totalCount} viewerCanClose viewerCanDelete viewerCanLabel viewerCanReact ` +
		`viewerCanReopen viewerCanSubscribe viewerCanUpdate viewerCanUpvote viewerDidAuthor viewerHasUpvoted ` +
		`viewerSubscription`
}

// discussionSelection is the whole discussion as the three read tools ask for it.
func discussionSelection(options int) string {
	return `id number title body author{` + gqlAccountSelection + `} category{` + discussionCategorySelected + `} ` +
		`closed locked isAnswered answerChosenAt upvoteCount comments{totalCount} ` +
		`labels(first:` + strconv.Itoa(discussionLabelsFirst) + `){totalCount pageInfo{hasNextPage} nodes{` +
		gqlLabelSelection + `}} createdAt updatedAt url ` + discussionWidthSelection(options)
}

// connectionTruncated reports whether a connection holds more entries than its first page returned.
func connectionTruncated(total *int64, hasNext bool, shown int) bool {
	return hasNext || (total != nil && *total > int64(shown))
}

type discussionAnswerRaw struct {
	ID         *string        `json:"id"`
	DatabaseID *int64         `json:"databaseId"`
	URL        *string        `json:"url"`
	Author     *gqlAccountRaw `json:"author"`
	CreatedAt  *string        `json:"createdAt"`
	IsAnswer   *bool          `json:"isAnswer"`
}

type discussionPollRaw struct {
	ID             *string `json:"id"`
	Question       *string `json:"question"`
	TotalVoteCount *int64  `json:"totalVoteCount"`
	ViewerCanVote  *bool   `json:"viewerCanVote"`
	ViewerHasVoted *bool   `json:"viewerHasVoted"`
	Options        *struct {
		TotalCount *int64 `json:"totalCount"`
		PageInfo   struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
		Nodes []*struct {
			ID             *string `json:"id"`
			Option         *string `json:"option"`
			TotalVoteCount *int64  `json:"totalVoteCount"`
			ViewerHasVoted *bool   `json:"viewerHasVoted"`
		} `json:"nodes"`
	} `json:"options"`
}

// discussionWidthRaw is the Discussion as discussionWidthSelection asks for it.
type discussionWidthRaw struct {
	DatabaseID          *int64                 `json:"databaseId"`
	AuthorAssociation   *string                `json:"authorAssociation"`
	Editor              *gqlAccountRaw         `json:"editor"`
	LastEditedAt        *string                `json:"lastEditedAt"`
	PublishedAt         *string                `json:"publishedAt"`
	CreatedViaEmail     *bool                  `json:"createdViaEmail"`
	IncludesCreatedEdit *bool                  `json:"includesCreatedEdit"`
	ActiveLockReason    *string                `json:"activeLockReason"`
	ClosedAt            *string                `json:"closedAt"`
	StateReason         *string                `json:"stateReason"`
	Answer              *discussionAnswerRaw   `json:"answer"`
	AnswerChosenBy      *gqlAccountRaw         `json:"answerChosenBy"`
	Poll                *discussionPollRaw     `json:"poll"`
	ReactionGroups      []*gqlReactionGroupRaw `json:"reactionGroups"`
	Reactions           *countRaw              `json:"reactions"`
	ResourcePath        *string                `json:"resourcePath"`
	UserContentEdits    *countRaw              `json:"userContentEdits"`
	ViewerCanClose      *bool                  `json:"viewerCanClose"`
	ViewerCanDelete     *bool                  `json:"viewerCanDelete"`
	ViewerCanLabel      *bool                  `json:"viewerCanLabel"`
	ViewerCanReact      *bool                  `json:"viewerCanReact"`
	ViewerCanReopen     *bool                  `json:"viewerCanReopen"`
	ViewerCanSubscribe  *bool                  `json:"viewerCanSubscribe"`
	ViewerCanUpdate     *bool                  `json:"viewerCanUpdate"`
	ViewerCanUpvote     *bool                  `json:"viewerCanUpvote"`
	ViewerDidAuthor     *bool                  `json:"viewerDidAuthor"`
	ViewerHasUpvoted    *bool                  `json:"viewerHasUpvoted"`
	ViewerSubscription  *string                `json:"viewerSubscription"`
}

// DiscussionAnswer is the comment chosen as the answer of a discussion; the comment is read with
// github.discussion-comments.list.
type DiscussionAnswer struct {
	ID         *string         `json:"id,omitempty"`
	DatabaseID *int64          `json:"database_id,omitempty"`
	URL        *string         `json:"url,omitempty"`
	Author     *GraphQLAccount `json:"author,omitempty"`
	CreatedAt  *string         `json:"created_at,omitempty"`
	IsAnswer   *bool           `json:"is_answer,omitempty"`
}

// DiscussionPollOption is one option of a poll.
type DiscussionPollOption struct {
	ID             *string `json:"id,omitempty"`
	Option         *string `json:"option,omitempty"`
	TotalVoteCount *int64  `json:"total_vote_count,omitempty"`
	ViewerHasVoted *bool   `json:"viewer_has_voted,omitempty"`
}

// DiscussionPoll is the poll of a discussion with the first page of its options.
type DiscussionPoll struct {
	ID                *string                `json:"id,omitempty"`
	Question          *string                `json:"question,omitempty"`
	TotalVoteCount    *int64                 `json:"total_vote_count,omitempty"`
	ViewerCanVote     *bool                  `json:"viewer_can_vote,omitempty"`
	ViewerHasVoted    *bool                  `json:"viewer_has_voted,omitempty"`
	Options           []DiscussionPollOption `json:"options,omitempty"`
	OptionsTotalCount *int64                 `json:"options_total_count,omitempty"`
	OptionsTruncated  bool                   `json:"options_truncated,omitempty"`
}

// DiscussionWidth is the width of GitHub's Discussion next to the compact discussion.
type DiscussionWidth struct {
	DatabaseID                 *int64                 `json:"database_id,omitempty"`
	AuthorDetail               *GraphQLAccount        `json:"author_detail,omitempty"`
	AuthorAssociation          *string                `json:"author_association,omitempty"`
	Editor                     *GraphQLAccount        `json:"editor,omitempty"`
	LastEditedAt               *string                `json:"last_edited_at,omitempty"`
	PublishedAt                *string                `json:"published_at,omitempty"`
	CreatedViaEmail            *bool                  `json:"created_via_email,omitempty"`
	IncludesCreatedEdit        *bool                  `json:"includes_created_edit,omitempty"`
	ActiveLockReason           *string                `json:"active_lock_reason,omitempty"`
	ClosedAt                   *string                `json:"closed_at,omitempty"`
	StateReason                *string                `json:"state_reason,omitempty"`
	Answer                     *DiscussionAnswer      `json:"answer,omitempty"`
	AnswerChosenBy             *GraphQLAccount        `json:"answer_chosen_by,omitempty"`
	Poll                       *DiscussionPoll        `json:"poll,omitempty"`
	LabelsDetails              []GraphQLLabel         `json:"labels_details,omitempty"`
	LabelsTotalCount           *int64                 `json:"labels_total_count,omitempty"`
	LabelsTruncated            bool                   `json:"labels_truncated,omitempty"`
	ReactionGroups             []GraphQLReactionGroup `json:"reaction_groups,omitempty"`
	Links                      Links                  `json:"links,omitempty"`
	ReactionsTotalCount        *int64                 `json:"reactions_total_count,omitempty"`
	UserContentEditsTotalCount *int64                 `json:"user_content_edits_total_count,omitempty"`
	ViewerCanClose             *bool                  `json:"viewer_can_close,omitempty"`
	ViewerCanDelete            *bool                  `json:"viewer_can_delete,omitempty"`
	ViewerCanLabel             *bool                  `json:"viewer_can_label,omitempty"`
	ViewerCanReact             *bool                  `json:"viewer_can_react,omitempty"`
	ViewerCanReopen            *bool                  `json:"viewer_can_reopen,omitempty"`
	ViewerCanSubscribe         *bool                  `json:"viewer_can_subscribe,omitempty"`
	ViewerCanUpdate            *bool                  `json:"viewer_can_update,omitempty"`
	ViewerCanUpvote            *bool                  `json:"viewer_can_upvote,omitempty"`
	ViewerDidAuthor            *bool                  `json:"viewer_did_author,omitempty"`
	ViewerHasUpvoted           *bool                  `json:"viewer_has_upvoted,omitempty"`
	ViewerSubscription         *string                `json:"viewer_subscription,omitempty"`
}

func (r *discussionAnswerRaw) project() *DiscussionAnswer {
	if r == nil {
		return nil
	}
	return &DiscussionAnswer{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, URL: address(r.URL),
		Author: r.Author.project(), CreatedAt: gqlName(r.CreatedAt), IsAnswer: r.IsAnswer}
}

func (r *discussionPollRaw) project() *DiscussionPoll {
	if r == nil {
		return nil
	}
	poll := &DiscussionPoll{ID: gqlName(r.ID), Question: gqlText(r.Question), TotalVoteCount: r.TotalVoteCount,
		ViewerCanVote: r.ViewerCanVote, ViewerHasVoted: r.ViewerHasVoted}
	if r.Options != nil {
		shown := 0
		for _, o := range r.Options.Nodes {
			if o == nil {
				continue
			}
			shown++
			poll.Options = append(poll.Options, DiscussionPollOption{ID: gqlName(o.ID), Option: gqlText(o.Option),
				TotalVoteCount: o.TotalVoteCount, ViewerHasVoted: o.ViewerHasVoted})
		}
		poll.OptionsTotalCount = r.Options.TotalCount
		poll.OptionsTruncated = connectionTruncated(r.Options.TotalCount, r.Options.PageInfo.HasNextPage, shown)
	}
	return poll
}

// width maps the members beyond the compact discussion. The labels are the connection n.Labels.
func (r *discussionWidthRaw) width() DiscussionWidth {
	return DiscussionWidth{DatabaseID: r.DatabaseID, AuthorAssociation: gqlName(r.AuthorAssociation),
		Editor: r.Editor.project(), LastEditedAt: gqlName(r.LastEditedAt), PublishedAt: gqlName(r.PublishedAt),
		CreatedViaEmail: r.CreatedViaEmail, IncludesCreatedEdit: r.IncludesCreatedEdit,
		ActiveLockReason: gqlName(r.ActiveLockReason), ClosedAt: gqlName(r.ClosedAt),
		StateReason: gqlName(r.StateReason), Answer: r.Answer.project(), AnswerChosenBy: r.AnswerChosenBy.project(),
		Poll: r.Poll.project(), ReactionGroups: projectGivenReactionGroups(r.ReactionGroups),
		Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath), ReactionsTotalCount: r.Reactions.total(),
		UserContentEditsTotalCount: r.UserContentEdits.total(), ViewerCanClose: r.ViewerCanClose,
		ViewerCanDelete: r.ViewerCanDelete, ViewerCanLabel: r.ViewerCanLabel, ViewerCanReact: r.ViewerCanReact,
		ViewerCanReopen: r.ViewerCanReopen, ViewerCanSubscribe: r.ViewerCanSubscribe,
		ViewerCanUpdate: r.ViewerCanUpdate, ViewerCanUpvote: r.ViewerCanUpvote, ViewerDidAuthor: r.ViewerDidAuthor,
		ViewerHasUpvoted: r.ViewerHasUpvoted, ViewerSubscription: gqlName(r.ViewerSubscription)}
}

const discussionPollOptionSchema = `{"type":"object","properties":{"id":{"type":"string"},` +
	`"option":{"type":"string"},"total_vote_count":{"type":"integer"},"viewer_has_voted":{"type":"boolean"}},` +
	`"additionalProperties":false}`

var discussionAnswerSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
	`"url":{"type":"string"},"author":` + gqlAccountSchema + `,"created_at":{"type":"string"},` +
	`"is_answer":{"type":"boolean"}`)

var discussionPollSchema = objectSchema(`"id":{"type":"string"},"question":{"type":"string"},` +
	`"total_vote_count":{"type":"integer"},"viewer_can_vote":{"type":"boolean"},` +
	`"viewer_has_voted":{"type":"boolean"},"options":{"type":"array","items":` + discussionPollOptionSchema + `},` +
	`"options_total_count":{"type":"integer"},"options_truncated":{"type":"boolean"}`)

// discussionCategoryRefProperties lists the category members a discussion reports.
const discussionCategoryRefProperties = `"id":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},` +
	`"description":{"type":"string"},"emoji":{"type":"string"},"is_answerable":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}`

var discussionWidthProperties = `"database_id":{"type":"integer"},` + detailProperty("author", gqlAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"editor":` + gqlAccountSchema + `,` +
	`"last_edited_at":{"type":"string"},"published_at":{"type":"string"},"created_via_email":{"type":"boolean"},` +
	`"includes_created_edit":{"type":"boolean"},"active_lock_reason":{"type":"string"},` +
	`"closed_at":{"type":"string"},"state_reason":{"type":"string"},"answer":` + discussionAnswerSchema + `,` +
	`"answer_chosen_by":` + gqlAccountSchema + `,"poll":` + discussionPollSchema + `,` +
	detailProperty("labels", gqlLabelSchema, true) + `,"labels_total_count":{"type":"integer"},` +
	`"labels_truncated":{"type":"boolean"},"reaction_groups":{"type":"array","items":` + gqlReactionGroupSchema + `},` +
	`"links":` + linksSchema(gqlResourceLinkKeys) + `,"reactions_total_count":{"type":"integer"},` +
	`"user_content_edits_total_count":{"type":"integer"},` + discussionViewerProperties

const discussionViewerProperties = `"viewer_can_close":{"type":"boolean"},"viewer_can_delete":{"type":"boolean"},` +
	`"viewer_can_label":{"type":"boolean"},"viewer_can_react":{"type":"boolean"},` +
	`"viewer_can_reopen":{"type":"boolean"},"viewer_can_subscribe":{"type":"boolean"},` +
	`"viewer_can_update":{"type":"boolean"},"viewer_can_upvote":{"type":"boolean"},` +
	`"viewer_did_author":{"type":"boolean"},"viewer_has_upvoted":{"type":"boolean"},` +
	`"viewer_subscription":{"type":"string"}`

// discussionWidthFields describes the members of a discussion of a list that the compact discussion lacks.
var discussionWidthFields = []capability.Field{
	{Name: "database_id", Description: "Numeric identifier of the discussion"},
	detailField("author", "Account", false),
	{Name: "author_association", Description: "Relation of the author to the repository such as OWNER, MEMBER, or NONE"},
	{Name: "editor", Description: "Account that last edited the discussion: " + gqlAccountMembers},
	{Name: "last_edited_at", Description: "Time of the last edit"},
	{Name: "published_at", Description: "Time the discussion was published"},
	{Name: "created_via_email", Description: "True when the discussion was created by an email reply"},
	{Name: "includes_created_edit", Description: "True when the edit history includes an edit with the creation data"},
	{Name: "active_lock_reason", Description: "Why the discussion was locked such as RESOLVED or SPAM"},
	{Name: "closed_at", Description: "Time the discussion was closed"},
	{Name: "state_reason", Description: "Why the discussion has its state such as RESOLVED, OUTDATED, or DUPLICATE"},
	{Name: "answer", Description: "Comment chosen as the answer: id, database_id, url, author (an account), " +
		"created_at, and is_answer; the comment itself is read by github.discussion-comments.list"},
	{Name: "answer_chosen_by", Description: "Account that chose the answer: " + gqlAccountMembers},
	{Name: "poll", Description: "Poll of the discussion: id, question (untrusted data), total_vote_count, " +
		"viewer_can_vote, viewer_has_voted, and the first options (id, option, total_vote_count, " +
		"viewer_has_voted) with options_total_count and options_truncated; options are untrusted data"},
	detailField("labels", "Labels", true),
	{Name: "labels_total_count", Description: "Number of all labels of the discussion; labels_details holds the " +
		"first 20: " + gqlLabelMembers},
	{Name: "labels_truncated", Description: "True when the discussion holds more labels than shown"},
	{Name: "reaction_groups", Description: "Reactions by kind, only kinds somebody gave: " + gqlReactionGroupMembers},
	{Name: "links", Description: "resource_path of the discussion, untrusted data, never fetched"},
	{Name: "reactions_total_count", Description: "Number of all reactions on the discussion"},
	{Name: "user_content_edits_total_count", Description: "Number of edits in the edit history of the discussion"},
	{Name: "viewer_can_close", Description: "True when the authenticated user can close the discussion"},
	{Name: "viewer_can_delete", Description: "True when the authenticated user can delete the discussion"},
	{Name: "viewer_can_label", Description: "True when the authenticated user can edit its labels"},
	{Name: "viewer_can_react", Description: "True when the authenticated user can react to the discussion"},
	{Name: "viewer_can_reopen", Description: "True when the authenticated user can reopen the discussion"},
	{Name: "viewer_can_subscribe", Description: "True when the authenticated user can change the subscription"},
	{Name: "viewer_can_update", Description: "True when the authenticated user can update the discussion"},
	{Name: "viewer_can_upvote", Description: "True when the authenticated user can add or remove an upvote"},
	{Name: "viewer_did_author", Description: "True when the authenticated user wrote the discussion"},
	{Name: "viewer_has_upvoted", Description: "True when the authenticated user upvoted the discussion"},
	{Name: "viewer_subscription", Description: "Subscription of the authenticated user: SUBSCRIBED, UNSUBSCRIBED, or IGNORED"},
}

// ---- Comments and replies

// discussionCommentWidthSelection selects the members of a comment or a reply beyond the compact ones.
const discussionCommentWidthSelection = `authorAssociation createdViaEmail editor{` + gqlAccountSelection + `} ` +
	`includesCreatedEdit isMinimized lastEditedAt minimizedReason publishedAt deletedAt replyTo{id} ` +
	`reactionGroups{` + gqlReactionGroupSelection + `} reactions{totalCount} resourcePath userContentEdits{totalCount} ` +
	`viewerCanDelete viewerCanMarkAsAnswer viewerCanMinimize viewerCanReact viewerCanUnmarkAsAnswer ` +
	`viewerCanUnminimize viewerCanUpdate viewerCanUpvote viewerCannotUpdateReasons viewerDidAuthor viewerHasUpvoted`

// discussionCommentSelection is a comment or a reply without its replies.
const discussionCommentSelection = `id databaseId author{` + gqlAccountSelection + `} body isAnswer upvoteCount ` +
	`createdAt updatedAt url ` + discussionCommentWidthSelection

// discussionCommentWidthRaw is the DiscussionComment as discussionCommentWidthSelection asks for it.
type discussionCommentWidthRaw struct {
	AuthorAssociation         *string                `json:"authorAssociation"`
	CreatedViaEmail           *bool                  `json:"createdViaEmail"`
	Editor                    *gqlAccountRaw         `json:"editor"`
	IncludesCreatedEdit       *bool                  `json:"includesCreatedEdit"`
	IsMinimized               *bool                  `json:"isMinimized"`
	LastEditedAt              *string                `json:"lastEditedAt"`
	MinimizedReason           *string                `json:"minimizedReason"`
	PublishedAt               *string                `json:"publishedAt"`
	DeletedAt                 *string                `json:"deletedAt"`
	ReplyTo                   *struct{ ID *string }  `json:"replyTo"`
	ReactionGroups            []*gqlReactionGroupRaw `json:"reactionGroups"`
	Reactions                 *countRaw              `json:"reactions"`
	ResourcePath              *string                `json:"resourcePath"`
	UserContentEdits          *countRaw              `json:"userContentEdits"`
	ViewerCanDelete           *bool                  `json:"viewerCanDelete"`
	ViewerCanMarkAsAnswer     *bool                  `json:"viewerCanMarkAsAnswer"`
	ViewerCanMinimize         *bool                  `json:"viewerCanMinimize"`
	ViewerCanReact            *bool                  `json:"viewerCanReact"`
	ViewerCanUnmarkAsAnswer   *bool                  `json:"viewerCanUnmarkAsAnswer"`
	ViewerCanUnminimize       *bool                  `json:"viewerCanUnminimize"`
	ViewerCanUpdate           *bool                  `json:"viewerCanUpdate"`
	ViewerCanUpvote           *bool                  `json:"viewerCanUpvote"`
	ViewerCannotUpdateReasons []*string              `json:"viewerCannotUpdateReasons"`
	ViewerDidAuthor           *bool                  `json:"viewerDidAuthor"`
	ViewerHasUpvoted          *bool                  `json:"viewerHasUpvoted"`
}

// DiscussionCommentWidth is the width of GitHub's DiscussionComment next to the compact comment or reply.
type DiscussionCommentWidth struct {
	AuthorDetail               *GraphQLAccount        `json:"author_detail,omitempty"`
	AuthorAssociation          *string                `json:"author_association,omitempty"`
	Editor                     *GraphQLAccount        `json:"editor,omitempty"`
	LastEditedAt               *string                `json:"last_edited_at,omitempty"`
	PublishedAt                *string                `json:"published_at,omitempty"`
	CreatedViaEmail            *bool                  `json:"created_via_email,omitempty"`
	IncludesCreatedEdit        *bool                  `json:"includes_created_edit,omitempty"`
	DeletedAt                  *string                `json:"deleted_at,omitempty"`
	IsMinimized                *bool                  `json:"is_minimized,omitempty"`
	MinimizedReason            *string                `json:"minimized_reason,omitempty"`
	ReplyTo                    *string                `json:"reply_to,omitempty"`
	ReactionGroups             []GraphQLReactionGroup `json:"reaction_groups,omitempty"`
	Links                      Links                  `json:"links,omitempty"`
	ReactionsTotalCount        *int64                 `json:"reactions_total_count,omitempty"`
	UserContentEditsTotalCount *int64                 `json:"user_content_edits_total_count,omitempty"`
	ViewerCanDelete            *bool                  `json:"viewer_can_delete,omitempty"`
	ViewerCanMarkAsAnswer      *bool                  `json:"viewer_can_mark_as_answer,omitempty"`
	ViewerCanMinimize          *bool                  `json:"viewer_can_minimize,omitempty"`
	ViewerCanReact             *bool                  `json:"viewer_can_react,omitempty"`
	ViewerCanUnmarkAsAnswer    *bool                  `json:"viewer_can_unmark_as_answer,omitempty"`
	ViewerCanUnminimize        *bool                  `json:"viewer_can_unminimize,omitempty"`
	ViewerCanUpdate            *bool                  `json:"viewer_can_update,omitempty"`
	ViewerCanUpvote            *bool                  `json:"viewer_can_upvote,omitempty"`
	ViewerCannotUpdateReasons  []string               `json:"viewer_cannot_update_reasons,omitempty"`
	ViewerDidAuthor            *bool                  `json:"viewer_did_author,omitempty"`
	ViewerHasUpvoted           *bool                  `json:"viewer_has_upvoted,omitempty"`
}

// width maps the members beyond the compact comment; author is the account of the comment.
func (r *discussionCommentWidthRaw) width(author *gqlAccountRaw) DiscussionCommentWidth {
	w := DiscussionCommentWidth{AuthorDetail: author.project(), AuthorAssociation: gqlName(r.AuthorAssociation),
		Editor: r.Editor.project(), LastEditedAt: gqlName(r.LastEditedAt), PublishedAt: gqlName(r.PublishedAt),
		CreatedViaEmail: r.CreatedViaEmail, IncludesCreatedEdit: r.IncludesCreatedEdit,
		DeletedAt: gqlName(r.DeletedAt), IsMinimized: r.IsMinimized, MinimizedReason: gqlName(r.MinimizedReason),
		ReactionGroups: projectGivenReactionGroups(r.ReactionGroups),
		Links:          gqlLinks(gqlResourceLinkKeys, r.ResourcePath), ReactionsTotalCount: r.Reactions.total(),
		UserContentEditsTotalCount: r.UserContentEdits.total(), ViewerCanDelete: r.ViewerCanDelete,
		ViewerCanMarkAsAnswer: r.ViewerCanMarkAsAnswer, ViewerCanMinimize: r.ViewerCanMinimize,
		ViewerCanReact: r.ViewerCanReact, ViewerCanUnmarkAsAnswer: r.ViewerCanUnmarkAsAnswer,
		ViewerCanUnminimize: r.ViewerCanUnminimize, ViewerCanUpdate: r.ViewerCanUpdate,
		ViewerCanUpvote: r.ViewerCanUpvote, ViewerCannotUpdateReasons: gqlNames(r.ViewerCannotUpdateReasons),
		ViewerDidAuthor: r.ViewerDidAuthor, ViewerHasUpvoted: r.ViewerHasUpvoted}
	if r.ReplyTo != nil {
		w.ReplyTo = gqlName(r.ReplyTo.ID)
	}
	return w
}

var discussionCommentWidthProperties = detailProperty("author", gqlAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"editor":` + gqlAccountSchema + `,` +
	`"last_edited_at":{"type":"string"},"published_at":{"type":"string"},"created_via_email":{"type":"boolean"},` +
	`"includes_created_edit":{"type":"boolean"},"deleted_at":{"type":"string"},"is_minimized":{"type":"boolean"},` +
	`"minimized_reason":{"type":"string"},"reply_to":{"type":"string"},"reaction_groups":{"type":"array","items":` +
	gqlReactionGroupSchema + `},"links":` + linksSchema(gqlResourceLinkKeys) + `,` +
	`"reactions_total_count":{"type":"integer"},"user_content_edits_total_count":{"type":"integer"},` +
	`"viewer_can_delete":{"type":"boolean"},"viewer_can_mark_as_answer":{"type":"boolean"},` +
	`"viewer_can_minimize":{"type":"boolean"},"viewer_can_react":{"type":"boolean"},` +
	`"viewer_can_unmark_as_answer":{"type":"boolean"},"viewer_can_unminimize":{"type":"boolean"},` +
	`"viewer_can_update":{"type":"boolean"},"viewer_can_upvote":{"type":"boolean"},` +
	`"viewer_cannot_update_reasons":{"type":"array","items":{"type":"string"}},` +
	`"viewer_did_author":{"type":"boolean"},"viewer_has_upvoted":{"type":"boolean"}`

// discussionCommentWidthFields describes the members of a comment of a comment list that the compact
// comment lacks; a reply carries the same members.
var discussionCommentWidthFields = []capability.Field{
	detailField("author", "Account", false),
	{Name: "author_association", Description: "Relation of the author to the repository such as OWNER, MEMBER, or NONE"},
	{Name: "editor", Description: "Account that last edited the comment: " + gqlAccountMembers},
	{Name: "last_edited_at", Description: "Time of the last edit"},
	{Name: "published_at", Description: "Time the comment was published"},
	{Name: "created_via_email", Description: "True when the comment was created by an email reply"},
	{Name: "includes_created_edit", Description: "True when the edit history includes an edit with the creation data"},
	{Name: "deleted_at", Description: "Time the comment was deleted, when it was"},
	{Name: "is_minimized", Description: "True when the comment is minimized"},
	{Name: "minimized_reason", Description: "Why the comment was minimized such as abuse or off-topic"},
	{Name: "reply_to", Description: "Id of the comment this comment replies to; absent for a top-level comment"},
	{Name: "reaction_groups", Description: "Reactions by kind, only kinds somebody gave: " + gqlReactionGroupMembers},
	{Name: "links", Description: "resource_path of the comment, untrusted data, never fetched"},
	{Name: "reactions_total_count", Description: "Number of all reactions on the comment"},
	{Name: "user_content_edits_total_count", Description: "Number of edits in the edit history of the comment"},
	{Name: "viewer_can_delete", Description: "True when the authenticated user can delete the comment"},
	{Name: "viewer_can_mark_as_answer", Description: "True when the authenticated user can mark the comment as the answer"},
	{Name: "viewer_can_minimize", Description: "True when the authenticated user can minimize the comment"},
	{Name: "viewer_can_react", Description: "True when the authenticated user can react to the comment"},
	{Name: "viewer_can_unmark_as_answer", Description: "True when the authenticated user can unmark the comment as the answer"},
	{Name: "viewer_can_unminimize", Description: "True when the authenticated user can unminimize the comment"},
	{Name: "viewer_can_update", Description: "True when the authenticated user can update the comment"},
	{Name: "viewer_can_upvote", Description: "True when the authenticated user can add or remove an upvote"},
	{Name: "viewer_cannot_update_reasons", Description: "Reasons the authenticated user cannot update the comment"},
	{Name: "viewer_did_author", Description: "True when the authenticated user wrote the comment"},
	{Name: "viewer_has_upvoted", Description: "True when the authenticated user upvoted the comment"},
}
