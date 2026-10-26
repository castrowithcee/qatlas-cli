package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Provisioning reads of an administrator or subadmin identity (OCS cloud/users and cloud/groups). Nextcloud
// requires a password confirmation for every change of users and groups, which an app password cannot give,
// so this family offers no change at all. The reach of the identity stays the upper bound: a subadmin sees
// only the users and groups of the groups it manages.

const (
	adminSensitivity = "nextcloud-admin"
	groupAdmin       = "admin"

	defaultAdminLimit  = 50
	maxAdminLimit      = 100
	maxAdminOffset     = 100000
	maxAdminSearch     = 64
	maxAdminID         = 64
	maxAdminMembers    = 200
	maxAdminListGroups = 50

	messageAdminDenied = "this Nextcloud identity is not an administrator or subadmin of the requested users or groups"
	messageAdminAbsent = "Nextcloud holds no such user or group, or it is outside the reach of this identity"
)

var adminRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: adminSensitivity,
}

var (
	ocsUsers  = []string{"cloud", "users"}
	ocsGroups = []string{"cloud", "groups"}
)

// requireAdmin refuses a connection without an admin target locally, before any credential access or
// request, and names no other target.
func requireAdmin(resolved *config.Resolved) error {
	s, err := scopeOf(resolved)
	if err != nil {
		return err
	}
	if !s.admin {
		return &provider.Error{
			Class: provider.ClassPermission, Op: "open",
			Message: "this connection is not bound to the administration of the instance",
		}
	}
	return nil
}

// adminBound is folderBound for the administration reads.
func adminBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if err := requireAdmin(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

// adminFailure gives a refused or unknown request one fixed explanation instead of the generic path text.
func adminFailure(op string, err error) error {
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return err
	}
	switch {
	case providerErr.Class == provider.ClassPermission:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageAdminDenied,
			Original: providerErr.Original}
	case providerErr.Class == provider.ClassProviderError && providerErr.Message == messageNotFound:
		return providerErrorFrom(op, messageAdminAbsent, err)
	}
	return err
}

// validAdminID accepts a user or group ID that stays one path segment. Spaces are allowed because group IDs
// carry them; the path encoding escapes them.
func validAdminID(value string) bool {
	if value == "" || len(value) > maxAdminID || value == "." || value == ".." || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		switch {
		case r == '/', r == '\\', r == '%', r == '?', r == '#':
			return false
		case r < 0x20 || r == 0x7f:
			return false
		}
	}
	return strings.TrimSpace(value) == value
}

