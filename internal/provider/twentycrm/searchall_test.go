package twentycrm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	hitOne = "aaaaaaaa-1111-4111-8111-111111111111"
	hitTwo = "bbbbbbbb-2222-4222-8222-222222222222"
	hitBad = "cccccccc-3333-4333-8333-333333333333"
)

// graphqlCall is one request sent to the fake transport.
type graphqlCall struct {
	path      string
	method    string
	query     string
	variables map[string]any
}

// searchAllServe serves the schema and the given search answer and records every request.
func searchAllServe(t *testing.T, status int, answer string) *[]graphqlCall {
	t.Helper()
	calls := &[]graphqlCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := graphqlCall{path: request.URL.Path, method: request.Method}
		if request.URL.Path == schemaPath {
			*calls = append(*calls, call)
			return jsonResponse(http.StatusOK, recordsSchema), nil
		}
		body, _ := io.ReadAll(request.Body)
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("body = %s", body)
		}
		call.query, call.variables = payload.Query, payload.Variables
		*calls = append(*calls, call)
		return jsonResponse(status, answer), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func searchCalls(calls []graphqlCall) []graphqlCall {
	var result []graphqlCall
	for _, call := range calls {
		if call.path != schemaPath {
			result = append(result, call)
		}
	}
	return result
}

func runSearchAll(args string, targets ...string) (*SearchAllResult, error) {
	red := &redact.Redactor{}
	result, err := invokeRecordsSearchAll(context.Background(), targetConnection(targets...), resolver(red), red,
		json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return result.(*SearchAllResult), nil
}

const hitsAnswer = `{"data":{"search":{"edges":[
 {"node":{"recordId":"` + hitOne + `","objectNameSingular":"person","label":"Ada"}},
 {"node":{"recordId":"` + hitTwo + `","objectNameSingular":"company","label":"Acme"}}
],"pageInfo":{"hasNextPage":true,"endCursor":"c3VjaA=="}}}}`

func TestSearchAllSendsTheFixedDocumentAndExactVariables(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		targets []string
		want    map[string]any
	}{
		{"all reachable objects", `{"text":"Ada"}`, nil, map[string]any{"searchInput": "Ada", "limit": float64(25),
			"includedObjectNameSingulars": []any{"company", "note", "person", "rocket"}}},
		{"objects subset", `{"text":"Ada","limit":3,"objects":["rocket","person","workspaceMember"]}`, nil,
			map[string]any{"searchInput": "Ada", "limit": float64(3), "includedObjectNameSingulars": []any{"person", "rocket"}}},
		{"targets", `{"text":"Ada"}`, []string{"object/person", "object/company"}, map[string]any{
			"searchInput": "Ada", "limit": float64(25), "includedObjectNameSingulars": []any{"company", "person"}}},
		{"targets and objects", `{"text":"Ada","objects":["person","note"]}`, []string{"object/person", "object/company"},
			map[string]any{"searchInput": "Ada", "limit": float64(25), "includedObjectNameSingulars": []any{"person"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := searchAllServe(t, http.StatusOK, hitsAnswer)
			if _, err := runSearchAll(test.args, test.targets...); err != nil {
				t.Fatal(err)
			}
			sent := searchCalls(*calls)
			if len(sent) != 1 || sent[0].path != graphqlPath || sent[0].method != http.MethodPost ||
				sent[0].query != searchAllDocument.text || !reflect.DeepEqual(sent[0].variables, test.want) {
				t.Fatalf("requests = %+v, want variables %v", sent, test.want)
			}
		})
	}
	t.Run("targets without objects read no catalog", func(t *testing.T) {
		calls := searchAllServe(t, http.StatusOK, hitsAnswer)
		if _, err := runSearchAll(`{"text":"Ada"}`, "object/person"); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 {
			t.Fatalf("requests = %+v", *calls)
		}
	})
}

func TestSearchAllKeepsTheTextOutOfTheDocument(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, hitsAnswer)
	if _, err := runSearchAll(`{"text":"Acme & Co's"}`, "object/person"); err != nil {
		t.Fatal(err)
	}
	sent := searchCalls(*calls)[0]
	if sent.query != searchAllDocument.text || strings.Contains(sent.query, "Acme") ||
		sent.variables["searchInput"] != "Acme & Co's" {
		t.Fatalf("request = %+v", sent)
	}
}

