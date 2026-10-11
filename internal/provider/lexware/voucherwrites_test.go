package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	categoryID = "0f1e2d3c-4b5a-4968-8777-665544332211"
	newVoucher = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

const voucherItemArgs = `[{"amount":100,"tax_amount":19,"tax_rate_percent":19,"category_id":"` + categoryID + `"}]`

func voucherArgs(extra string) string {
	return `{"type":"purchaseinvoice","voucher_date":"2026-05-14","total_gross_amount":119.5,"total_tax_amount":19,` +
		`"tax_type":"gross","voucher_items":` + voucherItemArgs + extra + `}`
}

func voucherCreated() (*http.Response, error) {
	return jsonResponse(http.StatusCreated, `{"id":"`+newVoucher+`","resourceUri":"x","createdDate":"c","updatedDate":"u","version":1}`), nil
}

func TestCreateVoucherSendsExactlyOneFixedStatusPost(t *testing.T) {
	tests := map[string]struct{ args, want string }{
		"collective contact": {
			`{"type":"purchaseinvoice","voucher_number":"R-1","voucher_date":"2026-05-14","due_date":"2026-06-14",` +
				`"total_gross_amount":119.50,"total_tax_amount":19,"tax_type":"gross","use_collective_contact":true,` +
				`"voucher_items":` + voucherItemArgs + `}`,
			`{"dueDate":"2026-06-14","taxType":"gross","totalGrossAmount":119.50,"totalTaxAmount":19,` +
				`"type":"purchaseinvoice","useCollectiveContact":true,"voucherDate":"2026-05-14","voucherItems":` +
				`[{"amount":100,"categoryId":"` + categoryID + `","taxAmount":19,"taxRatePercent":19}],` +
				`"voucherNumber":"R-1","voucherStatus":"unchecked"}`},
		"contact": {voucherArgs(`,"contact_id":"` + contactID + `","use_collective_contact":false`),
			`{"contactId":"` + contactID + `","taxType":"gross","totalGrossAmount":119.5,"totalTaxAmount":19,` +
				`"type":"purchaseinvoice","voucherDate":"2026-05-14","voucherItems":` +
				`[{"amount":100,"categoryId":"` + categoryID + `","taxAmount":19,"taxRatePercent":19}],` +
				`"voucherStatus":"unchecked"}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, storedGet, voucherCreated)
			core, _ := writeCore(t)
			result, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: "lexware.vouchers.create", Connection: "lexware-writer", Confirmed: true,
				Arguments: json.RawMessage(tt.args)})
			if err != nil || !strings.Contains(string(result.Result), `"id":"`+newVoucher+`"`) {
				t.Fatalf("invoke = %s, %v", result.Result, err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost || (*requests)[0].path != "/v1/vouchers" {
				t.Fatalf("requests = %+v", *requests)
			}
			sent, _ := json.Marshal((*requests)[0].body)
			if string(sent) != tt.want {
				t.Errorf("payload = %s\nwant      %s", sent, tt.want)
			}
		})
	}
}

func TestVoucherPayloadTakesTheStatusAsParameter(t *testing.T) {
	payload := voucherPayload("open", voucherInput{Type: "salesinvoice", UseCollectiveContact: true})
	if payload["voucherStatus"] != "open" || payload["useCollectiveContact"] != true {
		t.Errorf("payload = %v", payload)
	}
}

func TestVoucherCreateIsRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	core, reads := writeCore(t)
	const create = "lexware.vouchers.create"
	items101 := strings.TrimSuffix(strings.Repeat(`{"amount":1,"tax_amount":0,"tax_rate_percent":0,"category_id":"`+categoryID+`"},`, 101), ",")
	collective := `,"use_collective_contact":true`
	tests := []struct {
		name, args string
		confirm    bool
	}{
		{"without confirm", voucherArgs(collective), false},
		{"net", strings.Replace(voucherArgs(collective), `"gross"`, `"net"`, 1), true},
		{"no contact", voucherArgs(``), true},
		{"collective false", voucherArgs(`,"use_collective_contact":false`), true},
		{"both contacts", voucherArgs(`,"contact_id":"` + contactID + `"` + collective), true},
		{"contact not a uuid", voucherArgs(`,"contact_id":"../contacts/` + contactID + `"`), true},
		{"category not a uuid", strings.Replace(voucherArgs(collective), categoryID, "../x", 1), true},
		{"101 positions", strings.Replace(voucherArgs(collective), voucherItemArgs, `[`+items101+`]`, 1), true},
		{"no positions", strings.Replace(voucherArgs(collective), voucherItemArgs, `[]`, 1), true},
		{"status argument", voucherArgs(collective + `,"voucher_status":"open"`), true},
		{"remark argument", voucherArgs(collective + `,"remark":"x"`), true},
		{"files argument", voucherArgs(collective + `,"files":[]`), true},
		{"bad date", strings.Replace(voucherArgs(collective), "2026-05-14", "2026-13-45", 1), true},
		{"negative amount", strings.Replace(voucherArgs(collective), `"total_tax_amount":19`, `"total_tax_amount":-1`, 1), true},
		{"unknown type", strings.Replace(voucherArgs(collective), "purchaseinvoice", "invoice", 1), true},
		{"item passthrough", strings.Replace(voucherArgs(collective), `"tax_amount":19,`, `"tax_amount":19,"x":1,`, 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: create, Connection: "lexware-writer", Arguments: json.RawMessage(tt.args), Confirmed: tt.confirm})
			if err == nil {
				t.Fatal("the core accepted the request")
			}
			if !tt.confirm {
				var confirmation *application.ConfirmationRequiredError
				if !errors.As(err, &confirmation) {
					t.Errorf("err = %v, want a confirmation-required error", err)
				}
			}
		})
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

func TestVoucherHandlerRechecksTheRulesWithoutTheSchema(t *testing.T) {
	refuse(t)
	good := voucherInput{Type: "purchaseinvoice", VoucherDate: "2026-05-14", TotalGrossAmount: "1", TotalTaxAmount: "0",
		TaxType: "gross", UseCollectiveContact: true,
		VoucherItems: []voucherItemData{{Amount: "1", TaxAmount: "0", TaxRatePercent: "0", CategoryID: categoryID}}}
	if err := good.validate(); err != nil {
		t.Fatalf("validate(good) = %v", err)
	}
	net := good
	net.TaxType = "net"
	many := good
	many.VoucherItems = make([]voucherItemData, 101)
	for i := range many.VoucherItems {
		many.VoucherItems[i] = good.VoucherItems[0]
	}
	both := good
	both.ContactID = contactID
	c, _ := client(t)
	for name, input := range map[string]voucherInput{"net": net, "many": many, "both": both} {
		if _, err := c.CreateVoucher(context.Background(), input); err == nil {
			t.Errorf("%s: CreateVoucher accepted the input", name)
		}
	}
	if err := net.validate(); err == nil || !strings.Contains(err.Error(), "booked vouchers") {
		t.Errorf("net err = %v, want the booked-vouchers hint", err)
	}
}

func TestVoucherCreateFailuresAreClassifiedWithoutProviderText(t *testing.T) {
	reset := &net.OpError{Op: "read", Err: syscall.ECONNRESET}
	status := func(code int) func() (*http.Response, error) {
		return func() (*http.Response, error) { return jsonResponse(code, `{"message":"`+bodyCanary+`"}`), nil }
	}
	tests := []struct {
		name  string
		reply func() (*http.Response, error)
		class provider.Class
		text  string
		hint  bool
	}{
		{"406", status(406), provider.ClassProviderError, validationMessage, false},
		{"500", status(500), provider.ClassProviderError, "", true},
		{"504", status(504), provider.ClassTimeout, "", true},
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }, provider.ClassTimeout, "", true},
		{"reset", func() (*http.Response, error) { return nil, reset }, provider.ClassUnreachable, "", true},
		{"unusable answer", func() (*http.Response, error) { return jsonResponse(200, "<html>"), nil }, provider.ClassInvalidResponse, "", true},
		{"2xx without id", func() (*http.Response, error) { return jsonResponse(201, `{"version":1}`), nil }, provider.ClassInvalidResponse, "", true},
		{"2xx with bad id", func() (*http.Response, error) { return jsonResponse(201, `{"id":"../x"}`), nil }, provider.ClassInvalidResponse, "", true},
		{"429", status(429), provider.ClassRateLimited, "", false},
	}
	input := voucherInput{Type: "purchaseinvoice", VoucherDate: "2026-05-14", TotalGrossAmount: "1", TotalTaxAmount: "0",
		TaxType: "gross", UseCollectiveContact: true,
		VoucherItems: []voucherItemData{{Amount: "1", TaxAmount: "0", TaxRatePercent: "0", CategoryID: categoryID}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.CreateVoucher(context.Background(), input)
			if err == nil || (tt.class != "" && classOf(err) != tt.class) || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), voucherMayExist); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
				t.Errorf("err = %v, want %q", err, tt.text)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost {
				t.Errorf("requests = %+v, want exactly one POST", *requests)
			}
		})
	}
}

func TestVoucherCreateDeclaresItsRiskAndJoinsOnlyTheWriteProfile(t *testing.T) {
	risk := vouchersCreate.Risk
	if risk.Effect != "create" || risk.Idempotency != "non_idempotent" || risk.Confirmation != "required" ||
		!risk.OpenWorld || risk.DataSensitivity != bookkeepingSensitivity || vouchersCreate.RequiresToolAllowList {
		t.Errorf("risk = %+v", risk)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := false
		for _, id := range profile.Tools {
			has = has || id == vouchersCreate.ID
		}
		if has != (profile.ID == "write") {
			t.Errorf("profile %s tools = %v", profile.ID, profile.Tools)
		}
	}
}
