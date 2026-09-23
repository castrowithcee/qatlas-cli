// Package todoist implements controlled, read-only access to one personal Todoist account through the
// official Todoist API v1.
//
// A connection binds one personal API token either to an explicit set of projects or, after a deliberate
// choice, to the whole account through the * wildcard. A project connection answers only with tasks,
// sections, comments, and reminders of its projects: no argument names a scope, a project or task argument
// only selects inside it, and every result is checked against it again before it is returned. A task filter
// expression reaches Todoist only through the dedicated filter tool, and even its results are held to the
// same projects.
//
// Every list reads exactly one Todoist page per request and hands the caller an opaque cursor for the
// next one; no follow-up page is ever loaded unasked. The cursor is bound to the connection's scope and the
// filters that produced it.
//
// Task contents, descriptions, comments, and names arrive from the provider and are treated as untrusted
// data: they are normalised into a stable Qatlas shape, passed through the output encoders, and never
// rendered, executed, or stored.
package todoist

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "todoist"

// apiRoot is the fixed root of the official Todoist API v1. The provider accepts no other origin: a
// personal token belongs to this one service, so a second origin could only be a mistake or an
// exfiltration route.
const apiRoot = "https://api.todoist.com/api/v1"

// roleToken is the single secret role a Todoist credential must supply. It is used as a bearer token.
const roleToken = "token"

// dataSensitivity classifies results as personal task data of the configured Todoist account.
const dataSensitivity = "todoist-task-data"

// wildcard is the explicit target that binds the whole account.
const wildcard = "*"

// Bounds of one request. A page holds 50 entries unless the caller asks for fewer or more, and never more
// than the 200 Todoist serves at once.
const (
	defaultLimit     = 50
	maxLimit         = 200
	maxResponseBytes = 4 << 20
	defaultTimeout   = 30 * time.Second
	cursorBinding    = 12
	maxCursorLength  = 1024
	maxIDLength      = 64
)

// minInterval spaces the requests that share one token. Todoist counts its request budget per user.
const minInterval = 250 * time.Millisecond

// maxHold bounds how long a reported rate-limit wait may delay the next request of this process.
const maxHold = time.Minute

// limiters holds the rate-limit budget of every token this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

// scope is the complete project boundary of one connection. A wildcard is explicit configuration; an
// empty target never becomes the whole account by accident.
type scope struct {
	all      bool
	projects []string
}

// allows reports whether a project belongs to this connection.
func (s scope) allows(projectID string) bool {
	if s.all {
		return true
	}
	for _, id := range s.projects {
		if id == projectID {
			return true
		}
	}
	return false
}

// single returns the one project of a connection bound to exactly one.
func (s scope) single() (string, bool) {
	if !s.all && len(s.projects) == 1 {
		return s.projects[0], true
	}
	return "", false
}

func (s scope) String() string {
	if s.all {
		return wildcard
	}
	sorted := append([]string(nil), s.projects...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// selectProject checks a project argument against the scope before any request: an empty argument
// selects nothing, and a project outside the connection is refused.
func (s scope) selectProject(projectID string) error {
	if projectID != "" && !s.allows(projectID) {
		return outsideScope("project")
	}
	return nil
}

func scopeOf(resolved *config.Resolved) (scope, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	return parseScope(values)
}

// parseScope reads the target list of one connection: the * wildcard alone, or one or more project IDs.
// No error quotes a value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("a Todoist connection needs one or more project IDs or the * wildcard")
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == wildcard {
		return scope{all: true}, nil
	}
	bound := scope{projects: make([]string, 0, len(values))}
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == wildcard {
			return scope{}, errors.New("the Todoist * wildcard must be the only configured target")
		}
		if err := validateTarget(trimmed); err != nil {
			return scope{}, err
		}
		if seen[trimmed] {
			return scope{}, errors.New("the Todoist project list names a project more than once")
		}
		seen[trimmed] = true
		bound.projects = append(bound.projects, trimmed)
	}
	return bound, nil
}

// validateTarget checks one configured target: the wildcard or one project ID.
func validateTarget(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == wildcard {
		return nil
	}
	if !validID(trimmed) {
		return errors.New("a Todoist target must be a project ID of letters and digits, or the * wildcard")
	}
	return nil
}

