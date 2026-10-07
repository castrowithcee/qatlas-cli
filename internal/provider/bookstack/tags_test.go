package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// tagServer serves tag names, tag values, and system information. ignoreFilter makes it answer like an
// instance that does not know the filter fields.
func tagServer(t *testing.T, names []map[string]any, ignoreFilter bool) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/tags", "/api/tags/values-for-name":
			rows := names
			offset := 0
			offset, _ = strconv.Atoi(q.Get("offset"))
			if offset > len(rows) {
				offset = len(rows)
			}
			count := 0
			count, _ = strconv.Atoi(q.Get("count"))
			end := min(len(rows), offset+count)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows[offset:end], "total": len(rows)})
		case "/api/system":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "v1", "app_name": "Wiki", "instance_id": "abc",
				"base_url": "https://docs.example.com", "app_logo": "https://docs.example.com/logo.png"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	_ = ignoreFilter
	return srv, rec
}

func tagRow(name, value string) map[string]any {
	return map[string]any{"name": name, "value": value, "values": 2, "usages": 3, "page_count": 1,
		"chapter_count": 1, "book_count": 1, "shelf_count": 0, "hidden": "x"}
}

func TestTagsAreRefusedOnABoundConnectionBeforeSecretsAndIO(t *testing.T) {
	srv, rec := tagServer(t, nil, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for id, args := range map[string]string{tagsList.ID: `{}`, tagsValues.ID: `{"name":"k"}`} {
		_, err := lookup(t, id)(context.Background(), boundResolved(srv.URL, "book/7"), resolver(nil), nil, json.RawMessage(args))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "7") {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if m, _, _, _ := rec.snapshot(); len(m) != 0 {
		t.Errorf("requests = %v, want none", m)
	}
}

func TestSystemGetWorksWithTargetsAndOmitsTheLogo(t *testing.T) {
	srv, rec := tagServer(t, nil, false)
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	result, err := lookup(t, systemGet.ID)(context.Background(), boundResolved(srv.URL, "book/7"), resolver(nil), nil, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "logo") || !strings.Contains(string(encoded), `"Value":"abc"`) {
		t.Errorf("result = %s", encoded)
	}
	if _, paths, _, _ := rec.snapshot(); !reflect.DeepEqual(paths, []string{"/api/system"}) {
		t.Errorf("paths = %v", paths)
	}
	if systemGet.Group != "" || withAdministrationGroup(systemGet).Group != administrationGroup {
		t.Error("system.get must belong to the administration group")
	}
}

func TestTagFilterIsEscapedAndChecked(t *testing.T) {
	rows := []map[string]any{tagRow("100%_done", "a"), tagRow("plain", "b"), tagRow("100x", "c")}
	srv, rec := tagServer(t, rows, true)
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	result, err := lookup(t, tagsList.ID)(context.Background(), boundResolved(srv.URL), resolver(nil), nil,
		json.RawMessage(`{"name_contains":"0%_"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(output.Collection).Rows
	if len(got) != 1 || got[0]["name"] != "100%_done" || got[0]["hidden"] != nil {
		t.Errorf("rows = %v, want only the row that satisfies the filter", got)
	}
	_, _, queries, _ := rec.snapshot()
	q, _ := url.ParseQuery(queries[0])
	if q.Get("filter[name:like]") != `%0\%\_%` {
		t.Errorf("filter = %q", q.Get("filter[name:like]"))
	}
	if likeContains(`a\b`) != `%a\\b%` {
		t.Errorf("backslash = %q", likeContains(`a\b`))
	}
}

func TestTagValuesRequestAndPagination(t *testing.T) {
	var rows []map[string]any
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		rows = append(rows, tagRow("Cat", v))
	}
	srv, rec := tagServer(t, rows, false)
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	result, err := lookup(t, tagsValues.ID)(context.Background(), boundResolved(srv.URL), resolver(nil), nil,
		json.RawMessage(`{"name":"Cat","limit":2,"offset":1}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(output.Collection).Rows
	if len(got) != 2 || got[0]["value"] != "b" || got[1]["value"] != "c" {
		t.Errorf("rows = %v", got)
	}
	_, paths, queries, _ := rec.snapshot()
	q, _ := url.ParseQuery(queries[0])
	if paths[0] != "/api/tags/values-for-name" || q.Get("name") != "Cat" || q.Has("filter[value:like]") {
		t.Errorf("request = %s?%s", paths[0], queries[0])
	}
	if _, err := lookup(t, tagsValues.ID)(context.Background(), boundResolved(srv.URL), resolver(nil), nil,
		json.RawMessage(`{"name":""}`)); !isInvalidRequest(err) {
		t.Errorf("empty name: %v", err)
	}
}
