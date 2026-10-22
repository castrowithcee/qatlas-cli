package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fileListCase struct {
	id, path, args, filters string
	want                    map[string]string
}

func fileListCases() []fileListCase {
	return []fileListCase{
		{commentsList.ID, "/api/comments", `{"page_id":42}`,
			`[{"field":"parent_id","op":"gt","value":"0"},{"field":"content_ref","op":"like","value":"a_b"},{"field":"created_by","value":"4"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"}]`,
			map[string]string{"filter[parent_id:gt]": "0", "filter[content_ref:like]": `%a\_b%`, "filter[created_by]": "4",
				"filter[updated_at:gte]": "2026-01-02 02:04:05", "filter[commentable_type]": "page", "filter[commentable_id]": "42"}},
		{attachmentsList.ID, "/api/attachments", `{"page_id":42}`,
			`[{"field":"external","value":"false"},{"field":"order","op":"lt","value":"3"},{"field":"extension","value":"pdf"}]`,
			map[string]string{"filter[external]": "0", "filter[order:lt]": "3", "filter[extension]": "pdf", "filter[uploaded_to]": "42"}},
		{imagesList.ID, "/api/image-gallery", `{"page_id":42,"type":"gallery"}`,
			`[{"field":"path","op":"like","value":"/up"},{"field":"uploaded_to","value":"9"},{"field":"type","value":"drawio"},{"field":"updated_by","value":"2"}]`,
			map[string]string{"filter[path:like]": "%/up%", "filter[updated_by]": "2", "filter[uploaded_to]": "42", "filter[type]": "gallery"}},
	}
}

// The file lists send sort and filters, and a filter on a key the list already fixes does not replace it.
func TestFileListsSendSortAndFilters(t *testing.T) {
	for _, tc := range fileListCases() {
		t.Run(tc.id, func(t *testing.T) {
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				if strings.HasPrefix(r.URL.Path, "/api/pages/") {
					_ = json.NewEncoder(w).Encode(page(42, "p"))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
			}))
			defer server.Close()
			args := strings.TrimSuffix(tc.args, "}") + `,"sort":"-created_at","filters":` + tc.filters + "}"
			if _, err := listHandler(t, tc.id)(context.Background(), unboundResolved(server.URL), resolver(nil), nil, json.RawMessage(args)); err != nil {
				t.Fatalf("handler = %v", err)
			}
			_, paths, queries, _ := rec.snapshot()
			var got url.Values
			for i, p := range paths {
				if p == tc.path {
					got, _ = url.ParseQuery(queries[i])
				}
			}
			if got == nil {
				t.Fatalf("no request to %s: %v", tc.path, paths)
			}
			if got.Get("sort") != "-created_at" {
				t.Errorf("sort = %q", got.Get("sort"))
			}
			for k, v := range tc.want {
				if got.Get(k) != v {
					t.Errorf("%s = %q, want %q (query %v)", k, got.Get(k), v, got)
				}
			}
		})
	}
}

