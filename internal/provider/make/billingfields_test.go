package makeapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const richSubscription = `{"product":{"id":1,"name":"FREE","nextBill":"2021-11-26T13:31:00.082Z",` +
	`"price":{"id":9007199254740993,"price":"0.10","currencyCode":"USD","period":"1 month"},` +
	`"coupon":{"validFrom":"2021-12-12T10:47:05.334Z","validTo":"2021-12-19T10:47:17.768Z","price":"3.00",` +
	`"currencyCode":"usd","productId":4,"productName":"Teams"}},` +
	`"pauseStartsAt":"2021-12-01T00:00:00Z","pauseEndsAt":"2021-12-31T00:00:00Z","isSubscriptionPaused":true}`

const richPayments = `{"payments":[{"id":"in_1JqwmsAtYi1J9zZU7sIuH39K","invoice_number":"6B52691A-0002",` +
	`"created":"2021-10-25T13:42:55.110Z","type_name":"Invoice","status_name":"Pending",` +
	`"product_name":{"cs":"FREE-PRODUCT","en":"FREE-PRODUCT"},` +
	`"payment_method":{"type":"card","card":{"brand":"visa","last4":"4242","fingerprint":"pxynXFWHTejV4FMT"}},` +
	`"amount_total":"666.00","currency_code":"usd","invoice_url":"https://files.example/invoice.pdf?sig=SECRETSIG",` +
	`"hosted_invoice_url":"https://hosted.example/i/HOSTEDTOKEN","period_from":"2021-11-01T14:40:07.000Z",` +
	`"period_to":"2021-11-02T14:40:07.000Z"},` +
	`{"id":77,"invoice_number":"1002","amount_total":12.5},{"id":78,"invoice_number":1003}]}`

var billingLeaks = []string{"4242", "visa", "pxynXFWHTejV4FMT", "SECRETSIG", "HOSTEDTOKEN", "files.example",
	"hosted.example", "FREE-PRODUCT", "6B52691A"}

func TestOrganizationSubscriptionReportsPriceAndCouponExactly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, richSubscription), nil
	})
	got, err := env.invoke(organizationSubscription.ID, "orgmode", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if seen := assertOutputDeclared(t, organizationSubscription.ID, []byte(got)); seen < 17 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"plan_id": "1", "plan_name": "FREE", "next_bill": "2021-11-26T13:31:00.082Z",
		"is_paused": true, "price": "0.10", "price_currency": "USD", "price_period": "1 month",
		"coupon_valid_from": "2021-12-12T10:47:05.334Z", "coupon_valid_to": "2021-12-19T10:47:17.768Z",
		"coupon_price": "3.00", "coupon_currency": "usd", "coupon_product_name": "Teams",
		"pause_starts_at": "2021-12-01T00:00:00Z", "pause_ends_at": "2021-12-31T00:00:00Z"} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	if !strings.Contains(got, `"price_id":9007199254740993`) || !strings.Contains(got, `"coupon_product_id":4`) {
		t.Errorf("ids not exact: %s", got)
	}
}

func TestOrganizationSubscriptionWithoutPriceKeepsTheOldOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"product":{"id":"p1","name":"Pro","nextBill":"2026-11-01","price":7,"coupon":null},`+
			`"pauseStartsAt":null,"isSubscriptionPaused":false}`), nil
	})
	got, err := env.invoke(organizationSubscription.ID, "orgmode", `{}`)
	want := `{"next_bill":"2026-11-01","organization_id":` + itoa64(ownOrg) + `,"plan_id":"p1","plan_name":"Pro"}`
	if err != nil || got != want {
		t.Fatalf("got %s, %v; want %s", got, err, want)
	}
}

func TestOrganizationPaymentsVersion2TypesAndHiddenValues(t *testing.T) {
	if organizationPayments.Version != 2 {
		t.Fatalf("version = %d", organizationPayments.Version)
	}
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, richPayments), nil
	})
	got, err := env.invoke(organizationPayments.ID, "orgmode", `{"sort_dir":"asc"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range billingLeaks {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %q: %s", bad, got)
		}
	}
	if seen := assertOutputDeclared(t, organizationPayments.ID, []byte(got)); seen < 12 {
		t.Errorf("checked %d members: %s", seen, got)
	}
	var out struct {
		Payments []map[string]any `json:"payments"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil || len(out.Payments) != 3 {
		t.Fatalf("payments = %s, %v", got, err)
	}
	first := out.Payments[0]
	if first["id"] != "in_1JqwmsAtYi1J9zZU7sIuH39K" || first["amount_total"] != "666.00" ||
		first["currency"] != "usd" || first["type"] != "Invoice" {
		t.Errorf("first = %v", first)
	}
	if _, ok := first["invoice_number"]; ok {
		t.Errorf("a non-numeric invoice number must be left out: %v", first)
	}
	if out.Payments[1]["id"] != "77" || out.Payments[1]["invoice_number"] != 1002.0 ||
		out.Payments[1]["amount_total"] != "12.5" || out.Payments[2]["invoice_number"] != 1003.0 {
		t.Errorf("tolerated forms = %v", out.Payments)
	}
	if calls[0].query.Get("pg[sortDir]") != "asc" || calls[0].query.Get("pg[sortBy]") != "created" {
		t.Errorf("query = %v", calls[0].query)
	}
}

func TestBillingFiltersAreValidatedBeforeSecretsAndRequests(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"payments":[]}`), nil
	})
	for _, c := range []struct{ id, connection, args string }{
		{organizationPayments.ID, "orgmode", `{"sort_dir":"up"}`},
		{organizationPayments.ID, "orgmode", `{"sort_dir":1}`},
		{organizationPayments.ID, "orgmode", `{"sort_by":"id"}`},
		{organizationUsage.ID, "orgmode", `{"organization_timezone":"yes"}`},
		{organizationUsage.ID, "orgmode", `{"organization_timezone":1}`},
		{teamUsage.ID, "open", `{"organization_timezone":"true"}`},
		{teamUsage.ID, "open", `{"organizationTimezone":true}`},
	} {
		if _, err := env.invoke(c.id, c.connection, c.args); err == nil {
			t.Errorf("%s accepted %s", c.id, c.args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d", calls, *env.reads)
	}
}

func TestUsageToolsSendOrganizationTimezone(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[{"date":"2024-05-30","operations":9,"dataTransfer":135,"centicredits":900}]}`), nil
	})
	for _, c := range []struct{ id, connection string }{{teamUsage.ID, "open"}, {organizationUsage.ID, "orgmode"}} {
		for _, args := range []string{`{}`, `{"organization_timezone":true}`, `{"organization_timezone":false}`} {
			got, err := env.invoke(c.id, c.connection, args)
			if err != nil || !strings.Contains(got, `"centicredits":900`) {
				t.Fatalf("%s %s = %s, %v", c.id, args, got, err)
			}
			assertOutputDeclared(t, c.id, []byte(got))
		}
	}
	want := []string{"", "true", "false", "", "true", "false"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i, c := range calls {
		_, present := c.query["organizationTimezone"]
		if c.query.Get("organizationTimezone") != want[i] || present != (want[i] != "") {
			t.Errorf("call %d query = %v, want organizationTimezone %q", i, c.query, want[i])
		}
	}
}
