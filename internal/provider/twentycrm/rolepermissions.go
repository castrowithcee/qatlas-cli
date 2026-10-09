package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The fixed documents of the role permission tools, sent to the metadata endpoint. Only checked values are
// variables; the selections carry identifiers only.
var (
	roleObjectPermissionsDocument = graphqlDocument{path: metadataPath, text: `mutation UpsertObjectPermissions(` +
		`$input: UpsertObjectPermissionsInput!) { upsertObjectPermissions(upsertObjectPermissionsInput: $input) ` +
		`{ objectMetadataId } }`}
	roleFieldPermissionsDocument = graphqlDocument{path: metadataPath, text: `mutation UpsertFieldPermissions(` +
		`$input: UpsertFieldPermissionsInput!) { upsertFieldPermissions(upsertFieldPermissionsInput: $input) ` +
		`{ roleId objectMetadataId fieldMetadataId } }`}
	rolePermissionFlagsDocument = graphqlDocument{path: metadataPath, text: `mutation UpsertPermissionFlags(` +
		`$input: UpsertPermissionFlagsInput!) { upsertPermissionFlags(upsertPermissionFlagsInput: $input) ` +
		`{ roleId flag } }`}
)

// permissionFlagTypes are the values of PermissionFlagType (twenty-shared, twentyhq/twenty commit
// 9070979232c4f37165108a2eb5a4042e0cfdc3d6). Any other value is refused before a request is sent.
var permissionFlagTypes = []string{"API_KEYS_AND_WEBHOOKS", "WORKSPACE", "WORKSPACE_MEMBERS", "ROLES", "DATA_MODEL",
	"SECURITY", "WORKFLOWS", "IMPERSONATE", "SSO_BYPASS", "APPLICATIONS", "MARKETPLACE_APPS", "LAYOUTS", "BILLING",
	"AI_SETTINGS", "AI", "VIEWS", "UPLOAD_FILE", "DOWNLOAD_FILE", "SEND_EMAIL_TOOL", "CREATE_CALENDAR_EVENT_TOOL",
	"HTTP_REQUEST_TOOL", "CODE_INTERPRETER_TOOL", "IMPORT_CSV", "EXPORT_CSV", "CONNECTED_ACCOUNTS",
	"PROFILE_INFORMATION"}

const (
	// rolePermFieldsMax caps the fields of one call; rolePermAnswerMax caps the entries Twenty may answer with.
	rolePermFieldsMax = 100
	rolePermAnswerMax = 1000

	errPermArgs   = "the arguments are not a valid role permission request"
	errPermNone   = "at least one right must be given"
	errPermFields = "fields must hold 1 to 100 entries with distinct field_id values"
	errPermFlags  = "flags must be a list of distinct known permission flags; an empty list withdraws every flag"
	errPermTarget = "the object or a field is unknown, a field belongs to another object, or the object or a " +
		"field is a system one; read the object with twentycrm.metaobjects.get"

	rolePermNote = "The change applies to every member of the role at once. Needs the Twenty permissions " +
		"\"Roles\" and \"Data model\" (the object is read first) and a connection without object targets, and is " +
		"offered only by a connection whose tools list names it. A role that is assigned to an API key or cannot " +
		"be edited, and system objects and fields, are refused without a change. Sent once; after an unclear " +
		"result read the roles before repeating"
)

func rolePermFlagSchema() string {
	quoted := make([]string, len(permissionFlagTypes))
	for i, flag := range permissionFlagTypes {
		quoted[i] = `"` + flag + `"`
	}
	return `{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}`
}

var rolePermRoleArgument = capability.Argument{Name: "role_id", Required: true,
	Description: "UUID of the role, as returned by twentycrm.roles.list"}
var rolePermObjectArgument = capability.Argument{Name: "object_id", Required: true,
	Description: "UUID of the object, as returned by twentycrm.metaobjects.list"}

