package github

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The owner lists answer which projects and repositories a user or an organization holds, so a caller finds
// a project number or a repository name without knowing it in advance. They read names and a few flags only,
// never items, issues, or settings.

const ownerListInput = `{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100},` +
	`"cursor":` + cursorSchema + `},"additionalProperties":false}`

var ownerListArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 100; 30 when omitted"},
	{Name: "cursor", Description: "Opaque next_cursor of a previous batch of the same owner; the first batch " +
		"when omitted"},
}

var projectsList = capability.Descriptor{
	ID:      Provider + ".projects.list",
	Version: 1,
	Title:   "List GitHub projects of an owner",
	Description: "List one bounded batch of the projects of a user or an organization an explicit connection " +
		"allows, in number order, with number, title, URL, and whether each is closed",
	Tags:                       []string{"github", "projects", "owner", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(ownerListInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"projects":{"type":"array","items":{"type":"object","properties":{"project":{"type":"string"},` +
		`"number":{"type":"integer"},"title":{"type":"string"},"url":{"type":"string"},"closed":{"type":"boolean"}},` +
		`"required":["project","number","title","closed"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["projects","has_more"],"additionalProperties":false}`),
	Arguments: ownerListArguments,
	Fields: []capability.Field{
		{Name: "projects", Description: "Projects of the owner the connection allows: project as " +
			"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER, number, title (untrusted data), url, closed"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the owner may hold further projects; a full batch alone " +
			"never means the end"},
	},
	Examples: []capability.Example{{
		Description: "List the projects of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org","limit":30}`),
	}},
}

var repositoriesList = capability.Descriptor{
	ID:      Provider + ".repositories.list",
	Version: 1,
	Title:   "List GitHub repositories of an owner",
	Description: "List one bounded batch of the repositories a user or an organization owns and an explicit " +
		"connection allows, in name order, with visibility and whether each is archived",
	Tags:                       []string{"github", "repositories", "owner", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(ownerListInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"repositories":{"type":"array","items":{"type":"object","properties":{"repository":{"type":"string"},` +
		`"visibility":{"type":"string"},"archived":{"type":"boolean"}},` +
		`"required":["repository","visibility","archived"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["repositories","has_more"],"additionalProperties":false}`),
	Arguments: ownerListArguments,
	Fields: []capability.Field{
		{Name: "repositories", Description: "Repositories the owner owns and the connection allows: repository " +
			"as OWNER/REPO, visibility (public, private, or internal), archived"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the owner may hold further repositories; a full batch alone " +
			"never means the end"},
	},
	Examples: []capability.Example{{
		Description: "List the repositories of a user",
		Arguments:   json.RawMessage(`{"owner":"users/octocat"}`),
	}},
}

// ProjectList is one batch of the projects of an owner.
type ProjectList struct {
	Projects   []ProjectSummary `json:"projects"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

// ProjectSummary is the compact view of one project: Project is the value a project argument takes.
type ProjectSummary struct {
	Project string `json:"project"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Closed  bool   `json:"closed"`
}

// RepositoryList is one batch of the repositories of an owner.
type RepositoryList struct {
	Repositories []RepositorySummary `json:"repositories"`
	NextCursor   string              `json:"next_cursor,omitempty"`
	HasMore      bool                `json:"has_more"`
}

// RepositorySummary is the compact view of one repository: Repository is the value a repository argument
// takes.
type RepositorySummary struct {
	Repository string `json:"repository"`
	Visibility string `json:"visibility"`
	Archived   bool   `json:"archived"`
}

// The GraphQL documents of the owner lists. The owner field is chosen by users or orgs; every value travels as
// a variable. Repositories are the ones the owner owns: GitHub would add those it collaborates on otherwise.
const (
	ownerListVariables = `query($owner:String!,$first:Int!,$after:String){owner:`
	projectsSelection  = `(login:$owner){projectsV2(first:$first,after:$after,orderBy:{field:NUMBER,direction:ASC}){` +
		`pageInfo{hasNextPage endCursor} edges{cursor node{number title url closed}}}}}`
	repositoriesSelection = `(login:$owner){repositories(first:$first,after:$after,ownerAffiliations:[OWNER],` +
		`orderBy:{field:NAME,direction:ASC}){pageInfo{hasNextPage endCursor} ` +
		`edges{cursor node{name visibility isArchived}}}}}`
)

type pageInfoJSON struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type edgeJSON[T any] struct {
	Cursor string `json:"cursor"`
	Node   T      `json:"node"`
}

type connectionJSON[T any] struct {
	PageInfo pageInfoJSON  `json:"pageInfo"`
	Edges    []edgeJSON[T] `json:"edges"`
}

type projectNodeJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Closed bool   `json:"closed"`
}

type repositoryNodeJSON struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	IsArchived bool   `json:"isArchived"`
}

// ownerListOptions are the paging of an owner list.
type ownerListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// normalize applies the bounds of one owner list request and returns the GitHub cursor it continues after. A
// cursor is bound to the list and to the owner, whose login GitHub compares without case.
func (o *ownerListOptions) normalize(list string, owner target) ([]byte, string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return nil, "", err
	}
	o.Limit = limit
	binding := fingerprint(list, strings.ToLower(owner.String()))
	after, err := decodeCursor(binding, o.Cursor)
	return binding, after, err
}

// scanOwner reads pages of an owner connection after the continuation point until limit entries the targets
// show plus one look-ahead entry are found, the owner holds no further entries, or the scan bound is reached.
// The next cursor points at the last entry this batch consumed, so every shown entry is reached exactly once.
func scanOwner[T any](op, what string, limit int, after string, binding []byte,
	fetch func(first int, after string) (*connectionJSON[T], error), keep func(T) bool) ([]T, string, error) {
	const invalid = provider.ClassInvalidResponse
	kept := []T{}
	last := after
	for scan := 0; scan < maxScanRequests; scan++ {
		first := min(limit-len(kept)+1, maxLimit)
		page, err := fetch(first, after)
		if err != nil {
			return nil, "", err
		}
		for _, edge := range page.Edges {
			if edge.Cursor == "" {
				return nil, "", &provider.Error{Class: invalid, Op: op, Message: "GitHub returned " + what +
					" without a cursor"}
			}
			if keep(edge.Node) {
				if len(kept) == limit {
					return kept, encodeCursor(binding, last), nil
				}
				kept = append(kept, edge.Node)
			}
			last = edge.Cursor
		}
		if !page.PageInfo.HasNextPage {
			return kept, "", nil
		}
		if page.PageInfo.EndCursor == "" {
			return nil, "", &provider.Error{Class: invalid, Op: op, Message: "GitHub announced further " + what +
				" without a cursor"}
		}
		after, last = page.PageInfo.EndCursor, page.PageInfo.EndCursor
	}
	// The scan bound was reached while the owner still holds unread entries; the batch is never the end.
	return kept, encodeCursor(binding, last), nil
}

// ownerRights names what a token needs to list the projects or the repositories of an owner. GitHub decides on
// every request, so a refusal names the requirement and never claims what the token holds.
var ownerRights = map[string]struct{ what, needs string }{
	"projectsV2": {"the projects", "; classic: scope read:project; fine-grained: Projects: read of the " +
		"organization, as the projects of a user need a classic token"},
	"repositories": {"the repositories", "; classic: scope repo for private repositories; fine-grained: access " +
		"to the repositories"},
}

// ownerPage reads one page of an owner connection. An owner GitHub leaves out is one it does not hold under
// this kind, users or orgs, or does not show to this token; a refusal of the token names the list and what
// it needs.
func ownerPage[T any](ctx context.Context, c *Client, op, selection, field string, first int,
	after string) (*connectionJSON[T], error) {
	variables := map[string]any{"owner": c.target.owner, "first": first, "after": nil}
	if after != "" {
		variables["after"] = after
	}
	var answer struct {
		Owner map[string]*connectionJSON[T] `json:"owner"`
	}
	if err := c.graphql(ctx, op, ownerListVariables+c.target.ownerField()+selection, variables, &answer); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			rights := ownerRights[field]
			failure.Message = permissionMessage(subject{in: c.target, what: rights.what}, false) + rights.needs
		}
		return nil, err
	}
	if answer.Owner == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	page := answer.Owner[field]
	if page == nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub returned an owner without its " + strings.ToLower(field)}
	}
	return page, nil
}

// listProjects reads one batch of the projects of the bound owner that the targets show.
func (c *Client) listProjects(ctx context.Context, options ownerListOptions, binding []byte,
	after string) (*ProjectList, error) {
	const op = "list projects"
	owner := c.target
	shown := func(node projectNodeJSON) target {
		return target{kind: kindProject, scope: owner.scope, owner: owner.owner, number: node.Number}
	}
	nodes, next, err := scanOwner(op, "projects", options.Limit, after, binding,
		func(first int, after string) (*connectionJSON[projectNodeJSON], error) {
			return ownerPage[projectNodeJSON](ctx, c, op, projectsSelection, "projectsV2", first, after)
		}, func(node projectNodeJSON) bool { return node.Number > 0 && c.allowed.shows(owner, shown(node)) })
	if err != nil {
		return nil, err
	}
	result := &ProjectList{Projects: make([]ProjectSummary, 0, len(nodes)), NextCursor: next, HasMore: next != ""}
	for _, node := range nodes {
		result.Projects = append(result.Projects, ProjectSummary{Project: shown(node).String(), Number: node.Number,
			Title: node.Title, URL: node.URL, Closed: node.Closed})
	}
	return result, nil
}

// listRepositories reads one batch of the repositories the bound owner owns that the targets show.
func (c *Client) listRepositories(ctx context.Context, options ownerListOptions, binding []byte,
	after string) (*RepositoryList, error) {
	const op = "list repositories"
	owner := c.target
	shown := func(node repositoryNodeJSON) target {
		return target{kind: kindRepository, owner: owner.owner, repo: node.Name}
	}
	nodes, next, err := scanOwner(op, "repositories", options.Limit, after, binding,
		func(first int, after string) (*connectionJSON[repositoryNodeJSON], error) {
			return ownerPage[repositoryNodeJSON](ctx, c, op, repositoriesSelection, "repositories", first, after)
		}, func(node repositoryNodeJSON) bool {
			return validRepoName(node.Name) && c.allowed.shows(owner, shown(node))
		})
	if err != nil {
		return nil, err
	}
	result := &RepositoryList{Repositories: make([]RepositorySummary, 0, len(nodes)), NextCursor: next,
		HasMore: next != ""}
	for _, node := range nodes {
		result.Repositories = append(result.Repositories, RepositorySummary{Repository: shown(node).argument(),
			Visibility: strings.ToLower(node.Visibility), Archived: node.IsArchived})
	}
	return result, nil
}

func invokeProjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeOwnerList(ctx, resolved, secrets, red, raw, kindProject,
		func(c *Client, options ownerListOptions, binding []byte, after string) (any, error) {
			return c.listProjects(ctx, options, binding, after)
		})
}

func invokeRepositoriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeOwnerList(ctx, resolved, secrets, red, raw, kindRepository,
		func(c *Client, options ownerListOptions, binding []byte, after string) (any, error) {
			return c.listRepositories(ctx, options, binding, after)
		})
}

// invokeOwnerList checks the owner, the paging, and the cursor before a credential is resolved, so a refused
// request never becomes a secret read or a provider call.
func invokeOwnerList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, kind targetKind,
	list func(*Client, ownerListOptions, []byte, string) (any, error)) (any, error) {
	var options ownerListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list owner")
	}
	owner, err := selectOwner(resolved, kind, raw)
	if err != nil {
		return nil, err
	}
	name := "projects"
	if kind == kindRepository {
		name = "repositories"
	}
	binding, after, err := options.normalize(name, owner)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, owner)
	if err != nil {
		return nil, err
	}
	return owner.locate(list(client, options, binding, after))
}
