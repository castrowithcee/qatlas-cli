// Package excalidrawplus implements controlled, read-only access to one Excalidraw+ workspace through its
// public REST API (plus.excalidraw.com/docs/api, base https://api.excalidraw.com/api/v1). The API is a public
// beta whose names and schemas may still change; the provider is a beta for the same reason and every tool
// descriptor carries a version that moves with a breaking change. It lists collections and scenes, reads one
// scene's metadata, and reads one scene's content, bounded and searchable client-side.
//
// A connection binds one workspace key (the key belongs to exactly one workspace) and a collection
// allow-list (collection/COLLECTION_ID, repeatable) or the explicit * wildcard. The allow-list is the only
// boundary Qatlas adds to what the key itself may reach: a collection_id argument outside it is refused
// locally before any secret is read or request is sent, lists are filtered to the allowed collections, and a
// scene is bound to an allowed collection through the collection its own metadata reports before its detail
// or content is returned. That binding always costs one metadata request first, never a decision from the
// scene identifier alone, and a scene that belongs to no allowed collection (or to none) is refused without
// naming the collection it really belongs to. Content is only requested once that check passed.
//
// The documented API has no content search of its own, so the search is client-side: scenes.content filters
// a scene's elements by a case-insensitive substring of their text or name. Scene content is untrusted data
// of the workspace: it is reduced to a compact, capped element view, embedded files are never returned (only
// counted), and nothing is rendered, executed, or stored.
package excalidrawplus

import (
	"bytes"
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

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "excalidrawplus"

// roleAPIKey is the single secret role: a workspace API key sent as a Bearer token.
const roleAPIKey = "api-key"

const (
	// apiHost is the one host this provider talks to (the documented API base is
	// https://api.excalidraw.com/api/v1), and apiPath the fixed root below it.
	apiHost       = "api.excalidraw.com"
	defaultOrigin = "https://" + apiHost
	apiPath       = "/api/v1"
)

// dataSensitivity classifies results as drawings of the configured workspace.
const dataSensitivity = "excalidrawplus-scenes"

const (
	// maxResponseBytes bounds metadata and list answers; maxContentBytes bounds a scene's content, which may
	// embed images as data URLs. Excalidraw+ documents neither limit, so both are local ceilings.
	maxResponseBytes = 4 << 20
	maxContentBytes  = 16 << 20
	defaultTimeout   = 30 * time.Second
	// maxListLimit is the documented range of limit (1 to 100); defaultListLimit is a local choice.
	maxListLimit     = 100
	defaultListLimit = 50
	// maxOffset keeps an offset far below the documented integer range while above any realistic workspace.
	maxOffset = 1000000
	// minInterval spaces requests to stay below the documented 600 requests per minute and IP.
	minInterval = 100 * time.Millisecond
	// maxHold bounds how long a 429 may hold this key's limiter.
	maxHold = 60 * time.Second
)

var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every request. A nil value is Go's default transport; the package's tests replace it.
var transport http.RoundTripper

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Client binds one workspace key to its connection's collection scope.
type Client struct {
	scope   scope
	origin  string
	key     string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the API key of one selected connection and returns a client.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	const op = "open"
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	origin, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAPIKey)
	if err != nil {
		return nil, err
	}
	if !validKey(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Excalidraw+ API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	return &Client{scope: bound, origin: origin, key: value.Secret, http: newHTTPClient(),
		limiter: limiters.For(value.Secret)}, nil
}

const instanceReason = "an Excalidraw+ service needs the plain https URL https://" + apiHost +
	", with no port, path, user, query, or fragment"

// parseInstance accepts exactly the documented API origin.
func parseInstance(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Opaque != "" || parsed.Port() != "" ||
		strings.TrimRight(parsed.Path, "/") != "" || strings.ToLower(parsed.Hostname()) != apiHost {
		return "", errors.New(instanceReason)
	}
	return defaultOrigin, nil
}

// validKey keeps an obviously unusable value out of a request header; the real check is Excalidraw+'s own.
func validKey(value string) bool {
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

// get sends one bounded GET below the API root and decodes its body into out.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any, maxBytes int) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Excalidraw+", err)
	}
	endpoint := c.origin + apiPath + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "Excalidraw+", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		return invalidResponse(op, "the Excalidraw+ response could not be read within the size limit")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, "Excalidraw+ returned an invalid response")
	}
	return nil
}

// changeUncertain is appended when a change may have taken effect although no usable answer arrived.
const changeUncertain = "; this change may have taken effect, read the current state in Excalidraw+ before repeating it"

// send sends one JSON change (POST or PATCH) below the API root and decodes its answer into out. It is never
// repeated: after a timeout, a reset connection, a 5xx, or an unreadable answer the message says the change may
// have taken effect.
func (c *Client) send(ctx context.Context, op, method, path string, payload, out any) error {
	if method != http.MethodPost && method != http.MethodPatch {
		return providerError(op, "the method is not offered")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Excalidraw+", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+apiPath+path, bytes.NewReader(body))
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Excalidraw+", err)
		if failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown {
			failure.Message += changeUncertain
		}
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response)
		if response.StatusCode >= 500 {
			failure.Message += changeUncertain
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return invalidResponse(op, "the Excalidraw+ response could not be read within the size limit"+changeUncertain)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, "Excalidraw+ returned an invalid response"+changeUncertain)
	}
	return nil
}

