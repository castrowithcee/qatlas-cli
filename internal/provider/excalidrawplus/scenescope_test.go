package excalidrawplus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	listedScene   = "s-listed"
	siblingScene  = "s-sibling"
	listedInOther = "s-in-other"
)

func sceneCollections() map[string]string {
	return map[string]string{listedScene: ownCollection, siblingScene: ownCollection, listedInOther: foreignColl,
		"s-two": otherAllowed}
}

// A listed scene is read; another scene of the same collection is refused without a request and without its ID.
func TestSceneBindingAllowsOnlyListedScenes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(sceneCollections(), &calls))
	for _, id := range []string{listedScene, listedInOther} {
		out, err := env.invoke(scenesGet.ID, "sceneOnly", `{"scene_id":"`+id+`"}`)
		if err != nil || !strings.Contains(out, id) {
			t.Fatalf("scenes.get %s = %s, %v", id, out, err)
		}
	}
	if len(calls) != 2 || calls[0].path != apiPath+"/scenes/"+listedScene {
		t.Fatalf("calls = %+v, want one metadata read per scene", calls)
	}
	calls = nil
	out, err := env.invoke(scenesContent.ID, "sceneOnly", `{"scene_id":"`+listedScene+`"}`)
	if err != nil || !strings.Contains(out, "Roadmap Q4") || len(calls) != 2 || !strings.HasSuffix(calls[1].path, "/content") {
		t.Fatalf("content = %s, %v, calls %+v", out, err, calls)
	}
	calls = nil
	*env.reads = 0
	for _, id := range []string{siblingScene, "s-two", "unknown"} {
		_, err := env.invoke(scenesContent.ID, "sceneOnly", `{"scene_id":"`+id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) || strings.Contains(err.Error(), ownCollection) {
			t.Fatalf("scene %s: err = %v, want a refusal without the ID", id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want no request and no secret for a foreign scene", calls, *env.reads)
	}
}

func TestSceneBindingChangesOnlyListedScenes(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, deleteHandler(nil, sceneCollections(), &sent, okDelete))
	if _, err := env.invokeConfirmed(scenesDelete.ID, "sceneOnly", `{"scene_id":"`+siblingScene+`"}`); !isInvalidRequest(err) ||
		strings.Contains(err.Error(), siblingScene) || len(calls) != 0 || len(sent) != 0 {
		t.Fatalf("err = %v, calls %d, sent %v", err, len(calls), sent)
	}
	out, err := env.invokeConfirmed(scenesDelete.ID, "sceneOnly", `{"scene_id":"`+listedScene+`"}`)
	if err != nil || !strings.Contains(out, `"deleted":true`) || len(sent) != 1 || sent[0].path != apiPath+"/scenes/"+listedScene {
		t.Fatalf("out = %s, err = %v, sent %v", out, err, sent)
	}
}

