package bookstack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const auditRows = `{"data":[` +
	`{"id":1,"type":"auth_login","detail":"d1","user_id":5,"loggable_id":null,"loggable_type":null,"ip":"10.0.0.1","created_at":"2026-10-01T10:00:00.000000Z","user":{"id":5,"name":"Ann","slug":"ann"}},` +
	`{"id":2,"type":"page_update","detail":"d2","user_id":5,"loggable_id":9,"loggable_type":"page","ip":"10.0.0.2","created_at":"2026-10-02T10:00:00.000000Z","user":{"id":5,"name":"Ann","slug":"ann"}},` +
	`{"id":3,"type":"page_update","detail":"d3","user_id":6,"loggable_id":9,"loggable_type":"page","ip":"10.0.0.3","created_at":"2026-10-03T10:00:00.000000Z","user":{"id":6,"name":"Bob","slug":"bob"}},` +
	`{"id":4,"type":"book_update","detail":"d4","user_id":5,"loggable_id":2,"loggable_type":"book","ip":"10.0.0.4","created_at":"2026-10-05T10:00:00.000000Z","user":null}` +
	`],"total":4}`

func auditServer(t *testing.T, rec *recorder) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(auditRows)) // ignores every filter, like an instance that does not know them
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAuditLogReducesFieldsAndMapsFilters(t *testing.T) {
	rec := &recorder{}
	c := newClient(t, auditServer(t, rec).URL, nil)
	filter, _, _, err := parseAuditFilter([]byte(`{"type":"page_update","user_id":5,"loggable_type":"page","loggable_id":9,` +
		`"created_after":"2026-10-01","created_before":"2026-10-02T12:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListAuditLog(context.Background(), filter, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0]["id"] != int64(2) {
		t.Fatalf("rows = %v, want only entry 2 (client-side check)", got.Rows)
	}
	want := map[string]any{"id": int64(2), "type": "page_update", "detail": "d2", "user_id": int64(5), "user_name": "Ann",
		"loggable_type": "page", "loggable_id": int64(9), "ip": "10.0.0.2", "created_at": "2026-10-02T10:00:00.000000Z"}
	if !reflect.DeepEqual(map[string]any(got.Rows[0]), want) {
		t.Errorf("row = %v", got.Rows[0])
	}
	_, _, queries, _ := rec.snapshot()
	q, _ := url.ParseQuery(queries[0])
	for key, value := range map[string]string{"filter[type]": "page_update", "filter[user_id]": "5", "filter[loggable_type]": "page",
		"filter[loggable_id]": "9", "filter[created_at:gte]": "2026-10-01 00:00:00", "filter[created_at:lte]": "2026-10-02 12:00:00"} {
		if q.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, q.Get(key), value)
		}
	}
}

func TestAuditLogWithoutFilterSendsNone(t *testing.T) {
	rec := &recorder{}
	c := newClient(t, auditServer(t, rec).URL, nil)
	got, err := c.ListAuditLog(context.Background(), auditFilter{}, 0, 0)
	if err != nil || len(got.Rows) != 4 {
		t.Fatalf("rows = %v, %v", got.Rows, err)
	}
	if _, has := got.Rows[0]["loggable_id"]; has {
		t.Error("null loggable_id was output")
	}
	_, _, queries, _ := rec.snapshot()
	if q, _ := url.ParseQuery(queries[0]); len(q) != 3 {
		t.Errorf("query = %v, want only count, offset, sort", q)
	}
}

func TestAuditLogRejectsInvalidFiltersBeforeIO(t *testing.T) {
	rec := &recorder{}
	server := auditServer(t, rec)
	resolved := boundResolved(server.URL)
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	for _, arguments := range []string{
		`{"type":"Page"}`, `{"type":""}`, `{"type":"a-b"}`, `{"type":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"user_id":0}`, `{"user_id":-1}`, `{"user_id":"1"}`, `{"loggable_type":"user"}`, `{"loggable_id":0}`,
		`{"created_after":"yesterday"}`, `{"created_before":"2026-13-40"}`, `{"limit":-1}`,
	} {
		if _, err := invokeAuditLogList(context.Background(), resolved, resolver(nil), nil, []byte(arguments)); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if got := requests(rec); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestAuditLogIsOfferedOnlyWithAToolsListEntry(t *testing.T) {
	tool := auditLogList.Tool()
	cfg := config.New()
	cfg.Services["wiki"] = config.Service{Provider: Provider, BaseURL: "https://wiki.example"}
	cfg.Credentials["c"] = config.Credential{Type: config.CredentialTypeEnv}
	every := config.Permissions()
	cfg.Connections["open"] = config.Connection{Service: "wiki", Credential: "c", Permissions: every}
	cfg.Connections["other"] = config.Connection{Service: "wiki", Credential: "c", Permissions: every, Tools: []string{usersList.ID}}
	cfg.Connections["listed"] = config.Connection{Service: "wiki", Credential: "c", Permissions: every, Tools: []string{auditLogList.ID}}
	for name, want := range map[string]config.Refusal{
		"open": config.RefusalToolAllowList, "other": config.RefusalToolsList, "listed": "",
	} {
		if got := cfg.ConnectionRefusal(name, tool); got != want {
			t.Errorf("%s: refusal = %q, want %q", name, got, want)
		}
	}
	if !cfg.ConnectionAllows("open", usersList.Tool()) {
		t.Error("users.list must be offered without a tools list")
	}
}
