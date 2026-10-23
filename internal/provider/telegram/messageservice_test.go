package telegram

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	svcUser    = `{"id":8,"is_bot":false,"first_name":"Bo"}`
	svcBot     = `{"id":9,"is_bot":true,"first_name":"Biz","username":"biz_bot"}`
	svcShared  = `"new_chat_members":[` + svcUser + `,` + svcBot + `],"left_chat_member":{"id":10,"is_bot":false,"first_name":"Le"},`
	svcCommon1 = `"chat_owner_left":{"new_owner":{"id":11,"is_bot":false,"first_name":"No"}},` +
		`"chat_owner_changed":{"new_owner":{"id":12,"is_bot":false,"first_name":"Ow"}},` +
		`"new_chat_title":"New title","delete_chat_photo":true,"group_chat_created":true,"supergroup_chat_created":true,` +
		`"channel_chat_created":true,"message_auto_delete_timer_changed":{"message_auto_delete_time":0},` +
		`"migrate_to_chat_id":-1009999999999,"migrate_from_chat_id":-4242,` +
		`"connected_website":"example.org","write_access_allowed":{"from_request":true,"web_app_name":"App","from_attachment_menu":true},` +
		`"proximity_alert_triggered":{"traveler":` + svcUser + `,"watcher":` + svcBot + `,"distance":0},` +
		`"boost_added":{"boost_count":3},"direct_message_price_changed":{"are_direct_messages_enabled":false,"direct_message_star_count":7},` +
		`"forum_topic_created":{"name":"Topic","icon_color":7322096,"icon_custom_emoji_id":"EM1","is_name_implicit":true},` +
		`"forum_topic_edited":{"name":"Renamed","icon_custom_emoji_id":""},"forum_topic_closed":{},"forum_topic_reopened":{},` +
		`"general_forum_topic_hidden":{},"general_forum_topic_unhidden":{},"managed_bot_created":{"bot":` + svcBot + `},` +
		`"paid_message_price_changed":{"paid_message_star_count":5},"video_chat_scheduled":{"start_date":1900000000},` +
		`"video_chat_started":{},"video_chat_ended":{"duration":0},` +
		`"video_chat_participants_invited":{"users":[` + svcUser + `]},"web_app_data":{"data":"d","button_text":"b"}`

	svcPhotoIn  = `"new_chat_photo":[{"file_id":"FILE-NCP","file_unique_id":"U1","width":5,"height":6,"file_size":7}],`
	svcPhotoOut = `"new_chat_photo":[{"file_ref":"REF:FILE-NCP","file_unique_id":"U1","width":5,"height":6,"file_size":7}],`

	svcPinned    = `"pinned_message":{"message_id":55,"date":5,"chat":` + headTeam + `,"text":"pin","pinned_message":{"message_id":1,"date":2}},`
	svcPinnedOut = `"pinned_message":{"message_id":55,"date":5,"text":"pin","chat_info":` + headTeam +
		`,"omitted_fields":["pinned_message"]},`
)

func svcFixtures() (in, out string) {
	return svcShared + svcPhotoIn + svcPinned + svcCommon1, svcShared + svcPhotoOut + svcPinnedOut + svcCommon1
}

func listOne(t *testing.T, scope, message string) (map[string]any, *Client) {
	t.Helper()
	var calls []recorded
	updates := `[{"update_id":1,"message":{` + message + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), scope)
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	return jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any), client
}

func TestUpdatesListPrintsEveryServiceMember(t *testing.T) {
	in, want := svcFixtures()
	out, client := listOne(t, "-1001", contentMessage(in)+`,"passport_data":{"data":[],"credentials":{}}`)
	if _, ok := out["passport_data"]; ok {
		t.Error("passport_data is printed")
	}
	delete(out, "chat_info")
	refsToIDs(t, client, "-1001", out)
	expected := decodeObject(t, `{"update_id":1,"type":"message","chat":"-1001","message_id":9,"date":100,`+want+`}`)
	if !reflect.DeepEqual(out, expected) {
		t.Errorf("update = %s\nwant   %s", mustMarshal(out), mustMarshal(expected))
	}
}

