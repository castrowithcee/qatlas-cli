package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const groupMembers = "members"

const userIDSchema = `"user_id":{"type":"integer","minimum":1}`
const untilDateSchema = `"until_date":{"type":"integer","minimum":1}`

func memberRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{
		Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: memberDataSensitivity,
	}
}

var userIDArgument = capability.Argument{
	Name: "user_id", Description: "Telegram user identifier of the member", Required: true,
}

var untilDateArgument = capability.Argument{
	Name: "until_date", Description: "Unix time when the measure ends; Telegram treats less than 30 seconds or " +
		"more than 366 days from now as permanent",
}

var membersBan = capability.Descriptor{
	ID:      Provider + ".members.ban",
	Version: 1,
	Title:   "Ban a Telegram chat member",
	Description: "Ban one user from a bound chat of an explicit Telegram connection; with revoke_messages " +
		"Telegram also deletes all messages of the user in the chat",
	Tags:     []string{"telegram", "members", "ban"},
	Risk:     memberRisk(capability.EffectDelete),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		untilDateSchema + `,"revoke_messages":{"type":"boolean"}},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"banned":{"type":"boolean"}},` +
		`"required":["banned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument, userIDArgument, untilDateArgument,
		{Name: "revoke_messages", Description: "Also delete all messages of the user in the chat"},
	},
	Fields: []capability.Field{{Name: "banned", Description: "True when Telegram accepted the ban"}},
	Examples: []capability.Example{{
		Description: "Ban a user from this connection's chat",
		Arguments:   json.RawMessage(`{"user_id":42}`),
	}},
}

var membersUnban = capability.Descriptor{
	ID:          Provider + ".members.unban",
	Version:     1,
	Title:       "Unban a Telegram chat member",
	Description: "Lift the ban of one user in a bound chat of an explicit Telegram connection; a user who is not banned is left untouched",
	Tags:        []string{"telegram", "members", "unban"},
	Risk:        memberRisk(capability.EffectUpdate),
	Provider:    Provider,
	Group:       groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `},` +
		`"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"unbanned":{"type":"boolean"}},` +
		`"required":["unbanned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument},
	Fields:    []capability.Field{{Name: "unbanned", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Unban a user in this connection's chat",
		Arguments:   json.RawMessage(`{"user_id":42}`),
	}},
}

// restrictPermissionNames lists every ChatPermissions boolean of the Bot API; restrict requires all of them.
var restrictPermissionNames = []string{
	"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos",
	"can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages",
	"can_add_web_page_previews", "can_react_to_messages", "can_edit_tag", "can_change_info", "can_invite_users",
	"can_pin_messages", "can_manage_topics",
}

func restrictPermissionsSchema() string {
	props := ""
	for i, name := range restrictPermissionNames {
		if i > 0 {
			props += ","
		}
		props += `"` + name + `":{"type":"boolean"}`
	}
	required := ""
	for i, name := range restrictPermissionNames {
		if i > 0 {
			required += ","
		}
		required += `"` + name + `"`
	}
	return `"permissions":{"type":"object","properties":{` + props + `},"required":[` + required +
		`],"additionalProperties":false}`
}

var membersRestrict = capability.Descriptor{
	ID:      Provider + ".members.restrict",
	Version: 1,
	Title:   "Restrict a Telegram chat member",
	Description: "Set the complete permissions of one user in a bound supergroup of an explicit Telegram " +
		"connection; every permission must be given explicitly, true lifts and false restricts it",
	Tags:     []string{"telegram", "members", "restrict"},
	Risk:     memberRisk(capability.EffectUpdate),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		restrictPermissionsSchema() + `,` + untilDateSchema + `},"required":["user_id","permissions"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"restricted":{"type":"boolean"}},` +
		`"required":["restricted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument, userIDArgument,
		{Name: "permissions", Description: "Object with every ChatPermissions boolean of the Bot API, none omitted", Required: true},
		untilDateArgument,
	},
	Fields: []capability.Field{{Name: "restricted", Description: "True when Telegram accepted the restriction"}},
	Examples: []capability.Example{{
		Description: "Mute a user: all permissions false",
		Arguments:   json.RawMessage(mutedExample()),
	}},
}

