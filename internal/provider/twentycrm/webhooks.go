package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The webhook tools are workspace-wide and read or change only the event selection and description of a
// webhook. The signing secret and the target URL are never part of an output or of a request body.
const (
	webhooksGroup = "webhooks"
	// webhookDataSensitivity classifies webhook configuration: where the workspace reports events to.
	webhookDataSensitivity = "twentycrm-webhook-config"

	webhooksPath        = "/rest/webhooks"
	webhooksListMax     = 100
	webhookOperationMax = 50
	webhookDescMax      = 500
	webhookTargetMax    = 2048
	webhookUnknown      = "unknown"

	errWebhookID   = "id must be a webhook identifier in UUID form"
	errWebhookDesc = "description must be 1 to 500 characters without control characters"
	errWebhookOps  = "operations must hold 1 to 50 events of the form <object>.<created|updated|deleted|destroyed|" +
		"restored|upserted>, or *.*, with the camelCase singular name of an object"

	webhookUncertain = "; this change may have taken effect, read the webhook with twentycrm.webhooks.get before repeating it"

	errWebhookPermission = "the workspace role of this API key may not manage webhooks; check the role in Twenty, " +
		"the right is called API keys and webhooks"
)

// webhookOperationPattern is the only accepted event form. Further wildcard forms are not accepted because
// they are not part of the verified Twenty contract.
var webhookOperationPattern = regexp.MustCompile(`^([a-z][A-Za-z0-9]{0,63}\.(created|updated|deleted|destroyed|restored|upserted)|\*\.\*)$`)

const webhookOperationSchema = `{"type":"string","minLength":3,"maxLength":80,` +
	`"pattern":"^([a-z][A-Za-z0-9]{0,63}\\.(created|updated|deleted|destroyed|restored|upserted)|\\*\\.\\*)$"}`

const webhookDescSchema = `{"type":"string","minLength":1,"maxLength":500}`

const webhookSchema = `{"type":"object","properties":{"id":{"type":"string"},"target":{"type":"string"},` +
	`"operations":{"type":"array","items":{"type":"string"}},"description":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","target","operations"],"additionalProperties":false}`

const webhookIDOnlyInput = `{"type":"object","properties":{"id":` + recordIDSchema + `},"required":["id"],"additionalProperties":false}`

var webhookIDArgument = capability.Argument{Name: "id", Description: "Webhook identifier as a UUID, as returned by twentycrm.webhooks.list", Required: true}

var webhookFields = []capability.Field{
	{Name: "id", Description: "Webhook identifier"},
	{Name: "target", Description: "Where the workspace reports events: scheme, host with port, and path only; query, fragment, and credentials are never shown, untrusted data"},
	{Name: "operations", Description: "Selected events as <object>.<event> or *.*; a value of another form reads unknown"},
	{Name: "description", Description: "Description of the webhook, untrusted data"},
	{Name: "created_at", Description: "Creation timestamp"},
	{Name: "updated_at", Description: "Last change timestamp"},
}

const webhookNote = "The signing secret is never shown. Needs the Twenty settings right API keys and webhooks and a " +
	"connection without object targets"

func webhookRisk(effect capability.Effect, confirmation capability.Confirmation, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: confirmation, OpenWorld: true,
		DataSensitivity: webhookDataSensitivity}
}

var webhooksList = capability.Descriptor{
	ID: Provider + ".webhooks.list", Version: 1, Title: "List Twenty CRM webhooks",
	Description: "List the webhooks of the Twenty workspace of a connection: where the workspace reports events and " +
		"which events. At most 100 webhooks; truncated is true when there are more. " + webhookNote,
	Tags:        []string{"twentycrm", "crm", "webhooks", "list"},
	Risk:        webhookRisk(capability.EffectRead, capability.ConfirmationNone, capability.IdempotencySafe),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"webhooks":{"type":"array","items":` + webhookSchema +
		`},"truncated":{"type":"boolean"}},"required":["webhooks","truncated"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "webhooks", Description: "The webhooks with id, target, operations, description, and timestamps, untrusted data"},
		{Name: "truncated", Description: "True when the workspace holds more webhooks than listed"},
	},
	Examples: []capability.Example{{Description: "List the webhooks", Arguments: json.RawMessage(`{}`)}},
}

