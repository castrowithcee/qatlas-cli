package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	contactMayExist   = "; the contact may have been created, check the contact list before repeating it"
	contactMayChanged = "; the contact may have been changed, read it before repeating the change"
)

var contactCreateRisk = capability.Risk{
	Effect:          capability.EffectCreate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: contactSensitivity,
}

// The version read before the change makes a repeated update harmless: it fails as a conflict.
var contactUpdateRisk = capability.Risk{
	Effect:          capability.EffectUpdate,
	Idempotency:     capability.IdempotencyIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: contactSensitivity,
}

func textSchema(min, max int) string {
	return `{"type":"string","minLength":` + itoa(min) + `,"maxLength":` + itoa(max) + `}`
}

func itoa(n int) string { return strconv.Itoa(n) }

const (
	maxName = 255
	maxCode = 64
	maxNote = 1000
)

// contactWriteProperties are the closed, writable contact members. create adds the required members the
// provider needs, update keeps every nested member optional.
func contactWriteProperties(create bool) string {
	req := func(names ...string) string {
		if !create {
			return ""
		}
		quoted := `,"required":["`
		return quoted + strings.Join(names, `","`) + `"]`
	}
	email := textSchema(1, 255)
	phone := textSchema(1, 64)
	contactPerson := `{"type":"object","properties":{"salutation":` + textSchema(0, 32) + `,"first_name":` + textSchema(0, maxName) +
		`,"last_name":` + textSchema(1, maxName) + `,"primary":{"type":"boolean"},"email_address":` + email +
		`,"phone_number":` + phone + `},"minProperties":1` + req("last_name") + `,"additionalProperties":false}`
	address := `{"type":"object","properties":{"supplement":` + textSchema(0, maxName) + `,"street":` + textSchema(1, maxName) +
		`,"zip":` + textSchema(1, 32) + `,"city":` + textSchema(1, maxName) +
		`,"country_code":{"type":"string","pattern":"^[A-Z]{2}$"}},"minProperties":1` + req("country_code") + `,"additionalProperties":false}`
	return `"roles":{"type":"object","properties":{"customer":{"type":"boolean"},"vendor":{"type":"boolean"}},` +
		`"minProperties":1,"additionalProperties":false},` +
		`"person":{"type":"object","properties":{"salutation":` + textSchema(0, 32) + `,"first_name":` + textSchema(0, maxName) +
		`,"last_name":` + textSchema(1, maxName) + `},"minProperties":1` + req("last_name") + `,"additionalProperties":false},` +
		`"company":{"type":"object","properties":{"name":` + textSchema(1, maxName) + `,"tax_number":` + textSchema(0, maxCode) +
		`,"vat_registration_id":` + textSchema(0, maxCode) + `,"allow_tax_free_invoices":{"type":"boolean"},` +
		`"contact_person":` + contactPerson + `},"minProperties":1` + req("name") + `,"additionalProperties":false},` +
		`"billing_address":` + address + `,"shipping_address":` + address + `,` +
		`"email_addresses":{"type":"object","properties":{"business":` + email + `,"office":` + email + `,"private":` + email +
		`,"other":` + email + `},"minProperties":1,"additionalProperties":false},` +
		`"phone_numbers":{"type":"object","properties":{"business":` + phone + `,"office":` + phone + `,"mobile":` + phone +
		`,"private":` + phone + `,"fax":` + phone + `,"other":` + phone + `},"minProperties":1,"additionalProperties":false},` +
		`"note":` + textSchema(0, maxNote)
}

