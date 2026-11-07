package httpbind

import (
	"net/url"
	"regexp"
	"strings"
)

// seg is one path segment of a URI pattern: a literal, or a label that fills the whole segment.
type seg struct {
	lit    string
	label  string
	greedy bool
}

// pattern is a parsed URI pattern: the path segments and the literal query pairs of the http trait.
type pattern struct {
	segs  []seg
	query [][2]string
}

var literal = regexp.MustCompile(`^[A-Za-z0-9._~:@!$&'()*+,;=-]+$`)

// parseURI reads the uri of the http trait. A label fills a whole segment, at most one is greedy and it is
// the last segment; an empty segment, a dot segment and a trailing slash are refused, so a pattern cannot
// name a path the model did not mean.
func parseURI(uri string) (pattern, error) {
	var p pattern
	path, query, hasQuery := strings.Cut(uri, "?")
	if !strings.HasPrefix(path, "/") {
		return p, shapeErr("the uri pattern must start with a slash")
	}
	if path != "/" {
		parts := strings.Split(path[1:], "/")
		for i, part := range parts {
			switch {
			case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
				name := part[1 : len(part)-1]
				greedy := strings.HasSuffix(name, "+")
				name = strings.TrimSuffix(name, "+")
				if !labelName.MatchString(name) || (greedy && i != len(parts)-1) {
					return p, shapeErr("a label of the uri pattern is malformed or greedy before the end")
				}
				p.segs = append(p.segs, seg{label: name, greedy: greedy})
			case literal.MatchString(part) && part != "." && part != "..":
				p.segs = append(p.segs, seg{lit: part})
			default:
				return p, shapeErr("a segment of the uri pattern is empty, a dot segment or malformed")
			}
		}
	}
	if hasQuery {
		seen := map[string]bool{}
		for _, pair := range strings.Split(query, "&") {
			k, v, _ := strings.Cut(pair, "=")
			if !literal.MatchString(k) || (v != "" && !literal.MatchString(v)) || seen[k] {
				return p, shapeErr("the literal query of the uri pattern is malformed")
			}
			seen[k] = true
			p.query = append(p.query, [2]string{k, v})
		}
	}
	return p, nil
}

// labels returns the label names of the pattern.
func (p pattern) labels() map[string]bool {
	out := map[string]bool{}
	for _, s := range p.segs {
		if s.label != "" {
			out[s.label] = true
		}
	}
	return out
}

// Escape percent-encodes everything but the RFC 3986 unreserved characters, so a value cannot add a
// separator, a dot segment or a second query parameter. It serves labels and query values alike.
func Escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// labelValue checks and encodes one label value. Control characters, an empty value, a dot segment and, in a
// greedy label, an empty or dot segment are refused: the server would normalize them away from the path
// the caller named.
func labelValue(s seg, value, member string) (string, error) {
	bad := func() (string, error) {
		return "", valueErr("%s is empty, has a control character or a dot segment", member)
	}
	if value == "" {
		return bad()
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return bad()
		}
	}
	parts := []string{value}
	if s.greedy {
		parts = strings.Split(value, "/")
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return bad()
		}
		parts[i] = Escape(part)
	}
	return strings.Join(parts, "/"), nil
}

// build fills the pattern: the escaped path, the decoded path and the literal query.
func (p pattern) build(labels map[string]string) (raw, decoded string, err error) {
	if len(p.segs) == 0 {
		return "/", "/", nil
	}
	var b strings.Builder
	for _, s := range p.segs {
		b.WriteByte('/')
		if s.label == "" {
			b.WriteString(s.lit)
			continue
		}
		b.WriteString(labels[s.label])
	}
	raw = b.String()
	decoded, err = url.PathUnescape(raw)
	return raw, decoded, err
}
