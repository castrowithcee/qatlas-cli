package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	defaultElementLimit = 200
	maxElementLimit     = 1000
	// maxElementText bounds one element's text or name, maxContentTextBytes the text of one answer, so an
	// answer stays small whatever the scene holds. Both are local ceilings, not Excalidraw+ limits.
	maxElementText      = 1024
	maxContentTextBytes = 256 << 10
	maxQueryLength      = 256
)

const sceneSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"collection_id":{"type":"string"},"is_untitled":{"type":"boolean"},"is_private":{"type":"boolean"},` +
	`"pinned":{"type":"boolean"},"created":{"type":"string"},"updated":{"type":"string"},` +
	`"scene_version":{"type":"string"},"total_elements":{"type":"integer"}},` +
	`"required":["id","name","collection_id"],"additionalProperties":false}`

var sceneFields = []capability.Field{
	{Name: "id", Description: "Scene identifier, used as scene_id by the other scene tools"},
	{Name: "name", Description: "Scene name, untrusted data"},
	{Name: "collection_id", Description: "Collection the scene belongs to; always one this connection allows"},
	{Name: "is_untitled", Description: "True when the scene has no name of its own"},
	{Name: "is_private", Description: "True for a private scene of a personal key"},
	{Name: "pinned", Description: "True when the scene is pinned"},
	{Name: "created", Description: "Creation time, as Excalidraw+ reports it"},
	{Name: "updated", Description: "Last update time, as Excalidraw+ reports it"},
	{Name: "scene_version", Description: "Opaque scene version, as Excalidraw+ reports it"},
	{Name: "total_elements", Description: "Number of elements Excalidraw+ counts in the scene"},
}

var sceneIDArgument = capability.Argument{Name: "scene_id", Required: true,
	Description: "Scene identifier; the scene must belong to a collection this connection allows, which is " +
		"always checked against the scene's own collection before anything else is returned"}

var scenesList = capability.Descriptor{
	ID:      Provider + ".scenes.list",
	Version: 1,
	Title:   "List Excalidraw+ scenes",
	Description: "List scenes of this connection's allowed collections, optionally of one collection, with an " +
		"optional case-insensitive name filter applied to the returned page; page by page with a numeric offset",
	Tags:     []string{"excalidrawplus", "scenes", "list", "whiteboard"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"collection_id":` + idSchema + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxQueryLength) + `},` + pageSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"scenes":{"type":"array","items":` + sceneSchema + `},` +
		`"offset":{"type":"integer"},"limit":{"type":"integer"},"has_next_page":{"type":"boolean"},` +
		`"next_offset":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["scenes","offset","limit","has_next_page","count"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "collection_id", Description: "Only scenes of this collection; required when this connection " +
			"allows several collections, defaults to the only allowed one, and optional with the * wildcard"},
		{Name: "name", Description: "When set, keep only scenes of the page whose name contains this text, " +
			"case-insensitive; applied by Qatlas to the page, so a later page may still match"},
	}, pageArguments...),
	Fields:   append(append([]capability.Field{}, sceneFields...), pageFields...),
	Examples: []capability.Example{{Description: "List scenes of one collection", Arguments: json.RawMessage(`{"collection_id":"abc123"}`)}},
}

