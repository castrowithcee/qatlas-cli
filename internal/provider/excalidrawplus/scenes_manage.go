package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxSceneName is a local ceiling; Excalidraw+ documents only a minimum length of 1 for a name.
const maxSceneName = 250

// manageRisk is the contract of a confirmed change. Idempotency stays unknown until it is shown live.
func manageRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

const manageNote = "Names are untrusted provider data; the change is sent once and never repeated"

const nameSchema = `{"type":"string","minLength":1,"maxLength":250}`

var scenesCreate = capability.Descriptor{
	ID:      Provider + ".scenes.create",
	Version: 1,
	Title:   "Create an Excalidraw+ scene",
	Description: "Create a new, empty, unpinned scene with a name in an allowed collection; the scene's content " +
		"is not set. " + manageNote,
	Tags:     []string{"excalidrawplus", "scenes", "create", "whiteboard"},
	Risk:     manageRisk(capability.EffectCreate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + nameSchema + `,"collection_id":` +
		idSchema + `},"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(sceneSchema),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Scene name, 1 to 250 characters, no control characters"},
		{Name: "collection_id", Description: "Allowed collection to create the scene in; required unless this " +
			"connection allows exactly one collection, which is then the default. A scene is never created " +
			"outside the allowed collections, and the private collection is not offered"},
	},
	Fields:   sceneFields,
	Examples: []capability.Example{{Description: "Create a scene", Arguments: json.RawMessage(`{"name":"Roadmap","collection_id":"abc123"}`)}},
}

var scenesUpdate = capability.Descriptor{
	ID:      Provider + ".scenes.update",
	Version: 1,
	Title:   "Rename or move an Excalidraw+ scene",
	Description: "Rename a scene of an allowed collection, move it to another allowed collection, or both; " +
		"at least one of name and collection_id is required. The scene is first bound to an allowed collection " +
		"through its own metadata. " + manageNote,
	Tags:     []string{"excalidrawplus", "scenes", "update", "whiteboard"},
	Risk:     manageRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `,"name":` + nameSchema +
		`,"collection_id":` + idSchema + `},"required":["scene_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(sceneSchema),
	Arguments: []capability.Argument{sceneIDArgument,
		{Name: "name", Description: "New name, 1 to 250 characters, no control characters"},
		{Name: "collection_id", Description: "Allowed collection to move the scene to"},
	},
	Fields:   sceneFields,
	Examples: []capability.Example{{Description: "Rename a scene", Arguments: json.RawMessage(`{"scene_id":"abc123","name":"Roadmap 2027"}`)}},
}

// validSceneName keeps a name to printable text of 1 to 250 characters that is not only whitespace.
func validSceneName(name string) bool {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxSceneName {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// privateCollection is the personal-key virtual collection, which this provider never offers.
const privateCollection = "private"

// checkTargetCollection refuses, locally, a collection outside the allow-list or the private collection.
func checkTargetCollection(resolved *config.Resolved, collectionID string) error {
	if err := selectCollection(resolved, collectionID); err != nil {
		return err
	}
	if collectionID == privateCollection {
		return invalidRequest("collection_id is outside the targets of this connection")
	}
	return nil
}

type createArguments struct {
	Name         string `json:"name"`
	CollectionID string `json:"collection_id"`
}

type createBody struct {
	Name         string `json:"name"`
	Pinned       bool   `json:"pinned"`
	CollectionID string `json:"collectionId"`
}

func invokeScenesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input createArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("create scene", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !validSceneName(input.Name) {
		return nil, invalidRequest("name must be 1 to 250 printable characters")
	}
	if input.CollectionID == "" {
		only, ok := bound.single()
		if !ok {
			return nil, invalidRequest("collection_id is required unless this connection allows exactly one collection")
		}
		input.CollectionID = only
	}
	if err := checkTargetCollection(resolved, input.CollectionID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "create scene"
	var answer sceneEntryJSON
	if err := client.send(ctx, op, http.MethodPost, "/scenes",
		createBody{Name: input.Name, CollectionID: input.CollectionID}, &answer); err != nil {
		return nil, err
	}
	if !validID(answer.Metadata.ID) {
		return nil, invalidResponse(op, "Excalidraw+ returned an invalid response"+changeUncertain)
	}
	scene := sceneOf(answer.Metadata)
	return &scene, nil
}

type updateArguments struct {
	SceneID      string  `json:"scene_id"`
	Name         *string `json:"name"`
	CollectionID *string `json:"collection_id"`
}

type updateBody struct {
	Name         *string `json:"name,omitempty"`
	CollectionID *string `json:"collectionId,omitempty"`
}

func invokeScenesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input updateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("update scene", "the validated arguments could not be read")
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	if !validID(input.SceneID) {
		return nil, invalidRequest("scene_id is not a valid identifier")
	}
	if input.Name == nil && input.CollectionID == nil {
		return nil, invalidRequest("name or collection_id is required")
	}
	if input.Name != nil && !validSceneName(*input.Name) {
		return nil, invalidRequest("name must be 1 to 250 printable characters")
	}
	if input.CollectionID != nil {
		if err := checkTargetCollection(resolved, *input.CollectionID); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "update scene"
	if _, err := client.boundScene(ctx, op, input.SceneID); err != nil {
		return nil, err
	}
	var answer sceneEntryJSON
	if err := client.send(ctx, op, http.MethodPatch, "/scenes/"+url.PathEscape(input.SceneID),
		updateBody{Name: input.Name, CollectionID: input.CollectionID}, &answer); err != nil {
		return nil, err
	}
	if answer.Metadata.ID != input.SceneID {
		return nil, invalidResponse(op, "Excalidraw+ answered with a different scene than requested"+changeUncertain)
	}
	scene := sceneOf(answer.Metadata)
	return &scene, nil
}
