// Package penpot implements controlled access to a Penpot instance, Penpot Cloud or self-hosted: its teams,
// projects, files, and pages (read), the creation, renaming, deletion, and moving of projects and files, and the
// comment threads and comments of its files (read and change),
// through the backend RPC interface
// (POST /api/rpc/command/<name>, help.penpot.app/technical-guide/integration, and the command sources of
// penpot 2.18.0, backend/src/app/rpc/commands, read 2026-09-30, not against a live instance). The RPC
// interface carries no stability promise; this provider is a beta.
//
// The access token is sent as "Authorization: Token ...". It has no scopes and acts for every team of its
// account; Qatlas adds its own boundary: the team targets of the connection (required) and an optional project
// allow-list. Fixed commands are called, each from a handler that names it: get-teams, get-projects,
// get-project-files, get-file-summary, get-page, the comment reads get-comment-threads and get-comments, and the
// comment changes create-, update-, and delete-comment-thread or -comment, the management changes
// create-, rename-, and delete-project, create-file, rename-file, and move-files, the snapshot read
// get-file-snapshots and changes create- and restore-file-snapshot, and delete-file, the read
// get-team-deleted-files and the changes restore-deleted-team-files and permanently-delete-team-files, the media
// changes upload-file-media-object and create-file-media-object-from-url, and the file transfers export-binfile
// (a read that writes a local file) and import-binfile, and the team webhooks get-webhooks and create-,
// update-, and delete-webhook, and the team administration get-team-members and the changes create-team,
// delete-team, update-team-member-role, delete-team-member, and create-team-invitations. A change is one request that is never
// repeated; a failure that leaves its result open says so. No agent argument chooses a command, a path, a method,
// or a body. Penpot documents no pagination for these commands; the answers are bounded on the client side.
//
// A team or project ID that is outside the targets is refused before the credential is resolved and before
// any request is sent, and the refusal never names it. A file is bound through its project: the project is
// first located in the projects of the bound teams and the file in that project's file list (both are
// requests made with the credential; a project or file that is not found is refused as outside the targets)
// before the summary and page are read.
//
// Every value of a response is provider data and untrusted: strings, lists, and the page's shapes are
// bounded, shapes are reduced to a few geometry fields (no fills, images, media references, or text
// content), a response is read up to a size limit, and provider texts never reach an error message.
package penpot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
const Provider = "penpot"

// roleAccessToken is the single secret role: a Penpot access token.
const roleAccessToken = "access-token"

const (
	cloudOrigin     = "https://design.penpot.app"
	maxResponseSize = 16 << 20
	defaultTimeout  = 30 * time.Second
	// transferTimeout replaces the 30 s of the client for the requests that carry or produce file content.
	transferTimeout = 30 * time.Minute
	// fetchTimeout is the time Penpot gets to fetch an image from a URL itself.
	fetchTimeout    = 2 * time.Minute
	maxStringLength = 256
	commandPrefix   = "/api/rpc/command/"
)

// The fixed RPC commands. Nothing else is ever sent.
const (
	cmdTeams       = "get-teams"
	cmdProjects    = "get-projects"
	cmdProjectFile = "get-project-files"
	cmdSummary     = "get-file-summary"
	cmdPage        = "get-page"

	cmdThreads       = "get-comment-threads"
	cmdComments      = "get-comments"
	cmdCreateThread  = "create-comment-thread"
	cmdCreateComment = "create-comment"
	cmdUpdateThread  = "update-comment-thread"
	cmdUpdateComment = "update-comment"
	cmdDeleteThread  = "delete-comment-thread"
	cmdDeleteComment = "delete-comment"

	cmdCreateProject = "create-project"
	cmdRenameProject = "rename-project"
	cmdDeleteProject = "delete-project"
	cmdCreateFile    = "create-file"
	cmdRenameFile    = "rename-file"
	cmdMoveFiles     = "move-files"

	cmdFileLibraries = "get-file-libraries"
	cmdSetShared     = "set-file-shared"
	cmdLinkLibrary   = "link-file-to-library"

	cmdSnapshots       = "get-file-snapshots"
	cmdDeletedFiles    = "get-team-deleted-files"
	cmdCreateSnapshot  = "create-file-snapshot"
	cmdRestoreSnapshot = "restore-file-snapshot"
	cmdDeleteFile      = "delete-file"
	cmdRestoreFiles    = "restore-deleted-team-files"
	cmdPurgeFiles      = "permanently-delete-team-files"

	cmdWebhooks      = "get-webhooks"
	cmdCreateWebhook = "create-webhook"
	cmdUpdateWebhook = "update-webhook"
	cmdDeleteWebhook = "delete-webhook"

	cmdTeamMembers      = "get-team-members"
	cmdCreateTeam       = "create-team"
	cmdDeleteTeam       = "delete-team"
	cmdSetMemberRole    = "update-team-member-role"
	cmdDeleteMember     = "delete-team-member"
	cmdCreateInvitation = "create-team-invitations"

	cmdUploadMedia  = "upload-file-media-object"
	cmdMediaFromURL = "create-file-media-object-from-url"
	cmdExportFile   = "export-binfile"
	cmdImportFile   = "import-binfile"
)

