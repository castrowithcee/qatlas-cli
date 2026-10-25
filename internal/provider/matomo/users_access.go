package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxAccessLength = 64
	accessPattern   = `^[A-Za-z][A-Za-z0-9_]{0,63}$`
	maxSitesPerUser = 1000
)

var siteRoleFilters = []string{"some", "view", "write", "admin"}

var usersGetAccess = capability.Descriptor{
	ID: Provider + ".users.get-access", Version: 1, Title: "Get Matomo user access",
	Description: "Read who may access which website. With login it lists the websites of one user with role and " +
		"capabilities (UsersManager.getSitesAccessForUser; an admin right; not for Super Users), or with raw every " +
		"access entry of the user (getSitesAccessFromUser; Super User rights and the instance target). With idSite " +
		"it lists the users of the website with their access (getUsersAccessFromSite; an admin right on the " +
		"website), or with access also the users that hold that role or capability (getUsersWithSiteAccess). " +
		"With access alone it lists, per user, the websites where that role or capability is assigned " +
		"(getUsersSitesFromAccess; Super User rights and the instance target). Rows for websites outside the " +
		"connection's targets are dropped. Logins are personal data and untrusted",
	Tags: []string{"matomo", "users", "access", "roles", "permissions", "personal-data"}, Risk: readRisk(usersSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"login":{"type":"string","minLength":1,"maxLength":100},` +
		`"idSite":{"type":"integer","minimum":1,"maximum":999999999},"access":{"type":"string","pattern":"` + accessPattern +
		`","maxLength":64},"role":{"type":"string","enum":[` + quotedList(siteRoleFilters) + `]},` +
		`"search":{"type":"string","minLength":1,"maxLength":100},"raw":{"type":"boolean"},` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sites":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"integer"},"name":{"type":"string"},"role":{"type":"string"},` +
		`"capabilities":{"type":"array","items":{"type":"string"}}},"required":["id"],"additionalProperties":false}},` +
		`"entries":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"access":{"type":"string"}},"required":["id"],"additionalProperties":false}},` +
		`"users":{"type":"array","items":{"type":"object","properties":{` + userProperties + `,"access":{"type":"string"},` +
		`"sites":{"type":"array","items":{"type":"integer"}}},"required":["login"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "login", Description: "User whose websites are read, up to 100 characters; exclusive with idSite"},
		{Name: "idSite", Target: "site", Description: "Website whose users are read; must lie inside the connection's " +
			"targets; never defaulted; exclusive with login"},
		{Name: "access", Description: "Role or capability ID such as view, write, admin, or a capability of a plugin. " +
			"With idSite: only users that hold it. Without login and idSite: required, the websites per user"},
		{Name: "role", Description: "With login: only websites where the user holds this role: " +
			strings.Join(siteRoleFilters, ", ") + "; some means any access"},
		{Name: "search", Description: "With login: text the website name, address, or group must contain, up to 100 characters"},
		{Name: "raw", Description: "With login: true reads every access entry of the user, one row per role or " +
			"capability, with Super User rights and the instance target; cannot be combined with role or search"},
	}, pagingArguments...),
	Fields: slices.Concat([]capability.Field{
		{Name: "sites", Description: "With login: websites inside the targets with the user's role and capabilities, " +
			"in Matomo's order (name, then ID)"},
		{Name: "sites[].id", Description: "Website ID"},
		{Name: "sites[].name", Description: "Website name, untrusted"},
		{Name: "sites[].role", Description: "Role of the user on the website; noaccess when only capabilities are assigned"},
		{Name: "sites[].capabilities", Description: "Capabilities assigned to the user on the website, at most 100, untrusted"},
		{Name: "entries", Description: "With login and raw: one row per access entry, ordered by website and access; a " +
			"Super User gets admin on every website"},
		{Name: "entries[].id", Description: "Website ID"},
		{Name: "entries[].access", Description: "Role or capability ID, untrusted"},
		{Name: "users", Description: "With idSite or with access alone: users ordered by login"},
		{Name: "users[].login", Description: "Login of the user, untrusted"},
		{Name: "users[].access", Description: "With idSite: the access entry Matomo reports for the user on the website, or " +
			"the access asked for"},
		{Name: "users[].sites", Description: "With access alone: IDs of the websites inside the targets where the user holds it"},
	}, userFields("users[].")[1:], pagingFields),
	Examples: []capability.Example{
		{Description: "List the websites of a user", Arguments: json.RawMessage(`{"login":"alice"}`)},
		{Description: "List the users of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "List the admins of website 1", Arguments: json.RawMessage(`{"idSite":1,"access":"admin"}`)},
	},
}

// SiteAccess is the access of one user to one website.
type SiteAccess struct {
	ID           int      `json:"id"`
	Name         string   `json:"name,omitempty"`
	Role         string   `json:"role,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// AccessEntry is one raw access row of a user.
type AccessEntry struct {
	ID     int    `json:"id"`
	Access string `json:"access,omitempty"`
}

// AccessUser is a user in an access answer.
type AccessUser struct {
	User
	Access string `json:"access,omitempty"`
	Sites  []int  `json:"sites,omitempty"`
}

// UsersGetAccessResult is the answer of users.get-access.
type UsersGetAccessResult struct {
	Sites      []SiteAccess  `json:"sites,omitempty"`
	Entries    []AccessEntry `json:"entries,omitempty"`
	Users      []AccessUser  `json:"users,omitempty"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// objectOf reads a PHP array that is a map: Matomo answers an empty one as [].
func objectOf[T any](raw json.RawMessage) (map[string]T, bool) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("[]")) {
		return map[string]T{}, true
	}
	var table map[string]T
	return table, bytes.HasPrefix(trimmed, []byte("{")) && json.Unmarshal(trimmed, &table) == nil
}

func invokeUsersGetAccess(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get user access"
	var args struct {
		Login  *string `json:"login"`
		IDSite *int    `json:"idSite"`
		Access *string `json:"access"`
		Role   *string `json:"role"`
		Search *string `json:"search"`
		Raw    bool    `json:"raw"`
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
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if args.Login != nil && args.IDSite != nil {
		return nil, invalidArgument("login and idSite cannot be combined")
	}
	access := ""
	if args.Access != nil {
		access = *args.Access
		if !validAccessID(access) {
			return nil, invalidArgument("access must be a role or capability ID of letters, digits, and underscores")
		}
	}
	search, role := "", ""
	if args.Search != nil {
		if search = strings.TrimSpace(*args.Search); !validText(search, maxSearchLength) {
			return nil, invalidArgument("search must hold 1 to 100 characters")
		}
	}
	if args.Role != nil {
		if role = *args.Role; !slices.Contains(siteRoleFilters, role) {
			return nil, invalidArgument("role must be one of " + strings.Join(siteRoleFilters, ", "))
		}
	}
	mode := ""
	var login string
	var id int
	switch {
	case args.Login != nil:
		if login = *args.Login; !validLogin(login) {
			return nil, invalidArgument("login must hold 1 to 100 characters without control characters")
		}
		if args.Access != nil {
			return nil, invalidArgument("access applies only without login")
		}
		mode = "login"
		if args.Raw {
			if args.Role != nil || args.Search != nil {
				return nil, invalidArgument("raw cannot be combined with role or search")
			}
			if err := requireInstance(bound); err != nil {
				return nil, err
			}
			mode = "raw"
		}
	case args.IDSite != nil:
		if args.Raw || args.Role != nil || args.Search != nil {
			return nil, invalidArgument("raw, role, and search apply only with login")
		}
		var err error
		if id, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
		mode = "site"
		if args.Access != nil {
			mode = "site-access"
		}
	default:
		if args.Raw || args.Role != nil || args.Search != nil {
			return nil, invalidArgument("raw, role, and search apply only with login")
		}
		if args.Access == nil {
			return nil, invalidArgument("login, idSite, or access is required")
		}
		if err := requireInstance(bound); err != nil {
			return nil, err
		}
		mode = "access"
	}
	binding := provider.CursorBinding(usersGetAccess.ID, bound.allSites, sortedSites(bound), mode, login, id, access, role, search)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(usersGetAccess.ID)
	switch mode {
	case "login":
		return userSitesAccess(ctx, client, op, req, bound, paging, login, role, search)
	case "raw":
		var rows []struct {
			Site   *loose `json:"site"`
			Access *loose `json:"access"`
		}
		if err := client.call(ctx, op, req, "UsersManager.getSitesAccessFromUser", url.Values{"userLogin": {login}}, &rows); err != nil {
			return nil, err
		}
		var kept []AccessEntry
		for _, row := range rows {
			if site := row.Site.number(); site != nil && *site > 0 && bound.allows(*site) {
				kept = append(kept, AccessEntry{ID: *site, Access: row.Access.str(maxStringLength)})
			}
		}
		slices.SortStableFunc(kept, func(a, b AccessEntry) int {
			if a.ID != b.ID {
				return a.ID - b.ID
			}
			return strings.Compare(a.Access, b.Access)
		})
		batch, next := slice(paging, kept)
		if batch == nil {
			batch = []AccessEntry{}
		}
		return &UsersGetAccessResult{Entries: batch, HasMore: next != "", NextCursor: next}, nil
	case "site":
		var answer json.RawMessage
		params := url.Values{"idSite": {strconv.Itoa(id)}}
		if err := client.call(ctx, op, req, "UsersManager.getUsersAccessFromSite", params, &answer); err != nil {
			return nil, err
		}
		table, valid := objectOf[loose](answer)
		if !valid {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
		users := make([]AccessUser, 0, len(table))
		for name, entry := range table {
			if name = boundString(strings.TrimSpace(name), maxStringLength); name != "" {
				users = append(users, AccessUser{User: User{Login: name}, Access: entry.str(maxStringLength)})
			}
		}
		return pageAccessUsers(paging, users), nil
	case "site-access":
		var rows []userJSON
		params := url.Values{"idSite": {strconv.Itoa(id)}, "access": {access}}
		if err := client.call(ctx, op, req, "UsersManager.getUsersWithSiteAccess", params, &rows); err != nil {
			return nil, err
		}
		users := make([]AccessUser, 0, len(rows))
		for _, user := range usersOf(rows) {
			users = append(users, AccessUser{User: user, Access: access})
		}
		return pageAccessUsers(paging, users), nil
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "UsersManager.getUsersSitesFromAccess", url.Values{"access": {access}}, &answer); err != nil {
		return nil, err
	}
	table, valid := objectOf[[]loose](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
	}
	var users []AccessUser
	for name, ids := range table {
		var sites []int
		for i := range ids {
			if site := ids[i].number(); site != nil && bound.allows(*site) && len(sites) < maxSitesPerUser {
				sites = append(sites, *site)
			}
		}
		// A user whose websites all lie outside the targets is dropped with them.
		if name = boundString(strings.TrimSpace(name), maxStringLength); name != "" && len(sites) > 0 {
			slices.Sort(sites)
			users = append(users, AccessUser{User: User{Login: name}, Sites: sites})
		}
	}
	return pageAccessUsers(paging, users), nil
}

func pageAccessUsers(paging page, users []AccessUser) *UsersGetAccessResult {
	slices.SortStableFunc(users, func(a, b AccessUser) int { return strings.Compare(a.Login, b.Login) })
	batch, next := slice(paging, users)
	if batch == nil {
		batch = []AccessUser{}
	}
	return &UsersGetAccessResult{Users: batch, HasMore: next != "", NextCursor: next}
}

// userSitesAccess reads getSitesAccessForUser. With every website in scope Matomo pages the list; with
// single websites the rows are checked against the targets here, so a bounded number of rows is read.
func userSitesAccess(ctx context.Context, client *Client, op string, req requirement, bound scope, paging page,
	login, role, search string) (any, error) {
	params := url.Values{"userLogin": {login}}
	if role != "" {
		params.Set("filter_access", role)
	}
	if search != "" {
		params.Set("filter_search", search)
	}
	if bound.allSites {
		params.Set("limit", strconv.Itoa(paging.limit+1))
		params.Set("offset", strconv.Itoa(paging.offset))
	} else {
		params.Set("limit", strconv.Itoa(scanLimit))
	}
	var rows []struct {
		ID           *loose  `json:"idsite"`
		Name         *loose  `json:"site_name"`
		Role         *loose  `json:"role"`
		Capabilities []loose `json:"capabilities"`
	}
	if err := client.call(ctx, op, req, "UsersManager.getSitesAccessForUser", params, &rows); err != nil {
		return nil, err
	}
	if !bound.allSites && len(rows) >= scanLimit {
		return nil, invalidArgument("the user has too many websites to check against the targets; narrow role or search")
	}
	var kept []SiteAccess
	for _, row := range rows {
		site := row.ID.number()
		if site == nil || *site < 1 || !bound.allows(*site) {
			continue
		}
		entry := SiteAccess{ID: *site, Name: row.Name.str(maxStringLength), Role: row.Role.str(maxStringLength)}
		for i := range row.Capabilities {
			if text := row.Capabilities[i].str(maxStringLength); text != "" && len(entry.Capabilities) < maxCapabilities {
				entry.Capabilities = append(entry.Capabilities, text)
			}
		}
		kept = append(kept, entry)
	}
	var batch []SiteAccess
	var next string
	if bound.allSites {
		batch, next = sliceWindow(paging, kept)
	} else {
		batch, next = slice(paging, kept)
	}
	if batch == nil {
		batch = []SiteAccess{}
	}
	return &UsersGetAccessResult{Sites: batch, HasMore: next != "", NextCursor: next}, nil
}

func validAccessID(access string) bool {
	if access == "" || len(access) > maxAccessLength || !(access[0] >= 'A' && access[0] <= 'Z' || access[0] >= 'a' && access[0] <= 'z') {
		return false
	}
	for i := 1; i < len(access); i++ {
		c := access[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
