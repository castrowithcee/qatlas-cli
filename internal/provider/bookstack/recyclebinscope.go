package bookstack

import (
	"context"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// outsideDeletion is the subject of a recycle bin entry refused for the exclusions.
const outsideDeletion = "the deletion"

// deletionOut reports whether a recycle bin entry belongs to an excluded object: the item itself, its chapter,
// its book, or the parent it names, an excluded shelf included. A deleted book is not on its shelf any more,
// so with an excluded shelf it counts as out unless it answers a read (see bookOutLive). An entry whose book
// cannot be told counts as out as soon as a book can be excluded, and one of an unknown type as soon as
// anything is excluded.
func (c *Client) deletionOut(ctx context.Context, e recycleBinEntryJSON) (bool, error) {
	if !c.scope.excludes() {
		return false, nil
	}
	d := e.Deletable
	bookID, chapterID := int64(0), int64(0)
	if d.BookID != nil {
		bookID = *d.BookID
	}
	if d.ChapterID != nil {
		chapterID = *d.ChapterID
	}
	if p := d.Parent; p != nil {
		switch p.Type {
		case "bookshelf":
			if c.scope.excludesShelf(p.ID) {
				return true, nil
			}
		case "book":
			if bookID == 0 {
				bookID = p.ID
			}
		case "chapter":
			if chapterID == 0 {
				chapterID = p.ID
			}
		}
	}
	if chapterID > 0 && c.scope.excludesChapter(chapterID) {
		return true, nil
	}
	switch e.DeletableType {
	case "bookshelf":
		return c.scope.excludesShelf(e.DeletableID), nil
	case "book":
		return c.bookOutLive(ctx, e.DeletableID)
	case "chapter":
		if c.scope.excludesChapter(e.DeletableID) {
			return true, nil
		}
	case "page":
		if c.scope.excludesPage(e.DeletableID) {
			return true, nil
		}
	default:
		return true, nil
	}
	return c.bookOutLive(ctx, bookID)
}

// findDeletion reads the entry of one deletion from the recycle bin list; the API has no read of one entry.
func (c *Client) findDeletion(ctx context.Context, id int64) (recycleBinEntryJSON, bool, error) {
	var found recycleBinEntryJSON
	spec := scanSpec[recycleBinEntryJSON]{
		op: "list recycle bin", path: "/api/recycle-bin", filtered: true, limit: 1,
		query:     url.Values{"filter[id]": {strconv.FormatInt(id, 10)}},
		arguments: argNames(recycleBinList),
		id:        func(e recycleBinEntryJSON) int64 { return e.ID },
		keep: func(e recycleBinEntryJSON) bool {
			if e.ID != id {
				return false
			}
			found = e
			return true
		},
		row: recycleBinRow,
	}
	rows, err := scanList(ctx, c, spec)
	return found, len(rows) > 0, err
}

// requireDeletion proves a recycle bin entry before it is restored or destroyed, on a connection with
// exclusions only: it must be listed, belong to no excluded object, and not hold one. The refusal names
// neither the entry nor an exclusion.
func (c *Client) requireDeletion(ctx context.Context, id int64) error {
	if !c.scope.excludes() {
		return nil
	}
	entry, found, err := c.findDeletion(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return invalidRequest(unprovable)
	}
	out, err := c.deletionOut(ctx, entry)
	if err != nil {
		return err
	}
	if out {
		return c.scope.outside(outsideDeletion)
	}
	holds, err := c.deletionHoldsExcluded(ctx, entry)
	if err != nil {
		return err
	}
	if holds {
		return c.scope.outside(outsideBelow)
	}
	return nil
}

// deletionHoldsExcluded reports whether a deleted book or chapter may hold an excluded chapter or page, since
// restoring or destroying the entry takes everything deleted with it. The API does not list what an entry
// holds, so each excluded object that could lie below is read: one that still answers outside the entry (a
// different book, or a different chapter for a page below a chapter) is not below it. One that does not
// answer, or answers inside the entry, cannot be ruled out and counts as held.
func (c *Client) deletionHoldsExcluded(ctx context.Context, e recycleBinEntryJSON) (bool, error) {
	var chapters, pages []int64
	switch e.DeletableType {
	case "book":
		chapters, pages = c.scope.exChapters, c.scope.exPages
	case "chapter":
		pages = c.scope.exPages
	default:
		return false, nil
	}
	apart := func(path string, id int64, outside func(bookRef) bool) (bool, error) {
		var ref bookRef
		err := c.get(ctx, "get "+path, "/api/"+path+"s/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission)
		if unreadableAnswer(err) {
			return false, nil
		}
		return err == nil && ref.ID == id && outside(ref), err
	}
	for _, id := range chapters {
		ok, err := apart("chapter", id, func(r bookRef) bool { return r.BookID != e.DeletableID })
		if err != nil || !ok {
			return true, err
		}
	}
	for _, id := range pages {
		ok, err := apart("page", id, func(r bookRef) bool {
			if e.DeletableType == "chapter" {
				return r.ChapterID != e.DeletableID
			}
			return r.BookID != e.DeletableID
		})
		if err != nil || !ok {
			return true, err
		}
	}
	return false, nil
}
