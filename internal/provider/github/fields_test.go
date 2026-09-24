package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeFields are the data types and names of the fields of project 7 that a field change may address.
var fakeFields = map[string][2]string{"F_status": {"SINGLE_SELECT", "Status"}, "F_prio": {"SINGLE_SELECT", "Priority"},
	"F_tags": {"MULTI_SELECT", "Tags"}, "F_sprint": {"ITERATION", "Sprint"}, "F_note": {"TEXT", "Note"}}

// fieldSchema answers the field mutations and reports whether it did. A created or changed field is answered
// as GitHub does: with the options and iterations the request sent, a new option or iteration with a new
// identifier.
func (f *fakeGitHub) fieldSchema(w http.ResponseWriter, document string, variables map[string]any) bool {
	var id, name, dataType string
	var options, iteration any
	switch {
	case strings.Contains(document, "createProjectV2Field("):
		if variables["project"] != projectID {
			fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":["field"],"message":"no project"}]}`)
			return true
		}
		id, name, dataType = "F_new", fmt.Sprint(variables["name"]), fmt.Sprint(variables["type"])
		options, iteration = variables["single"], variables["iteration"]
		if dataType == "MULTI_SELECT" {
			options = variables["multi"]
		}
	case strings.Contains(document, "updateProjectV2Field("):
		id = fmt.Sprint(variables["field"])
		known := fakeFields[id]
		dataType, name = known[0], known[1]
		if value, ok := variables["name"].(string); ok {
			name = value
		}
		options, iteration = variables["singleSelectOptions"], variables["iterationConfiguration"]
		if dataType == "MULTI_SELECT" {
			options = variables["multiSelectOptions"]
		}
	case strings.Contains(document, "deleteProjectV2Field("):
		fmt.Fprintf(w, `{"data":{"field":{"projectV2Field":{"id":%q}}}}`, variables["field"])
		return true
	default:
		return false
	}
	// The answer copies what the request sent, so the recorded request stays as it was.
	withID := func(entry any, id string) map[string]any {
		out := map[string]any{"id": id}
		for key, value := range entry.(map[string]any) {
			out[key] = value
		}
		return out
	}
	node := map[string]any{"id": id, "name": name, "dataType": dataType}
	if list, ok := options.([]any); ok {
		answered := make([]any, len(list))
		for i, entry := range list {
			answered[i] = withID(entry, fmt.Sprintf("N_%d", i))
		}
		key := "options"
		if dataType == "MULTI_SELECT" {
			key = "multiSelectOptions"
		}
		node[key] = answered
	}
	if configuration, ok := iteration.(map[string]any); ok {
		iterations := configuration["iterations"].([]any)
		answered := make([]any, len(iterations))
		for i, entry := range iterations {
			answered[i] = withID(entry, fmt.Sprintf("NI_%d", i))
		}
		node["configuration"] = map[string]any{"duration": configuration["duration"], "startDay": 1,
			"iterations": answered, "completedIterations": []any{}}
	}
	answer, _ := json.Marshal(map[string]any{"data": map[string]any{"field": map[string]any{"projectV2Field": node}}})
	_, _ = w.Write(answer)
	return true
}

var fieldTools = []string{fieldsList.ID, fieldsCreate.ID, fieldsUpdate.ID, fieldOptionsDelete.ID,
	iterationsReplace.ID, fieldsDelete.ID}

// fieldsConfig holds the connections the field tools meet: one that may create and update, an administrator
// that lists every field tool and may delete, the same without the delete permission, and one with every
// permission but without a tools list.
func fieldsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	add := func(name string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: planningTargets,
			Permissions: permissions, Tools: tools}
	}
	add("fields", changes, nil)
	add("admin", config.Permissions(), fieldTools)
	add("nodelete", changes, fieldTools)
	add("unlisted", config.Permissions(), nil)
	return cfg
}

