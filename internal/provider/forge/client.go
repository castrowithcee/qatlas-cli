package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Client binds one access token to the origin of its instance and the rate limit that token shares. The
// targets of its connection are checked by the operation that opened it.
type Client struct {
	product *product
	origin  string
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// originOf accepts https://HOST, optionally with a port and a trailing slash, never with a path, user info, a
// query, or a fragment. An instance below a path prefix is not supported. The error never quotes the value.
func originOf(raw string) (string, error) {
	const reason = "a Forgejo or Gitea service needs its base URL as https://HOST, without a path, user, query, " +
		"or fragment"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New(reason)
	}
	return "https://" + strings.ToLower(parsed.Host), nil
}

// open checks the connection's base URL and targets, then resolves its token. Nothing is sent.
func open(ctx context.Context, p *product, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, provider.Fail(op, "no connection was selected")
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, provider.Fail(op, err.Error())
	}
	if _, err := allowlistOf(resolved); err != nil {
		return nil, err
	}
	token, err := resolveToken(ctx, p, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return &Client{product: p, origin: origin, auth: "token " + token,
		http: provider.NoRedirectClient(defaultTimeout, transport), limiter: limiters.For(token)}, nil
}

// resolveToken reads the token of the connection and registers it with the redactor.
func resolveToken(ctx context.Context, p *product, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (string, error) {
	if secrets == nil {
		return "", provider.Fail("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return "", err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return "", &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the " + p.name + " token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "token "+value.Secret)
	}
	return value.Secret, nil
}

// forbiddenQuery are query parameters no request carries: they would act as another user or authenticate
// through the URL.
var forbiddenQuery = []string{"sudo", "token", "access_token"}

// get sends one GET of a fixed API path, which the caller builds from constants and escaped, validated
// segments, and decodes the JSON answer into out. It returns the response headers for paging. A read is never
// retried.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) (http.Header, error) {
	_, header, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query, out)
	return header, err
}

// request is what a call adds to a plain GET: a fixed method and, for a change, a body the handler built from
// validated arguments. The zero value is a GET without a body. change marks a request that mutates the
// instance: a failure whose outcome is open then says so.
type request struct {
	method      string
	contentType string
	body        []byte
	// stream is a body of exactly length bytes that is sent as it is read, instead of body.
	stream io.Reader
	length int64
	change bool
}

// textAnswer receives a plain-text or HTML answer: data keeps at most limit bytes, and truncated reports that
// the instance sent more.
type textAnswer struct {
	limit     int
	data      []byte
	truncated bool
}

// post sends one POST of a fixed API path with a JSON or plain-text body that the caller built from validated
// arguments, and reads the answer as text. A POST is never retried.
func (c *Client) post(ctx context.Context, op, path string, req request, answer *textAnswer) error {
	req.method = http.MethodPost
	_, _, err := exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, nil, req, answer)
	return err
}

// uncertain is appended to a failure of a change whose request may have reached the instance: the change may
// have been applied although no confirmation arrived. A change is never repeated by Qatlas itself.
const uncertain = "; the change may have been applied, read the current state before repeating it"

// change sends exactly one mutating request of a fixed method (POST, PUT, PATCH, or DELETE) and API path, which the
// caller builds from constants and escaped, validated segments. body is encoded as JSON and must be built from
// validated arguments only; nil sends none. out receives the JSON answer, nil ignores an answer such as the
// empty one of a DELETE. The request is never retried. A failure whose outcome is open, such as a timeout, a
// reset connection, a 5xx status, or an unreadable answer, carries the uncertain note; a refusal that proves
// the change was not applied does not.
func (c *Client) change(ctx context.Context, op, method, path string, body any, out any) error {
	_, err := c.changeQuery(ctx, op, method, path, nil, body, out)
	return err
}

// changeQuery is change with validated query values and the status of the answer, zero when none arrived. The
// query values are built from enumerated arguments only.
func (c *Client) changeQuery(ctx context.Context, op, method, path string, query url.Values, body any,
	out any) (int, error) {
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch &&
		method != http.MethodDelete {
		return 0, provider.Fail(op, "the request method is not one of this provider")
	}
	spec := request{method: method, change: true}
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, provider.Fail(op, "the request could not be built")
		}
		spec.contentType, spec.body = "application/json", encoded
	}
	status, _, err := exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query, spec, out)
	return status, err
}

