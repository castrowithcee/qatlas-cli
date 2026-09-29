package github

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The discussion tools read the GitHub Discussions of one repository through GraphQL: its categories, its
// discussions, one discussion, and the top-level comments of one discussion. Titles, bodies, and comments
// come from other accounts and are untrusted data; a body is cut at a fixed length and says so.
//
// Verified 2026-09-29 against https://docs.github.com/en/graphql/reference/objects (Repository.discussions,
// Repository.discussionCategories, Repository.discussion, Discussion.comments): the fields are stable GraphQL
// schema fields. Reading needs repo (or public_repo for public repositories) on a classic token, or
// Discussions: read on a fine-grained token.

const (
	discussionListBodyLimit    = 1000
	discussionBodyLimit        = 20000
	discussionCommentBodyLimit = 4000
)

// discussionIDPattern is the shape of a GitHub node identifier such as a discussion category.
var discussionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]{4,200}$`)

const discussionIDSchema = `{"type":"string","minLength":4,"maxLength":200,"pattern":"^[A-Za-z0-9_=-]+$"}`
const discussionLimitSchema = `{"type":"integer","minimum":1,"maximum":100}`

const discussionCategoryProperties = `"id":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},` +
	`"description":{"type":"string"},"emoji":{"type":"string"},"is_answerable":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}`

const discussionProperties = `"id":{"type":"string"},"number":{"type":"integer"},"title":{"type":"string"},` +
	`"body":{"type":"string"},"body_truncated":{"type":"boolean"},"author":{"type":"string"},` +
	`"category":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"slug":{"type":"string"}},"additionalProperties":false},` +
	`"closed":{"type":"boolean"},"locked":{"type":"boolean"},"answered":{"type":"boolean"},` +
	`"answer_chosen_at":{"type":"string"},"upvote_count":{"type":"integer"},` +
	`"comment_count":{"type":"integer"},"labels":{"type":"array","items":{"type":"string"}},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"}`
const discussionRequired = `"required":["id","number","title","body","closed","locked","answered"],"additionalProperties":false`

const discussionReplyProperties = `"id":{"type":"string"},"database_id":{"type":"integer"},` +
	`"author":{"type":"string"},"body":{"type":"string"},"body_truncated":{"type":"boolean"},` +
	`"is_answer":{"type":"boolean"},"upvote_count":{"type":"integer"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"}`

const discussionCommentProperties = discussionReplyProperties + `,"reply_count":{"type":"integer"},` +
	`"replies":{"type":"array","items":{"type":"object","properties":{` + discussionReplyProperties + `},` +
	`"required":["id","body"],"additionalProperties":false}},"replies_truncated":{"type":"boolean"}`

var discussionCursorArgument = capability.Argument{Name: "cursor", Description: "Opaque next_cursor of a " +
	"previous batch of the same list with the same filters; the first batch when omitted"}

var discussionLimitArgument = capability.Argument{Name: "limit", Description: "Entries per batch, from 1 " +
	"through 100; 30 when omitted"}

