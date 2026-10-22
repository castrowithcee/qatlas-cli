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
		Description: "List the books of a knowledge base; a connection with targets lists only its books and the books on its shelves",
		Tags:        []string{"knowledge", "books", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"properties":{"id":{"type":"integer"}}},"updated_by":{"properties":{"id":{"type":"integer"}}},"owned_by":{"properties":{"id":{"type":"integer"}}}},"required":["id","name","slug","description","created_at","updated_at"]}}`),
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
			{Name: "created_by", Description: "Creator as id only"},
			{Name: "updated_by", Description: "Last editor as id only"},
			{Name: "owned_by", Description: "Owner as id only"},
		},
		Examples: []capability.Example{{Description: "List the first 25 books", Arguments: json.RawMessage(`{"limit":25,"offset":0}`)}},
	}

	booksGet = capability.Descriptor{
		ID: Provider + ".books.get", Version: 1, Title: "Get a BookStack book",
		Description: "Read one book with its content tree of chapters and pages; the tree holds at most 2000 entries",
		Tags:        []string{"knowledge", "books", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"updated_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"owned_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"default_template_id":{"type":"integer"},"tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"},"order":{"type":"integer"}}}},"contents":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"draft":{"type":"boolean"},"template":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},"pages":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"draft":{"type":"boolean"},"template":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"}}}}}}},"shelves":{"type":"array"},"truncated":{"type":"boolean"}},"required":["id","name","slug","contents","truncated"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "Book identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Book identifier"},
			{Name: "name", Description: "Book title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "description_html", Description: "Description as HTML, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "created_by", Description: "Creator with id, name and slug"},
			{Name: "updated_by", Description: "Last editor with id, name and slug"},
			{Name: "owned_by", Description: "Owner with id, name and slug"},
			{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
			{Name: "tags", Description: "At most 50 entries with name, value and order"},
			{Name: "contents", Description: "Chapters with their pages and pages, in book order; type chapter or page; each with book_id, created_at and url, an untrusted address"},
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
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"book_id":{"type":"integer","minimum":1,"x-form":"BOOK_ID"},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"book_id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"priority":{},"created_by":{"properties":{"id":{"type":"integer"}}},"updated_by":{"properties":{"id":{"type":"integer"}}},"owned_by":{"properties":{"id":{"type":"integer"}}},"book_slug":{}},"required":["id","book_id","name","slug","description","created_at","updated_at"]}}`),
		Arguments: []capability.Argument{
			{Name: "book_id", Description: "Only chapters of this book; required for a connection with targets other than a single book", Target: "book"},
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
			{Name: "priority", Description: "Position among the chapters of the book"},
			{Name: "created_by", Description: "Creator as id only"},
			{Name: "updated_by", Description: "Last editor as id only"},
			{Name: "owned_by", Description: "Owner as id only"},
			{Name: "book_slug", Description: "URL slug of the containing book; only when the book itself is bound or the connection has no targets"},
		},
		Examples: []capability.Example{{Description: "List the chapters of book 7", Arguments: json.RawMessage(`{"book_id":7}`)}},
	}

	chaptersGet = capability.Descriptor{
		ID: Provider + ".chapters.get", Version: 1, Title: "Get a BookStack chapter",
		Description: "Read one chapter with its pages; the page list holds at most 2000 entries",
		Tags:        []string{"knowledge", "chapters", "bookstack"},
		Risk:        bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"book_id":{"type":"integer"},"book_slug":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"priority":{"type":"integer"},"created_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"updated_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"owned_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"}}},"default_template_id":{"type":"integer"},"tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"},"order":{"type":"integer"}}}},"pages":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"draft":{"type":"boolean"},"template":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},"priority":{"type":"integer"},"created_by":{"type":"object","properties":{"id":{"type":"integer"}}},"updated_by":{"type":"object","properties":{"id":{"type":"integer"}}},"owned_by":{"type":"object","properties":{"id":{"type":"integer"}}},"revision_count":{"type":"integer"},"editor":{"type":"string"},"book_slug":{"type":"string"}}}},"truncated":{"type":"boolean"}},"required":["id","book_id","name","slug","pages","truncated"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "Chapter identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Chapter identifier"},
			{Name: "book_id", Description: "Identifier of the containing book"},
			{Name: "book_slug", Description: "URL slug of the containing book; only when the book itself is bound or the connection has no targets"},
			{Name: "name", Description: "Chapter title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "description_html", Description: "Description as HTML, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "priority", Description: "Position among the chapters of the book"},
			{Name: "created_by", Description: "Creator with id, name and slug"},
			{Name: "updated_by", Description: "Last editor with id, name and slug"},
			{Name: "owned_by", Description: "Owner with id, name and slug"},
			{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
			{Name: "tags", Description: "At most 50 entries with name, value and order"},
			{Name: "pages", Description: "Pages of the chapter in order; each also with priority, revision_count, editor, creator, last editor and owner as id only, and book_slug under the same rule as the chapter"},
			{Name: "truncated", Description: "True when the page list was cut at 2000 entries"},
		},
		Examples: []capability.Example{{Description: "Read chapter 12", Arguments: json.RawMessage(`{"id":12}`)}},
	}
)

type userJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
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
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*u = userJSON{ID: object.ID, Name: object.Name, Slug: object.Slug}
	return nil
}

type tagJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Order is only read: a pointer tells position 0 from an absent value, and omitempty keeps it out of requests.
	Order *int64 `json:"order,omitempty"`
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
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	URL       string      `json:"url"`
	Pages     []entryJSON `json:"pages"`

	// The members below only appear on the pages of a chapter read.
	Priority      *int64    `json:"priority"`
	CreatedBy     *userJSON `json:"created_by"`
	UpdatedBy     *userJSON `json:"updated_by"`
	OwnedBy       *userJSON `json:"owned_by"`
	RevisionCount *int64    `json:"revision_count"`
	Editor        string    `json:"editor"`
	BookSlug      string    `json:"book_slug"`
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
	Priority          *int64      `json:"priority"`
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

	// Chapter rows also carry these; BookStack gives the users of a list as identifiers only.
	Priority  *int64    `json:"priority"`
	CreatedBy *userJSON `json:"created_by"`
	UpdatedBy *userJSON `json:"updated_by"`
	OwnedBy   *userJSON `json:"owned_by"`
	BookSlug  string    `json:"book_slug"`
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
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if bound.bound() && !bound.bookWide() {
		return nil, needsBook("books.list")
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
	if _, err := bound.listBook(target.BookID, "chapters.list"); err != nil {
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

// ListBooks returns books. On a bound connection the rows are filtered here to the bound books and the books
// on the bound shelves, so limit and offset count the remaining rows.
func (c *Client) ListBooks(ctx context.Context, limit, offset int) (output.Collection, error) {
	if c.scope.bound() && !c.scope.bookWide() {
		return output.Collection{}, needsBook("books.list")
	}
	if err := c.loadShelves(ctx, 0); err != nil {
		return output.Collection{}, err
	}
	rows, err := scanList(ctx, c, scanSpec[listedJSON]{
		op: "list books", path: "/api/books", filtered: c.scope.bound(), limit: limit, offset: offset,
		arguments: argNames(booksList),
		id:        func(b listedJSON) int64 { return b.ID },
		keep:      func(b listedJSON) bool { return !c.scope.bound() || c.inBook(b.ID) },
		row: func(b listedJSON) output.Row {
			row := output.Row{
				"id": b.ID, "name": clip(b.Name, maxResultString), "slug": clip(b.Slug, maxResultString),
				"description": clip(b.Description, maxDescriptionChars), "created_at": b.CreatedAt, "updated_at": b.UpdatedAt,
			}
			addListUsers(row, b.CreatedBy, b.UpdatedBy, b.OwnedBy)
			return row
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
	bookID, err := c.scope.listBook(bookID, "chapters.list")
	if err != nil {
		return output.Collection{}, err
	}
	if bookID != 0 {
		if err := c.requireBook(ctx, bookID); err != nil {
			return output.Collection{}, err
		}
	}
	query := url.Values{}
	if bookID != 0 {
		query.Set("filter[book_id]", strconv.FormatInt(bookID, 10))
	}
	rows, err := scanList(ctx, c, scanSpec[listedJSON]{
		op: "list chapters", path: "/api/chapters", query: query, filtered: bookID != 0,
		limit: limit, offset: offset, narrow: "book_id", arguments: argNames(chaptersList),
		id:   func(ch listedJSON) int64 { return ch.ID },
		keep: func(ch listedJSON) bool { return bookID == 0 || ch.BookID == bookID },
		row: func(ch listedJSON) output.Row {
			row := output.Row{
				"id": ch.ID, "book_id": ch.BookID, "name": clip(ch.Name, maxResultString), "slug": clip(ch.Slug, maxResultString),
				"description": clip(ch.Description, maxDescriptionChars), "created_at": ch.CreatedAt, "updated_at": ch.UpdatedAt,
			}
			if ch.Priority != nil {
				row["priority"] = *ch.Priority
			}
			addListUsers(row, ch.CreatedBy, ch.UpdatedBy, ch.OwnedBy)
			if slug := c.shownBookSlug(ch.BookID, ch.BookSlug); slug != "" {
				row["book_slug"] = slug
			}
			return row
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(chaptersList), Rows: rows}, nil
}

// GetBook returns one book with its content tree. The book is checked against the targets before any request.
func (c *Client) GetBook(ctx context.Context, id int64) (output.Object, error) {
	if err := c.requireBook(ctx, id); err != nil {
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
			pages, cut := treePages(e.Pages, &budget, nil)
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
		{Name: "created_by", Value: userDetail(book.CreatedBy)}, {Name: "updated_by", Value: userDetail(book.UpdatedBy)},
		{Name: "owned_by", Value: userDetail(book.OwnedBy)},
		{Name: "default_template_id", Value: book.DefaultTemplateID},
		{Name: "tags", Value: orderedTags(book.Tags)},
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

// GetChapter returns one chapter. On a bound connection a bound chapter is read as it is; for any other one
// this single read is also the evidence that its book is reached, and a foreign chapter is refused before
// anything is shown.
func (c *Client) GetChapter(ctx context.Context, id int64) (output.Object, error) {
	if !c.scope.mayReachChapter(id) {
		return output.Object{}, c.scope.outside(outsideChapter)
	}
	var chapter chapterJSON
	if err := c.get(ctx, "get chapter", "/api/chapters/"+strconv.FormatInt(id, 10), nil, &chapter, nil, provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	if c.scope.bound() && !c.scope.hasChapter(id) {
		allowed, err := c.bookAllowed(ctx, chapter.BookID)
		if err != nil {
			return output.Object{}, err
		}
		if !allowed {
			return output.Object{}, c.scope.outside(outsideChapter)
		}
	}
	budget := maxTreeEntries
	pages, truncated := treePages(chapter.Pages, &budget, func(p entryJSON, item map[string]any) {
		if p.Priority != nil {
			item["priority"] = *p.Priority
		}
		addListUsers(item, p.CreatedBy, p.UpdatedBy, p.OwnedBy)
		if p.RevisionCount != nil {
			item["revision_count"] = *p.RevisionCount
		}
		if p.Editor != "" {
			item["editor"] = clip(p.Editor, maxResultString)
		}
		if slug := c.shownBookSlug(p.BookID, p.BookSlug); slug != "" {
			item["book_slug"] = slug
		}
	})
	fields := []output.Field{{Name: "id", Value: chapter.ID}, {Name: "book_id", Value: chapter.BookID}}
	if slug := c.shownBookSlug(chapter.BookID, chapter.BookSlug); slug != "" {
		fields = append(fields, output.Field{Name: "book_slug", Value: slug})
	}
	fields = append(fields, []output.Field{
		{Name: "name", Value: clip(chapter.Name, maxResultString)}, {Name: "slug", Value: clip(chapter.Slug, maxResultString)},
		{Name: "description", Value: clip(chapter.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(chapter.DescriptionHTML, maxDescriptionChars)},
		{Name: "created_at", Value: chapter.CreatedAt}, {Name: "updated_at", Value: chapter.UpdatedAt},
	}...)
	if chapter.Priority != nil {
		fields = append(fields, output.Field{Name: "priority", Value: *chapter.Priority})
	}
	return output.Object{Fields: append(fields,
		output.Field{Name: "created_by", Value: userDetail(chapter.CreatedBy)},
		output.Field{Name: "updated_by", Value: userDetail(chapter.UpdatedBy)},
		output.Field{Name: "owned_by", Value: userDetail(chapter.OwnedBy)},
		output.Field{Name: "default_template_id", Value: chapter.DefaultTemplateID},
		output.Field{Name: "tags", Value: orderedTags(chapter.Tags)},
		output.Field{Name: "pages", Value: pages}, output.Field{Name: "truncated", Value: truncated},
	)}, nil
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
	if e.BookID > 0 {
		item["book_id"] = e.BookID
	}
	if e.CreatedAt != "" {
		item["created_at"] = e.CreatedAt
	}
	if e.URL != "" {
		// An address from the instance, shown as text and never fetched.
		item["url"] = clip(e.URL, maxResultString)
	}
	if kind == "page" {
		item["chapter_id"] = e.ChapterID
		item["draft"] = e.Draft
		item["template"] = e.Template
	}
	return item
}

// treePages shows the pages of a chapter, each one against the shared entry budget; truncated is true when
// the budget ran out first. extra adds members only some trees carry.
func treePages(pages []entryJSON, budget *int, extra func(entryJSON, map[string]any)) ([]map[string]any, bool) {
	out := make([]map[string]any, 0, len(pages))
	for _, p := range pages {
		if *budget <= 0 {
			return out, true
		}
		*budget--
		p.Type = "page"
		item := treeEntry(p)
		if extra != nil {
			extra(p, item)
		}
		out = append(out, item)
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

// userDetail is reduceUser for a detail answer, which also names the slug of the user.
func userDetail(u *userJSON) map[string]any {
	out := reduceUser(u)
	if u != nil && u.Slug != "" {
		out["slug"] = clip(u.Slug, maxResultString)
	}
	return out
}

// addListUsers sets the creator, last editor, and owner of a list row. A list gives the identifier only, so a
// user is shown by id without a name; an absent user stays out.
func addListUsers(row map[string]any, created, updated, owned *userJSON) {
	for key, u := range map[string]*userJSON{"created_by": created, "updated_by": updated, "owned_by": owned} {
		if u != nil {
			row[key] = map[string]any{"id": u.ID}
		}
	}
}

// shownBookSlug is the slug of a book as far as the connection may show it: without targets always, with
// targets only for a book that is itself bound or stands on a bound shelf already read in this call. Any
// other book is reached through a page or chapter and appears by identifier only.
func (c *Client) shownBookSlug(bookID int64, slug string) string {
	if c.scope.bound() && !c.inBook(bookID) {
		return ""
	}
	return clip(slug, maxResultString)
}

// orderedTags is reduceTags for a read that also shows the position of each tag.
func orderedTags(tags []tagJSON) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		if len(out) >= maxResultTags {
			break
		}
		item := map[string]any{"name": clip(t.Name, maxResultString), "value": clip(t.Value, maxResultString)}
		if t.Order != nil {
			item["order"] = *t.Order
		}
		out = append(out, item)
	}
	return out
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
