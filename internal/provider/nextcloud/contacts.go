package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// groupContacts is the tool group of the address book and contact tools.
const groupContacts = "contacts"

// contactsSensitivity classifies address book results: names, addresses, and numbers are personal data.
const contactsSensitivity = "nextcloud-contacts"

// systemAddressbook is the URI of the address book Nextcloud generates from its user directory. It is
// only reachable through an explicit addressbook/URI target and is always read-only.
const systemAddressbook = "z-server-generated--system"

// addressbooksRoot are the fixed path segments of the address book home; the user ID follows them.
var addressbooksRoot = []string{"remote.php", "dav", "addressbooks", "users"}

// Bounds of the address book and contact reads.
const (
	maxAddressbooks          = 200
	maxAddressbookName       = 256
	maxAddressbookText       = 1024
	defaultContactLimit      = 50
	maxContactLimit          = 200
	maxContactIDLength       = 255
	maxContactQuery          = 128
	messageAddressbookNone   = "this connection is not bound to this address book"
	messageAddressbookNoBook = "this connection is not bound to address books"
)

const addressbooksBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:carddav" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
	`<d:displayname/><d:resourcetype/><c:addressbook-description/><c:supported-address-data/>` +
	`<c:max-resource-size/><c:supported-collation-set/><d:current-user-privilege-set/><d:sync-token/>` +
	`<oc:invite/></d:prop></d:propfind>`

// contactGetProps names the card properties a read asks the server for. PHOTO, LOGO,
// SOUND, KEY, and AGENT are never requested: CardDAV cannot filter by VALUE=uri, so the server would send
// embedded data. Their references are therefore not reported.
const (
	contactGetProps = `<card:prop name="VERSION"/><card:prop name="UID"/><card:prop name="FN"/>` +
		`<card:prop name="N"/><card:prop name="ORG"/><card:prop name="EMAIL"/><card:prop name="TEL"/><card:prop name="ADR"/>` +
		`<card:prop name="TITLE"/><card:prop name="BDAY"/><card:prop name="NOTE"/><card:prop name="URL"/>` +
		`<card:prop name="NICKNAME"/><card:prop name="MAILER"/><card:prop name="TZ"/><card:prop name="GEO"/>` +
		`<card:prop name="ROLE"/><card:prop name="LABEL"/><card:prop name="CATEGORIES"/><card:prop name="CLASS"/>` +
		`<card:prop name="SORT-STRING"/><card:prop name="REV"/><card:prop name="PRODID"/><card:prop name="NAME"/>` +
		`<card:prop name="PROFILE"/><card:prop name="SOURCE"/>`
	contactBodyHead   = `<?xml version="1.0" encoding="UTF-8"?>`
	contactNamespaces = ` xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`
	contactProp       = `<d:prop><d:getetag/><card:address-data>`
	contactPropEnd    = `</card:address-data></d:prop>`
)

// contactMatch is one text-match of the search; the escaped search text is its only variable part.
const contactMatch = `<card:prop-filter name="%s"><card:text-match collation="i;unicode-casemap" ` +
	`match-type="contains">%s</card:text-match></card:prop-filter>`

var contactRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: contactsSensitivity,
}

