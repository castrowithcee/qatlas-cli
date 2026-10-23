package github

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The comment width: the members of GitHub's IssueComment the two comment list tools answer beyond the
// compact view. The issue, pull request, and repository of a comment are left out, since the call names
// them; bodyHTML and bodyText repeat the body; the reactions and the edit history are counted only, since
// GitHub serves them as lists of their own.

const commentSelection = `id databaseId fullDatabaseId author{` + gqlAccountSelection + `} authorAssociation ` +
	`body createdAt createdViaEmail editor{` + gqlAccountSelection + `} includesCreatedEdit isMinimized isPinned ` +
	`lastEditedAt minimizedReason pinnedAt pinnedBy{` + gqlUserSelection + `} publishedAt ` +
	`reactionGroups{` + gqlReactionGroupSelection + `} reactions{totalCount} resourcePath updatedAt url ` +
	`userContentEdits{totalCount} viewerCanDelete viewerCanMinimize viewerCanPin viewerCanReact ` +
	`viewerCanUnminimize viewerCanUnpin viewerCanUpdate viewerCannotUpdateReasons viewerDidAuthor`

type countRaw struct {
	TotalCount *int64 `json:"totalCount"`
}

func (c *countRaw) total() *int64 {
	if c == nil {
		return nil
	}
	return c.TotalCount
}

// issueCommentNodeJSON is one IssueComment as commentSelection asks for it.
type issueCommentNodeJSON struct {
	ID                        string                 `json:"id"`
	DatabaseID                int64                  `json:"databaseId"`
	FullDatabaseID            *gqlBigInt             `json:"fullDatabaseId"`
	Author                    *gqlAccountRaw         `json:"author"`
	AuthorAssociation         *string                `json:"authorAssociation"`
	Body                      string                 `json:"body"`
	CreatedAt                 string                 `json:"createdAt"`
	CreatedViaEmail           *bool                  `json:"createdViaEmail"`
	Editor                    *gqlAccountRaw         `json:"editor"`
	IncludesCreatedEdit       *bool                  `json:"includesCreatedEdit"`
	IsMinimized               *bool                  `json:"isMinimized"`
	IsPinned                  *bool                  `json:"isPinned"`
	LastEditedAt              *string                `json:"lastEditedAt"`
	MinimizedReason           *string                `json:"minimizedReason"`
	PinnedAt                  *string                `json:"pinnedAt"`
	PinnedBy                  *gqlAccountRaw         `json:"pinnedBy"`
	PublishedAt               *string                `json:"publishedAt"`
	ReactionGroups            []*gqlReactionGroupRaw `json:"reactionGroups"`
	Reactions                 *countRaw              `json:"reactions"`
	ResourcePath              *string                `json:"resourcePath"`
	UpdatedAt                 string                 `json:"updatedAt"`
	URL                       string                 `json:"url"`
	UserContentEdits          *countRaw              `json:"userContentEdits"`
	ViewerCanDelete           *bool                  `json:"viewerCanDelete"`
	ViewerCanMinimize         *bool                  `json:"viewerCanMinimize"`
	ViewerCanPin              *bool                  `json:"viewerCanPin"`
	ViewerCanReact            *bool                  `json:"viewerCanReact"`
	ViewerCanUnminimize       *bool                  `json:"viewerCanUnminimize"`
	ViewerCanUnpin            *bool                  `json:"viewerCanUnpin"`
	ViewerCanUpdate           *bool                  `json:"viewerCanUpdate"`
	ViewerCannotUpdateReasons []*string              `json:"viewerCannotUpdateReasons"`
	ViewerDidAuthor           *bool                  `json:"viewerDidAuthor"`
}

