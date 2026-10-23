package github

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The owner lists answer which projects and repositories a user or an organization holds, so a caller finds
// a project number or a repository name without knowing it in advance. They read the members of the projects
// and repositories themselves, never their items, issues, or settings.

// repositoryListInput adds the structured filters GitHub's repository connection offers to the paging.
const repositoryListInput = `{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100},` +
	`"cursor":` + cursorSchema + `,"privacy":{"type":"string","enum":["PUBLIC","PRIVATE"]},` +
	`"visibility":{"type":"string","enum":["PUBLIC","PRIVATE","INTERNAL"]},"is_archived":{"type":"boolean"},` +
	`"is_fork":{"type":"boolean"},"is_locked":{"type":"boolean"},"has_issues_enabled":{"type":"boolean"},` +
	`"order_by":{"type":"string","enum":["CREATED_AT","NAME","PUSHED_AT","STARGAZERS","UPDATED_AT"]},` +
	`"direction":{"type":"string","enum":["ASC","DESC"]},"admin_counts":{"type":"boolean"}},` +
	`"additionalProperties":false}`

var repositoryListArguments = append(append([]capability.Argument(nil), ownerListArguments...),
	capability.Argument{Name: "privacy", Description: "Only PUBLIC or only PRIVATE repositories (internal ones " +
		"count as private); not together with visibility"},
	capability.Argument{Name: "visibility", Description: "Only PUBLIC, PRIVATE, or INTERNAL repositories; not " +
		"together with privacy"},
	capability.Argument{Name: "is_archived", Description: "True for only archived, false for only unarchived " +
		"repositories; both when omitted"},
	capability.Argument{Name: "is_fork", Description: "True for only forks, false for only repositories that are " +
		"no forks; both when omitted"},
	capability.Argument{Name: "is_locked", Description: "True for only locked, false for only unlocked " +
		"repositories; both when omitted"},
	capability.Argument{Name: "has_issues_enabled", Description: "True for only repositories with issues " +
		"enabled, false for only those without; both when omitted"},
	capability.Argument{Name: "order_by", Description: "CREATED_AT, NAME, PUSHED_AT, STARGAZERS, or UPDATED_AT; " +
		"NAME when omitted"},
	capability.Argument{Name: "direction", Description: "ASC or DESC; ASC when omitted"},
	capability.Argument{Name: "admin_counts", Description: "true to also read the members that can need " +
		"push or admin rights, such as collaborators, deploy keys, branch protection rules, and vulnerability " +
		"alerts; a member GitHub refuses the token is then absent and named in unreadable. false when omitted"})

var ownerListArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 100; 30 when omitted"},
	{Name: "cursor", Description: "Opaque next_cursor of a previous batch of the same owner; the first batch " +
		"when omitted"},
}

