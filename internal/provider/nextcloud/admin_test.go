package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	adminBase    = "/ocs/v2.php/cloud"
	adminPathBad = "/storage/canary-home-path"
)

func adminConnection(targets ...string) *config.Resolved {
	if len(targets) == 0 {
		targets = []string{"admin"}
	}
	resolved := resolvedConnection("admin", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	resolved.Targets = targets
	return resolved
}

func adminCall(fn func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error),
	resolved *config.Resolved, args string) (any, error) {
	red := &redact.Redactor{}
	return adminBound(fn)(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
}

var adminTools = []struct {
	name string
	fn   func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
	args string
}{
	{"users.list", invokeAdminUsersList, `{}`},
	{"users.get", invokeAdminUsersGet, `{"user_id":"bob"}`},
	{"groups.list", invokeAdminGroupsList, `{}`},
	{"groups.members", invokeAdminGroupsMembers, `{"group_id":"staff"}`},
}

func TestAdminToolsRefuseWithoutTheAdminTargetBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, resolved := range []*config.Resolved{reportsConnection(), accountConnection(), listed("account", "deck")} {
		for _, tool := range adminTools {
			_, err := adminBound(tool.fn)(context.Background(), resolved, res, &redact.Redactor{}, json.RawMessage(tool.args))
			if err == nil || classOf(err) != provider.ClassPermission {
				t.Errorf("%s: err = %v", tool.name, err)
			}
			if err != nil && strings.ContainsAny(err.Error(), "0123456789") {
				t.Errorf("%s names a target: %v", tool.name, err)
			}
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestAdminTargetCannotBeMixed(t *testing.T) {
	refuse(t)
	for _, mixed := range [][]string{{"admin", "account"}, {"admin", "folder/Reports"}, {"admin", "admin"}, {"admin", "deck"}} {
		for _, tool := range adminTools {
			if _, err := adminCall(tool.fn, adminConnection(mixed...), tool.args); err == nil {
				t.Errorf("%v: %s accepted a mixed target set", mixed, tool.name)
			}
		}
	}
}

func TestAdminUsersListPassesPaginationAndSearch(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"users":["alice","bob","carol"]}`)), nil
	})
	got, err := adminCall(invokeAdminUsersList, adminConnection(), `{"search":"a b&c","limit":2,"offset":4}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*AdminIDList)
	if list.Count != 2 || !list.More || list.Offset != 4 || list.IDs[0] != "alice" || list.IDs[1] != "bob" {
		t.Errorf("list = %+v", list)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || (*calls)[0].url.Path != adminBase+"/users" {
		t.Fatalf("calls = %+v", *calls)
	}
	query := (*calls)[0].url.Query()
	if query.Get("limit") != "3" || query.Get("offset") != "4" || query.Get("search") != "a b&c" {
		t.Errorf("query = %v", query)
	}
	if (*calls)[0].auth == "" {
		t.Error("the request is unauthenticated")
	}
}

func TestAdminListsValidateTheirBounds(t *testing.T) {
	refuse(t)
	for _, fn := range []func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		invokeAdminUsersList, invokeAdminGroupsList} {
		for _, args := range []string{`{"limit":0}`, `{"limit":101}`, `{"limit":-1}`, `{"offset":-1}`, `{"offset":100001}`,
			`{"search":"` + strings.Repeat("x", 65) + `"}`, `{"search":"a\u0000b"}`, `{"user_id":"bob"}`, `{"group_id":"g"}`, `{"path":"x"}`} {
			if _, err := adminCall(fn, adminConnection(), args); err == nil {
				t.Errorf("accepted %s", args)
			}
		}
	}
}

func TestAdminGroupsListAndMembers(t *testing.T) {
	var members []string
	for i := 0; i < maxAdminMembers+3; i++ {
		members = append(members, "u"+strings.Repeat("x", i%3))
	}
	raw, _ := json.Marshal(members)
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.EscapedPath(), "/users") {
			return ocsResponse(200, ocsData(`{"users":`+string(raw)+`}`)), nil
		}
		return ocsResponse(200, ocsData(`{"groups":["admin","staff"]}`)), nil
	})
	got, err := adminCall(invokeAdminGroupsList, adminConnection(), `{"limit":5}`)
	if err != nil || got.(*AdminIDList).Count != 2 || got.(*AdminIDList).More || (*calls)[0].url.Path != adminBase+"/groups" ||
		(*calls)[0].url.Query().Get("limit") != "6" {
		t.Errorf("groups = %+v, %v, %+v", got, err, *calls)
	}
	got, err = adminCall(invokeAdminGroupsMembers, adminConnection(), `{"group_id":"Team A"}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*AdminMembers)
	if result.Count != maxAdminMembers || !result.Truncated || result.GroupID != "Team A" {
		t.Errorf("members = %d truncated=%v", result.Count, result.Truncated)
	}
	if (*calls)[1].url.EscapedPath() != adminBase+"/groups/Team%20A/users" {
		t.Errorf("path = %s", (*calls)[1].url.EscapedPath())
	}
}

func TestAdminIDsAreValidatedAndPathSafe(t *testing.T) {
	refuse(t)
	for _, id := range []string{"", ".", "..", "a/b", "../x", "a\\b", "a?b", "a#b", "a%2Fb", "a\nb", " a", "a ", strings.Repeat("x", 65)} {
		for _, tool := range adminTools[1:2] {
			raw, _ := json.Marshal(map[string]string{"user_id": id})
			if _, err := adminCall(tool.fn, adminConnection(), string(raw)); err == nil {
				t.Errorf("user_id %q accepted", id)
			}
		}
		raw, _ := json.Marshal(map[string]string{"group_id": id})
		if _, err := adminCall(invokeAdminGroupsMembers, adminConnection(), string(raw)); err == nil {
			t.Errorf("group_id %q accepted", id)
		}
	}
	for _, args := range []string{`{"user_id":"bob","limit":1}`, `{"user_id":"bob","group_id":"g"}`, `{}`} {
		if _, err := adminCall(invokeAdminUsersGet, adminConnection(), args); err == nil {
			t.Errorf("accepted %s", args)
		}
	}
}

func TestAdminUsersGetReportsEveryDocumentedField(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"enabled":true,"storageLocation":"`+adminPathBad+`","id":"bob","lastLogin":1700000000000,`+
			`"backend":"Database","subadmin":["staff"],"quota":{"free":5,"used":10,"total":100,"relative":10,"quota":-3,"definition":"`+adminPathBad+`"},`+
			`"email":"bob@example.invalid","displayname":"Bob","display-name":"Bob","phone":"phone-1","address":"1 Main Street","website":"https://example.org",`+
			`"twitter":"@bob","groups":["staff","dev"],"language":"de","locale":"de_DE",`+
			`"backendCapabilities":{"setDisplayName":true,"setPassword":false}}`)), nil
	})
	got, err := adminCall(invokeAdminUsersGet, adminConnection(), `{"user_id":"bob"}`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, banned := range []string{adminPathBad, "storageLocation", "display-name", "definition"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("the result carries %q: %s", banned, raw)
		}
	}
	want := `{"id":"bob","display_name":"Bob","email":"bob@example.invalid","enabled":true,"language":"de","locale":"de_DE",` +
		`"last_login":"2023-11-14T22:13:20Z","groups":["staff","dev"],"subadmin_groups":["staff"],"phone":"phone-1",` +
		`"address":"1 Main Street","website":"https://example.org","twitter":"@bob","backend":"Database",` +
		`"backend_capabilities":{"set_display_name":true,"set_password":false},` +
		`"quota":{"used":10,"total":100,"relative":10,"free":5,"quota":-3}}`
	if string(raw) != want {
		t.Errorf("result = %s\nwant     %s", raw, want)
	}
	checkDeclared(t, adminUsersGet, raw, adminUsersGet.OutputSchema)
	if (*calls)[0].url.Path != adminBase+"/users/bob" {
		t.Errorf("path = %s", (*calls)[0].url.Path)
	}
}

