package twentycrm

import (
	"encoding/json"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// A relation list of a role beyond its limit is left out whole and marked, never shortened.
const (
	roleMembersMax      = 500
	roleAgentsMax       = 500
	roleAPIKeysMax      = 500
	roleFlagsMax        = 100
	roleObjectRightsMax = 500
	roleFieldRightsMax  = 2000
	roleRowRightsMax    = 500
)

const (
	rolePredicateSelection = `id fieldMetadataId objectMetadataId operand subFieldName workspaceMemberFieldMetadataId ` +
		`workspaceMemberSubFieldName rowLevelPermissionPredicateGroupId positionInRowLevelPermissionPredicateGroup ` +
		`roleId value`
	rolePredicateGroupSelection = `id parentRowLevelPermissionPredicateGroupId logicalOperator ` +
		`positionInRowLevelPermissionPredicateGroup roleId objectMetadataId`
)

const (
	roleStr  = `{"type":"string"}`
	roleBool = `{"type":"boolean"}`
	roleNum  = `{"type":"number"}`

	roleMemberSchema = `{"type":"object","properties":{"id":` + roleStr + `,"name":` + roleStr + `,"email":` + roleStr +
		`,"time_zone":` + roleStr + `,"locale":` + roleStr + `,"color_scheme":` + roleStr + `,"ui_scale":` + roleNum +
		`,"open_record_in":` + roleStr + `,"calendar_start_day":` + roleNum + `,"date_format":` + roleStr +
		`,"time_format":` + roleStr + `,"number_format":` + roleStr + `},"required":["id"],"additionalProperties":false}`
	roleAgentSchema = `{"type":"object","properties":{"id":` + roleStr + `,"name":` + roleStr + `,"label":` + roleStr +
		`},"required":["id"],"additionalProperties":false}`
	roleKeySchema = `{"type":"object","properties":{"id":` + roleStr + `,"name":` + roleStr + `,"expires_at":` + roleStr +
		`,"revoked_at":` + roleStr + `},"required":["id"],"additionalProperties":false}`
	roleFlagSchema = `{"type":"object","properties":{"id":` + roleStr + `,"role_id":` + roleStr + `,"flag":` + roleStr +
		`},"required":["id","flag"],"additionalProperties":false}`
	rolePredicateSchema = `{"type":"object","properties":{"id":` + roleStr + `,"field_metadata_id":` + roleStr +
		`,"object_metadata_id":` + roleStr + `,"operand":` + roleStr + `,"sub_field_name":` + roleStr +
		`,"workspace_member_field_metadata_id":` + roleStr + `,"workspace_member_sub_field_name":` + roleStr +
		`,"row_level_permission_predicate_group_id":` + roleStr +
		`,"position_in_row_level_permission_predicate_group":` + roleNum + `,"role_id":` + roleStr + `,"value":{}` +
		`,"value_omitted":` + roleBool + `},"required":["id"],"additionalProperties":false}`
	rolePredicateGroupSchema = `{"type":"object","properties":{"id":` + roleStr +
		`,"parent_row_level_permission_predicate_group_id":` + roleStr + `,"logical_operator":` + roleStr +
		`,"position_in_row_level_permission_predicate_group":` + roleNum + `,"role_id":` + roleStr +
		`,"object_metadata_id":` + roleStr + `},"required":["id"],"additionalProperties":false}`
	roleRowRightProperties = `"row_level_permission_predicates":{"type":"array","items":` + rolePredicateSchema + `}` +
		`,"row_level_permission_predicates_omitted":` + roleBool +
		`,"row_level_permission_predicate_groups":{"type":"array","items":` + rolePredicateGroupSchema + `}` +
		`,"row_level_permission_predicate_groups_omitted":` + roleBool
	roleObjectRightSchema = `{"type":"object","properties":{"object_metadata_id":` + roleStr +
		`,"can_read_object_records":` + roleBool + `,"can_update_object_records":` + roleBool +
		`,"can_soft_delete_object_records":` + roleBool + `,"can_destroy_object_records":` + roleBool +
		`,"restricted_fields":{"type":"object","additionalProperties":{"type":"object","properties":{"can_read":` +
		roleBool + `,"can_update":` + roleBool + `},"additionalProperties":false}},"restricted_fields_omitted":` +
		roleBool + `,` + roleRowRightProperties + `},"required":["object_metadata_id"],"additionalProperties":false}`
	roleFieldRightSchema = `{"type":"object","properties":{"id":` + roleStr + `,"object_metadata_id":` + roleStr +
		`,"field_metadata_id":` + roleStr + `,"role_id":` + roleStr + `,"can_read_field_value":` + roleBool +
		`,"can_update_field_value":` + roleBool + `},"required":["id"],"additionalProperties":false}`

	// roleDetailProperties are the members of a role beyond its label, flags, and counts.
	roleDetailProperties = `"universal_identifier":` + roleStr + `,"application_id":` + roleStr + `,"icon":` + roleStr +
		`,"agent_count":{"type":"integer"}` +
		`,"workspace_members":{"type":"array","items":` + roleMemberSchema + `},"workspace_members_omitted":` + roleBool +
		`,"agents":{"type":"array","items":` + roleAgentSchema + `},"agents_omitted":` + roleBool +
		`,"api_keys":{"type":"array","items":` + roleKeySchema + `},"api_keys_omitted":` + roleBool +
		`,"permission_flags":{"type":"array","items":` + roleFlagSchema + `},"permission_flags_omitted":` + roleBool +
		`,"object_permissions":{"type":"array","items":` + roleObjectRightSchema + `},"object_permissions_omitted":` + roleBool +
		`,"field_permissions":{"type":"array","items":` + roleFieldRightSchema + `},"field_permissions_omitted":` + roleBool +
		`,` + roleRowRightProperties
)

var roleDetailFields = []capability.Field{
	{Name: "roles[].universal_identifier", Description: "Universal identifier of the role"},
	{Name: "roles[].application_id", Description: "UUID of the application that owns the role"},
	{Name: "roles[].icon", Description: "Icon name, untrusted data"},
	{Name: "roles[].agent_count", Description: "Number of assigned AI agents, absent when Twenty sent no agent list"},
	{Name: "roles[].workspace_members", Description: "At most 500 assigned members with id, name, email, time zone, " +
		"locale, and display and format preferences; never avatar addresses, user account identifiers, or their " +
		"roles. Untrusted personal data"},
	{Name: "roles[].agents", Description: "At most 500 assigned AI agents with id, name, and label, untrusted data"},
	{Name: "roles[].api_keys", Description: "At most 500 assigned API keys with id, name, expiry, and revocation " +
		"time; never a token. Names are untrusted data"},
	{Name: "roles[].permission_flags", Description: "At most 100 settings permission flags the role holds"},
	{Name: "roles[].object_permissions", Description: "At most 500 object rights; restricted_fields maps field " +
		"UUIDs to their read and update rights, at most 2000 per object"},
	{Name: "roles[].field_permissions", Description: "At most 2000 field rights"},
	{Name: "roles[].row_level_permission_predicates", Description: "At most 500 row-level rights, also per object " +
		"right; value is untrusted workspace data of at most 8 levels and 16 KiB, a larger one is left out and " +
		"marked by value_omitted"},
	{Name: "roles[].row_level_permission_predicate_groups", Description: "At most 500 groups that combine row-level " +
		"rights, also per object right"},
	{Name: "roles[].workspace_members_omitted", Description: "True when the role has more than 500 members; the list is left out"},
	{Name: "roles[].agents_omitted", Description: "True when the role has more than 500 AI agents; the list is left out"},
	{Name: "roles[].api_keys_omitted", Description: "True when the role has more than 500 API keys; the list is left out"},
	{Name: "roles[].permission_flags_omitted", Description: "True when the role has more than 100 flags; the list is left out"},
	{Name: "roles[].object_permissions_omitted", Description: "True when the role has more than 500 object rights; the list is left out"},
	{Name: "roles[].field_permissions_omitted", Description: "True when the role has more than 2000 field rights; the list is left out"},
	{Name: "roles[].row_level_permission_predicates_omitted", Description: "True when there are more than 500 row-level rights; the list is left out"},
	{Name: "roles[].row_level_permission_predicate_groups_omitted", Description: "True when there are more than 500 groups; the list is left out"},
}

// RoleDetails are the members of a role beyond its label, flags, and counts.
type RoleDetails struct {
	UniversalIdentifier      string            `json:"universal_identifier,omitempty"`
	ApplicationID            string            `json:"application_id,omitempty"`
	Icon                     string            `json:"icon,omitempty"`
	AgentCount               *int              `json:"agent_count,omitempty"`
	WorkspaceMembers         []WorkspaceMember `json:"workspace_members,omitempty"`
	WorkspaceMembersOmitted  bool              `json:"workspace_members_omitted,omitempty"`
	Agents                   []RoleAgent       `json:"agents,omitempty"`
	AgentsOmitted            bool              `json:"agents_omitted,omitempty"`
	APIKeys                  []RoleAPIKey      `json:"api_keys,omitempty"`
	APIKeysOmitted           bool              `json:"api_keys_omitted,omitempty"`
	PermissionFlags          []RoleFlag        `json:"permission_flags,omitempty"`
	PermissionFlagsOmitted   bool              `json:"permission_flags_omitted,omitempty"`
	ObjectPermissions        []RoleObjectRight `json:"object_permissions,omitempty"`
	ObjectPermissionsOmitted bool              `json:"object_permissions_omitted,omitempty"`
	FieldPermissions         []RoleFieldRight  `json:"field_permissions,omitempty"`
	FieldPermissionsOmitted  bool              `json:"field_permissions_omitted,omitempty"`
	RoleRowRights
}

// RoleRowRights are the row-level rights of a role or of one of its object rights.
type RoleRowRights struct {
	Predicates             []RolePredicate      `json:"row_level_permission_predicates,omitempty"`
	PredicatesOmitted      bool                 `json:"row_level_permission_predicates_omitted,omitempty"`
	PredicateGroups        []RolePredicateGroup `json:"row_level_permission_predicate_groups,omitempty"`
	PredicateGroupsOmitted bool                 `json:"row_level_permission_predicate_groups_omitted,omitempty"`
}

// RoleAgent is an AI agent assigned to a role.
type RoleAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Label string `json:"label,omitempty"`
}

