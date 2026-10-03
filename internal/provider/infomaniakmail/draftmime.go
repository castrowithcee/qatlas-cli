package infomaniakmail

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Bounds of a draft. The message is built in memory, so the attachments share one total.
const (
	maxDraftRecipients  = 50
	maxDraftSubject     = 256
	maxDraftBody        = 256 << 10
	maxDraftAttachments = 10
	// maxDraftTotalBytes bounds all decoded attachments together; base64 makes the stored message a third
	// larger, which stays below the 25 MB message size Infomaniak documents.
	maxDraftTotalBytes = 15 << 20
	maxDraftInline     = 4 << 20
	// maxDraftInlineBase64 is the encoded length of maxDraftInline.
	maxDraftInlineBase64 = (maxDraftInline + 2) / 3 * 4
	maxDraftFileName     = 255
	// draftTransferBytes is the message size above which the longer transfer timeout applies.
	draftTransferBytes = 1 << 20
)

// now is the clock of the Date header; tests replace it.
var now = time.Now

// draftAttachment is one attachment whose content has been read and validated.
type draftAttachment struct {
	name        string
	contentType string
	content     []byte
}

// draftSpec is a validated draft. Every string in it is free of control characters and every address
// passed validAddress, so none of it can end a header line.
type draftSpec struct {
	from        string
	to, cc, bcc []string
	subject     string
	body        string
	attachments []draftAttachment
	// id is the Message-ID; empty means a fresh one is generated.
	id string
	// inReplyTo and references are validated Message-IDs of a reply.
	inReplyTo  string
	references []string
	// hideBcc leaves the Bcc header out, as a message that is sent must; the Bcc recipients then travel only
	// in the envelope.
	hideBcc bool
}

// mediaTypePattern is a plain type/subtype without parameters.
var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/[a-z0-9][a-z0-9!#$&^_.+-]{0,63}$`)

// plainText reports whether value is valid UTF-8 of at most max characters without any control, line
// separator, or paragraph separator character. It is the one test every value that becomes part of a
// header passes first.
func plainText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == ' ' || r == ' ' || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// validSubject accepts any plain text up to the subject limit, including the empty subject.
func validSubject(value string) bool { return plainText(value, maxDraftSubject) }

// validFileName accepts a plain file name without a path.
func validFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= maxDraftFileName &&
		plainText(name, maxDraftFileName) && !strings.ContainsAny(name, `/\`)
}

// validRecipient accepts a plain address that net/mail reads back unchanged.
func validRecipient(address string) bool {
	if !validAddress(address) {
		return false
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Name == "" && parsed.Address == address
}

// attachmentType returns the media type of an attachment: the given one when it is a plain type/subtype,
// else the one the file extension names, else application/octet-stream. multipart and message types are
// never accepted, so a part cannot pretend to carry structure of its own.
func attachmentType(given, name string) (string, bool) {
	value := strings.ToLower(given)
	if given == "" {
		value = "application/octet-stream"
		if known, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))); err == nil {
			value = known
		}
	}
	if !mediaTypePattern.MatchString(value) || strings.HasPrefix(value, "multipart/") ||
		strings.HasPrefix(value, "message/") {
		return "", false
	}
	return value, true
}

// DraftAttachment is the metadata of one attachment of a draft; the content is never returned.
type DraftAttachment struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// foldWords puts each RFC 2047 word of a long encoded header on its own line.
func foldWords(encoded string) string { return strings.ReplaceAll(encoded, "?= =?", "?=\r\n =?") }

// addressHeader writes an address list one address per line, so no line gets near the line limit.
func addressHeader(name string, addresses []string) string {
	if len(addresses) == 0 {
		return ""
	}
	return name + ": " + strings.Join(addresses, ",\r\n ") + "\r\n"
}

// replyHeaders writes In-Reply-To and References for validated Message-IDs, one ID per line in References.
func replyHeaders(inReplyTo string, references []string) string {
	out := ""
	if inReplyTo != "" {
		out += "In-Reply-To: " + inReplyTo + "\r\n"
	}
	if len(references) > 0 {
		out += "References: " + strings.Join(references, "\r\n ") + "\r\n"
	}
	return out
}

// base64Lines encodes data as base64 in lines of 76 characters.
func base64Lines(data []byte) []byte {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(encoded, data)
	var out bytes.Buffer
	for len(encoded) > 76 {
		out.Write(encoded[:76])
		out.WriteString("\r\n")
		encoded = encoded[76:]
	}
	out.Write(encoded)
	out.WriteString("\r\n")
	return out.Bytes()
}

func quotedPrintable(text string) ([]byte, error) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out bytes.Buffer
	writer := quotedprintable.NewWriter(&out)
	if _, err := writer.Write([]byte(text)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// messageID returns a fresh Message-ID in the domain of the mailbox.
func messageID(from string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	_, domain, _ := strings.Cut(from, "@")
	return "<" + hex.EncodeToString(random[:]) + "@" + domain + ">", nil
}

// build writes the draft as one RFC 5322 message: fixed headers in a fixed order from validated values,
// the text as quoted-printable UTF-8, and each attachment as a base64 part. The headers Qatlas writes are
// the only headers; no header ever comes from an argument as such.
func (d draftSpec) build() ([]byte, []DraftAttachment, error) {
	id := d.id
	if id == "" {
		var err error
		if id, err = messageID(d.from); err != nil {
			return nil, nil, errors.New("no message ID could be generated")
		}
	}
	text, err := quotedPrintable(d.body)
	if err != nil {
		return nil, nil, errors.New("the text could not be encoded")
	}
	var out bytes.Buffer
	out.WriteString("From: " + d.from + "\r\n")
	out.WriteString(addressHeader("To", d.to))
	out.WriteString(addressHeader("Cc", d.cc))
	if !d.hideBcc {
		out.WriteString(addressHeader("Bcc", d.bcc))
	}
	out.WriteString("Subject: " + foldWords(mime.QEncoding.Encode("utf-8", d.subject)) + "\r\n")
	out.WriteString("Date: " + now().Format(time.RFC1123Z) + "\r\n")
	out.WriteString("Message-ID: " + id + "\r\n")
	out.WriteString(replyHeaders(d.inReplyTo, d.references))
	out.WriteString("MIME-Version: 1.0\r\n")

	if len(d.attachments) == 0 {
		out.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		out.Write(text)
		return out.Bytes(), []DraftAttachment{}, nil
	}

	var parts bytes.Buffer
	writer := multipart.NewWriter(&parts)
	out.WriteString("Content-Type: " + mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": writer.Boundary()}) +
		"\r\n\r\n")
	textPart, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, nil, errors.New("the message could not be built")
	}
	_, _ = textPart.Write(text)
	summary := make([]DraftAttachment, 0, len(d.attachments))
	for _, attachment := range d.attachments {
		contentType := mime.FormatMediaType(attachment.contentType, map[string]string{"name": attachment.name})
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": attachment.name})
		if contentType == "" || disposition == "" {
			return nil, nil, errors.New("an attachment name cannot be encoded")
		}
		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {contentType},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {disposition},
		})
		if err != nil {
			return nil, nil, errors.New("the message could not be built")
		}
		_, _ = part.Write(base64Lines(attachment.content))
		sum := sha256.Sum256(attachment.content)
		summary = append(summary, DraftAttachment{Name: attachment.name, Type: attachment.contentType,
			Size: int64(len(attachment.content)), SHA256: hex.EncodeToString(sum[:])})
	}
	if err := writer.Close(); err != nil {
		return nil, nil, errors.New("the message could not be built")
	}
	out.Write(parts.Bytes())
	return out.Bytes(), summary, nil
}