func TestSearchAllReportsOnlyReachableHitsAndCountsTheOthers(t *testing.T) {
	answer := `{"data":{"search":{"edges":[
 {"node":{"recordId":"` + hitOne + `","objectNameSingular":"person","label":"Ada"}},
 {"node":{"recordId":"` + hitTwo + `","objectNameSingular":"company","label":"Acme"}},
 {"node":{"recordId":"` + hitBad + `","objectNameSingular":"workspaceMember","label":"secret-member"}}
],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	searchAllServe(t, http.StatusOK, answer)
	result, err := runSearchAll(`{"text":"a"}`, "object/person")
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchHit{{Object: "person", ID: hitOne, Label: "Ada"}}
	if !reflect.DeepEqual(result.Results, want) || result.Omitted != 2 || result.HasMore || result.NextCursor != "" {
		t.Fatalf("result = %+v", result)
	}
	out, _ := json.Marshal(result)
	for _, leaked := range []string{"secret-member", "Acme", "imageUrl", "tsRank"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("output leaked %s: %s", leaked, out)
		}
	}
}

func TestSearchAllGraphQLErrorsAreAClassWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-9f3"
	cases := map[string]provider.Class{
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"FORBIDDEN"}}]}`:                   provider.ClassPermission,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"UNAUTHENTICATED"}}]}`:             provider.ClassAuth,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"BAD_USER_INPUT"}}]}`:              provider.ClassProviderError,
		`{"data":{"search":{"edges":[],"pageInfo":{"hasNextPage":false}}},"errors":[{"message":"` + canary + `"}]}`: provider.ClassProviderError,
	}
	for answer, class := range cases {
		searchAllServe(t, http.StatusOK, answer)
		_, err := runSearchAll(`{"text":"a"}`, "object/person")
		if classOf(err) != class || strings.Contains(err.Error(), canary) {
			t.Errorf("%s = %v, want class %s", answer, err, class)
		}
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		searchAllServe(t, status, `{"message":"`+canary+`"}`)
		if _, err := runSearchAll(`{"text":"a"}`, "object/person"); err == nil || strings.Contains(err.Error(), canary) {
			t.Errorf("status %d = %v", status, err)
		}
	}
	searchAllServe(t, http.StatusOK, `{"data":{}}`)
	if _, err := runSearchAll(`{"text":"a"}`, "object/person"); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("empty data = %v", err)
	}
}

func TestSearchAllRefusesAnEmptyObjectSetBeforeAnyIO(t *testing.T) {
	refuse(t)
	for _, test := range []struct {
		args    string
		targets []string
	}{
		{`{"text":"a","objects":["note"]}`, []string{"object/person"}},
		{`{"text":"a","objects":["workspaceMember"]}`, nil},
		{`{"text":"a","objects":["message","noteTarget"]}`, []string{"object/person"}},
	} {
		if _, err := runSearchAll(test.args, test.targets...); classOf(err) != "" || err == nil {
			t.Errorf("%s = %v", test.args, err)
		}
	}
}

func TestSearchAllRefusesObjectsOutsideTheWorkspaceBeforeTheSearchRequest(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, hitsAnswer)
	if _, err := runSearchAll(`{"text":"a","objects":["unknownThing"]}`); err == nil {
		t.Fatal("expected a refusal")
	}
	if len(searchCalls(*calls)) != 0 {
		t.Fatalf("a search was sent: %+v", *calls)
	}
}

func TestSearchAllRefusesAnInvalidTextBeforeAnyIO(t *testing.T) {
	refuse(t)
	for _, text := range []string{"", "   ", `a"b`, "a,b", "a[b", `a\b`, "a:b", "a%b", strings.Repeat("x", 65), "a\nb"} {
		raw, _ := json.Marshal(map[string]string{"text": text})
		if _, err := runSearchAll(string(raw), "object/person"); err == nil {
			t.Errorf("text %q was accepted", text)
		}
	}
	if _, err := runSearchAll(`{"text":"a","limit":101}`, "object/person"); err == nil {
		t.Error("limit 101 was accepted")
	}
}

