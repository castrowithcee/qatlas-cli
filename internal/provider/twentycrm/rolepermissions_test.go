package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	wantObjectPermissions = `mutation UpsertObjectPermissions($input: UpsertObjectPermissionsInput!) { upsertObjectPermissions(upsertObjectPermissionsInput: $input) { objectMetadataId } }`
	wantFieldPermissions  = `mutation UpsertFieldPermissions($input: UpsertFieldPermissionsInput!) { upsertFieldPermissions(upsertFieldPermissionsInput: $input) { roleId objectMetadataId fieldMetadataId } }`
	wantPermissionFlags   = `mutation UpsertPermissionFlags($input: UpsertPermissionFlagsInput!) { upsertPermissionFlags(upsertPermissionFlagsInput: $input) { roleId flag } }`

	permSystemField  = "eeeeeeee-0000-4000-8000-000000000005"
	permForeignField = "ffffffff-0000-4000-8000-000000000006"
)

// permObject holds two editable fields, a system field, and a field that claims another object.
func permObject(system string) string {
	field := func(id, sys, owner string) string {
		extra := ""
		if owner != "" {
			extra = `,"objectMetadataId":"` + owner + `"`
		}
		return `{"id":"` + id + `","type":"TEXT","name":"n","label":"l","isSystem":` + sys + extra + `}`
	}
	return `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal",` +
		`"labelPlural":"Deals","isCustom":true,"isSystem":` + system + `,"isActive":true,"fields":[` +
		field(metaFieldID, "false", metaObjID) + `,` + field(metaOtherID, "false", "") + `,` +
		field(permSystemField, "true", "") + `,` + field(permForeignField, "false", roleIDEdit) + `]}`
}

type permCall struct {
	Method, Path, Query string
	Variables           map[string]any
}

// servePerm answers the guard, the object read, and every mutation.
func servePerm(t *testing.T, guard, object string, mutate func() (*http.Response, error)) *[]permCall {
	t.Helper()
	calls := &[]permCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(data, &payload)
		}
		*calls = append(*calls, permCall{request.Method, request.URL.Path, payload.Query, payload.Variables})
		switch {
		case request.Method == http.MethodGet:
			if request.URL.Path != metadataObjectsPath+"/"+metaObjID {
				return jsonResponse(http.StatusNotFound, `{}`), nil
			}
			return jsonResponse(http.StatusOK, object), nil
		case payload.Query == wantRoleGuardDocument:
			return jsonResponse(http.StatusOK, guard), nil
		}
		return mutate()
	})
	stubLimiter(t, cloudKey)
	return calls
}

func permMutations(calls *[]permCall) int {
	n := 0
	for _, call := range *calls {
		if call.Method == http.MethodPost && call.Query != wantRoleGuardDocument {
			n++
		}
	}
	return n
}

func answer(body string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil }
}

type permTool struct {
	name    string
	handler capability.Handler
	args    string
	ok      string
	reads   int // requests before the mutation
}

var permTools = []permTool{
	{"object", invokeRolesSetObjectPermissions, `{"role_id":"` + roleID + `","object_id":"` + metaObjID +
		`","can_read":true,"can_destroy":false}`,
		`{"data":{"upsertObjectPermissions":[{"objectMetadataId":"` + metaObjID + `"}]}}`, 2},
	{"field", invokeRolesSetFieldPermissions, `{"role_id":"` + roleID + `","object_id":"` + metaObjID +
		`","fields":[{"field_id":"` + metaFieldID + `","can_read":false}]}`,
		`{"data":{"upsertFieldPermissions":[{"roleId":"` + roleID + `","objectMetadataId":"` + metaObjID +
			`","fieldMetadataId":"` + metaFieldID + `"}]}}`, 2},
	{"flags", invokeRolesSetPermissionFlags, `{"role_id":"` + roleID + `","flags":["DATA_MODEL"]}`,
		`{"data":{"upsertPermissionFlags":[{"roleId":"` + roleID + `","flag":"DATA_MODEL"}]}}`, 1},
}

func runPerm(tool permTool, args string) (string, error) {
	red := &redact.Redactor{}
	out, err := tool.handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args))
	encoded, _ := json.Marshal(out)
	return string(encoded), err
}