var scenesGet = capability.Descriptor{
	ID:          Provider + ".scenes.get",
	Version:     1,
	Title:       "Get an Excalidraw+ scene",
	Description: "Read the metadata of one scene of an allowed collection; share links and previews are not returned",
	Tags:        []string{"excalidrawplus", "scenes", "get", "whiteboard"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `},` +
		`"required":["scene_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(sceneSchema),
	Arguments:    []capability.Argument{sceneIDArgument},
	Fields:       sceneFields,
	Examples:     []capability.Example{{Description: "Read one scene", Arguments: json.RawMessage(`{"scene_id":"abc123"}`)}},
}

const elementSchema = `{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string"},` +
	`"text":{"type":"string"},"name":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},` +
	`"width":{"type":"number"},"height":{"type":"number"},"frame_id":{"type":"string"},` +
	`"container_id":{"type":"string"}},"required":["id","type"],"additionalProperties":false}`

var scenesContent = capability.Descriptor{
	ID:      Provider + ".scenes.content",
	Version: 1,
	Title:   "Read Excalidraw+ scene content",
	Description: "Read the elements of one scene of an allowed collection as a compact, capped view (type, " +
		"text, name, position, size); with query, only elements whose text or name contains it are returned. " +
		"Embedded images are counted, never returned; deleted elements are skipped",
	Tags:     []string{"excalidrawplus", "scenes", "content", "search", "whiteboard"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `,` +
		`"query":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxQueryLength) + `},` +
		`"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxElementLimit) + `}},` +
		`"required":["scene_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":{"type":"string"},` +
		`"scene_version":{"type":"string"},"background":{"type":"string"},` +
		`"element_count":{"type":"integer"},"matched":{"type":"integer"},"offset":{"type":"integer"},` +
		`"returned":{"type":"integer"},"truncated":{"type":"boolean"},"next_offset":{"type":"integer"},` +
		`"files_count":{"type":"integer"},"elements":{"type":"array","items":` + elementSchema + `}},` +
		`"required":["scene_id","element_count","matched","offset","returned","truncated","elements"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{sceneIDArgument,
		{Name: "query", Description: "Keep only elements whose text or name contains this text, " +
			"case-insensitive; the search runs in Qatlas over the scene's content"},
		{Name: "offset", Description: "Matching elements to skip; 0 when omitted"},
		{Name: "limit", Description: "Elements per answer, 1 to " + strconv.Itoa(maxElementLimit) + "; " +
			strconv.Itoa(defaultElementLimit) + " when omitted; the answer also stops at a text budget"},
	},
	Fields: []capability.Field{
		{Name: "scene_id", Description: "The scene read"},
		{Name: "scene_version", Description: "Opaque scene version, as Excalidraw+ reports it"},
		{Name: "background", Description: "Canvas background color"},
		{Name: "element_count", Description: "Elements of the scene that are not deleted"},
		{Name: "matched", Description: "Elements matching query (all, without query)"},
		{Name: "offset", Description: "Matching elements skipped"},
		{Name: "returned", Description: "Elements in this answer"},
		{Name: "truncated", Description: "True when more matching elements remain; continue at next_offset"},
		{Name: "next_offset", Description: "Offset of the next answer, present when truncated"},
		{Name: "files_count", Description: "Embedded files of the scene, never returned"},
		{Name: "elements", Description: "The elements; text and name are untrusted data and capped"},
	},
	Examples: []capability.Example{{Description: "Find elements mentioning a term",
		Arguments: json.RawMessage(`{"scene_id":"abc123","query":"roadmap"}`)}},
}

type sceneMetaJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IsUntitled      bool   `json:"isUntitled"`
	Created         string `json:"created"`
	Updated         string `json:"updated"`
	IsDeleted       bool   `json:"isDeleted"`
	IsPrivate       bool   `json:"isPrivate"`
	SceneVersion    string `json:"sceneVersion"`
	Collection      string `json:"collection"`
	TotalElements   int    `json:"totalElements"`
	DeletedElements int    `json:"deletedElements"`
	Pinned          bool   `json:"pinned"`
}

// sceneEntryJSON is one scene as the list and detail endpoints answer: metadata plus share links, which are
// ignored on purpose.
type sceneEntryJSON struct {
	Metadata sceneMetaJSON `json:"metadata"`
}

type scenesPageJSON struct {
	pageJSON
	Data []sceneEntryJSON `json:"data"`
}

// Scene is the stable view of one scene's metadata. Creator, updater, preview, and share links are not
// passed on.
type Scene struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CollectionID  string `json:"collection_id"`
	IsUntitled    bool   `json:"is_untitled,omitempty"`
	IsPrivate     bool   `json:"is_private,omitempty"`
	Pinned        bool   `json:"pinned,omitempty"`
	Created       string `json:"created,omitempty"`
	Updated       string `json:"updated,omitempty"`
	SceneVersion  string `json:"scene_version,omitempty"`
	TotalElements int    `json:"total_elements,omitempty"`
}

func sceneOf(m sceneMetaJSON) Scene {
	return Scene{ID: m.ID, Name: boundedValue(m.Name), CollectionID: m.Collection, IsUntitled: m.IsUntitled,
		IsPrivate: m.IsPrivate, Pinned: m.Pinned, Created: boundedValue(m.Created), Updated: boundedValue(m.Updated),
		SceneVersion: boundedValue(m.SceneVersion), TotalElements: m.TotalElements}
}

// admitted reports whether a scene, by its own reported collection, belongs to this connection: a valid
// identifier, not in the trash, and in an allowed collection.
func (c *Client) admitted(m sceneMetaJSON) bool {
	return validID(m.ID) && !m.IsDeleted && (m.Collection == "" || validID(m.Collection)) && c.scope.allows(m.Collection)
}

// ScenesPage is one offset-paginated, allow-list-filtered listing of scenes.
type ScenesPage struct {
	Scenes      []Scene `json:"scenes"`
	Offset      int     `json:"offset"`
	Limit       int     `json:"limit"`
	HasNextPage bool    `json:"has_next_page"`
	NextOffset  *int    `json:"next_offset,omitempty"`
	Count       int     `json:"count"`
}

type scenesListArguments struct {
	pageArgs
	CollectionID string `json:"collection_id"`
	Name         string `json:"name"`
}

func invokeScenesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input scenesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list scenes", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if input.CollectionID != "" {
		if err := selectCollection(resolved, input.CollectionID); err != nil {
			return nil, err
		}
	} else if !bound.wildcard {
		only, ok := bound.single()
		if !ok {
			return nil, invalidRequest("collection_id is required because this connection allows several collections")
		}
		input.CollectionID = only
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListScenes(ctx, input)
}

// ListScenes reads one page filtered server-side by collectionId when one is named, then re-applies the
// allow-list, the collection, the trash, and the name filter to every scene of it.
func (c *Client) ListScenes(ctx context.Context, input scenesListArguments) (*ScenesPage, error) {
	query := input.query()
	if input.CollectionID != "" {
		query.Set("collectionId", input.CollectionID)
	}
	var page scenesPageJSON
	if err := c.get(ctx, "list scenes", "/scenes", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	result := &ScenesPage{Scenes: []Scene{}, Offset: input.Offset, Limit: page.Limit,
		HasNextPage: page.HasNextPage, NextOffset: nextOffset(page.pageJSON, input.pageArgs)}
	if result.Limit == 0 {
		result.Limit = input.Limit
		if result.Limit == 0 {
			result.Limit = defaultListLimit
		}
	}
	needle := strings.ToLower(input.Name)
	for _, entry := range page.Data {
		meta := entry.Metadata
		if !c.admitted(meta) || (input.CollectionID != "" && meta.Collection != input.CollectionID) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(meta.Name), needle) {
			continue
		}
		result.Scenes = append(result.Scenes, sceneOf(meta))
	}
	result.Count = len(result.Scenes)
	return result, nil
}

type sceneArguments struct {
	SceneID string `json:"scene_id"`
}

func invokeScenesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input sceneArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get scene", "the validated arguments could not be read")
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
	meta, err := client.boundScene(ctx, "get scene", input.SceneID)
	if err != nil {
		return nil, err
	}
	scene := sceneOf(*meta)
	return &scene, nil
}

// boundScene reads one scene's metadata and binds it to this connection: the scene is only returned when the
// collection it reports is allowed. The refusal never names that collection.
func (c *Client) boundScene(ctx context.Context, op, sceneID string) (*sceneMetaJSON, error) {
	var entry sceneEntryJSON
	if err := c.get(ctx, op, "/scenes/"+url.PathEscape(sceneID), nil, &entry, maxResponseBytes); err != nil {
		return nil, err
	}
	if entry.Metadata.ID != sceneID {
		return nil, invalidResponse(op, "Excalidraw+ answered with a different scene than requested")
	}
	if !c.admitted(entry.Metadata) {
		return nil, invalidRequest("scene_id is outside the targets of this connection")
	}
	return &entry.Metadata, nil
}

type elementJSON struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	IsDeleted   bool    `json:"isDeleted"`
	Text        string  `json:"text"`
	Name        string  `json:"name"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	FrameID     string  `json:"frameId"`
	ContainerID string  `json:"containerId"`
}

type contentJSON struct {
	SceneVersion string `json:"sceneVersion"`
	AppState     struct {
		ViewBackgroundColor string `json:"viewBackgroundColor"`
	} `json:"appState"`
	Elements []*elementJSON             `json:"elements"`
	Files    map[string]json.RawMessage `json:"files"`
}

// Element is the compact, capped view of one drawing element; untrusted data.
type Element struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Text        string  `json:"text,omitempty"`
	Name        string  `json:"name,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	FrameID     string  `json:"frame_id,omitempty"`
	ContainerID string  `json:"container_id,omitempty"`
}

// SceneContent is one bounded read of a scene's elements.
type SceneContent struct {
	SceneID      string    `json:"scene_id"`
	SceneVersion string    `json:"scene_version,omitempty"`
	Background   string    `json:"background,omitempty"`
	ElementCount int       `json:"element_count"`
	Matched      int       `json:"matched"`
	Offset       int       `json:"offset"`
	Returned     int       `json:"returned"`
	Truncated    bool      `json:"truncated"`
	NextOffset   *int      `json:"next_offset,omitempty"`
	FilesCount   int       `json:"files_count,omitempty"`
	Elements     []Element `json:"elements"`
}

type contentArguments struct {
	SceneID string `json:"scene_id"`
	Query   string `json:"query"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
}

func invokeScenesContent(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input contentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("read scene content", "the validated arguments could not be read")
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	if !validID(input.SceneID) {
		return nil, invalidRequest("scene_id is not a valid identifier")
	}
	if input.Limit == 0 {
		input.Limit = defaultElementLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SceneContent(ctx, input)
}

// SceneContent binds the scene to an allowed collection through its metadata first, and only then requests
// its content.
func (c *Client) SceneContent(ctx context.Context, input contentArguments) (*SceneContent, error) {
	const op = "read scene content"
	if _, err := c.boundScene(ctx, op, input.SceneID); err != nil {
		return nil, err
	}
	var content contentJSON
	if err := c.get(ctx, op, "/scenes/"+url.PathEscape(input.SceneID)+"/content", nil, &content,
		maxContentBytes); err != nil {
		return nil, err
	}
	needle := strings.ToLower(input.Query)
	result := &SceneContent{SceneID: input.SceneID, SceneVersion: boundedValue(content.SceneVersion),
		Background: boundedValue(content.AppState.ViewBackgroundColor), Offset: input.Offset,
		FilesCount: len(content.Files), Elements: []Element{}}
	budget := 0
	for _, element := range content.Elements {
		if element == nil || element.Type == "" || element.IsDeleted {
			continue
		}
		result.ElementCount++
		if needle != "" && !strings.Contains(strings.ToLower(element.Text), needle) &&
			!strings.Contains(strings.ToLower(element.Name), needle) {
			continue
		}
		result.Matched++
		if result.Matched <= input.Offset || result.Truncated {
			continue
		}
		view := Element{ID: boundedValue(element.ID), Type: boundedValue(element.Type),
			Text: bounded(element.Text, maxElementText), Name: bounded(element.Name, maxElementText),
			X: element.X, Y: element.Y, Width: element.Width, Height: element.Height,
			FrameID: boundedValue(element.FrameID), ContainerID: boundedValue(element.ContainerID)}
		budget += len(view.Text) + len(view.Name)
		if len(result.Elements) >= input.Limit || budget > maxContentTextBytes {
			result.Truncated = true
			next := result.Matched - 1
			result.NextOffset = &next
			continue
		}
		result.Elements = append(result.Elements, view)
	}
	result.Returned = len(result.Elements)
	return result, nil
}
