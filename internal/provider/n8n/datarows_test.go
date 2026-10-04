package n8n

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// rowServer extends tableServer with the row routes of ownTable, recording the raw query of changes.
func rowServer(t *testing.T, mutations *[]string, rowsBody string) func(*http.Request) (*http.Response, error) {
	base := tableServer(t, mutations)
	rows := apiPath + "/data-tables/" + ownTable + "/rows"
	return func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.Path, rows) {
			return base(r)
		}
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		if r.Method != http.MethodGet {
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" "+r.URL.Query().Get("filter")+string(b))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == rows:
			return jsonResponse(200, rowsBody), nil
		case r.Method == http.MethodPost && r.URL.Path == rows:
			return jsonResponse(200, `{"count":2}`), nil
		case r.URL.Path == rows+"/update" || r.URL.Path == rows+"/upsert" || r.URL.Path == rows+"/delete":
			return jsonResponse(200, `true`), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

const emailEq = `"filter":{"conditions":[{"column":"email","operator":"eq","value":"a@example.com"}]}`

func tableArg() string { return fmt.Sprintf(`"data_table_id":%q`, ownTable) }

func TestDataRowsOfAForeignTableAreRefusedWithoutMutation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataRowsList.ID, fmt.Sprintf(`{"data_table_id":%q}`, foreignTable)},
		{dataRowsInsert.ID, fmt.Sprintf(`{"data_table_id":%q,"rows":[{"email":"a"}]}`, foreignTable)},
		{dataRowsUpdate.ID, fmt.Sprintf(`{"data_table_id":%q,%s,"data":{"email":"b"}}`, foreignTable, emailEq)},
		{dataRowsUpsert.ID, fmt.Sprintf(`{"data_table_id":%q,%s,"data":{"email":"b"}}`, foreignTable, emailEq)},
		{dataRowsDelete.ID, fmt.Sprintf(`{"data_table_id":%q,%s}`, foreignTable, emailEq)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[]}`))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 || calls[0].method != http.MethodGet ||
				strings.Contains(err.Error(), foreignProject) || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
			}
		})
	}
}

func TestDataRowsUnknownColumnsAndWrongTypesAreRefusedWithoutMutation(t *testing.T) {
	bad := `"filter":{"conditions":[{"column":"nope","operator":"eq","value":"x"}]}`
	for _, tt := range []struct{ name, operation, arguments string }{
		{"list filter column", dataRowsList.ID, `{` + tableArg() + `,` + bad + `}`},
		{"insert column", dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"nope":"x"}]}`},
		{"insert type", dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":5}]}`},
		{"insert null", dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":null}]}`},
		{"insert object", dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":{"a":1}}]}`},
		{"insert empty row", dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{}]}`},
		{"update filter column", dataRowsUpdate.ID, `{` + tableArg() + `,` + bad + `,"data":{"email":"b"}}`},
		{"update data column", dataRowsUpdate.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"nope":"b"}}`},
		{"upsert data column", dataRowsUpsert.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"nope":"b"}}`},
		{"delete filter column", dataRowsDelete.ID, `{` + tableArg() + `,` + bad + `}`},
		{"delete filter type", dataRowsDelete.ID, `{` + tableArg() +
			`,"filter":{"conditions":[{"column":"email","operator":"eq","value":true}]}}`},
		{"gt on string", dataRowsDelete.ID, `{` + tableArg() +
			`,"filter":{"conditions":[{"column":"email","operator":"gt","value":"x"}]}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[]}`))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v, mutations = %v", err, calls, mutations)
			}
		})
	}
}

func TestDataRowsEmptyFilterAndBadShapesAreRefusedLocally(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataRowsUpdate.ID, `{` + tableArg() + `,"data":{"email":"b"}}`},
		{dataRowsUpdate.ID, `{` + tableArg() + `,"filter":{"conditions":[]},"data":{"email":"b"}}`},
		{dataRowsUpdate.ID, `{` + tableArg() + `,"filter":{},"data":{"email":"b"}}`},
		{dataRowsUpsert.ID, `{` + tableArg() + `,"filter":{"conditions":[]},"data":{"email":"b"}}`},
		{dataRowsDelete.ID, `{` + tableArg() + `}`},
		{dataRowsDelete.ID, `{` + tableArg() + `,"filter":{"conditions":[]}}`},
		{dataRowsDelete.ID, `{` + tableArg() + `,"filter":"email = 1"}`},
		{dataRowsDelete.ID, `{` + tableArg() + `,"filter":{"conditions":[{"column":"email","operator":"regex","value":"x"}]}}`},
		{dataRowsInsert.ID, `{` + tableArg() + `,"rows":[]}`},
		{dataRowsInsert.ID, `{` + tableArg() + `,"rows":[` + strings.TrimSuffix(strings.Repeat(`{"email":"a"},`, maxRowsPerInsert+1), ",") + `]}`},
		{dataRowsList.ID, `{` + tableArg() + `,"limit":101}`},
		{dataRowsList.ID, `{` + tableArg() + `,"filter":{"conditions":[]}}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s %.80s: err = %v, calls = %+v", tt.operation, tt.arguments, err, calls)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(dataRowsList.ID, "workflow", `{`+tableArg()+`}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("workflow allow-list: err = %v", err)
	}
}

