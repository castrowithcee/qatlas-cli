package github

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeIssueType is one issue type of the organization octo-org declares.
type fakeIssueType struct {
	id                       int64
	name, description, color string
	enabled                  bool
}

// github.issuetypes.list reads every issue type of an organization an explicit connection allows: an
// explicit owner target, a repository target of that organization, or a connection without targets allow
// the call; the result names the organization it read, the way every owner tool does.
func TestIssueTypesListSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{issueTypes: []fakeIssueType{
		{id: 1, name: "Bug", description: "Something broken", color: "red", enabled: true},
		{id: 2, name: "Task", enabled: true},
	}}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	// An owner target of the connection is the default.
	result, err := invoke(t, core, issueTypesList.ID, "org", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"id":1`) || !strings.Contains(string(result), `"name":"Bug"`) ||
		!strings.Contains(string(result), `"description":"Something broken"`) ||
		!strings.Contains(string(result), `"color":"red"`) || !strings.Contains(string(result), `"is_enabled":true`) ||
		!strings.Contains(string(result), `"name":"Task"`) || !strings.Contains(string(result), `"owner":"orgs/octo-org"`) {
		t.Fatalf("%s = %s, %v", issueTypesList.ID, result, err)
	}

	// A connection without targets allows any organization named explicitly.
	result, err = invoke(t, core, issueTypesList.ID, "open", `{"owner":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(result), `"name":"Bug"`) {
		t.Fatalf("open connection = %s, %v", result, err)
	}

	// A repository target of the organization allows the call as well, the way it allows
	// github.repositories.list to list the owner's repositories.
	result, err = invoke(t, core, issueTypesList.ID, "starrepo", `{"owner":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(result), `"name":"Bug"`) {
		t.Fatalf("repository-target connection = %s, %v", result, err)
	}
}

// Every refusal of the owner or its scope settles before a secret is read and before GitHub is contacted; a
// project-only connection refuses the call, the engere Auslegung of the target rule.
func TestIssueTypesListRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, connection, arguments, message string }{
		{"no owner and none the targets name exactly", "open", `{}`, "owner is required"},
		{"a user owner", "user", `{}`, "an organization, not a user"},
		{"a user owner argument on an open connection", "open", `{"owner":"users/octocat"}`,
			"an organization, not a user"},
		{"an owner outside the targets", "org", `{"owner":"orgs/other"}`, "outside the targets"},
		{"a project-only connection", "planning", `{"owner":"orgs/octo-org"}`, "outside the targets"},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, issueTypesList.ID, tt.connection, tt.arguments, false)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message) {
			t.Errorf("%s: err = %v, want an invalid request naming %q", tt.name, err, tt.message)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}

// A token without read:org is refused with a message naming what reading the issue types of an organization
// needs.
func TestIssueTypesListReportsThePermissionItNeeds(t *testing.T) {
	f := &fakeGitHub{issueTypesForbidden: true}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	_, err := invoke(t, core, issueTypesList.ID, "org", `{}`, false)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read:org") {
		t.Errorf("%s = %v, want a permission refusal naming read:org", issueTypesList.ID, err)
	}
}
