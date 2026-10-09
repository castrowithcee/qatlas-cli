package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const chanD = "chand000000000000000000d4"

// memberFixture is a fake instance: chanA and chanC belong to teamA, chanB to teamB, chanD is a direct
// channel. userInA is a member of teamA, userInB of teamB only.
type memberFixture struct {
	t       *testing.T
	users   usersFixture
	mutate  func(*http.Request) (*http.Response, error)
	members string
}

func (f *memberFixture) handle(r *http.Request) (*http.Response, error) {
	channels := map[string]string{chanA: channelWith(chanA, teamA, "O", ""), chanC: channelWith(chanC, teamA, "P", ""),
		chanB: channelWith(chanB, teamB, "O", ""), chanD: channelWith(chanD, "", "D", "")}
	if r.Method == http.MethodGet {
		for id, body := range channels {
			if r.URL.Path == "/api/v4/channels/"+id {
				return jsonResponse(200, body), nil
			}
		}
		if r.URL.Path == "/api/v4/channels/"+chanA+"/members" {
			return jsonResponse(200, f.members), nil
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v4/channels/") && r.Method != http.MethodGet {
		return f.mutate(r)
	}
	return f.users.handle(r)
}

func (f *memberFixture) env(calls *[]call) *environment {
	f.users.t = f.t
	return newEnvironment(f.t, calls, f.handle)
}

func memberFixtureWith(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *memberFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &memberFixture{t: t, mutate: mutate}
}

func addedAnswer(channelID string, users ...string) string {
	var out []string
	for _, id := range users {
		out = append(out, `{"channel_id":"`+channelID+`","user_id":"`+id+`","roles":"channel_user"}`)
	}
	return "[" + strings.Join(out, ",") + "]"
}

func TestMembersListOutputsOnlyTheThreeFieldsAndRefusesForeignChannels(t *testing.T) {
	f := memberFixtureWith(t, nil)
	f.members = `[{"channel_id":"` + chanA + `","user_id":"` + userInA + `","roles":"channel_user channel_admin",` +
		`"scheme_admin":true,"msg_count":3,"notify_props":{"desktop":"` + sensitive + `"}},` +
		`{"channel_id":"` + chanB + `","user_id":"` + userInB + `","roles":"channel_user"}]`
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(channelsMembersList.ID, "team", `{"channel_id":"`+chanA+`","page":2,"limit":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(result, sensitive) || strings.Contains(result, "msg_count") || strings.Contains(result, userInB) {
		t.Fatalf("result = %s, want only the three fields of members of this channel", result)
	}
	var page MembersPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 || page.Page != 2 || !page.HasMore ||
		page.Members[0] != (MemberEntry{UserID: userInA, Roles: "channel_user channel_admin", SchemeAdmin: true}) {
		t.Fatalf("page = %+v, %v", page, err)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != "/api/v4/channels/"+chanA+"/members" ||
		last.query.Get("page") != "1" || last.query.Get("per_page") != "1" {
		t.Fatalf("last call = %+v", last)
	}

	// A channel outside the allow-list is refused before the secret is read; a foreign team's channel and a
	// direct channel are refused by the live read, before the members are asked.
	calls, *env.reads = nil, 0
	if _, err := env.invoke(channelsMembersList.ID, "channel", `{"channel_id":"`+chanC+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
	for _, id := range []string{chanB, chanD} {
		calls = nil
		_, err := env.invoke(channelsMembersList.ID, "team", `{"channel_id":"`+id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) {
			t.Fatalf("%s: err = %v", id, err)
		}
		for _, c := range calls {
			if strings.HasSuffix(c.path, "/members") {
				t.Fatalf("%s: calls = %+v, want no members read", id, calls)
			}
		}
	}
	if _, err := env.invoke(channelsMembersList.ID, "team", `{"channel_id":"`+chanA+`","limit":201}`); err == nil {
		t.Fatal("limit above 200 was accepted")
	}
}

func TestMembersAddNeedsConfirmationAndSendsOnePostAfterTheReads(t *testing.T) {
	f := memberFixtureWith(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/channels/"+chanA+"/members" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(201, addedAnswer(chanA, userInA, selfID)), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"channel_id":"` + chanA + `","user_ids":["` + userInA + `","` + selfID + `","` + userInA + `"]}`
	if _, err := env.invoke(channelsMembersAdd.ID, "members", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(channelsMembersAdd.ID, "members", args)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var added MembersAdded
	if json.Unmarshal([]byte(result), &added) != nil || added.Count != 2 || added.UserIDs[0] != userInA ||
		added.UserIDs[1] != selfID {
		t.Fatalf("result = %s", result)
	}
	if countMethod(calls, http.MethodPost) != 2 || calls[len(calls)-1].path != "/api/v4/channels/"+chanA+"/members" {
		t.Fatalf("calls = %+v, want the team check and then exactly one add as the last request", calls)
	}
	var body map[string][]string
	if err := json.Unmarshal([]byte(calls[len(calls)-1].body), &body); err != nil || len(body) != 1 ||
		strings.Join(body["user_ids"], ",") != userInA+","+selfID {
		t.Fatalf("body = %s", calls[len(calls)-1].body)
	}
}

func TestMembersAddRefusesUsersOutsideTheChannelsTeamBeforeThePost(t *testing.T) {
	f := memberFixtureWith(t, nil)
	var calls []call
	env := f.env(&calls)
	// userInB belongs to teamB only; userLeft left teamA. Even when teamB is bound too, only the team of
	// the channel counts.
	for _, c := range []struct{ connection, arguments string }{
		{"members", `{"channel_id":"` + chanA + `","user_ids":["` + userInA + `","` + userInB + `"]}`},
		{"members", `{"channel_id":"` + chanA + `","user_ids":["` + userLeft + `"]}`},
		{"twoteams", `{"channel_id":"` + chanA + `","user_ids":["` + userInB + `"]}`},
	} {
		calls = nil
		_, err := env.confirmed(channelsMembersAdd.ID, c.connection, c.arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), userInB) || strings.Contains(err.Error(), userLeft) {
			t.Fatalf("%s: err = %v", c.arguments, err)
		}
		if countMethod(calls, http.MethodPost) != 1 || strings.HasSuffix(calls[len(calls)-1].path, "/members") {
			t.Fatalf("%s: calls = %+v, want the team check and no add", c.arguments, calls)
		}
	}
}

func TestMembersAddRefusesBadArgumentsAndForeignChannelsBeforeProviderIO(t *testing.T) {
	f := memberFixtureWith(t, nil)
	var calls []call
	env := f.env(&calls)
	many := make([]string, 21)
	for i := range many {
		many[i] = `"` + userInA[:len(userInA)-2] + string(rune('a'+i/10)) + string(rune('a'+i%10)) + `"`
	}
	for _, c := range []struct{ connection, arguments string }{
		{"members", `{"channel_id":"` + chanA + `","user_ids":[]}`},
		{"members", `{"channel_id":"` + chanA + `","user_ids":[` + strings.Join(many, ",") + `]}`},
		{"members", `{"channel_id":"` + chanA + `","user_ids":["../x"]}`},
		{"members", `{"channel_id":"` + chanA + `"}`},
		{"memberch", `{"channel_id":"` + chanC + `","user_ids":["` + userInA + `"]}`},
	} {
		if _, err := env.confirmed(channelsMembersAdd.ID, c.connection, c.arguments); err == nil {
			t.Fatalf("%s: want a refusal", c.arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
	for _, id := range []string{chanB, chanD} {
		calls = nil
		if _, err := env.confirmed(channelsMembersAdd.ID, "members",
			`{"channel_id":"`+id+`","user_ids":["`+userInA+`"]}`); !isInvalidRequest(err) {
			t.Fatalf("%s: err = %v", id, err)
		}
		if len(calls) != 1 || calls[0].method != http.MethodGet {
			t.Fatalf("%s: calls = %+v, want only the channel read", id, calls)
		}
	}
}

func TestMembersRemoveNeedsToolListAndConfirmationAndSendsOneDelete(t *testing.T) {
	f := memberFixtureWith(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/channels/"+chanA+"/members/"+userInA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"channel_id":"` + chanA + `","user_id":"` + userInA + `"}`
	if _, err := env.confirmed(channelsMembersRemove.ID, "memberno", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the tool in the tools list", err, calls)
	}
	if _, err := env.invoke(channelsMembersRemove.ID, "memberdel", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required", err, calls)
	}
	result, err := env.confirmed(channelsMembersRemove.ID, "memberdel", args)
	if err != nil || !strings.Contains(result, `"removed":true`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if countMethod(calls, http.MethodDelete) != 1 || len(calls) != 2 {
		t.Fatalf("calls = %+v, want one read and one DELETE", calls)
	}
	calls = nil
	for _, arguments := range []string{
		`{"channel_id":"` + chanC + `","user_id":"` + userInA + `"}`,
		`{"channel_id":"` + chanA + `","user_id":"../` + userInA + `"}`,
	} {
		if _, err := env.confirmed(channelsMembersRemove.ID, "memberdel", arguments); !isInvalidRequest(err) {
			t.Fatalf("%s: err = %v", arguments, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want local refusals", calls)
	}
}

func TestMembersRolesAllowsOnlyTheTwoChannelRoles(t *testing.T) {
	f := memberFixtureWith(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/channels/"+chanA+"/members/"+userInA+"/roles" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	for _, roles := range []string{"system_admin", "channel_admin", "channel_user channel_admin system_admin", ""} {
		args, _ := json.Marshal(map[string]string{"channel_id": chanA, "user_id": userInA, "roles": roles})
		if _, err := env.confirmed(channelsMembersRoles.ID, "memberroles", string(args)); err == nil {
			t.Fatalf("roles %q were accepted", roles)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
	args := `{"channel_id":"` + chanA + `","user_id":"` + userInA + `","roles":"channel_user channel_admin"}`
	if _, err := env.confirmed(channelsMembersRoles.ID, "memberno", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, want a refusal without the tool in the tools list", err)
	}
	if _, err := env.invoke(channelsMembersRoles.ID, "memberroles", args); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	if _, err := env.confirmed(channelsMembersRoles.ID, "memberroles", args); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var body map[string]string
	last := calls[len(calls)-1]
	if countMethod(calls, http.MethodPut) != 1 || json.Unmarshal([]byte(last.body), &body) != nil || len(body) != 1 ||
		body["roles"] != "channel_user channel_admin" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestMembersChangesAreNeverRetriedAfterAnUnclearResult(t *testing.T) {
	for _, c := range []struct {
		operation, connection, arguments, method, hint string
	}{
		{channelsMembersAdd.ID, "members", `{"channel_id":"` + chanA + `","user_ids":["` + userInA + `"]}`,
			http.MethodPost, "may have been added"},
		{channelsMembersRemove.ID, "memberdel", `{"channel_id":"` + chanA + `","user_id":"` + userInA + `"}`,
			http.MethodDelete, "may have been removed"},
		{channelsMembersRoles.ID, "memberroles",
			`{"channel_id":"` + chanA + `","user_id":"` + userInA + `","roles":"channel_user"}`,
			http.MethodPut, "may have been changed"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
		} {
			if c.method != http.MethodPost && name == "unreadable" {
				continue
			}
			var calls []call
			env := memberFixtureWith(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, c.connection, c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", c.operation, name, err)
			}
			if countMethod(calls, c.method) != map[bool]int{true: 2, false: 1}[c.method == http.MethodPost] {
				t.Fatalf("%s %s: calls = %+v, want exactly one change request", c.operation, name, calls)
			}
			if last := calls[len(calls)-1]; last.method != c.method || strings.HasSuffix(last.path, "/members/ids") {
				t.Fatalf("%s %s: calls = %+v", c.operation, name, calls)
			}
		}
	}
}

func TestMembersAddRejectsAnAnswerThatDoesNotConfirmEveryUser(t *testing.T) {
	f := memberFixtureWith(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(201, addedAnswer(chanA, userInA)), nil
	})
	var calls []call
	env := f.env(&calls)
	_, err := env.confirmed(channelsMembersAdd.ID, "members",
		`{"channel_id":"`+chanA+`","user_ids":["`+userInA+`","`+selfID+`"]}`)
	if err == nil || classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been added") {
		t.Fatalf("err = %v (%s)", err, classOf(err))
	}
}
