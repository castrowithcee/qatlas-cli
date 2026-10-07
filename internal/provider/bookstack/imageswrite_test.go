package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

type imageWriteServer struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []string
	changes []sentChange
	status  int
}

func (s *imageWriteServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func (s *imageWriteServer) sent() []sentChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentChange(nil), s.changes...)
}

// newImageWriteServer holds pages 1 in book 7 and 2 in book 9; image 3 is a png gallery image on page 1,
// 4 a jpg on page 1, 5 a png on page 2.
func newImageWriteServer(t *testing.T) *imageWriteServer {
	t.Helper()
	s := &imageWriteServer{}
	rows := map[string]map[string]any{
		"/api/image-gallery/3": {"id": 3, "name": "a", "url": "https://bs.example.com/uploads/a.png", "type": "gallery", "uploaded_to": 1},
		"/api/image-gallery/4": {"id": 4, "name": "b", "url": "https://bs.example.com/uploads/b.jpg", "type": "gallery", "uploaded_to": 1},
		"/api/image-gallery/5": {"id": 5, "name": "c", "url": "https://bs.example.com/uploads/c.png", "type": "gallery", "uploaded_to": 2},
	}
	record := func(r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		books := map[string]int{"/api/pages/1": 7, "/api/pages/2": 9}
		if book, ok := books[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	change := func(w http.ResponseWriter, r *http.Request) {
		got := sentChange{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), ContentLength: r.ContentLength}
		media, params, _ := mime.ParseMediaType(got.ContentType)
		switch media {
		case "multipart/form-data":
			counter := &countingReader{r: r.Body}
			reader := multipart.NewReader(counter, params["boundary"])
			got.Fields = map[string]string{}
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "image" {
					got.FileName, got.FileContent = part.FileName(), string(data)
				} else {
					got.Fields[part.FormName()] = string(data)
				}
			}
			_, _ = io.Copy(io.Discard, counter)
			got.BodyLength = counter.n
		case "application/json":
			_ = json.NewDecoder(r.Body).Decode(&got.JSON)
		}
		s.mu.Lock()
		s.changes = append(s.changes, got)
		status := s.status
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "name": "stored", "url": "https://bs.example.com/uploads/n.png",
			"type": "gallery", "uploaded_to": 1, "created_by": 1, "updated_by": 1})
	}
	mux.HandleFunc("/api/image-gallery", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		change(w, r)
	})
	mux.HandleFunc("/api/image-gallery/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if row, ok := rows[r.URL.Path]; ok && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(row)
			return
		}
		if _, ok := rows[r.URL.Path]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		change(w, r)
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

func imageFile(t *testing.T, name, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestImageWriteToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
		files       config.LocalFiles
	}{
		{imagesUpload, capability.EffectCreate, capability.IdempotencyNonIdempotent, config.LocalFilesRead},
		{imagesUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent, ""},
		{imagesReplace, capability.EffectUpdate, capability.IdempotencyNonIdempotent, config.LocalFilesRead},
		{imagesDelete, capability.EffectDelete, capability.IdempotencyIdempotent, ""},
	} {
		desc, _, ok := reg.Lookup(tt.d.ID)
		if !ok {
			t.Fatalf("%s not registered", tt.d.ID)
		}
		risk := desc.Risk
		if desc.Group != "files" || risk.Effect != tt.effect || risk.Idempotency != tt.idempotency ||
			risk.Confirmation != capability.ConfirmationRequired || !risk.OpenWorld || risk.DataSensitivity == "" {
			t.Errorf("%s: group %q risk %+v", tt.d.ID, desc.Group, risk)
		}
		if desc.RequiresToolAllowList != (tt.d.ID == imagesDelete.ID) || desc.LocalFiles != tt.files {
			t.Errorf("%s: allow list %v, local files %q", tt.d.ID, desc.RequiresToolAllowList, desc.LocalFiles)
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == tt.d.ID {
					t.Errorf("%s is in profile %s", id, profile.ID)
				}
			}
		}
	}
	if !strings.Contains(imagesDelete.Description, "final, without a usage check") {
		t.Errorf("delete description = %q", imagesDelete.Description)
	}
}

