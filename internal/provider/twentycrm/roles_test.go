package twentycrm

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const wantRolePredicateFields = `id fieldMetadataId objectMetadataId operand subFieldName workspaceMemberFieldMetadataId ` +
	`workspaceMemberSubFieldName rowLevelPermissionPredicateGroupId positionInRowLevelPermissionPredicateGroup roleId value`

const wantRoleGroupFields = `id parentRowLevelPermissionPredicateGroupId logicalOperator ` +
	`positionInRowLevelPermissionPredicateGroup roleId objectMetadataId`

const wantRolesDocument = `query GetRoles { getRoles { id label description isEditable canBeAssignedToUsers ` +
	`canBeAssignedToAgents canBeAssignedToApiKeys canUpdateAllSettings canAccessAllTools canReadAllObjectRecords ` +
	`canUpdateAllObjectRecords canSoftDeleteAllObjectRecords canDestroyAllObjectRecords universalIdentifier ` +
	`applicationId icon workspaceMembers { id name { firstName lastName } userEmail colorScheme uiScale openRecordIn ` +
	`locale calendarStartDay timeZone dateFormat timeFormat numberFormat } agents { id name label } ` +
	`apiKeys { id name expiresAt revokedAt } permissionFlags { id roleId flag } objectPermissions { objectMetadataId ` +
	`canReadObjectRecords canUpdateObjectRecords canSoftDeleteObjectRecords canDestroyObjectRecords restrictedFields ` +
	`rowLevelPermissionPredicates { ` + wantRolePredicateFields + ` } rowLevelPermissionPredicateGroups { ` +
	wantRoleGroupFields + ` } } fieldPermissions { id objectMetadataId fieldMetadataId roleId canReadFieldValue ` +
	`canUpdateFieldValue } rowLevelPermissionPredicates { ` + wantRolePredicateFields + ` } ` +
	`rowLevelPermissionPredicateGroups { ` + wantRoleGroupFields + ` } } }`

const (
	roleUniversalID  = "a1a1a1a1-0000-4000-8000-0000000000a1"
	roleAppID        = "a2a2a2a2-0000-4000-8000-0000000000a2"
	roleAgentID      = "a3a3a3a3-0000-4000-8000-0000000000a3"
	roleKeyID        = "a4a4a4a4-0000-4000-8000-0000000000a4"
	roleFlagID       = "a5a5a5a5-0000-4000-8000-0000000000a5"
	roleFieldID2     = "a6a6a6a6-0000-4000-8000-0000000000a6"
	roleFieldRightID = "a7a7a7a7-0000-4000-8000-0000000000a7"
	rolePredID       = "a8a8a8a8-0000-4000-8000-0000000000a8"
	roleGroupID      = "a9a9a9a9-0000-4000-8000-0000000000a9"

	roleUserWsCn = "canary-user-workspace-id-8855"
	roleNestedCn = "canary-member-role-9966"
	roleTokenCn  = "canary-api-key-token-1077"
	roleBigInt   = "9007199254740993"
)

// legacyRolesBody holds only the fields roles.list read before the role details.
const legacyRolesBody = `{"data":{"getRoles":[{"id":"` + roleID + `","label":"Admin\u0007","description":"All rights",` +
	`"isEditable":false,"canBeAssignedToUsers":true,"canBeAssignedToAgents":false,"canBeAssignedToApiKeys":true,` +
	`"canUpdateAllSettings":true,"canAccessAllTools":true,"canReadAllObjectRecords":true,"canUpdateAllObjectRecords":true,` +
	`"canSoftDeleteAllObjectRecords":false,"canDestroyAllObjectRecords":false},` +
	`{"id":"` + roleIDEdit + `","label":null,"description":null,"isEditable":true,"workspaceMembers":null,"apiKeys":null}]}}`

const wantLegacyRoles = `{"roles":[{"id":"` + roleID + `","label":"Admin","description":"All rights","editable":false,` +
	`"assignable_to_users":true,"assignable_to_agents":false,"assignable_to_api_keys":true,"can_update_all_settings":true,` +
	`"can_access_all_tools":true,"can_read_all_object_records":true,"can_update_all_object_records":true,` +
	`"can_soft_delete_all_object_records":false,"can_destroy_all_object_records":false,"member_count":0,"api_key_count":0},` +
	`{"id":"` + roleIDEdit + `","label":"","editable":true,"assignable_to_users":false,"assignable_to_agents":false,` +
	`"assignable_to_api_keys":false,"can_update_all_settings":false,"can_access_all_tools":false,` +
	`"can_read_all_object_records":false,"can_update_all_object_records":false,"can_soft_delete_all_object_records":false,` +
	`"can_destroy_all_object_records":false,"member_count":0,"api_key_count":0}],"truncated":false}`