// changeUncertain is appended to a failure of a change whose request may have reached Penpot. Qatlas never
// repeats such a request by itself.
const changeUncertain = "; this change may have taken effect, read the current state in Penpot before repeating it"

var limiters = ratelimit.NewRegistry(0)

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// Client binds one access token to the origin and the team and project scope of its connection.
type Client struct {
	origin  string
	token   string
	scope   scope
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the access token of one selected connection and returns a client for its origin.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	const op = "open"
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAccessToken)
	if err != nil {
		return nil, err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Penpot access token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Token "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{origin: origin, token: value.Secret, scope: bound, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
}

// originOf accepts an https URL of a Penpot instance: Cloud or self-hosted, optionally below an installation
// path, never with user info, a query, or a fragment. The fixed RPC path is appended to it.
func originOf(raw string) (string, error) {
	const reason = "a Penpot service needs a usable https URL, without user, query, or fragment"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New(reason)
	}
	return "https://" + parsed.Host + strings.TrimSuffix(parsed.EscapedPath(), "/"), nil
}

// isCommand keeps the client from sending anything but one of the fixed commands; isChange tells the
// commands that change data from the ones that only read.
func isCommand(name string) bool {
	switch name {
	case cmdTeams, cmdProjects, cmdProjectFile, cmdSummary, cmdPage, cmdThreads, cmdComments, cmdFileLibraries,
		cmdSnapshots, cmdDeletedFiles, cmdExportFile, cmdWebhooks, cmdTeamMembers:
		return true
	}
	return isChange(name)
}

func isChange(name string) bool {
	switch name {
	case cmdCreateThread, cmdCreateComment, cmdUpdateThread, cmdUpdateComment, cmdDeleteThread, cmdDeleteComment,
		cmdCreateProject, cmdRenameProject, cmdDeleteProject, cmdCreateFile, cmdRenameFile, cmdMoveFiles,
		cmdSetShared, cmdLinkLibrary, cmdCreateSnapshot, cmdRestoreSnapshot, cmdDeleteFile, cmdRestoreFiles, cmdPurgeFiles,
		cmdUploadMedia, cmdMediaFromURL, cmdImportFile, cmdCreateWebhook, cmdUpdateWebhook, cmdDeleteWebhook,
		cmdCreateTeam, cmdDeleteTeam, cmdSetMemberRole, cmdDeleteMember, cmdCreateInvitation:
		return true
	}
	return false
}

// do sends one bounded read command as a POST with a JSON body and decodes the JSON answer into out. The
// body holds only validated values under fixed keys. A read is never retried.
func (c *Client) do(ctx context.Context, op, command string, params map[string]any, out any) error {
	if isChange(command) {
		return providerError(op, "the command is not offered")
	}
	data, err := c.exchange(ctx, op, command, params, false)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, "Penpot returned an invalid response")
	}
	return nil
}

// change sends one change command exactly once and returns the bounded answer, which may be empty. A failure
// that leaves the result open carries the uncertain hint.
func (c *Client) change(ctx context.Context, op, command string, params map[string]any) ([]byte, error) {
	if !isChange(command) {
		return nil, providerError(op, "the command is not offered")
	}
	return c.exchange(ctx, op, command, params, true)
}

func (c *Client) exchange(ctx context.Context, op, command string, params map[string]any, change bool) ([]byte, error) {
	body, err := json.Marshal(params)
	if err != nil || params == nil {
		body = []byte("{}")
	}
	return c.send(ctx, op, command, bytes.NewReader(body), int64(len(body)), "application/json", c.http, change)
}

// withTimeout is the client of a request that needs more time than the default; it shares the transport and
// the refusal to follow redirects.
func (c *Client) withTimeout(limit time.Duration) *http.Client {
	copied := *c.http
	copied.Timeout = limit
	return &copied
}

