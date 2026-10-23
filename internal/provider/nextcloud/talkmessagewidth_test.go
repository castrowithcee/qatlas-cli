package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	messageCallLink  = "https://cloud.example/call/room-token-canary"
	messageShareLink = "https://cloud.example/s/share-token-canary"
)

// richParams holds rich objects of the types the chat documentation names, with the members the server
// definitions know and members that hold access values.
const richParams = `{"actor":{"type":"user","id":"bob","name":"Bob","server":"https://remote.cloud.example"},` +
	`"file":{"type":"file","id":"77","name":"plan.pdf","path":"Talk/plan.pdf","link":"https://cloud.example/f/77",` +
	`"size":2048,"mimetype":"application/pdf","preview-available":"yes","permissions":27,"mtime":1767225000,"etag":"abc",` +
	`"share-token":"mtok-canary","password":"mpw-canary","can-download":true,"latitude":1.5},` +
	`"room":{"type":"call","id":"room-token-canary","name":"Team","call-type":"group","link":"` + messageCallLink + `",` +
	`"icon-url":"https://cloud.example/avatar/team/64"},` +
	`"share":{"type":"file","id":"78","name":"shared.txt","link":"` + messageShareLink + `","token":"x"}}`

const fullMessageJSON = `{"id":101,"token":"abcd1234","timestamp":1767225600,"actorType":"users","actorId":"bob",` +
	`"actorDisplayName":"Bob","messageType":"comment","systemMessage":"","isReplyable":true,"referenceId":"ref1",` +
	`"message":"{actor} {file} {room} {share}","messageParameters":` + richParams + `,"expirationTimestamp":1767312000,` +
	`"reactions":{"👍":2,"❤️":1},"reactionsSelf":["👍"],"markdown":true,"lastEditActorType":"users","lastEditActorId":"carol",` +
	`"lastEditActorDisplayName":"Carol","lastEditTimestamp":1767225900,"silent":false,` +
	`"parent":{"id":100,"token":"abcd1234","timestamp":1767225000,"actorType":"users","actorId":"dora",` +
	`"actorDisplayName":"Dora","messageType":"comment","systemMessage":"","message":"first","messageParameters":[],` +
	`"reactions":{"🎉":3},"reactionsSelf":["🎉"],"markdown":false,"parent":{"id":99}}}`

func messagesServer(t *testing.T, items ...string) *[]call {
	t.Helper()
	return talkServer(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(items, ",")+`]`)), nil
	})
}

func listMessages(t *testing.T) (json.RawMessage, []map[string]any) {
	t.Helper()
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.messages.list", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatal(err)
	}
	var page struct{ Messages []map[string]any }
	if err := json.Unmarshal(response.Result, &page); err != nil {
		t.Fatalf("result = %s, %v", response.Result, err)
	}
	return response.Result, page.Messages
}

