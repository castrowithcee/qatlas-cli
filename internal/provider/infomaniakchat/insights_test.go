package infomaniakchat

import (
	"net/http"
	"strings"
	"testing"
)

const insightText = "insight-message-text"

// insightsServer answers the insights path with answer (or the given status when status is not 200) and
// serves the channel and user reads the filters need.
func insightsServer(t *testing.T, status int, answer string) func(*http.Request) (*http.Response, error) {
	users := &usersFixture{t: t}
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v4/teams/"+teamA+"/top/"):
			return jsonResponse(status, answer), nil
		case r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels":
			return jsonResponse(200, searchChannels()), nil
		}
		return users.handle(r)
	}
}

func insightsTopCall(t *testing.T, connection, arguments, answer string) (string, []call, error) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, insightsServer(t, 200, answer))
	result, err := env.invoke(insightsTop.ID, connection, arguments)
	return result, calls, err
}

func channelItem(id string) string {
	return `{"id":"` + id + `","type":"O","display_name":"Name ` + id + `","name":"n","team_id":"` + teamA +
		`","message_count":7,"extra":"canary"}`
}

func threadItem(post, channel string, participants ...string) string {
	return `{"post":{"id":"` + post + `","channel_id":"` + channel + `","user_id":"` + userInA + `","reply_count":4,` +
		`"message":"` + insightText + `","extra":"canary"},"channel_id":"` + channel + `","channel_display_name":"` +
		insightText + `","participants":["` + strings.Join(participants, `","`) + `"],"user_information":[{"id":"x"}]}`
}

func TestInsightsTopReadsEachKindFromItsFixedPath(t *testing.T) {
	for _, tt := range []struct{ kind, segment, answer, want string }{
		{"channels", "channels", `{"has_next":true,"items":[` + channelItem(chanA) + `]}`, `"message_count":7`},
		{"threads", "threads", `{"has_next":true,"items":[` + threadItem("post0000000000000000001", chanA) + `]}`,
			`"reply_count":4`},
		{"reactions", "reactions", `{"has_next":true,"items":[{"emoji_name":"smile","count":9}]}`, `"emoji_name":"smile"`},
		{"team_members", "team_members", `{"has_next":true,"items":[{"id":"` + userInA +
			`","username":"jane.doe","message_count":5}]}`, `"username":"jane.doe"`},
	} {
		result, calls, err := insightsTopCall(t, "team",
			`{"team_id":"`+teamA+`","kind":"`+tt.kind+`","time_range":"28_day","page":3,"limit":10}`, tt.answer)
		var top *call
		for i := range calls {
			if strings.Contains(calls[i].path, "/top/") {
				top = &calls[i]
			}
		}
		if err != nil || top == nil || top.method != http.MethodGet || top.path != "/api/v4/teams/"+teamA+"/top/"+tt.segment ||
			top.query.Get("time_range") != "28_day" || top.query.Get("page") != "2" || top.query.Get("per_page") != "10" ||
			!strings.Contains(result, tt.want) || !strings.Contains(result, `"has_more":true`) ||
			!strings.Contains(result, `"count":1`) || !strings.Contains(result, `"page":3`) ||
			strings.Contains(result, "canary") {
			t.Fatalf("%s: result = %s, err = %v, calls = %+v", tt.kind, result, err, calls)
		}
	}
}

