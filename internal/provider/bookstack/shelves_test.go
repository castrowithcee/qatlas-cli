package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// shelfServer serves a few shelves and records every change body.
type shelfServer struct {
	*httptest.Server
	rec    *recorder
	mu     sync.Mutex
	bodies []map[string]any
}

func newShelfServer(t *testing.T, shelves int) *shelfServer {
	t.Helper()
	s := &shelfServer{rec: &recorder{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/shelves":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			count, _ := strconv.Atoi(r.URL.Query().Get("count"))
			data := []map[string]any{}
			for id := offset + 1; id <= shelves && len(data) < count; id++ {
				data = append(data, map[string]any{"id": id, "name": "Shelf", "slug": "shelf", "description": "d", "hidden": "x"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": shelves})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 3, "name": "Shelf", "slug": "shelf", "hidden": "x", "owned_by": testUser,
				"tags":  []map[string]any{{"name": "k", "value": "v"}},
				"books": []map[string]any{{"id": 7, "name": "B", "slug": "b", "hidden": "x"}, {"id": 9, "name": "C", "slug": "c"}},
			})
		default:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "name": "Shelf", "slug": "shelf", "hidden": "x",
				"books": []map[string]any{{"id": 7}}})
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *shelfServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

func TestRequireInstanceScope(t *testing.T) {
	for _, tt := range []struct {
		name    string
		targets []string
		ok      bool
	}{
		{"none", nil, true},
		{"one book", []string{"book/7"}, false},
		{"several books", []string{"book/7", "book/9"}, false},
		{"malformed", []string{"bogus"}, false},
	} {
		err := requireInstanceScope(&config.Resolved{Targets: tt.targets}, "things")
		if tt.ok != (err == nil) {
			t.Fatalf("%s: err = %v", tt.name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "7") || strings.Contains(err.Error(), "9") || strings.Contains(err.Error(), "bogus")) {
			t.Fatalf("%s: err = %v names a target", tt.name, err)
		}
	}
	if err := requireInstanceScope(&config.Resolved{Targets: []string{"book/7"}}, "things"); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "things") {
		t.Fatalf("err = %v", err)
	}
	if err := requireInstanceScope(nil, "things"); err == nil {
		t.Fatal("a missing connection must be refused")
	}
}

func TestShelvesAreRefusedOnABoundConnectionBeforeSecretsAndIO(t *testing.T) {
	server := newShelfServer(t, 2)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, d := range []capability.Descriptor{shelvesList, shelvesGet, shelvesCreate, shelvesUpdate} {
		args := map[string]string{
			shelvesList.ID: `{}`, shelvesGet.ID: `{"id":3}`, shelvesCreate.ID: `{"name":"S"}`, shelvesUpdate.ID: `{"id":3,"name":"S"}`,
		}[d.ID]
		_, err := lookup(t, d.ID)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(args))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "7") {
			t.Errorf("%s: err = %v, want an invalid-request without the target", d.ID, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestShelfWriteBodies(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		want           map[string]any
		request        string
	}{
		{"create", shelvesCreate.ID, `{"name":"S","description":"d","tags":[{"name":"k","value":"v"}],"books":[9,7]}`,
			map[string]any{"name": "S", "description": "d", "tags": []any{map[string]any{"name": "k", "value": "v"}}, "books": []any{9.0, 7.0}},
			"POST /api/shelves"},
		{"create without books", shelvesCreate.ID, `{"name":"S","description_html":"<p>a & b</p>"}`,
			map[string]any{"name": "S", "description_html": "<p>a & b</p>"}, "POST /api/shelves"},
		{"update omits books", shelvesUpdate.ID, `{"id":3,"name":"N"}`,
			map[string]any{"name": "N"}, "PUT /api/shelves/3"},
		{"update empty books clears", shelvesUpdate.ID, `{"id":3,"books":[]}`,
			map[string]any{"books": []any{}}, "PUT /api/shelves/3"},
		{"update books replaces", shelvesUpdate.ID, `{"id":3,"books":[8,7]}`,
			map[string]any{"books": []any{8.0, 7.0}}, "PUT /api/shelves/3"},
		{"update clears tags", shelvesUpdate.ID, `{"id":3,"tags":[]}`,
			map[string]any{"tags": []any{}}, "PUT /api/shelves/3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newShelfServer(t, 0)
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			result, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(tt.args))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), `"books"`) {
				t.Errorf("result leaks provider fields: %s", encoded)
			}
			if len(server.bodies) != 1 || !reflect.DeepEqual(server.bodies[0], tt.want) {
				t.Errorf("body = %v, want %v", server.bodies, tt.want)
			}
			if got := server.requests(); !reflect.DeepEqual(got, []string{tt.request}) {
				t.Errorf("requests = %v, want %v", got, tt.request)
			}
		})
	}
}

