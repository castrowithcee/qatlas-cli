package twentycrm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const searchSchema = `{
  "paths":{
    "/people":{"post":{"operationId":"createOnePerson"}},
    "/companies":{"post":{"operationId":"createOneCompany"}},
    "/workspaceMembers":{"post":{"operationId":"createOneWorkspaceMember"}}
  },
  "components":{"schemas":{
    "Person":{"type":"object","properties":{"city":{"type":"string"}}},
    "PersonForResponse":{"type":"object","properties":{
      "id":{"type":"string","format":"uuid"},"city":{"type":"string"},"age":{"type":"integer"},"score":{"type":"number"},
      "active":{"type":"boolean"},
      "stage":{"type":"string","enum":["NEW","WON","LOST_X"]},
      "tags":{"type":"array","items":{"type":"string","enum":["A","B"]}},
      "oddStage":{"type":"string","enum":["has space","ok"]},
      "born":{"type":"string","format":"date"},"seen":{"type":"string","format":"date-time"},
      "emails":{"type":"object","properties":{"primaryEmail":{"type":"string"},"additionalEmails":{"type":"array","items":{"type":"string"}}}},
      "name":{"type":"object","properties":{"firstName":{"type":"string"},"lastName":{"type":"string"}}},
      "bodyV2":{"type":"object","properties":{"blocknote":{"type":"string"},"markdown":{"type":"string"}}},
      "actor":{"type":"object","properties":{"source":{"type":"string"},"workspaceMemberId":{"type":"string","format":"uuid"}}},
      "companyId":{"type":"string","format":"uuid"},"company":{"$ref":"#/components/schemas/CompanyForResponse"},
      "ownerId":{"type":"string","format":"uuid"},"owner":{"$ref":"#/components/schemas/WorkspaceMemberForResponse"},
      "orphanId":{"type":"string","format":"uuid"},
      "createdAt":{"type":"string","format":"date-time"},"deletedAt":{"type":"string","format":"date-time"}}},
    "Company":{"type":"object","properties":{"name":{"type":"string"}}},
    "CompanyForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}}},
    "WorkspaceMember":{"type":"object","properties":{"name":{"type":"object"}}}
  }}
}`

const emptyPeople = `{"data":{"people":[]},"pageInfo":{"hasNextPage":false}}`

const peoplePage = `{"data":{"people":[{"id":"` + personID + `","city":"Berlin"}]},` +
	`"pageInfo":{"hasNextPage":true,"endCursor":"cursorXYZ"}}`

func searchRequests(t *testing.T, data string) *[]*url.URL {
	t.Helper()
	requests := &[]*url.URL{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*requests = append(*requests, request.URL)
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, searchSchema), nil
		}
		return jsonResponse(http.StatusOK, data), nil
	})
	stubLimiter(t, cloudKey)
	return requests
}

func runTool(t *testing.T, handler capability.Handler, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	return out, nil
}

func search(t *testing.T, conditions string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	return runTool(t, invokeRecordsSearch, `{"object":"person","conditions":`+conditions+`}`, targets...)
}

func TestSearchBuildsTheFilterFromCheckedParts(t *testing.T) {
	const company = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	for name, tt := range map[string]struct{ conditions, want string }{
		"text selection number boolean": {
			`[{"field":"stage","operator":"in","value":["NEW","WON"]},{"field":"city","operator":"ilike","value":"Ber lin"},` +
				`{"field":"active","operator":"eq","value":true},{"field":"age","operator":"gte","value":18}]`,
			`active[eq]:true,age[gte]:18,city[ilike]:%Ber lin%,stage[in]:[NEW,WON]`,
		},
		"is subfield date date-time identifier": {
			`[{"field":"seen","operator":"gt","value":"2026-01-02T03:04:05Z"},{"field":"emails.primaryEmail","operator":"is","value":"NULL"},` +
				`{"field":"born","operator":"lt","value":"2000-01-31"},{"field":"companyId","operator":"eq","value":"` + company + `"}]`,
			`born[lt]:2000-01-31,companyId[eq]:` + company + `,emails.primaryEmail[is]:NULL,seen[gt]:2026-01-02T03:04:05Z`,
		},
		"single, startsWith, number list, decimal": {
			`[{"field":"name.firstName","operator":"startsWith","value":"Ad"}]`, `name.firstName[startsWith]:Ad`,
		},
		"number list and decimal": {
			`[{"field":"score","operator":"in","value":[1.5,-2]},{"field":"age","operator":"is","value":"NOT_NULL"}]`,
			`age[is]:NOT_NULL,score[in]:[1.5,-2]`,
		},
		"repeats and order do not matter": {
			`[{"field":"city","operator":"eq","value":"x"},{"field":"age","operator":"lt","value":3},{"field":"city","operator":"eq","value":"x"}]`,
			`age[lt]:3,city[eq]:x`,
		},
	} {
		requests := searchRequests(t, emptyPeople)
		if _, err := search(t, tt.conditions); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		query := dataRequests(*requests)[0].Query()
		if got := query.Get("filter"); got != tt.want || query.Get("depth") != "0" || query.Get("limit") != "25" {
			t.Errorf("%s: filter = %q, want %q (query %v)", name, got, tt.want, query)
		}
		if p := dataRequests(*requests)[0].Path; p != "/rest/people" {
			t.Errorf("%s: path = %s", name, p)
		}
	}
}

