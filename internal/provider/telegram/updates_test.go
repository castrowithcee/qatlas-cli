package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func multiClient(t *testing.T, token string, transport http.RoundTripper, targets ...string) (*Client, *config.Resolved) {
	t.Helper()
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(name string) string {
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return token
		}
		return ""
	}, nil, nil, red)
	httpClient := newHTTPClient()
	httpClient.Transport = transport
	resolved := resolvedWith(targets...)
	client, err := openWithHTTP(context.Background(), resolved, resolver, red, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client, resolved
}

type recorded struct {
	method string
	body   string
}

func fakeTelegram(calls *[]recorded, updates string, chatID string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		*calls = append(*calls, recorded{method, string(data)})
		switch method {
		case "getUpdates":
			return response(200, `{"ok":true,"result":`+updates+`}`), nil
		case "getChat":
			return response(200, `{"ok":true,"result":{"id":`+chatID+`,"type":"channel","title":"SECRET TITLE"}}`), nil
		}
		return response(404, `{"ok":false}`), nil
	}
}

const mixedUpdates = `[
 {"update_id":10,"message":{"message_id":1,"date":100,"chat":{"id":-100,"type":"group"},"from":{"id":7,"is_bot":false,"first_name":"Ann","username":"ann"},"text":"hello","photo":[{"file_id":"SMALLFILEID","file_size":10},{"file_id":"BIGFILEID","file_size":99}],"caption":"cap","message_thread_id":5,"reply_to_message":{"message_id":3}}},
 {"update_id":11,"message":{"message_id":2,"date":101,"chat":{"id":-999,"type":"group"},"text":"FOREIGNTEXT"}},
 {"update_id":12,"callback_query":{"id":"CBQUERYID","from":{"id":8,"is_bot":false,"first_name":"Bob"},"data":"approve:1","message":{"message_id":9,"date":0,"chat":{"id":-100}}}},
 {"update_id":13,"callback_query":{"id":"FOREIGNCB","from":{"id":8},"data":"x","message":{"message_id":9,"chat":{"id":-999}}}},
 {"update_id":14,"inline_query":{"id":"IQ","from":{"id":1},"query":"FOREIGNQUERY"}},
 {"update_id":15,"chat_join_request":{"chat":{"id":-100},"from":{"id":55,"is_bot":false,"first_name":"Joe"},"user_chat_id":55,"date":200,"bio":"BIO"}},
 {"update_id":16,"document_ignored":true,"edited_channel_post":{"message_id":4,"date":300,"chat":{"id":-100},"document":{"file_id":"DOCFILEID","file_size":5,"mime_type":"text/plain","file_name":"a.txt"}}},
 {"update_id":17,"callback_query":{"id":"INLINECB","from":{"id":8},"data":"x"}}
]`

