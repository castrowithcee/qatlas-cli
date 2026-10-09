package infomaniakchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// peopleSensitivity classifies user profile and presence data, which is more personal than team and
// message data.
const peopleSensitivity = "infomaniak-kchat-people"

const (
	maxUsernameLength = 64
	maxStatusIDs      = 100
	// maxSearchLimit is the cap kChat applies to a user search itself.
	maxSearchLimit     = 100
	defaultSearchLimit = 50
	maxSearchTermRunes = 64
	userOutOfScope     = "user is outside the teams of this connection"
)

// usernamePattern is the character class of a kChat username. The local check keeps it to one safe path
// segment; kChat may be more permissive, which only makes this the narrower reading.
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{1,` + strconv.Itoa(maxUsernameLength) + `}$`)

func usersRisk() capability.Risk {
	return capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity}
}

var userEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"username":{"type":"string"},"first_name":{"type":"string"},` +
	`"last_name":{"type":"string"},"nickname":{"type":"string"},"position":{"type":"string"},` +
	`"email":{"type":"string"},"is_bot":{"type":"boolean"},"deleted":{"type":"boolean"}},` +
	`"required":["id","username","is_bot","deleted"],"additionalProperties":false}`

var userEntryFields = []capability.Field{
	{Name: "id", Description: "User identifier"},
	{Name: "username", Description: "Username, untrusted data"},
	{Name: "first_name", Description: "First name, untrusted data"},
	{Name: "last_name", Description: "Last name, untrusted data"},
	{Name: "nickname", Description: "Nickname, untrusted data"},
	{Name: "position", Description: "Position or job title, untrusted data"},
	{Name: "email", Description: "Email address, only when kChat reports one to this token"},
	{Name: "is_bot", Description: "True for a bot account"},
	{Name: "deleted", Description: "True for a deactivated account"},
}

var pageArgument = capability.Argument{Name: "page",
	Description: "1-based page of the users; the first page when omitted"}

