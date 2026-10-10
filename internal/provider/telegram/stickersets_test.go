package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func (e *mediaEnv) stickerSet(tool string, arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "upload":
		return invokeStickersUploadFileWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "create":
		return invokeStickersetsCreateWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "add":
		return invokeStickersetsAddStickerWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeStickersetsDeleteWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

// answerSets serves getMe with the given username and the mutating method with the given answer.
func (e *mediaEnv) answerSets(username string, mutation func(*http.Request) (*http.Response, error)) {
	e.answer = func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			return response(200, `{"ok":true,"result":{"id":7,"username":"`+username+`"}}`), nil
		}
		return mutation(r)
	}
}

func okTrue(*http.Request) (*http.Response, error) {
	return response(200, `{"ok":true,"result":true}`), nil
}

func newSetEnv(t *testing.T) *mediaEnv {
	t.Helper()
	e := newMediaEnv(t, mediaChat, "bot")
	e.answerSets("MyBot", okTrue)
	return e
}

func (e *mediaEnv) inputSticker(binding string) map[string]any {
	return map[string]any{"file_ref": e.ref(binding, "FID-1"), "format": "static", "emoji_list": []string{"😀"},
		"keywords": []string{"smile"}}
}

func mutations(e *mediaEnv) int {
	n := 0
	for _, r := range e.requests {
		if !strings.HasSuffix(r.path, "/getMe") {
			n++
		}
	}
	return n
}

func TestStickerUploadFileSendsAnExactMultipartBodyAndReturnsABotRef(t *testing.T) {
	for ext, format := range map[string]string{".webp": "static", ".TGS": "animated", ".webm": "video"} {
		e := newMediaEnv(t, mediaChat, "bot")
		e.answer = func(*http.Request) (*http.Response, error) {
			return response(200, `{"ok":true,"result":{"file_id":"UP-FID","file_unique_id":"UNI","file_size":5}}`), nil
		}
		path := e.write(t, "private-name"+ext, 12)
		out, err := e.stickerSet("upload", map[string]any{"user_id": 42, "local_path": path})
		if err != nil {
			t.Fatal(err)
		}
		if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/uploadStickerFile" {
			t.Fatalf("requests = %+v", e.requests)
		}
		fields, files := parts(t, e.requests[0])
		if len(fields) != 2 || fields["user_id"] != "42" || fields["sticker_format"] != format {
			t.Errorf("fields = %v, want format %s", fields, format)
		}
		if len(files) != 1 || files["sticker"] != [2]string{"file" + ext, "xxxxxxxxxxxx"} {
			t.Errorf("files = %v", files)
		}
		if strings.Contains(string(e.requests[0].body), "private-name") {
			t.Error("request body contains the local name")
		}
		result := out.(struct {
			FileRef      string `json:"file_ref"`
			FileUniqueID string `json:"file_unique_id,omitempty"`
		})
		parsed, err := parseRef(e.resolved, refFile, result.FileRef)
		if err != nil || parsed.binding != botTarget || parsed.id != "UP-FID" || result.FileUniqueID != "UNI" {
			t.Errorf("result = %+v, ref = %+v, %v", result, parsed, err)
		}
		if encoded, _ := json.Marshal(out); strings.Contains(string(encoded), "UP-FID") {
			t.Errorf("output leaks the file id: %s", encoded)
		}
	}
}

func TestStickerUploadFileRejectsBeforeSecretsAndIO(t *testing.T) {
	noBot := newMediaEnv(t)
	e := newMediaEnv(t, mediaChat, "bot")
	cases := map[*mediaEnv]map[string]map[string]any{
		noBot: {"no bot target": {"user_id": 1, "local_path": noBot.write(t, "a.webp", 4)}},
		e: {
			"no user":      {"local_path": e.write(t, "a.webp", 4)},
			"zero user":    {"user_id": 0, "local_path": e.write(t, "b.webp", 4)},
			"extension":    {"user_id": 1, "local_path": e.write(t, "a.png", 4)},
			"no path":      {"user_id": 1},
			"too large":    {"user_id": 1, "local_path": e.write(t, "big.webp", maxStickerBytes+1)},
			"outside":      {"user_id": 1, "local_path": "/etc/passwd.webp"},
			"format given": {"user_id": 1, "local_path": e.write(t, "c.webp", 4), "sticker_format": "video"},
		},
	}
	for env, set := range cases {
		for name, arguments := range set {
			if _, err := env.stickerSet("upload", arguments); err == nil {
				t.Errorf("%s was accepted", name)
			}
		}
		if env.lookups != 0 || len(env.requests) != 0 {
			t.Errorf("secret lookups = %d, requests = %d, want none", env.lookups, len(env.requests))
		}
	}
}

