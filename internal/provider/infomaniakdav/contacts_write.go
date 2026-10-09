package infomaniakdav

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const contactIDKind = "contact"

var contactKind = writeKind{contactIDKind, "address book", "text/vcard; charset=utf-8"}

const (
	typeEnum         = `"type":{"type":"string","enum":["home","work","cell","voice","fax","other"]}`
	writeTypedSchema = `{"type":"object","properties":{"value":{"type":"string","minLength":1,"maxLength":256},` +
		typeEnum + `},"required":["value"],"additionalProperties":false}`
	writeNameSchema = `{"type":"object","properties":{"family":{"type":"string","maxLength":256},` +
		`"given":{"type":"string","maxLength":256},"additional":{"type":"string","maxLength":256},` +
		`"prefix":{"type":"string","maxLength":256},"suffix":{"type":"string","maxLength":256}},` +
		`"additionalProperties":false}`
	writePostalSchema = `{"type":"object","properties":{` + typeEnum + `,"po_box":{"type":"string","maxLength":256},` +
		`"extended":{"type":"string","maxLength":256},"street":{"type":"string","maxLength":256},` +
		`"locality":{"type":"string","maxLength":256},"region":{"type":"string","maxLength":256},` +
		`"postal_code":{"type":"string","maxLength":256},"country":{"type":"string","maxLength":256}},` +
		`"additionalProperties":false}`
	contactFieldSchemas = `"name":{"type":"string","minLength":1,"maxLength":256},"structured_name":` + writeNameSchema + `,` +
		`"emails":{"type":"array","maxItems":20,"items":` + writeTypedSchema + `},` +
		`"phones":{"type":"array","maxItems":20,"items":` + writeTypedSchema + `},` +
		`"addresses":{"type":"array","maxItems":20,"items":` + writePostalSchema + `},` +
		`"organization":{"type":"string","maxLength":256},"title":{"type":"string","maxLength":256},` +
		`"birthday":{"type":"string","minLength":10,"maxLength":10},"note":{"type":"string","maxLength":4096},` +
		`"urls":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":256}}`
	addressbookWriteOut = `"addressbook":{"type":"string"},"id":{"type":"string"}`
)

func contactWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: contactsSensitivity}
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

var addressbookArgument = capability.Argument{Name: "addressbook", Required: true,
	Description: "ID of an allow-listed address book, the ID of its addressbook/ID target"}

var contactETagArgument = capability.Argument{Name: "etag", Required: true,
	Description: "Entity tag of the contact version to change, as contacts.get or contacts.list reports it"}

var contactIDArgument = capability.Argument{Name: "id", Required: true,
	Description: "Contact id as the list tool reports it: one path segment of the contact resource"}

var contactsCreate = capability.Descriptor{
	ID: Provider + ".contacts.create", Version: 1, Title: "Create an Infomaniak contact",
	Description: "Create one new contact in an allow-listed address book from structured fields. Qatlas builds " +
		"the vCard 3.0 itself and generates the UID and the resource id; nothing raw comes from an argument. One " +
		"PUT with If-None-Match: * stores it, so no existing contact can be replaced. The request is never " +
		"repeated after an unclear result",
	Tags: []string{"infomaniak", "dav", "contacts", "contact", "create"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + contactFieldSchemas +
		`},"required":["addressbook","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"created":{"type":"boolean"}},"required":["addressbook","id","uid","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{addressbookArgument}, contactFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Generated resource id, the id argument of the other contact tools"},
		{Name: "uid", Description: "Generated vCard UID"},
		{Name: "etag", Description: "Entity tag of the new contact when Infomaniak reports one"},
		{Name: "created", Description: "True when Infomaniak accepted the contact"},
	},
	Examples: []capability.Example{{Description: "Create a contact with one e-mail address",
		Arguments: json.RawMessage(`{"addressbook":"main","name":"Ada Example","emails":[{"value":"ada@example.org","type":"work"}]}`)}},
}

var contactsUpdate = capability.Descriptor{
	ID: Provider + ".contacts.update", Version: 1, Title: "Replace an Infomaniak contact",
	Description: "Replace one contact of an allow-listed address book completely by structured fields, bound to " +
		"its etag. The UID is kept, which needs one GET before the single PUT with If-Match. A contact that " +
		"holds a photo, a group card, or any other property Qatlas does not model is refused and left unchanged. " +
		"A changed etag or a failed precondition changes nothing. The request is never repeated after an unclear " +
		"result",
	Tags: []string{"infomaniak", "dav", "contacts", "contact", "update"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + idArgSchema + `,` +
		etagArgSchema + `,` + contactFieldSchemas +
		`},"required":["addressbook","id","etag","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"updated":{"type":"boolean"}},"required":["addressbook","id","uid","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{addressbookArgument, contactIDArgument, contactETagArgument},
		contactFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Contact id"},
		{Name: "uid", Description: "vCard UID of the contact, kept"},
		{Name: "etag", Description: "Entity tag of the new version when Infomaniak reports one"},
		{Name: "updated", Description: "True when Infomaniak accepted the contact"},
	},
	Examples: []capability.Example{{Description: "Rename a contact",
		Arguments: json.RawMessage(`{"addressbook":"main","id":"ada.vcf","etag":"e1","name":"Ada Example"}`)}},
}

