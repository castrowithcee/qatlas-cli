package baserow

import (
	"net/http"
	"strings"
	"testing"
)

const lookupFields = `[{"id":1,"name":"Name","type":"text","primary":true},` +
	`{"id":2,"name":"Auftraege","type":"link_row","link_row_table_id":12},` +
	`{"id":3,"name":"Lieferant","type":"link_row","link_row_table_id":99},` +
	`{"id":4,"name":"LookOut","type":"lookup","through_field_id":3},` +
	`{"id":5,"name":"LookIn","type":"lookup","through_field_id":2},` +
	`{"id":6,"name":"LookLost","type":"lookup","through_field_id":77},` +
	`{"id":7,"name":"LookBare","type":"lookup"},` +
	`{"id":8,"name":"Roll","type":"rollup","through_field_id":3},` +
	`{"id":9,"name":"Anzahl","type":"count","through_field_id":3},` +
	`{"id":10,"name":"Formel","type":"formula"}]`

const lookupRow = `{"id":7,"order":"1","Name":"Acme",` +
	`"LookOut":[{"id":1,"value":"canary-out"}],"LookIn":[{"id":2,"value":"kept-in"}],` +
	`"LookLost":[{"id":3,"value":"canary-lost"}],"LookBare":[{"id":4,"value":"canary-bare"}],` +
	`"Roll":"41","Anzahl":3,"Formel":"f"}`

func lookupServer(row string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/database/fields/"):
			return jsonResponse(200, lookupFields), nil
		case strings.HasPrefix(r.URL.Path, "/api/database/views/table/"):
			return jsonResponse(200, `[]`), nil
		case r.Method != http.MethodGet:
			return jsonResponse(200, row), nil
		case strings.HasSuffix(r.URL.Path, "/7/"):
			return jsonResponse(200, row), nil
		}
		return jsonResponse(200, `{"count":1,"next":null,"results":[`+row+`]}`), nil
	}
}

func checkLookupOutput(t *testing.T, name, result string) {
	t.Helper()
	for _, canary := range []string{"canary-out", "canary-lost", "canary-bare", `"LookOut":`, `"LookLost":`, `"LookBare":`} {
		if strings.Contains(result, canary) {
			t.Errorf("%s: %s leaked: %s", name, canary, result)
		}
	}
	for _, want := range []string{`"omitted_fields":["LookBare","LookLost","LookOut"]`, "kept-in", `"Roll":"41"`, `"Anzahl":3`, `"Formel":"f"`} {
		if !strings.Contains(result, want) {
			t.Errorf("%s: lacks %s: %s", name, want, result)
		}
	}
}

func TestLookupOutsideTargetsIsLeftOutOfEveryRowOutput(t *testing.T) {
	for _, tc := range []struct{ name, op, connection, args string }{
		{"list", rowsList.ID, "list", `{"table_id":11}`},
		{"get", rowsGet.ID, "list", `{"table_id":11,"row_id":7}`},
		{"search", rowsSearch.ID, "list", `{"table_id":11}`},
		{"create", rowsCreate.ID, "write", `{"table_id":11,"fields":{"Name":"x"}}`},
		{"update", rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{"Name":"x"}}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, lookupServer(lookupRow))
		result, err := env.invokeConfirmed(tc.op, tc.connection, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		checkLookupOutput(t, tc.name, result)
	}
}

func TestLookupOnlyRowStillReadsTheFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, lookupServer(`{"id":7,"order":"1","LookBare":[{"id":4,"value":"canary-bare"}]}`))
	result, err := env.invoke(rowsGet.ID, "list", `{"table_id":11,"row_id":7}`)
	if err != nil || strings.Contains(result, "canary-bare") || len(calls) != 2 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

func TestLookupUnderWildcardIsUnchangedWithoutExtraRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, lookupServer(lookupRow))
	result, err := env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":7}`)
	if err != nil || !strings.Contains(result, "canary-out") || len(calls) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

func TestRowsSearchRefusesFilterSortAndSearchOnLookupOutside(t *testing.T) {
	for _, args := range []string{
		`{"table_id":11,"filters":[{"field":"LookOut","type":"has_value_equal","value":"a"}]}`,
		`{"table_id":11,"filters":[{"field_id":4,"type":"empty"}]}`,
		`{"table_id":11,"filters":[{"field":"LookLost","type":"empty"}]}`,
		`{"table_id":11,"filters":[{"field":"LookBare","type":"empty"}]}`,
		`{"table_id":11,"order_by":[{"field":"LookOut"}]}`,
		`{"table_id":11,"search":"x"}`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, lookupServer(lookupRow))
		_, err := env.invoke(rowsSearch.ID, "list", args)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", args, err)
			continue
		}
		for _, c := range calls {
			if strings.HasPrefix(c.path, "/api/database/rows/") {
				t.Errorf("%s: a row request followed the refusal: %+v", args, calls)
			}
		}
		if strings.Contains(err.Error(), "LookOut") || strings.Contains(err.Error(), "99") {
			t.Errorf("%s: message names a field or table: %v", args, err)
		}
	}
}

func TestRowsSearchKeepsLookupInsideAndComputedFieldsUsable(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, lookupServer(lookupRow))
	args := `{"table_id":11,"filters":[{"field":"LookIn","type":"has_value_equal","value":"a"},` +
		`{"field":"Roll","type":"higher_than","value":"1"},{"field":"Anzahl","type":"equal","value":"1"},` +
		`{"field":"Formel","type":"contains","value":"a"}],"order_by":[{"field":"LookIn"},{"field":"Roll"}]}`
	if _, err := env.invoke(rowsSearch.ID, "list", args); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOmittedFieldsAreAbsentWithoutOmissionAndUseFieldIDKeys(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, lookupServer(`{"id":7,"order":"1","Name":"a","LookIn":[{"id":2,"value":"kept-in"}]}`))
	result, err := env.invoke(rowsGet.ID, "list", `{"table_id":11,"row_id":7}`)
	if err != nil || strings.Contains(result, "omitted_fields") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	calls = nil
	env = newEnvironment(t, &calls, lookupServer(`{"id":7,"order":"1","field_4":[{"id":1,"value":"canary-out"}]}`))
	result, err = env.invoke(rowsGet.ID, "list", `{"table_id":11,"row_id":7,"user_field_names":false}`)
	if err != nil || strings.Contains(result, "canary-out") || !strings.Contains(result, `"omitted_fields":["field_4"]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}
