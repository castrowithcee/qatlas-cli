package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	headUserA = `{"id":8,"is_bot":false,"first_name":"Bo"}`
	headTeam  = `{"id":-1001,"type":"supergroup","title":"Team"}`

	// headJSON carries every header member of a Message; the nested reply carries a reply of its own as a canary.
	headJSON = `"direct_messages_topic":{"topic_id":3,"user":` + headUserA + `},` +
		`"sender_chat":{"id":-1009,"type":"channel","title":"Other"},"sender_boost_count":2,` +
		`"sender_business_bot":{"id":9,"is_bot":true,"first_name":"Biz"},"sender_tag":"vip",` +
		`"receiver_user":{"id":10,"is_bot":false,"first_name":"Rec"},"ephemeral_message_id":77,` +
		`"guest_query_id":"GUESTQ","forward_origin":{"type":"channel","date":50,` +
		`"chat":{"id":-1010,"type":"channel","title":"Src"},"message_id":5,"author_signature":"Sig"},` +
		`"is_topic_message":true,"is_automatic_forward":true,` +
		`"reply_to_message":{"message_id":3,"date":90,"chat":` + headTeam + `,"text":"orig","from":` + headUserA + `,` +
		`"sender_tag":"t2","reply_to_message":{"message_id":2,"text":"DEEPERCANARY"}},` +
		`"external_reply":{"origin":{"type":"user","date":40,"sender_user":{"id":11,"is_bot":false,"first_name":"Ext"}},` +
		`"chat":{"id":-1011,"type":"group","title":"Ext chat"},"message_id":6},` +
		`"quote":{"text":"q","position":0,"is_manual":true},` +
		`"reply_to_story":{"chat":{"id":-1012,"type":"channel"},"id":4},"reply_to_checklist_task_id":12,` +
		`"reply_to_poll_option_id":"opt","via_bot":{"id":12,"is_bot":true,"first_name":"Via"},` +
		`"guest_bot_caller_user":{"id":13,"is_bot":false,"first_name":"Caller"},` +
		`"guest_bot_caller_chat":{"id":-1013,"type":"group"},"edit_date":120,"has_protected_content":true,` +
		`"is_from_offline":true,"is_paid_post":true,"media_group_id":"mg1","author_signature":"Auth","paid_star_count":5`

	// expectedHead is headJSON as printed; GUEST stands for guest_query_ref.
	expectedHead = `"direct_messages_topic":{"topic_id":3,"user":` + headUserA + `},` +
		`"sender_chat":{"id":-1009,"type":"channel","title":"Other"},"sender_boost_count":2,` +
		`"sender_business_bot":{"id":9,"is_bot":true,"first_name":"Biz"},"sender_tag":"vip",` +
		`"receiver_user":{"id":10,"is_bot":false,"first_name":"Rec"},"ephemeral_message_id":77,` +
		`"guest_query_ref":"GUEST","chat_info":` + headTeam + `,"forward_origin":{"type":"channel","date":50,` +
		`"chat":{"id":-1010,"type":"channel","title":"Src"},"message_id":5,"author_signature":"Sig"},` +
		`"is_topic_message":true,"is_automatic_forward":true,` +
		`"reply_to_message":{"message_id":3,"date":90,"from":` + headUserA + `,"text":"orig","chat_info":` + headTeam +
		`,"sender_tag":"t2"},` +
		`"external_reply":{"origin":{"type":"user","date":40,"sender_user":{"id":11,"is_bot":false,"first_name":"Ext"}},` +
		`"chat":{"id":-1011,"type":"group","title":"Ext chat"},"message_id":6},` +
		`"quote":{"text":"q","position":0,"is_manual":true},` +
		`"reply_to_story":{"chat":{"id":-1012,"type":"channel"},"id":4},"reply_to_checklist_task_id":12,` +
		`"reply_to_poll_option_id":"opt","via_bot":{"id":12,"is_bot":true,"first_name":"Via"},` +
		`"guest_bot_caller_user":{"id":13,"is_bot":false,"first_name":"Caller"},` +
		`"guest_bot_caller_chat":{"id":-1013,"type":"group"},"edit_date":120,"has_protected_content":true,` +
		`"is_from_offline":true,"is_paid_post":true,"media_group_id":"mg1","author_signature":"Auth","paid_star_count":5`
)

