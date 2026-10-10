package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var (
	metaStickerTools = []string{"emojilist", "keywords", "maskposition"}
	metaSetTools     = []string{"title", "thumbnail", "thumbnailfile", "customemoji"}
	metaMethods      = map[string]string{
		"emojilist": "setStickerEmojiList", "keywords": "setStickerKeywords", "maskposition": "setStickerMaskPosition",
		"title": "setStickerSetTitle", "thumbnail": "setStickerSetThumbnail", "thumbnailfile": "setStickerSetThumbnail",
		"customemoji": "setCustomEmojiStickerSetThumbnail",
	}
)

func (e *mediaEnv) metaTool(tool string, arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "emojilist":
		return invokeStickersSetEmojiListWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "keywords":
		return invokeStickersSetKeywordsWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "maskposition":
		return invokeStickersSetMaskPositionWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "title":
		return invokeStickersetsSetTitleWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "thumbnail", "thumbnailfile":
		return invokeStickersetsSetThumbnailWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeStickersetsSetCustomEmojiThumbnailWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

// metaArgs returns valid arguments; the file variants need a written file.
func (e *mediaEnv) metaArgs(t *testing.T, tool, fileID string) map[string]any {
	t.Helper()
	ref := e.ref(botTarget, fileID)
	name := "a_by_mybot"
	switch tool {
	case "emojilist":
		return map[string]any{"name": name, "file_ref": ref, "emoji_list": []string{"😀", "🐱"}}
	case "keywords":
		return map[string]any{"name": name, "file_ref": ref, "keywords": []string{"cat", "pet"}}
	case "maskposition":
		return map[string]any{"name": name, "file_ref": ref, "mask_position": map[string]any{
			"point": "eyes", "x_shift": 0.5, "y_shift": -1, "scale": 2}}
	case "title":
		return map[string]any{"name": name, "title": "Animals"}
	case "thumbnail":
		return map[string]any{"name": name, "user_id": 42, "format": "static"}
	case "thumbnailfile":
		return map[string]any{"name": name, "user_id": 42, "format": "static", "local_path": e.write(t, "private-name.PNG", 12)}
	}
	return map[string]any{"name": name, "custom_emoji_id": "5368324170671202286"}
}

// newMetaEnv serves getMe, getStickerSet, getFile, and the mutation with the given answer.
func newMetaEnv(t *testing.T, mutation func(*http.Request) (*http.Response, error)) *mediaEnv {
	return newOrderEnv(t, orderSet, mutation)
}

func isMetaMutation(path string) bool {
	for _, m := range metaMethods {
		if path == m {
			return true
		}
	}
	return false
}

func metaMutations(e *mediaEnv) int {
	n := 0
	for _, p := range e.paths() {
		if isMetaMutation(p) {
			n++
		}
	}
	return n
}

func TestStickerMetaToolsSendExactRequests(t *testing.T) {
	cases := map[string]struct{ reads, body string }{
		"emojilist":    {"getMe,getStickerSet", `{"sticker":"FID-A","emoji_list":["😀","🐱"]}`},
		"keywords":     {"getMe,getStickerSet", `{"sticker":"FID-A","keywords":["cat","pet"]}`},
		"maskposition": {"getMe,getStickerSet", `{"sticker":"FID-A","mask_position":{"point":"eyes","x_shift":0.5,"y_shift":-1,"scale":2}}`},
		"title":        {"getMe", `{"name":"a_by_mybot","title":"Animals"}`},
		"thumbnail":    {"getMe", `{"name":"a_by_mybot","user_id":42,"format":"static"}`},
		"customemoji":  {"getMe", `{"name":"a_by_mybot","custom_emoji_id":"5368324170671202286"}`},
	}
	for tool, c := range cases {
		e := newMetaEnv(t, okTrue)
		out, err := e.metaTool(tool, e.metaArgs(t, tool, "FID-A"))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		want := c.reads + "," + metaMethods[tool]
		if got := strings.Join(e.paths(), ","); got != want {
			t.Errorf("%s: paths = %s, want %s", tool, got, want)
		}
		last := e.requests[len(e.requests)-1]
		if string(last.body) != c.body || last.contentType != "application/json" {
			t.Errorf("%s: body = %s (%s), want %s", tool, last.body, last.contentType, c.body)
		}
		if got := out.(map[string]any); len(got) != 2 || got["name"] != "a_by_mybot" || got["updated"] != true {
			t.Errorf("%s: result = %v", tool, got)
		}
	}
}

func TestStickerMetaRemovalVariantsOmitTheField(t *testing.T) {
	for tool, c := range map[string]struct {
		arguments func(*mediaEnv) map[string]any
		body      string
	}{
		"keywords": {func(e *mediaEnv) map[string]any {
			return map[string]any{"name": "a_by_mybot", "file_ref": e.ref(botTarget, "FID-A"), "keywords": []string{}}
		}, `{"sticker":"FID-A","keywords":[]}`},
		"maskposition": {func(e *mediaEnv) map[string]any {
			return map[string]any{"name": "a_by_mybot", "file_ref": e.ref(botTarget, "FID-A")}
		}, `{"sticker":"FID-A"}`},
		"customemoji": {func(*mediaEnv) map[string]any { return map[string]any{"name": "a_by_mybot"} },
			`{"name":"a_by_mybot"}`},
	} {
		e := newMetaEnv(t, okTrue)
		if _, err := e.metaTool(tool, c.arguments(e)); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if got := string(e.requests[len(e.requests)-1].body); got != c.body {
			t.Errorf("%s: body = %s, want %s", tool, got, c.body)
		}
	}
	e := newMetaEnv(t, okTrue)
	if _, err := e.metaTool("keywords", map[string]any{"name": "a_by_mybot", "file_ref": e.ref(botTarget, "FID-A")}); err == nil {
		t.Error("keywords without a list was accepted")
	}
}

func TestStickerSetThumbnailSendsAnExactMultipartBody(t *testing.T) {
	for _, c := range []struct{ format, file string }{
		{"static", "private.PNG"}, {"static", "private.webp"}, {"animated", "private.tgs"}, {"video", "private.webm"},
	} {
		e := newMetaEnv(t, okTrue)
		arguments := e.metaArgs(t, "thumbnail", "")
		arguments["format"] = c.format
		arguments["local_path"] = e.write(t, c.file, 12)
		if _, err := e.metaTool("thumbnail", arguments); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if got := strings.Join(e.paths(), ","); got != "getMe,setStickerSetThumbnail" {
			t.Errorf("paths = %s", got)
		}
		fields, files := parts(t, e.requests[1])
		ext := c.file[strings.LastIndex(c.file, "."):]
		if len(fields) != 3 || fields["name"] != "a_by_mybot" || fields["user_id"] != "42" || fields["format"] != c.format {
			t.Errorf("fields = %v", fields)
		}
		if len(files) != 1 || files["thumbnail"] != [2]string{"file" + ext, "xxxxxxxxxxxx"} {
			t.Errorf("files = %v", files)
		}
		if strings.Contains(string(e.requests[1].body), "private") {
			t.Error("request body contains the local name")
		}
	}
}

func TestStickerMetaToolsRejectBeforeSecretsAndIO(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	noBot := newMediaEnv(t)
	ref := e.ref(botTarget, "X")
	emoji21, kw21 := make([]string, 21), make([]string, 21)
	for i := range emoji21 {
		emoji21[i], kw21[i] = "😀", "k"
	}
	mask := func(key string, value any) map[string]any {
		m := map[string]any{"point": "eyes", "x_shift": 0, "y_shift": 0, "scale": 1}
		m[key] = value
		return map[string]any{"name": "a_by_mybot", "file_ref": ref, "mask_position": m}
	}
	type bad struct {
		tool string
		args map[string]any
	}
	cases := map[string]bad{
		"empty emoji list": {"emojilist", map[string]any{"name": "a_by_mybot", "file_ref": ref, "emoji_list": []string{}}},
		"many emoji":       {"emojilist", map[string]any{"name": "a_by_mybot", "file_ref": ref, "emoji_list": emoji21}},
		"empty emoji":      {"emojilist", map[string]any{"name": "a_by_mybot", "file_ref": ref, "emoji_list": []string{""}}},
		"long emoji": {"emojilist", map[string]any{"name": "a_by_mybot", "file_ref": ref,
			"emoji_list": []string{strings.Repeat("a", 17)}}},
		"many keywords": {"keywords", map[string]any{"name": "a_by_mybot", "file_ref": ref, "keywords": kw21}},
		"empty keyword": {"keywords", map[string]any{"name": "a_by_mybot", "file_ref": ref, "keywords": []string{""}}},
		"keyword total": {"keywords", map[string]any{"name": "a_by_mybot", "file_ref": ref,
			"keywords": []string{strings.Repeat("a", 40), strings.Repeat("b", 25)}}},
		"mask point":       {"maskposition", mask("point", "nose")},
		"mask x range":     {"maskposition", mask("x_shift", 2.5)},
		"mask y range":     {"maskposition", mask("y_shift", -2.5)},
		"mask scale zero":  {"maskposition", mask("scale", 0)},
		"mask scale large": {"maskposition", mask("scale", 4.5)},
		"mask NaN":         {"maskposition", mask("scale", "NaN")},
		"mask Inf":         {"maskposition", mask("x_shift", "Infinity")},
		"mask missing":     {"maskposition", mask("scale", nil)},
		"mask extra":       {"maskposition", mask("angle", 1)},
		"dash name":        {"emojilist", map[string]any{"name": "a-b", "file_ref": ref, "emoji_list": []string{"😀"}}},
		"raw file id":      {"keywords", map[string]any{"name": "a_by_mybot", "file_ref": "AgADraw", "keywords": []string{}}},
		"chat ref": {"keywords", map[string]any{"name": "a_by_mybot", "file_ref": e.ref(mediaChat, "X"),
			"keywords": []string{}}},
		"empty title":    {"title", map[string]any{"name": "a_by_mybot", "title": ""}},
		"long title":     {"title", map[string]any{"name": "a_by_mybot", "title": strings.Repeat("t", 65)}},
		"title name":     {"title", map[string]any{"name": strings.Repeat("a", 65), "title": "T"}},
		"title extra":    {"title", map[string]any{"name": "a_by_mybot", "title": "T", "user_id": 1}},
		"thumb user":     {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 0, "format": "static"}},
		"thumb format":   {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "gif"}},
		"thumb no fmt":   {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1}},
		"thumb ext":      {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "static", "local_path": e.write(t, "a.tgs", 4)}},
		"thumb ext anim": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "animated", "local_path": e.write(t, "a.webp", 4)}},
		"thumb ext vid":  {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "video", "local_path": e.write(t, "a.png", 4)}},
		"thumb static big": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "static",
			"local_path": e.write(t, "big.webp", maxStaticThumbnailBytes+1)}},
		"thumb anim big": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "animated",
			"local_path": e.write(t, "big.tgs", maxMovingThumbnailBytes+1)}},
		"thumb video big": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "video",
			"local_path": e.write(t, "big.webm", maxMovingThumbnailBytes+1)}},
		"thumb outside": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "static",
			"local_path": "/etc/passwd.png"}},
		"thumb url": {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "static",
			"local_path": "https://example.org/a.png"}},
		"thumb file_ref":   {"thumbnail", map[string]any{"name": "a_by_mybot", "user_id": 1, "format": "static", "file_ref": ref}},
		"emoji id letters": {"customemoji", map[string]any{"name": "a_by_mybot", "custom_emoji_id": "12a"}},
		"emoji id empty":   {"customemoji", map[string]any{"name": "a_by_mybot", "custom_emoji_id": ""}},
		"emoji id long":    {"customemoji", map[string]any{"name": "a_by_mybot", "custom_emoji_id": strings.Repeat("1", 21)}},
		"emoji id number":  {"customemoji", map[string]any{"name": "a_by_mybot", "custom_emoji_id": 12}},
	}
	for name, c := range cases {
		if _, err := e.metaTool(c.tool, c.args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, tool := range append(append([]string{}, metaStickerTools...), metaSetTools...) {
		if _, err := noBot.metaTool(tool, noBot.metaArgs(t, tool, "X")); err == nil {
			t.Errorf("%s without a bot target was accepted", tool)
		}
	}
	for _, env := range []*mediaEnv{e, noBot} {
		if env.lookups != 0 || len(env.requests) != 0 {
			t.Errorf("secret lookups = %d, requests = %d, want none", env.lookups, len(env.requests))
		}
	}
	// A reference signed for another token is caught before the first request.
	for _, tool := range metaStickerTools {
		env := newMetaEnv(t, okTrue)
		arguments := env.metaArgs(t, tool, "FID-A")
		arguments["file_ref"] = signRef("999:other_bot-token", refFile, botTarget, "FID-A")
		if _, err := env.metaTool(tool, arguments); err == nil || len(env.requests) != 0 {
			t.Errorf("%s foreign token: err = %v, requests = %v", tool, err, env.paths())
		}
	}
}

func TestStickerMetaToolsRejectForeignSetsWithOnlyGetMe(t *testing.T) {
	all := append(append([]string{}, metaStickerTools...), metaSetTools...)
	for _, tool := range all {
		for _, name := range []string{"a_by_otherbot", "mybot", "_by_mybot", "a_by_mybot_x"} {
			e := newMetaEnv(t, okTrue)
			arguments := e.metaArgs(t, tool, "FID-A")
			arguments["name"] = name
			if _, err := e.metaTool(tool, arguments); err == nil || strings.Contains(err.Error(), name+"\"") {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
			if got := strings.Join(e.paths(), ","); got != "getMe" {
				t.Errorf("%s %s: paths = %s", tool, name, got)
			}
		}
	}
}

func TestStickerMetaToolsRejectAStickerOutsideTheSet(t *testing.T) {
	foreign := `{"ok":true,"result":{"name":"a_by_mybot","stickers":[{"file_id":"FID-A","file_unique_id":"UNI-A"}]}}`
	for _, tool := range metaStickerTools {
		e := newOrderEnv(t, foreign, okTrue)
		_, err := e.metaTool(tool, e.metaArgs(t, tool, "FID-OTHER"))
		if err == nil || strings.Contains(err.Error(), "FID-OTHER") || strings.Contains(err.Error(), "a_by_mybot") {
			t.Errorf("%s: err = %v", tool, err)
		}
		if got := strings.Join(e.paths(), ","); got != "getMe,getStickerSet,getFile" {
			t.Errorf("%s: paths = %s", tool, got)
		}
	}
	// The stable unique identifier matches after one getFile read.
	e := newMetaEnv(t, okTrue)
	if _, err := e.metaTool("emojilist", e.metaArgs(t, "emojilist", "FID-OTHER")); err != nil || metaMutations(e) != 1 {
		t.Errorf("unique id match: err = %v, paths = %v", err, e.paths())
	}
}

func TestStickerMetaMutationsSendOnceAndReportAnUnclearOutcome(t *testing.T) {
	all := append(append([]string{}, metaStickerTools...), metaSetTools...)
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout":  func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"5xx":      func(*http.Request) (*http.Response, error) { return response(502, `{"ok":false}`), nil },
		"unusable": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{"x":1}}`), nil },
		"false":    func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":false}`), nil },
		"garbage":  func(*http.Request) (*http.Response, error) { return response(200, `<html>`), nil },
	} {
		for _, tool := range all {
			e := newMetaEnv(t, answer)
			_, err := e.metaTool(tool, e.metaArgs(t, tool, "FID-A"))
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || metaMutations(e) != 1 {
				t.Errorf("%s %s: err = %v, paths = %v", tool, name, err, e.paths())
			}
		}
	}
}

