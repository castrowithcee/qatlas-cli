package penpot

import (
	"context"
	"encoding/json"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// membersSensitive is the class of the tools that handle the email addresses of members and invitees.
	membersSensitive = "penpot-members"
	maxMembersListed = 200
	// maxInvitations is Penpot's own limit of invitations in one request.
	maxInvitations = 25
	maxEmailLength = 254
)

const (
	roleSchema       = `{"type":"string","enum":["admin","editor","viewer"]}`
	inviteRoleSchema = `{"type":"string","enum":["editor","viewer"]}`
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

var invitationsCreate = capability.Descriptor{
	ID: Provider + ".invitations.create", Version: 1, Title: "Invite people to a Penpot team",
	Description: "Invite up to 25 email addresses to one bound team as editor or viewer. **Penpot sends an email to " +
		"every address.** Other roles are set afterwards with penpot.members.setrole. Addresses of existing members " +
		"are skipped by Penpot. " + membersNote,
	Tags: []string{"penpot", "invitations", "create", "teams", "design"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: membersSensitive},
	InputSchema: schemaOf(`"team_id":`+uuidSchema+`,"emails":{"type":"array","minItems":1,"maxItems":25,`+
		`"items":{"type":"string","minLength":3,"maxLength":254}},"role":`+inviteRoleSchema, `"team_id","emails","role"`),
	OutputSchema: schemaOf(`"team_id":{"type":"string"},"requested":{"type":"integer"},"invited":{"type":"integer"}`,
		`"team_id","requested","invited"`),
	Arguments: []capability.Argument{teamIDArgument,
		{Name: "emails", Required: true, Description: "1 to 25 plain email addresses; each receives a mail from Penpot"},
		{Name: "role", Required: true, Description: "Role of the invited people: editor or viewer"}},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Identifier of the team"},
		{Name: "requested", Description: "Number of distinct addresses sent to Penpot"},
		{Name: "invited", Description: "Number of invitations Penpot created; addresses are never repeated"},
	},
	Examples: []capability.Example{{Description: "Invite a colleague as editor",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","emails":["colleague@example.com"],"role":"editor"}`)}},
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

// MemberRoleSet, MemberRemoved, and InvitationsCreated are the answers of the three changes.
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

type InvitationsCreated struct {
	TeamID    string `json:"team_id"`
	Requested int    `json:"requested"`
	Invited   int    `json:"invited"`
}

type memberArguments struct {
	TeamID   string   `json:"team_id"`
	MemberID string   `json:"member_id"`
	Role     string   `json:"role"`
	Emails   []string `json:"emails"`
}

func readMember(op string, raw json.RawMessage) (memberArguments, error) {
	var input memberArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// selectTeamForTeamWideChange is selectTeam for a change of the team itself (members, invitations, the team's
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

// cleanEmails accepts 1 to 25 plain addresses (no display name, no control characters), lower-cased and
// without duplicates. No error quotes an address.
func cleanEmails(values []string) ([]string, error) {
	const reason = "emails must hold 1 to 25 plain email addresses"
	if len(values) == 0 || len(values) > maxInvitations {
		return nil, invalidRequest(reason)
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || len(value) > maxEmailLength || !utf8.ValidString(value) {
			return nil, invalidRequest(reason)
		}
		for _, r := range value {
			if unicode.IsControl(r) || unicode.IsSpace(r) || r == '<' || r == '>' || r == ',' || r == '"' {
				return nil, invalidRequest(reason)
			}
		}
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Name != "" || parsed.Address != value || strings.Count(value, "@") != 1 ||
			!strings.Contains(value[strings.Index(value, "@"):], ".") {
			return nil, invalidRequest(reason)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func invokeInvitationsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create invitations"
	input, err := readMember(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, err := selectTeamForTeamWideChange(resolved, input.TeamID)
	if err != nil {
		return nil, err
	}
	if err := checkRole(input.Role, "editor", "viewer"); err != nil {
		return nil, err
	}
	emails, err := cleanEmails(input.Emails)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	data, err := client.change(ctx, op, cmdCreateInvitation,
		map[string]any{"team-id": teamID, "emails": emails, "role": input.Role})
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok || answer["total"] == nil {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	invited := answer.integer("total")
	if invited < 0 || invited > maxInvitations {
		invited = 0
	}
	return &InvitationsCreated{TeamID: teamID, Requested: len(emails), Invited: int(invited)}, nil
}