func TestAdminUsersGetKeepsOutputWithoutNewMembers(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"id":"bob","displayname":"Bob","enabled":true,"groups":["staff"],`+
			`"quota":{"used":10,"total":100,"relative":10,"free":"n/a","quota":"none"}}`)), nil
	})
	got, err := adminCall(invokeAdminUsersGet, adminConnection(), `{"user_id":"bob"}`)
	raw, _ := json.Marshal(got)
	want := `{"id":"bob","display_name":"Bob","enabled":true,"groups":["staff"],"quota":{"used":10,"total":100,"relative":10}}`
	if err != nil || string(raw) != want {
		t.Errorf("result = %s, %v", raw, err)
	}
}

func TestAdminDisabledUserKeepsEnabledFalse(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"id":"bob","enabled":false,"lastLogin":0}`)), nil
	})
	got, err := adminCall(invokeAdminUsersGet, adminConnection(), `{"user_id":"bob"}`)
	raw, _ := json.Marshal(got)
	if err != nil || string(raw) != `{"id":"bob","enabled":false}` {
		t.Errorf("result = %s, %v", raw, err)
	}
}

func TestAdminForbiddenIsPermissionAndNotFoundIsExplained(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusUnauthorized, http.StatusInternalServerError} {
		serve(t, func(*http.Request) (*http.Response, error) {
			return ocsResponse(status, bodyCanary), nil
		})
		for _, tool := range adminTools {
			_, err := adminCall(tool.fn, adminConnection(), tool.args)
			if err == nil {
				t.Fatalf("%s %d: no error", tool.name, status)
			}
			want := map[int]string{403: "permission", 404: "provider-error", 401: "auth", 500: "provider-error"}[status]
			if string(classOf(err)) != want {
				t.Errorf("%s %d: class = %q, want %q", tool.name, status, classOf(err), want)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %d carries the provider body", tool.name, status)
			}
		}
	}
}

