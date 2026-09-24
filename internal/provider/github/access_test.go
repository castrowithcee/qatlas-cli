package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeWorkflows are the built-in workflows of project 7.
const fakeWorkflows = `{"id":"W_1","number":1,"name":"Item closed","enabled":true},` +
	`{"id":"W_3","number":3,"name":"Auto-archive items","enabled":false}`

// accessChange answers the lookup of users and teams without an item, and the collaborator, team, and
// workflow mutations, and reports whether it did. Like GitHub, a team the organization lacks is answered as
// null without an error; the login ghost and the team ghost-team are not found.
func (f *fakeGitHub) accessChange(w http.ResponseWriter, document string, variables map[string]any) bool {
	var answer any
	switch {
	case strings.Contains(document, "teams(first:"):
		if f.noTeams {
			fmt.Fprint(w, `{"data":{"owner":{"projectV2":{"teams":null}}},"errors":[{"type":"INSUFFICIENT_SCOPES",`+
				`"path":["owner","projectV2","teams"],"message":"needs read:org"}]}`)
			return true
		}
		teams := []any{map[string]any{"slug": "design", "name": "Design"}, map[string]any{"slug": "ops",
			"name": "Operations"}}
		start := 0
		if variables["after"] == "team-0" {
			start = 1
		}
		end := min(start+int(variables["first"].(float64)), len(teams))
		answer = map[string]any{"owner": map[string]any{"projectV2": map[string]any{"teams": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": end < len(teams), "endCursor": fmt.Sprintf("team-%d", end-1)},
			"nodes":    teams[start:end]}}}}
	case strings.HasPrefix(document, "query") && !strings.Contains(document, "item:node") &&
		(strings.Contains(document, ":team(slug:") || strings.Contains(document, "assignee0:user(")):
		owner := map[string]json.RawMessage{"projectV2": json.RawMessage(f.projectJSON())}
		data := map[string]any{"owner": owner}
		for i := 0; ; i++ {
			alias := "team" + strconv.Itoa(i)
			slug, ok := variables[alias].(string)
			if !ok {
				break
			}
			owner[alias] = json.RawMessage(`{"id":"T_` + slug + `"}`)
			if slug == "ghost-team" {
				owner[alias] = json.RawMessage("null")
			}
		}
		for i := 0; ; i++ {
			alias := "assignee" + strconv.Itoa(i)
			login, ok := variables[alias].(string)
			if !ok {
				break
			}
			data[alias] = map[string]any{"id": "U_" + login}
			if login == "ghost" {
				fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s},"%s":null},"errors":[{"type":"NOT_FOUND",`+
					`"path":["%s"],"message":"Could not resolve"}]}`, f.projectJSON(), alias, alias)
				return true
			}
		}
		answer = data
	case strings.Contains(document, "updateProjectV2Collaborators("):
		answer = map[string]any{"update": map[string]any{"clientMutationId": nil}}
	case strings.Contains(document, "linkProjectV2ToTeam("), strings.Contains(document, "unlinkProjectV2FromTeam("):
		answer = map[string]any{"link": map[string]any{"clientMutationId": nil}}
	case strings.Contains(document, "deleteProjectV2Workflow("):
		answer = map[string]any{"workflow": map[string]any{"deletedWorkflowId": variables["workflow"]}}
	default:
		return false
	}
	if _, ok := variables["project"]; ok && variables["project"] != projectID {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":["update"],"message":"no project"}]}`)
		return true
	}
	data, _ := json.Marshal(map[string]any{"data": answer})
	_, _ = w.Write(data)
	return true
}

var accessTools = []string{teamsList.ID, collaboratorsUpdate.ID, teamsLink.ID, teamsUnlink.ID, projectWorkflowsList.ID,
	projectWorkflowsDelete.ID}

