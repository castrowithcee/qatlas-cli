package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const topicBinding = "-1001/topics/7"

func TestParseTopicTarget(t *testing.T) {
	for _, raw := range []string{topicBinding, "-1001234567890/topics/123456", "42/topics/1"} {
		if kind, value, err := parseTarget(raw); err != nil || kind != "topic" || value != raw {
			t.Errorf("parseTarget(%q) = %q, %q, %v", raw, kind, value, err)
		}
		if err := validateConfiguredTarget(raw); err != nil {
			t.Errorf("validateConfiguredTarget(%q) = %v", raw, err)
		}
	}
	for _, raw := range []string{"-1001/topics/0", "-1001/topics/-7", "-1001/topics/07", "-1001/topics/+7",
		"-1001/topics/", "/topics/7", "@news/topics/7", "+1001/topics/7", "-01001/topics/7", "0/topics/7",
		"-1001/topics/7/8", "-1001/topics/x", "-1001/topics/99999999999999999999", " -1001/topics/7",
		"-1001/topic/7", "-1001/Topics/7"} {
		_, _, err := parseTarget(raw)
		if err == nil {
			t.Errorf("parseTarget(%q) = nil, want error", raw)
			continue
		}
		if strings.Contains(err.Error(), "1001") || strings.Contains(err.Error(), "news") {
			t.Errorf("error quotes the target: %v", err)
		}
	}
	set, err := targetsOf(resolvedWith("-1002", topicBinding))
	if err != nil || len(set.chats) != 1 || len(set.topics) != 1 ||
		set.topics[0] != (topicTarget{chat: "-1001", thread: 7, binding: topicBinding}) {
		t.Fatalf("targetsOf() = %+v, %v", set, err)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	kind, ok := metadata.Target.KindByName("topic")
	if !ok || kind.Parent != "chat" || !kind.Unlisted || !metadata.Target.ValidTarget("topic", topicBinding) ||
		metadata.Target.ValidTarget("chat", topicBinding) || metadata.Target.ValidTarget("topic", "@news/topics/7") {
		t.Errorf("topic kind = %+v, %v", kind, ok)
	}
}

func TestSelectThread(t *testing.T) {
	tests := []struct {
		name      string
		targets   []string
		requested string
		thread    int64
		chat      string
		binding   string
		want      int64
		fail      bool
	}{
		{"topic without thread is set", []string{topicBinding}, "", 0, "-1001", topicBinding, 7, false},
		{"topic with its thread", []string{topicBinding}, "", 7, "-1001", topicBinding, 7, false},
		{"topic with the chat", []string{topicBinding}, "-1001", 0, "-1001", topicBinding, 7, false},
		{"topic with its own spelling", []string{topicBinding}, topicBinding, 0, "-1001", topicBinding, 7, false},
		{"another topic", []string{topicBinding}, "", 8, "", "", 8, true},
		{"spelling and another thread", []string{topicBinding}, topicBinding, 8, "", "", 8, true},
		{"unbound topic spelling", []string{topicBinding}, "-1001/topics/8", 0, "", "", 0, true},
		{"foreign chat", []string{topicBinding}, "-1002", 0, "", "", 0, true},
		{"two topics need the thread", []string{topicBinding, "-1001/topics/8"}, "", 0, "", "", 0, true},
		{"two topics with one of them", []string{topicBinding, "-1001/topics/8"}, "", 8, "-1001", "-1001/topics/8", 8, false},
		{"chat and topic are the whole chat", []string{"-1001", topicBinding}, "", 0, "-1001", "-1001", 0, false},
		{"chat and topic reach any thread", []string{"-1001", topicBinding}, "", 9, "-1001", "-1001", 9, false},
		{"topic spelling with the chat bound", []string{"-1001", topicBinding}, topicBinding, 0, "-1001", "-1001", 7, false},
		{"topic and another chat", []string{"-1002", topicBinding}, "", 0, "", "", 0, true},
		{"topic and another chat by name", []string{"-1002", topicBinding}, "-1001", 0, "-1001", topicBinding, 7, false},
		{"chat only is unchanged", []string{"-1001"}, "", 0, "-1001", "-1001", 0, false},
		{"chat only keeps a thread", []string{"-1001"}, "", 5, "-1001", "-1001", 5, false},
		{"bot only", []string{"bot"}, "", 0, "", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thread := tt.thread
			chat, binding, err := selectThread(resolvedWith(tt.targets...), tt.requested, "chat", &thread, 0)
			if (err != nil) != tt.fail || chat != tt.chat || binding != tt.binding || (!tt.fail && thread != tt.want) {
				t.Fatalf("selectThread() = %q, %q, thread %d, %v", chat, binding, thread, err)
			}
			if err != nil && strings.Contains(err.Error(), "1001") {
				t.Errorf("error names a target: %v", err)
			}
		})
	}
}

