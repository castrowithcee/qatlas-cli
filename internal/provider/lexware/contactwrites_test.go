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

const otherContactID = "2f9d6e1a-0b3c-4d5e-8f70-123456789abc"

// storedCompany carries members Qatlas does not model next to modelled ones; every list has one entry.
const storedCompany = `{"id":"` + contactID + `","organizationId":"org-1","version":3,"futureField":{"a":[1,2.50]},
  "roles":{"customer":{"number":10001}},
  "company":{"name":"Alt GmbH","taxNumber":"00/000/00000","allowTaxFreeInvoices":false,"legalForm":"X",
    "contactPersons":[{"salutation":"Frau","firstName":"Erika","lastName":"Muster","primary":true,
      "emailAddress":"erika@example.invalid","phoneNumber":"000","extra":1}]},
  "addresses":{"billing":[{"street":"Altstrasse 1","zip":"00000","city":"Altstadt","countryCode":"DE","contactId":"c"}]},
  "emailAddresses":{"business":["info@example.invalid"]},
  "phoneNumbers":{"business":["000 111"]},
  "note":"alt","archived":false}`

const storedPerson = `{"id":"` + contactID + `","version":4,"roles":{"vendor":{"number":70001}},
  "person":{"salutation":"Herr","firstName":"Max","lastName":"Muster"},"archived":false}`

func contactGet(body string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil }
}

func contactWriteOK() (*http.Response, error) {
	return jsonResponse(http.StatusCreated, `{"id":"`+contactID+`","resourceUri":"x","createdDate":"c","updatedDate":"u","version":4}`), nil
}