var rolesSetObjectPermissions = capability.Descriptor{
	ID: Provider + ".roles.setobjectpermissions", Version: 1, Title: "Set the object rights of a Twenty CRM role",
	Description: "Set the rights of one role on the records of one object of the Twenty workspace of a connection: " +
		"read, update, soft delete, and destroy; only the given rights change and at least one is required. " +
		rolePermNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "objects", "update"},
	Risk:     roleWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":` + uuidSchema + `,"object_id":` +
		uuidSchema + `,"can_read":` + roleBoolSchema + `,"can_update":` + roleBoolSchema +
		`,"can_soft_delete":` + roleBoolSchema + `,"can_destroy":` + roleBoolSchema +
		`},"required":["role_id","object_id"],"minProperties":3,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":{"type":"string"},` +
		`"object_id":{"type":"string"},"rights_set":{"type":"integer"}},` +
		`"required":["role_id","object_id","rights_set"],"additionalProperties":false}`),
	Arguments: []capability.Argument{rolePermRoleArgument, rolePermObjectArgument,
		{Name: "can_read", Description: "Role may read the records of the object"},
		{Name: "can_update", Description: "Role may update the records of the object"},
		{Name: "can_soft_delete", Description: "Role may delete the records of the object"},
		{Name: "can_destroy", Description: "Role may destroy the records of the object"}},
	Fields: []capability.Field{{Name: "role_id", Description: "The changed role"},
		{Name: "object_id", Description: "The object"}, {Name: "rights_set", Description: "Number of rights sent"}},
	Examples: []capability.Example{{Description: "Make an object read-only for a role",
		Arguments: json.RawMessage(`{"role_id":"123e4567-e89b-42d3-a456-426614174000",` +
			`"object_id":"123e4567-e89b-42d3-a456-426614174001","can_read":true,"can_update":false,` +
			`"can_soft_delete":false,"can_destroy":false}`)}},
}

var rolesSetFieldPermissions = capability.Descriptor{
	ID: Provider + ".roles.setfieldpermissions", Version: 1, Title: "Set the field rights of a Twenty CRM role",
	Description: "Set the rights of one role on the values of 1 to 100 fields of one object of the Twenty workspace " +
		"of a connection: read and update per field; only the given rights change. " + rolePermNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "fields", "update"},
	Risk:     roleWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":` + uuidSchema + `,"object_id":` +
		uuidSchema + `,"fields":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,"items":` +
		`{"type":"object","properties":{"field_id":` + uuidSchema + `,"can_read":` + roleBoolSchema +
		`,"can_update":` + roleBoolSchema + `},"required":["field_id"],"minProperties":2,"additionalProperties":false}}},` +
		`"required":["role_id","object_id","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":{"type":"string"},` +
		`"object_id":{"type":"string"},"fields_set":{"type":"integer"}},` +
		`"required":["role_id","object_id","fields_set"],"additionalProperties":false}`),
	Arguments: []capability.Argument{rolePermRoleArgument, rolePermObjectArgument,
		{Name: "fields", Required: true, Description: "1 to 100 entries {field_id, can_read, can_update} of fields " +
			"of this object, as returned by twentycrm.metaobjects.get; field_id values must differ"}},
	Fields: []capability.Field{{Name: "role_id", Description: "The changed role"},
		{Name: "object_id", Description: "The object"}, {Name: "fields_set", Description: "Number of fields sent"}},
	Examples: []capability.Example{{Description: "Hide a field from a role",
		Arguments: json.RawMessage(`{"role_id":"123e4567-e89b-42d3-a456-426614174000",` +
			`"object_id":"123e4567-e89b-42d3-a456-426614174001","fields":[{"field_id":` +
			`"123e4567-e89b-42d3-a456-426614174002","can_read":false,"can_update":false}]}`)}},
}