func TestInaccessiblePinnedMessagePrintsChatIDAndDate(t *testing.T) {
	out, _ := listOne(t, "-1001", contentMessage(`"pinned_message":{"message_id":55,"date":0,"chat":`+headTeam+`}`))
	want := decodeObject(t, `{"chat_info":`+headTeam+`,"message_id":55,"date":0}`)
	if !reflect.DeepEqual(out["pinned_message"], any(want)) {
		t.Errorf("pinned_message = %v", out["pinned_message"])
	}
}

func TestServiceMembersOfBusinessMessageHaveNoFileRefAndNoChat(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"business_message":{"business_connection_id":"b1","message_id":5,"date":100,` +
		`"chat":{"id":777123,"type":"private"},` + svcPhotoIn + `"pinned_message":{"message_id":4,"date":0,"chat":{"id":777123,"type":"private"}}}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, forbidden := range []string{"777123", `"file_ref"`, "FILE-NCP"} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(encoded, `"new_chat_photo":[{"file_unique_id":"U1"`) {
		t.Errorf("photo sizes are missing: %s", encoded)
	}
}

func TestMigrationIDsNeverBecomeATarget(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":9,"date":1,"chat":{"id":-4242,"type":"group"},"migrate_to_chat_id":-1001}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got["updates"].([]updateOut)); n != 0 || got["skipped"] != 1 {
		t.Errorf("updates = %v, skipped = %v", got["updates"], got["skipped"])
	}
}

func TestServiceTextsAreCleanedAndBounded(t *testing.T) {
	out, _ := listOne(t, "-1001", contentMessage(`"new_chat_title":"a\u0007b","connected_website":"x\u001by",`+
		`"web_app_data":{"data":"l1\nl2\u0000","button_text":"b\u0007"},"forum_topic_created":{"name":"n\u0007","icon_color":1}`))
	if out["new_chat_title"] != "ab" || out["connected_website"] != "xy" ||
		out["web_app_data"].(map[string]any)["data"] != "l1\nl2" || out["web_app_data"].(map[string]any)["button_text"] != "b" ||
		out["forum_topic_created"].(map[string]any)["name"] != "n" {
		t.Errorf("update = %v", out)
	}
}

