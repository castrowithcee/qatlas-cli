package nextcloud

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Address book sharing: one oc:share POST to a bound address book of the identity after the read-only owner
// pre-check of the address book management tools. Nextcloud ignores an unknown user or group without an error, so the
// answer says that the change was accepted, not that the recipient exists; addressbooks.list shows the result.

const addressbookShareOut = `"addressbook":{"type":"string"},"type":{"type":"string"},"id":{"type":"string"},` +
	`"accepted":{"type":"boolean"}`

var addressbooksShare = capability.Descriptor{
	ID: Provider + ".addressbooks.share", Version: 1, Title: "Share a Nextcloud address book with a user or group",
	Description: "Share one bound address book that belongs to the identity with one existing user or group of the " +
		"instance for reading or for reading and writing; calling it again changes the access. The system address book and an address book " +
		"shared by someone else are refused, and no e-mail, team, or remote recipient and no public link can be set. " +
		"Nextcloud ignores an unknown user or group without an error, so check addressbooks.list. One read-only " +
		"pre-check of the address book follows, then one POST; the request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "addressbook", "share"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + davShareTypeSchema + `,` +
		davShareIDSchema + `,"access":{"type":"string","enum":["read","read-write"]}},` +
		`"required":["addressbook","type","id","access"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookShareOut + `,` +
		`"access":{"type":"string"}},"required":["addressbook","type","id","access","accepted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument, davShareTypeArgument, davShareIDArgument,
		{Name: "access", Required: true, Description: "read or read-write"}},
	Fields: addressbookShareFields,
	Examples: []capability.Example{{Description: "Let a group read an address book",
		Arguments: json.RawMessage(`{"addressbook":"clients","type":"groups","id":"staff","access":"read"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

var addressbooksUnshare = capability.Descriptor{
	ID: Provider + ".addressbooks.unshare", Version: 1, Title: "Revoke the share of a Nextcloud address book",
	Description: "Revoke the share of one bound address book that belongs to the identity for one user or group. The " +
		"system address book and an address book shared by someone else are refused. One read-only pre-check of the " +
		"address book follows, then one POST; the request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "contacts", "addressbook", "share"}, Provider: Provider,
	Risk: contactWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookArgSchema + `,` + davShareTypeSchema + `,` +
		davShareIDSchema + `},"required":["addressbook","type","id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + addressbookShareOut + `},` +
		`"required":["addressbook","type","id","accepted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{addressbookArgument, davShareTypeArgument, davShareIDArgument},
	Fields:    addressbookShareFields[:4],
	Examples: []capability.Example{{Description: "Revoke the share of a user",
		Arguments: json.RawMessage(`{"addressbook":"clients","type":"users","id":"bob"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

var addressbookShareFields = []capability.Field{
	{Name: "addressbook", Description: "Id of the address book"},
	{Name: "type", Description: "users or groups"},
	{Name: "id", Description: "ID of the user or group"},
	{Name: "accepted", Description: "True when Nextcloud accepted the change"},
	{Name: "access", Description: "read or read-write"},
}

// AddressbookShareResult is the result of the address book share and unshare tools.
type AddressbookShareResult struct {
	Addressbook string `json:"addressbook"`
	Type        string `json:"type"`
	ID          string `json:"id"`
	Access      string `json:"access,omitempty"`
	Accepted    bool   `json:"accepted"`
}

type addressbookShareArguments struct {
	Addressbook string `json:"addressbook"`
	Type        string `json:"type"`
	ID          string `json:"id"`
	Access      string `json:"access"`
}

func invokeAddressbooksShare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeAddressbookShare(ctx, "share address book", false, resolved, secrets, red, raw)
}

func invokeAddressbooksUnshare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeAddressbookShare(ctx, "unshare address book", true, resolved, secrets, red, raw)
}

// changeAddressbookShare checks everything locally decidable before any credential access, then reads the
// owner of the address book once and sends the one POST.
func changeAddressbookShare(ctx context.Context, op string, remove bool, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input addressbookShareArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	href, err := davSharePrincipal(op, input.Type, input.ID)
	if err != nil {
		return nil, err
	}
	body := davShareRemoveBody(href)
	if remove {
		if input.Access != "" {
			return nil, providerError(op, "revoking a share takes no access")
		}
	} else {
		if input.Access != davShareRead && input.Access != davShareReadWrite {
			return nil, providerError(op, "access must be read or read-write")
		}
		body = davShareSetBody(href, input.Access == davShareReadWrite)
	}
	client, err := openOwnAddressbook(ctx, op, resolved, secrets, red, input.Addressbook)
	if err != nil {
		return nil, err
	}
	if err := client.ownAddressbook(ctx, op, input.Addressbook); err != nil {
		return nil, err
	}
	if err := client.sendDavShare(ctx, op, client.addressbookHome(input.Addressbook), body, addressbookUncertain); err != nil {
		return nil, err
	}
	return &AddressbookShareResult{Addressbook: input.Addressbook, Type: input.Type, ID: input.ID, Access: input.Access,
		Accepted: true}, nil
}
