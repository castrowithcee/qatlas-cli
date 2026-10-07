package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

var testUser = map[string]any{"id": 3, "name": "Editor", "slug": "editor", "email": "hidden"}

// booksServer serves books 7 and 9 with chapters 10 (book 7) and 20 (book 9). With ignoreFilter set, the
// chapter list answers like an instance that does not know the filter.
func booksServer(t *testing.T, rec *recorder, ignoreFilter bool) *httptest.Server {
	t.Helper()
	books := []map[string]any{
		{"id": 5, "name": "Other", "slug": "other", "description": "d", "created_at": "c", "updated_at": "u"},
		{"id": 7, "name": "Own", "slug": "own", "description": "d", "created_at": "c", "updated_at": "u"},
		{"id": 9, "name": "Foreign", "slug": "foreign", "description": "d", "created_at": "c", "updated_at": "u"},
	}
	chapters := []map[string]any{
		{"id": 10, "book_id": 7, "name": "Own chapter", "slug": "oc"},
		{"id": 20, "book_id": 9, "name": "Foreign chapter", "slug": "fc"},
		{"id": 11, "book_id": 7, "name": "Own second", "slug": "os"},
	}
	window := func(w http.ResponseWriter, r *http.Request, rows []map[string]any) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		end := min(offset+count, len(rows))
		out := []map[string]any{}
		if offset < end {
			out = rows[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "total": len(rows)})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/books", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		window(w, r, books)
	})
	mux.HandleFunc("/api/books/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 7, "name": "Own", "slug": "own", "description": "d", "description_html": "<p>d</p>",
			"created_by": testUser, "updated_by": testUser, "owned_by": testUser,
			"default_template_id": nil,
			"tags":                []map[string]any{{"name": "k", "value": "v", "order": 0}},
			"shelves":             []map[string]any{{"id": 1, "name": "Shelf", "slug": "shelf"}},
			"contents": []map[string]any{
				{"id": 10, "type": "chapter", "name": "C", "slug": "c", "book_id": 7, "pages": []map[string]any{
					{"id": 1, "name": "P1", "slug": "p1", "book_id": 7, "chapter_id": 10},
				}},
				{"id": 2, "type": "page", "name": "P2", "slug": "p2", "book_id": 7, "chapter_id": nil},
			},
		})
	})
	mux.HandleFunc("/api/chapters", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		var rows []map[string]any
		for _, c := range chapters {
			if v := r.URL.Query().Get("filter[book_id]"); !ignoreFilter && v != "" && v != strconv.Itoa(c["book_id"].(int)) {
				continue
			}
			rows = append(rows, c)
		}
		window(w, r, rows)
	})
	mux.HandleFunc("/api/chapters/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch strings.TrimPrefix(r.URL.Path, "/api/chapters/") {
		case "10":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 10, "book_id": 7, "book_slug": "own", "name": "Own chapter", "slug": "oc",
				"created_by": testUser, "updated_by": 3, "owned_by": testUser,
				"pages": []map[string]any{{"id": 1, "name": "P1", "slug": "p1", "book_id": 7, "chapter_id": 10, "created_by": 3}},
			})
		case "20":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 20, "book_id": 9, "name": "SECRET-FOREIGN", "book_slug": "secret-slug"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func fieldMap(o output.Object) map[string]any {
	m := map[string]any{}
	for _, f := range o.Fields {
		m[f.Name] = f.Value
	}
	return m
}

func TestBooksListPaginatesAndStopsWithoutProgress(t *testing.T) {
	rec := &recorder{}
	c := newClient(t, booksServer(t, rec, false).URL, nil)
	got, err := c.ListBooks(context.Background(), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{7, 9}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v", ids(got.Rows), want)
	}
	_, _, queries, _ := rec.snapshot()
	if len(queries) != 1 || !strings.Contains(queries[0], "count=2") || !strings.Contains(queries[0], "offset=1") ||
		!strings.Contains(queries[0], "sort=%2Bid") {
		t.Errorf("queries = %v", queries)
	}

	var requests int
	stuck := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 9, "data": []map[string]any{{"id": 1}, {"id": 2}}})
	}))
	defer stuck.Close()
	got, err = newClient(t, stuck.URL, nil).ListBooks(context.Background(), 0, 0)
	if err != nil || len(got.Rows) != 2 || requests > 2 {
		t.Errorf("rows = %d, requests = %d, err = %v", len(got.Rows), requests, err)
	}
}

func TestBooksListFiltersForeignBooks(t *testing.T) {
	rec := &recorder{}
	c := boundClient(t, booksServer(t, rec, false).URL, "book/7")
	got, err := c.ListBooks(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{7}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v", ids(got.Rows), want)
	}
}