// accessConfig holds the connections the access and automation tools meet: one that may do everything but
// lists no tools, an administrator that lists them all, the same without the delete permission, one that may
// only read, and an administrator bound to a project of a user.
func accessConfig(base string) *config.Config {
	cfg := coreConfig(base)
	add := func(name string, targets []string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: permissions, Tools: tools}
	}
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	add("unlisted", planningTargets, config.Permissions(), nil)
	add("admin", planningTargets, config.Permissions(), accessTools)
	add("nodelete", planningTargets, changes, accessTools)
	add("reader", planningTargets, []config.Permission{config.PermissionRead}, accessTools)
	add("user", []string{userTarget}, config.Permissions(), accessTools)
	return cfg
}

func accessCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), accessConfig(serve(t, f)), resolver(red, reads), red)
}

// Every refusal the target, the permissions, the tools list, the confirmation, or the arguments settle ends
// before a secret is read and before GitHub is contacted: changing access needs the tool's name in the tools
// list, deleting a workflow the effect delete as well, and a team needs a project of an organization.
func TestProjectAccessRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	core := accessCore(t, f, &reads)

	invalid := &application.InvalidRequestError{}
	unsupported := &capability.UnsupportedError{}
	unconfirmed := &application.ConfirmationRequiredError{}
	writer := `{"collaborators":[{"user":"octocat","role":"writer"}]}`
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"collaborators without a tools list", collaboratorsUpdate.ID, "unlisted", writer, true, unsupported, ""},
		{"collaborators without the update permission", collaboratorsUpdate.ID, "reader", writer, true,
			unsupported, ""},
		{"unconfirmed collaborators", collaboratorsUpdate.ID, "admin", writer, false, unconfirmed, ""},
		{"collaborators outside the targets", collaboratorsUpdate.ID, "admin",
			`{"project":"orgs/octo-org/projects/8","collaborators":[{"user":"octocat","role":"writer"}]}`, true,
			invalid, "outside the targets"},
		{"an entry naming user and team", collaboratorsUpdate.ID, "admin",
			`{"collaborators":[{"user":"octocat","team":"design","role":"reader"}]}`, true, invalid,
			"either user or team"},
		{"an entry naming neither", collaboratorsUpdate.ID, "admin", `{"collaborators":[{"role":"reader"}]}`, true,
			invalid, "either user or team"},
		{"a user named twice", collaboratorsUpdate.ID, "admin",
			`{"collaborators":[{"user":"octocat","role":"reader"},{"user":"OctoCat","role":"none"}]}`, true, invalid,
			"more than once"},
		{"an unknown role", collaboratorsUpdate.ID, "admin", `{"collaborators":[{"user":"octocat","role":"owner"}]}`,
			true, invalid, ""},
		{"a team of a user's project", collaboratorsUpdate.ID, "user",
			`{"collaborators":[{"team":"design","role":"reader"}]}`, true, invalid, "teams belong to an organization"},
		{"a link without a tools list", teamsLink.ID, "unlisted", `{"team":"design"}`, true, unsupported, ""},
		{"an unconfirmed unlink", teamsUnlink.ID, "admin", `{"team":"design"}`, false, unconfirmed, ""},
		{"a link of a user's project", teamsLink.ID, "user", `{"team":"design"}`, true, invalid,
			"users/octocat/projects/3 belongs to a user"},
		{"an unlink of a user's project", teamsUnlink.ID, "user", `{"team":"design"}`, true, invalid,
			"teams belong to an organization"},
		{"a team list outside the targets", teamsList.ID, "admin", `{"project":"orgs/octo-org/projects/8"}`, false,
			invalid, "outside the targets"},
		{"a team cursor of another list", teamsList.ID, "admin", `{"cursor":"` +
			encodeCursor(fingerprint("statusupdates", "orgs/octo-org/projects/7"), "c") + `"}`, false, invalid,
			"not a next_cursor"},
		{"a workflow list outside the targets", projectWorkflowsList.ID, "admin",
			`{"project":"orgs/octo-org/projects/8"}`, false, invalid, "outside the targets"},
		{"a workflow delete without the delete permission", projectWorkflowsDelete.ID, "nodelete", `{"workflow":1}`,
			true, unsupported, ""},
		{"a workflow delete without a tools list", projectWorkflowsDelete.ID, "unlisted", `{"workflow":1}`, true,
			unsupported, ""},
		{"an unconfirmed workflow delete", projectWorkflowsDelete.ID, "admin", `{"workflow":1}`, false,
			unconfirmed, ""},
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

