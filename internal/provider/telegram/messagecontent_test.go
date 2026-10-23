package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	// contentJSON carries every content member of a Message with exact values; file ids are FILE-prefixed.
	contentJSON = `"entities":[{"type":"text_link","offset":0,"length":2,"url":"https://example.org/a?b=1"},` +
		`{"type":"text_mention","offset":3,"length":2,"user":{"id":21,"is_bot":false,"first_name":"Men"}},` +
		`{"type":"pre","offset":6,"length":1,"language":"go"},{"type":"custom_emoji","offset":7,"length":1,"custom_emoji_id":"CE1"},` +
		`{"type":"date_time","offset":8,"length":1,"unix_time":0,"date_time_format":"Dt"}],` +
		`"link_preview_options":{"is_disabled":true,"url":"https://example.org/p","prefer_small_media":true,` +
		`"prefer_large_media":true,"show_above_text":true},` +
		`"suggested_post_info":{"state":"pending","price":{"currency":"XTR","amount":250},"send_date":1900000000},` +
		`"effect_id":"EFF","rich_message":{"blocks":[{"type":"paragraph","text":"rich"},` +
		`{"type":"photo","photo":[{"file_id":"FILE-RICH","file_unique_id":"U1","width":5,"height":6}],` +
		`"caption":{"text":"c"}},{"type":"mathematical_expression","expression":"x=1.50"}],"is_rtl":true},` +
		`"paid_media":{"star_count":7,"paid_media":[{"type":"preview","width":10,"height":11,"duration":12},` +
		`{"type":"photo","photo":[{"file_id":"FILE-PAID","file_unique_id":"U2","width":20,"height":21,"file_size":22}]},` +
		`{"type":"video","video":{"file_id":"FILE-PAIDVIDEO","file_unique_id":"U3","width":30,"height":31,"duration":32}}]},` +
		`"story":{"chat":{"id":-1014,"type":"channel","title":"Stories"},"id":15},` +
		`"caption_entities":[{"type":"bold","offset":0,"length":3}],"show_caption_above_media":true,"has_media_spoiler":true,` +
		`"checklist":{"title":"List","title_entities":[{"type":"bold","offset":0,"length":1}],"tasks":[` +
		`{"id":1,"text":"one","text_entities":[{"type":"italic","offset":0,"length":1}],"completed_by_user":` + headUserA +
		`,"completion_date":99},{"id":2,"text":"two","completed_by_chat":{"id":-1015,"type":"group","title":"Done"}}],` +
		`"others_can_add_tasks":true,"others_can_mark_tasks_as_done":true},` +
		`"contact":{"phone_number":"+491234","first_name":"Co","last_name":"Ntact","user_id":31,"vcard":"BEGIN:VCARD\nEND:VCARD"},` +
		`"dice":{"emoji":"🎲","value":4},` +
		`"game":{"title":"G","description":"Gd","photo":[{"file_id":"FILE-GAME","file_unique_id":"U4","width":1,"height":2}],` +
		`"text":"gt","text_entities":[{"type":"bold","offset":0,"length":1}],` +
		`"animation":{"file_id":"FILE-GANIM","file_unique_id":"U5","width":3,"height":4,"duration":5,"mime_type":"video/mp4"}},` +
		`"poll":{"id":"P1","question":"Q?","question_entities":[{"type":"custom_emoji","offset":0,"length":1,"custom_emoji_id":"QE"}],` +
		`"options":[{"persistent_id":"o0","text":"A","voter_count":0},` +
		`{"persistent_id":"o1","text":"B","voter_count":3,"added_by_user":` + headUserA + `,"addition_date":77,` +
		`"media":{"link":{"url":"https://example.org/o"},"photo":[{"file_id":"FILE-OPT","file_unique_id":"U6","width":7,"height":8}]}}],` +
		`"total_voter_count":3,"is_closed":false,"is_anonymous":true,"type":"quiz","allows_multiple_answers":false,` +
		`"allows_revoting":true,"members_only":false,"country_codes":["DE","AT"],"correct_option_ids":[0],` +
		`"explanation":"why\nbecause","explanation_entities":[{"type":"bold","offset":0,"length":3}],` +
		`"open_period":60,"close_date":1900000001,"description":"desc","media":{"location":{"latitude":1.5,"longitude":-2.25}}},` +
		`"venue":{"location":{"latitude":52.5200066,"longitude":13.404954},"title":"Spot","address":"Street 1",` +
		`"foursquare_id":"fs","foursquare_type":"ft","google_place_id":"gp","google_place_type":"gt"},` +
		`"location":{"latitude":-0.0,"longitude":0,"horizontal_accuracy":12.50,"live_period":300,"heading":90,"proximity_alert_radius":25},` +
		`"invoice":{"title":"Inv","description":"Ides","start_parameter":"sp","currency":"EUR","total_amount":1999},` +
		`"reply_markup":{"inline_keyboard":[[{"text":"Url","url":"https://example.org/u","style":"primary"},` +
		`{"text":"Cb","callback_data":"cb:1"}],[{"text":"Web","web_app":{"url":"https://example.org/w"}},` +
		`{"text":"Login","login_url":{"url":"https://example.org/l","forward_text":"F","bot_username":"lb","request_write_access":true}}],` +
		`[{"text":"Sw","switch_inline_query":"q"},{"text":"Sc","switch_inline_query_current_chat":""},` +
		`{"text":"Sx","switch_inline_query_chosen_chat":{"query":"x","allow_user_chats":true,"allow_channel_chats":true}},` +
		`{"text":"Cp","copy_text":{"text":"copied"}},{"text":"Gm","callback_game":{}},{"text":"Pay","pay":true},` +
		`{"text":"Off","disabled":{}}]]},` +
		`"giveaway":{"chats":[{"id":-1016,"type":"channel","title":"Join"}],"winners_selection_date":5,"winner_count":2,` +
		`"has_public_winners":true,"prize_description":"pd","country_codes":["DE"],"prize_star_count":9},` +
		`"giveaway_winners":{"chat":{"id":-1017,"type":"channel","title":"Won"},"giveaway_message_id":6,` +
		`"winners_selection_date":7,"winner_count":1,"winners":[` + headUserA + `],"was_refunded":true}`

	expectedContent = `"entities":[{"type":"text_link","offset":0,"length":2,"url":"https://example.org/a?b=1"},` +
		`{"type":"text_mention","offset":3,"length":2,"user":{"id":21,"is_bot":false,"first_name":"Men"}},` +
		`{"type":"pre","offset":6,"length":1,"language":"go"},{"type":"custom_emoji","offset":7,"length":1,"custom_emoji_id":"CE1"},` +
		`{"type":"date_time","offset":8,"length":1,"unix_time":0,"date_time_format":"Dt"}],` +
		`"link_preview_options":{"is_disabled":true,"url":"https://example.org/p","prefer_small_media":true,` +
		`"prefer_large_media":true,"show_above_text":true},` +
		`"suggested_post_info":{"state":"pending","price":{"currency":"XTR","amount":250},"send_date":1900000000},` +
		`"effect_id":"EFF","rich_message":{"blocks":[{"type":"paragraph","text":"rich"},` +
		`{"type":"photo","photo":[{"file_ref":"REF:FILE-RICH","file_unique_id":"U1","width":5,"height":6}],` +
		`"caption":{"text":"c"}},{"type":"mathematical_expression","expression":"x=1.50"}],"is_rtl":true},` +
		`"paid_media":{"star_count":7,"paid_media":[{"type":"preview","width":10,"height":11,"duration":12},` +
		`{"type":"photo","photo":[{"file_ref":"REF:FILE-PAID","file_unique_id":"U2","width":20,"height":21,"file_size":22}]},` +
		`{"type":"video","video":{"kind":"video","file_ref":"REF:FILE-PAIDVIDEO","file_unique_id":"U3","width":30,"height":31,"duration":32}}]},` +
		`"story":{"chat":{"id":-1014,"type":"channel","title":"Stories"},"id":15},` +
		`"caption_entities":[{"type":"bold","offset":0,"length":3}],"show_caption_above_media":true,"has_media_spoiler":true,` +
		`"checklist":{"title":"List","title_entities":[{"type":"bold","offset":0,"length":1}],"tasks":[` +
		`{"id":1,"text":"one","text_entities":[{"type":"italic","offset":0,"length":1}],"completed_by_user":` + headUserA +
		`,"completion_date":99},{"id":2,"text":"two","completed_by_chat":{"id":-1015,"type":"group","title":"Done"}}],` +
		`"others_can_add_tasks":true,"others_can_mark_tasks_as_done":true},` +
		`"contact":{"phone_number":"+491234","first_name":"Co","last_name":"Ntact","user_id":31,"vcard":"BEGIN:VCARD\nEND:VCARD"},` +
		`"dice":{"emoji":"🎲","value":4},` +
		`"game":{"title":"G","description":"Gd","photo":[{"file_ref":"REF:FILE-GAME","file_unique_id":"U4","width":1,"height":2}],` +
		`"text":"gt","text_entities":[{"type":"bold","offset":0,"length":1}],` +
		`"animation":{"kind":"animation","file_ref":"REF:FILE-GANIM","file_unique_id":"U5","width":3,"height":4,"duration":5,"mime_type":"video/mp4"}},` +
		`"poll":{"id":"P1","question":"Q?","question_entities":[{"type":"custom_emoji","offset":0,"length":1,"custom_emoji_id":"QE"}],` +
		`"options":[{"persistent_id":"o0","text":"A","voter_count":0},` +
		`{"persistent_id":"o1","text":"B","voter_count":3,"added_by_user":` + headUserA + `,"addition_date":77,` +
		`"media":{"link":{"url":"https://example.org/o"},"photo":[{"file_ref":"REF:FILE-OPT","file_unique_id":"U6","width":7,"height":8}]}}],` +
		`"total_voter_count":3,"is_closed":false,"is_anonymous":true,"type":"quiz","allows_multiple_answers":false,` +
		`"allows_revoting":true,"members_only":false,"country_codes":["DE","AT"],"correct_option_ids":[0],` +
		`"explanation":"why\nbecause","explanation_entities":[{"type":"bold","offset":0,"length":3}],` +
		`"open_period":60,"close_date":1900000001,"description":"desc","media":{"location":{"latitude":1.5,"longitude":-2.25}}},` +
		`"venue":{"location":{"latitude":52.5200066,"longitude":13.404954},"title":"Spot","address":"Street 1",` +
		`"foursquare_id":"fs","foursquare_type":"ft","google_place_id":"gp","google_place_type":"gt"},` +
		`"location":{"latitude":-0.0,"longitude":0,"horizontal_accuracy":12.50,"live_period":300,"heading":90,"proximity_alert_radius":25},` +
		`"invoice":{"title":"Inv","description":"Ides","start_parameter":"sp","currency":"EUR","total_amount":1999},` +
		`"reply_markup":{"inline_keyboard":[[{"text":"Url","url":"https://example.org/u","style":"primary"},` +
		`{"text":"Cb","callback_data":"cb:1"}],[{"text":"Web","web_app":{"url":"https://example.org/w"}},` +
		`{"text":"Login","login_url":{"url":"https://example.org/l","forward_text":"F","bot_username":"lb","request_write_access":true}}],` +
		`[{"text":"Sw","switch_inline_query":"q"},{"text":"Sc","switch_inline_query_current_chat":""},` +
		`{"text":"Sx","switch_inline_query_chosen_chat":{"query":"x","allow_user_chats":true,"allow_channel_chats":true}},` +
		`{"text":"Cp","copy_text":{"text":"copied"}},{"text":"Gm","callback_game":{}},{"text":"Pay","pay":true},` +
		`{"text":"Off","disabled":{}}]]},` +
		`"giveaway":{"chats":[{"id":-1016,"type":"channel","title":"Join"}],"winners_selection_date":5,"winner_count":2,` +
		`"has_public_winners":true,"prize_description":"pd","country_codes":["DE"],"prize_star_count":9},` +
		`"giveaway_winners":{"chat":{"id":-1017,"type":"channel","title":"Won"},"giveaway_message_id":6,` +
		`"winners_selection_date":7,"winner_count":1,"winners":[` + headUserA + `],"was_refunded":true}`
)