// AdminIDList is one page of user or group IDs.
type AdminIDList struct {
	IDs       []string `json:"ids"`
	Count     int      `json:"count"`
	Offset    int      `json:"offset"`
	More      bool     `json:"more,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

// AdminUser is the fixed view of one user. The storage location, the repeated display-name member, and every
// other member of the provider document cannot be decoded, so they cannot reach a result.
type AdminUser struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name,omitempty"`
	Email          string   `json:"email,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Language       string   `json:"language,omitempty"`
	Locale         string   `json:"locale,omitempty"`
	LastLogin      string   `json:"last_login,omitempty"`
	Groups         []string `json:"groups,omitempty"`
	SubadminGroups []string `json:"subadmin_groups,omitempty"`
	ContactDetails
	Quota     *Quota `json:"quota,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type rawAdminUser struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayname"`
	Email       string          `json:"email"`
	Enabled     json.RawMessage `json:"enabled"`
	Language    string          `json:"language"`
	Locale      string          `json:"locale"`
	LastLogin   json.Number     `json:"lastLogin"`
	Groups      []string        `json:"groups"`
	Subadmin    []string        `json:"subadmin"`
	Quota       json.RawMessage `json:"quota"`
	rawContact
}

type adminArguments struct {
	Search  string `json:"search"`
	Limit   *int   `json:"limit"`
	Offset  *int   `json:"offset"`
	UserID  string `json:"user_id"`
	GroupID string `json:"group_id"`
}

func readAdminArguments(op string, raw json.RawMessage) (adminArguments, error) {
	var input adminArguments
	if len(bytes.TrimSpace(raw)) == 0 {
		return input, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// adminPage validates search, limit, and offset locally.
func adminPage(op string, input adminArguments) (limit, offset int, err error) {
	limit = defaultAdminLimit
	if input.Limit != nil {
		limit = *input.Limit
	}
	if input.Offset != nil {
		offset = *input.Offset
	}
	if limit < 1 || limit > maxAdminLimit || offset < 0 || offset > maxAdminOffset {
		return 0, 0, providerError(op, "limit must be 1 to 100 and offset 0 to 100000")
	}
	if utf8.RuneCountInString(input.Search) > maxAdminSearch || hasControl(input.Search) || !utf8.ValidString(input.Search) {
		return 0, 0, providerError(op, "the search must be at most 64 characters without control characters")
	}
	return limit, offset, nil
}

// listAdminIDs reads one page of IDs below cloud/users or cloud/groups. One more than limit is requested
// to tell whether another page exists.
func (c *Client) listAdminIDs(ctx context.Context, op string, base []string, member string,
	input adminArguments) (*AdminIDList, error) {
	limit, offset, err := adminPage(op, input)
	if err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limit + 1)}, "offset": {strconv.Itoa(offset)}}
	if search := strings.TrimSpace(input.Search); search != "" {
		query.Set("search", search)
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: base, query: query})
	if err != nil {
		return nil, adminFailure(op, err)
	}
	items, err := adminMembers(op, data, member)
	if err != nil {
		return nil, err
	}
	list := &AdminIDList{IDs: []string{}, Offset: offset}
	for _, item := range items {
		if len(list.IDs) >= limit {
			list.More = true
			break
		}
		id, cutID := cut(item, maxActionLabel)
		list.Truncated = list.Truncated || cutID
		list.IDs = append(list.IDs, id)
	}
	list.Count = len(list.IDs)
	return list, nil
}

// adminMembers reads the string array below one member of the data object.
func adminMembers(op string, data json.RawMessage, member string) ([]string, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, invalidResponse(op, "the Nextcloud list could not be read")
	}
	var items []string
	if raw, ok := document[member]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, invalidResponse(op, "the Nextcloud list could not be read")
		}
	}
	return items, nil
}

// ListAdminUsers reads one page of user IDs.
func (c *Client) ListAdminUsers(ctx context.Context, input adminArguments) (*AdminIDList, error) {
	return c.listAdminIDs(ctx, "list users", ocsUsers, "users", input)
}

// ListAdminGroups reads one page of group IDs.
func (c *Client) ListAdminGroups(ctx context.Context, input adminArguments) (*AdminIDList, error) {
	return c.listAdminIDs(ctx, "list groups", ocsGroups, "groups", input)
}

// GetAdminUser reads one user through the fixed allowlist.
func (c *Client) GetAdminUser(ctx context.Context, userID string) (*AdminUser, error) {
	const op = "get user"
	if !validAdminID(userID) {
		return nil, providerError(op, "the user ID is unusable")
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsCloud, suffix: []string{"users", userID}})
	if err != nil {
		return nil, adminFailure(op, err)
	}
	var raw rawAdminUser
	if err := json.Unmarshal(data, &raw); err != nil || raw.ID == "" {
		return nil, invalidResponse(op, "the Nextcloud user data could not be read")
	}
	user := &AdminUser{}
	var cuts [6]bool
	user.ID, cuts[0] = cut(raw.ID, maxAccountText)
	user.DisplayName, cuts[1] = cut(raw.DisplayName, maxAccountText)
	user.Email, cuts[2] = cut(raw.Email, maxAccountText)
	user.Language, cuts[3] = cut(raw.Language, maxActionLabel)
	user.Locale, cuts[4] = cut(raw.Locale, maxActionLabel)
	user.ContactDetails, cuts[5] = contactOf(raw.rawContact)
	for _, flag := range cuts {
		user.Truncated = user.Truncated || flag
	}
	user.Enabled = enabledOf(raw.Enabled)
	user.LastLogin = lastLoginOf(raw.LastLogin)
	user.Groups, user.Truncated = boundedNames(raw.Groups, user.Truncated)
	user.SubadminGroups, user.Truncated = boundedNames(raw.Subadmin, user.Truncated)
	user.Quota = quotaOf(raw.Quota)
	return user, nil
}

func boundedNames(values []string, truncated bool) ([]string, bool) {
	var names []string
	for _, value := range values {
		if len(names) == maxAdminListGroups {
			return names, true
		}
		name, cutName := cut(value, maxActionLabel)
		truncated = truncated || cutName
		names = append(names, name)
	}
	return names, truncated
}

// AdminMembers is the member list of one group.
type AdminMembers struct {
	GroupID   string   `json:"group_id"`
	Users     []string `json:"users"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated,omitempty"`
}

