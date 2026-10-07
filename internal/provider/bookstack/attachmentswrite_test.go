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
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// sentChange is one change request as the server received it.
type sentChange struct {
	Method, Path, ContentType string
	ContentLength             int64
	BodyLength                int64
	JSON                      map[string]any
	Fields                    map[string]string
	FileName, FileContent     string
}

type writeServer struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []string
	changes []sentChange
	status  int
}

func (s *writeServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func (s *writeServer) sent() []sentChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentChange(nil), s.changes...)
}

// newWriteServer holds pages 1 and 3 in book 7 and page 2 in book 9; attachment 3 is a file and 4 a link on
// page 1, 5 is a file on page 2. With ignoreFilter the listing answers every attachment whatever the filter.
func newWriteServer(t *testing.T, ignoreFilter bool) *writeServer {
	t.Helper()
	s := &writeServer{}
	rows := []map[string]any{attachmentRow(3, 1, false), attachmentRow(4, 1, true), attachmentRow(5, 2, false)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		books := map[string]int{"/api/pages/1": 7, "/api/pages/2": 9, "/api/pages/3": 7}
		if book, ok := books[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/attachments", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodGet {
			var data []map[string]any
			for _, row := range rows {
				if ignoreFilter || fmt.Sprint(row["id"]) == r.URL.Query().Get("filter[id]") {
					data = append(data, row)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": len(data)})
			return
		}
		s.change(w, r)
	})
	mux.HandleFunc("/api/attachments/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		s.change(w, r)
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *writeServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
}

func (s *writeServer) change(w http.ResponseWriter, r *http.Request) {
	got := sentChange{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), ContentLength: r.ContentLength}
	media, params, _ := mime.ParseMediaType(got.ContentType)
	switch {
	case media == "multipart/form-data":
		counter := &countingReader{r: r.Body}
		reader := multipart.NewReader(counter, params["boundary"])
		got.Fields = map[string]string{}
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				got.FileName, got.FileContent = part.FileName(), string(data)
			} else {
				got.Fields[part.FormName()] = string(data)
			}
		}
		_, _ = io.Copy(io.Discard, counter)
		got.BodyLength = counter.n
	case media == "application/json":
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
	row := attachmentRow(9, 1, got.JSON["link"] != nil)
	row["created_by"], row["updated_by"] = 1, 1 // a change answer carries the user ids, not objects
	row["name"] = "stored"
	_ = json.NewEncoder(w).Encode(row)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// writeCall runs a tool of this provider; readDir, when not empty, is released for reading.
func writeCall(t *testing.T, op, url, args, readDir string, targets ...string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := boundResolved(url, targets...)
	if readDir != "" {
		resolved.Files = config.Files{Read: []string{readDir}}
	}
	return lookup(t, op)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(args))
}

func localFile(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func jsonArgs(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAttachmentWriteToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	inProfile := map[string]bool{}
	for _, id := range meta.Profiles[0].Tools {
		inProfile[id] = true
	}
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
		files       config.LocalFiles
	}{
		{attachmentsLink, capability.EffectCreate, capability.IdempotencyNonIdempotent, ""},
		{attachmentsUpload, capability.EffectCreate, capability.IdempotencyNonIdempotent, config.LocalFilesRead},
		{attachmentsUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent, ""},
		{attachmentsReplace, capability.EffectUpdate, capability.IdempotencyNonIdempotent, config.LocalFilesRead},
		{attachmentsDelete, capability.EffectDelete, capability.IdempotencyIdempotent, ""},
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
		if desc.RequiresToolAllowList != (tt.d.ID == attachmentsDelete.ID) {
			t.Errorf("%s: requires_tool_allow_list = %v", tt.d.ID, desc.RequiresToolAllowList)
		}
		if desc.LocalFiles != tt.files {
			t.Errorf("%s: local files = %q", tt.d.ID, desc.LocalFiles)
		}
		if inProfile[tt.d.ID] {
			t.Errorf("%s is in the read profile", tt.d.ID)
		}
	}
}

