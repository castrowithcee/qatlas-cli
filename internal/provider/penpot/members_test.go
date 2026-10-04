package penpot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	memberOwner   = "00000000-0000-0000-0000-000000000061"
	memberEditor  = "00000000-0000-0000-0000-000000000062"
	memberViewer  = "00000000-0000-0000-0000-000000000063"
	newTeamID     = "00000000-0000-0000-0000-0000000000c1"
	ownerEmail    = "owner@example.com"
	memberCanary  = "name-canary-photo"
	inviteeCanary = "invitee@example.com"
)

var teamTools = []string{membersList.ID, membersSetRole.ID, membersRemove.ID, invitationsCreate.ID, teamsCreate.ID, teamsDelete.ID}

func membersBody() string {
	return `[{"id":"` + memberOwner + `","team-id":"` + teamA + `","email":"` + ownerEmail + `","name":"` + memberCanary +
		`","fullname":"` + memberCanary + `","photo-id":"` + memberCanary + `","is-active":true,"is-owner":true,"is-admin":true,"can-edit":true},` +
		`{"id":"` + memberEditor + `","email":"editor@example.com","is-active":true,"is-owner":false,"is-admin":false,"can-edit":true},` +
		`{"id":"` + memberViewer + `","email":"viewer@example.com","is-active":false,"is-owner":false,"is-admin":false,"can-edit":false}]`
}

// teamHandler lists members and answers every change with the given status and body.
func teamHandler(status int, answer string) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		if isChange(c.command()) {
			return jsonResponse(status, answer), nil
		}
		if c.command() == cmdTeamMembers {
			return jsonResponse(200, membersBody()), nil
		}
		return jsonResponse(200, teamsBody()), nil
	}
}

func teamArgs() map[string]string {
	return map[string]string{
		membersList.ID:       `{"team_id":"` + teamA + `"}`,
		membersSetRole.ID:    `{"team_id":"` + teamA + `","member_id":"` + memberEditor + `","role":"viewer"}`,
		membersRemove.ID:     `{"team_id":"` + teamA + `","member_id":"` + memberEditor + `"}`,
		invitationsCreate.ID: `{"team_id":"` + teamA + `","emails":["` + inviteeCanary + `"],"role":"editor"}`,
		teamsCreate.ID:       `{"name":"Customer C"}`,
		teamsDelete.ID:       `{"team_id":"` + teamA + `"}`,
	}
}

func TestMembersListReducesToRoleAndEmail(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, teamHandler(200, ``))
	result, err := env.invoke(membersList.ID, "one", teamArgs()[membersList.ID])
	if err != nil || strings.Contains(result, memberCanary) || !strings.Contains(result, ownerEmail) ||
		!strings.Contains(result, `"role":"owner"`) || !strings.Contains(result, `"role":"editor"`) ||
		!strings.Contains(result, `"role":"viewer"`) || !strings.Contains(result, `"count":3`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if len(calls) != 1 || calls[0].command() != cmdTeamMembers || calls[0].method != http.MethodPost ||
		calls[0].body["team-id"] != teamA {
		t.Fatalf("calls = %+v", calls)
	}
	if membersList.Risk.Effect != "read" || membersList.Risk.Idempotency != "safe" || membersList.Risk.DataSensitivity != membersSensitive {
		t.Fatalf("risk = %+v", membersList.Risk)
	}
}

func TestTeamToolsRefuseForeignTeamBeforeSecretAndIO(t *testing.T) {
	args := map[string]string{
		membersList.ID:       `{"team_id":"` + teamForeign + `"}`,
		membersSetRole.ID:    `{"team_id":"` + teamForeign + `","member_id":"` + memberEditor + `","role":"viewer"}`,
		membersRemove.ID:     `{"team_id":"` + teamForeign + `","member_id":"` + memberEditor + `"}`,
		invitationsCreate.ID: `{"team_id":"` + teamForeign + `","emails":["a@example.com"],"role":"editor"}`,
		teamsDelete.ID:       `{"team_id":"` + teamForeign + `"}`,
	}
	for tool, arguments := range args {
		var calls []call
		env := newEnvironment(t, &calls, teamHandler(200, `{}`))
		_, err := env.invokeConfirmed(tool, "write", arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), teamForeign) || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, calls = %d, reads = %d", tool, err, len(calls), *env.reads)
		}
	}
}

