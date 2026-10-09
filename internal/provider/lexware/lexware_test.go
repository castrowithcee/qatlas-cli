package lexware

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The canaries stand for an API key and for provider content. No test reaches a productive organization:
// every request is answered by the package's own transport seam.
const (
	primaryKey   = "canary-lexware-primary-key-8f2c41"
	archiveKey   = "canary-lexware-archive-key-3ad907"
	primaryEnv   = "TEST_LEXWARE_PRIMARY_KEY"
	archiveEnv   = "TEST_LEXWARE_ARCHIVE_KEY"
	invoiceID    = "f3d3ae48-30d9-4b56-973a-b3159cbe743c"
	bodyCanary   = "provider-body-canary-lexware-51ab"
	sampleNumber = "RE1012"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// serve replaces the package transport for one test. Production keeps Go's default transport.
func serve(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(handler)
	t.Cleanup(func() { transport = previous })
}

// refuse fails the test when any provider request is attempted.
func refuse(t *testing.T) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", request.Method, request.URL.Path)
		return nil, errors.New("no request expected")
	})
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func resolvedConnection(name, credential, env string) *config.Resolved {
	return &config.Resolved{
		Name: name, Provider: Provider, BaseURL: gateway, Service: "lexware", Credential: credential,
		Secrets: config.Credential{
			Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: env},
		},
	}
}

func resolver(red *redact.Redactor) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		switch name {
		case primaryEnv:
			return primaryKey
		case archiveEnv:
			return archiveKey
		}
		return ""
	}, nil, nil, red)
}

// freeLimiter is a limiter that never waits. The rate limit itself is proven by its own test.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, ratelimit.Sleep)
}

// client opens the primary connection with the package transport currently installed.
func client(t *testing.T) (*Client, *redact.Redactor) {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	return c, red
}

const listBody = `{
  "content": [
    {"id":"f3d3ae48-30d9-4b56-973a-b3159cbe743c","voucherType":"invoice","voucherStatus":"open",
     "voucherNumber":"RE1012","voucherDate":"2026-05-14T00:00:00.000+02:00",
     "createdDate":"2026-05-14T16:52:21.000+02:00","updatedDate":"2026-05-14T16:52:21.000+02:00",
     "dueDate":"2026-05-24T00:00:00.000+02:00","contactId":"777c7793-9fbb-4ec7-9254-0619c199761e",
     "contactName":"Musterfrau, Erika","totalAmount":99.8,"openAmount":74.8,"currency":"EUR","archived":false},
    {"id":"55aa6de8-d32d-47bd-9c3c-d541ab65a8e8","voucherType":"invoice","voucherStatus":"overdue",
     "voucherNumber":"RE1011","voucherDate":"2026-03-02T00:00:00.000+01:00",
     "dueDate":"2026-04-06T00:00:00.000+02:00","contactId":null,"contactName":"Test GmbH",
     "totalAmount":498.8,"openAmount":null,"currency":"EUR","archived":false},
    {"id":"11111111-1111-1111-1111-111111111111","voucherType":"purchaseinvoice","voucherStatus":"open",
     "voucherNumber":"2010096","voucherDate":"2026-06-14T00:00:00.000+02:00","contactName":"Sammellieferant",
     "totalAmount":80.04,"openAmount":80.04,"currency":"EUR","archived":false},
    {"id":"22222222-2222-2222-2222-222222222222","voucherType":"invoice","voucherStatus":"open",
     "voucherNumber":"RE0900","voucherDate":"2025-01-02T00:00:00.000+01:00","contactName":"Archiv GmbH",
     "totalAmount":10,"openAmount":10,"currency":"EUR","archived":true}
  ],
  "first": true, "last": false, "totalPages": 3, "totalElements": 57, "numberOfElements": 4,
  "size": 25, "number": 0
}`

const invoiceBody = `{
  "id":"f3d3ae48-30d9-4b56-973a-b3159cbe743c","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8",
  "createdDate":"2026-05-14T16:52:21.000+02:00","updatedDate":"2026-05-14T16:52:21.000+02:00",
  "version":2,"language":"de","archived":false,"voucherStatus":"overdue","voucherNumber":"RE1012",
  "voucherDate":"2026-05-14T00:00:00.000+02:00","dueDate":"2026-05-24T00:00:00.000+02:00",
  "address":{"contactId":"777c7793-9fbb-4ec7-9254-0619c199761e","name":"Bike & Ride GmbH & Co. KG",
   "supplement":"Gebäude 10","street":"Musterstraße 42","city":"Freiburg","zip":"79112","countryCode":"DE"},
  "lineItems":[
    {"id":"97b98491-e953-4dc9-97a9-ae437a8052b4","type":"material","name":"Abus Kabelschloss",
     "description":"` + bodyCanary + `","quantity":2,"unitName":"Stück",
     "unitPrice":{"currency":"EUR","netAmount":13.4,"grossAmount":15.95,"taxRatePercentage":19},
     "discountPercentage":50,"lineItemAmount":13.4}],
  "totalPrice":{"currency":"EUR","totalNetAmount":26.72,"totalGrossAmount":29.85,"totalTaxAmount":3.13},
  "taxAmounts":[{"taxRatePercentage":19,"taxAmount":2.55,"netAmount":13.4}],
  "taxConditions":{"taxType":"net"},
  "shippingConditions":{"shippingType":"delivery"},
  "title":"Rechnung","introduction":"Ihre bestellten Positionen","remark":"Vielen Dank"
}`

