package telegram

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
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// fullChatInfo carries every member of ChatFullInfo; invite_link and pinned_message are canaries.
const fullChatInfo = `{"id":-1001,"type":"supergroup","title":"Team","username":"team","first_name":"Ann",` +
	`"last_name":"Lee","is_forum":true,"is_direct_messages":true,"accent_color_id":0,"max_reaction_count":11,` +
	`"photo":{"small_file_id":"SMALLID","small_file_unique_id":"smallu","big_file_id":"BIGID","big_file_unique_id":"bigu"},` +
	`"active_usernames":["team","team2"],"birthdate":{"day":3,"month":4,"year":1990},` +
	`"business_intro":{"title":"Hi","message":"Welcome","sticker":{"file_id":"STICKERID","file_unique_id":"stu",` +
	`"type":"regular","width":512,"height":512,"is_animated":false,"is_video":false,"emoji":"x"}},` +
	`"business_location":{"address":"Main St 1","location":{"latitude":0,"longitude":-12.5,"horizontal_accuracy":3.5}},` +
	`"business_opening_hours":{"time_zone_name":"Europe/Berlin","opening_hours":[{"opening_minute":0,"closing_minute":480}]},` +
	`"personal_chat":{"id":-1005,"type":"channel","title":"Mine","username":"mine"},` +
	`"parent_chat":{"id":-1006,"type":"channel","title":"Parent","is_direct_messages":false},` +
	`"available_reactions":[{"type":"emoji","emoji":"👍"},{"type":"custom_emoji","custom_emoji_id":"ce1"},{"type":"paid"}],` +
	`"background_custom_emoji_id":"bg1","profile_accent_color_id":0,"profile_background_custom_emoji_id":"pb1",` +
	`"emoji_status_custom_emoji_id":"es1","emoji_status_expiration_date":1700000000,"bio":"About me",` +
	`"has_private_forwards":true,"has_restricted_voice_and_video_messages":true,"join_to_send_messages":true,` +
	`"join_by_request":true,"description":"About","invite_link":"` + inviteCanary + `",` +
	`"pinned_message":{"message_id":55,"text":"PINNEDCANARY"},"permissions":{"can_send_messages":true},` +
	`"accepted_gift_types":{"unlimited_gifts":true,"limited_gifts":false,"unique_gifts":true,` +
	`"premium_subscription":false,"gifts_from_channels":true},"can_send_paid_media":true,"slow_mode_delay":10,` +
	`"unrestrict_boost_count":4,"message_auto_delete_time":86400,"has_aggressive_anti_spam_enabled":true,` +
	`"has_hidden_members":true,"has_protected_content":true,"has_visible_history":true,"sticker_set_name":"set1",` +
	`"can_set_sticker_set":true,"custom_emoji_sticker_set_name":"emoji1","linked_chat_id":-1002,` +
	`"location":{"location":{"latitude":52.52,"longitude":13.405},"address":"Berlin"},` +
	`"rating":{"level":-1,"rating":0,"current_level_rating":-10,"next_level_rating":5},` +
	`"first_profile_audio":{"file_id":"AUDIOID","file_unique_id":"au","duration":9,"title":"Song"},` +
	`"unique_gift_colors":{"model_custom_emoji_id":"m1","symbol_custom_emoji_id":"s1","light_theme_main_color":0,` +
	`"light_theme_other_colors":[1,2],"dark_theme_main_color":16777215,"dark_theme_other_colors":[3]},` +
	`"paid_message_star_count":25,"guard_bot":` + documentedUser + `,"community":{"id":77,"name":"Comm"},"EXTRA":"LEAK"}`

func httpTransportSwap(next http.RoundTripper) (restore func()) {
	previous := http.DefaultTransport
	http.DefaultTransport = next
	return func() { http.DefaultTransport = previous }
}

func chatFor(t *testing.T, result string) (map[string]any, *Client, *config.Resolved) {
	t.Helper()
	var calls []recorded
	client, resolved := multiClient(t, testToken, chatsFake(&calls, 200, `{"ok":true,"result":`+result+`}`), "-1001")
	got, err := client.GetChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getChat" {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "CANARY") || strings.Contains(string(encoded), "LEAK") {
		t.Fatalf("a forbidden value leaked: %s", encoded)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out, client, resolved
}