func TestRolePermissionToolsSendTheFixedDocumentsAndVariables(t *testing.T) {
	cases := []struct {
		tool      permTool
		args      string
		document  string
		variables string
		out       string
	}{
		{permTools[0], permTools[0].args, wantObjectPermissions,
			`{"input":{"objectPermissions":[{"canDestroyObjectRecords":false,"canReadObjectRecords":true,` +
				`"objectMetadataId":"` + metaObjID + `"}],"roleId":"` + roleID + `"}}`,
			`{"object_id":"` + metaObjID + `","rights_set":2,"role_id":"` + roleID + `"}`},
		{permTools[1], `{"role_id":"` + roleID + `","object_id":"` + metaObjID + `","fields":[` +
			`{"field_id":"` + metaFieldID + `","can_read":false,"can_update":false},{"field_id":"` + metaOtherID +
			`","can_update":true}]}`, wantFieldPermissions,
			`{"input":{"fieldPermissions":[{"canReadFieldValue":false,"canUpdateFieldValue":false,` +
				`"fieldMetadataId":"` + metaFieldID + `","objectMetadataId":"` + metaObjID + `"},` +
				`{"canUpdateFieldValue":true,"fieldMetadataId":"` + metaOtherID + `","objectMetadataId":"` + metaObjID +
				`"}],"roleId":"` + roleID + `"}}`,
			`{"fields_set":2,"object_id":"` + metaObjID + `","role_id":"` + roleID + `"}`},
		{permTools[2], `{"role_id":"` + roleID + `","flags":["DATA_MODEL","ROLES"]}`, wantPermissionFlags,
			`{"input":{"permissionFlagKeys":["DATA_MODEL","ROLES"],"roleId":"` + roleID + `"}}`,
			`{"flags_set":2,"role_id":"` + roleID + `"}`},
		{permTools[2], `{"role_id":"` + roleID + `","flags":[]}`, wantPermissionFlags,
			`{"input":{"permissionFlagKeys":[],"roleId":"` + roleID + `"}}`, `{"flags_set":0,"role_id":"` + roleID + `"}`},
	}
	for _, tt := range cases {
		answerBody := tt.tool.ok
		if tt.tool.name == "field" {
			answerBody = strings.Replace(answerBody, `]}}`, `,{"roleId":"`+roleID+`","objectMetadataId":"`+metaObjID+
				`","fieldMetadataId":"`+metaOtherID+`"}]}}`, 1)
		}
		if tt.tool.name == "flags" {
			answerBody = `{"data":{"upsertPermissionFlags":[` + map[bool]string{true: `{"roleId":"` + roleID +
				`","flag":"DATA_MODEL"},{"roleId":"` + roleID + `","flag":"ROLES"}`, false: ``}[strings.Contains(tt.args, "ROLES")] + `]}}`
		}
		calls := servePerm(t, freeRole, permObject("false"), answer(answerBody))
		out, err := runPerm(tt.tool, tt.args)
		if err != nil {
			t.Fatalf("%s: %v", tt.tool.name, err)
		}
		if len(*calls) != tt.tool.reads+1 || permMutations(calls) != 1 || (*calls)[0].Query != wantRoleGuardDocument {
			t.Fatalf("%s calls = %+v", tt.tool.name, *calls)
		}
		if tt.tool.reads == 2 && (*calls)[1].Method != http.MethodGet {
			t.Errorf("%s: second call = %+v", tt.tool.name, (*calls)[1])
		}
		last := (*calls)[len(*calls)-1]
		got, _ := json.Marshal(last.Variables)
		if last.Path != metadataPath || last.Query != tt.document || string(got) != tt.variables {
			t.Errorf("%s call = %+v, variables %s", tt.tool.name, last, got)
		}
		if out != tt.out {
			t.Errorf("%s out = %s, want %s", tt.tool.name, out, tt.out)
		}
	}
}

