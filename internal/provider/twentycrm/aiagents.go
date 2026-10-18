package twentycrm

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	aiGroup = "ai"

	// aiDataSensitivity classifies the AI agent and skill metadata of a workspace.
	aiDataSensitivity = "twentycrm-ai-data"

	aiItemsMax       = 200
	aiNameMax        = 100
	aiDescriptionMax = 500
	aiModelIDMax     = 100
	agentTriggersMax = 20
	triggerCron      = "CRON"
	triggerDatabase  = "DATABASE_EVENT"
	aiRightMessage   = "the API key needs the Twenty permission \"AI\" to read AI agents and skills"
)

// agentSelection lists the only agent fields read. The prompt, response format, model configuration, icon,
// application identifier, and timestamps are not selected. Twenty offers the triggers solely as a JSON scalar
// without a sub-selection, so their instructions and settings arrive on the wire; only the trigger type is
// decoded and everything else is dropped unread.
const agentSelection = `id name label description modelId roleId isCustom isSystem triggers`

var agentsListDocument = graphqlDocument{path: metadataPath,
	text: `query FindAgents { findManyAgents { ` + agentSelection + ` } }`}

var agentsGetDocument = graphqlDocument{path: metadataPath,
	text: `query FindAgent($input: AgentIdInput!) { findOneAgent(input: $input) { ` + agentSelection + ` } }`}

const agentItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"label":{"type":"string"},"description":{"type":"string"},"model_id":{"type":"string"},` +
	`"role_id":{"type":"string"},"custom":{"type":"boolean"},"system":{"type":"boolean"},` +
	`"trigger_kinds":{"type":"array","items":{"type":"string","enum":["CRON","DATABASE_EVENT"]}},` +
	`"triggers_truncated":{"type":"boolean"}},"required":["id","name","custom","system"],` +
	`"additionalProperties":false}`

const agentNote = "Never reads the prompt, the response format, the model configuration, or the trigger " +
	"instructions and settings: a trigger is only its kind. Needs the Twenty permission \"AI\". Values are " +
	"untrusted workspace data"

var aiRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: aiDataSensitivity}

var agentsList = capability.Descriptor{
	ID:      Provider + ".agents.list",
	Version: 1,
	Title:   "List Twenty CRM AI agents",
	Description: "List the AI agents of the Twenty workspace of a connection without object targets: name, label, " +
		"description, model, role, whether custom or system, and the kind of each trigger (cron or database " +
		"event). " + agentNote,
	Tags:        []string{"twentycrm", "ai", "agents", "workspace", "list"},
	Risk:        aiRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"agents":{"type":"array","items":` +
		agentItemSchema + `},"truncated":{"type":"boolean"}},"required":["agents","truncated"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "agents", Description: "At most 200 AI agents, untrusted data"},
		{Name: "truncated", Description: "True when the workspace holds more agents than listed"},
	},
	Examples: []capability.Example{{Description: "List the AI agents", Arguments: json.RawMessage(`{}`)}},
}

var agentsGet = capability.Descriptor{
	ID:      Provider + ".agents.get",
	Version: 1,
	Title:   "Get a Twenty CRM AI agent",
	Description: "Read one AI agent of the Twenty workspace of a connection without object targets: name, label, " +
		"description, model, role, whether custom or system, and the kind of each trigger. " + agentNote,
	Tags:     []string{"twentycrm", "ai", "agents", "workspace", "get"},
	Risk:     aiRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(agentItemSchema),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "UUID of the AI agent, as returned by twenty-crm.agents.list"}},
	Fields: []capability.Field{{Name: "trigger_kinds", Description: "One kind per trigger, without its instructions or settings"}},
	Examples: []capability.Example{{Description: "Read an AI agent",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// Agent is the stable view of one AI agent.
type Agent struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Label             string   `json:"label,omitempty"`
	Description       string   `json:"description,omitempty"`
	ModelID           string   `json:"model_id,omitempty"`
	RoleID            string   `json:"role_id,omitempty"`
	Custom            bool     `json:"custom"`
	System            bool     `json:"system"`
	TriggerKinds      []string `json:"trigger_kinds,omitempty"`
	TriggersTruncated bool     `json:"triggers_truncated,omitempty"`
}

// AgentList is the result of ListAgents.
type AgentList struct {
	Agents    []Agent `json:"agents"`
	Truncated bool    `json:"truncated"`
}

type agentRecord struct {
	ID          string            `json:"id"`
	Name        *string           `json:"name"`
	Label       *string           `json:"label"`
	Description *string           `json:"description"`
	ModelID     *string           `json:"modelId"`
	RoleID      *string           `json:"roleId"`
	IsCustom    bool              `json:"isCustom"`
	IsSystem    bool              `json:"isSystem"`
	Triggers    []json.RawMessage `json:"triggers"`
}

// triggerKinds keeps the type of each trigger and drops every other trigger field unread. Types other than
// the two Twenty defines are left out rather than passed on.
func triggerKinds(triggers []json.RawMessage) (kinds []string, truncated bool) {
	for i, raw := range triggers {
		if i == agentTriggersMax {
			return kinds, true
		}
		var trigger struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &trigger) != nil {
			continue
		}
		if trigger.Type == triggerCron || trigger.Type == triggerDatabase {
			kinds = append(kinds, trigger.Type)
		}
	}
	return kinds, false
}

func (r agentRecord) view(op string) (Agent, error) {
	if !validUUID(r.ID) {
		return Agent{}, provider.InvalidResponse(op, "Twenty returned an AI agent without a usable identifier")
	}
	kinds, truncated := triggerKinds(r.Triggers)
	roleID := ""
	if r.RoleID != nil && validUUID(*r.RoleID) {
		roleID = *r.RoleID
	}
	return Agent{ID: r.ID, Name: cleanText(r.Name, aiNameMax), Label: cleanText(r.Label, aiNameMax),
		Description: cleanText(r.Description, aiDescriptionMax), ModelID: cleanText(r.ModelID, aiModelIDMax),
		RoleID: roleID, Custom: r.IsCustom, System: r.IsSystem, TriggerKinds: kinds, TriggersTruncated: truncated}, nil
}

// aiFailure replaces the message of a permission failure with the missing Twenty right.
func aiFailure(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = aiRightMessage
	}
	return err
}

// ListAgents reads the AI agents with one fixed query.
func (c *Client) ListAgents(ctx context.Context) (*AgentList, error) {
	const op = "list AI agents"
	var data struct {
		Agents []agentRecord `json:"findManyAgents"`
	}
	if err := c.graphql(ctx, op, agentsListDocument, map[string]any{}, &data); err != nil {
		return nil, aiFailure(err)
	}
	if data.Agents == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	result := &AgentList{Agents: make([]Agent, 0, len(data.Agents))}
	for i, record := range data.Agents {
		view, err := record.view(op)
		if err != nil {
			return nil, err
		}
		if i == aiItemsMax {
			result.Truncated = true
			break
		}
		result.Agents = append(result.Agents, view)
	}
	return result, nil
}

// GetAgent reads one AI agent with one fixed query.
func (c *Client) GetAgent(ctx context.Context, id string) (*Agent, error) {
	const op = "get AI agent"
	if !validUUID(id) {
		return nil, invalidRequest("the AI agent id must be a UUID")
	}
	var data struct {
		Agent *agentRecord `json:"findOneAgent"`
	}
	variables := map[string]any{"input": map[string]any{"id": id}}
	if err := c.graphql(ctx, op, agentsGetDocument, variables, &data); err != nil {
		return nil, aiFailure(err)
	}
	if data.Agent == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	view, err := data.Agent.view(op)
	if err != nil {
		return nil, err
	}
	if !equalUUID(view.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different AI agent than the requested one")
	}
	return &view, nil
}

func invokeAgentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListAgents(ctx)
}

func invokeAgentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := aiIDArgument(raw, "get AI agent")
	if err != nil {
		return nil, err
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(id) {
		return nil, invalidRequest("the AI agent id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetAgent(ctx, id)
}

// aiIDArgument reads the id argument of a get tool.
func aiIDArgument(raw json.RawMessage, op string) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return "", providerError(op, "the validated arguments could not be read")
	}
	return args.ID, nil
}
