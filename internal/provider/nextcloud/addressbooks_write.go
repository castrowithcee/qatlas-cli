package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Address book management: an extended MKCOL creates an address book, PROPPATCH renames or describes it, and
// DELETE removes it with all its contacts. Update and delete send exactly one request after one read-only
// pre-check that refuses a missing, foreign, or shared address book locally.

const (
	addressbookWriteOut = `"id":{"type":"string"}`

	addressbookUncertain = "; the change may have been applied, check the address book with contacts.address-books.list " +
		"before repeating it"
)

// addressbookOwnProbeBody reads what decides whether an address book is an address book of the identity.
const addressbookOwnProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
	`<d:resourcetype/><oc:owner-principal/>` +
	`</d:prop></d:propfind>`

var addressbooksCreate = capability.Descriptor{
	ID: Provider + ".contacts.address-books.create", Version: 1, Title: "Create a Nextcloud address book",
	Description: "Create one address book of the bound Nextcloud identity with a display name. Qatlas generates the " +
		"address book id; nothing raw comes from an argument. It needs the general addressbook target of the " +
		"connection and binds the new address book. One extended MKCOL follows without a pre-check; the request " +
		"is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "addressbook", "create"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarNameSchema + `},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,` +
		`"created":{"type":"boolean"}},"required":["id","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Display name, at most 128 characters, no control characters", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Generated id of the address book, the addressbook argument of the contact tools"},
		{Name: "created", Description: "True when Nextcloud created the address book"},
	},
	Examples: []capability.Example{{Description: "Create an address book", Arguments: json.RawMessage(`{"name":"Clients"}`)}},
}

var addressbooksUpdate = capability.Descriptor{
	ID: Provider + ".contacts.address-books.update", Version: 1, Title: "Change a Nextcloud address book",
	Description: "Change the display name and/or the description of one bound address book that belongs to the " +
		"identity; the system address book and an address book shared by someone else are refused. One read-only " +
		"pre-check of the address book follows, then one PROPPATCH; a partly refused change is reported. The " +
		"request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "addressbook", "update"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + calendarNameSchema + `,` +
		`"description":{"type":"string","minLength":1,"maxLength":1024}},` +
		`"required":["addressbook"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,` +
		`"updated":{"type":"boolean"}},"required":["id","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument,
		{Name: "name", Description: "New display name, at most 128 characters; name or description is required"},
		{Name: "description", Description: "New description, at most 1024 bytes; name or description is required"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Id of the address book"},
		{Name: "updated", Description: "True when Nextcloud stored the change"},
	},
	Examples: []capability.Example{{Description: "Rename an address book",
		Arguments: json.RawMessage(`{"addressbook":"clients","name":"Clients 2026"}`)}},
}

var addressbooksDelete = capability.Descriptor{
	ID: Provider + ".contacts.address-books.delete", Version: 1, Title: "Delete a Nextcloud address book",
	Description: "Delete one bound address book that belongs to the identity, with all its contacts; the system " +
		"address book and an address book shared by someone else are refused. Qatlas offers no restore. One " +
		"read-only pre-check of the address book follows, then one DELETE. The request is never repeated after " +
		"an unclear result",
	Tags: []string{"nextcloud", "contacts", "addressbook", "delete"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `},` +
		`"required":["addressbook"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,` +
		`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Id of the address book"},
		{Name: "deleted", Description: "True when Nextcloud deleted the address book"},
	},
	Examples: []capability.Example{{Description: "Delete an address book", Arguments: json.RawMessage(`{"addressbook":"clients"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// AddressbookWriteResult is the result of the address book create, update, and delete tools.
type AddressbookWriteResult struct {
	ID      string `json:"id"`
	Created bool   `json:"created,omitempty"`
	Updated bool   `json:"updated,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

type addressbookWriteArguments struct {
	Addressbook string `json:"addressbook"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// mkcolBody builds the extended MKCOL body (RFC 5689) of an address book from a checked name.
func mkcolBody(name string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:mkcol xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop>` +
		`<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>` +
		`<d:displayname>` + xmlText(name) + `</d:displayname>` +
		`</d:prop></d:set></d:mkcol>`
}

// addressbookPatchBody sets the display name and/or the description; an empty value is left out.
func addressbookPatchBody(name, description string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propertyupdate xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop>`)
	if name != "" {
		b.WriteString(`<d:displayname>` + xmlText(name) + `</d:displayname>`)
	}
	if description != "" {
		b.WriteString(`<card:addressbook-description>` + xmlText(description) + `</card:addressbook-description>`)
	}
	b.WriteString(`</d:prop></d:set></d:propertyupdate>`)
	return b.String()
}

// openOwnAddressbook is openAddressbook for the management tools: besides the binding it refuses the system
// address book, which is never writable, before any credential access or request.
func openOwnAddressbook(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
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
	if uri == systemAddressbook {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "the system address book is never writable, nothing was changed"}
	}
	return open(ctx, resolved, secrets, red, false)
}

func invokeAddressbooksCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create address book"
	var input addressbookWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Addressbook != "" || input.Description != "" {
		return nil, providerError(op, "creating an address book takes only a name; its id is generated")
	}
	bound, err := requireAddressbook(resolved)
	if err != nil {
		return nil, err
	}
	// An address book of a single-address-book binding could not be reached afterwards, so it is not created.
	if !bound.all {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "creating an address book needs the general addressbook target of the connection"}
	}
	name, err := checkCalendarName(op, input.Name)
	if err != nil {
		return nil, err
	}
	id, err := dav.RandomID()
	if err != nil || checkSegment(id) != nil {
		return nil, providerError(op, "an address book id could not be generated")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	status, _, err := client.collectionSend(ctx, op, methodMkcol, client.addressbookHome(id), mkcolBody(name),
		map[int]string{
			http.StatusMethodNotAllowed: "an address book with this id already exists, nothing was created",
			http.StatusConflict:         "Nextcloud cannot create an address book here, nothing was created",
		}, addressbookUncertain)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+addressbookUncertain)
	}
	return &AddressbookWriteResult{ID: id, Created: true}, nil
}

func invokeAddressbooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "change address book"
	var input addressbookWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Name == "" && input.Description == "" {
		return nil, providerError(op, "name or description is required")
	}
	var name, description string
	var err error
	if input.Name != "" {
		if name, err = checkCalendarName(op, input.Name); err != nil {
			return nil, err
		}
	}
	if input.Description != "" {
		var ok bool
		if description, ok = dav.ValidText(strings.TrimSpace(input.Description), maxAddressbookText, true); !ok || description == "" {
			return nil, providerError(op, "description must be plain text of at most 1024 bytes")
		}
	}
	client, err := openOwnAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	if err := client.ownAddressbook(ctx, op, input.Addressbook); err != nil {
		return nil, err
	}
	path := client.addressbookHome(input.Addressbook)
	status, data, err := client.collectionSend(ctx, op, methodProppatch, path, addressbookPatchBody(name, description),
		nil, addressbookUncertain)
	if err != nil {
		return nil, err
	}
	if status != http.StatusMultiStatus {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+addressbookUncertain)
	}
	if err := client.patchResult(op, addressbookUncertain, data, path, []patchProp{
		{name != "", dav.KeyName, "name"}, {description != "", dav.KeyDescription, "description"}}); err != nil {
		return nil, err
	}
	return &AddressbookWriteResult{ID: input.Addressbook, Updated: true}, nil
}

func invokeAddressbooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete address book"
	var input addressbookWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Name != "" || input.Description != "" {
		return nil, providerError(op, "deleting an address book takes only the addressbook argument")
	}
	client, err := openOwnAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	if err := client.ownAddressbook(ctx, op, input.Addressbook); err != nil {
		return nil, err
	}
	status, _, err := client.collectionSend(ctx, op, http.MethodDelete, client.addressbookHome(input.Addressbook), "",
		nil, addressbookUncertain)
	if err != nil {
		return nil, err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+addressbookUncertain)
	}
	return &AddressbookWriteResult{ID: input.Addressbook, Deleted: true}, nil
}

// ownAddressbook is the pre-check of update and delete: one PROPFIND of depth 0 on the bound address book.
// A node that is no address book, or whose owner is not reported as the identity itself, is refused without
// a change.
func (c *Client) ownAddressbook(ctx context.Context, op, book string) error {
	base := c.addressbookHome(book)
	data, err := c.contactRequest(ctx, op, methodPropfind, base, depthSelf, addressbookOwnProbeBody)
	if err != nil {
		return err
	}
	node, err := c.eventOnlyNode(op, data, base)
	if err != nil {
		return err
	}
	if !node.Addressbook {
		return invalidResponse(op, "Nextcloud did not answer with an address book")
	}
	return c.ownedByIdentity(op, node, "address book")
}
