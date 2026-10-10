package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	draftArticleID = "66666666-7777-8888-9999-000000000000"
	layoutID       = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	precedeID      = "12121212-3434-5656-7878-909090909090"
	draftReply     = `{"id":"` + invoiceID + `","version":1}`
)

// draftArgs is a valid argument object; extra members are appended and line replaces the position.
func draftArgs(line, extra string) json.RawMessage {
	return json.RawMessage(`{"voucher_date":"2026-09-12T00:00:00+02:00","address":{"contact_id":"` + invoiceID + `"},` +
		`"line_items":[` + line + `],"currency":"EUR","tax_type":"net","shipping_type":"none"` + extra + `}`)
}

const articleLine = `{"type":"service","id":"` + draftArticleID + `","name":"Consulting","quantity":2,"unit_name":"hour","net_amount":120,"tax_rate_percentage":19}`

// creditArgs and deliveryArgs are valid arguments of the types without shipping or total members.
func creditArgs(extra string) json.RawMessage { return creditArgsLine(articleLine, extra) }

func creditArgsLine(line string, extra ...string) json.RawMessage {
	return json.RawMessage(`{"voucher_date":"2026-09-12T00:00:00+02:00","address":{"contact_id":"` + invoiceID + `"},` +
		`"line_items":[` + line + `],"currency":"EUR","tax_type":"net"` + strings.Join(extra, "") + `}`)
}

func deliveryArgs(extra string) json.RawMessage {
	return deliveryArgsLine(`{"type":"material","id":"`+draftArticleID+`","name":"Bolt","quantity":3,"unit_name":"piece"}`, extra)
}

func deliveryArgsLine(line string, extra ...string) json.RawMessage {
	return json.RawMessage(`{"voucher_date":"2026-09-12T00:00:00+02:00","address":{"contact_id":"` + invoiceID + `"},` +
		`"line_items":[` + line + `],"tax_type":"net","shipping_type":"none"` + strings.Join(extra, "") + `}`)
}

func draftCore(t *testing.T) *application.Core {
	t.Helper()
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	return application.New(registry(t), issueConfig(), resolver(red), red)
}

func invokeDraft(core *application.Core, operation string, args json.RawMessage, confirm bool) error {
	_, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "lexware-creator", Arguments: args, Confirmed: confirm})
	return err
}

