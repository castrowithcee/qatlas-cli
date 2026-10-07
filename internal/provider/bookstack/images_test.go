package bookstack

import (
	"context"
	"crypto/sha256"
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

const imageCanary = "CANARY-image-bytes-3c9a"

type imageServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
}

func (s *imageServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func (s *imageServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
}

func imageRow(id, page int64, kind string) map[string]any {
	return map[string]any{
		"id": id, "name": fmt.Sprintf("img-%d.png", id), "url": "https://bs.example.com/uploads/x.png", "path": "/uploads/x.png",
		"type": kind, "uploaded_to": page, "created_by": 1, "updated_by": 1,
		"created_at": "2026-01-01T00:00:00.000000Z", "updated_at": "2026-01-02T00:00:00.000000Z",
	}
}

// newImageServer serves images 3 (gallery) and 4 (drawio) on page 1 of book 7, image 5 on page 2 of book 9, and
// image 6 of a type this provider does not read. The data is canary bytes.
func newImageServer(t *testing.T, ignoreFilter bool, data func(w http.ResponseWriter)) *imageServer {
	t.Helper()
	s := &imageServer{}
	rows := []map[string]any{imageRow(3, 1, "gallery"), imageRow(4, 1, "drawio"), imageRow(5, 2, "gallery"), imageRow(6, 1, "cover_book")}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/image-gallery", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		var out []map[string]any
		q := r.URL.Query()
		for _, row := range rows {
			if !ignoreFilter && ((q.Get("filter[uploaded_to]") != "" && fmt.Sprint(row["uploaded_to"]) != q.Get("filter[uploaded_to]")) ||
				(q.Get("filter[type]") != "" && row["type"] != q.Get("filter[type]"))) {
				continue
			}
			out = append(out, row)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "total": len(out)})
	})
	mux.HandleFunc("/api/image-gallery/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		for _, row := range rows {
			base := fmt.Sprintf("/api/image-gallery/%v", row["id"])
			switch r.URL.Path {
			case base:
				single := map[string]any{}
				for k, v := range row {
					single[k] = v
				}
				single["created_by"] = map[string]any{"id": 1, "name": "Admin", "slug": "admin"}
				single["updated_by"] = single["created_by"]
				single["thumbs"] = map[string]any{"gallery": "https://bs.example.com/g.png", "display": "https://bs.example.com/d.png"}
				single["content"] = map[string]any{"html": "<img src=x>", "markdown": "![n](x)"}
				_ = json.NewEncoder(w).Encode(single)
				return
			case base + "/data":
				data(w)
				return
			}
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

func canaryData(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/png; charset=binary")
	_, _ = io.WriteString(w, imageCanary)
}

func TestImagesListBindsAndFiltersClientSide(t *testing.T) {
	server := newImageServer(t, true, canaryData) // the server ignores the filters: every row is checked here
	result, err := call(t, imagesList.ID, server.URL, `{"page_id":1}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(t, result); !reflect.DeepEqual(got, []int64{3, 4}) {
		t.Errorf("ids = %v, want the images of page 1 only", got)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/pages/1", "GET /api/image-gallery"}) {
		t.Errorf("requests = %v", got)
	}
	result, err = call(t, imagesList.ID, server.URL, `{"page_id":1,"type":"drawio"}`, "book/7")
	if err != nil || !reflect.DeepEqual(rowIDs(t, result), []int64{4}) {
		t.Errorf("type filter = %v, %v", result, err)
	}
	result, err = call(t, imagesList.ID, server.URL, `{"page_id":1,"limit":1,"offset":1}`, "book/7")
	if err != nil || !reflect.DeepEqual(rowIDs(t, result), []int64{4}) {
		t.Errorf("limit/offset = %v, %v", result, err)
	}
	if _, err := call(t, imagesList.ID, server.URL, `{}`, "book/7"); !isInvalidRequest(err) {
		t.Errorf("bound without page_id: %v", err)
	}
	_, err = call(t, imagesList.ID, server.URL, `{"page_id":2}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign page: %v", err)
	}
	result, err = call(t, imagesList.ID, server.URL, `{}`)
	if err != nil || !reflect.DeepEqual(rowIDs(t, result), []int64{3, 4, 5}) {
		t.Errorf("unbound = %v, %v", result, err)
	}
}

func TestImagesGetBindsThroughThePage(t *testing.T) {
	server := newImageServer(t, false, canaryData)
	result, err := call(t, imagesGet.ID, server.URL, `{"id":3}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	fields := attachmentFieldMap(result)
	if fields["page_id"] != int64(1) || fields["type"] != "gallery" {
		t.Errorf("fields = %v", fields)
	}
	if content := fields["content"].(map[string]string); content["markdown"] != "![n](x)" {
		t.Errorf("content = %v", content)
	}
	_, err = call(t, imagesGet.ID, server.URL, `{"id":5}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign image: %v", err)
	}
	if _, err := call(t, imagesGet.ID, server.URL, `{"id":6}`); err == nil {
		t.Error("an image of another type is read")
	}
	if result, err := call(t, imagesGet.ID, server.URL, `{"id":5}`); err != nil || attachmentFieldMap(result)["page_id"] != int64(2) {
		t.Errorf("unbound: %v, %v", result, err)
	}
}

func TestImagesDownloadWritesFileAndReturnsMetadata(t *testing.T) {
	server := newImageServer(t, false, canaryData)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.png")
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := exportResolved(server.URL, dir, "book/7")
	handler := lookup(t, imagesDownload.ID)
	result, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(imageCanary))
	want := []output.Field{{Name: "id", Value: int64(3)}, {Name: "name", Value: "img-3.png"},
		{Name: "size", Value: int64(len(imageCanary))}, {Name: "sha256", Value: hex.EncodeToString(sum[:])},
		{Name: "content_type", Value: "image/png"}}
	if got := result.(output.Object).Fields; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	if data, _ := os.ReadFile(target); string(data) != imageCanary {
		t.Errorf("file = %q", data)
	}
	if _, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target))); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Errorf("unconfirmed overwrite: %v", err)
	}
	if _, err := handler(capability.WithConfirmed(context.Background()), resolved, resolver(nil), nil, json.RawMessage(downloadArgs(3, target))); err != nil {
		t.Errorf("confirmed overwrite: %v", err)
	}
}