func TestChatWideSelectionNeedsTheWholeChat(t *testing.T) {
	tests := []struct {
		name      string
		targets   []string
		requested string
		want      string
		fail      bool
	}{
		{"topic only", []string{topicBinding}, "", "", true},
		{"topic only by chat", []string{topicBinding}, "-1001", "", true},
		{"topic only by spelling", []string{topicBinding}, topicBinding, "", true},
		{"chat and topic", []string{"-1001", topicBinding}, "", "-1001", false},
		{"chat and topic by chat", []string{"-1001", topicBinding}, "-1001", "-1001", false},
		{"chat and topic by spelling", []string{"-1001", topicBinding}, topicBinding, "", true},
		{"other chat and topic", []string{"-1002", topicBinding}, "-1002", "-1002", false},
		{"other chat and topic without chat", []string{"-1002", topicBinding}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectChat(resolvedWith(tt.targets...), tt.requested)
			if (err != nil) != tt.fail || got != tt.want {
				t.Fatalf("selectChat() = %q, %v", got, err)
			}
		})
	}
}

func TestTopicSendSetsTheThreadAndRefusesAnotherBeforeSecretAccess(t *testing.T) {
	var bodies []string
	httpClient := newHTTPClient()
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		return response(http.StatusOK, sendOK), nil
	})
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	send := func(args string, targets ...string) error {
		_, err := invokeMessagesSendWith(context.Background(), resolvedWith(targets...), resolver, nil,
			json.RawMessage(args), httpClient)
		return err
	}
	for _, args := range []string{`{"text":"hi"}`, `{"text":"hi","message_thread_id":7}`,
		`{"chat":"-1001","text":"hi"}`, `{"chat":"` + topicBinding + `","text":"hi"}`} {
		if err := send(args, topicBinding); err != nil {
			t.Fatalf("%s = %v", args, err)
		}
	}
	for _, body := range bodies {
		if body != `{"chat_id":"-1001","text":"hi","message_thread_id":7}` {
			t.Errorf("body = %s", body)
		}
	}
	bodies, resolutions = nil, 0
	for _, c := range []struct {
		args    string
		targets []string
	}{
		{`{"text":"hi","message_thread_id":8}`, []string{topicBinding}},
		{`{"chat":"` + topicBinding + `","text":"hi","message_thread_id":8}`, []string{topicBinding}},
		{`{"text":"hi"}`, []string{topicBinding, "-1001/topics/8"}},
		{`{"chat":"-1002","text":"hi"}`, []string{topicBinding}},
		{`{"text":"hi","reply_to_message_id":3}`, []string{topicBinding}},
		{`{"text":"hi","message_thread_id":7,"reply_to_message_id":3}`, []string{topicBinding}},
		{`{"chat":"-1001","text":"hi","reply_to_message_id":3}`, []string{"-1002", topicBinding}},
	} {
		if err := send(c.args, c.targets...); err == nil {
			t.Errorf("%s with %v was sent", c.args, c.targets)
		}
	}
	if resolutions != 0 || len(bodies) != 0 {
		t.Fatalf("resolutions = %d, requests = %d, want none", resolutions, len(bodies))
	}
	for _, c := range []struct{ args, want string }{
		{`{"text":"hi"}`, `{"chat_id":"-1001","text":"hi"}`},
		{`{"text":"hi","message_thread_id":9}`, `{"chat_id":"-1001","text":"hi","message_thread_id":9}`},
		{`{"text":"hi","reply_to_message_id":3}`, `{"chat_id":"-1001","text":"hi","reply_parameters":{"message_id":3}}`},
	} {
		bodies = nil
		if err := send(c.args, "-1001", topicBinding); err != nil || len(bodies) != 1 || bodies[0] != c.want {
			t.Errorf("chat and topic %s = %v, %v", c.args, bodies, err)
		}
	}
}