func TestDraftPayloads(t *testing.T) {
	type sent struct {
		path, query string
		payload     map[string]any
	}
	tests := []struct {
		name, operation string
		args            json.RawMessage
		path, query     string
		want            map[string]any
	}{
		{"quotation", "lexware.quotations.create",
			draftArgs(articleLine, `,"expiration_date":"2026-10-12T00:00:00+02:00","print_layout_id":"`+layoutID+`",`+
				`"payment_conditions":{"label":"14 days","duration_days":14,"discount_percentage":2,"discount_range_days":7}`),
			"/v1/quotations", "",
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "expirationDate": "2026-10-12T00:00:00+02:00",
				"address":    map[string]any{"contactId": invoiceID},
				"totalPrice": map[string]any{"currency": "EUR"}, "taxConditions": map[string]any{"taxType": "net"},
				"shippingConditions": map[string]any{"shippingType": "none"}, "printLayoutId": layoutID,
				"paymentConditions": map[string]any{"paymentTermLabel": "14 days", "paymentTermDuration": float64(14),
					"paymentDiscountConditions": map[string]any{"discountPercentage": float64(2), "discountRange": float64(7)}},
				"lineItems": []any{map[string]any{"type": "service", "id": draftArticleID, "name": "Consulting",
					"quantity": float64(2), "unitName": "hour",
					"unitPrice": map[string]any{"currency": "EUR", "netAmount": float64(120), "taxRatePercentage": float64(19)}}},
			}},
		{"order confirmation", "lexware.orderconfirmations.create",
			draftArgs(`{"type":"text","name":"Note"}`, ""),
			"/v1/order-confirmations", "",
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "address": map[string]any{"contactId": invoiceID},
				"totalPrice": map[string]any{"currency": "EUR"}, "taxConditions": map[string]any{"taxType": "net"},
				"shippingConditions": map[string]any{"shippingType": "none"},
				"lineItems":          []any{map[string]any{"type": "text", "name": "Note"}},
			}},
		{"credit note", "lexware.creditnotes.create",
			creditArgs(`,"title":"Correction","preceding_voucher_id":"` + precedeID + `"`),
			"/v1/credit-notes", "precedingSalesVoucherId=" + precedeID,
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "address": map[string]any{"contactId": invoiceID},
				"totalPrice": map[string]any{"currency": "EUR"}, "taxConditions": map[string]any{"taxType": "net"},
				"title": "Correction",
				"lineItems": []any{map[string]any{"type": "service", "id": draftArticleID, "name": "Consulting",
					"quantity": float64(2), "unitName": "hour",
					"unitPrice": map[string]any{"currency": "EUR", "netAmount": float64(120), "taxRatePercentage": float64(19)}}},
			}},
		{"credit note without predecessor", "lexware.creditnotes.create", creditArgs(""),
			"/v1/credit-notes", "", nil},
		{"delivery note", "lexware.deliverynotes.create",
			deliveryArgs(`,"delivery_terms":"Free delivery","preceding_voucher_id":"` + precedeID + `"`),
			"/v1/delivery-notes", "precedingSalesVoucherId=" + precedeID,
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "address": map[string]any{"contactId": invoiceID},
				"taxConditions": map[string]any{"taxType": "net"}, "shippingConditions": map[string]any{"shippingType": "none"},
				"deliveryTerms": "Free delivery",
				"lineItems": []any{map[string]any{"type": "material", "id": draftArticleID, "name": "Bolt",
					"quantity": float64(3), "unitName": "piece"}},
			}},
		{"delivery note with prices", "lexware.deliverynotes.create",
			deliveryArgsLine(`{"type":"custom","name":"Bolt","quantity":3,"unit_name":"piece","currency":"EUR","net_amount":2,"tax_rate_percentage":19}`),
			"/v1/delivery-notes", "",
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "address": map[string]any{"contactId": invoiceID},
				"taxConditions": map[string]any{"taxType": "net"}, "shippingConditions": map[string]any{"shippingType": "none"},
				"lineItems": []any{map[string]any{"type": "custom", "name": "Bolt", "quantity": float64(3), "unitName": "piece",
					"unitPrice": map[string]any{"currency": "EUR", "netAmount": float64(2), "taxRatePercentage": float64(19)}}},
			}},
		{"dunning", "lexware.dunnings.create",
			draftArgs(articleLine, `,"preceding_voucher_id":"`+precedeID+`"`),
			"/v1/dunnings", "precedingSalesVoucherId=" + precedeID,
			map[string]any{
				"voucherDate": "2026-09-12T00:00:00+02:00", "address": map[string]any{"contactId": invoiceID},
				"totalPrice": map[string]any{"currency": "EUR"}, "taxConditions": map[string]any{"taxType": "net"},
				"shippingConditions": map[string]any{"shippingType": "none"},
				"lineItems": []any{map[string]any{"type": "service", "id": draftArticleID, "name": "Consulting",
					"quantity": float64(2), "unitName": "hour",
					"unitPrice": map[string]any{"currency": "EUR", "netAmount": float64(120), "taxRatePercentage": float64(19)}}},
			}},
		{"invoice follow-up", "lexware.invoices.create",
			draftArgs(`{"type":"text","name":"Note"}`, `,"preceding_voucher_id":"`+precedeID+`"`),
			"/v1/invoices", "precedingSalesVoucherId=" + precedeID, nil},
		{"follow-up", "lexware.orderconfirmations.create",
			draftArgs(`{"type":"material","id":"`+draftArticleID+`","name":"Bolt","quantity":1,"unit_name":"piece","net_amount":1,"tax_rate_percentage":19}`,
				`,"preceding_voucher_id":"`+precedeID+`"`),
			"/v1/order-confirmations", "precedingSalesVoucherId=" + precedeID, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := draftCore(t)
			var got []sent
			serve(t, func(request *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+primaryKey {
					t.Errorf("request = %s", request.Method)
				}
				got = append(got, sent{request.URL.Path, request.URL.RawQuery, payload})
				return jsonResponse(http.StatusCreated, draftReply), nil
			})
			if err := invokeDraft(core, tt.operation, tt.args, true); err != nil {
				t.Fatalf("Invoke() = %v", err)
			}
			if len(got) != 1 || got[0].path != tt.path || got[0].query != tt.query {
				t.Fatalf("requests = %+v, want one to %s?%s", got, tt.path, tt.query)
			}
			if strings.Contains(got[0].query, "finalize") {
				t.Errorf("query = %q carries finalize", got[0].query)
			}
			if _, ok := got[0].payload["finalize"]; ok {
				t.Error("payload carries finalize")
			}
			if tt.want != nil {
				wantJSON, _ := json.Marshal(tt.want)
				gotJSON, _ := json.Marshal(got[0].payload)
				if string(wantJSON) != string(gotJSON) {
					t.Errorf("payload = %s, want %s", gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestInvoiceTakesArticleLinesAndOptionalFields(t *testing.T) {
	core := draftCore(t)
	var body map[string]any
	serve(t, func(request *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(request.Body).Decode(&body)
		return jsonResponse(http.StatusCreated, draftReply), nil
	})
	args := draftArgs(articleLine, `,"print_layout_id":"`+layoutID+`"`)
	if err := invokeDraft(core, "lexware.invoices.create", args, true); err != nil {
		t.Fatalf("Invoke() = %v", err)
	}
	if body["printLayoutId"] != layoutID || body["lineItems"].([]any)[0].(map[string]any)["id"] != draftArticleID {
		t.Errorf("payload = %v", body)
	}
}

func TestDraftCreationsAreRefusedBeforeIO(t *testing.T) {
	refuse(t)
	core := draftCore(t)
	quotation := `,"expiration_date":"2026-10-12T00:00:00+02:00"`
	tests := []struct {
		name, operation string
		args            json.RawMessage
		confirm         bool
	}{
		{"quotation without confirm", "lexware.quotations.create", draftArgs(articleLine, quotation), false},
		{"order confirmation without confirm", "lexware.orderconfirmations.create", draftArgs(articleLine, ""), false},
		{"quotation without expiration", "lexware.quotations.create", draftArgs(articleLine, ""), true},
		{"quotation with bad expiration", "lexware.quotations.create", draftArgs(articleLine, `,"expiration_date":"soon"`), true},
		{"finalize in quotation", "lexware.quotations.create", draftArgs(articleLine, quotation+`,"finalize":true`), true},
		{"finalize in order confirmation", "lexware.orderconfirmations.create", draftArgs(articleLine, `,"finalize":false`), true},
		{"expiration in order confirmation", "lexware.orderconfirmations.create", draftArgs(articleLine, quotation), true},
		{"preceding id in quotation", "lexware.quotations.create", draftArgs(articleLine, quotation+`,"preceding_voucher_id":"`+precedeID+`"`), true},
		{"preceding id in invoice no UUID", "lexware.invoices.create", draftArgs(articleLine, `,"preceding_voucher_id":"../`+bodyCanary+`"`), true},
		{"credit note without confirm", "lexware.creditnotes.create", creditArgs(`,"preceding_voucher_id":"` + precedeID + `"`), false},
		{"delivery note without confirm", "lexware.deliverynotes.create", deliveryArgs(""), false},
		{"dunning without confirm", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":"`+precedeID+`"`), false},
		{"dunning without predecessor", "lexware.dunnings.create", draftArgs(articleLine, ""), true},
		{"dunning with empty predecessor", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":""`), true},
		{"dunning predecessor no UUID", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":"../`+bodyCanary+`"`), true},
		{"credit note predecessor no UUID", "lexware.creditnotes.create", creditArgs(`,"preceding_voucher_id":"../` + bodyCanary + `"`), true},
		{"delivery note predecessor no UUID", "lexware.deliverynotes.create", deliveryArgs(`,"preceding_voucher_id":"../` + bodyCanary + `"`), true},
		{"finalize in credit note", "lexware.creditnotes.create", creditArgs(`,"finalize":true`), true},
		{"finalize in delivery note", "lexware.deliverynotes.create", deliveryArgs(`,"finalize":true`), true},
		{"finalize in dunning", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":"`+precedeID+`","finalize":true`), true},
		{"shipping in credit note", "lexware.creditnotes.create", creditArgs(`,"shipping_type":"none"`), true},
		{"payment conditions in credit note", "lexware.creditnotes.create", creditArgs(`,"payment_conditions":{"label":"x","duration_days":1}`), true},
		{"payment conditions in delivery note", "lexware.deliverynotes.create", deliveryArgs(`,"payment_conditions":{"label":"x","duration_days":1}`), true},
		{"payment conditions in dunning", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":"`+precedeID+`","payment_conditions":{"label":"x","duration_days":1}`), true},
		{"delivery terms in dunning", "lexware.dunnings.create", draftArgs(articleLine, `,"preceding_voucher_id":"`+precedeID+`","delivery_terms":"x"`), true},
		{"credit note without currency", "lexware.creditnotes.create", json.RawMessage(strings.Replace(string(creditArgs("")), `"currency":"EUR",`, "", 1)), true},
		{"credit note line without price", "lexware.creditnotes.create", creditArgsLine(`{"type":"custom","name":"x","quantity":1,"unit_name":"piece"}`), true},
		{"delivery note with half price", "lexware.deliverynotes.create", deliveryArgsLine(`{"type":"custom","name":"x","quantity":1,"unit_name":"piece","net_amount":1}`), true},
		{"delivery note line without quantity", "lexware.deliverynotes.create", deliveryArgsLine(`{"type":"custom","name":"x","unit_name":"piece"}`), true},
		{"delivery note price without currency", "lexware.deliverynotes.create",
			deliveryArgsLine(`{"type":"custom","name":"x","quantity":1,"unit_name":"piece","net_amount":1,"tax_rate_percentage":19}`), true},
		{"delivery note without shipping", "lexware.deliverynotes.create", json.RawMessage(strings.Replace(string(deliveryArgs("")), `,"shipping_type":"none"`, "", 1)), true},
		{"preceding id no UUID", "lexware.orderconfirmations.create", draftArgs(articleLine, `,"preceding_voucher_id":"../`+bodyCanary+`"`), true},
		{"print layout no UUID", "lexware.quotations.create", draftArgs(articleLine, quotation+`,"print_layout_id":"../`+bodyCanary+`"`), true},
		{"article id no UUID", "lexware.quotations.create",
			draftArgs(strings.Replace(articleLine, draftArticleID, "../"+bodyCanary, 1), quotation), true},
		{"article line without id", "lexware.quotations.create",
			draftArgs(strings.Replace(articleLine, `"id":"`+draftArticleID+`",`, "", 1), quotation), true},
		{"id on custom line", "lexware.quotations.create",
			draftArgs(strings.Replace(articleLine, `"service"`, `"custom"`, 1), quotation), true},
		{"article line without price", "lexware.orderconfirmations.create",
			draftArgs(`{"type":"material","id":"`+draftArticleID+`","name":"Bolt"}`, ""), true},
		{"contact id no UUID", "lexware.orderconfirmations.create",
			json.RawMessage(strings.Replace(string(draftArgs(articleLine, "")), invoiceID, "../"+bodyCanary, 1)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := invokeDraft(core, tt.operation, tt.args, tt.confirm)
			if err == nil {
				t.Fatal("the core accepted the request")
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error repeats the rejected value: %v", err)
			}
		})
	}
}

func TestDraftCreationReportsUncertaintyWithoutRetry(t *testing.T) {
	kinds := []struct {
		name string
		call func(*Client, context.Context, createInput) (*createResult, error)
		hint string
	}{
		{"quotation", (*Client).CreateQuotation, quotationDraft.mayExist},
		{"order confirmation", (*Client).CreateOrderConfirmation, orderConfirmationDraft.mayExist},
		{"credit note", (*Client).CreateCreditNote, creditNoteDraft.mayExist},
		{"delivery note", (*Client).CreateDeliveryNote, deliveryNoteDraft.mayExist},
		{"dunning", (*Client).CreateDunning, dunningDraft.mayExist},
	}
	replies := []struct {
		name   string
		status int
		hint   bool
	}{{"500", 500, true}, {"503", 503, true}, {"400", 400, false}, {"409", 409, false}}
	for _, kind := range kinds {
		for _, reply := range replies {
			t.Run(kind.name+" "+reply.name, func(t *testing.T) {
				requests := 0
				serve(t, func(*http.Request) (*http.Response, error) {
					requests++
					return jsonResponse(reply.status, `{"message":"`+bodyCanary+`"}`), nil
				})
				c, _ := client(t)
				_, err := kind.call(c, context.Background(), minimalCreateInput())
				if err == nil {
					t.Fatal("call succeeded")
				}
				if got := strings.Contains(err.Error(), kind.hint); got != reply.hint {
					t.Errorf("hint = %v, want %v (%v)", got, reply.hint, err)
				}
				if requests != 1 || strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("requests = %d, error = %v", requests, err)
				}
			})
		}
	}
}

func TestFollowUpRejectionHasFixedMessage(t *testing.T) {
	kinds := []struct {
		name string
		call func(*Client, context.Context, createInput) (*createResult, error)
		noun string
	}{
		{"invoice", (*Client).CreateInvoice, "an invoice"},
		{"order confirmation", (*Client).CreateOrderConfirmation, "an order confirmation"},
		{"credit note", (*Client).CreateCreditNote, "a credit note"},
		{"delivery note", (*Client).CreateDeliveryNote, "a delivery note"},
		{"dunning", (*Client).CreateDunning, "a dunning"},
	}
	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusNotAcceptable, `{"message":"`+bodyCanary+`"}`), nil
			})
			c, _ := client(t)
			input := minimalCreateInput()
			input.PrecedingVoucherID = precedeID
			_, err := kind.call(c, context.Background(), input)
			if err == nil || classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "followed up by "+kind.noun+",") ||
				strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error = %v, want the fixed follow-up message", err)
			}
			if kind.name == "dunning" {
				return
			}
			// Without a predecessor the generic validation message stays.
			_, err = kind.call(c, context.Background(), minimalCreateInput())
			if err == nil || !strings.Contains(err.Error(), validationMessage) {
				t.Errorf("error = %v, want the validation message", err)
			}
		})
	}
}

