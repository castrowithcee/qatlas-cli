package penpot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	shapeRoot  = "00000000-0000-0000-0000-000000000000"
	shapeRect  = "00000000-0000-0000-0000-0000000000b1"
	shapeFrame = "00000000-0000-0000-0000-0000000000b2"
	shapeText  = "00000000-0000-0000-0000-0000000000b3"
	shapePath  = "00000000-0000-0000-0000-0000000000b4"
	shapeBin   = "00000000-0000-0000-0000-0000000000b5"
	shapeImage = "00000000-0000-0000-0000-0000000000b6"
	shapeInst  = "00000000-0000-0000-0000-0000000000b7"
	shapeBool  = "00000000-0000-0000-0000-0000000000b8"
	shapeSVG   = "00000000-0000-0000-0000-0000000000b9"
	otherID    = "00000000-0000-0000-0000-0000000000c1"
	cellID     = "00000000-0000-0000-0000-0000000000c2"
	libraryID  = "00000000-0000-0000-0000-0000000000c3"
	mediaID    = "00000000-0000-0000-0000-0000000000c4"
	pluginID   = "00000000-0000-0000-0000-0000000000c5"
)

const rectAttrs = `"selrect":{"x":1.5,"y":2,"width":10,"height":20,"x1":1.5,"y1":2,"x2":11.5,"y2":22},` +
	`"points":[{"x":1.5,"y":2},{"x":11.5,"y":2}],"transform":{"a":1,"b":0,"c":0,"d":1,"e":0,"f":0},` +
	`"transform-inverse":{"a":1,"b":0,"c":0,"d":1,"e":0,"f":0},"page-id":"` + pageID + `","rotation":0,` +
	`"fills":[{"fill-color":"#112233","fill-opacity":1.0,"fill-color-ref-file":"` + libraryID + `"},` +
	`{"fill-image":{"id":"` + mediaID + `","width":100,"height":50,"mtype":"image/png","keep-aspect-ratio":false}}],` +
	`"strokes":[{"stroke-color":"#000000","stroke-width":2.50,"stroke-style":"solid","stroke-alignment":"inner"}],` +
	`"proportion":1.0,"proportion-lock":false,"constraints-h":"left","constraints-v":"top","fixed-scroll":false,` +
	`"r1":4,"r2":4,"r3":4,"r4":4,"opacity":0.10,"blend-mode":"normal","hidden":false,"locked":true,"blocked":false,` +
	`"shadow":[{"style":"drop-shadow","offset-x":2,"offset-y":2,"blur":4,"color":{"color":"#000000","opacity":0.2}}],` +
	`"blur":{"type":"layer-blur","value":4,"hidden":false},"exports":[{"type":"png","suffix":"x2","scale":2}],` +
	`"layout-item-margin-type":"simple","layout-item-margin":{"m1":1,"m2":2,"m3":3,"m4":4},"layout-item-max-h":100,` +
	`"layout-item-min-h":10,"layout-item-max-w":200,"layout-item-min-w":20,"layout-item-h-sizing":"fill",` +
	`"layout-item-v-sizing":"auto","layout-item-align-self":"center","layout-item-absolute":false,"layout-item-z-index":3`

const frameAttrs = `"shapes":["` + shapeRect + `","` + shapeText + `"],"layout":"flex","layout-flex-dir":"row",` +
	`"layout-gap":{"row-gap":8,"column-gap":4},"layout-gap-type":"multiple","layout-wrap-type":"wrap",` +
	`"layout-padding-type":"multiple","layout-padding":{"p1":1,"p2":2,"p3":3,"p4":4},"layout-justify-content":"start",` +
	`"layout-justify-items":"start","layout-align-content":"stretch","layout-align-items":"center",` +
	`"layout-grid-dir":"row","layout-grid-rows":[{"type":"auto","value":null}],"layout-grid-columns":[{"type":"flex","value":1}],` +
	`"layout-grid-cells":{"` + cellID + `":{"id":"` + cellID + `","area-name":"a","row":1,"column":1,"row-span":1,` +
	`"column-span":1,"position":"auto","shapes":["` + shapeRect + `"]}},"hide-fill-on-export":false,"show-content":true,` +
	`"hide-in-viewer":false,"is-variant-container":true,"variant-id":"` + otherID + `","variant-name":"Property 1=A",` +
	`"variant-error":"duplicate","grids":[{"type":"square","display":true,"params":{"size":16}}],` +
	`"interactions":[{"event-type":"click","action-type":"navigate","destination":"` + shapeFrame + `","preserve-scroll":false}],` +
	`"background-blur":{"type":"background-blur","value":2,"hidden":false},` +
	`"applied-tokens":{"fill":"color.primary","stroke-width":"size.sm"},` +
	`"plugin-data":{"global":{"my-key":"a\u0007b"},"` + pluginID + `":{"another_key":"v","camelKey":"w"}}`

