package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const writeCanary = "write-value-canary-twenty-b81c"

// personWriteProps are the writable properties of person, shaped as twenty-server generates them
// (convert-object-metadata-to-schema-properties.util.ts): composite types with their named parts, a
// selection and a multi-selection with their enum values, a relation as <name>Id, and the types the tools
// refuse (actor, free JSON, files, plain text lists).
const personWriteProps = `
  "name":{"type":"object","properties":{"firstName":{"type":"string"},"lastName":{"type":"string"}}},
  "emails":{"type":"object","properties":{"primaryEmail":{"type":"string"},
    "additionalEmails":{"type":"array","items":{"type":"string","format":"email"}}}},
  "phones":{"type":"object","properties":{
    "additionalPhones":{"type":"array","items":{"type":"object","properties":{"number":{"type":"string"},"countryCode":{"type":"string"},"callingCode":{"type":"string"}}}},
    "primaryPhoneCountryCode":{"type":"string"},"primaryPhoneCallingCode":{"type":"string"},"primaryPhoneNumber":{"type":"string"}}},
  "linkedinLink":{"type":"object","properties":{"primaryLinkLabel":{"type":"string"},"primaryLinkUrl":{"type":"string"},
    "secondaryLinks":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string","format":"uri"},"label":{"type":"string"}}}}}},
  "salary":{"type":"object","properties":{"amountMicros":{"type":"number"},"currencyCode":{"type":"string"}}},
  "address":{"type":"object","properties":{"addressStreet1":{"type":"string"},"addressCity":{"type":"string"},
    "addressCountry":{"type":"string"},"addressLat":{"type":"number"},"addressLng":{"type":"number"}}},
  "city":{"type":"string"},"age":{"type":"integer"},"score":{"type":"number"},"vip":{"type":"boolean"},
  "birthday":{"type":"string","format":"date"},"lastContact":{"type":"string","format":"date-time"},
  "status":{"type":"string","enum":["NEW","ACTIVE"]},
  "tags":{"type":"array","items":{"type":"string","enum":["ALPHA","BETA"]}},
  "bio":{"type":"object","properties":{"blocknote":{"type":"string"},"markdown":{"type":"string"}}},
  "companyId":{"type":"string","format":"uuid"},"ownerId":{"type":"string","format":"uuid"},
  "createdBy":{"type":"object","properties":{"source":{"type":"string","enum":["API","MANUAL"]}}},
  "position":{"type":"number"},"extra":{"type":"object"},
  "attachments":{"type":"array","items":{"type":"object","properties":{"fileId":{"type":"string","format":"uuid"},"label":{"type":"string"}}}},
  "nicknames":{"type":"array","items":{"type":"string"}}`

// writeSchema holds person with the properties above; the create schema requires name, the update schema
// requires nothing and lacks the field locked. The response shape holds a relation to company and one to
// the system object workspaceMember in the form the generator emits (oneOf), a field absent from both write
// shapes (readOnly), and the identifiers and timestamps.
const writeSchema = `{
  "paths":{
    "/people":{"post":{"operationId":"createOnePerson"}},
    "/companies":{"post":{"operationId":"createOneCompany"}},
    "/rockets":{"post":{"operationId":"createOneRocket"}},
    "/workspaceMembers":{"post":{"operationId":"createOneWorkspaceMember"}}
  },
  "components":{"schemas":{
    "Person":{"type":"object","required":["name","company"],"properties":{` + personWriteProps + `,"locked":{"type":"string"}}},
    "PersonForUpdate":{"type":"object","properties":{` + personWriteProps + `}},
    "PersonForResponse":{"type":"object","properties":{
      "id":{"type":"string","format":"uuid"},"name":{"type":"object","properties":{"firstName":{"type":"string"},"lastName":{"type":"string"}}},
      "emails":{"type":"object"},"city":{"type":"string"},"age":{"type":"integer"},"status":{"type":"string","enum":["NEW","ACTIVE"]},
      "bio":{"type":"object","properties":{"blocknote":{"type":"string"},"markdown":{"type":"string"}}},
      "readOnly":{"type":"string"},"locked":{"type":"string"},
      "companyId":{"type":"string","format":"uuid"},"company":{"type":"object","oneOf":[{"$ref":"#/components/schemas/CompanyForResponse"}]},
      "ownerId":{"type":"string","format":"uuid"},"owner":{"type":"object","oneOf":[{"$ref":"#/components/schemas/WorkspaceMemberForResponse"}]},
      "createdAt":{"type":"string","format":"date-time"},"updatedAt":{"type":"string","format":"date-time"},"deletedAt":{}}},
    "Company":{"type":"object","properties":{"name":{"type":"string"}}},
    "CompanyForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}}},
    "Rocket":{"type":"object","properties":{"name":{"type":"string"}}},
    "RocketForUpdate":{"type":"object","properties":{"name":{"type":"string"}}},
    "RocketForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}}},
    "WorkspaceMember":{"type":"object","properties":{"name":{"type":"object"}}}
  }}
}`

