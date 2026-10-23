package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func (e *mediaEnv) sticker(tool string, arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "send":
		return invokeStickersSendWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "set":
		return invokeStickersetsGetWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "emoji":
		return invokeStickersCustomEmojiWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeTopicsIconStickersWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

func TestStickerSendUploadsALocalFileUnderANeutralName(t *testing.T) {
	e := newMediaEnv(t)
	path := e.write(t, "private-name.WEBP", 12)
	out, err := e.sticker("send", map[string]any{"local_path": path, "emoji": "😀", "reply_to_message_id": 5,
		"message_thread_id": 9, "disable_notification": true, "protect_content": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/sendSticker" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	want := map[string]string{"chat_id": mediaChat, "emoji": "😀", "reply_parameters": `{"message_id":5}`,
		"message_thread_id": "9", "disable_notification": "true", "protect_content": "true"}
	if len(fields) != len(want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
	for name, value := range want {
		if fields[name] != value {
			t.Errorf("field %s = %q, want %q", name, fields[name], value)
		}
	}
	if len(files) != 1 || files["sticker"] != [2]string{"file.WEBP", "xxxxxxxxxxxx"} {
		t.Errorf("files = %v", files)
	}
	if strings.Contains(string(e.requests[0].body), "private-name") {
		t.Error("request body contains the local name")
	}
	result := out.(map[string]any)
	if len(result) != 2 || result["message_id"] != int64(7) || result["date"] != int64(1700000000) {
		t.Errorf("result = %v", result)
	}
}

func TestStickerSendFromARefUsesJSONForChatAndBotBindings(t *testing.T) {
	for _, binding := range []string{mediaChat, botTarget} {
		e := newMediaEnv(t, mediaChat, "bot")
		_, err := e.sticker("send", map[string]any{"file_ref": e.ref(binding, "FID-1"), "reply_to_message_id": 3})
		if err != nil {
			t.Fatalf("%s: %v", binding, err)
		}
		r := e.requests[0]
		if len(e.requests) != 1 || r.path != "POST /bot"+testToken+"/sendSticker" || r.contentType != "application/json" {
			t.Fatalf("request = %+v", r)
		}
		if want := `{"chat_id":"-1001","sticker":"FID-1","reply_parameters":{"message_id":3}}`; string(r.body) != want {
			t.Errorf("body = %s, want %s", r.body, want)
		}
	}
}

func TestStickerSendRejectsBeforeSecretsAndIO(t *testing.T) {
	outside := t.TempDir()
	e := newMediaEnv(t, mediaChat, "-1002", "bot")
	good := e.write(t, "a.webp", 4)
	big := e.write(t, "big.webp", maxStickerBytes+1)
	cases := map[string]map[string]any{
		"foreign chat":     {"file_ref": e.ref(mediaChat, "X"), "chat": "-999"},
		"several chats":    {"file_ref": e.ref(mediaChat, "X")},
		"other chat's ref": {"file_ref": e.ref("-1002", "X"), "chat": mediaChat},
		"unbound ref":      {"file_ref": e.ref("-777", "X"), "chat": mediaChat},
		"callback ref":     {"file_ref": signRef(testToken, refCallback, botTarget, "X"), "chat": mediaChat},
		"raw file id":      {"file_ref": "AgADBAADraw", "chat": mediaChat},
		"both sources":     {"file_ref": e.ref(botTarget, "X"), "local_path": good, "chat": mediaChat},
		"no source":        {"chat": mediaChat},
		"extension":        {"local_path": e.write(t, "a.png", 4), "chat": mediaChat},
		"no extension":     {"local_path": e.write(t, "a", 4), "chat": mediaChat},
		"outside release":  {"local_path": filepath.Join(outside, "b.webp"), "chat": mediaChat},
		"too large":        {"local_path": big, "chat": mediaChat},
		"emoji with ref":   {"file_ref": e.ref(botTarget, "X"), "emoji": "😀", "chat": mediaChat},
		"long emoji":       {"local_path": good, "emoji": strings.Repeat("a", 17), "chat": mediaChat},
		"reply markup":     {"local_path": good, "reply_markup": map[string]any{}, "chat": mediaChat},
		"business":         {"local_path": good, "business_connection_id": "x", "chat": mediaChat},
	}
	for name, arguments := range cases {
		_, err := e.sticker("send", arguments)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		for _, leaked := range []string{outside, "-999", "-777", e.dir} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("%s error leaks %q: %v", name, leaked, err)
			}
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
	other := signRef("999:other_bot-token", refFile, botTarget, "X")
	if _, err := e.sticker("send", map[string]any{"file_ref": other, "chat": mediaChat}); err == nil || len(e.requests) != 0 {
		t.Errorf("a reference of another bot was accepted: %v", err)
	}
}

func TestStickerSendSendsOnceAndReportsAnUnclearOutcome(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout":  func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"5xx":      func(*http.Request) (*http.Response, error) { return response(502, `{"ok":false}`), nil },
		"unusable": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{"x":1}}`), nil },
		"garbage":  func(*http.Request) (*http.Response, error) { return response(200, `<html>`), nil },
	} {
		e := newMediaEnv(t)
		e.answer = answer
		_, err := e.sticker("send", map[string]any{"file_ref": e.ref(mediaChat, "X")})
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(e.requests) != 1 {
			t.Errorf("%s: err = %v, requests = %d", name, err, len(e.requests))
		}
	}
}

const stickerAnswer = `{"ok":true,"result":{"name":"pack_by_bot","title":"Pack","sticker_type":"regular","secret":"x",` +
	`"stickers":[{"file_id":"FID","file_unique_id":"UNI","type":"regular","emoji":"😀","is_animated":true,` +
	`"is_video":false,"custom_emoji_id":"123","set_name":"pack_by_bot","thumbnail":{"file_id":"T"},` +
	`"file_size":99},{"file_id":"FID-SECOND","type":"custom_emoji","custom_emoji_id":"not-digits"}]}}`

func TestStickerSetGetUsesAllowListAndRefsOnlyWithBot(t *testing.T) {
	for _, withBot := range []bool{false, true} {
		e := newMediaEnv(t)
		if withBot {
			e = newMediaEnv(t, mediaChat, "bot")
		}
		e.answer = func(*http.Request) (*http.Response, error) { return response(200, stickerAnswer), nil }
		out, err := e.sticker("set", map[string]any{"name": "pack_by_bot"})
		if err != nil {
			t.Fatal(err)
		}
		if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/getStickerSet" ||
			string(e.requests[0].body) != `{"name":"pack_by_bot"}` {
			t.Fatalf("requests = %+v", e.requests)
		}
		encoded, _ := json.Marshal(out)
		text := string(encoded)
		for _, leaked := range []string{"FID", "FID-SECOND", "secret", "not-digits", `"T"`} {
			if strings.Contains(text, leaked) {
				t.Errorf("output leaks %q: %s", leaked, text)
			}
		}
		set := out.(stickerSetOut)
		if set.Name != "pack_by_bot" || set.Title != "Pack" || set.StickerType != "regular" || len(set.Stickers) != 2 ||
			set.Stickers[0].Emoji != "😀" || !set.Stickers[0].IsAnimated || set.Stickers[0].CustomEmojiID != "123" ||
			set.Stickers[0].FileUniqueID != "UNI" || set.Stickers[1].CustomEmojiID != "" || set.Stickers[0].FileSize != 99 {
			t.Errorf("set = %+v", set)
		}
		ref := set.Stickers[0].FileRef
		if !withBot {
			if ref != "" || set.Stickers[0].Thumbnail != nil {
				t.Errorf("file_ref or thumbnail without bot target: %q, %+v", ref, set.Stickers[0].Thumbnail)
			}
			continue
		}
		if th := set.Stickers[0].Thumbnail; th == nil || parsedID(t, e, th.FileRef) != "T" {
			t.Errorf("thumbnail = %+v", th)
		}
		parsed, err := parseRef(e.resolved, refFile, ref)
		if err != nil || parsed.binding != botTarget || parsed.id != "FID" {
			t.Errorf("file_ref = %+v, %v", parsed, err)
		}
	}
}

func TestStickerReadsValidateBeforeSecretsAndIO(t *testing.T) {
	e := newMediaEnv(t)
	many := make([]string, 201)
	for i := range many {
		many[i] = "1"
	}
	for name, call := range map[string]func() (any, error){
		"empty name":   func() (any, error) { return e.sticker("set", map[string]any{"name": ""}) },
		"dash name":    func() (any, error) { return e.sticker("set", map[string]any{"name": "a-b"}) },
		"long name":    func() (any, error) { return e.sticker("set", map[string]any{"name": strings.Repeat("a", 65)}) },
		"unicode name": func() (any, error) { return e.sticker("set", map[string]any{"name": "pä"}) },
		"extra":        func() (any, error) { return e.sticker("set", map[string]any{"name": "a", "x": 1}) },
		"no ids":       func() (any, error) { return e.sticker("emoji", map[string]any{"custom_emoji_ids": []string{}}) },
		"many ids":     func() (any, error) { return e.sticker("emoji", map[string]any{"custom_emoji_ids": many}) },
		"letter id":    func() (any, error) { return e.sticker("emoji", map[string]any{"custom_emoji_ids": []string{"12a"}}) },
		"long id": func() (any, error) {
			return e.sticker("emoji", map[string]any{"custom_emoji_ids": []string{strings.Repeat("1", 21)}})
		},
		"icon args": func() (any, error) { return e.sticker("icons", map[string]any{"x": 1}) },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
}

func TestCustomEmojiAndTopicIconReadsUseFixedMethods(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":[{"file_id":"FID","type":"custom_emoji","custom_emoji_id":"9",`+
			`"emoji":"x","extra":1}]}`), nil
	}
	out, err := e.sticker("emoji", map[string]any{"custom_emoji_ids": []string{"9", "10"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.requests[0].path != "POST /bot"+testToken+"/getCustomEmojiStickers" ||
		string(e.requests[0].body) != `{"custom_emoji_ids":["9","10"]}` {
		t.Errorf("request = %+v", e.requests[0])
	}
	if s := out.(stickersOut).Stickers; len(s) != 1 || s[0].CustomEmojiID != "9" || s[0].FileRef == "" {
		t.Errorf("stickers = %+v", s)
	}
	if _, err := e.sticker("icons", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if e.requests[1].path != "POST /bot"+testToken+"/getForumTopicIconStickers" || string(e.requests[1].body) != `{}` {
		t.Errorf("request = %+v", e.requests[1])
	}
}

func TestStickerReadsRejectAnUnusableAnswerWithoutProviderText(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"description":"PROVIDER-CANARY"}`), nil
	}
	_, err := e.sticker("set", map[string]any{"name": "nope"})
	if err == nil || strings.Contains(err.Error(), "PROVIDER-CANARY") {
		t.Errorf("err = %v", err)
	}
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{"x":1}}`), nil }
	if _, err := e.sticker("set", map[string]any{"name": "nope"}); err == nil {
		t.Error("an answer without a name was accepted")
	}
}

func TestStickerToolsDeclareRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{stickersSend, stickersetsGet, stickersCustomEmoji, topicsIconStickers} {
		read := d.ID != stickersSend.ID
		r := d.Risk
		if d.Group != groupStickers || (r.Effect == capability.EffectRead) != read || !r.OpenWorld ||
			(r.Confirmation == capability.ConfirmationRequired) == read ||
			(r.Idempotency == capability.IdempotencyNonIdempotent) == read ||
			(d.LocalFiles != "") == read || d.RequiresToolAllowList {
			t.Errorf("%s descriptor = %+v", d.ID, d)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("%s is part of profile %s", d.ID, profile.ID)
				}
			}
		}
	}
}

