package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// peopleSensitivity classifies results that name owners and roles of a BookStack instance.
const peopleSensitivity = "bookstack-people"

const (
	// maxRolePermissions bounds the role overrides one request sets and one result lists.
	maxRolePermissions = 100

	permissionsInstanceWide = "content permissions of shelves"
	guestProtected          = "the change would open content to the guest role (public access) or remove its " +
		"protection; Qatlas does not create access for anonymous visitors"
	guestOwner = "owner_id must not be a user of the guest role (public access)"
)

const contentPermissionsOutput = `{"type":"object","properties":{"owner":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"role_permissions":{"type":"array","items":{"type":"object","properties":{"role_id":{"type":"integer"},"display_name":{"type":"string"},"view":{"type":"boolean"},"create":{"type":"boolean"},"update":{"type":"boolean"},"delete":{"type":"boolean"}}}},"fallback_permissions":{"type":"object","properties":{"inheriting":{"type":"boolean"},"view":{"type":"boolean"},"create":{"type":"boolean"},"update":{"type":"boolean"},"delete":{"type":"boolean"}}},"truncated":{"type":"boolean"}},"required":["role_permissions","fallback_permissions","truncated"]}`

const (
	permissionTypeSchema  = `"type":{"type":"string","enum":["page","chapter","book","bookshelf"]},"id":{"type":"integer","minimum":1}`
	permissionValuesProps = `"view":{"type":"boolean"},"create":{"type":"boolean"},"update":{"type":"boolean"},"delete":{"type":"boolean"}`
)

const permissionsNote = "Owners and role names are personal data and untrusted provider content. On a connection " +
	"bound to books a page or chapter is proven to lie in a bound book first, a book must be a bound book, and " +
	"a shelf is only reachable on a connection without targets"

