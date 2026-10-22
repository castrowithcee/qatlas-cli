package telegram

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

// This file is the only mapping of the Bot API media types. Each type is decoded with the members the Bot API
// documents and nothing else, and printed with the same names, except that every file_id becomes a file_ref
// and file_path never appears. Telegram sends the shift and scale of a MaskPosition as JSON numbers; float64
// reads and prints the shortest text that parses back to the same value, so they pass through unrounded.

const (
	// A nested object is dropped, not shortened, when it exceeds one of these bounds.
	maxNestedDepth = 8
	maxNestedList  = 100
	maxNestedBytes = 16 << 10
)

// blockedFields are Telegram members that never reach an output, whatever type carries them.
var blockedFields = map[string]bool{"invite_link": true, "passport_data": true, "file_path": true}

// isBlockedField reports whether a Telegram member name is on the block list.
func isBlockedField(name string) bool { return blockedFields[name] }

// cleanText removes control characters of an untrusted text and bounds it.
func cleanText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, value)
	}
	return capText(value, limit)
}

// withinLimits reports whether a nested object stays inside the depth, list, and size bounds once serialized.
func withinLimits(v any) bool {
	encoded, err := json.Marshal(v)
	if err != nil || len(encoded) > maxNestedBytes {
		return false
	}
	type frame struct {
		array bool
		n     int
	}
	var stack []frame
	count := func() bool {
		if top := len(stack) - 1; top >= 0 && stack[top].array {
			stack[top].n++
			return stack[top].n <= maxNestedList
		}
		return true
	}
	dec := json.NewDecoder(strings.NewReader(string(encoded)))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				if !count() {
					return false
				}
				stack = append(stack, frame{array: d == '['})
				if len(stack) > maxNestedDepth {
					return false
				}
			} else {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !count() {
			return false
		}
	}
}

// bounded returns v, or its zero value after naming it in omitted when it exceeds the limits.
func bounded[T any](omitted *[]string, name string, v T) T {
	if withinLimits(v) {
		return v
	}
	*omitted = append(*omitted, name)
	var zero T
	return zero
}

// fileRefFunc turns a file_id into the signed reference of the target the answer came from. A nil function
// signs nothing: the files of a business message are listed but not addressable.
type fileRefFunc func(fileID string) string

func (r fileRefFunc) ref(fileID string) string {
	if r == nil || fileID == "" {
		return ""
	}
	return r(fileID)
}

// pair turns the small_file_id and big_file_id of a ChatPhoto into small_file_ref and big_file_ref.
func (r fileRefFunc) pair(small, big string) (smallRef, bigRef string) {
	return r.ref(small), r.ref(big)
}

// fileRefs signs file identifiers for the given binding with this client's token.
func (c *Client) fileRefs(binding string) fileRefFunc {
	return func(fileID string) string { return signRef(c.token, refFile, binding, fileID) }
}

// decodeFileID decodes b into v, a defined type without methods, and reads file_id apart so it never has an
// exported field that could print.
func decodeFileID(b []byte, v any, id *string) error {
	var head struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	*id = head.FileID
	return json.Unmarshal(b, v)
}

type photoSize struct {
	Kind         string `json:"kind,omitempty"`
	FileRef      string `json:"file_ref,omitempty"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Width        int64  `json:"width,omitempty"`
	Height       int64  `json:"height,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	fileID       string
}

func (p *photoSize) UnmarshalJSON(b []byte) error {
	type plain photoSize
	return decodeFileID(b, (*plain)(p), &p.fileID)
}

