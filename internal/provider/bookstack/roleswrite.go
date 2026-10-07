package bookstack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	roleNameMin     = 3
	roleTextMax     = 180
	rolesWriteScope = "role management"

	roleGuestRefused  = "the guest role (public access) cannot be changed through Qatlas"
	roleSystemRefused = "a system role cannot be deleted through Qatlas"

	rolesWriteRoles = "Needs the BookStack role permission user-roles-manage for the token's user. Role permissions " +
		"can escalate up to full administrative control of the instance, so the tool is only available through " +
		"the tool allow list, and only on a connection without targets. external_auth_id is not supported: " +
		"Qatlas never binds a role to groups of an external identity provider"
	rolesWritePermissions = "permissions names the system permissions and content permissions of the role from the fixed " +
		"list of BookStack v26.09.1 that the role form offers; unknown or repeated names are refused locally"
)

// rolePermissionNames are the permission names a role can be given, as the role form of BookStack v26.09.1
// offers them. The list is fixed in code; no other name is ever sent.
var rolePermissionNames = []string{
	"access-api",
	"attachment-create-all", "attachment-delete-all", "attachment-delete-own", "attachment-update-all", "attachment-update-own",
	"book-create-all", "book-delete-all", "book-delete-own", "book-update-all", "book-update-own", "book-view-all", "book-view-own",
	"bookshelf-create-all", "bookshelf-delete-all", "bookshelf-delete-own", "bookshelf-update-all", "bookshelf-update-own", "bookshelf-view-all", "bookshelf-view-own",
	"chapter-create-all", "chapter-create-own", "chapter-delete-all", "chapter-delete-own", "chapter-update-all", "chapter-update-own", "chapter-view-all", "chapter-view-own",
	"comment-create-all", "comment-delete-all", "comment-delete-own", "comment-update-all", "comment-update-own",
	"content-export", "content-import", "editor-change",
	"image-create-all", "image-delete-all", "image-delete-own", "image-update-all", "image-update-own",
	"page-create-all", "page-create-own", "page-delete-all", "page-delete-own", "page-update-all", "page-update-own", "page-view-all", "page-view-own",
	"receive-notifications",
	"restrictions-manage-all", "restrictions-manage-own",
	"revision-view-all",
	"settings-manage", "templates-manage", "user-roles-manage", "users-manage",
}

var rolesWriteSchemaProps = `"display_name":{"type":"string","minLength":3,"maxLength":180},"description":{"type":"string","maxLength":180},` +
	`"mfa_enforced":{"type":"boolean"},"permissions":{"type":"array","maxItems":` + strconv.Itoa(len(rolePermissionNames)) +
	`,"uniqueItems":true,"items":{"type":"string","enum":` + jsonStrings(rolePermissionNames) + `}}`

func jsonStrings(values []string) string {
	data, _ := json.Marshal(values)
	return string(data)
}

var (
	rolesCreateRisk = capability.Risk{
		Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}
	rolesUpdateRisk = capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}
	rolesDeleteRisk = capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}

	rolesCreate = capability.Descriptor{
		ID: Provider + ".roles.create", Version: 1, Title: "Create a BookStack role",
		Description: "Create a role with a name, an optional description, an optional multi-factor enforcement flag, " +
			"and optional permissions, in one request. " + rolesWritePermissions + ". " + rolesWriteRoles,
		Tags: []string{"administration", "roles", "bookstack", "create"}, Risk: rolesCreateRisk, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema:           json.RawMessage(`{"type":"object","properties":{` + rolesWriteSchemaProps + `},"required":["display_name"],"additionalProperties":false}`),
		OutputSchema:          rolesGet.OutputSchema,
		Arguments: []capability.Argument{
			{Name: "display_name", Description: "Role name, 3 to 180 characters", Required: true},
			{Name: "description", Description: "Role description, at most 180 characters"},
			{Name: "mfa_enforced", Description: "Whether users of the role must use multi-factor authentication"},
			{Name: "permissions", Description: "Permission names from the fixed list"},
		},
		Fields: rolesGet.Fields,
		Examples: []capability.Example{{Description: "Create a read-only role",
			Arguments: json.RawMessage(`{"display_name":"Readers","permissions":["book-view-all","chapter-view-all","page-view-all","bookshelf-view-all"]}`)}},
	}

	rolesUpdate = capability.Descriptor{
		ID: Provider + ".roles.update", Version: 1, Title: "Update a BookStack role",
		Description: "Change the name, description, multi-factor enforcement flag, or permissions of one role, in one " +
			"request. A field that is left out stays unchanged; permissions replaces the whole permission list, so an " +
			"empty list removes every permission. " + rolesWritePermissions + ". The guest role (system role public) " +
			"is never changed: Qatlas reads the role and determines the guest role first and refuses, without a change, " +
			"when the role is the guest role or the guest role cannot be determined. " + rolesWriteRoles,
		Tags: []string{"administration", "roles", "bookstack", "update"}, Risk: rolesUpdateRisk, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema:           json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},` + rolesWriteSchemaProps + `},"required":["id"],"additionalProperties":false}`),
		OutputSchema:          rolesGet.OutputSchema,
		Arguments: []capability.Argument{
			{Name: "id", Description: "Role identifier", Required: true},
			{Name: "display_name", Description: "New role name, 3 to 180 characters"},
			{Name: "description", Description: "New role description, at most 180 characters"},
			{Name: "mfa_enforced", Description: "Whether users of the role must use multi-factor authentication"},
			{Name: "permissions", Description: "Replaces the whole permission list with names from the fixed list"},
		},
		Fields: rolesGet.Fields,
		Examples: []capability.Example{{Description: "Replace the permissions of role 6",
			Arguments: json.RawMessage(`{"id":6,"permissions":["book-view-all","page-view-all"]}`)}},
	}

	rolesDelete = capability.Descriptor{
		ID: Provider + ".roles.delete", Version: 1, Title: "Delete a BookStack role",
		Description: "Permanently delete one role. This cannot be undone: the users of the role lose it, the content " +
			"permissions of the role are removed, and the API offers no migration of the users to another role. " +
			"BookStack refuses to delete the registration role, and Qatlas refuses every system role (a role with a " +
			"system name, such as admin or public) after reading the role, without a change. " + rolesWriteRoles,
		Tags: []string{"administration", "roles", "bookstack", "delete"}, Risk: rolesDeleteRisk, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema:           json.RawMessage(idOnlySchema),
		OutputSchema:          json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
		Arguments:             []capability.Argument{{Name: "id", Description: "Role identifier", Required: true}},
	}
)

// roleChange is the validated request body of a role write; a nil field is left out of the body. It has no
// external_auth_id.
type roleChange struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Description *string   `json:"description,omitempty"`
	MFAEnforced *bool     `json:"mfa_enforced,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

