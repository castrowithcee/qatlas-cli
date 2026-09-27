// Package infomaniakdrive implements controlled, read-only access to the Infomaniak kDrive REST API: it
// lists the drives of one bound Infomaniak account, lists the children of one folder page by page, reads
// the metadata of one file or folder, and reads bounded file content. No write, upload, share, rename,
// move, or trash operation is exposed.
//
// Infomaniak groups many products behind one account model where a single API token can reach every
// account and every kDrive its owner administers, which matters for a person who holds tokens of several
// customers. A connection therefore binds exactly one Infomaniak account (account/ACCOUNT_ID) and,
// optionally, an allow-list of drives of that account (drive/DRIVE_ID, repeatable). A drive_id argument
// outside a configured allow-list is refused locally, before any request is sent. Whether or not an
// allow-list is configured, every files.list, files.stat, and files.get call additionally confirms with
// Infomaniak's own drive detail endpoint that the named drive actually belongs to the bound account, before
// the file endpoint itself is ever called: an allow-list is local configuration and proves nothing about
// what a drive_id really resolves to, and the same token can otherwise reach a drive of another account.
// This ownership check is one extra request per call, so a files.* operation costs two requests against the
// shared 60-per-minute budget instead of one; drives.list needs no such check, because Infomaniak already
// answers it scoped to one account_id, and the entries are still matched against it again defensively.
// Every drive and file identifier an agent argument names is a plain positive integer used only as a path
// segment of the fixed Infomaniak REST paths below: no argument ever becomes a URL, an HTTP method, or a
// provider request body.
//
// Mail, CalDAV/CardDAV, and kChat are deliberately out of this provider's scope even though they are also
// Infomaniak products: kDrive is read here through a Bearer API token against api.infomaniak.com, while
// Mail is read over IMAP with mailbox credentials, CalDAV/CardDAV over WebDAV with Basic auth similar to the
// nextcloud provider, and kChat through a Mattermost-style token against its own API. Every declared secret
// role is mandatory for a connection today, so bundling those under one provider would force a kDrive-only
// connection to also declare secret roles it never uses. A sibling provider for each of them can be added
// later without touching this one.
//
// File and folder names, MIME types, and every other value in a listing or a metadata read arrive from the
// provider and are treated as untrusted data: normalised into a stable envelope, passed through the output
// encoders, and never rendered, executed, or stored.
package infomaniakdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace. It names the
// kDrive surface alone, see the package doc for why Mail, CalDAV/CardDAV, and kChat are not part of it.
const Provider = "infomaniakdrive"

// apiRoot is the fixed root of the Infomaniak REST API. The provider accepts no other origin: a personal
// token belongs to this one service, so a second origin could only be a mistake or an exfiltration route.
const apiRoot = "https://api.infomaniak.com"

// apiHost is apiRoot without its scheme, compared against a download redirect's target host.
const apiHost = "api.infomaniak.com"

// roleToken is the single secret role an Infomaniak credential must supply. It is used as a bearer token.
const roleToken = "token"

// dataSensitivity classifies results as file metadata and content of the configured Infomaniak account.
const dataSensitivity = "infomaniak-kdrive-files"

// Bounds of one request and one answer.
const (
	maxResponseBytes = 4 << 20
	maxFileBytes     = 4 << 20
	defaultTimeout   = 30 * time.Second
	// rootFileID is the fixed identifier of the root directory of a kDrive, as Infomaniak documents it: set
	// file_id to 1 to reach the root and navigate from there.
	rootFileID = 1
)

// minInterval spaces the requests that share one token. Infomaniak limits its whole API to 60 requests per
// minute per token and does not raise that limit on request, so this process never asks for more on its
// own; a 429 is still handled, because a token may be shared with other tools outside this process.
const minInterval = time.Second

// maxHold bounds how long a reported rate-limit wait may delay the next request of this process.
const maxHold = time.Minute

// limiters holds the rate-limit budget of every token this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

// Client binds one Infomaniak API token to the account and drive allow-list of its connection.
type Client struct {
	scope   scope
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the token of one selected connection and returns a client for its bound account and drive
// allow-list.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches Infomaniak.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if !isAPIRoot(resolved.BaseURL) {
		return nil, providerError("open", "an Infomaniak service must use the official API root "+apiRoot)
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Infomaniak API token is unusable"}
	}
	auth := "Bearer " + value.Secret
	if red != nil {
		red.Add(value.Secret, auth)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{scope: bound, auth: auth, http: newHTTPClient(), limiter: lim}, nil
}

