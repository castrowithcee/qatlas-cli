// Package make implements controlled access to one Make (make.com) zone's scenarios through its public REST
// API (developers.make.com/api-documentation, base path /api/v2). Milestone A lists and reads scenarios,
// reads a scenario's blueprint, and lists and reads its execution history ("logs" in Make's own naming,
// exposed here as runs). Milestone B adds confirmed changes: creating a scenario, replacing its blueprint,
// scheduling, name, or folder, starting and stopping it, and running it on demand. There is still no tool to
// delete, clone, or replay a scenario, and no generic webhook call: those are deliberately left out of this
// milestone.
//
// Every tool that changes something needs its own confirmation, sends exactly one changing request, and is
// never retried by this provider: a failure that could mean the request nonetheless reached Make (a timeout,
// a connection reset, or a 5xx) is reported as uncertain instead, see (*Client).do. scenarios.create always
// sends the connection's own bound team as teamId, never a caller-supplied one, and refuses outright on a
// connection restricted by a scenario allow-list, since a scenario that does not exist yet can never already
// be on that list. scenarios.create, scenarios.update, scenarios.start, and scenarios.stop all re-read the
// scenario after their one changing request and re-apply the bound team and scenario allow-list to it; a
// mismatch is reported as a provider error, not an invalid request, because the change has already happened
// and this milestone has no delete tool to undo it.
//
// Make's own OpenAPI schema declares a scenario's "blueprint" and "scheduling" as request-body strings, not
// nested objects, for both creating and updating a scenario (developers.make.com/api-documentation/api-
// reference/scenarios, checked 2026-09-27 through this package's own documentation tooling, not a live
// account): this provider accepts both as ordinary JSON objects from a caller and encodes each one to a
// compact JSON string before it is placed in the request body, exactly once, so a caller never has to double-
// encode anything itself. scenarios.run's own "data" argument is not documented the same way: Make's schema
// shows it as a plain nested object, so it is sent as one, unencoded. This asymmetry rests on documentation
// tooling, not a confirmed live call; verify it against a real zone before depending on it in a new setting.
//
// scenarios.run never offers a callback_url argument: Make's own "callbackUrl" body field would have this
// provider place a caller-chosen URL directly into an outbound webhook call it never controls, which this
// milestone refuses to expose the same way no provider here offers a generic webhook call. responsive
// defaults to false: Make's own "responsive": true makes this one request block until the run finishes,
// which could easily outlast this provider's own request timeout for a long-running scenario and then be
// reported as an uncertain run that in fact never even needed retrying. Make's own run response never
// carries a resource this provider can re-verify against the bound team, so, unlike every other change of
// this provider, running a scenario is not followed by a re-read.
//
// Make documents a run's own "executionId" identically, word for word, as the identifier a scenario's log
// entry reports under its own "id" key, for both its "Get execution log" and its "Get scenario execution
// details" endpoints: this is the basis for pointing a caller at runs.get (built on the logs endpoint) to
// learn a run's outcome, rather than at Make's separate, undocumented-here scenarios/{id}/executions/{id}
// endpoint, which this milestone does not add a tool for. Whether a still-running execution is already
// visible through the logs endpoint before it finishes is not documented either way and is not assumed.
//
// The Go package identifier is deliberately "makeapi", not "make": Go predeclares a builtin function named
// make, and an unaliased import of a package literally named make would shadow it in every file that
// imports this package. The provider ID, the CLI namespace, and every tool ID still use "make", exactly as
// Make itself is named; only the Go identifier differs, and only to avoid that footgun.
//
// A connection binds one Make zone, through its configured base URL, one API token of that zone, and
// exactly one team (team/TEAM_ID) of it, plus, optionally, one organization (organization/ORG_ID) and an
// allow-list of scenarios (scenario/SCENARIO_ID, repeatable) of that team. A Make API token is created for
// one specific zone and, within it, reaches every team its owner belongs to (developers.make.com's own
// token-creation guide: "If you have access to multiple Make zones, generate separate tokens for each of
// them"), which is exactly the situation a service provider holding several customers' tokens has to keep
// apart, the same reasoning the n8n provider's project allow-list and the Infomaniak kDrive and kChat
// providers' account and team bindings apply to their own multi-tenant credentials. Team is therefore
// required, never optional, unlike n8n's project allow-list: a Make token has no narrower, provider-issued
// scope than "every team its owner belongs to", so the connection's own team binding is the only boundary
// that exists at all.
//
// A scenario_id argument outside a configured scenario allow-list is refused locally, before any request is
// sent. Every operation that names a scenario_id directly (scenarios.get, scenarios.blueprint,
// scenarios.update, scenarios.start, scenarios.stop, scenarios.run, runs.list, runs.get) also reads that
// scenario from Make itself, in one extra request when the operation's own answer would not already carry
// it, and confirms live that its reported teamId matches the bound team before any content is returned or
// any changing request is sent; a scenario of another team is refused the same way as one outside the
// allow-list, an invalid request, never a provider error, and the refusal never names the scenario's real
// team. A run's list and get answers additionally carry their own teamId and organizationId, which this
// provider defensively re-checks against the bound team and, when configured, the bound organization, even
// though the scenario check above already proved the same boundary: a scenario object itself reports no
// organizationId at all (only teamId), so a configured organization target can only ever be verified this
// way, against a run's own report, never against a scenario directly.
//
// A scenario read never returns a connection's or a key's stored value: Make's own blueprint format
// references a connection, a key, or a webhook only by its numeric ID inside a module's parameters, never by
// its secret value, which is exactly why a blueprint can be exported and re-imported into another scenario
// without ever re-entering a credential, and why Make's own "Clone scenario" endpoint accepts an id-to-id
// account/key/hook mapping rather than any secret. This provider passes a blueprint through unmodified
// beyond its own response size bound, the same reasoning the n8n provider's node parameters passthrough
// uses for its own node parameters. A run's detail is bounded to its status, timings, operations and data
// volume, and a short, best-effort error excerpt when Make's own log entry carries one; it never returns a
// bundle's actual input or output data, which Make's execution history does not expose to this endpoint in
// the first place.
//
// Every value a listing or a read answers with arrives from Make and is treated as untrusted data:
// normalised into a stable envelope, passed through the output encoders, and never rendered, executed, or
// stored.
package makeapi

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
const Provider = "make"