// transferTimeout bounds the whole transfer of one file upload or download, which the request timeout would
// cut off.
const transferTimeout = time.Hour

// sendFile sends exactly one multipart POST of a fixed API path that carries file in the form member field
// under fileName, streamed as it is read, size bytes long. The body is framed by hand, so its length is known
// and the file is never held in memory. It is a change like change: never retried, and a failure whose
// outcome is open carries the uncertain note.
func (c *Client) sendFile(ctx context.Context, op, path string, query url.Values, field, fileName string,
	file io.Reader, size int64, out any) error {
	var head bytes.Buffer
	form := multipart.NewWriter(&head)
	if _, err := form.CreateFormFile(field, fileName); err != nil {
		return provider.Fail(op, "the request could not be built")
	}
	tail := "\r\n--" + form.Boundary() + "--\r\n"
	spec := request{method: http.MethodPost, contentType: form.FormDataContentType(), change: true,
		stream: io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(file, size), strings.NewReader(tail)),
		length: int64(head.Len()) + size + int64(len(tail))}
	long := *c.http
	long.Timeout = transferTimeout
	_, _, err := exchange(ctx, &long, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query, spec, out)
	return err
}

// fetchFile sends one GET of a fixed path of the instance's own origin outside the API, built by the caller
// from constants and validated segments, and streams the answer of exactly size bytes into to. The token goes
// to that origin only. A redirect is refused, never followed: the instance answers with one when it serves
// the file from another address.
func (c *Client) fetchFile(ctx context.Context, op, path string, size int64, to io.ReaderFrom) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#") {
		return provider.Fail(op, "the request path is not one of this provider")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, c.product.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return provider.Fail(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("User-Agent", "qatlas-cli")
	long := *c.http
	long.Timeout = transferTimeout
	response, err := long.Do(req)
	if err != nil {
		return provider.Transport(op, c.product.name, err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name +
			" serves this file from another address, such as external storage; Qatlas does not follow it"}
	case response.StatusCode != http.StatusOK:
		return refusal(op, c.product, c.limiter, response)
	case response.ContentLength >= 0 && response.ContentLength != size:
		return provider.InvalidResponse(op, c.product.name+" announced another size than the asset has")
	}
	body := &trackedReader{Reader: response.Body}
	if _, err := to.ReadFrom(body); err != nil {
		if body.err != nil {
			return provider.Transport(op, c.product.name, body.err)
		}
		return err
	}
	return nil
}