// Every refusal the target, the permissions, the tools list, the confirmation, or the arguments settle ends
// before a secret is read and before GitHub is contacted; losing values needs the effect delete and the
// tool's name in the tools list.
func TestFieldSchemaRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), fieldsConfig(base), resolver(red, &reads), red)

	invalid := &application.InvalidRequestError{}
	unsupported := &capability.UnsupportedError{}
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"a list outside the targets", fieldsList.ID, "fields", `{"project":"orgs/octo-org/projects/8"}`, false,
			invalid, "project is outside the targets"},
		{"an unconfirmed create", fieldsCreate.ID, "fields", `{"name":"Size","type":"number"}`, false,
			unconfirmed, ""},
		{"a create outside the targets", fieldsCreate.ID, "fields",
			`{"project":"users/octocat/projects/3","name":"Size","type":"number"}`, true, invalid, "outside the targets"},
		{"an unknown color", fieldsCreate.ID, "fields",
			`{"name":"Size","type":"single_select","options":[{"name":"S","color":"black"}]}`, true, invalid, "color"},
		{"a select field without options", fieldsCreate.ID, "fields", `{"name":"Size","type":"single_select"}`, true,
			invalid, "needs options"},
		{"options of a text field", fieldsCreate.ID, "fields",
			`{"name":"Size","type":"text","options":[{"name":"S"}]}`, true, invalid, "options belong to"},
		{"an iteration field without settings", fieldsCreate.ID, "fields", `{"name":"Sprint 2","type":"iteration"}`,
			true, invalid, "needs iteration"},
		{"an impossible start date", fieldsCreate.ID, "fields",
			`{"name":"Cycle","type":"iteration","iteration":{"start_date":"2026-02-30","duration":7}}`, true, invalid,
			"YYYY-MM-DD"},
		{"a renamed option in a new field", fieldsCreate.ID, "fields",
			`{"name":"Size","type":"single_select","options":[{"name":"S","new_name":"M"}]}`, true, invalid, "new_name"},
		{"an option named twice", fieldsCreate.ID, "fields",
			`{"name":"Size","type":"multi_select","options":[{"name":"S"},{"name":"s"}]}`, true, invalid,
			"more than once"},
		{"an update without a change", fieldsUpdate.ID, "fields", `{"field":"Priority"}`, true, invalid,
			"name at least one"},
		{"an order naming an option twice", fieldsUpdate.ID, "fields", `{"field":"Priority","order":["P1","p1"]}`,
			true, invalid, "more than once"},
		{"iterations through the update", fieldsUpdate.ID, "fields",
			`{"field":"Sprint","iteration":{"duration":7}}`, true, invalid, ""},
		{"an option removal without the delete permission", fieldOptionsDelete.ID, "fields",
			`{"field":"Tags","options":["UI"]}`, true, unsupported, ""},
		{"an option removal with delete tools but without the permission", fieldOptionsDelete.ID, "nodelete",
			`{"field":"Tags","options":["UI"]}`, true, unsupported, ""},
		{"an option removal without a tools list", fieldOptionsDelete.ID, "unlisted",
			`{"field":"Tags","options":["UI"]}`, true, unsupported, ""},
		{"an iteration change without the delete permission", iterationsReplace.ID, "fields",
			`{"field":"Sprint","duration":7}`, true, unsupported, ""},
		{"an iteration change without a tools list", iterationsReplace.ID, "unlisted",
			`{"field":"Sprint","duration":7}`, true, unsupported, ""},
		{"an iteration changed and removed", iterationsReplace.ID, "admin",
			`{"field":"Sprint","iterations":[{"title":"Sprint 5","duration":7}],"remove":["sprint 5"]}`, true, invalid,
			"both iterations and remove"},
		{"an iteration change without a change", iterationsReplace.ID, "admin", `{"field":"Sprint"}`, true, invalid,
			"name at least one"},
		{"an unconfirmed field delete", fieldsDelete.ID, "admin", `{"field":"Note"}`, false, unconfirmed, ""},
		{"a field delete without a tools list", fieldsDelete.ID, "unlisted", `{"field":"Note"}`, true, unsupported,
			""},
		{"a field delete outside the targets", fieldsDelete.ID, "admin",
			`{"project":"orgs/octo-org/projects/8","field":"Note"}`, true, invalid, "outside the targets"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, tt.confirmed)
			if want := fmt.Sprintf("%T", tt.want); fmt.Sprintf("%T", err) != want ||
				!strings.Contains(fmt.Sprint(err), tt.message) {
				t.Errorf("err = %T %v, want %s with %q", err, err, want, tt.message)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

func useToday(t *testing.T, date string) {
	t.Helper()
	day, _ := time.Parse(time.DateOnly, date)
	previous := today
	today = func() time.Time { return day }
	t.Cleanup(func() { today = previous })
}

// The field list reads the whole schema in one query: built-in fields are marked, options carry color and
// description, and iterations appear in start order with their state.
func TestFieldSchemaListsEveryField(t *testing.T) {
	useToday(t, "2026-09-24")
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), fieldsConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, fieldsList.ID, "fields", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Fields  []ProjectField `json:"fields"`
		Project string         `json:"project"`
	}
	if err := json.Unmarshal(result, &schema); err != nil || schema.Project != projectTarget {
		t.Fatalf("result = %s, %v", result, err)
	}
	byName := map[string]ProjectField{}
	for _, field := range schema.Fields {
		byName[field.Name] = field
	}
	if len(schema.Fields) != 15 || schema.Fields[0].Name != "Title" || !byName["Title"].BuiltIn ||
		byName["Title"].Type != "title" || !byName["Status"].BuiltIn || !byName["Assignees"].BuiltIn ||
		byName["Priority"].BuiltIn || byName["Note"].Type != "text" {
		t.Errorf("fields = %+v", schema.Fields)
	}
	if got := fmt.Sprint(byName["Tags"]); got != "{Tags multi_select false [{UI blue Interface} {API gray }] <nil>}" {
		t.Errorf("Tags = %s", got)
	}
	if got := fmt.Sprint(byName["Status"].Options); got != "[{Todo green Not started} {In progress yellow } {Done purple }]" {
		t.Errorf("Status options = %s", got)
	}
	sprint := byName["Sprint"].Iteration
	if sprint == nil || sprint.Duration != 14 || sprint.StartDay != "monday" ||
		fmt.Sprint(sprint.Iterations) != "[{Sprint 4 2026-09-07 14 completed} {Sprint 5 2026-09-21 14 current} "+
			"{Sprint 6 2026-10-05 14 planned}]" {
		t.Errorf("Sprint = %+v", sprint)
	}
	if queries, mutations, _ := split(f.recorded()); len(queries) != 1 || len(mutations) != 0 {
		t.Errorf("requests = %d queries, %d mutations; want one query", len(queries), len(mutations))
	}
}