const (
	svcPay = `"successful_payment":{"currency":"EUR","total_amount":1999,"invoice_payload":"pl","subscription_expiration_date":1900000000,` +
		`"is_recurring":true,"is_first_recurring":true,"shipping_option_id":"so","order_info":{"name":"Nm","phone_number":"+10",` +
		`"email":"a@b.c","shipping_address":{"country_code":"DE","state":"St","city":"Ci","street_line1":"L1","street_line2":"L2",` +
		`"post_code":"PC"}},"telegram_payment_charge_id":"TCH","provider_payment_charge_id":"PCH"},` +
		`"refunded_payment":{"currency":"XTR","total_amount":0,"invoice_payload":"pl","telegram_payment_charge_id":"TCH2",` +
		`"provider_payment_charge_id":"PCH2"},`

	svcSharedIn = `"users_shared":{"request_id":1,"users":[{"user_id":21,"first_name":"A","last_name":"B","username":"ab",` +
		`"photo":[{"file_id":"FILE-US","file_unique_id":"U2","width":1,"height":2}]}]},` +
		`"chat_shared":{"request_id":2,"chat_id":-100777,"title":"T","username":"tu",` +
		`"photo":[{"file_id":"FILE-CS","file_unique_id":"U3","width":3,"height":4}]},`
	svcSharedOut = `"users_shared":{"request_id":1,"users":[{"user_id":21,"first_name":"A","last_name":"B","username":"ab",` +
		`"photo":[{"file_ref":"REF:FILE-US","file_unique_id":"U2","width":1,"height":2}]}]},` +
		`"chat_shared":{"request_id":2,"chat_id":-100777,"title":"T","username":"tu",` +
		`"photo":[{"file_ref":"REF:FILE-CS","file_unique_id":"U3","width":3,"height":4}]},`

	svcGiftIn = `"gift":{"gift":{"id":"G1","sticker":{"file_id":"FILE-GS","file_unique_id":"U4","type":"regular","width":1,"height":2},` +
		`"star_count":50,"total_count":10,"remaining_count":4},"owned_gift_id":"OWN1","convert_star_count":9,` +
		`"prepaid_upgrade_star_count":3,"is_upgrade_separate":true,"can_be_upgraded":true,"text":"hi",` +
		`"entities":[{"type":"bold","offset":0,"length":1}],"is_private":true,"unique_gift_number":6},` +
		`"gift_upgrade_sent":{"gift":{"id":"G2","star_count":1}},` +
		`"unique_gift":{"gift":{"gift_id":"G1","base_name":"Base","name":"Base-1","number":1,"model":{"name":"M",` +
		`"sticker":{"file_id":"FILE-UM","file_unique_id":"U5","type":"regular"},"rarity_per_mille":5},` +
		`"backdrop":{"name":"B","colors":{"center_color":1,"edge_color":2,"symbol_color":3,"text_color":4},"rarity_per_mille":7},` +
		`"is_premium":true,"is_burned":true,"is_from_blockchain":true},"origin":"upgrade","text":"ut","is_private":true,` +
		`"last_resale_currency":"XTR","last_resale_amount":9,"owned_gift_id":"OWN2","transfer_star_count":8,` +
		`"next_transfer_date":1900000001},`
	svcGiftOut = `"gift":{"gift":{"id":"G1","sticker":{"kind":"sticker","file_ref":"REF:FILE-GS","file_unique_id":"U4","type":"regular","width":1,"height":2},` +
		`"star_count":50,"total_count":10,"remaining_count":4},"owned_gift_id":"OWN1","convert_star_count":9,` +
		`"prepaid_upgrade_star_count":3,"is_upgrade_separate":true,"can_be_upgraded":true,"text":"hi",` +
		`"entities":[{"type":"bold","offset":0,"length":1}],"is_private":true,"unique_gift_number":6},` +
		`"gift_upgrade_sent":{"gift":{"id":"G2","star_count":1}},` +
		`"unique_gift":{"gift":{"gift_id":"G1","base_name":"Base","name":"Base-1","number":1,"model":{"name":"M",` +
		`"sticker":{"kind":"sticker","file_ref":"REF:FILE-UM","file_unique_id":"U5","type":"regular"},"rarity_per_mille":5},` +
		`"backdrop":{"name":"B","colors":{"center_color":1,"edge_color":2,"symbol_color":3,"text_color":4},"rarity_per_mille":7},` +
		`"is_premium":true,"is_burned":true,"is_from_blockchain":true},"origin":"upgrade","text":"ut","is_private":true,` +
		`"last_resale_currency":"XTR","last_resale_amount":9,"owned_gift_id":"OWN2","transfer_star_count":8,` +
		`"next_transfer_date":1900000001},`

	svcBackgroundIn = `"chat_background_set":{"type":{"type":"pattern","document":{"file_id":"FILE-BG","file_unique_id":"U6",` +
		`"file_name":"w.png","mime_type":"image/png","file_size":3},"fill":{"type":"gradient","top_color":0,"bottom_color":16777215,` +
		`"rotation_angle":0},"intensity":0,"is_inverted":true,"is_moving":true}},`
	svcBackgroundOut = `"chat_background_set":{"type":{"type":"pattern","document":{"kind":"document","file_ref":"REF:FILE-BG","file_unique_id":"U6",` +
		`"file_name":"w.png","mime_type":"image/png","file_size":3},"fill":{"type":"gradient","top_color":0,"bottom_color":16777215,` +
		`"rotation_angle":0},"intensity":0,"is_inverted":true,"is_moving":true}},`

	svcTasksIn = `"checklist_tasks_done":{"checklist_message":{"message_id":30,"date":20,"chat":` + headTeam + `,` +
		`"checklist":{"title":"L","tasks":[{"id":1,"text":"t"}]},"reply_to_message":{"message_id":2,"text":"DEEPERCANARY"}},` +
		`"marked_as_done_task_ids":[1],"marked_as_not_done_task_ids":[0,2]},` +
		`"checklist_tasks_added":{"checklist_message":{"message_id":30,"date":20,"chat":` + headTeam + `},` +
		`"tasks":[{"id":3,"text":"n","completed_by_user":` + svcUser + `,"completion_date":5}]},`
	svcTasksOut = `"checklist_tasks_done":{"checklist_message":{"message_id":30,"date":20,"chat_info":` + headTeam + `,` +
		`"checklist":{"title":"L","tasks":[{"id":1,"text":"t"}]}},` +
		`"marked_as_done_task_ids":[1],"marked_as_not_done_task_ids":[0,2]},` +
		`"checklist_tasks_added":{"checklist_message":{"message_id":30,"date":20,"chat_info":` + headTeam + `},` +
		`"tasks":[{"id":3,"text":"n","completed_by_user":` + svcUser + `,"completion_date":5}]},`

	svcCommunity = `"community_chat_added":{"community":{"id":77,"name":"Comm"}},"community_chat_joined":{"community":{"id":78,"name":"Join"}},` +
		`"community_chat_removed":{},"giveaway_created":{"prize_star_count":100},`

	svcGiveawayIn = `"giveaway_completed":{"winner_count":3,"unclaimed_prize_count":1,"giveaway_message":{"message_id":31,"date":21,` +
		`"chat":` + headTeam + `,"text":"gw"},"is_star_giveaway":true},` +
		`"poll_option_added":{"poll_message":{"message_id":32,"date":22,"chat":` + headTeam + `,"text":"p"},"option_persistent_id":"o1",` +
		`"option_text":"Opt","option_text_entities":[{"type":"bold","offset":0,"length":1}]},` +
		`"poll_option_deleted":{"poll_message":{"message_id":33,"date":0,"chat":` + headTeam + `},"option_persistent_id":"o2","option_text":"Gone"},`
	svcGiveawayOut = `"giveaway_completed":{"winner_count":3,"unclaimed_prize_count":1,"giveaway_message":{"message_id":31,"date":21,` +
		`"text":"gw","chat_info":` + headTeam + `},"is_star_giveaway":true},` +
		`"poll_option_added":{"poll_message":{"message_id":32,"date":22,"text":"p","chat_info":` + headTeam + `},"option_persistent_id":"o1",` +
		`"option_text":"Opt","option_text_entities":[{"type":"bold","offset":0,"length":1}]},` +
		`"poll_option_deleted":{"poll_message":{"chat_info":` + headTeam + `,"message_id":33,"date":0},"option_persistent_id":"o2","option_text":"Gone"},`

	svcPostIn = `"suggested_post_approved":{"suggested_post_message":{"message_id":34,"date":23,"chat":` + headTeam + `,"text":"sp"},` +
		`"price":{"currency":"XTR","amount":5},"send_date":1900000002},` +
		`"suggested_post_approval_failed":{"price":{"currency":"TON","amount":6}},"suggested_post_declined":{"comment":"no"},` +
		`"suggested_post_paid":{"currency":"TON","amount":1000000000,"star_amount":{"amount":4,"nanostar_amount":5}},` +
		`"suggested_post_refunded":{"reason":"post_deleted"}`
	svcPostOut = `"suggested_post_approved":{"suggested_post_message":{"message_id":34,"date":23,"text":"sp","chat_info":` + headTeam + `},` +
		`"price":{"currency":"XTR","amount":5},"send_date":1900000002},` +
		`"suggested_post_approval_failed":{"price":{"currency":"TON","amount":6}},"suggested_post_declined":{"comment":"no"},` +
		`"suggested_post_paid":{"currency":"TON","amount":1000000000,"star_amount":{"amount":4,"nanostar_amount":5}},` +
		`"suggested_post_refunded":{"reason":"post_deleted"}`
)

