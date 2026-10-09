package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const uncertainNotify = "; the notification settings may have been changed, check them in kChat before " +
	"changing them again"

// The fixed values of the notification fields. kChat takes true and false for email, as its OpenAPI
// document states, and all or mention for mark_unread.
var (
	notifyLevels      = []string{"default", "all", "mention", "none"}
	notifyEmailModes  = []string{"default", "true", "false"}
	notifyUnreadModes = []string{"all", "mention"}
)

func enumSchema(values []string) string {
	return `{"type":"string","enum":["` + strings.Join(values, `","`) + `"]}`
}

var notificationsSchema = `{"type":"object","properties":{"channel_id":` + idSchema + `,"desktop":` +
	enumSchema(notifyLevels) + `,"push":` + enumSchema(notifyLevels) + `,"email":` + enumSchema(notifyEmailModes) +
	`,"mark_unread":` + enumSchema(notifyUnreadModes) + `},"required":["channel_id"],"additionalProperties":false}`

var channelNotificationsUpdate = capability.Descriptor{
	ID:      Provider + ".channelnotifications.update",
	Version: 1,
	Title:   "Change Infomaniak kChat channel notifications",
	Description: "Change the notification settings of the token's own user for exactly one public or private " +
		"channel of a bound team this connection may reach, with one confirmed request; direct and group " +
		"channels and archived channels are not reachable. Only the given fields change",
	Tags:        []string{"infomaniak", "kchat", "channels", "notifications", "update"},
	Risk:        channelChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	InputSchema: json.RawMessage(notificationsSchema),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"desktop":{"type":"string"},"push":{"type":"string"},"email":{"type":"string"},` +
		`"mark_unread":{"type":"string"}},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{channelIDArgument,
		{Name: "desktop", Description: "Desktop notifications: default, all, mention, or none"},
		{Name: "push", Description: "Push notifications: default, all, mention, or none"},
		{Name: "email", Description: "Email notifications: default, true, or false"},
		{Name: "mark_unread", Description: "Mark the channel unread for all or only mention; at least one of " +
			"desktop, push, email, and mark_unread is required"},
	},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel whose settings were changed"},
		{Name: "desktop", Description: "Value that was set, when given"},
		{Name: "push", Description: "Value that was set, when given"},
		{Name: "email", Description: "Value that was set, when given"},
		{Name: "mark_unread", Description: "Value that was set, when given"},
	},
	Examples: []capability.Example{{Description: "Notify only on mentions in one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","desktop":"mention",` +
			`"push":"mention"}`)}},
}

// NotificationsChanged is the answer of one confirmed notification change: the values that were sent.
type NotificationsChanged struct {
	ChannelID  string `json:"channel_id"`
	Desktop    string `json:"desktop,omitempty"`
	Push       string `json:"push,omitempty"`
	Email      string `json:"email,omitempty"`
	MarkUnread string `json:"mark_unread,omitempty"`
}

type notificationsArguments struct {
	ChannelID  string `json:"channel_id"`
	Desktop    string `json:"desktop"`
	Push       string `json:"push"`
	Email      string `json:"email"`
	MarkUnread string `json:"mark_unread"`
}

func oneOf(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invokeChannelNotificationsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update channel notifications"
	var input notificationsArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	if input.Desktop == "" && input.Push == "" && input.Email == "" && input.MarkUnread == "" {
		return nil, invalidRequest("give at least one of desktop, push, email, and mark_unread")
	}
	if (input.Desktop != "" && !oneOf(input.Desktop, notifyLevels)) ||
		(input.Push != "" && !oneOf(input.Push, notifyLevels)) {
		return nil, invalidRequest("desktop and push must be default, all, mention, or none")
	}
	if input.Email != "" && !oneOf(input.Email, notifyEmailModes) {
		return nil, invalidRequest("email must be default, true, or false")
	}
	if input.MarkUnread != "" && !oneOf(input.MarkUnread, notifyUnreadModes) {
		return nil, invalidRequest("mark_unread must be all or mention")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// A direct or group channel is deliberately refused: boundChannel admits only a channel of a bound team.
	ch, err := client.boundChannel(ctx, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.DeleteAt != 0 || (ch.Type != "O" && ch.Type != "P") {
		return nil, invalidRequest("only active public and private channels can change their notifications")
	}
	return client.UpdateChannelNotifications(ctx, input)
}

// UpdateChannelNotifications sends exactly one PUT carrying only the given fields of the four allowed ones for
// the own user and never repeats it: a failure after the request may have reached kChat says so instead.
func (c *Client) UpdateChannelNotifications(ctx context.Context,
	input notificationsArguments) (*NotificationsChanged, error) {
	const op = "update channel notifications"
	body := map[string]string{}
	for key, value := range map[string]string{"desktop": input.Desktop, "push": input.Push, "email": input.Email,
		"mark_unread": input.MarkUnread} {
		if value != "" {
			body[key] = value
		}
	}
	var answer struct {
		Status string `json:"status"`
	}
	path := "/api/v4/channels/" + url.PathEscape(input.ChannelID) + "/members/me/notify_props"
	if err := c.doWith(ctx, op, http.MethodPut, path, nil, body, &answer, uncertainNotify); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainNotify}
	}
	return &NotificationsChanged{ChannelID: input.ChannelID, Desktop: input.Desktop, Push: input.Push,
		Email: input.Email, MarkUnread: input.MarkUnread}, nil
}
