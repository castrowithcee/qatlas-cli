package telegram

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// stk is a gift sticker as Telegram sends it; n separates the identifiers of several stickers.
func giftStickerJSON(n int) string {
	return fmt.Sprintf(`{"file_id":"STK%[1]d","file_unique_id":"U%[1]d","type":"regular","width":512,"height":512,`+
		`"is_animated":true,"is_video":false,"emoji":"E","thumbnail":{"file_id":"TH%[1]d","file_unique_id":"T%[1]d",`+
		`"width":128,"height":128}}`, n)
}

// wantStk is the printed sticker; an empty binding prints no file_ref.
func wantGiftSticker(binding string, n int) string {
	ref := func(id string) string {
		if binding == "" {
			return ""
		}
		return `"file_ref":"` + signRef(testToken, refFile, binding, id) + `",`
	}
	return fmt.Sprintf(`{"kind":"sticker",%[2]s"file_unique_id":"U%[1]d","type":"regular","width":512,"height":512,`+
		`"is_animated":true,"is_video":false,"thumbnail":{%[3]s"file_unique_id":"T%[1]d","width":128,"height":128},"emoji":"E"}`,
		n, ref(fmt.Sprintf("STK%d", n)), ref(fmt.Sprintf("TH%d", n)))
}

const (
	giftBackground = `{"center_color":0,"edge_color":16777215,"text_color":5}`
	giftPublisher  = `{"id":-100,"type":"channel","title":"Pub"}`
	giftEntities   = `[{"type":"bold","offset":0,"length":2},{"type":"text_link","offset":0,"length":1,"url":"https://x.test/a"},` +
		`{"type":"text_mention","offset":1,"length":1,"user":{"id":9,"first_name":"Al"}},` +
		`{"type":"pre","offset":0,"length":1,"language":"go"},` +
		`{"type":"custom_emoji","offset":0,"length":1,"custom_emoji_id":"55"},` +
		`{"type":"date_time","offset":0,"length":1,"unix_time":5,"date_time_format":"Dt"}]`
	wantGiftEntities = `[{"type":"bold","offset":0,"length":2},{"type":"text_link","offset":0,"length":1,"url":"https://x.test/a"},` +
		`{"type":"text_mention","offset":1,"length":1,"user":{"id":9,"is_bot":false,"first_name":"Al"}},` +
		`{"type":"pre","offset":0,"length":1,"language":"go"},` +
		`{"type":"custom_emoji","offset":0,"length":1,"custom_emoji_id":"55"},` +
		`{"type":"date_time","offset":0,"length":1,"unix_time":5,"date_time_format":"Dt"}]`
)

func regularOwnedGift() string {
	return `{"type":"regular","owned_gift_id":"OWN1","send_date":10,"is_saved":true,"text":"hi","entities":` + giftEntities + `,` +
		`"is_private":true,"can_be_upgraded":true,"was_refunded":true,"sender_user":{"id":3,"first_name":"Bo"},` +
		`"convert_star_count":9,"prepaid_upgrade_star_count":4,"is_upgrade_separate":true,"unique_gift_number":77,` +
		`"gift":{"id":"G1","sticker":` + giftStickerJSON(1) + `,"star_count":50,"upgrade_star_count":99,"is_premium":true,` +
		`"has_colors":true,"total_count":1000,"remaining_count":0,"personal_total_count":10,"personal_remaining_count":0,` +
		`"background":` + giftBackground + `,"unique_gift_variant_count":12,"publisher_chat":` + giftPublisher + `}}`
}

func wantRegularOwnedGift(binding string) string {
	return `{"type":"regular","gift_id":"G1","send_date":10,"is_saved":true,"text":"hi","sender":{"id":3,"is_bot":false,` +
		`"first_name":"Bo"},"star_count":50,"convert_star_count":9,"owned_gift_id":"OWN1","entities":` + wantGiftEntities + `,` +
		`"is_private":true,"can_be_upgraded":true,"was_refunded":true,"prepaid_upgrade_star_count":4,` +
		`"is_upgrade_separate":true,"unique_gift_number":77,"gift_details":{"id":"G1","star_count":50,"total_count":1000,` +
		`"remaining_count":0,"sticker":` + wantGiftSticker(binding, 1) + `,"upgrade_star_count":99,"is_premium":true,` +
		`"has_colors":true,"personal_total_count":10,"personal_remaining_count":0,"background":` + giftBackground + `,` +
		`"unique_gift_variant_count":12,"publisher_chat":` + giftPublisher + `}}`
}

