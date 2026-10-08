package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	personID     = "11111111-2222-4333-8444-555555555555"
	personTwoID  = "66666666-7777-4888-9999-000000000000"
	recordCanary = "person-data-canary-twenty-5e27"
)

// recordsSchema holds person (composite name, relation, uuid field), note (rich text), a custom object, and a
// system object.
const recordsSchema = `{
  "paths":{
    "/people":{"post":{"operationId":"createOnePerson"}},
    "/notes":{"post":{"operationId":"createOneNote"}},
    "/rockets":{"post":{"operationId":"createOneRocket"}},
    "/companies":{"post":{"operationId":"createOneCompany"}},
    "/workspaceMembers":{"post":{"operationId":"createOneWorkspaceMember"}}
  },
  "components":{"schemas":{
    "Person":{"type":"object","properties":{"name":{"type":"object","properties":{"firstName":{"type":"string"},"lastName":{"type":"string"}}}}},
    "PersonForUpdate":{"type":"object","properties":{"name":{"type":"object"}}},
    "PersonForResponse":{"type":"object","properties":{
      "id":{"type":"string","format":"uuid"},"name":{"type":"object"},"city":{"type":"string"},
      "emails":{"type":"array","items":{"type":"string"}},"extra":{"type":"object"},
      "companyId":{"type":"string","format":"uuid"},"company":{"$ref":"#/components/schemas/CompanyForResponse"},
      "owner":{"$ref":"#/components/schemas/WorkspaceMemberForResponse"},
      "createdAt":{"type":"string"},"updatedAt":{"type":"string"},"deletedAt":{}}},
    "Note":{"type":"object","properties":{"bodyV2":{"type":"object","properties":{"blocknote":{"type":"string"},"markdown":{"type":"string"}}}}},
    "NoteForResponse":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"bodyV2":{"type":"object"}}},
    "Rocket":{"type":"object","properties":{"name":{"type":"string"}}},
    "RocketForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}}},
    "Company":{"type":"object","properties":{"name":{"type":"string"}}},
    "CompanyForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}}},
    "WorkspaceMember":{"type":"object","properties":{"name":{"type":"object"}}}
  }}
}`

// recordRequests serves the schema and the given data answer, and collects the requests.
func recordRequests(t *testing.T, data string) *[]*url.URL {
	t.Helper()
	requests := &[]*url.URL{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*requests = append(*requests, request.URL)
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, recordsSchema), nil
		}
		return jsonResponse(http.StatusOK, data), nil
	})
	stubLimiter(t, cloudKey)
	return requests
}

func dataRequests(requests []*url.URL) []*url.URL {
	var result []*url.URL
	for _, request := range requests {
		if request.Path != schemaPath {
			result = append(result, request)
		}
	}
	return result
}

func runRecords(t *testing.T, list bool, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	handler := invokeRecordsGet
	if list {
		handler = invokeRecordsList
	}
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	return out, nil
}

const personPage = `{"data":{"people":[
  {"id":"` + personID + `","name":{"firstName":"Ada","lastName":"` + recordCanary + `"},"city":"Berlin",
   "companyId":"` + otherID + `","company":{"id":"` + otherID + `","name":"embedded"},
   "owner":{"id":"x"},"position":1,"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"},
  {"id":"` + personTwoID + `","name":{"firstName":"Bob","lastName":"B"},"companyId":null}
 ]},"totalCount":2,"pageInfo":{"hasNextPage":true,"endCursor":"cursorABC"}}`

