package baserow

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// databaseHandler answers the token's table list (tables 11 and 12 in database 1, 99 in database 2), fields,
// rows, and deletes.
func databaseHandler(r *http.Request) (*http.Response, error) {
	switch {
	case r.URL.Path == allTablesPath:
		return jsonResponse(200, allTablesBody), nil
	case strings.HasPrefix(r.URL.Path, "/api/database/fields/table/"):
		return jsonResponse(200, fieldsBody), nil
	case r.Method == http.MethodDelete:
		return jsonResponse(204, ``), nil
	}
	return jsonResponse(200, `{"id":1,"order":"1.00","Name":"x"}`), nil
}

func paths(calls []call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

func TestDatabaseScopeTablesList(t *testing.T) {
	for connection, want := range map[string][]string{
		"db": {"Kunden", "Aufträge"}, "dbtable": {"Kunden", "Aufträge", "Geheim"},
	} {
		var calls []call
		env := newEnvironment(t, &calls, databaseHandler)
		result, err := env.invoke(tablesList.ID, connection, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Kunden", "Aufträge", "Geheim"} {
			if strings.Contains(result, name) != slices.Contains(want, name) {
				t.Errorf("%s: %q in %s", connection, name, result)
			}
		}
	}
}

func TestDatabaseScopeAdmitsTablesOfTheDatabaseWithOneExtraRead(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, databaseHandler)
	if _, err := env.invoke(rowsGet.ID, "db", `{"table_id":12,"row_id":1}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + allTablesPath, "GET /api/database/rows/table/12/1/"}; !slices.Equal(paths(calls), want) {
		t.Fatalf("calls = %v, want %v", paths(calls), want)
	}
	// A listed table needs no proof.
	calls = nil
	if _, err := env.invoke(rowsGet.ID, "dbtable", `{"table_id":99,"row_id":1}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/database/rows/table/99/1/"}; !slices.Equal(paths(calls), want) {
		t.Fatalf("listed table calls = %v, want %v", paths(calls), want)
	}
	// Reading the fields reuses the proof's table list.
	calls = nil
	if _, err := env.invoke(fieldsList.ID, "db", `{"table_id":11}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + allTablesPath, "GET /api/database/fields/table/11/"}; !slices.Equal(paths(calls), want) {
		t.Fatalf("fields calls = %v, want %v", paths(calls), want)
	}
}

func TestDatabaseScopeRefusesTableOfAnotherDatabaseWithoutAction(t *testing.T) {
	for _, tool := range []struct{ id, connection, args string }{
		{rowsGet.ID, "db", `{"table_id":99,"row_id":1}`},
		{rowsList.ID, "db", `{"table_id":99}`},
		{fieldsList.ID, "db", `{"table_id":99}`},
		{rowsDelete.ID, "dbwrite", `{"table_id":99,"row_id":1}`},
		{rowsBatchDelete.ID, "dbwrite", `{"table_id":99,"row_ids":[1]}`},
		{rowsGet.ID, "db", `{"table_id":77,"row_id":1}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, databaseHandler)
		_, err := env.invokeConfirmed(tool.id, tool.connection, tool.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") || strings.Contains(err.Error(), "77") {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		if want := []string{"GET " + allTablesPath}; !slices.Equal(paths(calls), want) {
			t.Errorf("%s: calls = %v, want only the table list", tool.id, paths(calls))
		}
	}
}