// The scope phrases a 403's message names, one per group of operations this provider sends, so a caller
// learns exactly what to add to the token instead of only that it was refused.
const (
	needRead  = "the scenarios:read scope"
	needWrite = "the scenarios:write scope"
	needRun   = "the scenarios:read, scenarios:write, and scenarios:run scopes"
)

// roleAPIToken is the single secret role a Make credential must supply. It is sent as the Authorization
// header's Token scheme (developers.make.com/api-documentation/authentication/create-authentication-token),
// never as a Bearer token.
const roleAPIToken = "api-token"

// apiPath is the fixed API root below the configured zone origin.
const apiPath = "/api/v2"

// dataSensitivity classifies results as scenario and run data of the configured Make zone.
const dataSensitivity = "make-scenarios"

// zones lists every Make API zone this provider accepts as a connection's base URL host, exactly as
// developers.make.com/api-documentation/getting-started/api-structure documents them as of 2026-09-27: four
// general-availability zones and two Celonis-branded enterprise zones. There is no wildcard or suffix rule,
// unlike the kChat provider's single fixed domain: make.com and make.celonis.com are two different apex
// domains, and Make may add a further zone later, which this list would then need to learn; until it does,
// an unlisted zone host is refused rather than silently trusted.
var zones = map[string]bool{
	"eu1.make.com":         true,
	"eu2.make.com":         true,
	"us1.make.com":         true,
	"us2.make.com":         true,
	"eu1.make.celonis.com": true,
	"us1.make.celonis.com": true,
}