var contactWriteFields = []capability.Field{
	{Name: "id", Description: "Identifier of the contact"},
	{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
	{Name: "updated_date", Description: "Last change timestamp reported by Lexware"},
	{Name: "version", Description: "Version counter after the write, reported by Lexware"},
}

var contactWriteArguments = []capability.Argument{
	{Name: "roles", Description: "Object with customer and vendor as booleans; create needs at least one true, update can only add a role"},
	{Name: "person", Description: "Private person with salutation, first_name and last_name; give person or company, never both"},
	{Name: "company", Description: "Company with name, tax_number, vat_registration_id, allow_tax_free_invoices and one contact_person (salutation, first_name, last_name, primary, email_address, phone_number)"},
	{Name: "billing_address", Description: "Billing address with supplement, street, zip, city and country_code as a two-letter upper-case code"},
	{Name: "shipping_address", Description: "Shipping address, same members as billing_address"},
	{Name: "email_addresses", Description: "One e-mail address per kind: business, office, private, other"},
	{Name: "phone_numbers", Description: "One phone number per kind: business, office, mobile, private, fax, other"},
	{Name: "note", Description: "Internal note, up to 1000 characters"},
}

func contactCreateArguments() []capability.Argument {
	arguments := append([]capability.Argument{}, contactWriteArguments...)
	arguments[0].Required = true
	return arguments
}

var contactsCreate = capability.Descriptor{
	ID: Provider + ".contacts.create", Version: 1, Title: "Create a Lexware contact",
	Description: "Create one customer or vendor, a person or a company, in the Lexware Office contact master data",
	Tags:        []string{"lexware", "contacts", "create", "customers", "vendors", "accounting"},
	Provider:    Provider, Risk: contactCreateRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + contactWriteProperties(true) +
		`},"required":["roles"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments:    contactCreateArguments(),
	Fields:       contactWriteFields,
	Examples: []capability.Example{{
		Description: "A customer company with a billing address",
		Arguments: json.RawMessage(`{"roles":{"customer":true},"company":{"name":"Example Company"},` +
			`"billing_address":{"street":"Example Street 1","zip":"00000","city":"Example City","country_code":"DE"}}`),
	}},
}

var contactsUpdate = capability.Descriptor{
	ID: Provider + ".contacts.update", Version: 1, Title: "Update a Lexware contact",
	Description: "Change fields of one contact: Qatlas reads the contact, replaces only the given fields and " +
		"writes it back with the version it read. A change made by someone else in between is reported as a " +
		"conflict and never overwritten. A contact cannot switch between person and company, and a contact " +
		"with more than one address, e-mail address, phone number or contact person is refused unchanged",
	Tags:     []string{"lexware", "contacts", "update", "customers", "vendors", "accounting"},
	Provider: Provider, Risk: contactUpdateRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,` +
		contactWriteProperties(false) + `},"required":["id"],"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Contact identifier as a UUID, as returned by lexware.contacts.list", Required: true},
	}, contactWriteArguments...),
	Fields: contactWriteFields,
	Examples: []capability.Example{{
		Description: "Change the e-mail address of one contact",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555",` +
			`"email_addresses":{"business":"info@example.invalid"}}`),
	}},
}

// textField is one writable string member with its bounds, in characters.
type textField struct {
	key, name string
	value     *string
	min, max  int
}

type contactRoles struct {
	Customer *bool `json:"customer"`
	Vendor   *bool `json:"vendor"`
}

type contactPersonChanges struct {
	Salutation   *string `json:"salutation"`
	FirstName    *string `json:"first_name"`
	LastName     *string `json:"last_name"`
	Primary      *bool   `json:"primary"`
	EmailAddress *string `json:"email_address"`
	PhoneNumber  *string `json:"phone_number"`
}

func (p *contactPersonChanges) texts() []textField {
	return []textField{{"salutation", "company.contact_person.salutation", p.Salutation, 0, 32},
		{"firstName", "company.contact_person.first_name", p.FirstName, 0, maxName},
		{"lastName", "company.contact_person.last_name", p.LastName, 1, maxName},
		{"emailAddress", "company.contact_person.email_address", p.EmailAddress, 1, 255},
		{"phoneNumber", "company.contact_person.phone_number", p.PhoneNumber, 1, 64}}
}

type personChanges struct {
	Salutation *string `json:"salutation"`
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
}

func (p *personChanges) texts() []textField {
	return []textField{{"salutation", "person.salutation", p.Salutation, 0, 32},
		{"firstName", "person.first_name", p.FirstName, 0, maxName},
		{"lastName", "person.last_name", p.LastName, 1, maxName}}
}

type companyChanges struct {
	Name                 *string               `json:"name"`
	TaxNumber            *string               `json:"tax_number"`
	VATRegistrationID    *string               `json:"vat_registration_id"`
	AllowTaxFreeInvoices *bool                 `json:"allow_tax_free_invoices"`
	ContactPerson        *contactPersonChanges `json:"contact_person"`
}