var rolesSetPermissionFlags = capability.Descriptor{
	ID: Provider + ".roles.setpermissionflags", Version: 1, Title: "Set the settings rights of a Twenty CRM role",
	Description: "Replace the settings permission flags of one role of the Twenty workspace of a connection with " +
		"exactly the given list: a flag that is not named is withdrawn, an empty list withdraws all. " +
		"Flags such as ROLES, API_KEYS_AND_WEBHOOKS, SECURITY, or DATA_MODEL are ordinary values of the list. " +
		rolePermNote,
	Tags:     []string{"twentycrm", "roles", "permissions", "settings", "update"},
	Risk:     roleWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":` + uuidSchema + `,"flags":{"type":"array",` +
		`"maxItems":` + strconv.Itoa(len(permissionFlagTypes)) + `,"uniqueItems":true,"items":` + rolePermFlagSchema() + `}},` +
		`"required":["role_id","flags"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"role_id":{"type":"string"},` +
		`"flags_set":{"type":"integer"}},"required":["role_id","flags_set"],"additionalProperties":false}`),
	Arguments: []capability.Argument{rolePermRoleArgument,
		{Name: "flags", Required: true, Description: "The complete list of settings flags the role keeps: " +
			strings.Join(permissionFlagTypes, ", ")}},
	Fields: []capability.Field{{Name: "role_id", Description: "The changed role"},
		{Name: "flags_set", Description: "Number of flags the role now holds"}},
	Examples: []capability.Example{{Description: "Allow a role to change the data model only",
		Arguments: json.RawMessage(`{"role_id":"123e4567-e89b-42d3-a456-426614174000","flags":["DATA_MODEL"]}`)}},
}

type roleRightArgs struct {
	CanRead       *bool `json:"can_read"`
	CanUpdate     *bool `json:"can_update"`
	CanSoftDelete *bool `json:"can_soft_delete"`
	CanDestroy    *bool `json:"can_destroy"`
}

type roleObjectPermArgs struct {
	RoleID   string `json:"role_id"`
	ObjectID string `json:"object_id"`
	roleRightArgs
}

type roleFieldPermArgs struct {
	RoleID   string `json:"role_id"`
	ObjectID string `json:"object_id"`
	Fields   []struct {
		FieldID   string `json:"field_id"`
		CanRead   *bool  `json:"can_read"`
		CanUpdate *bool  `json:"can_update"`
	} `json:"fields"`
}

type roleFlagArgs struct {
	RoleID string    `json:"role_id"`
	Flags  *[]string `json:"flags"`
}

func decodePermArgs(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return invalidRequest(errPermArgs)
	}
	return nil
}

// RoleObjectPermissions is the checked content of setobjectpermissions. A nil right is not sent.
type RoleObjectPermissions struct {
	RoleID, ObjectID                              string
	CanRead, CanUpdate, CanSoftDelete, CanDestroy *bool
}

// RoleFieldPermission is one checked field entry. A nil right is not sent.
type RoleFieldPermission struct {
	FieldID            string
	CanRead, CanUpdate *bool
}

func putRight(into map[string]any, name string, right *bool) int {
	if right == nil {
		return 0
	}
	into[name] = *right
	return 1
}

// readOwnedObject reads the object once and refuses system objects and any field that is unknown, a system
// field, or not a field of this object.
func (c *Client) readOwnedObject(ctx context.Context, op, objectID string, fieldIDs []string) error {
	var object metaObjectRecord
	if err := c.metaRead(ctx, op, metadataObjectsPath+"/"+url.PathEscape(objectID), "object", &object); err != nil {
		return err
	}
	if !equalUUID(object.ID, objectID) {
		return provider.InvalidResponse(op, "Twenty answered with a different object than the requested one")
	}
	if object.IsSystem == nil || *object.IsSystem {
		return invalidRequest(errPermTarget)
	}
	if len(fieldIDs) == 0 {
		return nil
	}
	items, ok := rawItems(object.Fields)
	if !ok {
		return provider.InvalidResponse(op, "Twenty returned unusable fields")
	}
	known := map[string]bool{}
	for _, item := range items {
		var field metaFieldRecord
		if json.Unmarshal(item, &field) != nil || !validUUID(field.ID) {
			return provider.InvalidResponse(op, "Twenty returned an unusable field")
		}
		if field.IsSystem == nil || *field.IsSystem ||
			(field.ObjectMetadataID != nil && !equalUUID(*field.ObjectMetadataID, objectID)) {
			continue
		}
		known[strings.ToLower(field.ID)] = true
	}
	for _, id := range fieldIDs {
		if !known[strings.ToLower(id)] {
			return invalidRequest(errPermTarget)
		}
	}
	return nil
}

