package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The webhook tools read and delete existing webhooks at one of three levels: a repository, an organization,
// or the account behind the token. No webhook is created, tested, or changed, and system webhooks are not
// reached. A webhook ID is addressed inside the path of its owner, so the owner binding of the level is the
// proof; only the account level needs a read of the account first. The target address of a webhook is
// untrusted text that is never fetched; the secret, the authorization header, and every config key outside
// hookConfigMembers are never shown.

const (
	accessSensitivity = "-access-data"

	hookOfRepository   = "repository"
	hookOfOrganization = "organization"
	hookOfAccount      = "account"

	hookSchemaID = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
)

// hookConfigMembers is the allowlist of the config keys a webhook shows. The instance stores the secret and the
// authorization header outside config; any other key it may add later stays hidden until listed here.
var hookConfigMembers = []member{
	{name: "url", kind: kindText, address: true, note: "Target address, untrusted data, never fetched; left out " +
		"when it carries user info or a query"},
	{name: "content_type", kind: kindText, note: "json or form"},
	{name: "channel", kind: kindText, note: "Slack channel, untrusted data"},
	{name: "username", kind: kindText, note: "Slack user name, untrusted data"},
	{name: "icon_url", kind: kindText, address: true, note: "Slack icon address, untrusted data, never fetched"},
	{name: "color", kind: kindText, note: "Slack color"},
}

var webhookMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the webhook"},
	{name: "type", kind: kindText, note: "Kind of the webhook, such as gitea, forgejo, slack, or discord"},
	{name: "name", kind: kindText, only: gitea, note: "Name of the webhook, untrusted data"},
	{name: "active", kind: kindFlag, note: "True when the webhook delivers"},
	{name: "events", kind: kindTexts, note: "Events that trigger the webhook"},
	{name: "branch_filter", kind: kindText, note: "Glob of the branches that trigger it, untrusted data"},
	{name: "config", kind: kindObject, sub: hookConfigMembers, note: "Allowlisted config keys; secrets are never shown"},
	{name: "content_type", kind: kindText, only: forgejo, note: "json or form"},
	{name: "url", kind: kindText, only: forgejo, address: true, note: "Address of the webhook settings page, " +
		"untrusted data"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last change"},
}

// hookLevel is the owner a webhook tool acts on.
type hookLevel struct {
	kind string
	repo target
	org  string
}

func (l hookLevel) path() string {
	switch l.kind {
	case hookOfRepository:
		return repositoryPath(l.repo) + "/hooks"
	case hookOfOrganization:
		return orgPath(l.org) + "/hooks"
	}
	return "/user/hooks"
}

// label is the owner as the results show it; the account has none.
func (l hookLevel) label() string {
	switch l.kind {
	case hookOfRepository:
		return fullName(l.repo)
	case hookOfOrganization:
		return l.org
	}
	return ""
}

func (l hookLevel) result(extra map[string]any) map[string]any {
	extra["level"] = l.kind
	if label := l.label(); label != "" {
		extra["target"] = label
	}
	return extra
}

// prove reads the account behind the token before anything is read or changed there; the other levels carry
// their owner in the path.
func (l hookLevel) prove(ctx context.Context, c *Client, op string, allowed allowlist) error {
	if l.kind == hookOfAccount {
		return verifyOwnAccount(ctx, c, op, allowed)
	}
	return nil
}

// hookLevelOf reads the level from exactly one of repository, organization, or account. Without any, the one
// repository the targets name is the level.
func (a allowlist) hookLevelOf(repository, organization string, account *bool) (hookLevel, error) {
	given := 0
	for _, set := range []bool{repository != "", organization != "", account != nil} {
		if set {
			given++
		}
	}
	if account != nil && !*account {
		return hookLevel{}, invalidArgument("account takes true; leave it out for another level")
	}
	switch {
	case given > 1:
		return hookLevel{}, invalidArgument("pass only one of repository, organization, and account")
	case organization != "":
		name, err := a.organizationOf(organization)
		if err != nil {
			return hookLevel{}, err
		}
		return hookLevel{kind: hookOfOrganization, org: name}, nil
	case account != nil:
		if err := a.ownAccountOnly(); err != nil {
			return hookLevel{}, err
		}
		return hookLevel{kind: hookOfAccount}, nil
	}
	repo, err := a.chooseRepository(repository)
	if err != nil {
		return hookLevel{}, err
	}
	return hookLevel{kind: hookOfRepository, repo: repo}, nil
}

const hookLevelKeys = `"repository":` + repoSchema + `,"organization":` + organizationSchema +
	`,"account":{"type":"boolean","enum":[true]}`

var hookLevelArguments = []capability.Argument{
	{Name: "repository", Description: "Repository as OWNER/REPO for its webhooks; optional when the connection's " +
		"targets name exactly one repository and no other level is given; must lie inside the targets", Target: kindRepository},
	{Name: "organization", Description: "Organization name for its webhooks; the connection's targets must name " +
		"it as orgs/NAME or name no targets"},
	{Name: "account", Description: "True for the webhooks of the account behind the token; with targets, they " +
		"must name it as users/NAME and no repository, and a read of the account proves it first"},
}

var hookIDArgument = capability.Argument{Name: "hook_id", Required: true,
	Description: "ID of the webhook at that level, as webhooks.list reports it as id"}

var hookLevelFields = []capability.Field{
	{Name: "level", Description: "repository, organization, or account"},
	{Name: "target", Description: "Repository as OWNER/REPO or organization name; absent for the account"},
}

