package n8n

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// assertOutputDeclared fails when a member of the tool output out is missing from the output schema or, on
// the top level and in the entries of a top-level list, from the fields; and when a schema member of the
// top level or of those entries has no field. A top-level list is named by its entry members, as Fields do.
// Deeper levels are checked against the schema only. It returns the number of members seen, so a caller can
// guard against an answer that carries too little.
func assertOutputDeclared(t *testing.T, toolID string, out []byte) int {
	t.Helper()
	descriptor, _, ok := registry(t).Lookup(toolID)
	if !ok {
		t.Fatalf("%s is not registered", toolID)
	}
	var schema map[string]any
	if err := json.Unmarshal(descriptor.OutputSchema, &schema); err != nil {
		t.Fatalf("%s: output schema unreadable: %v", toolID, err)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatalf("%s: output is not an object: %v", toolID, err)
	}
	fields := map[string]bool{}
	for _, field := range descriptor.Fields {
		fields[field.Name] = true
	}
	seen := 0
	declared := map[string]bool{}
	var walk func(path string, value map[string]any, schema map[string]any, depth int)
	walk = func(path string, value map[string]any, schema map[string]any, depth int) {
		properties, _ := schema["properties"].(map[string]any)
		for name, member := range value {
			seen++
			full := path + name
			property, ok := properties[name].(map[string]any)
			if !ok {
				t.Errorf("%s: %s is not in the output schema", toolID, full)
				continue
			}
			entry := entrySchema(property)
			// A top-level container may be documented by its entry members alone.
			if !(depth == 0 && entry != nil) {
				declared[name] = true
				if depth <= 1 && !fields[name] {
					t.Errorf("%s: %s is not in fields", toolID, full)
				}
			}
			if entry == nil {
				continue
			}
			switch typed := member.(type) {
			case map[string]any:
				walk(full+".", typed, entry, depth+1)
			case []any:
				for _, item := range typed {
					if object, ok := item.(map[string]any); ok {
						walk(full+"[].", object, entry, depth+1)
					}
				}
			}
		}
	}
	walk("", value, schema, 0)
	var unnamed []string
	for _, field := range descriptor.Fields {
		if !declared[field.Name] && !hasSchemaMember(schema, field.Name) {
			unnamed = append(unnamed, field.Name)
		}
	}
	sort.Strings(unnamed)
	if len(unnamed) > 0 {
		t.Errorf("%s: fields without schema member: %s", toolID, strings.Join(unnamed, ", "))
	}
	// Every member of the top level and of the entries of a top-level list needs a field.
	properties, _ := schema["properties"].(map[string]any)
	var undocumented []string
	for name, member := range properties {
		property, _ := member.(map[string]any)
		entry := entrySchema(property)
		if entry == nil {
			if !fields[name] {
				undocumented = append(undocumented, name)
			}
			continue
		}
		if _, isList := property["items"]; !isList {
			continue
		}
		entryProperties, _ := entry["properties"].(map[string]any)
		for entryName := range entryProperties {
			if !fields[entryName] {
				undocumented = append(undocumented, name+"[]."+entryName)
			}
		}
	}
	sort.Strings(undocumented)
	if len(undocumented) > 0 {
		t.Errorf("%s: schema members without field: %s", toolID, strings.Join(undocumented, ", "))
	}
	return seen
}

func hasSchemaMember(schema map[string]any, name string) bool {
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties[name]; ok {
		return true
	}
	for _, property := range properties {
		node, _ := property.(map[string]any)
		members, _ := entrySchema(node)["properties"].(map[string]any)
		if _, ok := members[name]; ok {
			return true
		}
	}
	return false
}

// entrySchema returns the schema of the entries of a list of objects or of an object with declared members,
// and nil for any other node, which is a leaf as far as the fields are concerned.
func entrySchema(node map[string]any) map[string]any {
	if items, ok := node["items"].(map[string]any); ok {
		node = items
	}
	if _, ok := node["properties"]; ok {
		return node
	}
	return nil
}

// richProject carries every member of n8n's projectPublicDto.
const richProject = `{"id":"` + ownProject + `","name":"Marketing","type":"team",` +
	`"icon":{"type":"emoji","value":"rocket","color":"#ff0000"},"description":"Workflows the marketing team owns.",` +
	`"customTelemetryTags":[{"key":"team","value":"mkt"},{"key":"cost","value":"42"}],` +
	`"creatorId":"f9a2cbb8-0b1e-4b64-9c1c-0d5f5f1f2a3b","createdAt":"2026-01-01T00:00:00.000Z",` +
	`"updatedAt":"2026-01-02T00:00:00.000Z"}`

// richExecution carries every non-deprecated member n8n documents for a listed execution; richExecutionDetail
// adds the members only the single read documents. Sizes are integers beyond float64's exact range.
const richExecution = `"id":1,"finished":true,"mode":"manual","retryOf":"7","retrySuccessId":"9",` +
	`"status":"success","startedAt":"2026-01-01T00:00:00.000Z","stoppedAt":"2026-01-01T00:00:05.000Z",` +
	`"workflowId":"` + ownWorkflow + `","waitTill":"2026-01-02T00:00:00.000Z","storedAt":"s3",` +
	`"jsonSizeBytes":9007199254740993,"workflowVersionId":"ver-1","dataTooLargeToDisplay":true`

