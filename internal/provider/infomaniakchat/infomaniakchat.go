// Package infomaniakchat implements controlled access to one Infomaniak kChat instance: kChat is
// Infomaniak's Mattermost-compatible team chat product, reached below an instance-owned base URL through
// the same /api/v4/... REST surface Mattermost documents, authenticated with a personal access token as a
// bearer credential (developer.infomaniak.com/openapi.json, operations GetTeamsForUser, GetChannel,
// GetChannelsForTeamForUser, GetPostsForChannel, GetPost, GetPostThread, and CreatePost, all marked
// x-auth-user with the bearerAuth security scheme). A kChat instance's base URL is always the team's own
// name as the one DNS label directly below kchat.infomaniak.com, never an arbitrary host: Infomaniak's own
// kChat MCP server (github.com/Infomaniak/mcp-server-kchat) builds every request from exactly that shape,
// https://TEAM.kchat.infomaniak.com/api/v4/..., and this provider accepts no other host, see parseInstance.
//
// A connection binds exactly one kChat instance, through its configured base URL, and one or more teams of
// it (team/TEAM_ID, repeatable) plus, optionally, a narrower allow-list of channels of those teams
// (channel/CHANNEL_ID, repeatable); without a channel allow-list, every channel of the bound teams the
// token can reach is reachable. This mirrors the kDrive provider's account-plus-drive boundary for the same
// reason: a single kChat personal token can belong to every team its owner is a member of, which matters
// for a person who holds tokens, or is a member of teams, of several customers. A channel_id argument
// outside a configured channel allow-list is refused locally, before any request is sent; every operation
// that names a channel_id directly (messages.list, messages.send) also confirms with kChat's own channel
// detail endpoint, in one extra request, that the channel actually belongs to one of the bound teams, and
// is refused before the matching endpoint is reached when it does not. A message reply additionally
// confirms that its root post belongs to the same channel before it is ever sent. Rejecting an out-of-scope
// channel or root post never carries a message's text into an error or a log.
//
// kChat is deliberately the only Infomaniak surface this provider reaches: Mail, CalDAV/CardDAV, and kDrive
// are separate Infomaniak products with their own authentication and their own providers, see the kDrive
// provider's package doc for why they are not bundled together.
//
// Channel and team management, editing or deleting a message, file attachments, reactions, and webhooks are
// deliberately out of scope: this provider lists the teams and channels a connection may reach, reads
// channel posts and threads page by page, and sends or replies with exactly one confirmed plain-text
// message. Every value a listing or a read answers with arrives from the provider and is treated as
// untrusted data: normalised into a stable envelope, passed through the output encoders, and never
// rendered, executed, or stored.
//
// kChat publishes no documented request budget the way kDrive's shared API does, so this provider applies
// no proactive spacing of its own; a 429 kChat itself reports is still classified and, when it names a
// Retry-After, holds this connection's own limiter before the next request.
package infomaniakchat

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

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "infomaniakchat"

// roleToken is the single secret role a kChat credential must supply. It is used as a bearer token.
const roleToken = "token"

// dataSensitivity classifies results as team, channel, and message data of the configured kChat instance.
const dataSensitivity = "infomaniak-kchat-messages"

// Bounds of one request and one answer.
const (
	maxResponseBytes = 4 << 20
	defaultTimeout   = 30 * time.Second
	// maxMessageLength is a local safety bound on the size of a request body this provider ever builds; the
	// OpenAPI contract of CreatePost declares no maximum of its own, and a real instance may configure a
	// different one, so a message inside this bound can still be rejected by kChat itself.
	maxMessageLength = 4000
	defaultListLimit = 50
	maxListLimit     = 200
)

// limiters holds the rate-limit budget of every token this process has used. No steady-state interval is
// enforced proactively, see the package doc; only a reported Retry-After holds it.
var limiters = ratelimit.NewRegistry(0)

// idSchema is the JSON Schema of one kChat identifier: a plain path segment of lowercase letters and
// digits, never a free-form value. Its pattern mirrors validMattermostID exactly.
var idSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxIDLength) + `,"pattern":"^[a-z0-9]{1,` +
	itoa(maxIDLength) + `}$"}`

// itoa avoids importing strconv into every descriptor file just to build a schema literal.
func itoa(n int) string { return strconv.Itoa(n) }

