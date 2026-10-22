package excalidrawplus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const richContent = `{"type":"excalidraw","version":2,"source":"https://plus.example/\u0007src",
"appState":{"viewBackgroundColor":"#fff","lockedMultiSelections":{"g1":true,"g2":false,"bad":"x"}},
"sceneVersion":"7","filesFailedToEmbed":["f9"],
"elements":[
{"id":"r1","type":"rectangle","x":1,"y":2,"width":3,"height":4,"angle":0.5,"strokeColor":"#1e1e1e",
 "backgroundColor":"transparent","fillStyle":"solid","strokeWidth":2,"strokeStyle":"dashed",
 "roundness":{"type":3},"roughness":1,"opacity":100,"seed":9007199254740993,"version":4,
 "versionNonce":1234567890123,"index":"a0","updated":1700000000123,"isDeleted":false,"locked":false,
 "groupIds":["g1","g0"],"boundElements":[{"id":"t1","type":"text"},{"id":"a1","type":"arrow"}],
 "link":"https://example.invalid/x\u0007y","customData":{"k":{"n":1.50,"l":[1,2]},"Z":"v"},"frameId":"fr1"},
{"id":"fr1","type":"frame","name":"Frame","x":0,"y":0,"width":9,"height":9,"roundness":{"type":2,"value":8}},
{"id":"t1","type":"text","text":"Hi","originalText":"Hi\nthere","fontSize":20,"fontFamily":5,"textAlign":"center",
 "verticalAlign":"middle","containerId":"r1","autoResize":true,"lineHeight":1.25},
{"id":"i1","type":"image","fileId":"f1","status":"saved","scale":[-1,1],
 "crop":{"x":1,"y":2,"width":3,"height":4,"naturalWidth":30,"naturalHeight":40}},
{"id":"l1","type":"line","points":[[0,0],[10,5.5]],"polygon":true,"startArrowhead":null,"endArrowhead":"bar"},
{"id":"a1","type":"arrow","points":[[0,0],[10,10]],"elbowed":true,"startIsSpecial":false,"endIsSpecial":true,
 "startBinding":{"elementId":"r1","fixedPoint":[0.5,1],"mode":"orbit"},"endBinding":{"elementId":"i1","mode":"inside"},
 "endArrowhead":"triangle","fixedSegments":[{"start":[1,2],"end":[3,4],"index":2}]},
{"id":"d1","type":"freedraw","points":[[0,0],[1,1]],"pressures":[0.5,0.7],"simulatePressure":false},
{"id":"x1","type":"text","text":"gone","isDeleted":true,"strokeColor":"#f00"}],
"files":{"f1":{"id":"f1","mimeType":"image/png","created":1700000000001,"lastRetrieved":1700000000002,"version":3,
 "dataURL":"data:image/png;base64,CANARY"}}}`

func contentEnv(t *testing.T, body string) (*environment, *[]call) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			return jsonResponse(200, body), nil
		}
		return jsonResponse(200, entryJSON("s-own", "n", ownCollection)), nil
	})
	return env, &calls
}

