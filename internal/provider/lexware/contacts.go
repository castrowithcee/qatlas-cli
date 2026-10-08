package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// contactSensitivity classifies results as personal and business contact data of the configured Lexware
// organization. The risk model has no dedicated personal-data flag; the provider-owned class carries it.
const contactSensitivity = "lexware-contact-data"

// minSearchLength is the shortest name or e-mail filter. A shorter fragment would turn the search into a
// bulk export of the address book.
const minSearchLength = 3

var contactsReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: contactSensitivity,
}

const uuidSchema = `{"type":"string","minLength":36,"maxLength":36,` +
	`"pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}`

const (
	addressSchema = `{"type":"object","properties":{"supplement":{"type":"string"},"street":{"type":"string"},` +
		`"zip":{"type":"string"},"city":{"type":"string"},"country_code":{"type":"string"}},"additionalProperties":false}`
	roleSchema    = `{"type":"object","properties":{"number":{"type":"integer"}},"additionalProperties":false}`
	contactSchema = `{"type":"object","properties":{"id":{"type":"string"},"version":{"type":"integer"},` +
		`"archived":{"type":"boolean"},` +
		`"roles":{"type":"object","properties":{"customer":` + roleSchema + `,"vendor":` + roleSchema + `},"additionalProperties":false},` +
		`"company":{"type":"object","properties":{"name":{"type":"string"},"tax_number":{"type":"string"},` +
		`"vat_registration_id":{"type":"string"},"allow_tax_free_invoices":{"type":"boolean"},` +
		`"contact_persons":{"type":"array","items":{"type":"object","properties":{"salutation":{"type":"string"},` +
		`"first_name":{"type":"string"},"last_name":{"type":"string"},"primary":{"type":"boolean"},` +
		`"email_address":{"type":"string"},"phone_number":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false},` +
		`"person":{"type":"object","properties":{"salutation":{"type":"string"},"first_name":{"type":"string"},` +
		`"last_name":{"type":"string"}},"additionalProperties":false},` +
		`"addresses":{"type":"object","properties":{"billing":{"type":"array","items":` + addressSchema + `},` +
		`"shipping":{"type":"array","items":` + addressSchema + `}},"additionalProperties":false},` +
		`"email_addresses":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},` +
		`"address":{"type":"string"}},"required":["kind","address"],"additionalProperties":false}},` +
		`"phone_numbers":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},` +
		`"number":{"type":"string"}},"required":["kind","number"],"additionalProperties":false}},` +
		`"note":{"type":"string"}},"required":["id","archived"],"additionalProperties":false}`
)

var contactFields = []capability.Field{
	{Name: "id", Description: "Contact identifier, usable as contact_id when creating an invoice"},
	{Name: "version", Description: "Version counter reported by Lexware"},
	{Name: "archived", Description: "True when the contact is archived in Lexware"},
	{Name: "roles", Description: "Customer and vendor role with their numbers; a missing role means the contact does not hold it"},
	{Name: "company", Description: "Company name, tax identifiers, and contact persons, untrusted personal data; absent for a person"},
	{Name: "person", Description: "Name of a private person, untrusted personal data; absent for a company"},
	{Name: "addresses", Description: "Billing and shipping addresses, untrusted personal data"},
	{Name: "email_addresses", Description: "E-mail addresses with their kind (business, office, private, other), untrusted personal data"},
	{Name: "phone_numbers", Description: "Phone numbers with their kind (business, office, mobile, private, fax, other), untrusted personal data"},
	{Name: "note", Description: "Free-text note on the contact, untrusted data"},
}

const searchDescription = " (at least 3 characters); the text is searched literally as a substring, so _, % and \\ " +
	"have no wildcard meaning"

