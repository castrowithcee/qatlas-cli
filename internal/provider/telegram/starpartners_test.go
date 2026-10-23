package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func partnerTransactions() string {
	return `{"ok":true,"result":{"transactions":[` +
		`{"id":"t1","amount":10,"nanostar_amount":5,"date":99,"source":{"type":"user","transaction_type":"invoice_payment",` +
		`"user":{"id":42,"first_name":"HIDE","username":"HIDE"},"invoice_payload":"order\u0007-1",` +
		`"subscription_period":2592000,"affiliate":{"affiliate_user":{"id":8,"first_name":"HIDE"},` +
		`"affiliate_chat":{"id":-100,"title":"HIDE"},"commission_per_mille":50,"amount":-1,"nanostar_amount":-7}}},` +
		`{"id":"t2","amount":3,"date":1,"source":{"type":"user","transaction_type":"paid_media_payment","user":{"id":43},` +
		`"paid_media_payload":"pm","paid_media":[{"type":"preview","width":10,"height":20,"duration":3},` +
		`{"type":"photo","photo":[{"file_id":"PHOTOID","file_unique_id":"pu","width":5,"height":6,"file_size":7}]},` +
		`{"type":"video","video":{"file_id":"VIDEOID","file_unique_id":"vu","width":1,"height":2,"duration":3}}]}},` +
		`{"id":"t3","amount":-3,"date":2,"receiver":{"type":"user","transaction_type":"gift_purchase","user":{"id":44},` +
		`"gift":{"id":"g1","star_count":50,"sticker":{"file_id":"STICKERID","file_unique_id":"su","width":512,"height":512}}}},` +
		`{"id":"t4","amount":-4,"date":3,"receiver":{"type":"user","transaction_type":"premium_purchase","user":{"id":45},` +
		`"premium_subscription_duration":6}},` +
		`{"id":"t5","amount":-5,"date":4,"receiver":{"type":"chat","chat":{"id":-1002,"title":"HIDE"},` +
		`"gift":{"id":"g2","star_count":15}}},` +
		`{"id":"t6","amount":6,"date":5,"source":{"type":"affiliate_program","sponsor_user":{"id":9,"first_name":"HIDE"},` +
		`"commission_per_mille":300}},` +
		`{"id":"t7","amount":-7,"date":6,"receiver":{"type":"fragment","withdrawal_state":{"type":"succeeded","date":77,` +
		`"url":"https://example.invalid/tx/1"}}},` +
		`{"id":"t8","amount":-8,"date":7,"receiver":{"type":"fragment","withdrawal_state":{"type":"pending"}}},` +
		`{"id":"t9","amount":-9,"date":8,"receiver":{"type":"telegram_api","request_count":1234}},` +
		`{"id":"t10","amount":-10,"date":9,"receiver":{"type":"telegram_ads"}},` +
		`{"id":"t11","amount":11,"date":10,"source":{"type":"other"}}]}}`
}