// The team list reads one batch of the linked teams in name order in one query and continues with a cursor
// bound to the project; a token that may not read teams is told which scope it lacks.
func TestProjectTeamsListPagesByName(t *testing.T) {
	f := &fakeGitHub{}
	core := accessCore(t, f, nil)

	result, err := invoke(t, core, teamsList.ID, "reader", `{"limit":1}`, false)
	var first TeamList
	if err != nil || json.Unmarshal(result, &first) != nil || fmt.Sprint(first.Teams) != "[{design Design}]" ||
		!first.HasMore || first.NextCursor == "" || !strings.Contains(string(result), `"project":"orgs/octo-org/projects/7"`) {
		t.Fatalf("first batch = %s, %v", result, err)
	}
	result, err = invoke(t, core, teamsList.ID, "reader", `{"cursor":"`+first.NextCursor+`"}`, false)
	var second TeamList
	if err != nil || json.Unmarshal(result, &second) != nil || fmt.Sprint(second.Teams) != "[{ops Operations}]" ||
		second.HasMore {
		t.Fatalf("second batch = %s, %v", result, err)
	}
	queries, mutations, _ := split(f.recorded())
	if len(queries) != 2 || len(mutations) != 0 || !strings.Contains(queries[0].document,
		"teams(first:$first,after:$after,orderBy:{field:NAME,direction:ASC})") || queries[1].variables["after"] != "team-0" {
		t.Errorf("queries = %+v", queries)
	}

	f.noTeams = true
	_, err = invoke(t, core, teamsList.ID, "reader", `{}`, false)
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) || providerErr.Class != provider.ClassPermission ||
		!strings.Contains(providerErr.Message, "reading teams needs read:org on a classic token") {
		t.Errorf("err = %v, want a permission naming read:org", err)
	}
}

// The workflow list reads every workflow with the project in one query.
func TestProjectWorkflowsListEveryWorkflow(t *testing.T) {
	f := &fakeGitHub{}
	core := accessCore(t, f, nil)

	result, err := invoke(t, core, projectWorkflowsList.ID, "admin", `{}`, false)
	if err != nil || string(result) != `{"project":"orgs/octo-org/projects/7","workflows":[`+
		`{"enabled":true,"name":"Item closed","number":1},{"enabled":false,"name":"Auto-archive items","number":3}]}` {
		t.Fatalf("result = %s, %v", result, err)
	}
	queries, mutations, _ := split(f.recorded())
	if len(queries) != 1 || len(mutations) != 0 ||
		!strings.Contains(queries[0].document, "workflows(first:100,orderBy:{field:NUMBER,direction:ASC})") {
		t.Errorf("requests = %d queries, %d mutations; want one query of the workflows", len(queries), len(mutations))
	}
}

