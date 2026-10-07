package bookstack

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	// maxAttachmentContentChars bounds the base64 text of one attachment read from the answer.
	maxAttachmentContentChars = 96 << 20
	// maxAttachmentMetaBytes bounds everything else in the answer, so the whole answer is at most 97 MiB.
	maxAttachmentMetaBytes = 1 << 20
	// maxAttachmentValueBytes bounds one metadata value.
	maxAttachmentValueBytes = 64 << 10
	// maxAttachmentFileBytes bounds the decoded file a download writes.
	maxAttachmentFileBytes = 72 << 20
)

var (
	errAttachmentTooLarge = errors.New("attachment answer too large")
	errAttachmentInvalid  = errors.New("attachment answer invalid")
)

// attachmentJSON mirrors the BookStack attachment fields this provider reads; content is handled by the stream.
type attachmentJSON struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Extension  string    `json:"extension"`
	UploadedTo int64     `json:"uploaded_to"`
	External   bool      `json:"external"`
	Order      int64     `json:"order"`
	CreatedBy  *userJSON `json:"created_by"`
	UpdatedBy  *userJSON `json:"updated_by"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
	Links      struct {
		HTML     string `json:"html"`
		Markdown string `json:"markdown"`
	} `json:"links"`
}

// attachmentRead is the outcome of reading one attachment answer.
type attachmentRead struct {
	Meta       attachmentJSON
	Link       string // the target of a link attachment, untrusted
	HasContent bool
	Decoded    int64 // bytes of file content that went to the writer
}

// readAttachment reads the answer of an attachment read as a stream. The base64 text of the content is decoded
// piece by piece and never held in memory: it goes to the writer that open returns. open is called once, when
// the content is reached or the object ends without one, and only after id, external, and uploaded_to are
// known; it is where the caller binds the attachment to its page. A link attachment never reaches the writer.
// The writer receives at most maxDecoded bytes.
func readAttachment(r io.Reader, maxDecoded int64, open func(*attachmentJSON) (io.Writer, error)) (attachmentRead, error) {
	p := &attachmentParser{br: bufio.NewReaderSize(&capReader{r: r, left: maxAttachmentContentChars + maxAttachmentMetaBytes}, 64<<10)}
	var out attachmentRead
	seen := map[string]bool{}
	opened := false
	call := func() (io.Writer, error) {
		opened = true
		return open(&out.Meta)
	}
	if b, err := p.next(); err != nil || b != '{' {
		return out, p.fail(err)
	}
	for {
		b, err := p.next()
		if err != nil {
			return out, p.fail(err)
		}
		if b == '}' {
			break
		}
		if b == ',' && len(seen) > 0 {
			if b, err = p.next(); err != nil {
				return out, p.fail(err)
			}
		}
		if b != '"' {
			return out, errAttachmentInvalid
		}
		keyRaw, err := p.str(256)
		if err != nil {
			return out, p.fail(err)
		}
		var key string
		if json.Unmarshal(keyRaw, &key) != nil {
			return out, errAttachmentInvalid
		}
		if b, err := p.next(); err != nil || b != ':' {
			return out, p.fail(err)
		}
		if seen[key] {
			return out, errAttachmentInvalid
		}
		seen[key] = true
		if key == "content" {
			if err := p.content(&out, seen, call, maxDecoded); err != nil {
				return out, err
			}
			continue
		}
		raw, err := p.value(maxAttachmentValueBytes)
		if err != nil {
			return out, p.fail(err)
		}
		if err := assignAttachment(&out.Meta, key, raw); err != nil {
			return out, err
		}
	}
	if !opened {
		if _, err := call(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// assignAttachment stores the metadata fields that are read; other keys are ignored.
func assignAttachment(m *attachmentJSON, key string, raw []byte) error {
	var target any
	switch key {
	case "id":
		target = &m.ID
	case "name":
		target = &m.Name
	case "extension":
		target = &m.Extension
	case "uploaded_to":
		target = &m.UploadedTo
	case "external":
		target = &m.External
	case "order":
		target = &m.Order
	case "created_by":
		target = &m.CreatedBy
	case "updated_by":
		target = &m.UpdatedBy
	case "created_at":
		target = &m.CreatedAt
	case "updated_at":
		target = &m.UpdatedAt
	case "links":
		target = &m.Links
	default:
		return nil
	}
	if json.Unmarshal(raw, target) != nil {
		return errAttachmentInvalid
	}
	return nil
}

type attachmentParser struct {
	br *bufio.Reader
}

// fail turns an end of input into an invalid answer and passes any other failure of the source on.
func (p *attachmentParser) fail(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errAttachmentInvalid
	}
	return err
}

// next returns the next byte that is not white space.
func (p *attachmentParser) next() (byte, error) {
	for {
		b, err := p.br.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return b, nil
		}
	}
}

// str reads the rest of a string whose opening quote is consumed and returns it with both quotes.
func (p *attachmentParser) str(limit int) ([]byte, error) {
	buf := []byte{'"'}
	escaped := false
	for {
		b, err := p.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if len(buf) >= limit {
			return nil, errAttachmentInvalid
		}
		buf = append(buf, b)
		switch {
		case escaped:
			escaped = false
		case b == '\\':
			escaped = true
		case b == '"':
			return buf, nil
		}
	}
}

// value reads one JSON value, the first byte included, bounded by limit.
func (p *attachmentParser) value(limit int) ([]byte, error) {
	b, err := p.next()
	if err != nil {
		return nil, err
	}
	if b == '"' {
		return p.str(limit)
	}
	buf := []byte{b}
	depth := 0
	if b == '{' || b == '[' {
		depth = 1
	}
	for {
		c, err := p.br.Peek(1)
		if err != nil {
			return nil, err
		}
		if depth == 0 && strings.IndexByte(",}] \t\r\n", c[0]) >= 0 {
			return buf, nil
		}
		_, _ = p.br.ReadByte()
		if len(buf) >= limit {
			return nil, errAttachmentInvalid
		}
		buf = append(buf, c[0])
		switch c[0] {
		case '"':
			rest, err := p.str(limit)
			if err != nil {
				return nil, err
			}
			buf = append(buf, rest[1:]...)
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return buf, nil
			}
		}
	}
}

// content reads the content value. A link is read as a bounded string; file content is decoded as a stream.
func (p *attachmentParser) content(out *attachmentRead, seen map[string]bool, open func() (io.Writer, error), maxDecoded int64) error {
	if !seen["id"] || !seen["external"] || !seen["uploaded_to"] {
		return errAttachmentInvalid
	}
	w, err := open()
	if err != nil {
		return err
	}
	b, err := p.next()
	if err != nil {
		return p.fail(err)
	}
	out.HasContent = true
	switch {
	case b == 'n':
		out.HasContent = false
		for _, want := range []byte("ull") {
			if c, err := p.br.ReadByte(); err != nil || c != want {
				return p.fail(err)
			}
		}
		return nil
	case b != '"':
		return errAttachmentInvalid
	case out.Meta.External:
		raw, err := p.str(maxAttachmentValueBytes)
		if err != nil {
			return p.fail(err)
		}
		if json.Unmarshal(raw, &out.Link) != nil {
			return errAttachmentInvalid
		}
		return nil
	}
	q := &quotedReader{br: p.br, left: maxAttachmentContentChars}
	dec := base64.NewDecoder(base64.StdEncoding, q)
	n, err := io.Copy(w, io.LimitReader(dec, maxDecoded))
	out.Decoded = n
	if err != nil {
		if q.err != nil {
			return q.err
		}
		var corrupt base64.CorruptInputError
		if errors.As(err, &corrupt) {
			return errAttachmentInvalid
		}
		return err
	}
	if n == maxDecoded {
		var probe [1]byte
		if m, _ := dec.Read(probe[:]); m > 0 {
			return errAttachmentTooLarge
		}
	}
	if q.err != nil {
		return q.err
	}
	if !q.closed {
		// The limit ended the copy before the closing quote: only the padding may remain.
		if _, err := io.Copy(io.Discard, q); err != nil {
			return q.err
		}
	}
	if !q.closed {
		return errAttachmentInvalid
	}
	return nil
}

// quotedReader yields the raw bytes of a string whose opening quote is consumed, up to the closing quote.
// Raw base64 text holds no escape sequence, so a backslash makes the answer invalid.
type quotedReader struct {
	br     *bufio.Reader
	left   int64
	closed bool
	err    error
}

func (q *quotedReader) Read(p []byte) (int, error) {
	if q.closed {
		return 0, io.EOF
	}
	if q.err != nil {
		return 0, q.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if q.br.Buffered() == 0 {
		if _, err := q.br.Peek(1); err != nil {
			q.err = err
			if errors.Is(err, io.EOF) {
				q.err = errAttachmentInvalid
			}
			return 0, q.err
		}
	}
	buf, _ := q.br.Peek(min(len(p), q.br.Buffered()))
	n := 0
	for n < len(buf) && buf[n] != '"' && buf[n] != '\\' {
		n++
	}
	if int64(n) > q.left {
		q.err = errAttachmentTooLarge
		return 0, q.err
	}
	q.left -= int64(n)
	copy(p, buf[:n])
	_, _ = q.br.Discard(n)
	if n < len(buf) {
		if buf[n] == '\\' {
			q.err = errAttachmentInvalid
			if n == 0 {
				return 0, q.err
			}
			return n, nil
		}
		_, _ = q.br.Discard(1)
		q.closed = true
		if n == 0 {
			return 0, io.EOF
		}
	}
	return n, nil
}

// capReader fails with errAttachmentTooLarge when the parser needs more than left bytes of the answer.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errAttachmentTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}