func TestSceneContentCarriesAllFields(t *testing.T) {
	env, _ := contentEnv(t, richContent)
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "CANARY") || strings.Contains(result, "dataURL") || strings.Contains(result, "gone") ||
		strings.Contains(result, "data_url") {
		t.Fatalf("result leaks file content or a deleted element: %s", result)
	}
	out := decode(t, result)
	wantScene := map[string]any{"type": "excalidraw", "version": json.Number("2"), "source": "https://plus.example/ src",
		"locked_multi_selections": map[string]any{"g1": true, "g2": false}, "files_failed_to_embed": []any{"f9"},
		"files": []any{map[string]any{"id": "f1", "mime_type": "image/png", "created": json.Number("1700000000001"),
			"last_retrieved": json.Number("1700000000002"), "version": json.Number("3")}}}
	for key, want := range wantScene {
		if !reflect.DeepEqual(out[key], want) {
			t.Errorf("%s = %#v, want %#v", key, out[key], want)
		}
	}
	elements := map[string]map[string]any{}
	for _, e := range out["elements"].([]any) {
		m := e.(map[string]any)
		elements[m["id"].(string)] = m
	}
	if len(elements) != 7 {
		t.Fatalf("elements = %d, want 7", len(elements))
	}
	n := func(s string) json.Number { return json.Number(s) }
	wantRect := map[string]any{"id": "r1", "type": "rectangle", "x": n("1"), "y": n("2"), "width": n("3"),
		"height": n("4"), "version": n("4"), "frame_id": "fr1", "angle": n("0.5"), "stroke_color": "#1e1e1e",
		"background_color": "transparent", "fill_style": "solid", "stroke_width": n("2"), "stroke_style": "dashed",
		"roundness": map[string]any{"type": n("3")}, "roughness": n("1"), "opacity": n("100"),
		"seed": n("9007199254740993"), "version_nonce": n("1234567890123"), "index": "a0",
		"updated": n("1700000000123"), "locked": false, "group_ids": []any{"g1", "g0"},
		"bound_elements": []any{map[string]any{"id": "t1", "type": "text"}, map[string]any{"id": "a1", "type": "arrow"}},
		"link":           "https://example.invalid/x y", "custom_data": map[string]any{"k": map[string]any{"n": n("1.50"),
			"l": []any{n("1"), n("2")}}, "Z": "v"}}
	if !reflect.DeepEqual(elements["r1"], wantRect) {
		t.Errorf("rectangle = %#v\nwant %#v", elements["r1"], wantRect)
	}
	if got := elements["fr1"]["roundness"]; !reflect.DeepEqual(got, map[string]any{"type": n("2"), "value": n("8")}) ||
		elements["fr1"]["name"] != "Frame" {
		t.Errorf("frame = %#v", elements["fr1"])
	}
	text := elements["t1"]
	if text["original_text"] != "Hi\nthere" || text["font_family"] != n("5") || text["font_size"] != n("20") ||
		text["text_align"] != "center" || text["vertical_align"] != "middle" || text["auto_resize"] != true ||
		text["line_height"] != n("1.25") || text["container_id"] != "r1" {
		t.Errorf("text = %#v", text)
	}
	image := elements["i1"]
	if image["file_id"] != "f1" || image["status"] != "saved" || !reflect.DeepEqual(image["scale"], []any{n("-1"), n("1")}) ||
		!reflect.DeepEqual(image["crop"], map[string]any{"x": n("1"), "y": n("2"), "width": n("3"), "height": n("4"),
			"natural_width": n("30"), "natural_height": n("40")}) {
		t.Errorf("image = %#v", image)
	}
	line := elements["l1"]
	if line["polygon"] != true || line["end_arrowhead"] != "bar" || line["start_arrowhead"] != nil ||
		!reflect.DeepEqual(line["points"], []any{[]any{n("0"), n("0")}, []any{n("10"), n("5.5")}}) {
		t.Errorf("line = %#v", line)
	}
	arrow := elements["a1"]
	wantStart := map[string]any{"element_id": "r1", "fixed_point": []any{n("0.5"), n("1")}, "mode": "orbit"}
	wantEnd := map[string]any{"element_id": "i1", "mode": "inside"}
	if arrow["elbowed"] != true || arrow["start_is_special"] != false || arrow["end_is_special"] != true ||
		!reflect.DeepEqual(arrow["start_binding"], wantStart) || !reflect.DeepEqual(arrow["end_binding"], wantEnd) ||
		arrow["end_arrowhead"] != "triangle" ||
		!reflect.DeepEqual(arrow["fixed_segments"], []any{map[string]any{"start": []any{n("1"), n("2")},
			"end": []any{n("3"), n("4")}, "index": n("2")}}) {
		t.Errorf("arrow = %#v", arrow)
	}
	free := elements["d1"]
	if free["simulate_pressure"] != false || !reflect.DeepEqual(free["pressures"], []any{n("0.5"), n("0.7")}) {
		t.Errorf("freedraw = %#v", free)
	}
}

