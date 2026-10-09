package telegram

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupChats = "chats"
	// memberDataSensitivity classifies tools that expose user identities and rights of chat members.
	memberDataSensitivity = "telegram-member-data"
	maxAdministrators     = 200
	// administratorsBytes bounds the getChatAdministrators response; 200 entries exceed the default bound.
	administratorsBytes = 256 << 10
)

const chatArgument = `"chat":{"type":"string","minLength":1,"maxLength":128}`

var chatArgumentDoc = capability.Argument{
	Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat",
}

func readRisk(sensitivity string) capability.Risk {
	return capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sensitivity,
	}
}

var memberRights = []string{
	"can_be_edited", "can_manage_chat", "can_delete_messages", "can_manage_video_chats", "can_restrict_members",
	"can_promote_members", "can_change_info", "can_invite_users", "can_post_stories", "can_edit_stories",
	"can_delete_stories", "can_post_messages", "can_edit_messages", "can_pin_messages", "can_manage_topics",
	"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos",
	"can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages",
	"can_add_web_page_previews",
}

var permissionNames = []string{
	"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos",
	"can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages",
	"can_add_web_page_previews", "can_change_info", "can_invite_users", "can_pin_messages", "can_manage_topics",
}

func booleanProperties(names []string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(`,"` + n + `":{"type":"boolean"}`)
	}
	return b.String()
}

var memberSchema = `{"type":"object","properties":{` +
	`"user":{"type":"object","properties":{"id":{"type":"integer"},"is_bot":{"type":"boolean"},` +
	`"first_name":{"type":"string"},"last_name":{"type":"string"},"username":{"type":"string"}},` +
	`"required":["id","is_bot"],"additionalProperties":false},"status":{"type":"string"},` +
	`"custom_title":{"type":"string"},"until_date":{"type":"integer"},"is_anonymous":{"type":"boolean"},` +
	`"is_member":{"type":"boolean"}` + booleanProperties(memberRights) + `},` +
	`"required":["user","status"],"additionalProperties":false}`

var permissionsSchema = `{"type":"object","properties":{` + booleanProperties(permissionNames)[1:] +
	`},"additionalProperties":false}`

var memberFields = []capability.Field{
	{Name: "user", Description: "Member identity: id, is_bot, first_name, last_name, username"},
	{Name: "status", Description: "creator, administrator, member, restricted, left, or kicked"},
	{Name: "custom_title", Description: "Custom administrator title"},
	{Name: "until_date", Description: "Unix time until which a restriction or ban lasts; absent when unlimited"},
	{Name: "is_anonymous", Description: "True when the member hides their presence"},
	{Name: "is_member", Description: "True when a restricted user is currently a member"},
	{Name: "can_*", Description: "Rights of the member as reported by Telegram; only rights Telegram returned appear"},
}

var chatsGet = capability.Descriptor{
	ID:          Provider + ".chats.get",
	Version:     1,
	Title:       "Get Telegram chat information",
	Description: "Read the master data of a bound chat; the invite link and all other fields are never shown",
	Tags:        []string{"telegram", "chats", "read"},
	Risk:        readRisk(dataSensitivity),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatArgument + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},` +
		`"title":{"type":"string"},"username":{"type":"string"},"is_forum":{"type":"boolean"},` +
		`"description":{"type":"string"},"permissions":` + permissionsSchema + `,"slow_mode_delay":{"type":"integer"},` +
		`"pinned_message_id":{"type":"integer"},"linked_chat_id":{"type":"integer"}},` +
		`"required":["id","type"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgumentDoc},
	Fields: []capability.Field{
		{Name: "id", Description: "Telegram chat identifier"},
		{Name: "type", Description: "private, group, supergroup, or channel"},
		{Name: "title", Description: "Chat title"},
		{Name: "username", Description: "Public username of the chat"},
		{Name: "is_forum", Description: "True when the chat has forum topics"},
		{Name: "description", Description: "Chat description"},
		{Name: "permissions", Description: "Default member permissions (booleans only)"},
		{Name: "slow_mode_delay", Description: "Seconds between messages of one member"},
		{Name: "pinned_message_id", Description: "Identifier of the pinned message; its content is never shown"},
		{Name: "linked_chat_id", Description: "Identifier of the linked chat; it never becomes a target"},
	},
	Examples: []capability.Example{{Description: "Read the chat of the connection", Arguments: json.RawMessage(`{}`)}},
}

