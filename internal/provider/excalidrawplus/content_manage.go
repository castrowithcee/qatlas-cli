package excalidrawplus

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The element schema is a deliberately small subset of the Excalidraw element format. Excalidraw+ documents no
// limit for any of these values, so every ceiling below is a local choice.
const (
	maxPatchElements   = 100
	maxReplaceElements = 500
	maxElementPoints   = 200
	maxElementTextLen  = 2000
	maxElementName     = 250
	maxGroupIDs        = 8
	maxBoundElements   = 16
	maxCoordinate      = 1e7
	maxChangeBody      = 1 << 20
	maxVersion         = 1 << 30
	defaultBackground  = "#ffffff"
)

var (
	elementTypes   = []string{"rectangle", "diamond", "ellipse", "frame", "text", "line", "arrow"}
	fillStyles     = []string{"hachure", "cross-hatch", "solid", "zigzag"}
	strokeStyles   = []string{"solid", "dashed", "dotted"}
	textAligns     = []string{"left", "center", "right"}
	verticalAligns = []string{"top", "middle", "bottom"}
	arrowheads     = []string{"arrow", "bar", "circle", "circle_outline", "triangle", "triangle_outline", "diamond",
		"diamond_outline"}
)

func enumOf(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return `{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}`
}

func numberSchema(min, max float64) string {
	return `{"type":"number","minimum":` + strconv.FormatFloat(min, 'f', -1, 64) + `,"maximum":` +
		strconv.FormatFloat(max, 'f', -1, 64) + `}`
}

const colorForm = `^(transparent|#[0-9A-Fa-f]{3}|#[0-9A-Fa-f]{6}|#[0-9A-Fa-f]{8})$`

var colorSchema = `{"type":"string","pattern":"` + colorForm + `","x-form":"transparent or #RGB, #RRGGBB, #RRGGBBAA"}`