func TestRecordsListReadsAPageWithoutRelations(t *testing.T) {
	requests := recordRequests(t, personPage)
	out, err := runRecords(t, true, `{"object":"person"}`)
	if err != nil {
		t.Fatal(err)
	}
	data := dataRequests(*requests)
	if len(data) != 1 || data[0].Path != "/rest/people" || data[0].Query().Get("depth") != "0" ||
		data[0].Query().Get("limit") != "25" || data[0].Query().Has("fields") || data[0].Query().Has("filter") {
		t.Fatalf("requests = %v", data)
	}
	var list RecordList
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatal(err)
	}
	first := list.Records[0]
	if len(list.Records) != 2 || first.ID != personID || first.CreatedAt == "" || first.UpdatedAt == "" ||
		!list.HasMore || list.NextCursor == "" || strings.Contains(list.NextCursor, "cursorABC") {
		t.Fatalf("list = %+v", list)
	}
	for _, dropped := range []string{"company", "owner", "position", "id", "createdAt"} {
		if _, ok := first.Fields[dropped]; ok {
			t.Errorf("field %s reached the output", dropped)
		}
	}
	if first.Fields["companyId"] != otherID || first.Fields["city"] != "Berlin" {
		t.Errorf("fields = %v", first.Fields)
	}
	if strings.Contains(string(out), "embedded") {
		t.Errorf("an embedded relation reached the output: %s", out)
	}
}

