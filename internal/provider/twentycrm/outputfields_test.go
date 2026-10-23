package twentycrm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// outputCase is one tool whose rich answer must be described completely by its descriptor. Later tools join
// the table with a descriptor, a handler, arguments, and a body builder; body receives the members that
// follow the data of the page, so the same body serves with and without a total.
type outputCase struct {
	descriptor capability.Descriptor
	handler    capability.Handler
	args       string
	body       func(tail string) string
	// total is true for a list that reports total_count.
	total bool
}

// outputDocuments replaces the workspace document served for the schema path of a tool that needs one.
var outputDocuments = map[string]string{objectsGet.ID: objectsOutputDocument, activitytargetsList.ID: linkSchema,
	attachmentsList.ID: attachmentSchema}

// richCompany is a company record in the shape of the Twenty company object with a custom field.
const richCompany = `{"id":"` + companyID + `","name":"Bike","domainName":{"primaryLinkUrl":"https://bike.example",` +
	`"primaryLinkLabel":"bike.example","secondaryLinks":[{"url":"https://shop.example","label":"Shop"}]},` +
	`"linkedinLink":{"primaryLinkUrl":"https://linkedin.example/bike","primaryLinkLabel":"","secondaryLinks":[]},` +
	`"annualRevenue":{"amountMicros":9007199254740993,"currencyCode":"CHF"},` +
	`"address":{"addressStreet1":"Hauptstrasse 1","addressStreet2":"","addressCity":"Zuerich","addressPostcode":"8000",` +
	`"addressState":"","addressCountry":"CH","addressLat":47.37,"addressLng":8.54},"addressOld":"alt",` +
	`"position":2,"searchVector":"'bike':1","deletedAt":null,` +
	`"createdBy":{"source":"API","workspaceMemberId":"` + memberID + `","name":"Ada","context":{}},` +
	`"updatedBy":{"source":"MANUAL","workspaceMemberId":null,"name":"Bob","context":{}},` +
	`"accountOwnerId":"` + memberID + `","employees":12,"idealCustomerProfile":true,"xLink":{"primaryLinkUrl":"https://x.example"},` +
	`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}`

const outputPageInfo = `"pageInfo":{"hasNextPage":true,"endCursor":"cursorABC"}`

const uuidPageInfo = `"pageInfo":{"hasNextPage":true,"endCursor":"` + metaOtherID + `"}`

