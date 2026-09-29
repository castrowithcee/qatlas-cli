package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// github.issuetypes.list reads the issue types an organization declares. GitHub scopes issue types to an
// organization as a whole, never to one repository or one user, so this tool takes an organization argument
// checked against the connection's targets the broad way github.repositories.list checks the owner of an
// owner list: allowed when the connection names no targets at all, when it names the organization as an
// owner target itself, or when it names a repository target of that organization, since a connection scoped
// to a repository already trusts the organization that owns it; a connection whose targets name only
// projects, or only a different organization, refuses the call as invalid-request before any credential is
// resolved. A user owner is refused as well, because issue types belong to an organization, not a user.
//
// Verified 2026-09-29 against https://docs.github.com/en/rest/orgs/issue-types (REST API, X-GitHub-Api-Version
// 2022-11-28): "List issue types for an organization" (GET /orgs/{org}/issue-types) is a stable, generally
// available REST endpoint, not a preview feature; it returns every issue type as a plain array, with no
// pagination parameters of its own. OAuth and classic personal access tokens need the read:org scope.

const issueTypeProperties = `"id":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"color":{"type":"string"},"is_enabled":{"type":"boolean"}`
const issueTypeRequired = `"required":["id","name","is_enabled"],"additionalProperties":false`

// issueTypesOwnerArgument and issueTypesOwnerField describe github.issuetypes.list's own owner argument,
// broader than organizationArgument/organizationField: a repository target of the organization allows the
// call as well, the way a repository target allows github.repositories.list to list its owner's repositories.
var issueTypesOwnerArgument = capability.Argument{Name: "owner", Description: "Organization as orgs/LOGIN " +
	"whose issue types this call reads; optional when the connection's targets name exactly one owner; must " +
	"lie inside the targets when the connection lists any, as an owner target itself or as the owner of a " +
	"repository target"}

var issueTypesOwnerField = capability.Field{Name: "owner", Description: "Organization the tool listed, as " +
	"orgs/LOGIN; the one a default chose when the argument was left out"}

var issueTypesList = capability.Descriptor{
	ID:      Provider + ".issuetypes.list",
	Version: 1,
	Title:   "List GitHub issue types",
	Description: "List the issue types an organization declares, that an explicit connection allows: name, " +
		"description, color, and whether each is enabled; github.issues.update's own type argument names one " +
		"of these by name",
	Tags:                       []string{"github", "issues", "issuetypes", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"issue_types":{"type":"array","items":` +
		`{"type":"object","properties":{` + issueTypeProperties + `},` + issueTypeRequired + `}}},` +
		`"required":["issue_types"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "issue_types", Description: "Issue types of the organization, in the order GitHub returns " +
			"them: id, name and description (untrusted data), color, and is_enabled"},
	},
	Examples: []capability.Example{{
		Description: "List the issue types of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org"}`),
	}},
}

// issueTypesReadPermission names what a token needs to read the issue types of an organization. GitHub
// decides on every request; this names the requirement and never claims what the configured token holds.
// The fine-grained permission is stated cautiously, since Qatlas cannot verify it against GitHub live.
const issueTypesReadPermission = "GitHub refused this token the issue types of this organization; reading " +
	"them needs read:org on a classic token, or, as far as GitHub documents it, the organization permission " +
	"Issue types: read, or Administration: read, on a fine-grained token"

// selectIssueTypesOwner resolves github.issuetypes.list's own owner argument against the connection's
// targets: allowed when the connection names no targets, when it lists the organization as an owner target
// itself, or when it lists a repository of that organization, the same broad rule github.repositories.list
// applies to its own owner argument. It runs before a credential is resolved, so a refused organization
// never becomes a secret read or a provider call.
func selectIssueTypesOwner(resolved *config.Resolved, raw json.RawMessage) (target, error) {
	owner, err := selectOwner(resolved, kindRepository, raw)
	if err != nil {
		return target{}, err
	}
	if owner.scope != "orgs" {
		return target{}, invalidRequest("owner must be an organization, as orgs/LOGIN; issue types belong to " +
			"an organization, not a user")
	}
	return owner, nil
}

func issueTypesPath(org target) string {
	return "/orgs/" + url.PathEscape(org.owner) + "/issue-types"
}

// issueTypeJSON is the REST issue type shape github.issuetypes.list normalises into an IssueType.
type issueTypeJSON struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
	IsEnabled   bool    `json:"is_enabled"`
}

// IssueType is one issue type of an organization.
type IssueType struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	IsEnabled   bool   `json:"is_enabled"`
}

// IssueTypeList is every issue type of an organization; GitHub's own route returns them all at once, with
// no pagination of its own.
type IssueTypeList struct {
	IssueTypes []IssueType `json:"issue_types"`
}

func (c *Client) listIssueTypes(ctx context.Context) (*IssueTypeList, error) {
	const op = "list issue types"
	var raw []issueTypeJSON
	if err := c.rest(ctx, op, issueTypesPath(c.target), &raw); err != nil {
		return nil, actionsFailure(err, issueTypesReadPermission)
	}
	result := &IssueTypeList{IssueTypes: make([]IssueType, 0, len(raw))}
	for _, item := range raw {
		if item.ID < 1 || strings.TrimSpace(item.Name) == "" {
			return nil, invalidEntry(op, "an issue type")
		}
		entry := IssueType{ID: item.ID, Name: item.Name, IsEnabled: item.IsEnabled}
		if item.Description != nil {
			entry.Description = *item.Description
		}
		if item.Color != nil {
			entry.Color = *item.Color
		}
		result.IssueTypes = append(result.IssueTypes, entry)
	}
	return result, nil
}

func invokeIssueTypesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	owner, err := selectIssueTypesOwner(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, owner)
	if err != nil {
		return nil, err
	}
	return owner.locate(client.listIssueTypes(ctx))
}

func issueTypesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: issueTypesList, Handler: capability.Handler(invokeIssueTypesList)},
	}
}
