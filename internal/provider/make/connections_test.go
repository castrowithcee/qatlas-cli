package makeapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	connectionID     int64 = 55
	canaryConnSecret       = "conn-secret-canary-4b7d"
)

func connectionJSONOf(id, teamID int64, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"accountName":"google","accountType":"oauth","packageName":"google",`+
		`"expire":"2030-01-01T00:00:00Z","scoped":true,"teamId":%d,"organizationId":%d,"editable":true,`+
		`"accountLabel":"label","metadata":{"value":%q,"type":"email"},"uid":%q,"token":%q,`+
		`"scopes":[{"id":%q}],"data":{"clientSecret":%q}}`,
		id, name, teamID, ownOrg, canaryConnSecret, canaryConnSecret, canaryConnSecret, canaryConnSecret,
		canaryConnSecret)
}

func connectionEnv(t *testing.T, calls *[]call, teamID int64) *environment {
	return newEnvironment(t, calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/connections":
			return jsonResponse(200, `{"connections":[`+connectionJSONOf(connectionID, ownTeam, "Own")+`,`+
				connectionJSONOf(66, foreignTeam, "Foreign")+`]}`), nil
		case strings.HasSuffix(r.URL.Path, "/editable-data-schema"):
			return jsonResponse(200, `{"editableParameters":["clientId",{"name":"x","default":"`+canaryConnSecret+
				`"},"region"]}`), nil
		case strings.HasSuffix(r.URL.Path, "/test"):
			return jsonResponse(200, `{"verified":true,"message":"`+canaryConnSecret+`"}`), nil
		default:
			return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, teamID, "Conn")+`}`), nil
		}
	})
}

func TestConnectionsRequestsUseDocumentedPathsAndQueries(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	id := strconv.FormatInt(connectionID, 10)
	cases := []struct{ op, args, method, path string }{
		{connectionsList.ID, `{"type":"google"}`, http.MethodGet, "/connections"},
		{connectionsGet.ID, `{"connection_id":` + id + `}`, http.MethodGet, "/connections/" + id},
		{connectionsEditableSchema.ID, `{"connection_id":` + id + `}`, http.MethodGet,
			"/connections/" + id + "/editable-data-schema"},
	}
	for _, c := range cases {
		calls = nil
		if _, err := env.invoke(c.op, "open", c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		last := calls[len(calls)-1]
		if last.method != c.method || last.path != apiPath+c.path {
			t.Fatalf("%s last call = %+v", c.op, last)
		}
	}
	calls = nil
	_, _ = env.invoke(connectionsList.ID, "open", `{"type":"google"}`)
	q := calls[0].query
	if q.Get("teamId") != itoa64(ownTeam) || q.Get("type[]") != "google" || len(q["cols[]"]) != len(connectionCols) {
		t.Fatalf("list query = %v", q)
	}
	calls = nil
	if _, err := env.confirmed(connectionsTest.ID, "open", `{"connection_id":`+id+`}`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost ||
		calls[1].path != apiPath+"/connections/"+id+"/test" {
		t.Fatalf("test calls = %+v", calls)
	}
}

func TestConnectionsOnlyAllowListedFieldsLeave(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	id := strconv.FormatInt(connectionID, 10)
	for _, c := range []struct {
		op, args string
		confirm  bool
	}{
		{connectionsList.ID, `{}`, false}, {connectionsGet.ID, `{"connection_id":` + id + `}`, false},
		{connectionsEditableSchema.ID, `{"connection_id":` + id + `}`, false},
		{connectionsTest.ID, `{"connection_id":` + id + `}`, true},
	} {
		var result string
		var err error
		if c.confirm {
			result, err = env.confirmed(c.op, "open", c.args)
		} else {
			result, err = env.invoke(c.op, "open", c.args)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		for _, leaked := range []string{canaryConnSecret, "metadata", "scopes", "token", "clientSecret", "accountLabel"} {
			if strings.Contains(result, leaked) {
				t.Fatalf("%s leaked %q: %s", c.op, leaked, result)
			}
		}
	}
	result, _ := env.invoke(connectionsEditableSchema.ID, "open", `{"connection_id":`+id+`}`)
	if !strings.Contains(result, `"clientId"`) || !strings.Contains(result, `"count":2`) {
		t.Fatalf("schema = %s", result)
	}
	result, _ = env.invoke(connectionsGet.ID, "open", `{"connection_id":`+id+`}`)
	for _, field := range []string{"id", "name", "account_name", "account_type", "package_name", "expire", "scoped",
		"team_id", "organization_id", "editable"} {
		if !strings.Contains(result, `"`+field+`"`) {
			t.Fatalf("get lacks %s: %s", field, result)
		}
	}
}

func TestConnectionsRebindForeignTeamWithoutFollowUp(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, foreignTeam)
	id := strconv.FormatInt(connectionID, 10)
	for _, op := range []string{connectionsGet.ID, connectionsEditableSchema.ID, connectionsTest.ID} {
		calls = nil
		_, err := env.confirmed(op, "open", `{"connection_id":`+id+`}`)
		if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
			t.Fatalf("%s err = %v, calls = %+v, want refusal after the detail read only", op, err, calls)
		}
		if strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
			t.Fatalf("%s error named the foreign team: %v", op, err)
		}
	}
	result, err := env.invoke(connectionsList.ID, "open", `{}`)
	if err != nil || strings.Contains(result, "Foreign") || !strings.Contains(result, `"count":1`) {
		t.Fatalf("list = %s, %v", result, err)
	}
}

