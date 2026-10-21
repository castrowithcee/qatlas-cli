package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func row(id int64, name, slug string, extra map[string]any) map[string]any {
	m := map[string]any{"id": id, "name": name, "slug": slug}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// choiceServer answers the lists the target choice reads. Books 1..120 exist; book 5 has chapter 20 with
// page 30 and a page 31 without chapter.
func choiceServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		q := r.URL.Query()
		list := func(rows []map[string]any, total int) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "total": total})
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		switch r.URL.Path {
		case "/api/shelves":
			if q.Get("filter[slug]") == "wiki" {
				list([]map[string]any{row(2, "Wiki", "wiki", nil)}, 1)
				return
			}
			if q.Get("filter[slug]") != "" {
				list(nil, 0)
				return
			}
			list([]map[string]any{row(2, "Wiki\x00", "wiki", nil)}, 1)
		case "/api/shelves/2":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "name": "Wiki", "books": []map[string]any{row(5, "Guide", "guide", nil)}})
		case "/api/books":
			switch q.Get("filter[slug]") {
			case "guide":
				list([]map[string]any{row(5, "Guide", "guide", nil)}, 1)
				return
			case "":
			default:
				list(nil, 0)
				return
			}
			var rows []map[string]any
			for id := offset + 1; id <= 120 && len(rows) < 50; id++ {
				rows = append(rows, row(int64(id), "Book "+strconv.Itoa(id), "b"+strconv.Itoa(id), nil))
			}
			list(rows, 120)
		case "/api/books/5":
			_ = json.NewEncoder(w).Encode(row(5, "Guide", "guide", nil))
		case "/api/chapters/20":
			_ = json.NewEncoder(w).Encode(row(20, "Intro", "intro", map[string]any{"book_id": 5}))
		case "/api/chapters":
			if q.Get("filter[book_id]") != "5" {
				list(nil, 0)
				return
			}
			if s := q.Get("filter[slug]"); s != "" && s != "intro" {
				list(nil, 0)
				return
			}
			list([]map[string]any{row(20, "Intro", "intro", map[string]any{"book_id": 5})}, 1)
		case "/api/pages":
			pages := []map[string]any{
				row(30, "In chapter", "in-chapter", map[string]any{"book_id": 5, "chapter_id": 20}),
				row(31, "Loose", "loose", map[string]any{"book_id": 5, "chapter_id": 0}),
			}
			var out []map[string]any
			for _, p := range pages {
				if q.Get("filter[chapter_id]") == "20" && p["chapter_id"] != 20 ||
					q.Get("filter[chapter_id]") == "0" && p["chapter_id"] != 0 ||
					q.Get("filter[slug]") != "" && q.Get("filter[slug]") != p["slug"] {
					continue
				}
				out = append(out, p)
			}
			list(out, len(out))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, rec
}

func choiceConnection(t *testing.T, baseURL string, targets ...string) (*config.Resolved, *redact.Redactor) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	red := &redact.Redactor{}
	return &config.Resolved{
		Name: "wiki", Provider: Provider, BaseURL: baseURL, Targets: targets,
		Secrets: envCredential(map[string]string{roleTokenID: "TEST_TOKEN_ID", roleTokenSecret: "TEST_TOKEN_SECRET"}),
	}, red
}

func list(t *testing.T, baseURL string, req config.TargetListRequest, targets ...string) config.TargetPage {
	t.Helper()
	resolved, red := choiceConnection(t, baseURL, targets...)
	got, err := ListTargets(context.Background(), resolved, resolver(red), red, req)
	if err != nil {
		t.Fatalf("ListTargets(%+v) = %v", req, err)
	}
	return got
}

