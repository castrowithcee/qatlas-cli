package bookstack

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// writeCase is one call of a write tool against the area server with the exclusions of the connection.
type writeCase struct {
	name     string
	tool     string
	args     string
	targets  []string
	excludes []string
	// reqs is the exact traffic; empty with refuse means the call ended before any request.
	reqs   []string
	refuse bool
}

func runWriteCases(t *testing.T, cases []writeCase) {
	t.Helper()
	for _, tt := range cases {
		s := newAreaServer(t)
		s.setShelf(302, []int64{1101})
		_, err := s.callExcluding(t, tt.tool, tt.args, tt.targets, tt.excludes)
		if tt.refuse {
			refused(t, tt.name, err)
			noExclusionWord(t, tt.name, err)
		} else if err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		got := s.requests()
		if !reflect.DeepEqual(got, tt.reqs) && !(len(got) == 0 && len(tt.reqs) == 0) {
			t.Errorf("%s: requests = %v, want %v", tt.name, got, tt.reqs)
		}
	}
}

func TestDeleteRefusesAnExcludedDescendant(t *testing.T) {
	for _, targets := range [][]string{nil, {"book/701"}, {"shelf/301"}} {
		reads := []string{"GET /api/books/701"}
		chapterReads := []string{"GET /api/chapters/1001"}
		if targets != nil && targets[0] == "shelf/301" {
			reads = []string{"GET /api/shelves/301", "GET /api/books/701"}
			chapterReads = []string{"GET /api/chapters/1001", "GET /api/shelves/301"}
		}
		bookDeleted := append(slices.Clone(reads), "DELETE /api/books/701")
		chapterDeleted := append(slices.Clone(chapterReads), "DELETE /api/chapters/1001")
		runWriteCases(t, []writeCase{
			{"book with an excluded page", booksDelete.ID, `{"id":701}`, targets, []string{"page/555"}, reads, true},
			{"book with a page in an excluded chapter", booksDelete.ID, `{"id":701}`, targets, []string{"chapter/1001"}, reads, true},
			{"book with a loose excluded page", booksDelete.ID, `{"id":701}`, targets, []string{"page/333"}, reads, true},
			{"book with an excluded page elsewhere", booksDelete.ID, `{"id":701}`, targets, []string{"page/444", "chapter/2001"}, bookDeleted, false},
			{"chapter with an excluded page", chaptersDelete.ID, `{"id":1001}`, targets, []string{"page/555"}, chapterReads, true},
			{"chapter with an excluded page elsewhere", chaptersDelete.ID, `{"id":1001}`, targets, []string{"page/444"}, chapterDeleted, false},
		})
	}
	// Without exclusions a bound book is deleted as before, and a book under an excluded shelf is refused.
	runWriteCases(t, []writeCase{
		{"book without exclusions", booksDelete.ID, `{"id":701}`, []string{"book/701"}, nil, []string{"DELETE /api/books/701"}, false},
		{"book on an excluded shelf", booksDelete.ID, `{"id":701}`, nil, []string{"shelf/301"},
			[]string{"GET /api/shelves/301"}, true},
		{"chapter in an excluded book", chaptersDelete.ID, `{"id":1001}`, nil, []string{"book/701"},
			[]string{"GET /api/chapters/1001"}, true},
		{"page under an excluded chapter", pagesDelete.ID, `{"id":111}`, nil, []string{"chapter/1001"},
			[]string{"GET /api/pages/111"}, true},
		{"page under an excluded shelf", pagesDelete.ID, `{"id":111}`, nil, []string{"shelf/301"},
			[]string{"GET /api/pages/111", "GET /api/shelves/301"}, true},
	})
}

func TestShelfDeleteKeepsItsExcludedBooksAndRefusesAnExcludedShelf(t *testing.T) {
	runWriteCases(t, []writeCase{
		{"shelf with an excluded book", shelvesDelete.ID, `{"id":301}`, nil, []string{"book/701"},
			[]string{"DELETE /api/shelves/301"}, false},
		{"excluded shelf", shelvesDelete.ID, `{"id":302}`, nil, []string{"shelf/302"}, nil, true},
		{"shelf update of an excluded shelf", shelvesUpdate.ID, `{"id":302,"name":"x"}`, nil, []string{"shelf/302"}, nil, true},
	})
}

