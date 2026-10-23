package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const insightPostA = "post0000000000000000001"

// legacyBot is a bot as the API sent it before the times were read.
func legacyBot(id string) string {
	return `{"user_id":"` + id + `","username":"deploy","display_name":"Deploy","description":"old",` +
		`"owner_id":"owner0000000000000000000a","delete_at":0}`
}

func legacyBotHandler(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams":
			return jsonResponse(200, bothTeams), nil
		case "/api/v4/bots":
			return jsonResponse(200, "["+legacyBot(botA)+"]"), nil
		case "/api/v4/bots/" + botA:
			return jsonResponse(200, legacyBot(botA)), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func legacyEmojiHandler(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams":
			return jsonResponse(200, bothTeams), nil
		case "/api/v4/emoji":
			return jsonResponse(200, `[{"id":"`+emojiA+`","name":"party","creator_id":"owner0000000000000000000a",`+
				`"create_at":1735689600000}]`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func stripCreator(body string) string {
	return strings.Replace(strings.Replace(body, `,"creator_id":"u1"`, "", 1), `"creator_id":"u1",`, "", 1)
}

// legacyOutputs runs the tools of the integration and insight set against answers without any field that was
// added later and returns the results by tool.
func legacyOutputs(t *testing.T) map[string]string {
	out := map[string]string{}
	run := func(name string, env *environment, id, connection, args string) {
		t.Helper()
		result, err := env.invoke(id, connection, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = result
	}
	var calls []call
	env := newEnvironment(t, &calls, legacyEmojiHandler(t))
	run("emoji.list", env, emojiList.ID, "botall", `{}`)
	env = newEnvironment(t, &calls, legacyBotHandler(t))
	run("bots.list", env, botsList.ID, "botall", `{}`)
	run("bots.get", env, botsGet.ID, "botall", `{"bot_user_id":"`+botA+`"}`)

	hooks := newOutgoingFixture(t, nil)
	hooks.hooks[ohookA] = stripCreator(hooks.hooks[ohookA])
	hooks.listing = "[" + hooks.hooks[ohookA] + "]"
	env = hooks.env(&calls)
	run("outgoing-webhooks.list", env, outgoingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`"}`)
	run("outgoing-webhooks.get", env, outgoingWebhooksGet.ID, "hooker", `{"hook_id":"`+ohookA+`"}`)

	commands := &commandFixture{t: t, mutate: func(r *http.Request) (*http.Response, error) { return nil, nil }}
	commands.listing = "[" + stripCreator(commandWith(cmdA, teamA)) + "]"
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/commands/"+cmdA {
			return jsonResponse(200, stripCreator(commandWith(cmdA, teamA))), nil
		}
		return commands.handle(r)
	})
	run("commands.list", env, commandsList.ID, "cmder", `{"team_id":"`+teamA+`"}`)
	run("commands.get", env, commandsGet.ID, "cmder", `{"command_id":"`+cmdA+`"}`)

	env = newEnvironment(t, &calls, insightsServer(t, 200, `{"has_next":false,"items":[{"id":"`+chanA+
		`","type":"O","display_name":"Name","name":"n","team_id":"`+teamA+`","message_count":7}]}`))
	run("insights.channels", env, insightsTop.ID, "team", `{"team_id":"`+teamA+`","kind":"channels"}`)
	env = newEnvironment(t, &calls, insightsServer(t, 200, `{"items":[{"emoji_name":"smile","count":9}]}`))
	run("insights.reactions", env, insightsTop.ID, "team", `{"team_id":"`+teamA+`","kind":"reactions"}`)
	return out
}

// legacyGolden is the output of these tools before the fields of the API were read in full, member order as
// the core writes it.
var legacyGolden = map[string]string{
	"emoji.list": `{"count":1,"emoji":[{"created_at":"2025-01-01T00:00:00Z","creator_id":"owner0000000000000000000a",` +
		`"id":"emja0000000000000000000a1","name":"party"}],"has_more":false,"page":1,"per_page":50}`,
	"bots.list": `{"bots":[{"deleted":false,"description":"old","display_name":"Deploy",` +
		`"owner_id":"owner0000000000000000000a","user_id":"bota0000000000000000000a1","username":"deploy"}],` +
		`"count":1,"has_more":false,"page":1,"per_page":50}`,
	"bots.get": `{"deleted":false,"description":"old","display_name":"Deploy","owner_id":"owner0000000000000000000a",` +
		`"user_id":"bota0000000000000000000a1","username":"deploy"}`,
	"outgoing-webhooks.list": `{"count":1,"has_more":false,"page":1,"per_page":50,"team_id":"teama0000000000000000001a",` +
		`"webhooks":[` + legacyHook(`"id":"ohooka00000000000000000a1",`) + `]}`,
	"outgoing-webhooks.get": legacyHook(``),
	"commands.list": `{"commands":[` + legacyCommand(`"id":"cmda0000000000000000000a1",`) + `],"count":1,` +
		`"page":1,"pages":1,"team_id":"teama0000000000000000001a","total":1}`,
	"commands.get": legacyCommand(``),
	"insights.channels": `{"channels":[{"channel_id":"chana000000000000000000a1","display_name":"Name",` +
		`"message_count":7,"name":"n","type":"O"}],"count":1,"has_more":false,"kind":"channels","page":1,` +
		`"team_id":"teama0000000000000000001a","time_range":"today"}`,
	"insights.reactions": `{"count":1,"has_more":false,"kind":"reactions","page":1,"reactions":[{"count":9,` +
		`"emoji_name":"smile"}],"team_id":"teama0000000000000000001a","time_range":"today"}`,
}

func legacyHook(id string) string {
	return `{"callback_origins":["https://hooks.example.invalid:8443","http://plain.example.invalid"],` +
		`"channel_id":"chana000000000000000000a1","content_type":"application/json","created_at":"2025-01-01T00:00:00Z",` +
		`"description":"old text","display_name":"Alerts","icon_url":"https://example.invalid/i.png",` + id +
		`"team_id":"teama0000000000000000001a","trigger_when":1,"trigger_words":["deploy","ship it"],` +
		`"updated_at":"2025-01-01T00:01:40Z","username":"bot-name"}`
}

func legacyCommand(id string) string {
	return `{"auto_complete":true,"auto_complete_desc":"old desc","auto_complete_hint":"[env]",` +
		`"callback_origin":"https://cmd.example.invalid:8443","created_at":"2025-01-01T00:00:00Z",` +
		`"description":"old text","display_name":"Deploy","icon_url":"https://example.invalid/i.png",` + id +
		`"method":"P","team_id":"teama0000000000000000001a","trigger":"deploy","updated_at":"2025-01-01T00:01:40Z",` +
		`"username":"bot-name"}`
}

func TestAnswersWithoutNewFieldsKeepTheFormerIntegrationAndInsightOutputByteForByte(t *testing.T) {
	for name, got := range legacyOutputs(t) {
		if got != legacyGolden[name] {
			t.Errorf("%s\n got %s\nwant %s", name, got, legacyGolden[name])
		}
	}
}

func TestEmojiListSendsTheSortAndReadsTheNewTimes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, emojiHandler(t, bothTeams))
	for _, tt := range []struct{ args, sort string }{{`{}`, "name"}, {`{"sort":"name"}`, "name"}, {`{"sort":"none"}`, ""}} {
		calls = nil
		result, err := env.invoke(emojiList.ID, "botall", tt.args)
		if err != nil || len(calls) != 2 || !calls[1].query.Has("sort") || calls[1].query.Get("sort") != tt.sort ||
			!strings.Contains(result, `"updated_at":"1970-01-01T00:00:00Z"`) {
			t.Fatalf("%s: result = %s, err = %v, calls = %+v", tt.args, result, err, calls)
		}
	}
	calls = nil
	if _, err := env.invoke(emojiList.ID, "botall", `{"sort":"created"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal before any request", err, calls)
	}
	deleted := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/emoji" {
			return jsonResponse(200, `[{"id":"`+emojiA+`","name":"gone","creator_id":"u1","create_at":1735689600000,`+
				`"update_at":1735689700000,"delete_at":1735689800000}]`), nil
		}
		return jsonResponse(200, bothTeams), nil
	})
	result, err := deleted.invoke(emojiList.ID, "botall", `{}`)
	if err != nil || !strings.Contains(result, `"updated_at":"2025-01-01T00:01:40Z"`) ||
		!strings.Contains(result, `"deleted_at":"2025-01-01T00:03:20Z"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestBotsSendTheDeletedAndOrphanedFiltersAndReadTheTimes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
	result, err := env.invoke(botsList.ID, "botall", `{"include_deleted":true,"only_orphaned":true}`)
	if err != nil || calls[1].query.Get("include_deleted") != "true" || calls[1].query.Get("only_orphaned") != "true" {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
	calls = nil
	if _, err = env.invoke(botsList.ID, "botall", `{"include_deleted":true}`); err != nil ||
		calls[1].query.Get("include_deleted") != "true" || calls[1].query.Has("only_orphaned") {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	calls = nil
	if _, err = env.invoke(botsList.ID, "botall", `{}`); err != nil || calls[1].query.Has("include_deleted") ||
		calls[1].query.Has("only_orphaned") {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	calls = nil
	result, err = env.invoke(botsGet.ID, "botall", `{"bot_user_id":"`+botA+`","include_deleted":true}`)
	if err != nil || calls[1].query.Get("include_deleted") != "true" || !strings.Contains(result, `"created_at"`) {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
	calls = nil
	if _, err = env.invoke(botsGet.ID, "botall", `{"bot_user_id":"`+botA+`"}`); err != nil || calls[1].query.Has("include_deleted") {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	base := botHandler(t, bothTeams, nil)
	deleted := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/bots/"+botA {
			return jsonResponse(200, strings.Replace(botWith(botA, "deploy"), `"delete_at":0`, `"delete_at":1735689800000`, 1)), nil
		}
		return base(r)
	})
	result, err = deleted.invoke(botsGet.ID, "botall", `{"bot_user_id":"`+botA+`","include_deleted":true}`)
	if err != nil || !strings.Contains(result, `"deleted":true`) || !strings.Contains(result, `"deleted_at":"2025-01-01T00:03:20Z"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func newTopThreads(items ...string) string {
	return `{"has_next":false,"items":[` + strings.Join(items, ",") + `]}`
}

func fullThread(post, channel, author string, postExtra string) string {
	return `{"post":{"id":"` + post + `","channel_id":"` + channel + `","user_id":"` + author + `","reply_count":4,` +
		`"create_at":1735689600000,"update_at":1735689700000,"message":"` + insightText + `"` + postExtra + `},` +
		`"channel_id":"` + channel + `","channel_name":"town-square","channel_display_name":"Town Square",` +
		`"Participants":["` + userInA + `"],"user_information":{"id":"` + author + `","first_name":"Jane",` +
		`"last_name":"Doe","nickname":"jd","username":"jane.doe","last_picture_update":"1735689700000",` +
		`"create_at":1735689600000}}`
}

func TestInsightsTopIsVersionTwoWithoutMessageCountForMembers(t *testing.T) {
	if insightsTop.Version != 2 || !strings.Contains(insightsTop.Description, "the team members who joined in the time range") ||
		strings.Contains(insightsTop.Description, "no message text") {
		t.Fatalf("descriptor = %d %q", insightsTop.Version, insightsTop.Description)
	}
	var schema struct {
		Properties struct {
			Members struct {
				Items struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"items"`
			} `json:"members"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(insightsTop.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if _, has := schema.Properties.Members.Items.Properties["message_count"]; has {
		t.Fatal("member schema still has message_count")
	}
	for _, f := range insightsTop.Fields {
		if f.Name == "message_count" && strings.Contains(f.Description, "member") {
			t.Fatalf("field text = %q", f.Description)
		}
	}
}

func TestInsightsTopNewMembersCarryTheirDetailsAndTheTotal(t *testing.T) {
	answer := `{"has_next":true,"total_count":12,"items":[{"id":"` + userInA + `","username":"jane.doe",` +
		`"first_name":"Jane","last_name":"Doe","nickname":"jd","position":"Lead","create_at":1735689600000,` +
		`"message_count":5},{"id":"` + userInB + `","username":"b","first_name":"Foreign"}]}`
	result, _, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"team_members","time_range":"7_day"}`, answer)
	want := `{"count":1,"has_more":true,"kind":"team_members","members":[{"first_name":"Jane","joined_at":"2025-01-01T00:00:00Z",` +
		`"last_name":"Doe","nickname":"jd","position":"Lead","user_id":"` + userInA + `","username":"jane.doe"}],"page":1,` +
		`"team_id":"` + teamA + `","time_range":"7_day","total":12}`
	if err != nil || result != want {
		t.Fatalf("result = %s, err = %v\nwant %s", result, err, want)
	}
	// Only team_members carries a total, and a total of 0 is kept.
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"team_members"}`, `{"items":[],"total_count":0}`)
	if err != nil || !strings.Contains(result, `"total":0`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"reactions"}`, `{"items":[],"total_count":3}`)
	if err != nil || strings.Contains(result, "total") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestInsightsTopThreadsCarryTextChannelNamesAndTheAuthor(t *testing.T) {
	answer := newTopThreads(
		fullThread(insightPostA, chanA, userInA, `,"props":{"k":"v\u0007"},"hashtag":"#x"`),
		fullThread("post0000000000000000002", chanA, userInB, ""),
		fullThread("post0000000000000000003", chanA, userLeft, `,"delete_at":9`),
		strings.Replace(fullThread("post0000000000000000004", chanA, userInA, ""), `"user_information":{"id":"`+userInA+`"`,
			`"user_information":{"id":"bad/id"`, 1),
	)
	result, _, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"threads"}`, answer)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Threads []InsightThread `json:"threads"`
		Count   int             `json:"count"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != 4 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	first := out.Threads[0]
	if first.ChannelName != "town-square" || first.ChannelDisplayName != "Town Square" || first.Post == nil ||
		first.Post.Message != insightText || first.Post.CreatedAt != "2025-01-01T00:00:00Z" ||
		first.Post.Props["k"] != "v" || first.Post.Hashtag != "#x" || first.ReplyCount != 4 ||
		first.UserInformation == nil || *first.UserInformation != (InsightUser{ID: userInA, FirstName: "Jane",
		LastName: "Doe", Nickname: "jd", Username: "jane.doe", CreatedAt: "2025-01-01T00:00:00Z",
		LastPictureUpdate: 1735689700000}) {
		t.Fatalf("first = %+v, post = %+v", first, first.Post)
	}
	// An author outside the reachable users shows only the ID, and the thread stays.
	if u := out.Threads[1].UserInformation; u == nil || *u != (InsightUser{ID: userInB}) ||
		strings.Contains(result, "Foreign") || strings.Count(result, "Jane") != 1 {
		t.Fatalf("second = %+v in %s", out.Threads[1], result)
	}
	if out.Threads[1].Post == nil {
		t.Fatal("thread of an unreachable author lost its post")
	}
	// A deleted root message leaves the thread without a post; an unusable author ID leaves it without information.
	if out.Threads[2].Post != nil || out.Threads[3].UserInformation != nil || out.Threads[3].Post == nil {
		t.Fatalf("threads = %+v", out.Threads[2:])
	}
}

