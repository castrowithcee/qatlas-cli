package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// newExcludingEnvironment is newEnvironment with the given exclusions on every connection of coreConfig.
func newExcludingEnvironment(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error),
	excluded ...string) *environment {
	t.Helper()
	serve(t, calls, handler)
	cfg := coreConfig()
	for name, conn := range cfg.Connections {
		conn.Exclude = excluded
		cfg.Connections[name] = conn
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func excludeChannel(id string) string { return "channel/" + id }

func TestExclusionsAreDeclaredAndParsedFailClosedWithoutQuotingValues(t *testing.T) {
	metadata, ok := registry(t).ProviderMetadata(Provider)
	if !ok || !metadata.Target.Excludes() {
		t.Fatalf("metadata = %+v, want declared exclusions", metadata)
	}
	for _, k := range metadata.Target.Kinds {
		if k.Name == "channel" != k.Excludable {
			t.Fatalf("kind %s Excludable = %t, want only channel excludable", k.Name, k.Excludable)
		}
	}
	targets := []string{"team/" + teamA, "channel/" + chanA}
	for _, tt := range []struct {
		name     string
		excludes []string
		wantErr  bool
	}{
		{"channel", []string{excludeChannel(chanC)}, false},
		{"two", []string{excludeChannel(chanC), excludeChannel(dmA)}, false},
		{"team", []string{"team/" + teamA}, true},
		{"equals target", []string{excludeChannel(chanA)}, true},
		{"duplicate", []string{excludeChannel(chanC), excludeChannel(chanC)}, true},
		{"wildcard", []string{"channel/*"}, true},
		{"empty", []string{""}, true},
		{"malformed", []string{"channel/../" + chanC}, true},
		{"padded", []string{" " + excludeChannel(chanC)}, true},
	} {
		_, err := parseScope(targets, tt.excludes...)
		if (err != nil) != tt.wantErr {
			t.Fatalf("%s: parseScope() = %v, wantErr=%t", tt.name, err, tt.wantErr)
		}
		if err != nil && (strings.Contains(err.Error(), chanC) || strings.Contains(err.Error(), teamA)) {
			t.Fatalf("%s: error quotes a configured value: %v", tt.name, err)
		}
	}

	var calls []call
	env := newExcludingEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}, "team/"+teamA)
	_, err := env.invoke(channelsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err == nil || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), teamA) {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a fail-closed refusal without secret or request", err, calls,
			*env.reads)
	}
}

func TestExclusionsNeverRemoveInstanceOrTeamBoundTools(t *testing.T) {
	resolved := resolvedConnection("team/" + teamA)
	resolved.Exclude = []string{excludeChannel(chanC)}
	if err := requireInstanceScope(resolved, "bots"); err != nil {
		t.Fatalf("requireInstanceScope() = %v, want an exclusion to leave instance tools alone", err)
	}
	if err := requireTeamWide(resolved); err != nil {
		t.Fatalf("requireTeamWide() = %v", err)
	}
	if err := refuseWithAllowList(resolved); err != nil {
		t.Fatalf("refuseWithAllowList() = %v", err)
	}
	var calls []call
	env := newExcludingEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/users/me/teams/"+teamA+"/channels" {
			return jsonResponse(200, "[]"), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}, excludeChannel(chanC))
	if _, err := env.invoke(channelsList.ID, "creator", `{"team_id":"`+teamA+`"}`); err != nil {
		t.Fatalf("invoke() = %v, want the read tools offered with exclusions", err)
	}
}

func TestExcludedChannelIDIsRefusedBeforeTheSecretAndWithoutRequest(t *testing.T) {
	for _, tt := range []struct{ operation, connection, args string }{
		{messagesList.ID, "team", `{"channel_id":"` + chanC + `"}`},
		{channelsGet.ID, "team", `{"channel_id":"` + chanC + `"}`},
		{flaggedList.ID, "team", `{"channel_id":"` + chanC + `"}`},
		{messagesSend.ID, "team", `{"channel_id":"` + chanC + `","text":"hi"}`},
		{outgoingWebhooksList.ID, "hooker", `{"team_id":"` + teamA + `","channel_id":"` + chanC + `"}`},
	} {
		var calls []call
		env := newExcludingEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}, excludeChannel(chanC))
		_, err := env.confirmed(tt.operation, tt.connection, tt.args)
		if !isInvalidRequest(err) || *env.reads != 0 || len(calls) != 0 || strings.Contains(err.Error(), chanC) {
			t.Fatalf("%s: err = %v, reads = %d, calls = %+v, want a local refusal naming nothing", tt.operation, err,
				*env.reads, calls)
		}
	}
}