func TestStarTransactionPartnersPrintEveryDocumentedField(t *testing.T) {
	e := newPayEnv(t, "bot")
	e.answers["getStarTransactions"] = partnerTransactions()
	got, err := e.run(invokePaymentsStarTransactionsWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(id string) string { return signRef(testToken, refFile, "bot", id) }
	want := `{"transactions":[` +
		`{"id":"t1","amount":10,"nanostar_amount":5,"date":99,"source":{"type":"user","id":42,"transaction_type":"invoice_payment",` +
		`"affiliate":{"affiliate_user":{"id":8},"affiliate_chat":{"id":-100},"commission_per_mille":50,"amount":-1,` +
		`"nanostar_amount":-7},"invoice_payload":"order-1","subscription_period":2592000}},` +
		`{"id":"t2","amount":3,"nanostar_amount":0,"date":1,"source":{"type":"user","id":43,"transaction_type":"paid_media_payment",` +
		`"paid_media":[{"type":"preview","width":10,"height":20,"duration":3},{"type":"photo","photo":[{"file_ref":"` +
		ref("PHOTOID") + `","file_unique_id":"pu","width":5,"height":6,"file_size":7}]},{"type":"video","video":{"kind":"video",` +
		`"file_ref":"` + ref("VIDEOID") + `","file_unique_id":"vu","width":1,"height":2,"duration":3}}],"paid_media_payload":"pm"}},` +
		`{"id":"t3","amount":-3,"nanostar_amount":0,"date":2,"receiver":{"type":"user","id":44,"transaction_type":"gift_purchase",` +
		`"gift":{"id":"g1","star_count":50,"sticker":{"kind":"sticker","file_ref":"` + ref("STICKERID") +
		`","file_unique_id":"su","width":512,"height":512}}}},` +
		`{"id":"t4","amount":-4,"nanostar_amount":0,"date":3,"receiver":{"type":"user","id":45,"transaction_type":"premium_purchase",` +
		`"premium_subscription_duration":6}},` +
		`{"id":"t5","amount":-5,"nanostar_amount":0,"date":4,"receiver":{"type":"chat","id":-1002,"gift":{"id":"g2","star_count":15}}},` +
		`{"id":"t6","amount":6,"nanostar_amount":0,"date":5,"source":{"type":"affiliate_program","sponsor_user":{"id":9},` +
		`"commission_per_mille":300}},` +
		`{"id":"t7","amount":-7,"nanostar_amount":0,"date":6,"receiver":{"type":"fragment","withdrawal_state":{"type":"succeeded",` +
		`"date":77,"url":"https://example.invalid/tx/1"}}},` +
		`{"id":"t8","amount":-8,"nanostar_amount":0,"date":7,"receiver":{"type":"fragment","withdrawal_state":{"type":"pending"}}},` +
		`{"id":"t9","amount":-9,"nanostar_amount":0,"date":8,"receiver":{"type":"telegram_api","request_count":1234}},` +
		`{"id":"t10","amount":-10,"nanostar_amount":0,"date":9,"receiver":{"type":"telegram_ads"}},` +
		`{"id":"t11","amount":11,"nanostar_amount":0,"date":10,"source":{"type":"other"}}]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, leak := range []string{"HIDE", "PHOTOID", "VIDEOID", "STICKERID", "first_name", "title"} {
		if strings.Contains(got, leak) {
			t.Errorf("%s leaked: %s", leak, got)
		}
	}
	if len(e.calls) != 1 {
		t.Errorf("requests = %d, want 1", len(e.calls))
	}
}

func TestStarTransactionPartnerBoundsUntrustedText(t *testing.T) {
	long := strings.Repeat("x", maxURLRunes+50)
	e := newPayEnv(t, "bot")
	e.answers["getStarTransactions"] = `{"ok":true,"result":{"transactions":[{"id":"t","amount":1,"date":1,"source":` +
		`{"type":"user","user":{"id":1},"invoice_payload":"` + long + `","paid_media_payload":"` + long + `"},"receiver":` +
		`{"type":"fragment","withdrawal_state":{"type":"succeeded","url":"` + long + `"}}}]}}`
	got, err := e.run(invokePaymentsStarTransactionsWith, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Transactions []struct {
			Source, Receiver map[string]any
		}
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	s, r := out.Transactions[0].Source, out.Transactions[0].Receiver
	if n := len(s["invoice_payload"].(string)); n != maxNameRunes {
		t.Errorf("invoice_payload = %d", n)
	}
	if n := len(s["paid_media_payload"].(string)); n != maxNameRunes {
		t.Errorf("paid_media_payload = %d", n)
	}
	if n := len(r["withdrawal_state"].(map[string]any)["url"].(string)); n != maxURLRunes {
		t.Errorf("url = %d", n)
	}
}

// bigPartner is a user partner filled to its maximum: payloads, ten paid media with long identifiers, and a gift
// with a sticker.
func bigPartner(i int) string {
	id := func(n int) string { return strings.Repeat(string(rune('A'+n%26)), 90) }
	var media []string
	for k := 0; k < 10; k++ {
		media = append(media, fmt.Sprintf(`{"type":"video","video":{"file_id":"%s","file_unique_id":"%s","width":1,"height":2,`+
			`"duration":3,"file_name":"%s","mime_type":"video/mp4","file_size":9,"cover":[{"file_id":"%s","file_unique_id":"%s",`+
			`"width":1,"height":2}],"thumbnail":{"file_id":"%s","file_unique_id":"%s","width":1,"height":2}}}`,
			id(k+i), id(k)[:16], strings.Repeat("n", 200), id(k+1), id(k + 1)[:16], id(k+2), id(k + 2)[:16]))
	}
	return fmt.Sprintf(`{"type":"user","transaction_type":"paid_media_payment","user":{"id":%d},"invoice_payload":"%s",`+
		`"paid_media_payload":"%s","subscription_period":1,"paid_media":[%s],"gift":{"id":"%s","star_count":50,`+
		`"sticker":{"file_id":"%s","file_unique_id":"%s","width":512,"height":512,"thumbnail":{"file_id":"%s",`+
		`"file_unique_id":"%s","width":3,"height":3},"premium_animation":{"file_id":"%s","file_unique_id":"%s"}}},`+
		`"affiliate":{"affiliate_user":{"id":1},"commission_per_mille":5,"amount":1}}`,
		i, strings.Repeat("p", 300), strings.Repeat("q", 300), strings.Join(media, ","), id(i),
		id(i+1), id(i + 1)[:16], id(i+2), id(i + 2)[:16], id(i+3), id(i + 3)[:16])
}

func TestFullPageOfStarTransactionsFitsTheResponseLimit(t *testing.T) {
	var list []string
	for i := 0; i < maxGiftsLimit; i++ {
		list = append(list, fmt.Sprintf(`{"id":"%s","amount":1,"nanostar_amount":1,"date":1,"source":%s,"receiver":%s}`,
			strings.Repeat("i", maxNameRunes), bigPartner(i), bigPartner(i+1)))
	}
	body := `{"ok":true,"result":{"transactions":[` + strings.Join(list, ",") + `]}}`
	t.Logf("answer of %d transactions with two full partners each: %d bytes (limit %d)", maxGiftsLimit, len(body), starTransactionsResponseMax)
	if len(body) > starTransactionsResponseMax {
		t.Fatalf("answer of %d bytes exceeds the limit", len(body))
	}
	e := newPayEnv(t, "bot")
	e.answers["getStarTransactions"] = body
	got, err := e.run(invokePaymentsStarTransactionsWith, `{"limit":100}`)
	if err != nil || strings.Contains(got, "omitted_fields") {
		t.Fatalf("err = %v, omitted = %v", err, strings.Contains(got, "omitted_fields"))
	}
}
