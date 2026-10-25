package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	usersSensitive = "matomo-users"
	// maxLoginLength and maxSearchLength bound the arguments that reach Matomo; Matomo caps logins and
	// addresses at 100 characters.
	maxLoginLength  = 100
	maxSearchLength = 100
	maxLoginFilter  = 100
	maxCapabilities = 100
)

// userProperties lists the members a user can have in a result. Only these members are ever read from a
// Matomo user row (an allow-list), so a member a later release adds cannot reach a result.
const userProperties = `"login":{"type":"string"},"email":{"type":"string"},"date_registered":{"type":"string"},` +
	`"last_seen":{"type":"string"},"last_seen_ago":{"type":"string"},"superuser_access":{"type":"boolean"},` +
	`"role":{"type":"string"},"capabilities":{"type":"array","items":{"type":"string"}}`

const userSchema = `{"type":"object","properties":{` + userProperties + `},"required":["login"],"additionalProperties":false}`

func userFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "login", Description: "Login of the user, untrusted"},
		{Name: prefix + "email", Description: "Email address, untrusted; Matomo shows it to a token without Super " +
			"User rights only for the token's own user and for Super Users"},
		{Name: prefix + "date_registered", Description: "Time the account was created, as Matomo reports it (UTC)"},
		{Name: prefix + "last_seen", Description: "Time of the last login, as Matomo reports it; absent when the user never logged in"},
		{Name: prefix + "last_seen_ago", Description: "Matomo's own wording for the time since the last login, untrusted"},
		{Name: prefix + "superuser_access", Description: "True when the user is a Super User"},
		{Name: prefix + "role", Description: "Role on the website asked for: view, write, admin, noaccess, or superuser"},
		{Name: prefix + "capabilities", Description: "Capabilities the user holds on the website asked for beyond the " +
			"role, at most 100, untrusted; empty for a Super User"},
	}
}