func mutedExample() string {
	out := `{"user_id":42,"permissions":{`
	for i, name := range restrictPermissionNames {
		if i > 0 {
			out += ","
		}
		out += `"` + name + `":false`
	}
	return out + `}}`
}

// chatPermissions is sent with every boolean explicit, including false, so Telegram never applies a default.
type chatPermissions struct {
	CanSendMessages       bool `json:"can_send_messages"`
	CanSendAudios         bool `json:"can_send_audios"`
	CanSendDocuments      bool `json:"can_send_documents"`
	CanSendPhotos         bool `json:"can_send_photos"`
	CanSendVideos         bool `json:"can_send_videos"`
	CanSendVideoNotes     bool `json:"can_send_video_notes"`
	CanSendVoiceNotes     bool `json:"can_send_voice_notes"`
	CanSendPolls          bool `json:"can_send_polls"`
	CanSendOtherMessages  bool `json:"can_send_other_messages"`
	CanAddWebPagePreviews bool `json:"can_add_web_page_previews"`
	CanReactToMessages    bool `json:"can_react_to_messages"`
	CanEditTag            bool `json:"can_edit_tag"`
	CanChangeInfo         bool `json:"can_change_info"`
	CanInviteUsers        bool `json:"can_invite_users"`
	CanPinMessages        bool `json:"can_pin_messages"`
	CanManageTopics       bool `json:"can_manage_topics"`
}

// permissionsInput detects missing fields, which a plain bool would silently turn into false.
type permissionsInput struct {
	CanSendMessages       *bool `json:"can_send_messages"`
	CanSendAudios         *bool `json:"can_send_audios"`
	CanSendDocuments      *bool `json:"can_send_documents"`
	CanSendPhotos         *bool `json:"can_send_photos"`
	CanSendVideos         *bool `json:"can_send_videos"`
	CanSendVideoNotes     *bool `json:"can_send_video_notes"`
	CanSendVoiceNotes     *bool `json:"can_send_voice_notes"`
	CanSendPolls          *bool `json:"can_send_polls"`
	CanSendOtherMessages  *bool `json:"can_send_other_messages"`
	CanAddWebPagePreviews *bool `json:"can_add_web_page_previews"`
	CanReactToMessages    *bool `json:"can_react_to_messages"`
	CanEditTag            *bool `json:"can_edit_tag"`
	CanChangeInfo         *bool `json:"can_change_info"`
	CanInviteUsers        *bool `json:"can_invite_users"`
	CanPinMessages        *bool `json:"can_pin_messages"`
	CanManageTopics       *bool `json:"can_manage_topics"`
}

func (p *permissionsInput) complete() (chatPermissions, bool) {
	if p == nil || p.CanSendMessages == nil || p.CanSendAudios == nil || p.CanSendDocuments == nil ||
		p.CanSendPhotos == nil || p.CanSendVideos == nil || p.CanSendVideoNotes == nil || p.CanSendVoiceNotes == nil ||
		p.CanSendPolls == nil || p.CanSendOtherMessages == nil || p.CanAddWebPagePreviews == nil ||
		p.CanReactToMessages == nil || p.CanEditTag == nil || p.CanChangeInfo == nil || p.CanInviteUsers == nil ||
		p.CanPinMessages == nil || p.CanManageTopics == nil {
		return chatPermissions{}, false
	}
	return chatPermissions{
		CanSendMessages:       *p.CanSendMessages,
		CanSendAudios:         *p.CanSendAudios,
		CanSendDocuments:      *p.CanSendDocuments,
		CanSendPhotos:         *p.CanSendPhotos,
		CanSendVideos:         *p.CanSendVideos,
		CanSendVideoNotes:     *p.CanSendVideoNotes,
		CanSendVoiceNotes:     *p.CanSendVoiceNotes,
		CanSendPolls:          *p.CanSendPolls,
		CanSendOtherMessages:  *p.CanSendOtherMessages,
		CanAddWebPagePreviews: *p.CanAddWebPagePreviews,
		CanReactToMessages:    *p.CanReactToMessages,
		CanEditTag:            *p.CanEditTag,
		CanChangeInfo:         *p.CanChangeInfo,
		CanInviteUsers:        *p.CanInviteUsers,
		CanPinMessages:        *p.CanPinMessages,
		CanManageTopics:       *p.CanManageTopics,
	}, true
}

