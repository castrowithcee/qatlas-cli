package penpot

import (
	"net/http"
	"strings"
	"testing"
)

func projectsHandler(c call) (*http.Response, error) {
	switch c.body["team-id"] {
	case teamA:
		return jsonResponse(200, `[{"id":"`+projectA1+`","name":"Web","team-id":"`+teamA+`","count":3,"is-pinned":true,"modified-at":"2026-09-01T10:00:00Z"},`+
			`{"id":"`+projectA2+`","name":"App","team-id":"`+teamA+`","count":1},`+
			`{"id":"`+projectOut+`","name":"Fremdteam","team-id":"`+teamForeign+`"}]`), nil
	case teamB:
		return jsonResponse(200, `[{"id":"`+projectB1+`","name":"Print","teamId":"`+teamB+`"}]`), nil
	}
	return jsonResponse(404, bodyCanary), nil
}

func TestProjectsListFiltersByTeamAndAllowList(t *testing.T) {
	for _, test := range []struct {
		connection, team string
		want, absent     []string
	}{
		{"one", teamA, []string{"Web", "App"}, []string{"Fremdteam"}},
		{"two", teamB, []string{"Print"}, []string{"Web"}},
		{"narrow", teamA, []string{"Web"}, []string{"App", "Fremdteam"}},
	} {
		var calls []call
		env := newEnvironment(t, &calls, projectsHandler)
		result, err := env.invoke(projectsList.ID, test.connection, `{"team_id":"`+test.team+`"}`)
		if err != nil {
			t.Fatalf("%s: %v", test.connection, err)
		}
		for _, name := range test.want {
			if !strings.Contains(result, name) {
				t.Errorf("%s: %q missing: %s", test.connection, name, result)
			}
		}
		for _, name := range test.absent {
			if strings.Contains(result, name) {
				t.Errorf("%s: %q must be absent: %s", test.connection, name, result)
			}
		}
		if len(calls) != 1 || calls[0].command() != cmdProjects || calls[0].body["team-id"] != test.team {
			t.Errorf("%s: calls = %+v", test.connection, calls)
		}
	}
}

func TestProjectsListRefusesForeignTeamBeforeSecretAndIO(t *testing.T) {
	for _, team := range []string{teamForeign, teamB, "not-a-uuid"} {
		var calls []call
		env := newEnvironment(t, &calls, projectsHandler)
		_, err := env.invoke(projectsList.ID, "one", `{"team_id":"`+team+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), team) {
			t.Errorf("team %q: err = %v, want an invalid request without the team", team, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("team %q: calls = %d, secret reads = %d, want none", team, len(calls), *env.reads)
		}
	}
}
