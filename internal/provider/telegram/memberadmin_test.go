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

func rightsJSON(value, skip string) string {
	var parts []string
	for _, name := range promoteRightNames {
		if name != skip {
			parts = append(parts, `"`+name+`":`+value)
		}
	}
	return `{` + strings.Join(parts, ",") + `}`
}

func TestPromoteRefusesIncompleteRightsBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	var cases []string
	for _, name := range promoteRightNames {
		cases = append(cases, `{"user_id":42,"rights":`+rightsJSON("true", name)+`}`)
	}
	cases = append(cases, `{"user_id":42}`, `{"user_id":42,"rights":null}`, `{"user_id":42,"rights":{}}`,
		`{"user_id":42,"rights":`+strings.TrimSuffix(rightsJSON("true", ""), "}")+`,"can_x":true}}`,
		`{"user_id":42,"rights":`+strings.Replace(rightsJSON("true", ""), `"is_anonymous":true`, `"is_anonymous":null`, 1)+`}`)
	for _, args := range cases {
		if _, err := invokeMembersPromote(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(args)); err == nil {
			t.Errorf("promote accepted %s", args)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestPromoteSendsEveryRightExplicitly(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	rights := map[string]bool{}
	for _, name := range promoteRightNames {
		rights[name] = false
	}
	got, err := client.PromoteMember(context.Background(), 42, rights)
	if err != nil || got["promoted"] != true || len(got) != 1 {
		t.Fatalf("demote = %v, %v", got, err)
	}
	rights["can_delete_messages"] = true
	if _, err := client.PromoteMember(context.Background(), 42, rights); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, name := range promoteRightNames {
		want = append(want, name)
	}
	for i, wantTrue := range []string{"", "can_delete_messages"} {
		var body map[string]any
		if err := json.Unmarshal([]byte((*bodies)[i]), &body); err != nil {
			t.Fatal(err)
		}
		if (*paths)[i] != "promoteChatMember" || len(body) != len(want)+2 || body["chat_id"] != "-1001" ||
			body["user_id"] != float64(42) {
			t.Fatalf("body %d = %s %s", i, (*paths)[i], (*bodies)[i])
		}
		for _, name := range want {
			if v, ok := body[name]; !ok || v != (name == wantTrue) {
				t.Errorf("body %d %s = %v, %v", i, name, v, ok)
			}
		}
	}
	if _, err := client.PromoteMember(context.Background(), 42, map[string]bool{"is_anonymous": true}); err == nil {
		t.Error("client accepted partial rights")
	}
	if len(*bodies) != 2 {
		t.Errorf("requests = %d, want 2", len(*bodies))
	}
}

func TestAdminTitleAndTagBodiesAndBounds(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	sixteen := strings.Repeat("ä", 16)
	if _, err := client.SetAdminTitle(ctx, 42, sixteen); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetAdminTitle(ctx, 42, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetMemberTag(ctx, 42, sixteen); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetMemberTag(ctx, 42, ""); err != nil {
		t.Fatal(err)
	}
	wantBodies := []string{
		`{"chat_id":"-1001","user_id":42,"custom_title":"` + sixteen + `"}`,
		`{"chat_id":"-1001","user_id":42,"custom_title":""}`,
		`{"chat_id":"-1001","user_id":42,"tag":"` + sixteen + `"}`,
		`{"chat_id":"-1001","user_id":42,"tag":""}`,
	}
	wantPaths := []string{"setChatAdministratorCustomTitle", "setChatAdministratorCustomTitle", "setChatMemberTag", "setChatMemberTag"}
	for i := range wantBodies {
		if (*bodies)[i] != wantBodies[i] || (*paths)[i] != wantPaths[i] {
			t.Errorf("call %d = %s %s", i, (*paths)[i], (*bodies)[i])
		}
	}
	for _, bad := range []string{strings.Repeat("ä", 17), "ok😀", "a❤", "a❤️", "👍🏽", "1️⃣", "a‍b", "©", "a\nb", "\xff"} {
		if _, err := client.SetAdminTitle(ctx, 42, bad); err == nil {
			t.Errorf("title accepted %q", bad)
		}
		if _, err := client.SetMemberTag(ctx, 42, bad); err == nil {
			t.Errorf("tag accepted %q", bad)
		}
	}
	if len(*bodies) != 4 {
		t.Errorf("requests = %d, want 4", len(*bodies))
	}
}

func TestAdminToolsRejectBadInputAndUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	cases := map[string]memberInvoke{
		"promote": invokeMembersPromote, "title": invokeMembersSetAdminTitle, "tag": invokeMembersSetTag,
	}
	valid := map[string]string{
		"promote": `"rights":` + rightsJSON("false", ""), "title": `"title":"x"`, "tag": `"tag":"x"`,
	}
	for name, invoke := range cases {
		for _, args := range []string{
			`{"chat":"-2002","user_id":1,` + valid[name] + `}`, `{"user_id":0,` + valid[name] + `}`,
			`{"user_id":1,` + valid[name] + `,"extra":1}`, `{"user_id":1}`,
		} {
			_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args))
			if err == nil || strings.Contains(err.Error(), "-2002") {
				t.Errorf("%s %s err = %v", name, args, err)
			}
		}
	}
	for _, args := range []string{`{"user_id":1,"title":"` + strings.Repeat("a", 17) + `"}`, `{"user_id":1,"title":"😀"}`} {
		if _, err := invokeMembersSetAdminTitle(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(args)); err == nil {
			t.Errorf("title accepted %s", args)
		}
	}
	for _, args := range []string{`{"user_id":1,"tag":"` + strings.Repeat("a", 17) + `"}`, `{"user_id":1,"tag":"😀"}`} {
		if _, err := invokeMembersSetTag(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(args)); err == nil {
			t.Errorf("tag accepted %s", args)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestAdminToolsSingleRequestAndUncertainty(t *testing.T) {
	ctx := context.Background()
	rights := map[string]bool{}
	for _, name := range promoteRightNames {
		rights[name] = false
	}
	ops := map[string]func(*Client) error{
		"promote": func(c *Client) error { _, err := c.PromoteMember(ctx, 1, rights); return err },
		"title":   func(c *Client) error { _, err := c.SetAdminTitle(ctx, 1, "x"); return err },
		"tag":     func(c *Client) error { _, err := c.SetMemberTag(ctx, 1, "x"); return err },
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

func TestAdminToolRisksAndAllowList(t *testing.T) {
	for _, c := range []struct {
		d    capability.Descriptor
		list bool
	}{{membersPromote, true}, {membersSetAdminTitle, false}, {membersSetTag, false}} {
		r := c.d.Risk
		if r.Effect != capability.EffectUpdate || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != memberDataSensitivity ||
			c.d.RequiresToolAllowList != c.list || c.d.Group != groupMembers {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
	}
	for _, name := range []string{"can_promote_members", "can_invite_users"} {
		if !strings.Contains(membersPromote.Description, name) {
			t.Errorf("promote descriptor does not name %s", name)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == membersPromote.ID || tool == membersSetAdminTitle.ID || tool == membersSetTag.ID {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
