package n8n

import (
	"context"
	"encoding/json"
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

// memberDataSensitivity marks the personal data (email address, name) the member tools read and change.
const memberDataSensitivity = "n8n-project-members"

// maxMemberFieldLength bounds every string of a listed member, whatever n8n sends.
const maxMemberFieldLength = 256

// memberRoles is the fixed allow-list of project roles a caller may assign. project:personalOwner and the
// instance roles (global:*) are deliberately not on it.
var memberRoles = []string{"project:admin", "project:editor", "project:viewer"}

// memberPermissionMessage is the one message of a 403 on a members endpoint. n8n answers 403 for a missing
// Projects license, a missing API key scope (user:list, project:manageMembers), and a role that may not
// manage members, without a field this provider reads to tell them apart; the body is never read.
const memberPermissionMessage = "n8n refused this project members operation: license or role missing (the " +
	"Projects feature license, the API key's member scope, or its owner's role); Qatlas cannot tell which"

var memberReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: memberDataSensitivity}

func memberChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: memberDataSensitivity}
}

var memberRoleSchema = func() string {
	quoted := make([]string, len(memberRoles))
	for i, r := range memberRoles {
		quoted[i] = strconv.Quote(r)
	}
	return `{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}`
}()

var memberSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"email":{"type":"string"},"name":{"type":"string"},"role":{"type":"string"}},` +
	`"required":["id","email","name","role"],"additionalProperties":false}`

var memberIDArgument = capability.Argument{Name: "user_id",
	Description: "ID of an existing n8n user; no user is created or invited", Required: true}

var memberRoleArgument = capability.Argument{Name: "role",
	Description: "Project role: " + strings.Join(memberRoles, ", "), Required: true}

var membersList = capability.Descriptor{
	ID: Provider + ".projectmembers.list", Version: 1, Title: "List n8n project members",
	Description: "List the members of one project of the bound n8n instance with id, email, name, and role; " +
		"only projects inside the project allow-list, so a connection without one refuses it",
	Tags: []string{"n8n", "projects", "members", "list", "automation"}, Risk: memberReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"required":["project_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"members":{"type":"array","items":` + memberSchema + `},"cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"},"truncated":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["members","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument,
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Members per page, 1 to 250; 100 when omitted"}},
	Fields: []capability.Field{
		{Name: "id", Description: "User identifier"},
		{Name: "email", Description: "Email address, personal data, untrusted"},
		{Name: "name", Description: "First and last name, personal data, untrusted"},
		{Name: "role", Description: "Project role as n8n reports it"},
		{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		{Name: "has_more", Description: "True when a further page remains"},
		{Name: "truncated", Description: "True when n8n sent more members than limit and the rest were dropped"},
		{Name: "count", Description: "Number of members on this page"}},
	Examples: []capability.Example{{Description: "List the members of a project",
		Arguments: json.RawMessage(`{"project_id":"VmwOO9HeTEj20kxM"}`)}},
}

var membersAdd = capability.Descriptor{
	ID: Provider + ".projectmembers.add", Version: 1, Title: "Add an n8n project member",
	Description: "Give one existing n8n user a role in one project of the bound instance. It creates and " +
		"invites no user; a repeated call is sent again. Only projects inside the project allow-list",
	Tags: []string{"n8n", "projects", "members", "add", "automation"},
	Risk: memberChangeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"user_id":` + targetIDSchema + `,"role":` + memberRoleSchema + `},` +
		`"required":["project_id","user_id","role"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":{"type":"string"},` +
		`"user_id":{"type":"string"},"role":{"type":"string"},"added":{"type":"boolean"}},` +
		`"required":["project_id","user_id","role","added"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, memberIDArgument, memberRoleArgument},
	Fields: []capability.Field{{Name: "project_id", Description: "The project"},
		{Name: "user_id", Description: "The user"}, {Name: "role", Description: "The role that was sent"},
		{Name: "added", Description: "True when n8n accepted the change"}},
	Examples: []capability.Example{{Description: "Add a viewer",
		Arguments: json.RawMessage(`{"project_id":"VmwOO9HeTEj20kxM","user_id":"u-1234","role":"project:viewer"}`)}},
}

var membersSetRole = capability.Descriptor{
	ID: Provider + ".projectmembers.setrole", Version: 1, Title: "Change an n8n project member's role",
	Description: "Set the role of one existing member of one project of the bound instance. Only projects " +
		"inside the project allow-list",
	Tags: []string{"n8n", "projects", "members", "role", "automation"},
	Risk: memberChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: membersAdd.InputSchema,
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":{"type":"string"},` +
		`"user_id":{"type":"string"},"role":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["project_id","user_id","role","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, memberIDArgument, memberRoleArgument},
	Fields: []capability.Field{{Name: "project_id", Description: "The project"},
		{Name: "user_id", Description: "The user"}, {Name: "role", Description: "The role that was sent"},
		{Name: "updated", Description: "True when n8n accepted the change"}},
	Examples: membersAdd.Examples,
}

var membersRemove = capability.Descriptor{
	ID: Provider + ".projectmembers.remove", Version: 1, Title: "Remove an n8n project member",
	Description: "Remove one member's access to one project of the bound instance; the user itself is kept. " +
		"Only projects inside the project allow-list",
	Tags: []string{"n8n", "projects", "members", "remove", "automation"},
	Risk: memberChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"user_id":` + targetIDSchema + `},"required":["project_id","user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":{"type":"string"},` +
		`"user_id":{"type":"string"},"removed":{"type":"boolean"}},` +
		`"required":["project_id","user_id","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, memberIDArgument},
	Fields: []capability.Field{{Name: "project_id", Description: "The project"},
		{Name: "user_id", Description: "The user"},
		{Name: "removed", Description: "True when n8n accepted the removal"}},
	Examples: []capability.Example{{Description: "Remove a member",
		Arguments: json.RawMessage(`{"project_id":"VmwOO9HeTEj20kxM","user_id":"u-1234"}`)}},
}

// Member is the stable Qatlas view of one project member.
type Member struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// MembersPage is one page of project members.
type MembersPage struct {
	Members   []Member `json:"members"`
	Cursor    string   `json:"cursor,omitempty"`
	HasMore   bool     `json:"has_more"`
	Truncated bool     `json:"truncated,omitempty"`
	Count     int      `json:"count"`
}

// MemberAdded, MemberRoleSet, and MemberRemoved are what the changing member tools report.
type MemberAdded struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Added     bool   `json:"added"`
}

type MemberRoleSet struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Updated   bool   `json:"updated"`
}

type MemberRemoved struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Removed   bool   `json:"removed"`
}

type memberJSON struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
	Role      *string `json:"role"`
}

type membersPageJSON struct {
	Data       []memberJSON `json:"data"`
	NextCursor *string      `json:"nextCursor"`
}

// memberError replaces the generic 403 message with the neutral license-or-role message.
func memberError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = memberPermissionMessage
	}
	return err
}

func validMemberRole(role string) error {
	for _, allowed := range memberRoles {
		if role == allowed {
			return nil
		}
	}
	return invalidRequest("role must be one of " + strings.Join(memberRoles, ", "))
}

// selectMember decides locally, before any secret or request, whether this connection may touch the members
// of the project, with the same narrow reading as projects.update: a project allow-list is required, the
// project must be on it, and a workflow allow-list refuses everything. It then validates user ID and role.
func selectMember(resolved *config.Resolved, projectID, userID, role string, needUser, needRole bool) error {
	if err := selectProjectMutation(resolved, projectID); err != nil {
		return err
	}
	if needUser && !validTargetID(userID) {
		return invalidRequest("user_id must be a usable n8n identifier")
	}
	if needRole {
		return validMemberRole(role)
	}
	return nil
}

func boundedField(value string) string {
	if utf8.RuneCountInString(value) > maxMemberFieldLength {
		value = string([]rune(value)[:maxMemberFieldLength])
	}
	return value
}

type memberArguments struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Cursor    string `json:"cursor"`
	Limit     int    `json:"limit"`
}

func readMemberArguments(op string, raw json.RawMessage) (memberArguments, error) {
	var input memberArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeMembersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readMemberArguments("list project members", raw)
	if err != nil {
		return nil, err
	}
	if err := selectMember(resolved, input.ProjectID, "", "", false, false); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListMembers(ctx, input.ProjectID, input.Cursor, limit)
	return page, memberError(err)
}

// ListMembers reads one page of GET /projects/{id}/users and keeps id, email, name, and role.
func (c *Client) ListMembers(ctx context.Context, projectID, cursor string, limit int) (*MembersPage, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page membersPageJSON
	if err := c.get(ctx, "list project members", "/projects/"+url.PathEscape(projectID)+"/users", query, &page,
		maxResponseBytes); err != nil {
		return nil, err
	}
	data := page.Data
	truncated := false
	if len(data) > limit {
		data, truncated = data[:limit], true
	}
	members := make([]Member, 0, len(data))
	for _, m := range data {
		var parts []string
		for _, p := range []*string{m.FirstName, m.LastName} {
			if p != nil && strings.TrimSpace(*p) != "" {
				parts = append(parts, strings.TrimSpace(*p))
			}
		}
		role := ""
		if m.Role != nil {
			role = *m.Role
		}
		members = append(members, Member{ID: boundedField(m.ID), Email: boundedField(m.Email),
			Name: boundedField(strings.Join(parts, " ")), Role: boundedField(role)})
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return &MembersPage{Members: members, Cursor: next, HasMore: next != "", Truncated: truncated,
		Count: len(members)}, nil
}

func invokeMembersAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readMemberArguments("add project member", raw)
	if err != nil {
		return nil, err
	}
	if err := selectMember(resolved, input.ProjectID, input.UserID, input.Role, true, true); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// One POST with exactly one relation; n8n's own answer carries nothing to re-read.
	err = client.change(ctx, "add project member", http.MethodPost, "/projects/"+url.PathEscape(input.ProjectID)+"/users",
		nil, map[string]any{"relations": []map[string]string{{"userId": input.UserID, "role": input.Role}}}, nil,
		maxResponseBytes)
	if err != nil {
		return nil, memberError(err)
	}
	return &MemberAdded{ProjectID: input.ProjectID, UserID: input.UserID, Role: input.Role, Added: true}, nil
}

func invokeMembersSetRole(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readMemberArguments("set project member role", raw)
	if err != nil {
		return nil, err
	}
	if err := selectMember(resolved, input.ProjectID, input.UserID, input.Role, true, true); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	err = client.change(ctx, "set project member role", http.MethodPatch,
		"/projects/"+url.PathEscape(input.ProjectID)+"/users/"+url.PathEscape(input.UserID), nil,
		map[string]any{"role": input.Role}, nil, maxResponseBytes)
	if err != nil {
		return nil, memberError(err)
	}
	return &MemberRoleSet{ProjectID: input.ProjectID, UserID: input.UserID, Role: input.Role, Updated: true}, nil
}

func invokeMembersRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readMemberArguments("remove project member", raw)
	if err != nil {
		return nil, err
	}
	if err := selectMember(resolved, input.ProjectID, input.UserID, "", true, false); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	err = client.change(ctx, "remove project member", http.MethodDelete,
		"/projects/"+url.PathEscape(input.ProjectID)+"/users/"+url.PathEscape(input.UserID), nil, nil, nil,
		maxResponseBytes)
	if err != nil {
		return nil, memberError(err)
	}
	return &MemberRemoved{ProjectID: input.ProjectID, UserID: input.UserID, Removed: true}, nil
}