func TestTeamChangesSendOneFixedCommand(t *testing.T) {
	for _, test := range []struct {
		tool, command, want string
		body                map[string]any
		answer              string
	}{
		{membersSetRole.ID, cmdSetMemberRole, `"updated":true`,
			map[string]any{"team-id": teamA, "member-id": memberEditor, "role": "viewer"}, `null`},
		{membersRemove.ID, cmdDeleteMember, `"removed":true`,
			map[string]any{"team-id": teamA, "member-id": memberEditor}, `null`},
		{invitationsCreate.ID, cmdCreateInvitation, `"invited":1`,
			map[string]any{"team-id": teamA, "role": "editor"}, `{"total":1,"invitations":[{"email":"` + inviteeCanary + `"}]}`},
		{teamsCreate.ID, cmdCreateTeam, newTeamID, map[string]any{"name": "Customer C"}, `{"id":"` + newTeamID + `","name":"Customer C"}`},
		{teamsDelete.ID, cmdDeleteTeam, `"deleted":true`, map[string]any{"id": teamA}, `null`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, teamHandler(200, test.answer))
		result, err := env.invokeConfirmed(test.tool, "write", teamArgs()[test.tool])
		if err != nil || !strings.Contains(result, test.want) || strings.Contains(result, inviteeCanary) {
			t.Errorf("%s: result = %s, err = %v", test.tool, result, err)
			continue
		}
		sent := changes(calls)
		if len(sent) != 1 || len(calls) != 1 || sent[0].command() != test.command {
			t.Errorf("%s: calls = %+v", test.tool, calls)
			continue
		}
		for key, want := range test.body {
			if sent[0].body[key] != want {
				t.Errorf("%s: body[%s] = %v, want %v", test.tool, key, sent[0].body[key], want)
			}
		}
		if test.tool == invitationsCreate.ID {
			emails, _ := sent[0].body["emails"].([]any)
			if len(emails) != 1 || emails[0] != inviteeCanary {
				t.Errorf("emails = %v", sent[0].body["emails"])
			}
		}
	}
}

func TestTeamChangesNeedConfirmAndToolAllowList(t *testing.T) {
	for _, tool := range teamTools[1:] {
		var calls []call
		env := newEnvironment(t, &calls, teamHandler(200, `{"id":"`+newTeamID+`","total":1}`))
		if _, err := env.invoke(tool, "write", teamArgs()[tool]); err == nil || len(calls) != 0 {
			t.Errorf("%s unconfirmed: err = %v, calls = %d", tool, err, len(calls))
		}
		// "one" has read permission only, "nodelete" all permissions but a tool list without the team tools
		for _, connection := range []string{"one", "nodelete"} {
			if _, err := env.invokeConfirmed(tool, connection, teamArgs()[tool]); err == nil || len(calls) != 0 {
				t.Errorf("%s on %s: err = %v, calls = %d", tool, connection, err, len(calls))
			}
		}
	}
	for descriptor, required := range map[*capability.Descriptor]bool{&membersSetRole: true, &membersRemove: true,
		&teamsDelete: true, &teamsCreate: true, &invitationsCreate: false, &membersList: false} {
		if descriptor.RequiresToolAllowList != required {
			t.Errorf("%s: RequiresToolAllowList = %t, want %t", descriptor.ID, descriptor.RequiresToolAllowList, required)
		}
	}
}

func TestTeamsCreateNeedsToolListEntryAndChangesNoTargets(t *testing.T) {
	// Permissions alone never offer teams.create: without a tools entry the call is refused before any I/O.
	var calls []call
	env := newEnvironment(t, &calls, teamHandler(200, `{"id":"`+newTeamID+`"}`))
	cfg := testConfig()
	connection := cfg.Connections["write"]
	connection.Tools = []string{projectsCreate.ID}
	cfg.Connections["write"] = connection
	env.core = application.New(registry(t), withFiles(cfg, env.read, env.write), resolver(env.red, env.reads), env.red)
	if _, err := env.invokeConfirmed(teamsCreate.ID, "write", `{"name":"Customer C"}`); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("without tool entry: err = %v, calls = %d", err, len(calls))
	}
	// With the entry it creates the team, names its ID, and leaves the targets and the team list alone.
	env = newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdTeams {
			return jsonResponse(200, `[{"id":"`+teamA+`","name":"Kunde A"},{"id":"`+newTeamID+`","name":"Customer C"}]`), nil
		}
		return teamHandler(200, `{"id":"`+newTeamID+`"}`)(c)
	})
	result, err := env.invokeConfirmed(teamsCreate.ID, "write", `{"name":"Customer C"}`)
	if err != nil || !strings.Contains(result, newTeamID) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := testConfig().Connections["write"].Targets; len(got) != 1 || got[0] != "team/"+teamA {
		t.Fatalf("targets = %v", got)
	}
	listed, err := env.invoke(teamsList.ID, "one", `{}`)
	if err != nil || strings.Contains(listed, newTeamID) {
		t.Fatalf("teams.list = %s, err = %v", listed, err)
	}
	if _, err := env.invokeConfirmed(membersList.ID, "write", `{"team_id":"`+newTeamID+`"}`); !isInvalidRequest(err) {
		t.Fatalf("the new team must stay outside the targets: %v", err)
	}
}

