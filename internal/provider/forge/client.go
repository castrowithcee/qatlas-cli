package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Client binds one access token to the origin of its instance and the rate limit that token shares. The
// targets of its connection are checked by the operation that opened it.
type Client struct {
	product *product
	origin  string
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// originOf accepts https://HOST, optionally with a port and a trailing slash, never with a path, user info, a
// query, or a fragment. An instance below a path prefix is not supported. The error never quotes the value.
func originOf(raw string) (string, error) {
	const reason = "a Forgejo or Gitea service needs its base URL as https://HOST, without a path, user, query, " +
		"or fragment"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New(reason)
	}
	return "https://" + strings.ToLower(parsed.Host), nil
}

// open checks the connection's base URL and targets, then resolves its token. Nothing is sent.
func open(ctx context.Context, p *product, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, provider.Fail(op, "no connection was selected")
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, provider.Fail(op, err.Error())
	}
	if _, err := allowlistOf(resolved); err != nil {
		return nil, err
	}
	token, err := resolveToken(ctx, p, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return &Client{product: p, origin: origin, auth: "token " + token,
		http: provider.NoRedirectClient(defaultTimeout, transport), limiter: limiters.For(token)}, nil
}

// resolveToken reads the token of the connection and registers it with the redactor.
func resolveToken(ctx context.Context, p *product, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (string, error) {
	if secrets == nil {
		return "", provider.Fail("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return "", err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return "", &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the " + p.name + " token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "token "+value.Secret)
	}
	return value.Secret, nil
}

// forbiddenQuery are query parameters no request carries: they would act as another user or authenticate
// through the URL.
var forbiddenQuery = []string{"sudo", "token", "access_token"}

// get sends one GET of a fixed API path, which the caller builds from constants and escaped, validated
// segments, and decodes the JSON answer into out. It returns the response headers for paging. A read is never
// retried.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) (http.Header, error) {
	_, header, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query, out)
	return header, err
}

// send is the one place that builds and sends a request; an empty auth sends it without a credential. The
// status is that of the answer, zero when none arrived.
func send(ctx context.Context, client *http.Client, limiter *ratelimit.Limiter, p *product, root, auth, op, path string,
	query url.Values, out any) (int, http.Header, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return 0, nil, provider.Fail(op, "the request path is not one of this provider")
	}
	for key := range query {
		for _, forbidden := range forbiddenQuery {
			if strings.EqualFold(key, forbidden) {
				return 0, nil, provider.Fail(op, "the request carries a parameter this provider never sends")
			}
		}
	}
	if err := limiter.Wait(ctx); err != nil {
		return 0, nil, provider.Waited(op, p.name, err)
	}
	target := root + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, provider.Fail(op, "the request could not be built")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, provider.Transport(op, p.name, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, nil, refusal(op, p, limiter, response)
	}
	if failure := provider.ReadJSON(op, p.name, response.Body, maxResponseBytes, out); failure != nil {
		return response.StatusCode, nil, failure
	}
	return response.StatusCode, response.Header, nil
}

// refusal maps an HTTP status to a stable class; the provider body is never read into the message. 403 and
// 404 are both "not reachable": an instance answers 404 instead of 403 to a token bound to other repositories
// or to public ones only, so neither proves that a resource exists or not.
func refusal(op string, p *product, limiter *ratelimit.Limiter, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: p.name + " does not show this " +
			"resource to this token; it may not exist, or the token's scopes or repository binding do not reach it"}
	case http.StatusTooManyRequests:
		limiter.HoldFor(min(provider.RetryAfter(response.Header), maxHold))
	}
	return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject: p.name,
		Auth:    p.name + " rejected the token; check that it is valid and not expired",
	})
}

func invalidArgument(message string) error { return &provider.InvalidRequestError{Message: message} }

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	if len(value) > maxStringLength {
		value = value[:maxStringLength]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

// object is one provider object as raw members, read by the projections.
type object map[string]json.RawMessage

func (o object) text(key string) (string, bool) {
	var value string
	if json.Unmarshal(o[key], &value) != nil {
		return "", false
	}
	return bounded(value), true
}

func (o object) integer(key string) (int64, bool) {
	var value int64
	if json.Unmarshal(o[key], &value) != nil {
		return 0, false
	}
	return value, true
}

func (o object) flag(key string) (bool, bool) {
	var value bool
	if json.Unmarshal(o[key], &value) != nil {
		return false, false
	}
	return value, true
}

func (o object) child(key string) (object, bool) {
	var value object
	if json.Unmarshal(o[key], &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}
