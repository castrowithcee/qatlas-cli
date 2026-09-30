package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// github.reactions.add and github.reactions.remove react to one issue, one issue or pull request
// conversation comment, or one pull request review line comment of the bound repository, by kind and a
// numeric id: an issue number for kind issue, since a pull request conversation is an issue for GitHub's
// reaction routes just as it is for github.comments.list and github.comments.create's database_id; the REST
// comment_id for kind issue_comment, of an issue or a pull request conversation comment alike, since GitHub
// keeps both under the same issue comment route; and the REST comment_id github.pullrequestreviewcomments.list
// already reports as id for kind review_comment. A foreign id is never accepted from outside the bound
// repository: every reaction route lives below /repos/OWNER/REPO, so an id of another repository answers
// not-found before anything changes.
//
// github.reactions.add sends one POST, which GitHub itself answers idempotently: 201 when it creates a new
// reaction of this account, or 200 with the reaction that already existed when this account had already
// reacted with the same content, never a duplicate. github.reactions.remove never accepts a reaction
// identifier from its caller, since that would let it delete a reaction of another account; instead it reads
// the account behind the connection's token, lists the existing reactions of this content, and finds this
// account's own among them, removing only that one when it exists.

const reactionKindSchema = `{"type":"string","enum":["issue","issue_comment","review_comment"]}`

const reactionContentSchema = `{"type":"string",` +
	`"enum":["+1","-1","laugh","confused","heart","hooray","rocket","eyes"]}`

const reactionInputKeys = `"kind":` + reactionKindSchema + `,"id":` + actionsIDSchema + `,` +
	`"content":` + reactionContentSchema

const reactionOutputProperties = `"kind":{"type":"string"},"id":{"type":"integer"},"content":{"type":"string"}`

var reactionKindArgument = capability.Argument{Name: "kind", Description: "issue for an issue or a pull " +
	"request number, since a pull request conversation is an issue for reactions; issue_comment for an " +
	"issue or a pull request conversation comment, by its database_id; review_comment for a pull request " +
	"line comment, by its id", Required: true}

var reactionIDArgument = capability.Argument{Name: "id", Description: "Issue or pull request number for " +
	"kind issue, or the numeric identifier of the comment for kind issue_comment or review_comment", Required: true}

var reactionContentArgument = capability.Argument{Name: "content", Description: "One of +1, -1, laugh, " +
	"confused, heart, hooray, rocket, eyes", Required: true}

var reactionsAdd = capability.Descriptor{
	ID:      Provider + ".reactions.add",
	Version: 1,
	Title:   "Add a reaction to a GitHub issue, comment, or line comment",
	Description: "React to one issue, issue or pull request conversation comment, or pull request line " +
		"comment of a repository a connection allows, with the account behind its token; a " +
		"reaction this account already left with the same content is left as it is and reported without a " +
		"further request",
	Tags:        []string{"github", "reactions", "add"},
	Risk:        changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	InputSchema: inputSchema(reactionInputKeys, "kind", "id", "content"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + reactionOutputProperties +
		`,"added":{"type":"boolean"}},"required":["kind","id","content","added"],"additionalProperties":false}`),
	Arguments: []capability.Argument{reactionKindArgument, reactionIDArgument, reactionContentArgument},
	Fields: []capability.Field{
		{Name: "kind", Description: "Kind of the target this call reacted to"},
		{Name: "id", Description: "Identifier of the target this call reacted to"},
		{Name: "content", Description: "Reaction content"},
		{Name: "added", Description: "True when this call added the reaction; false when the account had " +
			"already left it with this content"},
	},
	Examples: []capability.Example{{
		Description: "React to an issue with a thumbs up",
		Arguments:   json.RawMessage(`{"kind":"issue","id":42,"content":"+1"}`),
	}},
}

