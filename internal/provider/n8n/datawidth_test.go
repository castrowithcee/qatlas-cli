package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDataTablesListReportsColumnsAndSendsFilterAndSort(t *testing.T) {
	var calls []call
	body := fmt.Sprintf(`{"data":[%s],"nextCursor":null}`, tableJSONOf(ownTable, "Own", ownProject))
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, body), nil
	})
	result, err := env.invoke(dataTablesList.ID, "open", fmt.Sprintf(
		`{"project_id":%q,"name":"Own","ids":[%q,"OTHER1"],"sort_by":"updatedAt:desc"}`, ownProject, ownTable))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		DataTables []struct {
			Columns     []map[string]any `json:"columns"`
			ColumnCount int              `json:"column_count"`
		} `json:"data_tables"`
	}
	wantColumn := map[string]any{"id": "c1", "name": "email", "type": "string", "index": float64(0),
		"data_table_id": ownTable, "created_at": "2026-01-01T00:00:00.000Z", "updated_at": "2026-01-01T00:00:00.000Z"}
	if err := json.Unmarshal([]byte(result), &page); err != nil || len(page.DataTables) != 1 ||
		page.DataTables[0].ColumnCount != 1 || fmt.Sprint(page.DataTables[0].Columns) != fmt.Sprint([]map[string]any{wantColumn}) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 1 || calls[0].query.Get("sortBy") != "updatedAt:desc" {
		t.Fatalf("calls = %+v", calls)
	}
	var filter map[string]any
	if err := json.Unmarshal([]byte(calls[0].query.Get("filter")), &filter); err != nil {
		t.Fatal(err)
	}
	ids, _ := filter["id"].([]any)
	if filter["name"] != "Own" || filter["projectId"] != ownProject || len(filter) != 3 || len(ids) != 2 ||
		ids[0] != ownTable || ids[1] != "OTHER1" {
		t.Fatalf("filter = %v", filter)
	}
}

func TestDataTablesListWithoutNewArgumentsSendsTheOldRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	})
	if _, err := env.invoke(dataTablesList.ID, "open", `{}`); err != nil {
		t.Fatal(err)
	}
	if got := calls[0].query; got.Has("filter") || got.Has("sortBy") || len(got) != 1 {
		t.Fatalf("query = %v", got)
	}
}

