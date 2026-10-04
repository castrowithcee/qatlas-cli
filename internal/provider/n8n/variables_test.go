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
	ownVariable     = "VAR_OWN_0001AAAA"
	foreignVariable = "VAR_FOREIGN_0002B"
	globalVariable  = "VAR_GLOBAL_0003C"
)

func variableJSONOf(id, key, value, project string) string {
	p := `null`
	if project != "" {
		p = fmt.Sprintf(`{"id":%q,"name":"P","type":"team"}`, project)
	}
	return fmt.Sprintf(`{"id":%q,"key":%q,"value":%q,"type":"string","project":%s}`, id, key, value, p)
}

func variablesBody() string {
	return fmt.Sprintf(`{"data":[%s,%s,%s],"nextCursor":null}`,
		variableJSONOf(ownVariable, "OWN_KEY", "own", ownProject),
		variableJSONOf(foreignVariable, "FOREIGN_KEY", foreignCanary, foreignProject),
		variableJSONOf(globalVariable, "GLOBAL_KEY", "glob", ""))
}

func variableServer(t *testing.T, mutations *[]string) func(*http.Request) (*http.Response, error) {
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
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/variables":
			return jsonResponse(200, variablesBody()), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/variables":
			return jsonResponse(201, ``), nil
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, apiPath+"/variables/"),
			r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, apiPath+"/variables/"):
			return jsonResponse(204, ``), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func TestVariablesListSeparatesProjectAndGlobal(t *testing.T) {
	for _, tt := range []struct {
		connection string
		want       int
	}{{"project", 1}, {"open", 3}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, variablesBody()), nil
		})
		result, err := env.invoke(variablesList.ID, tt.connection, `{}`)
		var page VariablesPage
		if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != tt.want {
			t.Fatalf("%s: %s, %v", tt.connection, result, err)
		}
		if tt.connection == "project" && (strings.Contains(result, foreignProject) ||
			strings.Contains(result, foreignCanary) || strings.Contains(result, "GLOBAL_KEY") ||
			page.Variables[0].Scope != "project" || page.Variables[0].ProjectID != ownProject) {
			t.Fatalf("restricted list leaked: %s", result)
		}
		if tt.connection == "open" && page.Variables[2].Scope != "global" {
			t.Fatalf("scope = %+v", page.Variables)
		}
	}
}

func TestVariablesListFilterAndRefusals(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	})
	if _, err := env.invoke(variablesList.ID, "open", fmt.Sprintf(`{"project_id":%q,"limit":5}`, ownProject)); err != nil {
		t.Fatal(err)
	}
	if calls[0].query.Get("projectId") != ownProject || calls[0].query.Get("limit") != "5" {
		t.Fatalf("query = %v", calls[0].query)
	}
	for _, tt := range []struct{ connection, arguments string }{
		{"project", fmt.Sprintf(`{"project_id":%q}`, foreignProject)}, {"workflow", `{}`}, {"both", `{}`},
	} {
		var none []call
		e := newEnvironment(t, &none, noRequest(t))
		_, err := e.invoke(variablesList.ID, tt.connection, tt.arguments)
		if !isInvalidRequest(err) || len(none) != 0 || *e.reads != 0 || strings.Contains(err.Error(), foreignProject) {
			t.Fatalf("%s: err = %v", tt.connection, err)
		}
	}
}

func TestVariableChangesRequireConfirmation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{variablesCreate.ID, fmt.Sprintf(`{"key":"K","value":"v","project_id":%q}`, ownProject)},
		{variablesUpdate.ID, fmt.Sprintf(`{"variable_id":%q,"key":"K","value":"v"}`, ownVariable)},
		{variablesDelete.ID, fmt.Sprintf(`{"variable_id":%q}`, ownVariable)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(tt.operation, "pdelete", tt.arguments)
		if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v", tt.operation, err)
		}
	}
}