func TestSearchAllBindsTheCursor(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, hitsAnswer)
	first, err := runSearchAll(`{"text":"Ada"}`, "object/person")
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" || strings.Contains(first.NextCursor, "c3VjaA") {
		t.Fatalf("cursor = %q", first.NextCursor)
	}
	next := `{"text":"Ada","cursor":"` + first.NextCursor + `"}`
	if _, err := runSearchAll(next, "object/person"); err != nil {
		t.Fatal(err)
	}
	if sent := searchCalls(*calls); len(sent) != 2 || sent[1].variables["after"] != "c3VjaA==" ||
		sent[0].variables["after"] != nil {
		t.Fatalf("requests = %+v", sent)
	}
	refuse(t)
	for name, test := range map[string]struct {
		args    string
		targets []string
	}{
		"other text":    {`{"text":"Bob","cursor":"` + first.NextCursor + `"}`, []string{"object/person"}},
		"other objects": {`{"text":"Ada","objects":["person"],"cursor":"` + first.NextCursor + `"}`, []string{"object/person"}},
		"other targets": {next, []string{"object/person", "object/company"}},
		"altered":       {`{"text":"Ada","cursor":"` + first.NextCursor[:len(first.NextCursor)-2] + `AA"}`, []string{"object/person"}},
	} {
		if _, err := runSearchAll(test.args, test.targets...); err == nil {
			t.Errorf("%s: cursor was accepted", name)
		}
	}
}

func TestSearchAllCapsTheLabel(t *testing.T) {
	long := strings.Repeat("ä", 300)
	searchAllServe(t, http.StatusOK, `{"data":{"search":{"edges":[{"node":{"recordId":"`+hitOne+
		`","objectNameSingular":"person","label":"`+long+`"}}],"pageInfo":{"hasNextPage":false}}}}`)
	result, err := runSearchAll(`{"text":"a"}`, "object/person")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Results[0].Label; got != strings.Repeat("ä", searchAllLabelMax) {
		t.Fatalf("label has %d runes", len([]rune(got)))
	}
}

func TestSearchAllRefusesMalformedAnswers(t *testing.T) {
	for _, answer := range []string{
		`{"data":{"search":{"edges":[{"node":{"recordId":"nope","objectNameSingular":"person","label":"x"}}],"pageInfo":{}}}}`,
		`{"data":{"search":{"edges":[],"pageInfo":{"hasNextPage":true,"endCursor":"bad cursor!"}}}}`,
		`{"data":{"search":{"edges":[],"pageInfo":{"hasNextPage":true}}}}`,
		`{"data":{"search":null}}`,
	} {
		searchAllServe(t, http.StatusOK, answer)
		if _, err := runSearchAll(`{"text":"a"}`, "object/person"); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s = %v", answer, err)
		}
	}
}