// checkGuestRef checks that the guest_query_ref of got is a guest query reference of binding that redeems to
// GUESTQ and is not accepted as another kind or for another target.
func checkGuestRef(t *testing.T, client *Client, binding string, got map[string]any) map[string]any {
	t.Helper()
	ref, _ := got["guest_query_ref"].(string)
	if ref == "" {
		t.Fatalf("no guest_query_ref in %v", got)
	}
	parsed, err := parseRef(resolvedWith(binding), refGuestQuery, ref)
	if err != nil || parsed.binding != binding {
		t.Fatalf("guest_query_ref = %v %+v", err, parsed)
	}
	if id, err := client.checkRef(parsed); err != nil || id != "GUESTQ" {
		t.Errorf("guest_query_ref redeems %q, %v", id, err)
	}
	if _, err := parseRef(resolvedWith(binding), refFile, ref); err == nil {
		t.Error("a guest query reference passed as a file reference")
	}
	if _, err := parseRef(resolvedWith("-77"), refGuestQuery, ref); err == nil {
		t.Error("a guest query reference passed for another target")
	}
	return got
}

func wantObject(t *testing.T, text, ref string) map[string]any {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal([]byte(strings.Replace(text, "GUEST", ref, 1)), &want); err != nil {
		t.Fatal(err)
	}
	return want
}

func TestUpdatesListPrintsEveryMessageHeaderMember(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"edited_message":{"message_id":9,"date":100,"message_thread_id":4,"chat":` + headTeam +
		`,"from":{"id":7,"is_bot":false,"first_name":"Ann"},"text":"hi",` +
		headJSON + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getUpdates" {
		t.Fatalf("calls = %+v", calls)
	}
	encoded := string(mustMarshal(got))
	if strings.Contains(encoded, "DEEPERCANARY") || strings.Contains(encoded, "GUESTQ\"") {
		t.Errorf("output leaks: %s", encoded)
	}
	out := jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any)
	ref, _ := out["guest_query_ref"].(string)
	checkGuestRef(t, client, "-1001", out)
	want := wantObject(t, `{"update_id":1,"type":"edited_message","chat":"-1001","message_id":9,"date":100,`+
		`"message_thread_id":4,"reply_to_message_id":3,"from":{"id":7,"is_bot":false,"first_name":"Ann"},"text":"hi",`+
		expectedHead+`}`, ref)
	if !reflect.DeepEqual(out, want) {
		t.Errorf("update = %s\nwant   %s", mustMarshal(out), mustMarshal(want))
	}
}

func TestUpdatesListBusinessMessageShowsNoRawChatAndNoFiles(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"business_message":{"message_id":5,"business_connection_id":"b1","date":100,` +
		`"chat":{"id":777123,"type":"private","first_name":"Cust"},"from":{"id":42,"is_bot":false,"first_name":"Cust"},` +
		`"guest_query_id":"GQ","photo":[{"file_id":"BUSINESSPHOTO","file_size":3}],` +
		`"forward_origin":{"type":"user","date":5,"sender_user":{"id":43,"is_bot":false,"first_name":"Orig"}},` +
		`"external_reply":{"chat":{"id":777123,"type":"private"},"message_id":2},` +
		`"reply_to_message":{"message_id":4,"date":90,"chat":{"id":777123,"type":"private"},` +
		`"photo":[{"file_id":"REPLYPHOTO","file_size":4}],"text":"quoted"}}}]`
	client, resolved := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, forbidden := range []string{"777123", `"file_ref"`, "BUSINESSPHOTO", "REPLYPHOTO", `"chat_info"`} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	out := jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any)
	if out["business_chat_ref"] == "" || out["business_chat_ref"] == nil || out["chat"] != "business/b1" {
		t.Errorf("update = %v", out)
	}
	reply, _ := out["reply_to_message"].(map[string]any)
	if reply["text"] != "quoted" || out["forward_origin"] == nil || out["media_items"] == nil {
		t.Errorf("update = %s", encoded)
	}
	ref, _ := out["guest_query_ref"].(string)
	parsed, err := parseRef(resolved, refGuestQuery, ref)
	if err != nil || parsed.binding != "business/b1" {
		t.Errorf("guest_query_ref = %v %+v", err, parsed)
	}
}