const (
	contactTypedSchema = `{"type":"object","properties":{"value":{"type":"string"},"type":{"type":"string"},` +
		`"pref":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`
	contactNameSchema = `{"type":"object","properties":{"family":{"type":"string"},"given":{"type":"string"},` +
		`"additional":{"type":"string"},"prefix":{"type":"string"},"suffix":{"type":"string"}},` +
		`"additionalProperties":false}`
	contactPostalSchema = `{"type":"object","properties":{"type":{"type":"string"},"po_box":{"type":"string"},` +
		`"extended":{"type":"string"},"street":{"type":"string"},"locality":{"type":"string"},` +
		`"region":{"type":"string"},"postal_code":{"type":"string"},"country":{"type":"string"},` +
		`"pref":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`
	contactSchema = `{"type":"object","properties":{` +
		`"id":{"type":"string"},"uid":{"type":"string"},"name":{"type":"string"},"structured_name":` + contactNameSchema + `,` +
		`"email":{"type":"string"},"emails":{"type":"array","items":` + contactTypedSchema + `},` +
		`"phones":{"type":"array","items":` + contactTypedSchema + `},` +
		`"addresses":{"type":"array","items":` + contactPostalSchema + `},` +
		`"organization":{"type":"string"},"title":{"type":"string"},"birthday":{"type":"string"},` +
		`"note":{"type":"string"},"urls":{"type":"array","items":{"type":"string"}},` +
		`"truncated":{"type":"array","items":{"type":"string"}},"etag":{"type":"string"},` +
		dav.ContactValueProperties + `},"required":["id"],"additionalProperties":false}`
	addressbookSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"read_only":{"type":"boolean"},` +
		`"shares":{"type":"array","items":` + davShareSchema + `},"shares_truncated":{"type":"boolean"},` +
		dav.AddressbookPropertiesSchema + `},` +
		`"required":["id","name"],"additionalProperties":false}`
	addressbookArgSchema = `"addressbook":{"type":"string","minLength":1,"maxLength":255}`
)

var addressbookArgument = capability.Argument{
	Name: "addressbook", Required: true,
	Description: "URI of a bound address book, the id the addressbooks list reports",
}

var contactFields = append([]capability.Field{
	{Name: "id", Description: "Last path segment of the contact resource, the id argument of the get tool"},
	{Name: "uid", Description: "vCard UID, untrusted data"},
	{Name: "name", Description: "Formatted name (FN), untrusted personal data; built from the structured name if absent"},
	{Name: "email", Description: "First e-mail address, untrusted personal data; the list tool adds it to the full set"},
	{Name: "organization", Description: "Organization, untrusted data"},
	{Name: "structured_name", Description: "Family, given, additional name, prefix, suffix"},
	{Name: "emails", Description: "At most 20 e-mail addresses with type and the PREF parameter as pref"},
	{Name: "phones", Description: "At most 20 phone numbers with type and the PREF parameter as pref"},
	{Name: "addresses", Description: "At most 20 postal addresses with type and the PREF parameter as pref"},
	{Name: "title", Description: "Job title"},
	{Name: "birthday", Description: "Birthday as the card states it"},
	{Name: "note", Description: "Note, untrusted data"},
	{Name: "urls", Description: "At most 20 URLs, untrusted data, never followed"},
	{Name: "truncated", Description: "Names of the members that were cut at their cap"},
	{Name: "etag", Description: "Entity tag of the current version of the contact"},
	{Name: "count", Description: "Number of reported contacts"},
}, dav.ContactValueFields...)

var addressbooksList = capability.Descriptor{
	ID: Provider + ".contacts.address-books.list", Version: 1, Title: "List Nextcloud address books",
	Description: "List the address books of the bound Nextcloud identity that the connection's addressbook " +
		"targets bind, shared address books included; the system address book is listed only when an " +
		"addressbook target names it, and then read-only; address books only, no contacts",
	Tags:        []string{"nextcloud", "contacts", "addressbook", "list"},
	Risk:        contactRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"addressbooks":{"type":"array","items":` +
		addressbookSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["addressbooks","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{},
	Fields: append([]capability.Field{
		{Name: "id", Description: "URI of the address book, the form of the addressbook/URI target"},
		{Name: "name", Description: "Display name, untrusted data"},
		{Name: "description", Description: "Description, untrusted data"},
		{Name: "read_only", Description: "True for the system address book, which is never writable"},
		{Name: "shares", Description: "At most 50 users and groups the address book is shared with, untrusted data; " +
			"other recipients are typed other without id or name"},
		{Name: "shares_truncated", Description: "True when the address book has more shares than reported"},
	}, append(append([]capability.Field{}, dav.AddressbookPropertyFields...),
		capability.Field{Name: "count", Description: "Number of reported address books"},
		capability.Field{Name: "truncated", Description: "True when more address books match than one listing reports"})...),
	Examples: []capability.Example{{Description: "List the bound address books", Arguments: json.RawMessage(`{}`)}},
}

