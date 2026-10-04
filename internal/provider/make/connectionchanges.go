package makeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file rename and delete one connection of the bound team and read and set the
// members of its access list. Every one reads the connection first and binds it back to the bound team.
// API (checked 2026-10-04 against developers.make.com's published API reference, not a live account):
//   - PATCH /connections/{id} with {"name"} (max 128 characters), connections:write, answering {"connection"}.
//   - DELETE /connections/{id}, connections:write, answering {"connection":id}; "confirmed=true" is required
//     when a scenario includes the connection, otherwise Make refuses and deletes nothing.
//   - GET /users/{userId}/user-team-roles/{teamId}, user:read, answering {"userTeamRole":{userId,teamId,...}};
//     it proves before access.set that the user belongs to the bound team.
//   - GET /teams/{teamId}/connections/{id}/access-list, connections:read, answering {"accessList":[...]};
//     POST /teams/{teamId}/connections/{id}/access-list/users with {"userId","role"} and PATCH
//     .../users/{userId} with {"role"}, connections:write, each answering {"member":{...}}. Roles are
//     entity:entity-admin and entity:entity-member. All need the locked connections feature enabled for the
//     organization (otherwise 400 IM903) and the entity manage permission to change.
// Removing an access-list member is deliberately not offered. Operation IDs have three segments, so the
// access-list tools are make.connectionaccess.list and make.connectionaccess.set.

const (
	// maxConnectionNameLength is the documented bound of a connection's name.
	maxConnectionNameLength = 128
	// maxAccessMembers bounds the members one access-list answer returns.
	maxAccessMembers = 200
	// needConnectionsAccessRead and needConnectionsAccessWrite name what a 403 on an access list lacks.
	needConnectionsAccessRead = "the connections:read scope and, in Make, the right to view the connection's " +
		"access list (locked connections must be enabled for the organization)"
	needConnectionsAccessWrite = "the connections:write scope (and connections:read, which binds the connection, " +
		"and user:read, which proves the user belongs to the team) and, in Make, the entity manage permission on the connection's access list (locked connections must " +
		"be enabled for the organization)"
	needUserRead          = "the user:read scope"
	needConnectionsDelete = "the connections:write scope (and connections:read, which binds the connection) " +
		"and, for a connection restricted to its access list, a place on that list with permission to manage it"

	roleAdminAPI  = "entity:entity-admin"
	roleMemberAPI = "entity:entity-member"
)

var connectionNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxConnectionNameLength) + `}`

var connectionNameArgument = capability.Argument{Name: "name", Required: true,
	Description: "New connection name, 1 to " + strconv.Itoa(maxConnectionNameLength) +
		" characters, without control characters"}

var userIDSchema = idSchema

var accessMemberSchema = `{"type":"object","properties":{"user_id":{"type":"integer"},` +
	`"role":{"type":"string"}},"required":["user_id","role"],"additionalProperties":false}`

var affectedScenariosSchema = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
	`"name":{"type":"string"}},"required":["id"],"additionalProperties":false}}`

