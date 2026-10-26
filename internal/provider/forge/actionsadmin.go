package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The Actions administration tools manage the secrets and variables of a repository (repository), an
// organization (owner as orgs/NAME), or the user behind the token (owner as users/NAME) on both products. Each
// level has its fixed path below .../actions; the owner level needs the owner as an owner target of the same
// kind, and the user level, served by /user/actions for the account behind the token, is proven by reading that
// account first. Secrets and variables carry no repository of their own, so the path is what binds them.
//
// A secret value is never an argument and never read back: the API answers names only. The value is the one
// field of a forward credential the connection releases, read through the resolver after the confirmation,
// registered with the redactor, and sent once in the body of the one request; the answer to that request is
// discarded unread, so an instance that echoes the value cannot hand it on.

const (
	secretsSensitivity   = "-actions-secrets"
	variablesSensitivity = "-actions-variables"
	// maxActionsValue bounds a secret or variable value in bytes, the limit both products keep themselves.
	maxActionsValue       = 65536
	maxActionsDescription = 4096

	actionsNameSchema      = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z_][A-Za-z0-9_]*$"}`
	actionsValueSchema     = `{"type":"string","maxLength":65536}`
	actionsDescSchema      = `{"type":"string","maxLength":4096}`
	actionsSecretRefSchema = `{"type":"string","minLength":1,"maxLength":200}`
	adminLevelProperties   = `"repository":` + repoSchema + `,"owner":` + ownerSchema
)

// actionsName is the form both products accept for a secret or variable name; the prefixes GITHUB_ and GITEA_
// are reserved for the values the runner sets itself.
var (
	actionsName           = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,254}$`)
	reservedActionsPrefix = regexp.MustCompile(`(?i)^GIT(EA|HUB)_`)
)

var (
	adminRepositoryArgument = capability.Argument{Name: "repository", Target: kindRepository,
		Description: "Repository as OWNER/REPO; pass either repository or owner; optional when owner is left out " +
			"and the connection's targets name exactly one repository; must lie inside the targets when the " +
			"connection lists any"}
	adminOwnerArgument = capability.Argument{Name: "owner", Target: kindOwner,
		Description: "Organization as orgs/NAME, or users/NAME for the account behind the token, instead of a " +
			"repository; a connection with targets must name that owner as an owner target of the same kind, and " +
			"a repository target does not allow it"}
	adminDescriptionArgument = capability.Argument{Name: "description", Description: "Gitea only: description, " +
		"at most 4096 characters; give the current one to keep it, since Gitea stores the description it receives"}
)

var actionsSecretMembers = []member{
	{name: "name", kind: kindText, note: "Name of the secret, as the instance stores it"},
	{name: "description", kind: kindText, only: gitea, max: maxActionsDescription, note: "Description, untrusted data"},
	{name: "created_at", kind: kindText, note: "Creation time"},
}

var actionsVariableMembers = []member{
	{name: "name", kind: kindText, note: "Name of the variable, as the instance stores it"},
	{name: "data", kind: kindText, max: maxActionsValue, note: "Value of the variable, untrusted data; not a secret"},
	{name: "description", kind: kindText, only: gitea, max: maxActionsDescription, note: "Description, untrusted data"},
	{name: "owner_id", kind: kindInteger, note: "Numeric ID of the user or organization the variable belongs to"},
	{name: "repo_id", kind: kindInteger, note: "Numeric ID of the repository the variable belongs to"},
}

// adminArguments are the arguments of every administration tool; the schema of a tool allows only its own.
type adminArguments struct {
	Repository  string  `json:"repository"`
	Owner       string  `json:"owner"`
	Name        string  `json:"name"`
	NewName     string  `json:"new_name"`
	Value       *string `json:"value"`
	Description *string `json:"description"`
	SecretRef   string  `json:"secret_ref"`
	Limit       int     `json:"limit"`
	Cursor      string  `json:"cursor"`
}

// readAdmin decodes the arguments, chooses the level against the targets, and checks a name where the tool
// takes one; every refusal comes before the token and before any request.
func readAdmin(p *product, allowed allowlist, raw json.RawMessage, named bool) (adminArguments, actionsLevel, error) {
	var a adminArguments
	if err := decodeArguments(raw, &a); err != nil {
		return a, actionsLevel{}, err
	}
	level, err := actionsLevelOf(allowed, a.Repository, a.Owner)
	if err != nil {
		return a, actionsLevel{}, err
	}
	if named {
		if err := checkActionsName("name", a.Name); err != nil {
			return a, actionsLevel{}, err
		}
	}
	if a.Description != nil {
		if p != gitea {
			return a, actionsLevel{}, invalidArgument("description is a Gitea argument; " + p.name +
				" keeps no description")
		}
		if utf8.RuneCountInString(*a.Description) > maxActionsDescription || !plainTextAllowNewlines(*a.Description) {
			return a, actionsLevel{}, invalidArgument("description must hold at most 4096 characters without " +
				"control characters")
		}
	}
	return a, level, nil
}

func checkActionsName(argument, name string) error {
	if !actionsName.MatchString(name) || reservedActionsPrefix.MatchString(name) {
		return invalidArgument(argument + " must hold 1 to 255 letters, digits, and _, not start with a digit, " +
			"and not start with GITHUB_ or GITEA_")
	}
	return nil
}

func checkVariableValue(value *string) error {
	if value == nil {
		return invalidArgument("value is required")
	}
	if len(*value) > maxActionsValue || !plainTextAllowNewlines(*value) {
		return invalidArgument("value must hold at most 65536 bytes without control characters other than " +
			"line breaks and tabs")
	}
	return nil
}

// totalCount reads the X-Total-Count header Forgejo documents for these lists.
func totalCount(header http.Header) *int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-Total-Count")), 10, 64)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}

// onLevel reports whether a variable answer belongs to the level: one of an organization or a user names no
// repository. A repository variable carries the repository's ID, which the path already binds.
func (l actionsLevel) onLevel(row object) bool {
	if l.repo {
		return true
	}
	id, ok := row.integer("repo_id")
	return !ok || id == 0
}

// named is the result of a change: the name, the level, and what happened.
func (l actionsLevel) named(name string, extra map[string]any) map[string]any {
	result := map[string]any{"name": name}
	if l.repo {
		result["repository"] = l.name
	} else {
		result["owner"] = l.name
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func adminOutput(extra string, required ...string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"repository":{"type":"string"},` +
		`"owner":{"type":"string"}` + extra + `},"required":["name","` + strings.Join(required, `","`) + `"],` +
		`"additionalProperties":false}`)
}

