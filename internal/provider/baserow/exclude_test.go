package baserow

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Tables of the exclusion server: 11 and 12 in database 1, 99 in database 2, 13 in database 3.
const excludeTablesBody = `[{"id":11,"name":"Kunden","order":0,"database_id":1},{"id":12,"name":"Auftraege","order":1,"database_id":1},` +
	`{"id":13,"name":"Archiv","order":2,"database_id":3},{"id":99,"name":"Geheim","order":3,"database_id":2}]`

const excludeFields = `[{"id":1,"name":"Name","type":"text","primary":true},` +
	`{"id":2,"name":"Auftraege","type":"link_row","link_row_table_id":12,"link_row_related_field_id":501,` +
	`"link_row_limit_selection_view_id":502,"link_row_table_primary_field":"auftrag-primary"},` +
	`{"id":3,"name":"Lieferant","type":"link_row","link_row_table_id":99,"link_row_related_field_id":777,` +
	`"link_row_limit_selection_view_id":888,"link_row_table_primary_field":"lieferant-primary"},` +
	`{"id":4,"name":"LookOut","type":"lookup","through_field_id":3,"target_field_id":4040,"target_field_name":"lookout-target"},` +
	`{"id":5,"name":"LookIn","type":"lookup","through_field_id":2,"target_field_id":5050,"target_field_name":"lookin-target"},` +
	`{"id":6,"name":"Roll","type":"rollup","through_field_id":3,"target_field_id":6060},` +
	`{"id":7,"name":"Formel","type":"formula","formula":"formula-canary"},` +
	`{"id":8,"name":"Summe","type":"number"}]`

const excludeRow = `{"id":7,"order":"1","Name":"Acme","Auftraege":[{"id":3,"value":"kept-in"}],` +
	`"Lieferant":[{"id":4,"value":"canary-out"}],"LookOut":[{"id":1,"value":"canary-lookout"}],` +
	`"LookIn":[{"id":2,"value":"kept-lookin"}]}`

var excludeToolArguments = map[string]string{
	fieldsList.ID:      `{"table_id":%d}`,
	rowsList.ID:        `{"table_id":%d}`,
	rowsGet.ID:         `{"table_id":%d,"row_id":7}`,
	rowsSearch.ID:      `{"table_id":%d}`,
	rowsCreate.ID:      `{"table_id":%d,"fields":{"Name":"x"}}`,
	rowsUpdate.ID:      `{"table_id":%d,"row_id":7,"fields":{"Name":"x"}}`,
	rowsDelete.ID:      `{"table_id":%d,"row_id":7}`,
	rowsMove.ID:        `{"table_id":%d,"row_id":7,"before_id":8}`,
	rowsBatchCreate.ID: `{"table_id":%d,"rows":[{"Name":"x"}]}`,
	rowsBatchUpdate.ID: `{"table_id":%d,"rows":[{"row_id":7,"fields":{"Name":"x"}}]}`,
	rowsBatchDelete.ID: `{"table_id":%d,"row_ids":[7]}`,
	filesUpload.ID:     `{"table_id":%d,"row_id":7,"field":"Anhang","content_base64":"aGk=","name":"a.txt"}`,
	filesGet.ID:        `{"table_id":%d,"row_id":7,"field":"Anhang"}`,
}

func excludeServer(r *http.Request) (*http.Response, error) {
	switch {
	case r.URL.Path == allTablesPath:
		return jsonResponse(200, excludeTablesBody), nil
	case strings.HasPrefix(r.URL.Path, "/api/database/fields/"):
		return jsonResponse(200, excludeFields), nil
	case strings.HasPrefix(r.URL.Path, "/api/database/views/table/"):
		return jsonResponse(200, `[]`), nil
	case r.Method != http.MethodGet:
		return jsonResponse(200, `{"id":7,"Lieferant":[{"id":4,"value":"canary-out"}],"LookOut":[{"id":1,"value":"canary-lookout"}]}`), nil
	case strings.Contains(r.URL.Path, "/7/"):
		return jsonResponse(200, excludeRow), nil
	}
	return jsonResponse(200, `{"count":1,"next":null,"results":[`+excludeRow+`]}`), nil
}

