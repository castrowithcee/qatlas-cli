package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

func hit(typ string, id, book, chapter int64) map[string]any {
	return map[string]any{
		"type": typ, "id": id, "book_id": book, "chapter_id": chapter, "name": "N", "slug": "n",
		"url": "https://wiki.test/x", "extra": "dropped",
		"tags":         []map[string]any{{"name": "a", "value": "b", "order": 1}},
		"preview_html": map[string]any{"name": "<b>N</b>", "content": "<p>c</p>"},
	}
}

func searchServer(t *testing.T, rec *recorder, hits []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasPrefix(r.URL.Path, "/api/chapters/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/chapters/")
			book := 7
			if id == "20" {
				book = 9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "book_id": book})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": hits, "total": 42})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runSearch(t *testing.T, _ any, base string, args string, targets ...string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	_, handler, ok := reg.Lookup(contentSearch.ID)
	if !ok {
		t.Fatal("search not registered")
	}
	return handler(context.Background(), boundResolved(base, targets...), resolver(nil), nil, json.RawMessage(args))
}

func TestSearchEndpointsAndEncoding(t *testing.T) {
	for _, tt := range []struct{ args, path string }{
		{`{"query":"x"}`, "/api/search"},
		{`{"query":"x","book_id":7}`, "/api/search/book/7"},
		{`{"query":"x","chapter_id":10}`, "/api/search/chapter/10"},
	} {
		rec := &recorder{}
		srv := searchServer(t, rec, nil)
		if _, err := runSearch(t, nil, srv.URL, tt.args); err != nil {
			t.Fatal(err)
		}
		_, paths, _, _ := rec.snapshot()
		if len(paths) != 1 || paths[0] != tt.path {
			t.Errorf("%s: paths = %v, want %s", tt.args, paths, tt.path)
		}
	}

	rec := &recorder{}
	srv := searchServer(t, rec, nil)
	q := `a&b=c {type:page} [tag=v] x`
	body, _ := json.Marshal(map[string]any{"query": q, "page": 2, "count": 5})
	if _, err := runSearch(t, nil, srv.URL, string(body)); err != nil {
		t.Fatal(err)
	}
	_, _, queries, _ := rec.snapshot()
	values, err := url.ParseQuery(queries[0])
	if err != nil || len(values) != 3 || values.Get("query") != q || values.Get("page") != "2" || values.Get("count") != "5" {
		t.Errorf("query = %q, values = %v", queries[0], values)
	}
	if strings.ContainsAny(queries[0], " {[") {
		t.Errorf("raw query not encoded: %q", queries[0])
	}
}

