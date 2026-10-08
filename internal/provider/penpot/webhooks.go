package penpot

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxWebhooksListed = 100

const (
	mtypeJSON    = "application/json"
	mtypeTransit = "application/transit+json"
)

const (
	mtypeSchema = `{"type":"string","enum":["` + mtypeJSON + `","` + mtypeTransit + `"]}`
)

const webhookNote = "Webhooks belong to the whole team, so a connection with a project allow-list refuses every change. " +
	"The URL is never returned, only its host. The change is sent once and never repeated"

var webhookIDArgument = capability.Argument{Name: "webhook_id", Required: true,
	Description: "Webhook identifier from penpot.webhooks.list of the same team"}

var webhookMtypeArgument = capability.Argument{Name: "mtype", Required: true,
	Description: "Payload type, application/json or application/transit+json"}

var webhooksList = capability.Descriptor{
	ID: Provider + ".webhooks.list", Version: 1, Title: "List Penpot webhooks",
	Description: "List the webhooks of one bound team; the target URL is reduced to its host, which is untrusted provider data",
	Tags:        []string{"penpot", "webhooks", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: schemaOf(`"team_id":`+uuidSchema, `"team_id"`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"},"webhooks":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"host":{"type":"string"},"mtype":{"type":"string"},` +
		`"is_active":{"type":"boolean"},"error_code":{"type":"string"},"error_count":{"type":"integer"}},` +
		`"required":["id","mtype","is_active"],"additionalProperties":false}},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["team_id","webhooks","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Identifier of the team"},
		{Name: "webhooks", Description: "Webhooks with id (used as webhook_id), host of the target URL, mtype, is_active, error_code, and error_count"},
		{Name: "count", Description: "Number of webhooks in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 100 webhooks"},
	},
	Examples: []capability.Example{{Description: "List the webhooks of a team",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001"}`)}},
}

var webhooksUpdate = capability.Descriptor{
	ID: Provider + ".webhooks.update", Version: 2, Title: "Update a Penpot webhook",
	Description: "Replace the payload type and active state of one webhook of a bound team; the target URL is read " +
		"from the webhook list of that team and sent unchanged, and no argument sets it. Penpot resets the error counters. " + webhookNote,
	Tags: []string{"penpot", "webhooks", "update", "design"}, Provider: Provider,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"team_id":`+uuidSchema+`,"webhook_id":`+uuidSchema+`,"mtype":`+mtypeSchema+
		`,"is_active":{"type":"boolean"}`, `"team_id","webhook_id","mtype","is_active"`),
	OutputSchema: schemaOf(`"updated":{"type":"boolean"},"webhook_id":{"type":"string"},"team_id":{"type":"string"}`,
		`"updated","webhook_id","team_id"`),
	Arguments: []capability.Argument{teamIDArgument, webhookIDArgument, webhookMtypeArgument,
		{Name: "is_active", Required: true, Description: "Whether Penpot sends events to the webhook"}},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Penpot accepted the change"},
		{Name: "webhook_id", Description: "Identifier of the webhook"},
		{Name: "team_id", Description: "Identifier of the team"},
	},
	Examples: []capability.Example{{Description: "Pause a webhook",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","webhook_id":"00000000-0000-0000-0000-000000000002","mtype":"application/json","is_active":false}`)}},
}

