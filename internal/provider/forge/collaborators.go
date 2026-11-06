package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// The collaborator tools read and change who has direct access to one repository inside the connection's
// targets. Only an existing user of the instance can be added; the API has no invitation by email address, and
// the tools send none. The user objects are personal data. The permission of a collaborator is a closed
// vocabulary on the way in (read, write, admin) and a text on the way out, since the products differ in what
// they report (Gitea also names none and owner).

const collaboratorSensitivity = "-personal-data"

var collaboratorArgument = capability.Argument{Name: "collaborator", Required: true,
	Description: "User name of the collaborator"}

var collaboratorPermissionArgument = capability.Argument{Name: "permission", Required: true,
	Description: "Permission to grant: read, write, or admin"}

const collaboratorPermissionSchema = `{"type":"string","enum":["read","write","admin"]}`

var collaboratorPermissions = map[string]bool{"read": true, "write": true, "admin": true}

var collaboratorProperties = `"repository":` + repoSchema + `,"collaborator":` + issueSchemaLogin

var collaboratorArguments = []capability.Argument{repositoryArgument, collaboratorArgument}

var collaboratorPermissionMembers = []member{
	{name: "permission", kind: kindText, note: "Permission level of the collaborator as the instance names it"},
	{name: "role_name", kind: kindText, note: "Name of the permission role"},
	{name: "user", kind: kindObject, sub: personMembers, note: "The collaborator; personal data"},
}

// collaboratorLogin validates the user name used as a path segment.
func collaboratorLogin(value string) error {
	if value == "" || !validName(value) {
		return invalidArgument("collaborator must be a user name")
	}
	return nil
}

func collaboratorsPath(repo target) string { return repositoryPath(repo) + "/collaborators" }

func collaboratorPath(repo target, login string) string {
	return collaboratorsPath(repo) + "/" + escape(login)
}

type collaboratorArgs struct {
	Repository   string `json:"repository"`
	Collaborator string `json:"collaborator"`
	Permission   string `json:"permission"`
}

// collaboratorTarget reads the arguments of a tool that names one collaborator and binds the repository.
func collaboratorTarget(allowed allowlist, raw json.RawMessage) (collaboratorArgs, target, error) {
	var a collaboratorArgs
	if err := decodeArguments(raw, &a); err != nil {
		return a, target{}, err
	}
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return a, target{}, err
	}
	if err := collaboratorLogin(a.Collaborator); err != nil {
		return a, target{}, err
	}
	return a, repo, nil
}

func collaboratorOutput(extra string, required ...string) json.RawMessage {
	required = append([]string{"repository", "collaborator"}, required...)
	quoted := make([]string, len(required))
	for i, name := range required {
		quoted[i] = `"` + name + `"`
	}
	return json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
		`"collaborator":{"type":"string"}` + extra + `},"required":[` + strings.Join(quoted, ",") +
		`],"additionalProperties":false}`)
}

var collaboratorsList = &tool{
	group: "collaborators", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the collaborators of a " + p.name + " repository",
			Description: "List one batch of the users who are collaborators of a repository inside the connection's " +
				"targets, with every member of each user. Names and texts are untrusted personal data.",
			Tags:        []string{p.id, "collaborators", "list"},
			Risk:        readRisk(p.id + collaboratorSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema + `,` + pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"collaborators":{"type":"array","items":` + schemaOf(p, personMembers, "id", "login") + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["repository","collaborators","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{repositoryArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
				{Name: "collaborators", Description: "Users with every member of the user object"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the collaborators",
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
		page, err := startPaging(provider.CursorBinding(p.id, "collaborators.list", strings.ToLower(fullName(repo))),
			a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list collaborators", collaboratorsPath(repo), page.query(), &rows)
			if err != nil {
				return nil, err
			}
			result := pageResult(page, header, len(rows), "collaborators", userRows(p, rows))
			result["repository"] = fullName(repo)
			return result, nil
		}, nil
	},
}

var collaboratorsCheck = &tool{
	group: "collaborators", action: "check", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a user is a collaborator of a " + p.name + " repository",
			Description: "Ask whether one user is a collaborator of a repository inside the connection's targets. " +
				"The answer is a boolean; a 404 is reported as not a collaborator, although the instance also " +
				"answers it for a user that does not exist or a repository the token does not reach.",
			Tags:         []string{p.id, "collaborators", "check"},
			Risk:         readRisk(p.id + collaboratorSensitivity),
			InputSchema:  inputSchema(collaboratorProperties, "collaborator"),
			OutputSchema: collaboratorOutput(`,"is_collaborator":{"type":"boolean"}`, "is_collaborator"),
			Arguments:    collaboratorArguments,
			Fields: []capability.Field{{Name: "repository", Description: "Repository that was checked as OWNER/REPO"},
				{Name: "collaborator", Description: "User name that was checked"},
				{Name: "is_collaborator", Description: "True when the instance answered 204; false when it answered 404"}},
			Examples: []capability.Example{{Description: "Check a user",
				Arguments: json.RawMessage(`{"repository":"example/project","collaborator":"alice"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, repo, err := collaboratorTarget(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			ok, err := c.probe(ctx, "check collaborator", collaboratorPath(repo, a.Collaborator))
			if err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "collaborator": a.Collaborator,
				"is_collaborator": ok}, nil
		}, nil
	},
}

var collaboratorsGetPermission = &tool{
	group: "collaborators", action: "get-permission", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the permission of a collaborator of a " + p.name + " repository",
			Description: "Read the permission one user has in a repository inside the connection's targets, with " +
				"every member of the answer and the user object; names and texts are untrusted personal data. The " +
				"instance answers 404 for a user that does not exist or a repository the token does not reach, and " +
				"needs a token that is an administrator of the repository or the user asked about.",
			Tags:        []string{p.id, "collaborators", "get"},
			Risk:        readRisk(p.id + collaboratorSensitivity),
			InputSchema: inputSchema(collaboratorProperties, "collaborator"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"collaborator":{"type":"string"}` + permissionProperties(p) + `},` +
				`"required":["repository","collaborator"],"additionalProperties":false}`),
			Arguments: collaboratorArguments,
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository as OWNER/REPO"},
				{Name: "collaborator", Description: "User name that was asked about"}},
				fieldsOf(p, collaboratorPermissionMembers)...),
			Examples: []capability.Example{{Description: "Read a permission",
				Arguments: json.RawMessage(`{"repository":"example/project","collaborator":"alice"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, repo, err := collaboratorTarget(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "get collaborator permission", collaboratorPath(repo, a.Collaborator)+"/permission",
				nil, &answer); err != nil {
				return nil, err
			}
			result := project(p, collaboratorPermissionMembers, answer)
			result["repository"], result["collaborator"] = fullName(repo), a.Collaborator
			return result, nil
		}, nil
	},
}

