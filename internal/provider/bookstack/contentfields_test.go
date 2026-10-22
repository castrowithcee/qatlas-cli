package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The answers below are the documented BookStack example responses, shortened to what the read tools show.
const (
	fixturePagesList = `{"data":[{"id":1,"book_id":1,"chapter_id":1,"name":"How to create page content",
	 "slug":"how-to-create-page-content","priority":0,"draft":false,"revision_count":3,"template":false,
	 "created_at":"2019-05-05T21:49:58.000000Z","updated_at":"2020-07-04T15:50:58.000000Z",
	 "created_by":1,"updated_by":2,"owned_by":3,"editor":"wysiwyg","book_slug":"example-book"}],"total":322}`

	fixturePageRead = `{"id":306,"book_id":1,"chapter_id":null,"name":"A page written in markdown",
	 "slug":"a-page-written-in-markdown","html":"<h1>x</h1>","raw_html":"<h1>y</h1>","priority":13,
	 "created_at":"2020-02-02T21:40:38.000000Z","updated_at":"2020-11-28T14:43:20.000000Z",
	 "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":2,"name":"Editor","slug":"editor"},
	 "owned_by":{"id":3,"name":"Owner","slug":"owner"},"draft":false,"markdown":"# m","revision_count":5,
	 "template":false,"editor":"wysiwyg",
	 "tags":[{"name":"Category","value":"Top Content","order":0},{"name":"Animal","value":"Cat","order":1}]}`

	fixtureBooksList = `{"data":[{"id":1,"name":"BookStack User Guide","slug":"bookstack-user-guide",
	 "description":"Guide","created_at":"2019-05-05T21:48:46.000000Z","updated_at":"2019-12-11T20:57:31.000000Z",
	 "created_by":1,"updated_by":4,"owned_by":3,"cover":null}],"total":14}`

	fixtureBookRead = `{"id":16,"name":"My own book","slug":"my-own-book","description":"d","description_html":"<p>d</p>",
	 "created_at":"2020-01-12T14:09:59.000000Z","updated_at":"2020-01-12T14:11:51.000000Z",
	 "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":2,"name":"Editor","slug":"editor"},
	 "owned_by":{"id":3,"name":"Owner","slug":"owner"},"default_template_id":null,
	 "contents":[{"id":50,"name":"Bridge Structures","slug":"bridge-structures","book_id":16,
	  "created_at":"2021-12-19T15:22:11.000000Z","updated_at":"2021-12-21T19:42:29.000000Z",
	  "url":"https://example.com/books/my-own-book/chapter/bridge-structures","type":"chapter",
	  "pages":[{"id":42,"name":"Building Bridges","slug":"building-bridges","book_id":16,"chapter_id":50,
	   "draft":false,"template":false,"created_at":"2021-12-19T15:22:11.000000Z",
	   "updated_at":"2022-09-29T13:44:15.000000Z","url":"https://example.com/books/my-own-book/page/building-bridges"}]},
	  {"id":43,"name":"Cool Animals","slug":"cool-animals","book_id":16,"chapter_id":null,"draft":false,
	   "template":false,"created_at":"2021-12-19T18:22:11.000000Z","updated_at":"2022-07-29T13:44:15.000000Z",
	   "url":"https://example.com/books/my-own-book/page/cool-animals","type":"page"}],
	 "tags":[{"name":"Category","value":"Guide","order":0}],
	 "cover":{"id":452,"name":"c.jpg","url":"https://example.com/c.jpg"},
	 "shelves":[{"id":1,"name":"Great reads","slug":"great-reads"}]}`

	fixtureChaptersList = `{"data":[{"id":1,"book_id":1,"name":"Content Creation","slug":"content-creation",
	 "description":"How","priority":3,"created_at":"2019-05-05T21:49:56.000000Z","updated_at":"2019-09-28T11:24:23.000000Z",
	 "created_by":1,"updated_by":2,"owned_by":3,"book_slug":"example-book"}],"total":40}`

	fixtureChapterRead = `{"id":1,"book_id":1,"slug":"content-creation","name":"Content Creation","description":"How",
	 "description_html":"<p>How</p>","default_template_id":25,"priority":3,
	 "created_at":"2019-05-05T21:49:56.000000Z","updated_at":"2019-09-28T11:24:23.000000Z",
	 "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":2,"name":"Editor","slug":"editor"},
	 "owned_by":{"id":3,"name":"Owner","slug":"owner"},"book_slug":"example-book",
	 "tags":[{"name":"Category","value":"Guide","order":0}],
	 "pages":[{"id":1,"book_id":1,"chapter_id":1,"name":"How to create page content","slug":"how-to-create-page-content",
	  "priority":0,"created_at":"2019-05-05T21:49:58.000000Z","updated_at":"2019-08-26T14:32:59.000000Z",
	  "created_by":1,"updated_by":2,"owned_by":3,"draft":false,"revision_count":2,"template":false,
	  "editor":"wysiwyg","book_slug":"example-book"}]}`
)