var contactsList = capability.Descriptor{
	ID:      Provider + ".contacts.list",
	Version: 1,
	Title:   "List Lexware contacts",
	Description: "List one page of customers and vendors of an explicit Lexware Office connection, optionally " +
		"filtered by name, e-mail address, number, or role",
	Tags:     []string{"lexware", "contacts", "list", "customers", "vendors", "accounting"},
	Risk:     contactsReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"email":{"type":"string","minLength":3,"maxLength":255},` +
		`"name":{"type":"string","minLength":3,"maxLength":255},` +
		`"number":{"type":"integer","minimum":0},` +
		`"customer":{"type":"boolean"},"vendor":{"type":"boolean"},` +
		`"page":{"type":"integer","minimum":0,"maximum":200},` +
		`"size":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"contacts":{"type":"array","items":` + contactSchema + `},` +
		`"page":{"type":"integer"},"size":{"type":"integer"},"total_pages":{"type":"integer"},` +
		`"total_elements":{"type":"integer"},"last_page":{"type":"boolean"}},` +
		`"required":["contacts","page","size","total_pages","total_elements","last_page"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "email", Description: "Return contacts with an e-mail address containing this text" + searchDescription},
		{Name: "name", Description: "Return contacts whose name contains this text" + searchDescription},
		{Name: "number", Description: "Return the contact with this customer or vendor number, as an integer"},
		{Name: "customer", Description: "true returns only customers, false only contacts that are no customers"},
		{Name: "vendor", Description: "true returns only vendors, false only contacts that are no vendors"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "contacts", Description: "Contacts on this page, untrusted data; each carries the fields of lexware.contacts.get"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "Find customers whose name contains Muster",
		Arguments:   json.RawMessage(`{"name":"Muster","customer":true,"page":0,"size":25}`),
	}},
}

var contactsGet = capability.Descriptor{
	ID:          Provider + ".contacts.get",
	Version:     1,
	Title:       "Get a Lexware contact",
	Description: "Read one customer or vendor of an explicit Lexware Office connection by its identifier",
	Tags:        []string{"lexware", "contacts", "get", "customers", "vendors", "accounting"},
	Risk:        contactsReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(contactSchema),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Contact identifier as a UUID, as returned by lexware.contacts.list", Required: true},
	},
	Fields: contactFields,
	Examples: []capability.Example{{
		Description: "Read one contact by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

func invokeContactsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options ContactListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list contacts", "the validated arguments could not be read")
	}
	if err := options.normalize(); err != nil {
		return nil, providerError("list contacts", err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListContacts(ctx, options)
}

func invokeContactsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get contact", "the validated arguments could not be read")
	}
	if !validUUID(arguments.ID) {
		return nil, providerError("get contact", "the contact identifier must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetContact(ctx, arguments.ID)
}

// ContactListOptions are the controlled filters of the contact list. Customer and Vendor are sent only when
// set.
type ContactListOptions struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Number   *int   `json:"number"`
	Customer *bool  `json:"customer"`
	Vendor   *bool  `json:"vendor"`
	Page     int    `json:"page"`
	Size     int    `json:"size"`
}

// escapeLike turns a text into a literal pattern for the provider's SQL-style filters: the backslash is
// escaped first so that the escapes added for the placeholders are not escaped again.
func escapeLike(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, `_`, `\_`)
	return strings.ReplaceAll(text, `%`, `\%`)
}

func (o *ContactListOptions) normalize() error {
	for _, text := range []struct{ label, value string }{{"email", o.Email}, {"name", o.Name}} {
		if text.value == "" {
			continue
		}
		if n := utf8.RuneCountInString(text.value); n < minSearchLength || n > 255 {
			return fmt.Errorf("the %s filter needs 3 to 255 characters", text.label)
		}
	}
	if o.Number != nil && *o.Number < 0 {
		return errors.New("the number filter must not be negative")
	}
	return normalizePaging(&o.Page, &o.Size)
}

func contactQuery(options ContactListOptions) url.Values {
	query := url.Values{}
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("size", strconv.Itoa(options.Size))
	if options.Email != "" {
		query.Set("email", escapeLike(options.Email))
	}
	if options.Name != "" {
		query.Set("name", escapeLike(options.Name))
	}
	if options.Number != nil {
		query.Set("number", strconv.Itoa(*options.Number))
	}
	if options.Customer != nil {
		query.Set("customer", strconv.FormatBool(*options.Customer))
	}
	if options.Vendor != nil {
		query.Set("vendor", strconv.FormatBool(*options.Vendor))
	}
	return query
}

