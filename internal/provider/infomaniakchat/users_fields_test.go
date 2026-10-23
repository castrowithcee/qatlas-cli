package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// fullUserJSON is a user as the kChat User, UserNotifyProps, and Timezone schemas document it. Numbers and
// flags come in the forms kChat sends, including text where the schema says number or boolean.
func fullUserJSON(id, username string, deleteAt int64) string {
	return `{"id":"` + id + `","create_at":1735689600000,"update_at":"1735689700000","delete_at":` +
		strconv.FormatInt(deleteAt, 10) + `,"username":"` + username + `",` +
		`"first_name":"First","last_name":"Last","nickname":"Nick","position":"Dev",` +
		`"email":"` + username + `@example.invalid","email_verified":true,"auth_service":"gitlab",` +
		`"roles":"system_user","locale":"de","is_bot":false,` +
		`"notify_props":{"email":"true","push":"mention","desktop":"all","desktop_sound":"true",` +
		`"mention_keys":"jane,@jane","channel":"true","first_name":"false"},` +
		`"props":{"k":"v\u0007x","n":3},"last_password_update":1,"last_picture_update":1735689800000,` +
		`"failed_attempts":3,"mfa_active":true,` +
		`"timezone":{"useAutomaticTimezone":"true","manualTimezone":"Europe/Berlin","automaticTimezone":"Europe/Zurich"},` +
		`"terms_of_service_id":"tos1","terms_of_service_create_at":1735689900000}`
}

func fullUserWant(id, username string) string {
	return `{"id":"` + id + `","username":"` + username + `","first_name":"First","last_name":"Last",` +
		`"nickname":"Nick","position":"Dev","email":"` + username + `@example.invalid","is_bot":false,` +
		`"deleted":false,"create_at":"2025-01-01T00:00:00Z","update_at":"2025-01-01T00:01:40Z",` +
		`"last_picture_update":"2025-01-01T00:03:20Z","terms_of_service_create_at":"2025-01-01T00:05:00Z",` +
		`"email_verified":true,"auth_service":"gitlab","roles":"system_user","locale":"de","terms_of_service_id":"tos1",` +
		`"notify_props":{"channel":"true","desktop":"all","desktop_sound":"true","email":"true","first_name":"false",` +
		`"mention_keys":"jane,@jane","push":"mention"},"props":{"k":"vx","n":3},` +
		`"timezone":{"use_automatic_timezone":true,"manual_timezone":"Europe/Berlin","automatic_timezone":"Europe/Zurich"}}`
}

func fullFixture(t *testing.T) *usersFixture {
	return &usersFixture{t: t, render: func(id, username string) string { return fullUserJSON(id, username, 0) }}
}

// userEntries returns the raw user entries of a list or search answer by id.
func userEntries(t *testing.T, result string) map[string]json.RawMessage {
	t.Helper()
	var out struct {
		Users []json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	byID := map[string]json.RawMessage{}
	for _, raw := range out.Users {
		var head struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &head)
		byID[head.ID] = raw
	}
	return byID
}

