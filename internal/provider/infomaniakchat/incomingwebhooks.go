package infomaniakchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// integrationSensitivity classifies the webhook tools as their own class. The ID of an incoming webhook is
// the secret of its post URL, so whoever holds it can post to the channel; it never shares the class of
// message data.
const integrationSensitivity = "infomaniak-kchat-integration-secrets"

// Local bounds of the webhook fields this provider writes, and the refusal of a webhook outside the
// boundary. The refusal names neither the webhook ID nor the foreign channel or team.
const (
	maxHookDisplayName = 64
	maxHookDescription = 500
	hookOutOfScope     = "hook_id belongs to a channel outside the targets of this connection"
)

// The suffixes of an update and a delete request whose result is unclear.
const (
	uncertainHookUpdate = "; the webhook may have been changed, read it before changing it again"
	uncertainHookDelete = "; the webhook may have been deleted, read the webhooks before deleting it again"
)

func hookRisk(effect capability.Effect, idempotency capability.Idempotency, confirmation capability.Confirmation) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: confirmation, OpenWorld: true,
		DataSensitivity: integrationSensitivity}
}

var hookIDArgument = capability.Argument{Name: "hook_id", Required: true,
	Description: "Incoming webhook identifier from incomingwebhooks.list; it is the secret of the webhook's post " +
		"URL, so treat it as a secret"}

var incomingHookSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"channel_id":` + idSchema + `,` +
	`"display_name":{"type":"string"},"description":{"type":"string"},"username":{"type":"string"},` +
	`"icon_url":{"type":"string"},"channel_locked":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["team_id","channel_id","display_name","description","channel_locked"],"additionalProperties":false}`

var incomingHookFields = []capability.Field{
	{Name: "id", Description: "Webhook identifier, the secret of its post URL; only listed by incomingwebhooks.list, " +
		"absent elsewhere because the caller named it"},
	{Name: "team_id", Description: "Team of the webhook"},
	{Name: "channel_id", Description: "Channel the webhook posts to"},
	{Name: "display_name", Description: "Display name, untrusted data"},
	{Name: "description", Description: "Description, untrusted data"},
	{Name: "username", Description: "Username the webhook posts as when it overrides the sender, untrusted data"},
	{Name: "icon_url", Description: "Profile picture URL the webhook posts with, untrusted data; never fetched"},
	{Name: "channel_locked", Description: "True when the webhook may post only to its channel"},
	{Name: "created_at", Description: "Creation time, RFC 3339 in UTC"},
	{Name: "updated_at", Description: "Last change time, RFC 3339 in UTC"},
}

var incomingWebhooksList = capability.Descriptor{
	ID:      Provider + ".incomingwebhooks.list",
	Version: 1,
	Title:   "List Infomaniak kChat incoming webhooks",
	Description: "List the active incoming webhooks of one team this connection is bound to, one kChat page at a " +
		"time, restricted to the live public and private channels of that team inside this connection's channel " +
		"allow-list; the IDs are the secrets of the webhooks' post URLs",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "list"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"webhooks":{"type":"array","items":` + incomingHookSchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["team_id","webhooks","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Webhooks per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, incomingHookFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "per_page", Description: "Page size that was requested"},
		capability.Field{Name: "count", Description: "Number of webhooks reported on this page, after the filter"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
			"pages are never read on by themselves"},
	),
	Examples: []capability.Example{{Description: "List the incoming webhooks of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var incomingWebhooksGet = capability.Descriptor{
	ID:      Provider + ".incomingwebhooks.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat incoming webhook",
	Description: "Read exactly one active incoming webhook whose channel this connection may reach; the hook_id " +
		"is the secret of its post URL and is not returned",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "get"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(incomingHookSchema),
	Arguments:    []capability.Argument{hookIDArgument},
	Fields:       incomingHookFields,
	Examples: []capability.Example{{Description: "Read one incoming webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000"}`)}},
}

var incomingWebhooksUpdate = capability.Descriptor{
	ID:      Provider + ".incomingwebhooks.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat incoming webhook",
	Description: "Change the display name or description of exactly one active incoming webhook whose channel " +
		"this connection may reach, with one confirmed request; the channel, username, picture, and lock stay as " +
		"read, and only the given fields change",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "update"},
	Risk:                  hookRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `,` +
		`"display_name":{"type":"string","maxLength":` + itoa(maxHookDisplayName) + `},` +
		`"description":{"type":"string","maxLength":` + itoa(maxHookDescription) + `}},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(incomingHookSchema),
	Arguments: []capability.Argument{hookIDArgument,
		{Name: "display_name", Description: "New display name, up to " + itoa(maxHookDisplayName) +
			" characters; empty clears it"},
		{Name: "description", Description: "New description, up to " + itoa(maxHookDescription) +
			" characters; empty clears it; at least one of display_name and description is required"}},
	Fields: incomingHookFields,
	Examples: []capability.Example{{Description: "Change the description of one incoming webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000","description":"Build alerts"}`)}},
}

var incomingWebhooksDelete = capability.Descriptor{
	ID:      Provider + ".incomingwebhooks.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat incoming webhook",
	Description: "Delete exactly one active incoming webhook whose channel this connection may reach with one " +
		"confirmed request; its post URL stops working and cannot be restored",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "delete"},
	Risk:                  hookRisk(capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{hookIDArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True once kChat confirmed the deletion"}},
	Examples: []capability.Example{{Description: "Delete one incoming webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000"}`)}},
}

