package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func postListJSONOf(ids, posts []string, hasNext bool, nextPostID string) string {
	pairs := make([]string, len(ids))
	for i, id := range ids {
		pairs[i] = `"` + id + `":` + posts[i]
	}
	return `{"order":["` + strings.Join(ids, `","`) + `"],"posts":{` + strings.Join(pairs, ",") + `},` +
		`"next_post_id":"` + nextPostID + `","has_next":` + strconv.FormatBool(hasNext) + `}`
}

// messages.list confirms a directly named channel_id live against the instance before it ever reads
// messages, refuses a channel of a foreign team after exactly that one confirming request, and refuses a
// channel outside a configured channel allow-list before any request at all.
func TestMessagesListVerifiesChannelScopeLiveBeforeReadingPosts(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/channels/" + chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case "/api/v4/channels/" + chanA + "/posts":
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "10" {
				t.Fatalf("query = %v, want page=1 per_page=10", r.URL.Query())
			}
			one := postJSONOf("post1", chanA, "", "user1", messageCanary, 1735689600000)
			return jsonResponse(200, postListJSONOf([]string{"post1"}, []string{one}, true, "")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`","page":2,"limit":10}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page MessagesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if page.ChannelID != chanA || page.Count != 1 || !page.HasMore || page.Messages[0].Message != messageCanary {
		t.Fatalf("page = %+v", page)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want exactly one scope check and one read", calls)
	}

	// chanB belongs to teamB, a team the "team" connection is not bound to. Its channel allow-list is
	// empty, so the local check admits chanB; only the live check against the instance catches it.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/channels/"+chanB {
			return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a channel of a foreign team", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly the one live scope check, and no read of chanB's messages", calls)
	}
	if err != nil && strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("error leaked provider content: %v", err)
	}

	// chanC belongs to teamA, which the "channel" connection is bound to, but its own channel allow-list
	// narrows it to chanA alone.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.invoke(messagesList.ID, "channel", `{"channel_id":"`+chanC+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a channel outside the allow-list", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the disallowed channel was refused", calls)
	}
	if *env.reads != 0 {
		t.Fatalf("secret reads = %d, want none for a refused channel_id", *env.reads)
	}
}

// messages.thread learns a thread's channel from its root post and verifies that channel live, exactly the
// same guarantee a directly named channel_id gets, before it ever reads the thread itself.
func TestMessagesThreadResolvesChannelFromRootAndVerifiesScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/posts/" + rootInChanA:
			return jsonResponse(200, postJSONOf(rootInChanA, chanA, "", "user1", messageCanary, 1735689600000)), nil
		case "/api/v4/channels/" + chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case "/api/v4/posts/" + rootInChanA + "/thread":
			root := postJSONOf(rootInChanA, chanA, "", "user1", messageCanary, 1735689600000)
			reply := postJSONOf("reply1", chanA, rootInChanA, "user2", "reply text", 1735689700000)
			return jsonResponse(200, postListJSONOf([]string{rootInChanA, "reply1"}, []string{root, reply}, false, "")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.invoke(messagesThread.ID, "team", `{"post_id":"`+rootInChanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var thread Thread
	if err := json.Unmarshal([]byte(result), &thread); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if thread.ChannelID != chanA || len(thread.Messages) != 2 || thread.HasMore {
		t.Fatalf("thread = %+v", thread)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want the root lookup, the scope check, and the thread read", calls)
	}

	// rootInChanB belongs to chanB, a channel of teamB: the "team" connection is not bound to that team.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/posts/" + rootInChanB:
			return jsonResponse(200, postJSONOf(rootInChanB, chanB, "", "user1", messageCanary, 1735689600000)), nil
		case "/api/v4/channels/" + chanB:
			return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.invoke(messagesThread.ID, "team", `{"post_id":"`+rootInChanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a thread of a foreign team", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the root lookup and the scope check, and no thread read", calls)
	}
	if err != nil && strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("error leaked provider content: %v", err)
	}
}

