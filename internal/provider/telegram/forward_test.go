package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var transferInvokers = map[string]invokeFunc{"forward": invokeMessagesForward, "copy": invokeMessagesCopy}

// pairClient opens a client for a connection that binds -1001 and -1002 and records every request.
func pairClient(t *testing.T, status int, payload string, transportErr error) (*Client, *[]string, *[]string) {
	t.Helper()
	var bodies, paths []string
	httpClient := newHTTPClient()
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		bodies = append(bodies, b.String())
		paths = append(paths, strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/"))
		if transportErr != nil {
			return nil, transportErr
		}
		return response(status, payload), nil
	})
	resolver := secret.NewWith(func(string) string { return testToken }, nil, nil, &redact.Redactor{})
	client, _, err := openChatPairWithHTTP(context.Background(), resolvedWith("-1001", "-1002"), resolver, nil,
		"-1001", "-1002", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client, &bodies, &paths
}

func ids(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

func TestTransferMethodsAndBodies(t *testing.T) {
	ctx := context.Background()
	caption := "new"
	one := TransferOptions{MessageID: 5}
	all := TransferOptions{MessageID: 5, MessageThreadID: 3, DisableNotification: true, ProtectContent: true}
	list := TransferOptions{MessageIDs: []int64{5, 9}, MessageThreadID: 3, DisableNotification: true, ProtectContent: true}
	tests := []struct {
		name, payload, path, body, key string
		run                            func(*Client) (map[string]any, error)
		want                           any
	}{
		{"forward one", `{"ok":true,"result":{"message_id":70,"date":1,"text":"leak","chat":{"id":-5}}}`, "forwardMessage",
			`{"chat_id":"-1001","from_chat_id":"-1002","message_id":5}`, "message_id",
			func(c *Client) (map[string]any, error) { return c.Forward(ctx, "-1002", one) }, int64(70)},
		{"forward options", `{"ok":true,"result":{"message_id":70}}`, "forwardMessage",
			`{"chat_id":"-1001","message_thread_id":3,"from_chat_id":"-1002","message_id":5,` +
				`"disable_notification":true,"protect_content":true}`, "message_id",
			func(c *Client) (map[string]any, error) { return c.Forward(ctx, "-1002", all) }, int64(70)},
		{"forward list", `{"ok":true,"result":[{"message_id":70},{"message_id":71}]}`, "forwardMessages",
			`{"chat_id":"-1001","message_thread_id":3,"from_chat_id":"-1002","message_ids":[5,9],` +
				`"disable_notification":true,"protect_content":true}`, "message_ids",
			func(c *Client) (map[string]any, error) { return c.Forward(ctx, "-1002", list) }, []int64{70, 71}},
		{"copy one", `{"ok":true,"result":{"message_id":70}}`, "copyMessage",
			`{"chat_id":"-1001","from_chat_id":"-1001","message_id":5}`, "message_id",
			func(c *Client) (map[string]any, error) {
				return c.Copy(ctx, "-1001", CopyOptions{TransferOptions: one})
			}, int64(70)},
		{"copy caption", `{"ok":true,"result":{"message_id":70}}`, "copyMessage",
			`{"chat_id":"-1001","from_chat_id":"-1002","message_id":5,"caption":"new","parse_mode":"HTML"}`, "message_id",
			func(c *Client) (map[string]any, error) {
				return c.Copy(ctx, "-1002", CopyOptions{TransferOptions: one, Caption: &caption, ParseMode: "HTML"})
			}, int64(70)},
		{"copy list", `{"ok":true,"result":[{"message_id":70}]}`, "copyMessages",
			`{"chat_id":"-1001","message_thread_id":3,"from_chat_id":"-1002","message_ids":[5,9],` +
				`"disable_notification":true,"protect_content":true}`, "message_ids",
			func(c *Client) (map[string]any, error) {
				return c.Copy(ctx, "-1002", CopyOptions{TransferOptions: list})
			},
			[]int64{70}},
		{"copy list none", `{"ok":true,"result":[]}`, "copyMessages",
			`{"chat_id":"-1001","from_chat_id":"-1002","message_ids":[5,9]}`, "message_ids",
			func(c *Client) (map[string]any, error) {
				return c.Copy(ctx, "-1002", CopyOptions{TransferOptions: TransferOptions{MessageIDs: []int64{5, 9}}})
			}, []int64{}},
	}
	for _, tc := range tests {
		client, bodies, paths := pairClient(t, 200, tc.payload, nil)
		got, err := tc.run(client)
		if err != nil || len(got) != 1 || fmt.Sprint(got[tc.key]) != fmt.Sprint(tc.want) ||
			len(*bodies) != 1 || (*paths)[0] != tc.path || (*bodies)[0] != tc.body {
			t.Errorf("%s = %v, %v, %v %v", tc.name, got, err, *paths, *bodies)
		}
		if _, ok := got[tc.key].([]int64); tc.key == "message_ids" && !ok {
			t.Errorf("%s message_ids has type %T", tc.name, got[tc.key])
		}
	}
}

func TestTransferRejectsInvalidValuesBeforeIO(t *testing.T) {
	long := strings.Repeat("x", 1025)
	idsJSON := func(n int) string { b, _ := json.Marshal(ids(n)); return string(b) }
	cases := map[string][]string{
		"forward": {
			`{}`, `{"message_id":0}`, `{"message_id":-1}`, `{"message_id":1,"message_ids":[1,2]}`,
			`{"message_ids":[]}`, `{"message_ids":[1]}`, `{"message_ids":` + idsJSON(101) + `}`,
			`{"message_ids":[2,2]}`, `{"message_ids":[3,2]}`, `{"message_ids":[0,2]}`, `{"message_ids":[-1,2]}`,
			`{"message_id":1,"message_thread_id":-1}`, `{"message_id":1,"caption":"x"}`,
			`{"message_id":1,"method":"sendMessage"}`, `{"message_id":1,"reply_markup":{}}`,
			`{"message_id":1,"from_chat":"-9999"}`, `{"message_id":1,"chat":"-9999"}`,
		},
		"copy": {
			`{}`, `{"message_id":1,"message_ids":[1,2]}`, `{"message_ids":[1]}`, `{"message_ids":` + idsJSON(101) + `}`,
			`{"message_ids":[1,2],"caption":"x"}`, `{"message_ids":[1,2],"caption":"x","parse_mode":"HTML"}`,
			`{"message_id":1,"caption":""}`, `{"message_id":1,"caption":"` + long + `"}`,
			`{"message_id":1,"parse_mode":"HTML"}`, `{"message_id":1,"caption":"x","parse_mode":"Markdown"}`,
			`{"message_id":1,"caption":"x","reply_markup":{}}`, `{"message_id":1,"from_chat":"-9999"}`,
		},
	}
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, list := range cases {
		for _, args := range list {
			if _, err := transferInvokers[name](context.Background(), resolvedWith("-1001"), resolver, nil,
				json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestTransferListAndCaptionBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{2, 100} {
		client, bodies, _ := pairClient(t, 200, `{"ok":true,"result":[{"message_id":9}]}`, nil)
		if _, err := client.Forward(ctx, "-1002", TransferOptions{MessageIDs: ids(n)}); err != nil || len(*bodies) != 1 {
			t.Errorf("%d ids: %v", n, err)
		}
	}
	for _, n := range []int{1, 101} {
		client, bodies, _ := pairClient(t, 200, `{"ok":true,"result":[]}`, nil)
		if _, err := client.Forward(ctx, "-1002", TransferOptions{MessageIDs: ids(n)}); err == nil || len(*bodies) != 0 {
			t.Errorf("%d ids accepted: %v", n, err)
		}
	}
	for _, c := range []struct {
		caption string
		ok      bool
	}{{"x", true}, {strings.Repeat("ä", 1024), true}, {strings.Repeat("ä", 1025), false}, {"", false}} {
		client, bodies, _ := pairClient(t, 200, `{"ok":true,"result":{"message_id":9}}`, nil)
		caption := c.caption
		_, err := client.Copy(ctx, "-1002", CopyOptions{TransferOptions: TransferOptions{MessageID: 1}, Caption: &caption})
		if (err == nil) != c.ok || (len(*bodies) == 1) != c.ok {
			t.Errorf("caption of %d runes: err = %v requests = %d", len([]rune(c.caption)), err, len(*bodies))
		}
	}
	client, bodies, _ := pairClient(t, 200, `{"ok":true,"result":[]}`, nil)
	caption := "x"
	if _, err := client.Copy(ctx, "-1002", CopyOptions{TransferOptions: TransferOptions{MessageIDs: ids(2)},
		Caption: &caption}); err == nil || len(*bodies) != 0 {
		t.Errorf("caption with a list: %v", err)
	}
}

func TestTransferBindsSourceAndTargetBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, invoke := range transferInvokers {
		for _, tc := range []struct {
			resolved *config.Resolved
			args     string
		}{
			{resolvedWith("-1001"), `{"message_id":1,"from_chat":"-2002"}`},
			{resolvedWith("-1001"), `{"message_id":1,"chat":"-2002"}`},
			{resolvedWith("-1001", "-1002"), `{"message_id":1,"from_chat":"-2002","chat":"-1001"}`},
			{resolvedWith("-1001", "-1002"), `{"message_id":1,"chat":"-2002","from_chat":"-1001"}`},
			{resolvedWith("-1001", "-1002"), `{"message_id":1,"chat":"-1001"}`},
			{resolvedWith("-1001", "-1002"), `{"message_id":1,"from_chat":"-1001"}`},
			{resolvedWith("-1001", "-1002"), `{"message_id":1}`},
			{resolvedWith("@one", "-1002"), `{"message_id":1,"chat":"-1002","from_chat":"one"}`},
			{resolvedWith("bot"), `{"message_id":1}`},
			{resolvedWith("business/abc", "-1001"), `{"message_id":1,"from_chat":"business/abc","chat":"-1001"}`},
		} {
			_, err := invoke(context.Background(), tc.resolved, resolver, nil, json.RawMessage(tc.args))
			if err == nil || strings.Contains(err.Error(), "2002") || strings.Contains(err.Error(), "abc") {
				t.Errorf("%s %v %s err = %v", name, tc.resolved.Targets, tc.args, err)
			}
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestTransferAcceptsBoundChatsAndEquality(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		resolved *config.Resolved
		args     string
		body     string
	}{
		{resolvedWith("-1001"), `{"message_id":1}`, `{"chat_id":"-1001","from_chat_id":"-1001","message_id":1}`},
		{resolvedWith("-1001", "@two"), `{"message_id":1,"chat":"@two","from_chat":"@two"}`,
			`{"chat_id":"@two","from_chat_id":"@two","message_id":1}`},
		{resolvedWith("-1001", "@two"), `{"message_id":1,"chat":"-1001","from_chat":"@two"}`,
			`{"chat_id":"-1001","from_chat_id":"@two","message_id":1}`},
	} {
		var body string
		httpClient := newHTTPClient()
		httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			buf := make([]byte, 512)
			n, _ := r.Body.Read(buf)
			body = string(buf[:n])
			return response(200, `{"ok":true,"result":{"message_id":9}}`), nil
		})
		resolver := secret.NewWith(func(string) string { return testToken }, nil, nil, &redact.Redactor{})
		var arguments struct {
			Chat string `json:"chat"`
			TransferOptions
		}
		if err := json.Unmarshal([]byte(tc.args), &arguments); err != nil {
			t.Fatal(err)
		}
		client, source, err := openChatPairWithHTTP(ctx, tc.resolved, resolver, nil, arguments.Chat,
			arguments.FromChat, httpClient)
		if err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		if _, err := client.Copy(ctx, source, CopyOptions{TransferOptions: arguments.TransferOptions}); err != nil || body != tc.body {
			t.Errorf("%s = %s, %v", tc.args, body, err)
		}
	}
	client, bodies, _ := pairClient(t, 200, `{"ok":true,"result":{"message_id":9}}`, nil)
	for _, source := range []string{"", "-9999"} {
		if _, err := client.Forward(ctx, source, TransferOptions{MessageID: 1}); err == nil || len(*bodies) != 0 {
			t.Errorf("source %q accepted: %v", source, err)
		}
	}
}

func TestTransferSingleRequestAndUncertainty(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*Client) error{
		"forward one": func(c *Client) error { _, err := c.Forward(ctx, "-1002", TransferOptions{MessageID: 1}); return err },
		"forward list": func(c *Client) error {
			_, err := c.Forward(ctx, "-1002", TransferOptions{MessageIDs: ids(2)})
			return err
		},
		"copy one": func(c *Client) error {
			_, err := c.Copy(ctx, "-1002", CopyOptions{TransferOptions: TransferOptions{MessageID: 1}})
			return err
		},
		"copy list": func(c *Client) error {
			_, err := c.Copy(ctx, "-1002", CopyOptions{TransferOptions: TransferOptions{MessageIDs: ids(2)}})
			return err
		},
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := pairClient(t, status, `{"ok":false,"description":"secret text"}`, nil)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), "secret text") || len(*bodies) != 1 {
				t.Errorf("%s status %d err = %v requests = %d", name, status, err, len(*bodies))
			}
		}
		client, bodies, _ := pairClient(t, 0, "", &timeoutError{})
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(*bodies) != 1 {
			t.Errorf("%s timeout err = %v requests = %d", name, err, len(*bodies))
		}
		for _, garbage := range []string{`{"ok":true,"result":"garbage"}`, `{"ok":true,"result":{"message_id":0}}`,
			`{"ok":true,"result":[{"message_id":0},{"message_id":1}]}`, `{"ok":true,"result":[{"message_id":1},{"message_id":2},{"message_id":3}]}`,
			`{"ok":true,"result":{"message_id":1`} {
			client, bodies, _ := pairClient(t, 200, garbage, nil)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(*bodies) != 1 {
				t.Errorf("%s with %s: err = %v requests = %d", name, garbage, err, len(*bodies))
			}
		}
	}
}

func TestTransferDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == messagesForward.ID || tool == messagesCopy.ID {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
	for _, d := range []capability.Descriptor{messagesForward, messagesCopy} {
		r := d.Risk
		if d.Group != groupMessages || r.Effect != capability.EffectCreate ||
			r.Idempotency != capability.IdempotencyNonIdempotent || r.Confirmation != capability.ConfirmationRequired ||
			!r.OpenWorld || r.DataSensitivity != dataSensitivity || d.RequiresToolAllowList {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
		found := false
		for _, a := range d.Arguments {
			found = found || (a.Name == "from_chat" && !a.Required)
		}
		if !found || !strings.Contains(string(d.InputSchema), `"from_chat":{"type":"string"`) {
			t.Errorf("%s does not declare from_chat", d.ID)
		}
	}
}
