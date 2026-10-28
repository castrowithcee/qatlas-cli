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
	excludeForm = "a BookStack exclusion must be shelf/SHELF_ID, book/BOOK_ID, chapter/CHAPTER_ID, or page/PAGE_ID " +
		"with a positive integer, named once and different from every target"
	// unprovable refuses a call whose reach could not be read; it names neither an object nor an exclusion.
	unprovable        = "the reach of this call cannot be checked against the targets of this connection"
	bookIDRequiredMsg = "book_id is required for a connection bound to several books"
	targetRequiredMsg = "book_id or chapter_id is required for a connection bound to a shelf or to several targets"
)

// Subjects of a refusal by scope.outside.
const (
	outsidePage    = "the page"
	outsideChapter = "the chapter"
	outsideBook    = "book_id"
	outsideShelf   = "the shelf"
)

func invalidRequest(message string) error { return &provider.InvalidRequestError{Message: message} }

// scope is the boundary of one connection: a shelf stands for the books on it at the time of a call, a chapter
// for its pages, a page for itself. Several targets reach their union. An unbound scope (no target configured)
// reaches everything the token reaches.
//
// The exclusions take an object with everything below it out of that reach, an excluded shelf the books on it
// at the time of a call, and win over every target. They are kept apart on purpose: they never decide whether
// the connection is bound, which tools it is offered, or which argument a call needs.
type scope struct {
	shelves  []int64
	books    []int64
	chapters []int64
	pages    []int64

	exShelves  []int64
	exBooks    []int64
	exChapters []int64
	exPages    []int64
}

func (s scope) bound() bool {
	return len(s.shelves)+len(s.books)+len(s.chapters)+len(s.pages) > 0
}

// limited reports whether a call has anything to prove: the connection is bound or carries exclusions.
func (s scope) limited() bool { return s.bound() || s.excludes() }

// excludes reports whether the connection carries any exclusion.
func (s scope) excludes() bool {
	return len(s.exShelves)+len(s.exBooks)+len(s.exChapters)+len(s.exPages) > 0
}

// excludesBooks reports exclusions that a book can fall under, so every book needs a check.
func (s scope) excludesBooks() bool { return len(s.exShelves)+len(s.exBooks) > 0 }

// excludesContainers reports exclusions that a page can fall under through its chapter or book, so a page
// needs its chapter and book as evidence even where a target names it.
func (s scope) excludesContainers() bool { return len(s.exChapters) > 0 || s.excludesBooks() }

func (s scope) excludesShelf(id int64) bool   { return slices.Contains(s.exShelves, id) }
func (s scope) excludesBook(id int64) bool    { return slices.Contains(s.exBooks, id) }
func (s scope) excludesChapter(id int64) bool { return slices.Contains(s.exChapters, id) }
func (s scope) excludesPage(id int64) bool    { return slices.Contains(s.exPages, id) }

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

// outside refuses an object outside the connection without naming it, a target, or an exclusion. An excluded
// object is refused in the same words as one the targets do not reach.
func (s scope) outside(subject string) error {
	if !s.bound() {
		return invalidRequest(subject + " is outside the targets of this connection")
	}
	return invalidRequest(subject + " is outside the " + s.boundTo() + " this connection is bound to")
}

