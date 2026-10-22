package makeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
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
			return jsonResponse(200, `{"code":"OK","response":{"blueprint":{"name":"Own","flow":[{"id":1,"module":"http:ActionSendData",`+
				`"parameters":{"__IMTCONN__":4242}}]}}}`), nil
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

// A blueprint answer without response.blueprint is an invalid provider response, not an empty blueprint.
func TestScenariosBlueprintWithoutResponseBlueprintIsInvalid(t *testing.T) {
	for _, body := range []string{`{"code":"OK","response":{}}`, `{"code":"OK"}`, `{"blueprint":{"name":"Own"}}`} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/blueprint") {
				return jsonResponse(200, body), nil
			}
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		})
		_, err := env.invoke(scenariosBlueprint.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
		if classOf(err) != "invalid-provider-response" {
			t.Fatalf("body %s: class = %q, want invalid-provider-response", body, classOf(err))
		}
	}
}

// draft and blueprint_id become the draft and blueprintId query parameters; the answer is read from the
// same response envelope.
func TestScenariosBlueprintDraftAndBlueprintIDQuery(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/blueprint") {
			return jsonResponse(200, `{"code":"OK","response":{"blueprint":{"name":"Draft"}}}`), nil
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
	})
	result, err := env.invoke(scenariosBlueprint.ID, "open", fmt.Sprintf(`{"scenario_id":%d,"draft":true,"blueprint_id":7}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if !strings.Contains(result, `"name":"Draft"`) {
		t.Fatalf("result = %s, want the blueprint read from response.blueprint", result)
	}
	q := calls[len(calls)-1].query
	if q.Get("draft") != "true" || q.Get("blueprintId") != "7" {
		t.Fatalf("query = %v, want draft=true and blueprintId=7", q)
	}
}

func blueprintEnvironment(t *testing.T, blueprint string, calls *[]call) *environment {
	t.Helper()
	return newEnvironment(t, calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/blueprint") {
			return jsonResponse(200, `{"code":"OK","response":{"blueprint":`+blueprint+`}}`), nil
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
	})
}

func httpModule(secret string) string {
	return `{"id":1,"module":"http:ActionSendData","mapper":{"authUser":"user","authPass":"` + secret + `","ca":"cert","qs":[{"name":"token","value":"q-` + secret + `"}],` +
		`"headers":[{"name":"Authorization","value":"Bearer ` + secret + `"},{"name":"cookie","value":"sid=` + secret + `"},` +
		`{"name":"X-Other","value":"keep"},{"name":"X-Api-Key","value":""}]}}`
}

// Credentials are masked in flow, onerror, and router routes; every slot is named in masked and no secret
// survives anywhere in the result.
func TestScenariosBlueprintMasksCredentials(t *testing.T) {
	blueprint := `{"name":"Own","flow":[` + httpModule("s1") + `,{"id":2,"module":"builtin:BasicRouter","routes":[{"flow":[` +
		httpModule("s2") + `]}]}],"onerror":[` + httpModule("s3") + `],"big":12345678901234567890}`
	var calls []call
	result, err := blueprintEnvironment(t, blueprint, &calls).invoke(scenariosBlueprint.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, secret := range []string{"s1", "s2", "s3"} {
		if strings.Contains(result, `"`+secret+`"`) || strings.Contains(result, "Bearer "+secret) || strings.Contains(result, "sid="+secret) {
			t.Fatalf("result leaks %s: %s", secret, result)
		}
	}
	var got struct {
		Blueprint json.RawMessage `json:"blueprint"`
		Masked    []string        `json:"masked"`
		Truncated bool            `json:"masked_truncated"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, base := range []string{"/flow/0", "/flow/1/routes/0/flow/0", "/onerror/0"} {
		want = append(want, base+"/mapper/authPass", base+"/mapper/headers/0/value", base+"/mapper/headers/1/value")
	}
	if strings.Join(got.Masked, " ") != strings.Join(want, " ") || got.Truncated {
		t.Fatalf("masked = %v truncated = %v, want %v", got.Masked, got.Truncated, want)
	}
	if n := strings.Count(string(got.Blueprint), `"[redacted]"`); n != 9 {
		t.Fatalf("redacted values = %d, want 9: %s", n, got.Blueprint)
	}
	for _, keep := range []string{`"authUser":"user"`, `"ca":"cert"`, `"q-s1"`, `"keep"`, `"value":""`, `12345678901234567890`} {
		if !strings.Contains(string(got.Blueprint), keep) {
			t.Fatalf("blueprint lost %s: %s", keep, got.Blueprint)
		}
	}
}

// A blueprint without a credential slot is returned as Make reports it, with no masked list.
func TestScenariosBlueprintWithoutCredentialsIsUnchanged(t *testing.T) {
	blueprint := `{"flow":[{"id":1,"mapper":{"authPass":"","headers":[{"name":"X-Other","value":"v"}]}}],"name":"Own"}`
	var calls []call
	result, err := blueprintEnvironment(t, blueprint, &calls).invoke(scenariosBlueprint.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if want := fmt.Sprintf(`{"blueprint":%s,"scenario_id":%d}`, blueprint, ownScenario); result != want {
		t.Fatalf("result = %s, want %s", result, want)
	}
}

// More than 500 slots are all masked, but masked lists only 500 and says so.
func TestScenariosBlueprintMaskedListIsBounded(t *testing.T) {
	var flow []string
	for i := 0; i < 501; i++ {
		flow = append(flow, `{"mapper":{"authPass":"secret-value"}}`)
	}
	var calls []call
	result, err := blueprintEnvironment(t, `{"flow":[`+strings.Join(flow, ",")+`]}`, &calls).invoke(scenariosBlueprint.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var got struct {
		Masked    []string `json:"masked"`
		Truncated bool     `json:"masked_truncated"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Masked) != 500 || !got.Truncated || strings.Contains(result, "secret-value") {
		t.Fatalf("masked = %d, truncated = %v, leaked = %v", len(got.Masked), got.Truncated, strings.Contains(result, "secret-value"))
	}
}

// A blueprint nested deeper than this provider reads is an invalid response, never partly returned.
func TestScenariosBlueprintTooDeepIsInvalidResponse(t *testing.T) {
	deep := strings.Repeat(`{"a":`, maxBlueprintDepth+1) + `1` + strings.Repeat(`}`, maxBlueprintDepth+1)
	var calls []call
	_, err := blueprintEnvironment(t, deep, &calls).invoke(scenariosBlueprint.ID, "open",
		fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want invalid-provider-response", classOf(err))
	}
}

// create and update refuse a blueprint that still holds the marker, before any request; real values pass.
func TestScenariosWritesRefuseMaskedBlueprint(t *testing.T) {
	masked := []string{
		`{"flow":[{"mapper":{"authPass":"[redacted]"}}]}`,
		`{"flow":[{"mapper":{"headers":[{"name":"COOKIE","value":"[redacted]"}]}}]}`,
	}
	for _, blueprint := range masked {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		})
		_, err := env.confirmed(scenariosCreate.ID, "open",
			`{"blueprint":`+blueprint+`,"scheduling":`+minimalScheduling+`}`)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "/flow/0/mapper/") {
			t.Fatalf("create err = %v, want an invalid request naming the pointer", err)
		}
		_, err = env.confirmed(scenariosUpdate.ID, "open",
			fmt.Sprintf(`{"scenario_id":%d,"blueprint":%s}`, ownScenario, blueprint))
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("update err = %v, calls = %+v, secret reads = %d, want a local refusal", err, calls, *env.reads)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(newScenario, ownTeam, "My scenario")+`}`), nil
	})
	_, err := env.confirmed(scenariosCreate.ID, "open",
		`{"blueprint":{"flow":[{"mapper":{"authPass":"real"}}]},"scheduling":`+minimalScheduling+`}`)
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d, want one created scenario", err, len(calls))
	}
}
