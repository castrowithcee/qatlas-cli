// Package awsquery implements the AWS query protocol: a form-encoded POST with Action and Version, an XML
// answer <Action>Response/<Action>Result, and the <ErrorResponse> error shape. It is the shared runtime of
// the services that speak it (STS and IAM). An operation is bound by data, not by code: a Shape describes
// its input and output with the Smithy traits xmlName, xmlFlattened and the list and map forms, Encode turns
// arguments into form parameters, Call.Output decodes the answer, and a Pager carries NextToken and
// Marker/IsTruncated pagination in the Qatlas cursor.
package awsquery

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awserr"
)

const (
	// ContentType is the media type of every query-protocol request body.
	ContentType = "application/x-www-form-urlencoded; charset=utf-8"
	// DefaultResponseLimit caps a success response when Client.ResponseLimit is zero.
	DefaultResponseLimit = 4 << 20
)

// Signer signs a request for the exact body that is sent. *sigv4.Signer satisfies it.
type Signer interface {
	Sign(req *http.Request, body []byte) error
}

// Doer sends a request. Callers pass a client that refuses redirects (provider.NoRedirectClient) or a fake.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client sends query-protocol calls to one fixed endpoint.
type Client struct {
	HTTP   Doer
	Signer Signer
	// URL is the https endpoint from the endpoint table, for example https://sts.eu-central-1.amazonaws.com.
	URL string
	// UserAgent is sent when set. It is not signed.
	UserAgent string
	// ResponseLimit caps the answer in bytes; zero means DefaultResponseLimit.
	ResponseLimit int64
}

// Call is one operation: the action, the API version of the service model and scalar parameters.
type Call struct {
	Action  string
	Version string
	Params  map[string]string
	// Output, when set, decodes <Action>Result by its shape into the map out points to (a *map[string]any).
	// Without it, out is a pointer to a struct with xml tags.
	Output *Shape
}

// Meta is what the answer says about the exchange itself.
type Meta struct {
	// RequestID is AWS's request ID, kept only when it is a well-formed token. It helps CloudTrail and
	// support, and carries no secret.
	RequestID string
	// RetryAfter is the pause a rate-limited answer asked for; zero otherwise.
	RetryAfter time.Duration
	// Truncated is true when a decoded string was cut at its cap (see Shape.MaxLen).
	Truncated bool
}

var requestID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func safeID(s string) string {
	if requestID.MatchString(s) {
		return s
	}
	return ""
}

// Do sends the call and decodes <Action>Result into out (a pointer to a struct with xml tags; nil skips the
// result). op names the operation in errors. The returned error is nil on success; test it with != nil.
func (c *Client) Do(ctx context.Context, op string, call Call, out any) (Meta, *provider.Error) {
	endpoint, err := url.Parse(c.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return Meta{}, provider.Fail(op, "the AWS endpoint is not an https URL")
	}
	body, err := EncodeBody(call.Action, call.Version, call.Params)
	if err != nil {
		return Meta{}, provider.Fail(op, "the AWS request could not be built: "+err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return Meta{}, provider.Fail(op, "the AWS request could not be built")
	}
	req.Header.Set("Content-Type", ContentType)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if err := c.Signer.Sign(req, body); err != nil {
		return Meta{}, provider.Fail(op, "the AWS request could not be signed")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Meta{}, provider.Transport(op, awserr.Subject, err)
	}
	defer resp.Body.Close()
	meta := Meta{RequestID: safeID(resp.Header.Get("X-Amzn-Requestid"))}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.failure(op, resp, meta)
	}
	limit := c.ResponseLimit
	if limit <= 0 {
		limit = DefaultResponseLimit
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return meta, provider.InvalidResponse(op, "the AWS response could not be read within the size limit")
	}
	var target *map[string]any
	if call.Output != nil && out != nil {
		var ok bool
		if target, ok = out.(*map[string]any); !ok {
			return meta, provider.Fail(op, "the AWS result description needs a map to decode into")
		}
	}
	id, truncated, derr := decodeResult(data, call.Action, out, call.Output, target)
	if id = safeID(id); id != "" {
		meta.RequestID = id
	}
	meta.Truncated = truncated
	if errors.Is(derr, ErrShape) {
		return meta, provider.Fail(op, "the AWS result description is invalid")
	}
	if derr != nil {
		return meta, provider.InvalidResponse(op, "AWS returned an invalid response")
	}
	return meta, nil
}

// failure classifies a non-2xx answer. The <Message> element is deliberately never decoded.
func (c *Client) failure(op string, resp *http.Response, meta Meta) (Meta, *provider.Error) {
	var parsed struct {
		XMLName xml.Name `xml:"ErrorResponse"`
		Error   struct {
			Type string `xml:"Type"`
			Code string `xml:"Code"`
		} `xml:"Error"`
		RequestID string `xml:"RequestId"`
	}
	// A body that is not an <ErrorResponse> (a proxy page, a truncated answer) leaves the code empty and the
	// status alone decides.
	_ = xml.Unmarshal(provider.ReadErrorBody(resp.Body), &parsed)
	if id := safeID(parsed.RequestID); id != "" {
		meta.RequestID = id
	}
	failure, hold := awserr.Classify(op, resp.StatusCode, resp.Header, awserr.NormalizeCode(parsed.Error.Code), parsed.Error.Type)
	meta.RetryAfter = hold
	return meta, failure
}

// decodeResult walks <Action>Response, decodes <Action>Result into out (by shape into target, else as an
// XML-tagged struct) and returns the request ID of <ResponseMetadata>. An operation without output members
// has no Result element, which is fine.
func decodeResult(data []byte, action string, out any, shape *Shape, target *map[string]any) (id string, truncated bool, err error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var root xml.StartElement
	for {
		tok, err := d.Token()
		if err != nil {
			return "", false, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			root = se
			break
		}
	}
	if root.Name.Local != action+"Response" {
		return "", false, errUnexpectedRoot
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return id, truncated, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case action + "Result":
				switch {
				case shape != nil:
					var m map[string]any
					m, truncated, err = bindResult(d, t, shape)
					if err == nil && target != nil {
						*target = m
					}
				case out == nil:
					err = d.Skip()
				default:
					err = d.DecodeElement(out, &t)
				}
			case "ResponseMetadata":
				var meta struct {
					RequestID string `xml:"RequestId"`
				}
				err = d.DecodeElement(&meta, &t)
				id = meta.RequestID
			default:
				err = d.Skip()
			}
			if err != nil {
				return id, truncated, err
			}
		case xml.EndElement:
			return id, truncated, nil
		}
	}
}

var errUnexpectedRoot = errors.New("awsquery: unexpected response root element")