var projectsList = capability.Descriptor{
	ID:      Provider + ".projects.list",
	Version: 1,
	Title:   "List GitHub projects of an owner",
	Description: "List one bounded batch of the projects of a user or an organization a connection " +
		"allows, in number order unless order_by says otherwise, with number, title, URL, whether each is " +
		"closed, and the members of GitHub's project; the repositories of a project are cut at 10, with their " +
		"total count and a truncated marker",
	Tags:        []string{"github", "projects", "owner", "list", "planning"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(projectListInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"projects":{"type":"array","items":{"type":"object","properties":{"project":{"type":"string"},` +
		`"number":{"type":"integer"},"title":{"type":"string"},"url":{"type":"string"},"closed":{"type":"boolean"},` +
		projectWidthProperties + `},` +
		`"required":["project","number","title","closed"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["projects","has_more"],"additionalProperties":false}`),
	Arguments: projectListArguments,
	Fields: append([]capability.Field{
		{Name: "projects", Description: "Projects of the owner the connection allows: project as " +
			"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER, number, title (untrusted data), url, closed, " +
			"and the members described below, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the owner may hold further projects; a full batch alone " +
			"never means the end"},
	}, projectWidthFields...),
	Examples: []capability.Example{{
		Description: "List the projects of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org","limit":30}`),
	}, {
		Description: "Find the projects of an organization that can be edited, newest first",
		Arguments: json.RawMessage(`{"owner":"orgs/octo-org","query":"roadmap","order_by":"CREATED_AT",` +
			`"direction":"DESC","min_permission_level":"WRITE"}`),
	}},
}

var repositoriesList = capability.Descriptor{
	ID:      Provider + ".repositories.list",
	Version: 1,
	Title:   "List GitHub repositories of an owner",
	Description: "List one bounded batch of the repositories a user or an organization owns and an explicit " +
		"connection allows, in name order unless order_by says otherwise, with visibility, whether each is " +
		"archived, and the members of GitHub's repository; lists and connections are cut at 10 entries per " +
		"repository, with their total count and a truncated marker",
	Tags:        []string{"github", "repositories", "owner", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(repositoryListInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"repositories":{"type":"array","items":{"type":"object","properties":{"repository":{"type":"string"},` +
		`"visibility":{"type":"string"},"archived":{"type":"boolean"},` + repositoryWidthProperties + `},` +
		`"required":["repository","visibility","archived"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["repositories","has_more"],"additionalProperties":false}`),
	Arguments: repositoryListArguments,
	Fields: append([]capability.Field{
		{Name: "repositories", Description: "Repositories the owner owns and the connection allows: repository " +
			"as OWNER/REPO, visibility (public, private, or internal), archived, and the members described below, " +
			"untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the owner may hold further repositories; a full batch alone " +
			"never means the end"},
	}, repositoryWidthFields...),
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
	ProjectWidth
}

// RepositoryList is one batch of the repositories of an owner.
type RepositoryList struct {
	Repositories []OwnedRepository `json:"repositories"`
	NextCursor   string            `json:"next_cursor,omitempty"`
	HasMore      bool              `json:"has_more"`
}

// RepositorySummary is the compact view of one repository: Repository is the value a repository argument
// takes.
type RepositorySummary struct {
	Repository string `json:"repository"`
	Visibility string `json:"visibility"`
	Archived   bool   `json:"archived"`
}

// OwnedRepository is one entry of the repository list: the compact view and the repository width.
type OwnedRepository struct {
	RepositorySummary
	RepositoryWidth
}

// The GraphQL documents of the owner lists. The owner field is chosen by users or orgs; every value travels as
// a variable. Repositories are the ones the owner owns: GitHub would add those it collaborates on otherwise.
const (
	ownerListHead        = `query($owner:String!,$first:Int!,$after:String`
	projectsDeclarations = `,$query:String,$orderBy:ProjectV2Order!,$minPermissionLevel:ProjectV2PermissionLevel`
	projectsArguments    = `(login:$owner){projectsV2(first:$first,after:$after,query:$query,orderBy:$orderBy,` +
		`minPermissionLevel:$minPermissionLevel){pageInfo{hasNextPage endCursor} edges{cursor node{number title url closed`
	projectsCompact          = projectsArguments + `}}}}}`
	repositoriesDeclarations = `,$privacy:RepositoryPrivacy,$visibility:RepositoryVisibility,$isArchived:Boolean,` +
		`$isFork:Boolean,$isLocked:Boolean,$hasIssuesEnabled:Boolean,$orderBy:RepositoryOrder!`
	repositoriesArguments = `(login:$owner){repositories(first:$first,after:$after,ownerAffiliations:[OWNER],` +
		`privacy:$privacy,visibility:$visibility,isArchived:$isArchived,isFork:$isFork,isLocked:$isLocked,` +
		`hasIssuesEnabled:$hasIssuesEnabled,orderBy:$orderBy){pageInfo{hasNextPage endCursor} edges{cursor node{`
	repositoriesCompact = repositoriesArguments + `name visibility isArchived}}}}}`
)

func repositoriesSelection(adminCounts bool) string {
	return repositoriesArguments + `name visibility isArchived ` + repositorySelection(adminCounts) + `}}}}}`
}

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
	projectWidthRaw
	// unreadable names the members GitHub refused this token in this node.
	unreadable []string
}

type repositoryNodeJSON struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	IsArchived bool   `json:"isArchived"`
	repositoryWidthRaw
	// unreadable names the members GitHub refused this token in this node.
	unreadable []string
}

// ownerListOptions are the paging of an owner list.
type ownerListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// repositoryListOptions are the paging and the filters of the repository list.
type repositoryListOptions struct {
	ownerListOptions
	Privacy          string `json:"privacy"`
	Visibility       string `json:"visibility"`
	IsArchived       *bool  `json:"is_archived"`
	IsFork           *bool  `json:"is_fork"`
	IsLocked         *bool  `json:"is_locked"`
	HasIssuesEnabled *bool  `json:"has_issues_enabled"`
	OrderBy          string `json:"order_by"`
	Direction        string `json:"direction"`
	AdminCounts      bool   `json:"admin_counts"`
	// compact asks for the compact view only, as the target browser needs it.
	compact bool
}

// projectsSelection is the project node of the list with its width, and with the team count when asked.
func projectsSelection(teamCounts bool) string {
	selection := projectsArguments + ` ` + projectWidthSelection
	if teamCounts {
		selection += projectTeamsSelection
	}
	return selection + `}}}}}`
}

// projectListOptions are the paging and the filters of the project list. TeamCounts adds the team count of each
// project and does not bind a cursor.
type projectListOptions struct {
	ownerListOptions
	ProjectFilters
	TeamCounts bool `json:"team_counts"`
	// compact asks for the compact view only, as the target browser needs it.
	compact bool
}

// normalize applies the bounds of one project list request. The filters bind the cursor, and an unfiltered
// list keeps the binding it had without filters.
func (o *projectListOptions) normalize(owner target) ([]byte, string, error) {
	if err := o.ProjectFilters.check(); err != nil {
		return nil, "", err
	}
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return nil, "", err
	}
	o.Limit = limit
	parts := []any{"projects", strings.ToLower(owner.String())}
	if extra := o.ProjectFilters.binding(); extra != nil {
		parts = append(parts, extra)
	}
	binding := fingerprint(parts...)
	after, err := decodeCursor(binding, o.Cursor)
	return binding, after, err
}

// filters are the values that bind a cursor and travel as GraphQL variables. Unset filters are left out, so
// GitHub applies none; the order defaults to the name, ascending.
func (o *repositoryListOptions) filters() map[string]any {
	filters := map[string]any{}
	for name, value := range map[string]string{"privacy": o.Privacy, "visibility": o.Visibility} {
		if value != "" {
			filters[name] = value
		}
	}
	for name, value := range map[string]*bool{"isArchived": o.IsArchived, "isFork": o.IsFork,
		"isLocked": o.IsLocked, "hasIssuesEnabled": o.HasIssuesEnabled} {
		if value != nil {
			filters[name] = *value
		}
	}
	order := map[string]string{"field": "NAME", "direction": "ASC"}
	if o.OrderBy != "" {
		order["field"] = o.OrderBy
	}
	if o.Direction != "" {
		order["direction"] = o.Direction
	}
	filters["orderBy"] = order
	return filters
}

// normalize applies the bounds of one repository list request. GitHub refuses privacy together with
// visibility, so the request is refused before a credential is resolved. The filters bind the cursor, and an
// unfiltered list keeps the binding it had without filters.
func (o *repositoryListOptions) normalize(owner target) ([]byte, string, error) {
	if o.Privacy != "" && o.Visibility != "" {
		return nil, "", invalidRequest("privacy and visibility cannot be combined; pass only one of them")
	}
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return nil, "", err
	}
	o.Limit = limit
	parts := []any{"repositories", strings.ToLower(owner.String())}
	if filters := o.filters(); o.Privacy != "" || o.Visibility != "" || len(filters) > 1 || o.OrderBy != "" ||
		o.Direction != "" {
		parts = append(parts, filters)
	}
	if o.AdminCounts {
		parts = append(parts, "admin_counts")
	}
	binding := fingerprint(parts...)
	after, err := decodeCursor(binding, o.Cursor)
	return binding, after, err
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

// ownerRules selects the rules of the entries of an owner list.
func ownerRules(list string) refusalRules {
	if list == "projectsV2" {
		return projectRules()
	}
	return repositoryRules(list)
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
func ownerPage[T any](ctx context.Context, c *Client, op, declarations, selection, field string, first int,
	after string, filters map[string]any, mark func(*T, []string) bool) (*connectionJSON[T], error) {
	variables := map[string]any{"owner": c.target.owner, "first": first, "after": nil}
	for name, value := range filters {
		variables[name] = value
	}
	if after != "" {
		variables["after"] = after
	}
	var answer struct {
		Owner map[string]*connectionJSON[T] `json:"owner"`
	}
	document := ownerListHead + declarations + "){owner:" + c.target.ownerField() + selection
	refused := func(err error) error {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			rights := ownerRights[field]
			failure.Message = permissionMessage(subject{in: c.target, what: rights.what}, false) + rights.needs
		}
		return err
	}
	var unreadable map[int][]string
	if mark == nil {
		if err := c.graphql(ctx, op, document, variables, &answer); err != nil {
			return nil, refused(err)
		}
	} else {
		envelope, err := c.post(ctx, op, document, variables, false)
		if err != nil {
			return nil, err
		}
		var rest []graphQLError
		rest, unreadable = splitUnreadable(envelope.Errors, ownerRules(field))
		if len(rest) > 0 {
			return nil, refused(c.graphQLFailure(op, rest, variables, false))
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, &answer) != nil {
			return nil, invalidResponse(op, false)
		}
	}
	if answer.Owner == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	page := answer.Owner[field]
	if page == nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub returned an owner without its " + strings.ToLower(field)}
	}
	for index, members := range unreadable {
		// A refused member that GitHub cannot null on its own empties the whole node; the repository is then
		// unknown, and the page fails like a refusal instead of dropping the repository silently.
		if index >= len(page.Edges) || !mark(&page.Edges[index].Node, members) {
			return nil, refused(c.graphQLFailure(op, []graphQLError{{Type: "FORBIDDEN",
				Path: []any{"owner", field}}}, variables, false))
		}
	}
	return page, nil
}

// listProjects reads one batch of the projects of the bound owner that the targets show.
func (c *Client) listProjects(ctx context.Context, options projectListOptions, binding []byte,
	after string) (*ProjectList, error) {
	const op = "list projects"
	owner := c.target
	shown := func(node projectNodeJSON) target {
		return target{kind: kindProject, scope: owner.scope, owner: owner.owner, number: node.Number}
	}
	selection := projectsSelection(options.TeamCounts)
	if options.compact {
		selection = projectsCompact
	}
	mark := func(node *projectNodeJSON, members []string) bool {
		node.unreadable = members
		return node.Number > 0
	}
	if options.compact {
		mark = nil
	}
	nodes, next, err := scanOwner(op, "projects", options.Limit, after, binding,
		func(first int, after string) (*connectionJSON[projectNodeJSON], error) {
			return ownerPage[projectNodeJSON](ctx, c, op, projectsDeclarations, selection, "projectsV2", first, after,
				options.ProjectFilters.variables(), mark)
		}, func(node projectNodeJSON) bool { return node.Number > 0 && c.allowed.shows(owner, shown(node)) })
	if err != nil {
		return nil, err
	}
	result := &ProjectList{Projects: make([]ProjectSummary, 0, len(nodes)), NextCursor: next, HasMore: next != ""}
	for _, node := range nodes {
		summary := ProjectSummary{Project: shown(node).String(), Number: node.Number, Title: node.Title, URL: node.URL,
			Closed: node.Closed}
		if !options.compact {
			summary.ProjectWidth = node.projectWidthRaw.width(c.allowed)
			summary.Unreadable = node.unreadable
		}
		result.Projects = append(result.Projects, summary)
	}
	return result, nil
}

// listRepositories reads one batch of the repositories the bound owner owns that the targets show.
func (c *Client) listRepositories(ctx context.Context, options repositoryListOptions, binding []byte,
	after string) (*RepositoryList, error) {
	const op = "list repositories"
	owner := c.target
	shown := func(node repositoryNodeJSON) target {
		return target{kind: kindRepository, owner: owner.owner, repo: node.Name}
	}
	selection := repositoriesSelection(options.AdminCounts)
	if options.compact {
		selection = repositoriesCompact
	}
	mark := func(node *repositoryNodeJSON, members []string) bool {
		node.unreadable = members
		return node.Name != ""
	}
	if options.compact {
		mark = nil
	}
	nodes, next, err := scanOwner(op, "repositories", options.Limit, after, binding,
		func(first int, after string) (*connectionJSON[repositoryNodeJSON], error) {
			return ownerPage[repositoryNodeJSON](ctx, c, op, repositoriesDeclarations, selection, "repositories",
				first, after, options.filters(), mark)
		}, func(node repositoryNodeJSON) bool {
			return validRepoName(node.Name) && c.allowed.shows(owner, shown(node))
		})
	if err != nil {
		return nil, err
	}
	result := &RepositoryList{Repositories: make([]OwnedRepository, 0, len(nodes)), NextCursor: next,
		HasMore: next != ""}
	for _, node := range nodes {
		summary := OwnedRepository{RepositorySummary: RepositorySummary{Repository: shown(node).argument(),
			Visibility: strings.ToLower(node.Visibility), Archived: node.IsArchived}}
		if !options.compact {
			summary.RepositoryWidth = node.repositoryWidthRaw.width(c.allowed)
			summary.Unreadable = node.unreadable
		}
		result.Repositories = append(result.Repositories, summary)
	}
	return result, nil
}

func invokeProjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options projectListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list owner")
	}
	owner, err := selectOwner(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	binding, after, err := options.normalize(owner)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, owner)
	if err != nil {
		return nil, err
	}
	return owner.locate(client.listProjects(ctx, options, binding, after))
}

func invokeRepositoriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options repositoryListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list owner")
	}
	owner, err := selectOwner(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	binding, after, err := options.normalize(owner)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, owner)
	if err != nil {
		return nil, err
	}
	return owner.locate(client.listRepositories(ctx, options, binding, after))
}

// refusalRules locate the members GitHub refused inside one entry of a list: the path before the entry index,
// whether a "node" segment follows the index, the members of the compact view, which are never set aside, and
// the output name of a member.
type refusalRules struct {
	prefix  []string
	node    bool
	compact []string
	name    func(string) string
}

// splitUnreadable separates the errors of members GitHub refused inside one entry of a list from all others.
// Only FORBIDDEN and INSUFFICIENT_SCOPES at the path of the list, the index, and a member qualify, and never a
// member of the compact view; every other error is returned unchanged. The members come back by entry index
// under the names they have in the output.
func splitUnreadable(errs []graphQLError, rules refusalRules) (rest []graphQLError, unreadable map[int][]string) {
	for _, e := range errs {
		index, member, ok := rules.memberPath(e)
		if !ok {
			rest = append(rest, e)
			continue
		}
		if unreadable == nil {
			unreadable = map[int][]string{}
		}
		if !slices.Contains(unreadable[index], member) {
			unreadable[index] = append(unreadable[index], member)
		}
	}
	for _, members := range unreadable {
		slices.Sort(members)
	}
	return rest, unreadable
}

var memberKey = regexp.MustCompile(`^[A-Za-z]{1,64}$`)

func (r refusalRules) memberPath(e graphQLError) (int, string, bool) {
	if e.Type != "FORBIDDEN" && e.Type != "INSUFFICIENT_SCOPES" {
		return 0, "", false
	}
	p := e.Path
	at := len(r.prefix)
	skip := 1
	if r.node {
		skip = 2
	}
	if len(p) <= at+skip {
		return 0, "", false
	}
	for i, segment := range r.prefix {
		if pathSegment(p, i) != segment {
			return 0, "", false
		}
	}
	index, isNumber := p[at].(float64)
	member := pathSegment(p, at+skip)
	if !isNumber || index < 0 || index != float64(int(index)) || (r.node && pathSegment(p, at+1) != "node") ||
		!memberKey.MatchString(member) || slices.Contains(r.compact, member) {
		return 0, "", false
	}
	return int(index), r.name(member), true
}

// repositoryRules are the rules of the members of a repository of an owner list.
func repositoryRules(list string) refusalRules {
	return refusalRules{prefix: []string{"owner", list, "edges"}, node: true,
		compact: []string{"name", "visibility", "isArchived"}, name: outputMember}
}

// snakeMember names the output member a GraphQL member becomes; a connection read for its count becomes
// <name>_total_count.
func snakeMember(member string, totals ...string) string {
	name := regexp.MustCompile(`[A-Z]`).ReplaceAllStringFunc(member, func(s string) string { return "_" + strings.ToLower(s) })
	if slices.Contains(totals, member) {
		return name + "_total_count"
	}
	return name
}

// projectRules are the rules of the members of a project of an owner list. A project holds teams only for a
// token that may read the organizations, so its count can be refused.
func projectRules() refusalRules {
	return refusalRules{prefix: []string{"owner", "projectsV2", "edges"}, node: true,
		compact: []string{"number", "title", "url", "closed"}, name: func(member string) string {
			return snakeMember(member, "items", "fields", "views", "workflows", "statusUpdates", "teams")
		}}
}

// teamRules are the rules of the members of a team of a project.
func teamRules() refusalRules {
	return refusalRules{prefix: []string{"owner", "projectV2", "teams", "nodes"},
		compact: []string{"slug", "name"}, name: func(member string) string {
			return snakeMember(member, "members", "childTeams", "repositories", "invitations")
		}}
}

// outputMember names the output member a GraphQL member of the repository becomes.
func outputMember(member string) string {
	if member == "repositoryName" {
		return "name"
	}
	return snakeMember(member, repositoryTotalMembers...)
}
