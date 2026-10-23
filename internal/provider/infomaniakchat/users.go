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
	`"email":{"type":"string"},"is_bot":{"type":"boolean"},"deleted":{"type":"boolean"},` +
	`"create_at":{"type":"string"},"update_at":{"type":"string"},"delete_at":{"type":"string"},` +
	`"last_picture_update":{"type":"string"},"terms_of_service_create_at":{"type":"string"},` +
	`"email_verified":{"type":"boolean"},"auth_service":{"type":"string"},"roles":{"type":"string"},` +
	`"locale":{"type":"string"},"terms_of_service_id":{"type":"string"},` +
	`"notify_props":{"type":"object","properties":{"channel":{"type":"string"},"desktop":{"type":"string"},` +
	`"desktop_sound":{"type":"string"},"email":{"type":"string"},"first_name":{"type":"string"},` +
	`"mention_keys":{"type":"string"},"push":{"type":"string"}},"additionalProperties":false},` +
	`"props":{"type":"object"},` +
	`"timezone":{"type":"object","properties":{"use_automatic_timezone":{"type":"boolean"},` +
	`"manual_timezone":{"type":"string"},"automatic_timezone":{"type":"string"}},"additionalProperties":false},` +
	`"omitted":{"type":"array","items":{"type":"string"}}},` +
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
	{Name: "create_at", Description: "When the account was created, RFC 3339 in UTC"},
	{Name: "update_at", Description: "When the account was last updated, RFC 3339 in UTC"},
	{Name: "delete_at", Description: "When the account was deactivated, RFC 3339 in UTC"},
	{Name: "last_picture_update", Description: "When the profile picture last changed, RFC 3339 in UTC"},
	{Name: "terms_of_service_create_at", Description: "When the user accepted the terms of service, RFC 3339 in UTC"},
	{Name: "email_verified", Description: "True when kChat reports the email address as verified"},
	{Name: "auth_service", Description: "Authentication service of the account, untrusted data"},
	{Name: "roles", Description: "System roles as kChat reports them, untrusted data"},
	{Name: "locale", Description: "Language of the account, untrusted data"},
	{Name: "terms_of_service_id", Description: "Identifier of the accepted terms of service"},
	{Name: "notify_props", Description: "Notification settings with channel, desktop, desktop_sound, email, " +
		"first_name, mention_keys, and push, untrusted data"},
	{Name: "props", Description: "Free properties of the account, untrusted data; a bounded object, left out and " +
		"named in omitted when beyond the bound"},
	{Name: "timezone", Description: "Time zone settings with use_automatic_timezone, manual_timezone, and " +
		"automatic_timezone, untrusted data"},
	{Name: "omitted", Description: "Paths of parts left out because they exceed a bound, such as props"},
}

// roleListSchema is the schema of a list of 1 to 3 distinct roles of a team or a channel.
func roleListSchema(kind string) string {
	return `{"type":"array","minItems":1,"maxItems":3,"uniqueItems":true,"items":{"type":"string","enum":["` +
		kind + `_user","` + kind + `_admin","` + kind + `_guest"]}}`
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
		"time; exactly one of team_id and channel_id; filters by activity, sort order, team or channel roles, " +
		"and channel exclusion; users outside the bound teams are never listed",
	Tags:     []string{"infomaniak", "kchat", "users", "list"},
	Risk:     usersRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `},` +
		`"active":{"type":"boolean"},"inactive":{"type":"boolean"},` +
		`"sort":{"type":"string","enum":["last_activity_at","create_at","status"]},` +
		`"team_roles":` + roleListSchema("team") + `,"channel_roles":` + roleListSchema("channel") + `,` +
		`"not_in_channel_id":` + idSchema + `},"additionalProperties":false}`),
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
		{Name: "active", Description: "True lists only active users; excludes inactive"},
		{Name: "inactive", Description: "True lists only deactivated users; excludes active"},
		{Name: "sort", Description: "last_activity_at or create_at with team_id, status with channel_id; by " +
			"username when omitted"},
		{Name: "team_roles", Description: "1 to 3 distinct of team_user, team_admin, team_guest; only with team_id"},
		{Name: "channel_roles", Description: "1 to 3 distinct of channel_user, channel_admin, channel_guest; " +
			"only with channel_id"},
		{Name: "not_in_channel_id", Description: "Lists the users of team_id who are not in this channel, inside " +
			"this connection's channel targets and the team; only with team_id"},
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
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxSearchLimit) + `},` +
		`"allow_inactive":{"type":"boolean"},"not_in_channel_id":` + idSchema + `},` +
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
		{Name: "allow_inactive", Description: "True includes deactivated users"},
		{Name: "not_in_channel_id", Description: "Searches the users of team_id who are not in this channel, inside " +
			"this connection's channel targets and the team; needs team_id and excludes channel_id"},
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

