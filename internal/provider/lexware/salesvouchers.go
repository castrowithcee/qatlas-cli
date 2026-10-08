package lexware

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// SalesVoucher is the stable Qatlas view of one quotation, order confirmation, credit note, or delivery
// note. Everything a Lexware user typed is untrusted data. A delivery note carries no prices, so its totals
// and unit prices stay absent instead of being invented.
type SalesVoucher struct {
	ID               string           `json:"id"`
	VoucherNumber    string           `json:"voucher_number,omitempty"`
	VoucherStatus    string           `json:"voucher_status,omitempty"`
	VoucherDate      string           `json:"voucher_date,omitempty"`
	ExpirationDate   string           `json:"expiration_date,omitempty"`
	DeliveryTerms    string           `json:"delivery_terms,omitempty"`
	CreatedDate      string           `json:"created_date,omitempty"`
	UpdatedDate      string           `json:"updated_date,omitempty"`
	Version          int              `json:"version,omitempty"`
	Archived         bool             `json:"archived"`
	Language         string           `json:"language,omitempty"`
	Title            string           `json:"title,omitempty"`
	Introduction     string           `json:"introduction,omitempty"`
	Remark           string           `json:"remark,omitempty"`
	TaxType          string           `json:"tax_type,omitempty"`
	Currency         string           `json:"currency,omitempty"`
	TotalNetAmount   json.Number      `json:"total_net_amount,omitempty"`
	TotalGrossAmount json.Number      `json:"total_gross_amount,omitempty"`
	TotalTaxAmount   json.Number      `json:"total_tax_amount,omitempty"`
	Contact          *Contact         `json:"contact,omitempty"`
	Shipping         *Shipping        `json:"shipping,omitempty"`
	RelatedVouchers  []RelatedVoucher `json:"related_vouchers,omitempty"`
	LineItems        []LineItem       `json:"line_items"`
}

// Shipping is the delivery or service period of a sales voucher.
type Shipping struct {
	ShippingType    string `json:"shipping_type,omitempty"`
	ShippingDate    string `json:"shipping_date,omitempty"`
	ShippingEndDate string `json:"shipping_end_date,omitempty"`
}

// RelatedVoucher references a voucher that belongs to the same business case. Its content is not read.
type RelatedVoucher struct {
	ID            string `json:"id,omitempty"`
	VoucherNumber string `json:"voucher_number,omitempty"`
	VoucherType   string `json:"voucher_type,omitempty"`
}

// salesVoucherKind fixes one voucher type: its endpoint, its names, and the members only it carries. The
// type is never an argument; every tool has its own fixed path.
type salesVoucherKind struct {
	path, resource, op, tool  string
	expiration, deliveryTerms bool
}

var (
	quotationKind = salesVoucherKind{path: "/v1/quotations/", resource: "quotation", op: "get quotation",
		tool: "quotations", expiration: true}
	orderConfirmationKind = salesVoucherKind{path: "/v1/order-confirmations/", resource: "order confirmation",
		op: "get order confirmation", tool: "orderconfirmations", deliveryTerms: true}
	creditNoteKind = salesVoucherKind{path: "/v1/credit-notes/", resource: "credit note",
		op: "get credit note", tool: "creditnotes"}
	deliveryNoteKind = salesVoucherKind{path: "/v1/delivery-notes/", resource: "delivery note",
		op: "get delivery note", tool: "deliverynotes"}
)

var (
	quotationsGet         = salesVoucherDescriptor(quotationKind)
	orderConfirmationsGet = salesVoucherDescriptor(orderConfirmationKind)
	creditNotesGet        = salesVoucherDescriptor(creditNoteKind)
	deliveryNotesGet      = salesVoucherDescriptor(deliveryNoteKind)
)

