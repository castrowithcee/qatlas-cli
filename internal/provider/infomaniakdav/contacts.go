package infomaniakdav

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the contact tools. Every provider string and every multi-valued property is capped; a photo or
// other binary property is never reported.
const (
	defaultContactLimit = 50
	maxContactLimit     = 200
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

// ContactsPage is the result of the list tool.
type ContactsPage struct {
	Addressbook string        `json:"addressbook"`
	Contacts    []dav.Contact `json:"contacts"`
	Count       int           `json:"count"`
	Truncated   bool          `json:"truncated,omitempty"`
}

// ContactResult is the result of the get tool.
type ContactResult struct {
	Addressbook string      `json:"addressbook"`
	Contact     dav.Contact `json:"contact"`
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
	resources, err := c.propfind(ctx, op, principal, "0", dav.AddressbookHomeBody)
	if err != nil {
		return nil, err
	}
	return singleLink(op, resources, dav.PropBookHome)
}

func (c *Client) listContacts(ctx context.Context, book string, limit int) (*ContactsPage, error) {
	const op = "list contacts"
	home, err := c.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	base := append(append([]string{}, home...), book)
	data, _, err := c.request(ctx, op, methodReport, base, true, "1", dav.AddressbookQueryBody, 207, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := server.ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &ContactsPage{Addressbook: book, Contacts: []dav.Contact{}}
	seen := map[string]bool{}
	for i := range resources {
		id, err := eventID(op, resources[i].Href, base)
		if err != nil {
			return nil, err
		}
		if failure(&resources[i], op) != nil || seen[id] {
			continue
		}
		seen[id] = true
		contact, err := dav.ParseContact(resources[i].Text[dav.KeyAddress], false)
		if err != nil {
			continue
		}
		contact.ID = id
		contact.ETag = dav.ETagOf(resources[i].Text[dav.KeyETag])
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
	contact, err := dav.ParseContact(string(data), true)
	if err != nil {
		return nil, invalidResponse(op, "Infomaniak returned a contact that is not a valid vCard")
	}
	contact.ID = id
	contact.ETag = dav.ETagOf(header.Get("ETag"))
	return &ContactResult{Addressbook: book, Contact: *contact}, nil
}