// Every field change resolves the field model in one query and sends exactly one mutation, with every value
// as a variable. An option that stays keeps its identifier, so the values items hold stay with it.
func TestFieldChangesSendOneMutationEach(t *testing.T) {
	useToday(t, "2026-09-24")
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), fieldsConfig(base), resolver(red, nil), red)

	for _, tt := range []struct {
		name, operation, arguments string
		document                   []string
		absent                     []string
		variables                  map[string]string
		result                     string
	}{
		{"create a single-select field", fieldsCreate.ID,
			`{"name":"Size","type":"single_select","options":[{"name":"S","color":"green","description":"small"},{"name":"L"}]}`,
			[]string{"createProjectV2Field(input:{projectId:$project,dataType:$type,name:$name,"}, nil,
			map[string]string{"project": projectID, "type": "SINGLE_SELECT", "name": "Size", "multi": "<nil>",
				"iteration": "<nil>", "single": "[map[color:GREEN description:small name:S] " +
					"map[color:GRAY description: name:L]]"},
			`{"field":{"built_in":false,"name":"Size","options":[{"color":"green","description":"small","name":"S"},` +
				`{"color":"gray","description":"","name":"L"}],"type":"single_select"},"project":"orgs/octo-org/projects/7"}`},
		{"create a multi-select field", fieldsCreate.ID,
			`{"name":"Areas","type":"multi_select","options":[{"name":"Docs","color":"pink"}]}`, nil, nil,
			map[string]string{"type": "MULTI_SELECT", "single": "<nil>",
				"multi": "[map[color:PINK description: name:Docs]]"}, `"type":"multi_select"`},
		{"create a text field", fieldsCreate.ID, `{"name":"Owner","type":"text"}`, nil, nil,
			map[string]string{"type": "TEXT", "single": "<nil>", "multi": "<nil>", "iteration": "<nil>"},
			`{"field":{"built_in":false,"name":"Owner","type":"text"},"project":"orgs/octo-org/projects/7"}`},
		{"create an iteration field", fieldsCreate.ID,
			`{"name":"Cycle","type":"iteration","iteration":{"start_date":"2026-10-05","duration":14,` +
				`"iterations":[{"title":"C2","start_date":"2026-10-19","duration":7},{"title":"C1","start_date":"2026-10-05"}]}}`,
			nil, nil, map[string]string{"type": "ITERATION", "iteration": "map[duration:14 iterations:[" +
				"map[duration:14 startDate:2026-10-05 title:C1] map[duration:7 startDate:2026-10-19 title:C2]] " +
				"startDate:2026-10-05]"},
			`"iterations":[{"duration":14,"start_date":"2026-10-05","state":"planned","title":"C1"},`},
		{"rename, add, and order options", fieldsUpdate.ID,
			`{"field":"priority","options":[{"name":"p1","new_name":"High"},{"name":"P2","color":"yellow"}],` +
				`"order":["P2","high"]}`,
			[]string{"updateProjectV2Field(input:{fieldId:$field,singleSelectOptions:$singleSelectOptions})"},
			[]string{"name:", "multiSelectOptions:", "iterationConfiguration:"},
			map[string]string{"field": "F_prio", "singleSelectOptions": "[map[color:YELLOW description: name:P2] " +
				"map[color:RED description:Urgent id:O_p1 name:High]]"}, `"name":"Priority"`},
		{"add an option of a multi-select field", fieldsUpdate.ID, `{"field":"Tags","options":[{"name":"Docs"}]}`,
			[]string{"multiSelectOptions:$multiSelectOptions"}, []string{"singleSelectOptions:"},
			map[string]string{"field": "F_tags", "multiSelectOptions": "[map[color:BLUE description:Interface " +
				"id:M_ui name:UI] map[color:GRAY description: id:M_api name:API] map[color:GRAY description: name:Docs]]"},
			`"name":"Docs"`},
		{"maintain the built-in Status", fieldsUpdate.ID,
			`{"field":"Status","options":[{"name":"Done","description":"Finished"}]}`, nil, nil,
			map[string]string{"singleSelectOptions": "[map[color:GREEN description:Not started id:O_todo name:Todo] " +
				"map[color:YELLOW description: id:O_progress name:In progress] " +
				"map[color:PURPLE description:Finished id:O_done name:Done]]"}, `"built_in":true`},
		{"rename a field", fieldsUpdate.ID, `{"field":"note","name":"Notes"}`,
			[]string{"updateProjectV2Field(input:{fieldId:$field,name:$name})", "$name:String!"}, nil,
			map[string]string{"field": "F_note", "name": "Notes"}, `{"field":{"built_in":false,"name":"Notes"`},
		{"remove an option", fieldOptionsDelete.ID, `{"field":"Tags","options":["ui"]}`,
			[]string{"multiSelectOptions:$multiSelectOptions"}, nil,
			map[string]string{"field": "F_tags", "multiSelectOptions": "[map[color:GRAY description: id:M_api name:API]]"},
			`"removed":["UI"]`},
		{"replace iterations", iterationsReplace.ID,
			`{"field":"Sprint","iterations":[{"title":"sprint 6","duration":7},{"title":"Sprint 7",` +
				`"start_date":"2026-10-19"}],"remove":["Sprint 4"]}`,
			[]string{"iterationConfiguration:$iterationConfiguration"}, nil,
			map[string]string{"field": "F_sprint", "iterationConfiguration": "map[duration:14 iterations:[" +
				"map[duration:14 startDate:2026-09-21 title:Sprint 5] map[duration:7 startDate:2026-10-05 title:Sprint 6] " +
				"map[duration:14 startDate:2026-10-19 title:Sprint 7]] startDate:2026-09-21]"},
			`"removed":["Sprint 4"]`},
		{"move the iterations", iterationsReplace.ID, `{"field":"Sprint","start_date":"2026-09-01","duration":7}`,
			nil, nil, map[string]string{"iterationConfiguration": "map[duration:7 iterations:[" +
				"map[duration:14 startDate:2026-09-07 title:Sprint 4] map[duration:14 startDate:2026-09-21 title:Sprint 5] " +
				"map[duration:14 startDate:2026-10-05 title:Sprint 6]] startDate:2026-09-01]"}, `"removed":[]`},
		{"delete a field", fieldsDelete.ID, `{"field":"NOTE"}`,
			[]string{"deleteProjectV2Field(input:{fieldId:$field})"}, nil, map[string]string{"field": "F_note"},
			`{"deleted":true,"name":"Note","project":"orgs/octo-org/projects/7"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			result, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if err != nil || !strings.Contains(string(result), tt.result) {
				t.Fatalf("result = %s, %v; want %s", result, err, tt.result)
			}
			queries, mutations, rest := split(f.recorded()[before:])
			if len(queries) != 1 || len(mutations) != 1 || len(rest) != 0 {
				t.Fatalf("requests = %d queries, %d mutations, %v REST; want 1, 1, none", len(queries),
					len(mutations), rest)
			}
			for _, want := range tt.document {
				if !strings.Contains(mutations[0].document, want) {
					t.Errorf("mutation %s lacks %s", mutations[0].document, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(mutations[0].document, absent) {
					t.Errorf("mutation %s names %s", mutations[0].document, absent)
				}
			}
			for name, want := range tt.variables {
				if got := fmt.Sprint(mutations[0].variables[name]); got != want {
					t.Errorf("variable %s = %s, want %s", name, got, want)
				}
			}
			if strings.ContainsAny(strings.TrimPrefix(mutations[0].document, "mutation"), `"`) {
				t.Errorf("a value reached the document: %s", mutations[0].document)
			}
		})
	}
}

