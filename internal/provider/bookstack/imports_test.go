package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

const importSecretPath = "uploads/imports/secret-storage-path.zip"

type importServer struct {
	*httptest.Server
	*writeServer
	runStatus    int
	uploadStatus int
	importType   string
	details      string
}

// newImportServer answers import 5 with the given type; the run and upload status can be set.
func newImportServer(t *testing.T, importType string) *importServer {
	t.Helper()
	s := &importServer{writeServer: &writeServer{}, importType: importType}
	s.details = `{"name":"Handbook","chapters":[{"name":"Ch1","pages":[{"name":"P1","attachments":[{"name":"a"}],"tags":[{"name":"t"}]}]}],` +
		`"pages":[{"name":"P2","images":[{"name":"i"}]}],"attachments":[],"images":[],"tags":[{"name":"x"}]}`
	object := func(extra map[string]any) map[string]any {
		m := map[string]any{"id": 5, "name": "Handbook", "size": 1234, "type": s.importType, "path": importSecretPath,
			"created_by": 1, "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00Z"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/imports", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{object(nil)}, "total": 1})
			return
		}
		if s.uploadStatus != 0 {
			s.writeServer.change(httptest.NewRecorder(), r)
			w.WriteHeader(s.uploadStatus)
			_, _ = w.Write([]byte(`{"error":{"message":"SECRET provider text","validation":{"file":["SECRET zip text"]}}}`))
			return
		}
		s.writeServer.change(w, r)
	})
	mux.HandleFunc("/api/imports/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		switch r.Method {
		case http.MethodGet:
			var details any
			_ = json.Unmarshal([]byte(s.details), &details)
			_ = json.NewEncoder(w).Encode(object(map[string]any{"details": details}))
		case http.MethodPost:
			got := sentChange{Method: r.Method, Path: r.URL.Path}
			_ = json.NewDecoder(r.Body).Decode(&got.JSON)
			s.mu.Lock()
			s.changes = append(s.changes, got)
			s.mu.Unlock()
			if s.runStatus != 0 {
				w.WriteHeader(s.runStatus)
				_, _ = w.Write([]byte(`{"error":{"message":"SECRET import error list","errors":["SECRET one","SECRET two"]}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "name": "New", "slug": "new", "book_id": 3})
		case http.MethodDelete:
			s.change(w, r)
		}
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *importServer) requests() []string { return s.writeServer.requests() }

func zipFile(t *testing.T, name string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("PK\x03\x04 zip \x00 content"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestImportsDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	all := []capability.Descriptor{importsList, importsGet, importsUpload, importsRun, importsDelete}
	for _, d := range all {
		if !strings.Contains(d.Description, "content-import") || !strings.Contains(d.Description, "new book") {
			t.Errorf("%s description lacks the role or the new book note", d.ID)
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, d.ID)
				}
			}
		}
	}
	for _, d := range reg.Provider(Provider) {
		if strings.HasPrefix(d.ID, Provider+".imports.") && (d.Group != "imports" || d.RequiresToolAllowList != (d.ID == importsDelete.ID)) {
			t.Errorf("%s: group %q allowlist %v", d.ID, d.Group, d.RequiresToolAllowList)
		}
	}
	if importsList.Risk != bookstackReadRisk || importsGet.Risk != bookstackReadRisk {
		t.Error("list and get must be safe reads")
	}
	for _, d := range []capability.Descriptor{importsUpload, importsRun} {
		if d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if importsDelete.Risk.Effect != capability.EffectDelete || importsDelete.Risk.Confirmation != capability.ConfirmationRequired {
		t.Errorf("delete risk = %+v", importsDelete.Risk)
	}
	if importsUpload.LocalFiles != config.LocalFilesRead {
		t.Error("upload must read local files")
	}
}

func TestImportsAreRefusedOnABoundConnectionBeforeAnySecretOrRequest(t *testing.T) {
	server := newImportServer(t, "book")
	dir, path := zipFile(t, "x.zip")
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, tt := range []struct{ op, args string }{
		{importsList.ID, `{}`}, {importsGet.ID, `{"id":5}`}, {importsRun.ID, `{"id":5}`}, {importsDelete.ID, `{"id":5}`},
		{importsUpload.ID, jsonArgs(t, map[string]any{"local_path": path})},
	} {
		resolved := boundResolved(server.URL, "book/7")
		resolved.Files = config.Files{Read: []string{dir}}
		_, err := lookup(t, tt.op)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(tt.args))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "7") {
			t.Errorf("%s: err = %v", tt.op, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestImportsListAndGetShowNoPathAndReduceTheDetails(t *testing.T) {
	server := newImportServer(t, "book")
	got, err := writeCall(t, importsList.ID, server.URL, `{}`, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := got.(output.Collection).Rows
	want := map[string]any{"id": int64(5), "name": "Handbook", "size": int64(1234), "type": "book", "created_by": int64(1),
		"created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00Z"}
	if len(rows) != 1 || !reflect.DeepEqual(map[string]any(rows[0]), want) {
		t.Errorf("rows = %v", rows)
	}

	object, err := writeCall(t, importsGet.ID, server.URL, `{"id":5}`, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(object)
	if strings.Contains(string(encoded), "secret-storage-path") || strings.Contains(string(encoded), `"path"`) {
		t.Errorf("the storage path is shown: %s", encoded)
	}
	var details map[string]any
	for _, f := range object.(output.Object).Fields {
		if f.Name == "details" {
			details = f.Value.(map[string]any)
		}
	}
	wantDetails := map[string]any{"name": "Handbook", "chapters": 1, "pages": 2, "attachments": 1, "images": 0, "tags": 2,
		"chapter_names": []string{"Ch1"}}
	// The nested image sits on a page below the root, so it is counted: one image in total.
	wantDetails["images"] = 1
	if !reflect.DeepEqual(details, wantDetails) {
		t.Errorf("details = %v, want %v", details, wantDetails)
	}
}

func TestImportDetailsAreBounded(t *testing.T) {
	var chapters []string
	for i := 0; i < 100; i++ {
		chapters = append(chapters, `{"name":"`+strings.Repeat("n", 5000)+`"}`)
	}
	raw := `{"name":"` + strings.Repeat("x", 5000) + `","chapters":[` + strings.Join(chapters, ",") + `]}`
	got := reduceImportDetails(json.RawMessage(raw))
	names := got["chapter_names"].([]string)
	if len(names) != maxImportDetailNames || len(names[0]) > maxResultString+10 || len(got["name"].(string)) > maxResultString+10 {
		t.Errorf("names = %d, first %d, name %d", len(names), len(names[0]), len(got["name"].(string)))
	}
	if got["chapters"] != 100 {
		t.Errorf("chapters = %v", got["chapters"])
	}
	// Deep nesting stops at the depth limit instead of recursing without bound.
	deep := strings.Repeat(`{"pages":[`, 200) + `{}` + strings.Repeat(`]}`, 200)
	if reduceImportDetails(json.RawMessage(deep)) == nil {
		t.Error("deep details must still be reduced")
	}
	if reduceImportDetails(json.RawMessage(`"text"`)) != nil || reduceImportDetails(nil) != nil {
		t.Error("unreadable details must be omitted")
	}
}

func TestImportsUploadSendsMultipartFileFieldAndChecksBeforeIO(t *testing.T) {
	dir, path := zipFile(t, "export.ZIP")
	server := newImportServer(t, "book")
	result, err := writeCall(t, importsUpload.ID, server.URL, jsonArgs(t, map[string]any{"local_path": path}), dir)
	if err != nil {
		t.Fatal(err)
	}
	sent := server.sent()
	if len(sent) != 1 || sent[0].Path != "/api/imports" || !strings.HasPrefix(sent[0].ContentType, "multipart/form-data; boundary=") ||
		sent[0].FileName != "export.ZIP" || sent[0].FileContent != "PK\x03\x04 zip \x00 content" || len(sent[0].Fields) != 0 {
		t.Errorf("sent = %+v", sent)
	}
	if sent[0].ContentLength <= 0 || sent[0].ContentLength != sent[0].BodyLength {
		t.Errorf("content length = %d, body = %d", sent[0].ContentLength, sent[0].BodyLength)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret-storage-path") {
		t.Errorf("the storage path is shown: %s", encoded)
	}

	// A wrong extension is refused before any secret, file access, or request.
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	server = newImportServer(t, "book")
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	resolved := boundResolved(server.URL)
	for _, p := range []string{other, filepath.Join(dir, "missing")} {
		_, err = lookup(t, importsUpload.ID)(context.Background(), resolved, resolver(nil), nil,
			json.RawMessage(jsonArgs(t, map[string]any{"local_path": p})))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), ".zip") {
			t.Errorf("%s: err = %v", p, err)
		}
	}

	// A zip one byte over the limit is refused before any secret or request.
	big := filepath.Join(dir, "big.zip")
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxUploadBytes + 1); err != nil {
		t.Skip("sparse files are not supported here")
	}
	file.Close()
	resolved.Files = config.Files{Read: []string{dir}}
	_, err = lookup(t, importsUpload.ID)(context.Background(), resolved, resolver(nil), nil,
		json.RawMessage(jsonArgs(t, map[string]any{"local_path": big})))
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "50 MiB") {
		t.Errorf("big: err = %v", err)
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestImportsUploadRejectionCarriesNoProviderText(t *testing.T) {
	dir, path := zipFile(t, "export.zip")
	server := newImportServer(t, "book")
	server.uploadStatus = http.StatusUnprocessableEntity
	_, err := writeCall(t, importsUpload.ID, server.URL, jsonArgs(t, map[string]any{"local_path": path}), dir)
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "imports.get") ||
		strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("err = %v", err)
	}
	if got := len(server.sent()); got != 1 {
		t.Errorf("upload requests = %d, want 1", got)
	}
}

func TestImportsRunChecksTheParentAgainstTheTypeBeforeTheRunRequest(t *testing.T) {
	for _, tt := range []struct {
		importType, args string
		wantBody         map[string]any
		refused          bool
	}{
		{"book", `{"id":5}`, nil, false},
		{"book", `{"id":5,"parent_type":"book","parent_id":1}`, nil, true},
		{"book", `{"id":5,"parent_id":1}`, nil, true},
		{"chapter", `{"id":5,"parent_type":"book","parent_id":3}`, map[string]any{"parent_type": "book", "parent_id": float64(3)}, false},
		{"chapter", `{"id":5,"parent_type":"chapter","parent_id":3}`, nil, true},
		{"chapter", `{"id":5,"parent_type":"book"}`, nil, true},
		{"chapter", `{"id":5}`, nil, true},
		{"page", `{"id":5,"parent_type":"chapter","parent_id":4}`, map[string]any{"parent_type": "chapter", "parent_id": float64(4)}, false},
		{"page", `{"id":5,"parent_type":"book","parent_id":4}`, map[string]any{"parent_type": "book", "parent_id": float64(4)}, false},
		{"page", `{"id":5,"parent_id":4}`, nil, true},
		{"page", `{"id":5,"parent_type":"chapter"}`, nil, true},
		{"page", `{"id":5}`, nil, true},
		{"zip", `{"id":5}`, nil, true},
	} {
		t.Run(tt.importType+" "+tt.args, func(t *testing.T) {
			server := newImportServer(t, tt.importType)
			result, err := writeCall(t, importsRun.ID, server.URL, tt.args, "")
			if tt.refused {
				if !isInvalidRequest(err) {
					t.Errorf("err = %v, want invalid-request", err)
				}
				if want := []string{"GET /api/imports/5"}; !reflect.DeepEqual(server.requests(), want) {
					t.Errorf("requests = %v, want only the read", server.requests())
				}
				if len(server.sent()) != 0 {
					t.Errorf("run request sent: %v", server.sent())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"GET /api/imports/5", "POST /api/imports/5"}; !reflect.DeepEqual(server.requests(), want) {
				t.Errorf("requests = %v, want %v", server.requests(), want)
			}
			if sent := server.sent(); len(sent) != 1 || !reflect.DeepEqual(sent[0].JSON, tt.wantBody) {
				t.Errorf("sent = %+v, want body %v", sent, tt.wantBody)
			}
			wantFields := []output.Field{{Name: "type", Value: tt.importType}, {Name: "id", Value: int64(77)}}
			if got := result.(output.Object).Fields; !reflect.DeepEqual(got, wantFields) {
				t.Errorf("result = %v, want only type and id %v", got, wantFields)
			}
		})
	}
}

func TestImportsRunDoesNotRepeatAndReportsNoProviderErrors(t *testing.T) {
	const hint = "this change may have taken effect"
	for status, wantHint := range map[int]bool{http.StatusBadGateway: true, http.StatusInternalServerError: true,
		http.StatusUnprocessableEntity: false} {
		server := newImportServer(t, "book")
		server.runStatus = status
		_, err := writeCall(t, importsRun.ID, server.URL, `{"id":5}`, "")
		if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), hint) != wantHint {
			t.Errorf("status %d: err = %v, hint wanted %v", status, err, wantHint)
		}
		if got := len(server.sent()); got != 1 {
			t.Errorf("status %d: run requests = %d, want exactly 1", status, got)
		}
	}
}

func TestImportsDeleteSendsOneDelete(t *testing.T) {
	server := newImportServer(t, "book")
	result, err := writeCall(t, importsDelete.ID, server.URL, `{"id":5}`, "")
	if err != nil || !reflect.DeepEqual(result, map[string]bool{"deleted": true}) {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	if want := []string{"DELETE /api/imports/5"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
}
