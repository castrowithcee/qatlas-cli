package bookstack

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxRoleMembers bounds the users one role result lists.
	maxRoleMembers = 1000
	// maxRolePermissionNames bounds the permission names one role result lists.
	maxRolePermissionNames = 500

	usersInstanceWide = "user listings"
	rolesInstanceWide = "role listings"

	usersRoles = "BookStack requires the role permission users-manage for the token's user. Names, e-mail addresses, " +
		"and external authentication identifiers are personal data and untrusted provider content. The users of the " +
		"whole instance are covered, so a connection bound to books cannot use this tool"
	rolesRoles = "BookStack requires the role permission user-roles-manage for the token's user. Role and user names " +
		"are untrusted provider content. The roles of the whole instance are covered, so a connection bound to books " +
		"cannot use this tool"
)

const (
	userRolesProps = `"roles":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"display_name":{"type":"string"}}}}`
	userObject     = `"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"email":{"type":"string"},"external_auth_id":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"last_activity_at":{"type":"string"},` + userRolesProps + `,"profile_url":{"type":"string"}`
	roleCommon     = `"id":{"type":"integer"},"display_name":{"type":"string"},"description":{"type":"string"},"system_name":{"type":"string"},"external_auth_id":{"type":"string"},"mfa_enforced":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"}`
	idOnlySchema   = `{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`
	pagingSchema   = `{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`
)

var (
	peopleReadRisk = capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}

	userFields = []capability.Field{
		{Name: "id", Description: "User identifier"},
		{Name: "name", Description: "User name, untrusted data"},
		{Name: "slug", Description: "URL slug of the user"},
		{Name: "email", Description: "E-mail address, personal data"},
		{Name: "external_auth_id", Description: "Identifier at an external authentication system, personal data"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
		{Name: "last_activity_at", Description: "Time of the last activity of the user"},
		{Name: "roles", Description: "Roles of the user as id and display_name; the listing does not carry them"},
		{Name: "profile_url", Description: "Address of the profile of the user"},
	}

	usersList = capability.Descriptor{
		ID: Provider + ".users.list", Version: 1, Title: "List BookStack users",
		Description: "List the users of the instance with e-mail address and external authentication identifier. " +
			"BookStack does not report roles in the listing; read one user to see them. " + usersRoles,
		Tags: []string{"administration", "users", "bookstack"}, Risk: peopleReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(pagingSchema),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{` + userObject + `},"required":["id","name","slug"]}}`),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of users to return; 0 returns all"},
			{Name: "offset", Description: "Number of users to skip"},
		},
		Fields:   userFields,
		Examples: []capability.Example{{Description: "List the first 25 users", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	usersGet = capability.Descriptor{
		ID: Provider + ".users.get", Version: 1, Title: "Get a BookStack user",
		Description: "Read one user of the instance with the roles. " + usersRoles,
		Tags:        []string{"administration", "users", "bookstack"}, Risk: peopleReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(idOnlySchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + userObject + `},"required":["id","name","slug"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "User identifier", Required: true}},
		Fields:       userFields,
	}

	rolesList = capability.Descriptor{
		ID: Provider + ".roles.list", Version: 1, Title: "List BookStack roles",
		Description: "List the roles of the instance with the number of their users and permissions. " + rolesRoles,
		Tags:        []string{"administration", "roles", "bookstack"}, Risk: peopleReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(pagingSchema),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{` + roleCommon + `,"users_count":{"type":"integer"},"permissions_count":{"type":"integer"}},"required":["id","display_name"]}}`),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of roles to return; 0 returns all"},
			{Name: "offset", Description: "Number of roles to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Role identifier"},
			{Name: "display_name", Description: "Role name, untrusted data"},
			{Name: "description", Description: "Role description, untrusted data"},
			{Name: "system_name", Description: "System name of a built-in role, such as admin or public; empty otherwise"},
			{Name: "external_auth_id", Description: "Identifier of the role at an external authentication system"},
			{Name: "mfa_enforced", Description: "Whether the role enforces multi-factor authentication"},
			{Name: "users_count", Description: "Number of users with the role"},
			{Name: "permissions_count", Description: "Number of permissions of the role"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
		},
		Examples: []capability.Example{{Description: "List the first 25 roles", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	rolesGet = capability.Descriptor{
		ID: Provider + ".roles.get", Version: 1, Title: "Get a BookStack role",
		Description: "Read one role with its permission names and its users (at most 1000). " + rolesRoles,
		Tags:        []string{"administration", "roles", "bookstack"}, Risk: peopleReadRisk, Provider: Provider,
		InputSchema: json.RawMessage(idOnlySchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + roleCommon + `,"permissions":{"type":"array","items":{"type":"string"}},` +
			`"users":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},"truncated":{"type":"boolean"}},"required":["id","display_name","permissions","users","truncated"]}`),
		Arguments: []capability.Argument{{Name: "id", Description: "Role identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Role identifier"},
			{Name: "display_name", Description: "Role name, untrusted data"},
			{Name: "description", Description: "Role description, untrusted data"},
			{Name: "system_name", Description: "System name of a built-in role, such as admin or public; empty otherwise"},
			{Name: "external_auth_id", Description: "Identifier of the role at an external authentication system"},
			{Name: "mfa_enforced", Description: "Whether the role enforces multi-factor authentication"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "permissions", Description: "Names of the permissions of the role"},
			{Name: "users", Description: "Users with the role as id and name, at most 1000"},
			{Name: "truncated", Description: "True when the users or permissions were cut"},
		},
	}
)

type userRoleJSON struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

type accountJSON struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Email          string         `json:"email"`
	ExternalAuthID string         `json:"external_auth_id"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	LastActivityAt string         `json:"last_activity_at"`
	ProfileURL     string         `json:"profile_url"`
	Roles          []userRoleJSON `json:"roles"`
}

