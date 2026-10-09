package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const webhookCanary = "https://canary.example/hook-SECRETPATH"

func botFake(calls *[]recorded, status int, getMe, webhook string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		*calls = append(*calls, recorded{method: method})
		switch method {
		case "getMe":
			return response(status, getMe), nil
		case "getWebhookInfo":
			return response(status, webhook), nil
		}
		return response(404, `{"ok":false}`), nil
	}
}

func TestBotGetReturnsOnlyAllowListedFields(t *testing.T) {
	var calls []recorded
	body := `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Helper","username":"helper_bot",` +
		`"can_join_groups":true,"can_read_all_group_messages":false,"supports_inline_queries":true,` +
		`"has_main_web_app":true,"EXTRA":"LEAKEDFIELD"}}`
	client, _ := multiClient(t, testToken, botFake(&calls, 200, body, ""), "-100")
	got, err := client.GetBot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getMe" {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	want := `{"id":42,"username":"helper_bot","first_name":"Helper","can_join_groups":true,` +
		`"can_read_all_group_messages":false,"supports_inline_queries":true}`
	if string(encoded) != want {
		t.Errorf("output = %s", encoded)
	}
}

func TestBotGetWorksWithChatTargetOnly(t *testing.T) {
	var calls []recorded
	client, resolved := multiClient(t, testToken,
		botFake(&calls, 200, `{"ok":true,"result":{"id":1,"first_name":"B"}}`, ""), "-100")
	if _, err := client.GetBot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if set, _ := targetsOf(resolved); set.bot {
		t.Fatal("test setup binds the bot target")
	}
}

func TestWebhookGetDerivesHasWebhookAndHidesDetails(t *testing.T) {
	info := func(url string) string {
		return `{"ok":true,"result":{"url":"` + url + `","pending_update_count":3,"last_error_date":1700,` +
			`"last_error_message":"LEAKEDMESSAGE","ip_address":"203.0.113.9","allowed_updates":["message"]}}`
	}
	for _, tt := range []struct {
		name, url string
		want      string
	}{
		{"active", webhookCanary, `{"has_webhook":true,"pending_update_count":3,"last_error_date":1700}`},
		{"none", "", `{"has_webhook":false,"pending_update_count":3,"last_error_date":1700}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []recorded
			client, _ := multiClient(t, testToken, botFake(&calls, 200, "", info(tt.url)), "bot")
			got, err := client.GetWebhook(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0].method != "getWebhookInfo" {
				t.Fatalf("calls = %+v", calls)
			}
			encoded, _ := json.Marshal(got)
			if string(encoded) != tt.want {
				t.Errorf("output = %s", encoded)
			}
		})
	}
	var calls []recorded
	client, _ := multiClient(t, testToken, botFake(&calls, 200, "",
		`{"ok":true,"result":{"url":"","pending_update_count":0}}`), "bot")
	got, _ := client.GetWebhook(context.Background())
	if encoded, _ := json.Marshal(got); string(encoded) != `{"has_webhook":false,"pending_update_count":0}` {
		t.Errorf("output = %s", encoded)
	}
}

func TestWebhookGetRefusedWithoutBotTargetBeforeSecretAccess(t *testing.T) {
	accessed := false
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, &redact.Redactor{})
	_, err := invokeWebhookGet(context.Background(), resolvedWith("-100"), resolver, &redact.Redactor{}, json.RawMessage(`{}`))
	if err == nil || accessed {
		t.Fatalf("err = %v, secret accessed = %v", err, accessed)
	}
}

func TestBotToolsErrorsCarryNoTelegramText(t *testing.T) {
	bodies := `{"ok":false,"error_code":400,"description":"LEAKEDDESCRIPTION ` + webhookCanary + `"}`
	for _, status := range []int{400, 401, 500} {
		var calls []recorded
		client, _ := multiClient(t, testToken, botFake(&calls, status, bodies, bodies), "bot")
		_, err1 := client.GetBot(context.Background())
		_, err2 := client.GetWebhook(context.Background())
		for _, err := range []error{err1, err2} {
			if err == nil || strings.Contains(err.Error(), "LEAKEDDESCRIPTION") || strings.Contains(err.Error(), "canary.example") {
				t.Errorf("status %d error = %v", status, err)
			}
		}
	}
}
