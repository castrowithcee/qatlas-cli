// Package telegram implements the deliberately small Telegram Bot API surface used by Qatlas: a safe
// getMe connection check and confirmed plain-text send, edit, and delete operations.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	Provider         = "telegram"
	roleBotToken     = "bot-token"
	defaultURL       = "https://api.telegram.org"
	defaultTimeout   = 30 * time.Second
	maxMessageLength = 4096
	maxResponseBytes = 64 << 10
	dataSensitivity  = "telegram-message-content"
)

var messagesSend = capability.Descriptor{
	ID:          Provider + ".messages.send",
	Version:     1,
	Title:       "Send a Telegram message",
	Description: "Send one plain-text message to the fixed target of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "send"},
	Risk: capability.Risk{
		Effect:          capability.EffectCreate,
		Idempotency:     capability.IdempotencyNonIdempotent,
		Confirmation:    capability.ConfirmationRequired,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(
		`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096}},"required":["text"],"additionalProperties":false}`,
	),
	OutputSchema: json.RawMessage(
		`{"type":"object","properties":{"message_id":{"type":"integer"},"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`,
	),
	Arguments: []capability.Argument{{
		Name: "text", Description: "Plain-text message, from 1 through 4096 characters", Required: true,
	}},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Telegram message identifier"},
		{Name: "date", Description: "Telegram send time as Unix time"},
	},
	Examples: []capability.Example{{
		Description: "Send one plain-text notification to the connection's fixed target",
		Arguments:   json.RawMessage(`{"text":"Deployment finished"}`),
	}},
}