// SetObjectPermissions guards the role, checks the object, then sends exactly one mutation.
func (c *Client) SetObjectPermissions(ctx context.Context, p RoleObjectPermissions) (map[string]any, error) {
	const op = "set object permissions"
	entry := map[string]any{"objectMetadataId": p.ObjectID}
	set := putRight(entry, "canReadObjectRecords", p.CanRead) + putRight(entry, "canUpdateObjectRecords", p.CanUpdate) +
		putRight(entry, "canSoftDeleteObjectRecords", p.CanSoftDelete) +
		putRight(entry, "canDestroyObjectRecords", p.CanDestroy)
	if err := c.guardRole(ctx, op, p.RoleID); err != nil {
		return nil, err
	}
	if err := c.readOwnedObject(ctx, op, p.ObjectID, nil); err != nil {
		return nil, err
	}
	var data struct {
		Result []struct {
			ObjectMetadataID string `json:"objectMetadataId"`
		} `json:"upsertObjectPermissions"`
	}
	variables := map[string]any{"input": map[string]any{"roleId": p.RoleID, "objectPermissions": []any{entry}}}
	if err := c.graphqlWith(ctx, op, roleUncertain, roleObjectPermissionsDocument, variables, &data); err != nil {
		return nil, rolesWritePermission(err)
	}
	bad := data.Result == nil || len(data.Result) > 1
	for _, item := range data.Result {
		bad = bad || !equalUUID(item.ObjectMetadataID, p.ObjectID)
	}
	if bad {
		return nil, provider.InvalidResponse(op, "Twenty did not answer with the requested object"+roleUncertain)
	}
	return map[string]any{"role_id": p.RoleID, "object_id": p.ObjectID, "rights_set": set}, nil
}

// SetFieldPermissions guards the role, checks the object and its fields, then sends exactly one mutation.
func (c *Client) SetFieldPermissions(ctx context.Context, roleID, objectID string,
	fields []RoleFieldPermission) (map[string]any, error) {
	const op = "set field permissions"
	entries := make([]any, 0, len(fields))
	ids := make([]string, 0, len(fields))
	for _, f := range fields {
		entry := map[string]any{"objectMetadataId": objectID, "fieldMetadataId": f.FieldID}
		putRight(entry, "canReadFieldValue", f.CanRead)
		putRight(entry, "canUpdateFieldValue", f.CanUpdate)
		entries = append(entries, entry)
		ids = append(ids, f.FieldID)
	}
	if err := c.guardRole(ctx, op, roleID); err != nil {
		return nil, err
	}
	if err := c.readOwnedObject(ctx, op, objectID, ids); err != nil {
		return nil, err
	}
	var data struct {
		Result []struct {
			RoleID           string `json:"roleId"`
			ObjectMetadataID string `json:"objectMetadataId"`
			FieldMetadataID  string `json:"fieldMetadataId"`
		} `json:"upsertFieldPermissions"`
	}
	variables := map[string]any{"input": map[string]any{"roleId": roleID, "fieldPermissions": entries}}
	if err := c.graphqlWith(ctx, op, roleUncertain, roleFieldPermissionsDocument, variables, &data); err != nil {
		return nil, rolesWritePermission(err)
	}
	bad := data.Result == nil || len(data.Result) > rolePermAnswerMax
	for _, item := range data.Result {
		bad = bad || !equalUUID(item.RoleID, roleID) || !equalUUID(item.ObjectMetadataID, objectID) ||
			!validUUID(item.FieldMetadataID)
	}
	if bad {
		return nil, provider.InvalidResponse(op, "Twenty did not answer with the requested role and object"+roleUncertain)
	}
	return map[string]any{"role_id": roleID, "object_id": objectID, "fields_set": len(fields)}, nil
}