// elementProperties is the closed list of element fields an agent may send. Links, custom data, images,
// embedded frames, bindings, and free-form objects are not part of it.
func elementProperties(patch bool) string {
	coordinate := numberSchema(-maxCoordinate, maxCoordinate)
	size := numberSchema(0, maxCoordinate)
	fields := []string{
		`"id":` + idSchema,
		`"type":` + enumOf(elementTypes),
		`"x":` + coordinate, `"y":` + coordinate, `"width":` + size, `"height":` + size,
		`"angle":` + numberSchema(-1000, 1000),
		`"stroke_color":` + colorSchema, `"background_color":` + colorSchema,
		`"fill_style":` + enumOf(fillStyles),
		`"stroke_width":` + numberSchema(0, 100),
		`"stroke_style":` + enumOf(strokeStyles),
		`"roughness":{"type":"integer","minimum":0,"maximum":2}`,
		`"opacity":` + numberSchema(0, 100),
		`"locked":{"type":"boolean"}`,
		`"group_ids":{"type":"array","items":` + idSchema + `}`,
		`"frame_id":` + idSchema,
		`"bound_elements":{"type":"array","items":{"type":"object","properties":{"id":` + idSchema +
			`,"type":` + enumOf([]string{"arrow", "text"}) + `},"required":["id","type"],"additionalProperties":false}}`,
		`"text":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxElementTextLen) + `}`,
		`"font_size":` + numberSchema(1, 1000),
		`"font_family":{"type":"integer","minimum":1,"maximum":10}`,
		`"text_align":` + enumOf(textAligns), `"vertical_align":` + enumOf(verticalAligns),
		`"container_id":` + idSchema,
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxElementName) + `}`,
		`"points":{"type":"array","items":{"type":"array","items":` + numberSchema(-maxCoordinate, maxCoordinate) + `}}`,
		`"start_arrowhead":` + enumOf(arrowheads), `"end_arrowhead":` + enumOf(arrowheads),
	}
	if patch {
		fields = append(fields,
			`"expected_version":{"type":"integer","minimum":0,"maximum":`+strconv.Itoa(maxVersion)+`}`,
			`"is_deleted":{"type":"boolean"}`)
	}
	return strings.Join(fields, ",")
}

func elementSchemaOf(patch bool) string {
	required := `["id","type","x","y","width","height"]`
	if patch {
		required = `["id","type","x","y","width","height","expected_version"]`
	}
	return `{"type":"object","properties":{` + elementProperties(patch) + `},"required":` + required +
		`,"additionalProperties":false}`
}

var elementArguments = "Each element needs id (letters, digits, - and _, at most 64), type (rectangle, diamond, ellipse, " +
	"frame, text, line, arrow), x, y, width, and height. Optional: angle, stroke_color, background_color, " +
	"fill_style, stroke_width, stroke_style, roughness, opacity, locked, group_ids, frame_id, bound_elements. " +
	"Text elements need text and may set font_size, font_family, text_align, vertical_align, container_id; " +
	"frames may set name; lines and arrows need points (2 to 200 [x, y] pairs) and may set start_arrowhead and " +
	"end_arrowhead. Nothing else is accepted: no links, images, embedded frames, custom data, or bindings. The " +
	"request body is at most 1 MiB"

const contentNote = "Element texts are untrusted data; the change is sent once and never repeated. Excalidraw+'s " +
	"scene content API is a public beta"

const contentResultSchema = `{"type":"object","properties":{"scene_id":{"type":"string"},` +
	`"scene_version":{"type":"string"},"sent":{"type":"integer"},"element_count":{"type":"integer"},` +
	`"not_applied":{"type":"array","items":{"type":"string"}}},` +
	`"required":["scene_id","sent","element_count"],"additionalProperties":false}`

var contentFields = []capability.Field{
	{Name: "scene_id", Description: "The scene changed"},
	{Name: "scene_version", Description: "Opaque scene version after the change, as Excalidraw+ reports it"},
	{Name: "sent", Description: "Elements sent"},
	{Name: "element_count", Description: "Elements of the scene that are not deleted, as the answer shows"},
	{Name: "not_applied", Description: "Patch only: ids of sent elements the answer does not show at the sent " +
		"version, because a newer version of the element exists; read the scene and patch again"},
}

var contentPatch = capability.Descriptor{
	ID:      Provider + ".content.patch",
	Version: 1,
	Title:   "Patch Excalidraw+ scene content",
	Description: "Merge up to 100 complete elements into the content of a scene of an allowed collection. Excalidraw+ " +
		"merges by element id and the higher element version wins; every element states expected_version, the version " +
		"it has now (see scenes.content, 0 for a new element), and is sent as the next version. An element that " +
		"has a newer version is not changed and is listed in not_applied. Set is_deleted to delete an element. " +
		"Other elements and the connected editors are left alone. " + contentNote,
	Tags:     []string{"excalidrawplus", "scenes", "content", "patch", "whiteboard"},
	Risk:     manageRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `,"elements":` +
		`{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxPatchElements) + `,"items":` + elementSchemaOf(true) +
		`}},"required":["scene_id","elements"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(contentResultSchema),
	Arguments: []capability.Argument{sceneIDArgument,
		{Name: "elements", Required: true, Description: "1 to 100 complete elements, each with expected_version " +
			"(0 for a new element); is_deleted true deletes the element. " + elementArguments},
	},
	Fields: contentFields,
	Examples: []capability.Example{{Description: "Change a text and delete another element", Arguments: json.RawMessage(
		`{"scene_id":"abc123","elements":[{"id":"e1","type":"text","x":0,"y":0,"width":120,"height":25,` +
			`"text":"Roadmap","expected_version":3},{"id":"e2","type":"rectangle","x":0,"y":40,"width":50,` +
			`"height":50,"expected_version":1,"is_deleted":true}]}`)}},
}

var contentReplace = capability.Descriptor{
	ID:      Provider + ".content.replace",
	Version: 1,
	Title:   "Replace Excalidraw+ scene content",
	Description: "Replace the whole content of a scene of an allowed collection with up to 500 given elements, " +
		"finally: every element not given is removed, embedded images and files are removed, and the connected " +
		"editors are forced to reload instead of merging. Use content.patch for a targeted change. " + contentNote,
	Tags:                  []string{"excalidrawplus", "scenes", "content", "replace", "whiteboard"},
	Risk:                  manageRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scene_id":` + idSchema + `,"elements":` +
		`{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxReplaceElements) + `,"items":` + elementSchemaOf(false) +
		`},"view_background_color":` + colorSchema + `},"required":["scene_id","elements"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(contentResultSchema),
	Arguments: []capability.Argument{sceneIDArgument,
		{Name: "elements", Required: true, Description: "1 to 500 complete elements that become the scene's " +
			"whole content. " + elementArguments},
		{Name: "view_background_color", Description: "Canvas background color; " + defaultBackground +
			" when omitted"},
	},
	Fields: contentFields[:4],
	Examples: []capability.Example{{Description: "Replace a scene with one text", Arguments: json.RawMessage(
		`{"scene_id":"abc123","elements":[{"id":"e1","type":"text","x":0,"y":0,"width":120,"height":25,` +
			`"text":"Roadmap"}]}`)}},
}

