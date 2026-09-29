package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeIssueFieldOption is one option of a fake single-select or multi-select issue field.
type fakeIssueFieldOption struct {
	id, name, description, color string
}

// fakeIssueFieldDef is one organization issue field octo-org declares, read directly or inherited by
// repository octo-org/example.
type fakeIssueFieldDef struct {
	id, name, description, dataType, visibility string
	options                                     []fakeIssueFieldOption
}

func (d fakeIssueFieldDef) json() string {
	description := "null"
	if d.description != "" {
		description = fmt.Sprintf("%q", d.description)
	}
	options := ""
	if len(d.options) > 0 {
		items := make([]string, 0, len(d.options))
		for _, option := range d.options {
			optionDescription := "null"
			if option.description != "" {
				optionDescription = fmt.Sprintf("%q", option.description)
			}
			items = append(items, fmt.Sprintf(`{"id":%q,"name":%q,"description":%s,"color":%q}`,
				option.id, option.name, optionDescription, option.color))
		}
		options = `,"options":[` + strings.Join(items, ",") + `]`
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"description":%s,"dataType":%q,"visibility":%q%s}`,
		d.id, d.name, description, d.dataType, d.visibility, options)
}

// issueFieldsPage answers github.issuefields.list's own GraphQL query, of a repository or an organization
// directly, with f.issueFields; it never paginates in these tests, since a single page is enough to satisfy
// the contract.
func (f *fakeGitHub) issueFieldsPage(w http.ResponseWriter, document string, variables map[string]any) bool {
	if !strings.Contains(document, "issueFields(first") {
		return false
	}
	nodes := make([]string, 0, len(f.issueFields))
	for _, field := range f.issueFields {
		nodes = append(nodes, field.json())
	}
	connection := `{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[` + strings.Join(nodes, ",") + `]}`
	if strings.Contains(document, "organization(login:$owner)") {
		fmt.Fprintf(w, `{"data":{"organization":{"issueFields":%s}}}`, connection)
	} else {
		fmt.Fprintf(w, `{"data":{"repository":{"issueFields":%s}}}`, connection)
	}
	return true
}

// issueFieldsChange answers the setIssueFieldValue mutation github.issuefields.set sends: every field of one
// call is recorded as a whole, the way GitHub applies it as a single mutation.
func (f *fakeGitHub) issueFieldsChange(w http.ResponseWriter, document string, variables map[string]any) bool {
	if !strings.Contains(document, "setIssueFieldValue(input") {
		return false
	}
	issueID, _ := variables["issueId"].(string)
	fields, _ := variables["fields"].([]any)
	if f.issueFieldValues == nil {
		f.issueFieldValues = map[string]map[string]any{}
	}
	for _, raw := range fields {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fieldID, _ := entry["fieldId"].(string)
		if fieldID == "" {
			continue
		}
		if del, ok := entry["delete"].(bool); ok && del {
			delete(f.issueFieldValues, fieldID)
			continue
		}
		f.issueFieldValues[fieldID] = entry
	}
	fmt.Fprintf(w, `{"data":{"setIssueFieldValue":{"issue":{"id":%q,`+
		`"url":"https://github.com/octo-org/example/issues/42"}}}}`, issueID)
	return true
}

// github.issuefields.list reads the issue fields of a repository or, with organization instead, directly of
// an organization an explicit connection allows.
func TestIssueFieldsListSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{issueFields: []fakeIssueFieldDef{
		{id: "IF_text", name: "Component", dataType: "TEXT", visibility: "ALL"},
		{id: "IF_select", name: "Priority", dataType: "SINGLE_SELECT", visibility: "ORG_ONLY", options: []fakeIssueFieldOption{
			{id: "IFO_1", name: "P1", color: "RED"}, {id: "IFO_2", name: "P2", color: "YELLOW"},
		}},
	}}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, nil), red)

	byRepo, err := invoke(t, core, issueFieldsList.ID, "open", `{"repository":"octo-org/example"}`, false)
	if err != nil || !strings.Contains(string(byRepo), `"id":"IF_text"`) ||
		!strings.Contains(string(byRepo), `"data_type":"text"`) || !strings.Contains(string(byRepo), `"visibility":"all"`) ||
		!strings.Contains(string(byRepo), `"id":"IFO_1"`) || !strings.Contains(string(byRepo), `"color":"red"`) ||
		!strings.Contains(string(byRepo), `"repository":"octo-org/example"`) {
		t.Fatalf("by repository = %s, %v", byRepo, err)
	}

	byOrg, err := invoke(t, core, issueFieldsList.ID, "open", `{"organization":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(byOrg), `"id":"IF_select"`) ||
		!strings.Contains(string(byOrg), `"data_type":"single_select"`) ||
		!strings.Contains(string(byOrg), `"organization":"orgs/octo-org"`) {
		t.Fatalf("by organization = %s, %v", byOrg, err)
	}

	// A repository target of the organization allows the organization-wide call as well, and a connection
	// without targets allows either mode.
	byOwnerRepo, err := invoke(t, core, issueFieldsList.ID, "owner", `{"organization":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(byOwnerRepo), `"id":"IF_text"`) {
		t.Fatalf("organization allowed through a repository target = %s, %v", byOwnerRepo, err)
	}
}

// Exactly one of repository or organization is required, never both or neither, and a project-only
// connection refuses the organization-wide mode, the engere Auslegung of the target rule; every refusal
// settles before a secret is read and before GitHub is contacted.
func TestIssueFieldsListRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, connection, arguments, message string }{
		{"neither repository nor organization", "open", `{}`, "give exactly one"},
		{"both repository and organization", "open",
			`{"repository":"octo-org/example","organization":"orgs/octo-org"}`, "give exactly one"},
		{"a user organization", "open", `{"organization":"users/octocat"}`, "an organization"},
		{"an organization outside the targets", "single", `{"organization":"orgs/other"}`, "outside the targets"},
		{"a project-only connection", "planning", `{"organization":"orgs/octo-org"}`, "outside the targets"},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, issueFieldsList.ID, tt.connection, tt.arguments, false)
		if !isInvalidRequest(err) || (tt.message != "" && !strings.Contains(err.Error(), tt.message)) {
			t.Errorf("%s: err = %v, want an invalid request naming %q", tt.name, err, tt.message)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}

// github.issuefields.set writes every field value of one call as a single mutation and needs its own
// confirmation.
func TestIssueFieldsSetSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, issueFieldsSet.ID, "single", `{"number":42,"fields":[`+
		`{"field_id":"IF_text","text_value":"Backend"},{"field_id":"IF_select","single_select_option_id":"IFO_1"}]}`,
		true)
	if err != nil || !strings.Contains(string(result), `"updated":2`) ||
		!strings.Contains(string(result), `"url":"https://github.com/octo-org/example/issues/42"`) ||
		!strings.Contains(string(result), `"repository":"octo-org/example"`) {
		t.Fatalf("%s = %s, %v", issueFieldsSet.ID, result, err)
	}
	if f.issueFieldValues["IF_text"]["textValue"] != "Backend" ||
		f.issueFieldValues["IF_select"]["singleSelectOptionId"] != "IFO_1" {
		t.Errorf("recorded field values = %+v, want both values sent", f.issueFieldValues)
	}

	deleted, err := invoke(t, core, issueFieldsSet.ID, "single",
		`{"number":42,"fields":[{"field_id":"IF_text","delete":true}]}`, true)
	if err != nil || !strings.Contains(string(deleted), `"updated":1`) {
		t.Fatalf("delete = %s, %v", deleted, err)
	}
	if _, ok := f.issueFieldValues["IF_text"]; ok {
		t.Errorf("deleted field value still recorded: %+v", f.issueFieldValues)
	}

	unconfirmed := &application.ConfirmationRequiredError{}
	before := len(f.recorded())
	if _, err := invoke(t, core, issueFieldsSet.ID, "single",
		`{"number":42,"fields":[{"field_id":"IF_text","text_value":"Backend"}]}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("without --confirm = %v, want confirmation-required", err)
	}
	if len(f.recorded()) != before {
		t.Error("an unconfirmed set reached GitHub")
	}
}

// Every field value needs exactly one of its value properties or delete: true, and fields must name at least
// one entry; every refusal settles before GitHub is contacted.
func TestIssueFieldsSetChecksEveryFieldValue(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ name, arguments string }{
		{"no fields", `{"number":42,"fields":[]}`},
		{"no value and no delete", `{"number":42,"fields":[{"field_id":"IF_text"}]}`},
		{"two values", `{"number":42,"fields":[{"field_id":"IF_text","text_value":"a","number_value":1}]}`},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, issueFieldsSet.ID, "single", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s reached GitHub", tt.name)
		}
	}
}
