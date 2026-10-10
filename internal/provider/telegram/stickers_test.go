package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
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
	`"is_video":false,"custom_emoji_id":"123","set_name":"pack_by_bot","width":512,"thumbnail":{"file_id":"T"},` +
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
		for _, leaked := range []string{"FID", "FID-SECOND", "secret", "thumbnail", "width", "not-digits"} {
			if strings.Contains(text, leaked) {
				t.Errorf("output leaks %q: %s", leaked, text)
			}
		}
		set := out.(stickerSetOut)
		if set.Name != "pack_by_bot" || set.Title != "Pack" || set.StickerType != "regular" || len(set.Stickers) != 2 ||
			set.Stickers[0].Emoji != "😀" || !set.Stickers[0].IsAnimated || set.Stickers[0].CustomEmojiID != "123" ||
			set.Stickers[0].FileUniqueID != "UNI" || set.Stickers[1].CustomEmojiID != "" {
			t.Errorf("set = %+v", set)
		}
		ref := set.Stickers[0].FileRef
		if !withBot {
			if ref != "" {
				t.Errorf("file_ref without bot target: %q", ref)
			}
			continue
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
