package bookstack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func containerDeleteDescriptors() []capability.Descriptor {
	return []capability.Descriptor{booksDelete, chaptersDelete, shelvesDelete}
}

func TestContainerDeletesAreGatedAndDescribeTheRecycleBin(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, d := range containerDeleteDescriptors() {
		if !d.RequiresToolAllowList || d.Risk.Effect != capability.EffectDelete ||
			d.Risk.Confirmation != capability.ConfirmationRequired || d.Risk.Idempotency != capability.IdempotencyIdempotent {
			t.Errorf("%s: gate or risk wrong: %+v", d.ID, d)
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, d.ID)
				}
			}
		}
	}
	for _, d := range []capability.Descriptor{booksDelete, chaptersDelete} {
		if !strings.Contains(d.Description, "recycle bin") || !strings.Contains(d.Description, "RECYCLE_BIN_LIFETIME") ||
			!strings.Contains(d.Description, "admin") {
			t.Errorf("%s description = %q", d.ID, d.Description)
		}
	}
	if !strings.Contains(shelvesDelete.Description, "books stay") {
		t.Errorf("shelves.delete description = %q", shelvesDelete.Description)
	}
}

func deleteServer(rec *recorder, status int) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/chapters/10":
				_, _ = w.Write([]byte(`{"id":10,"book_id":7}`))
			case "/api/chapters/20":
				_, _ = w.Write([]byte(`{"id":20,"book_id":9}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		w.WriteHeader(status)
	}))
}

func requests(rec *recorder) []string {
	methods, paths, _, _ := rec.snapshot()
	out := []string{}
	for i := range methods {
		out = append(out, methods[i]+" "+paths[i])
	}
	return out
}

func TestContainerDeletesSendExactlyOneDelete(t *testing.T) {
	rec := &recorder{}
	server := deleteServer(rec, http.StatusNoContent)
	defer server.Close()
	c := newClient(t, server.URL, nil)
	if err := c.DeleteBook(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteChapter(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteShelf(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	want := []string{"DELETE /api/books/7", "DELETE /api/chapters/10", "DELETE /api/shelves/3"}
	if got := requests(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestBoundContainerDeletes(t *testing.T) {
	rec := &recorder{}
	server := deleteServer(rec, http.StatusNoContent)
	defer server.Close()
	c := boundClient(t, server.URL, "book/7")
	if err := c.DeleteBook(context.Background(), 9); !isInvalidRequest(err) {
		t.Errorf("foreign book: err = %v", err)
	}
	if err := c.DeleteChapter(context.Background(), 20); !isInvalidRequest(err) {
		t.Errorf("foreign chapter: err = %v", err)
	}
	if got := requests(rec); !reflect.DeepEqual(got, []string{"GET /api/chapters/20"}) {
		t.Errorf("requests = %v, want only the evidence read", got)
	}
	if err := c.DeleteBook(context.Background(), 7); err != nil {
		t.Errorf("own book: err = %v", err)
	}
	if err := c.DeleteChapter(context.Background(), 10); err != nil {
		t.Errorf("own chapter: err = %v", err)
	}
	want := []string{"GET /api/chapters/20", "DELETE /api/books/7", "GET /api/chapters/10", "DELETE /api/chapters/10"}
	if got := requests(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// The handlers refuse before a secret is resolved and before any request is sent.
func TestContainerDeleteHandlersGateBeforeSecretAndIO(t *testing.T) {
	rec := &recorder{}
	server := deleteServer(rec, http.StatusNoContent)
	defer server.Close()
	resolved := boundResolved(server.URL, "book/7")
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	ctx := context.Background()
	if _, err := invokeBooksDelete(ctx, resolved, resolver(nil), nil, []byte(`{"id":9}`)); !isInvalidRequest(err) {
		t.Errorf("books.delete: err = %v", err)
	}
	if _, err := invokeShelvesDelete(ctx, resolved, resolver(nil), nil, []byte(`{"id":1}`)); !isInvalidRequest(err) {
		t.Errorf("shelves.delete: err = %v", err)
	}
	if got := requests(rec); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestContainerDeleteErrors(t *testing.T) {
	const hint = "this change may have taken effect"
	for status, wantHint := range map[int]bool{http.StatusNotFound: false, http.StatusBadGateway: true} {
		rec := &recorder{}
		server := deleteServer(rec, status)
		c := newClient(t, server.URL, nil)
		for name, call := range map[string]func() error{
			"book":    func() error { return c.DeleteBook(context.Background(), 7) },
			"chapter": func() error { return c.DeleteChapter(context.Background(), 10) },
			"shelf":   func() error { return c.DeleteShelf(context.Background(), 3) },
		} {
			err := call()
			var perr *provider.Error
			if !errors.As(err, &perr) {
				t.Fatalf("%s %d: err = %v", name, status, err)
			}
			if status == http.StatusNotFound && perr.Class != provider.ClassNotFound {
				t.Errorf("%s: class = %v, want not-found", name, perr.Class)
			}
			if strings.Contains(err.Error(), hint) != wantHint {
				t.Errorf("%s %d: err = %v, hint wanted %v", name, status, err, wantHint)
			}
		}
		if n := len(requests(rec)); n != 3 {
			t.Errorf("status %d: requests = %d, want 3 (no retry)", status, n)
		}
		server.Close()
	}
}