// Register publishes the configuration metadata the TUI needs, two reads, a draft creation, and the
// allow-list-only issue operation.
func TestRegisterPublishesMetadataAndTheInvoiceOperations(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Lexware Office" || metadata.DefaultBaseURL != gateway ||
		metadata.Target.Required || metadata.Target.Multiple || metadata.Target.Validate == nil || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleAPIKey || metadata.SecretRoles[0].Description == "" {
		t.Fatalf("metadata = %+v, %v", metadata, ok)
	}

	operations := reg.Provider(Provider)
	if len(operations) != 30 {
		t.Fatalf("operations = %d, want four invoice, four contact, four article, three voucher, six sales voucher, two recurring template, two download, and five account and reference operations", len(operations))
	}
	versions := map[string]int{"lexware.invoices.create": 2, "lexware.invoices.issue": 1,
		"lexware.invoices.get": 1, "lexware.invoices.list": 1, "lexware.contacts.get": 1,
		"lexware.contacts.list": 1, "lexware.contacts.create": 1, "lexware.contacts.update": 1, "lexware.articles.get": 1, "lexware.articles.list": 1, "lexware.articles.create": 1, "lexware.articles.update": 1,
		"lexware.voucherlist.list": 1, "lexware.payments.get": 1, "lexware.vouchers.get": 1,
		"lexware.profile.get": 1, "lexware.countries.list": 1, "lexware.paymentconditions.list": 1,
		"lexware.postingcategories.list": 1, "lexware.printlayouts.list": 1,
		"lexware.quotations.get": 1, "lexware.orderconfirmations.get": 1, "lexware.creditnotes.get": 1,
		"lexware.deliverynotes.get": 1, "lexware.dunnings.get": 1, "lexware.downpaymentinvoices.get": 1,
		"lexware.recurringtemplates.list": 1, "lexware.recurringtemplates.get": 1,
		"lexware.documents.download": 1, "lexware.files.download": 1}
	sensitivities := map[string]string{"lexware.contacts.get": contactSensitivity,
		"lexware.contacts.list": contactSensitivity, "lexware.contacts.create": contactSensitivity,
		"lexware.contacts.update": contactSensitivity, "lexware.articles.get": articleSensitivity,
		"lexware.articles.list": articleSensitivity, "lexware.articles.create": articleSensitivity,
		"lexware.articles.update": articleSensitivity, "lexware.vouchers.get": bookkeepingSensitivity, "lexware.files.download": bookkeepingSensitivity,
		"lexware.profile.get": accountSensitivity, "lexware.countries.list": referenceSensitivity,
		"lexware.paymentconditions.list": referenceSensitivity, "lexware.postingcategories.list": referenceSensitivity,
		"lexware.printlayouts.list": referenceSensitivity}
	for _, descriptor := range operations {
		write := descriptor.Risk.Effect == capability.EffectCreate && strings.HasPrefix(descriptor.ID, "lexware.invoices.")
		if descriptor.Version != versions[descriptor.ID] || descriptor.Provider != Provider ||
			!descriptor.Risk.OpenWorld || descriptor.Risk.DataSensitivity != cmpSensitivity(sensitivities, descriptor.ID) {
			t.Errorf("descriptor %s = %+v, want a bounded operation",
				descriptor.ID, descriptor)
		}
		if len(descriptor.Examples) > 0 && strings.Contains(string(descriptor.Examples[0].Arguments), "key") {
			t.Errorf("descriptor %s examples = %s", descriptor.ID, descriptor.Examples)
		}
		if (len(descriptor.Arguments) == 0 && !strings.Contains(string(descriptor.InputSchema), `"properties":{}`)) || len(descriptor.Fields) == 0 || len(descriptor.Examples) == 0 {
			t.Errorf("descriptor %s lacks arguments, fields, or examples", descriptor.ID)
		}
		if write {
			wantRisk := capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
				Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
			if descriptor.Risk != wantRisk || strings.Contains(string(descriptor.InputSchema), "finalize") ||
				descriptor.RequiresToolAllowList != (descriptor.ID == "lexware.invoices.issue") {
				t.Errorf("descriptor %s = %+v", descriptor.ID, descriptor)
			}
		}
	}
	if operations[0].ID != "lexware.articles.create" || operations[29].ID != "lexware.vouchers.get" {
		t.Errorf("operation IDs are not sorted: %+v", operations)
	}
	profiles := map[string]config.ToolProfile{}
	for _, profile := range metadata.Profiles {
		profiles[profile.ID] = profile
		for _, id := range profile.Tools {
			if id == invoicesIssue.ID {
				t.Errorf("profile %s selects the issue tool", profile.ID)
			}
		}
	}
	if len(profiles) != 2 || !profiles["read"].Recommended || len(profiles["read"].Tools) != 22 ||
		profiles["write"].Recommended || profiles["write"].Title != "Master data and drafts" ||
		len(profiles["write"].Tools) != 27 {
		t.Errorf("profiles = %+v", profiles)
	}
}

// cmpSensitivity is the data class a descriptor must carry: the contact, article, and bookkeeping classes, otherwise the
// invoice class.
func cmpSensitivity(special map[string]string, id string) string {
	if class, ok := special[id]; ok {
		return class
	}
	return dataSensitivity
}

// invoiceInput is a valid input for the position-free paths a request test needs.
func invoiceInput() createInput {
	input := createInput{VoucherDate: "2026-09-12T00:00:00+02:00", Currency: "EUR", TaxType: "net", ShippingDate: "2026-09-12T00:00:00+02:00", ShippingType: "service"}
	input.Address.ContactID = invoiceID
	input.LineItems = append(input.LineItems, struct {
		Type        string      `json:"type"`
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		Quantity    json.Number `json:"quantity,omitempty"`
		UnitName    string      `json:"unit_name,omitempty"`
		Currency    string      `json:"currency,omitempty"`
		NetAmount   json.Number `json:"net_amount,omitempty"`
		TaxRate     json.Number `json:"tax_rate_percentage,omitempty"`
		Discount    json.Number `json:"discount_percentage,omitempty"`
	}{Type: "text", Name: "Note"})
	return input
}

