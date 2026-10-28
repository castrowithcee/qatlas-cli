package bookstack

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// noExclusionWord checks that a refusal does not speak of an exclusion.
func noExclusionWord(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "exclu") {
		t.Errorf("%s: refusal %q speaks of an exclusion", what, err)
	}
}

// treeIDs returns the identifiers of a list of tree entries, with the pages of each chapter after it.
func treeIDs(entries []map[string]any) []int64 {
	out := []int64{}
	for _, e := range entries {
		out = append(out, e["id"].(int64))
		if pages, ok := e["pages"].([]map[string]any); ok {
			out = append(out, treeIDs(pages)...)
		}
	}
	return out
}

func TestExclusionsFilterAConnectionWithoutTargets(t *testing.T) {
	s := newAreaServer(t)
	s.setShelf(301, []int64{701, 1101})
	s.setShelf(302, []int64{1101})
	excludes := []string{"page/555", "chapter/2001", "shelf/302"}
	list := func(tool, args string) []int64 {
		t.Helper()
		got, err := s.callExcluding(t, tool, args, nil, excludes)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return resultIDs(t, got)
	}
	s.requests()
	for _, tt := range []struct {
		tool string
		want []int64
		reqs []string
	}{
		{pagesList.ID, []int64{111, 333, 444}, []string{"GET /api/shelves/302", "GET /api/pages"}},
		{chaptersList.ID, []int64{1001}, []string{"GET /api/shelves/302", "GET /api/chapters"}},
		{booksList.ID, []int64{701, 901}, []string{"GET /api/shelves/302", "GET /api/books"}},
		{shelvesList.ID, []int64{301}, []string{"GET /api/shelves"}},
	} {
		if got := list(tt.tool, `{}`); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.tool, got, tt.want)
		}
		if got := s.requests(); !reflect.DeepEqual(got, tt.reqs) {
			t.Errorf("%s requests = %v, want %v", tt.tool, got, tt.reqs)
		}
	}

	// The search drops excluded hits of every kind, a shelf among them, and keeps BookStack's total.
	got, err := s.callExcluding(t, contentSearch.ID, `{"query":"x"}`, nil, append(excludes, "book/901"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 333, 1001, 701, 301}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("hits = %v, want %v", resultIDs(t, got), want)
	}
	if total := resultTotal(t, got); total != 9 {
		t.Errorf("total = %v, want BookStack's 9", total)
	}
	if got, want := s.requests(), []string{"GET /api/search", "GET /api/shelves/302"}; !reflect.DeepEqual(got, want) {
		t.Errorf("search requests = %v, want %v", got, want)
	}
	got, err = s.callExcluding(t, contentSearch.ID, `{"query":"x"}`, nil, []string{"shelf/301"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{222, 2001, 901}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("hits without the shelf's books = %v, want %v", resultIDs(t, got), want)
	}

	// A read leaves excluded children out of its tree and refuses an excluded object.
	read := func(tool, args, field string) []int64 {
		t.Helper()
		got, err := s.callExcluding(t, tool, args, nil, excludes)
		if err != nil {
			t.Fatalf("%s %s: %v", tool, args, err)
		}
		return treeIDs(objectField(got, field).([]map[string]any))
	}
	if got, want := read(booksGet.ID, `{"id":701}`, "contents"), []int64{1001, 111, 333}; !reflect.DeepEqual(got, want) {
		t.Errorf("book tree = %v, want %v", got, want)
	}
	if got := read(booksGet.ID, `{"id":701}`, "shelves"); !reflect.DeepEqual(got, []int64{}) {
		t.Errorf("shelves of the book = %v", got)
	}
	if got, want := read(chaptersGet.ID, `{"id":1001}`, "pages"), []int64{111}; !reflect.DeepEqual(got, want) {
		t.Errorf("chapter pages = %v, want %v", got, want)
	}
	if got, want := read(shelvesGet.ID, `{"id":301}`, "books"), []int64{701}; !reflect.DeepEqual(got, want) {
		t.Errorf("shelf books = %v, want %v", got, want)
	}
	if _, err := s.callExcluding(t, pagesGet.ID, `{"id":444}`, nil, excludes); err != nil {
		t.Errorf("page outside every exclusion: %v", err)
	}
	for _, tt := range []struct{ tool, args string }{
		{pagesGet.ID, `{"id":222}`},
		{booksGet.ID, `{"id":1101}`},
		{pagesList.ID, `{"book_id":1101}`},
		{chaptersList.ID, `{"book_id":1101}`},
		{contentSearch.ID, `{"query":"x","book_id":1101}`},
		{contentSearch.ID, `{"query":"x","chapter_id":2001}`},
	} {
		_, err := s.callExcluding(t, tt.tool, tt.args, nil, excludes)
		refused(t, tt.tool+" "+tt.args, err)
		noExclusionWord(t, tt.tool, err)
	}
}