// userJSON is the part of the kChat User resource this provider decodes. The last password change, failed
// sign-in attempts, and MFA state are deliberately absent, so they can never reach an answer. Members beyond
// the first block are optional and read tolerantly: a value of an unexpected type reads as missing.
type userJSON struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	Nickname  string  `json:"nickname"`
	Position  string  `json:"position"`
	Email     string  `json:"email"`
	IsBot     bool    `json:"is_bot"`
	DeleteAt  flexInt `json:"delete_at"`

	CreateAt      flexInt              `json:"create_at"`
	UpdateAt      flexInt              `json:"update_at"`
	LastPictureAt flexInt              `json:"last_picture_update"`
	TermsCreateAt flexInt              `json:"terms_of_service_create_at"`
	EmailVerified tolerant[*bool]      `json:"email_verified"`
	AuthService   tolerant[string]     `json:"auth_service"`
	Roles         tolerant[string]     `json:"roles"`
	Locale        tolerant[string]     `json:"locale"`
	TermsID       tolerant[string]     `json:"terms_of_service_id"`
	NotifyProps   tolerant[notifyIn]   `json:"notify_props"`
	Props         json.RawMessage      `json:"props"`
	Timezone      tolerant[timezoneIn] `json:"timezone"`
}

type notifyIn struct {
	Channel      tolerant[string] `json:"channel"`
	Desktop      tolerant[string] `json:"desktop"`
	DesktopSound tolerant[string] `json:"desktop_sound"`
	Email        tolerant[string] `json:"email"`
	FirstName    tolerant[string] `json:"first_name"`
	MentionKeys  tolerant[string] `json:"mention_keys"`
	Push         tolerant[string] `json:"push"`
}

type timezoneIn struct {
	Use       flexBool         `json:"useAutomaticTimezone"`
	Manual    tolerant[string] `json:"manualTimezone"`
	Automatic tolerant[string] `json:"automaticTimezone"`
}

// flexBool reads a flag that the API documents as a boolean but kChat sends as the text "true" or "false".
// Any other value reads as missing.
type flexBool struct{ Value *bool }

func (b *flexBool) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if value, err := strconv.ParseBool(text); err == nil && (text == "true" || text == "false") {
		b.Value = &value
	}
	return nil
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

	CreateAt      string         `json:"create_at,omitempty"`
	UpdateAt      string         `json:"update_at,omitempty"`
	DeleteAt      string         `json:"delete_at,omitempty"`
	LastPictureAt string         `json:"last_picture_update,omitempty"`
	TermsCreateAt string         `json:"terms_of_service_create_at,omitempty"`
	EmailVerified *bool          `json:"email_verified,omitempty"`
	AuthService   string         `json:"auth_service,omitempty"`
	Roles         string         `json:"roles,omitempty"`
	Locale        string         `json:"locale,omitempty"`
	TermsID       string         `json:"terms_of_service_id,omitempty"`
	NotifyProps   *UserNotify    `json:"notify_props,omitempty"`
	Props         map[string]any `json:"props,omitempty"`
	Timezone      *UserTimezone  `json:"timezone,omitempty"`
	Omitted       []string       `json:"omitted,omitempty"`
}

