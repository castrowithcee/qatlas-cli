package github

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The account tool reads the identity GitHub authenticates the connection's token as: its login, display
// name, account type, and, when GitHub reports one, its billing plan. It names no repository, project, or
// owner and is offered only by a connection whose targets name neither a repository nor a project, because
// such a connection is scoped to those, and the account behind its token may belong to a customer other than
// the one its targets name.

const accountProperties = `"login":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
	`"plan":{"type":"string"}`

var accountsMe = capability.Descriptor{
	ID:      Provider + ".accounts.me",
	Version: 1,
	Title:   "Get the GitHub account behind this connection",
	Description: "Read the login, display name, account type, and billing plan of the user GitHub " +
		"authenticates the connection's token as; offered only by a connection whose targets name no " +
		"repository and no project",
	Tags:        []string{"github", "account", "discovery", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + accountProperties + `},` +
		`"required":["login","type"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "login", Description: "Login of the authenticated account"},
		{Name: "name", Description: "Display name, untrusted data; absent when the account has none set"},
		{Name: "type", Description: "User or Organization"},
		{Name: "plan", Description: "Name of the billing plan; absent when GitHub does not report one"},
	},
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
}

// accountPermission names what a token needs to read its own account. GitHub decides on every request; the
// route itself asks for no scope, classic or fine-grained, beyond the token's own identity.
const accountPermission = "GitHub refused this token its own account, which get_me normally needs no scope " +
	"beyond the token's own identity to read, classic or fine-grained"

// getAccount reads the account behind the client's token.
func (c *Client) getAccount(ctx context.Context) (*Account, error) {
	const op = "get account"
	var raw struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		Type  string `json:"type"`
		Plan  *struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if err := actionsFailure(c.rest(ctx, op, "/user", &raw), accountPermission); err != nil {
		return nil, err
	}
	if raw.Login == "" || raw.Type == "" {
		return nil, invalidEntry(op, "the account")
	}
	account := &Account{Login: raw.Login, Name: raw.Name, Type: raw.Type}
	if raw.Plan != nil {
		account.Plan = raw.Plan.Name
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