// Client binds one kChat personal access token to the instance origin and the team/channel scope of its
// connection.
type Client struct {
	scope   scope
	origin  string
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the token of one selected connection and returns a client for its bound instance and scope.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches a real kChat instance.
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
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the kChat personal access token is unusable"}
	}
	auth := "Bearer " + value.Secret
	if red != nil {
		red.Add(value.Secret, auth)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{scope: bound, origin: origin, auth: auth, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
}

// kchatDomain is the fixed domain every kChat instance lives directly below. Infomaniak's own kChat MCP
// server (github.com/Infomaniak/mcp-server-kchat, src/kchat-client.ts) builds every one of its requests as
// https://${teamName}.kchat.infomaniak.com/api/v4/..., so a kChat instance's host is always exactly the
// team name as one DNS label directly below this domain, never an arbitrary host: a configured value one
// label short, one label too many, or one that merely ends with this domain's text as part of a longer,
// foreign host is refused, not silently trusted.
const kchatDomain = "kchat.infomaniak.com"

// instanceReason is the one refusal message of a malformed base URL. It never quotes the value that was
// refused, so a configuration mistake, or a manipulated configuration, is refused before a secret is read
// and never echoed back.
const instanceReason = "a kChat service needs a plain https URL of the form https://TEAM." + kchatDomain +
	", with no port, path, user, query, or fragment"

// parseInstance validates the configured base URL of one kChat instance: a plain https origin whose host
// is exactly one DNS label, the team name, directly below kchatDomain, with no port, path, user, query, or
// fragment, so it can only ever be replayed as the root of the fixed /api/v4/... paths this provider
// builds and never redirected or rewritten to a host this provider has not already validated. The host is
// normalised to lowercase, because DNS names are case-insensitive.
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
	suffix := "." + kchatDomain
	if !strings.HasSuffix(host, suffix) {
		return "", errors.New(instanceReason)
	}
	if !validTeamLabel(strings.TrimSuffix(host, suffix)) {
		return "", errors.New(instanceReason)
	}
	return "https://" + host, nil
}

// validTeamLabel checks the team name as one DNS label: lowercase letters, digits, and hyphens, 1 to 63
// characters, never starting or ending with a hyphen, and never itself containing a dot, so a host with an
// extra subdomain level below the team name (or the team name spanning more than one label) is refused
// rather than silently trusted.
func validTeamLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// transport carries every kChat request. A nil value is Go's default transport; the package's own tests
// replace it with a local fake.
var transport http.RoundTripper

// uncertain is appended to a failure of the send operation whose request may have reached kChat: the
// message may have been posted although no confirmation ever arrived. Qatlas never repeats such a request
// by itself.
const uncertain = "; the message may have been sent, read the channel before sending it again"

// do sends one bounded request below the instance origin, with a JSON body when body is not nil, and
// decodes the answer into out when kChat sends one and out is not nil. change marks a request that may
// change kChat's state: every failure of it that could mean the request nonetheless arrived says so, so
// this provider never repeats it by itself.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, body any, out any,
	change bool) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "kChat", err)
	}
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return providerError(op, "the request could not be built")
		}
		payload = encoded
	}
	endpoint := c.origin + path
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
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "kChat", err)
		if change && failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response)
		if change && response.StatusCode >= 500 {
			failure.Message += uncertain
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		message := "the kChat response could not be read within the size limit"
		if change {
			message += uncertain
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		message := "kChat returned an invalid response"
		if change {
			message += uncertain
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	return nil
}

// statusError maps an HTTP status to a stable class. kChat's own error body is never read into the
// message: it may echo request content such as a channel name back at the caller.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	status := response.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "kChat rejected the personal access token"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this kChat token may not " +
			"perform this operation; check its rights on the team or channel in kChat"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "kChat does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(provider.RetryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "kChat rate-limited the operation"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "kChat answered with a redirect, which Qatlas does not follow"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "kChat did not answer in time"}
	case status == http.StatusBadRequest:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "kChat rejected the request as invalid"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("kChat rejected the operation (HTTP %d)", status)}
	}
}

// verifyChannelScope confirms, with kChat's own channel detail endpoint (GET /api/v4/channels/{channel_id}),
// that a channel actually belongs to one of the teams this connection is bound to, and returns its team ID.
// It runs before every operation that names a channel_id directly: a configured channel allow-list is
// local configuration and proves nothing about what a channel_id really resolves to on the live instance,
// exactly as the kDrive provider's drive-account check. A channel of another team is reported the same way
// as one outside the allow-list: an invalid request, never a provider error, and the failure never carries
// the channel's name or purpose.
func (c *Client) verifyChannelScope(ctx context.Context, op, channelID string) (string, error) {
	var ch channelJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID), nil, nil, &ch, false); err != nil {
		return "", err
	}
	if ch.ID != channelID || !c.scope.allowsTeam(ch.TeamID) {
		return "", invalidRequest("channel_id belongs to a team outside the targets of this connection")
	}
	return ch.TeamID, nil
}

