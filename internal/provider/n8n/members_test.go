package n8n

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const memberUser = "user-example-0001"

func memberArgs(role string) string {
	return fmt.Sprintf(`{"project_id":%q,"user_id":%q,"role":%q}`, ownProject, memberUser, role)
}

func TestMemberToolsAreRefusedLocallyWithoutAProjectTargetOrWithBadInput(t *testing.T) {
	good := memberArgs("project:viewer")
	for _, tt := range []struct{ name, operation, connection, arguments string }{
		{"list without target", membersList.ID, "pdelete-open", fmt.Sprintf(`{"project_id":%q}`, ownProject)},
		{"add without target", membersAdd.ID, "pdelete-open", good},
		{"set_role without target", membersSetRole.ID, "pdelete-open", good},
		{"remove without target", membersRemove.ID, "pdelete-open", fmt.Sprintf(`{"project_id":%q,"user_id":"u1"}`, ownProject)},
		{"list foreign", membersList.ID, "pdelete", fmt.Sprintf(`{"project_id":%q}`, foreignProject)},
		{"add foreign", membersAdd.ID, "pdelete", strings.Replace(good, ownProject, foreignProject, 1)},
		{"list under workflow list", membersList.ID, "pdelete-workflow", fmt.Sprintf(`{"project_id":%q}`, ownProject)},
		{"bad project id", membersList.ID, "pdelete", `{"project_id":"a/../b"}`},
		{"bad user id", membersAdd.ID, "pdelete", fmt.Sprintf(`{"project_id":%q,"user_id":"a b","role":"project:viewer"}`, ownProject)},
		{"remove bad user id", membersRemove.ID, "pdelete", fmt.Sprintf(`{"project_id":%q,"user_id":"a?b"}`, ownProject)},
		{"owner role", membersAdd.ID, "pdelete", memberArgs("project:personalOwner")},
		{"instance role", membersSetRole.ID, "pdelete", memberArgs("global:admin")},
		{"empty role", membersSetRole.ID, "pdelete", memberArgs("")},
		{"extra field", membersAdd.ID, "pdelete", strings.Replace(good, "}", `,"email":"a@example.org"}`, 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(tt.operation, tt.connection, tt.arguments)
			if err == nil || (!isInvalidRequest(err) && tt.name != "extra field") {
				t.Fatalf("err = %v, want a local refusal", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			if strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("error names the foreign project: %v", err)
			}
		})
	}
}

func TestMemberChangesNeedConfirmation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{membersAdd.ID, memberArgs("project:editor")},
		{membersSetRole.ID, memberArgs("project:editor")},
		{membersRemove.ID, fmt.Sprintf(`{"project_id":%q,"user_id":%q}`, ownProject, memberUser)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(tt.operation, "pdelete", tt.arguments)
		if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", tt.operation, err, calls)
		}
	}
}

func TestMemberListBoundsOutputAndUsesTheRightRoute(t *testing.T) {
	long := strings.Repeat("x", 600)
	body := fmt.Sprintf(`{"data":[{"id":"u1","email":"a@example.org","firstName":"Ada","lastName":null,`+
		`"role":"project:admin","createdAt":"x","updatedAt":"y"},`+
		`{"id":"u2","email":%q,"firstName":%q,"lastName":"B","role":null,"createdAt":"x","updatedAt":"y"},`+
		`{"id":"u3","email":"c@example.org","firstName":null,"lastName":null,"role":"project:viewer"}],`+
		`"nextCursor":"next"}`, long, long)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != apiPath+"/projects/"+ownProject+"/users" {
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, body), nil
	})
	result, err := env.invoke(membersList.ID, "pdelete", fmt.Sprintf(`{"project_id":%q,"limit":2}`, ownProject))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page MembersPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 || !page.Truncated ||
		!page.HasMore || page.Cursor != "next" || page.Members[0].Name != "Ada" || page.Members[0].Role != "project:admin" {
		t.Fatalf("page = %s, %v", result, err)
	}
	if len(page.Members[1].Email) != maxMemberFieldLength || len(page.Members[1].Name) > maxMemberFieldLength {
		t.Fatalf("fields not bounded: %d, %d", len(page.Members[1].Email), len(page.Members[1].Name))
	}
	if strings.Contains(result, "createdAt") || calls[0].query.Get("limit") != "2" {
		t.Fatalf("result = %s, query = %v", result, calls[0].query)
	}
}

