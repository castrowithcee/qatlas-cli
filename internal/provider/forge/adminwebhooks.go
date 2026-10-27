package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The system webhook tools read and delete the webhooks the administrator set for the whole instance. They
// reach past any target, so only a connection without targets offers them. Nothing is created or changed:
// the instance resets the events, the branch filter, and the authorization header of a webhook that is
// changed without them. The projection is the one of the other webhook tools, so the secret, the
// authorization header, and every config key outside hookConfigMembers are never shown.

const adminHooksPath = "/admin/hooks"

var adminHookTypes = []string{"system", "default", "all"}

var adminHookIDArgument = capability.Argument{Name: "hook_id", Required: true,
	Description: "ID of the system webhook, as admin-webhooks.list reports it as id"}

type adminHookArguments struct {
	HookID int64  `json:"hook_id"`
	Type   string `json:"type"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// readAdminHook refuses targets, then decodes the arguments; both come before the token and any request.
func readAdminHook(allowed allowlist, raw json.RawMessage, withID bool) (adminHookArguments, error) {
	var a adminHookArguments
	if err := allowed.instanceWide(); err != nil {
		return a, err
	}
	if err := decodeArguments(raw, &a); err != nil {
		return a, err
	}
	if withID && a.HookID < 1 {
		return a, invalidArgument("hook_id must be the ID of a system webhook")
	}
	return a, nil
}

var adminWebhooksList = &tool{
	group: "admin-webhooks", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := pagingKeys
		arguments := append([]capability.Argument(nil), pagingArguments...)
		if p == gitea {
			properties = `"type":{"type":"string","enum":["system","default","all"]},` + pagingKeys
			arguments = append([]capability.Argument{{Name: "type", Description: "Gitea only: system lists the " +
				"system webhooks, default the default webhooks, all both; the instance decides when omitted"}}, arguments...)
		}
		return capability.Descriptor{
			Title: "List the system webhooks of " + p.name,
			Description: "List one batch of the system webhooks of the whole instance as an administrator, with every " +
				"member of each webhook. The target address, name, and branch filter are untrusted data and never " +
				"fetched. The secret, the authorization header, and secret config keys are never shown. Only a " +
				"connection without targets offers this tool.",
			Tags:         []string{p.id, "admin", "webhooks", "list"},
			Risk:         readRisk(p.id + accessSensitivity),
			InputSchema:  inputSchema(properties),
			OutputSchema: instanceListOutput("webhooks", schemaOf(p, webhookMembers)),
			Arguments:    arguments,
			Fields:       instanceListFields("webhooks", "System webhooks with every member of the webhook object"),
			Examples:     []capability.Example{{Description: "List a batch", Arguments: json.RawMessage(`{"limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, err := readAdminHook(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		query := url.Values{}
		if a.Type != "" {
			if p != gitea {
				return nil, invalidArgument("type is a Gitea argument; Forgejo lists its system webhooks only")
			}
			if !contains(adminHookTypes, a.Type) {
				return nil, invalidArgument("type must be system, default, or all")
			}
			query.Set("type", a.Type)
		}
		page, err := startPaging(provider.CursorBinding(p.id, "admin-webhooks.list", "", adminHooksPath, query.Encode()),
			a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return instancePage(ctx, c, "list system webhooks", adminHooksPath, query, "webhooks", "", page,
				func(row object) map[string]any { return project(p, webhookMembers, row) })
		}, nil
	},
}

var adminWebhooksGet = &tool{
	group: "admin-webhooks", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a system webhook of " + p.name,
			Description: "Read one system webhook of the whole instance by its ID as an administrator. The target " +
				"address is untrusted data and never fetched; secrets are never shown. Only a connection without " +
				"targets offers this tool.",
			Tags:         []string{p.id, "admin", "webhooks", "get"},
			Risk:         readRisk(p.id + accessSensitivity),
			InputSchema:  inputSchema(`"hook_id":`+hookSchemaID, "hook_id"),
			OutputSchema: json.RawMessage(schemaOf(p, webhookMembers)),
			Arguments:    []capability.Argument{adminHookIDArgument},
			Fields:       fieldsOf(p, webhookMembers),
			Examples:     []capability.Example{{Description: "Read a webhook", Arguments: json.RawMessage(`{"hook_id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, err := readAdminHook(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get system webhook"
			var row object
			if _, err := c.get(ctx, op, adminHooksPath+"/"+itoa64(a.HookID), nil, &row); err != nil {
				return nil, err
			}
			if err := requireHookAnswer(p, op, a.HookID, row); err != nil {
				return nil, err
			}
			return project(p, webhookMembers, row), nil
		}, nil
	},
}

var adminWebhooksDelete = &tool{
	group: "admin-webhooks", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a system webhook of " + p.name,
			Description: "Delete one system webhook of the whole instance by its ID as an administrator for good; " +
				"this cannot be undone and its deliveries stop. Idempotent; the request is sent once and never " +
				"repeated after an unclear outcome. Offered only by a connection without targets whose tools list " +
				"names it, in no profile.",
			Tags:                  []string{p.id, "admin", "webhooks", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"hook_id":`+hookSchemaID, "hook_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":{"type":"integer"},` +
				`"deleted":{"type":"boolean"}},"required":["hook_id","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{adminHookIDArgument},
			Fields: []capability.Field{{Name: "hook_id", Description: "ID of the webhook"},
				{Name: "deleted", Description: "True once the instance deleted the webhook"}},
			Examples: []capability.Example{{Description: "Delete a webhook", Arguments: json.RawMessage(`{"hook_id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, err := readAdminHook(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete system webhook", http.MethodDelete,
				adminHooksPath+"/"+itoa64(a.HookID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"hook_id": a.HookID, "deleted": true}, nil
		}, nil
	},
}