func TestTopicToolsKeepToTheBoundTopic(t *testing.T) {
	resolutions := 0
	// An empty token stops every call at the credential, so a call that gets this far passed the selection.
	resolver := secret.NewWith(func(string) string { resolutions++; return "" }, nil, nil, nil)
	invokes := map[string]capability.Handler{}
	for _, op := range operations() {
		invokes[op.Descriptor.ID] = op.Handler
	}
	tests := []struct {
		tool, args string
		targets    []string
		passes     bool
	}{
		{topicsClose.ID, `{"message_thread_id":7}`, []string{topicBinding}, true},
		{topicsReopen.ID, `{"message_thread_id":7}`, []string{topicBinding}, true},
		{topicsEdit.ID, `{"message_thread_id":7,"name":"x"}`, []string{topicBinding}, true},
		{topicsDelete.ID, `{"message_thread_id":7}`, []string{topicBinding}, true},
		{topicsUnpinAll.ID, `{"message_thread_id":7}`, []string{topicBinding}, true},
		{topicsClose.ID, `{"message_thread_id":8}`, []string{topicBinding}, false},
		{topicsDelete.ID, `{"message_thread_id":8}`, []string{topicBinding}, false},
		{topicsClose.ID, `{"general":true}`, []string{topicBinding}, false},
		{topicsUnpinAll.ID, `{"general":true}`, []string{topicBinding}, false},
		{topicsClose.ID, `{"general":true}`, []string{"-1001", topicBinding}, true},
		{topicsCreate.ID, `{"name":"x"}`, []string{topicBinding}, false},
		{topicsHideGeneral.ID, `{}`, []string{topicBinding}, false},
		{pollsSend.ID, `{"question":"q","options":["a","b"]}`, []string{topicBinding}, true},
		{pollsSend.ID, `{"question":"q","options":["a","b"],"message_thread_id":8}`, []string{topicBinding}, false},
		{chatActionsSend.ID, `{"action":"typing"}`, []string{topicBinding}, true},
		{diceSend.ID, `{"message_thread_id":8}`, []string{topicBinding}, false},
		{diceSend.ID, `{"reply_to_message_id":3}`, []string{topicBinding}, false},
		{diceSend.ID, `{"reply_to_message_id":3}`, []string{"-1001", topicBinding}, true},
		{pollsSend.ID, `{"question":"q","options":["a","b"],"reply_to_message_id":3}`, []string{topicBinding}, false},
		{pollsSend.ID, `{"question":"q","options":["a","b"],"reply_to_message_id":3}`, []string{"-1001"}, true},
		{locationsSend.ID, `{"latitude":1,"longitude":2,"reply_to_message_id":3}`, []string{topicBinding}, false},
		{venuesSend.ID, `{"latitude":1,"longitude":2,"title":"t","address":"a","reply_to_message_id":3}`,
			[]string{topicBinding}, false},
		{contactsSend.ID, `{"phone_number":"1","first_name":"a","reply_to_message_id":3}`, []string{topicBinding}, false},
		{contactsSend.ID, `{"phone_number":"1","first_name":"a"}`, []string{topicBinding}, true},
		{gamesSend.ID, `{"game_short_name":"snake","reply_to_message_id":3}`, []string{topicBinding}, false},
		{stickersSend.ID, `{"file_ref":"x","reply_to_message_id":3}`, []string{topicBinding}, false},
		{photosSend.ID, `{"file_ref":"x","reply_to_message_id":3}`, []string{topicBinding}, false},
		{mediaGroupsSend.ID, `{"type":"photo","items":[{"file_ref":"x"},{"file_ref":"y"}],"reply_to_message_id":3}`,
			[]string{topicBinding}, false},
		{richMessagesSend.ID, `{"blocks":[{"type":"paragraph","text":["x"]}],"reply_to_message_id":3}`,
			[]string{topicBinding}, false},
		{richMessagesSend.ID, `{"blocks":[{"type":"paragraph","text":["x"]}]}`, []string{topicBinding}, true},
		{gamesSend.ID, `{"game_short_name":"snake"}`, []string{topicBinding}, true},
		{gamesSend.ID, `{"game_short_name":"snake","message_thread_id":8}`, []string{topicBinding}, false},
		{messagesForward.ID, `{"from_chat":"-1002","message_id":1}`, []string{"-1002", topicBinding}, false},
		{messagesForward.ID, `{"chat":"-1001","from_chat":"-1002","message_id":1}`, []string{"-1002", topicBinding}, true},
		{messagesCopy.ID, `{"chat":"-1001","from_chat":"-1002","message_id":1,"message_thread_id":8}`,
			[]string{"-1002", topicBinding}, false},
		{messagesCopy.ID, `{"chat":"-1002","from_chat":"-1001","message_id":1}`, []string{"-1002", topicBinding}, false},
		{messagesEdit.ID, `{"message_id":1,"text":"x"}`, []string{topicBinding}, false},
		{messagesEdit.ID, `{"message_id":1,"text":"x"}`, []string{"-1001", topicBinding}, true},
		{messagesDelete.ID, `{"message_id":1}`, []string{topicBinding}, false},
		{pinsPin.ID, `{"message_id":1}`, []string{topicBinding}, false},
		{chatsGet.ID, `{}`, []string{topicBinding}, false},
		{membersBan.ID, `{"user_id":1}`, []string{topicBinding}, false},
	}
	for _, tt := range tests {
		resolutions = 0
		_, err := invokes[tt.tool](context.Background(), resolvedWith(tt.targets...), resolver, nil,
			json.RawMessage(tt.args))
		if err == nil {
			t.Fatalf("%s %s reached Telegram", tt.tool, tt.args)
		}
		if passed := resolutions > 0; passed != tt.passes {
			t.Errorf("%s %s with %v: passed the selection = %t (%v), want %t", tt.tool, tt.args, tt.targets,
				passed, err, tt.passes)
		}
		if !tt.passes && strings.Contains(tt.args, "reply_to_message_id") &&
			!strings.Contains(err.Error(), "a reply is not available") {
			t.Errorf("%s %s: refused with %v, want the reply refusal", tt.tool, tt.args, err)
		}
	}
}