var refPattern = regexp.MustCompile(`"file_ref":"[A-Za-z0-9_-]+"`)

func parsedID(t *testing.T, e *mediaEnv, ref string) string {
	t.Helper()
	parsed, err := parseRef(e.resolved, refFile, ref)
	if err != nil || parsed.binding != botTarget {
		t.Fatalf("file_ref %q = %+v, %v", ref, parsed, err)
	}
	return parsed.id
}

// A zero shift and a float that has no short decimal form must reach the output as the provider wrote them.
const fullSticker = `{"file_id":"FID","file_unique_id":"UNI","type":"mask","width":512,"height":256,` +
	`"is_animated":false,"is_video":true,"thumbnail":{"file_id":"THUMB","file_unique_id":"TU","width":128,` +
	`"height":64,"file_size":1234},"emoji":"x","set_name":"pack_by_bot",` +
	`"premium_animation":{"file_id":"PREM","file_unique_id":"PU","file_size":4321,"file_path":"secret/p"},` +
	`"mask_position":{"point":"chin","x_shift":0,"y_shift":0.30000000000000004,"scale":2.5e-7},` +
	`"custom_emoji_id":"9","needs_repainting":true,"file_size":999}`

const fullStickerSet = `{"name":"pack_by_bot","title":"Pack","sticker_type":"mask","stickers":[` + fullSticker + `],` +
	`"thumbnail":{"file_id":"SETTHUMB","file_unique_id":"SU","width":100,"height":100,"file_size":77}}`

