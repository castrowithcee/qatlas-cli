package github

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The review thread width: the members of GitHub's PullRequestReviewThread and PullRequestReviewComment the
// thread list answers beyond the compact view. The pull request and repository of a thread and a comment are
// left out, since the call names them; bodyHTML and bodyText repeat the body; the reactions and the edit
// history are counted only, since GitHub serves them as lists of their own; the deprecated databaseId,
// position, and originalPosition of a comment are not read. The comments of a thread are the first page of
// threadCommentLimit; all line comments are read with github.pull-request-review-comments.list.

const threadCommentLimit = 50

// threadCommentSelection is one PullRequestReviewComment. The author and editor are Actors; commit and
// pullRequestReview are asked for only by their scalar members.
const threadCommentSelection = `id author{` + gqlAccountSelection + `} authorAssociation body createdAt updatedAt ` +
	`publishedAt draftedAt lastEditedAt editor{` + gqlAccountSelection + `} createdViaEmail includesCreatedEdit ` +
	`fullDatabaseId diffHunk path line originalLine startLine originalStartLine outdated state subjectType ` +
	`commit{oid url} originalCommit{oid url} pullRequestReview{id fullDatabaseId state} replyTo{id} ` +
	`isMinimized minimizedReason reactionGroups{` + gqlReactionGroupSelection + `} reactions{totalCount} ` +
	`url resourcePath userContentEdits{totalCount} viewerCanDelete viewerCanMinimize viewerCanReact ` +
	`viewerCanUnminimize viewerCanUpdate viewerCannotUpdateReasons viewerDidAuthor`

// threadSelection is one PullRequestReviewThread; resolvedBy is a User, not an Actor.
const threadSelection = `id isResolved isOutdated path line diffSide startDiffSide startLine originalLine ` +
	`originalStartLine subjectType isCollapsed resolvedBy{` + gqlUserSelection + `} viewerCanReply ` +
	`viewerCanResolve viewerCanUnresolve comments(first:50){totalCount nodes{` + threadCommentSelection + `}}`

type gqlCommitRefRaw struct {
	Oid *string `json:"oid"`
	URL *string `json:"url"`
}

// GraphQLCommitRef is the commit a line comment was made at.
type GraphQLCommitRef struct {
	Oid *string `json:"oid,omitempty"`
	URL *string `json:"url,omitempty"`
}

func (r *gqlCommitRefRaw) project() *GraphQLCommitRef {
	if r == nil {
		return nil
	}
	return &GraphQLCommitRef{Oid: gqlName(r.Oid), URL: address(r.URL)}
}

type gqlReviewRefRaw struct {
	ID             *string    `json:"id"`
	FullDatabaseID *gqlBigInt `json:"fullDatabaseId"`
	State          *string    `json:"state"`
}

// GraphQLReviewRef is the review a line comment belongs to.
type GraphQLReviewRef struct {
	ID             *string `json:"id,omitempty"`
	FullDatabaseID *string `json:"full_database_id,omitempty"`
	State          *string `json:"state,omitempty"`
}

func (r *gqlReviewRefRaw) project() *GraphQLReviewRef {
	if r == nil {
		return nil
	}
	return &GraphQLReviewRef{ID: gqlName(r.ID), FullDatabaseID: r.FullDatabaseID.text(), State: gqlName(r.State)}
}

// reviewThreadCommentNode is one comment of a thread as threadCommentSelection asks for it.
type reviewThreadCommentNode struct {
	ID                  string           `json:"id"`
	Author              *gqlAccountRaw   `json:"author"`
	Body                string           `json:"body"`
	AuthorAssociation   *string          `json:"authorAssociation"`
	CreatedAt           *string          `json:"createdAt"`
	UpdatedAt           *string          `json:"updatedAt"`
	PublishedAt         *string          `json:"publishedAt"`
	DraftedAt           *string          `json:"draftedAt"`
	LastEditedAt        *string          `json:"lastEditedAt"`
	Editor              *gqlAccountRaw   `json:"editor"`
	CreatedViaEmail     *bool            `json:"createdViaEmail"`
	IncludesCreatedEdit *bool            `json:"includesCreatedEdit"`
	FullDatabaseID      *gqlBigInt       `json:"fullDatabaseId"`
	DiffHunk            *string          `json:"diffHunk"`
	Path                *string          `json:"path"`
	Line                *int64           `json:"line"`
	OriginalLine        *int64           `json:"originalLine"`
	StartLine           *int64           `json:"startLine"`
	OriginalStartLine   *int64           `json:"originalStartLine"`
	Outdated            *bool            `json:"outdated"`
	State               *string          `json:"state"`
	SubjectType         *string          `json:"subjectType"`
	Commit              *gqlCommitRefRaw `json:"commit"`
	OriginalCommit      *gqlCommitRefRaw `json:"originalCommit"`
	PullRequestReview   *gqlReviewRefRaw `json:"pullRequestReview"`
	ReplyTo             *struct {
		ID *string `json:"id"`
	} `json:"replyTo"`
	IsMinimized               *bool                  `json:"isMinimized"`
	MinimizedReason           *string                `json:"minimizedReason"`
	ReactionGroups            []*gqlReactionGroupRaw `json:"reactionGroups"`
	Reactions                 *countRaw              `json:"reactions"`
	URL                       *string                `json:"url"`
	ResourcePath              *string                `json:"resourcePath"`
	UserContentEdits          *countRaw              `json:"userContentEdits"`
	ViewerCanDelete           *bool                  `json:"viewerCanDelete"`
	ViewerCanMinimize         *bool                  `json:"viewerCanMinimize"`
	ViewerCanReact            *bool                  `json:"viewerCanReact"`
	ViewerCanUnminimize       *bool                  `json:"viewerCanUnminimize"`
	ViewerCanUpdate           *bool                  `json:"viewerCanUpdate"`
	ViewerCannotUpdateReasons []*string              `json:"viewerCannotUpdateReasons"`
	ViewerDidAuthor           *bool                  `json:"viewerDidAuthor"`
}