func TestCreateInvoicePostsADraftWithoutFinalizeQuery(t *testing.T) {
	requests := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/invoices" || request.URL.RawQuery != "" {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, ok := payload["lineItems"]; !ok {
			t.Error("lineItems missing")
		}
		return jsonResponse(http.StatusCreated, `{"id":"`+invoiceID+`","version":1}`), nil
	})
	c, _ := client(t)
	got, err := c.CreateInvoice(context.Background(), invoiceInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != invoiceID || requests != 1 {
		t.Fatalf("result = %+v, requests = %d", got, requests)
	}
}

func TestIssueInvoicePostsWithExactlyTheFinalizeQuery(t *testing.T) {
	requests := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/invoices" || request.URL.RawQuery != "finalize=true" {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		return jsonResponse(http.StatusCreated, `{"id":"`+invoiceID+`","version":1}`), nil
	})
	c, _ := client(t)
	got, err := c.IssueInvoice(context.Background(), invoiceInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != invoiceID || requests != 1 {
		t.Fatalf("result = %+v, requests = %d", got, requests)
	}
}

func invoiceArgs(extra string) json.RawMessage {
	return json.RawMessage(`{"voucher_date":"2026-09-12T00:00:00+02:00","address":{"contact_id":"` + invoiceID + `"},` +
		`"line_items":[{"type":"text","name":"Note"}],"currency":"EUR","tax_type":"net","shipping_type":"none"` + extra + `}`)
}

// issueConfig is coreConfig with a connection that may create but names no tool, and one that names both.
func issueConfig() *config.Config {
	cfg := coreConfig()
	cfg.Connections["lexware-creator"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate}}
	cfg.Connections["lexware-issuer"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Permissions: []config.Permission{config.PermissionCreate},
		Tools:       []string{"lexware.invoices.create", "lexware.invoices.issue"}}
	return cfg
}

func TestIssueIsOfferedOnlyThroughAnExplicitToolEntry(t *testing.T) {
	stubLimiter(t, primaryKey)
	var queries []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		queries = append(queries, request.URL.RawQuery)
		return jsonResponse(http.StatusCreated, `{"id":"`+invoiceID+`","version":1}`), nil
	})
	red := &redact.Redactor{}
	core := application.New(registry(t), issueConfig(), resolver(red), red)
	invoke := func(operation, connection string, args json.RawMessage, confirm bool) error {
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: operation, Connection: connection, Arguments: args, Confirmed: confirm})
		return err
	}

	for _, connection := range []string{"lexware-creator", "lexware-primary"} {
		if err := invoke("lexware.invoices.issue", connection, invoiceArgs(""), true); err == nil {
			t.Errorf("%s offered lexware.invoices.issue without a tools entry", connection)
		}
	}
	if len(queries) != 0 {
		t.Fatalf("requests before the offer = %v, want none", queries)
	}
	if err := invoke("lexware.invoices.create", "lexware-creator", invoiceArgs(""), true); err != nil {
		t.Fatalf("create on a permission connection = %v", err)
	}
	if err := invoke("lexware.invoices.issue", "lexware-issuer", invoiceArgs(""), true); err != nil {
		t.Fatalf("issue on a connection that names it = %v", err)
	}
	if len(queries) != 2 || queries[0] != "" || queries[1] != "finalize=true" {
		t.Errorf("queries = %q, want a draft and an issue request", queries)
	}
}

func TestInvoiceCreationsAreRefusedBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), issueConfig(), resolver(red), red)
	tests := []struct {
		name, operation, connection string
		args                        json.RawMessage
		confirm                     bool
	}{
		{"create without confirm", "lexware.invoices.create", "lexware-creator", invoiceArgs(""), false},
		{"issue without confirm", "lexware.invoices.issue", "lexware-issuer", invoiceArgs(""), false},
		{"finalize in create", "lexware.invoices.create", "lexware-creator", invoiceArgs(`,"finalize":true`), true},
		{"finalize false in create", "lexware.invoices.create", "lexware-creator", invoiceArgs(`,"finalize":false`), true},
		{"finalize in issue", "lexware.invoices.issue", "lexware-issuer", invoiceArgs(`,"finalize":true`), true},
		{"contact id that is no UUID", "lexware.invoices.create", "lexware-creator",
			json.RawMessage(strings.Replace(string(invoiceArgs("")), invoiceID, "../v1/contacts/"+bodyCanary, 1)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.operation, Connection: tt.connection, Arguments: tt.args, Confirmed: tt.confirm})
			if err == nil {
				t.Fatal("the core accepted the request")
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error repeats the rejected value: %v", err)
			}
			if strings.Contains(tt.name, "finalize") && string(application.ErrorCode(err)) != "invalid-request" {
				t.Errorf("code = %q, want invalid-request", application.ErrorCode(err))
			}
		})
	}
	var confirmation *application.ConfirmationRequiredError
	_, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.invoices.issue", Connection: "lexware-issuer", Arguments: invoiceArgs("")})
	if !errors.As(err, &confirmation) {
		t.Errorf("issue without confirm = %v, want a confirmation-required error", err)
	}
}

