package telegram

import (
	"context"
	"encoding/json"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func resolvedWith(targets ...string) *config.Resolved {
	return &config.Resolved{
		Name: "alerts", Provider: Provider, BaseURL: "https://api.telegram.test", Targets: targets,
		Credential: "notifier", Secrets: config.Credential{Type: config.CredentialTypeEnv,
			Values: map[string]string{roleBotToken: "TEST_TELEGRAM_BOT_TOKEN"}},
	}
}

func TestParseTargetKinds(t *testing.T) {
	for raw, kind := range map[string]string{
		"-1001": "chat", "42": "chat", "@alerts_channel": "chat", "bot": "bot", "business/abc-1_=": "business",
	} {
		if got, _, err := parseTarget(raw); err != nil || got != kind {
			t.Errorf("parseTarget(%q) = %q, %v, want %q", raw, got, err, kind)
		}
	}
	for _, raw := range []string{"", " ", "@", "0", "business/", "business/a b", "business", "Bot", "bot/1", "chat name"} {
		if _, _, err := parseTarget(raw); err == nil {
			t.Errorf("parseTarget(%q) = nil, want error", raw)
		}
	}
}

func TestSelectChat(t *testing.T) {
	tests := []struct {
		name      string
		targets   []string
		requested string
		want      string
		fail      bool
	}{
		{"single without chat", []string{"-1001"}, "", "-1001", false},
		{"single with chat", []string{"-1001"}, "-1001", "-1001", false},
		{"several with chat", []string{"-1001", "@news"}, "@news", "@news", false},
		{"several without chat", []string{"-1001", "@news"}, "", "", true},
		{"foreign chat", []string{"-1001", "@news"}, "-1999", "", true},
		{"username is not the numeric ID", []string{"@news"}, "-1001", "", true},
		{"numeric ID is not the username", []string{"-1001"}, "@news", "", true},
		{"username case differs", []string{"@news"}, "@News", "", true},
		{"bot only", []string{"bot"}, "", "", true},
		{"bot is not a chat", []string{"bot", "-1001"}, "bot", "", true},
		{"business only", []string{"business/x1"}, "", "", true},
		{"business with chat", []string{"business/x1", "-1001"}, "", "-1001", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectChat(resolvedWith(tt.targets...), tt.requested)
			if (err != nil) != tt.fail || got != tt.want {
				t.Fatalf("selectChat() = %q, %v", got, err)
			}
			if err != nil && tt.requested != "" && strings.Contains(err.Error(), tt.requested) {
				t.Errorf("error names the requested chat: %v", err)
			}
			if err != nil {
				for _, target := range tt.targets {
					if strings.Contains(err.Error(), target) {
						t.Errorf("error names a bound target: %v", err)
					}
				}
			}
		})
	}
}

func TestGates(t *testing.T) {
	if requireBotScope(resolvedWith("-1001")) == nil || requireBotScope(resolvedWith("-1001", "business/x1")) == nil {
		t.Error("requireBotScope accepted a connection without the bot target")
	}
	if err := requireBotScope(resolvedWith("bot")); err != nil {
		t.Errorf("requireBotScope() = %v", err)
	}
	if err := requireBusiness(resolvedWith("business/x1", "business/x2"), "x2"); err != nil {
		t.Errorf("requireBusiness() = %v", err)
	}
	for _, targets := range [][]string{{"-1001", "bot"}, {"business/x1"}, {"business/x11"}} {
		err := requireBusiness(resolvedWith(targets...), "x2")
		if err == nil {
			t.Errorf("requireBusiness(%v) = nil", targets)
		} else if strings.Contains(err.Error(), "x2") {
			t.Errorf("error names the business ID: %v", err)
		}
	}
}

func TestNumericChatMethodsRefuseUsernames(t *testing.T) {
	for method := range numericChatMethods {
		if requireNumericChat(method, "@news") == nil || requireNumericChat(method, "-1001") != nil {
			t.Errorf("%s numeric-chat gate is wrong", method)
		}
	}
	for _, method := range []string{"sendMessage", "editMessageText", "deleteMessage"} {
		if numericChatMethods[method] || requireNumericChat(method, "@news") != nil {
			t.Errorf("%s accepts @username and must not be listed", method)
		}
	}
}

