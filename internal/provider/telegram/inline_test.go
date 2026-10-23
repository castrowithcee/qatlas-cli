package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const rawInlineID = "RAWINLINEID7"

func newInlineEnv(t *testing.T, targets ...string) *queryEnv {
	env := newQueryEnv(t)
	env.resolved = resolvedWith(targets...)
	return env
}

func (e *queryEnv) inline(t *testing.T, args map[string]any) (any, error) {
	t.Helper()
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	raw, _ := json.Marshal(args)
	return invokeInlineAnswerWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red, raw, httpClient)
}

func article(id string) map[string]any {
	return map[string]any{"id": id, "title": "T", "message_text": "body"}
}

func TestInlineAnswerSendsTheExactBody(t *testing.T) {
	ref := signRef(testToken, refInlineQuery, botTarget, rawInlineID)
	for name, c := range map[string]struct {
		args map[string]any
		want string
	}{
		"minimal": {map[string]any{"inline_ref": ref, "results": []any{article("a")}},
			`answerInlineQuery {"inline_query_id":"` + rawInlineID + `","results":[{"type":"article","id":"a","title":"T","input_message_content":{"message_text":"body"}}]}`},
		"full": {map[string]any{"inline_ref": ref, "cache_time": 0, "is_personal": true, "results": []any{
			map[string]any{"id": "a", "title": "T", "description": "D", "message_text": "<b>x</b>", "parse_mode": "HTML"}}},
			`answerInlineQuery {"inline_query_id":"` + rawInlineID + `","results":[{"type":"article","id":"a","title":"T","description":"D","input_message_content":{"message_text":"\u003cb\u003ex\u003c/b\u003e","parse_mode":"HTML"}}],"cache_time":0,"is_personal":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := newInlineEnv(t, "-100", "bot")
			got, err := env.inline(t, c.args)
			if err != nil || got.(map[string]any)["answered"] != true {
				t.Fatalf("got %v, %v", got, err)
			}
			if len(env.calls) != 1 || env.calls[0] != c.want {
				t.Fatalf("calls = %q, want %q", env.calls, c.want)
			}
		})
	}
}

func TestInlineAnswerRejectsBeforeSecretAccess(t *testing.T) {
	ref := signRef(testToken, refInlineQuery, botTarget, rawInlineID)
	many := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = article(strings.Repeat("x", 3) + string(rune('a'+i/26)) + string(rune('a'+i%26)))
		}
		return out
	}
	with := func(r map[string]any) map[string]any {
		out := article("a")
		for k, v := range r {
			out[k] = v
		}
		return map[string]any{"inline_ref": ref, "results": []any{out}}
	}
	cases := map[string]map[string]any{
		"raw id":              {"inline_ref": rawInlineID, "results": []any{article("a")}},
		"callback kind":       {"inline_ref": signRef(testToken, refCallback, botTarget, rawInlineID), "results": []any{article("a")}},
		"chat binding":        {"inline_ref": signRef(testToken, refInlineQuery, "-100", rawInlineID), "results": []any{article("a")}},
		"foreign binding":     {"inline_ref": signRef(testToken, refInlineQuery, "-5", rawInlineID), "results": []any{article("a")}},
		"no results":          {"inline_ref": ref, "results": []any{}},
		"51 results":          {"inline_ref": ref, "results": many(51)},
		"id too long":         with(map[string]any{"id": strings.Repeat("a", 65)}),
		"empty id":            with(map[string]any{"id": ""}),
		"duplicate id":        {"inline_ref": ref, "results": []any{article("a"), article("a")}},
		"empty title":         with(map[string]any{"title": ""}),
		"long title":          with(map[string]any{"title": strings.Repeat("a", 257)}),
		"long description":    with(map[string]any{"description": strings.Repeat("a", 257)}),
		"empty text":          with(map[string]any{"message_text": ""}),
		"long text":           with(map[string]any{"message_text": strings.Repeat("a", 4097)}),
		"parse mode":          with(map[string]any{"parse_mode": "Markdown"}),
		"url field":           with(map[string]any{"url": "https://example.test"}),
		"thumbnail field":     with(map[string]any{"thumbnail_url": "https://example.test/a.png"}),
		"keyboard field":      with(map[string]any{"reply_markup": map[string]any{}}),
		"type field":          with(map[string]any{"type": "photo"}),
		"top-level next":      {"inline_ref": ref, "results": []any{article("a")}, "next_offset": "5"},
		"top-level button":    {"inline_ref": ref, "results": []any{article("a")}, "button": map[string]any{}},
		"raw query id field":  {"inline_ref": ref, "results": []any{article("a")}, "inline_query_id": rawInlineID},
		"cache time high":     {"inline_ref": ref, "results": []any{article("a")}, "cache_time": 3601},
		"cache time negative": {"inline_ref": ref, "results": []any{article("a")}, "cache_time": -1},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env := newInlineEnv(t, "-100", "bot")
			if _, err := env.inline(t, args); err == nil {
				t.Fatal("accepted")
			}
			if *env.lookups != 0 || len(env.calls) != 0 {
				t.Fatalf("secret lookups = %d, requests = %q", *env.lookups, env.calls)
			}
		})
	}
}

func TestInlineAnswerNeedsTheBotTargetBeforeSecretAccess(t *testing.T) {
	env := newInlineEnv(t, "-100")
	args := map[string]any{"inline_ref": signRef(testToken, refInlineQuery, botTarget, rawInlineID), "results": []any{article("a")}}
	if _, err := env.inline(t, args); err == nil || *env.lookups != 0 || len(env.calls) != 0 {
		t.Fatalf("err = %v, lookups = %d, calls = %q", err, *env.lookups, env.calls)
	}
}

func TestInlineAnswerRejectsForgedSignatureBeforeRequest(t *testing.T) {
	for _, ref := range []string{
		flipLast(signRef(testToken, refInlineQuery, botTarget, rawInlineID)),
		signRef("999:other-token", refInlineQuery, botTarget, rawInlineID),
	} {
		env := newInlineEnv(t, "-100", "bot")
		if _, err := env.inline(t, map[string]any{"inline_ref": ref, "results": []any{article("a")}}); err == nil || len(env.calls) != 0 {
			t.Fatalf("err = %v, calls = %q", err, env.calls)
		}
	}
}

func TestInlineAnswerSendsOneRequestAndReportsUncertainty(t *testing.T) {
	args := map[string]any{"inline_ref": signRef(testToken, refInlineQuery, botTarget, rawInlineID), "results": []any{article("a")}}
	for name, setup := range map[string]func(*queryEnv){
		"500":     func(e *queryEnv) { e.status = 500 },
		"502":     func(e *queryEnv) { e.status = 502 },
		"timeout": func(e *queryEnv) { e.fail = &timeoutError{} },
	} {
		env := newInlineEnv(t, "bot")
		setup(env)
		_, err := env.inline(t, args)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
			strings.Contains(err.Error(), "SECRETDESC") || strings.Contains(err.Error(), rawInlineID) || len(env.calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(env.calls))
		}
	}
}

const inlineUpdates = `[
 {"update_id":1,"inline_query":{"id":"IQID","from":{"id":5,"is_bot":false,"first_name":"Eve"},"query":"hello","offset":"OFFSET","chat_type":"sender","location":{"latitude":1.5,"longitude":2.5}}},
 {"update_id":2,"chosen_inline_result":{"result_id":"picked","from":{"id":5},"query":"hello","inline_message_id":"INLINEMSGID","location":{"latitude":1.5,"longitude":2.5}}},
 {"update_id":3,"message":{"message_id":1,"date":1,"chat":{"id":-100},"text":"chat"}}
]`

func TestUpdatesListShowsInlineUpdatesOnlyWithTheBotTarget(t *testing.T) {
	var calls []recorded
	client, resolved := multiClient(t, testToken, fakeTelegram(&calls, inlineUpdates, "0"), "-100", "bot")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"IQID", "INLINEMSGID"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 3 || got["skipped"] != 0 {
		t.Fatalf("updates = %d, skipped = %v", len(updates), got["skipped"])
	}
	q, r := updates[0], updates[1]
	if q.Type != "inline_query" || q.Chat != botTarget || q.Query != "hello" || q.From == nil || q.From.ID != 5 || q.InlineRef == "" {
		t.Errorf("inline query = %+v", q)
	}
	ref, err := parseRef(resolved, refInlineQuery, q.InlineRef)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := client.checkRef(ref); err != nil || id != "IQID" || ref.binding != botTarget {
		t.Errorf("inline ref = %q, %v, binding %q", id, err, ref.binding)
	}
	if r.Type != "chosen_inline_result" || r.ResultID != "picked" || r.Query != "hello" || r.InlineRef != "" {
		t.Errorf("chosen = %+v", r)
	}
	if updates[2].Type != "message" || updates[2].Chat != "-100" || updates[2].Text != "chat" {
		t.Errorf("chat update = %+v", updates[2])
	}
}

func TestUpdatesListSkipsInlineUpdatesWithoutTheBotTarget(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, inlineUpdates, "0"), "-100")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 1 || got["skipped"] != 2 || updates[0].Type != "message" {
		t.Fatalf("updates = %+v, skipped = %v", updates, got["skipped"])
	}
}

func TestUpdatesListCapsInlineText(t *testing.T) {
	body := `[{"update_id":1,"inline_query":{"id":"Q","from":{"id":5},"query":"` + strings.Repeat("ä", 300) + `"}},` +
		`{"update_id":2,"chosen_inline_result":{"result_id":"` + strings.Repeat("r", 100) + `","from":{"id":5},"query":"q"}}]`
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, body, "0"), "-100", "bot")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 2 || len([]rune(updates[0].Query)) != 256 || len(updates[1].ResultID) != 64 {
		t.Fatalf("updates = %+v", updates)
	}
}

var _ http.RoundTripper = roundTripFunc(nil)
