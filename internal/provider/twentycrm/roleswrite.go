package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The fixed documents of the role write tools, sent to the metadata endpoint. Only checked values are variables.
// The pre-read selects only the flag and the identifiers of the API keys: never a key name.
var (
	roleGuardDocument = graphqlDocument{path: metadataPath, text: `query GetRole($id: UUID!) { getRole(id: $id) ` +
		`{ id isEditable apiKeys { id } } }`}
	roleCreateDocument = graphqlDocument{path: metadataPath, text: `mutation CreateOneRole(` +
		`$createRoleInput: CreateRoleInput!) { createOneRole(createRoleInput: $createRoleInput) { id label } }`}
	roleUpdateDocument = graphqlDocument{path: metadataPath, text: `mutation UpdateOneRole(` +
		`$updateRoleInput: UpdateRoleInput!) { updateOneRole(updateRoleInput: $updateRoleInput) { id label } }`}
	roleDeleteDocument = graphqlDocument{path: metadataPath, text: `mutation DeleteOneRole($roleId: UUID!) ` +
		`{ deleteOneRole(roleId: $roleId) }`}
)

const (
	roleUncertain = "; this change may have taken effect, read the roles with twentycrm.roles.list before " +
		"repeating it"

	errRoleID    = "id must be a role identifier in UUID form"
	errRoleLabel = "label must be 1 to 100 characters without control characters"
	errRoleDesc  = "description must be 1 to 500 characters without control characters"
	errRoleArgs  = "the arguments are not a valid role request"
	errRoleNone  = "at least one of label, description, or a permission flag must be given"
	errRoleKeys  = "this role is not changed through Qatlas: it is assigned to an API key or cannot be edited; " +
		"change it in Twenty"

	roleWriteNote = "Changing a role changes the rights of every member and API key assigned to it at once. " +
		"Needs the Twenty permission \"Roles\" and a connection without object targets, and is offered only by " +
		"a connection whose tools list names it. A role that is assigned to an API key or cannot be edited is " +
		"refused without a change. Sent once; after an unclear result read the roles before repeating"

	roleLabelSchema = `{"type":"string","minLength":1,"maxLength":100}`
	roleDescSchema  = `{"type":"string","minLength":1,"maxLength":500}`
	roleBoolSchema  = `{"type":"boolean"}`

	roleFlagProperties = `"description":` + roleDescSchema + `,"can_update_all_settings":` + roleBoolSchema +
		`,"can_access_all_tools":` + roleBoolSchema + `,"can_read_all_object_records":` + roleBoolSchema +
		`,"can_update_all_object_records":` + roleBoolSchema + `,"can_soft_delete_all_object_records":` + roleBoolSchema +
		`,"can_destroy_all_object_records":` + roleBoolSchema + `,"assignable_to_users":` + roleBoolSchema +
		`,"assignable_to_agents":` + roleBoolSchema + `,"assignable_to_api_keys":` + roleBoolSchema

	roleResultSchema = `{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"}},` +
		`"required":["id","label"],"additionalProperties":false}`
)

func roleWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: roleDataSensitivity}
}

var roleFlagArguments = []capability.Argument{
	{Name: "description", Description: "Description, 1 to 500 characters without control characters"},
	{Name: "can_update_all_settings", Description: "Role may change every setting"},
	{Name: "can_access_all_tools", Description: "Role may use every tool"},
	{Name: "can_read_all_object_records", Description: "Role may read the records of every object"},
	{Name: "can_update_all_object_records", Description: "Role may update the records of every object"},
	{Name: "can_soft_delete_all_object_records", Description: "Role may delete the records of every object"},
	{Name: "can_destroy_all_object_records", Description: "Role may destroy the records of every object"},
	{Name: "assignable_to_users", Description: "Role may be assigned to members"},
	{Name: "assignable_to_agents", Description: "Role may be assigned to Twenty agents"},
	{Name: "assignable_to_api_keys", Description: "Role may be assigned to API keys"},
}

