package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type adminListCase struct {
	id, path, filters string
	want              map[string]string
	row               func(i int) map[string]any
}

func adminListCases() []adminListCase {
	return []adminListCase{
		{usersList.ID, "/api/users",
			`[{"field":"email","op":"like","value":"a_b"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"},{"field":"id","op":"lt","value":"9"}]`,
			map[string]string{"filter[email:like]": `%a\_b%`, "filter[updated_at:gte]": "2026-01-02 02:04:05", "filter[id:lt]": "9"},
			func(i int) map[string]any {
				return map[string]any{"id": i, "name": "u", "slug": "u", "email": fmt.Sprintf("a_b%d@x", i), "updated_at": "2026-02-01T00:00:00Z"}
			}},
		{rolesList.ID, "/api/roles",
			`[{"field":"mfa_enforced","value":"true"},{"field":"display_name","value":"Editor"},{"field":"created_at","op":"lte","value":"2026-01-02"}]`,
			map[string]string{"filter[mfa_enforced]": "1", "filter[display_name]": "Editor", "filter[created_at:lte]": "2026-01-02 00:00:00"},
			func(i int) map[string]any {
				return map[string]any{"id": i, "display_name": "Editor", "mfa_enforced": true, "created_at": "2026-01-01T00:00:00Z"}
			}},
		{auditLogList.ID, "/api/audit-log",
			`[{"field":"ip","value":"10.0.0.1"},{"field":"detail","op":"like","value":"x"},{"field":"user_id","op":"gt","value":"2"},{"field":"created_at","op":"gte","value":"2026-01-01"}]`,
			map[string]string{"filter[ip]": "10.0.0.1", "filter[detail:like]": "%x%", "filter[user_id:gt]": "2", "filter[created_at:gte]": "2026-01-01 00:00:00"},
			func(i int) map[string]any {
				return map[string]any{"id": i, "type": "t", "detail": "x", "user_id": 3, "ip": "10.0.0.1", "created_at": "2026-02-01T00:00:00Z"}
			}},
		{recycleBinList.ID, "/api/recycle-bin",
			`[{"field":"deleted_by","value":"4"},{"field":"deletable_type","value":"page"},{"field":"updated_at","op":"gt","value":"2026-01-01"}]`,
			map[string]string{"filter[deleted_by]": "4", "filter[deletable_type]": "page", "filter[updated_at:gt]": "2026-01-01 00:00:00"},
			func(i int) map[string]any {
				return map[string]any{"id": i, "deleted_by": 4, "deletable_type": "page", "deletable_id": i, "updated_at": "2026-02-01T00:00:00Z"}
			}},
		{importsList.ID, "/api/imports",
			`[{"field":"size","op":"gte","value":"10"},{"field":"type","value":"book"},{"field":"created_by","value":"2"}]`,
			map[string]string{"filter[size:gte]": "10", "filter[type]": "book", "filter[created_by]": "2"},
			func(i int) map[string]any {
				return map[string]any{"id": i, "name": "z", "size": 10, "type": "book", "created_by": 2}
			}},
	}
}

func adminHandlerCall(t *testing.T, id, url, args string) error {
	t.Helper()
	_, err := listHandler(t, id)(context.Background(), unboundResolved(url), resolver(nil), nil, json.RawMessage(args))
	return err
}

func TestAdminListsSendSortAndFilters(t *testing.T) {
	for _, tc := range adminListCases() {
		t.Run(tc.id, func(t *testing.T) {
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
			}))
			defer server.Close()
			wantSort := "-created_at"
			if tc.id == importsList.ID {
				wantSort = "-size"
			}
			args := `{"sort":"` + wantSort + `","filters":` + tc.filters + "}"
			if err := adminHandlerCall(t, tc.id, server.URL, args); err != nil {
				t.Fatalf("handler = %v", err)
			}
			_, paths, queries, _ := rec.snapshot()
			if len(paths) != 1 || paths[0] != tc.path {
				t.Fatalf("paths = %v", paths)
			}
			got, _ := url.ParseQuery(queries[0])
			if got.Get("sort") != wantSort {
				t.Errorf("sort = %q", got.Get("sort"))
			}
			for k, v := range tc.want {
				if got.Get(k) != v {
					t.Errorf("%s = %q, want %q (query %v)", k, got.Get(k), v, got)
				}
			}
		})
	}
}

