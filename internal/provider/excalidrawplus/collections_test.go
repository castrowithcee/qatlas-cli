package excalidrawplus

import (
	"net/http"
	"strings"
	"testing"
)

func TestCollectionsListFiltersToAllowedAndPaginates(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"limit":2,"offset":4,"hasNextPage":true,"data":[
			{"id":"col-own","name":"Own","isDefault":true,"creator":"u1","teams":["t"]},
			{"id":"col-foreign","name":"Foreign"},
			{"id":"col-two","name":"Trash","isDeleted":true}]}`), nil
	})
	result, err := env.invoke(collectionsList.ID, "one", `{"offset":4,"limit":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, "Foreign") || strings.Contains(result, "Trash") || strings.Contains(result, "u1") ||
		!strings.Contains(result, `"id":"col-own"`) || !strings.Contains(result, `"next_offset":6`) ||
		!strings.Contains(result, `"count":1`) {
		t.Fatalf("result = %s", result)
	}
	if calls[0].query.Get("offset") != "4" || calls[0].query.Get("limit") != "2" {
		t.Fatalf("query = %v", calls[0].query)
	}
	result, err = env.invoke(collectionsList.ID, "every", `{}`)
	if err != nil || !strings.Contains(result, "Foreign") || strings.Contains(result, "Trash") {
		t.Fatalf("wildcard result = %s err = %v", result, err)
	}
}

// A page size above the documented range is refused before any secret is read or request sent.
func TestPaginationBounds(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil })
	for _, args := range []string{`{"limit":101}`, `{"limit":0}`, `{"offset":-1}`, `{"offset":1000001}`} {
		if _, err := env.invoke(collectionsList.ID, "every", args); !isInvalidRequest(err) {
			t.Errorf("%s err = %v, want invalid request", args, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls=%v reads=%d, want none", calls, *env.reads)
	}
}