// A server that ignores the filters delivers no foreign row, and limit and offset count the rows that remain.
func TestFileListsCheckFiltersOnTheClient(t *testing.T) {
	rec := &recorder{}
	comment := func(i int) map[string]any {
		parent := any(nil)
		if i%2 == 0 {
			parent = 3
		}
		return map[string]any{"id": i, "commentable_id": 42, "commentable_type": "page", "parent_id": parent,
			"local_id": i, "created_by": i % 3}
	}
	server := filterServer(rec, 12, comment)
	defer server.Close()
	client := newClient(t, server.URL, nil)

	q, err := parseListQuery(commentListSchema, "", []filterArg{{"parent_id", "gt", "0"}, {"created_by", "", "0"}})
	if err != nil {
		t.Fatal(err)
	}
	all, err := client.ListComments(context.Background(), 0, 0, 0, q)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(all.Rows)) != "[6 12]" {
		t.Errorf("ids = %v, want the replies of user 0", ids(all.Rows))
	}
	// parent_id 0 is a top-level comment, read from the raw row as shown.
	q, _ = parseListQuery(commentListSchema, "", []filterArg{{"parent_id", "", "0"}, {"local_id", "lte", "5"}})
	top, err := client.ListComments(context.Background(), 0, 2, 1, q)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(top.Rows)) != "[3 5]" {
		t.Errorf("ids = %v, want the second and third top-level comment", ids(top.Rows))
	}

	// A user filter on commentable_type cannot bring in a foreign type.
	q, _ = parseListQuery(commentListSchema, "", []filterArg{{"commentable_type", "", "chapter"}})
	none, err := client.ListComments(context.Background(), 0, 0, 0, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Rows) != 0 {
		t.Errorf("rows = %v, want none", none.Rows)
	}

	attachments := filterServer(rec, 6, func(i int) map[string]any {
		return map[string]any{"id": i, "name": "f", "extension": []string{"pdf", "txt"}[i%2], "uploaded_to": 42,
			"external": i > 3, "order": i}
	})
	defer attachments.Close()
	aq, _ := parseListQuery(attachmentListSchema, "", []filterArg{{"external", "", "false"}, {"extension", "", "PDF"}})
	got, err := newClient(t, attachments.URL, nil).ListAttachments(context.Background(), 0, 0, 0, aq)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got.Rows)) != "[2]" {
		t.Errorf("ids = %v, want the file attachment with extension pdf", ids(got.Rows))
	}

	images := filterServer(rec, 6, func(i int) map[string]any {
		return map[string]any{"id": i, "name": "i", "type": []string{"gallery", "drawio"}[i%2], "uploaded_to": 42,
			"path": fmt.Sprintf("/uploads/%d.png", i)}
	})
	defer images.Close()
	iq, _ := parseListQuery(imageListSchema, "", []filterArg{{"path", "like", "/3."}, {"type", "", "gallery"}})
	img, err := newClient(t, images.URL, nil).ListImages(context.Background(), 0, "", 0, 0, iq)
	if err != nil {
		t.Fatal(err)
	}
	if len(img.Rows) != 0 {
		t.Errorf("ids = %v, want none: image 3 is a drawing", ids(img.Rows))
	}
	iq, _ = parseListQuery(imageListSchema, "", []filterArg{{"path", "like", "/4."}, {"uploaded_to", "", "42"}})
	img, err = newClient(t, images.URL, nil).ListImages(context.Background(), 0, "", 0, 0, iq)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(img.Rows)) != "[4]" {
		t.Errorf("ids = %v, want [4]", ids(img.Rows))
	}
}

// A bound connection keeps its page check: a filter on uploaded_to or commentable_id cannot name another page.
func TestFileListsFilterKeepsTargetBinding(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasPrefix(r.URL.Path, "/api/pages/") {
			_ = json.NewEncoder(w).Encode(page(42, "p"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 2, "data": []any{
			map[string]any{"id": 1, "uploaded_to": 42, "commentable_id": 42, "commentable_type": "page", "type": "gallery"},
			map[string]any{"id": 2, "uploaded_to": 9, "commentable_id": 9, "commentable_type": "page", "type": "gallery"},
		}})
	}))
	defer server.Close()
	client := boundClient(t, server.URL, "book/7")
	ctx := context.Background()

	aq, _ := parseListQuery(attachmentListSchema, "", []filterArg{{"uploaded_to", "", "9"}})
	if got, err := client.ListAttachments(ctx, 42, 0, 0, aq); err != nil || len(got.Rows) != 0 {
		t.Errorf("attachments = %v, %v, want none", got.Rows, err)
	}
	cq, _ := parseListQuery(commentListSchema, "", []filterArg{{"commentable_id", "", "9"}})
	if got, err := client.ListComments(ctx, 42, 0, 0, cq); err != nil || len(got.Rows) != 0 {
		t.Errorf("comments = %v, %v, want none", got.Rows, err)
	}
	iq, _ := parseListQuery(imageListSchema, "", []filterArg{{"id", "gte", "1"}})
	got, err := client.ListImages(ctx, 42, "", 0, 0, iq)
	if err != nil || fmt.Sprint(ids(got.Rows)) != "[1]" {
		t.Errorf("images = %v, %v, want only the row on the page", got.Rows, err)
	}
	_, _, queries, _ := rec.snapshot()
	for _, q := range queries {
		if strings.Contains(q, "=9") {
			t.Errorf("query %s sends the foreign page", q)
		}
	}
}

