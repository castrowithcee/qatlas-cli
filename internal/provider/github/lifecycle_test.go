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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// lifecycle answers the requests of the project lifecycle tools and reports whether it did: the node of the
// organization octo-org or the user octocat, project 7 of octo-org together with the repository
// octo-org/example, and the lifecycle mutations. Anything else it leaves to the other routes.
func (f *fakeGitHub) lifecycle(w http.ResponseWriter, document string, variables map[string]any) bool {
	notFound := func(path string) {
		fmt.Fprintf(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":[%q],"message":"Could not resolve"}]}`,
			path)
	}
	created := func(number int) {
		title, _ := json.Marshal(variables["title"])
		fmt.Fprintf(w, `{"data":{"create":{"projectV2":{"number":%d,"title":%s,`+
			`"url":"https://github.com/orgs/octo-org/projects/%d","closed":false,"public":false}}}}`, number, title,
			number)
	}
	switch {
	case strings.HasPrefix(document, "query($owner:String!){owner:"):
		switch {
		case variables["owner"] == "octo-org" && strings.Contains(document, "owner:organization("):
			fmt.Fprint(w, `{"data":{"owner":{"id":"O_octo-org"}}}`)
		case variables["owner"] == "octocat" && strings.Contains(document, "owner:user("):
			fmt.Fprint(w, `{"data":{"owner":{"id":"U_octocat"}}}`)
		default:
			notFound("owner")
		}
	case strings.Contains(document, "repository(owner:$repoOwner,name:$repoName){id}") &&
		!strings.Contains(document, "item:node"):
		if variables["repoOwner"] != "octo-org" || variables["repoName"] != "example" {
			fmt.Fprintf(w, `{"data":{"owner":{"projectV2":{"id":%q}},"repository":null},"errors":[{"type":`+
				`"NOT_FOUND","path":["repository"],"message":"Could not resolve"}]}`, projectID)
			return true
		}
		fmt.Fprintf(w, `{"data":{"owner":{"projectV2":{"id":%q}},"repository":{"id":"R_example"}}}`, projectID)
	case strings.Contains(document, "createProjectV2("):
		if variables["owner"] != "O_octo-org" && variables["owner"] != "U_octocat" {
			notFound("create")
			return true
		}
		created(12)
	case strings.Contains(document, "copyProjectV2("):
		if variables["project"] != projectID || variables["owner"] != "O_octo-org" {
			notFound("create")
			return true
		}
		created(13)
	case strings.Contains(document, "updateProjectV2("):
		title, closed, public := "Plan 7", false, false
		if value, ok := variables["title"].(string); ok {
			title = value
		}
		if value, ok := variables["closed"].(bool); ok {
			closed = value
		}
		if value, ok := variables["public"].(bool); ok {
			public = value
		}
		fmt.Fprintf(w, `{"data":{"update":{"projectV2":{"number":7,"title":%q,`+
			`"url":"https://github.com/orgs/octo-org/projects/7","closed":%t,"public":%t}}}}`, title, closed, public)
	case strings.Contains(document, "deleteProjectV2("):
		fmt.Fprint(w, `{"data":{"delete":{"clientMutationId":null}}}`)
	case strings.Contains(document, "linkProjectV2ToRepository(") ||
		strings.Contains(document, "unlinkProjectV2FromRepository("):
		fmt.Fprint(w, `{"data":{"link":{"clientMutationId":null}}}`)
	default:
		return false
	}
	return true
}

var lifecycleTools = []string{projectsCreate.ID, projectsUpdate.ID, projectsDelete.ID, projectsCopy.ID,
	projectsLink.ID, projectsUnlink.ID}

// lifecycleConfig holds the connections the lifecycle tools meet: an administrator that names the owner, a
// project, and a repository and may delete, the same targets without the delete permission or without a
// tools list, a connection with project and repository patterns only, an owner target alone, and a
// connection without targets.
func lifecycleConfig(base string) *config.Config {
	cfg := coreConfig(base)
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	admin := []string{"orgs/octo-org", projectTarget, repoTarget}
	add := func(name string, targets []string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: permissions, Tools: tools}
	}
	add("admin", admin, config.Permissions(), lifecycleTools)
	add("nodelete", admin, changes, lifecycleTools)
	add("unlisted", admin, config.Permissions(), nil)
	add("patterns", []string{"orgs/octo-org/projects/*", "repos/octo-org/*"}, changes, nil)
	add("owneronly", []string{"orgs/octo-org"}, changes, nil)
	add("open", nil, changes, nil)
	return cfg
}