// With a collection and a scene, the union applies; the listed scene does not open its collection.
func TestMixedBindingIsAUnion(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(sceneCollections(), &calls))
	for _, id := range []string{listedScene, "s-two"} {
		if _, err := env.invoke(scenesGet.ID, "mixed", `{"scene_id":"`+id+`"}`); err != nil {
			t.Fatalf("scenes.get %s = %v", id, err)
		}
	}
	for _, id := range []string{siblingScene, listedInOther} {
		if _, err := env.invoke(scenesGet.ID, "mixed", `{"scene_id":"`+id+`"}`); !isInvalidRequest(err) || strings.Contains(err.Error(), id) {
			t.Fatalf("scenes.get %s = %v, want a refusal", id, err)
		}
	}
	if _, err := env.invoke(scenesList.ID, "mixed", `{"collection_id":"`+ownCollection+`"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the listed scene not to open its collection", err)
	}
}

// The handlers refuse, before any secret or request, the tools a scene-only binding does not reach.
func TestSceneBindingRefusesCollectionAndWorkspaceTools(t *testing.T) {
	var calls []call
	serve(t, &calls, sceneHandler(sceneCollections(), &calls))
	reads := 0
	red := &redact.Redactor{}
	res := secret.NewWith(func(string) string { reads++; return apiKeyValue }, nil, nil, red)
	resolved := resolvedConnection("scene/"+listedScene, "scene/"+listedInOther)
	for name, call := range map[string]struct {
		run  func(json.RawMessage) (any, error)
		args string
	}{
		"collections.list": {func(a json.RawMessage) (any, error) {
			return invokeCollectionsList(context.Background(), resolved, res, red, a)
		}, `{}`},
		"scenes.list": {func(a json.RawMessage) (any, error) {
			return invokeScenesList(context.Background(), resolved, res, red, a)
		}, `{}`},
		"scenes.list named": {func(a json.RawMessage) (any, error) {
			return invokeScenesList(context.Background(), resolved, res, red, a)
		}, `{"collection_id":"` + ownCollection + `"}`},
		"scenes.create": {func(a json.RawMessage) (any, error) {
			return invokeScenesCreate(context.Background(), resolved, res, red, a)
		}, `{"name":"N","collection_id":"` + ownCollection + `"}`},
		"scenes.create bare": {func(a json.RawMessage) (any, error) {
			return invokeScenesCreate(context.Background(), resolved, res, red, a)
		}, `{"name":"N"}`},
		"scenes.update move": {func(a json.RawMessage) (any, error) {
			return invokeScenesUpdate(context.Background(), resolved, res, red, a)
		}, `{"scene_id":"` + listedScene + `","collection_id":"` + ownCollection + `"}`},
		"collections.delete": {func(a json.RawMessage) (any, error) {
			return invokeCollectionsDelete(context.Background(), resolved, res, red, a)
		}, `{"collection_id":"` + ownCollection + `"}`},
		"logs.list": {func(a json.RawMessage) (any, error) {
			return invokeLogsList(context.Background(), resolved, res, red, a)
		}, `{}`},
		"users.list": {func(a json.RawMessage) (any, error) {
			return invokeUsersList(context.Background(), resolved, res, red, a)
		}, `{}`},
		"users.get": {func(a json.RawMessage) (any, error) {
			return invokeUsersGet(context.Background(), resolved, res, red, a)
		}, `{"user_id":"u1"}`},
		"users.update": {func(a json.RawMessage) (any, error) {
			return invokeUsersUpdate(context.Background(), resolved, res, red, a)
		}, `{"user_id":"u1","name":"N"}`},
		"users.remove": {func(a json.RawMessage) (any, error) {
			return invokeUsersRemove(context.Background(), resolved, res, red, a)
		}, `{"user_id":"u1"}`},
	} {
		if _, err := call.run(json.RawMessage(call.args)); err == nil || strings.Contains(err.Error(), ownCollection) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if len(calls) != 0 || reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, reads)
	}
}

// A binding without a scene target behaves as before: the listed scene of another binding is not reachable.
func TestCollectionBindingIgnoresSceneTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sceneHandler(sceneCollections(), &calls))
	if _, err := env.invoke(scenesGet.ID, "one", `{"scene_id":"`+listedInOther+`"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if _, err := env.invoke(scenesGet.ID, "every", `{"scene_id":"`+listedInOther+`"}`); err != nil {
		t.Fatalf("err = %v, want the wildcard to read any scene", err)
	}
}

func TestReaches(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	collectionTools := map[string]bool{collectionsList.ID: true, scenesList.ID: true, scenesCreate.ID: true, collectionsDelete.ID: true}
	workspaceTools := map[string]bool{logsList.ID: true, usersList.ID: true, usersGet.ID: true, usersUpdate.ID: true, usersRemove.ID: true}
	for _, tool := range metadata.Tools {
		for name, targets := range map[string][]string{"wildcard": {"*"}, "collection": {"collection/c1"},
			"mixed": {"collection/c1", "scene/s1"}} {
			if reached, hint := metadata.Target.Reach(tool.ID, targets); !reached || hint != "" {
				t.Errorf("%s %s: reached = %t, %q", name, tool.ID, reached, hint)
			}
		}
		reached, hint := metadata.Target.Reach(tool.ID, []string{"scene/s1", "scene/s2"})
		switch {
		case collectionTools[tool.ID]:
			if reached || hint != "needs collection/COLLECTION_ID or *" {
				t.Errorf("scene %s: reached = %t, %q", tool.ID, reached, hint)
			}
		case workspaceTools[tool.ID]:
			if reached || hint != "needs *" {
				t.Errorf("scene %s: reached = %t, %q", tool.ID, reached, hint)
			}
		default:
			if !reached || hint != "" {
				t.Errorf("scene %s: reached = %t, %q", tool.ID, reached, hint)
			}
		}
	}
}
