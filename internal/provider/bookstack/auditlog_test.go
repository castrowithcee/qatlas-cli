package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

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
	filter, _, _, _, err := parseAuditFilter([]byte(`{"type":"page_update","user_id":5,"loggable_type":"page","loggable_id":9,` +
		`"created_after":"2026-10-01","created_before":"2026-10-02T12:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListAuditLog(context.Background(), filter, listQuery{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0]["id"] != int64(2) {
		t.Fatalf("rows = %v, want only entry 2 (client-side check)", got.Rows)
	}
	want := map[string]any{"id": int64(2), "type": "page_update", "detail": "d2", "user_id": int64(5), "user_name": "Ann", "user_slug": "ann",
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
	got, err := c.ListAuditLog(context.Background(), auditFilter{}, listQuery{}, 0, 0)
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
	every := config.Effects()
	cfg.Connections["open"] = config.Connection{Service: "wiki", Credential: "c", Effects: every}
	cfg.Connections["other"] = config.Connection{Service: "wiki", Credential: "c", Effects: every, Tools: []string{usersList.ID}}
	cfg.Connections["listed"] = config.Connection{Service: "wiki", Credential: "c", Effects: every, Tools: []string{auditLogList.ID}}
	for name, want := range map[string]config.Refusal{
		"open": config.RefusalToolAllowList, "other": config.RefusalToolsList, "listed": "",
	} {
		if got := cfg.ConnectionRefusal(name, tool, time.Time{}); got != want {
			t.Errorf("%s: refusal = %q, want %q", name, got, want)
		}
	}
	if !cfg.ConnectionAllows("open", usersList.Tool(), time.Time{}) {
		t.Error("users.list must be offered without a tools list")
	}
}

func TestAuditLogHonorsExclusions(t *testing.T) {
	s := newAreaServer(t)
	inner := s.Config.Handler
	entry := func(id int, kind string, object any) map[string]any {
		return map[string]any{"id": id, "type": "x", "detail": "d", "loggable_type": kind, "loggable_id": object,
			"ip": "10.0.0.1", "created_at": "2026-10-01T10:00:00.000000Z"}
	}
	entries := []map[string]any{
		entry(1, "page", 111),         // in the excluded chapter 1001
		entry(2, "page", 333),         // a loose page of book 701
		entry(3, "chapter", 2001),     // in the excluded book 901
		entry(4, "book", 901),         // excluded
		entry(5, "bookshelf", 302),    // excluded
		entry(6, "bookshelf", 301),    // not excluded
		entry(7, "page", 9999),        // cannot be read, so its container is unknown
		entry(8, "attachment", 81),    // no object the exclusions name
		entry(9, "", nil),             // no loggable fields
		entry(10, "chapter", 1001),    // excluded
		entry(11, "page", 555),        // excluded
		entry(12, "book", 701),        // on shelf 301 only
		entry(13, "page", 444),        // in the excluded book 901
		entry(14, "chapter", 3001),    // cannot be read
		entry(15, "bookshelf", 99999), // another shelf
	}
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/audit-log" {
			inner.ServeHTTP(w, r)
			return
		}
		s.rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": entries, "total": len(entries)})
	})
	got, err := s.callExcluding(t, auditLogList.ID, `{}`, nil, []string{"chapter/1001", "book/901", "shelf/302", "page/555"})
	if err != nil {
		t.Fatal(err)
	}
	// Entries naming an excluded object leave, so do those whose page or chapter cannot be placed outside one;
	// entries without a loggable stay.
	if want := []int64{2, 6, 8, 9, 12, 15}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("ids = %v, want %v", resultIDs(t, got), want)
	}
	// Only a page exclusion reads nothing about containers.
	s.requests()
	got, err = s.callExcluding(t, auditLogList.ID, `{}`, nil, []string{"page/555"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := resultIDs(t, got); len(ids) != 14 || slices.Contains(ids, 11) {
		t.Errorf("ids = %v", ids)
	}
	for _, r := range s.requests() {
		if r != "GET /api/audit-log" {
			t.Errorf("request %s", r)
		}
	}
}
