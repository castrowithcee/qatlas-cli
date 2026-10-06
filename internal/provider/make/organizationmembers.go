package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.invite and make.organization.members work on the organization an organization-mode
// connection is bound to; the organization is always the connection's target. API (checked 2026-10-06
// against developers.make.com's published API reference, not a live account):
//   - GET /users/roles?category=organization&organizationId=, user:read, answering {"usersRoles":[{id, name,
//     identifier, managementType, ...}]}.
//   - GET /users?organizationId=&organizationRoleId=, user:read, answering {"users":[{id, name, email}]}.
//   - POST /organizations/{organizationId}/invite, organizations:write, body email, name, usersRoleId.
// Narrower readings: members reads the role list once and then the users of each role (the role of a user is
// the role of the request that found them); invite always uses the predefined organization role "member",
// resolved live and refused unless exactly one non-custom role matches; no teamsId, no note, no role argument.

const (
	organizationPeopleSensitivity = "make-organization-people-personal-data"
	maxOrganizationRoles          = 20
	maxOrganizationMembers        = 200
	maxEmailLength                = 254
	needOrganizationRoles         = "the user:read scope"
	needOrganizationInvite        = "the organizations:write and user:read scopes (user:read resolves the member role)"
	inviteUncertain               = "; this invitation may have been sent, read the members before repeating it"
)

var organizationPeopleReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: organizationPeopleSensitivity}

var organizationMembers = capability.Descriptor{
	ID: Provider + ".organization.members", Version: 1, Title: "List members of the bound Make organization",
	Description: "List the members of the organization this connection is bound to with user id, name, email " +
		"address, and organization role (id and name); takes no arguments, changes nothing. Email addresses " +
		"are personal data. Only on an organization connection. Needs the user:read scope",
	Tags: []string{"make", "organization", "members", "automation"}, Risk: organizationPeopleReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"members":{"type":"array","items":{"type":"object","properties":{"user_id":{"type":"integer"},` +
		`"name":{"type":"string"},"email":{"type":"string"},"role_id":{"type":"integer"},` +
		`"role_name":{"type":"string"}},"required":["user_id","role_id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["organization_id","members","count","truncated"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "members", Description: "user_id, name and email (untrusted, personal data), role_id, role_name (untrusted)"},
		{Name: "count", Description: "Members returned, at most " + strconv.Itoa(maxOrganizationMembers)},
		{Name: "truncated", Description: "True when roles or members beyond the caps may exist"}},
	Examples: []capability.Example{{Description: "List the organization's members", Arguments: json.RawMessage(`{}`)}},
}

var organizationInvite = capability.Descriptor{
	ID: Provider + ".organization.invite", Version: 1, Title: "Invite a person into the bound Make organization",
	Description: "Invite exactly one person by email address into the organization this connection is bound " +
		"to, always with the predefined organization role member, which is resolved live and refused when not " +
		"found exactly once; no team assignment and no role argument. Sends one request and never repeats it. " +
		"Only on an organization connection. Needs the organizations:write and user:read scopes",
	Tags: []string{"make", "organization", "invite", "automation"},
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: organizationPeopleSensitivity},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"email":{"type":"string","minLength":3,` +
		`"maxLength":254},"name":{"type":"string","minLength":1,"maxLength":128}},"required":["email","name"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"invited":{"type":"boolean"}},"required":["organization_id","invited"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "email", Required: true, Description: "Email address of the person, a plain address, at most 254 characters"},
		{Name: "name", Required: true, Description: "Name shown to the organization, 1 to 128 characters: " +
			"letters, numbers, spaces, and ' - . ( ) * + , @ _ /"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "invited", Description: "True when Make accepted the invitation"}},
	Examples: []capability.Example{{Description: "Invite a person",
		Arguments: json.RawMessage(`{"email":"person@example.com","name":"Pat Person"}`)}},
}

