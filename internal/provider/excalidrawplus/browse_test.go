package excalidrawplus

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func browseHandler(r *http.Request) (*http.Response, error) {
	switch r.URL.Path {
	case apiPath + "/collections":
		next := "false"
		if r.URL.Query().Get("offset") == "0" {
			next = "true"
		}
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":`+next+`,"data":[`+
			`{"id":"col-own","name":"Own\u0007 `+strings.Repeat("x", 300)+`"},{"id":"col-gone","name":"Trash","isDeleted":true},`+
			`{"id":"bad/id","name":"Skip"}]}`), nil
	case apiPath + "/scenes":
		return jsonResponse(200, `{"limit":50,"offset":0,"hasNextPage":false,"data":[`+
			entryJSON("s-own", "Plan A", r.URL.Query().Get("collectionId"))+","+entryJSON("s-other", "Plan B", "col-elsewhere")+`]}`), nil
	}
	return jsonResponse(404, foreignCanary), nil
}

func browse(t *testing.T, calls *[]call, req config.TargetListRequest) (config.TargetPage, error) {
	t.Helper()
	serve(t, calls, browseHandler)
	red := &redact.Redactor{}
	res := secret.NewWith(func(string) string { return apiKeyValue }, nil, nil, red)
	// The connection's targets do not name the listed entries: the browser reads beyond them.
	return browseList(context.Background(), resolvedConnection("collection/other-bound"), res, red, req)
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target)
	}
	return out
}

func TestBrowseLevels(t *testing.T) {
	var calls []call
	page, err := browse(t, &calls, config.TargetListRequest{Kind: "collection"})
	if err != nil || !slices.Equal(targetsOf(page), []string{"collection/col-own"}) || page.Next != "50" || page.ServerSearch ||
		!page.Entries[0].HasChildren || len(calls) != 1 || calls[0].query.Get("offset") != "0" || calls[0].query.Get("limit") != "50" {
		t.Fatalf("collections = %+v, %v, calls %+v", page, err, calls)
	}
	if name := page.Entries[0].Name; strings.ContainsRune(name, 7) || len(name) > maxBrowseText {
		t.Fatalf("name = %q", name)
	}
	calls = nil
	page, err = browse(t, &calls, config.TargetListRequest{Kind: "collection", Cursor: "50"})
	if err != nil || page.Next != "" || calls[0].query.Get("offset") != "50" {
		t.Fatalf("second page = %+v, %v, calls %+v", page, err, calls)
	}
	calls = nil
	page, err = browse(t, &calls, config.TargetListRequest{Kind: "scene", Parent: "collection/col-own"})
	if err != nil || !slices.Equal(targetsOf(page), []string{"scene/s-own"}) || page.Entries[0].HasChildren ||
		calls[0].query.Get("collectionId") != "col-own" {
		t.Fatalf("scenes = %+v, %v, calls %+v", page, err, calls)
	}
}

func TestBrowseRefusals(t *testing.T) {
	for name, req := range map[string]config.TargetListRequest{
		"kind":        {Kind: "workspace"},
		"no parent":   {Kind: "scene"},
		"bad parent":  {Kind: "scene", Parent: "scene/x"},
		"bad cursor":  {Kind: "collection", Cursor: "-1"},
		"text cursor": {Kind: "collection", Cursor: "abc"},
	} {
		var calls []call
		if _, err := browse(t, &calls, req); !isInvalidRequest(err) || len(calls) != 0 {
			t.Errorf("%s: err = %v, calls %d", name, err, len(calls))
		}
	}
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(404, foreignCanary), nil })
	red := &redact.Redactor{}
	res := secret.NewWith(func(string) string { return apiKeyValue }, nil, nil, red)
	_, err := browseList(context.Background(), resolvedConnection("*"), res, red, config.TargetListRequest{Kind: "collection"})
	if classOf(err) == "" || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
}

// The documentation names no web interface URL with collection or scene identifiers, so no URL yields a target.
func TestResolveURLResolvesNothing(t *testing.T) {
	for _, raw := range []string{"https://plus.excalidraw.com/", "https://plus.excalidraw.com/collection/col-own",
		"https://app.excalidraw.com/s/s-own", "https://api.excalidraw.com/api/v1/scenes/s-own", "https://example.com/scene/s-own"} {
		entries, err := browseResolveURL(context.Background(), resolvedConnection("*"), nil, nil, defaultOrigin, raw)
		if err != nil || len(entries) != 0 {
			t.Errorf("%s = %v, %v, want nothing", raw, entries, err)
		}
	}
}
