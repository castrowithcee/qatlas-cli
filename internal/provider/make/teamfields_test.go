package makeapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const richOrganization = `{"organization":{"id":501,"name":"Acme","createdAt":"2020-01-01T00:00:00Z",` +
	`"serviceName":"make","nextReset":"2026-11-01T00:00:00Z","lastReset":"2026-10-01T00:00:00Z",` +
	`"isPaused":true,"countryId":7,"timezoneId":8,"deleted":true,"zone":"eu1.make.com",` +
	`"license":{"operations":10000,"big":9007199254740993,"dlm":true,"plan":"pro","ratio":1.5,"nested":{"a":1}},` +
	`"teams":[{"id":1001,"name":"Own"},{"id":9,"name":"Foreign"}],"productName":"Pro","ssoType":"saml2",` +
	`"scenarios":12,"activeScenarios":4,"tfaEnforced":true,"userSessionTimeout":86400,` +
	`"featureControls":[{"name":"a","enabled":true},{"name":"b","enabled":false}],` +
	`"privateSpaces":[{"id":77,"name":"Mine","isOwner":true,"hasAdminVisibility":true}]}}`

const richTeam = `{"team":{"id":1001,"name":"Own","organizationId":501,"type":"standard","activeScenarios":3,` +
	`"activeApps":2,"operationsLimit":1000}}`

func TestTeamAndOrganizationGetReportEveryField(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/teams/1001":
			return jsonResponse(200, richTeam), nil
		case apiPath + "/organizations/501":
			return jsonResponse(200, richOrganization), nil
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
		return jsonResponse(404, `{}`), nil
	})
	got, err := env.invoke(teamGet.ID, "open", `{}`)
	if err != nil || !strings.Contains(got, `"type":"standard"`) {
		t.Fatalf("team.get = %s, %v", got, err)
	}
	if !strings.Contains(strings.Join(calls[0].query["cols[]"], ","), "type") {
		t.Fatalf("cols = %v, want type", calls[0].query["cols[]"])
	}
	if seen := assertOutputDeclared(t, teamGet.ID, []byte(got)); seen < 7 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	got, err = env.invoke(organizationGet.ID, "org", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if seen := assertOutputDeclared(t, organizationGet.ID, []byte(got)); seen < 25 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"name": "Acme", "created_at": "2020-01-01T00:00:00Z", "service_name": "make",
		"next_reset": "2026-11-01T00:00:00Z", "last_reset": "2026-10-01T00:00:00Z", "is_paused": true,
		"country_id": 7.0, "timezone_id": 8.0, "deleted": true, "product_name": "Pro", "sso_type": "saml2",
		"scenarios": 12.0, "active_scenarios": 4.0, "tfa_enforced": true, "user_session_timeout": 86400.0} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	for _, want := range []string{
		`"teams":[{"id":1001,"name":"Own"},{"id":9}]`,
		`"feature_controls":[{"enabled":true,"name":"a"},{"enabled":false,"name":"b"}]`,
		`"private_spaces":[{"id":77}]`, `"big":9007199254740993`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %s: %s", want, got)
		}
	}
	for _, bad := range []string{"Foreign", "Mine", "is_owner", "has_admin_visibility", "nested"} {
		if strings.Contains(got, bad) {
			t.Errorf("result leaks %s: %s", bad, got)
		}
	}
}

func TestOrganizationGetWithoutNewFieldsIsUnchanged(t *testing.T) {
	var calls []call
	body := `{"organization":{"id":501,"zone":"eu1.make.com","license":{"operations":10000}}}`
	env := newEnvironment(t, &calls, teamsHandler(t, ownOrg, body))
	got, err := env.invoke(organizationGet.ID, "org", `{}`)
	if want := `{"id":501,"license":{"operations":10000},"zone":"eu1.make.com"}`; err != nil || got != want {
		t.Fatalf("got = %s, %v, want %s", got, err, want)
	}
	got, err = env.invoke(teamGet.ID, "open", `{}`)
	if err != nil || strings.Contains(got, `"type"`) {
		t.Fatalf("team.get = %s, %v", got, err)
	}
}