func TestVariableCreateIsRefusedLocally(t *testing.T) {
	for _, tt := range []struct{ name, connection, arguments string }{
		{"global on bound", "pdelete", `{"key":"K","value":"v"}`},
		{"foreign project", "pdelete", fmt.Sprintf(`{"key":"K","value":"v","project_id":%q}`, foreignProject)},
		{"workflow target", "pdelete-workflow", `{"key":"K","value":"v"}`},
		{"digit first", "pdelete-open", `{"key":"1K","value":"v"}`},
		{"bad key", "pdelete-open", `{"key":"a-b","value":"v"}`},
		{"long key", "pdelete-open", `{"key":"` + strings.Repeat("a", 51) + `","value":"v"}`},
		{"long value", "pdelete-open", `{"key":"K","value":"` + strings.Repeat("a", 1001) + `"}`},
		{"bad project id", "pdelete-open", `{"key":"K","value":"v","project_id":"a/b"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(variablesCreate.ID, tt.connection, tt.arguments)
			if err == nil || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}

func TestVariableMutationsOfForeignOrGlobalAreRefusedAfterTheRead(t *testing.T) {
	for _, id := range []string{foreignVariable, globalVariable, "VAR_MISSING_0004D"} {
		for _, tt := range []struct{ operation, arguments string }{
			{variablesUpdate.ID, fmt.Sprintf(`{"variable_id":%q,"key":"K","value":"v"}`, id)},
			{variablesDelete.ID, fmt.Sprintf(`{"variable_id":%q}`, id)},
		} {
			var calls []call
			var mutations []string
			env := newEnvironment(t, &calls, variableServer(t, &mutations))
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if !isInvalidRequest(err) || len(mutations) != 0 || len(calls) != 1 || calls[0].method != http.MethodGet ||
				strings.Contains(err.Error(), foreignProject) || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s %s: err = %v, calls = %+v", tt.operation, id, err, calls)
			}
		}
	}
}

func TestVariableToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, variableServer(t, &mutations))
	steps := []struct{ operation, connection, arguments string }{
		{variablesCreate.ID, "pdelete", fmt.Sprintf(`{"key":"NEW_KEY","value":"x","project_id":%q}`, ownProject)},
		{variablesCreate.ID, "pdelete-open", `{"key":"NEW_GLOBAL","value":"y"}`},
		{variablesUpdate.ID, "pdelete", fmt.Sprintf(`{"variable_id":%q,"key":"OWN_KEY","value":"z"}`, ownVariable)},
		{variablesUpdate.ID, "pdelete-open", fmt.Sprintf(`{"variable_id":%q,"key":"GLOBAL_KEY","value":"g"}`, globalVariable)},
		{variablesDelete.ID, "pdelete", fmt.Sprintf(`{"variable_id":%q}`, ownVariable)},
		{variablesDelete.ID, "pdelete-open", fmt.Sprintf(`{"variable_id":%q}`, globalVariable)},
	}
	for _, s := range steps {
		if _, err := env.confirmed(s.operation, s.connection, s.arguments); err != nil {
			t.Fatalf("%s: %v", s.operation, err)
		}
	}
	want := []string{
		fmt.Sprintf(`POST %s/variables {"key":"NEW_KEY","projectId":%q,"value":"x"}`, apiPath, ownProject),
		fmt.Sprintf(`POST %s/variables {"key":"NEW_GLOBAL","value":"y"}`, apiPath),
		fmt.Sprintf(`PUT %s/variables/%s {"key":"OWN_KEY","projectId":%q,"value":"z"}`, apiPath, ownVariable, ownProject),
		fmt.Sprintf(`PUT %s/variables/%s {"key":"GLOBAL_KEY","projectId":null,"value":"g"}`, apiPath, globalVariable),
		fmt.Sprintf(`DELETE %s/variables/%s `, apiPath, ownVariable),
		fmt.Sprintf(`DELETE %s/variables/%s `, apiPath, globalVariable),
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q, want %q", mutations, want)
	}
}

func TestVariableDeleteIsOnlyOfferedByAToolsList(t *testing.T) {
	if !variablesDelete.RequiresToolAllowList || variablesDelete.Risk.Effect != "delete" ||
		variablesDelete.Risk.Confirmation != "required" || variablesCreate.RequiresToolAllowList ||
		variablesUpdate.RequiresToolAllowList || variablesList.Risk.Confirmation != "none" {
		t.Fatalf("descriptor = %+v", variablesDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		got[profile.ID] = profile.Tools
		for _, id := range profile.Tools {
			if id == variablesDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.variables.") && !strings.HasPrefix(profile.ID, "variables") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	if len(got["variables-read"]) != 1 || len(got["variables-manage"]) != 3 {
		t.Fatalf("profiles = %v", got)
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(variablesDelete.ID, "project", fmt.Sprintf(`{"variable_id":%q}`, ownVariable))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestVariableForbiddenIsReportedAsLicenseOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{variablesList.ID, `{}`},
		{variablesCreate.ID, fmt.Sprintf(`{"key":"K","value":"v","project_id":%q}`, ownProject)},
		{variablesUpdate.ID, fmt.Sprintf(`{"variable_id":%q,"key":"K","value":"v"}`, ownVariable)},
		{variablesDelete.ID, fmt.Sprintf(`{"variable_id":%q}`, ownVariable)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "feat:variables") ||
			!strings.Contains(err.Error(), "role") || strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s: err = %v", tt.operation, err)
		}
	}
}

func TestVariableChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"create 5xx", variablesCreate.ID, fmt.Sprintf(`{"key":"K","value":"v","project_id":%q}`, ownProject),
			func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"update transport", variablesUpdate.ID, fmt.Sprintf(`{"variable_id":%q,"key":"K","value":"v"}`, ownVariable),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete 502", variablesDelete.ID, fmt.Sprintf(`{"variable_id":%q}`, ownVariable),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			var mutations []string
			base := variableServer(t, &mutations)
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
