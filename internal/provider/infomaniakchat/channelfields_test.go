package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	fieldChanArchived = "chanarch0000000000000000x1"
	fieldInviteID     = "invite-id-canary-3c1d"
	fieldTeamName     = "town-square"
)

// fieldTeam is a team as the API documents it; the invitation ID is part of the answer and must not be
// passed on.
func fieldTeam(full bool) string {
	team := `{"id":"` + teamA + `","name":"` + teamA + `","display_name":"Team","type":"I"`
	if !full {
		return team + `}`
	}
	return team + `,"description":"About\u0007 us","email":"team@example.invalid","allowed_domains":"example.invalid",` +
		`"invite_id":"` + fieldInviteID + `","create_at":1735689600000,"update_at":"1735689700000","delete_at":0,` +
		`"allow_open_invite":false,"policy_id":"pol1"}`
}

func fieldChannel(id, teamID string, full bool, extra string) string {
	channel := `{"id":"` + id + `","team_id":"` + teamID + `","type":"O","display_name":"Channel","name":"` + id +
		`","purpose":""` + extra
	if !full {
		return channel + `,"delete_at":0}`
	}
	return channel + `,"header":"Head\u0007er","create_at":1735689600000,"update_at":"1735689700000",` +
		`"last_post_at":1735689800000,"total_msg_count":"42","creator_id":"user1","delete_at":0}`
}

func fieldArchived(full bool) string {
	channel := fieldChannel(fieldChanArchived, teamA, full, "")
	return strings.Replace(channel, `"delete_at":0`, `"delete_at":1735689900000`, 1)
}

const fieldMember = `{"channel_id":"` + chanA + `","user_id":"` + userInA + `","roles":"channel_user",` +
	`"scheme_admin":false,"last_viewed_at":1735689800000,"msg_count":"7","mention_count":2,` +
	`"notify_props":{"email":"true","push":"mention","desktop":"all\u0007","mark_unread":"all","other":"x"},` +
	`"last_update_at":1735689700000}`

// chatFieldServer answers every read of the team, channel, and member tools. full chooses the answers with every
// documented field; otherwise they carry only the members the tools read before.
func chatFieldServer(t *testing.T, full bool) func(*http.Request) (*http.Response, error) {
	dm := `{"id":"` + dmA + `","team_id":"","type":"D","display_name":"","name":"` + directName(selfID, userInA) +
		`","purpose":"","delete_at":0}`
	if full {
		dm = `{"id":"` + dmA + `","team_id":"","type":"D","display_name":"","name":"` + directName(selfID, userInA) +
			`","purpose":"Chat","header":"Head\u0007er","create_at":1735689600000,"update_at":1735689700000,` +
			`"last_post_at":1735689800000,"total_msg_count":42,"creator_id":"user1","delete_at":0}`
	}
	direct := &directFixture{t: t, listing: "[" + fieldChannel(chanA, teamA, full, "") + "," + dm + "]"}
	member, legacyMember := fieldMember, `{"channel_id":"`+chanA+`","user_id":"`+userInA+`","roles":"channel_user"}`
	if !full {
		member = legacyMember
	}
	teamMember := `{"team_id":"` + teamA + `","user_id":"` + userInA + `","roles":"team_user","scheme_admin":false,` +
		`"delete_at":0}`
	if full {
		teamMember = `{"team_id":"` + teamA + `","user_id":"` + userInA + `","roles":"team_user","scheme_admin":false,` +
			`"delete_at":1735689900000,"scheme_user":true,"explicit_roles":"team_user"}`
	}
	unread := `{"channel_id":"` + chanA + `","msg_count":4,"mention_count":2}`
	if full {
		unread = `{"team_id":"` + teamA + `","channel_id":"` + chanA + `","msg_count":4,"mention_count":2}`
	}
	return func(r *http.Request) (*http.Response, error) {
		switch p := r.URL.Path; {
		case p == "/api/v4/users/me/teams":
			return jsonResponse(200, "["+fieldTeam(full)+"]"), nil
		case p == "/api/v4/teams/"+teamA:
			return jsonResponse(200, fieldTeam(full)), nil
		case p == "/api/v4/teams/"+teamA+"/stats":
			return jsonResponse(200, `{"team_id":"`+teamA+`","total_member_count":5,"active_member_count":4}`), nil
		case p == "/api/v4/teams/"+teamA+"/members" && r.Method == http.MethodGet:
			return jsonResponse(200, "["+teamMember+"]"), nil
		case p == "/api/v4/teams/"+teamA+"/channels":
			return jsonResponse(200, "["+fieldChannel(chanA, teamA, full, "")+"]"), nil
		case p == "/api/v4/teams/"+teamA+"/channels/deleted":
			return jsonResponse(200, "["+fieldArchived(full)+"]"), nil
		case p == "/api/v4/teams/"+teamA+"/channels/search":
			return jsonResponse(200, "["+fieldChannel(chanA, teamA, full, "")+"]"), nil
		case p == "/api/v4/teams/"+teamA+"/channels/name/"+fieldTeamName:
			if r.URL.Query().Get("include_deleted") == "true" {
				return jsonResponse(200, fieldArchived(full)), nil
			}
			return jsonResponse(200, fieldChannel(chanA, teamA, full, "")), nil
		case p == "/api/v4/channels/"+chanA:
			return jsonResponse(200, fieldChannel(chanA, teamA, full, "")), nil
		case p == "/api/v4/channels/"+fieldChanArchived:
			return jsonResponse(200, fieldArchived(full)), nil
		case p == "/api/v4/channels/"+fieldChanArchived+"/stats":
			return jsonResponse(200, `{"channel_id":"`+fieldChanArchived+`","member_count":3}`), nil
		case p == "/api/v4/channels/"+chanA+"/stats":
			return jsonResponse(200, `{"channel_id":"`+chanA+`","member_count":7}`), nil
		case p == "/api/v4/channels/"+chanA+"/members":
			return jsonResponse(200, "["+member+"]"), nil
		case p == "/api/v4/users/me/channels/"+chanA+"/unread":
			return jsonResponse(200, unread), nil
		}
		return direct.handle(r)
	}
}

