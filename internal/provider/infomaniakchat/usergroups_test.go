package infomaniakchat

import (
	"net/http"
	"strings"
	"testing"
)

const groupID = "group000000000000000000a1"

const groupObject = `{"id":"` + groupID + `","name":"devs","display_name":"Developers","description":"d",` +
	`"source":"custom","member_count":3,"allow_reference":true,"leak":"canary"}`

func groupsHandler(t *testing.T, answer string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, bothTeams), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanB:
			return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
		case r.Method == http.MethodGet && (r.URL.Path == "/api/v4/teams/"+teamA+"/groups" ||
			r.URL.Path == "/api/v4/channels/"+chanA+"/groups"):
			return jsonResponse(200, answer), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestUserGroupsListReadsTeamAndChannelBothAnswerForms(t *testing.T) {
	for _, answer := range []string{`[` + groupObject + `,{"id":"bad/id","name":"x"}]`,
		`{"groups":[` + groupObject + `,{"id":"bad/id","name":"x"}],"total_group_count":2}`} {
		var calls []call
		env := newEnvironment(t, &calls, groupsHandler(t, answer))
		result, err := env.invoke(usergroupsList.ID, "team", `{"team_id":"`+teamA+`","page":2,"limit":5}`)
		last := calls[len(calls)-1]
		if err != nil || last.path != "/api/v4/teams/"+teamA+"/groups" || last.query.Get("page") != "1" ||
			last.query.Get("per_page") != "5" || strings.Contains(result, "canary") ||
			!strings.Contains(result, `"count":1`) || !strings.Contains(result, `"member_count":3`) {
			t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
		}
		calls = nil
		result, err = env.invoke(usergroupsList.ID, "team", `{"channel_id":"`+chanA+`"}`)
		if err != nil || len(calls) != 2 || calls[0].path != "/api/v4/channels/"+chanA ||
			calls[1].path != "/api/v4/channels/"+chanA+"/groups" || !strings.Contains(result, groupID) {
			t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
		}
	}
}

func TestUserGroupsListRefusesForeignTargetsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, groupsHandler(t, `[]`))
	for _, c := range []struct{ connection, arguments string }{
		{"team", `{}`},
		{"team", `{"team_id":"` + teamA + `","channel_id":"` + chanA + `"}`},
		{"team", `{"team_id":"` + teamB + `"}`},
		{"channel", `{"channel_id":"` + chanC + `"}`},
		{"team", `{"team_id":"bad/id"}`},
		{"team", `{"team_id":"` + teamA + `","limit":201}`},
	} {
		if _, err := env.invoke(usergroupsList.ID, c.connection, c.arguments); err == nil {
			t.Errorf("%s %s was accepted", c.connection, c.arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestUserGroupsListRefusesChannelOfUnboundTeamLive(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, groupsHandler(t, `[]`))
	_, err := env.invoke(usergroupsList.ID, "team", `{"channel_id":"`+chanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/groups") {
			t.Fatalf("calls = %+v, want no groups request", calls)
		}
	}
}

func TestUserGroupsListProviderErrorTextIsNotShown(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/groups") {
			return jsonResponse(500, `{"message":"provider-secret-text"}`), nil
		}
		return jsonResponse(200, bothTeams), nil
	})
	_, err := env.invoke(usergroupsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err == nil || strings.Contains(err.Error(), "provider-secret-text") {
		t.Fatalf("err = %v", err)
	}
}

func TestUserGroupsListIsReadInUsersGroup(t *testing.T) {
	d := withGroup(usergroupsList)
	if d.Group != "users" || d.RequiresToolAllowList || d.Risk.Confirmation != "none" {
		t.Fatalf("descriptor = %+v", d)
	}
}
