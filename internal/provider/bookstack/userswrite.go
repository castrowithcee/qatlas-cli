package bookstack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	userNameMax     = 100
	userLanguageMax = 15
	userRolesMax    = 100
	usersWriteScope = "user management"

	userGuestRefused   = "a guest user (public access) cannot be changed through Qatlas"
	userMigrateRefused = "content ownership cannot be migrated to a guest user (public access)"

	usersWriteRoles = "Needs the BookStack role permission users-manage for the token's user. The tool is only " +
		"available through the tool allow list, and only on a connection without targets. No tool of Qatlas " +
		"creates users or sets an e-mail address, a password, or an external authentication identifier"
)

var (
	usersUpdateRisk = capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}
	usersDeleteRisk = capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}

	usersUpdate = capability.Descriptor{
		ID: Provider + ".users.update", Version: 1, Title: "Update a BookStack user",
		Description: "Change the name, the language, or the roles of one existing user, in one request. A field that " +
			"is left out stays unchanged; roles replaces all roles of the user, so an empty list removes every role. " +
			"The guest role (system role public) is never granted and a guest user is never changed through roles: " +
			"when roles is given, Qatlas determines the guest role first and refuses, without a change, when the " +
			"user is a guest user, when roles contains the guest role, or when the guest role cannot be determined; " +
			"that needs the role permission user-roles-manage as well. " + usersWriteRoles,
		Tags: []string{"administration", "users", "bookstack", "update"}, Risk: usersUpdateRisk, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},` +
			`"name":{"type":"string","minLength":1,"maxLength":100},` +
			`"language":{"type":"string","minLength":1,"maxLength":15,"pattern":"^[A-Za-z0-9_-]+$"},` +
			`"roles":{"type":"array","maxItems":100,"uniqueItems":true,"items":{"type":"integer","minimum":1}}},` +
			`"required":["id"],"additionalProperties":false}`),
		OutputSchema: usersGet.OutputSchema,
		Arguments: []capability.Argument{
			{Name: "id", Description: "User identifier", Required: true},
			{Name: "name", Description: "New user name, 1 to 100 characters"},
			{Name: "language", Description: "New language code, at most 15 letters, digits, hyphens, or underscores"},
			{Name: "roles", Description: "Role identifiers that replace all roles of the user, at most 100"},
		},
		Fields: usersGet.Fields,
		Examples: []capability.Example{{Description: "Give user 12 the roles 6 and 7",
			Arguments: json.RawMessage(`{"id":12,"roles":[6,7]}`)}},
	}

	usersDelete = capability.Descriptor{
		ID: Provider + ".users.delete", Version: 1, Title: "Delete a BookStack user",
		Description: "Permanently delete one existing user. This cannot be undone. Without migrate_ownership_id the " +
			"content of the user stays without an owner; with it, the ownership of that content passes to the " +
			"given user. Qatlas refuses, without a change, to migrate ownership to a guest user (public access) and " +
			"fails closed when the guest role cannot be determined; that needs the role permission user-roles-manage " +
			"as well. BookStack refuses to delete the only administrator and the guest user. " + usersWriteRoles,
		Tags: []string{"administration", "users", "bookstack", "delete"}, Risk: usersDeleteRisk, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},` +
			`"migrate_ownership_id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "id", Description: "Identifier of the user to delete", Required: true},
			{Name: "migrate_ownership_id", Description: "Identifier of the user who receives the content of the deleted user; must differ from id"},
		},
	}
)

// userChange is the validated request body of a user update; a nil field is left out of the body. It has no
// email, password, or external_auth_id.
type userChange struct {
	Name     *string  `json:"name,omitempty"`
	Language *string  `json:"language,omitempty"`
	Roles    *[]int64 `json:"roles,omitempty"`
}

// parseUserChange decodes and validates the user update locally, before any secret. Unknown arguments are
// refused, so no other field of the BookStack user API reaches the body.
func parseUserChange(raw json.RawMessage) (int64, userChange, error) {
	var in struct {
		ID int64 `json:"id"`
		userChange
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return 0, userChange{}, invalidRequest("the arguments could not be read or name an unsupported argument")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return 0, userChange{}, err
	}
	change := in.userChange
	if change.Name != nil {
		if n := utf8.RuneCountInString(*change.Name); n < 1 || n > userNameMax {
			return 0, userChange{}, invalidRequest("name must have 1 to 100 characters")
		}
	}
	if change.Language != nil {
		if n := len(*change.Language); n < 1 || n > userLanguageMax || !alphaDash(*change.Language) {
			return 0, userChange{}, invalidRequest("language must have 1 to 15 letters, digits, hyphens, or underscores")
		}
	}
	if change.Roles != nil {
		if len(*change.Roles) > userRolesMax {
			return 0, userChange{}, invalidRequest("roles must name at most 100 roles")
		}
		seen := map[int64]bool{}
		for _, id := range *change.Roles {
			if checkID(id, "roles entry") != nil {
				return 0, userChange{}, invalidRequest("roles must contain positive integers")
			}
			if seen[id] {
				return 0, userChange{}, invalidRequest("roles must not repeat a role")
			}
			seen[id] = true
		}
	}
	if change.Name == nil && change.Language == nil && change.Roles == nil {
		return 0, userChange{}, invalidRequest("at least one of name, language, and roles is required")
	}
	return in.ID, change, nil
}

func alphaDash(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// userDeletion is the validated request of a user deletion.
type userDeletion struct {
	migrate *int64
}

func parseUserDeletion(raw json.RawMessage) (int64, userDeletion, error) {
	var in struct {
		ID      int64  `json:"id"`
		Migrate *int64 `json:"migrate_ownership_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return 0, userDeletion{}, invalidRequest("the arguments could not be read or name an unsupported argument")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return 0, userDeletion{}, err
	}
	if in.Migrate != nil {
		if err := checkID(*in.Migrate, "migrate_ownership_id"); err != nil {
			return 0, userDeletion{}, err
		}
		if *in.Migrate == in.ID {
			return 0, userDeletion{}, invalidRequest("migrate_ownership_id must differ from id")
		}
	}
	return in.ID, userDeletion{migrate: in.Migrate}, nil
}

func invokeUsersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, change, err := parseUserChange(raw)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, usersWriteScope); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateUser(ctx, id, change)
}

func invokeUsersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, deletion, err := parseUserDeletion(raw)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, usersWriteScope); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteUser(ctx, id, deletion); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// UpdateUser sends exactly one change request, which is never repeated. When roles are replaced, it first
// determines the guest role and refuses a guest user and the guest role.
func (c *Client) UpdateUser(ctx context.Context, id int64, change userChange) (output.Object, error) {
	const op = "update user"
	if change.Roles != nil {
		guest, err := c.guestRole(ctx)
		if err != nil {
			return output.Object{}, err
		}
		if guest.members[id] || slices.Contains(*change.Roles, guest.id) {
			return output.Object{}, invalidRequest(userGuestRefused)
		}
	}
	var updated accountJSON
	if err := c.mutate(ctx, op, http.MethodPut, "/api/users/"+strconv.FormatInt(id, 10), change, &updated, argNames(usersUpdate)); err != nil {
		return output.Object{}, err
	}
	return rowObject(userRow(updated), fieldNames(usersGet)), nil
}

// DeleteUser sends exactly one delete request, which is never repeated. When ownership is migrated, it first
// determines the guest role and refuses a guest user as the new owner.
func (c *Client) DeleteUser(ctx context.Context, id int64, deletion userDeletion) error {
	const op = "delete user"
	path := "/api/users/" + strconv.FormatInt(id, 10)
	if deletion.migrate == nil {
		return c.mutate(ctx, op, http.MethodDelete, path, nil, nil, argNames(usersDelete))
	}
	guest, err := c.guestRole(ctx)
	if err != nil {
		return err
	}
	if guest.members[*deletion.migrate] {
		return invalidRequest(userMigrateRefused)
	}
	body := struct {
		MigrateOwnershipID int64 `json:"migrate_ownership_id"`
	}{*deletion.migrate}
	return c.mutate(ctx, op, http.MethodDelete, path, body, nil, argNames(usersDelete))
}