func TestDataTablesListIdsDoNotWidenTheAllowList(t *testing.T) {
	var calls []call
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":null}`,
		tableJSONOf(ownTable, "Own", ownProject), tableJSONOf(foreignTable, foreignCanary, foreignProject))
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, body), nil
	})
	result, err := env.invoke(dataTablesList.ID, "project", fmt.Sprintf(`{"ids":[%q,%q]}`, ownTable, foreignTable))
	if err != nil || strings.Contains(result, foreignCanary) || strings.Contains(result, foreignProject) {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestDataTablesListRefusesBadFiltersWithoutRequest(t *testing.T) {
	ids := func(n int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(`"T%d"`, i)
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	for _, tt := range []struct{ name, arguments string }{
		{"unknown sort", `{"sort_by":"size:sideways"}`},
		{"sort on project", `{"sort_by":"projectId:asc"}`},
		{"control name", `{"name":"a\u0007b"}`},
		{"blank name", `{"name":"   "}`},
		{"long name", `{"name":"` + strings.Repeat("a", 129) + `"}`},
		{"no ids", `{"ids":[]}`},
		{"many ids", `{"ids":` + ids(51) + `}`},
		{"duplicate ids", `{"ids":["A1","A1"]}`},
		{"bad id", `{"ids":["a/b"]}`},
		{"unknown filter", `{"filter":{"id":"x"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			if _, err := env.invoke(dataTablesList.ID, "open", tt.arguments); err == nil || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}

func TestDataTablesGetAndColumnsListReportTimestampsAndTable(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, columnServer(t, &mutations))
	got, err := env.invoke(dataTablesGet.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	if err != nil || !strings.Contains(got, `"columns":[{"created_at":"2026-01-01T00:00:00.000Z",`+
		`"data_table_id":"`+ownTable+`","id":"c1","index":0,"name":"email","type":"string",`+
		`"updated_at":"2026-01-01T00:00:00.000Z"}]`) {
		t.Fatalf("get = %s, %v", got, err)
	}
	listed, err := env.invoke(dataColumnsList.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	want := `{"columns":[{"created_at":"2026-01-01T00:00:00.000Z","data_table_id":"` + ownTable + `",` +
		`"id":"c1","index":0,"name":"email","type":"string","updated_at":"2026-01-01T00:00:00.000Z"}],"count":1}`
	if err != nil || listed != want {
		t.Fatalf("list = %s, %v, want %s", listed, err, want)
	}
}

func TestDataColumnWritesKeepTheirOutput(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, columnServer(t, &mutations))
	added, err := env.confirmed(dataColumnsAdd.ID, "pdelete",
		fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number"}`, ownTable))
	if err != nil || added != `{"id":"NEWCOLUMN0001","index":1,"name":"age","type":"number"}` {
		t.Fatalf("add = %s, %v", added, err)
	}
}

func rowQuery(t *testing.T, arguments string) (url.Values, error) {
	t.Helper()
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[],"nextCursor":null}`))
	_, err := env.invoke(dataRowsList.ID, "pdelete", arguments)
	if err != nil {
		return nil, err
	}
	last := calls[len(calls)-1]
	if len(calls) != 2 || !strings.HasSuffix(last.path, "/rows") {
		t.Fatalf("calls = %+v", calls)
	}
	return last.query, nil
}

func TestDataRowsListSendsSortSearchAndSystemColumnFilters(t *testing.T) {
	query, err := rowQuery(t, `{`+tableArg()+`,"sort_by":{"column":"createdAt","direction":"desc"},"search":"ada",`+
		`"filter":{"type":"or","conditions":[{"column":"id","operator":"gte","value":12},`+
		`{"column":"updatedAt","operator":"lt","value":"2026-02-01T00:00:00Z"},`+
		`{"column":"createdAt","operator":"neq","value":"2026-01-01T10:00:00+02:00"},`+
		`{"column":"email","operator":"like","value":"%x"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("sortBy") != "createdAt:desc" || query.Get("search") != "ada" {
		t.Fatalf("query = %v", query)
	}
	want := `{"filters":[{"columnName":"id","condition":"gte","value":12},` +
		`{"columnName":"updatedAt","condition":"lt","value":"2026-02-01T00:00:00Z"},` +
		`{"columnName":"createdAt","condition":"neq","value":"2026-01-01T10:00:00+02:00"},` +
		`{"columnName":"email","condition":"like","value":"%x"}],"type":"or"}`
	if got := query.Get("filter"); got != want {
		t.Fatalf("filter = %s, want %s", got, want)
	}
	query, err = rowQuery(t, `{`+tableArg()+`,"sort_by":{"column":"email","direction":"asc"}}`)
	if err != nil || query.Get("sortBy") != "email:asc" || query.Has("search") || query.Has("filter") {
		t.Fatalf("query = %v, %v", query, err)
	}
}

func TestDataRowsListRefusesBadSortSearchAndSystemFilters(t *testing.T) {
	system := func(column, operator, value string) string {
		return `{` + tableArg() + `,"filter":{"conditions":[{"column":"` + column + `","operator":"` + operator +
			`","value":` + value + `}]}}`
	}
	for _, tt := range []struct {
		name, arguments string
		reads           int
	}{
		{"unknown sort column", `{` + tableArg() + `,"sort_by":{"column":"nope","direction":"asc"}}`, 1},
		{"unknown direction", `{` + tableArg() + `,"sort_by":{"column":"email","direction":"up"}}`, 0},
		{"upper direction", `{` + tableArg() + `,"sort_by":{"column":"email","direction":"ASC"}}`, 0},
		{"control search", `{` + tableArg() + `,"search":"a\nb"}`, 0},
		{"long search", `{` + tableArg() + `,"search":"` + strings.Repeat("a", 257) + `"}`, 0},
		{"id as string", system("id", "eq", `"7"`), 0},
		{"id fractional", system("id", "eq", `1.5`), 0},
		{"id like", system("id", "like", `1`), 0},
		{"time as number", system("createdAt", "gt", `5`), 0},
		{"time not rfc3339", system("updatedAt", "gt", `"2026-01-01"`), 0},
		{"time like", system("createdAt", "ilike", `"2026-01-01T00:00:00Z"`), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[]}`))
			_, err := env.invoke(dataRowsList.ID, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(calls) != tt.reads {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}

func TestChangingRowToolsRefuseSystemColumnFilters(t *testing.T) {
	for _, column := range rowSystemColumns {
		value := `1`
		if column != "id" {
			value = `"2026-01-01T00:00:00Z"`
		}
		filter := `"filter":{"conditions":[{"column":"` + column + `","operator":"eq","value":` + value + `}]}`
		for _, tt := range []struct{ operation, arguments string }{
			{dataRowsUpdate.ID, `{` + tableArg() + `,` + filter + `,"data":{"email":"b"}}`},
			{dataRowsUpsert.ID, `{` + tableArg() + `,` + filter + `,"data":{"email":"b"}}`},
			{dataRowsDelete.ID, `{` + tableArg() + `,` + filter + `}`},
		} {
			t.Run(column+" "+tt.operation, func(t *testing.T) {
				var calls []call
				var mutations []string
				env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[]}`))
				_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
				if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 || calls[0].method != http.MethodGet {
					t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
				}
			})
		}
	}
}
