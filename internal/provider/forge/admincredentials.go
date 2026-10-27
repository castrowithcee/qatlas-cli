package forge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The credential administration tools delete a public SSH key of another account and, on Forgejo 16 and later,
// list and delete the access tokens of another account. Creating a key or a token is not offered, and no token
// value or part of it is ever shown. Like the other instance administration they reach past any target, so only
// a connection without targets offers them.

// adminCredentialsSensitivity is its own class: these tools reach the credentials of other people.
const adminCredentialsSensitivity = "-admin-credentials"

const adminCredentialsNote = "Only a connection without targets offers this tool."

// sinceAdminTokens is the oldest version with the access token routes of the administrator.
var sinceAdminTokens = map[string]version{forgejo.id: forgejoActions16}

var adminUserKeysDelete = &tool{
	group: "admin-user-keys", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a public SSH key of a " + p.name + " user",
			Description: "Delete one public SSH key of another account as an administrator, by user name and key ID, for " +
				"good; whatever uses the key loses access. Adding a key is not offered. Idempotent; the request is sent " +
				"once and never repeated after an unclear outcome. " + adminCredentialsNote + " Offered only by a " +
				"connection whose tools list names it, in no profile",
			Tags:                  []string{p.id, "admin", "keys", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+adminCredentialsSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"username":`+issueSchemaLogin+`,"id":`+accessIDSchema, "username", "id"),
			OutputSchema:          adminUserOutput(`,"id":{"type":"integer"},"deleted":{"type":"boolean"}`, "id", "deleted"),
			Arguments: []capability.Argument{adminUserArgument, {Name: "id", Required: true,
				Description: "Numeric ID of the key, as user-keys.list reports it as id for that account"}},
			Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
				{Name: "id", Description: "ID of the key"},
				{Name: "deleted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Delete a key",
				Arguments: json.RawMessage(`{"username":"octo","id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
			ID       int64  `json:"id"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		if err := accessID(a.ID); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete instance user key", http.MethodDelete,
				"/admin/users/"+escape(a.Username)+"/keys/"+itoa64(a.ID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "id": a.ID, "deleted": true}, nil
		}, nil
	},
}

var adminAccessTokensList = &tool{
	group: "admin-access-tokens", action: "list", since: sinceAdminTokens,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the access tokens of a " + p.name + " user",
			Description: "List one batch of the metadata of the access tokens of one account as an administrator: name, " +
				"ID, scopes, and limits. The token values and parts of them are never shown. Forgejo 16 and later. " +
				adminCredentialsNote,
			Tags:        []string{p.id, "admin", "access-tokens", "list"},
			Risk:        readRisk(p.id + adminCredentialsSensitivity),
			InputSchema: inputSchema(`"username":`+issueSchemaLogin+`,`+pagingKeys, "username"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"username":{"type":"string"},` +
				`"access_tokens":{"type":"array","items":` + schemaOf(p, accessTokenMembers) + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["username","access_tokens","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{adminUserArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "username", Description: "Account the tokens belong to"},
				{Name: "access_tokens", Description: "Tokens with every member of the metadata that is shown"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "List the tokens of an account",
				Arguments: json.RawMessage(`{"username":"octo","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
			Limit    int    `json:"limit"`
			Cursor   string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		path := "/admin/users/" + escape(a.Username) + "/tokens"
		page, err := startPaging(provider.CursorBinding(p.id, "admin-access-tokens.list", "", path), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list instance user access tokens", path, page.query(), &rows)
			if err != nil {
				return nil, err
			}
			result := pageResult(page, header, len(rows), "access_tokens", entryRows(p, accessTokenMembers, rows))
			result["username"] = a.Username
			return result, nil
		}, nil
	},
}

var adminAccessTokensDelete = &tool{
	group: "admin-access-tokens", action: "delete", since: sinceAdminTokens,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete an access token of a " + p.name + " user",
			Description: "Delete one access token of another account as an administrator, for good; whatever uses the " +
				"token stops working. The token is named by its numeric ID only, never by name, so a name cannot be " +
				"read as an ID. Creating a token is not offered. Forgejo 16 and later. Idempotent; the request is sent " +
				"once and never repeated after an unclear outcome. " + adminCredentialsNote + " Offered only by a " +
				"connection whose tools list names it, in no profile",
			Tags:                  []string{p.id, "admin", "access-tokens", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+adminCredentialsSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"username":`+issueSchemaLogin+`,"token_id":`+accessIDSchema, "username", "token_id"),
			OutputSchema:          adminUserOutput(`,"token_id":{"type":"integer"},"deleted":{"type":"boolean"}`, "token_id", "deleted"),
			Arguments: []capability.Argument{adminUserArgument, {Name: "token_id", Required: true,
				Description: "Numeric ID of the token, as admin-access-tokens.list reports it as id; a name is not accepted"}},
			Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
				{Name: "token_id", Description: "ID of the token"},
				{Name: "deleted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Delete a token",
				Arguments: json.RawMessage(`{"username":"octo","token_id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
			TokenID  int64  `json:"token_id"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		if a.TokenID < 1 {
			return nil, invalidArgument("token_id must be the numeric ID of an existing token")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete instance user access token", http.MethodDelete,
				"/admin/users/"+escape(a.Username)+"/tokens/"+itoa64(a.TokenID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"username": a.Username, "token_id": a.TokenID, "deleted": true}, nil
		}, nil
	},
}