// isAPIRoot reports whether a configured base URL names the official API root. An empty value and a
// trailing slash are the only variations a configuration may carry.
func isAPIRoot(raw string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return trimmed == "" || trimmed == apiRoot
}

// transport carries every Infomaniak request, of both HTTP clients below. A nil value is Go's default
// transport; the package's own tests replace it with a local fake.
var transport http.RoundTripper

// newHTTPClient is used for every JSON request. The token travels in the Authorization header, and no JSON
// endpoint this provider calls is documented to redirect, so none is followed: a redirect here could only
// be a mistake or an exfiltration route.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// newDownloadClient is used for the one endpoint Infomaniak documents as possibly answering with a redirect
// to the actual storage location of a file's content. At most one redirect is followed, only to an https
// location, and the Authorization header is removed before the redirected request is sent to any host other
// than the configured API host: the foreign storage location this may point to never receives the token.
func newDownloadClient() *http.Client {
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return &redirectRefusedError{message: "refused to follow more than one redirect for a file download"}
			}
			if req.URL.Scheme != "https" {
				return &redirectRefusedError{message: "refused to follow a redirect to a non-https location"}
			}
			if req.URL.Host != apiHost {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
}

// redirectRefusedError reports a redirect this provider deliberately did not follow, or followed without
// the credential. Its message never names the host it refused or stripped the credential for.
type redirectRefusedError struct{ message string }

func (e *redirectRefusedError) Error() string { return e.message }

// envelope is the generic Infomaniak response shape: result is "success", "error", or "asynchronous", and
// data carries the payload only when it is "success". This provider never sends anything but a read, so
// "asynchronous" is treated the same as "error": there is no payload this provider can use either way.
type envelope struct {
	Result string          `json:"result"`
	Data   json.RawMessage `json:"data"`
}

// do sends one bounded GET below the API root and decodes its data into out. meta, when not nil, receives
// the same top-level JSON besides data, for a response that also carries pagination fields as siblings of
// it.
func (c *Client) do(ctx context.Context, op, path string, query url.Values, out any) error {
	return c.doInto(ctx, op, path, query, out, nil)
}

func (c *Client) doInto(ctx context.Context, op, path string, query url.Values, out any, meta any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Infomaniak", err)
	}
	endpoint := apiRoot + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")

	response, err := c.http.Do(req)
	if err != nil {
		return transportError(op, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return invalidResponse(op, "the Infomaniak response could not be read within the size limit")
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return invalidResponse(op, "Infomaniak returned an invalid response")
	}
	if env.Result != "success" {
		return invalidResponse(op, "Infomaniak reported an error for a response with an HTTP success status")
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return invalidResponse(op, "Infomaniak returned an invalid response")
		}
	}
	if meta != nil {
		if err := json.Unmarshal(data, meta); err != nil {
			return invalidResponse(op, "Infomaniak returned an invalid response")
		}
	}
	return nil
}

// verifyDriveAccount confirms, with Infomaniak's own drive detail endpoint (GET /2/drive/{drive_id}, which
// requires no account_id and answers with the same Drive resource drives.list reads, account_id included),
// that a drive actually belongs to the account this connection is bound to. It runs before every file
// endpoint call, whether or not the connection also holds a drive allow-list: the allow-list only checks
// local configuration, never what the drive_id actually resolves to against the live account. A failure of
// this check, an unreadable answer, or a drive that resolves to another account all abort here, before any
// file endpoint is reached; there is no silent fallback. A drive of another account is reported the same way
// as one outside the allow-list: an invalid request, never a provider error, so a scope refusal is never
// mistaken for a missing drive.
func (c *Client) verifyDriveAccount(ctx context.Context, op string, driveID int64) error {
	var drive driveJSON
	if err := c.do(ctx, op, fmt.Sprintf("/2/drive/%d", driveID), nil, &drive); err != nil {
		return err
	}
	if drive.AccountID != c.scope.accountID {
		return invalidRequest("drive_id belongs to another Infomaniak account than the one this connection is bound to")
	}
	return nil
}

