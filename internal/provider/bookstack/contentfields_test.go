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
	 "comments":{"active":[{"comment":{"id":22,"commentable_id":306,"commentable_type":"page",
	  "html":"<p>Does this need revising?</p>","parent_id":null,"local_id":1,
	  "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":1,
	  "created_at":"2023-06-07T07:50:56.000000Z","updated_at":"2023-06-07T07:50:56.000000Z","content_ref":"","archived":false},
	  "depth":0,"children":[{"comment":{"id":34,"commentable_id":306,"commentable_type":"page",
	  "html":"<p>I think it's okay!</p>","parent_id":1,"local_id":2,
	  "created_by":{"id":2,"name":"Editor","slug":"editor"},"updated_by":1,
	  "created_at":"2023-06-07T13:46:25.000000Z","updated_at":"2023-06-07T13:46:25.000000Z","content_ref":"bkmrk-x","archived":false},
	  "depth":1,"children":[]}]}],
	 "archived":[{"comment":{"id":21,"commentable_id":306,"commentable_type":"page","html":"<p>Fix the title</p>",
	  "parent_id":null,"local_id":3,"created_by":{"id":2,"name":"Editor","slug":"editor"},"updated_by":1,
	  "created_at":"2023-06-07T07:50:49.000000Z","updated_at":"2025-10-24T08:37:22.000000Z","content_ref":"","archived":true},
	  "depth":0,"children":[]}]},
	 "tags":[{"name":"Category","value":"Top Content","order":0},{"name":"Animal","value":"Cat","order":1}]}`

	fixtureBooksList = `{"data":[{"id":1,"name":"BookStack User Guide","slug":"bookstack-user-guide",
	 "description":"Guide","created_at":"2019-05-05T21:48:46.000000Z","updated_at":"2019-12-11T20:57:31.000000Z",
	 "created_by":1,"updated_by":4,"owned_by":3,"cover":{"id":9,"name":"c.jpg","url":"https://example.com/c.jpg"}}],"total":14}`

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
	 "cover":{"id":452,"name":"c.jpg","url":"https://example.com/c.jpg","created_at":"2020-01-12T14:11:51.000000Z",
	  "updated_at":"2020-01-12T14:12:51.000000Z","created_by":1,"updated_by":2,"path":"/uploads/c.jpg",
	  "type":"cover_book","uploaded_to":16},
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

	fixtureShelvesList = `{"data":[{"id":8,"name":"Qui qui","slug":"qui-qui","description":"Enim dolor",
	 "created_at":"2019-05-05T22:10:16.000000Z","updated_at":"2020-04-10T13:00:45.000000Z",
	 "created_by":4,"updated_by":1,"owned_by":2,"cover":{"id":4,"name":"shelf.jpg","url":"https://example.com/shelf.jpg"}}],"total":3}`

	fixtureShelfRead = `{"id":14,"name":"My shelf","slug":"my-shelf","description":"This is my shelf",
	 "description_html":"<p>This is my shelf</p>",
	 "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":2,"name":"Editor","slug":"editor"},
	 "owned_by":{"id":3,"name":"Owner","slug":"owner"},
	 "created_at":"2020-04-10T13:24:09.000000Z","updated_at":"2020-04-10T13:31:04.000000Z",
	 "tags":[{"name":"Category","value":"Guide","order":0}],
	 "cover":{"id":501,"name":"c.jpg","url":"http://bookstack.local/c.jpg","path":"/uploads/c.jpg",
	  "created_at":"2020-04-10T13:31:04.000000Z","updated_at":"2020-04-10T13:32:04.000000Z","created_by":1,
	  "updated_by":2,"type":"cover_bookshelf","uploaded_to":14},
	 "books":[{"id":5,"name":"Sint explicabo","slug":"jbsQrzuaXe","description":"Hic forum est.",
	  "created_at":"2020-04-10T13:31:04.000000Z","updated_at":"2020-04-11T13:31:04.000000Z",
	  "created_by":1,"updated_by":2,"owned_by":3}]}`

	fixtureImagesList = `{"data":[{"id":1,"name":"My cat scribbles",
	 "url":"https:\/\/bookstack.example.com\/uploads\/images\/gallery\/2023-02\/scribbles.jpg",
	 "path":"\/uploads\/images\/gallery\/2023-02\/scribbles.jpg","type":"gallery","uploaded_to":1,
	 "created_by":1,"updated_by":2,"created_at":"2023-02-12T16:34:57.000000Z","updated_at":"2023-02-12T16:34:58.000000Z"}],"total":3}`

	fixtureImageRead = `{"id":618,"name":"cute-cat-image.png",
	 "url":"https:\/\/bookstack.example.com\/uploads\/images\/gallery\/2023-03\/cute-cat-image.png",
	 "created_at":"2023-03-15T16:32:09.000000Z","updated_at":"2023-03-15T16:32:10.000000Z",
	 "created_by":{"id":1,"name":"Admin","slug":"admin"},"updated_by":{"id":2,"name":"Editor","slug":"editor"},
	 "path":"\/uploads\/images\/gallery\/2023-03\/cute-cat-image.png","type":"gallery","uploaded_to":1,
	 "thumbs":{"gallery":"https:\/\/b.example\/g.png","display":"https:\/\/b.example\/d.png"},
	 "content":{"html":"<a>x<\/a>","markdown":"![x](y)"}}`
)

// contentServer answers the read endpoints with the example responses. Shelf 2 holds book 1.
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
	mux.HandleFunc("/api/shelves", answer(fixtureShelvesList))
	mux.HandleFunc("/api/shelves/14", answer(fixtureShelfRead))
	mux.HandleFunc("/api/image-gallery", answer(fixtureImagesList))
	mux.HandleFunc("/api/image-gallery/618", answer(fixtureImageRead))
	mux.HandleFunc("/api/recycle-bin", answer(fixtureRecycleBin))
	mux.HandleFunc("/api/system", answer(fixtureSystem))
	mux.HandleFunc("/api/users", answer(fixtureUsersList))
	mux.HandleFunc("/api/users/1", answer(fixtureUserRead))
	mux.HandleFunc("/api/roles/26", answer(fixtureRoleRead))
	mux.HandleFunc("/api/audit-log", answer(fixtureAuditLog))
	mux.HandleFunc("/api/shelves/2", answer(`{"id":2,"books":[{"id":1}]}`))
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

const (
	fixtureRecycleBin = `{"data":[{"id":18,"deleted_by":1,"created_at":"2022-04-20T12:57:46.000000Z",
			 "updated_at":"2022-04-20T12:58:46.000000Z","deletable_type":"page","deletable_id":2582,
			 "deletable":{"id":2582,"book_id":25,"chapter_id":null,"name":"A Wonderful Page","slug":"a-wonderful-page",
			  "priority":9,"created_at":"2022-02-08T00:44:45.000000Z","updated_at":"2022-04-20T12:57:46.000000Z",
			  "created_by":1,"updated_by":1,"draft":false,"revision_count":1,"template":false,"owned_by":1,
			  "editor":"wysiwyg","book_slug":"a-great-book",
			  "parent":{"id":25,"name":"A Great Book","slug":"a-great-book","description":"d",
			   "created_at":"2022-01-24T16:14:28.000000Z","updated_at":"2022-03-06T15:14:50.000000Z",
			   "created_by":1,"updated_by":2,"owned_by":3,"type":"book"}}},
			 {"id":19,"deleted_by":1,"created_at":"c","deletable_type":"book","deletable_id":13,
			  "deletable":{"id":13,"name":"A Big Book!","slug":"a-big-book","description":"Big","created_at":"2021-11-08T11:26:43.000000Z",
			  "updated_at":"2022-04-25T16:07:47.000000Z","created_by":27,"updated_by":1,"owned_by":1,"pages_count":208,"chapters_count":50}}],"total":2}`

	fixtureSystem = `{"version":"v25.02.4","instance_id":"i","app_name":"A","app_logo":"https://docs.example.com/uploads/images/system/cat.png","base_url":"https://docs.example.com"}`

	fixtureUsersList = `{"data":[{"id":1,"name":"Dan","slug":"dan","profile_url":"https://docs.example.com/user/dan",
			 "edit_url":"https://docs.example.com/settings/users/1","avatar_url":"https://docs.example.com/uploads/images/user/a.jpg"}],"total":1}`

	fixtureUserRead = `{"id":1,"name":"Dan","slug":"dan","edit_url":"https://docs.example.com/settings/users/1",
			 "avatar_url":"https://docs.example.com/uploads/images/user/a.jpg","roles":[{"id":1,"display_name":"Admin"}]}`

	fixtureRoleRead = `{"id":26,"display_name":"Maintainer","permissions":["book-view-all"],
			 "users":[{"id":11,"name":"Barry Scott","slug":"barry-scott"}]}`

	fixtureAuditLog = `{"data":[{"id":1,"type":"auth_login","user_id":1,"created_at":"c","user":{"id":1,"name":"Admins","slug":"admins"}}],"total":1}`
)

// contentTool is one tool of the content fields with the call that produces its result.
type contentTool struct {
	descriptor capability.Descriptor
	call       func(*Client) (any, error)
}

func contentTools() []contentTool {
	ctx := context.Background()
	return []contentTool{
		{pagesList, func(c *Client) (any, error) { return rowsJSON(c.ListPages(ctx, listTarget{}, 0, 0, listQuery{})) }},
		{pagesGet, func(c *Client) (any, error) { return objectJSON(c.GetPage(ctx, "306")) }},
		{booksList, func(c *Client) (any, error) { return rowsJSON(c.ListBooks(ctx, 0, 0, listQuery{})) }},
		{booksGet, func(c *Client) (any, error) { return objectJSON(c.GetBook(ctx, 16)) }},
		{chaptersList, func(c *Client) (any, error) { return rowsJSON(c.ListChapters(ctx, 0, 0, 0, listQuery{})) }},
		{chaptersGet, func(c *Client) (any, error) { return objectJSON(c.GetChapter(ctx, 1)) }},
		{shelvesList, func(c *Client) (any, error) { return rowsJSON(c.ListShelves(ctx, 0, 0, listQuery{})) }},
		{shelvesGet, func(c *Client) (any, error) { return objectJSON(c.GetShelf(ctx, 14)) }},
		{imagesList, func(c *Client) (any, error) { return rowsJSON(c.ListImages(ctx, 0, "", 0, 0, listQuery{})) }},
		{imagesGet, func(c *Client) (any, error) { return objectJSON(c.GetImage(ctx, 618)) }},
		{recycleBinList, func(c *Client) (any, error) { return rowsJSON(c.ListRecycleBin(ctx, 0, 0, listQuery{})) }},
		{systemGet, func(c *Client) (any, error) { return objectJSON(c.GetSystem(ctx)) }},
		{usersList, func(c *Client) (any, error) { return rowsJSON(c.ListUsers(ctx, 0, 0, listQuery{})) }},
		{usersGet, func(c *Client) (any, error) { return objectJSON(c.GetUser(ctx, 1)) }},
		{rolesGet, func(c *Client) (any, error) { return objectJSON(c.GetRole(ctx, 26)) }},
		{auditLogList, func(c *Client) (any, error) {
			return rowsJSON(c.ListAuditLog(ctx, auditFilter{}, listQuery{}, 0, 0))
		}},
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
	if _, ok := page["comments_truncated"]; ok {
		t.Error("pages.get marks an uncut comment tree")
	}
	wantComments := map[string]any{
		"active": []any{map[string]any{
			"depth": 0.0,
			"comment": map[string]any{
				"id": 22.0, "commentable_id": 306.0, "commentable_type": "page", "html": "<p>Does this need revising?</p>",
				"local_id": 1.0, "archived": false, "created_by": detail(1, "Admin", "admin"), "updated_by": id(1),
				"created_at": "2023-06-07T07:50:56.000000Z", "updated_at": "2023-06-07T07:50:56.000000Z",
			},
			"children": []any{map[string]any{
				"depth": 1.0, "children": []any{},
				"comment": map[string]any{
					"id": 34.0, "commentable_id": 306.0, "commentable_type": "page", "html": "<p>I think it's okay!</p>",
					"parent_id": 1.0, "local_id": 2.0, "content_ref": "bkmrk-x", "archived": false,
					"created_by": detail(2, "Editor", "editor"), "updated_by": id(1),
					"created_at": "2023-06-07T13:46:25.000000Z", "updated_at": "2023-06-07T13:46:25.000000Z",
				},
			}},
		}},
		"archived": []any{map[string]any{
			"depth": 0.0, "children": []any{},
			"comment": map[string]any{
				"id": 21.0, "commentable_id": 306.0, "commentable_type": "page", "html": "<p>Fix the title</p>",
				"local_id": 3.0, "archived": true, "created_by": detail(2, "Editor", "editor"), "updated_by": id(1),
				"created_at": "2023-06-07T07:50:49.000000Z", "updated_at": "2025-10-24T08:37:22.000000Z",
			},
		}},
	}
	if !reflect.DeepEqual(page["comments"], wantComments) {
		t.Errorf("pages.get comments = %v", page["comments"])
	}

	book := at(t, results["bookstack.books.list"], 0).(map[string]any)
	if !reflect.DeepEqual(book["created_by"], id(1)) || !reflect.DeepEqual(book["updated_by"], id(4)) ||
		!reflect.DeepEqual(book["owned_by"], id(3)) {
		t.Errorf("books.list users = %v", book)
	}
	if want := map[string]any{"id": 9.0, "name": "c.jpg", "url": "https://example.com/c.jpg"}; !reflect.DeepEqual(book["cover"], want) {
		t.Errorf("books.list cover = %v", book["cover"])
	}

	read := results["bookstack.books.get"].(map[string]any)
	if !reflect.DeepEqual(read["created_by"], detail(1, "Admin", "admin")) ||
		!reflect.DeepEqual(read["owned_by"], detail(3, "Owner", "owner")) ||
		!reflect.DeepEqual(at(t, read, "tags", 0), map[string]any{"name": "Category", "value": "Guide", "order": 0.0}) {
		t.Errorf("books.get = %v", read)
	}
	wantCover := map[string]any{
		"id": 452.0, "name": "c.jpg", "url": "https://example.com/c.jpg", "path": "/uploads/c.jpg",
		"type": "cover_book", "uploaded_to": 16.0, "created_at": "2020-01-12T14:11:51.000000Z",
		"updated_at": "2020-01-12T14:12:51.000000Z", "created_by": id(1), "updated_by": id(2),
	}
	if !reflect.DeepEqual(read["cover"], wantCover) {
		t.Errorf("books.get cover = %v", read["cover"])
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

func TestShelfAndImageReadsShowEveryDocumentedField(t *testing.T) {
	c := newClient(t, contentServer(t).URL, nil)
	id := func(n float64) map[string]any { return map[string]any{"id": n} }
	detail := func(n float64, name, slug string) map[string]any {
		return map[string]any{"id": n, "name": name, "slug": slug}
	}
	check := func(label string, got any, want map[string]any) {
		t.Helper()
		for key, w := range want {
			if g := at(t, got, key); !reflect.DeepEqual(g, w) {
				t.Errorf("%s %s = %v, want %v", label, key, g, w)
			}
		}
	}

	list, err := rowsJSON(c.ListShelves(context.Background(), 0, 0, listQuery{}))
	if err != nil {
		t.Fatal(err)
	}
	row := at(t, list, 0)
	check("shelves.list", row, map[string]any{
		"id": 8.0, "name": "Qui qui", "description": "Enim dolor", "created_at": "2019-05-05T22:10:16.000000Z",
		"created_by": id(4), "updated_by": id(1), "owned_by": id(2),
	})
	check("shelves.list cover", row, map[string]any{"cover": map[string]any{"id": 4.0, "name": "shelf.jpg", "url": "https://example.com/shelf.jpg"}})

	shelf, err := objectJSON(c.GetShelf(context.Background(), 14))
	if err != nil {
		t.Fatal(err)
	}
	check("shelves.get", shelf, map[string]any{
		"created_by": detail(1, "Admin", "admin"), "updated_by": detail(2, "Editor", "editor"),
		"owned_by": detail(3, "Owner", "owner"), "truncated": false,
		"tags": []any{map[string]any{"name": "Category", "value": "Guide", "order": 0.0}},
		"books": []any{map[string]any{
			"id": 5.0, "name": "Sint explicabo", "slug": "jbsQrzuaXe", "description": "Hic forum est.",
			"created_at": "2020-04-10T13:31:04.000000Z", "updated_at": "2020-04-11T13:31:04.000000Z",
			"created_by": id(1), "updated_by": id(2), "owned_by": id(3),
		}},
	})
	check("shelves.get cover", shelf, map[string]any{"cover": map[string]any{
		"id": 501.0, "name": "c.jpg", "url": "http://bookstack.local/c.jpg", "path": "/uploads/c.jpg",
		"type": "cover_bookshelf", "uploaded_to": 14.0, "created_at": "2020-04-10T13:31:04.000000Z",
		"updated_at": "2020-04-10T13:32:04.000000Z", "created_by": id(1), "updated_by": id(2),
	}})

	images, err := rowsJSON(c.ListImages(context.Background(), 0, "", 0, 0, listQuery{}))
	if err != nil {
		t.Fatal(err)
	}
	check("images.list", at(t, images, 0), map[string]any{
		"id": 1.0, "type": "gallery", "page_id": 1.0, "path": "/uploads/images/gallery/2023-02/scribbles.jpg",
		"url":        "https://bookstack.example.com/uploads/images/gallery/2023-02/scribbles.jpg",
		"created_by": map[string]any{"id": 1.0, "name": ""}, "updated_by": map[string]any{"id": 2.0, "name": ""},
	})

	image, err := objectJSON(c.GetImage(context.Background(), 618))
	if err != nil {
		t.Fatal(err)
	}
	check("images.get", image, map[string]any{
		"path": "/uploads/images/gallery/2023-03/cute-cat-image.png", "page_id": 1.0,
		"created_by": detail(1, "Admin", "admin"), "updated_by": detail(2, "Editor", "editor"),
		"thumbs":  map[string]any{"gallery": "https://b.example/g.png", "display": "https://b.example/d.png"},
		"content": map[string]any{"html": "<a>x</a>", "markdown": "![x](y)"},
	})
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
		{shelvesList, []string{"id", "name", "slug", "description", "created_at", "updated_at"}},
		{imagesList, []string{"id", "name", "type", "page_id", "url", "created_by", "updated_by", "created_at", "updated_at"}},
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
	got, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 0, 0, listQuery{})
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
		list, err := c.ListPages(ctx, listTarget{ChapterID: 1}, 0, 0, listQuery{})
		if err != nil {
			t.Fatal(err)
		}
		pages = list.Rows[0]["book_slug"]
		if len(c.scope.chapters) == 0 {
			chapterList, err := c.ListChapters(ctx, 1, 0, 0, listQuery{})
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
	list, err := c.ListPages(ctx, listTarget{pagesOnly: true}, 0, 0, listQuery{})
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

// The administration reads show the documented members; the answers are shortened example responses.
func TestAdministrationReadsShowEveryDocumentedField(t *testing.T) {
	server := contentServer(t)
	c := newClient(t, server.URL, nil)
	ctx := context.Background()
	id := func(n int64) map[string]any { return map[string]any{"id": n} }

	bin, err := c.ListRecycleBin(ctx, 0, 0, listQuery{})
	if err != nil {
		t.Fatal(err)
	}
	wantPage := map[string]any{
		"priority": int64(9), "created_at": "2022-02-08T00:44:45.000000Z", "updated_at": "2022-04-20T12:57:46.000000Z",
		"created_by": id(1), "updated_by": id(1), "owned_by": id(1), "draft": false, "template": false,
		"revision_count": int64(1), "editor": "wysiwyg", "book_slug": "a-great-book",
		"parent": map[string]any{
			"name": "A Great Book", "slug": "a-great-book", "description": "d", "created_at": "2022-01-24T16:14:28.000000Z",
			"updated_at": "2022-03-06T15:14:50.000000Z", "created_by": id(1), "updated_by": id(2), "owned_by": id(3),
		},
	}
	if !reflect.DeepEqual(bin.Rows[0]["deletable"], wantPage) || bin.Rows[0]["updated_at"] != "2022-04-20T12:58:46.000000Z" {
		t.Errorf("recycle-bin page = %v", bin.Rows[0])
	}
	wantBook := map[string]any{
		"description": "Big", "created_at": "2021-11-08T11:26:43.000000Z", "updated_at": "2022-04-25T16:07:47.000000Z",
		"created_by": id(27), "updated_by": id(1), "owned_by": id(1),
	}
	if !reflect.DeepEqual(bin.Rows[1]["deletable"], wantBook) {
		t.Errorf("recycle-bin book = %v", bin.Rows[1]["deletable"])
	}
	if _, ok := bin.Rows[1]["updated_at"]; ok {
		t.Error("an entry without updated_at must not carry it")
	}

	system, err := objectJSON(c.GetSystem(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if at(t, system, "app_logo") != "https://docs.example.com/uploads/images/system/cat.png" {
		t.Errorf("system = %v", system)
	}

	users, err := c.ListUsers(ctx, 0, 0, listQuery{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := objectJSON(c.GetUser(ctx, 1))
	if err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]any{"users.list": map[string]any(users.Rows[0]), "users.get": user} {
		if at(t, got, "edit_url") != "https://docs.example.com/settings/users/1" ||
			at(t, got, "avatar_url") != "https://docs.example.com/uploads/images/user/a.jpg" {
			t.Errorf("%s = %v", label, got)
		}
	}

	role, err := objectJSON(c.GetRole(ctx, 26))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"id": 11.0, "name": "Barry Scott", "slug": "barry-scott"}; !reflect.DeepEqual(at(t, role, "users", 0), want) {
		t.Errorf("roles.get users = %v", at(t, role, "users"))
	}

	audit, err := c.ListAuditLog(ctx, auditFilter{}, listQuery{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Rows[0]["user_slug"] != "admins" || audit.Rows[0]["user_name"] != "Admins" {
		t.Errorf("audit-log = %v", audit.Rows[0])
	}
}

// Answers without the new members leave them out of the administration results.
func TestAdministrationResultsWithoutTheNewMembersStayAsTheyWere(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/recycle-bin":
			_, _ = w.Write([]byte(`{"data":[{"id":1,"deleted_by":2,"created_at":"c","deletable_type":"page","deletable_id":3,"deletable":{"name":"n","slug":"s"}}],"total":1}`))
		case "/api/system":
			_, _ = w.Write([]byte(`{"version":"v","app_name":"a","instance_id":"i","base_url":"b"}`))
		case "/api/users":
			_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"n","slug":"s"}],"total":1}`))
		case "/api/audit-log":
			_, _ = w.Write([]byte(`{"data":[{"id":1,"type":"t","created_at":"c","user":{"id":1,"name":"n"}}],"total":1}`))
		}
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	ctx := context.Background()
	bin, _ := c.ListRecycleBin(ctx, 0, 0, listQuery{})
	users, _ := c.ListUsers(ctx, 0, 0, listQuery{})
	audit, _ := c.ListAuditLog(ctx, auditFilter{}, listQuery{}, 0, 0)
	system, _ := c.GetSystem(ctx)
	for label, absent := range map[string][2]any{
		"recycle-bin": {map[string]any(bin.Rows[0]), []string{"updated_at", "deletable"}},
		"users":       {map[string]any(users.Rows[0]), []string{"edit_url", "avatar_url"}},
		"audit-log":   {map[string]any(audit.Rows[0]), []string{"user_slug"}},
	} {
		for _, key := range absent[1].([]string) {
			if _, ok := absent[0].(map[string]any)[key]; ok {
				t.Errorf("%s carries %s", label, key)
			}
		}
	}
	if len(system.Fields) != 4 {
		t.Errorf("system = %v", system.Fields)
	}
}
