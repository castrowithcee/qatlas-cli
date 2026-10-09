package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	chanDM      = "chandm00000000000000000d1"
	chanP       = "chanp000000000000000000p4"
	chanGone    = "changone00000000000000g05"
	searchTerm  = "needle-term-canary"
	hitInChanA  = "hitpost000000000000000a1"
	hitInChanB  = "hitpost000000000000000b2"
	hitInChanC  = "hitpost000000000000000c3"
	hitInDM     = "hitpost000000000000000d4"
	hitInChanP  = "hitpost000000000000000p5"
	hitFileA    = "hitfile000000000000000a1"
	hitFileB    = "hitfile000000000000000b2"
	hitFileDM   = "hitfile000000000000000d3"
	hitFileNone = "hitfile000000000000000n4"
	hitFileC    = "hitfile000000000000000c5"
)

// searchChannels is the token's channel list of teamA: two public channels, a private one, a direct channel,
// and an archived one. chanB (teamB) is deliberately absent, the token being a member elsewhere.
func searchChannels() string {
	return "[" + channelJSONOf(chanA, teamA, "Channel A") + "," + channelJSONOf(chanC, teamA, "Channel C") + "," +
		strings.Replace(channelJSONOf(chanP, teamA, "Private"), `"type":"O"`, `"type":"P"`, 1) + "," +
		strings.Replace(channelJSONOf(chanDM, "", "dm"), `"type":"O"`, `"type":"D"`, 1) + "," +
		strings.Replace(channelJSONOf(chanGone, teamA, "Gone"), `"delete_at":0`, `"delete_at":5`, 1) + "]"
}

