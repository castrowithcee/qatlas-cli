package bookstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestParseListQuery(t *testing.T) {
	ok := func(field, op, value string) []filterArg { return []filterArg{{field, op, value}} }
	tooMany := make([]filterArg, 11)
	for i := range tooMany {
		tooMany[i] = filterArg{"id", "", "1"}
	}
	for _, tt := range []struct {
		name    string
		sort    string
		filters []filterArg
		valid   bool
	}{
		{"no input", "", nil, true},
		{"sort ascending", "name", nil, true},
		{"sort explicit plus", "+name", nil, true},
		{"sort descending", "-updated_at", nil, true},
		{"sort unknown field", "-nope", nil, false},
		{"sort double sign", "--name", nil, false},
		{"sort sign only", "-", nil, false},
		{"id eq", "", ok("book_id", "", "7"), true},
		{"id letters", "", ok("book_id", "", "7a"), false},
		{"id negative", "", ok("book_id", "", "-7"), false},
		{"id empty", "", ok("book_id", "", ""), false},
		{"id overflow", "", ok("book_id", "", "99999999999999999999"), false},
		{"id like", "", ok("book_id", "like", "7"), false},
		{"number gt", "", ok("priority", "gt", "3"), true},
		{"bool eq", "", ok("draft", "eq", "true"), true},
		{"bool ne", "", ok("draft", "ne", "false"), true},
		{"bool gt", "", ok("draft", "gt", "true"), false},
		{"bool like", "", ok("draft", "like", "true"), false},
		{"bool numeric", "", ok("draft", "", "1"), false},
		{"time rfc3339", "", ok("created_at", "gte", "2026-01-02T03:04:05+02:00"), true},
		{"time date", "", ok("created_at", "lt", "2026-01-02"), true},
		{"time like", "", ok("created_at", "like", "2026"), false},
		{"time bad", "", ok("created_at", "", "01.02.2026"), false},
		{"text like", "", ok("name", "like", "100%_\\"), true},
		{"text ne", "", ok("name", "ne", "x"), true},
		{"unknown field", "", ok("html", "", "x"), false},
		{"unknown op", "", ok("name", "regex", "x"), false},
		{"control character", "", ok("name", "", "a\nb"), false},
		{"value too long", "", ok("name", "", strings.Repeat("a", 256)), false},
		{"value at limit", "", ok("name", "", strings.Repeat("ä", 255)), true},
		{"invalid utf-8", "", ok("name", "", "a\xffb"), false},
		{"ten filters", "", tooMany[:10], true},
		{"eleven filters", "", tooMany, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseListQuery(pageListSchema, tt.sort, tt.filters)
			if tt.valid != (err == nil) {
				t.Fatalf("err = %v, valid = %v", err, tt.valid)
			}
			if err == nil {
				return
			}
			if !isInvalidRequest(err) {
				t.Errorf("err = %v, want an invalid-request", err)
			}
			for _, f := range tt.filters {
				if len(f.Value) > 3 && strings.Contains(err.Error(), f.Value) {
					t.Errorf("message %q quotes a value", err)
				}
			}
		})
	}
}