// writeCall is one request the fake transport saw.
type writeCall struct {
	Method string
	URI    string
	Body   string
}

// serveWrites serves the schema and answers every writing request with answer (status and body).
func serveWrites(t *testing.T, status int, answer string) *[]writeCall {
	t.Helper()
	calls := &[]writeCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := writeCall{Method: request.Method, URI: request.URL.RequestURI()}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, writeSchema), nil
		}
		return jsonResponse(status, answer), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func writes(calls []writeCall) []writeCall {
	var result []writeCall
	for _, call := range calls {
		if call.Method != http.MethodGet {
			result = append(result, call)
		}
	}
	return result
}

func createdAnswer(verb string) string {
	return `{"data":{"` + verb + `Person":{"id":"` + personID + `","name":{"firstName":"Ada","lastName":"L"},` +
		`"emails":{"primaryEmail":"a@example.com","additionalEmails":[]},"city":"Berlin","companyId":"` + otherID + `",` +
		`"company":{"id":"` + otherID + `","name":"embedded"},"createdAt":"2026-01-01T00:00:00.000Z",` +
		`"updatedAt":"2026-01-02T00:00:00.000Z","bio":{"blocknote":"[{\"x\":1}]","markdown":"# hi"}}}}`
}

func runWrite(t *testing.T, create bool, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	handler := invokeRecordsUpdate
	if create {
		handler = invokeRecordsCreate
	}
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	return out, nil
}

// coveredCreate satisfies the required fields of person.
const coveredCreate = `"name":{"firstName":"Ada"},"companyId":"` + otherID + `"`

