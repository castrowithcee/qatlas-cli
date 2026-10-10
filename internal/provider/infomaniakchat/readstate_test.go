package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	unreadPath   = "/api/v4/users/me/channels/"
	viewPath     = "/api/v4/channels/members/me/view"
	setUnreadFmt = "/api/v4/users/me/posts/"
)

// readStateServer answers the binding reads of the thread server and the read-state endpoints; change
// answers the one changing request (POST view, POST set_unread, PUT thread read).
func readStateServer(t *testing.T, change func(*http.Request) (*http.Response, error),
) func(*http.Request) (*http.Response, error) {
	threads := threadServer(t, `{}`, change)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, unreadPath) && strings.HasSuffix(r.URL.Path, "/unread"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, unreadPath), "/unread")
			return jsonResponse(200, `{"team_id":"`+teamA+`","channel_id":"`+id+`","msg_count":4,"mention_count":2}`), nil
		case r.Method == http.MethodPost && change != nil:
			return change(r)
		}
		return threads(r)
	}
}

func changing(calls []call) int {
	return countMethod(calls, http.MethodPost) + countMethod(calls, http.MethodPut) + countMethod(calls, http.MethodDelete)
}

func TestChannelsUnreadReturnsOnlyTheCountsOfTheBoundChannel(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, readStateServer(t, nil))
	result, err := env.invoke(channelsUnread.ID, "team", `{"channel_id":"`+chanA+`"}`)
	var got ChannelUnread
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || got != (ChannelUnread{chanA, 4, 2}) ||
		strings.Contains(result, teamA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != unreadPath+chanA+"/unread" || changing(calls) != 0 {
		t.Fatalf("calls = %+v, want reads only with the fixed unread read last", calls)
	}
}

func TestChannelsUnreadRefusesADifferentChannelAnswer(t *testing.T) {
	var calls []call
	base := readStateServer(t, nil)
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, unreadPath) {
			return jsonResponse(200, `{"channel_id":"`+chanC+`","msg_count":1,"mention_count":0}`), nil
		}
		return base(r)
	})
	_, err := env.invoke(channelsUnread.ID, "team", `{"channel_id":"`+chanA+`"}`)
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), chanC) {
		t.Fatalf("err = %v, want an invalid response without the other channel", err)
	}
}

func TestReadStateRefusesForeignTargetsBeforeAnyChange(t *testing.T) {
	type tc struct {
		name, connection, arguments string
		local                       bool
	}
	cases := map[string][]tc{
		channelsUnread.ID: {
			{"foreign channel", "editor", `{"channel_id":"` + chanB + `"}`, false},
			{"outside allow-list", "editorch", `{"channel_id":"` + chanC + `"}`, true},
			{"malformed", "editor", `{"channel_id":"../` + chanA + `"}`, true},
		},
		messagesMarkUnread.ID: {
			{"foreign post", "editor", `{"post_id":"` + postInChanB + `"}`, false},
			{"outside allow-list", "editorch", `{"post_id":"` + postInChanC + `"}`, false},
			{"deleted", "editor", `{"post_id":"` + postDeleted + `"}`, false},
			{"malformed", "editor", `{"post_id":"../` + postInChanA + `"}`, true},
		},
		threadsMarkRead.ID: {
			{"foreign post", "editor", `{"team_id":"` + teamA + `","thread_id":"` + postInChanB + `"}`, false},
			{"outside allow-list", "editorch", `{"team_id":"` + teamA + `","thread_id":"` + postInChanC + `"}`, false},
			{"foreign team", "editor", `{"team_id":"` + teamB + `","thread_id":"` + postInChanA + `"}`, true},
			{"other bound team", "editor2", `{"team_id":"` + teamA + `","thread_id":"` + postInChanB + `"}`, false},
			{"future timestamp", "editor", `{"team_id":"` + teamA + `","thread_id":"` + postInChanA +
				`","timestamp":"2999-01-01T00:00:00Z"}`, true},
			{"bad timestamp", "editor", `{"team_id":"` + teamA + `","thread_id":"` + postInChanA +
				`","timestamp":"yesterday"}`, true},
			{"epoch timestamp", "editor", `{"team_id":"` + teamA + `","thread_id":"` + postInChanA +
				`","timestamp":"1970-01-01T00:00:00Z"}`, true},
		},
		channelsMarkRead.ID: {
			{"foreign channel", "editor", `{"channel_id":"` + chanB + `"}`, false},
			{"outside allow-list", "editorch", `{"channel_id":"` + chanC + `"}`, true},
			{"malformed", "editor", `{"channel_id":"../` + chanA + `"}`, true},
		},
	}
	for id, list := range cases {
		for _, tt := range list {
			t.Run(id+"/"+tt.name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, readStateServer(t, okStatus))
				var err error
				if id == channelsUnread.ID {
					_, err = env.invoke(id, tt.connection, tt.arguments)
				} else {
					_, err = env.confirmed(id, tt.connection, tt.arguments)
				}
				if !isInvalidRequest(err) {
					t.Fatalf("err = %v, want an invalid request", err)
				}
				if tt.local && (*env.reads != 0 || len(calls) != 0) {
					t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
				}
				for _, c := range calls {
					if changing([]call{c}) != 0 || strings.HasSuffix(c.path, "/unread") && id != channelsUnread.ID {
						t.Fatalf("calls = %+v, want no read-state request", calls)
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

func TestChannelsMarkReadSendsTheChannelOnly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, readStateServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"OK","last_viewed_at_times":{"`+chanA+`":1735696800000}}`), nil
	}))
	result, err := env.confirmed(channelsMarkRead.ID, "editor", `{"channel_id":"`+chanA+`"}`)
	var got ChannelRead
	if err != nil || json.Unmarshal([]byte(result), &got) != nil ||
		got != (ChannelRead{chanA, true, "2025-01-01T02:00:00Z"}) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if changing(calls) != 1 || last.method != http.MethodPost || last.path != viewPath || len(last.query) != 0 ||
		last.body != `{"channel_id":"`+chanA+`"}` || strings.Contains(last.body, "prev_channel_id") {
		t.Fatalf("calls = %+v, want one POST with the channel only, last", calls)
	}
}

func TestMessagesMarkUnreadSendsOneBodylessRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, readStateServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"team_id":"`+teamA+`","channel_id":"`+chanA+
			`","msg_count":3,"mention_count":0,"last_viewed_at":1735696800000}`), nil
	}))
	result, err := env.confirmed(messagesMarkUnread.ID, "editor", `{"post_id":"`+postInChanA+`"}`)
	var got MessageUnread
	if err != nil || json.Unmarshal([]byte(result), &got) != nil ||
		got != (MessageUnread{postInChanA, chanA, "2025-01-01T02:00:00Z"}) || strings.Contains(result, messageCanary) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if changing(calls) != 1 || last.method != http.MethodPost || last.path != setUnreadFmt+postInChanA+"/set_unread" ||
		last.body != "" || len(last.query) != 0 {
		t.Fatalf("calls = %+v, want one bodyless POST last", calls)
	}
}