var chatsAdministrators = capability.Descriptor{
	ID:          Provider + ".chats.administrators",
	Version:     1,
	Title:       "List Telegram chat administrators",
	Description: "Read the non-bot administrators and the creator of a bound chat with their rights",
	Tags:        []string{"telegram", "chats", "read"},
	Risk:        readRisk(memberDataSensitivity),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatArgument + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"administrators":{"type":"array","items":` +
		memberSchema + `},"truncated":{"type":"boolean"}},"required":["administrators"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgumentDoc},
	Fields: append([]capability.Field{
		{Name: "administrators", Description: "At most 200 administrators"},
		{Name: "truncated", Description: "True when Telegram returned more administrators than are shown"},
	}, memberFields...),
	Examples: []capability.Example{{Description: "List the administrators of the chat", Arguments: json.RawMessage(`{}`)}},
}

var chatsMemberCount = capability.Descriptor{
	ID:          Provider + ".chats.membercount",
	Version:     1,
	Title:       "Get the Telegram chat member count",
	Description: "Read the number of members of a bound chat",
	Tags:        []string{"telegram", "chats", "read"},
	Risk:        readRisk(dataSensitivity),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatArgument + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},` +
		`"required":["count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgumentDoc},
	Fields:    []capability.Field{{Name: "count", Description: "Number of members of the chat"}},
	Examples:  []capability.Example{{Description: "Count the members of the chat", Arguments: json.RawMessage(`{}`)}},
}

