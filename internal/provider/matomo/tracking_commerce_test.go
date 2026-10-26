package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestTrackingCommerceFormBodies(t *testing.T) {
	for _, tc := range []struct {
		name, id, arguments string
		want                map[string]string
		absent              []string
	}{
		{"goal", "matomo.tracking.record-goal", `{"idSite":1,"url":"https://example.org/thanks","goalId":3,"revenue":19.9}`,
			map[string]string{"idgoal": "3", "revenue": "19.9"}, []string{"ec_id", "ec_items", "ec_st"}},
		{"goal without revenue", "matomo.tracking.record-goal", `{"idSite":1,"url":"https://example.org/thanks","goalId":2}`,
			map[string]string{"idgoal": "2"}, []string{"revenue"}},
		{"order", "matomo.tracking.record-order", `{"idSite":1,"url":"https://example.org/done","orderId":"order-1001","revenue":59.8,` +
			`"subtotal":49.8,"tax":8,"shipping":10,"discount":0,"items":[` +
			`{"sku":"SKU-1","name":"Mug","categories":["Kitchen"],"price":24.9,"quantity":2},` +
			`{"sku":"SKU-2","categories":["A","B & C"]},{"sku":"SKU-3","price":0}]}`,
			map[string]string{"idgoal": "0", "ec_id": "order-1001", "revenue": "59.8", "ec_st": "49.8", "ec_tx": "8",
				"ec_sh": "10", "ec_dt": "0",
				"ec_items": `[["SKU-1","Mug","Kitchen",24.9,2],["SKU-2","",["A","B & C"],0,1],["SKU-3","","",0,1]]`}, nil},
		{"minimal order", "matomo.tracking.record-order", `{"idSite":1,"url":"https://example.org/done","orderId":"o","revenue":0}`,
			map[string]string{"idgoal": "0", "ec_id": "o", "revenue": "0"}, []string{"ec_items", "ec_st", "ec_tx", "ec_sh", "ec_dt"}},
		{"cart", "matomo.tracking.record-cart-update", `{"idSite":1,"url":"https://example.org/cart","revenue":24.9,` +
			`"items":[{"sku":"SKU-1","name":"Mug","price":24.9,"quantity":1}]}`,
			map[string]string{"idgoal": "0", "revenue": "24.9", "ec_items": `[["SKU-1","Mug","",24.9,1]]`},
			[]string{"ec_id", "ec_st", "ec_tx", "ec_sh", "ec_dt"}},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, noContent)
		if _, err := env.invoke(tc.id, "trk", tc.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: %v", tc.name, err)
		}
		if _, err := env.change(tc.id, "trk", tc.arguments); err != nil || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %d", tc.name, err, len(calls))
		}
		c := calls[0]
		if c.method != http.MethodPost || c.rawQuery != "" || c.auth != "" || c.path != "/matomo/matomo.php" {
			t.Errorf("%s: request = %+v", tc.name, c)
		}
		for name, value := range tc.want {
			if !c.form.Has(name) || c.form.Get(name) != value {
				t.Errorf("%s: %s = %q, want %q", tc.name, name, c.form.Get(name), value)
			}
		}
		for _, name := range tc.absent {
			if c.form.Has(name) {
				t.Errorf("%s sent %s", tc.name, name)
			}
		}
		for _, name := range []string{"token_auth", "cip", "cdt", "_ects"} {
			if c.form.Has(name) {
				t.Errorf("%s sent %s", tc.name, name)
			}
		}
		if strings.Contains(c.path+c.rawQuery+c.form.Encode(), tokenValue) || *env.reads != 0 {
			t.Errorf("%s leaked or read the token", tc.name)
		}
	}
}

func TestTrackingOrderDescriptionNamesTheForgeryRisk(t *testing.T) {
	d, _, found := registry(t).Lookup("matomo.tracking.record-order")
	if !found {
		t.Fatal("record-order is missing")
	}
	for _, text := range []string{"can be forged", "cannot be undone", "ec_id", "refuses an order ID"} {
		if !strings.Contains(strings.ReplaceAll(d.Description, "normally ", ""), text) {
			t.Errorf("description lacks %q", text)
		}
	}
}

func TestTrackingCommerceRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	const base = `"idSite":1,"url":"https://example.org/x"`
	items101 := "[" + strings.Repeat(`{"sku":"A"},`, 100) + `{"sku":"A"}]`
	for id, cases := range map[string]map[string]string{
		"matomo.tracking.record-goal": {
			"missing goal":  `{` + base + `}`,
			"goal zero":     `{` + base + `,"goalId":0}`,
			"goal negative": `{` + base + `,"goalId":-1}`,
			"negative":      `{` + base + `,"goalId":1,"revenue":-1}`,
			"too large":     `{` + base + `,"goalId":1,"revenue":1e13}`,
			"order id":      `{` + base + `,"goalId":1,"orderId":"x"}`,
		},
		"matomo.tracking.record-order": {
			"missing order id":  `{` + base + `,"revenue":1}`,
			"blank order id":    `{` + base + `,"orderId":"  ","revenue":1}`,
			"long order id":     `{` + base + `,"orderId":"` + strings.Repeat("a", 256) + `","revenue":1}`,
			"missing revenue":   `{` + base + `,"orderId":"o"}`,
			"negative revenue":  `{` + base + `,"orderId":"o","revenue":-0.01}`,
			"negative tax":      `{` + base + `,"orderId":"o","revenue":1,"tax":-1}`,
			"negative shipping": `{` + base + `,"orderId":"o","revenue":1,"shipping":-1}`,
			"negative subtotal": `{` + base + `,"orderId":"o","revenue":1,"subtotal":-1}`,
			"negative discount": `{` + base + `,"orderId":"o","revenue":1,"discount":-1}`,
			"empty items":       `{` + base + `,"orderId":"o","revenue":1,"items":[]}`,
			"too many items":    `{` + base + `,"orderId":"o","revenue":1,"items":` + items101 + `}`,
			"item without sku":  `{` + base + `,"orderId":"o","revenue":1,"items":[{"name":"n"}]}`,
			"blank sku":         `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":" "}]}`,
			"zero quantity":     `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","quantity":0}]}`,
			"fraction quantity": `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","quantity":1.5}]}`,
			"negative price":    `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","price":-1}]}`,
			"six categories":    `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","categories":["1","2","3","4","5","6"]}]}`,
			"long name":         `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","name":"` + strings.Repeat("a", 256) + `"}]}`,
			"unknown item key":  `{` + base + `,"orderId":"o","revenue":1,"items":[{"sku":"A","extra":1}]}`,
			"ects":              `{` + base + `,"orderId":"o","revenue":1,"_ects":1700000000}`,
		},
		"matomo.tracking.record-cart-update": {
			"missing revenue": `{` + base + `,"items":[{"sku":"A"}]}`,
			"missing items":   `{` + base + `,"revenue":1}`,
			"empty items":     `{` + base + `,"revenue":1,"items":[]}`,
			"negative":        `{` + base + `,"revenue":-1,"items":[{"sku":"A"}]}`,
			"order id":        `{` + base + `,"revenue":1,"orderId":"o","items":[{"sku":"A"}]}`,
			"too many items":  `{` + base + `,"revenue":1,"items":` + items101 + `}`,
		},
	} {
		for name, arguments := range cases {
			if _, err := env.change(id, "trk", arguments); err == nil {
				t.Errorf("%s %s was accepted", id, name)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func TestTrackingCommerceRefusesNonFiniteAmountsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	nan, inf := nonFinite()
	if err := commerceAmount(map[string][]string{}, "revenue", "revenue", &nan, true); err == nil {
		t.Error("NaN was accepted")
	}
	if err := commerceAmount(map[string][]string{}, "revenue", "revenue", &inf, true); err == nil {
		t.Error("Inf was accepted")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTrackingCommerceFailuresAreClassifiedAndNeverRetried(t *testing.T) {
	for _, id := range []string{"matomo.tracking.record-goal", "matomo.tracking.record-order", "matomo.tracking.record-cart-update"} {
		var arguments string
		for _, tool := range trackingTools {
			if tool.id == id {
				arguments = tool.arguments
			}
		}
		for name, tc := range map[string]struct {
			status  int
			body    string
			class   provider.Class
			invalid bool
		}{
			"400":  {http.StatusBadRequest, bodyCanary, "", true},
			"503":  {http.StatusServiceUnavailable, bodyCanary, provider.ClassUnreachable, false},
			"429":  {http.StatusTooManyRequests, "", provider.ClassRateLimited, false},
			"html": {http.StatusOK, "<html>" + bodyCanary + "</html>", provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(tc.status, tc.body), nil })
			_, err := env.change(id, "trk", arguments)
			if len(calls) != 1 {
				t.Errorf("%s %s: %d requests", id, name, len(calls))
			}
			switch {
			case tc.invalid:
				if !isInvalidRequest(err) {
					t.Errorf("%s %s: %v", id, name, err)
				}
			case classOf(err) != tc.class || strings.Contains(err.Error(), bodyCanary):
				t.Errorf("%s %s: err = %v", id, name, err)
			}
		}
		var calls []call
		env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return nil, contextDeadline() })
		_, err := env.change(id, "trk", arguments)
		if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") || len(calls) != 1 {
			t.Errorf("%s timeout: err = %v, calls = %d", id, err, len(calls))
		}
	}
}

func nonFinite() (float64, float64) {
	zero := 0.0
	return zero / zero, 1 / zero
}

func contextDeadline() error { return context.DeadlineExceeded }
