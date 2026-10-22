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
)

func serveJSON(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	}
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

// A list row without a cover (null) and a read without one show no cover member.
func TestNullCoverStaysOut(t *testing.T) {
	server := serveJSON(t, map[string]string{
		"/api/books":   `{"data":[{"id":1,"name":"B","slug":"b","description":"","cover":null}],"total":1}`,
		"/api/books/1": `{"id":1,"name":"B","slug":"b","contents":[],"tags":[],"cover":null}`,
	})
	c := newClient(t, server.URL, nil)
	list, err := rowsJSON(c.ListBooks(context.Background(), 0, 0, listQuery{}))
	if err != nil {
		t.Fatal(err)
	}
	if v := at(t, list, 0).(map[string]any)["cover"]; v != nil {
		t.Errorf("books.list cover = %v", v)
	}
	book, err := objectJSON(c.GetBook(context.Background(), 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := book.(map[string]any)["cover"]; ok {
		t.Error("books.get shows a null cover")
	}
}

func commentNode(id, depth int, children ...string) string {
	n := strconv.Itoa(id)
	return `{"comment":{"id":` + n + `,"commentable_id":306,"commentable_type":"page","html":"<p>c</p>","parent_id":null,` +
		`"local_id":` + n + `,"archived":false,"created_at":"t","updated_at":"t"},"depth":` + strconv.Itoa(depth) +
		`,"children":[` + strings.Join(children, ",") + `]}`
}

func readComments(t *testing.T, tree string) map[string]any {
	t.Helper()
	server := serveJSON(t, map[string]string{
		"/api/pages/306": `{"id":306,"book_id":1,"name":"P","slug":"p","html":"","markdown":"","comments":` + tree + `}`,
	})
	got, err := objectJSON(newClient(t, server.URL, nil).GetPage(context.Background(), "306"))
	if err != nil {
		t.Fatal(err)
	}
	return got.(map[string]any)
}

func countNodes(v any) int {
	n := 0
	for _, node := range v.([]any) {
		n += 1 + countNodes(node.(map[string]any)["children"])
	}
	return n
}

func TestCommentTreeIsCappedAndMarked(t *testing.T) {
	t.Run("500 comments over both lists", func(t *testing.T) {
		var active, archived []string
		for i := 1; i <= 400; i++ {
			active = append(active, commentNode(i, 0))
		}
		for i := 401; i <= 600; i++ {
			archived = append(archived, commentNode(i, 0))
		}
		page := readComments(t, `{"active":[`+strings.Join(active, ",")+`],"archived":[`+strings.Join(archived, ",")+`]}`)
		comments := page["comments"].(map[string]any)
		if a, b := countNodes(comments["active"]), countNodes(comments["archived"]); a != 400 || b != 100 {
			t.Errorf("shown = %d active, %d archived", a, b)
		}
		if page["comments_truncated"] != true {
			t.Error("a cut tree is not marked")
		}
	})
	t.Run("10 levels", func(t *testing.T) {
		node := commentNode(12, 11)
		for level := 10; level >= 0; level-- {
			node = commentNode(level+1, level, node)
		}
		page := readComments(t, `{"active":[`+node+`],"archived":[]}`)
		if n := countNodes(page["comments"].(map[string]any)["active"]); n != 10 {
			t.Errorf("shown = %d levels, want 10", n)
		}
		if page["comments_truncated"] != true {
			t.Error("a cut tree is not marked")
		}
	})
	t.Run("exactly 10 levels and 500 comments are not cut", func(t *testing.T) {
		node := commentNode(10, 9)
		for level := 8; level >= 0; level-- {
			node = commentNode(level+1, level, node)
		}
		page := readComments(t, `{"active":[`+node+`],"archived":[]}`)
		if _, ok := page["comments_truncated"]; ok {
			t.Error("an uncut tree is marked")
		}
	})
	t.Run("a long text is cut like in comments.get", func(t *testing.T) {
		long := strings.Replace(commentNode(1, 0), "<p>c</p>", strings.Repeat("x", maxCommentHTMLBytes+10), 1)
		page := readComments(t, `{"active":[`+long+`],"archived":[]}`)
		html := at(t, page, "comments", "active", 0, "comment", "html").(string)
		if len(html) != maxCommentHTMLBytes || page["comments_truncated"] != true {
			t.Errorf("html = %d bytes, marked = %v", len(html), page["comments_truncated"])
		}
	})
	t.Run("no comments", func(t *testing.T) {
		page := readComments(t, `{"active":[],"archived":[]}`)
		if _, ok := page["comments"]; ok {
			t.Error("an empty tree is shown")
		}
	})
}

const searchHitsBody = `{"total":3,"data":[
 {"id":5,"book_id":1,"chapter_id":75,"name":"P","slug":"p","type":"page","url":"u","draft":true,"template":false,
  "created_at":"2021-05-15T16:28:10.000000Z","updated_at":"2021-11-14T15:56:49.000000Z",
  "book":{"id":1,"name":"Cats","slug":"cats"},"chapter":{"id":75,"name":"A chapter","slug":"a-chapter"},
  "preview_html":{"name":"P","content":"c"},"tags":[{"name":"Animal","value":"Cat","order":0},{"name":"K","value":"V","order":2}]},
 {"id":84,"book_id":1,"name":"C","slug":"c","type":"chapter","url":"u","book":{"id":1,"name":"Cats","slug":"cats"},
  "created_at":"t","updated_at":"t","preview_html":{"name":"C","content":"c"},"tags":[]}]}`

func searchResults(t *testing.T, body string, targets ...string) []map[string]any {
	t.Helper()
	server := serveJSON(t, map[string]string{
		"/api/search":            body,
		"/api/search/book/1":     body,
		"/api/search/chapter/75": body,
		"/api/books/1":           `{"id":1,"shelves":[]}`,
		"/api/chapters/75":       `{"id":75,"book_id":1}`,
		"/api/pages/5":           `{"id":5,"book_id":1,"chapter_id":75}`,
	})
	var c *Client
	if len(targets) == 0 {
		c = newClient(t, server.URL, nil)
	} else {
		c = boundClient(t, server.URL, targets...)
	}
	target := listTarget{}
	if len(targets) > 0 {
		target = listTarget{BookID: 1}
		if targets[0] == "page/5" {
			target = listTarget{pagesOnly: true}
		}
	}
	got, err := c.Search(context.Background(), "cats", target, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	return got.Fields[0].Value.([]map[string]any)
}

func TestSearchHitsShowEveryDocumentedField(t *testing.T) {
	results := searchResults(t, searchHitsBody)
	page, _ := json.Marshal(results[0])
	var got map[string]any
	_ = json.Unmarshal(page, &got)
	for key, want := range map[string]any{
		"draft": true, "template": false, "created_at": "2021-05-15T16:28:10.000000Z",
		"updated_at": "2021-11-14T15:56:49.000000Z",
		"book":       map[string]any{"id": 1.0, "name": "Cats", "slug": "cats"},
		"chapter":    map[string]any{"id": 75.0, "name": "A chapter", "slug": "a-chapter"},
		"tags": []any{map[string]any{"name": "Animal", "value": "Cat", "order": 0.0},
			map[string]any{"name": "K", "value": "V", "order": 2.0}},
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	for _, key := range []string{"draft", "template", "chapter"} {
		if _, ok := results[1][key]; ok {
			t.Errorf("a chapter hit shows %s", key)
		}
	}
}

func TestSearchHitLinksNameOnlyBoundObjects(t *testing.T) {
	t.Run("book bound: book named, chapter by id", func(t *testing.T) {
		r := searchResults(t, searchHitsBody, "book/1")[0]
		if !reflect.DeepEqual(r["book"], map[string]any{"id": int64(1), "name": "Cats", "slug": "cats"}) ||
			!reflect.DeepEqual(r["chapter"], map[string]any{"id": int64(75)}) {
			t.Errorf("book = %v, chapter = %v", r["book"], r["chapter"])
		}
	})
	t.Run("chapter bound in an unbound book: both by id", func(t *testing.T) {
		server := serveJSON(t, map[string]string{
			"/api/search": searchHitsBody, "/api/chapters/75": `{"id":75,"book_id":1}`,
			"/api/search/chapter/75": searchHitsBody,
		})
		c := boundClient(t, server.URL, "chapter/75")
		got, err := c.Search(context.Background(), "cats", listTarget{ChapterID: 75}, 1, 20)
		if err != nil {
			t.Fatal(err)
		}
		r := got.Fields[0].Value.([]map[string]any)[0]
		if !reflect.DeepEqual(r["book"], map[string]any{"id": int64(1)}) {
			t.Errorf("book = %v, want id only", r["book"])
		}
		if !reflect.DeepEqual(r["chapter"], map[string]any{"id": int64(75), "name": "A chapter", "slug": "a-chapter"}) {
			t.Errorf("chapter = %v", r["chapter"])
		}
	})
	t.Run("page bound: both by id", func(t *testing.T) {
		r := searchResults(t, searchHitsBody, "page/5")[0]
		if !reflect.DeepEqual(r["book"], map[string]any{"id": int64(1)}) ||
			!reflect.DeepEqual(r["chapter"], map[string]any{"id": int64(75)}) {
			t.Errorf("book = %v, chapter = %v", r["book"], r["chapter"])
		}
	})
}

func TestSearchHitMembersAreInSchema(t *testing.T) {
	results := searchResults(t, searchHitsBody)
	encoded, _ := json.Marshal(map[string]any{"results": results, "total": 2})
	var value, schema map[string]any
	_ = json.Unmarshal(encoded, &value)
	if err := json.Unmarshal(contentSearch.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if missing := keysNotDeclared(value, schema, "$"); len(missing) > 0 {
		t.Errorf("not declared: %v", missing)
	}
}