// GroupMembers reads the member IDs of one group. Nextcloud does not page this list, so the result is cut.
func (c *Client) GroupMembers(ctx context.Context, groupID string) (*AdminMembers, error) {
	const op = "list group members"
	if !validAdminID(groupID) {
		return nil, providerError(op, "the group ID is unusable")
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsCloud, suffix: []string{"groups", groupID, "users"}})
	if err != nil {
		return nil, adminFailure(op, err)
	}
	items, err := adminMembers(op, data, "users")
	if err != nil {
		return nil, err
	}
	result := &AdminMembers{GroupID: groupID, Users: []string{}}
	for _, item := range items {
		if len(result.Users) >= maxAdminMembers {
			result.Truncated = true
			break
		}
		id, cutID := cut(item, maxActionLabel)
		result.Truncated = result.Truncated || cutID
		result.Users = append(result.Users, id)
	}
	result.Count = len(result.Users)
	return result, nil
}

// testAdmin checks the administrator or subadmin right with the lightest possible read.
func (c *Client) testAdmin(ctx context.Context) (provider.Class, error) {
	const op = "test connection"
	_, err := c.ocsGet(ctx, op, ocsRequest{app: ocsCloud, suffix: []string{"users"},
		query: url.Values{"limit": {"1"}}})
	if err == nil {
		return provider.ClassOK, nil
	}
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class, nil
	}
	return provider.ClassProviderError, nil
}

const (
	adminSearchSchema = `{"type":"string","minLength":1,"maxLength":64,"x-form":"a search text without control characters"}`
	adminPageSchema   = `"limit":{"type":"integer","minimum":1,"maximum":100},"offset":{"type":"integer","minimum":0,"maximum":100000}`
	adminIDSchema     = `{"type":"string","minLength":1,"maxLength":64,"x-form":"an ID without / \\ ? # % or control characters"}`
	adminListSchema   = `{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"}},"count":{"type":"integer"},` +
		`"offset":{"type":"integer"},"more":{"type":"boolean"},"truncated":{"type":"boolean"}},` +
		`"required":["ids","count","offset"],"additionalProperties":false}`
)

var adminPageArguments = []capability.Argument{
	{Name: "search", Description: "Text that the ID or display name contains"},
	{Name: "limit", Description: "IDs per call, 1 to 100, default 50"},
	{Name: "offset", Description: "IDs to skip, for the next page"},
}

var adminListFields = []capability.Field{
	{Name: "ids", Description: "IDs of the requested kind, untrusted data"},
	{Name: "count", Description: "Number of reported IDs"},
	{Name: "offset", Description: "Offset of this page"},
	{Name: "more", Description: "True when more exist; pass offset plus count as the next offset"},
	{Name: "truncated", Description: "True when an ID was cut"},
}

var adminUsersList = capability.Descriptor{
	ID: Provider + ".admin.users.list", Version: 1, Title: "List Nextcloud users",
	Description: "List user IDs of the Nextcloud instance as an administrator, or those of the managed groups as " +
		"a subadmin; at most 100 per call, optionally narrowed by a search text. Reads only; nothing is changed",
	Tags: []string{"nextcloud", "admin", "users", "list"}, Provider: Provider, Group: groupAdmin, Risk: adminRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"search":` + adminSearchSchema + `,` + adminPageSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(adminListSchema), Arguments: adminPageArguments, Fields: adminListFields,
	Examples:              []capability.Example{{Description: "Find users", Arguments: json.RawMessage(`{"search":"ali","limit":20}`)}},
	RequiresToolAllowList: true,
}

var adminGroupsList = capability.Descriptor{
	ID: Provider + ".admin.groups.list", Version: 1, Title: "List Nextcloud groups",
	Description: "List group IDs of the Nextcloud instance as an administrator, or the managed groups as a " +
		"subadmin; at most 100 per call, optionally narrowed by a search text. Reads only; nothing is changed",
	Tags: []string{"nextcloud", "admin", "groups", "list"}, Provider: Provider, Group: groupAdmin, Risk: adminRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"search":` + adminSearchSchema + `,` + adminPageSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(adminListSchema), Arguments: adminPageArguments, Fields: adminListFields,
	Examples:              []capability.Example{{Description: "List groups", Arguments: json.RawMessage(`{}`)}},
	RequiresToolAllowList: true,
}

