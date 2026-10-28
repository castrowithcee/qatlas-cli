package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The organization tools read and change organizations themselves. An organization is bound by an owner target
// of scope orgs (orgs/NAME) or by a connection without targets; users/NAME does not name an organization. The
// instance-wide lists are offered only without targets, because their rows reach past any target, and the list
// of the token's own organizations is checked row by row. Nothing here lists or deletes the repositories of an
// organization, and no address (website, avatar) is ever fetched.

const (
	orgSensitivity = "-organization-data"

	maxOrgFullName    = 255
	maxOrgEmail       = 255
	maxOrgDescription = 2048
	maxOrgLocation    = 255
)

var orgMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the organization"},
	{name: "name", kind: kindText, note: "Organization name, untrusted data"},
	{name: "full_name", kind: kindText, note: "Display name, untrusted data"},
	{name: "email", kind: kindText, note: "Contact email address of the organization; personal data"},
	{name: "avatar_url", kind: kindText, note: "Address of the avatar image, never fetched"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "website", kind: kindText, note: "Website, untrusted data, never fetched"},
	{name: "location", kind: kindText, note: "Location, untrusted data"},
	{name: "visibility", kind: kindText, note: "public, limited, or private"},
	{name: "repo_admin_change_team_access", kind: kindFlag, note: "True when repository administrators may change " +
		"team access"},
	{name: "created", kind: kindText, only: forgejo, note: "Creation time"},
}

var orgPermissionMembers = []member{
	{name: "is_owner", kind: kindFlag, note: "True when the user owns the organization"},
	{name: "is_admin", kind: kindFlag, note: "True when the user administers the organization"},
	{name: "can_write", kind: kindFlag, note: "True when the user may write in the organization"},
	{name: "can_read", kind: kindFlag, note: "True when the user may read the organization"},
	{name: "can_create_repository", kind: kindFlag, note: "True when the user may create repositories in the organization"},
}

var orgArgument = capability.Argument{Name: "organization", Required: true, Description: "Organization name; the " +
	"connection's targets must name it as orgs/NAME (users/NAME does not), or name no targets at all"}

const orgListedNote = " Only a connection whose tools list names it offers this tool."

// organizationOf reads the organization a tool acts on. Only an owner target of scope orgs allows it. Every
// refusal is an invalid request that never quotes the value.
func (a allowlist) organizationOf(value string) (string, error) {
	if value == "" {
		return "", invalidArgument("organization is required")
	}
	if !validName(value) {
		return "", invalidArgument("organization must be an organization name")
	}
	if !a.namesOwnerOfScope("orgs", value) {
		return "", outsideTargets("organization")
	}
	return value, nil
}

func orgPath(name string) string { return "/orgs/" + escape(name) }

// requireOrgAnswer proves that an answer is the organization that was asked for.
func requireOrgAnswer(p *product, op, name string, row object, change bool) error {
	if got, ok := row.text("name"); !ok || !strings.EqualFold(got, name) {
		failure := provider.InvalidResponse(op, p.name+" returned another organization than the one asked for")
		if change {
			return unsure(failure)
		}
		return failure
	}
	return nil
}

func orgProperties(extra string) string {
	if extra == "" {
		return `"organization":` + organizationSchema
	}
	return `"organization":` + organizationSchema + `,` + extra
}

func orgOutput(p *product, extra string, required ...string) json.RawMessage {
	if extra != "" {
		extra = "," + extra
	}
	names := make([]string, len(required))
	for i, name := range required {
		names[i] = `"` + name + `"`
	}
	return json.RawMessage(`{"type":"object","properties":{"organization":{"type":"string"}` + extra + `},` +
		`"required":[` + strings.Join(names, ",") + `],"additionalProperties":false}`)
}

// ---- lists --------------------------------------------------------------------------------------------------

func orgListOutput(p *product, extra string) json.RawMessage {
	if extra != "" {
		extra += ","
	}
	return json.RawMessage(`{"type":"object","properties":{"organizations":{"type":"array","items":` +
		schemaOf(p, orgMembers) + `},` + extra + `"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["organizations","has_more"],"additionalProperties":false}`)
}

