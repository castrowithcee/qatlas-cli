package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const readOrgID = "aa93e8a8-2aa3-470b-b914-caad8a255dd8"

func schemaProperties(t *testing.T, schema json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var parsed struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("schema = %s: %v", schema, err)
	}
	return parsed.Properties
}

// Every key of a fully populated output is declared in the output schema and described in Fields.
func assertDeclared(t *testing.T, descriptor capability.Descriptor, output any, optional ...string) {
	t.Helper()
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	properties := schemaProperties(t, descriptor.OutputSchema)
	for key := range keys {
		if _, ok := properties[key]; !ok {
			t.Errorf("%s: output member %q missing in OutputSchema", descriptor.ID, key)
		}
		found := false
		for _, field := range descriptor.Fields {
			found = found || field.Name == key
		}
		if !found {
			t.Errorf("%s: output member %q missing in Fields", descriptor.ID, key)
		}
	}
	for key := range properties {
		if _, ok := keys[key]; !ok && !slices.Contains(optional, key) {
			t.Errorf("%s: schema member %q not exercised by the full fixture", descriptor.ID, key)
		}
	}
}

func TestContactReadsOrganizationAndXRechnung(t *testing.T) {
	body := strings.Replace(companyContactBody, `"note"`,
		`"xRechnung":{"buyerReference":"04011000-1234512345-35","vendorNumberAtCustomer":"70123456"},"note"`, 1)
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	record, err := c.GetContact(context.Background(), contactID)
	if err != nil {
		t.Fatal(err)
	}
	if record.OrganizationID != readOrgID || record.XRechnung == nil ||
		record.XRechnung.BuyerReference != "04011000-1234512345-35" ||
		record.XRechnung.VendorNumberAtCustomer != "70123456" {
		t.Fatalf("record = %+v", record)
	}
	encoded, _ := json.Marshal(record)
	if !strings.Contains(string(encoded), `"x_rechnung":{"buyer_reference":"04011000-1234512345-35","vendor_number_at_customer":"70123456"}`) {
		t.Errorf("encoded = %s", encoded)
	}
	assertDeclared(t, contactsGet, record, "person")
	properties := schemaProperties(t, contactsList.OutputSchema)
	var items struct {
		Items json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(properties["contacts"], &items)
	if props := schemaProperties(t, items.Items); props["organization_id"] == nil || props["x_rechnung"] == nil {
		t.Errorf("contacts.list item schema lacks the new members")
	}

	// A partial xRechnung keeps only the member Lexware sent; none at all keeps the member away.
	partial := strings.Replace(personContactBody, `"archived"`, `"xRechnung":{"buyerReference":"B"},"archived"`, 1)
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, partial), nil })
	c, _ = client(t)
	record, err = c.GetContact(context.Background(), personID)
	if err != nil || record.XRechnung == nil || *record.XRechnung != (ContactXRechnung{BuyerReference: "B"}) {
		t.Fatalf("partial record = %+v, %v", record, err)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, personContactBody), nil
	})
	c, _ = client(t)
	record, _ = c.GetContact(context.Background(), personID)
	encoded, _ = json.Marshal(record)
	if strings.Contains(string(encoded), "x_rechnung") || strings.Contains(string(encoded), "organization_id") {
		t.Errorf("absent values must stay away: %s", encoded)
	}
}

func TestArticleReadsOrganizationAndDates(t *testing.T) {
	body := `{"id":"` + articleID + `","organizationId":"` + readOrgID + `","createdDate":"2023-09-21T17:46:40.629+02:00",
	 "updatedDate":"2024-05-03T12:21:32.120+02:00","archived":false,"title":"T","description":"D","type":"PRODUCT",
	 "articleNumber":"A1","gtin":"9783648170632","note":"N","unitName":"U",
	 "price":{"netPrice":1,"grossPrice":1.19,"leadingPrice":"NET","taxRate":19},"version":2}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	record, err := c.GetArticle(context.Background(), articleID)
	if err != nil {
		t.Fatal(err)
	}
	if record.OrganizationID != readOrgID || record.CreatedDate != "2023-09-21T17:46:40.629+02:00" ||
		record.UpdatedDate != "2024-05-03T12:21:32.120+02:00" {
		t.Fatalf("record = %+v", record)
	}
	assertDeclared(t, articlesGet, record)
	if props := schemaProperties(t, articlesList.OutputSchema); props["articles"] == nil {
		t.Fatal("no articles member")
	}

	// A malformed organization identifier is not passed on.
	bad := strings.Replace(body, readOrgID, "../x", 1)
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, bad), nil })
	c, _ = client(t)
	record, err = c.GetArticle(context.Background(), articleID)
	if err != nil || record.OrganizationID != "" {
		t.Fatalf("record = %+v, %v", record, err)
	}
}

func TestVoucherReadsOrganizationAndCollectiveContactName(t *testing.T) {
	body := `{"id":"` + invoiceID + `","organizationId":"` + readOrgID + `","type":"salesinvoice",
	 "voucherStatus":"open","voucherNumber":"V1","voucherDate":"2023-06-30","shippingDate":"2023-07-02",
	 "dueDate":"2023-07-07","totalGrossAmount":326.00,"totalTaxAmount":26.00,"taxType":"gross",
	 "useCollectiveContact":true,"contactId":"` + contactID + `","contactName":"Max Mustermann","remark":"R",
	 "voucherItems":[{"amount":1,"taxAmount":0,"taxRatePercent":0,"categoryId":"` + contactID + `"}],
	 "files":["` + contactID + `"],"createdDate":"c","updatedDate":"u","version":2}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	voucher, err := c.GetVoucher(context.Background(), invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	if voucher.OrganizationID != readOrgID || voucher.ContactName != "Max Mustermann" {
		t.Fatalf("voucher = %+v", voucher)
	}
	assertDeclared(t, vouchersGet, voucher)
}

func TestEventSubscriptionReadsOrganization(t *testing.T) {
	subscription := `{"subscriptionId":"` + contactID + `","organizationId":"` + readOrgID + `",
	 "createdDate":"2023-04-11T12:15:00.000+02:00","eventType":"contact.changed","callbackUrl":"https://example.org/hook/secret"}`
	serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/event-subscriptions") {
			return jsonResponse(http.StatusOK, `{"content":[`+subscription+`]}`), nil
		}
		return jsonResponse(http.StatusOK, subscription), nil
	})
	c, _ := client(t)
	got, err := c.GetEventSubscription(context.Background(), contactID)
	if err != nil || got.OrganizationID != readOrgID {
		t.Fatalf("get = %+v, %v", got, err)
	}
	assertDeclared(t, eventSubscriptionsGet, got)
	items, _, err := c.ListEventSubscriptions(context.Background())
	if err != nil || len(items) != 1 || items[0].OrganizationID != readOrgID {
		t.Fatalf("list = %+v, %v", items, err)
	}
	if !strings.Contains(eventSubscriptionsList.Fields[0].Description, "organization_id") {
		t.Error("list Fields do not describe organization_id")
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "secret") {
		t.Errorf("callback path leaked: %s", encoded)
	}
}

