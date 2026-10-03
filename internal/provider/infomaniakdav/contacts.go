package infomaniakdav

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/emersion/go-vcard"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the contact tools. Every provider string and every multi-valued property is capped; a photo or
// other binary property is never reported.
const (
	defaultContactLimit = 50
	maxContactLimit     = 200
	maxContactText      = 256
	maxContactNote      = 4096
	maxContactEntries   = 20
	maxContactType      = 64
	maxContactBirthday  = 32
	contactsQueryBody   = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<card:addressbook-query xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
		`<d:prop><d:getetag/><card:address-data/></d:prop></card:addressbook-query>`
)

const (
	typedValueSchema = `{"type":"object","properties":{"value":{"type":"string"},"type":{"type":"string"}},` +
		`"additionalProperties":false}`
	nameSchema = `{"type":"object","properties":{"family":{"type":"string"},"given":{"type":"string"},` +
		`"additional":{"type":"string"},"prefix":{"type":"string"},"suffix":{"type":"string"}},` +
		`"additionalProperties":false}`
	postalSchema = `{"type":"object","properties":{"type":{"type":"string"},"po_box":{"type":"string"},` +
		`"extended":{"type":"string"},"street":{"type":"string"},"locality":{"type":"string"},` +
		`"region":{"type":"string"},"postal_code":{"type":"string"},"country":{"type":"string"}},` +
		`"additionalProperties":false}`
	contactSchema = `{"type":"object","properties":{` +
		`"id":{"type":"string"},"uid":{"type":"string"},"name":{"type":"string"},"structured_name":` + nameSchema + `,` +
		`"email":{"type":"string"},"emails":{"type":"array","items":` + typedValueSchema + `},` +
		`"phones":{"type":"array","items":` + typedValueSchema + `},` +
		`"addresses":{"type":"array","items":` + postalSchema + `},` +
		`"organization":{"type":"string"},"title":{"type":"string"},"birthday":{"type":"string"},` +
		`"note":{"type":"string"},"urls":{"type":"array","items":{"type":"string"}},` +
		`"truncated":{"type":"array","items":{"type":"string"}},"etag":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false}`
	addressbookArgSchema = `"addressbook":{"type":"string","minLength":1,"maxLength":128}`
)

var contactRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: contactsSensitivity,
}

var contactFields = []capability.Field{
	{Name: "id", Description: "Last path segment of the contact resource, the id argument of the get tool"},
	{Name: "uid", Description: "vCard UID, untrusted data"},
	{Name: "name", Description: "Formatted name (FN), untrusted personal data; built from the structured name if absent"},
	{Name: "email", Description: "First e-mail address, untrusted personal data; only the list tool reports it"},
	{Name: "organization", Description: "Organization, untrusted data"},
	{Name: "structured_name", Description: "Family, given, additional name, prefix, suffix; only the get tool reports it"},
	{Name: "emails", Description: "At most 20 e-mail addresses with type; only the get tool reports them"},
	{Name: "phones", Description: "At most 20 phone numbers with type; only the get tool reports them"},
	{Name: "addresses", Description: "At most 20 postal addresses with type; only the get tool reports them"},
	{Name: "title", Description: "Job title; only the get tool reports it"},
	{Name: "birthday", Description: "Birthday as the card states it; only the get tool reports it"},
	{Name: "note", Description: "Note, untrusted data; only the get tool reports it"},
	{Name: "urls", Description: "At most 20 URLs, untrusted data, never followed; only the get tool reports them"},
	{Name: "truncated", Description: "Names of the members that were cut at their cap; only the get tool reports it"},
	{Name: "etag", Description: "Entity tag of the current version of the contact"},
	{Name: "count", Description: "Number of reported contacts"},
}