var discussionCategoriesList = capability.Descriptor{
	ID:      Provider + ".discussioncategories.list",
	Version: 1,
	Title:   "List GitHub discussion categories",
	Description: "List one bounded batch of the discussion categories of a repository an explicit connection " +
		"allows: id, name, slug, description, emoji, and whether answers can be chosen",
	Tags:                       []string{"github", "discussions", "categories", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":` + discussionLimitSchema +
		`,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"categories":{"type":"array","items":` +
		`{"type":"object","properties":{` + discussionCategoryProperties + `},` +
		`"required":["id","name","slug","is_answerable"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["categories","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{discussionLimitArgument, discussionCursorArgument},
	Fields: []capability.Field{
		{Name: "categories", Description: "Discussion categories: id (the value of the category argument of " +
			"github.discussions.list), name, slug, description (untrusted data), emoji, is_answerable, and times"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the repository holds further categories"},
	},
	Examples: []capability.Example{{
		Description: "List the discussion categories of a repository",
		Arguments:   json.RawMessage(`{}`),
	}},
}

var discussionsList = capability.Descriptor{
	ID:      Provider + ".discussions.list",
	Version: 1,
	Title:   "List GitHub discussions",
	Description: "List one bounded batch of the discussions of a repository an explicit connection allows, " +
		"filtered by category, state, and answered status, ordered by creation or update time; bodies are " +
		"shortened",
	Tags:                       []string{"github", "discussions", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"category":` + discussionIDSchema + `,` +
		`"state":{"type":"string","enum":["open","closed"]},"answered":{"type":"boolean"},` +
		`"order_by":{"type":"string","enum":["created_at","updated_at"]},` +
		`"direction":{"type":"string","enum":["asc","desc"]},` +
		`"limit":` + discussionLimitSchema + `,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"discussions":{"type":"array","items":` +
		`{"type":"object","properties":{` + discussionProperties + `},` + discussionRequired + `}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["discussions","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "category", Description: "Category id, as github.discussioncategories.list reports it; every " +
			"category when omitted"},
		{Name: "state", Description: "open or closed; both when omitted"},
		{Name: "answered", Description: "true for answered discussions only, false for unanswered ones only; " +
			"both when omitted"},
		{Name: "order_by", Description: "created_at or updated_at; created_at when omitted"},
		{Name: "direction", Description: "asc or desc; desc when omitted"},
		discussionLimitArgument, discussionCursorArgument,
	},
	Fields: []capability.Field{
		{Name: "discussions", Description: "Discussions with number, title, body cut at 1000 characters " +
			"(body_truncated says so), author, category, state, counts, labels, times, and url; title and " +
			"body are untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when further discussions match"},
	},
	Examples: []capability.Example{{
		Description: "List the newest unanswered discussions of a category",
		Arguments:   json.RawMessage(`{"category":"DIC_kwDOExample","answered":false,"limit":10}`),
	}},
}

var discussionsGet = capability.Descriptor{
	ID:      Provider + ".discussions.get",
	Version: 1,
	Title:   "Get a GitHub discussion",
	Description: "Read one discussion of a repository an explicit connection allows by its number, with its " +
		"body; comments are read by github.discussioncomments.list",
	Tags:                       []string{"github", "discussions", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"discussion":{"type":"object",` +
		`"properties":{` + discussionProperties + `},` + discussionRequired + `}},` +
		`"required":["discussion"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "number", Description: "Discussion number in the repository",
		Required: true}},
	Fields: []capability.Field{
		{Name: "discussion", Description: "The discussion: title and body (cut at 20000 characters, " +
			"body_truncated says so) are untrusted data, with author, category, state, counts, labels, times, " +
			"and url"},
	},
	Examples: []capability.Example{{
		Description: "Read one discussion",
		Arguments:   json.RawMessage(`{"number":12}`),
	}},
}

var discussionCommentsList = capability.Descriptor{
	ID:      Provider + ".discussioncomments.list",
	Version: 1,
	Title:   "List comments of a GitHub discussion",
	Description: "List one bounded batch of the top-level comments of one discussion of a repository an " +
		"explicit connection allows, oldest first; replies are counted, and listed with include_replies",
	Tags:                       []string{"github", "discussions", "comments", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"include_replies":{"type":"boolean"},` +
		`"limit":` + discussionLimitSchema + `,"cursor":` + cursorSchema + `},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` + discussionCommentProperties + `},` +
		`"required":["id","body"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["comments","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Discussion number in the repository", Required: true},
		{Name: "include_replies", Description: "true to list the replies of every top-level comment in its " +
			"replies field, up to 100 each; false when omitted"},
		discussionLimitArgument, discussionCursorArgument,
	},
	Fields: []capability.Field{
		{Name: "comments", Description: "Top-level comments with author, body cut at 4000 characters " +
			"(body_truncated says so), is_answer, upvote_count, reply_count, times, and url; untrusted data. With " +
			"include_replies each carries replies (at most 100, same fields; replies_truncated says that more " +
			"exist), and the reply bodies of one answer share a budget of 200000 characters, after which " +
			"further reply bodies are empty with body_truncated"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the discussion holds further comments"},
	},
	Examples: []capability.Example{{
		Description: "Read the first comments of one discussion",
		Arguments:   json.RawMessage(`{"number":12,"limit":10}`),
	}},
}

// clipText cuts text at limit characters and reports whether it did.
func clipText(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	return string([]rune(text)[:limit]), true
}

// DiscussionCategory is one discussion category of a repository.
type DiscussionCategory struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Description  string `json:"description,omitempty"`
	Emoji        string `json:"emoji,omitempty"`
	IsAnswerable bool   `json:"is_answerable"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

// DiscussionCategoryList is one batch of discussion categories.
type DiscussionCategoryList struct {
	Categories []DiscussionCategory `json:"categories"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

// DiscussionCategoryRef names the category of a discussion.
type DiscussionCategoryRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

// Discussion is the stable Qatlas view of one discussion. Title and Body are untrusted data.
type Discussion struct {
	ID             string                 `json:"id"`
	Number         int                    `json:"number"`
	Title          string                 `json:"title"`
	Body           string                 `json:"body"`
	BodyTruncated  bool                   `json:"body_truncated,omitempty"`
	Author         string                 `json:"author,omitempty"`
	Category       *DiscussionCategoryRef `json:"category,omitempty"`
	Closed         bool                   `json:"closed"`
	Locked         bool                   `json:"locked"`
	Answered       bool                   `json:"answered"`
	AnswerChosenAt string                 `json:"answer_chosen_at,omitempty"`
	UpvoteCount    int                    `json:"upvote_count"`
	CommentCount   int                    `json:"comment_count"`
	Labels         []string               `json:"labels,omitempty"`
	CreatedAt      string                 `json:"created_at,omitempty"`
	UpdatedAt      string                 `json:"updated_at,omitempty"`
	URL            string                 `json:"url,omitempty"`
}

// DiscussionList is one batch of discussions.
type DiscussionList struct {
	Discussions []Discussion `json:"discussions"`
	NextCursor  string       `json:"next_cursor,omitempty"`
	HasMore     bool         `json:"has_more"`
}

// DiscussionGet is one discussion.
type DiscussionGet struct {
	Discussion Discussion `json:"discussion"`
}

// DiscussionComment is one top-level comment of a discussion. Body is untrusted data.
type DiscussionComment struct {
	ID            string `json:"id"`
	DatabaseID    int64  `json:"database_id,omitempty"`
	Author        string `json:"author,omitempty"`
	Body          string `json:"body"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
	IsAnswer      bool   `json:"is_answer,omitempty"`
	UpvoteCount   int    `json:"upvote_count"`
	ReplyCount    int    `json:"reply_count"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	URL           string `json:"url,omitempty"`

	Replies          []DiscussionReply `json:"replies,omitempty"`
	RepliesTruncated bool              `json:"replies_truncated,omitempty"`
}

// DiscussionReply is one reply to a top-level comment. Body is untrusted data.
type DiscussionReply struct {
	ID            string `json:"id"`
	DatabaseID    int64  `json:"database_id,omitempty"`
	Author        string `json:"author,omitempty"`
	Body          string `json:"body"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
	IsAnswer      bool   `json:"is_answer,omitempty"`
	UpvoteCount   int    `json:"upvote_count"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	URL           string `json:"url,omitempty"`
}

// DiscussionCommentList is one batch of top-level discussion comments.
type DiscussionCommentList struct {
	Comments   []DiscussionComment `json:"comments"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

type discussionPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type discussionNode struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	Category *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"category"`
	Closed         bool   `json:"closed"`
	Locked         bool   `json:"locked"`
	IsAnswered     *bool  `json:"isAnswered"`
	AnswerChosenAt string `json:"answerChosenAt"`
	UpvoteCount    int    `json:"upvoteCount"`
	Comments       struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	Labels *struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	URL       string `json:"url"`
}

const discussionFields = `id number title body author{login} category{id name slug} closed locked isAnswered ` +
	`answerChosenAt upvoteCount comments{totalCount} labels(first:20){nodes{name}} createdAt updatedAt url`

func (n discussionNode) normalize(op string, bodyLimit int) (Discussion, error) {
	if n.ID == "" || n.Number < 1 {
		return Discussion{}, invalidEntry(op, "a discussion")
	}
	d := Discussion{ID: n.ID, Number: n.Number, Title: n.Title, Closed: n.Closed, Locked: n.Locked,
		Answered: n.IsAnswered != nil && *n.IsAnswered, AnswerChosenAt: n.AnswerChosenAt,
		UpvoteCount: n.UpvoteCount, CommentCount: n.Comments.TotalCount, CreatedAt: n.CreatedAt,
		UpdatedAt: n.UpdatedAt, URL: n.URL}
	d.Body, d.BodyTruncated = clipText(n.Body, bodyLimit)
	if n.Author != nil {
		d.Author = n.Author.Login
	}
	if n.Category != nil {
		d.Category = &DiscussionCategoryRef{ID: n.Category.ID, Name: n.Category.Name, Slug: n.Category.Slug}
	}
	if n.Labels != nil {
		for _, label := range n.Labels.Nodes {
			d.Labels = append(d.Labels, label.Name)
		}
	}
	return d, nil
}

// cursorAfter checks a cursor against its binding and returns the GraphQL cursor it continues after.
func discussionVariables(bound target, limit int, after string, extra map[string]any) map[string]any {
	variables := map[string]any{"owner": bound.owner, "name": bound.repo, "first": limit, "after": nil}
	if after != "" {
		variables["after"] = after
	}
	for key, value := range extra {
		variables[key] = value
	}
	return variables
}

func discussionNext(op, kind string, info discussionPageInfo, binding []byte) (bool, string, error) {
	if !info.HasNextPage {
		return false, "", nil
	}
	if info.EndCursor == "" {
		return false, "", &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub announced further " + kind + " without a cursor"}
	}
	return true, encodeCursor(binding, info.EndCursor), nil
}

// --- categories ---

type discussionCategoriesArguments struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

func (a *discussionCategoriesArguments) binding(bound target) []byte {
	return fingerprint("discussioncategories", bound.String())
}

const discussionCategoriesQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){discussionCategories(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{id name slug description emoji isAnswerable createdAt updatedAt}}}}`

type discussionCategoriesPageJSON struct {
	Repository *struct {
		Categories struct {
			PageInfo discussionPageInfo `json:"pageInfo"`
			Nodes    []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				Slug         string `json:"slug"`
				Description  string `json:"description"`
				Emoji        string `json:"emoji"`
				IsAnswerable bool   `json:"isAnswerable"`
				CreatedAt    string `json:"createdAt"`
				UpdatedAt    string `json:"updatedAt"`
			} `json:"nodes"`
		} `json:"discussionCategories"`
	} `json:"repository"`
}

func (c *Client) listDiscussionCategories(ctx context.Context, limit int, after string,
	binding []byte) (*DiscussionCategoryList, error) {
	const op = "list discussion categories"
	var page discussionCategoriesPageJSON
	if err := c.graphql(ctx, op, discussionCategoriesQuery, discussionVariables(c.target, limit, after, nil),
		&page); err != nil {
		return nil, actionsFailure(err, discussionsReadPermission)
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	result := &DiscussionCategoryList{Categories: make([]DiscussionCategory, 0, len(page.Repository.Categories.Nodes))}
	for _, n := range page.Repository.Categories.Nodes {
		if n.ID == "" || n.Name == "" {
			return nil, invalidEntry(op, "a discussion category")
		}
		result.Categories = append(result.Categories, DiscussionCategory{ID: n.ID, Name: n.Name, Slug: n.Slug,
			Description: n.Description, Emoji: n.Emoji, IsAnswerable: n.IsAnswerable, CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt})
	}
	var err error
	result.HasMore, result.NextCursor, err = discussionNext(op, "categories", page.Repository.Categories.PageInfo, binding)
	return result, err
}

// --- discussions ---

type discussionListArguments struct {
	Category  string `json:"category"`
	State     string `json:"state"`
	Answered  *bool  `json:"answered"`
	OrderBy   string `json:"order_by"`
	Direction string `json:"direction"`
	Limit     int    `json:"limit"`
	Cursor    string `json:"cursor"`
}

func (a *discussionListArguments) normalize() error {
	if a.Category != "" && !discussionIDPattern.MatchString(a.Category) {
		return invalidRequest("category must be a category id as github.discussioncategories.list reports it")
	}
	switch a.State {
	case "", "open", "closed":
	default:
		return invalidRequest("state must be open or closed")
	}
	switch a.OrderBy {
	case "":
		a.OrderBy = "created_at"
	case "created_at", "updated_at":
	default:
		return invalidRequest("order_by must be created_at or updated_at")
	}
	switch a.Direction {
	case "":
		a.Direction = "desc"
	case "asc", "desc":
	default:
		return invalidRequest("direction must be asc or desc")
	}
	limit, err := normalizeLimit(a.Limit)
	a.Limit = limit
	return err
}

func (a *discussionListArguments) binding(bound target) []byte {
	answered := ""
	if a.Answered != nil {
		answered = strconv.FormatBool(*a.Answered)
	}
	return fingerprint("discussions", bound.String(), a.Category, a.State, answered, a.OrderBy, a.Direction)
}

const discussionsQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String,$category:ID,` +
	`$answered:Boolean,$states:[DiscussionState!],$order:DiscussionOrder){` +
	`repository(owner:$owner,name:$name){discussions(first:$first,after:$after,categoryId:$category,` +
	`answered:$answered,states:$states,orderBy:$order){pageInfo{hasNextPage endCursor} ` +
	`nodes{` + discussionFields + `}}}}`

type discussionsPageJSON struct {
	Repository *struct {
		Discussions struct {
			PageInfo discussionPageInfo `json:"pageInfo"`
			Nodes    []discussionNode   `json:"nodes"`
		} `json:"discussions"`
	} `json:"repository"`
}

func (c *Client) listDiscussions(ctx context.Context, a *discussionListArguments, after string) (*DiscussionList, error) {
	const op = "list discussions"
	extra := map[string]any{"category": nil, "answered": nil, "states": nil,
		"order": map[string]string{"field": strings.ToUpper(a.OrderBy), "direction": strings.ToUpper(a.Direction)}}
	if a.Category != "" {
		extra["category"] = a.Category
	}
	if a.Answered != nil {
		extra["answered"] = *a.Answered
	}
	if a.State != "" {
		extra["states"] = []string{strings.ToUpper(a.State)}
	}
	var page discussionsPageJSON
	if err := c.graphql(ctx, op, discussionsQuery, discussionVariables(c.target, a.Limit, after, extra),
		&page); err != nil {
		return nil, actionsFailure(err, discussionsReadPermission)
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	result := &DiscussionList{Discussions: make([]Discussion, 0, len(page.Repository.Discussions.Nodes))}
	for _, n := range page.Repository.Discussions.Nodes {
		d, err := n.normalize(op, discussionListBodyLimit)
		if err != nil {
			return nil, err
		}
		result.Discussions = append(result.Discussions, d)
	}
	var err error
	result.HasMore, result.NextCursor, err = discussionNext(op, "discussions", page.Repository.Discussions.PageInfo,
		a.binding(c.target))
	return result, err
}

// --- one discussion ---

const discussionQuery = `query($owner:String!,$name:String!,$number:Int!){` +
	`repository(owner:$owner,name:$name){discussion(number:$number){` + discussionFields + `}}}`

type discussionPageJSON struct {
	Repository *struct {
		Discussion *discussionNode `json:"discussion"`
	} `json:"repository"`
}

func (c *Client) getDiscussion(ctx context.Context, number int) (*DiscussionGet, error) {
	const op = "get discussion"
	if err := checkNumber(number); err != nil {
		return nil, err
	}
	var page discussionPageJSON
	if err := c.graphql(ctx, op, discussionQuery,
		map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": number}, &page); err != nil {
		return nil, actionsFailure(err, discussionsReadPermission)
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.Discussion == nil {
		return nil, notFound(op, subject{in: c.target, what: "discussion #" + strconv.Itoa(number)})
	}
	d, err := page.Repository.Discussion.normalize(op, discussionBodyLimit)
	if err != nil {
		return nil, err
	}
	return &DiscussionGet{Discussion: d}, nil
}

// --- comments ---

type discussionCommentsArguments struct {
	Number         int    `json:"number"`
	IncludeReplies bool   `json:"include_replies"`
	Limit          int    `json:"limit"`
	Cursor         string `json:"cursor"`
}

func (a *discussionCommentsArguments) binding(bound target) []byte {
	return fingerprint("discussioncomments", bound.String(), a.Number, strconv.FormatBool(a.IncludeReplies))
}

const discussionCommentsQuery = `query($owner:String!,$name:String!,$number:Int!,$first:Int!,$after:String,` +
	`$withReplies:Boolean!){` +
	`repository(owner:$owner,name:$name){discussion(number:$number){comments(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{id databaseId author{login} body isAnswer upvoteCount ` +
	`replies(first:100){totalCount pageInfo{hasNextPage} nodes @include(if:$withReplies){id databaseId ` +
	`author{login} body isAnswer upvoteCount createdAt updatedAt url}} createdAt updatedAt url}}}}}`

// discussionReplyBudget bounds the characters of all reply bodies of one answer, so include_replies with the
// largest batch stays a bounded response. Bodies beyond it come back empty and marked truncated.
const discussionReplyBudget = 200000

type discussionCommentsPageJSON struct {
	Repository *struct {
		Discussion *struct {
			Comments struct {
				PageInfo discussionPageInfo `json:"pageInfo"`
				Nodes    []struct {
					ID         string `json:"id"`
					DatabaseID int64  `json:"databaseId"`
					Author     *struct {
						Login string `json:"login"`
					} `json:"author"`
					Body        string `json:"body"`
					IsAnswer    bool   `json:"isAnswer"`
					UpvoteCount int    `json:"upvoteCount"`
					Replies     struct {
						TotalCount int                `json:"totalCount"`
						PageInfo   discussionPageInfo `json:"pageInfo"`
						Nodes      []struct {
							ID         string `json:"id"`
							DatabaseID int64  `json:"databaseId"`
							Author     *struct {
								Login string `json:"login"`
							} `json:"author"`
							Body        string `json:"body"`
							IsAnswer    bool   `json:"isAnswer"`
							UpvoteCount int    `json:"upvoteCount"`
							CreatedAt   string `json:"createdAt"`
							UpdatedAt   string `json:"updatedAt"`
							URL         string `json:"url"`
						} `json:"nodes"`
					} `json:"replies"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
					URL       string `json:"url"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"discussion"`
	} `json:"repository"`
}

