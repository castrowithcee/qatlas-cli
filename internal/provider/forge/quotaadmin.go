package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The quota administration tools read and manage the quota groups, rules, group members, and quotas of users of
// the whole instance. Forgejo only; Gitea has no quota. They reach every user, so only a connection without
// targets may call them: any target is refused before the token is read and without being named. The
// instance answers them to site administrators only.
//
// An instance serves the routes only while its quota is switched on, so a missing quota answers 404, as a missing
// group, rule, or user does. After a 404 the tools read the rule list to tell the two apart: if that answers, the
// object is missing; if it answers 404 as well, GET /admin/users?limit=1 (a route that exists with or without
// quota) decides: if that is reachable, the quota is reported as not available. Group, rule, and user names are
// untrusted data and travel as one escaped path segment. A change is sent once and never repeated.

const (
	quotaAdminSensitivity = "-quota-administration"
	maxQuotaAdminName     = 255
	// maxQuotaAdminRows bounds an unpaged list; the instance has no paging for them.
	maxQuotaAdminRows = 200
	// maxUserQuotaGroups bounds the groups a user is set to.
	maxUserQuotaGroups = 100
	// quotaUnlimited is the limit Forgejo reads as no limit.
	quotaUnlimited = -1

	quotaAdminGroups = "/admin/quota/groups"
	quotaAdminRules  = "/admin/quota/rules"
	quotaAdminUsers  = "/admin/users"

	quotaAdminNameSchema = `{"type":"string","minLength":1,"maxLength":255}`
	quotaLimitSchema     = `{"type":"integer","minimum":-1,"maximum":9007199254740991}`
)

var quotaGroupMembers = []member{
	{name: "name", kind: kindText, note: "Name of the group, shown to administrators only"},
	{name: "rules", kind: kindObjects, note: "Rules of the group", sub: quotaRuleMembers},
}

var (
	quotaAdminGroupArgument = capability.Argument{Name: "group", Required: true,
		Description: "Name of the quota group, 1 to 255 characters without control characters and \"/\""}
	quotaAdminRuleArgument = capability.Argument{Name: "rule", Required: true,
		Description: "Name of the quota rule, 1 to 255 characters without control characters and \"/\""}
	quotaAdminUserArgument  = capability.Argument{Name: "username", Required: true, Description: "User name"}
	quotaAdminLimitArgument = capability.Argument{Name: "limit",
		Description: "Limit in bytes; -1 for no limit"}
)

func quotaSubjectsSchema() string {
	quoted := make([]string, len(quotaSubjects))
	for i, subject := range quotaSubjects {
		quoted[i] = `"` + subject + `"`
	}
	return `{"type":"array","minItems":1,"maxItems":` + itoa64(int64(len(quotaSubjects))) +
		`,"items":{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}}`
}

func quotaAdminSubjectsArgument(required bool) capability.Argument {
	return capability.Argument{Name: "subjects", Required: required, Description: "Subjects the rule limits, from: " +
		strings.Join(quotaSubjects, ", ")}
}

