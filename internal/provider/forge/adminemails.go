package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The email administration tools list the email addresses of all accounts of the instance and, on Forgejo,
// those of one account, and remove addresses of one account. Adding an address is not offered. Like the other
// instance administration they reach past any target, so only a connection without targets offers them.

const adminEmailsNote = "Addresses are personal data. Only a connection without targets offers this tool."

func adminEmailsListTool(group, action, path, title, what string, searches bool) *tool {
	return &tool{
		group: group, action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties, arguments := pagingKeys, append([]capability.Argument(nil), pagingArguments...)
			if searches {
				properties = `"q":{"type":"string","minLength":1,"maxLength":255},` + pagingKeys
				arguments = append([]capability.Argument{{Name: "q", Description: "Keyword the instance matches " +
					"against email address, user name, and display name; all addresses when omitted"}}, arguments...)
			}
			return capability.Descriptor{
				Title: title + p.name,
				Description: "List one batch of " + what + " of the whole instance as an administrator, with every " +
					"member of the email object; the addresses and names are untrusted personal data. " + adminEmailsNote,
				Tags:         []string{p.id, "admin", "emails", action},
				Risk:         readRisk(p.id + adminAccountsSensitivity),
				InputSchema:  inputSchema(properties),
				OutputSchema: instanceListOutput("emails", schemaOf(p, emailMembers)),
				Arguments:    arguments,
				Fields:       instanceListFields("emails", "Addresses with every member of the email object"),
				Examples:     []capability.Example{{Description: "List a batch", Arguments: json.RawMessage(`{"limit":20}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			if err := allowed.instanceWide(); err != nil {
				return nil, err
			}
			var a struct {
				Q      string `json:"q"`
				Limit  int    `json:"limit"`
				Cursor string `json:"cursor"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			query := url.Values{}
			if searches && a.Q != "" {
				if err := plainFilter("q", a.Q); err != nil {
					return nil, err
				}
				query.Set("q", a.Q)
			}
			page, err := startPaging(provider.CursorBinding(p.id, group+"."+action, "", path, query.Encode()), a.Limit,
				a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				return instancePage(ctx, c, "list instance emails", path, query, "emails", "", page,
					func(row object) map[string]any { return project(p, emailMembers, row) })
			}, nil
		},
	}
}

var (
	adminEmailsList = adminEmailsListTool("admin-emails", "list", "/admin/emails", "List all email addresses of ",
		"the email addresses", false)
	adminEmailsSearch = adminEmailsListTool("admin-emails", "search", "/admin/emails/search",
		"Search the email addresses of ", "the email addresses that match a keyword", true)
)

var adminUserEmailsList = &tool{
	group: "admin-user-emails", action: "list", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the email addresses of a " + p.name + " user",
			Description: "List the email addresses of one account as an administrator, at most " + itoa(maxRunnerRows) +
				", with every member of the email object; the instance answers without paging. " + adminEmailsNote,
			Tags:         []string{p.id, "admin", "emails", "list"},
			Risk:         readRisk(p.id + adminAccountsSensitivity),
			InputSchema:  inputSchema(`"username":`+issueSchemaLogin, "username"),
			OutputSchema: instanceListOutput("emails", schemaOf(p, emailMembers)),
			Arguments:    []capability.Argument{adminUserArgument},
			Fields:       instanceListFields("emails", "Addresses with every member of the email object"),
			Examples: []capability.Example{{Description: "List the addresses of an account",
				Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return instanceUnpaged(ctx, c, "list instance user emails", "/admin/users/"+escape(a.Username)+"/emails",
				url.Values{}, "emails", "", func(row object) map[string]any { return project(p, emailMembers, row) })
		}, nil
	},
}

var adminUserEmailsDelete = &tool{
	group: "admin-user-emails", action: "delete", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete email addresses of a " + p.name + " user",
			Description: "Remove up to " + itoa(maxDeleteMails) + " email addresses from one account as an " +
				"administrator. Adding an address is not offered. Idempotent; the request is sent once and never " +
				"repeated after an unclear outcome. Offered only by a connection without targets whose tools list " +
				"names it, in no profile",
			Tags:                  []string{p.id, "admin", "emails", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+adminAccountsSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"username":`+issueSchemaLogin+`,"emails":{"type":"array","minItems":1,"maxItems":`+
				itoa(maxDeleteMails)+`,"uniqueItems":true,"items":{"type":"string","minLength":3,"maxLength":254}}`,
				"username", "emails"),
			OutputSchema: adminUserOutput(`,"deleted":{"type":"boolean"},"count":{"type":"integer"}`, "deleted", "count"),
			Arguments: []capability.Argument{adminUserArgument, {Name: "emails", Required: true,
				Description: "Addresses to remove, 1 to " + itoa(maxDeleteMails) + ", without duplicates"}},
			Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
				{Name: "deleted", Description: "True once the instance accepted the request"},
				{Name: "count", Description: "Number of addresses named"}},
			Examples: []capability.Example{{Description: "Remove an address",
				Arguments: json.RawMessage(`{"username":"octo","emails":["octo@example.test"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string   `json:"username"`
			Emails   []string `json:"emails"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		if len(a.Emails) < 1 || len(a.Emails) > maxDeleteMails {
			return nil, invalidArgument("emails takes 1 to " + itoa(maxDeleteMails) + " addresses")
		}
		seen := map[string]bool{}
		for _, email := range a.Emails {
			if len(email) < 3 || len(email) > maxEmailLength || !plainText(email) || !strings.Contains(email, "@") ||
				strings.ContainsAny(email, " \t\r\n") {
				return nil, invalidArgument("emails must be email addresses of at most 254 bytes")
			}
			// Addresses compare without regard to case, so the narrower reading rejects such a pair.
			key := strings.ToLower(email)
			if seen[key] {
				return nil, invalidArgument("emails must not repeat an address")
			}
			seen[key] = true
		}
		body := map[string]any{"emails": a.Emails}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := c.changeQuery(ctx, "delete instance user emails", http.MethodDelete,
				"/admin/users/"+escape(a.Username)+"/emails", nil, body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "deleted": true, "count": len(a.Emails)}, nil
		}, nil
	},
}
