package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
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
	wantRoleGuardDocument = `query GetRole($id: UUID!) { getRole(id: $id) { id isEditable apiKeys { id } } }`
	wantRoleCreate        = `mutation CreateOneRole($createRoleInput: CreateRoleInput!) { createOneRole(createRoleInput: $createRoleInput) { id label } }`
	wantRoleUpdate        = `mutation UpdateOneRole($updateRoleInput: UpdateRoleInput!) { updateOneRole(updateRoleInput: $updateRoleInput) { id label } }`
	wantRoleDelete        = `mutation DeleteOneRole($roleId: UUID!) { deleteOneRole(roleId: $roleId) }`
)

type roleCall struct {
	Path, Query string
	Variables   map[string]any
}

// serveRoleWrite answers the guard read with guard and every mutation with mutate.
func serveRoleWrite(t *testing.T, guard string, mutate func() (*http.Response, error)) *[]roleCall {
	t.Helper()
	calls := &[]roleCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		data, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(data, &payload)
		*calls = append(*calls, roleCall{request.URL.Path, payload.Query, payload.Variables})
		if payload.Query == wantRoleGuardDocument {
			return jsonResponse(http.StatusOK, guard), nil
		}
		return mutate()
	})
	stubLimiter(t, cloudKey)
	return calls
}

func roleMutations(calls *[]roleCall) int {
	n := 0
	for _, call := range *calls {
		if call.Query != wantRoleGuardDocument {
			n++
		}
	}
	return n
}

func guardBody(editable, keys string) string {
	return `{"data":{"getRole":{"id":"` + roleID + `","isEditable":` + editable + `,"apiKeys":` + keys + `}}}`
}

const freeRole = `{"data":{"getRole":{"id":"` + roleID + `","isEditable":true,"apiKeys":[]}}}`