// contentServer answers the six read endpoints with the example responses. Shelf 2 holds book 1.
func contentServer(t *testing.T) *httptest.Server {
	t.Helper()
	answer := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pages", answer(fixturePagesList))
	mux.HandleFunc("/api/pages/306", answer(fixturePageRead))
	mux.HandleFunc("/api/books", answer(fixtureBooksList))
	mux.HandleFunc("/api/books/16", answer(fixtureBookRead))
	mux.HandleFunc("/api/chapters", answer(fixtureChaptersList))
	mux.HandleFunc("/api/chapters/1", answer(fixtureChapterRead))
	mux.HandleFunc("/api/shelves/2", answer(`{"id":2,"books":[{"id":1}]}`))
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

// contentTool is one tool of the content fields with the call that produces its result.
type contentTool struct {
	descriptor capability.Descriptor
	call       func(*Client) (any, error)
}

func contentTools() []contentTool {
	ctx := context.Background()
	return []contentTool{
		{pagesList, func(c *Client) (any, error) { return rowsJSON(c.ListPages(ctx, listTarget{}, 0, 0)) }},
		{pagesGet, func(c *Client) (any, error) { return objectJSON(c.GetPage(ctx, "306")) }},
		{booksList, func(c *Client) (any, error) { return rowsJSON(c.ListBooks(ctx, 0, 0)) }},
		{booksGet, func(c *Client) (any, error) { return objectJSON(c.GetBook(ctx, 16)) }},
		{chaptersList, func(c *Client) (any, error) { return rowsJSON(c.ListChapters(ctx, 0, 0, 0)) }},
		{chaptersGet, func(c *Client) (any, error) { return objectJSON(c.GetChapter(ctx, 1)) }},
	}
}

// keysNotDeclared walks a result next to its output schema and reports every member the schema does not
// declare, by path. A node without declared properties ends the walk there.
func keysNotDeclared(value any, schema map[string]any, path string) []string {
	var missing []string
	switch v := value.(type) {
	case map[string]any:
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return nil
		}
		for key, child := range v {
			declared, ok := properties[key].(map[string]any)
			if !ok {
				missing = append(missing, path+"."+key)
				continue
			}
			missing = append(missing, keysNotDeclared(child, declared, path+"."+key)...)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			missing = append(missing, keysNotDeclared(item, items, path+"[]")...)
		}
	}
	return missing
}

