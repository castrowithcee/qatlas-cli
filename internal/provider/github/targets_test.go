package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// targetsConfig holds connections with every shape of target list: none at all, a pattern, one project and
// one repository, and a project next to a pattern.
func targetsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all}
	cfg.Connections["owner"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/*"}, Effects: all}
	cfg.Connections["single"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget, repoTarget}, Effects: all}
	cfg.Connections["mixed"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget, "repos/octo-org/*"}, Effects: all}
	return cfg
}

// inRepository runs the rest of the test inside a git repository whose origin points to url.
func inRepository(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, ".git", sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"HEAD":   "ref: refs/heads/main\n",
		"config": "[remote \"origin\"]\n\turl = " + url + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, ".git", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// A target argument may be left out only when the targets allow exactly one of its kind. An explicit
// argument wins, a default never widens the targets, and the git remote of the working directory never
// chooses a target.
func TestTargetDefaultsNeverWidenTheTargets(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	host := strings.Split(strings.TrimPrefix(base, "https://"), ":")[0]
	inRepository(t, "https://"+host+"/octo-org/example.git")
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, &reads), red)
	issue := func(connection, arguments string) (string, error) {
		before := len(f.recorded())
		_, err := invoke(t, core, "github.issues.get", connection, arguments, false)
		if requests := f.recorded()[before:]; len(requests) == 1 {
			return requests[0].path, err
		}
		return "", err
	}
	const want = "/api/v3/repos/octo-org/example/issues/42"

	if path, err := issue("single", `{"number":42}`); err != nil || path != want {
		t.Errorf("the only repository = %q, %v; want the default", path, err)
	}
	for _, connection := range []string{"open", "owner"} {
		if path, err := issue(connection, `{"number":42,"repository":"octo-org/example"}`); err != nil ||
			path != want {
			t.Errorf("an explicit repository on %s = %q, %v; want it to win", connection, path, err)
		}
	}

	for name, connection := range map[string]string{
		"no targets":                       "open",
		"a pattern the remote lies inside": "owner",
		"a project beside such a pattern":  "mixed",
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := issue(connection, `{"number":42}`); !isInvalidRequest(err) ||
			!strings.Contains(err.Error(), "pass repository as OWNER/REPO") {
			t.Errorf("%s = %v, want repository to be required", name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", name)
		}
	}
}

// A tool that touches a project and a repository checks both against the targets before any IO; without a
// repository argument it follows the same defaults as every other tool.
func TestCrossTargetToolsCheckBothTargets(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct{ name, connection, arguments string }{
		{"a repository outside the pattern", "mixed", `{"repository":"hubot/example","number":42}`},
		{"a project outside the targets", "mixed",
			`{"repository":"octo-org/example","number":42,"project":"orgs/octo-org/projects/8"}`},
		{"no repository beside a pattern", "mixed", `{"number":42}`},
		{"no project on a repository list", "owner", `{"repository":"octo-org/example","number":42}`},
		{"no project on an open connection", "open", `{"repository":"octo-org/example","number":42}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, "github.project-items.add", tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}

	result, err := invoke(t, core, "github.project-items.add", "mixed", `{"repository":"octo-org/example","number":42}`, true)
	if err != nil || !strings.Contains(string(result), "PVTI_for_I_example_42") {
		t.Fatalf("an allowed issue = %s, %v", result, err)
	}
	result, err = invoke(t, core, "github.project-items.add", "open",
		`{"repository":"octo-org/example","number":42,"project":"orgs/octo-org/projects/7"}`, true)
	if err != nil || !strings.Contains(string(result), "PVTI_for_I_example_42") {
		t.Fatalf("both targets on an open connection = %s, %v", result, err)
	}
}

// A cursor stays bound to the repository that produced it.
func TestACursorOfAnotherTargetIsRefused(t *testing.T) {
	f := &fakeGitHub{}
	for n := 3; n >= 1; n-- {
		f.issues = append(f.issues, fakeIssue{number: n, state: "open"})
	}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, &reads), red)

	first, err := invoke(t, core, "github.issues.list", "owner", `{"repository":"octo-org/example","limit":1}`, false)
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err != nil || json.Unmarshal(first, &page) != nil || page.NextCursor == "" {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	next := `{"repository":"octo-org/example","limit":1,"cursor":"` + page.NextCursor + `"}`
	if _, err := invoke(t, core, "github.issues.list", "owner", next, false); err != nil {
		t.Errorf("the same repository = %v, want the next batch", err)
	}
	reads = 0
	before := len(f.recorded())
	other := `{"repository":"octo-org/other","limit":1,"cursor":"` + page.NextCursor + `"}`
	if _, err := invoke(t, core, "github.issues.list", "owner", other, false); !isInvalidRequest(err) {
		t.Errorf("another repository = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("a foreign cursor reached the credential or GitHub")
	}
}

// Discovery names the target argument of every tool and says when it may be left out; the schema admits
// only a single target, never a pattern.
func TestDiscoveryDescribesTheTargetArguments(t *testing.T) {
	both := map[string]bool{itemsAdd.ID: true, projectIssuesCreate.ID: true, projectsLink.ID: true,
		projectsUnlink.ID: true}
	for _, descriptor := range registry(t).Provider(Provider) {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(descriptor.InputSchema, &schema); err != nil {
			t.Fatalf("%s schema = %v", descriptor.ID, err)
		}
		names := []string{"repository"}
		switch {
		case descriptor.ID == accountsMe.ID || descriptor.ID == starsList.ID ||
			descriptor.ID == repositoriesSearch.ID || descriptor.ID == codeSearch.ID ||
			descriptor.ID == issuesSearch.ID || descriptor.ID == pullRequestsSearch.ID ||
			descriptor.ID == commitsSearch.ID || descriptor.ID == usersSearch.ID ||
			descriptor.ID == organizationsSearch.ID || descriptor.ID == repositoriesCreate.ID ||
			descriptor.ID == rulesetsList.ID || descriptor.ID == rulesetsGet.ID ||
			descriptor.ID == rulesetsCreate.ID || descriptor.ID == rulesetsUpdate.ID ||
			descriptor.ID == rulesetsDelete.ID || descriptor.ID == customPropertiesGet.ID ||
			descriptor.ID == customPropertiesSet.ID || descriptor.ID == issueFieldsList.ID ||
			descriptor.ID == notificationsList.ID || descriptor.ID == notificationsGet.ID ||
			descriptor.ID == notificationsDismiss.ID || descriptor.ID == notificationsMarkAll.ID ||
			descriptor.ID == threadSubscriptionsSet.ID ||
			descriptor.ID == gistsList.ID || descriptor.ID == gistsGet.ID || descriptor.ID == gistsCreate.ID ||
			descriptor.ID == gistsUpdate.ID || descriptor.ID == gistsDelete.ID ||
			descriptor.ID == globalAdvisoriesList.ID || descriptor.ID == globalAdvisoriesGet.ID:
			// The notification tools take an optional repository of their own or a thread id; the gist tools take a
			// gist id or none; the global advisory tools name no target.
			names = []string{}
		case descriptor.ID == projectsList.ID || descriptor.ID == repositoriesList.ID ||
			descriptor.ID == projectsCreate.ID || descriptor.ID == issueTypesList.ID ||
			descriptor.ID == organizationTeamsList.ID ||
			descriptor.ID == teamMembersList.ID || descriptor.ID == organizationAdvisoriesList.ID:
			names = []string{"owner"}
		case descriptor.ID == projectsCopy.ID:
			names = []string{"project", "owner"}
		case strings.HasPrefix(descriptor.ID, Provider+".project"):
			names = []string{"project"}
			if both[descriptor.ID] {
				names = append(names, "repository")
			}
		}
		for _, name := range names {
			if _, ok := schema.Properties[name]; !ok || strings.Contains(strings.Join(schema.Required, ","), name) {
				t.Errorf("%s: %s is missing or required in %s", descriptor.ID, name, descriptor.InputSchema)
			}
			found := false
			for _, argument := range descriptor.Arguments {
				if argument.Name == name {
					found = !argument.Required && strings.Contains(argument.Description, "optional when") &&
						strings.Contains(argument.Description, "inside the targets") &&
						!strings.Contains(argument.Description, "remote")
				}
			}
			if !found {
				t.Errorf("%s does not describe %s as an optional target", descriptor.ID, name)
			}
		}
		// Every result names the target the tool acted on, under the name of its argument.
		var output struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(descriptor.OutputSchema, &output); err != nil {
			t.Fatalf("%s output schema = %v", descriptor.ID, err)
		}
		fields := map[string]bool{}
		for _, field := range descriptor.Fields {
			fields[field.Name] = true
		}
		for _, name := range names {
			if string(output.Properties[name]) != `{"type":"string"}` || !slices.Contains(output.Required, name) ||
				!fields[name] {
				t.Errorf("%s: the result does not name its %s: %s", descriptor.ID, name, descriptor.OutputSchema)
			}
		}
	}

	reg := registry(t)
	red := &redact.Redactor{}
	core := application.New(reg, targetsConfig("https://api.github.com"), resolver(red, nil), red)
	for _, arguments := range []string{`{"number":1,"repository":"octo-org/*"}`, `{"number":1,"repository":"octo-org"}`,
		`{"number":1,"project":"orgs/octo-org/projects/7"}`} {
		if _, err := invoke(t, core, "github.issues.get", "open", arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want the schema to refuse it", arguments, err)
		}
	}
}

// A connection without an exact target tests the token with the smallest read there is: its user.
func TestTestConnectionWithoutAnExactTargetReadsTheUser(t *testing.T) {
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v3/user" {
			return false
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
		return true
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	for _, targets := range [][]string{nil, {"repos/octo-org/*"}} {
		resolved := resolvedConnection("gh", base, "")
		resolved.Targets = targets
		before := len(f.recorded())
		class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red)
		requests := f.recorded()[before:]
		if err != nil || class != provider.ClassOK || len(requests) != 1 || requests[0].path != "/api/v3/user" {
			t.Errorf("targets %v: test = %q, %v with %+v", targets, class, err, requests)
		}
	}
}

// A result names the repository or project the tool acted on, whether the argument chose it or a default
// did, and a target GitHub does not hold or does not show to the token is not-found naming that target.
func TestResultsAndRefusalsNameTheChosenTarget(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, nil), red)
	named := func(result json.RawMessage, name string) string {
		var fields map[string]any
		_ = json.Unmarshal(result, &fields)
		value, _ := fields[name].(string)
		return value
	}

	result, err := invoke(t, core, "github.issues.get", "single", `{"number":42}`, false)
	if err != nil || named(result, "repository") != "octo-org/example" {
		t.Errorf("the only repository = %s, %v; want it named in the result", result, err)
	}
	result, err = invoke(t, core, "github.project-items.list", "single", `{}`, false)
	if err != nil || named(result, "project") != projectTarget || named(result, "repository") != "" {
		t.Errorf("the only project = %s, %v; want it named in the result", result, err)
	}
	result, err = invoke(t, core, "github.project-items.add", "mixed", `{"repository":"octo-org/example","number":42}`, true)
	if err != nil || named(result, "project") != projectTarget || named(result, "repository") != "octo-org/example" {
		t.Errorf("an added issue = %s, %v; want both targets named in the result", result, err)
	}
	result, err = invoke(t, core, "github.issues.get", "open", `{"number":42,"repository":"octo-org/example"}`, false)
	if err != nil || named(result, "repository") != "octo-org/example" {
		t.Errorf("an explicit repository = %s, %v; want it named in the result", result, err)
	}

	const repositoryHint = "; check the name, and that the token can see it (classic: scope repo for a private " +
		"repository; fine-grained: access to this repository)"
	for _, tt := range []struct{ operation, arguments, want string }{
		{"github.issues.list", `{"repository":"octo-org/absent"}`, "list issues: GitHub does not hold repository " +
			"octo-org/absent or does not show it to this token" + repositoryHint},
		{"github.issues.get", `{"repository":"octo-org/absent","number":42}`, "get issue: GitHub does not hold " +
			"issue #42 in repository octo-org/absent or does not show it to this token; check the arguments, and " +
			"that the token can see the repository"},
		{"github.workflows.list", `{"repository":"octo-org/absent"}`, "GitHub does not hold this resource in " +
			"repository octo-org/absent"},
	} {
		if _, err := invoke(t, core, tt.operation, "open", tt.arguments, false); classOf(err) != provider.ClassNotFound ||
			!strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s of an absent repository = %v (class %q), want not-found with %q", tt.operation,
				err, classOf(err), tt.want)
		}
	}

	// A target of the wrong form is refused before any request, with the form it must have.
	for _, tt := range []struct{ operation, arguments, want string }{
		{"github.issues.list", `{"repository":"nodash"}`, "$.repository does not have the required form OWNER/REPO"},
		{"github.project-items.list", `{"project":"octocat/3"}`, "$.project does not have the required form " +
			"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER"},
	} {
		var invalid *provider.InvalidRequestError
		if _, err := invoke(t, core, tt.operation, "open", tt.arguments, false); !errors.As(err, &invalid) ||
			err.Error() != tt.want {
			t.Errorf("%s with a malformed target = %v, want invalid-request %q", tt.operation, err, tt.want)
		}
	}

	// A project GitHub leaves out, with or without a NOT_FOUND error, is not-found naming the project; an
	// explicit refusal of the token stays permission and names it as well.
	for _, tt := range []struct {
		name, answer string
		class        provider.Class
		want         string
	}{
		{"not found", `{"data":{"owner":{"projectV2":null}},"errors":[{"type":"NOT_FOUND",` +
			`"path":["owner","projectV2"],"message":"Could not resolve to a ProjectV2 with the number 3."}]}`,
			provider.ClassNotFound, "list project items: GitHub does not hold project users/octocat/projects/3 " +
				"or does not show it to this token; check the name, and that the token can see it (classic: " +
				"scope read:project; fine-grained: Projects access of its organization, as a user-owned project " +
				"needs a classic token)"},
		{"left out", `{"data":{"owner":{"projectV2":null}}}`, provider.ClassNotFound,
			"GitHub does not hold project users/octocat/projects/3 or does not show it to this token"},
		{"refused", `{"data":{"owner":{"projectV2":null}},"errors":[{"type":"FORBIDDEN",` +
			`"path":["owner","projectV2"],"message":"Resource not accessible by personal access token"}]}`,
			provider.ClassPermission, "this GitHub token may not read project users/octocat/projects/3; " +
				"check its scopes or permissions"},
	} {
		answer := tt.answer
		f.failure = func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/api/graphql" {
				return false
			}
			_, _ = w.Write([]byte(answer))
			return true
		}
		_, err := invoke(t, core, "github.project-items.list", "open", `{"project":"users/octocat/projects/3"}`, false)
		if classOf(err) != tt.class || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s = %v (class %q), want %q with %q", tt.name, err, classOf(err), tt.class, tt.want)
		}
	}
	f.failure = nil

	// A REST 404 of the repository itself names it without a resource inside.
	c, err := open(context.Background(), resolvedConnection("gh", base, "repos/octo-org/absent"), resolver(red, nil), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	err = c.rest(context.Background(), "test connection", "/repos/octo-org/absent", &struct{}{})
	if want := "test connection: GitHub does not hold repository octo-org/absent or does not show it to this " +
		"token" + repositoryHint; classOf(err) != provider.ClassNotFound || err.Error() != want {
		t.Errorf("a repository GitHub does not show = %v, want %q", err, want)
	}
	if class, err := TestConnection(context.Background(), resolvedConnection("gh", base, "repos/octo-org/absent"),
		resolver(red, nil), red); err != nil || class != provider.ClassNotFound {
		t.Errorf("test of an absent repository = %q, %v; want not-found", class, err)
	}
}

// A state the request fixes is left out of every listed issue; without it, or with all, it stays.
func TestIssueListDropsTheStateAFilterFixes(t *testing.T) {
	f := &fakeGitHub{issues: []fakeIssue{{number: 1, state: "open"}}}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), targetsConfig(base), resolver(red, &reads), red)
	for arguments, want := range map[string]bool{
		`{"repository":"octo-org/example","state":"open"}`: false,
		`{"repository":"octo-org/example","state":"all"}`:  true,
		`{"repository":"octo-org/example"}`:                true,
	} {
		out, err := invoke(t, core, "github.issues.list", "owner", arguments, false)
		if err != nil {
			t.Fatalf("%s: %v", arguments, err)
		}
		if got := strings.Contains(string(out), `"state"`); got != want {
			t.Errorf("%s: state present = %v in %s, want %v", arguments, got, out, want)
		}
	}
}

func mustAllowlist(t *testing.T, targets, excludes []string) allowlist {
	t.Helper()
	list, err := parseAllowlistExcluding(targets, excludes)
	if err != nil {
		t.Fatalf("parseAllowlistExcluding(%v, %v) = %v", targets, excludes, err)
	}
	return list
}

// An exclusion wins over every target: an owner against its own pattern, a project number against a project
// pattern, a repository against a repository pattern; users/X and orgs/X are one login, and names ignore case.
func TestExclusionsWinOverTargets(t *testing.T) {
	repo := func(owner, name string) target { return target{kind: kindRepository, owner: owner, repo: name} }
	project := func(scope, owner string, number int) target {
		return target{kind: kindProject, scope: scope, owner: owner, number: number}
	}
	owner := func(scope, login string) target { return target{kind: kindOwner, scope: scope, owner: login} }
	for _, tt := range []struct {
		name              string
		targets, excludes []string
		allowed           []target
		refused           []target
	}{
		{"owner against a repository pattern", []string{"repos/octo-org/*"}, []string{"orgs/octo-org"},
			nil, []target{repo("octo-org", "a"), repo("OCTO-ORG", "b")}},
		{"user spelling excludes the same login", []string{"repos/octo-org/*"}, []string{"users/Octo-Org"},
			nil, []target{repo("octo-org", "a"), owner("orgs", "octo-org"), owner("users", "octo-org")}},
		{"owner against a project pattern", []string{"orgs/octo-org/projects/*"}, []string{"users/octo-org"},
			nil, []target{project("orgs", "octo-org", 3)}},
		{"project against a project pattern", []string{"orgs/octo-org/projects/*"},
			[]string{"orgs/octo-org/projects/3"},
			[]target{project("orgs", "octo-org", 4)}, []target{project("orgs", "OCTO-ORG", 3)}},
		{"repository against an owner target", []string{"orgs/octo-org", "repos/octo-org/*"},
			[]string{"repos/octo-org/Secret"},
			[]target{repo("octo-org", "open"), owner("orgs", "octo-org")}, []target{repo("octo-org", "secret")}},
		{"repository pattern as an exclusion", []string{"orgs/octo-org", "repos/other/x"},
			[]string{"repos/octo-org/*"}, []target{repo("other", "x")}, []target{repo("octo-org", "a")}},
		{"project pattern as an exclusion leaves repositories", nil, []string{"orgs/octo-org/projects/*"},
			[]target{repo("octo-org", "a"), owner("orgs", "octo-org")}, []target{project("orgs", "octo-org", 1)}},
		{"only exclusions leave the rest open", nil, []string{"repos/octo-org/a"},
			[]target{repo("octo-org", "b"), repo("hubot", "a"), project("users", "hubot", 1)},
			[]target{repo("octo-org", "a")}},
		{"an exclusion that matches nothing is harmless", []string{"repos/octo-org/*"}, []string{"orgs/nobody"},
			[]target{repo("octo-org", "a")}, nil},
	} {
		list := mustAllowlist(t, tt.targets, tt.excludes)
		for _, ok := range tt.allowed {
			if !list.allows(ok) {
				t.Errorf("%s: allows(%v) = false, want true", tt.name, ok)
			}
		}
		for _, no := range tt.refused {
			if list.allows(no) {
				t.Errorf("%s: allows(%v) = true, want false", tt.name, no)
			}
		}
	}
}

func TestExclusionValidity(t *testing.T) {
	for _, tt := range []struct {
		name              string
		targets, excludes []string
		ok                bool
	}{
		{"star alone", nil, []string{"*"}, false},
		{"star at a placeholder", nil, []string{"repos/*/x"}, false},
		{"star as owner", nil, []string{"repos/*/*"}, false},
		{"star as login", nil, []string{"users/*"}, false},
		{"star project owner", nil, []string{"orgs/*/projects/1"}, false},
		{"equals a target", []string{"repos/octo-org/a"}, []string{"repos/OCTO-ORG/a"}, false},
		{"owner equals a target in the other scope", []string{"orgs/octo-org"}, []string{"users/octo-org"}, false},
		{"pattern equals a pattern target", []string{"repos/octo-org/*"}, []string{"repos/octo-org/*"}, false},
		{"duplicate", nil, []string{"repos/octo-org/a", "repos/octo-org/A"}, false},
		{"repository pattern", nil, []string{"repos/octo-org/*"}, true},
		{"project pattern", nil, []string{"users/octocat/projects/*", "orgs/octo-org/projects/*"}, true},
		{"matches nothing", []string{"repos/octo-org/a"}, []string{"repos/hubot/b"}, true},
		{"narrower than a target", []string{"repos/octo-org/*"}, []string{"repos/octo-org/a"}, true},
	} {
		_, err := parseAllowlistExcluding(tt.targets, tt.excludes)
		if (err == nil) != tt.ok {
			t.Errorf("%s: error = %v, want ok=%t", tt.name, err, tt.ok)
		}
		if err != nil {
			for _, value := range append(append([]string{}, tt.targets...), tt.excludes...) {
				if strings.Contains(err.Error(), value) && len(value) > 1 {
					t.Errorf("%s: error quotes %q: %v", tt.name, value, err)
				}
			}
		}
	}
}