func refBody(field string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"`+field+`":{"id":"`+roleID+`","label":"Reader"}}}`), nil
	}
}

type roleTool struct {
	name    string
	handler capability.Handler
	args    string
	field   string
	reads   int
}

var roleTools = []roleTool{
	{"create", invokeRolesCreate, `{"label":"Reader"}`, "createOneRole", 0},
	{"update", invokeRolesUpdate, `{"id":"` + roleID + `","label":"Reader"}`, "updateOneRole", 1},
	{"delete", invokeRolesDelete, `{"id":"` + roleID + `"}`, "deleteOneRole", 1},
}

func runRole(handler capability.Handler, args string) (any, error) {
	red := &redact.Redactor{}
	return handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args))
}

func TestRoleWritesSendTheFixedDocumentsAndVariables(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		calls := serveRoleWrite(t, freeRole, refBody("createOneRole"))
		out, err := runRole(invokeRolesCreate, `{"label":"Reader","description":"Read only","can_read_all_object_records":true,`+
			`"can_destroy_all_object_records":false,"assignable_to_api_keys":false}`)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"createRoleInput": map[string]any{"label": "Reader", "description": "Read only",
			"canReadAllObjectRecords": true, "canDestroyAllObjectRecords": false, "canBeAssignedToApiKeys": false}}
		assertRoleCall(t, calls, 1, 0, wantRoleCreate, want)
		encoded, _ := json.Marshal(out)
		if string(encoded) != `{"id":"`+roleID+`","label":"Reader"}` {
			t.Errorf("out = %s", encoded)
		}
	})
	t.Run("update", func(t *testing.T) {
		calls := serveRoleWrite(t, freeRole, refBody("updateOneRole"))
		out, err := runRole(invokeRolesUpdate, `{"id":"`+roleID+`","label":"Reader","can_access_all_tools":false,`+
			`"can_update_all_settings":false,"assignable_to_users":true,"assignable_to_agents":false,`+
			`"can_update_all_object_records":false,"can_soft_delete_all_object_records":false}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 2 || (*calls)[0].Query != wantRoleGuardDocument || (*calls)[0].Path != metadataPath ||
			(*calls)[0].Variables["id"] != roleID || len((*calls)[0].Variables) != 1 {
			t.Fatalf("guard = %+v", *calls)
		}
		want := map[string]any{"updateRoleInput": map[string]any{"id": roleID, "update": map[string]any{
			"label": "Reader", "canAccessAllTools": false, "canUpdateAllSettings": false, "canBeAssignedToUsers": true,
			"canBeAssignedToAgents": false, "canUpdateAllObjectRecords": false, "canSoftDeleteAllObjectRecords": false}}}
		assertRoleCall(t, calls, 2, 1, wantRoleUpdate, want)
		encoded, _ := json.Marshal(out)
		if string(encoded) != `{"id":"`+roleID+`","label":"Reader"}` {
			t.Errorf("out = %s", encoded)
		}
	})
	t.Run("delete", func(t *testing.T) {
		calls := serveRoleWrite(t, freeRole, func() (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"data":{"deleteOneRole":"`+roleID+`"}}`), nil
		})
		out, err := runRole(invokeRolesDelete, `{"id":"`+roleID+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		assertRoleCall(t, calls, 2, 1, wantRoleDelete, map[string]any{"roleId": roleID})
		encoded, _ := json.Marshal(out)
		if string(encoded) != `{"deleted":true,"id":"`+roleID+`"}` {
			t.Errorf("out = %s", encoded)
		}
	})
}

func assertRoleCall(t *testing.T, calls *[]roleCall, total, index int, document string, variables map[string]any) {
	t.Helper()
	if len(*calls) != total || roleMutations(calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
	got, _ := json.Marshal((*calls)[index].Variables)
	want, _ := json.Marshal(variables)
	if (*calls)[index].Path != metadataPath || (*calls)[index].Query != document || string(got) != string(want) {
		t.Errorf("call = %+v, want variables %s", (*calls)[index], want)
	}
}

func TestRoleGuardRefusesKeyRolesAndNonEditableRolesBeforeTheMutation(t *testing.T) {
	cases := map[string]string{
		"api key":      guardBody("true", `[{"id":"`+roleIDEdit+`","name":"`+keyNameCn+`"}]`),
		"not editable": guardBody("false", `[]`),
		"keys unknown": guardBody("true", `null`),
		"flag missing": `{"data":{"getRole":{"id":"` + roleID + `","apiKeys":[]}}}`,
		"other role":   `{"data":{"getRole":{"id":"` + roleIDEdit + `","isEditable":true,"apiKeys":[]}}}`,
		"no role":      `{"data":{"getRole":null}}`,
		"errors":       `{"data":null,"errors":[{"message":"` + keyNameCn + `","extensions":{"code":"ROLE_NOT_FOUND"}}]}`,
	}
	for _, tool := range roleTools[1:] {
		for name, guard := range cases {
			calls := serveRoleWrite(t, guard, func() (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"data":{}}`), nil
			})
			_, err := runRole(tool.handler, tool.args)
			if err == nil || roleMutations(calls) != 0 || len(*calls) != 1 {
				t.Errorf("%s %s: err = %v, calls = %+v", tool.name, name, err, *calls)
				continue
			}
			if strings.Contains(err.Error(), keyNameCn) || strings.Contains(err.Error(), roleIDEdit) {
				t.Errorf("%s %s: key data reached the error: %v", tool.name, name, err)
			}
			if name == "api key" || name == "not editable" {
				if !asInvalidOK(err) {
					t.Errorf("%s %s: err = %v", tool.name, name, err)
				}
			}
		}
	}
}

func TestRoleGuardReadSelectsNoKeyNames(t *testing.T) {
	for _, forbidden := range []string{"name", "workspaceMembers", "objectPermissions"} {
		if strings.Contains(wantRoleGuardDocument, forbidden) || strings.Contains(roleGuardDocument.text, forbidden) {
			t.Errorf("guard document selects %s", forbidden)
		}
	}
	if roleGuardDocument.text != wantRoleGuardDocument {
		t.Errorf("guard document = %s", roleGuardDocument.text)
	}
}

func TestRoleWritesNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for _, tool := range roleTools {
		for name, mutate := range map[string]func() (*http.Response, error){
			"5xx":        func() (*http.Response, error) { return jsonResponse(http.StatusBadGateway, providerTextCn), nil },
			"timeout":    func() (*http.Response, error) { return nil, context.DeadlineExceeded },
			"abort":      func() (*http.Response, error) { return nil, errors.New("connection reset " + providerTextCn) },
			"unreadable": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":`), nil },
			"empty data": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":null}`), nil },
		} {
			calls := serveRoleWrite(t, freeRole, mutate)
			_, err := runRole(tool.handler, tool.args)
			if err == nil || roleMutations(calls) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v, mutations = %d", tool.name, name, err, roleMutations(calls))
			}
		}
	}
}

