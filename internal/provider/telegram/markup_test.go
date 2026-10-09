package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// capture returns a client whose transport records bodies and answers with status and payload.
func capture(t *testing.T, target string, status int, payload string) (*Client, *[]string, *[]string) {
	t.Helper()
	var bodies, paths []string
	client, _ := telegramClient(t, target, roundTripFunc(func(r *http.Request) (*http.Response, error) {
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
		return response(status, payload), nil
	}))
	return client, &bodies, &paths
}

const sendOK = `{"ok":true,"result":{"message_id":91,"date":1787220000}}`

func TestSendBodies(t *testing.T) {
	kb := keyboard{{{Text: "Open", URL: "https://example.test/a"}, {Text: "Ack", CallbackData: "ack:1"}}}
	tests := []struct {
		name string
		opts SendOptions
		want string
	}{
		{"plain", SendOptions{Text: "hi"}, `{"chat_id":"-1001","text":"hi"}`},
		{"html", SendOptions{Text: "hi", ParseMode: "HTML"}, `{"chat_id":"-1001","text":"hi","parse_mode":"HTML"}`},
		{"markdown", SendOptions{Text: "hi", ParseMode: "MarkdownV2"}, `{"chat_id":"-1001","text":"hi","parse_mode":"MarkdownV2"}`},
		{"reply", SendOptions{Text: "hi", ReplyToMessageID: 7}, `{"chat_id":"-1001","text":"hi","reply_parameters":{"message_id":7}}`},
		{"topic", SendOptions{Text: "hi", MessageThreadID: 5}, `{"chat_id":"-1001","text":"hi","message_thread_id":5}`},
		{"silent", SendOptions{Text: "hi", DisableNotification: true}, `{"chat_id":"-1001","text":"hi","disable_notification":true}`},
		{"protect", SendOptions{Text: "hi", ProtectContent: true}, `{"chat_id":"-1001","text":"hi","protect_content":true}`},
		{"preview", SendOptions{Text: "hi", DisableLinkPreview: true}, `{"chat_id":"-1001","text":"hi","link_preview_options":{"is_disabled":true}}`},
		{"keyboard", SendOptions{Text: "hi", InlineKeyboard: kb},
			`{"chat_id":"-1001","text":"hi","reply_markup":{"inline_keyboard":[[{"text":"Open","url":"https://example.test/a"},{"text":"Ack","callback_data":"ack:1"}]]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, bodies, _ := capture(t, "-1001", 200, sendOK)
			if _, err := client.Send(context.Background(), tt.opts); err != nil {
				t.Fatal(err)
			}
			if len(*bodies) != 1 || (*bodies)[0] != tt.want {
				t.Errorf("body = %v, want %s", *bodies, tt.want)
			}
		})
	}
}

func TestEditBodies(t *testing.T) {
	kb := keyboard{{{Text: "Ack", CallbackData: "ack"}}}
	tests := []struct {
		name string
		opts EditOptions
		want string
	}{
		{"plain", EditOptions{Text: "x"}, `{"chat_id":"-1001","message_id":91,"text":"x"}`},
		{"html", EditOptions{Text: "x", ParseMode: "HTML"}, `{"chat_id":"-1001","message_id":91,"text":"x","parse_mode":"HTML"}`},
		{"preview", EditOptions{Text: "x", DisableLinkPreview: true},
			`{"chat_id":"-1001","message_id":91,"text":"x","link_preview_options":{"is_disabled":true}}`},
		{"keyboard", EditOptions{Text: "x", InlineKeyboard: kb},
			`{"chat_id":"-1001","message_id":91,"text":"x","reply_markup":{"inline_keyboard":[[{"text":"Ack","callback_data":"ack"}]]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":{"message_id":91}}`)
			if _, err := client.Edit(context.Background(), 91, tt.opts); err != nil {
				t.Fatal(err)
			}
			if (*bodies)[0] != tt.want {
				t.Errorf("body = %s, want %s", (*bodies)[0], tt.want)
			}
		})
	}
}

func TestEditReplyMarkupSetsAndRemoves(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":{"message_id":91}}`)
	kb := keyboard{{{Text: "Go", URL: "tg://resolve?domain=x"}}}
	if got, err := client.EditReplyMarkup(context.Background(), 91, kb); err != nil || got["message_id"] != int64(91) {
		t.Fatalf("set = %v, %v", got, err)
	}
	if got, err := client.EditReplyMarkup(context.Background(), 91, nil); err != nil || got["message_id"] != int64(91) {
		t.Fatalf("remove = %v, %v", got, err)
	}
	want := []string{
		`{"chat_id":"-1001","message_id":91,"reply_markup":{"inline_keyboard":[[{"text":"Go","url":"tg://resolve?domain=x"}]]}}`,
		`{"chat_id":"-1001","message_id":91}`,
	}
	for i := range want {
		if (*bodies)[i] != want[i] || (*paths)[i] != "editMessageReplyMarkup" {
			t.Errorf("request %d = %s %s", i, (*paths)[i], (*bodies)[i])
		}
	}
	client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	if got, err := client.EditReplyMarkup(context.Background(), 91, nil); err != nil || got["updated"] != true {
		t.Errorf("true result = %v, %v", got, err)
	}
}

func rows(n, per int) keyboard {
	var k keyboard
	for i := 0; i < n; i++ {
		var row []button
		for j := 0; j < per; j++ {
			row = append(row, button{Text: "b", CallbackData: "c"})
		}
		k = append(k, row)
	}
	return k
}

func TestKeyboardRejectedBeforeIO(t *testing.T) {
	bad := map[string]keyboard{
		"http":        {{{Text: "a", URL: "http://example.test"}}},
		"ftp":         {{{Text: "a", URL: "ftp://example.test"}}},
		"javascript":  {{{Text: "a", URL: "javascript:alert(1)"}}},
		"no host":     {{{Text: "a", URL: "https:///x"}}},
		"control":     {{{Text: "a", URL: "https://example.test/\x00"}}},
		"space":       {{{Text: "a", URL: "https://example.test/a b"}}},
		"userinfo":    {{{Text: "a", URL: "https://u:p@example.test/"}}},
		"long url":    {{{Text: "a", URL: "https://example.test/" + strings.Repeat("a", 2048)}}},
		"callback 65": {{{Text: "a", CallbackData: strings.Repeat("a", 65)}}},
		"both":        {{{Text: "a", URL: "https://example.test", CallbackData: "c"}}},
		"neither":     {{{Text: "a"}}},
		"empty text":  {{{CallbackData: "c"}}},
		"long text":   {{{Text: strings.Repeat("a", 65), CallbackData: "c"}}},
		"9 rows":      rows(9, 1),
		"9 buttons":   rows(1, 9),
		"empty row":   {{}},
	}
	for name, kb := range bad {
		t.Run(name, func(t *testing.T) {
			client, bodies, _ := capture(t, "-1001", 200, sendOK)
			ctx := context.Background()
			if _, err := client.Send(ctx, SendOptions{Text: "x", InlineKeyboard: kb}); err == nil {
				t.Error("Send accepted the keyboard")
			}
			if _, err := client.Edit(ctx, 91, EditOptions{Text: "x", InlineKeyboard: kb}); err == nil {
				t.Error("Edit accepted the keyboard")
			}
			if _, err := client.EditReplyMarkup(ctx, 91, kb); err == nil {
				t.Error("EditReplyMarkup accepted the keyboard")
			}
			if len(*bodies) != 0 {
				t.Errorf("requests = %d, want none", len(*bodies))
			}
		})
	}
	max := rows(8, 8)
	client, _, _ := capture(t, "-1001", 200, sendOK)
	if _, err := client.Send(context.Background(), SendOptions{Text: "x", InlineKeyboard: max}); err != nil {
		t.Errorf("8x8 keyboard rejected: %v", err)
	}
}

func TestHandlerRejectsExtraFieldsAndBadOptionsBeforeSecret(t *testing.T) {
	cases := map[string]string{
		"extra button field": `{"text":"x","inline_keyboard":[[{"text":"a","callback_data":"c","web_app":{"url":"https://e.test"}}]]}`,
		"extra argument":     `{"text":"x","method":"deleteMessage"}`,
		"bad parse mode":     `{"text":"x","parse_mode":"Markdown"}`,
		"negative reply":     `{"text":"x","reply_to_message_id":-1}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			// A nil resolved connection would fail with "no connection"; validation must fire first.
			_, err := invokeMessagesSend(context.Background(), nil, nil, nil, json.RawMessage(args))
			var pe *provider.Error
			if err == nil || !errors.As(err, &pe) || strings.Contains(pe.Message, "no connection") {
				t.Errorf("err = %v", err)
			}
		})
	}
	_, err := invokeMessagesEditReplyMarkup(context.Background(), nil, nil, nil, json.RawMessage(`{"message_id":1,"inline_keyboard":[[]]}`))
	if err == nil || strings.Contains(err.Error(), "no connection") {
		t.Errorf("edit_reply_markup err = %v", err)
	}
}

func TestEditReplyMarkupRejectsUnboundChatBeforeSecret(t *testing.T) {
	_, err := invokeMessagesEditReplyMarkup(context.Background(), resolvedWith("-1001"), nil, nil,
		json.RawMessage(`{"chat":"-2002","message_id":1}`))
	if err == nil || strings.Contains(err.Error(), "-2002") {
		t.Errorf("err = %v", err)
	}
}

func TestEditReplyMarkupSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	for _, status := range []int{500, 502} {
		client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
		_, err := client.EditReplyMarkup(context.Background(), 91, nil)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
			t.Errorf("status %d err = %v", status, err)
		}
		if len(*bodies) != 1 {
			t.Errorf("requests = %d, want 1", len(*bodies))
		}
	}
	calls := 0
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &timeoutError{}
	}))
	_, err := client.EditReplyMarkup(context.Background(), 91, nil)
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
		t.Errorf("timeout err = %v calls = %d", err, calls)
	}
}

func TestParsingRejectionOmitsDescription(t *testing.T) {
	client, _, _ := capture(t, "-1001", 400,
		`{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Unsupported start tag"}`)
	_, err := client.Send(context.Background(), SendOptions{Text: "<x>", ParseMode: "HTML"})
	if err == nil || strings.Contains(err.Error(), "parse entities") || strings.Contains(err.Error(), "start tag") {
		t.Errorf("err = %v", err)
	}
}