func TestTopicReachMatchesWhatTheCallRefuses(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return "" }, nil, nil, nil)
	reachedAlone := 0
	for _, op := range operations() {
		id := op.Descriptor.ID
		reached, hint := metadata.Target.Reach(id, []string{topicBinding})
		for _, targets := range [][]string{{"-1001"}, {"-1001", topicBinding}, {"-1002", topicBinding}, {"bot"}} {
			if ok, _ := metadata.Target.Reach(id, targets); !ok {
				t.Errorf("%s is not reached with %v", id, targets)
			}
		}
		if alone, _ := metadata.Target.Reach(id, []string{"bot", topicBinding}); alone != reached {
			t.Errorf("%s: the bot target changes its reach through a topic", id)
		}
		if reached {
			reachedAlone++
			if topicTools[id] {
				continue
			}
			for _, a := range op.Descriptor.Arguments {
				if a.Target == "chat" {
					t.Errorf("%s has a chat argument but is reached through a topic alone", id)
				}
			}
			continue
		}
		if topicTools[id] || hint != "needs CHAT_ID or @CHANNEL_USERNAME" {
			t.Errorf("%s: reached=%t hint=%q", id, reached, hint)
		}
		for _, example := range op.Descriptor.Examples {
			resolutions = 0
			_, err := op.Handler(context.Background(), resolvedWith(topicBinding), resolver, nil, example.Arguments)
			if err == nil || resolutions != 0 {
				t.Errorf("%s %s: err=%v resolutions=%d", id, example.Arguments, err, resolutions)
				continue
			}
			resolutions = 0
			_, _ = op.Handler(context.Background(), resolvedWith("-1001"), resolver, nil, example.Arguments)
			if resolutions > 0 && !strings.Contains(err.Error(), "bound only through forum topics") {
				t.Errorf("%s %s passes for the chat but is refused for its topic with %v", id, example.Arguments, err)
			}
		}
	}
	// Every tool without a chat (bot, business, stickers, references) and the topic tools.
	if reachedAlone != len(operations())-countChatWide() {
		t.Errorf("reached through a topic alone: %d", reachedAlone)
	}
}