// Every member the answer outputs, at any depth, is declared in the output schema; the top-level and element
// members are also described in Fields.
func TestSceneContentMembersAreDeclared(t *testing.T) {
	env, _ := contentEnv(t, richContent)
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range scenesContent.Fields {
		fields[f.Name] = true
	}
	var walk func(path string, value any, schema json.RawMessage)
	walk = func(path string, value any, schema json.RawMessage) {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Items      json.RawMessage            `json:"items"`
			Additional json.RawMessage            `json:"additionalProperties"`
		}
		_ = json.Unmarshal(schema, &s)
		switch v := value.(type) {
		case map[string]any:
			if len(s.Properties) == 0 {
				return // free-keyed object: custom_data, locked_multi_selections
			}
			for key, inner := range v {
				property, ok := s.Properties[key]
				if !ok {
					t.Errorf("%s.%s not in the output schema", path, key)
					continue
				}
				if (path == "" || path == "elements[]") && !fields[key] {
					t.Errorf("%s.%s not in Fields", path, key)
				}
				walk(strings.TrimPrefix(path+"."+key, "."), inner, property)
			}
		case []any:
			for _, item := range v {
				walk(path+"[]", item, s.Items)
			}
		}
	}
	walk("", decode(t, result), scenesContent.OutputSchema)
	// Marker members are declared as well.
	props := schemaProperties(t, scenesContent.OutputSchema)
	items := schemaProperties(t, mustItems(t, props["elements"]))
	for _, name := range []string{"custom_data_omitted", "group_ids_omitted", "bound_elements_omitted",
		"points_omitted", "fixed_segments_omitted", "pressures_omitted"} {
		if _, ok := items[name]; !ok || !fields[name] {
			t.Errorf("marker %s is not declared", name)
		}
	}
	for _, name := range []string{"locked_multi_selections_omitted", "files_failed_to_embed_omitted", "files_omitted"} {
		if _, ok := props[name]; !ok || !fields[name] {
			t.Errorf("marker %s is not declared", name)
		}
	}
}

func mustItems(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var array struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &array); err != nil {
		t.Fatal(err)
	}
	return array.Items
}

// An answer without the new members yields the former output.
func TestSceneContentWithoutNewFieldsKeepsFormerOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(map[string]string{"s-own": ownCollection}, &calls))
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var got, exp map[string]any
	_ = json.Unmarshal([]byte(result), &got)
	_ = json.Unmarshal([]byte(`{"scene_id":"s-own","scene_version":"7","background":"#fff","element_count":4,`+
		`"matched":4,"offset":0,"returned":2,"truncated":true,"next_offset":2,"files_count":1,"type":"excalidraw",`+
		`"version":2,"files":[{"id":"f1"}],"elements":[`+
		`{"id":"e1","type":"text","text":"Roadmap Q4","x":1,"y":2,"width":3,"height":4},`+
		`{"id":"e2","type":"rectangle","x":5,"y":6,"width":7,"height":8}]}`), &exp)
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("result = %s", result)
	}
	// A bare answer has exactly the former members.
	env, _ = contentEnv(t, `{"sceneVersion":"7","appState":{"viewBackgroundColor":"#fff"},"elements":[`+
		`{"id":"e1","type":"text","text":"A","x":1,"y":2,"width":3,"height":4,"version":2}]}`)
	result, err = env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	var bare map[string]any
	_ = json.Unmarshal([]byte(result), &bare)
	wantBare := map[string]any{"scene_id": "s-own", "scene_version": "7", "background": "#fff", "element_count": 1.0,
		"matched": 1.0, "offset": 0.0, "returned": 1.0, "truncated": false, "elements": []any{map[string]any{
			"id": "e1", "type": "text", "text": "A", "x": 1.0, "y": 2.0, "width": 3.0, "height": 4.0, "version": 2.0}}}
	if !reflect.DeepEqual(bare, wantBare) {
		t.Fatalf("result = %s", result)
	}
}

