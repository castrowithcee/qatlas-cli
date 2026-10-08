package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const voucherID = "3f3d2c4b-5a6f-4789-9abc-def012345678"

const voucherlistBody = `{"content":[
 {"id":"` + voucherID + `","voucherType":"salesinvoice","voucherStatus":"open","voucherNumber":"RE1","voucherDate":"2026-01-02T00:00:00.000+01:00",
  "createdDate":"c","updatedDate":"u","dueDate":"d","contactId":"` + contactID + `","contactName":"` + bodyCanary + `",
  "totalAmount":119.123456789012345,"openAmount":0.10,"currency":"EUR","archived":false},
 {"id":"` + contactID + `","voucherType":"futuretype","voucherStatus":"futurestatus","archived":true}],
 "last":false,"totalPages":4,"totalElements":90,"size":25,"number":1}`

func listVouchers(t *testing.T, options VoucherListOptions) url.Values {
	t.Helper()
	var got url.Values
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/voucherlist" {
			t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
		}
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, voucherlistBody), nil
	})
	c, _ := client(t)
	if _, err := c.ListVouchers(context.Background(), options); err != nil {
		t.Fatalf("ListVouchers() = %v", err)
	}
	return got
}

func TestListVouchersSendsExactlyTheControlledQuery(t *testing.T) {
	yes := true
	if got, want := listVouchers(t, VoucherListOptions{}), (url.Values{"voucherType": {"any"}, "voucherStatus": {"any"},
		"page": {"0"}, "size": {"25"}, "sort": {"voucherDate,DESC"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("default query = %v, want %v", got, want)
	}
	got := listVouchers(t, VoucherListOptions{
		VoucherTypes: []string{"quotation", "salesinvoice", "invoice"}, VoucherStatuses: []string{"paid", "draft"},
		Archived: &yes, ContactID: contactID, VoucherDateFrom: "2026-01-01", VoucherDateTo: "2026-12-31",
		CreatedDateFrom: "2026-02-01", CreatedDateTo: "2026-02-28", UpdatedDateFrom: "2026-03-01",
		UpdatedDateTo: "2026-03-31", VoucherNumber: sampleNumber, Page: 3, Size: 50, Sort: "updated_date", Direction: "asc",
	})
	want := url.Values{"voucherType": {"salesinvoice,invoice,quotation"}, "voucherStatus": {"draft,paid"},
		"archived": {"true"}, "contactId": {contactID}, "voucherDateFrom": {"2026-01-01"}, "voucherDateTo": {"2026-12-31"},
		"createdDateFrom": {"2026-02-01"}, "createdDateTo": {"2026-02-28"}, "updatedDateFrom": {"2026-03-01"},
		"updatedDateTo": {"2026-03-31"}, "voucherNumber": {sampleNumber}, "page": {"3"}, "size": {"50"},
		"sort": {"updatedDate,ASC"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
	if got := listVouchers(t, VoucherListOptions{VoucherTypes: []string{"any"}, VoucherStatuses: []string{"overdue"}}); got.Get("voucherType") != "any" || got.Get("voucherStatus") != "overdue" {
		t.Errorf("query = %v", got)
	}
}

func TestListVouchersRejectsUnusableOptionsBeforeIO(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	tests := map[string]VoucherListOptions{
		"overdue with another status": {VoucherStatuses: []string{"overdue", "open"}},
		"any status with another":     {VoucherStatuses: []string{"any", "open"}},
		"any type with another":       {VoucherTypes: []string{"invoice", "any"}},
		"unknown type":                {VoucherTypes: []string{"receipt"}},
		"unknown status":              {VoucherStatuses: []string{"lost"}},
		"empty type list":             {VoucherTypes: []string{}},
		"duplicate type":              {VoucherTypes: []string{"invoice", "invoice"}},
		"duplicate status":            {VoucherStatuses: []string{"open", "open"}},
		"contact is no UUID":          {ContactID: "../v1/contacts"},
		"malformed created date":      {CreatedDateFrom: "01.02.2026"},
		"malformed updated date":      {UpdatedDateTo: "2026-13-45"},
		"unknown sort":                {Sort: "contact_name"},
		"page size above bound":       {Size: maxPageSize + 1},
		"page above bound":            {Page: maxPage + 1},
		"beyond 10000 hits":           {Page: 100, Size: 100},
		"just beyond 10000 hits":      {Page: 101, Size: 99},
		"beyond with small size":      {Page: 200, Size: 50},
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := c.ListVouchers(context.Background(), options); err == nil {
				t.Fatal("ListVouchers() accepted the options")
			}
		})
	}
	_, err := c.ListVouchers(context.Background(), VoucherListOptions{Page: 100, Size: 100})
	if err == nil || !strings.Contains(err.Error(), "at most 10,000 vouchers per filter; narrow the filter") {
		t.Errorf("limit message = %v", err)
	}
	// the last permitted page is not refused locally
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, voucherlistBody), nil })
	c, _ = client(t)
	if _, err := c.ListVouchers(context.Background(), VoucherListOptions{Page: 99, Size: 100}); err != nil {
		t.Errorf("page 99 of size 100 = %v", err)
	}
}