var webhooksGet = capability.Descriptor{
	ID: Provider + ".webhooks.get", Version: 1, Title: "Get a Twenty CRM webhook",
	Description: "Read one webhook of the Twenty workspace of a connection. " + webhookNote,
	Tags:        []string{"twentycrm", "crm", "webhooks", "get"},
	Risk:        webhookRisk(capability.EffectRead, capability.ConfirmationNone, capability.IdempotencySafe),
	Provider:    Provider,
	InputSchema: json.RawMessage(webhookIDOnlyInput), OutputSchema: json.RawMessage(webhookSchema),
	Arguments: []capability.Argument{webhookIDArgument}, Fields: webhookFields,
	Examples: []capability.Example{{Description: "Read a webhook",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`)}},
}

var webhooksUpdate = capability.Descriptor{
	ID: Provider + ".webhooks.update", Version: 1, Title: "Update a Twenty CRM webhook",
	Description: "Replace the event selection and/or the description of one webhook of the Twenty workspace of a " +
		"connection. operations replaces the whole list (1 to 50 entries, duplicates are removed keeping the first); " +
		"the target and the signing secret cannot be changed. " + webhookNote,
	Tags:     []string{"twentycrm", "crm", "webhooks", "update"},
	Risk:     webhookRisk(capability.EffectUpdate, capability.ConfirmationRequired, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + recordIDSchema + `,"operations":{"type":"array",` +
		`"minItems":1,"maxItems":50,"items":` + webhookOperationSchema + `},"description":` + webhookDescSchema +
		`},"required":["id"],"anyOf":[{"required":["operations"]},{"required":["description"]}],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(webhookSchema),
	Arguments: []capability.Argument{webhookIDArgument,
		{Name: "operations", Description: "Complete new event list, each <object>.<created|updated|deleted|destroyed|restored|upserted> or *.*"},
		{Name: "description", Description: "New description, 1 to 500 characters without control characters"}},
	Fields: webhookFields,
	Examples: []capability.Example{{Description: "Report only person events",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","operations":["person.created","person.updated"]}`)}},
}

var webhooksDelete = capability.Descriptor{
	ID: Provider + ".webhooks.delete", Version: 1, Title: "Delete a Twenty CRM webhook",
	Description: "Delete one webhook of the Twenty workspace of a connection; the workspace stops reporting events " +
		"to it. " + webhookNote + ". A connection offers this tool only when its tools list names it",
	Tags:     []string{"twentycrm", "crm", "webhooks", "delete"},
	Risk:     webhookRisk(capability.EffectDelete, capability.ConfirmationRequired, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(webhookIDOnlyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{webhookIDArgument},
	Examples: []capability.Example{{Description: "Delete a webhook",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`)}},
}

// Webhook is the stable view of one webhook. It has no field for the signing secret.
type Webhook struct {
	ID          string   `json:"id"`
	Target      string   `json:"target"`
	Operations  []string `json:"operations"`
	Description string   `json:"description,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// WebhookList is the result of ListWebhooks.
type WebhookList struct {
	Webhooks  []Webhook `json:"webhooks"`
	Truncated bool      `json:"truncated"`
}

// webhookWire is the positive projection of Twenty's webhook object: fields not named here, such as the
// signing secret, are never decoded.
type webhookWire struct {
	ID          string   `json:"id"`
	TargetURL   string   `json:"targetUrl"`
	Operations  []string `json:"operations"`
	Description *string  `json:"description"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// webhookPatch is the whole body of an update: only the event selection and the description.
type webhookPatch struct {
	Operations  []string `json:"operations,omitempty"`
	Description *string  `json:"description,omitempty"`
}

func validWebhookOperations(ops []string) ([]string, bool) {
	if len(ops) < 1 || len(ops) > webhookOperationMax {
		return nil, false
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(ops))
	for _, op := range ops {
		if !webhookOperationPattern.MatchString(op) {
			return nil, false
		}
		if !seen[op] {
			seen[op] = true
			clean = append(clean, op)
		}
	}
	return clean, true
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return true
		}
	}
	return false
}

// webhookTarget keeps scheme, host with port, and path of an http(s) target; anything else reads unknown.
func webhookTarget(raw string) string {
	if len(raw) > webhookTargetMax || hasControl(raw) {
		return webhookUnknown
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.Hostname() == "" {
		return webhookUnknown
	}
	out := u.Scheme + "://" + u.Host + u.EscapedPath()
	if len(out) > webhookTargetMax || strings.ContainsAny(out, " \t") {
		return webhookUnknown
	}
	return out
}

func capWebhookText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, value)
	if utf8.RuneCountInString(value) > webhookDescMax {
		value = string([]rune(value)[:webhookDescMax])
	}
	return value
}

func webhookTime(value string) string {
	if len(value) > 40 {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return ""
	}
	return value
}

func (w *webhookWire) view(op string) (Webhook, error) {
	if !validUUID(w.ID) || len(w.Operations) > 200 {
		return Webhook{}, provider.InvalidResponse(op, "Twenty returned an unusable webhook")
	}
	view := Webhook{ID: w.ID, Target: webhookTarget(w.TargetURL), Operations: make([]string, 0, len(w.Operations)),
		CreatedAt: webhookTime(w.CreatedAt), UpdatedAt: webhookTime(w.UpdatedAt)}
	for _, operation := range w.Operations {
		if webhookOperationPattern.MatchString(operation) {
			view.Operations = append(view.Operations, operation)
		} else {
			view.Operations = append(view.Operations, webhookUnknown)
		}
	}
	if w.Description != nil {
		view.Description = capWebhookText(*w.Description)
	}
	return view, nil
}

func parseWebhook(op string, raw json.RawMessage) (Webhook, error) {
	var wire webhookWire
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "null" ||
		json.Unmarshal(raw, &wire) != nil {
		return Webhook{}, provider.InvalidResponse(op, "Twenty returned an unusable webhook")
	}
	return wire.view(op)
}

// webhookPermission names the settings right on a permission failure.
func webhookPermission(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = errWebhookPermission
	}
	return err
}

func webhookNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "this Twenty workspace does not hold this webhook"}
}

// getWebhookBody performs one bounded read. Twenty answers an unknown webhook with an empty body or null.
func (c *Client) getWebhookBody(ctx context.Context, op, path string) (json.RawMessage, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Twenty", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Twenty", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, webhookPermission(c.responseError(op, response))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, provider.InvalidResponse(op, "the Twenty response could not be read within the size limit")
	}
	return data, nil
}

// ListWebhooks sends one read and returns at most webhooksListMax webhooks.
func (c *Client) ListWebhooks(ctx context.Context) (*WebhookList, error) {
	const op = "list webhooks"
	data, err := c.getWebhookBody(ctx, op, webhooksPath)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(data, &items) != nil || items == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable list of webhooks")
	}
	result := &WebhookList{Webhooks: make([]Webhook, 0, min(len(items), webhooksListMax))}
	for i, item := range items {
		if i >= webhooksListMax {
			result.Truncated = true
			break
		}
		view, err := parseWebhook(op, item)
		if err != nil {
			return nil, err
		}
		result.Webhooks = append(result.Webhooks, view)
	}
	return result, nil
}

// GetWebhook sends one read; the answer must name the requested webhook.
func (c *Client) GetWebhook(ctx context.Context, id string) (*Webhook, error) {
	const op = "get webhook"
	data, err := c.getWebhookBody(ctx, op, webhooksPath+"/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	if trimmed := strings.TrimSpace(string(data)); trimmed == "" || trimmed == "null" {
		return nil, webhookNotFound(op)
	}
	view, err := parseWebhook(op, data)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(view.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different webhook than the requested one")
	}
	return &view, nil
}

// UpdateWebhook sends exactly one PATCH whose body is a webhookPatch.
func (c *Client) UpdateWebhook(ctx context.Context, id string, patch webhookPatch) (*Webhook, error) {
	const op = "update webhook"
	var raw json.RawMessage
	if err := c.changeWith(ctx, op, webhookUncertain, http.MethodPatch, webhooksPath+"/"+url.PathEscape(id), patch, &raw); err != nil {
		return nil, webhookPermission(err)
	}
	view, err := parseWebhook(op, raw)
	if err == nil && !strings.EqualFold(view.ID, id) {
		err = provider.InvalidResponse(op, "Twenty answered with a different webhook than the requested one")
	}
	if err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) {
			failure.Message += webhookUncertain
		}
		return nil, err
	}
	return &view, nil
}

// DeleteWebhook sends exactly one DELETE; Twenty answers true.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	const op = "delete webhook"
	var deleted bool
	if err := c.changeWith(ctx, op, webhookUncertain, http.MethodDelete, webhooksPath+"/"+url.PathEscape(id), nil, &deleted); err != nil {
		return webhookPermission(err)
	}
	if !deleted {
		return provider.InvalidResponse(op, "Twenty did not confirm the deletion"+webhookUncertain)
	}
	return nil
}

// openWorkspace gates a workspace-wide tool before any secret is resolved and opens the client.
func openWorkspace(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	return Open(ctx, resolved, secrets, red)
}

func webhookID(raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return "", providerError("webhook", "the validated arguments could not be read")
	}
	if !validUUID(args.ID) {
		return "", invalidRequest(errWebhookID)
	}
	return args.ID, nil
}

func invokeWebhooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openWorkspace(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListWebhooks(ctx)
}

func invokeWebhooksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	id, err := webhookID(raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetWebhook(ctx, id)
}

func invokeWebhooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	id, err := webhookID(raw)
	if err != nil {
		return nil, err
	}
	var args struct {
		Operations  []string `json:"operations"`
		Description *string  `json:"description"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("update webhook", "the validated arguments could not be read")
	}
	var patch webhookPatch
	if args.Operations == nil && args.Description == nil {
		return nil, invalidRequest("operations or description is required")
	}
	if args.Operations != nil {
		ops, ok := validWebhookOperations(args.Operations)
		if !ok {
			return nil, invalidRequest(errWebhookOps)
		}
		patch.Operations = ops
	}
	if args.Description != nil {
		d := *args.Description
		if d == "" || utf8.RuneCountInString(d) > webhookDescMax || !utf8.ValidString(d) || hasControl(d) {
			return nil, invalidRequest(errWebhookDesc)
		}
		patch.Description = &d
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateWebhook(ctx, id, patch)
}

func invokeWebhooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	id, err := webhookID(raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteWebhook(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
