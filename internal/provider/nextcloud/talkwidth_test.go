package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const fullRoomJSON = `{"id":42,"token":"abcd1234","type":2,"name":"Team","displayName":"Team","description":"Plan",` +
	`"participantType":2,"attendeeId":7,"attendeePin":"pin-canary","actorType":"users","actorId":"dora",` +
	`"permissions":255,"attendeePermissions":128,"callPermissions":0,"defaultPermissions":182,` +
	`"participantFlags":3,"readOnly":0,"listable":1,"messageExpiration":3600,"lastPing":1767225000,` +
	`"sessionId":"session-canary","hasPassword":false,"hasCall":true,"callFlag":3,"canStartCall":true,` +
	`"canDeleteConversation":false,"canLeaveConversation":true,"lastActivity":1767225600,"isFavorite":true,` +
	`"notificationLevel":2,"lobbyState":1,"lobbyTimer":1767229200,"sipEnabled":1,"canEnableSIP":true,` +
	`"unreadMessages":2,"unreadMention":true,"unreadMentionDirect":false,"lastReadMessage":99,` +
	`"lastCommonReadMessage":98,"lastMessage":` + `{"id":100,"timestamp":1767225600,"actorType":"users","actorId":"bob",` +
	`"actorDisplayName":"Bob","messageType":"comment","systemMessage":"","message":"hi {mention-user1}",` +
	`"messageParameters":{"mention-user1":{"type":"user","id":"carol","name":"Carol"}},"reactions":{"x":1}},` +
	`"objectType":"room","objectId":"obj-1","breakoutRoomMode":"1","breakoutRoomStatus":"0",` +
	`"status":"away","statusIcon":"x","statusMessage":"gone","statusClearAt":1767300000,"avatarVersion":"v3",` +
	`"isCustomAvatar":true,"callStartTime":1767225100,"callRecording":1,"recordingConsent":2,` +
	`"mentionPermissions":1,"isArchived":false,"remoteServer":"https://remote.example.invalid",` +
	`"remoteToken":"remote-canary","callId":"call-canary"}`

const fullParticipantJSON = `{"attendeeId":12,"actorType":"users","actorId":"bob","displayName":"Bob","participantType":2,` +
	`"lastPing":1767225000,"inCall":3,"permissions":254,"attendeePermissions":128,"sessionIds":["sess-canary"],` +
	`"sessionId":"sess2-canary","status":"dnd","statusIcon":"i","statusMessage":"busy","roomToken":"brk12345",` +
	`"phoneNumber":"+100","callId":"call-canary","actorAvatarVersion":"v9"}`

var accessCanaries = []string{"pin-canary", "session-canary", "remote-canary", "call-canary", "sess-canary", "sess2-canary"}

func widthServer(t *testing.T, room, participant string) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case "/ocs/v2.php/apps/spreed/api/v4/room":
			return ocsResponse(http.StatusOK, ocsData(`[`+room+`]`)), nil
		case "/ocs/v2.php/apps/spreed/api/v4/room/" + boundRoom:
			return ocsResponse(http.StatusOK, ocsData(room)), nil
		case "/ocs/v2.php/apps/spreed/api/v4/room/" + boundRoom + "/participants":
			return ocsResponse(http.StatusOK, ocsData(`[`+participant+`]`)), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return nil, http.ErrNotSupported
	})
}

