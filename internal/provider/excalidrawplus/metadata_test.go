package excalidrawplus

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fullCollection = `{"id":"col-own","isDefault":true,"workspace":"ws-1","creator":"user-a","updater":"user-b",
 "teams":["team-1","bad id","team-2"],"created":"2019-08-24T14:15:22Z","sortOrder":"a0","name":"Own",
 "updated":"2019-08-25T14:15:22Z","isDeleted":false,"emoji":{"id":"rocket","native":"🚀","unified":"1f680"},
 "deletedAt":0}`

const fullScene = `{"metadata":{"workspace":"ws-1","name":"Plan","isUntitled":false,"created":"c","updated":"u",
 "previewUrl":"https://preview.invalid/canary","previewFilename":"p\u0007.png","previewBackground":"#fff",
 "isDeleted":false,"isPrivate":false,"creator":"user-a","updater":"user-b","sceneVersion":"7",
 "contentEpoch":9007199254740993,"linkSharing":0,"readComments":false,"writeComments":true,"allowCalls":false,
 "collection":"col-own","updateCount":0,"revisionCount":12,"lastRevision":"rev-3","totalElements":5,
 "deletedElements":0,"pinned":false,"hasNonDeletedFrames":false,"isSlidesSharingEnabled":true,
 "isWelcomeScene":false,"id":"s-own"},
 "readOnlyLinks":[{"id":"link-canary","preview":"https://readonly.invalid/canary"}],
 "sharedSlidesLinks":[{"id":"slides-canary"}]}`

func decode(t *testing.T, result string) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(result))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("%v: %s", err, result)
	}
	return out
}

func fullHandler(r *http.Request) (*http.Response, error) {
	switch {
	case r.URL.Path == apiPath+"/collections":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+fullCollection+`]}`), nil
	case r.URL.Path == apiPath+"/scenes":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+fullScene+`]}`), nil
	default:
		return jsonResponse(200, fullScene), nil
	}
}

func TestCollectionsListPassesMetadata(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fullHandler)
	result, err := env.invoke(collectionsList.ID, "one", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	item := decode(t, result)["collections"].([]any)[0].(map[string]any)
	want := map[string]any{"id": "col-own", "name": "Own", "is_default": true, "workspace": "ws-1",
		"creator": "user-a", "updater": "user-b", "teams": []any{"team-1", "team-2"}, "sort_order": "a0",
		"created": "2019-08-24T14:15:22Z", "updated": "2019-08-25T14:15:22Z",
		"emoji": map[string]any{"id": "rocket", "native": "\U0001F680", "unified": "1f680"}}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("item = %v\nwant %v", item, want)
	}
}

func TestScenesCarryMetadata(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fullHandler)
	list, err := env.invoke(scenesList.ID, "one", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	get, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "s-own", "name": "Plan", "collection_id": "col-own", "created": "c", "updated": "u",
		"scene_version": "7", "total_elements": json.Number("5"), "workspace": "ws-1", "creator": "user-a",
		"updater": "user-b", "preview_filename": "p .png", "preview_background": "#fff",
		"content_epoch": json.Number("9007199254740993"), "link_sharing": json.Number("0"),
		"read_comments": false, "write_comments": true, "allow_calls": false, "update_count": json.Number("0"),
		"revision_count": json.Number("12"), "last_revision": "rev-3", "deleted_elements": json.Number("0"),
		"has_non_deleted_frames": false, "is_slides_sharing_enabled": true, "is_welcome_scene": false}
	if got := decode(t, list)["scenes"].([]any)[0]; !reflect.DeepEqual(got, any(want)) {
		t.Fatalf("list = %v\nwant %v", got, want)
	}
	if got := decode(t, get); !reflect.DeepEqual(got, want) {
		t.Fatalf("get = %v\nwant %v", got, want)
	}
	for _, canary := range []string{"canary", "isDeleted", "deletedAt", "readOnlyLinks", "preview_url"} {
		if strings.Contains(list, canary) || strings.Contains(get, canary) {
			t.Fatalf("output carries %q", canary)
		}
	}
}

