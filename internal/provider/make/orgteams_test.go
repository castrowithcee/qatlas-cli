package makeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const orgTeam int64 = 3001

func orgTeamBody(id, org int64, name string) string {
	return `{"id":` + itoa64(id) + `,"name":"` + name + `","organizationId":` + itoa64(org) + `,"type":"standard"}`
}

func TestOrganizationModeTargetsAreStrict(t *testing.T) {
	bound, err := parseScope([]string{"organization/" + itoa64(ownOrg)})
	if err != nil || !bound.organizationMode() || bound.orgID != ownOrg {
		t.Fatalf("scope = %+v, %v", bound, err)
	}
	bound, err = parseScope([]string{"team/1", "organization/2"})
	if err != nil || bound.organizationMode() {
		t.Fatalf("team mode = %+v, %v", bound, err)
	}
	for _, values := range [][]string{{"organization/2", "scenario/3"}, {"organization/9999", "scenario/12345"}} {
		_, err = parseScope(values)
		if err == nil || strings.Contains(err.Error(), "9999") || strings.Contains(err.Error(), "12345") {
			t.Fatalf("parseScope(%v) = %v, want an error quoting no value", values, err)
		}
	}
}

// Every team tool refuses on an organization connection before it reads a secret or sends a request.
func TestEveryTeamToolRefusesOrganizationModeBeforeSecretAndIO(t *testing.T) {
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no request may be sent")
	})
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	checked := 0
	for _, d := range reg.Provider(Provider) {
		if strings.HasPrefix(d.ID, Provider+".teams.") {
			continue
		}
		_, handler, ok := reg.Lookup(d.ID)
		if !ok || len(d.Examples) == 0 {
			t.Fatalf("%s: no handler or example", d.ID)
		}
		_, err := handler(context.Background(), resolvedConnection("organization/"+itoa64(ownOrg)),
			resolver(red, &reads), red, d.Examples[0].Arguments)
		if err == nil {
			t.Fatalf("%s accepted an organization connection", d.ID)
		}
		checked++
	}
	if checked < 50 || reads != 0 || len(calls) != 0 {
		t.Fatalf("checked = %d, reads = %d, calls = %+v", checked, reads, calls)
	}
}