func TestUpdatesListMessageWithoutHeaderMembersStaysUnchanged(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":2,"date":3,"chat":{"id":-1001},"text":"hi","reply_to_message":{"message_id":1}}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A bare chat identifier adds nothing; a reply adds only its object next to the unchanged identifier.
	want := `{"update_id":1,"type":"message","chat":"-1001","message_id":2,"date":3,"reply_to_message_id":1,"text":"hi",` +
		`"reply_to_message":{"message_id":1}}`
	if s := string(mustMarshal(got["updates"].([]updateOut)[0])); s != want {
		t.Errorf("update = %s\nwant   %s", s, want)
	}
}

func TestPersonalChatMessagesPrintHeaderMembers(t *testing.T) {
	transport := func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "getChatMember" {
			return response(200, `{"ok":true,"result":{"status":"member","user":{"id":7}}}`), nil
		}
		return response(200, `{"ok":true,"result":[{"message_id":1,"date":2,"message_thread_id":4,"text":"hi","chat":`+
			headTeam+`,"from":{"id":7,"is_bot":false,"first_name":"Ann"},`+headJSON+`}]}`), nil
	}
	client, _ := telegramClient(t, "-1001", roundTripFunc(transport))
	got, err := client.PersonalChatMessages(context.Background(), 7, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustMarshal(got)), "DEEPERCANARY") {
		t.Errorf("a second reply level leaked")
	}
	out := jsonRoundTrip(t, got).(map[string]any)["messages"].([]any)[0].(map[string]any)
	ref, _ := out["guest_query_ref"].(string)
	checkGuestRef(t, client, "-1001", out)
	want := wantObject(t, `{"message_id":1,"date":2,"message_thread_id":4,"from":{"id":7,"is_bot":false,"first_name":"Ann"},`+
		`"text":"hi",`+expectedHead+`}`, ref)
	if !reflect.DeepEqual(out, want) {
		t.Errorf("message = %s\nwant    %s", mustMarshal(out), mustMarshal(want))
	}
}

func TestChatsGetPrintsPinnedMessage(t *testing.T) {
	pinned := `{"message_id":55,"date":5,"chat":` + headTeam + `,"text":"pinned","photo":[{"file_id":"PINPHOTO","file_size":3}],` +
		`"from":{"id":7,"is_bot":false,"first_name":"Ann"},` + headJSON + `}`
	out, client, resolved := chatFor(t, `{"id":-1001,"type":"supergroup","pinned_message":`+pinned+`}`)
	if out["pinned_message_id"] != float64(55) {
		t.Errorf("pinned_message_id = %v", out["pinned_message_id"])
	}
	message, _ := out["pinned_message"].(map[string]any)
	if message["message_id"] != float64(55) || message["text"] != "pinned" || message["sender_tag"] != "vip" {
		t.Fatalf("pinned_message = %v", message)
	}
	checkGuestRef(t, client, "-1001", message)
	items, _ := message["media_items"].([]any)
	ref, _ := items[0].(map[string]any)["file_ref"].(string)
	parsed, err := parseRef(resolved, refFile, ref)
	if err != nil || parsed.binding != "-1001" {
		t.Fatalf("file_ref = %v %+v", err, parsed)
	}
	if id, err := client.checkRef(parsed); err != nil || id != "PINPHOTO" {
		t.Errorf("file_ref redeems %q, %v", id, err)
	}
	if strings.Contains(string(mustMarshal(out)), "DEEPERCANARY") {
		t.Errorf("a second reply level leaked")
	}
}