// quotaAdminCall holds the arguments of every tool of this file; the schema of a tool allows only its own.
type quotaAdminCall struct {
	Group    string   `json:"group"`
	Rule     string   `json:"rule"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Limit    *int64   `json:"limit"`
	Subjects []string `json:"subjects"`
	Groups   []string `json:"groups"`
}

// instanceWideOnly refuses a connection with targets: the tools reach every user of the instance.
func (a allowlist) instanceWideOnly() error {
	if len(a) != 0 {
		return invalidArgument("quota administration reaches the whole instance, so this connection's targets do " +
			"not allow it; use a connection without targets")
	}
	return nil
}

// checkQuotaAdminName checks a group or rule name without quoting it.
func checkQuotaAdminName(argument, value string) error {
	if value == "" || utf8.RuneCountInString(value) > maxQuotaAdminName || !plainText(value) ||
		strings.Contains(value, "/") || value == "." || value == ".." {
		return invalidArgument(argument + " must hold 1 to 255 characters without control characters and \"/\", " +
			"and not be \".\" or \"..\"")
	}
	return nil
}

func checkQuotaAdminSubjects(subjects []string) error {
	if len(subjects) == 0 || len(subjects) > len(quotaSubjects) {
		return invalidArgument("subjects must name 1 to " + itoa64(int64(len(quotaSubjects))) + " quota subjects")
	}
	for i, subject := range subjects {
		if !slices.Contains(quotaSubjects, subject) || slices.Contains(subjects[:i], subject) {
			return invalidArgument("subjects must be distinct quota subjects the tool lists")
		}
	}
	return nil
}

func checkQuotaAdminLimit(limit int64) error {
	if limit < quotaUnlimited {
		return invalidArgument("limit must be -1 for no limit, or a size in bytes")
	}
	return nil
}

// ---- requests --------------------------------------------------------------------------------------------------

// quotaAdminAbsent tells a switched-off quota from a missing object after a 404; original is returned unless the
// quota is positively found off. The extra reads are never changes.
func (c *Client) quotaAdminAbsent(ctx context.Context, op, path string, original error) error {
	if path != quotaAdminRules && path != quotaAdminGroups {
		if served, err := c.probe(ctx, op, quotaAdminRules); err != nil || served {
			return original
		}
	}
	if _, _, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, quotaAdminUsers,
		url.Values{"limit": {"1"}}, nil); err == nil {
		return keepOriginal(provider.Fail(op, quotaOffMessage), original)
	}
	return original
}

func (c *Client) quotaAdminGet(ctx context.Context, op, path string, out any) error {
	status, _, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, nil, out)
	if err != nil && status == http.StatusNotFound {
		err = c.quotaAdminAbsent(ctx, op, path, err)
	}
	return err
}

// quotaAdminChange sends the one request of a change; its answer is not read.
func (c *Client) quotaAdminChange(ctx context.Context, op, method, path string, body any) error {
	status, err := c.changeQuery(ctx, op, method, path, nil, body, nil)
	if err != nil && status == http.StatusNotFound {
		err = c.quotaAdminAbsent(ctx, op, path, err)
	}
	return err
}

// ---- the tools -------------------------------------------------------------------------------------------------

// quotaAdminSpec describes one tool; the shared parts are built by tool().
type quotaAdminSpec struct {
	group, action, title, text string
	risk                       func(p *product) capability.Risk
	listedOnly                 bool
	properties                 string
	required                   []string
	arguments                  []capability.Argument
	output                     func(p *product) json.RawMessage
	fields                     func(p *product) []capability.Field
	example, exampleText       string
	// prepare checks the arguments and returns the operation; it runs before any request.
	prepare func(p *product, a quotaAdminCall) (run, error)
}

func (s quotaAdminSpec) tool() *tool {
	return &tool{
		group: s.group, action: s.action, since: map[string]version{forgejo.id: forgejo.minimum},
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: s.title, Description: s.text,
				Tags:                  []string{p.id, "quota", "admin", s.group, s.action},
				Risk:                  s.risk(p),
				RequiresToolAllowList: s.listedOnly,
				InputSchema:           inputSchema(s.properties, s.required...),
				OutputSchema:          s.output(p),
				Arguments:             s.arguments,
				Fields:                s.fields(p),
				Examples: []capability.Example{{Description: s.exampleText,
					Arguments: json.RawMessage(s.example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			if err := allowed.instanceWideOnly(); err != nil {
				return nil, err
			}
			var a quotaAdminCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			return s.prepare(p, a)
		},
	}
}

func quotaReadRisk(p *product) capability.Risk { return readRisk(p.id + quotaSensitivity) }

// quotaChangeRisk is the strictest risk the provider has: a change on the whole instance that needs confirmation.
func quotaChangeRisk(effect capability.Effect) func(p *product) capability.Risk {
	return func(p *product) capability.Risk {
		return changeRisk(effect, capability.IdempotencyIdempotent, p.id+quotaAdminSensitivity)
	}
}

const (
	adminOnly = " The instance answers site administrators only. Only a connection without targets may call it; " +
		"one with targets is refused before any request. A switched-off quota is reported as not available."
	sentOnce = " Idempotent; the request is sent once and never repeated after an unclear outcome."
	listedBy = " Offered only by a connection whose tools list names it, in no profile."
)

func memberOutput(members []member, required ...string) func(*product) json.RawMessage {
	return func(p *product) json.RawMessage { return json.RawMessage(schemaOf(p, members, required...)) }
}

func memberFields(members []member) func(*product) []capability.Field {
	return func(p *product) []capability.Field { return fieldsOf(p, members) }
}

func staticFields(fields ...capability.Field) func(*product) []capability.Field {
	return func(*product) []capability.Field { return fields }
}

// cappedOutput is the output of an unpaged list: the rows, at most maxQuotaAdminRows, and has_more.
func cappedOutput(key string, item func(p *product) string) func(*product) json.RawMessage {
	return func(p *product) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":` + item(p) +
			`},"has_more":{"type":"boolean"}},"required":["` + key + `","has_more"],"additionalProperties":false}`)
	}
}

