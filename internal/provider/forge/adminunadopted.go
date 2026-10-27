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

// The unadopted-repository tools list the repository directories on the disk of the instance that no repository
// record owns, adopt one as a repository, or delete its files for good. Like the other instance administration
// they reach past any target, so only a connection without targets offers them. They belong to the repository
// data class: the entries name repositories of other owners and delete removes their files.

const unadoptedFilterMax = 255

var unadoptedRowSchema = `{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},` +
	`"full_name":{"type":"string"}},"required":["owner","repo","full_name"],"additionalProperties":false}`

var unadoptedRepoArguments = []capability.Argument{
	{Name: "owner", Required: true, Description: "Owner of the directory, as admin-unadopted.list reports it as owner"},
	{Name: "repo", Required: true, Description: "Name of the directory, as admin-unadopted.list reports it as repo"}}

const unadoptedRepoProperties = `"owner":` + issueSchemaLogin + `,"repo":` + repoNameSchema

// unadoptedFilterName is the name of the filter in the query of the list: Gitea 28 renamed it.
func unadoptedFilterName(p *product, current version) string {
	if p == gitea && !current.less(giteaActions28) {
		return "query"
	}
	return "pattern"
}

// unadoptedPattern reads the narrow glob: owner or owner/repo with at most one slash and plain text.
func unadoptedPattern(value string) error {
	if len(value) > unadoptedFilterMax || value == "" || !plainText(value) || strings.Count(value, "/") > 1 ||
		strings.Contains(value, "\\") {
		return invalidArgument("pattern must be 1 to 255 characters of plain text with at most one /")
	}
	return nil
}

// unadoptedName reads the owner and the repository of the change tools.
func unadoptedName(raw json.RawMessage) (owner, repo string, err error) {
	var a struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	}
	if err := decodeArguments(raw, &a); err != nil {
		return "", "", err
	}
	if !validName(a.Owner) {
		return "", "", invalidArgument("owner must be a user or organization name")
	}
	if !validRepoName(a.Repo) {
		return "", "", invalidArgument("repo must be a repository name")
	}
	return a.Owner, a.Repo, nil
}

func unadoptedPath(owner, repo string) string {
	return "/admin/unadopted/" + escape(owner) + "/" + escape(repo)
}

var adminUnadoptedList = &tool{
	group: "admin-unadopted", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List " + p.name + " repositories on disk that no record owns",
			Description: "List one batch of the repository directories on the disk of the instance that are not " +
				"registered as repositories, as an administrator; the entries are untrusted data, and an entry " +
				"that is no owner/repo pair is left out. " + adminUserNote,
			Tags: []string{p.id, "admin", "unadopted", "list"},
			Risk: readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"pattern":{"type":"string","minLength":1,"maxLength":` + itoa(unadoptedFilterMax) +
				`},` + pagingKeys),
			OutputSchema: instanceListOutput("unadopted", unadoptedRowSchema),
			Arguments: append([]capability.Argument{{Name: "pattern", Description: "Glob pattern in the form owner or " +
				"owner/repo; the tool sends it under the name the instance version knows"}}, pagingArguments...),
			Fields: instanceListFields("unadopted", "Directories with owner, repo, and full_name"),
			Examples: []capability.Example{{Description: "List the directories of one owner",
				Arguments: json.RawMessage(`{"pattern":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Pattern string `json:"pattern"`
			Limit   int    `json:"limit"`
			Cursor  string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if a.Pattern != "" {
			if err := unadoptedPattern(a.Pattern); err != nil {
				return nil, err
			}
		}
		const path = "/admin/unadopted"
		bound := url.Values{}
		if a.Pattern != "" {
			bound.Set("pattern", a.Pattern)
		}
		page, err := startPaging(provider.CursorBinding(p.id, "admin-unadopted.list", "", path, bound.Encode()),
			a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list unadopted repositories"
			query := url.Values{}
			if a.Pattern != "" {
				current, err := c.knownVersion(ctx)
				if err != nil {
					return nil, err
				}
				query.Set(unadoptedFilterName(p, current), a.Pattern)
			}
			for name, values := range page.query() {
				query[name] = values
			}
			var names []string
			header, err := c.get(ctx, op, path, query, &names)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(names))
			for _, full := range names {
				owner, repo, ok := strings.Cut(full, "/")
				if ok && validName(owner) && validRepoName(repo) {
					rows = append(rows, map[string]any{"owner": owner, "repo": repo, "full_name": full})
				}
			}
			return instanceResult("unadopted", rows, totalCount(header), page.next(header, len(names))), nil
		}, nil
	},
}

var adminUnadoptedAdopt = &tool{
	group: "admin-unadopted", action: "adopt", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Adopt a " + p.name + " repository directory",
			Description: "Adopt the repository files of one directory on the disk of the instance as a repository, " +
				"as an administrator. Not idempotent; the request is sent once and never repeated after an unclear " +
				"outcome. " + adminUserNote,
			Tags:        []string{p.id, "admin", "unadopted", "adopt"},
			Risk:        changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+repositorySensitivity),
			InputSchema: inputSchema(unadoptedRepoProperties, "owner", "repo"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"full_name":{"type":"string"},` +
				`"adopted":{"type":"boolean"}},"required":["full_name","adopted"],"additionalProperties":false}`),
			Arguments: unadoptedRepoArguments,
			Fields: []capability.Field{{Name: "full_name", Description: "owner/repo of the directory"},
				{Name: "adopted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Adopt a directory",
				Arguments: json.RawMessage(`{"owner":"octo","repo":"project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		owner, repo, err := unadoptedName(raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "adopt unadopted repository", http.MethodPost, unadoptedPath(owner, repo), nil,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"full_name": owner + "/" + repo, "adopted": true}, nil
		}, nil
	},
}

var adminUnadoptedDelete = &tool{
	group: "admin-unadopted", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete the files of a " + p.name + " repository directory",
			Description: "Delete the files of one repository directory on the disk of the instance that no repository " +
				"owns, for good and without a way back, as an administrator. The request is sent once and never " +
				"repeated after an unclear outcome. Only a connection without targets whose tools list names this " +
				"tool offers it.",
			Tags:                  []string{p.id, "admin", "unadopted", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(unadoptedRepoProperties, "owner", "repo"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"full_name":{"type":"string"},` +
				`"deleted":{"type":"boolean"}},"required":["full_name","deleted"],"additionalProperties":false}`),
			Arguments: unadoptedRepoArguments,
			Fields: []capability.Field{{Name: "full_name", Description: "owner/repo of the directory"},
				{Name: "deleted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Delete a directory",
				Arguments: json.RawMessage(`{"owner":"octo","repo":"stale"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		owner, repo, err := unadoptedName(raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete unadopted repository files", http.MethodDelete, unadoptedPath(owner, repo), nil,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"full_name": owner + "/" + repo, "deleted": true}, nil
		}, nil
	},
}
