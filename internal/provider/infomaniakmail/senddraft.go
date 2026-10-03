package infomaniakmail

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"
)

// draftHeaders are the only headers a draft may carry to be sent: the ones Qatlas writes itself. Any other
// header refuses the draft, so no header of a foreign draft reaches the wire.
var draftHeaders = map[string]bool{
	"From": true, "To": true, "Cc": true, "Bcc": true, "Subject": true, "Date": true, "Message-Id": true,
	"In-Reply-To": true, "References": true, "Mime-Version": true, "Content-Type": true,
	"Content-Transfer-Encoding": true,
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 || c == 0 {
			return false
		}
	}
	return true
}

// outgoingFromDraft checks a draft that was read from the drafts folder against the rules of messages.send and
// rebuilds it for sending: the headers are written again from the checked values, Bcc is dropped, Date is set
// to now, and the body is kept byte for byte after it has been checked. It refuses anything it cannot validate.
func (s scope) outgoingFromDraft(raw []byte) (*outgoing, error) {
	reject := func(reason string) (*outgoing, error) {
		return nil, invalidRequest("the draft cannot be sent: " + reason)
	}
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	if end < 0 {
		return reject("its header block is not in the expected form")
	}
	block, body := raw[:end+4], raw[end+4:]
	if bytes.Count(block, []byte("\r")) != bytes.Count(block, []byte("\r\n")) ||
		bytes.Count(block, []byte("\n")) != bytes.Count(block, []byte("\r\n")) || !utf8.Valid(block) {
		return reject("its header block is not in the expected form")
	}
	for _, c := range block {
		if (c < 0x20 && c != '\r' && c != '\n' && c != '\t') || c == 0x7f {
			return reject("a header holds a control character")
		}
	}
	headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(block))).ReadMIMEHeader()
	if err != nil {
		return reject("its headers cannot be read")
	}
	for key, values := range headers {
		if !draftHeaders[key] || len(values) != 1 {
			return reject("it has a header that is unknown or repeated")
		}
	}
	addresses := func(key string) ([]string, bool) {
		if len(headers[key]) == 0 {
			return nil, true
		}
		list, err := mail.ParseAddressList(headers[key][0])
		if err != nil {
			return nil, false
		}
		out := make([]string, 0, len(list))
		for _, a := range list {
			if a.Name != "" || !validRecipient(a.Address) {
				return nil, false
			}
			out = append(out, a.Address)
		}
		return out, true
	}
	from, ok1 := addresses("From")
	to, ok2 := addresses("To")
	cc, ok3 := addresses("Cc")
	bcc, ok4 := addresses("Bcc")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return reject("an address is not a plain email address")
	}
	if len(from) != 1 || !strings.EqualFold(from[0], s.mailbox) || !s.allowsSender(s.mailbox) {
		return reject("From is not the mailbox of this connection")
	}
	if err := checkRecipients(to, cc, bcc); err != nil {
		return reject("it needs 1 to 50 plain recipients in To, Cc, and Bcc together")
	}
	subject := ""
	if len(headers["Subject"]) == 1 {
		subject, err = new(mime.WordDecoder).DecodeHeader(headers["Subject"][0])
		if err != nil || !validSubject(subject) {
			return reject("the subject is not valid")
		}
	}
	id := ""
	if len(headers["Message-Id"]) == 1 {
		id = strings.TrimSpace(headers["Message-Id"][0])
		if !validMessageID(id) {
			return reject("the Message-ID is not valid")
		}
	} else if id, err = messageID(s.mailbox); err != nil {
		return nil, providerError("send draft", "no message ID could be generated")
	}
	idList := func(key string) ([]string, bool) {
		if len(headers[key]) == 0 {
			return nil, true
		}
		ids := strings.Fields(headers[key][0])
		if len(ids) == 0 || len(ids) > maxReferences {
			return nil, false
		}
		for _, one := range ids {
			if !validMessageID(one) {
				return nil, false
			}
		}
		return ids, true
	}
	inReplyTo, ok1 := idList("In-Reply-To")
	references, ok2 := idList("References")
	if !ok1 || !ok2 || len(inReplyTo) > 1 || (len(inReplyTo) == 0 && len(references) > 0) {
		return reject("the reply headers are not valid")
	}
	if v := headers["Mime-Version"]; len(v) == 1 && strings.TrimSpace(v[0]) != "1.0" {
		return reject("the MIME version is not 1.0")
	}

	contentType := ""
	if len(headers["Content-Type"]) == 1 {
		contentType = headers["Content-Type"][0]
	}
	if contentType == "" || !isASCII([]byte(contentType)) || !isASCII(body) {
		return reject("the content is not 7-bit text in a known structure")
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return reject("the content type is not valid")
	}
	encoding := ""
	if len(headers["Content-Transfer-Encoding"]) == 1 {
		encoding = strings.ToLower(strings.TrimSpace(headers["Content-Transfer-Encoding"][0]))
	}
	attachments := []DraftAttachment{}
	switch mediaType {
	case "text/plain":
		if encoding != "" && encoding != "7bit" && encoding != "quoted-printable" && encoding != "base64" {
			return reject("the transfer encoding is not supported")
		}
	case "multipart/mixed":
		if encoding != "" && encoding != "7bit" || params["boundary"] == "" {
			return reject("the multipart structure is not supported")
		}
		if attachments, err = checkParts(body, params["boundary"]); err != nil {
			return reject(err.Error())
		}
	default:
		return reject("only a plain text part with attachments is supported")
	}

	var out bytes.Buffer
	out.WriteString("From: " + s.mailbox + "\r\n")
	out.WriteString(addressHeader("To", to))
	out.WriteString(addressHeader("Cc", cc))
	out.WriteString("Subject: " + foldWords(mime.QEncoding.Encode("utf-8", subject)) + "\r\n")
	out.WriteString("Date: " + now().Format(time.RFC1123Z) + "\r\n")
	out.WriteString("Message-ID: " + id + "\r\n")
	if len(inReplyTo) == 1 {
		references := references
		if len(references) == 0 {
			references = inReplyTo
		}
		out.WriteString(replyHeaders(inReplyTo[0], references))
	}
	out.WriteString("MIME-Version: 1.0\r\nContent-Type: " + contentType + "\r\n")
	if encoding != "" {
		out.WriteString("Content-Transfer-Encoding: " + encoding + "\r\n")
	}
	out.WriteString("\r\n")
	out.Write(body)
	return &outgoing{raw: out.Bytes(), from: s.mailbox, rcpts: recipientsOf(to, cc, bcc), messageID: id,
		attachments: attachments, draftKept: true}, nil
}