var roleIDArgument = capability.Argument{Name: "id", Required: true,
	Description: "UUID of the role, as returned by twentycrm.roles.list"}

var rolesCreate = capability.Descriptor{
	ID: Provider + ".roles.create", Version: 1, Title: "Create a Twenty CRM role",
	Description: "Create one role in the Twenty workspace of a connection, with a label and optionally a " +
		"description and its global rights. Members and API keys that are later assigned to the role receive " +
		"exactly these rights; the role is assigned to nobody by this tool. " + roleWriteNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "create"},
	Risk:     roleWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"label":` + roleLabelSchema + `,` + roleFlagProperties +
		`},"required":["label"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(roleResultSchema),
	Arguments: append([]capability.Argument{{Name: "label", Required: true,
		Description: "Label, 1 to 100 characters without control characters"}}, roleFlagArguments...),
	Fields: []capability.Field{{Name: "id", Description: "The created role"}, {Name: "label", Description: "Its label"}},
	Examples: []capability.Example{{Description: "Create a read-only role",
		Arguments: json.RawMessage(`{"label":"Reader","can_read_all_object_records":true}`)}},
}

var rolesUpdate = capability.Descriptor{
	ID: Provider + ".roles.update", Version: 1, Title: "Update a Twenty CRM role",
	Description: "Change the label, the description, or the global rights of one role of the Twenty workspace of " +
		"a connection; only the given values change and at least one is required. The change applies to every " +
		"member assigned to the role. Qatlas reads the role first. " + roleWriteNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "update"},
	Risk:     roleWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"label":` + roleLabelSchema +
		`,` + roleFlagProperties + `},"required":["id"],"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(roleResultSchema),
	Arguments: append([]capability.Argument{roleIDArgument,
		{Name: "label", Description: "New label, 1 to 100 characters without control characters"}}, roleFlagArguments...),
	Fields: []capability.Field{{Name: "id", Description: "The changed role"}, {Name: "label", Description: "Its label"}},
	Examples: []capability.Example{{Description: "Withdraw the right to destroy records",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000","can_destroy_all_object_records":false}`)}},
}

var rolesDelete = capability.Descriptor{
	ID: Provider + ".roles.delete", Version: 1, Title: "Delete a Twenty CRM role",
	Description: "Delete one role of the Twenty workspace of a connection; the members assigned to it lose its " +
		"rights. Qatlas reads the role first. " + roleWriteNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "delete"},
	Risk:     roleWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{roleIDArgument},
	Fields:    []capability.Field{{Name: "id", Description: "The deleted role"}, {Name: "deleted", Description: "Always true"}},
	Examples: []capability.Example{{Description: "Delete a role",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// RoleWrite is the checked content of a create or update. A nil pointer is not sent.
type RoleWrite struct {
	Label                         *string
	Description                   *string
	CanUpdateAllSettings          *bool
	CanAccessAllTools             *bool
	CanReadAllObjectRecords       *bool
	CanUpdateAllObjectRecords     *bool
	CanSoftDeleteAllObjectRecords *bool
	CanDestroyAllObjectRecords    *bool
	CanBeAssignedToUsers          *bool
	CanBeAssignedToAgents         *bool
	CanBeAssignedToAPIKeys        *bool
}

// roleWriteArgs mirrors the input schema; decoding it strictly rejects unknown arguments.
type roleWriteArgs struct {
	ID                            string  `json:"id"`
	Label                         *string `json:"label"`
	Description                   *string `json:"description"`
	CanUpdateAllSettings          *bool   `json:"can_update_all_settings"`
	CanAccessAllTools             *bool   `json:"can_access_all_tools"`
	CanReadAllObjectRecords       *bool   `json:"can_read_all_object_records"`
	CanUpdateAllObjectRecords     *bool   `json:"can_update_all_object_records"`
	CanSoftDeleteAllObjectRecords *bool   `json:"can_soft_delete_all_object_records"`
	CanDestroyAllObjectRecords    *bool   `json:"can_destroy_all_object_records"`
	AssignableToUsers             *bool   `json:"assignable_to_users"`
	AssignableToAgents            *bool   `json:"assignable_to_agents"`
	AssignableToAPIKeys           *bool   `json:"assignable_to_api_keys"`
}

// input is the variable value of the Twenty input type; only given fields are present.
func (w RoleWrite) input() map[string]any {
	fields := map[string]any{}
	set := func(name string, value any, given bool) {
		if given {
			fields[name] = value
		}
	}
	if w.Label != nil {
		set("label", *w.Label, true)
	}
	if w.Description != nil {
		set("description", *w.Description, true)
	}
	for name, flag := range map[string]*bool{"canUpdateAllSettings": w.CanUpdateAllSettings,
		"canAccessAllTools": w.CanAccessAllTools, "canReadAllObjectRecords": w.CanReadAllObjectRecords,
		"canUpdateAllObjectRecords": w.CanUpdateAllObjectRecords, "canSoftDeleteAllObjectRecords": w.CanSoftDeleteAllObjectRecords,
		"canDestroyAllObjectRecords": w.CanDestroyAllObjectRecords, "canBeAssignedToUsers": w.CanBeAssignedToUsers,
		"canBeAssignedToAgents": w.CanBeAssignedToAgents, "canBeAssignedToApiKeys": w.CanBeAssignedToAPIKeys} {
		if flag != nil {
			set(name, *flag, true)
		}
	}
	return fields
}

func parseRoleArgs(raw json.RawMessage, needID bool) (string, RoleWrite, error) {
	var args roleWriteArgs
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return "", RoleWrite{}, invalidRequest(errRoleArgs)
	}
	if needID && !validUUID(args.ID) {
		return "", RoleWrite{}, invalidRequest(errRoleID)
	}
	if !needID && args.ID != "" {
		return "", RoleWrite{}, invalidRequest(errRoleArgs)
	}
	if args.Label != nil && !validRoleText(*args.Label, roleLabelMax) {
		return "", RoleWrite{}, invalidRequest(errRoleLabel)
	}
	if args.Description != nil && !validRoleText(*args.Description, roleDescriptionMax) {
		return "", RoleWrite{}, invalidRequest(errRoleDesc)
	}
	return args.ID, RoleWrite{Label: args.Label, Description: args.Description,
		CanUpdateAllSettings: args.CanUpdateAllSettings, CanAccessAllTools: args.CanAccessAllTools,
		CanReadAllObjectRecords: args.CanReadAllObjectRecords, CanUpdateAllObjectRecords: args.CanUpdateAllObjectRecords,
		CanSoftDeleteAllObjectRecords: args.CanSoftDeleteAllObjectRecords,
		CanDestroyAllObjectRecords:    args.CanDestroyAllObjectRecords, CanBeAssignedToUsers: args.AssignableToUsers,
		CanBeAssignedToAgents: args.AssignableToAgents, CanBeAssignedToAPIKeys: args.AssignableToAPIKeys}, nil
}

// validRoleText accepts 1 to max characters of valid text that is not blank and holds no control character.
func validRoleText(value string, max int) bool {
	n := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && n >= 1 && n <= max && !hasControl(value) && cleanText(&value, max) != ""
}

// rolesWritePermission names the settings right on a permission failure.
func rolesWritePermission(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = rolesRightMessage
	}
	return err
}

// guardRole reads the role once before a change and refuses it unless the role is clearly editable and has no
// API key. Anything missing or ambiguous is a refusal; the key list is read as identifiers only.
func (c *Client) guardRole(ctx context.Context, op, id string) error {
	var data struct {
		Role *struct {
			ID         string            `json:"id"`
			IsEditable *bool             `json:"isEditable"`
			APIKeys    []json.RawMessage `json:"apiKeys"`
		} `json:"getRole"`
	}
	if err := c.graphql(ctx, op, roleGuardDocument, map[string]any{"id": id}, &data); err != nil {
		return rolesWritePermission(err)
	}
	role := data.Role
	if role == nil || !equalUUID(role.ID, id) {
		return provider.InvalidResponse(op, "Twenty answered with a different role than the requested one")
	}
	if role.IsEditable == nil || !*role.IsEditable || role.APIKeys == nil || len(role.APIKeys) > 0 {
		return invalidRequest(errRoleKeys)
	}
	return nil
}

// RoleRef is the answer of a create or update: the role and its label.
type RoleRef struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type roleRefWire struct {
	ID    string  `json:"id"`
	Label *string `json:"label"`
}

// CreateRole sends exactly one mutation.
func (c *Client) CreateRole(ctx context.Context, w RoleWrite) (*RoleRef, error) {
	const op = "create role"
	var data struct {
		Role *roleRefWire `json:"createOneRole"`
	}
	if err := c.graphqlWith(ctx, op, roleUncertain, roleCreateDocument,
		map[string]any{"createRoleInput": w.input()}, &data); err != nil {
		return nil, rolesWritePermission(err)
	}
	if data.Role == nil || !validUUID(data.Role.ID) || w.Label == nil || data.Role.Label == nil ||
		cleanText(data.Role.Label, roleLabelMax) != cleanText(w.Label, roleLabelMax) {
		return nil, provider.InvalidResponse(op, "Twenty did not answer with the requested role"+roleUncertain)
	}
	return &RoleRef{ID: data.Role.ID, Label: cleanText(data.Role.Label, roleLabelMax)}, nil
}

// UpdateRole reads the role once, refuses key roles and non-editable roles, then sends exactly one mutation.
func (c *Client) UpdateRole(ctx context.Context, id string, w RoleWrite) (*RoleRef, error) {
	const op = "update role"
	if err := c.guardRole(ctx, op, id); err != nil {
		return nil, err
	}
	var data struct {
		Role *roleRefWire `json:"updateOneRole"`
	}
	variables := map[string]any{"updateRoleInput": map[string]any{"id": id, "update": w.input()}}
	if err := c.graphqlWith(ctx, op, roleUncertain, roleUpdateDocument, variables, &data); err != nil {
		return nil, rolesWritePermission(err)
	}
	if data.Role == nil || !equalUUID(data.Role.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different role than the requested one"+
			roleUncertain)
	}
	return &RoleRef{ID: id, Label: cleanText(data.Role.Label, roleLabelMax)}, nil
}