var (
	contentPermissionsRead = capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}
	contentPermissionsWrite = capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: peopleSensitivity,
	}

	contentPermissionsFields = []capability.Field{
		{Name: "owner", Description: "Owner as id, name, and slug, untrusted data"},
		{Name: "role_permissions", Description: "Role overrides, at most 100, each with role_id, display_name (untrusted data), view, create, update, delete"},
		{Name: "fallback_permissions", Description: "Permissions for every role without an override: inheriting, and view, create, update, delete when not inheriting"},
		{Name: "truncated", Description: "Whether more than 100 role overrides were cut off"},
	}

	contentPermissionsGet = capability.Descriptor{
		ID: Provider + ".contentpermissions.get", Version: 1, Title: "Get BookStack content permissions",
		Description: "Read the permission overrides of one page, chapter, book, or shelf: the owner, the permissions " +
			"of the roles that have an override, and the fallback for all other roles. Reading needs the BookStack " +
			"permission to manage content permissions. " + permissionsNote,
		Tags: []string{"administration", "permissions", "bookstack"}, Risk: contentPermissionsRead, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{` + permissionTypeSchema + `},"required":["type","id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(contentPermissionsOutput),
		Arguments: []capability.Argument{
			{Name: "type", Description: "Kind of content: page, chapter, book, or bookshelf", Required: true},
			{Name: "id", Description: "Identifier of the content", Required: true},
		},
		Fields:   contentPermissionsFields,
		Examples: []capability.Example{{Description: "Read the permissions of book 7", Arguments: json.RawMessage(`{"type":"book","id":7}`)}},
	}

	contentPermissionsUpdate = capability.Descriptor{
		ID: Provider + ".contentpermissions.update", Version: 1, Title: "Update BookStack content permissions",
		Description: "Change the owner, the role overrides, or the fallback permissions of one page, chapter, book, or shelf " +
			"for existing roles, in one request. A category that is left out stays unchanged; role_permissions replaces " +
			"all role overrides, so an empty list removes all of them; fallback_permissions needs inheriting and, when " +
			"it is false, all four values. Public access is never opened: Qatlas determines the guest role " +
			"(system role public) first and refuses, without a change, when it cannot be determined, when an entry " +
			"gives the guest role any permission it does not already have, when role_permissions drops an existing " +
			"guest entry, when the fallback changes without an explicit guest entry that denies everything, or " +
			"when owner_id is a user of the guest role. Determining the guest role needs the BookStack permission " +
			"to manage user roles. " + permissionsNote,
		Tags: []string{"administration", "permissions", "bookstack", "update"}, Risk: contentPermissionsWrite, Provider: Provider,
		RequiresToolAllowList: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + permissionTypeSchema + `,"owner_id":{"type":"integer","minimum":1},` +
			`"role_permissions":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"role_id":{"type":"integer","minimum":1},` + permissionValuesProps + `},"required":["role_id","view","create","update","delete"],"additionalProperties":false}},` +
			`"fallback_permissions":{"type":"object","properties":{"inheriting":{"type":"boolean"},` + permissionValuesProps + `},"required":["inheriting"],"additionalProperties":false}},` +
			`"required":["type","id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(contentPermissionsOutput),
		Arguments: []capability.Argument{
			{Name: "type", Description: "Kind of content: page, chapter, book, or bookshelf", Required: true},
			{Name: "id", Description: "Identifier of the content", Required: true},
			{Name: "owner_id", Description: "New owner (user identifier); must not be a user of the guest role"},
			{Name: "role_permissions", Description: "Replaces all role overrides: at most 100 entries with a unique role_id and all of view, create, update, delete; an empty list removes all overrides"},
			{Name: "fallback_permissions", Description: "inheriting, and all of view, create, update, delete when inheriting is false; applies to every role without an override"},
		},
		Fields: contentPermissionsFields,
		Examples: []capability.Example{{Description: "Give role 4 read-only access to book 7",
			Arguments: json.RawMessage(`{"type":"book","id":7,"role_permissions":[{"role_id":4,"view":true,"create":false,"update":false,"delete":false}]}`)}},
	}
)

// permissionValues are the four permissions of one entry.
type permissionValues struct {
	View   bool `json:"view"`
	Create bool `json:"create"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
}

func (v permissionValues) any() bool { return v.View || v.Create || v.Update || v.Delete }

// rolePermission is one role override in a request body.
type rolePermission struct {
	RoleID int64 `json:"role_id"`
	permissionValues
}

// fallbackPermission is the fallback in a request body; the values are only sent when not inheriting.
type fallbackPermission struct {
	Inheriting bool  `json:"inheriting"`
	View       *bool `json:"view,omitempty"`
	Create     *bool `json:"create,omitempty"`
	Update     *bool `json:"update,omitempty"`
	Delete     *bool `json:"delete,omitempty"`
}

func (f fallbackPermission) values() permissionValues {
	return permissionValues{View: isTrue(f.View), Create: isTrue(f.Create), Update: isTrue(f.Update), Delete: isTrue(f.Delete)}
}

func isTrue(b *bool) bool { return b != nil && *b }

// permissionsChange is the validated request body of an update; a nil category is left out of the body.
type permissionsChange struct {
	OwnerID         *int64              `json:"owner_id,omitempty"`
	RolePermissions *[]rolePermission   `json:"role_permissions,omitempty"`
	Fallback        *fallbackPermission `json:"fallback_permissions,omitempty"`
}

// permissionsJSON is the answer of BookStack for one item.
type permissionsJSON struct {
	Owner *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"owner"`
	RolePermissions []struct {
		RoleID int64 `json:"role_id"`
		permissionValues
		Role struct {
			DisplayName string `json:"display_name"`
		} `json:"role"`
	} `json:"role_permissions"`
	Fallback fallbackPermission `json:"fallback_permissions"`
}

// parsePermissionsChange decodes and validates the update arguments locally, before any secret.
func parsePermissionsChange(raw json.RawMessage) (permissionsChange, error) {
	var in struct {
		OwnerID         *int64 `json:"owner_id"`
		RolePermissions *[]struct {
			RoleID *int64 `json:"role_id"`
			View   *bool  `json:"view"`
			Create *bool  `json:"create"`
			Update *bool  `json:"update"`
			Delete *bool  `json:"delete"`
		} `json:"role_permissions"`
		Fallback *struct {
			Inheriting *bool `json:"inheriting"`
			View       *bool `json:"view"`
			Create     *bool `json:"create"`
			Update     *bool `json:"update"`
			Delete     *bool `json:"delete"`
		} `json:"fallback_permissions"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return permissionsChange{}, providerError("update content permissions", "the validated arguments could not be read")
	}
	var change permissionsChange
	if in.OwnerID != nil {
		if err := checkID(*in.OwnerID, "owner_id"); err != nil {
			return permissionsChange{}, err
		}
		change.OwnerID = in.OwnerID
	}
	if in.RolePermissions != nil {
		entries := in.RolePermissions
		if len(*entries) > maxRolePermissions {
			return permissionsChange{}, invalidRequest("role_permissions must have at most 100 entries")
		}
		out := make([]rolePermission, 0, len(*entries))
		seen := map[int64]bool{}
		for _, e := range *entries {
			if e.RoleID == nil {
				return permissionsChange{}, invalidRequest("every role_permissions entry needs a role_id")
			}
			if err := checkID(*e.RoleID, "role_id"); err != nil {
				return permissionsChange{}, err
			}
			if seen[*e.RoleID] {
				return permissionsChange{}, invalidRequest("role_permissions must not repeat a role_id")
			}
			seen[*e.RoleID] = true
			if e.View == nil || e.Create == nil || e.Update == nil || e.Delete == nil {
				return permissionsChange{}, invalidRequest("every role_permissions entry needs view, create, update, and delete")
			}
			out = append(out, rolePermission{RoleID: *e.RoleID,
				permissionValues: permissionValues{View: *e.View, Create: *e.Create, Update: *e.Update, Delete: *e.Delete}})
		}
		change.RolePermissions = &out
	}
	if f := in.Fallback; f != nil {
		if f.Inheriting == nil {
			return permissionsChange{}, invalidRequest("fallback_permissions needs inheriting")
		}
		fallback := fallbackPermission{Inheriting: *f.Inheriting}
		given := f.View != nil || f.Create != nil || f.Update != nil || f.Delete != nil
		switch {
		case *f.Inheriting && given:
			return permissionsChange{}, invalidRequest("fallback_permissions takes no values while inheriting is true")
		case !*f.Inheriting && (f.View == nil || f.Create == nil || f.Update == nil || f.Delete == nil):
			return permissionsChange{}, invalidRequest("fallback_permissions needs view, create, update, and delete when inheriting is false")
		}
		fallback.View, fallback.Create, fallback.Update, fallback.Delete = f.View, f.Create, f.Update, f.Delete
		change.Fallback = &fallback
	}
	if change.OwnerID == nil && change.RolePermissions == nil && change.Fallback == nil {
		return permissionsChange{}, invalidRequest("at least one of owner_id, role_permissions, and fallback_permissions is required")
	}
	return change, nil
}

