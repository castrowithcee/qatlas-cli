package makeapi

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestConnectionsReadsReportEveryDocumentedField(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	id := strconv.FormatInt(connectionID, 10)
	for _, c := range []struct {
		tool, args string
		minimum    int
		scopes     bool
	}{
		{connectionsList.ID, `{}`, 14, false},
		{connectionsGet.ID, `{"connection_id":` + id + `}`, 18, true},
	} {
		calls = nil
		got, err := env.invoke(c.tool, "open", c.args)
		if err != nil {
			t.Fatal(err)
		}
		if seen := assertOutputDeclared(t, c.tool, []byte(got)); seen < c.minimum {
			t.Errorf("%s: checked %d members: %s", c.tool, seen, got)
		}
		for _, want := range []string{`"account_label":"label"`, `"metadata_value":"acct@example.test"`,
			`"metadata_type":"email"`, `"theme":"#ff0000"`, `"upgradeable":false`, `"scopes_count":1`,
			`"uid":"uid-1"`, `"connected_system_id":"sys-1"`} {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %s: %s", c.tool, want, got)
			}
		}
		if hasScopes := strings.Contains(got, `"scopes":[{"account":"acct","id":"read","name":"Read"}]`); hasScopes != c.scopes {
			t.Errorf("%s scopes present = %v: %s", c.tool, hasScopes, got)
		}
		if strings.Contains(got, canaryConnSecret) {
			t.Errorf("%s leaked a secret: %s", c.tool, got)
		}
		cols := strings.Join(calls[len(calls)-1].query["cols[]"], ",")
		if strings.Contains(cols, "scopes,") || strings.HasSuffix(cols, ",scopes") != c.scopes {
			t.Errorf("%s cols = %s", c.tool, cols)
		}
	}
}

func TestConnectionsReadsWithoutNewFieldsStayUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		const conn = `{"id":55,"name":"C","accountName":"google","scoped":true,"teamId":1001,"organizationId":501,` +
			`"editable":false}`
		if r.URL.Path == apiPath+"/connections" {
			return jsonResponse(200, `{"connections":[`+conn+`]}`), nil
		}
		return jsonResponse(200, `{"connection":`+conn+`}`), nil
	})
	const want = `{"account_name":"google","editable":false,"id":55,"name":"C","organization_id":501,` +
		`"scoped":true,"team_id":1001}`
	list, _ := env.invoke(connectionsList.ID, "open", `{}`)
	get, _ := env.invoke(connectionsGet.ID, "open", `{"connection_id":55}`)
	if !strings.Contains(list, want) || get != want {
		t.Fatalf("list = %s, get = %s", list, get)
	}
}

func TestConnectionsWriteResultsKeepTheirSummary(t *testing.T) {
	h := newConnChangeEnv(t, ownTeam)
	result, err := h.confirmed(connectionsRename.ID, "open",
		`{"connection_id":`+strconv.FormatInt(connectionID, 10)+`,"name":"New"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, added := range []string{"account_label", "metadata", "theme", "scopes", "uid", "connected_system_id"} {
		if strings.Contains(result, added) {
			t.Fatalf("rename reports %s: %s", added, result)
		}
	}
	if cols := strings.Join(h.calls[len(h.calls)-1].query["cols[]"], ","); strings.Contains(cols, "metadata") {
		t.Fatalf("rename cols = %s", cols)
	}
}

func TestConnectionsListFiltersReachTheRequest(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	if _, err := env.invoke(connectionsList.ID, "open", `{"types":["google","slack","google"]}`); err != nil {
		t.Fatal(err)
	}
	if got := calls[0].query["type[]"]; len(got) != 2 || got[0] != "google" || got[1] != "slack" {
		t.Fatalf("type[] = %v", got)
	}
	calls = nil
	if _, err := env.invoke(connectionsList.ID, "open",
		`{"type":"google","scope_check":{"google":["a","b c"]}}`); err != nil {
		t.Fatal(err)
	}
	q := calls[0].query
	if got := q["google[]"]; len(got) != 2 || got[0] != "a" || got[1] != "b c" || q.Get("type[]") != "google" ||
		len(q["cols[]"]) != len(readConnectionCols) {
		t.Fatalf("query = %v", q)
	}
}

func TestConnectionsListRefusesBadFiltersBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	long := strings.Repeat("s", maxScopeLength+1)
	many := `"` + strings.Join(make([]string, maxConnectionTypes+2), `","`) + `"`
	for name, args := range map[string]string{
		"type with types":      `{"type":"a","types":["b"]}`,
		"empty types":          `{"types":[]}`,
		"too many types":       `{"types":[` + many + `]}`,
		"bad type":             `{"types":["a b"]}`,
		"unknown check type":   `{"type":"a","scope_check":{"b":["x"]}}`,
		"check without type":   `{"scope_check":{"a":["x"]}}`,
		"empty check":          `{"type":"a","scope_check":{}}`,
		"empty scopes":         `{"type":"a","scope_check":{"a":[]}}`,
		"empty scope":          `{"type":"a","scope_check":{"a":[""]}}`,
		"long scope":           `{"type":"a","scope_check":{"a":["` + long + `"]}}`,
		"control scope":        `{"type":"a","scope_check":{"a":["x\ny"]}}`,
		"colliding cols":       `{"type":"cols","scope_check":{"cols":["x"]}}`,
		"colliding type":       `{"types":["type"],"scope_check":{"type":["x"]}}`,
		"unknown argument":     `{"passthrough":"x"}`,
		"check value not list": `{"type":"a","scope_check":{"a":"x"}}`,
	} {
		if _, err := env.invoke(connectionsList.ID, "open", args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
	tooMany := make([]string, maxScopeCheckTypes+1)
	check := make([]string, len(tooMany))
	for i := range tooMany {
		tooMany[i] = `"t` + strconv.Itoa(i) + `"`
		check[i] = tooMany[i] + `:["x"]`
	}
	if _, err := env.invoke(connectionsList.ID, "open",
		`{"types":[`+strings.Join(tooMany, ",")+`],"scope_check":{`+strings.Join(check, ",")+`}}`); err == nil || len(calls) != 0 {
		t.Fatalf("too many check types: %v, calls = %d", err, len(calls))
	}
}

func TestConnectionsScopesAreBounded(t *testing.T) {
	var calls []call
	var scopes []string
	for i := 0; i < maxReadScopes+3; i++ {
		scopes = append(scopes, `{"id":"s`+strconv.Itoa(i)+`","name":"r\u0007x"}`)
	}
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"connection":{"id":55,"teamId":1001,"organizationId":501,"scopes":[`+
			strings.Join(scopes, ",")+`]}}`), nil
	})
	got, err := env.invoke(connectionsGet.ID, "open", `{"connection_id":55}`)
	if err != nil || !strings.Contains(got, `"scopes_truncated":true`) || strings.Count(got, `"id":"s`) != maxReadScopes ||
		!strings.Contains(got, `"name":"r x"`) || strings.Contains(got, `\u0007`) {
		t.Fatalf("len = %d, %v %s", len(got), err, got)
	}
	assertOutputDeclared(t, connectionsGet.ID, []byte(got))
}
