package bookstack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// bodyServer records the body of every change request. Page 1 and chapter 10 belong to book 7, page 2 and
// chapter 20 to book 9.
type bodyServer struct {
	*httptest.Server
	rec    *recorder
	mu     sync.Mutex
	bodies []map[string]any
}

func newBodyServer(t *testing.T, status int) *bodyServer {
	t.Helper()
	s := &bodyServer{rec: &recorder{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/pages/"):
			if strings.HasSuffix(r.URL.Path, "/2") {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "book_id": 9})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": 7})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/chapters/"):
			book := 7
			if strings.HasSuffix(r.URL.Path, "/20") {
				book = 9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
		default:
			data, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(data, &body)
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			_ = json.NewEncoder(w).Encode(page(1, "Changed"))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *bodyServer) changes() []string {
	methods, paths, _, _ := s.rec.snapshot()
	var out []string
	for i := range methods {
		if methods[i] != http.MethodGet {
			out = append(out, methods[i]+" "+paths[i])
		}
	}
	return out
}

func lookup(t *testing.T, id string) capability.Handler {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	_, handler, ok := reg.Lookup(id)
	if !ok {
		t.Fatalf("operation %s missing", id)
	}
	return handler
}

func TestPageWriteBodies(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		want           map[string]any
	}{
		{"create html", pagesCreate.ID, `{"name":"n","book_id":7,"html":"<p>a & b</p>","priority":3}`,
			map[string]any{"name": "n", "book_id": 7.0, "html": "<p>a & b</p>", "priority": 3.0}},
		{"create markdown with tags", pagesCreate.ID,
			`{"name":"n","chapter_id":10,"markdown":"# t","tags":[{"name":"k","value":"v"},{"name":"solo"}]}`,
			map[string]any{"name": "n", "chapter_id": 10.0, "markdown": "# t",
				"tags": []any{map[string]any{"name": "k", "value": "v"}, map[string]any{"name": "solo", "value": ""}}}},
		{"update html", pagesUpdate.ID, `{"id":1,"html":"<p>x</p>","changelog":"fix"}`,
			map[string]any{"html": "<p>x</p>", "changelog": "fix"}},
		{"update markdown", pagesUpdate.ID, `{"id":1,"markdown":"m"}`, map[string]any{"markdown": "m"}},
		{"update tags replace", pagesUpdate.ID, `{"id":1,"tags":[{"name":"k","value":"v"}]}`,
			map[string]any{"tags": []any{map[string]any{"name": "k", "value": "v"}}}},
		{"update tags clear", pagesUpdate.ID, `{"id":1,"tags":[]}`, map[string]any{"tags": []any{}}},
		{"update priority zero", pagesUpdate.ID, `{"id":1,"priority":0}`, map[string]any{"priority": 0.0}},
		{"move to book", pagesUpdate.ID, `{"id":1,"book_id":7}`, map[string]any{"book_id": 7.0}},
		{"move to chapter", pagesUpdate.ID, `{"id":1,"chapter_id":10}`, map[string]any{"chapter_id": 10.0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newBodyServer(t, 0)
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			handler := lookup(t, tt.op)
			_, err := handler(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if len(server.bodies) != 1 || !reflect.DeepEqual(server.bodies[0], tt.want) {
				t.Errorf("body = %v, want %v", server.bodies, tt.want)
			}
		})
	}
}

func TestPageWriteIsRefusedLocallyBeforeSecretsAndIO(t *testing.T) {
	server := newBodyServer(t, 0)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	tags := func(n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(`{"name":"k"},`, n), ",") + "]"
	}
	huge := strings.Repeat("a", maxPageContentBytes+1)
	long := strings.Repeat("é", 256)
	for _, tt := range []struct{ name, op, args string }{
		{"create html and markdown", pagesCreate.ID, `{"name":"n","book_id":7,"html":"a","markdown":"b"}`},
		{"create neither", pagesCreate.ID, `{"name":"n","book_id":7}`},
		{"create too many tags", pagesCreate.ID, `{"name":"n","book_id":7,"html":"a","tags":` + tags(51) + `}`},
		{"create long tag name", pagesCreate.ID, `{"name":"n","book_id":7,"html":"a","tags":[{"name":"` + long + `"}]}`},
		{"create long tag value", pagesCreate.ID, `{"name":"n","book_id":7,"html":"a","tags":[{"name":"k","value":"` + long + `"}]}`},
		{"create long html", pagesCreate.ID, `{"name":"n","book_id":7,"html":"` + huge + `"}`},
		{"create long markdown", pagesCreate.ID, `{"name":"n","book_id":7,"markdown":"` + huge + `"}`},
		{"create negative priority", pagesCreate.ID, `{"name":"n","book_id":7,"html":"a","priority":-1}`},
		{"update html and markdown", pagesUpdate.ID, `{"id":1,"html":"a","markdown":"b"}`},
		{"update too many tags", pagesUpdate.ID, `{"id":1,"tags":` + tags(51) + `}`},
		{"update long html", pagesUpdate.ID, `{"id":1,"html":"` + huge + `"}`},
		{"update long changelog", pagesUpdate.ID, `{"id":1,"changelog":"` + strings.Repeat("c", 181) + `"}`},
		{"update book and chapter", pagesUpdate.ID, `{"id":1,"book_id":7,"chapter_id":10}`},
		{"update nothing", pagesUpdate.ID, `{"id":1}`},
		{"move to foreign book", pagesUpdate.ID, `{"id":1,"book_id":9}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := lookup(t, tt.op)
			_, err := handler(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(tt.args))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
			if strings.Contains(tt.name, "foreign") && strings.Contains(err.Error(), "9") {
				t.Errorf("err = %v names the foreign book", err)
			}
		})
	}
	if methods, _, _, _ := server.rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func TestMoveIsBoundBySourceAndTarget(t *testing.T) {
	for _, tt := range []struct {
		name, id string
		change   pageMutation
		reads    []string
		changed  bool
		foreign  bool
	}{
		{"own page into own chapter", "1", pageMutation{ChapterID: 10},
			[]string{"GET /api/pages/1", "GET /api/chapters/10"}, true, false},
		{"own page into foreign chapter", "1", pageMutation{ChapterID: 20},
			[]string{"GET /api/pages/1", "GET /api/chapters/20"}, false, true},
		{"foreign source page", "2", pageMutation{ChapterID: 10}, []string{"GET /api/pages/2"}, false, true},
		{"foreign source page into book", "2", pageMutation{BookID: 7}, []string{"GET /api/pages/2"}, false, true},
		{"own page into own book", "1", pageMutation{BookID: 7}, []string{"GET /api/pages/1"}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newBodyServer(t, 0)
			_, err := boundClient(t, server.URL, "book/7").UpdatePage(context.Background(), tt.id, tt.change)
			if tt.foreign != isInvalidRequest(err) || (!tt.foreign && err != nil) {
				t.Fatalf("err = %v", err)
			}
			if tt.foreign && (strings.Contains(err.Error(), "20") || strings.Contains(err.Error(), "9")) {
				t.Errorf("err = %v names the foreign target", err)
			}
			methods, paths, _, _ := server.rec.snapshot()
			var got []string
			for i := range methods {
				if methods[i] == http.MethodGet {
					got = append(got, methods[i]+" "+paths[i])
				}
			}
			if !reflect.DeepEqual(got, tt.reads) {
				t.Errorf("reads = %v, want %v", got, tt.reads)
			}
			if changed := len(server.changes()) == 1; changed != tt.changed {
				t.Errorf("changes = %v, want changed=%v", server.changes(), tt.changed)
			}
		})
	}
}

func TestPageWriteUncertaintyOn5xx(t *testing.T) {
	server := newBodyServer(t, http.StatusBadGateway)
	_, err := newClient(t, server.URL, nil).UpdatePage(context.Background(), "1", pageMutation{ChapterID: 10, HTML: "<p>x</p>"})
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("err = %v, want the uncertainty hint", err)
	}
	if got := server.changes(); len(got) != 1 {
		t.Errorf("changes = %v, want exactly one request", got)
	}
}

func TestPageWriteKeepsMarkupUnescaped(t *testing.T) {
	var raw string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		raw = string(data)
		_ = json.NewEncoder(w).Encode(page(1, "x"))
	}))
	defer server.Close()
	_, err := newClient(t, server.URL, nil).UpdatePage(context.Background(), "1", pageMutation{HTML: strings.Repeat("<", maxPageContentBytes)})
	if err != nil || !strings.HasPrefix(raw, `{"html":"<<`) {
		t.Errorf("err = %v, body start = %.20q", err, raw)
	}
}

func TestGetAndListShowThePageFields(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := page(5, "Full")
		p["priority"] = 4
		p["draft"] = true
		p["template"] = true
		p["owned_by"] = 3
		if r.URL.Path == "/api/pages/5" {
			p["raw_html"] = "<p>raw</p>"
			p["revision_count"] = 6
			p["editor"] = "markdown"
			p["tags"] = []map[string]any{{"name": "k", "value": "v", "order": 0}}
			person := func(id int, name string) map[string]any {
				return map[string]any{"id": id, "name": name, "slug": "private", "email": "hidden"}
			}
			p["created_by"] = person(1, "Creator")
			p["updated_by"] = person(2, "Editor")
			p["owned_by"] = person(3, "Owner")
			p["comments"] = map[string]any{"active": []any{}}
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{p}, "total": 1})
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)

	obj, err := c.GetPage(context.Background(), "5")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, f := range obj.Fields {
		got[f.Name] = f.Value
	}
	if got["raw_html"] != "<p>raw</p>" || got["priority"] != int64(4) || got["draft"] != true || got["template"] != true ||
		got["revision_count"] != int64(6) || got["editor"] != "markdown" {
		t.Errorf("fields = %v", got)
	}
	if !reflect.DeepEqual(got["tags"], []map[string]string{{"name": "k", "value": "v"}}) {
		t.Errorf("tags = %v", got["tags"])
	}
	for name, want := range map[string]map[string]any{
		"created_by": {"id": int64(1), "name": "Creator"},
		"updated_by": {"id": int64(2), "name": "Editor"},
		"owned_by":   {"id": int64(3), "name": "Owner"},
	} {
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s = %v, want %v", name, got[name], want)
		}
	}
	if _, ok := got["comments"]; ok {
		t.Error("comments must not be shown")
	}

	list, err := c.ListPages(context.Background(), listTarget{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := list.Rows[0]
	if row["priority"] != int64(4) || row["draft"] != true || row["template"] != true ||
		!reflect.DeepEqual(row["owned_by"], map[string]any{"id": int64(3), "name": ""}) {
		t.Errorf("row = %v", row)
	}
}
