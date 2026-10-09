package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Managing shares goes through the same OCS endpoint as reading them: a POST creates a share, a PUT changes
// one, and a DELETE revokes one. Only shares with a user or a group of the instance are created or changed;
// link, e-mail, federated, team, and Talk shares are never created or changed here, but any share the
// identity made below the root may be revoked.

const (
	maxShareWith = 255
	maxShareNote = 500

	shareTypeUser  = "user"
	shareTypeGroup = "group"

	// The numeric share types the OCS API takes for the two recipient kinds.
	ocsShareUser  = "0"
	ocsShareGroup = "1"

	uncertainShareCreate = "; the share may have been created, check shares.list before repeating"
	uncertainShareUpdate = "; the share may have been changed, check shares.get before repeating"
	uncertainShareDelete = "; the share may have been revoked, check shares.get before repeating"

	messageShareRefused = "Nextcloud refused this share; an instance policy (for example a required or too late " +
		"expiry date) or the arguments do not allow it"
	messageShareDenied = "the instance policy or the rights of this Nextcloud identity do not allow this share " +
		"(for example sharing is off or the item may not be shared with this recipient)"
	messageShareTargetMissing = "Nextcloud found no such item or recipient for this share"
	messageShareNotOwn        = "only a share this identity made can be changed or revoked"
	messageShareType          = "only a share with a user or a group can be changed"
)

var shareManageErrors = map[int]string{
	http.StatusBadRequest: messageShareRefused, http.StatusForbidden: messageShareDenied,
	http.StatusNotFound: messageShareTargetMissing,
}

const (
	shareWithSchema = `{"type":"string","minLength":1,"maxLength":255}`
	expiresSchema   = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$","x-form":"a date as YYYY-MM-DD"}`
	noteSchema      = `{"type":"string","maxLength":500}`
	rightFlags      = `"update":{"type":"boolean"},"create":{"type":"boolean"},"delete":{"type":"boolean"},` +
		`"share":{"type":"boolean"}`
)

func shareManageRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: sharesSensitivity}
}

var (
	rightsArguments = []capability.Argument{
		{Name: "update", Description: "Allow the recipient to change the content; false when omitted on create"},
		{Name: "create", Description: "Allow the recipient to create items in a folder; false when omitted on create"},
		{Name: "delete", Description: "Allow the recipient to delete items; false when omitted on create"},
		{Name: "share", Description: "Allow the recipient to share the item further; false when omitted on create"},
	}
	expiresArgument = capability.Argument{Name: "expires_at",
		Description: "Last day of the share as YYYY-MM-DD; the instance may require or limit it"}
	noteArgument    = capability.Argument{Name: "note", Description: "Note to the recipient, at most 500 characters"}
	shareIDArgument = capability.Argument{Name: "share_id", Required: true,
		Description: "Numeric share ID as reported by shares.list"}
)

var shareManageFields = []capability.Field{{Name: "share", Description: "The share as Nextcloud reports it after the change"}}