// RoleAPIKey is an API key assigned to a role; Twenty offers no token here and none is read.
type RoleAPIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
}

// RoleFlag is a settings permission flag a role holds.
type RoleFlag struct {
	ID     string `json:"id"`
	RoleID string `json:"role_id,omitempty"`
	Flag   string `json:"flag"`
}

// RoleObjectRight is the right of a role on the records of one object.
type RoleObjectRight struct {
	ObjectMetadataID        string                         `json:"object_metadata_id"`
	CanRead                 *bool                          `json:"can_read_object_records,omitempty"`
	CanUpdate               *bool                          `json:"can_update_object_records,omitempty"`
	CanSoftDelete           *bool                          `json:"can_soft_delete_object_records,omitempty"`
	CanDestroy              *bool                          `json:"can_destroy_object_records,omitempty"`
	RestrictedFields        map[string]RoleRestrictedField `json:"restricted_fields,omitempty"`
	RestrictedFieldsOmitted bool                           `json:"restricted_fields_omitted,omitempty"`
	RoleRowRights
}

// RoleRestrictedField is the read and update right on one field of an object right.
type RoleRestrictedField struct {
	CanRead   *bool `json:"can_read,omitempty"`
	CanUpdate *bool `json:"can_update,omitempty"`
}

// RoleFieldRight is the right of a role on one field.
type RoleFieldRight struct {
	ID               string `json:"id"`
	ObjectMetadataID string `json:"object_metadata_id,omitempty"`
	FieldMetadataID  string `json:"field_metadata_id,omitempty"`
	RoleID           string `json:"role_id,omitempty"`
	CanRead          *bool  `json:"can_read_field_value,omitempty"`
	CanUpdate        *bool  `json:"can_update_field_value,omitempty"`
}

