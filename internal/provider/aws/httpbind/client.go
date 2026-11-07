package httpbind

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awserr"
)

// DefaultResponseLimit caps a success response when Client.ResponseLimit is zero.
const DefaultResponseLimit = 4 << 20

// Codec is the body format of a REST protocol. restjson implements it; a restXml package would too.
type Codec interface {
	// Marshal serializes v, a value of the struct, document or payload shape s, into a whole body.
	Marshal(s *Shape, v any) ([]byte, error)
	// Unmarshal reads a non-empty body as a value of s. A struct comes back as map[string]any with the
	// Field.Key names. The second result says that a string was cut at its cap.
	Unmarshal(s *Shape, data []byte) (v any, cut bool, err error)
	// ContentType is the Content-Type of a body Marshal made; Accept is the Accept header of every request.
	ContentType() string
	Accept() string
	// ErrorCode returns the bare provider error code of a failed answer from its headers and its first
	// bytes, or empty when there is none. It never returns the provider's message.
	ErrorCode(header http.Header, body []byte) string
}

// Signer signs a request for the exact body that is sent. *sigv4.Signer satisfies it.
type Signer interface {
	Sign(req *http.Request, body []byte) error
}

// Doer sends a request. Callers pass a client that refuses redirects (provider.NoRedirectClient) or a fake.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client sends the calls of one REST protocol to one fixed endpoint. The host is the endpoint's, never a
// value of the request.
type Client struct {
	HTTP   Doer
	Signer Signer
	Codec  Codec
	// URL is the https endpoint from the endpoint table, for example https://email.eu-central-1.amazonaws.com.
	URL string
	// UserAgent is sent when set. It is not signed.
	UserAgent string
	// ResponseLimit caps the answer in bytes; zero means DefaultResponseLimit.
	ResponseLimit int64
}

// Meta is what the answer says about the exchange itself.
type Meta struct {
	// RequestID is AWS's request ID, kept only when it is a well-formed token.
	RequestID string
	// RetryAfter is the pause a rate-limited answer asked for; zero otherwise.
	RetryAfter time.Duration
	// Truncated is true when a decoded string was cut at its cap.
	Truncated bool
}

var requestID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func safeID(s string) string {
	if requestID.MatchString(s) {
		return s
	}
	return ""
}

// Do sends req, an encoded input of o, and decodes the answer into out (a pointer to a map; nil skips the
// result). op names the operation in errors. The returned error is nil on success; test it with != nil.
// Every 2xx status is a success; any other status goes through awserr, which maps the codes the models
// define and answers invalid-provider-response for a client error code it does not know.
func (c *Client) Do(ctx context.Context, op string, o *Operation, req Request, out *map[string]any) (Meta, *provider.Error) {
	endpoint, err := url.Parse(c.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return Meta{}, provider.Fail(op, "the AWS endpoint is not an https URL")
	}
	target := url.URL{Scheme: "https", Host: endpoint.Host, Path: req.Path, RawPath: req.RawPath, RawQuery: req.RawQuery}
	var reader io.Reader = http.NoBody
	if len(req.Body) > 0 {
		reader = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target.String(), reader)
	if err != nil {
		return Meta{}, provider.Fail(op, "the AWS request could not be built")
	}
	for name, values := range req.Header {
		httpReq.Header[name] = append([]string(nil), values...)
	}
	httpReq.Header.Set("Accept", c.Codec.Accept())
	if c.UserAgent != "" {
		httpReq.Header.Set("User-Agent", c.UserAgent)
	}
	if err := c.Signer.Sign(httpReq, req.Body); err != nil {
		return Meta{}, provider.Fail(op, "the AWS request could not be signed")
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Meta{}, provider.Transport(op, awserr.Subject, err)
	}
	defer resp.Body.Close()
	meta := Meta{RequestID: safeID(resp.Header.Get("X-Amzn-Requestid"))}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		code := c.Codec.ErrorCode(resp.Header, provider.ReadErrorBody(resp.Body))
		failure, hold := awserr.ClassifyStrict(op, resp.StatusCode, resp.Header, code, "")
		meta.RetryAfter = hold
		return meta, failure
	}
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = DefaultResponseLimit
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return meta, provider.InvalidResponse(op, "the AWS response could not be read within the size limit")
	}
	decoded, cut, err := o.Decode(c.Codec, resp.StatusCode, resp.Header, data)
	if errors.Is(err, ErrShape) {
		return meta, provider.Fail(op, "the AWS result description is invalid")
	}
	if err != nil {
		return meta, provider.InvalidResponse(op, "AWS returned an invalid response")
	}
	meta.Truncated = cut
	if out != nil {
		*out = decoded
	}
	return meta, nil
}