func TestUserFieldsArriveInGetListAndSearchWithoutAccountSecurity(t *testing.T) {
	var calls []call
	env := fullFixture(t).env(&calls)
	got, err := env.invoke(usersGet.ID, "team", `{"user_id":"`+userInA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, got, fullUserWant(userInA, nameInA))
	got, err = env.invoke(usersGet.ID, "team", `{"username":"`+nameInA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, got, fullUserWant(userInA, nameInA))

	listed, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	searched, err := env.invoke(usersSearch.ID, "team", `{"term":"jane","team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{listed, searched} {
		entries := userEntries(t, result)
		if len(entries) != 3 || entries[userInB] != nil {
			t.Fatalf("entries = %v, want the three reachable users only", entries)
		}
		equalJSON(t, string(entries[userInA]), fullUserWant(userInA, nameInA))
	}
	for _, result := range []string{got, listed, searched} {
		for _, key := range []string{"last_password_update", "failed_attempts", "mfa_active"} {
			if strings.Contains(result, key) {
				t.Errorf("result holds %s: %s", key, result)
			}
		}
	}
	for _, d := range []string{usersGet.ID, usersList.ID, usersSearch.ID} {
		descriptor := registry(t).Provider(Provider)
		for _, item := range descriptor {
			if item.ID != d {
				continue
			}
			for _, key := range []string{"last_password_update", "failed_attempts", "mfa_active", "failed_attempts"} {
				if strings.Contains(string(item.OutputSchema), key) {
					t.Errorf("%s schema offers %s", d, key)
				}
				for _, f := range item.Fields {
					if f.Name == key {
						t.Errorf("%s fields offer %s", d, key)
					}
				}
			}
		}
	}
}

func TestUsersWithoutNewFieldsKeepTheFormerOutputByteForByte(t *testing.T) {
	f := &usersFixture{t: t}
	var calls []call
	env := f.env(&calls)
	want := `{"deleted":false,"email":"jane.doe@example.invalid","first_name":"First","id":"` + userInA +
		`","is_bot":false,"last_name":"Last","nickname":"Nick","position":"Dev","username":"jane.doe"}`
	got, err := env.invoke(usersGet.ID, "team", `{"user_id":"`+userInA+`"}`)
	if err != nil || got != want {
		t.Fatalf("get = %s, %v\nwant %s", got, err, want)
	}
	listed, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	searched, err := env.invoke(usersSearch.ID, "team", `{"term":"jane","team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{listed, searched} {
		if got := string(userEntries(t, result)[userInA]); got != want {
			t.Errorf("entry = %s\nwant %s", got, want)
		}
	}
}

func TestUserFieldsToleratePlainTypesAndBoundProps(t *testing.T) {
	huge := `{"k":"` + strings.Repeat("x", 2000) + `"}`
	f := &usersFixture{t: t, render: func(id, username string) string {
		return `{"id":"` + id + `","username":"` + username + `","is_bot":false,"delete_at":"0","create_at":"1735689600000",` +
			`"roles":5,"locale":["de"],"email_verified":"yes","notify_props":"none","timezone":{"useAutomaticTimezone":"maybe"},` +
			`"props":` + huge + `}`
	}}
	var calls []call
	env := f.env(&calls)
	got, err := env.invoke(usersGet.ID, "team", `{"user_id":"`+userInA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, got, `{"id":"`+userInA+`","username":"`+nameInA+`","is_bot":false,"deleted":false,`+
		`"create_at":"2025-01-01T00:00:00Z","omitted":["props"]}`)
}

func TestInactiveTeamUserIsListedAsDeleted(t *testing.T) {
	f := &usersFixture{t: t, render: func(id, username string) string {
		if id == userInA {
			return fullUserJSON(id, username, 1735689600000)
		}
		return userJSONOf(id, username)
	}}
	var calls []call
	env := f.env(&calls)
	listed, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`","inactive":true}`)
	if err != nil {
		t.Fatal(err)
	}
	entry := string(userEntries(t, listed)[userInA])
	if !strings.Contains(entry, `"deleted":true`) || !strings.Contains(entry, `"delete_at":"2025-01-01T00:00:00Z"`) ||
		userEntries(t, listed)[userInB] != nil {
		t.Fatalf("entry = %s, list = %s", entry, listed)
	}
}

func lastRequestTo(t *testing.T, calls []call, path string) call {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].path == path {
			return calls[i]
		}
	}
	t.Fatalf("no request to %s in %+v", path, calls)
	return call{}
}

func TestUsersListSendsTheNewFilters(t *testing.T) {
	var calls []call
	env := (&usersFixture{t: t}).env(&calls)
	if _, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`","active":true,"sort":"create_at",`+
		`"team_roles":["team_user","team_admin"]}`); err != nil {
		t.Fatal(err)
	}
	q := lastRequestTo(t, calls, "/api/v4/users").query
	if q.Get("active") != "true" || q.Get("inactive") != "" || q.Get("sort") != "create_at" ||
		q.Get("team_roles") != "team_user,team_admin" || q.Get("in_team") != teamA || q.Get("not_in_channel") != "" {
		t.Fatalf("query = %v", q)
	}

	calls = nil
	if _, err := env.invoke(usersList.ID, "team", `{"channel_id":"`+chanA+`","inactive":true,"sort":"status",`+
		`"channel_roles":["channel_guest"]}`); err != nil {
		t.Fatal(err)
	}
	q = lastRequestTo(t, calls, "/api/v4/users").query
	if q.Get("inactive") != "true" || q.Get("active") != "" || q.Get("sort") != "status" ||
		q.Get("channel_roles") != "channel_guest" || q.Get("in_channel") != chanA {
		t.Fatalf("query = %v", q)
	}

	calls = nil
	if _, err := env.invoke(usersList.ID, "team", `{"team_id":"`+teamA+`","sort":"last_activity_at",`+
		`"not_in_channel_id":"`+chanA+`"}`); err != nil {
		t.Fatal(err)
	}
	if len(pathsOf(calls, http.MethodGet)) != 3 || calls[0].path != "/api/v4/channels/"+chanA ||
		calls[1].path != "/api/v4/users" {
		t.Fatalf("calls = %+v, want one channel check, then the listing and its usual scope check", calls)
	}
	q = lastRequestTo(t, calls, "/api/v4/users").query
	if q.Get("in_team") != teamA || q.Get("not_in_channel") != chanA || q.Get("sort") != "last_activity_at" ||
		q.Get("in_channel") != "" {
		t.Fatalf("query = %v", q)
	}
}