// A change the field model refuses ends after its one query, before any mutation: an unknown field, a
// built-in field, an option or an iteration the field lacks, an order that leaves an option out, or a
// removal that would leave a select field without options.
func TestFieldChangesAreResolvedBeforeTheMutation(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), fieldsConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ name, operation, arguments, message string }{
		{"an unknown field", fieldsUpdate.ID, `{"field":"Owner","name":"x"}`, "its fields are Title, Status,"},
		{"a built-in field", fieldsUpdate.ID, `{"field":"Title","name":"Name"}`, "built in and cannot be changed"},
		{"a new name of Status", fieldsUpdate.ID, `{"field":"status","name":"Stage"}`, "keeps its name"},
		{"options of a text field", fieldsUpdate.ID, `{"field":"Note","options":[{"name":"x"}]}`, "has no options"},
		{"an incomplete order", fieldsUpdate.ID, `{"field":"Status","order":["Done","Todo"]}`,
			"every option of field Status exactly once: Todo, In progress, Done"},
		{"an order with a stranger", fieldsUpdate.ID, `{"field":"Priority","order":["P9"]}`, "exactly once: P1"},
		{"a rename of a missing option", fieldsUpdate.ID, `{"field":"Priority","options":[{"name":"P9","new_name":"P0"}]}`,
			"its options are P1"},
		{"two options of one name", fieldsUpdate.ID,
			`{"field":"Tags","options":[{"name":"UI","new_name":"api"}]}`, "two options of one name"},
		{"a name another field has", fieldsUpdate.ID, `{"field":"Note","name":"priority"}`, "already holds a field"},
		{"a created name the project has", fieldsCreate.ID, `{"name":"sprint","type":"text"}`, "already holds a field"},
		{"a removal of every option", fieldOptionsDelete.ID, `{"field":"Priority","options":["P1"]}`,
			"keeps at least one option"},
		{"a removal of a missing option", fieldOptionsDelete.ID, `{"field":"Tags","options":["UI","Docs"]}`,
			"its options are UI, API"},
		{"an option removal of an iteration field", fieldOptionsDelete.ID, `{"field":"Sprint","options":["x"]}`,
			"has no options"},
		{"iterations of a select field", iterationsReplace.ID, `{"field":"Priority","duration":7}`,
			"is no iteration field"},
		{"a removal of a missing iteration", iterationsReplace.ID, `{"field":"Sprint","remove":["Sprint 9"]}`,
			"its iterations are Sprint 4, Sprint 5, Sprint 6"},
		{"a new iteration without a start", iterationsReplace.ID,
			`{"field":"Sprint","iterations":[{"title":"Sprint 9","duration":7}]}`, "needs start_date"},
		{"two iterations of one title", iterationsReplace.ID,
			`{"field":"Sprint","iterations":[{"title":"Sprint 5","new_title":"sprint 6"}]}`, "two iterations of one title"},
		{"a delete of Status", fieldsDelete.ID, `{"field":"Status"}`, "built in and cannot be deleted"},
		{"a delete of Title", fieldsDelete.ID, `{"field":"title"}`, "built in and cannot be deleted"},
		{"a delete of an unknown field", fieldsDelete.ID, `{"field":"Owner"}`, "is not a field of this project"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message) {
				t.Errorf("err = %v, want an invalid request with %q", err, tt.message)
			}
			if queries, mutations, _ := split(f.recorded()[before:]); len(queries) != 1 || len(mutations) != 0 {
				t.Errorf("requests = %d queries, %d mutations; want one query and no change", len(queries),
					len(mutations))
			}
		})
	}
}

