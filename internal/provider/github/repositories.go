package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// Repository lifecycle and collaborators. github.repositories.create makes one new repository, under the
// token's own account or, with owner, an organization a connection names as an owner target;
// private by default. github.repositories.fork forks one repository a connection allows into the
// token's own account or, with organization, an organization it names as an owner target; GitHub answers
// asynchronously, so the fork may still be importing when the answer names it. Creating or forking under the
// token's own account, rather than a repository or a project the connection names, reads the account behind
// the token as a whole, the same account-wide guard the account and star tools apply, since that account may
// belong to a customer other than the one the connection's targets name. github.repositories.delete deletes
// one repository permanently and is offered only where a connection's tools list names it; confirm_name must
// repeat the repository's owner/name exactly, checked before any credential is resolved, since this cannot be
// undone. github.collaborators.list reads the collaborators of a repository with their login, account type,
// role, and permissions, filtered by affiliation and permission, paged; it never answers an email address or
// any other personal detail GitHub's collaborator object may carry.

// repositoryNameSchema mirrors validRepoName loosely; the check itself refuses "." and ".." and any other
// character validRepoName does not allow.
const repositoryNameSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9._-]{1,100}$"}`

const repositoryDescriptionSchema = `{"type":"string","maxLength":350}`

// Permission messages of the repository lifecycle tools. GitHub decides on every request; a message names
// what such a request needs without claiming what the configured token holds.
const (
	repositoryCreatePermission = "GitHub refused this repository create; it needs repo on a classic token, or " +
		"Administration: read and write on a fine-grained token, and, under an organization, permission to " +
		"create repositories there"
	repositoryForkPermission = "GitHub refused this fork; it needs repo on a classic token, or Contents: read " +
		"on a fine-grained token"
	repositoryDeletePermission = "GitHub refused this delete of a repository; it needs delete_repo plus repo " +
		"on a classic token, or Administration: read and write on a fine-grained token"
	collaboratorsReadPermission = "GitHub refused this token the collaborators of this repository; reading " +
		"them needs repo on a classic token, or Metadata: read on a fine-grained token"
)

const repositoryCreatedOutput = `{"type":"object","properties":{"repository":{"type":"string"},` +
	`"owner":{"type":"string"},"private":{"type":"boolean"},"default_branch":{"type":"string"},` +
	`"url":{"type":"string"}},"required":["repository","owner","private"],"additionalProperties":false}`

var repositoriesCreate = capability.Descriptor{
	ID:      Provider + ".repositories.create",
	Version: 1,
	Title:   "Create a GitHub repository",
	Description: "Create one new repository under the token's own account, or, with owner, under an " +
		"organization a connection names as an owner target; private by default; a repeated call " +
		"creates a second repository",
	Tags:     []string{"github", "repositories", "create"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"owner":`+ownerSchema+`,"name":`+repositoryNameSchema+`,"description":`+
		repositoryDescriptionSchema+`,"private":{"type":"boolean"},"auto_init":{"type":"boolean"}`, "name"),
	OutputSchema: json.RawMessage(repositoryCreatedOutput),
	Arguments: []capability.Argument{
		{Name: "owner", Description: "Organization to create the repository in, as orgs/LOGIN; left out " +
			"creates it under the token's own account; must be an organization the connection names as an " +
			"owner target when it lists any targets"},
		{Name: "name", Description: "Repository name, 1 to 100 characters", Required: true},
		{Name: "description", Description: "Short description, at most 350 characters"},
		{Name: "private", Description: "false makes the repository public; true (the default) keeps it private"},
		{Name: "auto_init", Description: "true creates an initial commit with a README; false (the default) " +
			"creates an empty repository with no branches"},
	},
	Fields: []capability.Field{
		{Name: "repository", Description: "Repository GitHub created, as OWNER/REPO"},
		{Name: "owner", Description: "Account the repository was created under, as users/LOGIN or orgs/LOGIN"},
		{Name: "private", Description: "True while the repository is private"},
		{Name: "default_branch", Description: "Default branch name, present once GitHub set one"},
		{Name: "url", Description: "Web address of the repository"},
	},
	Examples: []capability.Example{{
		Description: "Create a private repository under an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org","name":"example"}`),
	}},
}

const repositoryForkOutput = `{"type":"object","properties":{"repository":{"type":"string"},` +
	`"fork":{"type":"string"},"private":{"type":"boolean"},"url":{"type":"string"},` +
	`"accepted":{"type":"boolean"}},"required":["repository","fork","private","accepted"],` +
	`"additionalProperties":false}`