var reactionsRemove = capability.Descriptor{
	ID:      Provider + ".reactions.remove",
	Version: 1,
	Title:   "Remove a reaction from a GitHub issue, comment, or line comment",
	Description: "Remove the reaction of this content the account behind the connection's token left on one " +
		"issue, issue or pull request conversation comment, or pull request line comment of a repository a " +
		"connection allows; only this account's own reaction is ever removed, never another " +
		"account's, and a reaction this account never left is reported without a further request",
	Tags:        []string{"github", "reactions", "remove"},
	Risk:        changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:    Provider,
	InputSchema: inputSchema(reactionInputKeys, "kind", "id", "content"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + reactionOutputProperties +
		`,"removed":{"type":"boolean"}},"required":["kind","id","content","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{reactionKindArgument, reactionIDArgument, reactionContentArgument},
	Fields: []capability.Field{
		{Name: "kind", Description: "Kind of the target this call acted on"},
		{Name: "id", Description: "Identifier of the target this call acted on"},
		{Name: "content", Description: "Reaction content"},
		{Name: "removed", Description: "True when this call removed the account's own reaction; false when " +
			"the account had not left one with this content"},
	},
	Examples: []capability.Example{{
		Description: "Remove a thumbs up from an issue",
		Arguments:   json.RawMessage(`{"kind":"issue","id":42,"content":"+1"}`),
	}},
}

// reactionArguments holds the arguments of both reaction tools.
type reactionArguments struct {
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

func checkReactionArguments(a *reactionArguments, _ target) error {
	switch a.Kind {
	case "issue":
		if a.ID < 1 || a.ID > 1000000000 {
			return invalidRequest("id must be a positive issue or pull request number for kind issue")
		}
	case "issue_comment", "review_comment":
		if a.ID < 1 {
			return invalidRequest("id must be a positive numeric identifier")
		}
	default:
		return invalidRequest("kind must be issue, issue_comment, or review_comment")
	}
	switch a.Content {
	case "+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes":
	default:
		return invalidRequest("content must be one of +1, -1, laugh, confused, heart, hooray, rocket, eyes")
	}
	return nil
}

// reactionsHandler decodes and checks the arguments and the repository before a credential is resolved, so
// a refused request never becomes a provider call, the way commentMaintenanceHandler does for the comment
// maintenance tools.
func reactionsHandler(id string, call func(context.Context, *Client, *reactionArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments reactionArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := checkReactionArguments(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func reactionsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: reactionsAdd, Handler: reactionsHandler(reactionsAdd.ID,
			func(ctx context.Context, c *Client, a *reactionArguments) (any, error) {
				return c.addReaction(ctx, a)
			})},
		{Descriptor: reactionsRemove, Handler: reactionsHandler(reactionsRemove.ID,
			func(ctx context.Context, c *Client, a *reactionArguments) (any, error) {
				return c.removeReaction(ctx, a)
			})},
	}
}

// ReactionAdd is the answer of github.reactions.add.
type ReactionAdd struct {
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Content string `json:"content"`
	Added   bool   `json:"added"`
}

// ReactionRemove is the answer of github.reactions.remove.
type ReactionRemove struct {
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Content string `json:"content"`
	Removed bool   `json:"removed"`
}

// reactionJSON is one reaction GitHub reports below a reactions route.
type reactionJSON struct {
	ID   int64 `json:"id"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	Content string `json:"content"`
}

// Permission message of the reaction tools; the same scopes reading and writing an issue or a pull request
// conversation or line comment need, since GitHub keeps a reaction under the resource it reacts to rather
// than a scope of its own.
const reactionsPermission = "GitHub refused this change of a reaction; it needs repo on a classic token, or " +
	"Issues: read and write for an issue or an issue comment, or Pull requests: read and write for a pull " +
	"request line comment, on a fine-grained token"

// reactionPath is the reactions REST route of one target of the bound repository, by kind and id.
func (c *Client) reactionPath(kind string, id int64) string {
	switch kind {
	case "issue":
		return c.repoPath("issues/" + strconv.FormatInt(id, 10) + "/reactions")
	case "review_comment":
		return c.repoPath("pulls/comments/" + strconv.FormatInt(id, 10) + "/reactions")
	default:
		return c.commentPath(id) + "/reactions"
	}
}

// addReaction sends exactly one POST, which GitHub itself answers idempotently: 201 for a new reaction of
// this account, 200 for one it already held with this content, never a duplicate. The request is never
// repeated.
func (c *Client) addReaction(ctx context.Context, a *reactionArguments) (*ReactionAdd, error) {
	const op = "add reaction"
	var raw reactionJSON
	status, err := c.restChangeStatus(ctx, op, http.MethodPost, c.reactionPath(a.Kind, a.ID),
		map[string]any{"content": a.Content}, &raw)
	if err != nil {
		return nil, actionsFailure(err, reactionsPermission)
	}
	if raw.ID < 1 {
		return nil, invalidResponse(op, true)
	}
	return &ReactionAdd{Kind: a.Kind, ID: a.ID, Content: a.Content, Added: status == http.StatusCreated}, nil
}

// maxReactionScanPages bounds how far removeReaction scans the existing reactions of one content to find the
// account's own: 20 pages of 100 reaches 2000 reactions of that content before giving up, which every
// repository this provider has been asked to reach stays well under.
// qatlas-dev: raise this, or read the reaction id GitHub's own POST answer names instead of scanning, if a
// resource with more reactions of one content than this bound needs removing.
const maxReactionScanPages = 20

// findOwnReaction lists the reactions of one content below path and reports the identifier of the one this
// login left, or zero when it left none. Only this identifier is ever deleted, so a reaction of another
// account is never removed.
func (c *Client) findOwnReaction(ctx context.Context, op, path, content, login string) (int64, error) {
	for page := 1; page <= maxReactionScanPages; page++ {
		var raw []reactionJSON
		query := url.Values{"content": {content}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		hasNext, err := c.restPage(ctx, op, path, query, &raw)
		if err != nil {
			return 0, actionsFailure(err, reactionsPermission)
		}
		for _, entry := range raw {
			if entry.User != nil && strings.EqualFold(entry.User.Login, login) && entry.Content == content {
				if entry.ID < 1 {
					return 0, invalidEntry(op, "a reaction")
				}
				return entry.ID, nil
			}
		}
		if !hasNext {
			return 0, nil
		}
	}
	return 0, providerError(op, "GitHub holds more reactions of this content than Qatlas scans to find the "+
		"account's own")
}

// removeReaction reads the account behind the connection's token, finds this account's own reaction of this
// content among the existing ones, and deletes only that one, once, never a reaction of another account. A
// content this account never left with this reaction is reported without a further request.
func (c *Client) removeReaction(ctx context.Context, a *reactionArguments) (*ReactionRemove, error) {
	const op = "remove reaction"
	account, err := c.getAccount(ctx)
	if err != nil {
		return nil, err
	}
	path := c.reactionPath(a.Kind, a.ID)
	reactionID, err := c.findOwnReaction(ctx, op, path, a.Content, account.Login)
	if err != nil {
		return nil, err
	}
	if reactionID == 0 {
		return &ReactionRemove{Kind: a.Kind, ID: a.ID, Content: a.Content, Removed: false}, nil
	}
	if err := c.restChange(ctx, op, http.MethodDelete, path+"/"+strconv.FormatInt(reactionID, 10), nil, nil); err != nil {
		return nil, actionsFailure(err, reactionsPermission)
	}
	return &ReactionRemove{Kind: a.Kind, ID: a.ID, Content: a.Content, Removed: true}, nil
}

// reactionsSubject names the comment a reactions path below a repository addresses: issues/comments/ID/...
// as "comment ID" and pulls/comments/ID/... as "review comment ID", the way reviewsSubject names a review
// comment below pulls/N/comments. A reactions path below issues/N is already named "issue #N" by the
// digit-extraction restSubject applies to every issues/ path, so it is not repeated here.
func reactionsSubject(path string) string {
	if rest, ok := strings.CutPrefix(path, "issues/comments/"); ok {
		digits, _, _ := strings.Cut(rest, "/")
		if id, ok := positiveID(digits); ok {
			return "comment " + id
		}
	}
	if rest, ok := strings.CutPrefix(path, "pulls/comments/"); ok {
		digits, _, _ := strings.Cut(rest, "/")
		if id, ok := positiveID(digits); ok {
			return "review comment " + id
		}
	}
	return ""
}
