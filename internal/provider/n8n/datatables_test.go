package n8n

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	ownTable     = "TABLE_OWN_0001AAAA"
	foreignTable = "TABLE_FOREIGN_0002B"
)

func tableJSONOf(id, name, project string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"columns":[{"id":"c1","name":"email","type":"string","index":0,`+
		`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z"}],"projectId":%q,`+
		`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z","sizeBytes":8192}`,
		id, name, project)
}

// tableServer answers the pre-read for ownTable and foreignTable and records mutations.
func tableServer(t *testing.T, mutations *[]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		raw := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			raw = string(b)
		}
		if r.Method != http.MethodGet {
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" "+raw)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/data-tables/"+ownTable:
			return jsonResponse(200, tableJSONOf(ownTable, "Own", ownProject)), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/data-tables/"+foreignTable:
			return jsonResponse(200, tableJSONOf(foreignTable, foreignCanary, foreignProject)), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/data-tables":
			return jsonResponse(201, tableJSONOf("NEWTABLE0001", "customers", ownProject)), nil
		case r.Method == http.MethodPatch && r.URL.Path == apiPath+"/data-tables/"+ownTable:
			return jsonResponse(200, tableJSONOf(ownTable, "renamed", ownProject)), nil
		case r.Method == http.MethodDelete && r.URL.Path == apiPath+"/data-tables/"+ownTable:
			return jsonResponse(204, ``), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func TestDataTablesListIsFilteredToTheProjectAllowList(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":"abc"}`,
		tableJSONOf(ownTable, "Own", ownProject), tableJSONOf(foreignTable, foreignCanary, foreignProject))
	for _, tt := range []struct {
		connection string
		want       int
	}{{"project", 1}, {"open", 2}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != apiPath+"/data-tables" {
				t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(200, body), nil
		})
		result, err := env.invoke(dataTablesList.ID, tt.connection, `{}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", tt.connection, err)
		}
		var page DataTablesPage
		if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != tt.want || !page.HasMore {
			t.Fatalf("%s: page = %s, %v", tt.connection, result, err)
		}
		if tt.connection == "project" && (strings.Contains(result, foreignProject) || strings.Contains(result, foreignCanary)) {
			t.Fatalf("restricted list leaked a foreign table: %s", result)
		}
	}
}

func TestDataTablesListProjectFilterAndRefusals(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	})
	if _, err := env.invoke(dataTablesList.ID, "open", fmt.Sprintf(`{"project_id":%q}`, ownProject)); err != nil {
		t.Fatal(err)
	}
	if got := calls[0].query.Get("filter"); got != fmt.Sprintf(`{"projectId":%q}`, ownProject) {
		t.Fatalf("filter = %q", got)
	}
	for _, tt := range []struct{ connection, arguments string }{
		{"project", fmt.Sprintf(`{"project_id":%q}`, foreignProject)},
		{"workflow", `{}`}, {"both", `{}`},
	} {
		var none []call
		e := newEnvironment(t, &none, noRequest(t))
		_, err := e.invoke(dataTablesList.ID, tt.connection, tt.arguments)
		if !isInvalidRequest(err) || len(none) != 0 || *e.reads != 0 || strings.Contains(err.Error(), foreignProject) {
			t.Fatalf("%s: err = %v, calls = %+v", tt.connection, err, none)
		}
	}
}

func TestDataTablesGetReadsAndRefusesForeign(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, tableServer(t, &mutations))
	result, err := env.invoke(dataTablesGet.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	if err != nil || !strings.Contains(result, `"email"`) || !strings.Contains(result, ownProject) {
		t.Fatalf("get = %s, %v", result, err)
	}
	_, err = env.invoke(dataTablesGet.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, foreignTable))
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignProject) || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v, want a refusal without the foreign target", err)
	}
}

func TestDataTableChangesRequireConfirmationAndSendNoRequestWithoutIt(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataTablesCreate.ID, fmt.Sprintf(`{"name":"X","project_id":%q}`, ownProject)},
		{dataTablesRename.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"X"}`, ownTable)},
		{dataTablesDelete.ID, fmt.Sprintf(`{"data_table_id":%q}`, ownTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.invoke(tt.operation, "pdelete", tt.arguments)
			if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
			}
		})
	}
}