const textContent = `{"type":"root","children":[{"type":"paragraph-set","children":[{"type":"paragraph","children":` +
	`[{"text":"Hallo\u0007 Welt","font-size":"14"}]}]}]}`

// fullPageBody is a page of Penpot 2.18.0 with every attribute the schemas define, one binary-encoded path.
func fullPageBody() string {
	objects := []string{
		`"` + shapeRoot + `":{"id":"` + shapeRoot + `","type":"frame","name":"Root Frame","parent-id":"` + shapeRoot +
			`","frame-id":"` + shapeRoot + `","x":0,"y":0,"width":0,"height":0,"shapes":["` + shapeFrame + `"]}`,
		`"` + shapeRect + `":{"id":"` + shapeRect + `","type":"rect","name":"Rechteck","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":1.5,"y":2,"width":10,"height":20,` + rectAttrs + `}`,
		`"` + shapeFrame + `":{"id":"` + shapeFrame + `","type":"frame","name":"Rahmen","parent-id":"` + shapeRoot +
			`","frame-id":"` + shapeRoot + `","x":0,"y":0,"width":300,"height":200,` + frameAttrs + `}`,
		`"` + shapeText + `":{"id":"` + shapeText + `","type":"text","name":"Text","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":3,"y":4,"width":50,"height":12,"content":` + textContent +
			`,"position-data":[{"x":3,"y":4,"width":50,"height":12,"text":"Hallo","font-family":"sourcesanspro"}],` +
			`"grow-type":"auto-width"}`,
		`"` + shapePath + `":{"id":"` + shapePath + `","type":"path","name":"Pfad","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":5,"height":5,"content":[{"command":"move-to",` +
			`"params":{"x":0,"y":0}},{"command":"line-to","params":{"x":5,"y":5}}]}`,
		`"` + shapeBin + `":{"id":"` + shapeBin + `","type":"path","name":"Binaer","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":5,"height":5,"content":"AAECAwQFBgc="}`,
		`"` + shapeImage + `":{"id":"` + shapeImage + `","type":"image","name":"Bild","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":100,"height":50,"metadata":{"width":100,"height":50,` +
			`"mtype":"image/png","id":"` + mediaID + `"}}`,
		`"` + shapeInst + `":{"id":"` + shapeInst + `","type":"group","name":"Instanz","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":9,"height":9,"shapes":["` + shapeRect + `"],` +
			`"component-id":"` + otherID + `","component-file":"` + libraryID + `","component-root":true,` +
			`"main-instance":false,"remote-synced":true,"shape-ref":"` + cellID + `","touched":["fill-group"],` +
			`"masked-group":true,"collapsed":true}`,
		`"` + shapeBool + `":{"id":"` + shapeBool + `","type":"bool","name":"Bool","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":9,"height":9,"shapes":["` + shapeRect + `"],` +
			`"bool-type":"union","content":[{"command":"move-to","params":{"x":1,"y":1}}]}`,
		`"` + shapeSVG + `":{"id":"` + shapeSVG + `","type":"svg-raw","name":"Svg","parent-id":"` + shapeFrame +
			`","frame-id":"` + shapeFrame + `","x":0,"y":0,"width":9,"height":9,"shapes":["` + shapeRect + `"],` +
			`"content":{"tag":"path","attrs":{"d":"M0 0","stroke-width":"2"}}}`,
	}
	return `{"id":"` + pageID + `","name":"Seite\u0007 1","index":0,"background":"#ffffff","pixel-grid-color":"#ff0000",` +
		`"pixel-grid-opacity":0.50,"default-grids":{"square":{"size":16,"color":{"color":"#aaaaaa","opacity":0.1}}},` +
		`"flows":{"` + otherID + `":{"id":"` + otherID + `","name":"Start","starting-frame":"` + shapeFrame + `"}},` +
		`"guides":{"` + cellID + `":{"id":"` + cellID + `","axis":"x","position":100,"frame-id":"` + shapeFrame + `","color":"#00ff00"}},` +
		`"comment-thread-positions":{"` + pluginID + `":{"frame-id":"` + shapeFrame + `","position":{"x":1,"y":2}}},` +
		`"plugin-data":{"global":{"page-key":"x\u0007y"}},"objects":{` + strings.Join(objects, ",") + `}}`
}