// file is the Bot API File of a premium sticker animation; file_path is not decoded.
type file struct {
	FileRef      string `json:"file_ref,omitempty"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	fileID       string
}

func (f *file) UnmarshalJSON(b []byte) error {
	type plain file
	return decodeFileID(b, (*plain)(f), &f.fileID)
}

type animation struct {
	Kind         string     `json:"kind"`
	FileRef      string     `json:"file_ref,omitempty"`
	FileUniqueID string     `json:"file_unique_id,omitempty"`
	Width        int64      `json:"width,omitempty"`
	Height       int64      `json:"height,omitempty"`
	Duration     int64      `json:"duration,omitempty"`
	Thumbnail    *photoSize `json:"thumbnail,omitempty"`
	FileName     string     `json:"file_name,omitempty"`
	MimeType     string     `json:"mime_type,omitempty"`
	FileSize     int64      `json:"file_size,omitempty"`
	fileID       string
}

func (a *animation) UnmarshalJSON(b []byte) error {
	type plain animation
	return decodeFileID(b, (*plain)(a), &a.fileID)
}

type audio struct {
	Kind         string     `json:"kind"`
	FileRef      string     `json:"file_ref,omitempty"`
	FileUniqueID string     `json:"file_unique_id,omitempty"`
	Duration     int64      `json:"duration,omitempty"`
	Performer    string     `json:"performer,omitempty"`
	Title        string     `json:"title,omitempty"`
	FileName     string     `json:"file_name,omitempty"`
	MimeType     string     `json:"mime_type,omitempty"`
	FileSize     int64      `json:"file_size,omitempty"`
	Thumbnail    *photoSize `json:"thumbnail,omitempty"`
	fileID       string
}

func (a *audio) UnmarshalJSON(b []byte) error {
	type plain audio
	return decodeFileID(b, (*plain)(a), &a.fileID)
}

type document struct {
	Kind         string     `json:"kind"`
	FileRef      string     `json:"file_ref,omitempty"`
	FileUniqueID string     `json:"file_unique_id,omitempty"`
	Thumbnail    *photoSize `json:"thumbnail,omitempty"`
	FileName     string     `json:"file_name,omitempty"`
	MimeType     string     `json:"mime_type,omitempty"`
	FileSize     int64      `json:"file_size,omitempty"`
	fileID       string
}

func (d *document) UnmarshalJSON(b []byte) error {
	type plain document
	return decodeFileID(b, (*plain)(d), &d.fileID)
}

type videoQuality struct {
	FileRef      string `json:"file_ref,omitempty"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Width        int64  `json:"width,omitempty"`
	Height       int64  `json:"height,omitempty"`
	Codec        string `json:"codec,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	fileID       string
}

func (q *videoQuality) UnmarshalJSON(b []byte) error {
	type plain videoQuality
	return decodeFileID(b, (*plain)(q), &q.fileID)
}

type video struct {
	Kind           string         `json:"kind"`
	FileRef        string         `json:"file_ref,omitempty"`
	FileUniqueID   string         `json:"file_unique_id,omitempty"`
	Width          int64          `json:"width,omitempty"`
	Height         int64          `json:"height,omitempty"`
	Duration       int64          `json:"duration,omitempty"`
	Thumbnail      *photoSize     `json:"thumbnail,omitempty"`
	Cover          []photoSize    `json:"cover,omitempty"`
	StartTimestamp int64          `json:"start_timestamp,omitempty"`
	Qualities      []videoQuality `json:"qualities,omitempty"`
	FileName       string         `json:"file_name,omitempty"`
	MimeType       string         `json:"mime_type,omitempty"`
	FileSize       int64          `json:"file_size,omitempty"`
	fileID         string
}

func (v *video) UnmarshalJSON(b []byte) error {
	type plain video
	return decodeFileID(b, (*plain)(v), &v.fileID)
}

type videoNote struct {
	Kind         string     `json:"kind"`
	FileRef      string     `json:"file_ref,omitempty"`
	FileUniqueID string     `json:"file_unique_id,omitempty"`
	Length       int64      `json:"length,omitempty"`
	Duration     int64      `json:"duration,omitempty"`
	Thumbnail    *photoSize `json:"thumbnail,omitempty"`
	FileSize     int64      `json:"file_size,omitempty"`
	fileID       string
}

func (v *videoNote) UnmarshalJSON(b []byte) error {
	type plain videoNote
	return decodeFileID(b, (*plain)(v), &v.fileID)
}

type voice struct {
	Kind         string `json:"kind"`
	FileRef      string `json:"file_ref,omitempty"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int64  `json:"duration,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	fileID       string
}

func (v *voice) UnmarshalJSON(b []byte) error {
	type plain voice
	return decodeFileID(b, (*plain)(v), &v.fileID)
}

// maskPositionOut keeps zero shifts: they are values, not absences.
type maskPositionOut struct {
	Point  string   `json:"point,omitempty"`
	XShift *float64 `json:"x_shift,omitempty"`
	YShift *float64 `json:"y_shift,omitempty"`
	Scale  *float64 `json:"scale,omitempty"`
}

type sticker struct {
	Kind             string           `json:"kind"`
	FileRef          string           `json:"file_ref,omitempty"`
	FileUniqueID     string           `json:"file_unique_id,omitempty"`
	Type             string           `json:"type,omitempty"`
	Width            int64            `json:"width,omitempty"`
	Height           int64            `json:"height,omitempty"`
	IsAnimated       *bool            `json:"is_animated,omitempty"`
	IsVideo          *bool            `json:"is_video,omitempty"`
	Thumbnail        *photoSize       `json:"thumbnail,omitempty"`
	Emoji            string           `json:"emoji,omitempty"`
	SetName          string           `json:"set_name,omitempty"`
	PremiumAnimation *file            `json:"premium_animation,omitempty"`
	MaskPosition     *maskPositionOut `json:"mask_position,omitempty"`
	CustomEmojiID    string           `json:"custom_emoji_id,omitempty"`
	NeedsRepainting  *bool            `json:"needs_repainting,omitempty"`
	FileSize         int64            `json:"file_size,omitempty"`
	fileID           string
}

func (s *sticker) UnmarshalJSON(b []byte) error {
	type plain sticker
	return decodeFileID(b, (*plain)(s), &s.fileID)
}

type livePhoto struct {
	Kind         string      `json:"kind"`
	Photo        []photoSize `json:"photo,omitempty"`
	FileRef      string      `json:"file_ref,omitempty"`
	FileUniqueID string      `json:"file_unique_id,omitempty"`
	Width        int64       `json:"width,omitempty"`
	Height       int64       `json:"height,omitempty"`
	Duration     int64       `json:"duration,omitempty"`
	MimeType     string      `json:"mime_type,omitempty"`
	FileSize     int64       `json:"file_size,omitempty"`
	fileID       string
}

func (l *livePhoto) UnmarshalJSON(b []byte) error {
	type plain livePhoto
	return decodeFileID(b, (*plain)(l), &l.fileID)
}

// The functions below return a cleaned copy with references in place of identifiers; the decoded value is not
// changed, because the message is read again for its media summary.

func (r fileRefFunc) photoSize(p photoSize, kind string) photoSize {
	return photoSize{Kind: kind, FileRef: r.ref(p.fileID), FileUniqueID: cleanText(p.FileUniqueID, maxNameRunes),
		Width: p.Width, Height: p.Height, FileSize: p.FileSize}
}

func (r fileRefFunc) thumbnail(p *photoSize) *photoSize {
	if p == nil {
		return nil
	}
	out := r.photoSize(*p, "")
	return &out
}

func (r fileRefFunc) photoSizes(list []photoSize) []photoSize {
	var out []photoSize
	for _, p := range list {
		out = append(out, r.photoSize(p, ""))
	}
	return out
}

func (r fileRefFunc) animation(a animation, omitted *[]string) animation {
	return animation{Kind: "animation", FileRef: r.ref(a.fileID), FileUniqueID: cleanText(a.FileUniqueID, maxNameRunes),
		Width: a.Width, Height: a.Height, Duration: a.Duration,
		Thumbnail: bounded(omitted, "media_items.animation.thumbnail", r.thumbnail(a.Thumbnail)),
		FileName:  cleanText(a.FileName, maxNameRunes), MimeType: cleanText(a.MimeType, maxNameRunes), FileSize: a.FileSize}
}

func (r fileRefFunc) audio(a audio, omitted *[]string) audio {
	return audio{Kind: "audio", FileRef: r.ref(a.fileID), FileUniqueID: cleanText(a.FileUniqueID, maxNameRunes),
		Duration: a.Duration, Performer: cleanText(a.Performer, maxNameRunes), Title: cleanText(a.Title, maxNameRunes),
		FileName: cleanText(a.FileName, maxNameRunes), MimeType: cleanText(a.MimeType, maxNameRunes), FileSize: a.FileSize,
		Thumbnail: bounded(omitted, "media_items.audio.thumbnail", r.thumbnail(a.Thumbnail))}
}

func (r fileRefFunc) document(d document, omitted *[]string) document {
	return document{Kind: "document", FileRef: r.ref(d.fileID), FileUniqueID: cleanText(d.FileUniqueID, maxNameRunes),
		Thumbnail: bounded(omitted, "media_items.document.thumbnail", r.thumbnail(d.Thumbnail)),
		FileName:  cleanText(d.FileName, maxNameRunes), MimeType: cleanText(d.MimeType, maxNameRunes), FileSize: d.FileSize}
}

func (r fileRefFunc) video(v video, omitted *[]string) video {
	var qualities []videoQuality
	for _, q := range v.Qualities {
		qualities = append(qualities, videoQuality{FileRef: r.ref(q.fileID),
			FileUniqueID: cleanText(q.FileUniqueID, maxNameRunes), Width: q.Width, Height: q.Height,
			Codec: cleanText(q.Codec, maxNameRunes), FileSize: q.FileSize})
	}
	return video{Kind: "video", FileRef: r.ref(v.fileID), FileUniqueID: cleanText(v.FileUniqueID, maxNameRunes),
		Width: v.Width, Height: v.Height, Duration: v.Duration,
		Thumbnail:      bounded(omitted, "media_items.video.thumbnail", r.thumbnail(v.Thumbnail)),
		Cover:          bounded(omitted, "media_items.video.cover", r.photoSizes(v.Cover)),
		StartTimestamp: v.StartTimestamp,
		Qualities:      bounded(omitted, "media_items.video.qualities", qualities),
		FileName:       cleanText(v.FileName, maxNameRunes), MimeType: cleanText(v.MimeType, maxNameRunes), FileSize: v.FileSize}
}

func (r fileRefFunc) videoNote(v videoNote, omitted *[]string) videoNote {
	return videoNote{Kind: "video_note", FileRef: r.ref(v.fileID), FileUniqueID: cleanText(v.FileUniqueID, maxNameRunes),
		Length: v.Length, Duration: v.Duration,
		Thumbnail: bounded(omitted, "media_items.video_note.thumbnail", r.thumbnail(v.Thumbnail)), FileSize: v.FileSize}
}

func (r fileRefFunc) voice(v voice) voice {
	return voice{Kind: "voice", FileRef: r.ref(v.fileID), FileUniqueID: cleanText(v.FileUniqueID, maxNameRunes),
		Duration: v.Duration, MimeType: cleanText(v.MimeType, maxNameRunes), FileSize: v.FileSize}
}

func (r fileRefFunc) sticker(s sticker, omitted *[]string) sticker {
	var animationFile *file
	if s.PremiumAnimation != nil {
		animationFile = &file{FileRef: r.ref(s.PremiumAnimation.fileID),
			FileUniqueID: cleanText(s.PremiumAnimation.FileUniqueID, maxNameRunes), FileSize: s.PremiumAnimation.FileSize}
	}
	var mask *maskPositionOut
	if s.MaskPosition != nil {
		mask = &maskPositionOut{Point: cleanText(s.MaskPosition.Point, maxNameRunes), XShift: s.MaskPosition.XShift,
			YShift: s.MaskPosition.YShift, Scale: s.MaskPosition.Scale}
	}
	return sticker{Kind: "sticker", FileRef: r.ref(s.fileID), FileUniqueID: cleanText(s.FileUniqueID, maxNameRunes),
		Type: cleanText(s.Type, maxNameRunes), Width: s.Width, Height: s.Height,
		IsAnimated: s.IsAnimated, IsVideo: s.IsVideo,
		Thumbnail: bounded(omitted, "media_items.sticker.thumbnail", r.thumbnail(s.Thumbnail)),
		Emoji:     cleanText(s.Emoji, maxNameRunes), SetName: cleanText(s.SetName, maxNameRunes),
		PremiumAnimation: bounded(omitted, "media_items.sticker.premium_animation", animationFile),
		MaskPosition:     bounded(omitted, "media_items.sticker.mask_position", mask),
		CustomEmojiID:    cleanText(s.CustomEmojiID, maxNameRunes), NeedsRepainting: s.NeedsRepainting, FileSize: s.FileSize}
}

func (r fileRefFunc) livePhoto(l livePhoto, omitted *[]string) livePhoto {
	return livePhoto{Kind: "live_photo", Photo: bounded(omitted, "media_items.live_photo.photo", r.photoSizes(l.Photo)),
		FileRef: r.ref(l.fileID), FileUniqueID: cleanText(l.FileUniqueID, maxNameRunes),
		Width: l.Width, Height: l.Height, Duration: l.Duration, MimeType: cleanText(l.MimeType, maxNameRunes),
		FileSize: l.FileSize}
}

// mediaItems lists every media object of a message in the order animation, audio, document, live_photo, photo,
// sticker, video, video_note, voice; a photo contributes one entry per size, with kind photo. A nested object
// over the limits is left out and named in omitted as media_items.<kind>.<member>; an entry over the limits is
// left out as media_items.<kind>; photo sizes that would take the list past its limit are left out as
// media_items.photo.
func mediaItems(m *tgMessage, r fileRefFunc) (items []any, omitted []string) {
	add := func(kind string, item any) {
		if withinLimits(item) {
			items = append(items, item)
		} else {
			omitted = append(omitted, "media_items."+kind)
		}
	}
	others := 0
	for _, present := range []bool{m.Animation != nil, m.Audio != nil, m.Document != nil, m.LivePhoto != nil,
		m.Sticker != nil, m.Video != nil, m.VideoNote != nil, m.Voice != nil} {
		if present {
			others++
		}
	}
	if m.Animation != nil {
		add("animation", r.animation(*m.Animation, &omitted))
	}
	if m.Audio != nil {
		add("audio", r.audio(*m.Audio, &omitted))
	}
	if m.Document != nil {
		add("document", r.document(*m.Document, &omitted))
	}
	if m.LivePhoto != nil {
		add("live_photo", r.livePhoto(*m.LivePhoto, &omitted))
	}
	switch {
	case len(m.Photo)+others > maxNestedList:
		omitted = append(omitted, "media_items.photo")
	default:
		for _, p := range m.Photo {
			add("photo", r.photoSize(p, "photo"))
		}
	}
	if m.Sticker != nil {
		add("sticker", r.sticker(*m.Sticker, &omitted))
	}
	if m.Video != nil {
		add("video", r.video(*m.Video, &omitted))
	}
	if m.VideoNote != nil {
		add("video_note", r.videoNote(*m.VideoNote, &omitted))
	}
	if m.Voice != nil {
		add("voice", r.voice(*m.Voice))
	}
	return items, omitted
}