// SetPermissionFlags guards the role, then sends exactly one mutation that replaces the flags of the role.
func (c *Client) SetPermissionFlags(ctx context.Context, roleID string, flags []string) (map[string]any, error) {
	const op = "set permission flags"
	if err := c.guardRole(ctx, op, roleID); err != nil {
		return nil, err
	}
	var data struct {
		Result []struct {
			RoleID string `json:"roleId"`
			Flag   string `json:"flag"`
		} `json:"upsertPermissionFlags"`
	}
	variables := map[string]any{"input": map[string]any{"roleId": roleID, "permissionFlagKeys": flags}}
	if err := c.graphqlWith(ctx, op, roleUncertain, rolePermissionFlagsDocument, variables, &data); err != nil {
		return nil, rolesWritePermission(err)
	}
	bad := data.Result == nil || len(data.Result) > len(flags)
	for _, item := range data.Result {
		bad = bad || !equalUUID(item.RoleID, roleID) || !contains(flags, item.Flag)
	}
	if bad {
		return nil, provider.InvalidResponse(op, "Twenty did not answer with the requested role and flags"+roleUncertain)
	}
	return map[string]any{"role_id": roleID, "flags_set": len(flags)}, nil
}

func invokeRolesSetObjectPermissions(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args roleObjectPermArgs
	if err := decodePermArgs(raw, &args); err != nil {
		return nil, err
	}
	if !validUUID(args.RoleID) {
		return nil, invalidRequest(errRoleID)
	}
	if !validUUID(args.ObjectID) {
		return nil, invalidRequest(errPermArgs)
	}
	if args.CanRead == nil && args.CanUpdate == nil && args.CanSoftDelete == nil && args.CanDestroy == nil {
		return nil, invalidRequest(errPermNone)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SetObjectPermissions(ctx, RoleObjectPermissions{RoleID: args.RoleID, ObjectID: args.ObjectID,
		CanRead: args.CanRead, CanUpdate: args.CanUpdate, CanSoftDelete: args.CanSoftDelete, CanDestroy: args.CanDestroy})
}

func invokeRolesSetFieldPermissions(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args roleFieldPermArgs
	if err := decodePermArgs(raw, &args); err != nil {
		return nil, err
	}
	if !validUUID(args.RoleID) {
		return nil, invalidRequest(errRoleID)
	}
	if !validUUID(args.ObjectID) {
		return nil, invalidRequest(errPermArgs)
	}
	if len(args.Fields) == 0 || len(args.Fields) > rolePermFieldsMax {
		return nil, invalidRequest(errPermFields)
	}
	seen := map[string]bool{}
	fields := make([]RoleFieldPermission, 0, len(args.Fields))
	for _, f := range args.Fields {
		key := strings.ToLower(f.FieldID)
		if !validUUID(f.FieldID) || seen[key] {
			return nil, invalidRequest(errPermFields)
		}
		if f.CanRead == nil && f.CanUpdate == nil {
			return nil, invalidRequest(errPermNone)
		}
		seen[key] = true
		fields = append(fields, RoleFieldPermission{FieldID: f.FieldID, CanRead: f.CanRead, CanUpdate: f.CanUpdate})
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SetFieldPermissions(ctx, args.RoleID, args.ObjectID, fields)
}

func invokeRolesSetPermissionFlags(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args roleFlagArgs
	if err := decodePermArgs(raw, &args); err != nil {
		return nil, err
	}
	if !validUUID(args.RoleID) {
		return nil, invalidRequest(errRoleID)
	}
	if args.Flags == nil || len(*args.Flags) > len(permissionFlagTypes) {
		return nil, invalidRequest(errPermFlags)
	}
	seen := map[string]bool{}
	for _, flag := range *args.Flags {
		if !contains(permissionFlagTypes, flag) || seen[flag] {
			return nil, invalidRequest(errPermFlags)
		}
		seen[flag] = true
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SetPermissionFlags(ctx, args.RoleID, *args.Flags)
}
