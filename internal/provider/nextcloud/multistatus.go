package nextcloud

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// The two namespaces this adapter reads. DAV: carries the standard properties, and the ownCloud namespace
// carries the file identifier, the folder size, and the effective permissions Nextcloud adds to them.
const (
	davNS = "DAV:"
	ocNS  = "http://owncloud.org/ns"
	// ncNS carries the trash bin properties.
	ncNS = "http://nextcloud.org/ns"
)

// The elements of a multi-status answer this parser navigates by.
var (
	elemMultistatus  = xml.Name{Space: davNS, Local: "multistatus"}
	elemResponse     = xml.Name{Space: davNS, Local: "response"}
	elemHref         = xml.Name{Space: davNS, Local: "href"}
	elemPropstat     = xml.Name{Space: davNS, Local: "propstat"}
	elemProp         = xml.Name{Space: davNS, Local: "prop"}
	elemStatus       = xml.Name{Space: davNS, Local: "status"}
	elemResourceType = xml.Name{Space: davNS, Local: "resourcetype"}
	elemCollection   = xml.Name{Space: davNS, Local: "collection"}
)

// The keys the fixed property set is normalised under.
const (
	propDisplayName   = "displayname"
	propContentType   = "contenttype"
	propContentLength = "contentlength"
	propLastModified  = "lastmodified"
	propETag          = "etag"
	propFileID        = "fileid"
	propSize          = "size"
	propPermissions   = "permissions"
	propTrashName     = "trashname"
	propTrashOrigin   = "trashorigin"
	propTrashDeleted  = "trashdeleted"
	propTagID         = "tagid"
	propTagName       = "tagname"
	propTagVisible    = "tagvisible"
	propTagAssignable = "tagassignable"
	propTagCanAssign  = "tagcanassign"

	propCommentVerb      = "commentverb"
	propCommentActorType = "commentactortype"
	propCommentActorID   = "commentactorid"
	propCommentActorName = "commentactorname"
	propCommentCreated   = "commentcreated"
	propCommentMessage   = "commentmessage"
	propCommentObjType   = "commentobjecttype"
	propCommentObjID     = "commentobjectid"
)

// wantedProps maps the requested properties to their keys. Every other property of an answer is dropped,
// so a server cannot widen the result by returning more than it was asked for.
var wantedProps = map[xml.Name]string{
	{Space: davNS, Local: "displayname"}:               propDisplayName,
	{Space: davNS, Local: "getcontenttype"}:            propContentType,
	{Space: davNS, Local: "getcontentlength"}:          propContentLength,
	{Space: davNS, Local: "getlastmodified"}:           propLastModified,
	{Space: davNS, Local: "getetag"}:                   propETag,
	{Space: ocNS, Local: "fileid"}:                     propFileID,
	{Space: ocNS, Local: "size"}:                       propSize,
	{Space: ocNS, Local: "permissions"}:                propPermissions,
	{Space: ocNS, Local: "id"}:                         propTagID,
	{Space: ocNS, Local: "display-name"}:               propTagName,
	{Space: ocNS, Local: "user-visible"}:               propTagVisible,
	{Space: ocNS, Local: "user-assignable"}:            propTagAssignable,
	{Space: ocNS, Local: "can-assign"}:                 propTagCanAssign,
	{Space: ocNS, Local: "verb"}:                       propCommentVerb,
	{Space: ocNS, Local: "actorType"}:                  propCommentActorType,
	{Space: ocNS, Local: "actorId"}:                    propCommentActorID,
	{Space: ocNS, Local: "actorDisplayName"}:           propCommentActorName,
	{Space: ocNS, Local: "creationDateTime"}:           propCommentCreated,
	{Space: ocNS, Local: "message"}:                    propCommentMessage,
	{Space: ocNS, Local: "objectType"}:                 propCommentObjType,
	{Space: ocNS, Local: "objectId"}:                   propCommentObjID,
	{Space: davNS, Local: "creationdate"}:              propCreationDate,
	{Space: davNS, Local: "getcontentlanguage"}:        propContentLanguage,
	{Space: davNS, Local: "quota-used-bytes"}:          propQuotaUsed,
	{Space: davNS, Local: "quota-available-bytes"}:     propQuotaAvailable,
	{Space: ocNS, Local: "favorite"}:                   propFavorite,
	{Space: ocsNS, Local: "share-permissions"}:         propSharePerms,
	{Space: ocmNS, Local: "share-permissions"}:         propOCMPerms,
	{Space: ocNS, Local: "owner-id"}:                   propOwnerID,
	{Space: ocNS, Local: "owner-display-name"}:         propOwnerName,
	{Space: ocNS, Local: "comments-count"}:             propCommentsCount,
	{Space: ocNS, Local: "comments-unread"}:            propCommentsUnread,
	{Space: ocNS, Local: "comments-href"}:              propCommentsHref,
	{Space: ocNS, Local: "data-fingerprint"}:           propFingerprint,
	{Space: ncNS, Local: "data-fingerprint"}:           propFingerprint,
	{Space: ncNS, Local: "creation_time"}:              propCreationTime,
	{Space: ncNS, Local: "upload_time"}:                propUploadTime,
	{Space: ncNS, Local: "last_activity"}:              propLastActivity,
	{Space: ncNS, Local: "note"}:                       propNote,
	{Space: ncNS, Local: "share-attributes"}:           propShareAttrs,
	{Space: ncNS, Local: "hide-download"}:              propHideDownload,
	{Space: ncNS, Local: "mount-type"}:                 propMountType,
	{Space: ncNS, Local: "is-mount-root"}:              propIsMountRoot,
	{Space: ncNS, Local: "is-federated"}:               propIsFederated,
	{Space: ncNS, Local: "is-encrypted"}:               propIsEncrypted,
	{Space: ncNS, Local: "has-preview"}:                propHasPreview,
	{Space: ncNS, Local: "hidden"}:                     propHidden,
	{Space: ncNS, Local: "metadata_etag"}:              propMetadataETag,
	{Space: ncNS, Local: "contained-file-count"}:       propContainedFiles,
	{Space: ncNS, Local: "contained-folder-count"}:     propContainedDirs,
	{Space: ncNS, Local: "trashbin-filename"}:          propTrashName,
	{Space: ncNS, Local: "trashbin-original-location"}: propTrashOrigin,
	{Space: ncNS, Local: "trashbin-deletion-time"}:     propTrashDeleted,
}

