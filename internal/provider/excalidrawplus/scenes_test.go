package excalidrawplus

import (
	"fmt"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"net/http"
	"strings"
	"testing"
)

func metaJSON(id, name, collection string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"collection":%q,"created":"c","updated":"u","totalElements":3,`+
		`"creator":"creator-canary","previewUrl":"https://preview.invalid/x"}`, id, name, collection)
}

func entryJSON(id, name, collection string) string {
	return `{"metadata":` + metaJSON(id, name, collection) + `,"readOnlyLinks":[{"id":"link-canary"}]}`
}

const contentBody = `{"type":"excalidraw","version":2,"sceneVersion":"7","appState":{"viewBackgroundColor":"#fff"},
"elements":[null,
{"id":"e1","type":"text","text":"Roadmap Q4","x":1,"y":2,"width":3,"height":4},
{"id":"e2","type":"rectangle","x":5,"y":6,"width":7,"height":8},
{"id":"e3","type":"text","text":"deleted roadmap","isDeleted":true},
{"id":"e4","type":"frame","name":"Roadmap frame"},
{"id":"e5","type":"text","text":"Other"}],
"files":{"f1":{"dataURL":"data:image/png;base64,AAAA-canary"}}}`

func sceneHandler(collections map[string]string, calls *[]call) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/content"):
			return jsonResponse(200, contentBody), nil
		case r.URL.Path == apiPath+"/scenes":
			return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+
				entryJSON("s-own", "Plan A", ownCollection)+","+entryJSON("s-foreign", "Plan B", foreignColl)+","+
				entryJSON("s-none", "Plan C", "")+`]}`), nil
		case strings.HasPrefix(r.URL.Path, apiPath+"/scenes/"):
			id := strings.TrimPrefix(r.URL.Path, apiPath+"/scenes/")
			if c, ok := collections[id]; ok {
				return jsonResponse(200, entryJSON(id, "Name", c)), nil
			}
		}
		return jsonResponse(404, `{}`), nil
	}
}

// A collection_id outside the allow-list is refused before any secret is read or request is sent, without
// naming the collection.
func TestCollectionRefusedBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(nil, &calls))
	_, err := env.invoke(scenesList.ID, "one", `{"collection_id":"`+foreignColl+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignColl) {
		t.Fatalf("err = %v, want an invalid request not naming the collection", err)
	}
	if _, err := env.invoke(scenesList.ID, "two", `{}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want collection_id required for several collections", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls=%v reads=%d, want none", calls, *env.reads)
	}
}

func TestScenesListFiltersAndNameFilter(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(nil, &calls))
	result, err := env.invoke(scenesList.ID, "one", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if calls[0].query.Get("collectionId") != ownCollection {
		t.Fatalf("query = %v, want the only allowed collection", calls[0].query)
	}
	for _, leaked := range []string{"s-foreign", "Plan B", "s-none", "creator-canary", "preview.invalid", "link-canary"} {
		if strings.Contains(result, leaked) {
			t.Fatalf("result leaked %q: %s", leaked, result)
		}
	}
	if !strings.Contains(result, "s-own") {
		t.Fatalf("result = %s", result)
	}
	// Wildcard, no filter: every non-trash scene is listed, and the name filter narrows the page.
	result, err = env.invoke(scenesList.ID, "every", `{"name":"plan b"}`)
	if err != nil || !strings.Contains(result, "s-foreign") || strings.Contains(result, "s-own") {
		t.Fatalf("result = %s err = %v", result, err)
	}
	if calls[len(calls)-1].query.Get("collectionId") != "" {
		t.Fatal("wildcard without collection_id must not filter by collection")
	}
	// An explicit allowed collection of a multi-collection connection.
	if _, err := env.invoke(scenesList.ID, "two", `{"collection_id":"`+otherAllowed+`"}`); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// A scene of a foreign collection (or of none) is refused before its content is requested or returned.
func TestForeignSceneRefusedWithoutContent(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(map[string]string{"s-foreign": foreignColl, "s-none": "", "s-own": ownCollection}, &calls))
	for _, scene := range []string{"s-foreign", "s-none"} {
		for _, op := range []string{scenesGet.ID, scenesContent.ID} {
			result, err := env.invoke(op, "one", `{"scene_id":"`+scene+`"}`)
			if !isInvalidRequest(err) || result != "" || strings.Contains(err.Error(), foreignColl) {
				t.Fatalf("%s %s: result=%q err=%v, want a refusal", op, scene, result, err)
			}
		}
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/content") {
			t.Fatalf("content was requested for a foreign scene: %+v", c)
		}
	}
	if _, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"../x"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid identifier refusal", err)
	}
	if result, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"s-own"}`); err != nil ||
		!strings.Contains(result, `"collection_id":"col-own"`) || strings.Contains(result, "link-canary") {
		t.Fatalf("result = %s err = %v", result, err)
	}
	// The wildcard admits a scene of any collection.
	if _, err := env.invoke(scenesGet.ID, "every", `{"scene_id":"s-foreign"}`); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSceneIdentityMismatchIsRefused(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, entryJSON("other", "x", ownCollection)), nil
	})
	if _, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"s-own"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, err = %v", classOf(err), err)
	}
}

