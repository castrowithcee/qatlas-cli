package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestChatAdminJSONBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	calls := []struct {
		run    func() (map[string]any, error)
		path   string
		body   string
		result string
	}{
		{func() (map[string]any, error) { return client.SetChatTitle(ctx, "Neu") },
			"setChatTitle", `{"chat_id":"-1001","title":"Neu"}`, "updated"},
		{func() (map[string]any, error) { return client.SetChatDescription(ctx, "Text") },
			"setChatDescription", `{"chat_id":"-1001","description":"Text"}`, "updated"},
		{func() (map[string]any, error) { return client.SetChatDescription(ctx, "") },
			"setChatDescription", `{"chat_id":"-1001","description":""}`, "updated"},
		{func() (map[string]any, error) { return client.DeleteChatPhoto(ctx) },
			"deleteChatPhoto", `{"chat_id":"-1001"}`, "deleted"},
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

func TestChatAdminLengthLimitsBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	resolved := resolvedWith("-1001")
	cases := []struct {
		invoke invokeFunc
		args   string
		ok     bool
	}{
		{invokeChatsSetTitle, `{"title":""}`, false},
		{invokeChatsSetTitle, `{"title":"` + strings.Repeat("ä", 129) + `"}`, false},
		{invokeChatsSetDescription, `{"description":"` + strings.Repeat("ä", 256) + `"}`, false},
		{invokeChatsSetDescription, `{}`, false},
	}
	for _, c := range cases {
		if _, err := c.invoke(context.Background(), resolved, resolver, nil, json.RawMessage(c.args)); err == nil {
			t.Errorf("accepted %.40s", c.args)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	for _, title := range []string{strings.Repeat("ä", 128)} {
		if _, err := client.SetChatTitle(context.Background(), title); err != nil {
			t.Errorf("128 characters refused: %v", err)
		}
	}
	if _, err := client.SetChatDescription(context.Background(), strings.Repeat("ä", 255)); err != nil {
		t.Errorf("255 characters refused: %v", err)
	}
	if _, err := client.SetChatTitle(context.Background(), ""); err == nil {
		t.Error("empty title accepted")
	}
	if len(*bodies) != 2 {
		t.Errorf("requests = %d, want 2", len(*bodies))
	}
}

func TestChatAdminRejectsForeignChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, c := range map[string]struct {
		invoke invokeFunc
		args   string
	}{
		"settitle":       {invokeChatsSetTitle, `{"chat":"-2002","title":"x"}`},
		"setdescription": {invokeChatsSetDescription, `{"chat":"-2002","description":"x"}`},
		"setphoto":       {invokeChatsSetPhoto, `{"chat":"-2002","local_path":"~/a.jpg"}`},
		"deletephoto":    {invokeChatsDeletePhoto, `{"chat":"-2002"}`},
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

func TestChatAdminSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	ops := map[string]func(*Client) error{
		"settitle": func(c *Client) error { _, err := c.SetChatTitle(context.Background(), "x"); return err },
		"setdescription": func(c *Client) error {
			_, err := c.SetChatDescription(context.Background(), "x")
			return err
		},
		"setphoto": func(c *Client) error {
			_, err := c.SetChatPhoto(context.Background(), mediaItem{name: "file.jpg", data: []byte("x")})
			return err
		},
		"deletephoto": func(c *Client) error { _, err := c.DeleteChatPhoto(context.Background()); return err },
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

func TestSetChatPhotoSendsNeutralMultipart(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":true}`), nil }
	path := e.write(t, "private-name.PNG", 12)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	raw, _ := json.Marshal(map[string]any{"local_path": path})
	out, err := invokeChatsSetPhotoWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red,
		raw, httpClient)
	if err != nil || out.(map[string]any)["updated"] != true || len(out.(map[string]any)) != 1 {
		t.Fatalf("out = %v, %v", out, err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/setChatPhoto" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	if len(fields) != 1 || fields["chat_id"] != mediaChat || len(files) != 1 ||
		files["photo"] != [2]string{"file.PNG", strings.Repeat("x", 12)} {
		t.Errorf("fields = %v files = %v", fields, files)
	}
	body := string(e.requests[0].body)
	if strings.Contains(body, e.dir) || strings.Contains(body, "private-name") {
		t.Error("path or file name reached the request")
	}
}

func TestSetChatPhotoRefusesBeforeSecretAndRead(t *testing.T) {
	e := newMediaEnv(t)
	outside := t.TempDir() + "/a.jpg"
	for name, arguments := range map[string]map[string]any{
		"outside release": {"local_path": outside},
		"too large":       {"local_path": e.write(t, "big.jpg", maxPhotoBytes+1)},
		"missing path":    {},
		"foreign chat":    {"local_path": e.write(t, "ok.jpg", 4), "chat": "-2002"},
		"file ref":        {"file_ref": "x"},
	} {
		raw, _ := json.Marshal(arguments)
		httpClient := newHTTPClient()
		httpClient.Transport = e.transport()
		_, err := invokeChatsSetPhotoWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets,
			e.red, raw, httpClient)
		if err == nil || strings.Contains(err.Error(), "-2002") || strings.Contains(err.Error(), e.dir) {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("lookups = %d requests = %d, want 0", e.lookups, len(e.requests))
	}
}

func TestChatAdminProfileAndAllowList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	found := false
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == chatsDeletePhoto.ID {
				t.Errorf("profile %s contains deletephoto", profile.ID)
			}
		}
		if profile.ID == "chat-admin" {
			found = true
			if profile.Recommended || len(profile.Tools) != 3 || profile.Tools[0] != chatsSetTitle.ID ||
				profile.Tools[1] != chatsSetDescription.ID || profile.Tools[2] != chatsSetPhoto.ID {
				t.Errorf("chat-admin profile = %+v", profile)
			}
		}
	}
	if !found {
		t.Error("profile chat-admin missing")
	}
	if !chatsDeletePhoto.RequiresToolAllowList || chatsSetTitle.RequiresToolAllowList ||
		chatsSetDescription.RequiresToolAllowList || chatsSetPhoto.RequiresToolAllowList {
		t.Error("only deletephoto requires a tools list")
	}
	if chatsSetPhoto.LocalFiles != config.LocalFilesRead || chatsSetPhoto.Risk.Idempotency != capability.IdempotencyUnknown ||
		chatsDeletePhoto.Risk.Effect != capability.EffectDelete {
		t.Error("setphoto or deletephoto risk or local files wrong")
	}
}
