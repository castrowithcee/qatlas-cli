package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestSenderChatAndReactionBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	calls := []struct {
		run         func() (map[string]any, error)
		path, body  string
		resultField string
	}{
		{func() (map[string]any, error) { return client.BanSenderChat(ctx, -1002002) },
			"banChatSenderChat", `{"chat_id":"-1001","sender_chat_id":-1002002}`, "banned"},
		{func() (map[string]any, error) { return client.UnbanSenderChat(ctx, -1002002) },
			"unbanChatSenderChat", `{"chat_id":"-1001","sender_chat_id":-1002002}`, "unbanned"},
		{func() (map[string]any, error) { return client.RemoveReaction(ctx, 91, 42, 0) },
			"deleteMessageReaction", `{"chat_id":"-1001","message_id":91,"user_id":42}`, "removed"},
		{func() (map[string]any, error) { return client.RemoveReaction(ctx, 91, 0, -1003003) },
			"deleteMessageReaction", `{"chat_id":"-1001","message_id":91,"actor_chat_id":-1003003}`, "removed"},
		{func() (map[string]any, error) { return client.RemoveAllReactions(ctx, 42, 0) },
			"deleteAllMessageReactions", `{"chat_id":"-1001","user_id":42}`, "removed"},
		{func() (map[string]any, error) { return client.RemoveAllReactions(ctx, 0, -1003003) },
			"deleteAllMessageReactions", `{"chat_id":"-1001","actor_chat_id":-1003003}`, "removed"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.resultField] != true || len(got) != 1 {
			t.Fatalf("call %d = %v, %v", i, got, err)
		}
		if (*paths)[i] != c.path || (*bodies)[i] != c.body {
			t.Errorf("call %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], c.path, c.body)
		}
	}
}

func TestSenderChatAndReactionRejectsForeignChatAndBadActorBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, c := range map[string]struct {
		invoke invokeFunc
		args   string
	}{
		"ban":             {invokeSenderChatsBan, `{"chat":"-2002","sender_chat_id":-5}`},
		"unban":           {invokeSenderChatsUnban, `{"chat":"-2002","sender_chat_id":-5}`},
		"remove":          {invokeReactionsRemove, `{"chat":"-2002","message_id":1,"user_id":42}`},
		"removeall":       {invokeReactionsRemoveAll, `{"chat":"-2002","user_id":42}`},
		"remove no actor": {invokeReactionsRemove, `{"message_id":1}`},
		"remove both":     {invokeReactionsRemove, `{"message_id":1,"user_id":42,"actor_chat_id":-5}`},
		"remove_all none": {invokeReactionsRemoveAll, `{}`},
		"remove_all both": {invokeReactionsRemoveAll, `{"user_id":42,"actor_chat_id":-5}`},
		"ban zero sender": {invokeSenderChatsBan, `{"sender_chat_id":0}`},
	} {
		_, err := c.invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(c.args))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestSenderChatAndReactionSingleRequestAndUncertainty(t *testing.T) {
	ops := map[string]func(*Client) error{
		"ban":       func(c *Client) error { _, err := c.BanSenderChat(context.Background(), -5); return err },
		"unban":     func(c *Client) error { _, err := c.UnbanSenderChat(context.Background(), -5); return err },
		"remove":    func(c *Client) error { _, err := c.RemoveReaction(context.Background(), 1, 42, 0); return err },
		"removeall": func(c *Client) error { _, err := c.RemoveAllReactions(context.Background(), 42, 0); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s status %d requests = %d, want 1", name, status, len(*bodies))
			}
		}
	}
}

func TestSenderChatAndReactionRisksGroupsAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		list   bool
		group  string
	}{
		{senderchatsBan, capability.EffectDelete, true, groupMembers},
		{senderchatsUnban, capability.EffectUpdate, false, groupMembers},
		{reactionsRemove, capability.EffectDelete, true, groupInteractions},
		{reactionsRemoveAll, capability.EffectDelete, true, groupInteractions},
	} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld ||
			c.d.RequiresToolAllowList != c.list || c.d.Group != c.group {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
		metadata, _ := reg.ProviderMetadata(Provider)
		for _, profile := range metadata.Profiles {
			for _, tool := range profile.Tools {
				if tool == c.d.ID {
					t.Errorf("profile %s contains %s", profile.ID, tool)
				}
			}
		}
	}
	if !strings.Contains(reactionsRemoveAll.Description, "10000") {
		t.Error("remove_all descriptor does not name the 10000 limit")
	}
}
