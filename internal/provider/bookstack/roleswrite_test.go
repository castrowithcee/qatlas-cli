package bookstack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// roleServer serves roles; role 5 is the guest role, role 6 an ordinary role, role 1 the admin role.
type roleServer struct {
	*httptest.Server
	rec *recorder

	mu     sync.Mutex
	bodies []map[string]any

	listStatus int  // status of GET /api/roles, 0 for 200
	plainFirst bool // the first read of role 5 reports no system name, later reads report public
	fiveReads  int
	writeCode  int // status of the change request, 0 for success
}

func newRoleServer(t *testing.T) *roleServer {
	t.Helper()
	s := &roleServer{rec: &recorder{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		role := func(id int, name, system string) map[string]any {
			return map[string]any{"id": id, "display_name": name, "system_name": system, "mfa_enforced": false,
				"external_auth_id": "ext-x", "permissions": []string{"book-view-all"}, "users": []map[string]any{{"id": 99, "name": "Guest"}}}
		}
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
			s.mu.Lock()
			s.fiveReads++
			system := "public"
			if s.plainFirst && s.fiveReads == 1 {
				system = ""
			}
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(role(5, "Public", system))
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles/1":
			_ = json.NewEncoder(w).Encode(role(1, "Admin", "admin"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles/6":
			_ = json.NewEncoder(w).Encode(role(6, "Editors", ""))
		case r.Method == http.MethodGet && r.URL.Path == "/api/roles/7":
			_ = json.NewEncoder(w).Encode(role(8, "Other", ""))
		case r.Method == http.MethodPost || r.Method == http.MethodPut:
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
			_ = json.NewEncoder(w).Encode(role(6, "Editors", ""))
		case r.Method == http.MethodDelete:
			if s.writeCode != 0 {
				w.WriteHeader(s.writeCode)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *roleServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

func (s *roleServer) written() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func TestRolePermissionNamesAreFixed(t *testing.T) {
	want := []string{
		"access-api",
		"attachment-create-all", "attachment-delete-all", "attachment-delete-own", "attachment-update-all", "attachment-update-own",
		"book-create-all", "book-delete-all", "book-delete-own", "book-update-all", "book-update-own", "book-view-all", "book-view-own",
		"bookshelf-create-all", "bookshelf-delete-all", "bookshelf-delete-own", "bookshelf-update-all", "bookshelf-update-own", "bookshelf-view-all", "bookshelf-view-own",
		"chapter-create-all", "chapter-create-own", "chapter-delete-all", "chapter-delete-own", "chapter-update-all", "chapter-update-own", "chapter-view-all", "chapter-view-own",
		"comment-create-all", "comment-delete-all", "comment-delete-own", "comment-update-all", "comment-update-own",
		"content-export", "content-import", "editor-change",
		"image-create-all", "image-delete-all", "image-delete-own", "image-update-all", "image-update-own",
		"page-create-all", "page-create-own", "page-delete-all", "page-delete-own", "page-update-all", "page-update-own", "page-view-all", "page-view-own",
		"receive-notifications",
		"restrictions-manage-all", "restrictions-manage-own",
		"revision-view-all",
		"settings-manage", "templates-manage", "user-roles-manage", "users-manage",
	}
	if !reflect.DeepEqual(rolePermissionNames, want) || len(want) != 57 {
		t.Errorf("rolePermissionNames = %v", rolePermissionNames)
	}
	if !slices.IsSorted(rolePermissionNames) {
		t.Error("rolePermissionNames must stay sorted for the lookup")
	}
	var schema struct {
		Properties struct {
			Permissions struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"permissions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(rolesCreate.InputSchema, &schema); err != nil || !reflect.DeepEqual(schema.Properties.Permissions.Items.Enum, want) {
		t.Errorf("schema enum = %v (%v)", schema.Properties.Permissions.Items.Enum, err)
	}
}

func TestRoleWriteDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{rolesCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{rolesUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
		{rolesDelete, capability.EffectDelete, capability.IdempotencyIdempotent},
	} {
		d, _, ok := reg.Lookup(tt.d.ID)
		if !ok || d.Group != administrationGroup || !d.RequiresToolAllowList {
			t.Errorf("%s: registered %v, group %q, allow list %v", tt.d.ID, ok, d.Group, d.RequiresToolAllowList)
		}
		r := d.Risk
		if r.Effect != tt.effect || r.Idempotency != tt.idem || r.Confirmation != capability.ConfirmationRequired ||
			!r.OpenWorld || r.DataSensitivity != "bookstack-people" {
			t.Errorf("%s: risk = %+v", tt.d.ID, r)
		}
		for _, want := range []string{"full administrative control", "user-roles-manage", "tool allow list"} {
			if !strings.Contains(d.Description, want) {
				t.Errorf("%s: description lacks %q", tt.d.ID, want)
			}
		}
		if strings.Contains(string(d.InputSchema), "external_auth_id") {
			t.Errorf("%s: schema offers external_auth_id", tt.d.ID)
		}
		for _, a := range d.Arguments {
			if a.Name == "external_auth_id" {
				t.Errorf("%s: argument external_auth_id", tt.d.ID)
			}
		}
		for _, profile := range meta.Profiles {
			if slices.Contains(profile.Tools, tt.d.ID) {
				t.Errorf("profile %s contains %s", profile.ID, tt.d.ID)
			}
		}
	}
	for _, want := range []string{"cannot be undone", "lose it", "content permissions of the role are removed", "no migration", "registration role"} {
		if !strings.Contains(rolesDelete.Description, want) {
			t.Errorf("delete description lacks %q", want)
		}
	}
	if !strings.Contains(rolesUpdate.Description, "replaces the whole permission list") {
		t.Error("update description does not state the replacement")
	}
}

func TestRoleWriteRejectsLocallyBeforeSecretAndIO(t *testing.T) {
	server := newRoleServer(t)
	resolved := boundResolved(server.URL)
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	long := strings.Repeat("a", 181)
	for _, tt := range []struct{ tool, args string }{
		{"create", `{"display_name":"Editors","permissions":["book-view-all","made-up-permission"]}`},
		{"create", `{"display_name":"Editors","permissions":["book-view","page-view-all"]}`},
		{"create", `{"display_name":"Editors","permissions":["book-view-all","book-view-all"]}`},
		{"create", `{"display_name":"Editors","external_auth_id":"group-1"}`},
		{"create", `{}`},
		{"create", `{"display_name":"ab"}`},
		{"create", `{"display_name":"` + long + `"}`},
		{"create", `{"display_name":"Editors","description":"` + long + `"}`},
		{"update", `{"id":6,"permissions":["nope"]}`},
		{"update", `{"id":6,"external_auth_id":"group-1"}`},
		{"update", `{"id":6}`},
		{"update", `{"id":0,"description":"x"}`},
		{"update", `{"id":6,"display_name":"x"}`},
		{"delete", `{"id":0}`},
	} {
		if _, err := callPerm(t, Provider+".roles."+tt.tool, resolved, tt.args); !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v, want invalid request", tt.tool, tt.args, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestRoleWriteGate(t *testing.T) {
	server := newRoleServer(t)
	resolved := boundResolved(server.URL, "book/7")
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	for tool, args := range map[string]string{
		"create": `{"display_name":"Editors"}`, "update": `{"id":6,"description":"x"}`, "delete": `{"id":6}`,
	} {
		_, err := callPerm(t, Provider+".roles."+tool, resolved, args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "book/7") {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestRoleCreateBodyAndResult(t *testing.T) {
	server := newRoleServer(t)
	result, err := callPerm(t, rolesCreate.ID, boundResolved(server.URL),
		`{"display_name":"Editors","description":"Wiki editors","mfa_enforced":true,"permissions":["book-view-all","page-update-own"]}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"display_name": "Editors", "description": "Wiki editors", "mfa_enforced": true,
		"permissions": []any{"book-view-all", "page-update-own"}}
	if got := server.written(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("bodies = %v, want %v", got, want)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"POST /api/roles"}) {
		t.Errorf("requests = %v", got)
	}
	obj, ok := result.(output.Object)
	if !ok {
		t.Fatalf("result = %T", result)
	}
	names := []string{}
	for _, f := range obj.Fields {
		names = append(names, f.Name)
	}
	for _, need := range []string{"id", "display_name", "permissions", "users", "truncated"} {
		if !slices.Contains(names, need) {
			t.Errorf("result lacks %q: %v", need, names)
		}
	}
}

func TestRoleBodiesNeverCarryExternalAuthID(t *testing.T) {
	server := newRoleServer(t)
	for _, args := range []string{`{"display_name":"Editors"}`, `{"display_name":"Editors","permissions":[]}`} {
		if _, err := callPerm(t, rolesCreate.ID, boundResolved(server.URL), args); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := callPerm(t, rolesUpdate.ID, boundResolved(server.URL), `{"id":6,"description":"x","mfa_enforced":false}`); err != nil {
		t.Fatal(err)
	}
	bodies := server.written()
	if len(bodies) != 3 {
		t.Fatalf("bodies = %v", bodies)
	}
	for _, body := range bodies {
		if _, found := body["external_auth_id"]; found {
			t.Errorf("body = %v carries external_auth_id", body)
		}
	}
}

func TestRoleUpdateReplacesPermissionsAndLeavesOtherFieldsOut(t *testing.T) {
	server := newRoleServer(t)
	for _, tt := range []struct {
		args string
		want map[string]any
	}{
		{`{"id":6,"permissions":["page-view-all"]}`, map[string]any{"permissions": []any{"page-view-all"}}},
		{`{"id":6,"permissions":[]}`, map[string]any{"permissions": []any{}}},
		{`{"id":6,"display_name":"Renamed","mfa_enforced":false}`, map[string]any{"display_name": "Renamed", "mfa_enforced": false}},
		{`{"id":6,"description":""}`, map[string]any{"description": ""}},
	} {
		if _, err := callPerm(t, rolesUpdate.ID, boundResolved(server.URL), tt.args); err != nil {
			t.Fatalf("%s: %v", tt.args, err)
		}
		got := server.written()
		if last := got[len(got)-1]; !reflect.DeepEqual(last, tt.want) {
			t.Errorf("%s: body = %v, want %v", tt.args, last, tt.want)
		}
	}
	want := []string{"GET /api/roles/6", "GET /api/roles", "GET /api/roles/5", "PUT /api/roles/6"}
	if got := server.requests()[:4]; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestRoleUpdateRefusesGuestRole(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*roleServer)
		id    string
	}{
		{"by system name", nil, "5"},
		{"by guest role id", func(s *roleServer) { s.plainFirst = true }, "5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newRoleServer(t)
			if tt.setup != nil {
				tt.setup(server)
			}
			args := `{"id":` + tt.id + `,"permissions":["book-view-all"]}`
			_, err := callPerm(t, rolesUpdate.ID, boundResolved(server.URL), args)
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), "guest role") {
				t.Errorf("err = %v", err)
			}
			if got := server.written(); len(got) != 0 {
				t.Errorf("bodies = %v, want none", got)
			}
		})
	}
}

func TestRoleUpdateFailsClosedWhenGuestRoleIsUnknown(t *testing.T) {
	server := newRoleServer(t)
	server.listStatus = http.StatusForbidden
	_, err := callPerm(t, rolesUpdate.ID, boundResolved(server.URL), `{"id":6,"permissions":["book-view-all"]}`)
	if err == nil || strings.Contains(err.Error(), "secret provider text") || !strings.Contains(err.Error(), "guest role") {
		t.Errorf("err = %v", err)
	}
	if got := server.written(); len(got) != 0 {
		t.Errorf("bodies = %v, want none", got)
	}
	for _, r := range server.requests() {
		if strings.HasPrefix(r, "PUT") {
			t.Errorf("requests = %v", server.requests())
		}
	}
}

func TestRoleUpdateRefusesMismatchingRead(t *testing.T) {
	server := newRoleServer(t)
	if _, err := callPerm(t, rolesUpdate.ID, boundResolved(server.URL), `{"id":7,"description":"x"}`); err == nil {
		t.Error("a role answer for another id must be refused")
	}
	if got := server.written(); len(got) != 0 {
		t.Errorf("bodies = %v", got)
	}
}

func TestRoleDelete(t *testing.T) {
	server := newRoleServer(t)
	result, err := callPerm(t, rolesDelete.ID, boundResolved(server.URL), `{"id":6}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, map[string]bool{"deleted": true}) {
		t.Errorf("result = %v", result)
	}
	if got, want := server.requests(), []string{"GET /api/roles/6", "DELETE /api/roles/6"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestRoleDeleteRefusesSystemRoles(t *testing.T) {
	for _, id := range []string{"1", "5"} {
		server := newRoleServer(t)
		_, err := callPerm(t, rolesDelete.ID, boundResolved(server.URL), `{"id":`+id+`}`)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "system role") {
			t.Errorf("role %s: err = %v", id, err)
		}
		for _, r := range server.requests() {
			if strings.HasPrefix(r, "DELETE") {
				t.Errorf("role %s: requests = %v", id, server.requests())
			}
		}
	}
}

func TestRoleChangesAreNotRepeatedAndReportUncertainty(t *testing.T) {
	const hint = "this change may have taken effect"
	for _, tt := range []struct {
		tool, args, method string
	}{
		{"create", `{"display_name":"Editors"}`, "POST"},
		{"update", `{"id":6,"description":"x"}`, "PUT"},
		{"delete", `{"id":6}`, "DELETE"},
	} {
		for _, status := range []int{http.StatusBadGateway, http.StatusInternalServerError} {
			server := newRoleServer(t)
			server.writeCode = status
			_, err := callPerm(t, Provider+".roles."+tt.tool, boundResolved(server.URL), tt.args)
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