var repositoriesFork = capability.Descriptor{
	ID:      Provider + ".repositories.fork",
	Version: 1,
	Title:   "Fork a GitHub repository",
	Description: "Fork a repository a connection allows into the token's own account or, with " +
		"organization, an organization the connection names as an owner target; GitHub creates the fork " +
		"asynchronously, so it may still be importing once this answers",
	Tags:     []string{"github", "repositories", "fork", "create"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"organization":` + ownerSchema + `,"name":` + repositoryNameSchema +
		`,"default_branch_only":{"type":"boolean"}`),
	OutputSchema: json.RawMessage(repositoryForkOutput),
	Arguments: []capability.Argument{
		{Name: "organization", Description: "Organization to fork into, as orgs/LOGIN; left out forks into the " +
			"token's own account; must be an organization the connection names as an owner target when it " +
			"lists any targets"},
		{Name: "name", Description: "New name of the fork; the source repository's name when left out"},
		{Name: "default_branch_only", Description: "true forks only the default branch; false (the default) " +
			"forks every branch"},
	},
	Fields: []capability.Field{
		{Name: "fork", Description: "New repository GitHub created, as OWNER/REPO"},
		{Name: "private", Description: "True while the fork is private"},
		{Name: "url", Description: "Web address of the fork"},
		{Name: "accepted", Description: "True once GitHub queued the fork; it may still be importing"},
	},
	Examples: []capability.Example{{
		Description: "Fork a repository into an organization",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","organization":"orgs/octo-fork-org"}`),
	}},
}

var repositoriesDelete = capability.Descriptor{
	ID:      Provider + ".repositories.delete",
	Version: 1,
	Title:   "Delete a GitHub repository",
	Description: "Delete one repository a connection allows permanently, with its issues, pull " +
		"requests, and history; confirm_name must repeat the repository as owner/name exactly, checked before " +
		"any credential is resolved, since this cannot be undone. Offered only by a connection whose tools " +
		"list names it",
	Tags:                  []string{"github", "repositories", "delete"},
	Risk:                  guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, dataSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(`"confirm_name":`+repoSchema, "confirm_name"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "confirm_name", Description: "The repository's owner/name, repeated exactly, to confirm the " +
			"delete", Required: true},
	},
	Fields: []capability.Field{{Name: "deleted", Description: "True once GitHub deleted the repository"}},
	Examples: []capability.Example{{
		Description: "Delete a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","confirm_name":"octo-org/example"}`),
	}},
}

// collaboratorAffiliationValues and collaboratorPermissionValues are the filter values github.collaborators.
// list accepts, mirrored from the input schema's enums.
var (
	collaboratorAffiliationValues = []string{"outside", "direct", "all"}
	collaboratorPermissionValues  = []string{"pull", "triage", "push", "maintain", "admin"}
)

const collaboratorPermissionsProperties = `"pull":{"type":"boolean"},"triage":{"type":"boolean"},` +
	`"push":{"type":"boolean"},"maintain":{"type":"boolean"},"admin":{"type":"boolean"}`

const collaboratorProperties = `"login":{"type":"string"},"type":{"type":"string"},` +
	`"role_name":{"type":"string"},"permissions":{"type":"object","properties":{` +
	collaboratorPermissionsProperties + `},"required":["pull","triage","push","maintain","admin"],` +
	`"additionalProperties":false}`

const collaboratorRequired = `"required":["login","type","role_name","permissions"],"additionalProperties":false`

var collaboratorsList = capability.Descriptor{
	ID:      Provider + ".collaborators.list",
	Version: 1,
	Title:   "List GitHub repository collaborators",
	Description: "List one bounded batch of the collaborators of a repository a connection allows, " +
		"with login, account type, role, and permissions; never an email address or another personal detail",
	Tags:     []string{"github", "repositories", "collaborators", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"affiliation":{"type":"string","enum":["outside","direct","all"]},` +
		`"permission":{"type":"string","enum":["pull","triage","push","maintain","admin"]},` + pagingKeys),
	OutputSchema: listOutput("collaborators", collaboratorProperties, collaboratorRequired),
	Arguments: append([]capability.Argument{
		{Name: "affiliation", Description: "outside, direct, or all; all when omitted"},
		{Name: "permission", Description: "Return only collaborators with at least this permission level"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "collaborators", Description: "Collaborators with login, type, role_name, and permissions; " +
			"never an email address or another personal detail"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List outside collaborators",
		Arguments:   json.RawMessage(`{"affiliation":"outside"}`),
	}},
}