func TestSearchKeepsPageSortAndFieldSelection(t *testing.T) {
	requests := searchRequests(t, peoplePage)
	_, err := runTool(t, invokeRecordsSearch, `{"object":"person","limit":5,"order_by":"city","direction":"desc",`+
		`"fields":["city"],"conditions":[{"field":"age","operator":"eq","value":1}]}`)
	if err != nil {
		t.Fatal(err)
	}
	query := dataRequests(*requests)[0].Query()
	if query.Get("filter") != "age[eq]:1" || query.Get("order_by") != "city[DescNullsLast]" ||
		query.Get("limit") != "5" || query.Get("fields") != "city,createdAt" {
		t.Errorf("query = %v", query)
	}
}

func TestSearchRefusesGrammarCharactersBeforeAnyRequest(t *testing.T) {
	refuse(t)
	const canary = "ZzValueCanary"
	for _, bad := range []string{`\"`, `'`, `\\`, `[`, `]`, `:`, `,`, `)`, `(`, `%`} {
		for name, conditions := range map[string]string{
			"text":     `[{"field":"city","operator":"eq","value":"` + canary + bad + `x"}]`,
			"ilike":    `[{"field":"city","operator":"ilike","value":"` + bad + canary + `"}]`,
			"enum":     `[{"field":"stage","operator":"eq","value":"NEW` + bad + canary + `"}]`,
			"in":       `[{"field":"stage","operator":"in","value":["NEW","` + canary + bad + `"]}]`,
			"in text":  `[{"field":"city","operator":"in","value":["a","b` + bad + canary + `"]}]`,
			"uuid":     `[{"field":"companyId","operator":"eq","value":"` + canary + bad + `"}]`,
			"is":       `[{"field":"city","operator":"is","value":"NULL` + bad + canary + `"}]`,
			"datetime": `[{"field":"seen","operator":"gt","value":"2026-01-02T03:04:05Z` + bad + canary + `"}]`,
		} {
			_, err := runTool(t, invokeRecordsSearch, `{"object":"person","conditions":`+conditions+`}`)
			if !asInvalidOK(err) {
				t.Errorf("%s %q: err = %v, want invalid-request", name, bad, err)
				continue
			}
			if strings.Contains(err.Error(), canary) {
				t.Errorf("%s %q: the error shows the value: %v", name, bad, err)
			}
		}
	}
}