// send is the one place that sends a command: one POST with the given body, never repeated.
func (c *Client) send(ctx context.Context, op, command string, body io.Reader, length int64, contentType string,
	httpClient *http.Client, change bool) ([]byte, error) {
	if !isCommand(command) {
		return nil, providerError(op, "the command is not offered")
	}
	hint := ""
	if change {
		hint = changeUncertain
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Penpot", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+commandPrefix+command, body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = length
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := httpClient.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Penpot", err)
		if change && failure.MayHaveArrived() {
			failure.Message += hint
		}
		return nil, failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response)
		if change && response.StatusCode >= 500 {
			failure.Message += hint
		}
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return nil, invalidResponse(op, "the Penpot response could not be read within the size limit"+hint)
	}
	return data, nil
}

// statusError maps an HTTP status to a stable class; the provider body is never read into the message.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSize))
	status := response.StatusCode
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Penpot rejected the access token; check that it is valid and that the access-tokens flag is enabled on the instance"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "Penpot refused this access token; check that the access-tokens flag is enabled on the instance and " +
				"that the account may read this resource"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Penpot does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(provider.RetryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Penpot rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Penpot is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Penpot did not answer in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Penpot answered with a redirect, which Qatlas does not follow for this request"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: "Penpot rejected the operation (HTTP " + strconv.Itoa(status) + ")"}
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

func invalidRequest(message string) error { return &provider.InvalidRequestError{Message: message} }

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

// obj is one provider object with its keys normalized (lower case, without '-' and '_'), so that a key is
// read the same whether Penpot writes it in camel case or kebab case.
type obj map[string]json.RawMessage

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "-", "")
	return strings.ReplaceAll(key, "_", "")
}

func asObj(raw json.RawMessage) (obj, bool) {
	var items map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, false
	}
	out := make(obj, len(items))
	for key, value := range items {
		out[normalizeKey(key)] = value
	}
	return out, true
}

// objects decodes a JSON array of objects; an element that is not an object is skipped.
func objects(raw json.RawMessage) ([]obj, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false
	}
	out := make([]obj, 0, len(items))
	for _, item := range items {
		if entry, ok := asObj(item); ok {
			out = append(out, entry)
		}
	}
	return out, true
}

// str reads a string value, bounded; number and boolean values read as empty.
func (o obj) str(key string) string {
	var value string
	if err := json.Unmarshal(o[key], &value); err != nil {
		return ""
	}
	return bounded(value)
}

// id reads a value that must be a UUID, normalized to lower case; anything else reads as empty.
func (o obj) id(key string) string {
	var value string
	if err := json.Unmarshal(o[key], &value); err != nil {
		return ""
	}
	id, ok := parseUUID(value)
	if !ok {
		return ""
	}
	return id
}

func (o obj) integer(key string) int64 {
	var value int64
	if err := json.Unmarshal(o[key], &value); err != nil {
		return 0
	}
	return value
}

func (o obj) number(key string) float64 {
	var value float64
	if err := json.Unmarshal(o[key], &value); err != nil {
		return 0
	}
	return value
}

func (o obj) boolean(key string) bool {
	var value bool
	if err := json.Unmarshal(o[key], &value); err != nil {
		return false
	}
	return value
}