func TestBooksGetRefusesForeignBookBeforeSecretsAndIO(t *testing.T) {
	rec := &recorder{}
	server := booksServer(t, rec, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	resolved := boundResolved(server.URL, "book/7")
	for _, tt := range []struct{ id, args string }{
		{booksGet.ID, `{"id":9}`},
		{chaptersList.ID, `{"book_id":9}`},
	} {
		_, handler, _ := reg.Lookup(tt.id)
		_, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(tt.args))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "outside the books") || strings.Contains(err.Error(), "9") {
			t.Errorf("%s: err = %v", tt.id, err)
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func TestBooksGetTreeUsersAndShelves(t *testing.T) {
	rec := &recorder{}
	server := booksServer(t, rec, false)

	got, err := newClient(t, server.URL, nil).GetBook(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	m := fieldMap(got)
	if !reflect.DeepEqual(m["created_by"], map[string]any{"id": int64(3), "name": "Editor"}) {
		t.Errorf("created_by = %v", m["created_by"])
	}
	if _, ok := m["shelves"]; !ok {
		t.Error("an unbound connection should show shelves")
	}
	contents := m["contents"].([]map[string]any)
	if len(contents) != 2 || contents[0]["type"] != "chapter" || len(contents[0]["pages"].([]map[string]any)) != 1 ||
		contents[1]["type"] != "page" || m["truncated"] != false || m["default_template_id"] != int64(0) {
		t.Errorf("contents = %v", contents)
	}
	if _, paths, _, _ := rec.snapshot(); !reflect.DeepEqual(paths, []string{"/api/books/7"}) {
		t.Errorf("paths = %v", paths)
	}

	bound, err := boundClient(t, server.URL, "book/7").GetBook(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fieldMap(bound)["shelves"]; ok {
		t.Error("a bound connection must not show shelves")
	}
}

func TestBooksGetTreeIsCapped(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var contents []map[string]any
		for i := 1; i <= maxTreeEntries+5; i++ {
			contents = append(contents, map[string]any{"id": i, "type": "page", "name": "P"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "contents": contents})
	}))
	defer server.Close()
	got, err := newClient(t, server.URL, nil).GetBook(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	m := fieldMap(got)
	if n := len(m["contents"].([]map[string]any)); n != maxTreeEntries || m["truncated"] != true {
		t.Errorf("entries = %d, truncated = %v", n, m["truncated"])
	}

	// Chapter pages count against the same budget.
	chapter := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var pages []map[string]any
		for i := 1; i <= maxTreeEntries+1; i++ {
			pages = append(pages, map[string]any{"id": i, "name": "P"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": 7, "pages": pages})
	}))
	defer chapter.Close()
	got, err = newClient(t, chapter.URL, nil).GetChapter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	m = fieldMap(got)
	if n := len(m["pages"].([]map[string]any)); n != maxTreeEntries || m["truncated"] != true {
		t.Errorf("pages = %d, truncated = %v", n, m["truncated"])
	}
}

func TestChaptersGetIsOneRequestAndRefusesForeign(t *testing.T) {
	rec := &recorder{}
	c := boundClient(t, booksServer(t, rec, false).URL, "book/7")
	got, err := c.GetChapter(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	m := fieldMap(got)
	if m["book_slug"] != "own" || !reflect.DeepEqual(m["owned_by"], map[string]any{"id": int64(3), "name": "Editor"}) {
		t.Errorf("chapter = %v", m)
	}
	if _, paths, _, _ := rec.snapshot(); !reflect.DeepEqual(paths, []string{"/api/chapters/10"}) {
		t.Errorf("paths = %v, want one request", paths)
	}

	rec2 := &recorder{}
	c2 := boundClient(t, booksServer(t, rec2, false).URL, "book/7")
	_, err = c2.GetChapter(context.Background(), 20)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "20") {
		t.Errorf("foreign chapter: err = %v", err)
	}
	if _, paths, _, _ := rec2.snapshot(); len(paths) != 1 {
		t.Errorf("paths = %v, want exactly one read", paths)
	}
}

func TestChaptersListBookRuleAndClientFilter(t *testing.T) {
	rec := &recorder{}
	server := booksServer(t, rec, true)
	c := boundClient(t, server.URL, "book/7")
	got, err := c.ListChapters(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{10, 11}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v (the ignored filter must not leak chapter 20)", ids(got.Rows), want)
	}
	if _, _, queries, _ := rec.snapshot(); len(queries) == 0 || !strings.Contains(queries[0], "filter%5Bbook_id%5D=7") {
		t.Errorf("queries = %v", queries)
	}

	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	reg := capability.NewRegistry()
	_ = Register(reg)
	_, handler, _ := reg.Lookup(chaptersList.ID)
	_, err = handler(context.Background(), &config.Resolved{Name: "wiki", Provider: Provider, BaseURL: server.URL,
		Targets: []string{"book/7", "book/9"}}, resolver(nil), nil, json.RawMessage(`{}`))
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "book_id is required for a connection bound to several books") {
		t.Errorf("err = %v", err)
	}

	unbound, err := newClient(t, server.URL, nil).ListChapters(context.Background(), 0, 0, 0)
	if err != nil || len(unbound.Rows) != 3 {
		t.Errorf("unbound rows = %d, err = %v", len(unbound.Rows), err)
	}
}

func TestEveryToolHasAGroupTheRegistryAccepts(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	for _, d := range reg.Provider(Provider) {
		want := "content"
		if d.ID == systemGet.ID || d.ID == contentPermissionsGet.ID || d.ID == contentPermissionsUpdate.ID {
			want = "administration"
		}
		if strings.HasPrefix(d.ID, Provider+".comments.") {
			want = "comments"
		}
		if strings.HasPrefix(d.ID, Provider+".attachments.") || strings.HasPrefix(d.ID, Provider+".images.") {
			want = "files"
		}
		if d.Group != want {
			t.Errorf("%s group = %q", d.ID, d.Group)
		}
	}
}