func TestSearchBounds(t *testing.T) {
	rec := &recorder{}
	srv := searchServer(t, rec, nil)
	for _, args := range []string{
		`{"query":""}`, `{"query":"` + strings.Repeat("a", 1001) + `"}`,
		`{"query":"x","page":0}`, `{"query":"x","count":0}`, `{"query":"x","count":101}`,
		`{"query":"x","book_id":1,"chapter_id":2}`,
	} {
		if _, err := runSearch(t, nil, srv.URL, args); !isInvalidRequest(err) {
			t.Errorf("%.40s: err = %v, want invalid-request", args, err)
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
	if _, err := runSearch(t, nil, srv.URL, `{"query":"`+strings.Repeat("ä", 1000)+`","count":100}`); err != nil {
		t.Errorf("limits: %v", err)
	}
}

func TestSearchBinding(t *testing.T) {
	t.Run("foreign book without request", func(t *testing.T) {
		rec := &recorder{}
		srv := searchServer(t, rec, nil)
		_, err := runSearch(t, nil, srv.URL, `{"query":"x","book_id":9}`, "book/7")
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
			t.Errorf("err = %v", err)
		}
		if m, _, _, _ := rec.snapshot(); len(m) != 0 {
			t.Errorf("requests = %v", m)
		}
	})
	t.Run("foreign chapter after one read", func(t *testing.T) {
		rec := &recorder{}
		srv := searchServer(t, rec, nil)
		_, err := runSearch(t, nil, srv.URL, `{"query":"x","chapter_id":20}`, "book/7")
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "20") {
			t.Errorf("err = %v", err)
		}
		if _, paths, _, _ := rec.snapshot(); len(paths) != 1 || paths[0] != "/api/chapters/20" {
			t.Errorf("paths = %v", paths)
		}
	})
	t.Run("several books need a book", func(t *testing.T) {
		rec := &recorder{}
		srv := searchServer(t, rec, nil)
		_, err := runSearch(t, nil, srv.URL, `{"query":"x"}`, "book/7", "book/9")
		if !isInvalidRequest(err) {
			t.Errorf("err = %v", err)
		}
		if m, _, _, _ := rec.snapshot(); len(m) != 0 {
			t.Errorf("requests = %v", m)
		}
	})
	t.Run("one book is the default", func(t *testing.T) {
		rec := &recorder{}
		srv := searchServer(t, rec, nil)
		if _, err := runSearch(t, nil, srv.URL, `{"query":"x"}`, "book/7"); err != nil {
			t.Fatal(err)
		}
		if _, paths, _, _ := rec.snapshot(); len(paths) != 1 || paths[0] != "/api/search/book/7" {
			t.Errorf("paths = %v", paths)
		}
	})
	t.Run("foreign and shelf hits are dropped", func(t *testing.T) {
		rec := &recorder{}
		hits := []map[string]any{
			hit("page", 1, 7, 0), hit("page", 2, 9, 0), hit("bookshelf", 3, 0, 0),
			hit("book", 7, 0, 0), hit("book", 9, 0, 0), hit("page", 4, 0, 0), hit("chapter", 5, 7, 0),
		}
		srv := searchServer(t, rec, hits)
		got, err := runSearch(t, nil, srv.URL, `{"query":"x"}`, "book/7")
		if err != nil {
			t.Fatal(err)
		}
		results := got.(output.Object).Fields[0].Value.([]map[string]any)
		var idsGot []int64
		for _, r := range results {
			idsGot = append(idsGot, r["id"].(int64))
		}
		if len(idsGot) != 3 || idsGot[0] != 1 || idsGot[1] != 7 || idsGot[2] != 5 {
			t.Errorf("ids = %v", idsGot)
		}
	})
	t.Run("chapter search drops hits of other chapters", func(t *testing.T) {
		rec := &recorder{}
		srv := searchServer(t, rec, []map[string]any{hit("page", 1, 7, 10), hit("page", 2, 7, 11)})
		got, err := runSearch(t, nil, srv.URL, `{"query":"x","chapter_id":10}`, "book/7")
		if err != nil {
			t.Fatal(err)
		}
		if n := len(got.(output.Object).Fields[0].Value.([]map[string]any)); n != 1 {
			t.Errorf("results = %d, want 1", n)
		}
	})
}

func TestSearchResultShapeAndCaps(t *testing.T) {
	h := hit("page", 1, 7, 0)
	var tags []map[string]any
	for i := 0; i < 60; i++ {
		tags = append(tags, map[string]any{"name": "n", "value": "v"})
	}
	h["tags"] = tags
	h["preview_html"] = map[string]any{"name": strings.Repeat("ä", 3000), "content": strings.Repeat("c", 3000)}
	rec := &recorder{}
	srv := searchServer(t, rec, []map[string]any{h})
	got, err := runSearch(t, nil, srv.URL, `{"query":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	obj := got.(output.Object)
	if obj.Fields[1].Name != "total" || obj.Fields[1].Value != 42 {
		t.Errorf("total = %v", obj.Fields[1])
	}
	r := obj.Fields[0].Value.([]map[string]any)[0]
	if len(r["tags"].([]map[string]string)) != 50 {
		t.Errorf("tags = %d", len(r["tags"].([]map[string]string)))
	}
	if len([]rune(r["preview_name"].(string))) != 2000 || len(r["preview_content"].(string)) != 2000 {
		t.Errorf("previews not capped")
	}
	if _, ok := r["extra"]; ok || len(r) != 10 {
		t.Errorf("fields = %v", r)
	}
}