type chatFieldTool struct {
	id, args string
	// entry returns the one entry of a result, the result itself for a single-entry tool.
	entry func(t *testing.T, result string) json.RawMessage
	// schema returns the schema of that entry within the output schema.
	schema func(output map[string]any) map[string]any
	// full is the entry of the answer with every documented field, legacy the whole result of the answer
	// without the newer members, as the core writes it.
	full, legacy string
}

func chatFieldTools() []chatFieldTool {
	member := func(name string) func(*testing.T, string) json.RawMessage {
		return func(t *testing.T, result string) json.RawMessage {
			var out map[string]json.RawMessage
			var list []json.RawMessage
			if err := json.Unmarshal([]byte(result), &out); err != nil || json.Unmarshal(out[name], &list) != nil ||
				len(list) != 1 {
				t.Fatalf("%s: %v in %s", name, err, result)
			}
			return list[0]
		}
	}
	whole := func(_ *testing.T, result string) json.RawMessage { return json.RawMessage(result) }
	items := func(name string) func(map[string]any) map[string]any {
		return func(output map[string]any) map[string]any {
			list, _ := output["properties"].(map[string]any)[name].(map[string]any)
			entry, _ := list["items"].(map[string]any)
			return entry
		}
	}
	itself := func(output map[string]any) map[string]any { return output }
	team := `{"team_id":"` + teamA + `"}`
	channelFull := `{"id":"` + chanA + `","team_id":"` + teamA + `","name":"` + chanA + `","display_name":"Channel",` +
		`"type":"O","header":"Header","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:01:40Z",` +
		`"last_post_at":"2025-01-01T00:03:20Z","total_msg_count":42,"creator_id":"user1"}`
	channelLegacy := func(extra string) string {
		return `{"display_name":"Channel","id":"` + chanA + `","name":"` + chanA + `"` + extra + `,"team_id":"` + teamA +
			`","type":"O"}`
	}
	archivedFull := strings.NewReplacer(chanA, fieldChanArchived).Replace(channelFull)
	archivedFull = strings.Replace(archivedFull, `"creator_id"`, `"deleted_at":"2025-01-01T00:05:00Z","creator_id"`, 1)
	pageEnd := `,"page":1,"per_page":50,"team_id":"` + teamA + `"}`
	detailLegacy := `{"archived":false,"display_name":"Channel","id":"` + chanA + `","member_count":7,"name":"` + chanA +
		`","team_id":"` + teamA + `","type":"O"}`
	return []chatFieldTool{
		{id: teamsList.ID, args: `{}`, entry: member("teams"), schema: items("teams"),
			full: `{"id":"` + teamA + `","name":"` + teamA + `","display_name":"Team","type":"I","description":"About us",` +
				`"email":"team@example.invalid","allowed_domains":"example.invalid","created_at":"2025-01-01T00:00:00Z",` +
				`"updated_at":"2025-01-01T00:01:40Z","allow_open_invite":false,"policy_id":"pol1"}`,
			legacy: `{"count":1,"page":1,"pages":1,"teams":[{"display_name":"Team","id":"` + teamA + `","name":"` + teamA +
				`","type":"I"}],"total":1}`},
		{id: teamsGet.ID, args: team, entry: whole, schema: itself,
			full: `{"id":"` + teamA + `","name":"` + teamA + `","display_name":"Team","type":"I","description":"About us",` +
				`"member_count":5,"active_member_count":4,"email":"team@example.invalid","allowed_domains":"example.invalid",` +
				`"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:01:40Z","allow_open_invite":false,` +
				`"policy_id":"pol1"}`,
			legacy: `{"active_member_count":4,"description":"","display_name":"Team","id":"` + teamA + `","member_count":5,` +
				`"name":"` + teamA + `","type":"I"}`},
		{id: teamMembersList.ID, args: team, entry: member("members"), schema: items("members"),
			full: `{"user_id":"` + userInA + `","roles":"team_user","scheme_admin":false,"deleted":true,` +
				`"scheme_user":true,"explicit_roles":"team_user","deleted_at":"2025-01-01T00:05:00Z"}`,
			legacy: `{"count":1,"has_more":false,"members":[{"deleted":false,"roles":"team_user","scheme_admin":false,` +
				`"user_id":"` + userInA + `"}],"page":1,"team_id":"` + teamA + `"}`},
		{id: channelsList.ID, args: team, entry: member("channels"), schema: items("channels"), full: channelFull,
			legacy: `{"channels":[` + channelLegacy("") + `],"count":1,"page":1,"pages":1,"team_id":"` + teamA + `","total":1}`},
		{id: channelsBrowse.ID, args: team, entry: member("channels"), schema: items("channels"), full: channelFull,
			legacy: `{"channels":[` + channelLegacy("") + `],"count":1,"has_more":false` + pageEnd},
		{id: channelsSearch.ID, args: `{"team_id":"` + teamA + `","term":"chan"}`, entry: member("channels"),
			schema: items("channels"), full: channelFull,
			legacy: `{"channels":[` + channelLegacy("") + `],"count":1,"team_id":"` + teamA + `"}`},
		{id: archivedChannelsList.ID, args: team, entry: member("channels"), schema: items("channels"), full: archivedFull,
			// An archived channel always reports when it was archived.
			legacy: `{"channels":[` + strings.Replace(strings.NewReplacer(chanA, fieldChanArchived).Replace(channelLegacy("")),
				`{"display_name"`, `{"deleted_at":"2025-01-01T00:05:00Z","display_name"`, 1) +
				`],"count":1,"has_more":false` + pageEnd},
		{id: channelsGet.ID, args: `{"team_id":"` + teamA + `","name":"` + fieldTeamName + `"}`, entry: whole, schema: itself,
			full: `{"id":"` + chanA + `","team_id":"` + teamA + `","name":"` + chanA + `","display_name":"Channel",` +
				`"type":"O","header":"Header","member_count":7,"created_at":"2025-01-01T00:00:00Z","archived":false,` +
				`"updated_at":"2025-01-01T00:01:40Z","last_post_at":"2025-01-01T00:03:20Z","total_msg_count":42,` +
				`"creator_id":"user1"}`,
			legacy: detailLegacy},
		{id: channelsUnread.ID, args: `{"channel_id":"` + chanA + `"}`, entry: whole, schema: itself,
			full:   `{"channel_id":"` + chanA + `","team_id":"` + teamA + `","msg_count":4,"mention_count":2}`,
			legacy: `{"channel_id":"` + chanA + `","mention_count":2,"msg_count":4}`},
		{id: directList.ID, args: team, entry: member("channels"), schema: items("channels"),
			full: `{"id":"` + dmA + `","type":"D","user_ids":["` + userInA + `"],"name":"` + directName(selfID, userInA) +
				`","header":"Header","purpose":"Chat","created_at":"2025-01-01T00:00:00Z",` +
				`"updated_at":"2025-01-01T00:01:40Z","last_post_at":"2025-01-01T00:03:20Z","total_msg_count":42,` +
				`"creator_id":"user1"}`,
			// The handle of a direct channel is part of every answer.
			legacy: `{"channels":[{"id":"` + dmA + `","name":"` + directName(selfID, userInA) + `","type":"D",` +
				`"user_ids":["` + userInA + `"]}],"count":1,` +
				`"has_more":false,"page":1,"team_id":"` + teamA + `"}`},
		{id: channelsMembersList.ID, args: `{"channel_id":"` + chanA + `"}`, entry: member("members"), schema: items("members"),
			full: `{"user_id":"` + userInA + `","roles":"channel_user","scheme_admin":false,` +
				`"last_viewed_at":"2025-01-01T00:03:20Z","msg_count":7,"mention_count":2,` +
				`"notify_props":{"email":"true","push":"mention","desktop":"all","mark_unread":"all"},` +
				`"last_update_at":"2025-01-01T00:01:40Z"}`,
			legacy: `{"channel_id":"` + chanA + `","count":1,"has_more":false,"members":[{"roles":"channel_user",` +
				`"scheme_admin":false,"user_id":"` + userInA + `"}],"page":1}`},
	}
}