func userRow(u accountJSON) output.Row {
	row := output.Row{
		"id": u.ID, "name": clip(u.Name, maxResultString), "slug": clip(u.Slug, maxResultString),
		"email": clip(u.Email, maxResultString), "external_auth_id": clip(u.ExternalAuthID, maxResultString),
		"created_at": clip(u.CreatedAt, maxResultString), "updated_at": clip(u.UpdatedAt, maxResultString),
		"last_activity_at": clip(u.LastActivityAt, maxResultString), "profile_url": clip(u.ProfileURL, maxResultString),
	}
	if u.Roles != nil {
		roles := make([]map[string]any, 0, len(u.Roles))
		for i, r := range u.Roles {
			if i >= maxRoleMembers {
				break
			}
			roles = append(roles, map[string]any{"id": r.ID, "display_name": clip(r.DisplayName, maxResultString)})
		}
		row["roles"] = roles
	}
	return row
}

type roleJSON struct {
	ID               int64    `json:"id"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	SystemName       string   `json:"system_name"`
	ExternalAuthID   string   `json:"external_auth_id"`
	MFAEnforced      bool     `json:"mfa_enforced"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	UsersCount       int64    `json:"users_count"`
	PermissionsCount int64    `json:"permissions_count"`
	Permissions      []string `json:"permissions"`
	Users            []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"users"`
}

func roleRow(r roleJSON) output.Row {
	return output.Row{
		"id": r.ID, "display_name": clip(r.DisplayName, maxResultString), "description": clip(r.Description, maxResultString),
		"system_name": clip(r.SystemName, maxResultString), "external_auth_id": clip(r.ExternalAuthID, maxResultString),
		"mfa_enforced": r.MFAEnforced, "users_count": r.UsersCount, "permissions_count": r.PermissionsCount,
		"created_at": clip(r.CreatedAt, maxResultString), "updated_at": clip(r.UpdatedAt, maxResultString),
	}
}

type pagingInput struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in pagingInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, invalidRequest("the arguments could not be read")
	}
	if err := requireInstanceScope(resolved, usersInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListUsers(ctx, in.Limit, in.Offset)
}

func invokeRolesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in pagingInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, invalidRequest("the arguments could not be read")
	}
	if err := requireInstanceScope(resolved, rolesInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRoles(ctx, in.Limit, in.Offset)
}

func invokeUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodePeopleID(raw, usersInstanceWide, resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetUser(ctx, id)
}

func invokeRolesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodePeopleID(raw, rolesInstanceWide, resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetRole(ctx, id)
}

// decodePeopleID reads the id argument after the instance gate and before any secret is read.
func decodePeopleID(raw json.RawMessage, what string, resolved *config.Resolved) (int64, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return 0, invalidRequest("the arguments could not be read")
	}
	if err := requireInstanceScope(resolved, what); err != nil {
		return 0, err
	}
	if in.ID <= 0 {
		return 0, invalidRequest("id must be a positive integer")
	}
	return in.ID, nil
}

// ListUsers returns the reduced users of the instance.
func (c *Client) ListUsers(ctx context.Context, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[accountJSON]{
		op: "list users", path: "/api/users", limit: limit, offset: offset, arguments: argNames(usersList),
		id:   func(u accountJSON) int64 { return u.ID },
		keep: func(accountJSON) bool { return true },
		row:  userRow,
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(usersList), Rows: rows}, nil
}

// GetUser returns one reduced user with the roles.
func (c *Client) GetUser(ctx context.Context, id int64) (output.Object, error) {
	var user accountJSON
	if err := c.get(ctx, "get user", "/api/users/"+strconv.FormatInt(id, 10), nil, &user, argNames(usersGet),
		provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	return rowObject(userRow(user), fieldNames(usersGet)), nil
}

// ListRoles returns the reduced roles of the instance.
func (c *Client) ListRoles(ctx context.Context, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[roleJSON]{
		op: "list roles", path: "/api/roles", limit: limit, offset: offset, arguments: argNames(rolesList),
		id:   func(r roleJSON) int64 { return r.ID },
		keep: func(roleJSON) bool { return true },
		row:  roleRow,
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(rolesList), Rows: rows}, nil
}

// GetRole returns one reduced role with permission names and at most maxRoleMembers users.
func (c *Client) GetRole(ctx context.Context, id int64) (output.Object, error) {
	var role roleJSON
	if err := c.get(ctx, "get role", "/api/roles/"+strconv.FormatInt(id, 10), nil, &role, argNames(rolesGet),
		provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	row := roleRow(role)
	delete(row, "users_count")
	delete(row, "permissions_count")
	truncated := len(role.Users) > maxRoleMembers || len(role.Permissions) > maxRolePermissionNames
	users := make([]map[string]any, 0, len(role.Users))
	for i, u := range role.Users {
		if i >= maxRoleMembers {
			break
		}
		users = append(users, map[string]any{"id": u.ID, "name": clip(u.Name, maxResultString)})
	}
	permissions := make([]string, 0, len(role.Permissions))
	for i, name := range role.Permissions {
		if i >= maxRolePermissionNames {
			break
		}
		permissions = append(permissions, clip(name, maxResultString))
	}
	row["permissions"], row["users"], row["truncated"] = permissions, users, truncated
	return rowObject(row, fieldNames(rolesGet)), nil
}

// rowObject orders a reduced row by the declared fields; fields without a value are left out.
func rowObject(row output.Row, names []string) output.Object {
	fields := make([]output.Field, 0, len(names))
	for _, name := range names {
		if value, ok := row[name]; ok {
			fields = append(fields, output.Field{Name: name, Value: value})
		}
	}
	return output.Object{Fields: fields}
}
