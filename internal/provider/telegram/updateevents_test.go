package telegram

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	evUserA = `{"id":7,"is_bot":false,"first_name":"Al"}`
	evUserB = `{"id":8,"is_bot":false,"first_name":"Bo"}`
	evChat  = `{"id":-100,"type":"supergroup","title":"Team"}`
)

// eventUpdates has one update of every event type, with every documented member filled. The canary strings
// must never reach an output.
const eventUpdates = `[
{"update_id":1,"my_chat_member":{"chat":` + evChat + `,"from":` + evUserA + `,"date":50,` +
	`"old_chat_member":{"status":"member","user":` + evUserB + `,"tag":"t1"},` +
	`"new_chat_member":{"status":"administrator","user":` + evUserB + `,"can_be_edited":true,"can_manage_chat":true,` +
	`"can_post_messages":false,"is_anonymous":false,"custom_title":"Lead"},` +
	`"invite_link":{"invite_link":"https://t.me/+CANARYINVITE","creator":` + evUserA + `,"creates_join_request":false,` +
	`"is_primary":false,"is_revoked":false,"name":"promo","expire_date":99,"member_limit":5,` +
	`"pending_join_request_count":2,"subscription_period":2592000,"subscription_price":10},` +
	`"via_join_request":true,"via_chat_folder_invite_link":true}},
{"update_id":2,"chat_member":{"chat":` + evChat + `,"from":` + evUserA + `,"date":51,` +
	`"old_chat_member":{"status":"restricted","user":` + evUserB + `,"is_member":true,"can_send_messages":false,"until_date":7},` +
	`"new_chat_member":{"status":"kicked","user":` + evUserB + `,"until_date":8}}},
{"update_id":3,"message_reaction":{"chat":` + evChat + `,"message_id":4,"user":` + evUserB + `,` +
	`"actor_chat":{"id":-555,"type":"channel","title":"Other"},"date":60,` +
	`"old_reaction":[{"type":"emoji","emoji":"x"}],` +
	`"new_reaction":[{"type":"custom_emoji","custom_emoji_id":"E1"},{"type":"paid"}]}},
{"update_id":4,"message_reaction_count":{"chat":` + evChat + `,"message_id":4,"date":61,` +
	`"reactions":[{"type":{"type":"emoji","emoji":"x"},"total_count":3},{"type":{"type":"paid"},"total_count":1}]}},
{"update_id":5,"chat_boost":{"chat":` + evChat + `,"boost":{"boost_id":"B1","add_date":10,"expiration_date":20,` +
	`"source":{"source":"giveaway","giveaway_message_id":5,"user":` + evUserB + `,"prize_star_count":50,"is_unclaimed":true}}}},
{"update_id":6,"removed_chat_boost":{"chat":` + evChat + `,"boost_id":"B1","remove_date":30,` +
	`"source":{"source":"premium","user":` + evUserB + `}}},
{"update_id":7,"chat_join_request":{"chat":` + evChat + `,"from":` + evUserB + `,"user_chat_id":8,"date":70,"bio":"hi",` +
	`"invite_link":{"invite_link":"https://t.me/+CANARYINVITE","creator":` + evUserA + `,"creates_join_request":true,` +
	`"is_primary":false,"is_revoked":true,"name":"j"},"query_id":"JQ"}},
{"update_id":8,"callback_query":{"id":"CB1","from":` + evUserB + `,"chat_instance":"ci","data":"d","game_short_name":"g",` +
	`"message":{"message_id":9,"date":80,"chat":` + evChat + `,"text":"press","from":` + evUserA + `}}},
{"update_id":9,"callback_query":{"id":"CB2","from":` + evUserB + `,"inline_message_id":"CANARYINLINE","chat_instance":"ci","data":"d"}},
{"update_id":10,"inline_query":{"id":"IQ","from":` + evUserB + `,"query":"q","offset":"o","chat_type":"sender",` +
	`"location":{"latitude":1.5,"longitude":2.5}}},
{"update_id":11,"chosen_inline_result":{"result_id":"r","from":` + evUserB + `,"query":"q","inline_message_id":"CANARYINLINE",` +
	`"location":{"latitude":1.5,"longitude":2.5}}},
{"update_id":12,"business_connection":{"id":"bc1","user":` + evUserB + `,"user_chat_id":8,"date":90,"is_enabled":true,` +
	`"rights":{"can_reply":true,"can_read_messages":false}}},
{"update_id":13,"guest_message":{"message_id":2,"date":100,"chat":` + evChat + `,"text":"hey","guest_query_id":"GQ"}},
{"update_id":14,"stopped_message_generation":{"chat":` + evChat + `,"message_thread_id":3,"draft_id":42}},
{"update_id":15,"shipping_query":{"id":"CANARYSHIP","from":` + evUserB + `,"invoice_payload":"pl",` +
	`"shipping_address":{"country_code":"XX","state":"S","city":"C","street_line1":"L1","street_line2":"L2","post_code":"P"}}},
{"update_id":16,"pre_checkout_query":{"id":"CANARYPAY","from":` + evUserB + `,"currency":"EUR","total_amount":1250,` +
	`"invoice_payload":"pl","shipping_option_id":"so","order_info":{"name":"N","phone_number":"+0","email":"n@example.invalid",` +
	`"shipping_address":{"country_code":"XX","city":"C"}}}},
{"update_id":17,"purchased_paid_media":{"from":` + evUserB + `,"paid_media_payload":"pm"}},
{"update_id":18,"poll":{"id":"P1","question":"Q?","options":[{"persistent_id":"o1","text":"a","voter_count":2},` +
	`{"persistent_id":"o2","text":"b","voter_count":0}],"total_voter_count":2,"is_closed":false,"is_anonymous":true,` +
	`"type":"regular","allows_multiple_answers":false,"allows_revoting":true,"members_only":false}},
{"update_id":19,"poll_answer":{"poll_id":"P1","voter_chat":{"id":-556,"type":"channel"},"user":` + evUserB + `,` +
	`"option_ids":[0,1],"option_persistent_ids":["o1","o2"]}},
{"update_id":20,"poll_answer":{"poll_id":"P1","user":` + evUserB + `,"option_ids":[],"option_persistent_ids":[]}},
{"update_id":21,"managed_bot":{"user":` + evUserB + `,"bot":{"id":9,"is_bot":true,"first_name":"Mb"}}},
{"update_id":22,"subscription":{"user":` + evUserB + `,"invoice_payload":"sub","state":"active"}}
]`

