package excalidrawplus

import (
	"encoding/json"
	"fmt"
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
	case r.URL.Path == apiPath+"/workspaces/users":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+fullUser+`]}`), nil
	case strings.HasPrefix(r.URL.Path, apiPath+"/workspaces/users/"):
		return jsonResponse(200, fullUser), nil
	case r.URL.Path == apiPath+"/logs":
		return jsonResponse(200, fmt.Sprintf(fullLogs, "x")), nil
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

const fullUser = `{"isInitialized":true,"created":"2019-08-24T14:15:22Z","uid":"uid-1","id":"u1",
 "picture":"https://pic.invalid/canary","email":"a@example.com","name":"Ann","updated":"2019-08-25T14:15:22Z",
 "workspaceTeams":{"g":["t1"]},"subscriptionStatus":"active","lastActive":"2019-08-26T14:15:22Z",
 "lastActiveRefreshedAt":"2019-08-27T14:15:22Z",
 "preferences":{"ossAutoRedirect":false,"sceneOrder":"manual","initialRedirect":"dashboard",
  "hideDeprecatedFonts":true,"enableAutoSceneNamingForPrivateCollections":false,
  "blockCommentEmailNotifications":true,"pushToTalkEnabled":false,"theme":"light","locale":"en",
  "localeSuggestionDismissed":true},
 "lastVisitedScenes":["s-1","bad id","s-2"],"lastEditedScenes":["s-3"],
 "rateLimits":{"textToDiagram":{"k\u0007ey":{"lastUsed":1700000000001,"firstUsed":1700000000000,"count":3}},
  "diagramToCode":{},"nameScene":{"p":{"lastUsed":"7","firstUsed":1.5,"count":"x"}}},
 "libraryVersion":9007199254740993,"role":"member","passwordChangedAt":"canary"}`

var wantUser = map[string]any{"id": "u1", "name": "Ann", "email": "a@example.com", "role": "member",
	"teams": map[string]any{"g": []any{"t1"}}, "created": "2019-08-24T14:15:22Z", "last_active": "2019-08-26T14:15:22Z",
	"is_initialized": true, "uid": "uid-1", "updated": "2019-08-25T14:15:22Z", "subscription_status": "active",
	"last_active_refreshed_at": "2019-08-27T14:15:22Z", "library_version": json.Number("9007199254740993"),
	"preferences": map[string]any{"oss_auto_redirect": false, "scene_order": "manual", "initial_redirect": "dashboard",
		"hide_deprecated_fonts": true, "enable_auto_scene_naming_for_private_collections": false,
		"block_comment_email_notifications": true, "push_to_talk_enabled": false, "theme": "light", "locale": "en",
		"locale_suggestion_dismissed": true},
	"rate_limits": map[string]any{
		"text_to_diagram": map[string]any{"k ey": map[string]any{"last_used": json.Number("1700000000001"),
			"first_used": json.Number("1700000000000"), "count": json.Number("3")}},
		"name_scene": map[string]any{"p": map[string]any{"last_used": json.Number("7")}}},
	"last_visited_scenes": []any{"s-1", "s-2"}, "last_edited_scenes": []any{"s-3"}}

