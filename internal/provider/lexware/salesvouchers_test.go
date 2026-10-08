package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const salesVoucherID = "0c1b2a39-4d5e-4f60-8a71-92b3c4d5e6f7"

// salesBody builds a voucher answer; extra members are appended to the shared base.
func salesBody(extra string) string {
	return `{"id":"` + salesVoucherID + `","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8",
 "createdDate":"2026-05-14T16:52:21.000+02:00","updatedDate":"2026-05-15T10:00:00.000+02:00",
 "version":3,"language":"de","archived":false,"voucherStatus":"open","voucherNumber":"XX0042",
 "voucherDate":"2026-05-14T00:00:00.000+02:00",
 "address":{"contactId":"777c7793-9fbb-4ec7-9254-0619c199761e","name":"Bike & Ride GmbH","city":"Freiburg","zip":"79112","countryCode":"DE"},
 "title":"Titel","introduction":"Einleitung","remark":"` + bodyCanary + `",
 "relatedVouchers":[{"id":"` + invoiceID + `","voucherNumber":"RE1012","voucherType":"invoice"}],
 "shippingConditions":{"shippingType":"serviceperiod","shippingDate":"2026-05-01T00:00:00.000+02:00","shippingEndDate":"2026-05-31T00:00:00.000+02:00"},` + extra + `}`
}

const pricedExtra = `"lineItems":[{"type":"custom","name":"Beratung","quantity":2,"unitName":"Std",
 "unitPrice":{"currency":"EUR","netAmount":100,"grossAmount":119,"taxRatePercentage":19},"discountPercentage":0,"lineItemAmount":200}],
 "totalPrice":{"currency":"EUR","totalNetAmount":200,"totalGrossAmount":238,"totalTaxAmount":38},
 "taxAmounts":[{"taxRatePercentage":19,"taxAmount":38,"netAmount":200}],"taxConditions":{"taxType":"net"}`

const unpricedExtra = `"lineItems":[{"type":"custom","name":"Lieferung","quantity":3,"unitName":"Stück"}]`

type salesCase struct {
	name, tool, path, resource string
	get                        func(*Client, context.Context, string) (*SalesVoucher, error)
	body                       string
	expiration, terms          string
	priced                     bool
}

func salesCases() []salesCase {
	return []salesCase{
		{"quotation", "lexware.quotations.get", "/v1/quotations/", "quotation",
			(*Client).GetQuotation, salesBody(`"expirationDate":"2026-06-14T00:00:00.000+02:00",` + pricedExtra),
			"2026-06-14T00:00:00.000+02:00", "", true},
		{"order confirmation", "lexware.orderconfirmations.get", "/v1/order-confirmations/", "order confirmation",
			(*Client).GetOrderConfirmation, salesBody(`"deliveryTerms":"frei Haus",` + pricedExtra), "", "frei Haus", true},
		{"credit note", "lexware.creditnotes.get", "/v1/credit-notes/", "credit note",
			(*Client).GetCreditNote, salesBody(pricedExtra), "", "", true},
		{"delivery note", "lexware.deliverynotes.get", "/v1/delivery-notes/", "delivery note",
			(*Client).GetDeliveryNote, salesBody(unpricedExtra), "", "", false},
	}
}

func TestSalesVoucherReadsExactlyOneFixedPathAndMapsTheDocument(t *testing.T) {
	for _, tt := range salesCases() {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
				return jsonResponse(http.StatusOK, tt.body), nil
			})
			c, _ := client(t)
			got, err := tt.get(c, context.Background(), salesVoucherID)
			if err != nil {
				t.Fatalf("get = %v", err)
			}
			if want := []string{"GET " + tt.path + salesVoucherID + "?"}; !reflect.DeepEqual(requests, want) {
				t.Fatalf("requests = %v, want %v", requests, want)
			}
			if got.ID != salesVoucherID || got.VoucherNumber != "XX0042" || got.VoucherStatus != "open" ||
				got.Version != 3 || got.Language != "de" || got.Title != "Titel" || got.Remark != bodyCanary ||
				got.ExpirationDate != tt.expiration || got.DeliveryTerms != tt.terms {
				t.Errorf("voucher = %#v", got)
			}
			if got.Contact == nil || got.Contact.Name != "Bike & Ride GmbH" || got.Contact.ID != "777c7793-9fbb-4ec7-9254-0619c199761e" ||
				got.Shipping == nil || got.Shipping.ShippingType != "serviceperiod" || got.Shipping.ShippingEndDate == "" ||
				len(got.RelatedVouchers) != 1 || got.RelatedVouchers[0] != (RelatedVoucher{ID: invoiceID, VoucherNumber: "RE1012", VoucherType: "invoice"}) ||
				len(got.LineItems) != 1 {
				t.Errorf("voucher = %#v", got)
			}
			if tt.priced {
				item := got.LineItems[0]
				if got.TaxType != "net" || got.Currency != "EUR" || got.TotalNetAmount != "200" || got.TotalGrossAmount != "238" ||
					got.TotalTaxAmount != "38" || item.UnitNetAmount != "100" || item.UnitGrossAmount != "119" ||
					item.TaxRatePercentage != "19" || item.Amount != "200" {
					t.Errorf("prices = %#v", got)
				}
				return
			}
			// A delivery note carries no prices; none may be invented.
			encoded, _ := json.Marshal(got)
			for _, absent := range []string{"total_", "currency", "unit_net", "unit_gross", "tax_rate", "amount", "tax_type"} {
				if strings.Contains(string(encoded), absent) {
					t.Errorf("output carries %q without prices: %s", absent, encoded)
				}
			}
		})
	}
}