func TestConnectionsRefusedWithScenarioAllowListAndInvalidIDs(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	for _, op := range []string{connectionsList.ID, connectionsGet.ID, connectionsEditableSchema.ID} {
		if _, err := env.invoke(op, "scenario", `{"connection_id":5}`); err == nil {
			t.Fatalf("%s accepted on a scenario allow-list connection", op)
		}
	}
	if _, err := env.confirmed(connectionsTest.ID, "scenario", `{"connection_id":5}`); !isInvalidRequest(err) {
		t.Fatalf("test err = %v", err)
	}
	for _, args := range []string{`{"connection_id":0}`, `{"connection_id":"7"}`, `{"connection_id":-1}`, `{}`} {
		if _, err := env.confirmed(connectionsGet.ID, "open", args); err == nil {
			t.Fatalf("%s accepted", args)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("reads = %d, calls = %d, want none", *env.reads, len(calls))
	}
}

func TestConnectionsTestNeedsConfirmationAndIsNeverRetried(t *testing.T) {
	var calls []call
	env := connectionEnv(t, &calls, ownTeam)
	if _, err := env.invoke(connectionsTest.ID, "open", `{"connection_id":55}`); !isConfirmationRequired(err) ||
		len(calls) != 0 {
		t.Fatalf("err = %v, calls = %d, want confirmation before any request", err, len(calls))
	}
	if connectionsTest.Risk.Confirmation != "required" || connectionsTest.Risk.Effect != "execute" ||
		!connectionsTest.Risk.OpenWorld || connectionsTest.Risk.DataSensitivity == "" ||
		connectionsTest.Risk.Idempotency == "" {
		t.Fatalf("risk = %+v", connectionsTest.Risk)
	}
	failures := map[string]func(*http.Request) (*http.Response, error){
		"5xx":     func(*http.Request) (*http.Response, error) { return jsonResponse(500, `{}`), nil },
		"aborted": func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbled": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"empty":   func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/test") {
					return failure(r)
				}
				return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "Conn")+`}`), nil
			})
			_, err := env.confirmed(connectionsTest.ID, "open", `{"connection_id":55}`)
			posts := 0
			for _, c := range calls {
				if c.method == http.MethodPost {
					posts++
				}
			}
			if err == nil || posts != 1 || !strings.Contains(err.Error(), "may have run") {
				t.Fatalf("err = %v, posts = %d, want one request and an uncertain note", err, posts)
			}
		})
	}
}

func TestConnectionsForbiddenNamesScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/connections") {
			return jsonResponse(403, `{}`), nil
		}
		return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "Conn")+`}`), nil
	})
	check := func(err error, scope string) {
		t.Helper()
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
			!strings.Contains(perr.Message, scope) || strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("err = %v, want the %s hint", err, scope)
		}
	}
	_, err := env.invoke(connectionsList.ID, "open", `{}`)
	check(err, "connections:read")
	_, err = env.confirmed(connectionsTest.ID, "open", `{"connection_id":55}`)
	check(err, "connections:write")
}

func TestConnectionsBoundStrings(t *testing.T) {
	var calls []call
	long := strings.Repeat("a", 5000)
	params := make([]string, 100)
	for i := range params {
		params[i] = strconv.Quote(long)
	}
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/editable-data-schema") {
			return jsonResponse(200, `{"editableParameters":[`+strings.Join(params, ",")+`]}`), nil
		}
		return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, long)+`}`), nil
	})
	result, err := env.invoke(connectionsGet.ID, "open", `{"connection_id":55}`)
	if err != nil || len(result) > 1500 {
		t.Fatalf("get = %d bytes, %v", len(result), err)
	}
	result, err = env.invoke(connectionsEditableSchema.ID, "open", `{"connection_id":55}`)
	if err != nil || len(result) > maxEditableParameters*(maxEditableParameterLength+4)+200 ||
		!strings.Contains(result, `"count":64`) {
		t.Fatalf("schema = %d bytes, %v", len(result), err)
	}
}

func TestConnectionProfiles(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	found := map[string][]string{}
	for _, profile := range metadata.Profiles {
		found[profile.ID] = profile.Tools
	}
	if len(found["connections-read"]) != 3 || len(found["connections-test"]) != 1 ||
		found["connections-test"][0] != connectionsTest.ID {
		t.Fatalf("profiles = %+v", found)
	}
	for _, tool := range found["connections-read"] {
		if tool == connectionsTest.ID {
			t.Fatal("connections-read contains the test tool")
		}
	}
}
