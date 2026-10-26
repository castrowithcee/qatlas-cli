package dav

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	nsDAV     = "DAV:"
	nsSabre   = "http://sabredav.org/ns"
	maxDepth  = 1 // children of the root
	rootError = "error"
)

// ErrorOriginal builds the Original of a failed DAV answer from its status and body. It reads at most
// provider.ErrorBodyLimit bytes. A DAV:error body (RFC 4918 section 16) yields one entry per condition element,
// its DAV:responsedescription as a message, and the SabreDAV exception and message as one entry. Any other body
// is kept as trimmed text, an empty body leaves only the status.
func ErrorOriginal(status int, body io.Reader) *provider.Original {
	data := provider.ReadErrorBody(body)
	if entries, ok := parseError(data); ok {
		return &provider.Original{Status: status, Entries: entries}
	}
	return provider.ErrorOriginal(status, data, provider.OriginalFields{})
}

// parseError reads the direct children of a DAV:error root. ok is false if the root is something else.
// A truncated body keeps what was read before the cut.
func parseError(data []byte) (entries []provider.OriginalEntry, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sabre provider.OriginalEntry
	var current xml.Name
	depth := 0
	rooted := false
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 && (t.Name.Space != nsDAV || t.Name.Local != rootError) {
				return nil, false
			}
			rooted = true
			if depth == maxDepth {
				current = t.Name
				text.Reset()
			}
			depth++
		case xml.CharData:
			if depth == maxDepth+1 {
				text.Write(t)
			}
		case xml.EndElement:
			depth--
			if depth != maxDepth {
				continue
			}
			value := strings.TrimSpace(strings.ToValidUTF8(text.String(), ""))
			switch {
			case current.Space == nsSabre && current.Local == "exception":
				sabre.Type = value
			case current.Space == nsSabre && current.Local == "message":
				sabre.Message = value
			case current.Space == nsSabre:
			case current.Space == nsDAV && current.Local == "responsedescription":
				if value != "" {
					entries = append(entries, provider.OriginalEntry{Message: value})
				}
			default:
				entries = append(entries, provider.OriginalEntry{Type: current.Local})
			}
		}
	}
	if sabre != (provider.OriginalEntry{}) {
		entries = append(entries, sabre)
	}
	return entries, rooted
}