func adminFields(nameNote string, extra ...capability.Field) []capability.Field {
	return append([]capability.Field{{Name: "name", Description: nameNote},
		{Name: "repository", Description: "Repository the tool acted on, as OWNER/REPO"},
		{Name: "owner", Description: "Owner the tool acted on, on the owner level"}}, extra...)
}

// listAdmin reads one batch of secrets or variables of the level and keeps the rows that belong to it.
func listAdmin(ctx context.Context, c *Client, op string, level actionsLevel, path, key string, members []member,
	page paging) (any, error) {
	if err := level.verify(ctx, c, op); err != nil {
		return nil, err
	}
	var rows []object
	header, err := c.get(ctx, op, path, page.query(), &rows)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row != nil && level.onLevel(row) {
			out = append(out, project(c.product, members, row))
		}
	}
	return level.result(key, out, totalCount(header), page.next(header, len(rows))), nil
}

// registerSecret adds a value about to be sent to the redactor of the call, beside the resolver's own
// registration, so that nothing the call reports afterwards can carry it.
func registerSecret(ctx context.Context, value string) {
	if red, ok := ctx.Value(redactorKey{}).(*redact.Redactor); ok && red != nil {
		red.Add(value)
	}
}

const levelNote = " of a repository, an organization, or the user behind the token inside the connection's targets"