func TestCreateContactSendsExactlyOneControlledPost(t *testing.T) {
	tests := map[string]struct{ args, want string }{
		"company": {`{"roles":{"customer":true,"vendor":false},"company":{"name":"Beispiel GmbH","tax_number":"00/000/00000",` +
			`"allow_tax_free_invoices":true,"contact_person":{"first_name":"Erika","last_name":"Muster","primary":true,` +
			`"email_address":"erika@example.invalid"}},"billing_address":{"street":"Teststrasse 1","zip":"00000",` +
			`"city":"Teststadt","country_code":"DE"},"email_addresses":{"business":"info@example.invalid"},` +
			`"phone_numbers":{"mobile":"000 111"},"note":"n"}`,
			`{"addresses":{"billing":[{"city":"Teststadt","countryCode":"DE","street":"Teststrasse 1","zip":"00000"}]},` +
				`"company":{"allowTaxFreeInvoices":true,"contactPersons":[{"emailAddress":"erika@example.invalid",` +
				`"firstName":"Erika","lastName":"Muster","primary":true}],"name":"Beispiel GmbH","taxNumber":"00/000/00000"},` +
				`"emailAddresses":{"business":["info@example.invalid"]},"note":"n","phoneNumbers":{"mobile":["000 111"]},` +
				`"roles":{"customer":{}},"version":0}`},
		"person": {`{"roles":{"vendor":true},"person":{"salutation":"Herr","last_name":"Muster"}}`,
			`{"person":{"lastName":"Muster","salutation":"Herr"},"roles":{"vendor":{}},"version":0}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, storedGet, contactWriteOK)
			core, _ := writeCore(t)
			result, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: "lexware.contacts.create", Connection: "lexware-writer", Confirmed: true,
				Arguments: json.RawMessage(tt.args)})
			if err != nil || !strings.Contains(string(result.Result), `"id":"`+contactID+`"`) {
				t.Fatalf("invoke = %s, %v", result.Result, err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost || (*requests)[0].path != "/v1/contacts" {
				t.Fatalf("requests = %+v", *requests)
			}
			sent, _ := json.Marshal((*requests)[0].body)
			if string(sent) != tt.want {
				t.Errorf("payload = %s\nwant      %s", sent, tt.want)
			}
		})
	}
}

func TestContactWritesAreRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	core, reads := writeCore(t)
	const create = "lexware.contacts.create"
	const update = "lexware.contacts.update"
	const id = `"id":"` + contactID + `"`
	tests := []struct {
		name, operation, args string
		confirm               bool
	}{
		{"create without confirm", create, `{"roles":{"customer":true},"person":{"last_name":"M"}}`, false},
		{"update without confirm", update, `{` + id + `,"note":"x"}`, false},
		{"create no roles", create, `{"person":{"last_name":"M"}}`, true},
		{"create no role true", create, `{"roles":{"customer":false},"person":{"last_name":"M"}}`, true},
		{"create neither person nor company", create, `{"roles":{"customer":true}}`, true},
		{"create person and company", create, `{"roles":{"customer":true},"person":{"last_name":"M"},"company":{"name":"C"}}`, true},
		{"create person without last name", create, `{"roles":{"customer":true},"person":{"first_name":"M"}}`, true},
		{"create company without name", create, `{"roles":{"customer":true},"company":{"tax_number":"1"}}`, true},
		{"create contact person without last name", create, `{"roles":{"customer":true},"company":{"name":"C","contact_person":{"first_name":"M"}}}`, true},
		{"create address without country", create, `{"roles":{"customer":true},"person":{"last_name":"M"},"billing_address":{"city":"X"}}`, true},
		{"create lower-case country", create, `{"roles":{"customer":true},"person":{"last_name":"M"},"billing_address":{"country_code":"de"}}`, true},
		{"create passthrough field", create, `{"roles":{"customer":true},"person":{"last_name":"M"},"archived":true}`, true},
		{"create nested passthrough", create, `{"roles":{"customer":true},"person":{"last_name":"M","title":"x"}}`, true},
		{"create id", create, `{` + id + `,"roles":{"customer":true},"person":{"last_name":"M"}}`, true},
		{"create long name", create, `{"roles":{"customer":true},"person":{"last_name":"` + strings.Repeat("x", 256) + `"}}`, true},
		{"create extra email kind", create, `{"roles":{"customer":true},"person":{"last_name":"M"},"email_addresses":{"work":"a@example.invalid"}}`, true},
		{"update bad id", update, `{"id":"../articles/` + articleID + `","note":"x"}`, true},
		{"update no field", update, `{` + id + `}`, true},
		{"update passthrough field", update, `{` + id + `,"archived":true}`, true},
		{"update remove role", update, `{` + id + `,"roles":{"customer":false}}`, true},
		{"update person and company", update, `{` + id + `,"person":{"last_name":"M"},"company":{"name":"C"}}`, true},
		{"update empty group", update, `{` + id + `,"company":{}}`, true},
		{"update empty email", update, `{` + id + `,"email_addresses":{"business":""}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.operation, Connection: "lexware-writer", Arguments: json.RawMessage(tt.args), Confirmed: tt.confirm})
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

func TestHandlerRechecksTheRulesWithoutTheSchema(t *testing.T) {
	for _, input := range []contactChanges{
		{Person: &personChanges{LastName: str("M")}, Company: &companyChanges{Name: str("C")}},
		{Roles: &contactRoles{}},
		{Note: str(strings.Repeat("x", 1001))},
	} {
		if input.validate(true) == nil {
			t.Errorf("validate(%+v) accepted the input", input)
		}
	}
}

func TestUpdateContactRoundTripsUnchangedMembersWithTheReadVersion(t *testing.T) {
	requests := scripted(t, contactGet(storedCompany), contactWriteOK)
	c, _ := client(t)
	truth := true
	_, err := c.UpdateContact(context.Background(), contactChanges{ID: contactID,
		Roles:          &contactRoles{Vendor: &truth},
		Company:        &companyChanges{Name: str("Neu GmbH"), ContactPerson: &contactPersonChanges{LastName: str("Beispiel")}},
		BillingAddress: &addressChanges{City: str("Neustadt")},
		EmailAddresses: &emailChanges{Office: str("buero@example.invalid")},
		Note:           str("")})
	if err != nil {
		t.Fatalf("UpdateContact() = %v", err)
	}
	if len(*requests) != 2 || (*requests)[0].method != http.MethodGet || (*requests)[1].method != http.MethodPut ||
		(*requests)[1].path != "/v1/contacts/"+contactID {
		t.Fatalf("requests = %+v", *requests)
	}
	got := (*requests)[1].body
	if string(got["version"]) != "3" || string(got["futureField"]) != `{"a":[1,2.50]}` || string(got["note"]) != `""` ||
		string(got["roles"]) != `{"customer":{"number":10001},"vendor":{}}` {
		t.Errorf("version = %s, futureField = %s, note = %s, roles = %s", got["version"], got["futureField"], got["note"], got["roles"])
	}
	want := map[string]string{
		"company": `{"allowTaxFreeInvoices":false,"contactPersons":[{"emailAddress":"erika@example.invalid","extra":1,` +
			`"firstName":"Erika","lastName":"Beispiel","phoneNumber":"000","primary":true,"salutation":"Frau"}],` +
			`"legalForm":"X","name":"Neu GmbH","taxNumber":"00/000/00000"}`,
		"addresses":      `{"billing":[{"city":"Neustadt","contactId":"c","countryCode":"DE","street":"Altstrasse 1","zip":"00000"}]}`,
		"emailAddresses": `{"business":["info@example.invalid"],"office":["buero@example.invalid"]}`,
		"phoneNumbers":   `{"business":["000 111"]}`,
	}
	for key, value := range want {
		var canonical any
		_ = json.Unmarshal(got[key], &canonical)
		sent, _ := json.Marshal(canonical)
		if string(sent) != value {
			t.Errorf("%s = %s\nwant %s", key, sent, value)
		}
	}
}

func TestUpdateContactRefusesWhatCannotBeDoneBeforeTheWrite(t *testing.T) {
	yes := true
	tests := map[string]struct {
		stored string
		change contactChanges
	}{
		"person to company": {storedPerson, contactChanges{ID: contactID, Company: &companyChanges{Name: str("C")}}},
		"company to person": {storedCompany, contactChanges{ID: contactID, Person: &personChanges{LastName: str("M")}}},
		"several billing addresses": {strings.Replace(storedCompany, `"billing":[{`, `"billing":[{"city":"B"},{`, 1),
			contactChanges{ID: contactID, Note: str("x")}},
		"several e-mail addresses": {strings.Replace(storedCompany, `["info@example.invalid"]`, `["a@example.invalid","b@example.invalid"]`, 1),
			contactChanges{ID: contactID, Roles: &contactRoles{Customer: &yes}}},
		"several phone numbers": {strings.Replace(storedCompany, `["000 111"]`, `["1","2"]`, 1),
			contactChanges{ID: contactID, Note: str("x")}},
		"several contact persons": {strings.Replace(storedCompany, `"contactPersons":[{`, `"contactPersons":[{"lastName":"A"},{`, 1),
			contactChanges{ID: contactID, Note: str("x")}},
		"address without country": {storedPerson, contactChanges{ID: contactID, ShippingAddress: &addressChanges{City: str("X")}}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, contactGet(tt.stored), contactWriteOK)
			c, _ := client(t)
			_, err := c.UpdateContact(context.Background(), tt.change)
			if err == nil || classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), contactMayChanged) ||
				strings.Contains(err.Error(), "Muster") {
				t.Fatalf("err = %v, want a refusal without uncertainty hint", err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
				t.Errorf("requests = %+v, want the read only", *requests)
			}
		})
	}
}