func TestInsightsTopReadsNumbersAsNumbersOrStrings(t *testing.T) {
	result, _, err := insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"channels"}`,
		`{"items":[{"id":"`+chanA+`","type":"O","name":"n","display_name":"N","message_count":"42"}]}`)
	if err != nil || !strings.Contains(result, `"message_count":42`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"channels"}`,
		`{"items":[{"id":"`+chanA+`","type":"O","name":"n","display_name":"N","message_count":"many"}]}`)
	if err != nil || !strings.Contains(result, `"message_count":0`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	thread := strings.Replace(fullThread(insightPostA, chanA, userInA, ""), `"last_picture_update":"1735689700000"`,
		`"last_picture_update":1735689700000`, 1)
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"threads"}`, newTopThreads(thread))
	if err != nil || !strings.Contains(result, `"last_picture_update":1735689700000`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	thread = strings.Replace(fullThread(insightPostA, chanA, userInA, ""), `"last_picture_update":"1735689700000"`,
		`"last_picture_update":"soon"`, 1)
	result, _, err = insightsTopCall(t, "team", `{"team_id":"`+teamA+`","kind":"threads"}`, newTopThreads(thread))
	if err != nil || strings.Contains(result, "last_picture_update") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

// outputKeys collects every member name of a result outside the free parts of a message, without the lists
// that hold the entries.
func outputKeys(value any, into map[string]bool, depth int) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			outputKeys(item, into, depth)
		}
	case map[string]any:
		for key, item := range v {
			if _, list := item.([]any); !list || depth > 0 {
				into[key] = true
			}
			if key != "post" {
				outputKeys(item, into, depth+1)
			}
		}
	}
}

func TestEveryIntegrationAndInsightMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	var calls []call
	hooks := newOutgoingFixture(t, nil)
	hooks.listing = "[" + hooks.hooks[ohookA] + "]"
	commands := newCommandFixture(t, nil)
	commands.listing = "[" + commandWith(cmdA, teamA) + "]"
	deletedBot := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/bots" || r.URL.Path == "/api/v4/bots/"+botA {
			body := strings.Replace(botWith(botA, "deploy"), `"delete_at":0`, `"delete_at":5`, 1)
			if r.URL.Path == "/api/v4/bots" {
				body = "[" + body + "]"
			}
			return jsonResponse(200, body), nil
		}
		return botHandler(t, bothTeams, nil)(r)
	}
	deletedEmoji := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/emoji" {
			return jsonResponse(200, `[{"id":"`+emojiA+`","name":"gone","creator_id":"u1","create_at":1,"update_at":2,"delete_at":3}]`), nil
		}
		return jsonResponse(200, bothTeams), nil
	}
	members := `{"total_count":1,"items":[{"id":"` + userInA + `","username":"u","first_name":"F","last_name":"L",` +
		`"nickname":"n","position":"p","create_at":1735689600000}]}`
	for _, tt := range []struct {
		id, connection, args string
		handler              func(*http.Request) (*http.Response, error)
	}{
		{emojiList.ID, "botall", `{}`, deletedEmoji},
		{botsList.ID, "botall", `{"include_deleted":true}`, deletedBot},
		{botsGet.ID, "botall", `{"bot_user_id":"` + botA + `","include_deleted":true}`, deletedBot},
		{outgoingWebhooksList.ID, "hooker", `{"team_id":"` + teamA + `"}`, hooks.handle},
		{outgoingWebhooksGet.ID, "hooker", `{"hook_id":"` + ohookA + `"}`, hooks.handle},
		{commandsList.ID, "cmder", `{"team_id":"` + teamA + `"}`, commands.handle},
		{commandsGet.ID, "cmder", `{"command_id":"` + cmdA + `"}`, commands.handle},
		{insightsTop.ID, "team", `{"team_id":"` + teamA + `","kind":"threads"}`,
			insightsServer(t, 200, newTopThreads(fullThread(insightPostA, chanA, userInA, "")))},
		{insightsTop.ID, "team", `{"team_id":"` + teamA + `","kind":"team_members"}`, insightsServer(t, 200, members)},
		{insightsTop.ID, "team", `{"team_id":"` + teamA + `","kind":"channels"}`,
			insightsServer(t, 200, `{"items":[`+channelItem(chanA)+`]}`)},
	} {
		t.Run(tt.id+" "+tt.args, func(t *testing.T) {
			env := newEnvironment(t, &calls, tt.handler)
			result, err := env.invoke(tt.id, tt.connection, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range registry.Provider(Provider) {
				if d.ID != tt.id {
					continue
				}
				var schema map[string]any
				var output any
				if err := json.Unmarshal(d.OutputSchema, &schema); err != nil || json.Unmarshal([]byte(result), &output) != nil {
					t.Fatal(err)
				}
				checkSchema(t, output, schema, "")
				fields := map[string]bool{}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
				keys := map[string]bool{}
				outputKeys(output, keys, 0)
				for key := range keys {
					if !fields[key] {
						t.Errorf("output member %s is not in Fields", key)
					}
				}
			}
		})
	}
}

func TestNoCommandTokenOrFullCallbackAddressReachesAnyOutput(t *testing.T) {
	commands := newCommandFixture(t, nil)
	commands.listing = "[" + commandWith(cmdA, teamA) + "]"
	hooks := newOutgoingFixture(t, nil)
	hooks.listing = "[" + hooks.hooks[ohookA] + "]"
	var calls []call
	for _, tt := range []struct {
		id, connection, args string
		env                  func(*[]call) *environment
	}{
		{commandsList.ID, "cmder", `{"team_id":"` + teamA + `"}`, commands.env},
		{commandsGet.ID, "cmder", `{"command_id":"` + cmdA + `"}`, commands.env},
		{outgoingWebhooksList.ID, "hooker", `{"team_id":"` + teamA + `"}`, hooks.env},
		{outgoingWebhooksGet.ID, "hooker", `{"hook_id":"` + ohookA + `"}`, hooks.env},
	} {
		result, err := tt.env(&calls).invoke(tt.id, tt.connection, tt.args)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		noSecrets(t, tt.id, result)
		noCommandSecrets(t, tt.id, result)
		if strings.Contains(result, `"token"`) {
			t.Fatalf("%s shows a token member: %s", tt.id, result)
		}
	}
}