// Bounds of one request and one answer.
const (
	// maxResponseBytes bounds every answer this provider reads, including a scenario's blueprint: Make
	// documents no size limit of its own for either, so this is a local ceiling on what this process ever
	// reads, not a belief about what a zone would send.
	maxResponseBytes = 4 << 20
	// maxErrorExcerptLength bounds the one best-effort error message runs.get may extract from a failed
	// run's own log entry; see fetchRunError.
	maxErrorExcerptLength = 2048
	defaultTimeout        = 30 * time.Second
	// defaultListLimit and maxListLimit bound scenarios.list and runs.list. Make documents pg[limit] as a
	// plain integer with no fixed default or maximum of its own, so both are a local, conservative choice,
	// not a value Make itself declares.
	defaultListLimit = 50
	maxListLimit     = 200
	// maxBlueprintWriteBytes bounds a blueprint argument of scenarios.create and scenarios.update before it
	// is encoded to the JSON string Make's own request body wants. It reuses maxResponseBytes: a blueprint
	// this provider writes is bounded the same as one it would read back.
	maxBlueprintWriteBytes = maxResponseBytes
	// maxBlueprintDepth bounds how deeply nested a blueprint argument's modules and their parameters may be,
	// well above any realistic Make scenario, so a malformed or adversarial payload is refused on its shape
	// alone before any request is built. It is a local ceiling this process enforces, not a limit Make
	// itself documents.
	maxBlueprintDepth = 64
	// maxSchedulingWriteBytes and maxSchedulingDepth bound a scheduling argument the same way, sized for the
	// small type/interval object Make documents plus headroom for the further, undocumented per-type fields
	// a real scheduling configuration carries.
	maxSchedulingWriteBytes = 8 << 10
	maxSchedulingDepth      = 8
	// maxRunDataBytes and maxRunDataDepth bound scenarios.run's own "data" argument, the input parameters of
	// one on-demand run, deliberately far smaller than a blueprint: Make documents no limit of its own for
	// it, so this is a local ceiling on what this process ever sends, not a belief about what Make would
	// accept.
	maxRunDataBytes = 64 << 10
	maxRunDataDepth = 16
	// maxScenarioNameLength and maxDescriptionLength bound the two plain-text arguments scenarios.create and
	// scenarios.update accept beyond blueprint and scheduling. Make documents no length of its own for
	// either, so both are a local, conservative choice.
	maxScenarioNameLength = 256
	maxDescriptionLength  = 2048
)

// limiters holds the rate-limit budget of every API token this process has used. Make documents no fixed
// request budget of its own for a personal API token (only that organization- and team-level automatic
// scenario execution has its own, unrelated, limits), so this provider applies no proactive spacing of its
// own; a 429 Make itself reports is still classified and, when it names a Retry-After, holds this
// connection's own limiter before the next request, exactly as the n8n and kChat providers do for the same
// reason.
var limiters = ratelimit.NewRegistry(0)

// Client binds one Make API token to the zone origin and the team, organization, and scenario scope of its
// connection.
type Client struct {
	scope   scope
	origin  string
	token   string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the API token of one selected connection and returns a client for its bound zone and scope.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches a real Make zone.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	origin, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAPIToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Make API token is unusable"}
	}
	auth := "Token " + value.Secret
	if red != nil {
		red.Add(value.Secret, auth)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{scope: bound, origin: origin, token: value.Secret, http: newHTTPClient(), limiter: lim}, nil
}

// instanceReason is the one refusal message of a malformed or unknown base URL. It never quotes the value
// that was refused.
var instanceReason = "a Make service needs a plain https URL whose host is one of Make's documented API " +
	"zones (for example eu1.make.com or us1.make.com), with no port, path, user, query, or fragment"

// parseInstance validates the configured base URL of one Make zone: a plain https origin whose host is
// exactly one of the documented zone hosts in zones, with no port, path, user, query, or fragment, so it can
// only ever be replayed as the root of the fixed apiPath paths this provider builds and never redirected or
// rewritten to a host this provider has not already validated. The host is normalised to lowercase, because
// DNS names are case-insensitive.
func parseInstance(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.Port() != "" {
		return "", errors.New(instanceReason)
	}
	if strings.TrimRight(parsed.Path, "/") != "" {
		return "", errors.New(instanceReason)
	}
	host := strings.ToLower(parsed.Hostname())
	if !zones[host] {
		return "", errors.New(instanceReason)
	}
	return "https://" + host, nil
}