// incomingHookJSON is the subset of the kChat IncomingWebhook resource this provider reads.
type incomingHookJSON struct {
	ID            string `json:"id"`
	TeamID        string `json:"team_id"`
	ChannelID     string `json:"channel_id"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	Username      string `json:"username"`
	IconURL       string `json:"icon_url"`
	ChannelLocked bool   `json:"channel_locked"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
}

// IncomingWebhook is the stable Qatlas view of one incoming webhook. The ID is set only by a listing: every
// other tool is addressed by it, and the redactor masks it there.
type IncomingWebhook struct {
	ID            string `json:"id,omitempty"`
	TeamID        string `json:"team_id"`
	ChannelID     string `json:"channel_id"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	Username      string `json:"username,omitempty"`
	IconURL       string `json:"icon_url,omitempty"`
	ChannelLocked bool   `json:"channel_locked"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func incomingHookOf(hook *incomingHookJSON, withID bool) IncomingWebhook {
	out := IncomingWebhook{TeamID: hook.TeamID, ChannelID: hook.ChannelID, DisplayName: bounded(hook.DisplayName),
		Description: bounded(hook.Description), Username: bounded(hook.Username), IconURL: bounded(hook.IconURL),
		ChannelLocked: hook.ChannelLocked, CreatedAt: msToRFC3339(hook.CreateAt), UpdatedAt: msToRFC3339(hook.UpdateAt)}
	if withID {
		out.ID = hook.ID
	}
	return out
}

// IncomingWebhooksPage is one page of the incoming webhooks of one team.
type IncomingWebhooksPage struct {
	TeamID   string            `json:"team_id"`
	Webhooks []IncomingWebhook `json:"webhooks"`
	Page     int               `json:"page"`
	PerPage  int               `json:"per_page"`
	Count    int               `json:"count"`
	HasMore  bool              `json:"has_more"`
}

// IncomingWebhookDeleted is the answer of one confirmed deletion.
type IncomingWebhookDeleted struct {
	Deleted bool `json:"deleted"`
}

func incomingHookPath(hookID string) string {
	return "/api/v4/hooks/incoming/" + url.PathEscape(hookID)
}

func invokeIncomingWebhooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input archivedListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list incoming webhooks", "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListIncomingWebhooks(ctx, input.TeamID, input.Page, input.PerPage)
}

// ListIncomingWebhooks reads exactly one page of kChat's own listing of the incoming webhooks of one bound
// team and never reads on by itself. A webhook is kept only when kChat reports it for that team and not
// deleted, and its channel is one of the team's live public or private channels the token is a member of,
// inside the channel allow-list; the membership requirement is the narrower reading of "bound channel".
func (c *Client) ListIncomingWebhooks(ctx context.Context, teamID string, page, perPage int) (*IncomingWebhooksPage, error) {
	const op = "list incoming webhooks"
	query := url.Values{"team_id": {teamID}, "page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}}
	var hooks []incomingHookJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/hooks/incoming", query, nil, &hooks, false); err != nil {
		return nil, err
	}
	reachable, err := c.reachableChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	entries := make([]IncomingWebhook, 0, len(hooks))
	for i := range hooks {
		hook := &hooks[i]
		if !validMattermostID(hook.ID) || hook.TeamID != teamID || hook.DeleteAt != 0 ||
			reachable[hook.ChannelID] == nil {
			continue
		}
		entries = append(entries, incomingHookOf(hook, true))
	}
	return &IncomingWebhooksPage{TeamID: teamID, Webhooks: entries, Page: page, PerPage: perPage,
		Count: len(entries), HasMore: len(hooks) >= perPage}, nil
}