func svcEventFixtures() (in, out string) {
	return svcPay + svcSharedIn + svcGiftIn + svcBackgroundIn + svcTasksIn + svcCommunity + svcGiveawayIn + svcPostIn,
		svcPay + svcSharedOut + svcGiftOut + svcBackgroundOut + svcTasksOut + svcCommunity + svcGiveawayOut + svcPostOut
}

func TestUpdatesListPrintsEveryPaymentGiftAndEventServiceMember(t *testing.T) {
	in, want := svcEventFixtures()
	out, client := listOne(t, "-1001", contentMessage(in))
	encoded := string(mustMarshal(out))
	for _, leaked := range []string{"DEEPERCANARY", "FILE-"} {
		if strings.Contains(encoded, leaked) {
			t.Errorf("output leaks %s", leaked)
		}
	}
	if omitted := out["omitted_fields"]; omitted != nil {
		t.Errorf("omitted_fields = %v", omitted)
	}
	delete(out, "chat_info")
	refsToIDs(t, client, "-1001", out)
	expected := decodeObject(t, `{"update_id":1,"type":"message","chat":"-1001","message_id":9,"date":100,`+want+`}`)
	if !reflect.DeepEqual(out, expected) {
		t.Errorf("update = %s\nwant   %s", mustMarshal(out), mustMarshal(expected))
	}
}