// A field change whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearFieldChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{fieldsCreate.ID, `{"name":"Size","type":"number"}`},
		{fieldsUpdate.ID, `{"field":"Priority","options":[{"name":"P2"}]}`},
		{fieldOptionsDelete.ID, `{"field":"Tags","options":["UI"]}`},
		{iterationsReplace.ID, `{"field":"Sprint","remove":["Sprint 4"]}`},
		{fieldsDelete.ID, `{"field":"Note"}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			f := &fakeGitHub{}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if len(f.recorded()) == 0 || !strings.HasPrefix(f.recorded()[len(f.recorded())-1].document, "mutation") {
					return false
				}
				w.WriteHeader(http.StatusBadGateway)
				return true
			}
			base := serve(t, f)
			red := &redact.Redactor{}
			core := application.New(registry(t), fieldsConfig(base), resolver(red, nil), red)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Fatalf("err = %v, want an unclear outcome", err)
			}
			if _, mutations, _ := split(f.recorded()); len(mutations) != 1 {
				t.Errorf("mutations = %d, want exactly one", len(mutations))
			}
		})
	}
}

// A multi-select field takes a list of option names, which resolves to option identifiers; an empty list
// clears it. A list is refused for every other field, and a name the field lacks is refused before a change.
func TestMultiSelectValues(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)

	planning, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, `{"Tags":["api","UI","API"]}`))
	if err != nil || !planning.Complete {
		t.Fatalf("UpdateItemFields() = %+v, %v", planning, err)
	}
	_, mutations, _ := split(f.recorded())
	if len(mutations) != 1 || fmt.Sprint(mutations[0].variables["v0"]) != "map[multiSelectOptionIds:[M_api M_ui]]" ||
		!strings.Contains(mutations[0].document, "f0:updateProjectV2ItemFieldValue(") {
		t.Fatalf("mutations = %+v", mutations)
	}

	before := len(f.recorded())
	if _, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, `{"Tags":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, mutations, _ = split(f.recorded()[before:])
	if len(mutations) != 1 || !strings.Contains(mutations[0].document, "f0:clearProjectV2ItemFieldValue(") {
		t.Errorf("an empty list = %+v, want the field cleared", mutations)
	}

	for name, document := range map[string]string{
		"an unknown option":         `{"Tags":["Docs"]}`,
		"a single name":             `{"Tags":"UI"}`,
		"a list for a text":         `{"Note":["x"]}`,
		"a list for a single":       `{"Priority":["P1"]}`,
		"an empty list for a text":  `{"Note":[]}`,
		"a list for a number":       `{"Estimate":["3"]}`,
		"a list for an iteration":   `{"Sprint":["Sprint 5"]}`,
		"a list without names only": `{"Tags":["UI",3]}`,
	} {
		before := len(f.recorded())
		if _, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, document)); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
		if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 0 {
			t.Errorf("%s: a change was sent", name)
		}
	}
	names := make([]string, 51)
	for i := range names {
		names[i] = fmt.Sprintf("%q", fmt.Sprintf("O%d", i))
	}
	before = len(f.recorded())
	if _, err := c.UpdateItemFields(context.Background(), "PVTI_item00",
		fields(t, `{"Tags":[`+strings.Join(names, ",")+`]}`)); !isInvalidRequest(err) || len(f.recorded()) != before {
		t.Errorf("51 options = %v, want a refusal before GitHub is contacted", err)
	}

	item, err := c.GetItem(context.Background(), "PVTI_item00")
	if err != nil || fmt.Sprint(item.Fields["Tags"]) != "[UI API]" {
		t.Errorf("GetItem() = %+v, %v; want the multi-select value as its option names", item, err)
	}
}