// resource is one d:response of a multi-status answer, reduced to what this adapter reads: where the node
// is, whether it is a collection, the properties a successful propstat reported, and the resource-level
// status a server uses to refuse one node of many.
type resource struct {
	href   string
	status string
	props  map[string]string
	// trees holds the nested properties and over those a bound dropped; see treeProps.
	trees      map[string]*propNode
	over       map[string]bool
	collection bool
	// read records that at least one propstat of this resource answered with a 2xx status.
	read bool
}

// failure reports why this resource carries no usable metadata. A multi-status answer mixes successful
// and refused nodes, so every node is checked before any of its data is used.
func (r *resource) failure(op string) error {
	if code, ok := statusCodeOf(r.status); ok && (code < 200 || code >= 300) {
		return statusError(op, code)
	}
	if !r.read {
		return invalidResponse(op, "Nextcloud answered without readable properties for this node")
	}
	return nil
}

// parseMultiStatus reads a 207 answer with a bounded token walk rather than a recursive unmarshal, so a
// hostile document can exhaust neither the stack nor the memory of this process. Depth, element count,
// and text length are all capped, and anything that does not match the documented shape is refused before
// a single value is handed on.
func parseMultiStatus(op string, body []byte) ([]resource, error) {
	return parseMultiStatusMax(op, body, maxEntries)
}

// parseMultiStatusMax is parseMultiStatus with the number of nodes it accepts; the trash bin lists
// entries of the whole account before they are filtered to the root folder.
func parseMultiStatusMax(op string, body []byte, limit int) ([]resource, error) {
	return parseMultiStatusBounded(op, body, limit, false)
}

