package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

const guestID = 5

// permServer serves roles and content permissions and records every PUT body. Role 5 is the guest role with
// the guest user 99; book 7, chapter 10, and page 1 belong to the bound book 7, chapter 20 and page 2 do not.
type permServer struct {
	*httptest.Server
	rec *recorder

	mu     sync.Mutex
	bodies []map[string]any

	rolesStatus  int    // status of GET /api/roles, 0 for 200
	detailStatus int    // status of GET /api/roles/5, 0 for 200
	publicName   string // system_name of role 5 in the listing; empty for "public"
	detailName   string // system_name of role 5 in the detail; empty for "public"
	noPublic     bool
	current      map[string]any
	putStatus    int
}

func defaultCurrent() map[string]any {
	return map[string]any{
		"owner": map[string]any{"id": 3, "name": "Ann", "slug": "ann", "hidden": "x"},
		"role_permissions": []map[string]any{
			{"role_id": 4, "view": true, "create": false, "update": true, "delete": false, "role": map[string]any{"id": 4, "display_name": "Editors"}},
		},
		"fallback_permissions": map[string]any{"inheriting": true, "view": nil, "create": nil, "update": nil, "delete": nil},
	}
}

func guestEntry(view, create, update, del bool) map[string]any {
	return map[string]any{"role_id": guestID, "view": view, "create": create, "update": update, "delete": del,
		"role": map[string]any{"id": guestID, "display_name": "Public"}}
}