func pageHandler(body string) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		if c.command() == cmdPage {
			return jsonResponse(200, body), nil
		}
		return filesHandler(0)(c)
	}
}

// getPage reads the page of the answer with numbers kept as written.
func getPage(t *testing.T, body string) (map[string]any, string) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, pageHandler(body))
	result, err := env.invoke(filesGet.ID, "one", `{`+fileArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	return decodeNumbers(t, result)["page"].(map[string]any), result
}

func decodeNumbers(t *testing.T, text string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func shapeOf(t *testing.T, page map[string]any, id string) map[string]any {
	t.Helper()
	for _, item := range page["shapes"].([]any) {
		if shape := item.(map[string]any); shape["id"] == id {
			return shape
		}
	}
	t.Fatalf("shape %s is not in the answer", id)
	return nil
}

func expectShape(t *testing.T, page map[string]any, id, want string) {
	t.Helper()
	got := shapeOf(t, page, id)
	expected := decodeNumbers(t, want)
	if !reflect.DeepEqual(got, expected) {
		g, _ := json.Marshal(got)
		t.Errorf("shape %s\n got  %s\n want %s", id, g, want)
	}
}

func TestFilesGetReturnsEverySchemaAttribute(t *testing.T) {
	page, result := getPage(t, fullPageBody())
	wantPage := decodeNumbers(t, `{"id":"`+pageID+`","name":"Seite  1","index":0,"background":"#ffffff",`+
		`"pixel_grid_color":"#ff0000","pixel_grid_opacity":0.50,`+
		`"default_grids":{"square":{"size":16,"color":{"color":"#aaaaaa","opacity":0.1}}},`+
		`"flows":{"`+otherID+`":{"id":"`+otherID+`","name":"Start","starting_frame":"`+shapeFrame+`"}},`+
		`"guides":{"`+cellID+`":{"id":"`+cellID+`","axis":"x","position":100,"frame_id":"`+shapeFrame+`","color":"#00ff00"}},`+
		`"comment_thread_positions":{"`+pluginID+`":{"frame_id":"`+shapeFrame+`","position":{"x":1,"y":2}}},`+
		`"plugin_data":{"global":{"page-key":"x y"}},"shape_count":10,"truncated":false}`)
	for key, want := range wantPage {
		if !reflect.DeepEqual(page[key], want) {
			t.Errorf("page %s = %v, want %v", key, page[key], want)
		}
	}
	for _, want := range []string{`"opacity":0.10`, `"stroke_width":2.50`, `"proportion":1.0`, `"fill_opacity":1.0`} {
		if !strings.Contains(result, want) {
			t.Errorf("number not kept as written: %s", want)
		}
	}
	expectShape(t, page, shapeRect, `{"id":"`+shapeRect+`","type":"rect","name":"Rechteck","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":1.5,"y":2,"width":10,"height":20,`+
		`"selrect":{"x":1.5,"y":2,"width":10,"height":20,"x1":1.5,"y1":2,"x2":11.5,"y2":22},`+
		`"points":[{"x":1.5,"y":2},{"x":11.5,"y":2}],"transform":{"a":1,"b":0,"c":0,"d":1,"e":0,"f":0},`+
		`"transform_inverse":{"a":1,"b":0,"c":0,"d":1,"e":0,"f":0},"page_id":"`+pageID+`","rotation":0,`+
		`"fills":[{"fill_color":"#112233","fill_opacity":1.0,"fill_color_ref_file":"`+libraryID+`"},`+
		`{"fill_image":{"id":"`+mediaID+`","width":100,"height":50,"mtype":"image/png","keep_aspect_ratio":false}}],`+
		`"strokes":[{"stroke_color":"#000000","stroke_width":2.50,"stroke_style":"solid","stroke_alignment":"inner"}],`+
		`"proportion":1.0,"proportion_lock":false,"constraints_h":"left","constraints_v":"top","fixed_scroll":false,`+
		`"r1":4,"r2":4,"r3":4,"r4":4,"opacity":0.10,"blend_mode":"normal","hidden":false,"locked":true,"blocked":false,`+
		`"shadow":[{"style":"drop-shadow","offset_x":2,"offset_y":2,"blur":4,"color":{"color":"#000000","opacity":0.2}}],`+
		`"blur":{"type":"layer-blur","value":4,"hidden":false},"exports":[{"type":"png","suffix":"x2","scale":2}],`+
		`"layout_item_margin_type":"simple","layout_item_margin":{"m1":1,"m2":2,"m3":3,"m4":4},"layout_item_max_h":100,`+
		`"layout_item_min_h":10,"layout_item_max_w":200,"layout_item_min_w":20,"layout_item_h_sizing":"fill",`+
		`"layout_item_v_sizing":"auto","layout_item_align_self":"center","layout_item_absolute":false,"layout_item_z_index":3}`)
	expectShape(t, page, shapeFrame, `{"id":"`+shapeFrame+`","type":"frame","name":"Rahmen","parent_id":"`+shapeRoot+
		`","frame_id":"`+shapeRoot+`","x":0,"y":0,"width":300,"height":200,`+
		`"shapes":["`+shapeRect+`","`+shapeText+`"],"layout":"flex","layout_flex_dir":"row",`+
		`"layout_gap":{"row_gap":8,"column_gap":4},"layout_gap_type":"multiple","layout_wrap_type":"wrap",`+
		`"layout_padding_type":"multiple","layout_padding":{"p1":1,"p2":2,"p3":3,"p4":4},"layout_justify_content":"start",`+
		`"layout_justify_items":"start","layout_align_content":"stretch","layout_align_items":"center",`+
		`"layout_grid_dir":"row","layout_grid_rows":[{"type":"auto"}],"layout_grid_columns":[{"type":"flex","value":1}],`+
		`"layout_grid_cells":{"`+cellID+`":{"id":"`+cellID+`","area_name":"a","row":1,"column":1,"row_span":1,`+
		`"column_span":1,"position":"auto","shapes":["`+shapeRect+`"]}},"hide_fill_on_export":false,"show_content":true,`+
		`"hide_in_viewer":false,"is_variant_container":true,"variant_id":"`+otherID+`","variant_name":"Property 1=A",`+
		`"variant_error":"duplicate","grids":[{"type":"square","display":true,"params":{"size":16}}],`+
		`"interactions":[{"event_type":"click","action_type":"navigate","destination":"`+shapeFrame+`","preserve_scroll":false}],`+
		`"background_blur":{"type":"background-blur","value":2,"hidden":false},`+
		`"applied_tokens":{"fill":"color.primary","stroke_width":"size.sm"},`+
		`"plugin_data":{"global":{"my-key":"a b"},"`+pluginID+`":{"another_key":"v","camelKey":"w"}}}`)
	expectShape(t, page, shapeText, `{"id":"`+shapeText+`","type":"text","name":"Text","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":3,"y":4,"width":50,"height":12,`+
		`"content":{"type":"root","children":[{"type":"paragraph-set","children":[{"type":"paragraph","children":`+
		`[{"text":"Hallo  Welt","font_size":"14"}]}]}]},`+
		`"position_data":[{"x":3,"y":4,"width":50,"height":12,"text":"Hallo","font_family":"sourcesanspro"}],`+
		`"grow_type":"auto-width"}`)
	expectShape(t, page, shapeBin, `{"id":"`+shapeBin+`","type":"path","name":"Binaer","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":5,"height":5,"omitted_fields":["content"]}`)
	expectShape(t, page, shapePath, `{"id":"`+shapePath+`","type":"path","name":"Pfad","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":5,"height":5,"content":[{"command":"move-to",`+
		`"params":{"x":0,"y":0}},{"command":"line-to","params":{"x":5,"y":5}}]}`)
	expectShape(t, page, shapeImage, `{"id":"`+shapeImage+`","type":"image","name":"Bild","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":100,"height":50,"metadata":{"width":100,"height":50,`+
		`"mtype":"image/png","id":"`+mediaID+`"}}`)
	expectShape(t, page, shapeInst, `{"id":"`+shapeInst+`","type":"group","name":"Instanz","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":9,"height":9,"shapes":["`+shapeRect+`"],`+
		`"component_id":"`+otherID+`","component_file":"`+libraryID+`","component_root":true,`+
		`"main_instance":false,"remote_synced":true,"shape_ref":"`+cellID+`","touched":["fill-group"],`+
		`"masked_group":true,"collapsed":true}`)
	expectShape(t, page, shapeBool, `{"id":"`+shapeBool+`","type":"bool","name":"Bool","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":9,"height":9,"shapes":["`+shapeRect+`"],`+
		`"bool_type":"union","content":[{"command":"move-to","params":{"x":1,"y":1}}]}`)
	expectShape(t, page, shapeSVG, `{"id":"`+shapeSVG+`","type":"svg-raw","name":"Svg","parent_id":"`+shapeFrame+
		`","frame_id":"`+shapeFrame+`","x":0,"y":0,"width":9,"height":9,"shapes":["`+shapeRect+`"],`+
		`"content":{"tag":"path","attrs":{"d":"M0 0","stroke_width":"2"}}}`)
	if strings.Contains(result, "AAECAwQ") || strings.Contains(result, "\\u0007") {
		t.Errorf("binary content or control character in the answer")
	}
}

func TestFilesGetReadsCamelCaseAsKebabCase(t *testing.T) {
	kebab := `{"id":"` + shapeFrame + `","type":"frame","name":"F","parent-id":"` + shapeRoot + `","frame-id":"` + shapeRoot +
		`","layout-flex-dir":"row","layout-gap":{"row-gap":8},"fills":[{"fill-color":"#112233","fill-color-ref-id":"` + libraryID + `"}],` +
		`"layout-grid-cells":{"` + cellID + `":{"area-name":"a","row-span":2}},"constraints-h":"left"}`
	camel := `{"id":"` + shapeFrame + `","type":"frame","name":"F","parentId":"` + shapeRoot + `","frameId":"` + shapeRoot +
		`","layoutFlexDir":"row","layoutGap":{"rowGap":8},"fills":[{"fillColor":"#112233","fillColorRefId":"` + libraryID + `"}],` +
		`"layoutGridCells":{"` + cellID + `":{"areaName":"a","rowSpan":2}},"constraintsH":"left"}`
	var results []map[string]any
	for _, shape := range []string{kebab, camel} {
		page, _ := getPage(t, `{"id":"`+pageID+`","name":"S","objects":{"`+shapeFrame+`":`+shape+`}}`)
		results = append(results, shapeOf(t, page, shapeFrame))
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("kebab = %v\ncamel = %v", results[0], results[1])
	}
	if _, ok := results[0]["layout_flex_dir"]; !ok {
		t.Fatalf("snake case name missing: %v", results[0])
	}
	cells := results[0]["layout_grid_cells"].(map[string]any)
	if _, ok := cells[cellID].(map[string]any)["area_name"]; !ok {
		t.Errorf("cells = %v", cells)
	}
}

func TestFilesGetPageWithoutNewAttributesIsUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(1))
	result, err := env.invoke(filesGet.ID, "one", `{`+fileArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	want := decodeNumbers(t, `{"id":"`+pageID+`","name":"Seite 1","shape_count":1,"truncated":false,"shapes":[{"id":`+
		`"00000000-0000-0000-1000-000000000000","type":"rect","name":"R0","parent_id":"`+projectA1+`","frame_id":"`+
		projectA1+`","x":1.5,"y":2,"width":10,"height":20}]}`)
	if got := decodeNumbers(t, result)["page"]; !reflect.DeepEqual(got, any(want)) {
		t.Fatalf("page = %v\nwant %v", got, want)
	}
}