func TestSearchRefusesMalformedConditionsBeforeSecretAccess(t *testing.T) {
	refuse(t)
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf(`"v%d"`, i)
	}
	six := strings.Repeat(`{"field":"age","operator":"eq","value":1},`, 5) + `{"field":"age","operator":"eq","value":2}`
	for name, conditions := range map[string]string{
		"none":                `[]`,
		"six":                 `[` + six + `]`,
		"21 values":           `[{"field":"city","operator":"in","value":[` + strings.Join(many, ",") + `]}]`,
		"empty list":          `[{"field":"city","operator":"in","value":[]}]`,
		"list without in":     `[{"field":"city","operator":"eq","value":["a"]}]`,
		"scalar for in":       `[{"field":"city","operator":"in","value":"a"}]`,
		"mixed list":          `[{"field":"city","operator":"in","value":["a",1]}]`,
		"unknown operator":    `[{"field":"city","operator":"like","value":"a"}]`,
		"containsAny":         `[{"field":"city","operator":"containsAny","value":"a"}]`,
		"is value":            `[{"field":"city","operator":"is","value":"EMPTY"}]`,
		"exponent":            `[{"field":"age","operator":"eq","value":1e3}]`,
		"empty text":          `[{"field":"city","operator":"eq","value":""}]`,
		"long text":           `[{"field":"city","operator":"eq","value":"` + strings.Repeat("a", 65) + `"}]`,
		"object value":        `[{"field":"city","operator":"eq","value":{"a":1}}]`,
		"null value":          `[{"field":"city","operator":"eq","value":null}]`,
		"field with comma":    `[{"field":"city,age","operator":"eq","value":"a"}]`,
		"field with bracket":  `[{"field":"city[eq]","operator":"eq","value":"a"}]`,
		"field with two dots": `[{"field":"a.b.c","operator":"eq","value":"a"}]`,
	} {
		_, err := runTool(t, invokeRecordsSearch, `{"object":"person","conditions":`+conditions+`}`)
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	// A missing conditions argument is refused as well, and an unreachable object before the secret.
	if _, err := invokeRecordsSearch(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{},
		json.RawMessage(`{"object":"person"}`)); !asInvalidOK(err) {
		t.Errorf("no conditions: err = %v", err)
	}
	if _, err := invokeRecordsSearch(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{},
		json.RawMessage(`{"object":"workspaceMember","conditions":[{"field":"a","operator":"eq","value":1}]}`)); !asInvalidOK(err) {
		t.Errorf("system object: err = %v", err)
	}
}

func TestSearchRefusesUnusableConditionsAfterTheSchemaAndBeforeTheData(t *testing.T) {
	const fieldCanary, valueCanary = "zzCanaryField", "ZZ_VALUE_CANARY"
	requests := searchRequests(t, emptyPeople)
	for name, conditions := range map[string]string{
		"unknown field":        `[{"field":"` + fieldCanary + `","operator":"eq","value":"x"}]`,
		"unknown subfield":     `[{"field":"emails.` + fieldCanary + `","operator":"eq","value":"x"}]`,
		"subfield of plain":    `[{"field":"city.` + fieldCanary + `","operator":"eq","value":"x"}]`,
		"composite itself":     `[{"field":"emails","operator":"is","value":"NULL"}]`,
		"array subfield":       `[{"field":"emails.additionalEmails","operator":"is","value":"NULL"}]`,
		"deletedAt":            `[{"field":"deletedAt","operator":"is","value":"NOT_NULL"}]`,
		"deletedAt eq":         `[{"field":"deletedAt","operator":"gt","value":"2026-01-01T00:00:00Z"}]`,
		"relation field":       `[{"field":"company","operator":"is","value":"NULL"}]`,
		"relation sub":         `[{"field":"company.name","operator":"eq","value":"x"}]`,
		"rich text":            `[{"field":"bodyV2.markdown","operator":"ilike","value":"x"}]`,
		"actor identifier":     `[{"field":"actor.workspaceMemberId","operator":"eq","value":"x"}]`,
		"multi selection":      `[{"field":"tags","operator":"in","value":["A"]}]`,
		"enum with odd value":  `[{"field":"oddStage","operator":"eq","value":"ok"}]`,
		"value outside enum":   `[{"field":"stage","operator":"eq","value":"` + valueCanary + `"}]`,
		"list outside enum":    `[{"field":"stage","operator":"in","value":["NEW","` + valueCanary + `"]}]`,
		"ilike on selection":   `[{"field":"stage","operator":"ilike","value":"NEW"}]`,
		"range on text":        `[{"field":"city","operator":"gt","value":"a"}]`,
		"range on boolean":     `[{"field":"active","operator":"gt","value":true}]`,
		"in on boolean":        `[{"field":"active","operator":"in","value":[true]}]`,
		"startsWith on number": `[{"field":"age","operator":"startsWith","value":"1"}]`,
		"range on identifier":  `[{"field":"companyId","operator":"gt","value":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}]`,
		"text for number":      `[{"field":"age","operator":"eq","value":"18"}]`,
		"number for text":      `[{"field":"city","operator":"eq","value":18}]`,
		"text for boolean":     `[{"field":"active","operator":"eq","value":"true"}]`,
		"bad identifier":       `[{"field":"companyId","operator":"eq","value":"` + valueCanary + `"}]`,
		"date for date-time":   `[{"field":"seen","operator":"eq","value":"2026-01-02"}]`,
		"date-time for date":   `[{"field":"born","operator":"eq","value":"2026-01-02T03:04:05Z"}]`,
		"impossible date":      `[{"field":"born","operator":"eq","value":"2026-02-31"}]`,
		"identifier relation":  `[{"field":"ownerId","operator":"eq","value":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}]`,
		"identifier unmatched": `[{"field":"orphanId","operator":"is","value":"NULL"}]`,
	} {
		_, err := search(t, conditions)
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
			continue
		}
		if strings.Contains(err.Error(), fieldCanary) || strings.Contains(err.Error(), valueCanary) {
			t.Errorf("%s: the error names the field or value: %v", name, err)
		}
	}
	if len(dataRequests(*requests)) != 0 {
		t.Errorf("a data request was sent: %v", *requests)
	}
}