func (c *companyChanges) texts() []textField {
	return []textField{{"name", "company.name", c.Name, 1, maxName},
		{"taxNumber", "company.tax_number", c.TaxNumber, 0, maxCode},
		{"vatRegistrationId", "company.vat_registration_id", c.VATRegistrationID, 0, maxCode}}
}

type addressChanges struct {
	Supplement  *string `json:"supplement"`
	Street      *string `json:"street"`
	Zip         *string `json:"zip"`
	City        *string `json:"city"`
	CountryCode *string `json:"country_code"`
}

func (a *addressChanges) texts(prefix string) []textField {
	return []textField{{"supplement", prefix + ".supplement", a.Supplement, 0, maxName},
		{"street", prefix + ".street", a.Street, 1, maxName}, {"zip", prefix + ".zip", a.Zip, 1, 32},
		{"city", prefix + ".city", a.City, 1, maxName}, {"countryCode", prefix + ".country_code", a.CountryCode, 2, 2}}
}

type emailChanges struct {
	Business *string `json:"business"`
	Office   *string `json:"office"`
	Private  *string `json:"private"`
	Other    *string `json:"other"`
}

func (e *emailChanges) texts() []textField {
	return []textField{{"business", "email_addresses.business", e.Business, 1, 255},
		{"office", "email_addresses.office", e.Office, 1, 255},
		{"private", "email_addresses.private", e.Private, 1, 255}, {"other", "email_addresses.other", e.Other, 1, 255}}
}

type phoneChanges struct {
	Business *string `json:"business"`
	Office   *string `json:"office"`
	Mobile   *string `json:"mobile"`
	Private  *string `json:"private"`
	Fax      *string `json:"fax"`
	Other    *string `json:"other"`
}

func (p *phoneChanges) texts() []textField {
	return []textField{{"business", "phone_numbers.business", p.Business, 1, 64},
		{"office", "phone_numbers.office", p.Office, 1, 64}, {"mobile", "phone_numbers.mobile", p.Mobile, 1, 64},
		{"private", "phone_numbers.private", p.Private, 1, 64}, {"fax", "phone_numbers.fax", p.Fax, 1, 64},
		{"other", "phone_numbers.other", p.Other, 1, 64}}
}

// contactChanges holds the writable contact members. A nil member is absent, so an update changes only
// what the caller named.
type contactChanges struct {
	ID              string          `json:"id"`
	Roles           *contactRoles   `json:"roles"`
	Person          *personChanges  `json:"person"`
	Company         *companyChanges `json:"company"`
	BillingAddress  *addressChanges `json:"billing_address"`
	ShippingAddress *addressChanges `json:"shipping_address"`
	EmailAddresses  *emailChanges   `json:"email_addresses"`
	PhoneNumbers    *phoneChanges   `json:"phone_numbers"`
	Note            *string         `json:"note"`
}

