// Package infomaniakchat implements controlled access to one Infomaniak kChat instance: kChat is Infomaniak's
// Mattermost-compatible team chat product, reached below an instance-owned base URL through the same
// /api/v4/... REST surface Mattermost documents, authenticated with a bearer token: an Infomaniak Manager API
// token with the kChat scope, or a bot token from the kChat interface, as the official kChat MCP server
// (github.com/Infomaniak/mcp-server-kchat) documents (developer.infomaniak.com/openapi.json, operations
// GetTeamsForUser, GetChannel, GetChannelMembers, GetChannelsForTeamForUser, CreateDirectChannel,
// CreateGroupChannel, GetPostsForChannel, GetPost, GetPostThread, CreatePost, PatchPost, and DeletePost,
// GetReactions, SaveReaction, and DeleteReaction, GetPinnedPosts, PinPost, and UnpinPost, GetUserThreads,
// GetUserThread, StartFollowingThread, and StopFollowingThread, GetChannelUnread, ViewChannel, SetPostUnread,
// and UpdateThreadReadForUser, GetUser,
// GetUserByUsername, GetUsers, SearchUsers, GetTeamMembersByIds, and GetUsersStatusesByIds,
// GetFileInfosForPost, GetFileInfo, and GetFile, SearchPosts, SearchFiles, and SearchChannels, all marked
// x-auth-user with the bearerAuth security scheme). A kChat instance's base URL is always the team's own name
// as the one DNS label directly below kchat.infomaniak.com, never an arbitrary host: the MCP server builds
// every request from exactly that shape, https://TEAM.kchat.infomaniak.com/api/v4/..., and this provider
// accepts no other host, see parseInstance.
//
// A connection binds exactly one kChat instance, through its configured base URL, and one or more teams of it
// (team/TEAM_ID, repeatable) plus, optionally, a narrower allow-list of channels (channel/CHANNEL_ID,
// repeatable); without a channel allow-list, every channel of the bound teams the token can reach is
// reachable, and so is every direct or group channel whose other participants are all live members of a bound
// team. This mirrors the kDrive provider's account-plus-drive boundary for the same reason: a single kChat
// token can reach several teams, which matters for a person who holds tokens, or is a member of teams, of
// several customers. A channel_id argument outside a configured channel allow-list is refused locally, before
// any request is sent; every operation that names a channel_id directly (messages.list, messages.send,
// users.list, users.search, pins.list, channels.unread, channels.markread) also confirms live, see
// verifyChannelScope, that the channel belongs to a bound team or is a direct or group channel with their
// members only, and is refused before the matching endpoint is reached when it does not. Every operation that
// names a post_id (messages.thread, messages.get, messages.files, messages.update, messages.delete,
// messages.pin, messages.unpin, messages.markunread, the reactions tools, and the threads tools, which also
// require the root post's channel to be a reachable channel of the named team) reads the post first and binds
// it to its channel the same way, see verifyPostScope; the searches
// (messages.search, files.search, channels.search) read the token's channels of the team once, see
// reachableChannels, and drop every hit outside that set, so direct and group messages, other teams, and
// channels outside the allow-list never show up; a message reply additionally confirms that its root post
// belongs to the same channel before it is ever sent. Rejecting an out-of-scope channel or post never carries
// a message's text into an error or a log.
//
// A file is reachable only through the post it is attached to: files.info and files.download read the file's
// info first and bind its post_id the same way; a file without a post is refused before any content is
// requested. The content request is the one fixed GET /api/v4/files/{file_id} whose body is streamed to a
// directory the connection releases for writing; a redirect, for example to a storage host, is a provider
// error and is never followed, and the content is never returned.
//
// A user is reachable only as the token's own user or as a live member of a bound team, see
// verifyUsersScope; every operation that names, lists, or searches users applies it before a profile or a
// presence status is returned, so users of other teams of the same instance are never shown. Users are
// reduced to a fixed allow-list of profile fields.
//
// A sidebar category is the own user's and belongs to a bound team; every category tool reads it before it
// changes it. A category shows only channels this connection may reach, see reachableChannels, and a change
// of its channels writes the channels it may not reach back unchanged. The notification tool refuses direct
// and group channels, whose participants it would otherwise have to prove, see boundChannel.
//
// kChat is deliberately the only Infomaniak surface this provider reaches: Mail, CalDAV/CardDAV, and kDrive
// are separate Infomaniak products with their own authentication and their own providers, see the kDrive
// provider's package doc for why they are not bundled together.
//
// An upload is one multipart request that Qatlas builds itself, with only the channel and one file from a
// released local path or small inline content; its channel is bound like a message's. A file created that way
// belongs to no message, so a message attaches it only after its info shows the token's own user as uploader,
// no message holding it, and, when kChat reports one, the target channel; kChat offers no way to remove an
// upload that is never attached.
//
// Creating, changing, or deleting teams, adding or removing team members, invitations, changes to other
// users, profile pictures, previews and thumbnails, the custom emoji catalog, and creating webhooks or
// changing where one posts are deliberately out of scope: this provider lists the teams, channels, and
// direct and group channels a connection may reach, reads the details and members of a bound team and,
// only when a connection's tools list names it, sets one current member's team role (the invitation identifier is never read), reads channel details and
// the public channels of a bound team, creates one confirmed channel in a bound team (only for a connection
// without a channel allow-list, because a new channel cannot be inside one), changes the display name,
// purpose, header, or handle of one confirmed public or private channel, lists the members of a public or
// private channel and the archived channels of a bound team, adds confirmed users who are the token's own
// user or members of the channel's team and, only when a connection's tools list names it, removes one
// member, sets one member's channel role, archives or restores one channel, or switches one channel between
// public and private (never in a direct or group channel, see openMemberChannel), opens one confirmed direct
// or group channel with members of the bound teams, reads channel posts, single posts, threads, reactions,
// pinned posts, and the attachments of posts (writing one to a released local directory), uploads one
// confirmed file for a message, reads, lists, and searches the users of the bound teams and reads their
// presence, searches the messages, files, and public channels of a bound team, sends or replies with exactly
// one confirmed message, optionally with own uploads attached, changes the text of one confirmed message,
// adds one confirmed reaction of the token's own user, pins or unpins one confirmed message, lists and reads
// the threads the token's own user follows and follows or unfollows one confirmed thread, reads the unread
// counts of a reachable channel and marks one confirmed channel, message, or thread as read or unread for the
// token's own user only, lists, creates, and changes the own sidebar categories of a bound team and sets the
// own notifications of one channel, and, only when a connection's tools list names it, deletes one confirmed
// message, one custom category, or removes one own reaction. Only when a connection's tools list names them,
// it also lists, reads, describes, and deletes the incoming webhooks of bound channels: a webhook's ID is the
// secret of its post URL, so those tools carry their own data sensitivity, bind every webhook through its
// channel like a post, and write a webhook back with its target unchanged. The token's own presence, custom
// status, and four display profile fields are the only user data it changes, always for the user read from
// users/me and never taken from an argument. kChat renders Markdown and mentions such as @channel in a
// message, so the text is sent as written. Every value a listing or a read answers with arrives from the
// provider and is treated as untrusted data: normalised into a stable envelope, passed through the output
// encoders, and never rendered, executed, or stored.
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