func TestExcludedIdentifierIsRefusedBeforeSecretsAndIO(t *testing.T) {
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
			{pagesGet.ID, `{"id":555}`},
			{chaptersGet.ID, `{"id":1001}`},
			{booksGet.ID, `{"id":901}`},
			{shelvesGet.ID, `{"id":302}`},
			{pagesList.ID, `{"book_id":901}`},
			{pagesList.ID, `{"chapter_id":1001}`},
			{chaptersList.ID, `{"book_id":901}`},
			{contentSearch.ID, `{"query":"x","book_id":901}`},
			{contentSearch.ID, `{"query":"x","chapter_id":1001}`},
			{contentExport.ID, `{"type":"page","id":555,"format":"plaintext"}`},
			{contentExport.ID, `{"type":"chapter","id":1001,"format":"plaintext"}`},
			{contentExport.ID, `{"type":"book","id":901,"format":"plaintext"}`},
		} {
			_, handler, _ := reg.Lookup(tt.tool)
			_, err := handler(context.Background(), resolved, resolver(nil), nil, []byte(tt.args))
			refused(t, tt.tool+" "+tt.args, err)
			noExclusionWord(t, tt.tool, err)
		}
	}
	if got := s.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestExcludedShelfTakesABoundBookOutOfReach(t *testing.T) {
	s := newAreaServer(t)
	targets, excludes := []string{"book/701"}, []string{"shelf/301"}
	_, err := s.callExcluding(t, pagesGet.ID, `{"id":111}`, targets, excludes)
	refused(t, "page of the book on the excluded shelf", err)
	if got, want := s.requests(), []string{"GET /api/pages/111", "GET /api/shelves/301"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	for _, tt := range []struct{ tool, args string }{
		{booksGet.ID, `{"id":701}`},
		{pagesList.ID, `{}`},
		{chaptersList.ID, `{}`},
		{chaptersGet.ID, `{"id":1001}`},
		{contentSearch.ID, `{"query":"x"}`},
		{contentExport.ID, `{"type":"book","id":701,"format":"plaintext"}`},
		{contentExport.ID, `{"type":"chapter","id":1001,"format":"plaintext"}`},
	} {
		_, err := s.callExcluding(t, tt.tool, tt.args, targets, excludes)
		refused(t, tt.tool+" "+tt.args, err)
	}
	for _, r := range s.requests() {
		if strings.Contains(r, "/export/") {
			t.Errorf("export requested: %s", r)
		}
	}

	// Membership is read at call time: off the excluded shelf, the book is back in reach.
	s.setShelf(301, nil)
	got, err := s.callExcluding(t, pagesList.ID, `{}`, targets, excludes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 333, 555}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("pages = %v, want %v", resultIDs(t, got), want)
	}
}