// TestConnection performs the smallest safe authenticated read: the teams of the current token. It proves
// that the token is accepted; it says nothing about a team or channel allow-list narrower than that,
// because every named team and channel is checked again on its own request.
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
	var teams []teamJSON
	if err := client.do(ctx, op, http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds Infomaniak kChat metadata, its read-only connection test, and its read and send operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Infomaniak kChat",
		Description:        "Infomaniak kChat team messaging, read and sent through its Mattermost-compatible REST API",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		ValidateBaseURL: func(raw string) error {
			_, err := parseInstance(raw)
			return err
		},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "kChat personal access token, created in kChat under Profile, Security, Personal Access " +
				"Tokens; it reaches every team and channel its owner belongs to on this one kChat instance, so " +
				"the connection target decides which teams, and optionally which channels, Qatlas exposes",
		}},
		Target: config.TargetMetadata{
			Label:    "teams and channels",
			Required: true,
			Multiple: true,
			Description: "one or more team/TEAM_ID targets this connection is bound to, plus an optional, " +
				"repeatable allow-list of channel/CHANNEL_ID targets of those teams; without a channel target, " +
				"every channel of the bound teams the token can reach is reachable. A channel_id or root_id " +
				"argument outside a configured allow-list, or outside a bound team, is refused locally or " +
				"against the live instance before the matching request, and a message is refused before it is " +
				"ever sent",
			Kinds: []config.TargetKind{{
				Name:        "team",
				Description: "one kChat team this connection may reach; required, and may be listed more than once",
				Forms:       []string{"team/TEAM_ID"},
			}, {
				Name:        "channel",
				Description: "one channel of a bound team; optional, and may be listed more than once",
				Forms:       []string{"channel/CHANNEL_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read teams, channels, and messages", Recommended: true,
			Description: "lists the bound teams and their channels, and reads channel messages and threads " +
				"page by page; changes nothing",
			Tools: []string{teamsList.ID, channelsList.ID, messagesList.ID, messagesThread.ID},
		}, {
			ID: "messaging", Title: "Read and send messages",
			Description: "also sends a confirmed message, or a confirmed reply to an existing thread, to a " +
				"channel of a bound team",
			Tools: []string{teamsList.ID, channelsList.ID, messagesList.ID, messagesThread.ID, messagesSend.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: teamsList, Handler: capability.Handler(invokeTeamsList)},
		capability.Operation{Descriptor: channelsList, Handler: capability.Handler(invokeChannelsList)},
		capability.Operation{Descriptor: messagesList, Handler: capability.Handler(invokeMessagesList)},
		capability.Operation{Descriptor: messagesThread, Handler: capability.Handler(invokeMessagesThread)},
		capability.Operation{Descriptor: messagesSend, Handler: capability.Handler(invokeMessagesSend)},
	)
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// invalidRequest refuses a request the connection's own configuration decides against, before any request
// reaches kChat, or a live check this provider itself ran against the instance: a channel or root post
// outside the connection's boundary, or an argument outside its schema's reach. No message ever quotes the
// refused value or any message content.
func invalidRequest(message string) error {
	return &provider.InvalidRequestError{Message: message}
}

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	const maxValueLength = 1024
	if len(value) > maxValueLength {
		return value[:maxValueLength]
	}
	return value
}

// msToRFC3339 normalises one Mattermost-style millisecond epoch time to RFC 3339 in UTC. Zero or negative
// means kChat reported none.
func msToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// windowOf slices items into the requested 1-based page of size limit, reporting the total page count and
// the item count of the whole, already filtered, list. It is this provider's own pagination over a kChat
// listing that answers with a complete array and no cursor of its own.
func windowOf[T any](items []T, page, limit int) (window []T, pages, total int) {
	total = len(items)
	pages = (total + limit - 1) / limit
	if pages == 0 {
		pages = 1
	}
	start := (page - 1) * limit
	if start < 0 || start >= total {
		return []T{}, pages, total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return items[start:end], pages, total
}
