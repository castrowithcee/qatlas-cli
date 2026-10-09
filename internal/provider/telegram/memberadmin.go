package telegram

import (
	"context"
	"encoding/json"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxMemberLabel = 16

// promoteRightNames lists every boolean of promoteChatMember; promote requires all of them.
var promoteRightNames = []string{
	"is_anonymous", "can_manage_chat", "can_delete_messages", "can_manage_video_chats", "can_restrict_members",
	"can_promote_members", "can_change_info", "can_invite_users", "can_post_stories", "can_edit_stories",
	"can_delete_stories", "can_post_messages", "can_edit_messages", "can_pin_messages", "can_manage_topics",
	"can_manage_direct_messages", "can_manage_tags", "can_send_welcome_messages",
}

func promoteRightsSchema() string {
	props, required := "", ""
	for i, name := range promoteRightNames {
		if i > 0 {
			props += ","
			required += ","
		}
		props += `"` + name + `":{"type":"boolean"}`
		required += `"` + name + `"`
	}
	return `"rights":{"type":"object","properties":{` + props + `},"required":[` + required +
		`],"additionalProperties":false}`
}

func demoteExample() string {
	out := `{"user_id":42,"rights":{`
	for i, name := range promoteRightNames {
		if i > 0 {
			out += ","
		}
		out += `"` + name + `":false`
	}
	return out + `}}`
}

var membersPromote = capability.Descriptor{
	ID:      Provider + ".members.promote",
	Version: 1,
	Title:   "Promote or demote a Telegram chat administrator",
	Description: "Set the complete administrator rights of one existing member in a bound supergroup or channel " +
		"of an explicit Telegram connection; every right must be given explicitly, all false demotes. " +
		"can_promote_members and can_invite_users extend who may add administrators and members",
	Tags:     []string{"telegram", "members", "promote"},
	Risk:     memberRisk(capability.EffectUpdate),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		promoteRightsSchema() + `},"required":["user_id","rights"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"promoted":{"type":"boolean"}},` +
		`"required":["promoted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument, userIDArgument,
		{Name: "rights", Description: "Object with every promoteChatMember boolean of the Bot API, none omitted", Required: true},
	},
	Fields: []capability.Field{{Name: "promoted", Description: "True when Telegram accepted the rights"}},
	Examples: []capability.Example{{
		Description: "Demote a user: all rights false",
		Arguments:   json.RawMessage(demoteExample()),
	}},
}

var membersSetAdminTitle = capability.Descriptor{
	ID:      Provider + ".members.setadmintitle",
	Version: 1,
	Title:   "Set a Telegram administrator title",
	Description: "Set the custom title of an administrator the bot promoted in a bound supergroup of an explicit " +
		"Telegram connection; 0 to 16 characters without emoji, empty removes it",
	Tags:     []string{"telegram", "members", "title"},
	Risk:     memberRisk(capability.EffectUpdate),
	Provider: Provider,
	Group:    groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		`"title":{"type":"string","maxLength":16}},"required":["user_id","title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument, userIDArgument,
		{Name: "title", Description: "New title, 0 to 16 characters, no emoji", Required: true},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the title"}},
	Examples: []capability.Example{{
		Description: "Give an administrator a title",
		Arguments:   json.RawMessage(`{"user_id":42,"title":"Moderator"}`),
	}},
}

var membersSetTag = capability.Descriptor{
	ID:      Provider + ".members.settag",
	Version: 1,
	Title:   "Set a Telegram member tag",
	Description: "Set the tag of a regular member in a bound group or supergroup of an explicit Telegram " +
		"connection; 0 to 16 characters without emoji, empty removes it",
	Tags:     []string{"telegram", "members", "tag"},
	Risk:     memberRisk(capability.EffectUpdate),
	Provider: Provider,
	Group:    groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		`"tag":{"type":"string","maxLength":16}},"required":["user_id","tag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument, userIDArgument,
		{Name: "tag", Description: "New tag, 0 to 16 characters, no emoji", Required: true},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the tag"}},
	Examples: []capability.Example{{
		Description: "Tag a member",
		Arguments:   json.RawMessage(`{"user_id":42,"tag":"Helper"}`),
	}},
}

// checkLabel counts Unicode characters, not bytes. Emoji detection is deliberately conservative: symbols,
// format and joiner characters, variation selectors, and the emoji blocks are all refused.
func checkLabel(op, field, value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxMemberLabel {
		return providerError(op, field+" must be at most 16 characters")
	}
	for _, r := range value {
		if unicode.In(r, unicode.So, unicode.Cf, unicode.Cc, unicode.Cs, unicode.Co, unicode.Mn, unicode.Me) ||
			(r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2190 && r <= 0x21FF) ||
			(r >= 0x2300 && r <= 0x23FF) || (r >= 0x2B00 && r <= 0x2BFF) || (r >= 0xFE00 && r <= 0xFE0F) ||
			r == 0x00A9 || r == 0x00AE || r == 0x203C || r == 0x2049 || r == 0x2122 || r == 0x3030 ||
			r == 0x303D || r == 0x3297 || r == 0x3299 || r == 0x20E3 {
			return providerError(op, field+" must not contain emoji or control characters")
		}
	}
	return nil
}

func invokeMembersPromote(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat   string           `json:"chat"`
		UserID int64            `json:"user_id"`
		Rights map[string]*bool `json:"rights"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("promote member", "the validated arguments could not be read")
	}
	if err := checkMember("promote member", arguments.UserID, 0); err != nil {
		return nil, err
	}
	rights, ok := completeRights(arguments.Rights)
	if !ok {
		return nil, providerError("promote member", "rights must set every right explicitly")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.PromoteMember(ctx, arguments.UserID, rights)
}

// completeRights accepts exactly the known rights, each as an explicit boolean.
func completeRights(in map[string]*bool) (map[string]bool, bool) {
	if len(in) != len(promoteRightNames) {
		return nil, false
	}
	out := make(map[string]bool, len(in))
	for _, name := range promoteRightNames {
		v, ok := in[name]
		if !ok || v == nil {
			return nil, false
		}
		out[name] = *v
	}
	return out, true
}

func invokeMembersSetAdminTitle(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat   string  `json:"chat"`
		UserID int64   `json:"user_id"`
		Title  *string `json:"title"`
	}
	const op = "set administrator title"
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.Title == nil {
		return nil, providerError(op, "title is required")
	}
	if err := checkMember(op, arguments.UserID, 0); err != nil {
		return nil, err
	}
	if err := checkLabel(op, "title", *arguments.Title); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetAdminTitle(ctx, arguments.UserID, *arguments.Title)
}

