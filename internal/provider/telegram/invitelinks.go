package telegram

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupInviteLinks = "invitelinks"
	// inviteLinkSensitivity classifies results and requests that carry a link granting chat membership.
	inviteLinkSensitivity = "telegram-invite-link"
	maxInviteLinkRunes    = 128
)

// inviteLinkPattern admits only the two private invite link forms on the exact host t.me: no user, port,
// query, or fragment, and a narrow token alphabet.
var inviteLinkPattern = regexp.MustCompile(`^https://t\.me/(?:\+|joinchat/)[A-Za-z0-9_-]{8,64}$`)

var invitelinksPrimary = capability.Descriptor{
	ID:          Provider + ".invitelinks.primary",
	Version:     1,
	Title:       "Read the primary Telegram invite link",
	Description: "Read the primary invite link of a bound chat; the link grants joining the chat",
	Tags:        []string{"telegram", "invitelinks", "read"},
	Risk:        readRisk(inviteLinkSensitivity),
	Provider:    Provider,
	Group:       groupInviteLinks,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"invite_link":{"type":"string"}},` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument},
	Fields:    []capability.Field{{Name: "invite_link", Description: "Primary invite link; absent when the chat has none"}},
	Examples:  []capability.Example{{Description: "Read the primary invite link of the chat", Arguments: json.RawMessage(`{}`)}},
}

var invitelinksRevoke = capability.Descriptor{
	ID:      Provider + ".invitelinks.revoke",
	Version: 1,
	Title:   "Revoke a Telegram invite link",
	Description: "Revoke one invite link of a bound chat; for a revoked primary link Telegram generates a new one, " +
		"which the result never contains",
	Tags: []string{"telegram", "invitelinks", "revoke"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: inviteLinkSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupInviteLinks,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"invite_link":{"type":"string","minLength":1,"maxLength":128}},` +
		`"required":["invite_link"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"revoked":{"type":"boolean"}},` +
		`"required":["revoked"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "invite_link", Description: "Link of the form https://t.me/+TOKEN or https://t.me/joinchat/TOKEN", Required: true},
	},
	Fields: []capability.Field{{Name: "revoked", Description: "True when Telegram accepted the revocation"}},
	Examples: []capability.Example{{
		Description: "Revoke an invite link of this connection's chat",
		Arguments:   json.RawMessage(`{"invite_link":"https://t.me/+AbCdEfGh1234"}`),
	}},
}

type inviteLinkOut struct {
	InviteLink string `json:"invite_link,omitempty"`
}

func invokeInviteLinksPrimary(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	chat, err := chatOnlyArguments(raw, "read primary invite link")
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, chat)
	if err != nil {
		return nil, err
	}
	return client.GetPrimaryInviteLink(ctx)
}

func invokeInviteLinksRevoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat       string `json:"chat"`
		InviteLink string `json:"invite_link"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("revoke invite link", "the validated arguments could not be read")
	}
	if !validInviteLink(arguments.InviteLink) {
		return nil, providerError("revoke invite link", "the invite link must have the form https://t.me/+TOKEN or https://t.me/joinchat/TOKEN")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.RevokeInviteLink(ctx, arguments.InviteLink)
}

func validInviteLink(link string) bool {
	return len(link) <= maxInviteLinkRunes && inviteLinkPattern.MatchString(link)
}

// GetPrimaryInviteLink reads getChat of the selected chat and returns only its invite_link field.
func (c *Client) GetPrimaryInviteLink(ctx context.Context) (inviteLinkOut, error) {
	const op = "read primary invite link"
	raw, err := c.getChatRaw(ctx, op)
	if err != nil {
		return inviteLinkOut{}, err
	}
	var info inviteLinkOut
	if json.Unmarshal(raw, &info) != nil {
		return inviteLinkOut{}, invalidResponse(op)
	}
	// A value outside the accepted link forms is provider text, not a link; it is dropped, never shown.
	if !validInviteLink(info.InviteLink) {
		info.InviteLink = ""
	}
	return info, nil
}

// RevokeInviteLink performs exactly one revokeChatInviteLink request without retrying an ambiguous result.
// The response carries the revoked link, so only Telegram's acceptance is evaluated.
func (c *Client) RevokeInviteLink(ctx context.Context, link string) (map[string]any, error) {
	const op = "revoke invite link"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if !validInviteLink(link) {
		return nil, providerError(op, "the invite link must have the form https://t.me/+TOKEN or https://t.me/joinchat/TOKEN")
	}
	if _, err := c.call(ctx, spec{op: op, method: "revokeChatInviteLink", limit: defaultResponseBytes}, struct {
		ChatID     string `json:"chat_id"`
		InviteLink string `json:"invite_link"`
	}{ChatID: c.target, InviteLink: link}); err != nil {
		return nil, err
	}
	return map[string]any{"revoked": true}, nil
}