func TestRoleWritesTreatGraphQLErrorsAndWrongAnswersAsFailures(t *testing.T) {
	errorsBody := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"BAD_USER_INPUT"}}]}`
	partial := func(field string) string {
		return `{"data":{"` + field + `":{"id":"` + roleID + `","label":"Reader"}},"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	}
	for _, tool := range roleTools {
		for name, body := range map[string]string{"errors": errorsBody, "partial": partial(tool.field),
			"null":        `{"data":{"` + tool.field + `":null}}`,
			"other role":  `{"data":{"` + tool.field + `":{"id":"` + roleIDEdit + `","label":"Reader"}}}`,
			"other label": `{"data":{"` + tool.field + `":{"id":"` + roleID + `","label":"Other"}}}`,
			"other id":    `{"data":{"` + tool.field + `":"` + roleIDEdit + `"}}`,
		} {
			if tool.name == "create" && name == "other role" {
				continue // a created role has a new identifier
			}
			if tool.name != "create" && name == "other label" {
				continue
			}
			if tool.name != "delete" && name == "other id" {
				continue
			}
			if tool.name == "delete" && (name == "other role" || name == "other label") {
				continue
			}
			calls := serveRoleWrite(t, freeRole, func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
			_, err := runRole(tool.handler, tool.args)
			if err == nil || roleMutations(calls) != 1 || strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
}

func TestRoleWritesNameTheRolesRightOnPermissionFailures(t *testing.T) {
	forbidden := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"FORBIDDEN"}}]}`
	for _, tool := range roleTools {
		for name, response := range map[string]func() (*http.Response, error){
			"403":       func() (*http.Response, error) { return jsonResponse(http.StatusForbidden, providerTextCn), nil },
			"FORBIDDEN": func() (*http.Response, error) { return jsonResponse(http.StatusOK, forbidden), nil },
		} {
			serveRoleWrite(t, freeRole, response)
			_, err := runRole(tool.handler, tool.args)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Roles") ||
				strings.Contains(err.Error(), providerTextCn) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
	// The guard read itself fails on the right.
	for _, tool := range roleTools[1:] {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, providerTextCn), nil
		})
		stubLimiter(t, cloudKey)
		_, err := runRole(tool.handler, tool.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Roles") {
			t.Errorf("%s guard 403: err = %v", tool.name, err)
		}
	}
}

func TestRoleWritesRefuseObjectTargetsAndBadArgumentsBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, tool := range roleTools {
		// A nil resolver would fail on any secret access.
		_, err := tool.handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(tool.args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s targets: err = %v", tool.name, err)
		}
	}
	long := strings.Repeat("a", 101)
	bad := map[string][]string{
		"create": {`{}`, `{"label":""}`, `{"label":"  "}`, `{"label":"` + long + `"}`, `{"label":"a\u0007"}`,
			`{"label":"a","description":"` + strings.Repeat("b", 501) + `"}`, `{"label":"a","description":"x\ny"}`,
			`{"label":"a","can_read_all_object_records":"yes"}`, `{"label":"a","icon":"x"}`,
			`{"label":"a","id":"` + roleID + `"}`, `{"label":"a","api_key":"x"}`, `[]`, `null`},
		"update": {`{"id":"` + roleID + `"}`, `{"label":"a"}`, `{"id":"nope","label":"a"}`, `{"id":"` + roleID + `","label":""}`,
			`{"id":"` + roleID + `","label":"` + long + `"}`, `{"id":"` + roleID + `","label":"a\u0000"}`,
			`{"id":"` + roleID + `","icon":"x"}`, `{"id":"` + roleID + `","can_access_all_tools":1}`},
		"delete": {`{}`, `{"id":"nope"}`, `{"id":"` + roleID + `","label":"a"}`, `{"id":"` + roleID + `,x"}`},
	}
	for _, tool := range roleTools {
		for _, args := range bad[tool.name] {
			calls := serveRoleWrite(t, freeRole, refBody(tool.field))
			if _, err := runRole(tool.handler, args); err == nil || classOf(err) != "" || !asInvalidOK(err) || len(*calls) != 0 {
				t.Errorf("%s %s: err = %v, calls = %d", tool.name, args, err, len(*calls))
			}
		}
	}
}

func TestRoleWriteDescriptorsDeclareRisk(t *testing.T) {
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{rolesCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{rolesUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
		{rolesDelete, capability.EffectDelete, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idem, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: "twentycrm-role-data"}
		if tt.d.Risk != want || !tt.d.RequiresToolAllowList || tt.d.Provider != Provider || tt.d.Version != 1 {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
		if !strings.Contains(tt.d.Description, "every member") && !strings.Contains(tt.d.Description, "members") {
			t.Errorf("%s description names no effect on members: %s", tt.d.ID, tt.d.Description)
		}
		if !json.Valid(tt.d.InputSchema) || !json.Valid(tt.d.OutputSchema) {
			t.Errorf("%s has an invalid schema", tt.d.ID)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == rolesCreate.ID || id == rolesUpdate.ID || id == rolesDelete.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestRoleWritesNeedTheToolsListAndConfirmation(t *testing.T) {
	serveRoleWrite(t, freeRole, refBody("createOneRole"))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	ids := []string{rolesCreate.ID, rolesUpdate.ID, rolesDelete.ID}
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: ids}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	args := map[string]string{rolesCreate.ID: roleTools[0].args, rolesUpdate.ID: roleTools[1].args, rolesDelete.ID: roleTools[2].args}
	for _, id := range ids {
		request := application.InvokeRequest{Operation: id, Connection: "crm", Arguments: json.RawMessage(args[id]), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", id)
		}
		request = application.InvokeRequest{Operation: id, Connection: "crm-internal", Arguments: json.RawMessage(args[id])}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", id)
		}
	}
	request := application.InvokeRequest{Operation: rolesCreate.ID, Connection: "crm-internal",
		Arguments: json.RawMessage(args[rolesCreate.ID]), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err != nil {
		t.Errorf("confirmed create on a connection that lists the tool: %v", err)
	}
}