func TestSearchFiltersByRelationIdentifiersOnlyOfReachableTargets(t *testing.T) {
	const company = `[{"field":"companyId","operator":"eq","value":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}]`
	requests := searchRequests(t, emptyPeople)
	if _, err := search(t, company, "object/person", "object/company"); err != nil {
		t.Fatalf("reachable target: %v", err)
	}
	if len(dataRequests(*requests)) != 1 {
		t.Fatalf("requests = %v", *requests)
	}
	if _, err := search(t, company, "object/person"); !asInvalidOK(err) {
		t.Errorf("outside the targets: err = %v, want invalid-request", err)
	}
	if len(dataRequests(*requests)) != 1 {
		t.Errorf("a data request followed the refusal: %v", *requests)
	}
}

func TestSearchCursorIsBoundToTheConditions(t *testing.T) {
	requests := searchRequests(t, peoplePage)
	const conditions = `[{"field":"city","operator":"eq","value":"Berlin"},{"field":"age","operator":"lt","value":9}]`
	out, err := search(t, conditions)
	if err != nil {
		t.Fatal(err)
	}
	var page RecordList
	_ = json.Unmarshal(out, &page)
	if page.NextCursor == "" {
		t.Fatal("no cursor")
	}
	// The same conditions in another order continue the sequence.
	reordered := `[{"field":"age","operator":"lt","value":9},{"field":"city","operator":"eq","value":"Berlin"}]`
	if _, err := runTool(t, invokeRecordsSearch, `{"object":"person","cursor":"`+page.NextCursor+`","conditions":`+reordered+`}`); err != nil {
		t.Fatalf("continuation: %v", err)
	}
	if got := dataRequests(*requests)[1].Query().Get("starting_after"); got != "cursorXYZ" {
		t.Errorf("starting_after = %q", got)
	}
	// A list cursor does not continue a search, and a search cursor not a list or another search.
	listOut, err := runTool(t, invokeRecordsList, `{"object":"person"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list RecordList
	_ = json.Unmarshal(listOut, &list)
	before := len(*requests)
	for name, call := range map[string]func() error{
		"list cursor to search": func() error {
			_, err := runTool(t, invokeRecordsSearch, `{"object":"person","cursor":"`+list.NextCursor+`","conditions":`+conditions+`}`)
			return err
		},
		"other conditions": func() error {
			_, err := runTool(t, invokeRecordsSearch, `{"object":"person","cursor":"`+page.NextCursor+
				`","conditions":[{"field":"city","operator":"eq","value":"Bonn"}]}`)
			return err
		},
		"fewer conditions": func() error {
			_, err := runTool(t, invokeRecordsSearch, `{"object":"person","cursor":"`+page.NextCursor+
				`","conditions":[{"field":"city","operator":"eq","value":"Berlin"}]}`)
			return err
		},
		"search cursor to list": func() error {
			_, err := runTool(t, invokeRecordsList, `{"object":"person","cursor":"`+page.NextCursor+`"}`)
			return err
		},
	} {
		if err := call(); !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	if len(*requests) != before {
		t.Errorf("a refused cursor caused requests: %d -> %d", before, len(*requests))
	}
}