func TestStickerMetaErrorsNeverCarryTelegramDescriptions(t *testing.T) {
	failing := func(*http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"description":"PROVIDER-CANARY"}`), nil
	}
	all := append(append([]string{}, metaStickerTools...), metaSetTools...)
	for _, stage := range []string{"getMe", "getStickerSet", "getFile", "mutation"} {
		for _, tool := range all {
			if stage == "getFile" && !isStickerTool(tool) {
				continue
			}
			e := newMetaEnv(t, failing)
			inner := e.answer
			e.answer = func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/"+stage) {
					return failing(r)
				}
				return inner(r)
			}
			id := "FID-A"
			if stage == "getFile" {
				id = "FID-OTHER"
			}
			if _, err := e.metaTool(tool, e.metaArgs(t, tool, id)); err == nil || strings.Contains(err.Error(), "PROVIDER-CANARY") {
				t.Errorf("%s %s: err = %v", tool, stage, err)
			}
		}
	}
}

func isStickerTool(tool string) bool {
	for _, t := range metaStickerTools {
		if t == tool {
			return true
		}
	}
	return false
}

func TestStickerMetaToolsDeclareRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]capability.Idempotency{
		stickersSetEmojiList.ID: capability.IdempotencyIdempotent, stickersSetKeywords.ID: capability.IdempotencyIdempotent,
		stickersSetMaskPosition.ID: capability.IdempotencyIdempotent, stickersetsSetTitle.ID: capability.IdempotencyIdempotent,
		stickersetsSetThumbnail.ID:            capability.IdempotencyUnknown,
		stickersetsSetCustomEmojiThumbnail.ID: capability.IdempotencyIdempotent,
	}
	for _, d := range []capability.Descriptor{stickersSetEmojiList, stickersSetKeywords, stickersSetMaskPosition,
		stickersetsSetTitle, stickersetsSetThumbnail, stickersetsSetCustomEmojiThumbnail} {
		r := d.Risk
		files := d.ID == stickersetsSetThumbnail.ID
		if d.Group != groupStickers || r.Effect != capability.EffectUpdate || r.Idempotency != want[d.ID] || !r.OpenWorld ||
			r.Confirmation != capability.ConfirmationRequired || (d.LocalFiles != "") != files || d.RequiresToolAllowList {
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
