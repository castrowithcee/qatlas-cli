package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeStatusUpdates are the status updates of project 7, newest first: one with every setting and one without
// any.
var fakeStatusUpdates = []map[string]any{
	{"id": "SU_1", "status": "ON_TRACK", "startDate": "2026-09-01", "targetDate": "2026-12-18",
		"body": "Going well", "createdAt": "2026-09-20T00:00:00Z", "updatedAt": "2026-09-21T00:00:00Z",
		"creator": map[string]any{"login": "octocat"}},
	{"id": "SU_2", "status": nil, "startDate": nil, "targetDate": nil, "body": nil,
		"createdAt": "2026-09-01T00:00:00Z", "updatedAt": "2026-09-01T00:00:00Z", "creator": nil},
}

// statusChange answers the status update list, the lookup of one status update, and the status update
// mutations, and reports whether it did. SU_foreign belongs to another project; any other unknown identifier
// is no node. A created or changed status update answers with the settings the request sent.
func (f *fakeGitHub) statusChange(w http.ResponseWriter, document string, variables map[string]any) bool {
	var answer any
	switch {
	case strings.Contains(document, "statusUpdates(first"):
		if variables["owner"] != "octo-org" || variables["number"] != float64(7) {
			fmt.Fprint(w, `{"data":{"owner":{"projectV2":null}},"errors":[{"type":"NOT_FOUND",`+
				`"path":["owner","projectV2"],"message":"Could not resolve"}]}`)
			return true
		}
		start := 0
		if variables["after"] == "su-0" {
			start = 1
		}
		end := min(start+int(variables["first"].(float64)), len(fakeStatusUpdates))
		answer = map[string]any{"owner": map[string]any{"projectV2": map[string]any{"statusUpdates": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": end < len(fakeStatusUpdates),
				"endCursor": fmt.Sprintf("su-%d", end-1)}, "nodes": fakeStatusUpdates[start:end]}}}}
	case strings.Contains(document, "statusUpdate:node("):
		data := map[string]json.RawMessage{"owner": json.RawMessage(`{"projectV2":` + f.projectJSON() + `}`)}
		switch id := variables["statusUpdate"]; id {
		case "SU_1", "SU_2":
			data["statusUpdate"] = json.RawMessage(fmt.Sprintf(`{"id":%q,"project":{"id":%q}}`, id, projectID))
		case "SU_foreign":
			data["statusUpdate"] = json.RawMessage(`{"id":"SU_foreign","project":{"id":"` + foreignID + `"}}`)
		default:
			fmt.Fprint(w, `{"data":{"owner":{"projectV2":`+f.projectJSON()+`},"statusUpdate":null},`+
				`"errors":[{"type":"NOT_FOUND","path":["statusUpdate"],"message":"Could not resolve"}]}`)
			return true
		}
		answer = data
	case strings.Contains(document, "createProjectV2StatusUpdate("), strings.Contains(document, "updateProjectV2StatusUpdate("):
		id := variables["statusUpdate"]
		if id == nil {
			if variables["project"] != projectID {
				fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":["status"],"message":"no project"}]}`)
				return true
			}
			id = "SU_new"
		}
		answer = map[string]any{"status": map[string]any{"statusUpdate": map[string]any{"id": id,
			"status": variables["status"], "startDate": variables["start"], "targetDate": variables["target"],
			"body": variables["body"], "createdAt": "2026-09-25T00:00:00Z", "updatedAt": "2026-09-25T00:00:00Z",
			"creator": map[string]any{"login": "octocat"}}}}
	case strings.Contains(document, "deleteProjectV2StatusUpdate("):
		answer = map[string]any{"status": map[string]any{"deletedStatusUpdateId": variables["statusUpdate"]}}
	default:
		return false
	}
	data, _ := json.Marshal(map[string]any{"data": answer})
	_, _ = w.Write(data)
	return true
}

var statusTools = []string{statusList.ID, statusCreate.ID, statusUpdate.ID, statusDelete.ID}

// statusConfig holds the connections the status update tools meet: one that may create and update, an
// administrator that lists every status tool and may delete, the same without the delete permission, and one
// with every permission but without a tools list.
func statusConfig(base string) *config.Config {
	cfg := coreConfig(base)
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	add := func(name string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: planningTargets,
			Permissions: permissions, Tools: tools}
	}
	add("status", changes, nil)
	add("admin", config.Permissions(), statusTools)
	add("nodelete", changes, statusTools)
	add("unlisted", config.Permissions(), nil)
	return cfg
}

func statusCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), statusConfig(serve(t, f)), resolver(red, reads), red)
}

// Every refusal the target, the permissions, the tools list, the confirmation, the arguments, or the cursor
// settle ends before a secret is read and before GitHub is contacted; a delete needs the effect delete and the
// tool's name in the tools list.
func TestProjectStatusRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	core := statusCore(t, f, &reads)

	invalid := &application.InvalidRequestError{}
	unsupported := &capability.UnsupportedError{}
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"a list outside the targets", statusList.ID, "status", `{"project":"orgs/octo-org/projects/8"}`, false,
			invalid, "outside the targets"},
		{"a cursor of another list", statusList.ID, "status", `{"cursor":"` +
			encodeCursor(fingerprint("comments", "x"), "c") + `"}`, false, invalid, "not a next_cursor"},
		{"an unconfirmed create", statusCreate.ID, "status", `{"status":"on_track"}`, false, unconfirmed, ""},
		{"a create outside the targets", statusCreate.ID, "status",
			`{"project":"users/octocat/projects/3","status":"on_track"}`, true, invalid, "outside the targets"},
		{"a create without a setting", statusCreate.ID, "status", `{}`, true, invalid, "name at least one"},
		{"an unknown status", statusCreate.ID, "status", `{"status":"late"}`, true, invalid, ""},
		{"an impossible date", statusCreate.ID, "status", `{"start_date":"2026-02-30"}`, true, invalid,
			"start_date must be a date"},
		{"an empty date of a new status update", statusCreate.ID, "status", `{"target_date":""}`, true, invalid,
			"target_date must be a date"},
		{"an update without a change", statusUpdate.ID, "status", `{"status_update_id":"SU_1"}`, true, invalid,
			"name at least one"},
		{"a delete without the delete permission", statusDelete.ID, "status", `{"status_update_id":"SU_1"}`, true,
			unsupported, ""},
		{"a delete with the tools list but without the permission", statusDelete.ID, "nodelete",
			`{"status_update_id":"SU_1"}`, true, unsupported, ""},
		{"a delete without a tools list", statusDelete.ID, "unlisted", `{"status_update_id":"SU_1"}`, true,
			unsupported, ""},
		{"an unconfirmed delete", statusDelete.ID, "admin", `{"status_update_id":"SU_1"}`, false, unconfirmed, ""},
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

// The list reads one batch of status updates newest first in one query and continues with a cursor bound to
// the project.
func TestProjectStatusListPagesNewestFirst(t *testing.T) {
	f := &fakeGitHub{}
	core := statusCore(t, f, nil)

	result, err := invoke(t, core, statusList.ID, "status", `{"limit":1}`, false)
	var first StatusUpdateList
	if err != nil || json.Unmarshal(result, &first) != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if !strings.Contains(string(result), `"project":"orgs/octo-org/projects/7"`) || len(first.StatusUpdates) != 1 ||
		fmt.Sprint(first.StatusUpdates[0]) != "{SU_1 on_track 2026-09-01 2026-12-18 Going well octocat "+
			"2026-09-20T00:00:00Z 2026-09-21T00:00:00Z}" || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first batch = %s", result)
	}
	queries, _, _ := split(f.recorded())
	if len(queries) != 1 || !strings.Contains(queries[0].document,
		"statusUpdates(first:$first,after:$after,orderBy:{field:CREATED_AT,direction:DESC})") ||
		queries[0].variables["after"] != nil {
		t.Errorf("queries = %+v, want one query of the newest status updates", queries)
	}

	result, err = invoke(t, core, statusList.ID, "status", `{"cursor":"`+first.NextCursor+`"}`, false)
	var second StatusUpdateList
	if err != nil || json.Unmarshal(result, &second) != nil || len(second.StatusUpdates) != 1 || second.HasMore ||
		fmt.Sprint(second.StatusUpdates[0]) != "{SU_2      2026-09-01T00:00:00Z 2026-09-01T00:00:00Z}" {
		t.Fatalf("second batch = %s, %v", result, err)
	}
	if queries, _, _ := split(f.recorded()); queries[1].variables["after"] != "su-0" {
		t.Errorf("continuation = %+v", queries[1].variables)
	}
}

