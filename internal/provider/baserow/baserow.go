// Package baserow implements controlled access to the tables, fields, and rows of a Baserow
// instance, Baserow Cloud or self-hosted, through its REST API with a database token
// (baserow.io/user-docs/database-api, checked 2026-09-30, not against a live instance).
//
// The token is sent as "Authorization: Token ...". A database token carries create, read, update, and
// delete rights per workspace, database, or table; Qatlas adds its own boundary, the table allow-list of the
// connection (table/ID entries or *). A table ID outside that allow-list is refused before the credential is
// resolved and before any request is sent, and the refusal never names the table. Every request is built from
// fixed paths, fixed methods, and validated integers, never from a free URL or method of an agent. The body of
// a row change carries only cell values whose field names the table's schema confirms as writable.
//
// A row change is one request without a retry; a failure that leaves its result open is reported as uncertain.
//
// Link fields (link_row) into a table outside the allow-list are reduced to row IDs: their display values
// are dropped. A link field whose other table cannot be determined counts as outside. Lookup and formula
// fields can carry values of other tables and are passed through as the instance reports them; this is a
// documented boundary, not a masked one.
//
// Every value of a response is provider data and untrusted: strings, arrays, objects, and nesting are
// bounded, a response is read up to a size limit, and provider texts never reach an error message.
package baserow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "baserow"

// roleDatabaseToken is the single secret role: a Baserow database token.
const roleDatabaseToken = "database-token"

const (
	cloudOrigin     = "https://api.baserow.io"
	schemaSensitive = "baserow-schema"
	rowsSensitive   = "baserow-rows"
	maxResponseSize = 4 << 20
	defaultTimeout  = 30 * time.Second
	maxStringLength = 512
)

var limiters = ratelimit.NewRegistry(0)

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// Client binds one database token to the origin and the table scope of its connection.
type Client struct {
	origin  string
	token   string
	scope   scope
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the database token of one selected connection and returns a client for its origin.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	const op = "open"
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleDatabaseToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Baserow database token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Token "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{origin: origin, token: value.Secret, scope: bound, http: newHTTPClient(), limiter: lim}, nil
}

// originOf accepts an https URL of a Baserow instance: Cloud or self-hosted, optionally below an installation
// path, never with user info, a query, or a fragment. The fixed API paths are appended to it.
func originOf(raw string) (string, error) {
	const reason = "a Baserow service needs a usable https URL, without user, query, or fragment"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New(reason)
	}
	return "https://" + parsed.Host + strings.TrimSuffix(parsed.EscapedPath(), "/"), nil
}

// newHTTPClient never follows a redirect: the token must not travel to a host the connection is not bound to.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get sends one bounded GET request and decodes its JSON answer into out.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Baserow", err)
	}
	endpoint := c.origin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "Baserow", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return invalidResponse(op, "the Baserow response could not be read within the size limit")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, "Baserow returned an invalid response")
	}
	return nil
}

// statusError maps an HTTP status to a stable class; the provider body is never read into the message.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	return c.statusErrorFor(op, response, "read")
}

// statusErrorFor is statusError for an operation that needs the given token right: read, create, update, or
// delete. A forbidden answer names that right, never the provider's text.
func (c *Client) statusErrorFor(op string, response *http.Response, right string) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSize))
	status := response.StatusCode
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Baserow rejected the database token"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this database token may not " + right + " this resource; check its " + right +
				" right for the table"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Baserow does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(retryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Baserow rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Baserow is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Baserow did not answer in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Baserow answered with a redirect, which Qatlas does not follow for this request"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: "Baserow rejected the operation (HTTP " + strconv.Itoa(status) + ")"}
}

func retryAfter(header http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

func invalidRequest(message string) error { return &application.InvalidRequestError{Message: message} }

// validToken keeps an obviously unusable value out of a request header; the real check is Baserow's.
func validToken(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// boundString keeps an oversized provider string out of a result without interpreting it.
func boundString(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func bounded(value string) string { return boundString(value, maxStringLength) }

// TestConnection performs the smallest safe authenticated read: the token's table list.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	var tables []json.RawMessage
	if err := client.get(ctx, "test connection", allTablesPath, nil, &tables); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds the provider metadata, its connection test, its five read operations, and its four row
// changes.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Baserow", DefaultBaseURL: cloudOrigin,
		Description:        "Open-source no-code database, tables, fields, and rows through a database token",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleDatabaseToken,
			Description: "Baserow database token, created under the account settings with the read right, and the " +
				"create, update, or delete right for changes, on the workspace, database, or tables this " +
				"connection may reach; it is sent as " +
				"'Authorization: Token ...'",
		}},
		Target: config.TargetMetadata{
			Label:    "table",
			Required: true,
			Multiple: true,
			Wildcard: "*",
			WildcardWarning: "All tables the database token may read are exposed to the agent. " +
				"Use a table allow-list when not all of them are required.",
			Description: "one or more tables as an allow-list, or * for every table the token reads",
			Kinds: []config.TargetKind{{
				Name:        "table",
				Description: "a table whose fields and rows this connection may read",
				Forms:       []string{"table/TABLE_ID", "*"},
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read tables and rows", Recommended: true,
			Description: "lists tables and fields and reads rows; changes nothing",
			Tools:       []string{tablesList.ID, fieldsList.ID, rowsList.ID, rowsGet.ID, rowsSearch.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: tablesList, Handler: capability.Handler(invokeTablesList)},
		capability.Operation{Descriptor: fieldsList, Handler: capability.Handler(invokeFieldsList)},
		capability.Operation{Descriptor: rowsList, Handler: capability.Handler(invokeRowsList)},
		capability.Operation{Descriptor: rowsGet, Handler: capability.Handler(invokeRowsGet)},
		capability.Operation{Descriptor: rowsSearch, Handler: capability.Handler(invokeRowsSearch)},
		capability.Operation{Descriptor: rowsCreate, Handler: capability.Handler(invokeRowsCreate)},
		capability.Operation{Descriptor: rowsUpdate, Handler: capability.Handler(invokeRowsUpdate)},
		capability.Operation{Descriptor: rowsDelete, Handler: capability.Handler(invokeRowsDelete)},
		capability.Operation{Descriptor: rowsMove, Handler: capability.Handler(invokeRowsMove)},
		capability.Operation{Descriptor: rowsBatchCreate, Handler: capability.Handler(invokeRowsBatchCreate)},
		capability.Operation{Descriptor: rowsBatchUpdate, Handler: capability.Handler(invokeRowsBatchUpdate)},
		capability.Operation{Descriptor: rowsBatchDelete, Handler: capability.Handler(invokeRowsBatchDelete)},
	)
}