// excludeEnvironment offers every tool to the connection "ex" with the given targets and exclusions.
func excludeEnvironment(t *testing.T, calls *[]call, targets, excludes []string) *environment {
	t.Helper()
	serve(t, calls, excludeServer)
	cfg := testConfig()
	var tools []string
	for id := range excludeToolArguments {
		tools = append(tools, id)
	}
	tools = append(tools, tablesList.ID)
	cfg.Connections["ex"] = config.Connection{Service: "baserow", Credential: "token", Targets: targets, Exclude: excludes,
		Effects: []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}, Tools: tools,
		Files: config.Files{Read: []string{t.TempDir()}, Write: []string{t.TempDir()}}}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func TestExcludeDeclaredAndValidated(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	if !metadata.Target.Excludes() {
		t.Fatal("baserow declares no exclusions")
	}
	for name, tc := range map[string]struct {
		targets, excludes []string
		valid             bool
	}{
		"table":              {[]string{"*"}, []string{"table/5"}, true},
		"database":           {[]string{"*"}, []string{"database/5"}, true},
		"both":               {[]string{"database/1"}, []string{"table/5", "database/5"}, true},
		"hits nothing":       {[]string{"table/1"}, []string{"table/5", "database/9"}, true},
		"wildcard":           {[]string{"table/1"}, []string{"*"}, false},
		"unknown kind":       {[]string{"*"}, []string{"view/5"}, false},
		"zero":               {[]string{"*"}, []string{"table/0"}, false},
		"duplicate":          {[]string{"*"}, []string{"table/5", "table/5"}, false},
		"table is target":    {[]string{"table/5"}, []string{"table/5"}, false},
		"database is target": {[]string{"database/5", "table/1"}, []string{"database/5"}, false},
	} {
		if got := metadata.Target.CheckExcludes(tc.excludes, tc.targets); (len(got) == 0) != tc.valid {
			t.Errorf("%s: findings = %+v, valid %t", name, got, tc.valid)
		}
		if _, err := parseScope(tc.targets, tc.excludes...); (err == nil) != tc.valid {
			t.Errorf("%s: parseScope = %v, valid %t", name, err, tc.valid)
		}
	}
	// The provider also refuses what the core would, and never quotes a value.
	if _, err := parseScope([]string{"table/1"}, "table/777", "table/777"); err == nil || strings.Contains(err.Error(), "777") {
		t.Errorf("duplicate = %v", err)
	}
	// Exclusions are not targets: a connection with only an exclusion has no target.
	if _, err := parseScope(nil, "table/5"); err == nil {
		t.Error("exclude without a target was accepted")
	}
}

func TestExcludedTableIsRefusedBeforeSecretAndIOInEveryTool(t *testing.T) {
	for _, targets := range [][]string{{"*"}, {"database/2"}, {"table/11", "table/13"}} {
		for id, format := range excludeToolArguments {
			var calls []call
			env := excludeEnvironment(t, &calls, targets, []string{"table/99"})
			_, err := env.invokeConfirmed(id, "ex", strings.Replace(format, "%d", "99", 1))
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") || strings.Contains(err.Error(), "exclu") {
				t.Errorf("%v %s: err = %v", targets, id, err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Errorf("%v %s: calls = %v, secret reads = %d, want none", targets, id, paths(calls), *env.reads)
			}
		}
	}
}

func TestExcludedDatabaseIsRefusedAfterOneTableListRead(t *testing.T) {
	for name, tc := range map[string]struct {
		targets []string
		table   int
	}{
		"wildcard, table of the database":       {[]string{"*"}, 13},
		"table target inside excluded database": {[]string{"table/13"}, 13},
		"database target and its exclusion":     {[]string{"database/1", "table/13"}, 13},
		"table of no listed database":           {[]string{"*"}, 555},
	} {
		for _, id := range []string{rowsGet.ID, rowsSearch.ID, rowsCreate.ID, rowsBatchDelete.ID, fieldsList.ID, filesGet.ID} {
			var calls []call
			env := excludeEnvironment(t, &calls, tc.targets, []string{"database/3"})
			_, err := env.invokeConfirmed(id, "ex", strings.Replace(excludeToolArguments[id], "%d", strconv.Itoa(tc.table), 1))
			if !isInvalidRequest(err) || strings.Contains(err.Error(), strconv.Itoa(tc.table)) || strings.Contains(err.Error(), "exclu") {
				t.Errorf("%s %s: err = %v", name, id, err)
			}
			if want := []string{"GET " + allTablesPath}; !slices.Equal(paths(calls), want) {
				t.Errorf("%s %s: calls = %v, want one table list read", name, id, paths(calls))
			}
		}
	}
}

