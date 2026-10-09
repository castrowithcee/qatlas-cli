package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	goodLink  = "https://t.me/+AbCdEfGh1234"
	oldLink   = "https://t.me/joinchat/AbCdEfGh1234"
	linkReply = `{"ok":true,"result":{"invite_link":"https://t.me/+AbCdEfGh1234","creator":{"id":1}}}`
)

type invokeFunc = func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)

func TestPrimaryInviteLinkReadsOnlyTheLink(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200,
		`{"ok":true,"result":{"id":-1001,"type":"supergroup","title":"T","invite_link":"`+goodLink+`"}}`)
	got, err := client.GetPrimaryInviteLink(context.Background())
	if err != nil || got.InviteLink != goodLink {
		t.Fatalf("got = %+v, %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	if string(encoded) != `{"invite_link":"`+goodLink+`"}` {
		t.Errorf("output = %s", encoded)
	}
	if (*paths)[0] != "getChat" || (*bodies)[0] != `{"chat_id":"-1001"}` || len(*bodies) != 1 {
		t.Errorf("request = %v %v", *paths, *bodies)
	}
}

func TestPrimaryInviteLinkHandlesMissingAndForeignValues(t *testing.T) {
	for _, result := range []string{`{"id":-1001}`, `{"id":-1001,"invite_link":""}`,
		`{"id":-1001,"invite_link":"https://evil.example/+AbCdEfGh1234"}`} {
		client, _, _ := capture(t, "-1001", 200, `{"ok":true,"result":`+result+`}`)
		got, err := client.GetPrimaryInviteLink(context.Background())
		encoded, _ := json.Marshal(got)
		if err != nil || string(encoded) != `{}` {
			t.Errorf("%s -> %s, %v", result, encoded, err)
		}
	}
	client, _, _ := capture(t, "-1001", 200, `{"ok":true,"result":"x"}`)
	if _, err := client.GetPrimaryInviteLink(context.Background()); err == nil {
		t.Error("accepted an unreadable result")
	}
}

func TestChatsGetStillHidesTheInviteLink(t *testing.T) {
	client, _, _ := capture(t, "-1001", 200,
		`{"ok":true,"result":{"id":-1001,"type":"supergroup","invite_link":"`+goodLink+`"}}`)
	got, err := client.GetChat(context.Background())
	encoded, _ := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "t.me") {
		t.Errorf("chats.get = %s, %v", encoded, err)
	}
}

func TestRevokeInviteLinkBodyAndResult(t *testing.T) {
	for _, link := range []string{goodLink, oldLink} {
		client, bodies, paths := capture(t, "-1001", 200, linkReply)
		got, err := client.RevokeInviteLink(context.Background(), link)
		encoded, _ := json.Marshal(got)
		if err != nil || string(encoded) != `{"revoked":true}` {
			t.Fatalf("got = %s, %v", encoded, err)
		}
		if (*paths)[0] != "revokeChatInviteLink" || len(*bodies) != 1 ||
			(*bodies)[0] != `{"chat_id":"-1001","invite_link":"`+link+`"}` {
			t.Errorf("request = %v %v", *paths, *bodies)
		}
	}
}

func TestRevokeRejectsBadLinksBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	bad := []string{"", "t.me/+AbCdEfGh1234", "http://t.me/+AbCdEfGh1234", "https://telegram.me/+AbCdEfGh1234",
		"https://t.me.evil.example/+AbCdEfGh1234", "https://evil.example/+AbCdEfGh1234",
		"https://user" + "@t.me/+AbCdEfGh1234", "https://t.me:443/+AbCdEfGh1234", "https://T.me/+AbCdEfGh1234",
		"https://t.me/+AbCdEfGh1234?x=1", "https://t.me/+AbCdEfGh1234#f", "https://t.me/+AbCdEfGh1234/",
		"https://t.me/+AbCd", "https://t.me/+AbCdEfGh1234%2F", "https://t.me/+AbCdEfGh 1234",
		"https://t.me/username", "https://t.me/c/123/4", "https://t.me/AbCdEfGh1234", "https://t.me/joinchat/",
		"https://t.me/joinchat/AbCd/../x", "https://t.me/+AbCdEfGh1234\n", "https://t.me/+" + strings.Repeat("a", 65)}
	client, bodies, _ := capture(t, "-1001", 200, linkReply)
	for _, link := range bad {
		if _, err := client.RevokeInviteLink(context.Background(), link); err == nil {
			t.Errorf("client accepted %q", link)
		}
		arguments, _ := json.Marshal(map[string]string{"invite_link": link})
		_, err := invokeInviteLinksRevoke(context.Background(), resolvedWith("-1001"), resolver, nil, arguments)
		if err == nil || (len(link) > 24 && strings.Contains(err.Error(), link)) {
			t.Errorf("invoke %q err = %v", link, err)
		}
	}
	if _, err := invokeInviteLinksRevoke(context.Background(), resolvedWith("-1001"), resolver, nil,
		json.RawMessage(`{"invite_link":"`+goodLink+`","extra":1}`)); err == nil {
		t.Error("accepted an unknown argument")
	}
	if len(*bodies) != 0 || resolutions != 0 {
		t.Errorf("requests = %d, resolutions = %d, want 0", len(*bodies), resolutions)
	}
}

func TestInviteLinksRejectUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, invoke := range map[string]invokeFunc{"primary": invokeInviteLinksPrimary, "revoke": invokeInviteLinksRevoke} {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(`{"chat":"-2002","invite_link":"`+goodLink+`"}`))
		if name == "primary" {
			_, err = invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(`{"chat":"-2002"}`))
		}
		if err == nil || strings.Contains(err.Error(), "-2002") || strings.Contains(err.Error(), "t.me") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestRevokeSingleRequestAndNoLinkInErrors(t *testing.T) {
	for _, status := range []int{400, 403, 500, 502} {
		client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"`+goodLink+` secret text"}`)
		_, err := client.RevokeInviteLink(context.Background(), goodLink)
		if err == nil || strings.Contains(err.Error(), "t.me") || strings.Contains(err.Error(), "secret text") {
			t.Errorf("status %d err = %v", status, err)
		}
		if status >= 500 && !strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("status %d lacks the uncertainty note: %v", status, err)
		}
		if len(*bodies) != 1 {
			t.Errorf("status %d requests = %d, want 1", status, len(*bodies))
		}
	}
	calls := 0
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &timeoutError{}
	}))
	_, err := client.RevokeInviteLink(context.Background(), goodLink)
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
		strings.Contains(err.Error(), "t.me") {
		t.Errorf("timeout err = %v calls = %d", err, calls)
	}
}

func TestPrimaryErrorsCarryNoLinkAndRevokeNeedsToolList(t *testing.T) {
	client, _, _ := capture(t, "-1001", 500, `{"ok":false,"description":"`+goodLink+`"}`)
	if _, err := client.GetPrimaryInviteLink(context.Background()); err == nil || strings.Contains(err.Error(), "t.me") {
		t.Errorf("err = %v", err)
	}
	if !invitelinksRevoke.RequiresToolAllowList || invitelinksPrimary.RequiresToolAllowList {
		t.Error("only revoke requires a tools list")
	}
	if invitelinksRevoke.Risk.Confirmation != "required" || invitelinksRevoke.Risk.DataSensitivity != inviteLinkSensitivity ||
		invitelinksPrimary.Risk.DataSensitivity != inviteLinkSensitivity {
		t.Errorf("risks = %+v %+v", invitelinksRevoke.Risk, invitelinksPrimary.Risk)
	}
}
