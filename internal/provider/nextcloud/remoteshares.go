package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Federated shares the identity accepted from another server are read and removed here. They are never
// created or accepted, and pending invitations are not touched. The tools reach the account of the identity,
// not a folder, so every one needs an account target.

const (
	maxRemoteHost = 255

	uncertainRemoteShareRemove = "; the remote share may have been removed, check shares.remote-shares.list or " +
		"shares.remote-shares.get before repeating"

	messageFederationOff       = "federated sharing is not available on this Nextcloud instance"
	messageRemoteShareNotFound = "this Nextcloud identity holds no such remote share"
	messageRemoteShareRefused  = "Nextcloud could not remove this remote share"
)

var remoteShareErrors = map[int]string{
	http.StatusForbidden: messageRemoteShareRefused, http.StatusNotFound: messageRemoteShareNotFound,
}

// RemoteShare is the stable view of one accepted federated share. It carries the other server only as a
// host and has no token, no password, and no remote URL.
type RemoteShare struct {
	ID         string `json:"id"`
	Owner      string `json:"owner,omitempty"`
	Host       string `json:"host,omitempty"`
	Name       string `json:"name,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// RemoteSharesResult is the list of the accepted federated shares.
type RemoteSharesResult struct {
	Shares    []RemoteShare `json:"shares"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated,omitempty"`
}

// rawRemoteShare lists only the members this adapter reads. The share token, the refresh token, and the
// password are not members, so they cannot reach a result.
type rawRemoteShare struct {
	ID         flexString `json:"id"`
	Remote     string     `json:"remote"`
	Name       string     `json:"name"`
	Owner      string     `json:"owner"`
	Mountpoint string     `json:"mountpoint"`
}

// remoteHost reduces the remote address to its host: userinfo, path, query, and fragment are dropped, and
// an address without a usable host yields nothing.
func remoteHost(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		remote = "https://" + remote
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	host := parsed.Host
	if host == "" || len(host) > maxRemoteHost || !utf8.ValidString(host) || hasControl(host) ||
		strings.ContainsAny(host, "@/\\ ") {
		return ""
	}
	return host
}

// mountpointOf reports the mount point relative to the Files root of the identity; an unsafe one is left out.
func mountpointOf(raw string) string {
	relative := strings.TrimPrefix(raw, "/")
	if segments, err := splitRelative(relative); err != nil || len(segments) == 0 {
		return ""
	}
	return relative
}

// remoteShareHidden reports whether the mount point lies on or below an excluded folder, or above one. A
// mount point that cannot be placed counts as hidden once a folder is excluded. Removing is the revocation
// and stays possible for a mount above an exclusion, so a caller that removes passes revoke.
func (c *Client) remoteShareHidden(raw rawRemoteShare, revoke bool) bool {
	if !c.scope.folderExcludesAny() {
		return false
	}
	segments, err := splitRelative(strings.Trim(raw.Mountpoint, "/"))
	if err != nil || len(segments) == 0 || c.scope.folderExcluded(segments) {
		return true
	}
	return !revoke && c.scope.folderHoldsExcluded(segments)
}

func normaliseRemote(raw rawRemoteShare) (RemoteShare, bool) {
	id := string(raw.ID)
	if checkShareIDFor("remote share", id) != nil {
		return RemoteShare{}, false
	}
	share := RemoteShare{ID: id, Host: remoteHost(raw.Remote), Mountpoint: mountpointOf(raw.Mountpoint)}
	var ownerCut, nameCut bool
	share.Owner, ownerCut = cut(raw.Owner, maxValueLength)
	share.Name, nameCut = cut(strings.TrimPrefix(raw.Name, "/"), maxValueLength)
	share.Truncated = ownerCut || nameCut
	if len(raw.Mountpoint) > maxValueLength {
		share.Truncated = true
	}
	return share, true
}

func remoteShareEndpoint(suffix ...string) ocsRequest {
	return ocsRequest{app: ocsSharing, suffix: append([]string{"remote_shares"}, suffix...)}
}