type hookArguments struct {
	HookID      string  `json:"hook_id"`
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

// prepareHook validates the hook ID locally and registers it with the redactor before any request, because
// it is the secret of the webhook's post URL and must reach no error or log.
func prepareHook(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, raw json.RawMessage, check func(*hookArguments) error) (*Client, *hookArguments, error) {
	var input hookArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.HookID) {
		return nil, nil, invalidRequest("hook_id must be a kChat-style identifier")
	}
	if red != nil {
		red.Add(input.HookID)
	}
	if check != nil {
		if err := check(&input); err != nil {
			return nil, nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	return client, &input, nil
}

func invokeIncomingWebhooksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get incoming webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	hook, err := client.boundIncomingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return incomingHookOf(hook, false), nil
}

// boundIncomingHook reads one webhook and binds it through its channel: the webhook must be the one asked
// for and not deleted, its team and channel must be inside the local boundary, and the live channel check
// must pass. It is the one gate every webhook tool passes before it returns or changes anything. A webhook
// of a direct or group channel has no team and is refused, the narrower reading.
func (c *Client) boundIncomingHook(ctx context.Context, op, hookID string) (*incomingHookJSON, error) {
	var hook incomingHookJSON
	if err := c.do(ctx, op, http.MethodGet, incomingHookPath(hookID), nil, nil, &hook, false); err != nil {
		return nil, err
	}
	if hook.ID != hookID || !validMattermostID(hook.ChannelID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	if hook.DeleteAt != 0 {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "kChat does not hold this resource or does not show it to this token"}
	}
	if !c.scope.allowsTeam(hook.TeamID) || !c.scope.allowsChannel(hook.ChannelID) {
		return nil, invalidRequest(hookOutOfScope)
	}
	if err := c.verifyChannelScope(ctx, op, hook.ChannelID); err != nil {
		var invalid *provider.InvalidRequestError
		if errors.As(err, &invalid) {
			return nil, invalidRequest(hookOutOfScope)
		}
		return nil, err
	}
	return &hook, nil
}

func invokeIncomingWebhooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update incoming webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, func(in *hookArguments) error {
		if in.DisplayName == nil && in.Description == nil {
			return invalidRequest("give at least one of display_name and description")
		}
		if in.DisplayName != nil && !validChannelText(*in.DisplayName, 0, maxHookDisplayName, false) {
			return invalidRequest("display_name must be up to " + itoa(maxHookDisplayName) +
				" characters without control characters")
		}
		if in.Description != nil && !validChannelText(*in.Description, 0, maxHookDescription, false) {
			return invalidRequest("description must be up to " + itoa(maxHookDescription) +
				" characters without control characters")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	hook, err := client.boundIncomingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return client.UpdateIncomingWebhook(ctx, hook, input)
}

// UpdateIncomingWebhook sends exactly one PUT that writes the webhook as read, with only the given display
// name and description replaced, and never repeats it: a failure after the request may have reached kChat
// says so instead. The target channel, username, picture, and lock are carried over unchanged.
func (c *Client) UpdateIncomingWebhook(ctx context.Context, current *incomingHookJSON, input *hookArguments) (*IncomingWebhook, error) {
	const op = "update incoming webhook"
	body := struct {
		ID            string `json:"id"`
		ChannelID     string `json:"channel_id"`
		DisplayName   string `json:"display_name"`
		Description   string `json:"description"`
		Username      string `json:"username"`
		IconURL       string `json:"icon_url"`
		ChannelLocked bool   `json:"channel_locked"`
	}{current.ID, current.ChannelID, current.DisplayName, current.Description, current.Username, current.IconURL,
		current.ChannelLocked}
	if input.DisplayName != nil {
		body.DisplayName = *input.DisplayName
	}
	if input.Description != nil {
		body.Description = *input.Description
	}
	var hook incomingHookJSON
	if err := c.doWith(ctx, op, http.MethodPut, incomingHookPath(current.ID), nil, body, &hook,
		uncertainHookUpdate); err != nil {
		return nil, err
	}
	if hook.ID != current.ID || hook.ChannelID != current.ChannelID || hook.TeamID != current.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainHookUpdate}
	}
	out := incomingHookOf(&hook, false)
	return &out, nil
}

func invokeIncomingWebhooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete incoming webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	hook, err := client.boundIncomingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return client.DeleteIncomingWebhook(ctx, hook.ID)
}

// DeleteIncomingWebhook sends exactly one DELETE and never repeats it: a failure after the request may have
// reached kChat says so instead. kChat answers with a status only.
func (c *Client) DeleteIncomingWebhook(ctx context.Context, hookID string) (*IncomingWebhookDeleted, error) {
	const op = "delete incoming webhook"
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodDelete, incomingHookPath(hookID), nil, nil, &answer,
		uncertainHookDelete); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainHookDelete}
	}
	return &IncomingWebhookDeleted{Deleted: true}, nil
}