// RolePredicate is one row-level right; its value is untrusted workspace data.
type RolePredicate struct {
	ID                             string      `json:"id"`
	FieldMetadataID                string      `json:"field_metadata_id,omitempty"`
	ObjectMetadataID               string      `json:"object_metadata_id,omitempty"`
	Operand                        string      `json:"operand,omitempty"`
	SubFieldName                   string      `json:"sub_field_name,omitempty"`
	WorkspaceMemberFieldMetadataID string      `json:"workspace_member_field_metadata_id,omitempty"`
	WorkspaceMemberSubFieldName    string      `json:"workspace_member_sub_field_name,omitempty"`
	GroupID                        string      `json:"row_level_permission_predicate_group_id,omitempty"`
	Position                       json.Number `json:"position_in_row_level_permission_predicate_group,omitempty"`
	RoleID                         string      `json:"role_id,omitempty"`
	Value                          any         `json:"value,omitempty"`
	ValueOmitted                   bool        `json:"value_omitted,omitempty"`
}

// RolePredicateGroup combines row-level rights.
type RolePredicateGroup struct {
	ID               string      `json:"id"`
	ParentGroupID    string      `json:"parent_row_level_permission_predicate_group_id,omitempty"`
	LogicalOperator  string      `json:"logical_operator,omitempty"`
	Position         json.Number `json:"position_in_row_level_permission_predicate_group,omitempty"`
	RoleID           string      `json:"role_id,omitempty"`
	ObjectMetadataID string      `json:"object_metadata_id,omitempty"`
}