var contactsList = capability.Descriptor{
	ID: Provider + ".contacts.list", Version: 1, Title: "List Infomaniak contacts",
	Description: "List the contacts of one allow-listed address book, ordered by name; the result is limited " +
		"and compact, and a card that is not a valid vCard is left out",
	Tags: []string{"infomaniak", "dav", "contacts", "contact", "list"}, Risk: contactRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["addressbook"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"addressbook":{"type":"string"},` +
		`"contacts":{"type":"array","items":` + contactSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["addressbook","contacts","count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "addressbook", Description: "ID of an allow-listed address book, the ID of its addressbook/ID target", Required: true},
		{Name: "limit", Description: "Maximum number of contacts, 50 by default and at most 200"},
	},
	Fields: contactFields,
	Examples: []capability.Example{{
		Description: "List the contacts of one address book",
		Arguments:   json.RawMessage(`{"addressbook":"main"}`),
	}},
}

var contactsGet = capability.Descriptor{
	ID: Provider + ".contacts.get", Version: 1, Title: "Read an Infomaniak contact",
	Description: "Read one contact of an allow-listed address book with its structured fields and entity tag; " +
		"a photo or other binary data is never returned",
	Tags: []string{"infomaniak", "dav", "contacts", "contact", "get"}, Risk: contactRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":128}},"required":["addressbook","id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"addressbook":{"type":"string"},` +
		`"contact":` + contactSchema + `},"required":["addressbook","contact"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "addressbook", Description: "ID of an allow-listed address book, the ID of its addressbook/ID target", Required: true},
		{Name: "id", Description: "Contact id as the list tool reports it: one path segment of the contact resource", Required: true},
	},
	Fields: contactFields,
	Examples: []capability.Example{{
		Description: "Read one contact the list tool reported",
		Arguments:   json.RawMessage(`{"addressbook":"main","id":"ada.vcf"}`),
	}},
}

// TypedValue is an e-mail address or phone number with its type parameter.
type TypedValue struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

// ContactName is the structured name (N) of a contact.
type ContactName struct {
	Family     string `json:"family,omitempty"`
	Given      string `json:"given,omitempty"`
	Additional string `json:"additional,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	Suffix     string `json:"suffix,omitempty"`
}

// PostalAddress is one ADR of a contact.
type PostalAddress struct {
	Type       string `json:"type,omitempty"`
	POBox      string `json:"po_box,omitempty"`
	Extended   string `json:"extended,omitempty"`
	Street     string `json:"street,omitempty"`
	Locality   string `json:"locality,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
}

// Contact is one contact. The list tool reports only the compact members.
type Contact struct {
	ID             string          `json:"id"`
	UID            string          `json:"uid,omitempty"`
	Name           string          `json:"name,omitempty"`
	StructuredName *ContactName    `json:"structured_name,omitempty"`
	Email          string          `json:"email,omitempty"`
	Emails         []TypedValue    `json:"emails,omitempty"`
	Phones         []TypedValue    `json:"phones,omitempty"`
	Addresses      []PostalAddress `json:"addresses,omitempty"`
	Organization   string          `json:"organization,omitempty"`
	Title          string          `json:"title,omitempty"`
	Birthday       string          `json:"birthday,omitempty"`
	Note           string          `json:"note,omitempty"`
	URLs           []string        `json:"urls,omitempty"`
	Truncated      []string        `json:"truncated,omitempty"`
	ETag           string          `json:"etag,omitempty"`
}

// ContactsPage is the result of the list tool.
type ContactsPage struct {
	Addressbook string    `json:"addressbook"`
	Contacts    []Contact `json:"contacts"`
	Count       int       `json:"count"`
	Truncated   bool      `json:"truncated,omitempty"`
}

// ContactResult is the result of the get tool.
type ContactResult struct {
	Addressbook string  `json:"addressbook"`
	Contact     Contact `json:"contact"`
}

type contactArguments struct {
	Addressbook string `json:"addressbook"`
	Limit       int    `json:"limit"`
	ID          string `json:"id"`
}

// allowedAddressbook checks the address book argument against the allow-list before any secret or request.
func allowedAddressbook(op string, resolved *config.Resolved, book string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validCollectionID(book) || !contains(bound.addressbooks, book) {
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this connection does not allow this address book"}
	}
	return nil
}

func decodeContactArguments(op string, raw json.RawMessage) (contactArguments, error) {
	var input contactArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeContactsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list contacts"
	input, err := decodeContactArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := allowedAddressbook(op, resolved, input.Addressbook); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultContactLimit
	}
	if limit < 0 || limit > maxContactLimit {
		return nil, providerError(op, "limit must be between 1 and 200")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listContacts(ctx, input.Addressbook, limit)
}

func invokeContactsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read contact"
	input, err := decodeContactArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := allowedAddressbook(op, resolved, input.Addressbook); err != nil {
		return nil, err
	}
	if !validCollectionID(input.ID) {
		return nil, providerError(op, "the contact id must be one literal path segment as the list tool reports it")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.getContact(ctx, input.Addressbook, input.ID)
}

// addressbookHome runs the discovery chain up to the address book home set.
func (c *Client) addressbookHome(ctx context.Context, op string) ([]string, error) {
	principal, err := c.discoverPrincipal(ctx, op)
	if err != nil {
		return nil, err
	}
	resources, err := c.propfind(ctx, op, principal, "0", addressbookHomeBody)
	if err != nil {
		return nil, err
	}
	return singleLink(op, resources, propBookHome)
}

func (c *Client) listContacts(ctx context.Context, book string, limit int) (*ContactsPage, error) {
	const op = "list contacts"
	home, err := c.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	base := append(append([]string{}, home...), book)
	data, _, err := c.request(ctx, op, methodReport, base, true, "1", contactsQueryBody, 207, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := parseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &ContactsPage{Addressbook: book, Contacts: []Contact{}}
	seen := map[string]bool{}
	for i := range resources {
		id, err := eventID(op, resources[i].href, base)
		if err != nil {
			return nil, err
		}
		if resources[i].failure(op) != nil || seen[id] {
			continue
		}
		seen[id] = true
		contact, err := parseContact(resources[i].text[keyAddress], false)
		if err != nil {
			continue
		}
		contact.ID = id
		contact.ETag = etagOf(resources[i].text[keyETag])
		page.Contacts = append(page.Contacts, *contact)
	}
	sort.Slice(page.Contacts, func(i, j int) bool {
		a, b := page.Contacts[i], page.Contacts[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	if len(page.Contacts) > limit {
		page.Contacts, page.Truncated = page.Contacts[:limit], true
	}
	page.Count = len(page.Contacts)
	return page, nil
}

func (c *Client) getContact(ctx context.Context, book, id string) (*ContactResult, error) {
	const op = "read contact"
	home, err := c.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	path := append(append([]string{}, home...), book, id)
	data, header, err := c.request(ctx, op, methodGet, path, false, "", "", 200, maxEventBytes)
	if err != nil {
		return nil, err
	}
	contact, err := parseContact(string(data), true)
	if err != nil {
		return nil, invalidResponse(op, "Infomaniak returned a contact that is not a valid vCard")
	}
	contact.ID = id
	contact.ETag = etagOf(header.Get("ETag"))
	return &ContactResult{Addressbook: book, Contact: *contact}, nil
}

var errNoContact = errors.New("no contact")

// parseContact reads one vCard. With full unset, only the compact members are filled. A photo or other
// binary property is never read.
func parseContact(data string, full bool) (contact *Contact, err error) {
	defer func() {
		if recover() != nil {
			contact, err = nil, errNoContact
		}
	}()
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	contact = &Contact{
		UID:          clean(card.Value(vcard.FieldUID), maxContactText),
		Name:         clean(card.PreferredValue(vcard.FieldFormattedName), maxContactText),
		Organization: orgOf(card.Value(vcard.FieldOrganization)),
	}
	name := card.Name()
	if contact.Name == "" && name != nil {
		contact.Name = clean(strings.Join(nonEmpty(name.HonorificPrefix, name.GivenName, name.AdditionalName,
			name.FamilyName, name.HonorificSuffix), " "), maxContactText)
	}
	if !full {
		if field := card.Get(vcard.FieldEmail); field != nil {
			contact.Email = clean(field.Value, maxContactText)
		}
		return contact, nil
	}
	var cut []string
	if name != nil {
		contact.StructuredName = &ContactName{
			Family: clean(name.FamilyName, maxContactText), Given: clean(name.GivenName, maxContactText),
			Additional: clean(name.AdditionalName, maxContactText),
			Prefix:     clean(name.HonorificPrefix, maxContactText), Suffix: clean(name.HonorificSuffix, maxContactText),
		}
	}
	contact.Title = clean(card.Value(vcard.FieldTitle), maxContactText)
	contact.Birthday = clean(card.Value(vcard.FieldBirthday), maxContactBirthday)
	contact.Note = cleanText(card.Value(vcard.FieldNote), maxContactNote, true)
	if len(strings.TrimSpace(card.Value(vcard.FieldNote))) > maxContactNote {
		cut = append(cut, "note")
	}
	fields := card[vcard.FieldEmail]
	contact.Emails, cut = typedOf(fields, "emails", cut)
	contact.Phones, cut = typedOf(card[vcard.FieldTelephone], "phones", cut)
	addresses := card.Addresses()
	for i, address := range addresses {
		if i >= maxContactEntries {
			cut = append(cut, "addresses")
			break
		}
		contact.Addresses = append(contact.Addresses, PostalAddress{
			Type: typeOf(address.Field), POBox: clean(address.PostOfficeBox, maxContactText),
			Extended: clean(address.ExtendedAddress, maxContactText), Street: clean(address.StreetAddress, maxContactText),
			Locality: clean(address.Locality, maxContactText), Region: clean(address.Region, maxContactText),
			PostalCode: clean(address.PostalCode, maxContactText), Country: clean(address.Country, maxContactText),
		})
	}
	for i, field := range card[vcard.FieldURL] {
		if i >= maxContactEntries {
			cut = append(cut, "urls")
			break
		}
		contact.URLs = append(contact.URLs, clean(field.Value, maxContactText))
	}
	contact.Truncated = cut
	return contact, nil
}

func typedOf(fields []*vcard.Field, name string, cut []string) ([]TypedValue, []string) {
	var out []TypedValue
	for i, field := range fields {
		if i >= maxContactEntries {
			return out, append(cut, name)
		}
		out = append(out, TypedValue{Value: clean(field.Value, maxContactText), Type: typeOf(field)})
	}
	return out, cut
}

func typeOf(field *vcard.Field) string {
	if field == nil {
		return ""
	}
	return clean(strings.Join(field.Params.Types(), ","), maxContactType)
}

func orgOf(value string) string {
	return clean(strings.Join(nonEmpty(strings.Split(value, ";")...), ", "), maxContactText)
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