// User is a Matomo user as far as a result may show it. The members that concern the security of the account
// (password, tokens, two-factor state, invitations, password change, failed attempts) have no place here.
type User struct {
	Login           string   `json:"login"`
	Email           string   `json:"email,omitempty"`
	DateRegistered  string   `json:"date_registered,omitempty"`
	LastSeen        string   `json:"last_seen,omitempty"`
	LastSeenAgo     string   `json:"last_seen_ago,omitempty"`
	SuperuserAccess *bool    `json:"superuser_access,omitempty"`
	Role            string   `json:"role,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

// userJSON is a user row of UsersManager. It names the allowed members only; everything else in the row is
// dropped by the decoder.
type userJSON struct {
	Login           *loose  `json:"login"`
	Email           *loose  `json:"email"`
	DateRegistered  *loose  `json:"date_registered"`
	LastSeen        *loose  `json:"last_seen"`
	LastSeenAgo     *loose  `json:"last_seen_ago"`
	SuperuserAccess *loose  `json:"superuser_access"`
	Role            *loose  `json:"role"`
	Capabilities    []loose `json:"capabilities"`
}

// user converts a row; ok is false for a row without a login.
func (u userJSON) user() (User, bool) {
	login := u.Login.str(maxStringLength)
	if login == "" {
		return User{}, false
	}
	out := User{Login: login, Email: u.Email.str(maxStringLength), DateRegistered: u.DateRegistered.str(maxStringLength),
		LastSeen: u.LastSeen.str(maxStringLength), LastSeenAgo: u.LastSeenAgo.str(maxStringLength),
		SuperuserAccess: u.SuperuserAccess.flag(), Role: u.Role.str(maxStringLength)}
	for i := range u.Capabilities {
		if i >= maxCapabilities {
			break
		}
		if text := u.Capabilities[i].str(maxStringLength); text != "" {
			out.Capabilities = append(out.Capabilities, text)
		}
	}
	return out, true
}

func usersOf(rows []userJSON) []User {
	users := make([]User, 0, len(rows))
	for _, row := range rows {
		if user, ok := row.user(); ok {
			users = append(users, user)
		}
	}
	return users
}

func sortUsers(users []User) {
	slices.SortStableFunc(users, func(a, b User) int { return strings.Compare(a.Login, b.Login) })
}

// validText accepts a non-empty argument text without control characters, at most limit bytes.
func validText(text string, limit int) bool {
	if text == "" || len(text) > limit {
		return false
	}
	return !strings.ContainsFunc(text, unicode.IsControl)
}

func validLogin(login string) bool { return validText(login, maxLoginLength) }

// requireInstance settles the instance target before any secret is read.
func requireInstance(bound scope) error {
	if !bound.instance {
		return invalidArgument("this connection is not bound to the instance")
	}
	return nil
}

func sortedSites(bound scope) []int {
	sites := slices.Clone(bound.sites)
	slices.Sort(sites)
	return sites
}

var userAccessFilters = []string{"noaccess", "some", "view", "write", "admin", "superuser"}

const usersListLimit = `"limit":{"type":"integer","minimum":1,"maximum":100}`

var usersList = capability.Descriptor{
	ID: Provider + ".users.list", Version: 1, Title: "List Matomo users",
	Description: "List the users of the instance. With idSite it lists the users with their role and capabilities on " +
		"that website (UsersManager.getUsersPlusRole; an admin right on the website, a user without it sees only " +
		"itself); search and access filter that list. Without idSite it lists the users the token may manage " +
		"instance-wide (getUsers, or only their logins with only_logins) and needs a connection bound to the " +
		"instance target; logins narrows it. Logins, addresses, and names are personal data and untrusted; " +
		"nothing about the security of an account is returned",
	Tags: []string{"matomo", "users", "roles", "list", "personal-data"}, Risk: readRisk(usersSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":1,` +
		`"maximum":999999999},"search":{"type":"string","minLength":1,"maxLength":100},"access":{"type":"string","enum":[` +
		quotedList(userAccessFilters) + `]},"logins":{"type":"array","minItems":1,"maxItems":100,"items":` +
		`{"type":"string","minLength":1,"maxLength":100}},"only_logins":{"type":"boolean"},` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"users":{"type":"array","items":` + userSchema + `},` +
		`"logins":{"type":"array","items":{"type":"string"}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "idSite", Target: "site", Description: "Website whose users are listed with their role; must lie inside the " +
			"connection's targets. Without it the list is instance-wide and needs the instance target"},
		{Name: "search", Description: "With idSite: text the login or email must contain, up to 100 characters"},
		{Name: "access", Description: "With idSite: only users with this role on the website: " +
			strings.Join(userAccessFilters, ", ") + "; superuser is allowed for Super Users only"},
		{Name: "logins", Description: "Without idSite: at most 100 logins to read; every user the token may manage when omitted"},
		{Name: "only_logins", Description: "Without idSite: true lists only the logins (getUsersLogin) and cannot be combined with logins"},
	}, pagingArguments...),
	Fields: slices.Concat([]capability.Field{
		{Name: "users", Description: "Users ordered by login; role and capabilities only with idSite"},
	}, userFields("users[]."), []capability.Field{
		{Name: "logins", Description: "Logins ordered by login; present with only_logins"},
	}, pagingFields),
	Examples: []capability.Example{
		{Description: "List the users of website 1 with their role", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "List the logins of the instance", Arguments: json.RawMessage(`{"only_logins":true}`)},
	},
}

