package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	previewPost  = "prevpost000000000000p001"
	previewLeakB = "foreign-channel-text-canary"
	previewLeakC = "unlisted-channel-text-canary"
)

func mention(display, team string) string {
	if team == "" {
		return `{"display_name":"` + display + `"}`
	}
	return `{"display_name":"` + display + `","team_name":"` + team + `"}`
}

func permalink(channelID, innerChannelID, text string) string {
	data := `"post_id":"embedded1"`
	if channelID != "" {
		data += `,"channel_id":"` + channelID + `"`
	}
	post := `"id":"embedded1","message":"` + text + `"`
	if innerChannelID != "" {
		post += `,"channel_id":"` + innerChannelID + `"`
	}
	post += `,"props":{"previewed_post":"nested","channel_mentions":{"` + chanA + `":` + mention("Channel A", teamA) +
		`,"` + chanB + `":` + mention(previewLeakB, teamB) + `}},"metadata":{"embeds":[{"type":"permalink","data":{}}]}`
	return `{"type":"permalink","url":"","data":{` + data + `,"post":{` + post + `}}}`
}

// previewMessage is a message with permalink previews of every kind and mixed channel mentions.
func previewMessage(id, channelID string) string {
	embeds := []string{
		permalink(chanA, chanA, "kept-preview"),
		permalink(chanA, "", "kept-again"),
		permalink(chanB, chanB, previewLeakB),
		permalink(chanC, chanC, "kept-c"),
		permalink("", "", previewLeakB),
		permalink(chanA, chanC, previewLeakB),
		`{"type":"opengraph","url":"https://example.org/x","data":{"title":"T"}}`,
	}
	extra := `"props":{"previewed_post":"embedded1","channel_mentions":{` +
		`"` + chanA + `":` + mention("Channel A", teamA) + `,` +
		`"` + chanB + `":` + mention(previewLeakB, teamB) + `,` +
		`"` + chanC + `":` + mention(previewLeakC, "") + `,` +
		`"other":` + mention(previewLeakB, teamA) + `}},` +
		`"metadata":{"embeds":[` + strings.Join(embeds, ",") + `]}`
	return fieldPostOf(id, channelID, extra)
}

func previewServer(t *testing.T, post string, hits func(*http.Request) (*http.Response, bool)) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, "["+teamJSONOf(teamA, "A")+","+teamJSONOf(teamB, "B")+"]"), nil
		case r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels":
			return jsonResponse(200, searchChannels()), nil
		}
		if resp, ok := hits(r); ok {
			return resp, nil
		}
		if r.Method == http.MethodGet {
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func pathCount(calls []call, path string) int {
	n := 0
	for _, c := range calls {
		if c.path == path {
			n++
		}
	}
	return n
}

func checkPreviews(t *testing.T, name, result string, calls []call) {
	t.Helper()
	for _, leaked := range []string{previewLeakB, previewLeakC, `"` + chanB + `"`, `"other"`, "nested"} {
		if strings.Contains(result, leaked) {
			t.Fatalf("%s: result leaks %q: %s", name, leaked, result)
		}
	}
	for _, kept := range []string{"kept-preview", "kept-again", "kept-c", "opengraph", `"previewed_post":"embedded1"`} {
		if !strings.Contains(result, kept) {
			t.Fatalf("%s: result lost %q: %s", name, kept, result)
		}
	}
	if strings.Count(result, `"type":"permalink"`) != 3 {
		t.Fatalf("%s: want exactly the three reachable permalink previews: %s", name, result)
	}
	if strings.Count(result, `"channel_mentions"`) != 4 {
		t.Fatalf("%s: want mentions of the message and the kept previews, each reduced: %s", name, result)
	}
	if n := pathCount(calls, "/api/v4/users/me/teams"); n != 1 {
		t.Fatalf("%s: team names read %d times, want once", name, n)
	}
	if n := pathCount(calls, "/api/v4/channels/"+chanA); n > 2 || pathCount(calls, "/api/v4/channels/"+chanB) > 1 ||
		pathCount(calls, "/api/v4/channels/"+chanC) > 1 {
		t.Fatalf("%s: channel proofs repeated: %+v", name, calls)
	}
}

func TestPreviewsAreFilteredInEveryMessageOutput(t *testing.T) {
	list := postListJSONOf([]string{previewPost}, []string{previewMessage(previewPost, chanA)}, false, "")
	cases := []struct {
		name, tool, connection, args string
		hits                         func(*http.Request) (*http.Response, bool)
	}{
		{"list", messagesList.ID, "team", `{"channel_id":"` + chanA + `"}`, func(r *http.Request) (*http.Response, bool) {
			if r.URL.Path == "/api/v4/channels/"+chanA+"/posts" {
				return jsonResponse(200, list), true
			}
			return nil, false
		}},
		{"search", messagesSearch.ID, "team", `{"team_id":"` + teamA + `","terms":"x"}`, func(r *http.Request) (*http.Response, bool) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/v4/teams/"+teamA+"/posts/search" {
				return jsonResponse(200, list), true
			}
			return nil, false
		}},
		{"flagged", flaggedList.ID, "team", `{"team_id":"` + teamA + `"}`, func(r *http.Request) (*http.Response, bool) {
			if r.URL.Path == "/api/v4/users/me/posts/flagged" {
				return jsonResponse(200, list), true
			}
			return nil, false
		}},
		{"thread", threadsGet.ID, "team", `{"team_id":"` + teamA + `","thread_id":"` + previewPost + `"}`, func(r *http.Request) (*http.Response, bool) {
			switch r.URL.Path {
			case "/api/v4/posts/" + previewPost:
				return jsonResponse(200, postJSONOf(previewPost, chanA, "", "user1", "x", 1735689600000)), true
			case "/api/v4/users/me/teams/" + teamA + "/threads/" + previewPost:
				return jsonResponse(200, threadJSONOf(previewPost, chanA, previewMessage(previewPost, chanA))), true
			}
			return nil, false
		}},
	}
	for _, c := range cases {
		var calls []call
		env := newEnvironment(t, &calls, previewServer(t, list, c.hits))
		result, err := env.invoke(c.tool, c.connection, c.args)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", c.name, err)
		}
		checkPreviews(t, c.name, result, calls)
	}
}