func TestStickerReadsPrintEveryDocumentedField(t *testing.T) {
	const wantSticker = `{"emoji":"x","type":"mask","is_animated":false,"is_video":true,"custom_emoji_id":"9",` +
		`"set_name":"pack_by_bot","file_unique_id":"UNI","file_ref":"REF","width":512,"height":256,` +
		`"thumbnail":{"file_ref":"REF","file_unique_id":"TU","width":128,"height":64,"file_size":1234},` +
		`"premium_animation":{"file_ref":"REF","file_unique_id":"PU","file_size":4321},` +
		`"mask_position":{"point":"chin","x_shift":0,"y_shift":0.30000000000000004,"scale":2.5e-7},` +
		`"needs_repainting":true,"file_size":999}`
	normalize := func(v any) string {
		encoded, _ := json.Marshal(v)
		text := string(encoded)
		return refPattern.ReplaceAllString(text, `"file_ref":"REF"`)
	}
	for tool, want := range map[string]string{
		"set": `{"name":"pack_by_bot","title":"Pack","sticker_type":"mask","thumbnail":{"file_ref":"REF",` +
			`"file_unique_id":"SU","width":100,"height":100,"file_size":77},"stickers":[` + wantSticker + `]}`,
		"emoji": `{"stickers":[` + wantSticker + `]}`,
		"icons": `{"stickers":[` + wantSticker + `]}`,
	} {
		e := newMediaEnv(t, mediaChat, "bot")
		answer := fullSticker
		if tool == "set" {
			answer = fullStickerSet
		} else {
			answer = "[" + fullSticker + "]"
		}
		e.answer = func(*http.Request) (*http.Response, error) {
			return response(200, `{"ok":true,"result":`+answer+`}`), nil
		}
		args := map[string]any{}
		switch tool {
		case "set":
			args["name"] = "pack_by_bot"
		case "emoji":
			args["custom_emoji_ids"] = []string{"9"}
		}
		out, err := e.sticker(tool, args)
		if err != nil {
			t.Fatal(err)
		}
		if got := normalize(out); got != want {
			t.Errorf("%s =\n%s\nwant\n%s", tool, got, want)
		}
		var first stickerOut
		if tool == "set" {
			first = out.(stickerSetOut).Stickers[0]
			if got := parsedID(t, e, out.(stickerSetOut).Thumbnail.FileRef); got != "SETTHUMB" {
				t.Errorf("set thumbnail ref id = %q", got)
			}
		} else {
			first = out.(stickersOut).Stickers[0]
		}
		for ref, id := range map[string]string{first.FileRef: "FID", first.Thumbnail.FileRef: "THUMB",
			first.PremiumAnimation.FileRef: "PREM"} {
			if got := parsedID(t, e, ref); got != id {
				t.Errorf("%s ref id = %q", id, got)
			}
		}
		encoded, _ := json.Marshal(out)
		for _, leaked := range []string{"FID", "THUMB", "PREM", "secret/p", "file_path"} {
			if strings.Contains(string(encoded), leaked) {
				t.Errorf("%s leaks %q", tool, leaked)
			}
		}
	}
}