// Every member a content read shows is declared in the output schema, and every top-level member of the
// result and of the schema is a documented field. Add a tool to contentTools to cover it.
func TestEveryContentMemberIsInSchemaAndFields(t *testing.T) {
	server := contentServer(t)
	c := newClient(t, server.URL, nil)
	for _, tool := range contentTools() {
		t.Run(tool.descriptor.ID, func(t *testing.T) {
			got, err := tool.call(c)
			if err != nil {
				t.Fatal(err)
			}
			result := got
			var schema map[string]any
			if err := json.Unmarshal(tool.descriptor.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			if rows, ok := result.([]any); ok {
				result, schema = rows[0], schema["items"].(map[string]any)
			}
			fields := map[string]bool{}
			for _, f := range tool.descriptor.Fields {
				fields[f.Name] = true
			}
			var problems []string
			for key := range result.(map[string]any) {
				if !fields[key] {
					problems = append(problems, key+" (fields)")
				}
			}
			for key := range schema["properties"].(map[string]any) {
				if !fields[key] {
					problems = append(problems, key+" (schema member without field)")
				}
			}
			problems = append(problems, keysNotDeclared(result, schema, "$")...)
			sort.Strings(problems)
			if len(problems) > 0 {
				t.Errorf("not declared: %v", problems)
			}
		})
	}
}

// at follows keys and indexes into a decoded result.
func at(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch s := step.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("no object before %q", s)
			}
			value = object[s]
		case int:
			list, ok := value.([]any)
			if !ok || s >= len(list) {
				t.Fatalf("no item %d", s)
			}
			value = list[s]
		}
	}
	return value
}