func outputCases() []outputCase {
	return []outputCase{
		{recordsList, invokeRecordsList, `{"object":"person"}`, func(tail string) string {
			return `{"data":{"people":[{"id":"` + personID + `","name":{"firstName":"Ada","lastName":"L"},"city":"Berlin",` +
				`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}]}` + tail + `,` + outputPageInfo + `}`
		}, true},
		{recordsSearch, invokeRecordsSearch, `{"object":"person","conditions":[{"field":"city","operator":"eq","value":"Berlin"}]}`,
			func(tail string) string {
				return `{"data":{"people":[{"id":"` + personID + `","city":"Berlin","createdAt":"2026-01-01T00:00:00.000Z",` +
					`"updatedAt":"2026-01-02T00:00:00.000Z"}]}` + tail + `,` + outputPageInfo + `}`
			}, true},
		{recordsSearchAll, invokeRecordsSearchAll, `{"text":"Ada","objects":["person"]}`, func(tail string) string {
			return `{"data":{"search":{"edges":[{"node":{"recordId":"` + hitOne + `","objectNameSingular":"person",` +
				`"objectLabelSingular":"Person","label":"Ada","imageUrl":"https://img.example/a.png","tsRank":0.1,"tsRankCD":0.2}}],` +
				`"pageInfo":{"hasNextPage":true,"endCursor":"c3VjaA=="}}}}`
		}, false},
		{companiesList, invokeCompaniesList, `{}`, func(tail string) string {
			return `{"data":{"companies":[` + richCompany + `]}` + tail + `,` + outputPageInfo + `}`
		}, true},
		{companiesGet, invokeCompaniesGet, `{"id":"` + companyID + `"}`, func(tail string) string {
			return `{"data":{"company":` + richCompany + `}}`
		}, false},
		{workflowsList, invokeWorkflowsList, `{}`, func(tail string) string {
			return `{"data":{"workflows":[` + fullWorkflow + `]}` + tail + `,` + outputPageInfo + `}`
		}, true},
		{workflowsGet, invokeWorkflowsGet, `{"id":"` + wfID + `"}`, func(tail string) string {
			return `{"data":{"workflow":` + fullWorkflow + `,"workflowVersions":[{"id":"` + wfVersion + `","workflowId":"` + wfID +
				`","coreWorkflowVersionId":"` + coreVersionID + `","position":3,"name":"v1","status":"ACTIVE",` +
				`"trigger":{"type":"MANUAL"},"steps":[{"type":"CODE"}],"createdAt":"2026-01-01T00:00:00.000Z",` +
				`"updatedAt":"2026-01-02T00:00:00.000Z"}]},` + outputPageInfo + `}`
		}, false},
		{workflowRunsList, invokeWorkflowRunsList, `{"workflow_id":"` + wfID + `"}`, func(tail string) string {
			return `{"data":{"workflowRuns":[{"id":"` + wfRun + `","workflowId":"` + wfID + `","coreWorkflowId":"` + coreWfID +
				`","workflowVersionId":"` + wfVersion + `","coreWorkflowVersionId":"` + coreVersionID + `","position":2,` +
				`"name":"Onboarding","status":"FAILED","createdBy":` + actorAda + `,"updatedBy":` + actorAPI + `,` +
				`"enqueuedAt":"2026-01-01T00:00:00.000Z","startedAt":"2026-01-01T00:00:01.000Z",` +
				`"endedAt":"2026-01-01T00:00:02.000Z","createdAt":"2026-01-01T00:00:00.000Z",` +
				`"updatedAt":"2026-01-01T00:00:03.000Z"}]}` + tail + `,` + outputPageInfo + `}`
		}, true},
		{membersList, invokeMembersList, `{}`, func(tail string) string {
			return `{"data":{"workspaceMembers":[` + fullMember + `]}` + tail + `,` + outputPageInfo + `}`
		}, true},
		{membersGet, invokeMembersGet, `{"id":"` + memberID + `"}`, func(tail string) string {
			return `{"data":{"workspaceMember":` + fullMember + `}}`
		}, false},
		{activitytargetsList, invokeActivityTargetsList, `{"activity":"note","activity_id":"` + noteID + `"}`, func(tail string) string {
			return `{"data":{"noteTargets":[` + linkRow(`"targetPersonId":"`+recordID+`","createdAt":"2026-01-01T00:00:00.000Z",`+
				`"updatedAt":"2026-01-02T00:00:00.000Z"`) + `]},` + outputPageInfo + `}`
		}, false},
		{attachmentsList, invokeAttachmentsList, `{"object":"person","record_id":"` + attPersonID + `"}`, func(tail string) string {
			return `{"data":{"attachments":[` + withActors(attachmentRowJSON(personTarget(), fileEntry(goodURL()))) + `]},` + outputPageInfo + `}`
		}, false},
		{viewsList, invokeViewsList, `{}`, func(tail string) string {
			return `{"data":[` + fullViewJSON(viewID) + `],` + uuidPageInfo + `}`
		}, false},
		{viewsGet, invokeViewsGet, `{"id":"` + viewID + `"}`, func(tail string) string { return fullViewJSON(viewID) }, false},
		{pageLayoutsList, invokePageLayoutsList, `{}`, func(tail string) string {
			return `{"data":[` + fullLayoutJSON(layoutID, ``) + `],` + uuidPageInfo + `}`
		}, false},
		{pageLayoutsGet, invokePageLayoutsGet, `{"id":"` + layoutID + `"}`, func(tail string) string {
			return fullLayoutJSON(layoutID, ``)
		}, false},
		{metaObjectsList, invokeMetaObjectsList, `{}`, func(tail string) string {
			return `{"data":[` + metaRichObjectJSON + `]` + tail + `,"pageInfo":{"hasNextPage":true,"endCursor":"` + metaOtherID + `"}}`
		}, true},
		{metaObjectsGet, invokeMetaObjectsGet, `{"id":"` + metaObjID + `"}`, func(tail string) string {
			return metaRichObjectJSON
		}, false},
		{descriptor: objectsGet, handler: invokeObjectsGet, args: `{"object":"person"}`, body: func(string) string { return `{}` }},
		{metaFieldsGet, invokeMetaFieldsGet, `{"id":"` + metaFieldID + `"}`, func(tail string) string {
			return metaRichFieldJSON
		}, false},
	}
}