func TestListVouchersKeepsUnknownValuesAndNormalizes(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, voucherlistBody), nil })
	c, _ := client(t)
	result, err := c.ListVouchers(context.Background(), VoucherListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Page != 1 || result.Size != 25 || result.TotalPages != 4 || result.TotalElements != 90 || result.LastPage ||
		len(result.Vouchers) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first := result.Vouchers[0]
	if first.ID != voucherID || first.VoucherType != "salesinvoice" || first.ContactID != contactID ||
		first.TotalAmount != "119.123456789012345" || first.OpenAmount != "0.10" || first.Currency != "EUR" {
		t.Errorf("first = %+v", first)
	}
	if second := result.Vouchers[1]; second.VoucherType != "futuretype" || second.VoucherStatus != "futurestatus" || !second.Archived {
		t.Errorf("second = %+v", second)
	}
}

func TestListVouchersRejectsUnusableIdentifiers(t *testing.T) {
	for _, body := range []string{
		`{"content":[{"id":"nope","voucherType":"invoice"}],"size":25}`,
		`{"content":[{"id":"` + voucherID + `","contactId":"nope"}],"size":25}`,
	} {
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
		c, _ := client(t)
		if _, err := c.ListVouchers(context.Background(), VoucherListOptions{}); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("class = %q (%v)", classOf(err), err)
		}
	}
}

const paymentBody = `{"openAmount":10.50,"currency":"EUR","paymentStatus":"openRevenue","voucherType":"salesinvoice",
 "voucherStatus":"open","paidDate":"2026-05-01T00:00:00.000+02:00","paymentItems":[
 {"paymentItemType":"manualPayment","postingDate":"2026-04-01","amount":100.25,"currency":"EUR","exchangeRate":1},
 {"paymentItemType":"futureItem","postingDate":"2026-04-02","amount":0.1,"currency":"USD","exchangeRate":1.0850000001}]}`

func TestGetPaymentStatusNormalizes(t *testing.T) {
	var path string
	serve(t, func(request *http.Request) (*http.Response, error) {
		path = request.URL.Path
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, paymentBody), nil
	})
	c, _ := client(t)
	status, err := c.GetPaymentStatus(context.Background(), voucherID)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/payments/"+voucherID {
		t.Errorf("path = %s", path)
	}
	if status.VoucherID != voucherID || status.PaymentStatus != "openRevenue" || status.OpenAmount != "10.50" ||
		status.Currency != "EUR" || status.VoucherType != "salesinvoice" || status.PaidDate == "" || len(status.PaymentItems) != 2 {
		t.Fatalf("status = %+v", status)
	}
	if item := status.PaymentItems[1]; item.PaymentItemType != "futureItem" || item.Amount != "0.1" ||
		item.ExchangeRate != "1.0850000001" || item.Currency != "USD" || item.PostingDate != "2026-04-02" {
		t.Errorf("item = %+v", item)
	}
	encoded, _ := json.Marshal(status)
	if !strings.Contains(string(encoded), `"payment_items"`) {
		t.Errorf("encoded = %s", encoded)
	}
}

const voucherBody = `{"id":"` + voucherID + `","type":"purchaseinvoice","voucherStatus":"unchecked","voucherNumber":"ER-7",
 "voucherDate":"2026-01-02","shippingDate":"2026-01-01","dueDate":"2026-02-01","totalGrossAmount":119.00,
 "totalTaxAmount":19.0,"taxType":"gross","useCollectiveContact":false,"contactId":"` + contactID + `",
 "remark":"` + bodyCanary + `","voucherItems":[{"amount":119.00,"taxAmount":19.00,"taxRatePercent":19,
 "categoryId":"8f8664a8-fd86-4a2c-a2c3-c1c4a1d8d8b1"}],"files":["` + contactID + `"],
 "createdDate":"c","updatedDate":"u","version":3}`

func TestGetVoucherNormalizesAndChecksTheIdentifier(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if !strings.EqualFold(request.URL.Path, "/v1/vouchers/"+voucherID) {
			t.Errorf("path = %s", request.URL.Path)
		}
		return jsonResponse(http.StatusOK, voucherBody), nil
	})
	c, _ := client(t)
	voucher, err := c.GetVoucher(context.Background(), strings.ToUpper(voucherID))
	if err != nil {
		t.Fatal(err)
	}
	if voucher.Type != "purchaseinvoice" || voucher.VoucherStatus != "unchecked" || voucher.TotalGrossAmount != "119.00" ||
		voucher.ContactID != contactID || voucher.Version != 3 || len(voucher.Files) != 1 || len(voucher.VoucherItems) != 1 ||
		voucher.VoucherItems[0] != (VoucherItem{Amount: "119.00", TaxAmount: "19.00", TaxRatePercent: "19",
			CategoryID: "8f8664a8-fd86-4a2c-a2c3-c1c4a1d8d8b1"}) {
		t.Errorf("voucher = %+v", voucher)
	}

	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Replace(voucherBody, voucherID, contactID, 1)), nil
	})
	c, _ = client(t)
	if _, err := c.GetVoucher(context.Background(), voucherID); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("class = %q (%v)", classOf(err), err)
	}
}

