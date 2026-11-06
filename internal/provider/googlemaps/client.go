package googlemaps

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// apiRoot is the only origin this provider talks to: the key belongs to this one service, so a second
// origin could only be a mistake or an exfiltration route.
const apiRoot = "https://places.googleapis.com"

// Bounds of one request. The timeout stays below the 60 seconds an invoke may take, so a slow answer ends
// as a timeout of this provider and not as a cancelled call.
const (
	maxResponseBytes = 2 << 20
	defaultTimeout   = 30 * time.Second
)

// minInterval is the spacing of two requests of one key. It is a cautious start of this provider, not a
// documented limit: the quota of a project is set in its Cloud console.
const minInterval = 250 * time.Millisecond

// maxHold bounds how long a Retry-After wait may delay the next request of this process.
const maxHold = time.Minute

// userAgent names Qatlas honestly.
const userAgent = "qatlas-connect"

// limiters holds the spacing of every key this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every request. A nil value is Go's default transport; the package's own tests replace it
// with a local test server.
var transport http.RoundTripper

// Client binds one API key to the rate limit that key shares.
type Client struct {
	key     string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// checkConnection refuses what no request may start from: no connection, a base URL other than the official
// origin, and any target. The messages never quote the value.
func checkConnection(resolved *config.Resolved) error {
	switch {
	case resolved == nil:
		return failure("open", "no connection was selected")
	case !isAPIRoot(resolved.BaseURL):
		return failure("open", "a Google Maps service must use the official API origin "+apiRoot)
	case len(provider.TargetsOf(resolved)) > 0:
		return failure("open", "a Google Maps connection takes no target")
	}
	return nil
}

// isAPIRoot reports whether a configured base URL names the official origin. An empty value and a trailing
// slash are the only variations a configuration may carry.
func isAPIRoot(raw string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return trimmed == "" || trimmed == apiRoot
}

// Open checks the connection and resolves its key. Anything wrong with the connection itself ends before
// the key is read. The key is registered with the redactor at once.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if err := checkConnection(resolved); err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, failure("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleKey)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Google Maps API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{key: value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
}

// get sends one GET request, once, and decodes a JSON object answer. path is below the API root and query is
// encoded into the URL; neither ever holds the key. The key and the field mask travel as headers only.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, mask string) (map[string]any, error) {
	endpoint := apiRoot + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, failure(op, "the request could not be built")
	}
	req.Header.Set("X-Goog-Api-Key", c.key)
	req.Header.Set("X-Goog-FieldMask", mask)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Google", err)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Google", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body := provider.ReadErrorBody(response.Body)
		if response.StatusCode == http.StatusTooManyRequests {
			// A missing or unusable Retry-After still means the key sent too much; the next call waits, and
			// nothing repeats this one.
			c.limiter.HoldFor(min(max(provider.RetryAfter(response.Header), time.Second), maxHold))
		}
		return nil, googleError(op, response.StatusCode, body)
	}
	if mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type")); err != nil ||
		mediaType != "application/json" {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
		return nil, provider.InvalidResponse(op, "Google returned an answer that is not JSON")
	}
	var out map[string]any
	if failed := provider.ReadJSON(op, "Google", response.Body, maxResponseBytes, &out); failed != nil {
		return nil, failed
	}
	if out == nil {
		return nil, provider.InvalidResponse(op, "Google returned an invalid response")
	}
	return out, nil
}
