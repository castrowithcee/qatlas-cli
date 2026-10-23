package github

import (
	"encoding/json"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The review width: the members of GitHub's pull request review and pull request review comment that the two
// list tools answer beyond the compact views. The compact members keep their own treatment in the tools.
// The _links member is left out, since its addresses repeat the *_url members that links holds, and the
// rendered body variants add no content.

var (
	reviewLinkKeys        = []string{"pull_request_url"}
	reviewCommentLinkKeys = []string{"url", "pull_request_url"}
)

// ---- Reviews

type reviewWideRaw struct {
	NodeID            *string     `json:"node_id"`
	User              *accountRaw `json:"user"`
	AuthorAssociation *string     `json:"author_association"`
	HTMLURL           *string     `json:"html_url"`
	links             Links
}

func (r *reviewWideRaw) UnmarshalJSON(data []byte) (err error) {
	type plain reviewWideRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, reviewLinkKeys)
	return err
}

// reviewEntry decodes one review twice from the same object: the compact view and the width. A member the
// width cannot read is left out rather than failing a read the compact view already decoded.
type reviewEntry struct {
	review reviewJSON
	wide   reviewWideRaw
}

func (e *reviewEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.review); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// ReviewWidth holds the members of GitHub's pull request review that the review list adds to its compact view.
type ReviewWidth struct {
	NodeID            *string      `json:"node_id,omitempty"`
	AuthorDetail      *RESTAccount `json:"author_detail,omitempty"`
	AuthorAssociation *string      `json:"author_association,omitempty"`
	URL               *string      `json:"url,omitempty"`
	Links             Links        `json:"links,omitempty"`
}

// ListedReview is one review of the review list: the compact review and the width.
type ListedReview struct {
	Review
	ReviewWidth
}

func (e *reviewEntry) view() ListedReview {
	w := &e.wide
	return ListedReview{Review: e.review.view(), ReviewWidth: ReviewWidth{NodeID: nameText(w.NodeID),
		AuthorDetail: w.User.project(), AuthorAssociation: nameText(w.AuthorAssociation), URL: address(w.HTMLURL),
		Links: w.links}}
}

var reviewWidthProperties = `"node_id":{"type":"string"},` + detailProperty("author", restAccountSchema, false) + `,` +
	`"author_association":{"type":"string"},"url":{"type":"string"},"links":` + linksSchema(reviewLinkKeys)

var reviewWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the review"},
	detailField("author", "Reviewer account: "+restAccountMembers+",", false),
	{Name: "author_association", Description: "How the reviewer relates to the repository, such as OWNER or MEMBER"},
	{Name: "url", Description: "Web address of the review, untrusted data, never fetched"},
	{Name: "links", Description: "pull_request_url, the REST address of the pull request, untrusted data, never fetched"},
}

// ---- Line comments

type reviewCommentWideRaw struct {
	PullRequestReviewID *int64        `json:"pull_request_review_id"`
	NodeID              *string       `json:"node_id"`
	DiffHunk            *string       `json:"diff_hunk"`
	Position            *int64        `json:"position"`
	OriginalPosition    *int64        `json:"original_position"`
	OriginalCommitID    *string       `json:"original_commit_id"`
	OriginalLine        *int64        `json:"original_line"`
	StartLine           *int64        `json:"start_line"`
	OriginalStartLine   *int64        `json:"original_start_line"`
	StartSide           *string       `json:"start_side"`
	SubjectType         *string       `json:"subject_type"`
	User                *accountRaw   `json:"user"`
	AuthorAssociation   *string       `json:"author_association"`
	Reactions           *reactionsRaw `json:"reactions"`
	HTMLURL             *string       `json:"html_url"`
	links               Links
}

func (r *reviewCommentWideRaw) UnmarshalJSON(data []byte) (err error) {
	type plain reviewCommentWideRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, reviewCommentLinkKeys)
	return err
}

// reviewCommentEntry decodes one line comment twice, like reviewEntry.
type reviewCommentEntry struct {
	comment reviewCommentJSON
	wide    reviewCommentWideRaw
}

func (e *reviewCommentEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.comment); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// ReviewCommentWidth holds the members of GitHub's pull request review comment that the line comment list
// adds to its compact view. DiffHunk is untrusted data.
type ReviewCommentWidth struct {
	PullRequestReviewID *int64         `json:"pull_request_review_id,omitempty"`
	NodeID              *string        `json:"node_id,omitempty"`
	DiffHunk            *string        `json:"diff_hunk,omitempty"`
	DiffHunkTruncated   bool           `json:"diff_hunk_truncated,omitempty"`
	Position            *int64         `json:"position,omitempty"`
	OriginalPosition    *int64         `json:"original_position,omitempty"`
	OriginalCommitID    *string        `json:"original_commit_id,omitempty"`
	OriginalLine        *int64         `json:"original_line,omitempty"`
	StartLine           *int64         `json:"start_line,omitempty"`
	OriginalStartLine   *int64         `json:"original_start_line,omitempty"`
	StartSide           *string        `json:"start_side,omitempty"`
	SubjectType         *string        `json:"subject_type,omitempty"`
	AuthorDetail        *RESTAccount   `json:"author_detail,omitempty"`
	AuthorAssociation   *string        `json:"author_association,omitempty"`
	Reactions           *RESTReactions `json:"reactions,omitempty"`
	URL                 *string        `json:"url,omitempty"`
	Links               Links          `json:"links,omitempty"`
}