// orgList is one of the three lists of organizations.
type orgList struct {
	action, title, text string
	// username marks the list of another user's organizations.
	username bool
	// open marks a list that reaches past any target and is offered only without targets.
	open bool
	path string
}

func newOrgList(s orgList) *tool {
	return &tool{
		group: "orgs", action: s.action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties, arguments, extra := pagingKeys, append([]capability.Argument(nil), pagingArguments...), ""
			example := `{"limit":20}`
			if s.username {
				properties = `"username":` + organizationSchema + `,` + pagingKeys
				arguments = append([]capability.Argument{{Name: "username", Required: true,
					Description: "User name whose organizations are listed"}}, arguments...)
				extra = `"username":{"type":"string"}`
				example = `{"username":"octo","limit":20}`
			}
			fields := []capability.Field{{Name: "organizations", Description: "Organizations with every member of the " +
				"organization object"}}
			if s.username {
				fields = append(fields, capability.Field{Name: "username", Description: "User the tool listed"})
			}
			input := inputSchema(properties)
			if s.username {
				input = inputSchema(properties, "username")
			}
			return capability.Descriptor{
				Title:        strings.ReplaceAll(s.title, "%s", p.name),
				Description:  s.text,
				Tags:         []string{p.id, "orgs", "list"},
				Risk:         readRisk(p.id + orgSensitivity),
				InputSchema:  input,
				OutputSchema: orgListOutput(p, extra),
				Arguments:    arguments,
				Fields:       append(fields, pagingFields...),
				Examples: []capability.Example{{Description: strings.ReplaceAll(s.title, "%s", p.name),
					Arguments: json.RawMessage(example)}},
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
			if s.open && allowed.bound() {
				return nil, invalidArgument("this list reaches organizations beyond the connection's targets, so a " +
					"connection with targets does not offer it; use a connection without targets")
			}
			path := s.path
			if s.username {
				if !validName(a.Username) {
					return nil, invalidArgument("username must be a user name")
				}
				path = "/users/" + escape(a.Username) + "/orgs"
			}
			binding := provider.CursorBinding(p.id, "orgs."+s.action, strings.ToLower(a.Username))
			page, err := startPaging(binding, a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "list organizations"
				var rows []object
				header, err := c.get(ctx, op, path, page.query(), &rows)
				if err != nil {
					return nil, err
				}
				organizations := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if row == nil {
						continue
					}
					// Every row is checked against the targets; without targets all are allowed.
					name, _ := row.text("name")
					if allowed.bound() && (name == "" || !allowed.namesOwnerOfScope("orgs", name)) {
						continue
					}
					organizations = append(organizations, project(p, orgMembers, row))
				}
				next := page.next(header, len(rows))
				result := map[string]any{"organizations": organizations, "has_more": next != ""}
				if next != "" {
					result["next_cursor"] = next
				}
				if s.username {
					result["username"] = a.Username
				}
				return result, nil
			}, nil
		},
	}
}

var orgsList = newOrgList(orgList{action: "list", title: "List %s organizations", open: true, path: "/orgs",
	text: "List one batch of the organizations of the whole instance that the token sees, with every member of each " +
		"organization. The list is instance-wide, so only a connection without targets offers it; one with targets " +
		"refuses it before any request. Names and texts are untrusted data."})

var orgsListMine = newOrgList(orgList{action: "list-mine", title: "List the %s organizations of this account",
	path: "/user/orgs",
	text: "List one batch of the organizations of the account behind the token, with every member of each " +
		"organization; rows the connection's targets do not name as orgs/NAME are left out, so a batch may be short " +
		"or empty while has_more is true. Names and texts are untrusted data."})

var orgsListUser = newOrgList(orgList{action: "list-user", title: "List the %s organizations of a user",
	username: true, open: true,
	text: "List one batch of the organizations of one user, with every member of each organization. The list reaches " +
		"past any target, so only a connection without targets offers it; one with targets refuses it before any " +
		"request. Names and texts are untrusted data."})

// ---- get ----------------------------------------------------------------------------------------------------