// fullRoleJSON is a role in the shape of the Twenty RoleDTO, with canaries in fields that are never selected.
const fullRoleJSON = `{"id":"` + roleID + `","universalIdentifier":"` + roleUniversalID + `","applicationId":"` + roleAppID +
	`","label":"Sales","description":"Sales team","icon":"IconUsers\u0007","isEditable":true,"canBeAssignedToUsers":true,` +
	`"canBeAssignedToAgents":true,"canBeAssignedToApiKeys":true,"canUpdateAllSettings":false,"canAccessAllTools":false,` +
	`"canReadAllObjectRecords":true,"canUpdateAllObjectRecords":false,"canSoftDeleteAllObjectRecords":false,` +
	`"canDestroyAllObjectRecords":false,` +
	`"workspaceMembers":[{"id":"` + memberID + `","name":{"firstName":"` + personCn + `","lastName":"Lovelace\u0007"},` +
	`"userEmail":"` + emailCn + `","colorScheme":"Dark","uiScale":"100","openRecordIn":"SIDE_PANEL","locale":"de-CH",` +
	`"calendarStartDay":1,"timeZone":"Europe/Zurich","dateFormat":"DAY_FIRST","timeFormat":"HOUR_24",` +
	`"numberFormat":"COMMAS_AND_DOT","avatarUrl":"` + avatarCn + `","userId":"` + userIDCn + `","userWorkspaceId":"` +
	roleUserWsCn + `","roles":[{"id":"` + roleIDEdit + `","label":"` + roleNestedCn + `"}]},{"id":"not-a-uuid"}],` +
	`"agents":[{"id":"` + roleAgentID + `","name":"sales-bot","label":"Sales bot","prompt":"` + aiPromptCn + `"}],` +
	`"apiKeys":[{"id":"` + roleKeyID + `","name":"CI key\u0007","expiresAt":"2027-01-01T00:00:00.000Z","revokedAt":null,` +
	`"token":"` + roleTokenCn + `"}],` +
	`"permissionFlags":[{"id":"` + roleFlagID + `","roleId":"` + roleID + `","flag":"WORKSPACE_MEMBERS"},` +
	`{"id":"` + roleFlagID + `","roleId":"` + roleID + `","flag":"<b>"}],` +
	`"objectPermissions":[{"objectMetadataId":"` + metaObjID + `","canReadObjectRecords":true,"canUpdateObjectRecords":false,` +
	`"canSoftDeleteObjectRecords":null,"canDestroyObjectRecords":false,"restrictedFields":{"` + metaFieldID +
	`":{"canRead":false,"canUpdate":false},"nameField":{"canRead":false}},"rowLevelPermissionPredicates":null,` +
	`"rowLevelPermissionPredicateGroups":[{"id":"` + roleGroupID + `","logicalOperator":"OR","roleId":"` + roleID + `"}]}],` +
	`"fieldPermissions":[{"id":"` + roleFieldRightID + `","objectMetadataId":"` + metaObjID + `","fieldMetadataId":"` +
	metaFieldID + `","roleId":"` + roleID + `","canReadFieldValue":false,"canUpdateFieldValue":null}],` +
	`"rowLevelPermissionPredicates":[{"id":"` + rolePredID + `","fieldMetadataId":"` + metaFieldID + `","objectMetadataId":"` +
	metaObjID + `","operand":"IS","subFieldName":null,"workspaceMemberFieldMetadataId":"` + roleFieldID2 + `",` +
	`"workspaceMemberSubFieldName":"primaryEmail","rowLevelPermissionPredicateGroupId":"` + roleGroupID + `",` +
	`"positionInRowLevelPermissionPredicateGroup":` + roleBigInt + `,"roleId":"` + roleID + `",` +
	`"value":{"isCurrentWorkspaceMemberSelected":true,"selectedRecordIds":["` + recordID + `"]}}],` +
	`"rowLevelPermissionPredicateGroups":[{"id":"` + roleGroupID + `","parentRowLevelPermissionPredicateGroupId":null,` +
	`"logicalOperator":"AND","positionInRowLevelPermissionPredicateGroup":0,"roleId":"` + roleID + `",` +
	`"objectMetadataId":"` + metaObjID + `"}]}`