func TestFilesGetLeavesOutValuesBeyondTheBounds(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 9) + `1` + strings.Repeat(`}`, 9)
	var many strings.Builder
	many.WriteString(`[`)
	for i := 0; i <= maxFreeEntries; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"i":%d}`, i)
	}
	many.WriteString(`]`)
	long := strings.Repeat("x", maxFreeString+1)
	shape := `{"id":"` + shapeRect + `","type":"rect","name":"R","parent-id":"` + shapeRoot + `","frame-id":"` + shapeRoot +
		`","x":1,"y":2,"width":3,"height":4,"opacity":0.5,"fills":[{"fill-color":"` + long + `"}],"strokes":` + many.String() +
		`,"blur":` + deep + `,"layout-item-min-h":"wide","component-id":"not-an-id"}`
	page, result := getPage(t, `{"id":"`+pageID+`","name":"S","index":2,"guides":`+deep+`,"objects":{"`+shapeRect+`":`+shape+`}}`)
	got := shapeOf(t, page, shapeRect)
	want := []any{"blur", "component_id", "fills", "layout_item_min_h", "strokes"}
	if !reflect.DeepEqual(got["omitted_fields"], want) {
		t.Errorf("omitted_fields = %v", got["omitted_fields"])
	}
	if got["opacity"].(json.Number).String() != "0.5" || len(got) != 11 {
		t.Errorf("shape = %v", got)
	}
	if !reflect.DeepEqual(page["omitted_fields"], []any{"guides"}) || page["index"].(json.Number).String() != "2" {
		t.Errorf("page = %v", page)
	}
	if strings.Contains(result, "xxxx") {
		t.Errorf("a value beyond the bounds was cut instead of left out")
	}
}

