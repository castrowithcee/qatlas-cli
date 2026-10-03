package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const deleteNote = "Moves it to the trash of Excalidraw+; shared links and embeds stop working, and only Excalidraw+ " +
	"itself can restore it from its trash, Qatlas cannot. Sent once and never repeated"

const deleteSchema = `{"type":"object","properties":{"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
	`"required":["id","deleted"],"additionalProperties":false}`

var deleteFields = []capability.Field{
	{Name: "id", Description: "Identifier of the scene or collection that was moved to the trash"},
	{Name: "deleted", Description: "True when Excalidraw+ accepted the deletion"},
}

var scenesDelete = capability.Descriptor{
	ID:      Provider + ".scenes.delete",
	Version: 1,
	Title:   "Delete an Excalidraw+ scene",
	Description: "Delete a scene of an allowed collection. The scene is first bound to an allowed collection " +
		"through its own metadata. " + deleteNote + "; the shared links and embeds of this scene break",
	Tags:                  []string{"excalidrawplus", "scenes", "delete", "whiteboard"},
	Risk:                  manageRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `},"required":["scene_id"],"additionalProperties":false}`),
	OutputSchema:          json.RawMessage(deleteSchema),
	Arguments:             []capability.Argument{sceneIDArgument},
	Fields:                deleteFields,
	Examples:              []capability.Example{{Description: "Delete a scene", Arguments: json.RawMessage(`{"scene_id":"abc123"}`)}},
}

var collectionsDelete = capability.Descriptor{
	ID:      Provider + ".collections.delete",
	Version: 1,
	Title:   "Delete an Excalidraw+ collection",
	Description: "Delete an allowed collection, other than the private and the default collection. " + deleteNote +
		"; the shared links and embeds of all scenes in the collection break",
	Tags:                  []string{"excalidrawplus", "collections", "delete", "whiteboard"},
	Risk:                  manageRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           json.RawMessage(`{"type":"object","properties":{"collection_id":` + idSchema + `},"required":["collection_id"],"additionalProperties":false}`),
	OutputSchema:          json.RawMessage(deleteSchema),
	Arguments: []capability.Argument{{Name: "collection_id", Required: true,
		Description: "Allowed collection to delete; the private and the default collection are refused"}},
	Fields:   deleteFields,
	Examples: []capability.Example{{Description: "Delete a collection", Arguments: json.RawMessage(`{"collection_id":"abc123"}`)}},
}

// Deleted is the result of a deletion.
type Deleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func invokeScenesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		SceneID string `json:"scene_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("delete scene", "the validated arguments could not be read")
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	if !validID(input.SceneID) {
		return nil, invalidRequest("scene_id is not a valid identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "delete scene"
	if _, err := client.boundScene(ctx, op, input.SceneID); err != nil {
		return nil, err
	}
	if err := client.send(ctx, op, http.MethodDelete, "/scenes/"+url.PathEscape(input.SceneID), nil, nil); err != nil {
		return nil, err
	}
	return &Deleted{ID: input.SceneID, Deleted: true}, nil
}

func invokeCollectionsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		CollectionID string `json:"collection_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("delete collection", "the validated arguments could not be read")
	}
	if err := checkTargetCollection(resolved, input.CollectionID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "delete collection"
	path := "/collections/" + url.PathEscape(input.CollectionID)
	var collection collectionJSON
	if err := client.get(ctx, op, path, nil, &collection, maxResponseBytes); err != nil {
		return nil, err
	}
	if collection.ID != input.CollectionID {
		return nil, invalidResponse(op, "Excalidraw+ answered with a different collection than requested")
	}
	if collection.IsDeleted {
		return nil, invalidRequest("collection_id is outside the targets of this connection")
	}
	if collection.IsDefault {
		return nil, invalidRequest("the default collection is not deleted by this tool")
	}
	if err := client.send(ctx, op, http.MethodDelete, path, nil, nil); err != nil {
		return nil, err
	}
	return &Deleted{ID: input.CollectionID, Deleted: true}, nil
}
