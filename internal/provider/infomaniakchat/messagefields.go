package infomaniakchat

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of the parts of a message that kChat lets users fill freely. A part beyond a bound is left out as a
// whole and named in the entry's omitted list; it is never cut, because a shortened object or list would
// look complete.
const (
	freeObjectDepth   = 6
	freeObjectEntries = 100
	freeObjectText    = 1024
	freeObjectBytes   = 16 << 10
	// metadataListMax bounds each list of a message's metadata; metadataImagesMax bounds its images map.
	metadataListMax   = 100
	metadataImagesMax = 50
)

// flexInt reads a whole number that the API documents as an integer but may send as a numeric string. Any
// other value reads as zero, which the entries leave out.
type flexInt int64

func (n *flexInt) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, `"`) {
		var quoted string
		if json.Unmarshal(data, &quoted) != nil {
			return nil
		}
		text = strings.TrimSpace(quoted)
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		*n = flexInt(value)
	}
	return nil
}

// tolerant reads a member of the expected type and ignores a value of any other type, so a deviation in one
// optional member never fails the whole answer.
type tolerant[T any] struct{ Value T }

func (t *tolerant[T]) UnmarshalJSON(data []byte) error {
	var value T
	if json.Unmarshal(data, &value) == nil {
		t.Value = value
	}
	return nil
}

// stripControls removes every control character except tab and line feed.
func stripControls(text string) string {
	if !strings.ContainsFunc(text, isStrippedControl) {
		return text
	}
	return strings.Map(func(r rune) rune {
		if isStrippedControl(r) {
			return -1
		}
		return r
	}, text)
}

func isStrippedControl(r rune) bool { return r != '\t' && r != '\n' && unicode.IsControl(r) }

