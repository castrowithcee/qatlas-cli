package bookstack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

const exportCanary = "CANARY-provider-text-5c1e"

// exportServer answers evidence reads (page 1 and chapter 10 belong to book 7, page 2 and chapter 20 to book
// 9) and every export path with body. A 403 for id 403 carries a canary that must never be relayed.
func exportServer(t *testing.T, rec *recorder, body []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
		if len(parts) > 1 && parts[1] == "403" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(exportCanary))
			return
		}
		if len(parts) == 4 && parts[2] == "export" {
			_, _ = w.Write(body)
			return
		}
		books := map[string]int{"pages/1": 7, "pages/2": 9, "chapters/10": 7, "chapters/20": 9}
		if book, ok := books[strings.Join(parts, "/")]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server
}

func exportResolved(baseURL, dir string, targets ...string) *config.Resolved {
	resolved := boundResolved(baseURL, targets...)
	if dir != "" {
		resolved.Files.Write = []string{dir}
	}
	return resolved
}

func exportArgs(kind string, id int, format, path string) json.RawMessage {
	args := map[string]any{"type": kind, "id": id, "format": format}
	if path != "" {
		args["local_path"] = path
	}
	encoded, _ := json.Marshal(args)
	return encoded
}

func exportPaths(rec *recorder) []string {
	_, paths, _, _ := rec.snapshot()
	return paths
}

func TestExportAndDownloadUseFixedPathsPerTypeAndFormat(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	for _, kind := range []string{"page", "chapter", "book"} {
		collection := kind + "s"
		for _, format := range downloadFormats {
			rec := &recorder{}
			server := exportServer(t, rec, []byte("body"))
			dir := t.TempDir()
			target := filepath.Join(dir, "out."+format)
			if _, err := lookup(t, contentDownload.ID)(context.Background(), exportResolved(server.URL, dir), resolver(nil), nil, exportArgs(kind, 5, format, target)); err != nil {
				t.Fatalf("download %s %s: %v", kind, format, err)
			}
			methods, paths, queries, auth := rec.snapshot()
			want := "/api/" + collection + "/5/export/" + format
			if len(paths) != 1 || paths[0] != want || methods[0] != http.MethodGet || queries[0] != "" || auth[0] != "Token "+canaryID+":"+canarySecret {
				t.Errorf("download %s %s: %v %v %v", kind, format, methods, paths, queries)
			}
		}
		for _, format := range inlineFormats {
			rec := &recorder{}
			server := exportServer(t, rec, []byte("body"))
			result, err := lookup(t, contentExport.ID)(context.Background(), exportResolved(server.URL, ""), resolver(nil), nil, exportArgs(kind, 5, format, ""))
			if err != nil {
				t.Fatalf("export %s %s: %v", kind, format, err)
			}
			if got := exportPaths(rec); len(got) != 1 || got[0] != "/api/"+collection+"/5/export/"+format {
				t.Errorf("export %s %s: paths = %v", kind, format, got)
			}
			if fields := result.(output.Object).Fields; fields[3].Value != "body" {
				t.Errorf("content = %v", fields[3].Value)
			}
		}
	}
}