func TestAttachmentsLink(t *testing.T) {
	server := newWriteServer(t, false)
	result, err := writeCall(t, attachmentsLink.ID, server.URL, `{"page_id":1,"name":"Spec","link":"https://example.com/a?b=1&c=<2>"}`, "", "book/7")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/pages/1", "POST /api/attachments"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
	sent := server.sent()[0]
	want := map[string]any{"name": "Spec", "uploaded_to": float64(1), "link": "https://example.com/a?b=1&c=<2>"}
	if sent.ContentType != "application/json" || !reflect.DeepEqual(sent.JSON, want) {
		t.Errorf("sent = %+v, want JSON %v", sent, want)
	}
	object, ok := result.(output.Object)
	if !ok || len(object.Fields) == 0 || object.Fields[0].Value != int64(9) {
		t.Errorf("result = %#v", result)
	}

	server = newWriteServer(t, false)
	if _, err := writeCall(t, attachmentsLink.ID, server.URL, `{"page_id":2,"name":"x","link":"https://example.com"}`, "", "book/7"); !isInvalidRequest(err) ||
		strings.Contains(err.Error(), "9") || len(server.sent()) != 0 {
		t.Errorf("foreign page: err = %v, changes = %v", err, server.sent())
	}
	server = newWriteServer(t, false)
	if _, err := writeCall(t, attachmentsLink.ID, server.URL, `{"page_id":2,"name":"x","link":"http://example.com"}`, ""); err != nil {
		t.Fatal(err)
	}
	if want := []string{"POST /api/attachments"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("unbound requests = %v, want %v", server.requests(), want)
	}
}

