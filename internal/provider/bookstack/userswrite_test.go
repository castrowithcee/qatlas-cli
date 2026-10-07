package bookstack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// userServer serves users and the guest role; role 5 is the guest role and user 99 its only member.
type userServer struct {
	*httptest.Server
	rec *recorder

	mu     sync.Mutex
	bodies []map[string]any

	listStatus int // status of GET /api/roles, 0 for 200
	writeCode  int // status of the change request, 0 for success
}

func newUserServer(t *testing.T) *userServer {
	t.Helper()
	s := &userServer{rec: &recorder{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles":
			if s.listStatus != 0 {
				w.WriteHeader(s.listStatus)
				_, _ = io.WriteString(w, `{"error":{"message":"secret provider text"}}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": 1, "system_name": "admin"}, {"id": 5, "system_name": "public"}}, "total": 2})
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles/5":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 5, "display_name": "Public", "system_name": "public",
				"users": []map[string]any{{"id": 99, "name": "Guest"}}})
		case r.Method == http.MethodPut || r.Method == http.MethodDelete:
			data, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(data, &body)
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			if s.writeCode != 0 {
				w.WriteHeader(s.writeCode)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 12, "name": "Ada", "slug": "ada", "email": "ada@example.com",
				"external_auth_id": "ext-1", "profile_url": "https://wiki.example.com/user/ada",
				"avatar_url": "https://wiki.example.com/avatar", "edit_url": "https://wiki.example.com/edit",
				"roles": []map[string]any{{"id": 6, "display_name": "Editors"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *userServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

func (s *userServer) written() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func (s *userServer) changeRequests() int {
	count := 0
	for _, r := range s.requests() {
		if strings.HasPrefix(r, "PUT ") || strings.HasPrefix(r, "DELETE ") {
			count++
		}
	}
	return count
}

func TestUserWriteDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
	}{
		{usersUpdate, capability.EffectUpdate},
		{usersDelete, capability.EffectDelete},
	} {
		d, _, ok := reg.Lookup(tt.d.ID)
		if !ok || d.Group != administrationGroup || !d.RequiresToolAllowList {
			t.Errorf("%s: registered %v, group %q, allow list %v", tt.d.ID, ok, d.Group, d.RequiresToolAllowList)
		}
		r := d.Risk
		if r.Effect != tt.effect || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != "bookstack-people" {
			t.Errorf("%s: risk = %+v", tt.d.ID, r)
		}
		for _, want := range []string{"users-manage", "tool allow list", "creates users", "password"} {
			if !strings.Contains(d.Description, want) {
				t.Errorf("%s: description lacks %q", tt.d.ID, want)
			}
		}
		for _, forbidden := range []string{"email", "password", "external_auth_id", "send_invite"} {
			if strings.Contains(string(d.InputSchema), forbidden) {
				t.Errorf("%s: schema offers %s", tt.d.ID, forbidden)
			}
			for _, a := range d.Arguments {
				if a.Name == forbidden {
					t.Errorf("%s: argument %s", tt.d.ID, forbidden)
				}
			}
		}
		for _, profile := range meta.Profiles {
			if slices.Contains(profile.Tools, tt.d.ID) {
				t.Errorf("profile %s contains %s", profile.ID, tt.d.ID)
			}
		}
	}
	for _, want := range []string{"cannot be undone", "without an owner", "only administrator", "guest user"} {
		if !strings.Contains(usersDelete.Description, want) {
			t.Errorf("delete description lacks %q", want)
		}
	}
	if !strings.Contains(usersUpdate.Description, "replaces all roles") {
		t.Error("update description does not state the replacement")
	}
}

func TestUserWriteRejectsLocallyBeforeSecretAndIO(t *testing.T) {
	server := newUserServer(t)
	resolved := boundResolved(server.URL)
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	manyRoles := make([]string, 101)
	for i := range manyRoles {
		manyRoles[i] = strconv.Itoa(i + 1)
	}
	for _, tt := range []struct{ tool, args string }{
		{"update", `{"id":12,"email":"x@example.com"}`},
		{"update", `{"id":12,"password":"x"}`},
		{"update", `{"id":12,"external_auth_id":"x"}`},
		{"update", `{"id":12,"send_invite":true}`},
		{"update", `{"id":12}`},
		{"update", `{"id":0,"name":"Ada"}`},
		{"update", `{"id":12,"name":""}`},
		{"update", `{"id":12,"name":"` + strings.Repeat("a", 101) + `"}`},
		{"update", `{"id":12,"language":""}`},
		{"update", `{"id":12,"language":"` + strings.Repeat("a", 16) + `"}`},
		{"update", `{"id":12,"language":"de DE"}`},
		{"update", `{"id":12,"roles":[0]}`},
		{"update", `{"id":12,"roles":[-3]}`},
		{"update", `{"id":12,"roles":[6,6]}`},
		{"update", `{"id":12,"roles":[` + strings.Join(manyRoles, ",") + `]}`},
		{"delete", `{"id":0}`},
		{"delete", `{"id":12,"migrate_ownership_id":0}`},
		{"delete", `{"id":12,"migrate_ownership_id":12}`},
		{"delete", `{"id":12,"email":"x@example.com"}`},
	} {
		if _, err := callPerm(t, Provider+".users."+tt.tool, resolved, tt.args); !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v, want invalid request", tt.tool, tt.args, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestUserWriteGate(t *testing.T) {
	server := newUserServer(t)
	resolved := boundResolved(server.URL, "book/7")
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	for tool, args := range map[string]string{
		"update": `{"id":12,"name":"Ada"}`, "delete": `{"id":12,"migrate_ownership_id":13}`,
	} {
		_, err := callPerm(t, Provider+".users."+tool, resolved, args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "book/7") {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestUserUpdateWithoutRolesDoesNotLookUpRoles(t *testing.T) {
	server := newUserServer(t)
	result, err := callPerm(t, usersUpdate.ID, boundResolved(server.URL), `{"id":12,"name":"Ada","language":"de_DE"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := server.requests(), []string{"PUT /api/users/12"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	want := map[string]any{"name": "Ada", "language": "de_DE"}
	if got := server.written(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("bodies = %v, want %v", got, want)
	}
	obj, ok := result.(output.Object)
	if !ok {
		t.Fatalf("result = %T", result)
	}
	names := []string{}
	for _, f := range obj.Fields {
		names = append(names, f.Name)
	}
	for _, need := range []string{"id", "name", "email", "roles", "profile_url"} {
		if !slices.Contains(names, need) {
			t.Errorf("result lacks %q: %v", need, names)
		}
	}
	for _, banned := range []string{"avatar_url", "edit_url"} {
		if slices.Contains(names, banned) {
			t.Errorf("result carries %q", banned)
		}
	}
}

func TestUserUpdateReplacesRoles(t *testing.T) {
	server := newUserServer(t)
	for _, tt := range []struct {
		args string
		want map[string]any
	}{
		{`{"id":12,"roles":[6,7]}`, map[string]any{"roles": []any{float64(6), float64(7)}}},
		{`{"id":12,"roles":[]}`, map[string]any{"roles": []any{}}},
	} {
		if _, err := callPerm(t, usersUpdate.ID, boundResolved(server.URL), tt.args); err != nil {
			t.Fatalf("%s: %v", tt.args, err)
		}
		got := server.written()
		if last := got[len(got)-1]; !reflect.DeepEqual(last, tt.want) {
			t.Errorf("%s: body = %v, want %v", tt.args, last, tt.want)
		}
	}
	want := []string{"GET /api/roles", "GET /api/roles/5", "PUT /api/users/12"}
	if got := server.requests()[:3]; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	for _, body := range server.written() {
		for _, forbidden := range []string{"email", "password", "external_auth_id", "send_invite"} {
			if _, found := body[forbidden]; found {
				t.Errorf("body = %v carries %s", body, forbidden)
			}
		}
	}
}

func TestUserUpdateRefusesGuestUserAndGuestRole(t *testing.T) {
	for _, tt := range []struct{ name, args string }{
		{"guest user", `{"id":99,"roles":[6]}`},
		{"guest role", `{"id":12,"roles":[6,5]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newUserServer(t)
			_, err := callPerm(t, usersUpdate.ID, boundResolved(server.URL), tt.args)
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), "guest") {
				t.Errorf("err = %v", err)
			}
			if server.changeRequests() != 0 || len(server.written()) != 0 {
				t.Errorf("requests = %v, want no change", server.requests())
			}
		})
	}
}

func TestUserWritesFailClosedWhenGuestRoleIsUnknown(t *testing.T) {
	for _, tt := range []struct{ tool, args string }{
		{"update", `{"id":12,"roles":[6]}`},
		{"delete", `{"id":12,"migrate_ownership_id":13}`},
	} {
		server := newUserServer(t)
		server.listStatus = http.StatusForbidden
		_, err := callPerm(t, Provider+".users."+tt.tool, boundResolved(server.URL), tt.args)
		if err == nil || strings.Contains(err.Error(), "secret provider text") || !strings.Contains(err.Error(), "guest role") {
			t.Errorf("%s: err = %v", tt.tool, err)
		}
		if server.changeRequests() != 0 {
			t.Errorf("%s: requests = %v, want no change", tt.tool, server.requests())
		}
	}
}

func TestUserDelete(t *testing.T) {
	server := newUserServer(t)
	result, err := callPerm(t, usersDelete.ID, boundResolved(server.URL), `{"id":12}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, map[string]bool{"deleted": true}) {
		t.Errorf("result = %v", result)
	}
	if got, want := server.requests(), []string{"DELETE /api/users/12"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if got := server.written(); len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("bodies = %v, want an empty body", got)
	}
}

func TestUserDeleteMigratesOwnership(t *testing.T) {
	server := newUserServer(t)
	if _, err := callPerm(t, usersDelete.ID, boundResolved(server.URL), `{"id":12,"migrate_ownership_id":13}`); err != nil {
		t.Fatal(err)
	}
	if got, want := server.requests(), []string{"GET /api/roles", "GET /api/roles/5", "DELETE /api/users/12"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	want := map[string]any{"migrate_ownership_id": float64(13)}
	if got := server.written(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("bodies = %v, want %v", got, want)
	}
}

func TestUserDeleteRefusesMigrationToGuestUser(t *testing.T) {
	server := newUserServer(t)
	_, err := callPerm(t, usersDelete.ID, boundResolved(server.URL), `{"id":12,"migrate_ownership_id":99}`)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "guest user") {
		t.Errorf("err = %v", err)
	}
	if server.changeRequests() != 0 {
		t.Errorf("requests = %v, want no DELETE", server.requests())
	}
}

func TestUserChangesAreNotRepeatedAndReportUncertainty(t *testing.T) {
	const hint = "this change may have taken effect"
	for _, tt := range []struct {
		tool, args, method string
	}{
		{"update", `{"id":12,"name":"Ada"}`, "PUT"},
		{"delete", `{"id":12}`, "DELETE"},
		{"delete", `{"id":12,"migrate_ownership_id":13}`, "DELETE"},
	} {
		for _, status := range []int{http.StatusBadGateway, http.StatusInternalServerError} {
			server := newUserServer(t)
			server.writeCode = status
			_, err := callPerm(t, Provider+".users."+tt.tool, boundResolved(server.URL), tt.args)
			if err == nil || !strings.Contains(err.Error(), hint) {
				t.Errorf("%s %d: err = %v, want uncertainty hint", tt.tool, status, err)
			}
			count := 0
			for _, r := range server.requests() {
				if strings.HasPrefix(r, tt.method+" ") {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%s %d: %d change requests, want exactly 1", tt.tool, status, count)
			}
		}
	}
}