// checkGuestProtection refuses a change that would open content to the guest role or remove its protection.
// It compares the request with the current state and sends nothing.
func checkGuestProtection(guest guestRole, current permissionsJSON, change permissionsChange) error {
	if change.OwnerID != nil && guest.members[*change.OwnerID] {
		return invalidRequest(guestOwner)
	}
	currentGuest, hasGuest := permissionValues{}, false
	for _, entry := range current.RolePermissions {
		if entry.RoleID == guest.id {
			currentGuest, hasGuest = entry.permissionValues, true
		}
	}
	requestGuest, requestHasGuest := permissionValues{}, false
	if change.RolePermissions != nil {
		for _, entry := range *change.RolePermissions {
			if entry.RoleID == guest.id {
				requestGuest, requestHasGuest = entry.permissionValues, true
			}
		}
		if requestHasGuest && requestGuest.any() && !(hasGuest && requestGuest == currentGuest) {
			return invalidRequest(guestProtected)
		}
		if hasGuest && !requestHasGuest {
			return invalidRequest(guestProtected)
		}
	}
	if change.Fallback != nil && !sameFallback(current.Fallback, *change.Fallback) {
		resultHasGuest, resultGuest := hasGuest, currentGuest
		if change.RolePermissions != nil {
			resultHasGuest, resultGuest = requestHasGuest, requestGuest
		}
		if !resultHasGuest || resultGuest.any() {
			return invalidRequest(guestProtected)
		}
	}
	return nil
}

func sameFallback(a, b fallbackPermission) bool {
	if a.Inheriting != b.Inheriting {
		return false
	}
	return a.Inheriting || a.values() == b.values()
}

// permissionTarget is the validated type and identifier of the content.
type permissionTarget struct {
	kind string
	id   int64
}

func (t permissionTarget) path() string {
	return "/api/content-permissions/" + t.kind + "/" + strconv.FormatInt(t.id, 10)
}