func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var a, b any
	if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		t.Fatalf("not JSON: %s / %s", got, want)
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestRecordsCreateSendsEachFieldGroupAsCheckedBody(t *testing.T) {
	for _, tt := range []struct{ name, fields, body string }{
		{"text number boolean date", `"city":"Berlin","age":42,"score":1.5,"vip":true,"birthday":"1990-02-03","lastContact":"2026-05-06T07:08:09Z"`,
			`"city":"Berlin","age":42,"score":1.5,"vip":true,"birthday":"1990-02-03","lastContact":"2026-05-06T07:08:09Z"`},
		{"selection and multi-selection", `"status":"ACTIVE","tags":["ALPHA","BETA"]`, `"status":"ACTIVE","tags":["ALPHA","BETA"]`},
		{"emails and phones", `"emails":{"primaryEmail":"a@example.com","additionalEmails":["b@example.com"]},` +
			`"phones":{"primaryPhoneNumber":"123","primaryPhoneCountryCode":"DE","primaryPhoneCallingCode":"+49",` +
			`"additionalPhones":[{"number":"456","countryCode":"DE","callingCode":"+49"}]}`,
			`"emails":{"primaryEmail":"a@example.com","additionalEmails":["b@example.com"]},` +
				`"phones":{"primaryPhoneNumber":"123","primaryPhoneCountryCode":"DE","primaryPhoneCallingCode":"+49",` +
				`"additionalPhones":[{"number":"456","countryCode":"DE","callingCode":"+49"}]}`},
		{"links currency address", `"linkedinLink":{"primaryLinkUrl":"https://x.example","secondaryLinks":[{"url":"https://y.example","label":"Y"}]},` +
			`"salary":{"amountMicros":1500000,"currencyCode":"EUR"},"address":{"addressCity":"Berlin","addressLat":52.5}`,
			`"linkedinLink":{"primaryLinkUrl":"https://x.example","secondaryLinks":[{"url":"https://y.example","label":"Y"}]},` +
				`"salary":{"amountMicros":1500000,"currencyCode":"EUR"},"address":{"addressCity":"Berlin","addressLat":52.5}`},
		{"rich text as markdown", `"bio":{"markdown":"# Title"}`, `"bio":{"markdown":"# Title"}`},
		{"relation identifier", `"companyId":"` + personTwoID + `"`, `"companyId":"` + personTwoID + `"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusCreated, createdAnswer("create"))
			fields := coveredCreate + "," + tt.fields
			want := `{"name":{"firstName":"Ada"},"companyId":"` + otherID + `",` + tt.body + `}`
			if strings.Contains(tt.fields, `"companyId"`) {
				fields = `"name":{"firstName":"Ada"},` + tt.fields
				want = `{"name":{"firstName":"Ada"},` + tt.body + `}`
			}
			out, err := runWrite(t, true, `{"object":"person","fields":{`+fields+`}}`)
			if err != nil {
				t.Fatal(err)
			}
			sent := writes(*calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != "/rest/people?depth=0" {
				t.Fatalf("writes = %+v", sent)
			}
			sameJSON(t, sent[0].Body, want)
			var record Record
			if err := json.Unmarshal(out, &record); err != nil || record.ID != personID || record.Fields["city"] != "Berlin" {
				t.Fatalf("record = %s (%v)", out, err)
			}
			if _, ok := record.Fields["company"]; ok || strings.Contains(string(out), "embedded") ||
				strings.Contains(string(out), "blocknote") || !strings.Contains(string(out), `"markdown":"# hi"`) {
				t.Errorf("the answer was not projected like a read: %s", out)
			}
		})
	}
}

func TestRecordsUpdateSendsOnlyTheNamedFields(t *testing.T) {
	calls := serveWrites(t, http.StatusOK, createdAnswer("update"))
	out, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"city":"Hamburg","bio":{"markdown":"x"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	sent := writes(*calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != "/rest/people/"+personID+"?depth=0" {
		t.Fatalf("writes = %+v", sent)
	}
	sameJSON(t, sent[0].Body, `{"city":"Hamburg","bio":{"markdown":"x"}}`)
	if !strings.Contains(string(out), personID) {
		t.Errorf("out = %s", out)
	}
	// Create requires name and company; an update of one field does not.
	if len(*calls) != 2 {
		t.Errorf("requests = %d, want the schema and one change", len(*calls))
	}
}

func TestRecordsWriteRelationNeedsAReachableTarget(t *testing.T) {
	// company is reachable without targets; workspaceMember is a system object and never.
	calls := serveWrites(t, http.StatusOK, createdAnswer("update"))
	if _, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"companyId":"`+otherID+`"}}`); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, writes(*calls)[0].Body, `{"companyId":"`+otherID+`"}`)
	before := len(writes(*calls))
	for _, args := range []string{
		`{"object":"person","id":"` + personID + `","fields":{"ownerId":"` + otherID + `"}}`,
	} {
		if _, err := runWrite(t, false, args); !asInvalidOK(err) || strings.Contains(err.Error(), "workspaceMember") || strings.Contains(err.Error(), "owner") {
			t.Errorf("err = %v", err)
		}
	}
	// With only person bound the company is outside the targets.
	if _, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"companyId":"`+otherID+`"}}`, "object/person"); !asInvalidOK(err) ||
		strings.Contains(err.Error(), "company") {
		t.Errorf("unreachable target: err = %v", err)
	}
	if len(writes(*calls)) != before {
		t.Errorf("a refused relation reached the provider: %+v", writes(*calls))
	}
}

func TestRecordsWriteRefusesWhatTheSchemaOrTheTypeDoesNotAllow(t *testing.T) {
	for name, fields := range map[string]string{
		"text as number":      `"city":5`,
		"number as text":      `"age":"5"`,
		"integer as fraction": `"age":1.5`,
		"boolean as text":     `"vip":"true"`,
		"bad date":            `"birthday":"2026-13-40"`,
		"date with time":      `"birthday":"2026-01-01T00:00:00Z"`,
		"bad date-time":       `"lastContact":"yesterday"`,
		"enum outside":        `"status":"` + writeCanary + `"`,
		"multi enum outside":  `"tags":["ALPHA","` + writeCanary + `"]`,
		"multi as text":       `"tags":"ALPHA"`,
		"unknown subfield":    `"name":{"firstName":"A","` + writeCanary + `":"x"}`,
		"subfield type":       `"salary":{"amountMicros":"1","currencyCode":"EUR"}`,
		"nested bad type":     `"emails":{"additionalEmails":[5]}`,
		"nested unknown":      `"linkedinLink":{"secondaryLinks":[{"href":"x"}]}`,
		"composite as text":   `"name":"` + writeCanary + `"`,
		"rich text blocknote": `"bio":{"blocknote":"[]","markdown":"x"}`,
		"rich text only bn":   `"bio":{"blocknote":"[]"}`,
		"rich text as text":   `"bio":"` + writeCanary + `"`,
		"relation not uuid":   `"companyId":"` + writeCanary + `"`,
		"system id":           `"id":"` + otherID + `"`,
		"system created":      `"createdAt":"2026-01-01T00:00:00Z"`,
		"system actor":        `"createdBy":{"source":"API"}`,
		"system position":     `"position":1`,
		"unknown field":       `"` + writeCanary + `":"x"`,
		"response-only field": `"readOnly":"` + writeCanary + `"`,
		"free json":           `"extra":{"a":"` + writeCanary + `"}`,
		"files":               `"attachments":[{"fileId":"` + otherID + `"}]`,
		"plain text list":     `"nicknames":["` + writeCanary + `"]`,
		"null":                `"city":null`,
		"scalar relation":     `"companyId":5`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, createdAnswer("update"))
			_, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{`+fields+`}}`)
			if !asInvalidOK(err) {
				t.Fatalf("err = %v, want invalid-request", err)
			}
			if strings.Contains(err.Error(), writeCanary) || strings.Contains(err.Error(), "city") ||
				strings.Contains(err.Error(), "readOnly") || strings.Contains(err.Error(), "status") {
				t.Errorf("the refusal names a field or value: %v", err)
			}
			if len(writes(*calls)) != 0 {
				t.Errorf("a writing request followed: %+v", writes(*calls))
			}
			for _, call := range *calls {
				if call.URI != schemaPath {
					t.Errorf("request %s beyond the schema", call.URI)
				}
			}
		})
	}
}