// ContactListResult is the normalised page of contacts.
type ContactListResult struct {
	Contacts      []ContactRecord `json:"contacts"`
	Page          int             `json:"page"`
	Size          int             `json:"size"`
	TotalPages    int             `json:"total_pages"`
	TotalElements int             `json:"total_elements"`
	LastPage      bool            `json:"last_page"`
}

// ContactRecord is the stable Qatlas view of one contact. Everything a Lexware user typed is untrusted
// personal data.
type ContactRecord struct {
	ID             string          `json:"id"`
	Version        int             `json:"version,omitempty"`
	Archived       bool            `json:"archived"`
	Roles          *ContactRoles   `json:"roles,omitempty"`
	Company        *ContactCompany `json:"company,omitempty"`
	Person         *ContactPerson  `json:"person,omitempty"`
	Addresses      *ContactAddrs   `json:"addresses,omitempty"`
	EmailAddresses []ContactEmail  `json:"email_addresses,omitempty"`
	PhoneNumbers   []ContactPhone  `json:"phone_numbers,omitempty"`
	Note           string          `json:"note,omitempty"`
}

// ContactRoles holds a role only when the contact has it.
type ContactRoles struct {
	Customer *ContactRole `json:"customer,omitempty"`
	Vendor   *ContactRole `json:"vendor,omitempty"`
}

// ContactRole carries the number Lexware assigned for the role.
type ContactRole struct {
	Number int `json:"number"`
}

// ContactCompany is the company side of a contact.
type ContactCompany struct {
	Name                 string          `json:"name,omitempty"`
	TaxNumber            string          `json:"tax_number,omitempty"`
	VatRegistrationID    string          `json:"vat_registration_id,omitempty"`
	AllowTaxFreeInvoices bool            `json:"allow_tax_free_invoices,omitempty"`
	ContactPersons       []ContactPerson `json:"contact_persons,omitempty"`
}

// ContactPerson is a private person or a contact person of a company.
type ContactPerson struct {
	Salutation   string `json:"salutation,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	Primary      bool   `json:"primary,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
	PhoneNumber  string `json:"phone_number,omitempty"`
}

// ContactAddrs groups the billing and shipping addresses of a contact.
type ContactAddrs struct {
	Billing  []ContactAddress `json:"billing,omitempty"`
	Shipping []ContactAddress `json:"shipping,omitempty"`
}