var chatsMember = capability.Descriptor{
	ID:          Provider + ".chats.member",
	Version:     1,
	Title:       "Get one Telegram chat member",
	Description: "Read the status and rights of one member of a bound chat",
	Tags:        []string{"telegram", "chats", "read"},
	Risk:        readRisk(memberDataSensitivity),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatArgument + `,` +
		`"user_id":{"type":"integer","minimum":1}},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(memberSchema),
	Arguments: []capability.Argument{
		chatArgumentDoc,
		{Name: "user_id", Description: "Telegram user identifier of the member", Required: true},
	},
	Fields:   memberFields,
	Examples: []capability.Example{{Description: "Read one member", Arguments: json.RawMessage(`{"user_id":42}`)}},
}

type permissionsOut struct {
	CanSendMessages       *bool `json:"can_send_messages,omitempty"`
	CanSendAudios         *bool `json:"can_send_audios,omitempty"`
	CanSendDocuments      *bool `json:"can_send_documents,omitempty"`
	CanSendPhotos         *bool `json:"can_send_photos,omitempty"`
	CanSendVideos         *bool `json:"can_send_videos,omitempty"`
	CanSendVideoNotes     *bool `json:"can_send_video_notes,omitempty"`
	CanSendVoiceNotes     *bool `json:"can_send_voice_notes,omitempty"`
	CanSendPolls          *bool `json:"can_send_polls,omitempty"`
	CanSendOtherMessages  *bool `json:"can_send_other_messages,omitempty"`
	CanAddWebPagePreviews *bool `json:"can_add_web_page_previews,omitempty"`
	CanChangeInfo         *bool `json:"can_change_info,omitempty"`
	CanInviteUsers        *bool `json:"can_invite_users,omitempty"`
	CanPinMessages        *bool `json:"can_pin_messages,omitempty"`
	CanManageTopics       *bool `json:"can_manage_topics,omitempty"`
}

type chatOut struct {
	ID              int64           `json:"id"`
	Type            string          `json:"type"`
	Title           string          `json:"title,omitempty"`
	Username        string          `json:"username,omitempty"`
	IsForum         bool            `json:"is_forum,omitempty"`
	Description     string          `json:"description,omitempty"`
	Permissions     *permissionsOut `json:"permissions,omitempty"`
	SlowModeDelay   int64           `json:"slow_mode_delay,omitempty"`
	PinnedMessageID int64           `json:"pinned_message_id,omitempty"`
	LinkedChatID    int64           `json:"linked_chat_id,omitempty"`
}

type memberUserOut struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// memberOut is the fixed member allow-list; Telegram fields outside it (language_code, is_premium, and the
// like) are dropped by decoding into this struct.
type memberOut struct {
	User        memberUserOut `json:"user"`
	Status      string        `json:"status"`
	CustomTitle string        `json:"custom_title,omitempty"`
	UntilDate   int64         `json:"until_date,omitempty"`
	IsAnonymous *bool         `json:"is_anonymous,omitempty"`
	IsMember    *bool         `json:"is_member,omitempty"`

	CanBeEdited         *bool `json:"can_be_edited,omitempty"`
	CanManageChat       *bool `json:"can_manage_chat,omitempty"`
	CanDeleteMessages   *bool `json:"can_delete_messages,omitempty"`
	CanManageVideoChats *bool `json:"can_manage_video_chats,omitempty"`
	CanRestrictMembers  *bool `json:"can_restrict_members,omitempty"`
	CanPromoteMembers   *bool `json:"can_promote_members,omitempty"`
	CanChangeInfo       *bool `json:"can_change_info,omitempty"`
	CanInviteUsers      *bool `json:"can_invite_users,omitempty"`
	CanPostStories      *bool `json:"can_post_stories,omitempty"`
	CanEditStories      *bool `json:"can_edit_stories,omitempty"`
	CanDeleteStories    *bool `json:"can_delete_stories,omitempty"`
	CanPostMessages     *bool `json:"can_post_messages,omitempty"`
	CanEditMessages     *bool `json:"can_edit_messages,omitempty"`
	CanPinMessages      *bool `json:"can_pin_messages,omitempty"`
	CanManageTopics     *bool `json:"can_manage_topics,omitempty"`

	CanSendMessages       *bool `json:"can_send_messages,omitempty"`
	CanSendAudios         *bool `json:"can_send_audios,omitempty"`
	CanSendDocuments      *bool `json:"can_send_documents,omitempty"`
	CanSendPhotos         *bool `json:"can_send_photos,omitempty"`
	CanSendVideos         *bool `json:"can_send_videos,omitempty"`
	CanSendVideoNotes     *bool `json:"can_send_video_notes,omitempty"`
	CanSendVoiceNotes     *bool `json:"can_send_voice_notes,omitempty"`
	CanSendPolls          *bool `json:"can_send_polls,omitempty"`
	CanSendOtherMessages  *bool `json:"can_send_other_messages,omitempty"`
	CanAddWebPagePreviews *bool `json:"can_add_web_page_previews,omitempty"`
}

func (m *memberOut) capped() {
	m.User.FirstName = capText(m.User.FirstName, maxNameRunes)
	m.User.LastName = capText(m.User.LastName, maxNameRunes)
	m.User.Username = capText(m.User.Username, maxNameRunes)
	m.Status = capText(m.Status, maxNameRunes)
	m.CustomTitle = capText(m.CustomTitle, maxNameRunes)
}

type administratorsOut struct {
	Administrators []memberOut `json:"administrators"`
	Truncated      bool        `json:"truncated,omitempty"`
}

type countOut struct {
	Count int64 `json:"count"`
}

func invokeChatsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	chat, err := chatOnlyArguments(raw, "get chat")
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, chat)
	if err != nil {
		return nil, err
	}
	return client.GetChat(ctx)
}

func invokeChatsAdministrators(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	chat, err := chatOnlyArguments(raw, "list administrators")
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, chat)
	if err != nil {
		return nil, err
	}
	return client.GetChatAdministrators(ctx)
}

func invokeChatsMemberCount(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	chat, err := chatOnlyArguments(raw, "count members")
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, chat)
	if err != nil {
		return nil, err
	}
	return client.GetChatMemberCount(ctx)
}

func invokeChatsMember(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat   string `json:"chat"`
		UserID int64  `json:"user_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("get member", "the validated arguments could not be read")
	}
	if arguments.UserID <= 0 {
		return nil, providerError("get member", "user_id must be a positive integer")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.GetChatMember(ctx, arguments.UserID)
}