func TestSearchAllResolvesTheKeyOnlyAfterTheLocalChecks(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	if _, err := invokeRecordsSearchAll(context.Background(), targetConnection("object/person"), nil, red,
		json.RawMessage(`{"text":"a","objects":["note"]}`)); classOf(err) != "" {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchAllReturnsHitDetailsAndDropsSignedAddresses(t *testing.T) {
	const canary = "sig-canary-123"
	node := func(object, extra string) string {
		return `{"node":{"recordId":"` + hitOne + `","objectNameSingular":"` + object + `","label":"Ada"` + extra + `}}`
	}
	answer := `{"data":{"search":{"edges":[` +
		node("person", `,"objectLabelSingular":"Pe\u0007rson","imageUrl":"https://crm.example/logo.png","tsRank":0.123456789012345678,"tsRankCD":9007199254740993`) + `,` +
		node("person", `,"imageUrl":"https://crm.example/file/x?token=`+canary+`","tsRank":"x"`) + `,` +
		node("person", `,"imageUrl":"https://crm.example/a.png#frag","tsRank":null`) + `,` +
		node("person", `,"imageUrl":"ftp://crm.example/a.png"`) + `,` +
		node("person", `,"imageUrl":"https://u@localhost/a.png"`) + `,` +
		node("person", ``) +
		`],"pageInfo":{"hasNextPage":false}}}}`
	searchAllServe(t, http.StatusOK, answer)
	result, err := runSearchAll(`{"text":"Ada"}`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("token in output: %s", raw)
	}
	first := result.Results[0]
	if first.ObjectLabel != "Person" || first.ImageURL != "https://crm.example/logo.png" ||
		first.TSRank != "0.123456789012345678" || first.TSRankCD != "9007199254740993" {
		t.Errorf("first = %+v", first)
	}
	for _, hit := range result.Results[1:] {
		if hit.ImageURL != "" || hit.TSRank != "" || hit.TSRankCD != "" || hit.ObjectLabel != "" {
			t.Errorf("hit = %+v", hit)
		}
	}
}

const (
	filterID1 = "11111111-1111-4111-8111-111111111111"
	filterID2 = "22222222-2222-4222-8222-222222222222"
)

func TestSearchAllBuildsTheFilterVariable(t *testing.T) {
	const stamp = "2026-03-01T10:00:00Z"
	const wantStamp = "2026-03-01T10:00:00Z"
	cases := []struct {
		name, conditions, extra string
		want                    any
	}{
		{"id eq", `{"field":"id","operator":"eq","value":"` + filterID1 + `"}`, ``,
			map[string]any{"and": []any{map[string]any{"id": map[string]any{"eq": filterID1}}}}},
		{"id gt", `{"field":"id","operator":"gt","value":"` + filterID1 + `"}`, ``,
			map[string]any{"and": []any{map[string]any{"id": map[string]any{"gt": filterID1}}}}},
		{"id in", `{"field":"id","operator":"in","value":["` + filterID2 + `","` + filterID1 + `"]}`, ``,
			map[string]any{"and": []any{map[string]any{"id": map[string]any{"in": []any{filterID1, filterID2}}}}}},
		{"id not_in", `{"field":"id","operator":"not_in","value":["` + filterID1 + `"]}`, ``,
			map[string]any{"and": []any{map[string]any{"not": map[string]any{"id": map[string]any{"in": []any{filterID1}}}}}}},
		{"id is", `{"field":"id","operator":"is","value":"NOT_NULL"}`, ``,
			map[string]any{"and": []any{map[string]any{"id": map[string]any{"is": "NOT_NULL"}}}}},
		{"createdAt lte", `{"field":"createdAt","operator":"lte","value":"` + stamp + `"}`, ``,
			map[string]any{"and": []any{map[string]any{"createdAt": map[string]any{"lte": wantStamp}}}}},
		{"updatedAt neq", `{"field":"updatedAt","operator":"neq","value":"` + stamp + `"}`, ``,
			map[string]any{"and": []any{map[string]any{"updatedAt": map[string]any{"neq": wantStamp}}}}},
		{"updatedAt not_in", `{"field":"updatedAt","operator":"not_in","value":["` + stamp + `"]}`, ``,
			map[string]any{"and": []any{map[string]any{"not": map[string]any{"updatedAt": map[string]any{"in": []any{wantStamp}}}}}}},
		{"updatedAt is", `{"field":"updatedAt","operator":"is","value":"NULL"}`, ``,
			map[string]any{"and": []any{map[string]any{"updatedAt": map[string]any{"is": "NULL"}}}}},
		{"createdAt gte any", `{"field":"createdAt","operator":"gte","value":"` + stamp + `"},` +
			`{"field":"id","operator":"eq","value":"` + filterID1 + `"}`, `,"match":"any"`,
			map[string]any{"or": []any{map[string]any{"createdAt": map[string]any{"gte": wantStamp}},
				map[string]any{"id": map[string]any{"eq": filterID1}}}}},
		{"all explicit", `{"field":"createdAt","operator":"gt","value":"` + stamp + `"},` +
			`{"field":"id","operator":"neq","value":"` + filterID1 + `"}`, `,"match":"all"`,
			map[string]any{"and": []any{map[string]any{"createdAt": map[string]any{"gt": wantStamp}},
				map[string]any{"id": map[string]any{"neq": filterID1}}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := searchAllServe(t, http.StatusOK, hitsAnswer)
			if _, err := runSearchAll(`{"text":"Ada","conditions":[`+test.conditions+`]`+test.extra+`}`, "object/person"); err != nil {
				t.Fatal(err)
			}
			sent := searchCalls(*calls)
			if len(sent) != 1 || sent[0].query != searchAllDocument.text ||
				!reflect.DeepEqual(sent[0].variables["filter"], test.want) || sent[0].variables["excludedObjectNameSingulars"] != nil {
				t.Fatalf("requests = %+v, want filter %v", sent, test.want)
			}
		})
	}
}

func TestSearchAllNeverSendsFilterVariablesWithoutFilter(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, hitsAnswer)
	if _, err := runSearchAll(`{"text":"Ada"}`, "object/person"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"filter", "excludedObjectNameSingulars"} {
		if _, found := searchCalls(*calls)[0].variables[name]; found {
			t.Errorf("%s was sent", name)
		}
	}
}

func TestSearchAllExcludesObjects(t *testing.T) {
	cases := []struct {
		name, args string
		targets    []string
		included   []any
		excluded   any
	}{
		{"bound", `{"text":"a","exclude_objects":["company","workspaceMember","unknownThing","rocket"]}`,
			[]string{"object/person", "object/company"}, []any{"person"}, []any{"company"}},
		{"catalog", `{"text":"a","exclude_objects":["note","unknownThing"]}`, nil,
			[]any{"company", "person", "rocket"}, []any{"note"}},
		{"objects and exclude", `{"text":"a","objects":["person","note"],"exclude_objects":["note","company"]}`, nil,
			[]any{"person"}, []any{"note"}},
		{"outside the searched set", `{"text":"a","objects":["person"],"exclude_objects":["note"]}`, nil,
			[]any{"person"}, nil},
		{"only ignored names", `{"text":"a","exclude_objects":["workspaceMember"]}`, []string{"object/person"},
			[]any{"person"}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := searchAllServe(t, http.StatusOK, hitsAnswer)
			if _, err := runSearchAll(test.args, test.targets...); err != nil {
				t.Fatal(err)
			}
			sent := searchCalls(*calls)
			if len(sent) != 1 || sent[0].query != searchAllDocument.text ||
				!reflect.DeepEqual(sent[0].variables["includedObjectNameSingulars"], test.included) ||
				!reflect.DeepEqual(sent[0].variables["excludedObjectNameSingulars"], test.excluded) {
				t.Fatalf("variables = %+v, want included %v excluded %v", sent, test.included, test.excluded)
			}
		})
	}
}

func TestSearchAllRefusesAnEmptyRemainderOfExcludedObjects(t *testing.T) {
	for name, test := range map[string]struct {
		args    string
		targets []string
		schema  bool
	}{
		"bound":   {`{"text":"a","exclude_objects":["person"]}`, []string{"object/person"}, false},
		"objects": {`{"text":"a","objects":["person"],"exclude_objects":["person"]}`, nil, true},
		"catalog": {`{"text":"a","exclude_objects":["company","note","person","rocket"]}`, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			calls := searchAllServe(t, http.StatusOK, hitsAnswer)
			if _, err := runSearchAll(test.args, test.targets...); err == nil || classOf(err) != "" {
				t.Fatalf("err = %v", err)
			}
			if len(searchCalls(*calls)) != 0 {
				t.Fatalf("a search was sent: %+v", *calls)
			}
		})
	}
}

