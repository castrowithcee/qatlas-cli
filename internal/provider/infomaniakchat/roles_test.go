package infomaniakchat

import (
	"net/http"
	"strings"
	"testing"
)

const roleID = "role0000000000000000000a1"

func roleHandler(t *testing.T, answer string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, bothTeams), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/roles/names":
			return jsonResponse(200, answer), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

const roleAnswer = `[{"id":"` + roleID + `","name":"team_user","display_name":"Member","description":"d",` +
	`"permissions":["view_team","list_team_channels"],"scheme_managed":true,"built_in":true,"leak":"canary"},` +
	`{"id":"` + roleID + `","name":"system_admin","display_name":"x","permissions":[]}]`

func TestRolesGetSendsFixedPostAndDropsUnrequested(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, roleHandler(t, roleAnswer))
	result, err := env.invoke(rolesGet.ID, "botall", `{"names":["team_user","channel_admin"]}`)
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodPost || calls[1].path != "/api/v4/roles/names" ||
		calls[1].body != `["team_user","channel_admin"]` || strings.Contains(result, "system_admin") ||
		strings.Contains(result, "canary") || !strings.Contains(result, `"count":1`) ||
		!strings.Contains(result, "list_team_channels") {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

func TestRolesGetRefusesChannelTargetBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, roleHandler(t, roleAnswer))
	_, err := env.invoke(rolesGet.ID, "botch", `{"names":["team_user"]}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestRolesGetRefusesBadNamesBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, roleHandler(t, roleAnswer))
	many := make([]string, 21)
	for i := range many {
		many[i] = `"r` + strings.Repeat("x", i) + `"`
	}
	for _, args := range []string{`{"names":["Bad-Name"]}`, `{"names":[]}`, `{"names":[` + strings.Join(many, ",") + `]}`,
		`{"names":["team_user","team_user"]}`, `{"names":["` + strings.Repeat("a", 65) + `"]}`} {
		if _, err := env.invoke(rolesGet.ID, "botall", args); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("args = %.60s, err = %v, calls = %+v, reads = %d", args, err, calls, *env.reads)
		}
	}
}

func TestRolesGetProviderErrorTextIsNotShown(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/roles/names" {
			return jsonResponse(500, `{"message":"provider-secret-text"}`), nil
		}
		return jsonResponse(200, bothTeams), nil
	})
	_, err := env.invoke(rolesGet.ID, "botall", `{"names":["team_user"]}`)
	if err == nil || strings.Contains(err.Error(), "provider-secret-text") {
		t.Fatalf("err = %v", err)
	}
}

func TestRolesGetIsAdministrationAndInNoProfile(t *testing.T) {
	d := withGroup(rolesGet)
	if d.Group != "administration" || !d.RequiresToolAllowList || !d.Risk.OpenWorld {
		t.Fatalf("descriptor = %+v", d)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == rolesGet.ID {
				t.Fatalf("roles.get in profile %s", profile.ID)
			}
		}
	}
}
