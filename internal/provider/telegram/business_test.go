package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const businessConnectionBody = `{"id":"b1","user":{"id":7,"is_bot":false,"first_name":"Ann","last_name":"LASTNAME","username":"ann","language_code":"de"},` +
	`"user_chat_id":777,"date":1700000000,"is_enabled":true,"rights":{"can_reply":true,"can_read_messages":true,"can_future_right":true},"extra":"EXTRAFIELD"}`

type businessEnv struct {
	resolved *config.Resolved
	secrets  *secret.Resolver
	red      *redact.Redactor
	lookups  int
	calls    []string
	status   int
	fail     error
	body     string
}

func newBusinessEnv(targets ...string) *businessEnv {
	e := &businessEnv{status: 200, body: `{"ok":true,"result":true}`, red: &redact.Redactor{}}
	e.resolved = resolvedWith(targets...)
	e.secrets = secret.NewWith(func(name string) string {
		e.lookups++
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return testToken
		}
		return ""
	}, nil, nil, e.red)
	return e
}

func (e *businessEnv) transport() roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		e.calls = append(e.calls, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+" "+string(data))
		if e.fail != nil {
			return nil, e.fail
		}
		if e.status != 200 {
			return response(e.status, `{"ok":false,"description":"SECRETDESC"}`), nil
		}
		return response(200, e.body), nil
	}
}

