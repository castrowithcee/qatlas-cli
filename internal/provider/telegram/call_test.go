package telegram

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestToolsPostExactBodyToFixedMethod(t *testing.T) {
	const target = "@alerts_channel"
	tests := []struct {
		name, path, body, reply string
		run                     func(*Client) error
	}{
		{"send", "sendMessage", `{"chat_id":"@alerts_channel","text":"hi"}`,
			`{"ok":true,"result":{"message_id":5,"date":9}}`,
			func(c *Client) error { _, err := c.SendMessage(context.Background(), "hi"); return err }},
		{"edit", "editMessageText", `{"chat_id":"@alerts_channel","message_id":5,"text":"new"}`,
			`{"ok":true,"result":{"message_id":5}}`,
			func(c *Client) error { _, err := c.EditMessage(context.Background(), 5, "new"); return err }},
		{"delete", "deleteMessage", `{"chat_id":"@alerts_channel","message_id":5}`,
			`{"ok":true,"result":true}`,
			func(c *Client) error { _, err := c.DeleteMessage(context.Background(), 5); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, _ := telegramClient(t, target, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || r.URL.Path != "/bot"+testToken+"/"+tt.path || string(raw) != tt.body {
					t.Errorf("request = %s %s %s", r.Method, r.URL.Path, raw)
				}
				return response(http.StatusOK, tt.reply), nil
			}))
			if err := tt.run(client); err != nil || calls != 1 {
				t.Fatalf("err = %v, requests = %d", err, calls)
			}
		})
	}
}

func TestCallMultipartSendsOneFormBody(t *testing.T) {
	calls := 0
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || r.URL.Path != "/bot"+testToken+"/sendDocument" {
			t.Fatalf("request = %s %v", r.URL.Path, r.Header)
		}
		form := multipart.NewReader(r.Body, params["boundary"])
		got := map[string]string{}
		for {
			part, err := form.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			got[part.FormName()+"|"+part.FileName()] = string(data)
		}
		if len(got) != 2 || got["chat_id|"] != "-1001" || got["document|a.txt"] != "payload" {
			t.Errorf("parts = %#v", got)
		}
		return response(http.StatusOK, `{"ok":true,"result":{"message_id":1}}`), nil
	}))
	raw, err := client.callMultipart(context.Background(),
		spec{op: "send document", method: "sendDocument", limit: defaultResponseBytes},
		[]field{{"chat_id", "-1001"}}, []filePart{{field: "document", name: "a.txt", data: []byte("payload")}})
	if err != nil || !strings.Contains(string(raw), "message_id") || calls != 1 {
		t.Fatalf("callMultipart() = %s, %v, requests = %d", raw, err, calls)
	}
}

func TestCallMultipartRefusesOversizedUploadBeforeIO(t *testing.T) {
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request sent")
		return nil, nil
	}))
	_, err := client.callMultipart(context.Background(), spec{op: "x", method: "sendDocument", limit: 10}, nil,
		[]filePart{{field: "document", name: "a", data: make([]byte, maxUploadBytes+1)}})
	if err == nil {
		t.Fatal("callMultipart() = nil, want error")
	}
}

func TestErrorMappingKeepsProviderTextOut(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   provider.Class
		in     string
	}{
		{"bad request", 400, `{"ok":false,"error_code":400,"description":"SECRET-DESC"}`, provider.ClassProviderError, "HTTP 400"},
		{"auth", 401, `{"ok":false,"description":"SECRET-DESC"}`, provider.ClassAuth, ""},
		{"permission", 403, `{"ok":false,"description":"SECRET-DESC"}`, provider.ClassPermission, ""},
		{"conflict", 409, `{"ok":false,"description":"SECRET-DESC"}`, provider.ClassProviderError, "conflict"},
		{"rate limit", 429, `{"ok":false,"description":"SECRET-DESC","parameters":{"retry_after":7}}`, provider.ClassRateLimited, ""},
		{"retry after on 400", 400, `{"ok":false,"parameters":{"retry_after":7}}`, provider.ClassRateLimited, ""},
		{"migration", 400, `{"ok":false,"description":"SECRET-DESC","parameters":{"migrate_to_chat_id":-1009998887776}}`,
			provider.ClassProviderError, "migrated to a supergroup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(tt.status, tt.body), nil
			}))
			_, err := client.DeleteMessage(context.Background(), 1)
			var providerErr *provider.Error
			if !errors.As(err, &providerErr) || providerErr.Class != tt.want || !strings.Contains(err.Error(), tt.in) {
				t.Fatalf("err = %v, want class %q containing %q", err, tt.want, tt.in)
			}
			for _, leak := range []string{"SECRET-DESC", "9998887776"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error leaks %q: %v", leak, err)
				}
			}
			if strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("a clean refusal is reported as uncertain: %v", err)
			}
			if calls != 1 {
				t.Fatalf("requests = %d, want 1", calls)
			}
		})
	}
}

func TestUnclearMutationOutcomeIsReportedAndNeverRetried(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"timeout", 0, "", &timeoutError{}},
		{"abort", 0, "", errors.New("aborted after write")},
		{"server error", 502, `{"ok":false}`, nil},
		{"unreadable body", 200, `{not-json`, nil},
		{"oversized body", 200, strings.Repeat("x", defaultResponseBytes+1), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tt.err != nil {
					return nil, tt.err
				}
				return response(tt.status, tt.body), nil
			}))
			_, err := client.SendMessage(context.Background(), "one")
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
				t.Fatalf("err = %v, requests = %d", err, calls)
			}
		})
	}
}

func TestValidateBaseURLMatchesOpen(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.telegram.org":     true,
		"https://proxy.example/tg":     true,
		"http://api.telegram.org":      false,
		"http://127.0.0.1:8080":        false,
		"http://localhost":             false,
		"https://user@127.0.0.1":       false,
		"https://api.telegram.org?x=1": false,
		"https://api.telegram.org#x":   false,
		"https://":                     false,
		"":                             false,
	} {
		if _, err := parseBase(raw); (err == nil) != ok {
			t.Errorf("parseBase(%q) err = %v, want valid=%t", raw, err, ok)
		}
	}
}
