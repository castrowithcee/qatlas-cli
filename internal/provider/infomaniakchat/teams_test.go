package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// teams.list keeps only the teams this connection is bound to, drops a team the token belongs to but this
// connection is not bound to, and pages the result itself: kChat's own listing answers as one complete
// array with no pagination of its own.
func TestTeamsListIsScopedAndPaginated(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/users/me/teams" {
			return jsonResponse(200, "["+teamJSONOf(teamA, "Team A")+","+teamJSONOf(teamB, "Team B")+"]"), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.invoke(teamsList.ID, "team", `{"limit":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page TeamsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if page.Total != 1 || page.Count != 1 || page.Pages != 1 || len(page.Teams) != 1 || page.Teams[0].ID != teamA {
		t.Fatalf("page = %+v, want exactly teamA: teamB is not bound to this connection", page)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one live read", calls)
	}
}