func TestStickerReadsPrintNoFileWithoutTheBotTarget(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":`+fullStickerSet+`}`), nil
	}
	out, err := e.sticker("set", map[string]any{"name": "pack_by_bot"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out)
	for _, leaked := range []string{"file_ref", "thumbnail", "premium_animation", "THUMB", "PREM"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("output without bot target contains %q: %s", leaked, encoded)
		}
	}
	for _, kept := range []string{`"width":512`, `"needs_repainting":true`, `"mask_position"`, `"file_size":999`} {
		if !strings.Contains(string(encoded), kept) {
			t.Errorf("output lacks %q: %s", kept, encoded)
		}
	}
}

func TestStickerItemOutputMembersAreInTheItemSchema(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(stickerItemSchema), &schema); err != nil {
		t.Fatal(err)
	}
	e := newMediaEnv(t, mediaChat, "bot")
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":`+fullStickerSet+`}`), nil
	}
	out, _ := e.sticker("set", map[string]any{"name": "pack_by_bot"})
	var decoded struct {
		Stickers []map[string]json.RawMessage `json:"stickers"`
	}
	encoded, _ := json.Marshal(out)
	_ = json.Unmarshal(encoded, &decoded)
	if len(decoded.Stickers) != 1 || len(decoded.Stickers[0]) != len(schema.Properties) {
		t.Fatalf("sticker members = %d, schema members = %d: %s", len(decoded.Stickers[0]), len(schema.Properties), encoded)
	}
	for key := range decoded.Stickers[0] {
		if _, ok := schema.Properties[key]; !ok || !strings.Contains(stickerFields, key) {
			t.Errorf("member %q missing in schema or fields", key)
		}
	}
}