func TestEveryNewTeamChannelAndMemberFieldArrivesInAllTools(t *testing.T) {
	for _, tool := range chatFieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, chatFieldServer(t, true))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if strings.Contains(result, fieldInviteID) || strings.Contains(result, "invite_id") {
				t.Fatalf("result carries the invitation ID: %s", result)
			}
			equalJSON(t, string(tool.entry(t, result)), tool.full)
		})
	}
}

func TestAnswersWithoutNewTeamChannelAndMemberFieldsKeepTheFormerOutputByteForByte(t *testing.T) {
	for _, tool := range chatFieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, chatFieldServer(t, false))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if result != tool.legacy {
				t.Fatalf("result = %s\n want   %s", result, tool.legacy)
			}
		})
	}
}

func TestEveryTeamChannelAndMemberOutputMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	for _, tool := range chatFieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, chatFieldServer(t, true))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range registry.Provider(Provider) {
				if d.ID != tool.id {
					continue
				}
				var schema map[string]any
				if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				fields := map[string]bool{}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
				var entry map[string]any
				if err := json.Unmarshal(tool.entry(t, result), &entry); err != nil {
					t.Fatal(err)
				}
				for key := range entry {
					if !fields[key] {
						t.Errorf("entry member %s is not in Fields", key)
					}
				}
				entrySchema := tool.schema(schema)
				checkSchema(t, entry, entrySchema, "")
				for key := range entrySchema["properties"].(map[string]any) {
					if !fields[key] {
						t.Errorf("schema member %s is not in Fields", key)
					}
				}
				if tool.id == teamsGet.ID || tool.id == teamsList.ID {
					if fields["invite_id"] || entrySchema["properties"].(map[string]any)["invite_id"] != nil {
						t.Error("the invitation ID is described")
					}
				}
			}
		})
	}
}

