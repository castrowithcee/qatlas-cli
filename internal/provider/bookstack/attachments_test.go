package bookstack

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

const attachmentCanary = "CANARY-attachment-bytes-7d2f"

type attachmentServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
}

func (s *attachmentServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func attachmentRow(id, page int64, external bool) map[string]any {
	return map[string]any{
		"id": id, "name": fmt.Sprintf("file-%d", id), "extension": "txt", "uploaded_to": page, "external": external,
		"order": 1, "created_at": "2026-01-01T00:00:00.000000Z", "updated_at": "2026-01-02T00:00:00.000000Z",
		"created_by": 1, "updated_by": 1,
	}
}

// newAttachmentServer serves attachments 3 and 6 (files) and 4 (link) on page 1 of book 7 and attachment 5 on page
// 2 of book 9. File content is canary bytes; the read answer is written like BookStack does: the metadata
// object, then raw base64 in place of the content, then the user objects.
func newAttachmentServer(t *testing.T, ignoreFilter bool) *attachmentServer {
	t.Helper()
	s := &attachmentServer{}
	rows := []map[string]any{
		attachmentRow(3, 1, false), attachmentRow(4, 1, true), attachmentRow(5, 2, false), attachmentRow(6, 1, false),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/attachments", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		var data []map[string]any
		for _, row := range rows {
			if !ignoreFilter && r.URL.Query().Get("filter[uploaded_to]") != "" &&
				fmt.Sprint(row["uploaded_to"]) != r.URL.Query().Get("filter[uploaded_to]") {
				continue
			}
			data = append(data, row)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": len(data)})
	})
	mux.HandleFunc("/api/attachments/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		for _, row := range rows {
			if r.URL.Path != fmt.Sprintf("/api/attachments/%v", row["id"]) {
				continue
			}
			// Fields in the order BookStack writes them: the content follows the attachment's own attributes.
			head := fmt.Sprintf(`{"id":%v,"name":%q,"extension":"txt","uploaded_to":%v,"external":%v,"order":1,`+
				`"created_at":"2026-01-01T00:00:00.000000Z","updated_at":"2026-01-02T00:00:00.000000Z",`+
				`"links":{"html":"<a href=\"x\">n</a>","markdown":"[n](x)"},"content":`,
				row["id"], row["name"], row["uploaded_to"], row["external"])
			tail := `,"created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":1,"name":"Admin","slug":"admin"}}`
			if row["external"] == true {
				_, _ = io.WriteString(w, head+`"https:\/\/link.example.com\/a?b=1"`+tail)
				return
			}
			_, _ = io.WriteString(w, head+`"`+base64.StdEncoding.EncodeToString([]byte(attachmentCanary))+`"`+tail)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		books := map[string]int{"/api/pages/1": 7, "/api/pages/2": 9}
		if book, ok := books[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *attachmentServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
}

func TestAttachmentsList(t *testing.T) {
	server := newAttachmentServer(t, true) // the server ignores the filter: every row is checked here
	result, err := call(t, attachmentsList.ID, server.URL, `{"page_id":1}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(t, result); !reflect.DeepEqual(got, []int64{3, 4, 6}) {
		t.Errorf("ids = %v, want the rows of page 1 only", got)
	}
	row := result.(output.Collection).Rows[1]
	if row["external"] != true || row["page_id"] != int64(1) {
		t.Errorf("row = %v", row)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/pages/1", "GET /api/attachments"}) {
		t.Errorf("requests = %v", got)
	}
	result, err = call(t, attachmentsList.ID, server.URL, `{"page_id":1,"limit":1,"offset":1}`, "book/7")
	if err != nil || !reflect.DeepEqual(rowIDs(t, result), []int64{4}) {
		t.Errorf("limit/offset = %v, %v", result, err)
	}
}

func TestAttachmentsListBinding(t *testing.T) {
	server := newAttachmentServer(t, false)
	if _, err := call(t, attachmentsList.ID, server.URL, `{}`, "book/7"); !isInvalidRequest(err) {
		t.Errorf("bound without page_id: err = %v", err)
	}
	_, err := call(t, attachmentsList.ID, server.URL, `{"page_id":2}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign page: err = %v", err)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/pages/2"}) {
		t.Errorf("requests = %v, want only the evidence read", got)
	}
	result, err := call(t, attachmentsList.ID, server.URL, `{}`)
	if err != nil || !reflect.DeepEqual(rowIDs(t, result), []int64{3, 4, 5, 6}) {
		t.Errorf("unbound = %v, %v", result, err)
	}
}

func attachmentFieldMap(obj any) map[string]any {
	fields := map[string]any{}
	for _, f := range obj.(output.Object).Fields {
		fields[f.Name] = f.Value
	}
	return fields
}

func TestAttachmentsGetFileAndLink(t *testing.T) {
	server := newAttachmentServer(t, false)
	result, err := call(t, attachmentsGet.ID, server.URL, `{"id":3}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	fields := attachmentFieldMap(result)
	if fields["page_id"] != int64(1) || fields["external"] != false || fields["name"] != "file-3" {
		t.Errorf("fields = %v", fields)
	}
	if _, ok := fields["url"]; ok {
		t.Error("a file attachment has a url")
	}
	if strings.Contains(fmt.Sprint(fields), attachmentCanary) || strings.Contains(fmt.Sprint(fields), base64.StdEncoding.EncodeToString([]byte(attachmentCanary))) {
		t.Error("the content reached the answer")
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/attachments/3", "GET /api/pages/1"}) {
		t.Errorf("requests = %v", got)
	}
	result, err = call(t, attachmentsGet.ID, server.URL, `{"id":4}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	fields = attachmentFieldMap(result)
	if fields["url"] != "https://link.example.com/a?b=1" || fields["external"] != true {
		t.Errorf("link fields = %v", fields)
	}
}

func TestAttachmentsGetRefusesForeignAttachment(t *testing.T) {
	server := newAttachmentServer(t, false)
	_, err := call(t, attachmentsGet.ID, server.URL, `{"id":5}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") || strings.Contains(err.Error(), "file-5") {
		t.Errorf("foreign attachment: err = %v", err)
	}
	if result, err := call(t, attachmentsGet.ID, server.URL, `{"id":5}`); err != nil || attachmentFieldMap(result)["page_id"] != int64(2) {
		t.Errorf("unbound: %v, %v", result, err)
	}
}

func downloadArgs(id int, path string) string {
	encoded, _ := json.Marshal(map[string]any{"id": id, "local_path": path})
	return string(encoded)
}

func TestAttachmentsDownloadWritesFileAndReturnsMetadata(t *testing.T) {
	server := newAttachmentServer(t, false)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := exportResolved(server.URL, dir, "book/7")
	handler := lookup(t, attachmentsDownload.ID)
	result, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(attachmentCanary))
	want := []output.Field{{Name: "id", Value: int64(3)}, {Name: "name", Value: "file-3"},
		{Name: "size", Value: int64(len(attachmentCanary))}, {Name: "sha256", Value: hex.EncodeToString(sum[:])}}
	if got := result.(output.Object).Fields; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	if data, _ := os.ReadFile(target); string(data) != attachmentCanary {
		t.Errorf("file = %q", data)
	}
	if _, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target))); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Errorf("unconfirmed overwrite: %v", err)
	}
	if _, err := handler(capability.WithConfirmed(context.Background()), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target))); err != nil {
		t.Errorf("confirmed overwrite: %v", err)
	}
}

func TestAttachmentsDownloadWritesNothingForForeignOrLinkAttachments(t *testing.T) {
	server := newAttachmentServer(t, false)
	dir := t.TempDir()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := exportResolved(server.URL, dir, "book/7")
	handler := lookup(t, attachmentsDownload.ID)
	for _, id := range []int{4, 5, 99} {
		_, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(id, filepath.Join(dir, "x.bin"))))
		if err == nil {
			t.Errorf("attachment %d: no error", id)
		}
		if id == 5 && (!isInvalidRequest(err) || strings.Contains(err.Error(), "9")) {
			t.Errorf("foreign attachment: err = %v", err)
		}
		if id == 4 && !isInvalidRequest(err) {
			t.Errorf("link attachment: err = %v", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("attachment %d left files: %v", id, entries)
		}
	}
	// An unbound connection refuses a link as well.
	resolved = exportResolved(server.URL, dir)
	if _, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(4, filepath.Join(dir, "x.bin")))); !isInvalidRequest(err) {
		t.Errorf("unbound link: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left files: %v", entries)
	}
}

func TestAttachmentsDownloadRefusesAPathOutsideTheReleaseBeforeIO(t *testing.T) {
	server := newAttachmentServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	_, err := lookup(t, attachmentsDownload.ID)(context.Background(), exportResolved(server.URL, t.TempDir()), resolver(nil), nil,
		json.RawMessage(downloadArgs(3, filepath.Join(t.TempDir(), "a"))))
	if err == nil || len(server.requests()) != 0 {
		t.Errorf("err = %v, requests = %v", err, server.requests())
	}
}

// repeatReader yields n copies of b and then stops.
type repeatReader struct {
	b    byte
	left int64
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	for i := int64(0); i < n; i++ {
		p[i] = r.b
	}
	r.left -= n
	return int(n), nil
}

func attachmentAnswer(content io.Reader) io.Reader {
	return io.MultiReader(strings.NewReader(`{"id":3,"uploaded_to":1,"external":false,"content":"`), content,
		strings.NewReader(`","created_by":{"id":1}}`))
}

func TestReadAttachmentCapsAreEnforced(t *testing.T) {
	discard := func(*attachmentJSON) (io.Writer, error) { return io.Discard, nil }
	// More base64 text than 96 MiB is refused without being held.
	_, err := readAttachment(attachmentAnswer(&repeatReader{b: 'A', left: maxAttachmentContentChars + 4096}), maxAttachmentContentChars, discard)
	if !errors.Is(err, errAttachmentTooLarge) {
		t.Errorf("raw cap: %v", err)
	}
	// Exactly 72 MiB decoded is accepted, one byte more is not.
	exact := maxAttachmentFileBytes / 3 * 4
	read, err := readAttachment(attachmentAnswer(&repeatReader{b: 'A', left: int64(exact)}), maxAttachmentFileBytes,
		func(*attachmentJSON) (io.Writer, error) { return io.Discard, nil })
	if err != nil || read.Decoded != maxAttachmentFileBytes {
		t.Errorf("72 MiB: decoded = %d, err = %v", read.Decoded, err)
	}
	_, err = readAttachment(attachmentAnswer(&repeatReader{b: 'A', left: int64(exact + 4)}), maxAttachmentFileBytes,
		func(*attachmentJSON) (io.Writer, error) { return io.Discard, nil })
	if !errors.Is(err, errAttachmentTooLarge) {
		t.Errorf("decoded cap: %v", err)
	}
}

func TestReadAttachmentRejectsMalformedAnswers(t *testing.T) {
	discard := func(*attachmentJSON) (io.Writer, error) { return io.Discard, nil }
	for name, body := range map[string]string{
		"metadata after content": `{"content":"QUJD","id":3,"uploaded_to":1,"external":false}`,
		"escape in base64":       `{"id":3,"uploaded_to":1,"external":false,"content":"QU\/D"}`,
		"invalid base64":         `{"id":3,"uploaded_to":1,"external":false,"content":"!!!!"}`,
		"truncated":              `{"id":3,"uploaded_to":1,"external":false,"content":"QUJD`,
		"duplicate content":      `{"id":3,"uploaded_to":1,"external":false,"content":"QUJD","content":"QUJD"}`,
		"number content":         `{"id":3,"uploaded_to":1,"external":false,"content":12}`,
	} {
		if _, err := readAttachment(strings.NewReader(body), 1<<20, discard); !errors.Is(err, errAttachmentInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	read, err := readAttachment(strings.NewReader(` { "id" : 3 , "uploaded_to":1,"external":false,"extra":{"a":["}",1]},"content":"QUJD","order":2}`), 1<<20, discard)
	if err != nil || read.Decoded != 3 || read.Meta.Order != 2 {
		t.Errorf("valid answer: %+v, %v", read, err)
	}
}

func TestAttachmentToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	hasGroup := false
	for _, g := range meta.Groups {
		hasGroup = hasGroup || g.ID == "files"
	}
	if !hasGroup {
		t.Error("group files is missing")
	}
	inProfile := map[string]bool{}
	for _, id := range meta.Profiles[0].Tools {
		inProfile[id] = true
	}
	for _, d := range []capability.Descriptor{attachmentsList, attachmentsGet, attachmentsDownload} {
		desc, _, ok := reg.Lookup(d.ID)
		if !ok {
			t.Fatalf("%s not registered", d.ID)
		}
		if desc.Group != "files" || desc.Risk != bookstackReadRisk {
			t.Errorf("%s: group %q risk %+v", d.ID, desc.Group, desc.Risk)
		}
		if want := d.ID != attachmentsDownload.ID; inProfile[d.ID] != want {
			t.Errorf("%s in read profile = %v, want %v", d.ID, inProfile[d.ID], want)
		}
	}
	if attachmentsDownload.LocalFiles != config.LocalFilesWrite {
		t.Error("download needs LocalFilesWrite")
	}
}