func cappedFields(key, what string) func(*product) []capability.Field {
	return staticFields(capability.Field{Name: key, Description: what + ", at most 200"},
		capability.Field{Name: "has_more", Description: "True when the instance holds more entries than the tool returns"})
}

// cappedList reads an unpaged list and keeps the first maxQuotaAdminRows rows.
func cappedList(ctx context.Context, c *Client, op, path, key string, members []member) (any, error) {
	var rows []object
	if err := c.quotaAdminGet(ctx, op, path, &rows); err != nil {
		return nil, err
	}
	more := len(rows) > maxQuotaAdminRows
	if more {
		rows = rows[:maxQuotaAdminRows]
	}
	kept := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			kept = append(kept, project(forgejo, members, row))
		}
	}
	return map[string]any{key: kept, "has_more": more}, nil
}

// doneOutput is the output of a change: the members of the object it names and a flag that tells what happened.
func doneOutput(properties string, required ...string) func(*product) json.RawMessage {
	return func(*product) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":["` +
			strings.Join(required, `","`) + `"],"additionalProperties":false}`)
	}
}

const (
	textMember = `{"type":"string"}`
	flagMember = `{"type":"boolean"}`
)

var quotaAdminGroupsList = quotaAdminSpec{
	group: "admin-quota-groups", action: "list", title: "List the Forgejo quota groups",
	text: "List the quota groups of the instance with their rules, at most 200." + adminOnly,
	risk: quotaReadRisk, output: cappedOutput("groups", func(p *product) string { return schemaOf(p, quotaGroupMembers) }),
	fields:  cappedFields("groups", "Quota groups with their rules"),
	example: `{}`, exampleText: "List the quota groups",
	prepare: func(p *product, _ quotaAdminCall) (run, error) {
		return func(ctx context.Context, c *Client) (any, error) {
			return cappedList(ctx, c, "list quota groups", quotaAdminGroups, "groups", quotaGroupMembers)
		}, nil
	},
}.tool()

var quotaAdminGroupsGet = quotaAdminSpec{
	group: "admin-quota-groups", action: "get", title: "Get a Forgejo quota group",
	text: "Read one quota group with its rules." + adminOnly,
	risk: quotaReadRisk, properties: `"group":` + quotaAdminNameSchema, required: []string{"group"},
	arguments: []capability.Argument{quotaAdminGroupArgument},
	output:    memberOutput(quotaGroupMembers), fields: memberFields(quotaGroupMembers),
	example: `{"group":"standard"}`, exampleText: "Read a quota group",
	prepare: func(p *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("group", a.Group); err != nil {
			return nil, err
		}
		path := quotaAdminGroups + "/" + escape(a.Group)
		return func(ctx context.Context, c *Client) (any, error) {
			var group object
			if err := c.quotaAdminGet(ctx, "get quota group", path, &group); err != nil {
				return nil, err
			}
			return project(p, quotaGroupMembers, group), nil
		}, nil
	},
}.tool()