// UserNotify holds the notification settings of one user.
type UserNotify struct {
	Channel      string `json:"channel,omitempty"`
	Desktop      string `json:"desktop,omitempty"`
	DesktopSound string `json:"desktop_sound,omitempty"`
	Email        string `json:"email,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	MentionKeys  string `json:"mention_keys,omitempty"`
	Push         string `json:"push,omitempty"`
}

// UserTimezone holds the time zone settings of one user.
type UserTimezone struct {
	UseAutomatic *bool  `json:"use_automatic_timezone,omitempty"`
	Manual       string `json:"manual_timezone,omitempty"`
	Automatic    string `json:"automatic_timezone,omitempty"`
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
	entry := UserEntry{ID: u.ID, Username: bounded(u.Username), FirstName: bounded(u.FirstName),
		LastName: bounded(u.LastName), Nickname: bounded(u.Nickname), Position: bounded(u.Position),
		Email: bounded(u.Email), IsBot: u.IsBot, Deleted: u.DeleteAt > 0,
		CreateAt: msToRFC3339(int64(u.CreateAt)), UpdateAt: msToRFC3339(int64(u.UpdateAt)),
		LastPictureAt: msToRFC3339(int64(u.LastPictureAt)), TermsCreateAt: msToRFC3339(int64(u.TermsCreateAt)),
		EmailVerified: u.EmailVerified.Value, AuthService: cleanText(u.AuthService.Value),
		Roles: cleanText(u.Roles.Value), Locale: cleanText(u.Locale.Value), TermsID: cleanText(u.TermsID.Value)}
	if u.DeleteAt > 0 {
		entry.DeleteAt = msToRFC3339(int64(u.DeleteAt))
	}
	n := u.NotifyProps.Value
	notify := UserNotify{Channel: cleanText(n.Channel.Value), Desktop: cleanText(n.Desktop.Value),
		DesktopSound: cleanText(n.DesktopSound.Value), Email: cleanText(n.Email.Value),
		FirstName: cleanText(n.FirstName.Value), MentionKeys: cleanText(n.MentionKeys.Value),
		Push: cleanText(n.Push.Value)}
	if notify != (UserNotify{}) {
		entry.NotifyProps = &notify
	}
	z := u.Timezone.Value
	zone := UserTimezone{UseAutomatic: z.Use.Value, Manual: cleanText(z.Manual.Value),
		Automatic: cleanText(z.Automatic.Value)}
	if zone != (UserTimezone{}) {
		entry.Timezone = &zone
	}
	props, within := boundedFreeObject(u.Props)
	if !within {
		entry.Omitted = []string{"props"}
	}
	entry.Props = props
	return entry
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
	return c.verifyChannelScope(ctx, op, f.channelID)
}

type usersListArguments struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

type usersListInput struct {
	usersListArguments
	Active         bool     `json:"active"`
	Inactive       bool     `json:"inactive"`
	Sort           string   `json:"sort"`
	TeamRoles      []string `json:"team_roles"`
	ChannelRoles   []string `json:"channel_roles"`
	NotInChannelID string   `json:"not_in_channel_id"`
}

// userListOptions are the checked filters of users.list beyond the team or channel.
type userListOptions struct {
	active, inactive bool
	sort             string
	teamRoles        []string
	channelRoles     []string
	notInChannelID   string
}

// checkRoles accepts 1 to 3 distinct roles out of the given kind.
func checkRoles(name, kind string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	if len(roles) > 3 {
		return invalidRequest(name + " must hold 1 to 3 roles")
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if role != kind+"_user" && role != kind+"_admin" && role != kind+"_guest" {
			return invalidRequest(name + " must be " + kind + "_user, " + kind + "_admin, or " + kind + "_guest")
		}
		if seen[role] {
			return invalidRequest(name + " must not repeat a role")
		}
		seen[role] = true
	}
	return nil
}

// checkUserListOptions validates the combination of the users.list filters locally, before any secret is
// read. The scope checks of the team, channel, and not_in_channel_id follow in checkUserFilter.
func checkUserListOptions(in usersListInput) (userListOptions, error) {
	if in.Active && in.Inactive {
		return userListOptions{}, invalidRequest("active and inactive exclude each other")
	}
	switch in.Sort {
	case "":
	case "last_activity_at", "create_at":
		if in.TeamID == "" {
			return userListOptions{}, invalidRequest("sort " + in.Sort + " needs team_id")
		}
	case "status":
		if in.ChannelID == "" {
			return userListOptions{}, invalidRequest("sort status needs channel_id")
		}
	default:
		return userListOptions{}, invalidRequest("sort must be last_activity_at, create_at, or status")
	}
	if len(in.TeamRoles) > 0 && in.TeamID == "" {
		return userListOptions{}, invalidRequest("team_roles needs team_id")
	}
	if len(in.ChannelRoles) > 0 && in.ChannelID == "" {
		return userListOptions{}, invalidRequest("channel_roles needs channel_id")
	}
	if in.NotInChannelID != "" && in.TeamID == "" {
		return userListOptions{}, invalidRequest("not_in_channel_id needs team_id")
	}
	if err := checkRoles("team_roles", "team", in.TeamRoles); err != nil {
		return userListOptions{}, err
	}
	if err := checkRoles("channel_roles", "channel", in.ChannelRoles); err != nil {
		return userListOptions{}, err
	}
	return userListOptions{active: in.Active, inactive: in.Inactive, sort: in.Sort, teamRoles: in.TeamRoles,
		channelRoles: in.ChannelRoles, notInChannelID: in.NotInChannelID}, nil
}

// checkExcludedChannel validates not_in_channel_id against the connection's channel targets, locally.
func checkExcludedChannel(resolved *config.Resolved, channelID string) error {
	if channelID == "" {
		return nil
	}
	return selectChannel(resolved, channelID)
}

// confirmExcludedChannel runs the live channel check of not_in_channel_id: the channel must be a channel of
// the bound team that is listed or searched. It is the only request beyond the listing itself.
func (c *Client) confirmExcludedChannel(ctx context.Context, op, teamID, channelID string) error {
	if channelID == "" {
		return nil
	}
	ch, err := c.boundChannel(ctx, op, channelID)
	if err != nil {
		return err
	}
	if ch.TeamID != teamID {
		return invalidRequest(channelOutOfScope)
	}
	return nil
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list users"
	var input usersListInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	options, err := checkUserListOptions(input)
	if err != nil {
		return nil, err
	}
	filter, err := checkUserFilter(resolved, input.TeamID, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if err := checkExcludedChannel(resolved, input.NotInChannelID); err != nil {
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
	if err := client.confirmExcludedChannel(ctx, op, filter.teamID, options.notInChannelID); err != nil {
		return nil, err
	}
	return client.ListUsers(ctx, filter, options, page, limit)
}

// ListUsers reads one page of the users of a team or channel exactly as kChat paginates its own listing,
// never follows a further page, and drops every user outside the bound teams.
func (c *Client) ListUsers(ctx context.Context, filter userFilter, options userListOptions, page, limit int) (*UsersPage, error) {
	const op = "list users"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	if filter.teamID != "" {
		query.Set("in_team", filter.teamID)
	} else {
		query.Set("in_channel", filter.channelID)
	}
	if options.active {
		query.Set("active", "true")
	}
	if options.inactive {
		query.Set("inactive", "true")
	}
	if options.sort != "" {
		query.Set("sort", options.sort)
	}
	if len(options.teamRoles) > 0 {
		query.Set("team_roles", strings.Join(options.teamRoles, ","))
	}
	if len(options.channelRoles) > 0 {
		query.Set("channel_roles", strings.Join(options.channelRoles, ","))
	}
	if options.notInChannelID != "" {
		query.Set("not_in_channel", options.notInChannelID)
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

	AllowInactive  bool   `json:"allow_inactive"`
	NotInChannelID string `json:"not_in_channel_id"`
}

// userSearchBody is the typed body of POST /api/v4/users/search.
type userSearchBody struct {
	Term           string `json:"term"`
	TeamID         string `json:"team_id,omitempty"`
	InChannelID    string `json:"in_channel_id,omitempty"`
	NotInChannelID string `json:"not_in_channel_id,omitempty"`
	AllowInactive  bool   `json:"allow_inactive,omitempty"`
	Limit          int    `json:"limit"`
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
	if input.NotInChannelID != "" && (input.TeamID == "" || input.ChannelID != "") {
		return nil, invalidRequest("not_in_channel_id needs team_id and excludes channel_id")
	}
	filter, err := checkUserFilter(resolved, input.TeamID, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if err := checkExcludedChannel(resolved, input.NotInChannelID); err != nil {
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
	if err := client.confirmExcludedChannel(ctx, op, filter.teamID, input.NotInChannelID); err != nil {
		return nil, err
	}
	return client.SearchUsers(ctx, filter, term, input.AllowInactive, input.NotInChannelID, limit)
}

// SearchUsers sends one search, which changes nothing, and drops every hit outside the bound teams.
func (c *Client) SearchUsers(ctx context.Context, filter userFilter, term string, allowInactive bool,
	notInChannelID string, limit int) (*UserSearchResult, error) {
	const op = "search users"
	body := userSearchBody{Term: term, TeamID: filter.teamID, InChannelID: filter.channelID,
		NotInChannelID: notInChannelID, AllowInactive: allowInactive, Limit: limit}
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
