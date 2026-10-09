package twentycrm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const wantRolesDocument = `query GetRoles { getRoles { id label description isEditable canBeAssignedToUsers ` +
	`canBeAssignedToAgents canBeAssignedToApiKeys canUpdateAllSettings canAccessAllTools canReadAllObjectRecords ` +
	`canUpdateAllObjectRecords canSoftDeleteAllObjectRecords canDestroyAllObjectRecords workspaceMembers { id } ` +
	`apiKeys { id } } }`

const rolesBody = `{"data":{"getRoles":[{"id":"` + roleID + `","label":"Admin\u0007","description":"All rights",` +
	`"icon":"IconLock","isEditable":false,"canBeAssignedToUsers":true,"canBeAssignedToAgents":false,` +
	`"canBeAssignedToApiKeys":true,"canUpdateAllSettings":true,"canAccessAllTools":true,"canReadAllObjectRecords":true,` +
	`"canUpdateAllObjectRecords":true,"canSoftDeleteAllObjectRecords":false,"canDestroyAllObjectRecords":false,` +
	`"workspaceMembers":[{"id":"` + memberID + `","name":{"firstName":"` + personCn + `"}},{"id":"` + memberID2 + `"}],` +
	`"apiKeys":[{"id":"` + roleIDEdit + `","name":"` + keyNameCn + `"}],"objectPermissions":[{"x":"` + schemeCn + `"}]},` +
	`{"id":"` + roleIDEdit + `","label":null,"description":null,"isEditable":true,"workspaceMembers":null,"apiKeys":null}]}}`

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

func TestRolesListSendsTheFixedDocumentAndCountsRelations(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, rolesBody)
	out, err := runMemberTool(t, invokeRolesList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantRolesDocument || len((*calls)[0].variables) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	for _, want := range []string{`"id":"` + roleID + `","label":"Admin","description":"All rights","editable":false`,
		`"assignable_to_users":true,"assignable_to_agents":false,"assignable_to_api_keys":true`,
		`"can_update_all_settings":true,"can_access_all_tools":true,"can_read_all_object_records":true`,
		`"can_soft_delete_all_object_records":false`, `"member_count":2,"api_key_count":1`,
		`"member_count":0,"api_key_count":0`, `"truncated":false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	for _, leaked := range []string{personCn, "IconLock", "objectPermissions", memberID, memberID2} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s reached the output: %s", leaked, out)
		}
	}
	for _, name := range []string{"name", "userId", "avatarUrl"} {
		if strings.Contains(wantRolesDocument, name) {
			t.Errorf("document selects %s", name)
		}
	}
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
