package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func allRights(value bool) map[string]bool {
	out := map[string]bool{}
	for _, name := range promoteRightNames {
		out[name] = value
	}
	return out
}

func TestDefaultAdminRightsBodiesAndPaths(t *testing.T) {
	rights := allRights(false)
	rights["can_delete_messages"] = true
	client, bodies, paths := capture(t, "bot", 200, `{"ok":true,"result":true}`)
	if out, err := client.SetDefaultAdminRights(context.Background(), true, rights); err != nil || out["updated"] != true {
		t.Fatalf("set = %v, %v", out, err)
	}
	var got struct {
		Rights      map[string]bool `json:"rights"`
		ForChannels bool            `json:"for_channels"`
	}
	if (*paths)[0] != "setMyDefaultAdministratorRights" || json.Unmarshal([]byte((*bodies)[0]), &got) != nil ||
		!got.ForChannels || len(got.Rights) != len(promoteRightNames) || !got.Rights["can_delete_messages"] ||
		got.Rights["is_anonymous"] || strings.Count((*bodies)[0], `"can_`)+strings.Count((*bodies)[0], `"is_`) != len(promoteRightNames) {
		t.Errorf("request = %v %v", *paths, *bodies)
	}
	reader, rbodies, rpaths := capture(t, "bot", 200, `{"ok":true,"result":{}}`)
	if _, err := reader.GetDefaultAdminRights(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if (*rpaths)[0] != "getMyDefaultAdministratorRights" || (*rbodies)[0] != `{"for_channels":false}` {
		t.Errorf("get = %v %v", *rpaths, *rbodies)
	}
}

func TestDefaultAdminRightsGetUsesFieldAllowList(t *testing.T) {
	client, _, _ := capture(t, "bot", 200,
		`{"ok":true,"result":{"is_anonymous":true,"can_post_messages":true,"LEAK":"LEAKEDFIELD","can_invite_users":true}}`)
	got, err := client.GetDefaultAdminRights(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	rights := got["rights"].(map[string]bool)
	if len(got) != 1 || len(rights) != len(promoteRightNames) || !rights["is_anonymous"] || !rights["can_post_messages"] ||
		!rights["can_invite_users"] || rights["can_manage_chat"] {
		t.Errorf("got = %v", got)
	}
	if encoded, _ := json.Marshal(got); strings.Contains(string(encoded), "LEAK") {
		t.Error("unlisted field leaked")
	}
	for _, body := range []string{`{"ok":true,"result":{"is_anonymous":"yes"}}`, `{"ok":true,"result":null}`} {
		bad, _, _ := capture(t, "bot", 200, body)
		if _, err := bad.GetDefaultAdminRights(context.Background(), false); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
}

func TestDefaultAdminRightsRefuseBadInputAndMissingBotBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	full := mustJSON(allRights(true))
	missing := allRights(true)
	delete(missing, "can_manage_tags")
	unknown := allRights(true)
	unknown["can_do_anything"] = true
	cases := map[string]invokeCase{
		"get no bot":     {invokeDefaultAdminRightsGet, `{"for_channels":true}`},
		"set no bot":     {invokeDefaultAdminRightsSet, `{"for_channels":true,"rights":` + string(full) + `}`},
		"get no flag":    {invokeDefaultAdminRightsGet, `{}`},
		"set no flag":    {invokeDefaultAdminRightsSet, `{"rights":` + string(full) + `}`},
		"set no rights":  {invokeDefaultAdminRightsSet, `{"for_channels":true}`},
		"set missing":    {invokeDefaultAdminRightsSet, `{"for_channels":true,"rights":` + string(mustJSON(missing)) + `}`},
		"set unknown":    {invokeDefaultAdminRightsSet, `{"for_channels":true,"rights":` + string(mustJSON(unknown)) + `}`},
		"set null right": {invokeDefaultAdminRightsSet, `{"for_channels":true,"rights":` + strings.Replace(string(full), `"is_anonymous":true`, `"is_anonymous":null`, 1) + `}`},
		"set chat":       {invokeDefaultAdminRightsSet, `{"chat":"42","for_channels":true,"rights":` + string(full) + `}`},
		"get chat":       {invokeDefaultAdminRightsGet, `{"chat":"42","for_channels":true}`},
	}
	for name, c := range cases {
		target := "bot"
		if strings.HasSuffix(name, "no bot") {
			target = "42"
		}
		if _, err := c.invoke(context.Background(), resolvedWith(target), resolver, nil, json.RawMessage(c.args)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestDefaultAdminRightsSetSingleRequestAndUncertainty(t *testing.T) {
	op := func(c *Client) error {
		_, err := c.SetDefaultAdminRights(context.Background(), false, allRights(false))
		return err
	}
	for _, status := range []int{500, 502} {
		client, bodies, _ := capture(t, "bot", status, `{"ok":false,"description":"secret text"}`)
		err := op(client)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") ||
			len(*bodies) != 1 {
			t.Errorf("status %d err = %v requests = %d", status, err, len(*bodies))
		}
	}
	calls := 0
	client, _ := telegramClient(t, "bot", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &timeoutError{}
	}))
	if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
		t.Errorf("timeout err = %v calls = %d", err, calls)
	}
	if _, err := (&Client{}).SetDefaultAdminRights(context.Background(), false, map[string]bool{"is_anonymous": true}); err == nil {
		t.Error("incomplete rights accepted by the client method")
	}
}