// transport carries every Make request. A nil value is Go's default transport; the package's own tests
// replace it with a local fake.
var transport http.RoundTripper

// newHTTPClient is used for every request this provider sends. The token travels in the Authorization
// header, and no endpoint this provider calls is documented to redirect, so none is followed: a redirect
// here could only be a mistake or an exfiltration route to a host this connection was never bound to.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// uncertain is appended to a failure of a change request whose request may have reached Make: the change
// may have taken effect although no confirmation ever arrived. Qatlas never repeats such a request by
// itself; a caller is told to read the current state before deciding whether to try again.
const uncertain = "; this change may have taken effect, read the current state before repeating it"

// runUncertain is scenarios.run's own uncertain note: unlike every other change of this provider, a run is
// never followed by a re-read (see the package doc), so a caller is pointed at runs.list instead of at "the
// current state" of a resource this provider would otherwise show directly.
const runUncertain = "; the run may have started; check make.runs.list before running again"

// get sends one bounded GET below the API root and decodes its body into out. need names the scope this
// read needs, for a 403's message.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any, need string) error {
	return c.do(ctx, op, http.MethodGet, path, query, nil, out, need, "")
}

// change sends one bounded, state-changing request below the API root, once, with a JSON body when body is
// not nil, and decodes the answer into out when out is not nil. It is never retried by this provider; every
// failure of it that could mean the request nonetheless reached Make is marked with note instead. need names
// the scope this change needs, for a 403's message.
func (c *Client) change(ctx context.Context, op, method, path string, query url.Values, body, out any,
	need, note string) error {
	return c.do(ctx, op, method, path, query, body, out, need, note)
}

// do sends one bounded request below the API root, with a JSON body when body is not nil, and decodes the
// answer into out when out is not nil. need names the scope this request needs, so a permission refusal
// tells a caller what to add to the token instead of only that it was refused. note marks this as a
// changing request when it is not empty: every failure of it that could mean the request nonetheless arrived
// has note appended, so this provider never repeats it by itself, the same contract n8n and infomaniakchat
// give their own change requests.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, body, out any,
	need, note string) error {
	changing := note != ""
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Make", err)
	}
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return providerError(op, "the request could not be built")
		}
		payload = encoded
	}
	endpoint := c.origin + apiPath + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Make", err)
		if changing && (failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown) {
			failure.Message += note
		}
		return failure
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response, need)
		if changing && response.StatusCode >= 500 {
			failure.Message += note
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBytes)+1))
	if err != nil || len(data) > maxResponseBytes {
		message := "the Make response could not be read within the size limit"
		if changing {
			message += note
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		message := "Make returned an invalid response"
		if changing {
			message += note
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	return nil
}

// pausedErrorCode reads a small, bounded prefix of a 429 response body and extracts only its "code" field,
// never its message or any other value: the provider body is otherwise never read into an error, so this is
// the one exception, kept to the single classification value Make documents (IM310, an organization or team
// that is paused, not a transient rate limit), which is itself never surfaced verbatim, only recognised.
func pausedErrorCode(body io.Reader) string {
	const maxPeekBytes = 4096
	data, _ := io.ReadAll(io.LimitReader(body, maxPeekBytes))
	var parsed struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(data, &parsed)
	return parsed.Code
}

// statusError maps an HTTP status to a stable class. The provider body is never read into the message,
// except the one bounded peek pausedErrorCode takes of a 429 to tell a paused organization or team apart
// from an ordinary rate limit. need names the scope this operation needs, so a permission refusal tells a
// caller what to add to the token instead of only that it was refused.
func (c *Client) statusError(op string, response *http.Response, need string) *provider.Error {
	status := response.StatusCode
	switch {
	case status == http.StatusUnauthorized:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Make rejected the API token; a " +
			"token created for a different zone is rejected here the same way as any other invalid token"}
	case status == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this Make API token may " +
			"not perform this operation; it needs " + need + ", added under Profile, API, when the token was created"}
	case status == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Make does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		// IM310 is Make's own code for an organization or team that is paused: waiting out a Retry-After
		// will not fix that, unlike every other 429 this provider still holds its own limiter for.
		code := pausedErrorCode(response.Body)
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		c.limiter.HoldFor(retryAfter(response.Header))
		if code == "IM310" {
			return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Make reports this " +
				"organization or team is paused (IM310); this is not a transient rate limit, and repeating " +
				"the request will not help until it is reactivated"}
		}
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Make rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Make is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Make did not answer in time"}
	case status >= 300 && status < 400:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Make answered with a redirect, which Qatlas does not follow for this request"}
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Make rejected the operation (HTTP %d)", status)}
	}
}

