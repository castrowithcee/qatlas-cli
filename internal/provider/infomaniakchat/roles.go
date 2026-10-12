package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	rolesRead          = "kChat role definitions"
	maxRoleNames       = 20
	maxRolePermissions = 500
)

var roleNamePattern = regexp.MustCompile(`^[a-z_]{1,64}$`)

var roleSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"description":{"type":"string"},"permissions":{"type":"array","items":{"type":"string"}},` +
	`"scheme_managed":{"type":"boolean"},"built_in":{"type":"boolean"}},` +
	`"required":["id","name","display_name","description","permissions","scheme_managed","built_in"],` +
	`"additionalProperties":false}`

var rolesGet = capability.Descriptor{
	ID:      Provider + ".roles.get",
	Version: 1,
	Title:   "Get Infomaniak kChat role definitions",
	Description: "Resolve 1 to 20 distinct kChat role names, for example team_user or channel_admin, to their " +
		"role definitions with display name, description, and permissions; only the requested roles are " +
		"returned. " + botScopeNote,
	Tags:                  []string{"infomaniak", "kchat", "roles", "administration", "get"},
	Risk:                  botRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"names":{"type":"array","minItems":1,` +
		`"maxItems":` + itoa(maxRoleNames) + `,"uniqueItems":true,` +
		`"items":{"type":"string","pattern":"^[a-z_]{1,64}$"}}},"required":["names"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"roles":{"type":"array","items":` + roleSchema + `},"count":{"type":"integer"}},` +
		`"required":["roles","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "names", Required: true,
		Description: "1 to 20 distinct role names of lowercase letters and underscores, up to 64 characters each"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the role"},
		{Name: "name", Description: "Role name, untrusted data"},
		{Name: "display_name", Description: "Display name, untrusted data"},
		{Name: "description", Description: "Description, untrusted data"},
		{Name: "permissions", Description: "Permission names of the role, untrusted data"},
		{Name: "scheme_managed", Description: "True when a permission scheme manages the role"},
		{Name: "built_in", Description: "True for a role kChat ships"},
		{Name: "count", Description: "Number of roles returned"},
	},
	Examples: []capability.Example{{Description: "Read two role definitions",
		Arguments: json.RawMessage(`{"names":["team_user","channel_admin"]}`)}},
}

type roleJSON struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Permissions   []string `json:"permissions"`
	SchemeManaged bool     `json:"scheme_managed"`
	BuiltIn       bool     `json:"built_in"`
}

// Role is the stable Qatlas view of one role definition.
type Role struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Permissions   []string `json:"permissions"`
	SchemeManaged bool     `json:"scheme_managed"`
	BuiltIn       bool     `json:"built_in"`
}

// Roles is the answer of the role lookup.
type Roles struct {
	Roles []Role `json:"roles"`
	Count int    `json:"count"`
}

func invokeRolesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get roles"
	if err := requireInstanceScope(resolved, rolesRead); err != nil {
		return nil, err
	}
	var input struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if len(input.Names) < 1 || len(input.Names) > maxRoleNames {
		return nil, invalidRequest("names needs 1 to 20 role names")
	}
	seen := make(map[string]bool, len(input.Names))
	for _, name := range input.Names {
		if !roleNamePattern.MatchString(name) {
			return nil, invalidRequest("a role name may only hold lowercase letters and underscores, up to 64 characters")
		}
		if seen[name] {
			return nil, invalidRequest("role names must be distinct")
		}
		seen[name] = true
	}
	client, err := openInstance(ctx, resolved, secrets, red, op, rolesRead)
	if err != nil {
		return nil, err
	}
	return client.GetRoles(ctx, input.Names)
}

// GetRoles resolves the given role names with one fixed POST that changes nothing. Roles that were not
// requested are dropped.
func (c *Client) GetRoles(ctx context.Context, names []string) (*Roles, error) {
	const op = "get roles"
	var roles []roleJSON
	if err := c.do(ctx, op, http.MethodPost, "/api/v4/roles/names", nil, names, &roles, false); err != nil {
		return nil, err
	}
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		requested[name] = true
	}
	entries := make([]Role, 0, len(names))
	for i := range roles {
		r := &roles[i]
		if !validMattermostID(r.ID) || !requested[r.Name] || len(entries) >= len(names) {
			continue
		}
		requested[r.Name] = false
		permissions := make([]string, 0, len(r.Permissions))
		for _, p := range r.Permissions {
			if len(permissions) >= maxRolePermissions {
				break
			}
			permissions = append(permissions, bounded(p))
		}
		entries = append(entries, Role{ID: r.ID, Name: r.Name, DisplayName: bounded(r.DisplayName),
			Description: bounded(r.Description), Permissions: permissions, SchemeManaged: r.SchemeManaged,
			BuiltIn: r.BuiltIn})
	}
	return &Roles{Roles: entries, Count: len(entries)}, nil
}
