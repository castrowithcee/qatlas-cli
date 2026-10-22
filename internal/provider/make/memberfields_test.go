package makeapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const richRoles = `{"usersRoles":[{"id":1,"name":"Own\u0007er","identifier":"owner","subsidiary":false,` +
	`"category":"organization","permissions":["a:read","b:write"],"managementType":"system_locked",` +
	`"description":"Full\u0001 access"},{"id":2,"name":"Empty","identifier":"empty"}]}`

const richUser = `{"id":7,"name":"Pat","email":"p@example.com","language":"de\u0007","timezoneId":8,"localeId":9,` +
	`"countryId":10,"features":{"allow_apps":false},"avatar":"https://cdn.example.com/a/7.png",` +
	`"lastLogin":"2026-10-01T10:00:00Z","tfaStatus":1,"supportEligible":true,"userTeamIds":[11,12],` +
	`"privateSpace":{"id":13,"name":"Pat\u0001 space","globalAgentsEnabled":true,"type":"personal",` +
	`"operationsLimit":1000,"transferLimit":9007199254740993,"consumedOperations":0,"consumedTransfer":5,` +
	`"isPaused":false,"consumedCenticredits":6}}`

func membersHandler(users map[string]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/users/roles") {
			return jsonResponse(200, richRoles), nil
		}
		body := users[r.URL.Query().Get("organizationRoleId")]
		return jsonResponse(200, `{"users":[`+body+`]}`), nil
	}
}