func TestDataRowChangesRequireConfirmation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":"a"}]}`},
		{dataRowsUpdate.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`},
		{dataRowsUpsert.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`},
		{dataRowsDelete.ID, `{` + tableArg() + `,` + emailEq + `}`},
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
}

func TestDataRowToolsSendStructuredRequestsExactly(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, rowServer(t, &mutations, `{"data":[]}`))
	for _, tt := range []struct{ operation, arguments, key string }{
		{dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":"a"},{"email":"b"}]}`, `"inserted":2`},
		{dataRowsUpdate.ID, `{` + tableArg() + `,"filter":{"type":"or","conditions":[` +
			`{"column":"email","operator":"like","value":"%x"},{"column":"email","operator":"neq","value":"y"}]},` +
			`"data":{"email":"b"}}`, `"updated":true`},
		{dataRowsUpsert.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`, `"upserted":true`},
		{dataRowsDelete.ID, `{` + tableArg() + `,` + emailEq + `}`, `"deleted":true`},
	} {
		if result, err := env.confirmed(tt.operation, "pdelete", tt.arguments); err != nil || !strings.Contains(result, tt.key) {
			t.Fatalf("%s = %s, %v", tt.operation, result, err)
		}
	}
	base := fmt.Sprintf("%s/data-tables/%s/rows", apiPath, ownTable)
	eq := `{"filters":[{"columnName":"email","condition":"eq","value":"a@example.com"}],"type":"and"}`
	want := []string{
		`POST ` + base + ` {"data":[{"email":"a"},{"email":"b"}],"returnType":"count"}`,
		`PATCH ` + base + `/update {"data":{"email":"b"},"filter":{"filters":[` +
			`{"columnName":"email","condition":"like","value":"%x"},` +
			`{"columnName":"email","condition":"neq","value":"y"}],"type":"or"}}`,
		`POST ` + base + `/upsert {"data":{"email":"b"},"filter":` + eq + `}`,
		`DELETE ` + base + `/delete ` + eq,
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q\nwant %q", mutations, want)
	}
}

func TestDataRowsListSendsFilterAndCapsTheOutput(t *testing.T) {
	long := strings.Repeat("x", 5000)
	rows := fmt.Sprintf(`{"data":[{"id":1,"email":%q,"nested":{"a":1},"createdAt":"c"},{"id":2,"email":"b"}],`+
		`"nextCursor":"next"}`, long)
	var calls []call
	var mutations []string
	var query string
	base := rowServer(t, &mutations, rows)
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rows") {
			query = r.URL.RawQuery
		}
		return base(r)
	})
	result, err := env.invoke(dataRowsList.ID, "pdelete", `{`+tableArg()+`,`+emailEq+`,"limit":1,"cursor":"c0"}`)
	if err != nil || strings.Contains(result, long) || strings.Contains(result, "nested") ||
		!strings.Contains(result, `"count":1`) || !strings.Contains(result, `"has_more":true`) ||
		!strings.Contains(result, `"cursor":"next"`) || len(result) > 2000 {
		t.Fatalf("result = %.300s, %v", result, err)
	}
	want := "cursor=c0&filter=%7B%22filters%22%3A%5B%7B%22columnName%22%3A%22email%22%2C%22condition%22%3A%22eq%22%2C" +
		"%22value%22%3A%22a%40example.com%22%7D%5D%2C%22type%22%3A%22and%22%7D&limit=1"
	if query != want || len(mutations) != 0 {
		t.Fatalf("query = %s", query)
	}
}

func TestDataRowUpdateUpsertDeleteAreOnlyOfferedByAToolsListAndProfilesAreOwn(t *testing.T) {
	for _, d := range []struct {
		id    string
		allow bool
	}{{dataRowsList.ID, false}, {dataRowsInsert.ID, false}, {dataRowsUpdate.ID, true},
		{dataRowsUpsert.ID, true}, {dataRowsDelete.ID, true}} {
		descriptor := map[string]bool{
			dataRowsList.ID: dataRowsList.RequiresToolAllowList, dataRowsInsert.ID: dataRowsInsert.RequiresToolAllowList,
			dataRowsUpdate.ID: dataRowsUpdate.RequiresToolAllowList, dataRowsUpsert.ID: dataRowsUpsert.RequiresToolAllowList,
			dataRowsDelete.ID: dataRowsDelete.RequiresToolAllowList}[d.id]
		if descriptor != d.allow {
			t.Fatalf("%s RequiresToolAllowList = %v", d.id, descriptor)
		}
	}
	if dataRowsDelete.Risk.Effect != "delete" || dataRowsDelete.Risk.Confirmation != "required" {
		t.Fatalf("delete risk = %+v", dataRowsDelete.Risk)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == dataRowsUpdate.ID || id == dataRowsUpsert.ID || id == dataRowsDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.datarows.") && !strings.HasPrefix(profile.ID, "datarows") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	for _, id := range []string{dataRowsUpdate.ID, dataRowsUpsert.ID, dataRowsDelete.ID} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(id, "project", `{`+tableArg()+`,`+emailEq+`,"data":{"email":"b"}}`)
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, want unavailable without a tools list", id, err, calls)
		}
	}
}

func TestDataRowForbiddenIsReportedAsLicenseOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{dataRowsList.ID, `{` + tableArg() + `}`},
		{dataRowsInsert.ID, `{` + tableArg() + `,"rows":[{"email":"a"}]}`},
		{dataRowsUpdate.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`},
		{dataRowsUpsert.ID, `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`},
		{dataRowsDelete.ID, `{` + tableArg() + `,` + emailEq + `}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := rowServer(t, &mutations, `{"data":[]}`)
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/rows") {
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

func TestDataRowChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	insert := `{` + tableArg() + `,"rows":[{"email":"a"}]}`
	update := `{` + tableArg() + `,` + emailEq + `,"data":{"email":"b"}}`
	del := `{` + tableArg() + `,` + emailEq + `}`
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"insert 5xx", dataRowsInsert.ID, insert, func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"insert unreadable", dataRowsInsert.ID, insert, func() (*http.Response, error) { return jsonResponse(200, `not json`), nil }},
		{"insert no count", dataRowsInsert.ID, insert, func() (*http.Response, error) { return jsonResponse(200, `{}`), nil }},
		{"update transport", dataRowsUpdate.ID, update, func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"upsert 502", dataRowsUpsert.ID, update, func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
		{"delete not true", dataRowsDelete.ID, del, func() (*http.Response, error) { return jsonResponse(200, `false`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := rowServer(t, &mutations, `{"data":[]}`)
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