// repositoryAdminSubject names the fork or the collaborators a repository lifecycle path below a repository
// addresses: forks or collaborators, with or without a further segment. It is empty for any other path, such
// as the repository route github.repositories.delete addresses on its own, which restSubject already names.
func repositoryAdminSubject(path string) string {
	switch {
	case path == "forks" || strings.HasPrefix(path, "forks/"):
		return "a fork of this repository"
	case path == "collaborators" || strings.HasPrefix(path, "collaborators/"):
		return "the collaborators of this repository"
	}
	return ""
}

// repositoriesOperations binds every repository lifecycle and collaborators tool to its handler.
func repositoriesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: repositoriesCreate, Handler: capability.Handler(invokeRepositoriesCreate)},
		{Descriptor: repositoriesFork, Handler: capability.Handler(invokeRepositoriesFork)},
		{Descriptor: repositoriesDelete, Handler: capability.Handler(invokeRepositoriesDelete)},
		{Descriptor: collaboratorsList, Handler: capability.Handler(invokeCollaboratorsList)},
	}
}

// chooseCreateOwner resolves the optional owner argument of github.repositories.create: given, it must name
// an organization, since GitHub creates a repository only under the token's own account or an organization,
// never under an arbitrary other user, and the organization must be one the connection's targets allow, when
// it lists any. Left out, a unique organization the targets name as an owner target is used as the default;
// otherwise the repository is created under the token's own account, allowed only the same way an
// account-wide read is: without targets, or with targets naming no repository and no project, since the
// account behind the token may belong to a customer other than the one the connection's targets name.
func chooseCreateOwner(allowed allowlist, value string) (target, bool, error) {
	if value != "" {
		return parseAllowedOrganization(allowed, "owner", value)
	}
	if only, ok := allowed.only(kindOwner); ok && only.scope == "orgs" {
		return only, true, nil
	}
	if err := accountWideAllowed(allowed); err != nil {
		return target{}, false, err
	}
	return target{}, false, nil
}

// chooseForkOrganization resolves the optional organization argument of github.repositories.fork the same
// way chooseCreateOwner resolves owner, except that omitting it never defaults to a unique organization
// target: a fork is a copy of a specific, already-bound source repository, so only an organization the
// caller names explicitly, and the connection's targets allow, is ever used in its place.
func chooseForkOrganization(allowed allowlist, value string) (target, bool, error) {
	if value != "" {
		return parseAllowedOrganization(allowed, "organization", value)
	}
	if err := accountWideAllowed(allowed); err != nil {
		return target{}, false, err
	}
	return target{}, false, nil
}

// parseAllowedOrganization reads an owner argument that must name an organization and resolves it against
// the connection's targets, before a credential is resolved. The error never quotes the value.
func parseAllowedOrganization(allowed allowlist, name, value string) (target, bool, error) {
	parsed, err := parseTarget(value)
	if err != nil || parsed.kind != kindOwner || strings.TrimSpace(value) != value {
		return target{}, false, invalidRequest(name + " must be orgs/LOGIN")
	}
	if parsed.scope != "orgs" {
		return target{}, false, invalidRequest(name + " must be an organization, as orgs/LOGIN; GitHub creates " +
			"or forks a repository only under the token's own account or an organization")
	}
	if !allowed.allows(parsed) {
		return target{}, false, invalidRequest(name + " is outside the targets of this connection; pass one " +
			"they allow, or add it to the connection's targets")
	}
	for _, entry := range allowed {
		if entry.kind == kindOwner && strings.EqualFold(entry.String(), parsed.String()) {
			parsed = entry
		}
	}
	return parsed, true, nil
}

// repositoryCreateArguments holds the arguments of github.repositories.create.
type repositoryCreateArguments struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     *bool  `json:"private"`
	AutoInit    bool   `json:"auto_init"`
}

// invokeRepositoriesCreate checks the arguments and the destination owner before a credential is resolved,
// so a refused request never reaches GitHub.
func invokeRepositoriesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments repositoryCreateArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(repositoriesCreate.ID)
	}
	if !validRepoName(arguments.Name) {
		return nil, invalidRequest("name must be a usable repository name")
	}
	if err := checkText("description", arguments.Description); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	owner, bound, err := chooseCreateOwner(allowed, arguments.Owner)
	if err != nil {
		return nil, err
	}
	var client *Client
	if bound {
		client, err = openAt(ctx, resolved, secrets, red, owner)
	} else {
		client, err = Open(ctx, resolved, secrets, red)
	}
	if err != nil {
		return nil, err
	}
	private := true
	if arguments.Private != nil {
		private = *arguments.Private
	}
	return client.createRepository(ctx, owner, bound, arguments.Name, arguments.Description, private, arguments.AutoInit)
}

