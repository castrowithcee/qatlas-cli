package makeapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func scenarioJSONOf(id, teamID int64, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"teamId":%d,"description":"","isActive":true,"islocked":false,`+
		`"isPaused":false}`, id, name, teamID)
}

func scenariosPageJSONOf(scenarios ...string) string {
	return fmt.Sprintf(`{"scenarios":[%s]}`, strings.Join(scenarios, ","))
}

// The list is server-side filtered by teamId and defensively re-filtered by this connection's own scope: a
// scenario of another team, or outside a configured scenario allow-list, is never reported.
func TestScenariosListPaginatesAndDefensivelyFiltersByScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/scenarios" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, scenariosPageJSONOf(
			scenarioJSONOf(ownScenario, ownTeam, "Own"),
			scenarioJSONOf(foreignScenario, foreignTeam, "Foreign"),
		)), nil
	})

	// The open connection has no scenario allow-list, but the server-side teamId filter and the defensive
	// re-check both drop the foreign-team scenario, and the request itself named only the bound team.
	result, err := env.invoke(scenariosList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, "Foreign") || strings.Contains(result, strconv.FormatInt(foreignTeam, 10)) {
		t.Fatalf("result leaked a foreign-team scenario: %s", result)
	}
	if !strings.Contains(result, `"id":`+strconv.FormatInt(ownScenario, 10)) {
		t.Fatalf("result = %s, want the own scenario", result)
	}
	if calls[0].query.Get("teamId") != itoa64(ownTeam) {
		t.Fatalf("calls = %+v, want the request scoped by teamId", calls)
	}

	// The scenario-restricted connection additionally drops a scenario outside its own allow-list, even
	// though it belongs to the bound team.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, scenariosPageJSONOf(
			scenarioJSONOf(ownScenario, ownTeam, "Own"),
			scenarioJSONOf(99, ownTeam, "SameTeamNotAllowed"),
		)), nil
	})
	result, err = env.invoke(scenariosList.ID, "scenario", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, "SameTeamNotAllowed") {
		t.Fatalf("result leaked a scenario outside the allow-list: %s", result)
	}

	// has_more is true exactly when the page returned a full batch; Make reports no total count of its own.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, scenariosPageJSONOf(scenarioJSONOf(ownScenario, ownTeam, "Own"))), nil
	})
	result, err = env.invoke(scenariosList.ID, "open", `{"limit":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !strings.Contains(result, `"has_more":true`) {
		t.Fatalf("result = %s, want has_more true for a full page", result)
	}
}

// scenarios.get confirms a scenario's team live, against Make's own report, before any content is
// returned; a scenario of another team is refused as an invalid request, and a scenario_id outside a
// configured allow-list never reaches a request at all.
func TestScenariosGetVerifiesLiveTeamMembershipAndAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10) {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
	})
	result, err := env.invoke(scenariosGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil || !strings.Contains(result, "Own") {
		t.Fatalf("invoke() = %s, %v", result, err)
	}

	// A scenario Make itself reports as belonging to a foreign team is refused, and the refusal never names
	// the foreign team.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, foreignTeam, "Foreign")+`}`), nil
	})
	_, err = env.invoke(scenariosGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	if err != nil && strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
		t.Fatalf("error named the foreign team: %v", err)
	}

	// A scenario_id outside a configured scenario allow-list is refused locally: no request is ever sent.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	_, err = env.invoke(scenariosGet.ID, "scenario", fmt.Sprintf(`{"scenario_id":%d}`, foreignScenario))
	if !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal with no request", err, calls)
	}
}

// scenarios.blueprint confirms the scenario's team live before it ever requests the blueprint itself, and
// passes the blueprint through unmodified, including a connection reference that is only ever a numeric id.
func TestScenariosBlueprintChecksScopeBeforeContentAndPassesThroughUnmodified(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10) + "/blueprint":
			return jsonResponse(200, `{"blueprint":{"name":"Own","flow":[{"id":1,"module":"http:ActionSendData",`+
				`"parameters":{"__IMTCONN__":4242}}]}}`), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err := env.invoke(scenariosBlueprint.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !strings.Contains(result, `"__IMTCONN__":4242`) {
		t.Fatalf("result = %s, want the connection reference id passed through unmodified", result)
	}
	if len(calls) != 2 || calls[0].path != apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10) {
		t.Fatalf("calls = %+v, want the scenario checked before the blueprint was requested", calls)
	}

	// A scenario of a foreign team never reaches the blueprint request at all.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10) {
			t.Fatalf("the blueprint must not be requested before the scope check: %s", r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, foreignTeam, "Foreign")+`}`), nil
	})
	_, err = env.invoke(scenariosBlueprint.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, want one refused scope check and no blueprint request", err, calls)
	}
}