func searchServer(t *testing.T, search map[string]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels" {
			return jsonResponse(200, searchChannels()), nil
		}
		if body, ok := search[r.URL.Path]; ok && r.Method == http.MethodPost {
			return jsonResponse(200, body), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func postsAnswer() string {
	ids := []string{hitInChanB, hitInChanA, hitInDM, hitInChanC, hitInChanP}
	chans := []string{chanB, chanA, chanDM, chanC, chanP}
	posts, order := `"posts":{`, `"order":[`
	for i, id := range ids {
		if i > 0 {
			order += ","
			posts += ","
		}
		order += `"` + id + `"`
		posts += `"` + id + `":` + postJSONOf(id, chans[i], "", "user", messageCanary, 1735689600000)
	}
	// An entry the order does not name, and an order entry without a post, are ignored.
	posts += `,"unlisted0000000000000000":` + postJSONOf("unlisted0000000000000000", chanA, "", "u", "x", 1)
	return `{` + order + `,"ghost"],` + posts + `},"matches":{}}`
}

func filesAnswer() string {
	file := func(id, channel string) string {
		extra := ""
		if channel != "" {
			extra = `,"channel_id":"` + channel + `"`
		}
		return `"` + id + `":` + `{"id":"` + id + `","post_id":"` + hitInChanA + `","user_id":"u","name":"` + fileNameSeen +
			`","size":10,"create_at":1735689600000,"delete_at":0` + extra + `}`
	}
	return `{"order":["` + hitFileB + `","` + hitFileA + `","` + hitFileDM + `","` + hitFileNone + `","` + hitFileC +
		`"],"file_infos":{` + file(hitFileA, chanA) + `,` + file(hitFileB, chanB) + `,` + file(hitFileDM, chanDM) + `,` +
		file(hitFileNone, "") + `,` + file(hitFileC, chanC) + `}}`
}

func channelsAnswer() string {
	return "[" + channelJSONOf(chanB, teamB, "B") + "," + channelJSONOf(chanA, teamA, "Channel A") + "," +
		channelJSONOf(chanDM, teamA, "dm") + "," + channelJSONOf(chanC, teamA, "Channel C") + "," +
		strings.Replace(channelJSONOf(chanP, teamA, "Private"), `"type":"O"`, `"type":"P"`, 1) + "," +
		channelJSONOf("chanpublic0000000000000x9", teamA, "Not joined") + "]"
}

func searchPaths() map[string]string {
	return map[string]string{
		"/api/v4/teams/" + teamA + "/posts/search":    postsAnswer(),
		"/api/v4/teams/" + teamA + "/files/search":    filesAnswer(),
		"/api/v4/teams/" + teamA + "/channels/search": channelsAnswer(),
	}
}

func TestSearchToolsAreReadOnlyGroupedAndInReadAndMessaging(t *testing.T) {
	for id, group := range map[string]string{messagesSearch.ID: "messages", filesSearch.ID: "files",
		channelsSearch.ID: "channels"} {
		var d capability.Descriptor
		switch id {
		case messagesSearch.ID:
			d = messagesSearch
		case filesSearch.ID:
			d = filesSearch
		default:
			d = channelsSearch
		}
		if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone ||
			d.RequiresToolAllowList || withGroup(d).Group != group {
			t.Fatalf("%s = %+v", id, d)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "read" && profile.ID != "messaging" {
			continue
		}
		got := map[string]bool{}
		for _, id := range profile.Tools {
			got[id] = true
		}
		if !got[messagesSearch.ID] || !got[filesSearch.ID] || !got[channelsSearch.ID] {
			t.Fatalf("profile %s = %v", profile.ID, profile.Tools)
		}
	}
}

func TestMessagesSearchKeepsOnlyReachableChannelHits(t *testing.T) {
	for _, c := range []struct {
		connection string
		want       []string
	}{{"team", []string{hitInChanA, hitInChanC, hitInChanP}}, {"channel", []string{hitInChanA}}} {
		var calls []call
		env := newEnvironment(t, &calls, searchServer(t, searchPaths()))
		result, err := env.invoke(messagesSearch.ID, c.connection,
			`{"team_id":"`+teamA+`","terms":"`+searchTerm+` from:bob","is_or_search":true,"page":3,"limit":5}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", c.connection, err)
		}
		var out SearchedMessages
		if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != len(c.want) || out.Page != 3 ||
			!out.HasMore || out.TeamID != teamA {
			t.Fatalf("%s: result = %s, %v", c.connection, result, err)
		}
		for i, id := range c.want {
			if out.Messages[i].ID != id {
				t.Fatalf("%s: messages = %+v, want %v in kChat's order", c.connection, out.Messages, c.want)
			}
		}
		if strings.Contains(result, hitInChanB) || strings.Contains(result, hitInDM) || strings.Contains(result, "unlisted") {
			t.Fatalf("%s: result leaks a hit outside the reachable channels: %s", c.connection, result)
		}
		if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost {
			t.Fatalf("calls = %+v, want one channel read and one search", calls)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(calls[1].body), &body); err != nil || len(body) != 4 ||
			body["terms"] != searchTerm+" from:bob" || body["is_or_search"] != true || body["page"] != float64(2) ||
			body["per_page"] != float64(5) {
			t.Fatalf("body = %s, %v", calls[1].body, err)
		}
		if strings.Contains(calls[1].body, "include_deleted_channels") {
			t.Fatalf("body = %s, want no include_deleted_channels", calls[1].body)
		}
	}
}

func TestFilesSearchKeepsOnlyReachableChannelHits(t *testing.T) {
	for _, c := range []struct {
		connection string
		want       []string
	}{{"team", []string{hitFileA, hitFileC}}, {"channel", []string{hitFileA}}} {
		var calls []call
		env := newEnvironment(t, &calls, searchServer(t, searchPaths()))
		result, err := env.invoke(filesSearch.ID, c.connection, `{"team_id":"`+teamA+`","terms":"ext:pdf"}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", c.connection, err)
		}
		var out SearchedFiles
		if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != len(c.want) || out.Page != 1 ||
			out.HasMore {
			t.Fatalf("%s: result = %s, %v", c.connection, result, err)
		}
		for i, id := range c.want {
			if out.Files[i].ID != id || out.Files[i].CreatedAt != "2025-01-01T00:00:00Z" {
				t.Fatalf("%s: files = %+v, want %v", c.connection, out.Files, c.want)
			}
		}
		if len(calls) != 2 || strings.Contains(calls[1].body, "include_deleted_channels") ||
			!strings.Contains(calls[1].body, `"page":0`) || !strings.Contains(calls[1].body, `"per_page":50`) {
			t.Fatalf("calls = %+v", calls)
		}
	}
}

func TestChannelsSearchKeepsOnlyReachableMemberPublicChannels(t *testing.T) {
	for _, c := range []struct {
		connection string
		want       []string
	}{{"team", []string{chanA, chanC}}, {"channel", []string{chanA}}} {
		var calls []call
		env := newEnvironment(t, &calls, searchServer(t, searchPaths()))
		result, err := env.invoke(channelsSearch.ID, c.connection, `{"team_id":"`+teamA+`","term":"chan"}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", c.connection, err)
		}
		var out SearchedChannels
		if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != len(c.want) || out.TeamID != teamA {
			t.Fatalf("%s: result = %s, %v", c.connection, result, err)
		}
		for i, id := range c.want {
			if out.Channels[i].ID != id {
				t.Fatalf("%s: channels = %+v, want %v", c.connection, out.Channels, c.want)
			}
		}
		if len(calls) != 2 || calls[1].body != `{"term":"chan"}` {
			t.Fatalf("calls = %+v", calls)
		}
	}
}

func TestSearchRefusesForeignTeamAndBadArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	long := strings.Repeat("a", maxSearchRunes+1)
	for _, tool := range []string{messagesSearch.ID, filesSearch.ID, channelsSearch.ID} {
		key := "terms"
		if tool == channelsSearch.ID {
			key = "term"
		}
		for _, arguments := range []string{
			`{"team_id":"` + teamB + `","` + key + `":"x"}`,
			`{"team_id":"bad id","` + key + `":"x"}`,
			`{"team_id":"` + teamA + `","` + key + `":""}`,
			`{"team_id":"` + teamA + `","` + key + `":"a\u0000b"}`,
			`{"team_id":"` + teamA + `","` + key + `":"a\nb"}`,
			`{"team_id":"` + teamA + `","` + key + `":"` + long + `"}`,
			`{"team_id":"` + teamA + `"}`,
		} {
			_, err := env.invoke(tool, "team", arguments)
			if err == nil {
				t.Fatalf("%s %s: want a refusal", tool, arguments)
			}
			if strings.Contains(err.Error(), long) {
				t.Fatalf("%s: error quotes the term", tool)
			}
		}
	}
	for _, arguments := range []string{
		`{"team_id":"` + teamA + `","terms":"x","page":0}`,
		`{"team_id":"` + teamA + `","terms":"x","limit":101}`,
		`{"team_id":"` + teamA + `","terms":"x","is_or_search":"yes"}`,
		`{"team_id":"` + teamA + `","terms":"x","per_page":5}`,
	} {
		if _, err := env.invoke(messagesSearch.ID, "team", arguments); err == nil {
			t.Fatalf("%s: want a refusal", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
	}
}

func TestSearchErrorsCarryNeitherTermNorProviderContent(t *testing.T) {
	for _, tool := range []string{messagesSearch.ID, filesSearch.ID, channelsSearch.ID} {
		for _, failing := range []string{"channels", "search"} {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if (failing == "channels") == strings.HasSuffix(r.URL.Path, "/channels") && r.Method == http.MethodGet ||
					(failing == "search" && r.Method == http.MethodPost) {
					return jsonResponse(500, `{"message":"`+messageCanary+`"}`), nil
				}
				return jsonResponse(200, searchChannels()), nil
			})
			key := "terms"
			if tool == channelsSearch.ID {
				key = "term"
			}
			_, err := env.invoke(tool, "team", `{"team_id":"`+teamA+`","`+key+`":"`+searchTerm+`"}`)
			if err == nil || strings.Contains(err.Error(), searchTerm) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s/%s: err = %v", tool, failing, err)
			}
			if failing == "search" && len(calls) != 2 {
				t.Fatalf("calls = %+v, want no retry", calls)
			}
		}
	}
}
