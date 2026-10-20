package bookstack

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	shelfPrefix   = "shelf/"
	bookPrefix    = "book/"
	chapterPrefix = "chapter/"
	pagePrefix    = "page/"
	// maxTargets bounds the targets one connection may be bound to.
	maxTargets = 100
	// maxIDDigits bounds a configured or argument identifier well above any realistic BookStack id.
	maxIDDigits = 18
)

const (
	targetForm = "a BookStack target must be shelf/SHELF_ID, book/BOOK_ID, chapter/CHAPTER_ID, or page/PAGE_ID " +
		"with a positive integer"
	bookIDRequiredMsg = "book_id is required for a connection bound to several books"
	targetRequiredMsg = "book_id or chapter_id is required for a connection bound to a shelf or to several targets"
)

// Subjects of a refusal by scope.outside.
const (
	outsidePage    = "the page"
	outsideChapter = "the chapter"
	outsideBook    = "book_id"
)

func invalidRequest(message string) error { return &provider.InvalidRequestError{Message: message} }

// scope is the boundary of one connection: a shelf stands for the books on it at the time of a call, a chapter
// for its pages, a page for itself. Several targets reach their union. An unbound scope (no target configured)
// reaches everything the token reaches.
type scope struct {
	shelves  []int64
	books    []int64
	chapters []int64
	pages    []int64
}

func (s scope) bound() bool {
	return len(s.shelves)+len(s.books)+len(s.chapters)+len(s.pages) > 0
}

// bookWide reports whether the scope can reach a whole book at all: through a book or a shelf.
func (s scope) bookWide() bool { return len(s.books)+len(s.shelves) > 0 }

// booksOnly reports a scope of books and nothing else, the form whose wording and defaults stay as they were.
func (s scope) booksOnly() bool {
	return len(s.books) > 0 && len(s.shelves)+len(s.chapters)+len(s.pages) == 0
}

func (s scope) hasShelf(id int64) bool   { return slices.Contains(s.shelves, id) }
func (s scope) hasBook(id int64) bool    { return slices.Contains(s.books, id) }
func (s scope) hasChapter(id int64) bool { return slices.Contains(s.chapters, id) }
func (s scope) hasPage(id int64) bool    { return slices.Contains(s.pages, id) }

// mayReachChapter is the local part of a chapter check: without a book, a shelf, or the chapter itself no read
// can prove it.
func (s scope) mayReachChapter(id int64) bool { return !s.bound() || s.hasChapter(id) || s.bookWide() }

// mayReachPage is the local part of a page check: a scope of pages alone proves a page by its identifier.
func (s scope) mayReachPage(id int64) bool {
	return !s.bound() || s.hasPage(id) || len(s.chapters) > 0 || s.bookWide()
}

// boundTo names what a bound connection is bound to in a refusal.
func (s scope) boundTo() string {
	if s.booksOnly() {
		return "books"
	}
	return "targets"
}

// outside refuses an object outside the connection without naming it or a target.
func (s scope) outside(subject string) error {
	return invalidRequest(subject + " is outside the " + s.boundTo() + " this connection is bound to")
}

// needsBook refuses a book-wide capability on a connection bound only to chapters or pages.
func needsBook(what string) error {
	return invalidRequest("this connection is bound to chapters or pages only, so it cannot use " + what +
		": that needs a book or shelf target")
}

// parseScope reads the configured targets. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) > maxTargets {
		return scope{}, errors.New("a BookStack connection may be bound to at most 100 targets")
	}
	var bound scope
	for _, raw := range values {
		prefix, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		list := map[string]*[]int64{
			shelfPrefix: &bound.shelves, bookPrefix: &bound.books, chapterPrefix: &bound.chapters, pagePrefix: &bound.pages,
		}[prefix]
		if slices.Contains(*list, id) {
			return scope{}, errors.New("the BookStack target list names a target more than once")
		}
		*list = append(*list, id)
	}
	return bound, nil
}

// parseTarget reads one KIND/ID entry and returns its prefix.
func parseTarget(raw string) (string, int64, error) {
	for _, prefix := range []string{shelfPrefix, bookPrefix, chapterPrefix, pagePrefix} {
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		id, ok := parseID(strings.TrimPrefix(raw, prefix))
		if !ok {
			return "", 0, errors.New(targetForm)
		}
		return prefix, id, nil
	}
	return "", 0, errors.New(targetForm)
}

