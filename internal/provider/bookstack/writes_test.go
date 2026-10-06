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
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// containerServer answers the reads that prove a binding and records every change. Page 1 and chapter 10
// belong to book 7, page 2 and chapter 20 to book 9.
type containerServer struct {
	*httptest.Server
	rec    *recorder
	mu     sync.Mutex
	bodies []map[string]any
}

func newContainerServer(t *testing.T) *containerServer {
	t.Helper()
	s := &containerServer{rec: &recorder{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			book := 7
			if strings.HasSuffix(r.URL.Path, "/2") || strings.HasSuffix(r.URL.Path, "/20") {
				book = 9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 5, "book_id": 7, "name": "Changed", "slug": "changed", "priority": 2, "default_template_id": 1,
			"tags": []map[string]any{{"name": "k", "value": "v"}}, "owned_by": testUser,
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *containerServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

func TestContainerWriteBodies(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		targets        []string
		want           map[string]any
		requests       []string
	}{
		{"create book", booksCreate.ID, `{"name":"B","description":"d","tags":[{"name":"k","value":"v"}],"default_template_id":1}`, nil,
			map[string]any{"name": "B", "description": "d", "default_template_id": 1.0,
				"tags": []any{map[string]any{"name": "k", "value": "v"}}},
			[]string{"POST /api/books"}},
		{"create book html unescaped", booksCreate.ID, `{"name":"B","description_html":"<p>a & b</p>"}`, nil,
			map[string]any{"name": "B", "description_html": "<p>a & b</p>"}, []string{"POST /api/books"}},
		{"update book clears template", booksUpdate.ID, `{"id":7,"default_template_id":null}`, nil,
			map[string]any{"default_template_id": nil}, []string{"PUT /api/books/7"}},
		{"update book clears tags", booksUpdate.ID, `{"id":7,"tags":[]}`, []string{"book/7"},
			map[string]any{"tags": []any{}}, []string{"PUT /api/books/7"}},
		{"update book own template", booksUpdate.ID, `{"id":7,"default_template_id":1}`, []string{"book/7"},
			map[string]any{"default_template_id": 1.0}, []string{"GET /api/pages/1", "PUT /api/books/7"}},
		{"update book template unbound adds no read", booksUpdate.ID, `{"id":7,"default_template_id":2}`, nil,
			map[string]any{"default_template_id": 2.0}, []string{"PUT /api/books/7"}},
		{"create chapter", chaptersCreate.ID, `{"book_id":7,"name":"C","priority":0,"description":"d"}`, []string{"book/7"},
			map[string]any{"book_id": 7.0, "name": "C", "priority": 0.0, "description": "d"}, []string{"POST /api/chapters"}},
		{"create chapter own template", chaptersCreate.ID, `{"book_id":7,"name":"C","default_template_id":1}`, []string{"book/7"},
			map[string]any{"book_id": 7.0, "name": "C", "default_template_id": 1.0}, []string{"GET /api/pages/1", "POST /api/chapters"}},
		{"update chapter", chaptersUpdate.ID, `{"id":10,"name":"N","priority":4}`, []string{"book/7"},
			map[string]any{"name": "N", "priority": 4.0}, []string{"GET /api/chapters/10", "PUT /api/chapters/10"}},
		{"move chapter", chaptersUpdate.ID, `{"id":10,"book_id":7}`, []string{"book/7", "book/9"},
			map[string]any{"book_id": 7.0}, []string{"GET /api/chapters/10", "PUT /api/chapters/10"}},
		{"update chapter clears template", chaptersUpdate.ID, `{"id":10,"default_template_id":null}`, []string{"book/7"},
			map[string]any{"default_template_id": nil}, []string{"GET /api/chapters/10", "PUT /api/chapters/10"}},
		{"update chapter unbound reads nothing", chaptersUpdate.ID, `{"id":20,"book_id":9,"description_html":"<b>x</b>"}`, nil,
			map[string]any{"book_id": 9.0, "description_html": "<b>x</b>"}, []string{"PUT /api/chapters/20"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newContainerServer(t)
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			result, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL, tt.targets...), resolver(nil), nil, json.RawMessage(tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Error("no result")
			}
			if len(server.bodies) != 1 || !reflect.DeepEqual(server.bodies[0], tt.want) {
				t.Errorf("body = %v, want %v", server.bodies, tt.want)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.requests) {
				t.Errorf("requests = %v, want %v", got, tt.requests)
			}
		})
	}
}

func TestContainerWriteIsRefusedLocallyBeforeSecretsAndIO(t *testing.T) {
	server := newContainerServer(t)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	tags := "[" + strings.TrimSuffix(strings.Repeat(`{"name":"k"},`, 51), ",") + "]"
	for _, tt := range []struct {
		name, op, args string
		targets        []string
	}{
		{"create book with targets", booksCreate.ID, `{"name":"B"}`, []string{"book/7"}},
		{"create book empty name", booksCreate.ID, `{"name":""}`, nil},
		{"create book long name", booksCreate.ID, `{"name":"` + strings.Repeat("é", 256) + `"}`, nil},
		{"create book both descriptions", booksCreate.ID, `{"name":"B","description":"a","description_html":"b"}`, nil},
		{"create book long description", booksCreate.ID, `{"name":"B","description":"` + strings.Repeat("é", 1901) + `"}`, nil},
		{"create book long description html", booksCreate.ID, `{"name":"B","description_html":"` + strings.Repeat("é", 2001) + `"}`, nil},
		{"create book many tags", booksCreate.ID, `{"name":"B","tags":` + tags + `}`, nil},
		{"update book foreign", booksUpdate.ID, `{"id":9,"name":"N"}`, []string{"book/7"}},
		{"update book nothing", booksUpdate.ID, `{"id":7}`, nil},
		{"update book bad template", booksUpdate.ID, `{"id":7,"default_template_id":0}`, nil},
		{"create chapter foreign book", chaptersCreate.ID, `{"book_id":9,"name":"C"}`, []string{"book/7"}},
		{"create chapter long description", chaptersCreate.ID, `{"book_id":7,"name":"C","description":"` + strings.Repeat("a", 1901) + `"}`, []string{"book/7"}},
		{"update chapter foreign target book", chaptersUpdate.ID, `{"id":10,"book_id":9}`, []string{"book/7"}},
		{"update chapter nothing", chaptersUpdate.ID, `{"id":10}`, nil},
		{"update chapter both descriptions", chaptersUpdate.ID, `{"id":10,"description":"a","description_html":"b"}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL, tt.targets...), resolver(nil), nil, json.RawMessage(tt.args))
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

func TestContainerWriteIsBoundByEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		reads          []string
	}{
		{"foreign chapter", chaptersUpdate.ID, `{"id":20,"name":"N"}`, []string{"GET /api/chapters/20"}},
		{"foreign chapter move into own book", chaptersUpdate.ID, `{"id":20,"book_id":7}`, []string{"GET /api/chapters/20"}},
		{"own chapter foreign template", chaptersUpdate.ID, `{"id":10,"default_template_id":2}`, []string{"GET /api/chapters/10", "GET /api/pages/2"}},
		{"chapter create foreign template", chaptersCreate.ID, `{"book_id":7,"name":"C","default_template_id":2}`, []string{"GET /api/pages/2"}},
		{"book foreign template", booksUpdate.ID, `{"id":7,"default_template_id":2}`, []string{"GET /api/pages/2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newContainerServer(t)
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			_, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(tt.args))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
			if strings.Contains(err.Error(), "20") || strings.Contains(err.Error(), "9") {
				t.Errorf("err = %v names the foreign target", err)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.reads) {
				t.Errorf("requests = %v, want %v", got, tt.reads)
			}
		})
	}
}

func TestContainerWriteOutputIsReduced(t *testing.T) {
	server := newContainerServer(t)
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	result, err := lookup(t, chaptersUpdate.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(`{"id":5,"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "hidden") {
		t.Errorf("result leaks provider fields: %s", encoded)
	}
}

func TestContainerWritesReportUncertainty(t *testing.T) {
	const hint = "this change may have taken effect, read the current state in BookStack before repeating it"
	run := func(name string, handler http.HandlerFunc, timeout time.Duration, wantHint bool) {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				handler(w, r)
			}))
			defer server.Close()
			c := newClient(t, server.URL, nil)
			calls := []func(context.Context) error{
				func(ctx context.Context) error { _, err := c.CreateBook(ctx, containerMutation{Name: "n"}); return err },
				func(ctx context.Context) error {
					_, err := c.UpdateBook(ctx, 1, containerMutation{Name: "n"})
					return err
				},
				func(ctx context.Context) error {
					_, err := c.CreateChapter(ctx, containerMutation{Name: "n", BookID: 1})
					return err
				},
				func(ctx context.Context) error {
					_, err := c.UpdateChapter(ctx, 1, containerMutation{Name: "n"})
					return err
				},
			}
			for _, call := range calls {
				ctx := context.Background()
				if timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				if err := call(ctx); err == nil || strings.Contains(err.Error(), hint) != wantHint {
					t.Errorf("error = %v, hint wanted %v", err, wantHint)
				}
			}
			if methods, _, _, _ := rec.snapshot(); len(methods) != len(calls) {
				t.Errorf("requests = %d, want %d (one per call, no retry)", len(methods), len(calls))
			}
		})
	}
	run("5xx", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, 0, true)
	run("timeout", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}, 500*time.Millisecond, true)
	run("unreadable response", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }, 0, true)
	run("422 is certain", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(422) }, 0, false)
}

func TestContainerWriteRisk(t *testing.T) {
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		{booksCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{chaptersCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{booksUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
		{chaptersUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idempotency, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: "bookstack-content"}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v allowlist = %v", tt.d.ID, tt.d.Risk, tt.d.RequiresToolAllowList)
		}
	}
}