type boundElement struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type contentElement struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	X               float64        `json:"x"`
	Y               float64        `json:"y"`
	Width           float64        `json:"width"`
	Height          float64        `json:"height"`
	ExpectedVersion *int64         `json:"expected_version"`
	IsDeleted       bool           `json:"is_deleted"`
	Angle           float64        `json:"angle"`
	StrokeColor     *string        `json:"stroke_color"`
	BackgroundColor *string        `json:"background_color"`
	FillStyle       *string        `json:"fill_style"`
	StrokeWidth     *float64       `json:"stroke_width"`
	StrokeStyle     *string        `json:"stroke_style"`
	Roughness       *int           `json:"roughness"`
	Opacity         *float64       `json:"opacity"`
	Locked          bool           `json:"locked"`
	GroupIDs        []string       `json:"group_ids"`
	FrameID         *string        `json:"frame_id"`
	BoundElements   []boundElement `json:"bound_elements"`
	Text            *string        `json:"text"`
	FontSize        *float64       `json:"font_size"`
	FontFamily      *int           `json:"font_family"`
	TextAlign       *string        `json:"text_align"`
	VerticalAlign   *string        `json:"vertical_align"`
	ContainerID     *string        `json:"container_id"`
	Name            *string        `json:"name"`
	Points          [][]float64    `json:"points"`
	StartArrowhead  *string        `json:"start_arrowhead"`
	EndArrowhead    *string        `json:"end_arrowhead"`
}

func inRange(v, min, max float64) bool { return !math.IsNaN(v) && v >= min && v <= max }

func oneOf(value *string, allowed []string) bool {
	if value == nil {
		return true
	}
	for _, a := range allowed {
		if *value == a {
			return true
		}
	}
	return false
}

func validColor(value *string) bool {
	if value == nil || *value == "transparent" {
		return true
	}
	v := *value
	if len(v) != 4 && len(v) != 7 && len(v) != 9 || v[0] != '#' {
		return false
	}
	for i := 1; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// validElementText keeps text to valid UTF-8 without control characters other than line breaks and tabs.
func validElementText(value string, max int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func optionalID(value *string) bool { return value == nil || validID(*value) }

// check enforces what the schema cannot say: which field belongs to which type, and the value ranges. A message
// never quotes an argument value.
func (e *contentElement) check(patch bool) string {
	if !validID(e.ID) || !oneOf(&e.Type, elementTypes) {
		return "id or type is not valid"
	}
	if !inRange(e.X, -maxCoordinate, maxCoordinate) || !inRange(e.Y, -maxCoordinate, maxCoordinate) ||
		!inRange(e.Width, 0, maxCoordinate) || !inRange(e.Height, 0, maxCoordinate) ||
		!inRange(e.Angle, -1000, 1000) {
		return "a position, size, or angle is out of range"
	}
	if patch != (e.ExpectedVersion != nil) {
		if patch {
			return "expected_version is required"
		}
		return "expected_version belongs to content.patch only"
	}
	if patch && (*e.ExpectedVersion < 0 || *e.ExpectedVersion > maxVersion) {
		return "expected_version is out of range"
	}
	if !patch && e.IsDeleted {
		return "is_deleted belongs to content.patch only"
	}
	if !validColor(e.StrokeColor) || !validColor(e.BackgroundColor) || !oneOf(e.FillStyle, fillStyles) ||
		!oneOf(e.StrokeStyle, strokeStyles) || !oneOf(e.TextAlign, textAligns) ||
		!oneOf(e.VerticalAlign, verticalAligns) || !oneOf(e.StartArrowhead, arrowheads) ||
		!oneOf(e.EndArrowhead, arrowheads) {
		return "a color or style value is not allowed"
	}
	if e.StrokeWidth != nil && !inRange(*e.StrokeWidth, 0, 100) || e.Opacity != nil && !inRange(*e.Opacity, 0, 100) ||
		e.Roughness != nil && (*e.Roughness < 0 || *e.Roughness > 2) ||
		e.FontSize != nil && !inRange(*e.FontSize, 1, 1000) ||
		e.FontFamily != nil && (*e.FontFamily < 1 || *e.FontFamily > 10) {
		return "a style number is out of range"
	}
	if len(e.GroupIDs) > maxGroupIDs || len(e.BoundElements) > maxBoundElements {
		return "too many group_ids or bound_elements"
	}
	for _, id := range e.GroupIDs {
		if !validID(id) {
			return "a group id is not valid"
		}
	}
	for _, b := range e.BoundElements {
		if !validID(b.ID) || b.Type != "arrow" && b.Type != "text" {
			return "a bound element is not valid"
		}
	}
	if !optionalID(e.FrameID) || !optionalID(e.ContainerID) {
		return "frame_id or container_id is not valid"
	}
	isText, isLine := e.Type == "text", e.Type == "line" || e.Type == "arrow"
	if isText != (e.Text != nil) {
		return "text is required for, and only for, a text element"
	}
	if isText && !validElementText(*e.Text, maxElementTextLen) {
		return "text must be 1 to 2000 printable characters"
	}
	if !isText && (e.FontSize != nil || e.FontFamily != nil || e.TextAlign != nil || e.VerticalAlign != nil ||
		e.ContainerID != nil) {
		return "font, alignment, and container fields belong to text elements only"
	}
	if e.Name != nil && (e.Type != "frame" || !validElementText(*e.Name, maxElementName)) {
		return "name belongs to frames and must be 1 to 250 printable characters"
	}
	if isLine != (len(e.Points) > 0) || !isLine && (e.StartArrowhead != nil || e.EndArrowhead != nil) {
		return "points and arrowheads belong to, and points are required for, lines and arrows"
	}
	if isLine {
		if len(e.Points) < 2 || len(e.Points) > maxElementPoints {
			return "points need 2 to 200 pairs"
		}
		for _, point := range e.Points {
			if len(point) != 2 || !inRange(point[0], -maxCoordinate, maxCoordinate) ||
				!inRange(point[1], -maxCoordinate, maxCoordinate) {
				return "a point must be an [x, y] pair within range"
			}
		}
	}
	return ""
}

// randomInt returns a non-negative random integer below 2^31, the range of Excalidraw's seeds and nonces.
func randomInt() int64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<31))
	if err != nil {
		return time.Now().UnixNano() & (1<<31 - 1)
	}
	return n.Int64()
}