func TestListTargetsLevels(t *testing.T) {
	server, rec := choiceServer(t)
	// The connection is bound to a page; the choice still reads everything.
	for _, tt := range []struct {
		name string
		req  config.TargetListRequest
		want []config.TargetEntry
	}{
		{"shelves", config.TargetListRequest{Kind: "shelf"},
			[]config.TargetEntry{{Target: "shelf/2", Name: "Wiki", Kind: "shelf", HasChildren: true}}},
		{"shelf books", config.TargetListRequest{Kind: "book", Parent: "shelf/2"},
			[]config.TargetEntry{{Target: "book/5", Name: "Guide", Kind: "book", Path: "/Wiki", HasChildren: true}}},
		{"book chapters", config.TargetListRequest{Kind: "chapter", Parent: "book/5"},
			[]config.TargetEntry{{Target: "chapter/20", Name: "Intro", Kind: "chapter", Path: "/Guide", HasChildren: true}}},
		{"book loose pages", config.TargetListRequest{Kind: "page", Parent: "book/5"},
			[]config.TargetEntry{{Target: "page/31", Name: "Loose", Kind: "page", Path: "/Guide"}}},
		{"chapter pages", config.TargetListRequest{Kind: "page", Parent: "chapter/20"},
			[]config.TargetEntry{{Target: "page/30", Name: "In chapter", Kind: "page", Path: "/Guide/Intro"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := list(t, server.URL, tt.req, "page/99")
			if len(got.Entries) != len(tt.want) || got.Entries[0] != tt.want[0] || got.Next != "" {
				t.Errorf("page = %+v, want %+v", got, tt.want)
			}
		})
	}
	_, _, queries, auth := rec.snapshot()
	for _, q := range queries {
		if strings.Contains(q, "page%2F99") {
			t.Errorf("query %q carries a target", q)
		}
	}
	for _, a := range auth {
		if a != "Token "+canaryID+":"+canarySecret {
			t.Errorf("authorization = %q", a)
		}
	}
}

func TestListTargetsTopLevelAndPaging(t *testing.T) {
	server, _ := choiceServer(t)
	first := list(t, server.URL, config.TargetListRequest{Kind: "book"})
	if len(first.Entries) != 50 || first.Next != "50" || first.Entries[0].Target != "book/1" || first.Entries[0].Path != "" {
		t.Fatalf("first = %d entries, next %q", len(first.Entries), first.Next)
	}
	second := list(t, server.URL, config.TargetListRequest{Kind: "book", Cursor: first.Next})
	third := list(t, server.URL, config.TargetListRequest{Kind: "book", Cursor: second.Next})
	if second.Entries[0].Target != "book/51" || second.Next != "100" || len(third.Entries) != 20 || third.Next != "" {
		t.Errorf("second %q/%q, third %d/%q", second.Entries[0].Target, second.Next, len(third.Entries), third.Next)
	}
}

func TestListTargetsNameIsText(t *testing.T) {
	server, _ := choiceServer(t)
	got := list(t, server.URL, config.TargetListRequest{Kind: "shelf"})
	if got.Entries[0].Name != "Wiki" {
		t.Errorf("name = %q", got.Entries[0].Name)
	}
}

func TestListTargetsRefusals(t *testing.T) {
	server, _ := choiceServer(t)
	resolved, red := choiceConnection(t, server.URL)
	for _, req := range []config.TargetListRequest{
		{Kind: "bogus"},
		{Kind: "shelf", Parent: "book/5"},
		{Kind: "chapter", Parent: "shelf/2"},
		{Kind: "page", Parent: "shelf/2"},
		{Kind: "book", Parent: "shelf/0"},
		{Kind: "book", Cursor: "0"},
		{Kind: "book", Cursor: "x"},
		{Kind: "book", Cursor: "05"},
	} {
		if _, err := ListTargets(context.Background(), resolved, resolver(red), red, req); err == nil {
			t.Errorf("ListTargets(%+v) accepted", req)
		}
	}
}

func TestResolveTargetURL(t *testing.T) {
	server, rec := choiceServer(t)
	resolved, red := choiceConnection(t, server.URL)
	for _, tt := range []struct {
		name, url, want string
	}{
		{"shelf", server.URL + "/shelves/wiki", "shelf/2"},
		{"book", server.URL + "/books/guide", "book/5"},
		{"chapter", server.URL + "/books/guide/chapter/intro", "chapter/20"},
		{"page", server.URL + "/books/guide/page/loose?x=1#y", "page/31"},
		{"page in chapter", server.URL + "/books/guide/page/in-chapter", "page/30"},
		{"other host", "https://other.example/books/guide", ""},
		{"http scheme", strings.Replace(server.URL, "https", "http", 1) + "/books/guide", ""},
		{"unknown shelf", server.URL + "/shelves/nope", ""},
		{"unknown book", server.URL + "/books/nope", ""},
		{"unknown chapter", server.URL + "/books/guide/chapter/nope", ""},
		{"unknown page", server.URL + "/books/guide/page/nope", ""},
		{"book list", server.URL + "/books", ""},
		{"other form", server.URL + "/books/guide/edit", ""},
		{"user info", strings.Replace(server.URL, "https://", "https://u:p@", 1) + "/books/guide", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, _, _, _ := rec.snapshot()
			got, err := ResolveTargetURL(context.Background(), resolved, resolver(red), red, "", tt.url)
			if err != nil {
				t.Fatalf("ResolveTargetURL() = %v", err)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("entries = %+v", got)
				}
				if after, _, _, _ := rec.snapshot(); strings.HasPrefix(tt.name, "other") && len(after) != len(before) {
					t.Error("a request was sent for another host")
				}
				return
			}
			if len(got) != 1 || got[0].Target != tt.want {
				t.Errorf("entries = %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveTargetURLInstallPath(t *testing.T) {
	server, _ := choiceServer(t)
	resolved, red := choiceConnection(t, server.URL+"/wiki")
	// The test server answers the same paths whatever the prefix is not; a URL outside the installation path
	// is refused before any request.
	got, err := ResolveTargetURL(context.Background(), resolved, resolver(red), red, "", server.URL+"/books/guide")
	if err != nil || len(got) != 0 {
		t.Errorf("outside install path = %+v, %v", got, err)
	}
}