// outputOf serves a body and returns the output of the tool as a generic object.
func outputOf(t *testing.T, tc outputCase, body string) (map[string]any, error) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			if document, ok := outputDocuments[tc.descriptor.ID]; ok {
				return jsonResponse(http.StatusOK, document), nil
			}
			return jsonResponse(http.StatusOK, searchSchema), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	raw, err := runTool(t, tc.handler, tc.args)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

type outputSchema struct {
	Properties map[string]struct {
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
	} `json:"properties"`
}

// Every member a tool outputs, at the top level and in the elements of its lists, is described by the output
// schema and by Fields, and every member of the schema has a Fields entry.
func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	for _, tc := range outputCases() {
		t.Run(tc.descriptor.ID, func(t *testing.T) {
			out, err := outputOf(t, tc, tc.body(`,"totalCount":3`))
			if err != nil {
				t.Fatal(err)
			}
			var schema outputSchema
			if err := json.Unmarshal(tc.descriptor.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			fields := map[string]bool{}
			for _, field := range tc.descriptor.Fields {
				fields[field.Name] = true
			}
			var problems []string
			for key, value := range out {
				property, ok := schema.Properties[key]
				if !ok {
					problems = append(problems, key+" (schema)")
				}
				if !fields[key] {
					problems = append(problems, key+" (fields)")
				}
				items, _ := value.([]any)
				if len(items) == 0 {
					continue
				}
				element, _ := items[0].(map[string]any)
				if len(property.Items.Properties) == 0 {
					continue // elements of a free list declare no members
				}
				for member := range element {
					if _, ok := property.Items.Properties[member]; !ok {
						problems = append(problems, key+"[]."+member+" (schema)")
					}
				}
			}
			for key := range schema.Properties {
				if !fields[key] {
					problems = append(problems, key+" (schema member without fields)")
				}
			}
			sort.Strings(problems)
			if len(problems) > 0 {
				t.Errorf("not described: %v", problems)
			}
		})
	}
}

func TestListToolsReportTheTotalCountExactly(t *testing.T) {
	for _, tc := range outputCases() {
		if !tc.total {
			continue
		}
		t.Run(tc.descriptor.ID, func(t *testing.T) {
			for name, want := range map[string]any{"3": json.Number("3"), "0": json.Number("0"), "9007199254740993": json.Number("9007199254740993")} {
				out, err := outputOfNumbers(t, tc, tc.body(`,"totalCount":`+name))
				if err != nil {
					t.Fatal(err)
				}
				if got, ok := out["total_count"]; !ok || got != want {
					t.Errorf("totalCount %s: total_count = %v (present %v)", name, got, ok)
				}
			}
			for name, tail := range map[string]string{"absent": ``, "null": `,"totalCount":null`} {
				out, err := outputOf(t, tc, tc.body(tail))
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := out["total_count"]; ok {
					t.Errorf("%s totalCount still reports total_count: %v", name, out)
				}
			}
			for _, bad := range []string{`-1`, `1.5`, `"3"`, `true`, `{}`} {
				if _, err := outputOf(t, tc, tc.body(`,"totalCount":`+bad)); err == nil {
					t.Errorf("totalCount %s was accepted", bad)
				}
			}
		})
	}
}

// outputOfNumbers decodes numbers as json.Number, so a large count is compared without a float.
func outputOfNumbers(t *testing.T, tc outputCase, body string) (map[string]any, error) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			if document, ok := outputDocuments[tc.descriptor.ID]; ok {
				return jsonResponse(http.StatusOK, document), nil
			}
			return jsonResponse(http.StatusOK, searchSchema), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	raw, err := runTool(t, tc.handler, tc.args)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}