func TestNumbersAndTextsOfTeamsAndChannelsAreReadTolerantly(t *testing.T) {
	odd := strings.Replace(fieldChannel(chanA, teamA, true, ""), `"update_at":"1735689700000"`, `"update_at":"x"`, 1)
	odd = strings.Replace(odd, `"total_msg_count":"42"`, `"total_msg_count":true`, 1)
	odd = strings.Replace(odd, `"creator_id":"user1"`, `"creator_id":5`, 1)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams":
			team := strings.Replace(fieldTeam(true), `"email":"team@example.invalid"`, `"email":["x"]`, 1)
			team = strings.Replace(team, `"allow_open_invite":false`, `"allow_open_invite":"yes"`, 1)
			return jsonResponse(200, "["+strings.Replace(team, `"update_at":"1735689700000"`, `"update_at":"y"`, 1)+"]"), nil
		case "/api/v4/teams/" + teamA + "/channels":
			return jsonResponse(200, "["+odd+"]"), nil
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		return jsonResponse(404, `{}`), nil
	})
	result, err := env.invoke(teamsList.ID, "team", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, string(chatFieldTools()[0].entry(t, result)), `{"id":"`+teamA+`","name":"`+teamA+`",`+
		`"display_name":"Team","type":"I","description":"About us","allowed_domains":"example.invalid",`+
		`"created_at":"2025-01-01T00:00:00Z","policy_id":"pol1"}`)
	result, err = env.invoke(channelsBrowse.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, string(chatFieldTools()[4].entry(t, result)), `{"id":"`+chanA+`","team_id":"`+teamA+`",`+
		`"name":"`+chanA+`","display_name":"Channel","type":"O","header":"Header","created_at":"2025-01-01T00:00:00Z",`+
		`"last_post_at":"2025-01-01T00:03:20Z"}`)
}

