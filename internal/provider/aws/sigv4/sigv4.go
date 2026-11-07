// Package sigv4 signs HTTP requests with AWS Signature Version 4 using only the standard library. SigV4a
// (multi-region) is deliberately not supported.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	algorithm = "AWS4-HMAC-SHA256"
	// UnsignedPayload is the payload hash value that tells the service not to verify the body. It is only
	// meant for HTTPS requests to services that accept it (S3).
	UnsignedPayload = "UNSIGNED-PAYLOAD"

	dateFormat      = "20060102T150405Z"
	scopeDateFormat = "20060102"
)

// Errors never carry a credential or a header value.
var (
	ErrNoCredentials = errors.New("sigv4: access key or secret key is empty")
	ErrNoScope       = errors.New("sigv4: region or service name is empty")
	ErrNoHost        = errors.New("sigv4: the request has no host")
	ErrBadQuery      = errors.New("sigv4: the request query is malformed")
)

// Credentials are static AWS credentials. SessionToken is optional. Every formatting verb prints a fixed
// placeholder, so a credential cannot reach a log or an error text by accident.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func (Credentials) String() string   { return "sigv4.Credentials{redacted}" }
func (Credentials) GoString() string { return "sigv4.Credentials{redacted}" }

// Clock supplies the signing time. The zero Signer uses the system clock.
type Clock interface{ Now() time.Time }

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Signer signs requests for one region and one signing name.
type Signer struct {
	Credentials Credentials
	// Region and Service form the credential scope. For a global service, Region is the signing region the
	// service documents (for example us-east-1), not a region of the caller.
	Region  string
	Service string
	// Clock is the time source; nil means the system clock.
	Clock Clock
	// ContentSHA256Header sets and signs x-amz-content-sha256 (required by S3).
	ContentSHA256Header bool
	// UnsignedPayload signs the literal UNSIGNED-PAYLOAD instead of the body hash and implies
	// ContentSHA256Header. Use it only over HTTPS.
	UnsignedPayload bool
	// S3Paths canonicalises the path the way S3 does: the decoded path is encoded once and dot segments and
	// repeated slashes are kept. Every other service double-encodes the normalised path.
	S3Paths bool
}

func (s *Signer) String() string { return "sigv4.Signer{" + s.Service + "/" + s.Region + "}" }

// unsignedHeaders are never part of the signature: proxies and the HTTP stack may rewrite them.
var unsignedHeaders = map[string]bool{
	"authorization":     true,
	"user-agent":        true,
	"x-amzn-trace-id":   true,
	"expect":            true,
	"connection":        true,
	"transfer-encoding": true,
}

// Sign adds x-amz-date, x-amz-security-token (when a session token is set), x-amz-content-sha256 (when
// requested) and Authorization to req. body is the exact payload that will be sent; it may be nil. Every
// header present on req except the unsigned ones, plus host, is signed, so set all headers before Sign.
func (s *Signer) Sign(req *http.Request, body []byte) error {
	c := s.Credentials
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return ErrNoCredentials
	}
	if s.Region == "" || s.Service == "" {
		return ErrNoScope
	}
	if req.URL == nil {
		return ErrNoHost
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if host == "" {
		return ErrNoHost
	}

	clock := s.Clock
	if clock == nil {
		clock = systemClock{}
	}
	now := clock.Now().UTC()
	stamp := now.Format(dateFormat)
	day := now.Format(scopeDateFormat)

	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("X-Amz-Date", stamp)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	} else {
		req.Header.Del("X-Amz-Security-Token")
	}
	payloadHash := hashHex(body)
	if s.UnsignedPayload {
		payloadHash = UnsignedPayload
	}
	if s.ContentSHA256Header || s.UnsignedPayload {
		req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}

	query, err := canonicalQuery(req.URL.RawQuery)
	if err != nil {
		return err
	}
	headers, signed := canonicalHeaders(host, req.Header)
	canonical := strings.Join([]string{
		req.Method,
		s.canonicalPath(req.URL),
		query,
		headers,
		signed,
		payloadHash,
	}, "\n")

	scope := day + "/" + s.Region + "/" + s.Service + "/aws4_request"
	toSign := algorithm + "\n" + stamp + "\n" + scope + "\n" + hashHex([]byte(canonical))
	signature := hex.EncodeToString(mac(signingKey(c.SecretAccessKey, day, s.Region, s.Service), toSign))

	req.Header.Set("Authorization", algorithm+" Credential="+c.AccessKeyID+"/"+scope+
		", SignedHeaders="+signed+", Signature="+signature)
	return nil
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mac(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// signingKey derives the key by four chained HMAC-SHA256 steps.
func signingKey(secret, day, region, service string) []byte {
	key := mac([]byte("AWS4"+secret), day)
	key = mac(key, region)
	key = mac(key, service)
	return mac(key, "aws4_request")
}

// canonicalHeaders returns the canonical header block (each line ends in a newline) and the signed header
// list. Names are lower-cased and sorted; values are trimmed with inner runs of spaces collapsed and
// repeated headers joined by a comma.
func canonicalHeaders(host string, header http.Header) (block, signed string) {
	values := map[string]string{"host": host}
	for name, list := range header {
		lower := strings.ToLower(name)
		if lower == "host" || unsignedHeaders[lower] {
			continue
		}
		trimmed := make([]string, len(list))
		for i, v := range list {
			trimmed[i] = collapseSpaces(v)
		}
		if prior, ok := values[lower]; ok {
			values[lower] = prior + "," + strings.Join(trimmed, ",")
		} else {
			values[lower] = strings.Join(trimmed, ",")
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name + ":" + values[name] + "\n")
	}
	return b.String(), strings.Join(names, ";")
}

func collapseSpaces(v string) string { return strings.Join(strings.Fields(v), " ") }

// canonicalQuery decodes, re-encodes with the SigV4 alphabet and sorts by key, then value.
func canonicalQuery(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", ErrBadQuery
	}
	pairs := make([][2]string, 0, len(values))
	for key, list := range values {
		for _, v := range list {
			pairs = append(pairs, [2]string{uriEncode(key, true), uriEncode(v, true)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p[0] + "=" + p[1]
	}
	return strings.Join(parts, "&"), nil
}

func (s *Signer) canonicalPath(u *url.URL) string {
	if s.S3Paths {
		p := u.Path
		if p == "" {
			p = "/"
		}
		return uriEncode(p, false)
	}
	// The service decodes the path once for routing and signs the encoded form once more, so the signed path
	// is the normalised wire path encoded a second time.
	wire := u.EscapedPath()
	if wire == "" {
		wire = "/"
	}
	return uriEncode(normalizePath(wire), false)
}

// normalizePath removes dot segments and repeated slashes and keeps a trailing slash.
func normalizePath(p string) string {
	trailing := strings.HasSuffix(p, "/")
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	joined := "/" + strings.Join(out, "/")
	if trailing && len(out) > 0 {
		joined += "/"
	}
	return joined
}

// uriEncode percent-encodes everything but the unreserved characters (A-Z a-z 0-9 - _ . ~) with upper-case
// hex. A slash stays literal when encodeSlash is false.
func uriEncode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}