func TestPostBoundToolsRefuseAnExcludedChannelAfterTheProofRead(t *testing.T) {
	for _, tt := range []struct{ operation, connection, args string }{
		{messagesGet.ID, "team", `{"post_id":"` + postInChanC + `"}`},
		{messagesFlag.ID, "editor", `{"post_id":"` + postInChanC + `"}`},
		{messagesUpdate.ID, "editor", `{"post_id":"` + postInChanC + `","text":"x"}`},
		{reactionsAdd.ID, "reactor", `{"post_id":"` + postInChanC + `","emoji_name":"smile"}`},
	} {
		var calls []call
		env := newExcludingEnvironment(t, &calls, postServer(t, nil), excludeChannel(chanC))
		_, err := env.confirmed(tt.operation, tt.connection, tt.args)
		if !isInvalidRequest(err) || changes(calls) != 0 || strings.Contains(err.Error(), chanC) {
			t.Fatalf("%s: err = %v, calls = %+v, want a refusal after the post read and no change", tt.operation, err, calls)
		}
		if len(calls) != 1 || calls[0].path != "/api/v4/posts/"+postInChanC {
			t.Fatalf("%s: calls = %+v, want only the post read", tt.operation, calls)
		}
	}
}

func TestListsLeaveExcludedChannelsOut(t *testing.T) {
	var calls []call
	search := searchServer(t, searchPaths())
	env := newExcludingEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/teams/"+teamA+"/channels" {
			return jsonResponse(200, "["+channelJSONOf(chanA, teamA, "Channel A")+","+channelJSONOf(chanC, teamA,
				"Channel C")+"]"), nil
		}
		return search(r)
	}, excludeChannel(chanC))
	for _, tt := range []struct {
		operation, args string
		keep, drop      string
	}{
		{messagesSearch.ID, `{"team_id":"` + teamA + `","terms":"` + searchTerm + `"}`, hitInChanA, hitInChanC},
		{filesSearch.ID, `{"team_id":"` + teamA + `","terms":"ext:pdf"}`, hitFileA, hitFileC},
		{channelsSearch.ID, `{"team_id":"` + teamA + `","term":"chan"}`, chanA, chanC},
		{channelsList.ID, `{"team_id":"` + teamA + `"}`, chanA, chanC},
		{channelsBrowse.ID, `{"team_id":"` + teamA + `"}`, chanA, chanC},
	} {
		result, err := env.invoke(tt.operation, "team", tt.args)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", tt.operation, err)
		}
		if !strings.Contains(result, tt.keep) || strings.Contains(result, tt.drop) {
			t.Fatalf("%s: result = %s, want %s kept and %s dropped", tt.operation, result, tt.keep, tt.drop)
		}
	}
}

func TestArchivedFlaggedThreadsAndInsightsLeaveExcludedChannelsOut(t *testing.T) {
	f := newArchiveFixture(t, nil)
	f.listing = "[" + strings.Join([]string{channelWith(chanE, teamA, "O", archivedAt),
		channelWith(chanC, teamA, "P", archivedAt)}, ",") + "]"
	var calls []call
	result, err := newExcludingEnvironment(t, &calls, f.handle, excludeChannel(chanC)).invoke(archivedChannelsList.ID,
		"team", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, chanE) || strings.Contains(result, chanC) {
		t.Fatalf("archived: result = %s, %v", result, err)
	}

	calls = nil
	result, err = newExcludingEnvironment(t, &calls, flaggedServer(t, flaggedSingle()), excludeChannel(chanC)).invoke(
		flaggedList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, flagPostA) || strings.Contains(result, flagPostA2) {
		t.Fatalf("flagged: result = %s, %v", result, err)
	}

	calls = nil
	list := `{"threads":[` + threadJSONOf(postInChanA, chanA, "") + `,` + threadJSONOf(postInChanC, chanC, "") + `],"total":2}`
	result, err = newExcludingEnvironment(t, &calls, threadServer(t, list, nil), excludeChannel(chanC)).invoke(
		threadsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, postInChanA) || strings.Contains(result, chanC) {
		t.Fatalf("threads: result = %s, %v", result, err)
	}

	calls = nil
	env := newExcludingEnvironment(t, &calls, insightsServer(t, 200,
		`{"has_next":false,"items":[`+channelItem(chanA)+`,`+channelItem(chanC)+`]}`), excludeChannel(chanC))
	result, err = env.invoke(insightsTop.ID, "team", `{"team_id":"`+teamA+`","kind":"channels"}`)
	if err != nil || !strings.Contains(result, chanA) || strings.Contains(result, chanC) {
		t.Fatalf("insights: result = %s, %v", result, err)
	}
	if !strings.Contains(insightsTop.Description, "excluded channels") {
		t.Fatalf("insights description = %q, want the reaction warning", insightsTop.Description)
	}
}