// messages.send sends exactly one confirmed message, and exactly one confirmed reply, to a channel this
// connection may reach.
func TestMessagesSendSendsExactlyOnePostWhenConfirmedAndScoped(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.URL.Path == "/api/v4/posts" && r.Method == http.MethodPost:
			return jsonResponse(201, postJSONOf("sent1", chanA, "", "user1", messageCanary, 1735689600000)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+chanA+`","text":"`+messageCanary+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var sent SentMessage
	if err := json.Unmarshal([]byte(result), &sent); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if sent.ID != "sent1" || sent.ChannelID != chanA || sent.RootID != "" {
		t.Fatalf("sent = %+v", sent)
	}
	posts := 0
	for _, c := range calls {
		if c.method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("calls = %+v, want exactly one POST", calls)
	}

	// A reply additionally confirms the root belongs to the same channel before it is ever sent.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.URL.Path == "/api/v4/posts/"+rootInChanA:
			return jsonResponse(200, postJSONOf(rootInChanA, chanA, "", "user1", "root text", 1735689600000)), nil
		case r.URL.Path == "/api/v4/posts" && r.Method == http.MethodPost:
			return jsonResponse(201, postJSONOf("reply1", chanA, rootInChanA, "user2", messageCanary, 1735689700000)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err = env.confirmed(messagesSend.ID, "channel",
		`{"channel_id":"`+chanA+`","text":"`+messageCanary+`","root_id":"`+rootInChanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &sent); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if sent.RootID != rootInChanA {
		t.Fatalf("sent = %+v, want the reply's root_id echoed back", sent)
	}
	posts = 0
	for _, c := range calls {
		if c.method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 || len(calls) != 3 {
		t.Fatalf("calls = %+v, want exactly one POST after the channel and root checks", calls)
	}
}

// A rejected channel, a foreign team confirmed live, a foreign root post, and a missing confirmation each
// fail before or at the matching kChat request, and never send the message.
func TestMessagesSendRefusesOutOfScopeRequestsAndMissingConfirmation(t *testing.T) {
	noRequest := func(t *testing.T) func(*http.Request) (*http.Response, error) {
		return func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	}

	// Missing confirmation: refused before any secret is resolved or any request is sent.
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.invoke(messagesSend.ID, "channel", `{"channel_id":"`+chanA+`","text":"x"}`)
	if !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want a confirmation-required refusal", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
	}

	// A channel outside the connection's own channel allow-list: refused before any request.
	calls = nil
	env = newEnvironment(t, &calls, noRequest(t))
	_, err = env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+chanC+`","text":"x"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a channel outside the allow-list", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the disallowed channel was refused", calls)
	}

	// A channel that is locally admitted (no channel allow-list) but belongs to a foreign team: refused
	// after exactly the one live check, before any post is ever sent.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/channels/"+chanB {
			return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(messagesSend.ID, "team", `{"channel_id":"`+chanB+`","text":"x"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a channel of a foreign team", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly the one live scope check, and no post sent", calls)
	}

	// A reply whose root belongs to a different channel: refused after the channel check and the root
	// lookup, before any post is ever sent.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/channels/" + chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case "/api/v4/posts/" + rootInChanB:
			return jsonResponse(200, postJSONOf(rootInChanB, chanB, "", "user1", messageCanary, 1735689600000)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(messagesSend.ID, "channel",
		`{"channel_id":"`+chanA+`","text":"x","root_id":"`+rootInChanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a root post of another channel", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the channel check and the root lookup, and no post sent", calls)
	}
	if err != nil && strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("error leaked provider content: %v", err)
	}
}

// A missing right (401 or 403) is classified and never carries kChat's own response body, which may echo
// request content, into the error.
func TestMessagesSendClassifiesAuthAndPermissionFailuresWithoutLeakingContent(t *testing.T) {
	for _, tt := range []struct {
		status int
		class  string
	}{{http.StatusUnauthorized, "auth"}, {http.StatusForbidden, "permission"}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(tt.status, `{"message":"`+messageCanary+`"}`), nil
		})
		_, err := env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+chanA+`","text":"x"}`)
		if string(classOf(err)) != tt.class {
			t.Fatalf("status %d: class = %q, want %q", tt.status, classOf(err), tt.class)
		}
		if err != nil && strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("status %d: error %v leaked the provider body", tt.status, err)
		}
	}
}

// A 5xx after the channel was confirmed is an unclear send result: it is reported, never repeated, and the
// message text never reaches the error.
func TestMessagesSendUnclearResultIsNeverRetried(t *testing.T) {
	var calls []call
	posts := 0
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.URL.Path == "/api/v4/posts" && r.Method == http.MethodPost:
			posts++
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+chanA+`","text":"`+messageCanary+`"}`)
	if classOf(err) != "provider-error" {
		t.Fatalf("class = %q, want provider-error", classOf(err))
	}
	if err == nil || !strings.Contains(err.Error(), "may have been sent") {
		t.Fatalf("err = %v, want it to name the send as unclear", err)
	}
	if err != nil && strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("error leaked the message text: %v", err)
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want exactly one attempt, never repeated by Qatlas itself", posts)
	}
}