func TestDataTableCreateIsRefusedLocally(t *testing.T) {
	for _, tt := range []struct{ name, connection, arguments string }{
		{"foreign project", "pdelete", fmt.Sprintf(`{"name":"X","project_id":%q}`, foreignProject)},
		{"no project under allow-list", "pdelete", `{"name":"X"}`},
		{"workflow list", "pdelete-workflow", fmt.Sprintf(`{"name":"X","project_id":%q}`, ownProject)},
		{"control character", "pdelete-open", `{"name":"a\nb"}`},
		{"bad project id", "pdelete-open", `{"name":"X","project_id":"a/b"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(dataTablesCreate.ID, tt.connection, tt.arguments)
			if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
			}
		})
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(dataTablesCreate.ID, "pdelete-open", `{"name":"`+strings.Repeat("a", 129)+`"}`)
	if err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want an oversized name refused", err, calls)
	}
}

func TestDataTableMutationsOfAForeignTableAreRefusedAfterTheReadWithoutMutation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataTablesRename.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"X"}`, foreignTable)},
		{dataTablesDelete.ID, fmt.Sprintf(`{"data_table_id":%q}`, foreignTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, tableServer(t, &mutations))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
			}
			if strings.Contains(err.Error(), foreignProject) || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("error names the foreign target: %v", err)
			}
		})
	}
}

func TestDataTableToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, tableServer(t, &mutations))
	if result, err := env.confirmed(dataTablesCreate.ID, "pdelete", fmt.Sprintf(`{"name":"customers","project_id":%q}`, ownProject)); err != nil ||
		!strings.Contains(result, "NEWTABLE0001") {
		t.Fatalf("create = %s, %v", result, err)
	}
	if result, err := env.confirmed(dataTablesRename.ID, "pdelete", fmt.Sprintf(`{"data_table_id":%q,"name":"renamed"}`, ownTable)); err != nil ||
		!strings.Contains(result, `"updated":true`) {
		t.Fatalf("rename = %s, %v", result, err)
	}
	if result, err := env.confirmed(dataTablesDelete.ID, "pdelete", fmt.Sprintf(`{"data_table_id":%q}`, ownTable)); err != nil ||
		!strings.Contains(result, `"deleted":true`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	want := []string{
		fmt.Sprintf(`POST %s/data-tables {"columns":[],"name":"customers","projectId":%q}`, apiPath, ownProject),
		fmt.Sprintf(`PATCH %s/data-tables/%s {"name":"renamed"}`, apiPath, ownTable),
		fmt.Sprintf(`DELETE %s/data-tables/%s `, apiPath, ownTable),
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q, want %q", mutations, want)
	}
	if len(calls) != 5 { // create, then two pre-reads plus rename and delete
		t.Fatalf("calls = %d, want 5", len(calls))
	}
}

func TestDataTableDeleteIsOnlyOfferedByAToolsList(t *testing.T) {
	if !dataTablesDelete.RequiresToolAllowList || dataTablesDelete.Risk.Effect != "delete" ||
		dataTablesDelete.Risk.Confirmation != "required" || dataTablesCreate.RequiresToolAllowList {
		t.Fatalf("descriptor = %+v", dataTablesDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		got[profile.ID] = profile.Tools
		for _, id := range profile.Tools {
			if id == dataTablesDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.datatables.") && !strings.HasPrefix(profile.ID, "datatables") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	if strings.Join(got["datatables-read"], ",") != dataTablesList.ID+","+dataTablesGet.ID ||
		len(got["datatables-manage"]) != 4 {
		t.Fatalf("profiles = %v", got)
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(dataTablesDelete.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want delete unavailable without a tools list", err, calls)
	}
}

func TestDataTableForbiddenIsReportedAsLicenseOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataTablesList.ID, `{}`},
		{dataTablesGet.ID, fmt.Sprintf(`{"data_table_id":%q}`, ownTable)},
		{dataTablesCreate.ID, fmt.Sprintf(`{"name":"X","project_id":%q}`, ownProject)},
		{dataTablesRename.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"X"}`, ownTable)},
		{dataTablesDelete.ID, fmt.Sprintf(`{"data_table_id":%q}`, ownTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
			})
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "license") ||
				!strings.Contains(err.Error(), "role") || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, want a license and role error", err)
			}
		})
	}
}

func TestDataTableChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"create 5xx", dataTablesCreate.ID, fmt.Sprintf(`{"name":"X","project_id":%q}`, ownProject),
			func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"create unreadable", dataTablesCreate.ID, fmt.Sprintf(`{"name":"X","project_id":%q}`, ownProject),
			func() (*http.Response, error) { return jsonResponse(201, `not json`), nil }},
		{"rename transport", dataTablesRename.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"X"}`, ownTable),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete 502", dataTablesDelete.ID, fmt.Sprintf(`{"data_table_id":%q}`, ownTable),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := tableServer(t, &mutations)
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return base(r)
				}
				return tt.respond()
			})
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("err = %v, want uncertain", err)
			}
			changes := 0
			for _, c := range calls {
				if c.method != http.MethodGet {
					changes++
				}
			}
			if changes != 1 {
				t.Fatalf("calls = %+v, want exactly one changing request", calls)
			}
		})
	}
}
