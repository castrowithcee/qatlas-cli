package github

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The account tool reads the identity GitHub authenticates the connection's token as, with every member
// GitHub reports for it, its billing plan and two-factor state among them. It names no repository, project, or
// owner and is offered only by a connection whose targets name neither a repository nor a project, because
// such a connection is scoped to those, and the account behind its token may belong to a customer other than
// the one its targets name.

var accountProperties = `"login":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
	`"plan":{"type":"string"},` + accountFacetsProperties + `,` + accountProfileProperties + `,` +
	`"url":{"type":"string"},"email":{"type":"string"},"notification_email":{"type":"string"},` +
	`"twitter_username":{"type":"string"},"private_gists":{"type":"integer"},` +
	`"total_private_repos":{"type":"integer"},"owned_private_repos":{"type":"integer"},` +
	`"disk_usage":{"type":"integer"},"collaborators":{"type":"integer"},` +
	`"two_factor_authentication":{"type":"boolean"},"business_plus":{"type":"boolean"},` +
	`"ldap_dn":{"type":"string"},"plan_detail":` + objectSchema(`"name":{"type":"string"},"space":{"type":"integer"},`+
	`"collaborators":{"type":"integer"},"private_repos":{"type":"integer"}`)

var accountsMe = capability.Descriptor{
	ID:      Provider + ".accounts.me",
	Version: 1,
	Title:   "Get the GitHub account behind this connection",
	Description: "Read the login, display name, account type, and billing plan of the user GitHub " +
		"authenticates the connection's token as; offered only by a connection whose targets name no " +
		"repository and no project; profile counts include repositories the connection excludes",
	Tags:        []string{"github", "account", "discovery", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + accountProperties + `},` +
		`"required":["login","type"],"additionalProperties":false}`),
	Fields: append(append(append([]capability.Field{
		{Name: "login", Description: "Login of the authenticated account"},
		{Name: "name", Description: "Display name, untrusted data; absent when the account has none set"},
		{Name: "type", Description: "User or Organization"},
		{Name: "plan", Description: "Name of the billing plan; absent when GitHub does not report one"},
	}, accountFacetsFields...), accountProfileFields...), []capability.Field{
		{Name: "url", Description: "Web address of the account, untrusted data, never fetched"},
		{Name: "email", Description: "Public email address, untrusted data"},
		{Name: "notification_email", Description: "Email address notifications go to, untrusted data"},
		{Name: "twitter_username", Description: "Twitter or X username, untrusted data"},
		{Name: "private_gists", Description: "Number of private gists"},
		{Name: "total_private_repos", Description: "Number of private repositories"},
		{Name: "owned_private_repos", Description: "Number of private repositories the account owns"},
		{Name: "disk_usage", Description: "Disk usage as GitHub counts it"},
		{Name: "collaborators", Description: "Number of collaborators on the account's private repositories"},
		{Name: "two_factor_authentication", Description: "Whether this account has two-factor authentication enabled"},
		{Name: "business_plus", Description: "Whether the account has the Business Plus plan"},
		{Name: "ldap_dn", Description: "LDAP distinguished name, untrusted data"},
		{Name: "plan_detail", Description: "Plan behind plan: name, space, collaborators, private_repos; only " +
			"when it adds more than the name; untrusted data"},
	}...),
	Examples: []capability.Example{{
		Description: "Read the account behind the connection",
		Arguments:   json.RawMessage(`{}`),
	}},
}

// Account is the identity GitHub reports for the account behind a connection's token.
type Account struct {
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
	Type  string `json:"type"`
	Plan  string `json:"plan,omitempty"`
	AccountFacets
	AccountProfile
	URL                     *string      `json:"url,omitempty"`
	Email                   *string      `json:"email,omitempty"`
	NotificationEmail       *string      `json:"notification_email,omitempty"`
	TwitterUsername         *string      `json:"twitter_username,omitempty"`
	PrivateGists            *int64       `json:"private_gists,omitempty"`
	TotalPrivateRepos       *int64       `json:"total_private_repos,omitempty"`
	OwnedPrivateRepos       *int64       `json:"owned_private_repos,omitempty"`
	DiskUsage               *int64       `json:"disk_usage,omitempty"`
	Collaborators           *int64       `json:"collaborators,omitempty"`
	TwoFactorAuthentication *bool        `json:"two_factor_authentication,omitempty"`
	BusinessPlus            *bool        `json:"business_plus,omitempty"`
	LdapDN                  *string      `json:"ldap_dn,omitempty"`
	PlanDetail              *AccountPlan `json:"plan_detail,omitempty"`
}

// AccountPlan is the billing plan behind the flat plan name.
type AccountPlan struct {
	Name          *string `json:"name,omitempty"`
	Space         *int64  `json:"space,omitempty"`
	Collaborators *int64  `json:"collaborators,omitempty"`
	PrivateRepos  *int64  `json:"private_repos,omitempty"`
}

// accountRead is the answer of GET /user: the members the tool always answered, read as before, and the
// ones it answers in addition.
type accountRead struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Plan  *struct {
		Name          string `json:"name"`
		Space         *int64 `json:"space"`
		Collaborators *int64 `json:"collaborators"`
		PrivateRepos  *int64 `json:"private_repos"`
	} `json:"plan"`
	profile profileRaw
	account accountRaw
	extra   struct {
		NotificationEmail       *string `json:"notification_email"`
		TwitterUsername         *string `json:"twitter_username"`
		PrivateGists            *int64  `json:"private_gists"`
		TotalPrivateRepos       *int64  `json:"total_private_repos"`
		OwnedPrivateRepos       *int64  `json:"owned_private_repos"`
		DiskUsage               *int64  `json:"disk_usage"`
		Collaborators           *int64  `json:"collaborators"`
		TwoFactorAuthentication *bool   `json:"two_factor_authentication"`
		BusinessPlus            *bool   `json:"business_plus"`
		LdapDN                  *string `json:"ldap_dn"`
	}
}

func (r *accountRead) UnmarshalJSON(data []byte) error {
	type plain accountRead
	if err := json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.profile); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.extra); err != nil {
		return err
	}
	return json.Unmarshal(data, &r.account)
}

// accountPermission names what a token needs to read its own account. GitHub decides on every request; the
// route itself asks for no scope, classic or fine-grained, beyond the token's own identity.
const accountPermission = "GitHub refused this token its own account, which get_me normally needs no scope " +
	"beyond the token's own identity to read, classic or fine-grained"

// getAccount reads the account behind the client's token.
func (c *Client) getAccount(ctx context.Context) (*Account, error) {
	const op = "get account"
	var raw accountRead
	if err := actionsFailure(c.rest(ctx, op, "/user", &raw), accountPermission); err != nil {
		return nil, err
	}
	if raw.Login == "" || raw.Type == "" {
		return nil, invalidEntry(op, "the account")
	}
	account := &Account{Login: raw.Login, Name: raw.Name, Type: raw.Type, AccountFacets: raw.account.facets(),
		AccountProfile: raw.profile.project(), URL: address(raw.account.HTMLURL), Email: shortText(raw.account.Email),
		NotificationEmail: shortText(raw.extra.NotificationEmail), TwitterUsername: shortText(raw.extra.TwitterUsername),
		PrivateGists: raw.extra.PrivateGists, TotalPrivateRepos: raw.extra.TotalPrivateRepos,
		OwnedPrivateRepos: raw.extra.OwnedPrivateRepos, DiskUsage: raw.extra.DiskUsage,
		Collaborators: raw.extra.Collaborators, TwoFactorAuthentication: raw.extra.TwoFactorAuthentication,
		BusinessPlus: raw.extra.BusinessPlus, LdapDN: shortText(raw.extra.LdapDN)}
	if raw.Plan != nil {
		account.Plan = raw.Plan.Name
		// The detail stays out while it would only repeat the plan name.
		if raw.Plan.Space != nil || raw.Plan.Collaborators != nil || raw.Plan.PrivateRepos != nil {
			account.PlanDetail = &AccountPlan{Name: shortText(&raw.Plan.Name), Space: raw.Plan.Space,
				Collaborators: raw.Plan.Collaborators, PrivateRepos: raw.Plan.PrivateRepos}
		}
	}
	return account, nil
}

func accountOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: accountsMe, Handler: capability.Handler(invokeAccountsMe)},
	}
}

// invokeAccountsMe checks the connection's targets before a credential is resolved, so a connection scoped
// to a repository or a project never resolves a secret to answer an account-wide read.
func invokeAccountsMe(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if err := accountWideAllowed(allowed); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.getAccount(ctx)
}
