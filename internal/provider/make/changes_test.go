package makeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const newScenario int64 = 30

// minimalScheduling is the smallest valid scheduling argument: just the one documented "type" field.
const minimalScheduling = `{"type":"on-demand"}`

func minimalCreateArguments(extra string) string {
	body := fmt.Sprintf(`{"blueprint":{"name":"My scenario","flow":[]},"scheduling":%s`, minimalScheduling)
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

// Every one of this provider's five change tools is refused before any request when it is not confirmed,
// and no secret is ever read for it either.
func TestChangeToolsRequireConfirmationAndSendNoRequestWithoutIt(t *testing.T) {
	cases := []struct {
		operation, arguments string
	}{
		{scenariosCreate.ID, minimalCreateArguments("")},
		{scenariosUpdate.ID, fmt.Sprintf(`{"scenario_id":%d,"name":"Renamed"}`, ownScenario)},
		{scenariosStart.ID, fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)},
		{scenariosStop.ID, fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)},
		{scenariosRun.ID, fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)},
	}
	for _, tt := range cases {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			_, err := env.invoke(tt.operation, "open", tt.arguments)
			if !isConfirmationRequired(err) {
				t.Fatalf("err = %v, want a confirmation-required refusal", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
			}
		})
	}
}

// scenarios.create is refused locally, before any request, on a connection restricted by a scenario
// allow-list: a scenario that does not exist yet can never already be on that list.
func TestScenariosCreateRefusesAScenarioRestrictedConnectionLocally(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(scenariosCreate.ID, "scenario", minimalCreateArguments(""))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the restriction was refused", calls)
	}
}

// A successful create sends exactly one POST, always with the connection's own bound team as teamId, never a
// caller-supplied one (the input schema does not even accept one), encodes blueprint and scheduling to JSON
// strings, and verifies the created scenario's team from the create response itself, without a second GET.
func TestScenariosCreateSendsOneRequestWithTheBoundTeamAndVerifiesItsResult(t *testing.T) {
	var calls []call
	var sentBody map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != apiPath+"/scenarios" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&sentBody)
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(newScenario, ownTeam, "My scenario")+`}`), nil
	})
	result, err := env.confirmed(scenariosCreate.ID, "open", minimalCreateArguments(`"description":"d"`))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var summary ScenarioSummary
	if err := json.Unmarshal([]byte(result), &summary); err != nil || summary.ID != newScenario {
		t.Fatalf("result = %s, %v, want the created scenario", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one request", calls)
	}
	if got := sentBody["teamId"]; got != float64(ownTeam) {
		t.Fatalf("sent teamId = %v, want the connection's own bound team %d", got, ownTeam)
	}
	blueprint, ok := sentBody["blueprint"].(string)
	if !ok || !strings.Contains(blueprint, "My scenario") {
		t.Fatalf("sent blueprint = %#v, want a JSON-encoded string, not a nested object", sentBody["blueprint"])
	}
	scheduling, ok := sentBody["scheduling"].(string)
	if !ok || !strings.Contains(scheduling, "on-demand") {
		t.Fatalf("sent scheduling = %#v, want a JSON-encoded string, not a nested object", sentBody["scheduling"])
	}
	if _, ok := sentBody["scenario_id"]; ok {
		t.Fatalf("sent body = %+v, want no scenario_id argument reaching the request", sentBody)
	}
}

