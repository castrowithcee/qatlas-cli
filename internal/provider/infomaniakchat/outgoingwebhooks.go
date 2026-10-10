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

// outgoingHookOutOfScope is the refusal of an outgoing webhook outside the boundary. It names neither the
// webhook ID nor the foreign team or channel.
const outgoingHookOutOfScope = "hook_id belongs to a team or channel outside the targets of this connection"

// maxHookListEntries bounds the trigger words and callback origins reported for one webhook.
const maxHookListEntries = 50

var outgoingHookIDArgument = capability.Argument{Name: "hook_id", Required: true,
	Description: "Outgoing webhook identifier from outgoingwebhooks.list; treat it as a secret"}

var outgoingHookSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"channel_id":` + idSchema + `,` +
	`"display_name":{"type":"string"},"description":{"type":"string"},` +
	`"trigger_words":{"type":"array","items":{"type":"string"}},"trigger_when":{"type":"integer"},` +
	`"callback_origins":{"type":"array","items":{"type":"string"}},"content_type":{"type":"string"},` +
	`"username":{"type":"string"},"icon_url":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["team_id","display_name","description","trigger_words","trigger_when","callback_origins"],` +
	`"additionalProperties":false}`

var outgoingHookFields = []capability.Field{
	{Name: "id", Description: "Webhook identifier; only listed by outgoingwebhooks.list, absent elsewhere because " +
		"the caller named it"},
	{Name: "team_id", Description: "Team of the webhook"},
	{Name: "channel_id", Description: "Public channel the webhook watches; absent for a webhook that watches " +
		"every public channel of its team, which a connection with a channel allow-list does not reach"},
	{Name: "display_name", Description: "Display name, untrusted data"},
	{Name: "description", Description: "Description, untrusted data"},
	{Name: "trigger_words", Description: "Words the webhook triggers on, untrusted data"},
	{Name: "trigger_when", Description: "0 when a trigger word anywhere in a message triggers, 1 when only a " +
		"leading one does"},
	{Name: "callback_origins", Description: "Scheme and host of each callback URL, untrusted data; path, query, " +
		"and credentials are never returned, and an unreadable URL is left out"},
	{Name: "content_type", Description: "Format the payload is posted in, untrusted data"},
	{Name: "username", Description: "Username the webhook posts as when it overrides the sender, untrusted data"},
	{Name: "icon_url", Description: "Profile picture URL the webhook posts with, untrusted data; never fetched"},
	{Name: "created_at", Description: "Creation time, RFC 3339 in UTC"},
	{Name: "updated_at", Description: "Last change time, RFC 3339 in UTC"},
}

var outgoingWebhooksList = capability.Descriptor{
	ID:      Provider + ".outgoingwebhooks.list",
	Version: 1,
	Title:   "List Infomaniak kChat outgoing webhooks",
	Description: "List the active outgoing webhooks of one team this connection is bound to, one kChat page at a " +
		"time, optionally of one channel, restricted to the live channels of that team inside this connection's " +
		"channel allow-list; callback URLs appear only as origins and tokens never",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "list"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"webhooks":{"type":"array","items":` + outgoingHookSchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["team_id","webhooks","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "channel_id", Description: "Only the webhooks of this channel; it must be inside the connection's " +
			"channel allow-list"},
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Webhooks per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, outgoingHookFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "per_page", Description: "Page size that was requested"},
		capability.Field{Name: "count", Description: "Number of webhooks reported on this page, after the filter"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
			"pages are never read on by themselves"},
	),
	Examples: []capability.Example{{Description: "List the outgoing webhooks of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var outgoingWebhooksGet = capability.Descriptor{
	ID:      Provider + ".outgoingwebhooks.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat outgoing webhook",
	Description: "Read exactly one active outgoing webhook of a bound team whose channel this connection may " +
		"reach; callback URLs appear only as origins and the token is never returned",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "get"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(outgoingHookSchema),
	Arguments:    []capability.Argument{outgoingHookIDArgument},
	Fields:       outgoingHookFields,
	Examples: []capability.Example{{Description: "Read one outgoing webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000"}`)}},
}

var outgoingWebhooksUpdate = capability.Descriptor{
	ID:      Provider + ".outgoingwebhooks.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat outgoing webhook",
	Description: "Change the display name or description of exactly one active outgoing webhook of a bound team " +
		"whose channel this connection may reach, with one confirmed request; channel, callback URLs, trigger " +
		"words, and the other fields stay as read, and only the given fields change",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "update"},
	Risk:                  hookRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `,` +
		`"display_name":{"type":"string","maxLength":` + itoa(maxHookDisplayName) + `},` +
		`"description":{"type":"string","maxLength":` + itoa(maxHookDescription) + `}},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(outgoingHookSchema),
	Arguments: []capability.Argument{outgoingHookIDArgument,
		{Name: "display_name", Description: "New display name, up to " + itoa(maxHookDisplayName) +
			" characters; empty clears it"},
		{Name: "description", Description: "New description, up to " + itoa(maxHookDescription) +
			" characters; empty clears it; at least one of display_name and description is required"}},
	Fields: outgoingHookFields,
	Examples: []capability.Example{{Description: "Change the description of one outgoing webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000","description":"Build alerts"}`)}},
}

var outgoingWebhooksDelete = capability.Descriptor{
	ID:      Provider + ".outgoingwebhooks.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat outgoing webhook",
	Description: "Delete exactly one active outgoing webhook of a bound team whose channel this connection may " +
		"reach with one confirmed request; it stops triggering and cannot be restored",
	Tags:                  []string{"infomaniak", "kchat", "webhooks", "integrations", "delete"},
	Risk:                  hookRisk(capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + idSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{outgoingHookIDArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True once kChat confirmed the deletion"}},
	Examples: []capability.Example{{Description: "Delete one outgoing webhook",
		Arguments: json.RawMessage(`{"hook_id":"abc123hook00000000000000000"}`)}},
}

// outgoingHookJSON is the subset of the kChat OutgoingWebhook resource this provider reads. The token is
// deliberately not decoded: it can reach no result, and kChat keeps it on an update.
type outgoingHookJSON struct {
	ID           string   `json:"id"`
	TeamID       string   `json:"team_id"`
	ChannelID    string   `json:"channel_id"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	TriggerWords []string `json:"trigger_words"`
	TriggerWhen  int      `json:"trigger_when"`
	CallbackURLs []string `json:"callback_urls"`
	ContentType  string   `json:"content_type"`
	Username     string   `json:"username"`
	IconURL      string   `json:"icon_url"`
	CreateAt     int64    `json:"create_at"`
	UpdateAt     int64    `json:"update_at"`
	DeleteAt     int64    `json:"delete_at"`
}

// OutgoingWebhook is the stable Qatlas view of one outgoing webhook. The ID is set only by a listing: every
// other tool is addressed by it, and the redactor masks it there.
type OutgoingWebhook struct {
	ID              string   `json:"id,omitempty"`
	TeamID          string   `json:"team_id"`
	ChannelID       string   `json:"channel_id,omitempty"`
	DisplayName     string   `json:"display_name"`
	Description     string   `json:"description"`
	TriggerWords    []string `json:"trigger_words"`
	TriggerWhen     int      `json:"trigger_when"`
	CallbackOrigins []string `json:"callback_origins"`
	ContentType     string   `json:"content_type,omitempty"`
	Username        string   `json:"username,omitempty"`
	IconURL         string   `json:"icon_url,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// callbackOrigin reduces a callback URL to scheme and host, because path, query, and userinfo can carry
// secrets. An unreadable URL, or one that is not http or https with a host, yields nothing.
func callbackOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return bounded(parsed.Scheme + "://" + parsed.Host), true
}

func outgoingHookOf(hook *outgoingHookJSON, withID bool) OutgoingWebhook {
	words := make([]string, 0, len(hook.TriggerWords))
	for _, word := range hook.TriggerWords {
		if len(words) == maxHookListEntries {
			break
		}
		words = append(words, bounded(word))
	}
	origins := make([]string, 0, len(hook.CallbackURLs))
	for _, raw := range hook.CallbackURLs {
		if origin, ok := callbackOrigin(raw); ok && len(origins) < maxHookListEntries {
			origins = append(origins, origin)
		}
	}
	out := OutgoingWebhook{TeamID: hook.TeamID, ChannelID: hook.ChannelID, DisplayName: bounded(hook.DisplayName),
		Description: bounded(hook.Description), TriggerWords: words, TriggerWhen: hook.TriggerWhen,
		CallbackOrigins: origins, ContentType: bounded(hook.ContentType), Username: bounded(hook.Username),
		IconURL: bounded(hook.IconURL), CreatedAt: msToRFC3339(hook.CreateAt), UpdatedAt: msToRFC3339(hook.UpdateAt)}
	if withID {
		out.ID = hook.ID
	}
	return out
}

// OutgoingWebhooksPage is one page of the outgoing webhooks of one team.
type OutgoingWebhooksPage struct {
	TeamID   string            `json:"team_id"`
	Webhooks []OutgoingWebhook `json:"webhooks"`
	Page     int               `json:"page"`
	PerPage  int               `json:"per_page"`
	Count    int               `json:"count"`
	HasMore  bool              `json:"has_more"`
}

// OutgoingWebhookDeleted is the answer of one confirmed deletion.
type OutgoingWebhookDeleted struct {
	Deleted bool `json:"deleted"`
}

func outgoingHookPath(hookID string) string {
	return "/api/v4/hooks/outgoing/" + url.PathEscape(hookID)
}

type outgoingListArguments struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	PerPage   int    `json:"per_page"`
}

func invokeOutgoingWebhooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input outgoingListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list outgoing webhooks", "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if input.ChannelID != "" {
		if err := selectChannel(resolved, input.ChannelID); err != nil {
			return nil, err
		}
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
	return client.ListOutgoingWebhooks(ctx, input.TeamID, input.ChannelID, input.Page, input.PerPage)
}

// ListOutgoingWebhooks reads exactly one page of kChat's own listing of the outgoing webhooks of one bound
// team and never reads on by itself. A webhook is kept only when kChat reports it for that team, not
// deleted, and, if it names a channel, that channel is one of the team's live channels the token is a
// member of inside the allow-list. A webhook without a channel watches every public channel of its team,
// so it is kept only on a connection without a channel allow-list: the narrower reading.
func (c *Client) ListOutgoingWebhooks(ctx context.Context, teamID, channelID string, page, perPage int) (
	*OutgoingWebhooksPage, error) {
	const op = "list outgoing webhooks"
	query := url.Values{"team_id": {teamID}, "page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}}
	if channelID != "" {
		query.Set("channel_id", channelID)
	}
	var hooks []outgoingHookJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/hooks/outgoing", query, nil, &hooks, false); err != nil {
		return nil, err
	}
	reachable, err := c.reachableChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	entries := make([]OutgoingWebhook, 0, len(hooks))
	for i := range hooks {
		hook := &hooks[i]
		if !validMattermostID(hook.ID) || hook.TeamID != teamID || hook.DeleteAt != 0 {
			continue
		}
		if hook.ChannelID == "" {
			if len(c.scope.channels) != 0 || channelID != "" {
				continue
			}
		} else if reachable[hook.ChannelID] == nil || (channelID != "" && hook.ChannelID != channelID) {
			continue
		}
		entries = append(entries, outgoingHookOf(hook, true))
	}
	return &OutgoingWebhooksPage{TeamID: teamID, Webhooks: entries, Page: page, PerPage: perPage,
		Count: len(entries), HasMore: len(hooks) >= perPage}, nil
}

func invokeOutgoingWebhooksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get outgoing webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	hook, err := client.boundOutgoingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return outgoingHookOf(hook, false), nil
}

// boundOutgoingHook reads one webhook and binds it: it must be the one asked for and not deleted, its team
// must be bound, and, when it names a channel, the channel must be inside the allow-list and pass the live
// check. A webhook without a channel reads every public channel of its team, so it is refused whenever the
// connection has a channel allow-list. It is the one gate every outgoing webhook tool passes before it
// returns or changes anything.
func (c *Client) boundOutgoingHook(ctx context.Context, op, hookID string) (*outgoingHookJSON, error) {
	var hook outgoingHookJSON
	if err := c.do(ctx, op, http.MethodGet, outgoingHookPath(hookID), nil, nil, &hook, false); err != nil {
		return nil, err
	}
	if hook.ID != hookID || (hook.ChannelID != "" && !validMattermostID(hook.ChannelID)) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	if hook.DeleteAt != 0 {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "kChat does not hold this resource or does not show it to this token"}
	}
	if !c.scope.allowsTeam(hook.TeamID) {
		return nil, invalidRequest(outgoingHookOutOfScope)
	}
	if hook.ChannelID == "" {
		if len(c.scope.channels) != 0 {
			return nil, invalidRequest(outgoingHookOutOfScope)
		}
		return &hook, nil
	}
	if !c.scope.allowsChannel(hook.ChannelID) {
		return nil, invalidRequest(outgoingHookOutOfScope)
	}
	if err := c.verifyChannelScope(ctx, op, hook.ChannelID); err != nil {
		var invalid *provider.InvalidRequestError
		if errors.As(err, &invalid) {
			return nil, invalidRequest(outgoingHookOutOfScope)
		}
		return nil, err
	}
	return &hook, nil
}

func invokeOutgoingWebhooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update outgoing webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, checkHookTexts)
	if err != nil {
		return nil, err
	}
	hook, err := client.boundOutgoingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return client.UpdateOutgoingWebhook(ctx, hook, input)
}

// UpdateOutgoingWebhook sends exactly one PUT that writes the webhook as read, with only the given display
// name and description replaced, and never repeats it: a failure after the request may have reached kChat
// says so instead. Channel, callback URLs, trigger words, and the other fields are carried over unchanged
// and complete; the token is not sent, kChat keeps it.
func (c *Client) UpdateOutgoingWebhook(ctx context.Context, current *outgoingHookJSON, input *hookArguments) (
	*OutgoingWebhook, error) {
	const op = "update outgoing webhook"
	body := struct {
		ID           string   `json:"id"`
		ChannelID    string   `json:"channel_id"`
		DisplayName  string   `json:"display_name"`
		Description  string   `json:"description"`
		TriggerWords []string `json:"trigger_words"`
		TriggerWhen  int      `json:"trigger_when"`
		CallbackURLs []string `json:"callback_urls"`
		ContentType  string   `json:"content_type"`
		Username     string   `json:"username"`
		IconURL      string   `json:"icon_url"`
	}{current.ID, current.ChannelID, current.DisplayName, current.Description, nonNil(current.TriggerWords),
		current.TriggerWhen, nonNil(current.CallbackURLs), current.ContentType, current.Username, current.IconURL}
	if input.DisplayName != nil {
		body.DisplayName = *input.DisplayName
	}
	if input.Description != nil {
		body.Description = *input.Description
	}
	var hook outgoingHookJSON
	if err := c.doWith(ctx, op, http.MethodPut, outgoingHookPath(current.ID), nil, body, &hook,
		uncertainHookUpdate); err != nil {
		return nil, err
	}
	if hook.ID != current.ID || hook.ChannelID != current.ChannelID || hook.TeamID != current.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainHookUpdate}
	}
	out := outgoingHookOf(&hook, false)
	return &out, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func invokeOutgoingWebhooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete outgoing webhook"
	client, input, err := prepareHook(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	hook, err := client.boundOutgoingHook(ctx, op, input.HookID)
	if err != nil {
		return nil, err
	}
	return client.DeleteOutgoingWebhook(ctx, hook.ID)
}

// DeleteOutgoingWebhook sends exactly one DELETE and never repeats it: a failure after the request may have
// reached kChat says so instead. kChat answers with a status only.
func (c *Client) DeleteOutgoingWebhook(ctx context.Context, hookID string) (*OutgoingWebhookDeleted, error) {
	const op = "delete outgoing webhook"
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodDelete, outgoingHookPath(hookID), nil, nil, &answer,
		uncertainHookDelete); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainHookDelete}
	}
	return &OutgoingWebhookDeleted{Deleted: true}, nil
}
