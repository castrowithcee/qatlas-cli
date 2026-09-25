package github

import (
	"encoding/json"
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

// itemChange answers the item and draft mutations and reports whether it did. A changed draft answers with
// the title it was sent, or its item's title, and the logins of the users it was assigned.
func (f *fakeGitHub) itemChange(w http.ResponseWriter, document string, variables map[string]any) bool {
	var answer any
	switch {
	case strings.Contains(document, "deleteProjectV2Item("):
		answer = map[string]any{"item": map[string]any{"deletedItemId": variables["item"]}}
	case strings.Contains(document, "unarchiveProjectV2Item("):
		answer = map[string]any{"item": map[string]any{"item": map[string]any{"id": variables["item"]}}}
	case strings.Contains(document, "updateProjectV2ItemPosition("):
		answer = map[string]any{"item": map[string]any{"clientMutationId": nil}}
	case strings.Contains(document, "updateProjectV2DraftIssue("):
		title := variables["title"]
		if title == nil {
			title = "Item 2"
		}
		logins := []any{}
		if ids, ok := variables["assignees"].([]any); ok {
			for _, id := range ids {
				logins = append(logins, map[string]any{"login": strings.TrimPrefix(fmt.Sprint(id), "U_")})
			}
		}
		answer = map[string]any{"draft": map[string]any{"draftIssue": map[string]any{"id": variables["draft"],
			"title": title, "assignees": map[string]any{"nodes": logins}}}}
	case strings.Contains(document, "convertProjectV2DraftIssueItemToIssue("):
		answer = map[string]any{"item": map[string]any{"item": map[string]any{"id": variables["item"],
			"content": map[string]any{"number": 102, "url": "https://github.com/octo-org/example/issues/102"}}}}
	default:
		return false
	}
	if _, ok := variables["project"]; ok && variables["project"] != projectID {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":["item"],"message":"no project"}]}`)
		return true
	}
	data, _ := json.Marshal(map[string]any{"data": answer})
	_, _ = w.Write(data)
	return true
}

var itemTools = []string{itemsDelete.ID, itemsUnarchive.ID, itemsMove.ID, draftsUpdate.ID, draftsConvert.ID}

// itemsConfig holds the connections the item tools meet: one that may create and update, an administrator
// that lists every item tool and may delete, the same without the delete permission, one with every
// permission but without a tools list, and one bound to the project alone.
func itemsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	add := func(name string, targets []string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: permissions, Tools: tools}
	}
	add("items", planningTargets, changes, nil)
	add("admin", planningTargets, config.Permissions(), itemTools)
	add("nodelete", planningTargets, changes, itemTools)
	add("unlisted", planningTargets, config.Permissions(), nil)
	add("project", []string{projectTarget}, changes, nil)
	return cfg
}

func itemsCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), itemsConfig(serve(t, f)), resolver(red, reads), red)
}

// Every refusal the target, the permissions, the tools list, the confirmation, or the arguments settle ends
// before a secret is read and before GitHub is contacted; a delete needs the effect delete and the tool's name
// in the tools list, and a conversion checks the repository beside the project.
func TestProjectItemRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	reads := 0
	core := itemsCore(t, f, &reads)

	invalid := &application.InvalidRequestError{}
	unsupported := &capability.UnsupportedError{}
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"a delete without the delete permission", itemsDelete.ID, "items", `{"item_id":"PVTI_item00"}`, true,
			unsupported, ""},
		{"a delete with the tools list but without the permission", itemsDelete.ID, "nodelete",
			`{"item_id":"PVTI_item00"}`, true, unsupported, ""},
		{"a delete without a tools list", itemsDelete.ID, "unlisted", `{"item_id":"PVTI_item00"}`, true,
			unsupported, ""},
		{"an unconfirmed delete", itemsDelete.ID, "admin", `{"item_id":"PVTI_item00"}`, false, unconfirmed, ""},
		{"an unconfirmed restore", itemsUnarchive.ID, "items", `{"item_id":"PVTI_item00"}`, false, unconfirmed, ""},
		{"an unconfirmed move", itemsMove.ID, "items", `{"item_id":"PVTI_item00"}`, false, unconfirmed, ""},
		{"an unconfirmed draft change", draftsUpdate.ID, "items", `{"item_id":"PVTI_item02","title":"x"}`, false,
			unconfirmed, ""},
		{"an unconfirmed conversion", draftsConvert.ID, "items", `{"item_id":"PVTI_item02"}`, false, unconfirmed, ""},
		{"a delete outside the targets", itemsDelete.ID, "admin",
			`{"item_id":"PVTI_item00","project":"orgs/octo-org/projects/8"}`, true, invalid, "outside the targets"},
		{"a move outside the targets", itemsMove.ID, "items",
			`{"item_id":"PVTI_item00","project":"users/octocat/projects/3"}`, true, invalid, "outside the targets"},
		{"a conversion into a repository outside the targets", draftsConvert.ID, "items",
			`{"item_id":"PVTI_item02","repository":"octo-org/other"}`, true, invalid, "outside the targets"},
		{"a conversion without a repository target", draftsConvert.ID, "project", `{"item_id":"PVTI_item02"}`, true,
			invalid, ""},
		{"a move after itself", itemsMove.ID, "items", `{"item_id":"PVTI_item00","after_id":"PVTI_item00"}`, true,
			invalid, "another item"},
		{"a draft change without a change", draftsUpdate.ID, "items", `{"item_id":"PVTI_item02"}`, true, invalid,
			"name at least one of title, body, or assignees"},
		{"a blank draft title", draftsUpdate.ID, "items", `{"item_id":"PVTI_item02","title":"  "}`, true, invalid,
			"title must hold"},
		{"a draft assignee that is no login", draftsUpdate.ID, "items",
			`{"item_id":"PVTI_item02","assignees":["no login"]}`, true, invalid, ""},
		{"draft labels", draftsUpdate.ID, "items", `{"item_id":"PVTI_item02","labels":["bug"]}`, true, invalid, ""},
		{"a malformed item", itemsUnarchive.ID, "items", `{"item_id":"x"}`, true, invalid, ""},
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

