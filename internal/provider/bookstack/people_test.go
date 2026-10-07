package bookstack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func TestPeopleDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	want := map[string][]string{
		usersList.ID: {"users-manage"}, usersGet.ID: {"users-manage"},
		rolesList.ID: {"user-roles-manage"}, rolesGet.ID: {"user-roles-manage"},
		auditLogList.ID: {"settings-manage", "users-manage"},
	}
	for _, d := range []capability.Descriptor{usersList, usersGet, rolesList, rolesGet, auditLogList} {
		for _, role := range want[d.ID] {
			if !strings.Contains(d.Description, role) {
				t.Errorf("%s description lacks %s", d.ID, role)
			}
		}
		if d.Risk != peopleReadRisk || d.Risk.DataSensitivity != peopleSensitivity ||
			d.Risk.Effect != capability.EffectRead || d.Risk.Idempotency != capability.IdempotencySafe {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if d.RequiresToolAllowList != (d.ID == auditLogList.ID) {
			t.Errorf("%s allowlist = %v", d.ID, d.RequiresToolAllowList)
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, d.ID)
				}
			}
		}
	}
}

func TestUsersListAndGetReduceFields(t *testing.T) {
	rec := &recorder{}
	const full = `"name":"Ann","slug":"ann","email":"ann@example.com","external_auth_id":"ext","created_at":"c","updated_at":"u",` +
		`"last_activity_at":"l","profile_url":"https://x/user/ann","avatar_url":"https://x/avatar","edit_url":"https://x/edit"`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path == "/api/users/3" {
			_, _ = w.Write([]byte(`{"id":3,` + full + `,"roles":[{"id":1,"display_name":"Admin","system_name":"admin"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":3,` + full + `}],"total":1}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	list, err := c.ListUsers(context.Background(), 0, 0)
	if err != nil || len(list.Rows) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if _, has := list.Rows[0]["roles"]; has {
		t.Error("list row carries roles the listing did not deliver")
	}
	for _, row := range list.Rows {
		for _, banned := range []string{"avatar_url", "edit_url"} {
			if _, has := row[banned]; has {
				t.Errorf("row carries %s", banned)
			}
		}
	}
	got, err := c.GetUser(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got.Fields {
		names = append(names, f.Name)
	}
	if want := []string{"id", "name", "slug", "email", "external_auth_id", "created_at", "updated_at", "last_activity_at", "roles", "profile_url"}; !reflect.DeepEqual(names, want) {
		t.Errorf("fields = %v", names)
	}
	roles := got.Fields[8].Value.([]map[string]any)
	if len(roles) != 1 || len(roles[0]) != 2 || roles[0]["display_name"] != "Admin" {
		t.Errorf("roles = %v", roles)
	}
	if got, want := requests(rec), []string{"GET /api/users", "GET /api/users/3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v", got)
	}
}

func TestRolesListAndGetReduceAndCapMembers(t *testing.T) {
	var users strings.Builder
	for i := 1; i <= maxRoleMembers+5; i++ {
		if i > 1 {
			users.WriteString(",")
		}
		fmt.Fprintf(&users, `{"id":%d,"name":"u%d","slug":"u%d"}`, i, i, i)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/roles/2" {
			_, _ = w.Write([]byte(`{"id":2,"display_name":"Editor","description":"d","created_at":"c","updated_at":"u","system_name":"","external_auth_id":"g","mfa_enforced":true,` +
				`"permissions":["page-create-all","page-view-all"],"users":[` + users.String() + `]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":2,"display_name":"Editor","description":"d","created_at":"c","updated_at":"u","system_name":"","external_auth_id":"g","mfa_enforced":true,"users_count":4,"permissions_count":9}],"total":1}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	list, err := c.ListRoles(context.Background(), 0, 0)
	if err != nil || len(list.Rows) != 1 || list.Rows[0]["users_count"] != int64(4) || list.Rows[0]["mfa_enforced"] != true {
		t.Fatalf("list = %v, %v", list, err)
	}
	got, err := c.GetRole(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for _, f := range got.Fields {
		values[f.Name] = f.Value
	}
	if n := len(values["users"].([]map[string]any)); n != maxRoleMembers {
		t.Errorf("users = %d, want %d", n, maxRoleMembers)
	}
	if values["truncated"] != true || !reflect.DeepEqual(values["permissions"], []string{"page-create-all", "page-view-all"}) ||
		values["system_name"] != "" || values["external_auth_id"] != "g" {
		t.Errorf("values = %v", values)
	}
	if _, has := values["users_count"]; has {
		t.Error("get carries the listing counts")
	}
}

func TestPeopleHandlersGateBeforeSecretAndIO(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { rec.record(r) }))
	defer server.Close()
	bound := boundResolved(server.URL, "book/7")
	bound.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"users.list": func() error { _, e := invokeUsersList(ctx, bound, resolver(nil), nil, []byte(`{}`)); return e },
		"users.get":  func() error { _, e := invokeUsersGet(ctx, bound, resolver(nil), nil, []byte(`{"id":1}`)); return e },
		"roles.list": func() error { _, e := invokeRolesList(ctx, bound, resolver(nil), nil, []byte(`{}`)); return e },
		"roles.get":  func() error { _, e := invokeRolesGet(ctx, bound, resolver(nil), nil, []byte(`{"id":1}`)); return e },
		"auditlog.list": func() error {
			_, e := invokeAuditLogList(ctx, bound, resolver(nil), nil, []byte(`{"type":"x"}`))
			return e
		},
		"users.get 0": func() error {
			_, e := invokeUsersGet(ctx, boundResolved(server.URL), resolver(nil), nil, []byte(`{"id":0}`))
			return e
		},
	} {
		if err := call(); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := requests(rec); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