// checkNested checks every member of a decoded object against the properties of its schema, recursively.
func checkNested(t *testing.T, path string, value any, schema map[string]any) {
	t.Helper()
	if items, isList := value.([]any); isList {
		element, _ := schema["items"].(map[string]any)
		for _, item := range items {
			checkNested(t, path+"[]", item, element)
		}
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, member := range object {
		declared, ok := properties[key].(map[string]any)
		if free, isFree := schema["additionalProperties"].(map[string]any); !ok && isFree && properties == nil {
			declared, ok = free, true
		}
		if !ok {
			t.Errorf("%s.%s is not declared in the schema", path, key)
			continue
		}
		checkNested(t, path+"."+key, member, declared)
	}
}

func checkDeclared(t *testing.T, descriptor capability.Descriptor, entry json.RawMessage, schema json.RawMessage) {
	t.Helper()
	var value any
	var declared map[string]any
	if err := json.Unmarshal(entry, &value); err != nil || json.Unmarshal(schema, &declared) != nil {
		t.Fatalf("entry %s, %v", entry, err)
	}
	checkNested(t, descriptor.ID, value, declared)
	fields := map[string]bool{}
	for _, f := range descriptor.Fields {
		fields[f.Name] = true
	}
	for key := range value.(map[string]any) {
		if !fields[key] {
			t.Errorf("%s: %s has no field", descriptor.ID, key)
		}
	}
}

func TestTalkRoomsReportEveryDocumentedField(t *testing.T) {
	calls := widthServer(t, fullRoomJSON, fullParticipantJSON)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.list",
		`{"modified_since":1767000000,"include_status":true}`)
	if err != nil {
		t.Fatal(err)
	}
	query := (*calls)[len(*calls)-1].url.Query()
	if query.Get("modifiedSince") != "1767000000" || query.Get("includeStatus") != "true" || query.Get("noStatusUpdate") != "1" {
		t.Errorf("query = %v", query)
	}
	var list struct{ Rooms []json.RawMessage }
	if err := json.Unmarshal(response.Result, &list); err != nil || len(list.Rooms) != 1 {
		t.Fatalf("result = %s, %v", response.Result, err)
	}
	var schema struct {
		Properties struct {
			Rooms struct {
				Items json.RawMessage `json:"items"`
			} `json:"rooms"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(talkRoomsList.OutputSchema, &schema)
	checkDeclared(t, talkRoomsList, list.Rooms[0], schema.Properties.Rooms.Items)

	want := `{"id":42,"attendee_id":7,"actor_type":"users","actor_id":"dora","permissions":255,"attendee_permissions":128,` +
		`"call_permissions":0,"default_permissions":182,"participant_flags":3,"listable":1,"message_expiration":3600,` +
		`"last_ping":1767225000,"has_call":true,"call_flag":3,"can_start_call":true,"can_delete_conversation":false,` +
		`"can_leave_conversation":true,"is_favorite":true,"notification_level":2,"lobby_state":1,"lobby_timer":1767229200,` +
		`"sip_enabled":1,"can_enable_sip":true,"unread_mention_direct":false,"last_read_message":99,` +
		`"last_common_read_message":98,"last_message":{"id":"100","time":"2026-01-01T00:00:00Z","actor_type":"users",` +
		`"actor_id":"bob","actor_name":"Bob","kind":"comment","text":"hi @Carol","objects":[{"key":"mention-user1",` +
		`"type":"user","id":"carol","name":"Carol"}],"reactions":{"x":1}},"object_type":"room","object_id":"obj-1","breakout_room_mode":"1",` +
		`"breakout_room_status":"0","status":"away","status_icon":"x","status_message":"gone","status_clear_at":1767300000,` +
		`"avatar_version":"v3","is_custom_avatar":true,"call_start_time":1767225100,"call_recording":1,` +
		`"recording_consent":2,"mention_permissions":1,"is_archived":false,"remote_server":"https://remote.example.invalid"}`
	var got, expected map[string]any
	_ = json.Unmarshal(list.Rooms[0], &got)
	_ = json.Unmarshal([]byte(want), &expected)
	for key, value := range expected {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
	for _, canary := range accessCanaries {
		if strings.Contains(string(response.Result), canary) {
			t.Errorf("the result carries %q", canary)
		}
	}

	get, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.get", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatal(err)
	}
	checkDeclared(t, talkRoomsGet, get.Result, talkRoomsGet.OutputSchema)
	var one map[string]any
	_ = json.Unmarshal(get.Result, &one)
	if _, ok := one["status"]; ok {
		t.Error("rooms.get reports a status it did not request")
	}
	if one["last_message"] == nil || one["remote_server"] == nil {
		t.Errorf("rooms.get = %s", get.Result)
	}
	for _, canary := range accessCanaries {
		if strings.Contains(string(get.Result), canary) {
			t.Errorf("rooms.get carries %q", canary)
		}
	}
}

func TestTalkRoomsWithoutNewArgumentsKeepRequestAndStatus(t *testing.T) {
	calls := widthServer(t, fullRoomJSON, fullParticipantJSON)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[len(*calls)-1].url.Query(); got.Get("noStatusUpdate") != "1" || got.Has("includeStatus") || got.Has("modifiedSince") {
		t.Errorf("query = %q", got)
	}
	if strings.Contains(string(response.Result), `"status`) {
		t.Errorf("status without include_status: %s", response.Result)
	}
}

func TestTalkRoomsRefuseUnusableFilters(t *testing.T) {
	refuse(t)
	for _, args := range []string{`{"modified_since":-1}`, `{"modified_since":1.5}`, `{"include_status":"yes"}`, `{"x":1}`} {
		if _, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talk.rooms.list", args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
}

func TestTalkRoomsOmitAbsentMembers(t *testing.T) {
	widthServer(t, roomJSON(boundRoom, "Team", 2), fullParticipantJSON)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.get", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"last_message", "id", "permissions", "is_archived", "lobby_timer", "has_call"} {
		if strings.Contains(string(response.Result), `"`+key+`"`) {
			t.Errorf("absent member %s reported: %s", key, response.Result)
		}
	}
	empty := strings.Replace(fullRoomJSON, `"lastMessage":{`, `"lastMessage":[],"x":{`, 1)
	widthServer(t, empty, fullParticipantJSON)
	response, err = talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.get", talkArgs(boundRoom, ""))
	if err != nil || strings.Contains(string(response.Result), "last_message") {
		t.Errorf("empty last message: %s, %v", response.Result, err)
	}
}

func TestTalkParticipantsReportEveryDocumentedField(t *testing.T) {
	calls := widthServer(t, fullRoomJSON, fullParticipantJSON)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.participants.list",
		talkArgs(boundRoom, `,"include_status":true`))
	if err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[len(*calls)-1].url.Query().Get("includeStatus"); got != "true" {
		t.Errorf("includeStatus = %q", got)
	}
	var list struct{ Participants []json.RawMessage }
	if err := json.Unmarshal(response.Result, &list); err != nil || len(list.Participants) != 1 {
		t.Fatalf("result = %s, %v", response.Result, err)
	}
	var schema struct {
		Properties struct {
			Participants struct {
				Items json.RawMessage `json:"items"`
			} `json:"participants"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(talkParticipantsList.OutputSchema, &schema)
	checkDeclared(t, talkParticipantsList, list.Participants[0], schema.Properties.Participants.Items)
	want := `{"attendee_id":"12","actor_type":"users","actor_id":"bob","display_name":"Bob","participant_type":"moderator",` +
		`"in_call":true,"permissions":254,"attendee_permissions":128,"last_ping":1767225000,"in_call_flags":3,` +
		`"room_token":"brk12345","status":"dnd","status_icon":"i","status_message":"busy","phone_number":"+100",` +
		`"actor_avatar_version":"v9"}`
	var got, expected map[string]any
	_ = json.Unmarshal(list.Participants[0], &got)
	_ = json.Unmarshal([]byte(want), &expected)
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("participant = %s", list.Participants[0])
	}
	for _, canary := range accessCanaries {
		if strings.Contains(string(response.Result), canary) {
			t.Errorf("the result carries %q", canary)
		}
	}

	response, err = talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.participants.list", talkArgs(boundRoom, ""))
	if err != nil || strings.Contains(string(response.Result), `"status`) ||
		(*calls)[len(*calls)-1].url.Query().Get("includeStatus") != "false" {
		t.Errorf("without include_status: %s, %v", response.Result, err)
	}
}