var quotaAdminGroupsCreate = quotaAdminSpec{
	group: "admin-quota-groups", action: "create", title: "Create a Forgejo quota group",
	text: "Create one empty quota group; rules are added with admin-quota-groups.add-rule. The instance refuses a " +
		"name that exists." + sentOnce + adminOnly,
	risk: quotaChangeRisk(capability.EffectCreate), properties: `"name":` + quotaAdminNameSchema,
	required: []string{"name"},
	arguments: []capability.Argument{{Name: "name", Required: true, Description: "Name of the new group, 1 to 255 " +
		"characters without control characters and \"/\""}},
	output: doneOutput(`"name":`+textMember+`,"created":`+flagMember, "name", "created"),
	fields: staticFields(capability.Field{Name: "name", Description: "Name of the group"},
		capability.Field{Name: "created", Description: "True once the instance created the group"}),
	example: `{"name":"standard"}`, exampleText: "Create a quota group",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("name", a.Name); err != nil {
			return nil, err
		}
		body := map[string]any{"name": a.Name}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "create quota group", http.MethodPost, quotaAdminGroups, body); err != nil {
				return nil, err
			}
			return map[string]any{"name": a.Name, "created": true}, nil
		}, nil
	},
}.tool()

var quotaAdminGroupsDelete = quotaAdminSpec{
	group: "admin-quota-groups", action: "delete", title: "Delete a Forgejo quota group",
	text: "Delete one quota group for good; its users lose the limits it gave them." + sentOnce + listedBy + adminOnly,
	risk: quotaChangeRisk(capability.EffectDelete), listedOnly: true, properties: `"group":` + quotaAdminNameSchema,
	required: []string{"group"}, arguments: []capability.Argument{quotaAdminGroupArgument},
	output: doneOutput(`"group":`+textMember+`,"deleted":`+flagMember, "group", "deleted"),
	fields: staticFields(capability.Field{Name: "group", Description: "Name of the group"},
		capability.Field{Name: "deleted", Description: "True once the instance deleted the group"}),
	example: `{"group":"standard"}`, exampleText: "Delete a quota group",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("group", a.Group); err != nil {
			return nil, err
		}
		path := quotaAdminGroups + "/" + escape(a.Group)
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "delete quota group", http.MethodDelete, path, nil); err != nil {
				return nil, err
			}
			return map[string]any{"group": a.Group, "deleted": true}, nil
		}, nil
	},
}.tool()

// groupRule is the change of the membership of one rule in one group.
func groupRule(action, verb, method, done string, doneText string) *tool {
	return quotaAdminSpec{
		group: "admin-quota-groups", action: action, title: verb + " a rule of a Forgejo quota group",
		text: verb + " one quota rule of one quota group; the users of the group are subject to the rule " +
			"accordingly." + sentOnce + adminOnly,
		risk: quotaChangeRisk(capability.EffectUpdate), properties: `"group":` + quotaAdminNameSchema +
			`,"rule":` + quotaAdminNameSchema,
		required:  []string{"group", "rule"},
		arguments: []capability.Argument{quotaAdminGroupArgument, quotaAdminRuleArgument},
		output:    doneOutput(`"group":`+textMember+`,"rule":`+textMember+`,"`+done+`":`+flagMember, "group", "rule", done),
		fields: staticFields(capability.Field{Name: "group", Description: "Name of the group"},
			capability.Field{Name: "rule", Description: "Name of the rule"},
			capability.Field{Name: done, Description: doneText}),
		example: `{"group":"standard","rule":"repos-1gb"}`, exampleText: verb + " a rule",
		prepare: func(_ *product, a quotaAdminCall) (run, error) {
			if err := checkQuotaAdminName("group", a.Group); err != nil {
				return nil, err
			}
			if err := checkQuotaAdminName("rule", a.Rule); err != nil {
				return nil, err
			}
			path := quotaAdminGroups + "/" + escape(a.Group) + "/rules/" + escape(a.Rule)
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.quotaAdminChange(ctx, action+" quota group rule", method, path, nil); err != nil {
					return nil, err
				}
				return map[string]any{"group": a.Group, "rule": a.Rule, done: true}, nil
			}, nil
		},
	}.tool()
}

var (
	quotaAdminGroupsAddRule = groupRule("add-rule", "Add", http.MethodPut, "added",
		"True once the instance added the rule to the group")
	quotaAdminGroupsRemoveRule = groupRule("remove-rule", "Remove", http.MethodDelete, "removed",
		"True once the instance removed the rule from the group")
)

