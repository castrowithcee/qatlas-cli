package infomaniakmail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Bounds of messages.get and attachments.get.
const (
	// maxBodyChars caps the returned text of a message in characters.
	maxBodyChars = 20000
	// maxBodyFetch caps the bytes of the text part that are fetched, in their transfer encoding.
	maxBodyFetch = 96 * 1024
	// maxAttachments caps the attachment entries of one message; maxParts the MIME parts looked at.
	maxAttachments = 50
	maxParts       = 200
	// maxPartDepth is the deepest part path that is listed or fetched.
	maxPartDepth      = 8
	maxAttachmentName = 255
	maxMediaType      = 100
	// maxInlineBytes is the largest decoded attachment returned inline as base64; maxInlineEncoded bounds the
	// transfer-encoded part that is read for it, which base64 and its line breaks make about a third larger.
	maxInlineBytes   = 4 << 20
	maxInlineEncoded = 6 << 20
)

// messageRef names one message. The UID means something only with the folder and its UIDVALIDITY.
type messageRef struct {
	folder      string
	uid         uint32
	uidValidity uint32
}

// mimePart is one single (non-multipart) part of a message with the part number taken from the message's own
// BODYSTRUCTURE. A part number never comes from an argument.
type mimePart struct {
	path   []int
	single *imap.BodyStructureSinglePart
}

func (p mimePart) id() string {
	text := make([]string, len(p.path))
	for i, n := range p.path {
		text[i] = strconv.Itoa(n)
	}
	return strings.Join(text, ".")
}

// partsOf lists the single parts of a body structure in document order. A message/rfc822 part counts as one
// part; its content is not looked into.
func partsOf(structure imap.BodyStructure) []mimePart {
	var parts []mimePart
	if structure == nil {
		return parts
	}
	structure.Walk(func(path []int, part imap.BodyStructure) bool {
		if len(parts) >= maxParts {
			return false
		}
		if single, ok := part.(*imap.BodyStructureSinglePart); ok && len(path) > 0 && len(path) <= maxPartDepth {
			parts = append(parts, mimePart{path: append([]int(nil), path...), single: single})
		}
		return true
	})
	return parts
}

func (p mimePart) mediaType() string { return p.single.MediaType() }

func (p mimePart) isAttachmentDisposition() bool {
	d := p.single.Disposition()
	return d != nil && strings.EqualFold(d.Value, "attachment")
}

// isBodyCandidate is a text part that reads as the message's own text, not as a file.
func (p mimePart) isBodyCandidate(mediaType string) bool {
	return p.mediaType() == mediaType && !p.isAttachmentDisposition() && p.single.Filename() == ""
}

// bodyPartOf picks the text of the message: the first text/plain part, else the first text/html part.
func bodyPartOf(parts []mimePart) *mimePart {
	for _, mediaType := range []string{"text/plain", "text/html"} {
		for i := range parts {
			if parts[i].isBodyCandidate(mediaType) {
				return &parts[i]
			}
		}
	}
	return nil
}

// attachmentsOf lists the parts that are files: every part but the chosen text that is marked as an
// attachment, carries a file name, or is no plain or HTML text. The other plain or HTML parts are
// alternatives of the text.
func attachmentsOf(parts []mimePart, body *mimePart) []mimePart {
	var out []mimePart
	for i := range parts {
		p := parts[i]
		if body != nil && p.id() == body.id() {
			continue
		}
		mediaType := p.mediaType()
		textual := mediaType == "text/plain" || mediaType == "text/html"
		if p.isAttachmentDisposition() || p.single.Filename() != "" || !textual {
			out = append(out, p)
		}
	}
	return out
}

