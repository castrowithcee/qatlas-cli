package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	userInA   = "useria0000000000000000a1"
	userInB   = "userib0000000000000000b2"
	userBoth  = "useric0000000000000000c3"
	userLeft  = "userid0000000000000000d4"
	nameInA   = "jane.doe"
	nameInB   = "foreign.user"
	sensitive = "sensitive-canary-5e7c"
)

// userJSONOf carries canary values in every field the provider must never output.
func userJSONOf(id, username string) string {
	return `{"id":"` + id + `","username":"` + username + `","first_name":"First","last_name":"Last",` +
		`"nickname":"Nick","position":"Dev","email":"` + username + `@example.invalid","is_bot":false,` +
		`"delete_at":0,"roles":"` + sensitive + `","auth_service":"` + sensitive + `",` +
		`"notify_props":{"email":"` + sensitive + `"},"props":{"k":"` + sensitive + `"},` +
		`"timezone":{"automaticTimezone":"` + sensitive + `"},"last_password_update":1,"mfa_active":true}`
}

// usersFixture is a fake instance: teamA holds userInA and userBoth, teamB holds userInB and userBoth, and
// the token's own user belongs to neither.
type usersFixture struct {
	t        *testing.T
	statuses string
}

func (f *usersFixture) members() map[string][]string {
	return map[string][]string{teamA: {userInA, userBoth}, teamB: {userInB, userBoth}}
}

func (f *usersFixture) users() map[string]string {
	return map[string]string{userInA: nameInA, userInB: nameInB, userBoth: "both.user", selfID: "self.user",
		userLeft: "left.user"}
}

func (f *usersFixture) handle(r *http.Request) (*http.Response, error) {
	users := f.users()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me":
		return jsonResponse(200, `{"id":"`+selfID+`"}`), nil
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v4/teams/") &&
		strings.HasSuffix(r.URL.Path, "/members/ids"):
		team := strings.Split(r.URL.Path, "/")[4]
		var asked []string
		_ = json.NewDecoder(r.Body).Decode(&asked)
		var out []string
		for _, id := range asked {
			for _, member := range f.members()[team] {
				if id == member {
					out = append(out, `{"team_id":"`+team+`","user_id":"`+id+`","delete_at":0}`)
				}
			}
			if team == teamA && id == userLeft {
				out = append(out, `{"team_id":"`+team+`","user_id":"`+id+`","delete_at":5}`)
			}
		}
		return jsonResponse(200, "["+strings.Join(out, ",")+"]"), nil
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users":
		var out []string
		for id, name := range users {
			out = append(out, userJSONOf(id, name))
		}
		return jsonResponse(200, "["+strings.Join(out, ",")+"]"), nil
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v4/users/username/"):
		name := strings.TrimPrefix(r.URL.Path, "/api/v4/users/username/")
		for id, candidate := range users {
			if candidate == name {
				return jsonResponse(200, userJSONOf(id, name)), nil
			}
		}
		return jsonResponse(404, `{}`), nil
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v4/users/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/v4/users/")
		if name, ok := users[id]; ok {
			return jsonResponse(200, userJSONOf(id, name)), nil
		}
		return jsonResponse(404, `{}`), nil
	case r.Method == http.MethodPost && r.URL.Path == "/api/v4/users/search":
		var out []string
		for id, name := range users {
			out = append(out, userJSONOf(id, name))
		}
		return jsonResponse(200, "["+strings.Join(out, ",")+"]"), nil
	case r.Method == http.MethodPost && r.URL.Path == "/api/v4/users/status/ids":
		return jsonResponse(200, f.statuses), nil
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanA:
		return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanB:
		return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	return nil, nil
}