// The list request carries exactly the fixed voucher filters plus the controlled ones, and nothing else.
func TestListInvoicesSendsOnlyTheFixedAndControlledFilters(t *testing.T) {
	var got url.Values
	calls := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" ||
			request.URL.Host != "api.lexware.io" || request.URL.Path != "/v1/voucherlist" {
			t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer "+primaryKey {
			t.Errorf("Authorization header = %q, want the bearer API key", authorization)
		}
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	if _, err := c.ListInvoices(context.Background(), ListOptions{
		VoucherNumber: sampleNumber, VoucherDateFrom: "2026-01-01", VoucherDateTo: "2026-12-31",
		Page: 2, Size: 50, Sort: "voucher_number", Direction: "asc",
	}); err != nil {
		t.Fatalf("ListInvoices() = %v", err)
	}

	if calls != 1 {
		t.Fatalf("requests = %d, want exactly one", calls)
	}
	want := url.Values{
		"voucherType": {"invoice"}, "voucherStatus": {"open"}, "archived": {"false"},
		"page": {"2"}, "size": {"50"}, "sort": {"voucherNumber,ASC"},
		"voucherNumber": {sampleNumber}, "voucherDateFrom": {"2026-01-01"}, "voucherDateTo": {"2026-12-31"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
}

// Omitted arguments produce the deterministic default page, and nothing widens the fixed filters.
func TestListInvoicesUsesDeterministicDefaults(t *testing.T) {
	var got url.Values
	serve(t, func(request *http.Request) (*http.Response, error) {
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	if _, err := c.ListInvoices(context.Background(), ListOptions{}); err != nil {
		t.Fatalf("ListInvoices() = %v", err)
	}
	want := url.Values{
		"voucherType": {"invoice"}, "voucherStatus": {"open"}, "archived": {"false"},
		"page": {"0"}, "size": {"25"}, "sort": {"voucherDate,DESC"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
}

// Open and overdue invoices are kept, the overdue special case is made explicit, and a record outside the
// confirmed workflow is dropped. The page metadata is normalised alongside them.
func TestListInvoicesKeepsOpenAndOverdueAndDropsForeignRecords(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	result, err := c.ListInvoices(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListInvoices() = %v", err)
	}

	want := &ListResult{
		Invoices: []ListInvoice{
			{
				ID: invoiceID, VoucherNumber: "RE1012", VoucherStatus: "open", Overdue: false,
				VoucherDate: "2026-05-14T00:00:00.000+02:00", DueDate: "2026-05-24T00:00:00.000+02:00",
				ContactName: "Musterfrau, Erika", TotalAmount: "99.8", OpenAmount: "74.8", Currency: "EUR",
			},
			{
				ID: "55aa6de8-d32d-47bd-9c3c-d541ab65a8e8", VoucherNumber: "RE1011",
				VoucherStatus: "overdue", Overdue: true, VoucherDate: "2026-03-02T00:00:00.000+01:00",
				DueDate: "2026-04-06T00:00:00.000+02:00", ContactName: "Test GmbH",
				TotalAmount: "498.8", Currency: "EUR",
			},
		},
		Page: 0, Size: 25, TotalPages: 3, TotalInvoices: 57, LastPage: false,
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %#v, want %#v", result, want)
	}
}

// Pagination stays deterministic: the requested page reaches the provider and its answer is reported back
// unchanged, including the last-page marker.
func TestListInvoicesPaginatesDeterministically(t *testing.T) {
	pages := map[string]string{
		"0": `{"content":[],"totalPages":2,"totalElements":30,"size":25,"number":0,"last":false}`,
		"1": `{"content":[],"totalPages":2,"totalElements":30,"size":25,"number":1,"last":true}`,
	}
	requested := make([]string, 0, 2)
	serve(t, func(request *http.Request) (*http.Response, error) {
		page := request.URL.Query().Get("page")
		requested = append(requested, page)
		return jsonResponse(http.StatusOK, pages[page]), nil
	})

	c, _ := client(t)
	for _, page := range []int{0, 1} {
		result, err := c.ListInvoices(context.Background(), ListOptions{Page: page})
		if err != nil {
			t.Fatalf("ListInvoices(page %d) = %v", page, err)
		}
		if result.Page != page || result.TotalPages != 2 || result.TotalInvoices != 30 ||
			result.LastPage != (page == 1) || len(result.Invoices) != 0 {
			t.Errorf("page %d = %#v", page, result)
		}
	}
	if !reflect.DeepEqual(requested, []string{"0", "1"}) {
		t.Errorf("requested pages = %v, want one request per page", requested)
	}
}

// Bounds and formats are refused before any provider I/O happens.
func TestListInvoicesRejectsUnusableOptionsBeforeIO(t *testing.T) {
	refuse(t)
	c, _ := client(t)

	tests := map[string]ListOptions{
		"page size zero is impossible":  {Size: -1},
		"page size above the bound":     {Size: maxPageSize + 1},
		"negative page":                 {Page: -1},
		"page above the bound":          {Page: maxPage + 1},
		"unknown sort property":         {Sort: "contact_name"},
		"unknown sort direction":        {Direction: "sideways"},
		"malformed voucher date filter": {VoucherDateFrom: "14.05.2026"},
		"impossible voucher date":       {VoucherDateTo: "2026-13-45"},
		"oversized voucher number":      {VoucherNumber: strings.Repeat("R", 65)},
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := c.ListInvoices(context.Background(), options); err == nil {
				t.Fatal("ListInvoices() accepted the options")
			}
		})
	}
}

// A list record without a usable identifier is a contract violation, not a result.
func TestListInvoicesRejectsAnUnusableIdentifier(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK,
			`{"content":[{"id":"not-a-uuid","voucherType":"invoice","voucherNumber":"RE1"}],"size":25}`), nil
	})

	c, _ := client(t)
	_, err := c.ListInvoices(context.Background(), ListOptions{})
	if class := classOf(err); class != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
	}
}