// When Make reports a newly created scenario outside this connection's bound team, the deviation is reported
// as a provider error, never as an invalid request: the one changing POST has already reached Make and
// cannot be undone by a tool this milestone does not offer.
func TestScenariosCreateReportsATeamMismatchAsAProviderErrorAfterTheRequestAlreadySent(t *testing.T) {
	var calls []call
	posts := 0
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == apiPath+"/scenarios" {
			posts++
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(newScenario, foreignTeam, "Foreign")+`}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(scenariosCreate.ID, "open", minimalCreateArguments(""))
	if classOf(err) != "provider-error" {
		t.Fatalf("class = %q, want provider-error for a team mismatch", classOf(err))
	}
	if isInvalidRequest(err) {
		t.Fatalf("err = %v, want it not classified as an invalid request: the change already happened", err)
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want exactly the one POST that already reached Make", posts)
	}
}

// scenarios.update sends its one changing PATCH only after the scenario's current scope is verified live,
// and re-reads it afterwards to answer with the same scope-checked summary scenarios.get would.
func TestScenariosUpdateVerifiesScopeBeforeAndAfterItsOnePATCH(t *testing.T) {
	var calls []call
	var sentBody map[string]any
	patched := false
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case r.Method == http.MethodPatch && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10):
			_ = json.NewDecoder(r.Body).Decode(&sentBody)
			patched = true
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(scenariosUpdate.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d,"name":"Renamed"}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !patched {
		t.Fatalf("want the PATCH to have been sent")
	}
	var summary ScenarioSummary
	if err := json.Unmarshal([]byte(result), &summary); err != nil || summary.ID != ownScenario {
		t.Fatalf("result = %s, %v, want the re-read scenario", result, err)
	}
	if len(calls) != 3 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPatch ||
		calls[2].method != http.MethodGet {
		t.Fatalf("calls = %+v, want the pre-check GET, one PATCH, and the post-check GET", calls)
	}
	if sentBody["name"] != "Renamed" {
		t.Fatalf("sent body = %+v, want name forwarded", sentBody)
	}
	if _, ok := sentBody["blueprint"]; ok {
		t.Fatalf("sent body = %+v, want no blueprint sent when it was not given", sentBody)
	}

	// A scenario_id outside the allow-list is refused before any request, PATCH included.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(scenariosUpdate.ID, "scenario",
		fmt.Sprintf(`{"scenario_id":%d,"name":"Renamed"}`, foreignScenario))
	if !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal with no request", err, calls)
	}

	// A scenario Make itself reports as belonging to a foreign team is refused after the one pre-check GET,
	// before any PATCH, even on a connection with no local allow-list to catch it.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("the PATCH must not be sent before the scope check: %s", r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, foreignTeam, "Foreign")+`}`), nil
	})
	_, err = env.confirmed(scenariosUpdate.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d,"name":"Renamed"}`, ownScenario))
	if !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, want one refused scope check and no PATCH", err, calls)
	}
}

// scenarios.update needs at least one field to change, folder_id and clear_folder are mutually exclusive,
// and both are refused locally before any request.
func TestScenariosUpdateRefusesAnEmptyOrContradictoryChangeLocally(t *testing.T) {
	cases := []struct {
		name, arguments string
	}{
		{"no field to change", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)},
		{"folder_id and clear_folder", fmt.Sprintf(`{"scenario_id":%d,"folder_id":1,"clear_folder":true}`, ownScenario)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			_, err := env.confirmed(scenariosUpdate.ID, "open", tt.arguments)
			if !isInvalidRequest(err) || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, want a local refusal with no request", err, calls)
			}
		})
	}
}

