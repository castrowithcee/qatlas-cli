package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Server names the DAV server a parser talks to. Name appears in error messages and Origin is the only host
// a node location may name.
type Server struct{ Name, Origin string }

// Namespaces and elements this parser reads. Everything else in an answer is dropped, so a server cannot
// widen the result by returning more than was asked for.
const (
	davNS    = "DAV:"
	caldavNS = "urn:ietf:params:xml:ns:caldav"
	carddavN = "urn:ietf:params:xml:ns:carddav"
	appleNS  = "http://apple.com/ns/ical/"
)

var (
	elemMultistatus  = xml.Name{Space: davNS, Local: "multistatus"}
	elemResponse     = xml.Name{Space: davNS, Local: "response"}
	elemHref         = xml.Name{Space: davNS, Local: "href"}
	elemPropstat     = xml.Name{Space: davNS, Local: "propstat"}
	elemProp         = xml.Name{Space: davNS, Local: "prop"}
	elemStatus       = xml.Name{Space: davNS, Local: "status"}
	elemResourceType = xml.Name{Space: davNS, Local: "resourcetype"}

	PropPrincipal    = xml.Name{Space: davNS, Local: "current-user-principal"}
	PropCalHome      = xml.Name{Space: caldavNS, Local: "calendar-home-set"}
	PropBookHome     = xml.Name{Space: carddavN, Local: "addressbook-home-set"}
	propCalendarData = xml.Name{Space: caldavNS, Local: "calendar-data"}
	propAddressData  = xml.Name{Space: carddavN, Local: "address-data"}
	typeCalendar     = xml.Name{Space: caldavNS, Local: "calendar"}
	typeAddressbook  = xml.Name{Space: carddavN, Local: "addressbook"}
)

// Keys of the text properties.
const (
	KeyName        = "displayname"
	KeyDescription = "description"
	KeyColor       = "color"
	KeyETag        = "etag"
	KeyCalendar    = "calendar-data"
	KeyAddress     = "address-data"
)

var textProps = map[xml.Name]string{
	{Space: davNS, Local: "displayname"}:                KeyName,
	{Space: caldavNS, Local: "calendar-description"}:    KeyDescription,
	{Space: carddavN, Local: "addressbook-description"}: KeyDescription,
	{Space: appleNS, Local: "calendar-color"}:           KeyColor,
	{Space: davNS, Local: "getetag"}:                    KeyETag,
	{Space: caldavNS, Local: "calendar-data"}:           KeyCalendar,
	{Space: carddavN, Local: "address-data"}:            KeyAddress,
}

// Bounds of one parsed answer.
const (
	// MaxResponseBytes caps one multi-status answer, MaxEventBytes one stored event or contact, in an answer,
	// a GET answer, and an encoded body.
	MaxResponseBytes = 1 << 20
	MaxEntries       = 500
	maxXMLDepth      = 16
	maxTextBytes     = 4 << 10
	MaxEventBytes    = 64 << 10
	maxLinks         = 8
	maxSegments      = 16
	maxSegmentLen    = 255
)

// Resource is one d:response reduced to what a DAV provider reads. Only successful propstat blocks
// contribute.
type Resource struct {
	Href   string
	Status string
	Text   map[string]string
	// Links holds the hrefs nested in the principal and home set properties, by property.
	Links map[xml.Name][]string
	// Calendar and Addressbook report the resource type; collection is not needed on its own.
	Calendar, Addressbook bool
	Read                  bool
}