// The get operation reads exactly the requested invoice and produces no other provider I/O.
func TestGetInvoiceReadsExactlyTheRequestedInvoice(t *testing.T) {
	requests := make([]string, 0, 1)
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
		return jsonResponse(http.StatusOK, invoiceBody), nil
	})

	c, _ := client(t)
	invoice, err := c.GetInvoice(context.Background(), invoiceID)
	if err != nil {
		t.Fatalf("GetInvoice() = %v", err)
	}
	if !reflect.DeepEqual(requests, []string{"GET /v1/invoices/" + invoiceID + "?"}) {
		t.Fatalf("requests = %v, want exactly one read of the invoice", requests)
	}

	if invoice.ID != invoiceID || invoice.VoucherNumber != "RE1012" ||
		invoice.VoucherStatus != "overdue" || !invoice.Overdue || invoice.Archived ||
		invoice.TaxType != "net" || invoice.Currency != "EUR" ||
		invoice.TotalNetAmount != "26.72" || invoice.TotalGrossAmount != "29.85" ||
		invoice.TotalTaxAmount != "3.13" || invoice.Title != "Rechnung" {
		t.Errorf("invoice = %#v", invoice)
	}
	if invoice.Contact == nil || invoice.Contact.Name != "Bike & Ride GmbH & Co. KG" ||
		invoice.Contact.Zip != "79112" || invoice.Contact.CountryCode != "DE" ||
		invoice.Contact.ID != "777c7793-9fbb-4ec7-9254-0619c199761e" {
		t.Errorf("contact = %#v", invoice.Contact)
	}
	if len(invoice.LineItems) != 1 {
		t.Fatalf("line items = %#v", invoice.LineItems)
	}
	item := invoice.LineItems[0]
	if item.Type != "material" || item.Name != "Abus Kabelschloss" || item.Quantity != "2" ||
		item.UnitName != "Stück" || item.UnitNetAmount != "13.4" || item.UnitGrossAmount != "15.95" ||
		item.TaxRatePercentage != "19" || item.DiscountPercentage != "50" || item.Amount != "13.4" {
		t.Errorf("line item = %#v", item)
	}
}

// Only a validated UUID reaches the provider, and an answer about another invoice is refused.
func TestGetInvoiceGuardsTheIdentifier(t *testing.T) {
	t.Run("a malformed identifier stops before the provider", func(t *testing.T) {
		refuse(t)
		c, _ := client(t)
		for _, id := range []string{"", "42", "../../v1/contacts", invoiceID + "0", strings.ToUpper("zz") +
			invoiceID[2:]} {
			if _, err := c.GetInvoice(context.Background(), id); err == nil {
				t.Errorf("GetInvoice(%q) was accepted", id)
			}
		}
	})

	t.Run("another invoice is an invalid response", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"id":"55aa6de8-d32d-47bd-9c3c-d541ab65a8e8"}`), nil
		})
		c, _ := client(t)
		_, err := c.GetInvoice(context.Background(), invoiceID)
		if class := classOf(err); class != provider.ClassInvalidResponse {
			t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
		}
	})
}

// Every provider status becomes a stable class, and no provider body reaches the message.
func TestProviderStatusesAreNormalized(t *testing.T) {
	tests := []struct {
		status int
		want   provider.Class
	}{
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusPaymentRequired, provider.ClassPermission},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotAcceptable, provider.ClassProviderError},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
		{http.StatusInternalServerError, provider.ClassProviderError},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(tt.status, `{"message":"`+bodyCanary+`"}`), nil
			})
			c, _ := client(t)
			_, err := c.ListInvoices(context.Background(), ListOptions{})
			if class := classOf(err); class != tt.want {
				t.Errorf("class = %q, want %q (%v)", class, tt.want, err)
			}
			if tt.want == provider.ClassPermission && !strings.Contains(err.Error(), "; check ") {
				t.Errorf("a refusal names no next step: %v", err)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error carries the provider body: %v", err)
			}
		})
	}
}

// A 404 names the resource only for a single-object operation and never carries an ID or the body.
func TestNotFoundIsReportedPerOperation(t *testing.T) {
	missing := func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	}
	check := func(t *testing.T, err error, want provider.Class, wantMsg string) {
		t.Helper()
		if class := classOf(err); class != want {
			t.Fatalf("class = %q, want %q (%v)", class, want, err)
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("message = %q, want %q", err, wantMsg)
		}
		if strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), invoiceID) {
			t.Errorf("message carries body or ID: %v", err)
		}
	}
	const endpoint = "Lexware did not find the requested endpoint or a referenced object (HTTP 404)"
	t.Run("get", func(t *testing.T) {
		serve(t, missing)
		c, _ := client(t)
		_, err := c.GetInvoice(context.Background(), invoiceID)
		check(t, err, provider.ClassNotFound, "Lexware does not hold this invoice or does not show it to this API key")
	})
	t.Run("list", func(t *testing.T) {
		serve(t, missing)
		c, _ := client(t)
		_, err := c.ListInvoices(context.Background(), ListOptions{})
		check(t, err, provider.ClassProviderError, endpoint)
		if strings.Contains(strings.TrimPrefix(err.Error(), "list invoices: "), "invoice") {
			t.Errorf("message names an invoice: %v", err)
		}
	})
	t.Run("connection test", func(t *testing.T) {
		serve(t, missing)
		c, _ := client(t)
		err := c.get(context.Background(), "test connection", "", "/v1/voucherlist", nil, &voucherListJSON{})
		check(t, err, provider.ClassProviderError, endpoint)
	})
	t.Run("create", func(t *testing.T) {
		serve(t, missing)
		c, _ := client(t)
		err := c.post(context.Background(), "create invoice", "", "/v1/invoices", nil, map[string]string{}, &struct{}{}, "")
		check(t, err, provider.ClassProviderError, endpoint)
		if strings.Contains(err.Error(), "invoice or") {
			t.Errorf("message names an invoice: %v", err)
		}
	})
}

// A failure before a status code exists is classified without copying the transport message.
func TestTransportFailuresAreNormalized(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want provider.Class
	}{
		{"timeout", &net.DNSError{IsTimeout: true, Err: bodyCanary}, provider.ClassTimeout},
		{"tls", &tls.CertificateVerificationError{Err: errors.New(bodyCanary)}, provider.ClassTLS},
		{"unreachable", errors.New(bodyCanary), provider.ClassUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return nil, tt.err })
			c, _ := client(t)
			_, err := c.ListInvoices(context.Background(), ListOptions{})
			if class := classOf(err); class != tt.want {
				t.Errorf("class = %q, want %q (%v)", class, tt.want, err)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error carries the transport message: %v", err)
			}
		})
	}
}

// An answer beyond the size limit is refused instead of being read into memory.
func TestOversizedResponsesAreRefused(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
	})
	c, _ := client(t)
	_, err := c.ListInvoices(context.Background(), ListOptions{})
	if class := classOf(err); class != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
	}
}

// Opening a connection registers the API key and the derived bearer value with the redactor, and refuses
// any base URL other than the fixed gateway.
func TestOpenRedactsTheKeyAndBindsTheFixedGateway(t *testing.T) {
	red := &redact.Redactor{}
	if _, err := open(context.Background(), resolvedConnection("lexware-primary", "lexware-key", primaryEnv),
		resolver(red), red, freeLimiter()); err != nil {
		t.Fatalf("open() = %v", err)
	}
	for _, value := range []string{primaryKey, "Bearer " + primaryKey} {
		if got := red.Apply("diagnostic " + value); strings.Contains(got, primaryKey) {
			t.Errorf("redactor keeps %q: %q", value, got)
		}
	}

	for _, base := range []string{"https://api.lexware.example.invalid", "http://api.lexware.io",
		"https://api.lexware.io.attacker.invalid", ""} {
		connection := resolvedConnection("lexware-primary", "lexware-key", primaryEnv)
		connection.BaseURL = base
		if _, err := open(context.Background(), connection, resolver(red), red, freeLimiter()); err == nil {
			t.Errorf("open() accepted base URL %q", base)
		}
	}

	missing := resolvedConnection("lexware-primary", "lexware-key", "TEST_LEXWARE_ABSENT")
	if _, err := open(context.Background(), missing, resolver(red), red, freeLimiter()); err == nil {
		t.Error("open() accepted a credential without a secret")
	}
}

// Two connections carry two API keys. Each request uses the key of its own connection, and the keys get
// separate rate-limit budgets.
func TestConnectionsKeepTheirOwnKeyAndRateBudget(t *testing.T) {
	authorizations := make([]string, 0, 2)
	serve(t, func(request *http.Request) (*http.Response, error) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		return jsonResponse(http.StatusOK, `{"content":[],"size":25}`), nil
	})

	red := &redact.Redactor{}
	secrets := resolver(red)
	for _, connection := range []*config.Resolved{
		resolvedConnection("lexware-primary", "primary-key", primaryEnv),
		resolvedConnection("lexware-archive", "archive-key", archiveEnv),
	} {
		c, err := open(context.Background(), connection, secrets, red, freeLimiter())
		if err != nil {
			t.Fatalf("open(%s) = %v", connection.Name, err)
		}
		if _, err := c.ListInvoices(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("ListInvoices(%s) = %v", connection.Name, err)
		}
	}

	want := []string{"Bearer " + primaryKey, "Bearer " + archiveKey}
	if !reflect.DeepEqual(authorizations, want) {
		t.Errorf("authorizations = %v, want each connection to use its own key", authorizations)
	}
	if limiters.For(primaryKey) == limiters.For(archiveKey) {
		t.Error("two API keys share one rate-limit budget")
	}
	if limiters.For(primaryKey) != limiters.For(primaryKey) {
		t.Error("one API key does not keep one rate-limit budget")
	}
}

// Requests that share a key are spaced by the documented limit of two requests per second.
func TestRateLimitSpacesRequestsOfOneKey(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"content":[],"size":25}`), nil
	})

	var (
		mu     sync.Mutex
		now    = time.Unix(0, 0)
		waited []time.Duration
	)
	limited := ratelimit.New(minInterval,
		func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			waited = append(waited, d)
			now = now.Add(d)
			return nil
		})

	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(red), red, limited)
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.ListInvoices(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("ListInvoices() = %v", err)
		}
	}

	if !reflect.DeepEqual(waited, []time.Duration{minInterval, minInterval}) {
		t.Errorf("waits = %v, want two waits of %v", waited, minInterval)
	}
}