func TestRecordsWriteChecksTheShapeOfTheOperation(t *testing.T) {
	calls := serveWrites(t, http.StatusOK, createdAnswer("create"))
	// locked exists in the create schema only.
	if _, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"locked":"x"}}`); !asInvalidOK(err) {
		t.Errorf("update of a field outside the update schema: err = %v", err)
	}
	if _, err := runWrite(t, true, `{"object":"person","fields":{`+coveredCreate+`,"locked":"x"}}`); err != nil {
		t.Errorf("create with a field of the create schema: err = %v", err)
	}
	// A create without a required field is refused: name, and company through companyId.
	for _, fields := range []string{`"companyId":"` + otherID + `"`, `"name":{"firstName":"A"}`, `"city":"x"`} {
		n := len(writes(*calls))
		_, err := runWrite(t, true, `{"object":"person","fields":{`+fields+`}}`)
		if !asInvalidOK(err) || strings.Contains(err.Error(), "name") || strings.Contains(err.Error(), "company") {
			t.Errorf("create with %s: err = %v", fields, err)
		}
		if len(writes(*calls)) != n {
			t.Errorf("a refused create reached the provider")
		}
	}
}

func TestRecordsWriteRefusesUnreachableObjectsBeforeSecretAccess(t *testing.T) {
	refuse(t)
	for _, tt := range []struct {
		object  string
		targets []string
	}{
		{"workspaceMember", nil}, {"noteTarget", nil}, {"taskTarget", nil}, {"person", []string{"object/rocket"}}, {"../x", nil},
	} {
		for _, create := range []bool{true, false} {
			handler := invokeRecordsUpdate
			if create {
				handler = invokeRecordsCreate
			}
			args := `{"object":"` + tt.object + `","id":"` + personID + `","fields":{"name":"x"}}`
			_, err := handler(context.Background(), targetConnection(tt.targets...), countingResolver(t), &redact.Redactor{}, json.RawMessage(args))
			if !asInvalidOK(err) || strings.Contains(err.Error(), tt.object) {
				t.Errorf("%q create=%v: err = %v", tt.object, create, err)
			}
		}
	}
}

func TestRecordsWriteChecksArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	many := make([]string, 101)
	for i := range many {
		many[i] = `"f` + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":1`
	}
	long := strings.Repeat("x", maxValueString+1)
	for name, args := range map[string]string{
		"update without fields": `{"object":"person","id":"` + personID + `","fields":{}}`,
		"bad id":                `{"object":"person","id":"../` + strings.Repeat("a", 33) + `","fields":{"city":"x"}}`,
		"empty id":              `{"object":"person","id":"","fields":{"city":"x"}}`,
		"101 fields":            `{"object":"person","id":"` + personID + `","fields":{` + strings.Join(many, ",") + `}}`,
		"string over 64 KiB":    `{"object":"person","id":"` + personID + `","fields":{"city":"` + long + `"}}`,
		"request over 1 MiB":    `{"object":"person","id":"` + personID + `","fields":{"city":"` + strings.Repeat("x", 1<<20) + `"}}`,
		"bad field name":        `{"object":"person","id":"` + personID + `","fields":{"a.b":"x"}}`,
		"system field":          `{"object":"person","id":"` + personID + `","fields":{"deletedAt":"2026-01-01T00:00:00Z"}}`,
	} {
		_, err := invokeRecordsUpdate(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{}, json.RawMessage(args))
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !asInvalidOK(err) && classOf(err) != provider.ClassProviderError {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// 100 fields pass the local bound; the schema decides afterwards.
	if _, err := newRecordWrite(targetConnection(), "update record", json.RawMessage(`{"object":"person","id":"`+personID+`","fields":{`+strings.Join(many[:100], ",")+`}}`), false); err != nil {
		t.Errorf("100 fields: %v", err)
	}
}

func TestRecordsWriteBoundsTheBodyAfterTheSchema(t *testing.T) {
	// Many strings of 64 KiB stay below the per-value bound and pass the argument size, but not the body size.
	calls := serveWrites(t, http.StatusOK, createdAnswer("update"))
	chunk := strings.Repeat("x", maxValueString)
	var list []string
	for i := 0; i < 17; i++ {
		list = append(list, `"`+chunk+`"`)
	}
	_, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"emails":{"additionalEmails":[`+strings.Join(list, ",")+`]}}}`)
	if err == nil {
		t.Fatal("a body over 1 MiB was accepted")
	}
	if len(writes(*calls)) != 0 {
		t.Errorf("a writing request followed")
	}
}