func TestUsersSearchSendsTheNewFilters(t *testing.T) {
	var calls []call
	env := (&usersFixture{t: t}).env(&calls)
	if _, err := env.invoke(usersSearch.ID, "team", `{"term":"j","team_id":"`+teamA+`","allow_inactive":true,`+
		`"not_in_channel_id":"`+chanA+`"}`); err != nil {
		t.Fatal(err)
	}
	if calls[0].path != "/api/v4/channels/"+chanA {
		t.Fatalf("calls = %+v, want the channel check first", calls)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(lastRequestTo(t, calls, "/api/v4/users/search").body), &body)
	if body["allow_inactive"] != true || body["not_in_channel_id"] != chanA || body["team_id"] != teamA ||
		body["in_channel_id"] != nil {
		t.Fatalf("body = %v", body)
	}
	calls = nil
	if _, err := env.invoke(usersSearch.ID, "team", `{"term":"j","team_id":"`+teamA+`"}`); err != nil {
		t.Fatal(err)
	}
	body = nil
	_ = json.Unmarshal([]byte(lastRequestTo(t, calls, "/api/v4/users/search").body), &body)
	if _, ok := body["allow_inactive"]; ok || body["not_in_channel_id"] != nil {
		t.Fatalf("body = %v, want no new members by default", body)
	}
}