func TestStickerSetCreateSendsOneExactJSONBody(t *testing.T) {
	e := newSetEnv(t)
	second := e.inputSticker(botTarget)
	second["format"], second["emoji_list"] = "video", []string{"🎉", "🎊"}
	delete(second, "keywords")
	out, err := e.stickerSet("create", map[string]any{"user_id": 42, "name": "Animals_BY_mybot", "title": "Animals",
		"sticker_type": "custom_emoji", "stickers": []any{e.inputSticker(botTarget), second}})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 2 || e.requests[0].path != "POST /bot"+testToken+"/getMe" ||
		e.requests[1].path != "POST /bot"+testToken+"/createNewStickerSet" ||
		e.requests[1].contentType != "application/json" {
		t.Fatalf("requests = %+v", e.requests)
	}
	want := `{"user_id":42,"name":"Animals_BY_mybot","title":"Animals","sticker_type":"custom_emoji","stickers":[` +
		`{"sticker":"FID-1","format":"static","emoji_list":["😀"],"keywords":["smile"]},` +
		`{"sticker":"FID-1","format":"video","emoji_list":["🎉","🎊"]}]}`
	if string(e.requests[1].body) != want {
		t.Errorf("body = %s\nwant   %s", e.requests[1].body, want)
	}
	if got := out.(map[string]any); len(got) != 2 || got["name"] != "Animals_BY_mybot" || got["created"] != true {
		t.Errorf("result = %v", got)
	}
	// The default sticker type is left out.
	e = newSetEnv(t)
	if _, err := e.stickerSet("create", map[string]any{"user_id": 1, "name": "a_by_mybot", "title": "T",
		"stickers": []any{e.inputSticker(botTarget)}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(e.requests[1].body), "sticker_type") {
		t.Errorf("body = %s", e.requests[1].body)
	}
}

func TestStickerSetAddAndDeleteSendOneExactJSONBody(t *testing.T) {
	e := newSetEnv(t)
	out, err := e.stickerSet("add", map[string]any{"user_id": 42, "name": "a_by_mybot", "sticker": e.inputSticker(botTarget)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"user_id":42,"name":"a_by_mybot","sticker":{"sticker":"FID-1","format":"static","emoji_list":["😀"],` +
		`"keywords":["smile"]}}`
	if len(e.requests) != 2 || e.requests[1].path != "POST /bot"+testToken+"/addStickerToSet" ||
		string(e.requests[1].body) != want {
		t.Errorf("requests = %+v", e.requests)
	}
	if got := out.(map[string]any); len(got) != 2 || got["name"] != "a_by_mybot" || got["added"] != true {
		t.Errorf("result = %v", got)
	}
	e = newSetEnv(t)
	out, err = e.stickerSet("delete", map[string]any{"name": "a_by_MYBOT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 2 || e.requests[1].path != "POST /bot"+testToken+"/deleteStickerSet" ||
		string(e.requests[1].body) != `{"name":"a_by_MYBOT"}` {
		t.Errorf("requests = %+v", e.requests)
	}
	if got := out.(map[string]any); len(got) != 2 || got["name"] != "a_by_MYBOT" || got["deleted"] != true {
		t.Errorf("result = %v", got)
	}
}

func TestStickerSetToolsRejectForeignSetsBeforeTheMutation(t *testing.T) {
	for tool, arguments := range map[string]func(*mediaEnv, string) map[string]any{
		"create": func(e *mediaEnv, name string) map[string]any {
			return map[string]any{"user_id": 1, "name": name, "title": "T", "stickers": []any{e.inputSticker(botTarget)}}
		},
		"add": func(e *mediaEnv, name string) map[string]any {
			return map[string]any{"user_id": 1, "name": name, "sticker": e.inputSticker(botTarget)}
		},
		"delete": func(_ *mediaEnv, name string) map[string]any { return map[string]any{"name": name} },
	} {
		for _, name := range []string{"FOREIGN_SET_CANARY", "a_by_otherbot", "_by_mybot", "a_by_mybot_x", "by_mybot"} {
			e := newSetEnv(t)
			_, err := e.stickerSet(tool, arguments(e, name))
			if err == nil || strings.Contains(err.Error(), name) {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
			if len(e.requests) != 1 || !strings.HasSuffix(e.requests[0].path, "/getMe") {
				t.Errorf("%s %s: requests = %+v, want only getMe", tool, name, e.requests)
			}
		}
		e := newSetEnv(t)
		e.answerSets("", okTrue)
		if _, err := e.stickerSet(tool, arguments(e, "a_by_mybot")); err == nil || mutations(e) != 0 {
			t.Errorf("%s accepted a bot without username: %v", tool, err)
		}
	}
}

func TestStickerSetToolsRejectBeforeSecretsAndIO(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	noBot := newMediaEnv(t)
	good := func(env *mediaEnv) map[string]any {
		return map[string]any{"user_id": 1, "name": "a_by_mybot", "title": "T", "stickers": []any{env.inputSticker(botTarget)}}
	}
	with := func(env *mediaEnv, key string, value any) map[string]any {
		m := good(env)
		m[key] = value
		return m
	}
	sticker := func(key string, value any) map[string]any {
		s := e.inputSticker(botTarget)
		s[key] = value
		return with(e, "stickers", []any{s})
	}
	many := make([]any, 51)
	for i := range many {
		many[i] = e.inputSticker(botTarget)
	}
	emoji21, kw21 := make([]string, 21), make([]string, 21)
	for i := range emoji21 {
		emoji21[i], kw21[i] = "😀", "k"
	}
	cases := map[string]map[string]any{
		"zero user":         with(e, "user_id", 0),
		"dash name":         with(e, "name", "a-b_by_mybot"),
		"long name":         with(e, "name", strings.Repeat("a", 65)),
		"empty title":       with(e, "title", ""),
		"long title":        with(e, "title", strings.Repeat("t", 65)),
		"sticker type":      with(e, "sticker_type", "paid"),
		"needs repainting":  with(e, "needs_repainting", true),
		"no stickers":       with(e, "stickers", []any{}),
		"too many stickers": with(e, "stickers", many),
		"format":            sticker("format", "gif"),
		"no format":         sticker("format", ""),
		"no emoji":          sticker("emoji_list", []string{}),
		"many emoji":        sticker("emoji_list", emoji21),
		"empty emoji":       sticker("emoji_list", []string{""}),
		"long emoji":        sticker("emoji_list", []string{strings.Repeat("a", 17)}),
		"many keywords":     sticker("keywords", kw21),
		"keyword total":     sticker("keywords", []string{strings.Repeat("a", 40), strings.Repeat("b", 25)}),
		"mask position":     sticker("mask_position", map[string]any{}),
		"local path":        sticker("local_path", "/x.webp"),
		"url":               sticker("file_ref", "https://example.org/a.webp"),
		"raw file id":       sticker("file_ref", "AgADBAADraw"),
		"chat ref":          sticker("file_ref", e.ref(mediaChat, "X")),
		"callback ref":      sticker("file_ref", signRef(testToken, refCallback, botTarget, "X")),
	}
	for name, arguments := range cases {
		before := e.lookups
		if _, err := e.stickerSet("create", arguments); err == nil || e.lookups != before {
			t.Errorf("create %s: err = %v, lookups = %d", name, err, e.lookups-before)
		}
		list, ok := arguments["stickers"].([]any)
		// The title and the sticker type belong to create only.
		if !ok || len(list) != 1 || arguments["title"] == "" || len(arguments["title"].(string)) > 64 ||
			arguments["sticker_type"] != nil || arguments["needs_repainting"] != nil {
			continue
		}
		single := map[string]any{"user_id": arguments["user_id"], "name": arguments["name"], "sticker": list[0]}
		if _, err := e.stickerSet("add", single); err == nil {
			t.Errorf("add %s was accepted", name)
		}
	}
	for _, name := range []string{"", "a-b", strings.Repeat("a", 65)} {
		if _, err := e.stickerSet("delete", map[string]any{"name": name}); err == nil {
			t.Errorf("delete accepted name %q", name)
		}
	}
	for tool, arguments := range map[string]map[string]any{"create": good(noBot), "delete": {"name": "a_by_mybot"},
		"add": {"user_id": 1, "name": "a_by_mybot", "sticker": noBot.inputSticker(botTarget)}} {
		if _, err := noBot.stickerSet(tool, arguments); err == nil {
			t.Errorf("%s without a bot target was accepted", tool)
		}
	}
	for _, env := range []*mediaEnv{e, noBot} {
		if env.lookups != 0 || len(env.requests) != 0 {
			t.Errorf("secret lookups = %d, requests = %d, want none", env.lookups, len(env.requests))
		}
	}
}

// The credential-free checks must stop every invalid argument before the credential is read.
func TestStickerSetValidationNeedsNoSecret(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	bad := e.inputSticker(mediaChat)
	for tool, arguments := range map[string]map[string]any{
		"create": {"user_id": 1, "name": "a_by_mybot", "title": "T", "stickers": []any{bad}},
		"add":    {"user_id": 1, "name": "a_by_mybot", "sticker": bad},
		"delete": {"name": "a-b"},
		"upload": {"user_id": 0, "local_path": e.write(t, "a.webp", 4)},
	} {
		if _, err := e.stickerSet(tool, arguments); err == nil {
			t.Errorf("%s was accepted", tool)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
	// A reference of another bot token needs the credential to be detected but never reaches the network.
	other := e.inputSticker(botTarget)
	other["file_ref"] = signRef("999:other_bot-token", refFile, botTarget, "X")
	if _, err := e.stickerSet("add", map[string]any{"user_id": 1, "name": "a_by_mybot", "sticker": other}); err == nil ||
		len(e.requests) != 0 {
		t.Errorf("foreign token reference: err = %v, requests = %d", err, len(e.requests))
	}
}

func TestStickerSetMutationsSendOnceAndReportAnUnclearOutcome(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout":  func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"5xx":      func(*http.Request) (*http.Response, error) { return response(502, `{"ok":false}`), nil },
		"unusable": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{"x":1}}`), nil },
		"false":    func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":false}`), nil },
		"garbage":  func(*http.Request) (*http.Response, error) { return response(200, `<html>`), nil },
	} {
		for tool, arguments := range map[string]func(*mediaEnv) map[string]any{
			"create": func(e *mediaEnv) map[string]any {
				return map[string]any{"user_id": 1, "name": "a_by_mybot", "title": "T",
					"stickers": []any{e.inputSticker(botTarget)}}
			},
			"add": func(e *mediaEnv) map[string]any {
				return map[string]any{"user_id": 1, "name": "a_by_mybot", "sticker": e.inputSticker(botTarget)}
			},
			"delete": func(*mediaEnv) map[string]any { return map[string]any{"name": "a_by_mybot"} },
		} {
			e := newMediaEnv(t, mediaChat, "bot")
			e.answerSets("mybot", answer)
			_, err := e.stickerSet(tool, arguments(e))
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || mutations(e) != 1 {
				t.Errorf("%s %s: err = %v, mutations = %d", tool, name, err, mutations(e))
			}
		}
	}
	e := newMediaEnv(t, mediaChat, "bot")
	e.answer = func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }
	_, err := e.stickerSet("upload", map[string]any{"user_id": 1, "local_path": e.write(t, "a.webp", 4)})
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(e.requests) != 1 {
		t.Errorf("upload: err = %v, requests = %d", err, len(e.requests))
	}
}