func TestAdminOCSForbiddenInsideASuccessfulStatusIsPermission(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, `{"ocs":{"meta":{"status":"failure","statuscode":403,"message":"`+bodyCanary+`"},"data":[]}}`), nil
	})
	_, err := adminCall(invokeAdminUsersList, adminConnection(), `{}`)
	if err == nil || classOf(err) != provider.ClassPermission {
		t.Errorf("err = %v", err)
	}
}

func TestAdminRedirectsAreNotFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 307} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "outside-canary") {
				t.Errorf("the redirect was followed: %s", r.URL)
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{mainInstance + redirectTarget}},
				Body: http.NoBody}, nil
		})
		for _, tool := range adminTools {
			before := len(*calls)
			_, err := adminCall(tool.fn, adminConnection(), tool.args)
			if err == nil || !strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), "outside-canary") {
				t.Errorf("%s %d: err = %v", tool.name, status, err)
			}
			if len(*calls)-before != 1 {
				t.Errorf("%s %d: %d requests", tool.name, status, len(*calls)-before)
			}
		}
	}
}

func TestConnectionTestOfAnAdminConnectionReadsOneUserPage(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   provider.Class
	}{
		{"admin", 200, provider.ClassOK}, {"not an admin", 403, provider.ClassPermission},
		{"rejected", 401, provider.ClassAuth}, {"moved", 302, provider.ClassProviderError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serve(t, func(*http.Request) (*http.Response, error) {
				if tt.status == 200 {
					return ocsResponse(200, ocsData(`{"users":["alice"]}`)), nil
				}
				return ocsResponse(tt.status, ""), nil
			})
			red := &redact.Redactor{}
			class, err := TestConnection(context.Background(), adminConnection(), resolver(red), red)
			if err != nil || class != tt.want {
				t.Fatalf("TestConnection = %q, %v", class, err)
			}
			if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || (*calls)[0].url.Path != adminBase+"/users" ||
				(*calls)[0].url.Query().Get("limit") != "1" {
				t.Errorf("calls = %+v", *calls)
			}
		})
	}
}