type roleRowRecord struct {
	Predicates      []json.RawMessage `json:"rowLevelPermissionPredicates"`
	PredicateGroups []json.RawMessage `json:"rowLevelPermissionPredicateGroups"`
}

type roleDetailRecord struct {
	UniversalIdentifier *string           `json:"universalIdentifier"`
	ApplicationID       *string           `json:"applicationId"`
	Icon                *string           `json:"icon"`
	Agents              []json.RawMessage `json:"agents"`
	PermissionFlags     []json.RawMessage `json:"permissionFlags"`
	ObjectPermissions   []json.RawMessage `json:"objectPermissions"`
	FieldPermissions    []json.RawMessage `json:"fieldPermissions"`
	roleRowRecord
}

func (r roleDetailRecord) details(members, keys []json.RawMessage) RoleDetails {
	d := RoleDetails{UniversalIdentifier: uuidOrEmpty(r.UniversalIdentifier),
		ApplicationID: uuidOrEmpty(r.ApplicationID), Icon: cleanText(r.Icon, metaTokenMax),
		RoleRowRights: r.roleRowRecord.rights()}
	if r.Agents != nil {
		count := len(r.Agents)
		d.AgentCount = &count
	}
	d.WorkspaceMembers, d.WorkspaceMembersOmitted = boundedItems(members, roleMembersMax, roleMember)
	d.Agents, d.AgentsOmitted = boundedItems(r.Agents, roleAgentsMax, roleAgent)
	d.APIKeys, d.APIKeysOmitted = boundedItems(keys, roleAPIKeysMax, roleKey)
	d.PermissionFlags, d.PermissionFlagsOmitted = boundedItems(r.PermissionFlags, roleFlagsMax, roleFlag)
	d.ObjectPermissions, d.ObjectPermissionsOmitted = boundedItems(r.ObjectPermissions, roleObjectRightsMax, roleObjectRight)
	d.FieldPermissions, d.FieldPermissionsOmitted = boundedItems(r.FieldPermissions, roleFieldRightsMax, roleFieldRight)
	return d
}

func (r roleRowRecord) rights() RoleRowRights {
	var rights RoleRowRights
	rights.Predicates, rights.PredicatesOmitted = boundedItems(r.Predicates, roleRowRightsMax, rolePredicate)
	rights.PredicateGroups, rights.PredicateGroupsOmitted = boundedItems(r.PredicateGroups, roleRowRightsMax, rolePredicateGroup)
	return rights
}

// boundedItems projects an optional list. A list longer than max is left out whole and reported as omitted;
// an item the projection rejects is skipped.
func boundedItems[T any](raw []json.RawMessage, max int, project func(json.RawMessage) (T, bool)) ([]T, bool) {
	if len(raw) > max {
		return nil, true
	}
	var items []T
	for _, item := range raw {
		if value, ok := project(item); ok {
			items = append(items, value)
		}
	}
	return items, false
}

