package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const existingVoucher = "5b0c8f1e-2a3d-4e6f-9a7b-1c2d3e4f5a6b"

// storedVoucherWith carries members Qatlas does not model next to the modelled ones.
func storedVoucherWith(status string) string {
	return `{"id":"` + existingVoucher + `","organizationId":"org-1","type":"purchaseinvoice","voucherStatus":"` + status + `",` +
		`"voucherNumber":"ALT-1","voucherDate":"2026-01-02","shippingDate":"2026-01-03","dueDate":"2026-02-01",` +
		`"totalGrossAmount":10.10,"totalTaxAmount":1.6,"taxType":"gross","useCollectiveContact":false,` +
		`"contactId":"` + contactID + `","remark":"intern","files":["f1f1f1f1-0000-4000-8000-000000000001"],` +
		`"voucherItems":[{"amount":10.10,"taxAmount":1.6,"taxRatePercent":19,"categoryId":"` + categoryID + `","futureItem":1}],` +
		`"futureField":{"a":[1,2.50]},"version":7}`
}

func voucherGetAs(status string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return jsonResponse(http.StatusOK, storedVoucherWith(status)), nil }
}

func voucherPutOK() (*http.Response, error) {
	return jsonResponse(http.StatusOK, `{"id":"`+existingVoucher+`","resourceUri":"x","createdDate":"c","updatedDate":"u","version":8}`), nil
}

// bookCore offers the book and update tools on one connection that names them.
func bookCore(t *testing.T) (*application.Core, *atomic.Int32) {
	t.Helper()
	stubLimiter(t, primaryKey)
	cfg := coreConfig()
	cfg.Connections["lexware-vouchers"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Effects: []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate},
		Tools:   []string{"lexware.vouchers.book", "lexware.vouchers.update"}}
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	return application.New(registry(t), cfg, counting, red), &reads
}

func bookArgs(id string) json.RawMessage { return json.RawMessage(`{"id":"` + id + `"}`) }

func TestBookVoucherByIDSendsOnePutWithOnlyTheStatusChanged(t *testing.T) {
	requests := scripted(t, voucherGetAs("unchecked"), voucherPutOK)
	core, _ := bookCore(t)
	result, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.vouchers.book", Connection: "lexware-vouchers", Confirmed: true, Arguments: bookArgs(existingVoucher)})
	if err != nil || !strings.Contains(string(result.Result), `"version":8`) {
		t.Fatalf("invoke = %s, %v", result.Result, err)
	}
	if len(*requests) != 2 || (*requests)[0].method != http.MethodGet || (*requests)[1].method != http.MethodPut ||
		(*requests)[1].path != "/v1/vouchers/"+existingVoucher {
		t.Fatalf("requests = %+v", *requests)
	}
	var want map[string]json.RawMessage
	_ = json.Unmarshal([]byte(storedVoucherWith("unchecked")), &want)
	want["voucherStatus"] = json.RawMessage(`"open"`)
	got := (*requests)[1].body
	if len(got) != len(want) {
		t.Errorf("members = %d, want %d", len(got), len(want))
	}
	for key, value := range want {
		if string(got[key]) != string(value) {
			t.Errorf("%s = %s, want %s", key, got[key], value)
		}
	}
	if string(got["version"]) != "7" || string(got["files"]) != `["f1f1f1f1-0000-4000-8000-000000000001"]` {
		t.Errorf("version = %s, files = %s", got["version"], got["files"])
	}
}