func newPermServer(t *testing.T) *permServer {
	t.Helper()
	s := &permServer{rec: &recorder{}, current: defaultCurrent()}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		name := func(override string) string {
			if override != "" {
				return override
			}
			return "public"
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles":
			if s.rolesStatus != 0 {
				w.WriteHeader(s.rolesStatus)
				_, _ = io.WriteString(w, `{"error":{"message":"secret provider text"}}`)
				return
			}
			data := []map[string]any{{"id": 1, "display_name": "Admin", "system_name": "admin"}, {"id": 4, "display_name": "Editors", "system_name": ""}}
			if !s.noPublic {
				data = append(data, map[string]any{"id": guestID, "display_name": "Public", "system_name": name(s.publicName)})
			}
			total := len(data)
			if s.noPublic {
				total = 101
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": total})
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles/5":
			if s.detailStatus != 0 {
				w.WriteHeader(s.detailStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": guestID, "display_name": "Public", "system_name": name(s.detailName),
				"users": []map[string]any{{"id": 99, "name": "Guest", "slug": "guest"}}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/pages/"):
			book := 7
			if strings.HasSuffix(r.URL.Path, "/2") {
				book = 9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/chapters/"):
			book := 7
			if strings.HasSuffix(r.URL.Path, "/20") {
				book = 9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 10, "book_id": book})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/content-permissions/"):
			_ = json.NewEncoder(w).Encode(s.current)
		case r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(data, &body)
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			if s.putStatus != 0 {
				w.WriteHeader(s.putStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(defaultCurrent())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *permServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

func (s *permServer) puts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func callPerm(t *testing.T, id string, resolved *config.Resolved, args string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	return lookup(t, id)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(args))
}

func TestContentPermissionsDescriptors(t *testing.T) {
	if contentPermissionsGet.Risk.Effect != "read" || contentPermissionsGet.Risk.DataSensitivity != "bookstack-people" ||
		contentPermissionsGet.RequiresToolAllowList {
		t.Errorf("get = %+v", contentPermissionsGet)
	}
	risk := contentPermissionsUpdate.Risk
	if risk.Effect != "update" || risk.Idempotency != "idempotent" || risk.Confirmation != "required" || !risk.OpenWorld ||
		risk.DataSensitivity != "bookstack-people" || !contentPermissionsUpdate.RequiresToolAllowList {
		t.Errorf("update = %+v", contentPermissionsUpdate)
	}
	for _, want := range []string{"left out stays unchanged", "empty list removes all", "guest role"} {
		if !strings.Contains(contentPermissionsUpdate.Description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{contentPermissionsGet.ID, contentPermissionsUpdate.ID} {
		d, _, ok := reg.Lookup(id)
		if !ok || d.Group != administrationGroup {
			t.Errorf("%s is not registered in the administration group", id)
		}
	}
}

func TestContentPermissionsUpdateBodies(t *testing.T) {
	editors := `{"role_id":4,"view":true,"create":false,"update":false,"delete":false}`
	for _, tt := range []struct {
		name, args string
		want       map[string]any
	}{
		{"owner only", `{"type":"book","id":7,"owner_id":3}`, map[string]any{"owner_id": 3.0}},
		{"role list set", `{"type":"book","id":7,"role_permissions":[` + editors + `]}`,
			map[string]any{"role_permissions": []any{map[string]any{"role_id": 4.0, "view": true, "create": false, "update": false, "delete": false}}}},
		{"role list empty", `{"type":"book","id":7,"role_permissions":[]}`, map[string]any{}},
		{"fallback inheriting", `{"type":"book","id":7,"fallback_permissions":{"inheriting":true}}`,
			map[string]any{"fallback_permissions": map[string]any{"inheriting": true}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newPermServer(t)
			server.current["role_permissions"] = []map[string]any{}
			if tt.name == "fallback inheriting" {
				server.current["fallback_permissions"] = map[string]any{"inheriting": false, "view": true, "create": false, "update": false, "delete": false}
				server.current["role_permissions"] = []map[string]any{guestEntry(false, false, false, false)}
				tt.args = strings.Replace(tt.args, `"fallback_permissions"`, `"role_permissions":[`+
					`{"role_id":5,"view":false,"create":false,"update":false,"delete":false}],"fallback_permissions"`, 1)
				tt.want = map[string]any{"fallback_permissions": map[string]any{"inheriting": true},
					"role_permissions": []any{map[string]any{"role_id": 5.0, "view": false, "create": false, "update": false, "delete": false}}}
			}
			if _, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL), tt.args); err != nil {
				t.Fatal(err)
			}
			bodies := server.puts()
			if len(bodies) != 1 {
				t.Fatalf("PUT bodies = %v", bodies)
			}
			if tt.name == "role list empty" {
				if got, ok := bodies[0]["role_permissions"].([]any); !ok || len(got) != 0 || len(bodies[0]) != 1 {
					t.Errorf("body = %v, want an empty role_permissions list only", bodies[0])
				}
				return
			}
			if !reflect.DeepEqual(bodies[0], tt.want) {
				t.Errorf("body = %v, want %v", bodies[0], tt.want)
			}
		})
	}
}

func TestContentPermissionsFallbackValuesAreSent(t *testing.T) {
	server := newPermServer(t)
	server.current["role_permissions"] = []map[string]any{guestEntry(false, false, false, false)}
	args := `{"type":"book","id":7,"fallback_permissions":{"inheriting":false,"view":true,"create":false,"update":false,"delete":false}}`
	if _, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL), args); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"fallback_permissions": map[string]any{"inheriting": false, "view": true, "create": false, "update": false, "delete": false}}
	if got := server.puts(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("bodies = %v, want %v", got, want)
	}
	if got, want := server.requests(), []string{"GET /api/roles", "GET /api/roles/5", "GET /api/content-permissions/book/7", "PUT /api/content-permissions/book/7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestContentPermissionsGuestProtection(t *testing.T) {
	allFalse := `{"role_id":5,"view":false,"create":false,"update":false,"delete":false}`
	for _, tt := range []struct {
		name    string
		current func(*permServer)
		args    string
		allowed bool
	}{
		{"a: guest gets view", nil, `{"type":"book","id":7,"role_permissions":[{"role_id":5,"view":true,"create":false,"update":false,"delete":false}]}`, false},
		{"a: guest gets delete", nil, `{"type":"book","id":7,"role_permissions":[{"role_id":5,"view":false,"create":false,"update":false,"delete":true}]}`, false},
		{"a: guest true changed", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(true, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[{"role_id":5,"view":true,"create":true,"update":false,"delete":false}]}`, false},
		{"a: guest true unchanged stays allowed", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(true, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[{"role_id":5,"view":true,"create":false,"update":false,"delete":false}]}`, true},
		{"guest set to all false is allowed", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(true, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[` + allFalse + `]}`, true},
		{"b: list drops guest entry", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(false, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[{"role_id":4,"view":true,"create":false,"update":false,"delete":false}]}`, false},
		{"b: empty list with guest entry", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(false, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[]}`, false},
		{"empty list without guest entry is allowed", nil, `{"type":"book","id":7,"role_permissions":[]}`, true},
		{"c: fallback change without guest entry", nil,
			`{"type":"book","id":7,"fallback_permissions":{"inheriting":false,"view":true,"create":false,"update":false,"delete":false}}`, false},
		{"c: fallback change with guest entry that allows", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(true, false, false, false)}
		},
			`{"type":"book","id":7,"fallback_permissions":{"inheriting":false,"view":true,"create":false,"update":false,"delete":false}}`, false},
		{"c: fallback change with request guest all false is allowed", nil,
			`{"type":"book","id":7,"role_permissions":[` + allFalse + `],"fallback_permissions":{"inheriting":false,"view":true,"create":false,"update":false,"delete":false}}`, true},
		{"c: request list drops guest entry", func(s *permServer) {
			s.current["role_permissions"] = []map[string]any{guestEntry(false, false, false, false)}
		},
			`{"type":"book","id":7,"role_permissions":[],"fallback_permissions":{"inheriting":false,"view":true,"create":false,"update":false,"delete":false}}`, false},
		{"unchanged fallback needs no guest entry", nil, `{"type":"book","id":7,"fallback_permissions":{"inheriting":true}}`, true},
		{"d: owner is a guest user", nil, `{"type":"book","id":7,"owner_id":99}`, false},
		{"owner is a normal user", nil, `{"type":"book","id":7,"owner_id":98}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newPermServer(t)
			if tt.current != nil {
				tt.current(server)
			}
			_, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL), tt.args)
			if tt.allowed {
				if err != nil || len(server.puts()) != 1 {
					t.Fatalf("err = %v, puts = %d, want one PUT", err, len(server.puts()))
				}
				return
			}
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
			if len(server.puts()) != 0 {
				t.Errorf("PUT bodies = %v, want none", server.puts())
			}
			if strings.Contains(err.Error(), "99") || strings.Contains(err.Error(), "secret provider text") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestContentPermissionsGuestRoleUndeterminedRefusesClosed(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*permServer)
	}{
		{"roles forbidden", func(s *permServer) { s.rolesStatus = http.StatusForbidden }},
		{"roles unauthorized", func(s *permServer) { s.rolesStatus = http.StatusUnauthorized }},
		{"roles server error", func(s *permServer) { s.rolesStatus = http.StatusInternalServerError }},
		{"no public role", func(s *permServer) { s.noPublic = true }},
		{"listing name differs", func(s *permServer) { s.publicName = "other" }},
		{"detail name differs", func(s *permServer) { s.detailName = "other" }},
		{"detail forbidden", func(s *permServer) { s.detailStatus = http.StatusForbidden }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newPermServer(t)
			tt.setup(server)
			_, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL), `{"type":"book","id":7,"role_permissions":[]}`)
			if err == nil || !strings.Contains(err.Error(), "guest role") || strings.Contains(err.Error(), "secret provider text") {
				t.Fatalf("err = %v, want the guest role message", err)
			}
			if len(server.puts()) != 0 {
				t.Errorf("PUT bodies = %v, want none", server.puts())
			}
			for _, r := range server.requests() {
				if strings.Contains(r, "content-permissions") {
					t.Errorf("request %s was sent after the guest role failed", r)
				}
			}
		})
	}
}

