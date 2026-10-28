package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The tools of the account behind the token: its settings, avatar, email addresses, and blocked users. All of
// them are bound to that account as in users.go (with targets, users/NAME proved by a read of the account, and
// no repository target). Adding an email address is not offered. Email addresses and blocked users are personal
// data; names and texts are untrusted data and the addresses are never fetched.

const (
	maxEmails      = 200
	maxDeleteMails = 20
	maxEmailLength = 254
)

// ---- settings -----------------------------------------------------------------------------------------------

var accountSettingsMembers = []member{
	{name: "full_name", kind: kindText, note: "Display name, untrusted data"},
	{name: "description", kind: kindText, note: "Profile description, untrusted data"},
	{name: "location", kind: kindText, note: "Location, untrusted data"},
	{name: "website", kind: kindText, note: "Website, untrusted data"},
	{name: "pronouns", kind: kindText, only: forgejo, note: "Pronouns, untrusted data"},
	{name: "language", kind: kindText, note: "Locale"},
	{name: "theme", kind: kindText, note: "Interface theme"},
	{name: "diff_view_style", kind: kindText, note: "Diff style, unified or split"},
	{name: "hide_activity", kind: kindFlag, note: "True when the activity is hidden"},
	{name: "hide_email", kind: kindFlag, note: "True when the email address is hidden"},
	{name: "hide_pronouns", kind: kindFlag, only: forgejo, note: "True when the pronouns are hidden"},
	{name: "enable_repo_unit_hints", kind: kindFlag, only: forgejo, note: "True when repository unit hints are shown"},
}

const settingsTextMax = 255

// settingsTexts and settingsFlags are the documented members of UserSettingsOptions per kind; forgejoSettings
// are the ones only Forgejo knows.
var settingsTexts = []string{"full_name", "description", "location", "website", "pronouns", "language", "theme",
	"diff_view_style"}
var settingsFlags = []string{"hide_activity", "hide_email", "hide_pronouns", "enable_repo_unit_hints"}
var forgejoSettings = map[string]bool{"pronouns": true, "hide_pronouns": true, "enable_repo_unit_hints": true}

var userSettingsGet = &tool{
	group: "user-settings", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the settings of the " + p.name + " account",
			Description: "Read the profile and interface settings of the account behind the token. With targets, " +
				"they must name the account as users/NAME. The texts are untrusted personal data.",
			Tags:         []string{p.id, "user-settings", "get"},
			Risk:         readRisk(p.id + userSensitivity),
			InputSchema:  inputSchema(""),
			OutputSchema: json.RawMessage(schemaOf(p, accountSettingsMembers)),
			Fields:       fieldsOf(p, accountSettingsMembers),
			Examples:     []capability.Example{{Description: "Read the settings", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, _ json.RawMessage) (run, error) {
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get user settings"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var settings object
			if _, err := c.get(ctx, op, "/user/settings", nil, &settings); err != nil {
				return nil, err
			}
			return project(p, accountSettingsMembers, settings), nil
		}, nil
	},
}

func settingsProperties(p *product) string {
	var props []string
	for _, name := range settingsTexts {
		if forgejoSettings[name] && p != forgejo {
			continue
		}
		props = append(props, `"`+name+`":{"type":"string","maxLength":255}`)
	}
	for _, name := range settingsFlags {
		if forgejoSettings[name] && p != forgejo {
			continue
		}
		props = append(props, `"`+name+`":{"type":"boolean"}`)
	}
	return strings.Join(props, ",")
}

