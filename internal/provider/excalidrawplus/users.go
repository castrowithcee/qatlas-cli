package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// peopleSensitivity classifies results and changes about the people of the workspace: names, e-mail addresses,
// roles, and team membership.
const peopleSensitivity = "excalidrawplus-workspace-people"

const (
	// Local ceilings: Excalidraw+ documents no length for a name, an e-mail address, or a team entry.
	maxDisplayName = 200
	maxEmailLength = 254
	maxTeamGroups  = 50
	maxTeamEntries = 50
	maxTeamText    = 128
	maxRoleText    = 32
)

// Roles are the closed set the documentation names for a user.
const (
	roleMember = "member"
	roleAdmin  = "admin"
)

// peopleRisk is the contract of a confirmed change to users. Idempotency stays unknown until it is
// shown live.
func peopleRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity}
}

var peopleReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity}

const (
	workspaceWide = "Workspace-wide: only a connection with the * target offers it"
	peopleNote    = "Names, e-mail addresses, and teams are personal data and untrusted provider data"
	changeNote    = "the change is sent once and never repeated; after a timeout or a server error it may have taken effect"
)

const userSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"email":{"type":"string"},"role":{"type":"string"},` +
	`"teams":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},` +
	`"created":{"type":"string"},"last_active":{"type":"string"}},` +
	`"required":["id"],"additionalProperties":false}`

var userFields = []capability.Field{
	{Name: "id", Description: "User identifier, usable as user_id"},
	{Name: "name", Description: "Display name, personal and untrusted data"},
	{Name: "email", Description: "E-mail address, personal data"},
	{Name: "role", Description: "Workspace role as Excalidraw+ reports it, member or admin"},
	{Name: "teams", Description: "Team assignment as Excalidraw+ reports it (workspaceTeams): groups with their " +
		"entries, capped; the meaning of the group keys is not documented"},
	{Name: "created", Description: "Creation time, as Excalidraw+ reports it"},
	{Name: "last_active", Description: "Last activity time, as Excalidraw+ reports it"},
}

const roleSchema = `{"type":"string","enum":["member","admin"]}`

var userIDArgument = capability.Argument{Name: "user_id", Required: true,
	Description: "User identifier, as returned by excalidrawplus.users.list"}

var usersList = capability.Descriptor{
	ID:      Provider + ".users.list",
	Version: 1,
	Title:   "List Excalidraw+ workspace users",
	Description: "List the users of the workspace with name, e-mail address, role, and teams, page by page with a " +
		"numeric offset. " + workspaceWide + ". " + peopleNote,
	Tags:     []string{"excalidrawplus", "users", "list", "whiteboard"},
	Risk:     peopleReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pageSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"users":{"type":"array","items":` + userSchema + `},` +
		`"offset":{"type":"integer"},"limit":{"type":"integer"},"has_next_page":{"type":"boolean"},` +
		`"next_offset":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["users","offset","limit","has_next_page","count"],"additionalProperties":false}`),
	Arguments: pageArguments,
	Fields:    append(append([]capability.Field{}, userFields...), pageFields...),
	Examples:  []capability.Example{{Description: "List the first page of users", Arguments: json.RawMessage(`{}`)}},
}