func TestBookOnABoundAndAnExcludedShelfIsOutOfReach(t *testing.T) {
	s := newAreaServer(t)
	s.setShelf(301, []int64{701, 1101})
	s.setShelf(302, []int64{701})
	targets, excludes := []string{"shelf/301"}, []string{"shelf/302"}
	got, err := s.callExcluding(t, booksList.ID, `{}`, targets, excludes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1101}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("books = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/shelves/301", "GET /api/shelves/302", "GET /api/books"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v: each shelf is read once", got, want)
	}
	got, err = s.callExcluding(t, shelvesGet.ID, `{"id":301}`, targets, excludes)
	if err != nil {
		t.Fatal(err)
	}
	if ids := treeIDs(objectField(got, "books").([]map[string]any)); !reflect.DeepEqual(ids, []int64{1101}) {
		t.Errorf("books of the bound shelf = %v, want [1101]", ids)
	}
	s.requests()
	_, err = s.callExcluding(t, booksGet.ID, `{"id":701}`, targets, excludes)
	refused(t, "book on both shelves", err)
	if got, want := s.requests(), []string{"GET /api/shelves/301", "GET /api/shelves/302"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	_, err = s.callExcluding(t, pagesGet.ID, `{"id":333}`, targets, excludes)
	refused(t, "page of the book on both shelves", err)
}

func TestBoundPageOrChapterUnderAnExclusionIsOutOfReach(t *testing.T) {
	s := newAreaServer(t)
	targets, excludes := []string{"page/111"}, []string{"chapter/1001"}
	_, err := s.callExcluding(t, pagesGet.ID, `{"id":111}`, targets, excludes)
	refused(t, "bound page in an excluded chapter", err)
	if got, want := s.requests(), []string{"GET /api/pages/111"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	for _, tt := range []struct{ tool, args string }{{pagesList.ID, `{}`}, {contentSearch.ID, `{"query":"x"}`}} {
		got, err := s.callExcluding(t, tt.tool, tt.args, targets, excludes)
		if err != nil {
			t.Fatalf("%s: %v", tt.tool, err)
		}
		if ids := resultIDs(t, got); len(ids) != 0 {
			t.Errorf("%s = %v, want none", tt.tool, ids)
		}
	}
	_, err = s.callExcluding(t, contentExport.ID, `{"type":"page","id":111,"format":"plaintext"}`, targets, excludes)
	refused(t, "export of the bound page", err)

	// A bound chapter in an excluded book is out of reach too; its book is read as evidence.
	targets, excludes = []string{"chapter/1001"}, []string{"book/701"}
	s.requests()
	for _, tt := range []struct{ tool, args string }{
		{chaptersGet.ID, `{"id":1001}`},
		{pagesList.ID, `{}`},
		{pagesGet.ID, `{"id":111}`},
		{contentSearch.ID, `{"query":"x"}`},
	} {
		_, err := s.callExcluding(t, tt.tool, tt.args, targets, excludes)
		refused(t, tt.tool+" "+tt.args, err)
	}
	for _, r := range s.requests() {
		if strings.HasPrefix(r, "GET /api/search") || r == "GET /api/pages" {
			t.Errorf("unexpected request %s", r)
		}
	}
}

func TestUnreadableExcludedShelfFailsClosed(t *testing.T) {
	s := newAreaServer(t)
	for _, shelf := range []string{"shelf/303", "shelf/399"} {
		for _, tt := range []struct{ tool, args string }{
			{pagesGet.ID, `{"id":111}`},
			{booksGet.ID, `{"id":701}`},
			{pagesList.ID, `{}`},
			{booksList.ID, `{}`},
			{chaptersList.ID, `{}`},
			{contentSearch.ID, `{"query":"x"}`},
		} {
			got, err := s.callExcluding(t, tt.tool, tt.args, nil, []string{shelf})
			label := shelf + " " + tt.tool
			if err == nil {
				t.Errorf("%s: answered %v, want a failure", label, got)
				continue
			}
			if strings.Contains(err.Error(), "303") || strings.Contains(err.Error(), "399") {
				t.Errorf("%s: failure %q names the shelf", label, err)
			}
			noExclusionWord(t, label, err)
			var perr *provider.Error
			if shelf == "shelf/303" {
				refused(t, label, err)
			} else if !errors.As(err, &perr) || perr.Class != provider.ClassProviderError || perr.Original != nil {
				t.Errorf("%s: err = %#v, want a provider error without the provider's text", label, err)
			}
		}
	}
	// A call that needs no book is not held up by the shelf.
	if _, err := s.callExcluding(t, shelvesList.ID, `{}`, nil, []string{"shelf/303"}); err != nil {
		t.Errorf("shelves.list: %v", err)
	}
}

func TestExclusionOfAMissingObjectHasNoEffect(t *testing.T) {
	s := newAreaServer(t)
	excludes := []string{"page/9001", "chapter/9002", "book/9003"}
	for _, tt := range []struct {
		tool string
		want []int64
	}{
		{pagesList.ID, []int64{111, 222, 333, 444, 555}},
		{chaptersList.ID, []int64{1001, 2001}},
		{booksList.ID, []int64{701, 901, 1101}},
	} {
		got, err := s.callExcluding(t, tt.tool, `{}`, nil, excludes)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resultIDs(t, got), tt.want) {
			t.Errorf("%s = %v, want %v", tt.tool, resultIDs(t, got), tt.want)
		}
	}
	got, err := s.callExcluding(t, contentSearch.ID, `{"query":"x"}`, []string{"book/701"}, excludes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{111, 333, 555, 1001, 701}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("hits = %v, want %v", resultIDs(t, got), want)
	}
	if _, err := s.callExcluding(t, pagesGet.ID, `{"id":111}`, []string{"page/111"}, excludes); err != nil {
		t.Errorf("bound page: %v", err)
	}
}

func TestExportWithAnExcludedDescendantIsRefused(t *testing.T) {
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	for _, targets := range [][]string{nil, {"book/1"}} {
		for _, tt := range []struct {
			excludes []string
			raw      map[int64]string
			kind     string
			id       int
			refused  bool
		}{
			{[]string{"page/12"}, nil, "chapter", 5, true},
			{[]string{"page/12"}, nil, "book", 1, true},
			{[]string{"page/12"}, nil, "page", 10, false},
			{[]string{"chapter/5"}, nil, "book", 1, true},
			{[]string{"page/11"}, map[int64]string{10: "{{@11}}"}, "page", 10, true},
			{[]string{"page/11"}, map[int64]string{10: "{{@12}}"}, "page", 10, false},
		} {
			server := newIncludeExportServer(t, tt.raw)
			resolved := exportResolved(server.url, "", targets...)
			resolved.Exclude = tt.excludes
			_, err := lookup(t, contentExport.ID)(context.Background(), resolved, resolver(nil), nil,
				exportArgs(tt.kind, tt.id, "plaintext", ""))
			label := strings.Join(targets, ",") + " " + strings.Join(tt.excludes, ",") + " " + tt.kind
			if !tt.refused {
				if err != nil {
					t.Errorf("%s: %v", label, err)
				}
				continue
			}
			if !isInvalidRequest(err) {
				t.Errorf("%s: err = %v, want an invalid request", label, err)
				continue
			}
			for _, leak := range []string{"11", "12", "5", "exclu"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s: refusal %q names %q", label, err, leak)
				}
			}
			if server.exported() {
				t.Errorf("%s: export requested: %v", label, exportPaths(server.rec))
			}
		}
	}
}