func TestSearchAllRefusesInvalidFiltersBeforeAnyIO(t *testing.T) {
	refuse(t)
	many := func(n int) string {
		values := make([]string, n)
		for i := range values {
			values[i] = `"` + filterID1[:35] + string(rune('a'+i%6)) + `"`
		}
		return "[" + strings.Join(values, ",") + "]"
	}
	cond := func(field, operator, value string) string {
		return `{"field":"` + field + `","operator":"` + operator + `","value":` + value + `}`
	}
	six := strings.TrimSuffix(strings.Repeat(cond("id", "eq", `"`+filterID1+`"`)+",", 6), ",")
	canary := "secretField"
	for name, args := range map[string]string{
		"deletedAt":         `"conditions":[` + cond("deletedAt", "is", `"NULL"`) + `]`,
		"unknown field":     `"conditions":[` + cond(canary, "eq", `"`+filterID1+`"`) + `]`,
		"unknown operator":  `"conditions":[` + cond("id", "like", `"`+filterID1+`"`) + `]`,
		"bad uuid":          `"conditions":[` + cond("id", "eq", `"not-`+canary+`"`) + `]`,
		"uuid not text":     `"conditions":[` + cond("id", "eq", `5`) + `]`,
		"date only":         `"conditions":[` + cond("createdAt", "eq", `"2026-03-01"`) + `]`,
		"date with offset":  `"conditions":[` + cond("createdAt", "eq", `"2026-03-01T10:00:00+02:00"`) + `]`,
		"uuid as date":      `"conditions":[` + cond("updatedAt", "eq", `"`+filterID1+`"`) + `]`,
		"date as uuid":      `"conditions":[` + cond("id", "eq", `"2026-03-01T10:00:00Z"`) + `]`,
		"in scalar":         `"conditions":[` + cond("id", "in", `"`+filterID1+`"`) + `]`,
		"eq list":           `"conditions":[` + cond("id", "eq", `["`+filterID1+`"]`) + `]`,
		"not_in scalar":     `"conditions":[` + cond("id", "not_in", `"`+filterID1+`"`) + `]`,
		"is other":          `"conditions":[` + cond("id", "is", `"`+filterID1+`"`) + `]`,
		"is list":           `"conditions":[` + cond("id", "is", `["NULL"]`) + `]`,
		"empty list":        `"conditions":[` + cond("id", "in", `[]`) + `]`,
		"list of 51":        `"conditions":[` + cond("id", "in", many(51)) + `]`,
		"mixed list":        `"conditions":[` + cond("id", "in", `["`+filterID1+`",3]`) + `]`,
		"null value":        `"conditions":[` + cond("id", "eq", `null`) + `]`,
		"no conditions":     `"conditions":[]`,
		"six conditions":    `"conditions":[` + six + `]`,
		"match alone":       `"match":"any"`,
		"bad match":         `"match":"some","conditions":[` + cond("id", "eq", `"`+filterID1+`"`) + `]`,
		"empty exclude":     `"exclude_objects":[]`,
		"51 excludes":       `"exclude_objects":[` + strings.TrimSuffix(strings.Repeat(`"note",`, 51), ",") + `]`,
		"malformed exclude": `"exclude_objects":["Bad Name"]`,
	} {
		_, err := runSearchAll(`{"text":"a",`+args+`}`, "object/person")
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		for _, leak := range []string{canary, filterID1, "deletedAt", "2026-03-01", "Bad Name"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: message %q names %q", name, err, leak)
			}
		}
	}
}