func countChatWide() int {
	n := 0
	for _, op := range operations() {
		chat := op.Descriptor.ID == joinrequestqueriesAnswer.ID
		for _, a := range op.Descriptor.Arguments {
			chat = chat || a.Target == "chat"
		}
		if chat && !topicTools[op.Descriptor.ID] {
			n++
		}
	}
	return n
}

const topicUpdates = `[
 {"update_id":20,"message":{"message_id":1,"date":100,"chat":{"id":-1001,"type":"supergroup"},"message_thread_id":7,"text":"INSIDE","document":{"file_id":"INSIDEFILE","file_size":3}}},
 {"update_id":21,"message":{"message_id":2,"date":101,"chat":{"id":-1001,"type":"supergroup"},"message_thread_id":8,"text":"OTHERTOPIC"}},
 {"update_id":22,"message":{"message_id":3,"date":102,"chat":{"id":-1001,"type":"supergroup"},"text":"GENERALTEXT"}},
 {"update_id":23,"edited_message":{"message_id":1,"date":103,"chat":{"id":-1001,"type":"supergroup"},"message_thread_id":7,"text":"EDITED"}},
 {"update_id":24,"callback_query":{"id":"INSIDECB","from":{"id":8},"data":"x","message":{"message_id":9,"date":0,"message_thread_id":7,"chat":{"id":-1001}}}},
 {"update_id":25,"callback_query":{"id":"OTHERCB","from":{"id":8},"data":"x","message":{"message_id":9,"date":0,"message_thread_id":8,"chat":{"id":-1001}}}},
 {"update_id":26,"message_reaction":{"chat":{"id":-1001},"message_id":1,"date":104,"user":{"id":5}}},
 {"update_id":27,"chat_join_request":{"chat":{"id":-1001},"from":{"id":55},"date":200,"query_id":"JOINQUERYID"}},
 {"update_id":28,"message":{"message_id":4,"date":105,"chat":{"id":-1002,"type":"supergroup"},"message_thread_id":7,"text":"FOREIGNCHAT"}}
]`

func TestUpdatesOfATopicShowOnlyItsMessages(t *testing.T) {
	var calls []recorded
	client, resolved := multiClient(t, testToken, fakeTelegram(&calls, topicUpdates, "0"), topicBinding)
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	updates := got["updates"].([]updateOut)
	if len(updates) != 3 || got["skipped"] != 6 || len(calls) != 1 {
		t.Fatalf("updates = %+v, skipped = %v, calls = %d", updates, got["skipped"], len(calls))
	}
	for _, u := range updates {
		if u.Chat != topicBinding || u.MessageThreadID != 7 && u.Type != "callback_query" {
			t.Errorf("update = %+v", u)
		}
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"OTHERTOPIC", "GENERALTEXT", "OTHERCB", "JOINQUERYID", "FOREIGNCHAT", "INSIDEFILE"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("output contains %s", forbidden)
		}
	}
	file, err := parseRef(resolved, refFile, updates[0].Media.FileRef)
	if err != nil || file.binding != topicBinding {
		t.Fatalf("file reference = %+v, %v", file, err)
	}
	if _, err := parseRef(resolved, refCallback, updates[2].CallbackRef); err != nil {
		t.Errorf("callback reference = %v", err)
	}
	if _, err := parseRef(resolvedWith("-1001"), refFile, updates[0].Media.FileRef); err == nil {
		t.Error("a topic reference was accepted for the whole chat")
	}
	if _, err := parseRef(resolved, refFile, signRef(testToken, refFile, "-1001", "F")); err == nil {
		t.Error("a reference of the whole chat was accepted for a topic")
	}
	// A media reference stays with its topic.
	if _, err := prepareSources(context.Background(), resolved, "send photo", topicBinding, []string{kindPhoto},
		[]mediaSource{{FileRef: updates[0].Media.FileRef}}); err != nil {
		t.Errorf("prepareSources(topic reference) = %v", err)
	}

	calls = nil
	client, _ = multiClient(t, testToken, fakeTelegram(&calls, topicUpdates, "0"), "-1001", topicBinding)
	got, err = client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	updates = got["updates"].([]updateOut)
	if len(updates) != 8 {
		t.Fatalf("chat and topic: updates = %+v", updates)
	}
	for _, u := range updates {
		if u.Chat != "-1001" {
			t.Errorf("chat and topic: update = %+v", u)
		}
	}
}
