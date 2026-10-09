package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const metaDeleteNote = "Deletes for good, and for every user and integration of the workspace at once. Needs the " +
	"Twenty permission \"Data model\" and a connection without object targets, and is offered only by a " +
	"connection whose tools list names it. Only custom, non-relational targets that are deactivated first " +
	"(update with is_active false) are accepted; standard and system targets and relation fields are refused. " +
	"Sent once; after an unclear result read before repeating"

const metaDeleteResult = `{"type":"object","properties":{"id":` + uuidSchema +
	`,"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`

var metaObjectsDelete = capability.Descriptor{
	ID: Provider + ".metaobjects.delete", Version: 1, Title: "Delete a Twenty CRM custom object",
	Description: "Permanently delete one custom object of the Twenty workspace of a connection together with all " +
		"of its records and fields; the loss is final and cannot be undone. " + metaDeleteNote,
	Tags:     []string{"twentycrm", "metadata", "schema", "objects", "delete"},
	Risk:     metaWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(metaDeleteResult),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the deactivated custom object, as returned by twentycrm.metaobjects.list"},
	},
	Examples: []capability.Example{{Description: "Delete a custom object",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

var metaFieldsDelete = capability.Descriptor{
	ID: Provider + ".metafields.delete", Version: 1, Title: "Delete a Twenty CRM custom field",
	Description: "Permanently delete one custom non-relational field of the Twenty workspace of a connection " +
		"together with its values in all records; the loss is final and cannot be undone. " + metaDeleteNote,
	Tags:     []string{"twentycrm", "metadata", "schema", "fields", "delete"},
	Risk:     metaWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(metaDeleteResult),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the deactivated custom field, as returned by twentycrm.metaobjects.get"},
	},
	Examples: []capability.Example{{Description: "Delete a custom field",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// MetaDeleted is the result of a deletion.
type MetaDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func newMetaDelete(args *metaWriteArgs) (*metaWriteChange, error) {
	if !validUUID(args.ID) || args.ObjectID != "" || args.Type != "" || args.NameSingular != nil ||
		args.NamePlural != nil || args.Name != nil || args.LabelSingular != nil || args.LabelPlural != nil ||
		args.Label != nil || args.Description != nil || args.IsActive != nil || args.IsNullable != nil ||
		args.Options != nil {
		return nil, invalidRequest(errMetaArguments)
	}
	return &metaWriteChange{id: args.ID}, nil
}

// metaDeleteAnswer accepts the deleted entity only when it names the requested id or carries none.
func metaDeleteAnswer(op, uncertain string, body json.RawMessage, key, id string) (*MetaDeleted, error) {
	entity, err := unwrapMeta(op, body, key)
	var record struct {
		ID *string `json:"id"`
	}
	if err == nil {
		err = json.Unmarshal(entity, &record)
	}
	if err != nil || (record.ID != nil && !equalUUID(*record.ID, id)) {
		return nil, uncertainly(provider.InvalidResponse(op, "Twenty answered in an unusable way"), uncertain)
	}
	return &MetaDeleted{ID: id, Deleted: true}, nil
}

func (c *Client) sendMetaDelete(ctx context.Context, op, uncertain, path, key, id string) (*MetaDeleted, error) {
	var body json.RawMessage
	if err := c.changeWith(ctx, op, uncertain, http.MethodDelete, path, nil, &body); err != nil {
		return nil, mapMetaPermission(err)
	}
	if bytes.TrimSpace(body) == nil {
		return nil, uncertainly(provider.InvalidResponse(op, "Twenty answered in an unusable way"), uncertain)
	}
	return metaDeleteAnswer(op, uncertain, body, key, id)
}

// DeleteMetaObject reads the object once, then sends exactly one DELETE.
func (c *Client) DeleteMetaObject(ctx context.Context, change *metaWriteChange) (*MetaDeleted, error) {
	const op = "delete object"
	path := metadataObjectsPath + "/" + url.PathEscape(change.id)
	var current metaObjectRecord
	if err := c.metaRead(ctx, op, path, "object", &current); err != nil {
		return nil, err
	}
	if !equalUUID(current.ID, change.id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different object than the requested one")
	}
	if !isTrue(current.IsCustom) || isTrue(current.IsSystem) {
		return nil, invalidRequest("only custom objects can be deleted through Qatlas")
	}
	if current.IsActive == nil || *current.IsActive {
		return nil, invalidRequest("deactivate the object first with twentycrm.metaobjects.update and is_active false")
	}
	return c.sendMetaDelete(ctx, op, metaObjectUncertain, path, "deleteOneObject", change.id)
}

// DeleteMetaField reads the field once, then sends exactly one DELETE.
func (c *Client) DeleteMetaField(ctx context.Context, change *metaWriteChange) (*MetaDeleted, error) {
	const op = "delete field"
	path := metadataFieldsPath + "/" + url.PathEscape(change.id)
	var current metaFieldRecord
	if err := c.metaRead(ctx, op, path, "field", &current); err != nil {
		return nil, err
	}
	if !equalUUID(current.ID, change.id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different field than the requested one")
	}
	if !isTrue(current.IsCustom) || isTrue(current.IsSystem) || !contains(metaFieldTypes, current.Type) ||
		current.Type == "RELATION" || current.Type == "MORPH_RELATION" {
		return nil, invalidRequest("only custom non-relational fields can be deleted through Qatlas")
	}
	if current.IsActive == nil || *current.IsActive {
		return nil, invalidRequest("deactivate the field first with twentycrm.metafields.update and is_active false")
	}
	return c.sendMetaDelete(ctx, op, metaFieldUncertain, path, "deleteOneField", change.id)
}

func invokeMetaObjectsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMetaWrite(ctx, resolved, secrets, red, raw, newMetaDelete, (*Client).DeleteMetaObject)
}

func invokeMetaFieldsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMetaWrite(ctx, resolved, secrets, red, raw, newMetaDelete, (*Client).DeleteMetaField)
}