// scenarios.start and scenarios.stop each send their one changing POST only after the scenario's scope is
// verified live, then re-read the scenario to confirm the active state actually changed.
func TestScenariosStartAndStopChangeStateAfterScopeVerification(t *testing.T) {
	var calls []call
	posted := false
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+inactiveScenarioJSONOf(ownScenario, ownTeam, posted)+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10)+"/start":
			posted = true
			return jsonResponse(200, `{"scenario":{"id":`+strconv.FormatInt(ownScenario, 10)+`,"isActive":true,"islinked":false}}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(scenariosStart.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var summary ScenarioSummary
	if err := json.Unmarshal([]byte(result), &summary); err != nil || !summary.IsActive {
		t.Fatalf("result = %s, %v, want the scenario reported active", result, err)
	}
	if len(calls) != 3 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost ||
		calls[2].method != http.MethodGet {
		t.Fatalf("calls = %+v, want the pre-check GET, one starting POST, and the post-check GET", calls)
	}

	// A scenario Make still reports inactive after the start POST succeeded is an invalid provider response,
	// not a silent success.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet:
			return jsonResponse(200, `{"scenario":`+inactiveScenarioJSONOf(ownScenario, ownTeam, false)+`}`), nil
		case r.Method == http.MethodPost:
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(scenariosStart.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if classOf(err) != "invalid-provider-response" {
		t.Fatalf("class = %q, want invalid-provider-response when the active state never changed", classOf(err))
	}
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v, want it named as uncertain", err)
	}

	// A scenario Make itself reports as belonging to a foreign team is refused after the one pre-check GET,
	// before any start or stop POST, for both tools.
	for _, tt := range []struct{ operation capability.Descriptor }{{scenariosStart}, {scenariosStop}} {
		calls = nil
		env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Fatalf("no start or stop POST must be sent before the scope check: %s", r.URL.Path)
			}
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, foreignTeam, "Foreign")+`}`), nil
		})
		_, err = env.confirmed(tt.operation.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
		if !isInvalidRequest(err) || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %+v, want one refused scope check and no request", tt.operation.ID, err, calls)
		}
	}
}

// inactiveScenarioJSONOf is scenarioJSONOf with an explicit active flag, for the start/stop tests.
func inactiveScenarioJSONOf(id, teamID int64, active bool) string {
	raw := scenarioJSONOf(id, teamID, "Own")
	return strings.Replace(raw, `"isActive":true`, fmt.Sprintf(`"isActive":%t`, active), 1)
}

// scenarios.run verifies the scenario's scope with the same live check every other scenario_id tool applies,
// before its one changing POST, and never re-reads the scenario afterward (see the package doc): its answer
// carries no scope of its own to re-verify.
func TestScenariosRunVerifiesScopeBeforeItsOnePOSTAndNeverReReads(t *testing.T) {
	var calls []call
	var sentBody map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10)+"/run":
			_ = json.NewDecoder(r.Body).Decode(&sentBody)
			return jsonResponse(200, `{"executionId":"exec-1","status":"1"}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(scenariosRun.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d,"data":{"x":1}}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var run ScenarioRun
	if err := json.Unmarshal([]byte(result), &run); err != nil || run.ExecutionID != "exec-1" {
		t.Fatalf("result = %s, %v, want the new run's execution id", result, err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost {
		t.Fatalf("calls = %+v, want the scope-check GET then exactly one run POST, no further re-read", calls)
	}
	data, ok := sentBody["data"].(map[string]any)
	if !ok || data["x"] != float64(1) {
		t.Fatalf("sent data = %#v, want a nested JSON object, not a string", sentBody["data"])
	}
	if _, ok := sentBody["callbackUrl"]; ok {
		t.Fatalf("sent body = %+v, want no callbackUrl ever sent", sentBody)
	}

	// A scenario of a foreign team never reaches the run request at all.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10) {
			t.Fatalf("the run must not be requested before the scope check: %s", r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, foreignTeam, "Foreign")+`}`), nil
	})
	_, err = env.confirmed(scenariosRun.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, want one refused scope check and no run request", err, calls)
	}
}