func TestBookVoucherByIDRefusesAnyOtherStatusBeforeThePut(t *testing.T) {
	for _, status := range []string{"open", "voided", "paid", "blank", ""} {
		t.Run(status, func(t *testing.T) {
			requests := scripted(t, voucherGetAs(status), voucherPutOK)
			c, _ := client(t)
			_, err := c.BookVoucher(context.Background(), existingVoucher)
			if err == nil || !strings.Contains(err.Error(), notUncheckedMessage) || strings.Contains(err.Error(), voucherChanged) {
				t.Fatalf("err = %v", err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
				t.Errorf("requests = %+v, want the read alone", *requests)
			}
		})
	}
}

func TestBookVoucherRecordsABookedNetVoucherWithOnePost(t *testing.T) {
	requests := scripted(t, storedGet, voucherCreated)
	core, _ := bookCore(t)
	args := strings.Replace(voucherArgs(`,"use_collective_contact":true`), `"gross"`, `"net"`, 1)
	result, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.vouchers.book", Connection: "lexware-vouchers", Confirmed: true, Arguments: json.RawMessage(args)})
	if err != nil || !strings.Contains(string(result.Result), `"id":"`+newVoucher+`"`) {
		t.Fatalf("invoke = %s, %v", result.Result, err)
	}
	if len(*requests) != 1 || (*requests)[0].method != http.MethodPost || (*requests)[0].path != "/v1/vouchers" {
		t.Fatalf("requests = %+v", *requests)
	}
	sent, _ := json.Marshal((*requests)[0].body)
	want := `{"taxType":"net","totalGrossAmount":119.5,"totalTaxAmount":19,"type":"purchaseinvoice",` +
		`"useCollectiveContact":true,"voucherDate":"2026-05-14","voucherItems":` +
		`[{"amount":100,"categoryId":"` + categoryID + `","taxAmount":19,"taxRatePercent":19}],"voucherStatus":"open"}`
	if string(sent) != want {
		t.Errorf("payload = %s\nwant      %s", sent, want)
	}
}

func TestVoucherBookAndUpdateAreRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	core, reads := bookCore(t)
	collective := `,"use_collective_contact":true`
	withID := func(args string) string { return strings.Replace(args, "{", `{"id":"`+existingVoucher+`",`, 1) }
	tests := []struct {
		name, op, args string
		confirm        bool
	}{
		{"book without confirm", "book", string(bookArgs(existingVoucher)), false},
		{"book new without confirm", "book", voucherArgs(collective), false},
		{"book id with fields", "book", withID(voucherArgs(collective)), true},
		{"book id with one field", "book", `{"id":"` + existingVoucher + `","voucher_number":"x"}`, true},
		{"book id not a uuid", "book", `{"id":"../vouchers/x"}`, true},
		{"book without anything", "book", `{}`, true},
		{"book new without contact", "book", voucherArgs(``), true},
		{"book new with files", "book", voucherArgs(collective + `,"files":[]`), true},
		{"book new with status", "book", voucherArgs(collective + `,"voucher_status":"open"`), true},
		{"update without confirm", "update", withID(voucherArgs(collective)), false},
		{"update without id", "update", voucherArgs(collective), true},
		{"update id not a uuid", "update", strings.Replace(voucherArgs(collective), "{", `{"id":"../x",`, 1), true},
		{"update without fields", "update", string(bookArgs(existingVoucher)), true},
		{"update both contacts", "update", withID(voucherArgs(`,"contact_id":"` + contactID + `"` + collective)), true},
		{"update with files", "update", withID(voucherArgs(collective + `,"files":[]`)), true},
		{"update with remark", "update", withID(voucherArgs(collective + `,"remark":"x"`)), true},
		{"update with status", "update", withID(voucherArgs(collective + `,"voucher_status":"open"`)), true},
		{"update 101 positions", "update", withID(strings.Replace(voucherArgs(collective), voucherItemArgs, "["+
			strings.TrimSuffix(strings.Repeat(`{"amount":1,"tax_amount":0,"tax_rate_percent":0,"category_id":"`+categoryID+`"},`, 101), ",")+"]", 1)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: "lexware.vouchers." + tt.op, Connection: "lexware-vouchers",
				Arguments: json.RawMessage(tt.args), Confirmed: tt.confirm})
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

func TestBookHandlerRechecksTheFormsWithoutTheSchema(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	if _, err := c.BookVoucher(context.Background(), "../x"); err == nil {
		t.Error("BookVoucher accepted an identifier that is no UUID")
	}
	net := voucherInput{Type: "purchaseinvoice", VoucherDate: "2026-05-14", TotalGrossAmount: "1", TotalTaxAmount: "0",
		TaxType: "net", ContactID: contactID, UseCollectiveContact: true,
		VoucherItems: []voucherItemData{{Amount: "1", TaxAmount: "0", TaxRatePercent: "0", CategoryID: categoryID}}}
	if _, err := c.RecordBookedVoucher(context.Background(), net); err == nil {
		t.Error("RecordBookedVoucher accepted two contacts")
	}
	if _, err := c.UpdateVoucher(context.Background(), existingVoucher, net); err == nil {
		t.Error("UpdateVoucher accepted two contacts")
	}
	if _, err := c.UpdateVoucher(context.Background(), "../x", net); err == nil {
		t.Error("UpdateVoucher accepted an identifier that is no UUID")
	}
}

func updateInput(contact string, collective bool) voucherInput {
	return voucherInput{Type: "salesinvoice", VoucherDate: "2026-03-04", TotalGrossAmount: "238", TotalTaxAmount: "38",
		TaxType: "net", ContactID: contact, UseCollectiveContact: collective,
		VoucherItems: []voucherItemData{
			{Amount: "100", TaxAmount: "19", TaxRatePercent: "19", CategoryID: categoryID},
			{Amount: "100", TaxAmount: "19", TaxRatePercent: "19", CategoryID: categoryID}}}
}

func TestUpdateVoucherReplacesOnlyTheSchemaFieldsAndKeepsFilesAndOtherMembers(t *testing.T) {
	requests := scripted(t, voucherGetAs("open"), voucherPutOK)
	c, _ := client(t)
	result, err := c.UpdateVoucher(context.Background(), existingVoucher, updateInput("", true))
	if err != nil || result.ID != existingVoucher || result.Version != 8 {
		t.Fatalf("UpdateVoucher() = %+v, %v", result, err)
	}
	if len(*requests) != 2 || (*requests)[0].method != http.MethodGet || (*requests)[1].method != http.MethodPut ||
		(*requests)[1].path != "/v1/vouchers/"+existingVoucher {
		t.Fatalf("requests = %+v", *requests)
	}
	var want map[string]json.RawMessage
	_ = json.Unmarshal([]byte(storedVoucherWith("open")), &want)
	item := `{"amount":100,"categoryId":"` + categoryID + `","taxAmount":19,"taxRatePercent":19}`
	for key, value := range map[string]string{"type": `"salesinvoice"`, "voucherDate": `"2026-03-04"`,
		"totalGrossAmount": "238", "totalTaxAmount": "38", "taxType": `"net"`, "useCollectiveContact": "true",
		"voucherStatus": `"open"`, "voucherItems": `[` + item + `,` + item + `]`} {
		want[key] = json.RawMessage(value)
	}
	delete(want, "voucherNumber") // not given, so removed
	delete(want, "dueDate")
	delete(want, "contactId") // collective contact excludes the contact
	got := (*requests)[1].body
	if len(got) != len(want) {
		t.Errorf("members = %d, want %d: %v", len(got), len(want), got)
	}
	for key, value := range want {
		if string(got[key]) != string(value) {
			t.Errorf("%s = %s, want %s", key, got[key], value)
		}
	}
	if string(got["version"]) != "7" || string(got["files"]) != `["f1f1f1f1-0000-4000-8000-000000000001"]` ||
		string(got["remark"]) != `"intern"` || string(got["futureField"]) != `{"a":[1,2.50]}` ||
		string(got["shippingDate"]) != `"2026-01-03"` {
		t.Errorf("kept members changed: %v", got)
	}
}

func TestUpdateVoucherSetsAContactAndDropsTheCollectiveFlag(t *testing.T) {
	requests := scripted(t, func() (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Replace(storedVoucherWith("open"),
			`"useCollectiveContact":false,"contactId":"`+contactID+`"`, `"useCollectiveContact":true`, 1)), nil
	}, voucherPutOK)
	c, _ := client(t)
	input := updateInput(otherContactID, false)
	input.VoucherNumber, input.DueDate = "NEU-2", "2026-04-01"
	if _, err := c.UpdateVoucher(context.Background(), existingVoucher, input); err != nil {
		t.Fatalf("UpdateVoucher() = %v", err)
	}
	got := (*requests)[1].body
	if string(got["contactId"]) != `"`+otherContactID+`"` || got["useCollectiveContact"] != nil ||
		string(got["voucherNumber"]) != `"NEU-2"` || string(got["dueDate"]) != `"2026-04-01"` {
		t.Errorf("body = %v", got)
	}
}

func TestUpdateVoucherRefusesAnyOtherStatusBeforeThePut(t *testing.T) {
	for _, status := range []string{"unchecked", "voided", "paid", "blank", ""} {
		t.Run(status, func(t *testing.T) {
			requests := scripted(t, voucherGetAs(status), voucherPutOK)
			c, _ := client(t)
			_, err := c.UpdateVoucher(context.Background(), existingVoucher, updateInput("", true))
			if err == nil || !strings.Contains(err.Error(), notOpenMessage) || strings.Contains(err.Error(), voucherChanged) {
				t.Fatalf("err = %v", err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
				t.Errorf("requests = %+v, want the read alone", *requests)
			}
		})
	}
}

func TestVoucherPutFailuresHintOnlyWhenTheOutcomeIsUnclear(t *testing.T) {
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
		{"409", status(409), provider.ClassProviderError, conflictMessage, false},
		{"406", status(406), provider.ClassProviderError, validationMessage, false},
		{"500", status(500), provider.ClassProviderError, "", true},
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }, provider.ClassTimeout, "", true},
		{"reset", func() (*http.Response, error) { return nil, reset }, provider.ClassUnreachable, "", true},
		{"other voucher", func() (*http.Response, error) {
			return jsonResponse(200, `{"id":"`+newVoucher+`","version":8}`), nil
		}, provider.ClassInvalidResponse, "", true},
	}
	calls := map[string]func(*Client) error{
		"book": func(c *Client) error { _, err := c.BookVoucher(context.Background(), existingVoucher); return err },
		"update": func(c *Client) error {
			_, err := c.UpdateVoucher(context.Background(), existingVoucher, updateInput("", true))
			return err
		},
	}
	for name, call := range calls {
		for _, tt := range tests {
			t.Run(name+" "+tt.name, func(t *testing.T) {
				get := voucherGetAs("open")
				if name == "book" {
					get = voucherGetAs("unchecked")
				}
				requests := scripted(t, get, tt.reply)
				c, _ := client(t)
				err := call(c)
				if err == nil || classOf(err) != tt.class || strings.Contains(err.Error(), bodyCanary) {
					t.Fatalf("err = %v, want class %q", err, tt.class)
				}
				if got := strings.Contains(err.Error(), voucherChanged); got != tt.hint {
					t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
				}
				if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
					t.Errorf("err = %v, want %q", err, tt.text)
				}
				puts := 0
				for _, request := range *requests {
					if request.method == http.MethodPut {
						puts++
					}
				}
				if len(*requests) != 2 || puts != 1 {
					t.Errorf("requests = %+v, want one read and exactly one PUT", *requests)
				}
			})
		}
	}
}

