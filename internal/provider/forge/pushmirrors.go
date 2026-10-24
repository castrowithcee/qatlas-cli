package forge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The push mirror tools read, sync, and delete the existing push mirrors of one repository. No push mirror is
// created. The target address is untrusted text and never fetched; the remote credentials and the public key
// text are never shown. The instance serves these routes only to a repository administrator and only while
// mirroring is enabled.

const (
	maxMirrorError = 1024

	mirrorNameSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9_.-]+$"}`
)

var pushMirrorMembers = []member{
	{name: "remote_name", kind: kindText, note: "Name of the push mirror, untrusted data"},
	{name: "remote_address", kind: kindText, address: true, note: "Target address, untrusted data, never fetched; " +
		"left out when it carries user info or a query"},
	{name: "repo_name", kind: kindText, note: "Name of the repository"},
	{name: "interval", kind: kindText, note: "Sync interval such as 8h0m0s"},
	{name: "sync_on_commit", kind: kindFlag, note: "True when every commit is pushed at once"},
	{name: "branch_filter", kind: kindText, only: forgejo, note: "Branches that are pushed, untrusted data"},
	{name: "created", kind: kindText, note: "Creation time"},
	{name: "last_update", kind: kindText, note: "Time of the last sync"},
	{name: "last_error", kind: kindText, max: maxMirrorError, note: "Error of the last sync, untrusted data"},
}

var pushMirrorArgument = capability.Argument{Name: "name", Required: true,
	Description: "Name of the push mirror, as push-mirrors.list reports it as remote_name"}

func mirrorName(value string) error {
	if !validRepoName(value) {
		return invalidArgument("name must be the name of a push mirror")
	}
	return nil
}

var pushMirrorsList = &tool{
	group: "push-mirrors", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the push mirrors of a " + p.name + " repository",
			Description: "List one batch of the existing push mirrors of a repository inside the connection's " +
				"targets. The target address is untrusted data and never fetched. The remote credentials and the " +
				"public key text are never shown. No push mirror is created. The instance serves this only to a " +
				"repository administrator and only while mirroring is enabled.",
			Tags:        []string{p.id, "push-mirrors", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema + `,` + pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"push_mirrors":{"type":"array","items":` + schemaOf(p, pushMirrorMembers) + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["repository","push_mirrors","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{repositoryArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "push_mirrors", Description: "Push mirrors with every member of the push mirror object"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "List the push mirrors",
				Arguments: json.RawMessage(`{"repository":"example/project","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "push-mirrors.list", fullName(repo)), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list push mirrors", repositoryPath(repo)+"/push_mirrors", page.query(), &rows)
			if err != nil {
				return nil, err
			}
			mirrors := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					mirrors = append(mirrors, project(p, pushMirrorMembers, row))
				}
			}
			result := pageResult(page, header, len(rows), "push_mirrors", mirrors)
			result["repository"] = fullName(repo)
			return result, nil
		}, nil
	},
}

var pushMirrorsGet = &tool{
	group: "push-mirrors", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a push mirror of a " + p.name + " repository",
			Description: "Read one existing push mirror of a repository inside the connection's targets by its " +
				"name. The target address is untrusted data and never fetched; credentials and the public key " +
				"text are never shown.",
			Tags:        []string{p.id, "push-mirrors", "get"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"name":`+mirrorNameSchema, "name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"push_mirror":` + schemaOf(p, pushMirrorMembers) + `},"required":["repository","push_mirror"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, pushMirrorArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "push_mirror", Description: "The push mirror with every member of the push mirror object"}},
			Examples: []capability.Example{{Description: "Read a push mirror",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"remote_mirror_1a2b"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Name       string `json:"name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := mirrorName(a.Name); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get push mirror"
			var row object
			if _, err := c.get(ctx, op, repositoryPath(repo)+"/push_mirrors/"+escape(a.Name), nil, &row); err != nil {
				return nil, err
			}
			if got, ok := row.text("remote_name"); !ok || got != a.Name {
				return nil, provider.InvalidResponse(op, p.name+" returned another push mirror than the one asked for")
			}
			return map[string]any{"repository": fullName(repo), "push_mirror": project(p, pushMirrorMembers, row)}, nil
		}, nil
	},
}

// pushMirrorsSync asks the instance to push to the existing push mirrors now.
var pushMirrorsSync = newRepositoryAction(repositoryAction{
	group: "push-mirrors", action: "sync", since: everywhere,
	title: "Sync the push mirrors of a %s repository",
	description: "Ask the instance to push a repository inside the connection's targets to all of its existing push " +
		"mirrors now. The instance syncs asynchronously and may name mirrors that failed. No push mirror is created " +
		"and no mirror setting changes. The instance serves this only to a repository administrator and only " +
		"while mirroring is enabled.",
	effect: capability.EffectExecute, idempotency: capability.IdempotencyNonIdempotent,
	suffix: "/push_mirrors-sync", method: http.MethodPost, op: "sync push mirrors", key: "requested",
	tags: []string{"push-mirrors", "sync", "execute"},
})

var pushMirrorsDelete = &tool{
	group: "push-mirrors", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a push mirror of a " + p.name + " repository",
			Description: "Delete one existing push mirror of a repository inside the connection's targets by its " +
				"name for good; this cannot be undone and the target receives no further pushes. Only a " +
				"connection whose tools list names it offers this tool. The change is the one request.",
			Tags:                  []string{p.id, "push-mirrors", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":`+repoSchema+`,"name":`+mirrorNameSchema, "name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["repository","name","deleted"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, pushMirrorArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "name", Description: "Name of the push mirror"},
				{Name: "deleted", Description: "True once the instance deleted the push mirror"}},
			Examples: []capability.Example{{Description: "Delete a push mirror",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"remote_mirror_1a2b"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Name       string `json:"name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := mirrorName(a.Name); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete push mirror", http.MethodDelete,
				repositoryPath(repo)+"/push_mirrors/"+escape(a.Name), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "name": a.Name, "deleted": true}, nil
		}, nil
	},
}
