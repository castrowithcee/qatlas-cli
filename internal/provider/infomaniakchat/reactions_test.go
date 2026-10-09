package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	selfID     = "selfuser00000000000000a1"
	otherID    = "otheruser000000000000b2"
	emojiCanry = "tada"
)

// reactionServer answers the binding reads and users/me; mutate handles POST and DELETE, list the
// reactions read.
func reactionServer(t *testing.T, me string, list string, mutate func(*http.Request) (*http.Response, error),
) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me":
			return jsonResponse(200, `{"id":"`+me+`"}`), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/reactions"):
			return jsonResponse(200, list), nil
		case r.Method == http.MethodPost || r.Method == http.MethodDelete:
			if mutate != nil {
				return mutate(r)
			}
		case r.Method == http.MethodGet:
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func reactionBody(user, post, emoji string) string {
	return `{"user_id":"` + user + `","post_id":"` + post + `","emoji_name":"` + emoji + `","create_at":1735689600000}`
}

func TestReactionsListPagesLocallyAndOutputsThreeFields(t *testing.T) {
	list := `[` + reactionBody(selfID, postInChanA, "a1") + `,` + reactionBody(otherID, postInChanA, "b2") + `,` +
		reactionBody(otherID, postInChanA, "c3") + `,` + reactionBody(otherID, postInChanB, "foreign") + `]`
	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, selfID, list, nil))
	result, err := env.invoke(reactionsList.ID, "team", `{"post_id":"`+postInChanA+`","page":2,"limit":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ReactionsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Total != 3 || page.Pages != 2 ||
		page.Count != 1 || page.Reactions[0].EmojiName != "c3" || page.Reactions[0].CreatedAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("result = %s, %v", result, err)
	}
	var generic struct {
		Reactions []map[string]any `json:"reactions"`
	}
	_ = json.Unmarshal([]byte(result), &generic)
	if len(generic.Reactions[0]) != 3 {
		t.Fatalf("entry = %v, want exactly user_id, emoji_name, created_at", generic.Reactions[0])
	}
}

func TestReactionToolsRefuseForeignDeletedAndMalformedBeforeAnyChange(t *testing.T) {
	cases := []struct {
		name, post, emoji string
		allowList         bool
		local             bool
	}{
		{"foreign team", postInChanB, "tada", false, false},
		{"outside allow-list", postInChanC, "tada", true, false},
		{"deleted post", postDeleted, "tada", false, false},
		{"malformed post", "../" + chanA, "tada", false, true},
		{"malformed emoji", postInChanA, "Bad/Emoji", false, true},
		{"empty emoji", postInChanA, "", false, true},
	}
	tools := []struct{ id, plain, narrow string }{
		{reactionsAdd.ID, "reactor", "reactorch"},
		{reactionsRemove.ID, "unreact", "unreactch"},
	}
	for _, tool := range tools {
		for _, tt := range cases {
			t.Run(tool.id+"/"+tt.name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, nil))
				connection := tool.plain
				if tt.allowList {
					connection = tool.narrow
				}
				_, err := env.confirmed(tool.id, connection, `{"post_id":"`+tt.post+`","emoji_name":"`+tt.emoji+`"}`)
				if !isInvalidRequest(err) {
					t.Fatalf("err = %v, want an invalid request", err)
				}
				if tt.local && (*env.reads != 0 || len(calls) != 0) {
					t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
				}
				if countMethod(calls, http.MethodPost)+countMethod(calls, http.MethodDelete) != 0 {
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

func TestReactionsListRefusesForeignPost(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, nil))
	_, err := env.invoke(reactionsList.ID, "team", `{"post_id":"`+postInChanB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/reactions") {
			t.Fatalf("calls = %+v, want no reactions read", calls)
		}
	}
}