func TestDirectListAndOpensRefuseExcludedResults(t *testing.T) {
	f := &directFixture{t: t, listing: listingOf(fixtureChannel(dmA), fixtureChannel(gmA))}
	var calls []call
	env := newExcludingEnvironment(t, &calls, f.handle, excludeChannel(dmA))
	result, err := env.invoke(directList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || strings.Contains(result, dmA) || !strings.Contains(result, gmA) {
		t.Fatalf("list: result = %s, %v", result, err)
	}

	f = &directFixture{t: t, mutate: openedDirect(dmA, selfID, userInA)}
	calls = nil
	_, err = newExcludingEnvironment(t, &calls, f.handle, excludeChannel(dmA)).confirmed(directOpen.ID, "team",
		`{"user_id":"`+userInA+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), dmA) {
		t.Fatalf("direct.open: err = %v, want a refusal naming nothing", err)
	}

	f = &directFixture{t: t, mutate: openedGroup}
	calls = nil
	_, err = newExcludingEnvironment(t, &calls, f.handle, excludeChannel(gmA)).confirmed(groupMessagesOpen.ID, "team",
		`{"user_ids":["`+userInA+`","`+userBoth+`"]}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), gmA) {
		t.Fatalf("group-messages.open: err = %v, want a refusal naming nothing", err)
	}

	f = &directFixture{t: t, mutate: openedDirect(dmA, selfID, userInA)}
	calls = nil
	result, err = newExcludingEnvironment(t, &calls, f.handle, excludeChannel(gmA)).confirmed(directOpen.ID, "team",
		`{"user_id":"`+userInA+`"}`)
	if err != nil || !strings.Contains(result, dmA) {
		t.Fatalf("direct.open of another channel: result = %s, %v", result, err)
	}
}

func TestCategoriesHideExcludedChannelsAndRefuseThemOnChange(t *testing.T) {
	var put string
	f := newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, put), nil
	})
	var calls []call
	env := newExcludingEnvironment(t, &calls, f.handle, excludeChannel(chanC))
	result, err := env.invoke(categoriesList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || strings.Contains(result, chanC) || !strings.Contains(result, chanA) {
		t.Fatalf("list: result = %s, %v", result, err)
	}

	for _, tt := range []struct{ operation, connection, args string }{
		{categoriesCreate.ID, "creator", `{"team_id":"` + teamA + `","display_name":"N","channel_ids":["` + chanC + `"]}`},
		{categoriesUpdate.ID, "editor", `{"team_id":"` + teamA + `","category_id":"` + catCustom + `","channel_ids":["` + chanC + `"]}`},
	} {
		calls, *env.reads = nil, 0
		_, err := env.confirmed(tt.operation, tt.connection, tt.args)
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), chanC) {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a local refusal", tt.operation, err, calls, *env.reads)
		}
	}

	// An update that does not name the excluded channel writes it back unchanged.
	put = categoryWith(catFavorites, ownUser, teamA, "Renamed", "favorites", chanC, chanB)
	calls = nil
	result, err = env.confirmed(categoriesUpdate.ID, "editor", `{"team_id":"`+teamA+`","category_id":"`+catFavorites+
		`","channel_ids":["`+chanA+`"]}`)
	if err != nil || strings.Contains(result, chanC) {
		t.Fatalf("update: result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodPut || !strings.Contains(last.body, chanC) || !strings.Contains(last.body, chanA) {
		t.Fatalf("last call = %+v, want a PUT that keeps the excluded channel", last)
	}
}

func TestIncomingWebhooksOfExcludedChannelsAreFilteredAndRefused(t *testing.T) {
	f := newHookFixture(t, nil)
	f.listing = "[" + hookWith(hookA, teamA, chanA, "") + "," + hookWith(hookD, teamA, chanC, "") + "]"
	var calls []call
	env := newExcludingEnvironment(t, &calls, f.handle, excludeChannel(chanC))
	result, err := env.invoke(incomingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, hookA) || strings.Contains(result, hookD) {
		t.Fatalf("list: result = %s, %v", result, err)
	}
	calls = nil
	_, err = env.invoke(incomingWebhooksGet.ID, "hooker", `{"hook_id":"`+hookD+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), chanC) || len(calls) != 1 {
		t.Fatalf("get: err = %v, calls = %+v, want a refusal after the hook read", err, calls)
	}
}

func TestOutgoingWebhooksOfExcludedChannelsAndChannellessHooks(t *testing.T) {
	f := newOutgoingFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			return jsonResponse(200, `{"status":"OK"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	f.listing = "[" + ohookWith(ohookA, teamA, chanA, "") + "," + ohookWith(ohookD, teamA, chanC, "") + "," +
		ohookWith(ohookN, teamA, "", "") + "]"
	var calls []call
	env := newExcludingEnvironment(t, &calls, f.handle, excludeChannel(chanC))

	result, err := env.invoke(outgoingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, ohookA) || !strings.Contains(result, ohookN) ||
		strings.Contains(result, ohookD) {
		t.Fatalf("list: result = %s, %v", result, err)
	}
	if _, err := env.invoke(outgoingWebhooksGet.ID, "hooker", `{"hook_id":"`+ohookN+`"}`); err != nil {
		t.Fatalf("get of a channelless hook = %v, want it allowed", err)
	}
	for _, operation := range []string{outgoingWebhooksGet.ID, outgoingWebhooksUpdate.ID, outgoingWebhooksDelete.ID} {
		_, err := env.confirmed(operation, "hooker", `{"hook_id":"`+ohookD+`","description":"x"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), chanC) {
			t.Fatalf("%s of an excluded channel's hook: err = %v", operation, err)
		}
	}

	calls = nil
	_, err = env.confirmed(outgoingWebhooksUpdate.ID, "hooker", `{"hook_id":"`+ohookN+`","description":"x"}`)
	if !isInvalidRequest(err) || changeCount(calls) != 0 {
		t.Fatalf("update of a channelless hook: err = %v, calls = %+v, want a refusal before any change", err, calls)
	}
	calls = nil
	if _, err := env.confirmed(outgoingWebhooksDelete.ID, "hooker", `{"hook_id":"`+ohookN+`"}`); err != nil ||
		changeCount(calls) != 1 {
		t.Fatalf("delete of a channelless hook: err = %v, calls = %+v, want it allowed", err, calls)
	}
}