func TestMemberChangesSendOneRequestWithTheRightBody(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		bodies = append(bodies, string(raw))
		user := apiPath + "/projects/" + ownProject + "/users"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == user:
			return jsonResponse(201, ``), nil
		case r.Method == http.MethodPatch && r.URL.Path == user+"/"+memberUser,
			r.Method == http.MethodDelete && r.URL.Path == user+"/"+memberUser:
			return jsonResponse(204, ``), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	for _, tt := range []struct{ operation, arguments, want string }{
		{membersAdd.ID, memberArgs("project:viewer"), `"added":true`},
		{membersSetRole.ID, memberArgs("project:editor"), `"updated":true`},
		{membersRemove.ID, fmt.Sprintf(`{"project_id":%q,"user_id":%q}`, ownProject, memberUser), `"removed":true`},
	} {
		if result, err := env.confirmed(tt.operation, "pdelete", tt.arguments); err != nil || !strings.Contains(result, tt.want) {
			t.Fatalf("%s = %s, %v", tt.operation, result, err)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want one request each", calls)
	}
	want := []string{`{"relations":[{"role":"project:viewer","userId":"` + memberUser + `"}]}`,
		`{"role":"project:editor"}`, ""}
	for i := range want {
		if bodies[i] != want[i] {
			t.Fatalf("body %d = %q, want %q", i, bodies[i], want[i])
		}
	}
}

func TestMemberRemoveIsOnlyOfferedByAToolsListAndProfilesAreSeparate(t *testing.T) {
	if !membersRemove.RequiresToolAllowList || membersRemove.Risk.Confirmation != "required" ||
		membersRemove.Risk.Effect != "delete" || membersAdd.Risk.DataSensitivity != memberDataSensitivity ||
		!membersAdd.Risk.OpenWorld {
		t.Fatalf("descriptors wrong: %+v", membersRemove)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		if profile.Recommended && strings.HasPrefix(profile.ID, "members") {
			t.Fatalf("profile %q is recommended", profile.ID)
		}
		for _, id := range profile.Tools {
			if id == membersRemove.ID {
				t.Fatalf("profile %q selects remove", profile.ID)
			}
			if strings.HasPrefix(id, "n8n.projectmembers.") && !strings.HasPrefix(profile.ID, "members") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
		got[profile.ID] = profile.Tools
	}
	if strings.Join(got["members-read"], ",") != membersList.ID ||
		strings.Join(got["members-manage"], ",") != strings.Join([]string{membersList.ID, membersAdd.ID, membersSetRole.ID}, ",") {
		t.Fatalf("profiles = %v", got)
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(membersRemove.ID, "open", fmt.Sprintf(`{"project_id":%q,"user_id":"u1"}`, ownProject))
	if err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, want remove unavailable without a tools list", err)
	}
}

func TestMemberForbiddenIsALicenseOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{membersList.ID, fmt.Sprintf(`{"project_id":%q}`, ownProject)},
		{membersAdd.ID, memberArgs("project:viewer")},
		{membersSetRole.ID, memberArgs("project:viewer")},
		{membersRemove.ID, fmt.Sprintf(`{"project_id":%q,"user_id":%q}`, ownProject, memberUser)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "license or role missing") ||
			strings.Contains(err.Error(), foreignCanary) || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %d", tt.operation, err, len(calls))
		}
	}
}

func TestMemberChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"add 5xx", membersAdd.ID, memberArgs("project:viewer"),
			func() (*http.Response, error) { return jsonResponse(500, `{"message":"`+foreignCanary+`"}`), nil }},
		{"set_role transport", membersSetRole.ID, memberArgs("project:viewer"),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"remove 502", membersRemove.ID, fmt.Sprintf(`{"project_id":%q,"user_id":%q}`, ownProject, memberUser),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return tt.respond() })
			_, err := env.confirmed(tt.operation, "pdelete", tt.arguments)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), foreignCanary) || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %d", err, len(calls))
			}
		})
	}
}
