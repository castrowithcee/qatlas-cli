package penpot

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// membersSensitive is the class of the tools that handle the email addresses of members.
	membersSensitive = "penpot-members"
	maxMembersListed = 200
)

const (
	roleSchema = `{"type":"string","enum":["admin","editor","viewer"]}`
)

const membersNote = "Members belong to the whole team, so a connection with a project allow-list refuses every change. " +
	"Emails are personal data and untrusted provider data. The change is sent once and never repeated"

var memberIDArgument = capability.Argument{Name: "member_id", Required: true,
	Description: "Member identifier (id) from penpot.members.list of the same team"}

var membersList = capability.Descriptor{
	ID: Provider + ".members.list", Version: 1, Title: "List Penpot team members",
	Description: "List the members of one bound team with id, email, role, and activity; emails are personal data " +
		"and untrusted provider data",
	Tags: []string{"penpot", "members", "list", "teams", "design"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: membersSensitive},
	InputSchema: schemaOf(`"team_id":`+uuidSchema, `"team_id"`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"},"members":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"email":{"type":"string"},"role":{"type":"string"},` +
		`"is_active":{"type":"boolean"}},"required":["id","role"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["team_id","members","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Identifier of the team"},
		{Name: "members", Description: "Members with id (used as member_id), email, role (owner, admin, editor, or viewer), and is_active"},
		{Name: "count", Description: "Number of members in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 200 members"},
	},
	Examples: []capability.Example{{Description: "List the members of a team",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001"}`)}},
}

