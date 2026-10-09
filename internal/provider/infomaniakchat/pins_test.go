package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// pinServer answers the binding reads of postServer and the pinned list of chanA; mutate handles the POST.
func pinServer(t *testing.T, pinned string, mutate func(*http.Request) (*http.Response, error),
) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pinned"):
			return jsonResponse(200, pinned), nil
		case r.Method == http.MethodPost && mutate != nil:
			return mutate(r)
		case r.Method == http.MethodGet:
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func pinnedList(entries ...[2]string) string {
	order, posts := []string{}, []string{}
	for _, e := range entries {
		order = append(order, `"`+e[0]+`"`)
		posts = append(posts, `"`+e[0]+`":`+postJSONOf(e[0], e[1], "", "user1", messageCanary, 1735689600000))
	}
	return `{"order":[` + strings.Join(order, ",") + `],"posts":{` + strings.Join(posts, ",") + `}}`
}

func TestPinsListDropsForeignChannelPostsAndPagesLocally(t *testing.T) {
	list := pinnedList([2]string{"pin00000000000000000000a1", chanA}, [2]string{postInChanB, chanB},
		[2]string{"pin00000000000000000000a2", chanA}, [2]string{"pin00000000000000000000a3", chanA})
	var calls []call
	env := newEnvironment(t, &calls, pinServer(t, list, nil))
	result, err := env.invoke(pinsList.ID, "team", `{"channel_id":"`+chanA+`","page":2,"limit":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page PinsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Total != 3 || page.Pages != 2 ||
		page.Count != 1 || page.Page != 2 || page.Messages[0].ID != "pin00000000000000000000a3" ||
		page.Messages[0].ChannelID != chanA {
		t.Fatalf("result = %s, %v", result, err)
	}
	if strings.Contains(result, chanB) || strings.Contains(result, postInChanB) {
		t.Fatalf("result = %s, want no foreign post", result)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.path != "/api/v4/channels/"+chanA+"/pinned" {
		t.Fatalf("calls = %+v, want the fixed pinned read last", calls)
	}
}

func TestPinsListRefusesForeignChannelsBeforeTheListIsRead(t *testing.T) {
	cases := []struct {
		name, connection, channel string
		local                     bool
	}{
		{"foreign team", "team", chanB, false},
		{"outside allow-list", "channel", chanC, true},
		{"malformed", "team", "../" + chanA, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, pinServer(t, pinnedList(), nil))
			_, err := env.invoke(pinsList.ID, tt.connection, `{"channel_id":"`+tt.channel+`"}`)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if tt.local && (*env.reads != 0 || len(calls) != 0) {
				t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
			}
			for _, c := range calls {
				if strings.HasSuffix(c.path, "/pinned") {
					t.Fatalf("calls = %+v, want no pinned read", calls)
				}
			}
			for _, leaked := range []string{messageCanary, chanB, teamB, chanC} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestPinMutationsSendOneFixedPostAfterTheBindingReads(t *testing.T) {
	for _, tool := range []struct {
		id, action string
		pinned     bool
	}{{messagesPin.ID, "pin", true}, {messagesUnpin.ID, "unpin", false}} {
		var calls []call
		env := newEnvironment(t, &calls, pinServer(t, `{}`, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"status":"OK"}`), nil
		}))
		result, err := env.confirmed(tool.id, "editor", `{"post_id":"`+postInChanA+`"}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", tool.id, err)
		}
		var state PinState
		if err := json.Unmarshal([]byte(result), &state); err != nil || state.PostID != postInChanA ||
			state.Pinned != tool.pinned || strings.Contains(result, "status") {
			t.Fatalf("%s: result = %s, %v", tool.id, result, err)
		}
		last := calls[len(calls)-1]
		if countMethod(calls, http.MethodPost) != 1 || len(calls) != 3 || last.method != http.MethodPost ||
			last.path != "/api/v4/posts/"+postInChanA+"/"+tool.action || last.body != "" {
			t.Fatalf("%s: calls = %+v, want two reads and one bodyless POST last", tool.id, calls)
		}
	}
}

func TestPinMutationsRefuseForeignDeletedAndMalformedPostsBeforeThePost(t *testing.T) {
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
	for _, id := range []string{messagesPin.ID, messagesUnpin.ID} {
		for _, tt := range cases {
			t.Run(id+"/"+tt.name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, pinServer(t, `{}`, nil))
				connection := "editor"
				if tt.allowList {
					connection = "editorch"
				}
				_, err := env.confirmed(id, connection, `{"post_id":"`+tt.post+`"}`)
				if !isInvalidRequest(err) {
					t.Fatalf("err = %v, want an invalid request", err)
				}
				if tt.local && (*env.reads != 0 || len(calls) != 0) {
					t.Fatalf("reads = %d, calls = %+v, want no secret access or provider I/O", *env.reads, calls)
				}
				if countMethod(calls, http.MethodPost) != 0 {
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

func TestPinMutationsRequireConfirmation(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, pinServer(t, `{}`, nil))
	for _, id := range []string{messagesPin.ID, messagesUnpin.ID} {
		if _, err := env.invoke(id, "editor", `{"post_id":"`+postInChanA+`"}`); !isConfirmationRequired(err) {
			t.Fatalf("%s err = %v, want confirmation-required", id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
	}
}

func TestPinMutationsUnclearResultIsNeverRetried(t *testing.T) {
	for _, tool := range []struct{ id, wantText string }{
		{messagesPin.ID, "may have been pinned"}, {messagesUnpin.ID, "may have been unpinned"},
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
			env := newEnvironment(t, &calls, pinServer(t, `{}`, fail))
			_, err := env.confirmed(tool.id, "editor", `{"post_id":"`+postInChanA+`"}`)
			if err == nil || !strings.Contains(err.Error(), tool.wantText) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if countMethod(calls, http.MethodPost) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", tool.id, name, calls)
			}
		}
	}
}

func TestPinMutationsNeedUpdatePermission(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, pinServer(t, `{}`, nil))
	if _, err := env.confirmed(messagesPin.ID, "team", `{"post_id":"`+postInChanA+`"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the update permission", err, calls)
	}
}