func expectJSON(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return v
}

func TestEventUpdatesPrintEveryDocumentedMember(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, eventUpdates, "0"), "-100", "bot", "business/bc1")
	got, err := client.ListUpdates(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].body != `{"limit":100,"timeout":0}` {
		t.Fatalf("calls = %+v", calls)
	}
	if got["skipped"] != 0 || got["last_update_id"] != int64(22) {
		t.Fatalf("skipped = %v, last = %v", got["skipped"], got["last_update_id"])
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"CANARY", "t.me/+"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	ref := func(kind refKind, binding, id string) string { return signRef(testToken, kind, binding, id) }
	userA := `{"id":7,"is_bot":false,"first_name":"Al"}`
	userB := `{"id":8,"is_bot":false,"first_name":"Bo"}`
	want := map[int]string{
		1: `{"update_id":1,"type":"my_chat_member","chat":"-100","date":50,"from":` + userA + `,` +
			`"old_chat_member":{"user":` + userB + `,"status":"member","tag":"t1"},` +
			`"new_chat_member":{"user":` + userB + `,"status":"administrator","custom_title":"Lead","is_anonymous":false,` +
			`"can_be_edited":true,"can_manage_chat":true,"can_post_messages":false},` +
			`"invite_link":{"creator":` + userA + `,"creates_join_request":false,"is_primary":false,"is_revoked":false,` +
			`"name":"promo","expire_date":99,"member_limit":5,"pending_join_request_count":2,` +
			`"subscription_period":2592000,"subscription_price":10},"via_join_request":true,"via_chat_folder_invite_link":true}`,
		2: `{"update_id":2,"type":"chat_member","chat":"-100","date":51,"from":` + userA + `,` +
			`"old_chat_member":{"user":` + userB + `,"status":"restricted","until_date":7,"is_member":true,"can_send_messages":false},` +
			`"new_chat_member":{"user":` + userB + `,"status":"kicked","until_date":8}}`,
		3: `{"update_id":3,"type":"message_reaction","chat":"-100","message_id":4,"date":60,"from":` + userB + `,"user":` + userB + `,` +
			`"actor_chat":{"id":-555,"type":"channel","title":"Other"},"old_reaction":[{"type":"emoji","emoji":"x"}],` +
			`"new_reaction":[{"type":"custom_emoji","custom_emoji_id":"E1"},{"type":"paid"}]}`,
		4: `{"update_id":4,"type":"message_reaction_count","chat":"-100","message_id":4,"date":61,` +
			`"reactions":[{"type":{"type":"emoji","emoji":"x"},"total_count":3},{"type":{"type":"paid"},"total_count":1}]}`,
		5: `{"update_id":5,"type":"chat_boost","chat":"-100","boost":{"boost_id":"B1","add_date":10,"expiration_date":20,` +
			`"source":{"source":"giveaway","user":` + userB + `,"giveaway_message_id":5,"prize_star_count":50,"is_unclaimed":true}}}`,
		6: `{"update_id":6,"type":"removed_chat_boost","chat":"-100","boost_id":"B1","remove_date":30,` +
			`"source":{"source":"premium","user":` + userB + `}}`,
		7: `{"update_id":7,"type":"chat_join_request","chat":"-100","date":70,"from":` + userB + `,` +
			`"join_request_ref":"` + ref(refJoinRequestQuery, "-100", "JQ") + `","user_chat_id":8,"bio":"hi",` +
			`"invite_link":{"creator":` + userA + `,"creates_join_request":true,"is_primary":false,"is_revoked":true,"name":"j"}}`,
		8: `{"update_id":8,"type":"callback_query","chat":"-100","message_id":9,"date":80,"from":` + userB + `,` +
			`"callback_data":"d","callback_ref":"` + ref(refCallback, "-100", "CB1") + `","chat_instance":"ci","game_short_name":"g",` +
			`"message":{"message_id":9,"date":80,"from":` + userA + `,"text":"press","chat_info":{"id":-100,"type":"supergroup","title":"Team"}}}`,
		9: `{"update_id":9,"type":"callback_query","chat":"bot","from":` + userB + `,"callback_data":"d",` +
			`"callback_ref":"` + ref(refCallback, "bot", "CB2") + `","chat_instance":"ci",` +
			`"inline_message_ref":"` + ref(refInlineMessage, "bot", "CANARYINLINE") + `"}`,
		10: `{"update_id":10,"type":"inline_query","chat":"bot","from":` + userB + `,"query":"q","inline_ref":"` +
			ref(refInlineQuery, "bot", "IQ") + `","offset":"o","chat_type":"sender","location":{"latitude":1.5,"longitude":2.5}}`,
		11: `{"update_id":11,"type":"chosen_inline_result","chat":"bot","from":` + userB + `,"query":"q","result_id":"r",` +
			`"inline_message_ref":"` + ref(refInlineMessage, "bot", "CANARYINLINE") + `","location":{"latitude":1.5,"longitude":2.5}}`,
		12: `{"update_id":12,"type":"business_connection","chat":"business/bc1","date":90,"from":` + userB + `,"is_enabled":true,` +
			`"business_chat_ref":"` + client.businessChatRef("bc1", 8) + `","rights":{"can_reply":true,"can_read_messages":false,` +
			`"can_delete_sent_messages":false,"can_delete_all_messages":false,"can_edit_name":false,"can_edit_bio":false,` +
			`"can_edit_profile_photo":false,"can_edit_username":false,"can_change_gift_settings":false,` +
			`"can_view_gifts_and_stars":false,"can_convert_gifts_to_stars":false,"can_transfer_and_upgrade_gifts":false,` +
			`"can_transfer_stars":false,"can_manage_stories":false}}`,
		13: `{"update_id":13,"type":"guest_message","chat":"-100","message_id":2,"date":100,"text":"hey",` +
			`"guest_query_ref":"` + ref(refGuestQuery, "-100", "GQ") + `","chat_info":{"id":-100,"type":"supergroup","title":"Team"}}`,
		14: `{"update_id":14,"type":"stopped_message_generation","chat":"-100","message_thread_id":3,"draft_id":42}`,
		15: `{"update_id":15,"type":"shipping_query","chat":"bot","from":` + userB + `,"invoice_payload":"pl",` +
			`"shipping_query_ref":"` + ref(refShippingQuery, "bot", "CANARYSHIP") + `",` +
			`"shipping_address":{"country_code":"XX","state":"S","city":"C","street_line1":"L1","street_line2":"L2","post_code":"P"}}`,
		16: `{"update_id":16,"type":"pre_checkout_query","chat":"bot","from":` + userB + `,"currency":"EUR","total_amount":1250,` +
			`"invoice_payload":"pl","shipping_option_id":"so","pre_checkout_query_ref":"` + ref(refPreCheckoutQuery, "bot", "CANARYPAY") + `",` +
			`"order_info":{"name":"N","phone_number":"+0","email":"n@example.invalid","shipping_address":{"country_code":"XX","city":"C"}}}`,
		17: `{"update_id":17,"type":"purchased_paid_media","chat":"bot","from":` + userB + `,"paid_media_payload":"pm"}`,
		18: `{"update_id":18,"type":"poll","chat":"bot","poll":{"id":"P1","question":"Q?","options":[` +
			`{"persistent_id":"o1","text":"a","voter_count":2},{"persistent_id":"o2","text":"b","voter_count":0}],` +
			`"total_voter_count":2,"is_closed":false,"is_anonymous":true,"type":"regular","allows_multiple_answers":false,` +
			`"allows_revoting":true,"members_only":false}}`,
		19: `{"update_id":19,"type":"poll_answer","chat":"bot","user":` + userB + `,"voter_chat":{"id":-556,"type":"channel"},` +
			`"poll_id":"P1","option_ids":[0,1],"option_persistent_ids":["o1","o2"]}`,
		20: `{"update_id":20,"type":"poll_answer","chat":"bot","user":` + userB + `,"poll_id":"P1","option_ids":[]}`,
		21: `{"update_id":21,"type":"managed_bot","chat":"bot","user":` + userB + `,"bot":{"id":9,"is_bot":true,"first_name":"Mb"}}`,
		22: `{"update_id":22,"type":"subscription","chat":"bot","user":` + userB + `,"invoice_payload":"sub","state":"active"}`,
	}
	updates := jsonRoundTrip(t, got["updates"]).([]any)
	if len(updates) != len(want) {
		t.Fatalf("updates = %d, want %d", len(updates), len(want))
	}
	for _, u := range updates {
		id := int(u.(map[string]any)["update_id"].(float64))
		if !reflect.DeepEqual(u, expectJSON(t, want[id])) {
			t.Errorf("update %d =\n%s\nwant\n%s", id, mustMarshal(u), want[id])
		}
	}
}