// The largest answers are a full custom emoji request and a full set; each sticker carries every field at the
// length of real Telegram identifiers.
func TestStickerReadsFitTheResponseLimitAtFullWidth(t *testing.T) {
	id := strings.Repeat("A", 90)
	one := `{"file_id":"` + id + `","file_unique_id":"` + strings.Repeat("u", 20) + `","type":"custom_emoji",` +
		`"width":512,"height":512,"is_animated":true,"is_video":false,"thumbnail":{"file_id":"` + id +
		`","file_unique_id":"` + strings.Repeat("t", 20) + `","width":128,"height":128,"file_size":12345},` +
		`"emoji":"😀","set_name":"` + strings.Repeat("s", 64) + `","premium_animation":{"file_id":"` + id +
		`","file_unique_id":"` + strings.Repeat("p", 20) + `","file_size":123456,"file_path":"` + strings.Repeat("f", 60) +
		`"},"mask_position":{"point":"forehead","x_shift":-0.30000000000000004,"y_shift":0.30000000000000004,` +
		`"scale":1.2345678901234567},"custom_emoji_id":"5368324170671202286","needs_repainting":true,"file_size":123456}`
	list := make([]string, maxStickersOut)
	for i := range list {
		list[i] = one
	}
	answer := "[" + strings.Join(list, ",") + "]"
	t.Logf("full answer of %d stickers = %d bytes, limit %d", maxStickersOut, len(answer), maxStickerResponseBytes)
	if len(answer) > maxStickerResponseBytes {
		t.Fatalf("answer of %d bytes exceeds the response limit %d", len(answer), maxStickerResponseBytes)
	}
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = "5368324170671202286"
	}
	e := newMediaEnv(t, mediaChat, "bot")
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":`+answer+`}`), nil
	}
	out, err := e.sticker("emoji", map[string]any{"custom_emoji_ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out.(stickersOut).Stickers); n != maxStickersOut {
		t.Errorf("stickers = %d, want %d", n, maxStickersOut)
	}
}