func TestMoveRefusesAnExcludedSourceOrTarget(t *testing.T) {
	move := `{"id":1001,"book_id":1101}`
	runWriteCases(t, []writeCase{
		{"chapter with an excluded page", chaptersUpdate.ID, move, nil, []string{"page/555"},
			[]string{"GET /api/chapters/1001", "PUT /api/chapters/1001"}, false},
		{"excluded source chapter", chaptersUpdate.ID, move, nil, []string{"chapter/1001"}, nil, true},
		{"source chapter in an excluded book", chaptersUpdate.ID, move, nil, []string{"book/701"},
			[]string{"GET /api/chapters/1001"}, true},
		{"excluded target book", chaptersUpdate.ID, move, nil, []string{"book/1101"}, nil, true},
		{"target book on an excluded shelf", chaptersUpdate.ID, move, nil, []string{"shelf/302"},
			[]string{"GET /api/chapters/1001", "GET /api/shelves/302"}, true},
		{"excluded target book under targets", chaptersUpdate.ID, move, []string{"shelf/301"}, []string{"book/1101"}, nil, true},
		{"page into an excluded chapter", pagesUpdate.ID, `{"id":111,"chapter_id":2001}`, nil, []string{"chapter/2001"}, nil, true},
		{"page into an excluded book", pagesUpdate.ID, `{"id":111,"book_id":901}`, nil, []string{"book/901"}, nil, true},
		{"excluded page", pagesUpdate.ID, `{"id":555,"name":"x"}`, nil, []string{"page/555"}, nil, true},
		{"page in an excluded book", pagesUpdate.ID, `{"id":222,"name":"x"}`, nil, []string{"book/901"},
			[]string{"GET /api/pages/222"}, true},
		{"page into a chapter of an excluded book", pagesUpdate.ID, `{"id":111,"chapter_id":2001}`, nil, []string{"book/901"},
			[]string{"GET /api/pages/111", "GET /api/chapters/2001"}, true},
		{"page move outside every exclusion", pagesUpdate.ID, `{"id":111,"chapter_id":2001}`, nil, []string{"page/555"},
			[]string{"PUT /api/pages/111"}, false},
	})
}

func TestCreateRefusesAnExcludedParent(t *testing.T) {
	runWriteCases(t, []writeCase{
		{"chapter in an excluded book", chaptersCreate.ID, `{"book_id":901,"name":"c"}`, nil, []string{"book/901"}, nil, true},
		{"chapter on an excluded shelf", chaptersCreate.ID, `{"book_id":701,"name":"c"}`, nil, []string{"shelf/301"},
			[]string{"GET /api/shelves/301"}, true},
		{"page in an excluded book", pagesCreate.ID, `{"book_id":901,"name":"p","markdown":"x"}`, nil, []string{"book/901"}, nil, true},
		{"page in an excluded chapter", pagesCreate.ID, `{"chapter_id":1001,"name":"p","markdown":"x"}`, nil, []string{"chapter/1001"}, nil, true},
		{"page in a chapter of an excluded book", pagesCreate.ID, `{"chapter_id":1001,"name":"p","markdown":"x"}`, nil, []string{"book/701"},
			[]string{"GET /api/chapters/1001"}, true},
		{"page in a book outside the exclusions", pagesCreate.ID, `{"book_id":701,"name":"p","markdown":"x"}`, nil, []string{"book/901", "page/555"},
			[]string{"POST /api/pages"}, false},
		{"page in a chapter outside the exclusions", pagesCreate.ID, `{"chapter_id":1001,"name":"p","markdown":"x"}`, []string{"book/701"}, []string{"page/555"},
			[]string{"GET /api/chapters/1001", "POST /api/pages"}, false},
	})
}