func TestVoucherReadsGuardIdentifiersBeforeIO(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	if _, err := c.GetVoucher(context.Background(), "../x"); err == nil {
		t.Error("GetVoucher accepted a non-UUID")
	}
	if _, err := c.GetPaymentStatus(context.Background(), "../x"); err == nil {
		t.Error("GetPaymentStatus accepted a non-UUID")
	}
}

func TestVoucherReadsReportMissingResourcesAndHideBodies(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+voucherID+`"}`), nil
	})
	c, _ := client(t)
	_, err := c.GetPaymentStatus(context.Background(), voucherID)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "this payment status") {
		t.Errorf("payment 404 = %v", err)
	}
	_, err2 := c.GetVoucher(context.Background(), voucherID)
	if classOf(err2) != provider.ClassNotFound || !strings.Contains(err2.Error(), "this voucher") {
		t.Errorf("voucher 404 = %v", err2)
	}
	for _, e := range []error{err, err2} {
		if strings.Contains(e.Error(), bodyCanary) || strings.Contains(e.Error(), voucherID) {
			t.Errorf("error carries body or ID: %v", e)
		}
	}
	_, err3 := c.ListVouchers(context.Background(), VoucherListOptions{})
	if classOf(err3) != provider.ClassProviderError || strings.Contains(err3.Error(), bodyCanary) {
		t.Errorf("list 404 = %v", err3)
	}
}

func TestVoucherReadsRefuseOversizedAnswers(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
	})
	c, _ := client(t)
	if _, err := c.GetVoucher(context.Background(), voucherID); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("voucher = %v", err)
	}
	if _, err := c.GetPaymentStatus(context.Background(), voucherID); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("payment = %v", err)
	}
	if _, err := c.ListVouchers(context.Background(), VoucherListOptions{}); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("list = %v", err)
	}
}

func TestVoucherOperationsThroughTheApplicationCore(t *testing.T) {
	var paths []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch {
		case request.URL.Path == "/v1/voucherlist":
			return jsonResponse(http.StatusOK, voucherlistBody), nil
		case strings.HasPrefix(request.URL.Path, "/v1/payments/"):
			return jsonResponse(http.StatusOK, paymentBody), nil
		}
		return jsonResponse(http.StatusOK, voucherBody), nil
	})
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	invoke := func(operation, args string) error {
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: operation, Connection: "lexware-primary", Arguments: json.RawMessage(args)})
		return err
	}
	for operation, args := range map[string]string{
		"lexware.voucherlist.list": `{"voucher_types":["salesinvoice","quotation"],"voucher_statuses":["open","paid"],"archived":false,"contact_id":"` + contactID + `","created_date_from":"2026-01-01","page":1,"size":10}`,
		"lexware.payments.get":     `{"voucher_id":"` + voucherID + `"}`,
		"lexware.vouchers.get":     `{"id":"` + voucherID + `"}`,
	} {
		if err := invoke(operation, args); err != nil {
			t.Errorf("%s = %v", operation, err)
		}
	}
	if len(paths) != 3 {
		t.Errorf("requests = %v", paths)
	}

	refuse(t)
	for name, request := range map[string][2]string{
		"unknown type":      {"lexware.voucherlist.list", `{"voucher_types":["receipt"]}`},
		"unknown status":    {"lexware.voucherlist.list", `{"voucher_statuses":["lost"]}`},
		"duplicate":         {"lexware.voucherlist.list", `{"voucher_types":["invoice","invoice"]}`},
		"empty list":        {"lexware.voucherlist.list", `{"voucher_types":[]}`},
		"free parameter":    {"lexware.voucherlist.list", `{"extra":"x"}`},
		"contact no UUID":   {"lexware.voucherlist.list", `{"contact_id":"x"}`},
		"overdue and other": {"lexware.voucherlist.list", `{"voucher_statuses":["overdue","open"]}`},
		"any and other":     {"lexware.voucherlist.list", `{"voucher_types":["any","invoice"]}`},
		"limit":             {"lexware.voucherlist.list", `{"page":150,"size":100}`},
		"payment no UUID":   {"lexware.payments.get", `{"voucher_id":"../x"}`},
		"voucher no UUID":   {"lexware.vouchers.get", `{"id":"../x"}`},
	} {
		if err := invoke(request[0], request[1]); err == nil {
			t.Errorf("%s: the core accepted the request", name)
		}
	}
}
