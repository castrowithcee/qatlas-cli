package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The star and watch tools read and change which repositories the account behind the token stars or watches, and
// read who stars or watches a repository. A change names one repository inside the connection's targets and
// acts on the account behind the token only. The lists of repositories are filtered row by row against the
// targets; a list of another user needs that user inside the targets. The people of a repository are personal
// data.

const starsPeopleSensitivity = "-personal-data"

// repositoryListSpec describes a list of repositories that belong to an account: its own at one path, another
// user's below /users/NAME.
type repositoryListSpec struct {
	group, noun, verb, own, other string
}

// repositoryPageResult is the answer of a repository list.
func repositoryPageResult(p *product, page paging, header http.Header, allowed allowlist, rows []object, user string) map[string]any {
	repositories := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		// A row outside the targets is dropped; the count that decides has_more is the unfiltered one.
		if fullName, _ := row.text("full_name"); allowed.allowsFullName(fullName) {
			repositories = append(repositories, project(p, repositoryMembers, row))
		}
	}
	result := pageResult(page, header, len(rows), "repositories", repositories)
	if user != "" {
		result["username"] = user
	}
	return result
}

func newRepositoryList(s repositoryListSpec) *tool {
	return &tool{
		group: s.group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "List the " + s.noun + " of a " + p.name + " account",
				Description: "List one batch of the repositories the account behind the token " + s.verb + ", or of " +
					"another user when username is given, with every member of each repository. Rows outside the " +
					"connection's targets are left out; a username must lie inside the targets as an owner or through " +
					"a repository of it.",
				Tags:        []string{p.id, s.group, "list"},
				Risk:        readRisk(p.id + repositorySensitivity),
				InputSchema: inputSchema(`"username":` + issueSchemaLogin + `,` + pagingKeys),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"repositories":{"type":"array","items":` +
					schemaOf(p, repositoryMembers, "id", "name", "full_name") + `},"username":{"type":"string"},` +
					`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
					`"required":["repositories","has_more"],"additionalProperties":false}`),
				Arguments: append([]capability.Argument{{Name: "username", Description: "User whose " + s.noun +
					" to list; the account behind the token when omitted"}}, pagingArguments...),
				Fields: append([]capability.Field{
					{Name: "repositories", Description: "Repositories with every member of the repository object"},
					{Name: "username", Description: "User the tool listed, left out for the account behind the token"},
				}, pagingFields...),
				Examples: []capability.Example{{Description: "List the " + s.noun, Arguments: json.RawMessage(`{"limit":20}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Username string `json:"username"`
				Limit    int    `json:"limit"`
				Cursor   string `json:"cursor"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			path := s.own
			if a.Username != "" {
				if err := userNameOf("username", a.Username); err != nil {
					return nil, err
				}
				if len(a.Username) > maxNameLength {
					return nil, invalidArgument("username must be a user name")
				}
				if !allowed.listsOwner(a.Username) {
					return nil, outsideTargets("username")
				}
				path = "/users/" + escape(a.Username) + s.other
			}
			page, err := startPaging(provider.CursorBinding(p.id, s.group+".list", strings.ToLower(a.Username)), a.Limit,
				a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var rows []object
				header, err := c.get(ctx, "list "+s.group, path, page.query(), &rows)
				if err != nil {
					return nil, err
				}
				return repositoryPageResult(p, page, header, allowed, rows, a.Username), nil
			}, nil
		},
	}
}

var starsList = newRepositoryList(repositoryListSpec{group: "stars", noun: "starred repositories", verb: "stars",
	own: "/user/starred", other: "/starred"})

var subscriptionsList = newRepositoryList(repositoryListSpec{group: "subscriptions", noun: "watched repositories", verb: "watches",
	own: "/user/subscriptions", other: "/subscriptions"})

// ---- the stars of one repository -----------------------------------------------------------------------------

type repositoryStarCall struct {
	Repository string `json:"repository"`
}

func starPath(repo target) string {
	return "/user/starred/" + escape(repo.owner) + "/" + escape(repo.repo)
}

var starsCheck = &tool{
	group: "stars", action: "check", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether the account stars a " + p.name + " repository",
			Description: "Ask whether the account behind the token stars a repository inside the connection's targets. " +
				"The answer is a boolean; a 404 is reported as not starred, although the instance also answers it " +
				"for a repository the token does not reach.",
			Tags:        []string{p.id, "stars", "check"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"starred":{"type":"boolean"}},"required":["repository","starred"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository the tool asked, as OWNER/REPO"},
				{Name: "starred", Description: "True when the account stars the repository"}},
			Examples: []capability.Example{{Description: "Check a star",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a repositoryStarCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			starred, err := c.probe(ctx, "check star", starPath(repo))
			if err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "starred": starred}, nil
		}, nil
	},
}

