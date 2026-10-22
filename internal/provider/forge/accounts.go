package forge

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The account tool reads the user the instance authenticates the token as, with every member of the API's
// user object. It is offered only by a connection whose targets name no repository, because such a connection
// is scoped to that repository and the account may belong to another customer.

// personMembers are the members of a user object that describe any person. selfMembers adds those about the
// security of an account, which only the account's own read shows; a user object of another person, such as
// a repository owner, leaves them out.
var personMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the user"},
	{name: "login", kind: kindText, note: "User name"},
	{name: "full_name", kind: kindText, note: "Display name, untrusted data"},
	{name: "email", kind: kindText, note: "Email address the instance shows; personal data"},
	{name: "avatar_url", kind: kindText, note: "Address of the avatar image"},
	{name: "html_url", kind: kindText, note: "Address of the profile page"},
	{name: "language", kind: kindText, note: "Locale of the user"},
	{name: "is_admin", kind: kindFlag, note: "True for an instance administrator"},
	{name: "restricted", kind: kindFlag, note: "True for a restricted user"},
	{name: "active", kind: kindFlag, note: "True once the account is activated"},
	{name: "created", kind: kindText, note: "Creation time"},
	{name: "location", kind: kindText, note: "Location, untrusted data"},
	{name: "pronouns", kind: kindText, only: forgejo, note: "Pronouns, untrusted data"},
	{name: "website", kind: kindText, note: "Website, untrusted data"},
	{name: "description", kind: kindText, note: "Profile description, untrusted data"},
	{name: "visibility", kind: kindText, note: "public, limited, or private"},
	{name: "type", kind: kindText, only: gitea, note: "Kind of account, reported by Gitea 28 and later"},
	{name: "followers_count", kind: kindInteger, note: "Number of followers"},
	{name: "following_count", kind: kindInteger, note: "Number of accounts the user follows"},
	{name: "starred_repos_count", kind: kindInteger, note: "Number of starred repositories"},
}

var selfMembers = append(append([]member(nil), personMembers...),
	member{name: "last_login", kind: kindText, note: "Time of the last sign-in"},
	member{name: "login_name", kind: kindText, note: "Sign-in name at the external authentication source"},
	member{name: "source_id", kind: kindInteger, note: "ID of the authentication source, 0 for a local account"},
	member{name: "prohibit_login", kind: kindFlag, note: "True when signing in is prohibited"},
)

var accountsMe = &tool{
	group: "accounts", action: "me", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the " + p.name + " account behind this connection",
			Description: "Read every member of the user " + p.name + " authenticates the connection's token as; " +
				"offered only by a connection whose targets name no repository",
			Tags:         []string{p.id, "account", "get"},
			Risk:         readRisk(p.id + "-personal-data"),
			InputSchema:  inputSchema(""),
			OutputSchema: json.RawMessage(schemaOf(p, selfMembers, "id", "login")),
			Fields:       fieldsOf(p, selfMembers),
			Examples:     []capability.Example{{Description: "Read the account", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, _ json.RawMessage) (run, error) {
		if err := allowed.accountWide(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get account"
			var account object
			if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
				return nil, err
			}
			return project(p, selfMembers, account), nil
		}, nil
	},
}
