package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The user administration tools list the accounts of the whole instance and change, rename, or delete one as
// an administrator. Like the other instance administration they reach past any target, so only a connection
// without targets offers them. Creating an account is not offered. The change tool sends none of the members
// that hand over an account (password, email address, sign-in name, authentication source, forced password
// change) or that raise the rights of an account to the host (administrator, Git hooks, local import).

// adminAccountsSensitivity is its own class: these tools reach the accounts of all people, also the hidden
// ones, and the change tools end or change an account.
const adminAccountsSensitivity = "-admin-accounts"

// giteaUserSorts and forgejoUserSorts are the orders per product. Gitea sorts by an attribute and takes an
// order; Forgejo takes one combined value.
var (
	giteaUserSorts   = []string{"name", "created", "updated", "id"}
	forgejoUserSorts = []string{"oldest", "newest", "alphabetically", "reversealphabetically", "recentupdate",
		"leastupdate"}
)

var userVisibilities = []string{"public", "limited", "private"}

// adminUserFlags and adminUserTexts are the members the change tool sends, when given. The rest of
// EditUserOption is deliberately absent.
var (
	adminUserTexts = []string{"full_name", "description", "location", "website", "pronouns"}
	adminUserFlags = []string{"active", "prohibit_login", "restricted", "allow_create_organization", "hide_email"}
	// forgejoUserMembers are the members only Forgejo knows.
	forgejoUserMembers = []string{"pronouns", "hide_email"}
)

// EditUserOption documents source_id as required for Gitea, but it is omitted on purpose: the instance keeps the
// authentication source and the sign-in name when they are absent.

const maxRepoCreationCap = 1000000

var adminUserArgument = capability.Argument{Name: "username", Required: true,
	Description: "User name of the account, as admin-users.list reports it as login"}

const adminUserNote = "Only a connection without targets offers this tool."

func adminUserOutput(extra string, required ...string) json.RawMessage {
	names := `"username"`
	for _, name := range required {
		names += `,"` + name + `"`
	}
	return json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"}` + extra +
		`},"required":[` + names + `],"additionalProperties":false}`)
}

