package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.members works on the organization an organization-mode connection is bound to; the organization is always the connection's target. API (checked 2026-10-06
// against developers.make.com's published API reference, not a live account):
//   - GET /users/roles?category=organization&organizationId=, user:read, answering {"usersRoles":[{id, name,
//     identifier, managementType, ...}]}.
//   - GET /users?organizationId=&organizationRoleId=, user:read, answering {"users":[{id, name, email}]}.
// Narrower reading: members reads the role list once and then the users of each role (the role of a user is
// the role of the request that found them).

const (
	organizationPeopleSensitivity = "make-organization-people-personal-data"
	maxOrganizationRoles          = 20
	maxOrganizationMembers        = 200
	needOrganizationRoles         = "the user:read scope"
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