func TestTeamChangesRefuseProjectAllowList(t *testing.T) {
	for _, tool := range []string{membersSetRole.ID, membersRemove.ID, invitationsCreate.ID, teamsCreate.ID, teamsDelete.ID} {
		var calls []call
		env := newEnvironment(t, &calls, teamHandler(200, `{}`))
		_, err := env.invokeConfirmed(tool, "writenarrow", teamArgs()[tool])
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, calls = %d, reads = %d", tool, err, len(calls), *env.reads)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, teamHandler(200, ``))
	if _, err := env.invoke(membersList.ID, "writenarrow", teamArgs()[membersList.ID]); err != nil {
		t.Errorf("members.list on a narrow connection: %v", err)
	}
}

func TestTeamChangesRefuseBadInput(t *testing.T) {
	many := `"a1@example.com"`
	for i := 2; i <= 26; i++ {
		many += `,"a` + strings.Repeat("b", i) + `@example.com"`
	}
	for name, test := range map[string]struct{ tool, args string }{
		"owner role":        {membersSetRole.ID, `{"team_id":"` + teamA + `","member_id":"` + memberEditor + `","role":"owner"}`},
		"bad member":        {membersSetRole.ID, `{"team_id":"` + teamA + `","member_id":"x","role":"viewer"}`},
		"remove bad member": {membersRemove.ID, `{"team_id":"` + teamA + `","member_id":"x"}`},
		"invite admin":      {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":["a@example.com"],"role":"admin"}`},
		"invite owner":      {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":["a@example.com"],"role":"owner"}`},
		"no emails":         {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":[],"role":"editor"}`},
		"bad email":         {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":["not-an-email-canary"],"role":"editor"}`},
		"display name":      {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":["Bob <b@example.com>"],"role":"editor"}`},
		"two addresses":     {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":["a@example.com,b@example.com"],"role":"editor"}`},
		"too many emails":   {invitationsCreate.ID, `{"team_id":"` + teamA + `","emails":[` + many + `],"role":"editor"}`},
		"slash in name":     {teamsCreate.ID, `{"name":"a/b"}`},
		"dot in name":       {teamsCreate.ID, `{"name":"a.b"}`},
		"blank name":        {teamsCreate.ID, `{"name":"   "}`},
		"long name":         {teamsCreate.ID, `{"name":"` + strings.Repeat("x", 251) + `"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, teamHandler(200, `{}`))
		_, err := env.invokeConfirmed(test.tool, "write", test.args)
		if err == nil || strings.Contains(err.Error(), "canary") || len(calls) != 0 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(calls))
		}
	}
}

func TestInvitationEmailsAreCleaned(t *testing.T) {
	got, err := cleanEmails([]string{" Alice@example.com ", "alice@example.com", "bob@example.org"})
	if err != nil || len(got) != 2 || got[0] != "alice@example.com" || got[1] != "bob@example.org" {
		t.Fatalf("cleanEmails = %v, %v", got, err)
	}
}

func TestTeamChangesSendOnceAndReportUncertainty(t *testing.T) {
	for tool, arguments := range teamArgs() {
		if tool == membersList.ID {
			continue
		}
		reads := tool == invitationsCreate.ID || tool == teamsCreate.ID
		for _, test := range []struct {
			status    int
			body      string
			uncertain bool
		}{{500, bodyCanary, true}, {502, bodyCanary, true}, {403, bodyCanary, false}, {429, bodyCanary, false},
			{200, `not json`, reads}} {
			var calls []call
			env := newEnvironment(t, &calls, teamHandler(test.status, test.body))
			_, err := env.invokeConfirmed(tool, "write", arguments)
			if test.status == 200 && !test.uncertain {
				if err != nil {
					t.Errorf("%s: err = %v", tool, err)
				}
				continue
			}
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "not json") ||
				strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), inviteeCanary) ||
				strings.Contains(err.Error(), "may have taken effect") != test.uncertain || len(changes(calls)) != 1 {
				t.Errorf("%s status %d: err = %v, changes = %d", tool, test.status, err, len(changes(calls)))
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return teamHandler(200, ``)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", arguments)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}

func TestTeamToolsHaveCompleteRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{membersList, membersSetRole, membersRemove, invitationsCreate, teamsCreate, teamsDelete} {
		r := d.Risk
		if r.DataSensitivity == "" || !r.OpenWorld {
			t.Errorf("%s: risk = %+v", d.ID, r)
		}
		if (r.Effect == capability.EffectRead) != (r.Confirmation == capability.ConfirmationNone) {
			t.Errorf("%s: confirmation does not match the effect: %+v", d.ID, r)
		}
	}
	if invitationsCreate.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		teamsCreate.Risk.Idempotency != capability.IdempotencyNonIdempotent {
		t.Error("invitations.create and teams.create must be non-idempotent")
	}
	if invitationsCreate.Risk.DataSensitivity != membersSensitive || membersList.Risk.DataSensitivity != membersSensitive {
		t.Error("the tools that handle email addresses need the members class")
	}
}