// validID mirrors idPattern: the letters and digits of a Todoist API v1 identifier.
func validID(value string) bool {
	if value == "" || len(value) > maxIDLength {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// validText refuses control characters and padding in a free text argument such as a search or a label
// name, and bounds its length.
func validText(value string, maxRunes int) bool {
	if value == "" || strings.TrimSpace(value) != value || len([]rune(value)) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, invalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxLimit))
	}
	return limit, nil
}

// fingerprint binds a cursor to the tool, the connection scope, and the normalised filters of the request
// that produced it.
func fingerprint(parts ...any) []byte {
	encoded, _ := json.Marshal(parts)
	sum := sha256.Sum256(encoded)
	return sum[:cursorBinding]
}

// encodeCursor wraps a Todoist cursor into the opaque, filter-bound Qatlas cursor. It carries the batch
// size of the first batch, because a Todoist cursor belongs to the parameters that produced it.
func encodeCursor(binding []byte, limit int, next string) string {
	raw := append(append([]byte(nil), binding...), byte(limit))
	return base64.RawURLEncoding.EncodeToString(append(raw, next...))
}

// decodeCursor returns the batch size and the Todoist cursor a Qatlas cursor continues with, or the given
// limit and the empty string for the first batch. A cursor that is malformed or belongs to other filters,
// another tool, or another scope is refused.
func decodeCursor(binding []byte, cursor string, limit int) (int, string, error) {
	if cursor == "" {
		return limit, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if len(cursor) > maxCursorLength || err != nil || len(decoded) <= cursorBinding+1 ||
		!bytes.Equal(decoded[:cursorBinding], binding) {
		return 0, "", invalidRequest("cursor is not a next_cursor of this list")
	}
	size, next := int(decoded[cursorBinding]), string(decoded[cursorBinding+1:])
	if size < 1 || size > maxLimit || !validProviderCursor(next) {
		return 0, "", invalidRequest("cursor is not a next_cursor of this list")
	}
	return size, next, nil
}

// validProviderCursor keeps a cursor to the characters Todoist uses for its own, so a cursor always stays
// one query value.
func validProviderCursor(value string) bool {
	if value == "" || len(value) > maxCursorLength {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// Client binds one Todoist token to the scope of its connection and to the rate limit that token shares.
type Client struct {
	scope   scope
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the token of one selected connection and returns a client for its configured scope.
func Open(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches Todoist.
func open(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if !isAPIRoot(resolved.BaseURL) {
		return nil, providerError("open", "a Todoist service must use the official API root "+apiRoot)
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Todoist token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{scope: bound, auth: "Bearer " + value.Secret, http: newHTTPClient(), limiter: lim}, nil
}

// isAPIRoot reports whether a configured base URL names the official API root. An empty value and a
// trailing slash are the only variations a configuration may carry.
func isAPIRoot(raw string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return trimmed == "" || trimmed == apiRoot
}

// transport carries every Todoist request. A nil value is Go's default transport; the package's own tests
// replace it with a local test server.
var transport http.RoundTripper

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
		// The token travels in the Authorization header, so no redirect is followed: a redirect could only
		// move a credential to a place the user never configured.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestConnection performs the smallest authenticated read of the configured scope: each configured
// project, or one project of the account for a wildcard connection. Success proves that this token can see
// the scope; plan-dependent reads such as reminders are checked by Todoist on each request.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	const op = "test connection"
	if client.scope.all {
		var page pageJSON
		err = client.get(ctx, op, "/projects", url.Values{"limit": {"1"}}, &page)
	} else {
		for _, id := range client.scope.projects {
			var project rawProject
			if err = client.get(ctx, op, "/projects/"+url.PathEscape(id), nil, &project); err != nil {
				break
			}
		}
	}
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// pageJSON is one page of a paginated Todoist list. Completed tasks arrive under items instead of results.
type pageJSON struct {
	Results    []json.RawMessage `json:"results"`
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// page reads exactly one page of a paginated Todoist list and returns its entries and the Todoist cursor of
// the next page, empty at the end. It never follows that cursor itself.
func (c *Client) page(ctx context.Context, op, path string, query url.Values, limit int,
	after string) ([]json.RawMessage, string, error) {
	values := url.Values{}
	for key, value := range query {
		values[key] = value
	}
	values.Set("limit", strconv.Itoa(limit))
	if after != "" {
		values.Set("cursor", after)
	}
	var page pageJSON
	if err := c.get(ctx, op, path, values, &page); err != nil {
		return nil, "", err
	}
	entries := page.Results
	if entries == nil {
		entries = page.Items
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	if next != "" && !validProviderCursor(next) {
		return nil, "", &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Todoist announced a further page with an unusable cursor"}
	}
	return entries, next, nil
}

// get performs one bounded read below the API root.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) error {
	endpoint := apiRoot + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return c.do(ctx, op, http.MethodGet, endpoint, nil, "", out)
}

// syncRead performs one read-only request of the Sync endpoint for the named resource types. It carries no
// command, so nothing can change through it.
func (c *Client) syncRead(ctx context.Context, op string, resourceTypes []string, out any) error {
	types, err := json.Marshal(resourceTypes)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	form := url.Values{"sync_token": {"*"}, "resource_types": {string(types)}}
	return c.do(ctx, op, http.MethodPost, apiRoot+"/sync", []byte(form.Encode()),
		"application/x-www-form-urlencoded", out)
}

// do sends one request with the shared authentication and size rules and decodes the answer.
func (c *Client) do(ctx context.Context, op, method, endpoint string, payload []byte, contentType string,
	out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return &provider.Error{Class: provider.ClassTimeout, Op: op,
			Message: "the request ended while it waited for the Todoist rate limit"}
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "Todoist", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the Todoist response could not be read within the size limit"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op)
	}
	return nil
}

// errorJSON is the part of a Todoist error body this provider inspects. It is read for classification only
// and never copied, because Todoist may echo request values into it.
type errorJSON struct {
	Tag   string `json:"error_tag"`
	Error string `json:"error"`
	Extra struct {
		Argument   string `json:"argument"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error_extra"`
}

// Messages of the refusals a test or a tool relies on to tell the classes apart.
const (
	notFoundMessage   = "Todoist does not hold this resource or does not show it to this token"
	planMessage       = "the Todoist plan of this account does not include this feature"
	permissionMessage = "this Todoist token may not read this resource"
)

// statusError maps an HTTP status to a stable class. A refusal because of the account's plan stays a
// permission failure with a message of its own, apart from a rejected token, a rate limit, and a missing
// resource.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	status := response.StatusCode
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var detail errorJSON
	_ = json.Unmarshal(snippet, &detail)

	switch {
	case status == http.StatusUnauthorized:
		// Retry-After on a rejected token is backoff metadata only; the same token must not be retried.
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Todoist rejected the token"}
	case status == http.StatusTooManyRequests:
		retry := retryAfter(response.Header, detail.Extra.RetryAfter)
		c.limiter.HoldFor(capHold(retry))
		message := "Todoist rate-limited the operation"
		if retry > 0 {
			message += fmt.Sprintf("; retry after %d seconds", int((retry+time.Second-1)/time.Second))
		}
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: message}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: notFoundMessage}
	case status >= 400 && status < 500 && planLimited(detail):
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: planMessage}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: permissionMessage}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Todoist answered with a redirect, which Qatlas does not follow"}
	case status == http.StatusBadRequest && detail.Extra.Argument == "cursor":
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Todoist no longer accepts this cursor; start the list again without one"}
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Todoist rejected the request as invalid"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Todoist did not answer in time"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: fmt.Sprintf("Todoist rejected the operation (HTTP %d)", status)}
}

// planLimited recognises a refusal because the account's plan lacks a feature, such as reminders on a
// free plan. Todoist names it in its error tag or error text as a premium or plan restriction.
func planLimited(detail errorJSON) bool {
	for _, text := range []string{detail.Tag, detail.Error} {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "premium") || strings.Contains(lower, "plan") ||
			strings.Contains(lower, "upgrade") {
			return true
		}
	}
	return false
}

// retryAfter reads how long Todoist asks a client to wait: Retry-After in seconds, or retry_after of the
// error body. Zero means Todoist named no time.
func retryAfter(header http.Header, fromBody int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if fromBody > 0 {
		return time.Duration(fromBody) * time.Second
	}
	return 0
}

func capHold(hold time.Duration) time.Duration {
	if hold > maxHold {
		return maxHold
	}
	return hold
}

func invalidResponse(op string) *provider.Error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Todoist returned an invalid response"}
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// outsideScope refuses a resource of a project this connection is not bound to. It names the kind of
// resource and never its identifier or content.
func outsideScope(kind string) error {
	return invalidRequest("the " + kind + " is not in a project of this connection")
}

// validToken keeps an obviously unusable value out of a request. The real check is Todoist's.
func validToken(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		// A header value may not carry control characters, and a Todoist token never does.
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}
