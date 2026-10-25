package brave

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// apiRoot is the only origin this provider talks to: the key belongs to this one service, so a second
// origin could only be a mistake or an exfiltration route.
const apiRoot = "https://api.search.brave.com"

// dataSensitivity classifies results as content of third parties that a search returns.
const dataSensitivity = "third-party-search-results"

// Bounds of one request. The timeout stays below the 60 seconds an invoke may take, so a slow answer ends
// as a timeout of this provider and not as a cancelled call.
const (
	maxResponseBytes = 4 << 20
	defaultTimeout   = 30 * time.Second
)

// minInterval is the spacing of two requests of one key before an answer told the real limit. Plans differ
// from one request per second up to many, and the first answer raises the spacing where the plan needs it.
const minInterval = 250 * time.Millisecond

// maxHold bounds how long a reported rate-limit wait may delay the next request of this process. A window
// that resets later than this is a quota, which ends the call instead of waiting.
const maxHold = time.Minute

// userAgent names Qatlas honestly: Brave's terms of use forbid hiding the identity of the application.
const userAgent = "qatlas-cli"

// limiters holds the spacing of every key this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every Brave request. A nil value is Go's default transport, which also asks for and
// decodes gzip by itself; the package's own tests replace it with a local test server.
var transport http.RoundTripper

// Client binds one API key to the rate limit that key shares.
type Client struct {
	token   string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open checks the connection and resolves its key. Anything wrong with the connection itself ends before
// the key is read.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam: a caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches Brave.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if err := checkConnection(resolved); err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, failure("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Brave API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{token: value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
}

// checkConnection refuses what no request may start from: no connection, a base URL other than the official
// origin, and any target. The messages never quote the value.
func checkConnection(resolved *config.Resolved) error {
	switch {
	case resolved == nil:
		return failure("open", "no connection was selected")
	case !isAPIRoot(resolved.BaseURL):
		return failure("open", "a Brave service must use the official API origin "+apiRoot)
	case len(provider.TargetsOf(resolved)) > 0:
		return failure("open", "a Brave connection takes no target")
	}
	return nil
}

// isAPIRoot reports whether a configured base URL names the official origin. An empty value and a trailing
// slash are the only variations a configuration may carry.
func isAPIRoot(raw string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return trimmed == "" || trimmed == apiRoot
}

// get sends one GET request, once, and decodes a JSON answer into out. query is encoded into the URL; extra
// carries further request headers of the caller and never the key. The returned rate information is that
// of the answer, also when the request failed.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, extra http.Header,
	out any) (rateInfo, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return rateInfo{}, provider.Waited(op, "Brave", err)
	}
	endpoint := apiRoot + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return rateInfo{}, failure(op, "the request could not be built")
	}
	for name, values := range extra {
		req.Header[name] = values
	}
	// The key is set last so that no caller header can replace it.
	req.Header.Set("X-Subscription-Token", c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	response, err := c.http.Do(req)
	if err != nil {
		return rateInfo{}, provider.Transport(op, "Brave", err)
	}
	defer response.Body.Close()

	info := parseRate(response.Header)
	c.pace(info)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return info, c.refusal(op, response.StatusCode, info)
	}
	if failed := provider.ReadJSON(op, "Brave", response.Body, maxResponseBytes, out); failed != nil {
		return info, failed
	}
	return info, nil
}

// pace slows the next request of this key by what the answer said about the plan: at least the spacing the
// per-second limit implies, and, when a short window is used up, until it resets.
func (c *Client) pace(info rateInfo) {
	if first, ok := info.first(); ok && first.limit != nil && *first.limit > 0 {
		c.limiter.HoldFor(time.Second / time.Duration(*first.limit))
	}
	if short, _ := info.exhausted(); short > 0 {
		c.limiter.HoldFor(short)
	}
}

// refusal maps an HTTP status to a stable class. It never reads the body: error.detail and error.meta are
// Brave's text and may echo request values. Only what Brave answers differently from the common mapping is
// decided here; the rest goes through provider.ClassifyStatus. The mapping of 402 and 403 to a plan or a
// balance is not documented, so those messages name both possibilities and claim neither.
func (c *Client) refusal(op string, status int, info rateInfo) *provider.Error {
	failed := func(class provider.Class, message string) *provider.Error {
		return &provider.Error{Class: class, Op: op, Message: message, Original: &provider.Original{Status: status}}
	}
	switch status {
	case http.StatusPaymentRequired:
		return failed(provider.ClassPermission, "Brave refused the request for the plan or balance of this key; "+
			"check both in the Brave dashboard")
	case http.StatusTooManyRequests:
		// An answer without usable headers still means the key sent too much: wait out the sliding second.
		c.limiter.HoldFor(time.Second)
		return failed(provider.ClassRateLimited, rateLimitedMessage(info))
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Qatlas checked the arguments before sending, so this is a mismatch with the API. Brave also
		// answers 422 for a key it does not accept, and gives no field Qatlas could safely name.
		return failed(provider.ClassInvalidResponse, fmt.Sprintf("Brave rejected the request as invalid "+
			"(HTTP %d) although Qatlas checked it; the API may have changed, or the key is not accepted", status))
	case http.StatusBadGateway:
		return failed(provider.ClassUnreachable, "Brave is unavailable")
	}
	mapped := provider.ClassifyStatus(op, status, provider.StatusTexts{
		Subject:    "Brave",
		Auth:       "Brave rejected the API key; check the key and whether its plan includes this endpoint",
		Permission: "Brave refused this key for the endpoint; its plan may not include it",
		NotFound:   "Brave did not find the endpoint; the plan of this key may not include it",
	})
	mapped.Original = &provider.Original{Status: status}
	return mapped
}

// rateLimitedMessage says what a 429 means: a window that resets soon is waited out by the next call, one
// that resets later is a quota that only time or a plan change refills.
func rateLimitedMessage(info rateInfo) string {
	short, long := info.exhausted()
	switch {
	case long > 0:
		return fmt.Sprintf("the Brave quota of this key is used up and resets in %d seconds; "+
			"wait for the reset or change the plan", seconds(long))
	case short > 0:
		return fmt.Sprintf("Brave rate-limited the key; retry after %d seconds", seconds(short))
	}
	return "Brave rate-limited the key"
}

// seconds rounds a duration up to whole seconds.
func seconds(d time.Duration) int { return int((d + time.Second - 1) / time.Second) }

// failure is a provider error of the generic class.
func failure(op, message string) *provider.Error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// badRequest is the refusal of an argument. It names the argument and the rule, never the value.
func badRequest(message string) error {
	return &provider.InvalidRequestError{Message: message}
}
