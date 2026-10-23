package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The issue fields carry the creation time and the identifiers of GitHub's IssueFieldCommon, and their
// options the priority and the identifiers of IssueFieldSingleSelectOption; a field without them keeps its
// former answer.
func TestIssueFieldsListCarriesTheFullField(t *testing.T) {
	nodes := `[{"id":"IF_1","name":"Priority","description":"How urgent","dataType":"SINGLE_SELECT",` +
		`"visibility":"ALL","createdAt":"2026-02-01T00:00:00Z","fullDatabaseId":"4503599627370497","options":[` +
		`{"id":"IFO_1","name":"P1","description":"Now","color":"RED","priority":1,"databaseId":11,` +
		`"fullDatabaseId":"4503599627370498"},{"id":"IFO_2","name":"P2","description":null,"color":"YELLOW"}]},` +
		`{"id":"IF_2","name":"Component","description":null,"dataType":"TEXT","visibility":"ORG_ONLY"}]`
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		requests := f.recorded()
		if !strings.Contains(requests[len(requests)-1].document, "issueFields(first") {
			return false
		}
		fmt.Fprintf(w, `{"data":{"organization":{"issueFields":{"pageInfo":{"hasNextPage":false,"endCursor":""},`+
			`"nodes":%s}}}}`, nodes)
		return true
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, issueFieldsList.ID, "open", `{"organization":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(result), `{"issue_fields":[{"id":"IF_1","name":"Priority","description":"How urgent",`+
		`"data_type":"single_select","visibility":"all","created_at":"2026-02-01T00:00:00Z",`+
		`"full_database_id":"4503599627370497","options":[{"id":"IFO_1","name":"P1","description":"Now","color":"red",`+
		`"priority":1,"database_id":11,"full_database_id":"4503599627370498"},{"id":"IFO_2","name":"P2","color":"yellow"}]},`+
		`{"id":"IF_2","name":"Component","data_type":"text","visibility":"org_only"}],"has_more":false,`+
		`"organization":"orgs/octo-org"}`)

	first, _ := json.Marshal(decodeAny(t, result).(map[string]any)["issue_fields"].([]any)[0])
	assertSelectionMatchesOutput(t, issueFieldSelection, first, entrySchema(t, issueFieldsList.OutputSchema, "issue_fields"),
		nil, nil)
}