func TestChatBackgroundVariantsKeepZeroValues(t *testing.T) {
	out, _ := listOne(t, "-1001", contentMessage(`"chat_background_set":{"type":{"type":"fill","dark_theme_dimming":0,`+
		`"fill":{"type":"solid","color":0}}}`))
	want := decodeObject(t, `{"type":{"type":"fill","dark_theme_dimming":0,"fill":{"type":"solid","color":0}}}`)
	if !reflect.DeepEqual(out["chat_background_set"], any(want)) {
		t.Errorf("chat_background_set = %v", out["chat_background_set"])
	}
	out, _ = listOne(t, "-1001", contentMessage(`"chat_background_set":{"type":{"type":"fill","dark_theme_dimming":10,`+
		`"fill":{"type":"freeform_gradient","colors":[1,2,3]}}}`))
	if fill := out["chat_background_set"].(map[string]any)["type"].(map[string]any)["fill"].(map[string]any); !reflect.DeepEqual(fill["colors"], []any{1.0, 2.0, 3.0}) {
		t.Errorf("fill = %v", fill)
	}
	out, _ = listOne(t, "-1001", contentMessage(`"chat_background_set":{"type":{"type":"chat_theme","theme_name":"T"}}`))
	if !reflect.DeepEqual(out["chat_background_set"], any(decodeObject(t, `{"type":{"type":"chat_theme","theme_name":"T"}}`))) {
		t.Errorf("chat_background_set = %v", out["chat_background_set"])
	}
}

func TestServiceMessagesNestOneLevelAndOmitDeeperOnes(t *testing.T) {
	// The replied-to message is itself a service message that carries a message; that one is left out and named.
	out, _ := listOne(t, "-1001", contentMessage(`"reply_to_message":{"message_id":3,"date":90,"chat":`+headTeam+`,`+
		`"checklist_tasks_done":{"checklist_message":{"message_id":2,"date":1,"chat":`+headTeam+`,"text":"DEEPERCANARY"}},`+
		`"pinned_message":{"message_id":4,"date":1,"chat":`+headTeam+`}}`))
	encoded := string(mustMarshal(out))
	if strings.Contains(encoded, "DEEPERCANARY") {
		t.Errorf("a third level leaked: %s", encoded)
	}
	reply := out["reply_to_message"].(map[string]any)
	if !reflect.DeepEqual(reply["omitted_fields"], []any{"pinned_message", "checklist_tasks_done.checklist_message"}) &&
		!reflect.DeepEqual(reply["omitted_fields"], []any{"checklist_tasks_done.checklist_message", "pinned_message"}) {
		t.Errorf("omitted_fields = %v", reply["omitted_fields"])
	}
}

func TestBusinessServiceGiftsAndBackgroundsHaveNoFileRef(t *testing.T) {
	var calls []recorded
	in, _ := svcEventFixtures()
	updates := `[{"update_id":1,"business_message":{"business_connection_id":"b1","message_id":5,"date":100,` +
		`"chat":{"id":777123,"type":"private"},` + in + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, forbidden := range []string{"777123", `"file_ref"`, "FILE-"} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output contains %q", forbidden)
		}
	}
	if !strings.Contains(encoded, `"owned_gift_id":"OWN1"`) || !strings.Contains(encoded, `"owned_gift_id":"OWN2"`) {
		t.Errorf("owned_gift_id is missing: %s", encoded)
	}
}

