package excalidrawplus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// deleteHandler answers scene and collection reads and records every other request.
func deleteHandler(collections map[string]string, scenes map[string]string, sent *[]sentRequest,
	change func() (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	scene := manageHandler(scenes, sent, change)
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, apiPath+"/collections/") {
			id := strings.TrimPrefix(r.URL.Path, apiPath+"/collections/")
			if flag, ok := collections[id]; ok {
				return jsonResponse(200, `{"id":"`+id+`","name":"C","isDefault":`+flag+`}`), nil
			}
			return jsonResponse(404, `{}`), nil
		}
		if r.Method == http.MethodDelete {
			_, _ = io.ReadAll(r.Body)
			*sent = append(*sent, sentRequest{r.Method, r.URL.Path, ""})
			return change()
		}
		return scene(r)
	}
}

func okDelete() (*http.Response, error) { return jsonResponse(200, `{}`), nil }

func TestDeleteSceneAndCollectionSendOneRequest(t *testing.T) {
	for _, c := range []struct{ op, args, path string }{
		{scenesDelete.ID, `{"scene_id":"s1"}`, apiPath + "/scenes/s1"},
		{collectionsDelete.ID, `{"collection_id":"` + ownCollection + `"}`, apiPath + "/collections/" + ownCollection},
	} {
		var calls []call
		var sent []sentRequest
		env := newEnvironment(t, &calls, deleteHandler(map[string]string{ownCollection: "false"},
			map[string]string{"s1": ownCollection}, &sent, okDelete))
		out, err := env.invokeConfirmed(c.op, "listed", c.args)
		if err != nil || !strings.Contains(out, `"deleted":true`) {
			t.Fatalf("%s: out = %s, err = %v", c.op, out, err)
		}
		if len(sent) != 1 || sent[0].method != http.MethodDelete || sent[0].path != c.path {
			t.Fatalf("%s: sent = %+v", c.op, sent)
		}
	}
}

func TestDeleteNeedsToolListAndConfirm(t *testing.T) {
	if !scenesDelete.RequiresToolAllowList || !collectionsDelete.RequiresToolAllowList {
		t.Fatal("both deletions require the tools list")
	}
	for _, c := range []struct{ op, args string }{
		{scenesDelete.ID, `{"scene_id":"s1"}`},
		{collectionsDelete.ID, `{"collection_id":"` + ownCollection + `"}`},
	} {
		var calls []call
		var sent []sentRequest
		env := newEnvironment(t, &calls, deleteHandler(map[string]string{ownCollection: "false"},
			map[string]string{"s1": ownCollection}, &sent, okDelete))
		if _, err := env.invokeConfirmed(c.op, "one", c.args); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s without tools list: err = %v, calls = %d", c.op, err, len(calls))
		}
		if _, err := env.core.Invoke(context.Background(), application.InvokeRequest{Operation: c.op,
			Connection: "listed", Arguments: json.RawMessage(c.args)}); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s without confirm: err = %v, calls = %d", c.op, err, len(calls))
		}
	}
}

func TestDeleteRefusesForeignWithoutDeleteOrName(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, deleteHandler(map[string]string{foreignColl: "false", "private": "false"},
		map[string]string{"sf": foreignColl}, &sent, okDelete))
	// A foreign scene is read once and never deleted.
	_, err := env.invokeConfirmed(scenesDelete.ID, "listed", `{"scene_id":"sf"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignColl) {
		t.Fatalf("foreign scene: err = %v", err)
	}
	// A foreign or private collection is refused locally, before any secret or request.
	before := len(calls)
	reads := *env.reads
	for _, id := range []string{foreignColl, "private"} {
		conn := "listed"
		if id == "private" {
			conn = "listedAll"
		}
		_, err := env.invokeConfirmed(collectionsDelete.ID, conn, `{"collection_id":"`+id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignColl) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if len(calls) != before || *env.reads != reads || len(sent) != 0 {
		t.Fatalf("calls = %d, reads = %d, sent = %+v", len(calls)-before, *env.reads-reads, sent)
	}
}

func TestDeleteRefusesDefaultCollection(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, deleteHandler(map[string]string{ownCollection: "true"}, nil, &sent, okDelete))
	_, err := env.invokeConfirmed(collectionsDelete.ID, "listed", `{"collection_id":"`+ownCollection+`"}`)
	if !isInvalidRequest(err) || len(sent) != 0 {
		t.Fatalf("err = %v, sent = %+v", err, sent)
	}
}

func TestDeleteChangeUncertainWithoutRetry(t *testing.T) {
	cases := map[string]func() (*http.Response, error){
		"5xx":     func() (*http.Response, error) { return jsonResponse(500, foreignCanary), nil },
		"timeout": func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"abort":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
	}
	for name, change := range cases {
		for _, op := range [][2]string{{scenesDelete.ID, `{"scene_id":"s1"}`},
			{collectionsDelete.ID, `{"collection_id":"` + ownCollection + `"}`}} {
			var calls []call
			var sent []sentRequest
			env := newEnvironment(t, &calls, deleteHandler(map[string]string{ownCollection: "false"},
				map[string]string{"s1": ownCollection}, &sent, change))
			_, err := env.invokeConfirmed(op[0], "listed", op[1])
			var providerErr *provider.Error
			if err == nil || !errors.As(err, &providerErr) || strings.Contains(err.Error(), foreignCanary) ||
				!strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s %s: err = %v", name, op[0], err)
			}
			if len(sent) != 1 {
				t.Errorf("%s %s: %d requests, want exactly 1", name, op[0], len(sent))
			}
		}
	}
}

func TestDeleteProviderTextStaysOutOfErrors(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, deleteHandler(map[string]string{ownCollection: "false"},
		map[string]string{"s1": ownCollection}, &sent, func() (*http.Response, error) {
			return jsonResponse(400, foreignCanary), nil
		}))
	for _, op := range [][2]string{{scenesDelete.ID, `{"scene_id":"s1"}`},
		{collectionsDelete.ID, `{"collection_id":"` + ownCollection + `"}`}} {
		if _, err := env.invokeConfirmed(op[0], "listed", op[1]); err == nil || strings.Contains(err.Error(), foreignCanary) {
			t.Errorf("%s: err = %v", op[0], err)
		}
	}
}

func TestDeleteContract(t *testing.T) {
	for _, d := range []string{string(scenesDelete.Risk.Effect), string(collectionsDelete.Risk.Effect)} {
		if d != "delete" {
			t.Errorf("effect = %s", d)
		}
	}
	for _, r := range []struct {
		version     int
		idempotency string
		confirm     string
		open        bool
		sens        string
	}{
		{scenesDelete.Version, string(scenesDelete.Risk.Idempotency), string(scenesDelete.Risk.Confirmation), scenesDelete.Risk.OpenWorld, scenesDelete.Risk.DataSensitivity},
		{collectionsDelete.Version, string(collectionsDelete.Risk.Idempotency), string(collectionsDelete.Risk.Confirmation), collectionsDelete.Risk.OpenWorld, collectionsDelete.Risk.DataSensitivity},
	} {
		if r.version != 1 || r.idempotency != "unknown" || r.confirm != "required" || !r.open || r.sens != dataSensitivity {
			t.Errorf("contract = %+v", r)
		}
	}
}