// salesVoucherDescriptor builds the read tool of one voucher type. Output schema and fields differ only in
// the members the type carries.
func salesVoucherDescriptor(kind salesVoucherKind) capability.Descriptor {
	name := kind.resource
	properties := `"id":{"type":"string"},"voucher_number":{"type":"string"},"voucher_status":{"type":"string"},` +
		`"voucher_date":{"type":"string"},`
	fields := []capability.Field{
		{Name: "id", Description: "Identifier of the " + name},
		{Name: "voucher_number", Description: "Number of the " + name},
		{Name: "voucher_status", Description: "Provider status, for example draft or open"},
		{Name: "voucher_date", Description: "Date of the " + name},
	}
	if kind.expiration {
		properties += `"expiration_date":{"type":"string"},`
		fields = append(fields, capability.Field{Name: "expiration_date", Description: "Date until the quotation is valid"})
	}
	if kind.deliveryTerms {
		properties += `"delivery_terms":{"type":"string"},`
		fields = append(fields, capability.Field{Name: "delivery_terms", Description: "Delivery terms, untrusted data"})
	}
	properties += `"created_date":{"type":"string"},"updated_date":{"type":"string"},"version":{"type":"integer"},` +
		`"archived":{"type":"boolean"},"language":{"type":"string"},"title":{"type":"string"},` +
		`"introduction":{"type":"string"},"remark":{"type":"string"},"tax_type":{"type":"string"},` +
		`"currency":{"type":"string"},"total_net_amount":{"type":"number"},"total_gross_amount":{"type":"number"},` +
		`"total_tax_amount":{"type":"number"},` +
		`"contact":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"supplement":{"type":"string"},"street":{"type":"string"},"zip":{"type":"string"},` +
		`"city":{"type":"string"},"country_code":{"type":"string"}},"additionalProperties":false},` +
		`"shipping":{"type":"object","properties":{"shipping_type":{"type":"string"},` +
		`"shipping_date":{"type":"string"},"shipping_end_date":{"type":"string"}},"additionalProperties":false},` +
		`"related_vouchers":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"voucher_number":{"type":"string"},"voucher_type":{"type":"string"}},"additionalProperties":false}},` +
		`"line_items":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"quantity":{"type":"number"},"unit_name":{"type":"string"},` +
		`"unit_net_amount":{"type":"number"},"unit_gross_amount":{"type":"number"},` +
		`"tax_rate_percentage":{"type":"number"},"discount_percentage":{"type":"number"},` +
		`"amount":{"type":"number"}},"additionalProperties":false}}`
	fields = append(fields,
		capability.Field{Name: "created_date", Description: "Creation timestamp"},
		capability.Field{Name: "updated_date", Description: "Last change timestamp"},
		capability.Field{Name: "version", Description: "Version counter reported by Lexware"},
		capability.Field{Name: "archived", Description: "True when the " + name + " is archived in Lexware"},
		capability.Field{Name: "language", Description: "Document language"},
		capability.Field{Name: "title", Description: "Document title, untrusted data"},
		capability.Field{Name: "introduction", Description: "Introductory text, untrusted data"},
		capability.Field{Name: "remark", Description: "Closing text, untrusted data"},
		capability.Field{Name: "tax_type", Description: "Tax condition, for example net or gross; absent when the provider reports none"},
		capability.Field{Name: "currency", Description: "Currency of the totals; absent without prices"},
		capability.Field{Name: "total_net_amount", Description: "Total net amount; absent without prices"},
		capability.Field{Name: "total_gross_amount", Description: "Total gross amount; absent without prices"},
		capability.Field{Name: "total_tax_amount", Description: "Total tax amount; absent without prices"},
		capability.Field{Name: "contact", Description: "Recipient name and address, untrusted data"},
		capability.Field{Name: "shipping", Description: "Delivery or service type and period"},
		capability.Field{Name: "related_vouchers", Description: "Vouchers of the same business case: id, voucher_number, voucher_type; their content is not read"},
		capability.Field{Name: "line_items", Description: "Positions, untrusted data; a delivery note carries no prices"},
	)
	return capability.Descriptor{
		ID:          Provider + "." + kind.tool + ".get",
		Version:     1,
		Title:       "Get a Lexware " + name,
		Description: "Read one " + name + " of an explicit Lexware Office connection by its identifier",
		Tags:        []string{"lexware", kind.tool, "get", "accounting"},
		Risk:        lexwareReadRisk,
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
			`"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + properties + `},` +
			`"required":["id","archived","line_items"],"additionalProperties":false}`),
		Arguments: []capability.Argument{{Name: "id", Required: true,
			Description: "Identifier of the " + name + " as a UUID, as returned by lexware.voucherlist.list"}},
		Fields: fields,
		Examples: []capability.Example{{
			Description: "Read one " + name + " by the identifier a voucher list reported",
			Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
		}},
	}
}

// salesVoucherHandler validates the identifier before any secret is resolved or request is built.
func salesVoucherHandler(kind salesVoucherKind) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var arguments struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, providerError(kind.op, "the validated arguments could not be read")
		}
		if !validUUID(arguments.ID) {
			return nil, providerError(kind.op, "the "+kind.resource+" identifier must be a UUID")
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return client.getSalesVoucher(ctx, kind, arguments.ID)
	}
}

// GetQuotation reads exactly the quotation of a validated identifier.
func (c *Client) GetQuotation(ctx context.Context, id string) (*SalesVoucher, error) {
	return c.getSalesVoucher(ctx, quotationKind, id)
}

// GetOrderConfirmation reads exactly the order confirmation of a validated identifier.
func (c *Client) GetOrderConfirmation(ctx context.Context, id string) (*SalesVoucher, error) {
	return c.getSalesVoucher(ctx, orderConfirmationKind, id)
}

// GetCreditNote reads exactly the credit note of a validated identifier.
func (c *Client) GetCreditNote(ctx context.Context, id string) (*SalesVoucher, error) {
	return c.getSalesVoucher(ctx, creditNoteKind, id)
}

// GetDeliveryNote reads exactly the delivery note of a validated identifier.
func (c *Client) GetDeliveryNote(ctx context.Context, id string) (*SalesVoucher, error) {
	return c.getSalesVoucher(ctx, deliveryNoteKind, id)
}

func (c *Client) getSalesVoucher(ctx context.Context, kind salesVoucherKind, id string) (*SalesVoucher, error) {
	if !validUUID(id) {
		return nil, providerError(kind.op, "the "+kind.resource+" identifier must be a UUID")
	}
	var raw invoiceJSON
	if err := c.get(ctx, kind.op, kind.resource, kind.path+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: kind.op,
			Message: "Lexware answered with a different " + kind.resource + " than the requested one",
		}
	}
	voucher := raw.normalize()
	// The provider's answer is untrusted: a member the type does not define is dropped, not forwarded.
	if !kind.expiration {
		voucher.ExpirationDate = ""
	}
	if !kind.deliveryTerms {
		voucher.DeliveryTerms = ""
	}
	return voucher, nil
}