func TestRecordsWriteChecksTheAnsweredRecord(t *testing.T) {
	for name, answer := range map[string]string{
		"other record": strings.Replace(createdAnswer("update"), personID, personTwoID, 1),
		"no record":    `{"data":{}}`,
		"wrong key":    strings.Replace(createdAnswer("update"), "updatePerson", "createPerson", 1),
		"no id":        `{"data":{"updatePerson":{"city":"x"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, answer)
			_, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"city":"x"}}`)
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "read the record before repeating") {
				t.Errorf("err = %v", err)
			}
			if len(writes(*calls)) != 1 {
				t.Errorf("writes = %d", len(writes(*calls)))
			}
		})
	}
	t.Run("create answer without a valid id", func(t *testing.T) {
		serveWrites(t, http.StatusCreated, `{"data":{"createPerson":{"id":"nope"}}}`)
		_, err := runWrite(t, true, `{"object":"person","fields":{`+coveredCreate+`}}`)
		if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRecordsWriteNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error":   {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":        {status: 504},
		"unreadable":     {status: 200, body: `not json ` + bodyCanary},
		"timeout":        {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection end": {fail: errors.New(bodyCanary)},
	} {
		for _, create := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				writesSeen := 0
				serve(t, func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == schemaPath {
						return jsonResponse(http.StatusOK, writeSchema), nil
					}
					writesSeen++
					if tt.fail != nil {
						return nil, tt.fail
					}
					return jsonResponse(tt.status, tt.body), nil
				})
				stubLimiter(t, cloudKey)
				args := `{"object":"person","id":"` + personID + `","fields":{"city":"x"}}`
				if create {
					args = `{"object":"person","fields":{` + coveredCreate + `}}`
				}
				_, err := runWrite(t, create, args)
				if err == nil || writesSeen != 1 {
					t.Fatalf("err = %v, writing requests = %d, want one", err, writesSeen)
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("provider text in the error: %v", err)
				}
				var failure *provider.Error
				mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
				if mayHave && create && !strings.Contains(err.Error(), "duplicate") {
					t.Errorf("a create names no duplicate danger: %v", err)
				}
				if mayHave && !create && !strings.Contains(err.Error(), "read the record before repeating") {
					t.Errorf("an update names no uncertainty: %v", err)
				}
			})
		}
	}
}

