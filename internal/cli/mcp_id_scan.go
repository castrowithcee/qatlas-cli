package cli

import "encoding/json"

// maxMCPIDBytes bounds the raw request id echoed back for an oversized message.
const maxMCPIDBytes = 256

// maxMCPKeyBytes only needs to hold the quoted key "id".
const maxMCPKeyBytes = 8

const (
	scanStart = iota
	scanKey
	scanKeyString
	scanColon
	scanValue
	scanValueString
	scanValueScalar
	scanValueNested
	scanComma
	scanDone
)

// mcpIDScanner extracts the raw top-level "id" of a JSON object from a byte stream of any length,
// in any fragmentation, with fixed memory. Keys written with escapes never match "id".
type mcpIDScanner struct {
	mode     int
	inString bool
	escaped  bool
	nesting  int
	isID     bool
	buf      []byte
	overflow bool
	id       json.RawMessage
}

// ID returns the last valid string or number id seen so far, or nil.
func (s *mcpIDScanner) ID() json.RawMessage { return s.id }

func (s *mcpIDScanner) feed(p []byte) {
	for _, c := range p {
		if s.mode == scanDone {
			return
		}
		s.step(c)
	}
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func (s *mcpIDScanner) begin(mode int, c byte) {
	s.mode = mode
	s.buf = append(s.buf[:0], c)
	s.overflow = false
	s.inString = false
	s.escaped = false
}

func (s *mcpIDScanner) capture(c byte) {
	limit := maxMCPIDBytes
	if s.mode == scanKeyString {
		limit = maxMCPKeyBytes
	}
	if len(s.buf) >= limit {
		s.overflow = true
		return
	}
	s.buf = append(s.buf, c)
}

// stringEnds consumes one byte of a string body and reports whether it was the closing quote.
func (s *mcpIDScanner) stringEnds(c byte) bool {
	switch {
	case s.escaped:
		s.escaped = false
	case c == '\\':
		s.escaped = true
	case c == '"':
		return true
	}
	return false
}

func (s *mcpIDScanner) step(c byte) {
	switch s.mode {
	case scanStart:
		if isJSONSpace(c) {
			return
		}
		if c == '{' {
			s.mode = scanKey
			return
		}
		s.mode = scanDone
	case scanKey:
		switch {
		case c == '"':
			s.begin(scanKeyString, c)
		case c == '}':
			s.mode = scanDone
		case !isJSONSpace(c):
			s.mode = scanDone
		}
	case scanKeyString:
		s.capture(c)
		if s.stringEnds(c) {
			s.isID = !s.overflow && string(s.buf) == `"id"`
			s.mode = scanColon
		}
	case scanColon:
		if isJSONSpace(c) {
			return
		}
		if c != ':' {
			s.mode = scanDone
			return
		}
		s.mode = scanValue
	case scanValue:
		switch {
		case isJSONSpace(c):
		case c == '"':
			s.begin(scanValueString, c)
		case c == '{' || c == '[':
			s.mode = scanValueNested
			s.nesting = 1
			s.inString = false
			s.escaped = false
		case c == ',' || c == '}' || c == ']' || c == ':':
			s.mode = scanDone
		default:
			s.begin(scanValueScalar, c)
		}
	case scanValueString:
		s.capture(c)
		if s.stringEnds(c) {
			s.finishValue(true)
		}
	case scanValueScalar:
		if isJSONSpace(c) || c == ',' || c == '}' {
			s.finishValue(false)
			s.step(c)
			return
		}
		s.capture(c)
	case scanValueNested:
		s.stepNested(c)
	case scanComma:
		switch {
		case isJSONSpace(c):
		case c == ',':
			s.mode = scanKey
		default:
			s.mode = scanDone
		}
	}
}

func (s *mcpIDScanner) stepNested(c byte) {
	if s.inString {
		if s.stringEnds(c) {
			s.inString = false
		}
		return
	}
	switch c {
	case '"':
		s.inString = true
	case '{', '[':
		s.nesting++
	case '}', ']':
		s.nesting--
		if s.nesting == 0 {
			if s.isID {
				s.id = nil
			}
			s.mode = scanComma
		}
	}
}

// finishValue settles a completed top-level value; the last "id" wins, so any unusable value clears it.
func (s *mcpIDScanner) finishValue(isString bool) {
	s.mode = scanComma
	if !s.isID {
		return
	}
	s.id = nil
	if s.overflow || len(s.buf) == 0 {
		return
	}
	if !isString && s.buf[0] != '-' && (s.buf[0] < '0' || s.buf[0] > '9') {
		return
	}
	if json.Valid(s.buf) {
		s.id = append(json.RawMessage(nil), s.buf...)
	}
}