// Exclusions never withhold a tool: only targets decide the account-wide check and the kinds a list names.
func TestExclusionsLeaveTheToolOfferAlone(t *testing.T) {
	list := mustAllowlist(t, nil, []string{"orgs/octo-org", "repos/hubot/a", "orgs/x/projects/1"})
	if err := accountWideAllowed(list); err != nil {
		t.Errorf("accountWideAllowed with only exclusions = %v", err)
	}
	if list.names(kindRepository) || list.names(kindProject) || list.names(kindOwner) || list.bound() || list.open() {
		t.Errorf("exclusions counted as targets: %+v", list)
	}

	f := &fakeGitHub{}
	cfg := coreConfig(serve(t, f))
	all := config.Effects()
	cfg.Connections["plain"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all}
	cfg.Connections["excl"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all,
		Exclude: []string{"orgs/octo-org", "repos/hubot/a", "users/hubot/projects/*"}}
	cfg.Connections["owners"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all,
		Targets: []string{"orgs/octo-org", "users/octocat"}}
	cfg.Connections["owners-excl"] = config.Connection{Service: "gh", Credential: "gh-reader", Effects: all,
		Targets: []string{"orgs/octo-org", "users/octocat"}, Exclude: []string{"repos/octo-org/*", "users/hubot"}}
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	listing := func(connection string) string {
		tools, err := core.Tools(application.SearchRequest{Provider: Provider, Connection: connection})
		if err != nil {
			t.Fatalf("tools of %s = %v", connection, err)
		}
		tools.Connections = nil
		encoded, _ := json.Marshal(tools)
		return string(encoded)
	}
	for _, pair := range [][2]string{{"plain", "excl"}, {"owners", "owners-excl"}} {
		if a, b := listing(pair[0]), listing(pair[1]); a != b {
			t.Errorf("%s and %s offer different tools", pair[0], pair[1])
		}
	}
}

