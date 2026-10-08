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

// bookkeepingSensitivity classifies results as bookkeeping vouchers of the configured organization.
const bookkeepingSensitivity = "lexware-bookkeeping-data"

var vouchersGet = capability.Descriptor{
	ID:      Provider + ".vouchers.get",
	Version: 1,
	Title:   "Get a Lexware bookkeeping voucher",
	Description: "Read one bookkeeping voucher of an explicit Lexware Office connection by its identifier, " +
		"with its positions and file identifiers",
	Tags:     []string{"lexware", "vouchers", "get", "bookkeeping", "accounting"},
	Risk:     capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: bookkeepingSensitivity},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"type":{"type":"string"},"voucher_status":{"type":"string"},` +
		`"voucher_number":{"type":"string"},"voucher_date":{"type":"string"},"shipping_date":{"type":"string"},` +
		`"due_date":{"type":"string"},"total_gross_amount":{"type":"number"},"total_tax_amount":{"type":"number"},` +
		`"tax_type":{"type":"string"},"use_collective_contact":{"type":"boolean"},"contact_id":{"type":"string"},` +
		`"remark":{"type":"string"},"voucher_items":{"type":"array","items":{"type":"object","properties":{` +
		`"amount":{"type":"number"},"tax_amount":{"type":"number"},"tax_rate_percent":{"type":"number"},` +
		`"category_id":{"type":"string"}},"additionalProperties":false}},` +
		`"files":{"type":"array","items":{"type":"string"}},"created_date":{"type":"string"},` +
		`"updated_date":{"type":"string"},"version":{"type":"integer"}},` +
		`"required":["id","voucher_items","files"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Bookkeeping voucher identifier as a UUID, as returned by lexware.voucherlist.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Voucher identifier"},
		{Name: "type", Description: "Voucher type, for example salesinvoice or purchaseinvoice"},
		{Name: "voucher_status", Description: "Provider status of the voucher"},
		{Name: "voucher_number", Description: "Voucher number"},
		{Name: "voucher_date", Description: "Voucher date"},
		{Name: "shipping_date", Description: "Delivery or service date"},
		{Name: "due_date", Description: "Date the payment is due"},
		{Name: "total_gross_amount", Description: "Total gross amount"},
		{Name: "total_tax_amount", Description: "Total tax amount"},
		{Name: "tax_type", Description: "Tax condition of the voucher, for example net or gross"},
		{Name: "use_collective_contact", Description: "True when the voucher is booked to a collective contact"},
		{Name: "contact_id", Description: "Identifier of the contact the voucher belongs to"},
		{Name: "remark", Description: "Free-text remark, untrusted data"},
		{Name: "voucher_items", Description: "Positions: amount, tax_amount, tax_rate_percent, category_id"},
		{Name: "files", Description: "Identifiers of the files attached to the voucher; their content is not read"},
		{Name: "created_date", Description: "Creation timestamp"},
		{Name: "updated_date", Description: "Last change timestamp"},
		{Name: "version", Description: "Version counter reported by Lexware"},
	},
	Examples: []capability.Example{{
		Description: "Read one bookkeeping voucher by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

func invokeVouchersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get voucher", "the validated arguments could not be read")
	}
	if !validUUID(arguments.ID) {
		return nil, providerError("get voucher", "the voucher identifier must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetVoucher(ctx, arguments.ID)
}

// Voucher is the stable Qatlas view of one bookkeeping voucher. Amounts keep the provider's exact digits;
// the remark is untrusted data.
type Voucher struct {
	ID                   string        `json:"id"`
	Type                 string        `json:"type,omitempty"`
	VoucherStatus        string        `json:"voucher_status,omitempty"`
	VoucherNumber        string        `json:"voucher_number,omitempty"`
	VoucherDate          string        `json:"voucher_date,omitempty"`
	ShippingDate         string        `json:"shipping_date,omitempty"`
	DueDate              string        `json:"due_date,omitempty"`
	TotalGrossAmount     json.Number   `json:"total_gross_amount,omitempty"`
	TotalTaxAmount       json.Number   `json:"total_tax_amount,omitempty"`
	TaxType              string        `json:"tax_type,omitempty"`
	UseCollectiveContact bool          `json:"use_collective_contact,omitempty"`
	ContactID            string        `json:"contact_id,omitempty"`
	Remark               string        `json:"remark,omitempty"`
	VoucherItems         []VoucherItem `json:"voucher_items"`
	Files                []string      `json:"files"`
	CreatedDate          string        `json:"created_date,omitempty"`
	UpdatedDate          string        `json:"updated_date,omitempty"`
	Version              int           `json:"version,omitempty"`
}

// VoucherItem is one position of a bookkeeping voucher.
type VoucherItem struct {
	Amount         json.Number `json:"amount,omitempty"`
	TaxAmount      json.Number `json:"tax_amount,omitempty"`
	TaxRatePercent json.Number `json:"tax_rate_percent,omitempty"`
	CategoryID     string      `json:"category_id,omitempty"`
}

// GetVoucher reads exactly the bookkeeping voucher of a validated identifier.
func (c *Client) GetVoucher(ctx context.Context, id string) (*Voucher, error) {
	const op = "get voucher"
	if !validUUID(id) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	var raw struct {
		ID                   string      `json:"id"`
		Type                 string      `json:"type"`
		VoucherStatus        string      `json:"voucherStatus"`
		VoucherNumber        string      `json:"voucherNumber"`
		VoucherDate          string      `json:"voucherDate"`
		ShippingDate         string      `json:"shippingDate"`
		DueDate              string      `json:"dueDate"`
		TotalGrossAmount     json.Number `json:"totalGrossAmount"`
		TotalTaxAmount       json.Number `json:"totalTaxAmount"`
		TaxType              string      `json:"taxType"`
		UseCollectiveContact bool        `json:"useCollectiveContact"`
		ContactID            string      `json:"contactId"`
		Remark               string      `json:"remark"`
		VoucherItems         []struct {
			Amount         json.Number `json:"amount"`
			TaxAmount      json.Number `json:"taxAmount"`
			TaxRatePercent json.Number `json:"taxRatePercent"`
			CategoryID     string      `json:"categoryId"`
		} `json:"voucherItems"`
		Files       []string `json:"files"`
		CreatedDate string   `json:"createdDate"`
		UpdatedDate string   `json:"updatedDate"`
		Version     int      `json:"version"`
	}
	if err := c.get(ctx, op, resourceVoucher, "/v1/vouchers/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different voucher than the requested one",
		}
	}
	items := make([]VoucherItem, 0, len(raw.VoucherItems))
	for _, item := range raw.VoucherItems {
		items = append(items, VoucherItem(item))
	}
	files := raw.Files
	if files == nil {
		files = []string{}
	}
	return &Voucher{
		ID: raw.ID, Type: raw.Type, VoucherStatus: raw.VoucherStatus, VoucherNumber: raw.VoucherNumber,
		VoucherDate: raw.VoucherDate, ShippingDate: raw.ShippingDate, DueDate: raw.DueDate,
		TotalGrossAmount: raw.TotalGrossAmount, TotalTaxAmount: raw.TotalTaxAmount, TaxType: raw.TaxType,
		UseCollectiveContact: raw.UseCollectiveContact, ContactID: raw.ContactID, Remark: raw.Remark,
		VoucherItems: items, Files: files, CreatedDate: raw.CreatedDate, UpdatedDate: raw.UpdatedDate,
		Version: raw.Version,
	}, nil
}
