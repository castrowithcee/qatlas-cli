package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	viewFiltersPath = "/rest/metadata/viewFilters"
	viewGroupsPath  = "/rest/metadata/viewGroups"

	viewPartDeleteUncertain = "; this deletion may have taken effect, read the view with twenty-crm.views.get " +
		"before repeating it"

	viewPartDeleteResult = `{"type":"object","properties":{"id":{"type":"string"},"kind":{"type":"string"},` +
		`"view_id":{"type":"string"},"field_id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","kind","view_id","deleted"],"additionalProperties":false}`
)

// viewPartDeleteKind describes one deletable part of a view layout. Everything the tools differ in is here.
type viewPartDeleteKind struct {
	kind, path string
	texts      provider.StatusTexts
	descriptor *capability.Descriptor
}

func viewPartTexts(noun string) provider.StatusTexts {
	return provider.StatusTexts{
		Subject:    "Twenty",
		Auth:       "Twenty rejected the API key",
		Permission: "the workspace role of this API key may not read views; check the role of the API key in Twenty",
		NotFound:   "this Twenty workspace does not hold this view " + noun,
	}
}

func viewPartDeleteDescriptor(id, title, noun, part, extra string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + id + ".delete", Version: 1, Title: title,
		Description: "Permanently delete " + part + " of a custom view of the Twenty workspace of a connection; the " +
			"loss is final and it cannot be restored. The change applies to all members of the workspace. " + extra +
			"The view of the " + noun + " must be a custom view of the whole workspace: standard views, index views, " +
			"personal views, and widget views are refused, and the last two read like an unknown " + noun + ". " +
			viewPartWriteNote,
		Tags:     []string{"twentycrm", "views", noun + "s", "delete"},
		Risk:     viewWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		Provider: Provider, RequiresToolAllowList: true,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(viewPartDeleteResult),
		Arguments: []capability.Argument{
			{Name: "id", Required: true, Description: "UUID of the " + noun + ", as returned by twenty-crm.views.get"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "The deleted " + noun},
			{Name: "kind", Description: "column, sort, filter, or group"},
			{Name: "view_id", Description: "The view that held it"},
			{Name: "field_id", Description: "The field it was bound to, when it had one"},
		},
		Examples: []capability.Example{{Description: "Delete a " + noun,
			Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174002"}`)}},
	}
}

var (
	viewfieldsDelete = viewPartDeleteDescriptor("view-fields", "Delete a column of a Twenty CRM view", "column",
		"one column", "")
	viewsortsDelete = viewPartDeleteDescriptor("view-sorts", "Delete a sort of a Twenty CRM view", "sort",
		"one sort", "")
	viewfiltersDelete = viewPartDeleteDescriptor("view-filters", "Delete a filter of a Twenty CRM view", "filter",
		"one filter", "")
	viewgroupsDelete = viewPartDeleteDescriptor("view-groups", "Delete a group of a Twenty CRM view", "group",
		"one group", "")

	viewPartDeleteKinds = []viewPartDeleteKind{
		{kind: "column", path: viewFieldsPath, texts: viewColumnStatusTexts, descriptor: &viewfieldsDelete},
		{kind: "sort", path: viewSortsPath, texts: viewSortStatusTexts, descriptor: &viewsortsDelete},
		{kind: "filter", path: viewFiltersPath, texts: viewPartTexts("filter"), descriptor: &viewfiltersDelete},
		{kind: "group", path: viewGroupsPath, texts: viewPartTexts("group"), descriptor: &viewgroupsDelete},
	}
)

// ViewPartDeleted names the part of a view that was deleted, as read before the deletion.
type ViewPartDeleted struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	ViewID  string `json:"view_id"`
	FieldID string `json:"field_id,omitempty"`
	Deleted bool   `json:"deleted"`
}

// DeleteViewPart reads the part and its view, then sends exactly one DELETE. The view of the part, not one
// named by the caller, decides whether it may go. Filter values and group values are never decoded.
func (c *Client) DeleteViewPart(ctx context.Context, part viewPartDeleteKind, id string) (*ViewPartDeleted, error) {
	op := "delete view " + part.kind
	path := part.path + "/" + url.PathEscape(id)
	var current json.RawMessage
	if err := c.getWith(ctx, op, path, nil, viewResponseBytes, &current, part.texts); err != nil {
		return nil, err
	}
	var wire struct {
		ID              string  `json:"id"`
		ViewID          string  `json:"viewId"`
		FieldMetadataID *string `json:"fieldMetadataId"`
		DeletedAt       *string `json:"deletedAt"`
	}
	unusable := provider.InvalidResponse(op, "Twenty returned an unusable view "+part.kind)
	if json.Unmarshal(current, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) ||
		(wire.FieldMetadataID != nil && !validUUID(*wire.FieldMetadataID)) {
		return nil, unusable
	}
	if !equalUUID(wire.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view "+part.kind+" than the requested one")
	}
	if wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, part.texts)
	}
	if _, err := c.fetchChangeableView(ctx, op, wire.ViewID, part.texts); err != nil {
		return nil, err
	}
	if err := c.destroyChild(ctx, op, viewPartDeleteUncertain, path, id); err != nil {
		return nil, err
	}
	result := &ViewPartDeleted{ID: strings.ToLower(wire.ID), Kind: part.kind, ViewID: strings.ToLower(wire.ViewID), Deleted: true}
	if wire.FieldMetadataID != nil {
		result.FieldID = strings.ToLower(*wire.FieldMetadataID)
	}
	return result, nil
}

func (part viewPartDeleteKind) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || !validUUID(args.ID) {
		return nil, invalidRequest("the " + part.kind + " id must be a UUID and the only argument")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DeleteViewPart(ctx, part, strings.ToLower(args.ID))
}
