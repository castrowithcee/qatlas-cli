package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// includeCanary marks content of pages outside the targets; it must never appear in an html result.
const includeCanary = "CANARY"

type includeFixture struct {
	book int64
	raw  string
	html string
}

// includeServer serves pages with stored and rendered HTML. Book 1 is the bound book, book 2 is foreign; a page
// missing from pages answers 404, one in failing 500. A write answers with the page written.
type includeServer struct {
	rec     *recorder
	pages   map[int64]includeFixture
	failing map[int64]bool
	written int64
	url     string
}

func newIncludeServer(t *testing.T, pages map[int64]includeFixture) *includeServer {
	t.Helper()
	s := &includeServer{rec: &recorder{}, pages: map[int64]includeFixture{
		222: {book: 2, raw: `<p>CANARY-222</p><p id="bkmrk-x">CANARY-222-x</p>`},
		111: {book: 1, raw: `<p>inside</p>`},
	}, failing: map[int64]bool{}}
	for id, p := range pages {
		s.pages[id] = p
	}
	answer := func(w http.ResponseWriter, id int64) {
		p, ok := s.pages[id]
		switch {
		case s.failing[id]:
			w.WriteHeader(http.StatusInternalServerError)
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "book_id": p.book, "chapter_id": 0, "name": "p", "slug": "p",
				"raw_html": p.raw, "html": p.html, "markdown": "",
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pages", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		answer(w, s.written)
	})
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/pages/"), 10, 64)
		if r.Method == http.MethodPut {
			id = s.written
		}
		answer(w, id)
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	s.url = server.URL
	return s
}

// reads counts the requests per page.
func (s *includeServer) reads() map[string]int {
	out := map[string]int{}
	for _, r := range requests(s.rec) {
		out[r]++
	}
	return out
}

