package infomaniakchat

import (
	"context"
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

func channelWith(id, team, kind, extra string) string {
	return `{"id":"` + id + `","team_id":"` + team + `","type":"` + kind + `","display_name":"Chan","name":"chan-name",` +
		`"purpose":"p","header":"h","create_at":1735689600000,"delete_at":0` + extra + `}`
}

func channelServer(t *testing.T, channels map[string]string, mutate func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet && mutate != nil {
			return mutate(r)
		}
		for id, body := range channels {
			if r.URL.Path == "/api/v4/channels/"+id {
				return jsonResponse(200, body), nil
			}
			if r.URL.Path == "/api/v4/channels/"+id+"/stats" {
				return jsonResponse(200, `{"channel_id":"`+id+`","member_count":7}`), nil
			}
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestChannelsGetReadsDetailsAndBindsEveryForm(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, channelServer(t, map[string]string{chanA: channelWith(chanA, teamA, "O", "")}, nil))
	result, err := env.invoke(channelsGet.ID, "team", `{"channel_id":"`+chanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail ChannelDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail.ID != chanA || detail.Header != "h" ||
		detail.MemberCount != 7 || detail.CreatedAt != "2025-01-01T00:00:00Z" || detail.Archived {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the channel and its statistics", calls)
	}

	// By name, the team is checked locally and the answer still has to pass the allow-list.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/teams/"+teamA+"/channels/name/town-square" {
			return jsonResponse(200, channelWith(chanC, teamA, "O", "")), nil
		}
		return channelServer(t, map[string]string{chanC: channelWith(chanC, teamA, "O", "")}, nil)(r)
	})
	if _, err := env.invoke(channelsGet.ID, "team", `{"team_id":"`+teamA+`","name":"town-square"}`); err != nil {
		t.Fatalf("by name: %v", err)
	}
	calls = nil
	if _, err := env.invoke(channelsGet.ID, "channel", `{"team_id":"`+teamA+`","name":"town-square"}`); !isInvalidRequest(err) {
		t.Fatalf("by name outside the channel allow-list: err = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want only the lookup before the refusal", calls)
	}
}