func TestChannelsGetReadsAnArchivedChannelByHandleWithTheSameChecks(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, chatFieldServer(t, true))
	result, err := env.invoke(channelsGet.ID, "team",
		`{"team_id":"`+teamA+`","name":"`+fieldTeamName+`","include_deleted":true}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail map[string]any
	if json.Unmarshal([]byte(result), &detail) != nil || detail["id"] != fieldChanArchived || detail["archived"] != true ||
		detail["deleted_at"] != "2025-01-01T00:05:00Z" {
		t.Fatalf("result = %s", result)
	}
	if first := calls[0]; first.path != "/api/v4/teams/"+teamA+"/channels/name/"+fieldTeamName ||
		first.query.Get("include_deleted") != "true" {
		t.Fatalf("first call = %+v, want include_deleted in the query", first)
	}

	// Without the parameter, or with false, nothing is added to the query.
	for _, args := range []string{`{"team_id":"` + teamA + `","name":"` + fieldTeamName + `"}`,
		`{"team_id":"` + teamA + `","name":"` + fieldTeamName + `","include_deleted":false}`} {
		calls = nil
		if _, err := env.invoke(channelsGet.ID, "team", args); err != nil {
			t.Fatal(err)
		}
		if _, present := calls[0].query["include_deleted"]; present {
			t.Fatalf("query = %v, want no include_deleted", calls[0].query)
		}
	}

	// With channel_id the parameter is refused before any request or secret read.
	calls, *env.reads = nil, 0
	for _, value := range []string{"true", "false"} {
		_, err = env.invoke(channelsGet.ID, "team", `{"channel_id":"`+chanA+`","include_deleted":`+value+`}`)
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
		}
	}
	_, err = env.invoke(channelsGet.ID, "team", `{"include_deleted":true}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want a refusal without a handle", err)
	}

	// An archived channel of another team, or one outside the channel allow-list, is refused.
	foreign := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, strings.Replace(fieldArchived(true), `"team_id":"`+teamA+`"`, `"team_id":"`+teamB+`"`, 1)), nil
	})
	if _, err := foreign.invoke(channelsGet.ID, "team",
		`{"team_id":"`+teamA+`","name":"`+fieldTeamName+`","include_deleted":true}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the archived channel of another team refused", err)
	}
	if _, err := env.invoke(channelsGet.ID, "channel",
		`{"team_id":"`+teamA+`","name":"`+fieldTeamName+`","include_deleted":true}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the archived channel outside the allow-list refused", err)
	}
}

func TestListsStillDropArchivedChannelsAndInvitationIDsStayOut(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		body := "[" + fieldChannel(chanA, teamA, true, "") + "," + fieldArchived(true) + "]"
		return jsonResponse(200, body), nil
	})
	for _, id := range []string{channelsList.ID, channelsBrowse.ID} {
		result, err := env.invoke(id, "team", `{"team_id":"`+teamA+`"}`)
		if err != nil || strings.Contains(result, fieldChanArchived) {
			t.Fatalf("%s: result = %s, %v", id, result, err)
		}
	}
	result, err := env.invoke(archivedChannelsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || strings.Contains(result, `"`+chanA+`"`) || !strings.Contains(result, `"deleted_at":"2025-01-01T00:05:00Z"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
}