// statusError maps an HTTP status to a stable class. The provider body is never read into the message.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	status := response.StatusCode
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Excalidraw+ rejected the API key"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this Excalidraw+ API key may " +
			"not perform this operation; it needs the read right or the matching routes, and must not be expired"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Excalidraw+ does not hold this resource or does not show it to this key"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(holdOf(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Excalidraw+ rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Excalidraw+ is unavailable"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Excalidraw+ did not answer in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Excalidraw+ answered with a redirect, which Qatlas does not follow for this request"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Excalidraw+ rejected the operation (HTTP %d)", status)}
	}
}

// holdOf reads how long to wait after a 429. Excalidraw+ documents X-RateLimit-Reset (a Unix timestamp) and
// no Retry-After; Retry-After is honoured too, should one be sent. The result is bounded.
func holdOf(header http.Header) time.Duration {
	var wait time.Duration
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds > 0 {
		wait = time.Duration(seconds) * time.Second
	} else if reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil {
		wait = time.Until(time.Unix(reset, 0))
	}
	if wait < 0 {
		return 0
	}
	if wait > maxHold {
		return maxHold
	}
	return wait
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

// invalidRequest refuses a request the connection's own configuration decides against. No message quotes
// the refused value or names the real collection of a scene.
func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// bounded keeps an oversized provider string out of a result without interpreting it, never splitting a
// multi-byte character.
func bounded(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return strings.ToValidUTF8(value[:max], "")
}

const maxValueLength = 2048

func boundedValue(value string) string { return bounded(value, maxValueLength) }

// TestConnection performs the smallest safe authenticated read: one collection.
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
	var page collectionsPageJSON
	if err := client.get(ctx, "test connection", "/collections", url.Values{"limit": {"1"}}, &page,
		maxResponseBytes); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds the provider metadata, its connection test, and its read and manage operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Excalidraw+", DefaultBaseURL: defaultOrigin,
		Description: "Whiteboard workspace, collections and scenes with their text content read through " +
			"the public workspace API (beta)",
		ValidateBaseURL: func(raw string) error {
			_, err := parseInstance(raw)
			return err
		},
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleAPIKey,
			Description: "Excalidraw+ workspace API key, created in the workspace settings with the read right " +
				"(or routes for collections and scenes) and an expiry date; a key belongs to one workspace and is " +
				"sent as 'Authorization: Bearer ...'",
		}},
		Target: config.TargetMetadata{
			Label:    "collection",
			Required: true,
			Multiple: true,
			Wildcard: wildcard,
			WildcardWarning: "All collections and scenes the API key may read are exposed to the agent. " +
				"Use a collection allow-list when not all of them are required.",
			Description: "one or more collections as an allow-list, or * for every collection the key reads; a " +
				"scene is only reachable when its own collection is allowed",
			Kinds: []config.TargetKind{{
				Name:        "collection",
				Description: "a collection whose scenes and scene content this connection may read",
				Forms:       []string{"collection/COLLECTION_ID", "*"},
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read collections and scenes", Recommended: true,
			Description: "lists collections and scenes, reads a scene's metadata, and reads and searches a " +
				"scene's text content; changes nothing",
			Tools: readTools,
		}, {
			ID: "manage", Title: "Create, rename, and move scenes",
			Description: "reads what the read profile reads, creates a scene in an allowed collection, and " +
				"renames or moves a scene of an allowed collection into an allowed collection; every change " +
				"needs its own confirmation. There is no tool to change a scene's content or to delete anything",
			Tools: append(append([]string{}, readTools...), scenesCreate.ID, scenesUpdate.ID),
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: collectionsList, Handler: capability.Handler(invokeCollectionsList)},
		capability.Operation{Descriptor: scenesList, Handler: capability.Handler(invokeScenesList)},
		capability.Operation{Descriptor: scenesGet, Handler: capability.Handler(invokeScenesGet)},
		capability.Operation{Descriptor: scenesContent, Handler: capability.Handler(invokeScenesContent)},
		capability.Operation{Descriptor: scenesCreate, Handler: capability.Handler(invokeScenesCreate)},
		capability.Operation{Descriptor: scenesUpdate, Handler: capability.Handler(invokeScenesUpdate)},
	)
}

var readTools = []string{collectionsList.ID, scenesList.ID, scenesGet.ID, scenesContent.ID}

var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

const idSchema = `{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9_-]+$"}`

// pageSchema is the shared offset and limit contract of both list tools.
var pageSchema = `"offset":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxOffset) + `},` +
	`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}`

var pageArguments = []capability.Argument{
	{Name: "offset", Description: "Items to skip before this page; 0 when omitted"},
	{Name: "limit", Description: "Items per page, 1 to 100; 50 when omitted"},
}

var pageFields = []capability.Field{
	{Name: "offset", Description: "Offset of this page"},
	{Name: "limit", Description: "Page size requested"},
	{Name: "has_next_page", Description: "True when Excalidraw+ reports a further page"},
	{Name: "next_offset", Description: "Offset of the next page, present when has_next_page is true"},
	{Name: "count", Description: "Items on this page after this connection's collection allow-list was applied"},
}