// ListedComment is one comment of a comment list: the compact comment and the width of GitHub's IssueComment.
// The compact members keep their treatment; the others pass through the shared GraphQL projections.
type ListedComment struct {
	Comment
	AuthorDetail               *GraphQLAccount        `json:"author_detail,omitempty"`
	AuthorAssociation          *string                `json:"author_association,omitempty"`
	Editor                     *GraphQLAccount        `json:"editor,omitempty"`
	LastEditedAt               *string                `json:"last_edited_at,omitempty"`
	PublishedAt                *string                `json:"published_at,omitempty"`
	CreatedViaEmail            *bool                  `json:"created_via_email,omitempty"`
	IncludesCreatedEdit        *bool                  `json:"includes_created_edit,omitempty"`
	FullDatabaseID             *string                `json:"full_database_id,omitempty"`
	IsMinimized                *bool                  `json:"is_minimized,omitempty"`
	MinimizedReason            *string                `json:"minimized_reason,omitempty"`
	IsPinned                   *bool                  `json:"is_pinned,omitempty"`
	PinnedAt                   *string                `json:"pinned_at,omitempty"`
	PinnedBy                   *GraphQLAccount        `json:"pinned_by,omitempty"`
	ReactionGroups             []GraphQLReactionGroup `json:"reaction_groups,omitempty"`
	Links                      Links                  `json:"links,omitempty"`
	ViewerCanDelete            *bool                  `json:"viewer_can_delete,omitempty"`
	ViewerCanMinimize          *bool                  `json:"viewer_can_minimize,omitempty"`
	ViewerCanPin               *bool                  `json:"viewer_can_pin,omitempty"`
	ViewerCanReact             *bool                  `json:"viewer_can_react,omitempty"`
	ViewerCanUnminimize        *bool                  `json:"viewer_can_unminimize,omitempty"`
	ViewerCanUnpin             *bool                  `json:"viewer_can_unpin,omitempty"`
	ViewerCanUpdate            *bool                  `json:"viewer_can_update,omitempty"`
	ViewerCannotUpdateReasons  []string               `json:"viewer_cannot_update_reasons,omitempty"`
	ViewerDidAuthor            *bool                  `json:"viewer_did_author,omitempty"`
	ReactionsTotalCount        *int64                 `json:"reactions_total_count,omitempty"`
	UserContentEditsTotalCount *int64                 `json:"user_content_edits_total_count,omitempty"`
}

// projectGivenReactionGroups maps the reaction groups and leaves out a group nobody gave.
func projectGivenReactionGroups(raw []*gqlReactionGroupRaw) []GraphQLReactionGroup {
	var groups []GraphQLReactionGroup
	for _, group := range raw {
		if group != nil && group.Reactors != nil && group.Reactors.TotalCount != nil && *group.Reactors.TotalCount == 0 {
			continue
		}
		if g := group.project(); g != nil {
			groups = append(groups, *g)
		}
	}
	return groups
}

// gqlNames bounds a list of enum names and leaves out empty ones.
func gqlNames(raw []*string) []string {
	var names []string
	for _, name := range raw {
		if n := gqlName(name); n != nil {
			names = append(names, *n)
		}
	}
	return names
}

// project maps one node. A reaction group nobody gave says nothing and stays out.
func (n *issueCommentNodeJSON) project() ListedComment {
	comment := ListedComment{
		Comment: Comment{ID: n.ID, DatabaseID: n.DatabaseID, Body: n.Body, CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt, URL: n.URL},
		AuthorDetail: n.Author.project(), AuthorAssociation: gqlName(n.AuthorAssociation),
		Editor: n.Editor.project(), LastEditedAt: gqlName(n.LastEditedAt), PublishedAt: gqlName(n.PublishedAt),
		CreatedViaEmail: n.CreatedViaEmail, IncludesCreatedEdit: n.IncludesCreatedEdit,
		FullDatabaseID: n.FullDatabaseID.text(), IsMinimized: n.IsMinimized,
		MinimizedReason: gqlName(n.MinimizedReason), IsPinned: n.IsPinned, PinnedAt: gqlName(n.PinnedAt),
		PinnedBy: n.PinnedBy.project(), Links: gqlLinks(gqlResourceLinkKeys, n.ResourcePath),
		ViewerCanDelete: n.ViewerCanDelete, ViewerCanMinimize: n.ViewerCanMinimize, ViewerCanPin: n.ViewerCanPin,
		ViewerCanReact: n.ViewerCanReact, ViewerCanUnminimize: n.ViewerCanUnminimize,
		ViewerCanUnpin: n.ViewerCanUnpin, ViewerCanUpdate: n.ViewerCanUpdate, ViewerDidAuthor: n.ViewerDidAuthor,
		ReactionsTotalCount: n.Reactions.total(), UserContentEditsTotalCount: n.UserContentEdits.total(),
	}
	if n.Author != nil && n.Author.Login != nil {
		comment.Author = *n.Author.Login
	}
	comment.ReactionGroups = projectGivenReactionGroups(n.ReactionGroups)
	comment.ViewerCannotUpdateReasons = gqlNames(n.ViewerCannotUpdateReasons)
	return comment
}

