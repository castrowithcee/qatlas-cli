package baserow

import (
	"net/http"
	"strings"
	"testing"
)

const searchFields = `[{"id":1,"name":"Name","type":"text","primary":true},` +
	`{"id":2,"name":"Auftraege","type":"link_row","link_row_table_id":12},` +
	`{"id":3,"name":"Lieferant","type":"link_row","link_row_table_id":99},` +
	`{"id":4,"name":"Betrag","type":"number"},{"id":5,"name":"Summe","type":"formula"},` +
	`{"id":6,"name":"Status","type":"single_select"},{"id":7,"name":"a__b","type":"text"}]`

func searchServer(views string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/database/fields/"):
			return jsonResponse(200, searchFields), nil
		case strings.HasPrefix(r.URL.Path, "/api/database/views/table/"):
			return jsonResponse(200, views), nil
		}
		return jsonResponse(200, `{"count":1,"next":null,"results":[{"id":7,"order":"1","Name":"Acme",`+
			`"Lieferant":[{"id":4,"value":"secret-supplier"}]}]}`), nil
	}
}

func TestRowsSearchBuildsQueryAndMasksLinks(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, searchServer(`[{"id":5},{"id":6}]`))
	args := `{"table_id":11,"search":"a&b=c","search_mode":"compat","filters":[` +
		`{"field":"Name","type":"contains","value":"x y&z"},{"field":"Betrag","type":"not_empty"}],` +
		`"filter_type":"OR","order_by":[{"field":"Betrag","direction":"desc"},{"field":"Name"}],"view_id":6,"size":200}`
	result, err := env.invoke(rowsSearch.ID, "all", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"Name":"Acme"`) {
		t.Fatalf("result = %s", result)
	}
	if len(calls) != 3 || calls[0].path != "/api/database/fields/table/11/" ||
		calls[1].path != "/api/database/views/table/11/" || calls[2].path != "/api/database/rows/table/11/" {
		t.Fatalf("calls = %+v", calls)
	}
	q := calls[2].query
	if q.Get("search") != "a&b=c" || q.Get("search_mode") != "compat" || q.Get("filter__Name__contains") != "x y&z" ||
		q.Has("filter__Betrag__not_empty") == false || q.Get("filter_type") != "OR" ||
		q.Get("order_by") != "-Betrag,Name" || q.Get("view_id") != "6" || q.Get("size") != "200" ||
		q.Get("user_field_names") != "true" || len(q) != 10 {
		t.Fatalf("query = %v", q)
	}
}

func TestRowsSearchRefusesBeforeIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, searchServer(`[]`))
	for _, args := range []string{
		`{"table_id":99}`, `{"table_id":11,"size":201}`, `{"table_id":11,"search_mode":"full-text"}`,
		`{"table_id":11,"search":"x","search_mode":"evil"}`, `{"table_id":11,"filters":[{"field":"Name","type":"regex","value":"x"}]}`,
		`{"table_id":11,"filters":[{"field":"Name","type":"equal"}]}`, `{"table_id":11,"filters":[{"field":"Name","type":"empty","value":"x"}]}`,
		`{"table_id":11,"filters":[{"field":"a__b","type":"equal","value":"x"}]}`,
		`{"table_id":11,"filters":[{"field":"Name","type":"equal","value":"` + strings.Repeat("x", 257) + `"}]}`,
		`{"table_id":11,"order_by":[{"field":"-Name"}]}`, `{"table_id":11,"order_by":[{"field":"Name","direction":"up"}]}`,
		`{"table_id":11,"filter__Name__equal":"x"}`, `{"table_id":11,"search":"` + strings.Repeat("x", 257) + `"}`,
		`{"table_id":11,"filters":[{"field":"Name","type":"equal","value":"a"},{"field":"Name","type":"equal","value":"b"}]}`,
	} {
		if _, err := env.invoke(rowsSearch.ID, "one", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
	}
}

func TestRowsSearchSchemaRefusalsReadOnlyFields(t *testing.T) {
	for _, args := range []string{
		`{"table_id":11,"filters":[{"field":"Unbekannt","type":"equal","value":"x"}]}`,
		`{"table_id":11,"filters":[{"field":"Lieferant","type":"contains","value":"x"}]}`,
		`{"table_id":11,"filters":[{"field":"Auftraege","type":"empty"}]}`,
		`{"table_id":11,"order_by":[{"field":"Summe"}]}`,
		`{"table_id":11,"search":"x"}`,
		`{"table_id":11,"view_id":9}`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, searchServer(`[{"id":5}]`))
		if _, err := env.invoke(rowsSearch.ID, "one", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", args, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.path, "/api/database/rows/") {
				t.Errorf("%s reached the rows endpoint", args)
			}
		}
		if len(calls) == 0 || len(calls) > 2 {
			t.Errorf("%s: calls = %+v", args, calls)
		}
	}
}

func TestRowsSearchWildcardAllowsSearchOnLinkTables(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, searchServer(`[]`))
	if _, err := env.invoke(rowsSearch.ID, "all", `{"table_id":99,"search":"x"}`); err != nil {
		t.Fatal(err)
	}
}

func TestRowsSearchViewListFailureIsNotAnAcceptance(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/database/views/") {
			return jsonResponse(404, `{"error":"secret"}`), nil
		}
		return searchServer(`[]`)(r)
	})
	_, err := env.invoke(rowsSearch.ID, "one", `{"table_id":11,"view_id":6}`)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestRowsSearchMasksLinks(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, searchServer(`[]`))
	result, err := env.invoke(rowsSearch.ID, "one", `{"table_id":11,"filters":[{"field":"Name","type":"equal","value":"Acme"}]}`)
	if err != nil || strings.Contains(result, "secret-supplier") || !strings.Contains(result, `"Lieferant":[{"id":4}]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}