func TestBookedVoucherPostFailuresHintOnlyWhenTheOutcomeIsUnclear(t *testing.T) {
	input := updateInput("", true)
	for name, tt := range map[string]struct {
		reply func() (*http.Response, error)
		hint  bool
	}{
		"500":     {func() (*http.Response, error) { return jsonResponse(500, "{}"), nil }, true},
		"timeout": {func() (*http.Response, error) { return nil, context.DeadlineExceeded }, true},
		"406":     {func() (*http.Response, error) { return jsonResponse(406, "{}"), nil }, false},
	} {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.RecordBookedVoucher(context.Background(), input)
			if err == nil || strings.Contains(err.Error(), voucherMayExist) != tt.hint {
				t.Fatalf("err = %v, want hint %v", err, tt.hint)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost {
				t.Errorf("requests = %+v, want exactly one POST", *requests)
			}
		})
	}
}

func TestVoucherBookAndUpdateAreOfferedOnlyThroughAToolEntry(t *testing.T) {
	want := map[string]capability.Risk{
		"lexware.vouchers.book": {Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: bookkeepingSensitivity},
		"lexware.vouchers.update": {Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: bookkeepingSensitivity},
	}
	reg := registry(t)
	for _, descriptor := range reg.Provider(Provider) {
		if risk, ok := want[descriptor.ID]; ok {
			delete(want, descriptor.ID)
			if descriptor.Risk != risk || !descriptor.RequiresToolAllowList {
				t.Errorf("%s = %+v", descriptor.ID, descriptor)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == vouchersBook.ID || id == vouchersUpdate.ID {
				t.Errorf("profile %s selects %s", profile.ID, id)
			}
		}
	}

	stubLimiter(t, primaryKey)
	var methods []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.Method == http.MethodGet {
			return voucherGetAs("unchecked")()
		}
		return voucherPutOK()
	})
	cfg := coreConfig()
	permissions := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate}
	cfg.Connections["lexware-plain"] = config.Connection{Service: "lexware", Credential: "primary-key", Effects: permissions}
	cfg.Connections["lexware-booker"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Effects: permissions, Tools: []string{"lexware.vouchers.book"}}
	red := &redact.Redactor{}
	core := application.New(reg, cfg, resolver(red), red)
	invoke := func(connection, operation string) error {
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: operation, Connection: connection, Arguments: bookArgs(existingVoucher), Confirmed: true})
		return err
	}
	for _, connection := range []string{"lexware-plain", "lexware-primary", "lexware-writer"} {
		for _, operation := range []string{"lexware.vouchers.book", "lexware.vouchers.update"} {
			if invoke(connection, operation) == nil {
				t.Errorf("%s offered %s without a tools entry", connection, operation)
			}
		}
	}
	if invoke("lexware-booker", "lexware.vouchers.update") == nil {
		t.Error("update was offered by a connection that names book only")
	}
	if len(methods) != 0 {
		t.Fatalf("requests before a valid call = %v, want none", methods)
	}
	if err := invoke("lexware-booker", "lexware.vouchers.book"); err != nil {
		t.Fatalf("book on a connection that names it = %v", err)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodPut {
		t.Errorf("methods = %v, want one GET and one PUT", methods)
	}
}
