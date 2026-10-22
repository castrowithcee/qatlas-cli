package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The assignee tools read who can be assigned in a repository inside the connection's targets and, on Gitea,
// change the assignees of one issue. The user objects are personal data. The index of a change or check is
// proven by the evidence read of the issue; the login is a validated path segment or body value.

var giteaOnly = map[string]version{gitea.id: gitea.minimum}

const assigneeSchemaList = `{"type":"array","minItems":1,"maxItems":10,"items":` + issueSchemaLogin + `}`

var assigneeArgument = capability.Argument{Name: "assignee", Required: true,
	Description: "User name of the person to check"}

var assigneesArgument = capability.Argument{Name: "assignees", Required: true,
	Description: "User names, one to ten, without duplicates"}

var assigneeCheckMembers = []member{
	{name: "assignee", kind: kindText, note: "User name that was checked"},
	{name: "assignable", kind: kindFlag, note: "True when the instance answered that the user can be assigned; " +
		"false when it answered 404, which it also gives for a repository the token does not reach"},
}

var issueAssigneeCheckMembers = []member{
	{name: "number", kind: kindInteger, note: "Index of the issue"},
	{name: "assignee", kind: kindText, note: "User name that was checked"},
	{name: "assigned", kind: kindFlag, note: "True when the user is an assignee of the issue; false when the " +
		"instance answered 404"},
}

var assigneesList = &tool{
	group: "assignees", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the assignable users of a " + p.name + " repository",
			Description: "List the users who can be assigned to the issues of a repository inside the connection's " +
				"targets, with every member of each user; names and texts are untrusted personal data.",
			Tags:        []string{p.id, "assignees", "list"},
			Risk:        readRisk(p.id + issueSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"assignees":{"type":"array","items":` +
				schemaOf(p, personMembers, "id", "login") + `},"truncated":{"type":"boolean"}},` +
				`"required":["assignees"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "assignees", Description: "Users with every member of the user object"},
				{Name: "truncated", Description: "True when the instance sent more entries than the tool returns"}},
			Examples: []capability.Example{{Description: "List the assignable users",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "list assignable users", repoAssigneesPath(repo), nil, &rows); err != nil {
				return nil, err
			}
			result := map[string]any{}
			if len(rows) > maxListItems {
				rows, result["truncated"] = rows[:maxListItems], true
			}
			users := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					users = append(users, project(p, personMembers, row))
				}
			}
			result["assignees"] = users
			return result, nil
		}, nil
	},
}

func repoAssigneesPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/assignees"
}

// assigneeLogin validates one user name used as a path segment or body value.
func assigneeLogin(value string) error {
	if value == "" || !validName(value) || len(value) > 40 {
		return invalidArgument("assignee must be a user name")
	}
	return nil
}