// Attachment is the metadata of one attachment. Size is the part as stored in the message, in its transfer
// encoding, so a base64 attachment is about a third larger than the file.
type Attachment struct {
	Part string `json:"part"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func attachmentOf(p mimePart) Attachment {
	return Attachment{Part: p.id(), Name: nameOf(p), Type: clean(p.mediaType(), maxMediaType), Size: int64(p.single.Size)}
}

// nameOf returns the cleaned and bounded file name, with RFC 2047 words decoded.
func nameOf(p mimePart) string {
	name := p.single.Filename()
	if decoded, err := new(mime.WordDecoder).DecodeHeader(name); err == nil {
		name = decoded
	}
	return clean(name, maxAttachmentName)
}

// decodeReader undoes the transfer encoding of a part. The second result is false for an encoding that is
// not defined.
func decodeReader(encoding string, r io.Reader) (io.Reader, bool) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return r, true
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, skipBlanks{r}), true
	case "quoted-printable":
		return quotedprintable.NewReader(r), true
	}
	return nil, false
}

// skipBlanks drops the spaces and tabs a base64 body may carry; the decoder skips line breaks itself.
type skipBlanks struct{ r io.Reader }

func (s skipBlanks) Read(p []byte) (int, error) {
	for {
		n, err := s.r.Read(p)
		kept := 0
		for _, b := range p[:n] {
			if b != ' ' && b != '\t' {
				p[kept] = b
				kept++
			}
		}
		if kept > 0 || err != nil || n == 0 {
			return kept, err
		}
	}
}

// textOf decodes the transfer encoding and the character set of a text part and bounds it. truncated says the
// fetched bytes were cut off before this call.
func textOf(raw []byte, p mimePart, truncated bool) (text string, cut bool) {
	reader, ok := decodeReader(p.single.Encoding, bytes.NewReader(raw))
	if !ok {
		return "", truncated
	}
	// A cut-off or damaged body keeps what decoded before the fault.
	decoded, _ := io.ReadAll(io.LimitReader(reader, 4*maxBodyFetch))
	charset := p.single.Params["charset"]
	if truncated && isUTF8(charset) {
		decoded = trimIncompleteRune(decoded)
	}
	text, cutChars := cleanBody(decodeCharset(decoded, charset), maxBodyChars)
	return text, truncated || cutChars
}

func isUTF8(charset string) bool {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return true
	}
	return false
}

func trimIncompleteRune(b []byte) []byte {
	for back := 1; back <= utf8.UTFMax && back <= len(b); back++ {
		if utf8.RuneStart(b[len(b)-back]) {
			if !utf8.FullRune(b[len(b)-back:]) {
				return b[:len(b)-back]
			}
			break
		}
	}
	return b
}

var windows1252 = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

var latin9 = map[byte]rune{
	0xA4: 0x20AC, 0xA6: 0x0160, 0xA8: 0x0161, 0xB4: 0x017D, 0xB8: 0x017E, 0xBC: 0x0152, 0xBD: 0x0153, 0xBE: 0x0178,
}

// decodeCharset converts the character sets that mail commonly uses and that need no dependency: UTF-8,
// US-ASCII, ISO-8859-1, ISO-8859-15, and Windows-1252. Any other set is read as UTF-8, where bytes that are
// not valid become U+FFFD instead of being guessed.
func decodeCharset(b []byte, charset string) string {
	var convert func(byte) rune
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "iso-8859-1", "iso8859-1", "latin1", "l1":
		convert = func(c byte) rune { return rune(c) }
	case "windows-1252", "cp1252":
		convert = func(c byte) rune {
			if c >= 0x80 && c <= 0x9F {
				return windows1252[c-0x80]
			}
			return rune(c)
		}
	case "iso-8859-15", "iso8859-15", "latin9":
		convert = func(c byte) rune {
			if r, ok := latin9[c]; ok {
				return r
			}
			return rune(c)
		}
	default:
		return string(b)
	}
	var builder strings.Builder
	for _, c := range b {
		builder.WriteRune(convert(c))
	}
	return builder.String()
}

// cleanBody makes message text safe to return: valid UTF-8, line breaks normalised to \n, tabs kept, every
// other control or line-separator character replaced by a space, and at most max characters. The second
// result says the text was cut.
func cleanBody(value string, max int) (string, bool) {
	value = strings.ToValidUTF8(value, "�")
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	var builder strings.Builder
	count := 0
	for _, r := range value {
		if count >= max {
			return builder.String(), true
		}
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == ' ' || r == ' ') {
			r = ' '
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String(), false
}

// errNoSuchMessage is what every refusal to show a message reports: a missing UID and a sender outside the
// allow-list look the same.
func errNoSuchMessage(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "Infomaniak Mail holds no such message in this folder"}
}

// loadedMessage is the metadata of one message: envelope, flags, size, and structure.
type loadedMessage struct {
	uid       imap.UID
	envelope  *imap.Envelope
	flags     []imap.Flag
	size      int64
	structure imap.BodyStructure
}

// examine opens the folder read-only and checks the UIDVALIDITY of the reference.
func examine(conn *session, op string, ref messageRef) error {
	selected, err := conn.client.Select(ref.folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return failure(op, err)
	}
	if selected.UIDValidity != ref.uidValidity {
		return invalidRequest("uidvalidity no longer matches this folder; list the folder again")
	}
	return nil
}

// load fetches the envelope, flags, size, and BODYSTRUCTURE of the one message and applies the sender
// allow-list to its parsed From addresses. None of these items sets a flag.
func (c *Client) load(conn *session, op string, ref messageRef) (*loadedMessage, error) {
	uid := imap.UID(ref.uid)
	buffers, err := conn.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID: true, Envelope: true, Flags: true, RFC822Size: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, failure(op, err)
	}
	for _, buffer := range buffers {
		if buffer.UID != uid {
			continue
		}
		if buffer.Envelope == nil || buffer.BodyStructure == nil {
			return nil, invalidResponse(op, "Infomaniak Mail answered without an envelope or body structure")
		}
		if !c.fromAllowed(buffer.Envelope.From) {
			return nil, errNoSuchMessage(op)
		}
		return &loadedMessage{uid: uid, envelope: buffer.Envelope, flags: buffer.Flags, size: buffer.RFC822Size,
			structure: buffer.BodyStructure}, nil
	}
	return nil, errNoSuchMessage(op)
}

// streamPart reads one part with BODY.PEEK, which never sets \Seen. partial, when set, asks for that byte
// range only. read gets the raw literal; when it returns an error the transport is dropped, since the rest
// of the literal is not read.
func streamPart(conn *session, op string, uid imap.UID, part []int, partial *imap.SectionPartial,
	read func(io.Reader) error) error {
	section := &imap.FetchItemBodySection{Part: part, Peek: true, Partial: partial}
	command := conn.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID: true, BodySection: []*imap.FetchItemBodySection{section},
	})
	found := false
	for message := command.Next(); message != nil; message = command.Next() {
		for item := message.Next(); item != nil; item = message.Next() {
			data, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok || data.Literal == nil || found {
				continue
			}
			found = true
			if err := read(data.Literal); err != nil {
				// Drop the transport first, so releasing the command discards the rest of the literal
				// without reading it from the network.
				conn.abort()
				_ = command.Close()
				return err
			}
		}
	}
	if err := command.Close(); err != nil {
		return failure(op, err)
	}
	if !found {
		return invalidResponse(op, "Infomaniak Mail answered without the requested part")
	}
	return nil
}
