package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The star tools read and change which repositories the account behind the connection's token has starred.
// GitHub answers both PUT and DELETE on the starring route with 204 whatever the repository's previous
// state, so either change is naturally idempotent; before sending one, the current state is read once with
// GET, which answers 204 when the repository is starred and 404 otherwise, so a repository already in the
// requested state is reported without a further request, and an actual change, when one is sent, travels
// once and is never repeated.
//
// The list names no repository or project and is offered only by a connection whose targets name neither:
// such a connection is scoped to those, and the account behind its token may belong to a customer other
// than the one its targets name. A connection whose targets name only owners still lists the account's
// stars, narrowed to the repositories of those owners.

const starredRepositoryProperties = `"repository":{"type":"string"},"visibility":{"type":"string"},` +
	`"archived":{"type":"boolean"}`

const starredRepositoryRequired = `"required":["repository","visibility","archived"],"additionalProperties":false`

var starsList = capability.Descriptor{
	ID:      Provider + ".stars.list",
	Version: 1,
	Title:   "List the GitHub repositories starred by this account",
	Description: "List one bounded batch of the repositories the account behind the connection's token has " +
		"starred, in the order GitHub returns them; offered only by a connection whose targets name no " +
		"repository and no project; a connection whose targets name an owner lists only the starred " +
		"repositories of that owner",
	Tags:                       []string{"github", "stars", "discovery", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(pagingKeys),
	OutputSchema:               listOutput("starred", starredRepositoryProperties, starredRepositoryRequired),
	Arguments:                  pagingArguments,
	Fields: append([]capability.Field{
		{Name: "starred", Description: "Starred repositories: repository as OWNER/REPO, visibility, and archived"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the account's starred repositories", Arguments: json.RawMessage(`{}`)}},
}

const starChangeOutputProperties = `"starred":{"type":"boolean"},"changed":{"type":"boolean"}`

const starChangeRequired = `"required":["starred","changed"],"additionalProperties":false`

var starsAdd = capability.Descriptor{
	ID:      Provider + ".stars.add",
	Version: 1,
	Title:   "Star a GitHub repository",
	Description: "Star one repository an explicit connection allows for the account behind its token; a " +
		"repository already starred is left as it is and reported without a further request",
	Tags:                       []string{"github", "stars", "add"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(""),
	OutputSchema:               json.RawMessage(`{"type":"object","properties":{` + starChangeOutputProperties + `},` + starChangeRequired + `}`),
	Fields: []capability.Field{
		{Name: "starred", Description: "True once the repository is starred"},
		{Name: "changed", Description: "True when this call starred it; false when it was already starred"},
	},
	Examples: []capability.Example{{
		Description: "Star a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

var starsRemove = capability.Descriptor{
	ID:      Provider + ".stars.remove",
	Version: 1,
	Title:   "Unstar a GitHub repository",
	Description: "Remove the star from one repository an explicit connection allows for the account behind " +
		"its token; a repository that is not starred is left as it is and reported without a further request",
	Tags:                       []string{"github", "stars", "remove"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(""),
	OutputSchema:               json.RawMessage(`{"type":"object","properties":{` + starChangeOutputProperties + `},` + starChangeRequired + `}`),
	Fields: []capability.Field{
		{Name: "starred", Description: "False once the star is removed"},
		{Name: "changed", Description: "True when this call removed the star; false when it was not starred"},
	},
	Examples: []capability.Example{{
		Description: "Unstar a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

func starOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: starsList, Handler: capability.Handler(invokeStarsList)},
		{Descriptor: starsAdd, Handler: capability.Handler(invokeStarChange(true))},
		{Descriptor: starsRemove, Handler: capability.Handler(invokeStarChange(false))},
	}
}

// starListOptions are the paging of the star list; its cursor is bound to no filter, since the list takes
// none.
type starListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func (o *starListOptions) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
}

func (o *starListOptions) normalize() error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	o.binding = fingerprint("stars", "list")
	o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit)
	return err
}

// invokeStarsList checks the paging and the connection's targets before a credential is resolved, so a
// connection scoped to a repository or a project never resolves a secret to answer an account-wide read.
func invokeStarsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options starListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list starred repositories")
	}
	if err := options.normalize(); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if err := accountWideAllowed(allowed); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listStars(ctx, &options)
}

// invokeStarChange checks the repository target before a credential is resolved.
func invokeStarChange(add bool) func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
	json.RawMessage) (any, error) {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(client.changeStar(ctx, add))
	}
}

// StarredRepositoryList is one batch of the account's starred repositories.
type StarredRepositoryList struct {
	Starred    []RepositorySummary `json:"starred"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

// StarChange is the answer to a star or an unstar.
type StarChange struct {
	Starred bool `json:"starred"`
	Changed bool `json:"changed"`
}

// Permission messages of the star tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	starsReadPermission = "GitHub refused this token its starred repositories; reading them needs no scope " +
		"on a classic token for public repositories, or repo as well for private ones, or Starring: read on " +
		"a fine-grained token"
	starsChangePermission = "GitHub refused this change of a star; it needs public_repo on a classic token " +
		"for a public repository, repo as well for a private one, or Starring: read and write on a " +
		"fine-grained token"
)

func (c *Client) listStars(ctx context.Context, o *starListOptions) (*StarredRepositoryList, error) {
	const op = "list starred repositories"
	var raw []struct {
		FullName   string `json:"full_name"`
		Visibility string `json:"visibility"`
		Archived   bool   `json:"archived"`
		Owner      struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	hasNext, err := c.restPage(ctx, op, "/user/starred", o.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, starsReadPermission)
	}
	result := &StarredRepositoryList{Starred: make([]RepositorySummary, 0, len(raw))}
	for _, repo := range raw {
		if repo.FullName == "" {
			return nil, invalidEntry(op, "a starred repository")
		}
		if !c.allowed.ownerNames(repo.Owner.Login) {
			continue
		}
		result.Starred = append(result.Starred, RepositorySummary{Repository: repo.FullName,
			Visibility: strings.ToLower(repo.Visibility), Archived: repo.Archived})
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// starPath is the starring REST route of the bound repository.
func (c *Client) starPath() string {
	return "/user/starred/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
}

// starState reads whether the bound repository is currently starred: GitHub answers this route with 204
// when it is and 404 when it is not.
func (c *Client) starState(ctx context.Context, op string) (bool, error) {
	err := c.rest(ctx, op, c.starPath(), nil)
	if err == nil {
		return true, nil
	}
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
		return false, nil
	}
	return false, actionsFailure(err, starsReadPermission)
}

// changeStar stars or unstars the bound repository. The current state is read first, so a repository
// already in the requested state is reported without a further request; otherwise the change is sent once.
func (c *Client) changeStar(ctx context.Context, add bool) (*StarChange, error) {
	op := "star repository"
	if !add {
		op = "unstar repository"
	}
	starred, err := c.starState(ctx, op)
	if err != nil {
		return nil, err
	}
	if starred == add {
		return &StarChange{Starred: add, Changed: false}, nil
	}
	method := http.MethodPut
	if !add {
		method = http.MethodDelete
	}
	if err := c.restChange(ctx, op, method, c.starPath(), struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, starsChangePermission)
	}
	return &StarChange{Starred: add, Changed: true}, nil
}