func TestListQueryWire(t *testing.T) {
	q, err := parseListQuery(pageListSchema, "-name", []filterArg{
		{"name", "", "Alpha"},
		{"name", "like", "50%_a\\"},
		{"draft", "", "true"},
		{"template", "ne", "false"},
		{"updated_at", "gte", "2026-03-04T05:06:07+02:00"},
		{"created_at", "lt", "2026-03-04"},
		{"priority", "gt", "007"},
		{"created_by", "", "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{}
	q.apply(query)
	want := map[string]string{
		"filter[name]":           "Alpha",
		"filter[name:like]":      `%50\%\_a\\%`,
		"filter[draft]":          "1",
		"filter[template:ne]":    "0",
		"filter[updated_at:gte]": "2026-03-04 03:06:07",
		"filter[created_at:lt]":  "2026-03-04 00:00:00",
		"filter[priority:gt]":    "7",
		"filter[created_by]":     "3",
	}
	if len(query) != len(want) {
		t.Errorf("query = %v", query)
	}
	for k, v := range want {
		if query.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, query.Get(k), v)
		}
	}
	if q.sort != "-name" {
		t.Errorf("sort = %q", q.sort)
	}
	// A key that is already set, as by the target binding, is not overwritten.
	taken := url.Values{"filter[created_by]": {"9"}}
	q.apply(taken)
	if taken.Get("filter[created_by]") != "9" {
		t.Errorf("a bound filter was overwritten: %v", taken)
	}
}

func TestListQueryMatch(t *testing.T) {
	row := pageJSON{
		ID: 5, BookID: 7, Name: "Ärger Mit Dem Zaun", Slug: "zaun", Priority: 3, Draft: true,
		CreatedAt: "2026-03-04T05:06:07.987654Z", UpdatedAt: "2026-03-04T07:06:07+02:00",
		CreatedBy: &userJSON{ID: 3},
	}
	for _, tt := range []struct {
		f    filterArg
		want bool
	}{
		{filterArg{"id", "", "5"}, true},
		{filterArg{"id", "ne", "5"}, false},
		{filterArg{"id", "gt", "4"}, true},
		{filterArg{"priority", "lte", "2"}, false},
		{filterArg{"draft", "", "true"}, true},
		{filterArg{"template", "", "false"}, true},
		{filterArg{"template", "ne", "false"}, false},
		{filterArg{"name", "", "ärger mit dem zaun"}, true},
		{filterArg{"name", "like", "MIT dem"}, true},
		{filterArg{"name", "like", "%"}, false},
		{filterArg{"name", "gt", "a"}, true},
		{filterArg{"name", "lt", "a"}, false},
		{filterArg{"slug", "ne", "ZAUN"}, false},
		// The row time is cut to the second and shown in UTC.
		{filterArg{"created_at", "", "2026-03-04T05:06:07Z"}, true},
		{filterArg{"created_at", "", "2026-03-04T07:06:07+02:00"}, true},
		{filterArg{"created_at", "gt", "2026-03-04T05:06:07Z"}, false},
		{filterArg{"updated_at", "", "2026-03-04T05:06:07Z"}, true},
		{filterArg{"updated_at", "gte", "2026-03-05"}, false},
		{filterArg{"updated_at", "lt", "2026-03-05"}, true},
		{filterArg{"created_by", "", "3"}, true},
		{filterArg{"created_by", "ne", "4"}, true},
		// The page has no updater: a missing value fails every operator, also ne.
		{filterArg{"updated_by", "ne", "4"}, false},
		{filterArg{"owned_by", "", "1"}, false},
	} {
		q, err := parseListQuery(pageListSchema, "", []filterArg{tt.f})
		if err != nil {
			t.Fatalf("%+v: %v", tt.f, err)
		}
		if got := q.match(func(f string) (any, bool) { return pageValue(row, f) }); got != tt.want {
			t.Errorf("%+v = %v, want %v", tt.f, got, tt.want)
		}
	}

	// A row time that does not parse never passes.
	q, _ := parseListQuery(pageListSchema, "", []filterArg{{"created_at", "ne", "2026-01-01"}})
	if q.match(func(string) (any, bool) { return "yesterday", true }) {
		t.Error("an unparsable time passed")
	}
}

// listCase runs one list tool through its handler, as a caller does.
type listCase struct {
	id      string
	path    string
	args    string
	filters string
	data    func(i int64) map[string]any
}

func listCases() []listCase {
	return []listCase{
		{pagesList.ID, "/api/pages", `{"book_id":7}`, `[{"field":"name","op":"like","value":"a_b"},{"field":"draft","value":"false"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"}]`,
			func(i int64) map[string]any { return page(i, "x") }},
		{booksList.ID, "/api/books", `{}`, `[{"field":"name","op":"like","value":"a_b"},{"field":"created_by","value":"4"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"}]`,
			func(i int64) map[string]any { return map[string]any{"id": i, "name": "x"} }},
		{chaptersList.ID, "/api/chapters", `{"book_id":7}`, `[{"field":"name","op":"like","value":"a_b"},{"field":"priority","op":"gt","value":"2"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"}]`,
			func(i int64) map[string]any { return map[string]any{"id": i, "book_id": 7, "name": "x"} }},
		{shelvesList.ID, "/api/shelves", `{}`, `[{"field":"name","op":"like","value":"a_b"},{"field":"owned_by","value":"4"},{"field":"updated_at","op":"gte","value":"2026-01-02T03:04:05+01:00"}]`,
			func(i int64) map[string]any { return map[string]any{"id": i, "name": "x"} }},
	}
}

func listHandler(t *testing.T, id string) capability.Handler {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	_, handler, ok := reg.Lookup(id)
	if !ok {
		t.Fatalf("%s is not registered", id)
	}
	return handler
}

func unboundResolved(baseURL string) *config.Resolved {
	return boundResolved(baseURL)
}

// Every list tool sends sort and the filters in the documented form, and the filter keeps its row check.
func TestListToolsSendSortAndFilters(t *testing.T) {
	for _, tc := range listCases() {
		t.Run(tc.id, func(t *testing.T) {
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				if strings.Contains(r.URL.Path, "/api/books/") {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "name": "b", "slug": "b"})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
			}))
			defer server.Close()

			args := strings.TrimSuffix(tc.args, "}")
			if args != "{" {
				args += ","
			}
			args += `"sort":"-name","filters":` + tc.filters + "}"
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
			if got.Get("sort") != "-name" {
				t.Errorf("sort = %q", got.Get("sort"))
			}
			if got.Get("filter[name:like]") != `%a\_b%` || got.Get("filter[updated_at:gte]") != "2026-01-02 02:04:05" {
				t.Errorf("query = %v", got)
			}
			switch tc.id {
			case pagesList.ID:
				if got.Get("filter[draft]") != "0" || got.Get("filter[book_id]") != "7" {
					t.Errorf("query = %v", got)
				}
			case chaptersList.ID:
				if got.Get("filter[priority:gt]") != "2" || got.Get("filter[book_id]") != "7" {
					t.Errorf("query = %v", got)
				}
			default:
				if got.Get("filter[created_by]")+got.Get("filter[owned_by]") != "4" {
					t.Errorf("query = %v", got)
				}
			}
		})
	}
}