// A default target that an exclusion covers is refused, and so is an explicit one, without quoting it.
func TestExclusionsRefuseTheChosenTarget(t *testing.T) {
	list := mustAllowlist(t, []string{"repos/octo-org/a"}, []string{"orgs/octo-org"})
	if _, err := list.choose(kindRepository, ""); !isInvalidRequest(err) {
		t.Errorf("default repository = %v, want an invalid request", err)
	}
	list = mustAllowlist(t, nil, []string{"repos/octo-org/secret", "orgs/hidden/projects/*", "users/hubot"})
	for _, call := range []struct {
		kind  targetKind
		value string
	}{{kindRepository, "Octo-Org/SECRET"}, {kindProject, "orgs/hidden/projects/2"}, {kindRepository, "hubot/x"}} {
		_, err := list.choose(call.kind, call.value)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "ecret") || strings.Contains(err.Error(), "hidden") ||
			strings.Contains(err.Error(), "hubot") {
			t.Errorf("choose(%v) = %v, want an invalid request without the name", call.value, err)
		}
	}
	if _, err := list.choose(kindRepository, "octo-org/other"); err != nil {
		t.Errorf("choose of a free repository = %v", err)
	}
	for _, value := range []string{"users/hubot", "orgs/hubot", "users/HUBOT"} {
		if _, err := list.chooseOwner(kindOwner, value); !isInvalidRequest(err) {
			t.Errorf("chooseOwner(%s) = %v, want an invalid request", value, err)
		}
		if _, err := list.chooseOwner(kindRepository, value); !isInvalidRequest(err) {
			t.Errorf("chooseOwner(%s, repositories) = %v, want an invalid request", value, err)
		}
	}
	// An owner whose projects alone are excluded stays usable for repositories.
	list = mustAllowlist(t, nil, []string{"orgs/octo-org/projects/*"})
	if _, err := list.chooseOwner(kindRepository, "orgs/octo-org"); err != nil {
		t.Errorf("chooseOwner with a project pattern excluded = %v", err)
	}
	// The selectors share the check and refuse before any secret access.
	resolved := resolvedConnection("gh", "https://example.invalid", "")
	resolved.Exclude = []string{"orgs/octo-org", "repos/hubot/*"}
	for _, raw := range []string{`{"owner":"orgs/octo-org"}`} {
		if _, err := selectOwner(resolved, kindOwner, json.RawMessage(raw)); !isInvalidRequest(err) {
			t.Errorf("selectOwner = %v", err)
		}
		if _, err := selectOrganization(resolved, json.RawMessage(raw)); !isInvalidRequest(err) {
			t.Errorf("selectOrganization = %v", err)
		}
	}
	if _, err := selectTarget(resolved, kindRepository, json.RawMessage(`{"repository":"hubot/x"}`)); !isInvalidRequest(err) {
		t.Errorf("selectTarget = %v", err)
	}
	bound, _ := parseArgument(kindRepository, "octocat/main")
	if _, err := selectSecondaryRepository(resolved, bound, "octo-org/x"); !isInvalidRequest(err) {
		t.Errorf("selectSecondaryRepository = %v", err)
	}
	if _, err := selectSecondaryRepository(resolved, bound, "octocat/free"); err != nil {
		t.Errorf("selectSecondaryRepository of a free repository = %v", err)
	}
}

