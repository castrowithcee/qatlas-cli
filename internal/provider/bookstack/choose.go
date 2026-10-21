package bookstack

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	kindShelf   = "shelf"
	kindBook    = "book"
	kindChapter = "chapter"
	kindPage    = "page"

	// choicePageSize is the number of entries of one page of choices, sent as count.
	choicePageSize = 50
	// maxChoiceOffset bounds a cursor well above any real listing.
	maxChoiceOffset = 10000000
	// maxChoiceName keeps a name well inside the bound the caller applies to entry texts.
	maxChoiceName = 200
	// maxSlugLength bounds a slug read from a URL.
	maxSlugLength = 255
)

// choiceJSON is the row of a list or read the choice needs.
type choiceJSON struct {
	ID        int64  `json:"id"`
	BookID    int64  `json:"book_id"`
	ChapterID int64  `json:"chapter_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Draft     bool   `json:"draft"`
}

// openUnbound returns a client for choosing targets: the connection may still be a draft without targets,
// or bound to some, and every list reads what the token reaches.
func openUnbound(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	unbound := *resolved
	unbound.Target, unbound.Targets = "", nil
	return Open(ctx, &unbound, secrets, red)
}

// choiceName makes a name from BookStack fit for an entry: without control characters, cut to a bounded
// length, else the fallback. It stays text; nothing interprets it.
func choiceName(name, fallback string) string {
	clean := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name))
	if len(clean) > maxChoiceName {
		clean = clean[:maxChoiceName]
		for !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	if clean == "" {
		return fallback
	}
	return clean
}

func invalidChoice(message string) error { return &provider.InvalidRequestError{Message: message} }

// choiceParent reads a parent KIND/ID of a listing.
func choiceParent(raw string) (kind string, id int64, err error) {
	for _, prefix := range []string{shelfPrefix, bookPrefix, chapterPrefix} {
		if rest, ok := strings.CutPrefix(raw, prefix); ok {
			if n, ok := parseID(rest); ok {
				return strings.TrimSuffix(prefix, "/"), n, nil
			}
		}
	}
	return "", 0, invalidChoice("the parent must be shelf/ID, book/ID, or chapter/ID")
}

func choiceOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 1 || n > maxChoiceOffset || strconv.Itoa(n) != cursor {
		return 0, invalidChoice("the target cursor is not a page of this provider")
	}
	return n, nil
}

// ListTargets reads one page of the shelves, books, chapters, or pages the token reaches, never filtered by
// the connection's current targets. The top level holds shelves and all books, also those on no shelf; a
// shelf opens its books, a book its chapters and the pages outside any chapter, a chapter its pages. The
// pages of a list are sent as count and offset; the cursor is the offset of the next page. The selection
// changes nothing in BookStack.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	offset, err := choiceOffset(req.Cursor)
	if err != nil {
		return config.TargetPage{}, err
	}
	var parentKind string
	var parentID int64
	if req.Parent != "" {
		if parentKind, parentID, err = choiceParent(req.Parent); err != nil {
			return config.TargetPage{}, err
		}
	}
	// The kind and the parent kind must fit: shelf has no parent, book sits on a shelf or at the top, a
	// chapter in a book, a page in a book or a chapter.
	okParent := map[string][]string{
		kindShelf: {""}, kindBook: {"", kindShelf}, kindChapter: {kindBook}, kindPage: {kindBook, kindChapter},
	}[req.Kind]
	if len(okParent) == 0 {
		return config.TargetPage{}, invalidChoice("the kind is not one this provider offers")
	}
	if !slices.Contains(okParent, parentKind) {
		return config.TargetPage{}, invalidChoice("the parent does not fit this kind")
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	switch {
	case req.Kind == kindShelf:
		return client.choiceList(ctx, "/api/shelves", nil, offset, "", kindShelf, true, nil)
	case req.Kind == kindBook && parentKind == "":
		return client.choiceList(ctx, "/api/books", nil, offset, "", kindBook, true, nil)
	case req.Kind == kindBook:
		return client.shelfChoices(ctx, parentID, offset)
	case req.Kind == kindChapter:
		place, err := client.bookPlace(ctx, parentID)
		if err != nil {
			return config.TargetPage{}, err
		}
		query := url.Values{"filter[book_id]": {strconv.FormatInt(parentID, 10)}}
		return client.choiceList(ctx, "/api/chapters", query, offset, place, kindChapter, true,
			func(r choiceJSON) bool { return r.BookID == parentID })
	case parentKind == kindBook:
		place, err := client.bookPlace(ctx, parentID)
		if err != nil {
			return config.TargetPage{}, err
		}
		query := url.Values{
			"filter[book_id]": {strconv.FormatInt(parentID, 10)}, "filter[chapter_id]": {"0"},
		}
		return client.choiceList(ctx, "/api/pages", query, offset, place, kindPage, false,
			func(r choiceJSON) bool { return r.BookID == parentID && r.ChapterID == 0 && !r.Draft })
	}
	var chapter choiceJSON
	if err := client.get(ctx, "read chapter", "/api/chapters/"+strconv.FormatInt(parentID, 10), nil, &chapter,
		nil, provider.ClassPermission); err != nil {
		return config.TargetPage{}, err
	}
	place, err := client.bookPlace(ctx, chapter.BookID)
	if err != nil {
		return config.TargetPage{}, err
	}
	place += "/" + choiceName(chapter.Name, strconv.FormatInt(chapter.ID, 10))
	query := url.Values{"filter[chapter_id]": {strconv.FormatInt(parentID, 10)}}
	return client.choiceList(ctx, "/api/pages", query, offset, place, kindPage, false,
		func(r choiceJSON) bool { return r.ChapterID == parentID && !r.Draft })
}

// bookPlace reads the name of a book for the path of its children.
func (c *Client) bookPlace(ctx context.Context, id int64) (string, error) {
	var book choiceJSON
	if err := c.get(ctx, "read book", "/api/books/"+strconv.FormatInt(id, 10), nil, &book, nil,
		provider.ClassPermission); err != nil {
		return "", err
	}
	return "/" + choiceName(book.Name, strconv.FormatInt(id, 10)), nil
}

// choiceList reads one page of a list endpoint. keep drops rows that do not belong to the parent, because
// BookStack silently ignores a filter it does not know; the cursor still counts the rows received.
func (c *Client) choiceList(ctx context.Context, path string, filter url.Values, offset int, place, kind string,
	children bool, keep func(choiceJSON) bool) (config.TargetPage, error) {
	query := url.Values{}
	for key, values := range filter {
		query[key] = values
	}
	query.Set("count", strconv.Itoa(choicePageSize))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("sort", "+id")
	var page struct {
		Data  []choiceJSON `json:"data"`
		Total int          `json:"total"`
	}
	if err := c.get(ctx, "list targets", path, query, &page, nil, provider.ClassPermission); err != nil {
		return config.TargetPage{}, err
	}
	result := config.TargetPage{}
	for _, row := range page.Data {
		if keep == nil || keep(row) {
			result.Entries = append(result.Entries, choiceEntry(kind, row, place, children))
		}
	}
	if end := offset + len(page.Data); len(page.Data) > 0 && end < page.Total && end <= maxChoiceOffset {
		result.Next = strconv.Itoa(end)
	}
	return result, nil
}

// shelfChoices lists the books of one shelf. BookStack returns them with the shelf in one piece, so the
// provider cuts the pages itself.
func (c *Client) shelfChoices(ctx context.Context, id int64, offset int) (config.TargetPage, error) {
	var shelf struct {
		Name  string       `json:"name"`
		Books []choiceJSON `json:"books"`
	}
	if err := c.get(ctx, "read shelf", "/api/shelves/"+strconv.FormatInt(id, 10), nil, &shelf, nil,
		provider.ClassPermission); err != nil {
		return config.TargetPage{}, err
	}
	place := "/" + choiceName(shelf.Name, strconv.FormatInt(id, 10))
	lo := min(offset, len(shelf.Books))
	hi := min(lo+choicePageSize, len(shelf.Books))
	result := config.TargetPage{}
	for _, row := range shelf.Books[lo:hi] {
		result.Entries = append(result.Entries, choiceEntry(kindBook, row, place, true))
	}
	if hi < len(shelf.Books) {
		result.Next = strconv.Itoa(hi)
	}
	return result, nil
}

func choiceEntry(kind string, row choiceJSON, place string, children bool) config.TargetEntry {
	id := strconv.FormatInt(row.ID, 10)
	return config.TargetEntry{
		Target: kind + "/" + id, Name: choiceName(row.Name, id), Kind: kind, Path: place,
		HasChildren: children && kind != kindPage,
	}
}

// ResolveTargetURL turns a web URL of the connection's own instance into targets: /shelves/SLUG and
// /books/SLUG, and below a book /chapter/SLUG and /page/SLUG. The installation path of the service is
// accepted. BookStack filters its lists by slug, so a slug is resolved by a read; the slug of a chapter or
// page is only unique inside its book, so the book is resolved first and the filter carries its ID. Another
// origin, another form, or an unknown slug gives nothing.
func ResolveTargetURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	_, rawURL string) ([]config.TargetEntry, error) {
	if resolved == nil {
		return nil, nil
	}
	base, err := baseOf(resolved.BaseURL)
	if err != nil {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
		!strings.EqualFold(u.Scheme+"://"+u.Host, base.Scheme+"://"+base.Host) {
		return nil, nil
	}
	rest, ok := pathBelow(base.EscapedPath(), u.EscapedPath())
	if !ok {
		return nil, nil
	}
	var first, second string
	var leaf string
	switch {
	case len(rest) == 2 && (rest[0] == "shelves" || rest[0] == "books"):
		first = rest[1]
	case len(rest) == 4 && rest[0] == "books" && (rest[2] == "chapter" || rest[2] == "page"):
		first, leaf, second = rest[1], rest[2], rest[3]
	default:
		return nil, nil
	}
	if !validSlug(first) || second != "" && !validSlug(second) {
		return nil, nil
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if rest[0] == "shelves" {
		shelf, err := client.bySlug(ctx, "/api/shelves", first, nil)
		if err != nil || shelf == nil {
			return nil, err
		}
		return []config.TargetEntry{choiceEntry(kindShelf, *shelf, "", true)}, nil
	}
	book, err := client.bySlug(ctx, "/api/books", first, nil)
	if err != nil || book == nil {
		return nil, err
	}
	if leaf == "" {
		return []config.TargetEntry{choiceEntry(kindBook, *book, "", true)}, nil
	}
	bookID := strconv.FormatInt(book.ID, 10)
	place := "/" + choiceName(book.Name, bookID)
	if leaf == "chapter" {
		row, err := client.bySlug(ctx, "/api/chapters", second, url.Values{"filter[book_id]": {bookID}})
		if err != nil || row == nil || row.BookID != book.ID {
			return nil, err
		}
		return []config.TargetEntry{choiceEntry(kindChapter, *row, place, true)}, nil
	}
	row, err := client.bySlug(ctx, "/api/pages", second, url.Values{"filter[book_id]": {bookID}})
	if err != nil || row == nil || row.BookID != book.ID {
		return nil, err
	}
	return []config.TargetEntry{choiceEntry(kindPage, *row, place, false)}, nil
}

// bySlug returns the one row of a list whose slug is exactly slug, nil when there is none or the slug is
// not unique in the answer (BookStack silently ignores a filter it does not know, so the slug is checked
// here as well).
func (c *Client) bySlug(ctx context.Context, path, slug string, filter url.Values) (*choiceJSON, error) {
	query := url.Values{}
	for key, values := range filter {
		query[key] = values
	}
	query.Set("filter[slug]", slug)
	query.Set("count", "10")
	var page struct {
		Data []choiceJSON `json:"data"`
	}
	if err := c.get(ctx, "resolve url", path, query, &page, nil, provider.ClassPermission); err != nil {
		return nil, err
	}
	var found *choiceJSON
	for i := range page.Data {
		if page.Data[i].Slug != slug || page.Data[i].ID < 1 {
			continue
		}
		if found != nil {
			return nil, nil
		}
		found = &page.Data[i]
	}
	return found, nil
}

// pathBelow returns the decoded segments of path below the installation path of the service.
func pathBelow(install, path string) ([]string, bool) {
	split := func(raw string) ([]string, bool) {
		var out []string
		for _, part := range strings.Split(strings.Trim(raw, "/"), "/") {
			decoded, err := url.PathUnescape(part)
			if err != nil || strings.Contains(decoded, "/") {
				return nil, false
			}
			out = append(out, decoded)
		}
		return out, true
	}
	base, ok := split(install)
	if !ok {
		return nil, false
	}
	if len(base) == 1 && base[0] == "" {
		base = nil
	}
	segments, ok := split(path)
	if !ok || len(segments) < len(base) {
		return nil, false
	}
	for i, s := range base {
		if segments[i] != s {
			return nil, false
		}
	}
	return segments[len(base):], true
}

func validSlug(s string) bool {
	if s == "" || len(s) > maxSlugLength || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
