package github

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// discoveryConfig adds the connections the account, organization, and star tools meet to coreConfig's
// project and repository connections: one with no targets at all, one whose targets name only the
// organization octo-org, one whose targets name only the user octocat, and one whose targets name the
// repository the star change tools act on with every permission.
func discoveryConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := config.Permissions()
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	cfg.Connections["org"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all}
	cfg.Connections["user"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"users/octocat"}, Permissions: all}
	cfg.Connections["starrepo"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{repoTarget}, Permissions: all}
	return cfg
}

func TestAccountSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, accountsMe.ID, "open", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"login":"octocat"`) ||
		!strings.Contains(string(result), `"name":"The Octocat"`) || !strings.Contains(string(result), `"type":"User"`) ||
		!strings.Contains(string(result), `"plan":"pro"`) {
		t.Fatalf("%s = %s, %v", accountsMe.ID, result, err)
	}
	// A connection whose targets name only an owner still reads the account behind its token.
	if _, err := invoke(t, core, accountsMe.ID, "org", `{}`, false); err != nil {
		t.Errorf("%s on a connection with only an owner target = %v, want it allowed", accountsMe.ID, err)
	}
}

// A connection whose targets name a repository or a project may not read the account behind its token as a
// whole, because that account may belong to a different customer than the one its targets name; the refusal
// settles before a credential is resolved.
func TestAccountRefusalBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, connection := range []string{"repo", "planning"} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, accountsMe.ID, connection, `{}`, false); !isInvalidRequest(err) ||
			!strings.Contains(err.Error(), "name a repository or a project") {
			t.Errorf("%s on %s = %v, want an invalid request", accountsMe.ID, connection, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on %s reached the credential or GitHub", accountsMe.ID, connection)
		}
	}
}