func TestSearchDoesNotShowEnumValues(t *testing.T) {
	requests := searchRequests(t, emptyPeople)
	_ = requests
	out, err := runTool(t, invokeObjectsGet, `{"object":"person"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"LOST_X", "has space", `"enum"`} {
		if strings.Contains(string(out), value) {
			t.Errorf("objects.get shows %q: %s", value, out)
		}
	}
}

func TestEnumValuesAreBoundedAndPlain(t *testing.T) {
	long := strings.Repeat("A", 65)
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("V%d", i)
	}
	for name, raw := range map[string]string{
		"too long":   `["` + long + `"]`,
		"too many":   `["` + strings.Join(many, `","`) + `"]`,
		"odd chars":  `["a b"]`,
		"quote":      `["a\"b"]`,
		"not string": `[1]`,
		"empty":      `[]`,
	} {
		if got := enumValues(json.RawMessage(raw)); got != nil {
			t.Errorf("%s: enum = %v", name, got)
		}
	}
	if got := enumValues(json.RawMessage(`["NEW","LOST_X"]`)); len(got) != 2 {
		t.Errorf("enum = %v", got)
	}
}

func group(t *testing.T, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	return runTool(t, invokeRecordsGroupBy, args, targets...)
}

func TestGroupByBuildsTheFixedRequest(t *testing.T) {
	requests := searchRequests(t, `[{"groupByDimensionValues":["NEW","2026-01-02"],"totalCount":3},`+
		`{"groupByDimensionValues":[null,"2026-01-03"],"totalCount":"4"}]`)
	out, err := group(t, `{"object":"person","group_by":["stage","born"],"conditions":[{"field":"city","operator":"eq","value":"Berlin"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	request := dataRequests(*requests)[0]
	query := request.Query()
	if request.Path != "/rest/people/groupBy" ||
		query.Get("group_by") != `[{"stage":true},{"born":{"granularity":"DAY","timeZone":"UTC"}}]` ||
		query.Get("aggregate") != `["totalCount"]` || query.Get("limit") != "200" || query.Get("filter") != "city[eq]:Berlin" ||
		len(query) != 4 {
		t.Fatalf("request = %s %v", request.Path, query)
	}
	want := `{"groups":[{"values":{"born":"2026-01-02","stage":"NEW"},"count":3},{"values":{"born":"2026-01-03","stage":null},"count":4}]}`
	if string(out) != want {
		t.Errorf("out = %s, want %s", out, want)
	}
}

func TestGroupByAcceptsTheDocumentedWrappedAnswerAndBooleansAndIdentifiers(t *testing.T) {
	requests := searchRequests(t, `{"data":{"peopleGroupBy":[{"groupByDimensionValues":[true,"`+personID+`"],"totalCount":1}]}}`)
	out, err := group(t, `{"object":"person","group_by":["active","companyId"]}`, "object/person", "object/company")
	if err != nil {
		t.Fatal(err)
	}
	if got := dataRequests(*requests)[0].Query().Get("group_by"); got != `[{"active":true},{"companyId":true}]` {
		t.Errorf("group_by = %s", got)
	}
	if !strings.Contains(string(out), `"active":true`) || !strings.Contains(string(out), `"count":1`) {
		t.Errorf("out = %s", out)
	}
}

func TestGroupByRefusesFieldsThatCannotBeGroupedBeforeTheData(t *testing.T) {
	const canary = "zzCanaryField"
	requests := searchRequests(t, `[]`)
	for name, args := range map[string]string{
		"text":            `{"object":"person","group_by":["city"]}`,
		"number":          `{"object":"person","group_by":["age"]}`,
		"record id":       `{"object":"person","group_by":["id"]}`,
		"unmatched id":    `{"object":"person","group_by":["orphanId"]}`,
		"system relation": `{"object":"person","group_by":["ownerId"]}`,
		"multi selection": `{"object":"person","group_by":["tags"]}`,
		"composite":       `{"object":"person","group_by":["name"]}`,
		"subfield":        `{"object":"person","group_by":["name.firstName"]}`,
		"relation":        `{"object":"person","group_by":["company"]}`,
		"deletedAt":       `{"object":"person","group_by":["deletedAt"]}`,
		"unknown":         `{"object":"person","group_by":["` + canary + `"]}`,
		"one bad of two":  `{"object":"person","group_by":["stage","city"]}`,
		"bad condition":   `{"object":"person","group_by":["stage"],"conditions":[{"field":"stage","operator":"eq","value":"NOPE"}]}`,
		"unreachable id":  `{"object":"person","group_by":["companyId"]}`,
	} {
		targets := []string(nil)
		if name == "unreachable id" {
			targets = []string{"object/person"}
		}
		_, err := group(t, args, targets...)
		if !asInvalidOK(err) || strings.Contains(err.Error(), canary) {
			t.Errorf("%s: err = %v, want a refusal without the field", name, err)
		}
	}
	if len(dataRequests(*requests)) != 0 {
		t.Errorf("a data request was sent: %v", *requests)
	}
}

func TestGroupByRefusesMalformedArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for name, args := range map[string]string{
		"none":      `{"object":"person","group_by":[]}`,
		"three":     `{"object":"person","group_by":["stage","active","born"]}`,
		"repeat":    `{"object":"person","group_by":["stage","stage"]}`,
		"bad name":  `{"object":"person","group_by":["a,b"]}`,
		"dotted":    `{"object":"person","group_by":["a.b"]}`,
		"object":    `{"object":"workspaceMember","group_by":["stage"]}`,
		"six conds": `{"object":"person","group_by":["stage"],"conditions":[` + strings.Repeat(`{"field":"age","operator":"eq","value":1},`, 5) + `{"field":"age","operator":"eq","value":2}]}`,
		"bad value": `{"object":"person","group_by":["stage"],"conditions":[{"field":"city","operator":"eq","value":"a,b"}]}`,
	} {
		if _, err := group(t, args); !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
}

func TestGroupByCapsGroupsAndValues(t *testing.T) {
	groups := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"groupByDimensionValues":["G%d"],"totalCount":1}`, i)
		}
		return `[` + strings.Join(items, ",") + `]`
	}
	const stage = `{"object":"person","group_by":["stage"]}`
	for name, data := range map[string]string{
		"201 groups":        groups(201),
		"a full page":       groups(200),
		"long value":        `[{"groupByDimensionValues":["` + strings.Repeat("A", 257) + `"],"totalCount":1}]`,
		"number value":      `[{"groupByDimensionValues":[5],"totalCount":1}]`,
		"object value":      `[{"groupByDimensionValues":[{"a":1}],"totalCount":1}]`,
		"missing dimension": `[{"totalCount":1}]`,
		"two dimensions":    `[{"groupByDimensionValues":["a","b"],"totalCount":1}]`,
		"missing count":     `[{"groupByDimensionValues":["a"]}]`,
		"negative count":    `[{"groupByDimensionValues":["a"],"totalCount":-1}]`,
		"fraction count":    `[{"groupByDimensionValues":["a"],"totalCount":1.5}]`,
		"no groups array":   `{"data":{}}`,
		"not json":          `nope`,
	} {
		searchRequests(t, data)
		_, err := group(t, stage)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid-response", name, err)
		}
	}
	searchRequests(t, groups(199))
	out, err := group(t, stage)
	if err != nil || strings.Count(string(out), `"count"`) != 199 {
		t.Errorf("199 groups: %v", err)
	}
	searchRequests(t, `[]`)
	if out, err := group(t, stage); err != nil || string(out) != `{"groups":[]}` {
		t.Errorf("no groups: %s %v", out, err)
	}
}

func TestSearchAndGroupByKeepProviderTextOutOfErrors(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, searchSchema), nil
		}
		return jsonResponse(http.StatusBadRequest, `{"message":"`+recordCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	_, err := search(t, `[{"field":"age","operator":"eq","value":1}]`)
	if err == nil || strings.Contains(err.Error(), recordCanary) {
		t.Errorf("search err = %v", err)
	}
	_, err = group(t, `{"object":"person","group_by":["stage"]}`)
	if err == nil || strings.Contains(err.Error(), recordCanary) {
		t.Errorf("group err = %v", err)
	}
}