func TestImagesDownloadRequestsNoDataForAForeignImage(t *testing.T) {
	server := newImageServer(t, false, canaryData)
	dir := t.TempDir()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	handler := lookup(t, imagesDownload.ID)
	_, err := handler(context.Background(), exportResolved(server.URL, dir, "book/7"), resolver(nil), nil,
		json.RawMessage(downloadArgs(5, filepath.Join(dir, "x.bin"))))
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("err = %v", err)
	}
	for _, r := range server.requests() {
		if strings.HasSuffix(r, "/data") {
			t.Errorf("data was requested: %v", server.requests())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left files: %v", entries)
	}
}

func TestImagesDownloadCapsSize(t *testing.T) {
	big := func(w http.ResponseWriter) {
		_, _ = io.Copy(w, &repeatReader{b: 'x', left: maxImageDownloadBytes + 1})
	}
	server := newImageServer(t, false, big)
	dir := t.TempDir()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	_, err := lookup(t, imagesDownload.ID)(context.Background(), exportResolved(server.URL, dir), resolver(nil), nil,
		json.RawMessage(downloadArgs(3, filepath.Join(dir, "big.bin"))))
	if err == nil {
		t.Fatal("an image over 64 MiB is written")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left files: %v", entries)
	}
}

func TestImagesDownloadRefusesAPathOutsideTheReleaseBeforeIO(t *testing.T) {
	server := newImageServer(t, false, canaryData)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	_, err := lookup(t, imagesDownload.ID)(context.Background(), exportResolved(server.URL, t.TempDir()), resolver(nil), nil,
		json.RawMessage(downloadArgs(3, filepath.Join(t.TempDir(), "a"))))
	if err == nil || len(server.requests()) != 0 {
		t.Errorf("err = %v, requests = %v", err, server.requests())
	}
}

func TestImageToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	inProfile := map[string]bool{}
	for _, id := range meta.Profiles[0].Tools {
		inProfile[id] = true
	}
	for _, d := range []capability.Descriptor{imagesList, imagesGet, imagesDownload} {
		desc, _, ok := reg.Lookup(d.ID)
		if !ok {
			t.Fatalf("%s not registered", d.ID)
		}
		if desc.Group != "files" || desc.Risk != bookstackReadRisk {
			t.Errorf("%s: group %q risk %+v", d.ID, desc.Group, desc.Risk)
		}
		if want := d.ID != imagesDownload.ID; inProfile[d.ID] != want {
			t.Errorf("%s in read profile = %v, want %v", d.ID, inProfile[d.ID], want)
		}
	}
	if imagesDownload.LocalFiles != config.LocalFilesWrite {
		t.Error("download needs LocalFilesWrite")
	}
}
