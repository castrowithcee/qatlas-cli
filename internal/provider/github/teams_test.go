package github

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestOrganizationToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{
		orgTeams: []fakeTeam{
			{slug: "design", name: "Design", description: "Design team", privacy: "closed"},
			{slug: "ops", name: "Operations", privacy: "secret"},
		},
		teamMembers: map[string][]string{"design": {"octocat", "hubot"}},
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, organizationTeamsList.ID, "org", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"team":"design"`) ||
		!strings.Contains(string(result), `"name":"Design"`) || !strings.Contains(string(result), `"privacy":"closed"`) ||
		!strings.Contains(string(result), `"team":"ops"`) {
		t.Fatalf("%s = %s, %v", organizationTeamsList.ID, result, err)
	}

	result, err = invoke(t, core, teamMembersList.ID, "org", `{"team":"design"}`, false)
	if err != nil || !strings.Contains(string(result), `"login":"octocat"`) ||
		!strings.Contains(string(result), `"login":"hubot"`) {
		t.Fatalf("%s = %s, %v", teamMembersList.ID, result, err)
	}

	// An explicit owner argument that matches the connection's own owner target still works.
	result, err = invoke(t, core, organizationTeamsList.ID, "org", `{"owner":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(result), `"team":"design"`) {
		t.Fatalf("%s with an explicit owner = %s, %v", organizationTeamsList.ID, result, err)
	}
}

// Every refusal the owner, the team slug, or the scope settles before a secret is read and before GitHub is
// contacted; teams belong to an organization, so a user owner is refused even where the connection's targets
// allow it.
func TestOrganizationToolRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, operation, connection, arguments, message string
	}{
		{"no owner and none the targets name exactly", organizationTeamsList.ID, "open", `{}`,
			"owner is required"},
		{"a user owner", organizationTeamsList.ID, "user", `{}`, "teams belong to an organization"},
		{"a user owner argument on an open connection", organizationTeamsList.ID, "open",
			`{"owner":"users/octocat"}`, "teams belong to an organization"},
		{"an owner outside the targets", organizationTeamsList.ID, "org", `{"owner":"orgs/other"}`,
			"outside the targets"},
		{"an invalid team slug", teamMembersList.ID, "org", `{"team":"not a slug"}`, ""},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, false)
		if !isInvalidRequest(err) || (tt.message != "" && !strings.Contains(err.Error(), tt.message)) {
			t.Errorf("%s: err = %v, want an invalid request naming %q", tt.name, err, tt.message)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}

// A token without read:org is refused with a message naming what reading the organization's teams needs.
func TestOrganizationToolsReportThePermissionTheyNeed(t *testing.T) {
	f := &fakeGitHub{orgTeamsForbidden: true}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	_, err := invoke(t, core, organizationTeamsList.ID, "org", `{}`, false)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read:org") {
		t.Errorf("%s = %v, want a permission refusal naming read:org", organizationTeamsList.ID, err)
	}
}
