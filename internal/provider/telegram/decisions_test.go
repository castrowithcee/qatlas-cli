package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestDecisionBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	soon := time.Now().Add(48 * time.Hour).Unix()
	calls := []struct {
		run    func() (map[string]any, error)
		path   string
		body   string
		result string
	}{
		{func() (map[string]any, error) { return client.DeclineJoinRequest(ctx, 42) },
			"declineChatJoinRequest", `{"chat_id":"-1001","user_id":42}`, "declined"},
		{func() (map[string]any, error) { return client.ApproveSuggestedPost(ctx, 91, 0) },
			"approveSuggestedPost", `{"chat_id":-1001,"message_id":91}`, "approved"},
		{func() (map[string]any, error) { return client.ApproveSuggestedPost(ctx, 91, soon) },
			"approveSuggestedPost", fmt.Sprintf(`{"chat_id":-1001,"message_id":91,"send_date":%d}`, soon), "approved"},
		{func() (map[string]any, error) { return client.DeclineSuggestedPost(ctx, 91, "") },
			"declineSuggestedPost", `{"chat_id":-1001,"message_id":91}`, "declined"},
		{func() (map[string]any, error) { return client.DeclineSuggestedPost(ctx, 91, "no fit") },
			"declineSuggestedPost", `{"chat_id":-1001,"message_id":91,"comment":"no fit"}`, "declined"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.result] != true || len(got) != 1 {
			t.Fatalf("call %d = %v, %v", i, got, err)
		}
		if (*paths)[i] != c.path || (*bodies)[i] != c.body {
			t.Errorf("call %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], c.path, c.body)
		}
	}
	if len(*bodies) != len(calls) {
		t.Errorf("requests = %d, want %d", len(*bodies), len(calls))
	}
}

func TestSendDateBounds(t *testing.T) {
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	now := time.Now()
	for _, bad := range []int64{-1, now.Add(-time.Hour).Unix(), now.Add(31 * 24 * time.Hour).Unix()} {
		if _, err := client.ApproveSuggestedPost(context.Background(), 1, bad); err == nil {
			t.Errorf("send_date %d accepted", bad)
		}
	}
	if len(*bodies) != 0 {
		t.Errorf("requests = %d, want 0", len(*bodies))
	}
	if err := checkSendDate("x", now.Add(maxSendDateAhead).Unix()-5, now); err != nil {
		t.Errorf("send_date near the limit refused: %v", err)
	}
}

func TestDecisionsRefuseBadInputAndTargetsBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	long := strings.Repeat("a", 129)
	invokes := map[string]struct {
		run  memberInvoke
		args []string
	}{
		"join": {invokeJoinRequestsDecline, []string{`{"user_id":0}`, `{}`, `{"user_id":1,"x":1}`}},
		"approve": {invokeSuggestedPostsApprove, []string{`{"message_id":0}`, `{}`, `{"message_id":1,"price":5}`,
			`{"message_id":1,"send_date":1}`, fmt.Sprintf(`{"message_id":1,"send_date":%d}`, time.Now().Add(40*24*time.Hour).Unix())}},
		"decline": {invokeSuggestedPostsDecline, []string{`{"message_id":0}`, `{}`, `{"message_id":1,"comment":"` + long + `"}`}},
	}
	for name, c := range invokes {
		for _, args := range c.args {
			if _, err := c.run(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
		}
		// A foreign chat, an unbound chat among several, and a missing chat among several fail locally.
		for _, r := range []struct {
			targets []string
			args    string
		}{
			{[]string{"-1001"}, `{"chat":"-2002",%s}`}, {[]string{"-1001", "-1002"}, `{%s}`},
			{[]string{"bot"}, `{%s}`},
		} {
			valid := map[string]string{"join": `"user_id":1`, "approve": `"message_id":1`, "decline": `"message_id":1`}[name]
			_, err := c.run(context.Background(), resolvedWith(r.targets...), resolver, nil,
				json.RawMessage(fmt.Sprintf(r.args, valid)))
			if err == nil || strings.Contains(err.Error(), "-2002") {
				t.Errorf("%s with %v err = %v", name, r.targets, err)
			}
		}
	}
	for name, run := range map[string]memberInvoke{"approve": invokeSuggestedPostsApprove, "decline": invokeSuggestedPostsDecline} {
		_, err := run(context.Background(), resolvedWith("@news"), resolver, nil, json.RawMessage(`{"message_id":1}`))
		if err == nil || strings.Contains(err.Error(), "@news") {
			t.Errorf("%s with an @username target err = %v", name, err)
		}
		_, err = run(context.Background(), resolvedWith("-1001", "@news"), resolver, nil,
			json.RawMessage(`{"chat":"@news","message_id":1}`))
		if err == nil {
			t.Errorf("%s accepted a selected @username target", name)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestDecisionsSendOneRequestAndReportUncertainty(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*Client) error{
		"join":    func(c *Client) error { _, err := c.DeclineJoinRequest(ctx, 1); return err },
		"approve": func(c *Client) error { _, err := c.ApproveSuggestedPost(ctx, 1, 0); return err },
		"decline": func(c *Client) error { _, err := c.DeclineSuggestedPost(ctx, 1, "x"); return err },
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

func TestDecisionRisksGroupsAndAllowList(t *testing.T) {
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
		group  string
		list   bool
	}{{joinrequestsDecline, capability.EffectUpdate, groupMembers, false},
		{callbacksAnswer, capability.EffectUpdate, groupQueries, false},
		{joinrequestqueriesAnswer, capability.EffectUpdate, groupQueries, false},
		{suggestedpostsApprove, capability.EffectCreate, groupSuggestedPosts, false},
		{suggestedpostsDecline, capability.EffectUpdate, groupSuggestedPosts, true}} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity == "" ||
			c.d.RequiresToolAllowList != c.list || c.d.Group != c.group {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == joinrequestsDecline.ID || tool == callbacksAnswer.ID || tool == joinrequestqueriesAnswer.ID || tool == suggestedpostsApprove.ID || tool == suggestedpostsDecline.ID {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