func TestInsightsTopDefaultsToTodayAndFirstPage(t *testing.T) {
	_, calls, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"reactions"}`, `{"items":[]}`)
	if err != nil || len(calls) != 1 || calls[0].query.Get("time_range") != "today" || calls[0].query.Get("page") != "0" ||
		calls[0].query.Get("per_page") != "50" {
		t.Fatalf("calls = %+v, err = %v", calls, err)
	}
}

func TestInsightsTopDropsChannelsAndThreadsOutsideTheReachableSet(t *testing.T) {
	channels := `{"items":[` + channelItem(chanA) + `,` + channelItem(chanB) + `,` + channelItem(chanDM) + `,` +
		channelItem(chanGone) + `,{"id":"bad/id"}]}`
	threads := `{"items":[` + threadItem("post0000000000000000001", chanA) + `,` +
		threadItem("post0000000000000000002", chanB) + `,` + threadItem("post0000000000000000003", chanDM) + `,` +
		threadItem("post0000000000000000004", "") + `,` + threadItem("post0000000000000000005", "bad/id") + `,` +
		threadItem("post0000000000000000006", chanC) + `]}`
	for _, tt := range []struct {
		kind, connection, answer string
		want, absent             []string
	}{
		{"channels", "team", channels, []string{chanA}, []string{chanB, chanDM, chanGone, "bad/id"}},
		{"channels", "channel", channels, []string{chanA}, []string{chanB, chanC}},
		{"threads", "team", threads, []string{"post0000000000000000001", "post0000000000000000006"},
			[]string{"0000000000000000002", "0000000000000000003", "0000000000000000004", "0000000000000000005"}},
		{"threads", "channel", threads, []string{"post0000000000000000001"}, []string{"0000000000000000006"}},
	} {
		result, _, err := insightsTopCall(t, tt.connection, `{"team_id":"`+teamA+`","kind":"`+tt.kind+`"}`, tt.answer)
		if err != nil {
			t.Fatalf("%s %s: %v", tt.kind, tt.connection, err)
		}
		for _, w := range tt.want {
			if !strings.Contains(result, w) {
				t.Errorf("%s %s: result = %s, want %s", tt.kind, tt.connection, result, w)
			}
		}
		for _, a := range tt.absent {
			if strings.Contains(result, a) {
				t.Errorf("%s %s: result = %s, want no %s", tt.kind, tt.connection, result, a)
			}
		}
		if strings.Contains(result, "canary") {
			t.Errorf("%s: result = %s, leaks a canary", tt.kind, result)
		}
	}
}

func TestInsightsTopDropsUnreachableMembersAndParticipants(t *testing.T) {
	members := `{"items":[{"id":"` + userInA + `","username":"a","message_count":1},{"id":"` + userInB +
		`","username":"b","message_count":2},{"id":"` + userLeft + `","username":"c","message_count":3},` +
		`{"id":"bad/id","username":"d"}]}`
	result, _, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"team_members"}`, members)
	if err != nil || !strings.Contains(result, userInA) || strings.Contains(result, userInB) ||
		strings.Contains(result, userLeft) || !strings.Contains(result, `"count":1`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	threads := `{"items":[` + threadItem("post0000000000000000001", chanA, userInA, userInB, "bad/id", userLeft) + `]}`
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"threads"}`, threads)
	if err != nil || !strings.Contains(result, `"participant_ids":["`+userInA+`"]`) || strings.Contains(result, userInB) ||
		strings.Contains(result, userLeft) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestInsightsTopBoundsEmojiNames(t *testing.T) {
	result, _, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"reactions"}`,
		`{"items":[{"emoji_name":"`+strings.Repeat("e", 5000)+`","count":1}]}`)
	if err != nil || strings.Contains(result, strings.Repeat("e", 1025)) {
		t.Fatalf("len(result) = %d, err = %v", len(result), err)
	}
}

func TestInsightsTopRefusesBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, insightsServer(t, 200, `{"items":[]}`))
	for _, arguments := range []string{
		`{"team_id":"` + teamA + `","kind":"inactive_channels"}`,
		`{"team_id":"` + teamA + `","kind":"channels/../../users"}`,
		`{"team_id":"` + teamA + `"}`,
		`{"team_id":"` + teamA + `","kind":"channels","time_range":"90_day"}`,
		`{"team_id":"` + teamA + `","kind":"channels","limit":201}`,
		`{"team_id":"` + teamB + `","kind":"channels"}`,
		`{"team_id":"bad/id","kind":"channels"}`,
	} {
		if _, err := env.invoke(insightsTop.ID, "team", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestInsightsTopMapsMissingAndUnsupportedWithoutProviderText(t *testing.T) {
	for status, class := range map[int]string{404: "not-found", 501: "provider-error"} {
		var calls []call
		env := newEnvironment(t, &calls, insightsServer(t, status, `{"message":"provider-secret-text"}`))
		_, err := env.invoke(insightsTop.ID, "team", `{"team_id":"`+teamA+`","kind":"channels"}`)
		if err == nil || string(classOf(err)) != class || strings.Contains(err.Error(), "provider-secret-text") {
			t.Fatalf("status %d: err = %v, class = %q", status, err, classOf(err))
		}
	}
}

func TestInsightsTopIsReadInTeamsGroup(t *testing.T) {
	d := withGroup(insightsTop)
	if d.Group != "teams" || d.RequiresToolAllowList || d.Risk.Confirmation != "none" {
		t.Fatalf("descriptor = %+v", d)
	}
}
