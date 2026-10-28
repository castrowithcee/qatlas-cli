package forge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The access token tools show the metadata of existing tokens and revoke the calling token. No token is created,
// and the value of a token never appears: sha1 and token_last_eight are not in any projection, so neither the
// value nor a part of it is shown. Forgejo lists the tokens of the account behind the token; Gitea answers that
// list, and Forgejo the deletion of a listed token, only to Basic Auth, which this provider never uses, so
// neither is wired. Gitea describes and revokes the calling token itself.

var accessTokenMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the token"},
	{name: "name", kind: kindText, note: "Name of the token, untrusted data"},
	{name: "scopes", kind: kindTexts, note: "Scopes of the token"},
	{name: "repositories", kind: kindObjects, only: forgejo, sub: repositoryReferenceMembers,
		note: "Repositories the token is limited to; left out for a token without such a limit"},
	{name: "created_at", kind: kindText, note: "Creation time, Forgejo 17 and later"},
	{name: "last_used_at", kind: kindText, only: gitea, note: "Time of the last use"},
}

var currentTokenMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the token"},
	{name: "name", kind: kindText, note: "Name of the token, untrusted data"},
	{name: "scopes", kind: kindTexts, note: "Scopes of the token"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "last_used_at", kind: kindText, note: "Time of the last use"},
	{name: "user", kind: kindObject, sub: keyOwnerMembers, note: "Account the token belongs to"},
}

// ownLogin reads the account behind the token and returns its login, which must be one the targets name.
func ownLogin(ctx context.Context, c *Client, op string, allowed allowlist) (string, error) {
	var account object
	if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
		return "", err
	}
	login, ok := account.text("login")
	if !ok || !validName(login) {
		return "", provider.InvalidResponse(op, c.product.name+" returned an account without a usable login")
	}
	if allowed.bound() && !allowed.namesOwnerOfScope("users", login) {
		return "", outsideTargets("account behind the token")
	}
	return login, nil
}

var accessTokensList = &tool{
	group: "access-tokens", action: "list", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the access tokens of a " + p.name + " account",
			Description: "List one batch of the metadata of the access tokens of the account behind the token: name, ID, " +
				"scopes, and limits. The token values and parts of them are never shown. Only the own account is " +
				"read; tokens of other people belong to the administration. Targets must name the account as " +
				"users/NAME, or be unset, and a read of the account proves it first. No token is created.",
			Tags:        []string{p.id, "access-tokens", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},` +
				`"access_tokens":{"type":"array","items":` + schemaOf(p, accessTokenMembers) + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["username","access_tokens","has_more"],"additionalProperties":false}`),
			Arguments: pagingArguments,
			Fields: append([]capability.Field{{Name: "username", Description: "Account the tokens belong to"},
				{Name: "access_tokens", Description: "Tokens with every member of the metadata that is shown"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "List the access tokens", Arguments: json.RawMessage(`{"limit":20}`)}},
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
		page, err := startPaging(provider.CursorBinding(p.id, "access-tokens.list", ""), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list access tokens"
			login, err := ownLogin(ctx, c, op, allowed)
			if err != nil {
				return nil, err
			}
			var rows []object
			header, err := c.get(ctx, op, "/users/"+escape(login)+"/tokens", page.query(), &rows)
			if err != nil {
				return nil, err
			}
			result := pageResult(page, header, len(rows), "access_tokens", entryRows(p, accessTokenMembers, rows))
			result["username"] = login
			return result, nil
		}, nil
	},
}

var accessTokensGetCurrent = &tool{
	group: "access-tokens", action: "get-current", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the " + p.name + " access token this connection uses",
			Description: "Read the metadata of the token the connection sends: name, ID, scopes, creation and last use, " +
				"and its account. The token value and parts of it are never shown. Targets must name the account as " +
				"users/NAME, or be unset, and a read of the account proves it first.",
			Tags:        []string{p.id, "access-tokens", "get-current"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"access_token":` +
				schemaOf(p, currentTokenMembers) + `},"required":["access_token"],"additionalProperties":false}`),
			Fields: []capability.Field{{Name: "access_token", Description: "The token with every member of the " +
				"metadata that is shown"}},
			Examples: []capability.Example{{Description: "Read the token of the connection", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := decodeArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get current access token"
			if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var row object
			if _, err := c.get(ctx, op, "/token", nil, &row); err != nil {
				return nil, err
			}
			return map[string]any{"access_token": project(p, currentTokenMembers, row)}, nil
		}, nil
	},
}

var accessTokensDeleteCurrent = &tool{
	group: "access-tokens", action: "delete-current", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Revoke the " + p.name + " access token this connection uses",
			Description: "Delete the token the connection sends, for good; this cannot be undone. Afterwards the " +
				"connection is unusable until its credential is replaced, and every other use of that token stops. " +
				"Targets must name the account as users/NAME, or be unset, and a read of the account proves it " +
				"first. Only a connection whose tools list names it offers this tool. The change is the one request.",
			Tags:                  []string{p.id, "access-tokens", "delete-current"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
				`"required":["deleted"],"additionalProperties":false}`),
			Fields:   []capability.Field{{Name: "deleted", Description: "True once the instance revoked the token"}},
			Examples: []capability.Example{{Description: "Revoke the token of the connection", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := decodeArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		if err := allowed.ownAccountOnly(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete current access token"
			if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, "/token", nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true}, nil
		}, nil
	},
}