// Client binds one kChat bearer token to the instance origin and the team/channel scope of its
// connection.
type Client struct {
	scope   scope
	self    string // own user ID, read at most once per client
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
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the kChat token is unusable"}
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

// uncertainUpdate and uncertainDelete are the matching suffixes of an edit and a delete request.
const (
	uncertainUpdate = "; the message may have been changed, read it before changing it again"
	uncertainDelete = "; the message may have been deleted, read it before deleting it again"
	uncertainOpen   = "; the channel may have been opened, list the direct channels before opening it again"
)

// The refusals of a channel outside the boundary. Neither names the channel or a participant.
const (
	channelOutOfScope = "channel_id belongs to a team outside the targets of this connection"
	directOutOfScope  = "channel_id is a direct or group channel outside the targets of this connection"
)

// do sends one bounded request below the instance origin, with a JSON body when body is not nil, and
// decodes the answer into out when kChat sends one and out is not nil. change marks a request that may
// change kChat's state: every failure of it that could mean the request nonetheless arrived says so, so
// this provider never repeats it by itself.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, body any, out any,
	change bool) error {
	suffix := ""
	if change {
		suffix = uncertain
	}
	return c.doWith(ctx, op, method, path, query, body, out, suffix)
}

// doWith is do for a request whose possibly-arrived failure is reported with the given suffix; an empty
// suffix marks a read.
func (c *Client) doWith(ctx context.Context, op, method, path string, query url.Values, body any, out any,
	suffix string) error {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return providerError(op, "the request could not be built")
		}
		payload = encoded
	}
	var reqBody io.Reader
	contentType := ""
	if payload != nil {
		reqBody, contentType = bytes.NewReader(payload), "application/json"
	}
	return c.exchange(ctx, c.http, op, method, path, query, rawBody{reader: reqBody, length: int64(len(payload)),
		contentType: contentType}, out, suffix)
}

