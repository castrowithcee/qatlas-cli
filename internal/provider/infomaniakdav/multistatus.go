package infomaniakdav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

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

	propPrincipal    = xml.Name{Space: davNS, Local: "current-user-principal"}
	propCalHome      = xml.Name{Space: caldavNS, Local: "calendar-home-set"}
	propBookHome     = xml.Name{Space: carddavN, Local: "addressbook-home-set"}
	propCalendarData = xml.Name{Space: caldavNS, Local: "calendar-data"}
	typeCalendar     = xml.Name{Space: caldavNS, Local: "calendar"}
	typeAddressbook  = xml.Name{Space: carddavN, Local: "addressbook"}
)

// Keys of the text properties.
const (
	keyName        = "displayname"
	keyDescription = "description"
	keyColor       = "color"
	keyETag        = "etag"
	keyCalendar    = "calendar-data"
)

var textProps = map[xml.Name]string{
	{Space: davNS, Local: "displayname"}:                keyName,
	{Space: caldavNS, Local: "calendar-description"}:    keyDescription,
	{Space: carddavN, Local: "addressbook-description"}: keyDescription,
	{Space: appleNS, Local: "calendar-color"}:           keyColor,
	{Space: davNS, Local: "getetag"}:                    keyETag,
	{Space: caldavNS, Local: "calendar-data"}:           keyCalendar,
}

// Bounds of one parsed answer.
const (
	maxResponseBytes = 1 << 20
	maxEntries       = 500
	maxXMLDepth      = 16
	maxTextBytes     = 4 << 10
	maxEventBytes    = 64 << 10
	maxLinks         = 8
	maxSegments      = 16
	maxSegmentLen    = 255
)

// resource is one d:response reduced to what this provider reads. Only successful propstat blocks
// contribute.
type resource struct {
	href   string
	status string
	text   map[string]string
	// links holds the hrefs nested in the principal and home set properties, by property.
	links map[xml.Name][]string
	// calendar and addressbook report the resource type; collection is not needed on its own.
	calendar, addressbook bool
	read                  bool
}

// failure reports why a resource carries no usable data.
func (r *resource) failure(op string) error {
	if code, ok := statusCodeOf(r.status); ok && (code < 200 || code >= 300) {
		return statusError(op, code)
	}
	if !r.read {
		return invalidResponse(op, "Infomaniak answered without readable properties for this node")
	}
	return nil
}

// parseMultiStatus reads a 207 answer with a bounded token walk. Depth, element count, and text length are
// capped, and a document that does not match the documented shape is refused before any value is used.
func parseMultiStatus(op string, body []byte) ([]resource, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true

	var (
		stack     []xml.Name
		resources []resource
		current   *resource
		pending   *resource
		status    string
		text      strings.Builder
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalidResponse(op, "Infomaniak returned an invalid multi-status document")
		}
		switch element := token.(type) {
		case xml.StartElement:
			if len(stack) >= maxXMLDepth {
				return nil, invalidResponse(op, "the Infomaniak response is nested too deeply")
			}
			stack = append(stack, element.Name)
			text.Reset()
			switch {
			case len(stack) == 1 && element.Name != elemMultistatus:
				return nil, invalidResponse(op, "Infomaniak did not answer with a multi-status document")
			case len(stack) == 2 && element.Name == elemResponse:
				if len(resources) >= maxEntries {
					return nil, invalidResponse(op, "Infomaniak reported more nodes than one read may handle")
				}
				current = &resource{text: map[string]string{}, links: map[xml.Name][]string{}}
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				pending = &resource{text: map[string]string{}, links: map[xml.Name][]string{}}
				status = ""
			case len(stack) == 6 && pending != nil && stack[4] == elemResourceType:
				switch element.Name {
				case typeCalendar:
					pending.calendar = true
				case typeAddressbook:
					pending.addressbook = true
				}
			}
		case xml.CharData:
			if len(stack) > 0 && stack[len(stack)-1] == propCalendarData {
				// An event is never cut: one beyond its cap refuses the answer.
				if text.Len()+len(element) > maxEventBytes {
					return nil, invalidResponse(op, "Infomaniak returned an event larger than one read may handle")
				}
				text.Write(element)
			} else if text.Len()+len(element) <= maxTextBytes {
				text.Write(element)
			}
		case xml.EndElement:
			switch {
			case len(stack) == 3 && current != nil && element.Name == elemHref:
				current.href = strings.TrimSpace(text.String())
			case len(stack) == 3 && current != nil && element.Name == elemStatus:
				current.status = text.String()
			case len(stack) == 4 && pending != nil && element.Name == elemStatus:
				status = text.String()
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				if code, ok := statusCodeOf(status); ok && code >= 200 && code < 300 {
					current.read = true
					current.calendar = current.calendar || pending.calendar
					current.addressbook = current.addressbook || pending.addressbook
					for key, value := range pending.text {
						current.text[key] = value
					}
					for key, values := range pending.links {
						current.links[key] = append(current.links[key], values...)
					}
				}
				pending = nil
			case len(stack) == 6 && pending != nil && element.Name == elemHref && stack[3] == elemProp:
				name := stack[4]
				if name == propPrincipal || name == propCalHome || name == propBookHome {
					if len(pending.links[name]) >= maxLinks {
						return nil, invalidResponse(op, "Infomaniak answered with too many locations")
					}
					pending.links[name] = append(pending.links[name], strings.TrimSpace(text.String()))
				}
			case len(stack) == 5 && pending != nil && stack[3] == elemProp:
				if key, ok := textProps[element.Name]; ok {
					pending.text[key] = strings.TrimSpace(text.String())
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

func statusCodeOf(line string) (int, bool) {
	for _, field := range strings.Fields(line) {
		if code, err := strconv.Atoi(field); err == nil {
			return code, true
		}
	}
	return 0, false
}

// segmentsOf turns an untrusted href into decoded path segments. The href must be on the fixed origin or
// a plain absolute path; a query, fragment, userinfo, opaque form, relative or percent-encoded separator,
// "." or ".." component, or control character refuses it.
func segmentsOf(op, href string) ([]string, error) {
	const unusable = "Infomaniak answered with an unusable node location"
	trimmed := strings.TrimSpace(href)
	if trimmed == "" {
		return nil, invalidResponse(op, "Infomaniak answered with a node without a location")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, invalidResponse(op, unusable)
	}
	if (parsed.Scheme != "" || parsed.Host != "") && parsed.Scheme+"://"+parsed.Host != origin {
		return nil, invalidResponse(op, "Infomaniak answered with a node of a different host")
	}
	escaped := parsed.EscapedPath()
	if !strings.HasPrefix(escaped, "/") {
		return nil, invalidResponse(op, unusable)
	}
	trimmedPath := strings.Trim(escaped, "/")
	if trimmedPath == "" {
		return nil, nil
	}
	raw := strings.Split(trimmedPath, "/")
	if len(raw) > maxSegments {
		return nil, invalidResponse(op, unusable)
	}
	segments := make([]string, 0, len(raw))
	for _, part := range raw {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || len(decoded) > maxSegmentLen ||
			!utf8.ValidString(decoded) {
			return nil, invalidResponse(op, unusable)
		}
		for _, r := range decoded {
			if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
				return nil, invalidResponse(op, unusable)
			}
		}
		segments = append(segments, decoded)
	}
	return segments, nil
}