var contactsList = capability.Descriptor{
	ID: Provider + ".contacts.contacts.list", Version: 1, Title: "List Nextcloud contacts",
	Description: "List the contacts of one bound address book, ordered by name, optionally only those whose " +
		"formatted name or e-mail address contains a text, and optionally narrowed by structured property " +
		"filters; the result is limited and carries the fields of the get tool, and a card " +
		"that is not a valid vCard is left out; photos and other binary data are never returned",
	Tags: []string{"nextcloud", "contacts", "contact", "list", "search"}, Risk: contactRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` +
		`"query":{"type":"string","minLength":1,"maxLength":128},` +
		`"limit":{"type":"integer","minimum":1,"maximum":200},` + dav.ContactFilterSchema() + `},` +
		`"required":["addressbook"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"addressbook":{"type":"string"},` +
		`"contacts":{"type":"array","items":` + contactSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["addressbook","contacts","count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		addressbookArgument,
		{Name: "query", Description: "Text the formatted name or an e-mail address contains, case-insensitive"},
		{Name: "limit", Description: "Maximum number of contacts, 50 by default and at most 200"},
		{Name: "property_filters", Description: "At most 8 conditions on contact properties; each names a property and " +
			"exactly one of text (1 to 256 bytes, optionally match_type, negate, and collation) or defined false, and " +
			"optionally a parameter with the same condition form; a query narrows the answer further"},
		{Name: "test", Description: "anyof (default) or allof: whether any or all of the property_filters must match"},
	},
	Fields: contactFields,
	Examples: []capability.Example{{
		Description: "Find contacts by name or e-mail address",
		Arguments:   json.RawMessage(`{"addressbook":"contacts","query":"ada"}`),
	}},
}

var contactsGet = capability.Descriptor{
	ID: Provider + ".contacts.contacts.get", Version: 1, Title: "Get a Nextcloud contact",
	Description: "Read one contact of a bound address book with its structured fields and entity tag; a " +
		"embedded binary data and embedded keys are never returned",
	Tags: []string{"nextcloud", "contacts", "contact", "get"}, Risk: contactRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":255}},"required":["addressbook","id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"addressbook":{"type":"string"},` +
		`"contact":` + contactSchema + `},"required":["addressbook","contact"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		addressbookArgument,
		{Name: "id", Description: "Contact id as the contacts list reports it: one path segment of the contact resource", Required: true},
	},
	Fields: contactFields,
	Examples: []capability.Example{{
		Description: "Read one contact the list tool reported",
		Arguments:   json.RawMessage(`{"addressbook":"contacts","id":"ada.vcf"}`),
	}},
}

// Addressbook is one bound address book.
type Addressbook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`

	Shares          []CollectionShare `json:"shares,omitempty"`
	SharesTruncated bool              `json:"shares_truncated,omitempty"`
	dav.CollectionProperties
}

// AddressbooksResult is the result of the address book list.
type AddressbooksResult struct {
	Addressbooks []Addressbook `json:"addressbooks"`
	Count        int           `json:"count"`
	Truncated    bool          `json:"truncated,omitempty"`
}

// ContactsPage is the result of the contact list.
type ContactsPage struct {
	Addressbook string        `json:"addressbook"`
	Contacts    []dav.Contact `json:"contacts"`
	Count       int           `json:"count"`
	Truncated   bool          `json:"truncated,omitempty"`
}

// ContactResult is the result of the contact get.
type ContactResult struct {
	Addressbook string      `json:"addressbook"`
	Contact     dav.Contact `json:"contact"`
}

type contactArguments struct {
	Addressbook string `json:"addressbook"`
	Query       string `json:"query"`
	Limit       int    `json:"limit"`
	ID          string `json:"id"`

	PropertyFilters []dav.ContactPropertyFilter `json:"property_filters"`
	Test            string                      `json:"test"`
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

// holdsAddressbook reports whether the selection covers an address book. The system address book is
// covered only by an explicit ID, never by the kind alone.
func (s selection) holdsAddressbook(uri string) bool {
	if uri == systemAddressbook {
		return selection{ids: s.ids}.holds(uri)
	}
	return s.holds(uri)
}

// requireAddressbook refuses a connection without an address book target locally, before any credential
// access or request, and names no other target.
func requireAddressbook(resolved *config.Resolved) (selection, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return selection{}, err
	}
	if !s.addressbooks.bound() {
		return selection{}, &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageAddressbookNoBook}
	}
	return s.addressbooks.excluding(s.excludes.addressbooks, false), nil
}

// addressbookBound wraps a handler so a connection without an address book target refuses before the
// handler reads any argument, credential, or request.
func addressbookBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if _, err := requireAddressbook(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

// contactChildID checks one path segment the contact tools address or report.
func contactChildID(id string) bool {
	return len(id) <= maxContactIDLength && checkSegment(id) == nil
}

// openAddressbook refuses an address book the connection does not bind before any credential access or
// request, then opens the client. The refusal does not name the address book.
func openAddressbook(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, uri string) (*Client, error) {
	bound, err := requireAddressbook(resolved)
	if err != nil {
		return nil, err
	}
	if !contactChildID(uri) {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageAddressbookNone}
	}
	// An excluded address book answers like one that does not exist.
	if bound.hides(uri) {
		return nil, hiddenError(op)
	}
	if !bound.holdsAddressbook(uri) {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageAddressbookNone}
	}
	return open(ctx, resolved, secrets, red, false)
}

func invokeAddressbooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	bound, err := requireAddressbook(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.listAddressbooks(ctx, bound)
}

func invokeContactsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list contacts"
	input, err := decodeContactArguments(op, raw)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultContactLimit
	}
	if limit < 0 || limit > maxContactLimit {
		return nil, providerError(op, "limit must be between 1 and 200")
	}
	query := ""
	if input.Query != "" {
		var ok bool
		if query, ok = dav.ValidText(strings.TrimSpace(input.Query), maxContactQuery, false); !ok || query == "" {
			return nil, providerError(op, "query must be plain text of at most 128 bytes")
		}
	}
	filter := dav.ContactFilter{Properties: input.PropertyFilters, Test: input.Test}
	body, err := contactListBody(op, query, filter)
	if err != nil {
		return nil, err
	}
	client, err := openAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	return client.listContacts(ctx, input.Addressbook, body, query, filter, limit)
}

func invokeContactsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read contact"
	input, err := decodeContactArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if input.PropertyFilters != nil || input.Test != "" {
		return nil, providerError(op, "the get tool takes no filter arguments")
	}
	if !contactChildID(input.ID) {
		return nil, providerError(op, "the contact id must be one literal path segment as the contacts list reports it")
	}
	client, err := openAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	return client.getContact(ctx, input.Addressbook, input.ID)
}

// contactServer names the one origin a DAV answer may point to.
func (c *Client) contactServer() dav.Server { return dav.Server{Name: "Nextcloud", Origin: c.origin} }

// addressbookHome are the segments of the address book home of the identity, followed by rel.
func (c *Client) addressbookHome(rel ...string) []string {
	segments := append(append([]string{}, c.install...), addressbooksRoot...)
	return append(append(segments, c.user), rel...)
}

// contactRequest sends one of the fixed address book requests to a path built from checked segments and
// returns the answer when it has the expected status. A collection is addressed with a trailing slash.
func (c *Client) contactRequest(ctx context.Context, op, method string, segments []string, depth, body string) ([]byte, error) {
	target := c.origin + escapePath(segments) + "/"
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("Depth", depth)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMultiStatus {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil, invalidResponse(op, "Nextcloud answered with an unexpected success status")
		}
		return nil, davStatusError(op, response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, dav.MaxResponseBytes+1))
	if err != nil || len(data) > dav.MaxResponseBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return data, nil
}

// contactChildOf returns the last segment of an href that names a direct child of base. Every other
// location, whatever the reason, yields false: a foreign host, a deeper or shallower path, or an unusable form.
func (c *Client) contactChildOf(op, href string, base []string) (string, bool) {
	segments, err := c.contactServer().Segments(op, href)
	if err != nil || len(segments) != len(base)+1 || !equalSegments(segments[:len(base)], base) {
		return "", false
	}
	return segments[len(base)], true
}

// escapeXMLText escapes a text for use as XML character data.
func escapeXMLText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// contactQueryBody asks for the cards of an address book, optionally only those whose FN or EMAIL contains
// the text. The text is escaped; nothing else in the body varies.
func contactQueryBody(query string) string {
	filter := ""
	if query != "" {
		text := escapeXMLText(query)
		filter = `<card:filter test="anyof">` + fmt.Sprintf(contactMatch, "FN", text) +
			fmt.Sprintf(contactMatch, "EMAIL", text) + `</card:filter>`
	}
	return contactBodyHead + `<card:addressbook-query` + contactNamespaces + contactProp + contactGetProps +
		contactPropEnd + filter + `</card:addressbook-query>`
}

// contactListBody builds the list request. Without structured filters it is the query body. With them, the body
// comes from the shared filter builder and only its address-data is narrowed to the get properties; an
// addressbook-query holds a single filter, so a query text cannot join it and is applied to the answer instead.
func contactListBody(op, query string, filter dav.ContactFilter) (string, error) {
	if filter.Zero() {
		return contactQueryBody(query), nil
	}
	body, err := dav.ContactQueryBody(op, filter)
	if err != nil {
		return "", err
	}
	return strings.Replace(body, `<card:address-data/>`,
		`<card:address-data>`+contactGetProps+`</card:address-data>`, 1), nil
}

// containsFold reports whether the contact's name or one of its e-mail addresses contains the lower-cased query.
func containsFold(contact *dav.Contact, query string) bool {
	if strings.Contains(strings.ToLower(contact.Name), query) || strings.Contains(strings.ToLower(contact.Email), query) {
		return true
	}
	for _, email := range contact.Emails {
		if strings.Contains(strings.ToLower(email.Value), query) {
			return true
		}
	}
	return false
}

