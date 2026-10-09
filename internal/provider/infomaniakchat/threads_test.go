package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	threadUser1 = "user1"
	threadUser2 = "user2"
)

func threadJSONOf(id, channelID string, post string) string {
	if post == "" {
		post = postJSONOf(id, channelID, "", "user1", messageCanary, 1735689600000)
	}
	return `{"id":"` + id + `","reply_count":3,"last_reply_at":1735693200000,"last_viewed_at":1735696800000,` +
		`"participants":[{"id":"` + threadUser1 + `","username":"leak-name","email":"leak@example.com"},` +
		`{"id":"../bad"},{"id":"` + threadUser2 + `"}],"unread_replies":1,"post":` + post + `}`
}

// threadServer answers the binding reads of postServer, the channel list of teamA, and the thread endpoints;
// mutate handles PUT and DELETE.
func threadServer(t *testing.T, list string, mutate func(*http.Request) (*http.Response, error),
) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels":
			return jsonResponse(200, searchChannels()), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/threads":
			return jsonResponse(200, list), nil
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v4/users/me/teams/"+teamA+"/threads/"):
			return jsonResponse(200, threadJSONOf(strings.TrimPrefix(r.URL.Path,
				"/api/v4/users/me/teams/"+teamA+"/threads/"), chanA, "")), nil
		case (r.Method == http.MethodPut || r.Method == http.MethodDelete) && mutate != nil:
			return mutate(r)
		case r.Method == http.MethodGet:
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func okStatus(*http.Request) (*http.Response, error) {
	return jsonResponse(200, `{"status":"OK"}`), nil
}

func TestThreadsListKeepsReachableRootsAndReducesParticipantsToIDs(t *testing.T) {
	foreign := threadJSONOf(postInChanB, chanB, "")
	dm := threadJSONOf("thread0000000000000000dm1", chanDM, "")
	private := threadJSONOf("thread0000000000000000pp1", chanP, "")
	gone := threadJSONOf("thread0000000000000000gn1", chanGone, "")
	wrongRoot := strings.Replace(threadJSONOf(postInChanC, chanC, ""), `"post":{"id":"`+postInChanC,
		`"post":{"id":"other0000000000000000000x`, 1)
	deleted := threadJSONOf(postDeleted, chanA, strings.Replace(
		postJSONOf(postDeleted, chanA, "", "user1", messageCanary, 1), `"edit_at":0`, `"edit_at":0,"delete_at":9`, 1))
	list := `{"threads":[` + strings.Join([]string{foreign, dm, gone, wrongRoot, deleted,
		threadJSONOf(postInChanA, chanA, ""), private}, ",") + `],"total":7}`
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, list, nil))
	result, err := env.invoke(threadsList.ID, "team", `{"team_id":"`+teamA+`","page":2,"limit":7}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ThreadsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.TeamID != teamA || page.Page != 2 ||
		page.Count != 2 || len(page.Threads) != 2 || !page.HasMore {
		t.Fatalf("result = %s, %v", result, err)
	}
	got := page.Threads[0]
	if got.ID != postInChanA || got.ChannelID != chanA || got.ReplyCount != 3 || got.Root.ID != postInChanA ||
		got.Root.Message != messageCanary || got.Root.ChannelID != chanA ||
		got.LastReplyAt != "2025-01-01T01:00:00Z" || got.LastViewedAt != "2025-01-01T02:00:00Z" ||
		strings.Join(got.Participants, ",") != threadUser1+","+threadUser2 || page.Threads[1].ChannelID != chanP {
		t.Fatalf("thread = %+v", got)
	}
	for _, leaked := range []string{"leak-name", "leak@example.com", chanB, postInChanB, chanDM, chanGone} {
		if strings.Contains(result, leaked) {
			t.Fatalf("result leaked %q: %s", leaked, result)
		}
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != "/api/v4/users/me/teams/"+teamA+"/threads" ||
		last.query.Get("page") != "1" || last.query.Get("pageSize") != "7" || len(last.query) != 2 ||
		last.body != "" {
		t.Fatalf("calls = %+v, want one fixed GET with page and pageSize only", calls)
	}
	for _, c := range calls {
		if c.query.Has("deleted") || c.query.Has("extended") {
			t.Fatalf("call %+v sets deleted or extended", c)
		}
	}
}

func TestThreadsListWithAllowListDropsUnlistedChannels(t *testing.T) {
	list := `{"threads":[` + threadJSONOf(postInChanC, chanC, "") + `,` + threadJSONOf(postInChanA, chanA, "") + `]}`
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, list, nil))
	result, err := env.invoke(threadsList.ID, "channel", `{"team_id":"`+teamA+`"}`)
	var page ThreadsPage
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 1 || page.Threads[0].ID != postInChanA ||
		page.HasMore {
		t.Fatalf("result = %s, %v", result, err)
	}
	if strings.Contains(result, chanC) {
		t.Fatalf("result = %s, want no channel outside the allow-list", result)
	}
}

func TestThreadsListRefusesBadArgumentsBeforeSecretsAndIO(t *testing.T) {
	for name, arguments := range map[string]string{
		"foreign team":   `{"team_id":"` + teamB + `"}`,
		"malformed team": `{"team_id":"../` + teamA + `"}`,
		"limit":          `{"team_id":"` + teamA + `","limit":101}`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, threadServer(t, `{}`, nil))
		_, err := env.invoke(threadsList.ID, "team", arguments)
		if err == nil || (name != "limit" && !isInvalidRequest(err)) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a refusal without secret access or I/O",
				name, err, calls, *env.reads)
		}
		if strings.Contains(err.Error(), teamB) {
			t.Fatalf("%s: error leaked the foreign team: %v", name, err)
		}
	}
}

func TestThreadsGetBindsTheRootPostToTheTeam(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, `{}`, nil))
	result, err := env.invoke(threadsGet.ID, "team", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+`"}`)
	var entry ThreadEntry
	if err != nil || json.Unmarshal([]byte(result), &entry) != nil || entry.ID != postInChanA ||
		entry.Root.Message != messageCanary || len(entry.Participants) != 2 || entry.ReplyCount != 3 {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != "/api/v4/users/me/teams/"+teamA+"/threads/"+postInChanA ||
		countMethod(calls, http.MethodGet) != len(calls) {
		t.Fatalf("calls = %+v, want only reads with the fixed thread read last", calls)
	}
}

func TestThreadsGetRefusesADifferentThreadAnswer(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/v4/users/me/teams/"+teamA+"/threads/") {
			return jsonResponse(200, threadJSONOf(postInChanC, chanC, "")), nil
		}
		return threadServer(t, `{}`, nil)(r)
	})
	_, err := env.invoke(threadsGet.ID, "team", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+`"}`)
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("err = %v, want an invalid response without content", err)
	}
}