// RepositoryCreated describes the repository github.repositories.create made.
type RepositoryCreated struct {
	Repository    string `json:"repository"`
	Owner         string `json:"owner"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch,omitempty"`
	URL           string `json:"url,omitempty"`
}

// createRepository creates one repository under the token's own account, or, while bound is true, under
// owner. The request is sent directly, instead of through restChange, so a 422 answer can be read once and,
// when it names an existing repository, turned into a clear refusal instead of the generic rejected message;
// the request is still sent exactly once, and every other status is classified exactly like restChange would.
func (c *Client) createRepository(ctx context.Context, owner target, bound bool, name, description string,
	private, autoInit bool) (*RepositoryCreated, error) {
	const op = "create repository"
	path := "/user/repos"
	if bound {
		path = "/orgs/" + url.PathEscape(owner.owner) + "/repos"
	}
	body := map[string]any{"name": name, "private": private}
	if description != "" {
		body["description"] = description
	}
	if autoInit {
		body["auto_init"] = true
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.rest+path, bytes.NewReader(payload))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	defer c.limiter.HoldFor(mutationInterval)
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnprocessableEntity &&
			bytes.Contains(bytes.ToLower(data), []byte("already exists")) {
			return nil, invalidRequest("GitHub already holds a repository named " + name +
				" there; choose another name")
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return nil, actionsFailure(c.statusError(op, response, true), repositoryCreatePermission)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the GitHub response could not be read within the size limit" + uncertain}
	}
	var answer struct {
		FullName      string `json:"full_name"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		HTMLURL       string `json:"html_url"`
		Owner         struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, invalidResponse(op, true)
	}
	if !validRepository(answer.FullName) || answer.Owner.Login == "" {
		return nil, invalidResponse(op, true)
	}
	if bound && !strings.EqualFold(answer.Owner.Login, owner.owner) {
		return nil, invalidResponse(op, true)
	}
	scope := "users"
	if strings.EqualFold(answer.Owner.Type, "Organization") {
		scope = "orgs"
	}
	return &RepositoryCreated{Repository: answer.FullName, Owner: scope + "/" + answer.Owner.Login,
		Private: answer.Private, DefaultBranch: answer.DefaultBranch, URL: answer.HTMLURL}, nil
}

// repositoryForkArguments holds the arguments of github.repositories.fork.
type repositoryForkArguments struct {
	Organization      string `json:"organization"`
	Name              string `json:"name"`
	DefaultBranchOnly bool   `json:"default_branch_only"`
}

// invokeRepositoriesFork checks the source repository and the destination organization before a credential
// is resolved, so a refused request never reaches GitHub.
func invokeRepositoriesFork(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	var arguments repositoryForkArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(repositoriesFork.ID)
	}
	if arguments.Name != "" && !validRepoName(arguments.Name) {
		return nil, invalidRequest("name must be a usable repository name")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	destination, destinationBound, err := chooseForkOrganization(allowed, arguments.Organization)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.forkRepository(ctx, destination, destinationBound, arguments.Name,
		arguments.DefaultBranchOnly))
}

// RepositoryForked describes the fork github.repositories.fork queued.
type RepositoryForked struct {
	Fork     string `json:"fork"`
	Private  bool   `json:"private"`
	URL      string `json:"url,omitempty"`
	Accepted bool   `json:"accepted"`
}

// forkRepository forks the bound source repository into the token's own account, or, while bound is true,
// into destination. GitHub answers this route asynchronously (202): a large repository may still be
// importing once this returns, though the answer already names the new repository.
func (c *Client) forkRepository(ctx context.Context, destination target, bound bool, name string,
	defaultBranchOnly bool) (*RepositoryForked, error) {
	const op = "fork repository"
	body := map[string]any{}
	if bound {
		body["organization"] = destination.owner
	}
	if name != "" {
		body["name"] = name
	}
	if defaultBranchOnly {
		body["default_branch_only"] = true
	}
	var answer struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		HTMLURL  string `json:"html_url"`
	}
	if err := c.restChange(ctx, op, http.MethodPost, c.repoPath("forks"), body, &answer); err != nil {
		return nil, actionsFailure(err, repositoryForkPermission)
	}
	if !validRepository(answer.FullName) {
		return nil, invalidResponse(op, true)
	}
	return &RepositoryForked{Fork: answer.FullName, Private: answer.Private, URL: answer.HTMLURL, Accepted: true}, nil
}

// invokeRepositoriesDelete checks the repository target and confirm_name before a credential is resolved, so
// a mismatched confirmation never becomes a provider call.
func invokeRepositoriesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	var arguments struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(repositoriesDelete.ID)
	}
	if arguments.ConfirmName != bound.argument() {
		return nil, invalidRequest("confirm_name must repeat the repository exactly: " + bound.argument())
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.deleteRepository(ctx))
}

// RepositoryDeleted is the answer to a deleted repository.
type RepositoryDeleted struct {
	Deleted bool `json:"deleted"`
}

// deleteRepository deletes the bound repository in one request that is never repeated.
func (c *Client) deleteRepository(ctx context.Context) (*RepositoryDeleted, error) {
	const op = "delete repository"
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
	if err := c.restChange(ctx, op, http.MethodDelete, path, struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, repositoryDeletePermission)
	}
	return &RepositoryDeleted{Deleted: true}, nil
}

// collaboratorListOptions are the filters and paging of github.collaborators.list.
type collaboratorListOptions struct {
	Affiliation string `json:"affiliation"`
	Permission  string `json:"permission"`
	Limit       int    `json:"limit"`
	Cursor      string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func (o *collaboratorListOptions) query() url.Values {
	values := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if o.Affiliation != "" {
		values.Set("affiliation", o.Affiliation)
	}
	if o.Permission != "" {
		values.Set("permission", o.Permission)
	}
	return values
}

// normalize applies the bounds of one collaborator list request and resolves the REST page its cursor
// continues at, bound to the repository and its filters.
func (o *collaboratorListOptions) normalize(bound target) error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	if o.Affiliation != "" && !containsFold(collaboratorAffiliationValues, o.Affiliation) {
		return invalidRequest("affiliation must be outside, direct, or all")
	}
	if o.Permission != "" && !containsFold(collaboratorPermissionValues, o.Permission) {
		return invalidRequest("permission must be pull, triage, push, maintain, or admin")
	}
	o.binding = fingerprint("collaborators", "list", bound.String(), strings.ToLower(o.Affiliation),
		strings.ToLower(o.Permission))
	o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit)
	return err
}

// invokeCollaboratorsList checks the target, the filters, and the cursor before a credential is resolved, so
// a refused request never becomes a provider call.
func invokeCollaboratorsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options collaboratorListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable(collaboratorsList.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := options.normalize(bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listCollaborators(ctx, &options))
}

// CollaboratorPermissions are the individual permissions of one collaborator.
type CollaboratorPermissions struct {
	Pull     bool `json:"pull"`
	Triage   bool `json:"triage"`
	Push     bool `json:"push"`
	Maintain bool `json:"maintain"`
	Admin    bool `json:"admin"`
}

// RepositoryCollaborator is the compact, non-personal view of one collaborator: login, account type, role, and
// permissions only, never an email address or another personal detail.
type RepositoryCollaborator struct {
	Login       string                  `json:"login"`
	Type        string                  `json:"type"`
	RoleName    string                  `json:"role_name"`
	Permissions CollaboratorPermissions `json:"permissions"`
}

// CollaboratorList is one batch of the collaborators of a repository.
type CollaboratorList struct {
	Collaborators []RepositoryCollaborator `json:"collaborators"`
	NextCursor    string                   `json:"next_cursor,omitempty"`
	HasMore       bool                     `json:"has_more"`
}

// listCollaborators reads one page of the collaborators of the bound repository. GitHub's collaborator
// object may carry further personal fields such as an email address; only login, type, role_name, and
// permissions are ever read into the result.
func (c *Client) listCollaborators(ctx context.Context, o *collaboratorListOptions) (*CollaboratorList, error) {
	const op = "list collaborators"
	var raw []struct {
		Login       string `json:"login"`
		Type        string `json:"type"`
		RoleName    string `json:"role_name"`
		Permissions struct {
			Pull     bool `json:"pull"`
			Triage   bool `json:"triage"`
			Push     bool `json:"push"`
			Maintain bool `json:"maintain"`
			Admin    bool `json:"admin"`
		} `json:"permissions"`
	}
	hasNext, err := c.restPage(ctx, op, c.repoPath("collaborators"), o.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, collaboratorsReadPermission)
	}
	result := &CollaboratorList{Collaborators: make([]RepositoryCollaborator, 0, len(raw))}
	for _, entry := range raw {
		if entry.Login == "" {
			return nil, invalidEntry(op, "a collaborator")
		}
		result.Collaborators = append(result.Collaborators, RepositoryCollaborator{Login: entry.Login, Type: entry.Type,
			RoleName: entry.RoleName, Permissions: CollaboratorPermissions{Pull: entry.Permissions.Pull,
				Triage: entry.Permissions.Triage, Push: entry.Permissions.Push,
				Maintain: entry.Permissions.Maintain, Admin: entry.Permissions.Admin}})
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}