func TestShelfWriteIsRefusedLocallyBeforeSecretsAndIO(t *testing.T) {
	server := newShelfServer(t, 0)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	many := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = strconv.Itoa(i + 1)
		}
		return "[" + strings.Join(ids, ",") + "]"
	}
	for _, tt := range []struct{ name, op, args string }{
		{"create empty name", shelvesCreate.ID, `{"name":""}`},
		{"create both descriptions", shelvesCreate.ID, `{"name":"S","description":"a","description_html":"b"}`},
		{"create too many books", shelvesCreate.ID, `{"name":"S","books":` + many(501) + `}`},
		{"create duplicate books", shelvesCreate.ID, `{"name":"S","books":[7,7]}`},
		{"create bad book id", shelvesCreate.ID, `{"name":"S","books":[0]}`},
		{"update too many books", shelvesUpdate.ID, `{"id":3,"books":` + many(501) + `}`},
		{"update duplicate books", shelvesUpdate.ID, `{"id":3,"books":[1,2,1]}`},
		{"update nothing", shelvesUpdate.ID, `{"id":3}`},
		{"update bad id", shelvesUpdate.ID, `{"id":0,"name":"S"}`},
		{"get bad id", shelvesGet.ID, `{"id":0}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(tt.args))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
		})
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
	// The ceiling itself is accepted.
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	if _, err := lookup(t, shelvesUpdate.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil,
		json.RawMessage(`{"id":3,"books":`+many(500)+`}`)); err != nil {
		t.Errorf("500 books: %v", err)
	}
}

func TestShelvesListAndGet(t *testing.T) {
	server := newShelfServer(t, 3)
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	result, err := lookup(t, shelvesList.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(`{"limit":2,"offset":1}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(output.Collection).Rows
	if got := ids(rows); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Errorf("ids = %v", got)
	}
	if _, ok := rows[0]["hidden"]; ok {
		t.Error("list row leaks provider fields")
	}
	got, err := lookup(t, shelvesGet.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(`{"id":3}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "hidden") {
		t.Errorf("result leaks provider fields: %s", encoded)
	}
	fields := got.(output.Object).Fields
	var books []map[string]any
	for _, f := range fields {
		if f.Name == "books" {
			books = f.Value.([]map[string]any)
		}
	}
	if len(books) != 2 || books[0]["id"] != int64(7) {
		t.Errorf("books = %v", books)
	}
	if got := server.requests(); got[len(got)-1] != "GET /api/shelves/3" {
		t.Errorf("requests = %v", got)
	}
}

func TestShelfBookListIsCapped(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		books := make([]map[string]any, maxTreeEntries+5)
		for i := range books {
			books[i] = map[string]any{"id": i + 1, "name": "B", "slug": "b"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "name": "S", "slug": "s", "books": books})
	}))
	defer server.Close()
	object, err := boundClient(t, server.URL).GetShelf(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range object.Fields {
		switch f.Name {
		case "books":
			if n := len(f.Value.([]map[string]any)); n != maxTreeEntries {
				t.Errorf("books = %d", n)
			}
		case "truncated":
			if f.Value != true {
				t.Error("truncated not set")
			}
		}
	}
}

func TestShelfWritesReportUncertaintyAndRisk(t *testing.T) {
	const hint = "this change may have taken effect, read the current state in BookStack before repeating it"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	if _, err := c.CreateShelf(context.Background(), shelfMutation{Name: "n"}); err == nil || !strings.Contains(err.Error(), hint) {
		t.Errorf("create: %v", err)
	}
	if _, err := c.UpdateShelf(context.Background(), 1, shelfMutation{Name: "n"}); err == nil || !strings.Contains(err.Error(), hint) {
		t.Errorf("update: %v", err)
	}
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		{shelvesCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{shelvesUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idempotency, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: "bookstack-content"}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", tt.d.ID, tt.d.Risk)
		}
	}
	if !strings.Contains(shelvesUpdate.Description, "replaces all books") {
		t.Error("update description must name the replacement")
	}
}