func TestContactWriteFailuresAreClassifiedWithoutProviderText(t *testing.T) {
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
		{"504", status(504), provider.ClassTimeout, "", true},
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }, provider.ClassTimeout, "", true},
		{"canceled", func() (*http.Response, error) { return nil, context.Canceled }, "", "", true},
		{"reset", func() (*http.Response, error) { return nil, reset }, provider.ClassUnreachable, "", true},
		{"unusable answer", func() (*http.Response, error) { return jsonResponse(200, "<html>"), nil }, provider.ClassInvalidResponse, "", true},
		{"429", status(429), provider.ClassRateLimited, "", false},
	}
	for _, tt := range tests {
		t.Run("update "+tt.name, func(t *testing.T) {
			requests := scripted(t, contactGet(storedCompany), tt.reply)
			c, _ := client(t)
			_, err := c.UpdateContact(context.Background(), contactChanges{ID: contactID, Note: str("x")})
			if err == nil || (tt.class != "" && classOf(err) != tt.class) || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), contactMayChanged); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
				t.Errorf("err = %v, want %q", err, tt.text)
			}
			if len(*requests) != 2 {
				t.Errorf("requests = %d, want one read and exactly one write", len(*requests))
			}
		})
		t.Run("create "+tt.name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.CreateContact(context.Background(), contactChanges{Roles: &contactRoles{Customer: new(bool)},
				Person: &personChanges{LastName: str("M")}})
			if err == nil || (tt.class != "" && classOf(err) != tt.class) || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), contactMayExist); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost {
				t.Errorf("requests = %+v, want exactly one POST", *requests)
			}
		})
	}
}

