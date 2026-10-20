package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// areaServer holds two books with chapters and pages, two shelves, comments, attachments, and a search. Book
// 701 holds chapter 1001 with pages 111 and 555 and the loose page 333; book 901 holds chapter 2001 with page
// 222 and the loose page 444. Shelf 301 holds book 701, shelf 302 nothing. The list routes ignore every filter,
// so only the client check holds the boundary. The identifiers are distinct, so a refusal that quotes one is
// caught.
type areaServer struct {
	*httptest.Server
	rec     *recorder
	mu      sync.Mutex
	shelves map[int64][]int64
	// searchFiller is the number of foreign page hits the instance search returns before the own ones.
	searchFiller int
}

var areaPages = []map[string]any{
	{"id": 111, "book_id": 701, "chapter_id": 1001, "name": "In chapter"},
	{"id": 222, "book_id": 901, "chapter_id": 2001, "name": "Foreign chapter"},
	{"id": 333, "book_id": 701, "chapter_id": 0, "name": "Loose"},
	{"id": 444, "book_id": 901, "chapter_id": 0, "name": "Foreign loose"},
	{"id": 555, "book_id": 701, "chapter_id": 1001, "name": "In chapter two"},
}

var areaChapters = []map[string]any{{"id": 1001, "book_id": 701}, {"id": 2001, "book_id": 901}}

var areaComments = map[string]map[string]any{
	"61": {"id": 61, "commentable_id": 555, "commentable_type": "page", "local_id": 1},
	"62": {"id": 62, "commentable_id": 333, "commentable_type": "page", "local_id": 1},
}

func newAreaServer(t *testing.T) *areaServer {
	t.Helper()
	s := &areaServer{rec: &recorder{}, shelves: map[int64][]int64{301: {701}, 302: {}}}
	mux := http.NewServeMux()
	list := func(w http.ResponseWriter, r *http.Request, rows []map[string]any) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		end := min(offset+count, len(rows))
		window := []map[string]any{}
		if offset < end {
			window = rows[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": window, "total": len(rows)})
	}
	byID := func(rows []map[string]any, id string) map[string]any {
		for _, row := range rows {
			if fmt.Sprint(row["id"]) == id {
				return row
			}
		}
		return nil
	}
	answer := func(w http.ResponseWriter, r *http.Request, rows []map[string]any, prefix string) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			if row := byID(rows, id); row != nil {
				_ = json.NewEncoder(w).Encode(row)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 999, "book_id": 701})
		}
	}
	mux.HandleFunc("/api/pages", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 999, "book_id": 701})
			return
		}
		list(w, r, areaPages)
	})
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		answer(w, r, areaPages, "/api/pages/")
	})
	mux.HandleFunc("/api/chapters", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 999, "book_id": 701})
			return
		}
		list(w, r, areaChapters)
	})
	mux.HandleFunc("/api/chapters/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		answer(w, r, areaChapters, "/api/chapters/")
	})
	books := []map[string]any{{"id": 701, "name": "Own"}, {"id": 901, "name": "Foreign"}, {"id": 1101, "name": "Other"}}
	mux.HandleFunc("/api/books", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		list(w, r, books)
	})
	mux.HandleFunc("/api/books/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		answer(w, r, books, "/api/books/")
	})
	mux.HandleFunc("/api/shelves", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		list(w, r, []map[string]any{{"id": 301}, {"id": 302}})
	})
	mux.HandleFunc("/api/shelves/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/shelves/"), 10, 64)
		s.mu.Lock()
		held, ok := s.shelves[id]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		onShelf := []map[string]any{}
		for _, book := range held {
			onShelf = append(onShelf, map[string]any{"id": book, "name": "B", "slug": "b"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "S", "slug": "s", "books": onShelf})
	})
	mux.HandleFunc("/api/comments/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		if m, ok := areaComments[strings.TrimPrefix(r.URL.Path, "/api/comments/")]; ok {
			_ = json.NewEncoder(w).Encode(m)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/attachments", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		list(w, r, []map[string]any{{"id": 81, "uploaded_to": 111}, {"id": 82, "uploaded_to": 555}, {"id": 83, "uploaded_to": 222}})
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		var hits []map[string]any
		for i := 0; i < s.searchFiller; i++ {
			hits = append(hits, hit("page", int64(5000+i), 901, 0))
		}
		hits = append(hits, s.hits()...)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		start := min((page-1)*count, len(hits))
		end := min(start+count, len(hits))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": hits[start:end], "total": len(hits)})
	})
	mux.HandleFunc("/api/search/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": s.hits(), "total": 4242})
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