var usersGet = capability.Descriptor{
	ID:      Provider + ".users.get",
	Version: 1,
	Title:   "Get an Excalidraw+ workspace user",
	Description: "Read one workspace user by identifier: name, e-mail address, role, and teams. " + workspaceWide +
		". " + peopleNote,
	Tags:     []string{"excalidrawplus", "users", "get", "whiteboard"},
	Risk:     peopleReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema +
		`},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(userSchema),
	Arguments:    []capability.Argument{userIDArgument},
	Fields:       userFields,
	Examples:     []capability.Example{{Description: "Read a user", Arguments: json.RawMessage(`{"user_id":"abc123"}`)}},
}

var usersUpdate = capability.Descriptor{
	ID:      Provider + ".users.update",
	Version: 1,
	Title:   "Update an Excalidraw+ workspace user",
	Description: "Change the display name, the role, or both of a workspace user; at least one is required. The " +
		"role admin grants workspace administration, so this tool is offered only to a connection whose tools list " +
		"names it. E-mail addresses and team assignments are not changed. " + workspaceWide + ". " + peopleNote +
		"; " + changeNote,
	Tags:                  []string{"excalidrawplus", "users", "update", "whiteboard"},
	Risk:                  peopleRisk(capability.EffectUpdate),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,"name":` +
		`{"type":"string","minLength":1,"maxLength":200},"role":` + roleSchema +
		`},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(userSchema),
	Arguments: []capability.Argument{userIDArgument,
		{Name: "name", Description: "New display name, 1 to 200 characters, no control characters"},
		{Name: "role", Description: "New role: member or admin"}},
	Fields: userFields,
	Examples: []capability.Example{{Description: "Make a user a member",
		Arguments: json.RawMessage(`{"user_id":"abc123","role":"member"}`)}},
}

const removeSchema = `{"type":"object","properties":{"id":{"type":"string"},"removed":{"type":"boolean"}},` +
	`"required":["id","removed"],"additionalProperties":false}`

var usersRemove = capability.Descriptor{
	ID:      Provider + ".users.remove",
	Version: 1,
	Title:   "Remove an Excalidraw+ workspace user",
	Description: "Remove a user from the workspace. Excalidraw+ revokes the user's access and removes the " +
		"workspace-specific data of the user; Qatlas cannot undo it. " + workspaceWide + ". " + changeNote,
	Tags:                  []string{"excalidrawplus", "users", "remove", "delete", "whiteboard"},
	Risk:                  peopleRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema +
		`},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(removeSchema),
	Arguments:    []capability.Argument{userIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the removed user"},
		{Name: "removed", Description: "True when Excalidraw+ accepted the removal"}},
	Examples: []capability.Example{{Description: "Remove a user", Arguments: json.RawMessage(`{"user_id":"abc123"}`)}},
}

// requireWildcard refuses, locally before any secret or request, a connection that is not bound to the whole
// workspace. The users of a workspace belong to no collection, so only the * target reaches them.
func requireWildcard(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.wildcard {
		return invalidRequest("this tool needs a connection with the * target")
	}
	return nil
}

func validRole(role string) bool { return role == roleMember || role == roleAdmin }

