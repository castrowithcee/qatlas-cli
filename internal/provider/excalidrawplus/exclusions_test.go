package excalidrawplus

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const plainElement = `{"id":"e1","type":"rectangle","x":0,"y":0,"width":1,"height":1}`

const (
	exclScene     = "s-excl"
	exclInForeign = "s-in-foreign"
	freeScene     = "s-free"
)

// exclusionWorkspace is a workspace with a free, an excluded, and a foreign collection; exclScene sits in the
// own collection, exclInForeign in the excluded foreign one.
type exclusionWorkspace struct {
	calls   []call
	sent    []sentRequest
	scenes  map[string]string
	trashed map[string]bool
}

func newExclusionWorkspace() *exclusionWorkspace {
	return &exclusionWorkspace{scenes: map[string]string{exclScene: ownCollection, exclInForeign: foreignColl,
		freeScene: ownCollection}, trashed: map[string]bool{}}
}

func (w *exclusionWorkspace) handle(r *http.Request) (*http.Response, error) {
	path := strings.TrimPrefix(r.URL.Path, apiPath)
	switch {
	case r.Method != http.MethodGet:
		body, _ := io.ReadAll(r.Body)
		w.sent = append(w.sent, sentRequest{r.Method, r.URL.Path, string(body)})
		if r.Method == http.MethodDelete || r.Method == http.MethodPost {
			return jsonResponse(200, entryJSON("s-new", "New", ownCollection)), nil
		}
		return jsonResponse(200, entryJSON(freeScene, "New", ownCollection)), nil
	case path == "/collections":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":true,"data":[`+
			`{"id":"`+ownCollection+`","name":"Own"},{"id":"`+foreignColl+`","name":"Foreign"},`+
			`{"id":"`+otherAllowed+`","name":"Two"}]}`), nil
	case path == "/scenes":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+
			entryJSON(freeScene, "Plan free", ownCollection)+","+entryJSON(exclScene, "Plan excluded", ownCollection)+","+
			entryJSON(exclInForeign, "Plan foreign", foreignColl)+","+entryJSON("s-two", "Plan two", otherAllowed)+`]}`), nil
	case strings.HasSuffix(path, "/content"):
		return jsonResponse(200, contentBody), nil
	case strings.HasPrefix(path, "/collections/"):
		return jsonResponse(200, `{"id":"`+strings.TrimPrefix(path, "/collections/")+`","name":"C"}`), nil
	case strings.HasPrefix(path, "/scenes/"):
		id := strings.TrimPrefix(path, "/scenes/")
		if collection, ok := w.scenes[id]; ok {
			if w.trashed[id] {
				return jsonResponse(200, strings.Replace(entryJSON(id, "N", collection), `"collection"`, `"isDeleted":true,"collection"`, 1)), nil
			}
			return jsonResponse(200, entryJSON(id, "N", collection)), nil
		}
	}
	return jsonResponse(404, `{}`), nil
}

func (w *exclusionWorkspace) env(t *testing.T) *environment {
	return newEnvironment(t, &w.calls, w.handle)
}

func requireRefusal(t *testing.T, err error, hidden ...string) {
	t.Helper()
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "is outside the targets of this connection") {
		t.Fatalf("err = %v, want the outside-the-targets refusal", err)
	}
	for _, text := range append(hidden, "exclu", "Exclu") {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("err = %v, names %q", err, text)
		}
	}
}

func TestExclusionsFilterListings(t *testing.T) {
	w := newExclusionWorkspace()
	env := w.env(t)
	out, err := env.invoke(collectionsList.ID, "exclAll", `{}`)
	if err != nil || strings.Contains(out, foreignColl) || !strings.Contains(out, ownCollection) ||
		!strings.Contains(out, otherAllowed) || !strings.Contains(out, `"has_next_page":true`) {
		t.Fatalf("collections = %s, %v", out, err)
	}
	out, err = env.invoke(scenesList.ID, "exclAll", `{}`)
	if err != nil || strings.Contains(out, exclScene) || strings.Contains(out, exclInForeign) ||
		!strings.Contains(out, freeScene) || !strings.Contains(out, "s-two") || len(w.calls) != 2 {
		t.Fatalf("scenes = %s, %v, calls %d", out, err, len(w.calls))
	}
	out, err = env.invoke(scenesList.ID, "exclAll", `{"name":"plan"}`)
	if err != nil || strings.Contains(out, "excluded") || strings.Contains(out, "foreign") || !strings.Contains(out, `"count":2`) {
		t.Fatalf("scenes by name = %s, %v", out, err)
	}
	out, err = env.invoke(scenesList.ID, "exclAll", `{"name":"excluded"}`)
	if err != nil || !strings.Contains(out, `"count":0`) {
		t.Fatalf("scenes by excluded name = %s, %v", out, err)
	}
}

