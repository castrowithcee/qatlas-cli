package lexware

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// integrationSensitivity classifies webhook subscriptions: they reveal which external systems the
// organization has connected.
const integrationSensitivity = "lexware-integration-data"

const (
	resourceEventSubscription = "event subscription"

	maxEventSubscriptions = 200

	// maskedCallback replaces a callback URL that cannot be reduced to scheme and host.
	maskedCallback = "[masked]"

	eventSubscriptionMayBeDeleted = "; the subscription may have been deleted, check the subscription list before repeating it"
)

// There is deliberately no tool that creates a subscription: it would register a callback to a third party.
var integrationReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: integrationSensitivity,
}

// Repeating a deletion only reports the subscription as missing.
var eventSubscriptionDeleteRisk = capability.Risk{
	Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: integrationSensitivity,
}

const eventSubscriptionProperties = `"id":{"type":"string"},"created_date":{"type":"string"},` +
	`"event_type":{"type":"string"},"callback_target":{"type":"string"}`

var eventSubscriptionFields = []capability.Field{
	{Name: "id", Description: "Identifier of the subscription"},
	{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
	{Name: "event_type", Description: "Event type the subscription listens to (untrusted data)"},
	{Name: "callback_target", Description: "Scheme and host of the callback URL followed by /…; path, query, " +
		"fragment and credentials are never shown, an unreadable URL is shown as [masked]"},
}

var eventSubscriptionsList = capability.Descriptor{
	ID: Provider + ".event-subscriptions.list", Version: 1, Title: "List Lexware event subscriptions",
	Description: "List the webhook subscriptions of the Lexware Office organization, at most 200 entries; the " +
		"callback URL is shown only as scheme and host",
	Tags:     []string{"lexware", "eventsubscriptions", "webhooks", "list", "integration"},
	Provider: Provider, Risk: integrationReadRisk,
	InputSchema:  json.RawMessage(noArguments),
	OutputSchema: listSchema("subscriptions", eventSubscriptionProperties),
	Fields: []capability.Field{
		{Name: "subscriptions", Description: "Subscriptions: id, created_date, event_type, callback_target (untrusted data)"},
		{Name: "truncated", Description: truncatedField},
	},
	Examples: referenceExample("List the webhook subscriptions of the organization"),
}

var eventSubscriptionsGet = capability.Descriptor{
	ID: Provider + ".event-subscriptions.get", Version: 1, Title: "Get a Lexware event subscription",
	Description: "Read one webhook subscription of the Lexware Office organization by its identifier; the " +
		"callback URL is shown only as scheme and host",
	Tags:     []string{"lexware", "eventsubscriptions", "webhooks", "get", "integration"},
	Provider: Provider, Risk: integrationReadRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventSubscriptionProperties +
		`},"required":["id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "Subscription identifier as a UUID, as returned by lexware.event-subscriptions.list"}},
	Fields: eventSubscriptionFields,
	Examples: []capability.Example{{
		Description: "Read one subscription",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

var eventSubscriptionsDelete = capability.Descriptor{
	ID: Provider + ".event-subscriptions.delete", Version: 1, Title: "Delete a Lexware event subscription",
	Description: "Permanently delete one webhook subscription of the Lexware Office organization; it cannot be " +
		"restored. The subscription may serve another application's integration, which stops receiving events. " +
		"Offered only by a connection whose tools list names it",
	Tags:     []string{"lexware", "eventsubscriptions", "webhooks", "delete", "integration"},
	Provider: Provider, Risk: eventSubscriptionDeleteRisk, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "Subscription identifier as a UUID, as returned by lexware.event-subscriptions.list"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted subscription"},
		{Name: "deleted", Description: "Always true: Lexware confirmed the deletion"},
	},
	Examples: []capability.Example{{
		Description: "Delete one subscription",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

// EventSubscription is one webhook subscription with a masked callback target.
type EventSubscription struct {
	ID             string `json:"id"`
	CreatedDate    string `json:"created_date,omitempty"`
	EventType      string `json:"event_type,omitempty"`
	CallbackTarget string `json:"callback_target"`
}

type eventSubscriptionJSON struct {
	ID          string `json:"subscriptionId"`
	CreatedDate string `json:"createdDate"`
	EventType   string `json:"eventType"`
	CallbackURL string `json:"callbackUrl"`
}

func (r eventSubscriptionJSON) normalize() EventSubscription {
	return EventSubscription{ID: r.ID, CreatedDate: r.CreatedDate, EventType: r.EventType,
		CallbackTarget: maskCallback(r.CallbackURL)}
}

// maskCallback reduces a callback URL to scheme and host. The path often carries a secret token, so
// everything else is dropped, including the port; anything but a plain http(s) URL is masked completely.
func maskCallback(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return maskedCallback
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, " \t\r\n/\\@?#") {
		return maskedCallback
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host + "/…"
}

// ListEventSubscriptions reads the subscriptions, capped at maxEventSubscriptions. An entry without a
// usable identifier makes the whole answer unusable rather than being silently dropped.
func (c *Client) ListEventSubscriptions(ctx context.Context) ([]EventSubscription, bool, error) {
	const op = "list event subscriptions"
	var page struct {
		Content []eventSubscriptionJSON `json:"content"`
	}
	if err := c.get(ctx, op, "", "/v1/event-subscriptions", nil, &page); err != nil {
		return nil, false, err
	}
	out := make([]EventSubscription, 0, min(len(page.Content), maxEventSubscriptions))
	truncated := false
	for _, entry := range page.Content {
		if !validUUID(entry.ID) {
			return nil, false, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned an event subscription without a usable identifier",
			}
		}
		if len(out) == maxEventSubscriptions {
			truncated = true
			continue
		}
		out = append(out, entry.normalize())
	}
	return out, truncated, nil
}

// GetEventSubscription reads exactly the subscription of a validated identifier.
func (c *Client) GetEventSubscription(ctx context.Context, id string) (*EventSubscription, error) {
	const op = "get event subscription"
	if !validUUID(id) {
		return nil, providerError(op, "the event subscription identifier must be a UUID")
	}
	var raw eventSubscriptionJSON
	if err := c.get(ctx, op, resourceEventSubscription, "/v1/event-subscriptions/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different event subscription than the requested one",
		}
	}
	subscription := raw.normalize()
	return &subscription, nil
}

type eventSubscriptionDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// DeleteEventSubscription sends exactly one DELETE, never repeated.
func (c *Client) DeleteEventSubscription(ctx context.Context, id string) (*eventSubscriptionDeleted, error) {
	const op = "delete event subscription"
	if !validUUID(id) {
		return nil, providerError(op, "the event subscription identifier must be a UUID")
	}
	if err := c.delete(ctx, op, resourceEventSubscription, "/v1/event-subscriptions/"+url.PathEscape(id),
		eventSubscriptionMayBeDeleted); err != nil {
		return nil, err
	}
	return &eventSubscriptionDeleted{ID: id, Deleted: true}, nil
}

func invokeEventSubscriptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	items, truncated, err := client.ListEventSubscriptions(ctx)
	return listResult("subscriptions", items, truncated, err)
}

// eventSubscriptionHandler reads the identifier first so that an invalid one causes no secret access.
func eventSubscriptionHandler(op string, run func(context.Context, *Client, string) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var arguments struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
		if !validUUID(arguments.ID) {
			return nil, providerError(op, "the event subscription identifier must be a UUID")
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return run(ctx, client, arguments.ID)
	}
}

var (
	invokeEventSubscriptionsGet = eventSubscriptionHandler("get event subscription",
		func(ctx context.Context, c *Client, id string) (any, error) { return c.GetEventSubscription(ctx, id) })
	invokeEventSubscriptionsDelete = eventSubscriptionHandler("delete event subscription",
		func(ctx context.Context, c *Client, id string) (any, error) {
			return c.DeleteEventSubscription(ctx, id)
		})
)