func TestDatabaseScopeChangesTableOfTheDatabase(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, databaseHandler)
	if _, err := env.invokeConfirmed(rowsDelete.ID, "dbwrite", `{"table_id":11,"row_id":3}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + allTablesPath, "DELETE /api/database/rows/table/11/3/"}; !slices.Equal(paths(calls), want) {
		t.Fatalf("calls = %v, want %v", paths(calls), want)
	}
}

func TestDatabaseScopeLinkMasking(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, databaseHandler)
	result, err := env.invoke(fieldsList.ID, "db", `{"table_id":11}`)
	if err != nil || !strings.Contains(result, `"link_table_id":12`) || strings.Contains(result, "99") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// A listed table of another database opens no link into its own database.
	result, err = env.invoke(fieldsList.ID, "dbtable", `{"table_id":11}`)
	if err != nil || !strings.Contains(result, `"link_table_id":12`) || !strings.Contains(result, `"link_table_id":99`) {
		t.Fatalf("dbtable result = %s, err = %v", result, err)
	}
}

func TestListedTableDoesNotOpenItsDatabase(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, databaseHandler)
	if _, err := env.invoke(rowsGet.ID, "one", `{"table_id":12,"row_id":1}`); !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %v", err, paths(calls))
	}
}

func browseResolved() *config.Resolved {
	return &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleDatabaseToken: tokenEnv}}}
}

func browse(t *testing.T, calls *[]call, req config.TargetListRequest) (config.TargetPage, error) {
	t.Helper()
	serve(t, calls, databaseHandler)
	red := &redact.Redactor{}
	return listTargets(context.Background(), browseResolved(), resolver(red, nil), red, req)
}

func entryTargets(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target)
	}
	return out
}

func TestBrowseLevels(t *testing.T) {
	var calls []call
	page, err := browse(t, &calls, config.TargetListRequest{Kind: "database"})
	if err != nil || !slices.Equal(entryTargets(page), []string{"database/1", "database/2"}) ||
		!page.Entries[0].HasChildren || page.Entries[0].Name != "1" || page.Next != "" {
		t.Fatalf("databases = %+v, %v", page, err)
	}
	if want := []string{"GET " + allTablesPath}; !slices.Equal(paths(calls), want) {
		t.Fatalf("calls = %v", paths(calls))
	}
	calls = nil
	page, err = browse(t, &calls, config.TargetListRequest{Kind: "table", Parent: "database/1"})
	if err != nil || !slices.Equal(entryTargets(page), []string{"table/11", "table/12"}) || page.Entries[0].Name != "Kunden" {
		t.Fatalf("tables = %+v, %v", page, err)
	}
	for _, req := range []config.TargetListRequest{
		{Kind: "table"}, {Kind: "table", Parent: "table/1"}, {Kind: "table", Parent: "database/x"},
		{Kind: "workspace"}, {Kind: "database", Cursor: "2"},
	} {
		calls = nil
		if _, err := browse(t, &calls, req); !isInvalidRequest(err) || len(calls) != 0 {
			t.Errorf("%+v: err = %v, calls = %v", req, err, paths(calls))
		}
	}
}

func TestResolveTargetURL(t *testing.T) {
	cloud := "https://api.baserow.io"
	self := "https://baserow.example.test"
	for _, c := range []struct {
		origin, url string
		want        []string
	}{
		{cloud, "https://baserow.io/database/5/table/17", []string{"database/5", "table/17"}},
		{cloud, "https://baserow.io/database/5/table/17/301", []string{"database/5", "table/17"}},
		{cloud, "https://baserow.io/database/5/table/17/301/row/9", []string{"database/5", "table/17"}},
		{cloud, "https://baserow.io/database/5", []string{"database/5"}},
		{"", "https://baserow.io/database/5/table/17", []string{"database/5", "table/17"}},
		{cloud, "https://api.baserow.io/database/5/table/17", []string{"database/5", "table/17"}},
		{self, "https://baserow.example.test/database/5/table/17/1", []string{"database/5", "table/17"}},
		{self, "https://baserow.io/database/5/table/17", nil},
		{cloud, "https://evil.example/database/5/table/17", nil},
		{cloud, "http://baserow.io/database/5/table/17", nil},
		{cloud, "https://baserow.io/database/x/table/17", nil},
		{cloud, "https://baserow.io/database/05/table/17", nil},
		{cloud, "https://baserow.io/database/5/table/x", []string{"database/5"}},
		{cloud, "https://baserow.io/dashboard", nil},
		{cloud, "https://baserow.io/", nil},
		{cloud, "not a url", nil},
	} {
		entries, err := resolveTargetURL(context.Background(), nil, nil, nil, c.origin, c.url)
		var got []string
		for _, e := range entries {
			got = append(got, e.Target)
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s %s = %v, %v, want %v", c.origin, c.url, got, err, c.want)
		}
	}
}

func TestKindsFormValidParents(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	if err := metadata.Target.ValidateKindParents(); err != nil {
		t.Fatal(err)
	}
	for target, kind := range map[string]string{"database/3": "database", "table/3": "table", "*": "table"} {
		if !metadata.Target.ValidTarget(kind, target) {
			t.Errorf("%s is not a valid %s target", target, kind)
		}
	}
}

func TestBrowseNameIsPlainText(t *testing.T) {
	for name, want := range map[string]string{"Orders": "Orders", "a\x00b\nc": "a b c", " \t ": "7", "": "7"} {
		if got := browseName(name, 7); got != want {
			t.Errorf("browseName(%q) = %q, want %q", name, got, want)
		}
	}
}
