package baserow

import (
	"net/http"
	"strings"
	"testing"
)

const rowBody = `{"id":7,"order":"1.00000000000000000000","Name":"Acme","Betrag":12.50,"Auftraege":[{"id":3,"value":"A-1"}],` +
	`"Lieferant":[{"id":4,"value":"secret-supplier"}],"Status":{"id":5,"value":"offen","color":"red"},` +
	`"Tags":[{"id":1,"value":"x","color":"blue"}],"Unbekannt":[{"id":8,"value":"mystery"}]}`

func fieldsAndRows(listBody string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/database/fields/") {
			return jsonResponse(200, `[{"id":1,"name":"Name","type":"text","primary":true},`+
				`{"id":2,"name":"Auftraege","type":"link_row","link_row_table_id":12},`+
				`{"id":3,"name":"Lieferant","type":"link_row","link_row_table_id":99},`+
				`{"id":4,"name":"Tags","type":"multiple_select"}]`), nil
		}
		return jsonResponse(200, listBody), nil
	}
}

func TestRowsGetMasksLinksOutsideTheAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(rowBody))
	result, err := env.invoke(rowsGet.ID, "list", `{"table_id":11,"row_id":7}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "secret-supplier") || strings.Contains(result, "mystery") {
		t.Fatalf("a foreign display value leaked: %s", result)
	}
	for _, want := range []string{`"A-1"`, `"Lieferant":[{"id":4}]`, `"Unbekannt":[{"id":8}]`, `"Betrag":12.50`,
		`"value":"x"`, `"id":7`} {
		if !strings.Contains(result, want) {
			t.Errorf("result lacks %s: %s", want, result)
		}
	}
	if len(calls) != 2 || calls[0].path != "/api/database/rows/table/11/7/" || calls[0].query.Get("user_field_names") != "true" ||
		calls[1].path != "/api/database/fields/table/11/" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRowsGetWildcardKeepsLinksAndReadsNoFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(rowBody))
	result, err := env.invoke(rowsGet.ID, "all", `{"table_id":99,"row_id":7}`)
	if err != nil || !strings.Contains(result, "secret-supplier") || len(calls) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %d", result, err, len(calls))
	}
}

func TestRowsListWithoutLinksReadsNoFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(`{"count":1,"next":null,"previous":null,"results":[{"id":1,"order":"1","Name":"a"}]}`))
	if _, err := env.invoke(rowsList.ID, "one", `{"table_id":11}`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want only the rows request", calls)
	}
}

func TestRowsListPagingAndFieldKeys(t *testing.T) {
	var calls []call
	body := `{"count":250,"next":"https://elsewhere.invalid/next","previous":null,"results":[` +
		`{"id":1,"order":"1","field_1":"a","field_3":[{"id":4,"value":"secret-supplier"}]}]}`
	env := newEnvironment(t, &calls, fieldsAndRows(body))
	result, err := env.invoke(rowsList.ID, "one", `{"table_id":11,"page":2,"size":200,"user_field_names":false}`)
	if err != nil {
		t.Fatal(err)
	}
	q := calls[0].query
	if calls[0].path != "/api/database/rows/table/11/" || q.Get("page") != "2" || q.Get("size") != "200" ||
		q.Get("user_field_names") != "false" {
		t.Fatalf("calls = %+v", calls)
	}
	if strings.Contains(result, "secret-supplier") || strings.Contains(result, "elsewhere") ||
		!strings.Contains(result, `"field_3":[{"id":4}]`) || !strings.Contains(result, `"has_more":true`) ||
		!strings.Contains(result, `"count":250`) {
		t.Fatalf("result = %s", result)
	}
}

func TestRowsListDefaultsAndLimits(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(`{"count":0,"next":null,"results":[]}`))
	if _, err := env.invoke(rowsList.ID, "one", `{"table_id":11}`); err != nil {
		t.Fatal(err)
	}
	if calls[0].query.Get("page") != "1" || calls[0].query.Get("size") != "50" || calls[0].query.Get("user_field_names") != "true" {
		t.Fatalf("query = %v", calls[0].query)
	}
	calls = nil
	for _, args := range []string{`{"table_id":11,"size":201}`, `{"table_id":11,"size":0}`, `{"table_id":11,"page":0}`,
		`{"table_id":11,"size":"5"}`, `{"table_id":11,"filter":"x"}`} {
		if _, err := env.invoke(rowsList.ID, "one", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
}

func TestRowsGetRejectsBadRowID(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(rowBody))
	for _, args := range []string{`{"table_id":11,"row_id":0}`, `{"table_id":11,"row_id":-3}`, `{"table_id":11}`} {
		if _, err := env.invoke(rowsGet.ID, "one", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRowValuesAreBounded(t *testing.T) {
	var calls []call
	long := strings.Repeat("ü", maxCellString)
	items := strings.Repeat(`"i",`, maxCellItems+20)
	deep := strings.Repeat(`[`, maxCellDepth+3) + strings.Repeat(`]`, maxCellDepth+3)
	body := `{"id":1,"order":"1","Long":"` + long + `","Items":[` + items + `"i"],"Deep":` + deep + `}`
	env := newEnvironment(t, &calls, fieldsAndRows(body))
	result, err := env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, strings.Repeat("ü", maxCellString/2+1)) || strings.Count(result, `"i"`) > maxCellItems ||
		strings.Contains(result, strings.Repeat("[", maxCellDepth+1)) {
		t.Fatalf("a value escaped its bound: %.300s", result)
	}
}

func TestRowWithoutIdentifierIsInvalid(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(`{"Name":"a"}`))
	if _, err := env.invoke(rowsGet.ID, "one", `{"table_id":11,"row_id":1}`); classOf(err) != "invalid-provider-response" {
		t.Fatalf("err = %v", err)
	}
}

func TestRowsListSendsColumnSelection(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(`{"count":1,"next":null,"results":[{"id":1,"order":"1","Name":"a"}]}`))
	if _, err := env.invoke(rowsList.ID, "one", `{"table_id":11}`); err != nil {
		t.Fatal(err)
	}
	if q := calls[0].query; q.Has("include") || q.Has("exclude") || len(q) != 3 {
		t.Fatalf("plain query = %v", q)
	}
	calls = nil
	if _, err := env.invoke(rowsList.ID, "one", `{"table_id":11,"include":["Name","Betrag"],"exclude":["Tags"]}`); err != nil {
		t.Fatal(err)
	}
	if q := calls[0].query; q.Get("include") != "Name,Betrag" || q.Get("exclude") != "Tags" || len(q) != 5 || len(calls) != 1 {
		t.Fatalf("query = %v, calls = %d", calls[0].query, len(calls))
	}
	calls = nil
	if _, err := env.invoke(rowsList.ID, "one", `{"table_id":11,"user_field_names":false,"include":["field_1"]}`); err != nil {
		t.Fatal(err)
	}
	if q := calls[0].query; q.Get("include") != "field_1" || q.Get("user_field_names") != "false" {
		t.Fatalf("query = %v", q)
	}
}

func TestRowsListMasksLinksOfSelectedColumns(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fieldsAndRows(`{"count":1,"next":null,"results":[{"id":7,"order":"1",`+
		`"Lieferant":[{"id":4,"value":"secret-supplier"}]}]}`))
	result, err := env.invoke(rowsList.ID, "list", `{"table_id":11,"include":["Lieferant"]}`)
	if err != nil || strings.Contains(result, "secret-supplier") || !strings.Contains(result, `"Lieferant":[{"id":4}]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestColumnKeysAreRefusedBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, searchServer(`[]`))
	many := `"a"` + strings.Repeat(`,"a"`, maxColumnKeys)
	for _, args := range []string{
		`{"table_id":11,"include":[]}`, `{"table_id":11,"exclude":[]}`, `{"table_id":11,"include":[""]}`,
		`{"table_id":11,"include":["a,b"]}`, `{"table_id":11,"exclude":["a__b"]}`, `{"table_id":11,"include":["-a"]}`,
		`{"table_id":11,"exclude":["+a"]}`, `{"table_id":11,"include":[` + many + `]}`, `{"table_id":11,"include":"Name"}`,
		`{"table_id":11,"exclude":["Name",5]}`,
	} {
		for _, tool := range []string{rowsList.ID, rowsSearch.ID} {
			if _, err := env.invoke(tool, "one", args); err == nil {
				t.Errorf("%s accepted %s", tool, args)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
	}
}

func TestRowsGetMetadataOnRequest(t *testing.T) {
	body := `{"id":7,"order":"1","Name":"Acme","metadata":{"row_comment_count":3,` +
		`"row_comments_notification_mode":"all\u0007x","other":1}}`
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("include") == "metadata" {
			return jsonResponse(200, body), nil
		}
		return jsonResponse(200, `{"id":7,"order":"1","Name":"Acme"}`), nil
	})
	result, err := env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":7,"include_metadata":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if q := calls[0].query; q.Get("include") != "metadata" || q.Has("view") || len(q) != 2 || len(calls) != 1 {
		t.Fatalf("query = %v, calls = %d", calls[0].query, len(calls))
	}
	for _, want := range []string{`"metadata":{"row_comment_count":3,"row_comments_notification_mode":"all x"}`, `"fields":{"Name":"Acme"}`} {
		if !strings.Contains(result, want) {
			t.Errorf("result lacks %s: %s", want, result)
		}
	}
	calls = nil
	result, err = env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":7}`)
	if err != nil || len(calls) != 1 || calls[0].query.Has("include") || len(calls[0].query) != 1 {
		t.Fatalf("plain call = %+v, err = %v", calls, err)
	}
	if strings.Contains(result, "row_comment_count") {
		t.Fatalf("metadata appeared without being requested: %s", result)
	}
}

func TestRowsGetViewIsCheckedBeforeTheRow(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/database/views/table/") {
			return jsonResponse(200, `[{"id":5},{"id":6}]`), nil
		}
		return jsonResponse(200, rowBody), nil
	})
	if _, err := env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":7,"view_id":6}`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].path != "/api/database/views/table/11/" ||
		calls[1].path != "/api/database/rows/table/11/7/" || calls[1].query.Get("view") != "6" {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	if _, err := env.invoke(rowsGet.ID, "all", `{"table_id":11,"row_id":7,"view_id":9}`); err == nil {
		t.Fatal("a view of another table was accepted")
	}
	if len(calls) != 1 || calls[0].path != "/api/database/views/table/11/" {
		t.Fatalf("calls = %+v, want only the view list", calls)
	}
	calls = nil
	for _, args := range []string{`{"table_id":11,"row_id":7,"view_id":0}`, `{"table_id":11,"row_id":7,"view_id":-1}`,
		`{"table_id":11,"row_id":7,"exclude":["Name"]}`} {
		if _, err := env.invoke(rowsGet.ID, "all", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}
