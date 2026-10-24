package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The Git hook tools read and reset the server-side Git hooks of one repository. A script is untrusted text:
// it is cut at maxHookScript bytes and never run. The instance serves these routes only to a repository
// administrator who is also a site administrator or allowed to edit Git hooks, and only while Git hooks are not
// disabled. Changing a script is not offered.

const (
	maxHookScript = 8192

	hookNameSchema = `{"type":"string","minLength":1,"maxLength":32,"pattern":"^[a-z][a-z-]*$"}`
)

var hookNamePattern = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)

var gitHookArgument = capability.Argument{Name: "name", Required: true,
	Description: "Name of the Git hook, as git-hooks.list reports it as name, such as pre-receive, update, or post-receive"}

// gitHookRow shows a hook with its script cut at maxHookScript bytes.
func gitHookRow(row object) map[string]any {
	out := map[string]any{}
	if name, ok := row.text("name"); ok {
		out["name"] = name
	}
	if active, ok := row.flag("is_active"); ok {
		out["is_active"] = active
	}
	if content, ok := row.textUpTo("content", maxHookScript+1); ok {
		out["content_truncated"] = len(content) > maxHookScript
		out["content"] = truncateText(content, maxHookScript)
	}
	return out
}

const gitHookRowSchema = `{"type":"object","properties":{"name":{"type":"string"},"is_active":{"type":"boolean"},` +
	`"content":{"type":"string"},"content_truncated":{"type":"boolean"}},"additionalProperties":false}`

var gitHookFields = []capability.Field{
	{Name: "name", Description: "Name of the Git hook, untrusted data"},
	{Name: "is_active", Description: "True when the hook has a script"},
	{Name: "content", Description: "Script, untrusted data, cut at 8192 bytes and never run"},
	{Name: "content_truncated", Description: "True when the script is longer than shown"},
}

var gitHooksList = &tool{
	group: "git-hooks", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the Git hooks of a " + p.name + " repository",
			Description: "List the server-side Git hooks of one repository inside the connection's targets. The " +
				"scripts are untrusted text, cut at 8192 bytes, and never run. The instance serves this only to a " +
				"repository administrator who is a site administrator or allowed to edit Git hooks, and only while " +
				"Git hooks are enabled; otherwise it answers as if the repository were not reachable.",
			Tags:        []string{p.id, "git-hooks", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"git_hooks":{"type":"array","items":` + gitHookRowSchema + `}},"required":["repository","git_hooks"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "git_hooks", Description: "Git hooks with name, is_active, content, and content_truncated"}},
				gitHookFields...),
			Examples: []capability.Example{{Description: "List the Git hooks",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "list git hooks", repositoryPath(repo)+"/hooks/git", nil, &rows); err != nil {
				return nil, err
			}
			if len(rows) > maxListItems {
				rows = rows[:maxListItems]
			}
			hooks := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					hooks = append(hooks, gitHookRow(row))
				}
			}
			return map[string]any{"repository": fullName(repo), "git_hooks": hooks}, nil
		}, nil
	},
}

func gitHookName(value string) error {
	if !hookNamePattern.MatchString(value) {
		return invalidArgument("name must be the name of a Git hook such as pre-receive")
	}
	return nil
}

var gitHooksGet = &tool{
	group: "git-hooks", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a Git hook of a " + p.name + " repository",
			Description: "Read one server-side Git hook of a repository inside the connection's targets by its " +
				"name. The script is untrusted text, cut at 8192 bytes, and never run. The instance serves this " +
				"only to a repository administrator who is a site administrator or allowed to edit Git hooks, and " +
				"only while Git hooks are enabled.",
			Tags:        []string{p.id, "git-hooks", "get"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"name":`+hookNameSchema, "name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"git_hook":` + gitHookRowSchema + `},"required":["repository","git_hook"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, gitHookArgument},
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "git_hook", Description: "The Git hook with name, is_active, content, and content_truncated"}},
				gitHookFields...),
			Examples: []capability.Example{{Description: "Read a Git hook",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"pre-receive"}`)}},
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
		if err := gitHookName(a.Name); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get git hook"
			var row object
			if _, err := c.get(ctx, op, repositoryPath(repo)+"/hooks/git/"+escape(a.Name), nil, &row); err != nil {
				return nil, err
			}
			if got, ok := row.text("name"); !ok || got != a.Name {
				return nil, provider.InvalidResponse(op, p.name+" returned another Git hook than the one asked for")
			}
			return map[string]any{"repository": fullName(repo), "git_hook": gitHookRow(row)}, nil
		}, nil
	},
}

var gitHooksDelete = &tool{
	group: "git-hooks", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Reset a Git hook of a " + p.name + " repository",
			Description: "Delete the script of one server-side Git hook of a repository inside the connection's " +
				"targets by its name; the hook is empty and inactive afterwards, and the script cannot be " +
				"restored. Changing a script is not offered. Only a connection whose tools list names it offers " +
				"this tool. The change is the one request.",
			Tags:                  []string{p.id, "git-hooks", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":`+repoSchema+`,"name":`+hookNameSchema, "name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["repository","name","deleted"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, gitHookArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "name", Description: "Name of the Git hook"},
				{Name: "deleted", Description: "True once the instance reset the hook"}},
			Examples: []capability.Example{{Description: "Reset a Git hook",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"pre-receive"}`)}},
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
		if err := gitHookName(a.Name); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "reset git hook", http.MethodDelete,
				repositoryPath(repo)+"/hooks/git/"+escape(a.Name), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "name": a.Name, "deleted": true}, nil
		}, nil
	},
}
