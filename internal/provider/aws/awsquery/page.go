package awsquery

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// MaxTokenLength bounds the token a service hands back. The wrapped cursor stays below MaxCursorLength.
const (
	MaxTokenLength  = 2048
	MaxCursorLength = 4096
)

// Argument names of the paging every generated list tool offers.
const (
	ArgPageSize = "page_size"
	ArgCursor   = "cursor"
)

// Pager describes how one list operation pages, as data. NextToken services set TokenIn and TokenOut;
// Marker services also set Truncated, which names the boolean output member (IsTruncated) that says whether
// the Marker continues the list.
type Pager struct {
	// TokenIn is the wire name of the request parameter that carries the token, for example NextToken or
	// Marker.
	TokenIn string
	// TokenOut is the Key of the output member that holds the next token.
	TokenOut string
	// Truncated is the Key of the boolean output member that decides the end of a Marker list. Empty for a
	// NextToken list, whose end is a missing token.
	Truncated string
	// SizeParam is the wire name of the page size parameter, empty when the service has none. Without it a
	// page_size argument is refused instead of being dropped.
	SizeParam string
	// MaxSize is the largest page size the service accepts; ignored without SizeParam.
	MaxSize int
}

// Page is one request of a list: the token to continue with and the page size to ask for.
type Page struct {
	pager   Pager
	binding []byte
	size    int
	token   string
}

// Binding returns the cursor binding of a list call: the tool and every argument except the ones that do not
// change the sequence (page_size) and the cursor itself. The arguments should hold the normalized target,
// such as the region, so a cursor is not valid for another one.
func Binding(tool string, args map[string]any) []byte {
	rest := make(map[string]any, len(args))
	for k, v := range args {
		if k != ArgPageSize && k != ArgCursor {
			rest[k] = v
		}
	}
	// encoding/json writes map keys sorted, so the same arguments always bind the same.
	encoded, _ := json.Marshal(rest)
	return provider.CursorBinding("aws", tool, string(encoded))
}

// Start reads the paging arguments of a request. pageSize is zero when the caller gave none. A cursor that
// is malformed, altered, or issued for other arguments is refused with an InvalidRequestError.
func (p Pager) Start(binding []byte, pageSize int, cursor string) (Page, error) {
	page := Page{pager: p, binding: binding, size: pageSize}
	if pageSize != 0 {
		switch {
		case p.SizeParam == "":
			return Page{}, &provider.InvalidRequestError{Message: "page_size is not supported by this operation"}
		case pageSize < 1 || pageSize > p.MaxSize:
			return Page{}, &provider.InvalidRequestError{Message: "page_size must be between 1 and " + strconv.Itoa(p.MaxSize)}
		}
	}
	if cursor != "" {
		inner, ok := provider.DecodeCursor(binding, cursor, MaxCursorLength)
		if !ok || !usableToken(inner) {
			return Page{}, &provider.InvalidRequestError{
				Message: "cursor is not a next_cursor of this list; start the list again without cursor"}
		}
		page.token = inner
	}
	return page, nil
}

// Params returns the form parameters this page adds to the request.
func (pg Page) Params() map[string]string {
	params := map[string]string{}
	if pg.size > 0 {
		params[pg.pager.SizeParam] = strconv.Itoa(pg.size)
	}
	if pg.token != "" {
		params[pg.pager.TokenIn] = pg.token
	}
	return params
}

// Next returns the cursor of the following page from a decoded answer, or "" at the end of the list. It
// never takes the number of entries into account: a full page says nothing about the end. An answer that
// claims more entries but gives no usable token, or that repeats the token it was asked with, is invalid,
// since ending the list there would hide entries and continuing would loop.
func (pg Page) Next(op string, out map[string]any) (string, *provider.Error) {
	invalid := func() (string, *provider.Error) {
		return "", provider.InvalidResponse(op, "AWS returned an invalid response")
	}
	token, present := out[pg.pager.TokenOut]
	if pg.pager.Truncated != "" {
		more, isBool := out[pg.pager.Truncated].(bool)
		if _, set := out[pg.pager.Truncated]; set && !isBool {
			return invalid()
		}
		if !more {
			return "", nil
		}
		// IsTruncated says there is more, so a missing token cannot be read as the end.
		if token == nil {
			return invalid()
		}
		present = true
	}
	if !present || token == nil {
		return "", nil
	}
	text, isString := token.(string)
	if isString && text == "" && pg.pager.Truncated == "" {
		// An empty NextToken cannot continue anything; it is how a service says it has no more.
		return "", nil
	}
	if !isString || !usableToken(text) || text == pg.token {
		return invalid()
	}
	return provider.EncodeCursor(pg.binding, text), nil
}

// usableToken accepts the opaque token of a service: non-empty, bounded, text without control characters.
func usableToken(t string) bool {
	if t == "" || len(t) > MaxTokenLength || !utf8.ValidString(t) {
		return false
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