func TestSceneContentSearchAndCaps(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(map[string]string{"s-own": ownCollection}, &calls))
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !strings.Contains(result, `"element_count":4`) || strings.Contains(result, "deleted roadmap") ||
		strings.Contains(result, "canary") || !strings.Contains(result, `"files_count":1`) {
		t.Fatalf("result = %s", result)
	}
	if len(calls) != 2 || calls[0].path != apiPath+"/scenes/s-own" || calls[1].path != apiPath+"/scenes/s-own/content" {
		t.Fatalf("calls = %+v, want the metadata binding first", calls)
	}
	// Client-side search over text and name, case-insensitive.
	result, err = env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","query":"ROADMAP"}`)
	if err != nil || !strings.Contains(result, `"matched":2`) || !strings.Contains(result, "Roadmap frame") ||
		strings.Contains(result, "Other") {
		t.Fatalf("result = %s err = %v", result, err)
	}
	// The element cap truncates and points at the next offset.
	result, err = env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":2}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || !strings.Contains(result, `"next_offset":2`) ||
		!strings.Contains(result, `"returned":2`) || !strings.Contains(result, `"matched":4`) {
		t.Fatalf("result = %s err = %v", result, err)
	}
	result, err = env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":2,"offset":2}`)
	if err != nil || strings.Contains(result, `"truncated":true`) || !strings.Contains(result, `"returned":2`) {
		t.Fatalf("result = %s err = %v", result, err)
	}
	if _, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":1001}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the element limit enforced", err)
	}
}

func TestSceneContentTextBudgetAndOversizeBody(t *testing.T) {
	var calls []call
	big := strings.Repeat("x", 5000)
	var elements []string
	for i := 0; i < 400; i++ {
		elements = append(elements, fmt.Sprintf(`{"id":"e%d","type":"text","text":%q}`, i, big))
	}
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			return jsonResponse(200, `{"elements":[`+strings.Join(elements, ",")+`]}`), nil
		}
		return jsonResponse(200, entryJSON("s-own", "n", ownCollection)), nil
	})
	result, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own","limit":1000}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxContentTextBytes+64<<10 ||
		strings.Contains(result, strings.Repeat("x", maxElementText+1)) {
		t.Fatalf("len=%d err=%v, want a capped answer", len(result), err)
	}

	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			return jsonResponse(200, `{"elements":[],"pad":"`+strings.Repeat("a", maxContentBytes)+`"}`), nil
		}
		return jsonResponse(200, entryJSON("s-own", "n", ownCollection)), nil
	})
	if _, err := env.invoke(scenesContent.ID, "one", `{"scene_id":"s-own"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want invalid-response for an oversized body", classOf(err))
	}
}