func TestContentReadsShowEveryDocumentedField(t *testing.T) {
	c := newClient(t, contentServer(t).URL, nil)
	results := map[string]any{}
	for _, tool := range contentTools() {
		got, err := tool.call(c)
		if err != nil {
			t.Fatalf("%s: %v", tool.descriptor.ID, err)
		}
		results[tool.descriptor.ID] = got
	}
	id := func(n float64) map[string]any { return map[string]any{"id": n} }
	detail := func(n float64, name, slug string) map[string]any {
		return map[string]any{"id": n, "name": name, "slug": slug}
	}

	row := at(t, results["bookstack.pages.list"], 0).(map[string]any)
	for key, want := range map[string]any{
		"revision_count": 3.0, "created_by": id(1), "updated_by": id(2), "editor": "wysiwyg", "book_slug": "example-book",
		"owned_by": map[string]any{"id": 3.0, "name": ""}, "priority": 0.0, "name": "How to create page content",
	} {
		if !reflect.DeepEqual(row[key], want) {
			t.Errorf("pages.list %s = %v, want %v", key, row[key], want)
		}
	}

	page := results["bookstack.pages.get"].(map[string]any)
	for key, want := range map[string]any{
		"created_by": detail(1, "Admin", "admin"), "updated_by": detail(2, "Editor", "editor"),
		"owned_by": detail(3, "Owner", "owner"), "revision_count": 5.0, "chapter_id": 0.0,
		"tags": []any{
			map[string]any{"name": "Category", "value": "Top Content", "order": 0.0},
			map[string]any{"name": "Animal", "value": "Cat", "order": 1.0},
		},
	} {
		if !reflect.DeepEqual(page[key], want) {
			t.Errorf("pages.get %s = %v, want %v", key, page[key], want)
		}
	}
	if _, ok := page["book_slug"]; ok {
		t.Error("pages.get shows a book_slug the answer does not document")
	}

	book := at(t, results["bookstack.books.list"], 0).(map[string]any)
	if !reflect.DeepEqual(book["created_by"], id(1)) || !reflect.DeepEqual(book["updated_by"], id(4)) ||
		!reflect.DeepEqual(book["owned_by"], id(3)) {
		t.Errorf("books.list users = %v", book)
	}
	if _, ok := book["cover"]; ok {
		t.Error("books.list shows the cover")
	}

	read := results["bookstack.books.get"].(map[string]any)
	if !reflect.DeepEqual(read["created_by"], detail(1, "Admin", "admin")) ||
		!reflect.DeepEqual(read["owned_by"], detail(3, "Owner", "owner")) ||
		!reflect.DeepEqual(at(t, read, "tags", 0), map[string]any{"name": "Category", "value": "Guide", "order": 0.0}) {
		t.Errorf("books.get = %v", read)
	}
	if _, ok := read["cover"]; ok {
		t.Error("books.get shows the cover")
	}
	wantChapter := map[string]any{
		"type": "chapter", "id": 50.0, "name": "Bridge Structures", "slug": "bridge-structures", "book_id": 16.0,
		"created_at": "2021-12-19T15:22:11.000000Z", "updated_at": "2021-12-21T19:42:29.000000Z",
		"url": "https://example.com/books/my-own-book/chapter/bridge-structures",
		"pages": []any{map[string]any{
			"type": "page", "id": 42.0, "name": "Building Bridges", "slug": "building-bridges", "book_id": 16.0,
			"chapter_id": 50.0, "draft": false, "template": false, "created_at": "2021-12-19T15:22:11.000000Z",
			"updated_at": "2022-09-29T13:44:15.000000Z", "url": "https://example.com/books/my-own-book/page/building-bridges",
		}},
	}
	if got := at(t, read, "contents", 0); !reflect.DeepEqual(got, wantChapter) {
		t.Errorf("contents[0] = %v\nwant %v", got, wantChapter)
	}
	wantLoose := map[string]any{
		"type": "page", "id": 43.0, "name": "Cool Animals", "slug": "cool-animals", "book_id": 16.0, "chapter_id": 0.0,
		"draft": false, "template": false, "created_at": "2021-12-19T18:22:11.000000Z",
		"updated_at": "2022-07-29T13:44:15.000000Z", "url": "https://example.com/books/my-own-book/page/cool-animals",
	}
	if got := at(t, read, "contents", 1); !reflect.DeepEqual(got, wantLoose) {
		t.Errorf("contents[1] = %v\nwant %v", got, wantLoose)
	}

	chapterRow := at(t, results["bookstack.chapters.list"], 0).(map[string]any)
	for key, want := range map[string]any{
		"priority": 3.0, "created_by": id(1), "updated_by": id(2), "owned_by": id(3), "book_slug": "example-book",
	} {
		if !reflect.DeepEqual(chapterRow[key], want) {
			t.Errorf("chapters.list %s = %v, want %v", key, chapterRow[key], want)
		}
	}

	chapter := results["bookstack.chapters.get"].(map[string]any)
	for key, want := range map[string]any{
		"priority": 3.0, "created_by": detail(1, "Admin", "admin"), "updated_by": detail(2, "Editor", "editor"),
		"owned_by": detail(3, "Owner", "owner"), "default_template_id": 25.0,
		"tags": []any{map[string]any{"name": "Category", "value": "Guide", "order": 0.0}},
	} {
		if !reflect.DeepEqual(chapter[key], want) {
			t.Errorf("chapters.get %s = %v, want %v", key, chapter[key], want)
		}
	}
	wantPage := map[string]any{
		"type": "page", "id": 1.0, "name": "How to create page content", "slug": "how-to-create-page-content",
		"book_id": 1.0, "chapter_id": 1.0, "draft": false, "template": false, "priority": 0.0,
		"created_at": "2019-05-05T21:49:58.000000Z", "updated_at": "2019-08-26T14:32:59.000000Z",
		"created_by": id(1), "updated_by": id(2), "owned_by": id(3), "revision_count": 2.0, "editor": "wysiwyg",
		"book_slug": "example-book",
	}
	if got := at(t, chapter, "pages", 0); !reflect.DeepEqual(got, wantPage) {
		t.Errorf("pages[0] = %v\nwant %v", got, wantPage)
	}
}

// Columns keep their former order; the new ones follow.
func TestListColumnsKeepTheirOrderAndAppendTheNewOnes(t *testing.T) {
	for _, tc := range []struct {
		descriptor capability.Descriptor
		former     []string
	}{
		{pagesList, []string{"id", "name", "slug", "book_id", "chapter_id", "created_at", "updated_at", "priority", "draft", "template", "owned_by"}},
		{booksList, []string{"id", "name", "slug", "description", "created_at", "updated_at"}},
		{chaptersList, []string{"id", "book_id", "name", "slug", "description", "created_at", "updated_at"}},
	} {
		if got := fieldNames(tc.descriptor); !reflect.DeepEqual(got[:len(tc.former)], tc.former) || len(got) == len(tc.former) {
			t.Errorf("%s columns = %v", tc.descriptor.ID, got)
		}
	}
}