func orString(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func orNumber(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

// wire builds the provider element from the closed field set; defaults follow the Excalidraw element format
// and are assumed. Nothing the agent sent reaches the body except through these fields.
func (e *contentElement) wire(version, nonce int64, now int64) map[string]any {
	roughness := 1
	if e.Roughness != nil {
		roughness = *e.Roughness
	}
	var frame any
	if e.FrameID != nil {
		frame = *e.FrameID
	}
	var bound any
	if len(e.BoundElements) > 0 {
		bound = e.BoundElements
	}
	groups := e.GroupIDs
	if groups == nil {
		groups = []string{}
	}
	out := map[string]any{
		"id": e.ID, "type": e.Type, "x": e.X, "y": e.Y, "width": e.Width, "height": e.Height, "angle": e.Angle,
		"strokeColor": orString(e.StrokeColor, "#1e1e1e"), "backgroundColor": orString(e.BackgroundColor, "transparent"),
		"fillStyle": orString(e.FillStyle, "solid"), "strokeWidth": orNumber(e.StrokeWidth, 2),
		"strokeStyle": orString(e.StrokeStyle, "solid"), "roughness": roughness, "opacity": orNumber(e.Opacity, 100),
		"seed": randomInt(), "version": version, "versionNonce": nonce, "index": nil, "updated": now,
		"isDeleted": e.IsDeleted, "locked": e.Locked, "groupIds": groups, "frameId": frame, "boundElements": bound,
		"link": nil, "customData": nil, "roundness": nil,
	}
	switch e.Type {
	case "text":
		out["text"], out["originalText"] = *e.Text, *e.Text
		out["fontSize"] = orNumber(e.FontSize, 20)
		family := 1
		if e.FontFamily != nil {
			family = *e.FontFamily
		}
		out["fontFamily"] = family
		out["textAlign"] = orString(e.TextAlign, "left")
		out["verticalAlign"] = orString(e.VerticalAlign, "top")
		out["autoResize"], out["lineHeight"] = true, 1.25
		if e.ContainerID != nil {
			out["containerId"] = *e.ContainerID
		} else {
			out["containerId"] = nil
		}
	case "frame":
		if e.Name != nil {
			out["name"] = *e.Name
		} else {
			out["name"] = nil
		}
	case "line", "arrow":
		out["points"] = e.Points
		out["startBinding"], out["endBinding"] = nil, nil
		var start, end any
		if e.StartArrowhead != nil {
			start = *e.StartArrowhead
		}
		if e.EndArrowhead != nil {
			end = *e.EndArrowhead
		} else if e.Type == "arrow" {
			end = "arrow"
		}
		out["startArrowhead"], out["endArrowhead"] = start, end
		if e.Type == "arrow" {
			out["elbowed"] = false
		} else {
			out["polygon"] = false
		}
	}
	return out
}

// contentRequest is a locally validated change: its provider body and, for a patch, the version and nonce sent
// for every element.
type contentRequest struct {
	sceneID  string
	body     any
	sent     int
	versions map[string][2]int64
}

type sceneContentSent struct {
	SceneID      string   `json:"scene_id"`
	SceneVersion string   `json:"scene_version,omitempty"`
	Sent         int      `json:"sent"`
	ElementCount int      `json:"element_count"`
	NotApplied   []string `json:"not_applied,omitempty"`
}

type contentArgumentsIn struct {
	SceneID             string           `json:"scene_id"`
	Elements            []contentElement `json:"elements"`
	ViewBackgroundColor *string          `json:"view_background_color"`
}

// prepareContent validates a content change locally, before any secret is read or request is sent.
func prepareContent(resolved *config.Resolved, raw json.RawMessage, op string, patch bool) (*contentRequest, error) {
	var input contentArgumentsIn
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	if !validID(input.SceneID) {
		return nil, invalidRequest("scene_id is not a valid identifier")
	}
	limit := maxReplaceElements
	if patch {
		limit = maxPatchElements
	}
	if len(input.Elements) == 0 || len(input.Elements) > limit {
		return nil, invalidRequest("elements needs 1 to " + strconv.Itoa(limit) + " items")
	}
	if !patch && !validColor(input.ViewBackgroundColor) {
		return nil, invalidRequest("view_background_color is not a valid color")
	}
	now := time.Now().UnixMilli()
	request := &contentRequest{sceneID: input.SceneID, sent: len(input.Elements), versions: map[string][2]int64{}}
	wire := make([]map[string]any, 0, len(input.Elements))
	for i := range input.Elements {
		element := &input.Elements[i]
		if problem := element.check(patch); problem != "" {
			return nil, invalidRequest("elements[" + strconv.Itoa(i) + "]: " + problem)
		}
		if _, seen := request.versions[element.ID]; seen {
			return nil, invalidRequest("elements[" + strconv.Itoa(i) + "]: an id is used twice")
		}
		version, nonce := int64(1), randomInt()
		if patch {
			version = *element.ExpectedVersion + 1
		}
		request.versions[element.ID] = [2]int64{version, nonce}
		wire = append(wire, element.wire(version, nonce, now))
	}
	if patch {
		request.body = map[string]any{"elements": wire}
	} else {
		background := defaultBackground
		if input.ViewBackgroundColor != nil {
			background = *input.ViewBackgroundColor
		}
		request.body = map[string]any{"type": "excalidraw", "version": 2, "source": "qatlas-cli",
			"appState": map[string]any{"viewBackgroundColor": background}, "elements": wire, "files": map[string]any{}}
	}
	encoded, err := json.Marshal(request.body)
	if err != nil || len(encoded) > maxChangeBody {
		return nil, invalidRequest("the content change is larger than 1 MiB")
	}
	return request, nil
}

type answerElement struct {
	ID           any   `json:"id"`
	Version      int64 `json:"version"`
	VersionNonce int64 `json:"versionNonce"`
	IsDeleted    bool  `json:"isDeleted"`
}

type contentAnswer struct {
	SceneVersion string          `json:"sceneVersion"`
	Elements     []answerElement `json:"elements"`
}

func (c *Client) changeContent(ctx context.Context, op, method string, request *contentRequest) (any, error) {
	if _, err := c.boundScene(ctx, op, request.sceneID); err != nil {
		return nil, err
	}
	var answer contentAnswer
	if err := c.sendBounded(ctx, op, method, "/scenes/"+url.PathEscape(request.sceneID)+"/content", request.body,
		&answer, maxContentBytes); err != nil {
		return nil, err
	}
	result := &sceneContentSent{SceneID: request.sceneID, SceneVersion: boundedValue(answer.SceneVersion),
		Sent: request.sent}
	shown := map[string]answerElement{}
	for _, element := range answer.Elements {
		id, _ := element.ID.(string)
		shown[id] = element
		if !element.IsDeleted {
			result.ElementCount++
		}
	}
	if method == http.MethodPatch {
		for id, sent := range request.versions {
			got, ok := shown[id]
			if !ok || got.Version != sent[0] || got.VersionNonce != sent[1] {
				result.NotApplied = append(result.NotApplied, id)
			}
		}
		sort.Strings(result.NotApplied)
	}
	return result, nil
}

func invokeContentPatch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "patch scene content"
	request, err := prepareContent(resolved, raw, op, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.changeContent(ctx, op, http.MethodPatch, request)
}

func invokeContentReplace(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "replace scene content"
	request, err := prepareContent(resolved, raw, op, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.changeContent(ctx, op, http.MethodPut, request)
}