// checkParts walks the one level of a multipart/mixed draft: one plain text part and base64 attachments, within
// the limits of a draft. It returns the metadata of the attachments.
func checkParts(body []byte, boundary string) ([]DraftAttachment, error) {
	bad := errors.New("the multipart structure is not supported")
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	attachments := []DraftAttachment{}
	texts, total := 0, int64(0)
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count > maxDraftAttachments {
			return nil, bad
		}
		mediaType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return nil, bad
		}
		encoding := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")))
		if name := part.FileName(); name != "" {
			if _, ok := attachmentType(mediaType, name); !ok || encoding != "base64" || !validFileName(name) {
				return nil, bad
			}
			hash := sha256.New()
			size, err := io.Copy(hash, io.LimitReader(base64.NewDecoder(base64.StdEncoding, part), maxDraftTotalBytes+1))
			total += size
			if err != nil || total > maxDraftTotalBytes {
				return nil, errors.New("an attachment is not valid base64 or the attachments exceed 15 MiB")
			}
			attachments = append(attachments, DraftAttachment{Name: name, Type: mediaType, Size: size,
				SHA256: hex.EncodeToString(hash.Sum(nil))})
			continue
		}
		if mediaType != "text/plain" || texts > 0 {
			return nil, bad
		}
		texts++
	}
	if texts != 1 {
		return nil, bad
	}
	return attachments, nil
}
