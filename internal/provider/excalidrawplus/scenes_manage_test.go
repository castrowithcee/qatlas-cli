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

type sentRequest struct{ method, path, body string }

// manageHandler answers metadata reads from scenes and records every changing request.
func manageHandler(scenes map[string]string, sent *[]sentRequest, change func() (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, apiPath+"/scenes/") {
			id := strings.TrimPrefix(r.URL.Path, apiPath+"/scenes/")
			if c, ok := scenes[id]; ok {
				return jsonResponse(200, entryJSON(id, "Old", c)), nil
			}
			return jsonResponse(404, `{}`), nil
		}
		body, _ := io.ReadAll(r.Body)
		*sent = append(*sent, sentRequest{r.Method, r.URL.Path, string(body)})
		return change()
	}
}

func (e *environment) invokeConfirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true})
	return string(response.Result), err
}

func okChange(id, collection string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return jsonResponse(200, entryJSON(id, "New", collection)), nil }
}

func TestCreateSendsOneRequestIntoTheOnlyCollection(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, manageHandler(nil, &sent, okChange("s-new", ownCollection)))
	if _, err := env.core.Invoke(context.Background(), application.InvokeRequest{Operation: scenesCreate.ID,
		Connection: "one", Arguments: json.RawMessage(`{"name":"Plan"}`)}); err == nil {
		t.Fatal("create without confirm succeeded")
	}
	if len(calls) != 0 {
		t.Fatalf("unconfirmed create sent %d requests", len(calls))
	}
	out, err := env.invokeConfirmed(scenesCreate.ID, "one", `{"name":"Plan"}`)
	if err != nil || !strings.Contains(out, `"id":"s-new"`) {
		t.Fatalf("out = %s, err = %v", out, err)
	}
	if len(sent) != 1 || sent[0].method != http.MethodPost || sent[0].path != apiPath+"/scenes" ||
		sent[0].body != `{"name":"Plan","pinned":false,"collectionId":"`+ownCollection+`"}` {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestCreateRefusedOutsideCollectionsBeforeIO(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, manageHandler(nil, &sent, okChange("s", ownCollection)))
	for name, args := range map[string][2]string{
		"foreign":  {"one", `{"name":"x","collection_id":"` + foreignColl + `"}`},
		"several":  {"two", `{"name":"x"}`},
		"wildcard": {"every", `{"name":"x"}`},
		"private":  {"every", `{"name":"x","collection_id":"private"}`},
		"name":     {"one", `{"name":"a\u0000b"}`},
	} {
		if _, err := env.invokeConfirmed(scenesCreate.ID, args[0], args[1]); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid request", name, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestUpdateRenamesAndMoves(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, manageHandler(map[string]string{"s1": ownCollection}, &sent, okChange("s1", otherAllowed)))
	if _, err := env.invokeConfirmed(scenesUpdate.ID, "two", `{"scene_id":"s1","name":"N","collection_id":"`+otherAllowed+`"}`); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].method != http.MethodPatch || sent[0].path != apiPath+"/scenes/s1" ||
		sent[0].body != `{"name":"N","collectionId":"`+otherAllowed+`"}` {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestUpdateRefusals(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, manageHandler(map[string]string{"s1": ownCollection, "sf": foreignColl}, &sent, okChange("s1", ownCollection)))
	// Moving into a collection that is not allowed is refused before I/O.
	if _, err := env.invokeConfirmed(scenesUpdate.ID, "two", `{"scene_id":"s1","collection_id":"`+foreignColl+`"}`); !isInvalidRequest(err) {
		t.Fatalf("move err = %v", err)
	}
	if _, err := env.invokeConfirmed(scenesUpdate.ID, "one", `{"scene_id":"s1"}`); !isInvalidRequest(err) {
		t.Fatalf("empty err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
	// A scene of a foreign collection is read for binding only; no PATCH is sent.
	_, err := env.invokeConfirmed(scenesUpdate.ID, "one", `{"scene_id":"sf","name":"x"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignColl) {
		t.Fatalf("foreign err = %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %+v, want none", sent)
	}
	if _, err := env.core.Invoke(context.Background(), application.InvokeRequest{Operation: scenesUpdate.ID,
		Connection: "one", Arguments: json.RawMessage(`{"scene_id":"s1","name":"x"}`)}); err == nil {
		t.Fatal("update without confirm succeeded")
	}
}

func TestChangeUncertainWithoutRetry(t *testing.T) {
	cases := map[string]func() (*http.Response, error){
		"5xx":      func() (*http.Response, error) { return jsonResponse(500, foreignCanary), nil },
		"abort":    func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"badid":    func() (*http.Response, error) { return jsonResponse(200, entryJSON("other", "n", ownCollection)), nil },
	}
	for name, change := range cases {
		var calls []call
		var sent []sentRequest
		env := newEnvironment(t, &calls, manageHandler(map[string]string{"s1": ownCollection}, &sent, change))
		for _, op := range [][2]string{{scenesCreate.ID, `{"name":"x"}`}, {scenesUpdate.ID, `{"scene_id":"s1","name":"x"}`}} {
			sent = nil
			if name == "badid" && op[0] == scenesCreate.ID {
				continue // any new identifier is valid for a created scene
			}
			_, err := env.invokeConfirmed(op[0], "one", op[1])
			var providerErr *provider.Error
			if err == nil || !errors.As(err, &providerErr) || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s %s: err = %v", name, op[0], err)
			}
			if !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s %s: no uncertainty hint: %v", name, op[0], err)
			}
			if len(sent) != 1 {
				t.Errorf("%s %s: %d change requests, want exactly 1", name, op[0], len(sent))
			}
		}
	}
}
