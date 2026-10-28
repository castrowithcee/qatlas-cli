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
	changes := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate}
	add := func(name string, targets []string, permissions []config.Effect, tools []string) {
		cfg.Connections[name] = connectionOf(config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets},
			permissions, tools)
	}
	add("items", planningTargets, changes, nil)
	add("admin", planningTargets, config.Effects(), itemTools)
	add("nodelete", planningTargets, changes, itemTools)
	add("unlisted", planningTargets, config.Effects(), nil)
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

	invalid := &provider.InvalidRequestError{}
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
			map[string]string{"assignees": "[]"}, `"item_id":"PVTI_item02"`},
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

// exclusionItems are a project's items: one in an excluded repository, one in a repository outside the
// targets that nothing excludes, a draft issue, and one inside the targets.
func exclusionItems() []fakeItem {
	return []fakeItem{
		{id: "PVTI_secret", kind: "ISSUE", title: "Secret plan", repo: "octo-org/secret", number: 5, status: "Todo",
			body: bodyCanary, assignees: []string{"octocat"}, labels: []string{"bug"}},
		{id: "PVTI_outside", kind: "PULL_REQUEST", title: "Outside work", repo: "acme/outside", number: 6,
			status: "Todo", assignees: []string{"octocat"}, labels: []string{"bug"}},
		{id: "PVTI_draft", kind: "DRAFT_ISSUE", title: "An idea", status: "Todo"},
		{id: "PVTI_inside", kind: "ISSUE", title: "Inside work", repo: "octo-org/example", number: 7, status: "Todo",
			body: bodyCanary},
	}
}

func exclusionItemsCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	cfg := coreConfig(serve(t, f))
	tools := append([]string{itemsList.ID, itemsGet.ID, itemsUpdate.ID, itemsArchive.ID, itemsAdd.ID,
		projectIssuesCreate.ID}, itemTools...)
	cfg.Connections["ex"] = connectionOf(config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: planningTargets, Exclude: []string{"repos/octo-org/secret", "orgs/hidden"}}, config.Effects(), tools)
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red, reads), red)
}

// An item whose content lies in an excluded repository is listed and read as redacted, with nothing but its
// id and type; an item outside the targets that nothing excludes, and a draft issue, stay as they are.
func TestProjectItemsRedactContentOfExclusions(t *testing.T) {
	f := &fakeGitHub{items: exclusionItems()}
	core := exclusionItemsCore(t, f, nil)

	raw, err := invoke(t, core, itemsList.ID, "ex", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list.Items) != 4 {
		t.Fatalf("list = %s, %v", raw, err)
	}
	if got := string(list.Items[0]); got != `{"id":"PVTI_secret","type":"redacted"}` {
		t.Errorf("redacted item = %s", got)
	}
	for _, leak := range []string{"Secret plan", "octo-org/secret", bodyCanary, "/issues/5", "octocat"} {
		if strings.Contains(string(list.Items[0]), leak) {
			t.Errorf("redacted item leaks %q: %s", leak, list.Items[0])
		}
	}
	for i, wants := range [][]string{{`"title":"Outside work"`, `"repository":"acme/outside"`}, {`"title":"An idea"`},
		{`"title":"Inside work"`, `"repository":"octo-org/example"`}} {
		for _, want := range wants {
			if !strings.Contains(string(list.Items[i+1]), want) {
				t.Errorf("item %d = %s, want %s", i+1, list.Items[i+1], want)
			}
		}
	}

	raw, err = invoke(t, core, itemsGet.ID, "ex", `{"item_id":"PVTI_secret"}`, false)
	if err != nil || !strings.Contains(string(raw), `"type":"redacted"`) {
		t.Fatalf("get = %s, %v", raw, err)
	}
	for _, leak := range []string{"Secret plan", "octo-org/secret", bodyCanary, "octocat"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("redacted get leaks %q: %s", leak, raw)
		}
	}
	raw, err = invoke(t, core, itemsGet.ID, "ex", `{"item_id":"PVTI_outside"}`, false)
	if err != nil || !strings.Contains(string(raw), `"repository":"acme/outside"`) ||
		!strings.Contains(string(raw), `"title":"Outside work"`) {
		t.Errorf("get of an item outside the targets = %s, %v, want it unchanged", raw, err)
	}
}