// checkExcluded is the local check of an identifier argument against the exclusions, before any secret or
// request. Only an object excluded by its own identifier is decided here; one below an excluded object needs
// its evidence read.
func (s scope) checkExcluded(prefix string, id int64) error {
	excluded, subject := false, outsidePage
	switch prefix {
	case shelfPrefix:
		excluded, subject = s.excludesShelf(id), outsideShelf
	case bookPrefix:
		excluded, subject = s.excludesBook(id), outsideBook
	case chapterPrefix:
		excluded, subject = s.excludesChapter(id), outsideChapter
	case pagePrefix:
		excluded = s.excludesPage(id)
	}
	if excluded {
		return s.outside(subject)
	}
	return nil
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

// withExcludes reads the exclusions of the connection into the scope. An exclusion has the form of a target,
// is named once, and differs from every target; the checks do not rely on the configuration validation, so an
// invalid exclusion fails closed here too. No error quotes a configured value.
func (s scope) withExcludes(values []string) (scope, error) {
	for _, raw := range values {
		prefix, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, errors.New(excludeForm)
		}
		lists := map[string][2]*[]int64{
			shelfPrefix: {&s.exShelves, &s.shelves}, bookPrefix: {&s.exBooks, &s.books},
			chapterPrefix: {&s.exChapters, &s.chapters}, pagePrefix: {&s.exPages, &s.pages},
		}[prefix]
		if slices.Contains(*lists[0], id) || slices.Contains(*lists[1], id) {
			return scope{}, errors.New(excludeForm)
		}
		*lists[0] = append(*lists[0], id)
	}
	return s, nil
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

// boundScope reads the connection's scope with its exclusions before any secret is resolved. A configured
// target or exclusion that cannot be read makes the connection unusable instead of widening it.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := parseScope(provider.TargetsOf(resolved))
	if err == nil {
		bound, err = bound.withExcludes(provider.ExcludesOf(resolved))
	}
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
		if err := s.checkBook(t.BookID); err != nil {
			return t, err
		}
		return t, s.checkExcluded(bookPrefix, t.BookID)
	case t.ChapterID != 0:
		if !s.mayReachChapter(t.ChapterID) {
			return t, s.outside(outsideChapter)
		}
		return t, s.checkExcluded(chapterPrefix, t.ChapterID)
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
		if err := s.checkBook(bookID); err != nil {
			return bookID, err
		}
		return bookID, s.checkExcluded(bookPrefix, bookID)
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

// loadExcludedShelves reads the excluded shelves this call has not read yet, each at most once, and stops as
// soon as one holds want; want 0 reads them all. Like a bound shelf, an excluded one stands for the books on it
// at the time of the call. A shelf that cannot be read fails the call instead of letting its books through,
// and the failure names neither the shelf nor the exclusion.
func (c *Client) loadExcludedShelves(ctx context.Context, want int64) error {
	if c.exShelfBooks == nil {
		c.exShelfBooks = map[int64]bool{}
		c.exShelvesRead = map[int64]bool{}
	}
	for _, id := range c.scope.exShelves {
		if want != 0 && c.exShelfBooks[want] {
			return nil
		}
		if c.exShelvesRead[id] {
			continue
		}
		var shelf struct {
			Books []struct {
				ID int64 `json:"id"`
			} `json:"books"`
		}
		if err := c.get(ctx, "get shelf", "/api/shelves/"+strconv.FormatInt(id, 10), nil, &shelf, nil, provider.ClassPermission); err != nil {
			return unreadableExclusion(err)
		}
		c.exShelvesRead[id] = true
		for _, b := range shelf.Books {
			c.exShelfBooks[b.ID] = true
		}
	}
	return nil
}

// unreadableExclusion is the failure of an excluded shelf's read without the object it concerns: an answer that
// says nothing about the shelf, such as a rate limit, keeps its class; one that leaves it unknown is a refusal.
func unreadableExclusion(err error) error {
	var perr *provider.Error
	if !errors.As(err, &perr) {
		return err
	}
	switch perr.Class {
	case provider.ClassNotFound, provider.ClassPermission, provider.ClassInvalidResponse:
		return invalidRequest(unprovable)
	}
	return &provider.Error{Class: perr.Class, Op: "check reach", Message: perr.Message, Cause: perr.Cause}
}

// bookOut reports whether a book is excluded, by itself or through an excluded shelf; it decides only after
// loadExcludedShelves read every excluded shelf. A book that cannot be named counts as excluded as soon as a
// book can be.
func (c *Client) bookOut(id int64) bool {
	if id <= 0 {
		return c.scope.excludesBooks()
	}
	return c.scope.excludesBook(id) || c.exShelfBooks[id]
}

// bookExcluded is bookOut for one book, reading the excluded shelves only as far as needed.
func (c *Client) bookExcluded(ctx context.Context, id int64) (bool, error) {
	if id <= 0 || c.scope.excludesBook(id) || len(c.scope.exShelves) == 0 {
		return c.bookOut(id), nil
	}
	if err := c.loadExcludedShelves(ctx, id); err != nil {
		return true, err
	}
	return c.bookOut(id), nil
}

// pageOut reports whether a page with its chapter and book is excluded; like bookOut it decides only after
// every excluded shelf was read.
func (c *Client) pageOut(id, chapterID, bookID int64) bool {
	return c.scope.excludesPage(id) || (chapterID > 0 && c.scope.excludesChapter(chapterID)) || c.bookOut(bookID)
}

// inBook reports whether a book is bound or stands on a shelf this call has read.
func (c *Client) inBook(id int64) bool { return id > 0 && (c.scope.hasBook(id) || c.shelfBooks[id]) }

// bookAllowed reports whether the connection reaches a whole book: without targets, a bound book, or a book on
// a bound shelf, and in each case not excluded.
func (c *Client) bookAllowed(ctx context.Context, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	if c.scope.bound() && !c.scope.hasBook(id) {
		if len(c.scope.shelves) == 0 {
			return false, nil
		}
		if err := c.loadShelves(ctx, id); err != nil {
			return false, err
		}
		if !c.inBook(id) {
			return false, nil
		}
	}
	excluded, err := c.bookExcluded(ctx, id)
	return !excluded, err
}

// requireBook proves a book_id before it is used: locally against the bound books and the exclusions, through
// the bound and the excluded shelves otherwise. A connection without targets and exclusions reads nothing.
func (c *Client) requireBook(ctx context.Context, id int64) error {
	if err := c.scope.checkBook(id); err != nil || !c.scope.limited() {
		return err
	}
	if err := c.scope.checkExcluded(bookPrefix, id); err != nil {
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

// readChapterRef reads a chapter once as evidence of its book.
func (c *Client) readChapterRef(ctx context.Context, id int64) (bookRef, error) {
	var ref bookRef
	err := c.get(ctx, "get chapter", "/api/chapters/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission)
	return ref, err
}

// chapterBook reads a chapter once and returns its book if the connection reaches that whole book.
func (c *Client) chapterBook(ctx context.Context, id int64) (int64, error) {
	if c.scope.excludesChapter(id) || (c.scope.bound() && !c.scope.bookWide()) {
		return 0, c.scope.outside(outsideChapter)
	}
	ref, err := c.readChapterRef(ctx, id)
	if err != nil {
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

// chapterAllowed reports whether a chapter read with its book lies in the connection: not excluded, and bound
// itself or in a whole book the connection reaches. A bound chapter is still out of reach in an excluded book.
func (c *Client) chapterAllowed(ctx context.Context, id, bookID int64) (bool, error) {
	if c.scope.excludesChapter(id) {
		return false, nil
	}
	if c.scope.hasChapter(id) {
		excluded, err := c.bookExcluded(ctx, bookID)
		return !excluded, err
	}
	return c.bookAllowed(ctx, bookID)
}

// chapterIn proves that the connection reaches a chapter and its pages: a bound chapter needs no read unless a
// book can be excluded, any other one its book. book is the chapter's book when it was read, 0 otherwise.
func (c *Client) chapterIn(ctx context.Context, id int64) (book int64, err error) {
	if !c.scope.limited() {
		return 0, nil
	}
	if c.scope.excludesChapter(id) || (c.scope.bound() && !c.scope.mayReachChapter(id)) {
		return 0, c.scope.outside(outsideChapter)
	}
	if !c.scope.excludesBooks() && (c.scope.hasChapter(id) || !c.scope.bound()) {
		return 0, nil
	}
	ref, err := c.readChapterRef(ctx, id)
	if err != nil {
		return 0, err
	}
	ok, err := c.chapterAllowed(ctx, id, ref.BookID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, c.scope.outside(outsideChapter)
	}
	return ref.BookID, nil
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
// whole book is reached, and neither the page nor its chapter nor its book is excluded.
func (c *Client) pageAllowed(ctx context.Context, id int64, ref bookRef) (bool, error) {
	if c.scope.excludesPage(id) || (ref.ID > 0 && c.scope.excludesPage(ref.ID)) ||
		(ref.ChapterID > 0 && c.scope.excludesChapter(ref.ChapterID)) {
		return false, nil
	}
	if c.scope.bound() && !c.scope.hasPage(id) && (ref.ChapterID <= 0 || !c.scope.hasChapter(ref.ChapterID)) {
		return c.bookAllowed(ctx, ref.BookID)
	}
	excluded, err := c.bookExcluded(ctx, ref.BookID)
	return !excluded, err
}

// pageInScope proves a page by identifier: a bound page needs no read unless a chapter or book can be
// excluded, any other one is read once as evidence of its chapter and book. A connection without targets and
// exclusions reads nothing.
func (c *Client) pageInScope(ctx context.Context, id int64) (bool, error) {
	return c.pageReached(ctx, id, func() (bookRef, error) {
		var ref bookRef
		err := c.get(ctx, "get page", "/api/pages/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission)
		return ref, err
	})
}

// pageReached is the reach of a page by identifier; evidence reads the page only when the targets and the
// exclusions alone cannot decide, so a caller that needs more of the page reads it once.
func (c *Client) pageReached(ctx context.Context, id int64, evidence func() (bookRef, error)) (bool, error) {
	if c.scope.excludesPage(id) {
		return false, nil
	}
	if !c.scope.excludesContainers() && (!c.scope.bound() || c.scope.hasPage(id)) {
		return true, nil
	}
	if c.scope.bound() && !c.scope.mayReachPage(id) {
		return false, nil
	}
	ref, err := evidence()
	if err != nil {
		return false, err
	}
	return c.pageAllowed(ctx, id, ref)
}

// requirePageBound proves a page before a change or a read of something on it; the refusal names neither the
// page nor a target.
func (c *Client) requirePageBound(ctx context.Context, id string) error {
	if !c.scope.limited() {
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