// OrganizationMember is one member with the organization role.
type OrganizationMember struct {
	UserID   int64  `json:"user_id"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	RoleID   int64  `json:"role_id"`
	RoleName string `json:"role_name,omitempty"`
}

// OrganizationMembers is the answer of organization.members.
type OrganizationMembers struct {
	OrganizationID int64                `json:"organization_id"`
	Members        []OrganizationMember `json:"members"`
	Count          int                  `json:"count"`
	Truncated      bool                 `json:"truncated"`
}

type organizationRoleJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Identifier     string `json:"identifier"`
	ManagementType string `json:"managementType"`
}

// organizationRoles reads the organization roles of the bound organization with one request.
func (c *Client) organizationRoles(ctx context.Context, op string) ([]organizationRoleJSON, error) {
	var page struct {
		Roles []organizationRoleJSON `json:"usersRoles"`
	}
	query := url.Values{"category": {"organization"}, "organizationId": {strconv.FormatInt(c.scope.orgID, 10)},
		"cols[]": {"id", "name", "identifier", "managementType"}}
	if err := c.get(ctx, op, "/users/roles", query, &page, needOrganizationRoles); err != nil {
		return nil, err
	}
	return page.Roles, nil
}

func invokeOrganizationMembers(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	const op = "list organization members"
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	roles, err := client.organizationRoles(ctx, op)
	if err != nil {
		return nil, err
	}
	result := &OrganizationMembers{OrganizationID: client.scope.orgID, Members: []OrganizationMember{}}
	if len(roles) > maxOrganizationRoles {
		roles, result.Truncated = roles[:maxOrganizationRoles], true
	}
	for _, role := range roles {
		if role.ID <= 0 {
			continue
		}
		remaining := maxOrganizationMembers - len(result.Members)
		if remaining <= 0 {
			result.Truncated = true
			break
		}
		var page struct {
			Users []struct {
				ID    int64  `json:"id"`
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"users"`
		}
		query := url.Values{"organizationId": {strconv.FormatInt(client.scope.orgID, 10)},
			"organizationRoleId": {strconv.FormatInt(role.ID, 10)}, "cols[]": {"id", "name", "email"},
			"pg[limit]": {strconv.Itoa(remaining)}}
		if err := client.get(ctx, op, "/users", query, &page, needOrganizationRoles); err != nil {
			return nil, err
		}
		if len(page.Users) >= remaining {
			result.Truncated = true
		}
		for _, u := range page.Users {
			if u.ID <= 0 || len(result.Members) >= maxOrganizationMembers {
				continue
			}
			result.Members = append(result.Members, OrganizationMember{UserID: u.ID, Name: bounded(u.Name),
				Email: bounded(u.Email), RoleID: role.ID, RoleName: bounded(role.Name)})
		}
	}
	result.Count = len(result.Members)
	return result, nil
}

// validInviteEmail accepts a plain ASCII address only: no display name, no comments, no whitespace.
func validInviteEmail(email string) bool {
	if len(email) < 3 || len(email) > maxEmailLength {
		return false
	}
	for i := 0; i < len(email); i++ {
		if email[i] <= 0x20 || email[i] >= 0x7f || strings.IndexByte("<>(),;:\\\"[]", email[i]) >= 0 {
			return false
		}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || strings.Count(email, "@") != 1 {
		return false
	}
	domain := email[strings.IndexByte(email, '@')+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

func invokeOrganizationInvite(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "invite organization member"
	var input struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validInviteEmail(input.Email) {
		return nil, invalidRequest("email must be a plain email address of at most " + strconv.Itoa(maxEmailLength) +
			" characters")
	}
	if !validOrganizationName(input.Name) {
		return nil, invalidRequest("name must be 1 to " + strconv.Itoa(maxOrganizationNameLength) +
			" characters: letters, numbers, spaces, and ' - . ( ) * + , @ _ /")
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	roles, err := client.organizationRoles(ctx, op)
	if err != nil {
		return nil, err
	}
	var roleID int64
	found := 0
	for _, r := range roles {
		if r.ID > 0 && (strings.EqualFold(r.Identifier, "member") || strings.EqualFold(r.Name, "member")) &&
			!strings.Contains(strings.ToLower(r.ManagementType), "custom") {
			roleID = r.ID
			found++
		}
	}
	if found != 1 {
		return nil, invalidRequest("the predefined organization role member was not found exactly once; nothing was sent")
	}
	body := map[string]any{"email": input.Email, "name": input.Name, "usersRoleId": roleID}
	path := "/organizations/" + strconv.FormatInt(client.scope.orgID, 10) + "/invite"
	if err := client.change(ctx, op, http.MethodPost, path, nil, body, nil, needOrganizationInvite,
		inviteUncertain); err != nil {
		return nil, err
	}
	return &struct {
		OrganizationID int64 `json:"organization_id"`
		Invited        bool  `json:"invited"`
	}{client.scope.orgID, true}, nil
}