// UsersListResult is the answer of users.list.
type UsersListResult struct {
	Users      []User   `json:"users,omitempty"`
	Logins     []string `json:"logins,omitempty"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func invokeUsersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list users"
	var args struct {
		IDSite     *int     `json:"idSite"`
		Search     *string  `json:"search"`
		Access     *string  `json:"access"`
		Logins     []string `json:"logins"`
		OnlyLogins bool     `json:"only_logins"`
		Limit      int      `json:"limit"`
		Cursor     string   `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	var binding []any
	if args.IDSite != nil {
		if args.Logins != nil || args.OnlyLogins {
			return nil, invalidArgument("logins and only_logins apply only without idSite")
		}
		id, err := selectSite(resolved, args.IDSite)
		if err != nil {
			return nil, err
		}
		params.Set("idSite", strconv.Itoa(id))
		search, access := "", ""
		if args.Search != nil {
			if search = strings.TrimSpace(*args.Search); !validText(search, maxSearchLength) {
				return nil, invalidArgument("search must hold 1 to 100 characters")
			}
			params.Set("filter_search", search)
		}
		if args.Access != nil {
			if access = *args.Access; !slices.Contains(userAccessFilters, access) {
				return nil, invalidArgument("access must be one of " + strings.Join(userAccessFilters, ", "))
			}
			params.Set("filter_access", access)
		}
		binding = []any{"site", id, search, access}
	} else {
		if args.Search != nil || args.Access != nil {
			return nil, invalidArgument("search and access apply only with idSite")
		}
		if err := requireInstance(bound); err != nil {
			return nil, err
		}
		if args.OnlyLogins && args.Logins != nil {
			return nil, invalidArgument("only_logins cannot be combined with logins")
		}
		if args.Logins != nil {
			if len(args.Logins) == 0 || len(args.Logins) > maxLoginFilter {
				return nil, invalidArgument("logins must hold 1 to 100 logins")
			}
			for _, login := range args.Logins {
				if !validLogin(login) || strings.Contains(login, ",") {
					return nil, invalidArgument("every login must hold 1 to 100 characters without a comma")
				}
			}
			params.Set("userLogins", strings.Join(args.Logins, ","))
		}
		binding = []any{"instance", args.OnlyLogins, args.Logins}
	}
	parts := append([]any{usersList.ID, bound.allSites, sortedSites(bound)}, binding...)
	paging, err := startPage(provider.CursorBinding(parts...), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(usersList.ID)
	switch {
	case args.IDSite != nil:
		// Matomo pages this method itself, by limit and offset.
		params.Set("limit", strconv.Itoa(paging.limit+1))
		params.Set("offset", strconv.Itoa(paging.offset))
		var rows []userJSON
		if err := client.call(ctx, op, req, "UsersManager.getUsersPlusRole", params, &rows); err != nil {
			return nil, err
		}
		batch, next := sliceWindow(paging, usersOf(rows))
		if batch == nil {
			batch = []User{}
		}
		return &UsersListResult{Users: batch, HasMore: next != "", NextCursor: next}, nil
	case args.OnlyLogins:
		var rows []loose
		if err := client.call(ctx, op, req, "UsersManager.getUsersLogin", url.Values{}, &rows); err != nil {
			return nil, err
		}
		logins := make([]string, 0, len(rows))
		for i := range rows {
			if login := rows[i].str(maxStringLength); login != "" {
				logins = append(logins, login)
			}
		}
		slices.Sort(logins)
		batch, next := slice(paging, logins)
		if batch == nil {
			batch = []string{}
		}
		return &UsersListResult{Logins: batch, HasMore: next != "", NextCursor: next}, nil
	}
	var rows []userJSON
	if err := client.call(ctx, op, req, "UsersManager.getUsers", params, &rows); err != nil {
		return nil, err
	}
	users := usersOf(rows)
	sortUsers(users)
	batch, next := slice(paging, users)
	if batch == nil {
		batch = []User{}
	}
	return &UsersListResult{Users: batch, HasMore: next != "", NextCursor: next}, nil
}

var userGetKinds = []string{"user", "exists", "superusers", "token-superuser"}

const emailSchema = `{"type":"string","minLength":1,"maxLength":100}`

var usersGet = capability.Descriptor{
	ID: Provider + ".users.get", Version: 1, Title: "Get a Matomo user",
	Description: "Read one user, or check one login or email, or list the Super Users. kind user (the default) reads " +
		"the user by login (UsersManager.getUser) or by email (getUserByEmail); exists tells whether a login " +
		"(userExists) or an email (userEmailExists) is taken; superusers lists the Super Users " +
		"(getUsersHavingSuperUserAccess); token-superuser tells whether the token itself has Super User rights " +
		"(hasSuperUserAccess). It needs a connection bound to the instance target; reading a user needs Super User " +
		"rights. Logins and addresses are personal data and untrusted; nothing about the security of an account is returned",
	Tags: []string{"matomo", "users", "get", "superuser", "personal-data", "instance"}, Risk: readRisk(usersSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":[` + quotedList(userGetKinds) +
		`]},"login":{"type":"string","minLength":1,"maxLength":100},"email":` + emailSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"user":` + userSchema + `,` +
		`"exists":{"type":"boolean"},"users":{"type":"array","items":` + userSchema + `},` +
		`"token_superuser":{"type":"boolean"},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["kind"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "kind", Description: "What to read: " + strings.Join(userGetKinds, ", ") + "; user when omitted"},
		{Name: "login", Description: "Login of the user, up to 100 characters; for user and exists, exclusive with email"},
		{Name: "email", Description: "Email address of the user, up to 100 characters; for user and exists, exclusive with login"},
	}, pagingArguments...),
	Fields: slices.Concat([]capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "user", Description: "The user, for kind user; no website is involved, so it holds no role"},
	}, userFields("user."), []capability.Field{
		{Name: "exists", Description: "For kind exists: true when the login or email belongs to a user (the login " +
			"anonymous and the token's own login always exist)"},
		{Name: "users", Description: "For kind superusers: the Super Users with login, email, and the Super User flag, ordered by login"},
		{Name: "token_superuser", Description: "For kind token-superuser: true when the token's user is a Super User; " +
			"it names no account and says nothing about other users"},
	}, pagingFields),
	Examples: []capability.Example{
		{Description: "Read a user by login", Arguments: json.RawMessage(`{"login":"alice"}`)},
		{Description: "List the Super Users", Arguments: json.RawMessage(`{"kind":"superusers"}`)},
	},
}

