// Package make implements controlled, read-only access to one Make (make.com) zone's scenarios through its
// public REST API (developers.make.com/api-documentation, base path /api/v2). Milestone A lists and reads
// scenarios, reads a scenario's blueprint, and lists and reads its execution history ("logs" in Make's own
// naming, exposed here as runs); it changes nothing and starts nothing. Creating or replacing a scenario,
// activating or deactivating it, running it on demand, and every other write are a later milestone, which
// this package's Client, error classes, and target model are built to extend without a rewrite.
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
// sent. Every operation that names a scenario_id directly (scenarios.get, scenarios.blueprint, runs.list,
// runs.get) also reads that scenario from Make itself, in one extra request when the operation's own answer
// would not already carry it, and confirms live that its reported teamId matches the bound team before any
// content is returned; a scenario of another team is refused the same way as one outside the allow-list, an
// invalid request, never a provider error, and the refusal never names the scenario's real team. A run's
// list and get answers additionally carry their own teamId and organizationId, which this provider
// defensively re-checks against the bound team and, when configured, the bound organization, even though
// the scenario check above already proved the same boundary: a scenario object itself reports no
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

// get sends one bounded GET below the API root and decodes its body into out.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Make", err)
	}
	endpoint := c.origin + apiPath + path
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
		return provider.Transport(op, "Make", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBytes)+1))
	if err != nil || len(data) > maxResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the Make response could not be read within the size limit"}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Make returned an invalid response"}
	}
	return nil
}

// statusError maps an HTTP status to a stable class. The provider body is never read into the message. need
// names the scope this operation needs, so a permission refusal tells a caller what to add to the token
// instead of only that it was refused.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	status := response.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Make rejected the API token; a " +
			"token created for a different zone is rejected here the same way as any other invalid token"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this Make API token may " +
			"not perform this operation; it needs the scenarios:read scope, added under Profile, API, when " +
			"the token was created"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Make does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(retryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Make rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Make is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Make did not answer in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Make answered with a redirect, which Qatlas does not follow for this request"}
	default:
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
	if err := client.get(ctx, op, "/scenarios", query, &page); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds Make metadata, its read-only connection test, and its read operations. There is no manage
// profile yet: every tool of Milestone A is a read, and a later milestone adds a manage profile the same
// way n8n's own does, once it has a change tool to offer.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Make", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Visual automation platform, scenarios and their run history read through its zone-" +
			"scoped REST API",
		ValidateBaseURL: func(raw string) error {
			_, err := parseInstance(raw)
			return err
		},
		SecretRoles: []config.SecretRole{{
			Name: roleAPIToken,
			Description: "Make API token, created in Make under the profile avatar, Profile, API, Add token, " +
				"with at least the scenarios:read scope; a token belongs to one zone only, so a token of a " +
				"different zone than the connection's own is rejected as invalid, and it reaches every team " +
				"its owner belongs to, which is why this connection's own team target decides what is exposed",
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
				"the bound organization",
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
	)
}

// readTools are the five tools of Milestone A. A future manage profile reuses this list instead of
// repeating it, the same way n8n's own manage profile does.
var readTools = []string{scenariosList.ID, scenariosGet.ID, scenariosBlueprint.ID, runsList.ID, runsGet.ID}