func (c *Client) listDiscussionComments(ctx context.Context, a *discussionCommentsArguments,
	after string) (*DiscussionCommentList, error) {
	const op = "list discussion comments"
	var page discussionCommentsPageJSON
	variables := discussionVariables(c.target, a.Limit, after, map[string]any{"number": a.Number,
		"withReplies": a.IncludeReplies})
	if err := c.graphql(ctx, op, discussionCommentsQuery, variables, &page); err != nil {
		return nil, actionsFailure(err, discussionsReadPermission)
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.Discussion == nil {
		return nil, notFound(op, subject{in: c.target, what: "discussion #" + strconv.Itoa(a.Number)})
	}
	comments := page.Repository.Discussion.Comments
	result := &DiscussionCommentList{Comments: make([]DiscussionComment, 0, len(comments.Nodes))}
	budget := discussionReplyBudget
	for _, n := range comments.Nodes {
		if n.ID == "" {
			return nil, invalidEntry(op, "a discussion comment")
		}
		comment := DiscussionComment{ID: n.ID, DatabaseID: n.DatabaseID, IsAnswer: n.IsAnswer,
			UpvoteCount: n.UpvoteCount, ReplyCount: n.Replies.TotalCount, CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt, URL: n.URL}
		comment.Body, comment.BodyTruncated = clipText(n.Body, discussionCommentBodyLimit)
		if n.Author != nil {
			comment.Author = n.Author.Login
		}
		for _, r := range n.Replies.Nodes {
			if r.ID == "" {
				return nil, invalidEntry(op, "a discussion reply")
			}
			reply := DiscussionReply{ID: r.ID, DatabaseID: r.DatabaseID, IsAnswer: r.IsAnswer,
				UpvoteCount: r.UpvoteCount, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, URL: r.URL}
			limit := discussionCommentBodyLimit
			if budget < limit {
				limit = budget
			}
			var cut bool
			reply.Body, cut = clipText(r.Body, limit)
			if !cut && budget < utf8.RuneCountInString(r.Body) {
				cut = true
			}
			reply.BodyTruncated = cut
			budget -= utf8.RuneCountInString(reply.Body)
			if r.Author != nil {
				reply.Author = r.Author.Login
			}
			comment.Replies = append(comment.Replies, reply)
		}
		comment.RepliesTruncated = a.IncludeReplies && n.Replies.PageInfo.HasNextPage
		result.Comments = append(result.Comments, comment)
	}
	var err error
	result.HasMore, result.NextCursor, err = discussionNext(op, "comments", comments.PageInfo, a.binding(c.target))
	return result, err
}

// discussionsReadPermission names what a token needs to read discussions. GitHub decides on every request;
// this names the requirement and never claims what the configured token holds.
const discussionsReadPermission = "GitHub refused this token the discussions of this repository; reading " +
	"them needs repo (or public_repo for a public repository) on a classic token, or Discussions: read on a " +
	"fine-grained token"

// --- handlers ---

func invokeDiscussionCategoriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a discussionCategoriesArguments
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, unreadable("list discussion categories")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if a.Limit, err = normalizeLimit(a.Limit); err != nil {
		return nil, err
	}
	binding := a.binding(bound)
	after, err := decodeCursor(binding, a.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listDiscussionCategories(ctx, a.Limit, after, binding))
}

func invokeDiscussionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a discussionListArguments
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, unreadable("list discussions")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := a.normalize(); err != nil {
		return nil, err
	}
	after, err := decodeCursor(a.binding(bound), a.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listDiscussions(ctx, &a, after))
}

func invokeDiscussionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, unreadable("get discussion")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(a.Number); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.getDiscussion(ctx, a.Number))
}

func invokeDiscussionCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a discussionCommentsArguments
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, unreadable("list discussion comments")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(a.Number); err != nil {
		return nil, err
	}
	if a.Limit, err = normalizeLimit(a.Limit); err != nil {
		return nil, err
	}
	after, err := decodeCursor(a.binding(bound), a.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listDiscussionComments(ctx, &a, after))
}

func discussionOperations() []capability.Operation {
	operations := []capability.Operation{
		{Descriptor: discussionCategoriesList, Handler: capability.Handler(invokeDiscussionCategoriesList)},
		{Descriptor: discussionsList, Handler: capability.Handler(invokeDiscussionsList)},
		{Descriptor: discussionsGet, Handler: capability.Handler(invokeDiscussionsGet)},
		{Descriptor: discussionCommentsList, Handler: capability.Handler(invokeDiscussionCommentsList)},
	}
	return append(operations, discussionWriteOperations()...)
}