func TestUpdatesListFiltersBoundChatsAndSignsReferences(t *testing.T) {
	var calls []recorded
	client, resolved := multiClient(t, testToken, fakeTelegram(&calls, mixedUpdates, "0"), "-100")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getUpdates" || calls[0].body != `{"limit":20,"timeout":0}` {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	text := string(encoded)
	for _, forbidden := range []string{"FOREIGNTEXT", "FOREIGNCB", "FOREIGNQUERY", "SMALLFILEID", "BIGFILEID", "DOCFILEID",
		"CBQUERYID", "BIO", "-999", "INLINECB"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, text)
		}
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 4 || got["skipped"] != 4 || got["last_update_id"] != int64(17) {
		t.Fatalf("updates = %d, skipped = %v, last = %v: %s", len(updates), got["skipped"], got["last_update_id"], text)
	}
	first := updates[0]
	if first.Type != "message" || first.Chat != "-100" || first.MessageThreadID != 5 || first.ReplyToMessageID != 3 ||
		first.From == nil || first.From.Username != "ann" || first.Text != "hello" || first.Caption != "cap" ||
		first.Media == nil || first.Media.Kind != "photo" || first.Media.Size != 99 {
		t.Errorf("first = %+v", first)
	}
	// Round trip of the three reference kinds through both check stages.
	ref, err := parseRef(resolved, refFile, first.Media.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := client.checkRef(ref); err != nil || id != "BIGFILEID" || ref.binding != "-100" {
		t.Errorf("file ref = %q, %v, binding %q", id, err, ref.binding)
	}
	ref, err = parseRef(resolved, refCallback, updates[1].CallbackRef)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := client.checkRef(ref); err != nil || id != "CBQUERYID" || updates[1].CallbackData != "approve:1" {
		t.Errorf("callback ref = %q, %v", id, err)
	}
	ref, err = parseRef(resolved, refJoinRequest, updates[2].JoinRequestRef)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := client.checkRef(ref); err != nil || id != "55" {
		t.Errorf("join ref = %q, %v", id, err)
	}
	if updates[3].Type != "edited_channel_post" || updates[3].Media.Kind != "document" ||
		updates[3].Media.MimeType != "text/plain" || updates[3].Media.FileName != "a.txt" {
		t.Errorf("fourth = %+v", updates[3])
	}
}

