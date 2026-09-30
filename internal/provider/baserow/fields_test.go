package baserow

import (
	"net/http"
	"strings"
	"testing"
)

const fieldsBody = `[{"id":1,"table_id":11,"name":"Name","order":0,"type":"text","primary":true,"read_only":false},` +
	`{"id":2,"table_id":11,"name":"Status","order":1,"type":"single_select","primary":false,"read_only":false,` +
	`"select_options":[{"id":5,"value":"offen","color":"red"}]},` +
	`{"id":3,"table_id":11,"name":"Auftraege","order":2,"type":"link_row","link_row_table_id":12},` +
	`{"id":4,"table_id":11,"name":"Lieferant","order":3,"type":"link_row","link_row_table_id":99},` +
	`{"id":5,"table_id":11,"name":"Gelöscht","order":4,"type":"link_row","link_row_table_id":null}]`

func TestFieldsListHidesForeignLinkTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, fieldsBody), nil })
	result, err := env.invoke(fieldsList.ID, "list", `{"table_id":11}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"link_table_id":12`) || strings.Contains(result, "99") ||
		!strings.Contains(result, `"primary":true`) || !strings.Contains(result, `"value":"offen"`) {
		t.Fatalf("result = %s", result)
	}
	if len(calls) != 1 || calls[0].path != "/api/database/fields/table/11/" || calls[0].method != http.MethodGet {
		t.Fatalf("calls = %+v", calls)
	}
	result, err = env.invoke(fieldsList.ID, "all", `{"table_id":11}`)
	if err != nil || !strings.Contains(result, `"link_table_id":99`) {
		t.Fatalf("wildcard result = %s, err = %v", result, err)
	}
}

func TestForeignTableIsRefusedBeforeSecretAndIO(t *testing.T) {
	for _, tool := range []struct{ id, args string }{
		{fieldsList.ID, `{"table_id":99}`}, {rowsList.ID, `{"table_id":99}`}, {rowsGet.ID, `{"table_id":99,"row_id":1}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
		_, err := env.invoke(tool.id, "list", tool.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") {
			t.Errorf("%s: err = %v, want an invalid request without the foreign ID", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: calls = %d, secret reads = %d, want none", tool.id, len(calls), *env.reads)
		}
	}
}

func TestInvalidIdentifiersAreRefused(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	for _, args := range []string{`{"table_id":0}`, `{"table_id":-1}`, `{"table_id":"11"}`, `{"table_id":1.5}`, `{}`, `{"table_id":11,"x":1}`} {
		if _, err := env.invoke(fieldsList.ID, "all", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %d, want none", len(calls))
	}
}