func TestPrecedingQueryIsEncoded(t *testing.T) {
	var query url.Values
	serve(t, func(request *http.Request) (*http.Response, error) {
		query = request.URL.Query()
		return jsonResponse(http.StatusCreated, draftReply), nil
	})
	c, _ := client(t)
	input := minimalCreateInput()
	input.PrecedingVoucherID = precedeID
	if _, err := c.CreateOrderConfirmation(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(query) != 1 || query.Get("precedingSalesVoucherId") != precedeID {
		t.Errorf("query = %v", query)
	}
}

func TestPrecedingQueryOfEveryFollowUpType(t *testing.T) {
	calls := map[string]func(*Client, context.Context, createInput) (*createResult, error){
		"invoice": (*Client).CreateInvoice, "credit note": (*Client).CreateCreditNote,
		"delivery note": (*Client).CreateDeliveryNote, "dunning": (*Client).CreateDunning,
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			var query url.Values
			serve(t, func(request *http.Request) (*http.Response, error) {
				query = request.URL.Query()
				return jsonResponse(http.StatusCreated, draftReply), nil
			})
			c, _ := client(t)
			input := minimalCreateInput()
			input.PrecedingVoucherID = precedeID
			if _, err := call(c, context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if len(query) != 1 || query.Get("precedingSalesVoucherId") != precedeID {
				t.Errorf("query = %v", query)
			}
		})
	}
}