func TestImagesUploadSendsMultipart(t *testing.T) {
	const content = "png-bytes"
	dir, path := imageFile(t, "pic.PNG", content)
	server := newImageWriteServer(t)
	args := jsonArgs(t, map[string]any{"page_id": 1, "type": "drawio", "name": "Diagram", "local_path": path})
	if _, err := writeCall(t, imagesUpload.ID, server.URL, args, dir, "book/7"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/pages/1", "POST /api/image-gallery"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
	sent := server.sent()[0]
	if want := map[string]string{"type": "drawio", "uploaded_to": "1", "name": "Diagram"}; !reflect.DeepEqual(sent.Fields, want) ||
		sent.FileContent != content || sent.FileName != "pic.PNG" || sent.ContentLength != sent.BodyLength {
		t.Errorf("sent = %+v", sent)
	}
	// A foreign page is refused without a change.
	server = newImageWriteServer(t)
	args = jsonArgs(t, map[string]any{"page_id": 2, "type": "gallery", "local_path": path})
	if _, err := writeCall(t, imagesUpload.ID, server.URL, args, dir, "book/7"); !isInvalidRequest(err) ||
		strings.Contains(err.Error(), "9") || len(server.sent()) != 0 {
		t.Errorf("foreign page: err = %v", err)
	}
}

func TestImagesUploadRefusesTypeAndNameBeforeSecretsAndIO(t *testing.T) {
	dir, png := imageFile(t, "a.png", "x")
	_, jpg := imageFile(t, "a.jpg", "x")
	_, txt := imageFile(t, "a.txt", "x")
	server := newImageWriteServer(t)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	resolved := boundResolved(server.URL)
	resolved.Files = config.Files{Read: []string{dir, filepath.Dir(jpg), filepath.Dir(txt)}}
	for name, args := range map[string]string{
		"drawio jpg": jsonArgs(t, map[string]any{"page_id": 1, "type": "drawio", "local_path": jpg}),
		"txt":        jsonArgs(t, map[string]any{"page_id": 1, "type": "gallery", "local_path": txt}),
		"bad type":   jsonArgs(t, map[string]any{"page_id": 1, "type": "cover_book", "local_path": png}),
		"long name":  jsonArgs(t, map[string]any{"page_id": 1, "type": "gallery", "name": strings.Repeat("n", 181), "local_path": png}),
		"no path":    `{"page_id":1,"type":"gallery"}`,
	} {
		_, err := lookup(t, imagesUpload.ID)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(args))
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestImagesReplaceRequiresSameType(t *testing.T) {
	const content = "new"
	for _, tt := range []struct {
		name, file string
		id         int
		ok         bool
	}{
		{"same png", "n.png", 3, true},
		{"png over jpg", "n.png", 4, false},
		{"jpeg over jpg", "n.jpeg", 4, true},
		{"gif over png", "n.gif", 3, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, path := imageFile(t, tt.file, content)
			server := newImageWriteServer(t)
			args := jsonArgs(t, map[string]any{"id": tt.id, "local_path": path})
			_, err := writeCall(t, imagesReplace.ID, server.URL, args, dir, "book/7")
			if tt.ok {
				if err != nil {
					t.Fatal(err)
				}
				want := []string{fmt.Sprintf("GET /api/image-gallery/%d", tt.id), "GET /api/pages/1", fmt.Sprintf("POST /api/image-gallery/%d", tt.id)}
				sent := server.sent()[0]
				if !reflect.DeepEqual(server.requests(), want) || !reflect.DeepEqual(sent.Fields, map[string]string{"_method": "PUT"}) ||
					sent.FileContent != content || sent.ContentLength != sent.BodyLength {
					t.Errorf("requests = %v, sent = %+v", server.requests(), sent)
				}
				return
			}
			if !isInvalidRequest(err) || len(server.sent()) != 0 {
				t.Errorf("err = %v, changes = %v", err, server.sent())
			}
		})
	}
}

func TestImagesUpdateReplaceDeleteBindToBoundBooks(t *testing.T) {
	dir, path := imageFile(t, "n.png", "x")
	for name, call := range map[string]struct{ op, args string }{
		"update":  {imagesUpdate.ID, `{"id":5,"name":"x"}`},
		"replace": {imagesReplace.ID, jsonArgs(t, map[string]any{"id": 5, "local_path": path})},
		"delete":  {imagesDelete.ID, `{"id":5}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := newImageWriteServer(t)
			_, err := writeCall(t, call.op, server.URL, call.args, dir, "book/7")
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") || len(server.sent()) != 0 {
				t.Errorf("err = %v, changes = %v", err, server.sent())
			}
		})
	}
	server := newImageWriteServer(t)
	if _, err := writeCall(t, imagesUpdate.ID, server.URL, `{"id":3,"name":"New"}`, "", "book/7"); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /api/image-gallery/3", "GET /api/pages/1", "PUT /api/image-gallery/3"}
	if !reflect.DeepEqual(server.requests(), want) || !reflect.DeepEqual(server.sent()[0].JSON, map[string]any{"name": "New"}) {
		t.Errorf("requests = %v, sent = %+v", server.requests(), server.sent())
	}
	server = newImageWriteServer(t)
	if _, err := writeCall(t, imagesDelete.ID, server.URL, `{"id":3}`, "", "book/7"); err != nil {
		t.Fatal(err)
	}
	want = []string{"GET /api/image-gallery/3", "GET /api/pages/1", "DELETE /api/image-gallery/3"}
	if !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
}

func TestImageChangesReportUncertaintyAndAreNotRepeated(t *testing.T) {
	const hint = "this change may have taken effect"
	dir, path := imageFile(t, "n.png", "x")
	for _, tt := range []struct{ op, args string }{
		{imagesUpload.ID, jsonArgs(t, map[string]any{"page_id": 1, "type": "gallery", "local_path": path})},
		{imagesUpdate.ID, `{"id":3,"name":"n"}`},
		{imagesReplace.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": path})},
		{imagesDelete.ID, `{"id":3}`},
	} {
		for status, wantHint := range map[int]bool{http.StatusBadGateway: true, http.StatusUnprocessableEntity: false} {
			t.Run(fmt.Sprintf("%s %d", tt.op, status), func(t *testing.T) {
				server := newImageWriteServer(t)
				server.status = status
				_, err := writeCall(t, tt.op, server.URL, tt.args, dir)
				if err == nil || strings.Contains(err.Error(), hint) != wantHint {
					t.Errorf("err = %v, hint wanted %v", err, wantHint)
				}
				if got := len(server.sent()); got != 1 {
					t.Errorf("change requests = %d, want exactly 1", got)
				}
			})
		}
	}
}
