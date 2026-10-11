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

const viewDeleteUncertain = "; this deletion may have taken effect, look for the view with twentycrm.views.list " +
	"before repeating it"

const viewsDeleteResult = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"object":{"type":"string"},"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`

var viewsDelete = capability.Descriptor{
	ID: Provider + ".views.delete", Version: 1, Title: "Delete a Twenty CRM view",
	Description: "Permanently delete one custom view of the Twenty workspace of a connection; the loss is final and " +
		"the view cannot be restored. The view disappears for all members of the workspace, together with its " +
		"columns, filters, sorts, and groups. Standard views, index views, personal views, and widget views are " +
		"refused, and the last two read like an unknown view. " + viewWriteNote,
	Tags:     []string{"twentycrm", "views", "delete"},
	Risk:     viewWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewsDeleteResult),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the custom view, as returned by twentycrm.views.list"},
	},
	Examples: []capability.Example{{Description: "Delete a custom view",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// ViewDeleted names the view that was deleted, as read before the deletion.
type ViewDeleted struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Object  string `json:"object,omitempty"`
	Deleted bool   `json:"deleted"`
}

// destroyChild sends exactly one DELETE to a fixed metadata path and accepts only an answer that confirms the
// deletion and, when it names an entity, names the requested one. Twenty answers {"success":true} without an
// identifier, so the confirmation flag stands in for the name; anything else is reported as uncertain.
func (c *Client) destroyChild(ctx context.Context, op, uncertain, path, id string) error {
	var body json.RawMessage
	if err := c.changeWith(ctx, op, uncertain, http.MethodDelete, path, nil, &body); err != nil {
		return mapViewWritePermission(err)
	}
	unusable := uncertainly(provider.InvalidResponse(op, "Twenty answered in an unusable way"), uncertain)
	if len(bytes.TrimSpace(body)) == 0 {
		return unusable
	}
	var answer struct {
		ID      *string `json:"id"`
		Success *bool   `json:"success"`
	}
	if json.Unmarshal(body, &answer) != nil || answer.Success == nil || !*answer.Success ||
		(answer.ID != nil && !equalUUID(*answer.ID, id)) {
		return unusable
	}
	return nil
}

// DeleteView reads the view once, then sends exactly one DELETE. Only custom views of the whole workspace go.
func (c *Client) DeleteView(ctx context.Context, id string) (*ViewDeleted, error) {
	const op = "delete view"
	view, err := c.fetchChangeableView(ctx, op, id, viewStatusTexts)
	if err != nil {
		return nil, err
	}
	// The object name is a courtesy of the answer; the data model may be unreadable for this key.
	object := ""
	if found, objectErr := c.GetMetaObject(ctx, view.record.ObjectMetadataID); objectErr == nil {
		object = metaText(&found.NameSingular, metaNameMax)
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := c.destroyChild(ctx, op, viewDeleteUncertain, viewsPath+"/"+url.PathEscape(id), id); err != nil {
		return nil, err
	}
	return &ViewDeleted{ID: strings.ToLower(view.record.ID), Name: metaText(view.record.Name, metaNameMax),
		Object: object, Deleted: true}, nil
}

func invokeViewsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
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
		return nil, invalidRequest("the view id must be a UUID and the only argument")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DeleteView(ctx, strings.ToLower(args.ID))
}