func chatOnlyArguments(raw json.RawMessage, op string) (string, error) {
	var arguments struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return "", providerError(op, "the validated arguments could not be read")
	}
	return arguments.Chat, nil
}

type chatBody struct {
	ChatID string `json:"chat_id"`
}

// GetChat reads getChat of the selected chat; only the fields of chatOut are decoded. The invite link, the
// pinned message content, and every other field never leave this function.
func (c *Client) GetChat(ctx context.Context) (chatOut, error) {
	const op = "get chat"
	if c.target == "" {
		return chatOut{}, providerError(op, "no chat was selected")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getChat", limit: defaultResponseBytes, readOnly: true},
		chatBody{ChatID: c.target})
	if err != nil {
		return chatOut{}, err
	}
	var info struct {
		chatOut
		PinnedMessage *struct {
			MessageID int64 `json:"message_id"`
		} `json:"pinned_message"`
	}
	if json.Unmarshal(raw, &info) != nil || info.ID == 0 {
		return chatOut{}, invalidResponse(op)
	}
	out := info.chatOut
	if info.PinnedMessage != nil {
		out.PinnedMessageID = info.PinnedMessage.MessageID
	}
	out.Type = capText(out.Type, maxNameRunes)
	out.Title = capText(out.Title, maxNameRunes)
	out.Username = capText(out.Username, maxNameRunes)
	out.Description = capText(out.Description, maxMessageLength)
	return out, nil
}

// GetChatAdministrators reads getChatAdministrators of the selected chat.
func (c *Client) GetChatAdministrators(ctx context.Context) (administratorsOut, error) {
	const op = "list administrators"
	if c.target == "" {
		return administratorsOut{}, providerError(op, "no chat was selected")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getChatAdministrators", limit: administratorsBytes, readOnly: true},
		chatBody{ChatID: c.target})
	if err != nil {
		return administratorsOut{}, err
	}
	var members []memberOut
	if json.Unmarshal(raw, &members) != nil {
		return administratorsOut{}, invalidResponse(op)
	}
	out := administratorsOut{Administrators: []memberOut{}}
	for i := range members {
		if members[i].User.ID == 0 {
			return administratorsOut{}, invalidResponse(op)
		}
		if len(out.Administrators) == maxAdministrators {
			out.Truncated = true
			break
		}
		members[i].capped()
		out.Administrators = append(out.Administrators, members[i])
	}
	return out, nil
}

// GetChatMemberCount reads getChatMemberCount of the selected chat.
func (c *Client) GetChatMemberCount(ctx context.Context) (countOut, error) {
	const op = "count members"
	if c.target == "" {
		return countOut{}, providerError(op, "no chat was selected")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getChatMemberCount", limit: defaultResponseBytes, readOnly: true},
		chatBody{ChatID: c.target})
	if err != nil {
		return countOut{}, err
	}
	var count int64
	if json.Unmarshal(raw, &count) != nil || count < 0 {
		return countOut{}, invalidResponse(op)
	}
	return countOut{Count: count}, nil
}

// GetChatMember reads getChatMember of the selected chat for one user.
func (c *Client) GetChatMember(ctx context.Context, userID int64) (memberOut, error) {
	const op = "get member"
	if c.target == "" {
		return memberOut{}, providerError(op, "no chat was selected")
	}
	if userID <= 0 {
		return memberOut{}, providerError(op, "user_id must be a positive integer")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getChatMember", limit: defaultResponseBytes, readOnly: true},
		struct {
			ChatID string `json:"chat_id"`
			UserID int64  `json:"user_id"`
		}{c.target, userID})
	if err != nil {
		return memberOut{}, err
	}
	var member memberOut
	if json.Unmarshal(raw, &member) != nil || member.User.ID == 0 {
		return memberOut{}, invalidResponse(op)
	}
	member.capped()
	return member, nil
}