// scenarios.run's own data argument is bounded by size and nesting depth before any request, and rejected
// locally the same way a malformed blueprint or scheduling argument of create and update would be.
func TestScenariosRunRejectsAnOversizedOrOverlyNestedDataLocally(t *testing.T) {
	deep := `{"a":`
	for i := 0; i < maxRunDataDepth+4; i++ {
		deep += `{"a":`
	}
	deep += "1"
	for i := 0; i < maxRunDataDepth+5; i++ {
		deep += "}"
	}
	cases := []struct {
		name, data string
	}{
		{"too deep", deep},
		{"too large", `{"a":"` + strings.Repeat("x", maxRunDataBytes) + `"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			_, err := env.confirmed(scenariosRun.ID, "open",
				fmt.Sprintf(`{"scenario_id":%d,"data":%s}`, ownScenario, tt.data))
			if !isInvalidRequest(err) || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, want a local refusal with no request", err, calls)
			}
		})
	}
}

// An unclear outcome of the one changing request is reported as uncertain and never repeated by this
// provider itself: a 5xx after scenarios.create's request already reached Make, and a timeout after
// scenarios.run's request may have reached Make, the latter naming runs.list, not "the current state",
// since a run is never followed by a re-read.
func TestChangeRequestsAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	var calls []call
	posts := 0
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == apiPath+"/scenarios" {
			posts++
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+foreignCanary+`"}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(scenariosCreate.ID, "open", minimalCreateArguments(""))
	if classOf(err) != "provider-error" {
		t.Fatalf("class = %q, want provider-error", classOf(err))
	}
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v, want it to name the change as uncertain", err)
	}
	if err != nil && strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("error leaked the provider body: %v", err)
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want exactly one attempt, never repeated by Qatlas itself", posts)
	}

	calls = nil
	runs := 0
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10):
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/scenarios/"+strconv.FormatInt(ownScenario, 10)+"/run":
			runs++
			return nil, errUnclearTransport{}
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(scenariosRun.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err == nil || !strings.Contains(err.Error(), "run may have started") ||
		!strings.Contains(err.Error(), "make.runs.list") {
		t.Fatalf("err = %v, want the run's own transport failure to point at make.runs.list", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want exactly one attempt, never repeated by Qatlas itself", runs)
	}
}

// errUnclearTransport stands for a connection reset after the request left this process: net/http reports it
// as a plain, untyped error, which (*Client).do's own transport classification cannot prove any more
// specific cause for, so it is marked uncertain for a change request all the same.
type errUnclearTransport struct{}

func (errUnclearTransport) Error() string { return "connection lost after the request was sent" }

// 401 and 403 during a change's one request are classified the same stable way every read classifies them,
// and never carry Make's own response body into the error; a 403 names the scope this change needs.
func TestChangeToolsClassifyAuthAndPermissionFailuresWithoutLeakingContent(t *testing.T) {
	for _, tt := range []struct {
		status int
		class  string
	}{{http.StatusUnauthorized, "auth"}, {http.StatusForbidden, "permission"}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(tt.status, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(scenariosCreate.ID, "open", minimalCreateArguments(""))
		if string(classOf(err)) != tt.class {
			t.Fatalf("status %d: class = %q, want %q", tt.status, classOf(err), tt.class)
		}
		if err != nil && strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("status %d: error %v leaked the provider body", tt.status, err)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{}`), nil
	})
	_, err := env.confirmed(scenariosCreate.ID, "open", minimalCreateArguments(""))
	if err == nil || !strings.Contains(err.Error(), "scenarios:write") {
		t.Fatalf("err = %v, want it to name the scenarios:write scope", err)
	}
}

// A 429 whose body names Make's own IM310 code is classified as a paused organization or team, not an
// ordinary, transient rate limit, even though both stay the rate-limited class.
func TestPausedOrganizationIsNamedDistinctlyFromAnOrdinaryRateLimit(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests, `{"code":"IM310"}`), nil
	})
	_, err := env.invoke(scenariosList.ID, "open", `{}`)
	if classOf(err) != "rate-limited" {
		t.Fatalf("class = %q, want rate-limited", classOf(err))
	}
	if err == nil || !strings.Contains(err.Error(), "IM310") || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("err = %v, want it to name the paused organization or team", err)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests, `{}`), nil
	})
	_, err = env.invoke(scenariosList.ID, "open", `{}`)
	if classOf(err) != "rate-limited" || err == nil || strings.Contains(err.Error(), "IM310") {
		t.Fatalf("err = %v, want an ordinary rate limit without IM310", err)
	}
}