// An answer without the later members yields the former output only.
func TestAnswersWithoutMetadataKeepFormerOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/collections" {
			return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[{"id":"col-own","name":"Own","emoji":{},"teams":["bad id"]}]}`), nil
		}
		return jsonResponse(200, entryJSON("s-own", "Plan", ownCollection)), nil
	})
	collections, err := env.invoke(collectionsList.ID, "one", `{}`)
	if err != nil || collections != `{"collections":[{"id":"col-own","name":"Own"}],"count":1,"has_next_page":false,"limit":50,"offset":0}` {
		t.Fatalf("collections = %s err = %v", collections, err)
	}
	scene, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil || scene != `{"collection_id":"col-own","created":"c","id":"s-own","name":"Plan","total_elements":3,"updated":"u"}` {
		t.Fatalf("scene = %s err = %v", scene, err)
	}
}

func TestTeamsAreCapped(t *testing.T) {
	var teams []string
	for i := 0; i < maxListedIDs+20; i++ {
		teams = append(teams, "t"+string(rune('a'+i%26))+strings.Repeat("x", i/26))
	}
	if got := idsOf(teams); len(got) != maxListedIDs {
		t.Fatalf("len = %d", len(got))
	}
}

func schemaProperties(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return schema.Properties
}

// memberNames lists the members a tool can output: its top-level members, with an undescribed list wrapper
// replaced by the members of its items.
func memberNames(t *testing.T, raw json.RawMessage, fields map[string]bool) []string {
	t.Helper()
	var names []string
	for name, property := range schemaProperties(t, raw) {
		var array struct {
			Type  string          `json:"type"`
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(property, &array); err != nil {
			t.Fatal(err)
		}
		if array.Type == "array" && !fields[name] && len(schemaProperties(t, array.Items)) > 0 {
			names = append(names, memberNames(t, array.Items, fields)...)
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Every member a tool outputs is declared in its output schema and described in Fields. content.replace shares
// its result schema with content.patch but never reports not_applied, which only the patch describes.
func TestOutputMembersAreDeclared(t *testing.T) {
	for _, descriptor := range registry(t).Provider(Provider) {
		fields := map[string]bool{}
		for _, field := range descriptor.Fields {
			fields[field.Name] = true
		}
		for _, name := range memberNames(t, descriptor.OutputSchema, fields) {
			if !fields[name] && !(descriptor.ID == contentReplace.ID && name == "not_applied") {
				t.Errorf("%s: schema member %q has no field description", descriptor.ID, name)
			}
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, fullHandler)
	check := func(tool, args string, items func(map[string]any) []any, itemSchema func(map[string]json.RawMessage) json.RawMessage) {
		result, err := env.invoke(tool, "one", args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		descriptor := map[string]json.RawMessage{}
		var fields map[string]bool
		for _, d := range registry(t).Provider(Provider) {
			if d.ID == tool {
				descriptor = schemaProperties(t, d.OutputSchema)
				fields = map[string]bool{}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
			}
		}
		for _, item := range items(decode(t, result)) {
			props := schemaProperties(t, itemSchema(descriptor))
			for key, value := range item.(map[string]any) {
				if _, ok := props[key]; !ok {
					t.Errorf("%s: output member %q not in the output schema", tool, key)
				}
				if !fields[key] {
					t.Errorf("%s: output member %q not in Fields", tool, key)
				}
				if nested, ok := value.(map[string]any); ok {
					for sub := range nested {
						if _, ok := schemaProperties(t, props[key])[sub]; !ok {
							t.Errorf("%s: nested member %s.%s not in the output schema", tool, key, sub)
						}
					}
				}
			}
		}
	}
	itemOf := func(key string) func(map[string]json.RawMessage) json.RawMessage {
		return func(props map[string]json.RawMessage) json.RawMessage {
			var array struct {
				Items json.RawMessage `json:"items"`
			}
			_ = json.Unmarshal(props[key], &array)
			return array.Items
		}
	}
	check(collectionsList.ID, `{}`, func(m map[string]any) []any { return m["collections"].([]any) }, itemOf("collections"))
	check(scenesList.ID, `{}`, func(m map[string]any) []any { return m["scenes"].([]any) }, itemOf("scenes"))
}