// parseRoleChange decodes and validates the role fields locally, before any secret. Unknown arguments are
// refused, so external_auth_id never reaches the body.
func parseRoleChange(raw json.RawMessage, create bool) (int64, roleChange, error) {
	var in struct {
		ID int64 `json:"id"`
		roleChange
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return 0, roleChange{}, invalidRequest("the arguments could not be read or name an unsupported argument")
	}
	if !create {
		if err := checkID(in.ID, "id"); err != nil {
			return 0, roleChange{}, err
		}
	} else if in.ID != 0 {
		return 0, roleChange{}, invalidRequest("id is not an argument of this tool")
	}
	change := in.roleChange
	if change.DisplayName != nil {
		if n := utf8.RuneCountInString(*change.DisplayName); n < roleNameMin || n > roleTextMax {
			return 0, roleChange{}, invalidRequest("display_name must have 3 to 180 characters")
		}
	} else if create {
		return 0, roleChange{}, invalidRequest("display_name is required")
	}
	if change.Description != nil && utf8.RuneCountInString(*change.Description) > roleTextMax {
		return 0, roleChange{}, invalidRequest("description must have at most 180 characters")
	}
	if change.Permissions != nil {
		seen := map[string]bool{}
		for _, name := range *change.Permissions {
			if _, found := slices.BinarySearch(rolePermissionNames, name); !found {
				return 0, roleChange{}, invalidRequest("permissions names a permission that is not on the fixed list of role permissions")
			}
			if seen[name] {
				return 0, roleChange{}, invalidRequest("permissions must not repeat a name")
			}
			seen[name] = true
		}
	}
	if !create && change.DisplayName == nil && change.Description == nil && change.MFAEnforced == nil && change.Permissions == nil {
		return 0, roleChange{}, invalidRequest("at least one of display_name, description, mfa_enforced, and permissions is required")
	}
	return in.ID, change, nil
}

func invokeRolesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, change, err := parseRoleChange(raw, true)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, rolesWriteScope); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateRole(ctx, change)
}

func invokeRolesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, change, err := parseRoleChange(raw, false)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, rolesWriteScope); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateRole(ctx, id, change)
}

func invokeRolesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodePeopleID(raw, rolesWriteScope, resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteRole(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// readRole reads one role before a change and checks that the answer is the requested role.
func (c *Client) readRole(ctx context.Context, op string, id int64) (roleJSON, error) {
	var role roleJSON
	if err := c.get(ctx, op, "/api/roles/"+strconv.FormatInt(id, 10), nil, &role, nil, provider.ClassPermission); err != nil {
		return roleJSON{}, err
	}
	if role.ID != id {
		return roleJSON{}, providerError(op, "the role answer did not match the requested role")
	}
	return role, nil
}

// CreateRole sends one create request, which is never repeated.
func (c *Client) CreateRole(ctx context.Context, change roleChange) (output.Object, error) {
	var created roleJSON
	if err := c.mutate(ctx, "create role", http.MethodPost, "/api/roles", change, &created, argNames(rolesCreate)); err != nil {
		return output.Object{}, err
	}
	return roleObject(created), nil
}

// UpdateRole reads the role and determines the guest role, refuses the guest role locally, and then sends
// exactly one request, which is never repeated.
func (c *Client) UpdateRole(ctx context.Context, id int64, change roleChange) (output.Object, error) {
	const op = "update role"
	current, err := c.readRole(ctx, op, id)
	if err != nil {
		return output.Object{}, err
	}
	if current.SystemName == publicSystemName {
		return output.Object{}, invalidRequest(roleGuestRefused)
	}
	guest, err := c.guestRole(ctx)
	if err != nil {
		return output.Object{}, err
	}
	if guest.id == id {
		return output.Object{}, invalidRequest(roleGuestRefused)
	}
	var updated roleJSON
	if err := c.mutate(ctx, op, http.MethodPut, "/api/roles/"+strconv.FormatInt(id, 10), change, &updated, argNames(rolesUpdate)); err != nil {
		return output.Object{}, err
	}
	return roleObject(updated), nil
}

// DeleteRole reads the role, refuses a system role locally, and then sends exactly one delete request.
func (c *Client) DeleteRole(ctx context.Context, id int64) error {
	const op = "delete role"
	current, err := c.readRole(ctx, op, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(current.SystemName) != "" {
		return invalidRequest(roleSystemRefused)
	}
	return c.mutate(ctx, op, http.MethodDelete, "/api/roles/"+strconv.FormatInt(id, 10), nil, nil, argNames(rolesDelete))
}
