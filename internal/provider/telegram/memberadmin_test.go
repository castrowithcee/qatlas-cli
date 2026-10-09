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
	client, bodies, paths := promoteClient(t, memberResult("member", ""), 200, `{"ok":true,"result":true}`)
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
		if err := json.Unmarshal([]byte((*bodies)[2*i+1]), &body); err != nil {
			t.Fatal(err)
		}
		if (*paths)[2*i] != "getChatMember" || (*paths)[2*i+1] != "promoteChatMember" || len(body) != len(want)+2 || body["chat_id"] != "-1001" ||
			body["user_id"] != float64(42) {
			t.Fatalf("body %d = %s %s", i, (*paths)[2*i+1], (*bodies)[2*i+1])
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
	if len(*bodies) != 4 {
		t.Errorf("requests = %d, want 4", len(*bodies))
	}
}

func memberResult(status, isMember string) string {
	extra := ""
	if isMember != "" {
		extra = `,"is_member":` + isMember
	}
	return `{"ok":true,"result":{"user":{"id":42,"is_bot":false,"first_name":"A"},"status":"` + status + `"` + extra + `}}`
}

// promoteClient answers getChatMember with member and every other method with the given status and payload.
func promoteClient(t *testing.T, member string, status int, payload string) (*Client, *[]string, *[]string) {
	t.Helper()
	var bodies, paths []string
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
		bodies, paths = append(bodies, string(raw)), append(paths, method)
		if method == "getChatMember" {
			return response(200, member), nil
		}
		return response(status, payload), nil
	}))
	return client, &bodies, &paths
}

func allFalseRights() map[string]bool {
	rights := map[string]bool{}
	for _, name := range promoteRightNames {
		rights[name] = false
	}
	return rights
}

func TestPromoteRequiresConfirmedMembership(t *testing.T) {
	const ok = `{"ok":true,"result":true}`
	for _, c := range []struct {
		name, member string
		allowed      bool
	}{
		{"member", memberResult("member", ""), true},
		{"administrator", memberResult("administrator", ""), true},
		{"restricted member", memberResult("restricted", "true"), true},
		{"left", memberResult("left", ""), false},
		{"kicked", memberResult("kicked", ""), false},
		{"creator", memberResult("creator", ""), false},
		{"restricted non-member", memberResult("restricted", "false"), false},
		{"restricted unknown", memberResult("restricted", ""), false},
		{"unknown status", memberResult("ghost", ""), false},
		{"other user", strings.Replace(memberResult("member", ""), `"id":42`, `"id":7`, 1), false},
		{"unreadable", `not json`, false},
		{"result true", ok, false},
	} {
		client, bodies, paths := promoteClient(t, c.member, 200, ok)
		_, err := client.PromoteMember(context.Background(), 42, allFalseRights())
		if c.allowed {
			if err != nil || len(*paths) != 2 || (*paths)[0] != "getChatMember" || (*paths)[1] != "promoteChatMember" {
				t.Errorf("%s: err = %v paths = %v", c.name, err, *paths)
			}
			continue
		}
		if err == nil || len(*paths) != 1 || (*paths)[0] != "getChatMember" {
			t.Errorf("%s: err = %v paths = %v", c.name, err, *paths)
			continue
		}
		if strings.Contains(err.Error(), "-1001") || strings.Contains(err.Error(), "42") ||
			strings.Contains(err.Error(), "secret text") {
			t.Errorf("%s: error leaks detail: %v", c.name, err)
		}
		if !strings.Contains((*bodies)[0], `"user_id":42`) || !strings.Contains((*bodies)[0], `"chat_id":"-1001"`) {
			t.Errorf("%s: lookup body = %s", c.name, (*bodies)[0])
		}
	}
}

func TestPromoteRefusesWhenMembershipLookupFails(t *testing.T) {
	for _, status := range []int{500, 429, 403} {
		var paths []string
		client, _ := telegramClient(t, "-1001", roundTripFunc(func(r *http.Request) (*http.Response, error) {
			paths = append(paths, r.URL.Path)
			return response(status, `{"ok":false,"description":"secret text"}`), nil
		}))
		_, err := client.PromoteMember(context.Background(), 42, allFalseRights())
		if err == nil || len(paths) != 1 || strings.Contains(err.Error(), "secret text") {
			t.Errorf("status %d: err = %v paths = %v", status, err, paths)
		}
	}
	calls := 0
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &timeoutError{}
	}))
	if _, err := client.PromoteMember(context.Background(), 42, allFalseRights()); err == nil || calls != 1 {
		t.Errorf("timeout err = %v calls = %d", err, calls)
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
		"promote": func(c *Client) error { _, err := c.PromoteMember(ctx, 42, rights); return err },
		"title":   func(c *Client) error { _, err := c.SetAdminTitle(ctx, 1, "x"); return err },
		"tag":     func(c *Client) error { _, err := c.SetMemberTag(ctx, 1, "x"); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := promoteClient(t, memberResult("member", ""), status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if want := map[bool]int{true: 2, false: 1}[name == "promote"]; len(*bodies) != want {
				t.Errorf("%s requests = %d, want %d", name, len(*bodies), want)
			}
		}
		calls := 0
		client, _ := telegramClient(t, "-1001", roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getChatMember") {
				return response(200, memberResult("member", "")), nil
			}
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
