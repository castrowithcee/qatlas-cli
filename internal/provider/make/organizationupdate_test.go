package makeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestOrganizationUpdateRefusesTeamModeBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return nil, errors.New("no") })
	if _, err := env.confirmed(organizationUpdate.ID, "team", `{"name":"x"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestOrganizationUpdateSendsOneRequestToTheBoundOrganization(t *testing.T) {
	var calls []call
	var body map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		return jsonResponse(200, `{"organization":{"id":`+itoa64(ownOrg)+`}}`), nil
	})
	if _, err := env.invoke(organizationUpdate.ID, "orgmode", `{"name":"Acme"}`); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("unconfirmed err = %v", err)
	}
	if _, err := env.confirmed(organizationUpdate.ID, "orgmode", `{"name":"Acme (EU)","country_id":5,"timezone_id":9}`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodPatch ||
		calls[0].path != apiPath+"/organizations/"+itoa64(ownOrg) || body["name"] != "Acme (EU)" ||
		body["countryId"] != float64(5) || body["timezoneId"] != float64(9) || len(body) != 3 {
		t.Fatalf("calls = %+v, body = %+v", calls, body)
	}
	for _, bad := range []string{`{}`, `{"name":"a<b"}`, `{"name":""}`, `{"country_id":0}`,
		`{"organization_id":9,"name":"x"}`, `{"name":"x","plan":"y"}`} {
		calls = nil
		if _, err := env.confirmed(organizationUpdate.ID, "orgmode", bad); err == nil || len(calls) != 0 {
			t.Fatalf("%s accepted, calls = %d", bad, len(calls))
		}
	}
}

func TestOrganizationUpdateIsNeverRetried(t *testing.T) {
	for name, respond := range map[string]func() (*http.Response, error){
		"5xx":      func() (*http.Response, error) { return jsonResponse(503, `{}`), nil },
		"timeout":  func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"foreign": func() (*http.Response, error) {
			return jsonResponse(200, `{"organization":{"id":`+itoa64(foreignOrg)+`}}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return respond() })
			_, err := env.confirmed(organizationUpdate.ID, "orgmode", `{"name":"x"}`)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %d", err, len(calls))
			}
		})
	}
}
