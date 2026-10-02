// Package infomaniakdav implements controlled, read-only access to the CalDAV and CardDAV service of
// Infomaniak Sync (https://www.infomaniak.com/en/support/faq/2432/sync-your-contacts-and-calendars-across-
// all-your-devices). It lists the calendars and address books of one Infomaniak identity.
//
// The origin is fixed to https://sync.infomaniak.com and cannot be configured, and no redirect is ever
// followed, so the basic-auth credential never leaves that origin. Discovery follows the DAV chain: the
// current-user-principal of the root, then the calendar-home-set or addressbook-home-set of that principal,
// then one depth 1 PROPFIND of that home set. Every location the server names is untrusted: it must be on
// the fixed origin and, for a collection, a direct child of the discovered home set.
//
// A connection must list the collections it may reach as calendar/ID and addressbook/ID targets, where ID is
// the last path segment of the collection below its home set. Only listed collections are reported; a kind
// without any target lists an empty set without contacting Infomaniak. Display names, descriptions, and
// colors arrive from the provider and are untrusted data.
package infomaniakdav

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "infomaniakdav"

// The secret roles of one identity: the personal user name shown in the Infomaniak sync settings (for
// example AB12345) and an application password created for it.
const (
	roleUserID      = "user-id"
	roleAppPassword = "app-password"
)

const (
	// origin is the fixed Infomaniak Sync endpoint. It is deliberately not derived from the configuration.
	origin = "https://sync.infomaniak.com"
	// dataSensitivity classifies results as calendar and address book metadata of the identity.
	dataSensitivity = "infomaniak-dav-collections"
	methodPropfind  = "PROPFIND"
	defaultTimeout  = 30 * time.Second
	maxUserIDLen    = 64
	maxSecretLen    = 1024
)

// limiters holds the budget of every identity this process has used. Infomaniak documents no request budget
// for sync, so there is no proactive spacing.
var limiters = ratelimit.NewRegistry(0)

// transport carries every request. A nil value is Go's default transport; the package's tests replace it.
var transport http.RoundTripper

// Client binds one identity to the fixed Infomaniak Sync origin and the allow-list of its connection.
type Client struct {
	scope   scope
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the identity of one selected connection and returns a client for its scope.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	const op = "open"
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	userID, err := role(ctx, resolved, secrets, roleUserID)
	if err != nil {
		return nil, err
	}
	if !validUserID(userID) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Infomaniak user name is unusable"}
	}
	password, err := role(ctx, resolved, secrets, roleAppPassword)
	if err != nil {
		return nil, err
	}
	if !validPassword(password) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Infomaniak application password is unusable"}
	}
	pair := userID + ":" + password
	encoded := base64.StdEncoding.EncodeToString([]byte(pair))
	header := "Basic " + encoded
	if red != nil {
		red.Add(password, pair, encoded, header)
	}
	return &Client{
		scope: bound, auth: header, http: newHTTPClient(), limiter: limiters.For(userID + "\x00" + password),
	}, nil
}

func role(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, name string) (string, error) {
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, name)
	if err != nil {
		return "", err
	}
	return value.Secret, nil
}

func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// redirectRefusedError reports that a redirect was not followed. Its message names no location.
type redirectRefusedError struct{}

func (e *redirectRefusedError) Error() string {
	return "refused to follow a redirect from the Infomaniak sync service"
}

// newHTTPClient bounds every request in time and refuses every redirect.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return &redirectRefusedError{} },
	}
}

// propfind is the single request path of this provider and builds no method other than PROPFIND. The path
// comes from the fixed root or from segments that segmentsOf already validated.
func (c *Client) propfind(ctx context.Context, op string, segments []string, depth, body string) ([]resource, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Infomaniak", err)
	}
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		escaped[i] = url.PathEscape(segment)
	}
	path := "/"
	if len(escaped) > 0 {
		path = "/" + strings.Join(escaped, "/") + "/"
	}
	req, err := http.NewRequestWithContext(ctx, methodPropfind, origin+path, strings.NewReader(body))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("Depth", depth)

	response, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(op, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMultiStatus {
		return nil, statusError(op, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, invalidResponse(op, "the Infomaniak response could not be read within the size limit")
	}
	return parseMultiStatus(op, data)
}

// TestConnection performs the smallest authenticated read: the principal discovery request.
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
	if _, err := client.discoverPrincipal(ctx, "test connection"); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

func validBaseURL(raw string) error {
	if !strings.EqualFold(strings.TrimRight(strings.TrimSpace(raw), "/"), origin) {
		return errors.New("an Infomaniak DAV service connects to a fixed Infomaniak host; leave base_url out or " +
			"set it to " + origin)
	}
	return nil
}

// Register adds Infomaniak DAV metadata, its connection test, and its read operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Infomaniak Calendar and Contacts", DefaultBaseURL: origin, ValidateBaseURL: validBaseURL,
		DefaultPermissions: []config.Permission{config.PermissionRead},
		Description:        "Infomaniak Sync over CalDAV and CardDAV, calendars and address books of one identity listed",
		SecretRoles: []config.SecretRole{{
			Name: roleUserID,
			Description: "Personal user name of the Infomaniak sync identity, shown in the Infomaniak " +
				"synchronisation settings (for example AB12345)",
		}, {
			Name: roleAppPassword,
			Description: "Application password created in the Infomaniak Manager for this identity; it can be " +
				"revoked on its own and replaces the account password for DAV clients",
		}},
		Target: config.TargetMetadata{
			Label:    "calendars and address books",
			Required: true,
			Multiple: true,
			Description: "an allow-list of calendar/ID and addressbook/ID targets, where ID is the last path " +
				"segment of the collection below the home set; at least one is required, and only listed " +
				"collections are reported",
			Kinds: []config.TargetKind{{
				Name:        "calendar",
				Description: "one calendar by its last path segment; repeatable; no wildcards",
				Forms:       []string{"calendar/ID"},
			}, {
				Name:        "addressbook",
				Description: "one address book by its last path segment; repeatable; no wildcards",
				Forms:       []string{"addressbook/ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "List calendars and address books", Recommended: true,
			Description: "lists the allow-listed calendars and address books; reads no event or contact and changes nothing",
			Tools:       []string{calendarsList.ID, addressbooksList.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: calendarsList, Handler: capability.Handler(invokeCalendarsList)},
		capability.Operation{Descriptor: addressbooksList, Handler: capability.Handler(invokeAddressbooksList)},
	)
}

// statusError maps an HTTP status to a stable class. The provider body is never read into a message.
func statusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Infomaniak rejected the user name or the application password"}
	case http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this Infomaniak identity may not read this collection"}
	case http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "this Infomaniak identity does not hold this location"}
	case http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Infomaniak rate-limited the operation"}
	case http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Infomaniak sync is unavailable"}
	case http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Infomaniak did not answer in time"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Infomaniak rejected the operation (HTTP %d)", status)}
	}
}

func transportError(op string, err error) error {
	var refused *redirectRefusedError
	if errors.As(err, &refused) {
		return providerError(op, refused.Error())
	}
	return provider.Transport(op, "Infomaniak", err)
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

func validUserID(value string) bool {
	if value == "" || len(value) > maxUserIDLen {
		return false
	}
	for _, r := range value {
		if r == ':' || r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func validPassword(value string) bool {
	if len(value) < 8 || len(value) > maxSecretLen {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
