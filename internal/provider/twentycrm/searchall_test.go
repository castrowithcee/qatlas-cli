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
