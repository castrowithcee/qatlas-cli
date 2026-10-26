package nextcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Contact writes in a bound address book through CardDAV: a PUT with If-None-Match: * creates, a PUT with
// If-Match replaces, and a DELETE with If-Match removes one contact. Each sends exactly one mutating request
// after at most one read-only pre-check.

var contactKind = objectKind{"a", "contact", "", "; the change may have been applied, check the address book " +
	"with contacts.contacts.list or contacts.contacts.get before repeating it"}

// contactBookProbeBody reads what decides whether an address book takes a new contact.
const contactBookProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:current-user-privilege-set/></d:prop></d:propfind>`

const (
	contactTypeEnum   = `"type":{"type":"string","enum":["home","work","cell","voice","fax","other"]}`
	contactWriteTyped = `{"type":"object","properties":{"value":{"type":"string","minLength":1,"maxLength":256},` + contactTypeEnum + `},"required":["value"],"additionalProperties":false}`
	contactWriteName  = `{"type":"object","properties":{"family":{"type":"string","maxLength":256},` +
		`"given":{"type":"string","maxLength":256},"additional":{"type":"string","maxLength":256},` +
		`"prefix":{"type":"string","maxLength":256},"suffix":{"type":"string","maxLength":256}},` +
		`"additionalProperties":false}`
	contactWritePostal = `{"type":"object","properties":{` + contactTypeEnum + `,"po_box":{"type":"string","maxLength":256},` +
		`"extended":{"type":"string","maxLength":256},"street":{"type":"string","maxLength":256},` +
		`"locality":{"type":"string","maxLength":256},"region":{"type":"string","maxLength":256},` +
		`"postal_code":{"type":"string","maxLength":256},"country":{"type":"string","maxLength":256}},` +
		`"additionalProperties":false}`
	contactFieldSchemas = `"name":{"type":"string","minLength":1,"maxLength":256},"structured_name":` + contactWriteName + `,` +
		`"emails":{"type":"array","maxItems":20,"items":` + contactWriteTyped + `},` +
		`"phones":{"type":"array","maxItems":20,"items":` + contactWriteTyped + `},` +
		`"addresses":{"type":"array","maxItems":20,"items":` + contactWritePostal + `},` +
		`"organization":{"type":"string","maxLength":256},"title":{"type":"string","maxLength":256},` +
		`"birthday":{"type":"string","minLength":10,"maxLength":10},"note":{"type":"string","maxLength":4096},` +
		`"urls":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":256}}`
	contactIDArgSchema   = `"id":{"type":"string","minLength":1,"maxLength":255}`
	contactETagArgSchema = `"etag":{"type":"string","minLength":1,"maxLength":258}`
	contactWriteOut      = `"addressbook":{"type":"string"},"id":{"type":"string"}`
)

// contactWriteRisk is open-world like every provider call; the cards hold personal data.
func contactWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: contactsSensitivity}
}

var contactFieldArguments = []capability.Argument{
	{Name: "name", Description: "Formatted name (FN)", Required: true},
	{Name: "structured_name", Description: "Name parts {family, given, additional, prefix, suffix}; no semicolons"},
	{Name: "emails", Description: "At most 20 {value, type} with a plain e-mail address; type is home, work, cell, voice, fax, or other"},
	{Name: "phones", Description: "At most 20 {value, type}; digits with + ( ) . / - and spaces, at most 32 bytes"},
	{Name: "addresses", Description: "At most 20 postal addresses {type, po_box, extended, street, locality, region, postal_code, country}"},
	{Name: "organization", Description: "Organization; no semicolons"},
	{Name: "title", Description: "Job title"},
	{Name: "birthday", Description: "Birthday as a date YYYY-MM-DD"},
	{Name: "note", Description: "Note of at most 4096 bytes; line breaks are kept"},
	{Name: "urls", Description: "At most 20 http or https URLs without credentials; never followed"},
}

var (
	contactIDArgument = capability.Argument{Name: "id", Required: true,
		Description: "Contact id as contacts.contacts.list or contacts.contacts.get reports it: one path segment of the contact resource"}
	contactETagArgument = capability.Argument{Name: "etag", Required: true,
		Description: "Entity tag of the contact version to change, as contacts.contacts.get or contacts.contacts.list reports it"}
)