// retryAfter reads how long Make asks a client to wait after a rate limit. Zero means Make named no time,
// or an unusable one.
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

// invalidRequest refuses a request the connection's own configuration, or a live scope check, decides
// against, before the matching content is ever returned. No message ever quotes the refused value or names
// a scenario's or a run's real team or organization.
func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// validToken keeps an obviously unusable value out of a request header. The real check is Make's own. Make
// issues its API tokens as UUIDs (developers.make.com's own authentication guide shows one as an example),
// but this accepts any printable-ASCII value in a generous range, the same tolerance the n8n and kChat
// providers give their own credential shape, in case Make changes the format later.
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

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	const maxValueLength = 2048
	if len(value) > maxValueLength {
		return value[:maxValueLength]
	}
	return value
}

// jsonDepth reports how deeply nested a JSON value is: a scalar is depth 0, and an object or array is one
// more than its deepest child. raw must already be known-valid JSON; the central schema validator proves
// that before any of this package's write handlers ever call this, so a decode failure here can only be this
// process's own size accounting, never a malformed caller value.
func jsonDepth(raw json.RawMessage) (int, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return 0, err
	}
	return depthOf(value), nil
}

func depthOf(value any) int {
	switch v := value.(type) {
	case map[string]any:
		deepest := 0
		for _, child := range v {
			if d := depthOf(child) + 1; d > deepest {
				deepest = d
			}
		}
		return deepest
	case []any:
		deepest := 0
		for _, child := range v {
			if d := depthOf(child) + 1; d > deepest {
				deepest = d
			}
		}
		return deepest
	default:
		return 0
	}
}

// validJSONObject checks a caller-supplied JSON object argument (a blueprint, a scheduling configuration, or
// a run's data) against a local size and nesting-depth ceiling before it is ever placed in a request body.
// Neither ceiling is a limit Make documents; both are this process's own protection against a malformed or
// adversarial payload, well above what any realistic value of that kind needs.
func validJSONObject(raw json.RawMessage, maxBytes, maxDepth int, label string) error {
	if len(raw) > maxBytes {
		return invalidRequest(fmt.Sprintf("%s is larger than the %d byte limit", label, maxBytes))
	}
	depth, err := jsonDepth(raw)
	if err != nil || depth > maxDepth {
		return invalidRequest(fmt.Sprintf("%s is nested deeper than %d levels", label, maxDepth))
	}
	return nil
}