var orgsGet = &tool{
	group: "orgs", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " organization",
			Description: "Read one organization the connection's targets name as orgs/NAME with every member of the " +
				"organization object; texts are untrusted data",
			Tags:         []string{p.id, "orgs", "get"},
			Risk:         readRisk(p.id + orgSensitivity),
			InputSchema:  inputSchema(orgProperties(""), "organization"),
			OutputSchema: json.RawMessage(schemaOf(p, orgMembers, "id", "name")),
			Arguments:    []capability.Argument{orgArgument},
			Fields:       fieldsOf(p, orgMembers),
			Examples: []capability.Example{{Description: "Read an organization",
				Arguments: json.RawMessage(`{"organization":"example-org"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get organization"
			var org object
			if _, err := c.get(ctx, op, orgPath(name), nil, &org); err != nil {
				return nil, err
			}
			if err := requireOrgAnswer(p, op, name, org, false); err != nil {
				return nil, err
			}
			return project(p, orgMembers, org), nil
		}, nil
	},
}

// ---- create and update --------------------------------------------------------------------------------------

func orgTextField(name, note string, max int, newlines bool) editField {
	field := textField(name, note, max)
	if newlines {
		field.check = func(_ *product, raw json.RawMessage) (any, error) {
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return nil, invalidArgument(name + " must be a text")
			}
			if utf8.RuneCountInString(*value) > max || !plainTextAllowNewlines(*value) {
				return nil, invalidArgument(name + " must hold at most " + itoa(max) + " characters without control characters")
			}
			return *value, nil
		}
	}
	return field
}

// orgFields are the settings both EditOrgOption and CreateOrgOption document, in both products.
var orgFields = []editField{
	orgTextField("full_name", "Display name, at most 255 characters", maxOrgFullName, false),
	orgTextField("email", "Contact email address; empty clears it on update", maxOrgEmail, false),
	orgTextField("description", "Description, at most 2048 characters", maxOrgDescription, true),
	orgTextField("website", "Website, stored as given and never fetched, at most 1024 characters", maxRepoWebsite, false),
	orgTextField("location", "Location, at most 255 characters", maxOrgLocation, false),
	enumFieldFor("visibility", "public, limited, or private", nil, []string{"public", "limited", "private"}),
	flagField("repo_admin_change_team_access", "True to let repository administrators change team access", nil),
}

func orgFieldProperties(p *product) string {
	var properties []string
	for _, field := range orgFields {
		if field.offered(p) {
			properties = append(properties, `"`+field.name+`":`+field.schema(p))
		}
	}
	return strings.Join(properties, ",")
}

func orgFieldArguments(p *product) []capability.Argument {
	var arguments []capability.Argument
	for _, field := range orgFields {
		if field.offered(p) {
			arguments = append(arguments, capability.Argument{Name: field.name, Description: field.note})
		}
	}
	return arguments
}

// orgBody checks the settings given and returns the request body. Only the documented settings are offered;
// another member is refused before anything is sent.
func orgBody(p *product, given map[string]json.RawMessage) (map[string]any, error) {
	body := map[string]any{}
	for key, value := range given {
		var found *editField
		for i := range orgFields {
			if orgFields[i].name == key {
				found = &orgFields[i]
			}
		}
		switch {
		case found == nil:
			return nil, invalidArgument("an argument is not offered by this tool")
		case !found.offered(p):
			return nil, otherProduct(p, found.name)
		}
		checked, err := found.check(p, value)
		if err != nil {
			return nil, err
		}
		body[key] = checked
	}
	return body, nil
}

func readGiven(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var given map[string]json.RawMessage
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if json.Unmarshal(raw, &given) != nil {
		return nil, invalidArgument("the arguments could not be read")
	}
	return given, nil
}

// takeText removes a text member from the given arguments.
func takeText(given map[string]json.RawMessage, key string) (string, error) {
	value, ok := given[key]
	if !ok {
		return "", nil
	}
	delete(given, key)
	var text string
	if json.Unmarshal(value, &text) != nil {
		return "", invalidArgument(key + " must be a text")
	}
	return text, nil
}

var orgsCreate = &tool{
	group: "orgs", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Create a " + p.name + " organization",
			Description: "Create one organization whose name the connection's targets name as orgs/NAME, or any name " +
				"on a connection without targets, so that the new organization lies inside the targets. The instance " +
				"may refuse to let this account create organizations.",
			Tags:         []string{p.id, "orgs", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+orgSensitivity),
			InputSchema:  inputSchema(`"username":`+organizationSchema+`,`+orgFieldProperties(p), "username"),
			OutputSchema: json.RawMessage(schemaOf(p, orgMembers, "id", "name")),
			Arguments: append([]capability.Argument{{Name: "username", Required: true, Description: "Name of the " +
				"new organization; the connection's targets must name it as orgs/NAME"}}, orgFieldArguments(p)...),
			Fields: fieldsOf(p, orgMembers),
			Examples: []capability.Example{{Description: "Create an organization",
				Arguments: json.RawMessage(`{"username":"example-org","visibility":"private"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		given, err := readGiven(raw)
		if err != nil {
			return nil, err
		}
		name, err := takeText(given, "username")
		if err != nil {
			return nil, err
		}
		name, err = allowed.organizationOf(name)
		if err != nil {
			return nil, err
		}
		body, err := orgBody(p, given)
		if err != nil {
			return nil, err
		}
		body["username"] = name
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "create organization"
			var created object
			if err := c.change(ctx, op, http.MethodPost, "/orgs", body, &created); err != nil {
				return nil, err
			}
			if err := requireOrgAnswer(p, op, name, created, true); err != nil {
				return nil, err
			}
			return project(p, orgMembers, created), nil
		}, nil
	},
}

var orgsUpdate = &tool{
	group: "orgs", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Change a " + p.name + " organization",
			Description: "Change the settings of one organization the connection's targets name as orgs/NAME with one " +
				"request; only the arguments given change. The name changes with orgs.rename, the avatar with " +
				"org-avatars.update.",
			Tags:         []string{p.id, "orgs", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+orgSensitivity),
			InputSchema:  inputSchema(orgProperties(orgFieldProperties(p)), "organization"),
			OutputSchema: json.RawMessage(schemaOf(p, orgMembers, "id", "name")),
			Arguments:    append([]capability.Argument{orgArgument}, orgFieldArguments(p)...),
			Fields:       fieldsOf(p, orgMembers),
			Examples: []capability.Example{{Description: "Change the description of an organization",
				Arguments: json.RawMessage(`{"organization":"example-org","description":"Example organization"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		given, err := readGiven(raw)
		if err != nil {
			return nil, err
		}
		value, err := takeText(given, "organization")
		if err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(value)
		if err != nil {
			return nil, err
		}
		body, err := orgBody(p, given)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, invalidArgument("pass at least one setting to change")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "change organization"
			var changed object
			if err := c.change(ctx, op, http.MethodPatch, orgPath(name), body, &changed); err != nil {
				return nil, err
			}
			if err := requireOrgAnswer(p, op, name, changed, true); err != nil {
				return nil, err
			}
			return project(p, orgMembers, changed), nil
		}, nil
	},
}

// ---- rename and delete --------------------------------------------------------------------------------------

var orgsRename = &tool{
	group: "orgs", action: "rename", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Rename a " + p.name + " organization",
			Description: "Rename one organization the connection's targets name as orgs/NAME. Its addresses change, and " +
				"an orgs/NAME target no longer reaches it afterwards. The answer names the organization only while " +
				"the new name lies inside the connection's targets." + orgListedNote,
			Tags:                  []string{p.id, "orgs", "rename", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+orgSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(orgProperties(`"new_name":`+organizationSchema), "organization", "new_name"),
			OutputSchema:          orgOutput(p, `"renamed":{"type":"boolean"},"reachable":{"type":"boolean"}`, "renamed", "reachable"),
			Arguments: []capability.Argument{orgArgument, {Name: "new_name", Required: true,
				Description: "New name of the organization; the instance refuses a name another account uses"}},
			Fields: []capability.Field{{Name: "renamed", Description: "True once the instance renamed the organization"},
				{Name: "reachable", Description: "True when the new name lies inside the connection's targets; false " +
					"when the rename moved it out of them, then no name is shown"},
				{Name: "organization", Description: "New name of the organization, only when reachable"}},
			Examples: []capability.Example{{Description: "Rename an organization",
				Arguments: json.RawMessage(`{"organization":"example-org","new_name":"example-team"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			NewName      string `json:"new_name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, err
		}
		if !validName(a.NewName) {
			return nil, invalidArgument("new_name must be an organization name")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			body := map[string]any{"new_name": a.NewName}
			if err := c.change(ctx, "rename organization", http.MethodPost, orgPath(name)+"/rename", body, nil); err != nil {
				return nil, err
			}
			result := map[string]any{"renamed": true, "reachable": false}
			if allowed.namesOwnerOfScope("orgs", a.NewName) {
				result["reachable"] = true
				result["organization"] = a.NewName
			}
			return result, nil
		}, nil
	},
}

// orgAction is a change of one organization with one fixed method and path suffix and no body.
type orgAction struct {
	group, action, title, text string
	listed                     bool
	effect                     capability.Effect
	idempotency                capability.Idempotency
	suffix, op, key            string
	tags                       []string
}

func newOrgAction(s orgAction) *tool {
	return &tool{
		group: s.group, action: s.action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			text := s.text
			if s.listed {
				text += orgListedNote
			}
			return capability.Descriptor{
				Title:                 strings.ReplaceAll(s.title, "%s", p.name),
				Description:           text,
				Tags:                  append([]string{p.id}, s.tags...),
				Risk:                  changeRisk(s.effect, s.idempotency, p.id+orgSensitivity),
				RequiresToolAllowList: s.listed,
				InputSchema:           inputSchema(orgProperties(""), "organization"),
				OutputSchema:          orgOutput(p, `"`+s.key+`":{"type":"boolean"}`, "organization", s.key),
				Arguments:             []capability.Argument{orgArgument},
				Fields: []capability.Field{{Name: "organization", Description: "Organization the tool acted on"},
					{Name: s.key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: strings.ReplaceAll(s.title, "%s", p.name),
					Arguments: json.RawMessage(`{"organization":"example-org"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Organization string `json:"organization"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			name, err := allowed.organizationOf(a.Organization)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.change(ctx, s.op, http.MethodDelete, orgPath(name)+s.suffix, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"organization": name, s.key: true}, nil
			}, nil
		},
	}
}

var orgsDelete = newOrgAction(orgAction{
	group: "orgs", action: "delete", listed: true,
	title: "Delete a %s organization",
	text: "Delete one organization the connection's targets name as orgs/NAME for good. The instance refuses while " +
		"the organization still owns a repository or a package; this tool deletes neither.",
	effect: capability.EffectDelete, idempotency: capability.IdempotencyIdempotent,
	op: "delete organization", key: "deleted", tags: []string{"orgs", "delete"},
})

var orgAvatarsDelete = newOrgAction(orgAction{
	group: "org-avatars", action: "delete",
	title: "Delete the avatar of a %s organization",
	text: "Remove the avatar of one organization the connection's targets name as orgs/NAME; the instance shows " +
		"its default image again.",
	effect: capability.EffectDelete, idempotency: capability.IdempotencyIdempotent,
	suffix: "/avatar", op: "delete organization avatar", key: "deleted", tags: []string{"org-avatars", "delete"},
})

var orgAvatarsUpdate = &tool{
	group: "org-avatars", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Set the avatar of a " + p.name + " organization",
			Description: "Replace the avatar of one organization the connection's targets name as orgs/NAME with a local " +
				"image file of at most 4 MiB, read from a directory the connection releases for reading and sent " +
				"base64 encoded. The instance may refuse a type or size its settings do not allow.",
			Tags:       []string{p.id, "org-avatars", "update"},
			Risk:       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+orgSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(orgProperties(`"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema),
				"organization", localfile.LocalPathArgument),
			OutputSchema: orgOutput(p, `"updated":{"type":"boolean"}`, "organization", "updated"),
			Arguments: []capability.Argument{orgArgument, {Name: localfile.LocalPathArgument, Required: true,
				Description: "Image file to upload, absolute or starting with ~/, inside a directory the " +
					"connection releases for reading"}},
			Fields: []capability.Field{{Name: "organization", Description: "Organization the tool acted on"},
				{Name: "updated", Description: "True once the instance accepted the image"}},
			Examples: []capability.Example{{Description: "Set the avatar from a local file",
				Arguments: json.RawMessage(`{"organization":"example-org","local_path":"~/images/logo.png"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var a struct {
			Organization string  `json:"organization"`
			LocalPath    *string `json:"local_path"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, nil, err
		}
		body, release, err := avatarBody(ctx, resolved, a.LocalPath, "set organization avatar")
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "set organization avatar", http.MethodPost, orgPath(name)+"/avatar", body,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"organization": name, "updated": true}, nil
		}, release, nil
	},
}

// ---- activities and permissions -----------------------------------------------------------------------------

var orgActivitiesList = &tool{
	group: "org-activities", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the " + p.name + " activities of an organization",
			Description: "List one batch of the activity feed of one organization the connection's targets name as " +
				"orgs/NAME with every member of each activity; the feed names the users who acted, and its texts are " +
				"untrusted data",
			Tags:        []string{p.id, "org-activities", "list"},
			Risk:        readRisk(p.id + activitySensitivity),
			InputSchema: inputSchema(orgProperties(`"date":`+activitySchemaDt+`,`+pagingKeys), "organization"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"activities":{"type":"array","items":` +
				schemaOf(p, activityMembers) + `},"organization":{"type":"string"},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["activities","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{orgArgument,
				{Name: "date", Description: "Only activities of this day, YYYY-MM-DD"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "activities", Description: "Activities with every member of the " +
				"activity object"}, {Name: "organization", Description: "Organization the tool listed"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the activities of a day",
				Arguments: json.RawMessage(`{"organization":"example-org","date":"2030-01-02"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			Date         string `json:"date"`
			Limit        int    `json:"limit"`
			Cursor       string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Date != "" {
			if _, err := time.Parse("2006-01-02", a.Date); err != nil {
				return nil, invalidArgument("date must be a day as YYYY-MM-DD")
			}
			filters.Set("date", a.Date)
		}
		binding := provider.CursorBinding(p.id, "org-activities.list", strings.ToLower(name), "", filters.Encode())
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			for key, values := range filters {
				query[key] = values
			}
			var rows []object
			header, err := c.get(ctx, "list organization activities", orgPath(name)+"/activities/feeds", query, &rows)
			if err != nil {
				return nil, err
			}
			activities := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				// An activity without a repository proves nothing about where it belongs; the repository must be
				// the organization's own and inside the targets.
				if inner, ok := row.child("repo"); ok {
					fullName, _ := inner.text("full_name")
					if owner, _, _ := strings.Cut(fullName, "/"); allowed.allowsFullName(fullName) &&
						strings.EqualFold(owner, name) {
						activities = append(activities, project(p, activityMembers, row))
					}
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"activities": activities, "has_more": next != "", "organization": name}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var orgPermissionsGet = &tool{
	group: "org-permissions", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the " + p.name + " permissions of a user in an organization",
			Description: "Read what one user may do in one organization the connection's targets name as orgs/NAME. " +
				"The instance answers only to a user who may see the organization.",
			Tags:        []string{p.id, "org-permissions", "get"},
			Risk:        readRisk(p.id + orgSensitivity),
			InputSchema: inputSchema(orgProperties(`"username":`+organizationSchema), "organization", "username"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization":{"type":"string"},` +
				`"username":{"type":"string"},"permissions":` + schemaOf(p, orgPermissionMembers) + `},` +
				`"required":["organization","username","permissions"],"additionalProperties":false}`),
			Arguments: []capability.Argument{orgArgument, {Name: "username", Required: true,
				Description: "User name whose permissions are read"}},
			Fields: []capability.Field{{Name: "organization", Description: "Organization the tool read"},
				{Name: "username", Description: "User the tool read"},
				{Name: "permissions", Description: "Permissions with every member of the permissions object"}},
			Examples: []capability.Example{{Description: "Read the permissions of a user",
				Arguments: json.RawMessage(`{"organization":"example-org","username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			Username     string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, err
		}
		if !validName(a.Username) {
			return nil, invalidArgument("username must be a user name")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			path := "/users/" + escape(a.Username) + "/orgs/" + escape(name) + "/permissions"
			if _, err := c.get(ctx, "get organization permissions", path, nil, &answer); err != nil {
				return nil, err
			}
			return map[string]any{"organization": name, "username": a.Username,
				"permissions": project(p, orgPermissionMembers, answer)}, nil
		}, nil
	},
}