func TestReactionsAddSendsOnePostAsTheOwnUser(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/reactions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, reactionBody(selfID, postInChanA, emojiCanry)), nil
	}))
	// An extra user_id argument is rejected by the schema before anything runs.
	if _, err := env.confirmed(reactionsAdd.ID, "reactor",
		`{"post_id":"`+postInChanA+`","emoji_name":"tada","user_id":"`+otherID+`"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a schema refusal", err, calls)
	}
	result, err := env.confirmed(reactionsAdd.ID, "reactor", `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var added AddedReaction
	if err := json.Unmarshal([]byte(result), &added); err != nil || added.UserID != selfID || added.PostID != postInChanA ||
		added.EmojiName != emojiCanry {
		t.Fatalf("result = %s, %v", result, err)
	}
	if countMethod(calls, http.MethodPost) != 1 || len(calls) != 4 {
		t.Fatalf("calls = %+v, want three reads and exactly one POST", calls)
	}
	last := calls[len(calls)-1]
	var body map[string]string
	if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body) != 3 || body["user_id"] != selfID ||
		body["post_id"] != postInChanA || body["emoji_name"] != emojiCanry {
		t.Fatalf("body = %s", last.body)
	}
}

func TestReactionsRemoveSendsOneDeleteForTheOwnUserAndNeedsTheToolsList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"OK"}`), nil
	}))
	if _, err := env.confirmed(reactionsRemove.ID, "unreact",
		`{"post_id":"`+postInChanA+`","emoji_name":"tada","user_id":"`+otherID+`"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a schema refusal", err, calls)
	}
	result, err := env.confirmed(reactionsRemove.ID, "unreact", `{"post_id":"`+postInChanA+`","emoji_name":"+1"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var removed RemovedReaction
	if err := json.Unmarshal([]byte(result), &removed); err != nil || !removed.Removed || removed.EmojiName != "+1" {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if countMethod(calls, http.MethodDelete) != 1 || len(calls) != 4 || last.method != http.MethodDelete ||
		last.path != "/api/v4/users/"+selfID+"/posts/"+postInChanA+"/reactions/+1" {
		t.Fatalf("calls = %+v, want three reads and one DELETE for the own user", calls)
	}

	calls = nil
	if _, err := env.confirmed(reactionsRemove.ID, "nounreact", `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`); err == nil ||
		len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without a tools list entry", err, calls)
	}
}

func TestReactionMutationsRequireConfirmation(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, nil))
	for _, tool := range []struct{ id, connection string }{{reactionsAdd.ID, "reactor"}, {reactionsRemove.ID, "unreact"}} {
		if _, err := env.invoke(tool.id, tool.connection, `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`); !isConfirmationRequired(err) {
			t.Fatalf("%s err = %v, want confirmation-required", tool.id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
	}
}

func TestReactionMutationsUnclearResultIsNeverRetried(t *testing.T) {
	for _, tool := range []struct {
		id, connection, method, wantText string
	}{
		{reactionsAdd.ID, "reactor", http.MethodPost, "may have been added"},
		{reactionsRemove.ID, "unreact", http.MethodDelete, "may have been removed"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		} {
			var calls []call
			env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, fail))
			_, err := env.confirmed(tool.id, tool.connection, `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if countMethod(calls, tool.method) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}

func TestReactionsAddRejectsAMismatchingAnswerAndAnInvalidOwnID(t *testing.T) {
	for _, answer := range []string{
		reactionBody(otherID, postInChanA, "tada"),
		reactionBody(selfID, postInChanC, "tada"),
		reactionBody(selfID, postInChanA, "other"),
		`not json`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, reactionServer(t, selfID, `[]`, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, answer), nil
		}))
		_, err := env.confirmed(reactionsAdd.ID, "reactor", `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`)
		if classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been added") {
			t.Fatalf("answer %s: err = %v, want an unreadable answer with the uncertainty note", answer, err)
		}
		if countMethod(calls, http.MethodPost) != 1 {
			t.Fatalf("calls = %+v, want one POST", calls)
		}
	}

	var calls []call
	env := newEnvironment(t, &calls, reactionServer(t, "../x", `[]`, nil))
	_, err := env.confirmed(reactionsAdd.ID, "reactor", `{"post_id":"`+postInChanA+`","emoji_name":"tada"}`)
	if classOf(err) != "invalid-provider-response" || countMethod(calls, http.MethodPost) != 0 {
		t.Fatalf("err = %v, calls = %+v, want an unreadable users/me answer and no change", err, calls)
	}
}
