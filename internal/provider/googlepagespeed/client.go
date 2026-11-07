package googlepagespeed

import (
	"bytes"
	"context"
	"encoding/json"
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

// The only origins this provider talks to. The key belongs to these two services, so any other origin could
// only be a mistake or an exfiltration route. Both paths come from the discovery documents of the services.
const (
	psiRoot        = "https://pagespeedonline.googleapis.com"
	psiPath        = "/pagespeedonline/v5/runPagespeed"
	cruxRoot       = "https://chromeuxreport.googleapis.com"
	cruxRecordPath = "/v1/records:queryRecord"
	cruxSeriesPath = "/v1/records:queryHistoryRecord"
)

// Bounds of one request. The timeout stays below the 60 seconds an invoke may take, so a slow analysis ends
// as a timeout of this provider and not as a cancelled call. A Lighthouse run takes seconds to a minute.
//
// The size caps are read limits of the raw answer. A Lighthouse answer with all categories carries a full
// page screenshot and the final screenshot as base64, which is the bulk of it; 10 MiB leaves room for such an
// answer, and a larger one is refused. A Chrome UX Report answer is a few dozen kilobytes even as a 40 period
// history, so 1 MiB is generous.
const (
	maxPSIBytes  = 10 << 20
	maxCruxBytes = 1 << 20
	timeout      = 50 * time.Second
)

// minInterval spaces two requests of one key. Chrome UX Report allows 150 queries per minute per project,
// which is 400 ms; the same spacing applies to PageSpeed Insights, whose per-minute quota is not documented
// in the sources used. It is a cautious start, not a Google limit: quotas are set in the Cloud console.
const minInterval = 400 * time.Millisecond

// maxHold bounds how long a rate-limit wait may delay the next request of this process.
const maxHold = time.Minute

// userAgent names Qatlas honestly.
const userAgent = "qatlas-connect"

// limiters holds the spacing of every key this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every request. A nil value is Go's default transport; the package's own tests replace it.
var transport http.RoundTripper

// Client binds one API key to the rate limit that key shares.
type Client struct {
	key     string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// isDefaultRoot reports whether a configured base URL is the default or empty. The base URL has no effect:
// both origins are fixed, so any other value could only suggest that it does.
func isDefaultRoot(raw string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return trimmed == "" || trimmed == psiRoot
}

// checkConnection refuses what no request may start from. The messages never quote the value.
func checkConnection(resolved *config.Resolved) error {
	switch {
	case resolved == nil:
		return failure("open", "no connection was selected")
	case !isDefaultRoot(resolved.BaseURL):
		return failure("open", "a Google PageSpeed service has fixed origins; leave the base URL as "+psiRoot)
	case len(provider.TargetsOf(resolved)) > 0:
		return failure("open", "a Google PageSpeed connection takes no target")
	}
	return nil
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
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Google API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{key: value.Secret, http: provider.NoRedirectClient(timeout, transport), limiter: lim}, nil
}

// apiCall is one request. root and path are constants of this package; query holds the arguments and never
// the key; body is the JSON body of a POST.
type apiCall struct {
	op, method, root, path string
	query                  url.Values
	body                   any
	limit                  int64
	notFound               string
}

// send performs one request, once, and decodes a JSON object answer into out. The key is the query parameter
// key, so the URL is built here and nowhere else, and no error built from it is ever returned: a failure of
// the transport is normalised by provider.Transport, which drops Go's URL-quoting error text.
func (c *Client) send(ctx context.Context, call apiCall, out any) error {
	query := url.Values{}
	for name, values := range call.query {
		query[name] = values
	}
	query.Set("key", c.key)
	var body io.Reader
	if call.body != nil {
		encoded, err := json.Marshal(call.body)
		if err != nil {
			return failure(call.op, "the request body could not be built")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, call.method, call.root+call.path+"?"+query.Encode(), body)
	if err != nil {
		return failure(call.op, "the request could not be built")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(call.op, "Google", err)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(call.op, "Google", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		raw := provider.ReadErrorBody(response.Body)
		if response.StatusCode == http.StatusTooManyRequests {
			// A missing or unusable retry time still means the key sent too much; the next call waits, and
			// nothing repeats this one.
			c.limiter.HoldFor(min(max(provider.RetryAfter(response.Header), time.Second), maxHold))
		}
		return googleError(call.op, response.StatusCode, raw, call.notFound)
	}
	if mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type")); err != nil ||
		mediaType != "application/json" {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
		return provider.InvalidResponse(call.op, "Google returned an answer that is not JSON")
	}
	if failed := provider.ReadJSON(call.op, "Google", response.Body, call.limit, out); failed != nil {
		return failed
	}
	return nil
}
