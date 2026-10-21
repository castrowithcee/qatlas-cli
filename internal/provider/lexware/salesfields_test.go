package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// readTool is one detail tool of a sales voucher type, including the invoice.
type readTool struct{ name, tool, path string }

func readTools() []readTool {
	return []readTool{
		{"invoice", "lexware.invoices.get", "/v1/invoices/"},
		{"quotation", "lexware.quotations.get", "/v1/quotations/"},
		{"order confirmation", "lexware.order-confirmations.get", "/v1/order-confirmations/"},
		{"credit note", "lexware.credit-notes.get", "/v1/credit-notes/"},
		{"delivery note", "lexware.delivery-notes.get", "/v1/delivery-notes/"},
		{"dunning", "lexware.dunnings.get", "/v1/dunnings/"},
		{"down payment invoice", "lexware.down-payment-invoices.get", "/v1/down-payment-invoices/"},
	}
}

// invoice returns one tool's output as a generic object through the application core, so the output schema
// is enforced on the way.
func invokeRead(t *testing.T, tool readTool, body string) map[string]any {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != tool.path+salesVoucherID {
			t.Errorf("path = %s", request.URL.Path)
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: tool.tool, Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"id":"` + salesVoucherID + `"}`),
	})
	if err != nil {
		t.Fatalf("%s = %v", tool.tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal(got.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A legacy answer carries none of the later members. The output keeps its former shape exactly.
const legacyCommon = `"id":"` + salesVoucherID + `","createdDate":"2026-05-14T16:52:21.000+02:00",
 "updatedDate":"2026-05-14T16:52:21.000+02:00","language":"de","archived":false,"voucherStatus":"open",
 "voucherNumber":"XX0042","voucherDate":"2026-05-14T00:00:00.000+02:00",
 "address":{"name":"Bike Ride GmbH","city":"Freiburg"},"title":"T","introduction":"I","remark":"R"`

const legacyPriced = legacyCommon + `,"lineItems":[{"type":"custom","name":"Beratung","quantity":2,"unitName":"Std",
 "unitPrice":{"netAmount":100,"grossAmount":119,"taxRatePercentage":19},"discountPercentage":0,"lineItemAmount":200}],
 "totalPrice":{"currency":"EUR","totalNetAmount":200,"totalGrossAmount":238,"totalTaxAmount":38},
 "taxConditions":{"taxType":"net"}`

const legacyUnpriced = legacyCommon + `,"lineItems":[{"type":"custom","name":"Lieferung","quantity":3,"unitName":"Stück"}]`

func TestLegacyAnswersKeepTheirFormerOutput(t *testing.T) {
	head := `{"id":"` + salesVoucherID + `","voucher_number":"XX0042","voucher_status":"open",` +
		`"voucher_date":"2026-05-14T00:00:00.000+02:00",`
	tail := `"created_date":"2026-05-14T16:52:21.000+02:00","updated_date":"2026-05-14T16:52:21.000+02:00",` +
		`"archived":false,"language":"de","title":"T","introduction":"I","remark":"R",`
	contact := `"contact":{"name":"Bike Ride GmbH","city":"Freiburg"},`
	priced := `"tax_type":"net","currency":"EUR","total_net_amount":200,"total_gross_amount":238,"total_tax_amount":38,` +
		contact + `"line_items":[{"type":"custom","name":"Beratung","quantity":2,"unit_name":"Std",` +
		`"unit_net_amount":100,"unit_gross_amount":119,"tax_rate_percentage":19,"discount_percentage":0,"amount":200}]}`
	unpriced := contact + `"line_items":[{"type":"custom","name":"Lieferung","quantity":3,"unit_name":"Stück"}]}`
	cases := map[string]struct{ body, want string }{
		"lexware.invoices.get": {legacyPriced, `{"id":"` + salesVoucherID + `","voucher_number":"XX0042","voucher_status":"open",` +
			`"overdue":false,"voucher_date":"2026-05-14T00:00:00.000+02:00",` + tail + priced},
		"lexware.quotations.get":            {legacyPriced, head + tail + priced},
		"lexware.order-confirmations.get":   {legacyPriced, head + tail + priced},
		"lexware.credit-notes.get":          {legacyPriced, head + tail + priced},
		"lexware.delivery-notes.get":        {legacyUnpriced, head + tail + unpriced},
		"lexware.dunnings.get":              {legacyPriced, head + tail + priced},
		"lexware.down-payment-invoices.get": {legacyPriced, head + tail + priced},
	}
	for _, tool := range readTools() {
		t.Run(tool.name, func(t *testing.T) {
			tt := cases[tool.tool]
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, "{"+tt.body+"}"), nil
			})
			c, _ := client(t)
			var got any
			var err error
			if tool.tool == "lexware.invoices.get" {
				got, err = c.GetInvoice(context.Background(), salesVoucherID)
			} else {
				for _, sc := range salesCases() {
					if sc.tool == tool.tool {
						got, err = sc.get(c, context.Background(), salesVoucherID)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(got)
			if string(encoded) != tt.want {
				t.Errorf("output changed:\n got %s\nwant %s", encoded, tt.want)
			}
		})
	}
}

// The example response of the Lexware documentation: one invoice with three tax rates.
const documentedInvoice = `{
 "id":"` + salesVoucherID + `","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8",
 "createdDate":"2023-04-24T08:20:22.528+02:00","updatedDate":"2023-04-24T08:20:22.528+02:00",
 "version":0,"language":"de","archived":false,"voucherStatus":"draft","voucherNumber":"RE1019",
 "voucherDate":"2023-02-22T00:00:00.000+01:00","dueDate":null,
 "address":{"contactId":null,"name":"Bike & Ride GmbH & Co. KG","supplement":"Gebäude 10","street":"Musterstraße 42",
  "city":"Freiburg","zip":"79112","countryCode":"DE","contactPerson":"Frau Muster"},
 "xRechnung":{"buyerReference":"04011000-12345-34"},"electronicDocumentProfile":"XRechnung",
 "lineItems":[
  {"id":"97b98491-e953-4dc9-97a9-ae437a8052b4","type":"material","name":"Abus Kabelschloss Primo 590 ","quantity":2,
   "unitName":"Stück","unitPrice":{"currency":"EUR","netAmount":13.4,"grossAmount":15.95,"taxRatePercentage":19},
   "discountPercentage":50,"lineItemAmount":13.4},
  {"id":"dc4c805b-7df1-4310-a548-22be4499eb04","type":"service","name":"Aufwändige Montage","quantity":1,
   "unitName":"Stunde","unitPrice":{"currency":"EUR","netAmount":8.32,"grossAmount":8.9,"taxRatePercentage":7},
   "discountPercentage":0,"lineItemAmount":8.32},
  {"id":null,"type":"custom","name":"Energieriegel Testpaket","description":null,"quantity":1,"unitName":"Stück",
   "unitPrice":{"currency":"EUR","netAmount":5,"grossAmount":5,"taxRatePercentage":0},"discountPercentage":0,"lineItemAmount":5},
  {"type":"text","name":"Freitextposition","description":"Text"}],
 "totalPrice":{"currency":"EUR","totalNetAmount":26.72,"totalGrossAmount":29.85,"totalTaxAmount":3.13,
  "totalDiscountAbsolute":1.5,"totalDiscountPercentage":2.25},
 "taxAmounts":[{"taxRatePercentage":0,"taxAmount":0,"netAmount":5},
  {"taxRatePercentage":7,"taxAmount":0.58,"netAmount":8.32},{"taxRatePercentage":19,"taxAmount":2.55,"netAmount":13.4}],
 "taxConditions":{"taxType":"net","taxSubType":"distanceSales","taxTypeNote":"` + bodyCanary + `"},
 "paymentConditions":{"paymentTermLabel":"10 Tage - 3 %, 30 Tage netto",
  "paymentTermLabelTemplate":"{discountRange} Tage -{discount}, {paymentRange} Tage netto","paymentTermDuration":30,
  "paymentDiscountConditions":{"discountPercentage":3,"discountRange":10}},
 "shippingConditions":{"shippingDate":"2023-04-22T00:00:00.000+02:00","shippingEndDate":null,"shippingType":"delivery"},
 "closingInvoice":false,"claimedGrossAmount":null,"downPaymentDeductions":null,
 "recurringTemplateId":"0c1b2a39-4d5e-4f60-8a71-92b3c4d5e6f8","relatedVouchers":[],
 "printLayoutId":"28c212c4-b6dd-11ee-b80a-dbc65f4ceccf","title":"Rechnung","introduction":"x","remark":"y",
 "files":{"documentFileId":"75295db7-7e69-4630-befd-a7f4ddfdaa83"}}`

func TestInvoiceReportsEveryDocumentedMember(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, documentedInvoice), nil
	})
	c, _ := client(t)
	invoice, err := c.GetInvoice(context.Background(), salesVoucherID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(invoice)
	out := string(encoded)
	for _, want := range []string{
		`"organization_id":"aa93e8a8-2aa3-470b-b914-caad8a255dd8"`,
		`"electronic_document_profile":"XRechnung"`, `"buyer_reference":"04011000-12345-34"`,
		`"print_layout_id":"28c212c4-b6dd-11ee-b80a-dbc65f4ceccf"`, `"contact_person":"Frau Muster"`,
		`"total_discount_absolute":1.5`, `"total_discount_percentage":2.25`,
		`"tax_amounts":[{"tax_rate_percentage":0,"tax_amount":0,"net_amount":5},` +
			`{"tax_rate_percentage":7,"tax_amount":0.58,"net_amount":8.32},` +
			`{"tax_rate_percentage":19,"tax_amount":2.55,"net_amount":13.4}]`,
		`"tax_sub_type":"distanceSales"`, `"tax_type_note":"` + bodyCanary + `"`,
		`"payment_conditions":{"payment_term_label":"10 Tage - 3 %, 30 Tage netto",` +
			`"payment_term_label_template":"{discountRange} Tage -{discount}, {paymentRange} Tage netto",` +
			`"payment_term_duration":30,"payment_discount_conditions":{"discount_percentage":3,"discount_range":10}}`,
		`"shipping":{"shipping_type":"delivery","shipping_date":"2023-04-22T00:00:00.000+02:00"}`,
		`"closing_invoice":false`, `"recurring_template_id":"0c1b2a39-4d5e-4f60-8a71-92b3c4d5e6f8"`,
		`"id":"97b98491-e953-4dc9-97a9-ae437a8052b4"`, `"unit_currency":"EUR"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s\n%s", want, out)
		}
	}
	for _, absent := range []string{"claimed_gross_amount", "down_payment_deductions", "documentFileId", "files",
		"related_vouchers", `"version"`, "sub_items", `"optional"`, `"alternative"`} {
		if strings.Contains(out, absent) {
			t.Errorf("output carries %s\n%s", absent, out)
		}
	}
}

