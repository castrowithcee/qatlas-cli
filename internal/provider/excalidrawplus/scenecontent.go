package excalidrawplus

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	// maxContentList bounds every list taken from a scene's content; a longer one is left out and marked
	// instead of being cut, so a partial list is never mistaken for the whole.
	maxContentList  = 1000
	maxContentFiles = 200
	// The free-form customData is bounded in nesting, in members and elements in total, and in size.
	maxCustomDepth   = 6
	maxCustomEntries = 100
	maxCustomBytes   = 4 << 10
)

// cleanBounded bounds a foreign text to max bytes and replaces control characters.
func cleanBounded(value string, max int) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, bounded(value, max)))
}

func has(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// limitedTextOf reads a text member that Excalidraw+ may send as anything; cleaned and capped at maxElementText.
func limitedTextOf(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return cleanBounded(text, maxElementText)
}

// intOf keeps an integer member exactly; any other type or a fraction drops the member.
func intOf(raw json.RawMessage) number {
	text := string(bytes.TrimSpace(raw))
	if _, err := strconv.ParseInt(text, 10, 64); err != nil {
		return ""
	}
	return number(text)
}

func floatOf(raw json.RawMessage) *float64 {
	var value float64
	if !has(raw) || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

// listOf decodes a list member. over reports a list above the bound, which is left out; a member of another
// type is dropped without a marker.
func listOf[T any](raw json.RawMessage) (list []T, over bool) {
	if !has(raw) || json.Unmarshal(raw, &list) != nil {
		return nil, false
	}
	if len(list) > maxContentList {
		return nil, true
	}
	return list, false
}

// Roundness is the corner rounding of an element.
type Roundness struct {
	Type  number   `json:"type,omitempty"`
	Value *float64 `json:"value,omitempty"`
}

func roundnessOf(raw json.RawMessage) *Roundness {
	var in struct {
		Type  json.RawMessage `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if !has(raw) || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := Roundness{Type: intOf(in.Type), Value: floatOf(in.Value)}
	if out == (Roundness{}) {
		return nil
	}
	return &out
}

// BoundElement links an element to a bound arrow or text.
type BoundElement struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
}

// Binding attaches an arrow end to an element.
type Binding struct {
	ElementID  string     `json:"element_id,omitempty"`
	FixedPoint [2]float64 `json:"fixed_point"`
	HasPoint   bool       `json:"-"`
	Mode       string     `json:"mode,omitempty"`
}

// MarshalJSON leaves fixed_point out when the answer had none.
func (b Binding) MarshalJSON() ([]byte, error) {
	out := struct {
		ElementID  string      `json:"element_id,omitempty"`
		FixedPoint *[2]float64 `json:"fixed_point,omitempty"`
		Mode       string      `json:"mode,omitempty"`
	}{ElementID: b.ElementID, Mode: b.Mode}
	if b.HasPoint {
		out.FixedPoint = &b.FixedPoint
	}
	return json.Marshal(out)
}

func pointOf(raw json.RawMessage) (point [2]float64, ok bool) {
	var values []float64
	if !has(raw) || json.Unmarshal(raw, &values) != nil || len(values) != 2 {
		return point, false
	}
	return [2]float64{values[0], values[1]}, true
}

func bindingOf(raw json.RawMessage) *Binding {
	var in struct {
		ElementID  json.RawMessage `json:"elementId"`
		FixedPoint json.RawMessage `json:"fixedPoint"`
		Mode       json.RawMessage `json:"mode"`
	}
	if !has(raw) || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := Binding{ElementID: limitedTextOf(in.ElementID), Mode: textOf(in.Mode)}
	out.FixedPoint, out.HasPoint = pointOf(in.FixedPoint)
	if out.ElementID == "" && out.Mode == "" && !out.HasPoint {
		return nil
	}
	return &out
}

// Crop is the visible part of an image element.
type Crop struct {
	X             *float64 `json:"x,omitempty"`
	Y             *float64 `json:"y,omitempty"`
	Width         *float64 `json:"width,omitempty"`
	Height        *float64 `json:"height,omitempty"`
	NaturalWidth  *float64 `json:"natural_width,omitempty"`
	NaturalHeight *float64 `json:"natural_height,omitempty"`
}

func cropOf(raw json.RawMessage) *Crop {
	var in struct {
		X, Y, Width, Height, NaturalWidth, NaturalHeight json.RawMessage
	}
	if !has(raw) || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := Crop{floatOf(in.X), floatOf(in.Y), floatOf(in.Width), floatOf(in.Height), floatOf(in.NaturalWidth),
		floatOf(in.NaturalHeight)}
	if out == (Crop{}) {
		return nil
	}
	return &out
}

// FixedSegment pins one segment of an elbow arrow.
type FixedSegment struct {
	Start *[2]float64 `json:"start,omitempty"`
	End   *[2]float64 `json:"end,omitempty"`
	Index number      `json:"index,omitempty"`
}

func fixedSegmentsOf(raw json.RawMessage) (out []FixedSegment, over bool) {
	items, over := listOf[struct{ Start, End, Index json.RawMessage }](raw)
	for _, item := range items {
		segment := FixedSegment{Index: intOf(item.Index)}
		if p, ok := pointOf(item.Start); ok {
			segment.Start = &p
		}
		if p, ok := pointOf(item.End); ok {
			segment.End = &p
		}
		if segment != (FixedSegment{}) {
			out = append(out, segment)
		}
	}
	return out, over
}

func boundElementsOf(raw json.RawMessage) (out []BoundElement, over bool) {
	items, over := listOf[struct{ ID, Type json.RawMessage }](raw)
	for _, item := range items {
		if bound := (BoundElement{ID: limitedTextOf(item.ID), Type: textOf(item.Type)}); bound != (BoundElement{}) {
			out = append(out, bound)
		}
	}
	return out, over
}

func groupIDsOf(raw json.RawMessage) (out []string, over bool) {
	items, over := listOf[string](raw)
	for _, item := range items {
		if id := cleanBounded(item, maxElementText); id != "" {
			out = append(out, id)
		}
	}
	return out, over
}

func pointsOf(raw json.RawMessage) (out [][2]float64, over bool) {
	items, over := listOf[json.RawMessage](raw)
	for _, item := range items {
		point, ok := pointOf(item)
		if !ok {
			return nil, false
		}
		out = append(out, point)
	}
	return out, over
}

func scaleOf(raw json.RawMessage) *[2]float64 {
	if point, ok := pointOf(raw); ok {
		return &point
	}
	return nil
}

// customDataOf keeps the object exactly as delivered when it stays within the bounds; otherwise it is left out
// and over is set.
func customDataOf(raw json.RawMessage) (data json.RawMessage, over bool) {
	if !has(raw) {
		return nil, false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(compact.Bytes()))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, false
	}
	entries := 0
	var within func(value any, depth int) bool
	within = func(value any, depth int) bool {
		switch v := value.(type) {
		case map[string]any:
			if depth > maxCustomDepth {
				return false
			}
			entries += len(v)
			for _, inner := range v {
				if entries > maxCustomEntries || !within(inner, depth+1) {
					return false
				}
			}
		case []any:
			if depth > maxCustomDepth {
				return false
			}
			entries += len(v)
			for _, inner := range v {
				if entries > maxCustomEntries || !within(inner, depth+1) {
					return false
				}
			}
		}
		return entries <= maxCustomEntries
	}
	if compact.Len() > maxCustomBytes || !within(value, 1) {
		return nil, true
	}
	return json.RawMessage(compact.Bytes()), false
}

// elementDetails is the part of an element beyond the compact view; every member is optional.
type elementDetails struct {
	Angle           *float64        `json:"angle,omitempty"`
	StrokeColor     string          `json:"stroke_color,omitempty"`
	BackgroundColor string          `json:"background_color,omitempty"`
	FillStyle       string          `json:"fill_style,omitempty"`
	StrokeWidth     *float64        `json:"stroke_width,omitempty"`
	StrokeStyle     string          `json:"stroke_style,omitempty"`
	Roundness       *Roundness      `json:"roundness,omitempty"`
	Roughness       *float64        `json:"roughness,omitempty"`
	Opacity         *float64        `json:"opacity,omitempty"`
	Seed            number          `json:"seed,omitempty"`
	VersionNonce    number          `json:"version_nonce,omitempty"`
	Index           string          `json:"index,omitempty"`
	Updated         number          `json:"updated,omitempty"`
	Locked          *bool           `json:"locked,omitempty"`
	GroupIDs        []string        `json:"group_ids,omitempty"`
	BoundElements   []BoundElement  `json:"bound_elements,omitempty"`
	Link            string          `json:"link,omitempty"`
	CustomData      json.RawMessage `json:"custom_data,omitempty"`

	FontSize      *float64 `json:"font_size,omitempty"`
	FontFamily    number   `json:"font_family,omitempty"`
	TextAlign     string   `json:"text_align,omitempty"`
	VerticalAlign string   `json:"vertical_align,omitempty"`
	OriginalText  string   `json:"original_text,omitempty"`
	AutoResize    *bool    `json:"auto_resize,omitempty"`
	LineHeight    *float64 `json:"line_height,omitempty"`

	FileID string      `json:"file_id,omitempty"`
	Status string      `json:"status,omitempty"`
	Scale  *[2]float64 `json:"scale,omitempty"`
	Crop   *Crop       `json:"crop,omitempty"`

	Points           [][2]float64   `json:"points,omitempty"`
	StartBinding     *Binding       `json:"start_binding,omitempty"`
	EndBinding       *Binding       `json:"end_binding,omitempty"`
	StartArrowhead   string         `json:"start_arrowhead,omitempty"`
	EndArrowhead     string         `json:"end_arrowhead,omitempty"`
	Polygon          *bool          `json:"polygon,omitempty"`
	Elbowed          *bool          `json:"elbowed,omitempty"`
	FixedSegments    []FixedSegment `json:"fixed_segments,omitempty"`
	StartIsSpecial   *bool          `json:"start_is_special,omitempty"`
	EndIsSpecial     *bool          `json:"end_is_special,omitempty"`
	Pressures        []float64      `json:"pressures,omitempty"`
	SimulatePressure *bool          `json:"simulate_pressure,omitempty"`

	CustomDataOmitted    bool `json:"custom_data_omitted,omitempty"`
	GroupIDsOmitted      bool `json:"group_ids_omitted,omitempty"`
	BoundElementsOmitted bool `json:"bound_elements_omitted,omitempty"`
	PointsOmitted        bool `json:"points_omitted,omitempty"`
	FixedSegmentsOmitted bool `json:"fixed_segments_omitted,omitempty"`
	PressuresOmitted     bool `json:"pressures_omitted,omitempty"`
}

// elementRaw holds the members read on their own, so one of an unexpected type drops only itself.
type elementRaw struct {
	Angle, StrokeColor, BackgroundColor, FillStyle, StrokeWidth, StrokeStyle, Roundness, Roughness, Opacity,
	Seed, VersionNonce, Index, Updated, Locked, GroupIDs, BoundElements, Link, CustomData json.RawMessage

	FontSize, FontFamily, TextAlign, VerticalAlign, OriginalText, AutoResize, LineHeight json.RawMessage

	FileID, Status, Scale, Crop json.RawMessage

	Points, StartBinding, EndBinding, StartArrowhead, EndArrowhead, Polygon, Elbowed, FixedSegments,
	StartIsSpecial, EndIsSpecial, Pressures, SimulatePressure json.RawMessage
}

func elementDetailsOf(r *elementRaw) elementDetails {
	d := elementDetails{Angle: floatOf(r.Angle), StrokeColor: textOf(r.StrokeColor),
		BackgroundColor: textOf(r.BackgroundColor), FillStyle: textOf(r.FillStyle),
		StrokeWidth: floatOf(r.StrokeWidth), StrokeStyle: textOf(r.StrokeStyle), Roundness: roundnessOf(r.Roundness),
		Roughness: floatOf(r.Roughness), Opacity: floatOf(r.Opacity), Seed: intOf(r.Seed),
		VersionNonce: intOf(r.VersionNonce), Index: textOf(r.Index), Updated: intOf(r.Updated),
		Locked: flagOf(r.Locked), Link: limitedTextOf(r.Link),
		FontSize: floatOf(r.FontSize), FontFamily: intOf(r.FontFamily), TextAlign: textOf(r.TextAlign),
		VerticalAlign: textOf(r.VerticalAlign), OriginalText: boundedTextOf(r.OriginalText),
		AutoResize: flagOf(r.AutoResize), LineHeight: floatOf(r.LineHeight),
		FileID: limitedTextOf(r.FileID), Status: textOf(r.Status), Scale: scaleOf(r.Scale), Crop: cropOf(r.Crop),
		StartBinding: bindingOf(r.StartBinding), EndBinding: bindingOf(r.EndBinding),
		StartArrowhead: textOf(r.StartArrowhead), EndArrowhead: textOf(r.EndArrowhead),
		Polygon: flagOf(r.Polygon), Elbowed: flagOf(r.Elbowed), StartIsSpecial: flagOf(r.StartIsSpecial),
		EndIsSpecial: flagOf(r.EndIsSpecial), SimulatePressure: flagOf(r.SimulatePressure)}
	d.CustomData, d.CustomDataOmitted = customDataOf(r.CustomData)
	d.GroupIDs, d.GroupIDsOmitted = groupIDsOf(r.GroupIDs)
	d.BoundElements, d.BoundElementsOmitted = boundElementsOf(r.BoundElements)
	d.Points, d.PointsOmitted = pointsOf(r.Points)
	d.FixedSegments, d.FixedSegmentsOmitted = fixedSegmentsOf(r.FixedSegments)
	d.Pressures, d.PressuresOmitted = listOf[float64](r.Pressures)
	return d
}

// boundedTextOf reads a text member like the element's own text: capped, with line breaks kept.
func boundedTextOf(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return bounded(text, maxElementText)
}

// FileMeta describes one embedded file; its content is never passed on.
type FileMeta struct {
	ID            string `json:"id,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	Created       number `json:"created,omitempty"`
	LastRetrieved number `json:"last_retrieved,omitempty"`
	Version       number `json:"version,omitempty"`
}

func filesOf(files map[string]json.RawMessage) (out []FileMeta, over bool) {
	if len(files) > maxContentFiles {
		return nil, true
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var in struct{ ID, MimeType, Created, LastRetrieved, Version json.RawMessage }
		if json.Unmarshal(files[key], &in) != nil {
			continue
		}
		meta := FileMeta{ID: limitedTextOf(in.ID), MimeType: textOf(in.MimeType), Created: intOf(in.Created),
			LastRetrieved: intOf(in.LastRetrieved), Version: intOf(in.Version)}
		if meta.ID == "" {
			meta.ID = cleanBounded(key, maxElementText)
		}
		out = append(out, meta)
	}
	return out, false
}

// lockedSelectionsOf reads the map of locked group selections; an entry that is not a boolean is dropped, and
// a map above the bound or with an over-long key is left out and marked.
func lockedSelectionsOf(raw json.RawMessage) (out map[string]bool, over bool) {
	var in map[string]json.RawMessage
	if !has(raw) || json.Unmarshal(raw, &in) != nil {
		return nil, false
	}
	if len(in) > maxContentList {
		return nil, true
	}
	for key, value := range in {
		if len(key) > maxElementText {
			return nil, true
		}
		if flag := flagOf(value); flag != nil {
			if out == nil {
				out = map[string]bool{}
			}
			out[key] = *flag
		}
	}
	return out, false
}

func failedFilesOf(raw json.RawMessage) (out []string, over bool) {
	items, over := listOf[string](raw)
	for _, item := range items {
		if id := cleanBounded(item, maxElementText); id != "" {
			out = append(out, id)
		}
	}
	return out, over
}

const (
	pairSchema    = `{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2}`
	pointSchema   = pairSchema
	bindingSchema = `{"type":"object","properties":{"element_id":{"type":"string"},"fixed_point":` + pairSchema +
		`,"mode":{"type":"string"}},"additionalProperties":false}`
	elementSchema = `{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string"},` +
		`"text":{"type":"string"},"name":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},` +
		`"width":{"type":"number"},"height":{"type":"number"},"version":{"type":"integer"},"frame_id":{"type":"string"},` +
		`"container_id":{"type":"string"},` +
		`"angle":{"type":"number"},"stroke_color":{"type":"string"},"background_color":{"type":"string"},` +
		`"fill_style":{"type":"string"},"stroke_width":{"type":"number"},"stroke_style":{"type":"string"},` +
		`"roundness":{"type":"object","properties":{"type":{"type":"integer"},"value":{"type":"number"}},` +
		`"additionalProperties":false},` +
		`"roughness":{"type":"number"},"opacity":{"type":"number"},"seed":{"type":"integer"},` +
		`"version_nonce":{"type":"integer"},"index":{"type":"string"},"updated":{"type":"integer"},` +
		`"locked":{"type":"boolean"},"group_ids":{"type":"array","items":{"type":"string"}},` +
		`"bound_elements":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"type":{"type":"string"}},"additionalProperties":false}},` +
		`"link":{"type":"string"},"custom_data":{"type":"object","additionalProperties":true},` +
		`"font_size":{"type":"number"},"font_family":{"type":"integer"},"text_align":{"type":"string"},` +
		`"vertical_align":{"type":"string"},"original_text":{"type":"string"},"auto_resize":{"type":"boolean"},` +
		`"line_height":{"type":"number"},` +
		`"file_id":{"type":"string"},"status":{"type":"string"},"scale":` + pairSchema + `,` +
		`"crop":{"type":"object","properties":{"x":{"type":"number"},"y":{"type":"number"},` +
		`"width":{"type":"number"},"height":{"type":"number"},"natural_width":{"type":"number"},` +
		`"natural_height":{"type":"number"}},"additionalProperties":false},` +
		`"points":{"type":"array","items":` + pointSchema + `},"start_binding":` + bindingSchema + `,` +
		`"end_binding":` + bindingSchema + `,"start_arrowhead":{"type":"string"},"end_arrowhead":{"type":"string"},` +
		`"polygon":{"type":"boolean"},"elbowed":{"type":"boolean"},` +
		`"fixed_segments":{"type":"array","items":{"type":"object","properties":{"start":` + pairSchema + `,` +
		`"end":` + pairSchema + `,"index":{"type":"integer"}},"additionalProperties":false}},` +
		`"start_is_special":{"type":"boolean"},"end_is_special":{"type":"boolean"},` +
		`"pressures":{"type":"array","items":{"type":"number"}},"simulate_pressure":{"type":"boolean"},` +
		`"custom_data_omitted":{"type":"boolean"},"group_ids_omitted":{"type":"boolean"},` +
		`"bound_elements_omitted":{"type":"boolean"},"points_omitted":{"type":"boolean"},` +
		`"fixed_segments_omitted":{"type":"boolean"},"pressures_omitted":{"type":"boolean"}},` +
		`"required":["id","type"],"additionalProperties":false}`
	fileSchema = `{"type":"object","properties":{"id":{"type":"string"},"mime_type":{"type":"string"},` +
		`"created":{"type":"integer"},"last_retrieved":{"type":"integer"},"version":{"type":"integer"}},` +
		`"additionalProperties":false}`
)

var contentSceneFields = []fieldDoc{
	{"type", "Scene: content type, as Excalidraw+ reports it; Element: its type; untrusted data"},
	{"version", "Scene: content format version; Element: its version"},
	{"source", "Origin of the scene content, untrusted data and capped"},
	{"locked_multi_selections", "Locked multi-selections of the scene, group identifier to boolean"},
	{"locked_multi_selections_omitted", "True when the locked multi-selections exceed the bound and are left out"},
	{"files_failed_to_embed", "Identifiers of files Excalidraw+ failed to embed, untrusted data"},
	{"files_failed_to_embed_omitted", "True when the list exceeds the bound and is left out"},
	{"files", "Metadata of the embedded files; the file content is never returned"},
	{"files_omitted", "True when the scene has more files than the bound allows and the list is left out"},
}

var contentElementFields = []fieldDoc{
	{"id", "Element: identifier"}, {"text", "Element: text; untrusted data and capped"},
	{"name", "Frame: name; untrusted data and capped"}, {"x", "Element: horizontal position"},
	{"y", "Element: vertical position"}, {"width", "Element: width"}, {"height", "Element: height"},
	{"frame_id", "Element: identifier of the frame it belongs to"},
	{"container_id", "Text: identifier of the container it is bound to"},
	{"angle", "Rotation in radians"}, {"stroke_color", "Stroke color"}, {"background_color", "Background color"},
	{"fill_style", "Fill style"}, {"stroke_width", "Stroke width"}, {"stroke_style", "Stroke style"},
	{"roundness", "Corner rounding: type and value"}, {"roughness", "Roughness"}, {"opacity", "Opacity, 0 to 100"},
	{"seed", "Random seed of the hand-drawn look"}, {"version_nonce", "Nonce that breaks version ties"},
	{"index", "Fractional ordering key, opaque"}, {"updated", "Last update time in epoch milliseconds"},
	{"locked", "Whether the element is locked"},
	{"group_ids", "Groups the element belongs to, deepest first; untrusted data"},
	{"bound_elements", "Arrows and texts bound to the element: id and type"},
	{"link", "Address attached to the element; untrusted data, capped, never fetched"},
	{"custom_data", "Free-form data as Excalidraw+ delivers it, with its keys unchanged; untrusted data"},
	{"font_size", "Text: font size"}, {"font_family", "Text: numeric font family"},
	{"text_align", "Text: horizontal alignment"}, {"vertical_align", "Text: vertical alignment"},
	{"original_text", "Text: text before wrapping; untrusted data and capped"},
	{"auto_resize", "Text: whether the box resizes with the text"}, {"line_height", "Text: unitless line height"},
	{"file_id", "Image: identifier of the file in files"}, {"status", "Image: pending, saved, or error"},
	{"scale", "Image: horizontal and vertical scale, used for flipping"},
	{"crop", "Image: visible part, with natural size"},
	{"points", "Line, arrow, freedraw: points relative to the element position"},
	{"start_binding", "Line, arrow: binding of the start: element_id, fixed_point, mode"},
	{"end_binding", "Line, arrow: binding of the end: element_id, fixed_point, mode"},
	{"start_arrowhead", "Line, arrow: start arrowhead"}, {"end_arrowhead", "Line, arrow: end arrowhead"},
	{"polygon", "Line: whether it is a closed polygon"}, {"elbowed", "Arrow: whether it is elbowed"},
	{"fixed_segments", "Elbow arrow: fixed segments with start, end, and index"},
	{"start_is_special", "Elbow arrow: start is special"}, {"end_is_special", "Elbow arrow: end is special"},
	{"pressures", "Freedraw: pressure per point"}, {"simulate_pressure", "Freedraw: whether pressure is simulated"},
	{"custom_data_omitted", "True when custom_data exceeds its bounds (depth 6, 100 entries, 4 KiB) and is left out"},
	{"group_ids_omitted", "True when group_ids exceeds 1000 entries and is left out"},
	{"bound_elements_omitted", "True when bound_elements exceeds 1000 entries and is left out"},
	{"points_omitted", "True when points exceeds 1000 entries and is left out"},
	{"fixed_segments_omitted", "True when fixed_segments exceeds 1000 entries and is left out"},
	{"pressures_omitted", "True when pressures exceeds 1000 entries and is left out"},
}

type fieldDoc struct{ name, description string }

func docFields(docs []fieldDoc) []capability.Field {
	fields := make([]capability.Field, 0, len(docs))
	for _, doc := range docs {
		fields = append(fields, capability.Field{Name: doc.name, Description: doc.description})
	}
	return fields
}

var contentMarkerFields []capability.Field