func TestThreadsMarkReadSendsMillisecondsInThePath(t *testing.T) {
	answer := func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, threadJSONOf(postInChanA, chanA, "")), nil
	}
	var calls []call
	env := newEnvironment(t, &calls, readStateServer(t, answer))
	before := time.Now().UnixMilli()
	result, err := env.confirmed(threadsMarkRead.ID, "editor", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+`"}`)
	after := time.Now().UnixMilli()
	var got ThreadRead
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || got.TeamID != teamA || got.ThreadID != postInChanA ||
		got.LastViewedAt != "2025-01-01T02:00:00Z" || strings.Contains(result, messageCanary) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	prefix := "/api/v4/users/me/teams/" + teamA + "/threads/" + postInChanA + "/read/"
	ms, convErr := strconv.ParseInt(strings.TrimPrefix(last.path, prefix), 10, 64)
	if changing(calls) != 1 || last.method != http.MethodPut || !strings.HasPrefix(last.path, prefix) ||
		convErr != nil || ms < before || ms > after || last.body != "" || len(last.query) != 0 {
		t.Fatalf("calls = %+v, want one bodyless PUT with the current milliseconds last", calls)
	}

	calls = nil
	_, err = env.confirmed(threadsMarkRead.ID, "editor", `{"team_id":"`+teamA+`","thread_id":"`+postInChanA+
		`","timestamp":"2025-01-01T01:00:00+01:00"}`)
	if last := calls[len(calls)-1]; err != nil || last.path != prefix+"1735689600000" {
		t.Fatalf("err = %v, calls = %+v, want the explicit time in milliseconds", err, calls)
	}
}

func TestThreadReadTime(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	if ms, err := threadReadTime("", now); err != nil || ms != now.UnixMilli() {
		t.Fatalf("default = %d, %v", ms, err)
	}
	if ms, err := threadReadTime(now.Format(time.RFC3339), now); err != nil || ms != now.UnixMilli() {
		t.Fatalf("now = %d, %v", ms, err)
	}
	for _, bad := range []string{"2025-06-01T12:00:01Z", "2025-06-01", "x", "1969-12-31T23:59:59Z"} {
		if _, err := threadReadTime(bad, now); !isInvalidRequest(err) {
			t.Fatalf("%q: err = %v, want an invalid request", bad, err)
		}
	}
}

func TestReadStateMarksRequireConfirmationAndUpdatePermission(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, readStateServer(t, okStatus))
	for id, arguments := range map[string]string{
		channelsMarkRead.ID:   `{"channel_id":"` + chanA + `"}`,
		messagesMarkUnread.ID: `{"post_id":"` + postInChanA + `"}`,
		threadsMarkRead.ID:    `{"team_id":"` + teamA + `","thread_id":"` + postInChanA + `"}`,
	} {
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

func TestReadStateMarksNeverRetryAnUnclearResult(t *testing.T) {
	tools := []struct{ id, arguments, wantText string }{
		{channelsMarkRead.ID, `{"channel_id":"` + chanA + `"}`, "may have been marked as read"},
		{messagesMarkUnread.ID, `{"post_id":"` + postInChanA + `"}`, "may have been marked as unread"},
		{threadsMarkRead.ID, `{"team_id":"` + teamA + `","thread_id":"` + postInChanA + `"}`, "may have been marked as read"},
	}
	for _, tool := range tools {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
			"other target": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"status":"FAIL","id":"`+postInChanC+`","channel_id":"`+chanC+`"}`), nil
			},
		} {
			var calls []call
			env := newEnvironment(t, &calls, readStateServer(t, fail))
			_, err := env.confirmed(tool.id, "editor", tool.arguments)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if changing(calls) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}
