package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxChecklistTitle = 255
	maxChecklistTasks = 30
	maxChecklistText  = 100

	// The id uniqueness is checked locally; the schema cannot express it per property.
	checklistSchema = `"checklist":{"type":"object","properties":{` +
		`"title":{"type":"string","minLength":1,"maxLength":255},` +
		`"tasks":{"type":"array","minItems":1,"maxItems":30,"items":{"type":"object","properties":{` +
		`"id":{"type":"integer","minimum":1},"text":{"type":"string","minLength":1,"maxLength":100}},` +
		`"required":["id","text"],"additionalProperties":false}},` +
		`"others_can_add_tasks":{"type":"boolean"},"others_can_mark_tasks_as_done":{"type":"boolean"}},` +
		`"required":["title","tasks"],"additionalProperties":false}`
	checklistChatSchema = `"chat":{"type":"string","minLength":1,"maxLength":2048}`
)

var checklistArguments = []capability.Argument{
	{Name: "business", Description: businessArgument, Required: true},
	{Name: "chat", Description: "business_chat_ref of the same business connection from telegram.updates.list or telegram.business.get", Required: true},
	{Name: "checklist", Description: "Checklist with a title of 1 through 255 characters, 1 through 30 tasks of distinct positive id " +
		"and 1 through 100 characters of text, and the optional booleans others_can_add_tasks and others_can_mark_tasks_as_done", Required: true},
}

var checklistOutputSchema = json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},` +
	`"date":{"type":"integer"}},"required":["message_id"],"additionalProperties":false}`)

var checklistsSend = capability.Descriptor{
	ID:          Provider + ".checklists.send",
	Version:     1,
	Title:       "Send a Telegram checklist as a business account",
	Description: "Send one checklist into a chat of a bound business connection",
	Tags:        []string{"telegram", "business", "checklist", "create"},
	Risk: capability.Risk{
		Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + checklistChatSchema + `,` +
		checklistSchema + `,"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}},` +
		`"required":["business","chat","checklist"],"additionalProperties":false}`),
	OutputSchema: checklistOutputSchema,
	Arguments: append(append([]capability.Argument{}, checklistArguments...),
		capability.Argument{Name: "disable_notification", Description: "Send silently"},
		capability.Argument{Name: "protect_content", Description: "Forbid forwarding and saving of the message"}),
	Fields: []capability.Field{
		{Name: "message_id", Description: "Identifier of the sent message"},
		{Name: "date", Description: "Unix time the message was sent"},
	},
	Examples: []capability.Example{{
		Description: "Send a checklist",
		Arguments: json.RawMessage(`{"business":"<connection id>","chat":"<business_chat_ref>",` +
			`"checklist":{"title":"Packing","tasks":[{"id":1,"text":"Passport"},{"id":2,"text":"Charger"}]}}`),
	}},
}