// Every lifecycle refusal ends before a secret is read and before GitHub is contacted: a new project and the
// destination of a copy need an owner target, a project pattern is not enough; every other tool needs an
// allowed project and, for a link, an allowed repository; the delete needs its effect, its name in the tools
// list, and a confirmation.
func TestProjectLifecycleRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), lifecycleConfig(base), resolver(red, &reads), red)

	invalid := &application.InvalidRequestError{}
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"a create with a project pattern only", projectsCreate.ID, "patterns",
			`{"owner":"orgs/octo-org","title":"x"}`, true, invalid, "a project pattern of the owner is not enough"},
		{"a create without an owner beside patterns", projectsCreate.ID, "patterns", `{"title":"x"}`, true, invalid,
			"owner is required"},
		{"a create of another owner", projectsCreate.ID, "admin", `{"owner":"orgs/hubot","title":"x"}`, true,
			invalid, "owner is outside the targets"},
		{"a create of the other owner kind", projectsCreate.ID, "admin", `{"owner":"users/octo-org","title":"x"}`,
			true, invalid, "owner is outside the targets"},
		{"a create without an owner and targets", projectsCreate.ID, "open", `{"title":"x"}`, true, invalid,
			"owner is required"},
		{"a create with a blank title", projectsCreate.ID, "admin", `{"title":"   "}`, true, invalid, "title"},
		{"a copy into an owner only a pattern names", projectsCopy.ID, "patterns",
			`{"project":"orgs/octo-org/projects/7","owner":"orgs/octo-org","title":"x"}`, true, invalid,
			"a project pattern of the owner is not enough"},
		{"a copy of a project outside the targets", projectsCopy.ID, "admin",
			`{"project":"orgs/octo-org/projects/8","title":"x"}`, true, invalid, "project is outside the targets"},
		{"a link to a repository outside the targets", projectsLink.ID, "admin",
			`{"repository":"octo-org/other"}`, true, invalid, "repository is outside the targets"},
		{"an unlink of a project outside the targets", projectsUnlink.ID, "admin",
			`{"project":"orgs/octo-org/projects/8","repository":"octo-org/example"}`, true, invalid,
			"project is outside the targets"},
		{"an update without a setting", projectsUpdate.ID, "admin", `{}`, true, invalid, "name at least one"},
		{"an update with a blank title", projectsUpdate.ID, "admin", `{"title":" "}`, true, invalid, "title"},
		{"an update that empties the short description", projectsUpdate.ID, "admin",
			`{"short_description":"","closed":true}`, true, invalid, "cannot clear either through its API"},
		{"an update that empties the readme", projectsUpdate.ID, "admin", `{"readme":""}`, true, invalid,
			"short_description and readme need at least 1 character"},
		{"an update on an owner target", projectsUpdate.ID, "owneronly",
			`{"project":"orgs/octo-org/projects/7","closed":true}`, true, invalid, "project is outside the targets"},
		{"a delete of a project outside the targets", projectsDelete.ID, "admin",
			`{"project":"orgs/octo-org/projects/8"}`, true, invalid, "project is outside the targets"},
		{"a delete without the delete permission", projectsDelete.ID, "nodelete", `{}`, true,
			&capability.UnsupportedError{}, ""},
		{"a delete without a tools list", projectsDelete.ID, "unlisted", `{}`, true,
			&capability.UnsupportedError{}, ""},
		{"an unconfirmed delete", projectsDelete.ID, "admin", `{}`, false,
			&application.ConfirmationRequiredError{}, ""},
		{"an unconfirmed create", projectsCreate.ID, "admin", `{"title":"x"}`, false,
			&application.ConfirmationRequiredError{}, ""},
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