func TestOrganizationGetListsAreBounded(t *testing.T) {
	var teams, controls, spaces []string
	for i := 1; i <= maxOrgTeams+1; i++ {
		teams = append(teams, `{"id":`+strconv.Itoa(i+5000)+`,"name":"x"}`)
	}
	for i := 0; i <= maxFeatureControls; i++ {
		controls = append(controls, `{"name":"f`+strconv.Itoa(i)+`","enabled":true}`)
	}
	for i := 1; i <= maxPrivateSpaces+1; i++ {
		spaces = append(spaces, `{"id":`+strconv.Itoa(i)+`}`)
	}
	body := `{"organization":{"id":501,"teams":[` + strings.Join(teams, ",") + `],"featureControls":[` +
		strings.Join(controls, ",") + `],"privateSpaces":[` + strings.Join(spaces, ",") + `]}}`
	var calls []call
	env := newEnvironment(t, &calls, teamsHandler(t, ownOrg, body))
	got, err := env.invoke(organizationGet.ID, "org", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Teams     []any `json:"teams"`
		Controls  []any `json:"feature_controls"`
		Spaces    []any `json:"private_spaces"`
		Truncated bool  `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Teams) != maxOrgTeams || len(out.Controls) != maxFeatureControls || len(out.Spaces) != maxPrivateSpaces ||
		!out.Truncated {
		t.Fatalf("lengths %d/%d/%d truncated %v", len(out.Teams), len(out.Controls), len(out.Spaces), out.Truncated)
	}
}

func TestScalarLicenseKeepsLargeIntegersExact(t *testing.T) {
	got := scalarLicense(map[string]json.RawMessage{
		"big": json.RawMessage(`9007199254740993`), "small": json.RawMessage(`10000`),
		"exp": json.RawMessage(`1e3`), "dec": json.RawMessage(`10000.0`), "frac": json.RawMessage(`1.5`),
	})
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"big":9007199254740993,"dec":10000,"exp":1000,"frac":1.5,"small":10000}`
	if string(out) != want {
		t.Fatalf("license = %s, want %s", out, want)
	}
}

const richListedTeam = `{"id":3001,"name":"A","organizationId":501,"type":"personal","activeScenarios":5,"activeApps":6}`

func TestTeamsListFiltersAndFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"teams":[`+richListedTeam+`]}`), nil
	})
	got, err := env.invoke(teamsList.ID, "orgmode",
		`{"offset":10,"limit":1,"include_private_spaces":true,"sort_dir":"desc"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"personal"`, `"active_scenarios":5`, `"active_apps":6`, `"offset":10`,
		`"has_more":true`, `"truncated":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %s: %s", want, got)
		}
	}
	if seen := assertOutputDeclared(t, teamsList.ID, []byte(got)); seen < 8 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	q := calls[0].query
	for k, want := range map[string]string{"pg[offset]": "10", "pg[limit]": "1", "pg[sortDir]": "desc",
		"includePrivateSpaces": "true", "organizationId": itoa64(ownOrg)} {
		if q.Get(k) != want {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), want)
		}
	}
	cols := strings.Join(q["cols[]"], ",")
	if !strings.Contains(cols, "activeScenarios") || !strings.Contains(cols, "activeApps") || !strings.Contains(cols, "type") {
		t.Errorf("cols = %s", cols)
	}
}

func TestTeamsListDefaultsAndHasMore(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"teams":[`+orgTeamBody(orgTeam, ownOrg, "A")+`]}`), nil
	})
	got, err := env.invoke(teamsList.ID, "orgmode", `{}`)
	if err != nil || !strings.Contains(got, `"has_more":false`) || !strings.Contains(got, `"truncated":false`) ||
		strings.Contains(got, "active_") {
		t.Fatalf("got = %s, %v", got, err)
	}
	q := calls[0].query
	if q.Get("pg[limit]") != "200" || q.Get("pg[offset]") != "0" || q.Has("includePrivateSpaces") || q.Has("pg[sortDir]") {
		t.Errorf("query = %v", q)
	}
}

func TestTeamsListRejectsInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"teams":[]}`), nil
	})
	for _, arguments := range []string{`{"offset":-1}`, `{"limit":0}`, `{"limit":201}`, `{"sort_dir":"up"}`,
		`{"sort_dir":"ASC"}`, `{"include_private_spaces":"yes"}`, `{"sort_by":"name"}`, `{"organization_id":1}`} {
		if _, err := env.invoke(teamsList.ID, "orgmode", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("%d requests were sent", len(calls))
	}
}

func TestTeamsMutationsStillRefusePersonalSpaces(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"team":`+richListedTeam+`}`), nil
	})
	if _, err := env.confirmed(teamsUpdate.ID, "orgmode", `{"team_id":3001,"name":"x"}`); !isInvalidRequest(err) {
		t.Fatalf("update err = %v", err)
	}
	if _, err := env.confirmed(teamsDelete.ID, "orgdelete", `{"team_id":3001,"confirmed":true}`); !isInvalidRequest(err) {
		t.Fatalf("delete err = %v", err)
	}
	for _, c := range calls {
		if c.method != http.MethodGet {
			t.Fatalf("changing request sent: %+v", c)
		}
	}
}

func TestCleanTextReplacesControlsAndCutsOnRuneBoundary(t *testing.T) {
	if got := cleanText("A\u0007B\nC", 256); got != "A B C" {
		t.Fatalf("cleanText = %q", got)
	}
	if got := cleanText("abää", 3); got != "ab" {
		t.Fatalf("cut = %q, want a rune boundary", got)
	}
}

func TestOrganizationGetTextIsCleaned(t *testing.T) {
	var calls []call
	body := `{"organization":{"id":501,"name":"A\u0007B\nC","productName":"` + strings.Repeat("x", 300) + `"}}`
	env := newEnvironment(t, &calls, teamsHandler(t, ownOrg, body))
	got, err := env.invoke(organizationGet.ID, "org", `{}`)
	if err != nil || !strings.Contains(got, `"name":"A B C"`) || strings.Contains(got, strings.Repeat("x", 257)) {
		t.Fatalf("got = %s, %v", got, err)
	}
}