func TestExportRejectsUnknownEnumsBeforeAnyIO(t *testing.T) {
	rec := &recorder{}
	server := exportServer(t, rec, []byte("x"))
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	dir := t.TempDir()
	for name, test := range map[string]struct {
		tool string
		args json.RawMessage
	}{
		"type":            {contentExport.ID, exportArgs("shelf", 1, "html", "")},
		"path-type":       {contentExport.ID, exportArgs("../books", 1, "html", "")},
		"inline pdf":      {contentExport.ID, exportArgs("page", 1, "pdf", "")},
		"inline zip":      {contentExport.ID, exportArgs("page", 1, "zip", "")},
		"download format": {contentDownload.ID, exportArgs("page", 1, "html/../x", filepath.Join(dir, "a"))},
		"id":              {contentExport.ID, exportArgs("page", 0, "html", "")},
		"no path":         {contentDownload.ID, exportArgs("page", 1, "html", "")},
	} {
		if _, err := lookup(t, test.tool)(context.Background(), exportResolved(server.URL, dir), resolver(nil), nil, test.args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func TestExportIsCappedInline(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	for name, test := range map[string]struct {
		body []byte
		ok   bool
	}{
		"at the limit":    {bytes.Repeat([]byte("a"), maxExportInlineBytes), true},
		"over the limit":  {bytes.Repeat([]byte("a"), maxExportInlineBytes+1), false},
		"not valid text":  {[]byte{0xff, 0xfe}, false},
		"empty":           {nil, true},
		"multibyte split": {[]byte("äöü"), true},
	} {
		server := exportServer(t, &recorder{}, test.body)
		_, err := lookup(t, contentExport.ID)(context.Background(), exportResolved(server.URL, ""), resolver(nil), nil, exportArgs("book", 3, "markdown", ""))
		if (err == nil) != test.ok {
			t.Errorf("%s: err = %v", name, err)
		}
		if name == "over the limit" && (err == nil || !strings.Contains(err.Error(), "content.download")) {
			t.Errorf("%s: the refusal does not point to content.download: %v", name, err)
		}
	}
}

func TestDownloadReturnsOnlyMetadataAndReplacesOnlyWithConfirmation(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	content := bytes.Repeat([]byte("PK-zip-content"), 1000)
	rec := &recorder{}
	server := exportServer(t, rec, content)
	dir := t.TempDir()
	target := filepath.Join(dir, "book.zip")
	resolved := exportResolved(server.URL, dir)
	handler := lookup(t, contentDownload.ID)

	result, err := handler(context.Background(), resolved, resolver(nil), nil, exportArgs("book", 7, "zip", target))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	want := []output.Field{{Name: "type", Value: "book"}, {Name: "id", Value: int64(7)}, {Name: "format", Value: "zip"},
		{Name: "size", Value: int64(len(content))}, {Name: "sha256", Value: hex.EncodeToString(sum[:])}}
	got := result.(output.Object).Fields
	if len(got) != len(want) {
		t.Fatalf("fields = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %v, want %v", i, got[i], want[i])
		}
	}
	if data, _ := os.ReadFile(target); !bytes.Equal(data, content) {
		t.Error("the written file differs from the export")
	}

	// Replacing needs confirmation, and nothing is requested before the refusal.
	before := len(exportPaths(rec))
	if _, err := handler(context.Background(), resolved, resolver(nil), nil, exportArgs("book", 7, "zip", target)); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	if len(exportPaths(rec)) != before {
		t.Error("an unconfirmed overwrite reached BookStack")
	}
	if _, err := handler(capability.WithConfirmed(context.Background()), resolved, resolver(nil), nil, exportArgs("book", 7, "zip", target)); err != nil {
		t.Fatalf("confirmed overwrite: %v", err)
	}
}

func TestDownloadRefusesAPathOutsideTheReleaseAndAnOversizedExport(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	rec := &recorder{}
	server := exportServer(t, rec, []byte("x"))
	dir, other := t.TempDir(), t.TempDir()
	if _, err := lookup(t, contentDownload.ID)(context.Background(), exportResolved(server.URL, dir), resolver(nil), nil,
		exportArgs("page", 1, "pdf", filepath.Join(other, "a.pdf"))); err == nil {
		t.Error("a path outside the released directory was accepted")
	}
	if len(exportPaths(rec)) != 0 {
		t.Error("the refusal reached BookStack")
	}

	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	big := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxDownloadBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(big.Close)
	target := filepath.Join(dir, "big.pdf")
	_, err := lookup(t, contentDownload.ID)(context.Background(), exportResolved(big.URL, dir), resolver(nil), nil, exportArgs("book", 1, "pdf", target))
	if err == nil || !strings.Contains(err.Error(), "512 MiB") {
		t.Errorf("err = %v", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Error("an oversized export left a file")
	}
}

func TestExportBindingPerType(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	dir := t.TempDir()
	for _, tool := range []string{contentExport.ID, contentDownload.ID} {
		path := func(name string) string {
			if tool == contentDownload.ID {
				return filepath.Join(dir, name)
			}
			return ""
		}
		for name, test := range map[string]struct {
			kind     string
			id       int
			ok       bool
			requests []string
		}{
			"own book":        {"book", 7, true, []string{"/api/books/7/export/html"}},
			"own chapter":     {"chapter", 10, true, []string{"/api/chapters/10", "/api/chapters/10/export/html"}},
			"own page":        {"page", 1, true, []string{"/api/pages/1", "/api/pages/1/export/html"}},
			"foreign chapter": {"chapter", 20, false, []string{"/api/chapters/20"}},
			"foreign page":    {"page", 2, false, []string{"/api/pages/2"}},
		} {
			rec := &recorder{}
			server := exportServer(t, rec, []byte("x"))
			_, err := lookup(t, tool)(context.Background(), exportResolved(server.URL, dir, "book/7"), resolver(nil), nil, exportArgs(test.kind, test.id, "html", path(tool+name)))
			if (err == nil) != test.ok {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
			if !test.ok && (!isInvalidRequest(err) || strings.Contains(err.Error(), "9")) {
				t.Errorf("%s %s: refusal = %v", tool, name, err)
			}
			if got := exportPaths(rec); strings.Join(got, ",") != strings.Join(test.requests, ",") {
				t.Errorf("%s %s: requests = %v, want %v", tool, name, got, test.requests)
			}
		}

		// A foreign book is refused before any secret and any request.
		rec := &recorder{}
		server := exportServer(t, rec, []byte("x"))
		t.Setenv("TEST_TOKEN_ID", "")
		t.Setenv("TEST_TOKEN_SECRET", "")
		_, err := lookup(t, tool)(context.Background(), exportResolved(server.URL, dir, "book/7"), resolver(nil), nil, exportArgs("book", 9, "html", path("foreign")))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "outside the books") || strings.Contains(err.Error(), "9") || len(exportPaths(rec)) != 0 {
			t.Errorf("%s foreign book: err = %v, requests = %v", tool, err, exportPaths(rec))
		}
		t.Setenv("TEST_TOKEN_ID", canaryID)
		t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	}
	if _, err := os.Stat(filepath.Join(dir, "foreign")); err == nil {
		t.Error("a refused download created a file")
	}
}

func TestExportHidesProviderText(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	server := exportServer(t, &recorder{}, nil)
	dir := t.TempDir()
	for tool, args := range map[string]json.RawMessage{
		contentExport.ID:   exportArgs("page", 403, "html", ""),
		contentDownload.ID: exportArgs("page", 403, "pdf", filepath.Join(dir, "x.pdf")),
	} {
		_, err := lookup(t, tool)(context.Background(), exportResolved(server.URL, dir), resolver(nil), nil, args)
		if err == nil || strings.Contains(err.Error(), exportCanary) || !strings.Contains(err.Error(), "role permission") {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x.pdf")); err == nil {
		t.Error("a failed download left a file")
	}
}

func TestExportDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{contentExport, contentDownload} {
		if d.Risk != bookstackReadRisk || d.RequiresToolAllowList || !strings.Contains(d.Description, "content-export") {
			t.Errorf("%s: %+v", d.ID, d)
		}
	}
	if contentExport.LocalFiles != "" || contentDownload.LocalFiles != config.LocalFilesWrite ||
		!strings.Contains(contentDownload.Description, "attachments and images") {
		t.Error("local files or zip hint wrong")
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, d := range reg.Provider(Provider) {
		if (d.ID == contentExport.ID || d.ID == contentDownload.ID) && d.Group != contentGroup {
			t.Errorf("%s group = %q", d.ID, d.Group)
		}
	}
}