// listedCommentProperties are the schema properties of one comment of a comment list.
var listedCommentProperties = commentProperties + `,` + detailProperty("author", gqlAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"editor":` + gqlAccountSchema + `,` +
	`"last_edited_at":{"type":"string"},"published_at":{"type":"string"},"created_via_email":{"type":"boolean"},` +
	`"includes_created_edit":{"type":"boolean"},"full_database_id":{"type":"string"},` +
	`"is_minimized":{"type":"boolean"},"minimized_reason":{"type":"string"},"is_pinned":{"type":"boolean"},` +
	`"pinned_at":{"type":"string"},"pinned_by":` + gqlAccountSchema + `,` +
	`"reaction_groups":{"type":"array","items":` + gqlReactionGroupSchema + `},` +
	`"links":` + linksSchema(gqlResourceLinkKeys) + `,"viewer_can_delete":{"type":"boolean"},` +
	`"viewer_can_minimize":{"type":"boolean"},"viewer_can_pin":{"type":"boolean"},` +
	`"viewer_can_react":{"type":"boolean"},"viewer_can_unminimize":{"type":"boolean"},` +
	`"viewer_can_unpin":{"type":"boolean"},"viewer_can_update":{"type":"boolean"},` +
	`"viewer_cannot_update_reasons":{"type":"array","items":{"type":"string"}},` +
	`"viewer_did_author":{"type":"boolean"},"reactions_total_count":{"type":"integer"},` +
	`"user_content_edits_total_count":{"type":"integer"}`

// listedCommentFields describes the members of one comment of a comment list that the compact comment lacks.
var listedCommentFields = []capability.Field{
	detailField("author", "Account", false),
	{Name: "author_association", Description: "Relation of the author to the repository such as OWNER, MEMBER, or NONE"},
	{Name: "editor", Description: "Account that last edited the comment: " + gqlAccountMembers},
	{Name: "last_edited_at", Description: "Time of the last edit"},
	{Name: "published_at", Description: "Time the comment was published"},
	{Name: "created_via_email", Description: "True when the comment was created by an email reply"},
	{Name: "includes_created_edit", Description: "True when the edit history includes an edit with the creation data"},
	{Name: "full_database_id", Description: "Numeric identifier of the comment as digits, for values beyond 53 bits"},
	{Name: "is_minimized", Description: "True when the comment is minimized"},
	{Name: "minimized_reason", Description: "Why the comment was minimized such as abuse or off-topic"},
	{Name: "is_pinned", Description: "True when the comment is pinned"},
	{Name: "pinned_at", Description: "Time the comment was pinned"},
	{Name: "pinned_by", Description: "User that pinned the comment: " + gqlAccountMembers},
	{Name: "reaction_groups", Description: "Reactions by kind, only kinds somebody gave: " + gqlReactionGroupMembers},
	{Name: "links", Description: "resource_path of the comment, untrusted data, never fetched"},
	{Name: "viewer_can_delete", Description: "True when the authenticated user can delete the comment"},
	{Name: "viewer_can_minimize", Description: "True when the authenticated user can minimize the comment"},
	{Name: "viewer_can_pin", Description: "True when the authenticated user can pin the comment"},
	{Name: "viewer_can_react", Description: "True when the authenticated user can react to the comment"},
	{Name: "viewer_can_unminimize", Description: "True when the authenticated user can unminimize the comment"},
	{Name: "viewer_can_unpin", Description: "True when the authenticated user can unpin the comment"},
	{Name: "viewer_can_update", Description: "True when the authenticated user can update the comment"},
	{Name: "viewer_cannot_update_reasons", Description: "Reasons the authenticated user cannot update the comment"},
	{Name: "viewer_did_author", Description: "True when the authenticated user wrote the comment"},
	{Name: "reactions_total_count", Description: "Number of all reactions on the comment"},
	{Name: "user_content_edits_total_count", Description: "Number of edits in the edit history of the comment"},
}