func TestChatToolsRouteToTheSelectedChatAndRefuseBeforeSecretAccess(t *testing.T) {
	resolutions, requests := 0, 0
	var bodies []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+" "+string(raw))
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			return response(http.StatusOK, `{"ok":true,"result":{"message_id":7,"date":9}}`), nil
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			return response(http.StatusOK, `{"ok":true,"result":{"message_id":7}}`), nil
		}
		return response(http.StatusOK, `{"ok":true,"result":true}`), nil
	})
	httpClient := newHTTPClient()
	httpClient.Transport = transport
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, red)
	resolved := resolvedWith("-1001", "@news", "bot", "business/x1")
	run := func(op, args string) error {
		var err error
		switch op {
		case "send":
			var c *Client
			if c, err = openChatWithHTTP(context.Background(), resolved, resolver, red, args, httpClient); err == nil {
				_, err = c.SendMessage(context.Background(), "hi")
			}
		case "edit":
			var c *Client
			if c, err = openChatWithHTTP(context.Background(), resolved, resolver, red, args, httpClient); err == nil {
				_, err = c.EditMessage(context.Background(), 7, "new")
			}
		case "delete":
			var c *Client
			if c, err = openChatWithHTTP(context.Background(), resolved, resolver, red, args, httpClient); err == nil {
				_, err = c.DeleteMessage(context.Background(), 7)
			}
		}
		return err
	}
	for _, op := range []string{"send", "edit", "delete"} {
		for _, chat := range []string{"", "-9999", "bot", "@News", "business/x1"} {
			resolutions, requests = 0, 0
			if err := run(op, chat); err == nil || resolutions != 0 || requests != 0 {
				t.Errorf("%s chat=%q: err=%v resolutions=%d requests=%d", op, chat, err, resolutions, requests)
			}
		}
	}
	bodies = nil
	for _, op := range []string{"send", "edit", "delete"} {
		if err := run(op, "@news"); err != nil {
			t.Fatalf("%s = %v", op, err)
		}
	}
	want := []string{
		`sendMessage {"chat_id":"@news","text":"hi"}`,
		`editMessageText {"chat_id":"@news","message_id":7,"text":"new"}`,
		`deleteMessage {"chat_id":"@news","message_id":7}`,
	}
	if strings.Join(bodies, "|") != strings.Join(want, "|") {
		t.Errorf("bodies = %q, want %q", bodies, want)
	}
}

func TestInvokeFunctionsRefuseBeforeSecretResolution(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	invokes := map[string]func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		"send": invokeMessagesSend, "edit": invokeMessagesEdit, "delete": invokeMessagesDelete,
		"edit_reply_markup": invokeMessagesEditReplyMarkup, "deletemany": invokeMessagesDeleteMany,
		"pin": invokePinsPin, "unpin": invokePinsUnpin, "unpinall": invokePinsUnpinAll,
		"chats_get": invokeChatsGet, "chats_administrators": invokeChatsAdministrators,
		"chats_membercount": invokeChatsMemberCount, "chats_member": invokeChatsMember,
	}
	for name, invoke := range invokes {
		for _, resolved := range []*config.Resolved{
			resolvedWith("-1001", "-1002"), resolvedWith("bot"), resolvedWith("-1001"),
		} {
			args := `{"message_id":1,"text":"x"`
			if strings.HasPrefix(name, "chats_") {
				args = `{"user_id":1`
			}
			if len(resolved.Targets) == 1 && resolved.Targets[0] == "-1001" {
				args += `,"chat":"-1002"`
			}
			if name == "chats_get" || name == "chats_administrators" || name == "chats_membercount" {
				args = strings.Replace(args, `"user_id":1`, `"chat":"-1001"`, 1)
				if !strings.Contains(args, "-1002") {
					args = `{"chat":"-9999"`
				} else {
					args = `{"chat":"-1002"`
				}
			}
			if _, err := invoke(context.Background(), resolved, resolver, nil, json.RawMessage(args+`}`)); err == nil {
				t.Errorf("%s with %v succeeded", name, resolved.Targets)
			}
		}
	}
	if resolutions != 0 {
		t.Fatalf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestChatArgumentIsDeclared(t *testing.T) {
	for _, d := range []capability.Descriptor{messagesSend, messagesEdit, messagesEditReplyMarkup, messagesDelete,
		messagesDeleteMany, messagesForward, messagesCopy, pinsPin, pinsUnpin, pinsUnpinAll, pollsSend, pollsStop,
		reactionsSet, chatActionsSend, locationsSend, venuesSend, contactsSend, diceSend,
		invitelinksPrimary, invitelinksRevoke, membersBan, membersUnban, membersRestrict, membersPromote,
		membersSetAdminTitle, membersSetTag, chatsSetTitle,
		chatsSetDescription, chatsSetPhoto, chatsDeletePhoto, chatsGet, chatsAdministrators, chatsMemberCount,
		chatsMember, senderchatsBan, senderchatsUnban, reactionsRemove, reactionsRemoveAll, photosSend, documentsSend,
		videosSend, animationsSend, videoNotesSend, audioSend, voiceSend, livePhotosSend, mediaGroupsSend} {
		found := false
		for _, a := range d.Arguments {
			found = found || (a.Name == "chat" && !a.Required)
		}
		if !found || !strings.Contains(string(d.InputSchema), `"chat":{"type":"string"`) {
			t.Errorf("%s does not declare the optional chat argument", d.ID)
		}
	}
}

func TestConnectionTestWorksForABotOnlyConnection(t *testing.T) {
	httpClient := newHTTPClient()
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Errorf("path = %s", r.URL.Path)
		}
		return response(http.StatusOK, `{"ok":true,"result":{"id":1}}`), nil
	})
	resolver := secret.NewWith(func(string) string { return testToken }, nil, nil, nil)
	client, err := openWithHTTP(context.Background(), resolvedWith("bot"), resolver, nil, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.testConnection(context.Background()); got != "ok" {
		t.Errorf("class = %q", got)
	}
	if _, err := client.SendMessage(context.Background(), "x"); err == nil {
		t.Error("a bot-only client sent a message")
	}
}
