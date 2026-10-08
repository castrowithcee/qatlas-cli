package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	contactID = "777c7793-9fbb-4ec7-9254-0619c199761e"
	personID  = "0b0e7d2e-5a4f-4c53-8a35-0f9c3a1d2b11"
)

const companyContactBody = `{
  "id":"` + contactID + `","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8","version":3,
  "roles":{"customer":{"number":10307},"vendor":{"number":70001}},
  "company":{"name":"Bike & Ride GmbH","taxNumber":"12345/67890","vatRegistrationId":"DE123456789",
   "allowTaxFreeInvoices":true,
   "contactPersons":[{"salutation":"Frau","firstName":"Erika","lastName":"Musterfrau","primary":true,
    "emailAddress":"erika@example.invalid","phoneNumber":"0761 1234"}]},
  "addresses":{"billing":[{"supplement":"Gebäude 10","street":"Musterstraße 42","zip":"79112","city":"Freiburg","countryCode":"DE"}],
   "shipping":[{"street":"Lagerweg 1","zip":"79111","city":"Freiburg","countryCode":"DE"}]},
  "emailAddresses":{"other":["x@example.invalid"],"business":["b1@example.invalid","b2@example.invalid"]},
  "phoneNumbers":{"mobile":["0170 1"],"fax":["0761 2"]},
  "note":"` + bodyCanary + `","archived":false}`

const personContactBody = `{
  "id":"` + personID + `","version":1,"roles":{"customer":{"number":10308}},
  "person":{"salutation":"Herr","firstName":"Max","lastName":"Mustermann"},
  "archived":true}`