func parsePermissionTarget(raw json.RawMessage, op string) (permissionTarget, error) {
	var in struct {
		Type string `json:"type"`
		ID   int64  `json:"id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return permissionTarget{}, providerError(op, "the validated arguments could not be read")
	}
	switch in.Type {
	case "page", "chapter", "book", "bookshelf":
	default:
		return permissionTarget{}, invalidRequest("type must be page, chapter, book, or bookshelf")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return permissionTarget{}, err
	}
	return permissionTarget{kind: in.Type, id: in.ID}, nil
}

// requireLocalBinding is the part of the binding that needs no request: a book against the targets, a shelf
// only without targets.
func (t permissionTarget) requireLocalBinding(resolved *config.Resolved) error {
	switch t.kind {
	case "book":
		bound, err := boundScope(resolved)
		if err != nil {
			return err
		}
		return bound.checkBook(t.id)
	case "bookshelf":
		return requireInstanceScope(resolved, permissionsInstanceWide)
	}
	return nil
}

func invokeContentPermissionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	target, err := parsePermissionTarget(raw, "get content permissions")
	if err != nil {
		return nil, err
	}
	if err := target.requireLocalBinding(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetContentPermissions(ctx, target)
}

func invokeContentPermissionsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	target, err := parsePermissionTarget(raw, "update content permissions")
	if err != nil {
		return nil, err
	}
	change, err := parsePermissionsChange(raw)
	if err != nil {
		return nil, err
	}
	if err := target.requireLocalBinding(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateContentPermissions(ctx, target, change)
}

// bindPermissionTarget proves a chapter or page on a bound connection through one read of the item.
func (c *Client) bindPermissionTarget(ctx context.Context, t permissionTarget) error {
	switch t.kind {
	case "book":
		return c.scope.checkBook(t.id)
	case "bookshelf":
		if c.scope.bound() {
			return invalidRequest("this connection is bound to books, so it cannot use " + permissionsInstanceWide)
		}
	case "chapter":
		if c.scope.bound() {
			_, err := c.chapterBook(ctx, t.id)
			return err
		}
	case "page":
		return c.requirePageBound(ctx, strconv.FormatInt(t.id, 10))
	}
	return nil
}

func (c *Client) readContentPermissions(ctx context.Context, op string, t permissionTarget) (permissionsJSON, error) {
	var current permissionsJSON
	err := c.get(ctx, op, t.path(), nil, &current, nil, provider.ClassPermission)
	return current, err
}

// GetContentPermissions reads the permission overrides of one item within the connection's books.
func (c *Client) GetContentPermissions(ctx context.Context, t permissionTarget) (output.Object, error) {
	if err := c.bindPermissionTarget(ctx, t); err != nil {
		return output.Object{}, err
	}
	current, err := c.readContentPermissions(ctx, "get content permissions", t)
	if err != nil {
		return output.Object{}, err
	}
	return permissionsObject(current), nil
}

// UpdateContentPermissions binds the item, determines the guest role, reads the current state, refuses locally
// what would open the guest role, and then sends exactly one request, which is never repeated.
func (c *Client) UpdateContentPermissions(ctx context.Context, t permissionTarget, change permissionsChange) (output.Object, error) {
	if err := c.bindPermissionTarget(ctx, t); err != nil {
		return output.Object{}, err
	}
	guest, err := c.guestRole(ctx)
	if err != nil {
		return output.Object{}, err
	}
	current, err := c.readContentPermissions(ctx, "update content permissions", t)
	if err != nil {
		return output.Object{}, err
	}
	if err := checkGuestProtection(guest, current, change); err != nil {
		return output.Object{}, err
	}
	var updated permissionsJSON
	if err := c.mutate(ctx, "update content permissions", http.MethodPut, t.path(), change, &updated,
		argNames(contentPermissionsUpdate)); err != nil {
		return output.Object{}, err
	}
	return permissionsObject(updated), nil
}

func permissionsObject(p permissionsJSON) output.Object {
	fields := map[string]any{}
	if p.Owner != nil {
		fields["owner"] = map[string]any{"id": p.Owner.ID, "name": clip(p.Owner.Name, maxResultString), "slug": clip(p.Owner.Slug, maxResultString)}
	}
	roles := make([]map[string]any, 0, len(p.RolePermissions))
	truncated := false
	for _, entry := range p.RolePermissions {
		if len(roles) >= maxRolePermissions {
			truncated = true
			break
		}
		roles = append(roles, map[string]any{"role_id": entry.RoleID, "display_name": clip(entry.Role.DisplayName, maxResultString),
			"view": entry.View, "create": entry.Create, "update": entry.Update, "delete": entry.Delete})
	}
	fallback := map[string]any{"inheriting": p.Fallback.Inheriting}
	if !p.Fallback.Inheriting {
		values := p.Fallback.values()
		fallback["view"], fallback["create"], fallback["update"], fallback["delete"] = values.View, values.Create, values.Update, values.Delete
	}
	fields["role_permissions"] = roles
	fields["fallback_permissions"] = fallback
	fields["truncated"] = truncated
	return mapObject(contentPermissionsGet, fields)
}