func validDisplayName(name string) bool {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxDisplayName {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type userJSON struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Email         string          `json:"email"`
	Role          string          `json:"role"`
	Created       string          `json:"created"`
	LastActive    string          `json:"lastActive"`
	WorkspaceTeam json.RawMessage `json:"workspaceTeams"`
}

type usersPageJSON struct {
	pageJSON
	Data []userJSON `json:"data"`
}

// User is the stable view of one workspace user. Picture, preferences, scene history, and rate limits are not
// passed on.
type User struct {
	ID         string              `json:"id"`
	Name       string              `json:"name,omitempty"`
	Email      string              `json:"email,omitempty"`
	Role       string              `json:"role,omitempty"`
	Teams      map[string][]string `json:"teams,omitempty"`
	Created    string              `json:"created,omitempty"`
	LastActive string              `json:"last_active,omitempty"`
}

// UsersPage is one offset-paginated listing of users.
type UsersPage struct {
	Users       []User `json:"users"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
	HasNextPage bool   `json:"has_next_page"`
	NextOffset  *int   `json:"next_offset,omitempty"`
	Count       int    `json:"count"`
}

// teamsOf reads the team assignment leniently: its structure is only documented as groups of strings, so an
// unexpected shape yields no teams instead of an error. The result is capped and every string is bounded.
func teamsOf(raw json.RawMessage) map[string][]string {
	var groups map[string][]string
	if len(raw) == 0 || json.Unmarshal(raw, &groups) != nil || len(groups) == 0 {
		return nil
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxTeamGroups {
		keys = keys[:maxTeamGroups]
	}
	teams := make(map[string][]string, len(keys))
	for _, key := range keys {
		entries := groups[key]
		if len(entries) > maxTeamEntries {
			entries = entries[:maxTeamEntries]
		}
		out := make([]string, 0, len(entries))
		for _, entry := range entries {
			out = append(out, bounded(entry, maxTeamText))
		}
		teams[bounded(key, maxTeamText)] = out
	}
	return teams
}

func userOf(item userJSON) User {
	return User{ID: item.ID, Name: bounded(item.Name, maxDisplayName*4), Email: bounded(item.Email, maxEmailLength),
		Role: bounded(item.Role, maxRoleText), Teams: teamsOf(item.WorkspaceTeam),
		Created: boundedValue(item.Created), LastActive: boundedValue(item.LastActive)}
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input pageArgs
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list users", "the validated arguments could not be read")
	}
	if err := requireWildcard(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page usersPageJSON
	if err := client.get(ctx, "list users", "/workspaces/users", input.query(), &page, maxResponseBytes); err != nil {
		return nil, err
	}
	result := &UsersPage{Users: []User{}, Offset: input.Offset, Limit: page.Limit,
		HasNextPage: page.HasNextPage, NextOffset: nextOffset(page.pageJSON, input)}
	if result.Limit == 0 {
		result.Limit = input.Limit
		if result.Limit == 0 {
			result.Limit = defaultListLimit
		}
	}
	for _, item := range page.Data {
		if validID(item.ID) {
			result.Users = append(result.Users, userOf(item))
		}
	}
	result.Count = len(result.Users)
	return result, nil
}

// userArgument reads and validates user_id after the wildcard check and before any secret or request.
func userArgument(resolved *config.Resolved, id string) error {
	if err := requireWildcard(resolved); err != nil {
		return err
	}
	if !validID(id) {
		return invalidRequest("user_id is not a valid identifier")
	}
	return nil
}

func invokeUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get user", "the validated arguments could not be read")
	}
	if err := userArgument(resolved, input.UserID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get user"
	var answer userJSON
	if err := client.get(ctx, op, "/workspaces/users/"+url.PathEscape(input.UserID), nil, &answer,
		maxResponseBytes); err != nil {
		return nil, err
	}
	if answer.ID != input.UserID {
		return nil, invalidResponse(op, "Excalidraw+ answered with a different user than requested")
	}
	user := userOf(answer)
	return &user, nil
}

type userUpdateBody struct {
	Name *string `json:"name,omitempty"`
	Role *string `json:"role,omitempty"`
}

func invokeUsersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		UserID string  `json:"user_id"`
		Name   *string `json:"name"`
		Role   *string `json:"role"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("update user", "the validated arguments could not be read")
	}
	if err := userArgument(resolved, input.UserID); err != nil {
		return nil, err
	}
	if input.Name == nil && input.Role == nil {
		return nil, invalidRequest("name or role is required")
	}
	if input.Name != nil && !validDisplayName(*input.Name) {
		return nil, invalidRequest("name must be 1 to 200 printable characters")
	}
	if input.Role != nil && !validRole(*input.Role) {
		return nil, invalidRequest("role must be member or admin")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "update user"
	var answer userJSON
	if err := client.send(ctx, op, http.MethodPatch, "/workspaces/users/"+url.PathEscape(input.UserID),
		userUpdateBody{Name: input.Name, Role: input.Role}, &answer); err != nil {
		return nil, err
	}
	if answer.ID != input.UserID {
		return nil, invalidResponse(op, "Excalidraw+ answered with a different user than requested"+changeUncertain)
	}
	user := userOf(answer)
	return &user, nil
}

// Removed is the result of removing a user.
type Removed struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed"`
}

func invokeUsersRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("remove user", "the validated arguments could not be read")
	}
	if err := userArgument(resolved, input.UserID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.send(ctx, "remove user", http.MethodDelete,
		"/workspaces/users/"+url.PathEscape(input.UserID), nil, nil); err != nil {
		return nil, err
	}
	return &Removed{ID: input.UserID, Removed: true}, nil
}