var contactsDelete = capability.Descriptor{
	ID: Provider + ".contacts.delete", Version: 1, Title: "Delete an Infomaniak contact",
	Description: "Delete one contact of an allow-listed address book for good, bound to its etag, with one " +
		"DELETE with If-Match. A changed etag deletes nothing. The request is never repeated after an unclear result",
	Tags: []string{"infomaniak", "dav", "contacts", "contact", "delete", "permanent"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk:                  contactWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + idArgSchema + `,` +
		etagArgSchema + `},"required":["addressbook","id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookWriteOut + `,"deleted":{"type":"boolean"}},` +
		`"required":["addressbook","id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument, contactIDArgument, contactETagArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Contact id"},
		{Name: "deleted", Description: "True when Infomaniak accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a contact",
		Arguments: json.RawMessage(`{"addressbook":"main","id":"ada.vcf","etag":"e1"}`)}},
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

func decodeContactWriteArguments(op string, raw json.RawMessage) (contactWriteArguments, error) {
	var input contactWriteArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// contactTarget checks the address book, the contact id, and the etag before any secret or request.
func contactTarget(op string, resolved *config.Resolved, input contactWriteArguments, withID bool) (string, error) {
	if err := allowedAddressbook(op, resolved, input.Addressbook); err != nil {
		return "", err
	}
	if !withID {
		return "", nil
	}
	if !validCollectionID(input.ID) {
		return "", providerError(op, "the contact id must be one literal path segment as the list tool reports it")
	}
	etag, ok := dav.NormalETag(input.ETag)
	if !ok {
		return "", providerError(op, "etag must be the entity tag of the contact as the read tools report it")
	}
	return etag, nil
}

func invokeContactsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create contact"
	input, err := decodeContactWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" {
		return nil, providerError(op, "the id of a new contact is generated and takes no etag")
	}
	if _, err := contactTarget(op, resolved, input, false); err != nil {
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	id := uid + ".vcf"
	header, err := client.mutate(ctx, op, contactKind, methodPut,
		append(append([]string{}, home...), input.Addressbook, id), "", body)
	if err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: id, UID: uid, ETag: dav.ETagOf(header.Get("ETag")),
		Created: true}, nil
}

func invokeContactsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update contact"
	input, err := decodeContactWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	etag, err := contactTarget(op, resolved, input, true)
	if err != nil {
		return nil, err
	}
	card, err := dav.NewContactCard(op, input.ContactInput)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	path := append(append([]string{}, home...), input.Addressbook, input.ID)
	data, header, err := client.request(ctx, op, methodGet, path, false, "", "", 200, maxEventBytes)
	if err != nil {
		return nil, err
	}
	uid, err := server.StoredContactUID(op, data)
	if err != nil {
		return nil, err
	}
	if served := header.Get("ETag"); served != "" && dav.ETagOf(served) != etag {
		return nil, errPrecondition(op, contactKind.noun)
	}
	body, err := dav.EncodeContact(op, card, uid)
	if err != nil {
		return nil, err
	}
	out, err := client.mutate(ctx, op, contactKind, methodPut, path, etag, body)
	if err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: input.ID, UID: uid, ETag: dav.ETagOf(out.Get("ETag")),
		Updated: true}, nil
}

func invokeContactsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete contact"
	input, err := decodeContactWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	etag, err := contactTarget(op, resolved, input, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.addressbookHome(ctx, op)
	if err != nil {
		return nil, err
	}
	if _, err := client.mutate(ctx, op, contactKind, methodDelete,
		append(append([]string{}, home...), input.Addressbook, input.ID), etag, ""); err != nil {
		return nil, err
	}
	return &ContactWriteResult{Addressbook: input.Addressbook, ID: input.ID, Deleted: true}, nil
}