// A cancelled request never becomes a provider call while it waits for the rate limit.
func TestRateLimitRespectsCancellation(t *testing.T) {
	refuse(t)
	limited := ratelimit.New(minInterval, time.Now, ratelimit.Sleep)
	limited.HoldFor(time.Hour)

	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(red), red, limited)
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListInvoices(ctx, ListOptions{})
	if class := classOf(err); class != provider.ClassTimeout {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassTimeout, err)
	}
}

// The connection test is the smallest authenticated read of the confirmed workflow.
func TestTestConnectionReadsOneInvoiceAndReportsTheClass(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var got url.Values
		serve(t, func(request *http.Request) (*http.Response, error) {
			got = request.URL.Query()
			return jsonResponse(http.StatusOK, `{"content":[],"size":1}`), nil
		})
		stubLimiter(t, primaryKey)
		class, err := TestConnection(context.Background(),
			resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(nil), nil)
		if err != nil || class != provider.ClassOK {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
		want := url.Values{
			"voucherType": {"invoice"}, "voucherStatus": {"open"}, "archived": {"false"},
			"page": {"0"}, "size": {"1"}, "sort": {"voucherDate,DESC"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("query = %v, want %v", got, want)
		}
	})

	t.Run("auth", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusUnauthorized, `{"message":"`+bodyCanary+`"}`), nil
		})
		stubLimiter(t, primaryKey)
		class, err := TestConnection(context.Background(),
			resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(nil), nil)
		if err != nil || class != provider.ClassAuth {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})

	t.Run("an unusable connection reports its class instead of failing", func(t *testing.T) {
		refuse(t)
		connection := resolvedConnection("lexware-primary", "lexware-key", primaryEnv)
		connection.BaseURL = "https://api.lexware.example.invalid"
		class, err := TestConnection(context.Background(), connection, resolver(nil), nil)
		if err != nil || class != provider.ClassProviderError {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})
}