func TestPermalinkEmbedOfAnExcludedChannelIsRemoved(t *testing.T) {
	list := postListJSONOf([]string{previewPost}, []string{previewMessage(previewPost, chanA)}, false, "")
	var calls []call
	env := newExcludingEnvironment(t, &calls, previewServer(t, list, func(r *http.Request) (*http.Response, bool) {
		if r.URL.Path == "/api/v4/channels/"+chanA+"/posts" {
			return jsonResponse(200, list), true
		}
		if r.URL.Path == "/api/v4/channels/"+chanA {
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), true
		}
		return nil, false
	}), excludeChannel(chanC))
	result, err := env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if pathCount(calls, "/api/v4/channels/"+chanC) != 0 {
		t.Fatalf("calls = %+v, want no probe of an excluded channel", calls)
	}
	for _, leaked := range []string{previewLeakC, "kept-c", chanC} {
		if strings.Contains(result, leaked) {
			t.Fatalf("result leaks %q: %s", leaked, result)
		}
	}
	if !strings.Contains(result, "kept-preview") {
		t.Fatalf("result = %s, want the preview of an allowed channel kept", result)
	}
	var decoded any
	if json.Unmarshal([]byte(result), &decoded) != nil {
		t.Fatalf("result is not JSON: %s", result)
	}
}