func TestTableOutsideTheExcludedDatabaseStaysReachable(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, []string{"database/3"})
	if _, err := env.invoke(rowsGet.ID, "ex", `{"table_id":11,"row_id":7}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + allTablesPath, "GET /api/database/rows/table/11/7/", "GET /api/database/fields/table/11/"}; !slices.Equal(paths(calls), want) {
		t.Fatalf("calls = %v, want %v", paths(calls), want)
	}
}

func TestExcludedTableWinsOverDatabaseTarget(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"database/1"}, []string{"table/12"})
	if _, err := env.invoke(rowsGet.ID, "ex", `{"table_id":12,"row_id":7}`); !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %v", err, paths(calls))
	}
	if _, err := env.invoke(rowsGet.ID, "ex", `{"table_id":11,"row_id":7}`); err != nil {
		t.Fatal(err)
	}
}

func TestTablesListDropsExcludedTablesWithoutExtraRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		targets, excludes []string
		want              []string
	}{
		"wildcard, table":    {[]string{"*"}, []string{"table/99"}, []string{"Kunden", "Auftraege", "Archiv"}},
		"wildcard, database": {[]string{"*"}, []string{"database/1", "database/3"}, []string{"Geheim"}},
		"database, table":    {[]string{"database/1", "table/99"}, []string{"table/12", "database/2"}, []string{"Kunden"}},
		"hits nothing":       {[]string{"table/11"}, []string{"table/5"}, []string{"Kunden"}},
		"table in database":  {[]string{"table/11", "table/99"}, []string{"database/1"}, []string{"Geheim"}},
	} {
		var calls []call
		env := excludeEnvironment(t, &calls, tc.targets, tc.excludes)
		result, err := env.invoke(tablesList.ID, "ex", `{}`)
		if err != nil || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %v", name, err, paths(calls))
		}
		for _, table := range []string{"Kunden", "Auftraege", "Archiv", "Geheim"} {
			if strings.Contains(result, table) != slices.Contains(tc.want, table) {
				t.Errorf("%s: %s in %s", name, table, result)
			}
		}
	}
}

func TestExcludedLinksAreMaskedUnderWildcard(t *testing.T) {
	for name, excludes := range map[string][]string{"table": {"table/99"}, "database": {"database/2"}} {
		for op, args := range map[string]string{
			rowsList.ID: `{"table_id":11}`, rowsGet.ID: `{"table_id":11,"row_id":7}`, rowsSearch.ID: `{"table_id":11}`,
			rowsCreate.ID: `{"table_id":11,"fields":{"Name":"x"}}`, rowsUpdate.ID: `{"table_id":11,"row_id":7,"fields":{"Name":"x"}}`,
		} {
			var calls []call
			env := excludeEnvironment(t, &calls, []string{"*"}, excludes)
			result, err := env.invokeConfirmed(op, "ex", args)
			if err != nil {
				t.Fatalf("%s %s: %v", name, op, err)
			}
			if strings.Contains(result, "canary-out") || strings.Contains(result, "canary-lookout") {
				t.Errorf("%s %s leaked: %s", name, op, result)
			}
			if op != rowsCreate.ID && op != rowsUpdate.ID {
				for _, want := range []string{`"Lieferant":[{"id":4}]`, "kept-in", "kept-lookin", `"omitted_fields":["LookOut"]`} {
					if !strings.Contains(result, want) {
						t.Errorf("%s %s lacks %s: %s", name, op, want, result)
					}
				}
			} else if !strings.Contains(result, `"Lieferant":[{"id":4}]`) {
				t.Errorf("%s %s: %s", name, op, result)
			}
		}
	}
}

func TestExcludedLinksAreMaskedInBatchAnswersUnderWildcard(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, []string{"table/99"})
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/database/fields/") {
			return jsonResponse(200, excludeFields), nil
		}
		return jsonResponse(200, `{"items":[{"id":9,"Lieferant":[{"id":4,"value":"canary-out"}]}]}`), nil
	})
	result, err := env.invokeConfirmed(rowsBatchCreate.ID, "ex", `{"table_id":11,"rows":[{"Name":"x"}]}`)
	if err != nil || strings.Contains(result, "canary-out") || !strings.Contains(result, `"Lieferant":[{"id":4}]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestWildcardWithoutExclusionStillReadsNoFields(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, nil)
	result, err := env.invoke(rowsGet.ID, "ex", `{"table_id":99,"row_id":7}`)
	if err != nil || !strings.Contains(result, "canary-out") || len(calls) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %v", result, err, paths(calls))
	}
}