// Both operations satisfy their registered contract end to end: the core validates the arguments, selects
// the explicit connection, resolves the key, and validates the normalised result against the descriptor.
func TestOperationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/voucherlist" {
			return jsonResponse(http.StatusOK, listBody), nil
		}
		return jsonResponse(http.StatusOK, invoiceBody), nil
	})
	stubLimiter(t, primaryKey)
	stubLimiter(t, archiveKey)

	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	list, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.invoices.list", Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"size":25,"sort":"voucher_date","direction":"desc"}`),
	})
	if err != nil {
		t.Fatalf("invoke list = %v", err)
	}
	var listed struct {
		Invoices []struct {
			ID      string `json:"id"`
			Overdue bool   `json:"overdue"`
		} `json:"invoices"`
		TotalInvoices int  `json:"total_invoices"`
		LastPage      bool `json:"last_page"`
	}
	if err := json.Unmarshal(list.Result, &listed); err != nil {
		t.Fatalf("list result = %s: %v", list.Result, err)
	}
	if len(listed.Invoices) != 2 || listed.Invoices[0].ID != invoiceID || listed.Invoices[1].Overdue != true ||
		listed.TotalInvoices != 57 || listed.LastPage {
		t.Errorf("list result = %s", list.Result)
	}

	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.invoices.get", Connection: "lexware-archive",
		Arguments: json.RawMessage(`{"id":"` + invoiceID + `"}`),
	})
	if err != nil {
		t.Fatalf("invoke get = %v", err)
	}
	if !strings.Contains(string(got.Result), `"voucher_number":"RE1012"`) ||
		!strings.Contains(string(got.Result), `"overdue":true`) {
		t.Errorf("get result = %s", got.Result)
	}
}

// The core refuses what the contract does not allow before a provider is contacted: a missing explicit
// connection, an argument outside the schema, and an identifier that is not a UUID.
func TestTheCoreRefusesUnsupportedRequestsBeforeProviderIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	tests := []struct {
		name    string
		request application.InvokeRequest
	}{
		{"no explicit connection", application.InvokeRequest{
			Operation: "lexware.invoices.list", Arguments: json.RawMessage(`{}`)}},
		{"unknown argument", application.InvokeRequest{
			Operation: "lexware.invoices.list", Connection: "lexware-primary",
			Arguments: json.RawMessage(`{"voucher_status":"paid"}`)}},
		{"page size above the bound", application.InvokeRequest{
			Operation: "lexware.invoices.list", Connection: "lexware-primary",
			Arguments: json.RawMessage(`{"size":101}`)}},
		{"malformed date filter", application.InvokeRequest{
			Operation: "lexware.invoices.list", Connection: "lexware-primary",
			Arguments: json.RawMessage(`{"voucher_date_from":"14.05.2026"}`)}},
		{"unknown sort property", application.InvokeRequest{
			Operation: "lexware.invoices.list", Connection: "lexware-primary",
			Arguments: json.RawMessage(`{"sort":"contact_name"}`)}},
		{"identifier that is not a UUID", application.InvokeRequest{
			Operation: "lexware.invoices.get", Connection: "lexware-primary",
			Arguments: json.RawMessage(`{"id":"../../v1/contacts/1234567890123456789012345"}`)}},
		{"a connection of another provider", application.InvokeRequest{
			Operation: "lexware.invoices.get", Connection: "wiki",
			Arguments: json.RawMessage(`{"id":"` + invoiceID + `"}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := core.Invoke(context.Background(), tt.request); err == nil {
				t.Fatal("the core accepted the request")
			}
		})
	}
}

// A resolved API key never reaches a result, a diagnostic, or an error, whatever the provider answers.
func TestTheAPIKeyNeverReachesTheOutput(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"content":[{"id":"`+invoiceID+`","voucherType":"invoice",`+
			`"voucherStatus":"open","voucherNumber":"`+primaryKey+`","voucherDate":"2026-05-14",`+
			`"contactName":"Bearer `+primaryKey+`"}],"size":25,"number":0,"totalPages":1,`+
			`"totalElements":1,"last":true}`), nil
	})
	stubLimiter(t, primaryKey)

	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.invoices.list", Connection: "lexware-primary", Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if strings.Contains(string(response.Result), primaryKey) {
		t.Errorf("the result carries the API key: %s", response.Result)
	}
	if !strings.Contains(string(response.Result), redact.Marker) {
		t.Errorf("the result was not redacted: %s", response.Result)
	}
	var value any
	if err := json.Unmarshal(response.Result, &value); err != nil {
		t.Errorf("the redacted result is not valid JSON: %v", err)
	}
}

// registry returns a registry with Lexware and one foreign provider, so a wrong route is provable.
func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "wiki", Name: "Wiki"}, nil); err != nil {
		t.Fatalf("RegisterProvider() = %v", err)
	}
	return reg
}

// coreConfig configures two Lexware connections with separate credentials and one foreign connection.
func coreConfig() *config.Config {
	return &config.Config{
		Version: 1,
		Services: map[string]config.Service{
			"lexware": {Provider: Provider, BaseURL: gateway},
			"wiki":    {Provider: "wiki", BaseURL: "https://wiki.example.invalid"},
		},
		Credentials: map[string]config.Credential{
			"primary-key": {Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: primaryEnv}},
			"archive-key": {Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: archiveEnv}},
			"wiki-reader": {Type: config.CredentialTypeEnv, Values: map[string]string{"token-id": "WIKI_ID"}},
		},
		Connections: map[string]config.Connection{
			"lexware-primary": {Service: "lexware", Credential: "primary-key"},
			"lexware-archive": {Service: "lexware", Credential: "archive-key"},
			"wiki":            {Service: "wiki", Credential: "wiki-reader"},
		},
	}
}

