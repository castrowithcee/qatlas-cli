package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// channels.list refuses a team outside this connection's bound teams before any request is sent, and pages
// the live, allow-list-filtered result of a bound team itself.
func TestChannelsListRefusesForeignTeamAndPaginatesTheBoundTeam(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.invoke(channelsList.ID, "team", `{"team_id":"`+teamB+`"}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a team outside this connection", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the foreign team was refused", calls)
	}
	if *env.reads != 0 {
		t.Fatalf("secret reads = %d, want none for a refused team_id", *env.reads)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels" {
			return jsonResponse(200, "["+channelJSONOf(chanA, teamA, "Channel A")+","+
				channelJSONOf(chanC, teamA, "Channel C")+"]"), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.invoke(channelsList.ID, "team", `{"team_id":"`+teamA+`","limit":1,"page":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ChannelsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if page.Total != 2 || page.Pages != 2 || page.Count != 1 || len(page.Channels) != 1 || page.Channels[0].ID != chanC {
		t.Fatalf("page = %+v, want the second page of two channels", page)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one live read", calls)
	}

	// The "channel" connection narrows the same team to chanA alone.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, "["+channelJSONOf(chanA, teamA, "Channel A")+","+
			channelJSONOf(chanC, teamA, "Channel C")+"]"), nil
	})
	result, err = env.invoke(channelsList.ID, "channel", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if page.Total != 1 || len(page.Channels) != 1 || page.Channels[0].ID != chanA {
		t.Fatalf("page = %+v, want only chanA once the channel allow-list narrows it", page)
	}
	if err != nil && strings.Contains(err.Error(), messageCanary) {
		t.Fatalf("error leaked provider content: %v", err)
	}
}
