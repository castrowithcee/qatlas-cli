package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// skillSelection lists the only skill fields read; the content, icon, application identifier, and
// timestamps are not selected.
const skillSelection = `id name label description isCustom isSystem isActive`

var skillsListDocument = graphqlDocument{path: metadataPath,
	text: `query FindSkills { skills { ` + skillSelection + ` } }`}

var skillsGetDocument = graphqlDocument{path: metadataPath,
	text: `query FindSkill($id: UUID!) { skill(id: $id) { ` + skillSelection + ` } }`}

// skillStatusTexts answers a skill that Twenty does not return.
var skillStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: aiRightMessage,
	NotFound:   "this Twenty workspace does not hold this skill",
}

const skillItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"label":{"type":"string"},"description":{"type":"string"},"custom":{"type":"boolean"},` +
	`"system":{"type":"boolean"},"active":{"type":"boolean"}},"required":["id","name","custom","system","active"],` +
	`"additionalProperties":false}`

const skillNote = "Never reads the skill content. Needs the Twenty permission \"AI\". Values are untrusted " +
	"workspace data"

var skillsList = capability.Descriptor{
	ID:      Provider + ".skills.list",
	Version: 1,
	Title:   "List Twenty CRM skills",
	Description: "List the skills of the Twenty workspace of a connection without object targets: name, label, " +
		"description, whether custom, system, and active. " + skillNote,
	Tags:        []string{"twentycrm", "ai", "skills", "workspace", "list"},
	Risk:        aiRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"skills":{"type":"array","items":` +
		skillItemSchema + `},"truncated":{"type":"boolean"}},"required":["skills","truncated"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "skills", Description: "At most 200 skills, untrusted data"},
		{Name: "truncated", Description: "True when the workspace holds more skills than listed"},
	},
	Examples: []capability.Example{{Description: "List the skills", Arguments: json.RawMessage(`{}`)}},
}

var skillsGet = capability.Descriptor{
	ID:      Provider + ".skills.get",
	Version: 1,
	Title:   "Get a Twenty CRM skill",
	Description: "Read one skill of the Twenty workspace of a connection without object targets: name, label, " +
		"description, whether custom, system, and active. " + skillNote,
	Tags:     []string{"twentycrm", "ai", "skills", "workspace", "get"},
	Risk:     aiRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(skillItemSchema),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "UUID of the skill, as returned by twenty-crm.skills.list"}},
	Examples: []capability.Example{{Description: "Read a skill",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// Skill is the stable view of one skill.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Custom      bool   `json:"custom"`
	System      bool   `json:"system"`
	Active      bool   `json:"active"`
}

// SkillList is the result of ListSkills.
type SkillList struct {
	Skills    []Skill `json:"skills"`
	Truncated bool    `json:"truncated"`
}

type skillRecord struct {
	ID          string  `json:"id"`
	Name        *string `json:"name"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
	IsCustom    bool    `json:"isCustom"`
	IsSystem    bool    `json:"isSystem"`
	IsActive    bool    `json:"isActive"`
}

func (r skillRecord) view(op string) (Skill, error) {
	if !validUUID(r.ID) {
		return Skill{}, provider.InvalidResponse(op, "Twenty returned a skill without a usable identifier")
	}
	return Skill{ID: r.ID, Name: cleanText(r.Name, aiNameMax), Label: cleanText(r.Label, aiNameMax),
		Description: cleanText(r.Description, aiDescriptionMax), Custom: r.IsCustom, System: r.IsSystem,
		Active: r.IsActive}, nil
}

// ListSkills reads the skills with one fixed query.
func (c *Client) ListSkills(ctx context.Context) (*SkillList, error) {
	const op = "list skills"
	var data struct {
		Skills []skillRecord `json:"skills"`
	}
	if err := c.graphql(ctx, op, skillsListDocument, map[string]any{}, &data); err != nil {
		return nil, aiFailure(err)
	}
	if data.Skills == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	result := &SkillList{Skills: make([]Skill, 0, len(data.Skills))}
	for i, record := range data.Skills {
		view, err := record.view(op)
		if err != nil {
			return nil, err
		}
		if i == aiItemsMax {
			result.Truncated = true
			break
		}
		result.Skills = append(result.Skills, view)
	}
	return result, nil
}

// GetSkill reads one skill with one fixed query.
func (c *Client) GetSkill(ctx context.Context, id string) (*Skill, error) {
	const op = "get skill"
	if !validUUID(id) {
		return nil, invalidRequest("the skill id must be a UUID")
	}
	var data struct {
		Skill json.RawMessage `json:"skill"`
	}
	if err := c.graphql(ctx, op, skillsGetDocument, map[string]any{"id": id}, &data); err != nil {
		return nil, aiFailure(err)
	}
	if data.Skill == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	// The query is nullable: an explicit null is an unknown skill, a missing key is an invalid answer.
	if bytes.Equal(bytes.TrimSpace(data.Skill), []byte("null")) {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, skillStatusTexts)
	}
	var record skillRecord
	if json.Unmarshal(data.Skill, &record) != nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	view, err := record.view(op)
	if err != nil {
		return nil, err
	}
	if !equalUUID(view.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different skill than the requested one")
	}
	return &view, nil
}

func invokeSkillsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListSkills(ctx)
}

func invokeSkillsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := aiIDArgument(raw, "get skill")
	if err != nil {
		return nil, err
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(id) {
		return nil, invalidRequest("the skill id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetSkill(ctx, id)
}