var actionsSecretsList = &tool{
	group: "actions-secrets", action: "list", since: everywhere,
	needs: func(p *product, raw json.RawMessage) (version, bool) {
		var a struct {
			Owner string `json:"owner"`
		}
		if p == forgejo && json.Unmarshal(raw, &a) == nil && strings.HasPrefix(a.Owner, "users/") {
			return forgejoActions17, true
		}
		return version{}, false
	},
	describe: func(p *product) capability.Descriptor {
		user := "Forgejo lists the secrets of a user from version 17."
		if p == gitea {
			user = "Gitea does not list the secrets of a user."
		}
		return capability.Descriptor{
			Title: "List " + p.name + " Actions secrets",
			Description: "List one batch of the Actions secrets" + levelNote + ", by name and metadata; the API " +
				"never answers a value. " + user,
			Tags:         []string{p.id, "actions", "secrets", "list"},
			Risk:         readRisk(p.id + secretsSensitivity),
			InputSchema:  inputSchema(adminLevelProperties + "," + pagingKeys),
			OutputSchema: listOutput("secrets", schemaOf(p, actionsSecretMembers, "name")),
			Arguments:    append([]capability.Argument{adminRepositoryArgument, adminOwnerArgument}, pagingArguments...),
			Fields:       listFields("secrets", "Secrets with their name and metadata, never a value"),
			Examples: []capability.Example{{Description: "List the secrets of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, false)
		if err != nil {
			return nil, err
		}
		if level.login != "" && p == gitea {
			return nil, invalidArgument("Gitea does not list the secrets of a user; this operation is not " +
				"available for users/NAME")
		}
		path := level.base + "/secrets"
		page, err := startPaging(provider.CursorBinding(p.id, "actions-secrets.list", level.name, path), a.Limit,
			a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return listAdmin(ctx, c, "list actions secrets", level, path, "secrets", actionsSecretMembers, page)
		}, nil
	},
}

var actionsSecretsSet = &tool{
	group: "actions-secrets", action: "set", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := adminLevelProperties + `,"name":` + actionsNameSchema + `,"secret_ref":` + actionsSecretRefSchema
		arguments := []capability.Argument{adminRepositoryArgument, adminOwnerArgument,
			{Name: "name", Required: true, Description: "Name of the secret: letters, digits, and _, not starting " +
				"with a digit, GITHUB_, or GITEA_; the instance stores it in upper case"},
			{Name: "secret_ref", Required: true, SecretRef: true, Description: "Name of a released forward " +
				"credential with exactly one field, whose value becomes the secret; never a value"}}
		if p == gitea {
			properties += `,"description":` + actionsDescSchema
			arguments = append(arguments, adminDescriptionArgument)
		}
		return capability.Descriptor{
			Title: "Set a " + p.name + " Actions secret",
			Description: "Create or replace one Actions secret" + levelNote + ". The value is never an " +
				"argument: secret_ref names a forward credential with exactly one field that this connection " +
				"releases, read only for this confirmed request and sent once in its body; the answer is " +
				"discarded unread, and the result names the secret and whether it was created or replaced. " +
				"Idempotent; the request is never repeated after an unclear outcome. Offered only by a " +
				"connection whose tools list names it, in no profile",
			Tags:                  []string{p.id, "actions", "secrets", "set"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+secretsSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(properties, "name", "secret_ref"),
			OutputSchema: adminOutput(`,"outcome":{"type":"string","enum":["created","replaced","set"]}`,
				"outcome"),
			Arguments: arguments,
			Fields: adminFields("Name of the secret", capability.Field{Name: "outcome", Description: "created for a new " +
				"secret, replaced for an existing one, set where the instance does not tell"}),
			Examples: []capability.Example{{Description: "Set a deployment key from a released forward credential",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"DEPLOY_KEY",` +
					`"secret_ref":"deploy-key"}`)}},
		}
	},
	forward: func(p *product, allowed allowlist, resolved *config.Resolved, secrets *secret.Resolver,
		raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, true)
		if err != nil {
			return nil, err
		}
		// The form of the credential is configuration; its value is read only in the operation.
		if len(resolved.Forward[a.SecretRef].Fields) != 1 {
			return nil, invalidArgument("secret_ref must name a released forward credential with exactly one " +
				"field, whose value becomes the secret")
		}
		path := level.base + "/secrets/" + escape(a.Name)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "set actions secret"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			values, err := capability.ResolveSecretRef(ctx, resolved, secrets, a.SecretRef)
			if err != nil {
				return nil, err
			}
			var value string
			for _, field := range values {
				value = field
			}
			registerSecret(ctx, value)
			if value == "" || len(value) > maxActionsValue {
				return nil, invalidArgument("the forward credential must hold a value of 1 to 65536 bytes")
			}
			body := map[string]any{"data": value}
			if a.Description != nil {
				body["description"] = *a.Description
			}
			// The answer is never read, and a refusal keeps only its status: an instance that echoes the body
			// cannot hand the value on.
			status, err := c.changeWith(ctx, op, http.MethodPut, path, nil, body, nil, true)
			if err != nil {
				return nil, err
			}
			outcome := "set"
			switch status {
			case http.StatusCreated:
				outcome = "created"
			case http.StatusNoContent:
				outcome = "replaced"
			}
			return level.named(a.Name, map[string]any{"outcome": outcome}), nil
		}, nil
	},
}

// adminDelete is the listed-only delete of one secret or variable.
func adminDelete(group, what, segment, sensitivity string) *tool {
	return &tool{
		group: group, action: "delete", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Delete a " + p.name + " Actions " + what,
				Description: "Delete one Actions " + what + levelNote + " for good; workflows that use it no " +
					"longer receive it. Idempotent; the request is sent once and never repeated after an unclear " +
					"outcome. Offered only by a connection whose tools list names it, in no profile",
				Tags:                  []string{p.id, "actions", segment, "delete"},
				Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+sensitivity),
				RequiresToolAllowList: true,
				InputSchema:           inputSchema(adminLevelProperties+`,"name":`+actionsNameSchema, "name"),
				OutputSchema:          adminOutput(`,"deleted":{"type":"boolean"}`, "deleted"),
				Arguments: []capability.Argument{adminRepositoryArgument, adminOwnerArgument,
					{Name: "name", Required: true, Description: "Name of the " + what}},
				Fields: adminFields("Name of the "+what, capability.Field{Name: "deleted", Description: "True once the instance " +
					"deleted the " + what}),
				Examples: []capability.Example{{Description: "Delete a " + what + " of an organization",
					Arguments: json.RawMessage(`{"owner":"orgs/example","name":"DEPLOY_KEY"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			a, level, err := readAdmin(p, allowed, raw, true)
			if err != nil {
				return nil, err
			}
			path := level.base + "/" + segment + "/" + escape(a.Name)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "delete actions " + what
				if err := level.verify(ctx, c, op); err != nil {
					return nil, err
				}
				if err := c.change(ctx, op, http.MethodDelete, path, nil, nil); err != nil {
					return nil, err
				}
				return level.named(a.Name, map[string]any{"deleted": true}), nil
			}, nil
		},
	}
}

var (
	actionsSecretsDelete   = adminDelete("actions-secrets", "secret", "secrets", secretsSensitivity)
	actionsVariablesDelete = adminDelete("actions-variables", "variable", "variables", variablesSensitivity)
)

func variableSchema(p *product) string { return schemaOf(p, actionsVariableMembers, "name") }

var actionsVariablesList = &tool{
	group: "actions-variables", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List " + p.name + " Actions variables",
			Description: "List one batch of the Actions variables" + levelNote + ", with every member of each " +
				"variable; values and descriptions are untrusted data",
			Tags:         []string{p.id, "actions", "variables", "list"},
			Risk:         readRisk(p.id + variablesSensitivity),
			InputSchema:  inputSchema(adminLevelProperties + "," + pagingKeys),
			OutputSchema: listOutput("variables", variableSchema(p)),
			Arguments:    append([]capability.Argument{adminRepositoryArgument, adminOwnerArgument}, pagingArguments...),
			Fields:       listFields("variables", "Variables with every member of the variable object"),
			Examples: []capability.Example{{Description: "List the variables of an organization",
				Arguments: json.RawMessage(`{"owner":"orgs/example"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, false)
		if err != nil {
			return nil, err
		}
		path := level.base + "/variables"
		page, err := startPaging(provider.CursorBinding(p.id, "actions-variables.list", level.name, path), a.Limit,
			a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return listAdmin(ctx, c, "list actions variables", level, path, "variables", actionsVariableMembers, page)
		}, nil
	},
}

var actionsVariablesGet = &tool{
	group: "actions-variables", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " Actions variable",
			Description: "Read one Actions variable" + levelNote + ", with every member of the variable; the " +
				"value and description are untrusted data",
			Tags:         []string{p.id, "actions", "variables", "get"},
			Risk:         readRisk(p.id + variablesSensitivity),
			InputSchema:  inputSchema(adminLevelProperties+`,"name":`+actionsNameSchema, "name"),
			OutputSchema: json.RawMessage(variableSchema(p)),
			Arguments: []capability.Argument{adminRepositoryArgument, adminOwnerArgument,
				{Name: "name", Required: true, Description: "Name of the variable"}},
			Fields: fieldsOf(p, actionsVariableMembers),
			Examples: []capability.Example{{Description: "Read a variable",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"REGISTRY"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, true)
		if err != nil {
			return nil, err
		}
		path := level.base + "/variables/" + escape(a.Name)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get actions variable"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			var row object
			if _, err := c.get(ctx, op, path, nil, &row); err != nil {
				return nil, err
			}
			if name, ok := row.text("name"); !ok || !strings.EqualFold(name, a.Name) || !level.onLevel(row) {
				return nil, provider.InvalidResponse(op, c.product.name+" returned a variable other than the "+
					"requested one")
			}
			return project(p, actionsVariableMembers, row), nil
		}, nil
	},
}

func variableWriteProperties(p *product, rename bool) (string, []capability.Argument) {
	properties := adminLevelProperties + `,"name":` + actionsNameSchema + `,"value":` + actionsValueSchema
	arguments := []capability.Argument{adminRepositoryArgument, adminOwnerArgument,
		{Name: "name", Required: true, Description: "Name of the variable: letters, digits, and _, not starting " +
			"with a digit, GITHUB_, or GITEA_; the instance stores it in upper case"},
		{Name: "value", Required: true, Description: "Value of the variable, at most 65536 bytes; line breaks " +
			"and tabs are kept, other control characters refused; not a secret, so put none into it"}}
	if rename {
		properties += `,"new_name":` + actionsNameSchema
		arguments = append(arguments, capability.Argument{Name: "new_name", Description: "New name of the " +
			"variable, in the form of name; the name stays when left out"})
	}
	if p == gitea {
		properties += `,"description":` + actionsDescSchema
		arguments = append(arguments, adminDescriptionArgument)
	}
	return properties, arguments
}

func variableBody(a adminArguments) map[string]any {
	body := map[string]any{"value": *a.Value}
	if a.Description != nil {
		body["description"] = *a.Description
	}
	return body
}

var actionsVariablesCreate = &tool{
	group: "actions-variables", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties, arguments := variableWriteProperties(p, false)
		return capability.Descriptor{
			Title: "Create a " + p.name + " Actions variable",
			Description: "Create one Actions variable" + levelNote + ". Idempotent: the instance refuses a name " +
				"that exists, so a repetition creates nothing. The request is sent once and never repeated after " +
				"an unclear outcome",
			Tags:         []string{p.id, "actions", "variables", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent, p.id+variablesSensitivity),
			InputSchema:  inputSchema(properties, "name", "value"),
			OutputSchema: adminOutput(`,"created":{"type":"boolean"}`, "created"),
			Arguments:    arguments,
			Fields: adminFields("Name of the variable", capability.Field{Name: "created", Description: "True once the " +
				"instance created the variable"}),
			Examples: []capability.Example{{Description: "Create a repository variable",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"REGISTRY",` +
					`"value":"registry.example.com"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, true)
		if err != nil {
			return nil, err
		}
		if err := checkVariableValue(a.Value); err != nil {
			return nil, err
		}
		path, body := level.base+"/variables/"+escape(a.Name), variableBody(a)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "create actions variable"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodPost, path, body, nil); err != nil {
				return nil, err
			}
			return level.named(a.Name, map[string]any{"created": true}), nil
		}, nil
	},
}

var actionsVariablesUpdate = &tool{
	group: "actions-variables", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties, arguments := variableWriteProperties(p, true)
		return capability.Descriptor{
			Title: "Update a " + p.name + " Actions variable",
			Description: "Replace the value of one Actions variable" + levelNote + ", and rename it with " +
				"new_name. Idempotent; the request is sent once and never repeated after an unclear outcome",
			Tags:         []string{p.id, "actions", "variables", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+variablesSensitivity),
			InputSchema:  inputSchema(properties, "name", "value"),
			OutputSchema: adminOutput(`,"previous_name":{"type":"string"},"updated":{"type":"boolean"}`, "updated"),
			Arguments:    arguments,
			Fields: adminFields("Name of the variable after the update",
				capability.Field{Name: "previous_name", Description: "Name before a rename"},
				capability.Field{Name: "updated", Description: "True once the instance updated the variable"}),
			Examples: []capability.Example{{Description: "Change the value of a variable",
				Arguments: json.RawMessage(`{"repository":"example/project","name":"REGISTRY",` +
					`"value":"registry.example.org"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readAdmin(p, allowed, raw, true)
		if err != nil {
			return nil, err
		}
		if err := checkVariableValue(a.Value); err != nil {
			return nil, err
		}
		body, result := variableBody(a), map[string]any{"updated": true}
		name := a.Name
		if a.NewName != "" {
			if err := checkActionsName("new_name", a.NewName); err != nil {
				return nil, err
			}
			body["name"], result["previous_name"], name = a.NewName, a.Name, a.NewName
		}
		path := level.base + "/variables/" + escape(a.Name)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "update actions variable"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodPut, path, body, nil); err != nil {
				return nil, err
			}
			return level.named(name, result), nil
		}, nil
	},
}