// A server that ignores the filters delivers no foreign row, and limit and offset count the rows that remain.
func TestAdminListsCheckFiltersOnTheClient(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	for _, tc := range adminListCases() {
		t.Run(tc.id, func(t *testing.T) {
			rec := &recorder{}
			// Every second row matches; the others are foreign to the first filter of each case.
			server := filterServer(rec, 12, func(i int) map[string]any {
				row := tc.row(i)
				if i%2 == 1 {
					for k, v := range row {
						switch x := v.(type) {
						case string:
							if k != "updated_at" && k != "created_at" {
								row[k] = "foreign"
							}
						case int:
							if k != "id" {
								row[k] = x + 100
							}
						case bool:
							row[k] = !x
						}
					}
				}
				return row
			})
			defer server.Close()
			var q listQuery
			var err error
			var got []int64
			client := newClient(t, server.URL, nil)
			filters := []filterArg{}
			if err = json.Unmarshal([]byte(tc.filters), &filters); err != nil {
				t.Fatal(err)
			}
			switch tc.id {
			case usersList.ID:
				q, _ = parseListQuery(userListSchema, "", filters)
				c, e := client.ListUsers(context.Background(), 3, 1, q)
				got, err = ids(c.Rows), e
			case rolesList.ID:
				q, _ = parseListQuery(roleListSchema, "", filters)
				c, e := client.ListRoles(context.Background(), 3, 1, q)
				got, err = ids(c.Rows), e
			case auditLogList.ID:
				q, _ = parseListQuery(auditLogListSchema, "", filters)
				c, e := client.ListAuditLog(context.Background(), auditFilter{}, q, 3, 1)
				got, err = ids(c.Rows), e
			case recycleBinList.ID:
				q, _ = parseListQuery(recycleBinListSchema, "", filters)
				c, e := client.ListRecycleBin(context.Background(), 3, 1, q)
				got, err = ids(c.Rows), e
			default:
				q, _ = parseListQuery(importListSchema, "", filters)
				c, e := client.ListImports(context.Background(), 3, 1, q)
				got, err = ids(c.Rows), e
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != "[4 6 8]" {
				t.Errorf("ids = %v, want [4 6 8]", got)
			}
		})
	}
}

// Fields the controller cannot filter or sort are refused before any secret or request.
func TestAdminListsRefuseInvalidQueriesFirst(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	cases := map[string][]string{
		usersList.ID: {`{"filters":[{"field":"created_at","value":"2026-01-01"}]}`, `{"filters":[{"field":"last_activity_at","value":"2026-01-01"}]}`,
			`{"sort":"roles"}`, `{"filters":[{"field":"id","value":"x"}]}`},
		rolesList.ID: {`{"filters":[{"field":"users_count","value":"1"}]}`, `{"filters":[{"field":"permissions_count","value":"1"}]}`,
			`{"filters":[{"field":"id","value":"1"}]}`, `{"filters":[{"field":"system_name","value":"admin"}]}`,
			`{"sort":"id"}`, `{"sort":"system_name"}`, `{"filters":[{"field":"mfa_enforced","op":"gt","value":"true"}]}`},
		auditLogList.ID:   {`{"sort":"user_name"}`, `{"filters":[{"field":"user_name","value":"x"}]}`, `{"filters":[{"field":"user_id","value":"x"}]}`},
		recycleBinList.ID: {`{"sort":"name"}`, `{"filters":[{"field":"name","value":"x"}]}`},
		importsList.ID:    {`{"sort":"details"}`, `{"filters":[{"field":"size","value":"-1"}]}`},
	}
	for id, list := range cases {
		for _, args := range list {
			if err := adminHandlerCall(t, id, server.URL, args); err == nil || !isInvalidRequest(err) {
				t.Errorf("%s %s: err = %v, want invalid-request", id, args, err)
			}
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

// The counts and the activity time sort, the roles list keeps +id without a sort, and the audit log combines its
// own arguments with filters.
func TestAdminListsSortAndCombine(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
	}))
	defer server.Close()
	calls := []struct{ id, args string }{
		{rolesList.ID, `{"sort":"-users_count"}`},
		{rolesList.ID, `{"sort":"permissions_count"}`},
		{rolesList.ID, `{}`},
		{usersList.ID, `{"sort":"-last_activity_at"}`},
		{usersList.ID, `{"sort":"created_at"}`},
		{auditLogList.ID, `{"type":"page_update","filters":[{"field":"type","value":"other"},{"field":"ip","value":"1.2.3.4"}]}`},
	}
	for _, c := range calls {
		if err := adminHandlerCall(t, c.id, server.URL, c.args); err != nil {
			t.Fatalf("%s %s: %v", c.id, c.args, err)
		}
	}
	_, _, queries, _ := rec.snapshot()
	for i, want := range []string{"sort=-users_count", "sort=%2Bpermissions_count", "sort=%2Bid", "sort=-last_activity_at", "sort=%2Bcreated_at"} {
		if !strings.Contains(queries[i], want) {
			t.Errorf("query %d = %s, want %s", i, queries[i], want)
		}
	}
	got, _ := url.ParseQuery(queries[5])
	if got.Get("filter[type]") != "page_update" || got.Get("filter[ip]") != "1.2.3.4" {
		t.Errorf("audit query = %v, want the argument value for type and the ip filter", got)
	}

	// All conditions hold: the existing argument and a filter on the same key must both pass.
	audit := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 2, "data": []any{
			map[string]any{"id": 1, "type": "page_update", "ip": "1.2.3.4", "created_at": "2026-01-01T00:00:00Z"},
			map[string]any{"id": 2, "type": "page_update", "ip": "9.9.9.9", "created_at": "2026-01-01T00:00:00Z"},
		}})
	}))
	defer audit.Close()
	c := newClient(t, audit.URL, nil)
	f := auditFilter{Type: "page_update"}
	q, _ := parseListQuery(auditLogListSchema, "", []filterArg{{"ip", "", "1.2.3.4"}})
	got2, err := c.ListAuditLog(context.Background(), f, q, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got2.Rows)) != "[1]" {
		t.Errorf("ids = %v, want [1]", ids(got2.Rows))
	}
	q, _ = parseListQuery(auditLogListSchema, "", []filterArg{{"type", "", "other"}})
	none, err := c.ListAuditLog(context.Background(), f, q, 0, 0)
	if err != nil || len(none.Rows) != 0 {
		t.Errorf("rows = %v, %v, want none: the filter also holds against the argument", none.Rows, err)
	}
}

// A connection with targets is refused before the query is read, any secret, or any request.
func TestAdminListsStayInstanceWide(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rec.record(r) }))
	defer server.Close()
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, tc := range adminListCases() {
		_, err := listHandler(t, tc.id)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil,
			json.RawMessage(`{"filters":`+tc.filters+`}`))
		if err == nil {
			t.Errorf("%s: accepted on a bound connection", tc.id)
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}
