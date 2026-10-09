package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Direct and group channels of the fake instance. selfID is the token's own user; userInA and userBoth are
// members of teamA, userInB of teamB only.
const (
	dmA       = "dmchana000000000000000a1" // self and userInA
	dmB       = "dmchanb000000000000000b2" // self and userInB
	dmForeign = "dmchanf000000000000000f3" // userInA and userInB, without self
	dmDeleted = "dmchand000000000000000d4"
	gmA       = "gmchana000000000000000a1" // self, userInA, userBoth
	gmMixed   = "gmchanm000000000000000m2" // self, userInA, userInB
	gmBig     = "gmchanbig00000000000000b3"
	gmNoSelf  = "gmchann000000000000000n4" // userInA and userBoth, without self
	dmCanary  = "direct-display-canary-77ab"
)

func directChannelJSON(id, kind, name string) string {
	return `{"id":"` + id + `","team_id":"","type":"` + kind + `","display_name":"` + dmCanary + `","name":"` +
		name + `","purpose":"","delete_at":0}`
}

func directName(a, b string) string { return a + "__" + b }

// directFixture is a fake instance with the channels above; mutate answers every other POST.
type directFixture struct {
	t       *testing.T
	listing string
	mutate  func(*http.Request) (*http.Response, error)
}

func (f *directFixture) channels() map[string]string {
	return map[string]string{
		dmA:       directChannelJSON(dmA, "D", directName(selfID, userInA)),
		dmB:       directChannelJSON(dmB, "D", directName(userInB, selfID)),
		dmForeign: directChannelJSON(dmForeign, "D", directName(userInA, userInB)),
		gmA:       directChannelJSON(gmA, "G", "gmhash1"),
		gmMixed:   directChannelJSON(gmMixed, "G", "gmhash2"),
		gmBig:     directChannelJSON(gmBig, "G", "gmhash3"),
		gmNoSelf:  directChannelJSON(gmNoSelf, "G", "gmhash4"),
		chanA:     channelJSONOf(chanA, teamA, "Channel A"),
	}
}

func groupMembers(channelID string, users ...string) string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = `{"channel_id":"` + channelID + `","user_id":"` + u + `"}`
	}
	return "[" + strings.Join(out, ",") + "]"
}

func (f *directFixture) members() map[string]string {
	big := []string{selfID, userInA, userBoth}
	for i := 0; i < 6; i++ {
		big = append(big, "extrauser00000000000000"+strconv.Itoa(i))
	}
	return map[string]string{
		gmA:      groupMembers(gmA, selfID, userInA, userBoth),
		gmMixed:  groupMembers(gmMixed, selfID, userInA, userInB),
		gmBig:    groupMembers(gmBig, big...),
		gmNoSelf: groupMembers(gmNoSelf, userInA, userBoth),
	}
}

func (f *directFixture) handle(r *http.Request) (*http.Response, error) {
	teams := map[string][]string{teamA: {userInA, userBoth}, teamB: {userInB, userBoth}}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/api/v4/users/me":
		return jsonResponse(200, `{"id":"`+selfID+`"}`), nil
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v4/teams/") && strings.HasSuffix(path, "/members/ids"):
		team := strings.Split(path, "/")[4]
		var asked []string
		_ = json.NewDecoder(r.Body).Decode(&asked)
		var out []string
		for _, id := range asked {
			for _, member := range teams[team] {
				if id == member {
					out = append(out, `{"team_id":"`+team+`","user_id":"`+id+`","delete_at":0}`)
				}
			}
		}
		return jsonResponse(200, "["+strings.Join(out, ",")+"]"), nil
	case r.Method == http.MethodGet && path == "/api/v4/users/me/teams/"+teamA+"/channels":
		return jsonResponse(200, f.listing), nil
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v4/channels/") && strings.HasSuffix(path, "/members"):
		id := strings.Split(path, "/")[4]
		if r.URL.Query().Get("per_page") != "9" {
			f.t.Fatalf("members query = %v, want per_page=9", r.URL.Query())
		}
		if body, ok := f.members()[id]; ok {
			return jsonResponse(200, body), nil
		}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v4/channels/"):
		if body, ok := f.channels()[strings.TrimPrefix(path, "/api/v4/channels/")]; ok {
			return jsonResponse(200, body), nil
		}
	case r.Method == http.MethodPost && f.mutate != nil:
		return f.mutate(r)
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, path)
	return nil, nil
}