// Every status update change resolves the project, and the status update it changes, in one query and sends
// exactly one mutation with every value as a variable; an empty date of an update is sent as null.
func TestProjectStatusChangesSendOneMutationEach(t *testing.T) {
	f := &fakeGitHub{}
	core := statusCore(t, f, nil)

	for _, tt := range []struct {
		name, operation, arguments string
		document                   []string
		absent                     []string
		variables                  map[string]string
		result                     string
	}{
		{"post a complete status update", statusCreate.ID,
			`{"status":"at_risk","start_date":"2026-09-01","target_date":"2026-12-18","body":"Slipping"}`,
			[]string{"createProjectV2StatusUpdate(input:{projectId:$project,status:$status,startDate:$start," +
				"targetDate:$target,body:$body})", "$status:ProjectV2StatusUpdateStatus", "$start:Date"}, nil,
			map[string]string{"project": projectID, "status": "AT_RISK", "start": "2026-09-01",
				"target": "2026-12-18", "body": "Slipping"},
			`{"project":"orgs/octo-org/projects/7","status_update":{"author":"octocat","body":"Slipping",` +
				`"created_at":"2026-09-25T00:00:00Z","id":"SU_new","start_date":"2026-09-01","status":"at_risk",` +
				`"target_date":"2026-12-18","updated_at":"2026-09-25T00:00:00Z"}}`},
		{"post a body alone", statusCreate.ID, `{"body":"Kickoff"}`,
			[]string{"createProjectV2StatusUpdate(input:{projectId:$project,body:$body})"},
			[]string{"status:$status", "startDate:", "targetDate:"}, map[string]string{"body": "Kickoff"},
			`"status":""`},
		{"change the status and remove the target date", statusUpdate.ID,
			`{"status_update_id":"SU_1","status":"complete","target_date":""}`,
			[]string{"updateProjectV2StatusUpdate(input:{statusUpdateId:$statusUpdate,status:$status," +
				"targetDate:$target})"}, []string{"body:", "startDate:"},
			map[string]string{"statusUpdate": "SU_1", "status": "COMPLETE", "target": "<nil>"},
			`"target_date":""`},
		{"empty the body", statusUpdate.ID, `{"status_update_id":"SU_2","body":""}`,
			[]string{"updateProjectV2StatusUpdate(input:{statusUpdateId:$statusUpdate,body:$body})"}, nil,
			map[string]string{"statusUpdate": "SU_2", "body": ""}, `"id":"SU_2"`},
		{"delete a status update", statusDelete.ID, `{"status_update_id":"SU_2"}`,
			[]string{"deleteProjectV2StatusUpdate(input:{statusUpdateId:$statusUpdate})"}, nil,
			map[string]string{"statusUpdate": "SU_2"},
			`{"deleted":true,"project":"orgs/octo-org/projects/7","status_update_id":"SU_2"}`},
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

// A status update of another project, or one GitHub does not show, is refused after the one query, before
// any mutation.
func TestProjectStatusUpdatesOfAnotherProjectAreRefused(t *testing.T) {
	for _, tt := range []struct{ name, operation, arguments string }{
		{"an update of another project's status update", statusUpdate.ID,
			`{"status_update_id":"SU_foreign","status":"inactive"}`},
		{"a delete of an unknown status update", statusDelete.ID, `{"status_update_id":"SU_gone"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{}
			core := statusCore(t, f, nil)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			var providerErr *provider.Error
			if !errors.As(err, &providerErr) || providerErr.Class != provider.ClassNotFound ||
				!strings.Contains(providerErr.Message, "this status update in project orgs/octo-org/projects/7") {
				t.Errorf("err = %v, want not-found of the status update", err)
			}
			if queries, mutations, _ := split(f.recorded()); len(queries) != 1 || len(mutations) != 0 {
				t.Errorf("requests = %d queries, %d mutations; want one query and no change", len(queries),
					len(mutations))
			}
		})
	}
}

// A status update change whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearStatusChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{statusCreate.ID, `{"status":"on_track"}`},
		{statusUpdate.ID, `{"status_update_id":"SU_1","body":"x"}`},
		{statusDelete.ID, `{"status_update_id":"SU_1"}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			f := &fakeGitHub{}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(f.recorded()[len(f.recorded())-1].document, "mutation") {
					return false
				}
				w.WriteHeader(http.StatusBadGateway)
				return true
			}
			core := statusCore(t, f, nil)
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