var assigneesCheck = &tool{
	group: "assignees", action: "check", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a user can be assigned in a " + p.name + " repository",
			Description: "Ask whether one user can be assigned to the issues of a repository inside the " +
				"connection's targets. The answer is a boolean; a 404 is reported as not assignable, although the " +
				"instance also answers it when the token does not reach the repository.",
			Tags:         []string{p.id, "assignees", "check"},
			Risk:         readRisk(p.id + issueSensitivity),
			InputSchema:  inputSchema(`"repository":`+repoSchema+`,"assignee":`+issueSchemaLogin, "assignee"),
			OutputSchema: json.RawMessage(schemaOf(p, assigneeCheckMembers, "assignee", "assignable")),
			Arguments:    []capability.Argument{repositoryArgument, assigneeArgument},
			Fields:       fieldsOf(p, assigneeCheckMembers),
			Examples: []capability.Example{{Description: "Check a user",
				Arguments: json.RawMessage(`{"repository":"example/project","assignee":"alice"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Assignee   string `json:"assignee"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := assigneeLogin(arguments.Assignee); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			ok, err := c.probe(ctx, "check assignable user", repoAssigneesPath(repo)+"/"+escape(arguments.Assignee))
			if err != nil {
				return nil, err
			}
			return map[string]any{"assignee": arguments.Assignee, "assignable": ok}, nil
		}, nil
	},
}

var issueAssigneesCheck = &tool{
	group: "issue-assignees", action: "check", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a user is assigned to a " + p.name + " issue",
			Description: "Ask whether one user is an assignee of an issue of a repository inside the connection's " +
				"targets. The answer is a boolean; a 404 is reported as not assigned. The index of a pull " +
				"request is refused.",
			Tags: []string{p.id, "issues", "assignees", "check"},
			Risk: readRisk(p.id + issueSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"number":`+issueSchemaNumber+`,"assignee":`+
				issueSchemaLogin, "number", "assignee"),
			OutputSchema: json.RawMessage(schemaOf(p, issueAssigneeCheckMembers, "number", "assignee", "assigned")),
			Arguments:    []capability.Argument{repositoryArgument, numberArgument, assigneeArgument},
			Fields:       fieldsOf(p, issueAssigneeCheckMembers),
			Examples: []capability.Example{{Description: "Check an assignee",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"assignee":"alice"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Number     int64  `json:"number"`
			Assignee   string `json:"assignee"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		if err := assigneeLogin(arguments.Assignee); err != nil {
			return nil, err
		}
		return onIssue(allowed, arguments.Repository, arguments.Number, "check issue assignee",
			func(ctx context.Context, c *Client, _ target, path string, _ object) (any, error) {
				ok, err := c.probe(ctx, "check issue assignee", path+"/assignees/"+escape(arguments.Assignee))
				if err != nil {
					return nil, err
				}
				return map[string]any{"number": arguments.Number, "assignee": arguments.Assignee, "assigned": ok}, nil
			})
	},
}

// assigneesChange is issue-assignees.add and issue-assignees.remove: one request with the validated user names.
func assigneesChange(action, method, verb, preposition string) *tool {
	return &tool{
		group: "issue-assignees", action: action, since: giteaOnly,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: verb + " assignees " + preposition + " a " + p.name + " issue",
				Description: verb + " one to ten assignees " + preposition + " one issue of a repository inside the " +
					"connection's targets and return the issue. The index of a pull request is refused.",
				Tags: []string{p.id, "issues", "assignees", action, "update"},
				Risk: changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				InputSchema: inputSchema(`"repository":`+repoSchema+`,"number":`+issueSchemaNumber+`,"assignees":`+
					assigneeSchemaList, "number", "assignees"),
				OutputSchema: json.RawMessage(schemaOf(p, issueMembers, "id", "number")),
				Arguments:    []capability.Argument{repositoryArgument, numberArgument, assigneesArgument},
				Fields:       fieldsOf(p, issueMembers),
				Examples: []capability.Example{{Description: verb + " an assignee",
					Arguments: json.RawMessage(`{"repository":"example/project","number":7,"assignees":["alice"]}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				Repository string   `json:"repository"`
				Number     int64    `json:"number"`
				Assignees  []string `json:"assignees"`
			}
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
				return nil, err
			}
			if len(arguments.Assignees) < 1 || len(arguments.Assignees) > maxAssignees {
				return nil, invalidArgument("assignees takes one to ten user names")
			}
			seen := map[string]bool{}
			for _, login := range arguments.Assignees {
				if err := assigneeLogin(login); err != nil {
					return nil, invalidArgument("assignees takes user names")
				}
				if seen[strings.ToLower(login)] {
					return nil, invalidArgument("assignees must not repeat a user name")
				}
				seen[strings.ToLower(login)] = true
			}
			body := map[string]any{"assignees": arguments.Assignees}
			return onIssue(allowed, arguments.Repository, arguments.Number, action+" issue assignees",
				func(ctx context.Context, c *Client, _ target, path string, _ object) (any, error) {
					var updated object
					if err := c.change(ctx, action+" issue assignees", method, path+"/assignees", body, &updated); err != nil {
						return nil, err
					}
					return project(p, issueMembers, updated), nil
				})
		},
	}
}

var (
	issueAssigneesAdd    = assigneesChange("add", http.MethodPost, "Add", "to")
	issueAssigneesRemove = assigneesChange("remove", http.MethodDelete, "Remove", "from")
)