func (f *directFixture) env(calls *[]call) *environment { return newEnvironment(f.t, calls, f.handle) }

func countPath(calls []call, method, path string) int {
	n := 0
	for _, c := range calls {
		if c.method == method && c.path == path {
			n++
		}
	}
	return n
}

// leaks reports whether a refusal names a participant, a channel name, or provider content.
func leaks(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, secret := range []string{userInA, userInB, userBoth, selfID, dmCanary, "__", "gmhash", messageCanary} {
		if strings.Contains(text, secret) {
			return true
		}
	}
	return false
}

func sendAnswer(channelID string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v4/posts" {
			return nil, context.Canceled
		}
		return jsonResponse(201, postJSONOf("sent1", channelID, "", selfID, messageCanary, 1735689600000)), nil
	}
}

// messages.send reaches a direct or group channel only when every other participant is a live member of a
// bound team; any other one is refused at the live channel check, before a post is sent, without naming a
// participant or the channel.
func TestMessagesSendBindsDirectAndGroupChannelsToTheBoundTeams(t *testing.T) {
	for _, tt := range []struct {
		name, channel string
		ok            bool
	}{
		{"direct with a member of teamA", dmA, true},
		{"group of members of teamA", gmA, true},
		{"direct with a user only in teamB", dmB, false},
		{"direct channel name without the own user", dmForeign, false},
		{"group with a user only in teamB", gmMixed, false},
		{"group with more than eight members", gmBig, false},
		{"group without the own user", gmNoSelf, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			f := &directFixture{t: t, mutate: sendAnswer(tt.channel)}
			_, err := f.env(&calls).confirmed(messagesSend.ID, "team",
				`{"channel_id":"`+tt.channel+`","text":"`+messageCanary+`"}`)
			posts := countPath(calls, http.MethodPost, "/api/v4/posts")
			if tt.ok {
				if err != nil || posts != 1 {
					t.Fatalf("err = %v, calls = %+v, want exactly one sent post", err, calls)
				}
				return
			}
			if !isInvalidRequest(err) || posts != 0 || leaks(err) {
				t.Fatalf("err = %v, calls = %+v, want an invalid request without a post or a leak", err, calls)
			}
		})
	}
}