// contactMultigetBody asks for one card by its path.
func contactMultigetBody(path string) string {
	return contactBodyHead + `<card:addressbook-multiget` + contactNamespaces + contactProp + contactGetProps +
		contactPropEnd + `<d:href>` + escapeXMLText(path) + `</d:href></card:addressbook-multiget>`
}

// listAddressbooks reads the children of the address book home with one PROPFIND of depth 1 and keeps the
// bound address books that are direct children of it.
func (c *Client) listAddressbooks(ctx context.Context, bound selection) (*AddressbooksResult, error) {
	const op = "list address books"
	home := c.addressbookHome()
	data, err := c.contactRequest(ctx, op, methodPropfind, home, depthChildren, addressbooksBody)
	if err != nil {
		return nil, err
	}
	resources, err := c.contactServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	result := &AddressbooksResult{Addressbooks: []Addressbook{}}
	seen := map[string]bool{}
	for i := range resources {
		uri, ok := c.contactChildOf(op, resources[i].Href, home)
		if !ok || !resources[i].Read || !resources[i].Addressbook || !bound.holdsAddressbook(uri) || seen[uri] {
			continue
		}
		seen[uri] = true
		book := Addressbook{
			ID:          uri,
			Name:        dav.Clean(resources[i].Text[dav.KeyName], maxAddressbookName),
			Description: dav.Clean(resources[i].Text[dav.KeyDescription], maxAddressbookText),
			ReadOnly:    uri == systemAddressbook,

			CollectionProperties: dav.CollectionPropertiesOf(&resources[i], false),
		}
		book.Shares, book.SharesTruncated = davSharesOf(resources[i].Invites, resources[i].InvitesTruncated)
		result.Addressbooks = append(result.Addressbooks, book)
	}
	sort.Slice(result.Addressbooks, func(i, j int) bool { return result.Addressbooks[i].ID < result.Addressbooks[j].ID })
	if len(result.Addressbooks) > maxAddressbooks {
		result.Addressbooks, result.Truncated = result.Addressbooks[:maxAddressbooks], true
	}
	result.Count = len(result.Addressbooks)
	return result, nil
}

func (c *Client) listContacts(ctx context.Context, book, body, query string, filter dav.ContactFilter, limit int) (*ContactsPage, error) {
	const op = "list contacts"
	base := c.addressbookHome(book)
	data, err := c.contactRequest(ctx, op, "REPORT", base, depthChildren, body)
	if err != nil {
		var failure *provider.Error
		// A server answers an unsupported collation with 403; report it as a request error, not a denial.
		if filter.UsesCollation() && errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			return nil, providerErrorFrom(op,
				"Nextcloud rejected the filter, possibly because it does not support a collation", err)
		}
		return nil, err
	}
	query = strings.ToLower(query)
	resources, err := c.contactServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &ContactsPage{Addressbook: book, Contacts: []dav.Contact{}}
	seen := map[string]bool{}
	for i := range resources {
		id, ok := c.contactChildOf(op, resources[i].Href, base)
		if !ok || !contactChildID(id) || !resources[i].Read || seen[id] {
			continue
		}
		seen[id] = true
		contact, err := dav.ParseContact(resources[i].Text[dav.KeyAddress], true)
		if err != nil {
			continue
		}
		if len(contact.Emails) > 0 {
			contact.Email = contact.Emails[0].Value
		}
		// A server may send these anyway; the output schema has no place for them.
		contact.PhotoURI, contact.LogoURI, contact.SoundURI, contact.AgentURI, contact.KeyURI = "", "", "", "", ""
		if !filter.Zero() && query != "" && !containsFold(contact, query) {
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
	base := c.addressbookHome(book)
	data, err := c.contactRequest(ctx, op, "REPORT", base, depthChildren,
		contactMultigetBody(escapePath(append(append([]string{}, base...), id))))
	if err != nil {
		return nil, err
	}
	resources, err := c.contactServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	for i := range resources {
		got, ok := c.contactChildOf(op, resources[i].Href, base)
		if !ok || got != id || !resources[i].Read {
			continue
		}
		contact, err := dav.ParseContact(resources[i].Text[dav.KeyAddress], true)
		if err != nil {
			return nil, invalidResponse(op, "Nextcloud returned a contact that is not a valid vCard")
		}
		// A server may send these anyway; the output schema has no place for them.
		contact.PhotoURI, contact.LogoURI, contact.SoundURI, contact.AgentURI, contact.KeyURI = "", "", "", "", ""
		contact.ID = id
		contact.ETag = dav.ETagOf(resources[i].Text[dav.KeyETag])
		return &ContactResult{Addressbook: book, Contact: *contact}, nil
	}
	return nil, statusError(op, http.StatusNotFound)
}
