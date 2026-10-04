package n8n

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	ownColumn     = "c1"
	foreignColumn = "COLUMN_OF_ANOTHER_TABLE"
)

func columnJSONOf(id, name, kind string, index int) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"type":%q,"index":%d,"dataTableId":%q,`+
		`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z"}`, id, name, kind, index, ownTable)
}

// columnServer extends tableServer with the column routes of ownTable.
func columnServer(t *testing.T, mutations *[]string) func(*http.Request) (*http.Response, error) {
	base := tableServer(t, mutations)
	cols := apiPath + "/data-tables/" + ownTable + "/columns"
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == cols:
			return jsonResponse(200, "["+columnJSONOf(ownColumn, "email", "string", 0)+"]"), nil
		case r.Method == http.MethodPost && r.URL.Path == cols:
			b, _ := io.ReadAll(r.Body)
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" "+string(b))
			return jsonResponse(201, columnJSONOf("NEWCOLUMN0001", "age", "number", 1)), nil
		case r.Method == http.MethodPatch && r.URL.Path == cols+"/"+ownColumn:
			b, _ := io.ReadAll(r.Body)
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" "+string(b))
			return jsonResponse(200, columnJSONOf(ownColumn, "mail", "string", 0)), nil
		case r.Method == http.MethodDelete && r.URL.Path == cols+"/"+ownColumn:
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" ")
			return jsonResponse(204, ``), nil
		}
		return base(r)
	}
}

func TestDataColumnsListReadsTheBoundTable(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, columnServer(t, &mutations))
	result, err := env.invoke(dataColumnsList.ID, "project", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	if err != nil || !strings.Contains(result, `"email"`) || !strings.Contains(result, `"count":1`) {
		t.Fatalf("list = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || len(mutations) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestDataColumnsOfAForeignTableAreRefusedWithoutMutation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataColumnsList.ID, fmt.Sprintf(`{"data_table_id":%q}`, foreignTable)},
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number"}`, foreignTable)},
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","name":"x"}`, foreignTable)},
		{dataColumnsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, foreignTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, columnServer(t, &mutations))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 || calls[0].method != http.MethodGet ||
				strings.Contains(err.Error(), foreignProject) || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
			}
		})
	}
}

func TestDataColumnsOfAnUnknownColumnAreRefusedWithoutMutation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":%q,"name":"x"}`, ownTable, foreignColumn)},
		{dataColumnsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":%q}`, ownTable, foreignColumn)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, columnServer(t, &mutations))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
			}
		})
	}
}

func TestDataColumnChangesRequireConfirmationAndRefuseBadInputLocally(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number"}`, ownTable)},
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","name":"x"}`, ownTable)},
		{dataColumnsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.invoke(tt.operation, "pdelete", tt.arguments)
			if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
	for _, tt := range []struct{ operation, arguments string }{
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"1bad","type":"number"}`, ownTable)},
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"ok","type":"json"}`, ownTable)},
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"ok","type":"string","index":-1}`, ownTable)},
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable)},
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","type":"string"}`, ownTable)},
		{dataColumnsAdd.ID, `{"data_table_id":"../x","name":"ok","type":"string"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s %s: err = %v, calls = %+v", tt.operation, tt.arguments, err, calls)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(dataColumnsList.ID, "workflow", fmt.Sprintf(`{"data_table_id":%q}`, ownTable))
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("workflow allow-list: err = %v", err)
	}
}

func TestDataColumnToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, columnServer(t, &mutations))
	if result, err := env.confirmed(dataColumnsAdd.ID, "pdelete",
		fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number","index":1}`, ownTable)); err != nil ||
		!strings.Contains(result, "NEWCOLUMN0001") {
		t.Fatalf("add = %s, %v", result, err)
	}
	if result, err := env.confirmed(dataColumnsUpdate.ID, "pdelete",
		fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","name":"mail","index":0}`, ownTable)); err != nil ||
		!strings.Contains(result, `"updated":true`) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if result, err := env.confirmed(dataColumnsDelete.ID, "pdelete",
		fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable)); err != nil ||
		!strings.Contains(result, `"deleted":true`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	base := fmt.Sprintf("%s/data-tables/%s/columns", apiPath, ownTable)
	want := []string{
		`POST ` + base + ` {"index":1,"name":"age","type":"number"}`,
		`PATCH ` + base + `/c1 {"index":0,"name":"mail"}`,
		`DELETE ` + base + `/c1 `,
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q, want %q", mutations, want)
	}
}

func TestDataColumnDeleteIsOnlyOfferedByAToolsListAndProfilesAreOwn(t *testing.T) {
	if !dataColumnsDelete.RequiresToolAllowList || dataColumnsDelete.Risk.Effect != "delete" ||
		dataColumnsDelete.Risk.Confirmation != "required" || dataColumnsAdd.RequiresToolAllowList {
		t.Fatalf("descriptor = %+v", dataColumnsDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == dataColumnsDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.datacolumns.") && !strings.HasPrefix(profile.ID, "datacolumns") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(dataColumnsDelete.ID, "project", fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want delete unavailable without a tools list", err, calls)
	}
}

func TestDataColumnForbiddenIsReportedAsLicenseOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataColumnsList.ID, fmt.Sprintf(`{"data_table_id":%q}`, ownTable)},
		{dataColumnsAdd.ID, fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number"}`, ownTable)},
		{dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","name":"x"}`, ownTable)},
		{dataColumnsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := columnServer(t, &mutations)
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/columns") {
					return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
				}
				return base(r)
			})
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "license") ||
				!strings.Contains(err.Error(), "role") || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, want a license and role error", err)
			}
		})
	}
}

func TestDataColumnChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	add := fmt.Sprintf(`{"data_table_id":%q,"name":"age","type":"number"}`, ownTable)
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"add 5xx", dataColumnsAdd.ID, add, func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"add unreadable", dataColumnsAdd.ID, add, func() (*http.Response, error) { return jsonResponse(201, `not json`), nil }},
		{"update transport", dataColumnsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1","name":"x"}`, ownTable),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete 502", dataColumnsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,"column_id":"c1"}`, ownTable),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := columnServer(t, &mutations)
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