func TestExcludedCollectionAndSceneAreRefusedLocally(t *testing.T) {
	w := newExclusionWorkspace()
	env := w.env(t)
	for _, c := range []struct{ op, connection, args string }{
		{scenesList.ID, "exclAll", `{"collection_id":"` + foreignColl + `"}`},
		{scenesGet.ID, "exclAll", `{"scene_id":"` + exclScene + `"}`},
		{scenesContent.ID, "exclAll", `{"scene_id":"` + exclScene + `"}`},
	} {
		_, err := env.invoke(c.op, c.connection, c.args)
		requireRefusal(t, err, foreignColl, exclScene)
	}
	for _, c := range []struct{ op, connection, args string }{
		{scenesCreate.ID, "exclAll", `{"name":"N","collection_id":"` + foreignColl + `"}`},
		{scenesUpdate.ID, "exclAll", `{"scene_id":"` + exclScene + `","name":"N"}`},
		{scenesUpdate.ID, "exclAll", `{"scene_id":"` + freeScene + `","collection_id":"` + foreignColl + `"}`},
		{contentPatch.ID, "exclAll", `{"scene_id":"` + exclScene + `","elements":[` + textElement + `]}`},
		{contentReplace.ID, "exclAll", `{"scene_id":"` + exclScene + `","elements":[` + plainElement + `]}`},
		{scenesDelete.ID, "exclAll", `{"scene_id":"` + exclScene + `"}`},
		{collectionsDelete.ID, "exclAll", `{"collection_id":"` + foreignColl + `"}`},
	} {
		_, err := env.invokeConfirmed(c.op, c.connection, c.args)
		requireRefusal(t, err, foreignColl, exclScene)
	}
	if len(w.calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want no secret and no request", w.calls, *env.reads)
	}
}

// A scene of an excluded collection is refused after its own metadata, before any content request or change.
func TestSceneInExcludedCollectionIsRefusedAfterTheProof(t *testing.T) {
	w := newExclusionWorkspace()
	env := w.env(t)
	for _, c := range []struct{ op, connection, args string }{
		{scenesGet.ID, "exclAll", `{"scene_id":"` + exclInForeign + `"}`},
		{scenesContent.ID, "exclAll", `{"scene_id":"` + exclInForeign + `"}`},
		{scenesContent.ID, "exclScene", `{"scene_id":"` + exclInForeign + `"}`},
		{scenesUpdate.ID, "exclAll", `{"scene_id":"` + exclInForeign + `","name":"N"}`},
		{contentPatch.ID, "exclAll", `{"scene_id":"` + exclInForeign + `","elements":[` + textElement + `]}`},
		{contentReplace.ID, "exclAll", `{"scene_id":"` + exclInForeign + `","elements":[` + plainElement + `]}`},
		{scenesDelete.ID, "exclAll", `{"scene_id":"` + exclInForeign + `"}`},
	} {
		w.calls = nil
		_, err := env.invokeConfirmed(c.op, c.connection, c.args)
		requireRefusal(t, err, foreignColl, exclInForeign)
		if len(w.calls) != 1 || w.calls[0].method != http.MethodGet || strings.HasSuffix(w.calls[0].path, "/content") {
			t.Fatalf("%s: calls = %+v, want only the metadata read", c.op, w.calls)
		}
	}
	if len(w.sent) != 0 {
		t.Fatalf("sent = %+v, want no change", w.sent)
	}
}

func TestExclusionsLeaveOtherWaysOpen(t *testing.T) {
	w := newExclusionWorkspace()
	env := w.env(t)
	if _, err := env.invoke(scenesContent.ID, "exclAll", `{"scene_id":"`+freeScene+`"}`); err != nil {
		t.Fatalf("content of a free scene = %v", err)
	}
	// A reachable scene moves anywhere but into an excluded collection; an exclusion that hits nothing is inert.
	if _, err := env.invokeConfirmed(scenesUpdate.ID, "exclAll", `{"scene_id":"`+freeScene+`","collection_id":"`+otherAllowed+`"}`); err != nil {
		t.Fatalf("move = %v", err)
	}
	if _, err := env.invokeConfirmed(scenesCreate.ID, "exclAll", `{"name":"N","collection_id":"`+otherAllowed+`"}`); err != nil {
		t.Fatalf("create = %v", err)
	}
	if _, err := env.invoke(scenesGet.ID, "exclOne", `{"scene_id":"`+freeScene+`"}`); err != nil {
		t.Fatalf("scenes.get = %v", err)
	}
	if _, err := env.invoke(scenesGet.ID, "exclOne", `{"scene_id":"`+exclScene+`"}`); !isInvalidRequest(err) {
		t.Fatalf("excluded scene on a single collection = %v", err)
	}
}