func TestChatlessUpdatesNeedTheBotTarget(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, eventUpdates, "0"), "-100", "business/bc1")
	got, err := client.ListUpdates(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Eleven updates have no chat: an inline callback, two inline updates, shipping and pre-checkout queries,
	// purchased media, a poll, two poll answers, a managed bot, and a subscription.
	if got["skipped"] != 11 || got["last_update_id"] != int64(22) {
		t.Errorf("skipped = %v, last = %v", got["skipped"], got["last_update_id"])
	}
	for _, u := range got["updates"].([]updateOut) {
		if u.Chat == botTarget {
			t.Errorf("%s was shown without the bot target", u.Type)
		}
	}
}

func TestGuestMessageAndStoppedGenerationFollowTheBinding(t *testing.T) {
	updates := `[{"update_id":1,"guest_message":{"message_id":2,"date":1,"chat":{"id":-999},"text":"FOREIGN","guest_query_id":"GQ"}},` +
		`{"update_id":2,"stopped_message_generation":{"chat":{"id":-999},"message_thread_id":3,"draft_id":4}},` +
		`{"update_id":3,"stopped_message_generation":{"chat":` + evChat + `,"message_thread_id":3,"draft_id":4}},` +
		`{"update_id":4,"stopped_message_generation":{"chat":` + evChat + `,"message_thread_id":5,"draft_id":6}}]`
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-100/topics/3", "bot")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	list := got["updates"].([]updateOut)
	if len(list) != 1 || list[0].DraftID != 4 || list[0].Chat != "-100/topics/3" || got["skipped"] != 3 {
		t.Errorf("updates = %s, skipped = %v", mustMarshal(list), got["skipped"])
	}
	if strings.Contains(string(mustMarshal(got)), "FOREIGN") {
		t.Error("the text of an unbound chat is shown")
	}
}

