package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const repositoryRenamePermission = "GitHub refused this rename of a repository; it needs repo on a classic " +
	"token with the Admin role on the repository, or Administration: read and write on a fine-grained token"

const repositoryRenamedOutput = `{"type":"object","properties":{"repository":{"type":"string"},` +
	`"previous":{"type":"string"},"url":{"type":"string"}},"required":["repository","previous"],` +
	`"additionalProperties":false}`

var repositoriesRename = capability.Descriptor{
	ID:      Provider + ".repositories.rename",
	Version: 1,
	Title:   "Rename a GitHub repository",
	Description: "Rename one repository a connection allows to a new name under the same owner; the new name " +
		"must be allowed by the connection's targets as well, and not excluded. GitHub forwards the old name " +
		"on the web and for Git, Qatlas never follows it. Offered only by a connection whose tools list names it",
	Tags:                  []string{"github", "repositories", "rename", "update"},
	Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(`"repository":`+repoSchema+`,"name":`+repositoryNameSchema, "name"),
	OutputSchema:          json.RawMessage(repositoryRenamedOutput),
	Arguments: []capability.Argument{
		repositoryArgument,
		{Name: "name", Description: "New repository name without the owner, 1 to 100 characters; a change of " +
			"letter case alone is allowed", Required: true},
	},
	Fields: []capability.Field{
		{Name: "repository", Description: "Repository under its new name, as OWNER/REPO"},
		{Name: "previous", Description: "Repository under its former name, as OWNER/REPO"},
		{Name: "url", Description: "Web address of the repository, untrusted data, never fetched"},
	},
	Examples: []capability.Example{{
		Description: "Rename a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","name":"example-renamed"}`),
	}},
}

// invokeRepositoriesRename checks the current and the new name against the connection's targets before a
// credential is resolved, so a refused rename never reaches GitHub.
func invokeRepositoriesRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	var arguments struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(repositoriesRename.ID)
	}
	if bound.pattern() {
		return nil, invalidRequest("repository must name one repository")
	}
	// GitHub refuses a name ending in .git, which validRepoName alone allows.
	if !validRepoName(arguments.Name) || strings.HasSuffix(strings.ToLower(arguments.Name), ".git") {
		return nil, invalidRequest("name must be a usable repository name")
	}
	if arguments.Name == bound.repo {
		return nil, invalidRequest("name must differ from the current name")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	// The new name must lie inside the targets too, or a rename would take the repository out of what the
	// connection covers. The errors quote no name.
	renamed := target{kind: kindRepository, owner: bound.owner, repo: arguments.Name}
	if allowed.excluded(renamed) {
		return nil, invalidRequest("the new name is not available on this connection; pass another one")
	}
	if !allowed.allows(renamed) {
		return nil, invalidRequest("the new name is outside the targets of this connection; add it to the " +
			"connection's targets first")
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return client.renameRepository(ctx, arguments.Name)
}

// RepositoryRenamed is the answer to a renamed repository.
type RepositoryRenamed struct {
	Repository string `json:"repository"`
	Previous   string `json:"previous"`
	URL        string `json:"url,omitempty"`
}

// renameRepository renames the bound repository in one request that is never repeated.
func (c *Client) renameRepository(ctx context.Context, name string) (*RepositoryRenamed, error) {
	const op = "rename repository"
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
	var answer struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	}
	if err := c.restChange(ctx, op, http.MethodPatch, path, map[string]string{"name": name}, &answer); err != nil {
		return nil, renameFailure(err)
	}
	// The answer must name the owner and the name that were asked for; anything else leaves the state unclear.
	owner, renamed, ok := strings.Cut(answer.FullName, "/")
	if !validRepository(answer.FullName) || !ok || !strings.EqualFold(owner, c.target.owner) || renamed != name {
		return nil, invalidResponse(op, true)
	}
	return &RepositoryRenamed{Repository: answer.FullName, Previous: c.target.argument(), URL: answer.HTMLURL}, nil
}

// renameFailure turns GitHub's 422 into an invalid request, since the name is taken or not allowed there,
// and names the needed rights of a refusal. No provider text is copied.
func renameFailure(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Original != nil &&
		failure.Original.Status == http.StatusUnprocessableEntity {
		return invalidRequest("GitHub refused the new name; it is already taken by another repository of this " +
			"owner or not allowed")
	}
	return actionsFailure(err, repositoryRenamePermission)
}