const wantFullRole = `{"id":"` + roleID + `","label":"Sales","description":"Sales team","editable":true,` +
	`"assignable_to_users":true,"assignable_to_agents":true,"assignable_to_api_keys":true,"can_update_all_settings":false,` +
	`"can_access_all_tools":false,"can_read_all_object_records":true,"can_update_all_object_records":false,` +
	`"can_soft_delete_all_object_records":false,"can_destroy_all_object_records":false,"member_count":2,"api_key_count":1,` +
	`"universal_identifier":"` + roleUniversalID + `","application_id":"` + roleAppID + `","icon":"IconUsers",` +
	`"agent_count":1,"workspace_members":[{"id":"` + memberID + `","name":"` + personCn + ` Lovelace","email":"` + emailCn +
	`","time_zone":"Europe/Zurich","locale":"de-CH","color_scheme":"Dark","ui_scale":100,"open_record_in":"SIDE_PANEL",` +
	`"calendar_start_day":1,"date_format":"DAY_FIRST","time_format":"HOUR_24","number_format":"COMMAS_AND_DOT"}],` +
	`"agents":[{"id":"` + roleAgentID + `","name":"sales-bot","label":"Sales bot"}],` +
	`"api_keys":[{"id":"` + roleKeyID + `","name":"CI key","expires_at":"2027-01-01T00:00:00.000Z"}],` +
	`"permission_flags":[{"id":"` + roleFlagID + `","role_id":"` + roleID + `","flag":"WORKSPACE_MEMBERS"}],` +
	`"object_permissions":[{"object_metadata_id":"` + metaObjID + `","can_read_object_records":true,` +
	`"can_update_object_records":false,"can_destroy_object_records":false,"restricted_fields":{"` + metaFieldID +
	`":{"can_read":false,"can_update":false}},"row_level_permission_predicate_groups":[{"id":"` + roleGroupID +
	`","logical_operator":"OR","role_id":"` + roleID + `"}]}],` +
	`"field_permissions":[{"id":"` + roleFieldRightID + `","object_metadata_id":"` + metaObjID + `","field_metadata_id":"` +
	metaFieldID + `","role_id":"` + roleID + `","can_read_field_value":false}],` +
	`"row_level_permission_predicates":[{"id":"` + rolePredID + `","field_metadata_id":"` + metaFieldID +
	`","object_metadata_id":"` + metaObjID + `","operand":"IS","workspace_member_field_metadata_id":"` + roleFieldID2 +
	`","workspace_member_sub_field_name":"primaryEmail","row_level_permission_predicate_group_id":"` + roleGroupID +
	`","position_in_row_level_permission_predicate_group":` + roleBigInt + `,"role_id":"` + roleID +
	`","value":{"isCurrentWorkspaceMemberSelected":true,"selectedRecordIds":["` + recordID + `"]}}],` +
	`"row_level_permission_predicate_groups":[{"id":"` + roleGroupID + `","logical_operator":"AND",` +
	`"position_in_row_level_permission_predicate_group":0,"role_id":"` + roleID + `","object_metadata_id":"` + metaObjID + `"}]}`

type rolesCall struct {
	path, method, query string
	variables           map[string]any
}

func serveRoles(t *testing.T, status int, answer string) *[]rolesCall {
	t.Helper()
	calls := &[]rolesCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("body: %v", err)
		}
		*calls = append(*calls, rolesCall{request.URL.Path, request.Method, payload.Query, payload.Variables})
		return jsonResponse(status, answer), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func assertNoRoleSecrets(t *testing.T, out string) {
	t.Helper()
	for _, leaked := range []string{avatarCn, userIDCn, roleUserWsCn, roleNestedCn, roleTokenCn, aiPromptCn,
		"avatar", "user_id", "user_workspace_id", "token", "prompt", "nameField", "\\u003cb", "\\u0007"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s reached the output: %s", leaked, out)
		}
	}
}

func TestRolesListSendsTheFixedDocumentAndKeepsTheFormerOutput(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, legacyRolesBody)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantRolesDocument || len((*calls)[0].variables) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	if out != wantLegacyRoles {
		t.Errorf("output = %s\nwant     %s", out, wantLegacyRoles)
	}
	for _, name := range []string{"avatarUrl", "userId", "userWorkspaceId", "roles {", "prompt", "token", "key "} {
		if strings.Contains(wantRolesDocument, name) {
			t.Errorf("document selects %s", name)
		}
	}
}