// A channel allow-list keeps applying to direct channels: a direct channel it does not name is refused
// before any secret is read or any request is sent, and the one it names stays reachable.
func TestChannelAllowListAppliesToDirectChannels(t *testing.T) {
	var calls []call
	f := &directFixture{t: t, mutate: sendAnswer(dmA)}
	env := f.env(&calls)
	_, err := env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+dmA+`","text":"x"}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
	_, err = env.invoke(messagesList.ID, "dmonly", `{"channel_id":"`+dmB+`"}`)
	if !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a local refusal of an unlisted direct channel", err, calls)
	}
	if _, err := env.confirmed(messagesSend.ID, "dmonly", `{"channel_id":"`+dmA+`","text":"x"}`); err != nil ||
		countPath(calls, http.MethodPost, "/api/v4/posts") != 1 {
		t.Fatalf("err = %v, calls = %+v, want the listed direct channel reachable", err, calls)
	}
}

// A post_id in a foreign direct channel is refused through its channel before any detail is returned.
func TestPostInForeignDirectChannelIsRefused(t *testing.T) {
	var calls []call
	f := &directFixture{t: t}
	inner := f.handle
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/posts/"+rootInChanB {
			return jsonResponse(200, postJSONOf(rootInChanB, dmB, "", userInB, messageCanary, 1735689600000)), nil
		}
		return inner(r)
	})
	_, err := env.invoke(messagesGet.ID, "team", `{"post_id":"`+rootInChanB+`"}`)
	if !isInvalidRequest(err) || leaks(err) {
		t.Fatalf("err = %v, want an invalid request without a leak", err)
	}
}

func openedDirect(id, a, b string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		return jsonResponse(201, directChannelJSON(id, "D", directName(a, b))), nil
	}
}

func TestDirectOpenSendsExactlyOnePostForAMemberOfABoundTeam(t *testing.T) {
	var calls []call
	f := &directFixture{t: t, mutate: openedDirect(dmA, selfID, userInA)}
	result, err := f.env(&calls).confirmed(directOpen.ID, "team", `{"user_id":"`+userInA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var opened OpenedChannel
	if err := json.Unmarshal([]byte(result), &opened); err != nil || opened.ID != dmA || opened.Type != "D" ||
		len(opened.UserIDs) != 1 || opened.UserIDs[0] != userInA {
		t.Fatalf("result = %s, %v", result, err)
	}
	if countPath(calls, http.MethodPost, "/api/v4/channels/direct") != 1 {
		t.Fatalf("calls = %+v, want exactly one open", calls)
	}
	last := calls[len(calls)-1]
	if last.path != "/api/v4/channels/direct" || last.body != `["`+selfID+`","`+userInA+`"]` {
		t.Fatalf("last call = %+v, want the open last, naming the own user and the partner", last)
	}
}

// A user only in teamB, the own user, a missing confirmation, and a connection with a channel allow-list
// are all refused before the opening POST.
func TestDirectOpenRefusesBeforeThePost(t *testing.T) {
	noOpen := func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected change %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
	var calls []call
	f := &directFixture{t: t, mutate: noOpen}
	env := f.env(&calls)
	_, err := env.confirmed(directOpen.ID, "team", `{"user_id":"`+userInB+`"}`)
	if !isInvalidRequest(err) || leaks(err) {
		t.Fatalf("err = %v, want an invalid request without the user", err)
	}
	if _, err := env.confirmed(directOpen.ID, "team", `{"user_id":"`+selfID+`"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the own user refused", err)
	}

	calls = nil
	env = f.env(&calls)
	if _, err := env.invoke(directOpen.ID, "team", `{"user_id":"`+userInA+`"}`); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want a confirmation-required refusal", err)
	}
	if _, err := env.confirmed(directOpen.ID, "channel", `{"user_id":"`+userInA+`"}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an allow-list refusal", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestOpensNeverRepeatAnUnclearResult(t *testing.T) {
	for _, tool := range []struct{ id, arguments, path string }{
		{directOpen.ID, `{"user_id":"` + userInA + `"}`, "/api/v4/channels/direct"},
		{groupMessagesOpen.ID, `{"user_ids":["` + userInA + `","` + userBoth + `"]}`, "/api/v4/channels/group"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":  func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"not json": func(*http.Request) (*http.Response, error) { return jsonResponse(201, `not json`), nil },
			"wrong pair": func(*http.Request) (*http.Response, error) {
				return jsonResponse(201, directChannelJSON(dmForeign, "D", directName(userInA, userInB))), nil
			},
			"wrong type": func(*http.Request) (*http.Response, error) {
				return jsonResponse(201, channelJSONOf(chanA, teamA, "Channel A")), nil
			},
		} {
			if name == "wrong pair" && tool.id != directOpen.ID {
				continue
			}
			var calls []call
			f := &directFixture{t: t, mutate: fail}
			_, err := f.env(&calls).confirmed(tool.id, "team", tool.arguments)
			if err == nil || !strings.Contains(err.Error(), "may have been opened") || leaks(err) {
				t.Fatalf("%s %s: err = %v", tool.id, name, err)
			}
			if countPath(calls, http.MethodPost, tool.path) != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one open", tool.id, name, calls)
			}
		}
	}
}

func openedGroup(r *http.Request) (*http.Response, error) {
	return jsonResponse(201, directChannelJSON(gmA, "G", "gmhash1")), nil
}

func TestGroupMessagesOpenProvesEveryMemberAndSendsOnePost(t *testing.T) {
	var calls []call
	f := &directFixture{t: t, mutate: openedGroup}
	env := f.env(&calls)
	// A repeated id counts once, and the own user is never named twice.
	result, err := env.confirmed(groupMessagesOpen.ID, "team",
		`{"user_ids":["`+userInA+`","`+selfID+`","`+userInA+`","`+userBoth+`"]}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var opened OpenedChannel
	if err := json.Unmarshal([]byte(result), &opened); err != nil || opened.ID != gmA || opened.Type != "G" ||
		len(opened.UserIDs) != 2 {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if countPath(calls, http.MethodPost, "/api/v4/channels/group") != 1 ||
		last.body != `["`+selfID+`","`+userInA+`","`+userBoth+`"]` {
		t.Fatalf("calls = %+v, want one open naming the own user once", calls)
	}

	f.mutate = func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected change %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
	for _, arguments := range []string{
		`{"user_ids":["` + userInA + `","` + userInB + `"]}`,       // one user outside the bound teams
		`{"user_ids":["` + userInA + `","` + selfID + `"]}`,        // only one other user
		`{"user_ids":["` + userInA + `","` + userInA + `"]}`,       // one distinct user
		`{"user_ids":["` + userInA + `","` + userBoth + `","x_"]}`, // malformed
	} {
		calls = nil
		env = f.env(&calls)
		_, err := env.confirmed(groupMessagesOpen.ID, "team", arguments)
		if err == nil || leaks(err) {
			t.Fatalf("%s: err = %v, want a refusal without a leak", arguments, err)
		}
	}
	calls = nil
	env = f.env(&calls)
	if _, err := env.confirmed(groupMessagesOpen.ID, "channel",
		`{"user_ids":["`+userInA+`","`+userBoth+`"]}`); !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want an allow-list refusal before any request", err, calls)
	}
	if _, err := env.invoke(groupMessagesOpen.ID, "team",
		`{"user_ids":["`+userInA+`","`+userBoth+`"]}`); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, want a confirmation-required refusal", err)
	}
}

func listingOf(entries ...string) string { return "[" + strings.Join(entries, ",") + "]" }

func TestDirectListKeepsOnlyProvenDirectAndGroupChannels(t *testing.T) {
	deleted := strings.Replace(directChannelJSON(dmDeleted, "D", directName(selfID, userInA)), `"delete_at":0`,
		`"delete_at":5`, 1)
	f := &directFixture{t: t, listing: listingOf(channelJSONOf(chanA, teamA, "Channel A"), fixtureChannel(dmA), fixtureChannel(dmB),
		fixtureChannel(dmForeign), deleted, fixtureChannel(gmA), fixtureChannel(gmMixed), fixtureChannel(gmBig), fixtureChannel(gmNoSelf))}
	var calls []call
	result, err := f.env(&calls).invoke(directList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page DirectPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 || page.HasMore ||
		page.Channels[0].ID != dmA || page.Channels[1].ID != gmA || page.Channels[1].Type != "G" {
		t.Fatalf("result = %s, %v", result, err)
	}
	// One batched user check for the whole page, one member read per group channel.
	if n := len(pathsOf(calls, http.MethodPost)); n != 1 {
		t.Fatalf("calls = %+v, want one batched user check, got %d", calls, n)
	}

	calls = nil
	result, err = f.env(&calls).invoke(directList.ID, "team", `{"team_id":"`+teamA+`","limit":2}`)
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 1 || !page.HasMore {
		t.Fatalf("result = %s, %v", result, err)
	}

	calls = nil
	result, err = f.env(&calls).invoke(directList.ID, "dmonly", `{"team_id":"`+teamA+`"}`)
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 1 || page.Channels[0].ID != dmA {
		t.Fatalf("result = %s, %v", result, err)
	}

	f.listing = listingOf(channelJSONOf(chanA, teamA, "Channel A"))
	calls = nil
	result, err = f.env(&calls).invoke(directList.ID, "team", `{"team_id":"`+teamA+`"}`)
	page = DirectPage{}
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 0 || len(page.Channels) != 0 {
		t.Fatalf("result = %s, %v, want an empty listing", result, err)
	}

	calls = nil
	env := f.env(&calls)
	if _, err := env.invoke(directList.ID, "team", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, want a foreign team refused before any request", err)
	}
}

// f0 is the listing entry of one fixture channel.
func fixtureChannel(id string) string { return (&directFixture{}).channels()[id] }
