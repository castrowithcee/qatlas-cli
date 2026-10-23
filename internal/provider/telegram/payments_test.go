package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type payEnv struct {
	t        *testing.T
	resolved *config.Resolved
	secrets  *secret.Resolver
	lookups  int
	calls    []recorded
	answers  map[string]string
}

func newPayEnv(t *testing.T, targets ...string) *payEnv {
	e := &payEnv{t: t, resolved: resolvedWith(targets...), answers: map[string]string{}}
	e.secrets = secret.NewWith(func(name string) string {
		e.lookups++
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return testToken
		}
		return ""
	}, nil, nil, &redact.Redactor{})
	return e
}

func (e *payEnv) client() *http.Client {
	c := newHTTPClient()
	c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		e.calls = append(e.calls, recorded{name, string(data)})
		if a, ok := e.answers[name]; ok {
			return response(200, a), nil
		}
		return response(200, `{"ok":true,"result":{}}`), nil
	})
	return c
}

type payInvoke func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage,
	*http.Client) (any, error)

func (e *payEnv) run(invoke payInvoke, args string) (string, error) {
	out, err := invoke(context.Background(), e.resolved, e.secrets, &redact.Redactor{}, json.RawMessage(args), e.client())
	data, _ := json.Marshal(out)
	return string(data), err
}

const memberOK = `{"ok":true,"result":{"status":"member","user":{"id":7}}}`