var contactsCreate = capability.Descriptor{
	ID: Provider + ".contacts.contacts.create", Version: 1, Title: "Create a Nextcloud contact",
	Description: "Create one new contact in a bound, writable address book from structured fields. Qatlas builds " +
		"the vCard 3.0 itself and generates the UID and the resource id; nothing raw comes from an argument. One " +
		"read-only pre-check of the address book refuses a read-only one, then one PUT with If-None-Match: * " +
		"stores the contact, so no existing contact can be replaced. The system address book is never writable. " +
		"The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "contact", "create"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + contactFieldSchemas +
		`},"required":["addressbook","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + contactWriteOut + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"created":{"type":"boolean"}},"required":["addressbook","id","uid","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{addressbookArgument}, contactFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Generated resource id, the id argument of the other contact tools"},
		{Name: "uid", Description: "Generated vCard UID"},
		{Name: "etag", Description: "Entity tag of the new contact when Nextcloud reports one"},
		{Name: "created", Description: "True when Nextcloud stored the contact"},
	},
	Examples: []capability.Example{{Description: "Create a contact with one e-mail address",
		Arguments: json.RawMessage(`{"addressbook":"contacts","name":"Ada Example","emails":[{"value":"ada@example.org","type":"work"}]}`)}},
}

var contactsUpdate = capability.Descriptor{
	ID: Provider + ".contacts.contacts.update", Version: 1, Title: "Replace a Nextcloud contact",
	Description: "Replace one contact of a bound, writable address book completely by structured fields, bound to " +
		"its etag. One read-only GET of the contact keeps its UID and refuses a changed etag and a contact that " +
		"holds a photo, a group card, or any other property Qatlas does not model; it is left unchanged. Then one " +
		"PUT with If-Match follows. The system address book is never writable. The request is never repeated " +
		"after an unclear result",
	Tags: []string{"nextcloud", "contacts", "contact", "update"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + contactIDArgSchema + `,` +
		contactETagArgSchema + `,` + contactFieldSchemas +
		`},"required":["addressbook","id","etag","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + contactWriteOut + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"updated":{"type":"boolean"}},"required":["addressbook","id","uid","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{addressbookArgument, contactIDArgument, contactETagArgument},
		contactFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Contact id"},
		{Name: "uid", Description: "vCard UID of the contact, kept"},
		{Name: "etag", Description: "Entity tag of the new version when Nextcloud reports one"},
		{Name: "updated", Description: "True when Nextcloud stored the new version"},
	},
	Examples: []capability.Example{{Description: "Rename a contact",
		Arguments: json.RawMessage(`{"addressbook":"contacts","id":"ada.vcf","etag":"e1","name":"Ada Example"}`)}},
}

var contactsDelete = capability.Descriptor{
	ID: Provider + ".contacts.contacts.delete", Version: 1, Title: "Delete a Nextcloud contact",
	Description: "Delete one contact of a bound, writable address book, bound to its etag. One read-only GET " +
		"checks that the contact is a vCard with this etag, then one DELETE with If-Match follows; a changed etag " +
		"deletes nothing. Nextcloud keeps no restore for it through Qatlas. The system address book is never " +
		"writable. The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "contact", "delete"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + contactIDArgSchema + `,` +
		contactETagArgSchema + `},"required":["addressbook","id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + contactWriteOut + `,"deleted":{"type":"boolean"}},` +
		`"required":["addressbook","id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument, contactIDArgument, contactETagArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Contact id"},
		{Name: "deleted", Description: "True when Nextcloud deleted the contact"},
	},
	Examples: []capability.Example{{Description: "Delete a contact",
		Arguments: json.RawMessage(`{"addressbook":"contacts","id":"ada.vcf","etag":"e1"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// ContactWriteResult is the result of the contact create, update, and delete tools.
type ContactWriteResult struct {
	Addressbook string `json:"addressbook"`
	ID          string `json:"id"`
	UID         string `json:"uid,omitempty"`
	ETag        string `json:"etag,omitempty"`
	Created     bool   `json:"created,omitempty"`
	Updated     bool   `json:"updated,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
}

type contactWriteArguments struct {
	Addressbook string `json:"addressbook"`
	ID          string `json:"id"`
	ETag        string `json:"etag"`
	dav.ContactInput
}

type contactDeleteArguments struct {
	Addressbook string `json:"addressbook"`
	ID          string `json:"id"`
	ETag        string `json:"etag"`
}

func contactReadOnly(op string) error {
	return &provider.Error{Class: provider.ClassPermission, Op: op,
		Message: "this address book is read-only for this Nextcloud identity, nothing was changed"}
}

// contactTarget refuses the system address book and an unusable contact id or entity tag before any secret or
// request. It returns the normalized entity tag of an existing contact.
func contactTarget(op, book, id, etag string, existing bool) (string, error) {
	if book == systemAddressbook {
		return "", &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "the system address book is read-only, nothing was changed"}
	}
	if !existing {
		return "", nil
	}
	if !contactChildID(id) {
		return "", providerError(op, "the contact id must be one literal path segment as the contacts list reports it")
	}
	normal, ok := dav.NormalETag(etag)
	if !ok {
		return "", providerError(op, "etag must be the entity tag of the contact as the read tools report it")
	}
	return normal, nil
}

func invokeContactsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create contact"
	var input contactWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" {
		return nil, providerError(op, "the id of a new contact is generated and takes no etag")
	}
	if _, err := contactTarget(op, input.Addressbook, "", "", false); err != nil {
		return nil, err
	}
	card, err := dav.NewContactCard(op, input.ContactInput)
	if err != nil {
		return nil, err
	}
	uid, err := dav.RandomID()
	if err != nil {
		return nil, providerError(op, "a contact id could not be generated")
	}
	body, err := dav.EncodeContact(op, card, uid)
	if err != nil {
		return nil, err
	}
	client, err := openAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	if err := client.addressbookWritable(ctx, op, input.Addressbook); err != nil {
		return nil, err
	}
	id := uid + ".vcf"
	header, err := client.contactSend(ctx, op, http.MethodPut, input.Addressbook, id, "", body)
	if err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: id, UID: uid, ETag: dav.ETagOf(header.Get("ETag")),
		Created: true}, nil
}

func invokeContactsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update contact"
	var input contactWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	etag, err := contactTarget(op, input.Addressbook, input.ID, input.ETag, true)
	if err != nil {
		return nil, err
	}
	card, err := dav.NewContactCard(op, input.ContactInput)
	if err != nil {
		return nil, err
	}
	client, err := openAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	data, err := client.contactCurrent(ctx, op, input.Addressbook, input.ID, etag)
	if err != nil {
		return nil, err
	}
	uid, err := client.contactServer().StoredContactUID(op, data)
	if err != nil {
		return nil, err
	}
	body, err := dav.EncodeContact(op, card, uid)
	if err != nil {
		return nil, err
	}
	header, err := client.contactSend(ctx, op, http.MethodPut, input.Addressbook, input.ID, etag, body)
	if err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: input.ID, UID: uid,
		ETag: dav.ETagOf(header.Get("ETag")), Updated: true}, nil
}

func invokeContactsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete contact"
	var input contactDeleteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	etag, err := contactTarget(op, input.Addressbook, input.ID, input.ETag, true)
	if err != nil {
		return nil, err
	}
	client, err := openAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	data, err := client.contactCurrent(ctx, op, input.Addressbook, input.ID, etag)
	if err != nil {
		return nil, err
	}
	if _, err := dav.ParseContact(string(data), false); err != nil {
		return nil, invalidResponse(op, "Nextcloud returned a contact that is not a valid vCard")
	}
	if _, err := client.contactSend(ctx, op, http.MethodDelete, input.Addressbook, input.ID, etag, ""); err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: input.ID, Deleted: true}, nil
}

// addressbookWritable is the pre-check of a create: one PROPFIND of depth 0 on the bound address book. A node
// that is no address book or is read-only for the identity is refused without a change.
func (c *Client) addressbookWritable(ctx context.Context, op, book string) error {
	base := c.addressbookHome(book)
	data, err := c.contactRequest(ctx, op, methodPropfind, base, depthSelf, contactBookProbeBody)
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
	if !writable(node.Privileges) {
		return contactReadOnly(op)
	}
	return nil
}

// contactCurrent is the pre-check of update and delete: one GET of the contact. It refuses a changed entity tag
// and returns the stored card.
func (c *Client) contactCurrent(ctx context.Context, op, book, id, etag string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+escapePath(c.addressbookHome(book, id)), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "text/vcard")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil, invalidResponse(op, "Nextcloud answered with an unexpected success status")
		}
		return nil, davStatusError(op, response)
	}
	if served := response.Header.Get("ETag"); served != "" && dav.ETagOf(served) != etag {
		return nil, objectPrecondition(op, contactKind)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, dav.MaxEventBytes+1))
	if err != nil || len(data) > dav.MaxEventBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return data, nil
}

// contactSend is the single write path of contacts. It sends exactly one PUT or DELETE to the contact and
// never repeats it. Without a match the request carries If-None-Match: *, otherwise If-Match with the validated
// entity tag. A failure that leaves the outcome open carries the uncertainty hint; a refusal or a redirect is
// clear.
func (c *Client) contactSend(ctx context.Context, op, method, book, id, match, body string) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.origin+escapePath(c.addressbookHome(book, id)),
		strings.NewReader(body))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
	}
	if match == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", `"`+match+`"`)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, sentTransportError(op, err, contactKind.uncertain)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusPreconditionFailed && match == "":
		return nil, providerError(op, "a contact with this id already exists, nothing was written")
	case status == http.StatusPreconditionFailed:
		return nil, objectPrecondition(op, contactKind)
	case status == http.StatusForbidden:
		return nil, contactReadOnly(op)
	case status < 200 || status >= 300:
		return nil, sentDAVStatusError(op, response, contactKind.uncertain)
	case status == http.StatusNoContent, status == http.StatusOK, status == http.StatusCreated && method == http.MethodPut:
		return response.Header, nil
	}
	return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+contactKind.uncertain)
}