func TestClosingInvoiceReportsClaimAndBothDeductions(t *testing.T) {
	body := `{"id":"` + salesVoucherID + `","closingInvoice":true,"claimedGrossAmount":595.10,
 "downPaymentDeductions":[
  {"id":"11111111-2222-4333-8444-555555555551","voucherType":"downpaymentinvoice","title":"Abschlag 1","voucherNumber":"AR0001",
   "voucherDate":"2026-01-10T00:00:00.000+01:00","receivedGrossAmount":119.00,"receivedNetAmount":100.00,"receivedTaxAmount":19.00,"taxRatePercentage":19},
  {"id":"11111111-2222-4333-8444-555555555552","voucherType":"downpaymentinvoice","title":"Abschlag 2","voucherNumber":"AR0002",
   "voucherDate":"2026-02-10T00:00:00.000+01:00","receivedGrossAmount":107.5,"receivedNetAmount":100,"receivedTaxAmount":7.5,"taxRatePercentage":7.5}]}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	invoice, err := c.GetInvoice(context.Background(), salesVoucherID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(invoice)
	for _, want := range []string{`"closing_invoice":true`, `"claimed_gross_amount":595.10`,
		`{"id":"11111111-2222-4333-8444-555555555551","voucher_type":"downpaymentinvoice","title":"Abschlag 1","voucher_number":"AR0001","voucher_date":"2026-01-10T00:00:00.000+01:00","received_gross_amount":119.00,"received_net_amount":100.00,"received_tax_amount":19.00,"tax_rate_percentage":19}`,
		`{"id":"11111111-2222-4333-8444-555555555552","voucher_type":"downpaymentinvoice","title":"Abschlag 2","voucher_number":"AR0002","voucher_date":"2026-02-10T00:00:00.000+01:00","received_gross_amount":107.5,"received_net_amount":100,"received_tax_amount":7.5,"tax_rate_percentage":7.5}`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("output lacks %s\n%s", want, encoded)
		}
	}
}

func TestQuotationReportsOptionalAndAlternativePositionsOnly(t *testing.T) {
	body := `{"id":"` + salesVoucherID + `","lineItems":[
 {"type":"custom","name":"A","optional":false,"alternative":false,"subItems":[
   {"type":"custom","name":"A2","alternative":true,"optional":false}]},
 {"type":"custom","name":"B","optional":true}]}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	quotation, err := c.GetQuotation(context.Background(), salesVoucherID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(quotation.LineItems)
	want := `[{"type":"custom","name":"A","optional":false,"alternative":false,"sub_items":[{"type":"custom","name":"A2","optional":false,"alternative":true}]},` +
		`{"type":"custom","name":"B","optional":true}]`
	if string(encoded) != want {
		t.Errorf("line items = %s\nwant %s", encoded, want)
	}
	order, err := c.GetOrderConfirmation(context.Background(), salesVoucherID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(order.LineItems)
	if strings.Contains(string(encoded), "optional") || strings.Contains(string(encoded), "sub_items") {
		t.Errorf("order confirmation carries quotation members: %s", encoded)
	}
}

func TestMembersOfOtherTypesAreDropped(t *testing.T) {
	body := salesBody(`"paymentConditions":{"paymentTermDuration":14},"taxAmounts":[{"taxRatePercentage":19,"taxAmount":1,"netAmount":5}],
 "totalPrice":{"totalDiscountAbsolute":2},"lineItems":[]`)
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	credit, err := c.GetCreditNote(context.Background(), salesVoucherID)
	if err != nil || credit.PaymentConditions != nil || len(credit.TaxAmounts) != 1 || credit.TotalDiscountAbsolute != "2" {
		t.Errorf("credit note = %#v, %v", credit, err)
	}
	note, err := c.GetDeliveryNote(context.Background(), salesVoucherID)
	if err != nil || note.PaymentConditions != nil || note.TaxAmounts != nil || note.TotalDiscountAbsolute != "" {
		t.Errorf("delivery note = %#v, %v", note, err)
	}
	order, err := c.GetOrderConfirmation(context.Background(), salesVoucherID)
	if err != nil || order.PaymentConditions == nil || order.PaymentConditions.PaymentTermDuration != "14" {
		t.Errorf("order confirmation = %#v, %v", order, err)
	}
}

// richBody carries every member any of the seven types documents, so each tool must forward all of its own.
func richBody() string {
	return `{"id":"` + salesVoucherID + `","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8",
 "createdDate":"c","updatedDate":"u","version":3,"language":"de","archived":false,"voucherStatus":"open",
 "voucherNumber":"N","voucherDate":"d","dueDate":"d","expirationDate":"e","deliveryTerms":"t",
 "closingInvoiceId":"` + closingInvoiceID + `",
 "address":{"contactId":"777c7793-9fbb-4ec7-9254-0619c199761e","name":"n","supplement":"s","street":"st","city":"c","zip":"z","countryCode":"DE","contactPerson":"p"},
 "xRechnung":{"buyerReference":"b"},"electronicDocumentProfile":"NONE",
 "lineItems":[{"id":"97b98491-e953-4dc9-97a9-ae437a8052b4","type":"custom","name":"n","description":"d","quantity":1,"unitName":"u",
  "unitPrice":{"currency":"EUR","netAmount":1,"grossAmount":1,"taxRatePercentage":0},"discountPercentage":0,"lineItemAmount":1,
  "optional":false,"alternative":false,"subItems":[{"type":"custom","name":"s","alternative":true}]}],
 "totalPrice":{"currency":"EUR","totalNetAmount":1,"totalGrossAmount":1,"totalTaxAmount":0,"totalDiscountAbsolute":1,"totalDiscountPercentage":1},
 "taxAmounts":[{"taxRatePercentage":0,"taxAmount":0,"netAmount":1}],
 "taxConditions":{"taxType":"net","taxSubType":"distanceSales","taxTypeNote":"n"},
 "paymentConditions":{"paymentTermLabel":"l","paymentTermLabelTemplate":"t","paymentTermDuration":30,
  "paymentDiscountConditions":{"discountPercentage":3,"discountRange":10}},
 "shippingConditions":{"shippingType":"delivery","shippingDate":"d","shippingEndDate":"e"},
 "closingInvoice":true,"claimedGrossAmount":1,
 "downPaymentDeductions":[{"id":"11111111-2222-4333-8444-555555555551","voucherType":"downpaymentinvoice","title":"t","voucherNumber":"n",
  "voucherDate":"d","receivedGrossAmount":1,"receivedNetAmount":1,"receivedTaxAmount":0,"taxRatePercentage":0}],
 "recurringTemplateId":"0c1b2a39-4d5e-4f60-8a71-92b3c4d5e6f8",
 "relatedVouchers":[{"id":"` + invoiceID + `","voucherNumber":"RE1","voucherType":"invoice"}],
 "printLayoutId":"28c212c4-b6dd-11ee-b80a-dbc65f4ceccf","title":"t","introduction":"i","remark":"r"}`
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	for _, tool := range readTools() {
		t.Run(tool.name, func(t *testing.T) {
			out := invokeRead(t, tool, richBody())
			var descriptor struct {
				schema map[string]any
				fields map[string]bool
			}
			descriptor.fields = map[string]bool{}
			for _, d := range registry.Provider(Provider) {
				if d.ID != tool.tool {
					continue
				}
				if err := json.Unmarshal(d.OutputSchema, &descriptor.schema); err != nil {
					t.Fatal(err)
				}
				for _, f := range d.Fields {
					descriptor.fields[f.Name] = true
				}
			}
			properties, _ := descriptor.schema["properties"].(map[string]any)
			var missing, schemaOnly []string
			for key := range out {
				if _, ok := properties[key]; !ok {
					missing = append(missing, key+" (schema)")
				}
				if !descriptor.fields[key] {
					missing = append(missing, key+" (fields)")
				}
			}
			for key := range properties {
				if !descriptor.fields[key] {
					schemaOnly = append(schemaOnly, key)
				}
			}
			sort.Strings(missing)
			sort.Strings(schemaOnly)
			if len(missing) > 0 || len(schemaOnly) > 0 {
				t.Errorf("not in schema or fields: %v; schema members without field: %v", missing, schemaOnly)
			}
			if len(out) < 20 {
				t.Errorf("rich answer produced only %d members: %v", len(out), out)
			}
		})
	}
}
