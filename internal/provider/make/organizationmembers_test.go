package makeapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

const rolesBody = `{"usersRoles":[{"id":1,"name":"Owner","identifier":"owner","managementType":"system"},` +
	`{"id":2,"name":"Member","identifier":"member","managementType":"system"}]}`

func TestOrganizationPeopleToolsRefuseTeamModeBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return nil, errors.New("no") })
	if _, err := env.confirmed(organizationMembers.ID, "team", `{}`); err == nil {
		t.Fatalf("%s accepted a team connection", organizationMembers.ID)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOrganizationMembersReadsRolesThenUsersOfTheBoundOrganization(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/users/roles") {
			return jsonResponse(200, rolesBody), nil
		}
		return jsonResponse(200, `{"users":[{"id":7,"name":"Pat","email":"p@example.com"}]}`), nil
	})
	got, err := env.invoke(organizationMembers.ID, "orgmode", `{}`)
	if err != nil || !strings.Contains(got, `"role_name":"Member"`) || !strings.Contains(got, `"user_id":7`) {
		t.Fatalf("got = %s, %v", got, err)
	}
	if len(calls) != 3 || calls[0].method != http.MethodGet {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if c.query.Get("organizationId") != itoa64(ownOrg) {
			t.Fatalf("call outside the bound organization: %+v", c)
		}
	}
}