// TestConnection performs the smallest safe authenticated read: the token's team list.
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
	var teams json.RawMessage
	if err := client.do(ctx, "test connection", cmdTeams, nil, &teams); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds the provider metadata, its connection test, and its operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Penpot", DefaultBaseURL: cloudOrigin,
		Description:        "Open-source design platform, teams, projects, and files through an access token (beta)",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleAccessToken,
			Description: "Penpot access token, created under the profile's access tokens; the instance needs the " +
				"access-tokens flag enabled. The token has no scopes and acts for every team of the account; it is " +
				"sent as 'Authorization: Token ...'",
		}},
		Target: config.TargetMetadata{
			Label:    "teams and projects",
			Required: true,
			Multiple: true,
			Description: "one or more team/TEAM_ID targets, and optionally a project/PROJECT_ID allow-list of " +
				"projects of those teams; without a project target every project of the bound teams is reachable",
			Kinds: []config.TargetKind{{
				Name:        "team",
				Description: "a team whose projects and files this connection may read; required, repeatable",
				Forms:       []string{"team/TEAM_ID"},
			}, {
				Name:        "project",
				Description: "a project of a bound team that narrows the connection; optional, repeatable",
				Forms:       []string{"project/PROJECT_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read teams, projects, and files", Recommended: true,
			Description: "lists teams, projects, and files and reads a file's summary and page; changes nothing",
			Tools:       []string{teamsList.ID, projectsList.ID, filesList.ID, filesGet.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: teamsList, Handler: capability.Handler(invokeTeamsList)},
		capability.Operation{Descriptor: projectsList, Handler: capability.Handler(invokeProjectsList)},
		capability.Operation{Descriptor: filesList, Handler: capability.Handler(invokeFilesList)},
		capability.Operation{Descriptor: filesGet, Handler: capability.Handler(invokeFilesGet)},
		capability.Operation{Descriptor: commentsThreads, Handler: capability.Handler(invokeCommentsThreads)},
		capability.Operation{Descriptor: commentsList, Handler: capability.Handler(invokeCommentsList)},
		capability.Operation{Descriptor: commentsCreate, Handler: capability.Handler(invokeCommentsCreate)},
		capability.Operation{Descriptor: commentsUpdate, Handler: capability.Handler(invokeCommentsUpdate)},
		capability.Operation{Descriptor: commentsDelete, Handler: capability.Handler(invokeCommentsDelete)},
		capability.Operation{Descriptor: projectsCreate, Handler: capability.Handler(invokeProjectsCreate)},
		capability.Operation{Descriptor: projectsRename, Handler: capability.Handler(invokeProjectsRename)},
		capability.Operation{Descriptor: projectsDelete, Handler: capability.Handler(invokeProjectsDelete)},
		capability.Operation{Descriptor: filesCreate, Handler: capability.Handler(invokeFilesCreate)},
		capability.Operation{Descriptor: filesRename, Handler: capability.Handler(invokeFilesRename)},
		capability.Operation{Descriptor: filesMove, Handler: capability.Handler(invokeFilesMove)},
		capability.Operation{Descriptor: librariesList, Handler: capability.Handler(invokeLibrariesList)},
		capability.Operation{Descriptor: librariesShare, Handler: capability.Handler(invokeLibrariesShare)},
		capability.Operation{Descriptor: librariesLink, Handler: capability.Handler(invokeLibrariesLink)},
		capability.Operation{Descriptor: snapshotsCreate, Handler: capability.Handler(invokeSnapshotsCreate)},
		capability.Operation{Descriptor: snapshotsRestore, Handler: capability.Handler(invokeSnapshotsRestore)},
		capability.Operation{Descriptor: filesDelete, Handler: capability.Handler(invokeFilesDelete)},
		capability.Operation{Descriptor: filesRestore, Handler: capability.Handler(invokeFilesRestore)},
		capability.Operation{Descriptor: filesPurge, Handler: capability.Handler(invokeFilesPurge)},
		capability.Operation{Descriptor: mediaUpload, Handler: capability.Handler(invokeMediaUpload)},
		capability.Operation{Descriptor: mediaFromURL, Handler: capability.Handler(invokeMediaFromURL)},
		capability.Operation{Descriptor: filesExport, Handler: capability.Handler(invokeFilesExport)},
		capability.Operation{Descriptor: filesImport, Handler: capability.Handler(invokeFilesImport)},
		capability.Operation{Descriptor: webhooksList, Handler: capability.Handler(invokeWebhooksList)},
		capability.Operation{Descriptor: webhooksCreate, Handler: capability.Handler(invokeWebhooksCreate)},
		capability.Operation{Descriptor: webhooksUpdate, Handler: capability.Handler(invokeWebhooksUpdate)},
		capability.Operation{Descriptor: webhooksDelete, Handler: capability.Handler(invokeWebhooksDelete)},
		capability.Operation{Descriptor: membersList, Handler: capability.Handler(invokeMembersList)},
		capability.Operation{Descriptor: membersSetRole, Handler: capability.Handler(invokeMembersSetRole)},
		capability.Operation{Descriptor: membersRemove, Handler: capability.Handler(invokeMembersRemove)},
		capability.Operation{Descriptor: invitationsCreate, Handler: capability.Handler(invokeInvitationsCreate)},
		capability.Operation{Descriptor: teamsCreate, Handler: capability.Handler(invokeTeamsCreate)},
		capability.Operation{Descriptor: teamsDelete, Handler: capability.Handler(invokeTeamsDelete)},
	)
}
