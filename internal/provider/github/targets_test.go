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
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	cfg.Connections["owner"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/*"}, Permissions: all}
	cfg.Connections["single"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget, repoTarget}, Permissions: all}
	cfg.Connections["mixed"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget, "repos/octo-org/*"}, Permissions: all}
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
		if _, err := invoke(t, core, "github.projectitems.add", tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}

	result, err := invoke(t, core, "github.projectitems.add", "mixed", `{"repository":"octo-org/example","number":42}`, true)
	if err != nil || !strings.Contains(string(result), "PVTI_for_I_example_42") {
		t.Fatalf("an allowed issue = %s, %v", result, err)
	}
	result, err = invoke(t, core, "github.projectitems.add", "open",
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
		case descriptor.ID == projectsList.ID || descriptor.ID == repositoriesList.ID ||
			descriptor.ID == projectsCreate.ID:
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
	result, err = invoke(t, core, "github.projectitems.list", "single", `{}`, false)
	if err != nil || named(result, "project") != projectTarget || named(result, "repository") != "" {
		t.Errorf("the only project = %s, %v; want it named in the result", result, err)
	}
	result, err = invoke(t, core, "github.projectitems.add", "mixed", `{"repository":"octo-org/example","number":42}`, true)
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
		{"github.projectitems.list", `{"project":"octocat/3"}`, "$.project does not have the required form " +
			"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER"},
	} {
		var invalid *application.InvalidRequestError
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
		_, err := invoke(t, core, "github.projectitems.list", "open", `{"project":"users/octocat/projects/3"}`, false)
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