func TestPreviewsOfChannelsOutsideTheAllowListAreRemovedWithoutProbing(t *testing.T) {
	list := postListJSONOf([]string{previewPost}, []string{previewMessage(previewPost, chanA)}, false, "")
	var calls []call
	env := newEnvironment(t, &calls, previewServer(t, list, func(r *http.Request) (*http.Response, bool) {
		if r.URL.Path == "/api/v4/channels/"+chanA+"/posts" {
			return jsonResponse(200, list), true
		}
		if r.URL.Path == "/api/v4/channels/"+chanA {
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), true
		}
		return nil, false
	}))
	result, err := env.invoke(messagesList.ID, "channel", `{"channel_id":"`+chanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if pathCount(calls, "/api/v4/channels/"+chanC) != 0 || pathCount(calls, "/api/v4/channels/"+chanB) != 0 {
		t.Fatalf("calls = %+v, want no probe of an excluded channel", calls)
	}
	for _, leaked := range []string{previewLeakB, previewLeakC, "kept-c", chanC} {
		if strings.Contains(result, leaked) {
			t.Fatalf("result leaks %q: %s", leaked, result)
		}
	}
}

func TestMessagesWithoutPreviewsCauseNoExtraRequest(t *testing.T) {
	one := fieldPostOf(previewPost, chanA, `"props":{"from_bot":"true"},"metadata":{"embeds":[{"type":"opengraph","url":"https://example.org/x"}]}`)
	list := postListJSONOf([]string{previewPost}, []string{one}, false, "")
	var calls []call
	env := newEnvironment(t, &calls, previewServer(t, list, func(r *http.Request) (*http.Response, bool) {
		if r.URL.Path == "/api/v4/channels/"+chanA+"/posts" {
			return jsonResponse(200, list), true
		}
		if r.URL.Path == "/api/v4/channels/"+chanA {
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), true
		}
		return nil, false
	}))
	result, err := env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`"}`)
	if err != nil || !strings.Contains(result, "opengraph") || len(calls) != 2 {
		t.Fatalf("result = %s, %v, calls = %+v, want the scope check and the read only", result, err, calls)
	}
}

func TestPreviewedPostWithoutAKeptPermalinkIsRemoved(t *testing.T) {
	entry := MessageEntry{Props: map[string]any{"previewed_post": "x", "keep": "y"}}
	(&previewGuard{}).apply(&entry)
	raw, _ := json.Marshal(entry.Props)
	if string(raw) != `{"keep":"y"}` {
		t.Fatalf("props = %s", raw)
	}
}
