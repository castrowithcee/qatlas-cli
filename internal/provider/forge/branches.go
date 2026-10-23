package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The branch tools. Every path carries the repository, chosen against the targets, so the instance itself binds
// a branch to its repository; no evidence read is needed. A branch name travels as one escaped path segment,
// its slashes included, and as plain text in a body. No tool offers a force update: update-ref sends only a
// new commit and an optional expected old one.

const (
	branchSensitivity = "-repository-data"

	branchSchemaName = `{"type":"string","minLength":1,"maxLength":255}`
	branchSchemaID   = `{"type":"string","minLength":7,"maxLength":64,"pattern":"^[0-9a-fA-F]+$"}`
)

var branchPersonMembers = []member{
	{name: "name", kind: kindText, note: "Name in the commit, untrusted data; personal data"},
	{name: "username", kind: kindText, note: "User name of the account, untrusted data; personal data"},
	{name: "email", kind: kindText, note: "Email address in the commit; personal data"},
}

// The raw payload and signature of a verification are left out: they are large signed blobs that repeat the
// committer's identity.
var branchVerificationMembers = []member{
	{name: "verified", kind: kindFlag, note: "True when the signature is verified"},
	{name: "reason", kind: kindText, note: "Why it is or is not verified"},
	{name: "signer", kind: kindObject, sub: branchPersonMembers, note: "Signer; personal data"},
}

var branchCommitMembers = []member{
	{name: "id", kind: kindText, note: "Commit hash"},
	{name: "message", kind: kindText, max: maxPullBody, note: "Commit message, untrusted data, at most 64 KiB"},
	{name: "url", kind: kindText, address: true, note: "Address of the commit; left out with user info or query"},
	{name: "timestamp", kind: kindText, note: "Commit time"},
	{name: "author", kind: kindObject, sub: branchPersonMembers, note: "Author as the commit names it; personal data"},
	{name: "committer", kind: kindObject, sub: branchPersonMembers,
		note: "Committer as the commit names it; personal data"},
	{name: "added", kind: kindTexts, note: "Paths the commit added, untrusted data"},
	{name: "modified", kind: kindTexts, note: "Paths the commit changed, untrusted data"},
	{name: "removed", kind: kindTexts, note: "Paths the commit removed, untrusted data"},
	{name: "verification", kind: kindObject, sub: branchVerificationMembers, note: "Signature verification"},
}

var branchMembers = []member{
	{name: "name", kind: kindText, note: "Branch name, untrusted data"},
	{name: "commit", kind: kindObject, sub: branchCommitMembers, note: "Commit at the tip of the branch"},
	{name: "protected", kind: kindFlag, note: "True when a protection rule covers the branch"},
	{name: "required_approvals", kind: kindInteger, note: "Approvals a merge into the branch needs"},
	{name: "enable_status_check", kind: kindFlag, note: "True when a merge needs passing status checks"},
	{name: "status_check_contexts", kind: kindTexts, note: "Status checks a merge needs, untrusted data"},
	{name: "user_can_push", kind: kindFlag, note: "True when the token's user may push to the branch"},
	{name: "user_can_merge", kind: kindFlag, note: "True when the token's user may merge into the branch"},
	{name: "effective_branch_protection_name", kind: kindText,
		note: "Name of the protection rule that applies, untrusted data"},
}

func branchesPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/branches"
}

func branchPath(repo target, name string) string { return branchesPath(repo) + "/" + escape(name) }

// checkBranchID is a commit hash of 7 to 64 hexadecimal digits under the argument's own name.
func checkBranchID(field, value string) error {
	if checkCommitID(value) != nil {
		return invalidArgument(field + " must be a commit ID of 7 to 64 hexadecimal digits")
	}
	return nil
}

var branchArgument = capability.Argument{Name: "branch", Required: true,
	Description: "Branch name; a slash is allowed and sent encoded"}

func branchOutput(p *product) json.RawMessage { return json.RawMessage(schemaOf(p, branchMembers)) }