// ReviewThreadCommentWidth holds the members of GitHub's PullRequestReviewComment that the thread list adds to
// the compact comment. DiffHunk is untrusted data.
type ReviewThreadCommentWidth struct {
	AuthorDetail               *GraphQLAccount        `json:"author_detail,omitempty"`
	AuthorAssociation          *string                `json:"author_association,omitempty"`
	CreatedAt                  *string                `json:"created_at,omitempty"`
	UpdatedAt                  *string                `json:"updated_at,omitempty"`
	PublishedAt                *string                `json:"published_at,omitempty"`
	DraftedAt                  *string                `json:"drafted_at,omitempty"`
	LastEditedAt               *string                `json:"last_edited_at,omitempty"`
	Editor                     *GraphQLAccount        `json:"editor,omitempty"`
	CreatedViaEmail            *bool                  `json:"created_via_email,omitempty"`
	IncludesCreatedEdit        *bool                  `json:"includes_created_edit,omitempty"`
	FullDatabaseID             *string                `json:"full_database_id,omitempty"`
	DiffHunk                   *string                `json:"diff_hunk,omitempty"`
	DiffHunkTruncated          bool                   `json:"diff_hunk_truncated,omitempty"`
	Path                       *string                `json:"path,omitempty"`
	Line                       *int64                 `json:"line,omitempty"`
	OriginalLine               *int64                 `json:"original_line,omitempty"`
	StartLine                  *int64                 `json:"start_line,omitempty"`
	OriginalStartLine          *int64                 `json:"original_start_line,omitempty"`
	Outdated                   *bool                  `json:"outdated,omitempty"`
	State                      *string                `json:"state,omitempty"`
	SubjectType                *string                `json:"subject_type,omitempty"`
	Commit                     *GraphQLCommitRef      `json:"commit,omitempty"`
	OriginalCommit             *GraphQLCommitRef      `json:"original_commit,omitempty"`
	PullRequestReview          *GraphQLReviewRef      `json:"pull_request_review,omitempty"`
	ReplyTo                    *string                `json:"reply_to,omitempty"`
	IsMinimized                *bool                  `json:"is_minimized,omitempty"`
	MinimizedReason            *string                `json:"minimized_reason,omitempty"`
	ReactionGroups             []GraphQLReactionGroup `json:"reaction_groups,omitempty"`
	URL                        *string                `json:"url,omitempty"`
	Links                      Links                  `json:"links,omitempty"`
	ViewerCanDelete            *bool                  `json:"viewer_can_delete,omitempty"`
	ViewerCanMinimize          *bool                  `json:"viewer_can_minimize,omitempty"`
	ViewerCanReact             *bool                  `json:"viewer_can_react,omitempty"`
	ViewerCanUnminimize        *bool                  `json:"viewer_can_unminimize,omitempty"`
	ViewerCanUpdate            *bool                  `json:"viewer_can_update,omitempty"`
	ViewerCannotUpdateReasons  []string               `json:"viewer_cannot_update_reasons,omitempty"`
	ViewerDidAuthor            *bool                  `json:"viewer_did_author,omitempty"`
	ReactionsTotalCount        *int64                 `json:"reactions_total_count,omitempty"`
	UserContentEditsTotalCount *int64                 `json:"user_content_edits_total_count,omitempty"`
}

