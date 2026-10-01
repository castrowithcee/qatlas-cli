package baserow

import (
	"net/http"
	"strings"
	"testing"
)

const allTablesBody = `[{"id":11,"name":"Kunden","order":0,"database_id":1},{"id":12,"name":"Aufträge","order":1,"database_id":1},` +
	`{"id":99,"name":"Geheim","order":2,"database_id":2}]`

func TestTablesListShowsOnlyAllowedTables(t *testing.T) {
	for connection, want := range map[string][]string{
		"one": {"Kunden"}, "list": {"Kunden", "Aufträge"}, "all": {"Kunden", "Aufträge", "Geheim"},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, allTablesBody), nil
		})
		result, err := env.invoke(tablesList.ID, connection, `{}`)
		if err != nil {
			t.Fatalf("%s: %v", connection, err)
		}
		for _, name := range []string{"Kunden", "Aufträge", "Geheim"} {
			contained := strings.Contains(result, name)
			wanted := false
			for _, w := range want {
				wanted = wanted || w == name
			}
			if contained != wanted {
				t.Errorf("%s: %q in result = %t, want %t: %s", connection, name, contained, wanted, result)
			}
		}
		if len(calls) != 1 || calls[0].path != allTablesPath || calls[0].method != http.MethodGet {
			t.Errorf("%s: calls = %+v", connection, calls)
		}
	}
}

func TestTablesListBoundsStringsAndCount(t *testing.T) {
	var calls []call
	var body strings.Builder
	body.WriteString(`[{"id":11,"name":"` + strings.Repeat("ä", 600) + `","database_id":1}`)
	for i := 0; i < maxTables+5; i++ {
		body.WriteString(`,{"id":11,"name":"x","database_id":1}`)
	}
	body.WriteString(`]`)
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, body.String()), nil })
	result, err := env.invoke(tablesList.ID, "all", `{}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || strings.Contains(result, strings.Repeat("ä", 300)) {
		t.Fatalf("result = %.200s, err = %v", result, err)
	}
}