func TestRolePermissionToolsRefuseKeyRolesAndNonEditableRolesBeforeAnyMutation(t *testing.T) {
	for _, tool := range permTools {
		for name, guard := range map[string]string{
			"api key":      guardBody("true", `[{"id":"`+roleIDEdit+`","name":"`+keyNameCn+`"}]`),
			"not editable": guardBody("false", `[]`),
			"keys unknown": guardBody("true", `null`),
			"other role":   `{"data":{"getRole":{"id":"` + roleIDEdit + `","isEditable":true,"apiKeys":[]}}}`,
		} {
			calls := servePerm(t, guard, permObject("false"), answer(tool.ok))
			_, err := runPerm(tool, tool.args)
			if err == nil || len(*calls) != 1 || permMutations(calls) != 0 ||
				strings.Contains(err.Error(), keyNameCn) || strings.Contains(err.Error(), roleIDEdit) {
				t.Errorf("%s %s: err = %v, calls = %+v", tool.name, name, err, *calls)
			}
			if (name == "api key" || name == "not editable") && !asInvalidOK(err) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
}

func TestRolePermissionToolsRefuseUnknownAndSystemTargetsBeforeAnyMutation(t *testing.T) {
	other := "12345678-0000-4000-8000-00000000000a"
	field := func(id string) string {
		return `{"role_id":"` + roleID + `","object_id":"` + metaObjID + `","fields":[{"field_id":"` + id + `","can_read":false}]}`
	}
	cases := []struct {
		name, object string
		tool         permTool
		args         string
	}{
		{"system object", permObject("true"), permTools[0], permTools[0].args},
		{"system object fields", permObject("true"), permTools[1], permTools[1].args},
		{"object without flag", strings.Replace(permObject("false"), `"isSystem":false,"isActive"`, `"isActive"`, 1),
			permTools[0], permTools[0].args},
		{"unknown object", permObject("false"), permTools[0], strings.Replace(permTools[0].args, metaObjID, other, 1)},
		{"unknown field", permObject("false"), permTools[1], field(other)},
		{"foreign field", permObject("false"), permTools[1], field(permForeignField)},
		{"system field", permObject("false"), permTools[1], field(permSystemField)},
		{"one of two unknown", permObject("false"), permTools[1], `{"role_id":"` + roleID + `","object_id":"` +
			metaObjID + `","fields":[{"field_id":"` + metaFieldID + `","can_read":true},{"field_id":"` + other +
			`","can_read":true}]}`},
	}
	for _, tt := range cases {
		calls := servePerm(t, freeRole, tt.object, answer(tt.tool.ok))
		_, err := runPerm(tt.tool, tt.args)
		if err == nil || permMutations(calls) != 0 || strings.Contains(err.Error(), other) ||
			strings.Contains(err.Error(), permForeignField) {
			t.Errorf("%s: err = %v, calls = %+v", tt.name, err, *calls)
		}
	}
}

func TestRolePermissionToolsRefuseObjectTargetsAndBadArgumentsBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, tool := range permTools {
		_, err := tool.handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(tool.args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s targets: err = %v", tool.name, err)
		}
	}
	fields := func(entries string) string {
		return `{"role_id":"` + roleID + `","object_id":"` + metaObjID + `","fields":` + entries + `}`
	}
	entry := func(id string) string { return `{"field_id":"` + id + `","can_read":true}` }
	many := make([]string, 101)
	for i := range many {
		many[i] = entry(fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", i))
	}
	bad := map[string][]string{
		"object": {`{}`, `{"role_id":"` + roleID + `","object_id":"` + metaObjID + `"}`,
			`{"role_id":"nope","object_id":"` + metaObjID + `","can_read":true}`,
			`{"role_id":"` + roleID + `","object_id":"nope","can_read":true}`,
			`{"role_id":"` + roleID + `","object_id":"` + metaObjID + `","can_read":"yes"}`,
			`{"role_id":"` + roleID + `","object_id":"` + metaObjID + `","can_read":true,"row_filter":"x"}`, `[]`, `null`},
		"field": {`{}`, fields(`[]`), fields(`[{"field_id":"` + metaFieldID + `"}]`), fields(`[` + entry("nope") + `]`),
			fields(`[` + entry(metaFieldID) + `,` + entry(strings.ToUpper(metaFieldID)) + `]`),
			fields(`[{"field_id":"` + metaFieldID + `","can_read":true,"can_destroy":true}]`),
			fields(`[` + strings.Join(many, ",") + `]`),
			`{"role_id":"` + roleID + `","fields":[` + entry(metaFieldID) + `]}`},
		"flags": {`{}`, `{"role_id":"` + roleID + `"}`, `{"role_id":"` + roleID + `","flags":null}`,
			`{"role_id":"` + roleID + `","flags":["NOPE"]}`, `{"role_id":"` + roleID + `","flags":["ROLES","ROLES"]}`,
			`{"role_id":"` + roleID + `","flags":["roles"]}`, `{"role_id":"nope","flags":[]}`,
			`{"role_id":"` + roleID + `","flags":[],"extra":1}`},
	}
	for _, tool := range permTools {
		for _, args := range bad[tool.name] {
			if _, err := runPerm(tool, args); err == nil || classOf(err) != "" || !asInvalidOK(err) {
				t.Errorf("%s %.80s: err = %v", tool.name, args, err)
			}
		}
	}
}

func TestRolePermissionToolsNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for _, tool := range permTools {
		for name, mutate := range map[string]func() (*http.Response, error){
			"5xx":        func() (*http.Response, error) { return jsonResponse(http.StatusBadGateway, providerTextCn), nil },
			"timeout":    func() (*http.Response, error) { return nil, context.DeadlineExceeded },
			"abort":      func() (*http.Response, error) { return nil, errors.New("connection reset " + providerTextCn) },
			"unreadable": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":`), nil },
			"empty data": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":null}`), nil },
		} {
			calls := servePerm(t, freeRole, permObject("false"), mutate)
			_, err := runPerm(tool, tool.args)
			if err == nil || permMutations(calls) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v, mutations = %d", tool.name, name, err, permMutations(calls))
			}
		}
	}
}