const richExecutionDetail = `{` + richExecution + `,"createdAt":"2025-12-31T23:59:59.000Z","deletedAt":null,` +
	`"tracingContext":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","tracestate":"k=v"},` +
	`"deduplicationKey":"dedup-1","binaryDataSizeBytes":9007199254740995,"usedPrivateCredentials":false}`

// richDataColumn carries every member n8n documents for a data table column; richDataTable carries every
// member of a data table including such a column.
const richDataColumn = `{"id":"c1","name":"email","type":"string","index":0,"dataTableId":"` + ownTable + `",` +
	`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}`

const richDataTable = `{"id":"` + ownTable + `","name":"Own","projectId":"` + ownProject + `",` +
	`"columns":[` + richDataColumn + `],"createdAt":"2026-01-01T00:00:00.000Z",` +
	`"updatedAt":"2026-01-02T00:00:00.000Z","sizeBytes":8192}`

// outputFieldCases lists the read tools whose every output member must be declared. A tool is added with an
// answer that carries every member n8n documents for it, so a member the mapper adds without declaring it
// fails here. minimum guards against an answer that carries too little.
var outputFieldCases = []struct {
	tool, connection, arguments, path, body string
	minimum                                 int
}{
	{projectsList.ID, "open", `{}`, "/projects", `{"data":[` + richProject + `],"nextCursor":"c"}`, 15},
	{membersList.ID, "pdelete", `{"project_id":"` + ownProject + `"}`, "/projects/" + ownProject + "/users",
		`{"data":[{"id":"u1","email":"a@example.org","firstName":"Ada","lastName":"Lovelace","role":"project:admin",` +
			`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}],"nextCursor":"c"}`, 11},
	{variablesList.ID, "open", `{}`, "/variables", `{"data":[{"id":"` + ownVariable + `","key":"K","value":"v",` +
		`"type":"string","project":` + richProject + `}],"nextCursor":"c"}`, 27},
	{executionsList.ID, "open", `{}`, "/executions", `{"data":[{` + richExecution + `}],"nextCursor":"c"}`, 18},
	{workflowsList.ID, "open", `{}`, "/workflows", `{"data":[` + richWorkflow() + `],"nextCursor":"c"}`, 100},
	{workflowsGet.ID, "open", `{"workflow_id":"` + ownWorkflow + `"}`, "/workflows/" + ownWorkflow, richWorkflow(), 100},
	{executionsGet.ID, "open", `{"execution_id":1}`, "/executions/1", richExecutionDetail, 21},
	{dataTablesList.ID, "open", `{}`, "/data-tables", `{"data":[` + richDataTable + `],"nextCursor":"c"}`, 19},
	{credentialsList.ID, "open", `{}`, "/credentials", `{"data":[` + richSharedCredential() + `],"nextCursor":"c"}`, 17},
	{credentialsGet.ID, "open", `{"credential_id":"` + ownCredential + `"}`, "/credentials/" + ownCredential,
		`{"id":"` + ownCredential + `","name":"n","type":"t","isManaged":false,"isGlobal":false,"isResolvable":false,` +
			`"resolvableAllowFallback":false,"resolverId":"r","description":"d","createdAt":"c","updatedAt":"u"}`, 11},
	{usersList.ID, "pdelete-open", `{}`, "/users", `{"data":[` + userBody + `],"nextCursor":"c"}`, 12},
	{usersGet.ID, "pdelete-open", `{"user_id":"` + ownUser + `"}`, "/users/" + ownUser, userBody, 8},
	{sourceControlStatus.ID, "open", `{"direction":"pull"}`, "/source-control/status",
		`{"data":[` + richStatusFile + `]}`, 25},
	{auditGenerate.ID, "open", `{}`, "/audit", auditBody(richAuditSettings, `[{"name":"1.2.3","nodes":[{"name":"N"}],`+
		`"createdAt":"c","description":"d","documentationUrl":"u","hasBreakingChange":true,"hasSecurityFix":true,`+
		`"hasSecurityIssue":false,"securityIssueFixVersion":"1.2.4"}]`), 20},
	{dataTablesGet.ID, "open", `{"data_table_id":"` + ownTable + `"}`, "/data-tables/" + ownTable, richDataTable, 14},
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	for _, tt := range outputFieldCases {
		t.Run(tt.tool, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != apiPath+tt.path {
					t.Errorf("unexpected request to %s", r.URL.Path)
				}
				return jsonResponse(200, tt.body), nil
			})
			out, err := env.invoke(tt.tool, tt.connection, tt.arguments)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if seen := assertOutputDeclared(t, tt.tool, []byte(out)); seen < tt.minimum {
				t.Errorf("checked %d members, want at least %d: %s", seen, tt.minimum, out)
			}
		})
	}
}

func TestDataColumnsListOutputMembersAreInSchemaAndFields(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, columnServer(t, &mutations))
	out, err := env.invoke(dataColumnsList.ID, "project", `{"data_table_id":"`+ownTable+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if seen := assertOutputDeclared(t, dataColumnsList.ID, []byte(out)); seen < 9 {
		t.Errorf("checked %d members, want at least 9: %s", seen, out)
	}
}
