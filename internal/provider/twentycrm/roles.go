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
	// roleDataSensitivity classifies the roles and rights a role write reads and changes.
	roleDataSensitivity = "twentycrm-role-data"

	rolesMax           = 100
	roleLabelMax       = 100
	roleDescriptionMax = 500

	rolesRightMessage = "the API key needs the Twenty permission \"Roles\" to read or change roles; that " +
		"permission also covers role permissions, so use a connection with a key of its own for it"
)

// rolesDocument is the only document of roles.list and takes no variables. Members are read without avatar
// addresses, user account identifiers, and their own roles; API keys only by identifier, name, and dates.
var rolesDocument = graphqlDocument{path: metadataPath, text: `query GetRoles { getRoles { id label description ` +
	`isEditable canBeAssignedToUsers canBeAssignedToAgents canBeAssignedToApiKeys canUpdateAllSettings ` +
	`canAccessAllTools canReadAllObjectRecords canUpdateAllObjectRecords canSoftDeleteAllObjectRecords ` +
	`canDestroyAllObjectRecords universalIdentifier applicationId icon workspaceMembers { id name { firstName ` +
	`lastName } userEmail colorScheme uiScale openRecordIn locale calendarStartDay timeZone dateFormat timeFormat ` +
	`numberFormat } agents { id name label } apiKeys { id name expiresAt revokedAt } permissionFlags { id roleId ` +
	`flag } objectPermissions { objectMetadataId canReadObjectRecords canUpdateObjectRecords ` +
	`canSoftDeleteObjectRecords canDestroyObjectRecords restrictedFields rowLevelPermissionPredicates { ` +
	rolePredicateSelection + ` } rowLevelPermissionPredicateGroups { ` + rolePredicateGroupSelection + ` } } ` +
	`fieldPermissions { id objectMetadataId fieldMetadataId roleId canReadFieldValue canUpdateFieldValue } ` +
	`rowLevelPermissionPredicates { ` + rolePredicateSelection + ` } rowLevelPermissionPredicateGroups { ` +
	rolePredicateGroupSelection + ` } } }`}

const roleItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"},` +
	`"description":{"type":"string"},"editable":{"type":"boolean"},"assignable_to_users":{"type":"boolean"},` +
	`"assignable_to_agents":{"type":"boolean"},"assignable_to_api_keys":{"type":"boolean"},` +
	`"can_update_all_settings":{"type":"boolean"},"can_access_all_tools":{"type":"boolean"},` +
	`"can_read_all_object_records":{"type":"boolean"},"can_update_all_object_records":{"type":"boolean"},` +
	`"can_soft_delete_all_object_records":{"type":"boolean"},"can_destroy_all_object_records":{"type":"boolean"},` +
	`"member_count":{"type":"integer"},"api_key_count":{"type":"integer"},` + roleDetailProperties + `},` +
	`"required":["id","label","editable","member_count","api_key_count"],"additionalProperties":false}`