// ParseMultiStatus reads a 207 answer with a bounded token walk. Depth, element count, and text length are
// capped, and a document that does not match the documented shape is refused before any value is used.
func (s Server) ParseMultiStatus(op string, body []byte) ([]Resource, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true

	var (
		stack     []xml.Name
		resources []Resource
		current   *Resource
		pending   *Resource
		status    string
		text      strings.Builder
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, provider.InvalidResponse(op, s.Name+" returned an invalid multi-status document")
		}
		switch element := token.(type) {
		case xml.StartElement:
			if len(stack) >= maxXMLDepth {
				return nil, provider.InvalidResponse(op, "the "+s.Name+" response is nested too deeply")
			}
			stack = append(stack, element.Name)
			text.Reset()
			switch {
			case len(stack) == 1 && element.Name != elemMultistatus:
				return nil, provider.InvalidResponse(op, s.Name+" did not answer with a multi-status document")
			case len(stack) == 2 && element.Name == elemResponse:
				if len(resources) >= MaxEntries {
					return nil, provider.InvalidResponse(op, s.Name+" reported more nodes than one read may handle")
				}
				current = &Resource{Text: map[string]string{}, Links: map[xml.Name][]string{}}
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				pending = &Resource{Text: map[string]string{}, Links: map[xml.Name][]string{}}
				status = ""
			case len(stack) == 6 && pending != nil && stack[4] == elemResourceType:
				switch element.Name {
				case typeCalendar:
					pending.Calendar = true
				case typeAddressbook:
					pending.Addressbook = true
				}
			}
		case xml.CharData:
			if len(stack) > 0 && (stack[len(stack)-1] == propCalendarData || stack[len(stack)-1] == propAddressData) {
				// An event or contact is never cut: one beyond its cap refuses the answer.
				if text.Len()+len(element) > MaxEventBytes {
					return nil, provider.InvalidResponse(op, s.Name+" returned an item larger than one read may handle")
				}
				text.Write(element)
			} else if text.Len()+len(element) <= maxTextBytes {
				text.Write(element)
			}
		case xml.EndElement:
			switch {
			case len(stack) == 3 && current != nil && element.Name == elemHref:
				current.Href = strings.TrimSpace(text.String())
			case len(stack) == 3 && current != nil && element.Name == elemStatus:
				current.Status = text.String()
			case len(stack) == 4 && pending != nil && element.Name == elemStatus:
				status = text.String()
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				if code, ok := StatusCodeOf(status); ok && code >= 200 && code < 300 {
					current.Read = true
					current.Calendar = current.Calendar || pending.Calendar
					current.Addressbook = current.Addressbook || pending.Addressbook
					for key, value := range pending.Text {
						current.Text[key] = value
					}
					for key, values := range pending.Links {
						current.Links[key] = append(current.Links[key], values...)
					}
				}
				pending = nil
			case len(stack) == 6 && pending != nil && element.Name == elemHref && stack[3] == elemProp:
				name := stack[4]
				if name == PropPrincipal || name == PropCalHome || name == PropBookHome {
					if len(pending.Links[name]) >= maxLinks {
						return nil, provider.InvalidResponse(op, s.Name+" answered with too many locations")
					}
					pending.Links[name] = append(pending.Links[name], strings.TrimSpace(text.String()))
				}
			case len(stack) == 5 && pending != nil && stack[3] == elemProp:
				if key, ok := textProps[element.Name]; ok {
					pending.Text[key] = strings.TrimSpace(text.String())
				}
			case len(stack) == 2 && current != nil && element.Name == elemResponse:
				resources = append(resources, *current)
				current = nil
			}
			text.Reset()
			stack = stack[:len(stack)-1]
		}
	}
	return resources, nil
}

func StatusCodeOf(line string) (int, bool) {
	for _, field := range strings.Fields(line) {
		if code, err := strconv.Atoi(field); err == nil {
			return code, true
		}
	}
	return 0, false
}

// Segments turns an untrusted href into decoded path segments. The href must be on the fixed origin or
// a plain absolute path; a query, fragment, userinfo, opaque form, relative or percent-encoded separator,
// "." or ".." component, or control character refuses it.
func (s Server) Segments(op, href string) ([]string, error) {
	unusable := s.Name + " answered with an unusable node location"
	trimmed := strings.TrimSpace(href)
	if trimmed == "" {
		return nil, provider.InvalidResponse(op, s.Name+" answered with a node without a location")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, provider.InvalidResponse(op, unusable)
	}
	if (parsed.Scheme != "" || parsed.Host != "") && parsed.Scheme+"://"+parsed.Host != s.Origin {
		return nil, provider.InvalidResponse(op, s.Name+" answered with a node of a different host")
	}
	escaped := parsed.EscapedPath()
	if !strings.HasPrefix(escaped, "/") {
		return nil, provider.InvalidResponse(op, unusable)
	}
	trimmedPath := strings.Trim(escaped, "/")
	if trimmedPath == "" {
		return nil, nil
	}
	raw := strings.Split(trimmedPath, "/")
	if len(raw) > maxSegments {
		return nil, provider.InvalidResponse(op, unusable)
	}
	segments := make([]string, 0, len(raw))
	for _, part := range raw {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || len(decoded) > maxSegmentLen ||
			!utf8.ValidString(decoded) {
			return nil, provider.InvalidResponse(op, unusable)
		}
		for _, r := range decoded {
			if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
				return nil, provider.InvalidResponse(op, unusable)
			}
		}
		segments = append(segments, decoded)
	}
	return segments, nil
}