// hits is every kind of hit, of both books, as an instance that ignores the search scope answers.
func (s *areaServer) hits() []map[string]any {
	return []map[string]any{
		hit("page", 111, 701, 1001), hit("page", 222, 901, 2001), hit("page", 333, 701, 0),
		hit("page", 555, 701, 1001), hit("chapter", 1001, 701, 0), hit("chapter", 2001, 901, 0),
		hit("book", 701, 0, 0), hit("book", 901, 0, 0), hit("bookshelf", 301, 0, 0),
	}
}

func (s *areaServer) setShelf(id int64, books []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shelves[id] = books
}

// requests returns the method and path of every request since the last call.
func (s *areaServer) requests() []string {
	methods, paths, _, _ := s.rec.snapshot()
	s.rec.mu.Lock()
	s.rec.methods, s.rec.paths, s.rec.queries, s.rec.auth = nil, nil, nil, nil
	s.rec.mu.Unlock()
	out := make([]string, len(methods))
	for i := range methods {
		out[i] = methods[i] + " " + paths[i]
	}
	return out
}

// call runs one tool through its registered handler on a connection with the targets.
func (s *areaServer) call(t *testing.T, tool, args string, targets ...string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	_, handler, ok := reg.Lookup(tool)
	if !ok {
		t.Fatalf("%s is not registered", tool)
	}
	return handler(context.Background(), boundResolved(s.URL, targets...), resolver(nil), nil, json.RawMessage(args))
}

// areaIDs are every identifier of the server; no refusal may quote one.
var areaIDs = []string{"111", "222", "333", "444", "555", "701", "901", "1001", "2001", "301", "302", "61", "62"}

// refused checks an invalid-request refusal that quotes no identifier.
func refused(t *testing.T, what string, err error) {
	t.Helper()
	if !isInvalidRequest(err) {
		t.Errorf("%s: err = %v, want an invalid request", what, err)
		return
	}
	for _, id := range areaIDs {
		if strings.Contains(err.Error(), id) {
			t.Errorf("%s: refusal %q quotes %s", what, err, id)
		}
	}
}

