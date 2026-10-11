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

const cmdOK = `{"ok":true,"result":true}`

func TestCommandsAndMenuButtonBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "bot", 200, cmdOK)
	ctx := context.Background()
	list := []botCommand{{"help", "Show help"}}
	chatScope := commandScope{Type: "chat", ChatID: "-1001"}
	steps := []struct {
		run        func() error
		path, body string
	}{
		{func() error { _, err := client.SetCommands(ctx, commandScope{Type: "default"}, "", list); return err },
			"setMyCommands", `{"commands":[{"command":"help","description":"Show help"}],"scope":{"type":"default"}}`},
		{func() error { _, err := client.SetCommands(ctx, chatScope, "de", list); return err },
			"setMyCommands", `{"commands":[{"command":"help","description":"Show help"}],` +
				`"scope":{"type":"chat","chat_id":"-1001"},"language_code":"de"}`},
		{func() error {
			_, err := client.DeleteCommands(ctx, commandScope{Type: "chat_member", ChatID: "-1001", UserID: 7}, "")
			return err
		}, "deleteMyCommands", `{"scope":{"type":"chat_member","chat_id":"-1001","user_id":7}}`},
		{func() error {
			_, err := client.DeleteCommands(ctx, commandScope{Type: "all_group_chats"}, "en")
			return err
		},
			"deleteMyCommands", `{"scope":{"type":"all_group_chats"},"language_code":"en"}`},
		{func() error { _, err := client.SetMenuButton(ctx, 0, "commands"); return err },
			"setChatMenuButton", `{"menu_button":{"type":"commands"}}`},
		{func() error { _, err := client.SetMenuButton(ctx, 42, "default"); return err },
			"setChatMenuButton", `{"chat_id":42,"menu_button":{"type":"default"}}`},
	}
	for i, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if (*paths)[i] != s.path || (*bodies)[i] != s.body {
			t.Errorf("step %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], s.path, s.body)
		}
	}
	reads, rbodies, rpaths := capture(t, "bot", 200, `{"ok":true,"result":[]}`)
	if _, err := reads.GetCommands(ctx, chatScope, "fr"); err != nil {
		t.Fatal(err)
	}
	if (*rpaths)[0] != "getMyCommands" ||
		(*rbodies)[0] != `{"scope":{"type":"chat","chat_id":"-1001"},"language_code":"fr"}` {
		t.Errorf("get = %v %v", *rpaths, *rbodies)
	}
	menu, mbodies, mpaths := capture(t, "bot", 200, `{"ok":true,"result":{"type":"commands"}}`)
	if _, err := menu.GetMenuButton(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if _, err := menu.GetMenuButton(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if (*mpaths)[0] != "getChatMenuButton" || (*mbodies)[0] != `{"chat_id":42}` || (*mbodies)[1] != `{}` {
		t.Errorf("menu get = %v %v", *mpaths, *mbodies)
	}
}

func TestCommandsGetUsesFieldAllowListAndCaps(t *testing.T) {
	var items []string
	for i := 0; i < 120; i++ {
		items = append(items, `{"command":"c`+strings.Repeat("x", 40)+`","description":"`+strings.Repeat("d", 300)+
			`","LEAK":"LEAKEDFIELD"}`)
	}
	client, _, _ := capture(t, "bot", 200, `{"ok":true,"result":[`+strings.Join(items, ",")+`]}`)
	got, err := client.GetCommands(context.Background(), commandScope{Type: "default"}, "")
	if err != nil || len(got.Commands) != 100 {
		t.Fatalf("got %d commands, %v", len(got.Commands), err)
	}
	if len(got.Commands[0].Command) != 32 || len(got.Commands[0].Description) != 256 {
		t.Errorf("not capped: %+v", got.Commands[0])
	}
	if encoded, _ := json.Marshal(got); strings.Contains(string(encoded), "LEAK") {
		t.Error("unlisted field leaked")
	}
	empty, _, _ := capture(t, "bot", 200, `{"ok":true,"result":[]}`)
	if got, _ := empty.GetCommands(context.Background(), commandScope{Type: "default"}, ""); string(mustJSON(got)) != `{"commands":[]}` {
		t.Errorf("empty = %s", mustJSON(got))
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestMenuButtonGetNeverShowsWebAppURL(t *testing.T) {
	client, _, _ := capture(t, "bot", 200,
		`{"ok":true,"result":{"type":"web_app","text":"Open","web_app":{"url":"https://canary.example/SECRETPATH"}}}`)
	got, err := client.GetMenuButton(context.Background(), 0)
	if err != nil || string(mustJSON(got)) != `{"type":"web_app","text":"Open"}` {
		t.Errorf("got %s, %v", mustJSON(got), err)
	}
	bad, _, _ := capture(t, "bot", 200, `{"ok":true,"result":{"text":"x"}}`)
	if _, err := bad.GetMenuButton(context.Background(), 0); err == nil {
		t.Error("missing type accepted")
	}
}

type invokeCase struct {
	invoke invokeFunc
	args   string
}

func TestCommandsRefuseWithoutBotTargetBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, c := range map[string]invokeCase{
		"get":      {invokeCommandsGet, `{}`},
		"set":      {invokeCommandsSet, `{"commands":[{"command":"a","description":"b"}]}`},
		"delete":   {invokeCommandsDelete, `{}`},
		"mget":     {invokeMenubuttonGet, `{}`},
		"mset":     {invokeMenubuttonSet, `{"type":"commands"}`},
		"mgetchat": {invokeMenubuttonGet, `{"chat":"42"}`},
	} {
		if _, err := c.invoke(context.Background(), resolvedWith("42"), resolver, nil, json.RawMessage(c.args)); err == nil {
			t.Errorf("%s accepted without bot target", name)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d", resolutions)
	}
}

func TestCommandsRefuseBadInputAndForeignChatsBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	cmds := func(n int) string {
		var items []string
		for i := 0; i < n; i++ {
			items = append(items, `{"command":"a","description":"b"}`)
		}
		return `"commands":[` + strings.Join(items, ",") + `]`
	}
	set := func(extra string) invokeCase { return invokeCase{invokeCommandsSet, `{` + cmds(1) + extra + `}`} }
	cases := []invokeCase{
		{invokeCommandsSet, `{` + cmds(101) + `}`}, {invokeCommandsSet, `{` + cmds(0) + `}`},
		{invokeCommandsSet, `{}`},
		{invokeCommandsSet, `{"commands":[{"command":"","description":"b"}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"Up","description":"b"}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"a-b","description":"b"}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"` + strings.Repeat("a", 33) + `","description":"b"}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"a","description":""}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"a","description":"` + strings.Repeat("ä", 257) + `"}]}`},
		{invokeCommandsSet, `{"commands":[{"command":"a","description":"b","x":1}]}`},
		set(`,"language_code":"DE"`), set(`,"language_code":"deu"`), set(`,"language_code":"d1"`),
		set(`,"scope":"everyone"`), set(`,"scope":"default","chat":"-1001"`),
		set(`,"scope":"chat"`), set(`,"scope":"chat","chat":"-2002"`),
		set(`,"scope":"chat_administrators","chat":"-2002"`),
		set(`,"scope":"chat_member","chat":"-1001"`), set(`,"scope":"chat_member","chat":"-1001","user_id":0`),
		set(`,"scope":"chat_member","chat":"-1001","user_id":-3`), set(`,"scope":"chat","chat":"-1001","user_id":3`),
		set(`,"scope":"default","user_id":3`),
		{invokeCommandsGet, `{"scope":"chat","chat":"-2002"}`}, {invokeCommandsGet, `{"language_code":"zz9"}`},
		{invokeCommandsDelete, `{"scope":"chat_member","chat":"-2002","user_id":3}`},
		{invokeCommandsDelete, `{"scope":"chat"}`},
		{invokeMenubuttonGet, `{"chat":"-2002"}`}, {invokeMenubuttonGet, `{"chat":"@chan"}`},
		{invokeMenubuttonSet, `{"type":"commands","chat":"-2002"}`}, {invokeMenubuttonSet, `{"type":"commands","chat":"@chan"}`},
		{invokeMenubuttonSet, `{"type":"web_app"}`}, {invokeMenubuttonSet, `{}`},
		{invokeMenubuttonSet, `{"type":"commands","text":"x"}`},
		{invokeMenubuttonSet, `{"type":"web_app","text":"x","web_app":{"url":"https://x.example"}}`},
		{invokeMenubuttonSet, `{"type":"commands","url":"https://x.example"}`},
		{invokeMenubuttonSet, `{"type":"commands","menu_button":{"type":"web_app"}}`},
	}
	for _, c := range cases {
		_, err := c.invoke(context.Background(), resolvedWith("bot", "-1001", "@chan"), resolver, nil, json.RawMessage(c.args))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%.80s err = %v", c.args, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestBoundChatScopesResolveToConfiguredSpelling(t *testing.T) {
	scope, err := commandScopeArgs{Scope: "chat_member", Chat: "-1001", UserID: 7, LanguageCode: "de"}.
		resolve(resolvedWith("bot", "-1001"), "x")
	if err != nil || scope != (commandScope{Type: "chat_member", ChatID: "-1001", UserID: 7}) {
		t.Errorf("scope = %+v, %v", scope, err)
	}
	if id, err := menuChat(resolvedWith("bot", "42"), "x", "42"); err != nil || id != 42 {
		t.Errorf("menuChat = %d, %v", id, err)
	}
	if id, err := menuChat(resolvedWith("bot", "42"), "x", ""); err != nil || id != 0 {
		t.Errorf("default menuChat = %d, %v", id, err)
	}
}

func TestCommandsSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*Client) error{
		"set": func(c *Client) error {
			_, err := c.SetCommands(ctx, commandScope{Type: "default"}, "", []botCommand{{"a", "b"}})
			return err
		},
		"delete":  func(c *Client) error { _, err := c.DeleteCommands(ctx, commandScope{Type: "default"}, ""); return err },
		"menuset": func(c *Client) error { _, err := c.SetMenuButton(ctx, 0, "default"); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "bot", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "bot", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
	}
	// Reads are not marked uncertain and are not retried either.
	calls := 0
	client, _ := telegramClient(t, "bot", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(500, `{"ok":false,"description":"secret text"}`), nil
	}))
	if _, err := client.GetCommands(ctx, commandScope{Type: "default"}, ""); err == nil ||
		strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") || calls != 1 {
		t.Errorf("read err = %v calls = %d", err, calls)
	}
}