func TestSceneContentOverBoundMembersAreLeftOutAndMarked(t *testing.T) {
	points := "[" + strings.TrimSuffix(strings.Repeat("[0,0],", maxContentList+1), ",") + "]"
	pressures := "[" + strings.TrimSuffix(strings.Repeat("0.5,", maxContentList+1), ",") + "]"
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":1}}}}}}}`
	var wide []string
	for i := 0; i < maxCustomEntries+1; i++ {
		wide = append(wide, fmt.Sprintf(`"k%d":1`, i))
	}
	var manyKeys []string
	for i := 0; i <= maxContentList; i++ {
		manyKeys = append(manyKeys, fmt.Sprintf(`"k%d":true`, i))
	}
	big := fmt.Sprintf(`{"k":%q}`, strings.Repeat("x", maxCustomBytes))
	files := []string{}
	for i := 0; i <= maxContentFiles; i++ {
		files = append(files, fmt.Sprintf(`"f%d":{"id":"f%d"}`, i, i))
	}
	ids := "[" + strings.TrimSuffix(strings.Repeat(`"g",`, maxContentList+1), ",") + "]"
	body := `{"elements":[{"id":"p","type":"freedraw","points":` + points + `,"pressures":` + pressures +
		`,"groupIds":` + ids + `,"boundElements":` + strings.ReplaceAll(points, "[0,0]", `{"id":"b"}`) +
		`,"fixedSegments":` + strings.ReplaceAll(points, "[0,0]", `{"index":1}`) + `},
{"id":"c1","type":"rectangle","customData":` + deep + `},
{"id":"c2","type":"rectangle","customData":{` + strings.Join(wide, ",") + `}},
{"id":"c3","type":"rectangle","customData":` + big + `},
{"id":"c4","type":"rectangle","customData":{"ok":[1,{"x":2}]}}],
"appState":{"lockedMultiSelections":{` + strings.Join(manyKeys, ",") + `}},
"filesFailedToEmbed":` + ids + `,"files":{` + strings.Join(files, ",") + `}}`
	env, _ := contentEnv(t, body)
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	out := decode(t, result)
	byID := map[string]map[string]any{}
	for _, e := range out["elements"].([]any) {
		m := e.(map[string]any)
		byID[m["id"].(string)] = m
	}
	for _, key := range []string{"points", "pressures", "group_ids", "bound_elements", "fixed_segments"} {
		if _, ok := byID["p"][key]; ok || byID["p"][key+"_omitted"] != true {
			t.Errorf("%s: want left out and marked, got %#v", key, byID["p"])
		}
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if _, ok := byID[id]["custom_data"]; ok || byID[id]["custom_data_omitted"] != true {
			t.Errorf("%s: custom_data want left out and marked: %#v", id, byID[id])
		}
	}
	if _, ok := byID["c4"]["custom_data_omitted"]; ok || byID["c4"]["custom_data"] == nil {
		t.Errorf("c4 = %#v", byID["c4"])
	}
	for _, key := range []string{"locked_multi_selections", "files_failed_to_embed", "files"} {
		if _, ok := out[key]; ok || out[key+"_omitted"] != true {
			t.Errorf("%s: want left out and marked", key)
		}
	}
	if out["files_count"] != json.Number("201") {
		t.Errorf("files_count = %v", out["files_count"])
	}
}

func TestSceneContentToleratesUnexpectedMemberTypes(t *testing.T) {
	env, _ := contentEnv(t, `{"type":5,"version":"2","source":[1],"appState":{"lockedMultiSelections":[1]},
"filesFailedToEmbed":"x","files":{"f":3},"elements":[{"id":"e","type":"rectangle","seed":1.5,"angle":"x",
"updated":"1","locked":"no","roundness":[1],"startBinding":5,"points":[[1]],"customData":[1],"groupIds":3,
"link":5,"fontFamily":2.5,"crop":"c","scale":[1]}]}`)
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(result), &got)
	want := map[string]any{"scene_id": "s-own", "element_count": 1.0, "matched": 1.0, "offset": 0.0, "returned": 1.0,
		"truncated": false, "files_count": 1.0, "elements": []any{map[string]any{"id": "e", "type": "rectangle",
			"x": 0.0, "y": 0.0, "width": 0.0, "height": 0.0}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result = %s", result)
	}
}

// Original text and links count toward the text budget, so the answer stops and continues at next_offset.
func TestSceneContentBudgetCountsOriginalTextAndLinks(t *testing.T) {
	big := strings.Repeat("x", 1000)
	var elements []string
	for i := 0; i < 400; i++ {
		elements = append(elements, fmt.Sprintf(`{"id":"e%d","type":"text","originalText":%q,"link":%q}`, i, big, big))
	}
	env, _ := contentEnv(t, `{"elements":[`+strings.Join(elements, ",")+`]}`)
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":1000}`)
	if err != nil {
		t.Fatal(err)
	}
	out := decode(t, result)
	returned, _ := out["returned"].(json.Number).Int64()
	if out["truncated"] != true || returned < 100 || returned > 140 || out["next_offset"] != json.Number(fmt.Sprint(returned)) {
		t.Fatalf("returned=%d truncated=%v next=%v", returned, out["truncated"], out["next_offset"])
	}
}

func TestSceneContentTextsAreBounded(t *testing.T) {
	long := strings.Repeat("y", maxElementText+50)
	env, _ := contentEnv(t, fmt.Sprintf(`{"source":%q,"elements":[{"id":"e","type":"text","originalText":%q,"link":%q}]}`,
		long, long, long))
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, strings.Repeat("y", maxElementText+1)) || !strings.Contains(result, strings.Repeat("y", maxElementText)) {
		t.Fatalf("texts are not capped at %d bytes", maxElementText)
	}
}
