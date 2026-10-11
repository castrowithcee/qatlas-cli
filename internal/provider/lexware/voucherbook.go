package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	notUncheckedMessage = "only a voucher with the status unchecked can be booked"
	notOpenMessage      = "only a booked voucher with the status open can be updated"
)

// Booking by id is a single PUT, recording a booked voucher is a POST; the tool covers both, so it carries
// the weaker idempotency of the two.
var voucherBookRisk = capability.Risk{
	Effect:          capability.EffectUpdate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: bookkeepingSensitivity,
}

var voucherUpdateRisk = capability.Risk{
	Effect:          capability.EffectUpdate,
	Idempotency:     capability.IdempotencyIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: bookkeepingSensitivity,
}

const voucherRequiredFields = `"type","voucher_date","total_gross_amount","total_tax_amount","tax_type","voucher_items"`

var vouchersBook = capability.Descriptor{
	ID: Provider + ".vouchers.book", Version: 1, Title: "Book a Lexware bookkeeping voucher",
	Description: "Book a bookkeeping voucher for good: the status open cannot be reverted through the Lexware API. " +
		"Two exclusive forms. With id alone, an existing voucher with the status unchecked is booked and " +
		"otherwise left as it is. Without id, a new booked voucher is recorded from the voucher fields as in " +
		"lexware.vouchers.create, and tax_type net is allowed. Attached files are always kept. Offered only by " +
		"a connection whose tools list names it",
	Tags:     []string{"lexware", "vouchers", "book", "bookkeeping", "accounting"},
	Provider: Provider, Risk: voucherBookRisk, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,` + voucherWriteProperties +
		`},"minProperties":1,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Identifier of an unchecked voucher as a UUID; book it and send no other argument. Omit it to record a new booked voucher"},
	}, voucherWriteArguments(false, "gross or net; required without id")...),
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the voucher"},
		{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
		{Name: "updated_date", Description: "Last change timestamp reported by Lexware"},
		{Name: "version", Description: "Version counter after the write, reported by Lexware"},
	},
	Examples: []capability.Example{{
		Description: "Book an unchecked voucher",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

var vouchersUpdate = capability.Descriptor{
	ID: Provider + ".vouchers.update", Version: 1, Title: "Update a booked Lexware bookkeeping voucher",
	Description: "Change a booked bookkeeping voucher (status open): Qatlas reads it, replaces the voucher fields " +
		"with the given set and writes it back with the version it read; positions are replaced as a whole and " +
		"an omitted optional field is removed. Attached files and other members are always kept. A voucher " +
		"with another status is refused, a change made by someone else in between is a conflict. Offered only " +
		"by a connection whose tools list names it",
	Tags:     []string{"lexware", "vouchers", "update", "bookkeeping", "accounting"},
	Provider: Provider, Risk: voucherUpdateRisk, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,` + voucherWriteProperties +
		`},"required":["id",` + voucherRequiredFields + `],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Identifier of the booked voucher as a UUID", Required: true},
	}, voucherWriteArguments(true, "gross or net")...),
	Fields: vouchersBook.Fields,
	Examples: []capability.Example{{
		Description: "Replace the fields of a booked voucher",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","type":"purchaseinvoice",` +
			`"voucher_date":"2026-05-14","total_gross_amount":119,"total_tax_amount":19,"tax_type":"net",` +
			`"use_collective_contact":true,"voucher_items":[{"amount":100,"tax_amount":19,"tax_rate_percent":19,` +
			`"category_id":"11111111-2222-3333-4444-555555555555"}]}`),
	}},
}

// voucherWithID is the voucher schema plus the identifier of an existing voucher.
type voucherWithID struct {
	ID string `json:"id"`
	voucherInput
}

func decodeVoucherWithID(op string, raw json.RawMessage) (voucherWithID, error) {
	var input voucherWithID
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeVouchersBook(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "book voucher"
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	input, err := decodeVoucherWithID(op, raw)
	if err != nil {
		return nil, err
	}
	if _, byID := members["id"]; byID {
		if len(members) != 1 {
			return nil, providerError(op, "name either id alone or the voucher fields, not both")
		}
		if !validUUID(input.ID) {
			return nil, providerError(op, "the voucher identifier must be a UUID")
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return client.BookVoucher(ctx, input.ID)
	}
	if err := input.validate(voucherStatusOpen); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RecordBookedVoucher(ctx, input.voucherInput)
}

func invokeVouchersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update voucher"
	input, err := decodeVoucherWithID(op, raw)
	if err != nil {
		return nil, err
	}
	if !validUUID(input.ID) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	if err := input.validate(voucherStatusOpen); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateVoucher(ctx, input.ID, input.voucherInput)
}

// voucherStatusOf reads the status of a voucher as received.
func voucherStatusOf(object rawObject) string {
	var status string
	_ = json.Unmarshal(object["voucherStatus"], &status)
	return status
}

// BookVoucher books an unchecked voucher: one GET, then one PUT that changes only the status.
func (c *Client) BookVoucher(ctx context.Context, id string) (*createResult, error) {
	const op = "book voucher"
	if !validUUID(id) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	return c.updateObject(ctx, op, resourceVoucher, "/v1/vouchers", id, voucherChanged, "a voucher",
		func(object rawObject) error {
			if voucherStatusOf(object) != voucherStatusUnchecked {
				return providerError(op, notUncheckedMessage)
			}
			return object.set("voucherStatus", voucherStatusOpen)
		})
}

// RecordBookedVoucher posts one voucher with the status open: exactly one request, never repeated.
func (c *Client) RecordBookedVoucher(ctx context.Context, input voucherInput) (*createResult, error) {
	const op = "book voucher"
	if err := input.validate(voucherStatusOpen); err != nil {
		return nil, providerError(op, err.Error())
	}
	return c.postObject(ctx, op, "/v1/vouchers", voucherPayload(voucherStatusOpen, input),
		voucherMayExist, "a voucher")
}

// UpdateVoucher replaces the schema fields of a booked voucher. Members outside the schema, above all the
// attached files, stay as read, because a PUT deletes every file it leaves out.
func (c *Client) UpdateVoucher(ctx context.Context, id string, input voucherInput) (*createResult, error) {
	const op = "update voucher"
	if !validUUID(id) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	if err := input.validate(voucherStatusOpen); err != nil {
		return nil, providerError(op, err.Error())
	}
	return c.updateObject(ctx, op, resourceVoucher, "/v1/vouchers", id, voucherChanged, "a voucher",
		func(object rawObject) error {
			if voucherStatusOf(object) != voucherStatusOpen {
				return providerError(op, notOpenMessage)
			}
			payload := voucherPayload(voucherStatusOpen, input)
			// An optional field the input leaves out must not survive from the read voucher.
			for _, key := range []string{"voucherNumber", "dueDate", "contactId", "useCollectiveContact"} {
				if _, given := payload[key]; !given {
					delete(object, key)
				}
			}
			for key, value := range payload {
				if err := object.set(key, value); err != nil {
					return errors.New("encode")
				}
			}
			return nil
		})
}
