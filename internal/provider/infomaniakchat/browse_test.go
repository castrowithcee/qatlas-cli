package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseList(t *testing.T, req config.TargetListRequest, targets ...string) (config.TargetPage, error) {
	t.Helper()
	red := &redact.Redactor{}
	return registry(t).ListTargets(context.Background(), resolvedConnection(targets...), resolver(red, nil), red, req)
}

func teamNamedJSON(id, name, display string) string {
	return `{"id":"` + id + `","name":"` + name + `","display_name":"` + display + `","type":"O"}`
}

func TestTargetBrowserRegistration(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider not registered")
	}
	team, _ := meta.Target.KindByName("team")
	channel, _ := meta.Target.KindByName("channel")
	if team.Parent != "" || channel.Parent != "team" {
		t.Fatalf("parents = %q, %q", team.Parent, channel.Parent)
	}
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	if _, err := browseList(t, config.TargetListRequest{Kind: "team"}); err == config.ErrTargetBrowseUnsupported {
		t.Fatal("no target browser registered")
	}
}

func TestTeamTargetsPageAndIgnoreTargets(t *testing.T) {
	var calls []call
	var body strings.Builder
	body.WriteString("[")
	for i := 0; i < 52; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		id := "team" + strconv.Itoa(i)
		body.WriteString(teamNamedJSON(id, id, "Team "+strconv.Itoa(i)))
	}
	body.WriteString("]")
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v4/users/me/teams" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		return jsonResponse(200, body.String()), nil
	})
	// The connection is bound to teamA only; the list shows every team of the token.
	first, err := browseList(t, config.TargetListRequest{Kind: "team"}, "team/"+teamA)
	if err != nil || len(first.Entries) != 50 || first.Next != "2" || first.ServerSearch {
		t.Fatalf("first = %d entries, next %q, err %v", len(first.Entries), first.Next, err)
	}
	if first.Entries[0] != (config.TargetEntry{Target: "team/team0", Name: "Team 0", Kind: "team", HasChildren: true}) {
		t.Fatalf("entry = %+v", first.Entries[0])
	}
	second, err := browseList(t, config.TargetListRequest{Kind: "team", Cursor: "2"})
	if err != nil || len(second.Entries) != 2 || second.Next != "" {
		t.Fatalf("second = %d entries, next %q, err %v", len(second.Entries), second.Next, err)
	}
	for _, c := range calls {
		if c.method != http.MethodGet || c.host != "acme-support.kchat.infomaniak.com" {
			t.Fatalf("call = %+v", c)
		}
	}
}

func TestChannelTargetsListFilterAndNameAsText(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v4/users/me/teams/"+teamB+"/channels" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		return jsonResponse(200, `[`+
			`{"id":"`+chanB+`","team_id":"`+teamB+`","type":"P","display_name":"Ops\u0007 <b>x</b>","name":"ops","delete_at":0},`+
			`{"id":"`+chanA+`","team_id":"`+teamA+`","type":"O","display_name":"other team","name":"o","delete_at":0},`+
			`{"id":"`+chanC+`","team_id":"`+teamB+`","type":"O","display_name":"gone","name":"g","delete_at":5},`+
			`{"id":"dm1","team_id":"","type":"D","display_name":"","name":"x__y","delete_at":0},`+
			`{"id":"bad id","team_id":"`+teamB+`","type":"O","display_name":"bad","name":"b","delete_at":0},`+
			`{"id":"chand000000000000000000d4","team_id":"`+teamB+`","type":"O","display_name":"","name":"town-square","delete_at":0}]`), nil
	})
	page, err := browseList(t, config.TargetListRequest{Kind: "channel", Parent: "team/" + teamB})
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TargetEntry{
		{Target: "channel/" + chanB, Name: "Ops <b>x</b>", Kind: "channel"},
		{Target: "channel/chand000000000000000000d4", Name: "town-square", Kind: "channel"},
	}
	if !reflect.DeepEqual(page.Entries, want) || page.Next != "" || page.ServerSearch {
		t.Fatalf("page = %+v", page)
	}
}