// ListRemoteShares reads the accepted federated shares of the identity. A 404 means the route is missing,
// which is how an instance without federation answers.
func (c *Client) ListRemoteShares(ctx context.Context) (*RemoteSharesResult, error) {
	const op = "list remote shares"
	data, err := c.ocsGet(ctx, op, remoteShareEndpoint())
	if err != nil {
		if isNotFound(err) {
			return nil, providerErrorFrom(op, messageFederationOff, err)
		}
		return nil, err
	}
	var items []rawRemoteShare
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud remote share list could not be read")
	}
	result := &RemoteSharesResult{Shares: []RemoteShare{}}
	for _, item := range items {
		share, ok := normaliseRemote(item)
		if !ok || c.remoteShareHidden(item, false) {
			continue
		}
		if len(result.Shares) == maxShareEntries {
			result.Truncated = true
			break
		}
		result.Shares = append(result.Shares, share)
	}
	result.Count = len(result.Shares)
	return result, nil
}

func remoteShareNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageRemoteShareNotFound}
}

// GetRemoteShare reads one accepted federated share. A 404 is reported as not found without saying whether
// the share or federation is missing.
func (c *Client) GetRemoteShare(ctx context.Context, op, id string) (*RemoteShare, error) {
	return c.readRemoteShare(ctx, op, id, false)
}

func (c *Client) readRemoteShare(ctx context.Context, op, id string, revoke bool) (*RemoteShare, error) {
	data, err := c.ocsGet(ctx, op, remoteShareEndpoint(id))
	if err != nil {
		if isNotFound(err) {
			return nil, withOriginalValue(remoteShareNotFound(op), refusedOriginal(err))
		}
		return nil, err
	}
	var single rawRemoteShare
	if err := json.Unmarshal(data, &single); err != nil {
		var list []rawRemoteShare
		if err := json.Unmarshal(data, &list); err != nil || len(list) != 1 {
			return nil, invalidResponse(op, "the Nextcloud remote share could not be read")
		}
		single = list[0]
	}
	share, ok := normaliseRemote(single)
	if !ok || share.ID != id {
		return nil, remoteShareNotFound(op)
	}
	if c.remoteShareHidden(single, revoke) {
		return nil, withOriginalValue(remoteShareNotFound(op), &provider.Original{Status: http.StatusNotFound})
	}
	return &share, nil
}

// RemoveRemoteShare reads the share once and sends exactly one DELETE; the outcome of a request that was
// sent and not clearly refused is open and is never retried.
func (c *Client) RemoveRemoteShare(ctx context.Context, id string) (any, error) {
	const op = "remove remote share"
	current, err := c.readRemoteShare(ctx, op, id, true)
	if err != nil {
		return nil, err
	}
	if _, err := c.ocsSend(ctx, op, http.MethodDelete, remoteShareEndpoint(id), nil, uncertainRemoteShareRemove); err != nil {
		return nil, tagAdminError(op, err, remoteShareErrors)
	}
	return map[string]any{"removed": true, "id": current.ID, "host": current.Host, "name": current.Name}, nil
}

const (
	remoteShareSchema = `{"type":"object","properties":{"id":{"type":"string"},"owner":{"type":"string"},` +
		`"host":{"type":"string"},"name":{"type":"string"},"mountpoint":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`
	remoteShareTags = "federated"
)

var remoteShareFields = []capability.Field{
	{Name: "id", Description: "Remote share ID"},
	{Name: "owner", Description: "Owner on the other server, untrusted data"},
	{Name: "host", Description: "Host of the other server only; no user, path, or token"},
	{Name: "name", Description: "Name of the share, untrusted data"},
	{Name: "mountpoint", Description: "Mount point relative to the Files root folder of the identity"},
	{Name: "truncated", Description: "True when a text of this entry was cut"},
}

var remoteShareIDArgument = capability.Argument{Name: "share_id", Required: true,
	Description: "Numeric remote share ID as reported by shares.remote-shares.list"}