// Every item change resolves the project with the items, the draft, the repository, and the users it names in
// one query and sends exactly one mutation, with every identifier and value as a variable.
func TestProjectItemChangesSendOneMutationEach(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	core := itemsCore(t, f, nil)

	for _, tt := range []struct {
		name, operation, arguments string
		query, document            []string
		variables                  map[string]string
		result                     string
	}{
		{"delete an issue item", itemsDelete.ID, `{"item_id":"PVTI_item00"}`,
			[]string{"item:node(id:$item){... on ProjectV2Item{id isArchived project{id}}}"},
			[]string{"deleteProjectV2Item(input:{projectId:$project,itemId:$item}){deletedItemId}"},
			map[string]string{"project": projectID, "item": "PVTI_item00"},
			`{"deleted":true,"item_id":"PVTI_item00","project":"orgs/octo-org/projects/7"}`},
		{"restore an item", itemsUnarchive.ID, `{"item_id":"PVTI_item03"}`, nil,
			[]string{"unarchiveProjectV2Item(input:{projectId:$project,itemId:$item})"},
			map[string]string{"project": projectID, "item": "PVTI_item03"},
			`{"archived":false,"item_id":"PVTI_item03","project":"orgs/octo-org/projects/7"}`},
		{"move an item to the top", itemsMove.ID, `{"item_id":"PVTI_item03"}`, nil,
			[]string{"updateProjectV2ItemPosition(input:{projectId:$project,itemId:$item,afterId:$after})"},
			map[string]string{"item": "PVTI_item03", "after": "<nil>"},
			`{"item_id":"PVTI_item03","moved":true,"project":"orgs/octo-org/projects/7"}`},
		{"move an item after another", itemsMove.ID, `{"item_id":"PVTI_item03","after_id":"PVTI_item05"}`,
			[]string{"after:node(id:$after){... on ProjectV2Item{id project{id}}}"}, nil,
			map[string]string{"item": "PVTI_item03", "after": "PVTI_item05"},
			`{"after_id":"PVTI_item05","item_id":"PVTI_item03","moved":true,"project":"orgs/octo-org/projects/7"}`},
		{"retitle a draft and assign it", draftsUpdate.ID,
			`{"item_id":"PVTI_item02","title":"Idea","body":"","assignees":["octocat","Hubot"]}`,
			[]string{"content{... on DraftIssue{id}}", "assignee0:user(login:$assignee0){id}",
				"assignee1:user(login:$assignee1){id}"},
			[]string{"updateProjectV2DraftIssue(input:{draftIssueId:$draft,title:$title,body:$body," +
				"assigneeIds:$assignees})", "$assignees:[ID!]!"},
			map[string]string{"draft": "DI_PVTI_item02", "title": "Idea", "body": "",
				"assignees": "[U_octocat U_Hubot]"},
			`{"assignees":["octocat","Hubot"],"item_id":"PVTI_item02","project":"orgs/octo-org/projects/7",` +
				`"title":"Idea"}`},
		{"remove every assignee of a draft", draftsUpdate.ID, `{"item_id":"PVTI_item02","assignees":[]}`, nil,
			[]string{"updateProjectV2DraftIssue(input:{draftIssueId:$draft,assigneeIds:$assignees})"},
			map[string]string{"assignees": "[]"}, `"assignees":[]`},
		{"convert a draft", draftsConvert.ID, `{"item_id":"PVTI_item02","repository":"octo-org/example"}`,
			[]string{"repository(owner:$repoOwner,name:$repoName){id}", "content{... on DraftIssue{id}}"},
			[]string{"convertProjectV2DraftIssueItemToIssue(input:{itemId:$item,repositoryId:$repository})"},
			map[string]string{"item": "PVTI_item02", "repository": "R_example"},
			`{"issue":{"number":102,"repository":"octo-org/example","url":"https://github.com/octo-org/example/issues/102"},` +
				`"item_id":"PVTI_item02","project":"orgs/octo-org/projects/7","repository":"octo-org/example"}`},
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
			for _, want := range tt.query {
				if !strings.Contains(queries[0].document, want) {
					t.Errorf("query %s lacks %s", queries[0].document, want)
				}
			}
			for _, want := range tt.document {
				if !strings.Contains(mutations[0].document, want) {
					t.Errorf("mutation %s lacks %s", mutations[0].document, want)
				}
			}
			for name, want := range tt.variables {
				if got := fmt.Sprint(mutations[0].variables[name]); got != want {
					t.Errorf("variable %s = %s, want %s", name, got, want)
				}
			}
			for _, request := range []recorded{queries[0], mutations[0]} {
				if document := request.document[strings.Index(request.document, "{"):]; strings.ContainsAny(document, `"`) {
					t.Errorf("a value reached the document: %s", request.document)
				}
			}
		})
	}
}