var quotaAdminGroupUsersList = quotaAdminSpec{
	group: "admin-quota-group-users", action: "list", title: "List the users of a Forgejo quota group",
	text: "List the users of one quota group with the person members of each user object, at most 200. Names and " +
		"texts are untrusted personal data." + adminOnly,
	risk:       func(p *product) capability.Risk { return readRisk(p.id + userSensitivity) },
	properties: `"group":` + quotaAdminNameSchema, required: []string{"group"},
	arguments: []capability.Argument{quotaAdminGroupArgument},
	output:    cappedOutput("users", func(p *product) string { return schemaOf(p, personMembers) }),
	fields:    cappedFields("users", "Users of the group with the person members of the user object"),
	example:   `{"group":"standard"}`, exampleText: "List the users of a quota group",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("group", a.Group); err != nil {
			return nil, err
		}
		path := quotaAdminGroups + "/" + escape(a.Group) + "/users"
		return func(ctx context.Context, c *Client) (any, error) {
			return cappedList(ctx, c, "list quota group users", path, "users", personMembers)
		}, nil
	},
}.tool()

// groupUser is the change of the membership of one user in one group.
func groupUser(action, verb, method, done string, doneText string) *tool {
	return quotaAdminSpec{
		group: "admin-quota-group-users", action: action, title: verb + " a user of a Forgejo quota group",
		text: verb + " one user of one quota group; the user is subject to the rules of the group accordingly." +
			sentOnce + adminOnly,
		risk: quotaChangeRisk(capability.EffectUpdate), properties: `"group":` + quotaAdminNameSchema +
			`,"username":` + issueSchemaLogin,
		required:  []string{"group", "username"},
		arguments: []capability.Argument{quotaAdminGroupArgument, quotaAdminUserArgument},
		output: doneOutput(`"group":`+textMember+`,"username":`+textMember+`,"`+done+`":`+flagMember, "group",
			"username", done),
		fields: staticFields(capability.Field{Name: "group", Description: "Name of the group"},
			capability.Field{Name: "username", Description: "User name"},
			capability.Field{Name: done, Description: doneText}),
		example: `{"group":"standard","username":"octo"}`, exampleText: verb + " a user",
		prepare: func(_ *product, a quotaAdminCall) (run, error) {
			if err := checkQuotaAdminName("group", a.Group); err != nil {
				return nil, err
			}
			if err := userNameOf("username", a.Username); err != nil {
				return nil, err
			}
			path := quotaAdminGroups + "/" + escape(a.Group) + "/users/" + escape(a.Username)
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.quotaAdminChange(ctx, action+" quota group user", method, path, nil); err != nil {
					return nil, err
				}
				return map[string]any{"group": a.Group, "username": a.Username, done: true}, nil
			}, nil
		},
	}.tool()
}

var (
	quotaAdminGroupUsersAdd = groupUser("add", "Add", http.MethodPut, "added",
		"True once the instance added the user to the group")
	quotaAdminGroupUsersRemove = groupUser("remove", "Remove", http.MethodDelete, "removed",
		"True once the instance removed the user from the group")
)

var quotaAdminRulesList = quotaAdminSpec{
	group: "admin-quota-rules", action: "list", title: "List the Forgejo quota rules",
	text: "List the quota rules of the instance, at most 200." + adminOnly,
	risk: quotaReadRisk, output: cappedOutput("rules", func(p *product) string { return schemaOf(p, quotaRuleMembers) }),
	fields:  cappedFields("rules", "Quota rules with name, limit, and subjects"),
	example: `{}`, exampleText: "List the quota rules",
	prepare: func(_ *product, _ quotaAdminCall) (run, error) {
		return func(ctx context.Context, c *Client) (any, error) {
			return cappedList(ctx, c, "list quota rules", quotaAdminRules, "rules", quotaRuleMembers)
		}, nil
	},
}.tool()

var quotaAdminRulesGet = quotaAdminSpec{
	group: "admin-quota-rules", action: "get", title: "Get a Forgejo quota rule",
	text: "Read one quota rule." + adminOnly,
	risk: quotaReadRisk, properties: `"rule":` + quotaAdminNameSchema, required: []string{"rule"},
	arguments: []capability.Argument{quotaAdminRuleArgument},
	output:    memberOutput(quotaRuleMembers), fields: memberFields(quotaRuleMembers),
	example: `{"rule":"repos-1gb"}`, exampleText: "Read a quota rule",
	prepare: func(p *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("rule", a.Rule); err != nil {
			return nil, err
		}
		path := quotaAdminRules + "/" + escape(a.Rule)
		return func(ctx context.Context, c *Client) (any, error) {
			var rule object
			if err := c.quotaAdminGet(ctx, "get quota rule", path, &rule); err != nil {
				return nil, err
			}
			return project(p, quotaRuleMembers, rule), nil
		}, nil
	},
}.tool()

