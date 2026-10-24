package matomo

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Matomo lists carry no total over HTTP. A list tool reads one row more than it returns: the extra row, and
// nothing else, says that a further page exists. The cursor carries the offset and the page size and is bound
// to the tool, the connection's targets, and the arguments that shape the list.
const (
	defaultLimit    = 25
	maxLimit        = 100
	maxOffset       = 10000
	maxCursorLength = 128
)

const pagingKeys = `"limit":{"type":"integer","minimum":1,"maximum":100},` +
	`"cursor":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9_-]+$"}`

var pagingArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 100; 25 when omitted; a continuation keeps " +
		"the batch size of its first batch"},
	{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same arguments; the first " +
		"batch when omitted"},
}

var pagingFields = []capability.Field{
	{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
	{Name: "has_more", Description: "True when Matomo returned entries beyond this batch"},
}

// page is one batch of a list, bound to the arguments of the list it continues.
type page struct {
	offset  int
	limit   int
	binding []byte
}

// startPage reads the batch arguments. A cursor carries its offset and batch size, so a continuation that
// names another limit is refused rather than skipping or repeating entries. A manipulated cursor, or one of
// another list, is refused.
func startPage(binding []byte, limit int, cursor string) (page, error) {
	if limit != 0 && (limit < 1 || limit > maxLimit) {
		return page{}, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxLimit))
	}
	if cursor == "" {
		if limit == 0 {
			limit = defaultLimit
		}
		return page{limit: limit, binding: binding}, nil
	}
	const refused = "cursor is not a next_cursor of this list; start the list again without cursor"
	inner, ok := provider.DecodeCursor(binding, cursor, maxCursorLength)
	offsetText, limitText, cut := strings.Cut(inner, ":")
	offset, offsetErr := strconv.Atoi(offsetText)
	size, sizeErr := strconv.Atoi(limitText)
	if !ok || !cut || offsetErr != nil || sizeErr != nil || offset < 1 || offset > maxOffset || size < 1 || size > maxLimit {
		return page{}, invalidArgument(refused)
	}
	if limit != 0 && limit != size {
		return page{}, invalidArgument("limit must stay " + strconv.Itoa(size) + " for this cursor, or be left out")
	}
	return page{offset: offset, limit: size, binding: binding}, nil
}

// want is the number of rows to ask for: everything up to the end of this batch plus the one row that tells
// whether another follows. It is the value for a method that limits the number of rows it returns from the
// start of the list.
func (p page) want() int { return p.offset + p.limit + 1 }

// serverWindow adds the filter_limit and filter_offset of a DataTable method, which pages on the server: the
// batch plus one row from the offset on. A method without these parameters uses want and slice instead.
func (p page) serverWindow(params url.Values) {
	params.Set("filter_offset", strconv.Itoa(p.offset))
	params.Set("filter_limit", strconv.Itoa(p.limit+1))
}

// slice cuts this batch out of rows that start at the beginning of the list, and returns the cursor of the
// next batch, empty at the end. rows must hold the rows from the start of the list up to at least want of them.
func slice[T any](p page, rows []T) (batch []T, next string) {
	return cut(p, rows, 0)
}

// sliceWindow cuts this batch out of the rows a method with filter_offset and filter_limit answered, which
// start at the offset of the batch.
func sliceWindow[T any](p page, rows []T) (batch []T, next string) {
	return cut(p, rows, p.offset)
}

// cut cuts this batch out of rows whose first row is entry from of the list.
func cut[T any](p page, rows []T, from int) (batch []T, next string) {
	start := p.offset - from
	if start > len(rows) {
		start = len(rows)
	}
	rest := rows[start:]
	if len(rest) > p.limit {
		return rest[:p.limit], p.cursor(p.offset + p.limit)
	}
	return rest, ""
}

func (p page) cursor(offset int) string {
	if offset > maxOffset {
		return ""
	}
	return provider.EncodeCursor(p.binding, strconv.Itoa(offset)+":"+strconv.Itoa(p.limit))
}

// decodeArguments reads the arguments of a call; an empty body is an empty object.
func decodeArguments(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if json.Unmarshal(raw, out) != nil {
		return invalidArgument("the arguments could not be read")
	}
	return nil
}