var adminUsersGet = capability.Descriptor{
	ID: Provider + ".admin.users.get", Version: 1, Title: "Get a Nextcloud user",
	Description: "Read one user as an administrator or subadmin: ID, display name, email, enabled, language, locale, " +
		"last login, groups and managed groups (at most 50 each), phone, address, website, twitter, backend and its " +
		"capabilities, and storage use and quota as numbers. Values are personal and untrusted data; the storage " +
		"location is never reported",
	Tags: []string{"nextcloud", "admin", "users", "get"}, Provider: Provider, Group: groupAdmin, Risk: adminRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + adminIDSchema +
		`},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"display_name":{"type":"string"},` +
		`"email":{"type":"string"},"enabled":{"type":"boolean"},"language":{"type":"string"},"locale":{"type":"string"},` +
		`"last_login":{"type":"string"},"groups":{"type":"array","items":{"type":"string"}},` +
		`"subadmin_groups":{"type":"array","items":{"type":"string"}},` + userContactSchema + `,"quota":` + quotaSchema + `,` +
		`"truncated":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "user_id", Required: true, Description: "User ID from admin.users.list"}},
	Fields: append([]capability.Field{{Name: "id", Description: "User ID"},
		{Name: "display_name", Description: "Display name; untrusted data"}, {Name: "email", Description: "Email address; untrusted data"},
		{Name: "enabled", Description: "False for a disabled user"}, {Name: "language", Description: "Language"},
		{Name: "locale", Description: "Locale"},
		{Name: "last_login", Description: "Last login time in UTC, empty when the user never signed in"},
		{Name: "groups", Description: "At most 50 group IDs; untrusted data"},
		{Name: "subadmin_groups", Description: "At most 50 IDs of the groups the user manages as a subadmin"}},
		append(userContactFields, capability.Field{Name: "quota", Description: quotaFieldText},
			capability.Field{Name: "truncated", Description: "True when a value or a list was cut"})...),
	Examples:              []capability.Example{{Description: "Read one user", Arguments: json.RawMessage(`{"user_id":"alice"}`)}},
	RequiresToolAllowList: true,
}

var adminGroupsMembers = capability.Descriptor{
	ID: Provider + ".admin.groups.members", Version: 1, Title: "List the members of a Nextcloud group",
	Description: "List the user IDs of one group as an administrator or of a managed group as a subadmin; at most " +
		"200 are reported and a longer list is marked. Reads only; nothing is changed",
	Tags: []string{"nextcloud", "admin", "groups", "members"}, Provider: Provider, Group: groupAdmin, Risk: adminRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"group_id":` + adminIDSchema +
		`},"required":["group_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"},"users":{"type":"array",` +
		`"items":{"type":"string"}},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["group_id","users","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "group_id", Required: true, Description: "Group ID from admin.groups.list"}},
	Fields: []capability.Field{{Name: "group_id", Description: "Group that was read"},
		{Name: "users", Description: "At most 200 member user IDs; untrusted data"},
		{Name: "count", Description: "Number of reported members"},
		{Name: "truncated", Description: "True when the list or an ID was cut"}},
	Examples:              []capability.Example{{Description: "Read one group", Arguments: json.RawMessage(`{"group_id":"staff"}`)}},
	RequiresToolAllowList: true,
}

func invokeAdminUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readAdminArguments("list users", raw)
	if err != nil {
		return nil, err
	}
	if input.UserID != "" || input.GroupID != "" {
		return nil, providerError("list users", "only search, limit, and offset are accepted")
	}
	if _, _, err := adminPage("list users", input); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListAdminUsers(ctx, input)
}

func invokeAdminGroupsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readAdminArguments("list groups", raw)
	if err != nil {
		return nil, err
	}
	if input.UserID != "" || input.GroupID != "" {
		return nil, providerError("list groups", "only search, limit, and offset are accepted")
	}
	if _, _, err := adminPage("list groups", input); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListAdminGroups(ctx, input)
}

func invokeAdminUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readAdminArguments("get user", raw)
	if err != nil {
		return nil, err
	}
	if !validAdminID(input.UserID) || input.GroupID != "" || input.Search != "" || input.Limit != nil || input.Offset != nil {
		return nil, providerError("get user", "only a usable user_id is accepted")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GetAdminUser(ctx, input.UserID)
}

func invokeAdminGroupsMembers(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readAdminArguments("list group members", raw)
	if err != nil {
		return nil, err
	}
	if !validAdminID(input.GroupID) || input.UserID != "" || input.Search != "" || input.Limit != nil || input.Offset != nil {
		return nil, providerError("list group members", "only a usable group_id is accepted")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GroupMembers(ctx, input.GroupID)
}
