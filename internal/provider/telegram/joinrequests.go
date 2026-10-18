package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var joinrequestsDecline = capability.Descriptor{
	ID:      Provider + ".join-requests.decline",
	Version: 1,
	Title:   "Decline a Telegram join request",
	Description: "Decline the pending join request of one user for a bound chat of an explicit Telegram connection; " +
		"no tool accepts a join request, and a user without a pending request is left untouched",
	Tags:     []string{"telegram", "joinrequests", "decline"},
	Risk:     memberRisk(capability.EffectUpdate),
	Provider: Provider,
	Group:    groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `},` +
		`"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"declined":{"type":"boolean"}},` +
		`"required":["declined"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, {
		Name: "user_id", Description: "Telegram user identifier of the user who asked to join", Required: true,
	}},
	Fields: []capability.Field{{Name: "declined", Description: "True when Telegram accepted the decision"}},
	Examples: []capability.Example{{
		Description: "Decline the join request of a user for this connection's chat",
		Arguments:   json.RawMessage(`{"user_id":42}`),
	}},
}

func invokeJoinRequestsDecline(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "decline join request"
	var arguments struct {
		Chat   string `json:"chat"`
		UserID int64  `json:"user_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkMember(op, arguments.UserID, 0); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.DeclineJoinRequest(ctx, arguments.UserID)
}

// DeclineJoinRequest performs exactly one declineChatJoinRequest request.
func (c *Client) DeclineJoinRequest(ctx context.Context, userID int64) (map[string]any, error) {
	const op = "decline join request"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkMember(op, userID, 0); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "declineChatJoinRequest", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
		UserID int64  `json:"user_id"`
	}{c.target, userID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"declined": true}, nil
}