func TestTemplateOnAnExcludedPageIsRefused(t *testing.T) {
	for _, tt := range []struct {
		tool, args string
	}{
		{booksCreate.ID, `{"name":"b","default_template_id":555}`},
		{booksUpdate.ID, `{"id":701,"default_template_id":555}`},
		{chaptersCreate.ID, `{"book_id":701,"name":"c","default_template_id":555}`},
		{chaptersUpdate.ID, `{"id":1001,"default_template_id":555}`},
	} {
		runWriteCases(t, []writeCase{{tt.tool + " by identifier", tt.tool, tt.args, nil, []string{"page/555"}, nil, true}})
	}
	// A template under an excluded container is found by reading it; one outside the exclusions is accepted,
	// and one that can fall under no container exclusion needs no read.
	runWriteCases(t, []writeCase{
		{"template in an excluded chapter", booksUpdate.ID, `{"id":701,"default_template_id":111}`, nil, []string{"chapter/1001"},
			[]string{"GET /api/pages/111"}, true},
		{"template in an excluded book", booksCreate.ID, `{"name":"b","default_template_id":222}`, nil, []string{"book/901"},
			[]string{"GET /api/pages/222"}, true},
		{"template in an excluded book of a chapter", chaptersCreate.ID, `{"book_id":701,"name":"c","default_template_id":222}`, nil,
			[]string{"book/901"}, []string{"GET /api/pages/222"}, true},
		{"template outside the exclusions", booksUpdate.ID, `{"id":701,"default_template_id":333}`, nil, []string{"page/555"},
			[]string{"PUT /api/books/701"}, false},
		{"removed template", booksUpdate.ID, `{"id":701,"default_template_id":null}`, nil, []string{"page/555"},
			[]string{"PUT /api/books/701"}, false},
	})
}