func TestFilesGetBoundsTheAttributesOfAllShapes(t *testing.T) {
	long := strings.Repeat("y", maxFreeString)
	var body strings.Builder
	body.WriteString(`{"id":"` + pageID + `","name":"S","objects":{`)
	const total = 190
	for i := 0; i < total; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `"00000000-0000-0000-2000-%012d":{"type":"text","name":"T","x":1,"y":1,"width":1,"height":1,`+
			`"content":{"a":"%s","b":"%s"},"position-data":[{"text":"%s"}]}`, i, long, long, long)
	}
	body.WriteString(`}}`)
	page, result := getPage(t, body.String())
	shapes := page["shapes"].([]any)
	if page["truncated"] != true || len(shapes) == 0 || len(shapes) >= total || page["shape_count"].(json.Number).String() != "190" {
		t.Fatalf("truncated = %v, shapes = %d", page["truncated"], len(shapes))
	}
	if len(result) > maxAttrBytes+len(shapes)*400+2000 {
		t.Errorf("answer = %d bytes for %d shapes", len(result), len(shapes))
	}
}

func TestSnakeKeyMapsBothFormsToTheSchemaName(t *testing.T) {
	for in, want := range map[string]string{"layout-grid-cells": "layout_grid_cells", "layoutGridCells": "layout_grid_cells",
		"layout_grid_cells": "layout_grid_cells", "r1": "r1", "p1": "p1", "m4": "m4", "fillColorRefId": "fill_color_ref_id",
		"constraintsH": "constraints_h", "x": "x"} {
		if got := snakeKey(in); got != want {
			t.Errorf("snakeKey(%q) = %q, want %q", in, got, want)
		}
	}
	for key, spec := range commonAttrs {
		if key != normalizeKey(spec.name) {
			t.Errorf("attribute %q is named %q", key, spec.name)
		}
		if snakeKey(strings.ReplaceAll(spec.name, "_", "-")) != spec.name {
			t.Errorf("%q does not translate to itself", spec.name)
		}
	}
}