// ListedReviewComment is one line comment of the line comment list: the compact comment and the width.
type ListedReviewComment struct {
	ReviewComment
	ReviewCommentWidth
}

func (e *reviewCommentEntry) view() ListedReviewComment {
	w := &e.wide
	width := ReviewCommentWidth{PullRequestReviewID: w.PullRequestReviewID, NodeID: nameText(w.NodeID),
		Position: w.Position, OriginalPosition: w.OriginalPosition, OriginalCommitID: nameText(w.OriginalCommitID),
		OriginalLine: w.OriginalLine, StartLine: w.StartLine, OriginalStartLine: w.OriginalStartLine,
		StartSide: nameText(w.StartSide), SubjectType: nameText(w.SubjectType), AuthorDetail: w.User.project(),
		AuthorAssociation: nameText(w.AuthorAssociation), Reactions: w.Reactions.project(), URL: address(w.HTMLURL),
		Links: w.links}
	width.DiffHunk, width.DiffHunkTruncated = cleanText(w.DiffHunk, projectionBodyLimit, true)
	return ListedReviewComment{ReviewComment: e.comment.view(), ReviewCommentWidth: width}
}

var reviewCommentWidthProperties = `"pull_request_review_id":{"type":"integer"},"node_id":{"type":"string"},` +
	`"diff_hunk":{"type":"string"},"diff_hunk_truncated":{"type":"boolean"},"position":{"type":"integer"},` +
	`"original_position":{"type":"integer"},"original_commit_id":{"type":"string"},` +
	`"original_line":{"type":"integer"},"start_line":{"type":"integer"},"original_start_line":{"type":"integer"},` +
	`"start_side":{"type":"string"},"subject_type":{"type":"string"},` +
	detailProperty("author", restAccountSchema, false) + `,"author_association":{"type":"string"},` +
	`"reactions":` + restReactionsSchema + `,"url":{"type":"string"},"links":` + linksSchema(reviewCommentLinkKeys)

var reviewCommentWidthFields = []capability.Field{
	{Name: "pull_request_review_id", Description: "Identifier of the review the line comment belongs to"},
	{Name: "node_id", Description: "GraphQL node id of the line comment"},
	{Name: "diff_hunk", Description: "Diff excerpt the comment refers to, cut at 4000 characters, untrusted data"},
	{Name: "diff_hunk_truncated", Description: "True when the diff excerpt was cut"},
	{Name: "position", Description: "Line index in the diff; absent once the comment is outdated"},
	{Name: "original_position", Description: "Line index in the diff when the comment was made"},
	{Name: "original_commit_id", Description: "Commit the comment was first made at"},
	{Name: "original_line", Description: "Line of the file the comment was first made on"},
	{Name: "start_line", Description: "First line of a multi-line comment"},
	{Name: "original_start_line", Description: "First line of a multi-line comment when it was made"},
	{Name: "start_side", Description: "Side of the diff of the first line: LEFT or RIGHT"},
	{Name: "subject_type", Description: "Whether the comment is on a line or a whole file"},
	detailField("author", "Commenter account: "+restAccountMembers+",", false),
	{Name: "author_association", Description: "How the commenter relates to the repository, such as OWNER or MEMBER"},
	{Name: "reactions", Description: "Reaction counts: " + restReactionsMembers},
	{Name: "url", Description: "Web address of the line comment, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and pull_request_url by their GitHub names, untrusted data, never fetched"},
}

// ---- Line comment filters

const reviewCommentFilterSchemas = `"sort":{"type":"string","enum":["created","updated"]},` +
	`"direction":{"type":"string","enum":["asc","desc"]},"since":{"type":"string","minLength":20,"maxLength":35}`

var reviewCommentFilterArguments = []capability.Argument{
	{Name: "sort", Description: "created or updated; GitHub's own order when omitted"},
	{Name: "direction", Description: "asc or desc; applies with sort, GitHub's default when omitted"},
	{Name: "since", Description: "Return only line comments updated at or after this time, as an RFC 3339 date-time"},
}

// checkFilters bounds the filters of the line comment list. since is sent to GitHub as a UTC time.
func (a *reviewCommentArguments) checkFilters() error {
	switch a.Sort {
	case "", "created", "updated":
	default:
		return invalidRequest("sort must be created or updated")
	}
	switch a.Direction {
	case "", "asc", "desc":
	default:
		return invalidRequest("direction must be asc or desc")
	}
	if a.Since == "" {
		return nil
	}
	moment, err := time.Parse(time.RFC3339, a.Since)
	if err != nil {
		return invalidRequest("since must be an RFC 3339 date-time such as 2026-01-01T00:00:00Z")
	}
	a.Since = moment.UTC().Format(isoInstantLayout)
	return nil
}

// filterParts are the fingerprint parts of the filters. A filter that is not set adds nothing, so a cursor
// issued before the filters existed stays valid.
func (a *reviewCommentArguments) filterParts() []any {
	var parts []any
	if a.Sort != "" {
		parts = append(parts, "sort", a.Sort)
	}
	if a.Direction != "" {
		parts = append(parts, "direction", a.Direction)
	}
	if a.Since != "" {
		parts = append(parts, "since", a.Since)
	}
	return parts
}