func TestPaymentAndInlineMessageReferencesAreBoundToTheBot(t *testing.T) {
	resolved := resolvedWith("-100", "bot")
	chatOnly := resolvedWith("-100")
	client, _ := multiClient(t, testToken, fakeTelegram(new([]recorded), "[]", "0"), "-100", "bot")
	for _, kind := range []refKind{refShippingQuery, refPreCheckoutQuery, refInlineMessage} {
		raw := signRef(testToken, kind, botTarget, "ID9")
		parsed, err := parseRef(resolved, kind, raw)
		if err != nil || parsed.binding != botTarget {
			t.Fatalf("kind %d: %+v, %v", kind, parsed, err)
		}
		if id, err := client.checkRef(parsed); err != nil || id != "ID9" {
			t.Errorf("kind %d: checkRef = %q, %v", kind, id, err)
		}
		if _, err := parseRef(chatOnly, kind, raw); err == nil {
			t.Errorf("kind %d: accepted by a connection without the bot target", kind)
		}
		other := refShippingQuery
		if kind == refShippingQuery {
			other = refPreCheckoutQuery
		}
		if _, err := parseRef(resolved, other, raw); err == nil {
			t.Errorf("kind %d: accepted as another kind", kind)
		}
		if _, err := parseRef(resolved, kind, raw[:len(raw)-2]); err == nil {
			t.Errorf("kind %d: a truncated reference was accepted", kind)
		}
		forged := signRef("999:other-token", kind, botTarget, "ID9")
		parsed, _ = parseRef(resolved, kind, forged)
		if _, err := client.checkRef(parsed); err == nil {
			t.Errorf("kind %d: a reference of another token passed", kind)
		}
	}
}