func uuidOrEmpty(value *string) string {
	if value == nil || !validUUID(*value) {
		return ""
	}
	return *value
}

// numericTextPattern is the plain decimal form of a number that Twenty's GraphQL String type sends as text.
var numericTextPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// numericText keeps a number sent either as a JSON number or as a decimal text, without a float detour.
func numericText(raw json.RawMessage) json.Number {
	if number := plainNumber(raw); number != "" {
		return number
	}
	var text string
	if json.Unmarshal(raw, &text) != nil || len(text) > numberTextMax || !numericTextPattern.MatchString(text) {
		return ""
	}
	return json.Number(text)
}

// roleMember projects a member like workspace-members.get. The role query selects no job title, sort
// position, or timestamps, so whatever else arrives stays out.
func roleMember(raw json.RawMessage) (WorkspaceMember, bool) {
	var record memberRecord
	if json.Unmarshal(raw, &record) != nil {
		return WorkspaceMember{}, false
	}
	member, err := record.project("list roles")
	if err != nil {
		return WorkspaceMember{}, false
	}
	member.UIScale = numericText(record.UIScale)
	member.JobTitle, member.Position, member.CreatedAt, member.UpdatedAt = "", "", "", ""
	return member, true
}

func roleAgent(raw json.RawMessage) (RoleAgent, bool) {
	var record struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Label *string `json:"label"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RoleAgent{}, false
	}
	return RoleAgent{ID: record.ID, Name: cleanText(record.Name, aiNameMax), Label: cleanText(record.Label, aiNameMax)}, true
}

func roleKey(raw json.RawMessage) (RoleAPIKey, bool) {
	var record struct {
		ID        string  `json:"id"`
		Name      *string `json:"name"`
		ExpiresAt *string `json:"expiresAt"`
		RevokedAt *string `json:"revokedAt"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RoleAPIKey{}, false
	}
	return RoleAPIKey{ID: record.ID, Name: cleanText(record.Name, roleLabelMax), ExpiresAt: timestamp(record.ExpiresAt),
		RevokedAt: timestamp(record.RevokedAt)}, true
}

func roleFlag(raw json.RawMessage) (RoleFlag, bool) {
	var record struct {
		ID     string  `json:"id"`
		RoleID *string `json:"roleId"`
		Flag   *string `json:"flag"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RoleFlag{}, false
	}
	flag := shapedText(record.Flag, settingPattern)
	if flag == "" {
		return RoleFlag{}, false
	}
	return RoleFlag{ID: record.ID, RoleID: uuidOrEmpty(record.RoleID), Flag: flag}, true
}

func roleObjectRight(raw json.RawMessage) (RoleObjectRight, bool) {
	var record struct {
		ObjectMetadataID string          `json:"objectMetadataId"`
		CanRead          *bool           `json:"canReadObjectRecords"`
		CanUpdate        *bool           `json:"canUpdateObjectRecords"`
		CanSoftDelete    *bool           `json:"canSoftDeleteObjectRecords"`
		CanDestroy       *bool           `json:"canDestroyObjectRecords"`
		RestrictedFields json.RawMessage `json:"restrictedFields"`
		roleRowRecord
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ObjectMetadataID) {
		return RoleObjectRight{}, false
	}
	right := RoleObjectRight{ObjectMetadataID: record.ObjectMetadataID, CanRead: record.CanRead,
		CanUpdate: record.CanUpdate, CanSoftDelete: record.CanSoftDelete, CanDestroy: record.CanDestroy,
		RoleRowRights: record.roleRowRecord.rights()}
	right.RestrictedFields, right.RestrictedFieldsOmitted = restrictedFields(record.RestrictedFields)
	return right, true
}

// restrictedFields reads the map of field UUIDs to rights; a key that is not a UUID and an entry without a
// right are dropped.
func restrictedFields(raw json.RawMessage) (map[string]RoleRestrictedField, bool) {
	var entries map[string]json.RawMessage
	if isNull(raw) || json.Unmarshal(raw, &entries) != nil {
		return nil, false
	}
	if len(entries) > roleFieldRightsMax {
		return nil, true
	}
	fields := map[string]RoleRestrictedField{}
	for id, value := range entries {
		var field struct {
			CanRead   *bool `json:"canRead"`
			CanUpdate *bool `json:"canUpdate"`
		}
		if !validUUID(id) || json.Unmarshal(value, &field) != nil || (field.CanRead == nil && field.CanUpdate == nil) {
			continue
		}
		fields[id] = RoleRestrictedField{CanRead: field.CanRead, CanUpdate: field.CanUpdate}
	}
	if len(fields) == 0 {
		return nil, false
	}
	return fields, false
}

func roleFieldRight(raw json.RawMessage) (RoleFieldRight, bool) {
	var record struct {
		ID               string  `json:"id"`
		ObjectMetadataID *string `json:"objectMetadataId"`
		FieldMetadataID  *string `json:"fieldMetadataId"`
		RoleID           *string `json:"roleId"`
		CanRead          *bool   `json:"canReadFieldValue"`
		CanUpdate        *bool   `json:"canUpdateFieldValue"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RoleFieldRight{}, false
	}
	return RoleFieldRight{ID: record.ID, ObjectMetadataID: uuidOrEmpty(record.ObjectMetadataID),
		FieldMetadataID: uuidOrEmpty(record.FieldMetadataID), RoleID: uuidOrEmpty(record.RoleID),
		CanRead: record.CanRead, CanUpdate: record.CanUpdate}, true
}