var checklistsEdit = capability.Descriptor{
	ID:          Provider + ".checklists.edit",
	Version:     1,
	Title:       "Edit a Telegram checklist as a business account",
	Description: "Replace one checklist message of a chat of a bound business connection",
	Tags:        []string{"telegram", "business", "checklist", "update"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + checklistChatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1},` + checklistSchema + `},` +
		`"required":["business","chat","message_id","checklist"],"additionalProperties":false}`),
	OutputSchema: checklistOutputSchema,
	Arguments: append(append([]capability.Argument{}, checklistArguments...),
		capability.Argument{Name: "message_id", Description: "Positive identifier of the checklist message in that chat", Required: true}),
	Fields: []capability.Field{
		{Name: "message_id", Description: "Identifier of the edited message"},
		{Name: "date", Description: "Unix time the message was originally sent, when Telegram reports it"},
	},
	Examples: []capability.Example{{
		Description: "Replace a checklist",
		Arguments: json.RawMessage(`{"business":"<connection id>","chat":"<business_chat_ref>","message_id":91,` +
			`"checklist":{"title":"Packing","tasks":[{"id":1,"text":"Passport"}]}}`),
	}},
}

type checklistTask struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type checklistInput struct {
	Title                    string          `json:"title"`
	Tasks                    []checklistTask `json:"tasks"`
	OthersCanAddTasks        bool            `json:"others_can_add_tasks,omitempty"`
	OthersCanMarkTasksAsDone bool            `json:"others_can_mark_tasks_as_done,omitempty"`
}

func (l checklistInput) validate(op string) error {
	if n := utf8.RuneCountInString(l.Title); n < 1 || n > maxChecklistTitle {
		return providerError(op, "the checklist title must have from 1 through 255 characters")
	}
	if len(l.Tasks) < 1 || len(l.Tasks) > maxChecklistTasks {
		return providerError(op, "the checklist needs from 1 through 30 tasks")
	}
	seen := make(map[int64]struct{}, len(l.Tasks))
	for _, task := range l.Tasks {
		if task.ID < 1 {
			return providerError(op, "the task ids must be positive")
		}
		if _, dup := seen[task.ID]; dup {
			return providerError(op, "the task ids must be distinct")
		}
		seen[task.ID] = struct{}{}
		if n := utf8.RuneCountInString(task.Text); n < 1 || n > maxChecklistText {
			return providerError(op, "each task text must have from 1 through 100 characters")
		}
	}
	return nil
}

type checklistArgs struct {
	Business            string         `json:"business"`
	Chat                string         `json:"chat"`
	MessageID           int64          `json:"message_id"`
	Checklist           checklistInput `json:"checklist"`
	DisableNotification bool           `json:"disable_notification"`
	ProtectContent      bool           `json:"protect_content"`
}

// prepareChecklist runs every check that needs no credential: arguments, scope, and reference format and binding.
func prepareChecklist(op string, resolved *config.Resolved, raw json.RawMessage, edit bool) (checklistArgs, parsedRef, error) {
	var a checklistArgs
	if err := decodeStrict(raw, &a); err != nil {
		return a, parsedRef{}, providerError(op, "the validated arguments could not be read")
	}
	if edit && a.MessageID < 1 {
		return a, parsedRef{}, providerError(op, "message_id must be positive")
	}
	if !edit && a.MessageID != 0 {
		return a, parsedRef{}, providerError(op, "the validated arguments could not be read")
	}
	if err := a.Checklist.validate(op); err != nil {
		return a, parsedRef{}, err
	}
	if err := scopeBusiness(op, resolved, a.Business); err != nil {
		return a, parsedRef{}, err
	}
	parsed, err := parseRef(resolved, refBusinessChat, a.Chat)
	if err != nil {
		return a, parsedRef{}, err
	}
	if parsed.binding != businessBinding(a.Business) {
		return a, parsedRef{}, providerError(op, "the reference does not belong to this business connection")
	}
	return a, parsed, nil
}

func invokeChecklistsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeChecklistsSendWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeChecklistsSendWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	a, parsed, err := prepareChecklist("send business checklist", resolved, raw, false)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.SendChecklist(ctx, a.Business, parsed, a.Checklist, a.DisableNotification, a.ProtectContent)
}

func invokeChecklistsEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeChecklistsEditWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeChecklistsEditWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	a, parsed, err := prepareChecklist("edit business checklist", resolved, raw, true)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.EditChecklist(ctx, a.Business, parsed, a.MessageID, a.Checklist)
}

// businessChatID verifies the reference's signature and returns its numeric chat.
func (c *Client) businessChatID(op, method, id string, parsed parsedRef) (int64, error) {
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return 0, providerError(op, "this connection does not bind that business connection")
	}
	if parsed.kind != refBusinessChat || parsed.binding != businessBinding(id) {
		return 0, providerError(op, "the reference does not belong to this business connection")
	}
	value, err := c.checkRef(parsed)
	if err != nil {
		return 0, err
	}
	chat, err := strconv.ParseInt(value, 10, 64)
	if err != nil || chat == 0 {
		return 0, providerError(op, "the reference is malformed")
	}
	if err := requireNumericChat(method, value); err != nil {
		return 0, err
	}
	return chat, nil
}

// SendChecklist performs exactly one sendChecklist request without retrying an ambiguous result.
func (c *Client) SendChecklist(ctx context.Context, id string, parsed parsedRef, l checklistInput,
	silent, protect bool) (map[string]any, error) {
	const op = "send business checklist"
	if err := l.validate(op); err != nil {
		return nil, err
	}
	chat, err := c.businessChatID(op, "sendChecklist", id, parsed)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "sendChecklist", limit: defaultResponseBytes}, struct {
		ID                  string         `json:"business_connection_id"`
		ChatID              int64          `json:"chat_id"`
		Checklist           checklistInput `json:"checklist"`
		DisableNotification bool           `json:"disable_notification,omitempty"`
		ProtectContent      bool           `json:"protect_content,omitempty"`
	}{id, chat, l, silent, protect})
	if err != nil {
		return nil, err
	}
	var result struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID <= 0 || result.Date <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"message_id": result.MessageID, "date": result.Date}, nil
}

// EditChecklist performs exactly one editMessageChecklist request without retrying an ambiguous result.
func (c *Client) EditChecklist(ctx context.Context, id string, parsed parsedRef, messageID int64,
	l checklistInput) (map[string]any, error) {
	const op = "edit business checklist"
	if messageID < 1 {
		return nil, providerError(op, "message_id must be positive")
	}
	if err := l.validate(op); err != nil {
		return nil, err
	}
	chat, err := c.businessChatID(op, "editMessageChecklist", id, parsed)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "editMessageChecklist", limit: defaultResponseBytes}, struct {
		ID        string         `json:"business_connection_id"`
		ChatID    int64          `json:"chat_id"`
		MessageID int64          `json:"message_id"`
		Checklist checklistInput `json:"checklist"`
	}{id, chat, messageID, l})
	if err != nil {
		return nil, err
	}
	var result struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID != messageID {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	out := map[string]any{"message_id": result.MessageID}
	if result.Date > 0 {
		out["date"] = result.Date
	}
	return out, nil
}