// refsToIDs replaces every file_ref of v with its file_id after redeeming it for binding, as REF:<id>, and
// renames the member to file_ref again so a printed tree can be compared with the expected text.
func refsToIDs(t *testing.T, client *Client, binding string, v any) any {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && k == "file_ref" {
				parsed, err := parseRef(resolvedWith(binding), refFile, s)
				if err != nil || parsed.binding != binding {
					t.Fatalf("file_ref %q = %v %+v", s, err, parsed)
				}
				id, err := client.checkRef(parsed)
				if err != nil {
					t.Fatal(err)
				}
				x[k] = "REF:" + id
				continue
			}
			x[k] = refsToIDs(t, client, binding, child)
		}
	case []any:
		for i := range x {
			x[i] = refsToIDs(t, client, binding, x[i])
		}
	}
	return v
}

func contentMessage(extra string) string {
	return `"message_id":9,"date":100,"chat":` + headTeam + `,` + extra
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUpdatesListPrintsEveryMessageContentMember(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{` + contentMessage(contentJSON+`,"reply_to_message":{"message_id":3,"date":90,"chat":`+
		headTeam+`,"contact":{"phone_number":"1","first_name":"R"},"poll":{"id":"RP","question":"RQ","options":[{"persistent_id":"x","text":"y"}]}}`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, exact := range []string{`"horizontal_accuracy":12.50`, `"latitude":-0.0,`, `"latitude":52.5200066`, `"longitude":13.404954`} {
		if !strings.Contains(encoded, exact) {
			t.Errorf("output lacks %s: %s", exact, encoded)
		}
	}
	for _, leaked := range []string{"FILE-"} {
		if strings.Contains(encoded, leaked) {
			t.Errorf("output leaks %s", leaked)
		}
	}
	out := jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any)
	if omitted := out["omitted_fields"]; omitted != nil {
		t.Errorf("omitted_fields = %v", omitted)
	}
	reply := out["reply_to_message"].(map[string]any)
	if reply["contact"] == nil || reply["poll"] == nil {
		t.Errorf("the replied-to message lacks its content: %v", reply)
	}
	delete(out, "reply_to_message")
	delete(out, "reply_to_message_id")
	delete(out, "chat_info")
	refsToIDs(t, client, "-1001", out)
	want := decodeObject(t, `{"update_id":1,"type":"message","chat":"-1001","message_id":9,"date":100,`+expectedContent+`}`)
	if !reflect.DeepEqual(out, want) {
		t.Errorf("update = %s\nwant   %s", mustMarshal(out), mustMarshal(want))
	}
}

func TestBusinessMessageContentHasNoFileRef(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"business_message":{"business_connection_id":"b1","message_id":5,"date":100,` +
		`"chat":{"id":777123,"type":"private"},` + contentJSON + `,"giveaway_winners":{"chat":{"id":777123,"type":"private"},"giveaway_message_id":1,"winners_selection_date":1,"winner_count":0,"winners":[]}}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, forbidden := range []string{"file_ref", "FILE-", "777123"} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	out := jsonRoundTrip(t, got["updates"]).([]any)[0].(map[string]any)
	for _, member := range []string{"poll", "paid_media", "game", "reply_markup", "contact", "rich_message"} {
		if out[member] == nil {
			t.Errorf("%s is missing: %s", member, encoded)
		}
	}
	// A rich photo block without a printable reference keeps its other members.
	blocks := out["rich_message"].(map[string]any)["blocks"].([]any)
	if photo := blocks[1].(map[string]any)["photo"].([]any)[0].(map[string]any); photo["width"] != float64(5) {
		t.Errorf("rich photo = %v", photo)
	}
}

func TestMessageEntitiesAreCappedAtOneHundred(t *testing.T) {
	var calls []recorded
	var list []string
	for i := 0; i < 101; i++ {
		list = append(list, `{"type":"bold","offset":`+string(rune('0'+i%10))+`,"length":1}`)
	}
	joined := strings.Join(list, ",")
	updates := `[{"update_id":1,"message":{` + contentMessage(`"entities":[`+joined+`],"caption_entities":[`+joined+`]`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	if len(u.Entities) != 100 || len(u.CaptionEntities) != 100 || len(u.OmittedFields) != 0 {
		t.Errorf("entities = %d, caption_entities = %d, omitted = %v", len(u.Entities), len(u.CaptionEntities), u.OmittedFields)
	}
}

func TestOversizeContentMembersAreOmittedAndNamed(t *testing.T) {
	var calls []recorded
	var options []string
	for i := 0; i < 101; i++ {
		options = append(options, `{"persistent_id":"o","text":"t"}`)
	}
	deep := `{"type":"paragraph","text":"x"}`
	for i := 0; i < 10; i++ {
		deep = `{"type":"details","blocks":[` + deep + `]}`
	}
	updates := `[{"update_id":1,"message":{` + contentMessage(`"poll":{"id":"p","question":"q","options":[`+strings.Join(options, ",")+`]},`+
		`"rich_message":{"blocks":[`+deep+`]},`+
		`"reply_markup":{"inline_keyboard":[[{"text":"`+strings.Repeat("\U0001F600", 100)+`","url":"`+strings.Repeat("u", 5000)+`"}]]},`+
		`"dice":{"emoji":"x","value":1}`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	if u.Poll != nil || u.RichMessage != nil || u.Dice == nil || u.ReplyMarkup == nil {
		t.Errorf("update = %s", mustMarshal(u))
	}
	if !reflect.DeepEqual(u.OmittedFields, []string{"rich_message", "poll"}) {
		t.Errorf("omitted_fields = %v", u.OmittedFields)
	}
	// The address is bounded text, not dropped.
	if n := len([]rune(u.ReplyMarkup.InlineKeyboard[0][0].URL)); n != maxURLRunes {
		t.Errorf("url runes = %d", n)
	}
}

func TestMessageContentTextsAreCleaned(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{` + contentMessage(`"poll":{"id":"p","question":"a\u0007b","options":[{"persistent_id":"o","text":"x"}],`+
		`"explanation":"l1\nl2\u001b","description":"d"},"contact":{"phone_number":"1","first_name":"F\u0000","vcard":"v\u0007\nw"},`+
		`"invoice":{"title":"t\u0007","description":"d","start_parameter":"s","currency":"EUR","total_amount":1},`+
		`"entities":[{"type":"text_link","offset":0,"length":1,"url":"https://e.org/\u0007x"}]`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	if u.Poll.Question != "ab" || u.Poll.Explanation != "l1\nl2" || u.Contact.FirstName != "F" || u.Contact.VCard != "v\nw" ||
		u.Invoice.Title != "t" || u.Entities[0].URL != "https://e.org/x" {
		t.Errorf("update = %s", mustMarshal(u))
	}
}

func TestRichMessageDropsBlockedMembers(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{` + contentMessage(`"rich_message":{"blocks":[{"type":"audio","audio":{"file_id":"FILE-A",`+
		`"file_path":"LEAKPATH","invite_link":"LEAKLINK","duration":3,"access_hash":"LEAKHASH"}},`+
		`{"type":"paragraph","text":"p","share_token":"LEAKTOKEN"}],"passport_data":"LEAKPASS","secret":"LEAKSECRET"}`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	for _, leaked := range []string{"LEAK", "FILE-A"} {
		if strings.Contains(encoded, leaked) {
			t.Errorf("output leaks %s: %s", leaked, encoded)
		}
	}
	if !strings.Contains(encoded, `"duration":3`) || !strings.Contains(encoded, `"file_ref"`) {
		t.Errorf("output = %s", encoded)
	}
}

func TestPersonalChatMessagesAndPinnedMessageCarryContent(t *testing.T) {
	transport := func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "getChatMember" {
			return response(200, `{"ok":true,"result":{"status":"member","user":{"id":7}}}`), nil
		}
		return response(200, `{"ok":true,"result":[{"message_id":1,"date":2,"chat":`+headTeam+`,`+contentJSON+`}]}`), nil
	}
	client, _ := telegramClient(t, "-1001", roundTripFunc(transport))
	got, err := client.PersonalChatMessages(context.Background(), 7, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	out := jsonRoundTrip(t, got).(map[string]any)["messages"].([]any)[0].(map[string]any)
	for _, member := range []string{"entities", "poll", "paid_media", "reply_markup", "rich_message", "giveaway"} {
		if out[member] == nil {
			t.Errorf("%s is missing", member)
		}
	}
	pinned, _, _ := chatFor(t, `{"id":-1001,"type":"supergroup","pinned_message":{"message_id":55,"date":5,"chat":`+headTeam+`,`+contentJSON+`}}`)
	message, _ := pinned["pinned_message"].(map[string]any)
	if message["poll"] == nil || message["contact"] == nil || message["location"] == nil {
		t.Errorf("pinned_message = %v", message)
	}
}

// A full page of messages with every content member filled, text and caption at their limits, and a replied-to
// text message must fit the response bound. Telegram sends these members whether or not they are printed, so
// the bound is set by the page Telegram returns, not by the mapping.
func TestFullPageOfFullMessagesFitsTheResponseBound(t *testing.T) {
	entityList := func(n int) string {
		var list []string
		for i := 0; i < n; i++ {
			list = append(list, `{"type":"text_link","offset":`+strings.Repeat("1", 3)+`,"length":4,"url":"https://example.org/`+
				strings.Repeat("p", 40)+`"}`)
		}
		return "[" + strings.Join(list, ",") + "]"
	}
	var options, tasks, buttons []string
	for i := 0; i < 12; i++ {
		options = append(options, `{"persistent_id":"`+strings.Repeat("o", 20)+`","text":"`+strings.Repeat("t", 100)+
			`","voter_count":1000}`)
	}
	for i := 0; i < 30; i++ {
		tasks = append(tasks, `{"id":1,"text":"`+strings.Repeat("k", 100)+`"}`)
	}
	for i := 0; i < 8; i++ {
		buttons = append(buttons, `{"text":"`+strings.Repeat("b", 30)+`","url":"https://example.org/`+strings.Repeat("u", 80)+`"}`)
	}
	photo := `[{"file_id":"` + strings.Repeat("F", 90) + `","file_unique_id":"` + strings.Repeat("U", 16) + `","width":1280,"height":960,"file_size":123456}]`
	var blocks []string
	for i := 0; i < 20; i++ {
		blocks = append(blocks, `{"type":"paragraph","text":"`+strings.Repeat("r", 200)+`"}`)
	}
	full := func(extra string) string {
		return `"message_id":9,"date":100,"chat":` + headTeam + `,"from":` + headUserA + `,"text":"` + strings.Repeat("t", 4096) +
			`","caption":"` + strings.Repeat("c", 1024) + `","photo":` + photo + `,` + headJSON[:strings.Index(headJSON, `"reply_to_message"`)] +
			`"entities":` + entityList(100) + `,"caption_entities":` + entityList(10) +
			`,"link_preview_options":{"url":"https://example.org/` + strings.Repeat("l", 100) + `"},"effect_id":"` + strings.Repeat("e", 20) + `",` +
			`"suggested_post_info":{"state":"pending","price":{"currency":"XTR","amount":1},"send_date":1},` +
			`"rich_message":{"blocks":[` + strings.Join(blocks, ",") + `]},` +
			`"paid_media":{"star_count":1,"paid_media":[{"type":"photo","photo":` + photo + `},{"type":"photo","photo":` + photo + `}]},` +
			`"story":{"chat":{"id":-1,"type":"channel","title":"` + strings.Repeat("s", 100) + `"},"id":1},` +
			`"checklist":{"title":"` + strings.Repeat("c", 100) + `","tasks":[` + strings.Join(tasks, ",") + `]},` +
			`"contact":{"phone_number":"+49170","first_name":"` + strings.Repeat("f", 64) + `","vcard":"` + strings.Repeat("v", 500) + `"},` +
			`"dice":{"emoji":"x","value":1},"game":{"title":"g","description":"` + strings.Repeat("d", 255) + `","photo":` + photo + `},` +
			`"poll":{"id":"p","question":"` + strings.Repeat("q", 300) + `","options":[` + strings.Join(options, ",") + `],` +
			`"explanation":"` + strings.Repeat("x", 200) + `","description":"` + strings.Repeat("d", 1000) + `"},` +
			`"venue":{"location":{"latitude":1.5,"longitude":2.5},"title":"` + strings.Repeat("v", 100) + `","address":"` + strings.Repeat("a", 200) + `"},` +
			`"location":{"latitude":1.5,"longitude":2.5,"horizontal_accuracy":12.5},` +
			`"invoice":{"title":"i","description":"` + strings.Repeat("i", 255) + `","start_parameter":"s","currency":"EUR","total_amount":1},` +
			`"reply_markup":{"inline_keyboard":[[` + strings.Join(buttons, ",") + `]]}` + extra
	}
	var list []string
	for i := 1; i <= maxUpdateLimit; i++ {
		list = append(list, `{"update_id":`+strconv.Itoa(i)+`,"message":{`+
			full(`,"reply_to_message":{"message_id":3,"date":90,"chat":`+headTeam+`,"text":"`+strings.Repeat("t", 4096)+`"}`)+`}}`)
	}
	updates := "[" + strings.Join(list, ",") + "]"
	t.Logf("100 full messages: %d bytes of %d", len(updates), updatesResponseBytes)
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

func TestNestedMessageTextAndCaptionKeepLineBreaksLikeTheTopLevel(t *testing.T) {
	var calls []recorded
	long := strings.Repeat("x", maxCaptionRunes+5)
	updates := `[{"update_id":1,"message":{` + contentMessage(`"text":"a\nb\u0007c","caption":"`+long+`","reply_to_message":`+
		`{"message_id":3,"chat":`+headTeam+`,"text":"a\nb\u0007c","caption":"`+long+`"}`) + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-1001")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := got["updates"].([]updateOut)[0]
	r := u.ReplyToMessage
	if r.Text != u.Text || r.Caption != u.Caption || !strings.Contains(r.Text, "a\nb") || len([]rune(r.Caption)) != maxCaptionRunes {
		t.Errorf("reply = %q %d, top level = %q %d", r.Text, len([]rune(r.Caption)), u.Text, len([]rune(u.Caption)))
	}
	pinned, _, _ := chatFor(t, `{"id":-1001,"type":"supergroup","pinned_message":{"message_id":55,"chat":`+headTeam+`,"text":"a\nb"}}`)
	if text := pinned["pinned_message"].(map[string]any)["text"]; text != "a\nb" {
		t.Errorf("pinned text = %q", text)
	}
}

func TestUpdatesListDescriptorNamesTheResponseBound(t *testing.T) {
	want := strconv.Itoa(updatesResponseBytes>>20) + " MiB"
	for _, a := range updatesList.Arguments {
		if a.Name == "limit" && strings.Contains(a.Description, want) {
			return
		}
	}
	t.Errorf("no limit argument names %s", want)
}
