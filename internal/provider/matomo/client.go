package matomo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxResponseSize = 4 << 20
	defaultTimeout  = 30 * time.Second
	maxStringLength = 512
	// rateLimitHold is how long the key rests after a 429 that names no wait; maxRateLimitHold caps a named one.
	rateLimitHold    = 5 * time.Second
	maxRateLimitHold = 60 * time.Second
	subject          = "Matomo"
)

// Matomo Cloud limits requests per IP; on-premise installations have no API limit of their own. A quarter
// second between two requests of one token stays below the Cloud limit of 350 requests per minute.
var limiters = ratelimit.NewRegistry(250 * time.Millisecond)

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// Client binds one token to the origin and the website scope of its connection.
type Client struct {
	origin     string
	token      string
	connection string
	scope      scope
	http       *http.Client
	limiter    *ratelimit.Limiter
	key        instanceKey
}

// Open resolves the token of one selected connection. The base URL and the targets are checked first, so a
// connection that cannot be valid never reads its secret.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, provider.Fail(op, "no connection was selected")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	return build(ctx, resolved, secrets, red, bound)
}

// build is Open for a scope already settled; choosing targets passes every website, because the targets it
// helps to choose may not exist yet.
func build(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	bound scope) (*Client, error) {
	const op = "open"
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, provider.Fail(op, err.Error())
	}
	if secrets == nil {
		return nil, provider.Fail(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Matomo token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	return &Client{
		origin: origin, token: value.Secret, connection: resolved.Name, scope: bound,
		http: provider.NoRedirectClient(defaultTimeout, transport), limiter: limiters.For(value.Secret),
		key: instanceKeyOf(origin, value.Secret),
	}, nil
}

// originOf accepts an https URL of a Matomo installation, optionally below an installation path, never with
// user info, a query, or a fragment. index.php is appended to it. The error quotes no part of the value.
func originOf(raw string) (string, error) {
	const reason = "a Matomo connection needs a base URL of the form https://HOST[/PATH], without user, query, or fragment"
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.Opaque != "" || strings.ContainsAny(raw, "?#\\ ") {
		return "", errors.New(reason)
	}
	return "https://" + parsed.Host + strings.TrimSuffix(parsed.EscapedPath(), "/"), nil
}

// parameters a request never carries: the client sets the fixed ones itself, and the others change the shape
// or the size of an answer in ways no tool of this provider relies on.
var reservedParameters = map[string]bool{
	"module": true, "method": true, "format": true, "token_auth": true, "callback": true, "jsoncallback": true,
	"disable_generic_filters": true, "disable_queued_filters": true, "disable_root_datatable_post_processor": true,
}

// send performs one Matomo API call and decodes its JSON answer into out. It makes no availability checks
// and never retries; call is the entry point of a tool. The token is sent as a bearer header and, only when
// bodyToken is set because the instance predates header support, also as a form field.
func (c *Client) send(ctx context.Context, op, method string, params url.Values, bodyToken bool, out any) error {
	form := url.Values{}
	for name, values := range params {
		if reservedParameters[name] || name == "filter_limit" && len(values) > 0 && values[0] == "-1" {
			return provider.Fail(op, "the request carries a parameter this provider never sends")
		}
		form[name] = values
	}
	form.Set("module", "API")
	form.Set("method", method)
	form.Set("format", "JSON")
	if bodyToken {
		form.Set("token_auth", c.token)
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, subject, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/index.php", strings.NewReader(form.Encode()))
	if err != nil {
		return provider.Fail(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, subject, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.failure(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return provider.InvalidResponse(op, "the Matomo response could not be read within the size limit")
	}
	// An HTML login page or an XML answer is no valid JSON; neither is read as a result.
	if !json.Valid(data) {
		return provider.InvalidResponse(op, "Matomo returned an answer that is not JSON")
	}
	if failure := applicationError(op, data); failure != nil {
		return failure
	}
	if err := json.Unmarshal(data, out); err != nil {
		return provider.InvalidResponse(op, "Matomo returned an invalid response")
	}
	return nil
}

// failure maps an HTTP status other than 200 to a stable class. The message holds no text of the body; the
// body goes apart into Original: the message of Matomo's error form, or the text of a non-JSON answer.
func (c *Client) failure(op string, response *http.Response) *provider.Error {
	body := provider.ReadErrorBody(response.Body)
	fields := provider.OriginalFields{}
	var envelope struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Result == "error" {
		fields = envelopeFields
	}
	if response.StatusCode == http.StatusTooManyRequests {
		// Matomo Cloud documents no header for its limit. Without a named wait the key rests briefly, and
		// nothing here ever retries the request.
		hold := provider.RetryAfter(response.Header)
		if hold <= 0 {
			hold = rateLimitHold
		}
		c.limiter.HoldFor(min(hold, maxRateLimitHold))
	}
	failure := provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject:    subject,
		Auth:       "Matomo rejected the token",
		Permission: "Matomo refused the operation for this token",
		NotFound:   "Matomo does not hold this resource or does not show it to this token",
	})
	failure.Original = provider.ErrorOriginal(response.StatusCode, body, fields)
	return failure
}

// call runs one fixed method of a tool after the instance supports the tool: version, plugin, and method
// are decided by the requirement and the instance, without a request when the instance is known.
func (c *Client) call(ctx context.Context, op string, req requirement, method string, params url.Values, out any) error {
	info, err := c.ensure(ctx, op, req)
	if err != nil {
		return err
	}
	return c.refuseMissing(req, c.send(ctx, op, method, params, info.bodyToken, out))
}

// refuseMissing turns Matomo's answer for a method, module, or plugin it lacks into the refusal of the tool.
func (c *Client) refuseMissing(req requirement, err error) error {
	var missing *missingError
	if errors.As(err, &missing) {
		if missing.granular {
			return c.unavailable(req, "Matomo's granular compliance feature is not enabled; a Super User sets "+
				`[FeatureFlags] GranularPrivacyCompliance_feature = "enabled" in the Matomo configuration`)
		}
		return c.unavailable(req, "the instance does not provide the method this tool calls")
	}
	return err
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

type instanceKey [sha256.Size]byte

// instanceKeyOf identifies one instance seen with one token without keeping a copy of the token.
func instanceKeyOf(origin, token string) instanceKey {
	return sha256.Sum256([]byte(origin + "\x00" + token))
}