// DeleteRole reads the role once, refuses key roles and non-editable roles, then sends exactly one mutation.
func (c *Client) DeleteRole(ctx context.Context, id string) error {
	const op = "delete role"
	if err := c.guardRole(ctx, op, id); err != nil {
		return err
	}
	var data struct {
		ID *string `json:"deleteOneRole"`
	}
	if err := c.graphqlWith(ctx, op, roleUncertain, roleDeleteDocument, map[string]any{"roleId": id}, &data); err != nil {
		return rolesWritePermission(err)
	}
	if data.ID == nil || !equalUUID(*data.ID, id) {
		return provider.InvalidResponse(op, "Twenty answered with a different role than the requested one"+roleUncertain)
	}
	return nil
}

func invokeRolesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	_, write, err := parseRoleArgs(raw, false)
	if err != nil {
		return nil, err
	}
	if write.Label == nil {
		return nil, invalidRequest(errRoleLabel)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateRole(ctx, write)
}

func invokeRolesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	id, write, err := parseRoleArgs(raw, true)
	if err != nil {
		return nil, err
	}
	if len(write.input()) == 0 {
		return nil, invalidRequest(errRoleNone)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateRole(ctx, id, write)
}

func invokeRolesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return nil, invalidRequest(errRoleArgs)
	}
	id := args.ID
	if !validUUID(id) {
		return nil, invalidRequest(errRoleID)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteRole(ctx, id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}