func TestRecordsWritePermissionNamesTheRoleOnly(t *testing.T) {
	serveWrites(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	_, err := runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"city":"x"}}`)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "role") || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		serveWrites(t, status, `{"messages":["`+bodyCanary+writeCanary+`"]}`)
		_, err = runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"city":"`+writeCanary+`"}}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), writeCanary) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestRecordsWriteDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		{recordsCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{recordsUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idempotency, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: "twentycrm-record-data"}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", tt.d.ID, tt.d.Risk)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := strings.Contains(strings.Join(profile.Tools, " "), "records.create")
		if (profile.ID == "read") == has {
			t.Errorf("profile %s holds the record writes: %v", profile.ID, has)
		}
	}
	for _, d := range reg.Provider(Provider) {
		if (d.ID == recordsCreate.ID || d.ID == recordsUpdate.ID) && d.Group != recordsGroup {
			t.Errorf("%s group = %q", d.ID, d.Group)
		}
	}
}

// A relation the generator writes as oneOf is a relation for the reads as well: it stays out of a record.
func TestCatalogReadsOneOfRelationsAndWriteShapes(t *testing.T) {
	var document openAPIJSON
	if err := json.Unmarshal([]byte(writeSchema), &document); err != nil {
		t.Fatal(err)
	}
	object := parseCatalog(&document).objects["person"]
	fields := map[string]catalogField{}
	for _, field := range object.Fields {
		fields[field.Name] = field
	}
	if company := fields["company"]; !company.Reference || company.Relation != "company" || company.Creatable || company.Writable {
		t.Errorf("company = %+v", company)
	}
	if locked := fields["locked"]; !locked.Creatable || locked.Writable || locked.CreateShape == nil || locked.UpdateShape != nil {
		t.Errorf("locked = %+v", locked)
	}
	if tags := fields["tags"]; tags.UpdateShape == nil || tags.UpdateShape.Items == nil || len(tags.UpdateShape.Items.Enum) != 2 {
		t.Errorf("tags = %+v", tags.UpdateShape)
	}
}