// permissionProperties are the schema members of the permission answer, ready to follow other members.
func permissionProperties(p *product) string {
	schema := schemaOf(p, collaboratorPermissionMembers)
	inner := strings.TrimSuffix(strings.TrimPrefix(schema, `{"type":"object","properties":{`),
		`},"additionalProperties":false}`)
	return "," + inner
}

func newCollaboratorChange(action, key, method, title, text string, permission bool) *tool {
	return &tool{
		group: "collaborators", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties, required := collaboratorProperties, []string{"collaborator"}
			arguments := collaboratorArguments
			extra, example := "", `{"repository":"example/project","collaborator":"alice"}`
			fields := []capability.Field{{Name: "repository", Description: "Repository the tool acted on as OWNER/REPO"},
				{Name: "collaborator", Description: "User name the tool acted on"}}
			if permission {
				properties += `,"permission":` + collaboratorPermissionSchema
				required = append(required, "permission")
				arguments = append(append([]capability.Argument(nil), arguments...), collaboratorPermissionArgument)
				extra = `,"permission":{"type":"string"}`
				example = `{"repository":"example/project","collaborator":"alice","permission":"write"}`
				fields = append(fields, capability.Field{Name: "permission", Description: "Permission that was sent"})
			}
			fields = append(fields, capability.Field{Name: key, Description: "True once the instance accepted the request"})
			required2 := []string{key}
			if permission {
				required2 = []string{"permission", key}
			}
			return capability.Descriptor{
				Title:        strings.ReplaceAll(title, "%s", p.name),
				Description:  text,
				Tags:         []string{p.id, "collaborators", action, "update"},
				Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+collaboratorSensitivity),
				InputSchema:  inputSchema(properties, required...),
				OutputSchema: collaboratorOutput(extra+`,"`+key+`":{"type":"boolean"}`, required2...),
				Arguments:    arguments,
				Fields:       fields,
				Examples: []capability.Example{{Description: strings.ReplaceAll(title, "%s", p.name),
					Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			a, repo, err := collaboratorTarget(allowed, raw)
			if err != nil {
				return nil, err
			}
			var body any
			if permission {
				if !collaboratorPermissions[a.Permission] {
					return nil, invalidArgument("permission must be read, write, or admin")
				}
				body = map[string]string{"permission": a.Permission}
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.change(ctx, action+" collaborator", method, collaboratorPath(repo, a.Collaborator), body,
					nil); err != nil {
					return nil, err
				}
				result := map[string]any{"repository": fullName(repo), "collaborator": a.Collaborator, key: true}
				if permission {
					result["permission"] = a.Permission
				}
				return result, nil
			}, nil
		},
	}
}

var collaboratorsAdd = newCollaboratorChange("add", "added", http.MethodPut,
	"Add a collaborator to a %s repository",
	"Give one existing user of the instance direct access to a repository inside the connection's targets at the "+
		"permission read, write, or admin; a collaborator that exists keeps the repository and gets the new "+
		"permission. A user that does not exist is not invited and not created; no email address is accepted. The "+
		"instance answers 422 for the owner of the repository. The token must be an administrator of the repository.",
	true)

var collaboratorsRemove = newCollaboratorChange("remove", "removed", http.MethodDelete,
	"Remove a collaborator from a %s repository",
	"Remove one user's direct access to a repository inside the connection's targets. The user and the repository "+
		"stay; access the user has through a team or as a public repository reader stays as well. The token must be an "+
		"administrator of the repository.",
	false)