func TestTalkMessagesReportEveryDocumentedField(t *testing.T) {
	calls := messagesServer(t, fullMessageJSON,
		`{"id":102,"timestamp":1767225700,"messageType":"comment","message":"re","messageParameters":[],`+
			`"expirationTimestamp":0,"parent":{"id":101,"deleted":true}}`)
	result, messages := listMessages(t)
	if len(messages) != 2 {
		t.Fatalf("result = %s", result)
	}
	checkOutputDeclared(t, talkMessagesList, "messages", result)
	var entries struct{ Messages []json.RawMessage }
	_ = json.Unmarshal(result, &entries)
	var schema struct {
		Properties struct {
			Messages struct {
				Items json.RawMessage `json:"items"`
			} `json:"messages"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(talkMessagesList.OutputSchema, &schema)
	for _, entry := range entries.Messages {
		checkDeclared(t, talkMessagesList, entry, schema.Properties.Messages.Items)
	}

	want := `{"id":"101","token":"abcd1234","time":"2026-01-01T00:00:00Z","actor_type":"users","actor_id":"bob","actor_name":"Bob",` +
		`"kind":"comment","text":"@Bob plan.pdf @Team shared.txt","is_replyable":true,"reference_id":"ref1",` +
		`"expiration_timestamp":1767312000,"reactions":{"👍":2,"❤️":1},"reactions_self":["👍"],"markdown":true,` +
		`"last_edit_actor_type":"users","last_edit_actor_id":"carol","last_edit_actor_name":"Carol",` +
		`"last_edit_time":"2026-01-01T00:05:00Z","silent":false,"parent_id":"100",` +
		`"objects":[` +
		`{"key":"actor","type":"user","id":"bob","name":"Bob","details":{"server":"https://remote.cloud.example"}},` +
		`{"key":"file","type":"file","name":"plan.pdf","details":{"path":"Talk/plan.pdf","link":"https://cloud.example/f/77",` +
		`"size":2048,"mimetype":"application/pdf","preview_available":"yes","permissions":27,"mtime":1767225000,"etag":"abc"}},` +
		`{"key":"room","type":"call","name":"Team","details":{"call_type":"group","icon_url":"https://cloud.example/avatar/team/64"}},` +
		`{"key":"share","type":"file","name":"shared.txt"}],` +
		`"parent":{"id":"100","time":"2025-12-31T23:50:00Z","actor_type":"users","actor_id":"dora","actor_name":"Dora",` +
		`"kind":"comment","text":"first","token":"abcd1234","reactions":{"🎉":3},"reactions_self":["🎉"],"markdown":false,"parent_id":"99"}}`
	var expected map[string]any
	_ = json.Unmarshal([]byte(want), &expected)
	if !reflect.DeepEqual(messages[0], expected) {
		t.Errorf("message = %v\nwant    %v", messages[0], expected)
	}
	for _, canary := range []string{"mtok-canary", "mpw-canary", "room-token-canary", "share-token-canary",
		messageCallLink, messageShareLink, `"x"`} {
		if strings.Contains(string(result), canary) {
			t.Errorf("the result carries %q", canary)
		}
	}
	if _, ok := messages[1]["expiration_timestamp"]; ok {
		t.Error("an expiration of 0 is reported")
	}
	if parent := messages[1]["parent"]; !reflect.DeepEqual(parent, map[string]any{"id": "101", "deleted": true}) ||
		messages[1]["parent_id"] != "101" {
		t.Errorf("deleted parent = %v", parent)
	}
	query := (*calls)[len(*calls)-1].url.Query()
	for key, value := range map[string]string{"setReadMarker": "0", "markNotificationsAsRead": "0", "noStatusUpdate": "1", "lookIntoFuture": "0"} {
		if query.Get(key) != value {
			t.Errorf("%s = %q", key, query.Get(key))
		}
	}
	if query.Has("timeout") {
		t.Error("the request waits for new messages")
	}
}

func TestTalkMessagesWithoutNewFieldsKeepTheirOutput(t *testing.T) {
	messagesServer(t, messageJSON("5", "{actor} hi", `{"actor":{"type":"user","id":"bob","name":"Bob"}}`))
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.messages.list", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":1,"messages":[{"actor_id":"bob","actor_name":"Bob","actor_type":"users","id":"5","kind":"comment",` +
		`"objects":[{"id":"bob","key":"actor","name":"Bob","type":"user"}],"text":"@Bob hi","time":"2026-01-01T00:00:00Z"}]}`
	if string(response.Result) != want {
		t.Errorf("result = %s", response.Result)
	}
}

func TestTalkRichObjectDetailsNeverCarryAccessValues(t *testing.T) {
	members := func(raw string) map[string]json.RawMessage {
		var result map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	object := richObjectOf("k", members(`{"type":"file","id":"7","name":"n","Token":"a","guest-token":"b","password":"c",`+
		`"link":"https://cloud.example/index.php/call/abc","website":"https://cloud.example/s/abc","server":"https://cloud.example",`+
		`"ok":"v\u0007x","empty":""}`))
	if want := map[string]any{"server": "https://cloud.example", "ok": "vx"}; !reflect.DeepEqual(object.Details, want) || object.ID != "" {
		t.Errorf("details = %v, id = %q", object.Details, object.ID)
	}
	if object := richObjectOf("k", members(`{"type":"file","name":"n"}`)); object.Details != nil || object.OmittedFields != nil {
		t.Errorf("object = %+v", object)
	}
}

func TestTalkRichObjectDetailsOverBoundsAreOmittedAndFlagged(t *testing.T) {
	var many []string
	for i := 0; i <= maxRichDetails; i++ {
		many = append(many, `"m`+strconv.Itoa(i)+`":"v"`)
	}
	atBound := strings.Join(many[:maxRichDetails], ",")
	cases := map[string]string{
		"too many members": `{"type":"file","name":"n",` + strings.Join(many, ",") + `}`,
		"long value":       `{"type":"file","name":"n","path":"` + strings.Repeat("a", maxValueLength+1) + `"}`,
	}
	for name, raw := range cases {
		var members map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &members)
		object := richObjectOf("k", members)
		if object.Details != nil || !reflect.DeepEqual(object.OmittedFields, []string{"details"}) || object.Name != "n" {
			t.Errorf("%s: %+v", name, object)
		}
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"type":"file","name":"n",`+atBound+`,"path":"`+strings.Repeat("a", maxValueLength)+`"}`), &members)
	delete(members, "m0")
	if object := richObjectOf("k", members); len(object.Details) != maxRichDetails || object.OmittedFields != nil {
		t.Errorf("at the bound: %d members, %v", len(object.Details), object.OmittedFields)
	}
}

func TestTalkMessagesFlagTooManyReactions(t *testing.T) {
	var reactions []string
	for i := 0; i <= maxReactions; i++ {
		reactions = append(reactions, `"e`+strconv.Itoa(i)+`":1`)
	}
	item := strings.Replace(messageJSON("5", "x", `[]`), `"messageParameters"`, `"reactions":{`+strings.Join(reactions, ",")+
		`},"reactionsSelf":["a","b"],"x":0,"messageParameters"`, 1)
	messagesServer(t, item)
	_, messages := listMessages(t)
	if _, ok := messages[0]["reactions"]; ok || !reflect.DeepEqual(messages[0]["omitted_fields"], []any{"reactions"}) ||
		!reflect.DeepEqual(messages[0]["reactions_self"], []any{"a", "b"}) {
		t.Errorf("message = %v", messages[0])
	}
}

func TestTalkLastMessageFollowsTheExtendedFormWithoutParent(t *testing.T) {
	start, end := strings.Index(fullRoomJSON, `"lastMessage":`), strings.Index(fullRoomJSON, `,"objectType"`)
	room := fullRoomJSON[:start] + `"lastMessage":` + fullMessageJSON + fullRoomJSON[end:]
	widthServer(t, room, fullParticipantJSON)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talk.rooms.get", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatal(err)
	}
	checkDeclared(t, talkRoomsGet, response.Result, talkRoomsGet.OutputSchema)
	var got struct {
		LastMessage map[string]any `json:"last_message"`
	}
	_ = json.Unmarshal(response.Result, &got)
	last := got.LastMessage
	for _, key := range []string{"parent", "reactions_self"} {
		if _, ok := last[key]; ok {
			t.Errorf("last_message carries %s", key)
		}
	}
	if last["parent_id"] != "100" || last["markdown"] != true || last["last_edit_time"] != "2026-01-01T00:05:00Z" ||
		!reflect.DeepEqual(last["reactions"], map[string]any{"👍": float64(2), "❤️": float64(1)}) {
		t.Errorf("last_message = %v", last)
	}
	if strings.Contains(string(response.Result), "token-canary") {
		t.Errorf("result carries an access value: %s", response.Result)
	}
}