func TestOversizeServiceMembersAreOmittedAndNamed(t *testing.T) {
	var users []string
	for i := 0; i < 100; i++ {
		users = append(users, `{"id":8,"is_bot":false,"first_name":"`+strings.Repeat("n", 200)+`"}`)
	}
	out, _ := listOne(t, "-1001", contentMessage(`"video_chat_participants_invited":{"users":[`+strings.Join(users, ",")+`]},`+
		`"web_app_data":{"data":"`+strings.Repeat("\U0001F600", maxMessageLength)+`","button_text":"b"}`))
	if _, ok := out["web_app_data"]; ok {
		t.Error("an oversized web_app_data is printed")
	}
	if got := out["omitted_fields"]; !reflect.DeepEqual(got, []any{"video_chat_participants_invited", "web_app_data"}) &&
		!reflect.DeepEqual(got, []any{"web_app_data", "video_chat_participants_invited"}) {
		t.Errorf("omitted_fields = %v", got)
	}
}

// Telegram sends one kind of service event per message, so a page of 100 messages needs at most the size of its largest
// kind. A page of each kind at maximum size fits the response bound.
func TestFullPageOfServiceMessagesFitsTheResponseBound(t *testing.T) {
	text := strings.Repeat("t", 4096)
	nested := func(id string) string {
		return `{"message_id":` + id + `,"date":90,"chat":` + headTeam + `,"text":"` + text + `","caption":"` + strings.Repeat("c", 1024) + `"}`
	}
	var members, shared []string
	for i := 0; i < 100; i++ {
		members = append(members, `{"id":8,"is_bot":false,"first_name":"`+strings.Repeat("f", 64)+`","last_name":"`+strings.Repeat("l", 64)+
			`","username":"`+strings.Repeat("u", 32)+`"}`)
	}
	for i := 0; i < 10; i++ {
		shared = append(shared, `{"user_id":21,"first_name":"`+strings.Repeat("f", 64)+`","last_name":"`+strings.Repeat("l", 64)+
			`","username":"`+strings.Repeat("u", 32)+`","photo":[{"file_id":"`+strings.Repeat("F", 90)+`","file_unique_id":"`+
			strings.Repeat("U", 16)+`","width":640,"height":640,"file_size":123456}]}`)
	}
	services := []string{
		`"new_chat_members":[` + strings.Join(members, ",") + `]`,
		`"users_shared":{"request_id":1,"users":[` + strings.Join(shared, ",") + `]}`,
		`"pinned_message":` + nested("55"),
		`"checklist_tasks_done":{"checklist_message":` + nested("56") + `}`,
		`"giveaway_completed":{"winner_count":1,"giveaway_message":` + nested("57") + `}`,
		`"poll_option_added":{"poll_message":` + nested("58") + `,"option_persistent_id":"o","option_text":"` + strings.Repeat("o", 100) + `"}`,
		`"suggested_post_paid":{"suggested_post_message":` + nested("59") + `,"currency":"XTR"}`,
		`"web_app_data":{"data":"` + strings.Repeat("d", 4096) + `","button_text":"b"}`,
	}
	for _, service := range services {
		var list []string
		for i := 1; i <= maxUpdateLimit; i++ {
			list = append(list, `{"update_id":`+strconv.Itoa(i)+`,"message":{`+contentMessage(service)+`}}`)
		}
		updates := "[" + strings.Join(list, ",") + "]"
		t.Logf("100 messages of %.30s: %d bytes of %d", service, len(updates), updatesResponseBytes)
		if len(updates) > updatesResponseBytes {
			t.Fatalf("a full page needs %d bytes, over the %d bound", len(updates), updatesResponseBytes)
		}
		var calls []recorded
		client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
		got, err := client.ListUpdates(context.Background(), maxUpdateLimit, 0)
		if err != nil {
			t.Fatalf("a full page does not fit: %v", err)
		}
		if n := len(got["updates"].([]updateOut)); n != maxUpdateLimit {
			t.Errorf("updates = %d", n)
		}
	}
}
