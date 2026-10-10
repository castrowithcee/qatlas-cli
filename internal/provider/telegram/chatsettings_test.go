package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestChatSettingsJSONBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	calls := []struct {
		run    func() (map[string]any, error)
		path   string
		body   string
		result string
	}{
		{func() (map[string]any, error) {
			return client.SetChatPermissions(ctx, chatPermissions{CanSendMessages: true})
		},
			"setChatPermissions", `{"chat_id":"-1001","permissions":{"can_send_messages":true,"can_send_audios":false,` +
				`"can_send_documents":false,"can_send_photos":false,"can_send_videos":false,"can_send_video_notes":false,` +
				`"can_send_voice_notes":false,"can_send_polls":false,"can_send_other_messages":false,` +
				`"can_add_web_page_previews":false,"can_react_to_messages":false,"can_edit_tag":false,` +
				`"can_change_info":false,"can_invite_users":false,"can_pin_messages":false,"can_manage_topics":false},` +
				`"use_independent_chat_permissions":true}`, "updated"},
		{func() (map[string]any, error) { return client.SetChatStickerSet(ctx, "team_Set1") },
			"setChatStickerSet", `{"chat_id":"-1001","sticker_set_name":"team_Set1"}`, "updated"},
		{func() (map[string]any, error) { return client.DeleteChatStickerSet(ctx) },
			"deleteChatStickerSet", `{"chat_id":"-1001"}`, "deleted"},
		{func() (map[string]any, error) { return client.LeaveChat(ctx) }, "leaveChat", `{"chat_id":"-1001"}`, "left"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.result] != true || len(got) != 1 {
			t.Fatalf("call %d = %v, %v", i, got, err)
		}
		if (*bodies)[i] != c.body || (*paths)[i] != c.path {
			t.Errorf("call %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], c.path, c.body)
		}
	}
}

func TestChatSettingsRefuseBadInputBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	var cases []struct {
		invoke invokeFunc
		args   string
	}
	add := func(i invokeFunc, a string) {
		cases = append(cases, struct {
			invoke invokeFunc
			args   string
		}{i, a})
	}
	for _, name := range restrictPermissionNames {
		add(invokeChatsSetPermissions, `{"permissions":`+permissionsJSON("true", name)+`}`)
	}
	add(invokeChatsSetPermissions, `{}`)
	add(invokeChatsSetPermissions, `{"permissions":null}`)
	add(invokeChatsSetPermissions, `{"permissions":`+strings.TrimSuffix(permissionsJSON("true", ""), "}")+`,"can_x":true}}`)
	for _, name := range []string{"", strings.Repeat("a", 65), "https://t.me/addstickers/x", "a-b", "ä", "a b"} {
		add(invokeChatsSetStickerSet, `{"sticker_set_name":"`+name+`"}`)
	}
	add(invokeChatsSetStickerSet, `{}`)
	for _, c := range cases {
		if _, err := c.invoke(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(c.args)); err == nil {
			t.Errorf("accepted %.60s", c.args)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	if _, err := client.SetChatStickerSet(context.Background(), strings.Repeat("a", 64)); err != nil {
		t.Errorf("64 characters refused: %v", err)
	}
	if _, err := client.SetChatStickerSet(context.Background(), "a-b"); err == nil || len(*bodies) != 1 {
		t.Errorf("invalid name sent or accepted: %v, %d requests", err, len(*bodies))
	}
}

func TestChatSettingsRejectForeignChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, c := range map[string]struct {
		invoke invokeFunc
		args   string
	}{
		"setpermissions":   {invokeChatsSetPermissions, `{"chat":"-2002","permissions":` + permissionsJSON("false", "") + `}`},
		"setstickerset":    {invokeChatsSetStickerSet, `{"chat":"-2002","sticker_set_name":"x"}`},
		"deletestickerset": {invokeChatsDeleteStickerSet, `{"chat":"-2002"}`},
		"leave":            {invokeChatsLeave, `{"chat":"-2002"}`},
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

func TestChatSettingsSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*Client) error{
		"setpermissions":   func(c *Client) error { _, err := c.SetChatPermissions(ctx, chatPermissions{}); return err },
		"setstickerset":    func(c *Client) error { _, err := c.SetChatStickerSet(ctx, "x"); return err },
		"deletestickerset": func(c *Client) error { _, err := c.DeleteChatStickerSet(ctx); return err },
		"leave":            func(c *Client) error { _, err := c.LeaveChat(ctx); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
	}
}

func TestChatSettingsRisksAndAllowList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, d := range []capability.Descriptor{chatsSetPermissions, chatsSetStickerSet, chatsDeleteStickerSet, chatsLeave} {
		wantDelete := d.ID == chatsDeleteStickerSet.ID || d.ID == chatsLeave.ID
		if d.RequiresToolAllowList != wantDelete || (d.Risk.Effect == capability.EffectDelete) != wantDelete ||
			d.Risk.Idempotency != capability.IdempotencyIdempotent || d.Group != groupChats ||
			d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s descriptor = %+v", d.ID, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			switch tool {
			case chatsSetPermissions.ID, chatsSetStickerSet.ID, chatsDeleteStickerSet.ID, chatsLeave.ID:
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