var usersGet = capability.Descriptor{
	ID:      Provider + ".users.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat user",
	Description: "Read the profile of one user who is a member of a team this connection is bound to, by user " +
		"id or by username; the own user is always readable",
	Tags:     []string{"infomaniak", "kchat", "users", "get"},
	Risk:     usersRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,"username":` +
		`{"type":"string","minLength":1,"maxLength":` + itoa(maxUsernameLength) + `,"pattern":"^[a-z0-9._-]{1,` +
		itoa(maxUsernameLength) + `}$"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(userEntrySchema),
	Arguments: []capability.Argument{
		{Name: "user_id", Description: "User identifier; exactly one of user_id and username"},
		{Name: "username", Description: "Username, 1 to " + itoa(maxUsernameLength) +
			" characters of lowercase letters, digits, dot, underscore, and hyphen; exactly one of user_id " +
			"and username"},
	},
	Fields: userEntryFields,
	Examples: []capability.Example{{Description: "Read one user by username",
		Arguments: json.RawMessage(`{"username":"jane.doe"}`)}},
}

var usersList = capability.Descriptor{
	ID:      Provider + ".users.list",
	Version: 1,
	Title:   "List Infomaniak kChat users",
	Description: "List the users of one bound team or of one channel this connection may reach, one page at a " +
		"time; exactly one of team_id and channel_id; users outside the bound teams are never listed",
	Tags:     []string{"infomaniak", "kchat", "users", "list"},
	Risk:     usersRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"users":{"type":"array","items":` +
		userEntrySchema + `},"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["users","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "team_id", Description: "Bound team to list; exactly one of team_id and channel_id"},
		{Name: "channel_id", Description: "Channel to list, inside this connection's channel targets; exactly " +
			"one of team_id and channel_id"},
		pageArgument,
		{Name: "limit", Description: "Users per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) +
			" when omitted"},
	},
	Fields: append(append([]capability.Field{}, userEntryFields...),
		capability.Field{Name: "team_id", Description: "Team that was listed, when listed by team"},
		capability.Field{Name: "channel_id", Description: "Channel that was listed, when listed by channel"},
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "has_more", Description: "True when kChat may hold a further page"},
		capability.Field{Name: "count", Description: "Number of users reported on this page, after users outside " +
			"the bound teams were dropped"},
	),
	Examples: []capability.Example{{Description: "List the users of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var usersSearch = capability.Descriptor{
	ID:      Provider + ".users.search",
	Version: 1,
	Title:   "Search Infomaniak kChat users",
	Description: "Search the users of one bound team or of one channel this connection may reach by name, " +
		"username, or email term; exactly one of team_id and channel_id; users outside the bound teams are " +
		"never returned",
	Tags:     []string{"infomaniak", "kchat", "users", "search"},
	Risk:     usersRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"term":{"type":"string","minLength":1,` +
		`"maxLength":` + itoa(maxSearchTermRunes) + `},"team_id":` + idSchema + `,"channel_id":` + idSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxSearchLimit) + `}},` +
		`"required":["term"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"users":{"type":"array","items":` +
		userEntrySchema + `},"count":{"type":"integer"}},"required":["users","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "term", Required: true, Description: "Search term, 1 to " + itoa(maxSearchTermRunes) + " characters"},
		{Name: "team_id", Description: "Bound team to search; exactly one of team_id and channel_id"},
		{Name: "channel_id", Description: "Channel to search, inside this connection's channel targets; " +
			"exactly one of team_id and channel_id"},
		{Name: "limit", Description: "Maximum number of users, 1 to " + itoa(maxSearchLimit) + "; " +
			itoa(defaultSearchLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, userEntryFields...),
		capability.Field{Name: "count", Description: "Number of users found, after users outside the bound " +
			"teams were dropped"}),
	Examples: []capability.Example{{Description: "Search one bound team",
		Arguments: json.RawMessage(`{"term":"jane","team_id":"abc123team0000000000000000"}`)}},
}

var usersStatus = capability.Descriptor{
	ID:      Provider + ".users.status",
	Version: 1,
	Title:   "Get Infomaniak kChat user presence",
	Description: "Read the presence status of 1 to " + itoa(maxStatusIDs) + " users who are members of teams " +
		"this connection is bound to; one user outside them refuses the whole request",
	Tags:     []string{"infomaniak", "kchat", "users", "status", "presence"},
	Risk:     usersRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_ids":{"type":"array","minItems":1,` +
		`"maxItems":` + itoa(maxStatusIDs) + `,"uniqueItems":true,"items":` + idSchema + `}},` +
		`"required":["user_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"statuses":{"type":"array","items":` +
		`{"type":"object","properties":{"user_id":` + idSchema + `,"status":{"type":"string"},` +
		`"manual":{"type":"boolean"},"last_activity_at":{"type":"string"}},` +
		`"required":["user_id","status","manual"],"additionalProperties":false}},"count":{"type":"integer"}},` +
		`"required":["statuses","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "user_ids", Required: true,
		Description: "1 to " + itoa(maxStatusIDs) + " distinct user identifiers"}},
	Fields: []capability.Field{
		{Name: "user_id", Description: "User the status belongs to"},
		{Name: "status", Description: "Presence status such as online, away, dnd, or offline"},
		{Name: "manual", Description: "True when the user set the status by hand"},
		{Name: "last_activity_at", Description: "Last activity, normalised to RFC 3339 in UTC, when kChat reports one"},
		{Name: "count", Description: "Number of statuses reported"},
	},
	Examples: []capability.Example{{Description: "Read the presence of two users",
		Arguments: json.RawMessage(`{"user_ids":["abc123user0000000000000000a","abc123user0000000000000000b"]}`)}},
}

// userJSON is the only part of the kChat User resource this provider decodes. Fields such as notify_props,
// props, roles, timezone, auth_service, and every credential or MFA field are deliberately absent, so they
// can never reach an answer.
type userJSON struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Nickname  string `json:"nickname"`
	Position  string `json:"position"`
	Email     string `json:"email"`
	IsBot     bool   `json:"is_bot"`
	DeleteAt  int64  `json:"delete_at"`
}

// UserEntry is the stable Qatlas view of one kChat user.
type UserEntry struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	Position  string `json:"position,omitempty"`
	Email     string `json:"email,omitempty"`
	IsBot     bool   `json:"is_bot"`
	Deleted   bool   `json:"deleted"`
}

// UsersPage is one page of users of a team or channel.
type UsersPage struct {
	TeamID    string      `json:"team_id,omitempty"`
	ChannelID string      `json:"channel_id,omitempty"`
	Users     []UserEntry `json:"users"`
	Page      int         `json:"page"`
	HasMore   bool        `json:"has_more"`
	Count     int         `json:"count"`
}

// UserSearchResult is the answer of one user search.
type UserSearchResult struct {
	Users []UserEntry `json:"users"`
	Count int         `json:"count"`
}

// UserStatus is the presence of one user.
type UserStatus struct {
	UserID         string `json:"user_id"`
	Status         string `json:"status"`
	Manual         bool   `json:"manual"`
	LastActivityAt string `json:"last_activity_at,omitempty"`
}

// UserStatuses is the answer of one presence read.
type UserStatuses struct {
	Statuses []UserStatus `json:"statuses"`
	Count    int          `json:"count"`
}

func userEntryOf(u userJSON) UserEntry {
	return UserEntry{ID: u.ID, Username: bounded(u.Username), FirstName: bounded(u.FirstName),
		LastName: bounded(u.LastName), Nickname: bounded(u.Nickname), Position: bounded(u.Position),
		Email: bounded(u.Email), IsBot: u.IsBot, Deleted: u.DeleteAt > 0}
}

// verifyUsersScope returns the subset of ids (in their given order, without duplicates) the connection may
// reach: the token's own user, and every user who is currently a member of a bound team. kChat is asked
// once per bound team, with all ids still unproven, and no further team is asked once every id is proven.
// A team membership that has ended does not count. An id that is not a kChat-style identifier is an
// invalid request. Callers decide whether an unreachable id refuses the request or is filtered out.
func (c *Client) verifyUsersScope(ctx context.Context, op string, ids []string) ([]string, error) {
	pending := map[string]bool{}
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if !validMattermostID(id) {
			return nil, invalidRequest("user_id must be a kChat-style identifier")
		}
		if !pending[id] {
			pending[id] = true
			ordered = append(ordered, id)
		}
	}
	if len(ordered) == 0 {
		return []string{}, nil
	}
	reachable := map[string]bool{}
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	if pending[own] {
		reachable[own] = true
	}
	for _, teamID := range c.scope.teams {
		var remaining []string
		for _, id := range ordered {
			if !reachable[id] {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) == 0 {
			break
		}
		var members []struct {
			TeamID   string `json:"team_id"`
			UserID   string `json:"user_id"`
			DeleteAt int64  `json:"delete_at"`
		}
		path := "/api/v4/teams/" + url.PathEscape(teamID) + "/members/ids"
		if err := c.do(ctx, op, http.MethodPost, path, nil, remaining, &members, false); err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.TeamID == teamID && m.DeleteAt == 0 && pending[m.UserID] {
				reachable[m.UserID] = true
			}
		}
	}
	result := make([]string, 0, len(ordered))
	for _, id := range ordered {
		if reachable[id] {
			result = append(result, id)
		}
	}
	return result, nil
}

// reachableUsers keeps the users whose id verifyUsersScope proves reachable.
func (c *Client) reachableUsers(ctx context.Context, op string, users []userJSON) ([]UserEntry, error) {
	ids := make([]string, 0, len(users))
	for _, u := range users {
		if validMattermostID(u.ID) {
			ids = append(ids, u.ID)
		}
	}
	allowed, err := c.verifyUsersScope(ctx, op, ids)
	if err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		ok[id] = true
	}
	entries := make([]UserEntry, 0, len(users))
	for _, u := range users {
		if ok[u.ID] {
			entries = append(entries, userEntryOf(u))
		}
	}
	return entries, nil
}

type usersGetArguments struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

func invokeUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get user"
	var input usersGetArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if (input.UserID == "") == (input.Username == "") {
		return nil, invalidRequest("give exactly one of user_id and username")
	}
	if input.UserID != "" && !validMattermostID(input.UserID) {
		return nil, invalidRequest("user_id must be a kChat-style identifier")
	}
	if input.Username != "" && !usernamePattern.MatchString(input.Username) {
		return nil, invalidRequest("username must be 1 to " + itoa(maxUsernameLength) +
			" characters of lowercase letters, digits, dot, underscore, and hyphen")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if input.UserID != "" {
		// A foreign user id is refused before its profile is read at all.
		return client.GetUser(ctx, "/api/v4/users/"+url.PathEscape(input.UserID), input.UserID)
	}
	return client.GetUser(ctx, "/api/v4/users/username/"+url.PathEscape(input.Username), "")
}

// GetUser reads one user. With wantID the reachability is proven first; without it (a username lookup)
// the user is read and then bound through the id kChat reports, before anything is returned.
func (c *Client) GetUser(ctx context.Context, path, wantID string) (*UserEntry, error) {
	const op = "get user"
	if wantID != "" {
		if err := c.requireUsers(ctx, op, []string{wantID}); err != nil {
			return nil, err
		}
	}
	var user userJSON
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &user, false); err != nil {
		// An unknown username is refused like a foreign one, so a lookup cannot probe the instance's
		// usernames outside the bound teams.
		var failure *provider.Error
		if wantID == "" && errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
			return nil, invalidRequest(userOutOfScope)
		}
		return nil, err
	}
	if !validMattermostID(user.ID) || (wantID != "" && user.ID != wantID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	if wantID == "" {
		if err := c.requireUsers(ctx, op, []string{user.ID}); err != nil {
			return nil, err
		}
	}
	entry := userEntryOf(user)
	return &entry, nil
}

// requireUsers refuses the request as invalid unless every id is reachable.
func (c *Client) requireUsers(ctx context.Context, op string, ids []string) error {
	allowed, err := c.verifyUsersScope(ctx, op, ids)
	if err != nil {
		return err
	}
	if len(allowed) != len(uniqueStrings(ids)) {
		return invalidRequest(userOutOfScope)
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// userFilter is the exactly-one team or channel filter of users.list and users.search.
type userFilter struct {
	teamID, channelID string
}

// checkUserFilter validates the filter locally, before any secret is read.
func checkUserFilter(resolved *config.Resolved, teamID, channelID string) (userFilter, error) {
	if (teamID == "") == (channelID == "") {
		return userFilter{}, invalidRequest("give exactly one of team_id and channel_id")
	}
	if teamID != "" {
		return userFilter{teamID: teamID}, selectTeam(resolved, teamID)
	}
	return userFilter{channelID: channelID}, selectChannel(resolved, channelID)
}

// confirm runs the live check a channel filter needs; a team filter was fully checked locally.
func (f userFilter) confirm(ctx context.Context, c *Client, op string) error {
	if f.channelID == "" {
		return nil
	}
	_, err := c.verifyChannelScope(ctx, op, f.channelID)
	return err
}

type usersListArguments struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list users"
	var input usersListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	filter, err := checkUserFilter(resolved, input.TeamID, input.ChannelID)
	if err != nil {
		return nil, err
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := filter.confirm(ctx, client, op); err != nil {
		return nil, err
	}
	return client.ListUsers(ctx, filter, page, limit)
}

// ListUsers reads one page of the users of a team or channel exactly as kChat paginates its own listing,
// never follows a further page, and drops every user outside the bound teams.
func (c *Client) ListUsers(ctx context.Context, filter userFilter, page, limit int) (*UsersPage, error) {
	const op = "list users"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	if filter.teamID != "" {
		query.Set("in_team", filter.teamID)
	} else {
		query.Set("in_channel", filter.channelID)
	}
	var users []userJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/users", query, nil, &users, false); err != nil {
		return nil, err
	}
	entries, err := c.reachableUsers(ctx, op, users)
	if err != nil {
		return nil, err
	}
	return &UsersPage{TeamID: filter.teamID, ChannelID: filter.channelID, Users: entries, Page: page,
		HasMore: len(users) >= limit, Count: len(entries)}, nil
}

type usersSearchArguments struct {
	Term      string `json:"term"`
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Limit     int    `json:"limit"`
}

// userSearchBody is the typed body of POST /api/v4/users/search.
type userSearchBody struct {
	Term        string `json:"term"`
	TeamID      string `json:"team_id,omitempty"`
	InChannelID string `json:"in_channel_id,omitempty"`
	Limit       int    `json:"limit"`
}

func invokeUsersSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "search users"
	var input usersSearchArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	term := strings.TrimSpace(input.Term)
	if term == "" || utf8.RuneCountInString(term) > maxSearchTermRunes || strings.IndexFunc(term, unicode.IsControl) >= 0 {
		return nil, invalidRequest("term must be 1 to " + itoa(maxSearchTermRunes) + " characters without control characters")
	}
	if input.Limit < 0 || input.Limit > maxSearchLimit {
		return nil, invalidRequest("limit must be 1 to " + itoa(maxSearchLimit))
	}
	filter, err := checkUserFilter(resolved, input.TeamID, input.ChannelID)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultSearchLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := filter.confirm(ctx, client, op); err != nil {
		return nil, err
	}
	return client.SearchUsers(ctx, filter, term, limit)
}

// SearchUsers sends one search, which changes nothing, and drops every hit outside the bound teams.
func (c *Client) SearchUsers(ctx context.Context, filter userFilter, term string, limit int) (*UserSearchResult, error) {
	const op = "search users"
	body := userSearchBody{Term: term, TeamID: filter.teamID, InChannelID: filter.channelID, Limit: limit}
	var users []userJSON
	if err := c.do(ctx, op, http.MethodPost, "/api/v4/users/search", nil, body, &users, false); err != nil {
		return nil, err
	}
	entries, err := c.reachableUsers(ctx, op, users)
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return &UserSearchResult{Users: entries, Count: len(entries)}, nil
}

type usersStatusArguments struct {
	UserIDs []string `json:"user_ids"`
}

func invokeUsersStatus(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get user status"
	var input usersStatusArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if len(input.UserIDs) == 0 || len(input.UserIDs) > maxStatusIDs {
		return nil, invalidRequest("user_ids must hold 1 to " + itoa(maxStatusIDs) + " identifiers")
	}
	seen := map[string]bool{}
	for _, id := range input.UserIDs {
		if !validMattermostID(id) {
			return nil, invalidRequest("user_ids must be kChat-style identifiers")
		}
		if seen[id] {
			return nil, invalidRequest("user_ids must not repeat an identifier")
		}
		seen[id] = true
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UserStatuses(ctx, input.UserIDs)
}

// UserStatuses checks every id against the bound teams first, so one foreign id refuses the request before
// any status is requested, then reads the statuses and drops any entry kChat adds for another id.
func (c *Client) UserStatuses(ctx context.Context, ids []string) (*UserStatuses, error) {
	const op = "get user status"
	if err := c.requireUsers(ctx, op, ids); err != nil {
		return nil, err
	}
	var statuses []struct {
		UserID         string `json:"user_id"`
		Status         string `json:"status"`
		Manual         bool   `json:"manual"`
		LastActivityAt int64  `json:"last_activity_at"`
	}
	if err := c.do(ctx, op, http.MethodPost, "/api/v4/users/status/ids", nil, ids, &statuses, false); err != nil {
		return nil, err
	}
	asked := make(map[string]bool, len(ids))
	for _, id := range ids {
		asked[id] = true
	}
	entries := make([]UserStatus, 0, len(statuses))
	for _, s := range statuses {
		if !asked[s.UserID] {
			continue
		}
		asked[s.UserID] = false
		entries = append(entries, UserStatus{UserID: s.UserID, Status: bounded(s.Status), Manual: s.Manual,
			LastActivityAt: msToRFC3339(s.LastActivityAt)})
	}
	return &UserStatuses{Statuses: entries, Count: len(entries)}, nil
}