func TestForbiddenFilterCombinationsAreRefusedBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := (&usersFixture{t: t}).env(&calls)
	team := `"team_id":"` + teamA + `"`
	channel := `"channel_id":"` + chanA + `"`
	for _, c := range []struct{ tool, connection, arguments string }{
		{usersList.ID, "team", `{` + team + `,"active":true,"inactive":true}`},
		{usersList.ID, "team", `{` + team + `,"sort":"status"}`},
		{usersList.ID, "team", `{` + channel + `,"sort":"create_at"}`},
		{usersList.ID, "team", `{` + channel + `,"sort":"last_activity_at"}`},
		{usersList.ID, "team", `{` + channel + `,"team_roles":["team_user"]}`},
		{usersList.ID, "team", `{` + team + `,"channel_roles":["channel_user"]}`},
		{usersList.ID, "team", `{` + channel + `,"not_in_channel_id":"` + chanA + `"}`},
		{usersList.ID, "team", `{` + team + `,"team_roles":[]}`},
		{usersList.ID, "team", `{` + team + `,"team_roles":["team_user","team_user"]}`},
		{usersList.ID, "team", `{` + team + `,"team_roles":["system_admin"]}`},
		{usersList.ID, "team", `{` + team + `,"team_roles":["team_user","team_admin","team_guest","team_user"]}`},
		{usersList.ID, "team", `{` + channel + `,"channel_roles":["team_user"]}`},
		{usersList.ID, "team", `{` + team + `,"sort":"username"}`},
		{usersList.ID, "team", `{` + team + `,"role":"system_admin"}`},
		{usersList.ID, "team", `{` + team + `,"roles":"system_admin"}`},
		{usersList.ID, "team", `{` + team + `,"in_group":"` + chanA + `"}`},
		{usersList.ID, "team", `{` + team + `,"not_in_team":"` + teamB + `"}`},
		{usersList.ID, "team", `{` + team + `,"without_team":true}`},
		{usersList.ID, "team", `{` + team + `,"group_constrained":true}`},
		{usersSearch.ID, "team", `{"term":"x","not_in_channel_id":"` + chanA + `"}`},
		{usersSearch.ID, "team", `{"term":"x",` + channel + `,"not_in_channel_id":"` + chanA + `"}`},
		{usersSearch.ID, "team", `{"term":"x",` + team + `,` + channel + `,"not_in_channel_id":"` + chanA + `"}`},
		{usersSearch.ID, "team", `{"term":"x",` + team + `,"without_team":true}`},
		{usersSearch.ID, "team", `{"term":"x",` + team + `,"not_in_team_id":"` + teamB + `"}`},
		{usersList.ID, "channel", `{` + team + `,"not_in_channel_id":"` + chanC + `"}`},
		{usersSearch.ID, "channel", `{"term":"x",` + team + `,"not_in_channel_id":"` + chanC + `"}`},
		{usersList.ID, "team", `{` + team + `,"not_in_channel_id":"bad/id"}`},
	} {
		if _, err := env.invoke(c.tool, c.connection, c.arguments); err == nil {
			t.Errorf("%s %s was accepted", c.tool, c.arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestNotInChannelMustBelongToTheTeamAndBeBound(t *testing.T) {
	var calls []call
	env := (&usersFixture{t: t}).env(&calls)
	for _, tool := range []struct{ id, arguments string }{
		{usersList.ID, `{"team_id":"` + teamA + `","not_in_channel_id":"` + chanB + `"}`},
		{usersSearch.ID, `{"term":"x","team_id":"` + teamA + `","not_in_channel_id":"` + chanB + `"}`},
	} {
		calls = nil
		// chanB belongs to teamB, which "team" does not bind: refused live after one channel read.
		if _, err := env.invoke(tool.id, "team", tool.arguments); !isInvalidRequest(err) ||
			len(calls) != 1 || calls[0].path != "/api/v4/channels/"+chanB {
			t.Errorf("%s: err = %v, calls = %+v", tool.id, err, calls)
		}
		// Both teams bound: chanB is bound but not a channel of team_id.
		calls = nil
		if _, err := env.invoke(tool.id, "twoteams", tool.arguments); !isInvalidRequest(err) ||
			len(calls) != 1 || calls[0].path != "/api/v4/channels/"+chanB {
			t.Errorf("%s twoteams: err = %v, calls = %+v", tool.id, err, calls)
		}
	}
}

func TestEveryUserAndGroupMemberIsInSchemaAndFields(t *testing.T) {
	var calls []call
	fixture := fullFixture(t)
	handle := fixture.handle
	group := `{"id":"` + groupID + `","name":"devs","display_name":"Developers","description":"d","source":"custom",` +
		`"remote_id":"r\u0001emote","create_at":1735689600000,"update_at":"1735689700000","delete_at":0,` +
		`"has_syncables":true,"member_count":3,"allow_reference":true}`
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/groups") {
			return jsonResponse(200, "["+group+"]"), nil
		}
		return handle(r)
	})
	for _, tool := range []struct{ id, args, member string }{
		{usersGet.ID, `{"user_id":"` + userInA + `"}`, ""},
		{usersList.ID, `{"team_id":"` + teamA + `"}`, "users"},
		{usersSearch.ID, `{"term":"j","team_id":"` + teamA + `"}`, "users"},
		{usergroupsList.ID, `{"team_id":"` + teamA + `"}`, "groups"},
	} {
		result, err := env.invoke(tool.id, "team", tool.args)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		var entry map[string]any
		if tool.member == "" {
			_ = json.Unmarshal([]byte(result), &entry)
		} else {
			var out map[string][]map[string]any
			_ = json.Unmarshal([]byte(result), &out)
			entry = out[tool.member][0]
		}
		for _, d := range registry(t).Provider(Provider) {
			if d.ID != tool.id {
				continue
			}
			var schema map[string]any
			_ = json.Unmarshal(d.OutputSchema, &schema)
			if tool.member != "" {
				schema = schema["properties"].(map[string]any)[tool.member].(map[string]any)["items"].(map[string]any)
			}
			fields := map[string]bool{}
			for _, f := range d.Fields {
				fields[f.Name] = true
			}
			for key := range entry {
				if !fields[key] {
					t.Errorf("%s: member %s is not in Fields", tool.id, key)
				}
			}
			for key := range schema["properties"].(map[string]any) {
				if !fields[key] {
					t.Errorf("%s: schema member %s is not in Fields", tool.id, key)
				}
			}
			checkSchema(t, entry, schema, "")
		}
		if tool.id == usergroupsList.ID {
			equalJSON(t, result, `{"groups":[{"id":"`+groupID+`","name":"devs","display_name":"Developers",`+
				`"description":"d","source":"custom","member_count":3,"allow_reference":true,"remote_id":"remote",`+
				`"create_at":"2025-01-01T00:00:00Z","update_at":"2025-01-01T00:01:40Z","has_syncables":true}],`+
				`"page":1,"has_more":false,"count":1,"team_id":"`+teamA+`"}`)
		}
	}
}

func TestUserGroupsWithoutNewFieldsKeepTheFormerOutputAndSendTheFilter(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, groupsHandler(t, `[`+groupObject+`]`))
	got, err := env.invoke(usergroupsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	want := `{"count":1,"groups":[{"allow_reference":true,"description":"d","display_name":"Developers","id":"` +
		groupID + `","member_count":3,"name":"devs","source":"custom"}],"has_more":false,"page":1,"team_id":"` + teamA + `"}`
	if err != nil || got != want {
		t.Fatalf("got = %s, %v\nwant %s", got, err, want)
	}
	if q := lastRequestTo(t, calls, "/api/v4/teams/"+teamA+"/groups").query; q.Get("filter_allow_reference") != "" {
		t.Fatalf("query = %v", q)
	}
	calls = nil
	for _, args := range []string{`{"team_id":"` + teamA + `","filter_allow_reference":true}`,
		`{"channel_id":"` + chanA + `","filter_allow_reference":true}`} {
		calls = nil
		if _, err := env.invoke(usergroupsList.ID, "team", args); err != nil {
			t.Fatal(err)
		}
		if q := calls[len(calls)-1].query; q.Get("filter_allow_reference") != "true" {
			t.Fatalf("query = %v", q)
		}
	}
}