// trackedReader remembers a failure of the source, so it can be told from a failure of the local file.
type trackedReader struct {
	io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.Reader.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

// unsure marks the failure of a change whose outcome is open.
func unsure(failure *provider.Error) *provider.Error {
	failure.Message += uncertain
	return failure
}

// probe sends one GET of a fixed API path whose answer has no body and reports whether the instance answered
// 2xx. Only 404 counts as a plain no; every other refusal stays a failure, and a 404 hides nothing beyond what
// the caller documents.
func (c *Client) probe(ctx context.Context, op, path string) (bool, error) {
	status, _, err := exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, nil,
		request{}, nil)
	if err == nil {
		return true, nil
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// getText sends one GET of a fixed API path and reads the answer as text.
func (c *Client) getText(ctx context.Context, op, path string, answer *textAnswer) error {
	_, _, err := exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, nil, request{},
		answer)
	return err
}

// send is a GET of a fixed path with a JSON answer; an empty auth sends it without a credential. The status
// is that of the answer, zero when none arrived.
func send(ctx context.Context, client *http.Client, limiter *ratelimit.Limiter, p *product, root, auth, op, path string,
	query url.Values, out any) (int, http.Header, error) {
	return exchange(ctx, client, limiter, p, root, auth, op, path, query, request{}, out)
}

// exchange is the one place that builds and sends a request. Only GET, POST, PUT, PATCH, and DELETE exist, a body
// never on GET. out is a *textAnswer for a text answer, a JSON target otherwise, and nil for an answer that is
// not read.
func exchange(ctx context.Context, client *http.Client, limiter *ratelimit.Limiter, p *product, root, auth, op,
	path string, query url.Values, spec request, out any) (int, http.Header, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return 0, nil, provider.Fail(op, "the request path is not one of this provider")
	}
	method := spec.method
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return 0, nil, provider.Fail(op, "the request method is not one of this provider")
	}
	hasBody := spec.body != nil || spec.stream != nil
	if method == http.MethodGet && hasBody {
		return 0, nil, provider.Fail(op, "the request method is not one of this provider")
	}
	for key := range query {
		for _, forbidden := range forbiddenQuery {
			if strings.EqualFold(key, forbidden) {
				return 0, nil, provider.Fail(op, "the request carries a parameter this provider never sends")
			}
		}
	}
	if err := limiter.Wait(ctx); err != nil {
		return 0, nil, provider.Waited(op, p.name, err)
	}
	target := root + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	switch {
	case spec.stream != nil:
		body = spec.stream
	case spec.body != nil:
		body = bytes.NewReader(spec.body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, provider.Fail(op, "the request could not be built")
	}
	if spec.stream != nil {
		req.ContentLength = spec.length
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	text, isText := out.(*textAnswer)
	if isText {
		req.Header.Set("Accept", "text/html, text/plain")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if hasBody {
		req.Header.Set("Content-Type", spec.contentType)
	}
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := client.Do(req)
	if err != nil {
		failure := provider.Transport(op, p.name, err)
		if spec.change && failure.MayHaveArrived() {
			failure = unsure(failure)
		}
		return 0, nil, failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := refusal(op, p, limiter, response)
		if spec.change && response.StatusCode >= 500 {
			failure = unsure(failure)
		}
		return response.StatusCode, nil, failure
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return response.StatusCode, response.Header, nil
	}
	if isText {
		data, err := io.ReadAll(io.LimitReader(response.Body, int64(text.limit)+1))
		if err != nil {
			failure := provider.InvalidResponse(op, "the "+p.name+" response could not be read")
			if spec.change {
				failure = unsure(failure)
			}
			return response.StatusCode, nil, failure
		}
		text.truncated = len(data) > text.limit
		if text.truncated {
			data = data[:text.limit]
		}
		text.data = data
		return response.StatusCode, response.Header, nil
	}
	if failure := provider.ReadJSON(op, p.name, response.Body, maxResponseBytes, out); failure != nil {
		if spec.change {
			failure = unsure(failure)
		}
		return response.StatusCode, nil, failure
	}
	return response.StatusCode, response.Header, nil
}

// refusal maps an HTTP status to a stable class; the provider body is never read into the message. 403 and
// 404 are both "not reachable": an instance answers 404 instead of 403 to a token bound to other repositories
// or to public ones only, so neither proves that a resource exists or not.
func refusal(op string, p *product, limiter *ratelimit.Limiter, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: p.name + " does not show this " +
			"resource to this token; it may not exist, or the token's scopes or repository binding do not reach it"}
	case http.StatusTooManyRequests:
		limiter.HoldFor(min(provider.RetryAfter(response.Header), maxHold))
	}
	return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject: p.name,
		Auth:    p.name + " rejected the token; check that it is valid and not expired",
	})
}

func invalidArgument(message string) error { return &provider.InvalidRequestError{Message: message} }

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	if len(value) > maxStringLength {
		value = value[:maxStringLength]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

// object is one provider object as raw members, read by the projections.
type object map[string]json.RawMessage

// truncateText cuts a text to at most limit bytes without splitting a character.
func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (o object) text(key string) (string, bool) { return o.textUpTo(key, maxStringLength) }

// textUpTo reads a string member and cuts it at limit bytes.
func (o object) textUpTo(key string, limit int) (string, bool) {
	var value string
	if json.Unmarshal(o[key], &value) != nil {
		return "", false
	}
	return truncateText(value, limit), true
}

func (o object) integer(key string) (int64, bool) {
	var value int64
	if json.Unmarshal(o[key], &value) != nil {
		return 0, false
	}
	return value, true
}

func (o object) flag(key string) (bool, bool) {
	var value bool
	if json.Unmarshal(o[key], &value) != nil {
		return false, false
	}
	return value, true
}

func (o object) child(key string) (object, bool) {
	var value object
	if json.Unmarshal(o[key], &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}