// A list answer without the new members leaves them out of the row.
func TestRowsWithoutTheNewMembersStayAsTheyWere(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":1,"book_id":2,"chapter_id":0,"name":"n","slug":"s"}],"total":1}`))
	}))
	defer server.Close()
	got, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"revision_count", "created_by", "updated_by", "editor", "book_slug"} {
		if value, ok := got.Rows[0][key]; ok {
			t.Errorf("%s = %v, want it absent", key, value)
		}
	}
}

// With targets, book_slug is shown only for a book that is itself bound; a book reached through a chapter or a
// page is shown by id only. A book on a bound shelf counts once the call has read that shelf.
func TestBookSlugFollowsTheBindingOfTheBook(t *testing.T) {
	server := contentServer(t)
	ctx := context.Background()
	slugOf := func(targets ...string) (pages, chapters, chapter any) {
		c := boundClient(t, server.URL, targets...)
		list, err := c.ListPages(ctx, listTarget{ChapterID: 1}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		pages = list.Rows[0]["book_slug"]
		if len(c.scope.chapters) == 0 {
			chapterList, err := c.ListChapters(ctx, 1, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			chapters = chapterList.Rows[0]["book_slug"]
		}
		got, err := c.GetChapter(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		fields := fieldMap(got)
		shown, _ := objectJSON(got, nil)
		return pages, chapters, []any{fields["book_slug"], at(t, shown, "pages", 0, "book_slug"), fields["book_id"]}
	}

	pages, chapters, chapter := slugOf("book/1")
	if pages != "example-book" || chapters != "example-book" ||
		!reflect.DeepEqual(chapter, []any{"example-book", "example-book", int64(1)}) {
		t.Errorf("book target: %v %v %v", pages, chapters, chapter)
	}

	pages, _, chapter = slugOf("chapter/1")
	if pages != nil || !reflect.DeepEqual(chapter, []any{nil, nil, int64(1)}) {
		t.Errorf("chapter target: %v %v", pages, chapter)
	}

	pages, chapters, chapter = slugOf("shelf/2")
	if pages != "example-book" || chapters != "example-book" ||
		!reflect.DeepEqual(chapter, []any{"example-book", "example-book", int64(1)}) {
		t.Errorf("shelf target: %v %v %v", pages, chapters, chapter)
	}

	// A page target reaches a page, never its book: the row keeps book_id only.
	c := boundClient(t, server.URL, "page/1")
	list, err := c.ListPages(ctx, listTarget{pagesOnly: true}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.Rows[0]["book_slug"]; ok || list.Rows[0]["book_id"] != int64(1) {
		t.Errorf("page target row = %v", list.Rows[0])
	}
}

// Free texts of the new members are cut like the existing ones.
func TestNewTextMembersAreClipped(t *testing.T) {
	long := strings.Repeat("x", maxResultString+10)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":16,"contents":[{"id":1,"type":"page","url":"` + long + `"}],
		 "tags":[{"name":"` + long + `","value":"v","order":2}]}`))
	}))
	defer server.Close()
	got, err := newClient(t, server.URL, nil).GetBook(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := objectJSON(got, nil)
	if n := len(at(t, m, "contents", 0, "url").(string)); n != maxResultString {
		t.Errorf("url length = %d", n)
	}
	if n := len(at(t, m, "tags", 0, "name").(string)); n != maxResultString {
		t.Errorf("tag name length = %d", n)
	}
	if at(t, m, "tags", 0, "order") != 2.0 {
		t.Errorf("tag order = %v", at(t, m, "tags", 0, "order"))
	}
}
