package makeapi

import (
	"context"
	"encoding/json"
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
	for id, args := range map[string]string{organizationMembers.ID: `{}`,
		organizationInvite.ID: `{"email":"a@example.com","name":"A"}`} {
		if _, err := env.confirmed(id, "team", args); err == nil {
			t.Fatalf("%s accepted a team connection", id)
		}
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

func TestOrganizationInviteSendsOneRequestWithTheMemberRole(t *testing.T) {
	var calls []call
	var body map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return jsonResponse(200, rolesBody), nil
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return jsonResponse(200, `{}`), nil
	})
	args := `{"email":"p@example.com","name":"Pat"}`
	if _, err := env.invoke(organizationInvite.ID, "orgmode", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("unconfirmed err = %v", err)
	}
	if _, err := env.confirmed(organizationInvite.ID, "orgmode", args); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodPost ||
		calls[1].path != apiPath+"/organizations/"+itoa64(ownOrg)+"/invite" ||
		len(body) != 3 || body["usersRoleId"] != float64(2) || body["email"] != "p@example.com" {
		t.Fatalf("calls = %+v, body = %+v", calls, body)
	}
}

func TestOrganizationInviteRefusesBadInputAndAmbiguousRoleWithoutSending(t *testing.T) {
	var calls []call
	roles := rolesBody
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatal("a change was sent")
		}
		return jsonResponse(200, roles), nil
	})
	for _, bad := range []string{`{"email":"nope","name":"A"}`, `{"email":"Pat <p@example.com>","name":"A"}`,
		`{"email":"a@b","name":"A"}`, `{"email":"a@example.com","name":"A<"}`,
		`{"email":"a@example.com","name":"A","usersRoleId":1}`, `{"email":"a@example.com","name":"A","teamsId":[1]}`} {
		calls = nil
		_, err := env.confirmed(organizationInvite.ID, "orgmode", bad)
		if err == nil || len(calls) != 0 || strings.Contains(err.Error(), "example.com") {
			t.Fatalf("%s: err = %v, calls = %d", bad, err, len(calls))
		}
	}
	for _, r := range []string{`{"usersRoles":[]}`,
		`{"usersRoles":[{"id":2,"name":"Member","identifier":"member","managementType":"custom-managed"}]}`,
		`{"usersRoles":[{"id":2,"identifier":"member"},{"id":3,"name":"member"}]}`} {
		roles = r
		if _, err := env.confirmed(organizationInvite.ID, "orgmode", `{"email":"a@example.com","name":"A"}`); err == nil {
			t.Fatalf("role list %s accepted", r)
		}
	}
}

func TestOrganizationInviteIsNeverRetried(t *testing.T) {
	for name, respond := range map[string]func() (*http.Response, error){
		"5xx":     func() (*http.Response, error) { return jsonResponse(503, `{}`), nil },
		"timeout": func() (*http.Response, error) { return nil, context.DeadlineExceeded },
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return jsonResponse(200, rolesBody), nil
				}
				return respond()
			})
			_, err := env.confirmed(organizationInvite.ID, "orgmode", `{"email":"a@example.com","name":"A"}`)
			posts := 0
			for _, c := range calls {
				if c.method == http.MethodPost {
					posts++
				}
			}
			if err == nil || !strings.Contains(err.Error(), "may have been sent") || posts != 1 ||
				strings.Contains(err.Error(), "example.com") {
				t.Fatalf("err = %v, posts = %d", err, posts)
			}
		})
	}
}