// Every lifecycle tool resolves what it needs, sends exactly one mutation with its values as variables, and
// answers within its contract, naming its targets.
func TestProjectLifecycleSendsOneMutationEach(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), lifecycleConfig(base), resolver(red, nil), red)

	for _, tt := range []struct {
		name, operation, connection, arguments string
		queries                                int
		document                               []string
		absent                                 []string
		variables                              map[string]any
		result                                 string
	}{
		{"create in the one owner target", projectsCreate.ID, "admin", `{"title":"Roadmap"}`, 1,
			[]string{"createProjectV2(input:{ownerId:$owner,title:$title})"}, nil,
			map[string]any{"owner": "O_octo-org", "title": "Roadmap"},
			`{"created":{"closed":false,"number":12,"project":"orgs/octo-org/projects/12","public":false,` +
				`"title":"Roadmap","url":"https://github.com/orgs/octo-org/projects/12"},"owner":"orgs/octo-org"}`},
		{"create for a user without targets", projectsCreate.ID, "open", `{"owner":"users/octocat","title":"Mine"}`,
			1, []string{"createProjectV2("}, nil, map[string]any{"owner": "U_octocat", "title": "Mine"},
			`"project":"users/octocat/projects/12"`},
		{"update the title and close", projectsUpdate.ID, "admin", `{"title":"Done plan","closed":true}`, 1,
			[]string{"updateProjectV2(input:{projectId:$project,title:$title,closed:$closed})",
				"$title:String!", "$closed:Boolean!"}, []string{"readme:", "public:", "shortDescription:"},
			map[string]any{"project": projectID, "title": "Done plan", "closed": true},
			`{"closed":true,"number":7,"project":"orgs/octo-org/projects/7","public":false,"title":"Done plan",` +
				`"url":"https://github.com/orgs/octo-org/projects/7"}`},
		{"update the description, readme, and visibility", projectsUpdate.ID, "admin",
			`{"short_description":"Next year","readme":"# Plan","public":true}`, 1,
			[]string{"shortDescription:$shortDescription", "readme:$readme", "public:$public"}, []string{"title:"},
			map[string]any{"project": projectID, "shortDescription": "Next year", "readme": "# Plan", "public": true},
			`"public":true`},
		{"reopen", projectsUpdate.ID, "admin", `{"closed":false}`, 1, []string{"closed:$closed"}, nil,
			map[string]any{"project": projectID, "closed": false}, `"closed":false`},
		{"copy with drafts", projectsCopy.ID, "admin", `{"title":"Next","include_drafts":true}`, 2,
			[]string{"copyProjectV2(input:{projectId:$project,ownerId:$owner,title:$title,includeDraftIssues:$drafts})"},
			nil, map[string]any{"project": projectID, "owner": "O_octo-org", "title": "Next", "drafts": true},
			`{"created":{"closed":false,"number":13,"project":"orgs/octo-org/projects/13","public":false,` +
				`"title":"Next","url":"https://github.com/orgs/octo-org/projects/13"},"owner":"orgs/octo-org",` +
				`"project":"orgs/octo-org/projects/7"}`},
		{"copy without drafts", projectsCopy.ID, "admin", `{"title":"Next"}`, 2, []string{"copyProjectV2("}, nil,
			map[string]any{"drafts": false}, `"number":13`},
		{"link", projectsLink.ID, "admin", `{}`, 1,
			[]string{"linkProjectV2ToRepository(input:{projectId:$project,repositoryId:$repository})"},
			[]string{"unlink"}, map[string]any{"project": projectID, "repository": "R_example"},
			`{"linked":true,"project":"orgs/octo-org/projects/7","repository":"octo-org/example"}`},
		{"unlink", projectsUnlink.ID, "admin", `{"repository":"octo-org/example"}`, 1,
			[]string{"unlinkProjectV2FromRepository(input:{projectId:$project,repositoryId:$repository})"}, nil,
			map[string]any{"project": projectID, "repository": "R_example"},
			`{"linked":false,"project":"orgs/octo-org/projects/7","repository":"octo-org/example"}`},
		{"delete", projectsDelete.ID, "admin", `{"project":"orgs/octo-org/projects/7"}`, 1,
			[]string{"deleteProjectV2(input:{projectId:$project})"}, nil, map[string]any{"project": projectID},
			`{"deleted":true,"project":"orgs/octo-org/projects/7"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			result, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, true)
			if err != nil || !strings.Contains(string(result), tt.result) {
				t.Fatalf("result = %s, %v; want %s", result, err, tt.result)
			}
			queries, mutations, rest := split(f.recorded()[before:])
			if len(queries) != tt.queries || len(mutations) != 1 || len(rest) != 0 {
				t.Fatalf("requests = %d queries, %d mutations, %v REST; want %d, 1, none", len(queries),
					len(mutations), rest, tt.queries)
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
				if mutations[0].variables[name] != want {
					t.Errorf("variable %s = %v, want %v", name, mutations[0].variables[name], want)
				}
			}
		})
	}
}

// A project, an owner, or a repository GitHub does not resolve ends before the mutation and names it.
func TestProjectLifecycleNamesWhatGitHubDoesNotHold(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), lifecycleConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ operation, connection, arguments, want string }{
		{projectsCreate.ID, "open", `{"owner":"orgs/hubot","title":"x"}`, "GitHub does not hold owner orgs/hubot"},
		{projectsLink.ID, "open", `{"project":"orgs/octo-org/projects/7","repository":"octo-org/other"}`,
			"GitHub does not hold repository octo-org/other"},
	} {
		before := len(f.recorded())
		_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, true)
		if !strings.Contains(fmt.Sprint(err), tt.want) {
			t.Errorf("%s %s = %v, want %q", tt.operation, tt.arguments, err, tt.want)
		}
		if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 0 {
			t.Errorf("%s sent %d mutations, want none", tt.operation, len(mutations))
		}
	}
}

// A lifecycle mutation whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearProjectLifecycleChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{projectsCreate.ID, `{"title":"x"}`},
		{projectsDelete.ID, `{}`},
		{projectsLink.ID, `{}`},
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
			core := application.New(registry(t), lifecycleConfig(base), resolver(red, nil), red)
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
