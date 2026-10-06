package bookstack

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxTreeEntries bounds the entries of a content tree or chapter page list in one result.
	maxTreeEntries = 2000
	// maxDescriptionChars bounds a description of a book or chapter.
	maxDescriptionChars = 20000
)

var (
	booksList = capability.Descriptor{
		ID: Provider + ".books.list", Version: 1, Title: "List BookStack books",
		Description: "List the books of a knowledge base; a bound connection lists only its books",
		Tags:        []string{"knowledge", "books", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","slug","description","created_at","updated_at"]}}`),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of books to return; 0 returns all"},
			{Name: "offset", Description: "Number of books to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Book identifier"},
			{Name: "name", Description: "Book title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
		},
		Examples: []capability.Example{{Description: "List the first 25 books", Arguments: json.RawMessage(`{"limit":25,"offset":0}`)}},
	}

	booksGet = capability.Descriptor{
		ID: Provider + ".books.get", Version: 1, Title: "Get a BookStack book",
		Description: "Read one book with its content tree of chapters and pages; the tree holds at most 2000 entries",
		Tags:        []string{"knowledge", "books", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"default_template_id":{"type":"integer"},"tags":{"type":"array"},"contents":{"type":"array"},"shelves":{"type":"array"},"truncated":{"type":"boolean"}},"required":["id","name","slug","contents","truncated"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "Book identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Book identifier"},
			{Name: "name", Description: "Book title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "description_html", Description: "Description as HTML, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "created_by", Description: "Creator with id and name"},
			{Name: "updated_by", Description: "Last editor with id and name"},
			{Name: "owned_by", Description: "Owner with id and name"},
			{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
			{Name: "tags", Description: "At most 50 name/value pairs"},
			{Name: "contents", Description: "Chapters with their pages and pages, in book order; type chapter or page"},
			{Name: "shelves", Description: "Shelves holding the book with id, name, slug; only for a connection without targets"},
			{Name: "truncated", Description: "True when the content tree was cut at 2000 entries"},
		},
		Examples: []capability.Example{{Description: "Read book 7", Arguments: json.RawMessage(`{"id":7}`)}},
	}

	chaptersList = capability.Descriptor{
		ID: Provider + ".chapters.list", Version: 1, Title: "List BookStack chapters",
		Description: "List chapters of a knowledge base or of one book",
		Tags:        []string{"knowledge", "chapters", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"book_id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"book_id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","book_id","name","slug","description","created_at","updated_at"]}}`),
		Arguments: []capability.Argument{
			{Name: "book_id", Description: "Only chapters of this book; required for a connection bound to several books"},
			{Name: "limit", Description: "Maximum number of chapters to return; 0 returns all"},
			{Name: "offset", Description: "Number of chapters to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Chapter identifier"},
			{Name: "book_id", Description: "Identifier of the containing book"},
			{Name: "name", Description: "Chapter title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
		},
		Examples: []capability.Example{{Description: "List the chapters of book 7", Arguments: json.RawMessage(`{"book_id":7}`)}},
	}

	chaptersGet = capability.Descriptor{
		ID: Provider + ".chapters.get", Version: 1, Title: "Get a BookStack chapter",
		Description: "Read one chapter with its pages; the page list holds at most 2000 entries",
		Tags:        []string{"knowledge", "chapters", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"book_id":{"type":"integer"},"book_slug":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"default_template_id":{"type":"integer"},"tags":{"type":"array"},"pages":{"type":"array"},"truncated":{"type":"boolean"}},"required":["id","book_id","name","slug","pages","truncated"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "Chapter identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Chapter identifier"},
			{Name: "book_id", Description: "Identifier of the containing book"},
			{Name: "book_slug", Description: "URL slug of the containing book"},
			{Name: "name", Description: "Chapter title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "description_html", Description: "Description as HTML, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "created_by", Description: "Creator with id and name"},
			{Name: "updated_by", Description: "Last editor with id and name"},
			{Name: "owned_by", Description: "Owner with id and name"},
			{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
			{Name: "tags", Description: "At most 50 name/value pairs"},
			{Name: "pages", Description: "Pages of the chapter in order"},
			{Name: "truncated", Description: "True when the page list was cut at 2000 entries"},
		},
		Examples: []capability.Example{{Description: "Read chapter 12", Arguments: json.RawMessage(`{"id":12}`)}},
	}
)

type userJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UnmarshalJSON accepts the user object and, for list-shaped payloads, a bare identifier.
func (u *userJSON) UnmarshalJSON(data []byte) error {
	var id int64
	if json.Unmarshal(data, &id) == nil {
		*u = userJSON{ID: id}
		return nil
	}
	var object struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*u = userJSON{ID: object.ID, Name: object.Name}
	return nil
}

type tagJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// entryJSON is a page or chapter inside a content tree; pages only appear below a chapter.
type entryJSON struct {
	Type      string      `json:"type"`
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Slug      string      `json:"slug"`
	ChapterID int64       `json:"chapter_id"`
	BookID    int64       `json:"book_id"`
	Draft     bool        `json:"draft"`
	Template  bool        `json:"template"`
	UpdatedAt string      `json:"updated_at"`
	Pages     []entryJSON `json:"pages"`
}

type bookJSON struct {
	ID                int64       `json:"id"`
	Name              string      `json:"name"`
	Slug              string      `json:"slug"`
	Description       string      `json:"description"`
	DescriptionHTML   string      `json:"description_html"`
	CreatedAt         string      `json:"created_at"`
	UpdatedAt         string      `json:"updated_at"`
	CreatedBy         *userJSON   `json:"created_by"`
	UpdatedBy         *userJSON   `json:"updated_by"`
	OwnedBy           *userJSON   `json:"owned_by"`
	DefaultTemplateID int64       `json:"default_template_id"`
	Contents          []entryJSON `json:"contents"`
	Tags              []tagJSON   `json:"tags"`
	Shelves           []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"shelves"`
}

type chapterJSON struct {
	ID                int64       `json:"id"`
	BookID            int64       `json:"book_id"`
	BookSlug          string      `json:"book_slug"`
	Name              string      `json:"name"`
	Slug              string      `json:"slug"`
	Description       string      `json:"description"`
	DescriptionHTML   string      `json:"description_html"`
	CreatedAt         string      `json:"created_at"`
	UpdatedAt         string      `json:"updated_at"`
	CreatedBy         *userJSON   `json:"created_by"`
	UpdatedBy         *userJSON   `json:"updated_by"`
	OwnedBy           *userJSON   `json:"owned_by"`
	DefaultTemplateID int64       `json:"default_template_id"`
	Tags              []tagJSON   `json:"tags"`
	Pages             []entryJSON `json:"pages"`
}

// listedJSON is a book or chapter row of a list.
type listedJSON struct {
	ID          int64  `json:"id"`
	BookID      int64  `json:"book_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func invokeBooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListBooks(ctx, in.Limit, in.Offset)
}

func invokeBooksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := bound.checkBook(in.ID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetBook(ctx, in.ID)
}

func invokeChaptersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		BookID *int64 `json:"book_id"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	var target listTarget
	if in.BookID != nil {
		target.BookID = *in.BookID
		if target.BookID <= 0 {
			return nil, invalidRequest("book_id must be a positive integer")
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if _, err := bound.listBook(target); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListChapters(ctx, target.BookID, in.Limit, in.Offset)
}

func invokeChaptersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.ID <= 0 {
		return nil, invalidRequest("id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetChapter(ctx, in.ID)
}

// ListBooks returns books. On a bound connection the rows are filtered here to the bound books, so limit
// and offset count the remaining rows.
func (c *Client) ListBooks(ctx context.Context, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[listedJSON]{
		op: "list books", path: "/api/books", filtered: c.scope.bound(), limit: limit, offset: offset,
		arguments: argNames(booksList),
		id:        func(b listedJSON) int64 { return b.ID },
		keep:      func(b listedJSON) bool { return !c.scope.bound() || c.scope.allows(b.ID) },
		row: func(b listedJSON) output.Row {
			return output.Row{
				"id": b.ID, "name": clip(b.Name, maxResultString), "slug": clip(b.Slug, maxResultString),
				"description": clip(b.Description, maxDescriptionChars), "created_at": b.CreatedAt, "updated_at": b.UpdatedAt,
			}
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(booksList), Rows: rows}, nil
}

// ListChapters returns chapters, of one book when bookID is given. A bound connection always has a book here
// (the argument or its only book); every row is checked against that book on the client.
func (c *Client) ListChapters(ctx context.Context, bookID int64, limit, offset int) (output.Collection, error) {
	target, err := c.scope.listBook(listTarget{BookID: bookID})
	if err != nil {
		return output.Collection{}, err
	}
	query := url.Values{}
	if target.BookID != 0 {
		query.Set("filter[book_id]", strconv.FormatInt(target.BookID, 10))
	}
	rows, err := scanList(ctx, c, scanSpec[listedJSON]{
		op: "list chapters", path: "/api/chapters", query: query, filtered: target.BookID != 0,
		limit: limit, offset: offset, narrow: "book_id", arguments: argNames(chaptersList),
		id:   func(ch listedJSON) int64 { return ch.ID },
		keep: func(ch listedJSON) bool { return target.BookID == 0 || ch.BookID == target.BookID },
		row: func(ch listedJSON) output.Row {
			return output.Row{
				"id": ch.ID, "book_id": ch.BookID, "name": clip(ch.Name, maxResultString), "slug": clip(ch.Slug, maxResultString),
				"description": clip(ch.Description, maxDescriptionChars), "created_at": ch.CreatedAt, "updated_at": ch.UpdatedAt,
			}
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(chaptersList), Rows: rows}, nil
}

// GetBook returns one book with its content tree. The book is checked against the targets before any request.
func (c *Client) GetBook(ctx context.Context, id int64) (output.Object, error) {
	if err := c.scope.checkBook(id); err != nil {
		return output.Object{}, err
	}
	var book bookJSON
	if err := c.get(ctx, "get book", "/api/books/"+strconv.FormatInt(id, 10), nil, &book, nil, provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	budget := maxTreeEntries
	contents := make([]map[string]any, 0, len(book.Contents))
	truncated := false
	for _, e := range book.Contents {
		if budget <= 0 {
			truncated = true
			break
		}
		budget--
		item := treeEntry(e)
		if e.Type == "chapter" {
			pages, cut := treePages(e.Pages, &budget)
			item["pages"] = pages
			truncated = truncated || cut
		}
		contents = append(contents, item)
	}
	fields := []output.Field{
		{Name: "id", Value: book.ID}, {Name: "name", Value: clip(book.Name, maxResultString)},
		{Name: "slug", Value: clip(book.Slug, maxResultString)},
		{Name: "description", Value: clip(book.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(book.DescriptionHTML, maxDescriptionChars)},
		{Name: "created_at", Value: book.CreatedAt}, {Name: "updated_at", Value: book.UpdatedAt},
		{Name: "created_by", Value: reduceUser(book.CreatedBy)}, {Name: "updated_by", Value: reduceUser(book.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(book.OwnedBy)},
		{Name: "default_template_id", Value: book.DefaultTemplateID},
		{Name: "tags", Value: reduceTags(book.Tags)},
		{Name: "contents", Value: contents},
	}
	// Shelves are instance-wide, so a bound connection never shows them.
	if !c.scope.bound() {
		shelves := make([]map[string]any, 0, len(book.Shelves))
		for _, s := range book.Shelves {
			shelves = append(shelves, map[string]any{"id": s.ID, "name": clip(s.Name, maxResultString), "slug": clip(s.Slug, maxResultString)})
		}
		fields = append(fields, output.Field{Name: "shelves", Value: shelves})
	}
	fields = append(fields, output.Field{Name: "truncated", Value: truncated})
	return output.Object{Fields: fields}, nil
}

// GetChapter returns one chapter. On a bound connection this single read is also the evidence that the
// chapter belongs to one of the books; a foreign chapter is refused before anything is shown.
func (c *Client) GetChapter(ctx context.Context, id int64) (output.Object, error) {
	var chapter chapterJSON
	if err := c.get(ctx, "get chapter", "/api/chapters/"+strconv.FormatInt(id, 10), nil, &chapter, nil, provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	if c.scope.bound() && !c.scope.allows(chapter.BookID) {
		return output.Object{}, invalidRequest(outsideChapter)
	}
	budget := maxTreeEntries
	pages, truncated := treePages(chapter.Pages, &budget)
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: chapter.ID}, {Name: "book_id", Value: chapter.BookID},
		{Name: "book_slug", Value: clip(chapter.BookSlug, maxResultString)},
		{Name: "name", Value: clip(chapter.Name, maxResultString)}, {Name: "slug", Value: clip(chapter.Slug, maxResultString)},
		{Name: "description", Value: clip(chapter.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(chapter.DescriptionHTML, maxDescriptionChars)},
		{Name: "created_at", Value: chapter.CreatedAt}, {Name: "updated_at", Value: chapter.UpdatedAt},
		{Name: "created_by", Value: reduceUser(chapter.CreatedBy)}, {Name: "updated_by", Value: reduceUser(chapter.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(chapter.OwnedBy)},
		{Name: "default_template_id", Value: chapter.DefaultTemplateID},
		{Name: "tags", Value: reduceTags(chapter.Tags)},
		{Name: "pages", Value: pages}, {Name: "truncated", Value: truncated},
	}}, nil
}

// treeEntry shows one page or chapter of a tree without its children.
func treeEntry(e entryJSON) map[string]any {
	kind := e.Type
	if kind == "" {
		kind = "page"
	}
	item := map[string]any{
		"type": clip(kind, 20), "id": e.ID, "name": clip(e.Name, maxResultString), "slug": clip(e.Slug, maxResultString),
		"updated_at": e.UpdatedAt,
	}
	if kind == "page" {
		item["chapter_id"] = e.ChapterID
		item["draft"] = e.Draft
		item["template"] = e.Template
	}
	return item
}

// treePages shows the pages of a chapter, each one against the shared entry budget; truncated is true when
// the budget ran out first.
func treePages(pages []entryJSON, budget *int) ([]map[string]any, bool) {
	out := make([]map[string]any, 0, len(pages))
	for _, p := range pages {
		if *budget <= 0 {
			return out, true
		}
		*budget--
		p.Type = "page"
		out = append(out, treeEntry(p))
	}
	return out, false
}

// reduceUser keeps only the identifier and the name of a user object.
func reduceUser(u *userJSON) map[string]any {
	if u == nil {
		return map[string]any{"id": int64(0), "name": ""}
	}
	return map[string]any{"id": u.ID, "name": clip(u.Name, maxResultString)}
}

func reduceTags(tags []tagJSON) []map[string]string {
	out := make([]map[string]string, 0, len(tags))
	for _, t := range tags {
		if len(out) >= maxResultTags {
			break
		}
		out = append(out, map[string]string{"name": clip(t.Name, maxResultString), "value": clip(t.Value, maxResultString)})
	}
	return out
}