var adminUsersList = &tool{
	group: "admin-users", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"q":{"type":"string","minLength":1,"maxLength":255}`
		arguments := []capability.Argument{{Name: "q", Description: "Keyword the instance matches against user " +
			"name, display name, and email address"}}
		if p == forgejo {
			properties = `"sort":` + enumSchema(forgejoUserSorts)
			arguments = []capability.Argument{{Name: "sort", Description: "Forgejo only: " +
				strings.Join(forgejoUserSorts, ", ")}}
		} else {
			properties += `,"visibility":` + enumSchema(userVisibilities) + `,"is_active":{"type":"boolean"},` +
				`"is_admin":{"type":"boolean"},"is_restricted":{"type":"boolean"},"sort":` + enumSchema(giteaUserSorts) +
				`,"order":` + enumSchema([]string{"asc", "desc"})
			arguments = append(arguments,
				capability.Argument{Name: "visibility", Description: "Gitea only: only users with this visibility: " +
					strings.Join(userVisibilities, ", ")},
				capability.Argument{Name: "is_active", Description: "Gitea only: true lists the activated accounts, " +
					"false the others"},
				capability.Argument{Name: "is_admin", Description: "Gitea only: true lists the administrators, " +
					"false the others"},
				capability.Argument{Name: "is_restricted", Description: "Gitea only: true lists the restricted " +
					"accounts, false the others"},
				capability.Argument{Name: "sort", Description: "Gitea only: " + strings.Join(giteaUserSorts, ", ") +
					"; name when omitted"},
				capability.Argument{Name: "order", Description: "Gitea only: asc or desc; asc when omitted, ignored " +
					"without sort"})
		}
		example := capability.Example{Description: "List the accounts that match a keyword",
			Arguments: json.RawMessage(`{"q":"octo","limit":20}`)}
		if p == forgejo {
			example = capability.Example{Description: "List the newest accounts",
				Arguments: json.RawMessage(`{"sort":"newest","limit":20}`)}
		}
		return capability.Descriptor{
			Title: "List all " + p.name + " users",
			Description: "List one batch of the accounts of the whole instance as an administrator, with the " +
				"person members of each user object; names and texts are untrusted personal data. Sign-in " +
				"names, authentication sources, last sign-in, and security state of the accounts are neither " +
				"filtered by nor returned. " + adminUserNote,
			Tags:         []string{p.id, "admin", "users", "list"},
			Risk:         readRisk(p.id + adminAccountsSensitivity),
			InputSchema:  inputSchema(properties + `,` + pagingKeys),
			OutputSchema: instanceListOutput("users", schemaOf(p, personMembers, "id", "login")),
			Arguments:    append(arguments, pagingArguments...),
			Fields:       instanceListFields("users", "Users with the person members of the user object"),
			Examples:     []capability.Example{example},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Q            string `json:"q"`
			Visibility   string `json:"visibility"`
			IsActive     *bool  `json:"is_active"`
			IsAdmin      *bool  `json:"is_admin"`
			IsRestricted *bool  `json:"is_restricted"`
			Sort         string `json:"sort"`
			Order        string `json:"order"`
			Limit        int    `json:"limit"`
			Cursor       string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		query := url.Values{}
		sorts := giteaUserSorts
		if p == forgejo {
			sorts = forgejoUserSorts
			if a.Q != "" || a.Visibility != "" || a.IsActive != nil || a.IsAdmin != nil || a.IsRestricted != nil ||
				a.Order != "" {
				return nil, invalidArgument("q, visibility, is_active, is_admin, is_restricted, and order are Gitea " +
					"arguments; Forgejo filters by sort only")
			}
		}
		if a.Q != "" {
			if err := plainFilter("q", a.Q); err != nil {
				return nil, err
			}
			query.Set("q", a.Q)
		}
		if a.Visibility != "" {
			if !contains(userVisibilities, a.Visibility) {
				return nil, invalidArgument("visibility must be public, limited, or private")
			}
			query.Set("visibility", a.Visibility)
		}
		for name, flag := range map[string]*bool{"is_active": a.IsActive, "is_admin": a.IsAdmin,
			"is_restricted": a.IsRestricted} {
			if flag != nil {
				query.Set(name, boolText(*flag))
			}
		}
		if a.Sort != "" {
			if !contains(sorts, a.Sort) {
				return nil, invalidArgument("sort is not one of the documented orders")
			}
			query.Set("sort", a.Sort)
		}
		if a.Order != "" {
			if a.Order != "asc" && a.Order != "desc" {
				return nil, invalidArgument("order must be asc or desc")
			}
			query.Set("order", a.Order)
		}
		const path = "/admin/users"
		page, err := startPaging(provider.CursorBinding(p.id, "admin-users.list", "", path, query.Encode()), a.Limit,
			a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return instancePage(ctx, c, "list instance users", path, query, "users", "", page,
				func(row object) map[string]any { return project(p, personMembers, row) })
		}, nil
	},
}

func adminUserProperties(p *product) string {
	var props []string
	for _, name := range adminUserTexts {
		if p != forgejo && contains(forgejoUserMembers, name) {
			continue
		}
		props = append(props, `"`+name+`":{"type":"string","maxLength":255}`)
	}
	for _, name := range adminUserFlags {
		if p != forgejo && contains(forgejoUserMembers, name) {
			continue
		}
		props = append(props, `"`+name+`":{"type":"boolean"}`)
	}
	props = append(props, `"visibility":`+enumSchema(userVisibilities),
		`"max_repo_creation":{"type":"integer","minimum":-1,"maximum":`+itoa(maxRepoCreationCap)+`}`)
	return `"username":` + issueSchemaLogin + `,` + strings.Join(props, ",")
}

// adminUserChange builds the body of the change tool from the arguments given; only the documented members
// below ever enter it.
func adminUserChange(p *product, given map[string]json.RawMessage) (map[string]any, error) {
	body := map[string]any{}
	if p != forgejo {
		for _, name := range forgejoUserMembers {
			if _, ok := given[name]; ok {
				return nil, invalidArgument(name + " is a Forgejo member; " + p.name + " does not know it")
			}
		}
	}
	for _, name := range adminUserTexts {
		value, ok := given[name]
		if !ok {
			continue
		}
		var text string
		plain := plainText
		if name == "description" {
			plain = plainTextAllowNewlines
		}
		if json.Unmarshal(value, &text) != nil || utf8.RuneCountInString(text) > settingsTextMax || !plain(text) {
			return nil, invalidArgument(name + " must hold at most 255 characters without control characters")
		}
		body[name] = text
	}
	for _, name := range adminUserFlags {
		value, ok := given[name]
		if !ok {
			continue
		}
		var flag bool
		if json.Unmarshal(value, &flag) != nil {
			return nil, invalidArgument(name + " must be true or false")
		}
		body[name] = flag
	}
	if value, ok := given["visibility"]; ok {
		var text string
		if json.Unmarshal(value, &text) != nil || !contains(userVisibilities, text) {
			return nil, invalidArgument("visibility must be public, limited, or private")
		}
		body["visibility"] = text
	}
	if value, ok := given["max_repo_creation"]; ok {
		var n int64
		if json.Unmarshal(value, &n) != nil || n < -1 || n > maxRepoCreationCap {
			return nil, invalidArgument("max_repo_creation must be -1 for no limit or a count up to " +
				itoa(maxRepoCreationCap))
		}
		body["max_repo_creation"] = n
	}
	return body, nil
}

var adminUsersUpdate = &tool{
	group: "admin-users", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		arguments := []capability.Argument{adminUserArgument,
			{Name: "active", Description: "False deactivates the account, true activates it"},
			{Name: "prohibit_login", Description: "True prohibits the sign-in of the account, false allows it"},
			{Name: "restricted", Description: "True restricts the account, false lifts the restriction"},
			{Name: "allow_create_organization", Description: "Whether the user may create organizations"},
			{Name: "hide_email", Description: "Forgejo only: whether the email address is hidden from other users"},
			{Name: "max_repo_creation", Description: "Number of repositories the user may create, -1 for no limit"},
			{Name: "visibility", Description: "public, limited, or private"},
			{Name: "full_name", Description: "Display name"},
			{Name: "description", Description: "Profile description"},
			{Name: "location", Description: "Location"},
			{Name: "website", Description: "Website"},
			{Name: "pronouns", Description: "Forgejo only: pronouns"}}
		if p != forgejo {
			arguments = slices.DeleteFunc(arguments, func(a capability.Argument) bool {
				return contains(forgejoUserMembers, a.Name)
			})
		}
		return capability.Descriptor{
			Title: "Update a " + p.name + " user",
			Description: "Change the given members of one account as an administrator: lock or unlock it " +
				"(prohibit_login, active), restrict it, set its limits, visibility, and profile texts. Members " +
				"that are left out stay as they are and at least one must be given. Password, email address, " +
				"sign-in name, authentication source, forced password change, and the rights to administer, to " +
				"use Git hooks, and to import local repositories cannot be set. Forgejo alone knows hide_email and " +
				"pronouns. " +
				"Idempotent; the request is sent once and never repeated after an unclear outcome, and the " +
				"result carries the user as the instance answers it. " + adminUserNote,
			Tags:         []string{p.id, "admin", "users", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+adminAccountsSensitivity),
			InputSchema:  inputSchema(adminUserProperties(p), "username"),
			OutputSchema: adminUserOutput(`,"user":`+schemaOf(p, personMembers, "id", "login"), "user"),
			Arguments:    arguments,
			Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
				{Name: "user", Description: "The user with the person members as the instance answers it"}},
			Examples: []capability.Example{{Description: "Prohibit the sign-in of an account",
				Arguments: json.RawMessage(`{"username":"octo","prohibit_login":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var given map[string]json.RawMessage
		if err := decodeArguments(raw, &given); err != nil {
			return nil, err
		}
		var name string
		if json.Unmarshal(given["username"], &name) != nil {
			return nil, invalidArgument("username must be a user name")
		}
		if err := userNameOf("username", name); err != nil {
			return nil, err
		}
		delete(given, "username")
		body, err := adminUserChange(p, given)
		if err != nil {
			return nil, err
		}
		if len(body) != len(given) {
			return nil, invalidArgument("an argument is not a documented member")
		}
		if len(body) == 0 {
			return nil, invalidArgument("at least one member must be given")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "update instance user"
			var row object
			if _, err := c.changeQuery(ctx, op, http.MethodPatch, "/admin/users/"+escape(name), nil, body, &row); err != nil {
				return nil, err
			}
			if login, ok := row.text("login"); !ok || !strings.EqualFold(login, name) {
				return nil, provider.InvalidResponse(op, c.product.name+" returned a user other than the requested one")
			}
			return map[string]any{"username": name, "user": project(p, personMembers, row)}, nil
		}, nil
	},
}