func (e *businessEnv) run(tool string, args map[string]any) (any, error) {
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	raw, _ := json.Marshal(args)
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "get":
		return invokeBusinessGetWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "markread":
		return invokeBusinessMarkReadWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeBusinessDeleteMessagesWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

func businessRef(binding, id string) string { return signRef(testToken, refBusinessChat, binding, id) }

func TestBusinessToolsSendTheExactBodies(t *testing.T) {
	ref := businessRef("business/b1", "777")
	for name, c := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"mark read": {"markread", map[string]any{"business": "b1", "chat": ref, "message_id": 91},
			`readBusinessMessage {"business_connection_id":"b1","chat_id":777,"message_id":91}`},
		"delete": {"deletemessages", map[string]any{"business": "b1", "message_ids": []int{3, 1, 2}},
			`deleteBusinessMessages {"business_connection_id":"b1","message_ids":[3,1,2]}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := newBusinessEnv("-100", "business/b1")
			if _, err := env.run(c.tool, c.args); err != nil {
				t.Fatal(err)
			}
			if len(env.calls) != 1 || env.calls[0] != c.want {
				t.Fatalf("calls = %q, want %q", env.calls, c.want)
			}
		})
	}
}

func TestBusinessGetOutputIsAnAllowListAndSignsTheChat(t *testing.T) {
	env := newBusinessEnv("business/b1")
	env.body = `{"ok":true,"result":` + businessConnectionBody + `}`
	got, err := env.run("get", map[string]any{"business": "b1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.calls) != 1 || env.calls[0] != `getBusinessConnection {"business_connection_id":"b1"}` {
		t.Fatalf("calls = %q", env.calls)
	}
	encoded, _ := json.Marshal(got)
	text := string(encoded)
	for _, forbidden := range []string{"LASTNAME", "EXTRAFIELD", "language_code", "can_future_right", "777"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, text)
		}
	}
	out := got.(*businessOut)
	if out.ID != "b1" || !out.IsEnabled || out.Date != 1700000000 || out.User == nil || out.User.Username != "ann" ||
		out.Rights == nil || !out.Rights.CanReply || !out.Rights.CanReadMessages || out.Rights.CanManageStories {
		t.Errorf("out = %+v", out)
	}
	parsed, err := parseRef(env.resolved, refBusinessChat, out.BusinessChatRef)
	if err != nil || parsed.binding != "business/b1" || parsed.id != "777" {
		t.Fatalf("ref = %+v, %v", parsed, err)
	}
	// The signed chat is accepted by the mark-read tool of the same connection only.
	env.body = `{"ok":true,"result":true}`
	if _, err := env.run("markread", map[string]any{"business": "b1", "chat": out.BusinessChatRef, "message_id": 5}); err != nil {
		t.Errorf("markread with the issued ref: %v", err)
	}
}

func TestBusinessGetRefusesAnAnswerForAnotherConnection(t *testing.T) {
	env := newBusinessEnv("business/b1")
	env.body = `{"ok":true,"result":` + strings.Replace(businessConnectionBody, `"id":"b1"`, `"id":"other"`, 1) + `}`
	if _, err := env.run("get", map[string]any{"business": "b1"}); err == nil {
		t.Error("an answer for another connection was accepted")
	}
}

func TestBusinessToolsRejectBeforeSecretAccess(t *testing.T) {
	ref := businessRef("business/b1", "777")
	cases := map[string]struct {
		targets []string
		tool    string
		args    map[string]any
	}{}
	add := func(name string, targets []string, tool string, args map[string]any) {
		cases[name] = struct {
			targets []string
			tool    string
			args    map[string]any
		}{targets, tool, args}
	}
	for _, tool := range []string{"get", "markread", "deletemessages"} {
		args := func(extra map[string]any) map[string]any {
			out := map[string]any{"business": "b1"}
			switch tool {
			case "markread":
				out["chat"], out["message_id"] = ref, 1
			case "deletemessages":
				out["message_ids"] = []int{1}
			}
			for k, v := range extra {
				out[k] = v
			}
			return out
		}
		both := []string{"-100", "business/b1", "business/b2"}
		add(tool+" no business target", []string{"-100", "bot"}, tool, args(nil))
		add(tool+" foreign business", both, tool, args(map[string]any{"business": "FOREIGNID"}))
		add(tool+" invalid business", both, tool, args(map[string]any{"business": "a/b"}))
		add(tool+" empty business", both, tool, args(map[string]any{"business": ""}))
		add(tool+" unknown argument", both, tool, args(map[string]any{"text": "x"}))
		add(tool+" passthrough argument", both, tool, args(map[string]any{"chat_id": 5, "business_connection_id": "b2"}))
	}
	chatRef := func(binding string) map[string]any {
		return map[string]any{"business": "b1", "message_id": 1, "chat": businessRef(binding, "777")}
	}
	both := []string{"-100", "business/b1", "business/b2"}
	add("raw chat id", both, "markread", map[string]any{"business": "b1", "message_id": 1, "chat": "777"})
	add("numeric chat", both, "markread", map[string]any{"business": "b1", "message_id": 1, "chat": 777})
	add("other business ref", both, "markread", chatRef("business/b2"))
	add("chat bound ref", both, "markread", chatRef("-100"))
	add("bot bound ref", append([]string{"bot"}, both...), "markread", chatRef("bot"))
	add("unbound business ref", both, "markread", chatRef("business/b3"))
	add("file kind ref", both, "markread", map[string]any{"business": "b1", "message_id": 1, "chat": signRef(testToken, refFile, "business/b1", "777")})
	add("callback kind ref", both, "markread", map[string]any{"business": "b1", "message_id": 1, "chat": signRef(testToken, refCallback, "business/b1", "777")})
	add("message id zero", both, "markread", map[string]any{"business": "b1", "message_id": 0, "chat": ref})
	add("message id negative", both, "markread", map[string]any{"business": "b1", "message_id": -4, "chat": ref})
	add("missing chat", both, "markread", map[string]any{"business": "b1", "message_id": 1})
	many := make([]int, 101)
	for i := range many {
		many[i] = i + 1
	}
	for name, ids := range map[string][]int{"zero": {0}, "101": many, "duplicate": {4, 5, 4}, "negative": {-1}, "empty": {}} {
		add("ids "+name, both, "deletemessages", map[string]any{"business": "b1", "message_ids": ids})
	}
	add("ids missing", both, "deletemessages", map[string]any{"business": "b1"})
	add("ids string", both, "deletemessages", map[string]any{"business": "b1", "message_ids": []string{"1"}})
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newBusinessEnv(c.targets...)
			_, err := env.run(c.tool, c.args)
			if err == nil {
				t.Fatal("accepted")
			}
			if env.lookups != 0 || len(env.calls) != 0 {
				t.Fatalf("secret lookups = %d, requests = %q", env.lookups, env.calls)
			}
			for _, leak := range []string{"FOREIGNID", "b2", "b3", testToken} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error leaks %q: %v", leak, err)
				}
			}
		})
	}
}

func TestBusinessMarkReadRefusesForgedReferencesBeforeRequest(t *testing.T) {
	for name, ref := range map[string]string{
		"flipped tag": flipLast(businessRef("business/b1", "777")),
		"other token": signRef("999:other-token", refBusinessChat, "business/b1", "777"),
	} {
		env := newBusinessEnv("business/b1")
		if _, err := env.run("markread", map[string]any{"business": "b1", "chat": ref, "message_id": 1}); err == nil {
			t.Errorf("%s accepted", name)
		}
		if len(env.calls) != 0 {
			t.Errorf("%s sent %q", name, env.calls)
		}
	}
	// A reference to a non-numeric chat identifier never becomes a request.
	env := newBusinessEnv("business/b1")
	if _, err := env.run("markread", map[string]any{"business": "b1", "chat": businessRef("business/b1", "@name"), "message_id": 1}); err == nil || len(env.calls) != 0 {
		t.Errorf("non-numeric chat err = %v, calls = %q", err, env.calls)
	}
}

func TestBusinessMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	ref := businessRef("business/b1", "777")
	for tool, args := range map[string]map[string]any{
		"markread":       {"business": "b1", "chat": ref, "message_id": 1},
		"deletemessages": {"business": "b1", "message_ids": []int{1, 2}},
	} {
		for name, setup := range map[string]func(*businessEnv){
			"500":     func(e *businessEnv) { e.status = 500 },
			"502":     func(e *businessEnv) { e.status = 502 },
			"timeout": func(e *businessEnv) { e.fail = &timeoutError{} },
			"false":   func(e *businessEnv) { e.body = `{"ok":true,"result":false}` },
			"object":  func(e *businessEnv) { e.body = `{"ok":true,"result":{"x":1}}` },
		} {
			env := newBusinessEnv("business/b1")
			setup(env)
			_, err := env.run(tool, args)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), "SECRETDESC") || len(env.calls) != 1 {
				t.Errorf("%s %s: err = %v, calls = %d", tool, name, err, len(env.calls))
			}
		}
	}
}

func TestBusinessDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
		list        bool
	}{
		{businessGet, capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone, false},
		{businessMarkRead, capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired, false},
		{businessDeleteMessages, capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired, true},
	} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != c.idempotency || r.Confirmation != c.confirm || !r.OpenWorld ||
			r.DataSensitivity != "telegram-business-content" || c.d.RequiresToolAllowList != c.list || c.d.Group != groupBusiness {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
		if _, _, ok := reg.Lookup(c.d.ID); !ok {
			t.Errorf("%s is not registered", c.d.ID)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.business.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func businessUpdates(extra string) string {
	return `[` + extra + `]`
}

func TestUpdatesListFiltersBusinessUpdatesByBinding(t *testing.T) {
	const updates = `
 {"update_id":1,"business_message":{"message_id":5,"business_connection_id":"b1","date":100,"chat":{"id":777,"type":"private"},"from":{"id":7,"first_name":"Ann"},"text":"hello","photo":[{"file_id":"BUSINESSFILEID","file_size":9}]}},
 {"update_id":2,"business_message":{"message_id":6,"business_connection_id":"FOREIGNCONN","date":100,"chat":{"id":888},"text":"FOREIGNTEXT"}},
 {"update_id":3,"edited_business_message":{"message_id":5,"business_connection_id":"b1","date":101,"chat":{"id":777},"text":"edited"}},
 {"update_id":4,"deleted_business_messages":{"business_connection_id":"b1","chat":{"id":777},"message_ids":[5,6]}},
 {"update_id":5,"deleted_business_messages":{"business_connection_id":"FOREIGNCONN","chat":{"id":888},"message_ids":[9]}},
 {"update_id":6,"business_connection":{"id":"b1","user":{"id":7,"first_name":"Ann"},"user_chat_id":777,"date":50,"is_enabled":true,"rights":{"can_reply":true}}},
 {"update_id":7,"business_connection":{"id":"FOREIGNCONN","user":{"id":9},"user_chat_id":888,"date":50,"is_enabled":true}},
 {"update_id":8,"business_message":{"message_id":7,"date":100,"chat":{"id":-100},"text":"NOCONNECTION"}},
 {"update_id":9,"message":{"message_id":1,"date":100,"chat":{"id":-100},"text":"plain"}}`
	for name, targets := range map[string][]string{
		"business and chat": {"-100", "business/b1", "business/b2"},
		"business only":     {"business/b1"},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []recorded
			client, resolved := multiClient(t, testToken, fakeTelegram(&calls, businessUpdates(updates), "0"), targets...)
			got, err := client.ListUpdates(context.Background(), 20, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0].method != "getUpdates" {
				t.Fatalf("calls = %+v", calls)
			}
			encoded, _ := json.Marshal(got)
			text := string(encoded)
			for _, forbidden := range []string{"FOREIGNCONN", "FOREIGNTEXT", "NOCONNECTION", "BUSINESSFILEID", `"888`, "file_ref"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("output contains %q: %s", forbidden, text)
				}
			}
			out := got["updates"].([]updateOut)
			wantTypes := []string{"business_message", "edited_business_message", "deleted_business_messages", "business_connection"}
			wantSkipped := 5
			if name == "business and chat" {
				wantTypes = append(wantTypes, "message")
				wantSkipped = 4
			}
			if got["skipped"] != wantSkipped || got["last_update_id"] != int64(9) || len(out) != len(wantTypes) {
				t.Fatalf("updates = %d, skipped = %v: %s", len(out), got["skipped"], text)
			}
			for i, want := range wantTypes {
				if out[i].Type != want {
					t.Errorf("update %d type = %s, want %s", i, out[i].Type, want)
				}
			}
			first := out[0]
			if first.Chat != "business/b1" || first.Text != "hello" || first.MessageID != 5 || first.From == nil ||
				first.Media == nil || first.Media.Kind != "photo" || first.Media.FileRef != "" {
				t.Errorf("first = %+v", first)
			}
			if out[2].Chat != "business/b1" || len(out[2].MessageIDs) != 2 || out[3].IsEnabled == nil || !*out[3].IsEnabled {
				t.Errorf("deleted/connection = %+v, %+v", out[2], out[3])
			}
			for i := 0; i < 4; i++ {
				p, err := parseRef(resolved, refBusinessChat, out[i].BusinessChatRef)
				if err != nil || p.binding != "business/b1" || p.id != strconv.Itoa(777) {
					t.Fatalf("update %d ref = %+v, %v", i, p, err)
				}
				if id, err := client.checkRef(p); err != nil || id != "777" {
					t.Errorf("update %d checkRef = %q, %v", i, id, err)
				}
			}
		})
	}
}

func TestUpdatesListNeedsAChatOrBusinessTarget(t *testing.T) {
	resolver := secret.NewWith(func(string) string { t.Error("credential read"); return "" }, nil, nil, &redact.Redactor{})
	for _, targets := range [][]string{{"bot"}} {
		if _, err := invokeUpdatesList(context.Background(), resolvedWith(targets...), resolver, &redact.Redactor{}, json.RawMessage(`{}`)); err == nil {
			t.Errorf("targets %v accepted", targets)
		}
	}
	var calls []recorded
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, mixedUpdates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil || len(got["updates"].([]updateOut)) != 0 || got["skipped"] != 8 {
		t.Errorf("business-only connection with chat updates = %v, %v", got, err)
	}
}

func TestReferenceKindValuesAreStable(t *testing.T) {
	for kind, want := range map[refKind]byte{refFile: 1, refCallback: 2, refRetired: 3, refInlineQuery: 4,
		refJoinRequestQuery: 5, refBusinessChat: 6} {
		if byte(kind) != want {
			t.Errorf("reference kind %d changed its value to %d", want, kind)
		}
	}
}
