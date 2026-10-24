package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The key tools read and delete existing SSH keys: the deploy keys of one repository and the keys of the
// account behind the token, and read the public SSH and GPG keys of the account and of other people. No key is
// added or verified. Every projection is an allowlist: the public key text (key, public_key, subkey texts) and
// the deploy token of a newer Gitea never appear; the fingerprint and key ID identify a key. A key is addressed
// inside the path of its owner, so the owner binding is the proof; only the account level reads the account
// first. Another person's public keys reach past any target and are offered only without targets.

const accessIDSchema = hookSchemaID

const fingerprintSchema = `{"type":"string","minLength":1,"maxLength":200}`

var repositoryReferenceMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "name", kind: kindText, note: "Repository name"},
	{name: "full_name", kind: kindText, note: "Canonical OWNER/REPO"},
}

var keyOwnerMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the user"},
	{name: "login", kind: kindText, note: "User name"},
}

var deployKeyMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the deploy key, used by get and delete"},
	{name: "key_id", kind: kindInteger, note: "Numeric ID of the underlying public key"},
	{name: "title", kind: kindText, note: "Title of the key, untrusted data"},
	{name: "fingerprint", kind: kindText, note: "Fingerprint of the key"},
	{name: "read_only", kind: kindFlag, note: "True when the key may only read"},
	{name: "key_type", kind: kindText, only: gitea, note: "Kind of the deploy key, reported by Gitea 28 and later"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "url", kind: kindText, address: true, note: "API address of the key, untrusted data, never fetched"},
	{name: "repository", kind: kindObject, sub: repositoryReferenceMembers, note: "Repository of the key"},
}

var publicKeyMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the key, used by get and delete"},
	{name: "title", kind: kindText, note: "Title of the key, untrusted data"},
	{name: "key_type", kind: kindText, note: "Kind of the key"},
	{name: "fingerprint", kind: kindText, note: "Fingerprint of the key"},
	{name: "read_only", kind: kindFlag, note: "True when the key may only read"},
	{name: "verified", kind: kindFlag, only: forgejo, note: "True when the key is verified, Forgejo 17 and later"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, only: forgejo, note: "Time of the last change, Forgejo 17 and later"},
	{name: "last_used_at", kind: kindText, only: gitea, note: "Time of the last use"},
	{name: "url", kind: kindText, address: true, note: "API address of the key, untrusted data, never fetched"},
	{name: "user", kind: kindObject, sub: keyOwnerMembers, note: "Owner of the key"},
}

var gpgSubkeyMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the subkey"},
	{name: "key_id", kind: kindText, note: "GPG key ID"},
	{name: "primary_key_id", kind: kindText, note: "GPG key ID of the primary key"},
	{name: "can_sign", kind: kindFlag, note: "True when the key can sign"},
	{name: "can_encrypt_comms", kind: kindFlag, note: "True when the key can encrypt communication"},
	{name: "can_encrypt_storage", kind: kindFlag, note: "True when the key can encrypt storage"},
	{name: "can_certify", kind: kindFlag, note: "True when the key can certify"},
	{name: "verified", kind: kindFlag, note: "True when the key is verified"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "expires_at", kind: kindText, note: "Expiry time"},
}

var gpgKeyMembers = append(append([]member(nil), gpgSubkeyMembers...),
	member{name: "emails", kind: kindObjects, note: "Email addresses of the key, personal data", sub: []member{
		{name: "email", kind: kindText, note: "Email address"},
		{name: "verified", kind: kindFlag, note: "True when the address is verified"},
	}},
	member{name: "subkeys", kind: kindObjects, sub: gpgSubkeyMembers, note: "Subkeys without their key text"},
)

// ownResource is a group of tools on one kind of entry of the account behind the token. Every entry is addressed
// by ID inside the account's own path.
type ownResource struct {
	group       string // tool group such as user-keys
	noun        string // singular for texts such as SSH key
	path        string // path of the account's own list
	other       string // path below /users/NAME of another person's list, empty when the API has none
	single      string // result key of one entry
	plural      string // result key of a list
	members     []member
	fingerprint bool   // list takes the fingerprint filter
	hidden      string // what is never shown, for the descriptions
}