var messagesEdit = capability.Descriptor{
	ID:          Provider + ".messages.edit",
	Version:     1,
	Title:       "Edit a Telegram message",
	Description: "Replace the plain text of one message in the fixed target of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "edit"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"message_id":{"type":"integer","minimum":1},` +
		`"text":{"type":"string","minLength":1,"maxLength":4096}},` +
		`"required":["message_id","text"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"}},` +
		`"required":["message_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "message_id", Description: "Identifier of the message in the fixed target", Required: true},
		{Name: "text", Description: "Replacement plain text, from 1 through 4096 characters", Required: true},
	},
	Fields: []capability.Field{{Name: "message_id", Description: "Edited Telegram message identifier"}},
	Examples: []capability.Example{{
		Description: "Replace the text of a message sent to this connection's target",
		Arguments:   json.RawMessage(`{"message_id":91,"text":"Deployment completed"}`),
	}},
}

var messagesDelete = capability.Descriptor{
	ID:          Provider + ".messages.delete",
	Version:     1,
	Title:       "Delete a Telegram message",
	Description: "Delete one message from the fixed target of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer","minimum":1}},` +
		`"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{
		Name: "message_id", Description: "Identifier of the message in the fixed target", Required: true,
	}},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the deletion"}},
	Examples: []capability.Example{{
		Description: "Delete a message from this connection's target",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

// Register adds Telegram metadata, its read-only connection test, and the message operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Telegram", DefaultBaseURL: defaultURL,
		DefaultPermissions: []config.Permission{config.PermissionCreate},
		SecretRoles: []config.SecretRole{{
			Name:        roleBotToken,
			Description: "Telegram bot token issued by BotFather",
		}},
		Target: config.TargetMetadata{
			Label: "chat ID", Description: "fixed Telegram chat ID or @channel username", Required: true,
		},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: messagesSend, Handler: capability.Handler(invokeMessagesSend)},
		capability.Operation{Descriptor: messagesEdit, Handler: capability.Handler(invokeMessagesEdit)},
		capability.Operation{Descriptor: messagesDelete, Handler: capability.Handler(invokeMessagesDelete)},
	)
}

func invokeMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, &provider.Error{
			Class: provider.ClassProviderError, Op: "send message", Message: "the validated arguments could not be read",
		}
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, arguments.Text)
}

func invokeMessagesEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("edit message", "the validated arguments could not be read")
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.EditMessage(ctx, arguments.MessageID, arguments.Text)
}

func invokeMessagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("delete message", "the validated arguments could not be read")
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessage(ctx, arguments.MessageID)
}

// Client binds one bot token to the fixed target of one resolved connection.
type Client struct {
	base   *url.URL
	token  string
	target string
	http   *http.Client
}

// Open resolves the bot token only after the application core selected and confirmed the exact request.
func Open(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return openWithHTTP(resolved, secrets, red, newHTTPClient())
}

func openWithHTTP(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	httpClient *http.Client) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	base, err := url.Parse(resolved.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" {
		return nil, providerError("open", "the Telegram base URL must be a plain HTTPS URL")
	}
	if err := validateTarget(resolved.Target); err != nil {
		return nil, providerError("open", "the configured Telegram target is unusable")
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(resolved.Credential, resolved.Secrets, roleBotToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the bot token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "bot"+value.Secret)
	}
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	return &Client{base: base, token: value.Secret, target: resolved.Target, http: httpClient}, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultTimeout,
		// The bot token is part of Telegram's required URL path. Refusing every redirect guarantees that
		// it is never replayed to a second URL, even on the configured origin.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestConnection calls getMe, Telegram's read-only authentication check. It never sends to Target.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	return client.testConnection(ctx), nil
}

func (c *Client) testConnection(ctx context.Context) provider.Class {
	response, err := c.do(ctx, "test connection", http.MethodGet, "getMe", nil)
	if err != nil {
		return errorClass(err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return errorClass(statusError("test connection", response.StatusCode))
	}
	body, err := readResponse(response.Body)
	if err != nil {
		return provider.ClassInvalidResponse
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(body, &result) != nil || !result.OK {
		return provider.ClassInvalidResponse
	}
	return provider.ClassOK
}

// SendMessage performs exactly one sendMessage request. The target comes only from the Client's resolved
// connection and no retry is attempted after any transport or provider result.
func (c *Client) SendMessage(ctx context.Context, text string) (map[string]any, error) {
	if count := utf8.RuneCountInString(text); !utf8.ValidString(text) || count < 1 || count > maxMessageLength {
		return nil, providerError("send message", "the plain-text message is outside the supported length")
	}
	body, err := json.Marshal(struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}{ChatID: c.target, Text: text})
	if err != nil {
		return nil, providerError("send message", "the request could not be encoded")
	}
	response, err := c.do(ctx, "send message", http.MethodPost, "sendMessage", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, statusError("send message", response.StatusCode)
	}
	responseBody, err := readResponse(response.Body)
	if err != nil {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: "send message",
			Message: "Telegram returned an invalid response",
		}
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
			Date      int64 `json:"date"`
		} `json:"result"`
	}
	if json.Unmarshal(responseBody, &result) != nil || !result.OK ||
		result.Result.MessageID <= 0 || result.Result.Date <= 0 {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: "send message",
			Message: "Telegram returned an invalid response",
		}
	}
	return map[string]any{"message_id": result.Result.MessageID, "date": result.Result.Date}, nil
}

// EditMessage replaces one message's plain text in the fixed target without retrying an ambiguous result.
func (c *Client) EditMessage(ctx context.Context, messageID int64, text string) (map[string]any, error) {
	if messageID < 1 {
		return nil, providerError("edit message", "the message identifier must be positive")
	}
	if count := utf8.RuneCountInString(text); !utf8.ValidString(text) || count < 1 || count > maxMessageLength {
		return nil, providerError("edit message", "the plain-text message is outside the supported length")
	}
	body, err := json.Marshal(struct {
		ChatID    string `json:"chat_id"`
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
	}{ChatID: c.target, MessageID: messageID, Text: text})
	if err != nil {
		return nil, providerError("edit message", "the request could not be encoded")
	}
	responseBody, err := c.mutate(ctx, "edit message", "editMessageText", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if json.Unmarshal(responseBody, &result) != nil || !result.OK || result.Result.MessageID != messageID {
		return nil, invalidResponse("edit message")
	}
	return map[string]any{"message_id": result.Result.MessageID}, nil
}

// DeleteMessage removes one message from the fixed target without retrying an ambiguous result.
func (c *Client) DeleteMessage(ctx context.Context, messageID int64) (map[string]any, error) {
	if messageID < 1 {
		return nil, providerError("delete message", "the message identifier must be positive")
	}
	body, err := json.Marshal(struct {
		ChatID    string `json:"chat_id"`
		MessageID int64  `json:"message_id"`
	}{ChatID: c.target, MessageID: messageID})
	if err != nil {
		return nil, providerError("delete message", "the request could not be encoded")
	}
	responseBody, err := c.mutate(ctx, "delete message", "deleteMessage", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		OK     bool `json:"ok"`
		Result bool `json:"result"`
	}
	if json.Unmarshal(responseBody, &result) != nil || !result.OK || !result.Result {
		return nil, invalidResponse("delete message")
	}
	return map[string]any{"deleted": true}, nil
}

func (c *Client) mutate(ctx context.Context, op, method string, body []byte) ([]byte, error) {
	response, err := c.do(ctx, op, http.MethodPost, method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, statusError(op, response.StatusCode)
	}
	responseBody, err := readResponse(response.Body)
	if err != nil {
		return nil, invalidResponse(op)
	}
	return responseBody, nil
}

func invalidResponse(op string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Telegram returned an invalid response"}
}

func (c *Client) do(ctx context.Context, op, method, apiMethod string, body io.Reader) (*http.Response, error) {
	target := *c.base
	target.Path = strings.TrimRight(target.Path, "/") + "/bot" + c.token + "/" + apiMethod
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(op, err)
	}
	return response, nil
}

func readResponse(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	value, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(value) > maxResponseBytes {
		return nil, errors.New("response could not be read within the limit")
	}
	return value, nil
}

func statusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Telegram rejected the bot token"}
	case http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "Telegram refused this operation"}
	case http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Telegram rate-limited the operation"}
	default:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Telegram rejected the operation (HTTP %d)", status),
		}
	}
}

// transportError classifies a failure that happened before a status code existed. The shared classifier
// owns the rules, so Telegram publishes the same class and the same transport cause as every other
// provider, and the original error text is never copied.
func transportError(op string, err error) error {
	return provider.Transport(op, "Telegram", err)
}

func errorClass(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return provider.ClassProviderError
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func validToken(value string) bool {
	if len(value) < 3 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == ':' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validateTarget(target string) error {
	if target == "" || strings.TrimSpace(target) != target || len(target) > 128 {
		return errors.New("target is empty, padded, or too long")
	}
	if strings.HasPrefix(target, "@") {
		if len(target) < 2 {
			return errors.New("username is empty")
		}
		for _, r := range target[1:] {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				continue
			}
			return errors.New("username has unsupported characters")
		}
		return nil
	}
	value, err := strconv.ParseInt(target, 10, 64)
	if err != nil || value == 0 {
		return errors.New("target is neither a chat ID nor an @username")
	}
	return nil
}