// Foreign, deleted, malformed, reply, and wrong-team threads are refused by all three thread tools before
// the thread endpoint is touched or anything changes.
func TestThreadToolsRefuseForeignThreadsBeforeTheThreadEndpoint(t *testing.T) {
	reply := "reply0000000000000000r0r"
	cases := []struct {
		name, connection, team, thread string
		local                          bool
	}{
		{"foreign team post", "editor", teamA, postInChanB, false},
		{"outside allow-list", "editorch", teamA, postInChanC, false},
		{"deleted post", "editor", teamA, postDeleted, false},
		{"malformed thread", "editor", teamA, "../" + postInChanA, true},
		{"foreign team argument", "editor", teamB, postInChanA, true},
		{"thread of another bound team", "editor2", teamA, postInChanB, false},
		{"reply is no thread root", "editor", teamA, reply, false},
	}
	for _, id := range []string{threadsGet.ID, threadsFollow.ID, threadsUnfollow.ID} {
		for _, tt := range cases {
			t.Run(id+"/"+tt.name, func(t *testing.T) {
				var calls []call
				base := threadServer(t, `{}`, nil)
				env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/v4/posts/"+reply {
						return jsonResponse(200, postJSONOf(reply, chanA, postInChanA, "user1", messageCanary, 1)), nil
					}
					return base(r)
				})
				arguments := `{"team_id":"` + tt.team + `","thread_id":"` + tt.thread + `"}`
				var err error
				if id == threadsGet.ID {
					_, err = env.invoke(id, tt.connection, arguments)
				} else {
					_, err = env.confirmed(id, tt.connection, arguments)
				}
				if !isInvalidRequest(err) {
					t.Fatalf("err = %v, want an invalid request", err)
				}
				if tt.local && (*env.reads != 0 || len(calls) != 0) {
					t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
				}
				for _, c := range calls {
					if strings.Contains(c.path, "/threads/") || c.method == http.MethodPut || c.method == http.MethodDelete {
						t.Fatalf("calls = %+v, want no thread endpoint request", calls)
					}
				}
				for _, leaked := range []string{messageCanary, chanB, teamB, postInChanB} {
					if strings.Contains(err.Error(), leaked) {
						t.Fatalf("error leaked %q: %v", leaked, err)
					}
				}
			})
		}
	}
}

func TestThreadFollowingSendsOneFixedBodylessRequestAfterTheBindingReads(t *testing.T) {
	for _, tool := range []struct {
		id, method string
		following  bool
	}{{threadsFollow.ID, http.MethodPut, true}, {threadsUnfollow.ID, http.MethodDelete, false}} {
		var calls []call
		env := newEnvironment(t, &calls, threadServer(t, `{}`, okStatus))
		result, err := env.confirmed(tool.id, "editor", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+`"}`)
		var state ThreadFollowing
		if err != nil || json.Unmarshal([]byte(result), &state) != nil || state.TeamID != teamA ||
			state.ThreadID != postInChanA || state.Following != tool.following || strings.Contains(result, "status") {
			t.Fatalf("%s: result = %s, %v", tool.id, result, err)
		}
		last := calls[len(calls)-1]
		if countMethod(calls, http.MethodPut)+countMethod(calls, http.MethodDelete) != 1 || last.method != tool.method ||
			last.path != "/api/v4/users/me/teams/"+teamA+"/threads/"+postInChanA+"/following" || last.body != "" ||
			len(last.query) != 0 || len(calls) != 4 {
			t.Fatalf("%s: calls = %+v, want three reads and one bodyless request last", tool.id, calls)
		}
	}
}

func TestThreadFollowingRequiresConfirmationAndUpdatePermission(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, `{}`, okStatus))
	for _, id := range []string{threadsFollow.ID, threadsUnfollow.ID} {
		arguments := `{"team_id":"` + teamA + `","thread_id":"` + postInChanA + `"}`
		if _, err := env.invoke(id, "editor", arguments); !isConfirmationRequired(err) {
			t.Fatalf("%s err = %v, want confirmation-required", id, err)
		}
		if _, err := env.confirmed(id, "team", arguments); err == nil {
			t.Fatalf("%s: want a refusal without the update permission", id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestThreadFollowingUnclearResultIsNeverRetried(t *testing.T) {
	for _, tool := range []struct{ id, wantText string }{
		{threadsFollow.ID, "may have been followed"}, {threadsUnfollow.ID, "may have been unfollowed"},
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
			env := newEnvironment(t, &calls, threadServer(t, `{}`, fail))
			_, err := env.confirmed(tool.id, "editor", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+`"}`)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if countMethod(calls, http.MethodPut)+countMethod(calls, http.MethodDelete) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}