func (r ownResource) idArgument() capability.Argument {
	return capability.Argument{Name: "id", Required: true,
		Description: "ID of the " + r.noun + ", as " + r.group + ".list reports it as id"}
}

func entryRows(p *product, members []member, rows []object) []map[string]any {
	list := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			list = append(list, project(p, members, row))
		}
	}
	return list
}

// requireEntryAnswer proves that an answer is the entry that was asked for.
func requireEntryAnswer(p *product, op string, id int64, row object) error {
	if got, ok := row.integer("id"); !ok || got != id {
		return provider.InvalidResponse(op, p.name+" returned another entry than the one asked for")
	}
	return nil
}

func accessID(id int64) error {
	if id < 1 {
		return invalidArgument("id must be the ID of an existing entry")
	}
	return nil
}

func fingerprintOf(value string) error {
	if value != "" {
		return checkOneLine("fingerprint", value, 200)
	}
	return nil
}

func (r ownResource) list() *tool {
	return &tool{
		group: r.group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties := pagingKeys
			arguments := []capability.Argument{}
			fields := []capability.Field{{Name: r.plural, Description: "Entries with every member of the " + r.noun +
				" object that is shown"}}
			text := "List one batch of the " + r.noun + "s of the account behind the token. " + r.hidden
			if r.other != "" {
				properties = `"username":` + issueSchemaLogin + `,` + properties
				arguments = append(arguments, capability.Argument{Name: "username", Description: "User whose " +
					r.noun + "s to list; the account behind the token when omitted"})
				fields = append(fields, capability.Field{Name: "username", Description: "User the tool listed, " +
					"left out for the account behind the token"})
				text += " With username it reaches past any target, so only a connection without targets offers it; " +
					"without username, targets must name the account as users/NAME."
			} else {
				text += " Targets must name the account as users/NAME, or be unset."
			}
			if r.fingerprint {
				properties = `"fingerprint":` + fingerprintSchema + `,` + properties
				arguments = append(arguments, capability.Argument{Name: "fingerprint",
					Description: "Only the " + r.noun + " with this fingerprint"})
			}
			output := `"` + r.plural + `":{"type":"array","items":` + schemaOf(p, r.members) + `},`
			if r.other != "" {
				output += `"username":{"type":"string"},`
			}
			return capability.Descriptor{
				Title:       "List the " + r.noun + "s of a " + p.name + " account",
				Description: text + " No " + r.noun + " is added.",
				Tags:        []string{p.id, r.group, "list"},
				Risk:        readRisk(p.id + accessSensitivity),
				InputSchema: inputSchema(properties),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{` + output + `"next_cursor":` +
					`{"type":"string"},"has_more":{"type":"boolean"}},"required":["` + r.plural + `","has_more"],` +
					`"additionalProperties":false}`),
				Arguments: append(arguments, pagingArguments...),
				Fields:    append(fields, pagingFields...),
				Examples: []capability.Example{{Description: "List the " + r.noun + "s",
					Arguments: json.RawMessage(`{"limit":20}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Username    string `json:"username"`
				Fingerprint string `json:"fingerprint"`
				Limit       int    `json:"limit"`
				Cursor      string `json:"cursor"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			base := r.path
			if a.Username != "" {
				if r.other == "" {
					return nil, invalidArgument("the arguments could not be read")
				}
				if err := allowed.otherPeopleOnly(); err != nil {
					return nil, err
				}
				if err := userNameOf("username", a.Username); err != nil {
					return nil, err
				}
				base = "/users/" + escape(a.Username) + r.other
			} else if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			if err := fingerprintOf(a.Fingerprint); err != nil {
				return nil, err
			}
			page, err := startPaging(provider.CursorBinding(p.id, r.group+".list",
				strings.ToLower(a.Username)+"|"+a.Fingerprint), a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "list " + r.group
				if a.Username == "" {
					if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
						return nil, err
					}
				}
				query := page.query()
				if a.Fingerprint != "" {
					query.Set("fingerprint", a.Fingerprint)
				}
				var rows []object
				header, err := c.get(ctx, op, base, query, &rows)
				if err != nil {
					return nil, err
				}
				result := pageResult(page, header, len(rows), r.plural, entryRows(p, r.members, rows))
				if a.Username != "" {
					result["username"] = a.Username
				}
				return result, nil
			}, nil
		},
	}
}

func (r ownResource) get() *tool {
	return &tool{
		group: r.group, action: "get", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Get a " + r.noun + " of a " + p.name + " account",
				Description: "Read one " + r.noun + " of the account behind the token by its ID. " + r.hidden +
					" Targets must name the account as users/NAME, or be unset.",
				Tags:        []string{p.id, r.group, "get"},
				Risk:        readRisk(p.id + accessSensitivity),
				InputSchema: inputSchema(`"id":`+accessIDSchema, "id"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + r.single + `":` +
					schemaOf(p, r.members) + `},"required":["` + r.single + `"],"additionalProperties":false}`),
				Arguments: []capability.Argument{r.idArgument()},
				Fields: []capability.Field{{Name: r.single, Description: "The " + r.noun +
					" with every member of the object that is shown"}},
				Examples: []capability.Example{{Description: "Read a " + r.noun, Arguments: json.RawMessage(`{"id":12}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				ID int64 `json:"id"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			if err := accessID(a.ID); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "get " + r.group
				if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
					return nil, err
				}
				var row object
				if _, err := c.get(ctx, op, r.path+"/"+itoa64(a.ID), nil, &row); err != nil {
					return nil, err
				}
				if err := requireEntryAnswer(p, op, a.ID, row); err != nil {
					return nil, err
				}
				return map[string]any{r.single: project(p, r.members, row)}, nil
			}, nil
		},
	}
}

