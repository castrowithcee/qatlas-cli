// Package seatableaccount implements controlled access to the snapshots of one SeaTable base through the
// account-level REST API (api.seatable.com/reference, checked 2026-09-30): listing a base's snapshots and
// restoring one. A restore creates a new base in the account; that base lies outside every connection.
//
// The connection is bound to exactly one base, WORKSPACE_ID/BASE_NAME. Both values come only from that
// target, never from an agent argument. snapshots.restore sends exactly one changing request and is never
// retried: a failure that could mean it nonetheless arrived is reported as uncertain. The account token is
// broad (it is the login of a person), which is why this provider is separate from the base-token provider
// and why Qatlas limits it through the target, the tools list, and the permissions.
package seatableaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "seatableaccount"

// roleAccountToken is the single secret role: the account token, sent as a Bearer token.
const roleAccountToken = "account-token"

const (
	cloudOrigin     = "https://cloud.seatable.io"
	dataSensitivity = "seatable-snapshots"
	maxResponseSize = 1 << 20
	defaultTimeout  = 30 * time.Second
	maxStringLength = 512
)

// uncertain is appended to a failure of the restore request that may have reached SeaTable.
const uncertain = "; the restore may have created a base, check the SeaTable account before repeating it"

var limiters = ratelimit.NewRegistry(0)

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// Client binds one account token to the origin and the one base of its connection.
type Client struct {
	origin  string
	base    boundBase
	token   string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the account token of one selected connection and returns a client for its bound base.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	base, err := baseOf(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAccountToken)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the SeaTable account token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{origin: origin, base: base, token: value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
}

// originOf accepts a bare https origin: cloud or self-hosted, but never user info, a path, a query, or a
// fragment.
func originOf(raw string) (string, error) {
	const reason = "a SeaTable service must be a bare https origin without user, path, query, or fragment"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", errors.New(reason)
	}
	return "https://" + parsed.Host, nil
}

// basePath is the fixed path of the bound base's snapshots, built only from the target.
func (c *Client) basePath() string {
	return "/api/v2.1/workspace/" + c.base.workspaceID + "/dtable/" + url.PathEscape(c.base.name) + "/snapshots/"
}

// do sends one bounded request. note marks a changing request: a failure that could mean it arrived has
// note appended and the request is never repeated here.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, out any, note string) error {
	changing := note != ""
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "SeaTable", err)
	}
	endpoint := c.origin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if changing {
		body = bytes.NewReader([]byte("{}"))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if changing {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "SeaTable", err)
		if changing && failure.MayHaveArrived() {
			failure.Message += note
		}
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.responseError(op, response)
		if changing && response.StatusCode >= 500 {
			failure.Message += note
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return invalidResponse(op, "the SeaTable response could not be read within the size limit"+note)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, "SeaTable returned an invalid response"+note)
	}
	return nil
}

// responseError maps an HTTP status to a stable class; the provider body is never read into the message.
func (c *Client) responseError(op string, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSize))
	if response.StatusCode == http.StatusTooManyRequests {
		c.limiter.HoldFor(provider.RetryAfter(response.Header))
	}
	return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject:    "SeaTable",
		Auth:       "SeaTable rejected the account token",
		Permission: "this SeaTable account may not perform this operation on the bound base",
		NotFound:   "SeaTable does not hold this resource or does not show it to this account",
	})
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

func invalidRequest(message string) error { return &provider.InvalidRequestError{Message: message} }

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

// TestConnection performs the smallest safe authenticated read: one snapshot entry of the bound base.
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
	var page snapshotPageJSON
	query := url.Values{"page": {"1"}, "per_page": {"1"}}
	if err := client.do(ctx, "test connection", http.MethodGet, client.basePath(), query, &page, ""); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds the provider metadata, its connection test, and its two operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "SeaTable account", DefaultBaseURL: cloudOrigin,
		Description:        "SeaTable account API, snapshots of one base listed and restored into a new base",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleAccountToken,
			Description: "SeaTable account token of a person, created by that person with POST /api2/auth-token/ " +
				"(username and password, plus a one-time password where two-factor authentication is on). " +
				"The token is broad: it reaches everything the account reaches, which is why the connection's " +
				"base target, tools list, and permissions decide what is exposed",
		}},
		Target: config.TargetMetadata{
			Label:    "base",
			Required: true,
			Description: "exactly one base as WORKSPACE_ID/BASE_NAME; every request is built from it and never " +
				"from an argument",
			Kinds: []config.TargetKind{{
				Name:        "base",
				Description: "the one base whose snapshots this connection may list and restore",
				Forms:       []string{"WORKSPACE_ID/BASE_NAME"},
			}},
			Validate:    func(raw string) error { _, err := parseTarget(raw); return err },
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "List snapshots", Recommended: true,
			Description: "lists the snapshots of the bound base; changes nothing",
			Tools:       []string{snapshotsList.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: snapshotsList, Handler: capability.Handler(invokeSnapshotsList)},
		capability.Operation{Descriptor: snapshotsRestore, Handler: capability.Handler(invokeSnapshotsRestore)},
	)
}