// UsersGetResult is the answer of users.get.
type UsersGetResult struct {
	Kind           string `json:"kind"`
	User           *User  `json:"user,omitempty"`
	Exists         *bool  `json:"exists,omitempty"`
	Users          []User `json:"users,omitempty"`
	TokenSuperuser *bool  `json:"token_superuser,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	NextCursor     string `json:"next_cursor,omitempty"`
}

func invokeUsersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get user"
	var args struct {
		Kind   *string `json:"kind"`
		Login  *string `json:"login"`
		Email  *string `json:"email"`
		Limit  int     `json:"limit"`
		Cursor string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	// Every kind needs the instance target: reading a user and listing Super Users are instance-wide, a
	// user or email check lets a caller probe the accounts of the instance, and the Super User check of the
	// token is about instance-wide rights.
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	kind := "user"
	if args.Kind != nil {
		kind = *args.Kind
	}
	if !slices.Contains(userGetKinds, kind) {
		return nil, invalidArgument("kind must be one of " + strings.Join(userGetKinds, ", "))
	}
	byIdentity := kind == "user" || kind == "exists"
	if !byIdentity && (args.Login != nil || args.Email != nil) {
		return nil, invalidArgument("login and email apply only to user and exists")
	}
	if kind != "superusers" && (args.Limit != 0 || args.Cursor != "") {
		return nil, invalidArgument("limit and cursor apply only to superusers")
	}
	var identity string
	if byIdentity {
		if (args.Login == nil) == (args.Email == nil) {
			return nil, invalidArgument("exactly one of login and email is required")
		}
		if args.Login != nil {
			identity = *args.Login
		} else {
			identity = *args.Email
		}
		if !validText(identity, maxLoginLength) {
			return nil, invalidArgument("login or email must hold 1 to 100 characters without control characters")
		}
	}
	paging, err := startPage(provider.CursorBinding(usersGet.ID, kind), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(usersGet.ID)
	result := &UsersGetResult{Kind: kind}
	switch {
	case kind == "user":
		method, param := "UsersManager.getUser", "userLogin"
		if args.Email != nil {
			method, param = "UsersManager.getUserByEmail", "userEmail"
		}
		var answer json.RawMessage
		if err := client.call(ctx, op, req, method, url.Values{param: {identity}}, &answer); err != nil {
			return nil, err
		}
		// Matomo answers an empty list when it holds no record for an existing login.
		if bytes.Equal(bytes.TrimSpace(answer), []byte("[]")) {
			return nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
				Message: "Matomo does not hold this resource or does not show it to this token"}
		}
		var row userJSON
		if !bytes.HasPrefix(bytes.TrimSpace(answer), []byte("{")) || json.Unmarshal(answer, &row) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid user")
		}
		user, found := row.user()
		if !found {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid user")
		}
		result.User = &user
	case kind == "exists":
		method, param := "UsersManager.userExists", "userLogin"
		if args.Email != nil {
			method, param = "UsersManager.userEmailExists", "userEmail"
		}
		var answer json.RawMessage
		if err := client.call(ctx, op, req, method, url.Values{param: {identity}}, &answer); err != nil {
			return nil, err
		}
		var exists bool
		if scalar(answer, &exists) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
		result.Exists = &exists
	case kind == "superusers":
		var rows []userJSON
		if err := client.call(ctx, op, req, "UsersManager.getUsersHavingSuperUserAccess", url.Values{}, &rows); err != nil {
			return nil, err
		}
		users := usersOf(rows)
		sortUsers(users)
		batch, next := slice(paging, users)
		if batch == nil {
			batch = []User{}
		}
		result.Users, result.HasMore, result.NextCursor = batch, next != "", next
	default:
		var answer json.RawMessage
		if err := client.call(ctx, op, req, "UsersManager.hasSuperUserAccess", url.Values{}, &answer); err != nil {
			return nil, err
		}
		var superuser bool
		if scalar(answer, &superuser) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
		result.TokenSuperuser = &superuser
	}
	return result, nil
}