// Without sort the list keeps +id, and without filters the scan stays unfiltered.
func TestListWithoutSortKeepsIDOrder(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{page(1, "A")}, "total": 1})
	}))
	defer server.Close()
	if _, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 0, 0, listQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, _, q, _ := rec.snapshot(); !strings.Contains(q[0], "sort=%2Bid") || strings.Contains(q[0], "filter") {
		t.Errorf("query = %s", q[0])
	}
}

// A malformed sort or filter ends the call before a secret is read and before a request is sent.
func TestListQueryRefusedBeforeSecretsAndIO(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	// No token variables are set: a secret access would fail with another error.
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")

	const canary = "canary-filter-value"
	for _, tc := range listCases() {
		for _, args := range []string{
			`{"sort":"-nope"}`,
			`{"filters":[{"field":"nope","value":"` + canary + `"}]}`,
			`{"filters":[{"field":"id","op":"like","value":"1"}]}`,
			`{"filters":[{"field":"id","value":"` + canary + `"}]}`,
			`{"filters":[{"field":"created_at","value":"` + canary + `"}]}`,
			`{"filters":[{"field":"name","value":"a\u0000` + canary + `"}]}`,
			`{"filters":[{"field":"name","value":"` + strings.Repeat("a", 256) + `"}]}`,
			`{"filters":[{"field":"name","op":"x","value":"` + canary + `"}]}`,
			`{"filters":[` + strings.TrimSuffix(strings.Repeat(`{"field":"id","value":"1"},`, 11), ",") + `]}`,
		} {
			_, err := listHandler(t, tc.id)(context.Background(), unboundResolved(server.URL), resolver(nil), nil, json.RawMessage(args))
			if !isInvalidRequest(err) {
				t.Errorf("%s %.60s: err = %v, want an invalid-request", tc.id, args, err)
			} else if strings.Contains(err.Error(), canary) {
				t.Errorf("%s: message quotes a value: %v", tc.id, err)
			}
		}
	}
	// Two filter members beyond the schema are refused as well.
	_, err := listHandler(t, booksList.ID)(context.Background(), unboundResolved(server.URL), resolver(nil), nil,
		json.RawMessage(`{"filters":[{"field":"id","value":"1"}],"raw":"x"}`))
	if err == nil {
		t.Error("an unknown argument was accepted")
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func filterServer(rec *recorder, total int, row func(i int) map[string]any) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		data := []map[string]any{}
		// The server ignores every filter and never returns more than three records at once.
		for i := offset; i < offset+count && i < total && len(data) < 3; i++ {
			data = append(data, row(i+1))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": total})
	}))
}

