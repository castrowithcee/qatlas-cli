package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The branch and tag tools read the refs of a repository without writing anything. github.branches.list
// lists one bounded batch of branches with their protection status and their latest commit SHA;
// github.tags.list lists one bounded batch of tags with the commit SHA each points at; github.tags.get reads
// one tag, lightweight or annotated, resolved through the Git refs and Git tags APIs rather than through the
// plain tags list, since only those name a lightweight tag's own object type and an annotated tag's tagger
// and message. None of the three writes.

// maxTagMessageLength bounds an annotated tag's message, in runes kept, the way maxCommitMessageLength
// bounds a commit's.
const maxTagMessageLength = 4096

const branchProperties = `"name":{"type":"string"},"protected":{"type":"boolean"},"sha":{"type":"string"}`

const branchRequired = `"required":["name","protected","sha"],"additionalProperties":false`

var branchesList = capability.Descriptor{
	ID:      Provider + ".branches.list",
	Version: 1,
	Title:   "List GitHub branches",
	Description: "List one bounded batch of the branches of a repository an explicit connection allows, " +
		"each with its protection status and its latest commit SHA",
	Tags:                       []string{"github", "branches", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(pagingKeys),
	OutputSchema:               listOutput("branches", branchProperties, branchRequired),
	Arguments:                  pagingArguments,
	Fields: append([]capability.Field{
		{Name: "branches", Description: "Branches with name, whether they are protected, and their latest " +
			"commit sha"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the branches", Arguments: json.RawMessage(`{}`)}},
}

const tagEntryProperties = `"name":{"type":"string"},"sha":{"type":"string"}`

const tagEntryRequired = `"required":["name","sha"],"additionalProperties":false`

var tagsList = capability.Descriptor{
	ID:      Provider + ".tags.list",
	Version: 1,
	Title:   "List GitHub tags",
	Description: "List one bounded batch of the tags of a repository an explicit connection allows, each " +
		"with the commit SHA it points at",
	Tags:                       []string{"github", "tags", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(pagingKeys),
	OutputSchema:               listOutput("tags", tagEntryProperties, tagEntryRequired),
	Arguments:                  pagingArguments,
	Fields: append([]capability.Field{
		{Name: "tags", Description: "Tags with name and the commit SHA it points at; an annotated tag's own " +
			"object SHA is not this commit SHA and is read only by github.tags.get"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the tags", Arguments: json.RawMessage(`{}`)}},
}

const tagDetailProperties = `"name":{"type":"string"},"type":{"type":"string"},"sha":{"type":"string"},` +
	`"target_sha":{"type":"string"},"target_type":{"type":"string"},"tagger":{"type":"string"},` +
	`"tagged_at":{"type":"string"},"message":{"type":"string"},"message_truncated":{"type":"boolean"}`

var tagsGet = capability.Descriptor{
	ID:      Provider + ".tags.get",
	Version: 1,
	Title:   "Get a GitHub tag",
	Description: "Read one tag of a repository an explicit connection allows, lightweight or annotated, " +
		"resolved through the Git refs and Git tags APIs: an annotated tag's tagger, message, and target object",
	Tags:                       []string{"github", "tags", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"tag":`+refSchema, "tag"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + tagDetailProperties + `},` +
		`"required":["name","type","sha"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "tag", Description: "Tag name", Required: true},
	},
	Fields: []capability.Field{
		{Name: "type", Description: "lightweight or annotated"},
		{Name: "sha", Description: "The commit this tag points at when lightweight, or its own tag object SHA " +
			"when annotated"},
		{Name: "target_sha", Description: "The object the tag object points at; present only for an annotated tag"},
		{Name: "target_type", Description: "commit, tree, blob, or tag; present only for an annotated tag"},
		{Name: "tagger", Description: "Name of whoever created an annotated tag; present only for one"},
		{Name: "tagged_at", Description: "When an annotated tag was created; present only for one"},
		{Name: "message", Description: "Annotated tag message, untrusted data, cut to at most 4096 characters " +
			"with message_truncated; present only for one"},
	},
	Examples: []capability.Example{{Description: "Read one tag", Arguments: json.RawMessage(`{"tag":"v1.0.0"}`)}},
}

func refsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: branchesList, Handler: refsHandler(branchesList.ID, checkRefsPagingOnly("branches"),
			func(ctx context.Context, c *Client, a *refsArguments) (any, error) { return c.listBranches(ctx, a) })},
		{Descriptor: tagsList, Handler: refsHandler(tagsList.ID, checkRefsPagingOnly("tags"),
			func(ctx context.Context, c *Client, a *refsArguments) (any, error) { return c.listTags(ctx, a) })},
		{Descriptor: tagsGet, Handler: refsHandler(tagsGet.ID, checkTagsGetArguments,
			func(ctx context.Context, c *Client, a *refsArguments) (any, error) { return c.getTag(ctx, a.Tag) })},
	}
}

// refsArguments holds the arguments of every branch and tag tool; the input schema of each tool admits only
// its own. page, perPage, and binding are derived by the checks.
type refsArguments struct {
	Tag    string `json:"tag"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	page, perPage int
	binding       []byte
}

// refsHandler decodes and checks the arguments and the repository before a credential is resolved, so a
// refused request never becomes a provider call.
func refsHandler(id string, check func(*refsArguments, target) error,
	call func(context.Context, *Client, *refsArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments refsArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

// checkRefsPagingOnly returns the check of a branch or tag list: paging only, no filter of its own.
func checkRefsPagingOnly(list string) func(*refsArguments, target) error {
	return func(a *refsArguments, bound target) error {
		limit, err := normalizeLimit(a.Limit)
		if err != nil {
			return err
		}
		a.binding = fingerprint("refs", list, bound.String())
		a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
		return err
	}
}

func checkTagsGetArguments(a *refsArguments, _ target) error {
	if !validRef(a.Tag) {
		return invalidRequest("tag must be a valid tag name")
	}
	return nil
}

func (a *refsArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// Permission messages of the branch and tag tools. GitHub decides on every request; a message names what
// such a request needs without claiming what the configured token holds.
const (
	branchesReadPermission = "GitHub refused this token the branches of this repository; reading them needs " +
		"no scope for a public repository, or repo on a classic token, or Contents: read on a fine-grained " +
		"token, for a private one"
	tagsReadPermission = "GitHub refused this token the tags of this repository; reading them needs no scope " +
		"for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, for a " +
		"private one"
)

// Branch is the compact view of one branch.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	SHA       string `json:"sha"`
}

type branchJSON struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (b branchJSON) view() Branch {
	return Branch{Name: b.Name, Protected: b.Protected, SHA: b.Commit.SHA}
}

// BranchList is one batch of branches.
type BranchList struct {
	Branches   []Branch `json:"branches"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

func (c *Client) listBranches(ctx context.Context, a *refsArguments) (*BranchList, error) {
	const op = "list branches"
	var raw []branchJSON
	hasNext, err := c.restPage(ctx, op, c.repoPath("branches"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, branchesReadPermission)
	}
	result := &BranchList{Branches: make([]Branch, 0, len(raw))}
	for _, branch := range raw {
		if branch.Name == "" || branch.Commit.SHA == "" {
			return nil, invalidEntry(op, "a branch")
		}
		result.Branches = append(result.Branches, branch.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// TagEntry is the compact view of one tag of the list.
type TagEntry struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

type tagEntryJSON struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (t tagEntryJSON) view() TagEntry {
	return TagEntry{Name: t.Name, SHA: t.Commit.SHA}
}

// TagList is one batch of tags.
type TagList struct {
	Tags       []TagEntry `json:"tags"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

func (c *Client) listTags(ctx context.Context, a *refsArguments) (*TagList, error) {
	const op = "list tags"
	var raw []tagEntryJSON
	hasNext, err := c.restPage(ctx, op, c.repoPath("tags"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, tagsReadPermission)
	}
	result := &TagList{Tags: make([]TagEntry, 0, len(raw))}
	for _, tag := range raw {
		if tag.Name == "" || tag.Commit.SHA == "" {
			return nil, invalidEntry(op, "a tag")
		}
		result.Tags = append(result.Tags, tag.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// TagDetail is one tag read by github.tags.get, lightweight or annotated.
type TagDetail struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	SHA              string `json:"sha"`
	TargetSHA        string `json:"target_sha,omitempty"`
	TargetType       string `json:"target_type,omitempty"`
	Tagger           string `json:"tagger,omitempty"`
	TaggedAt         string `json:"tagged_at,omitempty"`
	Message          string `json:"message,omitempty"`
	MessageTruncated bool   `json:"message_truncated,omitempty"`
}

type tagRefJSON struct {
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

type tagObjectJSON struct {
	Message string `json:"message"`
	Tagger  struct {
		Name string `json:"name"`
		Date string `json:"date"`
	} `json:"tagger"`
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

// tagRefPath and tagObjectPath escape the tag name and the object SHA as one opaque path segment each, the
// way getTree escapes a ref: a slash inside either becomes %2F rather than a further path segment, so
// neither can ever address another route.
func (c *Client) tagRefPath(tag string) string {
	return c.repoPath("git/refs/tags/" + url.PathEscape(tag))
}

func (c *Client) tagObjectPath(sha string) string {
	return c.repoPath("git/tags/" + url.PathEscape(sha))
}

// getTag resolves one tag of the bound repository through the Git refs API and, when it names an annotated
// tag object rather than a commit directly, dereferences that object through the Git tags API. GitHub's own
// answer names the object SHA and its type; validObjectSHA checks it before it is escaped into the second
// route, so a malformed upstream answer can never address another one.
func (c *Client) getTag(ctx context.Context, name string) (*TagDetail, error) {
	const op = "get tag"
	var ref tagRefJSON
	if err := c.rest(ctx, op, c.tagRefPath(name), &ref); err != nil {
		return nil, actionsFailure(err, tagsReadPermission)
	}
	if !validObjectSHA(ref.Object.SHA) {
		return nil, invalidEntry(op, "a tag ref")
	}
	if ref.Object.Type != "tag" {
		return &TagDetail{Name: name, Type: "lightweight", SHA: ref.Object.SHA}, nil
	}
	var obj tagObjectJSON
	if err := c.rest(ctx, op, c.tagObjectPath(ref.Object.SHA), &obj); err != nil {
		return nil, actionsFailure(err, tagsReadPermission)
	}
	if !validObjectSHA(obj.Object.SHA) || obj.Object.Type == "" {
		return nil, invalidEntry(op, "a tag object")
	}
	message, truncated := cutRunes(obj.Message, maxTagMessageLength)
	return &TagDetail{Name: name, Type: "annotated", SHA: ref.Object.SHA, TargetSHA: obj.Object.SHA,
		TargetType: obj.Object.Type, Tagger: obj.Tagger.Name, TaggedAt: obj.Tagger.Date, Message: message,
		MessageTruncated: truncated}, nil
}

// validObjectSHA accepts the hex object SHA GitHub's Git refs and Git tags routes report: at least the
// shortest abbreviation Git itself ever prints (7) and at most a full SHA (40). Every git/tags/{sha} route
// this tool builds from GitHub's own answer is checked against this pattern first, so a malformed upstream
// answer can never address another route.
func validObjectSHA(value string) bool {
	if len(value) < 7 || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// refsSubject names the tag or the tag object a Git refs or Git tags path below a repository addresses:
// git/refs/tags/TAG or git/tags/SHA. It names only a tag of the characters the input schema allows, or a hex
// object SHA, and is empty otherwise.
func refsSubject(path string) string {
	if rest, ok := strings.CutPrefix(path, "git/refs/tags/"); ok {
		if tag, err := url.PathUnescape(rest); err == nil && validRef(tag) {
			return "tag " + tag
		}
		return ""
	}
	if rest, ok := strings.CutPrefix(path, "git/tags/"); ok {
		if sha, err := url.PathUnescape(rest); err == nil && validObjectSHA(sha) {
			return "tag object " + sha
		}
	}
	return ""
}