// rawBody is a request body that is already built: the reader, its exact length, and the fixed content type
// Qatlas itself chose. A nil reader sends no body.
type rawBody struct {
	reader      io.Reader
	length      int64
	contentType string
}

// exchange sends one bounded request with the given client and decodes the answer; it is the one place a
// request leaves this provider.
func (c *Client) exchange(ctx context.Context, hc *http.Client, op, method, path string, query url.Values,
	body rawBody, out any, suffix string) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "kChat", err)
	}
	endpoint := c.origin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body.reader)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	if body.reader != nil {
		req.ContentLength = body.length
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if body.reader != nil {
		req.Header.Set("Content-Type", body.contentType)
	}

	response, err := hc.Do(req)
	if err != nil {
		failure := provider.Transport(op, "kChat", err)
		if suffix != "" && failure.MayHaveArrived() {
			failure.Message += suffix
		}
		return failure
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response)
		if suffix != "" && response.StatusCode >= 500 {
			failure.Message += suffix
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		message := "the kChat response could not be read within the size limit"
		message += suffix
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		message := "kChat returned an invalid response"
		message += suffix
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
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "kChat rejected the token"}
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
	case status == http.StatusRequestEntityTooLarge:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "kChat refuses the request as too large"}
	case status == http.StatusBadRequest:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "kChat rejected the request as invalid"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("kChat rejected the operation (HTTP %d)", status)}
	}
}

// verifyChannelScope confirms, with kChat's own channel detail endpoint (GET /api/v4/channels/{channel_id}),
// that a channel is inside this connection's boundary. A team channel must belong to a bound team. A direct
// (D) or group (G) channel has no team; it is reachable only when every participant besides the token's own
// user is a live member of a bound team, see participants and verifyUsersScope. It runs before every
// operation that names a channel_id directly: a configured channel allow-list is local configuration and
// proves nothing about what a channel_id really resolves to on the live instance, exactly as the kDrive
// provider's drive-account check. Every refusal is an invalid request that never carries the channel's
// name, purpose, or participants.
func (c *Client) verifyChannelScope(ctx context.Context, op, channelID string) error {
	var ch channelJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID), nil, nil, &ch, false); err != nil {
		return err
	}
	if ch.ID != channelID {
		return invalidRequest(channelOutOfScope)
	}
	if ch.Type != channelDirect && ch.Type != channelGroup {
		if !c.scope.allowsTeam(ch.TeamID) {
			return invalidRequest(channelOutOfScope)
		}
		return nil
	}
	if ch.TeamID != "" {
		return invalidRequest(directOutOfScope)
	}
	others, ok, err := c.participants(ctx, op, ch)
	if err != nil {
		return err
	}
	if !ok {
		return invalidRequest(directOutOfScope)
	}
	allowed, err := c.verifyUsersScope(ctx, op, others)
	if err != nil {
		return err
	}
	if len(allowed) != len(others) {
		return invalidRequest(directOutOfScope)
	}
	return nil
}

// boundChannel reads a channel and returns it only when it belongs to a bound team; unlike
// verifyChannelScope it never admits a direct or group channel, which has no team.
func (c *Client) boundChannel(ctx context.Context, op, channelID string) (*channelJSON, error) {
	var ch channelJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID), nil, nil, &ch, false); err != nil {
		return nil, err
	}
	if ch.ID != channelID || !c.scope.allowsTeam(ch.TeamID) {
		return nil, invalidRequest(channelOutOfScope)
	}
	return &ch, nil
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