var remoteSharesList = capability.Descriptor{
	ID: Provider + ".shares.remote-shares.list", Version: 1, Title: "List accepted Nextcloud federated shares",
	Description: "List the federated shares the identity accepted from other servers; the other server is " +
		"reported as a host only, and no token, password, or remote URL is reported. Shares mounted in or above an " +
		"excluded folder are left out. Needs an account target",
	Tags:     []string{"nextcloud", "shares", "sharing", "remoteshares", remoteShareTags, "list", "ocs"},
	Provider: Provider, Group: groupShares, Risk: sharesRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"shares":{"type":"array","items":` +
		remoteShareSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["shares","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "shares", Description: "Accepted federated shares, untrusted data"},
		{Name: "count", Description: "Number of reported shares"},
		{Name: "truncated", Description: "True when more than 500 shares existed and the list was cut"},
	},
	Examples: []capability.Example{{Description: "List the accepted federated shares", Arguments: json.RawMessage(`{}`)}},
}

var remoteSharesGet = capability.Descriptor{
	ID: Provider + ".shares.remote-shares.get", Version: 1, Title: "Get an accepted Nextcloud federated share",
	Description: "Read one accepted federated share of the identity by its ID; the other server is reported as " +
		"a host only. A share mounted in or above an excluded folder is reported as missing. Needs an account target",
	Tags:     []string{"nextcloud", "shares", "sharing", "remoteshares", remoteShareTags, "get", "ocs"},
	Provider: Provider, Group: groupShares, Risk: sharesRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema + `},` +
		`"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(remoteShareSchema),
	Arguments:    []capability.Argument{remoteShareIDArgument},
	Fields:       remoteShareFields,
	Examples: []capability.Example{{Description: "Read one share a listing reported",
		Arguments: json.RawMessage(`{"share_id":"5"}`)}},
}

var remoteSharesRemove = capability.Descriptor{
	ID: Provider + ".shares.remote-shares.remove", Version: 1, Title: "Remove an accepted Nextcloud federated share",
	Description: "Remove exactly one accepted federated share from the identity, which unmounts it; the share " +
		"on the other server stays. One read-only pre-check precedes exactly one request; a share mounted in an " +
		"excluded folder is reported as missing. Needs an account target",
	Tags:     []string{"nextcloud", "shares", "sharing", "remoteshares", remoteShareTags, "remove", "delete", "ocs"},
	Provider: Provider, Group: groupShares,
	Risk: shareManageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema +
		`},"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"},"id":{"type":"string"},` +
		`"host":{"type":"string"},"name":{"type":"string"}},"required":["removed","id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{remoteShareIDArgument},
	Fields: []capability.Field{{Name: "removed", Description: "True when Nextcloud removed the share"},
		{Name: "id", Description: "ID of the removed share"}, remoteShareFields[2], remoteShareFields[3]},
	Examples: []capability.Example{{Description: "Use a share_id from shares.remote-shares.list",
		Arguments: json.RawMessage(`{"share_id":"5"}`)}},
	RequiresToolAllowList: true,
}

func invokeRemoteSharesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := decodeNoArguments("list remote shares", raw); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListRemoteShares(ctx)
}

func decodeNoArguments(op string, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var input struct{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return providerError(op, "the validated arguments could not be read")
	}
	return nil
}

func readRemoteShareID(op string, raw json.RawMessage) (string, error) {
	var input struct {
		ShareID string `json:"share_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", providerError(op, "the validated arguments could not be read")
	}
	if err := checkShareIDFor(op, input.ShareID); err != nil {
		return "", err
	}
	return input.ShareID, nil
}

func invokeRemoteSharesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get remote share"
	id, err := readRemoteShareID(op, raw)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GetRemoteShare(ctx, op, id)
}

func invokeRemoteSharesRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := readRemoteShareID("remove remote share", raw)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.RemoveRemoteShare(ctx, id)
}