func TestOrganizationMembersReportEveryField(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, membersHandler(map[string]string{"1": richUser}))
	got, err := env.invoke(organizationMembers.ID, "orgmode", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if seen := assertOutputDeclared(t, organizationMembers.ID, []byte(got)); seen < 30 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	for _, want := range []string{`"language":"de"`, `"timezone_id":8`, `"locale_id":9`, `"country_id":10`,
		`"features":{"allow_apps":false}`, `"avatar":"https://cdn.example.com/a/7.png"`,
		`"last_login":"2026-10-01T10:00:00Z"`, `"support_eligible":true`, `"user_team_ids":[11,12]`,
		`"id":13`, `"name":"Pat  space"`, `"global_agents_enabled":true`, `"type":"personal"`,
		`"operations_limit":1000`, `"transfer_limit":9007199254740993`, `"consumed_operations":0`,
		`"consumed_transfer":5`, `"is_paused":false`, `"consumed_centicredits":6`,
		`"user_id":7`, `"role_id":1`, `"role_name":"Own\u0007er"`,
		`{"category":"organization","description":"Full  access","id":1,"identifier":"owner","management_type":"system_locked","name":"Own er","permissions":["a:read","b:write"],"subsidiary":false}`,
		`{"id":2,"identifier":"empty","name":"Empty"}`} {
		if !strings.Contains(canonical(t, got), want) && !strings.Contains(got, want) {
			t.Errorf("result lacks %s: %s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "tfa") {
		t.Errorf("result reports a two-factor status: %s", got)
	}
	cols := strings.Join(calls[1].query["cols[]"], ",")
	if want := "id,name,email,language,timezoneId,localeId,countryId,features,avatar,lastLogin,userTeamIds,privateSpace"; cols != want {
		t.Errorf("cols = %s, want %s", cols, want)
	}
}

// canonical re-encodes the role entries with sorted keys so a test can compare them as text.
func canonical(t *testing.T, got string) string {
	t.Helper()
	var out struct {
		Roles []map[string]any `json:"roles"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, r := range out.Roles {
		b, _ := json.Marshal(r)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n")
}

func TestOrganizationMembersWithoutNewFieldsIsUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/users/roles") {
			return jsonResponse(200, rolesBody), nil
		}
		return jsonResponse(200, `{"users":[{"id":7,"name":"Pat","email":"p@example.com"}]}`), nil
	})
	got, err := env.invoke(organizationMembers.ID, "orgmode", `{}`)
	want := `{"count":2,"members":[` +
		`{"email":"p@example.com","name":"Pat","role_id":1,"role_name":"Owner","user_id":7},` +
		`{"email":"p@example.com","name":"Pat","role_id":2,"role_name":"Member","user_id":7}],` +
		`"organization_id":501,"roles":[` +
		`{"id":1,"identifier":"owner","management_type":"system","name":"Owner"},` +
		`{"id":2,"identifier":"member","management_type":"system","name":"Member"}],"truncated":false}`
	if err != nil || got != want {
		t.Fatalf("got = %s, %v\nwant %s", got, err, want)
	}
}

func TestOrganizationMembersFiltersReachEveryRole(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, membersHandler(nil))
	_, err := env.invoke(organizationMembers.ID, "orgmode",
		`{"name":"Pat","email":"p@","filter_by_team_id":0,"ids":[3,4],"sort_by":"email","sort_dir":"desc"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	for _, c := range calls[1:] {
		q := c.query
		if q.Get("name") != "Pat" || q.Get("email") != "p@" || !q.Has("filterByTeamId") ||
			q.Get("filterByTeamId") != "0" || strings.Join(q["ids"], ",") != "3,4" ||
			q.Get("pg[sortBy]") != "email" || q.Get("pg[sortDir]") != "desc" {
			t.Errorf("query = %v", q)
		}
	}
	if q := calls[0].query; q.Has("name") || q.Has("filterByTeamId") || q.Has("pg[sortBy]") {
		t.Errorf("role request carries user filters: %v", q)
	}
}

func TestOrganizationMembersRejectsInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, membersHandler(nil))
	ids101 := "[" + strings.Repeat("1,", 100) + "1]"
	for _, arguments := range []string{`{"name":""}`, `{"name":"` + strings.Repeat("a", 257) + `"}`, `{"email":""}`,
		`{"email":"` + strings.Repeat("a", 257) + `"}`, `{"name":"   "}`, `{"filter_by_team_id":-1}`,
		`{"filter_by_team_id":"1"}`, `{"ids":[]}`, `{"ids":[0]}`, `{"ids":` + ids101 + `}`, `{"sort_by":"role"}`,
		`{"sort_dir":"up"}`, `{"tfa_status":1}`, `{"tfaStatus":1}`, `{"organization_id":1}`} {
		if _, err := env.invoke(organizationMembers.ID, "orgmode", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOrganizationMembersOmitsCredentialAvatarsAndBoundsLists(t *testing.T) {
	var teams, perms []string
	for i := 1; i <= maxMemberTeamIDs+1; i++ {
		teams = append(teams, itoa64(int64(i)))
	}
	for i := 0; i <= maxRolePermissions; i++ {
		perms = append(perms, `"p`+itoa64(int64(i))+`"`)
	}
	users := map[string]string{
		"1": `{"id":1,"avatar":"https://cdn.example.com/a.png?X-Amz-Signature=abc"},` +
			`{"id":2,"avatar":"https://` + "u:p" + "@" + `cdn.example.com/a.png"},{"id":3,"avatar":"javascript:alert(1)"},` +
			`{"id":4,"avatar":"https://cdn.example.com/a.png?token=x"},{"id":5,"avatar":"https://cdn.example.com/a.png?s=80"},` +
			`{"id":6,"userTeamIds":[` + strings.Join(teams, ",") + `]}`}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/users/roles") {
			return jsonResponse(200, `{"usersRoles":[{"id":1,"name":"R","permissions":[`+strings.Join(perms, ",")+`]}]}`), nil
		}
		return jsonResponse(200, `{"users":[`+users["1"]+`]}`), nil
	})
	got, err := env.invoke(organizationMembers.ID, "orgmode", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Members []struct {
			UserID int64   `json:"user_id"`
			Avatar string  `json:"avatar"`
			Teams  []int64 `json:"user_team_ids"`
		} `json:"members"`
		Roles     []struct{ Permissions []string } `json:"roles"`
		Truncated bool                             `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Members {
		wantAvatar := ""
		if m.UserID == 5 {
			wantAvatar = "https://cdn.example.com/a.png?s=80"
		}
		if m.Avatar != wantAvatar {
			t.Errorf("user %d avatar = %q, want %q", m.UserID, m.Avatar, wantAvatar)
		}
		if m.UserID == 6 && len(m.Teams) != maxMemberTeamIDs {
			t.Errorf("teams = %d", len(m.Teams))
		}
	}
	if len(out.Roles[0].Permissions) != maxRolePermissions || !out.Truncated {
		t.Errorf("permissions %d truncated %v", len(out.Roles[0].Permissions), out.Truncated)
	}
}