var quotaAdminRulesCreate = quotaAdminSpec{
	group: "admin-quota-rules", action: "create", title: "Create a Forgejo quota rule",
	text: "Create one quota rule with a limit and the subjects it limits. The instance refuses a name that exists." +
		sentOnce + adminOnly,
	risk: quotaChangeRisk(capability.EffectCreate),
	properties: `"name":` + quotaAdminNameSchema + `,"limit":` + quotaLimitSchema + `,"subjects":` +
		quotaSubjectsSchema(),
	required: []string{"name", "limit", "subjects"},
	arguments: []capability.Argument{{Name: "name", Required: true, Description: "Name of the new rule, 1 to 255 " +
		"characters without control characters and \"/\""}, {Name: "limit", Required: true,
		Description: quotaAdminLimitArgument.Description}, quotaAdminSubjectsArgument(true)},
	output: doneOutput(`"name":`+textMember+`,"created":`+flagMember, "name", "created"),
	fields: staticFields(capability.Field{Name: "name", Description: "Name of the rule"},
		capability.Field{Name: "created", Description: "True once the instance created the rule"}),
	example: `{"name":"repos-1gb","limit":1073741824,"subjects":["size:repos:all"]}`, exampleText: "Create a quota rule",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("name", a.Name); err != nil {
			return nil, err
		}
		if a.Limit == nil {
			return nil, invalidArgument("limit is required")
		}
		if err := checkQuotaAdminLimit(*a.Limit); err != nil {
			return nil, err
		}
		if err := checkQuotaAdminSubjects(a.Subjects); err != nil {
			return nil, err
		}
		body := map[string]any{"name": a.Name, "limit": *a.Limit, "subjects": a.Subjects}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "create quota rule", http.MethodPost, quotaAdminRules, body); err != nil {
				return nil, err
			}
			return map[string]any{"name": a.Name, "created": true}, nil
		}, nil
	},
}.tool()

var quotaAdminRulesUpdate = quotaAdminSpec{
	group: "admin-quota-rules", action: "update", title: "Update a Forgejo quota rule",
	text: "Change the limit or the subjects of one quota rule; what is left out stays. The change reaches every " +
		"group that holds the rule." + sentOnce + adminOnly,
	risk: quotaChangeRisk(capability.EffectUpdate),
	properties: `"rule":` + quotaAdminNameSchema + `,"limit":` + quotaLimitSchema + `,"subjects":` +
		quotaSubjectsSchema(),
	required: []string{"rule"},
	arguments: []capability.Argument{quotaAdminRuleArgument, quotaAdminLimitArgument,
		quotaAdminSubjectsArgument(false)},
	output: doneOutput(`"rule":`+textMember+`,"updated":`+flagMember, "rule", "updated"),
	fields: staticFields(capability.Field{Name: "rule", Description: "Name of the rule"},
		capability.Field{Name: "updated", Description: "True once the instance updated the rule"}),
	example: `{"rule":"repos-1gb","limit":2147483648}`, exampleText: "Raise the limit of a quota rule",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("rule", a.Rule); err != nil {
			return nil, err
		}
		body := map[string]any{}
		if a.Limit != nil {
			if err := checkQuotaAdminLimit(*a.Limit); err != nil {
				return nil, err
			}
			body["limit"] = *a.Limit
		}
		if a.Subjects != nil {
			if err := checkQuotaAdminSubjects(a.Subjects); err != nil {
				return nil, err
			}
			body["subjects"] = a.Subjects
		}
		if len(body) == 0 {
			return nil, invalidArgument("pass limit, subjects, or both")
		}
		path := quotaAdminRules + "/" + escape(a.Rule)
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "update quota rule", http.MethodPatch, path, body); err != nil {
				return nil, err
			}
			return map[string]any{"rule": a.Rule, "updated": true}, nil
		}, nil
	},
}.tool()