func TestRolePermissionToolsTreatGraphQLErrorsAndWrongAnswersAsFailures(t *testing.T) {
	errorsBody := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"BAD_USER_INPUT"}}]}`
	for _, tool := range permTools {
		bodies := map[string]string{
			"errors":  errorsBody,
			"partial": strings.TrimSuffix(tool.ok, "}") + `,"errors":[{"message":"x","extensions":{"code":"INTERNAL"}}]}`,
			"null":    `{"data":{"x":null}}`,
		}
		switch tool.name {
		case "object":
			bodies["other object"] = strings.Replace(tool.ok, metaObjID, metaOtherID, 1)
			bodies["two entries"] = strings.Replace(tool.ok, `}]}}`, `},{"objectMetadataId":"`+metaObjID+`"}]}}`, 1)
		case "field":
			bodies["other role"] = strings.Replace(tool.ok, roleID, roleIDEdit, 1)
			bodies["other object"] = strings.Replace(tool.ok, `"objectMetadataId":"`+metaObjID, `"objectMetadataId":"`+metaOtherID, 1)
		case "flags":
			bodies["other role"] = strings.Replace(tool.ok, roleID, roleIDEdit, 1)
			bodies["other flag"] = strings.Replace(tool.ok, "DATA_MODEL", "ROLES", 1)
		}
		for name, body := range bodies {
			calls := servePerm(t, freeRole, permObject("false"), answer(body))
			_, err := runPerm(tool, tool.args)
			if err == nil || permMutations(calls) != 1 || strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
}

func TestRolePermissionToolsNameTheRolesRightOnPermissionFailures(t *testing.T) {
	forbidden := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"FORBIDDEN"}}]}`
	for _, tool := range permTools {
		for name, response := range map[string]func() (*http.Response, error){
			"403":       func() (*http.Response, error) { return jsonResponse(http.StatusForbidden, providerTextCn), nil },
			"FORBIDDEN": answer(forbidden),
		} {
			servePerm(t, freeRole, permObject("false"), response)
			_, err := runPerm(tool, tool.args)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Roles") ||
				strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
}

func TestRolePermissionDescriptorsDeclareRiskAndAllowList(t *testing.T) {
	for _, d := range []capability.Descriptor{rolesSetObjectPermissions, rolesSetFieldPermissions, rolesSetPermissionFlags} {
		want := capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "twentycrm-role-data"}
		if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider || d.Version != 1 {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if !strings.Contains(d.Description, "every member") || !json.Valid(d.InputSchema) || !json.Valid(d.OutputSchema) {
			t.Errorf("%s description or schema: %s", d.ID, d.Description)
		}
	}
	if !strings.Contains(rolesSetPermissionFlags.Description, "withdrawn") {
		t.Error("the flags description does not state the replace semantics")
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, Provider+".roles.set") {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestRolePermissionToolsNeedTheToolsListAndConfirmation(t *testing.T) {
	servePerm(t, freeRole, permObject("false"), answer(permTools[0].ok))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	ids := []string{rolesSetObjectPermissions.ID, rolesSetFieldPermissions.ID, rolesSetPermissionFlags.ID}
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: ids}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	for i, id := range ids {
		args := json.RawMessage(permTools[i].args)
		request := application.InvokeRequest{Operation: id, Connection: "crm", Arguments: args, Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", id)
		}
		request = application.InvokeRequest{Operation: id, Connection: "crm-internal", Arguments: args}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", id)
		}
	}
	request := application.InvokeRequest{Operation: ids[0], Connection: "crm-internal",
		Arguments: json.RawMessage(permTools[0].args), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err != nil {
		t.Errorf("confirmed call on a connection that lists the tool: %v", err)
	}
}