func (r ownResource) delete() *tool {
	return &tool{
		group: r.group, action: "delete", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Delete a " + r.noun + " of a " + p.name + " account",
				Description: "Delete one existing " + r.noun + " of the account behind the token by its ID for good; " +
					"this cannot be undone and whatever uses it loses access. Targets must name the account as " +
					"users/NAME, or be unset. Only a connection whose tools list names it offers this tool. The " +
					"change is the one request.",
				Tags:                  []string{p.id, r.group, "delete"},
				Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
				RequiresToolAllowList: true,
				InputSchema:           inputSchema(`"id":`+accessIDSchema, "id"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` +
					`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
				Arguments: []capability.Argument{r.idArgument()},
				Fields: []capability.Field{{Name: "id", Description: "ID of the " + r.noun},
					{Name: "deleted", Description: "True once the instance deleted the " + r.noun}},
				Examples: []capability.Example{{Description: "Delete a " + r.noun, Arguments: json.RawMessage(`{"id":12}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				ID int64 `json:"id"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := allowed.ownAccountOnly(); err != nil {
				return nil, err
			}
			if err := accessID(a.ID); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "delete " + r.group
				if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
					return nil, err
				}
				if err := c.change(ctx, op, http.MethodDelete, r.path+"/"+itoa64(a.ID), nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"id": a.ID, "deleted": true}, nil
			}, nil
		},
	}
}

const keyText = "The key text is never shown, only the fingerprint and metadata."

var userKeys = ownResource{group: "user-keys", noun: "SSH key", path: "/user/keys", other: "/keys",
	single: "key", plural: "keys", members: publicKeyMembers, fingerprint: true, hidden: keyText}

var userGPGKeys = ownResource{group: "user-gpg-keys", noun: "GPG key", path: "/user/gpg_keys", other: "/gpg_keys",
	single: "gpg_key", plural: "gpg_keys", members: gpgKeyMembers,
	hidden: "The public key text of the key and of its subkeys is never shown."}

var userKeysList, userKeysGet, userKeysDelete = userKeys.list(), userKeys.get(), userKeys.delete()
var userGPGKeysList, userGPGKeysGet, userGPGKeysDelete = userGPGKeys.list(), userGPGKeys.get(), userGPGKeys.delete()

// ---- deploy keys --------------------------------------------------------------------------------------------

var deployKeyIDArgument = capability.Argument{Name: "id", Required: true,
	Description: "ID of the deploy key, as deploy-keys.list reports it as id"}

var deployKeysList = &tool{
	group: "deploy-keys", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the deploy keys of a " + p.name + " repository",
			Description: "List one batch of the existing deploy keys of a repository inside the connection's targets. " +
				keyText + " No deploy key is added. The instance serves this only to a repository administrator.",
			Tags:        []string{p.id, "deploy-keys", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema + `,"fingerprint":` + fingerprintSchema + `,` + pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"deploy_keys":{"type":"array","items":` + schemaOf(p, deployKeyMembers) + `},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["repository","deploy_keys","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{repositoryArgument, {Name: "fingerprint",
				Description: "Only the deploy key with this fingerprint"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "deploy_keys", Description: "Deploy keys with every member of the object that is shown"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "List the deploy keys",
				Arguments: json.RawMessage(`{"repository":"example/project","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository  string `json:"repository"`
			Fingerprint string `json:"fingerprint"`
			Limit       int    `json:"limit"`
			Cursor      string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := fingerprintOf(a.Fingerprint); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "deploy-keys.list",
			strings.ToLower(fullName(repo))+"|"+a.Fingerprint), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			if a.Fingerprint != "" {
				query.Set("fingerprint", a.Fingerprint)
			}
			var rows []object
			header, err := c.get(ctx, "list deploy keys", repositoryPath(repo)+"/keys", query, &rows)
			if err != nil {
				return nil, err
			}
			result := pageResult(page, header, len(rows), "deploy_keys", entryRows(p, deployKeyMembers, rows))
			result["repository"] = fullName(repo)
			return result, nil
		}, nil
	},
}

// deployKeyRequest is the one repository and key a get or delete acts on.
func deployKeyRequest(allowed allowlist, raw json.RawMessage) (target, int64, error) {
	var a struct {
		Repository string `json:"repository"`
		ID         int64  `json:"id"`
	}
	if err := decodeArguments(raw, &a); err != nil {
		return target{}, 0, err
	}
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return target{}, 0, err
	}
	return repo, a.ID, accessID(a.ID)
}

var deployKeysGet = &tool{
	group: "deploy-keys", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a deploy key of a " + p.name + " repository",
			Description: "Read one existing deploy key of a repository inside the connection's targets by its ID. " +
				keyText + " The instance serves this only to a repository administrator.",
			Tags:        []string{p.id, "deploy-keys", "get"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"id":`+accessIDSchema, "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"deploy_key":` + schemaOf(p, deployKeyMembers) + `},"required":["repository","deploy_key"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, deployKeyIDArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "deploy_key", Description: "The deploy key with every member of the object that is shown"}},
			Examples: []capability.Example{{Description: "Read a deploy key",
				Arguments: json.RawMessage(`{"repository":"example/project","id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		repo, id, err := deployKeyRequest(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get deploy key"
			var row object
			if _, err := c.get(ctx, op, repositoryPath(repo)+"/keys/"+itoa64(id), nil, &row); err != nil {
				return nil, err
			}
			if err := requireEntryAnswer(p, op, id, row); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "deploy_key": project(p, deployKeyMembers, row)}, nil
		}, nil
	},
}

var deployKeysDelete = &tool{
	group: "deploy-keys", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a deploy key of a " + p.name + " repository",
			Description: "Delete one existing deploy key of a repository inside the connection's targets by its ID for " +
				"good; this cannot be undone and whatever uses the key loses access. Only a connection whose tools " +
				"list names it offers this tool. The change is the one request.",
			Tags:                  []string{p.id, "deploy-keys", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":`+repoSchema+`,"id":`+accessIDSchema, "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"id":{"type":"integer"},"deleted":{"type":"boolean"}},"required":["repository","id","deleted"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, deployKeyIDArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "id", Description: "ID of the deploy key"},
				{Name: "deleted", Description: "True once the instance deleted the deploy key"}},
			Examples: []capability.Example{{Description: "Delete a deploy key",
				Arguments: json.RawMessage(`{"repository":"example/project","id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		repo, id, err := deployKeyRequest(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete deploy key", http.MethodDelete,
				repositoryPath(repo)+"/keys/"+itoa64(id), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "id": id, "deleted": true}, nil
		}, nil
	},
}