func TestShelfReachesTheBooksOnItAtCallTime(t *testing.T) {
	s := newAreaServer(t)
	if _, err := s.call(t, pagesGet.ID, `{"id":111}`, "shelf/301"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.requests(), []string{"GET /api/pages/111", "GET /api/shelves/301"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	_, err := s.call(t, pagesGet.ID, `{"id":222}`, "shelf/301")
	refused(t, "page of a book off the shelf", err)
	_, err = s.call(t, booksGet.ID, `{"id":901}`, "shelf/301")
	refused(t, "book off the shelf", err)
	if got, want := s.requests(), []string{"GET /api/pages/222", "GET /api/shelves/301", "GET /api/shelves/301"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v: a foreign book is never read", got, want)
	}

	// A list proves its book and filters its rows with one read of the shelf per call.
	got, err := s.call(t, pagesList.ID, `{"book_id":701}`, "shelf/301", "shelf/302")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 333, 555}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("ids = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/shelves/301", "GET /api/pages"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}

	// A book taken off the shelf is out of reach with the next call.
	s.setShelf(301, nil)
	_, err = s.call(t, pagesGet.ID, `{"id":111}`, "shelf/301")
	refused(t, "page after its book left the shelf", err)
	_, err = s.call(t, booksGet.ID, `{"id":701}`, "shelf/301")
	refused(t, "book after it left the shelf", err)
	_, err = s.call(t, pagesList.ID, `{"book_id":701}`, "shelf/301")
	refused(t, "list after the book left the shelf", err)
}

func TestShelfListsItsBooksAndReadsOnlyItself(t *testing.T) {
	s := newAreaServer(t)
	got, err := s.call(t, booksList.ID, `{}`, "shelf/301")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{701}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("books = %v, want %v", resultIDs(t, got), want)
	}
	got, err = s.call(t, booksList.ID, `{}`, "shelf/301", "book/901")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{701, 901}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("books of shelf and book = %v, want %v", resultIDs(t, got), want)
	}
	if _, err := s.call(t, shelvesGet.ID, `{"id":301}`, "shelf/301"); err != nil {
		t.Errorf("reading the bound shelf: %v", err)
	}
	s.requests()
	for _, tt := range []struct{ tool, args string }{
		{shelvesGet.ID, `{"id":302}`},
		{shelvesList.ID, `{}`},
		{tagsList.ID, `{}`},
		{booksCreate.ID, `{"name":"x"}`},
		{chaptersList.ID, `{}`},
		{pagesList.ID, `{}`},
		{contentSearch.ID, `{"query":"x"}`},
	} {
		_, err := s.call(t, tt.tool, tt.args, "shelf/301")
		refused(t, tt.tool, err)
	}
	if got := s.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestShelfSearchKeepsHitsOfItsBooks(t *testing.T) {
	s := newAreaServer(t)
	got, err := s.call(t, contentSearch.ID, `{"query":"x","book_id":701}`, "shelf/301")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 333, 555, 1001, 701}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("hits = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/shelves/301", "GET /api/search/book/701"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestChapterReachesItsPagesOnly(t *testing.T) {
	s := newAreaServer(t)
	const bound = "chapter/1001"
	for _, id := range []string{"111", "555"} {
		if _, err := s.call(t, pagesGet.ID, `{"id":`+id+`}`, bound); err != nil {
			t.Errorf("page %s of the chapter: %v", id, err)
		}
	}
	_, err := s.call(t, pagesGet.ID, `{"id":333}`, bound)
	refused(t, "loose page of the same book", err)
	_, err = s.call(t, pagesGet.ID, `{"id":222}`, bound)
	refused(t, "page of a foreign chapter", err)
	s.requests()

	if _, err := s.call(t, chaptersGet.ID, `{"id":1001}`, bound); err != nil {
		t.Fatal(err)
	}
	got, err := s.call(t, pagesList.ID, `{}`, bound)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 555}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("pages = %v, want %v", resultIDs(t, got), want)
	}
	if _, err := s.call(t, pagesCreate.ID, `{"name":"x","chapter_id":1001,"markdown":"m"}`, bound); err != nil {
		t.Errorf("create in the chapter: %v", err)
	}
	if got, want := s.requests(), []string{"GET /api/chapters/1001", "GET /api/pages", "POST /api/pages"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v: a bound chapter needs no evidence read", got, want)
	}

	// Everything that reaches a whole book, and every change of the chapter itself, is refused without a request.
	for _, tt := range []struct{ tool, args string }{
		{chaptersGet.ID, `{"id":2001}`},
		{booksGet.ID, `{"id":701}`},
		{booksList.ID, `{}`},
		{booksUpdate.ID, `{"id":701,"name":"x"}`},
		{chaptersList.ID, `{}`},
		{chaptersCreate.ID, `{"name":"x","book_id":701}`},
		{chaptersUpdate.ID, `{"id":1001,"name":"x"}`},
		{chaptersDelete.ID, `{"id":1001}`},
		{contentPermissionsGet.ID, `{"type":"chapter","id":1001}`},
		{contentExport.ID, `{"type":"book","id":701,"format":"plaintext"}`},
		{contentExport.ID, `{"type":"chapter","id":2001,"format":"plaintext"}`},
		{pagesList.ID, `{"book_id":701}`},
		{pagesList.ID, `{"chapter_id":2001}`},
		{pagesCreate.ID, `{"name":"x","book_id":701,"markdown":"m"}`},
		{pagesCreate.ID, `{"name":"x","chapter_id":2001,"markdown":"m"}`},
		{tagsList.ID, `{}`},
	} {
		_, err := s.call(t, tt.tool, tt.args, bound)
		refused(t, tt.tool+" "+tt.args, err)
	}
	if got := s.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}

	// A move out of the chapter is refused before any change.
	_, err = s.call(t, pagesUpdate.ID, `{"id":111,"book_id":701}`, bound)
	refused(t, "move into the book", err)
	if _, err := s.call(t, commentsGet.ID, `{"id":61}`, bound); err != nil {
		t.Errorf("comment on a page of the chapter: %v", err)
	}
	_, err = s.call(t, commentsGet.ID, `{"id":62}`, bound)
	refused(t, "comment on a loose page", err)
	for _, r := range s.requests() {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("unexpected change %s", r)
		}
	}
}