var rolesList = capability.Descriptor{
	ID:      Provider + ".roles.list",
	Version: 1,
	Title:   "List Twenty CRM roles",
	Description: "List the roles of the Twenty workspace of a connection without object targets: label, " +
		"description, icon, whether the role is editable, to whom it can be assigned, its global rights, settings " +
		"flags, object, field, and row-level rights, and the assigned members, AI agents, and API keys with their " +
		"counts. Members carry personal data but never avatar addresses or user account identifiers; API keys " +
		"carry no token. A relation list beyond its limit is left out and marked, never shortened. Needs the " +
		"Twenty permission \"Roles\", which also allows changing roles and permissions: use a connection with an " +
		"API key of its own for it. Values are untrusted workspace data",
	Tags:        []string{"twentycrm", "roles", "permissions", "workspace", "list"},
	Risk:        capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: memberDataSensitivity},
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"roles":{"type":"array","items":` + roleItemSchema +
		`},"truncated":{"type":"boolean"}},"required":["roles","truncated"],"additionalProperties":false}`),
	Fields: append([]capability.Field{
		{Name: "roles", Description: "At most 100 roles, untrusted data"},
		{Name: "truncated", Description: "True when the workspace holds more roles than listed"},
	}, roleDetailFields...),
	Examples: []capability.Example{{Description: "List the roles", Arguments: json.RawMessage(`{}`)}},
}

// Role is the stable view of one role.
type Role struct {
	ID                            string `json:"id"`
	Label                         string `json:"label"`
	Description                   string `json:"description,omitempty"`
	Editable                      bool   `json:"editable"`
	AssignableToUsers             bool   `json:"assignable_to_users"`
	AssignableToAgents            bool   `json:"assignable_to_agents"`
	AssignableToAPIKeys           bool   `json:"assignable_to_api_keys"`
	CanUpdateAllSettings          bool   `json:"can_update_all_settings"`
	CanAccessAllTools             bool   `json:"can_access_all_tools"`
	CanReadAllObjectRecords       bool   `json:"can_read_all_object_records"`
	CanUpdateAllObjectRecords     bool   `json:"can_update_all_object_records"`
	CanSoftDeleteAllObjectRecords bool   `json:"can_soft_delete_all_object_records"`
	CanDestroyAllObjectRecords    bool   `json:"can_destroy_all_object_records"`
	MemberCount                   int    `json:"member_count"`
	APIKeyCount                   int    `json:"api_key_count"`
	RoleDetails
}

// RoleList is the result of ListRoles.
type RoleList struct {
	Roles     []Role `json:"roles"`
	Truncated bool   `json:"truncated"`
}

type roleRecord struct {
	ID                            string            `json:"id"`
	Label                         *string           `json:"label"`
	Description                   *string           `json:"description"`
	IsEditable                    bool              `json:"isEditable"`
	CanBeAssignedToUsers          bool              `json:"canBeAssignedToUsers"`
	CanBeAssignedToAgents         bool              `json:"canBeAssignedToAgents"`
	CanBeAssignedToAPIKeys        bool              `json:"canBeAssignedToApiKeys"`
	CanUpdateAllSettings          bool              `json:"canUpdateAllSettings"`
	CanAccessAllTools             bool              `json:"canAccessAllTools"`
	CanReadAllObjectRecords       bool              `json:"canReadAllObjectRecords"`
	CanUpdateAllObjectRecords     bool              `json:"canUpdateAllObjectRecords"`
	CanSoftDeleteAllObjectRecords bool              `json:"canSoftDeleteAllObjectRecords"`
	CanDestroyAllObjectRecords    bool              `json:"canDestroyAllObjectRecords"`
	WorkspaceMembers              []json.RawMessage `json:"workspaceMembers"`
	APIKeys                       []json.RawMessage `json:"apiKeys"`
	roleDetailRecord
}

// ListRoles reads the roles with one fixed query.
func (c *Client) ListRoles(ctx context.Context) (*RoleList, error) {
	const op = "list roles"
	var data struct {
		GetRoles []roleRecord `json:"getRoles"`
	}
	if err := c.graphql(ctx, op, rolesDocument, map[string]any{}, &data); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = rolesRightMessage
		}
		return nil, err
	}
	if data.GetRoles == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	result := &RoleList{Roles: make([]Role, 0, len(data.GetRoles))}
	for i, r := range data.GetRoles {
		if !validUUID(r.ID) {
			return nil, provider.InvalidResponse(op, "Twenty returned a role without a usable identifier")
		}
		if i == rolesMax {
			result.Truncated = true
			break
		}
		result.Roles = append(result.Roles, Role{ID: r.ID, Label: cleanText(r.Label, roleLabelMax),
			Description: cleanText(r.Description, roleDescriptionMax), Editable: r.IsEditable,
			AssignableToUsers: r.CanBeAssignedToUsers, AssignableToAgents: r.CanBeAssignedToAgents,
			AssignableToAPIKeys: r.CanBeAssignedToAPIKeys, CanUpdateAllSettings: r.CanUpdateAllSettings,
			CanAccessAllTools: r.CanAccessAllTools, CanReadAllObjectRecords: r.CanReadAllObjectRecords,
			CanUpdateAllObjectRecords: r.CanUpdateAllObjectRecords, CanSoftDeleteAllObjectRecords: r.CanSoftDeleteAllObjectRecords,
			CanDestroyAllObjectRecords: r.CanDestroyAllObjectRecords, MemberCount: len(r.WorkspaceMembers),
			APIKeyCount: len(r.APIKeys), RoleDetails: r.details(r.WorkspaceMembers, r.APIKeys)})
	}
	return result, nil
}

func invokeRolesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRoles(ctx)
}