// statusError maps an HTTP status to a stable class. The provider body is never read into the message.
func (c *Client) statusError(op string, response *http.Response) error {
	status := response.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Infomaniak rejected the API token"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this Infomaniak token may not " +
			"perform this operation; check its scope and the account's rights in the Infomaniak Manager"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Infomaniak does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(retryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op,
			Message: "Infomaniak rate-limited the operation; it allows at most 60 requests per minute per token"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Infomaniak answered with a redirect, which Qatlas does not follow for this request"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op,
			Message: "Infomaniak kDrive is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Infomaniak did not answer in time"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Infomaniak rejected the operation (HTTP %d)", status)}
	}
}

// retryAfter reads how long Infomaniak asks a client to wait after a rate limit, capped at maxHold. Zero
// means Infomaniak named no time, or an unusable one.
func retryAfter(header http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	wait := time.Duration(seconds) * time.Second
	if wait > maxHold {
		return maxHold
	}
	return wait
}

// transportError classifies a failure that happened before a status code existed. A refused redirect is a
// policy decision, not an unreachable server.
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

// invalidRequest refuses a request the connection's own configuration decides against, before any request
// reaches Infomaniak: a drive outside the connection's allow-list, or an argument outside its schema's
// reach. No message ever quotes the refused value.
func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// validToken keeps an obviously unusable value out of a request header. The real check is Infomaniak's.
func validToken(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		// A header value may not carry control characters, and an Infomaniak token never does.
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	const maxValueLength = 1024
	if len(value) > maxValueLength {
		return value[:maxValueLength]
	}
	return value
}

// TestConnection performs the smallest safe authenticated read: one page of at most one drive of the bound
// account. It proves that the token is accepted and that this token may list drives of that account; it
// says nothing about a drive allow-list narrower than the account, because every one of its drives is
// checked again on its own request.
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
	const op = "test connection"
	query := url.Values{
		"account_id": {strconv.FormatInt(client.scope.accountID, 10)},
		"per_page":   {"1"},
	}
	var page []driveJSON
	if err := client.do(ctx, op, "/2/drive", query, &page); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds Infomaniak kDrive metadata, its read-only connection test, and its read operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Infomaniak kDrive", DefaultBaseURL: apiRoot,
		Description:        "Infomaniak kDrive file storage, read through the Infomaniak REST API",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "Infomaniak API token, created in the Infomaniak Manager under Account settings, API " +
				"tokens; it reaches every Infomaniak account and kDrive its owner administers, so the connection " +
				"target decides which single account, and which of its drives, Qatlas exposes",
		}},
		Target: config.TargetMetadata{
			Label:    "account and drives",
			Required: true,
			Multiple: true,
			Description: "exactly one account/ACCOUNT_ID target this connection is bound to, plus an optional, " +
				"repeatable allow-list of drive/DRIVE_ID targets of that account; without a drive target, every " +
				"kDrive of the bound account is reachable. A drive_id argument outside a configured allow-list is " +
				"refused locally, before any request is sent; every files.list, files.stat, and files.get call " +
				"also confirms with Infomaniak itself, in one extra request, that the drive actually belongs to " +
				"the bound account, and is refused before the file endpoint is called when it does not or when " +
				"that check fails",
			Kinds: []config.TargetKind{{
				Name: "account",
				Description: "the Infomaniak account (customer) this connection may reach; required, exactly " +
					"one; find it as the numeric account identifier in the Infomaniak Manager or in the account " +
					"field of a drive this token can list",
				Forms: []string{"account/ACCOUNT_ID"},
			}, {
				Name:        "drive",
				Description: "one kDrive of the bound account; optional, and may be listed more than once",
				Forms:       []string{"drive/DRIVE_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseAllowlist(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read files", Recommended: true,
			Description: "lists the reachable drives, lists folder contents page by page, and reads file and " +
				"folder metadata and bounded content; changes nothing",
			Tools: []string{drivesList.ID, filesList.ID, filesStat.ID, filesGet.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: drivesList, Handler: capability.Handler(invokeDrivesList)},
		capability.Operation{Descriptor: filesList, Handler: capability.Handler(invokeFilesList)},
		capability.Operation{Descriptor: filesStat, Handler: capability.Handler(invokeFilesStat)},
		capability.Operation{Descriptor: filesGet, Handler: capability.Handler(invokeFilesGet)},
	)
}