// parseMultiStatusBounded is parseMultiStatusMax that may also accept an answer without a single node,
// which is the correct answer of a search without a hit.
func parseMultiStatusBounded(op string, body []byte, limit int, allowEmpty bool) ([]resource, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true

	var (
		stack     []xml.Name
		resources []resource
		current   *resource
		pending   map[string]string
		pendTrees map[string]*propNode
		pendOver  map[string]bool
		capture   *treeCapture
		collected bool
		status    string
		text      strings.Builder
	)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalidResponse(op, "Nextcloud returned an invalid multi-status document")
		}

		switch element := token.(type) {
		case xml.StartElement:
			if len(stack) >= maxXMLDepth {
				return nil, invalidResponse(op, "the Nextcloud response is nested too deeply")
			}
			stack = append(stack, element.Name)
			text.Reset()
			switch {
			case len(stack) == 1 && element.Name != elemMultistatus:
				return nil, invalidResponse(op, "Nextcloud did not answer with a multi-status document")
			case len(stack) == 2 && element.Name == elemResponse:
				if len(resources) >= limit {
					return nil, invalidResponse(op,
						"this Nextcloud folder holds more entries than one read may report")
				}
				current = &resource{props: map[string]string{}, trees: map[string]*propNode{}, over: map[string]bool{}}
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				pending, collected, status = map[string]string{}, false, ""
				pendTrees, pendOver = map[string]*propNode{}, map[string]bool{}
			case len(stack) == 6 && pending != nil && stack[4] == elemResourceType &&
				element.Name == elemCollection:
				collected = true
			}
			if capture == nil && len(stack) == 5 && pending != nil && stack[3] == elemProp {
				if key, ok := treeProps[element.Name]; ok {
					capture = &treeCapture{key: key}
				}
			}
			if capture != nil {
				capture.start(element.Name)
			}

		case xml.CharData:
			if capture != nil {
				capture.text(element)
			}
			if text.Len()+len(element) <= maxTextBytes {
				text.Write(element)
			}

		case xml.EndElement:
			switch {
			case len(stack) == 3 && current != nil && element.Name == elemHref:
				current.href = text.String()
			case len(stack) == 3 && current != nil && element.Name == elemStatus:
				current.status = text.String()
			case len(stack) == 3 && current != nil && element.Name == elemPropstat:
				// Properties become part of the resource only when their own propstat succeeded, so a
				// 404 block of a partially answered node cannot contribute a value.
				if code, ok := statusCodeOf(status); ok && code >= 200 && code < 300 {
					current.read = true
					current.collection = current.collection || collected
					for key, value := range pending {
						current.props[key] = value
					}
					for key, node := range pendTrees {
						current.trees[key] = node
					}
					for key := range pendOver {
						current.over[key] = true
					}
				}
				pending, pendTrees, pendOver = nil, nil, nil
			case len(stack) == 4 && pending != nil && element.Name == elemStatus:
				status = text.String()
			case len(stack) == 5 && pending != nil && stack[3] == elemProp:
				if key, ok := wantedProps[element.Name]; ok {
					pending[key] = strings.TrimSpace(text.String())
					if extra, ok := extraProps[element.Name]; ok {
						pending[extra] = pending[key]
					}
				}
			case len(stack) == 2 && current != nil && element.Name == elemResponse:
				resources = append(resources, *current)
				current = nil
			}
			if capture != nil {
				capture.path = capture.path[:len(capture.path)-1]
				if len(capture.path) == 0 {
					if capture.over {
						pendOver[capture.key] = true
					} else if pending != nil {
						pendTrees[capture.key] = capture.root
					}
					capture = nil
				}
			}
			text.Reset()
			stack = stack[:len(stack)-1]
		}
	}

	if len(resources) == 0 && !allowEmpty {
		return nil, invalidResponse(op, "Nextcloud answered without a single node")
	}
	return resources, nil
}

// statusCodeOf reads the numeric code out of a WebDAV status line such as "HTTP/1.1 200 OK".
func statusCodeOf(line string) (int, bool) {
	for _, field := range strings.Fields(line) {
		if code, err := strconv.Atoi(field); err == nil {
			return code, true
		}
	}
	return 0, false
}

// relativeOf turns the location of one answered node into its path below the connection root. The href is
// untrusted: it is compared segment by segment against the origin, the installation path, the identity,
// and the fixed root of this connection, so a server cannot hand back a node outside them.
func (c *Client) relativeOf(op, href string) ([]string, error) {
	return c.segmentsBelow(op, href, append(append([]string{}, c.prefix...), c.root...))
}

// segmentsBelow is relativeOf for any fixed bound: it returns the segments of href below bound.
func (c *Client) segmentsBelow(op, href string, bound []string) ([]string, error) {
	trimmed := strings.TrimSpace(href)
	if trimmed == "" {
		return nil, invalidResponse(op, "Nextcloud answered with a node without a location")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, invalidResponse(op, "Nextcloud answered with an unusable node location")
	}
	if (parsed.Scheme != "" || parsed.Host != "") && parsed.Scheme+"://"+parsed.Host != c.origin {
		return nil, invalidResponse(op, "Nextcloud answered with a node of a different instance")
	}

	escaped := parsed.EscapedPath()
	if !strings.HasPrefix(escaped, "/") {
		return nil, invalidResponse(op, "Nextcloud answered with a relative node location")
	}
	// The escaped form is split before it is decoded, so a percent-encoded separator inside a name stays
	// inside that one segment instead of silently becoming a new path component.
	raw := strings.Split(strings.TrimSuffix(escaped, "/"), "/")[1:]
	if len(raw) > len(bound)+maxSegments {
		return nil, invalidResponse(op, "Nextcloud answered with a node location that is too deep")
	}

	segments := make([]string, 0, len(raw))
	for _, part := range raw {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, invalidResponse(op, "Nextcloud answered with an unusable node location")
		}
		if err := checkResponseSegment(decoded); err != nil {
			return nil, invalidResponse(op, "Nextcloud answered with an unusable node location")
		}
		segments = append(segments, decoded)
	}

	if len(segments) < len(bound) || !equalSegments(segments[:len(bound)], bound) {
		return nil, invalidResponse(op, messageForeignEntry)
	}
	return segments[len(bound):], nil
}

// checkResponseSegment applies the path rules to provider data as well. A decoded separator, a relative
// component, or a control character in an href would move a node out of the connection root.
func checkResponseSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." || len(segment) > maxSegmentLen {
		return errors.New("unusable path component")
	}
	for _, r := range segment {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return errors.New("unusable path component")
		}
	}
	return nil
}
