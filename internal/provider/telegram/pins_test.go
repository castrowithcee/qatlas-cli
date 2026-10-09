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

func TestPinBodiesAndPaths(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	ctx := context.Background()
	calls := []struct {
		run    func() (map[string]any, error)
		path   string
		body   string
		result string
	}{
		{func() (map[string]any, error) { return client.PinMessage(ctx, 5, false) },
			"pinChatMessage", `{"chat_id":"-1001","message_id":5}`, "pinned"},
		{func() (map[string]any, error) { return client.PinMessage(ctx, 5, true) },
			"pinChatMessage", `{"chat_id":"-1001","message_id":5,"disable_notification":true}`, "pinned"},
		{func() (map[string]any, error) { return client.UnpinMessage(ctx, 5) },
			"unpinChatMessage", `{"chat_id":"-1001","message_id":5}`, "unpinned"},
		{func() (map[string]any, error) { return client.UnpinMessage(ctx, 0) },
			"unpinChatMessage", `{"chat_id":"-1001"}`, "unpinned"},
		{func() (map[string]any, error) { return client.UnpinAllMessages(ctx) },
			"unpinAllChatMessages", `{"chat_id":"-1001"}`, "unpinned"},
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

func TestPinRejectsBadInputBeforeIO(t *testing.T) {
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	if _, err := client.PinMessage(context.Background(), 0, false); err == nil {
		t.Error("pin accepted message_id 0")
	}
	if _, err := client.UnpinMessage(context.Background(), -1); err == nil {
		t.Error("unpin accepted a negative message_id")
	}
	if len(*bodies) != 0 {
		t.Errorf("requests = %d, want 0", len(*bodies))
	}
	resolved := resolvedWith("-1001")
	for name, invoke := range map[string]func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		"pin": invokePinsPin, "unpin": invokePinsUnpin, "unpinall": invokePinsUnpinAll,
	} {
		for _, args := range []string{`{"message_id":0,"extra":1}`, `{"message_id":-3}`, `{"text":"x"}`} {
			if _, err := invoke(context.Background(), resolved, nil, nil, json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
		}
	}
	if _, err := invokePinsPin(context.Background(), resolved, nil, nil, json.RawMessage(`{}`)); err == nil {
		t.Error("pin accepted a missing message_id")
	}
}

func TestPinRejectsUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, invoke := range map[string]func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		"pin": invokePinsPin, "unpin": invokePinsUnpin, "unpinall": invokePinsUnpinAll,
	} {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil,
			json.RawMessage(`{"chat":"-2002","message_id":1}`))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestPinSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	ops := map[string]func(*Client) error{
		"pin":      func(c *Client) error { _, err := c.PinMessage(context.Background(), 5, false); return err },
		"unpin":    func(c *Client) error { _, err := c.UnpinMessage(context.Background(), 5); return err },
		"unpinall": func(c *Client) error { _, err := c.UnpinAllMessages(context.Background()); return err },
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
		client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":{"message_id":1}}`)
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("%s non-true result err = %v", name, err)
		}
	}
}

func TestPinsProfileAndAllowList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == pinsUnpinAll.ID {
				t.Errorf("profile %s contains unpinall", profile.ID)
			}
		}
		if profile.ID == "pins" {
			if profile.Recommended || len(profile.Tools) != 2 ||
				profile.Tools[0] != pinsPin.ID || profile.Tools[1] != pinsUnpin.ID {
				t.Errorf("pins profile = %+v", profile)
			}
		}
	}
	if !pinsUnpinAll.RequiresToolAllowList || pinsPin.RequiresToolAllowList || pinsUnpin.RequiresToolAllowList {
		t.Error("only unpinall requires a tools list")
	}
}