func starChange(action, method, key, title, text string) *tool {
	return &tool{
		group: "stars", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:       title + " " + p.name + " repository",
				Description: text + " Only the account behind the token changes. The change is the one request.",
				Tags:        []string{p.id, "stars", action, "update"},
				Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
				InputSchema: inputSchema(`"repository":` + repoSchema),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},"` + key +
					`":{"type":"boolean"}},"required":["repository","` + key + `"],"additionalProperties":false}`),
				Arguments: []capability.Argument{repositoryArgument},
				Fields: []capability.Field{{Name: "repository", Description: "Repository the tool changed, as OWNER/REPO"},
					{Name: key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: title,
					Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
			}
		},
		prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a repositoryStarCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(a.Repository)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.change(ctx, action+" star", method, starPath(repo), nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"repository": fullName(repo), key: true}, nil
			}, nil
		},
	}
}

var (
	starsAdd = starChange("add", http.MethodPut, "starred", "Star a",
		"Star a repository inside the connection's targets.")
	starsRemove = starChange("remove", http.MethodDelete, "unstarred", "Remove the star from a",
		"Remove the star of the account from a repository inside the connection's targets.")
)

// ---- the people of one repository ----------------------------------------------------------------------------

func newRepositoryPeopleList(group, path, title, text string) *tool {
	return &tool{
		group: group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:       title + " " + p.name + " repository",
				Description: text + " Names and texts are untrusted personal data.",
				Tags:        []string{p.id, group, "list"},
				Risk:        readRisk(p.id + starsPeopleSensitivity),
				InputSchema: inputSchema(`"repository":` + repoSchema + `,` + pagingKeys),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
					`"users":{"type":"array","items":` + schemaOf(p, personMembers, "id", "login") + `},` +
					`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
					`"required":["repository","users","has_more"],"additionalProperties":false}`),
				Arguments: append([]capability.Argument{repositoryArgument}, pagingArguments...),
				Fields: append([]capability.Field{{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
					{Name: "users", Description: "Users with every member of the user object"}}, pagingFields...),
				Examples: []capability.Example{{Description: title,
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
			page, err := startPaging(provider.CursorBinding(p.id, group+".list", strings.ToLower(fullName(repo))), a.Limit,
				a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var rows []object
				header, err := c.get(ctx, "list "+group, repositoryPath(repo)+path, page.query(), &rows)
				if err != nil {
					return nil, err
				}
				result := pageResult(page, header, len(rows), "users", userRows(p, rows))
				result["repository"] = fullName(repo)
				return result, nil
			}, nil
		},
	}
}

var repositoryStargazersList = newRepositoryPeopleList("repository-stargazers", "/stargazers", "List the stargazers of a",
	"List one batch of the users who star a repository inside the connection's targets, with every member of each user.")

var repositorySubscribersList = newRepositoryPeopleList("repository-subscribers", "/subscribers", "List the watchers of a",
	"List one batch of the users who watch a repository inside the connection's targets, with every member of each user.")