func TestContentPermissionsLocalValidationBeforeSecretsAndIO(t *testing.T) {
	server := newPermServer(t)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	var many []string
	for i := 1; i <= 101; i++ {
		many = append(many, fmt.Sprintf(`{"role_id":%d,"view":false,"create":false,"update":false,"delete":false}`, i))
	}
	entry := `{"role_id":4,"view":true,"create":false,"update":false,"delete":false}`
	for _, tt := range []struct{ name, args string }{
		{"nothing to change", `{"type":"book","id":7}`},
		{"owner zero", `{"type":"book","id":7,"owner_id":0}`},
		{"duplicate role", `{"type":"book","id":7,"role_permissions":[` + entry + `,` + entry + `]}`},
		{"role zero", `{"type":"book","id":7,"role_permissions":[{"role_id":0,"view":true,"create":false,"update":false,"delete":false}]}`},
		{"missing value", `{"type":"book","id":7,"role_permissions":[{"role_id":4,"view":true}]}`},
		{"too many roles", `{"type":"book","id":7,"role_permissions":[` + strings.Join(many, ",") + `]}`},
		{"fallback without inheriting", `{"type":"book","id":7,"fallback_permissions":{}}`},
		{"fallback false without values", `{"type":"book","id":7,"fallback_permissions":{"inheriting":false,"view":true}}`},
		{"fallback inheriting with values", `{"type":"book","id":7,"fallback_permissions":{"inheriting":true,"view":false}}`},
		{"bad type", `{"type":"user","id":7,"owner_id":3}`},
		{"foreign book", `{"type":"book","id":9,"owner_id":3}`},
		{"shelf on bound connection", `{"type":"bookshelf","id":2,"owner_id":3}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookup(t, contentPermissionsUpdate.ID)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(tt.args))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
			if strings.Contains(err.Error(), "9") && strings.Contains(tt.name, "foreign") {
				t.Errorf("err = %v names the foreign book", err)
			}
		})
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestContentPermissionsBinding(t *testing.T) {
	for _, tt := range []struct {
		name, args string
		targets    []string
		want       []string
		foreign    bool
	}{
		{"own chapter", `{"type":"chapter","id":10,"owner_id":3}`, []string{"book/7"},
			[]string{"GET /api/chapters/10", "GET /api/roles", "GET /api/roles/5", "GET /api/content-permissions/chapter/10", "PUT /api/content-permissions/chapter/10"}, false},
		{"foreign chapter", `{"type":"chapter","id":20,"owner_id":3}`, []string{"book/7"}, []string{"GET /api/chapters/20"}, true},
		{"own page", `{"type":"page","id":1,"owner_id":3}`, []string{"book/7"},
			[]string{"GET /api/pages/1", "GET /api/roles", "GET /api/roles/5", "GET /api/content-permissions/page/1", "PUT /api/content-permissions/page/1"}, false},
		{"foreign page", `{"type":"page","id":2,"owner_id":3}`, []string{"book/7"}, []string{"GET /api/pages/2"}, true},
		{"unbound page needs no proof", `{"type":"page","id":2,"owner_id":3}`, nil,
			[]string{"GET /api/roles", "GET /api/roles/5", "GET /api/content-permissions/page/2", "PUT /api/content-permissions/page/2"}, false},
		{"shelf without targets", `{"type":"bookshelf","id":2,"owner_id":3}`, nil,
			[]string{"GET /api/roles", "GET /api/roles/5", "GET /api/content-permissions/bookshelf/2", "PUT /api/content-permissions/bookshelf/2"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newPermServer(t)
			_, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL, tt.targets...), tt.args)
			if tt.foreign != isInvalidRequest(err) || (!tt.foreign && err != nil) {
				t.Fatalf("err = %v", err)
			}
			if tt.foreign && (strings.Contains(err.Error(), "20") || strings.Contains(err.Error(), "9")) {
				t.Errorf("err = %v names the foreign target", err)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("requests = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContentPermissionsGet(t *testing.T) {
	server := newPermServer(t)
	server.current["fallback_permissions"] = map[string]any{"inheriting": false, "view": true, "create": false, "update": false, "delete": false}
	result, err := callPerm(t, contentPermissionsGet.ID, boundResolved(server.URL), `{"type":"book","id":7}`)
	if err != nil {
		t.Fatal(err)
	}
	object := result.(output.Object)
	encoded, _ := json.Marshal(object)
	for _, want := range []string{`"Ann"`, `"Editors"`, `"role_id":4`, `"inheriting":false`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("result %s lacks %s", encoded, want)
		}
	}
	if strings.Contains(string(encoded), "hidden") {
		t.Errorf("result = %s carries an unlisted field", encoded)
	}
	if got, want := server.requests(), []string{"GET /api/content-permissions/book/7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v (reading needs no guest lookup)", got, want)
	}
}

func TestContentPermissionsGetBinding(t *testing.T) {
	server := newPermServer(t)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, args := range []string{`{"type":"book","id":9}`, `{"type":"bookshelf","id":2}`} {
		_, err := lookup(t, contentPermissionsGet.ID)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(args))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
			t.Errorf("%s: err = %v", args, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
	if _, err := callPerm(t, contentPermissionsGet.ID, boundResolved(server.URL, "book/7"), `{"type":"page","id":2}`); !isInvalidRequest(err) {
		t.Errorf("foreign page: err = %v", err)
	}
	if got, want := server.requests(), []string{"GET /api/pages/2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestContentPermissionsUpdateIsSentOnceAndReportsUncertainty(t *testing.T) {
	server := newPermServer(t)
	server.putStatus = http.StatusInternalServerError
	_, err := callPerm(t, contentPermissionsUpdate.ID, boundResolved(server.URL), `{"type":"book","id":7,"owner_id":3}`)
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v, want the uncertainty hint", err)
	}
	if len(server.puts()) != 1 {
		t.Errorf("PUT count = %d, want 1", len(server.puts()))
	}
}

func TestContentPermissionsAreInNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, profile := range meta.Profiles {
		for _, id := range profile.Tools {
			if id == contentPermissionsGet.ID || id == contentPermissionsUpdate.ID {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

// permAreaServer adds the roles and content permissions to the area server: book 701 holds chapter 1001 with
// the pages 111 and 555 and the page 333, shelf 301 holds book 701.
func permAreaServer(t *testing.T) *areaServer {
	t.Helper()
	s := newAreaServer(t)
	inner := s.Config.Handler
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/roles":
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": guestID, "display_name": "Public", "system_name": "public"}}, "total": 1})
		case path == "/api/roles/5":
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": guestID, "system_name": "public", "users": []any{}})
		case strings.HasPrefix(path, "/api/content-permissions/"):
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(defaultCurrent())
		default:
			inner.ServeHTTP(w, r)
		}
	})
	return s
}

func TestContentPermissionsHonorExclusions(t *testing.T) {
	s := permAreaServer(t)
	update := func(_, args string, excludes ...string) error {
		t.Helper()
		s.requests()
		_, err := s.callExcluding(t, contentPermissionsUpdate.ID, args, nil, excludes)
		return err
	}
	puts := func() int {
		n := 0
		for _, r := range s.requests() {
			if strings.HasPrefix(r, "PUT ") {
				n++
			}
		}
		return n
	}
	book := `{"type":"book","id":701,"owner_id":3}`
	chapter := `{"type":"chapter","id":1001,"owner_id":3}`

	// Inheritance reaches the excluded chapter or page below, so the change is refused; a sibling is not.
	for what, tt := range map[string]struct{ args, exclude string }{
		"book with an excluded chapter": {book, "chapter/1001"},
		"book with an excluded page":    {book, "page/555"},
		"chapter with an excluded page": {chapter, "page/555"},
	} {
		err := update(what, tt.args, tt.exclude)
		refused(t, what, err)
		noExclusionWord(t, what, err)
		if puts() != 0 {
			t.Errorf("%s: a change was sent", what)
		}
	}
	if err := update("book with an unrelated exclusion", book, "chapter/2001", "page/444"); err != nil || puts() != 1 {
		t.Errorf("unrelated exclusions: err = %v", err)
	}

	// A shelf passes nothing down, so its permissions stay changeable while the shelf itself is not excluded.
	shelf := `{"type":"bookshelf","id":301,"owner_id":3}`
	if err := update("shelf", shelf, "book/701", "chapter/1001", "page/555"); err != nil || puts() != 1 {
		t.Errorf("shelf with excluded content: err = %v", err)
	}

	// An excluded object is refused for reading and for changing, before any request.
	for what, tt := range map[string]struct{ args, exclude string }{
		"shelf":   {shelf, "shelf/301"},
		"book":    {book, "book/701"},
		"chapter": {chapter, "chapter/1001"},
		"page":    {`{"type":"page","id":555,"owner_id":3}`, "page/555"},
	} {
		s.requests()
		_, err := s.callExcluding(t, contentPermissionsUpdate.ID, tt.args, nil, []string{tt.exclude})
		refused(t, what+" update", err)
		noExclusionWord(t, what+" update", err)
		read := strings.Replace(tt.args, `,"owner_id":3`, "", 1)
		_, err = s.callExcluding(t, contentPermissionsGet.ID, read, nil, []string{tt.exclude})
		refused(t, what+" read", err)
		if got := s.requests(); len(got) != 0 {
			t.Errorf("%s: requests = %v, want none", what, got)
		}
	}
	// A chapter of an excluded book and a book on an excluded shelf are refused after their evidence is read.
	err := update("chapter in an excluded book", chapter, "book/701")
	refused(t, "chapter in an excluded book", err)
	err = update("book on an excluded shelf", book, "shelf/301")
	refused(t, "book on an excluded shelf", err)
	if puts() != 0 {
		t.Error("a change was sent")
	}
}