// Every access and workflow change resolves the project and every user, team, and workflow it names in one
// query and sends exactly one mutation, with every identifier and value as a variable. A team is resolved
// inside the organization that owns the project.
func TestProjectAccessChangesSendOneMutationEach(t *testing.T) {
	f := &fakeGitHub{}
	core := accessCore(t, f, nil)

	for _, tt := range []struct {
		name, operation, arguments string
		query, document            []string
		variables                  map[string]string
		result                     string
	}{
		{"grant, change, and remove roles", collaboratorsUpdate.ID,
			`{"collaborators":[{"user":"octocat","role":"writer"},{"team":"design","role":"reader"},` +
				`{"user":"hubot","role":"none"}]}`,
			[]string{"owner:organization(login:$owner){projectV2(number:$number){id} team0:team(slug:$team0){id}}",
				"assignee0:user(login:$assignee0){id}", "assignee1:user(login:$assignee1){id}"},
			[]string{"updateProjectV2Collaborators(input:{projectId:$project,collaborators:$collaborators})"},
			map[string]string{"project": projectID, "collaborators": "[map[role:WRITER userId:U_octocat] " +
				"map[role:READER teamId:T_design] map[role:NONE userId:U_hubot]]"},
			`{"collaborators":[{"role":"writer","user":"octocat"},{"role":"reader","team":"design"},` +
				`{"role":"none","user":"hubot"}],"project":"orgs/octo-org/projects/7","updated":true}`},
		{"link a team", teamsLink.ID, `{"team":"design"}`, []string{"team0:team(slug:$team0){id}"},
			[]string{"linkProjectV2ToTeam(input:{projectId:$project,teamId:$team})"},
			map[string]string{"project": projectID, "team": "T_design"},
			`{"linked":true,"project":"orgs/octo-org/projects/7","team":"design"}`},
		{"unlink a team", teamsUnlink.ID, `{"team":"design"}`, nil,
			[]string{"unlinkProjectV2FromTeam(input:{projectId:$project,teamId:$team})"},
			map[string]string{"project": projectID, "team": "T_design"}, `"linked":false`},
		{"delete a workflow", projectWorkflowsDelete.ID, `{"workflow":3}`, []string{"workflows(first:100"},
			[]string{"deleteProjectV2Workflow(input:{workflowId:$workflow})"}, map[string]string{"workflow": "W_3"},
			`{"deleted":true,"name":"Auto-archive items","number":3,"project":"orgs/octo-org/projects/7"}`},
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
			if strings.ContainsAny(strings.TrimPrefix(mutations[0].document, "mutation"), `"`) ||
				strings.ContainsAny(strings.TrimPrefix(queries[0].document, "query"), `"`) {
				t.Errorf("a value reached a document: %s %s", queries[0].document, mutations[0].document)
			}
		})
	}
}

// A user, team, or workflow the project or its organization lacks is refused after the one query, before
// any mutation.
func TestProjectAccessChangesAreResolvedBeforeTheMutation(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments, message string
		class                               provider.Class
	}{
		{"an unknown user", collaboratorsUpdate.ID, `{"collaborators":[{"user":"ghost","role":"reader"}]}`,
			"GitHub does not hold user ghost", provider.ClassNotFound},
		{"a team the organization lacks", teamsLink.ID, `{"team":"ghost-team"}`,
			"GitHub does not hold team ghost-team of orgs/octo-org", provider.ClassNotFound},
		{"an unknown workflow", projectWorkflowsDelete.ID, `{"workflow":2}`,
			"workflow 2 is not a workflow of this project; its workflows are 1, 3", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{}
			core := accessCore(t, f, nil)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			var providerErr *provider.Error
			if tt.class == "" && (!isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message)) ||
				tt.class != "" && (!errors.As(err, &providerErr) || providerErr.Class != tt.class ||
					!strings.Contains(providerErr.Message, tt.message)) {
				t.Errorf("err = %v, want %q", err, tt.message)
			}
			if queries, mutations, _ := split(f.recorded()); len(queries) != 1 || len(mutations) != 0 {
				t.Errorf("requests = %d queries, %d mutations; want one query and no change", len(queries),
					len(mutations))
			}
		})
	}
}

// An access or workflow change whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearAccessChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{collaboratorsUpdate.ID, `{"collaborators":[{"user":"octocat","role":"admin"}]}`},
		{teamsLink.ID, `{"team":"design"}`},
		{teamsUnlink.ID, `{"team":"design"}`},
		{projectWorkflowsDelete.ID, `{"workflow":1}`},
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
			core := accessCore(t, f, nil)
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