// Invalid sort and filters of the file and tag lists end the call before a secret is read or a request is sent.
func TestFileAndTagListsRefuseInvalidQueriesFirst(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")

	cases := map[string][]string{
		commentsList.ID:    {`{"sort":"html"}`, `{"filters":[{"field":"html","value":"x"}]}`, `{"filters":[{"field":"parent_id","value":"x"}]}`},
		attachmentsList.ID: {`{"sort":"links"}`, `{"filters":[{"field":"external","op":"gt","value":"true"}]}`, `{"filters":[{"field":"order","value":"-1"}]}`},
		imagesList.ID:      {`{"sort":"thumbs"}`, `{"filters":[{"field":"nope","value":"x"}]}`, `{"filters":[{"field":"uploaded_to","op":"like","value":"1"}]}`},
		tagsList.ID:        {`{"sort":"id"}`, `{"sort":"+value"}`, `{"filters":[{"field":"name","value":"x"}]}`},
		tagsValues.ID:      {`{"name":"k","sort":"values"}`, `{"name":"k","filters":[{"field":"value","value":"x"}]}`},
	}
	for id, list := range cases {
		for _, args := range list {
			_, err := listHandler(t, id)(context.Background(), unboundResolved(server.URL), resolver(nil), nil, json.RawMessage(args))
			if err == nil {
				t.Errorf("%s %s: accepted", id, args)
			}
		}
	}
	// A sort is checked before the secret even when it passes the schema form.
	if _, err := parseListQuery(tagNameListSchema, "+page_count", nil); err != nil {
		t.Errorf("tag sort = %v", err)
	}
	if _, err := parseListQuery(tagNameListSchema, "", []filterArg{{"name", "", "x"}}); !isInvalidRequest(err) {
		t.Errorf("tag filters = %v, want an invalid-request", err)
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

// The tag lists sort by the requested field instead of the default and still drop repeated rows.
func TestTagListsSort(t *testing.T) {
	rows := []map[string]any{tagRow("b", "1"), tagRow("a", "2"), tagRow("a", "2"), tagRow("c", "3")}
	srv, rec := tagServer(t, rows, false)
	client := newClient(t, srv.URL, nil)

	names, err := client.ListTagNames(context.Background(), 0, 0, "", "-usages")
	if err != nil {
		t.Fatal(err)
	}
	if len(names.Rows) != 3 {
		t.Errorf("tag names = %v, want the repeated row dropped", names.Rows)
	}
	if _, err := client.ListTagValues(context.Background(), "a", "", 0, 0, "-page_count"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTagNames(context.Background(), 0, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTagValues(context.Background(), "a", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	_, _, queries, _ := rec.snapshot()
	for i, want := range []string{"sort=-usages", "sort=-page_count", "sort=%2Bname", "sort=%2Bvalue"} {
		if !strings.Contains(queries[i], want) {
			t.Errorf("query %d = %s, want %s", i, queries[i], want)
		}
	}
}

// A sorted tag scan over several requests drops repeats and ends when a request brings nothing new.
func TestTagScanSortedAcrossRequests(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 9, "data": []any{tagRow("a", ""), tagRow("b", ""), tagRow("b", "")}})
	}))
	defer server.Close()
	got, err := newClient(t, server.URL, nil).ListTagNames(context.Background(), 0, 0, "", "-values")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || requests != 2 {
		t.Errorf("rows = %d after %d requests, want 2 after 2", len(got.Rows), requests)
	}
}