func TestEventMembersAreInSchemaAndFields(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, eventUpdates, "0"), "-100", "bot", "business/bc1")
	got, err := client.ListUpdates(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(updatesList.OutputSchema, &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)["updates"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	fields := map[string]bool{}
	var all strings.Builder
	for _, f := range updatesList.Fields {
		fields[f.Name] = true
		all.WriteString(f.Description + " ")
	}
	for _, u := range jsonRoundTrip(t, got["updates"]).([]any) {
		for key := range u.(map[string]any) {
			if _, ok := properties[key]; !ok {
				t.Errorf("%s is not in the output schema", key)
			}
			// The message header members are named by the text of the updates field.
			if !fields[key] && !strings.Contains(updatesList.Fields[0].Description, key) {
				t.Errorf("%s is not a field", key)
			}
		}
	}
	for key := range properties {
		if !fields[key] && !strings.Contains(all.String(), key) {
			t.Errorf("schema member %s is not named in the fields", key)
		}
	}
}

// A page of button presses, each with a message that has every content member filled and a replied-to message,
// is the largest page an event type adds; it must fit the response bound like a page of full messages.
func TestFullPageOfCallbackQueriesFitsTheResponseBound(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	var list []string
	for i := 1; i <= maxUpdateLimit; i++ {
		list = append(list, `{"update_id":`+strconv.Itoa(i)+`,"callback_query":{"id":"`+long(64)+`","from":`+evUserA+
			`,"chat_instance":"`+long(32)+`","data":"`+long(64)+`","game_short_name":"`+long(64)+`","message":{`+
			fullMessageJSON(`,"reply_to_message":{"message_id":3,"date":90,"chat":`+headTeam+`,"text":"`+long(4096)+`"}`)+`}}}`)
	}
	updates := "[" + strings.Join(list, ",") + "]"
	t.Logf("100 callback queries with full messages: %d bytes of %d", len(updates), updatesResponseBytes)
	if len(updates) >= updatesResponseBytes {
		t.Fatalf("a page needs %d bytes, the bound is %d", len(updates), updatesResponseBytes)
	}
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), maxUpdateLimit, 0)
	if err != nil {
		t.Fatalf("a full page does not fit: %v", err)
	}
	list0 := got["updates"].([]updateOut)
	if len(list0) != maxUpdateLimit || list0[0].Message == nil || list0[0].Message.ReplyToMessage == nil {
		t.Errorf("updates = %d, first message = %+v", len(list0), list0[0].Message)
	}
}

func TestEventTextsAreCleanedAndBounded(t *testing.T) {
	updates := `[{"update_id":1,"chat_join_request":{"chat":` + evChat + `,"from":` + evUserB + `,"bio":"a\u0007b` + strings.Repeat("y", 400) + `",` +
		`"invite_link":{"invite_link":"x","name":"n\u001bm"}}},` +
		`{"update_id":2,"pre_checkout_query":{"id":"I","from":` + evUserB + `,"currency":"EUR","total_amount":0,` +
		`"invoice_payload":"p\u0000q","order_info":{"shipping_address":{"street_line1":"s\u0007t"}}}}]`
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-100", "bot")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	list := got["updates"].([]updateOut)
	if len(list) != 2 || !strings.HasPrefix(list[0].Bio, "ab") || len([]rune(list[0].Bio)) != maxNameRunes ||
		list[0].InviteLink.Name != "nm" {
		t.Errorf("join request = %s", mustMarshal(list[0]))
	}
	if list[1].InvoicePayload != "pq" || list[1].OrderInfo.ShippingAddress.StreetLine1 != "st" ||
		list[1].TotalAmount == nil || *list[1].TotalAmount != 0 {
		t.Errorf("pre-checkout query = %s", mustMarshal(list[1]))
	}
}