var membersSetRole = capability.Descriptor{
	ID: Provider + ".members.setrole", Version: 1, Title: "Change a Penpot team member's role",
	Description: "Set the role of one member of a bound team to admin, editor, or viewer. The owner role is not " +
		"offered, so ownership cannot be transferred; Penpot refuses a change of the owner. " + membersNote,
	Tags: []string{"penpot", "members", "role", "teams", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: metadataSensitive},
	InputSchema: schemaOf(`"team_id":`+uuidSchema+`,"member_id":`+uuidSchema+`,"role":`+roleSchema,
		`"team_id","member_id","role"`),
	OutputSchema: schemaOf(`"updated":{"type":"boolean"},"team_id":{"type":"string"},"member_id":{"type":"string"},`+
		`"role":{"type":"string"}`, `"updated","team_id","member_id","role"`),
	Arguments: []capability.Argument{teamIDArgument, memberIDArgument,
		{Name: "role", Required: true, Description: "New role: admin, editor, or viewer"}},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Penpot accepted the change"},
		{Name: "team_id", Description: "Identifier of the team"},
		{Name: "member_id", Description: "Identifier of the member"},
		{Name: "role", Description: "The role that was set"},
	},
	Examples: []capability.Example{{Description: "Make a member a viewer",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","member_id":"00000000-0000-0000-0000-000000000002","role":"viewer"}`)}},
}

var membersRemove = capability.Descriptor{
	ID: Provider + ".members.remove", Version: 1, Title: "Remove a Penpot team member",
	Description: "Remove one member from a bound team. Penpot refuses to remove the token's own account and, for " +
		"an admin, the owner. " + membersNote,
	Tags: []string{"penpot", "members", "remove", "teams", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:         manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema:  schemaOf(`"team_id":`+uuidSchema+`,"member_id":`+uuidSchema, `"team_id","member_id"`),
	OutputSchema: schemaOf(`"removed":{"type":"boolean"},"team_id":{"type":"string"},"member_id":{"type":"string"}`, `"removed","team_id","member_id"`),
	Arguments:    []capability.Argument{teamIDArgument, memberIDArgument},
	Fields: []capability.Field{
		{Name: "removed", Description: "True when Penpot accepted the removal"},
		{Name: "team_id", Description: "Identifier of the team"},
		{Name: "member_id", Description: "Identifier of the member"},
	},
	Examples: []capability.Example{{Description: "Remove a member",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","member_id":"00000000-0000-0000-0000-000000000002"}`)}},
}

// Member is one member of a bound team; the name and photo of the profile are not returned.
type Member struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

// MembersResult is the answer of members.list.
type MembersResult struct {
	TeamID    string   `json:"team_id"`
	Members   []Member `json:"members"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated"`
}

// MemberRoleSet and MemberRemoved are the answers of the two member changes.
type MemberRoleSet struct {
	Updated  bool   `json:"updated"`
	TeamID   string `json:"team_id"`
	MemberID string `json:"member_id"`
	Role     string `json:"role"`
}

type MemberRemoved struct {
	Removed  bool   `json:"removed"`
	TeamID   string `json:"team_id"`
	MemberID string `json:"member_id"`
}

type memberArguments struct {
	TeamID   string `json:"team_id"`
	MemberID string `json:"member_id"`
	Role     string `json:"role"`
}

func readMember(op string, raw json.RawMessage) (memberArguments, error) {
	var input memberArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// selectTeamForTeamWideChange is selectTeam for a change of the team itself (members, the team's
// existence): such a change reaches beyond any project, so a connection narrowed by a project allow-list refuses
// it. The refusal comes before any secret.
func selectTeamForTeamWideChange(resolved *config.Resolved, raw string) (string, error) {
	id, err := selectTeam(resolved, raw)
	if err != nil {
		return "", err
	}
	if err := refuseProjectAllowList(resolved); err != nil {
		return "", err
	}
	return id, nil
}

func refuseProjectAllowList(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.projects) > 0 {
		return invalidRequest("a connection with a project allow-list cannot change teams or their members, which are not part of a project")
	}
	return nil
}

func memberTargets(resolved *config.Resolved, input memberArguments) (string, string, error) {
	teamID, err := selectTeamForTeamWideChange(resolved, input.TeamID)
	if err != nil {
		return "", "", err
	}
	memberID, ok := parseUUID(input.MemberID)
	if !ok {
		return "", "", invalidRequest("member_id must be a UUID")
	}
	return teamID, memberID, nil
}

// memberRole reads the role of a team member row from its flags.
func memberRole(entry obj) string {
	switch {
	case entry.boolean("isowner"):
		return "owner"
	case entry.boolean("isadmin"):
		return "admin"
	case entry.boolean("canedit"):
		return "editor"
	}
	return "viewer"
}

func (c *Client) teamMembers(ctx context.Context, op, teamID string) ([]Member, bool, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdTeamMembers, map[string]any{"team-id": teamID}, &raw); err != nil {
		return nil, false, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, false, invalidResponse(op, "Penpot returned an invalid response")
	}
	members, truncated := []Member{}, false
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if len(members) >= maxMembersListed {
			truncated = true
			break
		}
		members = append(members, Member{ID: id, Email: entry.str("email"), Role: memberRole(entry),
			IsActive: entry.boolean("isactive")})
	}
	return members, truncated, nil
}

func invokeMembersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list members"
	input, err := readMember(op, raw)
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
	members, truncated, err := client.teamMembers(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	return &MembersResult{TeamID: teamID, Members: members, Count: len(members), Truncated: truncated}, nil
}

func checkRole(role string, allowed ...string) error {
	for _, item := range allowed {
		if role == item {
			return nil
		}
	}
	return invalidRequest("role must be " + strings.Join(allowed, ", or "))
}

func invokeMembersSetRole(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set member role"
	input, err := readMember(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, memberID, err := memberTargets(resolved, input)
	if err != nil {
		return nil, err
	}
	if err := checkRole(input.Role, "admin", "editor", "viewer"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"team-id": teamID, "member-id": memberID, "role": input.Role}
	if _, err := client.change(ctx, op, cmdSetMemberRole, params); err != nil {
		return nil, err
	}
	return &MemberRoleSet{Updated: true, TeamID: teamID, MemberID: memberID, Role: input.Role}, nil
}

func invokeMembersRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "remove member"
	input, err := readMember(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, memberID, err := memberTargets(resolved, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdDeleteMember, map[string]any{"team-id": teamID, "member-id": memberID}); err != nil {
		return nil, err
	}
	return &MemberRemoved{Removed: true, TeamID: teamID, MemberID: memberID}, nil
}