func TestPaymentsToolsSendExactRequests(t *testing.T) {
	gifts := `{"ok":true,"result":{"total_count":1,"next_offset":"n","gifts":[{"type":"regular","owned_gift_id":"OWN",` +
		`"send_date":5,"is_saved":true,"text":"hi","sender_user":{"id":3,"first_name":"A","phone":"HIDE"},` +
		`"convert_star_count":4,"transfer_star_count":9,"can_be_upgraded":true,` +
		`"gift":{"id":"g1","star_count":50,"upgrade_star_count":99,"sticker":{"file_id":"HIDE"}}}]}}`
	tests := []struct {
		name    string
		invoke  payInvoke
		targets []string
		args    string
		method  string
		body    string
		answer  string
		want    string
	}{
		{"balance", invokePaymentsStarBalanceWith, []string{"bot"}, `{}`, "getMyStarBalance", `{}`,
			`{"ok":true,"result":{"amount":5,"nanostar_amount":6,"x":"HIDE"}}`, `{"amount":5,"nanostar_amount":6}`},
		{"transactions", invokePaymentsStarTransactionsWith, []string{"bot"}, `{"offset":2,"limit":3}`,
			"getStarTransactions", `{"offset":2,"limit":3}`,
			`{"ok":true,"result":{"transactions":[{"id":"t1","amount":10,"nanostar_amount":1,"date":99,` +
				`"source":{"type":"user","user":{"id":42,"first_name":"HIDE","username":"HIDE"},"invoice_payload":"HIDE"},` +
				`"receiver":{"type":"fragment","withdrawal_state":{"url":"HIDE"}},"paid_media":[{"x":"HIDE"}]},` +
				`{"id":"t2","amount":1,"date":1,"source":{"type":"chat","chat":{"id":-100,"title":"HIDE"}}}]}}`,
			`{"transactions":[{"id":"t1","amount":10,"nanostar_amount":1,"date":99,"source":{"type":"user","id":42},` +
				`"receiver":{"type":"fragment"}},{"id":"t2","amount":1,"nanostar_amount":0,"date":1,` +
				`"source":{"type":"chat","id":-100}}]}`},
		{"transactions default", invokePaymentsStarTransactionsWith, []string{"bot"}, `{}`, "getStarTransactions", `{}`,
			`{"ok":true,"result":{"transactions":[]}}`, `{"transactions":[]}`},
		{"available", invokeGiftsAvailableWith, []string{"bot"}, `{}`, "getAvailableGifts", `{}`,
			`{"ok":true,"result":{"gifts":[{"id":"a","star_count":15,"upgrade_star_count":7,"total_count":100,` +
				`"remaining_count":3,"sticker":{"file_id":"HIDE"}}]}}`,
			`{"gifts":[{"gift_id":"a","star_count":15,"total_count":100,"remaining_count":3,"sticker":{"kind":"sticker",` +
				`"file_ref":"` + signRef(testToken, refFile, "bot", "HIDE") + `"},"upgrade_star_count":7}]}`},
		{"chat", invokeGiftsChatWith, []string{"-1001"}, `{"limit":5,"offset":"o"}`, "getChatGifts",
			`{"chat_id":"-1001","offset":"o","limit":5}`, gifts,
			`{"total_count":1,"next_offset":"n","gifts":[{"type":"regular","gift_id":"g1","send_date":5,"is_saved":true,` +
				`"text":"hi","sender":{"id":3,"is_bot":false,"first_name":"A"},"star_count":50,"convert_star_count":4,` +
				`"owned_gift_id":"OWN","can_be_upgraded":true,"gift_details":{"id":"g1","star_count":50,` +
				`"sticker":{"kind":"sticker","file_ref":"` + signRef(testToken, refFile, "-1001", "HIDE") + `"},` +
				`"upgrade_star_count":99}}]}`},
		{"chat default", invokeGiftsChatWith, []string{"-1001"}, `{}`, "getChatGifts", `{"chat_id":"-1001"}`,
			`{"ok":true,"result":{"total_count":0,"gifts":[]}}`, `{"total_count":0,"gifts":[]}`},
		{"user", invokeGiftsUserWith, []string{"-1001"}, `{"user_id":7,"limit":100}`, "getUserGifts",
			`{"user_id":7,"limit":100}`, gifts,
			`{"total_count":1,"next_offset":"n","gifts":[{"type":"regular","gift_id":"g1","send_date":5,"is_saved":true,` +
				`"text":"hi","sender":{"id":3,"is_bot":false,"first_name":"A"},"star_count":50,"convert_star_count":4,` +
				`"owned_gift_id":"OWN","can_be_upgraded":true,"gift_details":{"id":"g1","star_count":50,` +
				`"sticker":{"kind":"sticker","file_ref":"` + signRef(testToken, refFile, "-1001", "HIDE") + `"},` +
				`"upgrade_star_count":99}}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newPayEnv(t, tc.targets...)
			e.answers[tc.method] = tc.answer
			e.answers["getChatMember"] = memberOK
			got, err := e.run(tc.invoke, tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("got %s, %v, want %s", got, err, tc.want)
			}
			last := e.calls[len(e.calls)-1]
			if last.method != tc.method || last.body != tc.body {
				t.Errorf("request = %+v, want %s %s", last, tc.method, tc.body)
			}
			if strings.Contains(got, "HIDE") || strings.Contains(got, "transfer_star_count") {
				t.Errorf("foreign field leaked: %s", got)
			}
		})
	}
}

func TestPaymentsBotToolsRefuseWithoutBotTarget(t *testing.T) {
	for name, invoke := range map[string]payInvoke{
		"balance": invokePaymentsStarBalanceWith, "transactions": invokePaymentsStarTransactionsWith,
		"available": invokeGiftsAvailableWith,
	} {
		e := newPayEnv(t, "-1001")
		if _, err := e.run(invoke, `{}`); err == nil || e.lookups != 0 || len(e.calls) != 0 {
			t.Errorf("%s: err = %v, lookups = %d, calls = %d", name, err, e.lookups, len(e.calls))
		}
	}
}

func TestGiftsRefuseForeignChatBeforeIO(t *testing.T) {
	for name, tc := range map[string]struct {
		invoke payInvoke
		args   string
	}{
		"chat":     {invokeGiftsChatWith, `{"chat":"-9999"}`},
		"user":     {invokeGiftsUserWith, `{"chat":"-9999","user_id":7}`},
		"bot only": {invokeGiftsChatWith, `{}`},
	} {
		e := newPayEnv(t, "-1001")
		if name == "bot only" {
			e = newPayEnv(t, "bot")
		}
		_, err := e.run(tc.invoke, tc.args)
		if err == nil || e.lookups != 0 || len(e.calls) != 0 || strings.Contains(err.Error(), "-9999") {
			t.Errorf("%s: err = %v, lookups = %d, calls = %d", name, err, e.lookups, len(e.calls))
		}
	}
}

func TestGiftsUserChecksMembershipFirst(t *testing.T) {
	e := newPayEnv(t, "-1001")
	e.answers["getChatMember"] = `{"ok":true,"result":{"status":"left","user":{"id":7}}}`
	if _, err := e.run(invokeGiftsUserWith, `{"user_id":7}`); err == nil || len(e.calls) != 1 ||
		e.calls[0].method != "getChatMember" || e.calls[0].body != `{"chat_id":"-1001","user_id":7}` {
		t.Fatalf("non-member: err = %v, calls = %+v", err, e.calls)
	}
	e = newPayEnv(t, "-1001")
	e.answers["getChatMember"] = memberOK
	e.answers["getUserGifts"] = `{"ok":true,"result":{"total_count":0,"gifts":[]}}`
	if _, err := e.run(invokeGiftsUserWith, `{"user_id":7}`); err != nil || len(e.calls) != 2 ||
		e.calls[0].method != "getChatMember" || e.calls[1].method != "getUserGifts" {
		t.Fatalf("member: err = %v, calls = %+v", err, e.calls)
	}
}

func TestPaymentsRejectInvalidPaging(t *testing.T) {
	long := strings.Repeat("x", maxGiftsOffset+1)
	for name, tc := range map[string]struct {
		invoke  payInvoke
		targets []string
		args    string
	}{
		"tx limit 0":   {invokePaymentsStarTransactionsWith, []string{"bot"}, `{"limit":0}`},
		"tx limit 101": {invokePaymentsStarTransactionsWith, []string{"bot"}, `{"limit":101}`},
		"tx offset -1": {invokePaymentsStarTransactionsWith, []string{"bot"}, `{"offset":-1}`},
		"tx extra":     {invokePaymentsStarTransactionsWith, []string{"bot"}, `{"method":"x"}`},
		"chat limit 0": {invokeGiftsChatWith, []string{"-1001"}, `{"limit":0}`},
		"chat limit":   {invokeGiftsChatWith, []string{"-1001"}, `{"limit":101}`},
		"chat offset":  {invokeGiftsChatWith, []string{"-1001"}, `{"offset":"` + long + `"}`},
		"user limit":   {invokeGiftsUserWith, []string{"-1001"}, `{"user_id":7,"limit":101}`},
		"user id":      {invokeGiftsUserWith, []string{"-1001"}, `{"user_id":0}`},
		"user extra":   {invokeGiftsUserWith, []string{"-1001"}, `{"user_id":7,"exclude_saved":true}`},
	} {
		e := newPayEnv(t, tc.targets...)
		if _, err := e.run(tc.invoke, tc.args); err == nil || e.lookups != 0 || len(e.calls) != 0 {
			t.Errorf("%s: err = %v, lookups = %d, calls = %d", name, err, e.lookups, len(e.calls))
		}
	}
}

func TestPaymentsErrorsOmitTelegramDescription(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"error_code":400,"description":"SECRET-DESC"}`), nil
	})
	client, _ := telegramClient(t, "-1001", transport)
	ctx := context.Background()
	_, e1 := client.GetStarBalance(ctx)
	_, e2 := client.ListStarTransactions(ctx, nil, nil)
	_, e3 := client.ListAvailableGifts(ctx)
	_, e4 := client.ListOwnedGifts(ctx, "list chat gifts", "getChatGifts", struct{}{})
	for i, err := range []error{e1, e2, e3, e4} {
		if err == nil || strings.Contains(err.Error(), "SECRET-DESC") {
			t.Errorf("error %d = %v", i, err)
		}
	}
}

func TestPaymentsDescriptorsAndProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	ids := []string{paymentsStarBalance.ID, paymentsStarTransactions.ID, giftsAvailable.ID, giftsChat.ID, giftsUser.ID}
	for _, id := range ids {
		d, _, ok := reg.Lookup(id)
		if !ok || d.Group != groupPayments || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Idempotency != capability.IdempotencySafe || d.Risk.Confirmation != capability.ConfirmationNone ||
			d.Risk.DataSensitivity != "telegram-financial" || d.RequiresToolAllowList {
			t.Errorf("%s: descriptor = %+v, found = %v", id, d, ok)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID == "payments-read" {
			if strings.Join(profile.Tools, ",") != strings.Join(ids, ",") {
				t.Errorf("payments-read tools = %v", profile.Tools)
			}
			continue
		}
		for _, tool := range profile.Tools {
			for _, id := range ids {
				if tool == id {
					t.Errorf("profile %s contains %s", profile.ID, id)
				}
			}
		}
	}
}