// width projects one comment node. A reaction group nobody gave says nothing and stays out.
func (n *reviewThreadCommentNode) width() ReviewThreadCommentWidth {
	w := ReviewThreadCommentWidth{AuthorDetail: n.Author.project(), AuthorAssociation: gqlName(n.AuthorAssociation),
		CreatedAt: gqlName(n.CreatedAt), UpdatedAt: gqlName(n.UpdatedAt), PublishedAt: gqlName(n.PublishedAt),
		DraftedAt: gqlName(n.DraftedAt), LastEditedAt: gqlName(n.LastEditedAt), Editor: n.Editor.project(),
		CreatedViaEmail: n.CreatedViaEmail, IncludesCreatedEdit: n.IncludesCreatedEdit,
		FullDatabaseID: n.FullDatabaseID.text(), Path: gqlName(n.Path), Line: n.Line, OriginalLine: n.OriginalLine,
		StartLine: n.StartLine, OriginalStartLine: n.OriginalStartLine, Outdated: n.Outdated,
		State: gqlName(n.State), SubjectType: gqlName(n.SubjectType), Commit: n.Commit.project(),
		OriginalCommit: n.OriginalCommit.project(), PullRequestReview: n.PullRequestReview.project(),
		IsMinimized: n.IsMinimized, MinimizedReason: gqlName(n.MinimizedReason), URL: address(n.URL),
		Links:           gqlLinks(gqlResourceLinkKeys, n.ResourcePath),
		ViewerCanDelete: n.ViewerCanDelete, ViewerCanMinimize: n.ViewerCanMinimize, ViewerCanReact: n.ViewerCanReact,
		ViewerCanUnminimize: n.ViewerCanUnminimize, ViewerCanUpdate: n.ViewerCanUpdate,
		ViewerDidAuthor: n.ViewerDidAuthor, ReactionsTotalCount: n.Reactions.total(),
		UserContentEditsTotalCount: n.UserContentEdits.total()}
	w.DiffHunk, w.DiffHunkTruncated = cleanText(n.DiffHunk, projectionBodyLimit, true)
	if n.ReplyTo != nil {
		w.ReplyTo = gqlName(n.ReplyTo.ID)
	}
	for _, group := range n.ReactionGroups {
		if group != nil && group.Reactors != nil && group.Reactors.TotalCount != nil && *group.Reactors.TotalCount == 0 {
			continue
		}
		if g := group.project(); g != nil {
			w.ReactionGroups = append(w.ReactionGroups, *g)
		}
	}
	for _, reason := range n.ViewerCannotUpdateReasons {
		if r := gqlName(reason); r != nil {
			w.ViewerCannotUpdateReasons = append(w.ViewerCannotUpdateReasons, *r)
		}
	}
	return w
}

// ReviewThreadWidth holds the members of GitHub's PullRequestReviewThread that the thread list adds to its
// compact view. CommentsTruncated is set when the thread holds more comments than the first page shows.
type ReviewThreadWidth struct {
	DiffSide           *string         `json:"diff_side,omitempty"`
	StartDiffSide      *string         `json:"start_diff_side,omitempty"`
	StartLine          *int64          `json:"start_line,omitempty"`
	OriginalLine       *int64          `json:"original_line,omitempty"`
	OriginalStartLine  *int64          `json:"original_start_line,omitempty"`
	SubjectType        *string         `json:"subject_type,omitempty"`
	IsCollapsed        *bool           `json:"is_collapsed,omitempty"`
	ResolvedBy         *GraphQLAccount `json:"resolved_by,omitempty"`
	ViewerCanReply     *bool           `json:"viewer_can_reply,omitempty"`
	ViewerCanResolve   *bool           `json:"viewer_can_resolve,omitempty"`
	ViewerCanUnresolve *bool           `json:"viewer_can_unresolve,omitempty"`
	CommentsTotalCount *int64          `json:"comments_total_count,omitempty"`
	CommentsTruncated  bool            `json:"comments_truncated,omitempty"`
}