var userSettingsUpdate = &tool{
	group: "user-settings", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		var arguments []capability.Argument
		for _, m := range accountSettingsMembers {
			if m.offered(p) {
				arguments = append(arguments, capability.Argument{Name: m.name, Description: m.note +
					"; only sent when given"})
			}
		}
		return capability.Descriptor{
			Title: "Update the settings of the " + p.name + " account",
			Description: "Change the given profile and interface settings of the account behind the token; members " +
				"that are left out stay as they are, and at least one must be given. Forgejo alone knows pronouns, " +
				"hide_pronouns, and enable_repo_unit_hints. With targets, a read of the account first proves that " +
				"they name it as users/NAME. The change is the one request.",
			Tags:         []string{p.id, "user-settings", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+userSensitivity),
			InputSchema:  inputSchema(settingsProperties(p)),
			OutputSchema: json.RawMessage(schemaOf(p, accountSettingsMembers)),
			Arguments:    arguments,
			Fields:       fieldsOf(p, accountSettingsMembers),
			Examples: []capability.Example{{Description: "Hide the email address",
				Arguments: json.RawMessage(`{"hide_email":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var given map[string]json.RawMessage
		if err := decodeArguments(raw, &given); err != nil {
			return nil, err
		}
		body := map[string]any{}
		for _, name := range settingsTexts {
			value, ok := given[name]
			if !ok {
				continue
			}
			var text string
			if forgejoSettings[name] && p != forgejo {
				return nil, invalidArgument(name + " is a Forgejo setting; " + p.name + " does not know it")
			}
			// The profile description is free text on the instance and may hold line breaks.
			plain := plainText
			if name == "description" {
				plain = plainTextAllowNewlines
			}
			if json.Unmarshal(value, &text) != nil || utf8.RuneCountInString(text) > settingsTextMax || !plain(text) {
				return nil, invalidArgument(name + " must hold at most 255 characters without control characters")
			}
			body[name] = text
		}
		for _, name := range settingsFlags {
			value, ok := given[name]
			if !ok {
				continue
			}
			var flag bool
			if forgejoSettings[name] && p != forgejo {
				return nil, invalidArgument(name + " is a Forgejo setting; " + p.name + " does not know it")
			}
			if json.Unmarshal(value, &flag) != nil {
				return nil, invalidArgument(name + " must be true or false")
			}
			body[name] = flag
		}
		if len(body) != len(given) {
			return nil, invalidArgument("an argument is not a documented setting")
		}
		if len(body) == 0 {
			return nil, invalidArgument("at least one setting must be given")
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "update user settings"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var settings object
			if err := c.change(ctx, op, http.MethodPatch, "/user/settings", body, &settings); err != nil {
				return nil, err
			}
			return project(p, accountSettingsMembers, settings), nil
		}, nil
	},
}

// ---- avatar -------------------------------------------------------------------------------------------------

var userAvatarsUpdate = &tool{
	group: "user-avatars", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Set the avatar of the " + p.name + " account",
			Description: "Replace the avatar of the account behind the token with a local image file of at most 4 " +
				"MiB, read from a directory the connection releases for reading and sent base64 encoded. With " +
				"targets, a read of the account first proves that they name it as users/NAME.",
			Tags:       []string{p.id, "user-avatars", "update"},
			Risk:       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+userSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(`"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema,
				localfile.LocalPathArgument),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
				`"required":["updated"],"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: localfile.LocalPathArgument, Required: true,
				Description: "Image file to upload, absolute or starting with ~/, inside a directory the " +
					"connection releases for reading"}},
			Fields: []capability.Field{{Name: "updated", Description: "True once the instance accepted the image"}},
			Examples: []capability.Example{{Description: "Set the avatar from a local file",
				Arguments: json.RawMessage(`{"local_path":"~/images/me.png"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var a struct {
			LocalPath *string `json:"local_path"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, nil, err
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, nil, err
		}
		body, release, err := avatarBody(ctx, resolved, a.LocalPath, "set user avatar")
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "set user avatar"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodPost, "/user/avatar", body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"updated": true}, nil
		}, release, nil
	},
}

var userAvatarsDelete = &tool{
	group: "user-avatars", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete the avatar of the " + p.name + " account",
			Description: "Remove the avatar of the account behind the token; the instance shows its default image " +
				"again. With targets, a read of the account first proves that they name it as users/NAME.",
			Tags:        []string{p.id, "user-avatars", "delete"},
			Risk:        changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+userSensitivity),
			InputSchema: inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
				`"required":["deleted"],"additionalProperties":false}`),
			Fields:   []capability.Field{{Name: "deleted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Delete the avatar", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, _ json.RawMessage) (run, error) {
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete user avatar"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, "/user/avatar", nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true}, nil
		}, nil
	},
}

// ---- email addresses ----------------------------------------------------------------------------------------

var emailMembers = []member{
	{name: "email", kind: kindText, note: "Email address; personal data"},
	{name: "primary", kind: kindFlag, note: "True for the primary address"},
	{name: "verified", kind: kindFlag, note: "True once the address is verified"},
	{name: "user_id", kind: kindInteger, note: "Numeric ID of the user"},
	{name: "username", kind: kindText, note: "User name"},
}

var userEmailsList = &tool{
	group: "user-emails", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the email addresses of the " + p.name + " account",
			Description: "List the email addresses of the account behind the token, at most 200, with every member " +
				"of the email object. With targets, a read of the account first proves that they name it as " +
				"users/NAME. The addresses are personal data and are never used by the tool.",
			Tags:        []string{p.id, "user-emails", "list"},
			Risk:        readRisk(p.id + userSensitivity),
			InputSchema: inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"emails":{"type":"array","items":` +
				schemaOf(p, emailMembers) + `},"truncated":{"type":"boolean"}},"required":["emails","truncated"],` +
				`"additionalProperties":false}`),
			Fields: []capability.Field{{Name: "emails", Description: "Addresses with every member of the email " +
				"object"}, {Name: "truncated", Description: "True when the instance sent more than 200 addresses"}},
			Examples: []capability.Example{{Description: "List the addresses", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, _ json.RawMessage) (run, error) {
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list user emails"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var rows []object
			if _, err := c.get(ctx, op, "/user/emails", nil, &rows); err != nil {
				return nil, err
			}
			truncated := len(rows) > maxEmails
			if truncated {
				rows = rows[:maxEmails]
			}
			emails := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					emails = append(emails, project(p, emailMembers, row))
				}
			}
			return map[string]any{"emails": emails, "truncated": truncated}, nil
		}, nil
	},
}

var userEmailsDelete = &tool{
	group: "user-emails", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete email addresses of the " + p.name + " account",
			Description: "Remove up to 20 email addresses from the account behind the token. The primary address " +
				"cannot be removed. Adding an address is not offered. Only a connection whose tools list names " +
				"this tool offers it. With targets, a read of the account first proves that they name it as " +
				"users/NAME. The change is the one request.",
			Tags:                  []string{p.id, "user-emails", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+userSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"emails":{"type":"array","minItems":1,"maxItems":20,"items":`+
				`{"type":"string","minLength":3,"maxLength":254}}`, "emails"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
				`"count":{"type":"integer"}},"required":["deleted","count"],"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "emails", Required: true, Description: "Addresses to remove, 1 to 20"}},
			Fields: []capability.Field{{Name: "deleted", Description: "True once the instance accepted the request"},
				{Name: "count", Description: "Number of addresses named"}},
			Examples: []capability.Example{{Description: "Remove an address",
				Arguments: json.RawMessage(`{"emails":["octo@example.invalid"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Emails []string `json:"emails"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if len(a.Emails) < 1 || len(a.Emails) > maxDeleteMails {
			return nil, invalidArgument("emails takes 1 to 20 addresses")
		}
		for _, email := range a.Emails {
			if len(email) < 3 || len(email) > maxEmailLength || !plainText(email) || !strings.Contains(email, "@") ||
				strings.ContainsAny(email, " \t\r\n") {
				return nil, invalidArgument("emails must be email addresses of at most 254 bytes")
			}
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete user emails"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, "/user/emails", map[string]any{"emails": a.Emails},
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "count": len(a.Emails)}, nil
		}, nil
	},
}

// ---- blocked users ------------------------------------------------------------------------------------------

var userBlocksList = &tool{
	group: "user-blocks", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the users blocked by the " + p.name + " account",
			Description: "List one batch of the users the account behind the token blocks. Gitea lists the user " +
				"objects, Forgejo only the block entries without a user. With targets, a read of the account first " +
				"proves that they name it as users/NAME. Names and texts are untrusted personal data.",
			Tags:        []string{p.id, "user-blocks", "list"},
			Risk:        readRisk(p.id + userSensitivity),
			InputSchema: inputSchema(pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"blocked":{"type":"array","items":` +
				schemaOf(p, orgPeopleRows(p, true)) + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["blocked","has_more"],"additionalProperties":false}`),
			Arguments: pagingArguments,
			Fields: append([]capability.Field{{Name: "blocked", Description: "Entries with every member of the " +
				"object"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the blocked users",
				Arguments: json.RawMessage(`{"limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "user-blocks.list", ""), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		path := "/user/blocks"
		if p == forgejo {
			path = "/user/list_blocked"
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list user blocks"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var rows []object
			header, err := c.get(ctx, op, path, page.query(), &rows)
			if err != nil {
				return nil, err
			}
			members := orgPeopleRows(p, true)
			entries := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					entries = append(entries, project(p, members, row))
				}
			}
			return pageResult(page, header, len(rows), "blocked", entries), nil
		}, nil
	},
}

var userBlocksCheck = &tool{
	group: "user-blocks", action: "check", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether the " + p.name + " account blocks a user",
			Description: "Ask whether the account behind the token blocks one user. The answer is a boolean; a 404 " +
				"is reported as not blocked. With targets, a read of the account first proves that they name it as " +
				"users/NAME.",
			Tags:        []string{p.id, "user-blocks", "check"},
			Risk:        readRisk(p.id + userSensitivity),
			InputSchema: inputSchema(`"username":`+issueSchemaLogin, "username"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},` +
				`"blocked":{"type":"boolean"}},"required":["username","blocked"],"additionalProperties":false}`),
			Arguments: []capability.Argument{orgUserArgument},
			Fields: []capability.Field{{Name: "username", Description: "User that was checked"},
				{Name: "blocked", Description: "True when the instance answered yes; false when it answered 404"}},
			Examples: []capability.Example{{Description: "Check a user", Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := orgUserOf(a.Username); err != nil {
			return nil, err
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "check user block"
			if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			code, err := c.status(ctx, op, "/user/blocks/"+escape(a.Username))
			result := map[string]any{"username": a.Username}
			switch {
			case err == nil:
				result["blocked"] = true
			case code == http.StatusNotFound:
				result["blocked"] = false
			default:
				return nil, err
			}
			return result, nil
		}, nil
	},
}

func newUserBlockChange(action, key, title, text string, note bool, request func(p *product, user string) (string, string)) *tool {
	return &tool{
		group: "user-blocks", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			props := `"username":` + issueSchemaLogin
			arguments := []capability.Argument{orgUserArgument}
			if note && p == gitea {
				props += `,"note":{"type":"string","maxLength":255}`
				arguments = append(arguments, capability.Argument{Name: "note",
					Description: "Optional note for the block, at most 255 characters; Gitea only"})
			}
			return capability.Descriptor{
				Title:       strings.ReplaceAll(title, "%s", p.name),
				Description: text,
				Tags:        []string{p.id, "user-blocks", action, "update"},
				Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+userSensitivity),
				InputSchema: inputSchema(props, "username"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},"` + key +
					`":{"type":"boolean"}},"required":["username","` + key + `"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields: []capability.Field{{Name: "username", Description: "User the tool acted on"},
					{Name: key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: strings.ReplaceAll(title, "%s", p.name),
					Arguments: json.RawMessage(`{"username":"octo"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Username string  `json:"username"`
				Note     *string `json:"note"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := orgUserOf(a.Username); err != nil {
				return nil, err
			}
			var query url.Values
			if a.Note != nil {
				if !note || p != gitea {
					return nil, invalidArgument("note is not an argument of this tool")
				}
				if utf8.RuneCountInString(*a.Note) > maxBlockNote || !plainText(*a.Note) {
					return nil, invalidArgument("note must hold at most 255 characters without control characters")
				}
				query = url.Values{"note": {*a.Note}}
			}
			if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			method, path := request(p, a.Username)
			return func(ctx context.Context, c *Client) (any, error) {
				op := action + " user block"
				if err := verifyOwnProfile(ctx, c, op, allowed); err != nil {
					return nil, err
				}
				if _, err := c.changeQuery(ctx, op, method, path, query, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"username": a.Username, key: true}, nil
			}, nil
		},
	}
}

var userBlocksAdd = newUserBlockChange("add", "blocked", "Block a user from the %s account",
	"Make the account behind the token block one user. Gitea takes an optional note. With targets, a read of the "+
		"account first proves that they name it as users/NAME. The change is the one request.", true,
	func(p *product, user string) (string, string) {
		if p == forgejo {
			return http.MethodPut, "/user/block/" + escape(user)
		}
		return http.MethodPut, "/user/blocks/" + escape(user)
	})

var userBlocksRemove = newUserBlockChange("remove", "unblocked", "Unblock a user from the %s account",
	"Lift the block of one user from the account behind the token. With targets, a read of the account first "+
		"proves that they name it as users/NAME. The change is the one request.", false,
	func(p *product, user string) (string, string) {
		if p == forgejo {
			return http.MethodPut, "/user/unblock/" + escape(user)
		}
		return http.MethodDelete, "/user/blocks/" + escape(user)
	})
