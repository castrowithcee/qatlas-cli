package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	postInChanA   = "post00000000000000000a0a"
	postInChanB   = "post00000000000000000b0b"
	postInChanC   = "post00000000000000000c0c"
	postDeleted   = "post00000000000000000d0d"
	newTextCanary = "new-text-canary-77aa"
)

// postServer answers the post and channel reads of the fake instance; mutate handles PUT and DELETE.
func postServer(t *testing.T, mutate func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/posts/"+postInChanA:
			return jsonResponse(200, postJSONOf(postInChanA, chanA, "", "user1", messageCanary, 1735689600000)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/posts/"+postInChanB:
			return jsonResponse(200, postJSONOf(postInChanB, chanB, "", "user1", messageCanary, 1735689600000)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/posts/"+postInChanC:
			return jsonResponse(200, postJSONOf(postInChanC, chanC, "", "user1", messageCanary, 1735689600000)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/posts/"+postDeleted:
			gone := strings.Replace(postJSONOf(postDeleted, chanA, "", "user1", messageCanary, 1735689600000),
				`"edit_at":0`, `"edit_at":0,"delete_at":1735689900000`, 1)
			return jsonResponse(200, gone), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanB:
			return jsonResponse(200, channelJSONOf(chanB, teamB, "Channel B")), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/channels/"+chanC:
			return jsonResponse(200, channelJSONOf(chanC, teamA, "Channel C")), nil
		case r.Method == http.MethodPut || r.Method == http.MethodDelete:
			if mutate != nil {
				return mutate(r)
			}
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func countMethod(calls []call, method string) int {
	n := 0
	for _, c := range calls {
		if c.method == method {
			n++
		}
	}
	return n
}

func TestMessagesGetReadsOnePostOfABoundChannel(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, postServer(t, nil))
	result, err := env.invoke(messagesGet.ID, "team", `{"post_id":"`+postInChanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var entry MessageEntry
	if err := json.Unmarshal([]byte(result), &entry); err != nil || entry.ID != postInChanA ||
		entry.ChannelID != chanA || entry.Message != messageCanary {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the post read and the channel check", calls)
	}
}

// Posts of a foreign team, outside the allow-list, deleted, or with a malformed ID are refused before any
// changing request, with an error that names neither the foreign target nor any message content.
func TestPostToolsRefuseForeignDeletedAndMalformedPostsBeforeAnyChange(t *testing.T) {
	cases := []struct {
		name, post string
		allowList  bool
	}{
		{"foreign team", postInChanB, false},
		{"outside allow-list", postInChanC, true},
		{"deleted post", postDeleted, false},
		{"malformed id", "../" + chanA, false},
	}
	tools := []struct {
		id, plain, narrow, arguments string
	}{
		{messagesGet.ID, "team", "channel", `{"post_id":"%s"}`},
		{messagesUpdate.ID, "editor", "editorch", `{"post_id":"%s","text":"x"}`},
		{messagesDelete.ID, "deleter", "deleterch", `{"post_id":"%s"}`},
	}
	for _, tool := range tools {
		for _, tt := range cases {
			t.Run(tool.id+"/"+tt.name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, postServer(t, nil))
				connection := tool.plain
				if tt.allowList {
					connection = tool.narrow
				}
				arguments := strings.Replace(tool.arguments, "%s", tt.post, 1)
				var err error
				if tool.id == messagesGet.ID {
					_, err = env.invoke(tool.id, connection, arguments)
				} else {
					_, err = env.confirmed(tool.id, connection, arguments)
				}
				if !isInvalidRequest(err) {
					t.Fatalf("err = %v, want an invalid request", err)
				}
				if tt.name == "malformed id" && (*env.reads != 0 || len(calls) != 0) {
					t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
				}
				if countMethod(calls, http.MethodPut)+countMethod(calls, http.MethodDelete) != 0 {
					t.Fatalf("calls = %+v, want no changing request", calls)
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

func TestMessagesUpdateSendsExactlyOnePatchWithOnlyTheMessage(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, postServer(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/posts/"+postInChanA+"/patch" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, strings.Replace(postJSONOf(postInChanA, chanA, "", "user1", newTextCanary, 1735689600000),
			`"edit_at":0`, `"edit_at":1735689800000`, 1)), nil
	}))
	result, err := env.confirmed(messagesUpdate.ID, "editor", `{"post_id":"`+postInChanA+`","text":"`+newTextCanary+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var updated UpdatedMessage
	if err := json.Unmarshal([]byte(result), &updated); err != nil || updated.ID != postInChanA ||
		updated.ChannelID != chanA || updated.EditedAt == "" {
		t.Fatalf("result = %s, %v", result, err)
	}
	if countMethod(calls, http.MethodPut) != 1 || len(calls) != 3 {
		t.Fatalf("calls = %+v, want two reads and exactly one PUT", calls)
	}
	last := calls[len(calls)-1]
	var body map[string]any
	if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body) != 1 || body["message"] != newTextCanary {
		t.Fatalf("body = %s, want only the message", last.body)
	}
}

func TestMessagesUpdateAndDeleteRequireConfirmationAndValidText(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, postServer(t, nil))
	if _, err := env.invoke(messagesUpdate.ID, "editor", `{"post_id":"`+postInChanA+`","text":"x"}`); !isConfirmationRequired(err) {
		t.Fatalf("update err = %v, want confirmation-required", err)
	}
	if _, err := env.invoke(messagesDelete.ID, "deleter", `{"post_id":"`+postInChanA+`"}`); !isConfirmationRequired(err) {
		t.Fatalf("delete err = %v, want confirmation-required", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
	}
	_, err := env.confirmed(messagesUpdate.ID, "editor", `{"post_id":"`+postInChanA+`","text":"bad\u0001text"}`)
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal of the text", err, calls)
	}
}

func TestMessagesUpdateAndDeleteUnclearResultIsNeverRetried(t *testing.T) {
	for _, tool := range []struct {
		id, connection, arguments, method, wantText string
	}{
		{messagesUpdate.ID, "editor", `{"post_id":"` + postInChanA + `","text":"x"}`, http.MethodPut, "may have been changed"},
		{messagesDelete.ID, "deleter", `{"post_id":"` + postInChanA + `"}`, http.MethodDelete, "may have been deleted"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		} {
			var calls []call
			env := newEnvironment(t, &calls, postServer(t, fail))
			_, err := env.confirmed(tool.id, tool.connection, tool.arguments)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if countMethod(calls, tool.method) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}

func TestMessagesUpdateRejectsAnAnswerForAnotherPostOrChannel(t *testing.T) {
	for _, answer := range []string{
		postJSONOf(postInChanC, chanA, "", "user1", "x", 1735689600000),
		postJSONOf(postInChanA, chanC, "", "user1", "x", 1735689600000),
		`not json`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, postServer(t, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, answer), nil
		}))
		_, err := env.confirmed(messagesUpdate.ID, "editor", `{"post_id":"`+postInChanA+`","text":"x"}`)
		if classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been changed") {
			t.Fatalf("answer %s: err = %v, want an unreadable answer with the uncertainty note", answer, err)
		}
		if countMethod(calls, http.MethodPut) != 1 {
			t.Fatalf("calls = %+v, want one PUT", calls)
		}
	}
}

func TestMessagesDeleteSendsExactlyOneDeleteAndNeedsTheToolsList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, postServer(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v4/posts/"+postInChanA {
			t.Fatalf("unexpected delete of %s", r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	}))
	result, err := env.confirmed(messagesDelete.ID, "deleter", `{"post_id":"`+postInChanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var deleted DeletedMessage
	if err := json.Unmarshal([]byte(result), &deleted); err != nil || !deleted.Deleted || deleted.ID != postInChanA ||
		deleted.ChannelID != chanA {
		t.Fatalf("result = %s, %v", result, err)
	}
	if countMethod(calls, http.MethodDelete) != 1 || len(calls) != 3 {
		t.Fatalf("calls = %+v, want two reads and exactly one DELETE", calls)
	}

	calls = nil
	if _, err := env.confirmed(messagesDelete.ID, "nodelete", `{"post_id":"`+postInChanA+`"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without a tools list entry", err, calls)
	}
}
