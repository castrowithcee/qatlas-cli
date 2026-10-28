package bookstack

import (
	"context"
	"errors"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// provePages decides for each page a listed row points to whether the connection reaches it, once per page
// and call. A page is read as evidence of its chapter and book only when the targets and the exclusions alone
// cannot decide, and every excluded shelf at most once. A page that cannot be read or does not answer as the
// page asked for counts as out of reach, so its rows are left out: one read per page proves a row, while a set
// of the pages below the excluded containers would let through a page the listing happens to miss. A failure
// that says nothing about the page, such as a rate limit, fails the call.
func (c *Client) provePages(ctx context.Context, ids []int64) error {
	if !c.scope.limited() {
		return nil
	}
	if c.pagesIn == nil {
		c.pagesIn = map[int64]bool{}
	}
	for _, id := range ids {
		if _, done := c.pagesIn[id]; done {
			continue
		}
		in, err := c.pageRowReached(ctx, id)
		if err != nil {
			return err
		}
		c.pagesIn[id] = in
	}
	return nil
}

// pageRowReached is the reach of the page one row points to; a page without an identifier is out.
func (c *Client) pageRowReached(ctx context.Context, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	unreadable := false
	in, err := c.pageReached(ctx, id, func() (bookRef, error) {
		var ref bookRef
		err := c.get(ctx, "get page", "/api/pages/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission)
		var perr *provider.Error
		if errors.As(err, &perr) {
			switch perr.Class {
			case provider.ClassNotFound, provider.ClassPermission, provider.ClassInvalidResponse:
				unreadable = true
			}
		}
		if err == nil && ref.ID != id {
			unreadable = true
			err = errors.New("the answer is not the requested page")
		}
		return ref, err
	})
	if unreadable {
		return false, nil
	}
	return in, err
}

// rowPageIn reports a verdict of provePages; a page it has not seen is out. Without targets and exclusions
// every page is in.
func (c *Client) rowPageIn(id int64) bool { return !c.scope.limited() || c.pagesIn[id] }

// unreadableAnswer reports an answer that says nothing about the object read: it is missing, forbidden, or not
// what was asked for. Any other failure, such as a rate limit, is not an answer about the object.
func unreadableAnswer(err error) bool {
	var perr *provider.Error
	if !errors.As(err, &perr) {
		return false
	}
	switch perr.Class {
	case provider.ClassNotFound, provider.ClassPermission, provider.ClassInvalidResponse:
		return true
	}
	return false
}

// bookOutLive reports whether a book is excluded or cannot be proven outside the excluded shelves. A deleted
// book no longer shows on its shelf, so with an excluded shelf only a book that answers a read is proven; one
// that does not counts as out. A book that cannot be named counts as out as soon as a book can be excluded.
func (c *Client) bookOutLive(ctx context.Context, id int64) (bool, error) {
	out, err := c.bookExcluded(ctx, id)
	if err != nil || out || len(c.scope.exShelves) == 0 {
		return out, err
	}
	if live, done := c.booksLive[id]; done {
		return !live, nil
	}
	var ref bookRef
	err = c.get(ctx, "get book", "/api/books/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission)
	live := err == nil && ref.ID == id
	if err != nil && !unreadableAnswer(err) {
		return true, err
	}
	if c.booksLive == nil {
		c.booksLive = map[int64]bool{}
	}
	c.booksLive[id] = live
	return !live, nil
}

// chapterRowOut reports whether the chapter a row points to is excluded, itself or through its book. Only a
// book exclusion needs the chapter read as evidence, and a chapter that cannot be read counts as out then.
func (c *Client) chapterRowOut(ctx context.Context, id int64) (bool, error) {
	if c.scope.excludesChapter(id) || id <= 0 {
		return true, nil
	}
	if !c.scope.excludesBooks() {
		return false, nil
	}
	ref, err := c.readChapterRef(ctx, id)
	if unreadableAnswer(err) || (err == nil && ref.ID != id) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return c.bookExcluded(ctx, ref.BookID)
}