func TestUpdateContactStopsBeforeTheWriteWhenTheReadIsUnusable(t *testing.T) {
	tests := map[string]func() (*http.Response, error){
		"read fails":  func() (*http.Response, error) { return jsonResponse(500, "{}"), nil },
		"not found":   func() (*http.Response, error) { return jsonResponse(404, "{}"), nil },
		"other":       contactGet(`{"id":"` + otherContactID + `","version":1}`),
		"no version":  contactGet(`{"id":"` + contactID + `"}`),
		"answer null": contactGet(`null`),
	}
	for name, get := range tests {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, get, contactWriteOK)
			c, _ := client(t)
			_, err := c.UpdateContact(context.Background(), contactChanges{ID: contactID, Note: str("x")})
			if err == nil || strings.Contains(err.Error(), contactMayChanged) {
				t.Fatalf("err = %v, want a failure without an uncertainty hint", err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
				t.Errorf("requests = %+v, want the read only", *requests)
			}
		})
	}
}

func TestUpdateContactRejectsAnAnswerForAnotherContact(t *testing.T) {
	scripted(t, contactGet(storedCompany), func() (*http.Response, error) {
		return jsonResponse(200, `{"id":"`+otherContactID+`","version":9}`), nil
	})
	c, _ := client(t)
	_, err := c.UpdateContact(context.Background(), contactChanges{ID: contactID, Note: str("x")})
	if err == nil || classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), contactMayChanged) ||
		strings.Contains(err.Error(), otherContactID) {
		t.Fatalf("err = %v", err)
	}
}

func TestContactWriteToolsDeclareTheirRiskAndJoinOnlyTheWriteProfile(t *testing.T) {
	want := map[string]struct{ effect, idempotency string }{
		"lexware.contacts.create": {"create", "non_idempotent"},
		"lexware.contacts.update": {"update", "idempotent"},
	}
	reg := registry(t)
	for _, descriptor := range reg.Provider(Provider) {
		expected, ok := want[descriptor.ID]
		if !ok {
			continue
		}
		delete(want, descriptor.ID)
		risk := descriptor.Risk
		if string(risk.Effect) != expected.effect || string(risk.Idempotency) != expected.idempotency ||
			risk.Confirmation != "required" || !risk.OpenWorld || risk.DataSensitivity != contactSensitivity ||
			descriptor.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", descriptor.ID, risk)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := map[string]bool{}
		for _, id := range profile.Tools {
			has[id] = true
		}
		if has["lexware.contacts.create"] != (profile.ID == "write") || has["lexware.contacts.update"] != (profile.ID == "write") {
			t.Errorf("profile %s tools = %v", profile.ID, profile.Tools)
		}
	}
}
