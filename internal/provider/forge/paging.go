package forge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Lists page with page (from 1) and limit. An instance caps limit silently at its max_response_items, 50
// unless its administrator changed it, so a batch never asks for more. The end of a list comes from the Link
// header, or without one from X-Total-Count, never from a batch shorter than its limit.
const (
	defaultLimit    = 30
	maxLimit        = 50
	maxPage         = 100000
	maxCursorLength = 256
)

const pagingKeys = `"limit":{"type":"integer","minimum":1,"maximum":50},` +
	`"cursor":{"type":"string","minLength":1,"maxLength":256,"pattern":"^[A-Za-z0-9_-]+$"}`

var pagingArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 50; 30 when omitted; the instance may serve " +
		"fewer; a continuation keeps the batch size of its first batch"},
	{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same arguments; the first " +
		"batch when omitted"},
}

var pagingFields = []capability.Field{
	{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
	{Name: "has_more", Description: "True when the instance reports further entries"},
}

// paging is the page one batch reads, bound to the arguments of the list it continues.
type paging struct {
	page    int
	limit   int
	binding []byte
}

// startPaging reads the batch arguments. A cursor carries its page and batch size, so a continuation that
// names another limit is refused rather than skipping or repeating entries.
func startPaging(binding []byte, limit int, cursor string) (paging, error) {
	if limit != 0 && (limit < 1 || limit > maxLimit) {
		return paging{}, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxLimit))
	}
	if cursor == "" {
		if limit == 0 {
			limit = defaultLimit
		}
		return paging{page: 1, limit: limit, binding: binding}, nil
	}
	const refused = "cursor is not a next_cursor of this list; start the list again without cursor"
	inner, ok := provider.DecodeCursor(binding, cursor, maxCursorLength)
	pageText, limitText, cut := strings.Cut(inner, ":")
	page, pageErr := strconv.Atoi(pageText)
	size, sizeErr := strconv.Atoi(limitText)
	if !ok || !cut || pageErr != nil || sizeErr != nil || page < 2 || page > maxPage || size < 1 || size > maxLimit {
		return paging{}, invalidArgument(refused)
	}
	if limit != 0 && limit != size {
		return paging{}, invalidArgument("limit must stay " + strconv.Itoa(size) + " for this cursor, or be left out")
	}
	return paging{page: page, limit: size, binding: binding}, nil
}

func (p paging) query() url.Values {
	return url.Values{"page": {strconv.Itoa(p.page)}, "limit": {strconv.Itoa(p.limit)}}
}

// next returns the cursor of the following batch, or "" at the end of the list. rows is the number of entries
// the instance answered for this batch, before any row was filtered out.
func (p paging) next(header http.Header, rows int) string {
	if !hasMore(header, p.page, p.limit, rows) || p.page >= maxPage {
		return ""
	}
	return provider.EncodeCursor(p.binding, strconv.Itoa(p.page+1)+":"+strconv.Itoa(p.limit))
}

// hasMore reads the end of a list. A Link header is decisive: the instance sets it from the page size it
// actually served. Without one, an instance sets no Link because the list fits on one page; X-Total-Count is
// then checked against the entries read so far.
func hasMore(header http.Header, page, limit, rows int) bool {
	if links := header.Values("Link"); len(links) > 0 {
		for _, link := range links {
			for _, part := range strings.Split(link, ",") {
				for _, param := range strings.Split(part, ";")[1:] {
					name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
					if strings.EqualFold(strings.TrimSpace(name), "rel") &&
						strings.Contains(" "+strings.Trim(strings.TrimSpace(value), `"`)+" ", " next ") {
						return true
					}
				}
			}
		}
		return false
	}
	total, err := strconv.Atoi(strings.TrimSpace(header.Get("X-Total-Count")))
	if err != nil || total < 0 {
		return false
	}
	return (page-1)*limit+rows < total
}

// unwrapList returns the entries of a list answer: a JSON array, or, where wrapped is true, the data array of
// the {"ok":true,"data":[...]} wrapper the repository search answers with. A wrapper without ok is invalid.
func unwrapList(op, subject string, raw json.RawMessage, wrapped bool) ([]json.RawMessage, error) {
	var entries []json.RawMessage
	if !wrapped {
		if json.Unmarshal(raw, &entries) != nil {
			return nil, provider.InvalidResponse(op, subject+" returned an invalid list")
		}
		return entries, nil
	}
	var wrapper struct {
		OK   bool              `json:"ok"`
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapper) != nil || !wrapper.OK {
		return nil, provider.InvalidResponse(op, subject+" returned an invalid list")
	}
	return wrapper.Data, nil
}
