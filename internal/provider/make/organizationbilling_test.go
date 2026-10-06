package makeapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestOrganizationBillingToolsRefuseTeamModeBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return nil, errors.New("no") })
	for _, id := range []string{organizationSubscription.ID, organizationUsage.ID, organizationPayments.ID} {
		if _, err := env.invoke(id, "team", `{}`); err == nil {
			t.Fatalf("%s accepted a team connection", id)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOrganizationBillingReadsTheBoundOrganizationAndDropsPaymentDetails(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/subscription"):
			return jsonResponse(200, `{"product":{"id":"p1","name":"Pro","nextBill":"2026-11-01","price":{"x":1},`+
				`"card":"4111111111111111"},"isSubscriptionPaused":false,"iban":"DE00"}`), nil
		case strings.HasSuffix(r.URL.Path, "/usage"):
			return jsonResponse(200, `{"data":[{"date":"2026-10-01","operations":5,"dataTransfer":7,"centicredits":9}]}`), nil
		}
		return jsonResponse(200, `{"payments":[{"id":3,"invoice_number":"INV-1","created":"2026-10-01","type_name":"Sub",`+
			`"status_name":"paid","payment_method":{"card":"4111111111111111","iban":"DE00"},"amount_total":1200,`+
			`"currency_code":"EUR","invoice_url":"https://pay.example/x","period_from":"a","period_to":"b"},`+
			`{"id":4},{"id":5}]}`), nil
	})
	for _, id := range []string{organizationSubscription.ID, organizationUsage.ID, organizationPayments.ID} {
		args := `{}`
		if id == organizationPayments.ID {
			args = `{"offset":5,"limit":2}`
		}
		got, err := env.invoke(id, "orgmode", args)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, bad := range []string{"4111", "DE00", "pay.example", "card", "iban", "price"} {
			if strings.Contains(got, bad) {
				t.Fatalf("%s leaked %q: %s", id, bad, got)
			}
		}
		if id == organizationPayments.ID && (!strings.Contains(got, `"amount_total":"1200"`) ||
			strings.Contains(got, `"id":5`) || !strings.Contains(got, `"has_more":true`)) {
			t.Fatalf("payments = %s", got)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if c.method != http.MethodGet || !strings.Contains(c.path, "/organizations/"+itoa64(ownOrg)+"/") {
			t.Fatalf("call = %+v", c)
		}
	}
	if calls[2].query.Get("pg[offset]") != "5" || calls[2].query.Get("pg[limit]") != "2" {
		t.Fatalf("query = %v", calls[2].query)
	}
	for _, bad := range []string{`{"limit":101}`, `{"limit":0}`, `{"offset":-1}`, `{"organization_id":9}`} {
		if _, err := env.invoke(organizationPayments.ID, "orgmode", bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("invalid arguments sent a request: %+v", calls)
	}
}
