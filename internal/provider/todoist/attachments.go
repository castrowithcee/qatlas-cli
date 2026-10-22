package todoist

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Bounds of the free members of a comment.
const (
	// maxAddressBytes bounds an attachment address; a longer one is not shown.
	maxAddressBytes = 2048
	// maxReactionEmoji, maxEmojiChars and maxReactionUsers bound the reactions of a comment. A comment above
	// one of them shows none and says so, because a cut set would misstate who reacted.
	maxReactionEmoji = 50
	maxEmojiChars    = 32
	maxReactionUsers = 100
	// maxNotified bounds the users a comment notified; the list is cut and says so.
	maxNotified = 100
)

// rawAttachment is the file of a comment. Todoist describes it as a free object, so the members added
// beyond the name and type are read leniently: one of an unexpected type is left out, never an error.
type rawAttachment struct {
	FileName     string          `json:"file_name"`
	FileType     string          `json:"file_type"`
	ResourceType string          `json:"resource_type"`
	FileSize     json.RawMessage `json:"file_size"`
	UploadState  json.RawMessage `json:"upload_state"`
	FileURL      json.RawMessage `json:"file_url"`
	Image        json.RawMessage `json:"image"`
	ImageWidth   json.RawMessage `json:"image_width"`
	ImageHeight  json.RawMessage `json:"image_height"`
	FileDuration json.RawMessage `json:"file_duration"`
	TnS          json.RawMessage `json:"tn_s"`
	TnM          json.RawMessage `json:"tn_m"`
	TnL          json.RawMessage `json:"tn_l"`
}

// Thumbnail is a preview of an image attachment. Its address is untrusted text under the attachment address
// rule and is never fetched.
type Thumbnail struct {
	URL    string `json:"url,omitempty"`
	Width  *int64 `json:"width,omitempty"`
	Height *int64 `json:"height,omitempty"`
}

func attachmentOf(raw *rawAttachment) *Attachment {
	return &Attachment{FileName: raw.FileName, FileType: raw.FileType, ResourceType: raw.ResourceType,
		FileSize: wholeNumber(raw.FileSize), UploadState: textMember(raw.UploadState, maxTagText),
		FileURL: addressMember(raw.FileURL), Image: addressMember(raw.Image),
		ImageWidth: wholeNumber(raw.ImageWidth), ImageHeight: wholeNumber(raw.ImageHeight),
		FileDuration: wholeNumber(raw.FileDuration),
		TnS:          thumbnailOf(raw.TnS), TnM: thumbnailOf(raw.TnM), TnL: thumbnailOf(raw.TnL)}
}

// wholeNumber reads a non-negative integer without a detour through a float; anything else is left out.
func wholeNumber(raw json.RawMessage) *int64 {
	if len(raw) == 0 {
		return nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}

func textMember(raw json.RawMessage, limit int) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return boundedText(value, limit)
}

func addressMember(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return shownAddress(value)
}

// thumbnailOf reads the list Todoist documents: address, width, and height. A missing or unusable address
// leaves the sizes in place.
func thumbnailOf(raw json.RawMessage) *Thumbnail {
	var list []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var thumbnail Thumbnail
	if len(list) > 0 {
		thumbnail.URL = addressMember(list[0])
	}
	if len(list) > 1 {
		thumbnail.Width = wholeNumber(list[1])
	}
	if len(list) > 2 {
		thumbnail.Height = wholeNumber(list[2])
	}
	if thumbnail == (Thumbnail{}) {
		return nil
	}
	return &thumbnail
}

// shownAddress returns an attachment address only when it is safe to show: an absolute http or https address
// without user information whose host is not todoist.com or one of its subdomains, because an address Todoist
// hosts counts as an access value. Anything unparsable, odd, or longer than maxAddressBytes yields "".
func shownAddress(value string) string {
	if value == "" || len(value) > maxAddressBytes || !utf8.ValidString(value) {
		return ""
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c <= ' ' || c == 0x7f || c == '\\' {
			return ""
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" {
		return ""
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return ""
	}
	if strings.Contains(parsed.Host, "@") || strings.Contains(parsed.Host, "%") {
		return ""
	}
	host := strings.ToLower(strings.TrimRight(parsed.Hostname(), "."))
	if host == "" {
		return ""
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == ':' || c == '[' || c == ']') {
			return ""
		}
	}
	if host == "todoist.com" || strings.HasSuffix(host, ".todoist.com") {
		return ""
	}
	return value
}

// notifiedOf bounds the users a comment notified.
func notifiedOf(raw []json.RawMessage) ([]string, bool) {
	var ids []string
	for _, entry := range raw {
		if id := idText(entry); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxNotified {
		return ids[:maxNotified], true
	}
	return ids, false
}

// reactionsOf maps emoji to the users who reacted. A set beyond a bound, or one that cleaning makes
// ambiguous, is not shown, and the second result says so.
func reactionsOf(raw map[string][]json.RawMessage) (map[string][]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	if len(raw) > maxReactionEmoji {
		return nil, true
	}
	out := make(map[string][]string, len(raw))
	for emoji, users := range raw {
		key := boundedText(emoji, 4*maxEmojiChars)
		if utf8.RuneCountInString(emoji) > maxEmojiChars || len(users) > maxReactionUsers || key == "" {
			return nil, true
		}
		if _, clash := out[key]; clash {
			return nil, true
		}
		ids := []string{}
		for _, user := range users {
			if id := idText(user); id != "" {
				ids = append(ids, id)
			}
		}
		out[key] = ids
	}
	return out, false
}
