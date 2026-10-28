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

var starredRepositoryProperties = `"repository":{"type":"string"},"visibility":{"type":"string"},` +
	`"archived":{"type":"boolean"},"starred_at":{"type":"string"},"repository_detail":` + restRepositorySchema

// starsMediaType makes GitHub wrap each starred repository with the time it was starred.
const starsMediaType = "application/vnd.github.star+json"

const starsKeys = `"sort":{"type":"string","enum":["created","updated"]},` +
	`"direction":{"type":"string","enum":["asc","desc"]}`

var (
	starSortValues      = []string{"created", "updated"}
	starDirectionValues = []string{"asc", "desc"}
)

const starredRepositoryRequired = `"required":["repository","visibility","archived"],"additionalProperties":false`

var starsList = capability.Descriptor{
	ID:      Provider + ".stars.list",
	Version: 1,
	Title:   "List the GitHub repositories starred by this account",
	Description: "List one bounded batch of the repositories the account behind the connection's token has " +
		"starred, in the order GitHub returns them; offered only by a connection whose targets name no " +
		"repository and no project; a connection whose targets name an owner lists only the starred " +
		"repositories of that owner",
	Tags:         []string{"github", "stars", "discovery", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(starsKeys + `,` + pagingKeys),
	OutputSchema: listOutput("starred", starredRepositoryProperties, starredRepositoryRequired),
	Arguments: append([]capability.Argument{
		{Name: "sort", Description: "created (when the repository was starred) or updated (when it last " +
			"changed); GitHub's order, by star time, when omitted"},
		{Name: "direction", Description: "asc or desc; applies with sort, desc when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "starred", Description: "Starred repositories: repository as OWNER/REPO, visibility, archived, " +
			"starred_at (the time the account starred it), and repository_detail with the members of GitHub's " +
			"repository, untrusted data, addresses never fetched; a repository outside the connection's " +
			"targets keeps only id, node_id, and full_name"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the account's starred repositories", Arguments: json.RawMessage(`{}`)}},
}

const starChangeOutputProperties = `"starred":{"type":"boolean"},"changed":{"type":"boolean"}`

const starChangeRequired = `"required":["starred","changed"],"additionalProperties":false`

var starsAdd = capability.Descriptor{
	ID:      Provider + ".stars.add",
	Version: 1,
	Title:   "Star a GitHub repository",
	Description: "Star one repository a connection allows for the account behind its token; a " +
		"repository already starred is left as it is and reported without a further request",
	Tags:         []string{"github", "stars", "add"},
	Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:     Provider,
	InputSchema:  inputSchema(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + starChangeOutputProperties + `},` + starChangeRequired + `}`),
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
	Description: "Remove the star from one repository a connection allows for the account behind " +
		"its token; a repository that is not starred is left as it is and reported without a further request",
	Tags:         []string{"github", "stars", "remove"},
	Risk:         changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:     Provider,
	InputSchema:  inputSchema(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + starChangeOutputProperties + `},` + starChangeRequired + `}`),
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

// starListOptions are the filters and the paging of the star list. The cursor is bound to a filter only
// while it is set, so a cursor issued without one stays valid without it.
type starListOptions struct {
	Sort      string `json:"sort"`
	Direction string `json:"direction"`
	Limit     int    `json:"limit"`
	Cursor    string `json:"cursor"`

	sort, direction string
	page, perPage   int
	binding         []byte
}

func (o *starListOptions) query() url.Values {
	values := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if o.sort != "" {
		values.Set("sort", o.sort)
	}
	if o.direction != "" {
		values.Set("direction", o.direction)
	}
	return values
}

func (o *starListOptions) normalize() error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	binding := []any{"stars", "list"}
	if o.Sort != "" {
		if !containsFold(starSortValues, o.Sort) {
			return invalidRequest("sort must be created or updated")
		}
		o.sort = strings.ToLower(o.Sort)
		binding = append(binding, "sort", o.sort)
	}
	if o.Direction != "" {
		if !containsFold(starDirectionValues, o.Direction) {
			return invalidRequest("direction must be asc or desc")
		}
		o.direction = strings.ToLower(o.Direction)
		binding = append(binding, "direction", o.direction)
	}
	o.binding = fingerprint(binding...)
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

// StarredRepository is one starred repository: the compact view, the time it was starred, and the
// repository in full.
type StarredRepository struct {
	RepositorySummary
	StarredAt        *string         `json:"starred_at,omitempty"`
	RepositoryDetail *RESTRepository `json:"repository_detail,omitempty"`
}

// StarredRepositoryList is one batch of the account's starred repositories.
type StarredRepositoryList struct {
	Starred    []StarredRepository `json:"starred"`
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

// starsContext asks for the media type that wraps each repository with its star time.
func starsContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, acceptKey{}, starsMediaType)
}

// starItem decodes one entry of the star list twice from the same object: the compact view and the full
// repository. A member the full view cannot read is left out rather than failing the compact view.
type starItem struct {
	StarredAt *string `json:"starred_at"`
	compact   struct {
		FullName   string `json:"full_name"`
		Visibility string `json:"visibility"`
		Archived   bool   `json:"archived"`
		Owner      struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	wide *repositoryRaw
}

func (e *starItem) UnmarshalJSON(data []byte) error {
	var entry struct {
		StarredAt *string         `json:"starred_at"`
		Repo      json.RawMessage `json:"repo"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	e.StarredAt = entry.StarredAt
	if err := json.Unmarshal(entry.Repo, &e.compact); err != nil {
		return err
	}
	_ = json.Unmarshal(entry.Repo, &e.wide)
	return nil
}

func (c *Client) listStars(ctx context.Context, o *starListOptions) (*StarredRepositoryList, error) {
	const op = "list starred repositories"
	var raw []starItem
	hasNext, err := c.restPage(starsContext(ctx), op, "/user/starred", o.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, starsReadPermission)
	}
	result := &StarredRepositoryList{Starred: make([]StarredRepository, 0, len(raw))}
	for _, item := range raw {
		repo := &item.compact
		if repo.FullName == "" {
			return nil, invalidEntry(op, "a starred repository")
		}
		if !c.allowed.ownerNames(repo.Owner.Login) {
			continue
		}
		if len(c.allowed.excludes) > 0 &&
			(!validRepository(repo.FullName) || c.allowed.excluded(starTarget(repo.FullName))) {
			// An exclusion cannot be checked against a name that is not OWNER/REPO.
			continue
		}
		result.Starred = append(result.Starred, StarredRepository{
			RepositorySummary: RepositorySummary{Repository: repo.FullName,
				Visibility: strings.ToLower(repo.Visibility), Archived: repo.Archived},
			StarredAt: shortText(item.StarredAt), RepositoryDetail: item.wide.project(c.allowed)})
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// starTarget reads the repository of a full name.
func starTarget(fullName string) target {
	owner, name, _ := strings.Cut(fullName, "/")
	return target{kind: kindRepository, owner: owner, repo: name}
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
