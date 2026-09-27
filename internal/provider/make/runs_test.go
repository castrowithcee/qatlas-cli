package makeapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func runJSONOf(id string, teamID, orgID int64, status int) string {
	return fmt.Sprintf(`{"id":%q,"teamId":%d,"organizationId":%d,"status":%d,"type":"execution"}`, id, teamID, orgID, status)
}

func scenarioScopeHandler(t *testing.T, scenarioID, teamID int64) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(scenarioID, 10) {
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(scenarioID, teamID, "Own")+`}`), nil
		}
		t.Fatalf("unexpected scope-check request to %s", r.URL.Path)
		return nil, nil
	}
}

// runs.list confirms the named scenario's team live before it ever lists a run, then defensively re-checks
// every returned run's own teamId and organizationId, dropping anything outside this connection's bound
// team or organization even though the up-front scenario check already covers the team.
func TestRunsListChecksScenarioScopeThenDefensivelyFiltersRuns(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10) + "/logs":
			return jsonResponse(200, fmt.Sprintf(`{"scenarioLogs":[%s,%s]}`,
				runJSONOf("run-own", ownTeam, ownOrg, 1), runJSONOf("run-foreign", foreignTeam, foreignOrg, 3))), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err := env.invoke(runsList.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, "run-foreign") {
		t.Fatalf("result leaked a foreign-team run: %s", result)
	}
	if !strings.Contains(result, "run-own") {
		t.Fatalf("result = %s, want the own run", result)
	}
	if len(calls) != 2 || calls[0].path != apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10) {
		t.Fatalf("calls = %+v, want the scenario checked before the runs were listed", calls)
	}

	// A connection additionally bound to one organization drops a run of another organization, even when
	// the run's own team matches.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10) + "/logs":
			return jsonResponse(200, fmt.Sprintf(`{"scenarioLogs":[%s]}`, runJSONOf("run-other-org", ownTeam, foreignOrg, 1))), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err = env.invoke(runsList.ID, "org", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, "run-other-org") {
		t.Fatalf("result leaked a run of a foreign organization: %s", result)
	}

	// A scenario of a foreign team never reaches the logs request at all.
	calls = nil
	env = newEnvironment(t, &calls, scenarioScopeHandler(t, ownScenario, foreignTeam))
	_, err = env.invoke(runsList.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, want one refused scope check and no logs request", err, calls)
	}
}

// runs.get checks the scenario's team live, then the run's own team and organization, and extracts a
// best-effort error excerpt only when the run's own detail carries the recognised shape; a run of a foreign
// team or organization is refused, never returned.
func TestRunsGetChecksScopeAndExtractsBestEffortError(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10) + "/logs/run-1":
			return jsonResponse(200, `{"scenarioLog":{"id":"run-1","teamId":`+strconv.FormatInt(ownTeam, 10)+
				`,"status":3,"detail":{"error":{"message":"`+strings.Repeat("x", maxErrorExcerptLength+50)+`"}}}}`), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err := env.invoke(runsGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d,"execution_id":"run-1"}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !strings.Contains(result, `"error":"`+strings.Repeat("x", maxErrorExcerptLength)+`"`) {
		t.Fatalf("result = %s, want a bounded error excerpt", result)
	}
	if strings.Contains(result, strings.Repeat("x", maxErrorExcerptLength+1)) {
		t.Fatalf("result = %s, want the error excerpt bounded to %d characters", result, maxErrorExcerptLength)
	}

	// A run whose own report belongs to a foreign team is refused, even though the scenario itself checked
	// out, and the refusal never quotes the run's detail.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10) + "/logs/run-1":
			return jsonResponse(200, `{"scenarioLog":{"id":"run-1","teamId":`+strconv.FormatInt(foreignTeam, 10)+`,"status":1}}`), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	_, err = env.invoke(runsGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d,"execution_id":"run-1"}`, ownScenario))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}

	// A malformed execution_id is refused before any request is sent.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	_, err = env.invoke(runsGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d,"execution_id":"../etc/passwd"}`, ownScenario))
	if !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal with no request", err, calls)
	}
}
