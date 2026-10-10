package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const orderSet = `{"ok":true,"result":{"name":"a_by_mybot","stickers":[` +
	`{"file_id":"FID-A","file_unique_id":"UNI-A"},{"file_id":"FID-B","file_unique_id":"UNI-B"}]}}`

func (e *mediaEnv) orderTool(tool string, arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "setposition":
		return invokeStickersSetPositionWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "replace":
		return invokeStickersReplaceWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeStickersDeleteWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

// newOrderEnv serves getMe, getStickerSet, getFile, and the mutation with the given answer.
func newOrderEnv(t *testing.T, set string, mutation func(*http.Request) (*http.Response, error)) *mediaEnv {
	t.Helper()
	e := newMediaEnv(t, mediaChat, "bot")
	e.answer = func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			return response(200, `{"ok":true,"result":{"id":7,"username":"mybot"}}`), nil
		case strings.HasSuffix(r.URL.Path, "/getStickerSet"):
			return response(200, set), nil
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			return response(200, `{"ok":true,"result":{"file_unique_id":"UNI-B","file_size":3,"file_path":"x/y"}}`), nil
		}
		return mutation(r)
	}
	return e
}

func (e *mediaEnv) paths() []string {
	var out []string
	for _, r := range e.requests {
		out = append(out, strings.TrimPrefix(r.path, "POST /bot"+testToken+"/"))
	}
	return out
}

func (e *mediaEnv) orderArgs(tool, fileID string) map[string]any {
	ref := e.ref(botTarget, fileID)
	switch tool {
	case "setposition":
		return map[string]any{"name": "a_by_mybot", "file_ref": ref, "position": 1}
	case "replace":
		return map[string]any{"user_id": 42, "name": "a_by_mybot", "old_file_ref": ref, "sticker": e.inputSticker(botTarget)}
	}
	return map[string]any{"name": "a_by_mybot", "file_ref": ref}
}

func TestStickerOrderToolsSendExactRequestsInOrder(t *testing.T) {
	cases := map[string]struct{ method, body, key string }{
		"setposition": {"setStickerPositionInSet", `{"sticker":"FID-A","position":1}`, "position"},
		"replace": {"replaceStickerInSet", `{"user_id":42,"name":"a_by_mybot","old_sticker":"FID-A","sticker":` +
			`{"sticker":"FID-1","format":"static","emoji_list":["😀"],"keywords":["smile"]}}`, "replaced"},
		"delete": {"deleteStickerFromSet", `{"sticker":"FID-A"}`, "deleted"},
	}
	for tool, c := range cases {
		e := newOrderEnv(t, orderSet, okTrue)
		out, err := e.orderTool(tool, e.orderArgs(tool, "FID-A"))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if got, want := strings.Join(e.paths(), ","), "getMe,getStickerSet,"+c.method; got != want {
			t.Errorf("%s: paths = %s, want %s", tool, got, want)
		}
		if string(e.requests[1].body) != `{"name":"a_by_mybot"}` || string(e.requests[2].body) != c.body {
			t.Errorf("%s: bodies = %s, %s", tool, e.requests[1].body, e.requests[2].body)
		}
		if got := out.(map[string]any); len(got) != 2 || got["name"] != "a_by_mybot" || got[c.key] == nil {
			t.Errorf("%s: result = %v", tool, got)
		}
	}
}

func TestStickerOrderToolsMatchByUniqueIDAfterGetFile(t *testing.T) {
	for _, tool := range []string{"setposition", "replace", "delete"} {
		e := newOrderEnv(t, orderSet, okTrue)
		// FID-OTHER is not in the set; getFile reports the unique id of FID-B.
		if _, err := e.orderTool(tool, e.orderArgs(tool, "FID-OTHER")); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		paths := e.paths()
		if len(paths) != 4 || paths[2] != "getFile" || string(e.requests[2].body) != `{"file_id":"FID-OTHER"}` {
			t.Errorf("%s: paths = %v", tool, paths)
		}
		if !strings.Contains(string(e.requests[3].body), "FID-OTHER") {
			t.Errorf("%s: mutation body = %s", tool, e.requests[3].body)
		}
	}
}

func TestStickerOrderToolsRejectAStickerOutsideTheSet(t *testing.T) {
	foreign := `{"ok":true,"result":{"name":"a_by_mybot","stickers":[{"file_id":"FID-A","file_unique_id":"UNI-A"}]}}`
	for _, tool := range []string{"setposition", "replace", "delete"} {
		e := newOrderEnv(t, foreign, okTrue)
		_, err := e.orderTool(tool, e.orderArgs(tool, "FID-OTHER"))
		if err == nil || strings.Contains(err.Error(), "FID-OTHER") || strings.Contains(err.Error(), "a_by_mybot") {
			t.Errorf("%s: err = %v", tool, err)
		}
		if got := strings.Join(e.paths(), ","); got != "getMe,getStickerSet,getFile" {
			t.Errorf("%s: paths = %s", tool, got)
		}
	}
	// A set without any match and a matching file id make no getFile call.
	e := newOrderEnv(t, `{"ok":true,"result":{"name":"a_by_mybot","stickers":[]}}`, okTrue)
	if _, err := e.orderTool("delete", e.orderArgs("delete", "FID-A")); err == nil || mutationsOf(e) != 0 {
		t.Errorf("empty set: err = %v", err)
	}
}

func mutationsOf(e *mediaEnv) int {
	n := 0
	for _, p := range e.paths() {
		switch p {
		case "setStickerPositionInSet", "replaceStickerInSet", "deleteStickerFromSet":
			n++
		}
	}
	return n
}

