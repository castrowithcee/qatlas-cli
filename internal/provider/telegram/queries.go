package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupQueries = "queries"
	// maxCallbackAnswerRunes is the Bot API limit for the text of answerCallbackQuery.
	maxCallbackAnswerRunes = 200

	joinResultDecline = "decline"
	joinResultQueue   = "queue"
)

func queryRisk() capability.Risk {
	return capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var callbacksAnswer = capability.Descriptor{
	ID:      Provider + ".callbacks.answer",
	Version: 1,
	Title:   "Answer a Telegram callback query",
	Description: "Answer the button press behind one callback_ref of telegram.updates.list with an optional " +
		"notification or alert; Telegram expects the answer shortly after the press, and a late " +
		"answer is refused",
	Tags:     []string{"telegram", "callbacks", "answer"},
	Risk:     queryRisk(),
	Provider: Provider,
	Group:    groupQueries,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"callback_ref":{"type":"string","minLength":1,` +
		`"maxLength":2048},"text":{"type":"string","maxLength":200},"show_alert":{"type":"boolean"}},` +
		`"required":["callback_ref"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"answered":{"type":"boolean"}},` +
		`"required":["answered"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "callback_ref", Description: "callback_ref of an update from telegram.updates.list", Required: true},
		{Name: "text", Description: "Notification text, at most 200 characters; without it the press is only acknowledged"},
		{Name: "show_alert", Description: "Show the text as an alert the user must dismiss instead of a notification"},
	},
	Fields: []capability.Field{{Name: "answered", Description: "True when Telegram accepted the answer"}},
	Examples: []capability.Example{{
		Description: "Acknowledge a button press with a notification",
		Arguments:   json.RawMessage(`{"callback_ref":"<callback_ref>","text":"Done"}`),
	}},
}

var joinrequestqueriesAnswer = capability.Descriptor{
	ID:      Provider + ".join-request-queries.answer",
	Version: 1,
	Title:   "Answer a Telegram join request query",
	Description: "Decline a pending join request query of a bound chat or leave the decision to the other " +
		"administrators, using the join_request_ref of telegram.updates.list; no tool accepts a join request, and " +
		"Telegram expects the answer within 10 seconds of the request",
	Tags:     []string{"telegram", "joinrequestqueries", "answer"},
	Risk:     queryRisk(),
	Provider: Provider,
	Group:    groupQueries,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"join_request_ref":{"type":"string","minLength":1,` +
		`"maxLength":2048},"result":{"type":"string","enum":["decline","queue"]}},` +
		`"required":["join_request_ref","result"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"answered":{"type":"boolean"}},` +
		`"required":["answered"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "join_request_ref", Description: "join_request_ref of a chat_join_request update from telegram.updates.list", Required: true},
		{Name: "result", Description: "decline to refuse the user, or queue to leave the decision to other administrators", Required: true},
	},
	Fields: []capability.Field{{Name: "answered", Description: "True when Telegram accepted the answer"}},
	Examples: []capability.Example{{
		Description: "Leave a join request to the other administrators",
		Arguments:   json.RawMessage(`{"join_request_ref":"<join_request_ref>","result":"queue"}`),
	}},
}

func invokeCallbacksAnswer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeCallbacksAnswerWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeCallbacksAnswerWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "answer callback query"
	var arguments struct {
		CallbackRef string `json:"callback_ref"`
		Text        string `json:"text"`
		ShowAlert   bool   `json:"show_alert"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if utf8.RuneCountInString(arguments.Text) > maxCallbackAnswerRunes || !utf8.ValidString(arguments.Text) {
		return nil, providerError(op, "text is longer than 200 characters or not valid UTF-8")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	// Format, kind, and binding are checked before the credential is resolved.
	parsed, err := parseRef(resolved, refCallback, arguments.CallbackRef)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.AnswerCallback(ctx, parsed, arguments.Text, arguments.ShowAlert)
}

// AnswerCallback verifies the reference's signature, then performs exactly one answerCallbackQuery request.
func (c *Client) AnswerCallback(ctx context.Context, parsed parsedRef, text string, showAlert bool) (map[string]any, error) {
	const op = "answer callback query"
	if utf8.RuneCountInString(text) > maxCallbackAnswerRunes || !utf8.ValidString(text) {
		return nil, providerError(op, "text is longer than 200 characters or not valid UTF-8")
	}
	id, err := c.checkRef(parsed)
	if err != nil {
		return nil, err
	}
	err = c.confirmed(ctx, spec{op: op, method: "answerCallbackQuery", limit: defaultResponseBytes}, struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
		ShowAlert       bool   `json:"show_alert,omitempty"`
	}{id, text, showAlert})
	if err != nil {
		return nil, err
	}
	return map[string]any{"answered": true}, nil
}

func invokeJoinRequestQueriesAnswer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeJoinRequestQueriesAnswerWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeJoinRequestQueriesAnswerWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "answer join request query"
	var arguments struct {
		JoinRequestRef string `json:"join_request_ref"`
		Result         string `json:"result"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkJoinResult(op, arguments.Result); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	parsed, err := parseRef(resolved, refJoinRequestQuery, arguments.JoinRequestRef)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.AnswerJoinRequestQuery(ctx, parsed, arguments.Result)
}

// checkJoinResult is the only gate for the result: approve and every other value never reach a request.
func checkJoinResult(op, result string) error {
	if result != joinResultDecline && result != joinResultQueue {
		return providerError(op, "result must be decline or queue; a join request is never approved")
	}
	return nil
}

// AnswerJoinRequestQuery verifies the reference's signature, then performs exactly one
// answerChatJoinRequestQuery request.
func (c *Client) AnswerJoinRequestQuery(ctx context.Context, parsed parsedRef, result string) (map[string]any, error) {
	const op = "answer join request query"
	if err := checkJoinResult(op, result); err != nil {
		return nil, err
	}
	id, err := c.checkRef(parsed)
	if err != nil {
		return nil, err
	}
	err = c.confirmed(ctx, spec{op: op, method: "answerChatJoinRequestQuery", limit: defaultResponseBytes}, struct {
		QueryID string `json:"chat_join_request_query_id"`
		Result  string `json:"result"`
	}{id, result})
	if err != nil {
		return nil, err
	}
	return map[string]any{"answered": true}, nil
}