// cleanText returns an untrusted text without control characters, cut to the text bound at a character
// boundary.
func cleanText(text string) string {
	text = stripControls(text)
	if len(text) <= freeObjectText {
		return text
	}
	end := freeObjectText
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// boundedFreeObject decodes one free object of a message, such as props or the data of an embed. within is
// false when the value is not an object or exceeds a bound; the caller then leaves it out and names it. A
// missing, null, or empty object is within the bounds and yields no object. Addresses inside are plain text.
func boundedFreeObject(raw json.RawMessage) (object map[string]any, within bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	top, isObject := value.(map[string]any)
	if !isObject {
		return nil, false
	}
	cleaned, ok := cleanFree(top, 1)
	if !ok {
		return nil, false
	}
	object = cleaned.(map[string]any)
	if len(object) == 0 {
		return nil, true
	}
	if encoded, err := json.Marshal(object); err != nil || len(encoded) > freeObjectBytes {
		return nil, false
	}
	return object, true
}

// cleanFree checks one decoded value against the bounds and returns it with control characters removed from
// its texts and keys. Null members of an object are dropped.
func cleanFree(value any, depth int) (any, bool) {
	switch v := value.(type) {
	case string:
		if len(v) > freeObjectText {
			return nil, false
		}
		return stripControls(v), true
	case []any:
		if depth > freeObjectDepth || len(v) > freeObjectEntries {
			return nil, false
		}
		out := make([]any, len(v))
		for i, item := range v {
			cleaned, ok := cleanFree(item, depth+1)
			if !ok {
				return nil, false
			}
			out[i] = cleaned
		}
		return out, true
	case map[string]any:
		if depth > freeObjectDepth || len(v) > freeObjectEntries {
			return nil, false
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			if len(key) > freeObjectText {
				return nil, false
			}
			if item == nil {
				continue
			}
			cleaned, ok := cleanFree(item, depth+1)
			if !ok {
				return nil, false
			}
			out[stripControls(key)] = cleaned
		}
		return out, true
	}
	return value, true
}

// MessageMetadata is the display information kChat attaches to a message.
type MessageMetadata struct {
	Embeds    []MetadataEmbed          `json:"embeds,omitempty"`
	Emojis    []MetadataEmoji          `json:"emojis,omitempty"`
	Files     []FileEntry              `json:"files,omitempty"`
	Images    map[string]MetadataImage `json:"images,omitempty"`
	Reactions []MetadataReaction       `json:"reactions,omitempty"`
}

// MetadataEmbed is one piece of content embedded in a message. Its address is untrusted text and never fetched.
type MetadataEmbed struct {
	Type string         `json:"type,omitempty"`
	URL  string         `json:"url,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

// MetadataEmoji is one custom emoji that appears in a message or its reactions.
type MetadataEmoji struct {
	ID        string `json:"id,omitempty"`
	CreatorID string `json:"creator_id,omitempty"`
	Name      string `json:"name,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	DeletedAt string `json:"deleted_at,omitempty"`
}

// MetadataReaction is one reaction made to a message.
type MetadataReaction struct {
	UserID    string `json:"user_id,omitempty"`
	PostID    string `json:"post_id,omitempty"`
	EmojiName string `json:"emoji_name,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// MetadataImage is the size kChat reports for one external image.
type MetadataImage struct {
	Width  int64 `json:"width,omitempty"`
	Height int64 `json:"height,omitempty"`
}

// metadataOf reads the metadata of a message. A list or object beyond a bound is left out and its path is
// appended to omitted.
func metadataOf(raw json.RawMessage, omitted *[]string) *MessageMetadata {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var parts struct {
		Embeds    json.RawMessage `json:"embeds"`
		Emojis    json.RawMessage `json:"emojis"`
		Files     json.RawMessage `json:"files"`
		Images    json.RawMessage `json:"images"`
		Reactions json.RawMessage `json:"reactions"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		*omitted = append(*omitted, "metadata")
		return nil
	}
	meta := &MessageMetadata{}
	if items, ok := metadataList(parts.Embeds, "metadata.embeds", omitted); ok {
		for i, item := range items {
			var embed struct {
				Type tolerant[string] `json:"type"`
				URL  tolerant[string] `json:"url"`
				Data json.RawMessage  `json:"data"`
			}
			if json.Unmarshal(item, &embed) != nil {
				continue
			}
			entry := MetadataEmbed{Type: cleanText(embed.Type.Value), URL: cleanText(embed.URL.Value)}
			data, within := boundedFreeObject(embed.Data)
			if !within {
				*omitted = append(*omitted, "metadata.embeds["+strconv.Itoa(i)+"].data")
			}
			entry.Data = data
			if entry.Type != "" || entry.URL != "" || entry.Data != nil {
				meta.Embeds = append(meta.Embeds, entry)
			}
		}
	}
	if items, ok := metadataList(parts.Emojis, "metadata.emojis", omitted); ok {
		for _, item := range items {
			var emoji struct {
				ID        tolerant[string] `json:"id"`
				CreatorID tolerant[string] `json:"creator_id"`
				Name      tolerant[string] `json:"name"`
				CreateAt  flexInt          `json:"create_at"`
				UpdateAt  flexInt          `json:"update_at"`
				DeleteAt  flexInt          `json:"delete_at"`
			}
			if json.Unmarshal(item, &emoji) != nil {
				continue
			}
			entry := MetadataEmoji{ID: cleanText(emoji.ID.Value), CreatorID: cleanText(emoji.CreatorID.Value),
				Name: cleanText(emoji.Name.Value), CreatedAt: msToRFC3339(int64(emoji.CreateAt)),
				UpdatedAt: msToRFC3339(int64(emoji.UpdateAt)), DeletedAt: msToRFC3339(int64(emoji.DeleteAt))}
			if entry != (MetadataEmoji{}) {
				meta.Emojis = append(meta.Emojis, entry)
			}
		}
	}
	if items, ok := metadataList(parts.Files, "metadata.files", omitted); ok {
		for _, item := range items {
			var file fileJSON
			if json.Unmarshal(item, &file) != nil || file.ID == "" || file.DeleteAt > 0 {
				continue
			}
			meta.Files = append(meta.Files, fileEntryOf(file))
		}
	}
	meta.Images = imagesOf(parts.Images, omitted)
	if items, ok := metadataList(parts.Reactions, "metadata.reactions", omitted); ok {
		for _, item := range items {
			var reaction struct {
				UserID    tolerant[string] `json:"user_id"`
				PostID    tolerant[string] `json:"post_id"`
				EmojiName tolerant[string] `json:"emoji_name"`
				CreateAt  flexInt          `json:"create_at"`
			}
			if json.Unmarshal(item, &reaction) != nil {
				continue
			}
			entry := MetadataReaction{UserID: cleanText(reaction.UserID.Value), PostID: cleanText(reaction.PostID.Value),
				EmojiName: cleanText(reaction.EmojiName.Value), CreatedAt: msToRFC3339(int64(reaction.CreateAt))}
			if entry != (MetadataReaction{}) {
				meta.Reactions = append(meta.Reactions, entry)
			}
		}
	}
	if len(meta.Embeds)+len(meta.Emojis)+len(meta.Files)+len(meta.Images)+len(meta.Reactions) == 0 {
		return nil
	}
	return meta
}

// metadataList splits one list of the metadata into its elements. ok is false for a missing list and for one
// that is not a list or exceeds the bound; the latter is named in omitted.
func metadataList(raw json.RawMessage, path string, omitted *[]string) (items []json.RawMessage, ok bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false
	}
	if json.Unmarshal(raw, &items) != nil || len(items) > metadataListMax {
		*omitted = append(*omitted, path)
		return nil, false
	}
	return items, true
}

// imagesOf reads the map from an external image address to its size. The addresses are untrusted text and
// are never fetched.
func imagesOf(raw json.RawMessage, omitted *[]string) map[string]MetadataImage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var sizes map[string]json.RawMessage
	if json.Unmarshal(raw, &sizes) != nil || len(sizes) > metadataImagesMax {
		*omitted = append(*omitted, "metadata.images")
		return nil
	}
	images := make(map[string]MetadataImage, len(sizes))
	for address, item := range sizes {
		if len(address) > freeObjectText {
			*omitted = append(*omitted, "metadata.images")
			return nil
		}
		var size struct {
			Width  flexInt `json:"width"`
			Height flexInt `json:"height"`
		}
		_ = json.Unmarshal(item, &size)
		images[stripControls(address)] = MetadataImage{Width: int64(size.Width), Height: int64(size.Height)}
	}
	if len(images) == 0 {
		return nil
	}
	return images
}