// excludingClient opens a client on a connection with targets and exclusions.
func excludingClient(t *testing.T, baseURL string, targets, excludes []string) *Client {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := boundResolved(baseURL, targets...)
	resolved.Exclude = excludes
	client, err := Open(context.Background(), resolved, resolver(nil), nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return client
}

func TestIncludeOfAnExcludedPageIsEmpty(t *testing.T) {
	for _, targets := range [][]string{nil, {"book/1"}} {
		s := newIncludeServer(t, mainPage(`<p>before</p><p>{{@111}}</p>`))
		s.pages[111] = includeFixture{book: 1, raw: `<p>` + includeCanary + `-111</p>`}
		html, _ := getHTML(t, excludingClient(t, s.url, targets, []string{"page/111"}))
		if html != "<p>before</p><p></p>" {
			t.Errorf("%v: html = %q", targets, html)
		}
		s.wantReads(t, "GET /api/pages/100")
	}
}

func TestInvalidExclusionFailsClosedWithoutQuotingIt(t *testing.T) {
	for _, excludes := range [][]string{{"page/x7"}, {"pages/7"}, {"page/7", "page/7"}, {"book/701"}} {
		_, err := boundScope(&config.Resolved{Targets: []string{"book/701"}, Exclude: excludes})
		if err == nil {
			t.Errorf("%v accepted", excludes)
			continue
		}
		if strings.Contains(err.Error(), "7") {
			t.Errorf("%v: error %q quotes the value", excludes, err)
		}
	}
}

func TestReachOfferIgnoresExclusions(t *testing.T) {
	meta := targetMetadata(t)
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	excludes := []string{"shelf/302", "book/901", "chapter/2001", "page/555"}
	for _, targets := range [][]string{nil, {"shelf/301"}, {"book/701"}, {"chapter/1001"}, {"page/111"}, {"book/701", "page/111"}} {
		plain, err := boundScope(&config.Resolved{Targets: targets})
		if err != nil {
			t.Fatal(err)
		}
		excluding, err := boundScope(&config.Resolved{Targets: targets, Exclude: excludes})
		if err != nil {
			t.Fatal(err)
		}
		if excluding.bound() != plain.bound() || !excluding.limited() {
			t.Errorf("%v: bound = %v, limited = %v", targets, excluding.bound(), excluding.limited())
		}
		gate := requireInstanceScope(&config.Resolved{Targets: targets, Exclude: excludes}, "x")
		if (gate == nil) != !plain.bound() {
			t.Errorf("%v: instance gate = %v", targets, gate)
		}
		for _, d := range reg.Provider(Provider) {
			reached, _ := meta.Reach(d.ID, targets)
			want, _ := reaches(d.ID, targets)
			if reached != want {
				t.Errorf("%s with %v: reached = %v, want %v", d.ID, targets, reached, want)
			}
		}
		if _, err := excluding.listIn(listTarget{}); err != nil && !plain.bound() {
			t.Errorf("%v: list without an argument: %v", targets, err)
		}
		_, plainErr := plain.listIn(listTarget{})
		_, exclErr := excluding.listIn(listTarget{})
		if (plainErr == nil) != (exclErr == nil) {
			t.Errorf("%v: list defaults differ: %v / %v", targets, plainErr, exclErr)
		}
	}

	// Instance-wide tools stay usable on a connection with exclusions and no targets.
	s := newAreaServer(t)
	for _, tool := range []string{shelvesList.ID, tagsList.ID, usersList.ID, auditLogList.ID} {
		_, err := s.callExcluding(t, tool, `{}`, nil, excludes)
		if isInvalidRequest(err) {
			t.Errorf("%s refused: %v", tool, err)
		}
	}
}