func TestUsersCarryMetadata(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/workspaces/users" {
			return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+fullUser+`]}`), nil
		}
		return jsonResponse(200, fullUser), nil
	})
	list, err := env.invoke(usersList.ID, "peopleAll", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	get, err := env.invoke(usersGet.ID, "peopleAll", `{"user_id":"u1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, list)["users"].([]any)[0]; !reflect.DeepEqual(got, any(wantUser)) {
		t.Fatalf("list = %v\nwant %v", got, wantUser)
	}
	if got := decode(t, get); !reflect.DeepEqual(got, wantUser) {
		t.Fatalf("get = %v\nwant %v", got, wantUser)
	}
	for _, canary := range []string{"canary", "picture", "passwordChangedAt"} {
		if strings.Contains(list, canary) || strings.Contains(get, canary) {
			t.Fatalf("output carries %q", canary)
		}
	}
}

// A member of an unexpected type drops only itself.
func TestUserMetadataIsReadLeniently(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":"u1","isInitialized":"yes","uid":5,"libraryVersion":"v","preferences":[],
 "rateLimits":{"textToDiagram":7},"lastVisitedScenes":{"a":1},"lastEditedScenes":"x","name":"Ann"}`), nil
	})
	out, err := env.invoke(usersGet.ID, "peopleAll", `{"user_id":"u1"}`)
	if err != nil || out != `{"id":"u1","name":"Ann"}` {
		t.Fatalf("out = %s err = %v", out, err)
	}
}

func TestRateLimitKeysAreCapped(t *testing.T) {
	var keys []string
	for i := 0; i < maxRateKeys+10; i++ {
		keys = append(keys, fmt.Sprintf(`"k%03d":{"count":1}`, i))
	}
	got := rateLimitsOf(json.RawMessage(`{"nameScene":{` + strings.Join(keys, ",") + `}}`))
	if len(got["name_scene"]) != maxRateKeys {
		t.Fatalf("keys = %d", len(got["name_scene"]))
	}
}

const fullLogs = `{"logs":[{"id":"l1","action":"scene:create","operation":"create","details":"made a scene",
 "created_at":"c","ip_address":"203.0.113.9","user_id":"u1","workspace_id":"ws-1","app_source":"admin",
 "source_type":"user","source_id":"src-1","status":200,"user_email":"a@example.com","user_full_name":"Ann\u0007",
 "user_picture":"https://pic.invalid/canary"},
 {"id":"l2","action":"a","details":{"scene":{"id":"s","n":3}}},
 {"id":"l3","action":"a","details":{"a":{"b":{"c":{"d":{"e":1}}}}}},
 {"id":"l4","action":"a","details":["` + "%s" + `"]},
 {"id":"l5","action":"a","details":42}],
 "nextCursor":"cursor-canary","hasMore":false,"availableActions":["scene:create","ai:text-to-diagram"],
 "totalCount":5,"totalPages":1,"currentPage":1}`

func TestLogsCarryMetadata(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, fmt.Sprintf(fullLogs, strings.Repeat("x", maxLogText+1))), nil
	})
	out, err := env.invoke(logsList.ID, "every", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	page := decode(t, out)
	want := map[string]any{"id": "l1", "action": "scene:create", "operation": "create", "details": "made a scene",
		"created_at": "c", "ip_address": "203.0.113.9", "user_id": "u1", "workspace_id": "ws-1", "app_source": "admin",
		"source_type": "user", "source_id": "src-1", "status": json.Number("200"), "user_email": "a@example.com",
		"user_full_name": "Ann"}
	logs := page["logs"].([]any)
	if !reflect.DeepEqual(logs[0], any(want)) {
		t.Fatalf("entry = %v\nwant %v", logs[0], want)
	}
	if !reflect.DeepEqual(logs[1].(map[string]any)["details"], any(map[string]any{"scene": map[string]any{"id": "s", "n": json.Number("3")}})) {
		t.Fatalf("details = %v", logs[1])
	}
	for _, i := range []int{2, 3} {
		entry := logs[i].(map[string]any)
		if _, ok := entry["details"]; ok || entry["details_omitted"] != true {
			t.Fatalf("entry %d = %v", i, entry)
		}
	}
	if logs[4].(map[string]any)["details"] != json.Number("42") {
		t.Fatalf("scalar details = %v", logs[4])
	}
	if page["total_count"] != json.Number("5") || page["total_pages"] != json.Number("1") ||
		page["current_page"] != json.Number("1") ||
		!reflect.DeepEqual(page["available_actions"], []any{"scene:create", "ai:text-to-diagram"}) {
		t.Fatalf("head = %v", page)
	}
	for _, leak := range []string{"canary", "picture", "cursor", "nextCursor", `"page"`} {
		if strings.Contains(out, leak) {
			t.Fatalf("output carries %q: %s", leak, out)
		}
	}
}