func TestRecordsListSendsFieldsAndSortAndFollowsTheCursor(t *testing.T) {
	requests := recordRequests(t, personPage)
	out, err := runRecords(t, true, `{"object":"person","fields":["name","city"],"order_by":"name.lastName","direction":"desc","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	query := dataRequests(*requests)[0].Query()
	if query.Get("fields") != "city,name,createdAt,updatedAt" || query.Get("order_by") != "name.lastName[DescNullsLast]" ||
		query.Get("limit") != "2" || query.Get("depth") != "0" || query.Has("starting_after") {
		t.Fatalf("query = %v", query)
	}
	var list RecordList
	_ = json.Unmarshal(out, &list)
	if _, ok := list.Records[0].Fields["companyId"]; ok {
		t.Error("a field that was not requested is reported")
	}
	// The same selection continues, in another batch size.
	_, err = runRecords(t, true, `{"object":"person","fields":["city","name"],"order_by":"name.lastName","direction":"desc","limit":5,"cursor":"`+list.NextCursor+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	query = dataRequests(*requests)[1].Query()
	if query.Get("starting_after") != "cursorABC" || query.Get("limit") != "5" {
		t.Fatalf("continuation query = %v", query)
	}
}

func TestRecordsListRefusesForeignCursorsBeforeAnyRequest(t *testing.T) {
	requests := recordRequests(t, personPage)
	out, err := runRecords(t, true, `{"object":"person","order_by":"city"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list RecordList
	_ = json.Unmarshal(out, &list)
	before := len(*requests)
	for name, args := range map[string]string{
		"other object":    `{"object":"rocket","order_by":"city","cursor":"` + list.NextCursor + `"}`,
		"other sort":      `{"object":"person","order_by":"city","direction":"desc","cursor":"` + list.NextCursor + `"}`,
		"no sort":         `{"object":"person","cursor":"` + list.NextCursor + `"}`,
		"other fields":    `{"object":"person","order_by":"city","fields":["city"],"cursor":"` + list.NextCursor + `"}`,
		"raw twenty one":  `{"object":"person","cursor":"cursorABC"}`,
		"altered":         `{"object":"person","order_by":"city","cursor":"` + list.NextCursor[:len(list.NextCursor)-2] + `AA"}`,
		"not base64 form": `{"object":"person","cursor":"a+b/c="}`,
	} {
		_, err := runRecords(t, true, args)
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	if len(*requests) != before {
		t.Errorf("a refused cursor caused %d request(s)", len(*requests)-before)
	}
}

func TestRecordsRefuseUnreachableObjectsBeforeSecretAccess(t *testing.T) {
	refuse(t)
	for _, tt := range []struct {
		object  string
		targets []string
	}{
		{"workspaceMember", nil}, {"person", []string{"object/rocket"}}, {"../x", nil}, {"message", nil},
	} {
		list := `{"object":"` + tt.object + `"}`
		get := `{"object":"` + tt.object + `","id":"` + personID + `"}`
		for name, call := range map[string]func() error{
			"list": func() error {
				_, err := invokeRecordsList(context.Background(), targetConnection(tt.targets...), countingResolver(t),
					&redact.Redactor{}, json.RawMessage(list))
				return err
			},
			"get": func() error {
				_, err := invokeRecordsGet(context.Background(), targetConnection(tt.targets...), countingResolver(t),
					&redact.Redactor{}, json.RawMessage(get))
				return err
			},
		} {
			if err := call(); !asInvalidOK(err) {
				t.Errorf("%s %q %v: err = %v, want invalid-request", name, tt.object, tt.targets, err)
			}
		}
	}
	// Argument errors are refused before the secret as well.
	_, err := invokeRecordsGet(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{},
		json.RawMessage(`{"object":"person","id":"not-a-uuid"}`))
	if !asInvalidOK(err) {
		t.Errorf("bad id: err = %v", err)
	}
}

func TestRecordsRefuseUnknownFieldsAfterTheSchemaAndBeforeTheData(t *testing.T) {
	const secretName = "zzCanaryField"
	requests := recordRequests(t, personPage)
	for name, args := range map[string]string{
		"unknown field":  `{"object":"person","fields":["` + secretName + `"]}`,
		"unknown sort":   `{"object":"person","order_by":"` + secretName + `"}`,
		"relation field": `{"object":"person","fields":["company"]}`,
		"relation sort":  `{"object":"person","order_by":"company"}`,
		"array sort":     `{"object":"person","order_by":"emails"}`,
		"composite sort": `{"object":"person","order_by":"name"}`,
		"unknown sub":    `{"object":"person","order_by":"name.nope"}`,
		"rich text sort": `{"object":"note","order_by":"bodyV2.markdown"}`,
		"too many":       `{"object":"person","fields":["` + strings.Join(manyNames(51), `","`) + `"]}`,
		"get unknown":    `{"object":"person","id":"` + personID + `","fields":["` + secretName + `"]}`,
	} {
		_, err := runRecords(t, !strings.Contains(name, "get"), args)
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
			continue
		}
		if strings.Contains(err.Error(), secretName) {
			t.Errorf("%s: the error names the field: %v", name, err)
		}
	}
	if len(dataRequests(*requests)) != 0 {
		t.Errorf("a data request was sent: %v", *requests)
	}
}

func manyNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = "f" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return names
}

func TestRecordsPluralComesFromTheCatalog(t *testing.T) {
	requests := recordRequests(t, `{"data":{"notes":[]},"pageInfo":{"hasNextPage":false}}`)
	if _, err := runRecords(t, true, `{"object":"note"}`); err != nil {
		t.Fatal(err)
	}
	if data := dataRequests(*requests); len(data) != 1 || data[0].Path != "/rest/notes" {
		t.Fatalf("requests = %v", data)
	}
	if _, err := runRecords(t, true, `{"object":"ghost"}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown object err = %v, want not-found", err)
	}
}

func TestRecordsReportRichTextAsMarkdownOnly(t *testing.T) {
	recordRequests(t, `{"data":{"notes":[{"id":"`+personID+`","title":"t","bodyV2":{"blocknote":"[{\"secret\":1}]","markdown":"# Hi"}}]},"pageInfo":{"hasNextPage":false}}`)
	out, err := runRecords(t, true, `{"object":"note"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "blocknote") || strings.Contains(string(out), "secret") {
		t.Fatalf("blocknote reached the output: %s", out)
	}
	var list RecordList
	_ = json.Unmarshal(out, &list)
	body, _ := list.Records[0].Fields["bodyV2"].(map[string]any)
	if len(body) != 1 || body["markdown"] != "# Hi" || list.HasMore || list.NextCursor != "" {
		t.Errorf("fields = %v", list.Records[0].Fields)
	}
}

func TestRecordsGetChecksTheAnsweredRecord(t *testing.T) {
	requests := recordRequests(t, `{"data":{"person":{"id":"`+personID+`","city":"Berlin","company":{"id":"`+otherID+`"}}}}`)
	out, err := runRecords(t, false, `{"object":"person","id":"`+personID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	data := dataRequests(*requests)
	if len(data) != 1 || data[0].Path != "/rest/people/"+personID || data[0].Query().Get("depth") != "0" {
		t.Fatalf("requests = %v", data)
	}
	var record Record
	_ = json.Unmarshal(out, &record)
	if record.ID != personID || record.Fields["city"] != "Berlin" || len(record.Fields) != 1 {
		t.Errorf("record = %+v", record)
	}
	_, err = runRecords(t, false, `{"object":"person","id":"`+personTwoID+`"}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("another record: err = %v, want invalid-response", err)
	}
}

func TestRecordsRefuseUnusableIdentifiers(t *testing.T) {
	for name, body := range map[string]string{
		"record id":  `{"data":{"people":[{"id":"nope"}]},"pageInfo":{"hasNextPage":false}}`,
		"no id":      `{"data":{"people":[{"city":"x"}]},"pageInfo":{"hasNextPage":false}}`,
		"uuid field": `{"data":{"people":[{"id":"` + personID + `","companyId":"` + recordCanary + `"}]},"pageInfo":{"hasNextPage":false}}`,
		"no list":    `{"data":{},"pageInfo":{"hasNextPage":false}}`,
		"bad cursor": `{"data":{"people":[]},"pageInfo":{"hasNextPage":true,"endCursor":"a b"}}`,
		"no cursor":  `{"data":{"people":[]},"pageInfo":{"hasNextPage":true}}`,
		"too many":   `{"data":{"people":[` + strings.Repeat(`{"id":"`+personID+`"},`, 3) + `{"id":"` + personID + `"}]},"pageInfo":{"hasNextPage":false}}`,
	} {
		recordRequests(t, body)
		_, err := runRecords(t, true, `{"object":"person","limit":2}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid-response", name, err)
		} else if strings.Contains(err.Error(), recordCanary) {
			t.Errorf("%s: the error carries record data: %v", name, err)
		}
	}
}

func TestRecordsCapValuesWithoutLeakingThem(t *testing.T) {
	nest := func(depth int) string {
		return strings.Repeat("[", depth) + `"` + recordCanary + `"` + strings.Repeat("]", depth)
	}
	items := make([]string, 101)
	for i := range items {
		items[i] = `"` + recordCanary + `"`
	}
	for name, tt := range map[string]struct {
		value string
		ok    bool
	}{
		"string at limit":   {`"` + strings.Repeat("a", maxValueString) + `"`, true},
		"string over limit": {`"` + strings.Repeat("a", maxValueString+1) + `"`, false},
		"depth four":        {nest(4), true},
		"depth five":        {nest(5), false},
		"object depth five": {`{"a":{"a":{"a":{"a":{"a":1}}}}}`, false},
		"hundred items":     {"[" + strings.Join(items[:100], ",") + "]", true},
		"hundred and one":   {"[" + strings.Join(items, ",") + "]", false},
		"nested string cap": {`{"a":"` + strings.Repeat("a", maxValueString+1) + `"}`, false},
	} {
		recordRequests(t, `{"data":{"people":[{"id":"`+personID+`","extra":`+tt.value+`}]},"pageInfo":{"hasNextPage":false}}`)
		_, err := runRecords(t, true, `{"object":"person"}`)
		switch {
		case tt.ok && err != nil:
			t.Errorf("%s: err = %v", name, err)
		case !tt.ok && classOf(err) != provider.ClassInvalidResponse:
			t.Errorf("%s: err = %v, want invalid-response", name, err)
		case err != nil && strings.Contains(err.Error(), recordCanary):
			t.Errorf("%s: the error carries record data", name)
		}
	}
}

func TestRecordValuesStayOutOfProviderErrors(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, recordsSchema), nil
		}
		return jsonResponse(http.StatusBadRequest, `{"message":"`+recordCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	_, err := runRecords(t, true, `{"object":"person"}`)
	if err == nil || strings.Contains(err.Error(), recordCanary) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordToolsDeclareTheirOwnSensitivityAndJoinTheReadProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, d := range []capability.Descriptor{recordsList, recordsGet} {
		if d.Risk.DataSensitivity != "twentycrm-record-data" || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || d.Group != "" && d.Group != recordsGroup {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	tools := strings.Join(metadata.Profiles[0].Tools, " ")
	if !strings.Contains(tools, recordsList.ID) || !strings.Contains(tools, recordsGet.ID) {
		t.Errorf("read profile = %v", metadata.Profiles[0].Tools)
	}
}