func rolePredicate(raw json.RawMessage) (RolePredicate, bool) {
	var record struct {
		ID                             string          `json:"id"`
		FieldMetadataID                *string         `json:"fieldMetadataId"`
		ObjectMetadataID               *string         `json:"objectMetadataId"`
		Operand                        *string         `json:"operand"`
		SubFieldName                   *string         `json:"subFieldName"`
		WorkspaceMemberFieldMetadataID *string         `json:"workspaceMemberFieldMetadataId"`
		WorkspaceMemberSubFieldName    *string         `json:"workspaceMemberSubFieldName"`
		GroupID                        *string         `json:"rowLevelPermissionPredicateGroupId"`
		Position                       json.RawMessage `json:"positionInRowLevelPermissionPredicateGroup"`
		RoleID                         *string         `json:"roleId"`
		Value                          json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RolePredicate{}, false
	}
	predicate := RolePredicate{ID: record.ID, FieldMetadataID: uuidOrEmpty(record.FieldMetadataID),
		ObjectMetadataID: uuidOrEmpty(record.ObjectMetadataID), Operand: shapedText(record.Operand, settingPattern),
		SubFieldName:                   cleanText(record.SubFieldName, metaTokenMax),
		WorkspaceMemberFieldMetadataID: uuidOrEmpty(record.WorkspaceMemberFieldMetadataID),
		WorkspaceMemberSubFieldName:    cleanText(record.WorkspaceMemberSubFieldName, metaTokenMax),
		GroupID:                        uuidOrEmpty(record.GroupID), Position: plainNumber(record.Position),
		RoleID: uuidOrEmpty(record.RoleID)}
	predicate.Value, predicate.ValueOmitted = boundFreeObject(record.Value)
	return predicate, true
}

func rolePredicateGroup(raw json.RawMessage) (RolePredicateGroup, bool) {
	var record struct {
		ID               string          `json:"id"`
		ParentGroupID    *string         `json:"parentRowLevelPermissionPredicateGroupId"`
		LogicalOperator  *string         `json:"logicalOperator"`
		Position         json.RawMessage `json:"positionInRowLevelPermissionPredicateGroup"`
		RoleID           *string         `json:"roleId"`
		ObjectMetadataID *string         `json:"objectMetadataId"`
	}
	if json.Unmarshal(raw, &record) != nil || !validUUID(record.ID) {
		return RolePredicateGroup{}, false
	}
	return RolePredicateGroup{ID: record.ID, ParentGroupID: uuidOrEmpty(record.ParentGroupID),
		LogicalOperator: shapedText(record.LogicalOperator, settingPattern), Position: plainNumber(record.Position),
		RoleID: uuidOrEmpty(record.RoleID), ObjectMetadataID: uuidOrEmpty(record.ObjectMetadataID)}, true
}