func TestSearchAllBindsTheCursorToTheFilter(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, hitsAnswer)
	base := `"conditions":[{"field":"id","operator":"in","value":["` + filterID1 + `","` + filterID2 + `"]},` +
		`{"field":"createdAt","operator":"gt","value":"2026-03-01T10:00:00Z"}],"exclude_objects":["company","note"]`
	first, err := runSearchAll(`{"text":"Ada",`+base+`}`, "object/person", "object/company", "object/note")
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	targets := []string{"object/person", "object/company", "object/note"}
	reordered := `{"text":"Ada","exclude_objects":["note","company","note"],"conditions":[` +
		`{"field":"createdAt","operator":"gt","value":"2026-03-01T10:00:00Z"},` +
		`{"field":"id","operator":"in","value":["` + filterID2 + `","` + filterID1 + `","` + filterID2 + `"]},` +
		`{"field":"createdAt","operator":"gt","value":"2026-03-01T10:00:00Z"}],"cursor":"` + first.NextCursor + `"}`
	if _, err := runSearchAll(reordered, targets...); err != nil {
		t.Fatalf("same filter reordered: %v", err)
	}
	if sent := searchCalls(*calls); len(sent) != 2 || sent[1].variables["after"] != "c3VjaA==" {
		t.Fatalf("requests = %+v", sent)
	}
	refuse(t)
	cursor := `,"cursor":"` + first.NextCursor + `"`
	for name, args := range map[string]string{
		"no filter":      `{"text":"Ada","exclude_objects":["company","note"]` + cursor + `}`,
		"other value":    `{"text":"Ada","conditions":[{"field":"id","operator":"in","value":["` + filterID1 + `"]},{"field":"createdAt","operator":"gt","value":"2026-03-01T10:00:00Z"}],"exclude_objects":["company","note"]` + cursor + `}`,
		"other match":    `{"text":"Ada","match":"any",` + base + cursor + `}`,
		"other excluded": `{"text":"Ada","conditions":[{"field":"id","operator":"in","value":["` + filterID1 + `","` + filterID2 + `"]},{"field":"createdAt","operator":"gt","value":"2026-03-01T10:00:00Z"}],"exclude_objects":["company"]` + cursor + `}`,
	} {
		if _, err := runSearchAll(args, targets...); err == nil {
			t.Errorf("%s: cursor was accepted", name)
		}
	}
}