func (f *usersFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

func pathsOf(calls []call, method string) []string {
	var out []string
	for _, c := range calls {
		if c.method == method {
			out = append(out, c.path)
		}
	}
	return out
}

func idsOf(t *testing.T, result string) map[string]bool {
	t.Helper()
	var page struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	ids := map[string]bool{}
	for _, u := range page.Users {
		ids[u["id"].(string)] = true
	}
	return ids
}

func TestUsersGetBindsToTheBoundTeamsByIDAndUsername(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(usersGet.ID, "team", `{"user_id":"`+userInA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(result), &entry); err != nil || entry["username"] != nameInA ||
		entry["deleted"] != false || entry["email"] == nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	allowed := map[string]bool{"id": true, "username": true, "first_name": true, "last_name": true,
		"nickname": true, "position": true, "email": true, "is_bot": true, "deleted": true}
	for key := range entry {
		if !allowed[key] {
			t.Errorf("output holds a field %q outside the allow-list", key)
		}
	}
	if strings.Contains(result, sensitive) {
		t.Fatalf("result leaked a canary: %s", result)
	}

	// Foreign by id: refused before the profile is read.
	calls = nil
	_, err = env.invoke(usersGet.ID, "team", `{"user_id":"`+userInB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	for _, p := range pathsOf(calls, http.MethodGet) {
		if p == "/api/v4/users/"+userInB {
			t.Fatalf("the foreign profile was read: %+v", calls)
		}
	}

	// Foreign by username: read, then refused without output.
	result, err = env.invoke(usersGet.ID, "team", `{"username":"`+nameInB+`"}`)
	if !isInvalidRequest(err) || result != "" || (err != nil && strings.Contains(err.Error(), nameInB)) {
		t.Fatalf("result = %q, err = %v, want an invalid request without output", result, err)
	}

	// An unknown username is refused exactly like a foreign one.
	_, unknownErr := env.invoke(usersGet.ID, "team", `{"username":"nobody.here"}`)
	if !isInvalidRequest(unknownErr) || err == nil || unknownErr.Error() != err.Error() {
		t.Fatalf("unknown username err = %v, foreign err = %v, want the same invalid request", unknownErr, err)
	}

	// Username of a member.
	if result, err = env.invoke(usersGet.ID, "team", `{"username":"`+nameInA+`"}`); err != nil ||
		!strings.Contains(result, userInA) {
		t.Fatalf("result = %s, %v", result, err)
	}

	// A user who left the bound team is not reachable.
	if _, err = env.invoke(usersGet.ID, "team", `{"user_id":"`+userLeft+`"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a former member", err)
	}

	// The own user is always reachable, without any team membership.
	if result, err = env.invoke(usersGet.ID, "team", `{"user_id":"`+selfID+`"}`); err != nil ||
		!strings.Contains(result, selfID) {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestUsersGetRefusesMalformedArgumentsBeforeSecretAndIO(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	env := f.env(&calls)
	for _, arguments := range []string{
		`{}`,
		`{"user_id":"` + userInA + `","username":"` + nameInA + `"}`,
		`{"user_id":"../` + userInA + `"}`,
		`{"username":"Jane Doe"}`,
		`{"username":"a/b"}`,
		`{"username":"` + strings.Repeat("a", 65) + `"}`,
	} {
		if _, err := env.invoke(usersGet.ID, "team", arguments); err == nil {
			t.Errorf("arguments %s were accepted", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
	}
}

func TestVerifyUsersScopeAsksOncePerBoundTeamUntilAllAreProven(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	serve(t, &calls, f.handle)
	client, err := open(t.Context(), resolvedConnection("team/"+teamA, "team/"+teamB), resolver(nil, nil), nil, freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	got, err := client.verifyUsersScope(t.Context(), "op", []string{userInB, selfID, userInA, userInA, userLeft})
	if err != nil || strings.Join(got, ",") != strings.Join([]string{userInB, selfID, userInA}, ",") {
		t.Fatalf("got = %v, %v", got, err)
	}
	if n := len(pathsOf(calls, http.MethodPost)); n != 2 {
		t.Fatalf("membership requests = %d, want one per bound team: %+v", n, calls)
	}

	calls = nil
	got, err = client.verifyUsersScope(t.Context(), "op", []string{userBoth, selfID})
	if err != nil || len(got) != 2 {
		t.Fatalf("got = %v, %v", got, err)
	}
	if n := len(pathsOf(calls, http.MethodPost)); n != 1 {
		t.Fatalf("membership requests = %d, want 1 once every id is proven: %+v", n, calls)
	}

	calls = nil
	if _, err = client.verifyUsersScope(t.Context(), "op", []string{"BAD/ID"}); !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal", err, calls)
	}
}

func TestUsersListFiltersAndRejectsForeignScopes(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`","page":2,"limit":5}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	ids := idsOf(t, result)
	if !ids[userInA] || !ids[userBoth] || !ids[selfID] || ids[userInB] || ids[userLeft] || strings.Contains(result, sensitive) {
		t.Fatalf("result = %s", result)
	}
	var list *call
	for i := range calls {
		if calls[i].path == "/api/v4/users" {
			list = &calls[i]
		}
	}
	if list == nil || list.query.Get("in_team") != teamA || list.query.Get("page") != "1" ||
		list.query.Get("per_page") != "5" {
		t.Fatalf("list request = %+v", list)
	}

	// By channel: the channel is confirmed live first.
	calls = nil
	if _, err = env.invoke(usersList.ID, "team", `{"channel_id":"`+chanA+`"}`); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if calls[0].path != "/api/v4/channels/"+chanA {
		t.Fatalf("calls = %+v, want the channel check first", calls)
	}

	// Channel of a foreign team: refused live, no listing.
	calls = nil
	if _, err = env.invoke(usersList.ID, "team", `{"channel_id":"`+chanB+`"}`); !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}

	// Local refusals without secret or I/O.
	calls = nil
	*env.reads = 0
	for _, c := range []struct{ connection, arguments string }{
		{"team", `{}`},
		{"team", `{"team_id":"` + teamA + `","channel_id":"` + chanA + `"}`},
		{"team", `{"team_id":"` + teamB + `"}`},
		{"channel", `{"channel_id":"` + chanC + `"}`},
		{"team", `{"team_id":"bad/id"}`},
	} {
		if _, err := env.invoke(usersList.ID, c.connection, c.arguments); err == nil {
			t.Errorf("%s %s was accepted", c.connection, c.arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestUsersSearchSendsATypedBodyAndFiltersHits(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(usersSearch.ID, "team", `{"term":" jane ","team_id":"`+teamA+`","limit":20}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	ids := idsOf(t, result)
	if !ids[userInA] || ids[userInB] || ids[userLeft] || strings.Contains(result, sensitive) {
		t.Fatalf("result = %s", result)
	}
	var body map[string]any
	for _, c := range calls {
		if c.path == "/api/v4/users/search" {
			_ = json.Unmarshal([]byte(c.body), &body)
		}
	}
	if body["term"] != "jane" || body["team_id"] != teamA || body["in_channel_id"] != nil || body["limit"] != float64(20) {
		t.Fatalf("body = %v", body)
	}

	calls = nil
	if _, err = env.invoke(usersSearch.ID, "team", `{"term":"x","channel_id":"`+chanB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}

	calls = nil
	*env.reads = 0
	for _, arguments := range []string{
		`{"term":"x"}`,
		`{"term":"x","team_id":"` + teamA + `","channel_id":"` + chanA + `"}`,
		`{"term":"x","team_id":"` + teamB + `"}`,
		`{"term":"   ","team_id":"` + teamA + `"}`,
		`{"term":"` + strings.Repeat("a", 65) + `","team_id":"` + teamA + `"}`,
		`{"term":"x","team_id":"` + teamA + `","limit":101}`,
	} {
		if _, err := env.invoke(usersSearch.ID, "team", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestUsersStatusChecksAllIDsBeforeTheStatusRequest(t *testing.T) {
	statuses := `[{"user_id":"` + userInA + `","status":"online","manual":true,"last_activity_at":1735689600000,` +
		`"dnd_end_time":7,"active_channel":"` + sensitive + `"},` +
		`{"user_id":"` + userInB + `","status":"away","manual":false,"last_activity_at":0}]`
	f := &usersFixture{t: t, statuses: statuses}
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(usersStatus.ID, "team", `{"user_ids":["`+userInA+`","`+selfID+`"]}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var out struct {
		Statuses []map[string]any `json:"statuses"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != 1 ||
		out.Statuses[0]["user_id"] != userInA || out.Statuses[0]["status"] != "online" ||
		out.Statuses[0]["manual"] != true || out.Statuses[0]["last_activity_at"] != "2025-01-01T00:00:00Z" ||
		len(out.Statuses[0]) != 4 || strings.Contains(result, sensitive) {
		t.Fatalf("result = %s, %v", result, err)
	}

	calls = nil
	_, err = env.invoke(usersStatus.ID, "team", `{"user_ids":["`+userInA+`","`+userInB+`"]}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	for _, c := range calls {
		if c.path == "/api/v4/users/status/ids" {
			t.Fatalf("the status request was sent: %+v", calls)
		}
	}

	calls = nil
	*env.reads = 0
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = `"` + userInA[:20] + strings.Repeat("a", 5) + string(rune('a'+i%26)) + `"`
	}
	for _, arguments := range []string{
		`{"user_ids":[]}`,
		`{"user_ids":["` + userInA + `","` + userInA + `"]}`,
		`{"user_ids":["bad/id"]}`,
		`{"user_ids":[` + strings.Join(tooMany, ",") + `]}`,
	} {
		if _, err := env.invoke(usersStatus.ID, "team", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}
