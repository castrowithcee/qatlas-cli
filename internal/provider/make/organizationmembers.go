package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.members works on the organization an organization-mode connection is bound to; the organization is always the connection's target. API (checked 2026-10-06
// against developers.make.com's published API reference, not a live account):
//   - GET /users/roles?category=organization&organizationId=, user:read, answering {"usersRoles":[{id, name,
//     identifier, subsidiary, category, permissions, managementType, description}]}.
//   - GET /users?organizationId=&organizationRoleId= with the optional name, email, filterByTeamId, ids,
//     pg[sortBy], and pg[sortDir], user:read, answering {"users":[{id, name, email, language, ...}]}.
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

const (
	maxMemberFilterText  = 256
	maxMemberFilterIDs   = 100
	maxMemberTeamIDs     = 100
	maxRolePermissions   = 200
	maxPermissionLength  = 128
	maxMemberTextLength  = 256
	maxMemberAvatarBytes = 2048
)

var organizationMembers = capability.Descriptor{
	ID: Provider + ".organization.members", Version: 1, Title: "List members of the bound Make organization",
	Description: "List the members of the organization this connection is bound to, grouped by organization " +
		"role: user id, name, email address, role, language, locale, country, time zone, last login, support " +
		"eligibility, team ids, avatar address, and private space with its limits and consumption, plus the " +
		"read roles with their permissions. Optional filters (name, email, team, ids) and sorting apply to " +
		"every role. Email addresses and the other person data are personal data. Another person's " +
		"two-factor status is not reported. Only on an organization connection. Needs the user:read scope",
	Tags: []string{"make", "organization", "members", "automation"}, Risk: organizationPeopleReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxMemberFilterText) + `},` +
		`"email":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxMemberFilterText) + `},` +
		`"filter_by_team_id":{"type":"integer","minimum":0},` +
		`"ids":{"type":"array","items":{"type":"integer","minimum":1},"minItems":1,"maxItems":` +
		strconv.Itoa(maxMemberFilterIDs) + `},` +
		`"sort_by":{"type":"string","enum":["name","id","email"]},` +
		`"sort_dir":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"members":{"type":"array","items":{"type":"object","properties":{"user_id":{"type":"integer"},` +
		`"name":{"type":"string"},"email":{"type":"string"},"role_id":{"type":"integer"},` +
		`"role_name":{"type":"string"},"language":{"type":"string"},"timezone_id":{"type":"integer"},` +
		`"locale_id":{"type":"integer"},"country_id":{"type":"integer"},` +
		`"features":{"type":"object","properties":{"allow_apps":{"type":"boolean"}},"additionalProperties":false},` +
		`"avatar":{"type":"string"},"last_login":{"type":"string"},"support_eligible":{"type":"boolean"},` +
		`"user_team_ids":{"type":"array","items":{"type":"integer"}},` +
		`"private_space":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
		`"global_agents_enabled":{"type":"boolean"},"type":{"type":"string"},` +
		`"operations_limit":{"type":"integer"},"transfer_limit":{"type":"integer"},` +
		`"consumed_operations":{"type":"integer"},"consumed_transfer":{"type":"integer"},` +
		`"is_paused":{"type":"boolean"},"consumed_centicredits":{"type":"integer"}},"additionalProperties":false}},` +
		`"required":["user_id","role_id"],"additionalProperties":false}},` +
		`"roles":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"name":{"type":"string"},"identifier":{"type":"string"},"subsidiary":{"type":"boolean"},` +
		`"category":{"type":"string"},"permissions":{"type":"array","items":{"type":"string"}},` +
		`"management_type":{"type":"string"},"description":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["organization_id","members","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Only members whose name matches, 1 to " + strconv.Itoa(maxMemberFilterText) +
			" characters; applies to every role"},
		{Name: "email", Description: "Only members whose email address matches, 1 to " +
			strconv.Itoa(maxMemberFilterText) + " characters; applies to every role"},
		{Name: "filter_by_team_id", Description: "Only members of this team; 0 selects members without any team"},
		{Name: "ids", Description: "Only these user ids, 1 to " + strconv.Itoa(maxMemberFilterIDs) +
			" positive integers"},
		{Name: "sort_by", Description: "Sort members of each role by name, id, or email"},
		{Name: "sort_dir", Description: "Sort direction, asc or desc"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "members", Description: "The members found per role, grouped by role"},
		{Name: "user_id", Description: "The member's user id"},
		{Name: "name", Description: "Member name, or role name in roles (untrusted; personal data for a member)"},
		{Name: "email", Description: "Member email address (untrusted, personal data)"},
		{Name: "role_id", Description: "Id of the role the member was found under"},
		{Name: "role_name", Description: "Name of that role (untrusted)"},
		{Name: "language", Description: "Member language (untrusted, personal data)"},
		{Name: "timezone_id", Description: "Make's id of the member's time zone"},
		{Name: "locale_id", Description: "Make's id of the member's locale"},
		{Name: "country_id", Description: "Make's id of the member's country"},
		{Name: "features", Description: "allow_apps: whether the member may use apps"},
		{Name: "avatar", Description: "Avatar address (untrusted, never fetched); left out when it carries a " +
			"token, signature, or credential in its address"},
		{Name: "last_login", Description: "Time of the member's last login as Make reports it (personal data)"},
		{Name: "support_eligible", Description: "Whether Make deems the member eligible to contact support, when Make reports it"},
		{Name: "user_team_ids", Description: "Ids of the member's teams in the organization, at most " +
			strconv.Itoa(maxMemberTeamIDs) + " (truncated is true when more exist)"},
		{Name: "private_space", Description: "The member's private space: id, name (untrusted), " +
			"global_agents_enabled, type, operations_limit, transfer_limit, consumed_operations, " +
			"consumed_transfer, is_paused, consumed_centicredits; only the values Make reports"},
		{Name: "roles", Description: "The roles read, at most " + strconv.Itoa(maxOrganizationRoles) +
			", also those without members; the count of members is in count"},
		{Name: "id", Description: "Role id"},
		{Name: "identifier", Description: "Role identifier (untrusted)"},
		{Name: "subsidiary", Description: "Whether Make marks the role as subsidiary"},
		{Name: "category", Description: "Role category (untrusted)"},
		{Name: "permissions", Description: "Permissions of the role (untrusted), at most " +
			strconv.Itoa(maxRolePermissions) + " (truncated is true when more exist)"},
		{Name: "management_type", Description: "How the role is managed, as Make reports it (untrusted)"},
		{Name: "description", Description: "Role description (untrusted)"},
		{Name: "count", Description: "Members returned, at most " + strconv.Itoa(maxOrganizationMembers)},
		{Name: "truncated", Description: "True when roles, members, or a team or permission list beyond the " +
			"caps may exist"}},
	Examples: []capability.Example{{Description: "List the organization's members", Arguments: json.RawMessage(`{}`)},
		{Description: "Find a member by email, sorted by name",
			Arguments: json.RawMessage(`{"email":"pat@example.com","sort_by":"name","sort_dir":"asc"}`)}},
}

// OrganizationMember is one member with the organization role. Another person's two-factor status is account
// security data and is deliberately not part of it.
type OrganizationMember struct {
	UserID          int64               `json:"user_id"`
	Name            string              `json:"name,omitempty"`
	Email           string              `json:"email,omitempty"`
	RoleID          int64               `json:"role_id"`
	RoleName        string              `json:"role_name,omitempty"`
	Language        string              `json:"language,omitempty"`
	TimezoneID      *int64              `json:"timezone_id,omitempty"`
	LocaleID        *int64              `json:"locale_id,omitempty"`
	CountryID       *int64              `json:"country_id,omitempty"`
	Features        *MemberFeatures     `json:"features,omitempty"`
	Avatar          string              `json:"avatar,omitempty"`
	LastLogin       string              `json:"last_login,omitempty"`
	SupportEligible *bool               `json:"support_eligible,omitempty"`
	UserTeamIDs     []int64             `json:"user_team_ids,omitempty"`
	PrivateSpace    *MemberPrivateSpace `json:"private_space,omitempty"`
}

// MemberFeatures holds the feature flags of a member.
type MemberFeatures struct {
	AllowApps *bool `json:"allow_apps,omitempty"`
}

// MemberPrivateSpace is the personal private space of a member.
type MemberPrivateSpace struct {
	ID                   int64  `json:"id,omitempty"`
	Name                 string `json:"name,omitempty"`
	GlobalAgentsEnabled  *bool  `json:"global_agents_enabled,omitempty"`
	Type                 string `json:"type,omitempty"`
	OperationsLimit      *int64 `json:"operations_limit,omitempty"`
	TransferLimit        *int64 `json:"transfer_limit,omitempty"`
	ConsumedOperations   *int64 `json:"consumed_operations,omitempty"`
	ConsumedTransfer     *int64 `json:"consumed_transfer,omitempty"`
	IsPaused             *bool  `json:"is_paused,omitempty"`
	ConsumedCenticredits *int64 `json:"consumed_centicredits,omitempty"`
}

// OrganizationRole is one organization role that was read.
type OrganizationRole struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name,omitempty"`
	Identifier     string   `json:"identifier,omitempty"`
	Subsidiary     *bool    `json:"subsidiary,omitempty"`
	Category       string   `json:"category,omitempty"`
	Permissions    []string `json:"permissions,omitempty"`
	ManagementType string   `json:"management_type,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// OrganizationMembers is the answer of organization.members.
type OrganizationMembers struct {
	OrganizationID int64                `json:"organization_id"`
	Members        []OrganizationMember `json:"members"`
	Roles          []OrganizationRole   `json:"roles,omitempty"`
	Count          int                  `json:"count"`
	Truncated      bool                 `json:"truncated"`
}

type organizationRoleJSON struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Identifier     string   `json:"identifier"`
	Subsidiary     *bool    `json:"subsidiary"`
	Category       string   `json:"category"`
	Permissions    []string `json:"permissions"`
	ManagementType string   `json:"managementType"`
	Description    string   `json:"description"`
}

// organizationRoles reads the organization roles of the bound organization with one request.
func (c *Client) organizationRoles(ctx context.Context, op string) ([]organizationRoleJSON, error) {
	var page struct {
		Roles []organizationRoleJSON `json:"usersRoles"`
	}
	query := url.Values{"category": {"organization"}, "organizationId": {strconv.FormatInt(c.scope.orgID, 10)},
		"cols[]": {"id", "name", "identifier", "subsidiary", "category", "permissions", "managementType", "description"}}
	if err := c.get(ctx, op, "/users/roles", query, &page, needOrganizationRoles); err != nil {
		return nil, err
	}
	return page.Roles, nil
}

// memberFilter is the validated optional filter and sorting of every role's user request.
type memberFilter struct {
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	FilterByTeamID *int64  `json:"filter_by_team_id"`
	IDs            []int64 `json:"ids"`
	SortBy         string  `json:"sort_by"`
	SortDir        string  `json:"sort_dir"`
}

// parseMemberFilter validates the arguments before any secret is read or request is sent.
func parseMemberFilter(raw json.RawMessage) (memberFilter, error) {
	var f memberFilter
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, providerError("list organization members", "the validated arguments could not be read")
	}
	for _, text := range []struct{ name, value string }{{"name", f.Name}, {"email", f.Email}} {
		if n := utf8.RuneCountInString(text.value); text.value != "" &&
			(n > maxMemberFilterText || strings.TrimSpace(text.value) == "") {
			return f, invalidRequest(text.name + " must be 1 to " + strconv.Itoa(maxMemberFilterText) + " characters")
		}
	}
	if f.FilterByTeamID != nil && *f.FilterByTeamID < 0 {
		return f, invalidRequest("filter_by_team_id must be 0 or more")
	}
	if f.IDs != nil && (len(f.IDs) == 0 || len(f.IDs) > maxMemberFilterIDs) {
		return f, invalidRequest("ids must hold 1 to " + strconv.Itoa(maxMemberFilterIDs) + " user ids")
	}
	for _, id := range f.IDs {
		if id <= 0 {
			return f, invalidRequest("ids must be positive integers")
		}
	}
	if f.SortBy != "" && f.SortBy != "name" && f.SortBy != "id" && f.SortBy != "email" {
		return f, invalidRequest("sort_by must be name, id, or email")
	}
	if f.SortDir != "" && f.SortDir != "asc" && f.SortDir != "desc" {
		return f, invalidRequest("sort_dir must be asc or desc")
	}
	return f, nil
}

func (f memberFilter) apply(query url.Values) {
	if f.Name != "" {
		query.Set("name", f.Name)
	}
	if f.Email != "" {
		query.Set("email", f.Email)
	}
	if f.FilterByTeamID != nil {
		query.Set("filterByTeamId", strconv.FormatInt(*f.FilterByTeamID, 10))
	}
	for _, id := range f.IDs {
		query.Add("ids", strconv.FormatInt(id, 10))
	}
	if f.SortBy != "" {
		query.Set("pg[sortBy]", f.SortBy)
	}
	if f.SortDir != "" {
		query.Set("pg[sortDir]", f.SortDir)
	}
}

type memberUserJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Language   string `json:"language"`
	TimezoneID *int64 `json:"timezoneId"`
	LocaleID   *int64 `json:"localeId"`
	CountryID  *int64 `json:"countryId"`
	Features   *struct {
		AllowApps *bool `json:"allow_apps"`
	} `json:"features"`
	Avatar          string  `json:"avatar"`
	LastLogin       string  `json:"lastLogin"`
	SupportEligible *bool   `json:"supportEligible"`
	UserTeamIDs     []int64 `json:"userTeamIds"`
	PrivateSpace    *struct {
		ID                   int64  `json:"id"`
		Name                 string `json:"name"`
		GlobalAgentsEnabled  *bool  `json:"globalAgentsEnabled"`
		Type                 string `json:"type"`
		OperationsLimit      *int64 `json:"operationsLimit"`
		TransferLimit        *int64 `json:"transferLimit"`
		ConsumedOperations   *int64 `json:"consumedOperations"`
		ConsumedTransfer     *int64 `json:"consumedTransfer"`
		IsPaused             *bool  `json:"isPaused"`
		ConsumedCenticredits *int64 `json:"consumedCenticredits"`
	} `json:"privateSpace"`
}

// credentialQueryKeys are fragments of query parameter names that mark an address as an access value.
var credentialQueryKeys = []string{"token", "sig", "key", "secret", "auth", "cred", "pass", "jwt", "session",
	"expires", "policy", "code"}

// safeAvatar returns the avatar address as bounded text, or "" when it is not a plain http(s) address or
// carries a credential-like part (user info or a token or signature in the query or fragment). The address is
// never fetched.
func safeAvatar(raw string) string {
	text := cleanText(raw, maxMemberAvatarBytes)
	u, err := url.Parse(text)
	if text == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	if u.Fragment != "" {
		return ""
	}
	for key := range u.Query() {
		lower := strings.ToLower(key)
		for _, fragment := range credentialQueryKeys {
			if strings.Contains(lower, fragment) {
				return ""
			}
		}
	}
	return text
}

func memberOf(u memberUserJSON, role organizationRoleJSON, truncated *bool) OrganizationMember {
	m := OrganizationMember{UserID: u.ID, Name: bounded(u.Name), Email: bounded(u.Email), RoleID: role.ID,
		RoleName: bounded(role.Name), Language: cleanText(u.Language, maxMemberTextLength), TimezoneID: u.TimezoneID,
		LocaleID: u.LocaleID, CountryID: u.CountryID, SupportEligible: u.SupportEligible,
		Avatar: safeAvatar(u.Avatar), LastLogin: cleanText(u.LastLogin, maxMemberTextLength)}
	if u.Features != nil && u.Features.AllowApps != nil {
		m.Features = &MemberFeatures{AllowApps: u.Features.AllowApps}
	}
	for _, id := range u.UserTeamIDs {
		if id <= 0 {
			continue
		}
		if len(m.UserTeamIDs) >= maxMemberTeamIDs {
			*truncated = true
			break
		}
		m.UserTeamIDs = append(m.UserTeamIDs, id)
	}
	if p := u.PrivateSpace; p != nil {
		m.PrivateSpace = &MemberPrivateSpace{ID: p.ID, Name: cleanText(p.Name, maxMemberTextLength),
			GlobalAgentsEnabled: p.GlobalAgentsEnabled, Type: cleanText(p.Type, maxMemberTextLength),
			OperationsLimit: p.OperationsLimit, TransferLimit: p.TransferLimit, ConsumedOperations: p.ConsumedOperations,
			ConsumedTransfer: p.ConsumedTransfer, IsPaused: p.IsPaused, ConsumedCenticredits: p.ConsumedCenticredits}
	}
	return m
}

func roleOf(role organizationRoleJSON, truncated *bool) OrganizationRole {
	r := OrganizationRole{ID: role.ID, Name: cleanText(role.Name, maxMemberTextLength),
		Identifier: cleanText(role.Identifier, maxMemberTextLength), Subsidiary: role.Subsidiary,
		Category:       cleanText(role.Category, maxMemberTextLength),
		ManagementType: cleanText(role.ManagementType, maxMemberTextLength),
		Description:    cleanText(role.Description, maxMemberAvatarBytes)}
	for _, permission := range role.Permissions {
		permission = cleanText(permission, maxPermissionLength)
		if permission == "" {
			continue
		}
		if len(r.Permissions) >= maxRolePermissions {
			*truncated = true
			break
		}
		r.Permissions = append(r.Permissions, permission)
	}
	return r
}

func invokeOrganizationMembers(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list organization members"
	filter, err := parseMemberFilter(raw)
	if err != nil {
		return nil, err
	}
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
		if role.ID > 0 {
			result.Roles = append(result.Roles, roleOf(role, &result.Truncated))
		}
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
			Users []memberUserJSON `json:"users"`
		}
		query := url.Values{"organizationId": {strconv.FormatInt(client.scope.orgID, 10)},
			"organizationRoleId": {strconv.FormatInt(role.ID, 10)}, "cols[]": {"id", "name", "email", "language",
				"timezoneId", "localeId", "countryId", "features", "avatar", "lastLogin",
				"userTeamIds", "privateSpace"},
			"pg[limit]": {strconv.Itoa(remaining)}}
		filter.apply(query)
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
			result.Members = append(result.Members, memberOf(u, role, &result.Truncated))
		}
	}
	result.Count = len(result.Members)
	return result, nil
}