func TestCommandsToolRisksGroupsAndProfiles(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, d := range []capability.Descriptor{commandsGet, commandsSet, commandsDelete, menubuttonGet, menubuttonSet} {
		read := d.ID == commandsGet.ID || d.ID == menubuttonGet.ID
		del := d.ID == commandsDelete.ID
		if d.Group != groupBot || d.RequiresToolAllowList != del || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != dataSensitivity ||
			read != (d.Risk.Effect == capability.EffectRead) || del != (d.Risk.Effect == capability.EffectDelete) {
			t.Errorf("%s descriptor = %+v", d.ID, d)
		}
		if read && (d.Risk.Idempotency != capability.IdempotencySafe || d.Risk.Confirmation != capability.ConfirmationNone) {
			t.Errorf("%s read risk = %+v", d.ID, d.Risk)
		}
		if !read && (d.Risk.Idempotency != capability.IdempotencyIdempotent || d.Risk.Confirmation != capability.ConfirmationRequired) {
			t.Errorf("%s mutation risk = %+v", d.ID, d.Risk)
		}
		if !strings.Contains(string(d.InputSchema), `"additionalProperties":false`) {
			t.Errorf("%s schema is open", d.ID)
		}
	}
	if !strings.Contains(string(menubuttonSet.InputSchema), `"enum":["commands","default"]`) ||
		strings.Contains(string(menubuttonSet.InputSchema), "web_app") || strings.Contains(string(menubuttonSet.InputSchema), "url") {
		t.Error("menubutton.set schema admits more than commands and default")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.commands.") || strings.HasPrefix(tool, "telegram.menubutton.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
