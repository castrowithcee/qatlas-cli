package n8n

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const ownUser = "USER_OWN_0001AAAA"

const userBody = `{"id":"USER_OWN_0001AAAA","email":"a@example.com","firstName":"Ann","lastName":"Lee",` +
	`"isPending":false,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z",` +
	`"mfaEnabled":true,"role":"global:member"}`

func userServer(t *testing.T, mutations *[]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		raw := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			raw = string(b)
		}
		if r.Method != http.MethodGet {
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+raw)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/users":
			return jsonResponse(200, `{"data":[`+userBody+`],"nextCursor":"next"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/users/"+ownUser:
			return jsonResponse(200, userBody), nil
		case r.URL.Path == apiPath+"/users/"+ownUser, r.URL.Path == apiPath+"/users/"+ownUser+"/role":
			return jsonResponse(204, ``), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func TestUserToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, userServer(t, &mutations))
	result, err := env.invoke(usersList.ID, "pdelete-open", `{"limit":5,"cursor":"c1"}`)
	if err != nil || calls[0].method != http.MethodGet || calls[0].path != apiPath+"/users" ||
		calls[0].query.Get("limit") != "5" || calls[0].query.Get("cursor") != "c1" ||
		calls[0].query.Get("includeRole") != "true" || calls[0].query.Get("projectId") != "" ||
		!strings.Contains(result, `"has_more":true`) || !strings.Contains(result, `"role":"global:member"`) ||
		strings.Contains(result, "mfa") {
		t.Fatalf("list: %s, %v, %+v", result, err, calls)
	}
	result, err = env.invoke(usersGet.ID, "pdelete-open", fmt.Sprintf(`{"user_id":%q}`, ownUser))
	if err != nil || !strings.Contains(result, `"first_name":"Ann"`) || calls[1].method != http.MethodGet ||
		calls[1].path != apiPath+"/users/"+ownUser || calls[1].query.Get("includeRole") != "true" {
		t.Fatalf("get: %s, %v, %+v", result, err, calls)
	}
	for _, s := range []struct{ operation, arguments string }{
		{usersSetRole.ID, fmt.Sprintf(`{"user_id":%q,"role":"global:admin"}`, ownUser)},
		{usersDelete.ID, fmt.Sprintf(`{"user_id":%q,"transfer_project_id":"PROJ_1"}`, ownUser)},
		{usersDelete.ID, fmt.Sprintf(`{"user_id":%q,"delete_owned_resources":true}`, ownUser)},
	} {
		if _, err := env.confirmed(s.operation, "pdelete-open", s.arguments); err != nil {
			t.Fatalf("%s: %v", s.operation, err)
		}
	}
	want := []string{
		fmt.Sprintf(`PATCH %s/users/%s/role? {"newRoleName":"global:admin"}`, apiPath, ownUser),
		fmt.Sprintf(`DELETE %s/users/%s?transferId=PROJ_1 `, apiPath, ownUser),
		fmt.Sprintf(`DELETE %s/users/%s? `, apiPath, ownUser),
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q, want %q", mutations, want)
	}
}

func userArgs() map[string]string {
	return map[string]string{
		usersList.ID:    `{}`,
		usersGet.ID:     fmt.Sprintf(`{"user_id":%q}`, ownUser),
		usersSetRole.ID: fmt.Sprintf(`{"user_id":%q,"role":"global:member"}`, ownUser),
		usersDelete.ID:  fmt.Sprintf(`{"user_id":%q,"delete_owned_resources":true}`, ownUser),
	}
}

func TestUserToolsAreRefusedLocallyOnATargetedConnection(t *testing.T) {
	for operation, args := range userArgs() {
		for _, connection := range []string{"project", "workflow", "both", "pdelete", "pdelete-workflow"} {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(operation, connection, args)
			if err == nil || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("%s on %s: err = %v, calls = %+v", operation, connection, err, calls)
			}
			for _, leaked := range []string{ownProject, ownWorkflow} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("%s on %s leaked a target: %v", operation, connection, err)
				}
			}
		}
	}
}

func TestUserArgumentsAreValidatedBeforeAnyRequest(t *testing.T) {
	id := fmt.Sprintf("%q", ownUser)
	for _, tt := range []struct{ operation, arguments string }{
		{usersGet.ID, `{"user_id":"a@example.com"}`},
		{usersGet.ID, `{"user_id":"a/b"}`},
		{usersSetRole.ID, `{"user_id":` + id + `,"role":"global:owner"}`},
		{usersSetRole.ID, `{"user_id":` + id + `,"role":"custom:role"}`},
		{usersSetRole.ID, `{"user_id":"../x","role":"global:member"}`},
		{usersDelete.ID, `{"user_id":` + id + `}`},
		{usersDelete.ID, `{"user_id":` + id + `,"transfer_project_id":"P1","delete_owned_resources":true}`},
		{usersDelete.ID, `{"user_id":` + id + `,"delete_owned_resources":false}`},
		{usersDelete.ID, `{"user_id":` + id + `,"transfer_project_id":"a/b"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(tt.operation, "pdelete-open", tt.arguments)
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s %s: err = %v, calls = %+v", tt.operation, tt.arguments, err, calls)
		}
	}
}

func TestUserChangesRequireConfirmation(t *testing.T) {
	for _, operation := range []string{usersSetRole.ID, usersDelete.ID} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(operation, "pdelete-open", userArgs()[operation])
		if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestUserDeleteIsOnlyOfferedByAToolsListAndProfilesAreOwn(t *testing.T) {
	r := usersDelete.Risk
	if !usersDelete.RequiresToolAllowList || r.Effect != "delete" || r.Confirmation != "required" ||
		r.DataSensitivity != "n8n-users-personal" || usersSetRole.RequiresToolAllowList || usersList.Risk.Confirmation != "none" ||
		!strings.Contains(usersDelete.Description, "permanently") ||
		!strings.Contains(usersDelete.Description, "transfer_project_id") {
		t.Fatalf("descriptor = %+v", usersDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		got[profile.ID] = profile.Tools
		for _, id := range profile.Tools {
			if id == usersDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.users.") && !strings.HasPrefix(profile.ID, "users") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	if len(got["users-read"]) != 2 || len(got["users-manage"]) != 3 {
		t.Fatalf("profiles = %v", got)
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(usersDelete.ID, "open", userArgs()[usersDelete.ID])
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestUserForbiddenIsReportedAsOwnerOrScopeError(t *testing.T) {
	for operation, args := range userArgs() {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(operation, "pdelete-open", args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "owner") ||
			strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestUserChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation string
		respond         func() (*http.Response, error)
	}{
		{"role transport", usersSetRole.ID, func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete 502", usersDelete.ID, func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return tt.respond() })
			_, err := env.confirmed(tt.operation, "pdelete-open", userArgs()[tt.operation])
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}