func TestAttachmentLinkIsCheckedLocallyBeforeSecretsAndIO(t *testing.T) {
	server := newWriteServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for name, link := range map[string]string{
		"javascript":  "javascript:alert(1)",
		"ftp":         "ftp://example.com/a",
		"file":        "file:///etc/passwd",
		"data":        "data:text/plain,hi",
		"no scheme":   "example.com/a",
		"relative":    "/a/b",
		"user info":   "https://user:pass@example.com/a",
		"user only":   "https://user@example.com/a",
		"no host":     "https:///a",
		"opaque":      "https:example.com",
		"space":       "https://example.com/a b",
		"too long":    "https://example.com/" + strings.Repeat("a", 2000),
		"empty":       "",
		"control":     "https://example.com/\x01",
		"uppercase s": "HTTPS:",
	} {
		t.Run(name, func(t *testing.T) {
			args := jsonArgs(t, map[string]any{"page_id": 1, "name": "x", "link": link})
			_, err := lookup(t, attachmentsLink.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(args))
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "user") && strings.Contains(err.Error(), "pass") {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
		})
	}
	// The longest allowed link is accepted by the local check.
	if err := checkAttachmentLink("https://example.com/" + strings.Repeat("a", 2000-len("https://example.com/"))); err != nil {
		t.Errorf("2000 characters: %v", err)
	}
	if err := checkAttachmentLink("HTTP://Example.com/x"); err != nil {
		t.Errorf("upper case scheme: %v", err)
	}
	for name, args := range map[string]string{
		"empty name": `{"page_id":1,"name":"","link":"https://example.com"}`,
		"long name":  `{"page_id":1,"name":"` + strings.Repeat("é", 256) + `","link":"https://example.com"}`,
		"bad page":   `{"page_id":0,"name":"x","link":"https://example.com"}`,
	} {
		if _, err := lookup(t, attachmentsLink.ID)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(args)); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestAttachmentsUploadSendsAStreamedMultipartForm(t *testing.T) {
	const content = "line one\r\n--not-a-boundary\r\nbinary \x00\xff end"
	dir, path := localFile(t, content)
	server := newWriteServer(t, false)
	args := jsonArgs(t, map[string]any{"page_id": 1, "name": "Datasheet", "local_path": path})
	if _, err := writeCall(t, attachmentsUpload.ID, server.URL, args, dir, "book/7"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/pages/1", "POST /api/attachments"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
	sent := server.sent()[0]
	if !strings.HasPrefix(sent.ContentType, "multipart/form-data; boundary=") {
		t.Errorf("content type = %q", sent.ContentType)
	}
	if want := map[string]string{"name": "Datasheet", "uploaded_to": "1"}; !reflect.DeepEqual(sent.Fields, want) {
		t.Errorf("fields = %v, want %v", sent.Fields, want)
	}
	if sent.FileName != "data.bin" || sent.FileContent != content {
		t.Errorf("file = %q %q", sent.FileName, sent.FileContent)
	}
	if sent.ContentLength <= 0 || sent.ContentLength != sent.BodyLength {
		t.Errorf("content length = %d, body = %d: the length must be announced and exact", sent.ContentLength, sent.BodyLength)
	}
}

func TestAttachmentsUploadUsesALongTimeoutAndAFiftyMiBLimit(t *testing.T) {
	dir, path := localFile(t, "x")
	server := newWriteServer(t, false)
	var deadline time.Duration
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			if d, ok := r.Context().Deadline(); ok {
				deadline = time.Until(d)
			}
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { transport = previous })
	args := jsonArgs(t, map[string]any{"page_id": 1, "name": "n", "local_path": path})
	if _, err := writeCall(t, attachmentsUpload.ID, server.URL, args, dir); err != nil {
		t.Fatal(err)
	}
	if deadline < 29*time.Minute || deadline > 30*time.Minute {
		t.Errorf("upload deadline in %v, want about 30 minutes", deadline)
	}
	if maxUploadBytes != 50<<20 || uploadTimeout != 30*time.Minute {
		t.Errorf("limits = %d, %v", maxUploadBytes, uploadTimeout)
	}

	// A sparse file one byte over the limit is refused before any secret or request.
	big := filepath.Join(dir, "big.bin")
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxUploadBytes + 1); err != nil {
		t.Skip("sparse files are not supported here")
	}
	file.Close()
	server = newWriteServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	resolved := boundResolved(server.URL)
	resolved.Files = config.Files{Read: []string{dir}}
	for _, tt := range []struct{ op, args string }{
		{attachmentsUpload.ID, jsonArgs(t, map[string]any{"page_id": 1, "name": "n", "local_path": big})},
		{attachmentsReplace.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": big})},
	} {
		_, err := lookup(t, tt.op)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(tt.args))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "50 MiB") {
			t.Errorf("%s: err = %v", tt.op, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttachmentUploadPathIsRefusedBeforeSecretsAndIO(t *testing.T) {
	_, path := localFile(t, "x")
	server := newWriteServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, tt := range []struct{ op, args string }{
		{attachmentsUpload.ID, jsonArgs(t, map[string]any{"page_id": 1, "name": "n", "local_path": path})},
		{attachmentsReplace.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": path})},
		{attachmentsUpload.ID, `{"page_id":1,"name":"n"}`},
	} {
		// No directory is released for reading.
		_, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL), resolver(nil), nil, json.RawMessage(tt.args))
		if err == nil || strings.Contains(err.Error(), "token") {
			t.Errorf("%s: err = %v", tt.op, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestAttachmentsReplaceSendsPostWithMethodOverride(t *testing.T) {
	const content = "new content"
	dir, path := localFile(t, content)
	server := newWriteServer(t, false)
	args := jsonArgs(t, map[string]any{"id": 3, "local_path": path})
	if _, err := writeCall(t, attachmentsReplace.ID, server.URL, args, dir, "book/7"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/attachments", "GET /api/pages/1", "POST /api/attachments/3"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}
	sent := server.sent()[0]
	if want := map[string]string{"_method": "PUT"}; !reflect.DeepEqual(sent.Fields, want) || sent.FileContent != content ||
		sent.ContentLength != sent.BodyLength {
		t.Errorf("sent = %+v", sent)
	}
}

func TestAttachmentsReplaceRefusesLinksAndForeignAttachments(t *testing.T) {
	dir, path := localFile(t, "x")
	for _, tt := range []struct {
		name    string
		id      int
		targets []string
		reads   []string
	}{
		{"link, bound", 4, []string{"book/7"}, []string{"GET /api/attachments", "GET /api/pages/1"}},
		{"link, unbound", 4, nil, []string{"GET /api/attachments"}},
		{"foreign file", 5, []string{"book/7"}, []string{"GET /api/attachments", "GET /api/pages/2"}},
		{"missing", 8, []string{"book/7"}, []string{"GET /api/attachments"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newWriteServer(t, false)
			args := jsonArgs(t, map[string]any{"id": tt.id, "local_path": path})
			_, err := writeCall(t, attachmentsReplace.ID, server.URL, args, dir, tt.targets...)
			if err == nil || strings.Contains(err.Error(), "9") && tt.id == 5 {
				t.Fatalf("err = %v", err)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.reads) || len(server.sent()) != 0 {
				t.Errorf("requests = %v, want %v", got, tt.reads)
			}
		})
	}
}

func TestAttachmentsUpdateBindsSourceAndTargetPage(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    string
		targets []string
		reqs    []string
		body    map[string]any
		refused bool
	}{
		{"rename", `{"id":3,"name":"New"}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/1", "PUT /api/attachments/3"}, map[string]any{"name": "New"}, false},
		{"link target", `{"id":4,"link":"https://example.com/x"}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/1", "PUT /api/attachments/4"}, map[string]any{"link": "https://example.com/x"}, false},
		{"move within the books", `{"id":3,"page_id":3}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/1", "GET /api/pages/3", "PUT /api/attachments/3"}, map[string]any{"uploaded_to": float64(3)}, false},
		{"move to a foreign page", `{"id":3,"page_id":2}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/1", "GET /api/pages/2"}, nil, true},
		{"move a foreign attachment", `{"id":5,"page_id":1}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/2"}, nil, true},
		{"foreign rename", `{"id":5,"name":"x"}`, []string{"book/7"},
			[]string{"GET /api/attachments", "GET /api/pages/2"}, nil, true},
		{"unbound reads nothing", `{"id":5,"name":"x","page_id":2}`, nil,
			[]string{"PUT /api/attachments/5"}, map[string]any{"name": "x", "uploaded_to": float64(2)}, false},
		{"several books, move across them", `{"id":3,"page_id":2}`, []string{"book/7", "book/9"},
			[]string{"GET /api/attachments", "GET /api/pages/1", "GET /api/pages/2", "PUT /api/attachments/3"}, map[string]any{"uploaded_to": float64(2)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newWriteServer(t, false)
			_, err := writeCall(t, attachmentsUpdate.ID, server.URL, tt.args, "", tt.targets...)
			if tt.refused {
				if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
					t.Fatalf("err = %v, want a refusal without the foreign target", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.reqs) {
				t.Errorf("requests = %v, want %v", got, tt.reqs)
			}
			if tt.body != nil {
				if sent := server.sent(); len(sent) != 1 || !reflect.DeepEqual(sent[0].JSON, tt.body) || sent[0].ContentType != "application/json" {
					t.Errorf("sent = %+v, want %v", sent, tt.body)
				}
			}
		})
	}
}

func TestAttachmentsUpdateRefusesInvalidInputLocally(t *testing.T) {
	server := newWriteServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for name, args := range map[string]string{
		"nothing":    `{"id":3}`,
		"bad link":   `{"id":4,"link":"ftp://example.com"}`,
		"user info":  `{"id":4,"link":"https://u:p@example.com"}`,
		"empty name": `{"id":3,"name":""}`,
		"bad page":   `{"id":3,"page_id":0}`,
	} {
		_, err := lookup(t, attachmentsUpdate.ID)(context.Background(), boundResolved(server.URL, "book/7"), resolver(nil), nil, json.RawMessage(args))
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestAttachmentsDeleteIsToolListOnlyAndBound(t *testing.T) {
	if !attachmentsDelete.RequiresToolAllowList || attachmentsDelete.Risk.Effect != capability.EffectDelete {
		t.Fatalf("delete descriptor = %+v", attachmentsDelete)
	}
	server := newWriteServer(t, false)
	result, err := writeCall(t, attachmentsDelete.ID, server.URL, `{"id":3}`, "", "book/7")
	if err != nil || !reflect.DeepEqual(result, map[string]bool{"deleted": true}) {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	if want := []string{"GET /api/attachments", "GET /api/pages/1", "DELETE /api/attachments/3"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("requests = %v, want %v", server.requests(), want)
	}

	server = newWriteServer(t, false)
	if _, err := writeCall(t, attachmentsDelete.ID, server.URL, `{"id":5}`, "", "book/7"); !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign: err = %v", err)
	}
	if want := []string{"GET /api/attachments", "GET /api/pages/2"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("foreign requests = %v, want %v", server.requests(), want)
	}

	server = newWriteServer(t, true) // the listing ignores the filter, the row is found by id
	if _, err := writeCall(t, attachmentsDelete.ID, server.URL, `{"id":5}`, "", "book/9"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/attachments", "GET /api/pages/2", "DELETE /api/attachments/5"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("ignored filter requests = %v, want %v", server.requests(), want)
	}

	server = newWriteServer(t, true) // an id the listing does not hold is not found, nothing is deleted
	if _, err := writeCall(t, attachmentsDelete.ID, server.URL, `{"id":77}`, "", "book/7"); err == nil || len(server.sent()) != 0 {
		t.Errorf("missing: err = %v, changes = %v", err, server.sent())
	}

	server = newWriteServer(t, false)
	if _, err := writeCall(t, attachmentsDelete.ID, server.URL, `{"id":5}`, ""); err != nil {
		t.Fatal(err)
	}
	if want := []string{"DELETE /api/attachments/5"}; !reflect.DeepEqual(server.requests(), want) {
		t.Errorf("unbound requests = %v, want %v", server.requests(), want)
	}
}

func TestAttachmentChangesReportUncertaintyAndAreNotRepeated(t *testing.T) {
	const hint = "this change may have taken effect"
	dir, path := localFile(t, "x")
	for _, tt := range []struct{ op, args string }{
		{attachmentsLink.ID, `{"page_id":1,"name":"n","link":"https://example.com"}`},
		{attachmentsUpload.ID, jsonArgs(t, map[string]any{"page_id": 1, "name": "n", "local_path": path})},
		{attachmentsUpdate.ID, `{"id":3,"name":"n"}`},
		{attachmentsReplace.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": path})},
		{attachmentsDelete.ID, `{"id":3}`},
	} {
		for status, wantHint := range map[int]bool{http.StatusBadGateway: true, http.StatusUnprocessableEntity: false} {
			t.Run(fmt.Sprintf("%s %d", tt.op, status), func(t *testing.T) {
				server := newWriteServer(t, false)
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