var connectionsRename = capability.Descriptor{
	ID: Provider + ".connections.rename", Version: 1, Title: "Rename a Make connection",
	Description: "Rename one connection of the bound team; nothing else about it changes. Needs the " +
		"connections:read and connections:write scopes",
	Tags: []string{"make", "connections", "rename", "automation"}, Risk: connectionChangeRisk(
		capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `,` +
		`"name":` + connectionNameSchema + `},"required":["connection_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(connectionSummarySchema),
	Arguments:    []capability.Argument{connectionIDArgument, connectionNameArgument},
	Fields:       connectionSummaryFields,
	Examples: []capability.Example{{Description: "Rename a connection",
		Arguments: json.RawMessage(`{"connection_id":1,"name":"Shop Google"}`)}},
}

var connectionsDelete = capability.Descriptor{
	ID: Provider + ".connections.delete", Version: 1, Title: "Delete a Make connection",
	Description: "Delete one connection of the bound team for good. A scenario that includes the connection " +
		"stops working without it, so Make refuses the deletion until it is confirmed: without " +
		"confirm_scenarios_affected this tool sends no confirmation and, when Make refuses and names the " +
		"scenarios, returns them without deleting or repeating anything; with it true, the connection is " +
		"deleted and the scenarios using it break. Offered only when a connection's tools list names it, in " +
		"no profile",
	Tags: []string{"make", "connections", "delete", "automation"}, Risk: connectionChangeRisk(
		capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `,` +
		`"confirm_scenarios_affected":{"type":"boolean"}},"required":["connection_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"deleted":{"type":"boolean"},"confirmation_required":{"type":"boolean"},` +
		`"scenarios":` + affectedScenariosSchema + `},"required":["connection_id","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument,
		{Name: "confirm_scenarios_affected", Description: "Set true to delete the connection even though " +
			"scenarios use it; they stop working. When omitted, nothing is deleted while scenarios use it"}},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection that was addressed"},
		{Name: "deleted", Description: "True when Make deleted the connection"},
		{Name: "confirmation_required", Description: "True when Make refused the deletion because scenarios " +
			"use the connection; nothing was deleted and nothing was repeated"},
		{Name: "scenarios", Description: "Scenarios Make's refusal names, at most " +
			strconv.Itoa(maxAffectedScenarios) + "; ids and names are untrusted data, names bounded"},
	},
	Examples: []capability.Example{{Description: "Delete an unused connection",
		Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

var connectionsAccessList = capability.Descriptor{
	ID: Provider + ".connectionaccess.list", Version: 1, Title: "List a Make connection's access list",
	Description: "List the members of one locked connection's access list in the bound team as user ids and " +
		"roles only, never a name or email. Make answers only when its locked connections feature is " +
		"enabled for the organization. Needs the connections:read scope",
	Tags: []string{"make", "connectionaccess", "list", "automation"}, Risk: connectionsReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"members":{"type":"array","items":` + accessMemberSchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["connection_id","team_id","members","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection the access list belongs to"},
		{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
		{Name: "members", Description: "Users on the list as user_id and role (admin, member, or the role " +
			"as Make names it, untrusted data); other principal kinds are skipped"},
		{Name: "count", Description: "Number of members returned"},
		{Name: "truncated", Description: "True when more than " + strconv.Itoa(maxAccessMembers) +
			" members were reported and the rest was dropped"},
	},
	Examples: []capability.Example{{Description: "Read one connection's access list",
		Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

var connectionsAccessSet = capability.Descriptor{
	ID: Provider + ".connectionaccess.set", Version: 1, Title: "Set a user's role on a Make connection",
	Description: "Give one user of the bound team a role on one locked connection's access list: the user is " +
		"added when not on the list and changed when on it with another role, with exactly one changing " +
		"request after the reads; the rest of the list stays as it is, and no member is removed. Granting " +
		"admin lets that user manage the connection and its access list. Make answers only when its " +
		"locked connections feature is enabled and the token's user may manage the list. The user must " +
		"belong to the bound team, which is read from Make first; otherwise nothing is changed. Needs the " +
		"connections:read, connections:write, and user:read scopes",
	Tags: []string{"make", "connectionaccess", "set", "automation"}, Risk: connectionChangeRisk(
		capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `,` +
		`"user_id":` + userIDSchema + `,"role":{"type":"string","enum":["admin","member"]}},` +
		`"required":["connection_id","user_id","role"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"user_id":{"type":"integer"},"role":{"type":"string"},` +
		`"changed":{"type":"boolean"}},"required":["connection_id","team_id","user_id","role","changed"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument,
		{Name: "user_id", Required: true, Description: "Make user id; must be a member of the bound team, which is verified live before " +
			"anything is changed"},
		{Name: "role", Required: true, Description: "admin (entity admin) or member (entity member)"}},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection that was addressed"},
		{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
		{Name: "user_id", Description: "The user whose role was set"},
		{Name: "role", Description: "The role the user now holds, as Make reports it after the change"},
		{Name: "changed", Description: "False when the user already held the role and no change was sent"},
	},
	Examples: []capability.Example{{Description: "Make a user a member of a connection",
		Arguments: json.RawMessage(`{"connection_id":1,"user_id":7,"role":"member"}`)}},
}

func connectionChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: connectionsSensitivity}
}

func validConnectionName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxConnectionNameLength ||
		!utf8.ValidString(name) || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return invalidRequest("name must be 1 to " + strconv.Itoa(maxConnectionNameLength) +
			" characters without control characters")
	}
	return nil
}

func connectionPath(id int64) string { return "/connections/" + strconv.FormatInt(id, 10) }

func accessListPath(teamID, id int64) string {
	return "/teams/" + strconv.FormatInt(teamID, 10) + connectionPath(id) + "/access-list"
}

type connectionRenameArguments struct {
	ConnectionID int64  `json:"connection_id"`
	Name         string `json:"name"`
}

func invokeConnectionsRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename connection"
	var input connectionRenameArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validConnectionName(input.Name); err != nil {
		return nil, err
	}
	client, id, _, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodPatch, connectionPath(id), nil,
		map[string]any{"name": input.Name}, nil, needConnectionsWrite, uncertain); err != nil {
		return nil, err
	}
	// Make's change answer is read for nothing: the connection is read again, with the same allow-list and
	// team binding as every other read, so the result is what Make now holds.
	var wrapper struct {
		Connection connectionJSON `json:"connection"`
	}
	if err := client.get(ctx, op, connectionPath(id), colsQuery(url.Values{}), &wrapper, needConnectionsRead); err != nil {
		return nil, err
	}
	if wrapper.Connection.ID != id {
		return nil, invalidResponse(op, "Make did not report the changed connection"+uncertain)
	}
	if !client.allowsConnection(wrapper.Connection) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; "+
			"the change already took effect")
	}
	return connectionSummaryOf(wrapper.Connection), nil
}

type connectionDeleteArguments struct {
	ConnectionID             int64 `json:"connection_id"`
	ConfirmScenariosAffected bool  `json:"confirm_scenarios_affected"`
}

// ConnectionDeletion is the answer of connections.delete: deleted, or refused by Make until scenarios are
// confirmed.
type ConnectionDeletion struct {
	ConnectionID         int64              `json:"connection_id"`
	Deleted              bool               `json:"deleted"`
	ConfirmationRequired bool               `json:"confirmation_required,omitempty"`
	Scenarios            []AffectedScenario `json:"scenarios,omitempty"`
}

func invokeConnectionsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete connection"
	var input connectionDeleteArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, id, _, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	var query url.Values
	if input.ConfirmScenariosAffected {
		query = url.Values{"confirmed": {"true"}}
	}
	client.wantRefusal = !input.ConfirmScenariosAffected
	var answer struct {
		Connection int64 `json:"connection"`
	}
	err = client.change(ctx, op, http.MethodDelete, connectionPath(id), query, nil, &answer,
		needConnectionsDelete, uncertain)
	client.wantRefusal = false
	if err != nil {
		scenarios := client.refusedConnectionScenarios()
		if client.refusalStatus == 0 || len(scenarios) == 0 {
			return nil, err
		}
		return &ConnectionDeletion{ConnectionID: id, ConfirmationRequired: true, Scenarios: scenarios}, nil
	}
	if answer.Connection != 0 && answer.Connection != id {
		return nil, invalidResponse(op, "Make reported a different connection than the one deleted"+uncertain)
	}
	return &ConnectionDeletion{ConnectionID: id, Deleted: true}, nil
}

// refusedConnectionScenarios reads the scenarios a refused deletion's body lists, tolerating the two places
// Make uses for such a list. Names are bounded and the list is capped.
func (c *Client) refusedConnectionScenarios() []AffectedScenario {
	type scenarioRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	var body struct {
		Scenarios []scenarioRef `json:"scenarios"`
		Detail    struct {
			Scenarios []scenarioRef `json:"scenarios"`
		} `json:"detail"`
	}
	_ = json.Unmarshal(c.refusal, &body)
	seen := map[int64]bool{}
	var out []AffectedScenario
	for _, ref := range append(body.Scenarios, body.Detail.Scenarios...) {
		if ref.ID <= 0 || seen[ref.ID] || len(out) >= maxAffectedScenarios {
			continue
		}
		seen[ref.ID] = true
		name := ref.Name
		if len(name) > maxAffectedNameLength {
			name = name[:maxAffectedNameLength]
		}
		out = append(out, AffectedScenario{ID: ref.ID, Name: strings.ToValidUTF8(name, "")})
	}
	return out
}

type accessMemberJSON struct {
	MembershipType string `json:"membershipType"`
	MembershipID   int64  `json:"membershipId"`
	Role           string `json:"role"`
}

// AccessMember is one user of an access list; name and email are never returned.
type AccessMember struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

func accessRoleName(apiRole string) string {
	switch apiRole {
	case roleAdminAPI:
		return "admin"
	case roleMemberAPI:
		return "member"
	}
	return boundText(apiRole)
}

// ConnectionAccessList is one capped read of an access list.
type ConnectionAccessList struct {
	ConnectionID int64          `json:"connection_id"`
	TeamID       int64          `json:"team_id"`
	Members      []AccessMember `json:"members"`
	Count        int            `json:"count"`
	Truncated    bool           `json:"truncated"`
}

func (c *Client) readAccessList(ctx context.Context, op string, id int64, need string) ([]accessMemberJSON, error) {
	var answer struct {
		AccessList []accessMemberJSON `json:"accessList"`
	}
	if err := c.get(ctx, op, accessListPath(c.scope.teamID, id), nil, &answer, need); err != nil {
		return nil, err
	}
	return answer.AccessList, nil
}

func invokeConnectionsAccessList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list connection access"
	client, id, conn, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	list, err := client.readAccessList(ctx, op, id, needConnectionsAccessRead)
	if err != nil {
		return nil, err
	}
	result := &ConnectionAccessList{ConnectionID: id, TeamID: conn.TeamID, Members: make([]AccessMember, 0, len(list))}
	for _, member := range list {
		if member.MembershipType != "user" || member.MembershipID <= 0 {
			continue
		}
		if len(result.Members) >= maxAccessMembers {
			result.Truncated = true
			break
		}
		result.Members = append(result.Members, AccessMember{UserID: member.MembershipID, Role: accessRoleName(member.Role)})
	}
	result.Count = len(result.Members)
	return result, nil
}

type connectionAccessSetArguments struct {
	ConnectionID int64  `json:"connection_id"`
	UserID       int64  `json:"user_id"`
	Role         string `json:"role"`
}

// ConnectionAccessChange is the outcome of one access.set.
type ConnectionAccessChange struct {
	ConnectionID int64  `json:"connection_id"`
	TeamID       int64  `json:"team_id"`
	UserID       int64  `json:"user_id"`
	Role         string `json:"role"`
	Changed      bool   `json:"changed"`
}

func invokeConnectionsAccessSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set connection access"
	var input connectionAccessSetArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	var apiRole string
	switch input.Role {
	case "admin":
		apiRole = roleAdminAPI
	case "member":
		apiRole = roleMemberAPI
	default:
		return nil, invalidRequest("role must be admin or member")
	}
	if input.UserID <= 0 {
		return nil, invalidRequest("user_id must be a positive integer")
	}
	client, id, conn, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	if err := client.requireTeamMember(ctx, op, input.UserID); err != nil {
		return nil, err
	}
	list, err := client.readAccessList(ctx, op, id, needConnectionsAccessWrite)
	if err != nil {
		return nil, err
	}
	present, current := false, ""
	for _, member := range list {
		if member.MembershipType == "user" && member.MembershipID == input.UserID {
			present, current = true, member.Role
			break
		}
	}
	if present && current == apiRole {
		return &ConnectionAccessChange{ConnectionID: id, TeamID: conn.TeamID, UserID: input.UserID,
			Role: input.Role}, nil
	}
	var answer struct {
		Member accessMemberJSON `json:"member"`
	}
	base := accessListPath(client.scope.teamID, id) + "/users"
	if present {
		err = client.change(ctx, op, http.MethodPatch, base+"/"+strconv.FormatInt(input.UserID, 10), nil,
			map[string]any{"role": apiRole}, &answer, needConnectionsAccessWrite, uncertain)
	} else {
		err = client.change(ctx, op, http.MethodPost, base, nil,
			map[string]any{"userId": input.UserID, "role": apiRole}, &answer, needConnectionsAccessWrite, uncertain)
	}
	if err != nil {
		return nil, err
	}
	if answer.Member.MembershipID != input.UserID || answer.Member.Role != apiRole {
		return nil, invalidResponse(op, "Make did not report the requested role after the change"+uncertain)
	}
	return &ConnectionAccessChange{ConnectionID: id, TeamID: conn.TeamID, UserID: input.UserID,
		Role: accessRoleName(answer.Member.Role), Changed: true}, nil
}

// requireTeamMember proves, with one read, that the user belongs to the bound team, before any change. A user
// Make does not report as a member of that team, including a missing one, is refused without naming anything.
func (c *Client) requireTeamMember(ctx context.Context, op string, userID int64) error {
	var answer struct {
		Role struct {
			UserID int64 `json:"userId"`
			TeamID int64 `json:"teamId"`
		} `json:"userTeamRole"`
	}
	path := "/users/" + strconv.FormatInt(userID, 10) + "/user-team-roles/" + strconv.FormatInt(c.scope.teamID, 10)
	err := c.get(ctx, op, path, nil, &answer, needUserRead)
	if err != nil {
		if errorClassOf(err) == provider.ClassNotFound {
			return invalidRequest(errNotTeamMember)
		}
		return err
	}
	if answer.Role.UserID != userID || !c.scope.allowsTeam(answer.Role.TeamID) {
		return invalidRequest(errNotTeamMember)
	}
	return nil
}

const errNotTeamMember = "user_id is not a member of the team of this connection"

func errorClassOf(err error) provider.Class {
	var perr *provider.Error
	if errors.As(err, &perr) {
		return perr.Class
	}
	return ""
}