var adminUsersRename = &tool{
	group: "admin-users", action: "rename", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Rename a " + p.name + " user",
			Description: "Rename one account as an administrator. Its addresses change, and the old name may be " +
				"taken by someone else afterwards. Not idempotent; the request is sent once and never repeated " +
				"after an unclear outcome. Offered only by a connection without targets whose tools list names " +
				"it, in no profile",
			Tags:                  []string{p.id, "admin", "users", "rename", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+adminAccountsSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"username":`+issueSchemaLogin+`,"new_username":`+issueSchemaLogin, "username",
				"new_username"),
			OutputSchema: adminUserOutput(`,"new_username":{"type":"string"},"renamed":{"type":"boolean"}`,
				"new_username", "renamed"),
			Arguments: []capability.Argument{adminUserArgument, {Name: "new_username", Required: true,
				Description: "New user name; the instance refuses a name another account uses"}},
			Fields: []capability.Field{{Name: "username", Description: "Former user name"},
				{Name: "new_username", Description: "New user name"},
				{Name: "renamed", Description: "True once the instance renamed the account"}},
			Examples: []capability.Example{{Description: "Rename an account",
				Arguments: json.RawMessage(`{"username":"octo","new_username":"octocat"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username    string `json:"username"`
			NewUsername string `json:"new_username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		if err := userNameOf("new_username", a.NewUsername); err != nil {
			return nil, err
		}
		body := map[string]any{"new_username": a.NewUsername}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "rename instance user", http.MethodPost, "/admin/users/"+escape(a.Username)+"/rename",
				body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "new_username": a.NewUsername, "renamed": true}, nil
		}, nil
	},
}

var adminUsersDelete = &tool{
	group: "admin-users", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " user",
			Description: "Delete one account as an administrator for good. Without purge the instance refuses an " +
				"account that still owns repositories or organizations; with purge true it removes the account " +
				"and everything it owns, such as its repositories, completely. Idempotent; the request is sent " +
				"once and never repeated after an unclear outcome. Offered only by a connection without targets " +
				"whose tools list names it, in no profile",
			Tags:                  []string{p.id, "admin", "users", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+adminAccountsSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"username":`+issueSchemaLogin+`,"purge":{"type":"boolean"}`, "username"),
			OutputSchema:          adminUserOutput(`,"deleted":{"type":"boolean"}`, "deleted"),
			Arguments: []capability.Argument{adminUserArgument, {Name: "purge", Description: "True removes the " +
				"account and everything it owns completely; false when omitted"}},
			Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
				{Name: "deleted", Description: "True once the instance removed the account"}},
			Examples: []capability.Example{{Description: "Delete an account",
				Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
			Purge    *bool  `json:"purge"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		query := url.Values{}
		if a.Purge != nil {
			query.Set("purge", boolText(*a.Purge))
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := c.changeQuery(ctx, "delete instance user", http.MethodDelete, "/admin/users/"+escape(a.Username),
				query, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "deleted": true}, nil
		}, nil
	},
}
