package nextcloud

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Bounds of the members a rich object reports beyond its key, type, ID, and name. Members over a bound are
// left out as a whole and named in omitted_fields, never cut.
const (
	maxRichDetails  = 20
	maxRichName     = 64
	fieldRichDetail = "details"
)

// richAddressMembers are the members of a rich object that hold an address. They are reported as bounded
// text and never fetched.
var richAddressMembers = map[string]bool{"link": true, "server": true, "thumb": true, "icon_url": true, "website": true}

// richObjectOf maps one rich object, a member of the message parameters, to its view. The members type, id,
// and name stay as they are; every further member goes to details. The ID is reported for people only,
// because the ID of a call or conversation is its token. Notifications and activities use the same mapping.
func richObjectOf(key string, members map[string]json.RawMessage) MessageObject {
	var object struct {
		Type string
		ID   flexString
		Name string
	}
	_ = json.Unmarshal(members["type"], &object.Type)
	_ = json.Unmarshal(members["id"], &object.ID)
	_ = json.Unmarshal(members["name"], &object.Name)
	name, _ := cut(object.Name, maxValueLength)
	view := MessageObject{Key: bounded(key), Type: bounded(object.Type), Name: name}
	switch object.Type {
	case "user", "group", "guest":
		view.ID = bounded(string(object.ID))
	}
	details, omitted := richDetails(members)
	view.Details = details
	if omitted {
		view.OmittedFields = []string{fieldRichDetail}
	}
	return view
}

// richDetails reports the members of a rich object other than type, id, and name under snake_case names, as
// text or whole number. Access values never appear: members that hold a token or a password, and an address
// that leads to a public share or a conversation by its token. It reports omitted when more members exist or a
// value is longer than the bounds allow; details are then absent.
func richDetails(members map[string]json.RawMessage) (map[string]any, bool) {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	details := map[string]any{}
	for _, key := range keys {
		name, ok := richMemberName(key)
		if !ok || name == "type" || name == "id" || name == "name" || richAccessName(name) {
			continue
		}
		if _, seen := details[name]; seen {
			continue
		}
		value, kind := richMemberValue(members[key])
		switch kind {
		case richText:
			text := value.(string)
			if len(text) > maxValueLength {
				return nil, true
			}
			text = cleanText(text, maxValueLength)
			if text == "" || richAddressMembers[name] && richAccessAddress(text) {
				continue
			}
			value = text
		case richNumber:
		default:
			continue
		}
		if len(details) == maxRichDetails {
			return nil, true
		}
		details[name] = value
	}
	if len(details) == 0 {
		return nil, false
	}
	return details, false
}

const (
	richSkip = iota
	richText
	richNumber
)

func richMemberValue(raw json.RawMessage) (any, int) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, richSkip
		}
		return value, richText
	}
	if number, err := strconv.ParseInt(text, 10, 64); err == nil {
		return number, richNumber
	}
	return nil, richSkip
}

// richMemberName turns a member name such as preview-available into snake_case. A name outside letters,
// digits, hyphen, and underscore is not reported.
func richMemberName(key string) (string, bool) {
	if key == "" || len(key) > maxRichName {
		return "", false
	}
	var b strings.Builder
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '-':
			b.WriteByte('_')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 'a' - 'A')
		default:
			return "", false
		}
	}
	return b.String(), true
}

func richAccessName(name string) bool {
	return name == "password" || strings.Contains(name, "token")
}

// richAccessAddress reports whether an address leads to a public share or to a conversation by its token.
func richAccessAddress(address string) bool {
	path := address
	if parsed, err := url.Parse(address); err == nil {
		path = parsed.Path + "?" + parsed.RawQuery + "#" + parsed.Fragment
	}
	return strings.Contains(path, "/s/") || strings.Contains(path, "/call/")
}