func TestStickerSetErrorsNeverCarryTelegramDescriptions(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	e.answerSets("mybot", func(*http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"description":"PROVIDER-CANARY"}`), nil
	})
	for tool, arguments := range map[string]map[string]any{
		"create": {"user_id": 1, "name": "a_by_mybot", "title": "T", "stickers": []any{e.inputSticker(botTarget)}},
		"add":    {"user_id": 1, "name": "a_by_mybot", "sticker": e.inputSticker(botTarget)},
		"delete": {"name": "a_by_mybot"},
		"upload": {"user_id": 1, "local_path": e.write(t, "a.webp", 4)},
	} {
		if _, err := e.stickerSet(tool, arguments); err == nil || strings.Contains(err.Error(), "PROVIDER-CANARY") {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
}

func TestStickerSetToolsDeclareRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect capability.Effect
		idem   capability.Idempotency
		files  bool
		guard  bool
	}{
		stickersUploadFile.ID:    {capability.EffectCreate, capability.IdempotencyNonIdempotent, true, false},
		stickersetsCreate.ID:     {capability.EffectCreate, capability.IdempotencyNonIdempotent, false, false},
		stickersetsAddSticker.ID: {capability.EffectUpdate, capability.IdempotencyNonIdempotent, false, false},
		stickersetsDelete.ID:     {capability.EffectDelete, capability.IdempotencyIdempotent, false, true},
	}
	for _, d := range []capability.Descriptor{stickersUploadFile, stickersetsCreate, stickersetsAddSticker, stickersetsDelete} {
		w, r := want[d.ID], d.Risk
		if d.Group != groupStickers || r.Effect != w.effect || r.Idempotency != w.idem || !r.OpenWorld ||
			r.Confirmation != capability.ConfirmationRequired || (d.LocalFiles != "") != w.files ||
			d.RequiresToolAllowList != w.guard {
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