// Register adds Infomaniak kChat metadata, its read-only connection test, and its operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Infomaniak kChat",
		Description:        "Infomaniak kChat for bound teams: messages, threads, search, files, reactions, pins, users, own status, profile, and sidebar, team roles, channels with members and notifications, and webhooks",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		Groups:             toolGroups,
		ValidateBaseURL: func(raw string) error {
			_, err := parseInstance(raw)
			return err
		},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "kChat bearer token: an Infomaniak Manager API token with the kChat scope (valid " +
				"account-wide), or a bot token created in the kChat interface; it reaches every team and " +
				"channel its owner can reach on this one kChat instance, so the connection target decides " +
				"which teams, and optionally which channels, Qatlas exposes",
		}},
		Target: config.TargetMetadata{
			Label:    "teams and channels",
			Required: true,
			Multiple: true,
			Description: "one or more team/TEAM_ID targets this connection is bound to, plus an optional, " +
				"repeatable allow-list of channel/CHANNEL_ID targets of those teams or of direct and group " +
				"channels with their members; without a channel target, every channel of the bound teams the " +
				"token can reach, and every direct or group channel with their members only, is reachable. A " +
				"channel_id or root_id argument outside a configured allow-list, or outside that boundary, is " +
				"refused locally or against the live instance before the matching request, and a message is " +
				"refused before it is ever sent",
			Kinds: []config.TargetKind{{
				Name:        "team",
				Description: "one kChat team this connection may reach; required, and may be listed more than once",
				Forms:       []string{"team/TEAM_ID"},
			}, {
				Name: "channel",
				Description: "one channel of a bound team, or one direct or group channel whose other " +
					"participants are members of them; optional, and may be listed more than once",
				Forms: []string{"channel/CHANNEL_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read teams, channels, messages, and users", Recommended: true,
			Description: "lists the bound teams and their channels, reads team details and members, reads channel details and the public " +
				"channels of a bound team, lists the direct and group channels, searches messages, files, and " +
				"channels, lists the members of a channel and the archived channels of a team, reads channel " +
				"messages, single messages, threads, followed threads, unread counts, reactions, pinned messages, " +
				"and attachments (metadata and download to a released local directory), reads, lists, and searches " +
				"the users of the bound teams and their presence, and lists the own sidebar categories; changes nothing",
			Tools: []string{teamsList.ID, teamsGet.ID, teamMembersList.ID, channelsList.ID, channelsGet.ID, channelsBrowse.ID,
				channelsMembersList.ID, directList.ID, messagesList.ID, messagesThread.ID, messagesGet.ID, messagesFiles.ID, filesInfo.ID,
				filesDownload.ID, reactionsList.ID, pinsList.ID, usersGet.ID, usersList.ID, usersSearch.ID,
				usersStatus.ID, messagesSearch.ID, filesSearch.ID, channelsSearch.ID, archivedChannelsList.ID,
				threadsList.ID, threadsGet.ID, channelsUnread.ID, categoriesList.ID},
		}, {
			ID: "messaging", Title: "Read, send, edit, and react",
			Description: "also opens a confirmed direct or group channel with members of the bound teams, sends " +
				"a confirmed message, or a confirmed reply to an existing thread, to a reachable channel, " +
				"uploads a confirmed file to such a channel, edits the text of a confirmed message, adds a " +
				"confirmed own reaction, pins or unpins a confirmed message, follows or unfollows a confirmed thread, " +
				"marks a confirmed channel, message, or thread as read or unread, sets the own presence and custom " +
				"status or clears the custom status, and creates or changes a confirmed own sidebar category or the " +
				"notifications of a channel; deleting a message, a category, or removing a reaction is never part " +
				"of a profile",
			Tools: []string{teamsList.ID, channelsList.ID, directList.ID, directOpen.ID, groupMessagesOpen.ID,
				messagesList.ID, messagesThread.ID, messagesGet.ID, messagesSend.ID, messagesUpdate.ID,
				messagesFiles.ID, filesInfo.ID, filesDownload.ID, filesUpload.ID, reactionsList.ID, reactionsAdd.ID,
				pinsList.ID, messagesPin.ID, messagesUnpin.ID, usersGet.ID, usersList.ID, usersSearch.ID,
				usersStatus.ID, statusSet.ID, customStatusSet.ID, customStatusClear.ID, messagesSearch.ID,
				filesSearch.ID, channelsSearch.ID, threadsList.ID, threadsGet.ID, threadsFollow.ID, threadsUnfollow.ID,
				channelsUnread.ID, channelsMarkRead.ID, messagesMarkUnread.ID, threadsMarkRead.ID,
				categoriesList.ID, categoriesCreate.ID, categoriesUpdate.ID, channelNotificationsUpdate.ID},
		}, {
			ID: "channel-admin", Title: "Manage channels",
			Description: "reads the bound teams, their channels with details, and the public channels, and also " +
				"creates a confirmed channel in a bound team (only without a channel allow-list), changes " +
				"the display name, purpose, header, or handle of a confirmed channel, and adds confirmed " +
				"members of the channel's team to a channel; archiving, restoring, visibility " +
				"changes, removing members, and changing member roles are never part of a profile",
			Tools: []string{teamsList.ID, channelsList.ID, channelsGet.ID, channelsBrowse.ID, channelsCreate.ID,
				channelsUpdate.ID, channelsMembersAdd.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: withGroup(teamsList), Handler: capability.Handler(invokeTeamsList)},
		capability.Operation{Descriptor: withGroup(teamsGet), Handler: capability.Handler(invokeTeamsGet)},
		capability.Operation{Descriptor: withGroup(teamMembersList), Handler: capability.Handler(invokeTeamMembersList)},
		capability.Operation{Descriptor: withGroup(teamMembersRoles), Handler: capability.Handler(invokeTeamMembersRoles)},
		capability.Operation{Descriptor: withGroup(channelsList), Handler: capability.Handler(invokeChannelsList)},
		capability.Operation{Descriptor: withGroup(channelsGet), Handler: capability.Handler(invokeChannelsGet)},
		capability.Operation{Descriptor: withGroup(channelsBrowse), Handler: capability.Handler(invokeChannelsBrowse)},
		capability.Operation{Descriptor: withGroup(channelsCreate), Handler: capability.Handler(invokeChannelsCreate)},
		capability.Operation{Descriptor: withGroup(channelsUpdate), Handler: capability.Handler(invokeChannelsUpdate)},
		capability.Operation{Descriptor: withGroup(archivedChannelsList), Handler: capability.Handler(invokeArchivedChannelsList)},
		capability.Operation{Descriptor: withGroup(channelsArchive), Handler: capability.Handler(invokeChannelsArchive)},
		capability.Operation{Descriptor: withGroup(channelsRestore), Handler: capability.Handler(invokeChannelsRestore)},
		capability.Operation{Descriptor: withGroup(channelsPrivacy), Handler: capability.Handler(invokeChannelsPrivacy)},
		capability.Operation{Descriptor: withGroup(channelsMembersList), Handler: capability.Handler(invokeChannelsMembersList)},
		capability.Operation{Descriptor: withGroup(channelsMembersAdd), Handler: capability.Handler(invokeChannelsMembersAdd)},
		capability.Operation{Descriptor: withGroup(channelsMembersRemove), Handler: capability.Handler(invokeChannelsMembersRemove)},
		capability.Operation{Descriptor: withGroup(channelsMembersRoles), Handler: capability.Handler(invokeChannelsMembersRoles)},
		capability.Operation{Descriptor: withGroup(categoriesList), Handler: capability.Handler(invokeCategoriesList)},
		capability.Operation{Descriptor: withGroup(categoriesCreate), Handler: capability.Handler(invokeCategoriesCreate)},
		capability.Operation{Descriptor: withGroup(categoriesUpdate), Handler: capability.Handler(invokeCategoriesUpdate)},
		capability.Operation{Descriptor: withGroup(categoriesDelete), Handler: capability.Handler(invokeCategoriesDelete)},
		capability.Operation{Descriptor: withGroup(channelNotificationsUpdate),
			Handler: capability.Handler(invokeChannelNotificationsUpdate)},
		capability.Operation{Descriptor: withGroup(messagesList), Handler: capability.Handler(invokeMessagesList)},
		capability.Operation{Descriptor: withGroup(messagesThread), Handler: capability.Handler(invokeMessagesThread)},
		capability.Operation{Descriptor: withGroup(messagesGet), Handler: capability.Handler(invokeMessagesGet)},
		capability.Operation{Descriptor: withGroup(messagesSend), Handler: capability.Handler(invokeMessagesSend)},
		capability.Operation{Descriptor: withGroup(messagesUpdate), Handler: capability.Handler(invokeMessagesUpdate)},
		capability.Operation{Descriptor: withGroup(messagesDelete), Handler: capability.Handler(invokeMessagesDelete)},
		capability.Operation{Descriptor: withGroup(messagesFiles), Handler: capability.Handler(invokeMessagesFiles)},
		capability.Operation{Descriptor: withGroup(filesInfo), Handler: capability.Handler(invokeFilesInfo)},
		capability.Operation{Descriptor: withGroup(filesDownload), Handler: capability.Handler(invokeFilesDownload)},
		capability.Operation{Descriptor: withGroup(filesUpload), Handler: capability.Handler(invokeFilesUpload)},
		capability.Operation{Descriptor: withGroup(reactionsList), Handler: capability.Handler(invokeReactionsList)},
		capability.Operation{Descriptor: withGroup(reactionsAdd), Handler: capability.Handler(invokeReactionsAdd)},
		capability.Operation{Descriptor: withGroup(reactionsRemove), Handler: capability.Handler(invokeReactionsRemove)},
		capability.Operation{Descriptor: withGroup(pinsList), Handler: capability.Handler(invokePinsList)},
		capability.Operation{Descriptor: withGroup(messagesPin), Handler: capability.Handler(invokePin)},
		capability.Operation{Descriptor: withGroup(messagesUnpin), Handler: capability.Handler(invokeUnpin)},
		capability.Operation{Descriptor: withGroup(usersGet), Handler: capability.Handler(invokeUsersGet)},
		capability.Operation{Descriptor: withGroup(usersList), Handler: capability.Handler(invokeUsersList)},
		capability.Operation{Descriptor: withGroup(usersSearch), Handler: capability.Handler(invokeUsersSearch)},
		capability.Operation{Descriptor: withGroup(usersStatus), Handler: capability.Handler(invokeUsersStatus)},
		capability.Operation{Descriptor: withGroup(statusSet), Handler: capability.Handler(invokeStatusSet)},
		capability.Operation{Descriptor: withGroup(customStatusSet), Handler: capability.Handler(invokeCustomStatusSet)},
		capability.Operation{Descriptor: withGroup(customStatusClear), Handler: capability.Handler(invokeCustomStatusClear)},
		capability.Operation{Descriptor: withGroup(profileUpdate), Handler: capability.Handler(invokeProfileUpdate)},
		capability.Operation{Descriptor: withGroup(directList), Handler: capability.Handler(invokeDirectList)},
		capability.Operation{Descriptor: withGroup(directOpen), Handler: capability.Handler(invokeDirectOpen)},
		capability.Operation{Descriptor: withGroup(groupMessagesOpen), Handler: capability.Handler(invokeGroupMessagesOpen)},
		capability.Operation{Descriptor: withGroup(messagesSearch), Handler: capability.Handler(invokeMessagesSearch)},
		capability.Operation{Descriptor: withGroup(filesSearch), Handler: capability.Handler(invokeFilesSearch)},
		capability.Operation{Descriptor: withGroup(channelsSearch), Handler: capability.Handler(invokeChannelsSearch)},
		capability.Operation{Descriptor: withGroup(threadsList), Handler: capability.Handler(invokeThreadsList)},
		capability.Operation{Descriptor: withGroup(threadsGet), Handler: capability.Handler(invokeThreadsGet)},
		capability.Operation{Descriptor: withGroup(threadsFollow), Handler: capability.Handler(invokeThreadsFollow)},
		capability.Operation{Descriptor: withGroup(threadsUnfollow), Handler: capability.Handler(invokeThreadsUnfollow)},
		capability.Operation{Descriptor: withGroup(channelsUnread), Handler: capability.Handler(invokeChannelsUnread)},
		capability.Operation{Descriptor: withGroup(channelsMarkRead), Handler: capability.Handler(invokeChannelsMarkRead)},
		capability.Operation{Descriptor: withGroup(messagesMarkUnread), Handler: capability.Handler(invokeMessagesMarkUnread)},
		capability.Operation{Descriptor: withGroup(threadsMarkRead), Handler: capability.Handler(invokeThreadsMarkRead)},
		capability.Operation{Descriptor: withGroup(incomingWebhooksList), Handler: capability.Handler(invokeIncomingWebhooksList)},
		capability.Operation{Descriptor: withGroup(incomingWebhooksGet), Handler: capability.Handler(invokeIncomingWebhooksGet)},
		capability.Operation{Descriptor: withGroup(incomingWebhooksUpdate), Handler: capability.Handler(invokeIncomingWebhooksUpdate)},
		capability.Operation{Descriptor: withGroup(incomingWebhooksDelete), Handler: capability.Handler(invokeIncomingWebhooksDelete)},
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