func TestAnswerWithoutLogMetadataKeepsFormerOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"logs":[{"id":"l1","action":"a","status":200}],"hasMore":false}`), nil
	})
	out, err := env.invoke(logsList.ID, "every", `{}`)
	if err != nil || out != `{"count":1,"has_next_page":false,"limit":50,"logs":[{"action":"a","id":"l1","status":200}],"offset":0}` {
		t.Fatalf("out = %s err = %v", out, err)
	}
}

func TestLogsOperationAndSingleBounds(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"logs":[],"hasMore":false}`), nil
	})
	for _, op := range []string{"create", "read", "update", "delete"} {
		if _, err := env.invoke(logsList.ID, "every", `{"operation":"`+op+`"}`); err != nil ||
			calls[len(calls)-1].query.Get("operation") != op {
			t.Fatalf("%s: err = %v", op, err)
		}
	}
	for _, op := range []string{"purge", "Create", "create&x=1"} {
		before := len(calls)
		if _, err := env.invoke(logsList.ID, "every", `{"operation":"`+op+`"}`); err == nil || len(calls) != before {
			t.Fatalf("%s: err = %v, calls = %d", op, err, len(calls)-before)
		}
	}
	if _, err := env.invoke(logsList.ID, "every", `{"from":"2026-01-01T01:00:00+01:00"}`); err != nil {
		t.Fatal(err)
	}
	if q := calls[len(calls)-1].query; q.Get("dateFrom") != "2026-01-01T00:00:00Z" || q.Has("dateTo") {
		t.Fatalf("query = %v", q)
	}
	if _, err := env.invoke(logsList.ID, "every", `{"to":"2030-01-01T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if q := calls[len(calls)-1].query; q.Get("dateTo") != "2030-01-01T00:00:00Z" || q.Has("dateFrom") {
		t.Fatalf("query = %v", q)
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
		connection := "one"
		switch tool {
		case usersList.ID, usersGet.ID:
			connection = "peopleAll"
		case logsList.ID:
			connection = "every"
		}
		result, err := env.invoke(tool, connection, args)
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
				if nested, ok := value.(map[string]any); ok && key != "details" && key != "teams" { // free-form or foreign-keyed members
					declared := schemaProperties(t, props[key])
					for sub, inner := range nested {
						if _, ok := declared[sub]; !ok {
							t.Errorf("%s: nested member %s.%s not in the output schema", tool, key, sub)
						}
						// Maps keyed by foreign names (rate limits) declare the shape of their values.
						if groups, ok := inner.(map[string]any); ok && key == "rate_limits" {
							var shape struct {
								Additional json.RawMessage `json:"additionalProperties"`
							}
							_ = json.Unmarshal(declared[sub], &shape)
							for _, group := range groups {
								for member := range group.(map[string]any) {
									if _, ok := schemaProperties(t, shape.Additional)[member]; !ok {
										t.Errorf("%s: %s.%s.%s not in the output schema", tool, key, sub, member)
									}
								}
							}
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
	check(usersList.ID, `{}`, func(m map[string]any) []any { return m["users"].([]any) }, itemOf("users"))
	check(usersGet.ID, `{"user_id":"u1"}`, func(m map[string]any) []any { return []any{m} },
		func(props map[string]json.RawMessage) json.RawMessage {
			raw, _ := json.Marshal(map[string]any{"properties": props})
			return raw
		})
	check(logsList.ID, `{}`, func(m map[string]any) []any { return m["logs"].([]any) }, itemOf("logs"))
	check(scenesList.ID, `{}`, func(m map[string]any) []any { return m["scenes"].([]any) }, itemOf("scenes"))
}