func TestShelfBooksFollowTheAssignmentOfBookStack(t *testing.T) {
	books := func(body map[string]any) []int64 {
		out := []int64{}
		for _, v := range body["books"].([]any) {
			out = append(out, int64(v.(float64)))
		}
		return out
	}
	s := newAreaServer(t)
	s.setShelf(302, []int64{1101})
	s.setShelf(301, []int64{701, 901, 1101})
	// Create sends neither an excluded book nor one on an excluded shelf.
	if _, err := s.callExcluding(t, shelvesCreate.ID, `{"name":"s","books":[701,901,1101,5]}`, nil,
		[]string{"book/901", "shelf/302"}); err != nil {
		t.Fatal(err)
	}
	if got := books(s.lastBody(t)); !reflect.DeepEqual(got, []int64{701, 5}) {
		t.Errorf("created books = %v, want [701 5]", got)
	}
	if got, want := s.requests(), []string{"GET /api/shelves/302", "POST /api/shelves"}; !reflect.DeepEqual(got, want) {
		t.Errorf("create requests = %v, want %v", got, want)
	}
	// Without exclusions nothing is read and the list stays as given.
	if _, err := s.call(t, shelvesCreate.ID, `{"name":"s","books":[901,701]}`); err != nil {
		t.Fatal(err)
	}
	if got := books(s.lastBody(t)); !reflect.DeepEqual(got, []int64{901, 701}) {
		t.Errorf("books without exclusions = %v", got)
	}
	if got, want := s.requests(), []string{"POST /api/shelves"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests without exclusions = %v, want %v", got, want)
	}
	// An empty list stays empty, an omitted one stays omitted and reads no shelf.
	if _, err := s.callExcluding(t, shelvesCreate.ID, `{"name":"s","books":[901]}`, nil, []string{"book/901"}); err != nil {
		t.Fatal(err)
	}
	if got := s.lastBody(t)["books"]; !reflect.DeepEqual(got, []any{}) {
		t.Errorf("books = %v, want []", got)
	}
	s.requests()
	if _, err := s.callExcluding(t, shelvesUpdate.ID, `{"id":301,"name":"n"}`, nil, []string{"book/901"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.lastBody(t)["books"]; ok {
		t.Error("an omitted books list was sent")
	}
	if got, want := s.requests(), []string{"PUT /api/shelves/301"}; !reflect.DeepEqual(got, want) {
		t.Errorf("update without books requests = %v, want %v", got, want)
	}

	// Update keeps the excluded books of the shelf behind the sent ones, whatever the list says of them.
	for _, tt := range []struct {
		args     string
		excludes []string
		want     []int64
	}{
		{`{"id":301,"books":[1101]}`, []string{"book/701", "book/901"}, []int64{1101, 701, 901}},
		{`{"id":301,"books":[1101,701,5]}`, []string{"book/701"}, []int64{1101, 5, 701}},
		{`{"id":301,"books":[]}`, []string{"book/701"}, []int64{701}},
		{`{"id":301,"books":[5]}`, []string{"shelf/302"}, []int64{5, 1101}},
		{`{"id":301,"books":[5]}`, []string{"book/4000"}, []int64{5}},
	} {
		s.requests()
		if _, err := s.callExcluding(t, shelvesUpdate.ID, tt.args, nil, tt.excludes); err != nil {
			t.Fatalf("%s: %v", tt.args, err)
		}
		if got := books(s.lastBody(t)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %v: books = %v, want %v", tt.args, tt.excludes, got, tt.want)
		}
		reqs := s.requests()
		if reqs[0] != "GET /api/shelves/301" || reqs[len(reqs)-1] != "PUT /api/shelves/301" {
			t.Errorf("%s: requests = %v", tt.args, reqs)
		}
	}

	// A shelf that cannot be read fails the call closed, without a change.
	s.requests()
	_, err := s.callExcluding(t, shelvesCreate.ID, `{"name":"s","books":[701]}`, nil, []string{"shelf/9999"})
	if err == nil || !strings.Contains(err.Error(), unprovable) {
		t.Errorf("unreadable excluded shelf: err = %v, want %q", err, unprovable)
	}
	for _, r := range s.requests() {
		if strings.HasPrefix(r, "POST") {
			t.Errorf("a change was sent: %v", r)
		}
	}
}

func TestExcludedWriteArgumentsAreRefusedBeforeSecretsAndIO(t *testing.T) {
	s := newAreaServer(t)
	excludes := []string{"page/555", "chapter/1001", "book/901", "shelf/302"}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	// No token variables are set: a secret access would fail with a different error.
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, targets := range [][]string{nil, {"shelf/301"}} {
		resolved := boundResolved(s.URL, targets...)
		resolved.Exclude = excludes
		for _, tt := range []struct{ tool, args string }{
			{pagesCreate.ID, `{"book_id":901,"name":"p","markdown":"x"}`},
			{pagesCreate.ID, `{"chapter_id":1001,"name":"p","markdown":"x"}`},
			{pagesUpdate.ID, `{"id":555,"name":"x"}`},
			{pagesUpdate.ID, `{"id":111,"book_id":901}`},
			{pagesUpdate.ID, `{"id":111,"chapter_id":1001}`},
			{pagesDelete.ID, `{"id":555}`},
			{booksCreate.ID, `{"name":"b","default_template_id":555}`},
			{booksUpdate.ID, `{"id":901,"name":"x"}`},
			{booksUpdate.ID, `{"id":701,"default_template_id":555}`},
			{booksDelete.ID, `{"id":901}`},
			{chaptersCreate.ID, `{"book_id":901,"name":"c"}`},
			{chaptersCreate.ID, `{"book_id":701,"name":"c","default_template_id":555}`},
			{chaptersUpdate.ID, `{"id":1001,"name":"x"}`},
			{chaptersUpdate.ID, `{"id":2001,"book_id":901}`},
			{chaptersUpdate.ID, `{"id":2001,"default_template_id":555}`},
			{chaptersDelete.ID, `{"id":1001}`},
			{shelvesUpdate.ID, `{"id":302,"name":"x"}`},
			{shelvesDelete.ID, `{"id":302}`},
		} {
			_, handler, _ := reg.Lookup(tt.tool)
			if targets != nil && (tt.tool == shelvesUpdate.ID || tt.tool == shelvesDelete.ID || tt.tool == booksCreate.ID) {
				continue // refused by the targets themselves
			}
			_, err := handler(t.Context(), resolved, resolver(nil), nil, []byte(tt.args))
			refused(t, tt.tool+" "+tt.args, err)
			noExclusionWord(t, tt.tool, err)
		}
	}
	if got := s.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestDeleteWithAnUnreadableTreeIsNotSent(t *testing.T) {
	s := newAreaServer(t)
	for _, tt := range []struct{ tool, args string }{
		{booksDelete.ID, `{"id":8888}`},
		{chaptersDelete.ID, `{"id":8888}`},
	} {
		if _, err := s.callExcluding(t, tt.tool, tt.args, nil, []string{"page/555"}); err == nil {
			t.Errorf("%s: err = nil, want a failure", tt.tool)
		}
	}
	for _, r := range s.requests() {
		if strings.HasPrefix(r, "DELETE") {
			t.Errorf("a delete was sent: %s", r)
		}
	}
}