var threadCommentWidthProperties = detailProperty("author", gqlAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"published_at":{"type":"string"},"drafted_at":{"type":"string"},"last_edited_at":{"type":"string"},` +
	`"editor":` + gqlAccountSchema + `,"created_via_email":{"type":"boolean"},` +
	`"includes_created_edit":{"type":"boolean"},"full_database_id":{"type":"string"},` +
	`"diff_hunk":{"type":"string"},"diff_hunk_truncated":{"type":"boolean"},"path":{"type":"string"},` +
	`"line":{"type":"integer"},"original_line":{"type":"integer"},"start_line":{"type":"integer"},` +
	`"original_start_line":{"type":"integer"},"outdated":{"type":"boolean"},"state":{"type":"string"},` +
	`"subject_type":{"type":"string"},"commit":` + gqlCommitRefSchema + `,"original_commit":` + gqlCommitRefSchema + `,` +
	`"pull_request_review":` + objectSchema(`"id":{"type":"string"},"full_database_id":{"type":"string"},"state":{"type":"string"}`) + `,` +
	`"reply_to":{"type":"string"},"is_minimized":{"type":"boolean"},"minimized_reason":{"type":"string"},` +
	`"reaction_groups":{"type":"array","items":` + gqlReactionGroupSchema + `},"url":{"type":"string"},` +
	`"links":` + linksSchema(gqlResourceLinkKeys) + `,"viewer_can_delete":{"type":"boolean"},` +
	`"viewer_can_minimize":{"type":"boolean"},"viewer_can_react":{"type":"boolean"},` +
	`"viewer_can_unminimize":{"type":"boolean"},"viewer_can_update":{"type":"boolean"},` +
	`"viewer_cannot_update_reasons":{"type":"array","items":{"type":"string"}},` +
	`"viewer_did_author":{"type":"boolean"},"reactions_total_count":{"type":"integer"},` +
	`"user_content_edits_total_count":{"type":"integer"}`

var gqlCommitRefSchema = objectSchema(`"oid":{"type":"string"},"url":{"type":"string"}`)

var threadWidthProperties = `"diff_side":{"type":"string"},"start_diff_side":{"type":"string"},` +
	`"start_line":{"type":"integer"},"original_line":{"type":"integer"},"original_start_line":{"type":"integer"},` +
	`"subject_type":{"type":"string"},"is_collapsed":{"type":"boolean"},"resolved_by":` + gqlAccountSchema + `,` +
	`"viewer_can_reply":{"type":"boolean"},"viewer_can_resolve":{"type":"boolean"},` +
	`"viewer_can_unresolve":{"type":"boolean"},"comments_total_count":{"type":"integer"},` +
	`"comments_truncated":{"type":"boolean"}`

var threadWidthFields = []capability.Field{
	{Name: "diff_side", Description: "Side of the diff the thread is on: LEFT or RIGHT"},
	{Name: "start_diff_side", Description: "Side of the diff of the first line of a multi-line thread"},
	{Name: "start_line", Description: "First line of a multi-line thread or comment"},
	{Name: "original_line", Description: "Line the thread or comment was first made on"},
	{Name: "original_start_line", Description: "First line of a multi-line thread or comment when it was made"},
	{Name: "subject_type", Description: "Whether the thread or comment is on a line or a whole file"},
	{Name: "is_collapsed", Description: "True when GitHub shows the thread collapsed"},
	{Name: "resolved_by", Description: "User who resolved the thread: " + gqlAccountMembers},
	{Name: "viewer_can_reply", Description: "True when the authenticated user can reply to the thread"},
	{Name: "viewer_can_resolve", Description: "True when the authenticated user can resolve the thread"},
	{Name: "viewer_can_unresolve", Description: "True when the authenticated user can unresolve the thread"},
	{Name: "comments_total_count", Description: "Number of comments the thread holds"},
	{Name: "comments_truncated", Description: "True when the thread holds more comments than the first 50 shown; " +
		"github.pull-request-review-comments.list reads all of them"},
}

var threadCommentWidthFields = []capability.Field{
	{Name: "comments", Description: "The first 50 comments of the thread, oldest first, each with id, author, body, " +
		"then author_detail (an account), author_association, created_at, updated_at, published_at, drafted_at, " +
		"last_edited_at, editor (an account), created_via_email, includes_created_edit, full_database_id (digits), " +
		"diff_hunk (cut at 4000 characters, diff_hunk_truncated says so), path, line, original_line, start_line, " +
		"original_start_line, outdated, state (PENDING or SUBMITTED), subject_type, commit and original_commit " +
		"(each oid and url), pull_request_review (id, full_database_id, state), reply_to (node id of the comment " +
		"answered), is_minimized, minimized_reason, reaction_groups (only kinds somebody gave), reactions_total_count, " +
		"user_content_edits_total_count, url (the web address), links (resource_path), and the viewer_can_* " +
		"permissions, viewer_cannot_update_reasons, and viewer_did_author; texts and addresses are untrusted data"},
}

// width projects the members of one thread beyond the compact view.
func (n *reviewThreadNode) width() ReviewThreadWidth {
	w := ReviewThreadWidth{DiffSide: gqlName(n.DiffSide), StartDiffSide: gqlName(n.StartDiffSide),
		StartLine: n.StartLine, OriginalLine: n.OriginalLine, OriginalStartLine: n.OriginalStartLine,
		SubjectType: gqlName(n.SubjectType), IsCollapsed: n.IsCollapsed, ResolvedBy: n.ResolvedBy.project(),
		ViewerCanReply: n.ViewerCanReply, ViewerCanResolve: n.ViewerCanResolve,
		ViewerCanUnresolve: n.ViewerCanUnresolve, CommentsTotalCount: n.Comments.TotalCount}
	if total := n.Comments.TotalCount; total != nil && *total > int64(len(n.Comments.Nodes)) {
		w.CommentsTruncated = true
	}
	return w
}