func TestChannelTargetsSearchUsesTheProvidersSearch(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams/" + teamA + "/channels":
			return jsonResponse(200, `[`+channelJSONOf(chanA, teamA, "Support")+`,`+channelJSONOf(chanC, teamA, "Sales")+`]`), nil
		case "/api/v4/teams/" + teamA + "/channels/search":
			return jsonResponse(200, `[`+channelJSONOf(chanA, teamA, "Support")+`,`+channelJSONOf("chane000000000000000000e5", teamA, "Not joined")+`]`), nil
		}
		t.Fatalf("unexpected %s", r.URL.Path)
		return nil, nil
	})
	page, err := browseList(t, config.TargetListRequest{Kind: "channel", Parent: "team/" + teamA, Query: "supp"})
	if err != nil || !page.ServerSearch || page.Next != "" ||
		!reflect.DeepEqual(page.Entries, []config.TargetEntry{{Target: "channel/" + chanA, Name: "Support", Kind: "channel"}}) {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	var search call
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/channels/search") {
			search = c
		}
	}
	var body struct{ Term string }
	if json.Unmarshal([]byte(search.body), &body); body.Term != "supp" {
		t.Fatalf("search body = %q", search.body)
	}
}

func TestTargetListErrors(t *testing.T) {
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	for _, req := range []config.TargetListRequest{
		{Kind: "nope"},
		{Kind: "team", Cursor: "0"},
		{Kind: "team", Cursor: "x"},
		{Kind: "team", Cursor: "01"},
		{Kind: "channel"},
		{Kind: "channel", Parent: "channel/" + chanA},
		{Kind: "channel", Parent: "team/UPPER"},
		{Kind: "channel", Parent: "team/" + teamA, Query: "x", Cursor: "2"},
		{Kind: "channel", Parent: "team/" + teamA, Query: "a\x00b"},
	} {
		if _, err := browseList(t, req); !isInvalidRequest(err) {
			t.Fatalf("%+v: err = %v, want invalid request", req, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("invalid requests sent %d requests", len(calls))
	}
	// A failed read is an error, with no value quoted.
	serve(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(500, `{"message":"secret-detail"}`), nil
	})
	_, err := browseList(t, config.TargetListRequest{Kind: "team"})
	if err == nil || strings.Contains(err.Error(), "secret-detail") || strings.Contains(err.Error(), tokenValue) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveTargetURL(t *testing.T) {
	const host = "https://acme-support.kchat.infomaniak.com"
	team := config.TargetEntry{Target: "team/" + teamA, Name: "Acme Support", Kind: "team", HasChildren: true}
	channel := config.TargetEntry{Target: "channel/" + chanA, Name: "Support", Kind: "channel"}
	cases := []struct {
		name, url string
		want      []config.TargetEntry
		reads     int
	}{
		{"channel url", host + "/acme/channels/support", []config.TargetEntry{team, channel}, 2},
		{"trailing slash and upper host", "https://ACME-SUPPORT.kchat.infomaniak.com/acme/channels/support/", []config.TargetEntry{team, channel}, 2},
		{"query ignored", host + "/acme/channels/support?x=1#y", []config.TargetEntry{team, channel}, 2},
		{"unknown channel", host + "/acme/channels/missing", nil, 2},
		{"unknown team", host + "/other/channels/support", nil, 1},
		{"other instance", "https://other.kchat.infomaniak.com/acme/channels/support", nil, 0},
		{"foreign host", "https://example.invalid/acme/channels/support", nil, 0},
		{"lookalike host", "https://acme-support.kchat.infomaniak.com.example.invalid/acme/channels/support", nil, 0},
		{"subdomain of instance", "https://x.acme-support.kchat.infomaniak.com/acme/channels/support", nil, 0},
		{"http", "http://acme-support.kchat.infomaniak.com/acme/channels/support", nil, 0},
		{"port", host + ":8443/acme/channels/support", nil, 0},
		{"no channel", host + "/acme/channels", nil, 0},
		{"team only", host + "/acme", nil, 0},
		{"no team", host + "/channels/support", nil, 0},
		{"permalink", host + "/acme/pl/abc", nil, 0},
		{"direct message", host + "/acme/messages/@someone", nil, 0},
		{"extra segment", host + "/acme/channels/support/threads", nil, 0},
		{"bad channel name", host + "/acme/channels/Support", nil, 0},
		{"bad team name", host + "/ac.me/channels/support", nil, 0},
		{"invalid url", host + "/%zz", nil, 0},
		{"empty", "", nil, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			serve(t, &calls, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/v4/users/me/teams":
					return jsonResponse(200, `[`+teamNamedJSON(teamB, "beta", "Beta")+`,`+teamNamedJSON(teamA, "acme", "Acme Support")+`]`), nil
				case "/api/v4/teams/" + teamA + "/channels/name/support":
					return jsonResponse(200, `{"id":"`+chanA+`","team_id":"`+teamA+`","type":"O","display_name":"Support","name":"support","delete_at":0}`), nil
				case "/api/v4/teams/" + teamA + "/channels/name/missing":
					return jsonResponse(404, `{}`), nil
				}
				t.Fatalf("unexpected %s", r.URL.Path)
				return nil, nil
			})
			red := &redact.Redactor{}
			got, err := registry(t).ResolveTargetURL(context.Background(), resolvedConnection(), resolver(red, nil), red, origin, tt.url)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
			if len(calls) != tt.reads {
				t.Fatalf("reads = %d, want %d", len(calls), tt.reads)
			}
		})
	}
}