func invokeMembersBan(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat           string `json:"chat"`
		UserID         int64  `json:"user_id"`
		UntilDate      int64  `json:"until_date"`
		RevokeMessages bool   `json:"revoke_messages"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("ban member", "the validated arguments could not be read")
	}
	if err := checkMember("ban member", arguments.UserID, arguments.UntilDate); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.BanMember(ctx, arguments.UserID, arguments.UntilDate, arguments.RevokeMessages)
}

func invokeMembersUnban(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat   string `json:"chat"`
		UserID int64  `json:"user_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("unban member", "the validated arguments could not be read")
	}
	if err := checkMember("unban member", arguments.UserID, 0); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.UnbanMember(ctx, arguments.UserID)
}

func invokeMembersRestrict(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat        string            `json:"chat"`
		UserID      int64             `json:"user_id"`
		Permissions *permissionsInput `json:"permissions"`
		UntilDate   int64             `json:"until_date"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("restrict member", "the validated arguments could not be read")
	}
	if err := checkMember("restrict member", arguments.UserID, arguments.UntilDate); err != nil {
		return nil, err
	}
	permissions, ok := arguments.Permissions.complete()
	if !ok {
		return nil, providerError("restrict member", "permissions must set every permission explicitly")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.RestrictMember(ctx, arguments.UserID, permissions, arguments.UntilDate)
}

// checkMember accepts only a positive user and, when given, a positive until_date; zero means omitted.
func checkMember(op string, userID, untilDate int64) error {
	if userID <= 0 {
		return providerError(op, "user_id must be a positive integer")
	}
	if untilDate < 0 {
		return providerError(op, "until_date must be a positive Unix time")
	}
	return nil
}

// BanMember performs exactly one banChatMember request without retrying an ambiguous result.
func (c *Client) BanMember(ctx context.Context, userID, untilDate int64, revoke bool) (map[string]any, error) {
	const op = "ban member"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, untilDate); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "banChatMember", limit: defaultResponseBytes}, struct {
		ChatID         string `json:"chat_id"`
		UserID         int64  `json:"user_id"`
		UntilDate      int64  `json:"until_date,omitempty"`
		RevokeMessages bool   `json:"revoke_messages,omitempty"`
	}{c.target, userID, untilDate, revoke})
	if err != nil {
		return nil, err
	}
	return map[string]any{"banned": true}, nil
}

// UnbanMember performs exactly one unbanChatMember request. only_if_banned is fixed so that a current
// member is never removed from the chat.
func (c *Client) UnbanMember(ctx context.Context, userID int64) (map[string]any, error) {
	const op = "unban member"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, 0); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "unbanChatMember", limit: defaultResponseBytes}, struct {
		ChatID       string `json:"chat_id"`
		UserID       int64  `json:"user_id"`
		OnlyIfBanned bool   `json:"only_if_banned"`
	}{c.target, userID, true})
	if err != nil {
		return nil, err
	}
	return map[string]any{"unbanned": true}, nil
}

// RestrictMember performs exactly one restrictChatMember request. Independent permissions are fixed so
// Telegram does not derive permissions from one another.
func (c *Client) RestrictMember(ctx context.Context, userID int64, permissions chatPermissions,
	untilDate int64) (map[string]any, error) {
	const op = "restrict member"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, untilDate); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "restrictChatMember", limit: defaultResponseBytes}, struct {
		ChatID                        string          `json:"chat_id"`
		UserID                        int64           `json:"user_id"`
		Permissions                   chatPermissions `json:"permissions"`
		UseIndependentChatPermissions bool            `json:"use_independent_chat_permissions"`
		UntilDate                     int64           `json:"until_date,omitempty"`
	}{c.target, userID, permissions, true, untilDate})
	if err != nil {
		return nil, err
	}
	return map[string]any{"restricted": true}, nil
}
