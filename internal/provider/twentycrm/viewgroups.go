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
	viewGroupUncertain = "; this change may have taken effect, read the view with twenty-crm.views.get before repeating it"

	viewGroupResultSchema = `{"type":"object","properties":{"id":{"type":"string"},"view_id":{"type":"string"},` +
		`"is_visible":{"type":"boolean"},"position":{"type":"number"}},` +
		`"required":["id","view_id"],"additionalProperties":false}`
)

var viewgroupsUpdate = capability.Descriptor{
	ID: Provider + ".view-groups.update", Version: 1, Title: "Update a group of a Twenty CRM view",
	Description: "Show or hide one group of a view of the Twenty workspace of a connection, or change its position; " +
		"only the given values change and at least one is required. The value the group stands for does not " +
		"change. " + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "groups", "update"},
	Risk:     viewWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"is_visible":{"type":"boolean"},` +
		`"position":` + viewColumnPositionSchema + `},"required":["id"],"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewGroupResultSchema),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the group, as returned by twenty-crm.views.get"},
		{Name: "is_visible", Description: "Whether the group is shown"},
		{Name: "position", Description: "Position among the groups, a number from 0 through 1000000"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "The group"},
		{Name: "view_id", Description: "The view that holds the group"},
	},
	Examples: []capability.Example{{Description: "Hide a group",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174002","is_visible":false}`)}},
}

// ViewGroupResult is one group of a view as the group update reports it. The group value is never read.
type ViewGroupResult struct {
	ID        string  `json:"id"`
	ViewID    string  `json:"view_id"`
	IsVisible *bool   `json:"is_visible,omitempty"`
	Position  float64 `json:"position"`
}

// viewGroupWire is the positive projection of Twenty's view group: value, creator, and application are never decoded.
type viewGroupWire struct {
	ID        string   `json:"id"`
	ViewID    string   `json:"viewId"`
	IsVisible *bool    `json:"isVisible"`
	Position  *float64 `json:"position"`
	DeletedAt *string  `json:"deletedAt"`
}

// viewGroupPatch is the whole body of a group update. The field and its value are never sent.
type viewGroupPatch struct {
	IsVisible *bool    `json:"isVisible,omitempty"`
	Position  *float64 `json:"position,omitempty"`
}

// parseGroup checks the answer to a group change: it must name the requested group in the requested view and
// carry the requested values.
func parseGroup(op string, raw json.RawMessage, wantID, wantView string, patch *viewGroupPatch) (*ViewGroupResult, error) {
	var wire viewGroupWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view group")
	}
	if !equalUUID(wire.ID, wantID) || (wantView != "" && !equalUUID(wire.ViewID, wantView)) ||
		(patch != nil && ((patch.IsVisible != nil && (wire.IsVisible == nil || *wire.IsVisible != *patch.IsVisible)) ||
			(patch.Position != nil && (wire.Position == nil || *wire.Position != *patch.Position)))) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view group than the requested one")
	}
	return &ViewGroupResult{ID: wire.ID, ViewID: wire.ViewID, IsVisible: wire.IsVisible, Position: number(wire.Position)}, nil
}

// UpdateViewGroup reads the group and its view, then sends exactly one PATCH. The view of the group, not one
// named by the caller, decides what may change.
func (c *Client) UpdateViewGroup(ctx context.Context, id string, patch viewGroupPatch) (*ViewGroupResult, error) {
	const op = "update view group"
	var current json.RawMessage
	path := viewGroupsPath + "/" + url.PathEscape(id)
	if err := c.getWith(ctx, op, path, nil, viewResponseBytes, &current, viewPartTexts("group")); err != nil {
		return nil, err
	}
	group, err := parseGroup(op, current, id, "", nil)
	if err != nil {
		return nil, err
	}
	var wire viewGroupWire
	if json.Unmarshal(current, &wire) != nil || wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, viewPartTexts("group"))
	}
	if _, err := c.fetchChangeableView(ctx, op, group.ViewID, viewPartTexts("group")); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewGroupUncertain, http.MethodPatch, path, patch, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	updated, err := parseGroup(op, answer, id, group.ViewID, &patch)
	if err != nil {
		return nil, uncertainly(err, viewGroupUncertain)
	}
	return updated, nil
}

func invokeViewGroupsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID        string   `json:"id"`
		IsVisible *bool    `json:"is_visible"`
		Position  *float64 `json:"position"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return nil, invalidRequest("the arguments are not valid for this tool; check id, is_visible, and position")
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the group id must be a UUID")
	}
	if args.IsVisible == nil && args.Position == nil {
		return nil, invalidRequest("is_visible or position is required")
	}
	if !validColumnShape(nil, args.Position) {
		return nil, invalidRequest("position must be a number from 0 through 1000000")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateViewGroup(ctx, strings.ToLower(args.ID), viewGroupPatch{IsVisible: args.IsVisible, Position: args.Position})
}