func TestChannelsGetRefusesForeignTargetsAndBadFormsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	for _, c := range []struct{ connection, arguments string }{
		{"team", `{"team_id":"` + teamB + `","name":"town-square"}`},
		{"channel", `{"channel_id":"` + chanC + `"}`},
		{"team", `{}`},
		{"team", `{"team_id":"` + teamA + `"}`},
		{"team", `{"channel_id":"` + chanA + `","team_id":"` + teamA + `","name":"town-square"}`},
		{"team", `{"team_id":"` + teamA + `","name":"Bad Name"}`},
	} {
		if _, err := env.invoke(channelsGet.ID, c.connection, c.arguments); err == nil {
			t.Fatalf("%s: want a refusal", c.arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
	// A channel of a foreign team, or a direct channel, is refused after the live read, naming nothing.
	env = newEnvironment(t, &calls, channelServer(t, map[string]string{
		chanB: channelWith(chanB, teamB, "O", ""), chanA: channelWith(chanA, "", "D", "")}, nil))
	for _, id := range []string{chanB, chanA} {
		_, err := env.invoke(channelsGet.ID, "team", `{"channel_id":"`+id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "chan-name") {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}

func TestChannelsBrowseReadsOneKChatPageAndFilters(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v4/teams/"+teamA+"/channels" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, "["+channelWith(chanA, teamA, "O", "")+","+channelWith(chanC, teamA, "O", "")+","+
			channelWith(chanB, teamB, "O", "")+","+channelWith("chand000000000000000000d4", teamA, "P", "")+"]"), nil
	})
	result, err := env.invoke(channelsBrowse.ID, "team", `{"team_id":"`+teamA+`","page":3,"per_page":4}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ChannelsBrowsePage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 || !page.HasMore || page.Page != 3 {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 1 || calls[0].query.Get("page") != "2" || calls[0].query.Get("per_page") != "4" {
		t.Fatalf("calls = %+v, want one request for kChat page 2 (0-based)", calls)
	}
	result, err = env.invoke(channelsBrowse.ID, "channel", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 || page.Channels[0].ID != chanA ||
		page.HasMore {
		t.Fatalf("result = %s, %v", result, err)
	}
	calls = nil
	if _, err := env.invoke(channelsBrowse.ID, "team", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) || len(calls) != 0 {
		t.Fatalf("foreign team: err = %v, calls = %+v", err, calls)
	}
}

func TestChannelsCreateIsBoundToTeamsWithoutAllowListAndConfirmed(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	valid := `"team_id":"` + teamA + `","name":"project-x","display_name":"Project X","type":"O"`
	for name, c := range map[string]struct{ connection, arguments string }{
		"allow-list":   {"channel", `{` + valid + `}`},
		"foreign team": {"creator", `{"team_id":"` + teamB + `","name":"project-x","display_name":"X","type":"O"}`},
		"type":         {"creator", `{"team_id":"` + teamA + `","name":"project-x","display_name":"X","type":"D"}`},
		"name":         {"creator", `{"team_id":"` + teamA + `","name":"Project X","display_name":"X","type":"O"}`},
		"control":      {"creator", `{"team_id":"` + teamA + `","name":"project-x","display_name":"X\u0001","type":"O"}`},
		"header":       {"creator", `{` + valid + `,"header":"` + strings.Repeat("h", 1025) + `"}`},
	} {
		if _, err := env.confirmed(channelsCreate.ID, c.connection, c.arguments); !isInvalidRequest(err) {
			t.Fatalf("%s: err = %v, want a local invalid request", name, err)
		}
	}
	if _, err := env.invoke(channelsCreate.ID, "creator", `{`+valid+`}`); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestChannelsCreateSendsExactlyOnePostFromTypedFieldsAndNeverRetries(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/channels" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(201, channelWith(chanC, teamA, "P", "")), nil
	})
	result, err := env.confirmed(channelsCreate.ID, "creator",
		`{"team_id":"`+teamA+`","name":"project-x","display_name":"Project X","type":"P","purpose":"why"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var written ChannelWritten
	if err := json.Unmarshal([]byte(result), &written); err != nil || written.ID != chanC {
		t.Fatalf("result = %s, %v", result, err)
	}
	var body map[string]string
	if len(calls) != 1 || json.Unmarshal([]byte(calls[0].body), &body) != nil || len(body) != 5 ||
		body["team_id"] != teamA || body["type"] != "P" || body["purpose"] != "why" || body["header"] != "" {
		t.Fatalf("calls = %+v, want one POST with typed fields only", calls)
	}
	for name, fail := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		},
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"foreign answer": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, channelWith(chanB, teamB, "O", "")), nil
		},
	} {
		calls = nil
		env = newEnvironment(t, &calls, fail)
		_, err := env.confirmed(channelsCreate.ID, "creator",
			`{"team_id":"`+teamA+`","name":"project-x","display_name":"Project X","type":"O"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been created") || strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if countMethod(calls, http.MethodPost) != 1 || len(calls) != 1 {
			t.Fatalf("%s: calls = %+v, want exactly one POST", name, calls)
		}
	}
}

func TestChannelsUpdateSendsExactlyOnePutWithOnlyAllowedFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, channelServer(t, map[string]string{chanA: channelWith(chanA, teamA, "O", "")},
		func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPut || r.URL.Path != "/api/v4/channels/"+chanA+"/patch" {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(200, channelWith(chanA, teamA, "O", "")), nil
		}))
	if _, err := env.invoke(channelsUpdate.ID, "editor", `{"channel_id":"`+chanA+`","header":"x"}`); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none without confirmation", calls)
	}
	if _, err := env.confirmed(channelsUpdate.ID, "editor",
		`{"channel_id":"`+chanA+`","display_name":"New","purpose":"","header":"Hdr"}`); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var body map[string]string
	if countMethod(calls, http.MethodPut) != 1 || len(calls) != 2 ||
		json.Unmarshal([]byte(calls[1].body), &body) != nil || len(body) != 3 || body["display_name"] != "New" ||
		body["header"] != "Hdr" || body["purpose"] != "" {
		t.Fatalf("calls = %+v, want one read and one PUT with exactly the given fields", calls)
	}
}

func TestChannelsUpdateRefusesForeignDirectArchivedAndEmptyChanges(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, channelServer(t, map[string]string{
		chanA: channelWith(chanA, teamA, "D", ""), chanB: channelWith(chanB, teamB, "O", ""),
		chanC: channelWith(chanC, teamA, "O", "")}, nil))
	for _, c := range []struct{ connection, arguments string }{
		{"editor", `{"channel_id":"` + chanA + `","header":"x"}`},
		{"editor", `{"channel_id":"` + chanB + `","header":"x"}`},
	} {
		if _, err := env.confirmed(channelsUpdate.ID, c.connection, c.arguments); !isInvalidRequest(err) {
			t.Fatalf("%s: err = %v", c.arguments, err)
		}
	}
	if countMethod(calls, http.MethodPut) != 0 || len(calls) != 2 {
		t.Fatalf("calls = %+v, want only the two binding reads", calls)
	}
	calls = nil
	for _, c := range []struct{ connection, arguments string }{
		{"editorch", `{"channel_id":"` + chanC + `","header":"x"}`},
		{"editor", `{"channel_id":"` + chanC + `"}`},
		{"editor", `{"channel_id":"` + chanC + `","name":"Bad Name"}`},
	} {
		if _, err := env.confirmed(channelsUpdate.ID, c.connection, c.arguments); err == nil {
			t.Fatalf("%s: want a local refusal", c.arguments)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
}

func TestChannelsUpdateUnclearResultIsNeverRetried(t *testing.T) {
	for name, fail := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		},
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
	} {
		var calls []call
		env := newEnvironment(t, &calls, channelServer(t, map[string]string{chanA: channelWith(chanA, teamA, "O", "")}, fail))
		_, err := env.confirmed(channelsUpdate.ID, "editor", `{"channel_id":"`+chanA+`","header":"x"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been changed") || strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if countMethod(calls, http.MethodPut) != 1 {
			t.Fatalf("%s: calls = %+v, want exactly one PUT", name, calls)
		}
	}
}