// While a connection excludes anything, every GraphQL lookup of a repository stops following renames; without
// an exclusion the documents stay as written.
func TestExclusionsPinGraphQLRepositoryLookups(t *testing.T) {
	f := &fakeGitHub{}
	cfg := coreConfig(serve(t, f))
	cfg.Connections["plain"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget}
	cfg.Connections["ex"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Exclude: []string{"repos/octo-org/other"}}
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	for connection, want := range map[string]bool{"plain": false, "ex": true} {
		before := len(f.recorded())
		if _, err := invoke(t, core, issuesList.ID, connection, `{}`, false); err != nil {
			t.Fatalf("issues list on %s = %v", connection, err)
		}
		documents := 0
		for _, request := range f.recorded()[before:] {
			if request.document == "" {
				continue
			}
			documents++
			if got := strings.Contains(request.document, "repository(owner:$owner,name:$name,followRenames:false)"); got != want {
				t.Errorf("%s: followRenames:false = %t, want %t in %s", connection, got, want, request.document)
			}
			if !want && strings.Contains(request.document, "followRenames") {
				t.Errorf("%s: document changed: %s", connection, request.document)
			}
		}
		if documents == 0 {
			t.Errorf("%s: no GraphQL document was sent", connection)
		}
	}

	// Every lookup shape of the package is rewritten, also one split over lines or spaced out.
	client := &Client{allowed: mustAllowlist(t, nil, []string{"repos/octo-org/other"})}
	for in, out := range map[string]string{
		"{repository(owner:$owner,name:$name){id}}":            "{repository(owner:$owner,name:$name,followRenames:false){id}}",
		"{repository(owner:$repoOwner,\n name:$repoName){id}}": "{repository(owner:$repoOwner,\n name:$repoName,followRenames:false){id}}",
		"{node(id:$id){id}}":                                   "{node(id:$id){id}}",
	} {
		if got := client.pinRepositories(in); got != out {
			t.Errorf("pinRepositories(%q) = %q, want %q", in, got, out)
		}
	}
	if got := (&Client{}).pinRepositories("{repository(owner:$owner,name:$name){id}}"); strings.Contains(got, "followRenames") {
		t.Errorf("a client without exclusions changed the document: %s", got)
	}
}