func decodeContactChanges(op string, raw json.RawMessage) (contactChanges, error) {
	var input contactChanges
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// allTexts lists every string member the changes name.
func (c contactChanges) allTexts() []textField {
	var fields []textField
	if c.Person != nil {
		fields = append(fields, c.Person.texts()...)
	}
	if c.Company != nil {
		fields = append(fields, c.Company.texts()...)
		if c.Company.ContactPerson != nil {
			fields = append(fields, c.Company.ContactPerson.texts()...)
		}
	}
	if c.BillingAddress != nil {
		fields = append(fields, c.BillingAddress.texts("billing_address")...)
	}
	if c.ShippingAddress != nil {
		fields = append(fields, c.ShippingAddress.texts("shipping_address")...)
	}
	if c.EmailAddresses != nil {
		fields = append(fields, c.EmailAddresses.texts()...)
	}
	if c.PhoneNumbers != nil {
		fields = append(fields, c.PhoneNumbers.texts()...)
	}
	return append(fields, textField{"note", "note", c.Note, 0, maxNote})
}

// validate re-checks the bounds the schema states, because the handler is also reachable without it. The
// errors name members, never values.
func (c contactChanges) validate(update bool) error {
	for _, field := range c.allTexts() {
		if field.value == nil {
			continue
		}
		if n := utf8.RuneCountInString(*field.value); n < field.min || n > field.max {
			return errors.New(field.name + " has an unsupported length")
		}
		if strings.HasSuffix(field.name, "country_code") && (strings.Trim(*field.value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
			return errors.New(field.name + " must be a two-letter upper-case code")
		}
	}
	if r := c.Roles; r != nil {
		if r.Customer == nil && r.Vendor == nil {
			return errors.New("roles needs customer or vendor")
		}
		if update && ((r.Customer != nil && !*r.Customer) || (r.Vendor != nil && !*r.Vendor)) {
			return errors.New("a role cannot be removed from a contact")
		}
	}
	if (c.Person != nil && *c.Person == personChanges{}) || (c.BillingAddress != nil && *c.BillingAddress == addressChanges{}) ||
		(c.ShippingAddress != nil && *c.ShippingAddress == addressChanges{}) ||
		(c.EmailAddresses != nil && *c.EmailAddresses == emailChanges{}) || (c.PhoneNumbers != nil && *c.PhoneNumbers == phoneChanges{}) ||
		(c.Company != nil && *c.Company == companyChanges{}) ||
		(c.Company != nil && c.Company.ContactPerson != nil && *c.Company.ContactPerson == contactPersonChanges{}) {
		return errors.New("a given group needs at least one member")
	}
	if c.Person != nil && c.Company != nil {
		return errors.New("a contact is either a person or a company")
	}
	return nil
}

// validateCreate adds the rules for a new contact on top of validate.
func (c contactChanges) validateCreate() error {
	switch {
	case c.ID != "":
		return errors.New("id is not allowed")
	case c.Roles == nil || ((c.Roles.Customer == nil || !*c.Roles.Customer) && (c.Roles.Vendor == nil || !*c.Roles.Vendor)):
		return errors.New("at least one role is required")
	case c.Person == nil && c.Company == nil:
		return errors.New("person or company is required")
	case c.Person != nil && c.Person.LastName == nil:
		return errors.New("person.last_name is required")
	case c.Company != nil && c.Company.Name == nil:
		return errors.New("company.name is required")
	}
	return c.validate(false)
}

// refusal is a fixed, value-free refusal raised before any write.
func refusal(op, message string) error { return providerError(op, message) }

// rejectMultipleEntries refuses a contact whose list members Lexware would shorten on a write.
func rejectMultipleEntries(op string, contact rawObject) error {
	many := func(raw json.RawMessage) bool {
		var entries []json.RawMessage
		return json.Unmarshal(raw, &entries) == nil && len(entries) > 1
	}
	for _, group := range []string{"addresses", "emailAddresses", "phoneNumbers"} {
		for _, raw := range contact.object(group) {
			if many(raw) {
				return refusal(op, "the contact has several addresses, e-mail addresses, or phone numbers of one kind; "+
					"Qatlas does not change such contacts because Lexware would drop entries")
			}
		}
	}
	if many(contact.object("company")["contactPersons"]) {
		return refusal(op, "the contact has several contact persons; Qatlas does not change such contacts because "+
			"Lexware would drop entries")
	}
	return nil
}

func setTexts(object rawObject, fields []textField) error {
	for _, field := range fields {
		if field.value != nil {
			if err := object.set(field.key, *field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeFirst applies change to the single entry of an object list, creating it when the list is empty.
func mergeFirst(parent rawObject, key string, change func(rawObject) error) error {
	var entries []rawObject
	if json.Unmarshal(parent[key], &entries) != nil || len(entries) == 0 {
		entries = []rawObject{{}}
	}
	if err := change(entries[0]); err != nil {
		return err
	}
	return parent.set(key, entries[:1])
}

func hasText(object rawObject, key string) bool {
	var value string
	return json.Unmarshal(object[key], &value) == nil && value != ""
}

// kindList sets the single entry of every named kind in a string-list group.
func kindList(object rawObject, group string, fields []textField) error {
	members := object.object(group)
	for _, field := range fields {
		if field.value != nil {
			if err := members.set(field.key, []string{*field.value}); err != nil {
				return err
			}
		}
	}
	return object.set(group, members)
}

// set writes the named members into a contact object; update additionally refuses what Lexware could lose.
func (c contactChanges) set(op string, update bool, object rawObject) error {
	if update {
		if err := rejectMultipleEntries(op, object); err != nil {
			return err
		}
	}
	if r := c.Roles; r != nil {
		roles := object.object("roles")
		for key, on := range map[string]*bool{"customer": r.Customer, "vendor": r.Vendor} {
			if on != nil && *on && roles[key] == nil {
				roles[key] = json.RawMessage(`{}`)
			}
		}
		if err := object.set("roles", roles); err != nil {
			return err
		}
	}
	if c.Person != nil {
		if object["company"] != nil {
			return refusal(op, "a company cannot be changed into a person")
		}
		person := object.object("person")
		if err := setTexts(person, c.Person.texts()); err != nil {
			return err
		}
		if err := object.set("person", person); err != nil {
			return err
		}
	}
	if c.Company != nil {
		if err := c.setCompany(op, object); err != nil {
			return err
		}
	}
	if c.BillingAddress != nil || c.ShippingAddress != nil {
		addresses := object.object("addresses")
		for key, change := range map[string]*addressChanges{"billing": c.BillingAddress, "shipping": c.ShippingAddress} {
			if change == nil {
				continue
			}
			err := mergeFirst(addresses, key, func(entry rawObject) error {
				if err := setTexts(entry, change.texts("")); err != nil {
					return err
				}
				if !hasText(entry, "countryCode") {
					return refusal(op, "an address needs a country_code")
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if err := object.set("addresses", addresses); err != nil {
			return err
		}
	}
	if c.EmailAddresses != nil {
		if err := kindList(object, "emailAddresses", c.EmailAddresses.texts()); err != nil {
			return err
		}
	}
	if c.PhoneNumbers != nil {
		if err := kindList(object, "phoneNumbers", c.PhoneNumbers.texts()); err != nil {
			return err
		}
	}
	if c.Note != nil {
		return object.set("note", *c.Note)
	}
	return nil
}

func (c contactChanges) setCompany(op string, object rawObject) error {
	if object["person"] != nil {
		return refusal(op, "a person cannot be changed into a company")
	}
	company := object.object("company")
	change := c.Company
	if err := setTexts(company, change.texts()); err != nil {
		return err
	}
	if change.AllowTaxFreeInvoices != nil {
		if err := company.set("allowTaxFreeInvoices", *change.AllowTaxFreeInvoices); err != nil {
			return err
		}
	}
	if person := change.ContactPerson; person != nil {
		err := mergeFirst(company, "contactPersons", func(entry rawObject) error {
			if err := setTexts(entry, person.texts()); err != nil {
				return err
			}
			if person.Primary != nil {
				if err := entry.set("primary", *person.Primary); err != nil {
					return err
				}
			}
			if !hasText(entry, "lastName") {
				return refusal(op, "a contact person needs a last_name")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return object.set("company", company)
}

func invokeContactsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create contact"
	input, err := decodeContactChanges(op, raw)
	if err != nil {
		return nil, err
	}
	if err := input.validateCreate(); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateContact(ctx, input)
}

func invokeContactsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update contact"
	input, err := decodeContactChanges(op, raw)
	if err != nil {
		return nil, err
	}
	if !validUUID(input.ID) {
		return nil, providerError(op, "the contact identifier must be a UUID")
	}
	changed := input
	changed.ID = ""
	if changed == (contactChanges{}) {
		return nil, providerError(op, "at least one field to change is required")
	}
	if err := input.validate(true); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateContact(ctx, input)
}

// CreateContact posts one contact at version 0: exactly one request, never repeated.
func (c *Client) CreateContact(ctx context.Context, input contactChanges) (*createResult, error) {
	const op = "create contact"
	object := rawObject{}
	if err := object.set("version", 0); err != nil {
		return nil, providerError(op, "the change could not be applied")
	}
	if err := input.set(op, false, object); err != nil {
		return nil, err
	}
	return c.postObject(ctx, op, "/v1/contacts", object, contactMayExist, "a contact")
}

// UpdateContact reads the contact and writes it back with the given fields replaced and the version it read.
func (c *Client) UpdateContact(ctx context.Context, input contactChanges) (*createResult, error) {
	const op = "update contact"
	return c.updateObject(ctx, op, resourceContact, "/v1/contacts", input.ID, contactMayChanged, "a contact",
		func(object rawObject) error { return input.set(op, true, object) })
}