func TestRolesListReadsEveryDocumentedField(t *testing.T) {
	serveRoles(t, http.StatusOK, `{"data":{"getRoles":[`+fullRoleJSON+`]}}`)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"roles":[` + wantFullRole + `],"truncated":false}`; out != want {
		t.Errorf("output = %s\nwant     %s", out, want)
	}
	assertNoRoleSecrets(t, out)
}

func TestRolesListCountsRelationsWithIdentifiersOnly(t *testing.T) {
	serveRoles(t, http.StatusOK, `{"data":{"getRoles":[{"id":"`+roleID+`","label":"x","workspaceMembers":[{"id":"`+
		memberID+`"},{"id":"`+memberID2+`"}],"apiKeys":[{"id":"`+roleKeyID+`"}],"agents":[]}]}}`)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"member_count":2,"api_key_count":1`, `"agent_count":0`,
		`"workspace_members":[{"id":"` + memberID + `"},{"id":"` + memberID2 + `"}]`, `"api_keys":[{"id":"` + roleKeyID + `"}]`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, `"agents"`) {
		t.Errorf("an empty agent list is reported: %s", out)
	}
}

func repeatItems(n int, item string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = item
	}
	return strings.Join(items, ",")
}

func TestRolesListLeavesOutAndMarksListsBeyondTheirLimits(t *testing.T) {
	restricted := make([]string, roleFieldRightsMax+1)
	for i := range restricted {
		restricted[i] = `"` + strings.Replace(metaFieldID, "000000000002", strconv.Itoa(100000000000+i), 1) + `":{"canRead":false}`
	}
	predicate := `{"id":"` + rolePredID + `"}`
	group := `{"id":"` + roleGroupID + `"}`
	over := `{"id":"` + roleID + `","label":"over","workspaceMembers":[` + repeatItems(roleMembersMax+1, `{"id":"`+memberID+`"}`) +
		`],"agents":[` + repeatItems(roleAgentsMax+1, `{"id":"`+roleAgentID+`"}`) +
		`],"apiKeys":[` + repeatItems(roleAPIKeysMax+1, `{"id":"`+roleKeyID+`"}`) +
		`],"permissionFlags":[` + repeatItems(roleFlagsMax+1, `{"id":"`+roleFlagID+`","flag":"WORKSPACE"}`) +
		`],"objectPermissions":[` + repeatItems(roleObjectRightsMax+1, `{"objectMetadataId":"`+metaObjID+`"}`) +
		`],"fieldPermissions":[` + repeatItems(roleFieldRightsMax+1, `{"id":"`+roleFieldRightID+`"}`) +
		`],"rowLevelPermissionPredicates":[` + repeatItems(roleRowRightsMax+1, predicate) +
		`],"rowLevelPermissionPredicateGroups":[` + repeatItems(roleRowRightsMax+1, group) + `]}`
	nested := `{"id":"` + roleIDEdit + `","label":"nested","workspaceMembers":[` + repeatItems(roleMembersMax, `{"id":"`+memberID+`"}`) +
		`],"objectPermissions":[{"objectMetadataId":"` + metaObjID + `","restrictedFields":{` + strings.Join(restricted, ",") +
		`},"rowLevelPermissionPredicates":[` + repeatItems(roleRowRightsMax+1, predicate) +
		`],"rowLevelPermissionPredicateGroups":[` + repeatItems(roleRowRightsMax+1, group) + `]}]}`
	serveRoles(t, http.StatusOK, `{"data":{"getRoles":[`+over+`,`+nested+`]}}`)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var list RoleList
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Roles) != 2 {
		t.Fatalf("output = %v %s", err, out)
	}
	first := list.Roles[0]
	if !first.WorkspaceMembersOmitted || !first.AgentsOmitted || !first.APIKeysOmitted || !first.PermissionFlagsOmitted ||
		!first.ObjectPermissionsOmitted || !first.FieldPermissionsOmitted || !first.PredicatesOmitted ||
		!first.PredicateGroupsOmitted || first.WorkspaceMembers != nil || first.Agents != nil || first.APIKeys != nil ||
		first.PermissionFlags != nil || first.ObjectPermissions != nil || first.FieldPermissions != nil ||
		first.Predicates != nil || first.PredicateGroups != nil {
		t.Errorf("over the limits = %+v", first.RoleDetails)
	}
	if first.MemberCount != roleMembersMax+1 || first.APIKeyCount != roleAPIKeysMax+1 || first.AgentCount == nil ||
		*first.AgentCount != roleAgentsMax+1 {
		t.Errorf("counts = %d %d %v", first.MemberCount, first.APIKeyCount, first.AgentCount)
	}
	second := list.Roles[1]
	if len(second.WorkspaceMembers) != roleMembersMax || second.WorkspaceMembersOmitted || len(second.ObjectPermissions) != 1 {
		t.Fatalf("at the limit = %+v", second.RoleDetails)
	}
	right := second.ObjectPermissions[0]
	if !right.RestrictedFieldsOmitted || right.RestrictedFields != nil || !right.PredicatesOmitted || !right.PredicateGroupsOmitted ||
		right.Predicates != nil || right.PredicateGroups != nil {
		t.Errorf("object right = %+v", right)
	}
}

func TestRolesListBoundsRowLevelValues(t *testing.T) {
	deep := strings.Repeat(`{"a":`, freeObjectDepthMax+1) + `1` + strings.Repeat(`}`, freeObjectDepthMax+1)
	large := `"` + strings.Repeat("v", freeObjectBytesMax+1) + `"`
	predicates := []string{
		`{"id":"` + rolePredID + `","value":` + deep + `}`,
		`{"id":"` + rolePredID + `","value":` + large + `}`,
		`{"id":"` + rolePredID + `","value":` + roleBigInt + `}`,
		`{"id":"` + rolePredID + `","value":["a\u0007","b"]}`,
		`{"id":"` + rolePredID + `","value":"https://share.example/x?token=` + roleTokenCn + `"}`,
		`{"id":"` + rolePredID + `","value":false}`,
	}
	serveRoles(t, http.StatusOK, `{"data":{"getRoles":[{"id":"`+roleID+`","label":"x","rowLevelPermissionPredicates":[`+
		strings.Join(predicates, ",")+`]}]}}`)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"row_level_permission_predicates":[{"id":"` + rolePredID + `","value_omitted":true},{"id":"` + rolePredID +
		`","value_omitted":true},{"id":"` + rolePredID + `","value":` + roleBigInt + `},{"id":"` + rolePredID +
		`","value":["a","b"]},{"id":"` + rolePredID + `"},{"id":"` + rolePredID + `","value":false}]`
	if !strings.Contains(out, want) {
		t.Errorf("output = %s\nwant %s", out, want)
	}
	assertNoRoleSecrets(t, out)
}

func TestRolesListCapsTheListAndText(t *testing.T) {
	var roles []string
	for i := 0; i < rolesMax+1; i++ {
		roles = append(roles, `{"id":"`+roleID+`","label":"`+strings.Repeat("l", 300)+`","description":"`+strings.Repeat("d", 900)+`"}`)
	}
	serveRoles(t, http.StatusOK, `{"data":{"getRoles":[`+strings.Join(roles, ",")+`]}}`)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var list RoleList
	if json.Unmarshal([]byte(out), &list) != nil || len(list.Roles) != rolesMax || !list.Truncated ||
		len(list.Roles[0].Label) != roleLabelMax || len(list.Roles[0].Description) != roleDescriptionMax {
		t.Errorf("roles = %d, truncated = %v", len(list.Roles), list.Truncated)
	}
}

func TestRolesListFailuresAreClassesWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-2b8"
	for answer, class := range map[string]provider.Class{
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"BAD_USER_INPUT"}}]}`:  provider.ClassProviderError,
		`{"data":{"getRoles":[]},"errors":[{"message":"` + canary + `"}]}`:                              provider.ClassProviderError,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"UNAUTHENTICATED"}}]}`: provider.ClassAuth,
		`{"data":{"getRoles":null}}`: provider.ClassInvalidResponse,
		`{"data":{}}`:                provider.ClassInvalidResponse,
		`{"data":{"getRoles":[{"id":"` + canary + `","label":"x"}]}}`: provider.ClassInvalidResponse,
	} {
		serveRoles(t, http.StatusOK, answer)
		_, err := runMemberTool(t, invokeRolesList, `{}`)
		if classOf(err) != class || strings.Contains(err.Error(), canary) {
			t.Errorf("%s = %v, want class %s", answer, err, class)
		}
	}
}

func TestRolesListForbiddenNamesTheRightWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-9c1"
	for name, test := range map[string]struct {
		status int
		body   string
	}{
		"graphql": {http.StatusOK, `{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"FORBIDDEN"}}]}`},
		"http":    {http.StatusForbidden, `{"message":"` + canary + `"}`},
	} {
		serveRoles(t, test.status, test.body)
		_, err := runMemberTool(t, invokeRolesList, `{}`)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), `"Roles"`) ||
			strings.Contains(err.Error(), canary) {
			t.Errorf("%s err = %v", name, err)
		}
	}
}