func invokeMembersSetTag(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat   string  `json:"chat"`
		UserID int64   `json:"user_id"`
		Tag    *string `json:"tag"`
	}
	const op = "set member tag"
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.Tag == nil {
		return nil, providerError(op, "tag is required")
	}
	if err := checkMember(op, arguments.UserID, 0); err != nil {
		return nil, err
	}
	if err := checkLabel(op, "tag", *arguments.Tag); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetMemberTag(ctx, arguments.UserID, *arguments.Tag)
}

// PromoteMember performs exactly one promoteChatMember request with every right explicit.
func (c *Client) PromoteMember(ctx context.Context, userID int64, rights map[string]bool) (map[string]any, error) {
	const op = "promote member"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, 0); err != nil {
		return nil, err
	}
	if len(rights) != len(promoteRightNames) {
		return nil, providerError(op, "rights must set every right explicitly")
	}
	body := map[string]any{"chat_id": c.target, "user_id": userID}
	for _, name := range promoteRightNames {
		v, ok := rights[name]
		if !ok {
			return nil, providerError(op, "rights must set every right explicitly")
		}
		body[name] = v
	}
	if err := c.confirmed(ctx, spec{op: op, method: "promoteChatMember", limit: defaultResponseBytes}, body); err != nil {
		return nil, err
	}
	return map[string]any{"promoted": true}, nil
}

// SetAdminTitle performs exactly one setChatAdministratorCustomTitle request.
func (c *Client) SetAdminTitle(ctx context.Context, userID int64, title string) (map[string]any, error) {
	const op = "set administrator title"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, 0); err != nil {
		return nil, err
	}
	if err := checkLabel(op, "title", title); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatAdministratorCustomTitle", limit: defaultResponseBytes}, struct {
		ChatID      string `json:"chat_id"`
		UserID      int64  `json:"user_id"`
		CustomTitle string `json:"custom_title"`
	}{c.target, userID, title})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// SetMemberTag performs exactly one setChatMemberTag request; an empty tag is sent to remove the tag.
func (c *Client) SetMemberTag(ctx context.Context, userID int64, tag string) (map[string]any, error) {
	const op = "set member tag"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, 0); err != nil {
		return nil, err
	}
	if err := checkLabel(op, "tag", tag); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatMemberTag", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
		UserID int64  `json:"user_id"`
		Tag    string `json:"tag"`
	}{c.target, userID, tag})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}