func TestInvoicesListSendsTheNewFiltersAndKeepsTheFixedOnes(t *testing.T) {
	var got url.Values
	calls := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls++
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, listBody), nil
	})
	c, _ := client(t)
	if _, err := c.ListInvoices(context.Background(), ListOptions{
		ContactID: contactID, CreatedDateFrom: "2026-01-01", CreatedDateTo: "2026-02-01",
		UpdatedDateFrom: "2026-03-01", UpdatedDateTo: "2026-04-01",
	}); err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"voucherType": {"invoice"}, "voucherStatus": {"open"}, "archived": {"false"},
		"page": {"0"}, "size": {"25"}, "sort": {"voucherDate,DESC"}, "contactId": {contactID},
		"createdDateFrom": {"2026-01-01"}, "createdDateTo": {"2026-02-01"},
		"updatedDateFrom": {"2026-03-01"}, "updatedDateTo": {"2026-04-01"},
	}
	if calls != 1 || !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %d, query = %v, want %v", calls, got, want)
	}
}

func TestInvoicesListRejectsNewFiltersBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	for name, arguments := range map[string]string{
		"contact id":   `{"contact_id":"../../x"}`,
		"created from": `{"created_date_from":"01.01.2026"}`,
		"created to":   `{"created_date_to":"2026-13-45"}`,
		"updated from": `{"updated_date_from":"x"}`,
		"updated to":   `{"updated_date_to":"2026-02-30"}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Nil dependencies would panic if the handler reached secret resolution.
			if _, err := invokeInvoicesList(context.Background(), nil, nil, nil, json.RawMessage(arguments)); err == nil {
				t.Fatal("the handler accepted the filter")
			}
		})
	}
}

func TestInvoicesListOutputIsDeclaredAndStaysCompactWithoutNewFields(t *testing.T) {
	full := ListInvoice{ID: invoiceID, VoucherNumber: "RE1", VoucherStatus: "open", VoucherDate: "d", DueDate: "d",
		CreatedDate: "c", UpdatedDate: "u", ContactID: contactID, ContactName: "N", TotalAmount: "1",
		OpenAmount: "1", Currency: "EUR"}
	encoded, _ := json.Marshal(full)
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &keys)
	var items struct {
		Items json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(schemaProperties(t, invoicesList.OutputSchema)["invoices"], &items)
	properties := schemaProperties(t, items.Items)
	var description string
	for _, field := range invoicesList.Fields {
		if field.Name == "invoices" {
			description = field.Description
		}
	}
	for key := range keys {
		if key == "overdue" {
			continue
		}
		if _, ok := properties[key]; !ok {
			t.Errorf("%q missing in the item schema", key)
		}
		if !strings.Contains(description, key) {
			t.Errorf("%q missing in the invoices field description", key)
		}
	}
	if len(keys) != len(properties) {
		t.Errorf("item keys %d, schema members %d", len(keys), len(properties))
	}
	if !strings.Contains(description, "untrusted personal data") {
		t.Error("contact_name is not described as personal data")
	}

	old, _ := json.Marshal(ListInvoice{ID: invoiceID, VoucherNumber: "RE1", VoucherStatus: "open", VoucherDate: "d"})
	if strings.Contains(string(old), "created_date") || strings.Contains(string(old), "contact_id") {
		t.Errorf("absent members must stay away: %s", old)
	}
}