// A change the project refuses ends after its one query, before any mutation: an item or an after item of
// another project or of none, an issue where a draft is needed, and a user GitHub does not know.
func TestProjectItemChangesAreResolvedBeforeTheMutation(t *testing.T) {
	foreign := []fakeItem{{id: "PVTI_foreign", kind: "DRAFT_ISSUE", title: "Elsewhere"}}
	for _, tt := range []struct {
		name, operation, arguments string
		class                      provider.Class
		message                    string
	}{
		{"a delete of an item of another project", itemsDelete.ID, `{"item_id":"PVTI_foreign"}`,
			provider.ClassNotFound, "does not hold this item in project orgs/octo-org/projects/7"},
		{"a restore of an item of no project", itemsUnarchive.ID, `{"item_id":"PVTI_missing"}`,
			provider.ClassNotFound, "does not hold this item"},
		{"a move of an item of another project", itemsMove.ID, `{"item_id":"PVTI_foreign"}`,
			provider.ClassNotFound, "does not hold this item"},
		{"a move after an item of another project", itemsMove.ID,
			`{"item_id":"PVTI_item00","after_id":"PVTI_foreign"}`, provider.ClassNotFound,
			"does not hold the item after_id names in project orgs/octo-org/projects/7"},
		{"a move after an unknown item", itemsMove.ID, `{"item_id":"PVTI_item00","after_id":"PVTI_missing"}`,
			provider.ClassNotFound, "the item after_id names"},
		{"a draft change of a foreign draft", draftsUpdate.ID, `{"item_id":"PVTI_foreign","title":"x"}`,
			provider.ClassNotFound, "does not hold this item"},
		{"a draft change of an issue", draftsUpdate.ID, `{"item_id":"PVTI_item00","title":"x"}`, "",
			"only a draft issue can be changed or converted"},
		{"a draft assigned to an unknown user", draftsUpdate.ID, `{"item_id":"PVTI_item02","assignees":["ghost"]}`,
			provider.ClassNotFound, "does not hold user ghost"},
		{"a conversion of a pull request", draftsConvert.ID, `{"item_id":"PVTI_item01"}`, "",
			"only a draft issue can be changed or converted"},
		{"a conversion of a foreign draft", draftsConvert.ID, `{"item_id":"PVTI_foreign"}`, provider.ClassNotFound,
			"does not hold this item"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{items: roster(), foreign: foreign}
			core := itemsCore(t, f, nil)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if tt.class == "" && !isInvalidRequest(err) || tt.class != "" && classOf(err) != tt.class ||
				!strings.Contains(fmt.Sprint(err), tt.message) {
				t.Errorf("err = %v, want %q with %q", err, tt.class, tt.message)
			}
			if queries, mutations, _ := split(f.recorded()); len(queries) != 1 || len(mutations) != 0 {
				t.Errorf("requests = %d queries, %d mutations; want one query and no change", len(queries),
					len(mutations))
			}
		})
	}
}

// An item change whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearItemChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{itemsDelete.ID, `{"item_id":"PVTI_item00"}`},
		{itemsUnarchive.ID, `{"item_id":"PVTI_item00"}`},
		{itemsMove.ID, `{"item_id":"PVTI_item00","after_id":"PVTI_item03"}`},
		{draftsUpdate.ID, `{"item_id":"PVTI_item02","title":"x"}`},
		{draftsConvert.ID, `{"item_id":"PVTI_item02"}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			f := &fakeGitHub{items: roster()}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(f.recorded()[len(f.recorded())-1].document, "mutation") {
					return false
				}
				w.WriteHeader(http.StatusBadGateway)
				return true
			}
			core := itemsCore(t, f, nil)
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

// The planning profile ticks the item tools that order items and maintain drafts; restoring stays unticked
// with archiving, and the delete stays out of every profile.
func TestPlanningProfileTicksTheItemTools(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		ticked := map[string]bool{}
		for _, id := range profile.Tools {
			ticked[id] = true
		}
		if profile.ID == "planning" && (!ticked[itemsMove.ID] || !ticked[draftsUpdate.ID] ||
			!ticked[draftsConvert.ID] || ticked[itemsUnarchive.ID] || ticked[itemsArchive.ID]) {
			t.Errorf("planning = %v", profile.Tools)
		}
		if ticked[itemsDelete.ID] {
			t.Errorf("profile %s ticks the item delete", profile.ID)
		}
	}
}