var sharesCreate = capability.Descriptor{
	ID: Provider + ".shares.create", Version: 1, Title: "Share a Nextcloud item with a user or group",
	Description: "Share exactly one file or folder below the fixed root folder with one existing user or group of the " +
		"Nextcloud instance; the recipient always may read, the other rights default to false. Links, e-mail, " +
		"federated, team, and Talk shares cannot be created, and no password is set",
	Tags: []string{"nextcloud", "shares", "sharing", "create", "ocs"}, Provider: Provider, Group: groupShares,
	Risk: shareManageRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string","enum":["user","group"]},` +
		`"path":` + pathSchema + `,"share_with":` + shareWithSchema + `,` + rightFlags +
		`,"expires_at":` + expiresSchema + `,"note":` + noteSchema +
		`},"required":["type","path","share_with"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},"share":` + shareSchema +
		`},"required":["created","share"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "type", Required: true, Description: "user or group"},
		{Name: "path", Required: true, Description: "File or folder relative to the fixed root folder of this connection"},
		{Name: "share_with", Required: true, Description: "User ID or group ID as reported by sharees.search"},
	}, append(rightsArguments, expiresArgument, noteArgument)...),
	Fields: append([]capability.Field{{Name: "created", Description: "True when Nextcloud created the share"}},
		shareManageFields...),
	Examples: []capability.Example{{Description: "Share a report read-only with a colleague",
		Arguments: json.RawMessage(`{"type":"user","path":"2026/q1.pdf","share_with":"alice"}`)}},
	RequiresToolAllowList: true,
}

var sharesUpdate = capability.Descriptor{
	ID: Provider + ".shares.update", Version: 1, Title: "Change a Nextcloud share",
	Description: "Change the rights, expiry, or note of exactly one share the identity made with a user or a group, " +
		"below the fixed root folder, at least one field; rights not given keep their value. One read-only " +
		"pre-check precedes exactly one change request",
	Tags: []string{"nextcloud", "shares", "sharing", "update", "ocs"}, Provider: Provider, Group: groupShares,
	Risk: shareManageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema + `,` + rightFlags +
		`,"expires_at":` + expiresSchema + `,"note":` + noteSchema + `},"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"share":` + shareSchema +
		`},"required":["updated","share"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{shareIDArgument}, append(rightsArguments, expiresArgument,
		capability.Argument{Name: "note", Description: "Note to the recipient, at most 500 characters; empty removes it"})...),
	Fields: append([]capability.Field{{Name: "updated", Description: "True when Nextcloud applied the change"}},
		shareManageFields...),
	Examples:              []capability.Example{{Description: "Let the recipient edit", Arguments: json.RawMessage(`{"share_id":"42","update":true}`)}},
	RequiresToolAllowList: true,
}

var sharesDelete = capability.Descriptor{
	ID: Provider + ".shares.delete", Version: 1, Title: "Revoke a Nextcloud share",
	Description: "Revoke exactly one share the identity made below the fixed root folder, of any type including link, " +
		"e-mail, federated, team, and Talk shares; the recipient loses access. One read-only pre-check precedes " +
		"exactly one request",
	Tags: []string{"nextcloud", "shares", "sharing", "delete", "revoke", "ocs"}, Provider: Provider, Group: groupShares,
	Risk: shareManageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema +
		`},"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"share_id":{"type":"string"},` +
		`"type":{"type":"string"},"path":{"type":"string"}},"required":["deleted","share_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{shareIDArgument},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Nextcloud revoked the share"},
		{Name: "share_id", Description: "ID of the revoked share"}, {Name: "type", Description: "Type name of the revoked share"},
		{Name: "path", Description: "Path of the shared item relative to the root"}},
	Examples:              []capability.Example{{Description: "Use a share_id from shares.list", Arguments: json.RawMessage(`{"share_id":"42"}`)}},
	RequiresToolAllowList: true,
}

type shareManageArguments struct {
	Type      string  `json:"type"`
	Path      string  `json:"path"`
	ShareWith string  `json:"share_with"`
	ShareID   string  `json:"share_id"`
	Update    *bool   `json:"update"`
	Create    *bool   `json:"create"`
	Delete    *bool   `json:"delete"`
	Share     *bool   `json:"share"`
	ExpiresAt *string `json:"expires_at"`
	Note      *string `json:"note"`
}

func (a shareManageArguments) hasRights() bool {
	return a.Update != nil || a.Create != nil || a.Delete != nil || a.Share != nil
}

// readShareManageArguments decodes strictly and validates every value locally, before any credential
// access or request. Refusals name no path.
func readShareManageArguments(op string, raw json.RawMessage, mode string) (shareManageArguments, []string, error) {
	var input shareManageArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	var rel []string
	switch mode {
	case modeCreate:
		if input.ShareID != "" {
			return input, nil, providerError(op, "a new share has no share_id")
		}
		if input.Type != shareTypeUser && input.Type != shareTypeGroup {
			return input, nil, providerError(op, "only shares with a user or a group can be created")
		}
		segments, err := splitRelative(input.Path)
		if err != nil || len(segments) == 0 {
			return input, nil, providerError(op, "the path must name a file or folder below the root folder of this connection")
		}
		rel = segments
		if input.ShareWith == "" || len(input.ShareWith) > maxShareWith || !utf8.ValidString(input.ShareWith) ||
			hasControl(input.ShareWith) {
			return input, nil, providerError(op, "share_with must be a user or group ID of 1 to 255 characters")
		}
	default:
		if err := checkShareIDFor(op, input.ShareID); err != nil {
			return input, nil, err
		}
		if input.Type != "" || input.Path != "" || input.ShareWith != "" {
			return input, nil, providerError(op, "the type, path, and recipient of a share cannot be given here")
		}
	}
	if mode == modeDelete {
		if input.hasRights() || input.ExpiresAt != nil || input.Note != nil {
			return input, nil, providerError(op, "only a share_id is accepted")
		}
		return input, nil, nil
	}
	if input.ExpiresAt != nil {
		if _, err := time.Parse("2006-01-02", *input.ExpiresAt); err != nil {
			return input, nil, providerError(op, "expires_at must be a date as YYYY-MM-DD")
		}
	}
	if input.Note != nil {
		if utf8.RuneCountInString(*input.Note) > maxShareNote || !utf8.ValidString(*input.Note) ||
			hasControl(strings.ReplaceAll(*input.Note, "\n", "")) {
			return input, nil, providerError(op, "the note must be at most 500 characters without control characters")
		}
	}
	if mode == modeUpdate && !input.hasRights() && input.ExpiresAt == nil && input.Note == nil {
		return input, nil, providerError(op, "at least one field to change is required")
	}
	return input, rel, nil
}

func checkShareIDFor(op, id string) error {
	if id == "" || len(id) > maxShareIDLength || !digitsOnly(id) {
		return providerError(op, "a share ID is a number")
	}
	return nil
}

// permissionMask builds the OCS bitmask; reading is always granted.
func permissionMask(rights Rights) int {
	mask := permRead
	for _, flag := range []struct {
		set bool
		bit int
	}{{rights.Update, permUpdate}, {rights.Create, permCreate}, {rights.Delete, permDelete}, {rights.Share, permShare}} {
		if flag.set {
			mask |= flag.bit
		}
	}
	return mask
}

// withGiven overlays the rights that were given onto a base.
func (a shareManageArguments) withGiven(base Rights) Rights {
	for _, change := range []struct {
		value *bool
		field *bool
	}{{a.Update, &base.Update}, {a.Create, &base.Create}, {a.Delete, &base.Delete}, {a.Share, &base.Share}} {
		if change.value != nil {
			*change.field = *change.value
		}
	}
	return base
}

func shareManageOpen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return Open(ctx, resolved, secrets, red)
}

func invokeSharesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create share"
	input, rel, err := readShareManageArguments(op, raw, modeCreate)
	if err != nil {
		return nil, err
	}
	client, err := shareManageOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.createShare(ctx, op, input, rel)
}

func invokeSharesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update share"
	input, _, err := readShareManageArguments(op, raw, modeUpdate)
	if err != nil {
		return nil, err
	}
	client, err := shareManageOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.updateShare(ctx, op, input)
}

func invokeSharesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "revoke share"
	input, _, err := readShareManageArguments(op, raw, modeDelete)
	if err != nil {
		return nil, err
	}
	client, err := shareManageOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.deleteShare(ctx, op, input)
}

var shareEndpoint = ocsRequest{app: ocsSharing, suffix: []string{"shares"}}

func shareEndpointFor(id string) ocsRequest {
	return ocsRequest{app: ocsSharing, suffix: []string{"shares", id}}
}

// sharedAnswer reads the share a successful create or change answers with. The change did happen, so an
// answer that cannot be read leaves the outcome open.
func (c *Client) sharedAnswer(op string, data json.RawMessage, hint string) (Share, error) {
	var single rawShare
	if err := json.Unmarshal(data, &single); err != nil {
		var list []rawShare
		if err := json.Unmarshal(data, &list); err != nil || len(list) != 1 {
			return Share{}, withUncertainty(invalidResponse(op, "the Nextcloud share could not be read"), hint)
		}
		single = list[0]
	}
	share, ok := c.normalise(single)
	if !ok {
		return Share{}, withUncertainty(invalidResponse(op, "the Nextcloud share could not be read"), hint)
	}
	return share, nil
}

// createShare sends exactly one POST.
func (c *Client) createShare(ctx context.Context, op string, input shareManageArguments, rel []string) (any, error) {
	form := url.Values{"path": {c.absolute(rel)}, "shareWith": {input.ShareWith}}
	if input.Type == shareTypeGroup {
		form.Set("shareType", ocsShareGroup)
	} else {
		form.Set("shareType", ocsShareUser)
	}
	form.Set("permissions", strconv.Itoa(permissionMask(input.withGiven(Rights{}))))
	if input.ExpiresAt != nil {
		form.Set("expireDate", *input.ExpiresAt)
	}
	if input.Note != nil {
		form.Set("note", *input.Note)
	}
	data, err := c.ocsSend(ctx, op, http.MethodPost, shareEndpoint, form, uncertainShareCreate)
	if err != nil {
		return nil, tagAdminError(op, err, shareManageErrors)
	}
	share, err := c.sharedAnswer(op, data, uncertainShareCreate)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "share": share}, nil
}

// ownShare reads the share once and refuses, without a change, every share that is not the identity's own
// below the root. The refusal names no path.
func (c *Client) ownShare(ctx context.Context, op, id string) (*Share, error) {
	share, err := c.GetShare(ctx, id)
	if err != nil {
		return nil, err
	}
	if share.ID != id {
		return nil, shareNotFound(op)
	}
	if share.Direction != directionOutgoing {
		return nil, providerError(op, messageShareNotOwn)
	}
	return share, nil
}

// updateShare reads the share once and sends exactly one PUT.
func (c *Client) updateShare(ctx context.Context, op string, input shareManageArguments) (any, error) {
	current, err := c.ownShare(ctx, op, input.ShareID)
	if err != nil {
		return nil, err
	}
	if current.Type != shareTypeUser && current.Type != shareTypeGroup {
		return nil, providerError(op, messageShareType)
	}
	form := url.Values{}
	if input.hasRights() {
		form.Set("permissions", strconv.Itoa(permissionMask(input.withGiven(current.Rights))))
	}
	if input.ExpiresAt != nil {
		form.Set("expireDate", *input.ExpiresAt)
	}
	if input.Note != nil {
		form.Set("note", *input.Note)
	}
	data, err := c.ocsSend(ctx, op, http.MethodPut, shareEndpointFor(input.ShareID), form, uncertainShareUpdate)
	if err != nil {
		return nil, tagAdminError(op, err, shareManageErrors)
	}
	share, err := c.sharedAnswer(op, data, uncertainShareUpdate)
	if err != nil {
		return nil, err
	}
	if share.ID != input.ShareID {
		return nil, withUncertainty(invalidResponse(op, "Nextcloud answered with another share"), uncertainShareUpdate)
	}
	return map[string]any{"updated": true, "share": share}, nil
}

// deleteShare reads the share once and sends exactly one DELETE.
func (c *Client) deleteShare(ctx context.Context, op string, input shareManageArguments) (any, error) {
	current, err := c.ownShare(ctx, op, input.ShareID)
	if err != nil {
		return nil, err
	}
	if _, err := c.ocsSend(ctx, op, http.MethodDelete, shareEndpointFor(input.ShareID), nil, uncertainShareDelete); err != nil {
		return nil, tagAdminError(op, err, shareManageErrors)
	}
	return map[string]any{"deleted": true, "share_id": current.ID, "type": current.Type, "path": current.Path}, nil
}
