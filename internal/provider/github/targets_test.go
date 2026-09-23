package github

import (
	"context"
	"encoding/json"
	"net/http"
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

// remotes makes the working directory a git repository with these remotes for the rest of the test.
func remotes(t *testing.T, byName map[string][]string) {
	t.Helper()
	previous := workingRemotes
	workingRemotes = func(context.Context) map[string][]string { return byName }
	t.Cleanup(func() { workingRemotes = previous })
}

// A target argument may be left out when the targets allow exactly one of its kind, and a repository also
// when the GitHub remote of the working directory lies inside them on the configured host. An explicit
// argument wins, and a default never widens the targets.
func TestTargetDefaultsNeverWidenTheTargets(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	host := strings.Split(strings.TrimPrefix(base, "https://"), ":")[0]
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

	for name, byName := range map[string]map[string][]string{
		"an https origin":             {"origin": {"https://" + host + "/octo-org/example.git"}, "fork": {"x"}},
		"an ssh origin":               {"origin": {"ssh://git@" + host + ":22/octo-org/example"}},
		"the only remote in scp form": {"upstream": {"git@" + host + ":octo-org/example.git"}},
	} {
		remotes(t, byName)
		if path, err := issue("open", `{"number":42}`); err != nil || path != want {
			t.Errorf("%s = %q, %v; want the remote as default", name, path, err)
		}
	}

	remotes(t, map[string][]string{"origin": {"git@" + host + ":octo-org/other.git"}})
	if path, err := issue("owner", `{"number":42,"repository":"octo-org/example"}`); err != nil || path != want {
		t.Errorf("an explicit repository = %q, %v; want it to win over the remote", path, err)
	}

	for name, byName := range map[string]map[string][]string{
		"a remote outside the targets": {"origin": {"https://" + host + "/other-org/example.git"}},
		"a remote of another host":     {"origin": {"https://github.com/octo-org/example.git"}},
		"two remotes without origin":   {"a": {"git@" + host + ":octo-org/example"}, "b": {"git@" + host + ":octo-org/x"}},
		"an origin with two urls":      {"origin": {"git@" + host + ":octo-org/example", "git@" + host + ":octo-org/x"}},
		"a remote with a deeper path":  {"origin": {"https://" + host + "/octo-org/example/extra"}},
		"no git repository":            nil,
	} {
		remotes(t, byName)
		reads = 0
		before := len(f.recorded())
		if _, err := issue("owner", `{"number":42}`); !isInvalidRequest(err) ||
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
	both := map[string]bool{itemsAdd.ID: true, projectIssuesCreate.ID: true}
	for _, descriptor := range registry(t).Provider(Provider) {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(descriptor.InputSchema, &schema); err != nil {
			t.Fatalf("%s schema = %v", descriptor.ID, err)
		}
		names := []string{"repository"}
		if strings.HasPrefix(descriptor.ID, Provider+".project") {
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
						strings.Contains(argument.Description, "inside the targets")
				}
			}
			if !found {
				t.Errorf("%s does not describe %s as an optional target", descriptor.ID, name)
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

func TestRemotesAreReadWithoutGuessing(t *testing.T) {
	for raw, want := range map[string][2]string{
		"https://github.com/octo-org/example.git":         {"github.com", "/octo-org/example.git"},
		"https://user:secret@github.com/octo-org/example": {"github.com", "/octo-org/example"},
		"ssh://git@github.com:22/octo-org/example.git":    {"github.com", "/octo-org/example.git"},
		"git@github.com:octo-org/example.git":             {"github.com", "octo-org/example.git"},
		"github.com:octo-org/example":                     {"github.com", "octo-org/example"},
	} {
		host, path, ok := splitRemote(raw)
		if !ok || host != want[0] || path != want[1] {
			t.Errorf("splitRemote(%q) = %q, %q, %v", raw, host, path, ok)
		}
	}
	for _, raw := range []string{"/srv/git/example.git", "./example", "file:///srv/git/example.git"} {
		if host, _, ok := splitRemote(raw); ok && host != "" {
			t.Errorf("splitRemote(%q) = %q, want no host", raw, host)
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