// A change of an item in an exclusion is read once to prove it, then refused as missing without a change; the
// same changes reach items outside the exclusions.
func TestProjectItemChangesRefuseExclusionsAfterOneRead(t *testing.T) {
	f := &fakeGitHub{items: exclusionItems()}
	core := exclusionItemsCore(t, f, nil)
	for _, tt := range []struct{ name, operation, arguments string }{
		{"update", itemsUpdate.ID, `{"item_id":"PVTI_secret","fields":{"Priority":"P1"}}`},
		{"archive", itemsArchive.ID, `{"item_id":"PVTI_secret"}`},
		{"unarchive", itemsUnarchive.ID, `{"item_id":"PVTI_secret"}`},
		{"delete", itemsDelete.ID, `{"item_id":"PVTI_secret"}`},
		{"move", itemsMove.ID, `{"item_id":"PVTI_secret"}`},
		{"move after", itemsMove.ID, `{"item_id":"PVTI_inside","after_id":"PVTI_secret"}`},
		{"draft change", draftsUpdate.ID, `{"item_id":"PVTI_secret","title":"x"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			_, err := invoke(t, core, tt.operation, "ex", tt.arguments, true)
			if classOf(err) != provider.ClassNotFound {
				t.Fatalf("err = %v, want not-found", err)
			}
			requests := f.recorded()[before:]
			if len(requests) != 1 || strings.HasPrefix(strings.TrimSpace(requests[0].document), "mutation") {
				t.Errorf("requests = %+v, want exactly one read", requests)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("the refusal names the excluded repository: %v", err)
			}
		})
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, itemsArchive.ID, "ex", `{"item_id":"PVTI_outside"}`, true); err != nil {
		t.Errorf("archive of an item outside the targets = %v, want it allowed", err)
	}
	if len(f.recorded())-before != 2 {
		t.Errorf("requests = %d, want the proving read and the mutation", len(f.recorded())-before)
	}
}

// An item added, created, or converted into an excluded repository is refused before a secret or a request.
func TestProjectItemTargetsRefuseExcludedRepositories(t *testing.T) {
	f := &fakeGitHub{items: exclusionItems()}
	reads := 0
	core := exclusionItemsCore(t, f, &reads)
	for _, tt := range []struct{ operation, arguments string }{
		{itemsAdd.ID, `{"repository":"octo-org/secret","number":5}`},
		{itemsAdd.ID, `{"repository":"Hidden/anything","number":5}`},
		{projectIssuesCreate.ID, `{"repository":"octo-org/SECRET","title":"x"}`},
		{draftsConvert.ID, `{"item_id":"PVTI_draft","repository":"octo-org/secret"}`},
	} {
		before := len(f.recorded())
		_, err := invoke(t, core, tt.operation, "ex", tt.arguments, true)
		if !isInvalidRequest(err) || reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s %s = %v (reads %d), want an invalid request before any I/O", tt.operation,
				tt.arguments, err, reads)
		}
	}
}

func referenceNode(repository, title string) string {
	repo := "null"
	if repository != "" {
		repo = `{"nameWithOwner":"` + repository + `"}`
	}
	return `{"id":"I_` + title + `","number":3,"title":"` + title + `","state":"OPEN","url":"https://github.com/x/y/pull/3",` +
		`"repository":` + repo + `}`
}

// The pull requests of a project item's field value drop a reference from an excluded repository or without a
// repository, keep one outside the targets, and count as GitHub counted; without exclusions nothing is dropped.
func TestProjectItemFieldValueReferencesHonourExclusions(t *testing.T) {
	node := `{"id":"PVTI_refs","type":"DRAFT_ISSUE","fieldValues":{"totalCount":1,"nodes":[` +
		`{"__typename":"ProjectV2ItemFieldPullRequestValue","field":{"id":"F_pr"},"pullRequests":{"totalCount":4,"nodes":[` +
		referenceNode("octo-org/secret", "SecretRef") + `,` + referenceNode("acme/outside", "OutsideRef") + `,` +
		referenceNode("", "LooseRef") + `,` + referenceNode("hidden/any", "OwnerRef") + `]}}]},` +
		`"content":{"__typename":"DraftIssue","id":"DI_refs","title":"t"}}`
	f := &fakeGitHub{items: []fakeItem{{id: "PVTI_refs", kind: "DRAFT_ISSUE", node: node}}}
	core := exclusionItemsCore(t, f, nil)

	raw, err := invoke(t, core, itemsGet.ID, "ex", `{"item_id":"PVTI_refs"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"SecretRef", "LooseRef", "OwnerRef", "octo-org/secret", "hidden"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("get leaks %q: %s", leak, raw)
		}
	}
	if !strings.Contains(string(raw), "OutsideRef") || !strings.Contains(string(raw), `"pull_requests_total_count":4`) {
		t.Errorf("get = %s, want the outside reference and the counted total", raw)
	}

	raw, err = invoke(t, core, itemsGet.ID, "planning", `{"item_id":"PVTI_refs"}`, false)
	if err != nil || strings.Count(string(raw), `"number":3`) != 4 {
		t.Errorf("get without exclusions = %s, %v, want all four references", raw, err)
	}
}

// Issue references (parent, duplicate, linked, closing, tracked, pending suggestions) and pinned issues are
// left out under the same rule as the project item references.
func TestIssueAndPinnedReferencesHonourExclusions(t *testing.T) {
	ref := func(repository, title string) string { return referenceNode(repository, title) }
	connection := func(nodes ...string) string {
		return `{"totalCount":` + fmt.Sprint(len(nodes)) + `,"pageInfo":{"hasNextPage":false},"nodes":[` +
			strings.Join(nodes, ",") + `]}`
	}
	issue := `{"parent":` + ref("octo-org/secret", "SecretParent") + `,"duplicateOf":` + ref("acme/outside", "OutsideDup") +
		`,"closedByPullRequestsReferences":` + connection(ref("octo-org/secret", "SecretClosing"), ref("acme/outside", "OutsideClosing")) +
		`,"relatesTo":` + connection(ref("hidden/x", "SecretRelates"), ref("", "LooseRelates")) +
		`,"trackedInIssues":` + connection(ref("octo-org/secret", "SecretIn")) +
		`,"trackedIssues":` + connection(ref("octo-org/secret", "SecretTracked"), ref("acme/outside", "OutsideTracked")) +
		`,"pendingSuggestions":[{"__typename":"PendingCloseSuggestion","duplicateOf":` + ref("octo-org/secret", "SecretSuggested") + `}]}`
	var node issueNodeRaw
	if err := json.Unmarshal([]byte(issue), &node); err != nil {
		t.Fatal(err)
	}
	pinned := `{"nodes":[{"id":"PI_1","issue":` + ref("octo-org/secret", "SecretPinned") + `},{"id":"PI_2","issue":` +
		ref("acme/outside", "OutsidePinned") + `}],"totalCount":2}`
	var pins gqlNodes[gqlPinnedIssueRaw]
	if err := json.Unmarshal([]byte(pinned), &pins); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name    string
		allowed allowlist
		dropped bool
	}{
		{"excluded", mustAllowlist(t, []string{"repos/octo-org/example"}, []string{"repos/octo-org/secret", "orgs/hidden"}), true},
		{"not excluded", mustAllowlist(t, []string{"repos/octo-org/example"}, nil), false},
		{"open", allowlist{}, false},
	} {
		width, _ := json.Marshal(node.width(tt.allowed))
		list, _, _ := pageItems(&pins, func(p *gqlPinnedIssueRaw) *GraphQLPinnedIssue { return p.project(tt.allowed) })
		listed, _ := json.Marshal(list)
		all := string(width) + string(listed)
		for _, title := range []string{"SecretParent", "SecretClosing", "SecretRelates", "LooseRelates", "SecretIn", "SecretTracked",
			"SecretSuggested", "SecretPinned"} {
			if got := strings.Contains(all, title); got == tt.dropped {
				t.Errorf("%s: %s present = %v, want %v", tt.name, title, got, !tt.dropped)
			}
		}
		for _, title := range []string{"OutsideDup", "OutsideClosing", "OutsideTracked", "OutsidePinned"} {
			if !strings.Contains(all, title) {
				t.Errorf("%s: %s was dropped, want it kept", tt.name, title)
			}
		}
		if !strings.Contains(string(width), `"tracked_issues_total_count":2`) {
			t.Errorf("%s: the counted total changed: %s", tt.name, width)
		}
	}
}