func TestPageReachesOnlyItself(t *testing.T) {
	s := newAreaServer(t)
	const bound = "page/111"
	if _, err := s.call(t, pagesGet.ID, `{"id":111}`, bound); err != nil {
		t.Fatal(err)
	}
	if _, err := s.call(t, pagesUpdate.ID, `{"id":111,"name":"x"}`, bound); err != nil {
		t.Fatal(err)
	}
	got, err := s.call(t, pagesList.ID, `{}`, bound)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("pages = %v, want %v", resultIDs(t, got), want)
	}
	got, err = s.call(t, attachmentsList.ID, `{"page_id":111}`, bound)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{81}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("attachments = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/pages/111", "PUT /api/pages/111", "GET /api/pages", "GET /api/attachments"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v: a bound page needs no evidence read", got, want)
	}
	for _, tt := range []struct{ tool, args string }{
		{pagesGet.ID, `{"id":555}`},
		{pagesUpdate.ID, `{"id":555,"name":"x"}`},
		{pagesUpdate.ID, `{"id":111,"chapter_id":1001}`},
		{pagesCreate.ID, `{"name":"x","chapter_id":1001,"markdown":"m"}`},
		{pagesCreate.ID, `{"name":"x","book_id":701,"markdown":"m"}`},
		{chaptersGet.ID, `{"id":1001}`},
		{booksGet.ID, `{"id":701}`},
		{attachmentsList.ID, `{"page_id":555}`},
		{commentsGet.ID, `{"id":61}`},
		{contentExport.ID, `{"type":"page","id":555,"format":"plaintext"}`},
		{shelvesGet.ID, `{"id":301}`},
	} {
		_, err := s.call(t, tt.tool, tt.args, bound)
		refused(t, tt.tool+" "+tt.args, err)
	}
	for _, r := range s.requests() {
		if r != "GET /api/comments/61" {
			t.Errorf("unexpected request %s", r)
		}
	}
}

func TestSearchKeepsHitsInsideChapterAndPage(t *testing.T) {
	s := newAreaServer(t)
	got, err := s.call(t, contentSearch.ID, `{"query":"x"}`, "chapter/1001")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 555, 1001}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("chapter hits = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/search/chapter/1001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}

	// The own page comes after a full page of foreign hits; the scan finds it and shows no foreign count.
	s.searchFiller = maxSearchCount
	got, err = s.call(t, contentSearch.ID, `{"query":"x"}`, "page/111")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("page hits = %v, want %v", resultIDs(t, got), want)
	}
	if total := resultTotal(t, got); total != 1 {
		t.Errorf("total = %v, want 1", total)
	}
	if got, want := s.requests(), []string{"GET /api/search", "GET /api/search"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	got, err = s.call(t, contentSearch.ID, `{"query":"x","page":2,"count":1}`, "page/111", "page/555")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{555}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("second page of page hits = %v, want %v", resultIDs(t, got), want)
	}
	_, err = s.call(t, contentSearch.ID, `{"query":"x","chapter_id":1001}`, "page/111")
	refused(t, "search in the chapter of a bound page", err)
}

func TestMixedTargetsReachTheirUnion(t *testing.T) {
	s := newAreaServer(t)
	targets := []string{"chapter/2001", "page/333"}
	for _, id := range []string{"222", "333"} {
		if _, err := s.call(t, pagesGet.ID, `{"id":`+id+`}`, targets...); err != nil {
			t.Errorf("page %s: %v", id, err)
		}
	}
	for _, id := range []string{"111", "444"} {
		_, err := s.call(t, pagesGet.ID, `{"id":`+id+`}`, targets...)
		refused(t, "page "+id, err)
	}
	_, err := s.call(t, pagesList.ID, `{}`, targets...)
	refused(t, "list without an argument", err)
	got, err := s.call(t, pagesList.ID, `{"chapter_id":2001}`, targets...)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{222}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("pages = %v, want %v", resultIDs(t, got), want)
	}
}

func TestNewTargetFormsAreValid(t *testing.T) {
	for _, value := range []string{"shelf/3", "book/4", "chapter/5", "page/6"} {
		if err := validateTarget(value); err != nil {
			t.Errorf("validateTarget(%q) = %v", value, err)
		}
	}
	if err := validateSet([]string{"shelf/3", "book/3", "chapter/3", "page/3"}); err != nil {
		t.Errorf("one identifier in several kinds: %v", err)
	}
	for _, values := range [][]string{{"page/3", "page/3"}, {"chapter/03"}, {"chapter/"}, {"pages/3"}} {
		if err := validateSet(values); err == nil {
			t.Errorf("validateSet(%v) accepted", values)
		}
	}
}