// ContactAddress is one postal address.
type ContactAddress struct {
	Supplement  string `json:"supplement,omitempty"`
	Street      string `json:"street,omitempty"`
	Zip         string `json:"zip,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

// ContactEmail is one e-mail address with its kind.
type ContactEmail struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

// ContactPhone is one phone number with its kind.
type ContactPhone struct {
	Kind   string `json:"kind"`
	Number string `json:"number"`
}

// contactJSON mirrors the provider fields the contact operations read.
type contactJSON struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Roles   struct {
		Customer *struct {
			Number int `json:"number"`
		} `json:"customer"`
		Vendor *struct {
			Number int `json:"number"`
		} `json:"vendor"`
	} `json:"roles"`
	Company *struct {
		Name                 string `json:"name"`
		TaxNumber            string `json:"taxNumber"`
		VatRegistrationID    string `json:"vatRegistrationId"`
		AllowTaxFreeInvoices bool   `json:"allowTaxFreeInvoices"`
		ContactPersons       []struct {
			Salutation   string `json:"salutation"`
			FirstName    string `json:"firstName"`
			LastName     string `json:"lastName"`
			Primary      bool   `json:"primary"`
			EmailAddress string `json:"emailAddress"`
			PhoneNumber  string `json:"phoneNumber"`
		} `json:"contactPersons"`
	} `json:"company"`
	Person *struct {
		Salutation string `json:"salutation"`
		FirstName  string `json:"firstName"`
		LastName   string `json:"lastName"`
	} `json:"person"`
	Addresses struct {
		Billing  []addressJSON `json:"billing"`
		Shipping []addressJSON `json:"shipping"`
	} `json:"addresses"`
	EmailAddresses map[string][]string `json:"emailAddresses"`
	PhoneNumbers   map[string][]string `json:"phoneNumbers"`
	Note           string              `json:"note"`
	Archived       bool                `json:"archived"`
}

type addressJSON struct {
	Supplement  string `json:"supplement"`
	Street      string `json:"street"`
	Zip         string `json:"zip"`
	City        string `json:"city"`
	CountryCode string `json:"countryCode"`
}

// The kinds Lexware documents, in the order the normalised lists report them. A kind outside them is not
// reported.
var (
	emailKinds = []string{"business", "office", "private", "other"}
	phoneKinds = []string{"business", "office", "mobile", "private", "fax", "other"}
)

func (raw contactJSON) record() ContactRecord {
	record := ContactRecord{ID: raw.ID, Version: raw.Version, Archived: raw.Archived, Note: raw.Note}
	var roles ContactRoles
	if raw.Roles.Customer != nil {
		roles.Customer = &ContactRole{Number: raw.Roles.Customer.Number}
	}
	if raw.Roles.Vendor != nil {
		roles.Vendor = &ContactRole{Number: raw.Roles.Vendor.Number}
	}
	if roles != (ContactRoles{}) {
		record.Roles = &roles
	}
	if c := raw.Company; c != nil {
		company := ContactCompany{Name: c.Name, TaxNumber: c.TaxNumber, VatRegistrationID: c.VatRegistrationID,
			AllowTaxFreeInvoices: c.AllowTaxFreeInvoices}
		for _, p := range c.ContactPersons {
			company.ContactPersons = append(company.ContactPersons, ContactPerson{
				Salutation: p.Salutation, FirstName: p.FirstName, LastName: p.LastName, Primary: p.Primary,
				EmailAddress: p.EmailAddress, PhoneNumber: p.PhoneNumber,
			})
		}
		record.Company = &company
	} else if p := raw.Person; p != nil {
		record.Person = &ContactPerson{Salutation: p.Salutation, FirstName: p.FirstName, LastName: p.LastName}
	}
	var addresses ContactAddrs
	for _, a := range raw.Addresses.Billing {
		addresses.Billing = append(addresses.Billing, ContactAddress(a))
	}
	for _, a := range raw.Addresses.Shipping {
		addresses.Shipping = append(addresses.Shipping, ContactAddress(a))
	}
	if len(addresses.Billing)+len(addresses.Shipping) > 0 {
		record.Addresses = &addresses
	}
	for _, kind := range emailKinds {
		for _, address := range raw.EmailAddresses[kind] {
			record.EmailAddresses = append(record.EmailAddresses, ContactEmail{Kind: kind, Address: address})
		}
	}
	for _, kind := range phoneKinds {
		for _, number := range raw.PhoneNumbers[kind] {
			record.PhoneNumbers = append(record.PhoneNumbers, ContactPhone{Kind: kind, Number: number})
		}
	}
	return record
}

// ListContacts reads exactly one page of contacts.
func (c *Client) ListContacts(ctx context.Context, options ContactListOptions) (*ContactListResult, error) {
	const op = "list contacts"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	var page struct {
		Content []contactJSON `json:"content"`
		pagingJSON
	}
	if err := c.get(ctx, op, "", "/v1/contacts", contactQuery(options), &page); err != nil {
		return nil, err
	}
	contacts := make([]ContactRecord, 0, len(page.Content))
	for _, entry := range page.Content {
		if !validUUID(entry.ID) {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned a contact without a usable identifier",
			}
		}
		contacts = append(contacts, entry.record())
	}
	return &ContactListResult{
		Contacts: contacts, Page: page.Number, Size: page.Size, TotalPages: page.TotalPages,
		TotalElements: page.TotalElements, LastPage: page.Last,
	}, nil
}

// GetContact reads exactly the contact of a validated identifier and performs no other provider I/O.
func (c *Client) GetContact(ctx context.Context, id string) (*ContactRecord, error) {
	const op = "get contact"
	if !validUUID(id) {
		return nil, providerError(op, "the contact identifier must be a UUID")
	}
	var raw contactJSON
	if err := c.get(ctx, op, resourceContact, "/v1/contacts/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different contact than the requested one",
		}
	}
	record := raw.record()
	return &record, nil
}