func TestResolveTargetURLRefusesWrongChannels(t *testing.T) {
	for name, channel := range map[string]string{
		"other team": `{"id":"` + chanB + `","team_id":"` + teamB + `","type":"O","name":"support","delete_at":0}`,
		"archived":   `{"id":"` + chanA + `","team_id":"` + teamA + `","type":"O","name":"support","delete_at":9}`,
		"direct":     `{"id":"` + chanA + `","team_id":"","type":"D","name":"support","delete_at":0}`,
		"bad id":     `{"id":"A B","team_id":"` + teamA + `","type":"O","name":"support","delete_at":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			serve(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v4/users/me/teams" {
					return jsonResponse(200, `[`+teamNamedJSON(teamA, "acme", "Acme")+`]`), nil
				}
				return jsonResponse(200, channel), nil
			})
			red := &redact.Redactor{}
			got, err := registry(t).ResolveTargetURL(context.Background(), resolvedConnection(), resolver(red, nil), red, origin,
				origin+"/acme/channels/support")
			if err != nil || got != nil {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
	// An unusable answer is an error, not an empty result.
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(500, `{}`), nil })
	red := &redact.Redactor{}
	if _, err := registry(t).ResolveTargetURL(context.Background(), resolvedConnection(), resolver(red, nil), red, origin,
		origin+"/acme/channels/support"); err == nil {
		t.Fatal("a failing read must be an error")
	}
}

func TestReachesWithChannelTargets(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	withTeams := []string{"team/" + teamA}
	withChannel := []string{"team/" + teamA, "channel/" + chanA}
	teamWide := []string{botsList.ID, botsGet.ID, botsUpdate.ID, botsDisable.ID, botsEnable.ID, emojiList.ID,
		emojiCreate.ID, emojiDelete.ID, rolesGet.ID, channelsCreate.ID, commandsList.ID, commandsGet.ID,
		commandsUpdate.ID, commandsDelete.ID, directOpen.ID, groupMessagesOpen.ID}
	isTeamWide := map[string]bool{}
	for _, id := range teamWide {
		isTeamWide[id] = true
	}
	if len(meta.Tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range meta.Tools {
		if reached, _ := meta.Target.Reach(tool.ID, withTeams); !reached {
			t.Fatalf("%s must be reached by a team binding", tool.ID)
		}
		if reached, _ := meta.Target.Reach(tool.ID, withChannel); reached == isTeamWide[tool.ID] {
			t.Fatalf("%s: reached = %v with a channel target", tool.ID, reached)
		}
	}
}
