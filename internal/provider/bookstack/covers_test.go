package bookstack

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

type coverServer struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []string
	changes []sentChange
}

func newCoverServer(t *testing.T) *coverServer {
	t.Helper()
	s := &coverServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sentChange{Method: r.Method, Path: r.URL.Path}
		media, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch media {
		case "multipart/form-data":
			reader := multipart.NewReader(r.Body, params["boundary"])
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
		case "application/json":
			_ = json.NewDecoder(r.Body).Decode(&got.JSON)
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
		s.changes = append(s.changes, got)
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "name": "N", "slug": "n"})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *coverServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func TestCoverToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{booksSetCover, shelvesSetCover} {
		desc, _, ok := reg.Lookup(d.ID)
		if !ok {
			t.Fatalf("%s not registered", d.ID)
		}
		risk := desc.Risk
		if desc.Group != "content" || risk.Effect != capability.EffectUpdate || risk.Idempotency != capability.IdempotencyNonIdempotent ||
			risk.Confirmation != capability.ConfirmationRequired || desc.LocalFiles != config.LocalFilesRead || desc.RequiresToolAllowList {
			t.Errorf("%s: group %q risk %+v", d.ID, desc.Group, risk)
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("%s is in profile %s", id, profile.ID)
				}
			}
		}
	}
}

func TestSetCoverSendsMultipartWithMethodOverride(t *testing.T) {
	dir, path := imageFile(t, "cover.png", "png-bytes")
	for _, tt := range []struct{ op, request string }{
		{booksSetCover.ID, "POST /api/books/7"}, {shelvesSetCover.ID, "POST /api/shelves/7"},
	} {
		server := newCoverServer(t)
		args := jsonArgs(t, map[string]any{"id": 7, "local_path": path})
		var targets []string
		if tt.op == booksSetCover.ID {
			targets = []string{"book/7"}
		}
		if _, err := writeCall(t, tt.op, server.URL, args, dir, targets...); err != nil {
			t.Fatal(err)
		}
		sent := server.changes[0]
		if !reflect.DeepEqual(server.requests(), []string{tt.request}) || !reflect.DeepEqual(sent.Fields, map[string]string{"_method": "PUT"}) ||
			sent.FileContent != "png-bytes" || sent.FileName != "cover.png" {
			t.Errorf("%s: requests = %v, sent = %+v", tt.op, server.requests(), sent)
		}
	}
}

func TestRemoveCoverSendsImageNull(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		want           map[string]any
		request        string
		targets        []string
	}{
		{"book alone", booksUpdate.ID, `{"id":7,"remove_cover":true}`, map[string]any{"image": nil}, "PUT /api/books/7", []string{"book/7"}},
		{"book combined", booksUpdate.ID, `{"id":7,"name":"N","remove_cover":true}`, map[string]any{"name": "N", "image": nil}, "PUT /api/books/7", nil},
		{"book false", booksUpdate.ID, `{"id":7,"name":"N","remove_cover":false}`, map[string]any{"name": "N"}, "PUT /api/books/7", nil},
		{"shelf alone", shelvesUpdate.ID, `{"id":7,"remove_cover":true}`, map[string]any{"image": nil}, "PUT /api/shelves/7", nil},
		{"shelf combined", shelvesUpdate.ID, `{"id":7,"books":[1],"remove_cover":true}`, map[string]any{"books": []any{1.0}, "image": nil}, "PUT /api/shelves/7", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newCoverServer(t)
			if _, err := writeCall(t, tt.op, server.URL, tt.args, "", tt.targets...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(server.changes[0].JSON, tt.want) || !reflect.DeepEqual(server.requests(), []string{tt.request}) {
				t.Errorf("body = %v, requests = %v", server.changes[0].JSON, server.requests())
			}
		})
	}
	// remove_cover false alone changes nothing and is refused.
	server := newCoverServer(t)
	if _, err := writeCall(t, booksUpdate.ID, server.URL, `{"id":7,"remove_cover":false}`, ""); !isInvalidRequest(err) || len(server.requests()) != 0 {
		t.Errorf("err = %v", err)
	}
}

func TestCoverBindingAndGateRefuseBeforeSecretsAndIO(t *testing.T) {
	dir, png := imageFile(t, "a.png", "x")
	_, txt := imageFile(t, "a.txt", "x")
	server := newCoverServer(t)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for name, call := range map[string]struct {
		op, args string
		targets  []string
	}{
		"foreign book setcover":   {booksSetCover.ID, jsonArgs(t, map[string]any{"id": 9, "local_path": png}), []string{"book/7"}},
		"foreign book remove":     {booksUpdate.ID, `{"id":9,"remove_cover":true}`, []string{"book/7"}},
		"bound shelf setcover":    {shelvesSetCover.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": png}), []string{"book/7"}},
		"bound shelf remove":      {shelvesUpdate.ID, `{"id":3,"remove_cover":true}`, []string{"book/7"}},
		"book setcover bad type":  {booksSetCover.ID, jsonArgs(t, map[string]any{"id": 7, "local_path": txt}), []string{"book/7"}},
		"shelf setcover bad type": {shelvesSetCover.ID, jsonArgs(t, map[string]any{"id": 3, "local_path": txt}), nil},
	} {
		resolved := boundResolved(server.URL, call.targets...)
		resolved.Files = config.Files{Read: []string{dir, filepath.Dir(txt)}}
		_, err := lookup(t, call.op)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(call.args))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