func TestChatsGetPrintsEveryChatFullInfoMember(t *testing.T) {
	out, client, resolved := chatFor(t, fullChatInfo)
	photo := out["photo"].(map[string]any)
	smallRef, bigRef := photo["small_file_ref"].(string), photo["big_file_ref"].(string)
	stickerRef := out["business_intro"].(map[string]any)["sticker"].(map[string]any)["file_ref"].(string)
	audioRef := out["first_profile_audio"].(map[string]any)["file_ref"].(string)
	for ref, id := range map[string]string{smallRef: "SMALLID", bigRef: "BIGID", stickerRef: "STICKERID", audioRef: "AUDIOID"} {
		parsed, err := parseRef(resolved, refFile, ref)
		if err != nil || parsed.binding != "-1001" {
			t.Fatalf("%s: %v %+v", id, err, parsed)
		}
		if got, err := client.checkRef(parsed); err != nil || got != id {
			t.Errorf("%s: redeems %q, %v", id, got, err)
		}
	}
	want := `{"id":-1001,"type":"supergroup","title":"Team","username":"team","first_name":"Ann","last_name":"Lee",` +
		`"is_forum":true,"is_direct_messages":true,"accent_color_id":0,"max_reaction_count":11,` +
		`"photo":{"small_file_ref":"` + smallRef + `","small_file_unique_id":"smallu","big_file_ref":"` + bigRef +
		`","big_file_unique_id":"bigu"},"active_usernames":["team","team2"],` +
		`"birthdate":{"day":3,"month":4,"year":1990},` +
		`"business_intro":{"title":"Hi","message":"Welcome","sticker":{"kind":"sticker","file_ref":"` + stickerRef +
		`","file_unique_id":"stu","type":"regular","width":512,"height":512,"is_animated":false,"is_video":false,"emoji":"x"}},` +
		`"business_location":{"address":"Main St 1","location":{"latitude":0,"longitude":-12.5,"horizontal_accuracy":3.5}},` +
		`"business_opening_hours":{"time_zone_name":"Europe/Berlin","opening_hours":[{"opening_minute":0,"closing_minute":480}]},` +
		`"personal_chat":{"id":-1005,"type":"channel","title":"Mine","username":"mine"},` +
		`"parent_chat":{"id":-1006,"type":"channel","title":"Parent"},` +
		`"available_reactions":[{"type":"emoji","emoji":"👍"},{"type":"custom_emoji","custom_emoji_id":"ce1"},{"type":"paid"}],` +
		`"background_custom_emoji_id":"bg1","profile_accent_color_id":0,"profile_background_custom_emoji_id":"pb1",` +
		`"emoji_status_custom_emoji_id":"es1","emoji_status_expiration_date":1700000000,"bio":"About me",` +
		`"has_private_forwards":true,"has_restricted_voice_and_video_messages":true,"join_to_send_messages":true,` +
		`"join_by_request":true,"description":"About","permissions":{"can_send_messages":true},` +
		`"accepted_gift_types":{"unlimited_gifts":true,"limited_gifts":false,"unique_gifts":true,` +
		`"premium_subscription":false,"gifts_from_channels":true},"can_send_paid_media":true,"slow_mode_delay":10,` +
		`"unrestrict_boost_count":4,"message_auto_delete_time":86400,"has_aggressive_anti_spam_enabled":true,` +
		`"has_hidden_members":true,"has_protected_content":true,"has_visible_history":true,"sticker_set_name":"set1",` +
		`"can_set_sticker_set":true,"custom_emoji_sticker_set_name":"emoji1","linked_chat_id":-1002,` +
		`"pinned_message_id":55,` +
		`"location":{"location":{"latitude":52.52,"longitude":13.405},"address":"Berlin"},` +
		`"rating":{"level":-1,"rating":0,"current_level_rating":-10,"next_level_rating":5},` +
		`"first_profile_audio":{"kind":"audio","file_ref":"` + audioRef + `","file_unique_id":"au","duration":9,"title":"Song"},` +
		`"unique_gift_colors":{"model_custom_emoji_id":"m1","symbol_custom_emoji_id":"s1","light_theme_main_color":0,` +
		`"light_theme_other_colors":[1,2],"dark_theme_main_color":16777215,"dark_theme_other_colors":[3]},` +
		`"paid_message_star_count":25,"guard_bot":` + documentedUser + `,"community":{"id":77,"name":"Comm"}}`
	var wantMap map[string]any
	if err := json.Unmarshal([]byte(want), &wantMap); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, wantMap) {
		encoded, _ := json.Marshal(out)
		t.Errorf("output = %s\nwant   %s", encoded, want)
	}
}

