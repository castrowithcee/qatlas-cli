package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const inviteCanary = "invite-canary-9f3a"

// teamFixture is a fake instance: teamA holds userInA and userBoth (via usersFixture), teamB userInB and
// userBoth, and the token's own user belongs to neither. Team resources carry canaries in every field the
// provider must not output.
type teamFixture struct {
	t      *testing.T
	users  usersFixture
	mutate func(*http.Request) (*http.Response, error)
}

func (f *teamFixture) handle(r *http.Request) (*http.Response, error) {
	teamBody := func(id string) string {
		return `{"id":"` + id + `","name":"` + id + `","display_name":"Team","description":"About","type":"I",` +
			`"invite_id":"` + inviteCanary + `","email":"` + inviteCanary + `@example.invalid",` +
			`"allowed_domains":"` + inviteCanary + `.invalid"}`
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/v4/teams/" + teamA:
			return jsonResponse(200, teamBody(teamA)), nil
		case "/api/v4/teams/" + teamA + "/stats":
			return jsonResponse(200, `{"team_id":"`+teamA+`","total_member_count":5,"active_member_count":4}`), nil
		case "/api/v4/teams/" + teamB:
			return jsonResponse(200, teamBody(teamA)), nil // answers with another team's ID
		case "/api/v4/teams/" + teamB + "/stats":
			return jsonResponse(200, `{"team_id":"`+teamB+`","total_member_count":1,"active_member_count":1}`), nil
		case "/api/v4/teams/" + teamA + "/members":
			return jsonResponse(200, `[{"team_id":"`+teamA+`","user_id":"`+userInA+`","roles":"team_user team_admin",`+
				`"scheme_admin":true,"delete_at":0,"msg_count":3,"invite":"`+inviteCanary+`"},`+
				`{"team_id":"`+teamA+`","user_id":"`+userLeft+`","roles":"team_user","delete_at":7},`+
				`{"team_id":"`+teamB+`","user_id":"`+userInB+`","roles":"team_user","delete_at":0}]`), nil
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v4/teams/") && r.Method == http.MethodPut {
		return f.mutate(r)
	}
	return f.users.handle(r)
}

func (f *teamFixture) env(calls *[]call) *environment {
	f.users.t = f.t
	return newEnvironment(f.t, calls, f.handle)
}

func teamFixtureWith(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *teamFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &teamFixture{t: t, mutate: mutate}
}

func TestTeamsGetOutputsOnlyTheAllowListAndChecksTheTeamID(t *testing.T) {
	var calls []call
	env := teamFixtureWith(t, nil).env(&calls)
	result, err := env.invoke(teamsGet.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, inviteCanary) || strings.Contains(result, "invite_id") ||
		strings.Contains(result, "allowed_domains") || strings.Contains(result, "email") {
		t.Fatalf("result = %s, want no invitation, email, or domain data", result)
	}
	var detail TeamDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail != (TeamDetail{ID: teamA, Name: teamA,
		DisplayName: "Team", Description: "About", Type: "I", MemberCount: 5, ActiveMemberCount: 4}) {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if len(calls) != 2 || calls[0].path != "/api/v4/teams/"+teamA || calls[1].path != "/api/v4/teams/"+teamA+"/stats" {
		t.Fatalf("calls = %+v, want the team and its stats", calls)
	}

	// A foreign team is refused locally before the secret is read; an answer for another team is invalid.
	calls, *env.reads = nil, 0
	_, err = env.invoke(teamsGet.ID, "team", `{"team_id":"`+teamB+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), teamB) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
	if _, err := env.invoke(teamsGet.ID, "team", `{"team_id":"Bad/Id"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	if _, err := env.invoke(teamsGet.ID, "twoteams", `{"team_id":"`+teamB+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want an invalid response for a mismatched team ID", err)
	}
}

