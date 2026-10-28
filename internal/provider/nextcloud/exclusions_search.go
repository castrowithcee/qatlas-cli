package nextcloud

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// searchRefs collects the identifiers a search hit names: the file path or ID the provider reports, and
// whatever its address carries. A hit that names none of them is not attributable and stays.
func searchRefs(resourceURL string, rawAttributes json.RawMessage) feedRefs {
	var r feedRefs
	var members map[string]json.RawMessage
	_ = json.Unmarshal(rawAttributes, &members)
	text := func(key string) string {
		var v flexString
		if json.Unmarshal(members[key], &v) != nil {
			return ""
		}
		return strings.TrimSpace(string(v))
	}
	path, fileID := text("path"), text("fileId")
	if fileID == "" {
		fileID = text("fileid")
	}
	if token := text("conversation"); token != "" {
		r.tokens = append(r.tokens, token)
	}
	r.addLink(resourceURL)
	if parsed, err := url.Parse(strings.TrimSpace(resourceURL)); err == nil {
		if id := parsed.Query().Get("openfile"); fileID == "" && allDigits(id) {
			fileID = id
		}
		r.addDavReferences(parsed)
	}
	if path != "" {
		// The exact path of the file makes its ID needless.
		r.addPath(path)
		r.fileIDs = nil
	} else if fileID != "" {
		r.fileIDs = append(r.fileIDs, fileID)
	}
	return r
}

// addDavReferences reads the two address forms of the calendar and contacts apps that addLink does not:
// a calendar object as an encoded DAV address, and a contact as UID~ADDRESSBOOK.
func (r *feedRefs) addDavReferences(u *url.URL) {
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	for _, segment := range segments {
		if len(segment) >= 16 {
			for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
				if decoded, err := enc.DecodeString(segment); err == nil {
					r.addLink(string(decoded))
					break
				}
			}
		}
	}
	if len(segments) > 0 {
		last := segments[len(segments)-1]
		if i := strings.LastIndex(last, "~"); i >= 0 && i+1 < len(last) && strings.Contains(u.Path, "/contacts/") {
			r.addressbooks = append(r.addressbooks, last[i+1:])
		}
	}
}