func (s *includeServer) wantReads(t *testing.T, want ...string) {
	t.Helper()
	got := requests(s.rec)
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func getHTML(t *testing.T, c *Client) (string, map[string]any) {
	t.Helper()
	o, err := c.GetPage(context.Background(), "100")
	if err != nil {
		t.Fatal(err)
	}
	fields := fieldMap(o)
	html, _ := fields["html"].(string)
	if strings.Contains(html, includeCanary) {
		t.Errorf("html carries content outside the targets: %q", html)
	}
	return html, fields
}

func mainPage(raw string) map[int64]includeFixture {
	return map[int64]includeFixture{100: {book: 1, raw: raw, html: "BOOKSTACK " + includeCanary}}
}

func TestIncludeOutsidePageTargetsIsEmptyWithoutARead(t *testing.T) {
	s := newIncludeServer(t, mainPage(`<p>before</p><p>{{@222}}</p><p>after</p>`))
	html, fields := getHTML(t, boundClient(t, s.url, "page/100"))
	if html != "<p>before</p><p></p><p>after</p>" {
		t.Errorf("html = %q", html)
	}
	if fields["raw_html"] != `<p>before</p><p>{{@222}}</p><p>after</p>` {
		t.Errorf("raw_html = %q, want the placeholder kept", fields["raw_html"])
	}
	if fields["html_withheld"] != nil {
		t.Error("html marked as withheld")
	}
	s.wantReads(t, "GET /api/pages/100")
}

func TestIncludeOfAForeignSectionIsEmpty(t *testing.T) {
	s := newIncludeServer(t, mainPage(`<p>a {{@222#bkmrk-x}} b</p>`))
	if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != "<p>a  b</p>" {
		t.Errorf("html = %q", html)
	}
	s.wantReads(t, "GET /api/pages/100", "GET /api/pages/222")
}

func TestIncludedSectionsFollowBookStacksInsertion(t *testing.T) {
	s := newIncludeServer(t, map[int64]includeFixture{
		100: {book: 1, raw: `<p id="keep">one {{@112#bkmrk-t}} two {{@112#bkmrk-p}} three {{@222}} {{@112#none}}</p>`},
		112: {book: 1, raw: `<p>x</p><table id="bkmrk-t"><tbody><tr><td>T</td></tr></tbody></table><p id="bkmrk-p">P <b>bold</b></p>`},
	})
	html, _ := getHTML(t, boundClient(t, s.url, "book/1"))
	want := `<p>one </p><table id="bkmrk-t"><tbody><tr><td>T</td></tr></tbody></table><p id="keep"> two P <b>bold</b> three  </p>`
	if html != want {
		t.Errorf("html = %q, want %q", html, want)
	}
	s.wantReads(t, "GET /api/pages/100", "GET /api/pages/112", "GET /api/pages/222")
}

func TestIncludeOutsideTheTargetsIsEmptyOnEveryLevel(t *testing.T) {
	t.Run("inside, inside, outside", func(t *testing.T) {
		s := newIncludeServer(t, map[int64]includeFixture{
			100: {book: 1, raw: `<p>{{@120}}</p>`, html: includeCanary},
			120: {book: 1, raw: `<p>B-in</p><p>{{@222}}</p>`},
		})
		if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != "<p>B-in</p><p></p>" {
			t.Errorf("html = %q", html)
		}
		s.wantReads(t, "GET /api/pages/100", "GET /api/pages/120", "GET /api/pages/222")
	})
	t.Run("inside, outside, inside", func(t *testing.T) {
		s := newIncludeServer(t, map[int64]includeFixture{
			100: {book: 1, raw: `<p>{{@223}}</p>`, html: "CANARY-223 inside"},
			223: {book: 2, raw: `<p>CANARY-223 {{@111}}</p>`},
		})
		html, _ := getHTML(t, boundClient(t, s.url, "book/1"))
		if html != "<p></p>" {
			t.Errorf("html = %q, want the included page of a foreign page left out", html)
		}
		s.wantReads(t, "GET /api/pages/100", "GET /api/pages/223")
	})
}

func TestIncludeBeyondTheThirdLevelStaysText(t *testing.T) {
	s := newIncludeServer(t, map[int64]includeFixture{
		100: {book: 1, raw: `<p>{{@131}}</p><p>{{@222}}</p>`},
		131: {book: 1, raw: `<p>L1</p><p>{{@132}}</p>`},
		132: {book: 1, raw: `<p>L2</p><p>{{@133}}</p>`},
		133: {book: 1, raw: `<p>L3</p><p>{{@134}}</p>`},
		134: {book: 1, raw: `<p>L4</p>`},
	})
	html, _ := getHTML(t, boundClient(t, s.url, "book/1"))
	if want := "<p>L1</p><p>L2</p><p>L3</p><p>{{@134}}</p><p></p>"; html != want {
		t.Errorf("html = %q, want %q", html, want)
	}
	s.wantReads(t, "GET /api/pages/100", "GET /api/pages/131", "GET /api/pages/132", "GET /api/pages/133",
		"GET /api/pages/222")
}

func TestIncludeTagFormsAreFoundLikeBookStack(t *testing.T) {
	for _, tc := range []struct{ raw, html string }{
		{`<p>x &#123;&#123;@222}} y</p>`, "<p>x  y</p>"},
		{`<p>x {{@ 222}} y</p>`, "<p>x  y</p>"},
		{`<p>x {{@222abc}} y</p>`, "<p>x  y</p>"},
		{`<p>x {{@0222#bkmrk-x}} y</p>`, "<p>x  y</p>"},
		{`<p>x {{@1e3}} y</p>`, "<p>x  y</p>"},
		{`<p title="{{@222}}">t</p><!-- {{@222}} -->`, `<p title="{{@222}}">t</p><!-- {{@222}} -->`},
	} {
		s := newIncludeServer(t, mainPage(tc.raw))
		if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != tc.html {
			t.Errorf("%s: html = %q, want %q", tc.raw, html, tc.html)
		}
		if s.reads()["GET /api/pages/111"] != 0 || len(requests(s.rec)) > 2 {
			t.Errorf("%s: requests = %v", tc.raw, requests(s.rec))
		}
	}
}

func TestIncludeJoinedFromTwoTextsIsChecked(t *testing.T) {
	// The empty section leaves "{{@" and "222}}" side by side; the block include splits the paragraph, and
	// BookStack's clean-up joins both into a tag resolved in the second pass.
	s := newIncludeServer(t, map[int64]includeFixture{
		100: {book: 1, raw: `<p>{{@{{@140#s}}222}} {{@111}}</p>`, html: includeCanary},
		140: {book: 1, raw: `<p><span id="s"></span></p>`},
	})
	if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != "<p> </p><p>inside</p>" {
		t.Errorf("html = %q", html)
	}
	if s.reads()["GET /api/pages/222"] != 1 {
		t.Errorf("requests = %v, want the joined tag checked", requests(s.rec))
	}
}

func TestIncludesInReachKeepBookStacksHTML(t *testing.T) {
	const rendered = `<p id="bkmrk-1">rendered by BookStack</p>`
	pages := map[int64]includeFixture{
		100: {book: 1, raw: `<p>{{@111}} {{@111#a}}</p><p>{{@999}}</p>`, html: rendered},
	}
	t.Run("bound", func(t *testing.T) {
		s := newIncludeServer(t, pages)
		if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != rendered {
			t.Errorf("html = %q, want BookStack's", html)
		}
		s.wantReads(t, "GET /api/pages/100", "GET /api/pages/111", "GET /api/pages/999")
	})
	t.Run("unbound", func(t *testing.T) {
		s := newIncludeServer(t, pages)
		if html, _ := getHTML(t, newClient(t, s.url, nil)); html != rendered {
			t.Errorf("html = %q, want BookStack's", html)
		}
		s.wantReads(t, "GET /api/pages/100")
	})
	t.Run("no include", func(t *testing.T) {
		s := newIncludeServer(t, map[int64]includeFixture{100: {book: 1, raw: `<p>{{@}} {{x}}</p>`, html: rendered}})
		if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != rendered {
			t.Errorf("html = %q, want BookStack's", html)
		}
		s.wantReads(t, "GET /api/pages/100")
	})
}

func TestIncludeTheTokenCannotReadIsEmpty(t *testing.T) {
	s := newIncludeServer(t, mainPage(`<p>{{@999}}</p><p>{{@111#x}}</p><p>{{@222}}</p>`))
	if html, _ := getHTML(t, boundClient(t, s.url, "book/1")); html != "<p></p><p></p><p></p>" {
		t.Errorf("html = %q", html)
	}
}

func TestIncludeCheckFailureOfARead(t *testing.T) {
	s := newIncludeServer(t, mainPage(`<p>{{@222}}</p>`))
	s.failing[222] = true
	if _, err := boundClient(t, s.url, "book/1").GetPage(context.Background(), "100"); err == nil {
		t.Fatal("GetPage() succeeded although the include could not be checked")
	}
}

func TestIncludeWithAnUnreadableShelfFailsTheCall(t *testing.T) {
	s := newIncludeServer(t, mainPage(`<p>{{@111}}</p>`))
	if _, err := boundClient(t, s.url, "page/100", "shelf/5").GetPage(context.Background(), "100"); err == nil {
		t.Fatal("GetPage() succeeded although the shelf as evidence could not be read")
	}
}

func TestIncludeLimitWithholdsHTML(t *testing.T) {
	var raw strings.Builder
	for id := 1001; id <= 1000+maxIncludedPages+1; id++ {
		fmt.Fprintf(&raw, "<p>{{@%d}}</p>", id)
	}
	s := newIncludeServer(t, mainPage(raw.String()))
	html, fields := getHTML(t, boundClient(t, s.url, "book/1"))
	if html != "" || fields["html_withheld"] != true {
		t.Errorf("html = %q, html_withheld = %v", html, fields["html_withheld"])
	}
	if got := len(requests(s.rec)); got != 1+maxIncludedPages {
		t.Errorf("%d requests, want %d", got, 1+maxIncludedPages)
	}
	if fields["raw_html"] != raw.String() {
		t.Error("raw_html changed")
	}
}

func TestPageWritesLeaveForeignIncludesEmpty(t *testing.T) {
	fixtures := map[int64]includeFixture{
		100: {book: 1, raw: `<p>{{@222}}</p>`, html: includeCanary},
	}
	write := func(t *testing.T, c *Client, update bool) map[string]any {
		t.Helper()
		var o, err = c.CreatePage(context.Background(), pageMutation{Name: "n", BookID: 1, HTML: "<p>{{@222}}</p>"})
		if update {
			o, err = c.UpdatePage(context.Background(), "100", pageMutation{HTML: "<p>{{@222}}</p>"})
		}
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
		fields := fieldMap(o)
		if html, _ := fields["html"].(string); strings.Contains(html, includeCanary) {
			t.Errorf("html carries content outside the targets: %q", html)
		}
		return fields
	}
	for _, update := range []bool{false, true} {
		s := newIncludeServer(t, fixtures)
		s.written = 100
		if fields := write(t, boundClient(t, s.url, "book/1"), update); fields["html"] != "<p></p>" || fields["html_withheld"] != nil {
			t.Errorf("update %v: html = %q, html_withheld = %v", update, fields["html"], fields["html_withheld"])
		}

		s = newIncludeServer(t, fixtures)
		s.written = 100
		s.failing[222] = true
		fields := write(t, boundClient(t, s.url, "book/1"), update)
		if fields["html"] != "" || fields["html_withheld"] != true {
			t.Errorf("update %v, failed check: html = %q, html_withheld = %v", update, fields["html"], fields["html_withheld"])
		}
		if fields["raw_html"] != `<p>{{@222}}</p>` {
			t.Errorf("update %v: raw_html = %q", update, fields["raw_html"])
		}
	}
}