func TestOversizeHeaderMembersAreOmittedAndNamed(t *testing.T) {
	var calls []recorded

	updates := `[{"update_id":1,"message":{"message_id":2,"date":3,"chat":` + headTeam + `,` +
		`"quote":{"text":"` + strings.Repeat("\U0001F600", maxMessageLength) + `","position":1},` +
		`"reply_to_message":{"message_id":1,"text":"` + strings.Repeat("\U0001F600", maxMessageLength) + `"}}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	if u.Quote != nil || u.ReplyToMessage != nil || !reflect.DeepEqual(u.OmittedFields, []string{"quote", "reply_to_message"}) {
		t.Errorf("omitted_fields = %v, quote = %v, reply = %v", u.OmittedFields, u.Quote, u.ReplyToMessage)
	}
}

func TestMessageTextsAreCleaned(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":2,"date":3,"chat":` + headTeam + `,` +
		`"sender_tag":"a\u0007b","author_signature":"s\u001bx","quote":{"text":"q\u0000r","position":2},` +
		`"via_bot":{"id":5,"is_bot":true,"first_name":"V\u0007"}}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	if u.SenderTag != "ab" || u.AuthorSignature != "sx" || u.Quote.Text != "qr" || u.ViaBot.FirstName != "V" {
		t.Errorf("update = %s", mustMarshal(u))
	}
}

// Every printed member of a message is in the output schema, and the tool fields name it.
func TestMessageMembersAreInSchemaAndFields(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":9,"date":100,"chat":` + headTeam + `,` + headJSON + `,` + contentJSON + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	update := jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any)
	reply := update["reply_to_message"].(map[string]any)

	itemProperties := func(schema json.RawMessage, list string) map[string]any {
		var root map[string]any
		if err := json.Unmarshal(schema, &root); err != nil {
			t.Fatal(err)
		}
		items := root["properties"].(map[string]any)[list].(map[string]any)["items"].(map[string]any)
		return items["properties"].(map[string]any)
	}
	check := func(name string, member map[string]any, properties map[string]any, fields string) {
		for key := range member {
			switch key {
			case "update_id", "type", "chat", "message_id", "date", "reply_to_message_id":
				continue
			}
			if _, ok := properties[key]; !ok {
				t.Errorf("%s: %s is not in the output schema", name, key)
			}
			if !strings.Contains(fields, key) {
				t.Errorf("%s: %s is not named in the fields", name, key)
			}
		}
	}
	updateFields := updatesList.Fields[0].Description
	check("updates", update, itemProperties(updatesList.OutputSchema, "updates"), updateFields)
	messageFields := usersPersonalChatMessages.Fields[0].Description
	messageProperties := itemProperties(usersPersonalChatMessages.OutputSchema, "messages")
	check("users", update, messageProperties, messageFields)
	// The members of a nested message are named by the head text too.
	check("reply", reply, messageProperties, messageFields)

	var chat map[string]any
	if err := json.Unmarshal(chatsGet.OutputSchema, &chat); err != nil {
		t.Fatal(err)
	}
	if _, ok := chat["properties"].(map[string]any)["pinned_message"]; !ok {
		t.Error("pinned_message is not in the chats.get output schema")
	}
	found := false
	for _, f := range chatsGet.Fields {
		if f.Name == "pinned_message" {
			found = true
			for key := range reply {
				if !strings.Contains(f.Description, key) {
					t.Errorf("pinned_message: %s is not named in the field", key)
				}
			}
		}
	}
	if !found {
		t.Error("pinned_message is not a chats.get field")
	}
}
