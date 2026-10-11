package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	flagPostA  = "flagpost0000000000000a01"
	flagPostA2 = "flagpost0000000000000a02"
	flagPostB  = "flagpost0000000000000b03"
	flagPostDM = "flagpost0000000000000d04"
)

func flaggedPost(id, channel string) string {
	return `"` + id + `":` + postJSONOf(id, channel, "", "user1", messageCanary, 1735689600000)
}

// flaggedSingle is the single PostList form of the flagged answer.
func flaggedSingle() string {
	return `{"order":["` + flagPostB + `","` + flagPostA + `","` + flagPostDM + `","` + flagPostA2 + `","ghost"],"posts":{` +
		flaggedPost(flagPostA, chanA) + `,` + flaggedPost(flagPostB, chanB) + `,` + flaggedPost(flagPostDM, chanDM) + `,` +
		flaggedPost(flagPostA2, chanC) + `}}`
}

// flaggedArray is the array-of-PostLists form of the same answer.
func flaggedArray() string {
	return `[{"order":["` + flagPostB + `","` + flagPostA + `"],"posts":{` + flaggedPost(flagPostA, chanA) + `,` +
		flaggedPost(flagPostB, chanB) + `}},{"order":["` + flagPostDM + `","` + flagPostA2 + `","` + flagPostA +
		`"],"posts":{` + flaggedPost(flagPostA, chanA) + `,` + flaggedPost(flagPostDM, chanDM) + `,` +
		flaggedPost(flagPostA2, chanC) + `}}]`
}

func flaggedServer(t *testing.T, answer string) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/posts/flagged":
			return jsonResponse(200, answer), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels":
			return jsonResponse(200, searchChannels()), nil
		case r.Method == http.MethodGet:
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestFlaggedListParsesBothFormsAndDropsPostsOutsideTheBinding(t *testing.T) {
	for name, answer := range map[string]string{"single": flaggedSingle(), "array": flaggedArray()} {
		for _, tt := range []struct {
			args, connection string
			want             []string
			query            string
		}{
			{`{"team_id":"` + teamA + `","page":2,"limit":3}`, "team", []string{flagPostA, flagPostA2}, "team_id"},
			{`{"channel_id":"` + chanA + `"}`, "team", []string{flagPostA}, "channel_id"},
		} {
			var calls []call
			env := newEnvironment(t, &calls, flaggedServer(t, answer))
			result, err := env.invoke(flaggedList.ID, tt.connection, tt.args)
			if err != nil {
				t.Fatalf("%s %s: invoke() = %v", name, tt.query, err)
			}
			var out FlaggedMessages
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != len(tt.want) {
				t.Fatalf("%s %s: result = %s, %v", name, tt.query, result, err)
			}
			for i, id := range tt.want {
				if out.Messages[i].ID != id {
					t.Fatalf("%s %s: result = %s, want %v", name, tt.query, result, tt.want)
				}
			}
			for _, leaked := range []string{chanB, flagPostB, flagPostDM, chanDM} {
				if strings.Contains(result, leaked) {
					t.Fatalf("%s %s: result leaked %q: %s", name, tt.query, leaked, result)
				}
			}
			last := calls[len(calls)-1]
			if last.method != http.MethodGet || last.path != "/api/v4/users/me/posts/flagged" ||
				last.query.Get(tt.query) == "" || last.body != "" {
				t.Fatalf("%s %s: calls = %+v", name, tt.query, calls)
			}
			if tt.query == "team_id" && (last.query.Get("page") != "1" || last.query.Get("per_page") != "3" ||
				last.query.Get("channel_id") != "" || !out.HasMore || out.Page != 2) {
				t.Fatalf("%s: query = %v, out = %+v", name, last.query, out)
			}
			if tt.query == "channel_id" && last.query.Get("team_id") != "" {
				t.Fatalf("%s: query = %v", name, last.query)
			}
		}
	}
}

func TestFlaggedListNeedsExactlyOneBoundScopeBeforeAnyIO(t *testing.T) {
	cases := []struct{ name, connection, args string }{
		{"neither", "team", `{}`},
		{"both", "team", `{"team_id":"` + teamA + `","channel_id":"` + chanA + `"}`},
		{"foreign team", "team", `{"team_id":"` + teamB + `"}`},
		{"malformed team", "team", `{"team_id":"../` + teamA + `"}`},
		{"outside allow-list", "channel", `{"channel_id":"` + chanC + `"}`},
		{"malformed channel", "team", `{"channel_id":"../` + chanA + `"}`},
	}
	for _, tt := range cases {
		var calls []call
		env := newEnvironment(t, &calls, flaggedServer(t, `{}`))
		_, err := env.invoke(flaggedList.ID, tt.connection, tt.args)
		if !isInvalidRequest(err) || *env.reads != 0 || len(calls) != 0 {
			t.Fatalf("%s: err = %v, reads = %d, calls = %+v, want a local invalid request", tt.name, err, *env.reads, calls)
		}
	}
}