func TestSalesVoucherDropsMembersTheTypeDoesNotDefine(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, salesBody(`"expirationDate":"x","deliveryTerms":"y",`+unpricedExtra)), nil
	})
	c, _ := client(t)
	got, err := c.GetCreditNote(context.Background(), salesVoucherID)
	if err != nil || got.ExpirationDate != "" || got.DeliveryTerms != "" {
		t.Errorf("credit note = %#v, %v", got, err)
	}
}

func TestSalesVoucherGuardsTheIdentifier(t *testing.T) {
	for _, tt := range salesCases() {
		t.Run(tt.name+" malformed", func(t *testing.T) {
			refuse(t)
			c, _ := client(t)
			for _, id := range []string{"", "42", "../../v1/contacts", salesVoucherID + "0"} {
				if _, err := tt.get(c, context.Background(), id); err == nil {
					t.Errorf("get(%q) was accepted", id)
				}
			}
		})
		t.Run(tt.name+" other answer", func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"id":"`+invoiceID+`"}`), nil
			})
			c, _ := client(t)
			_, err := tt.get(c, context.Background(), salesVoucherID)
			if class := classOf(err); class != provider.ClassInvalidResponse {
				t.Fatalf("class = %q (%v)", class, err)
			}
		})
		t.Run(tt.name+" not found", func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
			})
			c, _ := client(t)
			_, err := tt.get(c, context.Background(), salesVoucherID)
			if class := classOf(err); class != provider.ClassNotFound {
				t.Fatalf("class = %q (%v)", class, err)
			}
			if !strings.Contains(err.Error(), "Lexware does not hold this "+tt.resource+" or does not show it to this API key") ||
				strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), salesVoucherID) {
				t.Errorf("message = %v", err)
			}
		})
	}
}

// The shared mapper must not change what invoices.get reports.
func TestInvoiceOutputIsUnchangedBySharedMapper(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, invoiceBody), nil
	})
	c, _ := client(t)
	invoice, err := c.GetInvoice(context.Background(), invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(invoice)
	want := `{"id":"` + invoiceID + `","voucher_number":"RE1012","voucher_status":"overdue","overdue":true,` +
		`"voucher_date":"2026-05-14T00:00:00.000+02:00","due_date":"2026-05-24T00:00:00.000+02:00",` +
		`"created_date":"2026-05-14T16:52:21.000+02:00","updated_date":"2026-05-14T16:52:21.000+02:00",` +
		`"archived":false,"language":"de","title":"Rechnung","introduction":"Ihre bestellten Positionen",` +
		`"remark":"Vielen Dank","tax_type":"net","currency":"EUR","total_net_amount":26.72,` +
		`"total_gross_amount":29.85,"total_tax_amount":3.13,"contact":{"id":"777c7793-9fbb-4ec7-9254-0619c199761e",` +
		`"name":"Bike \u0026 Ride GmbH \u0026 Co. KG","supplement":"Gebäude 10","street":"Musterstraße 42",` +
		`"zip":"79112","city":"Freiburg","country_code":"DE"},"line_items":[{"type":"material",` +
		`"name":"Abus Kabelschloss","description":"` + bodyCanary + `","quantity":2,"unit_name":"Stück",` +
		`"unit_net_amount":13.4,"unit_gross_amount":15.95,"tax_rate_percentage":19,"discount_percentage":50,"amount":13.4}]}`
	if string(encoded) != want {
		t.Errorf("invoice output changed:\n got %s\nwant %s", encoded, want)
	}
}

// Each tool satisfies its registered contract through the application core, including the delivery note
// without prices, and the core refuses a malformed identifier before any provider I/O.
func TestSalesVoucherToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	for _, tt := range salesCases() {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != tt.path+salesVoucherID {
					t.Errorf("path = %s", request.URL.Path)
				}
				return jsonResponse(http.StatusOK, tt.body), nil
			})
			stubLimiter(t, primaryKey)
			red := &redact.Redactor{}
			core := application.New(registry(t), coreConfig(), resolver(red), red)
			got, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.tool, Connection: "lexware-primary",
				Arguments: json.RawMessage(`{"id":"` + salesVoucherID + `"}`),
			})
			if err != nil || !strings.Contains(string(got.Result), `"voucher_number":"XX0042"`) {
				t.Fatalf("invoke = %s, %v", got.Result, err)
			}
			refuse(t)
			for _, arguments := range []string{`{"id":"../../v1/contacts"}`, `{"id":"` + salesVoucherID + `","type":"invoice"}`, `{}`} {
				if _, err := core.Invoke(context.Background(), application.InvokeRequest{
					Operation: tt.tool, Connection: "lexware-primary", Arguments: json.RawMessage(arguments),
				}); err == nil {
					t.Errorf("core accepted %s", arguments)
				}
			}
		})
	}
}