var webhooksDelete = capability.Descriptor{
	ID: Provider + ".webhooks.delete", Version: 1, Title: "Delete a Penpot webhook",
	Description: "Delete one webhook of a bound team for good; the webhook is bound through the webhook list of that team. " +
		webhookNote,
	Tags: []string{"penpot", "webhooks", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:         manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema:  schemaOf(`"team_id":`+uuidSchema+`,"webhook_id":`+uuidSchema, `"team_id","webhook_id"`),
	OutputSchema: schemaOf(`"deleted":{"type":"boolean"},"webhook_id":{"type":"string"},"team_id":{"type":"string"}`, `"deleted","webhook_id","team_id"`),
	Arguments:    []capability.Argument{teamIDArgument, webhookIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Penpot accepted the deletion"},
		{Name: "webhook_id", Description: "Identifier of the webhook"},
		{Name: "team_id", Description: "Identifier of the team"},
	},
	Examples: []capability.Example{{Description: "Delete a webhook",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","webhook_id":"00000000-0000-0000-0000-000000000002"}`)}},
}

// Webhook is one webhook of a bound team. The target URL is reduced to its host.
type Webhook struct {
	ID         string `json:"id"`
	Host       string `json:"host,omitempty"`
	Mtype      string `json:"mtype"`
	IsActive   bool   `json:"is_active"`
	ErrorCode  string `json:"error_code,omitempty"`
	ErrorCount int64  `json:"error_count,omitempty"`
}

// WebhooksResult is the answer of webhooks.list.
type WebhooksResult struct {
	TeamID    string    `json:"team_id"`
	Webhooks  []Webhook `json:"webhooks"`
	Count     int       `json:"count"`
	Truncated bool      `json:"truncated"`
}

// WebhookUpdated and WebhookDeleted are the answers of the two changes; IDs only.
type WebhookUpdated struct {
	Updated   bool   `json:"updated"`
	WebhookID string `json:"webhook_id"`
	TeamID    string `json:"team_id"`
}

type WebhookDeleted struct {
	Deleted   bool   `json:"deleted"`
	WebhookID string `json:"webhook_id"`
	TeamID    string `json:"team_id"`
}

type webhookArguments struct {
	TeamID    string `json:"team_id"`
	WebhookID string `json:"webhook_id"`
	Mtype     string `json:"mtype"`
	IsActive  *bool  `json:"is_active"`
}

func readWebhook(op string, raw json.RawMessage) (webhookArguments, error) {
	var input webhookArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// selectTeamForWebhookChange is selectTeam for a change: webhooks are team-wide, so a connection narrowed by a
// project allow-list refuses it. The refusal comes before any secret.
func selectTeamForWebhookChange(resolved *config.Resolved, raw string) (string, error) {
	id, err := selectTeam(resolved, raw)
	if err != nil {
		return "", err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return "", err
	}
	if len(bound.projects) > 0 {
		return "", invalidRequest("a connection with a project allow-list cannot change webhooks, which belong to the whole team")
	}
	return id, nil
}

func checkMtype(mtype string) error {
	if mtype != mtypeJSON && mtype != mtypeTransit {
		return invalidRequest("mtype must be application/json or application/transit+json")
	}
	return nil
}

// webhookHost reduces a target URL to its host name; anything unparsable reads as empty.
func webhookHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return boundString(strings.ToLower(parsed.Hostname()), maxStringLength)
}

func (c *Client) teamWebhooks(ctx context.Context, op, teamID string) ([]Webhook, bool, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdWebhooks, map[string]any{"team-id": teamID}, &raw); err != nil {
		return nil, false, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, false, invalidResponse(op, "Penpot returned an invalid response")
	}
	hooks, truncated := []Webhook{}, false
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if len(hooks) >= maxWebhooksListed {
			truncated = true
			break
		}
		hooks = append(hooks, Webhook{ID: id, Host: webhookHost(entry.str("uri")), Mtype: entry.str("mtype"),
			IsActive: entry.boolean("isactive"), ErrorCode: entry.str("errorcode"), ErrorCount: entry.integer("errorcount")})
	}
	return hooks, truncated, nil
}

// locateWebhook proves that the webhook lies in the given bound team and returns its stored target URL, which
// never leaves the client.
func (c *Client) locateWebhook(ctx context.Context, op, teamID, webhookID string) (string, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdWebhooks, map[string]any{"team-id": teamID}, &raw); err != nil {
		return "", err
	}
	entries, ok := objects(raw)
	if !ok {
		return "", invalidResponse(op, "Penpot returned an invalid response")
	}
	for i, entry := range entries {
		if i >= maxWebhooksListed {
			break
		}
		if entry.id("id") == webhookID {
			return entry.str("uri"), nil
		}
	}
	return "", invalidRequest("webhook_id is outside the targets of this connection")
}

func invokeWebhooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list webhooks"
	input, err := readWebhook(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, err := selectTeam(resolved, input.TeamID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	hooks, truncated, err := client.teamWebhooks(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	return &WebhooksResult{TeamID: teamID, Webhooks: hooks, Count: len(hooks), Truncated: truncated}, nil
}

func invokeWebhooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update webhook"
	input, err := readWebhook(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, webhookID, err := webhookTargets(resolved, input)
	if err != nil {
		return nil, err
	}
	if err := checkMtype(input.Mtype); err != nil {
		return nil, err
	}
	if input.IsActive == nil {
		return nil, invalidRequest("is_active is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	target, err := client.locateWebhook(ctx, op, teamID, webhookID)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, invalidResponse(op, "Penpot listed the webhook without its target URL")
	}
	params := map[string]any{"id": webhookID, "uri": target, "mtype": input.Mtype, "is-active": *input.IsActive}
	if _, err := client.change(ctx, op, cmdUpdateWebhook, params); err != nil {
		return nil, err
	}
	return &WebhookUpdated{Updated: true, WebhookID: webhookID, TeamID: teamID}, nil
}

func invokeWebhooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete webhook"
	input, err := readWebhook(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, webhookID, err := webhookTargets(resolved, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.locateWebhook(ctx, op, teamID, webhookID); err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdDeleteWebhook, map[string]any{"id": webhookID}); err != nil {
		return nil, err
	}
	return &WebhookDeleted{Deleted: true, WebhookID: webhookID, TeamID: teamID}, nil
}

// webhookTargets checks team and webhook ID of a change locally, before any secret is resolved.
func webhookTargets(resolved *config.Resolved, input webhookArguments) (string, string, error) {
	teamID, err := selectTeamForWebhookChange(resolved, input.TeamID)
	if err != nil {
		return "", "", err
	}
	webhookID, ok := parseUUID(input.WebhookID)
	if !ok {
		return "", "", invalidRequest("webhook_id must be a UUID")
	}
	return teamID, webhookID, nil
}