func TestChatsGetOmitsOversizedNestedObjects(t *testing.T) {
	var reactions []string
	for i := 0; i <= maxNestedList; i++ {
		reactions = append(reactions, `{"type":"custom_emoji","custom_emoji_id":"c`+strconv.Itoa(i)+`"}`)
	}
	var names []string
	for i := 0; i < 150; i++ {
		names = append(names, `"u`+strconv.Itoa(i)+`"`)
	}
	out, _, _ := chatFor(t, `{"id":-1001,"type":"supergroup","available_reactions":[`+strings.Join(reactions, ",")+`],`+
		`"active_usernames":[`+strings.Join(names, ",")+`],"bio":"a\u0007b",`+
		`"business_intro":{"message":"`+strings.Repeat("m", 3000)+`","sticker":{"file_id":"S","thumbnail":{"file_id":"T"}}}}`)
	if _, ok := out["available_reactions"]; ok {
		t.Error("an oversized list was kept")
	}
	if !reflect.DeepEqual(out["omitted_fields"], []any{"available_reactions"}) {
		t.Errorf("omitted_fields = %v", out["omitted_fields"])
	}
	if got := len(out["active_usernames"].([]any)); got != maxNestedList {
		t.Errorf("active_usernames = %d", got)
	}
	if out["bio"] != "ab" {
		t.Errorf("bio = %q", out["bio"])
	}
	if _, ok := out["business_intro"]; !ok {
		t.Error("a bounded object was dropped")
	}
}

func TestChatsGetWithoutNewMembersStaysUnchanged(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200,
		`{"ok":true,"result":{"id":5,"type":"private","photo":{"x":1},"birthdate":null,"accepted_gift_types":null}}`), "5")
	got, err := client.GetChat(context.Background())
	encoded, _ := json.Marshal(got)
	if err != nil || string(encoded) != `{"id":5,"type":"private"}` {
		t.Errorf("output = %s, %v", encoded, err)
	}
}

func TestChatsAdministratorsReturnBots(t *testing.T) {
	for name, tt := range map[string]struct{ args, body string }{
		"absent": {`{}`, `{"chat_id":"-1001"}`},
		"true":   {`{"return_bots":true}`, `{"chat_id":"-1001","return_bots":true}`},
		"false":  {`{"return_bots":false}`, `{"chat_id":"-1001","return_bots":false}`},
	} {
		var calls []recorded
		resolver := secret.NewWith(func(string) string { return testToken }, nil, nil, &redact.Redactor{})
		previous := httpTransportSwap(chatsFake(&calls, 200, `{"ok":true,"result":[]}`))
		_, err := invokeChatsAdministrators(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{},
			json.RawMessage(tt.args))
		previous()
		if err != nil || len(calls) != 1 || calls[0].body != tt.body {
			t.Errorf("%s: err = %v, calls = %+v", name, err, calls)
		}
	}
}

func TestChatsAdministratorsRejectNonBooleanBeforeCredentials(t *testing.T) {
	accessed := false
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, &redact.Redactor{})
	for _, args := range []string{`{"return_bots":"true"}`, `{"return_bots":1}`, `{"return_bots":{}}`} {
		_, err := invokeChatsAdministrators(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{},
			json.RawMessage(args))
		if err == nil || accessed {
			t.Errorf("%s: err = %v, accessed = %v", args, err, accessed)
		}
	}
}

func TestChatsMemberPrintsTag(t *testing.T) {
	for _, status := range []string{"member", "restricted"} {
		var calls []recorded
		client, _ := multiClient(t, testToken, chatsFake(&calls, 200,
			`{"ok":true,"result":{"status":"`+status+`","tag":"VIP\u0007","user":{"id":7,"is_bot":false}}}`), "-1001")
		got, err := client.GetChatMember(context.Background(), 7)
		encoded, _ := json.Marshal(got)
		want := `{"user":{"id":7,"is_bot":false},"status":"` + status + `","tag":"VIP"}`
		if err != nil || string(encoded) != want {
			t.Errorf("%s: %s, %v", status, encoded, err)
		}
	}
}