func TestFlaggedListRefusesAForeignChannelLiveWithoutTheList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, flaggedServer(t, flaggedSingle()))
	_, err := env.invoke(flaggedList.ID, "team", `{"channel_id":"`+chanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/flagged") {
			t.Fatalf("calls = %+v, want no flagged read", calls)
		}
	}
	if strings.Contains(err.Error(), chanB) || strings.Contains(err.Error(), teamB) {
		t.Fatalf("error leaked a foreign target: %v", err)
	}
}

func flagServer(t *testing.T, mutate func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me":
			return jsonResponse(200, `{"id":"`+selfID+`"}`), nil
		case r.Method == http.MethodGet:
			return posts(r)
		case mutate != nil:
			return mutate(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestFlagMutationsSendOneFixedPreferenceOfTheOwnUser(t *testing.T) {
	for _, tool := range []struct {
		id, method, path string
		flagged          bool
	}{
		{messagesFlag.ID, http.MethodPut, "/api/v4/users/me/preferences", true},
		{messagesUnflag.ID, http.MethodPost, "/api/v4/users/me/preferences/delete", false},
	} {
		var calls []call
		env := newEnvironment(t, &calls, flagServer(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"status":"OK"}`), nil
		}))
		result, err := env.confirmed(tool.id, "editor", `{"post_id":"`+postInChanA+`"}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", tool.id, err)
		}
		var state FlagState
		if err := json.Unmarshal([]byte(result), &state); err != nil || state.PostID != postInChanA ||
			state.Flagged != tool.flagged || strings.Contains(result, "status") {
			t.Fatalf("%s: result = %s, %v", tool.id, result, err)
		}
		last := calls[len(calls)-1]
		want := `[{"user_id":"` + selfID + `","category":"flagged_post","name":"` + postInChanA + `","value":"true"}]`
		if last.method != tool.method || last.path != tool.path || last.body != want ||
			len(calls)-countMethod(calls, http.MethodGet) != 1 {
			t.Fatalf("%s: calls = %+v, want reads and one fixed %s %s with %s", tool.id, calls, tool.method, tool.path, want)
		}
	}
}

func TestFlagMutationsIgnoreAnyUserIDArgument(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, flagServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"OK"}`), nil
	}))
	if _, err := env.confirmed(messagesFlag.ID, "editor", `{"post_id":"`+postInChanA+`","user_id":"`+otherID+`"}`); err == nil {
		t.Fatal("want the schema to refuse an extra user_id")
	}
	if countMethod(calls, http.MethodPut) != 0 {
		t.Fatalf("calls = %+v, want no changing request", calls)
	}
}

func TestFlagMutationsRefuseForeignDeletedAndMalformedPostsBeforeTheChange(t *testing.T) {
	cases := []struct {
		name, post string
		allowList  bool
		local      bool
	}{
		{"foreign team", postInChanB, false, false},
		{"outside allow-list", postInChanC, true, false},
		{"deleted post", postDeleted, false, false},
		{"malformed post", "../" + chanA, false, true},
	}
	for _, id := range []string{messagesFlag.ID, messagesUnflag.ID} {
		for _, tt := range cases {
			var calls []call
			env := newEnvironment(t, &calls, flagServer(t, nil))
			connection := "editor"
			if tt.allowList {
				connection = "editorch"
			}
			_, err := env.confirmed(id, connection, `{"post_id":"`+tt.post+`"}`)
			if !isInvalidRequest(err) {
				t.Fatalf("%s/%s: err = %v, want an invalid request", id, tt.name, err)
			}
			if tt.local && (*env.reads != 0 || len(calls) != 0) {
				t.Fatalf("%s/%s: reads = %d, calls = %+v, want no secret access or provider I/O", id, tt.name, *env.reads, calls)
			}
			if len(calls) != countMethod(calls, http.MethodGet) {
				t.Fatalf("%s/%s: calls = %+v, want no changing request", id, tt.name, calls)
			}
			for _, leaked := range []string{messageCanary, chanB, teamB, postInChanB} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked %q: %v", leaked, err)
				}
			}
		}
	}
}

func TestFlagToolsRequireConfirmationAndUpdatePermission(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, flagServer(t, nil))
	for _, id := range []string{messagesFlag.ID, messagesUnflag.ID} {
		if _, err := env.invoke(id, "editor", `{"post_id":"`+postInChanA+`"}`); !isConfirmationRequired(err) {
			t.Fatalf("%s err = %v, want confirmation-required", id, err)
		}
		if _, err := env.confirmed(id, "team", `{"post_id":"`+postInChanA+`"}`); err == nil {
			t.Fatalf("%s: want a refusal without the update permission", id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestFlagMutationsUnclearResultIsNeverRetried(t *testing.T) {
	for _, tool := range []struct{ id, wantText string }{
		{messagesFlag.ID, "may have been flagged"}, {messagesUnflag.ID, "may have been unflagged"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
			"no status":  func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
		} {
			var calls []call
			env := newEnvironment(t, &calls, flagServer(t, fail))
			_, err := env.confirmed(tool.id, "editor", `{"post_id":"`+postInChanA+`"}`)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) ||
				strings.Contains(err.Error(), "may have been sent") {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if len(calls)-countMethod(calls, http.MethodGet) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}