type hookArguments struct {
	Repository   string `json:"repository"`
	Organization string `json:"organization"`
	Account      *bool  `json:"account"`
	HookID       int64  `json:"hook_id"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor"`
}

func hookID(a hookArguments) error {
	if a.HookID < 1 {
		return invalidArgument("hook_id must be the ID of a webhook")
	}
	return nil
}

// requireHookAnswer proves that an answer is the webhook that was asked for.
func requireHookAnswer(p *product, op string, id int64, row object) error {
	if got, ok := row.integer("id"); !ok || got != id {
		return provider.InvalidResponse(op, p.name+" returned another webhook than the one asked for")
	}
	return nil
}

var webhooksList = &tool{
	group: "webhooks", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the webhooks of a " + p.name + " repository, organization, or account",
			Description: "List one batch of the existing webhooks of one repository, one organization, or the " +
				"account behind the token. Give exactly one of repository, organization, and account. The target " +
				"address, name, and branch filter are untrusted data and never fetched. The secret, the " +
				"authorization header, and secret config keys are never shown. No webhook is created, tested, " +
				"or changed.",
			Tags:        []string{p.id, "webhooks", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(hookLevelKeys + `,` + pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"level":{"type":"string"},"target":` +
				`{"type":"string"},"webhooks":{"type":"array","items":` + schemaOf(p, webhookMembers) + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["level","webhooks",` +
				`"has_more"],"additionalProperties":false}`),
			Arguments: append(append([]capability.Argument(nil), hookLevelArguments...), pagingArguments...),
			Fields: append(append(append([]capability.Field(nil), hookLevelFields...),
				capability.Field{Name: "webhooks", Description: "Webhooks with every member of the webhook object"}),
				pagingFields...),
			Examples: []capability.Example{{Description: "List the webhooks of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a hookArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		level, err := allowed.hookLevelOf(a.Repository, a.Organization, a.Account)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "webhooks.list", level.kind+"/"+strings.ToLower(level.label())),
			a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list webhooks"
			if err := level.prove(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var rows []object
			header, err := c.get(ctx, op, level.path(), page.query(), &rows)
			if err != nil {
				return nil, err
			}
			hooks := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					hooks = append(hooks, project(p, webhookMembers, row))
				}
			}
			result := pageResult(page, header, len(rows), "webhooks", hooks)
			return level.result(result), nil
		}, nil
	},
}

var webhooksGet = &tool{
	group: "webhooks", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a webhook of a " + p.name + " repository, organization, or account",
			Description: "Read one existing webhook by its ID at one repository, one organization, or the account " +
				"behind the token. Give exactly one of repository, organization, and account. The target address " +
				"is untrusted data and never fetched; secrets are never shown.",
			Tags:        []string{p.id, "webhooks", "get"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(hookLevelKeys+`,"hook_id":`+hookSchemaID, "hook_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"level":{"type":"string"},"target":` +
				`{"type":"string"},"webhook":` + schemaOf(p, webhookMembers) + `},"required":["level","webhook"],` +
				`"additionalProperties":false}`),
			Arguments: append(append([]capability.Argument(nil), hookLevelArguments...), hookIDArgument),
			Fields: append(append([]capability.Field(nil), hookLevelFields...),
				capability.Field{Name: "webhook", Description: "The webhook with every member of the webhook object"}),
			Examples: []capability.Example{{Description: "Read a webhook",
				Arguments: json.RawMessage(`{"repository":"example/project","hook_id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a hookArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		level, err := allowed.hookLevelOf(a.Repository, a.Organization, a.Account)
		if err != nil {
			return nil, err
		}
		if err := hookID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get webhook"
			if err := level.prove(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var row object
			if _, err := c.get(ctx, op, level.path()+"/"+itoa64(a.HookID), nil, &row); err != nil {
				return nil, err
			}
			if err := requireHookAnswer(p, op, a.HookID, row); err != nil {
				return nil, err
			}
			return level.result(map[string]any{"webhook": project(p, webhookMembers, row)}), nil
		}, nil
	},
}

var webhooksDelete = &tool{
	group: "webhooks", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a webhook of a " + p.name + " repository, organization, or account",
			Description: "Delete one existing webhook by its ID at one repository, one organization, or the " +
				"account behind the token for good; this cannot be undone and its deliveries stop. Give exactly " +
				"one of repository, organization, and account. Only a connection whose tools list names it " +
				"offers this tool. The change is the one request.",
			Tags:                  []string{p.id, "webhooks", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(hookLevelKeys+`,"hook_id":`+hookSchemaID, "hook_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"level":{"type":"string"},"target":` +
				`{"type":"string"},"hook_id":{"type":"integer"},"deleted":{"type":"boolean"}},` +
				`"required":["level","hook_id","deleted"],"additionalProperties":false}`),
			Arguments: append(append([]capability.Argument(nil), hookLevelArguments...), hookIDArgument),
			Fields: append(append([]capability.Field(nil), hookLevelFields...),
				capability.Field{Name: "hook_id", Description: "ID of the webhook"},
				capability.Field{Name: "deleted", Description: "True once the instance deleted the webhook"}),
			Examples: []capability.Example{{Description: "Delete a webhook",
				Arguments: json.RawMessage(`{"repository":"example/project","hook_id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a hookArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		level, err := allowed.hookLevelOf(a.Repository, a.Organization, a.Account)
		if err != nil {
			return nil, err
		}
		if err := hookID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete webhook"
			if err := level.prove(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, level.path()+"/"+itoa64(a.HookID), nil, nil); err != nil {
				return nil, err
			}
			return level.result(map[string]any{"hook_id": a.HookID, "deleted": true}), nil
		}, nil
	},
}