func uniqueOwnedGift() string {
	return `{"type":"unique","owned_gift_id":"OWN2","send_date":11,"is_saved":true,"can_be_transferred":true,` +
		`"transfer_star_count":25,"next_transfer_date":1234,"gift":{"gift_id":"G2","base_name":"Cat","name":"CatSLUG",` +
		`"number":8,"model":{"name":"M","sticker":` + giftStickerJSON(2) + `,"rarity_per_mille":15,"rarity":"rare"},` +
		`"symbol":{"name":"S","sticker":` + giftStickerJSON(3) + `,"rarity_per_mille":0},"backdrop":{"name":"B","colors":` +
		`{"center_color":1,"edge_color":2,"symbol_color":3,"text_color":0},"rarity_per_mille":20},"is_premium":true,` +
		`"is_burned":true,"is_from_blockchain":true,"colors":{"model_custom_emoji_id":"11","symbol_custom_emoji_id":"22",` +
		`"light_theme_main_color":5,"light_theme_other_colors":[6],"dark_theme_main_color":7,"dark_theme_other_colors":[8,9]},` +
		`"publisher_chat":{"id":-200,"type":"channel","title":"Pub2"}}}`
}

func wantUniqueOwnedGift(binding string) string {
	return `{"type":"unique","gift_id":"G2","name":"Cat","number":8,"send_date":11,"is_saved":true,"owned_gift_id":"OWN2",` +
		`"can_be_transferred":true,"transfer_star_count":25,"next_transfer_date":1234,"gift_details":{"gift_id":"G2",` +
		`"base_name":"Cat","name":"CatSLUG","number":8,"model":{"name":"M","sticker":` + wantGiftSticker(binding, 2) + `,` +
		`"rarity_per_mille":15,"rarity":"rare"},"symbol":{"name":"S","sticker":` + wantGiftSticker(binding, 3) + `,` +
		`"rarity_per_mille":0},"backdrop":{"name":"B","colors":{"center_color":1,"edge_color":2,"symbol_color":3,` +
		`"text_color":0},"rarity_per_mille":20},"is_premium":true,"is_burned":true,"is_from_blockchain":true,` +
		`"colors":{"model_custom_emoji_id":"11","symbol_custom_emoji_id":"22","light_theme_main_color":5,` +
		`"light_theme_other_colors":[6],"dark_theme_main_color":7,"dark_theme_other_colors":[8,9]},` +
		`"publisher_chat":{"id":-200,"type":"channel","title":"Pub2"}}}`
}

func availableGift() string {
	return `{"id":"a","sticker":` + giftStickerJSON(4) + `,"star_count":15,"upgrade_star_count":7,"is_premium":true,"has_colors":true,` +
		`"total_count":100,"remaining_count":3,"personal_total_count":5,"personal_remaining_count":0,` +
		`"background":` + giftBackground + `,"unique_gift_variant_count":9,"publisher_chat":` + giftPublisher + `}`
}

func wantAvailableGift(binding string) string {
	return `{"gift_id":"a","star_count":15,"total_count":100,"remaining_count":3,"sticker":` + wantGiftSticker(binding, 4) + `,` +
		`"upgrade_star_count":7,"is_premium":true,"has_colors":true,"personal_total_count":5,"personal_remaining_count":0,` +
		`"background":` + giftBackground + `,"unique_gift_variant_count":9,"publisher_chat":` + giftPublisher + `}`
}

// giftTool is one of the three owned-gift tools with the binding its sticker references carry.
type giftTool struct {
	name    string
	invoke  payInvoke
	targets []string
	args    string
	method  string
	binding string
}

func ownedGiftTools() []giftTool {
	return []giftTool{
		{"business", invokeBusinessGiftsWith, []string{"business/b1"}, `{"business":"b1"}`, "getBusinessAccountGifts", ""},
		{"chat", invokeGiftsChatWith, []string{"-1001"}, `{}`, "getChatGifts", "-1001"},
		{"user", invokeGiftsUserWith, []string{"-1001"}, `{"user_id":7}`, "getUserGifts", "-1001"},
	}
}

func giftJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is no JSON: %v: %s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is no JSON: %v: %s", err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestOwnedGiftsReportEveryDocumentedMember(t *testing.T) {
	for _, tool := range ownedGiftTools() {
		t.Run(tool.name, func(t *testing.T) {
			e := newPayEnv(t, tool.targets...)
			e.answers[tool.method] = `{"ok":true,"result":{"total_count":2,"next_offset":"n","gifts":[` +
				regularOwnedGift() + `,` + uniqueOwnedGift() + `]}}`
			e.answers["getChatMember"] = memberOK
			got, err := e.run(tool.invoke, tool.args)
			if err != nil {
				t.Fatal(err)
			}
			giftJSONEqual(t, got, `{"total_count":2,"next_offset":"n","gifts":[`+wantRegularOwnedGift(tool.binding)+`,`+
				wantUniqueOwnedGift(tool.binding)+`]}`)
			for _, id := range []string{"STK", "TH1", "file_id"} {
				if strings.Contains(got, id) {
					t.Errorf("file identifier %s leaked", id)
				}
			}
			if tool.binding == "" && strings.Contains(got, "file_ref") {
				t.Error("a business sticker has a file_ref")
			}
		})
	}
}

func TestAvailableGiftsReportEveryDocumentedMember(t *testing.T) {
	e := newPayEnv(t, "bot")
	e.answers["getAvailableGifts"] = `{"ok":true,"result":{"gifts":[` + availableGift() + `]}}`
	got, err := e.run(invokeGiftsAvailableWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	giftJSONEqual(t, got, `{"gifts":[`+wantAvailableGift("bot")+`]}`)
	if strings.Contains(got, "STK") || strings.Contains(got, "file_id") {
		t.Error("file identifier leaked")
	}
}

func TestAvailableGiftsKeepFormerOutputAndLimit(t *testing.T) {
	e := newPayEnv(t, "bot")
	var list []string
	for i := 0; i < maxAvailableGifts+5; i++ {
		list = append(list, fmt.Sprintf(`{"id":"g%d","star_count":1}`, i))
	}
	e.answers["getAvailableGifts"] = `{"ok":true,"result":{"gifts":[` + strings.Join(list, ",") + `]}}`
	got, err := e.run(invokeGiftsAvailableWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Gifts []map[string]any }
	if json.Unmarshal([]byte(got), &out) != nil || len(out.Gifts) != maxAvailableGifts ||
		!reflect.DeepEqual(out.Gifts[0], map[string]any{"gift_id": "g0", "star_count": float64(1)}) {
		t.Errorf("gifts = %d, first = %v", len(out.Gifts), out.Gifts[0])
	}
}

func TestOwnedGiftsKeepFormerMembersWithoutNewOnes(t *testing.T) {
	e := newPayEnv(t, "-1001")
	e.answers["getChatGifts"] = `{"ok":true,"result":{"total_count":1,"gifts":[{"type":"regular","send_date":5,` +
		`"is_saved":false,"text":"t","convert_star_count":2}]}}`
	got, err := e.run(invokeGiftsChatWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"total_count":1,"gifts":[{"type":"regular","send_date":5,"is_saved":false,"text":"t","convert_star_count":2}]}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestGiftFiltersReachEveryPage(t *testing.T) {
	kinds := map[string]any{"exclude_unlimited": true, "exclude_limited_upgradable": false,
		"exclude_limited_non_upgradable": true, "exclude_unique": true, "exclude_from_blockchain": false,
		"sort_by_price": true}
	saved := map[string]any{"exclude_unsaved": true, "exclude_saved": false}
	merge := func(maps ...map[string]any) map[string]any {
		out := map[string]any{}
		for _, m := range maps {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	cases := []struct {
		tool    giftTool
		filters map[string]any
		base    map[string]any
	}{
		{ownedGiftTools()[0], merge(kinds, saved), map[string]any{"business_connection_id": "b1"}},
		{ownedGiftTools()[1], merge(kinds, saved), map[string]any{"chat_id": "-1001"}},
		{ownedGiftTools()[2], kinds, map[string]any{"user_id": float64(7)}},
	}
	for _, tc := range cases {
		t.Run(tc.tool.name, func(t *testing.T) {
			e := newPayEnv(t, tc.tool.targets...)
			e.answers[tc.tool.method] = `{"ok":true,"result":{"total_count":0,"gifts":[]}}`
			e.answers["getChatMember"] = memberOK
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.tool.args), &args)
			for _, offset := range []string{"", "next"} {
				arguments := merge(args, tc.filters)
				want := merge(tc.base, tc.filters)
				if offset != "" {
					arguments["offset"], want["offset"] = offset, offset
				}
				raw, _ := json.Marshal(arguments)
				before := len(e.calls)
				if _, err := e.run(tc.tool.invoke, string(raw)); err != nil {
					t.Fatal(err)
				}
				last := e.calls[len(e.calls)-1]
				var sent map[string]any
				_ = json.Unmarshal([]byte(last.body), &sent)
				if len(e.calls) <= before || last.method != tc.tool.method || !reflect.DeepEqual(sent, want) {
					t.Errorf("offset %q: request = %s %s, want %v", offset, last.method, last.body, want)
				}
			}
		})
	}
}

func TestGiftFiltersMustBeBooleansBeforeTheCredential(t *testing.T) {
	for _, tool := range ownedGiftTools() {
		var args map[string]any
		_ = json.Unmarshal([]byte(tool.args), &args)
		for _, filter := range []string{"exclude_unlimited", "exclude_limited_upgradable", "exclude_limited_non_upgradable",
			"exclude_unique", "exclude_from_blockchain", "sort_by_price", "exclude_unsaved", "exclude_saved"} {
			for _, bad := range []any{"true", 1, []bool{true}} {
				e := newPayEnv(t, tool.targets...)
				arguments := map[string]any{filter: bad}
				for k, v := range args {
					arguments[k] = v
				}
				raw, _ := json.Marshal(arguments)
				if _, err := e.run(tool.invoke, string(raw)); err == nil || e.lookups != 0 || len(e.calls) != 0 {
					t.Errorf("%s %s=%v: err = %v, lookups = %d, calls = %d", tool.name, filter, bad, err, e.lookups, len(e.calls))
				}
			}
		}
	}
}

func TestUserGiftsOfferNoSavedFilters(t *testing.T) {
	for name, d := range map[string]capability.Descriptor{"business": businessGifts, "chat": giftsChat, "user": giftsUser} {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		offered := map[string]bool{}
		for _, a := range d.Arguments {
			offered[a.Name] = true
		}
		for _, filter := range []string{"exclude_unsaved", "exclude_saved"} {
			_, inSchema := schema.Properties[filter]
			if want := name != "user"; inSchema != want || offered[filter] != want {
				t.Errorf("%s %s: schema = %v, argument = %v, want %v", name, filter, inSchema, offered[filter], want)
			}
		}
		for _, filter := range []string{"exclude_unlimited", "exclude_limited_upgradable", "exclude_limited_non_upgradable",
			"exclude_unique", "exclude_from_blockchain", "sort_by_price"} {
			if _, ok := schema.Properties[filter]; !ok || !offered[filter] {
				t.Errorf("%s does not offer %s", name, filter)
			}
		}
		if !strings.Contains(offsetDescription(d), "same filter values") {
			t.Errorf("%s: offset does not demand the same filters", name)
		}
	}
	e := newPayEnv(t, "-1001")
	e.answers["getChatMember"] = memberOK
	for _, filter := range []string{"exclude_unsaved", "exclude_saved"} {
		if _, err := e.run(invokeGiftsUserWith, `{"user_id":7,"`+filter+`":true}`); err == nil || len(e.calls) != 0 {
			t.Errorf("gifts.user accepted %s", filter)
		}
	}
}

func offsetDescription(d capability.Descriptor) string {
	for _, a := range d.Arguments {
		if a.Name == "offset" {
			return a.Description
		}
	}
	return ""
}

func TestGiftEntitiesOverTheLimitAreOmittedAndTextsCleaned(t *testing.T) {
	var many []string
	for i := 0; i <= maxGiftsEntities; i++ {
		many = append(many, `{"type":"bold","offset":0,"length":1}`)
	}
	e := newPayEnv(t, "-1001")
	e.answers["getChatGifts"] = `{"ok":true,"result":{"total_count":2,"gifts":[` +
		`{"type":"regular","text":"x","entities":[` + strings.Join(many, ",") + `],"gift":{"id":"G"}},` +
		`{"type":"unique","gift":{"gift_id":"U","base_name":"Ca\u0007t","model":{"name":"M\u0000"}}}]}}`
	got, err := e.run(invokeGiftsChatWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	giftJSONEqual(t, got, `{"total_count":2,"gifts":[{"type":"regular","gift_id":"G","is_saved":false,"text":"x",`+
		`"gift_details":{"id":"G"},"omitted_fields":["entities"]},`+
		`{"type":"unique","gift_id":"U","name":"Ca\u0007t","is_saved":false,`+
		`"gift_details":{"gift_id":"U","base_name":"Cat","model":{"name":"M"}}}]}`)
}

func TestGiftMembersAreInSchemaAndFields(t *testing.T) {
	var out struct {
		Properties struct {
			Gifts struct {
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"items"`
			} `json:"gifts"`
		} `json:"properties"`
	}
	words := func(desc string) func(string) bool {
		return func(name string) bool { return regexp.MustCompile(`\b` + name + `\b`).MatchString(desc) }
	}
	members := func(raw string) []map[string]any {
		var got struct{ Gifts []map[string]any }
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		return got.Gifts
	}
	check := func(tool string, d capability.Descriptor, gifts []map[string]any) {
		if err := json.Unmarshal(d.OutputSchema, &out); err != nil {
			t.Fatal(err)
		}
		var description string
		for _, f := range d.Fields {
			if f.Name == "gifts" {
				description = f.Description
			}
		}
		documented := words(description)
		var details struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if raw, ok := out.Properties.Gifts.Items.Properties["gift_details"]; ok {
			_ = json.Unmarshal(raw, &details)
		}
		for _, gift := range gifts {
			for name, value := range gift {
				if _, ok := out.Properties.Gifts.Items.Properties[name]; !ok || !documented(name) {
					t.Errorf("%s: %s is missing from schema or fields", tool, name)
				}
				if sub, ok := value.(map[string]any); ok && name == "gift_details" {
					for inner := range sub {
						if _, ok := details.Properties[inner]; !ok || !documented(inner) {
							t.Errorf("%s: gift_details.%s is missing from schema or fields", tool, inner)
						}
					}
				}
			}
		}
	}
	for _, tool := range ownedGiftTools() {
		e := newPayEnv(t, tool.targets...)
		e.answers[tool.method] = `{"ok":true,"result":{"total_count":2,"gifts":[` + regularOwnedGift() + `,` + uniqueOwnedGift() + `]}}`
		e.answers["getChatMember"] = memberOK
		got, err := e.run(tool.invoke, tool.args)
		if err != nil {
			t.Fatal(err)
		}
		d := map[string]capability.Descriptor{"business": businessGifts, "chat": giftsChat, "user": giftsUser}[tool.name]
		check(tool.name, d, members(got))
	}
	e := newPayEnv(t, "bot")
	e.answers["getAvailableGifts"] = `{"ok":true,"result":{"gifts":[` + availableGift() + `]}}`
	got, err := e.run(invokeGiftsAvailableWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	check("available", giftsAvailable, members(got))
}

// bigUniqueGift is a unique gift with long identifiers and the duplicate thumb member as Telegram sends them.
func bigUniqueGift(i int) string {
	id := func(n int) string { return strings.Repeat(string(rune('A'+n%26)), 90) }
	sticker := func(n int) string {
		thumb := fmt.Sprintf(`{"file_id":"%s","file_unique_id":"%s","file_size":3000,"width":320,"height":320}`, id(n), id(n)[:16])
		return fmt.Sprintf(`{"width":512,"height":512,"emoji":"E","set_name":"Gifts%d","is_animated":true,"is_video":false,`+
			`"type":"regular","thumbnail":%s,"thumb":%s,"file_id":"%s","file_unique_id":"%s","file_size":12000,`+
			`"premium_animation":{"file_id":"%s","file_unique_id":"%s","file_size":20000}}`, i, thumb, thumb, id(n+1), id(n + 1)[:16],
			id(n+2), id(n + 2)[:16])
	}
	return fmt.Sprintf(`{"type":"unique","owned_gift_id":"%s","send_date":1,"gift":{"gift_id":"%d","base_name":"Plush Pepe",`+
		`"name":"PlushPepe-%d","number":%d,"model":{"name":"Model name","sticker":%s,"rarity_per_mille":15},`+
		`"symbol":{"name":"Symbol name","sticker":%s,"rarity_per_mille":20},"backdrop":{"name":"Backdrop","colors":`+
		`{"center_color":1,"edge_color":2,"symbol_color":3,"text_color":4},"rarity_per_mille":30},`+
		`"publisher_chat":{"id":-1001234567890,"type":"channel","title":"Publisher"}}}`, id(i), i, i, i, sticker(i), sticker(i+3))
}

func TestFullPageOfGiftsFitsTheResponseLimit(t *testing.T) {
	var list []string
	for i := 0; i < maxGiftsLimit; i++ {
		list = append(list, bigUniqueGift(i))
	}
	body := `{"ok":true,"result":{"total_count":100,"gifts":[` + strings.Join(list, ",") + `]}}`
	t.Logf("answer of %d unique gifts with three stickers each: %d bytes (limit %d)", maxGiftsLimit, len(body), giftsResponseMax)
	if len(body) > giftsResponseMax {
		t.Fatalf("answer of %d bytes exceeds the limit", len(body))
	}
	e := newPayEnv(t, "-1001")
	e.answers["getChatGifts"] = body
	got, err := e.run(invokeGiftsChatWith, `{"limit":100}`)
	if err != nil || strings.Contains(got, "omitted_fields") {
		t.Fatalf("err = %v, omitted = %v", err, strings.Contains(got, "omitted_fields"))
	}
}
