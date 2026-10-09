package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type memberInvoke func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)

var memberInvokes = map[string]memberInvoke{
	"ban": invokeMembersBan, "unban": invokeMembersUnban, "restrict": invokeMembersRestrict,
}

func permissionsJSON(value string, skip string) string {
	var parts []string
	for _, name := range restrictPermissionNames {
		if name != skip {
			parts = append(parts, `"`+name+`":`+value)
		}
	}
	return `{` + strings.Join(parts, ",") + `}`
}

func TestMemberBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	perms := chatPermissions{CanSendMessages: true, CanInviteUsers: true}
	calls := []struct {
		run    func() (map[string]any, error)
		path   string
		body   string
		result string
	}{
		{func() (map[string]any, error) { return client.BanMember(ctx, 42, 0, false) },
			"banChatMember", `{"chat_id":"-1001","user_id":42}`, "banned"},
		{func() (map[string]any, error) { return client.BanMember(ctx, 42, 1900000000, true) },
			"banChatMember", `{"chat_id":"-1001","user_id":42,"until_date":1900000000,"revoke_messages":true}`, "banned"},
		{func() (map[string]any, error) { return client.UnbanMember(ctx, 42) },
			"unbanChatMember", `{"chat_id":"-1001","user_id":42,"only_if_banned":true}`, "unbanned"},
		{func() (map[string]any, error) { return client.RestrictMember(ctx, 42, perms, 0) },
			"restrictChatMember", `{"chat_id":"-1001","user_id":42,"permissions":{"can_send_messages":true,` +
				`"can_send_audios":false,"can_send_documents":false,"can_send_photos":false,"can_send_videos":false,` +
				`"can_send_video_notes":false,"can_send_voice_notes":false,"can_send_polls":false,` +
				`"can_send_other_messages":false,"can_add_web_page_previews":false,"can_react_to_messages":false,` +
				`"can_edit_tag":false,"can_change_info":false,"can_invite_users":true,"can_pin_messages":false,` +
				`"can_manage_topics":false},"use_independent_chat_permissions":true}`, "restricted"},
		{func() (map[string]any, error) { return client.RestrictMember(ctx, 42, chatPermissions{}, 1900000000) },
			"restrictChatMember", `"use_independent_chat_permissions":true,"until_date":1900000000}`, "restricted"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.result] != true || len(got) != 1 {
			t.Fatalf("call %d = %v, %v", i, got, err)
		}
		if (*paths)[i] != c.path || (!strings.HasPrefix(c.body, `{`) && !strings.HasSuffix((*bodies)[i], c.body)) ||
			(strings.HasPrefix(c.body, `{`) && (*bodies)[i] != c.body) {
			t.Errorf("call %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], c.path, c.body)
		}
	}
}

func TestMemberRestrictRefusesIncompletePermissionsBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	var cases []string
	for _, name := range restrictPermissionNames {
		cases = append(cases, `{"user_id":42,"permissions":`+permissionsJSON("true", name)+`}`)
	}
	cases = append(cases, `{"user_id":42}`, `{"user_id":42,"permissions":null}`,
		`{"user_id":42,"permissions":`+strings.TrimSuffix(permissionsJSON("true", ""), "}")+`,"can_x":true}}`)
	for _, args := range cases {
		if _, err := invokeMembersRestrict(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(args)); err == nil {
			t.Errorf("restrict accepted %s", args)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestMemberRejectsBadInputBeforeIO(t *testing.T) {
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	if _, err := client.BanMember(ctx, 0, 0, false); err == nil {
		t.Error("ban accepted user_id 0")
	}
	if _, err := client.BanMember(ctx, 1, -5, false); err == nil {
		t.Error("ban accepted a negative until_date")
	}
	if _, err := client.UnbanMember(ctx, -1); err == nil {
		t.Error("unban accepted a negative user_id")
	}
	if _, err := client.RestrictMember(ctx, 0, chatPermissions{}, 0); err == nil {
		t.Error("restrict accepted user_id 0")
	}
	if len(*bodies) != 0 {
		t.Errorf("requests = %d, want 0", len(*bodies))
	}
	resolved := resolvedWith("-1001")
	for name, invoke := range memberInvokes {
		for _, args := range []string{`{"user_id":0}`, `{"user_id":-3}`, `{"user_id":1,"extra":1}`, `{}`,
			`{"user_id":1,"until_date":-1,"permissions":` + permissionsJSON("true", "") + `}`} {
			if _, err := invoke(ctx, resolved, nil, nil, json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
		}
	}
	for _, args := range []string{`{"user_id":1,"only_if_banned":false}`} {
		if _, err := invokeMembersUnban(ctx, resolved, nil, nil, json.RawMessage(args)); err == nil {
			t.Errorf("unban accepted %s", args)
		}
	}
}

func TestMemberRejectsUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	args := map[string]string{
		"ban": `{"chat":"-2002","user_id":1}`, "unban": `{"chat":"-2002","user_id":1}`,
		"restrict": `{"chat":"-2002","user_id":1,"permissions":` + permissionsJSON("false", "") + `}`,
	}
	for name, invoke := range memberInvokes {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args[name]))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestMemberSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*Client) error{
		"ban":      func(c *Client) error { _, err := c.BanMember(ctx, 1, 0, true); return err },
		"unban":    func(c *Client) error { _, err := c.UnbanMember(ctx, 1); return err },
		"restrict": func(c *Client) error { _, err := c.RestrictMember(ctx, 1, chatPermissions{}, 0); return err },
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
		client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":{"x":1}}`)
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("%s non-true result err = %v", name, err)
		}
	}
}

func TestMemberRisksProfileAndAllowList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		list   bool
	}{{membersBan, capability.EffectDelete, true}, {membersUnban, capability.EffectUpdate, false},
		{membersRestrict, capability.EffectUpdate, true}} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != memberDataSensitivity ||
			c.d.RequiresToolAllowList != c.list || c.d.Group != groupMembers {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
		if c.d.ID == membersBan.ID && !strings.Contains(c.d.Description, "revoke_messages") {
			t.Error("ban descriptor does not name revoke_messages")
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	found := false
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if (tool == membersBan.ID || tool == membersRestrict.ID) || (tool == membersUnban.ID && profile.ID != "moderation") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
		if profile.ID == "moderation" {
			found = true
			if profile.Recommended || len(profile.Tools) != 1 || profile.Tools[0] != membersUnban.ID {
				t.Errorf("moderation profile = %+v", profile)
			}
		}
	}
	if !found {
		t.Error("moderation profile is missing")
	}
}