var branchesList = &tool{
	group: "branches", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"repository":` + repoSchema + `,` + pagingKeys
		arguments := []capability.Argument{repositoryArgument}
		if p == gitea {
			properties += `,"q":{"type":"string","minLength":1,"maxLength":256}`
			arguments = append(arguments, capability.Argument{Name: "q",
				Description: "Only branches whose name matches this text, at most 256 characters; Gitea only"})
		}
		return capability.Descriptor{
			Title: "List " + p.name + " branches",
			Description: "List one batch of the branches of a repository inside the connection's targets with " +
				"every member of each branch; names, commit texts, and identities are untrusted data",
			Tags:         []string{p.id, "branches", "list"},
			Risk:         readRisk(p.id + branchSensitivity),
			InputSchema:  inputSchema(properties),
			OutputSchema: issueListOutputOf(p, "branches", branchMembers),
			Arguments:    append(arguments, pagingArguments...),
			Fields: append([]capability.Field{
				{Name: "branches", Description: "Branches with every member of the branch object"},
				{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
			}, pagingFields...),
			Examples: []capability.Example{{Description: "List the branches of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Q          string `json:"q"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if arguments.Q != "" && p != gitea {
			return nil, otherProduct(p, "q")
		}
		if len(arguments.Q) > maxQueryText || !plainText(arguments.Q) {
			return nil, invalidArgument("q must be a text of at most 256 characters without control characters")
		}
		filters := url.Values{}
		if arguments.Q != "" {
			filters.Set("q", arguments.Q)
		}
		binding := provider.CursorBinding(p.id, "branches.list", strings.ToLower(repo.owner+"/"+repo.repo),
			filters.Encode())
		page, err := startPaging(binding, arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			for key, values := range filters {
				query[key] = values
			}
			var rows []object
			header, err := c.get(ctx, "list branches", branchesPath(repo), query, &rows)
			if err != nil {
				return nil, err
			}
			branches := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					branches = append(branches, project(p, branchMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"branches": branches, "has_more": next != "",
				"repository": repo.owner + "/" + repo.repo}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var branchesGet = &tool{
	group: "branches", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " branch",
			Description: "Read one branch of a repository inside the connection's targets with every member of " +
				"the branch object; names, commit texts, and identities are untrusted data",
			Tags:         []string{p.id, "branches", "get"},
			Risk:         readRisk(p.id + branchSensitivity),
			InputSchema:  inputSchema(`"repository":`+repoSchema+`,"branch":`+branchSchemaName, "branch"),
			OutputSchema: branchOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, branchArgument},
			Fields:       fieldsOf(p, branchMembers),
			Examples: []capability.Example{{Description: "Read a branch",
				Arguments: json.RawMessage(`{"repository":"example/project","branch":"main"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkRefName("branch", arguments.Branch, true); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var branch object
			if _, err := c.get(ctx, "get branch", branchPath(repo, arguments.Branch), nil, &branch); err != nil {
				return nil, err
			}
			return project(p, branchMembers, branch), nil
		}, nil
	},
}

var branchesCreate = &tool{
	group: "branches", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Create a " + p.name + " branch",
			Description: "Create exactly one branch in a repository inside the connection's targets, from the " +
				"given branch, tag, or commit or from the default branch. An existing name is refused by the " +
				"instance.",
			Tags: []string{p.id, "branches", "create"},
			Risk: changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+branchSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"new_branch_name":`+branchSchemaName+
				`,"old_ref_name":`+branchSchemaName, "new_branch_name"),
			OutputSchema: branchOutput(p),
			Arguments: []capability.Argument{repositoryArgument,
				{Name: "new_branch_name", Required: true, Description: "Name of the new branch"},
				{Name: "old_ref_name", Description: "Branch, tag, or commit the branch starts from; the default " +
					"branch when omitted"}},
			Fields: fieldsOf(p, branchMembers),
			Examples: []capability.Example{{Description: "Branch off main",
				Arguments: json.RawMessage(`{"repository":"example/project","new_branch_name":"feature/x","old_ref_name":"main"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository    string `json:"repository"`
			NewBranchName string `json:"new_branch_name"`
			OldRefName    string `json:"old_ref_name"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkRefName("new_branch_name", arguments.NewBranchName, true); err != nil {
			return nil, err
		}
		body := map[string]any{"new_branch_name": arguments.NewBranchName}
		if arguments.OldRefName != "" {
			if err := checkRefName("old_ref_name", arguments.OldRefName, true); err != nil {
				return nil, err
			}
			body["old_ref_name"] = arguments.OldRefName
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var created object
			if err := c.change(ctx, "create branch", http.MethodPost, branchesPath(repo), body, &created); err != nil {
				return nil, err
			}
			return project(p, branchMembers, created), nil
		}, nil
	},
}

var branchesRename = &tool{
	group: "branches", action: "rename", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Rename a " + p.name + " branch",
			Description: "Rename exactly one branch of a repository inside the connection's targets. The " +
				"instance may refuse the default branch, a protected branch, or an existing name.",
			Tags: []string{p.id, "branches", "rename"},
			Risk: changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+branchSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"branch":`+branchSchemaName+`,"name":`+
				branchSchemaName, "branch", "name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"branch":{"type":"string"},` +
				`"name":{"type":"string"},"renamed":{"type":"boolean"}},"required":["branch","name","renamed"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, branchArgument,
				{Name: "name", Required: true, Description: "New branch name"}},
			Fields: []capability.Field{{Name: "branch", Description: "Former name of the branch"},
				{Name: "name", Description: "New name of the branch"},
				{Name: "renamed", Description: "True once the instance renamed the branch"}},
			Examples: []capability.Example{{Description: "Rename a branch",
				Arguments: json.RawMessage(`{"repository":"example/project","branch":"old","name":"new"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
			Name       string `json:"name"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkRefName("branch", arguments.Branch, true); err != nil {
			return nil, err
		}
		if err := checkRefName("name", arguments.Name, true); err != nil {
			return nil, err
		}
		body := map[string]any{"name": arguments.Name}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "rename branch", http.MethodPatch, branchPath(repo, arguments.Branch), body,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"branch": arguments.Branch, "name": arguments.Name, "renamed": true}, nil
		}, nil
	},
}

var branchesUpdateRef = &tool{
	group: "branches", action: "update-ref", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Point a " + p.name + " branch at another commit",
			Description: "Set the reference of exactly one branch of a repository inside the connection's " +
				"targets to another commit; the instance refuses an update that is not a fast-forward, and with " +
				"old_commit_id one whose current tip differs. No force update is offered. Only a connection " +
				"whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "branches", "update-ref"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+branchSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"branch":`+branchSchemaName+`,"new_commit_id":`+
				branchSchemaID+`,"old_commit_id":`+branchSchemaID, "branch", "new_commit_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"branch":{"type":"string"},` +
				`"new_commit_id":{"type":"string"},"updated":{"type":"boolean"}},` +
				`"required":["branch","new_commit_id","updated"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, branchArgument,
				{Name: "new_commit_id", Required: true, Description: "Commit hash the branch should point to"},
				{Name: "old_commit_id", Description: "Commit hash the branch must currently point to; the " +
					"instance refuses the update otherwise"}},
			Fields: []capability.Field{{Name: "branch", Description: "Name of the branch"},
				{Name: "new_commit_id", Description: "Commit the branch was set to"},
				{Name: "updated", Description: "True once the instance updated the branch"}},
			Examples: []capability.Example{{Description: "Move a branch forward",
				Arguments: json.RawMessage(`{"repository":"example/project","branch":"main","new_commit_id":"0123456789abcdef","old_commit_id":"fedcba9876543210"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository  string `json:"repository"`
			Branch      string `json:"branch"`
			NewCommitID string `json:"new_commit_id"`
			OldCommitID string `json:"old_commit_id"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkRefName("branch", arguments.Branch, true); err != nil {
			return nil, err
		}
		if err := checkBranchID("new_commit_id", arguments.NewCommitID); err != nil {
			return nil, err
		}
		body := map[string]any{"new_commit_id": arguments.NewCommitID}
		if arguments.OldCommitID != "" {
			if err := checkBranchID("old_commit_id", arguments.OldCommitID); err != nil {
				return nil, err
			}
			body["old_commit_id"] = arguments.OldCommitID
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "update branch reference", http.MethodPut, branchPath(repo, arguments.Branch),
				body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"branch": arguments.Branch, "new_commit_id": arguments.NewCommitID,
				"updated": true}, nil
		}, nil
	},
}

var branchesDelete = &tool{
	group: "branches", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " branch",
			Description: "Delete one branch of a repository inside the connection's targets for good; the " +
				"instance may refuse the default branch or a protected branch. Only a connection whose tools " +
				"list names it offers this tool.",
			Tags:                  []string{p.id, "branches", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+branchSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":`+repoSchema+`,"branch":`+branchSchemaName, "branch"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"branch":{"type":"string"},` +
				`"deleted":{"type":"boolean"}},"required":["branch","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, branchArgument},
			Fields: []capability.Field{{Name: "branch", Description: "Name of the branch"},
				{Name: "deleted", Description: "True once the instance deleted the branch"}},
			Examples: []capability.Example{{Description: "Delete a branch",
				Arguments: json.RawMessage(`{"repository":"example/project","branch":"feature/x"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkRefName("branch", arguments.Branch, true); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete branch", http.MethodDelete, branchPath(repo, arguments.Branch), nil,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"branch": arguments.Branch, "deleted": true}, nil
		}, nil
	},
}