// TestConnection performs the smallest safe authenticated read: one page of at most one scenario of the
// bound team. It proves that the API token is accepted and holds the scenarios:read scope for this zone; it
// says nothing about an organization or scenario allow-list narrower than that, because every scenario and
// run a connection's own operations name is checked again on its own request.
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
	var page scenariosPageJSON
	query := url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}, "pg[limit]": {"1"}}
	if err := client.get(ctx, op, "/scenarios", query, &page, needRead); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds Make metadata, its connection test, and its read and change operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Make", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Visual automation platform, scenarios and their run history read, created, changed, " +
			"started, stopped, and run through its zone-scoped REST API",
		ValidateBaseURL: func(raw string) error {
			_, err := parseInstance(raw)
			return err
		},
		SecretRoles: []config.SecretRole{{
			Name: roleAPIToken,
			Description: "Make API token, created in Make under the profile avatar, Profile, API, Add token; " +
				"scenarios:read for the read profile, plus scenarios:write for the manage profile's create, " +
				"update, start, and stop tools, plus scenarios:run for its run tool. A token belongs to one " +
				"zone only, so a token of a different zone than the connection's own is rejected as invalid, " +
				"and it reaches every team its owner belongs to, which is why this connection's own team " +
				"target decides what is exposed",
		}},
		Target: config.TargetMetadata{
			Label:    "team, organization, and scenarios",
			Required: true,
			Multiple: true,
			Description: "exactly one team/TEAM_ID target this connection is bound to, plus an optional " +
				"organization/ORG_ID target and an optional, repeatable allow-list of scenario/SCENARIO_ID " +
				"targets of that team; without a scenario target, every scenario of the bound team the token " +
				"can reach is reachable. A scenario_id argument outside a configured allow-list is refused " +
				"locally, before any request is sent; every operation that names a scenario_id also confirms " +
				"live, against Make's own report, that the scenario belongs to the bound team, and a run's own " +
				"answer is defensively re-checked the same way against the bound team and, when configured, " +
				"the bound organization. scenarios.create always creates in the bound team and refuses outright " +
				"on a connection restricted by a scenario allow-list, since a scenario that does not exist yet " +
				"can never already be on that list",
			Kinds: []config.TargetKind{{
				Name: "team",
				Description: "the Make team this connection may reach; required, exactly one; find it as the " +
					"numeric team identifier in Make's team settings URL or in a scenario's own teamId",
				Forms: []string{"team/TEAM_ID"},
			}, {
				Name: "organization",
				Description: "the Make organization the bound team belongs to; optional, at most one. A " +
					"scenario itself reports no organizationId of its own, only teamId, so this target can only " +
					"be verified against a run's own report, never against a scenario directly",
				Forms: []string{"organization/ORG_ID"},
			}, {
				Name:        "scenario",
				Description: "one scenario of the bound team; optional, and may be listed more than once",
				Forms:       []string{"scenario/SCENARIO_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read scenarios and runs", Recommended: true,
			Description: "lists and reads the scenarios of the bound team, reads a scenario's blueprint, " +
				"and lists and reads its run history; changes nothing and starts nothing",
			Tools: readTools,
		}, {
			ID: "manage", Title: "Manage scenarios and runs",
			Description: "reads what the read profile reads, creates a scenario, replaces a scenario's " +
				"blueprint, scheduling, name, or folder, starts and stops it, and runs it on demand; every " +
				"change needs its own confirmation. There is still no tool to delete, clone, or replay a " +
				"scenario, and no generic webhook call: those are deliberately left out of this milestone",
			Tools: append(append([]string{}, readTools...), scenariosCreate.ID, scenariosUpdate.ID,
				scenariosStart.ID, scenariosStop.ID, scenariosRun.ID),
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: scenariosList, Handler: capability.Handler(invokeScenariosList)},
		capability.Operation{Descriptor: scenariosGet, Handler: capability.Handler(invokeScenariosGet)},
		capability.Operation{Descriptor: scenariosBlueprint, Handler: capability.Handler(invokeScenariosBlueprint)},
		capability.Operation{Descriptor: runsList, Handler: capability.Handler(invokeRunsList)},
		capability.Operation{Descriptor: runsGet, Handler: capability.Handler(invokeRunsGet)},
		capability.Operation{Descriptor: scenariosCreate, Handler: capability.Handler(invokeScenariosCreate)},
		capability.Operation{Descriptor: scenariosUpdate, Handler: capability.Handler(invokeScenariosUpdate)},
		capability.Operation{Descriptor: scenariosStart, Handler: capability.Handler(invokeScenariosStart)},
		capability.Operation{Descriptor: scenariosStop, Handler: capability.Handler(invokeScenariosStop)},
		capability.Operation{Descriptor: scenariosRun, Handler: capability.Handler(invokeScenariosRun)},
	)
}

// readTools are the five tools of Milestone A. The manage profile reuses this list instead of repeating it.
var readTools = []string{scenariosList.ID, scenariosGet.ID, scenariosBlueprint.ID, runsList.ID, runsGet.ID}