func TestTeamMembersListOutputsOnlyTheFieldsAndDropsForeignEntries(t *testing.T) {
	var calls []call
	env := teamFixtureWith(t, nil).env(&calls)
	result, err := env.invoke(teamMembersList.ID, "team", `{"team_id":"`+teamA+`","page":2,"limit":3}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, inviteCanary) || strings.Contains(result, "msg_count") || strings.Contains(result, userInB) {
		t.Fatalf("result = %s", result)
	}
	var page TeamMembersPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 || page.Page != 2 || !page.HasMore ||
		page.Members[0] != (TeamMemberEntry{UserID: userInA, Roles: "team_user team_admin", SchemeAdmin: true}) ||
		!page.Members[1].Deleted {
		t.Fatalf("page = %+v, %v", page, err)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != "/api/v4/teams/"+teamA+"/members" ||
		last.query.Get("page") != "1" || last.query.Get("per_page") != "3" || len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	calls, *env.reads = nil, 0
	if _, err := env.invoke(teamMembersList.ID, "team", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal", err, calls)
	}
	if _, err := env.invoke(teamMembersList.ID, "team", `{"team_id":"`+teamA+`","limit":201}`); err == nil {
		t.Fatal("limit above 200 was accepted")
	}
}

func TestTeamMembersRolesNeedsToolListConfirmationAndSendsOnePut(t *testing.T) {
	f := teamFixtureWith(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/teams/"+teamA+"/members/"+userInA+"/roles" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	for _, roles := range []string{"system_admin", "team_admin", "team_user team_admin system_admin", "channel_user", ""} {
		args, _ := json.Marshal(map[string]string{"team_id": teamA, "user_id": userInA, "roles": roles})
		if _, err := env.confirmed(teamMembersRoles.ID, "teamroles", string(args)); err == nil {
			t.Fatalf("roles %q were accepted", roles)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
	args := `{"team_id":"` + teamA + `","user_id":"` + userInA + `","roles":"team_user team_admin"}`
	if _, err := env.confirmed(teamMembersRoles.ID, "memberno", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, want a refusal without the tool in the tools list", err)
	}
	if _, err := env.confirmed(teamMembersRoles.ID, "team", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, want a refusal for a connection without the update permission", err)
	}
	if _, err := env.invoke(teamMembersRoles.ID, "teamroles", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(teamMembersRoles.ID, "teamroles", args)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var out TeamMemberRoles
	if json.Unmarshal([]byte(result), &out) != nil || out != (TeamMemberRoles{TeamID: teamA, UserID: userInA,
		Roles: "team_user team_admin"}) {
		t.Fatalf("result = %s", result)
	}
	var body map[string]string
	last := calls[len(calls)-1]
	if countMethod(calls, http.MethodPut) != 1 || last.method != http.MethodPut ||
		json.Unmarshal([]byte(last.body), &body) != nil || len(body) != 1 || body["roles"] != "team_user team_admin" {
		t.Fatalf("calls = %+v, want exactly one PUT as the last request", calls)
	}
}

func TestTeamMembersRolesRefusesTargetsOutsideTheTeamBeforeThePut(t *testing.T) {
	var calls []call
	env := teamFixtureWith(t, nil).env(&calls)
	// userInB belongs to teamB only (even when bound), userLeft left teamA, and the token's own user is no
	// member of teamA, so none of them may be changed.
	for _, c := range []struct{ connection, team, user string }{
		{"teamroles", teamA, userInB},
		{"teamroles", teamA, userLeft},
		{"teamroles", teamA, selfID},
		{"teamroles2", teamA, userInB},
	} {
		calls = nil
		args := `{"team_id":"` + c.team + `","user_id":"` + c.user + `","roles":"team_user"}`
		_, err := env.confirmed(teamMembersRoles.ID, c.connection, args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), c.user) {
			t.Fatalf("%s: err = %v", args, err)
		}
		if countMethod(calls, http.MethodPut) != 0 {
			t.Fatalf("%s: calls = %+v, want no PUT", args, calls)
		}
	}
	// A foreign team is refused before any provider I/O.
	calls, *env.reads = nil, 0
	args := `{"team_id":"` + teamB + `","user_id":"` + userInB + `","roles":"team_user"}`
	if _, err := env.confirmed(teamMembersRoles.ID, "teamroles", args); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

func TestTeamMembersRolesIsNeverRetriedAfterAnUnclearResult(t *testing.T) {
	args := `{"team_id":"` + teamA + `","user_id":"` + userInA + `","roles":"team_user"}`
	for name, fail := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		},
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
	} {
		var calls []call
		env := teamFixtureWith(t, fail).env(&calls)
		_, err := env.confirmed(teamMembersRoles.ID, "teamroles", args)
		if err == nil || !strings.Contains(err.Error(), "may have been changed") || strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if countMethod(calls, http.MethodPut) != 1 || calls[len(calls)-1].method != http.MethodPut {
			t.Fatalf("%s: calls = %+v, want exactly one PUT", name, calls)
		}
	}
}