var quotaAdminRulesDelete = quotaAdminSpec{
	group: "admin-quota-rules", action: "delete", title: "Delete a Forgejo quota rule",
	text: "Delete one quota rule for good; every group that holds it loses it." + sentOnce + listedBy + adminOnly,
	risk: quotaChangeRisk(capability.EffectDelete), listedOnly: true, properties: `"rule":` + quotaAdminNameSchema,
	required: []string{"rule"}, arguments: []capability.Argument{quotaAdminRuleArgument},
	output: doneOutput(`"rule":`+textMember+`,"deleted":`+flagMember, "rule", "deleted"),
	fields: staticFields(capability.Field{Name: "rule", Description: "Name of the rule"},
		capability.Field{Name: "deleted", Description: "True once the instance deleted the rule"}),
	example: `{"rule":"repos-1gb"}`, exampleText: "Delete a quota rule",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := checkQuotaAdminName("rule", a.Rule); err != nil {
			return nil, err
		}
		path := quotaAdminRules + "/" + escape(a.Rule)
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "delete quota rule", http.MethodDelete, path, nil); err != nil {
				return nil, err
			}
			return map[string]any{"rule": a.Rule, "deleted": true}, nil
		}, nil
	},
}.tool()

var quotaAdminUserQuotaGet = quotaAdminSpec{
	group: "admin-user-quota", action: "get", title: "Get the Forgejo quota of a user",
	text: "Read the quota groups with their rules and the storage in use of one user." + adminOnly,
	risk: quotaReadRisk, properties: `"username":` + issueSchemaLogin, required: []string{"username"},
	arguments: []capability.Argument{quotaAdminUserArgument},
	output:    memberOutput(quotaInfoMembers), fields: memberFields(quotaInfoMembers),
	example: `{"username":"octo"}`, exampleText: "Read the quota of a user",
	prepare: func(p *product, a quotaAdminCall) (run, error) {
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		path := quotaAdminUsers + "/" + escape(a.Username) + "/quota"
		return func(ctx context.Context, c *Client) (any, error) {
			var info object
			if err := c.quotaAdminGet(ctx, "get user quota", path, &info); err != nil {
				return nil, err
			}
			return project(p, quotaInfoMembers, info), nil
		}, nil
	},
}.tool()

var quotaAdminUserQuotaSetGroups = quotaAdminSpec{
	group: "admin-user-quota", action: "set-groups", title: "Set the Forgejo quota groups of a user",
	text: "Replace all quota groups of one user with the given ones; an empty list removes the user from every " +
		"group." + sentOnce + adminOnly,
	risk: quotaChangeRisk(capability.EffectUpdate),
	properties: `"username":` + issueSchemaLogin + `,"groups":{"type":"array","maxItems":100,"items":` +
		quotaAdminNameSchema + `}`,
	required: []string{"username", "groups"},
	arguments: []capability.Argument{quotaAdminUserArgument, {Name: "groups", Required: true,
		Description: "Names of the quota groups the user shall have, at most 100 and distinct; empty for none"}},
	output: doneOutput(`"username":`+textMember+`,"groups":{"type":"array","items":`+textMember+`},"updated":`+
		flagMember, "username", "groups", "updated"),
	fields: staticFields(capability.Field{Name: "username", Description: "User name"},
		capability.Field{Name: "groups", Description: "Quota groups the user was set to"},
		capability.Field{Name: "updated", Description: "True once the instance set the groups"}),
	example: `{"username":"octo","groups":["standard"]}`, exampleText: "Set the quota group of a user",
	prepare: func(_ *product, a quotaAdminCall) (run, error) {
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		if a.Groups == nil || len(a.Groups) > maxUserQuotaGroups {
			return nil, invalidArgument("groups must be a list of at most 100 quota group names")
		}
		for i, group := range a.Groups {
			if err := checkQuotaAdminName("groups", group); err != nil {
				return nil, err
			}
			if slices.Contains(a.Groups[:i], group) {
				return nil, invalidArgument("groups must be distinct")
			}
		}
		body := map[string]any{"groups": a.Groups}
		path := quotaAdminUsers + "/" + escape(a.Username) + "/quota/groups"
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.quotaAdminChange(ctx, "set user quota groups", http.MethodPost, path, body); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "groups": a.Groups, "updated": true}, nil
		}, nil
	},
}.tool()