// stubLimiter gives one API key a limiter without waiting time, so a test that performs several requests
// does not sleep. The spacing itself is proven by TestRateLimitSpacesRequestsOfOneKey.
func stubLimiter(t *testing.T, key string) {
	t.Helper()
	t.Cleanup(limiters.Replace(key, freeLimiter()))
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func minimalCreateInput() createInput {
	input := createInput{VoucherDate: "2026-09-12T00:00:00+02:00", Currency: "EUR", TaxType: "net", ShippingDate: "2026-09-12T00:00:00+02:00", ShippingType: "service"}
	input.Address.ContactID = invoiceID
	return input
}

// An invoice creation says when its outcome is uncertain, and only then; reads never do.
func TestCreateReportsUncertainOutcome(t *testing.T) {
	reset := &net.OpError{Op: "read", Err: syscall.ECONNRESET}
	refused := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	status := func(code int) func() (*http.Response, error) {
		return func() (*http.Response, error) { return jsonResponse(code, `{"message":"`+bodyCanary+`"}`), nil }
	}
	body := func(code int, text string) func() (*http.Response, error) {
		return func() (*http.Response, error) { return jsonResponse(code, text), nil }
	}
	fail := func(err error) func() (*http.Response, error) {
		return func() (*http.Response, error) { return nil, err }
	}
	tests := []struct {
		name  string
		reply func() (*http.Response, error)
		class provider.Class
		hint  bool
	}{
		{"timeout", fail(&net.DNSError{IsTimeout: true, Err: bodyCanary}), provider.ClassTimeout, true},
		{"deadline", fail(context.DeadlineExceeded), provider.ClassTimeout, true},
		{"reset", fail(reset), provider.ClassUnreachable, true},
		{"unknown", fail(errors.New(bodyCanary)), provider.ClassUnreachable, true},
		{"dns", fail(&net.DNSError{Err: bodyCanary}), provider.ClassUnreachable, false},
		{"refused", fail(refused), provider.ClassUnreachable, false},
		{"tls", fail(&tls.CertificateVerificationError{Err: errors.New(bodyCanary)}), provider.ClassTLS, false},
		{"500", status(500), provider.ClassProviderError, true},
		{"502", status(502), provider.ClassProviderError, true},
		{"503", status(503), provider.ClassProviderError, true},
		{"504", status(504), provider.ClassTimeout, true},
		{"599", status(599), provider.ClassProviderError, true},
		{"html", body(200, "<html>"+bodyCanary+"</html>"), provider.ClassInvalidResponse, true},
		{"truncated", body(201, `{"id":"`+bodyCanary), provider.ClassInvalidResponse, true},
		{"oversized", body(201, strings.Repeat("x", maxResponseBytes+1)), provider.ClassInvalidResponse, true},
		{"no id", body(201, `{"version":1}`), provider.ClassInvalidResponse, true},
		{"bad id", body(201, `{"id":"`+bodyCanary+`"}`), provider.ClassInvalidResponse, true},
		{"204", body(204, ""), provider.ClassInvalidResponse, true},
		{"400", status(400), provider.ClassProviderError, false},
		{"401", status(401), provider.ClassAuth, false},
		{"402", status(402), provider.ClassPermission, false},
		{"403", status(403), provider.ClassPermission, false},
		{"404", status(404), provider.ClassProviderError, false},
		{"406", status(406), provider.ClassProviderError, false},
		{"409", status(409), provider.ClassProviderError, false},
		{"415", status(415), provider.ClassProviderError, false},
		{"429", status(429), provider.ClassRateLimited, false},
	}
	modes := []struct {
		name string
		call func(*Client, context.Context, createInput) (*createResult, error)
		hint string
	}{
		{"create", (*Client).CreateInvoice, invoiceMayExist},
		{"issue", (*Client).IssueInvoice, invoiceMayBeIssued},
	}
	for _, mode := range modes {
		for _, tt := range tests {
			t.Run(mode.name+" "+tt.name, func(t *testing.T) {
				requests := 0
				serve(t, func(*http.Request) (*http.Response, error) { requests++; return tt.reply() })
				c, _ := client(t)
				_, err := mode.call(c, context.Background(), minimalCreateInput())
				if err == nil {
					t.Fatal("CreateInvoice() succeeded")
				}
				if class := classOf(err); class != tt.class {
					t.Errorf("class = %q, want %q (%v)", class, tt.class, err)
				}
				if got := strings.Contains(err.Error(), mode.hint); got != tt.hint {
					t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
				}
				if requests != 1 {
					t.Errorf("requests = %d, want 1", requests)
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("error carries provider content: %v", err)
				}
			})
		}
	}

	t.Run("rate-limit wait", func(t *testing.T) {
		refuse(t)
		limited := ratelimit.New(minInterval, time.Now, ratelimit.Sleep)
		limited.HoldFor(time.Hour)
		red := &redact.Redactor{}
		c, err := open(context.Background(), resolvedConnection("lexware-primary", "lexware-key", primaryEnv), resolver(red), red, limited)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err = c.CreateInvoice(ctx, minimalCreateInput())
		if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), "may have been created") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("oversized request", func(t *testing.T) {
		refuse(t)
		c, _ := client(t)
		input := minimalCreateInput()
		input.Title = strings.Repeat("x", maxResponseBytes+1)
		_, err := c.CreateInvoice(context.Background(), input)
		if err == nil || strings.Contains(err.Error(), "may have been created") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestReadsNeverCarryTheCreateHint(t *testing.T) {
	replies := map[string]func() (*http.Response, error){
		"timeout": func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":   func() (*http.Response, error) { return nil, &net.OpError{Op: "read", Err: syscall.ECONNRESET} },
		"500":     func() (*http.Response, error) { return jsonResponse(500, "{}"), nil },
		"html":    func() (*http.Response, error) { return jsonResponse(200, "<html>"), nil },
	}
	for name, reply := range replies {
		t.Run(name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return reply() })
			c, _ := client(t)
			_, err := c.GetInvoice(context.Background(), invoiceID)
			_, err2 := c.ListInvoices(context.Background(), ListOptions{})
			for _, e := range []error{err, err2} {
				if e == nil || strings.Contains(e.Error(), "may have been created") {
					t.Errorf("err = %v", e)
				}
			}
		})
	}
}