func TestExcludedLinkTableCannotBeSearchedFilteredSortedOrWritten(t *testing.T) {
	for _, excludes := range [][]string{{"table/99"}, {"database/2"}} {
		for _, args := range []string{
			`{"table_id":11,"search":"x"}`,
			`{"table_id":11,"filters":[{"field":"Lieferant","type":"contains","value":"a"}]}`,
			`{"table_id":11,"filters":[{"field_id":3,"type":"link_row_has","value":"4"},{"field":"LookOut","type":"empty"}]}`,
			`{"table_id":11,"order_by":[{"field":"Lieferant"}]}`,
		} {
			var calls []call
			env := excludeEnvironment(t, &calls, []string{"*"}, excludes)
			_, err := env.invoke(rowsSearch.ID, "ex", args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "Lieferant") || strings.Contains(err.Error(), "99") {
				t.Errorf("%v %s: err = %v", excludes, args, err)
			}
			for _, c := range calls {
				if strings.HasPrefix(c.path, "/api/database/rows/") {
					t.Errorf("%v %s: a row request followed", excludes, args)
				}
			}
		}
		for _, op := range []struct{ id, args string }{
			{rowsCreate.ID, `{"table_id":11,"fields":{"Lieferant":[4]}}`},
			{rowsUpdate.ID, `{"table_id":11,"row_id":7,"fields":{"Lieferant":[4]}}`},
			{rowsBatchCreate.ID, `{"table_id":11,"rows":[{"Name":"a"},{"Lieferant":[4]}]}`},
			{rowsBatchUpdate.ID, `{"table_id":11,"rows":[{"row_id":7,"fields":{"Lieferant":[4]}}]}`},
		} {
			var calls []call
			env := excludeEnvironment(t, &calls, []string{"*"}, excludes)
			_, err := env.invokeConfirmed(op.id, "ex", op.args)
			if !isInvalidRequest(err) || len(changes(calls)) != 0 {
				t.Errorf("%v %s: err = %v, calls = %v", excludes, op.id, err, paths(calls))
			}
		}
	}
}

func TestExcludedLinkTableKeepsMaskedFiltersAndReachableLinksWritable(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, []string{"table/99"})
	for _, filter := range []string{"empty", "not_empty", "link_row_has", "link_row_has_not"} {
		value := `,"value":"4"`
		if filter == "empty" || filter == "not_empty" {
			value = ""
		}
		args := `{"table_id":11,"filters":[{"field":"Lieferant","type":"` + filter + `"` + value + `}]}`
		if _, err := env.invoke(rowsSearch.ID, "ex", args); err != nil {
			t.Errorf("%s: %v", filter, err)
		}
	}
	if _, err := env.invokeConfirmed(rowsUpdate.ID, "ex", `{"table_id":11,"row_id":7,"fields":{"Auftraege":[4]}}`); err != nil {
		t.Errorf("reachable link: %v", err)
	}
}

func TestFieldsListDropsDetailsOfExcludedLinkTargets(t *testing.T) {
	for _, excludes := range [][]string{{"table/99"}, {"database/2"}} {
		var calls []call
		env := excludeEnvironment(t, &calls, []string{"*"}, excludes)
		result, err := env.invoke(fieldsList.ID, "ex", `{"table_id":11}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaked := range []string{`"link_table_id":99`, "777", "888", "lieferant-primary", "lookout-target", "4040", "6060", "formula-canary"} {
			if strings.Contains(result, leaked) {
				t.Errorf("%v: %s leaked: %s", excludes, leaked, result)
			}
		}
		if !strings.Contains(result, `"link_table_id":12`) || !strings.Contains(result, "501") || !strings.Contains(result, "lookin-target") {
			t.Errorf("%v: reachable parts missing: %s", excludes, result)
		}
	}
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, nil)
	result, err := env.invoke(fieldsList.ID, "ex", `{"table_id":11}`)
	if err != nil || !strings.Contains(result, `"link_table_id":99`) || !strings.Contains(result, "formula-canary") || len(calls) != 1 {
		t.Fatalf("wildcard result = %s, err = %v, calls = %v", result, err, paths(calls))
	}
}

func TestUndeterminedDatabaseOfLinkTargetCountsAsExcluded(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, &calls, []string{"*"}, []string{"database/3"})
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == allTablesPath {
			return jsonResponse(200, `[{"id":11,"name":"Kunden","database_id":1}]`), nil
		}
		return excludeServer(r)
	})
	result, err := env.invoke(rowsGet.ID, "ex", `{"table_id":11,"row_id":7}`)
	if err != nil || strings.Contains(result, "canary-out") || strings.Contains(result, "kept-in") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}