func TestUpdatesListResolvesUsernameTargets(t *testing.T) {
	var calls []recorded
	body := `[{"update_id":1,"channel_post":{"message_id":5,"date":1,"chat":{"id":-1005},"text":"news"}},
	          {"update_id":2,"channel_post":{"message_id":6,"date":1,"chat":{"id":-1006},"text":"OTHER"}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, body, "-1005"), "@news_channel", "-100")
	got, err := client.ListUpdates(context.Background(), 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].body != `{"limit":5,"timeout":3}` || calls[1].method != "getChat" ||
		calls[1].body != `{"chat_id":"@news_channel"}` {
		t.Fatalf("calls = %+v", calls)
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 1 || updates[0].Chat != "@news_channel" || got["skipped"] != 1 {
		t.Fatalf("got = %+v", got)
	}
	if text, _ := json.Marshal(got); strings.Contains(string(text), "OTHER") || strings.Contains(string(text), "SECRET TITLE") {
		t.Errorf("foreign content in output: %s", text)
	}
}

func TestUpdatesListSkipsGetChatWhenNothingNeedsIt(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, `[]`, "1"), "@news_channel")
	got, err := client.ListUpdates(context.Background(), 1, 0)
	if err != nil || len(calls) != 1 || got["skipped"] != 0 || len(got["updates"].([]updateOut)) != 0 {
		t.Fatalf("got = %+v, %v, calls = %+v", got, err, calls)
	}
	if _, ok := got["last_update_id"]; ok {
		t.Error("last_update_id set without updates")
	}
}

func TestUpdatesListErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		class  provider.Class
	}{
		"conflict":  {409, `{"ok":false,"description":"Conflict: webhook SECRETDESC"}`, provider.ClassProviderError},
		"oversized": {200, `{"ok":true,"result":[` + strings.Repeat(" ", updatesResponseBytes) + `]}`, provider.ClassInvalidResponse},
		"bad id":    {200, `{"ok":true,"result":[{"update_id":0}]}`, provider.ClassInvalidResponse},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			client, _ := multiClient(t, testToken, roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(tc.status, tc.body), nil
			}), "-100")
			_, err := client.ListUpdates(context.Background(), 20, 0)
			var perr *provider.Error
			if !asProviderError(err, &perr) || perr.Class != tc.class || strings.Contains(err.Error(), "SECRETDESC") || calls != 1 {
				t.Fatalf("err = %v, calls = %d", err, calls)
			}
			if name == "conflict" && !strings.Contains(err.Error(), "conflict") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func asProviderError(err error, target **provider.Error) bool {
	e, ok := err.(*provider.Error)
	*target = e
	return ok
}

func TestUpdatesListArgumentsAndScope(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("request sent")
		return nil, io.EOF
	})
	client, _ := multiClient(t, testToken, transport, "-100")
	for _, args := range [][2]int{{0, 0}, {101, 0}, {20, -1}, {20, 11}} {
		if _, err := client.ListUpdates(context.Background(), args[0], args[1]); err == nil {
			t.Errorf("ListUpdates(%v) = nil error", args)
		}
	}
	if int(defaultTimeout.Seconds()) <= maxWaitSeconds {
		t.Error("the HTTP timeout does not exceed the longest long poll")
	}
	// No chat target: refused before the credential is read.
	resolver := secret.NewWith(func(string) string { t.Error("credential read"); return "" }, nil, nil, &redact.Redactor{})
	for _, targets := range [][]string{{"bot"}, {"business/x"}} {
		_, err := invokeUpdatesList(context.Background(), resolvedWith(targets...), resolver, &redact.Redactor{}, json.RawMessage(`{}`))
		if err == nil {
			t.Errorf("targets %v accepted", targets)
		}
	}
	for _, bad := range []string{`{"limit":0}`, `{"limit":101}`, `{"wait_seconds":11}`} {
		if _, err := invokeUpdatesList(context.Background(), resolvedWith("-100"), resolver, &redact.Redactor{}, json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestReferenceChecks(t *testing.T) {
	resolved := resolvedWith("-100", "@chan", "bot", "business/b1")
	client, _ := multiClient(t, testToken, nil, "-100", "@chan", "bot", "business/b1")
	other, _ := multiClient(t, "999:other-token", nil, "-100", "@chan", "bot", "business/b1")
	for _, binding := range []string{"-100", "@chan", "bot", "business/b1"} {
		ref := signRef(testToken, refFile, binding, "ID")
		p, err := parseRef(resolved, refFile, ref)
		if err != nil {
			t.Fatalf("%s: %v", binding, err)
		}
		if id, err := client.checkRef(p); err != nil || id != "ID" {
			t.Errorf("%s: %q, %v", binding, id, err)
		}
		if _, err := other.checkRef(p); err == nil {
			t.Errorf("%s: another token accepted the reference", binding)
		}
	}
	good := signRef(testToken, refFile, "-100", "ID")
	// Stage one: wrong kind, foreign binding, malformed. None of these touches a secret.
	for name, raw := range map[string]struct {
		kind refKind
		ref  string
	}{
		"kind":    {refCallback, good},
		"foreign": {refFile, signRef(testToken, refFile, "-5", "ID")},
		"empty":   {refFile, ""},
		"garbage": {refFile, "!!!"},
		"short":   {refFile, good[:10]},
		"long":    {refFile, good + "AAAA"},
		"unknown": {refFile, signRef(testToken, refKind(9), "-100", "ID")},
	} {
		if _, err := parseRef(resolved, raw.kind, raw.ref); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := parseRef(resolvedWith("@other"), refFile, good); err == nil {
		t.Error("a connection without the binding accepted the reference")
	}
	// Stage two: a manipulated tag, a moved field boundary, and a changed identifier.
	for name, tampered := range map[string]string{
		"tag":      flipLast(good),
		"id":       signRef(testToken, refFile, "-100", "ID2")[:len(good)-1] + good[len(good)-1:],
		"boundary": signRef(testToken, refFile, "-10", "0ID"),
	} {
		p, err := parseRef(resolved, refFile, tampered)
		if err != nil {
			continue // rejected by stage one
		}
		if _, err := client.checkRef(p); err == nil && tampered != good {
			t.Errorf("%s: tampered reference accepted", name)
		}
	}
	// The same bytes under another kind or binding never share a tag.
	if signRef(testToken, refFile, "-100", "ID") == signRef(testToken, refCallback, "-100", "ID") {
		t.Error("kinds share a reference")
	}
}

func flipLast(s string) string {
	b := []byte(s)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}