const contactsPageBody = `{"content":[` + companyContactBody + `,` + personContactBody + `],
  "first":false,"last":true,"totalPages":4,"totalElements":77,"numberOfElements":2,"size":2,"number":3}`

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestListContactsSendsExactlyTheControlledQuery(t *testing.T) {
	tests := []struct {
		name    string
		options ContactListOptions
		want    url.Values
	}{
		{"defaults", ContactListOptions{}, url.Values{"page": {"0"}, "size": {"25"}}},
		{"name", ContactListOptions{Name: "Muster"}, url.Values{"page": {"0"}, "size": {"25"}, "name": {"Muster"}}},
		{"email", ContactListOptions{Email: "erika@", Page: 2, Size: 100},
			url.Values{"page": {"2"}, "size": {"100"}, "email": {"erika@"}}},
		{"wildcards and backslashes are literal", ContactListOptions{Name: `a_b%c\d`, Email: `100%_x\`},
			url.Values{"page": {"0"}, "size": {"25"}, "name": {`a\_b\%c\\d`}, "email": {`100\%\_x\\`}}},
		{"number zero is sent", ContactListOptions{Number: intPtr(0)},
			url.Values{"page": {"0"}, "size": {"25"}, "number": {"0"}}},
		{"number", ContactListOptions{Number: intPtr(10307)},
			url.Values{"page": {"0"}, "size": {"25"}, "number": {"10307"}}},
		{"roles only when set", ContactListOptions{Customer: boolPtr(true), Vendor: boolPtr(false)},
			url.Values{"page": {"0"}, "size": {"25"}, "customer": {"true"}, "vendor": {"false"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			serve(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet || request.URL.Host != "api.lexware.io" ||
					request.URL.Path != "/v1/contacts" {
					t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
				}
				if got := request.URL.Query(); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("query = %v, want %v", got, tt.want)
				}
				return jsonResponse(http.StatusOK, contactsPageBody), nil
			})
			c, _ := client(t)
			if _, err := c.ListContacts(context.Background(), tt.options); err != nil || calls != 1 {
				t.Fatalf("ListContacts() = %v after %d requests", err, calls)
			}
		})
	}
}

func TestListContactsNormalizesPageCompanyAndPerson(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, contactsPageBody), nil
	})
	c, _ := client(t)
	result, err := c.ListContacts(context.Background(), ContactListOptions{})
	if err != nil {
		t.Fatalf("ListContacts() = %v", err)
	}
	if result.Page != 3 || result.Size != 2 || result.TotalPages != 4 || result.TotalElements != 77 ||
		!result.LastPage || len(result.Contacts) != 2 {
		t.Fatalf("result = %+v", result)
	}
	company, person := result.Contacts[0], result.Contacts[1]
	if company.ID != contactID || company.Version != 3 || company.Person != nil || company.Company == nil ||
		company.Company.Name != "Bike & Ride GmbH" || company.Company.VatRegistrationID != "DE123456789" ||
		!company.Company.AllowTaxFreeInvoices || len(company.Company.ContactPersons) != 1 ||
		!company.Company.ContactPersons[0].Primary || company.Company.ContactPersons[0].EmailAddress == "" {
		t.Errorf("company = %+v", company)
	}
	if company.Roles == nil || company.Roles.Customer.Number != 10307 || company.Roles.Vendor.Number != 70001 {
		t.Errorf("roles = %+v", company.Roles)
	}
	wantEmails := []ContactEmail{{"business", "b1@example.invalid"}, {"business", "b2@example.invalid"}, {"other", "x@example.invalid"}}
	wantPhones := []ContactPhone{{"mobile", "0170 1"}, {"fax", "0761 2"}}
	if !reflect.DeepEqual(company.EmailAddresses, wantEmails) || !reflect.DeepEqual(company.PhoneNumbers, wantPhones) {
		t.Errorf("emails = %+v, phones = %+v", company.EmailAddresses, company.PhoneNumbers)
	}
	if company.Addresses == nil || len(company.Addresses.Billing) != 1 || company.Addresses.Billing[0].CountryCode != "DE" ||
		company.Addresses.Shipping[0].Street != "Lagerweg 1" {
		t.Errorf("addresses = %+v", company.Addresses)
	}
	if person.Company != nil || person.Person == nil || person.Person.LastName != "Mustermann" ||
		!person.Archived || person.Roles.Vendor != nil || person.Roles.Customer.Number != 10308 {
		t.Errorf("person = %+v", person)
	}
	encoded, _ := json.Marshal(person)
	if strings.Contains(string(encoded), `"company"`) || strings.Contains(string(encoded), `"addresses"`) {
		t.Errorf("person carries empty members: %s", encoded)
	}
}

func TestContactFiltersAreRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	core := application.New(registry(t), coreConfig(), counting, red)
	for name, request := range map[string]application.InvokeRequest{
		"short name":      {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"name":"ab"}`)},
		"short email":     {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"email":"a@"}`)},
		"fractional":      {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"number":1.5}`)},
		"text number":     {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"number":"12"}`)},
		"negative number": {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"number":-1}`)},
		"text flag":       {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"customer":"true"}`)},
		"free parameter":  {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"sort":"name"}`)},
		"size":            {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"size":101}`)},
		"page":            {Operation: "lexware.contacts.list", Arguments: json.RawMessage(`{"page":201}`)},
		"path id":         {Operation: "lexware.contacts.get", Arguments: json.RawMessage(`{"id":"../invoices/` + invoiceID + `"}`)},
		"short id":        {Operation: "lexware.contacts.get", Arguments: json.RawMessage(`{"id":"42"}`)},
	} {
		request.Connection = "lexware-primary"
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s: the core accepted the request", name)
		}
	}
	// A direct caller is held to the same bounds.
	c, _ := client(t)
	for _, options := range []ContactListOptions{{Name: "ab"}, {Email: "ab"}, {Name: "äö"}, {Number: intPtr(-1)}} {
		if _, err := c.ListContacts(context.Background(), options); err == nil {
			t.Errorf("ListContacts(%+v) was accepted", options)
		}
	}
	for _, id := range []string{"", "42", contactID + "0", "../" + contactID} {
		if _, err := c.GetContact(context.Background(), id); err == nil {
			t.Errorf("GetContact(%q) was accepted", id)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

func TestGetContactReadsExactlyTheRequestedContact(t *testing.T) {
	calls := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != "/v1/contacts/"+strings.ToUpper(contactID) || request.URL.RawQuery != "" {
			t.Errorf("request = %s", request.URL.Redacted())
		}
		return jsonResponse(http.StatusOK, companyContactBody), nil
	})
	c, _ := client(t)
	record, err := c.GetContact(context.Background(), strings.ToUpper(contactID))
	if err != nil || calls != 1 || record.ID != contactID || record.Note != bodyCanary {
		t.Fatalf("GetContact() = %+v, %v after %d requests", record, err, calls)
	}
}

func TestContactAnswersAreValidated(t *testing.T) {
	t.Run("another contact", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, personContactBody), nil
		})
		c, _ := client(t)
		_, err := c.GetContact(context.Background(), contactID)
		if class := classOf(err); class != provider.ClassInvalidResponse {
			t.Fatalf("class = %q (%v)", class, err)
		}
		if strings.Contains(err.Error(), personID) || strings.Contains(err.Error(), contactID) {
			t.Errorf("message carries an ID: %v", err)
		}
	})
	t.Run("list entry without identifier", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"content":[{"id":"`+bodyCanary+`"}],"size":25}`), nil
		})
		c, _ := client(t)
		_, err := c.ListContacts(context.Background(), ContactListOptions{})
		if class := classOf(err); class != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
			t.Fatalf("class = %q (%v)", class, err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
		})
		c, _ := client(t)
		if _, err := c.ListContacts(context.Background(), ContactListOptions{}); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("list: %v", err)
		}
		if _, err := c.GetContact(context.Background(), contactID); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("get: %v", err)
		}
	})
	t.Run("provider body never reaches a message", func(t *testing.T) {
		for _, status := range []int{400, 401, 402, 403, 404, 429, 500, 504} {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"message":"`+bodyCanary+`"}`), nil
			})
			c, _ := client(t)
			if _, err := c.ListContacts(context.Background(), ContactListOptions{Name: "Muster"}); err == nil ||
				strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "Muster") {
				t.Errorf("status %d: %v", status, err)
			}
		}
	})
}

func TestContactNotFoundIsReportedForGetOnly(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	c, _ := client(t)
	_, err := c.GetContact(context.Background(), contactID)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(),
		"Lexware does not hold this contact or does not show it to this API key") ||
		strings.Contains(err.Error(), contactID) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("get: %v", err)
	}
	_, err = c.ListContacts(context.Background(), ContactListOptions{})
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "does not hold") {
		t.Errorf("list: %v", err)
	}
}

func TestContactOperationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/contacts" {
			return jsonResponse(http.StatusOK, contactsPageBody), nil
		}
		return jsonResponse(http.StatusOK, companyContactBody), nil
	})
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	list, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.contacts.list", Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"name":"Muster","number":10307,"customer":true,"size":2}`),
	})
	if err != nil {
		t.Fatalf("invoke list = %v", err)
	}
	for _, want := range []string{`"total_elements":77`, `"last_page":true`, `"total_pages":4`, `"page":3`} {
		if !strings.Contains(string(list.Result), want) {
			t.Errorf("list result lacks %s: %s", want, list.Result)
		}
	}
	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.contacts.get", Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"id":"` + contactID + `"}`),
	})
	if err != nil || !strings.Contains(string(got.Result), `"vat_registration_id":"DE123456789"`) {
		t.Fatalf("invoke get = %s, %v", got.Result, err)
	}
}