// A server that ignores the filter delivers no foreign row, and limit and offset count the rows that remain.
func TestFilterIgnoredByServer(t *testing.T) {
	rec := &recorder{}
	server := filterServer(rec, 12, func(i int) map[string]any {
		p := page(int64(i), fmt.Sprintf("Page%d", i))
		p["draft"] = i%2 == 0
		p["created_by"] = i % 4
		return p
	})
	defer server.Close()
	client := newClient(t, server.URL, nil)
	q, err := parseListQuery(pageListSchema, "name", []filterArg{{"draft", "", "true"}, {"name", "like", "PAGE"}})
	if err != nil {
		t.Fatal(err)
	}

	all, err := client.ListPages(context.Background(), listTarget{}, 0, 0, q)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(all.Rows); fmt.Sprint(got) != "[2 4 6 8 10 12]" {
		t.Errorf("ids = %v, want the six drafts", got)
	}
	part, err := client.ListPages(context.Background(), listTarget{}, 2, 1, q)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(part.Rows); fmt.Sprint(got) != "[4 6]" {
		t.Errorf("ids = %v, want two rows after skipping one match", got)
	}

	owned, _ := parseListQuery(pageListSchema, "", []filterArg{{"created_by", "", "2"}})
	got, err := client.ListPages(context.Background(), listTarget{}, 0, 0, owned)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got.Rows)) != "[2 6 10]" {
		t.Errorf("ids = %v, want the rows of user 2 read from the raw row", ids(got.Rows))
	}
	_, _, queries, _ := rec.snapshot()
	if !strings.Contains(queries[0], "sort=%2Bname") {
		t.Errorf("query = %s", queries[0])
	}
}

// The scan limit still applies when a filter matches nothing.
func TestFilterScanLimit(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []any{page(int64(offset+1), "x")},
			"total": 1 << 30,
		})
	}))
	defer server.Close()
	q, _ := parseListQuery(pageListSchema, "-id", []filterArg{{"name", "", "nothing"}})

	_, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 0, 0, q)

	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want the scan limit", err)
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != maxScanRequests {
		t.Errorf("requests = %d, want %d", len(methods), maxScanRequests)
	}
}

// With a sort the scan still drops repeated records and stops when a request brings nothing new.
func TestSortedScanStopsWithoutProgress(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 9,
			"data":  []any{page(1, "A"), page(2, "B"), page(2, "B"), page(3, "C")},
		})
	}))
	defer server.Close()
	client := newClient(t, server.URL, nil)

	sorted, _ := parseListQuery(pageListSchema, "-name", nil)
	got, err := client.ListPages(context.Background(), listTarget{}, 0, 0, sorted)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got.Rows)) != "[1 2 3]" || requests != 2 {
		t.Errorf("ids = %v after %d requests, want [1 2 3] after 2", ids(got.Rows), requests)
	}

	requests = 0
	filtered, _ := parseListQuery(pageListSchema, "-name", []filterArg{{"name", "ne", "b"}})
	got, err = client.ListPages(context.Background(), listTarget{}, 0, 0, filtered)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got.Rows)) != "[1 3]" || requests != 2 {
		t.Errorf("ids = %v after %d requests, want [1 3] after 2", ids(got.Rows), requests)
	}
}

// A filter never widens the binding: the target check of every row stays, whatever the filter says.
func TestFilterKeepsTargetBinding(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		inBook := page(1, "Mine")
		foreign := page(2, "Mine")
		foreign["book_id"] = 9
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{inBook, foreign}, "total": 2})
	}))
	defer server.Close()
	client := boundClient(t, server.URL, "book/7")

	// The filter names the foreign book, which the binding refuses.
	q, _ := parseListQuery(pageListSchema, "", []filterArg{{"book_id", "", "9"}})
	got, err := client.ListPages(context.Background(), listTarget{}, 0, 0, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 0 {
		t.Errorf("rows = %v, want none", got.Rows)
	}
	_, _, queries, _ := rec.snapshot()
	if !strings.Contains(queries[0], "filter%5Bbook_id%5D=7") || strings.Contains(queries[0], "=9") {
		t.Errorf("query = %s, want the bound book on the wire", queries[0])
	}

	q, _ = parseListQuery(pageListSchema, "", []filterArg{{"name", "", "mine"}})
	got, err = client.ListPages(context.Background(), listTarget{}, 0, 0, q)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got.Rows)) != "[1]" {
		t.Errorf("ids = %v, want only the row inside the binding", ids(got.Rows))
	}
}