func TestStickerOrderToolsRejectForeignSetsWithOnlyGetMe(t *testing.T) {
	for _, tool := range []string{"setposition", "replace", "delete"} {
		for _, name := range []string{"a_by_otherbot", "mybot", "_by_mybot", "a"} {
			e := newOrderEnv(t, orderSet, okTrue)
			arguments := e.orderArgs(tool, "FID-A")
			arguments["name"] = name
			if _, err := e.orderTool(tool, arguments); err == nil || strings.Contains(err.Error(), name+"\"") {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
			if got := strings.Join(e.paths(), ","); got != "getMe" {
				t.Errorf("%s %s: paths = %s", tool, name, got)
			}
		}
	}
}

func TestStickerOrderToolsRejectBeforeSecretsAndIO(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "bot")
	noBot := newMediaEnv(t)
	for _, tool := range []string{"setposition", "replace", "delete"} {
		refKey := "file_ref"
		if tool == "replace" {
			refKey = "old_file_ref"
		}
		cases := map[string]func(*mediaEnv) map[string]any{
			"no bot target": func(*mediaEnv) map[string]any { return noBot.orderArgs(tool, "X") },
			"dash name": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["name"] = "a-b"
				return a
			},
			"long name": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["name"] = strings.Repeat("a", 65)
				return a
			},
			"raw file id": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a[refKey] = "AgADraw"
				return a
			},
			"chat ref": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a[refKey] = env.ref(mediaChat, "X")
				return a
			},
			"callback ref": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a[refKey] = signRef(testToken, refCallback, botTarget, "X")
				return a
			},
			"extra argument": func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["limit"] = 1
				return a
			},
		}
		if tool == "setposition" {
			cases["negative position"] = func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["position"] = -1
				return a
			}
			cases["position over limit"] = func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["position"] = 200
				return a
			}
		}
		if tool == "replace" {
			cases["zero user"] = func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["user_id"] = 0
				return a
			}
			cases["chat bound new sticker"] = func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				a["sticker"] = env.inputSticker(mediaChat)
				return a
			}
			cases["bad format"] = func(env *mediaEnv) map[string]any {
				a := env.orderArgs(tool, "X")
				s := env.inputSticker(botTarget)
				s["format"] = "gif"
				a["sticker"] = s
				return a
			}
		}
		for name, arguments := range cases {
			env := e
			if name == "no bot target" {
				env = noBot
			}
			if _, err := env.orderTool(tool, arguments(env)); err == nil {
				t.Errorf("%s %s was accepted", tool, name)
			}
		}
	}
	for _, env := range []*mediaEnv{e, noBot} {
		if env.lookups != 0 || len(env.requests) != 0 {
			t.Errorf("secret lookups = %d, requests = %d, want none", env.lookups, len(env.requests))
		}
	}
	// A reference signed for another token is caught before the first request.
	for _, tool := range []string{"setposition", "replace", "delete"} {
		env := newOrderEnv(t, orderSet, okTrue)
		arguments := env.orderArgs(tool, "FID-A")
		key := "file_ref"
		if tool == "replace" {
			key = "old_file_ref"
		}
		arguments[key] = signRef("999:other_bot-token", refFile, botTarget, "FID-A")
		if _, err := env.orderTool(tool, arguments); err == nil || len(env.requests) != 0 {
			t.Errorf("%s foreign token: err = %v, requests = %v", tool, err, env.paths())
		}
	}
}

func TestStickerSetPositionStaysBelowTheSetSize(t *testing.T) {
	e := newOrderEnv(t, orderSet, okTrue)
	arguments := e.orderArgs("setposition", "FID-A")
	arguments["position"] = 2
	if _, err := e.orderTool("setposition", arguments); err == nil || mutationsOf(e) != 0 {
		t.Errorf("err = %v, paths = %v", err, e.paths())
	}
}

func TestStickerOrderMutationsSendOnceAndReportAnUnclearOutcome(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout":  func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"5xx":      func(*http.Request) (*http.Response, error) { return response(502, `{"ok":false}`), nil },
		"unusable": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{"x":1}}`), nil },
		"false":    func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":false}`), nil },
		"garbage":  func(*http.Request) (*http.Response, error) { return response(200, `<html>`), nil },
	} {
		for _, tool := range []string{"setposition", "replace", "delete"} {
			e := newOrderEnv(t, orderSet, answer)
			_, err := e.orderTool(tool, e.orderArgs(tool, "FID-A"))
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || mutationsOf(e) != 1 {
				t.Errorf("%s %s: err = %v, paths = %v", tool, name, err, e.paths())
			}
		}
	}
}

func TestStickerOrderErrorsNeverCarryTelegramDescriptions(t *testing.T) {
	failing := func(*http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"description":"PROVIDER-CANARY"}`), nil
	}
	for _, stage := range []string{"getMe", "getStickerSet", "getFile", "mutation"} {
		for _, tool := range []string{"setposition", "replace", "delete"} {
			e := newOrderEnv(t, orderSet, failing)
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
			if _, err := e.orderTool(tool, e.orderArgs(tool, id)); err == nil || strings.Contains(err.Error(), "PROVIDER-CANARY") {
				t.Errorf("%s %s: err = %v", tool, stage, err)
			}
		}
	}
}

func TestStickerOrderToolsDeclareRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect capability.Effect
		idem   capability.Idempotency
		guard  bool
	}{
		stickersSetPosition.ID: {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		stickersReplace.ID:     {capability.EffectUpdate, capability.IdempotencyUnknown, false},
		stickersDelete.ID:      {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	}
	for _, d := range []capability.Descriptor{stickersSetPosition, stickersReplace, stickersDelete} {
		w, r := want[d.ID], d.Risk
		if d.Group != groupStickers || r.Effect != w.effect || r.Idempotency != w.idem || !r.OpenWorld ||
			r.Confirmation != capability.ConfirmationRequired || d.RequiresToolAllowList != w.guard {
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