func TestCollectionDeleteChecksExcludedScenes(t *testing.T) {
	w := newExclusionWorkspace()
	env := w.env(t)
	args := `{"collection_id":"` + ownCollection + `"}`
	for _, trashed := range []bool{false, true} {
		w.calls, w.sent = nil, nil
		w.trashed[exclScene] = trashed
		_, err := env.invokeConfirmed(collectionsDelete.ID, "exclAll", args)
		requireRefusal(t, err, exclScene)
		if len(w.sent) != 0 || len(w.calls) != 2 || w.calls[1].path != apiPath+"/scenes/"+exclScene {
			t.Fatalf("trashed=%t: calls = %+v, sent = %+v, want a collection read, one scene read, no DELETE", trashed, w.calls, w.sent)
		}
	}
	// An excluded scene of another collection, or one the workspace does not know, does not stop the deletion.
	w.calls, w.sent = nil, nil
	out, err := env.invokeConfirmed(collectionsDelete.ID, "exclAll", `{"collection_id":"`+otherAllowed+`"}`)
	if err != nil || !strings.Contains(out, `"deleted":true`) || len(w.sent) != 1 {
		t.Fatalf("delete = %s, %v, sent %+v", out, err, w.sent)
	}
	w.calls, w.sent = nil, nil
	delete(w.scenes, exclScene)
	out, err = env.invokeConfirmed(collectionsDelete.ID, "exclAll", args)
	if err != nil || !strings.Contains(out, `"deleted":true`) || len(w.sent) != 1 || w.sent[0].method != http.MethodDelete {
		t.Fatalf("delete after 404 = %s, %v, sent %+v", out, err, w.sent)
	}
	w.sent = nil
	w.scenes[exclScene] = ownCollection
	w.trashed = map[string]bool{}
	w.calls = nil
	failing := newEnvironment(t, &w.calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/scenes/"+exclScene) {
			return jsonResponse(503, `{}`), nil
		}
		return w.handle(r)
	})
	if _, err := failing.invokeConfirmed(collectionsDelete.ID, "exclAll", args); err == nil || len(w.sent) != 0 {
		t.Fatalf("an unreadable excluded scene must stop the deletion: err = %v, sent %+v", err, w.sent)
	}
}

func TestLogsAndUsersStayOfferedWithExclusions(t *testing.T) {
	for _, tool := range []string{logsList.ID, usersList.ID, usersGet.ID, usersUpdate.ID, usersRemove.ID, scenesList.ID,
		collectionsList.ID} {
		if ok, _ := reaches(tool, []string{"*"}); !ok {
			t.Errorf("%s is not offered for *", tool)
		}
	}
	if !strings.Contains(logsList.Description, "Exclusions of this connection do not apply") {
		t.Error("logs.list does not warn about exclusions")
	}
}

func TestRegisterDeclaresExclusionsAndChecksThem(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok || !meta.Target.Excludes() {
		t.Fatal("the provider does not declare exclusions")
	}
	for _, kind := range meta.Target.Kinds {
		if !(len(kind.ExcludeForms) > 0) {
			t.Errorf("kind %s is not excludable", kind.Name)
		}
	}
	targets := []string{"*"}
	if got := meta.Target.CheckExcludes([]string{"collection/B", "scene/X", "collection/unrelated"}, targets); len(got) != 0 {
		t.Errorf("valid exclusions: %+v", got)
	}
	for _, bad := range []string{"*", "collection/*", "scene/", "folder/x", "x", " scene/x", "collection/a/b"} {
		if got := meta.Target.CheckExcludes([]string{bad}, targets); len(got) != 1 {
			t.Errorf("CheckExcludes(%q) = %+v", bad, got)
		}
	}
	if got := meta.Target.CheckExcludes([]string{"collection/A"}, []string{"collection/A"}); len(got) != 1 {
		t.Errorf("an exclusion equal to a target is valid: %+v", got)
	}
}

func TestParseScopeFailsClosedOnInvalidExclusions(t *testing.T) {
	for _, bad := range [][]string{{"*"}, {"collection/*"}, {"x"}, {"scene/a", "scene/a"}, {"scene/t"}, {"collection/t"}} {
		if _, err := parseScope([]string{"collection/t", "scene/t"}, bad...); err == nil {
			t.Errorf("exclusions %v were accepted", bad)
		}
	}
	bound, err := parseScope([]string{"*"}, "scene/a", "collection/b")
	if err != nil || !bound.wildcard || !bound.excludesScene("a") || !bound.excludesCollection("b") {
		t.Fatalf("scope = %+v, %v", bound, err)
	}
	// Exclusions never change what the binding itself is.
	bound, _ = parseScope([]string{"collection/a"}, "collection/b")
	if only, ok := bound.single(); !ok || only != "a" || bound.sceneOnly() {
		t.Fatalf("scope = %+v", bound)
	}
	bound, _ = parseScope([]string{"scene/a"}, "scene/b")
	if !bound.sceneOnly() {
		t.Fatal("exclusions changed a scene-only binding")
	}
}