// parseID accepts plain, non-padded decimal digits only.
func parseID(raw string) (int64, bool) {
	if raw == "" || len(raw) > maxIDDigits || raw[0] == '0' {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil
}

func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved. A configured target that cannot be
// read makes the connection unusable instead of widening it.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	values := provider.TargetsOf(resolved)
	bound, err := parseScope(values)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// checkBook is the local check of a book_id argument, before any secret or request. A book that is not bound
// itself may still stand on a bound shelf, so with shelves the decision falls to Client.requireBook, which every
// caller runs before the book is used. The refusal never names the book.
func (s scope) checkBook(bookID int64) error {
	if bookID <= 0 || len(strconv.FormatInt(bookID, 10)) > maxIDDigits {
		return invalidRequest("book_id must be a positive integer")
	}
	if s.bound() && !s.hasBook(bookID) && len(s.shelves) == 0 {
		return s.outside(outsideBook)
	}
	return nil
}

// requireInstanceScope is the local gate of every instance-wide tool. It admits only a connection without
// targets and refuses a bound one, before a secret is resolved and before any request is sent. what names the
// refused capability in the message, never a configured target.
func requireInstanceScope(resolved *config.Resolved, what string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if bound.bound() {
		return instanceRefusal(bound, what)
	}
	return nil
}

func instanceRefusal(s scope, what string) error {
	return invalidRequest("this connection is bound to " + s.boundTo() + ", so it cannot use " + what +
		": it is instance-wide and only available on a connection without targets")
}

// listTarget names the book or chapter a list or search is limited to; zero means not given. pagesOnly limits
// it to the bound pages of a connection bound to pages alone.
type listTarget struct {
	BookID    int64
	ChapterID int64
	pagesOnly bool
}

// listIn applies the local rules of a page list or a search against the connection's targets, before any
// request. Without book_id and chapter_id a bound connection takes its only book or its only chapter, and one
// bound to pages alone stays with those pages; any other binding needs an argument. A book on a shelf and a
// chapter that is not bound still need their evidence read.
func (s scope) listIn(t listTarget) (listTarget, error) {
	switch {
	case t.BookID != 0 && t.ChapterID != 0:
		return t, invalidRequest("book_id and chapter_id are mutually exclusive")
	case t.BookID != 0:
		return t, s.checkBook(t.BookID)
	case t.ChapterID != 0:
		if !s.mayReachChapter(t.ChapterID) {
			return t, s.outside(outsideChapter)
		}
		return t, nil
	case !s.bound():
		return t, nil
	case s.booksOnly() && len(s.books) == 1:
		t.BookID = s.books[0]
	case len(s.chapters) == 1 && len(s.shelves)+len(s.books)+len(s.pages) == 0:
		t.ChapterID = s.chapters[0]
	case len(s.pages) > 0 && len(s.shelves)+len(s.books)+len(s.chapters) == 0:
		t.pagesOnly = true
	case s.booksOnly():
		return t, invalidRequest(bookIDRequiredMsg)
	default:
		return t, invalidRequest(targetRequiredMsg)
	}
	return t, nil
}

// listBook applies the local rules of a list of one book's content, before any request: only a book or a shelf
// reaches a whole book, and without book_id a connection bound to one book takes it.
func (s scope) listBook(bookID int64, what string) (int64, error) {
	switch {
	case bookID != 0:
		return bookID, s.checkBook(bookID)
	case !s.bound():
		return 0, nil
	case !s.bookWide():
		return 0, needsBook(what)
	case s.booksOnly() && len(s.books) == 1:
		return s.books[0], nil
	case s.booksOnly():
		return 0, invalidRequest(bookIDRequiredMsg)
	}
	return 0, invalidRequest("book_id is required for a connection bound to a shelf or to several targets")
}

// loadShelves reads the bound shelves this call has not read yet, each at most once, and stops as soon as one
// holds want; want 0 reads them all. The books are never kept beyond the call, so a book taken off a shelf is
// out of reach with the next one. A shelf that cannot be read fails the call instead of narrowing it.
func (c *Client) loadShelves(ctx context.Context, want int64) error {
	if c.shelfBooks == nil {
		c.shelfBooks = map[int64]bool{}
		c.shelvesRead = map[int64]bool{}
	}
	for _, id := range c.scope.shelves {
		if want != 0 && c.shelfBooks[want] {
			return nil
		}
		if c.shelvesRead[id] {
			continue
		}
		var shelf struct {
			Books []struct {
				ID int64 `json:"id"`
			} `json:"books"`
		}
		if err := c.get(ctx, "get shelf", "/api/shelves/"+strconv.FormatInt(id, 10), nil, &shelf, nil, provider.ClassPermission); err != nil {
			return err
		}
		c.shelvesRead[id] = true
		for _, b := range shelf.Books {
			c.shelfBooks[b.ID] = true
		}
	}
	return nil
}

// inBook reports whether a book is bound or stands on a shelf this call has read.
func (c *Client) inBook(id int64) bool { return id > 0 && (c.scope.hasBook(id) || c.shelfBooks[id]) }

// bookAllowed reports whether the connection reaches a whole book: a bound book or a book on a bound shelf.
func (c *Client) bookAllowed(ctx context.Context, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	if c.scope.hasBook(id) || len(c.scope.shelves) == 0 {
		return c.scope.hasBook(id), nil
	}
	if err := c.loadShelves(ctx, id); err != nil {
		return false, err
	}
	return c.inBook(id), nil
}

// requireBook proves a book_id before it is used: locally against the bound books, through the bound shelves
// otherwise. An unbound connection reads nothing.
func (c *Client) requireBook(ctx context.Context, id int64) error {
	if err := c.scope.checkBook(id); err != nil || !c.scope.bound() {
		return err
	}
	ok, err := c.bookAllowed(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return c.scope.outside(outsideBook)
	}
	return nil
}

// bookRef is the part of a page or chapter read as evidence of where it belongs; a chapter has no chapter_id.
type bookRef struct {
	ID        int64 `json:"id"`
	BookID    int64 `json:"book_id"`
	ChapterID int64 `json:"chapter_id"`
}

// chapterBook reads a chapter once and returns its book if the connection reaches that whole book.
func (c *Client) chapterBook(ctx context.Context, id int64) (int64, error) {
	if !c.scope.bookWide() {
		return 0, c.scope.outside(outsideChapter)
	}
	var ref bookRef
	if err := c.get(ctx, "get chapter", "/api/chapters/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission); err != nil {
		return 0, err
	}
	ok, err := c.bookAllowed(ctx, ref.BookID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, c.scope.outside(outsideChapter)
	}
	return ref.BookID, nil
}

// chapterIn proves that the connection reaches a chapter and its pages: a bound chapter needs no read, any
// other one its book. book is 0 when no read was needed or the connection is unbound.
func (c *Client) chapterIn(ctx context.Context, id int64) (book int64, err error) {
	if !c.scope.bound() || c.scope.hasChapter(id) {
		return 0, nil
	}
	return c.chapterBook(ctx, id)
}

// requireChapterBookWide proves a chapter for a change of the chapter itself, which only a book or a shelf
// target reaches: a bound chapter grants its pages, not the chapter.
func (c *Client) requireChapterBookWide(ctx context.Context, id int64, what string) error {
	if !c.scope.bound() {
		return nil
	}
	if !c.scope.bookWide() {
		return needsBook(what)
	}
	_, err := c.chapterBook(ctx, id)
	return err
}

// pageAllowed reports whether a page read as ref lies in the connection: the page itself, its chapter, or its
// whole book is reached.
func (c *Client) pageAllowed(ctx context.Context, id int64, ref bookRef) (bool, error) {
	if c.scope.hasPage(id) || (ref.ChapterID > 0 && c.scope.hasChapter(ref.ChapterID)) {
		return true, nil
	}
	return c.bookAllowed(ctx, ref.BookID)
}

// pageInScope proves a page by identifier: a bound page needs no read, any other one is read once as evidence
// of its chapter and book. An unbound connection reads nothing.
func (c *Client) pageInScope(ctx context.Context, id int64) (bool, error) {
	if !c.scope.bound() || c.scope.hasPage(id) {
		return true, nil
	}
	if !c.scope.mayReachPage(id) {
		return false, nil
	}
	var ref bookRef
	if err := c.get(ctx, "get page", "/api/pages/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission); err != nil {
		return false, err
	}
	return c.pageAllowed(ctx, id, ref)
}

// requirePageBound proves a page before a change or a read of something on it; the refusal names neither the
// page nor a target.
func (c *Client) requirePageBound(ctx context.Context, id string) error {
	if !c.scope.bound() {
		return nil
	}
	n, ok := parseID(id)
	if !ok {
		return invalidRequest("the page identifier must be a positive integer")
	}
	allowed, err := c.pageInScope(ctx, n)
	if err != nil {
		return err
	}
	if !allowed {
		return c.scope.outside(outsidePage)
	}
	return nil
}

// reaches mirrors the call checks as the tools a binding is offered: an instance-wide tool only without
// targets, a whole-book tool only with a book or shelf, and a chapter read or a new page not on pages alone.
// A binding that does not parse reaches everything; configuration validation reports it. The checks at the
// call stay in force.
func reaches(tool string, targets []string) (bool, []string) {
	bound, err := parseScope(targets)
	if err != nil || !bound.bound() {
		return true, nil
	}
	switch tool {
	case auditLogList.ID, importsList.ID, importsGet.ID, importsUpload.ID, importsRun.ID, importsDelete.ID,
		recycleBinList.ID, recycleBinRestore.ID, recycleBinDestroy.ID, usersList.ID, usersGet.ID, usersUpdate.ID,
		usersDelete.ID, rolesList.ID, rolesGet.ID, rolesCreate.ID, rolesUpdate.ID, rolesDelete.ID, tagsList.ID,
		tagsValues.ID, shelvesList.ID, shelvesCreate.ID, shelvesUpdate.ID, shelvesDelete.ID, shelvesSetCover.ID,
		booksCreate.ID:
		return false, nil
	case shelvesGet.ID:
		return len(bound.shelves) > 0, []string{"shelf/SHELF_ID"}
	case booksList.ID, booksGet.ID, booksUpdate.ID, booksDelete.ID, booksSetCover.ID, chaptersList.ID,
		chaptersCreate.ID, chaptersUpdate.ID, chaptersDelete.ID:
		return bound.bookWide(), []string{"book/BOOK_ID", "shelf/SHELF_ID"}
	case chaptersGet.ID, pagesCreate.ID:
		return bound.bookWide() || len(bound.chapters) > 0, []string{"chapter/CHAPTER_ID", "book/BOOK_ID", "shelf/SHELF_ID"}
	}
	return true, nil
}