func TestTeamToolsRefuseOrganizationModeWithTheModeMessage(t *testing.T) {
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return nil, errors.New("no") })
	red := &redact.Redactor{}
	reg := registry(t)
	for _, id := range []string{teamGet.ID, teamUsage.ID, teamMembers.ID, organizationGet.ID, scenariosList.ID,
		teamVariablesList.ID, hooksList.ID} {
		_, handler, _ := reg.Lookup(id)
		_, err := handler(context.Background(), resolvedConnection("organization/"+itoa64(ownOrg)),
			resolver(red, nil), red, json.RawMessage(`{}`))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "needs a team connection") {
			t.Fatalf("%s err = %v", id, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestTeamsToolsRefuseTeamModeBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return nil, errors.New("no") })
	for _, tt := range []struct{ op, args string }{
		{teamsList.ID, `{}`}, {teamsCreate.ID, `{"name":"x"}`}, {teamsUpdate.ID, `{"team_id":5,"name":"x"}`},
	} {
		_, err := env.confirmed(tt.op, "open", tt.args)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "needs an organization connection") {
			t.Fatalf("%s err = %v", tt.op, err)
		}
	}
	_, handler, _ := registry(t).Lookup(teamsDelete.ID)
	_, err := handler(context.Background(), resolvedConnection("team/"+itoa64(ownTeam)), resolver(env.red, env.reads),
		env.red, json.RawMessage(`{"team_id":5,"confirmed":true}`))
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("delete err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestTeamsListReadsBoundOrganizationOnly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/teams" || r.Method != http.MethodGet {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"teams":[`+orgTeamBody(orgTeam, ownOrg, "A")+`,`+
			orgTeamBody(9, foreignOrg, foreignCanary)+`]}`), nil
	})
	got, err := env.invoke(teamsList.ID, "orgmode", `{}`)
	if err != nil || strings.Contains(got, foreignCanary) || !strings.Contains(got, `"count":1`) {
		t.Fatalf("got = %s, %v", got, err)
	}
	if len(calls) != 1 || calls[0].query.Get("organizationId") != itoa64(ownOrg) {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestTeamsCreateSendsBoundOrganizationOnce(t *testing.T) {
	var calls []call
	var body map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		return jsonResponse(200, `{"team":`+orgTeamBody(orgTeam, ownOrg, "New")+`}`), nil
	})
	if _, err := env.invoke(teamsCreate.ID, "orgmode", `{"name":"New"}`); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("unconfirmed err = %v, calls = %d", err, len(calls))
	}
	got, err := env.confirmed(teamsCreate.ID, "orgmode", `{"name":"New","operations_limit":5000}`)
	if err != nil || !strings.Contains(got, `"id":3001`) {
		t.Fatalf("got = %s, %v", got, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].path != apiPath+"/teams" ||
		body["organizationId"] != float64(ownOrg) || body["name"] != "New" || body["operationsLimit"] != float64(5000) {
		t.Fatalf("calls = %+v, body = %+v", calls, body)
	}
	if _, err := env.confirmed(teamsCreate.ID, "orgmode", `{"name":"New","organization_id":9}`); err == nil {
		t.Fatal("an organization argument must be refused")
	}
}

func TestTeamsUpdateAndDeleteBindTeamToOrganization(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/teams/"+itoa64(orgTeam):
			return jsonResponse(200, `{"team":`+orgTeamBody(orgTeam, ownOrg, "A")+`}`), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/teams/"+itoa64(foreignTeam):
			return jsonResponse(200, `{"team":`+orgTeamBody(foreignTeam, foreignOrg, foreignCanary)+`}`), nil
		case r.Method == http.MethodPatch && r.URL.Path == apiPath+"/teams/"+itoa64(orgTeam):
			return jsonResponse(200, `{"team":`+orgTeamBody(orgTeam, ownOrg, "Renamed")+`}`), nil
		case r.Method == http.MethodDelete && r.URL.Path == apiPath+"/teams/"+itoa64(orgTeam):
			return jsonResponse(200, `{"team":3001}`), nil
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	got, err := env.confirmed(teamsUpdate.ID, "orgmode", `{"team_id":3001,"name":"Renamed"}`)
	if err != nil || !strings.Contains(got, "Renamed") || len(calls) != 2 || calls[1].method != http.MethodPatch {
		t.Fatalf("update = %s, %v, calls = %+v", got, err, calls)
	}
	calls = nil
	_, err = env.confirmed(teamsUpdate.ID, "orgmode", `{"team_id":2002,"name":"x"}`)
	if !isInvalidRequest(err) || len(calls) != 1 || strings.Contains(err.Error(), foreignCanary) ||
		strings.Contains(err.Error(), itoa64(foreignOrg)) {
		t.Fatalf("foreign update err = %v, calls = %+v", err, calls)
	}
	if _, err = env.confirmed(teamsUpdate.ID, "orgmode", `{"team_id":3001}`); !isInvalidRequest(err) {
		t.Fatalf("empty update err = %v", err)
	}

	calls = nil
	if _, err = env.confirmed(teamsDelete.ID, "orgdelete", `{"team_id":3001,"confirmed":false}`); !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("unconfirmed delete err = %v, calls = %d", err, len(calls))
	}
	if _, err = env.invoke(teamsDelete.ID, "orgdelete", `{"team_id":3001,"confirmed":true}`); !isConfirmationRequired(err) {
		t.Fatalf("delete without confirm err = %v", err)
	}
	if _, err = env.confirmed(teamsDelete.ID, "orgdelete", `{"team_id":2002,"confirmed":true}`); !isInvalidRequest(err) {
		t.Fatalf("foreign delete err = %v", err)
	}
	for _, c := range calls {
		if c.method == http.MethodDelete {
			t.Fatalf("a delete was sent for a foreign team: %+v", calls)
		}
	}
	calls = nil
	got, err = env.confirmed(teamsDelete.ID, "orgdelete", `{"team_id":3001,"confirmed":true}`)
	if err != nil || !strings.Contains(got, `"deleted":true`) || len(calls) != 2 || calls[1].method != http.MethodDelete ||
		calls[1].query.Get("confirmed") != "true" {
		t.Fatalf("delete = %s, %v, calls = %+v", got, err, calls)
	}
	// Delete is offered only through a tools list, never on a plain connection.
	if _, err = env.confirmed(teamsDelete.ID, "orgmode", `{"team_id":3001,"confirmed":true}`); err == nil {
		t.Fatal("teams.delete must not be offered without a tools list")
	}
}

// A changing request is sent exactly once and never repeated; an unclear outcome is reported as uncertain.
func TestTeamsChangesAreNeverRetried(t *testing.T) {
	for name, respond := range map[string]func() (*http.Response, error){
		"5xx":      func() (*http.Response, error) { return jsonResponse(503, `{}`), nil },
		"timeout":  func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"foreign": func() (*http.Response, error) {
			return jsonResponse(200, `{"team":`+orgTeamBody(7, foreignOrg, "x")+`}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return jsonResponse(200, `{"team":`+orgTeamBody(orgTeam, ownOrg, "A")+`}`), nil
				}
				return respond()
			})
			for _, tt := range []struct{ op, args, conn string }{
				{teamsCreate.ID, `{"name":"x"}`, "orgmode"},
				{teamsUpdate.ID, `{"team_id":3001,"name":"x"}`, "orgmode"},
				{teamsDelete.ID, `{"team_id":3001,"confirmed":true}`, "orgdelete"},
			} {
				calls = nil
				_, err := env.confirmed(tt.op, tt.conn, tt.args)
				if tt.op == teamsDelete.ID && (name == "unusable" || name == "foreign") {
					continue // a delete answer has no body to judge; only the transport failures apply
				}
				var perr *provider.Error
				if err == nil || !errors.As(err, &perr) || !strings.Contains(err.Error(), "may have taken effect") {
					t.Fatalf("%s %s err = %v, want an uncertain failure", name, tt.op, err)
				}
				changes := 0
				for _, c := range calls {
					if c.method != http.MethodGet {
						changes++
					}
				}
				if changes != 1 {
					t.Fatalf("%s %s sent %d changing requests, want 1", name, tt.op, changes)
				}
			}
		})
	}
}

func TestTestConnectionInOrganizationMode(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `{"teams":[]}`), nil })
	red := &redact.Redactor{}
	class, err := TestConnection(context.Background(), resolvedConnection("organization/"+itoa64(ownOrg)), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK || len(calls) != 1 || calls[0].path != apiPath+"/teams" ||
		calls[0].query.Get("organizationId") != itoa64(ownOrg) {
		t.Fatalf("class = %q, %v, calls = %+v", class, err, calls)
	}
}
